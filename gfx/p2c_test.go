package gfx

import (
	"testing"
	"time"
)

// P2-1 单行文本输入 <input>。
//
// 断言分两层:
//   - 脱 VM 层 (mkNode/renderTree/mountTestApp): 尺寸、外观、光标像素、
//     按键消费与光标位置。输入框的光标位置是节点上的运行时字段, 不依赖
//     回调桥, 所以这一层能直接断言。
//   - 全链路层 (runDemoSteps + input_demo.js): 受控语义 —— 只有真 VM 在
//     场, object.CallFunction 才会真的执行 onInput 并写回 signal
//     (无 VM 时回调桥一律返回 undefined, 见 object/callback.go)。

var (
	pxCaret       = colorText
	pxPlaceholder = colorPlaceholder
	pxFieldHover  = colorFieldHover
)

// mkInput 造一个带 value 的输入框 (mkNode 只写数值属性)。
func mkInput(value string) *GuiNode {
	n := mkNode("input", nil)
	if value != "" {
		withStr(n, "value", value)
	}
	return n
}

// ===== 尺寸 =====

func TestInputDefaultGeometryAndOverrides(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	mountChildren(root, in)
	Layout(root, 400, 300)

	if in.Box.W != inputMinW || in.Box.H != selectRowH {
		t.Fatalf("input 缺省尺寸 = %dx%d, want %dx%d",
			in.Box.W, in.Box.H, inputMinW, selectRowH)
	}

	// 输入框不参与交叉轴 stretch: 宽度按固定缺省值, 不做"随文字变长"的
	// 内容测量 (那样打字时整行都在抖)。
	root2 := mkNode("column", nil)
	in2 := mkInput("hi")
	mountChildren(root2, in2)
	Layout(root2, 400, 300)
	if in2.Box.W != inputMinW {
		t.Fatalf("input 在 column 里被拉伸成 %d, 应保持缺省 %d", in2.Box.W, inputMinW)
	}

	// 显式尺寸优先
	root3 := mkNode("column", nil)
	in3 := mkNode("input", map[string]float64{"width": 240, "height": 40})
	mountChildren(root3, in3)
	Layout(root3, 400, 300)
	if in3.Box.W != 240 || in3.Box.H != 40 {
		t.Fatalf("input 显式尺寸未生效: %v", in3.Box)
	}
}

// ===== 外观 =====

func TestInputFaceBorderAndFocusColor(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	mountChildren(root, in)

	img := renderTree(root, 300, 160)
	if got := img.RGBAAt(in.Box.X+3, in.Box.Y+3); got != pxWhite {
		t.Fatalf("未获焦底色 = %v, want 白底", got)
	}
	if got := img.RGBAAt(in.Box.X, in.Box.Y+1); got != pxInputEdge {
		t.Fatalf("未获焦边框 = %v, want %v", got, pxInputEdge)
	}

	// 获焦: 边框换强调蓝 (光标像素另有用例)
	in.focused = true
	img2 := renderTree(root, 300, 160)
	if got := img2.RGBAAt(in.Box.X, in.Box.Y+1); got != colorFocusRing {
		t.Fatalf("获焦边框 = %v, want %v", got, colorFocusRing)
	}
}

func TestInputHoverTintUsesFieldFace(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	mountChildren(root, in)
	Layout(root, 300, 160)

	in.hovered = true
	img := renderTree(root, 300, 160)
	if got := img.RGBAAt(in.Box.X+3, in.Box.Y+3); got != pxFieldHover {
		t.Fatalf("悬停底色 = %v, want %v", got, pxFieldHover)
	}
	in.hovered = false
}

// ===== 光标闪烁相位 =====

