package gfx

import (
	"image"
)

// scroll 滚动容器 (P2-5; 横向滚动与滚动条拖拽 rSkhXA / RELEASE_NOTES 已知问题 2)。
//
// 结构约定:
//   - 视口 = 容器内容区 (inner) 扣掉滚动条占位: 内容超高让出右侧竖向轨道,
//     内容超宽让出底部横向轨道 (判定规则见 scrollAxes)。子节点被整体平移
//     (-offsetX, -offsetY)。
//   - **子节点的 Box 直接落在"屏幕坐标"上** (布局时已经减去偏移) —— 而不是
//     任务书原文说的"Box 不变 + 绘制变换"。这么做是为了让滚动只发生在
//     一个地方: 命中测试、脏矩形比对 (diffRects 比较 Box 的 PrevBox)、
//     光标定位都不需要再单独处理偏移。代价是每帧布局要重排一次子节点
//     (Layout 本来就每帧跑, 增量可忽略)。
//   - 裁剪: 进入 scroll 子树时把可绘制范围收缩到视口 (见 raster.go 的
//     drawNode), 于是"内容溢出容器"不会画到外面。
//   - 滚动条: 右侧/底部 8px 轨道 + 滑块 (边长 = 视口/内容 比例, 位置 = 偏移
//     比例, 最短 scrollMinThumb)。滑块可拖拽 (beginScrollDrag → scrollDragTo,
//     与 slider 共用 render.go 的 dragTarget 捕获基建)。
//
// offsetX/offsetY/contentW/contentH 是节点的运行时字段 (与 hovered/expanded
// 同类): 来自用户滚动与布局测量结果, 不是 props。

const (
	scrollTrackW   = 8  // 滚动条轨道厚度 (右侧竖向与底部横向共用)
	scrollMinThumb = 24 // 滑块最小边长 (内容极长时也要能抓住)
	scrollDefH     = 200

	// wheelDeltaUnit 是 Windows 一格滚轮的原始增量 (WHEEL_DELTA)。
	// scrollNotch 是一格滚轮对应的内容位移像素 (约两行列表项)。
	wheelDeltaUnit = 120
	scrollNotch    = 60
)

// scrollInChain 从 n 起沿祖先链找第一个 scroll (滚轮分发用: 光标可能落在
// 滚动区域里的任意子节点上)。
func scrollInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "scroll" {
			return p
		}
	}
	return nil
}

// scrollAxes 判定两个方向的滚动条是否出现。
//
// 竖向滚动条看"内容高 > 内容区高"; 横向滚动条看"内容宽 > 扣掉竖向轨道后的
// 视口宽" —— 与浏览器一致: 竖向滚动条先出现, 把可用宽度压窄后内容再溢出
// 才轮到横向。layoutScroll 的测量与 scrollViewport 的视口收缩都用这一份
// 判定, 保证"画出来的轨道"与"点得到/画得出的视口"永远同一套。
func (n *GuiNode) scrollAxes() (v, h bool) {
	area := inner(n)
	v = n.contentH > area.H
	w := area.W
	if v {
		w -= scrollTrackW
	}
	h = n.contentW > w
	return v, h
}

// scrollViewport 返回内容区里真正可见的那部分 (按 scrollAxes 扣掉轨道占位)。
func (n *GuiNode) scrollViewport() Rect {
	area := inner(n)
	v, h := n.scrollAxes()
	if v {
		area.W -= scrollTrackW
		if area.W < 0 {
			area.W = 0
		}
	}
	if h {
		area.H -= scrollTrackW
		if area.H < 0 {
			area.H = 0
		}
	}
	return area
}

// scrollMaxOffset 是 offsetY 的上限 (内容高 - 视口高); 内容不足一屏时为 0。
func (n *GuiNode) scrollMaxOffset() int {
	max := n.contentH - n.scrollViewport().H
	if max < 0 {
		max = 0
	}
	return max
}

// scrollMaxOffsetX 是 offsetX 的上限 (内容宽 - 视口宽); 内容不超宽时为 0。
func (n *GuiNode) scrollMaxOffsetX() int {
	max := n.contentW - n.scrollViewport().W
	if max < 0 {
		max = 0
	}
	return max
}

