package gfx

// ===== For / Show / Switch 的"短写法 + 误用出声"测试 =====
//
// 这一批改动不改任何语义, 只做两件事:
//   - **短写法**: `each={rows}` / `when={open}` (signal 本身就是取值函数) 与
//     `key="id"` (字段名简写) —— 必须真的有断言钉住, 否则"文档里能写、实际不行";
//   - **误用出声**: 快照写法 (each={rows()} / when={open()}) 与非法形态必须打警告。
//     断言走警告环 (gx/dev 的那份缓冲), 与 view_test.go 的 viewWarned 同一口径。
//
// 复用/重建仍然只认节点身份与渲染次数 (文本长得一样, 数文本没用)。

import (
	"strings"
	"testing"
)

// TestViewBareSignalAndFieldKey 短写法的等价性: `each={rows}` 等价 `each={() => rows()}`,
// `when={open}` 等价 `when={() => open()}`, `key="id"` 等价 `key={(r) => r.id}`。
func TestViewBareSignalAndFieldKey(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For, Show } from "gx/view";

		let renders = 0;
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
		]);
		const [open, setOpen] = createSignal(true);
		const Row = (row) => {
			renders = renders + 1;
			return <row note={row.id}><text>{row.title}</text></row>;
		};
		render(
			<window title="guard" width={400} height={220}>
				<column gap={4}>
					<For each={rows} key="id" stable fallback={<row note="empty"><text>暂无</text></row>}>
						{Row}
					</For>
					<Show when={open} fallback={<row note="off"><text>隐藏中</text></row>}>
						<row note="body"><text>面板</text></row>
					</Show>
				</column>
			</window>
		);
		globalThis.renderCount = () => renders;
		globalThis.append = () => setRows(rows().concat([{ id: "c", title: "Gamma" }]));
		globalThis.retitleB = () => setRows(rows().map((r) => r.id === "b" ? { id: "b", title: "Beta2" } : r));
		globalThis.flip = () => setOpen(!open());
	`)

	runPumpSteps(t, v, fake, []func(){
		// 0) 裸 signal 的 each / when 都挂上了
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|body", "Alpha|Beta|面板")
			if got := callGlobalInspect(t, v, "renderCount"); got != "2" {
				t.Fatalf("初次渲染次数 = %v, want 2 (裸 signal 的 each 没生效?)", got)
			}
		},
		// 1) 追加一项
		func() { callGlobalFn(t, v, "append") },
		// 2) key="id" 配对正确: 只构建新行 (body/面板 是静态子树, 不参与渲染计数)
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c|body", "Alpha|Beta|Gamma|面板")
			if got := callGlobalInspect(t, v, "renderCount"); got != "3" {
				t.Fatalf("追加后渲染次数 = %v, want 3 (key=\"id\" 没有按字段配对)", got)
			}
		},
		// 3) 换掉 b 的对象引用 (同 id)
		func() { callGlobalFn(t, v, "retitleB") },
		// 4) 只有 b 重建; stable 下 a/c 的下标变化不参与判定
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c|body", "Alpha|Beta2|Gamma|面板")
			if got := callGlobalInspect(t, v, "renderCount"); got != "4" {
				t.Fatalf("改一行后渲染次数 = %v, want 4 (只该重建那一行)", got)
			}
		},
		// 5) 隐藏
		func() { callGlobalFn(t, v, "flip") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c|off", "Alpha|Beta2|Gamma|隐藏中")
			if got := callGlobalInspect(t, v, "renderCount"); got != "4" {
				t.Fatalf("隐藏不该重建列表行: %v", got)
			}
		},
		// 6) 再显示 (裸 signal 的 when 必须真的响应)
		func() { callGlobalFn(t, v, "flip") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c|body", "Alpha|Beta2|Gamma|面板")
		},
	})
}

// TestViewMisuseWarns 非法/快照形态必须出声 (否则现象是"列表空着""条件冻住", 完全无提示)。
func TestViewMisuseWarns(t *testing.T) {
	// 警告环与去重表都是进程级的: 先清掉, 免得被别的用例的同类警告污染
	resetWarnRing()
	resetViewWarns()

	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For, Show, Switch, Match } from "gx/view";

		const [rows, setRows] = createSignal([{ id: "a" }]);
		const [open, setOpen] = createSignal(true);
		const [phase, setPhase] = createSignal("loading");
		render(
			<window title="guard" width={400} height={220}>
				<column gap={4}>
					<For each="oops">{() => <text>x</text>}</For>
					<For each={rows} key={42} stable={() => true}>{() => <text>y</text>}</For>
					<Show when={open()}><text>z</text></Show>
					<Switch fallback={<text>fb</text>}>
						<Match when={phase()}><text>m</text></Match>
					</Switch>
				</column>
			</window>
		);
	`)

	// 这个用例只断警告 (警告在挂载期就打完了), 但 runPumpSteps 要一个真 VM 跑事件循环。
	runPumpSteps(t, v, fake, []func(){
		func() {
			want := []string{
				"each 需要取值函数",      // each="oops" (字符串)
				"key 需要取值函数或字段名简写", // key={42}
				"stable 只认字面量布尔",   // stable={() => true}
				"when 收到静态布尔",      // when={open()}  <- 最像正确的那个错误
				"它会被当成固定真值/假值",     // when={phase()} (字符串)
			}
			var missing []string
			for _, frag := range want {
				if !viewWarned(frag) {
					missing = append(missing, frag)
				}
			}
			if len(missing) > 0 {
				var got []string
				for _, w := range warnSnapshot() {
					got = append(got, w.Text)
				}
				t.Fatalf("缺少警告 %v\n实际缓冲:\n%s", missing, strings.Join(got, "\n"))
			}
		},
	})
}
