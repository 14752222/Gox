package gfx

import (
	"testing"
	"unicode/utf16"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P2-7: 输入法 (IME) =====
//
// 分两层验:
//   - 纯函数 + 纯 Go 挂载 (mountTestApp + Go 侧 builtin 回调) 验**插入语义**:
//     整批插入、光标跨过整批、代理对不被切开、textarea 的二维光标折算、
//     一次提交只派发一次 onInput。这些不需要真输入法。
//   - 全链路 (真 VM + 真脚本 signal) 验**受控回写**: 提交的文本要经
//     onInput → setText → effect 写回 value prop, 与手敲键盘完全同路。
//
// 拿不到真输入法也没关系: 平台后端只负责"把结果串投递成一条
// EventIMECommit", 测试直接推这条事件, 等价于用户选定了候选词。

// imeRecorder 记下每次 onInput 派发的 value, 并像 JS 那样写回受控 prop
// (受控模型: 显示内容只来自 prop, 回调不写回就永远是旧值)。
func imeRecorder(t *testing.T, n *GuiNode, got *[]string) *GuiNode {
	n.Props["onInput"] = object.NewBuiltin("onInput", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			t.Fatalf("onInput 的参数不是对象: %s", args[0].Type())
			return object.UndefinedSingleton
		}
		pv, _ := obj.GetProperty("value")
		v := valueText(pv)
		*got = append(*got, v)
		n.Props["value"] = object.NewString(v)
		return object.UndefinedSingleton
	})
	return n
}

// TestIMESplice 整批插入: 按 rune 计数、越界钳位、代理对不被切开。
func TestIMESplice(t *testing.T) {
	cases := []struct {
		text, ins string
		off       int
		want      string
		wantOff   int
	}{
		{"ac", "你好", 1, "a你好c", 3},  // 插在中间
		{"", "你好", 0, "你好", 2},      // 空值
		{"abc", "x", 3, "abcx", 4},  // 插在末尾
		{"abc", "x", 9, "abcx", 4},  // 越界钳到末尾
		{"abc", "x", -1, "xabc", 1}, // 负值钳到开头
		{"a😀", "好", 1, "a好😀", 2},    // 代理对当成一个 rune
		{"abc", "", 1, "abc", 1},    // 空提交不改动
	}
	for _, c := range cases {
		got, off := imeSplice(c.text, c.off, c.ins)
		if got != c.want || off != c.wantOff {
			t.Fatalf("imeSplice(%q, %d, %q) = (%q, %d), want (%q, %d)",
				c.text, c.off, c.ins, got, off, c.want, c.wantOff)
		}
	}
}

// TestTaOffsetRoundTrip (行,列) ↔ 全文 rune 下标必须严格互逆: 插入之后光标
// 要能落回二维坐标, 否则在 textarea 里提交一次就会串到别的行。
func TestTaOffsetRoundTrip(t *testing.T) {
	lines := []string{"ab", "", "cde"}
	text := "ab\n\ncde"
	cases := []struct{ line, col, off int }{
		{0, 0, 0},
		{0, 2, 2},
		{1, 0, 3},
		{2, 0, 4},
		{2, 3, 7},
	}
	for _, c := range cases {
		if got := taOffsetOf(lines, c.line, c.col); got != c.off {
			t.Fatalf("taOffsetOf(%v, %d, %d) = %d, want %d", lines, c.line, c.col, got, c.off)
		}
		if l, col := taLineColOf(text, c.off); l != c.line || col != c.col {
			t.Fatalf("taLineColOf(%q, %d) = (%d, %d), want (%d, %d)", text, c.off, l, col, c.line, c.col)
		}
	}
	// 越界: 行号超出要钳到最后一行, 且不能返回负下标
	if got := taOffsetOf(lines, 99, 0); got != 4 {
		t.Fatalf("taOffsetOf 行号越界 = %d, want 4", got)
	}
	if l, col := taLineColOf(text, 99); l != 2 || col != 3 {
		t.Fatalf("taLineColOf 下标越界 = (%d, %d), want (2, 3)", l, col)
	}
}

