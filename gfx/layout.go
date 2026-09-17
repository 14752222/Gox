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
	case "button":
		layoutButton(n)
	case "select":
		layoutSelect(n)
	case "select-option":
		// 下拉项: 一行内容垂直居中 + 8px 左留白 (与 button 同一套排布)
		layoutInlineRow(n, fieldPadX, 0)
	case "select-popup":
		// 下拉弹层: 纵排选项 (盒子由 layoutSelect 定: 贴在字段正下方且等宽)
		layoutStack(n, false)
	case "dialog":
		layoutDialog(n)
	case "toast":
		layoutToast(n)
	case "slot":
		// 动态子节点占位容器: 单子时子节点直接占满 slot 的盒子 (slot 的尺寸
		// 就是按这个子节点算出来的, 等价于子节点直接挂在祖父下面); 多子
		// (列表渲染) 时按父容器方向堆叠。
		if c := n.slotChild(); c != nil {
			c.Box = n.Box
			layoutNode(c)
			return
		}
		layoutStack(n, n.slotHorizontal())
	default:
		// 非容器: 子节点以内容区左上角为原点, 按自身 width/height 定位
		area := inner(n)
		for _, c := range n.Children {
			if !c.isFlowChild() {
				continue // 绝对定位/弹层子节点不参与常规流, 循环后统一摆放
			}
			cw, ch := c.intrinsicSize()
			c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
			layoutNode(c)
		}
		placeAbsoluteIn(n, area)
	}
}

