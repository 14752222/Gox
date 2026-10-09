package gfx

import (
	"image"
	"image/color"
)

// S4/T09 drawer 抽屉弹层。
//
//	<drawer open={x} side="right" width={280} onClose={() => setX(false)}>
//	  <column>...</column>
//	</drawer>
//
// **复用 dialog 的弹层机制** (overlay.go): 靠 layer.go 的层叠模型自动
// escapeClipping (提升到根层级最后绘制, 不受祖先盒裁剪) 并拿到 overlayZBase
// 层级抬升 —— 脚本不必手写大 zIndex。
//
// 与 dialog 的差异只有两处, 其余(遮罩/模态/Esc/点外部关闭)完全同款:
//
//  1. **盒子铺满窗口**, 但内容卡片不居中, 而是贴着 side 指定的边 (left/right),
//     宽度取 width prop (缺省 280);
//  2. 绘制时卡片从侧边**滑入**: 用 progress (0..1) 做水平位移, 未到时卡片
//     在窗口外 (只画遮罩, 或按淡入处理)。
//
// 遮罩沿用 dialog 的做法 —— **就是 drawer 自己的盒子** (铺满窗口), 不额外
// 造子节点。于是"点遮罩关闭"仍只需在事件层判断"点在 drawer 内但不在内容卡片
// 上" (dialogMaskHit), 树结构保持与脚本写的一致。
//
// 动画: 复用 animate.go 的动画心跳 —— 卡片 x 位移按 `open` 的真假驱动一个
// 0..1 的 progress。这与 dialog 的"瞬间出现"不同 (抽屉语义要求滑入), 但实现
// 只多一个 progress 字段, 不需要新增任何定时器。

const (
	// drawerDefaultW 是未指定 width 时的抽屉宽度。
	drawerDefaultW = 280
	// drawerSlideFrames 是滑入/滑出的动画帧数 (按 animFrame=16ms 约 160ms)。
	// 取值与 tooltip/select 弹层的观感统一: 太快像闪, 太慢挡操作。
	drawerSlideFrames = 10
)

// drawerSide 读取 side prop, 只认 "left"/"right", 缺省 "right"。
// 抽屉贴哪边不改变模态语义, 只改定位与滑入方向。
func (n *GuiNode) drawerSide() string {
	s, _ := n.PropStr("side")
	if s == "left" {
		return "left"
	}
	return "right"
}

// drawerWidth 读取 width prop (逻辑单位, 内核换算成设备像素 —— 见 density.go);
// 非法/缺省回落 drawerDefaultW (内置度量, 设备像素)。
func (n *GuiNode) drawerWidth() int {
	if v, ok := n.PropNum("width"); ok && v > 0 {
		return dpToPx(v) // 逻辑值 → 设备像素 (density.go)
	}
	return drawerDefaultW
}

// drawerPanel 返回抽屉内容卡片: 第一个流内子节点。
// 与 dialog 的"内容卡片"是同一个概念 (dialogMaskHit 也按它判定).
func (n *GuiNode) drawerPanel() *GuiNode {
	for _, c := range n.Children {
		if c.isFlowChild() {
			return c
		}
	}
	return nil
}

// drawerProgress 返回滑入进度 (0 = 完全在窗口外, 1 = 完全到位)。
//
// 由 open 的真假驱动: open 为真时递增、为假时递减一个帧步长。关掉 (open=false)
// 时进度归零, 于是"关闭的抽屉"整支不绘制 (见 layoutDrawer 清盒), 与 dialog 一致。
func (n *GuiNode) drawerProgress() float64 {
	return n.drawerAnim
}

