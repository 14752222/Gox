package gfx

import (
	"fmt"
	"image"
	"time"

	"github.com/14752222/Gox/object"
)

// T08 datepicker 日期选择器。
//
//	<datepicker model={birthday} />                      // 受控 + model 指令
//	<datepicker value={d()} onChange={(e) => setD(e.value)} min="2020-01-01" />
//	<datepicker placeholder="请选择日期" />
//
// 结构与 select 同款 (字段 + 贴字段弹层), 差别只在弹层里画的是**日历**:
//
//	datepicker          —— 28px 字段行 (当前值/placeholder + 右侧日历图标)
//	datepicker-popup    —— 展开弹层: 月份头 (‹ 2026年11月 ›) + 星期行 + 日期格
//
// 日历**整块由 datepicker-popup 的绘制分支自己画**, 不像下拉框那样为每个
// 选项物化一个子节点。理由: 一个月的格子有 42 个, 而它们既没有内容可承载
// (纯数字) 也没有各自的样式 (高亮/禁用都由光标与 min/max 算出来), 建 42 个
// 节点换不来任何表达力, 只是把同一份几何写两遍。命中走几何 (与 tabs /
// pagination / rating 同一套"自绘区 + 几何命中"), 绘制与命中共用
// dateLayoutOf 这一份几何 —— 两处各算一份必然漂移成"点左边选中右边"。
//
// 受控语义与 select 完全一致: value 是 "YYYY-MM-DD" 字符串, 由脚本驱动;
// 点格子只派发 onChange({value}), 显示内容仍取决于下一轮 value prop。
// model 指令把它接成 value ⇄ onChange (见 gfx/model.go)。
//
// v1 边界 (写清楚免得被当 bug):
//   - 只认 "YYYY-MM-DD" 一种格式, 显示与取值都是它 (没有 format prop: 多格式
//     共存要一套日期解析库, 与"零依赖"冲突; 想换观感请在脚本侧自己格式化);
//   - 无范围选择 (range)、无时间部分。范围选择是另一种组件 (两个日历 + 选中态),
//     真要做时按同样的几何单独落一个 daterange, 不是给这个加 prop;
//   - 弹层不会向窗口上方翻转 (select 也没有): 字段贴着窗口底边时弹层会被裁掉。

// ===== 尺寸常量 =====

const (
	// dpFieldIconW 是字段右侧日历图标的边长。
	dpFieldIconW = 14
	// dpFieldMinW 是无显式宽度时的最小字段宽度 (要能放下 "2026-11-03" 与图标)。
	dpFieldMinW = 130
	// dpPopupPad 是弹层四周内边距。
	dpPopupPad = 8
	// dpHeadH 是月份头高度 (标题 + 左右翻月热区)。
	dpHeadH = 26
	// dpWeekH 是星期行高度。
	dpWeekH = 20
	// dpCellW / dpCellH 是单个日期格的尺寸。
	dpCellW = 28
	dpCellH = 24
	// dpNavW 是左右翻月热区宽度。
	dpNavW = 22
	// dpReadoutH 是弹层底部"选中值回显"行的高度。
	dpReadoutH = 20
)

// dpNow 取"今天"。抽成变量是为了让单测能把日期钉死 —— 日历里"今天"的
// 描边与"无 value 时展开到哪个月"都读它, 直接调 time.Now 的话这两个断言
// 会随运行日期漂移 (跨月那几天必然偶发失败)。
var dpNow = time.Now

// dpWeekdayNames 是星期行的中文短名 (下标 = Weekday, 0 = 周日)。
var dpWeekdayNames = [7]string{"日", "一", "二", "三", "四", "五", "六"}

// ===== props =====

// datepickerInChain 从 n 起沿祖先链找第一个 datepicker。
func datepickerInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "datepicker" {
			return p
		}
	}
	return nil
}

// dateValue 读 value prop (受控值, "YYYY-MM-DD"); 缺失/非法 → ok=false。
func (n *GuiNode) dateValue() (dateParts, bool) {
	s, ok := n.PropStr("value")
	if !ok || s == "" {
		return dateParts{}, false
	}
	return dateParse(s)
}

