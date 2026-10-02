package gfx

import (
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== T08 <datepicker> =====
//
// 用例分层与内核一致:
//   - **几何/状态层** (mountTestApp + 独立算出的坐标): 展开、翻月、点选、
//     方向键、min/max 边界、焦点回收。断言不经过 dateLayoutOf, 否则
//     "命中与绘制共用一份几何"这件事会让测试和实现一起错还能通过。
//   - **像素层** (renderTree): 选中日/今天/光标的画法。
//   - **aria 层**: role / 可聚焦 / 三件套字段都进 Tab 序。

// pinToday 把"今天"钉死。
//
// 日历里有两处读当前日期: "今天"的描边, 以及**没有 value 时**展开到哪个月。
// 不钉死的话这两个断言会随运行日期漂移 (跨月那几天必然偶发失败)。
func pinToday(t *testing.T, y, m, d int) {
	t.Helper()
	old := dpNow
	dpNow = func() time.Time { return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { dpNow = old })
}

// mkDatepickerTree 造 <column><datepicker/></column> 并挂到假窗口上。
func mkDatepickerTree(t *testing.T, configure func(*GuiNode)) (*GuiNode, *GuiNode, *app, *fakeSurface) {
	t.Helper()
	root := mkNode("column", map[string]float64{"padding": 10})
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	if configure != nil {
		configure(dp)
	}
	attachDatepickerHandler(dp)
	mountChildren(root, dp)
	fake, a := mountTestApp(t, root, 320, 320)
	return root, dp, a, fake
}

// dateCellCenter 返回日历里"某一天"的格子中心 (窗口坐标)。
//
// **刻意自己算一遍几何** (而不是调 dateLayoutOf): 那段算术是实现的私有细节,
// 测试若复用它, "绘制与命中共用一份几何"就会退化成"三处一起错也算通过"。
// 这里只依赖三个常量与"星期行在标题下、日期网格在星期行下"这个约定。
func dateCellCenter(dp *GuiNode, lead, day int) (int, int) {
	b := dp.popup.Box
	idx := lead + day - 1
	col, row := idx%7, idx/7
	x := b.X + dpPopupPad + col*dpCellW + dpCellW/2
	y := b.Y + dpPopupPad + dpHeadH + dpWeekH + row*dpCellH + dpCellH/2
	return x, y
}

// recordDates 挂一个 onChange, 把收到的 value 记下来。
func recordDates(dp *GuiNode) *[]string {
	var got []string
	dp.Props["onChange"] = object.NewBuiltin("recordDates", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("value"); ok {
					if s, ok := v.(*object.String); ok {
						got = append(got, s.Value)
					}
				}
			}
		}
		return object.UndefinedSingleton
	})
	return &got
}

// ===== 字段本身 =====

func TestDatepickerFieldShowsValueOrPlaceholder(t *testing.T) {
	pinToday(t, 2026, 11, 20)

	filled := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(filled, "value", "2026-11-03")
	empty := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}

	root := mkNode("column", nil)
	mountChildren(root, filled, empty)
	renderTree(root, 320, 80)

	if text, ok := filled.dateText(); !ok || text != "2026-11-03" {
		t.Fatalf("字段该显示受控值, got %q ok=%v", text, ok)
	}
	if _, ok := empty.dateText(); ok {
		t.Fatalf("没有 value 的字段不该有显示值")
	}
	if got := empty.datePlaceholder(); got != "请选择日期" {
		t.Fatalf("缺省 placeholder = %q, want 请选择日期", got)
	}
	// 与 select/input 同一套字段常量: 表单里三者才会齐平
	if filled.Box.H != selectRowH {
		t.Fatalf("字段行高 = %d, want %d", filled.Box.H, selectRowH)
	}
	if filled.Box.W < dpFieldMinW {
		t.Fatalf("字段宽度 %d 小于最小值 %d", filled.Box.W, dpFieldMinW)
	}
}

