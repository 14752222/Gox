package gfx

import (
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== T08 <colorpicker> =====
//
// 与 datepicker 同一套分层: 状态/几何层用独立算出的坐标 (不复用 colorLayoutFor),
// 像素层验色块填充与选中边框, aria 层验 role 与可聚焦。

// mkColorpickerTree 造 <column><colorpicker/></column> 并挂到假窗口上。
func mkColorpickerTree(t *testing.T, configure func(*GuiNode)) (*GuiNode, *GuiNode, *app, *fakeSurface) {
	t.Helper()
	root := mkNode("column", map[string]float64{"padding": 10})
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	if configure != nil {
		configure(cp)
	}
	attachColorpickerHandler(cp)
	mountChildren(root, cp)
	fake, a := mountTestApp(t, root, 320, 320)
	return root, cp, a, fake
}

// colorSwatchCenter 返回第 idx 个色块的中心 (窗口坐标)。
// 与 dateCellCenter 同一立场: 几何自己算一遍, 不复用实现里的私有布局。
func colorSwatchCenter(cp *GuiNode, idx int) (int, int) {
	b := cp.popup.Box
	col := idx % cp.colorColumns()
	row := idx / cp.colorColumns()
	x := b.X + cpPopupPad + col*(cpCellSize+cpCellGap) + cpCellSize/2
	y := b.Y + cpPopupPad + row*(cpCellSize+cpCellGap) + cpCellSize/2
	return x, y
}

// recordColors 挂一个 onChange, 把收到的 value 记下来。
func recordColors(cp *GuiNode) *[]string {
	var got []string
	cp.Props["onChange"] = object.NewBuiltin("recordColors", func(args ...object.Value) object.Value {
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

// twoColorPalette 是给几何断言用的最小色板 (2 色 2 列 ⇒ 1 行)。
func twoColorPalette(n *GuiNode) {
	n.Props["colors"] = object.NewArray([]object.Value{
		object.NewString("#ff0000"), object.NewString("#00ff00"),
	})
	withNum(n, "columns", 2)
}

// ===== value 形态 =====

func TestColorpickerValueShapes(t *testing.T) {
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}

	withStr(cp, "value", "#a1b2c3")
	if got, ok := cp.colorValue(); !ok || got != "#a1b2c3" {
		t.Fatalf("字符串 value 应原样返回, got %q ok=%v", got, ok)
	}
	// {r,g,b} 形态 (与 canvas 的 ImageData 同一套字段名)
	o := object.NewObject()
	o.SetProperty("r", object.NewNumber(255))
	o.SetProperty("g", object.NewNumber(0))
	o.SetProperty("b", object.NewNumber(128))
	cp.Props["value"] = o
	if got, ok := cp.colorValue(); !ok || got != "#ff0080" {
		t.Fatalf("{r,g,b} 应转成 #rrggbb, got %q ok=%v", got, ok)
	}
	// 空串 / 缺字段 / 非法值都当"没有值"
	cp.Props["value"] = object.NewString("")
	if _, ok := cp.colorValue(); ok {
		t.Fatalf("空串不该算有值")
	}
	bad := object.NewObject()
	bad.SetProperty("r", object.NewNumber(1))
	cp.Props["value"] = bad
	if _, ok := cp.colorValue(); ok {
		t.Fatalf("缺 g/b 的对象不该算有值")
	}
}

func TestColorpickerDefaultPalette(t *testing.T) {
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	if got := len(cp.colorPalette()); got != len(cpDefaultPalette) {
		t.Fatalf("缺省色板 = %d 色, want %d", got, len(cpDefaultPalette))
	}
	if got := cp.colorColumns(); got != cpDefaultColumns {
		t.Fatalf("缺省列数 = %d, want %d", got, cpDefaultColumns)
	}
	// 显式给 [] 是"我要一个空色板"(常见于色板还没加载完), 不该塞回缺省色
	cp.Props["colors"] = object.NewArray(nil)
	if got := len(cp.colorPalette()); got != 0 {
		t.Fatalf("显式空色板应保持空, got %d", got)
	}
}

// ===== 展开 / 收起 =====

func TestColorpickerOpensPopupBelowField(t *testing.T) {
	_, cp, a, _ := mkColorpickerTree(t, twoColorPalette)

	if got := countTag(a.rootNode(), "colorpicker-popup"); got != 0 {
		t.Fatalf("未展开时不该有弹层, got %d", got)
	}
	a.toggleColorpicker(cp)
	a.redraw()

	if !cp.expanded || cp.popup == nil || cp.popup.Tag != "colorpicker-popup" {
		t.Fatalf("展开后应持有色板弹层: expanded=%v popup=%v", cp.expanded, cp.popup)
	}
	if cp.popup.Parent != cp {
		t.Fatalf("弹层 Parent 必须是字段")
	}
	if cp.popup.Box.Y != cp.Box.Y+cp.Box.H || cp.popup.Box.X != cp.Box.X {
		t.Fatalf("弹层应贴字段正下方且左对齐: popup=%v field=%v", cp.popup.Box, cp.Box)
	}
	// 2 色 2 列 ⇒ 宽 = 2*pad + 2*cell + 1*gap, 高 = 2*pad + 1*cell + 回显行
	wantW := 2*cpPopupPad + 2*cpCellSize + cpCellGap
	wantH := 2*cpPopupPad + cpCellSize + cpReadoutH
	if cp.popup.Box.W < wantW || cp.popup.Box.H != wantH {
		t.Fatalf("弹层尺寸 = %dx%d, want >=%dx%d", cp.popup.Box.W, cp.popup.Box.H, wantW, wantH)
	}
}

func TestColorpickerCursorStartsAtCurrentValue(t *testing.T) {
	_, cp, a, _ := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#00ff00")
	})
	a.openColorpicker(cp)
	if cp.highlight != 1 {
		t.Fatalf("光标应落在当前值那格, got %d, want 1", cp.highlight)
	}

	// 值不在色板里 (或没有值) ⇒ 从第一格起
	_, cp2, a2, _ := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#123456")
	})
	a2.openColorpicker(cp2)
	if cp2.highlight != 0 {
		t.Fatalf("值不在色板里时光标该从 0 起, got %d", cp2.highlight)
	}
}