// TestIMETarget 只有"可编辑且未禁用"的字段类组件能接输入法。
func TestIMETarget(t *testing.T) {
	ta := mkNode("textarea", nil)
	in := mkNode("input", nil)
	btn := mkNode("button", nil)
	if got := imeTarget(ta); got != ta {
		t.Fatalf("textarea 应选中自身, 实际 %v", got)
	}
	if got := imeTarget(in); got != in {
		t.Fatalf("input 应选中自身, 实际 %v", got)
	}
	if got := imeTarget(btn); got != nil {
		t.Fatalf("button 不该接 IME, 实际 %v", got)
	}
	if got := imeTarget(nil); got != nil {
		t.Fatalf("nil 焦点不该接 IME, 实际 %v", got)
	}
	// 禁用框: 输入法不能改它的内容
	dis := withBool(mkNode("input", nil), "disabled", true)
	if got := imeTarget(dis); got != nil {
		t.Fatalf("禁用的 input 不该接 IME, 实际 %v", got)
	}
	// 祖先禁用也算
	parent := withBool(mkNode("column", nil), "disabled", true)
	child := mkNode("input", nil)
	parent.Children = []*GuiNode{child}
	child.Parent = parent // disabledInChain 沿 Parent 上溯, mkNode 不设这个字段
	if got := imeTarget(child); got != nil {
		t.Fatalf("祖先禁用的 input 不该接 IME, 实际 %v", got)
	}
}

// TestIMEInsertInput 单行框: 整批插入 + 光标跨过整批 + 只派发一次 onInput。
func TestIMEInsertInput(t *testing.T) {
	var got []string
	in := imeRecorder(t, withStr(mkNode("input", map[string]float64{"width": 200}), "value", "ac"), &got)
	root := mkNode("column", nil)
	root.Children = []*GuiNode{in}
	fake, a := mountTestApp(t, root, 300, 120)
	a.setFocus(in)
	in.caret = 1 // "a|c"

	pushAndPump(t, fake, a, Event{Kind: EventIMECommit, Text: "你好"})

	if len(got) != 1 {
		t.Fatalf("onInput 派发 %d 次 (一次提交只能派发一次), 内容 %v", len(got), got)
	}
	if got[0] != "a你好c" {
		t.Fatalf("派发的 value = %q, want %q", got[0], "a你好c")
	}
	if in.inputValue() != "a你好c" {
		t.Fatalf("写回后的 value = %q, want %q", in.inputValue(), "a你好c")
	}
	// 光标必须跨过整批 (停在中间的话, 下一个字会插到词的内部 → 字序错乱)
	if in.caret != 3 {
		t.Fatalf("caret = %d, want 3", in.caret)
	}
}

// TestIMEInsertTextarea 多行框: 二维光标折算 (含"提交内容里带换行")。
func TestIMEInsertTextarea(t *testing.T) {
	var got []string
	ta := imeRecorder(t, withStr(mkNode("textarea", map[string]float64{"width": 200, "height": 80}),
		"value", "ab\ncd"), &got)
	root := mkNode("column", nil)
	root.Children = []*GuiNode{ta}
	fake, a := mountTestApp(t, root, 300, 120)
	a.setFocus(ta)
	ta.caretLine, ta.caret = 1, 1 // "ab" / "c|d"

	pushAndPump(t, fake, a, Event{Kind: EventIMECommit, Text: "好"})

	if len(got) != 1 || got[0] != "ab\nc好d" {
		t.Fatalf("派发内容 = %v, want [ab\\nc好d]", got)
	}
	if ta.caretLine != 1 || ta.caret != 2 {
		t.Fatalf("光标 = (%d, %d), want (1, 2)", ta.caretLine, ta.caret)
	}

	// 提交内容自带换行: 光标要跟着落到新行
	pushAndPump(t, fake, a, Event{Kind: EventIMECommit, Text: "x\ny"})
	if len(got) != 2 || got[1] != "ab\nc好x\nyd" {
		t.Fatalf("第二次派发内容 = %v, want [.. ab\\nc好x\\nyd]", got)
	}
	if ta.caretLine != 2 || ta.caret != 1 {
		t.Fatalf("换行后光标 = (%d, %d), want (2, 1)", ta.caretLine, ta.caret)
	}
}

