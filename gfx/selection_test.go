package gfx

import (
	"image"
	"strings"
	"testing"
)

// 文本选区与复制 (ru628k) —— input / textarea 共用一套内核 (textedit.go)。
//
// 分三层:
//   - **纯逻辑**: Shift+移动 / Ctrl+A / 选区替换, 直接调内核, 不碰字体与 VM;
//   - **剪贴板**: 假 Surface 的内存剪贴板 (helpers_test.go), 不碰系统剪贴板;
//   - **几何与绘制**: 拖选全链路 + 高亮像素。
//
// 这一层最要紧的一条是 TestSelectionReplacedByTypingAndDelete: "输入即覆盖"
// 是选区存在的**全部意义** —— 只能选中不能覆盖的选区等于没做。

// selIn 是"带选区的按键输入"的简写: lines + 光标 + 锚点。
func selIn(lines []string, line, col, aLine, aCol int) taEditIn {
	return taEditIn{
		lines: lines, line: line, col: col,
		multi: true, aimX: -1,
		sel: taSel{line: aLine, col: aCol, active: true},
	}
}

// ===== 纯逻辑 =====

func TestSelectAllInCore(t *testing.T) {
	lines := []string{"ab", "cde", "f"}
	in := taEditIn{lines: lines, line: 0, col: 1, multi: true, aimX: -1, ctrl: true}
	r := taApplyKeyEx(in, "a")
	if !r.consumed || !r.sel {
		t.Fatalf("Ctrl+A 应消费并打开选区: %+v", r)
	}
	if r.line != 2 || r.col != 1 {
		t.Fatalf("全选后光标应在末尾 (2,1), got (%d,%d)", r.line, r.col)
	}
	if r.anchor != (taPos{0, 0}) {
		t.Fatalf("全选锚点应在开头, got %+v", r.anchor)
	}
	// 大写 A 等同 (键盘布局/Caps 状态不该影响)
	if r2 := taApplyKeyEx(taEditIn{lines: lines, multi: true, aimX: -1, ctrl: true}, "A"); !r2.sel {
		t.Fatalf("Ctrl+Shift+A 也是全选")
	}
	// 单行同样适用 (lines 只有一行)
	if r3 := taApplyKeyEx(taEditIn{lines: []string{"abc"}, multi: false, aimX: -1, ctrl: true}, "a"); r3.col != 3 {
		t.Fatalf("单行全选后光标 = %d, want 3", r3.col)
	}
	// 其它 Ctrl 组合仍然放行 (脚本自己的快捷键不能被打断)
	if r4 := taApplyKeyEx(taEditIn{lines: lines, multi: true, aimX: -1, ctrl: true}, "s"); r4.consumed {
		t.Fatalf("Ctrl+S 不该被编辑框消费")
	}
}

func TestShiftArrowExtendsSelection(t *testing.T) {
	lines := []string{"abcdef"}
	// 第一次 Shift+→: 锚点钉在起点, 光标前移一格
	in := taEditIn{lines: lines, line: 0, col: 2, multi: true, aimX: -1, shift: true}
	r := taApplyKeyEx(in, "ArrowRight")
	if !r.sel || r.anchor != (taPos{0, 2}) || r.col != 3 {
		t.Fatalf("Shift+→ = sel=%v anchor=%+v col=%d, want true (0,2) 3", r.sel, r.anchor, r.col)
	}
	// 再按一次: 锚点**不动**, 光标继续前移 (锚点被重设的症状是"选区永远一格")
	r = taApplyKeyEx(taEditIn{lines: lines, line: r.line, col: r.col, multi: true,
		aimX: -1, shift: true, sel: taSel{r.anchor.line, r.anchor.col, r.sel}}, "ArrowRight")
	if r.anchor != (taPos{0, 2}) || r.col != 4 {
		t.Fatalf("连续 Shift+→: anchor=%+v col=%d, want (0,2) 4", r.anchor, r.col)
	}
	// 反向收缩: Shift+← 把光标移回来
	r = taApplyKeyEx(taEditIn{lines: lines, line: r.line, col: r.col, multi: true,
		aimX: -1, shift: true, sel: taSel{r.anchor.line, r.anchor.col, r.sel}}, "ArrowLeft")
	if r.col != 3 {
		t.Fatalf("Shift+← 收缩后 col=%d, want 3", r.col)
	}
	// 不带 Shift 的移动 = 收起选区
	r = taApplyKeyEx(taEditIn{lines: lines, line: r.line, col: r.col, multi: true,
		aimX: -1, sel: taSel{0, 2, true}}, "ArrowRight")
	if r.sel {
		t.Fatalf("不按 Shift 的移动应收起选区")
	}
}

