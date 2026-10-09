package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// 静默失败收口 (看板 rG73bo 第 1 条): JSX 子节点位置写了**响应式对象本身**。
//
// ## 陷阱的两种形态 (别混淆, 它们的检测手段完全不同)
//
// Gox 有两套响应式 API, 写错时的形态和可行的检测手段都不一样:
//
//   - `obs()` / `computed()` (GetX 风格, 全局函数): 返回的是 **Observable /
//     Computed 对象**, 不可调用。`{bad}` 会被 ToString 成 `Rx<"init">` 这种
//     乱码般的静态文本, 之后永不刷新 —— **对象还在, 运行期判得出**, 本文件的
//     三个测试钉的就是这一类。
//
//   - `createSignal()` (gx/solid): getter 本身就是**函数**, 所以 `{sig}` 是对的
//     (会被当函数子节点订阅), 写错的是 `{sig()}` —— 它求值成普通字符串,
//     与手写 "init" 在类型上**完全无法区分**, 运行期**判不出来**。这一类只能
//     靠 gox lint 的静态分析 (cmd/gox/cmd_lint.go 的 snapshot-child 规则)。
//
// 实测 (改 signal 之后):
//
//	{n}      → "A" → "Z"   更新 (getter 是函数, 走 wireReactiveChild)
//	{n()}    → "A" → "A"   静默失败 (求值为静态字符串)
//	{() => n()} → "A" → "Z"  更新
//
// ## 断言为什么这样写
//
// 只断言"发了警告"是不够的 —— 那只能证明多打了一行字, 证明不了它指向的确实是
// 个 bug。所以每个用例都同时钉住: 告警文案带定位信息、正确写法不误报、以及
// **告警背后的后果真实存在** (写错的那条纹丝不动)。
//
// 生产模式的行为由 TestUncalledReactiveChildSilentInProductionMode 单独钉住:
// dev 与生产跑出来的界面若不一致, "dev 下测过"就失去意义了。

// jsxSnapshotScript 是最小复现: 三个 text 并列, 前两个写错 (响应式对象本身),
// 第三个是对的 (函数子节点), 兼作"signal 变更机制在本用例里是活的"的对照组。
const jsxSnapshotScript = `
	import { createSignal } from "gx/solid";
	import { h, render } from "gx/gfx";

	const [good, setGood] = createSignal("init");
	const bad = obs("init");
	const worse = computed(() => bad.value + "!");

	render(
		<window title="snap" width={360} height={140}>
			<column>
				<text>{bad}</text>
				<text>{worse}</text>
				<text>{() => good()}</text>
			</column>
		</window>
	);

	// 钩子必须换名: 顶层 const 在脚本模式下是**全局**变量, globalThis.setGood
	// = (v) => setGood(v) 会把模块级的 setGood 覆盖成箭头函数自己 ⇒ 无限递归
	// (Gox 专有陷阱, 已进 lint 规则的观察清单)。
	globalThis.pushGood = (v) => setGood(v);
	globalThis.pushBad = (v) => { bad.value = v; };
`

// reactiveChildWarns 数警告环里"未调用的响应式子节点"的告警。
func reactiveChildWarns() []string {
	var out []string
	for _, w := range warnSnapshot() {
		if strings.Contains(w.Text, "个子节点是") {
			out = append(out, w.Text)
		}
	}
	return out
}