func TestDatepickerRejectsMalformedValue(t *testing.T) {
	// 三种都是"看起来像日期但不是"的输入; 一律当没有值 (显示 placeholder),
	// 而不是猜一个相近的日期 —— time.Parse 挡住了非零填充与不存在的日期。
	for _, bad := range []string{"2026-2-3", "2026-02-30", "20261103", "", "today"} {
		dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
		withStr(dp, "value", bad)
		if d, ok := dp.dateValue(); ok {
			t.Fatalf("value=%q 不该被解析成日期 (got %s)", bad, d)
		}
	}
}

// ===== 展开 / 收起 =====

func TestDatepickerOpensPopupBelowField(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, _ := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })

	if got := countTag(a.rootNode(), "datepicker-popup"); got != 0 {
		t.Fatalf("未展开时不该有弹层节点, got %d", got)
	}
	a.toggleDatepicker(dp)
	a.redraw()

	if !dp.expanded || dp.popup == nil {
		t.Fatalf("展开后应持有弹层: expanded=%v popup=%v", dp.expanded, dp.popup)
	}
	if dp.popup.Tag != "datepicker-popup" {
		t.Fatalf("弹层标签 = %q", dp.popup.Tag)
	}
	if dp.popup.Parent != dp {
		t.Fatalf("弹层的 Parent 必须是字段 (命中/焦点回收沿链回查)")
	}
	// 贴字段正下方, 且不比日历自身的最小宽度窄
	if dp.popup.Box.Y != dp.Box.Y+dp.Box.H {
		t.Fatalf("弹层 Y = %d, want %d (字段下缘)", dp.popup.Box.Y, dp.Box.Y+dp.Box.H)
	}
	if dp.popup.Box.X != dp.Box.X {
		t.Fatalf("弹层 X = %d, want %d (与字段左对齐)", dp.popup.Box.X, dp.Box.X)
	}
	wantW := 2*dpPopupPad + 7*dpCellW
	if dp.popup.Box.W < wantW {
		t.Fatalf("弹层宽 %d 小于 7 格所需 %d", dp.popup.Box.W, wantW)
	}
	// 2026-11: 11月1日是周日 ⇒ 无前置空格, 30 天 ⇒ 5 行
	wantH := 2*dpPopupPad + dpHeadH + dpWeekH + 5*dpCellH + dpReadoutH
	if dp.popup.Box.H != wantH {
		t.Fatalf("弹层高 = %d, want %d", dp.popup.Box.H, wantH)
	}
}

func TestDatepickerCursorStartsAtValueOrToday(t *testing.T) {
	pinToday(t, 2026, 5, 7)

	_, dp, a, _ := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2024-02-29") })
	a.openDatepicker(dp)
	if got := dp.dateCursor(); got != (dateParts{2024, 2, 29}) {
		t.Fatalf("有值时初始光标 = %v, want 2024-02-29", got)
	}

	_, dp2, a2, _ := mkDatepickerTree(t, nil)
	a2.openDatepicker(dp2)
	if got := dp2.dateCursor(); got != (dateParts{2026, 5, 7}) {
		t.Fatalf("无值时初始光标 = %v, want 今天 2026-05-07", got)
	}
}

func TestDatepickerDisabledDoesNotOpen(t *testing.T) {
	_, dp, a, _ := mkDatepickerTree(t, func(n *GuiNode) { withBool(n, "disabled", true) })
	a.toggleDatepicker(dp)
	if dp.expanded {
		t.Fatalf("禁用字段不该展开")
	}
}

func TestDatepickerOpenClosesOtherPopupFields(t *testing.T) {
	// select 与 datepicker 共用一个状态机: 打开日历必须收掉开着的下拉,
	// 否则两个弹层会同时盖在界面上 (点外面收起也只会收掉一个)。
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := &GuiNode{Tag: "select", Props: map[string]object.Value{}}
	sel.Props["options"] = object.NewArray([]object.Value{
		object.NewString("a"), object.NewString("b"),
	})
	attachSelectHandler(sel)
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	attachDatepickerHandler(dp)
	mountChildren(root, sel, dp)
	_, a := mountTestApp(t, root, 320, 400)

	a.openSelect(sel)
	if !sel.expanded {
		t.Fatalf("前置条件: 下拉该已展开")
	}
	a.toggleDatepicker(dp)
	if sel.expanded {
		t.Fatalf("打开日历应收掉已展开的下拉")
	}
	if !dp.expanded {
		t.Fatalf("日历该已展开")
	}
}