func TestShiftArrowCollapsesForwardFromSelection(t *testing.T) {
	// 已经有选区 (0,1)..(0,4) 时按 → : 收起并落到**终点** (浏览器语义)
	in := selIn([]string{"abcdef"}, 0, 4, 0, 1)
	r := taApplyKeyEx(in, "ArrowRight")
	if r.sel {
		t.Fatalf("→ 应收起选区: %+v", r)
	}
	if r.col != 4 {
		t.Fatalf("→ 收起后应落在选区终点, col=%d want 4", r.col)
	}
	r = taApplyKeyEx(selIn([]string{"abcdef"}, 0, 4, 0, 1), "ArrowLeft")
	if r.sel || r.col != 1 {
		t.Fatalf("← 应收起并落到选区起点: sel=%v col=%d", r.sel, r.col)
	}
	// 带 Shift 时**不能**收起 (否则扩不出选区)
	r = taApplyKeyEx(taEditIn{lines: []string{"abcdef"}, line: 0, col: 4, multi: true,
		aimX: -1, shift: true, sel: taSel{0, 1, true}}, "ArrowRight")
	if !r.sel || r.col != 5 || r.anchor != (taPos{0, 1}) {
		t.Fatalf("Shift+→ 应扩展: sel=%v col=%d anchor=%+v", r.sel, r.col, r.anchor)
	}
}

func TestShiftDownKeepsAnchor(t *testing.T) {
	// 跨行的 Shift+↓: 锚点在上一行, 光标落到下一行 —— 锚点必须留住
	in := selIn([]string{"abcdef", "xy"}, 0, 3, 0, 1)
	in.shift = true
	r := taApplyKeyEx(in, "ArrowDown")
	if !r.sel {
		t.Fatalf("Shift+↓ 应保持选区: %+v", r)
	}
	if r.anchor != (taPos{0, 1}) {
		t.Fatalf("锚点被改动了: %+v", r.anchor)
	}
	if r.line != 1 || r.col != 2 {
		t.Fatalf("光标 = (%d,%d), want (1,2) (列号按目标行钳位)", r.line, r.col)
	}
	// 不带 Shift 的 ↓ 才是"收起选区再下移"
	r = taApplyKeyEx(selIn([]string{"abcdef", "xy"}, 0, 3, 0, 1), "ArrowDown")
	if r.sel {
		t.Fatalf("不按 Shift 的 ↓ 应收起选区")
	}
	if r.line != 1 || r.col != 2 {
		t.Fatalf("收起后 ↓ 光标 = (%d,%d), want (1,2)", r.line, r.col)
	}
}

