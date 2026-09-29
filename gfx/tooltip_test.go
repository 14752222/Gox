package gfx

import (
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4 tooltip =====
//
// 用例覆盖四条不变量:
//   1. tooltip 对布局透明 (触发元素就是它的盒子), 没悬停时树上没有弹层;
//   2. 计时到点才显示, 移出/按下/Esc 都收;
//   3. 弹层贴触发盒按 placement 定位, 越界翻转, 最终 clamp 进窗口;
//   4. 弹层没有事件处理器, 命中测试穿过它 —— 提示不挡交互。
// (mkNode / mountChildren / renderTree / pushAndPump 等 helper 见 helpers_test.go。)

// mkTooltip 造一个带 text prop 的 tooltip 包着 trigger (无 text 时传 "")。
func mkTooltip(text string, trigger *GuiNode) *GuiNode {
	tip := &GuiNode{Tag: "tooltip", Props: map[string]object.Value{}}
	if text != "" {
		withStrProp(tip, "text", text)
	}
	if trigger != nil {
		mountChildren(tip, trigger)
	}
	return tip
}

// hoverTooltip 悬停到触发元素上并泵一轮 (delay=0 时同一轮就会显示弹层)。
func hoverTooltip(t *testing.T, fake *fakeSurface, a *app, btn *GuiNode) {
	t.Helper()
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
}

// tooltipShown 断言 host 上已有弹层并返回它。
func tooltipShown(t *testing.T, host *GuiNode) *GuiNode {
	t.Helper()
	if host.tipPopup == nil {
		t.Fatalf("tooltip 未显示 (树上没有弹层)")
	}
	return host.tipPopup
}

func TestTooltipWrapsTriggerTransparently(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := mkNode("button", map[string]float64{"width": 90, "height": 30})
	tip := mkTooltip("Save changes", btn)
	mountChildren(root, tip)

	img := renderTree(root, 300, 160)

	// 布局透明: tooltip 的盒子就是触发按钮的盒子
	if tip.Box != btn.Box {
		t.Fatalf("tooltip 未跟随触发元素: tip=%v btn=%v", tip.Box, btn.Box)
	}
	// 没悬停: 树上不该有弹层, 按钮照常绘制
	if n := countTag(root, "tooltip-popup"); n != 0 {
		t.Fatalf("未悬停时弹层数量 = %d, want 0", n)
	}
	assertPx(t, img, btn.Box.X+4, btn.Box.Y+4, pxBtnFace, "触发按钮缺省底色")
}

func TestTooltipShowsAfterDeadlineAndPaints(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	// 按钮要足够宽: 弹层比"按钮中心到窗口左缘"还宽就会被 clamp (那是
	// TestTooltipClampedInsideWindow 的职责), 这里只验居中与贴下方。
	btn := withClick(mkNode("button", map[string]float64{"width": 130, "height": 30}))
	tip := mkTooltip("Save changes", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)

	fake, a := mountTestApp(t, root, 400, 200)
	hoverTooltip(t, fake, a, btn)

	popup := tooltipShown(t, tip)
	// 默认 placement=bottom: 贴触发盒正下方 (间距 6), 水平居中
	if popup.Box.Y != btn.Box.Y+btn.Box.H+tipGap {
		t.Fatalf("弹层未贴触发盒下方: popup.Y=%d want %d", popup.Box.Y, btn.Box.Y+btn.Box.H+tipGap)
	}
	if cxWant := btn.Box.X + btn.Box.W/2 - popup.Box.W/2; popup.Box.X != cxWant {
		t.Fatalf("弹层未水平居中: popup.X=%d want %d", popup.Box.X, cxWant)
	}
	if popup.Box.W <= 2*tipPadX || popup.Box.H <= 2*tipPadY {
		t.Fatalf("弹层尺寸没把文本算进去: %v", popup.Box)
	}

	// 重绘后弹层有墨迹: 深色底 + 中间文字行有非底色像素
	img := renderTree(root, 400, 200)
	// 半透明深灰叠在白底上的 src-over 混合结果 (与 dialog 遮罩用例同一算法):
	// 38*242/255 + 255*13/255 = 49
	assertPx(t, img, popup.Box.X+2, popup.Box.Y+2, color.RGBA{49, 49, 49, 255}, "弹层深色底")
	if !hasInkIn(img, Rect{popup.Box.X + tipPadX, popup.Box.Y, popup.Box.W - 2*tipPadX, popup.Box.H}) {
		t.Fatalf("弹层未绘制提示文本")
	}
}

func TestTooltipHidesWhenMouseLeaves(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("Save changes", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)

	fake, a := mountTestApp(t, root, 300, 160)
	hoverTooltip(t, fake, a, btn)
	tooltipShown(t, tip)

	// 移到触发区之外: 弹层摘除
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 260, Y: 150})
	if tip.tipPopup != nil {
		t.Fatalf("移出触发区后弹层仍在树上")
	}
	if n := countTag(root, "tooltip-popup"); n != 0 {
		t.Fatalf("移出后弹层数量 = %d, want 0", n)
	}

	// 再次悬停显示, 光标离开窗口同理收起
	hoverTooltip(t, fake, a, btn)
	tooltipShown(t, tip)
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if tip.tipPopup != nil {
		t.Fatalf("MouseLeave 后弹层仍在树上")
	}
}

