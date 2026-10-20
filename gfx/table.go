package gfx

import (
	"image"
	"image/color"
	"strconv"

	"github.com/14752222/Gox/object"
)

// table 数据表格 (S4 组件库二期)。
//
// 用法 (声明式数据驱动 —— 不要求脚本拼 row):
//
//	<table
//	  columns={["名称", "大小", "类型"]}
//	  rows={[["a.txt", "12KB", "文本"], ["b.png", "48KB", "图片"]]}
//	  zebra
//	/>
//
// 或列配置 + 对象行 (键名取值, 列宽/对齐可按列给):
//
//	<table
//	  columns={[{key:"name",label:"名称",width:120},{key:"size",label:"大小",align:"right"}]}
//	  rows={[{name:"a.txt", size:"12KB"}, {name:"b.png", size:"48KB"}]}
//	/>
//
// 结构约定:
//   - 与 select 同一套"数据进、节点树出"的思路: columns/rows 是数据, 展开成
//     table-header / table-row / table-cell 三个内部标签的节点树 (脚本写不到,
//     但仍登记进 knownTags 以免内部构造被当成未知标签)。
//   - 行高固定 tableRowH = 32 (Prism S5): 比字段行高 36 矮一档 —— 表格是
//     "密集阅读"的场景, 与字段同高会让一屏能看的行数明显变少; 而 28 又太
//     挤 (中文一行 17px 字面 + 上下各 7px 呼吸)。表头底色 + 底边分隔线,
//     数据行斑马纹/悬停/选中底色由 tabular 开关与运行时状态控制。
//   - 列宽: 显式 width 优先; 缺省时按**表头与该列所有单元格**里最宽的文本
//     测量取最大 (见 tableColumnWidths), 各列按内容比例分剩余空间。只按表头
//     量是 S4 的 bug: 列宽被表头定死, 单元格被 DrawText 的 maxWidth 硬截断,
//     截图里 "button" 显示成 "butto"。text-align 用 align (left/right/
//     center), 只影响单元格内文字对齐。
//   - `borderless` 关掉单元格竖线与横线 (表头底边保留, 否则表头与数据糊在
//     一起); `headerBackground` 覆盖表头底色。
//
// 为什么不做虚拟滚动: 这是 S4 的可视化表格组件, 行数由数据决定。万行级
// 虚拟滚动是同一迭代里的独立子任务 (ryngvk), 到时候把 table 的行渲染接到
// 窗口化区间即可 —— 本文件的行高常量与"行按 y 等距排布"约定就是为它留的。

const (
	// tableRowH 是行高 (**设计像素**, Prism S5: 28 → 32; 取值处经 Space()
	// 换算 —— S8 之前它是裸物理像素, 于是 2x 屏上行还是 32px 而行里的字
	// 已经被 dpToPx 放大到 32px, 上下留白归零、中文被裁)。
	// 比字段行高 36 矮一档: 表格是
	// 密集阅读场景, 见文件头。**刻意小于 TouchMin (44)** —— 桌面端的鼠标
	// 行不必满足触控下限; 触控场景下表格本身应换成卡片式布局 (不是把行
	// 撑到 44), 那不在本组件的范围里。
	tableRowH = 32
	// tableBorderW 是网格线宽 (发丝线, 不随 density 放大)
	tableBorderW = 1
	// tableRowBarW 是数据行左侧的选中指示条宽度 (Prism S5, 设计像素)。
	tableRowBarW = 3
)

// tableCellPadX 是单元格水平内边距 (物理像素)。
//
// 写成函数而不是常量: Space() 要按 displayScale 换算, 2x/3x 屏上留白
// 跟着放大, 否则文字会贴着格线。取值 Space5 = 12 (S3 给字段定的同一档)。
func tableCellPadX() int { return Space(Space5) }

