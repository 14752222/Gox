package gfx

// ===== model 指令 (受控组件的双向绑定) 的全链路测试 =====
//
// 这条指令的卖点是"一条 props 顶两条", 所以断言必须**两个方向都走真事件**:
//   - 写方向: 点/敲 → 控件派发 → model 写回 signal。这是它存在的理由,
//     不能只断言"Props 里多了个键" —— 键补错了照样有键;
//   - 读方向: 从 Go 侧改 signal → 控件的显示必须跟着变。这一条恰好验证了
//     补上去的 value/checked 是**函数**(响应式 prop) 而不是快照 ——
//     快照正是 model 要消灭的那个坑。
//
// 驱动纪律与 view_test.go 同源:
//   - 从 Go 侧调脚本函数必须在 RunTimersWithPump 执行期内 (否则回调桥静默
//     返回 undefined), 所以信号转发都放在 pump 轮次里;
//   - **导出钩子必须另起名**: `globalThis.setDraft = (v) => setDraft(v)` 与顶层
//     `const setDraft` 同名会自我覆盖 —— 箭头调到自己, 无限递归只在 Go 侧
//     callbackError 里留一句 (callGlobalFn 不读它), 表现是"写入静默无效"。
//     本文件一律用 writeXxx 命名, 并且写完都要断言 signal 本身, 让踩坑时
//     失败在正确的断言上。

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// lineNodes 返回"标签为 label 的那一行"里所有 tag 节点 (一行里可能有多个同类
// 控件, 例如 radio ×2) —— 按内容定位, 不数下标, 于是演示脚本增删一行不会把
// 测试改成脆的。
func lineNodes(t *testing.T, root *GuiNode, label, tag string) []*GuiNode {
	t.Helper()
	for _, row := range findAll(root, "row") {
		if !textContainsAny(row, label) {
			continue
		}
		if ns := findAll(row, tag); len(ns) > 0 {
			return ns
		}
	}
	t.Fatalf("找不到标签 %q 那一行里的 %s", label, tag)
	return nil
}

// lineIn 是 lineNodes 的第一个 (单元件行的常用写法)。
func lineIn(t *testing.T, root *GuiNode, label, tag string) *GuiNode {
	t.Helper()
	return lineNodes(t, root, label, tag)[0]
}

// modelStr / modelBool / modelNum 调脚本里的取值钩子并断言类型 (helpers_test.go
// 的 globalStr 读的是全局变量, 这里读的是调用结果)。
func modelStr(t *testing.T, v *vm.VM, fn string) string {
	t.Helper()
	res := callGlobalFn(t, v, fn)
	if res == nil {
		t.Fatalf("%s() 返回 nil (未定义/未执行)", fn)
	}
	s, ok := res.(*object.String)
	if !ok {
		t.Fatalf("%s() 返回 %s, want string", fn, res.Type())
	}
	return s.Value
}

func modelBool(t *testing.T, v *vm.VM, fn string) bool {
	t.Helper()
	res := callGlobalFn(t, v, fn)
	if res == nil {
		t.Fatalf("%s() 返回 nil (未定义/未执行)", fn)
	}
	b, ok := res.(*object.Boolean)
	if !ok {
		t.Fatalf("%s() 返回 %s, want boolean", fn, res.Type())
	}
	return b.Value
}

func modelNum(t *testing.T, v *vm.VM, fn string) float64 {
	t.Helper()
	res := callGlobalFn(t, v, fn)
	if res == nil {
		t.Fatalf("%s() 返回 nil (未定义/未执行)", fn)
	}
	n, ok := res.(*object.Number)
	if !ok {
		t.Fatalf("%s() 返回 %s, want number", fn, res.Type())
	}
	return n.Value
}

// focusInput 点一下输入框让它拿到键盘焦点 (按键只投给焦点节点)。
func focusInput(fake *fakeSurface, in *GuiNode) {
	fake.push(Event{Kind: EventMouseUp, X: in.Box.X + 4, Y: in.Box.Y + in.Box.H/2})
}