func TestColorpickerEmptyPaletteDoesNotOpen(t *testing.T) {
	_, cp, a, _ := mkColorpickerTree(t, func(n *GuiNode) {
		n.Props["colors"] = object.NewArray(nil)
	})
	a.toggleColorpicker(cp)
	if cp.expanded {
		t.Fatalf("空色板不该展开 (展开一个空盒子没有意义)")
	}
}

func TestColorpickerDisabledDoesNotOpen(t *testing.T) {
	_, cp, a, _ := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withBool(n, "disabled", true)
	})
	a.toggleColorpicker(cp)
	if cp.expanded {
		t.Fatalf("禁用字段不该展开")
	}
}

func TestColorpickerOutsideClickClosesPopup(t *testing.T) {
	_, cp, a, _ := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	a.toggleColorpicker(cp)
	a.redraw()
	a.handleMouseDown(cp.Box.X+cp.Box.W+50, cp.Box.Y)
	if cp.expanded || cp.popup != nil {
		t.Fatalf("点弹层之外应收起色板: expanded=%v popup=%v", cp.expanded, cp.popup)
	}
}

func TestColorpickerEscapeClosesPopup(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	a.setFocus(cp)
	a.toggleColorpicker(cp)
	a.redraw()
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})
	if cp.expanded {
		t.Fatalf("Esc 应收起色板")
	}
}

// ===== 点选 =====

func TestColorpickerClickSelectsSwatch(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	got := recordColors(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	x, y := colorSwatchCenter(cp, 1)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})

	if len(*got) != 1 || (*got)[0] != "#00ff00" {
		t.Fatalf("点第 2 个色块该派发 #00ff00, got %v", *got)
	}
	if cp.expanded {
		t.Fatalf("选中后应收起弹层")
	}
	if a.focused != cp {
		t.Fatalf("焦点该回收给字段, got %v", a.focused)
	}
	if cur, _ := cp.colorValue(); cur != "#ff0000" {
		t.Fatalf("受控组件不该自己改值, got %q", cur)
	}
}

