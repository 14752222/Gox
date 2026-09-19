package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== gx/view: 声明式视图 (For / Show / Switch / Match) =====
//
// 断言口径: 走"真 VM + 真窗口 + 事件泵"的全链路 (与 resource_test 同构)。
// 因为 For / Show 的关键语义是**跨轮次的节点身份** —— "这一行是复用了还是重建
// 了"只有节点指针能证明, 数文本数量看不出来 (重建后文本一模一样)。
//
// 两条纪律 (与 resource_test / gx/view 的接线约定同源):
//   - 从 Go 侧驱动脚本函数必须在 v.RunTimersWithPump 执行期内, 否则 currentVM
//     为 nil, 调用静默变 undefined;
//   - 断言只在泵轮次里做 (最后一轮会推 EventClose 关窗, 之后树就不在了)。

// evalView / viewRoot 等共享引导与树遍历 helper 见 helpers_test.go
// (evalUI / uiRoot)。

// viewNotes 按树序返回所有带 note prop 的节点标记。脚本用 note 给行/分支打
// 标记, 测试于是能"按标记认行", 而不是靠位置或数量猜。
func viewNotes(root *GuiNode) []string {
	var out []string
	var walk func(*GuiNode)
	walk = func(n *GuiNode) {
		for _, c := range n.Children {
			if s, ok := c.PropStr("note"); ok {
				out = append(out, s)
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

// viewNodesOf 返回 note == want 的节点 (0 或多个)。
func viewNodesOf(root *GuiNode, want string) []*GuiNode {
	var out []*GuiNode
	var walk func(*GuiNode)
	walk = func(n *GuiNode) {
		for _, c := range n.Children {
			if s, ok := c.PropStr("note"); ok && s == want {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

// viewTexts 按树序返回子树内全部 #text 内容。
func viewTexts(root *GuiNode) []string {
	var out []string
	var walk func(*GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "#text" {
			out = append(out, n.Text)
			return
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// viewOne 断言 want 标记的节点恰好一个并返回它 (复用/指针身份断言的抓手)。
func viewOne(t *testing.T, root *GuiNode, want string) *GuiNode {
	t.Helper()
	ns := viewNodesOf(root, want)
	if len(ns) != 1 {
		t.Fatalf("标记 %q 的节点数量 = %d, want 1 (树上标记 = %v)", want, len(ns), viewNotes(root))
	}
	return ns[0]
}

// viewAssertLayout 断言"标记顺序 + 文本内容"。
func viewAssertLayout(t *testing.T, root *GuiNode, wantNotes string, wantTexts string) {
	t.Helper()
	if got := strings.Join(viewNotes(root), "|"); got != wantNotes {
		t.Fatalf("标记顺序 = %q, want %q", got, wantNotes)
	}
	if got := strings.Join(viewTexts(root), "|"); got != wantTexts {
		t.Fatalf("文本 = %q, want %q", got, wantTexts)
	}
}

// viewWarned 报告警告环里是否出现过某个片段 (gx/dev 的那份缓冲)。
func viewWarned(fragment string) bool {
	for _, w := range warnSnapshot() {
		if strings.Contains(w.Text, fragment) {
			return true
		}
	}
	return false
}

// TestViewForKeyedReuseAndReindex 列表渲染的主干语义:
// 追加一项只渲染新行; 同引用的新数组一行都不重建; 下标变化则重建那一行
// (序号必须跟着位置更新 —— 本引擎的 JSX 内容是求值一次的静态值)。
func TestViewForKeyedReuseAndReindex(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
			{ id: "c", title: "Gamma" },
		]);
		const Row = (row, i) => {
			probes = probes + 1;
			return <row note={row.id}><text>{(i + 1) + ". " + row.title}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()} key={(r) => r.id}>{Row}</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.append = () => setRows(rows().concat([{ id: "d", title: "Delta" }]));
		globalThis.touch = () => setRows(rows().slice());
		globalThis.swapFirst = () => { const rs = rows(); setRows([rs[1], rs[0]].concat(rs.slice(2))); };
	`)

	var a, b, c, d *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c", "1. Alpha|2. Beta|3. Gamma")
			if got := callGlobalInspect(t, v, "probeCount"); got != "3" {
				t.Fatalf("初次挂载渲染次数 = %v, want 3", got)
			}
			root := uiRoot(t)
			a, b, c = viewOne(t, root, "a"), viewOne(t, root, "b"), viewOne(t, root, "c")
		},
		func() { callGlobalFn(t, v, "append") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c|d", "1. Alpha|2. Beta|3. Gamma|4. Delta")
			if got := callGlobalInspect(t, v, "probeCount"); got != "4" {
				t.Fatalf("追加一行后渲染次数 = %v, want 4 (只渲染新行)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "a") != a || viewOne(t, root, "b") != b || viewOne(t, root, "c") != c {
				t.Fatalf("追加后旧行被重建 (节点指针应该不变)")
			}
			d = viewOne(t, root, "d")
		},
		func() { callGlobalFn(t, v, "touch") }, // 新数组对象、同一批元素引用
		func() {
			if got := callGlobalInspect(t, v, "probeCount"); got != "4" {
				t.Fatalf("同引用重设后渲染次数 = %v, want 4 (引用同一 ⇒ 一行都不重建)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "a") != a || viewOne(t, root, "b") != b || viewOne(t, root, "c") != c {
				t.Fatalf("同引用重设后旧行被重建")
			}
		},
		func() { callGlobalFn(t, v, "swapFirst") },
		func() {
			// 前两项互换: a/b 的下标变了 ⇒ 只有它们就地重渲染 (序号必须跟着位置
			// 更新 —— 本引擎的 JSX 内容是求值一次的静态值, 下标变了就等于内容
			// 过期了); c/d 的引用与下标都没动 ⇒ 原样复用。
			viewAssertLayout(t, uiRoot(t), "b|a|c|d", "1. Beta|2. Alpha|3. Gamma|4. Delta")
			if got := callGlobalInspect(t, v, "probeCount"); got != "6" {
				t.Fatalf("前两项互换后渲染次数 = %v, want 6 (4 + 2 行下标变了)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "c") != c || viewOne(t, root, "d") != d {
				t.Fatalf("下标未变的行被重建 (c/d 应该原样复用)")
			}
		},
	})
}

// TestViewForStableModeReusesAcrossReorder stable 模式: 下标退出复用判定,
// 于是重排 / 中间删除都不重建行 (代价: 下标参数停在挂载时的值)。
func TestViewForStableModeReusesAcrossReorder(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
			{ id: "c", title: "Gamma" },
		]);
		const Row = (row) => {
			probes = probes + 1;
			return <row note={row.id}><text>{row.title}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()} key={(r) => r.id} stable>{Row}</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.reverse = () => setRows(rows().slice().reverse());
		globalThis.drop = () => setRows(rows().filter((r) => r.id !== "b"));
	`)

	var a, c *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			if got := callGlobalInspect(t, v, "probeCount"); got != "3" {
				t.Fatalf("初次挂载渲染次数 = %v, want 3", got)
			}
			root := uiRoot(t)
			a, c = viewOne(t, root, "a"), viewOne(t, root, "c")
		},
		func() { callGlobalFn(t, v, "reverse") },
		func() {
			viewAssertLayout(t, uiRoot(t), "c|b|a", "Gamma|Beta|Alpha")
			if got := callGlobalInspect(t, v, "probeCount"); got != "3" {
				t.Fatalf("stable 重排后渲染次数 = %v, want 3 (一行都没重建)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "c") != c || viewOne(t, root, "a") != a {
				t.Fatalf("stable 重排后行被重建 (节点指针应该不变)")
			}
		},
		func() { callGlobalFn(t, v, "drop") }, // 在 [c,b,a] 上删中间那行
		func() {
			viewAssertLayout(t, uiRoot(t), "c|a", "Gamma|Alpha")
			if got := callGlobalInspect(t, v, "probeCount"); got != "3" {
				t.Fatalf("中间删除后渲染次数 = %v, want 3 (后续行不因下标前移而重建)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "c") != c || viewOne(t, root, "a") != a {
				t.Fatalf("中间删除后幸存行被重建")
			}
		},
	})
}

// TestViewForDisposesRemovedRowsAndRunsCleanup 行被删除时子树必须收尾
// (effect 注销 + onCleanup 执行), 且只执行一次。删尾行不会移动其它行的下标,
// 于是"只跑被删行的清理"这条断言是干净的; 中间删除的连带效果另见下一个用例。
func TestViewForDisposesRemovedRowsAndRunsCleanup(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal, onCleanup } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		let log = "";
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
			{ id: "c", title: "Gamma" },
		]);
		const Row = (row) => {
			probes = probes + 1;
			onCleanup(() => { log = log + row.id + ";"; });
			return <row note={row.id}><text>{row.title}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()} key={(r) => r.id}>{Row}</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.cleanupLog = () => log;
		globalThis.dropTail = () => setRows(rows().filter((r) => r.id !== "c"));
	`)

	var a, b *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			root := uiRoot(t)
			a, b = viewOne(t, root, "a"), viewOne(t, root, "b")
		},
		func() { callGlobalFn(t, v, "dropTail") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b", "Alpha|Beta")
			if got := callGlobalInspect(t, v, "cleanupLog"); got != "c;" {
				t.Fatalf("onCleanup 日志 = %q, want %q (被删行的清理必须执行且只执行一次)", got, "c;")
			}
			if got := callGlobalInspect(t, v, "probeCount"); got != "3" {
				t.Fatalf("删除后渲染次数 = %v, want 3 (幸存行的下标未变)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "a") != a || viewOne(t, root, "b") != b {
				t.Fatalf("删除后幸存行被重建")
			}
		},
	})
}

// TestViewForMiddleRemovalRerendersTail 删中间一项会让后续行的下标前移 ⇒ 那些行
// 内容过期而就地重渲染 (于是它们**上一代**的 onCleanup 也执行)。这是"下标参与
// 复用判定"的必然代价, 也是与 stable 的分界; 把它钉成断言, 免得以后有人"顺手
// 优化"成不重渲染, 把序号静默改错。
func TestViewForMiddleRemovalRerendersTail(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal, onCleanup } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		let log = "";
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
			{ id: "c", title: "Gamma" },
		]);
		const Row = (row, i) => {
			probes = probes + 1;
			onCleanup(() => { log = log + row.id + ";"; });
			return <row note={row.id}><text>{(i + 1) + "." + row.title}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()} key={(r) => r.id}>{Row}</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.cleanupLog = () => log;
		globalThis.dropMiddle = () => setRows(rows().filter((r) => r.id !== "b"));
	`)

	var a *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() { a = viewOne(t, uiRoot(t), "a") },
		func() { callGlobalFn(t, v, "dropMiddle") },
		func() {
			// c 从下标 2 前移到 1 ⇒ 重渲染, 序号由 "3." 正确地变成 "2."
			viewAssertLayout(t, uiRoot(t), "a|c", "1.Alpha|2.Gamma")
			if got := callGlobalInspect(t, v, "probeCount"); got != "4" {
				t.Fatalf("渲染次数 = %v, want 4 (3 + c 因下标前移而重渲染)", got)
			}
			if got := callGlobalInspect(t, v, "cleanupLog"); got != "c;b;" {
				t.Fatalf("清理日志 = %q, want %q (c 上一代的清理 + b 的销毁)", got, "c;b;")
			}
			if viewOne(t, uiRoot(t), "a") != a {
				t.Fatalf("下标未变的首行被重建")
			}
		},
	})
}

// TestViewForFallbackAcrossEmptyCycles 空列表 fallback: 懒构建 + 保活
// ("空 → 有数据 → 再空"时 fallback 仍是同一个节点, 内容保持响应式)。
func TestViewForFallbackAcrossEmptyCycles(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		const [rows, setRows] = createSignal([]);
		const [hint, setHint] = createSignal("暂无数据");
		render(
			<window title="view" width={400} height={300}>
				<column>
					<For each={() => rows()} fallback={<row note="empty"><text>{() => hint()}</text></row>}>
						{(row) => { probes = probes + 1; return <row note={row.id}><text>{row.title}</text></row>; }}
					</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.add = () => setRows([{ id: "x", title: "X" }]);
		globalThis.clear = () => setRows([]);
		globalThis.rehint = () => setHint("还没有内容");
	`)

	var fb *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "empty", "暂无数据")
			if got := callGlobalInspect(t, v, "probeCount"); got != "0" {
				t.Fatalf("空列表时渲染函数不该被调用, probes = %v", got)
			}
			fb = viewOne(t, uiRoot(t), "empty")
		},
		func() { callGlobalFn(t, v, "add") },
		func() {
			viewAssertLayout(t, uiRoot(t), "x", "X")
			if len(viewNodesOf(uiRoot(t), "empty")) != 0 {
				t.Fatalf("有数据时 fallback 应该从布局流里摘掉")
			}
		},
		func() { callGlobalFn(t, v, "rehint") }, // 隐藏期间改 fallback 的响应式内容
		func() {
			if got := strings.Join(viewTexts(fb), "|"); got != "还没有内容" {
				t.Fatalf("摘下的 fallback 内容 = %q, want %q (保活 ⇒ 隐藏期间仍在更新)", got, "还没有内容")
			}
		},
		func() { callGlobalFn(t, v, "clear") },
		func() {
			if viewOne(t, uiRoot(t), "empty") != fb {
				t.Fatalf("再回到空列表时 fallback 被重建 (应该复用保活的那个节点)")
			}
			if got := strings.Join(viewTexts(uiRoot(t)), "|"); got != "还没有内容" {
				t.Fatalf("文本 = %q", got)
			}
		},
	})
}

// TestViewForUnkeyedReusesByPosition 无 key 时按位置配对 (Vue 的"就地复用"):
// 同一位置、同一引用则不动; 换了对象只重建那一行; 交换位置等于两行都换内容。
func TestViewForUnkeyedReusesByPosition(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		const [rows, setRows] = createSignal([
			{ id: "a", title: "Alpha" },
			{ id: "b", title: "Beta" },
			{ id: "c", title: "Gamma" },
		]);
		const Row = (row, i) => {
			probes = probes + 1;
			return <row note={row.id}><text>{(i + 1) + ". " + row.title}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()}>{Row}</For>
				</column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.patchB = () => setRows(rows().map((r) => r.id === "b" ? { id: "b", title: "Beta2" } : r));
		globalThis.swap = () => { const rs = rows(); setRows([rs[2], rs[1], rs[0]]); };
	`)

	var a, b *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			root := uiRoot(t)
			a, b = viewOne(t, root, "a"), viewOne(t, root, "b")
		},
		func() { callGlobalFn(t, v, "patchB") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|b|c", "1. Alpha|2. Beta2|3. Gamma")
			if got := callGlobalInspect(t, v, "probeCount"); got != "4" {
				t.Fatalf("只改中间一行后渲染次数 = %v, want 4 (a/c 引用与下标都没变)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "a") != a {
				t.Fatalf("未变化的行被重建")
			}
			if viewOne(t, root, "b") == b {
				t.Fatalf("内容变化的行没有重建 (无 key 靠引用判断, 换了对象就该重渲染)")
			}
		},
		func() { callGlobalFn(t, v, "swap") },
		func() {
			// 无 key ⇒ 位置身份: 0/2 两行的内容都换了, 只有下标 1 上的 b 没动
			viewAssertLayout(t, uiRoot(t), "c|b|a", "1. Gamma|2. Beta2|3. Alpha")
			if got := callGlobalInspect(t, v, "probeCount"); got != "6" {
				t.Fatalf("交换后渲染次数 = %v, want 6", got)
			}
		},
	})
}