// TestModelInputBothDirections input 的读写两个方向。
// 写方向用真按键 (h → i → Backspace), 读方向从 Go 侧改 signal。
func TestModelInputBothDirections(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [draft, setDraft] = createSignal("");
		render(
			<window title="model" width={360} height={120}>
				<column gap={6}>
					<input width={200} model={draft} />
					<text>{(draft)}</text>
				</column>
			</window>
		);
		globalThis.getDraft = () => draft();
		globalThis.writeDraft = (v) => setDraft(v);
	`)

	runPumpSteps(t, v, fake, []func(){
		// 0) 初值空
		func() {
			if got := modelStr(t, v, "getDraft"); got != "" {
				t.Fatalf("初值 = %q, want 空", got)
			}
			focusInput(fake, findFirst(uiRoot(t), "input"))
		},
		// 1) 敲 h
		func() {
			in := findFirst(uiRoot(t), "input")
			if !in.focused {
				t.Fatalf("点击后 input 未获焦")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "h"})
		},
		// 2) 写方向: signal 已跟着走 (model 补的 onInput 生效), 镜像文本也更新
		func() {
			if got := modelStr(t, v, "getDraft"); got != "h" {
				t.Fatalf("敲 h 后 signal = %q, want \"h\" (model 的写回没生效)", got)
			}
			if !textContainsAny(uiRoot(t), "h") {
				t.Fatalf("镜像文本没更新")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "i"})
		},
		func() {
			if got := modelStr(t, v, "getDraft"); got != "hi" {
				t.Fatalf("敲 i 后 signal = %q, want \"hi\"", got)
			}
		},
		// 3) 读方向: 从 Go 侧改 signal, 输入框显示必须跟着变
		//    (快照实现会停在上一个值 —— 这正是 model 要消灭的坑)
		func() { callGlobalFn(t, v, "writeDraft", object.NewString("zz")) },
		func() {
			if got := modelStr(t, v, "getDraft"); got != "zz" {
				t.Fatalf("外部写入后 signal = %q, want \"zz\"", got)
			}
			in := findFirst(uiRoot(t), "input")
			if got := in.inputValue(); got != "zz" {
				t.Fatalf("外部改 signal 后输入框显示 = %q, want \"zz\" (读方向不是响应式的)", got)
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Backspace"})
		},
		// 4) 写方向再来一次: 编辑基于外部写入后的新值
		func() {
			if got := modelStr(t, v, "getDraft"); got != "z" {
				t.Fatalf("Backspace 后 signal = %q, want \"z\"", got)
			}
			if got := findFirst(uiRoot(t), "input").inputValue(); got != "z" {
				t.Fatalf("Backspace 后显示 = %q, want \"z\"", got)
			}
		},
	})
}

// TestModelCheckboxSwitchRadio 布尔控件 (checkbox / switch) 与 radio 组。
// checkbox/switch 的 onClick 没有载荷 ⇒ 新值是"当前值取反", 这条只有双向绑定
// 才写得顺; radio 的 checked 是派生的 (model() === value), 互斥天然成立。
func TestModelCheckboxSwitchRadio(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [agree, setAgree] = createSignal(false);
		const [dark, setDark] = createSignal(false);
		const [plan, setPlan] = createSignal("free");
		render(
			<window title="model" width={400} height={200}>
				<column gap={8}>
					<checkbox model={agree} />
					<switch model={dark} />
					<radio model={plan} value="free" />
					<radio model={plan} value="pro" />
				</column>
			</window>
		);
		globalThis.getAgree = () => agree();
		globalThis.getDark = () => dark();
		globalThis.getPlan = () => plan();
		globalThis.writeDark = (v) => setDark(v);
		globalThis.writePlan = (v) => setPlan(v);
	`)

	radioState := func(root *GuiNode) (bool, bool) {
		rs := findAll(root, "radio")
		if len(rs) != 2 {
			t.Fatalf("radio 数量 = %d, want 2", len(rs))
		}
		on0, _ := rs[0].PropBool("checked")
		on1, _ := rs[1].PropBool("checked")
		return on0, on1
	}

	runPumpSteps(t, v, fake, []func(){
		// 0) 初态: 全 false, radio 组选中 "free"
		func() {
			if modelBool(t, v, "getAgree") || modelBool(t, v, "getDark") {
				t.Fatalf("初值应为 false")
			}
			on0, on1 := radioState(uiRoot(t))
			if !on0 || on1 {
				t.Fatalf("radio 初态 = (%v, %v), want (true, false)", on0, on1)
			}
		},
		// 1) 点 checkbox 与 switch
		func() {
			root := uiRoot(t)
			click(fake, findFirst(root, "checkbox"))
			click(fake, findFirst(root, "switch"))
		},
		// 2) 两个都翻转 (取反语义: onClick 没有载荷, 新值只能由当前值算)
		func() {
			root := uiRoot(t)
			if !modelBool(t, v, "getAgree") {
				t.Fatalf("点 checkbox 后 signal 仍为 false (取反写回没生效)")
			}
			if !modelBool(t, v, "getDark") {
				t.Fatalf("点 switch 后 signal 仍为 false")
			}
			if on, _ := findFirst(root, "checkbox").PropBool("checked"); !on {
				t.Fatalf("checkbox 的 checked 没有跟随 model")
			}
			if on, _ := findFirst(root, "switch").PropBool("checked"); !on {
				t.Fatalf("switch 的 checked 没有跟随 model")
			}
		},
		// 3) 读方向: 外部把 switch 关掉, 控件必须跟着灭
		func() { callGlobalFn(t, v, "writeDark", object.NewBoolean(false)) },
		func() {
			if modelBool(t, v, "getDark") {
				t.Fatalf("外部写 false 未生效")
			}
			if on, _ := findFirst(uiRoot(t), "switch").PropBool("checked"); on {
				t.Fatalf("外部写 false 后 switch 仍显示打开")
			}
		},
		// 4) 点第二个 radio ("pro"): 选中项写进 model, 两个 radio 的 checked 由派生决定
		func() {
			rs := findAll(uiRoot(t), "radio")
			click(fake, rs[1])
		},
		func() {
			if got := modelStr(t, v, "getPlan"); got != "pro" {
				t.Fatalf("点第二个 radio 后 plan = %q, want \"pro\"", got)
			}
			on0, on1 := radioState(uiRoot(t))
			if on0 || !on1 {
				t.Fatalf("切换后 radio 态 = (%v, %v), want (false, true) —— 互斥应自动成立", on0, on1)
			}
		},
		// 5) 读方向: 外部改回 "free", 派生 checked 必须跟着换边
		func() { callGlobalFn(t, v, "writePlan", object.NewString("free")) },
		func() {
			if got := modelStr(t, v, "getPlan"); got != "free" {
				t.Fatalf("外部写入后 plan = %q, want \"free\"", got)
			}
			on0, on1 := radioState(uiRoot(t))
			if !on0 || on1 {
				t.Fatalf("外部改回 free 后 radio 态 = (%v, %v), want (true, false)", on0, on1)
			}
		},
	})
}

