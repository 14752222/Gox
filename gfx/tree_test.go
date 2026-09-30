package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4 tree =====
//
// 用例覆盖五条不变量:
//   1. nodes 数据被展开成可见行的节点树, 收起的分支不物化;
//   2. 层级用缩进表达, 缩进随深度递增且同层文字左对齐;
//   3. 点击有子节点的行切换展开态, 重建后其它分支的展开态保留;
//   4. 叶子行不挂切换处理器; onSelect 收到 {label,key,depth?} 形态的节点;
//   5. 行高统一, 行按 y 等距排布。

// mkTreeNode 造一个树节点对象 {label, children?}。
func mkTreeNode(label string, children ...*object.Object) *object.Object {
	o := object.NewObject()
	o.SetProperty("label", object.NewString(label))
	o.SetProperty("key", object.NewString(label))
	if len(children) > 0 {
		arr := &object.Array{}
		for _, c := range children {
			arr.Elements = append(arr.Elements, c)
		}
		o.SetProperty("children", arr)
	}
	return o
}

// mkTree 造一个 tree 节点。
func mkTree(nodes ...*object.Object) *GuiNode {
	t := &GuiNode{Tag: "tree", Props: map[string]object.Value{}}
	arr := &object.Array{}
	for _, n := range nodes {
		arr.Elements = append(arr.Elements, n)
	}
	t.Props["nodes"] = arr
	return t
}

func TestTreeShowsOnlyExpandedRows(t *testing.T) {
	// src (有子) + README (叶子)
	tbl := mkTree(
		mkTreeNode("src", mkTreeNode("main.go"), mkTreeNode("gfx")),
		mkTreeNode("README.md"),
	)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 300, 300)

	// 初次全收起: 只有顶层 2 行可见
	rows := treeRows(tbl)
	if len(rows) != 2 {
		t.Fatalf("初始可见行数 = %d, want 2 (树缺省全收起)", len(rows))
	}
	if rows[0].treeLabel != "src" || rows[1].treeLabel != "README.md" {
		t.Fatalf("顶层行文本不对: %q / %q", rows[0].treeLabel, rows[1].treeLabel)
	}
	// src 可展开, README 是叶子
	if !rows[0].treeExpandable {
		t.Fatalf("src 应标记可展开")
	}
	if rows[1].treeExpandable {
		t.Fatalf("README.md 是叶子, 不该标记可展开")
	}
}

func TestTreeExpandsOnClick(t *testing.T) {
	tbl := mkTree(
		mkTreeNode("src", mkTreeNode("main.go"), mkTreeNode("gfx")),
		mkTreeNode("README.md"),
	)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)

	fake, a := mountTestApp(t, root, 300, 300)
	rows := treeRows(tbl)
	src := rows[0]

	// 点 src 行 → 展开, 多出 2 个子行
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: src.Box.X + 2, Y: src.Box.Y + 2})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: src.Box.X + 2, Y: src.Box.Y + 2})

	rows = treeRows(tbl)
	if len(rows) != 4 {
		t.Fatalf("展开后可见行数 = %d, want 4", len(rows))
	}
	// 子行紧跟父行, 层级 1, 文字正确
	if rows[1].treeLabel != "main.go" || rows[1].treeDepth != 1 {
		t.Fatalf("首个子行不对: %q depth=%d", rows[1].treeLabel, rows[1].treeDepth)
	}
	if rows[2].treeLabel != "gfx" || rows[2].treeDepth != 1 {
		t.Fatalf("次个子行不对: %q depth=%d", rows[2].treeLabel, rows[2].treeDepth)
	}
	// README 仍在最后
	if rows[3].treeLabel != "README.md" {
		t.Fatalf("展开后 README 位置不对: %q", rows[3].treeLabel)
	}
	// src 自身标记展开
	if !rows[0].treeOpen {
		t.Fatalf("src 未标记展开态")
	}

	// 再点一次 → 收起
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: src.Box.X + 2, Y: src.Box.Y + 2})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: src.Box.X + 2, Y: src.Box.Y + 2})
	if n := len(treeRows(tbl)); n != 2 {
		t.Fatalf("再次点击后可见行数 = %d, want 2", n)
	}
}

func TestTreeKeepsOtherBranchesExpanded(t *testing.T) {
	// 两个可展开的顶层分支
	tbl := mkTree(
		mkTreeNode("a", mkTreeNode("a1")),
		mkTreeNode("b", mkTreeNode("b1")),
	)
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	fake, a := mountTestApp(t, root, 300, 300)

	// 展开 a
	clickRow(t, fake, a, treeRows(tbl)[0])
	if rows := treeRows(tbl); len(rows) != 3 {
		t.Fatalf("展开 a 后行数 = %d, want 3", len(rows))
	}
	// 展开 b: a 应保持展开 (展开态跨重建持久)
	clickRow(t, fake, a, treeRows(tbl)[2]) // 此刻 [a, a1, b] → b 在下标 2
	rows := treeRows(tbl)
	if len(rows) != 4 {
		t.Fatalf("展开 b 后行数 = %d, want 4", len(rows))
	}
	if !rows[0].treeOpen {
		t.Fatalf("展开 b 之后 a 被误收起 (展开态没跨重建保留)")
	}
	// 全部展开后顺序是 [a, a1, b, b1] —— b 在下标 2
	if rows[2].treeLabel != "b" || !rows[2].treeOpen {
		t.Fatalf("b 未标记展开态 (label=%q open=%v)", rows[2].treeLabel, rows[2].treeOpen)
	}
	if rows[3].treeLabel != "b1" || rows[3].treeDepth != 1 {
		t.Fatalf("b 的子行不对: %q depth=%d", rows[3].treeLabel, rows[3].treeDepth)
	}
}