// advanceDrawer 推进一步滑入动画, 返回是否还在动画中。
//
// 用帧计数而不是 wall-clock: 与 tabs/select 的过渡动画同款, 在单测里可
// 精确推进帧数而不依赖 sleep。step 是每帧 1/drawerSlideFrames。
func (n *GuiNode) advanceDrawer() bool {
	target := 0.0
	if n.overlayVisible() {
		target = 1.0
	}
	step := 1.0 / float64(drawerSlideFrames)
	// 收尾阈值加一点容差: 浮点累积会让"距目标正好一步"变成略大于一步
	// (0.1 的 9 次累加 = 0.8999999999999999, 目标距离 = 0.10000000000000009),
	// 严格比较就会多走一帧。1e-9 远小于一帧位移, 只吸收这种舍入误差。
	const snapEps = 1e-9
	p := n.drawerAnim
	switch {
	case p < target:
		if target-p <= step+snapEps {
			p = target
		} else {
			p += step
		}
	case p > target:
		if p-target <= step+snapEps {
			p = target
		} else {
			p -= step
		}
	}
	n.drawerAnim = p
	return p != target
}

// drawerPanelRect 计算内容卡片的静止 (progress=1) 位置: 贴 side 边、纵向铺满、
// 宽度按 width prop。progress<1 时由 paintDrawer 在水平方向做位移。
//
// 单独抽出来是因为**命中测试不该受动画影响**: 卡片滑到一半时点它, 应当命中
// 它的静止位置 (用户看到的卡片位置与命中位置一致靠绘制侧同款公式保证; 这里
// 只提供静止框给布局用)。
func drawerPanelRect(win Rect, side string, w int) Rect {
	if w > win.W {
		w = win.W
	}
	x := win.X + win.W - w
	if side == "left" {
		x = win.X
	}
	return Rect{X: x, Y: win.Y, W: w, H: win.H}
}

// layoutDrawer 布局抽屉: 自身铺满窗口 (作为遮罩), 内容卡片贴 side 边。
//
// 不显示 (open=false) 时把盒子清空: 空盒子整支不绘制、也命不中, 于是"关掉的
// 抽屉"在没从树上摘除的前提下零副作用 —— 与 layoutDialog 同一套可见性约定。
func layoutDrawer(n *GuiNode) {
	if !n.overlayVisible() {
		n.Box = Rect{}
		return
	}
	n.Box = n.windowBox()
	panel := n.drawerPanel()
	if panel == nil {
		placeAbsoluteIn(n, inner(n))
		return
	}
	pr := drawerPanelRect(n.Box, n.drawerSide(), n.drawerWidth())
	panel.Box = pr
	layoutNode(panel)
	placeAbsoluteIn(n, pr)
}

// paintDrawer 画抽屉: 遮罩 + 内容卡片 (带侧边阴影)。
//
// 卡片缺省外观 (白底 + 边框) 在卡片自己没给 background/border 时补上, 让
// 最简用法 <drawer open={x}><column>...</column></drawer> 就有可看的样子 ——
// 与 paintDialog 的处理一致。
//
// 滑入: 卡片按 progress 做水平位移。progress=0 时卡片整体在窗口外, 只剩遮罩
// (遮罩此时也按 progress 淡入, 观感上与滑入同步)。
func paintDrawer(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	progress := n.drawerProgress()
	if progress <= 0 {
		return
	}
	// 遮罩淡入: 与卡片同步。
	FillRect(img, n.Box, withAlpha(colorMask, progress))

	panel := n.drawerPanel()
	if panel == nil || panel.Box.W <= 0 || panel.Box.H <= 0 {
		return
	}
	// 位移: 抽屉在右侧时卡片从右边滑入 (起始位在窗口右缘外), 左侧反之。
	side := n.drawerSide()
	rest := drawerPanelRect(n.Box, side, n.drawerWidth())
	offset := int(float64(rest.W) * (1 - progress))
	drawRect := rest
	if side == "left" {
		drawRect.X = rest.X - offset
	} else {
		drawRect.X = rest.X + offset
	}

	_, hasBg := panel.PropStr("background")
	_, hasBd := panel.PropStr("border")
	if !hasBg && !hasBd {
		FillRect(img, drawRect, tint(colorPopupFace, disabled))
		StrokeRect(img, drawRect, tint(colorPopupEdge, disabled))
	}
	// 侧边分隔线: 抽屉贴边, 与内容之间那条线是"层次感"的来源。
	edgeX := drawRect.X
	if side == "right" {
		edgeX = drawRect.X - 1
	}
	fillLine(img, edgeX, drawRect.Y, edgeX, drawRect.Y+drawRect.H, 1, tint(colorPopupEdge, disabled))
}