func TestSelectionReplacedByTypingAndDelete(t *testing.T) {
	// 1) 打字覆盖选区 (选区存在的全部意义)
	r := taApplyKeyEx(selIn([]string{"hello"}, 0, 4, 0, 1), "X")
	if got := strings.Join(r.lines, "\n"); got != "hXo" {
		t.Fatalf("打字覆盖选区 = %q, want %q", got, "hXo")
	}
	if r.col != 2 || r.sel {
		t.Fatalf("覆盖后光标 = %d, sel=%v, want 2 false", r.col, r.sel)
	}
	// 2) 退格删选区
	r = taApplyKeyEx(selIn([]string{"hello"}, 0, 4, 0, 1), "Backspace")
	if got := strings.Join(r.lines, "\n"); got != "ho" {
		t.Fatalf("退格删选区 = %q, want %q", got, "ho")
	}
	if r.col != 1 || !r.changed {
		t.Fatalf("删除后光标 = %d changed=%v", r.col, r.changed)
	}
	// 3) Delete 同上 (方向不同但结果一致)
	r = taApplyKeyEx(selIn([]string{"hello"}, 0, 4, 0, 1), "Delete")
	if got := strings.Join(r.lines, "\n"); got != "ho" {
		t.Fatalf("Delete 删选区 = %q", got)
	}
	// 4) 跨行选区: 一次删干净 (中间那些整行也要没)
	r = taApplyKeyEx(selIn([]string{"abcd", "efgh", "ijkl"}, 2, 2, 0, 2), "Backspace")
	if got := strings.Join(r.lines, "\n"); got != "abkl" {
		t.Fatalf("跨行删选区 = %q, want %q", got, "abkl")
	}
	// 5) Shift+退格 **不是**选区操作 (仍是普通退格)
	r = taApplyKeyEx(taEditIn{lines: []string{"abc"}, line: 0, col: 3, multi: true, aimX: -1, shift: true}, "Backspace")
	if r.sel {
		t.Fatalf("Shift+退格不该产生选区: %+v", r)
	}
	if got := strings.Join(r.lines, "\n"); got != "ab" {
		t.Fatalf("Shift+退格结果 = %q, want ab", got)
	}
}

func TestSelectionReplacedByEnter(t *testing.T) {
	// 多行框: Enter 覆盖选区 = 删掉选区再换行
	r := taApplyKeyEx(selIn([]string{"abcd", "efgh"}, 1, 2, 0, 1), "Enter")
	if got := strings.Join(r.lines, "\n"); got != "a\ngh" {
		t.Fatalf("Enter 覆盖选区 = %q, want %q", got, "a\ngh")
	}
	if r.line != 1 || r.col != 0 {
		t.Fatalf("换行后光标 = (%d,%d), want (1,0)", r.line, r.col)
	}
}

func TestSelTextAndCutRangeHelpers(t *testing.T) {
	lines := []string{"abcd", "ef", "ghij"}
	if got := taSelText(lines, taPos{0, 1}, taPos{2, 3}); got != "bcd\nef\nghi" {
		t.Fatalf("跨行取文本 = %q, want %q", got, "bcd\nef\nghi")
	}
	if got := taSelText(lines, taPos{0, 2}, taPos{0, 4}); got != "cd" {
		t.Fatalf("同行取文本 = %q", got)
	}
	// 删除只碰 [a,b): 两头都要留。跨行删到 "ghij" 的第 3 列 ⇒ 留 "a" + "j"
	out, cur := taCutRange(lines, taPos{0, 1}, taPos{2, 3})
	if got := strings.Join(out, "\n"); got != "aj" {
		t.Fatalf("跨行删除 = %q, want aj", got)
	}
	if cur != (taPos{0, 1}) {
		t.Fatalf("删除后光标应落在区间起点, got %+v", cur)
	}
	// 纯函数: 入参不能被改
	src := []string{"abcd", "ef"}
	before := strings.Join(src, "\n")
	taCutRange(src, taPos{0, 1}, taPos{1, 1})
	taInsertText(src, taPos{0, 0}, "xyz\nuv")
	if got := strings.Join(src, "\n"); got != before {
		t.Fatalf("纯函数改动了入参: %q -> %q", before, got)
	}
}