// datePlaceholder 读占位文本 (缺省 "请选择日期")。
func (n *GuiNode) datePlaceholder() string {
	if s, ok := n.PropStr("placeholder"); ok && s != "" {
		return s
	}
	return "请选择日期"
}

// dateMin / dateMax 读 min / max prop (可选; 缺失 = 不限)。
func (n *GuiNode) dateMin() (dateParts, bool) {
	if s, ok := n.PropStr("min"); ok && s != "" {
		return dateParse(s)
	}
	return dateParts{}, false
}

func (n *GuiNode) dateMax() (dateParts, bool) {
	if s, ok := n.PropStr("max"); ok && s != "" {
		return dateParse(s)
	}
	return dateParts{}, false
}

// dateText 返回字段上应显示的文本 (受控值; 无值 → ok=false 用 placeholder)。
func (n *GuiNode) dateText() (string, bool) {
	d, ok := n.dateValue()
	if !ok {
		return "", false
	}
	return d.String(), true
}

// ===== 日期算术 =====
//
// 全部走 time.Date 的**归一化**行为 (Go 会把 2026-13-01 收成 2027-01-01,
// 把 day=0 收成上月最后一天) —— 于是"加减一个月""这个月几天"都不用自己写
// 闰年与大小月的分支。

// dateParts 是一个日历日 (无时区/时间部分, 只关心年月日)。
type dateParts struct {
	Y, M, D int
}

// String 输出 "YYYY-MM-DD" (受控值与 onChange 的统一格式)。
func (d dateParts) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Y, d.M, d.D)
}

// dateParse 解析 "YYYY-MM-DD"; 失败 ok=false (不 panic, 也不做模糊猜测)。
//
// 用 time.Parse 而不是自己切字符串: 它顺带挡下 "2026-2-3" 这种非零填充写法
// 与 "2026-02-30" 这种不存在的日期 —— 后者的静默归一化会让 value 与界面上
// 显示的日期不一致, 是最难查的一类。
func dateParse(s string) (dateParts, bool) {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return dateParts{}, false
	}
	return dateParts{Y: t.Year(), M: int(t.Month()), D: t.Day()}, true
}

// dateFromTime 把 time.Time 折算成 dateParts (丢掉时间部分)。
func dateFromTime(t time.Time) dateParts {
	return dateParts{Y: t.Year(), M: int(t.Month()), D: t.Day()}
}

// dateDaysInMonth 返回某年某月的天数 (m 允许越界, 由 time 归一化)。
func dateDaysInMonth(y, m int) int {
	return time.Date(y, time.Month(m)+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

// dateWeekday 返回某日的星期 (0 = 周日)。
func dateWeekday(y, m, d int) int {
	return int(time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).Weekday())
}

// dateAddDays 在 y-m-d 上加减天数 (跨月/跨年自动进位)。
func dateAddDays(y, m, d, delta int) dateParts {
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC).AddDate(0, 0, delta)
	return dateFromTime(t)
}

// dateAddMonths 在 y-m-d 上加减月份, **日子钳到目标月的天数上限**
// (1月31日 + 1个月 = 2月28/29日, 而不是被归一化成 3月3日)。
func dateAddMonths(y, m, d, delta int) dateParts {
	t := time.Date(y, time.Month(m+delta), 1, 0, 0, 0, 0, time.UTC)
	last := dateDaysInMonth(t.Year(), int(t.Month()))
	if d > last {
		d = last
	}
	return dateParts{Y: t.Year(), M: int(t.Month()), D: d}
}

// dateCompare 三态比较 (-1 / 0 / 1)。
func dateCompare(a, b dateParts) int {
	switch {
	case a.Y != b.Y:
		if a.Y < b.Y {
			return -1
		}
		return 1
	case a.M != b.M:
		if a.M < b.M {
			return -1
		}
		return 1
	case a.D != b.D:
		if a.D < b.D {
			return -1
		}
		return 1
	}
	return 0
}