func TestCaretVisiblePhaseFlipsEvery500ms(t *testing.T) {
	base := time.Now()
	resetCaretPhase(base)

	visibleAt := func(d time.Duration) bool { return caretVisibleAt(base.Add(d)) }
	cases := []struct {
		at   time.Duration
		want bool
	}{
		{0, true},
		{499 * time.Millisecond, true},
		{500 * time.Millisecond, false},
		{999 * time.Millisecond, false},
		{1000 * time.Millisecond, true},
		{1500 * time.Millisecond, false},
	}
	for _, c := range cases {
		if got := visibleAt(c.at); got != c.want {
			t.Fatalf("caretVisibleAt(+%v) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestInputCaretPixelTracksFocusPhaseAndPosition(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	mountChildren(root, in)

	// 未获焦: 不画光标
	resetCaretPhase(time.Now())
	img := renderTree(root, 300, 160)
	cx, cy := in.Box.X+fieldPadX, in.Box.Y+in.Box.H/2
	if got := img.RGBAAt(cx, cy); got != pxWhite {
		t.Fatalf("未获焦画了光标: %v", got)
	}

	// 获焦 + 可见相: 光标是 1px 竖线, 颜色同文字色
	in.focused = true
	resetCaretPhase(time.Now())
	img = renderTree(root, 300, 160)
	if got := img.RGBAAt(cx, cy); got != pxCaret {
		t.Fatalf("获焦可见相光标 = %v, want %v", got, pxCaret)
	}
	// 光标只占 1px 宽: 右边一格不该被染上
	if got := img.RGBAAt(cx+1, cy); got != pxWhite {
		t.Fatalf("光标宽度超过 1px: (cx+1) = %v", got)
	}

	// 获焦 + 不可见相: 同一像素回到底色
	resetCaretPhase(time.Now().Add(-600 * time.Millisecond))
	img = renderTree(root, 300, 160)
	if got := img.RGBAAt(cx, cy); got != pxWhite {
		t.Fatalf("不可见相仍画了光标: %v", got)
	}
}

func TestInputDisabledDrawsNoCaretAndKeepsIdleBorder(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	withBool(in, "disabled", true)
	mountChildren(root, in)

	in.focused = true
	resetCaretPhase(time.Now())
	img := renderTree(root, 300, 160)

	if got := img.RGBAAt(in.Box.X, in.Box.Y+1); got != dim(pxInputEdge) {
		t.Fatalf("禁用边框 = %v, want %v (不随焦点变蓝)", got, dim(pxInputEdge))
	}
	if got := img.RGBAAt(in.Box.X+fieldPadX, in.Box.Y+in.Box.H/2); got != dim(pxWhite) {
		t.Fatalf("禁用态画了光标: %v", got)
	}
}

// ===== 文本与 placeholder =====

func TestInputTextFallsBackToPlaceholder(t *testing.T) {
	in := mkInput("Ada")
	withStr(in, "placeholder", "Type here")
	if text, c := in.inputText(); text != "Ada" || c != colorText {
		t.Fatalf("有值时应显示真值: %q / %v", text, c)
	}
	if !in.hasInputValue() {
		t.Fatalf("有值时 hasInputValue 应为 true")
	}

	empty := mkInput("")
	withStr(empty, "placeholder", "Type here")
	if text, c := empty.inputText(); text != "Type here" || c != colorPlaceholder {
		t.Fatalf("空值应退回 placeholder: %q / %v", text, c)
	}
	if empty.hasInputValue() {
		t.Fatalf("空值时 hasInputValue 应为 false (光标不能被灰字挤走)")
	}
}

// ===== 按键消费与光标移动 (脱 VM) =====

func TestInputCaretMovementKeys(t *testing.T) {
	in := mkInput("hello")
	a := &app{}

	press := func(key string) bool {
		return a.handleInputKey(in, key, Event{Key: key})
	}

	if in.caret != 0 {
		t.Fatalf("初始光标 = %d, want 0", in.caret)
	}
	press("ArrowRight")
	if in.caret != 1 {
		t.Fatalf("ArrowRight 后 = %d, want 1", in.caret)
	}
	press("ArrowLeft")
	if in.caret != 0 {
		t.Fatalf("ArrowLeft 后 = %d, want 0", in.caret)
	}
	// 已在最左再按左: 不越界
	press("ArrowLeft")
	if in.caret != 0 {
		t.Fatalf("最左再按 ArrowLeft 越界到 %d", in.caret)
	}
	press("End")
	if in.caret != 5 {
		t.Fatalf("End 后 = %d, want 5", in.caret)
	}
	// 已在最右再按右: 不越界
	press("ArrowRight")
	if in.caret != 5 {
		t.Fatalf("最右再按 ArrowRight 越界到 %d", in.caret)
	}
	press("Home")
	if in.caret != 0 {
		t.Fatalf("Home 后 = %d, want 0", in.caret)
	}
	// Backspace 在行首无事发生 (也不该退到负下标)
	press("Backspace")
	if in.caret != 0 {
		t.Fatalf("行首 Backspace 后 = %d, want 0", in.caret)
	}
}

func TestInputCaretClampedWhenValueShrinks(t *testing.T) {
	// 受控值由 JS 决定: 外部把值改短后, 旧的光标位置必须被钳回范围内,
	// 否则下一次插入会 panic (slice 越界)。
	in := mkInput("abc")
	a := &app{}
	a.handleInputKey(in, "End", Event{Key: "End"})
	if in.caret != 3 {
		t.Fatalf("caret = %d, want 3", in.caret)
	}
	withStr(in, "value", "a") // 外部改短
	if got := in.caretIndex([]rune(in.inputValue())); got != 1 {
		t.Fatalf("钳位后 caret = %d, want 1", got)
	}
	// 钳位之后照常能编辑, 不 panic
	if !a.handleInputKey(in, "Backspace", Event{Key: "Backspace"}) {
		t.Fatalf("Backspace 未被消费")
	}
}

func TestInputKeyConsumption(t *testing.T) {
	in := mkInput("x")
	a := &app{}

	// 键名是"一个字符"; 多字符的键名一律按具名键处理 (见 printableRune)。
	consumed := []string{"a", "Z", "7", " ", "!", "中"}
	for _, k := range consumed {
		if !a.handleInputKey(in, k, Event{Key: k}) {
			t.Fatalf("可打印字符 %q 应被输入框消费", k)
		}
	}
	// Enter / Escape / Tab / 功能键 / 修饰键组合一律放行给上层:
	// "输入框放在对话框里按 Esc 关掉"依赖这一点。
	passed := []struct {
		key string
		ev  Event
	}{
		{"Enter", Event{Key: "Enter"}},
		{"Escape", Event{Key: "Escape"}},
		{"Tab", Event{Key: "Tab"}},
		{"F1", Event{Key: "F1"}},
		{"Shift", Event{Key: "Shift"}},
		{"c", Event{Key: "c", Ctrl: true}},
		{"a", Event{Key: "a", Alt: true}},
	}
	for _, p := range passed {
		if a.handleInputKey(in, p.key, p.ev) {
			t.Fatalf("按键 %q (ctrl=%v alt=%v) 不该被输入框消费",
				p.key, p.ev.Ctrl, p.ev.Alt)
		}
	}
}

func TestInputUnicodeKeyInsertsWholeRune(t *testing.T) {
	// BMP 直输: 键名就是一个字符 (长度按 rune 算, 不能按 byte)。
	in := mkInput("")
	a := &app{}
	a.handleInputKey(in, "中", Event{Key: "中"})
	if in.caret != 1 {
		t.Fatalf("插入一个汉字后 caret = %d, want 1 (按 rune 计数)", in.caret)
	}
	if r, ok := printableRune("中"); !ok || r != '中' {
		t.Fatalf("printableRune(中文) = %q, %v", r, ok)
	}
	if _, ok := printableRune("Enter"); ok {
		t.Fatalf("具名键不该被当成可打印字符")
	}
	if _, ok := printableRune("\x1b"); ok {
		t.Fatalf("控制字符不该被当成可打印字符")
	}
}

// ===== 点击获焦与光标定位 =====

func TestInputClickFocusesAndPlacesCaret(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("abc")
	mountChildren(root, in)
	fake, a := mountTestApp(t, root, 300, 160)

	// 输入框表面没有 onClick 处理器, 常规命中测试会判成"点到空白";
	// 字段类组件必须能靠"最深命中节点"这条路拿到焦点。
	if HitTest(root, in.Box.X+20, in.Box.Y+in.Box.H/2) != nil {
		t.Fatalf("用例前提不成立: input 不该有 onClick 处理器")
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: in.Box.X + 2, Y: in.Box.Y + in.Box.H/2})

	if a.focused != in {
		t.Fatalf("点击后焦点 = %v, want input", a.focused)
	}
	if !in.focused {
		t.Fatalf("点击后节点的 focused 标记未置位 (绘制要靠它)")
	}
	if in.caret != 0 {
		t.Fatalf("点最左侧 caret = %d, want 0", in.caret)
	}

	// 点最右侧 → 光标到末尾
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: in.Box.X + in.Box.W - 1, Y: in.Box.Y + in.Box.H/2})
	if in.caret != 3 {
		t.Fatalf("点最右侧 caret = %d, want 3", in.caret)
	}

	// 单调性: 点得越靠右, 光标不会反而更靠左
	mid := in.Box.X + in.Box.W/2
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: mid, Y: in.Box.Y + in.Box.H/2})
	if in.caret < 0 || in.caret > 3 {
		t.Fatalf("中间点击 caret = %d, 越界", in.caret)
	}
}