// tableColumn 是一列的解析结果 (键名 / 表头文字 / 宽度 / 对齐)。
type tableColumn struct {
	key   string // 取值键 (字符串列时是下标字符串)
	label string // 表头显示文字
	width int    // 显式列宽; 0 = 按内容算
	align string // left / right / center, 缺省 left
	index int    // 在 cols 切片里的位置 (取单元格文本用, 与 key 无关)
}

// tableColumns 读取 columns prop: 字符串数组或 {key,label,width,align} 数组。
// 非数组/非法元素直接跳过 (与 selectOptions 同: 数据形状不对不 panic)。
func (n *GuiNode) tableColumns() []tableColumn {
	v, ok := n.Props["columns"]
	if !ok {
		return nil
	}
	arr, ok := v.(*object.Array)
	if !ok {
		return nil
	}
	out := make([]tableColumn, 0, len(arr.Elements))
	for i, e := range arr.Elements {
		switch x := e.(type) {
		case *object.String:
			// 字符串列: 表头即文字, 取值键用下标 (行是数组时按位置取)
			out = append(out, tableColumn{key: strconv.Itoa(i), label: string(x.Value), index: len(out)})
		case *object.Object:
			col := tableColumn{key: strconv.Itoa(i)}
			if s, ok := objStringField(x, "key"); ok {
				col.key = s
			}
			if s, ok := objStringField(x, "label"); ok {
				col.label = s
			} else {
				col.label = col.key
			}
			if f, ok := objNumField(x, "width"); ok && f > 0 {
				col.width = int(f)
			}
			if s, ok := objStringField(x, "align"); ok {
				col.align = s
			}
			col.index = len(out)
			out = append(out, col)
		}
	}
	return out
}

// tableRows 读取 rows prop: 数组的数组 (按列下标取) 或 对象数组 (按 key 取)。
// 两种形状都收 —— 数组行直接用, 对象行包一层只含该对象的 Array (于是下游
// 统一的 "遍历元素取单元格" 逻辑不用分叉)。
func (n *GuiNode) tableRows() []*object.Array {
	v, ok := n.Props["rows"]
	if !ok {
		return nil
	}
	arr, ok := v.(*object.Array)
	if !ok {
		return nil
	}
	out := make([]*object.Array, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		switch x := e.(type) {
		case *object.Array:
			out = append(out, x)
		case *object.Object:
			// 对象行: 包成单元素数组, 让 rowAsObject 能取到它
			out = append(out, &object.Array{Elements: []object.Value{x}})
		}
	}
	return out
}

// tableCells 把一行数据按列定义抽成每列的显示文本。行是数组时按 key 解析出的
// 下标取值; 行是对象时按列 key 取值。取不到的单元格显示空串 (不是 "undefined",
// 表格里看到 undefined 只会让人以为是引擎 bug)。
func (n *GuiNode) tableCells(row *object.Array, cols []tableColumn) []string {
	out := make([]string, len(cols))
	for i, col := range cols {
		idx, err := strconv.Atoi(col.key)
		if err == nil {
			// 下标列: 行是数组时按位置取
			if idx >= 0 && idx < len(row.Elements) {
				out[i] = valueToCellText(row.Elements[idx])
			}
			continue
		}
		// 键名列: 行是对象时按字段取
		if obj, ok := rowAsObject(row); ok {
			if pv, ok := obj.GetProperty(col.key); ok {
				out[i] = valueToCellText(pv)
			}
		}
	}
	return out
}

// rowAsObject 取一行背后的对象: 对象行在 tableRows 里被包成单元素数组,
// 所以这里只在"唯一元素是 Object"时返回真。数组行 (元素是字符串/数字)
// 不会命中 —— 即使它是单元素 (["a"] 这种单列数组行)。
func rowAsObject(row *object.Array) (*object.Object, bool) {
	if len(row.Elements) != 1 {
		return nil, false
	}
	o, ok := row.Elements[0].(*object.Object)
	return o, ok
}