// ===== 光标与视图状态 =====
//
// 光标 (calCursor) 就是"当前指向的那一天", 视图月份由它推出来 —— 不另存一份
// "正在看哪个月", 两者也就没有分叉的可能 (翻月 = 把光标挪到目标月)。
// 弹层的光标不复用 highlight: highlight 是"该月份里的第几格", 而这里需要的是
// 一个完整的日期 (方向键可以跨月), 用下标表达不了。

// dateCursor 返回当前光标日期; 弹层没开过时按 value / 今天兜底。
func (n *GuiNode) dateCursor() dateParts {
	if n.expanded && n.calSet {
		return dateParts{Y: n.calY, M: n.calM, D: n.calD}
	}
	if d, ok := n.dateValue(); ok {
		return d
	}
	return dateFromTime(dpNow())
}

// setDateCursor 写光标 (同时决定视图月份)。
func (n *GuiNode) setDateCursor(d dateParts) {
	n.calY, n.calM, n.calD = d.Y, d.M, d.D
	n.calSet = true
}

// dateGridRows 返回当前光标月份在日历里要占几行 (4/5/6)。
func (n *GuiNode) dateGridRows() int {
	c := n.dateCursor()
	return dateGridRowsOf(c.Y, c.M)
}

// dateGridRowsOf 算某月日历需要几周: 首日之前的空格 + 当月天数, 向上取整。
func dateGridRowsOf(y, m int) int {
	lead := dateWeekday(y, m, 1)
	days := dateDaysInMonth(y, m)
	rows := (lead + days + 6) / 7
	if rows < 4 {
		rows = 4 // 2 月有时只要 4 行; 固定下限让弹层高度不随月份跳动
	}
	return rows
}

// ===== 展开 / 收起 =====

// attachDatepickerHandler 给 datepicker 装上内置的展开处理器 (由 JSBuiltinH
// 在建节点时调用; 手法与 attachSelectHandler 完全一致 —— 包一层脚本自己的
// onClick, 于是"组件语义"与"用户回调"共存)。
func attachDatepickerHandler(n *GuiNode) {
	user := n.PropHandler("onClick")
	n.Props["onClick"] = object.NewBuiltin("datepickerToggle", func(args ...object.Value) object.Value {
		if a := appOfNode(n); a != nil {
			a.toggleDatepicker(n)
		}
		if user != nil {
			app := appOfNode(n)
			if app != nil {
				app.callHandlerValue(user, "onClick", nil)
			}
		}
		return object.UndefinedSingleton
	})
}

// toggleDatepicker 展开 / 收起日历 (点字段本身, 或键盘 Enter/Space)。
func (a *app) toggleDatepicker(dp *GuiNode) {
	if dp.expanded {
		a.closePopupField(dp)
		return
	}
	a.setFocus(dp)
	a.openDatepicker(dp)
}

// openDatepicker 展开日历弹层。
func (a *app) openDatepicker(dp *GuiNode) {
	if dp.expanded || dp.disabledInChain() {
		return
	}
	// 光标起点: 有值就是那一天, 没值就是今天。都落在 min/max 之内 (越界的
	// 初始光标会让方向键"按了不动", 看起来像坏了)。
	start := dp.dateCursor()
	start = dp.dateClamp(start)
	dp.setDateCursor(start)
	a.openPopupField(dp, buildDatepickerPopup(dp))
}

// buildDatepickerPopup 造出日历弹层 (一个空盒子, 内容由绘制分支画)。
func buildDatepickerPopup(dp *GuiNode) *GuiNode {
	popup := &GuiNode{Tag: "datepicker-popup", Props: map[string]object.Value{}}
	withStrProp(popup, "background", colorPopupFaceHex)
	withStrProp(popup, "border", colorPopupEdgeHex)
	withBoolProp(popup, "escapeClipping", true)
	withNumProp(popup, "zIndex", selectPopupZ)
	popup.owner = dp
	popup.Parent = dp
	dp.Children = append(dp.Children, popup)
	return popup
}

