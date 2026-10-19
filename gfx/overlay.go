package gfx

import (
	"image"
	"image/color"
)

// 弹层组件 (P2-4): <dialog> 模态遮罩 + <toast> 非模态提示。
//
// 两者都靠 layer.go 的层叠模型工作: overlay 标签自动 escapeClipping (被提升
// 到根层级最后绘制, 不受任何祖先盒子裁剪) 并拿到 overlayZBase 的层级抬升
// (永远盖住普通内容, 脚本不必给每个 dialog 手写大 zIndex)。
//
// 关键设计: dialog 的遮罩就是 dialog 自己的盒子 (铺满窗口), 不额外生成
// 遮罩节点。这样"点遮罩关闭"只需要在事件层判断"点在 dialog 内但不在内容
// 卡片上", 不必往树里塞一个 Go 造的子节点 —— 树结构保持与 JS 写的一致,
// 调试时看到的就是脚本描述的那棵树。

// toastMargin 是 toast 距窗口右上角的边距 (**设计像素**, 用点见 Space())。
const toastMargin = 16

// toastAccentW 是 toast 左侧 level 色条宽度 (**设计像素**, 用点见 Space())。
//
// Prism S8: 它参与"文本左边到多少"的计算, 必须与 device-independent 的文字
// 同一套口径 —— 否则 2x 屏上叠加后的左上角会有一个只放大了一半的色条。
const toastAccentW = 4

// ===== 布局 =====

// windowBox 返回节点所在树的根盒子。弹层以"窗口"为参考系 (遮罩铺满窗口、
// toast 贴右上角), 而它的父节点可能只是某个 28px 高的小盒子。
func (n *GuiNode) windowBox() Rect {
	r := n
	for r.Parent != nil {
		r = r.Parent
	}
	return r.Box
}

// layoutDialog 布局模态对话框: 自身铺满窗口 (作为遮罩), 流内子节点
// (内容卡片) 居中摆放。
//
// 不显示时把盒子清空: 空盒子的子树不会被绘制 (见 drawNode 的可见性判断),
// 也命不中 (hitEscapesLayer 用 Box.Contains 过滤), 于是"关掉的 dialog"
// 在没有从树上摘除的前提下也不会有一点副作用。
func layoutDialog(n *GuiNode) {
	if !n.overlayVisible() {
		n.Box = Rect{}
		return
	}
	n.Box = n.windowBox()
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{
			X: area.X + (area.W-cw)/2,
			Y: area.Y + (area.H-ch)/2,
			W: cw, H: ch,
		}
		layoutNode(c)
	}
	placeAbsoluteIn(n, area)
}

// layoutToast 布局非模态提示: 贴窗口右上角, 尺寸按 contentSize/message 固有。
func layoutToast(n *GuiNode) {
	if !n.overlayVisible() {
		n.Box = Rect{}
		return
	}
	win := n.windowBox()
	tw, th := n.intrinsicSize()
	n.Box = Rect{
		X: win.X + win.W - tw - Space(toastMargin),
		Y: win.Y + Space(toastMargin),
		W: tw, H: th,
	}
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X, Y: area.Y + (area.H-ch)/2, W: cw, H: ch}
		layoutNode(c)
	}
	placeAbsoluteIn(n, area)
}

// ===== 绘制 =====

// paintDialog 画遮罩 + 内容卡片缺省外观 (Prism S6)。
//
// 遮罩走 Scrim token (prism 预设 55% 黑, v1 与 Mask 同值 40%): FillRect 的
// 预乘 src-over 真混合透出下方内容。Scrim 与 Mask 拆成两个 token 的意义在于
// 预设可以分别标定 —— 组件只认 scrim, mask 留给脚本侧兼容。
//
// 卡片缺省: r16 圆角 + surfaceRaised 底 + 1px popupEdge 描边 + 投影
// (paintShadow: 偏移轮廓 + 两轮盒式模糊, 软光栅对高斯的诚实近似)。卡片自己
// 给了 background/border 时不插手。
func paintDialog(img *image.RGBA, n *GuiNode) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	FillRect(img, n.Box, colorScrim)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		_, hasBg := c.PropStr("background")
		_, hasBd := c.PropStr("border")
		if hasBg || hasBd {
			continue
		}
		if c.Box.W <= 0 || c.Box.H <= 0 {
			continue
		}
		rad := Radius(RadiusXL)
		paintShadow(img, c.Box, rad, shadowSpec{x: 0, y: 6, blur: 16, c: color.RGBA{A: 72}})
		fillRoundRect(img, c.Box, rad, colorSurfaceRaised)
		strokeRoundRectW(img, c.Box, rad, 1, colorPopupEdge)
	}
}