func TestInsertTextSplitsLines(t *testing.T) {
	// 在行中间粘贴多行文本: 插入内容的**首段接在光标前的头**上, **末段接在
	// 原行的尾**上, 中间各段独立成行 —— 原行的尾不会被丢掉 ("zb" 与 "cd"
	// 分成两行是最容易写错的一处)。
	out, cur := taInsertText([]string{"ab", "cd"}, taPos{0, 1}, "x\ny\nz")
	if got := strings.Join(out, "\n"); got != "ax\ny\nzb\ncd" {
		t.Fatalf("多行插入 = %q, want ax\\ny\\nzb\\ncd", got)
	}
	if cur != (taPos{2, 1}) {
		t.Fatalf("插入后光标 = %+v, want (2,1)", cur)
	}
	// 末尾换行也要多留一行
	out, cur = taInsertText([]string{"ab"}, taPos{0, 2}, "\n")
	if got := strings.Join(out, "\n"); got != "ab\n" || cur != (taPos{1, 0}) {
		t.Fatalf("末尾插入换行 = %q %+v", got, cur)
	}
}

func TestSelectionSurvivesShrunkValue(t *testing.T) {
	// 受控值被 JS 改短后, 越界的锚点/光标必须被钳位 —— 否则切字符串直接 panic
	lines := []string{"ab"}
	if a, b, ok := taNormSel(lines, taSel{line: 9, col: 99, active: true}, taPos{0, 1}); !ok {
		t.Fatalf("钳位后应仍有选区 (0,1)..(2,2)? 实际无")
	} else if a != (taPos{0, 1}) || b != (taPos{0, 2}) {
		t.Fatalf("钳位结果 = %+v %+v, want (0,1) (0,2)", a, b)
	}
	// 两端都被钳到同一点 = 没有选区 (不该崩, 也不该画出退化高亮)
	if _, _, ok := taNormSel(lines, taSel{line: 5, col: 7, active: true}, taPos{5, 7}); ok {
		t.Fatalf("钳位后重合的两点不该算选区")
	}
}

// ===== 剪贴板 (假 Surface 的内存剪贴板) =====

func TestCtrlCXVUseClipboard(t *testing.T) {
	resetClipboard()
	root := mkNode("column", nil)
	// imeRecorder 是"JS 侧的受控写回": 没有它, onInput 派发了也没人把值写回
	// value prop, 于是剪切/粘贴看起来"什么都没发生"(受控组件的常态陷阱)。
	var seen []string
	ta := imeRecorder(t, mkTextarea("hello world"), &seen)
	mountChildren(root, ta)
	_, a := mountTestApp(t, root, 400, 300)

	// 选中 "hello" (列 0..5)
	ta.caretLine, ta.caret = 0, 5
	ta.selAnchorLine, ta.selAnchorCol, ta.selActive = 0, 0, true
	if !a.handleTextareaKey(ta, "c", Event{Key: "c", Ctrl: true}) {
		t.Fatalf("Ctrl+C 应被消费")
	}
	if clipText != "hello" {
		t.Fatalf("复制到剪贴板 = %q, want hello", clipText)
	}
	// 复制不改文本, 只派发一次…… 其实复制根本不派发 onInput
	if len(seen) != 0 {
		t.Fatalf("Ctrl+C 不该触发 onInput: %q", seen)
	}
	// 剪切: 剪贴板拿到内容, 文本里那段没了
	if !a.handleTextareaKey(ta, "x", Event{Key: "x", Ctrl: true}) {
		t.Fatalf("Ctrl+X 应被消费")
	}
	if clipText != "hello" {
		t.Fatalf("剪切到剪贴板 = %q", clipText)
	}
	if ta.taValue() != " world" {
		t.Fatalf("剪切后文本 = %q, want \" world\"", ta.taValue())
	}
	// 光标落在剪切处 → 粘贴回来
	if ta.caret != 0 {
		t.Fatalf("剪切后光标 = %d, want 0", ta.caret)
	}
	if !a.handleTextareaKey(ta, "v", Event{Key: "v", Ctrl: true}) {
		t.Fatalf("Ctrl+V 应被消费")
	}
	if ta.taValue() != "hello world" {
		t.Fatalf("粘贴后文本 = %q", ta.taValue())
	}
	if ta.caret != 5 || ta.selActive {
		t.Fatalf("粘贴后光标 = %d sel=%v, want 5 false", ta.caret, ta.selActive)
	}
}