// valueToCellText 把单元格值转成显示文本。字符串直通; 其余走 object.ToString
// (与 select 的 valueText 同语义: 数字/布尔都显示得出来)。空值返回空串 ——
// 表格里出现 "undefined" 只会让人以为是引擎 bug。
func valueToCellText(v object.Value) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(*object.String); ok {
		return s.Value
	}
	switch v.(type) {
	case *object.Object:
		return "[对象]"
	case *object.Array:
		return "[数组]"
	}
	return object.ToString(v)
}

// objStringField / objNumField 是 object.Object 取字段的便捷封装 (字段不存在
// 或类型不符时 ok=false)。与 select.go 一致走 GetProperty 而不是直接读
// Properties 表 —— 后者拿不到原型链上的字段。
func objStringField(o *object.Object, key string) (string, bool) {
	v, ok := o.GetProperty(key)
	if !ok {
		return "", false
	}
	s, ok := v.(*object.String)
	if !ok {
		return "", false
	}
	return s.Value, true
}

func objNumField(o *object.Object, key string) (float64, bool) {
	v, ok := o.GetProperty(key)
	if !ok {
		return 0, false
	}
	n, ok := v.(*object.Number)
	if !ok {
		return 0, false
	}
	return n.Value, true
}

// ===== 布局 =====

// layoutTable 布局表格: 先定列宽 (显式 width 优先, 其余按内容分配剩余空间),
// 再逐行横排单元格。行框从左到右等距, 每格 = 列宽 × 行高。
func layoutTable(n *GuiNode) {
	ensureTableBuilt(n)
	area := inner(n)
	cols := n.tableColumns()
	if len(cols) == 0 {
		placeAbsoluteIn(n, area)
		return
	}
	widths := tableColumnWidths(n, cols, area.W)

	// Prism S8: 行的高宽都经 Space() 换算 (见上方常量的说明)。
	rowH := Space(tableRowH)
	y := area.Y
	// 表头行 (总在流内第一个)
	if h := tableHeaderNode(n); h != nil {
		h.Box = Rect{X: area.X, Y: y, W: area.W, H: rowH}
		layoutTableRowCells(h, widths, area.X)
		y += rowH
	}
	for _, r := range tableRowNodes(n) {
		r.Box = Rect{X: area.X, Y: y, W: area.W, H: rowH}
		layoutTableRowCells(r, widths, area.X)
		y += rowH
	}
	placeAbsoluteIn(n, area)
}

// tableIntrinsic 表格的固有尺寸: 宽 = 各列需要宽度之和 (给 stretch 一个下限,
// 脚本可显式 width 覆盖); 高 = (1 + 行数) × 行高。行数含表头 —— 表头是表格
// 的一部分, 不算进去会让"只给 rows 不给高度"的 table 高度少一行。
func tableIntrinsic(n *GuiNode) (int, int) {
	ensureTableBuilt(n)
	cols := n.tableColumns()
	if len(cols) == 0 {
		return 0, 0
	}
	pad := tableCellPadX()
	textNeeds := tableColumnNeeds(n, cols)
	w := 0
	for i, col := range cols {
		if col.width > 0 {
			w += col.width
			continue
		}
		w += textNeeds[i] + 2*pad
	}
	rows := len(tableRowNodes(n)) + 1 // +1 是表头 (有列定义就必有表头)
	return w, rows * Space(tableRowH)
}