// TestViewForDuplicateKeysStayDistinct 重复 key 是使用者错误, 但不能把树写坏:
// 内核必须让两行各自拿到独立节点 (否则同一节点被两条路径遍历)。
func TestViewForDuplicateKeysStayDistinct(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		const [rows, setRows] = createSignal([
			{ id: "dup", title: "First" },
			{ id: "dup", title: "Second" },
		]);
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}>
					<For each={() => rows()} key={(r) => r.id}>
						{(row) => <row note={row.title}><text>{row.title}</text></row>}
					</For>
				</column>
			</window>
		);
		globalThis.flip = () => setRows(rows().slice().reverse());
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "First|Second", "First|Second")
			n1 := viewOne(t, uiRoot(t), "First")
			n2 := viewOne(t, uiRoot(t), "Second")
			if n1 == n2 {
				t.Fatalf("重复 key 的两行共用了同一个节点")
			}
			if !viewWarned("重复 key") {
				t.Fatalf("重复 key 应该留下一条警告 (gx/dev 可见)")
			}
		},
		func() { callGlobalFn(t, v, "flip") },
		func() {
			// 顺序变了但两个 title 仍在: 树没被写坏 (仍然是两行两个字)
			viewAssertLayout(t, uiRoot(t), "Second|First", "Second|First")
		},
	})
}