func TestCopyWithoutSelectionBubbles(t *testing.T) {
	resetClipboard()
	root := mkNode("column", nil)
	var seen []string
	in := imeRecorder(t, mkInput("abc"), &seen)
	mountChildren(root, in)
	_, a := mountTestApp(t, root, 400, 300)
	// 挂上窗口之后才能写假剪贴板 (clipboardBackend 要一个当前窗口)
	if !writeClipboardText("用户的剪贴板") {
		t.Fatalf("写入假剪贴板失败")
	}

	// 没有选区 → 不消费 (留给脚本), 也**不能**覆盖用户的剪贴板
	if a.handleInputKey(in, "c", Event{Key: "c", Ctrl: true}) {
		t.Fatalf("无选区时 Ctrl+C 不该被输入框消费")
	}
	if clipText != "用户的剪贴板" {
		t.Fatalf("无选区时不该动剪贴板, got %q", clipText)
	}
	// Alt 组合一律放行 (AltGr 会带着 Alt 送普通字符)
	if a.handleInputKey(in, "c", Event{Key: "c", Alt: true}) {
		t.Fatalf("Alt+C 不该被消费")
	}
	// 剪贴板后端不可用 (被别的进程占着) 也不能让按键"消失": 剪切仍要删掉选区
	// (拿不到剪贴板是环境问题, 用户按了剪切就该看到内容被删掉)。
	clipFail = true
	defer func() { clipFail = false }()
	in.caret = 3
	in.selAnchorLine, in.selAnchorCol, in.selActive = 0, 0, true
	before := len(seen)
	if !a.handleInputKey(in, "x", Event{Key: "x", Ctrl: true}) {
		t.Fatalf("剪贴板不可用时 Ctrl+X 仍应被消费")
	}
	if len(seen) == before {
		t.Fatalf("剪贴板不可用时剪切仍应删掉选区并派发 onInput")
	}
}

func TestPasteReplacesSelectionAndFoldsNewlinesInInput(t *testing.T) {
	resetClipboard()
	root := mkNode("column", nil)
	var seen []string
	in := imeRecorder(t, mkInput("hello"), &seen)
	mountChildren(root, in)
	_, a := mountTestApp(t, root, 400, 300)
	writeClipboardText("A\nB")

	// 单行框: 粘贴的换行折成空格 —— 否则单行值变成多行, 后续行根本渲染不出来
	in.caret = 5
	if !a.handleInputKey(in, "v", Event{Key: "v", Ctrl: true}) {
		t.Fatalf("Ctrl+V 应被消费")
	}
	if got := in.inputValue(); got != "helloA B" {
		t.Fatalf("单行粘贴 = %q, want %q", got, "helloA B")
	}
	// 有选区时粘贴要**替换**选区
	writeClipboardText("XY")
	in.caret = 2
	in.selAnchorLine, in.selAnchorCol, in.selActive = 0, 5, true
	if !a.handleInputKey(in, "v", Event{Key: "v", Ctrl: true}) {
		t.Fatalf("Ctrl+V 应被消费")
	}
	if got := in.inputValue(); got != "heXYA B" {
		t.Fatalf("粘贴替换选区 = %q, want %q", got, "heXYA B")
	}
	if in.selActive {
		t.Fatalf("粘贴后选区该没了")
	}
}