// TestModelSliderAndSelect slider 写回必须是**数字** (不做 "3" → 3 这类转换,
// 也不做反向的字符串化), select 的 onChange 载荷是 {value} ⇒ 写回字符串。
func TestModelSliderAndSelect(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [vol, setVol] = createSignal(0);
		const [city, setCity] = createSignal("bj");
		render(
			<window title="model" width={400} height={200}>
				<column gap={10}>
					<slider width={160} min={0} max={100} model={vol} />
					<select width={160} options={["bj", "sh", "gz"]} model={city} />
				</column>
			</window>
		);
		// ===== 测试钩子: 把脚本里的值/动作挂到 globalThis, 供 Go 测试按名字调用 =====
		// 注意: 这个 VM 里**顶层 const/let 本来就对 Go 侧可见** (v.Globals().Get("vol")
		// 直接拿得到 getter, helpers_test.go 的 globalVal/globalNum 就是这么用的), 所以
		// 钩子不是唯一途径, 它的价值是给测试一个**显式而稳定**的接口面: 不依赖脚本内部
		// 的变量名与形状, 还能暴露派生投影 (volKind = typeof vol()) 与动作 (writeXxx)。
		// 若状态长在组件内部 (不在顶层), 那就只能靠钩子 —— 闭包变量 Go 侧拿不到。
		// 命名纪律: 一律 writeXxx —— 写成 globalThis.setCity = (v) => setCity(v) 会
		// 自我覆盖 (箭头调到自己 ⇒ 无限递归), 而那个错只在 Go 侧 callbackError 里留
		// 一句 (callGlobalFn 不读它), 现象是"写入静默无效"。
		globalThis.getVol = () => vol();
		globalThis.getCity = () => city();
		globalThis.volKind = () => typeof vol();
		globalThis.writeCity = (v) => setCity(v);
	`)

	runPumpSteps(t, v, fake, []func(){
		// 0) 初态
		func() {
			root := uiRoot(t)
			if got := modelNum(t, v, "getVol"); got != 0 {
				t.Fatalf("slider 初值 = %v, want 0", got)
			}
			if got, _ := findFirst(root, "select").PropStr("value"); got != "bj" {
				t.Fatalf("select 初值 = %q, want bj", got)
			}
			sl := findFirst(root, "slider")
			fake.push(Event{Kind: EventMouseDown, X: sl.Box.X + sl.Box.W/2, Y: sl.Box.Y + sl.Box.H/2})
			fake.push(Event{Kind: EventMouseUp, X: sl.Box.X + sl.Box.W/2, Y: sl.Box.Y + sl.Box.H/2})
		},
		// 1) 单击轨道中点 ⇒ 约 50, 且类型仍是 number
		func() {
			got := modelNum(t, v, "getVol")
			if got < 40 || got > 60 {
				t.Fatalf("点轨道中点后 slider 值 = %v, want ≈50", got)
			}
			if kind := modelStr(t, v, "volKind"); kind != "number" {
				t.Fatalf("写回的值类型 = %q, want number (model 不该做类型转换)", kind)
			}
			click(fake, findFirst(uiRoot(t), "select")) // 展开下拉
		},
		// 2) 点第二个选项 "sh"
		func() {
			opts := findAll(uiRoot(t), "select-option")
			if len(opts) != 3 {
				t.Fatalf("下拉选项数 = %d, want 3", len(opts))
			}
			click(fake, opts[1])
		},
		// 3) select 写回字符串
		func() {
			if got := modelStr(t, v, "getCity"); got != "sh" {
				t.Fatalf("选中第二项后 city = %q, want \"sh\"", got)
			}
			if got, _ := findFirst(uiRoot(t), "select").PropStr("value"); got != "sh" {
				t.Fatalf("select 显示值 = %q, want sh", got)
			}
		},
		// 4) 读方向
		func() { callGlobalFn(t, v, "writeCity", object.NewString("gz")) },
		func() {
			if got := modelStr(t, v, "getCity"); got != "gz" {
				t.Fatalf("外部写入后 city = %q, want \"gz\"", got)
			}
			if got, _ := findFirst(uiRoot(t), "select").PropStr("value"); got != "gz" {
				t.Fatalf("外部改 signal 后 select 显示 = %q, want gz", got)
			}
		},
	})
}

// TestModelPairSourceAndUserHandler [get, set] 二元组 (自定义来源) 与
// "model + 脚本自己的 onInput 两个都跑"这条合成规则。
func TestModelPairSourceAndUserHandler(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [user, setUser] = createSignal({ name: "" });
		let seen = "";
		render(
			<window title="model" width={400} height={120}>
				<column gap={6}>
					<input width={200} model={[() => user().name, (v) => setUser({ name: v }) ]} />
					<input width={200} model={[() => user().name, (v) => setUser({ name: v }) ]}
						onInput={(e) => { seen = seen + "<" + e.value + ">"; }} />
				</column>
			</window>
		);
		globalThis.getName = () => user().name;
		globalThis.getSeen = () => seen;
	`)

	runPumpSteps(t, v, fake, []func(){
		// 0) 点第一个输入框 (自定义二元组来源)
		func() { focusInput(fake, findAll(uiRoot(t), "input")[0]) },
		func() {
			in := findAll(uiRoot(t), "input")[0]
			if !in.focused {
				t.Fatalf("第一个 input 未获焦")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "a"})
		},
		// 1) 自定义 setter 被调用 (写出一个新对象), 读方向也跟上了
		func() {
			if got := modelStr(t, v, "getName"); got != "a" {
				t.Fatalf("二元组来源写回后 name = %q, want \"a\"", got)
			}
			// 两个输入框绑的是同一个来源 ⇒ 第二个的显示也该是新值
			if got := findAll(uiRoot(t), "input")[1].inputValue(); got != "a" {
				t.Fatalf("同源的第二框显示 = %q, want \"a\"", got)
			}
		},
		// 2) 第二个框: model 与脚本自己的 onInput 都要跑
		func() { focusInput(fake, findAll(uiRoot(t), "input")[1]) },
		func() {
			if !findAll(uiRoot(t), "input")[1].focused {
				t.Fatalf("第二个 input 未获焦")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "b"})
		},
		func() {
			// 焦点落在行首 ⇒ 插入位置由光标决定, 这里只断言"编辑落地了"这个事实:
			// 值变了、显示与 model 一致、脚本自己的处理器也跑了。
			name := modelStr(t, v, "getName")
			if name != "ab" && name != "ba" {
				t.Fatalf("model 写回后 name = %q, want \"ab\" 或 \"ba\"", name)
			}
			if got := findAll(uiRoot(t), "input")[1].inputValue(); got != name {
				t.Fatalf("显示 = %q 与 model = %q 不一致", got, name)
			}
			if got := modelStr(t, v, "getSeen"); got == "" {
				t.Fatalf("脚本自己的 onInput 没跑 (model 把它吞了)")
			}
		},
	})
}