// tableColumnWidths 分配列宽: 显式 width 先占, 剩下的宽度按各列"内容需要
// 的宽度"比例分。内容需求 = 表头与**该列所有单元格**文本里最宽的那个 +
// 两侧内边距。总宽不够时按比例压缩 (不溢出容器), 单元格再按 DrawText 的
// maxWidth 硬截断兜底。
//
// 为什么要把单元格也量进来: S4 只量表头, 列宽于是被表头文字定死; 表头是
// 中文两字、数据却是 "button" 这类长词时, 单元格被截成 "butto" (截图
// testdata/shots/table.js 里的既定 bug)。列宽是所有行共用的, 它的需求
// 必须由**最宽的那一格**决定, 而不是表头单独说了算。
func tableColumnWidths(n *GuiNode, cols []tableColumn, total int) []int {
	pad := tableCellPadX()
	need := make([]int, len(cols))
	fixed := make([]int, len(cols))
	fixedSum := 0
	for i, col := range cols {
		if col.width > 0 {
			fixed[i] = col.width
			fixedSum += col.width
			continue
		}
		textNeeds := tableColumnNeeds(n, cols)
		need[i] = textNeeds[i] + 2*pad
	}
	rest := total - fixedSum
	if rest < 0 {
		rest = 0
	}
	needSum := 0
	for _, w := range need {
		needSum += w
	}
	out := make([]int, len(cols))
	for i := range cols {
		if fixed[i] > 0 {
			out[i] = fixed[i]
			continue
		}
		if needSum > 0 {
			out[i] = rest * need[i] / needSum
		} else {
			// 全是空列: 均分
			out[i] = rest / len(cols)
		}
	}
	return out
}

// tableColumnNeeds 返回各列"文字需要多宽": 表头与该列所有数据格文本里最宽
// 的那个 (不含内边距)。数据来自 rows prop 而不是已构造的单元格节点 ——
// 布局时节点树可能还没建 (intrinsicSize 早于 layoutNode 运行), 而 rows
// 是数据源头, 两边读到的一定是同一份。
//
// 一次算出**全部列**: 调用方 (intrinsicSize 与列宽分配) 各来一遍, 若按列
// 单独算, 每列都要把所有行的单元格文本重新抽一遍 —— 列数平方级的重复劳动。
//
// 列下标取 tableColumn.index (构造期写死): 列定义里可能有非法元素被跳过,
// 用切片下标会和数据行的位置对不上。
func tableColumnNeeds(n *GuiNode, cols []tableColumn) []int {
	size := n.FontSize()
	out := make([]int, len(cols))
	for i, col := range cols {
		w, _ := MeasureText(col.label, size)
		out[i] = w
	}
	for _, row := range n.tableRows() {
		texts := n.tableCells(row, cols)
		for i, col := range cols {
			if col.index < 0 || col.index >= len(texts) {
				continue
			}
			if cw, _ := MeasureText(texts[col.index], size); cw > out[i] {
				out[i] = cw
			}
		}
	}
	return out
}

// layoutTableRowCells 把一行的单元格从左到右摆开, 每个占该列宽度。
func layoutTableRowCells(row *GuiNode, widths []int, x0 int) {
	x := x0
	i := 0
	for _, c := range row.Children {
		if !c.isFlowChild() {
			continue
		}
		w := 0
		if i < len(widths) {
			w = widths[i]
		}
		c.Box = Rect{X: x, Y: row.Box.Y, W: w, H: row.Box.H}
		layoutNode(c)
		x += w
		i++
	}
	placeAbsoluteIn(row, row.Box)
}

// ===== 绘制 =====

// paintTableHeader / paintTableRow / paintTableCell 画表格三件套。
//
// Prism S5 的三层底色, 优先级从高到低:
//  1. 选中行 → colorStateSelected, 再叠一条左侧 tableRowBarW 宽的强调色指示条
//     (软光栅没有透明度, "选中"只能靠整行铺色 + 一个强信号条来表达);
//  2. 悬停行 → colorStateRowHover (只给数据行; 表头是标题, 高亮它没有意义);
//  3. 斑马纹 → colorSurfaceSunken (面层级差一档, 比裸 #fafafa 更能跟主题走)。
//
// 网格线/斑马纹/行底线一律用 colorBorderDecorative: 它是"装饰性"边界,
// 与控件边线 (colorBorderControl) 是两档对比度, 不能混用。
func paintTableHeader(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	face := n.fieldFace(colorSurfaceInset)
	FillRect(img, b, tint(face, disabled))
	// 表头底边: 表格的视觉锚线, borderless 也保留
	FillRect(img, Rect{X: b.X, Y: b.Y + b.H - tableBorderW, W: b.W, H: tableBorderW},
		tableEdgeColor(disabled))
}

