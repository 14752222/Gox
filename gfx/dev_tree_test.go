package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== [M3] Inspector v1: 元素树查看 (gx/dev 的 devTree/devNode/devProps) =====
//
// 断言策略: 真 VM 挂载一棵真树, 经 gx/dev 的 JS 内置取回快照对象, 再在 Go 侧
// 逐字段校验 —— 与 dev_test.go 的 TestDevSnapshotShape 同一路子 (字段名是 API)。

// resetDevApps 把窗口注册表清空 (包级状态, 用例之间不许串味)。
//
// 为什么必须清: devTree 遍历 appsSnapshot, 前一个用例遗留的窗口会让
// "windows.length == 1" 这类断言随机失败; 而注册表是包级单例, 只有显式清。
func resetDevApps(t *testing.T) {
	t.Helper()
	appMu.Lock()
	apps = nil
	activeApp = nil
	appMu.Unlock()
	t.Cleanup(func() {
		appMu.Lock()
		apps = nil
		activeApp = nil
		appMu.Unlock()
	})
}

func TestDevTreeShapeAndPaths(t *testing.T) {
	resetDevApps(t)
	resetDevState()
	t.Cleanup(resetDevState)

	v, _ := evalUI(t, `
		import { h, render } from "gx/gfx";
		import { devTree, devNode, devProps } from "gx/dev";
		const circ = {}; circ.self = circ;          // 循环引用: 序列化必须不 panic
		render(h("column", {gap: 4},
			h("row", {key: "r1"},
				h("button", {onClick: () => {}}, "hi"),
				h("rect", {width: 10, height: 5, data: circ})),
			h("text", {font: 13}, "x")));
		globalThis.g_tree = devTree();
		globalThis.g_node = devNode("0/0/0");
		globalThis.g_props = devProps("0/0");
		globalThis.g_data = devProps("0/0/1");
		globalThis.g_missing = devNode("0/9");
	`)

	tree := asObj(t, globalVal(t, v, "g_tree"), "devTree()")
	arr, ok := propOf(t, tree, "windows").(*object.Array)
	if !ok || len(arr.Elements) != 1 {
		t.Fatalf("windows 应为长度 1 的数组, 得到 %s", propOf(t, tree, "windows").Inspect())
	}

	win := asObj(t, arr.Elements[0], "window")
	if propNum(t, win, "id") == 0 {
		t.Fatalf("window.id 应为非零窗口号")
	}
	if got := propStr(t, win, "scope"); got == "" {
		t.Fatalf("window.scope 不能为空")
	}

	root := asObj(t, propOf(t, win, "root"), "root")
	if got := propStr(t, root, "tag"); got != "column" {
		t.Fatalf("root.tag = %q, want column", got)
	}
	if got := propStr(t, root, "path"); got != "0" {
		t.Fatalf("root.path = %q, want 0", got)
	}
	// box 形状
	box := asObj(t, propOf(t, root, "box"), "root.box")
	for _, k := range []string{"x", "y", "w", "h"} {
		_ = propNum(t, box, k) // 缺失会 Fatalf
	}
	if _, ok := propOf(t, root, "focusable").(*object.Boolean); !ok {
		t.Fatalf("focusable 应为布尔")
	}

	kids := propOf(t, root, "children").(*object.Array)
	if len(kids.Elements) != 2 {
		t.Fatalf("root 子节点数 = %d, want 2", len(kids.Elements))
	}
	row := asObj(t, kids.Elements[0], "row")
	if propStr(t, row, "path") != "0/0" || propStr(t, row, "tag") != "row" {
		t.Fatalf("row 形状不对: %s", row.Inspect())
	}
	if got := propStr(t, row, "key"); got != "r1" {
		t.Fatalf("row.key = %q, want r1", got)
	}

	// 函数值 prop → "[Function]"
	btn := asObj(t, devChildrenOf(t, row, 0), "button")
	if propStr(t, btn, "tag") != "button" {
		t.Fatalf("0/0/0 应为 button")
	}
	bprops := asObj(t, propOf(t, btn, "props"), "button.props")
	if got := propStr(t, bprops, "onClick"); got != "[Function]" {
		t.Fatalf("函数 prop 应序列化成 [Function], 得到 %q", got)
	}
	// #text 子节点带 text
	txt := asObj(t, devChildrenOf(t, btn, 0), "text 子节点")
	if propStr(t, txt, "tag") != "#text" || propStr(t, txt, "text") != "hi" {
		t.Fatalf("文本子节点形状不对: %s", txt.Inspect())
	}

	// devNode 命中
	node := asObj(t, globalVal(t, v, "g_node"), "devNode")
	if propStr(t, node, "tag") != "button" || propStr(t, node, "path") != "0/0/0" {
		t.Fatalf("devNode(0/0/0) 形状不对: %s", node.Inspect())
	}

	// devProps 只返回 props
	props := asObj(t, globalVal(t, v, "g_props"), "devProps")
	if got := propStr(t, props, "key"); got != "r1" {
		t.Fatalf("devProps(0/0).key = %q, want r1", got)
	}

	// 循环引用被打破
	dataProps := asObj(t, globalVal(t, v, "g_data"), "devProps(rect)")
	circ := asObj(t, propOf(t, dataProps, "data"), "data")
	selfVal := propOf(t, circ, "self")
	if s, ok := selfVal.(*object.String); !ok || s.Value != "[Circular]" {
		t.Fatalf("循环引用应序列化为 [Circular], 得到 %s", selfVal.Inspect())
	}

	// 未命中 → null
	if _, isNull := globalVal(t, v, "g_missing").(*object.Null); !isNull {
		t.Fatalf("devNode(\"0/9\") 应为 null, 得到 %s", globalVal(t, v, "g_missing").Inspect())
	}
}

