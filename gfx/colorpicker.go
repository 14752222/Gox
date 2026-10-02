package gfx

import (
	"image"
	"strings"

	"github.com/14752222/Gox/object"
)

// T08 colorpicker 取色器。
//
//	<colorpicker model={tint} />                    // 受控 + model 指令
//	<colorpicker value={c()} onChange={(e) => setC(e.value)} />
//	<colorpicker colors={["#fff", "#000"]} columns={2} />
//
// 结构与 select / datepicker 同款 (字段 + 贴字段弹层):
//
//	colorpicker          —— 28px 字段行 (左侧色块 + 十六进制文本/placeholder)
//	colorpicker-popup    —— 展开弹层: N×M 色板格 + 底部显示光标色的回显行
//
// **调色板而不是取色盘 (hue wheel / saturation square)**: 后者需要 HSV→RGB
// 转换与逐像素渐变绘制, 是"另一个组件"的体量; 而组件库这一期的目标是
// "表单能填完", 预设色板覆盖了绝大多数场景 (主题色选择、标签着色)。脚本想
// 要任意色时仍可自己写 <canvas> + onDraw, 或者直接用 <input> 收十六进制串。
//
// 色板整块由 colorpicker-popup 的绘制分支自己画 (不物化子节点), 命中走几何 ——
// 与 datepicker 同一套理由 (格子里只有颜色, 没有内容也没有各自的样式)。
//
// 受控语义: value 是 "#rrggbb" / "#rgb" / "#rrggbbaa" 字符串, 由脚本驱动;
// 点色块只派发 onChange({value}), 显示内容仍取决于下一轮 value prop。
//
// v1 边界: 不内置吸管 (系统取色器) 与自定色输入框 —— 两者都要平台能力或
// 额外的文本编辑链路, 不在一期范围 (写在这里免得被当 bug)。

// ===== 尺寸常量 =====

const (
	// cpSwatchSize 是字段左侧小色块的边长。
	cpSwatchSize = 14
	// cpFieldMinW 是无显式宽度时的最小字段宽度。
	cpFieldMinW = 110
	// cpDefaultColumns 是色板的缺省列数。
	cpDefaultColumns = 8
	// cpMaxColumns 是列数上限 (与 grid 的 columns 上限一致)。
	cpMaxColumns = 32
	// cpPopupPad 是弹层四周内边距。
	cpPopupPad = 8
	// cpCellSize 是单个色块格的边长 (含 1px 边框)。
	cpCellSize = 22
	// cpCellGap 是色块之间的间距。
	cpCellGap = 4
	// cpReadoutH 是弹层底部回显行高度。
	cpReadoutH = 20
)

// cpDefaultPalette 是缺省色板 (24 色, 缺省 8 列 ⇒ 3 行): 灰阶 7 + 白 + 棕 + 16 个色相。
//
// 刻意的排布: 第一行是灰阶与白 (最常用的"中性色"), 后两行按色相绕着
// 色环排 (红 → 品红 → 紫 → 蓝 → 青 → 绿 → 黄 → 橙), 相邻格子的观感差异
// 足够大 —— 色板排得跳来跳去时, 用键盘点选会很难受。
var cpDefaultPalette = []string{
	"#000000", "#434343", "#666666", "#999999", "#cccccc", "#efefef", "#ffffff", "#795548",
	"#e53935", "#d81b60", "#8e24aa", "#5e35b1", "#3949ab", "#1e88e5", "#039be5", "#00acc1",
	"#00897b", "#43a047", "#7cb342", "#c0ca33", "#fdd835", "#ffb300", "#fb8c00", "#f4511e",
}

// ===== props =====

// colorpickerInChain 从 n 起沿祖先链找第一个 colorpicker。
func colorpickerInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "colorpicker" {
			return p
		}
	}
	return nil
}