func TestInputDisabledClickDoesNotFocus(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("abc")
	withBool(in, "disabled", true)
	mountChildren(root, in)
	fake, a := mountTestApp(t, root, 300, 160)

	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: in.Box.X + 20, Y: in.Box.Y + in.Box.H/2})

	if a.focused == in || in.focused {
		t.Fatalf("禁用输入框不该获得焦点")
	}
}

func TestInputFocusHandoffClearsOldFlag(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	a1 := mkInput("one")
	a2 := mkInput("two")
	mountChildren(root, a1, a2)
	fake, app := mountTestApp(t, root, 300, 200)

	pushAndPump(t, fake, app, Event{Kind: EventMouseUp, X: a1.Box.X + 4, Y: a1.Box.Y + a1.Box.H/2})
	if !a1.focused {
		t.Fatalf("第一个输入框未获焦")
	}
	pushAndPump(t, fake, app, Event{Kind: EventMouseUp, X: a2.Box.X + 4, Y: a2.Box.Y + a2.Box.H/2})
	if a1.focused {
		t.Fatalf("焦点交接后旧节点仍标记为 focused (会让旧框继续画蓝边)")
	}
	if !a2.focused {
		t.Fatalf("第二个输入框未获焦")
	}
}

// ===== 全链路 (真 VM): 受控写回 =====