// scrollBy 按给定像素滚动 (正数 = 内容上移/左移, 即向右/向下滚)。返回值表示
// 偏移是否**任一方向**真的改变了 —— 两个方向都已到边界时返回 false, 调用方
// 据此决定要不要把滚轮事件继续往外传 (与 DOM 的滚动链一致)。
func (n *GuiNode) scrollBy(dx, dy int) bool {
	moved := false
	if dy != 0 {
		if v := clampScrollOffset(n.offsetY+dy, n.scrollMaxOffset()); v != n.offsetY {
			n.offsetY = v
			moved = true
		}
	}
	if dx != 0 {
		if v := clampScrollOffset(n.offsetX+dx, n.scrollMaxOffsetX()); v != n.offsetX {
			n.offsetX = v
			moved = true
		}
	}
	if moved {
		markNodeDirty(n)
	}
	return moved
}

// clampScrollOffset 把候选偏移钳进 [0, max]。max 为负 (内容不足一屏) 时
// 视为 0 —— 不修的话 "钳位" 会把偏移推成负数, 后续滚动判定全部失真。
func clampScrollOffset(v, max int) int {
	if max < 0 {
		max = 0
	}
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// scrollThumb 返回竖向滑块的矩形 (不需要滚动条时 ok=false)。
// 比例与行程按**视口** (扣轨后) 算; 位置 X 钉在 inner 最右一条轨道上
// (轨道与视口并排, 不占视口 —— 见 paintScroll)。
func (n *GuiNode) scrollThumb() (Rect, bool) {
	area := inner(n)
	vp := n.scrollViewport()
	max := n.scrollMaxOffset()
	if max <= 0 || vp.H <= 0 || area.W < scrollTrackW {
		return Rect{}, false
	}
	// max > 0 蕴含 contentH > vp.H >= 1, 除法安全。
	th := vp.H * vp.H / n.contentH
	if th < scrollMinThumb {
		th = scrollMinThumb
	}
	if th > vp.H {
		th = vp.H
	}
	pos := 0
	if span := vp.H - th; span > 0 {
		pos = span * n.offsetY / max
	}
	return Rect{
		X: area.X + area.W - scrollTrackW + 1, Y: area.Y + pos,
		W: scrollTrackW - 2, H: th,
	}, true
}

// scrollThumbX 返回横向滑块的矩形 (不需要滚动条时 ok=false)。
// 比例与行程按**视口**算; 位置 Y 钉在 inner 最下一条轨道上。
func (n *GuiNode) scrollThumbX() (Rect, bool) {
	area := inner(n)
	vp := n.scrollViewport()
	max := n.scrollMaxOffsetX()
	if max <= 0 || vp.W <= 0 || area.H < scrollTrackW {
		return Rect{}, false
	}
	tw := vp.W * vp.W / n.contentW
	if tw < scrollMinThumb {
		tw = scrollMinThumb
	}
	if tw > vp.W {
		tw = vp.W
	}
	pos := 0
	if span := vp.W - tw; span > 0 {
		pos = span * n.offsetX / max
	}
	return Rect{
		X: area.X + pos, Y: area.Y + area.H - scrollTrackW + 1,
		W: tw, H: scrollTrackW - 2,
	}, true
}

// scrollThumbAt 在 root 子树里找滑块矩形覆盖 (x,y) 的 scroll 节点, 找不到
// 返回 nil。多个滑块重叠时取**最深**的 (内层优先, 与命中测试的深优先一致)。
//
// 鼠标按下走这里而不是常规命中测试: hittest.go 把滚动条占位区从"可命中的
// 子内容"里排除了, HitTestDeep 对滑块区域返回 nil, 常规路径抓不到滑块。
func scrollThumbAt(root *GuiNode, x, y int) *GuiNode {
	if root == nil {
		return nil
	}
	var hit *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.Tag == "scroll" && !n.disabledInChain() {
			if th, ok := n.scrollThumb(); ok && th.Contains(x, y) {
				hit = n
			}
			if th, ok := n.scrollThumbX(); ok && th.Contains(x, y) {
				hit = n
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return hit
}

// scrollDragTo 把拖拽中的鼠标坐标应用到偏移上 (拖拽 = 捕获期间 MouseMove
// 全部喂给被拖的 scroll, 见 render.go 的 dragMove)。
//
// 换算基于 beginScrollDrag 记下的 grab 快照: 鼠标位移 × (可滚范围 / 滑块
// 可行行程)。比例用**当下**的 max 与行程 —— 拖拽期间内容被脚本改动时也能
// 跟着最新布局走; 起点 offsetY 用快照值, 避免滑块位置反馈造成的"越拖越快"。
func (n *GuiNode) scrollDragTo(x, y int) {
	changed := false
	if max := n.scrollMaxOffset(); max > 0 {
		if _, ok := n.scrollThumb(); ok {
			span := n.scrollViewport().H - n.scrollThumbH()
			if span > 0 {
				v := clampScrollOffset(n.scrollGrabOffY+(y-n.scrollGrabY)*max/span, max)
				if v != n.offsetY {
					n.offsetY = v
					changed = true
				}
			}
		}
	}
	if max := n.scrollMaxOffsetX(); max > 0 {
		if _, ok := n.scrollThumbX(); ok {
			span := n.scrollViewport().W - n.scrollThumbW()
			if span > 0 {
				v := clampScrollOffset(n.scrollGrabOffX+(x-n.scrollGrabX)*max/span, max)
				if v != n.offsetX {
					n.offsetX = v
					changed = true
				}
			}
		}
	}
	if changed {
		markNodeDirty(n)
	}
}

// scrollThumbH / scrollThumbW 取滑块边长 (拖拽行程换算用; 滑块不存在时 0)。
func (n *GuiNode) scrollThumbH() int {
	if th, ok := n.scrollThumb(); ok {
		return th.H
	}
	return 0
}

func (n *GuiNode) scrollThumbW() int {
	if th, ok := n.scrollThumbX(); ok {
		return th.W
	}
	return 0
}

func layoutScroll(n *GuiNode) {
	area := inner(n)
	if area.W <= 0 || area.H <= 0 {
		n.contentH = 0
		n.contentW = 0
		n.offsetX = 0
		n.offsetY = 0
		placeAbsoluteIn(n, area)
		return
	}

	// 第一遍: 按完整视口堆叠, 得到内容总高与总宽 (总宽 = 流内子最大固有宽)。
	contentH := layoutContentColumn(n, area.X, area.Y, area.W)
	contentW := flowContentWidth(n)

	// 轨道判定与 scrollAxes 同一套: 内容超高让出右轨, 扣窄后内容再溢出
	// 才让出底轨。收窄后重排一次 (v1 无自动换行, 内容尺寸不随宽度变化,
	// 不会出现"有滚动条→变窄→没滚动条"的来回抖动; 将来加 wrap 需要收敛判断)。
	v, h := n.scrollAxesWith(contentH, contentW)
	w, hgt := area.W, area.H
	if v {
		w -= scrollTrackW
		if w < 0 {
			w = 0
		}
	}
	if h {
		hgt -= scrollTrackW
		if hgt < 0 {
			hgt = 0
		}
	}
	if w != area.W {
		contentH = layoutContentColumn(n, area.X, area.Y, w)
	}

	n.contentH = contentH
	n.contentW = contentW

	// 钳位偏移 (内容变短/变窄后旧的偏移可能越界)
	n.offsetY = clampScrollOffset(n.offsetY, contentH-hgt)
	if n.offsetY < 0 {
		n.offsetY = 0
	}
	n.offsetX = clampScrollOffset(n.offsetX, contentW-w)
	if n.offsetX < 0 {
		n.offsetX = 0
	}

	// 第二遍: 按最终偏移摆放 (子节点 Box 即屏幕坐标)
	if n.offsetX != 0 || n.offsetY != 0 {
		layoutContentColumn(n, area.X-n.offsetX, area.Y-n.offsetY, w)
	}
	abs := area
	abs.X -= n.offsetX
	abs.Y -= n.offsetY
	placeAbsoluteIn(n, abs)
}

// scrollAxesWith 用给定的内容尺寸跑 scrollAxes 的判定 (布局中内容尺寸还是
// 局部变量, 还没写回字段 —— 判定逻辑与 scrollAxes 保持一致, 见那边的说明)。
func (n *GuiNode) scrollAxesWith(contentH, contentW int) (v, h bool) {
	area := inner(n)
	v = contentH > area.H
	w := area.W
	if v {
		w -= scrollTrackW
	}
	h = contentW > w
	return v, h
}

// flowContentWidth 测内容的固有总宽 (相对内容区左沿 x0 的最大右边缘)。
// 取**固有宽** (intrinsicSize) 而不是布局后的 Box.W: 默认 stretch 的子节点
// 铺满视口是"跟随容器", 不构成横向溢出 —— 与浏览器一致, 块级子元素默认
// 不触发横向滚动条, 只有显式更宽 (或文本等固有内容超宽) 才滚。
// 绝对定位/弹层子节点不算 (与 contentH 只统计流内子是同一约定 —— 它们
// 不该撑出横向滚动条, 溢出部分由 z 序与裁剪处理)。align 的 center/end 偏移
// 不参与: 那是把不超宽的内容在视口内摆放, 不是溢出。
func flowContentWidth(n *GuiNode) int {
	right := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, _ := c.intrinsicSize()
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		if r := cw + 2*mg; r > right {
			right = r
		}
	}
	return right
}

// layoutContentColumn 把流内子节点在 (x, y) 处纵向堆叠, 宽度给定、高度不限,
// 返回内容总高 (含 gap 与 margin)。交叉轴按 alignItems 处理: 默认 stretch
// 铺满 w —— 滚动列表的行通常要占满视口宽度。
// 横向滚动时调用方传 x = 视口左沿 - offsetX: 子节点 Box 一次落到屏幕坐标。
func layoutContentColumn(n *GuiNode, x, y, w int) int {
	g := n.gapOf()
	align := n.alignItems()
	pos := 0
	first := true

	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue // 绝对定位/弹层: 不占内容高度 (由 placeAbsoluteIn 摆放)
		}
		cw, ch := c.intrinsicSize()
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		if !first {
			pos += g
		}
		first = false
		pos += mg

		cross := cw
		if align == "stretch" && !c.hasExplicitCross(false) &&
			(cross == 0 || c.stretchesCross()) {
			cross = w - 2*mg
		}
		if cross < 0 {
			cross = 0
		}
		off := 0
		switch align {
		case "center":
			off = (w - cross - 2*mg) / 2
		case "end":
			off = w - cross - 2*mg
		}
		if off < 0 {
			off = 0
		}

		// 文本块: 盒宽定下来才能知道折几行 (见 textblock.go)
		ch = c.blockHeight(cross, ch)

		c.Box = Rect{X: x + mg + off, Y: y + pos, W: cross, H: ch}
		layoutNode(c)
		pos += ch + mg
	}
	return pos
}