func TestColorpickerClickSameColorIsNoop(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	got := recordColors(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	x, y := colorSwatchCenter(cp, 0)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if len(*got) != 0 {
		t.Fatalf("点当前值不该派发 onChange, got %v", *got)
	}
	if cp.expanded {
		t.Fatalf("仍应收起弹层")
	}
}

func TestColorpickerClickIsCaseInsensitive(t *testing.T) {
	// 脚本写 "#FFF" 与色板里的 "#ffffff" 是同一个颜色: 纯字符串比较会让
	// "点当前色"误派发一次 onChange (值其实没变)。
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		n.Props["colors"] = object.NewArray([]object.Value{object.NewString("#FFFFFF")})
		withNum(n, "columns", 1)
		withStr(n, "value", "#ffffff")
	})
	got := recordColors(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	if cp.highlight != 0 {
		t.Fatalf("大小写不该影响'当前值在色板里的位置', got %d", cp.highlight)
	}
	x, y := colorSwatchCenter(cp, 0)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if len(*got) != 0 {
		t.Fatalf("同一个颜色 (大小写不同) 不该派发 onChange, got %v", *got)
	}
}

func TestColorpickerClickInGapDoesNothing(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	got := recordColors(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	// 两个色块之间的 4px 缝隙: 属于弹层但不在任何格子里
	b := cp.popup.Box
	x := b.X + cpPopupPad + cpCellSize + cpCellGap/2
	y := b.Y + cpPopupPad + cpCellSize/2
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})

	if len(*got) != 0 {
		t.Fatalf("点在缝隙里不该选中颜色, got %v", *got)
	}
	if !cp.expanded {
		t.Fatalf("点在弹层内的空白处不该收起弹层")
	}
}

// ===== 键盘 =====

func TestColorpickerArrowKeysMoveCursor(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		n.Props["colors"] = object.NewArray([]object.Value{
			object.NewString("#111111"), object.NewString("#222222"),
			object.NewString("#333333"), object.NewString("#444444"),
		})
		withNum(n, "columns", 2)
	})
	a.setFocus(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	press := func(key string) { pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: key}) }
	press("ArrowRight")
	if cp.highlight != 1 {
		t.Fatalf("→ 之后光标 = %d, want 1", cp.highlight)
	}
	press("ArrowDown") // 列数 = 2 ⇒ 下移两格
	if cp.highlight != 3 {
		t.Fatalf("↓ 之后光标 = %d, want 3", cp.highlight)
	}
	// 已到末格: 停住而非绕回 (色板是二维的, 绕回会让"往下按"跳到最上面)
	press("ArrowDown")
	if cp.highlight != 3 {
		t.Fatalf("末格再按下箭头该停住, got %d", cp.highlight)
	}
	press("ArrowUp")
	if cp.highlight != 1 {
		t.Fatalf("↑ 之后光标 = %d, want 1", cp.highlight)
	}
}

func TestColorpickerHomeEndKeys(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		n.Props["colors"] = object.NewArray([]object.Value{
			object.NewString("#111111"), object.NewString("#222222"), object.NewString("#333333"),
		})
		withNum(n, "columns", 3)
	})
	a.setFocus(cp)
	a.toggleColorpicker(cp)
	a.redraw()

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "End"})
	if cp.highlight != 2 {
		t.Fatalf("End 该到末格, got %d", cp.highlight)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Home"})
	if cp.highlight != 0 {
		t.Fatalf("Home 该到首格, got %d", cp.highlight)
	}
}

func TestColorpickerEnterOpensThenChooses(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) {
		twoColorPalette(n)
		withStr(n, "value", "#ff0000")
	})
	got := recordColors(cp)
	a.setFocus(cp)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if !cp.expanded {
		t.Fatalf("Enter 应展开色板")
	}
	a.redraw()
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: " "})
	if len(*got) != 1 || (*got)[0] != "#00ff00" {
		t.Fatalf("空格应选中光标那格, got %v", *got)
	}
	if cp.expanded {
		t.Fatalf("选中后应收起")
	}
}