// tableEdgeColor 是表格网格线的颜色 (装饰性边界 token, 随主题走)。
func tableEdgeColor(disabled bool) color.RGBA { return tint(colorBorderDecorative, disabled) }

func paintTableRow(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	selected := n.tableRowSelected()
	switch {
	case selected:
		FillRect(img, b, tint(colorStateSelected, disabled))
		// 左指示条: 整行高的竖条, 与选中底同层 (先铺底再压条)
		// Prism S8: 指示条也是参与排版的尺寸, 跟着 Space() 走。
		FillRect(img, Rect{X: b.X, Y: b.Y, W: Space(tableRowBarW), H: b.H},
			tint(colorAccent, disabled))
	case n.hovered && !n.tblHeader:
		FillRect(img, b, tint(colorStateRowHover, disabled))
	case n.tableRowZebra():
		// 斑马纹: 由 Go 侧写入的 _zebra prop 决定 (奇数数据行着色)。
		// 排在悬停之后 = 悬停时斑马纹让位, 不叠出第三种颜色。
		FillRect(img, b, tint(colorSurfaceSunken, disabled))
	}
	// 行底线 (borderless 时跳过)
	if !n.tableBorderless() {
		FillRect(img, Rect{X: b.X, Y: b.Y + b.H - tableBorderW, W: b.W, H: tableBorderW},
			tableEdgeColor(disabled))
	}
}

// paintTableCell 画单元格: 只画竖分隔线 + 文字 (底色由行负责)。文字来自
// cellText 节点字段 (数据而非元素树, 与 menu-item 画 menuLabel 同理)。
//
// 文字左右都夹在单元格内: 旧实现给 DrawText 的 maxWidth 是 b.W-pad, 起点
// 却已经在 b.X+pad, 右对齐时同一格文字会超出右边界 pad 像素。这里统一
// 算内容区 (两侧各让 pad) 与可用宽度。
func paintTableCell(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	if !n.tableBorderless() {
		FillRect(img, Rect{X: b.X + b.W - tableBorderW, Y: b.Y, W: tableBorderW, H: b.H},
			tableEdgeColor(disabled))
	}
	text := n.cellText
	if text == "" {
		return
	}
	pad := tableCellPadX()
	contentW := b.W - 2*pad
	if contentW <= 0 {
		// 窄到放不下留白: 退回"整格可用", 让文字至少能露出来
		pad, contentW = 0, b.W
	}
	size := n.FontSize()
	tw, th := MeasureText(text, size)
	// 对齐: 只在单元格内容区内移动起点, 不改变单元格本身的分栏地位。
	// 表头走 secondary 档、数据行走正文色 —— 表头是"标题"而不是数据。
	ink := n.textColor()
	if n.tblHeader {
		// 表头是"标题"而不是数据: 降一档到 secondary。脚本显式给 color
		// 仍以脚本为准 (textColor 已沿祖先链读过)。
		if _, ok := n.PropStr("color"); !ok && !hasAncestorColor(n) {
			ink = colorTextSecondary
		}
	}
	x := b.X + pad
	switch n.cellAlign {
	case "right":
		x = b.X + pad + contentW - tw
	case "center":
		x = b.X + pad + (contentW-tw)/2
	}
	DrawText(img, img.Bounds(), text, x, b.Y+(b.H-th)/2, size,
		tint(ink, disabled), contentW)
}

// ===== props / 运行时读取 =====

// tableBorderless 读 borderless 开关。属性写在 <table> 上, 而行/单元格
// 是 Go 侧构造的内部节点 (owner 指向 table), 所以沿 owner 链向上找 —
// 否则脚本写 <table borderless> 会被静默吞掉, 网格线照常画。
func (n *GuiNode) tableBorderless() bool {
	for p := n; p != nil; p = p.owner {
		if v, ok := p.PropBool("borderless"); ok {
			return v
		}
	}
	return false
}