// colorValue 读 value prop (受控值)。字符串原样返回 (不解析也不归一化:
// 显示要的是**脚本写的那串**, 归一化后反而看不出自己写的是什么);
// 颜色对象 {r,g,b} 也认, 转成 #rrggbb。
func (n *GuiNode) colorValue() (string, bool) {
	v, ok := n.Props["value"]
	if !ok || v == nil {
		return "", false
	}
	switch x := v.(type) {
	case *object.String:
		if x.Value == "" {
			return "", false
		}
		return x.Value, true
	case *object.Object:
		// {r, g, b} / {r,g,b,a} —— 与 canvas 的 ImageData 同一套字段名
		r, okR := x.GetProperty("r")
		g, okG := x.GetProperty("g")
		b, okB := x.GetProperty("b")
		if !okR || !okG || !okB {
			return "", false
		}
		rr, ok1 := channel255(r)
		gg, ok2 := channel255(g)
		bb, ok3 := channel255(b)
		if !ok1 || !ok2 || !ok3 {
			return "", false
		}
		return hexOfColor(rr, gg, bb), true
	}
	return "", false
}

// channel255 把 0-255 的分量转成 uint8 (越界/非数字 → ok=false)。
func channel255(v object.Value) (uint8, bool) {
	f, ok := v.(*object.Number)
	if !ok {
		return 0, false
	}
	n := int(f.Value)
	if n < 0 {
		n = 0
	}
	if n > 255 {
		n = 255
	}
	return uint8(n), true
}

// hexOfColor 输出 "#rrggbb"。
func hexOfColor(r, g, b uint8) string {
	const digits = "0123456789abcdef"
	out := []byte{'#', 0, 0, 0, 0, 0, 0}
	pairs := [3]uint8{r, g, b}
	for i, p := range pairs {
		out[1+i*2] = digits[p>>4]
		out[2+i*2] = digits[p&0x0f]
	}
	return string(out)
}

// colorPlaceholder 读占位文本 (缺省 "选择颜色")。
func (n *GuiNode) colorPlaceholder() string {
	if s, ok := n.PropStr("placeholder"); ok && s != "" {
		return s
	}
	return "选择颜色"
}

// colorPalette 读色板: colors prop (字符串数组; 非法项跳过), 缺省 cpDefaultPalette。
//
// 空数组**不**回落到缺省色板 —— 脚本显式给 [] 是"我要一个空色板"(常见于
// 色板还没加载完), 塞回 24 个缺省色会让"加载中"看起来像"已经好了"。
func (n *GuiNode) colorPalette() []string {
	v, ok := n.Props["colors"]
	if !ok {
		return cpDefaultPalette
	}
	arr, ok := v.(*object.Array)
	if !ok {
		return cpDefaultPalette
	}
	out := make([]string, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		if s, ok := e.(*object.String); ok && s.Value != "" {
			out = append(out, s.Value)
		}
	}
	return out
}

// colorColumns 读列数 (缺省 8, 钳到 [1, 32])。
func (n *GuiNode) colorColumns() int {
	c := cpDefaultColumns
	if v, ok := n.PropNum("columns"); ok && int(v) > 0 {
		c = int(v)
	}
	if c > cpMaxColumns {
		c = cpMaxColumns
	}
	return c
}

// colorIndex 返回当前受控值在色板里的下标 (-1 = 不在色板里)。
// 色块比较统一小写去空白: 脚本写 "#FFF" 与色板里的 "#ffffff" 是同一个颜色,
// 纯字符串比较会让"选中态丢高亮"。
func (n *GuiNode) colorIndex() int {
	cur, ok := n.colorValue()
	if !ok {
		return -1
	}
	cur = normalizeHex(cur)
	for i, c := range n.colorPalette() {
		if normalizeHex(c) == cur {
			return i
		}
	}
	return -1
}