// intrinsicSize 返回节点的期望尺寸: 显式 width/height 优先, 内置组件有
// 缺省固有尺寸, button 按内容尺寸, 文本节点按字体测量, 其余为 0。
func (n *GuiNode) intrinsicSize() (w, h int) {
	if v, ok := n.PropNum("width"); ok {
		w = int(v)
	}
	if v, ok := n.PropNum("height"); ok {
		h = int(v)
	}
	switch n.Tag {
	case "column", "row":
		// 容器按内容确定尺寸: 主轴 = 子节点累加 (+gap), 交叉轴 = 最大者,
		// 两侧各加 padding。缺了这条, 嵌套容器恒为 0 尺寸, 而 drawNode 会
		// 跳过"自身盒为空"的子树 → 嵌套几层就整片不渲染。
		cw, ch := stackContentSize(n, n.Tag == "row")
		if w == 0 {
			w = cw
		}
		if h == 0 {
			h = ch
		}
	case "slot":
		if c := n.slotChild(); c != nil {
			// 单子 slot 对布局透明: 尺寸完全跟随子节点
			cw, ch := c.intrinsicSize()
			if w == 0 {
				w = cw
			}
			if h == 0 {
				h = ch
			}
		} else {
			cw, ch := stackContentSize(n, n.slotHorizontal())
			if w == 0 {
				w = cw
			}
			if h == 0 {
				h = ch
			}
		}
	case "checkbox", "radio":
		if w == 0 {
			w = 18
		}
		if h == 0 {
			h = 18
		}
	case "switch":
		if w == 0 {
			w = 36
		}
		if h == 0 {
			h = 20
		}
	case "progress":
		if w == 0 {
			w = 200
		}
		if h == 0 {
			h = 8
		}
	case "separator":
		// 横线: 高 1、宽 0 → 由父容器 stretch 撑开; 纵线: 反之。
		// (纵线在 column 里主轴不被 stretch, 需显式 height。)
		if n.vertical() {
			if w == 0 {
				w = 1
			}
		} else if h == 0 {
			h = 1
		}
	case "spacer":
		// 弹性占位: 无固有尺寸、不绘制, 靠 flexGrow 吃掉主轴富余空间
	case "button":
		// 内容尺寸: 让缺省外观有实体高度 (此前 button 无尺寸 → 0 高空盒,
		// 既不可见也命不中)。v1 多子节点按横排累加, 不做自动换行。
		cw, ch := n.contentSize()
		padX, padY := n.buttonPadding()
		if w == 0 {
			w = cw + 2*padX
		}
		if h == 0 {
			h = ch + 2*padY
		}
	case "select":
		// 字段行: 高 28; 宽 = 当前值/placeholder 文本 + 两侧留白 + 箭头。
		// 宽度按内容算而不是 stretch: select 放在 column 里被拉满时, 箭头
		// 会跑到离文字很远的地方 (脚本仍可显式给 width 覆盖)。
		label, ok := n.selectLabel()
		if !ok {
			label = n.selectPlaceholder()
		}
		tw, _ := MeasureText(label, n.FontSize())
		if w == 0 {
			w = tw + 2*fieldPadX + selectArrowW
			if w < selectMinW {
				w = selectMinW
			}
		}
		if h == 0 {
			h = selectRowH
		}
	case "select-option":
		tw, _ := MeasureText(n.TextContent(), n.FontSize())
		if w == 0 {
			w = tw + 2*fieldPadX
		}
		if h == 0 {
			h = selectRowH
		}
	case "toast":
		// 提示卡片: 宽按 message 文本 + 左侧色条 + 两侧留白; 高给 36 (单行
		// 文本在 36px 卡片里垂直居中看起来才不局促)。
		tw, th := MeasureText(n.toastMessage(), n.FontSize())
		if w == 0 {
			w = tw + toastAccentW + 2*fieldPadX
		}
		if h == 0 {
			h = th + 16
			if h < 36 {
				h = 36
			}
		}
	case "input":
		// 单行输入框: 高 28 (与 select 同一套字段常量); 宽度按内容算不合适
		// —— 文字会随打字变长, 宽度跟着跳变很难看, 所以给一个固定缺省值,
		// 需要更宽就显式写 width。
		if w == 0 {
			w = inputMinW
		}
		if h == 0 {
			h = selectRowH
		}
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

// contentSize 返回子节点按声明顺序横排所需的内容区尺寸
// (宽度累加, 高度取最大)。绝对定位/弹层子节点不占位。
func (n *GuiNode) contentSize() (w, h int) {
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		w += cw
		if ch > h {
			h = ch
		}
	}
	return w, h
}

// stackContentSize 按给定方向计算"堆叠子节点"所需的外框尺寸
// (主轴累加 + gap + margin, 交叉轴取最大, 两侧再加 padding)。
// column/row 容器与多子 slot (列表渲染) 共用同一套算法。
// 绝对定位/弹层子节点不参与: 它们不占位, 不能把容器撑大。
func stackContentSize(n *GuiNode, horizontal bool) (w, h int) {
	pad, _ := n.PropNum("padding")
	p := int(pad)
	if p < 0 {
		p = 0
	}
	g := n.gapOf()
	var contentMain, contentCross int
	placed := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		if placed > 0 {
			contentMain += g
		}
		placed++
		if horizontal {
			contentMain += cw + 2*mg
			contentCross = max(contentCross, ch+2*mg)
		} else {
			contentMain += ch + 2*mg
			contentCross = max(contentCross, cw+2*mg)
		}
	}
	if horizontal {
		return contentMain + 2*p, contentCross + 2*p
	}
	return contentCross + 2*p, contentMain + 2*p
}

// placeAbsoluteIn 摆放容器内"脱离常规流"的直系子节点 (P2-2):
// 相对容器内容区按 left/top 定位, 尺寸取自身固有尺寸。
//
// 放在常规流摆完之后统一处理, 是因为绝对定位的参考系 (内容区) 与
// 兄弟节点的排布结果无关 —— 先排完流内子节点再落弹层, 顺序更清楚。
func placeAbsoluteIn(n *GuiNode, area Rect) {
	for _, c := range n.Children {
		if c.isFlowChild() {
			continue
		}
		l, t := c.absoluteOffset()
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X + l, Y: area.Y + t, W: cw, H: ch}
		layoutNode(c)
	}
}

// gapOf 读取容器的子节点间距。slot 没有自己的 gap 时跟随父容器: 列表渲染
// 写进 gap 容器后, 列表项的间距与直接写子元素时一致 (否则 slot 只能用
// 默认 0, 写 <column gap={4}>{() => items.map(...)}</column> 会挤在一起)。
func (n *GuiNode) gapOf() int {
	v, ok := n.PropNum("gap")
	if !ok && n.Tag == "slot" && n.Parent != nil {
		v, _ = n.Parent.PropNum("gap")
	}
	if v < 0 {
		return 0
	}
	return int(v)
}

