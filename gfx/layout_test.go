package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 基础 flex 布局: margin / 对齐 / 弹性分配 / 文本固有尺寸 =====
//
// (弹性词汇 —— 百分比 / min-max / flexShrink / wrap —— 的用例在
// layout_elastic_test.go; 网格在 grid_test.go。)

func TestLayoutMarginAlignJustify(t *testing.T) {
	// margin: 10 → 子节点偏移且占位
	root := mkNode("column", nil)
	a := mkNode("rect", map[string]float64{"width": 50, "height": 10, "margin": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 10 || a.Box.Y != 10 {
		t.Fatalf("margin box = %v, want (10,10)", a.Box)
	}

	// alignItems center: 交叉轴居中 (未显式宽的节点用固有尺寸 50)
	root = mkNode("column", nil)
	root.Props["alignItems"] = object.NewString("center")
	a = mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != (400-50)/2 {
		t.Fatalf("align center x = %d, want %d", a.Box.X, (400-50)/2)
	}

	// alignItems end
	root = mkNode("column", nil)
	root.Props["alignItems"] = object.NewString("end")
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 400-50 {
		t.Fatalf("align end x = %d", a.Box.X)
	}

	// justifyContent center (主轴垂直)
	root = mkNode("column", nil)
	root.Props["justifyContent"] = object.NewString("center")
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.Y != (300-10)/2 {
		t.Fatalf("justify center y = %d", a.Box.Y)
	}

	// justifyContent between: 两节点分布到两端
	root = mkNode("row", nil)
	root.Props["justifyContent"] = object.NewString("between")
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)
	if a.Box.X != 0 || b.Box.X != 400-50 {
		t.Fatalf("justify between: a=%v b=%v", a.Box, b.Box)
	}
}

func TestLayoutFlexGrow(t *testing.T) {
	// row 400 宽: a=100 + b(flexGrow:1) → b 撑满剩余 300
	root := mkNode("row", nil)
	a := mkNode("rect", map[string]float64{"width": 100, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10, "flexGrow": 1})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)
	if b.Box.X != 100 || b.Box.W != 300 {
		t.Fatalf("flexGrow: b=%v, want x=100 w=300", b.Box)
	}
}

func TestLayoutTextIntrinsic(t *testing.T) {
	requireFont(t)
	// text 节点无显式尺寸 → 按字体测量固有宽
	root := mkNode("column", nil)
	label := &GuiNode{Tag: "text", Props: map[string]object.Value{
		"font": object.NewNumber(20),
	}}
	label.Children = []*GuiNode{{Tag: "#text", Text: "count: 42", Props: map[string]object.Value{}}}
	root.Children = []*GuiNode{label}
	Layout(root, 400, 300)

	tw, _ := MeasureText("count: 42", 20)
	if label.Box.W != tw {
		t.Fatalf("text intrinsic width = %d, want %d", label.Box.W, tw)
	}
	if label.Box.H < 20 {
		t.Fatalf("text height too small: %d", label.Box.H)
	}
}