// normalizeHex 小写 + 去空白 (仅用于比较, 不改显示)。
func normalizeHex(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ===== 展开 / 收起 =====

// attachColorpickerHandler 给 colorpicker 装上内置的展开处理器
// (由 JSBuiltinH 在建节点时调用; 手法与 attachSelectHandler 一致)。
func attachColorpickerHandler(n *GuiNode) {
	user := n.PropHandler("onClick")
	n.Props["onClick"] = object.NewBuiltin("colorpickerToggle", func(args ...object.Value) object.Value {
		a := appOfNode(n)
		if a != nil {
			a.toggleColorpicker(n)
			if user != nil {
				a.callHandlerValue(user, "onClick", nil)
			}
		}
		return object.UndefinedSingleton
	})
}

// toggleColorpicker 展开 / 收起色板。
func (a *app) toggleColorpicker(cp *GuiNode) {
	if cp.expanded {
		a.closePopupField(cp)
		return
	}
	a.setFocus(cp)
	a.openColorpicker(cp)
}

// openColorpicker 展开色板弹层。
func (a *app) openColorpicker(cp *GuiNode) {
	if cp.expanded || cp.disabledInChain() || len(cp.colorPalette()) == 0 {
		return // 空色板: 展开一个空盒子没有意义 (与 select 无选项同款)
	}
	// 光标起点: 当前值在色板里的位置; 不在色板里 (或还没有值) 就从第一格起。
	idx := cp.colorIndex()
	if idx < 0 {
		idx = 0
	}
	// highlight 要在 openPopupField 之前写: 后者会先收掉别的弹层, 而收弹层
	// 会把 highlight 复位 (closePopupField 的最后一步)。
	cp.highlight = idx
	a.openPopupField(cp, buildColorpickerPopup(cp))
}

// buildColorpickerPopup 造出色板弹层 (空盒子, 内容由绘制分支画)。
func buildColorpickerPopup(cp *GuiNode) *GuiNode {
	popup := &GuiNode{Tag: "colorpicker-popup", Props: map[string]object.Value{}}
	withStrProp(popup, "background", colorPopupFaceHex)
	withStrProp(popup, "border", colorPopupEdgeHex)
	withBoolProp(popup, "escapeClipping", true)
	withNumProp(popup, "zIndex", selectPopupZ)
	popup.owner = cp
	popup.Parent = cp
	cp.Children = append(cp.Children, popup)
	return popup
}

// ===== 取值 =====

// colorChoose 选中色板里的第 idx 格: 收起弹层 → 焦点收回字段 → 派发 onChange。
func (a *app) colorChoose(cp *GuiNode, idx int) {
	pal := cp.colorPalette()
	if idx < 0 || idx >= len(pal) {
		return
	}
	value := pal[idx]
	cp.highlight = idx
	a.closePopupField(cp)
	a.setFocus(cp)
	// 值没变就不派发 (与 dateChoose / chooseOption 同一口径)。
	if cur, ok := cp.colorValue(); ok && normalizeHex(cur) == normalizeHex(value) {
		return
	}
	h := cp.PropHandler("onChange")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(value))
	a.callHandler(cp, "onChange", arg)
}

// colorMoveCursor 把色板光标挪 delta 格 (按行列走, 越界**不绕回**)。
//
// 不绕回是有意的: 色板是二维的, 从第一行按↑绕到最后一行会让"往上按"变成
// "跳到最下面", 与直觉相反 (radio 组那种一维组才适合绕回)。
func (a *app) colorMoveCursor(cp *GuiNode, delta int) {
	pal := cp.colorPalette()
	if len(pal) == 0 {
		return
	}
	cur := cp.highlight
	if cur < 0 || cur >= len(pal) {
		cur = 0
	} else {
		next := cur + delta
		if next < 0 || next >= len(pal) {
			next = cur // 已到边界: 停住
		}
		cur = next
	}
	cp.highlight = cur
	markFullDirtyFor(cp)
}

// ===== 键盘 =====