func TestTreeIndentIncreasesWithDepth(t *testing.T) {
	tbl := mkTree(mkTreeNode("a", mkTreeNode("a1")))
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	fake, a := mountTestApp(t, root, 300, 300)
	clickRow(t, fake, a, treeRows(tbl)[0])

	rows := treeRows(tbl)
	if len(rows) != 2 {
		t.Fatalf("行数 = %d, want 2", len(rows))
	}
	// 层级递增
	if rows[0].treeDepth != 0 || rows[1].treeDepth != 1 {
		t.Fatalf("深度不对: %d / %d", rows[0].treeDepth, rows[1].treeDepth)
	}
	// 行高统一 + y 等距
	if rows[0].Box.H != treeRowH || rows[1].Box.H != treeRowH {
		t.Fatalf("行高不是 %d: %d / %d", treeRowH, rows[0].Box.H, rows[1].Box.H)
	}
	if rows[1].Box.Y != rows[0].Box.Y+treeRowH {
		t.Fatalf("行 y 不等距: %d → %d", rows[0].Box.Y, rows[1].Box.Y)
	}
	// 缩进在绘制期生效: 子行的文字墨迹应比父行靠右 → 用箭头列位置间接验证。
	// 这里直接验语义字段 (绘制坐标由 paintTreeRow 按 treeDepth 计算)。
	if rows[1].treeDepth*treeIndent <= rows[0].treeDepth*treeIndent {
		t.Fatalf("子行缩进未大于父行")
	}
}

func TestTreeLeafHasNoToggle(t *testing.T) {
	tbl := mkTree(mkTreeNode("only-leaf"))
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 300, 200)

	rows := treeRows(tbl)
	if len(rows) != 1 {
		t.Fatalf("行数 = %d, want 1", len(rows))
	}
	if rows[0].PropHandler("onClick") != nil {
		t.Fatalf("叶子行不该挂切换处理器 (点了没有可展开的东西)")
	}
}

func TestTreeSelectCallback(t *testing.T) {
	tbl := mkTree(mkTreeNode("leaf"))
	var gotLabel string
	var gotLeaf bool
	var gotIndex float64 = -1
	tbl.Props["onSelect"] = object.NewBuiltin("select", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if pv, ok := o.GetProperty("label"); ok {
					if s, ok := pv.(*object.String); ok {
						gotLabel = s.Value
					}
				}
				if pv, ok := o.GetProperty("leaf"); ok {
					if b, ok := pv.(*object.Boolean); ok {
						gotLeaf = b.Value
					}
				}
				if pv, ok := o.GetProperty("index"); ok {
					if n, ok := pv.(*object.Number); ok {
						gotIndex = n.Value
					}
				}
			}
		}
		return object.UndefinedSingleton
	})

	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	fake, a := mountTestApp(t, root, 300, 200)
	clickRow(t, fake, a, treeRows(tbl)[0])

	if gotLabel != "leaf" {
		t.Fatalf("onSelect label = %q, want leaf", gotLabel)
	}
	if !gotLeaf {
		t.Fatalf("onSelect leaf 应为 true")
	}
	if gotIndex != 0 {
		t.Fatalf("onSelect index = %v, want 0", gotIndex)
	}
}

func TestTreeStringNodesShorthand(t *testing.T) {
	// nodes={["a","b"]} 简写: 纯字符串当叶子
	tbl := &GuiNode{Tag: "tree", Props: map[string]object.Value{}}
	arr := &object.Array{}
	arr.Elements = append(arr.Elements, object.NewString("a"), object.NewString("b"))
	tbl.Props["nodes"] = arr

	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 300, 200)

	rows := treeRows(tbl)
	if len(rows) != 2 {
		t.Fatalf("字符串简写的行数 = %d, want 2", len(rows))
	}
	if rows[0].treeLabel != "a" || rows[1].treeLabel != "b" {
		t.Fatalf("字符串简写文本不对: %q / %q", rows[0].treeLabel, rows[1].treeLabel)
	}
}

func TestTreeEmptyNodes(t *testing.T) {
	tbl := mkTree()
	root := mkNode("column", map[string]float64{})
	mountChildren(root, tbl)
	renderTree(root, 300, 100)
	if n := len(treeRows(tbl)); n != 0 {
		t.Fatalf("空树不该有行, got %d", n)
	}
}

// clickRow 在一次行盒中心按下并抬起 (复用事件泵的完整按下-抬起链路)。
func clickRow(t *testing.T, fake *fakeSurface, a *app, row *GuiNode) {
	t.Helper()
	x, y := row.Box.X+2, row.Box.Y+row.Box.H/2
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: x, Y: y})
}