// TestIMECommitIgnoredOffField 焦点不在可编辑控件上时, 提交必须被丢弃
// (否则"在按钮上开着输入法敲字"会把字塞进某个输入框)。
func TestIMECommitIgnoredOffField(t *testing.T) {
	var got []string
	in := imeRecorder(t, withStr(mkNode("input", map[string]float64{"width": 200}), "value", ""), &got)
	btn := mkNode("button", nil)
	root := mkNode("column", nil)
	root.Children = []*GuiNode{btn, in}
	fake, a := mountTestApp(t, root, 300, 120)
	a.setFocus(btn)

	pushAndPump(t, fake, a, Event{Kind: EventIMECommit, Text: "你好"})

	if len(got) != 0 {
		t.Fatalf("焦点在 button 上却派发了 onInput: %v", got)
	}
	if in.inputValue() != "" {
		t.Fatalf("未获焦的输入框被改成了 %q", in.inputValue())
	}
	// 焦点为 nil (还没点过任何东西) 同样要丢
	a.setFocus(nil)
	pushAndPump(t, fake, a, Event{Kind: EventIMECommit, Text: "你好"})
	if len(got) != 0 {
		t.Fatalf("无焦点却派发了 onInput: %v", got)
	}
}

// TestIMEResultStringIsUTF16 结果串是 UTF-16 单元序列: 长度必须按**字节**
// 算, 否则代理对会被截掉一半 (这也是 win32 侧两次调 ImmGetCompositionStringW
// 的原因)。
func TestIMEResultStringIsUTF16(t *testing.T) {
	// 常用汉字: 一个 UTF-16 单元一个字
	units := utf16.Encode([]rune("你好"))
	if len(units) != 2 {
		t.Fatalf("\"你好\" 的 UTF-16 单元数 = %d, want 2", len(units))
	}
	if got := string(utf16.Decode(units)); got != "你好" {
		t.Fatalf("解码 = %q, want %q", got, "你好")
	}
	// emoji / 扩展区汉字: 一个 rune 对应两个单元 —— 按"字符数"分配缓冲就会截断
	emoji := []rune{0x1F600}
	units = utf16.Encode(emoji)
	if len(units) != 2 {
		t.Fatalf("emoji 的 UTF-16 单元数 = %d, want 2", len(units))
	}
	if got := string(utf16.Decode(units)); got != string(emoji) {
		t.Fatalf("代理对解码失败: %q", got)
	}
	// 插入语义也因此必须是"按 rune 切", 不能按 UTF-16 单元切
	if got, _ := imeSplice("ab", 1, string(emoji)); got != "a😀b" {
		t.Fatalf("插入 emoji = %q, want %q", got, "a😀b")
	}
}

// ===== 全链路: 真 VM + 真脚本 signal =====