func TestDatepickerOutsideClickClosesPopup(t *testing.T) {
	_, dp, a, _ := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.toggleDatepicker(dp)
	a.redraw()

	// 点字段右侧的空白 (弹层之外)
	a.handleMouseDown(dp.Box.X+dp.Box.W+40, dp.Box.Y)
	if dp.expanded {
		t.Fatalf("点弹层之外应收起日历")
	}
	if dp.popup != nil {
		t.Fatalf("收起后弹层引用必须断开 (否则下一帧还会被布局)")
	}
	if got := countTag(a.rootNode(), "datepicker-popup"); got != 0 {
		t.Fatalf("收起后弹层该已从树上摘掉, got %d", got)
	}
}

func TestDatepickerEscapeClosesPopup(t *testing.T) {
	_, dp, a, fake := mkDatepickerTree(t, nil)
	a.toggleDatepicker(dp)
	a.redraw()
	if !dp.expanded {
		t.Fatalf("前置条件: 日历该已展开")
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})
	if dp.expanded {
		t.Fatalf("Esc 应收起日历")
	}
}

// ===== 点选 =====

func TestDatepickerClickSelectsDay(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	got := recordDates(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	// 2026-11-01 是周日 ⇒ 前置空格 0 (单独硬编码: 不调 dateWeekday)
	x, y := dateCellCenter(dp, 0, 3)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: x, Y: y})

	if len(*got) != 1 || (*got)[0] != "2026-11-03" {
		t.Fatalf("点第 3 天该派发 2026-11-03, got %v", *got)
	}
	if dp.expanded {
		t.Fatalf("选中后该收起弹层")
	}
	if a.focused != dp {
		t.Fatalf("焦点该回收给字段, got %v", a.focused)
	}
	// 受控: 派发不等于改值 (脚本没回写 signal, 显示还是旧值)
	if v, _ := dp.dateText(); v != "2026-11-15" {
		t.Fatalf("受控组件不该自己改值, got %q", v)
	}
}

func TestDatepickerClickOnSelectedDayIsNoop(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	got := recordDates(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	x, y := dateCellCenter(dp, 0, 15)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if len(*got) != 0 {
		t.Fatalf("点当前值不该派发 onChange (值没变), got %v", *got)
	}
	if dp.expanded {
		// 仍然收起: 用户的动作是"选这一天", 结果就是选定 —— 弹层没理由留着
		t.Fatalf("点当前值也该收起弹层 (用户已完成选择)")
	}
	if a.focused != dp {
		t.Fatalf("焦点该回收给字段, got %v", a.focused)
	}
}

func TestDatepickerClickOnBlankCellDoesNothing(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	got := recordDates(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	// 2026-11 只有 30 天且首日无空格 ⇒ 第 6 行 (row=5) 整行都是月外空格
	b := dp.popup.Box
	x := b.X + dpPopupPad + 3*dpCellW + dpCellW/2
	y := b.Y + dpPopupPad + dpHeadH + dpWeekH + 5*dpCellH + dpCellH/2
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})

	if len(*got) != 0 {
		t.Fatalf("点月外空格不该选中任何日期, got %v", *got)
	}
	if !dp.expanded {
		t.Fatalf("点月外空格不该收起弹层 (还在同一个日历里操作)")
	}
}

func TestDatepickerNavArrowsSwitchMonth(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.toggleDatepicker(dp)
	a.redraw()

	l, ok := dateLayoutOf(dp)
	if !ok {
		t.Fatalf("弹层几何应可用")
	}
	// 点右侧箭头 (下个月)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: l.next.X + 2, Y: l.next.Y + 2})
	if got := dp.dateCursor(); got != (dateParts{2026, 12, 15}) {
		t.Fatalf("点右箭头后光标 = %v, want 2026-12-15", got)
	}
	// 点左侧箭头两次 (回到 10 月)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: l.prev.X + 2, Y: l.prev.Y + 2})
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: l.prev.X + 2, Y: l.prev.Y + 2})
	if got := dp.dateCursor(); got != (dateParts{2026, 10, 15}) {
		t.Fatalf("两次左箭头后光标 = %v, want 2026-10-15", got)
	}
}