// drawerAt 返回 (x, y) 处最上层的可见 drawer, 无则 nil。
// 与 modalAt 同款: 从绘制序末尾往前找, 与压盖关系一致。
func drawerAt(root *GuiNode, x, y int) *GuiNode {
	if root == nil {
		return nil
	}
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		d := esc[i]
		if d.Tag != "drawer" || !d.overlayVisible() {
			continue
		}
		if d.Box.Contains(x, y) {
			return d
		}
	}
	return nil
}

// drawerPanelHit 报告 (x, y) 是否落在内容卡片 (而非遮罩) 上。
// 命中用**静止框**(受控几何)而不是动画中的实时框, 保证"看到哪点到哪"
// 不随动画进度抖动; 关闭路径也只依赖静止框。
func drawerPanelHit(d *GuiNode, x, y int) bool {
	if !d.Box.Contains(x, y) {
		return false
	}
	panel := d.drawerPanel()
	if panel == nil {
		return false
	}
	return drawerPanelRect(d.Box, d.drawerSide(), d.drawerWidth()).Contains(x, y)
}

// closeTopDrawer 关掉最上层的可见 drawer (Esc 兜底路径), 返回是否关掉了一个。
// 与 closeTopDialog 同款: Esc 没有坐标, 只按绘制序倒着取最上面那个。
func (a *app) closeTopDrawer() bool {
	root := a.rootNode()
	if root == nil {
		return false
	}
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		if esc[i].Tag == "drawer" && esc[i].overlayVisible() {
			a.callHandler(esc[i], "onClose", nil)
			return true
		}
	}
	return false
}

// ===== 与动画心跳的接线 =====

// drawerTick 推进一帧抽屉滑入动画, 返回是否还有抽屉在动。
//
// 与 tickerTick (spinner/skeleton) 同一根心跳, 由 animTick 调用: 抽屉在动时
// 心跳续表, 全部到位后一起停表 —— 静止态零开销。
//
// 抽屉的滑入进度是节点自身字段 (drawerAnim), 不是无限循环的加载态, 所以
// 不走 tickerNodes 集合: 每帧从树上现扫一次"还没到位"的抽屉即可 (数量极少)。
func drawerTick() bool {
	a := currentApp()
	if a == nil {
		return false
	}
	pending := drawerAnimateNodes(a.rootNode())
	for _, n := range pending {
		if !n.advanceDrawer() {
			markNodeDirty(n)
			continue
		}
		markNodeDirty(n)
	}
	return len(pending) > 0
}

// drawerAnimateNodes 收集树上所有"滑入未完成"的 drawer, 供 animTick 推进。
// 与 tooltip/过渡动画同一根心跳 (animate.go), 不新增定时器。
func drawerAnimateNodes(root *GuiNode) []*GuiNode {
	if root == nil {
		return nil
	}
	var out []*GuiNode
	var walk func(*GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "drawer" {
			target := 0.0
			if n.overlayVisible() {
				target = 1.0
			}
			if n.drawerAnim != target {
				out = append(out, n)
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// drawerColorOf 是给测试用的稳定取色点: 遮罩中段的颜色。
func drawerColorOf(img *image.RGBA, n *GuiNode) color.RGBA {
	return img.RGBAAt(n.Box.X+2, n.Box.Y+n.Box.H/2)
}