// imeApp 起一个"value 受控 + 记录每次 onInput"的输入框。
func imeApp(t *testing.T, field string) (*vm.VM, *GuiNode, *app) {
	t.Helper()
	// 调度器是进程级单例, 泄漏的定时器会串到别的用例里
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	v, root, a := evalUIRoot(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [text, setText] = createSignal("");
		const seen = [];
		globalThis.seen = seen;
		const f = h("`+field+`", {
			width: 200, height: 60,
			value: () => text(),
			onInput: (e) => { seen.push(e.value); setText(e.value); },
		});
		render(h("column", null, f), { title: "ime", width: 300, height: 120 });
	`)
	return v, root, a
}

// imeSeen 读脚本里 seen 数组的第 i 项。
func imeSeen(t *testing.T, v *vm.VM, i int) string {
	t.Helper()
	arr := jsArray(t, v, "seen")
	if i >= len(arr.Elements) {
		t.Fatalf("seen 只有 %d 项, 取不到第 %d 项", len(arr.Elements), i)
	}
	s, ok := arr.Elements[i].(*object.String)
	if !ok {
		t.Fatalf("seen[%d] 不是字符串, 实际 %s", i, arr.Elements[i].Type())
	}
	return s.Value
}

// TestIMECommitFullChain 真脚本下的完整链路: 点击获焦 → 输入法提交 →
// onInput 写回 signal → effect 把新值写回 value prop (与手敲键盘同路)。
func TestIMECommitFullChain(t *testing.T) {
	v, root, a := imeApp(t, "input")
	in := findFirst(root, "input")
	if in == nil {
		t.Fatalf("没有挂上 input 节点")
	}
	if _, ok := a.surface.(*fakeSurface); !ok {
		t.Fatalf("测试用的 app 不是假 Surface")
	}

	// 点击获焦 (input 没有 onClick, 走 HitTestDeep 兜底路径)
	pumpEvents(t, v, a, Event{Kind: EventMouseUp, X: in.Box.X + 2, Y: in.Box.Y + in.Box.H/2})
	if a.focused != in {
		t.Fatalf("点击后焦点 = %v, want input", a.focused)
	}

	// 第一次提交
	pumpEvents(t, v, a, Event{Kind: EventIMECommit, Text: "你好"})
	if got := imeSeen(t, v, 0); got != "你好" {
		t.Fatalf("seen[0] = %q, want %q", got, "你好")
	}
	if in.inputValue() != "你好" {
		t.Fatalf("受控回写后 value = %q, want %q", in.inputValue(), "你好")
	}
	if in.caret != 2 {
		t.Fatalf("caret = %d, want 2 (光标要跨过整批)", in.caret)
	}

	// 第二次提交: 追加在光标处 (即末尾), 不是覆盖
	pumpEvents(t, v, a, Event{Kind: EventIMECommit, Text: "世界"})
	if got := imeSeen(t, v, 1); got != "你好世界" {
		t.Fatalf("seen[1] = %q, want %q", got, "你好世界")
	}
	if len(jsArray(t, v, "seen").Elements) != 2 {
		t.Fatalf("两次提交应只派发 2 次 onInput, 实际 %d",
			len(jsArray(t, v, "seen").Elements))
	}
}

// TestIMECommitTextareaFullChain 多行框同链路: 二维光标与受控回写都要对。
func TestIMECommitTextareaFullChain(t *testing.T) {
	v, root, a := imeApp(t, "textarea")
	ta := findFirst(root, "textarea")
	if ta == nil {
		t.Fatalf("没有挂上 textarea 节点")
	}
	if _, ok := a.surface.(*fakeSurface); !ok {
		t.Fatalf("测试用的 app 不是假 Surface")
	}
	pumpEvents(t, v, a, Event{Kind: EventMouseUp, X: ta.Box.X + 2, Y: ta.Box.Y + 2})
	pumpEvents(t, v, a, Event{Kind: EventIMECommit, Text: "你好"})
	pumpEvents(t, v, a, Event{Kind: EventIMECommit, Text: "世界"})

	if got := imeSeen(t, v, 1); got != "你好世界" {
		t.Fatalf("seen[1] = %q, want %q", got, "你好世界")
	}
	if ta.taValue() != "你好世界" {
		t.Fatalf("受控回写后 value = %q, want %q", ta.taValue(), "你好世界")
	}
	if ta.caretLine != 0 || ta.caret != 4 {
		t.Fatalf("光标 = (%d, %d), want (0, 4)", ta.caretLine, ta.caret)
	}
}