// TestViewForEachNumber 数字 each (v-for="n in N") 与"值比较"的复用:
// 每次求值都会新造 Number 对象, 但值相等 ⇒ 仍然复用。
func TestViewForEachNumber(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		let probes = 0;
		const [n, setN] = createSignal(2);
		const Cell = (v, i) => {
			probes = probes + 1;
			return <row note={"cell" + i}><text>{"#" + i}</text></row>;
		};
		render(
			<window title="view" width={400} height={300}>
				<column gap={2}><For each={() => n()}>{Cell}</For></column>
			</window>
		);
		globalThis.probeCount = () => probes;
		globalThis.grow = () => setN(4);
		globalThis.shrink = () => setN(1);
	`)

	var c0, c1 *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "cell0|cell1", "#0|#1")
			if got := callGlobalInspect(t, v, "probeCount"); got != "2" {
				t.Fatalf("渲染次数 = %v, want 2", got)
			}
			root := uiRoot(t)
			c0, c1 = viewOne(t, root, "cell0"), viewOne(t, root, "cell1")
		},
		func() { callGlobalFn(t, v, "grow") },
		func() {
			viewAssertLayout(t, uiRoot(t), "cell0|cell1|cell2|cell3", "#0|#1|#2|#3")
			if got := callGlobalInspect(t, v, "probeCount"); got != "4" {
				t.Fatalf("扩到 4 项后渲染次数 = %v, want 4 (前两项值相同 ⇒ 复用)", got)
			}
			root := uiRoot(t)
			if viewOne(t, root, "cell0") != c0 || viewOne(t, root, "cell1") != c1 {
				t.Fatalf("值相同的项被重建 (item 值比较失败)")
			}
		},
		func() { callGlobalFn(t, v, "shrink") },
		func() {
			viewAssertLayout(t, uiRoot(t), "cell0", "#0")
			if viewOne(t, uiRoot(t), "cell0") != c0 {
				t.Fatalf("缩到 1 项后幸存项被重建")
			}
		},
	})
}

// TestViewShowKeepsBranchesAlive Show 是 keep-alive 显隐: 隐藏的分支摘出
// 布局流**但保持挂载** —— 隐藏期间内容继续响应 signal, 再显示还是同一个节点。
// (这也是它不做 v-if 的原因: 本引擎销毁过的静态子树无法复活。)
func TestViewShowKeepsBranchesAlive(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { Show } from "gx/view";

		const [on, setOn] = createSignal(true);
		const [tag, setTag] = createSignal("x");
		render(
			<window title="view" width={400} height={300}>
				<column>
					<Show when={() => on()} fallback={<row note="fb"><text>隐藏中</text></row>}>
						<row note="body"><text>{() => tag()}</text></row>
					</Show>
				</column>
			</window>
		);
		globalThis.flip = () => setOn(!on());
		globalThis.retag = (s) => setTag(s);
	`)

	var body *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "body", "x")
			body = viewOne(t, uiRoot(t), "body")
		},
		func() { callGlobalFn(t, v, "retag", object.NewString("y")) },
		func() {
			viewAssertLayout(t, uiRoot(t), "body", "y")
		},
		func() { callGlobalFn(t, v, "flip") },
		func() {
			viewAssertLayout(t, uiRoot(t), "fb", "隐藏中")
			if len(viewNodesOf(uiRoot(t), "body")) != 0 {
				t.Fatalf("隐藏的分支不该出现在布局树里")
			}
		},
		func() { callGlobalFn(t, v, "retag", object.NewString("z")) }, // 隐藏期间改内容
		func() {
			if got := strings.Join(viewTexts(body), "|"); got != "z" {
				t.Fatalf("隐藏分支的内容 = %q, want %q (保活 ⇒ 仍在跟着 signal 走)", got, "z")
			}
		},
		func() { callGlobalFn(t, v, "flip") },
		func() {
			if viewOne(t, uiRoot(t), "body") != body {
				t.Fatalf("再显示时分支被重建 (keep-alive 应复用同一个节点)")
			}
			viewAssertLayout(t, uiRoot(t), "body", "z")
		},
	})
}