// ===== 取值 / 限制 =====

// dateClamp 把日期钳进 [min, max] (未设的一端不限)。
func (n *GuiNode) dateClamp(d dateParts) dateParts {
	if min, ok := n.dateMin(); ok && dateCompare(d, min) < 0 {
		d = min
	}
	if max, ok := n.dateMax(); ok && dateCompare(d, max) > 0 {
		d = max
	}
	return d
}

// dateAllowed 报告某日是否可选 (在 min/max 之内)。
//
// 与 dateClamp 分开: clamp 是"把光标拉回范围"(方向键越界时不动), allowed 是
// "这一格能不能点"(范围外的格子画成灰的、点了没反应)。用同一个函数表达两件事
// 会让"钳位"把范围外的点击也变成选中相邻日 —— 那是更糟的行为。
func (n *GuiNode) dateAllowed(d dateParts) bool {
	if min, ok := n.dateMin(); ok && dateCompare(d, min) < 0 {
		return false
	}
	if max, ok := n.dateMax(); ok && dateCompare(d, max) > 0 {
		return false
	}
	return true
}

// dateChoose 选中某日: 光标跟上 → 收起弹层 → 焦点收回字段 → 派发 onChange。
//
// **不写回 value**: 受控组件, 值由脚本的 signal 决定 (与 chooseOption 同款)。
// 值没变时不派发, 免得脚本收到一串无意义的事件。
func (a *app) dateChoose(dp *GuiNode, day int) {
	c := dp.dateCursor()
	d := dateParts{Y: c.Y, M: c.M, D: day}
	if !dp.dateAllowed(d) {
		return
	}
	dp.setDateCursor(d)
	a.closePopupField(dp)
	a.setFocus(dp)
	if old, ok := dp.dateValue(); ok && dateCompare(old, d) == 0 {
		return
	}
	h := dp.PropHandler("onChange")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(d.String()))
	a.callHandler(dp, "onChange", arg)
}

// dateMoveCursor 把光标挪 delta 天 (跨月自动翻页)。
func (a *app) dateMoveCursor(dp *GuiNode, delta int) {
	c := dp.dateCursor()
	next := dateAddDays(c.Y, c.M, c.D, delta)
	// 越界时**停在原地**, 而不是钳到边界: 钳位会让"连按 → 到 min 之后一直
	// 给同一天", 用户按了没反应却也不知道为什么; 停住与"已经到头了"一致。
	if !dp.dateAllowed(next) {
		return
	}
	dp.setDateCursor(next)
	markFullDirtyFor(dp)
}

// dateShiftMonth 翻到上/下一个月 (光标日子按目标月钳位)。
func (a *app) dateShiftMonth(dp *GuiNode, delta int) {
	c := dp.dateCursor()
	next := dateAddMonths(c.Y, c.M, c.D, delta)
	next = dp.dateClamp(next)
	dp.setDateCursor(next)
	markFullDirtyFor(dp)
}

// ===== 键盘 =====

