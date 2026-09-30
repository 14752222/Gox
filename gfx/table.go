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
//   - 行高固定 28 (与 select/input 同一套字段常量), 保证跨组件对齐;
//     表头底色略深 + 底边分隔线, 数据行斑马纹由 `zebra` 开关控制。
//   - 列宽: 显式 width 优先; 缺省时按该列文本测量宽度取最大, 各列等分剩余
//     空间 (把 table 的可用宽度按内容比例分配)。text-align 用 align (left/
//     right/center), 只影响单元格内文字对齐。
//   - `borderless` 关掉单元格竖线与横线 (表头底边保留, 否则表头与数据糊在
//     一起); `headerBackground` 覆盖表头底色。
//
// 为什么不做虚拟滚动: 这是 S4 的可视化表格组件, 行数由数据决定。万行级
// 虚拟滚动是同一迭代里的独立子任务 (ryngvk), 到时候把 table 的行渲染接到
// 窗口化区间即可 —— 本文件的行高常量与"行按 y 等距排布"约定就是为它留的。

const (
	tableRowH     = 28 // 行高 (与 selectRowH 同值, 便于和字段控件对齐)
	tableCellPadX = 10 // 单元格水平内边距
	tableBorderW  = 1  // 网格线宽
)

// tableColumn 是一列的解析结果 (键名 / 表头文字 / 宽度 / 对齐)。
type tableColumn struct {
	key   string // 取值键 (字符串列时是下标字符串)
	label string // 表头显示文字
	width int    // 显式列宽; 0 = 按内容算
	align string // left / right / center, 缺省 left
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
			out = append(out, tableColumn{key: strconv.Itoa(i), label: string(x.Value)})
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

	y := area.Y
	// 表头行 (总在流内第一个)
	if h := tableHeaderNode(n); h != nil {
		h.Box = Rect{X: area.X, Y: y, W: area.W, H: tableRowH}
		layoutTableRowCells(h, widths, area.X)
		y += tableRowH
	}
	for _, r := range tableRowNodes(n) {
		r.Box = Rect{X: area.X, Y: y, W: area.W, H: tableRowH}
		layoutTableRowCells(r, widths, area.X)
		y += tableRowH
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
	w := 0
	for _, col := range cols {
		if col.width > 0 {
			w += col.width
			continue
		}
		lw, _ := MeasureText(col.label, n.FontSize())
		w += lw + 2*tableCellPadX
	}
	rows := len(tableRowNodes(n)) + 1 // +1 是表头 (有列定义就必有表头)
	return w, rows * tableRowH
}

// tableColumnWidths 分配列宽: 显式 width 先占, 剩下的宽度按各列"内容需要
// 的宽度"比例分。内容需求 = 表头文本与各格文本里最宽的那个 + 两侧内边距。
// 总宽不够时按比例压缩 (不溢出容器)。
func tableColumnWidths(n *GuiNode, cols []tableColumn, total int) []int {
	need := make([]int, len(cols))
	fixed := make([]int, len(cols))
	fixedSum := 0
	for i, col := range cols {
		if col.width > 0 {
			fixed[i] = col.width
			fixedSum += col.width
			continue
		}
		w, _ := MeasureText(col.label, n.FontSize())
		need[i] = w + 2*tableCellPadX
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
// 网格线用浅灰与 separator 观感统一; 斑马纹用极浅灰底, 比纯白更易读但不抢文字。
func paintTableHeader(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	face := n.fieldFace(colorTableHeaderFace)
	FillRect(img, b, tint(face, disabled))
	// 表头底边: 表格的视觉锚线, borderless 也保留
	FillRect(img, Rect{X: b.X, Y: b.Y + b.H - tableBorderW, W: b.W, H: tableBorderW},
		tint(colorTableEdge, disabled))
}

func paintTableRow(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	// 斑马纹: 由 Go 侧写入的 _zebra prop 决定 (奇数数据行着色)
	if v, ok := n.PropBool("_zebra"); ok && v {
		FillRect(img, b, tint(colorTableZebra, disabled))
	}
	// 悬停高亮只给数据行 (表头是标题, 高亮它没有意义)
	if n.hovered && !n.tblHeader {
		FillRect(img, b, tint(colorTableRowHover, disabled))
	}
	// 行底线 (borderless 时跳过)
	if !n.tableBorderless() {
		FillRect(img, Rect{X: b.X, Y: b.Y + b.H - tableBorderW, W: b.W, H: tableBorderW},
			tint(colorTableEdge, disabled))
	}
}

// paintTableCell 画单元格: 只画竖分隔线 + 文字 (底色由行负责)。文字来自
// cellText 节点字段 (数据而非元素树, 与 menu-item 画 menuLabel 同理)。
func paintTableCell(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	if !n.tableBorderless() {
		FillRect(img, Rect{X: b.X + b.W - tableBorderW, Y: b.Y, W: tableBorderW, H: b.H},
			tint(colorTableEdge, disabled))
	}
	text := n.cellText
	if text == "" {
		return
	}
	size := n.FontSize()
	tw, th := MeasureText(text, size)
	// 对齐: 只在单元格内容区内移动起点, 不改变单元格本身的分栏地位
	x := b.X + tableCellPadX
	switch n.cellAlign {
	case "right":
		x = b.X + b.W - tableCellPadX - tw
	case "center":
		x = b.X + (b.W-tw)/2
	}
	// 右对齐/居中时文字可能顶到左边界外 (内容比可用宽度长), 夹回来
	if min := b.X + 1; x < min {
		x = min
	}
	DrawText(img, img.Bounds(), text, x, b.Y+(b.H-th)/2, size,
		tint(n.textColor(), disabled), b.W-tableCellPadX)
}

// ===== props / 运行时读取 =====

func (n *GuiNode) tableBorderless() bool {
	v, ok := n.PropBool("borderless")
	return ok && v
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
		// 斑马纹: 奇数行 (1,3,5…) 着色 —— 由构造期决定, 不是 props
		if ri%2 == 1 {
			r.Props["_zebra"] = &object.Boolean{Value: true}
		}
		texts := n.tableCells(row, cols)
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

// 表格配色 (浅灰网格 + 极浅斑马纹, 与整体灰白风格一致)。
var (
	colorTableHeaderFace = color.RGBA{R: 0xF3, G: 0xF3, B: 0xF3, A: 255} // 表头底
	colorTableZebra      = color.RGBA{R: 0xFA, G: 0xFA, B: 0xFA, A: 255} // 斑马纹行底
	colorTableEdge       = color.RGBA{R: 0xD8, G: 0xD8, B: 0xD8, A: 255} // 网格线
	colorTableRowHover   = color.RGBA{R: 0xEE, G: 0xF3, B: 0xFA, A: 255} // 行悬停底
)