// ===== 键盘 =====

func TestDatepickerArrowKeysMoveCursor(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.setFocus(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	press := func(key string) {
		pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: key})
	}
	press("ArrowRight")
	press("ArrowRight")
	if got := dp.dateCursor(); got != (dateParts{2026, 11, 17}) {
		t.Fatalf("两次 → 之后 = %v, want 2026-11-17", got)
	}
	press("ArrowUp") // 一周
	if got := dp.dateCursor(); got != (dateParts{2026, 11, 10}) {
		t.Fatalf("↑ 之后 = %v, want 2026-11-10", got)
	}
	press("ArrowLeft")
	if got := dp.dateCursor(); got != (dateParts{2026, 11, 9}) {
		t.Fatalf("← 之后 = %v, want 2026-11-09", got)
	}
}

func TestDatepickerArrowCrossesMonthBoundary(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-30") })
	a.setFocus(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	if got := dp.dateCursor(); got != (dateParts{2026, 12, 1}) {
		t.Fatalf("月末 → 应跨到 2026-12-01, got %v", got)
	}
	// 视图月份跟着光标走 ⇒ 弹层行数按 12 月算 (2026-12-01 是周二 ⇒ 5 行)
	wantH := 2*dpPopupPad + dpHeadH + dpWeekH + 5*dpCellH + dpReadoutH
	if dp.popup.Box.H != wantH {
		t.Fatalf("弹层高应随月份重算: got %d, want %d", dp.popup.Box.H, wantH)
	}
}

func TestDatepickerPageAndHomeEndKeys(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.setFocus(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	press := func(key string) { pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: key}) }

	press("PageDown")
	if got := dp.dateCursor(); got != (dateParts{2026, 12, 15}) {
		t.Fatalf("PageDown = %v, want 2026-12-15", got)
	}
	press("PageUp")
	press("PageUp")
	if got := dp.dateCursor(); got != (dateParts{2026, 10, 15}) {
		t.Fatalf("两次 PageUp = %v, want 2026-10-15", got)
	}
	press("Home")
	if got := dp.dateCursor(); got != (dateParts{2026, 10, 1}) {
		t.Fatalf("Home = %v, want 2026-10-01", got)
	}
	press("End")
	if got := dp.dateCursor(); got != (dateParts{2026, 10, 31}) {
		t.Fatalf("End = %v, want 2026-10-31", got)
	}
}

func TestDatepickerEnterOpensThenChooses(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	got := recordDates(dp)
	a.setFocus(dp)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if !dp.expanded {
		t.Fatalf("Enter 应展开日历")
	}
	a.redraw()
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if len(*got) != 1 || (*got)[0] != "2026-11-16" {
		t.Fatalf("Enter 应选中光标那天, got %v", *got)
	}
	if dp.expanded {
		t.Fatalf("选中后应收起")
	}
}

func TestDatepickerArrowOpensPopupWhenClosed(t *testing.T) {
	// 未展开时按方向键先进弹层: 否则这四个键在字段上完全静默, 键盘用户
	// 会以为控件不可用 (与 select 同款)。
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.setFocus(dp)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowDown"})
	if !dp.expanded {
		t.Fatalf("未展开时按方向键该展开")
	}
	if got := dp.dateCursor(); got != (dateParts{2026, 11, 15}) {
		t.Fatalf("首次展开不该顺手挪光标, got %v", got)
	}
}

func TestDatepickerCtrlArrowLeftGoesToShortcuts(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) { withStr(n, "value", "2026-11-15") })
	a.setFocus(dp)
	a.toggleDatepicker(dp)
	a.redraw()
	before := dp.dateCursor()

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft", Ctrl: true})
	if got := dp.dateCursor(); got != before {
		t.Fatalf("带 Ctrl 的方向键该留给脚本, 光标不该动: %v → %v", before, got)
	}
}