// handleDatepickerKey 处理字段链上的按键, 返回是否消费。
//
// 语义对齐 ARIA 的 datepicker 约定:
//
//	←/→       前后一天      ↑/↓   前后一周
//	PageUp/Down 前后一月    Home/End 本月首/末日
//	Enter/Space 展开; 展开后 = 选中光标那天
//	Esc        收起
//
// 带 Ctrl/Alt 的组合键留给脚本 (同 input/select)。
func (a *app) handleDatepickerKey(dp *GuiNode, key string, ev Event) bool {
	if ev.Ctrl || ev.Alt {
		return false
	}
	switch key {
	case "Enter", " ":
		if !dp.expanded {
			a.toggleDatepicker(dp)
			return true
		}
		a.dateChoose(dp, dp.dateCursor().D)
		return true
	case "Escape":
		if dp.expanded {
			a.closePopupField(dp)
			return true
		}
		return false
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown":
		if !dp.expanded {
			// 未展开时按方向键先进弹层 (与 select 同款): 否则这四个键在
			// 字段上完全静默, 键盘用户会以为这个控件不可用。
			a.toggleDatepicker(dp)
			return true
		}
		delta := 1
		switch key {
		case "ArrowLeft":
			delta = -1
		case "ArrowUp":
			delta = -7
		case "ArrowDown":
			delta = 7
		}
		a.dateMoveCursor(dp, delta)
		return true
	case "PageUp", "PageDown":
		if !dp.expanded {
			a.toggleDatepicker(dp)
			return true
		}
		delta := 1
		if key == "PageUp" {
			delta = -1
		}
		a.dateShiftMonth(dp, delta)
		return true
	case "Home", "End":
		if !dp.expanded {
			return false
		}
		c := dp.dateCursor()
		day := 1
		if key == "End" {
			day = dateDaysInMonth(c.Y, c.M)
		}
		target := dateParts{Y: c.Y, M: c.M, D: day}
		if !dp.dateAllowed(target) {
			return true // 消费掉: 本月没有可选的端点日
		}
		dp.setDateCursor(target)
		markFullDirtyFor(dp)
		return true
	}
	return false
}

// ===== 布局 =====

// dateLayoutOf 是日历弹层里**所有**几何的唯一来源: 绘制与命中共用。
//
// 前提: 弹层盒子已经由 layoutDatepicker 定好 (它贴在字段正下方, 尺寸由
// datepickerPopupSize 算)。所以这里不再做任何尺寸决策, 只是把盒子切成
// "标题行 / 星期行 / 日期网格"三个区块并把左右翻月热区算出来。
type dateLayout struct {
	popup Rect
	headY int
	weekY int
	gridY int
	rows  int
	prev  Rect
	next  Rect
}

func dateLayoutOf(dp *GuiNode) (dateLayout, bool) {
	if dp == nil || dp.popup == nil {
		return dateLayout{}, false
	}
	b := dp.popup.Box
	if b.W <= 0 || b.H <= 0 {
		return dateLayout{}, false
	}
	l := dateLayout{popup: b, rows: dp.dateGridRows()}
	l.headY = b.Y + dpPopupPad
	l.weekY = l.headY + dpHeadH
	l.gridY = l.weekY + dpWeekH
	l.prev = Rect{X: b.X + dpPopupPad, Y: l.headY, W: dpNavW, H: dpHeadH}
	l.next = Rect{X: b.X + b.W - dpPopupPad - dpNavW, Y: l.headY, W: dpNavW, H: dpHeadH}
	return l, true
}

// datepickerPopupSize 算日历弹层的尺寸 (宽度按 7 格, 高度按行数 + 回显行)。
func datepickerPopupSize(dp *GuiNode) (w, h int) {
	w = 2*dpPopupPad + 7*dpCellW
	h = 2*dpPopupPad + dpHeadH + dpWeekH + dp.dateGridRows()*dpCellH + dpReadoutH
	return w, h
}

// layoutDatepicker 摆放日期字段: 与 layoutSelect 同款 —— 字段没有流内子节点
// (内容全由绘制分支画), 弹层由这里显式定位 (贴字段正下方, 不比字段窄)。
//
// 放在布局阶段而不是"展开时算一次坐标": 布局每帧都跑, 窗口 resize 后弹层
// 自然跟着走, 不需要额外的失效通知。
func layoutDatepicker(n *GuiNode) {
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
	pw, ph := datepickerPopupSize(n)
	if w := n.Box.W; w > pw {
		pw = w // 字段更宽时跟着字段走 (对齐好看), 更窄时保留日历自己的最小宽度
	}
	n.popup.Box = Rect{X: n.Box.X, Y: n.Box.Y + n.Box.H, W: pw, H: ph}
	layoutNode(n.popup)
}

