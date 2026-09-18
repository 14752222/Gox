package gfx

import (
	"image"
)

// scroll 滚动容器 (P2-5)。
//
// 结构约定:
//   - 视口 = 容器内容区 (inner) 去掉右侧滚动条占位; 内容超出视口高度时
//     offsetY 生效, 子节点被整体上移 offsetY。
//   - **子节点的 Box 直接落在"屏幕坐标"上** (布局时已经减去 offsetY) —— 而不是
//     任务书原文说的"Box 不变 + 绘制变换"。这么做是为了让滚动只发生在
//     一个地方: 命中测试、脏矩形比对 (diffRects 比较 Box 的 PrevBox)、
//     光标定位都不需要再单独处理偏移。代价是每帧布局要重排一次子节点
//     (Layout 本来就每帧跑, 增量可忽略)。
//   - 裁剪: 进入 scroll 子树时把可绘制范围收缩到视口 (见 raster.go 的
//     drawNode), 于是"内容溢出容器"不会画到外面。
//   - 滚动条: 右侧 8px 轨道 + 滑块 (高 = 视口/内容 比例, 位置 = 偏移比例)。
//     鼠标拖拽滚动条 v1 未做 (见文末说明)。
//
// offsetY / contentH 是节点的运行时字段 (与 hovered/expanded 同类):
// 来自用户滚动与布局测量结果, 不是 props。

const (
	scrollTrackW   = 8  // 右侧滚动条占位宽度
	scrollMinThumb = 24 // 滑块最小高度 (内容极长时也要能抓住)
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

// scrollViewport 返回内容区里真正可见的那部分 (内容超高时右侧让出滚动条)。
func (n *GuiNode) scrollViewport() Rect {
	area := inner(n)
	if n.contentH > area.H {
		area.W -= scrollTrackW
		if area.W < 0 {
			area.W = 0
		}
	}
	return area
}

// scrollMaxOffset 是 offsetY 的上限 (内容高 - 视口高); 内容不足一屏时为 0。
func (n *GuiNode) scrollMaxOffset() int {
	max := n.contentH - inner(n).H
	if max < 0 {
		max = 0
	}
	return max
}

// scrollBy 按给定像素滚动 (正数 = 内容上移, 即向下滚)。返回值表示偏移是否
// 真的改变了 —— 已经在边界上返回 false, 调用方据此决定要不要把滚轮事件
// 继续往外传 (与 DOM 的滚动链一致)。
func (n *GuiNode) scrollBy(dy int) bool {
	max := n.scrollMaxOffset()
	if max <= 0 {
		return false
	}
	old := n.offsetY
	v := old + dy
	if v < 0 {
		v = 0
	}
	if v > max {
		v = max
	}
	if v == old {
		return false
	}
	n.offsetY = v
	markNodeDirty(n)
	return true
}

// scrollThumb 返回滑块的矩形 (不需要滚动条时 ok=false)。
func (n *GuiNode) scrollThumb() (Rect, bool) {
	area := inner(n)
	max := n.scrollMaxOffset()
	if max <= 0 || area.H <= 0 || area.W < scrollTrackW {
		return Rect{}, false
	}
	// max > 0 蕴含 contentH > area.H >= 1, 除法安全。
	th := area.H * area.H / n.contentH
	if th < scrollMinThumb {
		th = scrollMinThumb
	}
	if th > area.H {
		th = area.H
	}
	pos := (area.H - th) * n.offsetY / max
	return Rect{
		X: area.X + area.W - scrollTrackW + 1, Y: area.Y + pos,
		W: scrollTrackW - 2, H: th,
	}, true
}

func layoutScroll(n *GuiNode) {
	area := inner(n)
	if area.W <= 0 || area.H <= 0 {
		n.contentH = 0
		n.offsetY = 0
		placeAbsoluteIn(n, area)
		return
	}

	// 第一遍: 按完整内容宽度堆叠, 得到内容总高。
	contentH := layoutContentColumn(n, area.X, area.Y, area.W)
	w := area.W
	if contentH > area.H {
		// 内容超高 → 让出滚动条宽度再排一次。这里只重排一次而不迭代:
		// v1 没有自动换行, 内容高度不随宽度变化, 不会出现"有滚动条→变矮→
		// 没滚动条"的来回抖动 (将来加了 wrap 需要加收敛判断)。
		w = area.W - scrollTrackW
		if w < 0 {
			w = 0
		}
		contentH = layoutContentColumn(n, area.X, area.Y, w)
	}
	n.contentH = contentH

	// 钳位偏移 (内容变短后旧的偏移可能越界)
	max := contentH - area.H
	if max < 0 {
		max = 0
	}
	if n.offsetY > max {
		n.offsetY = max
	}
	if n.offsetY < 0 {
		n.offsetY = 0
	}

	// 第二遍: 按最终偏移摆放 (子节点 Box 即屏幕坐标)
	if n.offsetY != 0 {
		layoutContentColumn(n, area.X, area.Y-n.offsetY, w)
	}
	abs := area
	abs.Y -= n.offsetY
	placeAbsoluteIn(n, abs)
}

// layoutContentColumn 把流内子节点在 (x, y) 处纵向堆叠, 宽度给定、高度不限,
// 返回内容总高 (含 gap 与 margin)。交叉轴按 alignItems 处理: 默认 stretch
// 铺满 w —— 滚动列表的行通常要占满视口宽度。
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

// paintScroll 画滚动条 (轨道 + 滑块)。容器本身不画底色, 按需读
// background/border (与通用盒子一致)。
func paintScroll(img *image.RGBA, n *GuiNode, disabled bool) {
	if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(bg, disabled))
	}
	if bd, ok := n.borderFor(); ok {
		StrokeRect(img, n.Box, tint(bd, disabled))
	}
	thumb, ok := n.scrollThumb()
	if !ok {
		return
	}
	area := inner(n)
	FillRect(img, Rect{
		X: area.X + area.W - scrollTrackW, Y: area.Y,
		W: scrollTrackW, H: area.H,
	}, tint(colorScrollTrack, disabled))
	FillRect(img, thumb, tint(colorScrollThumb, disabled))
}
