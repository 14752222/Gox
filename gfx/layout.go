package gfx

// 布局 (P3): flex 风格子集。
//   - 容器: column/row, gap/padding, alignItems (stretch|start|center|end),
//     justifyContent (start|center|end|between), 子节点 flexGrow 简版
//   - 子节点: width/height/margin; 文本节点 (#text/text) 有固有尺寸
//     (按字体测量), 未显式指定尺寸时使用
//   - 文本单行优先, 绘制阶段按 maxWidth 截断
//
// 简化 (与 CSS 的差异, 见任务书): 只支持单层主轴尺寸分配, 不支持
// flexShrink/order/wrap; alignItems 默认 stretch。

// Layout 以给定画布尺寸对根节点做一次布局 (自顶向下写 Box)。
func Layout(root *GuiNode, w, h int) {
	if root == nil {
		return
	}
	root.Box = Rect{X: 0, Y: 0, W: w, H: h}
	layoutNode(root)
}

// inner 返回节点内容区 (减去 padding)。
func inner(n *GuiNode) Rect {
	pad, _ := n.PropNum("padding")
	if pad < 0 {
		pad = 0
	}
	p := int(pad)
	return Rect{
		X: n.Box.X + p, Y: n.Box.Y + p,
		W: n.Box.W - 2*p, H: n.Box.H - 2*p,
	}
}

// layoutNode 布局 n 的子节点 (n.Box 已定)。
func layoutNode(n *GuiNode) {
	switch n.Tag {
	case "column":
		layoutStack(n, false)
	case "row":
		layoutStack(n, true)
	default:
		// 非容器: 子节点以内容区左上角为原点, 按自身 width/height 定位
		area := inner(n)
		for _, c := range n.Children {
			cw, ch := c.intrinsicSize()
			c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
			layoutNode(c)
		}
	}
}

// intrinsicSize 返回节点的期望尺寸: 显式 width/height 优先,
// 文本节点按字体测量, 其余为 0。
func (n *GuiNode) intrinsicSize() (w, h int) {
	if v, ok := n.PropNum("width"); ok {
		w = int(v)
	}
	if v, ok := n.PropNum("height"); ok {
		h = int(v)
	}
	if n.Tag == "#text" || (n.Tag == "text" && n.TextContent() != "") {
		if w == 0 || h == 0 {
			tw, th := MeasureText(n.TextContent(), n.FontSize())
			if w == 0 {
				w = tw
			}
			if h == 0 {
				h = th
			}
		}
	}
	return w, h
}

// layoutStack 布局 column/row 容器。
func layoutStack(n *GuiNode, horizontal bool) {
	area := inner(n)
	gap, _ := n.PropNum("gap")
	g := int(gap)
	if g < 0 {
		g = 0
	}
	align := n.alignItems()

	type slot struct {
		child       *GuiNode
		main, cross int // 不含 margin 的尺寸
		margin      int
		grow        float64
	}
	slots := make([]slot, 0, len(n.Children))
	totalMain := 0
	var sumGrow float64

	for _, c := range n.Children {
		cw, ch := c.intrinsicSize()
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		grow, _ := c.PropNum("flexGrow")
		s := slot{child: c, margin: mg, grow: grow}
		if horizontal {
			s.main, s.cross = cw, ch
		} else {
			s.main, s.cross = ch, cw
		}
		totalMain += s.main + 2*mg
		sumGrow += grow
		slots = append(slots, s)
	}
	if len(slots) == 0 {
		return
	}

	areaMain, areaCross := area.H, area.W
	if horizontal {
		areaMain, areaCross = area.W, area.H
	}
	free := areaMain - totalMain - g*(len(slots)-1)

	// 主轴富余分配: flexGrow 优先, 否则按 justifyContent
	lead, betweenGap := 0, 0
	if free > 0 && sumGrow > 0 {
		for i := range slots {
			if slots[i].grow > 0 {
				slots[i].main += int(float64(free) * slots[i].grow / sumGrow)
			}
		}
	} else if free > 0 {
		switch n.justifyContent() {
		case "center":
			lead = free / 2
		case "end":
			lead = free
		case "between":
			if len(slots) > 1 {
				betweenGap = free / (len(slots) - 1)
			}
		}
	}

	// 依序摆放
	pos := lead
	for i, s := range slots {
		if i > 0 {
			pos += g + betweenGap
		}
		pos += s.margin

		// 交叉轴: 显式尺寸直接用; 否则 stretch 占满, start/center/end 用固有尺寸
		cross := s.cross
		if cross == 0 && align == "stretch" {
			cross = areaCross - 2*s.margin
		}
		crossOffset := 0
		if s.cross > 0 || align != "stretch" {
			switch align {
			case "center":
				crossOffset = (areaCross - cross - 2*s.margin) / 2
			case "end":
				crossOffset = areaCross - cross - 2*s.margin
			}
		}
		if cross < 0 {
			cross = 0
		}
		if crossOffset < 0 {
			crossOffset = 0
		}

		c := s.child
		if horizontal {
			c.Box = Rect{X: area.X + pos, Y: area.Y + s.margin + crossOffset, W: s.main, H: cross}
		} else {
			c.Box = Rect{X: area.X + s.margin + crossOffset, Y: area.Y + pos, W: cross, H: s.main}
		}
		layoutNode(c)
		pos += s.main + s.margin
	}
}

// alignItems 读取容器交叉轴对齐 (默认 stretch)。
func (n *GuiNode) alignItems() string {
	if v, ok := n.PropStr("alignItems"); ok {
		return v
	}
	return "stretch"
}

// justifyContent 读取容器主轴分布 (默认 start)。
func (n *GuiNode) justifyContent() string {
	if v, ok := n.PropStr("justifyContent"); ok {
		return v
	}
	return "start"
}
