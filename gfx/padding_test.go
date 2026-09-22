package gfx

import "testing"

// ===== padding 四边化 (2026-09-21): 基准值 + 单边覆盖 =====
//
// 语义 (gfx/layout.go 的 paddingOf): 统一的 `padding` 是**基准值**, 四边的
// paddingTop/Right/Bottom/Left 在它之上做**覆盖** —— 给了哪边就用哪边, 没给就
// 跟随基准值。所以
//
//	padding={12} paddingTop={40}  →  上 40 / 其余 12
//
// 而**不是** 52 (相加)。这条语义正被 gx/viewport 的 safeAreaStyle() 依赖: 应用
// 写的是"通用内边距", 只有被状态栏/刘海压住的那一边需要额外让开。写成相加会让
// 安全区叠出一个谁都解释不清的数字, 所以下面逐条钉住"是覆盖不是相加"。
//
// 四边化之前, 全仓的 padding 都按 `2*p` 算 (inner / stackContentSize /
// gridContentSize / wrapStackCrossTotal 四处都改过), 所以这里对**尺寸**也要
// 断言 —— 只改 inner 不改尺寸会让"单边加大"的容器把内容裁掉。

// TestPaddingOfBase 只给 padding 时四边相同; 缺失与负数都归 0。
func TestPaddingOfBase(t *testing.T) {
	n := mkNode("column", map[string]float64{"padding": 12})
	top, right, bottom, left := paddingOf(n)
	if top != 12 || right != 12 || bottom != 12 || left != 12 {
		t.Fatalf("四边应都是 12, 实际 t=%d r=%d b=%d l=%d", top, right, bottom, left)
	}

	none := mkNode("column", nil)
	if tp, r, b, l := paddingOf(none); tp != 0 || r != 0 || b != 0 || l != 0 {
		t.Fatalf("没给 padding 应四边全 0, 实际 t=%d r=%d b=%d l=%d", tp, r, b, l)
	}

	neg := mkNode("column", map[string]float64{"padding": -5})
	if tp, r, b, l := paddingOf(neg); tp != 0 || r != 0 || b != 0 || l != 0 {
		t.Fatalf("负 padding 应钳 0, 实际 t=%d r=%d b=%d l=%d", tp, r, b, l)
	}
}

// TestPaddingOfPerSideOverride 单边覆盖基准值 —— **不是相加**。
//
// 这是本文件最重要的一条: 相加会让 padding={12} + paddingTop={40} 得到 52,
// 而没有任何一处能解释那个数字从哪来。
func TestPaddingOfPerSideOverride(t *testing.T) {
	n := mkNode("column", map[string]float64{"padding": 12, "paddingTop": 40})
	top, right, bottom, left := paddingOf(n)
	if top != 40 {
		t.Fatalf("paddingTop 应**覆盖**基准值 → 40 (相加会得 52), 实际 %d", top)
	}
	if right != 12 || bottom != 12 || left != 12 {
		t.Fatalf("未被覆盖的三边应跟随基准值 12, 实际 r=%d b=%d l=%d", right, bottom, left)
	}

	// 四边各自独立覆盖 (没有 padding 基准时, 没给的边就是 0)
	n2 := mkNode("column", map[string]float64{
		"paddingTop": 2, "paddingRight": 16, "paddingBottom": 4, "paddingLeft": 8,
	})
	if tp, r, b, l := paddingOf(n2); tp != 2 || r != 16 || b != 4 || l != 8 {
		t.Fatalf("四边独立覆盖错: t=%d r=%d b=%d l=%d", tp, r, b, l)
	}

	// 单边给负数 → 那一边钳 0, 且不影响其它边
	n3 := mkNode("column", map[string]float64{"padding": 10, "paddingTop": -3})
	if tp, r, _, _ := paddingOf(n3); tp != 0 || r != 10 {
		t.Fatalf("负的单边应钳 0 且不牵连其它边: t=%d r=%d", tp, r)
	}
}

// TestPaddingPerSideIntrinsicSize 容器的固有尺寸要按**四边**加, 而不是 2×基准。
//
// 用"四边不等且基准为 0"的输入, 才能把两种实现区分开: 按 2×基准 算会得 20
// (看着还挺合理), 正确值是 60。
func TestPaddingPerSideIntrinsicSize(t *testing.T) {
	// column: 上 30 / 下 10, 子节点 100×20
	root := mkNode("column", map[string]float64{"paddingTop": 30, "paddingBottom": 10})
	root.Children = []*GuiNode{mkNode("rect", map[string]float64{"width": 100, "height": 20})}
	w, h := root.intrinsicSize()
	if h != 60 {
		t.Fatalf("column 固有高应 = 20+30+10 = 60 (按 2×基准 会得 20), 实际 %d", h)
	}
	if w != 100 {
		t.Fatalf("左右没给 padding, 宽应 = 100, 实际 %d", w)
	}

	// row: 左 30 / 右 10, 主轴是宽
	row := mkNode("row", map[string]float64{"paddingLeft": 30, "paddingRight": 10})
	row.Children = []*GuiNode{mkNode("rect", map[string]float64{"width": 100, "height": 20})}
	rw, rh := row.intrinsicSize()
	if rw != 140 {
		t.Fatalf("row 固有宽应 = 100+30+10 = 140, 实际 %d", rw)
	}
	if rh != 20 {
		t.Fatalf("row 上下没给 padding, 高应 = 20, 实际 %d", rh)
	}
}

// TestPaddingOverrideIntrinsicSize 基准值 + 单边覆盖在**尺寸**上也是覆盖不是相加。
//
// padding=12 + paddingTop=40 ⇒ 上 40 / 下 12 ⇒ 20+40+12 = 72。
// 若实现成相加 (上 52 / 下 12) 会得 84。
func TestPaddingOverrideIntrinsicSize(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12, "paddingTop": 40})
	root.Children = []*GuiNode{mkNode("rect", map[string]float64{"width": 100, "height": 20})}
	w, h := root.intrinsicSize()
	if h != 72 {
		t.Fatalf("固有高应 = 20+40+12 = 72 (相加会得 84), 实际 %d", h)
	}
	if w != 124 {
		t.Fatalf("未被覆盖的左右各 12 ⇒ 宽应 = 100+24 = 124, 实际 %d", w)
	}
}

// TestPaddingAffectsChildBox 内容区偏移: 子节点从 padding 之后开始排。
func TestPaddingAffectsChildBox(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12, "paddingTop": 40})
	child := mkNode("rect", map[string]float64{"width": 50, "height": 20})
	root.Children = []*GuiNode{child}
	Layout(root, 200, 300)
	if child.Box.X != 12 || child.Box.Y != 40 {
		t.Fatalf("子节点应从 (12,40) 开始 (左 12 / 上 40), 实际 (%d,%d)",
			child.Box.X, child.Box.Y)
	}
}

// TestPaddingGridContentSize 网格的固有尺寸同样按四边加。
//
// gridContentSize 是四处改动之一, 单独钉一条 —— 它有自己的 padding 收口,
// 不经过 stackContentSize。
func TestPaddingGridContentSize(t *testing.T) {
	g := mkNode("grid", map[string]float64{"paddingTop": 7, "paddingBottom": 11})
	g.Children = []*GuiNode{mkNode("rect", map[string]float64{"width": 40, "height": 30})}
	w, h := g.intrinsicSize()
	if h != 48 {
		t.Fatalf("grid 固有高应 = 30+7+11 = 48, 实际 %d", h)
	}
	if w != 40 {
		t.Fatalf("grid 左右没给 padding, 宽应 = 40, 实际 %d", w)
	}
}