func TestSelectionReplacedByIMECommit(t *testing.T) {
	// 中文输入的主路径: 选中几个字再打拼音, 提交时必须**替换**选区
	root := mkNode("column", nil)
	var seen []string
	ta := imeRecorder(t, mkTextarea("世界"), &seen)
	mountChildren(root, ta)
	_, a := mountTestApp(t, root, 400, 300)

	ta.caretLine, ta.caret = 0, 0
	ta.selAnchorLine, ta.selAnchorCol, ta.selActive = 0, 2, true
	a.insertIMEChars(ta, "你好")
	if got := ta.taValue(); got != "你好" {
		t.Fatalf("IME 提交未替换选区: %q", got)
	}
	if ta.caret != 2 || ta.selActive {
		t.Fatalf("提交后光标 = %d sel=%v, want 2 false", ta.caret, ta.selActive)
	}
	if len(seen) != 1 {
		t.Fatalf("一次提交应只派发一次 onInput, got %d", len(seen))
	}
	// 单行框同理 (插到光标处, 不是恒插到末尾)
	in := imeRecorder(t, mkInput("ab"), &seen)
	in.caret = 2
	a.insertIMEChars(in, "中")
	if got := in.inputValue(); got != "ab中" {
		t.Fatalf("单行 IME 追加 = %q", got)
	}
	in.caret = 0
	a.insertIMEChars(in, "一")
	if got := in.inputValue(); got != "一ab中" {
		t.Fatalf("单行 IME 插到行首 = %q", got)
	}
}

// ===== 鼠标拖选 (全链路) =====

func TestDragSelectFullChain(t *testing.T) {
	resetClipboard()
	root := mkNode("column", nil)
	ta := mkTextarea("hello world")
	mountChildren(root, ta)
	fake, a := mountTestApp(t, root, 400, 300)

	area := ta.taArea()
	lh := ta.taLineHeight()
	// x 必须**由列现算**: 比例字体里 'h'/'e'/'l'/'o' 宽度各不相同, 拿某一个
	// 字母的宽度乘列数去点, 落到的列是不确定的 (用例会时过时不过)。
	rs := []rune("hello world")
	v := taVisual{line: 0, start: 0, end: len(rs)}
	xOf := func(col int) int { return area.X + taXOfCol(rs, v, col, ta.taTextStyle()) }
	y := area.Y + lh/2

	// 按下 → 锚点钉在行首
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: xOf(0), Y: y})
	if !ta.selActive || ta.selAnchorLine != 0 || ta.selAnchorCol != 0 {
		t.Fatalf("按下后锚点 = (%d,%d) sel=%v", ta.selAnchorLine, ta.selAnchorCol, ta.selActive)
	}
	// 拖到第 5 列 → 选区增长
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: xOf(5), Y: y})
	if ta.caret != 5 {
		t.Fatalf("拖动后光标 = %d, want 5", ta.caret)
	}
	if got := ta.fieldSelText(); got != "hello" {
		t.Fatalf("拖选内容 = %q, want hello", got)
	}
	// 松手 → 选区**保留** (松手不该清选区)
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: xOf(5), Y: y})
	if !ta.selActive || ta.fieldSelText() != "hello" {
		t.Fatalf("松手后选区丢了: %v %q", ta.selActive, ta.fieldSelText())
	}
	if a.textDrag != nil {
		t.Fatalf("松手后 textDrag 未清 (下次移动会被当成拖选)")
	}
	// 反向拖: 从第 11 列拖回第 5 列 → 选中 "世界" 之外的那半截
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: xOf(11), Y: y})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: xOf(6), Y: y})
	if got := ta.fieldSelText(); got != "world" {
		t.Fatalf("反向拖选 = %q, want world", got)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: xOf(6), Y: y})

	// 只点不拖 = 空选区 (定位光标, 不选中任何东西)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: xOf(2), Y: y})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: xOf(2), Y: y})
	if _, _, _, ok := fieldSel(ta); ok {
		t.Fatalf("只点一下不该产生选区")
	}
	if ta.caret != 2 {
		t.Fatalf("点击定位后光标 = %d, want 2", ta.caret)
	}
}

