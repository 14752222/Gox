package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 网格布局 (§四 布局缺口, 2026-09-19): <grid columns={n}> =====

// gridKid 显式宽高的子节点; gridAuto 只有高 (宽走格内拉伸)。
func gridKid(w, h float64) *GuiNode {
	return mkNode("rect", map[string]float64{"width": w, "height": h})
}

func gridAuto(h float64) *GuiNode {
	return mkNode("rect", map[string]float64{"height": h})
}

func TestGridLayoutBasic(t *testing.T) {
	// 300 宽 3 列无 gap: 每列 100; 第 4 个折到第 2 行第 1 列
	root := mkNode("grid", map[string]float64{"columns": 3})
	kids := []*GuiNode{gridAuto(20), gridAuto(20), gridAuto(20), gridAuto(20)}
	root.Children = kids
	Layout(root, 300, 200)
	want := []Rect{
		{0, 0, 100, 20}, {100, 0, 100, 20}, {200, 0, 100, 20}, {0, 20, 100, 20},
	}
	for i, k := range kids {
		if k.Box != want[i] {
			t.Fatalf("子 %d = %v, want %v", i, k.Box, want[i])
		}
	}
}

func TestGridLayoutGap(t *testing.T) {
	// 310 宽 2 列 gap 10: 每列 (310-10)/2=150, x = 0 / 160
	root := mkNode("grid", map[string]float64{"columns": 2, "gap": 10})
	a, b := gridAuto(20), gridAuto(20)
	root.Children = []*GuiNode{a, b}
	Layout(root, 310, 100)
	if a.Box.W != 150 || b.Box.X != 160 || b.Box.W != 150 {
		t.Fatalf("gap 列宽: a=%v b=%v, want w=150 x=160", a.Box, b.Box)
	}
}

func TestGridLayoutRowHeightMax(t *testing.T) {
	// 行高取该行最高者; 下一行 y 按上一行行高排
	root := mkNode("grid", map[string]float64{"columns": 2})
	short, tall := gridAuto(20), gridAuto(40)
	next := gridAuto(20)
	root.Children = []*GuiNode{short, tall, next}
	Layout(root, 200, 200)
	if tall.Box.H != 40 || short.Box.H != 20 {
		t.Fatalf("显式高保留: short=%v tall=%v", short.Box, tall.Box)
	}
	if next.Box.Y != 40 {
		t.Fatalf("第二行 y 应为行高 40: %v", next.Box)
	}
	// 矮项默认顶部对齐 (非容器保持固有高度)
	if short.Box.Y != 0 {
		t.Fatalf("矮项应顶部对齐: %v", short.Box)
	}
}

func TestGridLayoutContainerStretchesToRow(t *testing.T) {
	// 无显式高的容器子节点拉伸到行高 (卡片等高)
	root := mkNode("grid", map[string]float64{"columns": 2})
	card := mkNode("column", nil) // 无宽无高
	tall := gridKid(0, 50)
	root.Children = []*GuiNode{card, tall}
	Layout(root, 200, 200)
	if card.Box.W != 100 {
		t.Fatalf("容器应拉伸到列宽: %v", card.Box)
	}
	if card.Box.H != 50 {
		t.Fatalf("容器应拉伸到行高: %v", card.Box)
	}
}

func TestGridLayoutAlignCenterInCell(t *testing.T) {
	// alignItems=center: 显式尺寸的子节点在格内两轴居中 (不再拉伸)
	root := mkNode("grid", nil)
	root.Props["columns"] = object.NewNumber(1)
	root.Props["alignItems"] = object.NewString("center")
	kid := gridKid(40, 20)
	root.Children = []*GuiNode{kid}
	Layout(root, 100, 60) // 行高 20, 格 100x20 → ox=(100-40)/2=30, oy=0
	if kid.Box.X != 30 || kid.Box.Y != 0 || kid.Box.W != 40 {
		t.Fatalf("center: %v, want x=30 w=40", kid.Box)
	}
}

func TestGridLayoutPercentChild(t *testing.T) {
	// 百分比子宽按网格内容区解析 (2 列各 100, 子宽 50% = 100)
	root := mkNode("grid", map[string]float64{"columns": 2})
	kid := withStr(gridKid(0, 20), "width", "50%")
	root.Children = []*GuiNode{kid}
	Layout(root, 200, 100)
	if kid.Box.W != 100 {
		t.Fatalf("百分比子宽 = %d, want 100 (200×50%%)", kid.Box.W)
	}
}

func TestGridLayoutAutoWidthContentTracks(t *testing.T) {
	// row 父里 auto 宽的网格: 固有宽 = 内容轨道 (列取最大子宽)
	outer := mkNode("row", nil)
	grid := mkNode("grid", map[string]float64{"columns": 2})
	wide := gridKid(80, 20)
	narrow := gridKid(30, 20)
	sibling := gridKid(50, 20)
	grid.Children = []*GuiNode{wide, narrow}
	outer.Children = []*GuiNode{grid, sibling}
	Layout(outer, 1000, 100)
	if grid.Box.W != 110 {
		t.Fatalf("网格固有宽 = %d, want 80+30", grid.Box.W)
	}
	// 拿到确定宽后等宽列: 110/2=55, 窄项拉伸? 显式宽 30 保留, 宽项 80 保留
	if wide.Box.W != 80 || narrow.Box.W != 30 {
		t.Fatalf("显式子宽保留: wide=%v narrow=%v", wide.Box, narrow.Box)
	}
}

func TestGridLayoutColumnsClamped(t *testing.T) {
	// columns=0 → 1 列 (退化成纵向堆叠); 99 → 32 上限不炸
	root := mkNode("grid", nil)
	root.Props["columns"] = object.NewNumber(0)
	a, b := gridKid(0, 10), gridKid(0, 10)
	root.Children = []*GuiNode{a, b}
	Layout(root, 100, 100)
	if a.Box.Y != 0 || b.Box.Y != 10 || b.Box.X != 0 {
		t.Fatalf("columns=0 应按 1 列: a=%v b=%v", a.Box, b.Box)
	}

	root2 := mkNode("grid", nil)
	root2.Props["columns"] = object.NewNumber(99)
	kids := make([]*GuiNode, 33)
	for i := range kids {
		kids[i] = gridKid(0, 10)
	}
	root2.Children = kids
	Layout(root2, 3200, 100)
	if kids[32].Box.X != 0 || kids[32].Box.Y != 10 {
		t.Fatalf("32 列上限: 第 33 个应折行: %v", kids[32].Box)
	}
}