// handleColorpickerKey 处理字段链上的按键, 返回是否消费。
//
//	←/→ 前后一格   ↑/↓ 上下 N 格 (N = 列数)
//	Home/End 首/末格   Enter/Space 展开; 展开后 = 选中光标色
//	Esc 收起
func (a *app) handleColorpickerKey(cp *GuiNode, key string, ev Event) bool {
	if ev.Ctrl || ev.Alt {
		return false
	}
	switch key {
	case "Enter", " ":
		if !cp.expanded {
			a.toggleColorpicker(cp)
			return true
		}
		a.colorChoose(cp, cp.highlight)
		return true
	case "Escape":
		if cp.expanded {
			a.closePopupField(cp)
			return true
		}
		return false
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown":
		if !cp.expanded {
			a.toggleColorpicker(cp)
			return true
		}
		cols := cp.colorColumns()
		delta := 1
		switch key {
		case "ArrowLeft":
			delta = -1
		case "ArrowUp":
			delta = -cols
		case "ArrowDown":
			delta = cols
		}
		a.colorMoveCursor(cp, delta)
		return true
	case "Home", "End":
		if !cp.expanded {
			return false
		}
		idx := 0
		if key == "End" {
			idx = len(cp.colorPalette()) - 1
		}
		if idx < 0 {
			return true
		}
		cp.highlight = idx
		markFullDirtyFor(cp)
		return true
	}
	return false
}

// ===== 布局 =====

// colorLayoutOf 是色板弹层几何的唯一来源 (绘制与命中共用)。
type colorLayout struct {
	popup Rect
	cols  int
	rows  int
	gridX int
	gridY int
}

// colorLayoutFor 算出弹层几何。n 是**字段** (colorpicker), 不是弹层。
func colorLayoutFor(cp *GuiNode) (colorLayout, bool) {
	if cp == nil || cp.popup == nil {
		return colorLayout{}, false
	}
	b := cp.popup.Box
	if b.W <= 0 || b.H <= 0 {
		return colorLayout{}, false
	}
	cols := cp.colorColumns()
	count := len(cp.colorPalette())
	rows := (count + cols - 1) / cols
	if rows < 1 {
		rows = 1
	}
	return colorLayout{
		popup: b,
		cols:  cols,
		rows:  rows,
		gridX: b.X + cpPopupPad,
		gridY: b.Y + cpPopupPad,
	}, true
}

// colorpickerPopupSize 算色板弹层尺寸。
func colorpickerPopupSize(cp *GuiNode) (w, h int) {
	cols := cp.colorColumns()
	count := len(cp.colorPalette())
	rows := (count + cols - 1) / cols
	if rows < 1 {
		rows = 1
	}
	w = 2*cpPopupPad + cols*cpCellSize + (cols-1)*cpCellGap
	h = 2*cpPopupPad + rows*cpCellSize + (rows-1)*cpCellGap + cpReadoutH
	return w, h
}

// layoutColorpicker 摆放颜色字段: 与 layoutSelect / layoutDatepicker 同款。
func layoutColorpicker(n *GuiNode) {
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() || c == n.popup {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
		layoutNode(c)
	}
	if n.popup == nil || !n.expanded {
		return
	}
	pw, ph := colorpickerPopupSize(n)
	if w := n.Box.W; w > pw {
		pw = w
	}
	n.popup.Box = Rect{X: n.Box.X, Y: n.Box.Y + n.Box.H, W: pw, H: ph}
	layoutNode(n.popup)
}

// intrinsicColorpicker 字段的固有尺寸。
func intrinsicColorpicker(n *GuiNode) (w, h int) {
	text, ok := n.colorValue()
	if !ok {
		text = n.colorPlaceholder()
	}
	tw, _ := MeasureText(text, n.FontSize())
	w = tw + 2*fieldPadX + cpSwatchSize + searchIconGap
	if w < cpFieldMinW {
		w = cpFieldMinW
	}
	h = selectRowH
	return w, h
}

// ===== 绘制 =====