func TestColorpickerCtrlArrowLeftGoesToShortcuts(t *testing.T) {
	_, cp, a, fake := mkColorpickerTree(t, func(n *GuiNode) { twoColorPalette(n) })
	a.setFocus(cp)
	a.toggleColorpicker(cp)
	a.redraw()
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft", Ctrl: true})
	if cp.highlight != 0 {
		t.Fatalf("带 Ctrl 的方向键该留给脚本, got %d", cp.highlight)
	}
}

// ===== 像素 =====

func TestColorpickerPopupPaintsSwatchesAndSelection(t *testing.T) {
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	twoColorPalette(cp)
	withStr(cp, "value", "#ff0000")
	attachColorpickerHandler(cp)
	root := mkNode("column", map[string]float64{"padding": 10})
	mountChildren(root, cp)
	renderTree(root, 320, 320)

	cp.expanded = true
	cp.popup = buildColorpickerPopup(cp)
	cp.highlight = 1
	img := renderTree(root, 320, 320)

	if cp.popup.Box.W <= 0 {
		t.Fatalf("前置条件: 弹层该已布局")
	}
	red := color.RGBA{R: 0xff, A: 0xff}
	green := color.RGBA{G: 0xff, A: 0xff}
	cx, cy := colorSwatchCenter(cp, 0)
	if got := img.RGBAAt(cx, cy); got != red {
		t.Fatalf("第 1 格中心 = %v, want %v", got, red)
	}
	cx, cy = colorSwatchCenter(cp, 1)
	if got := img.RGBAAt(cx, cy); got != green {
		t.Fatalf("第 2 格中心 = %v, want %v", got, green)
	}
	// 选中格 (当前值 = 第 0 格) 有一圈 focusRing; 光标格 (第 1 格) 有一圈强调色
	b := cp.popup.Box
	cell0 := Rect{X: b.X + cpPopupPad, Y: b.Y + cpPopupPad, W: cpCellSize, H: cpCellSize}
	cell1 := Rect{X: cell0.X + cpCellSize + cpCellGap, Y: cell0.Y, W: cpCellSize, H: cpCellSize}
	if n := countColor(img, cell0, pxRing); n == 0 {
		t.Fatalf("当前值那格该有 focusRing 边框")
	}
	if n := countColor(img, cell1, pxAccent); n == 0 {
		t.Fatalf("键盘光标那格该有强调色边框")
	}
}

func TestColorpickerFieldPaintsSwatchFrameWhenNoValue(t *testing.T) {
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	root := mkNode("column", nil)
	mountChildren(root, cp)
	img := renderTree(root, 240, 60)

	// 没有值: 色块只剩一个空框 (画当前值解析出的颜色; 解析不出来就不填)
	sw := Rect{
		X: cp.Box.X + fieldPadX,
		Y: cp.Box.Y + (cp.Box.H-cpSwatchSize)/2,
		W: cpSwatchSize, H: cpSwatchSize,
	}
	if n := countColor(img, sw, pxWhite); n == 0 {
		t.Fatalf("没有颜色值时色块内部应保持底色 (画一个空框)")
	}
	if n := countColor(img, sw, pxInputEdge); n == 0 {
		t.Fatalf("色块该有边框")
	}
	if got := cp.colorPlaceholder(); got != "选择颜色" {
		t.Fatalf("缺省 placeholder = %q", got)
	}
}

// ===== aria =====

func TestColorpickerA11yShape(t *testing.T) {
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	withStr(cp, "placeholder", "主题色")
	if !cp.focusable() {
		t.Fatalf("colorpicker 该进 Tab 序")
	}
	if got := cp.AriaRole(); got != "combobox" {
		t.Fatalf("role = %q, want combobox", got)
	}
	if got := cp.AriaName(); got != "主题色" {
		t.Fatalf("无障碍名该回落到 placeholder, got %q", got)
	}
}

// ===== 十六进制工具 =====

func TestHexOfColor(t *testing.T) {
	if got := hexOfColor(0, 0, 0); got != "#000000" {
		t.Fatalf("got %q", got)
	}
	if got := hexOfColor(255, 255, 255); got != "#ffffff" {
		t.Fatalf("got %q", got)
	}
	if got := hexOfColor(0x0a, 0xb1, 0xc2); got != "#0ab1c2" {
		t.Fatalf("got %q", got)
	}
}