// tableZebra 读 zebra 开关 (同样在 <table> 上, 沿 owner 链找)。
func (n *GuiNode) tableZebra() bool {
	for p := n; p != nil; p = p.owner {
		if v, ok := p.PropBool("zebra"); ok {
			return v
		}
	}
	return false
}

// tableHeaderNode / tableRowNodes 取内部构造的表头行与数据行。
func tableHeaderNode(n *GuiNode) *GuiNode {
	for _, c := range n.Children {
		if c.Tag == "table-header" {
			return c
		}
	}
	return nil
}

func tableRowNodes(n *GuiNode) []*GuiNode {
	out := make([]*GuiNode, 0, len(n.Children))
	for _, c := range n.Children {
		if c.Tag == "table-row" {
			out = append(out, c)
		}
	}
	return out
}

// ===== 节点树构造 =====

// buildTable 把 columns/rows 数据展开成 table-header / table-row / table-cell
// 节点树。在布局前调用 (惰性: 只有节点还没有内部行时才建, 见 ensureTableBuilt)。
//
// 为什么由 Go 侧构造而不是要求脚本写 children: 与 select 的 options 同理 ——
// columns/rows 是数据, 展开成声明式 children 没有额外表达力, 却让每个使用点
// 都要写一遍嵌套 map。数据变化时整棵内部行重建 (表格是数据视图, 不做 keyed
// 复用; 万行级虚拟滚动的复用留给 ryngvk)。
func buildTable(n *GuiNode) {
	cols := n.tableColumns()
	rows := n.tableRows()
	// 先摘掉旧的内部行 (保留非表格子节点, 允许脚本往 table 里塞自定义内容)
	kept := n.Children[:0]
	for _, c := range n.Children {
		if c.Tag == "table-header" || c.Tag == "table-row" {
			disposeNode(c)
			continue
		}
		kept = append(kept, c)
	}
	n.Children = kept
	if len(cols) == 0 {
		return
	}

	// 表头行
	hdr := &GuiNode{Tag: "table-header", Props: map[string]object.Value{}}
	hdr.owner = n
	hdr.tblHeader = true
	alignCells(hdr, cols, n)
	n.Children = append(n.Children, hdr)

	// 数据行
	for ri, row := range rows {
		r := &GuiNode{Tag: "table-row", Props: map[string]object.Value{}}
		r.owner = n
		// 斑马纹: 奇数行 (1,3,5…) 着色, 但仅当 <table zebra> 显式打开。
		// 此前无条件写 _zebra 导致 zebra prop 被静默吞掉 (关不掉也无需开)。
		if n.tableZebra() && ri%2 == 1 {
			r.Props["_zebra"] = &object.Boolean{Value: true}
		}
		texts := n.tableCells(row, cols)
		// 受控选中行: <table rowSelected={下标}> 在构造期拷到对应行 (行
		// 是 Go 侧构造的, 脚本没有机会直接写它的 prop)。
		if sel, ok := n.PropNum("rowSelected"); ok && int(sel) == ri {
			r.Props["selected"] = &object.Boolean{Value: true}
		}
		alignCellsWithText(r, cols, texts, n)
		attachRowClick(r, n, ri, row)
		n.Children = append(n.Children, r)
	}
}

// alignCells 建表头单元格 (文字来自列的 label)。
func alignCells(row *GuiNode, cols []tableColumn, owner *GuiNode) {
	texts := make([]string, len(cols))
	for i, c := range cols {
		texts[i] = c.label
	}
	alignCellsWithText(row, cols, texts, owner)
}