// ===== 绘制 =====

// blueishIn 数区域内"明显偏蓝"的像素 —— 选区底的签名色。
// 用"比底色更蓝"而不是精确比色: FillRect 是半透明混合, 精确值随底色变。
func blueishIn(img *image.RGBA, r Rect) int {
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			p := img.RGBAAt(x, y)
			if int(p.B)-int(p.R) >= 30 && int(p.B) > 150 {
				n++
			}
		}
	}
	return n
}

func TestSelectionHighlightPainted(t *testing.T) {
	requireFont(t)
	root := mkNode("column", nil)
	ta := mkTextarea("hello world")
	mountChildren(root, ta)
	_, a := mountTestApp(t, root, 400, 300)

	area := ta.taArea()
	rs := []rune("hello world")
	v := taVisual{line: 0, start: 0, end: len(rs)}
	st := ta.taTextStyle()
	xOf := func(col int) int { return taXOfCol(rs, v, col, st) }
	lh := ta.taLineHeight()
	// 选中的 "hello" 横带 / 未选中的后半段横带 (x 由列现算, 不用估计宽度)
	band := Rect{X: area.X, Y: area.Y, W: xOf(5), H: lh}
	rest := Rect{X: area.X + xOf(6), Y: area.Y, W: area.W - xOf(6), H: lh}

	// 基线: 没有选区时不该有任何偏蓝像素
	a.setFocus(ta)
	markNodeDirty(ta)
	a.redraw()
	if n := blueishIn(a.img, band); n != 0 {
		t.Fatalf("无选区时不该有选区底: %d px", n)
	}
	// 加上选区 → 选中段上色, 未选中段不上色
	ta.caretLine, ta.caret = 0, 5
	ta.selAnchorLine, ta.selAnchorCol, ta.selActive = 0, 0, true
	markNodeDirty(ta)
	a.redraw()
	if n := blueishIn(a.img, band); n == 0 {
		t.Fatalf("选区没有绘制高亮")
	}
	if n := blueishIn(a.img, rest); n != 0 {
		t.Fatalf("未选中区域被误上色: %d px", n)
	}
	// 选区高亮必须画在**文字之下**: 选中的字仍然要看得见。判据是"这一带还有
	// 深色像素" —— 高亮底本身的亮度在 600 以上, 正文接近 80, 可分性稳定。
	if minLuma(a.img, band) > 300 {
		t.Fatalf("选区把文字盖掉了 (最暗像素 %d)", minLuma(a.img, band))
	}
}

func TestSelectionHighlightSpansWholeLines(t *testing.T) {
	requireFont(t)
	root := mkNode("column", nil)
	ta := mkTextarea("abcd\nef\nghij")
	mountChildren(root, ta)
	_, a := mountTestApp(t, root, 400, 300)

	area := ta.taArea()
	lh := ta.taLineHeight()
	a.setFocus(ta)
	// 从第 1 行第 2 列选到第 3 行第 2 列 → 中间那行 (第 2 行) 是"整行选中",
	// 它的高亮必须铺到**行尾**, 否则看起来像只选了 "ef" 两个字符。
	ta.caretLine, ta.caret = 2, 2
	ta.selAnchorLine, ta.selAnchorCol, ta.selActive = 0, 2, true
	markNodeDirty(ta)
	a.redraw()

	// 行尾右侧 (文字之后) 也亮
	tail := Rect{X: area.X + area.W - 30, Y: area.Y + lh, W: 28, H: lh}
	if n := blueishIn(a.img, tail); n == 0 {
		t.Fatalf("整行选中的中间行没有铺到行尾")
	}
	// 相邻行不该被带上: 第一行只选到 "cd", 它的**行尾**是空的
	above := Rect{X: area.X + area.W - 30, Y: area.Y, W: 28, H: lh}
	if n := blueishIn(a.img, above); n != 0 {
		t.Fatalf("第一行行尾被误上色: %d px", n)
	}
	// 空行整行选中时也要整行亮 (用一条空行验证"空行不是没高亮")
	root2 := mkNode("column", nil)
	ta2 := mkTextarea("aa\n\nbb")
	mountChildren(root2, ta2)
	_, a2 := mountTestApp(t, root2, 400, 300)
	area2 := ta2.taArea()
	a2.setFocus(ta2)
	ta2.caretLine, ta2.caret = 2, 0
	ta2.selAnchorLine, ta2.selAnchorCol, ta2.selActive = 0, 0, true
	markNodeDirty(ta2)
	a2.redraw()
	empty := Rect{X: area2.X, Y: area2.Y + lh, W: area2.W, H: lh}
	if n := blueishIn(a2.img, empty); n == 0 {
		t.Fatalf("空行没有被选区高亮")
	}
}