// paintToast 画提示卡片: 白底 + 1px 边框 + 左侧 level 色条 + message 文本。
func paintToast(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(colorPopupFace, disabled))
	StrokeRect(img, b, tint(colorPopupEdge, disabled))
	// Prism S8: 色条宽、文本起始 x 与右侧留白必须走同一套换算, 否则高密屏上
	// 文字会压在色条上 (色条没放大, 文字放大了)。
	accentW := Space(toastAccentW)
	FillRect(img, Rect{X: b.X, Y: b.Y, W: accentW, H: b.H},
		tint(toastLevelColor(n), disabled))

	msg := n.toastMessage()
	if msg == "" {
		return
	}
	size := n.FontSize()
	_, th := MeasureText(msg, size)
	x := b.X + accentW + Space(fieldPadX)
	maxW := b.X + b.W - Space(fieldPadX) - x
	if maxW < 0 {
		maxW = 0
	}
	DrawText(img, img.Bounds(), msg, x, b.Y+(b.H-th)/2, size,
		tint(n.textColor(), disabled), maxW)
}

// toastMessage 读取提示文本 (message prop)。
func (n *GuiNode) toastMessage() string {
	s, _ := n.PropStr("message")
	return s
}

// toastLevelColor 把 level prop 映射成左侧色条颜色。
// 缺省 (含未识别的值) 用信息蓝, 与"成功/警告/错误"区分开。
func toastLevelColor(n *GuiNode) color.RGBA {
	switch lv, _ := n.PropStr("level"); lv {
	case "success":
		return colorAccent
	case "warn", "warning":
		return colorWarn
	case "error":
		return colorDanger
	}
	return colorInfo
}

// ===== 模态语义 =====

// modalAt 返回 (x, y) 处最上层的可见 dialog, 无则 nil。
// 从绘制序的末尾 (最上层) 往前找, 与弹层之间的压盖关系一致。
func modalAt(root *GuiNode, x, y int) *GuiNode {
	if root == nil {
		return nil
	}
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		d := esc[i]
		if d.Tag != "dialog" || !d.overlayVisible() {
			continue
		}
		if d.Box.Contains(x, y) {
			return d
		}
	}
	return nil
}

// dialogMaskHit 报告 (x, y) 是否落在"遮罩区域": 在 dialog 盒子内, 但不在
// 任何内容卡片上。
//
// 这个判断是"点遮罩关闭、点卡片不关闭"的全部依据。之所以不用"给卡片挂一个
// 空 onClick 挡住冒泡"的常见做法: 那会往树里塞非脚本产生的处理器, 让
// HitTest 的语义 ("谁会被点到") 变得依赖实现细节。
func dialogMaskHit(d *GuiNode, x, y int) bool {
	if !d.Box.Contains(x, y) {
		return false
	}
	for _, c := range d.Children {
		if c.isFlowChild() && c.Box.Contains(x, y) {
			return false
		}
	}
	return true
}

// closeTopDialog 关掉最上层的可见 dialog (Esc 兜底路径), 返回是否关掉了一个
// —— 没关到 (没有可见 dialog) 时调用方继续走后续兜底 (tooltip) 或把按键
// 交给脚本。
//
// 与 modalAt 的区别: modalAt 用坐标筛选 (点击路径), Esc 没有坐标, 这里
// 只需按绘制序倒着取最上面那个 dialog。
func (a *app) closeTopDialog() bool {
	root := a.rootNode()
	if root == nil {
		return false
	}
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		if esc[i].Tag == "dialog" && esc[i].overlayVisible() {
			a.callHandler(esc[i], "onClose", nil)
			return true
		}
	}
	return false
}

// closeAnyExpandedSelect 收起任意一个展开中的字段弹层 (下拉 / 日历 / 色板),
// 返回是否关掉了。Esc 的第一优先级: 先收摊开的弹层, 再考虑关对话。
//
// 实现搬到 popupfield.go 了 (三家共用同一个状态机) —— 这里保留函数名,
// 因为"Esc 先收下拉"是这个顺序在 overlay.go 里的一环, 名字换掉会让
// 前后两段的优先级注释对不上。
func (a *app) closeAnyExpandedSelect(root *GuiNode) bool {
	return a.closeTopPopupField()
}