func TestUncalledReactiveChildWarnsInDevMode(t *testing.T) {
	// 警告环与去重表都是进程级的: 先清掉, 免得被别的用例污染
	resetWarnRing()
	resetReactiveWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetReactiveWarns() })

	v, fake := evalUI(t, jsxSnapshotScript)

	var stale []string
	runPumpSteps(t, v, fake, []func(){
		// 0) 建树: 两条告警已发出, 三个 text 都渲染出了内容
		func() {
			texts := viewTexts(uiRoot(t))
			if len(texts) != 3 {
				t.Fatalf("文本节点数 = %d, want 3 (实际 = %v)", len(texts), texts)
			}
			stale = texts

			warns := reactiveChildWarns()
			if len(warns) != 2 {
				t.Fatalf("告警条数 = %d, want 2 (只有写错的两条该告警, 正确写法不许"+
					"误报; 实际 = %v)", len(warns), warns)
			}
			// 文案必须带定位信息 —— "你传了个响应式对象"这种话等于没说
			for _, frag := range []string{"<text> 的第 0 个子节点是", "之后界面永不刷新"} {
				if !strings.Contains(warns[0], frag) {
					t.Fatalf("告警文案缺少 %q (实际 = %q)", frag, warns[0])
				}
			}
			// 四种响应式容器的 Type() 都是 OBSERVABLE_OBJ, 文案要细分到具体种类
			kinds := strings.Join(warns, "\n")
			for _, kind := range []string{"obs() 信号对象", "computed() 计算对象"} {
				if !strings.Contains(kinds, kind) {
					t.Fatalf("告警未按类型细分, 缺 %q (实际 = %v)", kind, warns)
				}
			}
		},
		// 1) 改 signal
		func() {
			callGlobalFn(t, v, "pushGood", object.NewString("changed"))
			callGlobalFn(t, v, "pushBad", object.NewString("changed"))
		},
		// 2) 后果: 写错的两条纹丝不动, 写对的那条跟着变
		func() {
			texts := viewTexts(uiRoot(t))
			if len(texts) != 3 {
				t.Fatalf("改 signal 后文本节点数 = %d, want 3", len(texts))
			}
			if texts[2] != "changed" {
				t.Fatalf("正确写法 ({() => good()}) 的文本 = %q, want \"changed\" —— "+
					"signal 变更与 effect 重跑在本用例里没生效, 那下面两条断言也就"+
					"没有说服力了", texts[2])
			}
			for i := 0; i < 2; i++ {
				if texts[i] != stale[i] {
					t.Fatalf("写错的第 %d 条从 %q 变成了 %q —— 它本该**永不刷新** "+
						"(这正是这个陷阱的全部危害)", i, stale[i], texts[i])
				}
			}
		},
	})
}

// TestUncalledReactiveChildSilentInProductionMode 钉住另一半约定: 生产模式
// **逐字不变** —— 既不告警, 渲染结果也与 dev 模式一致。
func TestUncalledReactiveChildSilentInProductionMode(t *testing.T) {
	resetWarnRing()
	resetReactiveWarns()
	SetDevMode(false) // 默认即是, 显式写出以免被上个用例的状态漏过来
	t.Cleanup(resetReactiveWarns)

	v, fake := evalUI(t, jsxSnapshotScript)

	var stale []string
	runPumpSteps(t, v, fake, []func(){
		func() {
			stale = viewTexts(uiRoot(t))
			if len(stale) != 3 {
				t.Fatalf("文本节点数 = %d, want 3", len(stale))
			}
			if warns := reactiveChildWarns(); len(warns) != 0 {
				t.Fatalf("生产模式不该告警, 实际 = %v", warns)
			}
		},
		func() {
			callGlobalFn(t, v, "pushGood", object.NewString("changed"))
			callGlobalFn(t, v, "pushBad", object.NewString("changed"))
		},
		func() {
			texts := viewTexts(uiRoot(t))
			if texts[2] != "changed" {
				t.Fatalf("正确写法的文本 = %q, want \"changed\"", texts[2])
			}
			for i := 0; i < 2; i++ {
				if texts[i] != stale[i] {
					t.Fatalf("生产模式下写错的第 %d 条也跟着变了 (%q → %q) —— dev 与"+
						"生产的渲染语义必须一致", i, stale[i], texts[i])
				}
			}
		},
	})
}

// TestUncalledReactiveChildWarnsOncePerSite 钉住去重: 响应式子节点换代时会反复
// 走 wireChild, 不去重会在长列表里刷出成百上千条同内容告警, 把环形缓冲
// (64 条) 冲干净 —— 那等于把别的诊断也一起消音了。
func TestUncalledReactiveChildWarnsOncePerSite(t *testing.T) {
	resetWarnRing()
	resetReactiveWarns()
	SetDevMode(true)
	t.Cleanup(func() { SetDevMode(false); resetReactiveWarns() })

	// 用一个 each 列表反复重建同一处模板: 每次重建都过一遍 wireChild
	evalUI(t, `
		import { h, render } from "gx/gfx";

		const rows = obs(["a", "b", "c", "d", "e"]);
		render(
			<window title="once" width={360} height={200}>
				<column>
					<view each={() => rows.items}>
						{(row, i) => <text>{row}</text>}
					</view>
				</column>
			</window>
		);
	`)

	if n := len(reactiveChildWarns()); n > 1 {
		t.Fatalf("同一处模板的告警条数 = %d, want ≤1 (去重失效, 实际 = %v)",
			n, reactiveChildWarns())
	}
}
