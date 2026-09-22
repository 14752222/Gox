package gfx

import (
	"strconv"
	"strings"
)

// 布局 (P3): flex 风格子集。
//   - 容器: column/row, gap/padding, alignItems (stretch|start|center|end),
//     justifyContent (start|center|end|between), 子节点 flexGrow 简版
//   - 子节点: width/height/margin; 文本节点 (#text/text) 有固有尺寸
//     (按字体测量), 未显式指定尺寸时使用
//   - 文本单行优先, 绘制阶段按 maxWidth 截断
//
// 弹性词汇 (2026-09-19, 屏幕 D → §四 布局缺口的第一批): 百分比尺寸
// width="50%"、minWidth/maxWidth/minHeight/maxHeight、flexShrink。
// 第二批: 容器级 wrap (主轴折行, 见 layoutWrapStack)。
// 不支持: order、alignSelf、align-content (见 agent_doc/gui-component-status.md §3.4)。

// Layout 以给定画布尺寸对根节点做一次布局 (自顶向下写 Box)。
func Layout(root *GuiNode, w, h int) {
	if root == nil {
		return
	}
	root.Box = Rect{X: 0, Y: 0, W: w, H: h}
	layoutNode(root)
}

// paddingOf 读节点四边的内边距 (像素, 非负)。
//
// 语义: 统一的 `padding` 是**基准值**, 四边的 paddingTop/Right/Bottom/Left 在它
// 之上做**覆盖** (给了哪边就用哪边, 没给就跟随 padding)。所以
// `padding={12} paddingTop={40}` 得到的是 上 40 / 其余 12 —— 这正是安全区
// (`safeAreaStyle()`) 需要的形态: 应用写的是"通用内边距", 只有被状态栏/刘海
// 压住的那一边需要额外让开。
//
// 为什么不做成"四边相加": 相加会让 `padding={12} paddingTop={40}` 变成 52 ——
// 一个谁都想不到的数字, 而且没有任何一处能解释它。覆盖语义与 CSS 的
// padding vs padding-top 直觉一致。
//
// 返回值取 int 而不是 float64: 像素是整数, 且历史上这个包的四则运算里
// float 反复被误当成 int 用 (见 raster.go 的历史记录)。
func paddingOf(n *GuiNode) (top, right, bottom, left int) {
	base, _ := n.PropNum("padding")
	if base < 0 {
		base = 0
	}
	p := int(base)
	side := func(name string) int {
		v, ok := n.PropNum(name)
		if !ok {
			return p
		}
		if v < 0 {
			return 0
		}
		return int(v)
	}
	return side("paddingTop"), side("paddingRight"), side("paddingBottom"), side("paddingLeft")
}

// inner 返回节点内容区 (减去四边内边距)。
func inner(n *GuiNode) Rect {
	t, r, b, l := paddingOf(n)
	return Rect{
		X: n.Box.X + l, Y: n.Box.Y + t,
		W: n.Box.W - l - r, H: n.Box.H - t - b,
	}
}

// layoutNode 布局 n 的子节点 (n.Box 已定)。
func layoutNode(n *GuiNode) {
	switch n.Tag {
	case "column":
		layoutStack(n, false)
	case "row":
		if n.wrapEnabled() {
			layoutWrapStack(n, true)
		} else {
			layoutStack(n, true)
		}
	case "grid":
		layoutGrid(n)
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
	case "scroll":
		layoutScroll(n)
	case "textarea":
		layoutTextarea(n)
	case "menubar":
		layoutMenuBar(n)
	case "menu":
		layoutMenu(n)
	case "menu-popup":
		layoutMenuPopup(n)
	case "menu-item":
		layoutMenuItem(n)
	case "slot", "view":
		// 动态子节点占位容器: 单子时子节点直接占满 slot 的盒子 (slot 的尺寸
		// 就是按这个子节点算出来的, 等价于子节点直接挂在祖父下面); 多子
		// (列表渲染) 时按父容器方向堆叠。
		if c := n.slotChild(); c != nil {
			c.Box = n.Box
			// 百分比子节点经 slot 的补解析: slot 的固有尺寸里百分比轴贡献 0
			// (intrinsicSize 只认数字), slot 的盒子由外层分配定下来之后这里
			// 才有机会按它解析。slot 自身没分到尺寸时 (主轴 0) 解析结果仍是
			// 0 —— 百分比经 slot 只在交叉轴 stretch 等场景下成立, v1 已知边界。
			if p, ok := c.percentProp("width"); ok {
				c.Box.W = int(float64(n.Box.W) * p)
			}
			if p, ok := c.percentProp("height"); ok {
				c.Box.H = int(float64(n.Box.H) * p)
			}
			c.Box.W = clampDim(c, c.Box.W, "minWidth", "maxWidth")
			c.Box.H = clampDim(c, c.Box.H, "minHeight", "maxHeight")
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
			cw, ch := c.sizeInArea(area.W, area.H)
			c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
			layoutNode(c)
		}
		placeAbsoluteIn(n, area)
	}
}