// intrinsicDatepicker 字段的固有尺寸: 高 28 (与 select/input 同一常量),
// 宽按当前值/placeholder 文本 + 图标 + 两侧留白。
func intrinsicDatepicker(n *GuiNode) (w, h int) {
	text, ok := n.dateText()
	if !ok {
		text = n.datePlaceholder()
	}
	tw, _ := MeasureText(text, n.FontSize())
	w = tw + 2*fieldPadX + dpFieldIconW + searchIconGap
	if w < dpFieldMinW {
		w = dpFieldMinW
	}
	h = selectRowH
	return w, h
}

// ===== 绘制 =====

// paintDatepicker 画字段行: 与 select 同一套外观 (白底 + 1px 边框 + 值),
// 只是右侧的箭头换成日历图标, 展开时边框换强调色。
func paintDatepicker(img *image.RGBA, n *GuiNode, disabled bool) {
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

	size := n.FontSize()
	text, ok := n.dateText()
	textColor := colorText
	if !ok {
		text, textColor = n.datePlaceholder(), colorPlaceholder
	}
	if text != "" {
		_, th := MeasureText(text, size)
		maxW := b.W - 2*fieldPadX - dpFieldIconW - searchIconGap
		if maxW < 0 {
			maxW = 0
		}
		DrawText(img, img.Bounds(), text, b.X+fieldPadX, b.Y+(b.H-th)/2, size,
			tint(textColor, disabled), maxW)
	}
	if fn := builtinIcons["calendar"]; fn != nil {
		fn(img, Rect{
			X: b.X + b.W - fieldPadX - dpFieldIconW,
			Y: b.Y + (b.H-dpFieldIconW)/2,
			W: dpFieldIconW, H: dpFieldIconW,
		}, dpFieldIconW, tint(colorInputEdge, disabled))
	}
}

// paintDatepickerPopup 画整个日历弹层: 月份头 / 星期行 / 日期网格 / 底部回显。
func paintDatepickerPopup(img *image.RGBA, n *GuiNode, disabled bool) {
	dp := n.owner
	l, ok := dateLayoutOf(dp)
	if !ok || dp == nil {
		return
	}
	paintBoxDecor(img, n, disabled)

	cursor := dp.dateCursor()
	size := dp.FontSize()
	disabled = disabled || dp.disabledInChain()

	// 月份头: 左右翻月图标 + 居中标题。
	paintNavArrow(img, l.prev, false, disabled)
	paintNavArrow(img, l.next, true, disabled)
	title := fmt.Sprintf("%d年%d月", cursor.Y, cursor.M)
	tw, th := MeasureText(title, size)
	if tw > l.popup.W-2*dpNavW {
		tw = l.popup.W - 2*dpNavW
	}
	DrawText(img, img.Bounds(), title,
		l.popup.X+(l.popup.W-tw)/2, l.headY+(dpHeadH-th)/2, size,
		tint(colorText, disabled), tw)

	// 星期行: 七列均分, 每格居中。
	for i := 0; i < 7; i++ {
		w, h := MeasureText(dpWeekdayNames[i], size)
		x := l.popup.X + dpPopupPad + i*dpCellW + (dpCellW-w)/2
		DrawText(img, img.Bounds(), dpWeekdayNames[i], x, l.weekY+(dpWeekH-h)/2, size,
			tint(colorPlaceholder, disabled), w)
	}

	// 日期网格: 逐格判定"属于本月?""是选中日?""是光标?""可选?"。
	sel, hasSel := dp.dateValue()
	today := dateFromTime(dpNow())
	lead := dateWeekday(cursor.Y, cursor.M, 1)
	days := dateDaysInMonth(cursor.Y, cursor.M)
	for row := 0; row < l.rows; row++ {
		for col := 0; col < 7; col++ {
			day := row*7 + col - lead + 1
			if day < 1 || day > days {
				continue // 月外的空格: 不画 (留白比画灰色上月日期更干净)
			}
			cell := Rect{
				X: l.popup.X + dpPopupPad + col*dpCellW,
				Y: l.gridY + row*dpCellH,
				W: dpCellW, H: dpCellH,
			}
			d := dateParts{Y: cursor.Y, M: cursor.M, D: day}
			allowed := dp.dateAllowed(d)
			selected := hasSel && dateCompare(sel, d) == 0
			label := fmt.Sprintf("%d", day)
			lw, lh := MeasureText(label, size)
			tx, ty := cell.X+(cell.W-lw)/2, cell.Y+(cell.H-lh)/2

			switch {
			case selected:
				FillRect(img, cell, tint(colorAccent, disabled))
				DrawText(img, img.Bounds(), label, tx, ty, size, colorAccentText, lw)
			case dateCompare(d, cursor) == 0:
				// 键盘光标: 铺一层浅底 + 强调色文字 (与下拉项的 highlight 同款观感)。
				FillRect(img, cell, tint(colorOptionActive, disabled))
				DrawText(img, img.Bounds(), label, tx, ty, size, tint(colorAccent, disabled), lw)
			default:
				// 今天加一圈描边 (与选中/光标可以叠加, 因为它是边框不是填充)。
				if dateCompare(today, d) == 0 {
					StrokeRect(img, cell, tint(colorAccent, disabled))
				}
				c := colorText
				if !allowed {
					c = colorPlaceholder
				}
				DrawText(img, img.Bounds(), label, tx, ty, size, tint(c, disabled), lw)
			}
		}
	}

	// 底部回显: 光标那天 (+ 可选/不可选的提示)。键盘用户在弹层里挪动时,
	// 这里给出明确的"当前指向哪一天"的文字反馈。
	readout := cursor.String()
	readoutColor := colorText
	if !dp.dateAllowed(cursor) {
		readout = readout + "  (超出可选范围)"
		readoutColor = colorPlaceholder
	}
	rw, rh := MeasureText(readout, size)
	rx := l.popup.X + (l.popup.W-rw)/2
	ry := l.popup.Y + l.popup.H - dpPopupPad - dpReadoutH + (dpReadoutH-rh)/2
	DrawText(img, img.Bounds(), readout, rx, ry, size, tint(readoutColor, disabled), rw)
}