// alignCellsWithText 按列定义给一行建单元格: 每格缓存 cellText/cellAlign,
// 对齐信息来自列定义 (同一列的所有格对齐一致)。
func alignCellsWithText(row *GuiNode, cols []tableColumn, texts []string, owner *GuiNode) {
	for i, col := range cols {
		cell := &GuiNode{Tag: "table-cell", Props: map[string]object.Value{}}
		cell.owner = owner
		cell.cellText = texts[i]
		cell.cellAlign = col.align
		if cell.cellAlign == "" {
			cell.cellAlign = "left"
		}
		row.Children = append(row.Children, cell)
	}
}

// attachRowClick 给数据行挂 onRowClick 桥接: 脚本写 <table onRowClick={fn}>
// 想拿到"点了哪一行", 行本身是 Go 侧构造的, 所以这里把父节点的处理器包一层
// 传行下标。没有 onRowClick 就不挂 —— 不挂处理器时表格行不可点 (只有悬停
// 高亮), 保持"表格默认不是交互控件"的克制。
//
// 回调走 object.NewBuiltin 挂在行节点上 (与 select-option 完全同一条链路),
// 于是"命中 → 回调"复用现有事件系统, 不引入并行机制。参数是 {index, row},
// 行数据原样透出 (脚本自己按 key 取字段)。
func attachRowClick(row *GuiNode, owner *GuiNode, index int, data *object.Array) {
	if owner.PropHandler("onRowClick") == nil {
		return
	}
	idx, dataRow := index, data
	row.Props["onClick"] = object.NewBuiltin("tableRow", func(args ...object.Value) object.Value {
		app := appOfNode(owner)
		if app == nil {
			return object.UndefinedSingleton
		}
		arg := object.NewObject()
		arg.SetProperty("index", object.NewNumber(float64(idx)))
		arg.SetProperty("row", dataRow)
		app.callHandlerValue(owner.PropHandler("onRowClick"), "onRowClick", arg)
		return object.UndefinedSingleton
	})
}

// ensureTableBuilt 惰性构造: 布局每帧都会跑, 若每次都重建内部行会丢掉
// "当前悬停在哪一行"这类运行时状态, 也会让每帧的节点数膨胀。只在还没有
// 内部行 (首次布局) 时构建。
//
// 判据是"还没建过"而不是"有数据": 空 rows 的表格仍要建表头 (表头是列定义
// 的呈现, 与有无数据无关 —— 空表格画一个只有表头的表, 是用户想看到的
// "暂无数据" 形态)。所以这里用一个显式标记, 不用节点数反推。
func ensureTableBuilt(n *GuiNode) {
	if n.tableBuilt {
		return
	}
	n.tableBuilt = true
	buildTable(n)
}

// tableRowSelected 报告这一数据行是否是选中行。
//
// 选中态走受控 prop `selected` (与 list-item 同款): "选中哪一行"是业务
// 状态。内部行不对脚本直接开放, 所以脚本写在宿主上
// (<table rowSelected={i}>), 构造期拷到对应行 —— 每帧重建时跟着数据走,
// 不会"脚本改了选中行、画面还停在旧行"。表头永远不是选中行。
func (n *GuiNode) tableRowSelected() bool {
	if n.tblHeader {
		return false
	}
	v, ok := n.PropBool("selected")
	return ok && v
}

// tableRowZebra 报告这一数据行是否该铺斑马纹底色。
//
// 斑马纹由构造期写的 _zebra prop 决定 (奇数数据行, 仅当 <table zebra>);
// 读的是本节点的 prop 而不是 owner 链 —— 否则每一行都会读到 <table> 上的
// 同名开关, "奇数行着色"退化成"整表着色"。
func (n *GuiNode) tableRowZebra() bool {
	if n.tblHeader {
		return false
	}
	v, ok := n.PropBool("_zebra")
	return ok && v
}

// hasAncestorColor 报告节点是否有祖先显式写了 color (CSS 式继承)。
func hasAncestorColor(n *GuiNode) bool {
	for p := n.Parent; p != nil; p = p.Parent {
		if _, ok := p.PropStr("color"); ok {
			return true
		}
	}
	return false
}
