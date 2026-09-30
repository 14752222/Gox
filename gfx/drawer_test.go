package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4/T09 drawer 抽屉弹层 =====
//
// 复用 dialog 的弹层机制 (overlay.go), 所以验收重点分两类:
//  1. **弹层共性** (与 dialog 同款, 必须一致): open=false 整支不绘制不拦截;
//     遮罩吃掉其下点击; 点遮罩派发 onClose; 点内容卡片不关; Esc 关最上层。
//  2. **抽屉特有**: 内容卡片贴 side 边 (left/right); 宽按 width prop (缺省 280);
//     滑入进度 0→1, 关闭回落到 0。

// mkDrawer 造一个 drawer (open 由参数定), 内容卡片是带 padding 的 column。
func mkDrawer(open bool, side string, width float64) (*GuiNode, *GuiNode) {
	d := &GuiNode{Tag: "drawer", Props: map[string]object.Value{}}
	withBoolProp(d, "open", open)
	if side != "" {
		withStrProp(d, "side", side)
	}
	if width > 0 {
		withNum(d, "width", width)
	}
	d.drawerAnim = 1 // 静止到位 (测布局/命中不掺动画)
	panel := mkNode("column", map[string]float64{"padding": 10})
	panelIn := mkNode("rect", map[string]float64{"width": 60, "height": 30})
	mountChildren(panel, panelIn)
	mountChildren(d, panel)
	return d, panel
}

// TestDrawerPanelRestsAtRightEdge: 缺省贴右边, 宽 280。
func TestDrawerPanelRestsAtRightEdge(t *testing.T) {
	root := mkNode("column", nil)
	d, panel := mkDrawer(true, "", 0)
	mountChildren(root, d)

	renderTree(root, 400, 200)

	// 缺省宽度 drawerDefaultW=280, 贴右: x = 400-280 = 120
	want := Rect{X: 400 - drawerDefaultW, Y: 0, W: drawerDefaultW, H: 200}
	if panel.Box != want {
		t.Fatalf("右侧抽屉面板 = %v, want %v", panel.Box, want)
	}
}

// TestDrawerSideLeft: side="left" 时贴左边缘。
func TestDrawerSideLeft(t *testing.T) {
	root := mkNode("column", nil)
	d, panel := mkDrawer(true, "left", 200)
	mountChildren(root, d)

	renderTree(root, 400, 200)

	want := Rect{X: 0, Y: 0, W: 200, H: 200}
	if panel.Box != want {
		t.Fatalf("左侧抽屉面板 = %v, want %v", panel.Box, want)
	}
}

// TestDrawerWidthOverridesDefault: width prop 覆盖缺省宽度。
func TestDrawerWidthOverridesDefault(t *testing.T) {
	root := mkNode("column", nil)
	d, panel := mkDrawer(true, "right", 120)
	mountChildren(root, d)

	renderTree(root, 400, 200)

	if panel.Box.W != 120 || panel.Box.X != 280 {
		t.Fatalf("width=120 的右侧抽屉面板 = %v, want X=280 W=120", panel.Box)
	}
	// width 超过窗口时钳到窗口宽 (不能画出窗口外)
	d2, panel2 := mkDrawer(true, "left", 9999)
	root2 := mkNode("column", nil)
	mountChildren(root2, d2)
	renderTree(root2, 300, 150)
	if panel2.Box.W != 300 {
		t.Fatalf("超宽抽屉应钳到窗口宽 300, got %d", panel2.Box.W)
	}
}

// TestDrawerClosedIsInert: open=false 整支不绘制、不拦截。
func TestDrawerClosedIsInert(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	d, _ := mkDrawer(false, "right", 200)
	mountChildren(root, btn, d)

	img := renderTree(root, 400, 200)

	if d.Box != (Rect{}) {
		t.Fatalf("关闭的 drawer 盒子应为空: %v", d.Box)
	}
	assertPx(t, img, btn.Box.X+2, btn.Box.Y+2, pxBtnFace, "关闭的 drawer 不该画遮罩")
	if hit := HitTest(root, btn.Box.X+2, btn.Box.Y+2); hit != btn {
		t.Fatalf("关闭的 drawer 不应拦截点击, got %v", hit)
	}
}

// TestDrawerMaskSwallowsClicksBelow: 遮罩吃掉其下点击 (模态)。
func TestDrawerMaskSwallowsClicksBelow(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	d, panel := mkDrawer(true, "right", 200)
	panelBtn := withClick(mkNode("button", map[string]float64{"width": 40, "height": 20}))
	mountChildren(panel, panelBtn)
	mountChildren(root, btn, d)

	renderTree(root, 400, 200)

	// 遮罩区 (左侧, 不在面板上): HitTest 必须返回 nil
	mx, my := btn.Box.X+2, btn.Box.Y+2
	if hit := HitTest(root, mx, my); hit != nil {
		t.Fatalf("遮罩应吃掉其下的点击, got %v", hit)
	}
	// 事件层据此判定"点的是遮罩" → drawerPanelHit 为假
	if drawerAt(root, mx, my) != d {
		t.Fatalf("drawerAt 未找到遮罩上的 drawer")
	}
	if drawerPanelHit(d, mx, my) {
		t.Fatalf("遮罩区不该判定为面板命中")
	}
	// 面板上的点判定为面板命中
	if !drawerPanelHit(d, panel.Box.X+2, panel.Box.Y+2) {
		t.Fatalf("面板上的点应判定为面板命中")
	}
	// 面板内部的按钮照常可命中 (模态不吞自己的内容)
	if hit := HitTest(root, panelBtn.Box.X+2, panelBtn.Box.Y+2); hit != panelBtn {
		t.Fatalf("面板内的按钮应可命中, got %v", hit)
	}
}