// TestModelMisuseWarnsAndDegrades 误用必须出声, 且界面照旧能渲染:
// 标量 model / model+value 冲突 / 不支持的标签。
func TestModelMisuseWarnsAndDegrades(t *testing.T) {
	// 警告环与去重表都是进程级的: 先清掉, 免得被别的用例的同类警告污染
	resetWarnRing()
	resetModelWarns()

	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [draft, setDraft] = createSignal("x");
		render(
			<window title="model" width={400} height={200}>
				<column gap={6}>
					<input width={200} model={draft()} />
					<input width={200} model={draft} value={() => "y"} />
					<row model={draft}></row>
				</column>
			</window>
		);
		globalThis.getDraft = () => draft();
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			ins := findAll(uiRoot(t), "input")
			if len(ins) != 2 {
				t.Fatalf("input 数量 = %d, want 2", len(ins))
			}
			// 标量 model: 降级成只读值, 但界面必须照旧显示当前值
			if got := ins[0].inputValue(); got != "x" {
				t.Fatalf("标量 model 的显示 = %q, want \"x\" (降级后仍要能读)", got)
			}
			// 冲突: model 为准
			if got := ins[1].inputValue(); got != "x" {
				t.Fatalf("model+value 冲突时显示 = %q, want \"x\" (model 优先)", got)
			}
			for _, frag := range []string{
				"收到了一个标量",
				"同时给了 model 与 value",
				"只支持受控组件",
			} {
				if !viewWarned(frag) {
					t.Fatalf("缺少警告 %q (缓冲 = %v)", frag, warnSnapshot())
				}
			}
		},
	})
}