// paintColorpicker 画字段行: 左侧色块 + 十六进制文本。
//
// 色块用**当前值解析出的颜色**填充; 解析不出来 (值写错/没有值) 就画一个
// 空框, 而不是偷偷用别色顶上 —— 用户看到"这里没有颜色"比看到"一个不对的
// 颜色"更容易发现自己的值写错了。
func paintColorpicker(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(n.fieldFace(colorFieldFace), disabled))
	edge := colorInputEdge
	if n.expanded {
		edge = colorFocusRing
	}
	StrokeRect(img, b, tint(edge, disabled))

	sw := Rect{
		X: b.X + fieldPadX,
		Y: b.Y + (b.H-cpSwatchSize)/2,
		W: cpSwatchSize, H: cpSwatchSize,
	}
	if cur, ok := n.colorValue(); ok {
		if c, ok := ParseColor(cur); ok {
			FillRect(img, sw, tint(c, disabled))
		}
	}
	StrokeRect(img, sw, tint(colorInputEdge, disabled))

	size := n.FontSize()
	text, ok := n.colorValue()
	textColor := colorText
	if !ok {
		text, textColor = n.colorPlaceholder(), colorPlaceholder
	}
	if text != "" {
		_, th := MeasureText(text, size)
		x := sw.X + sw.W + searchIconGap
		maxW := b.X + b.W - fieldPadX - x
		if maxW < 0 {
			maxW = 0
		}
		DrawText(img, img.Bounds(), text, x, b.Y+(b.H-th)/2, size,
			tint(textColor, disabled), maxW)
	}
}

// paintColorpickerPopup 画整块色板: 逐格填充 + 边框 (光标格与选中格各加一圈
// 强调色边框), 底部回显光标格的色值。
func paintColorpickerPopup(img *image.RGBA, n *GuiNode, disabled bool) {
	cp := n.owner
	l, ok := colorLayoutFor(cp)
	if !ok || cp == nil {
		return
	}
	paintBoxDecor(img, n, disabled)

	pal := cp.colorPalette()
	sel := cp.colorIndex()
	size := cp.FontSize()
	disabled = disabled || cp.disabledInChain()

	for i, hex := range pal {
		col, row := i%l.cols, i/l.cols
		cell := Rect{
			X: l.gridX + col*(cpCellSize+cpCellGap),
			Y: l.gridY + row*(cpCellSize+cpCellGap),
			W: cpCellSize, H: cpCellSize,
		}
		if c, ok := ParseColor(hex); ok {
			FillRect(img, cell, tint(c, disabled))
		}
		border := colorInputEdge
		switch {
		case i == sel:
			border = colorFocusRing
		case i == cp.highlight:
			border = colorAccent
		}
		StrokeRect(img, cell, tint(border, disabled))
	}

	// 底部回显: 光标格的色值 (键鼠都能读到"现在指着哪个颜色")。
	readout := ""
	if cp.highlight >= 0 && cp.highlight < len(pal) {
		readout = normalizeHex(pal[cp.highlight])
	}
	if readout == "" && sel >= 0 {
		readout = normalizeHex(pal[sel])
	}
	if readout != "" {
		rw, rh := MeasureText(readout, size)
		rx := l.popup.X + (l.popup.W-rw)/2
		ry := l.popup.Y + l.popup.H - cpReadoutH + (cpReadoutH-rh)/2
		if ry+rh > l.popup.Y+l.popup.H {
			ry = l.popup.Y + l.popup.H - rh
		}
		DrawText(img, img.Bounds(), readout, rx, ry, size, tint(colorText, disabled), rw)
	}
}

// ===== 命中 =====

// colorHitAt 把弹层里的一次按下翻译成动作, 返回被点中的色块下标
// (-1 = 这次按下不在弹层里, 调用方继续走通用流程; 点在弹层空白处返回 -2,
// 表示"已消费但不动作")。
func (n *GuiNode) colorHitAt(x, y int) int {
	l, ok := colorLayoutFor(n)
	if !ok || !l.popup.Contains(x, y) {
		return -1
	}
	// 相对网格原点的坐标必须落在某一格内 (格子之间有 4px 缝隙, 缝里算"空白")。
	dx, dy := x-l.gridX, y-l.gridY
	if dx < 0 || dy < 0 {
		return -2
	}
	col := dx / (cpCellSize + cpCellGap)
	row := dy / (cpCellSize + cpCellGap)
	if col >= l.cols || row >= l.rows {
		return -2
	}
	if dx%(cpCellSize+cpCellGap) >= cpCellSize {
		return -2
	}
	if dy%(cpCellSize+cpCellGap) >= cpCellSize {
		return -2
	}
	idx := row*l.cols + col
	if idx >= len(n.colorPalette()) {
		return -2
	}
	if a := appOfNode(n); a != nil {
		a.colorChoose(n, idx)
	}
	return idx
}