func TestTooltipPlacementTop(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	// 上面垫一块 100 高的占位: 让触发盒离窗口顶足够远, top 弹层放得下
	// (贴顶时 top 会翻回 bottom, 那是翻转用例的职责)。
	filler := mkNode("rect", map[string]float64{"width": 90, "height": 100})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("Above", btn)
	withNumProp(tip, "delay", 0)
	withStrProp(tip, "placement", "top")
	mountChildren(root, filler, tip)

	fake, a := mountTestApp(t, root, 300, 200)
	hoverTooltip(t, fake, a, btn)

	popup := tooltipShown(t, tip)
	if want := btn.Box.Y - tipGap - popup.Box.H; popup.Box.Y != want {
		t.Fatalf("top 弹层未贴触发盒上方: popup.Y=%d want %d", popup.Box.Y, want)
	}
}

// 触发盒贴近窗口底边时, bottom 放不下应翻到上方 (而不是被 clamp 盖住触发元素)。
func TestTooltipFlipsUpWhenBottomOverflows(t *testing.T) {
	// 窗口 200 高: 上面垫一块 160 高的占位, 触发按钮贴在距底 10px 处
	root := mkNode("column", nil)
	filler := mkNode("rect", map[string]float64{"width": 90, "height": 160})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("Save changes", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, filler, tip)

	fake, a := mountTestApp(t, root, 300, 200)
	hoverTooltip(t, fake, a, btn)

	popup := tooltipShown(t, tip)
	if popup.Box.Y+popup.Box.H > 200 {
		t.Fatalf("弹层超出窗口底边: %v", popup.Box)
	}
	if popup.Box.Y+popup.Box.H > btn.Box.Y {
		t.Fatalf("bottom 放不下时应翻到触发盒上方: popup=%v btn=%v", popup.Box, btn.Box)
	}
}

// 弹层没有事件处理器: 显示中, 命中测试穿过它直达下层控件 (提示不挡交互)。
func TestTooltipDoesNotBlockHits(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("Save changes", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)
	// 第二个按钮紧跟其下 —— bottom 弹层正好盖在它的上部
	btn2 := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	mountChildren(root, btn2)

	fake, a := mountTestApp(t, root, 300, 200)
	hoverTooltip(t, fake, a, btn)
	popup := tooltipShown(t, tip)

	// 用例前提: 弹层确实盖到了 btn2
	if popup.Box.Y+popup.Box.H <= btn2.Box.Y {
		t.Fatalf("用例前提不成立: 弹层没盖到第二个按钮 popup=%v btn2=%v", popup.Box, btn2.Box)
	}
	// 点在弹层盖住 btn2 的区域: 命中的仍是 btn2
	px := popup.Box.X + popup.Box.W/2
	py := popup.Box.Y + popup.Box.H/2
	if hit := HitTest(root, px, py); hit != btn2 {
		t.Fatalf("显示中的 tooltip 不该挡住下层点击, got %v", hit)
	}
}

// 按下与 Esc 都收提示: 点击 = 开始交互, Esc 是弹层兜底链的最后一环。
func TestTooltipHidesOnMouseDownAndEsc(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("Save changes", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)

	fake, a := mountTestApp(t, root, 300, 160)
	hoverTooltip(t, fake, a, btn)
	tooltipShown(t, tip)

	// Esc 收起
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})
	if tip.tipPopup != nil {
		t.Fatalf("Esc 后弹层仍在树上")
	}

	// 再悬停显示, 按下让路
	hoverTooltip(t, fake, a, btn)
	tooltipShown(t, tip)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if tip.tipPopup != nil {
		t.Fatalf("按下后弹层仍在树上")
	}
}

func TestTooltipEmptyTextNeverShows(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	tip := mkTooltip("", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)

	fake, a := mountTestApp(t, root, 300, 160)
	hoverTooltip(t, fake, a, btn)
	if tip.tipPopup != nil {
		t.Fatalf("text 为空不该弹层")
	}
}

// 窄窗口 + 长文本: 弹层比窗口还宽时 clamp 退化成贴边缘, 至少保证可见。
func TestTooltipClampedInsideWindow(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 120, "height": 30}))
	tip := mkTooltip("A fairly long hint that is much wider than the trigger", btn)
	withNumProp(tip, "delay", 0)
	mountChildren(root, tip)

	fake, a := mountTestApp(t, root, 160, 200)
	hoverTooltip(t, fake, a, btn)
	popup := tooltipShown(t, tip)

	win := root.Box
	if popup.Box.X < win.X+tipWinMargin {
		t.Fatalf("弹层越出窗口左缘: popup=%v win=%v", popup.Box, win)
	}
	if popup.Box.Y < win.Y+tipWinMargin || popup.Box.Y+popup.Box.H > win.Y+win.H {
		t.Fatalf("弹层越出窗口上下缘: popup=%v win=%v", popup.Box, win)
	}
}