// TestModelDemoScript 把演示脚本 (testdata/model_demo.js) 真跑一遍:
// 按行标签点每个控件, 读"③ state"那一行 —— 它是全部绑定的投影, 于是
// "点下去有没有写回 signal" 只需看一行文本。
//
// 假 Surface 的尺寸决定布局尺寸 (fakeFactory 不读 WindowConfig), 所以这里
// 手工设成脚本里 <window> 的尺寸 —— 顺带能断言"内容没溢出窗口"
// (窗口不是滚动容器: 溢出部分既画不出来也点不中)。
func TestModelDemoScript(t *testing.T) {
	const demoW, demoH = 700, 705

	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "model_demo.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	fake.w, fake.h = demoW, demoH
	// upload 那一行会弹原生"打开文件"对话框: 注入假对话框, 否则整条链路走降级路径
	// ("后端不支持"打 stderr 且什么都不做), 演示里的 files 会永远停在 0。
	fake.dialog = &fakeDialogHost{openPath: "/tmp/demo-report.pdf", openOk: true}
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	stateHas := func(frag string) bool { return textContainsAny(root, frag) }

	steps := []func(){
		// 0) 初态 + 不溢出窗口
		func() {
			for _, frag := range []string{"name=Ada", "bio=0 字符", "volume=40", "city=beijing",
				"due=2026-11-20", "brand=#ffffff", "files=0",
				"agree=false", "dark=false", "plan=free"} {
				if !stateHas(frag) {
					t.Fatalf("初态缺 %q (文本 = %v)", frag, viewTexts(root))
				}
			}
			bottom := 0
			for _, n := range allNodes(root) {
				if n == root {
					continue
				}
				if b := n.Box.Y + n.Box.H; b > bottom {
					bottom = b
				}
			}
			t.Logf("窗口 %dx%d, 内容底部 y = %d (下边距 14)", demoW, demoH, bottom)
			if bottom > demoH-14 {
				t.Fatalf("内容溢出窗口: 底部 y=%d > 可用高 %d", bottom, demoH-14)
			}
			// 点掉除输入框以外的控件 (一轮只投 1 个事件, 所以分两步)
			click(fake, lineIn(t, root, "checkbox", "checkbox"))
		},
		// 0b) slider 的单击定位发生在按下那一刻 ⇒ 成对投 Down/Up (与 slider_test 同口径);
		//     焦点最后给输入框: 前面几个点击都会顺手抢焦点。
		func() {
			click(fake, lineNodes(t, root, "radio ×2", "radio")[1]) // 点第二个: plan 应变成 pro
			click(fake, lineIn(t, root, "switch", "switch"))
			sl := lineIn(t, root, "slider (数字)", "slider")
			fake.push(Event{Kind: EventMouseDown, X: sl.Box.X + 2, Y: sl.Box.Y + sl.Box.H/2})
			fake.push(Event{Kind: EventMouseUp, X: sl.Box.X + 2, Y: sl.Box.Y + sl.Box.H/2})
			focusInput(fake, lineIn(t, root, "input", "input"))
		},
		// 1) 输入框敲一个字 (光标在行首 ⇒ 结果可预测)
		func() {
			fake.push(Event{Kind: EventKeyDown, Key: "7"})
		},
		// 2) 一行 state 同时验证四个绑定; 检查完把焦点交给 textarea
		func() {
			for _, frag := range []string{"name=7Ada", "agree=true", "dark=true", "plan=pro"} {
				if !stateHas(frag) {
					t.Fatalf("点过之后缺 %q (文本 = %v)", frag, viewTexts(root))
				}
			}
			if stateHas("volume=40") {
				t.Fatalf("slider 点击没有写回 volume")
			}
			focusInput(fake, lineIn(t, root, "textarea", "textarea"))
		},
		// 3) textarea 敲一个字
		func() { fake.push(Event{Kind: EventKeyDown, Key: "z"}) },
		// 4) textarea 写回 (按长度投影); 展开下拉
		func() {
			if !stateHas("bio=1 字符") {
				t.Fatalf("textarea 没写回: %v", viewTexts(root))
			}
			click(fake, lineIn(t, root, "select", "select"))
		},
		// 5) 点下拉第二项 "shanghai"
		func() {
			opts := findAll(root, "select-option")
			if len(opts) != 3 {
				t.Fatalf("下拉选项数 = %d, want 3", len(opts))
			}
			click(fake, opts[1])
		},
		// 6) select 写回; 焦点先交给 [get, set] 来源那一行
		func() {
			if !stateHas("city=shanghai") {
				t.Fatalf("select 没写回: %v", viewTexts(root))
			}
			focusInput(fake, lineIn(t, root, "[get, set] 来源", "input"))
		},
		// 6b–6f) T08 三个新控件: datepicker / colorpicker / upload 各点一次。
		//   这一段会被插在步骤 6 与 7 之间, 而步骤 7 要往 [get, set] 那个输入框里敲字,
		//   所以最后一步必须把焦点交回去 (每个弹层的选中都会把焦点收回字段)。
		func() { click(fake, lineIn(t, root, "datepicker", "datepicker")) },
		// 6c) 点日历里的第 5 天 (2026-11-01 是周日 ⇒ 前置空格 0)
		func() {
			dp := lineIn(t, root, "datepicker", "datepicker")
			if dp.popup == nil {
				t.Fatalf("日历没展开")
			}
			x, y := dateCellCenter(dp, 0, 5)
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
			fake.push(Event{Kind: EventMouseUp, X: x, Y: y})
		},
		func() {
			if !stateHas("due=2026-11-05") {
				t.Fatalf("datepicker 没写回: %v", viewTexts(root))
			}
			click(fake, lineIn(t, root, "colorpicker", "colorpicker"))
		},
		// 6e) 点色板第 3 格 (#2f80ed)
		func() {
			cp := lineIn(t, root, "colorpicker", "colorpicker")
			if cp.popup == nil {
				t.Fatalf("色板没展开")
			}
			x, y := colorSwatchCenter(cp, 2)
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
			fake.push(Event{Kind: EventMouseUp, X: x, Y: y})
		},
		// 6f) 色值写回; 点 upload (假对话框直接给一个路径), 之后把焦点交回 [get, set] 那一行
		func() {
			if !stateHas("brand=#2f80ed") {
				t.Fatalf("colorpicker 没写回: %v", viewTexts(root))
			}
			click(fake, lineIn(t, root, "upload", "upload"))
		},
		func() {
			// upload 的 model 写回取的是载荷里的 paths ⇒ 长度 1 (不是 undefined / 不是载荷对象)
			if !stateHas("files=1") {
				t.Fatalf("upload 没写回: %v", viewTexts(root))
			}
			focusInput(fake, lineIn(t, root, "[get, set] 来源", "input"))
		},
		// 7) 自定义来源那一行敲一个字
		func() { fake.push(Event{Kind: EventKeyDown, Key: "k"}) },
		// 8) 二元组来源写回; 切到 saved
		func() {
			if !textContainsAny(root, "profile.nick = k") {
				t.Fatalf("[get, set] 来源没写回: %v", viewTexts(root))
			}
			click(fake, demoPhaseButton(t, root, "save"))
		},
		// 9) Switch 切到 saved; 再走 fallback
		func() {
			if !textContainsAny(root, "saved —") {
				t.Fatalf("Switch 没切到 saved: %v", viewTexts(root))
			}
			click(fake, demoPhaseButton(t, root, "bad state"))
		},
		// 10) Switch 的 fallback 可达
		func() {
			if !textContainsAny(root, "没有任何 Match 为真") {
				t.Fatalf("Switch fallback 没上屏: %v", viewTexts(root))
			}
		},
	}

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		if round-1 < len(steps) {
			// 假 Surface 的 events/arrived 通道容量都只有 16, 而每个 Pump 轮次
			// 只消费一个唤醒 token ⇒ 一轮里投太多事件会把 push 永久卡死
			// (整用例挂到超时)。所以: 步骤自己推了事件就不再补无害唤醒,
			// 一轮的投递数保持 ≈1 (见 helpers_test.go 的 runDemoSteps 同款纪律)。
			before := len(fake.events)
			steps[round-1]()
			if len(fake.events) == before {
				fake.push(Event{Kind: EventMouseLeave})
			}
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
}

// demoPhaseButton 按可见文本找演示里 ④ 那一排按钮。
func demoPhaseButton(t *testing.T, root *GuiNode, label string) *GuiNode {
	t.Helper()
	for _, b := range findAll(root, "button") {
		if textContainsAny(b, label) {
			return b
		}
	}
	t.Fatalf("找不到按钮 %q", label)
	return nil
}

// TestModelPickerAndUpload model 覆盖 T08 的三个新控件。语义表按载荷形状分两支:
//
//	datepicker · colorpicker  model ⇄ value  onChange({value})
//	upload                    model ⇄ value  onChange({files, paths}) ⇒ 写回**取 paths**
//
// upload 那一支与别的标签不同, 所以必须走真链路断言: 如果写回的是整个载荷对象,
// signal 里会躺一个 {files, paths} 而不是路径数组 —— 这种错在"Props 里有没有键"
// 的断言下完全看不出来, 只有点开弹层真选一次才暴露。
func TestModelPickerAndUpload(t *testing.T) {
	// 钉住"今天", 让日历的视图月份只由 value 决定 (否则用例会随运行日期漂移)
	pinToday(t, 2026, 11, 20)

	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [due, setDue] = createSignal("2026-11-20");
		const [brand, setBrand] = createSignal("#ffffff");
		const [files, setFiles] = createSignal([]);
		render(
			<window title="model" width={460} height={420}>
				<column gap={10}>
					<datepicker model={due} />
					<colorpicker model={brand} colors={["#ffffff", "#000000", "#2f80ed"]} columns={3} />
					<upload model={files} />
				</column>
			</window>
		);
		globalThis.getDue = () => due();
		globalThis.getBrand = () => brand();
		globalThis.fileCount = () => files().length;
		globalThis.firstFile = () => String(files()[0]);
	`)
	// upload 的点击会去弹原生"打开文件"对话框: 不注入假对话框的话整条链路会走
	// 降级路径 ("后端不支持"打 stderr 且什么都不做), 用例会以"点了没反应"失败。
	fake.dialog = &fakeDialogHost{openPath: "/tmp/report.pdf", openOk: true}

	runPumpSteps(t, v, fake, []func(){
		// 0) 读方向: 显示值来自 signal (model 补的 value 是函数, 不是快照); 展开日历
		func() {
			root := uiRoot(t)
			if got, _ := findFirst(root, "datepicker").PropStr("value"); got != "2026-11-20" {
				t.Fatalf("datepicker 初值 = %q, want 2026-11-20", got)
			}
			if got, _ := findFirst(root, "colorpicker").PropStr("value"); got != "#ffffff" {
				t.Fatalf("colorpicker 初值 = %q, want #ffffff", got)
			}
			if got := modelNum(t, v, "fileCount"); got != 0 {
				t.Fatalf("upload 初值长度 = %v, want 0", got)
			}
			click(fake, findFirst(root, "datepicker"))
		},
		// 1) 点日历里的第 5 天 (2026-11-01 是周日 ⇒ 前置空格 0)
		func() {
			dp := findFirst(uiRoot(t), "datepicker")
			if dp.popup == nil {
				t.Fatalf("日历没展开")
			}
			x, y := dateCellCenter(dp, 0, 5)
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
			fake.push(Event{Kind: EventMouseUp, X: x, Y: y})
		},
		// 2) 写方向: signal 里是新日期; 读方向: 字段显示也跟着走; 展开色板
		func() {
			if got := modelStr(t, v, "getDue"); got != "2026-11-05" {
				t.Fatalf("选中第 5 天后 due = %q, want 2026-11-05 (model 的写回没生效)", got)
			}
			if got, _ := findFirst(uiRoot(t), "datepicker").PropStr("value"); got != "2026-11-05" {
				t.Fatalf("datepicker 显示 = %q, want 2026-11-05 (读方向没跟上)", got)
			}
			click(fake, findFirst(uiRoot(t), "colorpicker"))
		},
		// 3) 点色板第 3 格 (#2f80ed)
		func() {
			cp := findFirst(uiRoot(t), "colorpicker")
			if cp.popup == nil {
				t.Fatalf("色板没展开")
			}
			x, y := colorSwatchCenter(cp, 2)
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
			fake.push(Event{Kind: EventMouseUp, X: x, Y: y})
		},
		// 4) 色值写回; 点 upload 起原生对话框 (假对话框直接给一个路径)
		func() {
			if got := modelStr(t, v, "getBrand"); got != "#2f80ed" {
				t.Fatalf("选中第三格后 brand = %q, want #2f80ed", got)
			}
			click(fake, findFirst(uiRoot(t), "upload"))
		},
		// 5) upload 写回的是 paths (不是整个 {files, paths}), 且显示与值同源
		func() {
			if got := modelNum(t, v, "fileCount"); got != 1 {
				t.Fatalf("选完文件后 files.length = %v, want 1", got)
			}
			if got := modelStr(t, v, "firstFile"); got != "/tmp/report.pdf" {
				t.Fatalf("files[0] = %q, want /tmp/report.pdf", got)
			}
			// 显示与值同源: upload 的文本是**自绘**的 (没有 text 子节点), 所以这里
			// 读它的显示内容本身 —— 用 viewTexts 会永远看不到东西。
			if text, ok := findFirst(uiRoot(t), "upload").uploadDisplay(); !ok || text != "report.pdf" {
				t.Fatalf("upload 显示 = %q ok=%v, want report.pdf", text, ok)
			}
		},
	})
}

// TestModelSignalCarriesSetter signal 自带 setter (model 的凭据) 是可断言的
// 契约: gx/solid 的 getter 上必须有 .set, 且它与配对 setter 语义一致。

func TestModelSignalCarriesSetter(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";

		const [n, setN] = createSignal(1);
		render(<window title="model" width={200} height={80}><column><text>{(n)}</text></column></window>);
		globalThis.viaPropertyKind = () => typeof n.set;
		globalThis.writeViaProperty = (v) => n.set(v);
		globalThis.read = () => n();
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			if kind := modelStr(t, v, "viaPropertyKind"); kind != "function" {
				t.Fatalf("signal 的 .set 类型 = %q, want function", kind)
			}
			callGlobalFn(t, v, "writeViaProperty", object.NewNumber(7))
		},
		func() {
			if got := modelNum(t, v, "read"); got != 7 {
				t.Fatalf("经 .set 写入后值 = %v, want 7", got)
			}
		},
	})
}