// TestDrawerMaskClickDispatchesOnClose: 点遮罩派发 onClose, 点面板不派发。
func TestDrawerMaskClickDispatchesOnClose(t *testing.T) {
	root := mkNode("column", nil)
	d, panel := mkDrawer(true, "right", 200)
	closed := 0
	d.Props["onClose"] = object.NewBuiltin("close", func(args ...object.Value) object.Value {
		closed++
		return object.UndefinedSingleton
	})
	mountChildren(root, d)

	fake, a := mountTestApp(t, root, 400, 200)

	// 点面板: 不关
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: panel.Box.X + 5, Y: panel.Box.Y + 5})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: panel.Box.X + 5, Y: panel.Box.Y + 5})
	if closed != 0 {
		t.Fatalf("点面板不该关抽屉, onClose 被调 %d 次", closed)
	}
	// 点遮罩 (面板左侧): 关
	mx := panel.Box.X - 20
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: mx, Y: panel.Box.Y + 5})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: mx, Y: panel.Box.Y + 5})
	if closed != 1 {
		t.Fatalf("点遮罩应派发一次 onClose, got %d", closed)
	}
}

// TestDrawerEscapeClosesTop: Esc 关掉最上层 drawer。
func TestDrawerEscapeClosesTop(t *testing.T) {
	root := mkNode("column", nil)
	d, _ := mkDrawer(true, "right", 200)
	closed := 0
	d.Props["onClose"] = object.NewBuiltin("close", func(args ...object.Value) object.Value {
		closed++
		return object.UndefinedSingleton
	})
	mountChildren(root, d)

	fake, a := mountTestApp(t, root, 400, 200)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})

	if closed != 1 {
		t.Fatalf("Esc 应关掉 drawer (onClose 一次), got %d", closed)
	}
	// Esc 只派发通知, 不替脚本改 open —— 受控语义下是否真的关由脚本回写决定。
	// 脚本回写 open=false 后, 再次 Esc 才应"无抽屉可关"。
	withBoolProp(d, "open", false)
	if a.closeTopDrawer() {
		t.Fatalf("已关的抽屉不该再被关")
	}
}

// TestDrawerSlideProgress: 进度按帧逼近目标; open=false 时回落到 0。
func TestDrawerSlideProgress(t *testing.T) {
	d, _ := mkDrawer(true, "right", 200)
	d.drawerAnim = 0

	// 从 0 起步: 每帧 +1/drawerSlideFrames, 到 1 停
	for i := 0; i < drawerSlideFrames-1; i++ {
		if !d.advanceDrawer() {
			t.Fatalf("第 %d 帧不该已到位", i)
		}
	}
	if d.advanceDrawer() {
		t.Fatalf("第 %d 帧应到位 (1.0)", drawerSlideFrames)
	}
	if d.drawerProgress() != 1 {
		t.Fatalf("到位后进度 = %v, want 1", d.drawerProgress())
	}

	// 关闭: 逐帧回落到 0
	withBoolProp(d, "open", false)
	for d.advanceDrawer() {
	}
	if d.drawerProgress() != 0 {
		t.Fatalf("关闭后进度 = %v, want 0", d.drawerProgress())
	}
}

// TestDrawerClosedProducesNoInk: progress=0 时不画任何东西 (面板在窗口外)。
func TestDrawerClosedProducesNoInk(t *testing.T) {
	root := mkNode("column", nil)
	d, _ := mkDrawer(true, "right", 200)
	d.drawerAnim = 0 // 未开始滑入
	// open=true 但进度为 0: 遮罩淡入系数 0, 整帧无墨
	mountChildren(root, d)
	img := renderTree(root, 400, 200)

	if hasInkIn(img, Rect{0, 0, 400, 200}) {
		t.Fatalf("progress=0 的抽屉不该产生任何墨迹")
	}
}

// TestDrawerPanelHitUnaffectedByProgress: 命中判定用静止框, 不受动画进度影响。
func TestDrawerPanelHitUnaffectedByProgress(t *testing.T) {
	d, panel := mkDrawer(true, "right", 200)
	root := mkNode("column", nil)
	mountChildren(root, d)
	renderTree(root, 400, 200)

	// 面板静止框命中点
	px, py := panel.Box.X+5, panel.Box.Y+5
	// 把进度改成滑入途中, 命中判定应不变
	d.drawerAnim = 0.3
	if !drawerPanelHit(d, px, py) {
		t.Fatalf("命中判定不该受动画进度影响")
	}
	// 遮罩区始终不命中
	if drawerPanelHit(d, panel.Box.X-10, py) {
		t.Fatalf("遮罩区不该命中面板")
	}
}