func TestInputDemoTypingMirrorsValueAndCaret(t *testing.T) {
	runDemoSteps(t, "input_demo.js", []func(*GuiNode, *fakeSurface){
		// 1) 点输入框获焦
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			fake.push(Event{Kind: EventMouseUp, X: in.Box.X + 4, Y: in.Box.Y + in.Box.H/2})
		},
		// 2) 断言获焦, 然后敲两个字母
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if !in.focused {
				t.Fatalf("点击后 input 未获焦")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "h"})
			fake.push(Event{Kind: EventKeyDown, Key: "i"})
		},
		// 3) 镜像文本应已跟随 (受控: 值来自 signal 回写), caret = 2
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if !textContainsAny(root, `name = "hi"`) {
				t.Fatalf("镜像文本未跟随输入: value=%q", in.inputValue())
			}
			if in.caret != 2 {
				t.Fatalf("输入两个字符后 caret = %d, want 2", in.caret)
			}
			// 4) 退格一次
			fake.push(Event{Kind: EventKeyDown, Key: "Backspace"})
		},
		// 5) 值变成 "h", caret = 1
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if !textContainsAny(root, `name = "h"`) || textContainsAny(root, `name = "hi"`) {
				t.Fatalf("Backspace 后 value=%q", in.inputValue())
			}
			if in.caret != 1 {
				t.Fatalf("Backspace 后 caret = %d, want 1", in.caret)
			}
			// 6) Home 回到行首, 再 Delete 删掉第一个字符
			fake.push(Event{Kind: EventKeyDown, Key: "Home"})
		},
		// 7) caret = 0, Delete 掉 'h'
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if in.caret != 0 {
				t.Fatalf("Home 后 caret = %d, want 0", in.caret)
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Delete"})
		},
		// 8) 值清空 (回到 placeholder 态), 再敲 "ok"
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if in.inputValue() != "" {
				t.Fatalf("Delete 后 value = %q, want 空", in.inputValue())
			}
			if !textContainsAny(root, `name = ""`) {
				t.Fatalf("镜像文本未反映空值: value=%q", in.inputValue())
			}
			fake.push(Event{Kind: EventKeyDown, Key: "o"})
			fake.push(Event{Kind: EventKeyDown, Key: "k"})
		},
		// 9) Enter 不被输入框消费 → 冒泡到 onKeyDown 计数, 且不改动文本
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if !textContainsAny(root, `name = "ok"`) {
				t.Fatalf("连续输入后 value = %q, want \"ok\"", in.inputValue())
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Enter"})
		},
		// 10) Enter 计数生效, 文本未被 Enter 改动
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if in.inputValue() != "ok" {
				t.Fatalf("Enter 改动了文本: %q", in.inputValue())
			}
			if !textContainsAny(root, "enter presses = 1") {
				t.Fatalf("Enter 未冒泡到 onKeyDown (未计数)")
			}
		},
	})
}

func TestInputDemoEscapeIsNotConsumedByField(t *testing.T) {
	// Esc 不被输入框吃掉: 焦点在输入框里按 Esc 仍能一路走到 onKeyDown
	// (演示脚本没有 Esc 处理, 这里只断言"没有崩且值不变")。
	runDemoSteps(t, "input_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			fake.push(Event{Kind: EventMouseUp, X: in.Box.X + 4, Y: in.Box.Y + in.Box.H/2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "Escape"})
		},
		func(root *GuiNode, fake *fakeSurface) {
			in := findFirst(root, "input")
			if in.inputValue() != "" {
				t.Fatalf("Esc 改动了文本: %q", in.inputValue())
			}
			if !in.focused {
				t.Fatalf("Esc 不该让输入框丢焦点")
			}
		},
	})
}
