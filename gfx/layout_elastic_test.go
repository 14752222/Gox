package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 布局弹性词汇 (2026-09-19): 百分比 / min-max / flexShrink =====
//
// 词汇语义 (agent_doc/gui-responsive-screen-options.md §3.4 → §四 布局缺口):
//   - width="50%" 按父容器**内容区**解析; 百分比子节点不撑大父容器
//     (auto 尺寸下贡献 0, 与 CSS 一致)。
//   - minWidth/maxWidth/minHeight/maxHeight 在 stretch/grow/shrink/百分比
//     全部落定后终钳位; v1 不回收钳位差。
//   - flexShrink 按 系数×基础尺寸 加权分摊溢出 (CSS 同款权重)。

func TestLayoutPercentMainAxis(t *testing.T) {
	// row 400: a 宽 "50%" → 200; b 定宽 100 从 200 起
	root := mkNode("row", nil)
	a := withStr(mkNode("rect", map[string]float64{"height": 10}), "width", "50%")
	b := mkNode("rect", map[string]float64{"width": 100, "height": 10})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)
	if a.Box.W != 200 {
		t.Fatalf("百分比主轴宽 = %d, want 200", a.Box.W)
	}
	if b.Box.X != 200 || b.Box.W != 100 {
		t.Fatalf("b = %v, want x=200 w=100", b.Box)
	}

	// column 300 高: 高度 "50%" → 150 (主轴方向的百分比同理)
	root2 := mkNode("column", nil)
	c := withStr(mkNode("rect", map[string]float64{"width": 20}), "height", "50%")
	root2.Children = []*GuiNode{c}
	Layout(root2, 200, 300)
	if c.Box.H != 150 {
		t.Fatalf("百分比主轴高 = %d, want 150", c.Box.H)
	}
}

func TestLayoutPercentCrossAxis(t *testing.T) {
	// column 默认 alignItems=stretch: 宽 "50%" 算显式 → 不吃 stretch, 解析为父宽一半
	root := mkNode("column", nil)
	a := withStr(mkNode("rect", map[string]float64{"height": 10}), "width", "50%")
	root.Children = []*GuiNode{a}
	Layout(root, 300, 200)
	if a.Box.W != 150 {
		t.Fatalf("百分比交叉轴宽 = %d, want 150 (而不是 stretch 的 300)", a.Box.W)
	}
}

func TestLayoutPercentDoesNotSizeParent(t *testing.T) {
	// 百分比子节点不撑大父容器: 容器 auto 尺寸只由定宽兄弟决定
	outer := mkNode("row", nil)
	inner := mkNode("row", nil)
	pct := withStr(mkNode("rect", map[string]float64{"height": 10}), "width", "50%")
	fixed := mkNode("rect", map[string]float64{"width": 80, "height": 10})
	inner.Children = []*GuiNode{pct, fixed}
	outer.Children = []*GuiNode{inner}
	Layout(outer, 1000, 100) // 外层足够大, inner 拿到的宽 = 自身内容尺寸
	if inner.Box.W != 80 {
		t.Fatalf("父容器宽 = %d, want 80 (百分比子节点贡献 0)", inner.Box.W)
	}
	if pct.Box.W != 40 {
		t.Fatalf("解析后的百分比子节点宽 = %d, want 80×50%%=40", pct.Box.W)
	}
}

func TestLayoutMinMaxClampsStretch(t *testing.T) {
	// "拉伸但有上限": column stretch 下子节点被拉满, maxWidth 钳住
	root := mkNode("column", nil)
	a := mkNode("rect", map[string]float64{"height": 10, "maxWidth": 100})
	root.Children = []*GuiNode{a}
	Layout(root, 300, 200)
	if a.Box.W != 100 {
		t.Fatalf("maxWidth 钳位后宽 = %d, want 100 (stretch 300 被截)", a.Box.W)
	}

	// "拉伸但有底线": 固有 10 的节点 minWidth 抬到 50 (stretch 本来就给 300,
	// 用 alignItems=start 场景验证 min 对固有尺寸的抬升)
	root2 := mkNode("column", map[string]float64{"alignItems": 0}) // alignItems 只认字符串
	root2.Props["alignItems"] = object.NewString("start")
	b := mkNode("rect", map[string]float64{"width": 10, "height": 10, "minWidth": 50})
	root2.Children = []*GuiNode{b}
	Layout(root2, 300, 200)
	if b.Box.W != 50 {
		t.Fatalf("minWidth 抬升后宽 = %d, want 50", b.Box.W)
	}
}

func TestLayoutMinMaxClampsGrow(t *testing.T) {
	// grow 分配超过 maxWidth 的部分被钳掉 (v1 不回收: 富余不再分给别人)
	root := mkNode("row", nil)
	a := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10, "flexGrow": 1, "maxWidth": 100})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 100)
	if b.Box.W != 100 {
		t.Fatalf("grow+maxWidth 后宽 = %d, want 100 (grow 到 350 被钳)", b.Box.W)
	}
	if a.Box.W != 50 || a.Box.X != 0 {
		t.Fatalf("a = %v, 不受影响", a.Box)
	}
}