// paintNavArrow 画翻月箭头 (复用内置 icon 的 arrow-left / arrow-right)。
func paintNavArrow(img *image.RGBA, box Rect, next bool, disabled bool) {
	name := "arrow-left"
	if next {
		name = "arrow-right"
	}
	fn := builtinIcons[name]
	if fn == nil {
		return
	}
	size := 12
	fn(img, Rect{X: box.X + (box.W-size)/2, Y: box.Y + (box.H-size)/2, W: size, H: size},
		size, tint(colorInputEdge, disabled))
}

// ===== 命中 =====

// dateHitAt 把弹层里的一次按下翻译成动作, 返回:
//
//	0 = 这次按下不在弹层里 (调用方继续走通用流程)
//	1 = 已经在弹层里处理掉了 (翻月 / 选中某日 / 点在月外空格上)
//
// 用"有没有处理掉"而不是"选中了哪一天"当返回值: 点在月外空格上也是"这次
// 按下属于日历", 不该让事件继续往下钻 (否则会落到字段上把弹层又收起一次)。
func (n *GuiNode) dateHitAt(x, y int) int {
	l, ok := dateLayoutOf(n)
	if !ok || !l.popup.Contains(x, y) {
		return 0
	}
	a := appOfNode(n)
	if a == nil {
		return 1
	}
	switch {
	case l.prev.Contains(x, y):
		a.dateShiftMonth(n, -1)
		return 1
	case l.next.Contains(x, y):
		a.dateShiftMonth(n, 1)
		return 1
	}
	col := (x - l.popup.X - dpPopupPad) / dpCellW
	row := (y - l.gridY) / dpCellH
	if col < 0 || col >= 7 || row < 0 || row >= l.rows {
		return 1
	}
	day := row*7 + col - dateWeekday(n.dateCursor().Y, n.dateCursor().M, 1) + 1
	if day < 1 || day > dateDaysInMonth(n.dateCursor().Y, n.dateCursor().M) {
		return 1
	}
	a.dateChoose(n, day)
	return 1
}