// ===== min / max =====

func TestDatepickerMinMaxBlocksSelection(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) {
		withStr(n, "value", "2026-11-15")
		withStr(n, "min", "2026-11-10")
		withStr(n, "max", "2026-11-20")
	})
	got := recordDates(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	// 范围外的第 5 天: 画出来但点了没反应
	x, y := dateCellCenter(dp, 0, 5)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if len(*got) != 0 {
		t.Fatalf("超出 min 的日期不该可选, got %v", *got)
	}
	// 范围内的第 18 天照常
	x, y = dateCellCenter(dp, 0, 18)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if len(*got) != 1 || (*got)[0] != "2026-11-18" {
		t.Fatalf("范围内日期该可选, got %v", *got)
	}
}

func TestDatepickerArrowStopsAtMinMax(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, fake := mkDatepickerTree(t, func(n *GuiNode) {
		withStr(n, "value", "2026-11-12")
		withStr(n, "min", "2026-11-10")
	})
	a.setFocus(dp)
	a.toggleDatepicker(dp)
	a.redraw()

	press := func(key string) { pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: key}) }
	for i := 0; i < 5; i++ {
		press("ArrowLeft")
	}
	// "停在原地"而不是钳到 min: 钳位会让连按一直给同一天, 用户按了没反应
	// 也不知道为什么; 停在 12 - 2 = 10 (再往前就越界了)。
	if got := dp.dateCursor(); got != (dateParts{2026, 11, 10}) {
		t.Fatalf("方向键应在 min 边缘停住, got %v, want 2026-11-10", got)
	}
}

// ===== 展开时不应破坏别的交互 =====

func TestDatepickerFocusReturnsOnClose(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	_, dp, a, _ := mkDatepickerTree(t, nil)
	a.toggleDatepicker(dp)
	if a.focused != dp {
		t.Fatalf("展开时焦点该在字段上, got %v", a.focused)
	}
	a.closePopupField(dp)
	if a.focused != dp {
		t.Fatalf("收起后焦点该仍在字段上, got %v", a.focused)
	}
	if dp.highlight != -1 {
		t.Fatalf("收起后弹层光标该复位, got %d", dp.highlight)
	}
}

// ===== 日期算术 (跨月/闰年/钳位) =====

func TestDateArithmetic(t *testing.T) {
	if got := dateDaysInMonth(2024, 2); got != 29 {
		t.Fatalf("2024-02 天数 = %d, want 29", got)
	}
	if got := dateDaysInMonth(2026, 2); got != 28 {
		t.Fatalf("2026-02 天数 = %d, want 28", got)
	}
	// 加月份时日子钳到目标月上限 (1-31 + 1 月 = 2-28, 而不是被归一化到 3-03)
	if got := dateAddMonths(2026, 1, 31, 1); got != (dateParts{2026, 2, 28}) {
		t.Fatalf("2026-01-31 +1 月 = %v, want 2026-02-28", got)
	}
	if got := dateAddMonths(2024, 1, 31, 1); got != (dateParts{2024, 2, 29}) {
		t.Fatalf("2024-01-31 +1 月 = %v, want 2024-02-29", got)
	}
	// 跨年
	if got := dateAddMonths(2026, 12, 15, 1); got != (dateParts{2027, 1, 15}) {
		t.Fatalf("2026-12-15 +1 月 = %v, want 2027-01-15", got)
	}
	if got := dateAddDays(2026, 12, 31, 1); got != (dateParts{2027, 1, 1}) {
		t.Fatalf("2026-12-31 +1 天 = %v, want 2027-01-01", got)
	}
	if got := dateAddDays(2027, 1, 1, -1); got != (dateParts{2026, 12, 31}) {
		t.Fatalf("2027-01-01 -1 天 = %v, want 2026-12-31", got)
	}
	// clamp 未设的一端不限
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(dp, "min", "2026-06-01")
	if got := dp.dateClamp(dateParts{2026, 1, 1}); got != (dateParts{2026, 6, 1}) {
		t.Fatalf("clamp 应抬到 min, got %v", got)
	}
	if got := dp.dateClamp(dateParts{2030, 1, 1}); got != (dateParts{2030, 1, 1}) {
		t.Fatalf("未设 max 时不该被钳, got %v", got)
	}
}