// layoutButton 摆放 button 的内容区: 内边距走 buttonPadding (缺省 8/6)。
func layoutButton(n *GuiNode) {
	padX, padY := n.buttonPadding()
	layoutInlineRow(n, padX, padY)
}

// layoutInlineRow 摆放"单行内容": 子节点按声明序横排, 交叉轴垂直居中,
// 四周预留 padX/padY 内边距。v1 不换行。
//
// 抽出来是给 button 与下拉项共用 —— 两者的内容排布规则完全一样
// ("一行文字居中"), 复制一份只会让后续调整漏掉其中一处。
func layoutInlineRow(n *GuiNode, padX, padY int) {
	area := Rect{
		X: n.Box.X + padX, Y: n.Box.Y + padY,
		W: n.Box.W - 2*padX, H: n.Box.H - 2*padY,
	}
	if area.W < 0 {
		area.W = 0
	}
	if area.H < 0 {
		area.H = 0
	}
	x := area.X
	right := area.X + area.W
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue // 绝对定位子节点不参与横排 (下面统一摆放)
		}
		cw, ch := c.intrinsicSize()
		if x+cw > right { // 内容超出内容区: 截断宽度 (文本绘制本身也会截断)
			cw = right - x
		}
		if cw < 0 {
			cw = 0
		}
		c.Box = Rect{X: x, Y: area.Y + (area.H-ch)/2, W: cw, H: ch}
		layoutNode(c)
		x += cw
	}
	placeAbsoluteIn(n, area)
}

// layoutStack 布局 column/row 容器 (以及多子 slot)。
func layoutStack(n *GuiNode, horizontal bool) {
	area := inner(n)
	g := n.gapOf()
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
		if !c.isFlowChild() {
			continue // 绝对定位/弹层: 不参与主轴分配 (见函数末尾统一摆放)
		}
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
		// 全是绝对定位子节点: 内容区还是要作为它们的参考系
		placeAbsoluteIn(n, area)
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

		// 交叉轴: 显式尺寸直接用; 容器与无固有尺寸的节点在 stretch 下占满,
		// 其余组件保持内容尺寸 (checkbox/button 被拉满会变形)。
		cross := s.cross
		if align == "stretch" && !s.child.hasExplicitCross(horizontal) &&
			(cross == 0 || s.child.stretchesCross()) {
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
	placeAbsoluteIn(n, area)
}

// alignItems 读取容器交叉轴对齐 (默认 stretch)。
func (n *GuiNode) alignItems() string {
	if v, ok := n.PropStr("alignItems"); ok {
		return v
	}
	return "stretch"
}

// isContainer 报告节点是否为 flex 容器。
func (n *GuiNode) isContainer() bool {
	return n.Tag == "column" || n.Tag == "row"
}

// stretchesCross 报告节点在父容器 alignItems=stretch 时是否占满交叉轴:
// flex 容器总是占满 (内容尺寸只当下限); 单子 slot 则跟随它那个子节点
// (slot 对布局透明), 多子 slot 按容器处理。
//
// 下拉项也占满: 它是"整行"元素, 高亮底色只有铺满弹层宽度才像一条选项
// (只盖住文字宽度会显得像文本背景色)。
func (n *GuiNode) stretchesCross() bool {
	if n.Tag == "slot" {
		if c := n.slotChild(); c != nil {
			return c.stretchesCross()
		}
		return true
	}
	if n.Tag == "select-option" {
		return true
	}
	return n.isContainer()
}

// hasExplicitCross 报告节点是否显式指定了交叉轴尺寸 (父容器是 row 时
// 交叉轴为高, 否则为宽): 显式值即"定死", 不参与 stretch 拉伸。
// 单子 slot 委托给子节点, 保持"透明"。
func (n *GuiNode) hasExplicitCross(parentHorizontal bool) bool {
	if n.Tag == "slot" {
		if c := n.slotChild(); c != nil {
			return c.hasExplicitCross(parentHorizontal)
		}
	}
	name := "width"
	if parentHorizontal {
		name = "height"
	}
	_, ok := n.PropNum(name)
	return ok
}

// justifyContent 读取容器主轴分布 (默认 start)。
func (n *GuiNode) justifyContent() string {
	if v, ok := n.PropStr("justifyContent"); ok {
		return v
	}
	return "start"
}