// TestViewBranchCleanupOnHostDispose 保活不是泄漏: 宿主(Show/Switch 的 slot, 或
// 装着它的列表行)被销毁时, **已经摘下的分支**也必须收尾 —— 它不在 Children 里,
// disposeNode 的递归走不到, 靠宿主 cleanups 上挂的那一发补收。这是 keep-alive
// 最容易漏的一环, 也是"Show 嵌在 For 行里"这种组合下的真实路径。
func TestViewBranchCleanupOnHostDispose(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal, onCleanup } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For, Show } from "gx/view";

		let log = "";
		const [rows, setRows] = createSignal([{ id: "a" }]);
		const [open, setOpen] = createSignal(true);
		const Panel = () => {
			onCleanup(() => { log = log + "panel;"; });
			return <row note="panel"><text>panel</text></row>;
		};
		const Row = (row) => (
			<row note={row.id}>
				<Show when={() => open()} fallback={<row note="off"><text>off</text></row>}>
					{Panel}
				</Show>
			</row>
		);
		render(
			<window title="view" width={400} height={300}>
				<column><For each={() => rows()} key={(r) => r.id}>{Row}</For></column>
			</window>
		);
		globalThis.cleanupLog = () => log;
		globalThis.hide = () => setOpen(false);
		globalThis.dropRow = () => setRows([]);
	`)

	runPumpSteps(t, v, fake, []func(){
		func() { viewAssertLayout(t, uiRoot(t), "a|panel", "panel") },
		func() { callGlobalFn(t, v, "hide") },
		func() {
			viewAssertLayout(t, uiRoot(t), "a|off", "off")
			if got := callGlobalInspect(t, v, "cleanupLog"); got != "" {
				t.Fatalf("隐藏不该触发清理 (保活), 日志 = %q", got)
			}
		},
		func() { callGlobalFn(t, v, "dropRow") }, // 整行销毁: 隐藏的分支也要跟着走
		func() {
			viewAssertLayout(t, uiRoot(t), "", "")
			if got := callGlobalInspect(t, v, "cleanupLog"); got != "panel;" {
				t.Fatalf("宿主销毁时隐藏分支的清理 = %q, want %q", got, "panel;")
			}
		},
	})
}

// TestViewShowWithoutWhenAndStaticFallback 缺 when 只警告不炸; 没有 fallback
// 时条件为假就是空宿主 (零尺寸, 对布局透明)。
func TestViewShowWithoutWhenAndStaticFallback(t *testing.T) {
	v, fake := evalUI(t, `
		import { h, render } from "gx/gfx";
		import { Show } from "gx/view";
		render(
			<window title="view" width={400} height={300}>
				<column>
					<Show><row note="never"><text>不该出现</text></row></Show>
					<Show when={() => false} fallback={<row note="fb"><text>兜底</text></row>}>
						<row note="body"><text>正文</text></row>
					</Show>
				</column>
			</window>
		);
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "fb", "兜底")
			if !viewWarned("Show: 缺少 when") {
				t.Fatalf("缺 when 应留下警告")
			}
		},
	})
}