// paintScroll 画滚动条 (右侧竖向 + 底部横向轨道与滑块)。容器本身不画底色,
// 按需读 background/border (与通用盒子一致)。
// 轨道钉在 inner 的最右/最下边缘 (与视口并排, 不占视口); 两个轨道同现时
// 横向轨道画到竖向轨道左沿为止, 交叉角归竖向轨道。
func paintScroll(img *image.RGBA, n *GuiNode, disabled bool) {
	if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(bg, disabled))
	}
	if bd, ok := n.borderFor(); ok {
		StrokeRect(img, n.Box, tint(bd, disabled))
	}
	area := inner(n)
	if thumb, ok := n.scrollThumb(); ok {
		FillRect(img, Rect{
			X: area.X + area.W - scrollTrackW, Y: area.Y,
			W: scrollTrackW, H: area.H,
		}, tint(colorScrollTrack, disabled))
		FillRect(img, thumb, tint(colorScrollThumb, disabled))
	}
	if thumb, ok := n.scrollThumbX(); ok {
		trackW := area.W
		if _, vok := n.scrollThumb(); vok {
			trackW -= scrollTrackW
		}
		if trackW > 0 {
			FillRect(img, Rect{
				X: area.X, Y: area.Y + area.H - scrollTrackW,
				W: trackW, H: scrollTrackW,
			}, tint(colorScrollTrack, disabled))
		}
		FillRect(img, thumb, tint(colorScrollThumb, disabled))
	}
}