func TestLayoutFlexShrink(t *testing.T) {
	// 溢出收缩: row 300, a=200 固定, b=200 shrink=1 → deficit 100 全由 b 吸收
	root := mkNode("row", nil)
	a := mkNode("rect", map[string]float64{"width": 200, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 200, "height": 10, "flexShrink": 1})
	root.Children = []*GuiNode{a, b}
	Layout(root, 300, 100)
	if a.Box.W != 200 || a.Box.X != 0 {
		t.Fatalf("a = %v, want w=200 不收缩", a.Box)
	}
	if b.Box.W != 100 || b.Box.X != 200 {
		t.Fatalf("b = %v, want x=200 w=100", b.Box)
	}

	// 加权: 两个 shrink=1 (尺寸 200/100) 分摊 deficit 150 → 按 基础尺寸 加权
	// 200 的收 100、100 的收 50
	root2 := mkNode("row", nil)
	x := mkNode("rect", map[string]float64{"width": 200, "height": 10, "flexShrink": 1})
	y := mkNode("rect", map[string]float64{"width": 100, "height": 10, "flexShrink": 1})
	root2.Children = []*GuiNode{x, y}
	Layout(root2, 150, 100)
	if x.Box.W != 100 {
		t.Fatalf("x 宽 = %d, want 200-100=100 (加权收缩)", x.Box.W)
	}
	if y.Box.W != 50 {
		t.Fatalf("y 宽 = %d, want 100-50=50", y.Box.W)
	}

	// 无 shrink 时保持溢出 (既有行为不变)
	root3 := mkNode("row", nil)
	p := mkNode("rect", map[string]float64{"width": 200, "height": 10})
	q := mkNode("rect", map[string]float64{"width": 200, "height": 10})
	root3.Children = []*GuiNode{p, q}
	Layout(root3, 300, 100)
	if q.Box.X != 200 || q.Box.W != 200 {
		t.Fatalf("无 shrink 应保持溢出: q = %v", q.Box)
	}
}

func TestLayoutShrinkRespectsMinWidth(t *testing.T) {
	// 收缩下限: minWidth 挡住 shrink, 剩余溢出由别的 shrink 项吸收
	root := mkNode("row", nil)
	a := mkNode("rect", map[string]float64{"width": 200, "height": 10, "flexShrink": 1, "minWidth": 150})
	b := mkNode("rect", map[string]float64{"width": 200, "height": 10, "flexShrink": 1})
	root.Children = []*GuiNode{a, b}
	Layout(root, 300, 100) // deficit 100: a 按权重应收 50, 但 min 150 允许收 50 ✓
	if a.Box.W != 150 {
		t.Fatalf("a 宽 = %d, want 150 (minWidth 下限)", a.Box.W)
	}
	if b.Box.W != 150 {
		t.Fatalf("b 宽 = %d, want 150", b.Box.W)
	}
}

func TestLayoutPercentBadFormatIgnored(t *testing.T) {
	// 坏格式静默忽略: "50x" 不是百分比 → 按固有尺寸 (0 → stretch)
	root := mkNode("column", nil)
	a := withStr(mkNode("rect", map[string]float64{"height": 10}), "width", "50x")
	root.Children = []*GuiNode{a}
	Layout(root, 300, 200)
	if a.Box.W != 300 {
		t.Fatalf("坏格式应回退 stretch: 宽 = %d, want 300", a.Box.W)
	}
}

// ===== 容器级 wrap (§四 布局缺口第二批) =====

func TestLayoutWrapBasic(t *testing.T) {
	// 宽 300: 三个 100 恰好一行, 第四个折行
	root := withBool(mkNode("row", nil), "wrap", true)
	var kids []*GuiNode
	for i := 0; i < 4; i++ {
		kids = append(kids, mkNode("rect", map[string]float64{"width": 100, "height": 20}))
	}
	root.Children = kids
	Layout(root, 300, 200)
	if kids[2].Box.X != 200 || kids[2].Box.Y != 0 {
		t.Fatalf("第三个应在第一行末: %v", kids[2].Box)
	}
	if kids[3].Box.X != 0 || kids[3].Box.Y != 20 {
		t.Fatalf("第四个应折行: %v", kids[3].Box)
	}
}

func TestLayoutWrapWithGap(t *testing.T) {
	// 宽 300 gap 10: 两个 100 (110 间隔) 一行, 第三个 (再 +110 超宽) 折行
	root := mkNode("row", map[string]float64{"gap": 10})
	root.Props["wrap"] = object.NewBoolean(true)
	kids := []*GuiNode{
		mkNode("rect", map[string]float64{"width": 100, "height": 20}),
		mkNode("rect", map[string]float64{"width": 100, "height": 20}),
		mkNode("rect", map[string]float64{"width": 100, "height": 20}),
	}
	root.Children = kids
	Layout(root, 300, 200)
	if kids[1].Box.X != 110 || kids[1].Box.Y != 0 {
		t.Fatalf("行内位置: %v", kids[1].Box)
	}
	// 行2 y = 行1 高 20 + 行间距 gap 10
	if kids[2].Box.X != 0 || kids[2].Box.Y != 30 {
		t.Fatalf("第三个应折到第二行 (y=20+10): %v", kids[2].Box)
	}
}