// ===== 像素 =====

func TestDatepickerPopupPaintsSelectedAndToday(t *testing.T) {
	pinToday(t, 2026, 11, 20)
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(dp, "value", "2026-11-03")
	attachDatepickerHandler(dp)
	root := mkNode("column", map[string]float64{"padding": 10})
	mountChildren(root, dp)
	renderTree(root, 320, 320)

	dp.setDateCursor(dateParts{2026, 11, 3}) // 光标 = 选中日, 避免"光标浅底"盖住选中色
	dp.expanded = true
	dp.popup = buildDatepickerPopup(dp)
	img := renderTree(root, 320, 320)

	if dp.popup.Box.W <= 0 {
		t.Fatalf("前置条件: 弹层该已布局")
	}
	// 选中日 (11-03) 的格子应以强调色填充
	cx, cy := dateCellCenter(dp, 0, 3)
	if got := img.RGBAAt(cx, cy); got != pxAccent {
		t.Fatalf("选中日格子中心 = %v, want %v (强调色)", got, pxAccent)
	}
	// 今天 (11-20) 只有描边, 不是填充: 用"强调色像素数"判断,
	// 不去看格子中心 —— 中心正好落在日期数字的笔画边缘上 (抗锯齿混出浅灰),
	// 拿它当"底色"断言会随字体渲染细节漂移。
	b := dp.popup.Box
	todayCell := Rect{
		X: b.X + dpPopupPad + 5*dpCellW, // 2026-11-20 是周五 ⇒ 第 5 列 (日=0)
		Y: b.Y + dpPopupPad + dpHeadH + dpWeekH + 2*dpCellH,
		W: dpCellW, H: dpCellH,
	}
	if n := countColor(img, todayCell, pxAccent); n == 0 {
		t.Fatalf("今天该有一圈强调色描边 (格子 %v)", todayCell)
	}
	// 既非选中也非今天的格子: 一个强调色像素都不该有
	plainCell := Rect{
		X: b.X + dpPopupPad + 4*dpCellW,
		Y: b.Y + dpPopupPad + dpHeadH + dpWeekH + 1*dpCellH,
		W: dpCellW, H: dpCellH,
	}
	if n := countColor(img, plainCell, pxAccent); n != 0 {
		t.Fatalf("普通日期格不该有强调色 (%d 个像素)", n)
	}
}

func TestDatepickerFieldPaintsCalendarIcon(t *testing.T) {
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	root := mkNode("column", nil)
	mountChildren(root, dp)
	img := renderTree(root, 240, 60)

	// 图标区应出现非白像素 (图标颜色是 colorInputEdge)
	iconBox := Rect{
		X: dp.Box.X + dp.Box.W - fieldPadX - dpFieldIconW,
		Y: dp.Box.Y + (dp.Box.H-dpFieldIconW)/2,
		W: dpFieldIconW, H: dpFieldIconW,
	}
	n := 0
	for y := iconBox.Y; y < iconBox.Y+iconBox.H; y++ {
		for x := iconBox.X; x < iconBox.X+iconBox.W; x++ {
			if img.RGBAAt(x, y) != pxWhite {
				n++
			}
		}
	}
	if n == 0 {
		t.Fatalf("字段右侧该画出日历图标")
	}
}

// ===== aria =====

func TestDatepickerA11yShape(t *testing.T) {
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(dp, "placeholder", "出生日期")

	if !dp.focusable() {
		t.Fatalf("datepicker 该进 Tab 序")
	}
	if got := dp.AriaRole(); got != "combobox" {
		t.Fatalf("role = %q, want combobox", got)
	}
	if got := dp.AriaName(); got != "出生日期" {
		t.Fatalf("无障碍名该回落到 placeholder, got %q", got)
	}
}