// intrinsicSize 返回节点的期望尺寸: 显式 width/height 优先, 内置组件有
// 缺省固有尺寸, button 按内容尺寸, 文本节点按字体测量, 其余为 0。
//
// 读的是 effectivePropNumOk (P3-2): 过渡动画期间尺寸走插值, 而且"有动画"
// 必须算作"有显式尺寸" —— 否则动画中途会突然回退到内容尺寸再跳回来。
func (n *GuiNode) intrinsicSize() (w, h int) {
	if v, ok := effectivePropNumOk(n, "width"); ok {
		w = int(v)
	}
	if v, ok := effectivePropNumOk(n, "height"); ok {
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
	case "grid":
		// 网格固有尺寸按内容轨道 (每列/行取最大者); 拿到确定宽度后列改等宽
		// —— 两套口径的差异见 layoutGrid 头注释。
		cw, ch := gridContentSize(n)
		if w == 0 {
			w = cw
		}
		if h == 0 {
			h = ch
		}
	case "slot", "view":
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
	case "scroll":
		// 滚动视口: 宽度不给固有值 (0 → 父容器 stretch 时铺满, 或脚本显式
		// width); 高度必须给缺省值 —— column 的主轴是高度, 0 高会变成
		// 不可见的空盒, 整个滚动区消失。
		if h == 0 {
			h = scrollDefH
		}
	case "textarea":
		// 多行输入框: 宽固定缺省 (理由同 input), 高 = 行数 × 行高 + 上下内边距。
		// 行数用 `rows` 覆盖 (与 HTML textarea 同名)。
		if w == 0 {
			w = textareaMinW
		}
		if h == 0 {
			rows := textareaDefaultRows
			if v, ok := n.PropNum("rows"); ok && int(v) > 0 {
				rows = int(v)
			}
			h = rows*lineHeight(n.FontSize()) + 2*textareaPadY
		}
	case "image":
		// 图片: 未显式给尺寸时用**自然尺寸**; 加载失败给一个非 0 的兜底尺寸。
		// 兜底必须非 0 —— 0 尺寸子树会被 drawNode 整支跳过, 用户看到的是
		// "什么都没有" (与"本来就没放图"分不清), 而不是占位图。
		if w == 0 || h == 0 {
			nw, nh := imageNaturalSize(n)
			if nw <= 0 || nh <= 0 {
				nw, nh = imgPlaceholderW, imgPlaceholderH
			}
			if w == 0 {
				w = nw
			}
			if h == 0 {
				h = nh
			}
		}
	case "canvas":
		// 画布: canvas 不是容器 (stretchesCross 为 false), 拿不到父容器的
		// 交叉轴拉伸, 所以**两个方向都要给缺省值** —— 否则不写 width/height
		// 的 canvas 是个 0 尺寸空盒, 被 drawNode 整支跳过, 界面上看不到,
		// 而 onDraw 明明在跑 (最难排查的一类"没反应")。
		if w == 0 {
			w = canvasDefW
		}
		if h == 0 {
			h = canvasDefH
		}
	case "slider":
		// 滑块: 横向一条, 宽高都按内容定 (轨道在盒内居中铺开), 与 input/select
		// 一样不参与交叉轴 stretch —— 被拉满的滑块很难看, 也失去了"预设长度"
		// 这个语义。需要更宽就显式写 width。
		if w == 0 {
			w = sliderDefW
		}
		if h == 0 {
			h = sliderDefH
		}
	case "menubar":
		// 菜单栏: 高度固定一行, 宽度撑满父容器 (它是"窗口顶部的一条",
		// 不铺满会露出底色)。高度**不吃 stretch**: 被拉到全高的菜单栏是
		// 明显的错误, 而 column 的交叉轴缺省正是 stretch。
		if w == 0 {
			w = menuBarContentWidth(n)
		}
		if h == 0 {
			h = menuBarH
		}
	case "menu":
		// 菜单标题: 一行 26px (右键菜单是纯弹层, 自身无尺寸)。
		if n.ctxMenu {
			return 0, 0
		}
		if w == 0 {
			w = n.menuTitleWidth()
		}
		if h == 0 {
			h = menuBarH
		}
	case "menuitem":
		// 菜单项在声明位置上不绘制也不占位 (它的文字由下拉弹层里的
		// menu-item 画)。给 0 尺寸会让 drawNode 跳过整支 —— 而它的
		// 子 menu (子菜单) 恰恰要能被 layoutMenu 走到, 所以给一个非 0 的
		// 名义行高, 保证子树不被剪掉。
		if h == 0 {
			h = menuItemH
		}
	}
	if n.Tag == "#text" {
		if w == 0 || h == 0 {
			tw, th := MeasureText(n.TextContent(), n.FontSize())
			if w == 0 {
				w = tw
			}
			if h == 0 {
				h = th
			}
		}
	} else if n.Tag == "text" && n.TextContent() != "" {
		if n.wrapsText() {
			// 文本块: 高度按换行后的行数算 (宽度有约束才会真的折行,
			// 没约束时与单行等价)。显式 width 在这里就能生效, 见 textblock.go。
			if w == 0 || h == 0 {
				tw, th := n.blockSize(w)
				if w == 0 {
					w = tw
				}
				if h == 0 {
					h = th
				}
			}
		} else if w == 0 || h == 0 {
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

// percentProp 读取百分比尺寸 prop: "50%" 形态的字符串 → 0.5。
// 非百分比形态 (数字 / 无 % / 格式坏 / 负数) 返回 false —— 坏格式静默忽略,
// 与字段级容错口径一致 ("50x" 这类笔误不至于让布局炸掉)。
func (n *GuiNode) percentProp(name string) (float64, bool) {
	s, ok := n.PropStr(name)
	if !ok || len(s) < 2 || !strings.HasSuffix(s, "%") {
		return 0, false
	}
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return v / 100, true
}

// clampDim 用 min/max prop 钳位一个轴的最终尺寸 (0 或缺省 = 不约束)。
// min 优先于 max 语义上由调用顺序保证 (先钳下限再钳上限, 同 CSS)。
func clampDim(n *GuiNode, base int, minName, maxName string) int {
	if v, ok := n.PropNum(minName); ok && v > 0 && float64(base) < v {
		base = int(v)
	}
	if v, ok := n.PropNum(maxName); ok && v > 0 && float64(base) > v {
		base = int(v)
	}
	return base
}

// sizeInArea 是"子节点尺寸"的统一出口: 固有尺寸 → 百分比解析 (按父容器
// 内容区) → min/max 钳位。layoutStack 的子节点收集、非容器分支与绝对
// 定位都走它, 保证三条路径的词汇行为一致。
//
// 百分比在这里解析而不是塞进 intrinsicSize: "固有"必须是自包含的 (样式
// 文档 C2), 百分比依赖父约束 —— 放分配点 (父内容区已知处) 解析, intrinsic
// 路径里百分比轴天然贡献 0 (intrinsicSize 只认数字), 父容器不会被百分比
// 子节点撑大, 与 CSS 的 auto 尺寸行为一致。
func (n *GuiNode) sizeInArea(areaW, areaH int) (w, h int) {
	w, h = n.intrinsicSize()
	if p, ok := n.percentProp("width"); ok {
		w = int(float64(areaW) * p)
	}
	if p, ok := n.percentProp("height"); ok {
		h = int(float64(areaH) * p)
	}
	w = clampDim(n, w, "minWidth", "maxWidth")
	h = clampDim(n, h, "minHeight", "maxHeight")
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
	pt, pr, pb, pl := paddingOf(n)
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
		return contentMain + pl + pr, contentCross + pt + pb
	}
	return contentCross + pl + pr, contentMain + pt + pb
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
		cw, ch := c.sizeInArea(area.W, area.H) // 固有 → 百分比(按内容区) → min/max
		c.Box = Rect{X: area.X + l, Y: area.Y + t, W: cw, H: ch}
		layoutNode(c)
	}
}

// gapOf 读取容器的子节点间距。透明容器 (slot / view) 没有自己的 gap 时跟随父容器:
// 列表渲染写进 gap 容器后, 列表项的间距与直接写子元素时一致 (否则只能用默认 0,
// 写 <column gap={4}>{() => items.map(...)}</column> 会挤在一起)。
func (n *GuiNode) gapOf() int {
	v, ok := n.PropNum("gap")
	if !ok && n.isPassthrough() && n.Parent != nil {
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
		shrink      float64
	}
	slots := make([]slot, 0, len(n.Children))
	totalMain := 0
	var sumGrow, sumShrink float64

	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue // 绝对定位/弹层: 不参与主轴分配 (见函数末尾统一摆放)
		}
		cw, ch := c.intrinsicSize()
		if p, ok := c.percentProp("width"); ok {
			cw = int(float64(area.W) * p)
		}
		if p, ok := c.percentProp("height"); ok {
			ch = int(float64(area.H) * p)
		}
		// 主轴的 min/max 在收集期钳 (要计入 free 计算); **交叉轴不能在这里钳**
		// —— "cross==0 → 参与拉伸"的判定依赖原始固有值, minWidth 把 0 抬成
		// 非 0 会让节点反而失去 stretch (写盒处的终钳位兜住上限/下限)。
		if horizontal {
			cw = clampDim(c, cw, "minWidth", "maxWidth")
		} else {
			ch = clampDim(c, ch, "minHeight", "maxHeight")
		}
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		grow, _ := c.PropNum("flexGrow")
		shrink, _ := c.PropNum("flexShrink")
		s := slot{child: c, margin: mg, grow: grow, shrink: shrink}
		if horizontal {
			s.main, s.cross = cw, ch
		} else {
			s.main, s.cross = ch, cw
		}
		totalMain += s.main + 2*mg
		sumGrow += grow
		sumShrink += shrink
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
	} else if free < 0 && sumShrink > 0 {
		// 主轴溢出收缩: 按 flexShrink×基础尺寸 加权分摊 (CSS 同款权重 ——
		// 纯按系数分摊会让大个子欠收)。尺寸下限由写 Box 前的 min/max 钳位
		// 兜底 (收缩可以压到 0, 有 minWidth 的 item 停在底线上)。
		deficit := -free
		var weighted float64
		for i := range slots {
			if slots[i].shrink > 0 {
				weighted += slots[i].shrink * float64(slots[i].main)
			}
		}
		if weighted > 0 {
			for i := range slots {
				if slots[i].shrink > 0 {
					slots[i].main -= int(float64(deficit) * slots[i].shrink * float64(slots[i].main) / weighted)
				}
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
		// 主轴尺寸回填 (两遍机制, 先定交叉再回填主轴):
		//   - wrap 文本块: 盒宽定下来才能知道折几行 (原有);
		//   - wrap 行容器: 高度 = "宽度约束下的折行结果" (§四 布局缺口第二批)。
		//     显式主轴尺寸优先 (同 blockHeight 的定宽守卫); 只在垂直父里回填 ——
		//     那是"父给确定宽 (交叉轴 stretch)、子回填高"唯一自洽的方向。
		if !horizontal {
			if c.wrapsText() {
				s.main = c.blockHeight(cross, s.main)
			} else if c.Tag == "row" && c.wrapEnabled() {
				if _, explicit := effectivePropNumOk(c, "height"); !explicit {
					if h := wrapStackCrossTotal(c, cross); h > 0 {
						s.main = h
					}
				}
			}
		}
		// min/max 终钳位: stretch/grow/shrink/百分比全部落定后做最终钳制
		// (v1 不回收钳位差 —— grow 超过 maxWidth 的部分不分给别人, 行为
		// 简单可预测)。钳位后的尺寸同时用于 pos 记账, 后续兄弟按实际占位走。
		boxW, boxH := s.main, cross // horizontal 容器: main=宽 cross=高
		if !horizontal {
			boxW, boxH = cross, s.main
		}
		boxW = clampDim(c, boxW, "minWidth", "maxWidth")
		boxH = clampDim(c, boxH, "minHeight", "maxHeight")
		if horizontal {
			c.Box = Rect{X: area.X + pos, Y: area.Y + s.margin + crossOffset, W: boxW, H: boxH}
			pos += boxW + s.margin
		} else {
			c.Box = Rect{X: area.X + s.margin + crossOffset, Y: area.Y + pos, W: boxW, H: boxH}
			pos += boxH + s.margin
		}
		layoutNode(c)
	}
	placeAbsoluteIn(n, area)
}

// ===== 网格布局 (§四 布局缺口, 2026-09-19) =====
//
// <grid columns={3} gap={8}>: 等宽列、声明序逐行填格的最小网格 (卡片栅格、
// 设置页分组)。词汇刻意收窄: 不做轨道语法 ("1fr 2fr") / colSpan / 区域命名 /
// 自动流密度 —— 需要不等宽列时用 row + 百分比/min-max 组合已有词汇。
//
// 两套口径 (与 CSS 的 min-content/definite 二重性同源): 固有尺寸 (auto 宽)
// 按**内容轨道**估 (每列取最大子宽); 拿到确定宽度后列是**等宽**的
// ((内容宽-gap*(cols-1))/cols)。网格几乎总在 column 里被 stretch 拿到确定宽,
// 内容口径只在 row 父容器里 auto 宽时出现。
//
// 格内语义 (与 layoutStack 的 stretch 哲学同源):
//   - 宽: 显式 (数字/百分比) 用自己的, 否则拉伸到列宽;
//   - 高: 显式用之; 无显式时容器/slot/零高节点拉伸到行高, 其余保持固有高度
//     顶部对齐 (行高 = 该行最高者);
//   - alignItems 在格内**两轴同时**生效: 缺省 stretch (auto 轴拉伸),
//     start/center/end 则不拉伸、按轴摆放;
//   - flexGrow/flexShrink/justifyContent 不参与 (没有主轴分配)。

// gridColumns 读取列数 (缺省 1, 钳 [1,32] —— 列数再多也是脚本拼错了)。
func (n *GuiNode) gridColumns() int {
	v, _ := n.PropNum("columns")
	c := int(v)
	if c < 1 {
		c = 1
	}
	if c > 32 {
		c = 32
	}
	return c
}

// gridContentSize 网格的固有尺寸: 列宽/行高按内容轨道取最大者 (百分比子节点
// 在固有口径下贡献 0, 与栈容器一致)。
func gridContentSize(n *GuiNode) (w, h int) {
	pt, pr, pb, pl := paddingOf(n)
	g := n.gapOf()
	cols := n.gridColumns()
	colW := make([]int, cols)
	var rowH []int
	idx := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		if col := idx % cols; cw > colW[col] {
			colW[col] = cw
		}
		row := idx / cols
		for len(rowH) <= row {
			rowH = append(rowH, 0)
		}
		if ch > rowH[row] {
			rowH[row] = ch
		}
		idx++
	}
	w = g * (cols - 1)
	for _, cw := range colW {
		w += cw
	}
	h = g * max(0, len(rowH)-1)
	for _, rh := range rowH {
		h += rh
	}
	return w + pl + pr, h + pt + pb
}