func TestDevTreeTruncation(t *testing.T) {
	resetDevApps(t)
	resetDevState()
	t.Cleanup(resetDevState)

	v, _ := evalUI(t, `
		import { h, render } from "gx/gfx";
		import { devTree } from "gx/dev";
		const deep = (d) => d === 0 ? h("rect", {width: 1, height: 1})
		                            : h("column", null, deep(d - 1));
		render(deep(12));                       // 13 个节点, 深度 12
		globalThis.g_deep = devTree();           // 默认边界: depth 8 / nodes 2000
		globalThis.g_shallow = devTree({ maxDepth: 2 });
		globalThis.g_tiny = devTree({ maxNodes: 4 });
	`)

	// 默认 maxDepth=8: 深度 12 的链会被截断
	deep := asObj(t, globalVal(t, v, "g_deep"), "g_deep")
	if !propBool(t, deep, "truncated") {
		t.Fatalf("默认边界下 13 深的链应被标记 truncated")
	}
	if d := devSerializedDepth(t, deep); d > devTreeDefaultMaxDepth+1 {
		t.Fatalf("默认展开深度 = %d, 超过上限 %d", d, devTreeDefaultMaxDepth+1)
	}

	// maxDepth=2: 只有三层
	shallow := asObj(t, globalVal(t, v, "g_shallow"), "g_shallow")
	if !propBool(t, shallow, "truncated") {
		t.Fatalf("maxDepth=2 应截断")
	}
	if d := devSerializedDepth(t, shallow); d != 3 {
		t.Fatalf("maxDepth=2 展开深度 = %d, want 3 (根+2 层)", d)
	}

	// maxNodes=4: 序列化节点数不超过 4
	tiny := asObj(t, globalVal(t, v, "g_tiny"), "g_tiny")
	if !propBool(t, tiny, "truncated") {
		t.Fatalf("maxNodes=4 应截断")
	}
	if n := devCountNodes(t, tiny); n > 4 {
		t.Fatalf("maxNodes=4 却序列化了 %d 个节点", n)
	}
}

// ===== 测试辅助 =====

func asObj(t *testing.T, v object.Value, what string) *object.Object {
	t.Helper()
	o, ok := v.(*object.Object)
	if !ok {
		t.Fatalf("%s 应为对象, 得到 %s", what, v.Inspect())
	}
	return o
}

func propOf(t *testing.T, o *object.Object, name string) object.Value {
	t.Helper()
	v, ok := o.GetProperty(name)
	if !ok {
		t.Fatalf("缺属性 %s", name)
	}
	return v
}

func propStr(t *testing.T, o *object.Object, name string) string {
	t.Helper()
	s, ok := propOf(t, o, name).(*object.String)
	if !ok {
		t.Fatalf("%s 应为字符串, 得到 %s", name, propOf(t, o, name).Inspect())
	}
	return s.Value
}

func propNum(t *testing.T, o *object.Object, name string) float64 {
	t.Helper()
	n, ok := propOf(t, o, name).(*object.Number)
	if !ok {
		t.Fatalf("%s 应为数字, 得到 %s", name, propOf(t, o, name).Inspect())
	}
	return n.Value
}

func propBool(t *testing.T, o *object.Object, name string) bool {
	t.Helper()
	b, ok := propOf(t, o, name).(*object.Boolean)
	if !ok {
		t.Fatalf("%s 应为布尔, 得到 %s", name, propOf(t, o, name).Inspect())
	}
	return b.Value
}

// devChildrenOf 取节点对象 children[i]。
func devChildrenOf(t *testing.T, node *object.Object, i int) object.Value {
	t.Helper()
	arr, ok := propOf(t, node, "children").(*object.Array)
	if !ok {
		t.Fatalf("children 应为数组")
	}
	if i < 0 || i >= len(arr.Elements) {
		t.Fatalf("children[%d] 越界 (共 %d)", i, len(arr.Elements))
	}
	return arr.Elements[i]
}

// devSerializedDepth 返回快照里第一棵窗口树的展开深度 (根计 1)。
func devSerializedDepth(t *testing.T, tree *object.Object) int {
	t.Helper()
	wins := propOf(t, tree, "windows").(*object.Array)
	root := asObj(t, propOf(t, asObj(t, wins.Elements[0], "win"), "root"), "root")
	return devNodeDepth(root)
}

func devNodeDepth(node *object.Object) int {
	arr, ok := node.GetProperty("children")
	if !ok {
		return 1
	}
	ca, ok := arr.(*object.Array)
	if !ok || len(ca.Elements) == 0 {
		return 1
	}
	max := 0
	for _, e := range ca.Elements {
		c, ok := e.(*object.Object)
		if !ok {
			continue
		}
		if d := devNodeDepth(c); d > max {
			max = d
		}
	}
	return max + 1
}

// devCountNodes 统计快照里所有窗口序列化出的节点总数。
func devCountNodes(t *testing.T, tree *object.Object) int {
	t.Helper()
	wins := propOf(t, tree, "windows").(*object.Array)
	total := 0
	for _, w := range wins.Elements {
		wo, ok := w.(*object.Object)
		if !ok {
			continue
		}
		if rv, ok := wo.GetProperty("root"); ok {
			if ro, ok := rv.(*object.Object); ok {
				total += devCountNode(ro)
			}
		}
	}
	return total
}

func devCountNode(node *object.Object) int {
	n := 1
	if cv, ok := node.GetProperty("children"); ok {
		if ca, ok := cv.(*object.Array); ok {
			for _, e := range ca.Elements {
				if c, ok := e.(*object.Object); ok {
					n += devCountNode(c)
				}
			}
		}
	}
	return n
}
