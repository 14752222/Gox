package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 布局弹性词汇 (2026-09-19): 百分比 / min-max / flexShrink =====
//
// 词汇语义 (gui-responsive-screen-options.md §3.4 → §四 布局缺口):
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