// TestViewSwitchMatchFirstTruthyWins Switch/Match: 声明序取第一个为真的分支,
// 都不真用 fallback; 分支同样保活 (切回来还是同一个节点)。
func TestViewSwitchMatchFirstTruthyWins(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { Switch, Match } from "gx/view";

		const [phase, setPhase] = createSignal("loading");
		render(
			<window title="view" width={400} height={300}>
				<column>
					<Switch fallback={<row note="unknown"><text>未知状态</text></row>}>
						<Match when={() => phase() === "loading"}>
							<row note="loading"><text>加载中…</text></row>
						</Match>
						<Match when={() => phase() === "error"}>
							<row note="error"><text>出错了</text></row>
						</Match>
					</Switch>
				</column>
			</window>
		);
		globalThis.go = (p) => setPhase(p);
	`)

	var loading *GuiNode
	runPumpSteps(t, v, fake, []func(){
		func() {
			viewAssertLayout(t, uiRoot(t), "loading", "加载中…")
			loading = viewOne(t, uiRoot(t), "loading")
		},
		func() { callGlobalFn(t, v, "go", object.NewString("error")) },
		func() { viewAssertLayout(t, uiRoot(t), "error", "出错了") },
		func() { callGlobalFn(t, v, "go", object.NewString("nope")) },
		func() { viewAssertLayout(t, uiRoot(t), "unknown", "未知状态") },
		func() { callGlobalFn(t, v, "go", object.NewString("loading")) },
		func() {
			if viewOne(t, uiRoot(t), "loading") != loading {
				t.Fatalf("切回原分支时被重建 (keep-alive 应复用)")
			}
			viewAssertLayout(t, uiRoot(t), "loading", "加载中…")
		},
	})
}

// TestViewForIsLayoutTransparent 宿主 slot 对布局透明: 列表项的量出的盒子
// 与"直接写在父容器里"一致 (For 不会凭空多出一层盒子 / 缩进)。
func TestViewForIsLayoutTransparent(t *testing.T) {
	v, fake := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		import { For } from "gx/view";

		const [rows, setRows] = createSignal([{ id: "a" }, { id: "b" }]);
		const Row = (row) => <rect note={row.id} width={50} height={10}></rect>;
		render(
			<window title="view" width={400} height={300}>
				<column gap={4} padding={6}>
					<For each={() => rows()}>{Row}</For>
				</column>
			</window>
		);
		globalThis.drop = () => setRows([{ id: "a" }]);
	`)

	runPumpSteps(t, v, fake, []func(){
		func() {
			root := uiRoot(t)
			a, b := viewOne(t, root, "a"), viewOne(t, root, "b")
			// 列内边距 6 + gap 4: 两个 10 高的条从 y=6 起, 间距 4
			if a.Box.Y != b.Box.Y-14 {
				t.Fatalf("列表项间距 = %d, want 14 (10 高 + 4 gap); a=%v b=%v", b.Box.Y-a.Box.Y, a.Box, b.Box)
			}
			if a.Box.W != 50 || a.Box.H != 10 {
				t.Fatalf("列表项未按自身 width/height 布局: %v", a.Box)
			}
		},
		func() { callGlobalFn(t, v, "drop") },
		func() {
			root := uiRoot(t)
			if len(viewNodesOf(root, "b")) != 0 {
				t.Fatalf("删除的行不该留在布局树里")
			}
			if a := viewOne(t, root, "a"); a.Box.W != 50 {
				t.Fatalf("幸存行布局被扰动: %v", a.Box)
			}
		},
	})
}