// layoutGrid 布局网格容器。
func layoutGrid(n *GuiNode) {
	area := inner(n)
	g := n.gapOf()
	cols := n.gridColumns()
	align := n.alignItems()

	type cell struct {
		child  *GuiNode
		w, h   int  // 解析后的显式/固有尺寸
		ew, eh bool // 该轴有显式尺寸 (数字或百分比)
	}
	cells := make([]cell, 0, len(n.Children))
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		e := cell{child: c, w: cw, h: ch}
		if p, ok := c.percentProp("width"); ok {
			e.w = int(float64(area.W) * p)
			e.ew = true
		} else if _, ok := effectivePropNumOk(c, "width"); ok {
			e.ew = true
		}
		if p, ok := c.percentProp("height"); ok {
			e.h = int(float64(area.H) * p)
			e.eh = true
		} else if _, ok := effectivePropNumOk(c, "height"); ok {
			e.eh = true
		}
		cells = append(cells, e)
	}
	if len(cells) == 0 {
		placeAbsoluteIn(n, area)
		return
	}
	colW := (area.W - g*(cols-1)) / cols
	if colW < 0 {
		colW = 0
	}
	rows := (len(cells) + cols - 1) / cols
	rowH := make([]int, rows)
	for i, e := range cells {
		if e.h > rowH[i/cols] {
			rowH[i/cols] = e.h
		}
	}
	// 行起点 = 前面各行高 (含 gap) 的累计 —— 不是当前行的行高
	rowY := make([]int, rows)
	for r := 1; r < rows; r++ {
		rowY[r] = rowY[r-1] + rowH[r-1] + g
	}
	for i, e := range cells {
		col, row := i%cols, i/cols
		x := area.X + col*(colW+g)
		y := area.Y + rowY[row]
		c := e.child
		w, h := e.w, e.h
		if align == "stretch" {
			if !e.ew {
				w = colW
			}
			if !e.eh && (h == 0 || c.isContainer() || c.isPassthrough()) {
				h = rowH[row]
			}
		}
		w = clampDim(c, w, "minWidth", "maxWidth")
		h = clampDim(c, h, "minHeight", "maxHeight")
		ox, oy := 0, 0
		switch align {
		case "center":
			ox, oy = (colW-w)/2, (rowH[row]-h)/2
		case "end":
			ox, oy = colW-w, rowH[row]-h
		}
		if ox < 0 {
			ox = 0
		}
		if oy < 0 {
			oy = 0
		}
		c.Box = Rect{X: x + ox, Y: y + oy, W: w, H: h}
		layoutNode(c)
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

// ===== 容器级 wrap (§四 布局缺口第二批, 2026-09-19) =====
//
// <row wrap gap={8}>: 行内横向排, 放不下就折到下一行 (标签流 / 工栏换行)。
// 语义:
//   - 折行 = 贪心分line (声明序, 当前行放不下即折; 单个超宽项独占一行);
//   - gap 同时是行内间距与行间距 (单 gap 词汇, 不拆 row-gap/column-gap);
//   - grow/shrink/justifyContent 只在**行内**生效 (不跨行, 与 CSS 一致);
//   - alignItems 在行内对**行高**生效 (stretch 拉到行高, 不是容器总高);
//   - 容器自身高度: 未显式给时由 layoutStack 的回填钩子按折行结果算
//     (wrapStackCrossTotal); align-content 不支持 (行从上往下堆)。
// 刻意不支持: column 方向的 wrap (需要"父给确定高"的罕见场景, v1 不做)。

// wrapEnabled 报告容器是否开启主轴折行。
func (n *GuiNode) wrapEnabled() bool {
	v, _ := n.PropBool("wrap")
	return v
}

// wrapSlot 是折行布局的一个子节点条目 (与 layoutStack 的局部 slot 同构)。
type wrapSlot struct {
	child        *GuiNode
	main, cross  int // 不含 margin 的尺寸
	margin       int
	grow, shrink float64
}

// wrapCollect 收集子节点并解析尺寸 (与 layoutStack 同一套规则: 固有 → 百分比
// → 主轴 min/max 收集期钳, 交叉轴写盒时钳)。
func wrapCollect(n *GuiNode, areaW, areaH int) []wrapSlot {
	var slots []wrapSlot
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		if p, ok := c.percentProp("width"); ok {
			cw = int(float64(areaW) * p)
		}
		if p, ok := c.percentProp("height"); ok {
			ch = int(float64(areaH) * p)
		}
		cw = clampDim(c, cw, "minWidth", "maxWidth")
		m, _ := c.PropNum("margin")
		mg := int(m)
		if mg < 0 {
			mg = 0
		}
		grow, _ := c.PropNum("flexGrow")
		shrink, _ := c.PropNum("flexShrink")
		slots = append(slots, wrapSlot{
			child: c, main: cw, cross: ch, margin: mg, grow: grow, shrink: shrink,
		})
	}
	return slots
}