func TestLayoutWrapAutoHeightBackfill(t *testing.T) {
	// column 里的 wrap row (未给高): 高度由"宽 200 约束下的折行结果"回填
	// 90+10+90=190 ≤ 200 → 每行两个; 三个子节点 → 两行 → 高 = 20+10+20 = 50
	root := mkNode("column", nil)
	flow := mkNode("row", map[string]float64{"gap": 10})
	flow.Props["wrap"] = object.NewBoolean(true)
	var kids []*GuiNode
	for i := 0; i < 3; i++ {
		kids = append(kids, mkNode("rect", map[string]float64{"width": 90, "height": 20}))
	}
	flow.Children = kids
	root.Children = []*GuiNode{flow}
	Layout(root, 200, 300)
	if flow.Box.W != 200 {
		t.Fatalf("wrap row 应被 stretch 到 200: %v", flow.Box)
	}
	if flow.Box.H != 50 {
		t.Fatalf("折行回填高度 = %d, want 50 (两行 20 + gap 10)", flow.Box.H)
	}
	if kids[2].Box.Y != 30 || kids[2].Box.X != 0 {
		t.Fatalf("第三个应在第二行: %v", kids[2].Box)
	}
}

func TestLayoutWrapExplicitHeightWins(t *testing.T) {
	// 显式 height 优先于折行回填 (同 blockHeight 的定宽守卫)
	root := mkNode("column", nil)
	flow := mkNode("row", map[string]float64{"height": 80})
	flow.Props["wrap"] = object.NewBoolean(true)
	flow.Children = []*GuiNode{
		mkNode("rect", map[string]float64{"width": 90, "height": 20}),
		mkNode("rect", map[string]float64{"width": 90, "height": 20}),
		mkNode("rect", map[string]float64{"width": 90, "height": 20}),
	}
	root.Children = []*GuiNode{flow}
	Layout(root, 100, 300)
	if flow.Box.H != 80 {
		t.Fatalf("显式 height 应保留: %d, want 80", flow.Box.H)
	}
}

func TestLayoutWrapAlignCenterInLine(t *testing.T) {
	// 行内垂直居中: 行高取最大者 (40), 矮的居中偏移 10
	root := mkNode("row", nil)
	root.Props["alignItems"] = object.NewString("center")
	root.Props["wrap"] = object.NewBoolean(true)
	tall := mkNode("rect", map[string]float64{"width": 100, "height": 40})
	short := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	root.Children = []*GuiNode{short, tall}
	Layout(root, 300, 200)
	if short.Box.Y != 10 {
		t.Fatalf("矮项应行内居中 y=10: %v", short.Box)
	}
	if tall.Box.Y != 0 {
		t.Fatalf("高项应贴行顶: %v", tall.Box)
	}
}

func TestLayoutWrapStretchToLine(t *testing.T) {
	// 行内 stretch: 无固有高的节点拉到行高 (不是容器总高)
	root := mkNode("row", nil)
	root.Props["wrap"] = object.NewBoolean(true)
	flat := mkNode("rect", map[string]float64{"width": 100}) // height 缺省 0
	tall := mkNode("rect", map[string]float64{"width": 100, "height": 30})
	root.Children = []*GuiNode{flat, tall}
	Layout(root, 300, 200)
	if flat.Box.H != 30 {
		t.Fatalf("stretch 应拉到行高 30: %v", flat.Box)
	}
}

func TestLayoutWrapJustifyBetweenPerLine(t *testing.T) {
	// justifyContent 在行内生效: 一行两个 100, between → 间隙 100
	root := mkNode("row", nil)
	root.Props["wrap"] = object.NewBoolean(true)
	root.Props["justifyContent"] = object.NewString("between")
	a := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	b := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	root.Children = []*GuiNode{a, b}
	Layout(root, 300, 200)
	if a.Box.X != 0 || b.Box.X != 200 {
		t.Fatalf("between: a=%v b=%v, want b.x=200", a.Box, b.Box)
	}
}

func TestLayoutWrapSingleOversizedItem(t *testing.T) {
	// 单个超宽项独占一行 (溢出, 不强行塞进上一行)
	root := mkNode("row", nil)
	root.Props["wrap"] = object.NewBoolean(true)
	a := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	big := mkNode("rect", map[string]float64{"width": 400, "height": 20})
	root.Children = []*GuiNode{a, big}
	Layout(root, 300, 200)
	if big.Box.Y != 20 || big.Box.X != 0 {
		t.Fatalf("超宽项应独占第二行: %v", big.Box)
	}
}