func TestSelectionThemeTokenOverridable(t *testing.T) {
	// 新增的 selection token 必须真的接进主题系统 (覆盖 + 投影 + JS 可读)。
	defer SetThemeNamed("light")
	if n := ApplyOverrides(map[string]string{"selection": "#ff0000", "不存在的键": "#fff"}); n != 1 {
		t.Fatalf("ApplyOverrides 生效数 = %d, want 1", n)
	}
	if got := CurrentTheme().Selection; got.R != 0xff || got.G != 0 || got.B != 0 {
		t.Fatalf("selection token 未生效: %+v", got)
	}
	if colorSelection.R != 0xff || colorSelection.G != 0 {
		t.Fatalf("投影缓存未同步: %+v", colorSelection)
	}
	cur := CurrentTheme()
	if hex := themeTokens(&cur)["selection"]; !strings.Contains(strings.ToLower(hex), "ff0000") {
		t.Fatalf("gx/theme 读到的 selection = %q", hex)
	}
	// 亮/暗预设的选区底都必须**半透明** —— 不透明会把选中的字整段盖掉
	for _, name := range []string{"light", "dark"} {
		SetThemeNamed(name)
		if al := CurrentTheme().Selection.A; al == 0 || al == 255 {
			t.Fatalf("%s 主题的选区底 alpha = %d, 应为半透明", name, al)
		}
	}
}

// 钉住"选区的三处口径必须一致"里的第一处: 绘制用的列→x 与点击用的 x→列互逆。
func TestSelectionColumnPixelRoundTrip(t *testing.T) {
	requireFont(t)
	st := TextStyle{Size: 16}
	rs := []rune("Wim hello 中文")
	v := taVisual{line: 0, start: 0, end: len(rs)}
	for col := 0; col <= len(rs); col++ {
		x := taXOfCol(rs, v, col, st)
		if got := taColAtX(rs, v, x+1, st); got != col {
			t.Fatalf("列 %d → x %d → 列 %d (该列中点判定失效)", col, x, got)
		}
	}
	// 越界输入也要有确定结果
	if got := taColAtX(rs, v, -100, st); got != 0 {
		t.Fatalf("x 为负应得到 0, got %d", got)
	}
	if got := taColAtX(rs, v, 1<<20, st); got != len(rs) {
		t.Fatalf("x 超大应得到行尾, got %d", got)
	}
	// 行内的锚点/光标在 input 上共用同一套 (value 一行, 于是 line 恒为 0)
	in := mkInput("abc")
	in.caretLine, in.caret = 0, 3
	in.selAnchorLine, in.selAnchorCol, in.selActive = 0, 0, true
	if got := in.fieldSelText(); got != "abc" {
		t.Fatalf("单行全选文本 = %q", got)
	}
	if !in.hasSel() {
		t.Fatalf("hasSel 与 fieldSelText 口径不一致")
	}
	in.clearSel()
	if in.hasSel() {
		t.Fatalf("clearSel 之后仍有选区")
	}
}