// wrapLines 把条目贪心分line: 当前行放不下就折行。单个条目超过整行宽度时
// 独占一行 (溢出部分由行内 shrink / min-max 各自兜底)。
func wrapLines(slots []wrapSlot, g, areaMain int) [][]wrapSlot {
	var lines [][]wrapSlot
	var cur []wrapSlot
	curMain := 0
	for _, s := range slots {
		need := s.main + 2*s.margin
		if len(cur) > 0 && curMain+g+need > areaMain {
			lines = append(lines, cur)
			cur = nil
			curMain = 0
		}
		if len(cur) > 0 {
			curMain += g
		}
		cur = append(cur, s)
		curMain += need
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	return lines
}

// wrapStackCrossTotal 计算折行容器在给定宽度下的内容总高 (含 padding) ——
// layoutStack 的主轴回填钩子用它 (wrap 文本 blockHeight 的同款两遍)。
// 宽度不可用 (<=0) 时返回 0, 调用方保留原主轴尺寸。
func wrapStackCrossTotal(n *GuiNode, mainAvailable int) int {
	if mainAvailable <= 0 {
		return 0
	}
	pt, pr, pb, pl := paddingOf(n)
	g := n.gapOf()
	// 测量遍: areaH 传 0 (真实高度正是要求的量) —— 百分比高的子节点按
	// 固有值 (通常 0) 计, 放置遍才按容器盒解析, 已知偏差记录在案。
	lines := wrapLines(wrapCollect(n, mainAvailable-pl-pr, 0), g, mainAvailable-pl-pr)
	total := 0
	for li, line := range lines {
		if li > 0 {
			total += g
		}
		lineCross := 0
		for _, s := range line {
			if s.cross+2*s.margin > lineCross {
				lineCross = s.cross + 2*s.margin
			}
		}
		total += lineCross
	}
	return total + pt + pb
}

// layoutWrapStack 布局折行容器 (仅 row 派发到这里)。
func layoutWrapStack(n *GuiNode, horizontal bool) {
	area := inner(n)
	g := n.gapOf()
	align := n.alignItems()

	slots := wrapCollect(n, area.W, area.H)
	if len(slots) == 0 {
		placeAbsoluteIn(n, area)
		return
	}
	lines := wrapLines(slots, g, area.W)

	crossPos := 0
	for li, line := range lines {
		if li > 0 {
			crossPos += g
		}
		// 行内主轴分配: 与 layoutStack 同款 (grow / shrink / justify), 不跨行
		totalMain := 0
		var sumGrow, sumShrink float64
		lineCross := 0
		for _, s := range line {
			totalMain += s.main + 2*s.margin
			sumGrow += s.grow
			sumShrink += s.shrink
			if s.cross+2*s.margin > lineCross {
				lineCross = s.cross + 2*s.margin
			}
		}
		free := area.W - totalMain - g*(len(line)-1)
		lead, betweenGap := 0, 0
		if free > 0 && sumGrow > 0 {
			for i := range line {
				if line[i].grow > 0 {
					line[i].main += int(float64(free) * line[i].grow / sumGrow)
				}
			}
		} else if free > 0 {
			switch n.justifyContent() {
			case "center":
				lead = free / 2
			case "end":
				lead = free
			case "between":
				if len(line) > 1 {
					betweenGap = free / (len(line) - 1)
				}
			}
		} else if free < 0 && sumShrink > 0 {
			deficit := -free
			var weighted float64
			for i := range line {
				if line[i].shrink > 0 {
					weighted += line[i].shrink * float64(line[i].main)
				}
			}
			if weighted > 0 {
				for i := range line {
					if line[i].shrink > 0 {
						line[i].main -= int(float64(deficit) * line[i].shrink * float64(line[i].main) / weighted)
					}
				}
			}
		}

		pos := lead
		for i, s := range line {
			if i > 0 {
				pos += g + betweenGap
			}
			pos += s.margin
			// 行内 stretch: 拉到行高 (折行容器的交叉轴是多行总高, 拉到容器
			// 高会把第一行的子节点拉出屏)。
			cross := s.cross
			if align == "stretch" && !s.child.hasExplicitCross(horizontal) &&
				(s.cross == 0 || s.child.stretchesCross()) {
				cross = lineCross - 2*s.margin
			}
			crossOffset := 0
			if s.cross > 0 || align != "stretch" {
				switch align {
				case "center":
					crossOffset = (lineCross - cross - 2*s.margin) / 2
				case "end":
					crossOffset = lineCross - cross - 2*s.margin
				}
			}
			if cross < 0 {
				cross = 0
			}
			if crossOffset < 0 {
				crossOffset = 0
			}
			c := s.child
			if c.wrapsText() {
				s.main = c.blockHeight(cross, s.main)
			}
			boxW, boxH := s.main, cross
			boxW = clampDim(c, boxW, "minWidth", "maxWidth")
			boxH = clampDim(c, boxH, "minHeight", "maxHeight")
			c.Box = Rect{
				X: area.X + pos,
				Y: area.Y + crossPos + s.margin + crossOffset,
				W: boxW, H: boxH,
			}
			layoutNode(c)
			pos += boxW + s.margin
		}
		crossPos += lineCross
	}
	placeAbsoluteIn(n, area)
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
	if n.isPassthrough() {
		if c := n.slotChild(); c != nil {
			return c.stretchesCross()
		}
		return true
	}
	if n.Tag == "select-option" {
		return true
	}
	// 菜单栏必须拿到可用宽度: 它是"窗口顶部的一条", 拿不到宽度就只按标题
	// 总宽撑开, 右侧会露出一截窗口底色。
	// 菜单标题**不吃** stretch: 被拉满的标题会摊掉整行, 菜单栏也就不成立了。
	if n.Tag == "menubar" {
		return true
	}
	// 开了 wrap 的文本块必须拿到可用宽度, 否则它没有换行依据 —— 在 column
	// 里会按"未折行的整行宽"撑开并溢出容器。
	if n.wrapsText() {
		return true
	}
	if n.Tag == "grid" {
		return true // 网格几乎总是要确定宽度 (等宽列的基准)
	}
	return n.isContainer()
}

// hasExplicitCross 报告节点是否显式指定了交叉轴尺寸 (父容器是 row 时
// 交叉轴为高, 否则为宽): 显式值即"定死", 不参与 stretch 拉伸。
// 单子 slot 委托给子节点, 保持"透明"。
func (n *GuiNode) hasExplicitCross(parentHorizontal bool) bool {
	if n.isPassthrough() {
		if c := n.slotChild(); c != nil {
			return c.hasExplicitCross(parentHorizontal)
		}
	}
	name := "width"
	if parentHorizontal {
		name = "height"
	}
	// 菜单栏的高度是固定的 (26px), 对"高度敏感"的父容器而言它相当于
	// 显式指定 —— 不加这条, 它在 column 里会被 stretch 到全高。
	if n.Tag == "menubar" && parentHorizontal {
		return true
	}
	// 百分比尺寸算显式: "50%" 是脚本对这一轴的明确表态, 不再吃 stretch
	if _, ok := n.percentProp(name); ok {
		return true
	}
	_, ok := effectivePropNumOk(n, name)
	return ok
}

// justifyContent 读取容器主轴分布 (默认 start)。
func (n *GuiNode) justifyContent() string {
	if v, ok := n.PropStr("justifyContent"); ok {
		return v
	}
	return "start"
}
