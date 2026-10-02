package gfx

import (
	"strings"
)

// IME 输入法输入 (P2-7)。
//
// 职责边界: 平台后端只负责"拿到输入法提交的结果串"并投递一条
// EventIMECommit 事件; 插到哪儿、怎么改受控值、派发什么回调, 全在这里。
// 这样 WndProc 依旧只投递事件、不碰元素树 (与本包一贯的线程纪律一致)。
//
// v1 只做**结果提交**: 组合过程 (拼音还没选定候选词的那段) 由系统自己的
// 组合窗显示, 不在输入框里内联绘制 —— 内联预编辑要额外维护"未提交文本"的
// 一套绘制与光标语义, 收益不足以抵消复杂度。X11/XIM 后端留 TODO。
//
// 与单字符输入的关系: 一次提交是一**批**字符 (可能是"你好", 也可能是一个
// emoji 的代理对), 所以不能走 handleInputKey 那个"一个键一个 rune"的路径,
// 必须整批插入、光标一次性跨过整批 —— 否则光标会停在中间, 再敲一个字就
// 插到了词的内部 (表现为"输入中文时字序错乱")。

// imeController 是 Surface 的**可选能力**: 在窗口上开/关输入法。
//
// 与 capturer 同思路: 不扩 Surface 接口, 没实现的后端 (X11 / 测试用
// fakeSurface) 自动退化成"输入法始终开着" —— 功能不坏, 只是在非编辑控件上
// 敲字也会弹候选窗。方法必须导出 (win32 是另一个包, 实现不了未导出方法)。
type imeController interface {
	SetIMEEnabled(on bool)
}

// imeTarget 返回焦点链上真正能吃 IME 文本的编辑框, 没有则 nil。
//
// 顺序与 handleFieldKey 一致 (textarea 优先于 input): 万一嵌套, 由最内层
// 的编辑器接管。禁用子树一律排除 —— 禁用框不该被输入法改动内容。
func imeTarget(n *GuiNode) *GuiNode {
	if n == nil {
		return nil
	}
	if ta := textareaInChain(n); ta != nil && !ta.disabledInChain() {
		return ta
	}
	if in := inputInChain(n); in != nil && !in.disabledInChain() {
		return in
	}
	return nil
}

// imeSplice 把 s 整批插到 text 的第 off 个 rune **之前**, 返回新文本与插入
// 之后的光标偏移 (纯函数, 不碰节点)。
//
// 一律按 rune 计数: 结果串里可能有代理对 (emoji / 扩展区汉字), 按字节切会
// 把字符切成两半。
func imeSplice(text string, off int, s string) (string, int) {
	rs := []rune(text)
	if off < 0 {
		off = 0
	}
	if off > len(rs) {
		off = len(rs)
	}
	ins := []rune(s)
	out := make([]rune, 0, len(rs)+len(ins))
	out = append(out, rs[:off]...)
	out = append(out, ins...)
	out = append(out, rs[off:]...)
	return string(out), off + len(ins)
}

// taOffsetOf 把 textarea 的 (行, 列) 折算成全文的 rune 下标。
// 每行末尾的 '\n' 占一个下标 (它是内容的一部分)。
func taOffsetOf(lines []string, line, col int) int {
	if line < 0 {
		line = 0
	}
	if line > len(lines)-1 {
		line = len(lines) - 1
	}
	if line < 0 {
		return 0
	}
	off := 0
	for i := 0; i < line; i++ {
		off += len([]rune(lines[i])) + 1 // +1 = 行尾的 '\n'
	}
	c := col
	if c < 0 {
		c = 0
	}
	if rl := len([]rune(lines[line])); c > rl {
		c = rl
	}
	return off + c
}

// taLineColOf 把全文 rune 下标折回 (行, 列)。遇到 '\n' 即换行, 与 taOffsetOf
// 严格互逆 (插入后光标要落回二维坐标)。
func taLineColOf(text string, off int) (int, int) {
	rs := []rune(text)
	if off < 0 {
		off = 0
	}
	if off > len(rs) {
		off = len(rs)
	}
	line, col := 0, 0
	for i := 0; i < off; i++ {
		if rs[i] == '\n' {
			line++
			col = 0
			continue
		}
		col++
	}
	return line, col
}

// insertIMEChars 把整批字符插到编辑框的光标处并派发 onInput。
//
// 走的是与单字符输入相同的受控回写路径 (inputEdited / taEdited): 一次提交
// 只派发一次 onInput, 而不是每个字符一次 —— 否则 "你好" 会让 JS 侧看到两次
// 中间态 ("你" 和 "你好"), 在带校验的输入框上表现为"中文输入过程中一直报
// 格式错误"。
//
// 实现上直接复用**粘贴的那个入口** (fieldInsertText): 有选区时先删选区
// ("选中几个字再打中文"是最常见的用法, 不替换就会把新字插在选区**前面**,
// 选中的旧字反而留在后面)、多行文本一次插入、onInput 只派发一次 —— 三条
// 要求一模一样, 没有理由写第二遍。
func (a *app) insertIMEChars(n *GuiNode, s string) {
	if s == "" {
		return
	}
	a.fieldInsertText(n, s)
	if n.Tag == "textarea" {
		// 与 handleTextareaKey 一致: 内容高度按受控值算, 必须写在回写之后。
		n.taEnsureCaretVisible()
	}
}

// insertIMECommit 处理一条 IME 提交事件: 交给当前焦点的编辑框。
//
// 焦点不在可编辑控件上时静默丢弃 —— 这时后端已经按要求把输入法关掉了,
// 正常情况下不会有提交进来; 即便漏进来, 也比"把字插到按钮上"安全。
func (a *app) insertIMECommit(s string) {
	a.mu.Lock()
	n := a.focused
	a.mu.Unlock()
	target := imeTarget(n)
	if target == nil {
		return
	}
	a.insertIMEChars(target, s)
}

// ===== 编辑框状态 → 宿主 (M2: 候选词与光标位置回传) =====
//
// 移动端宿主的 InputConnection 要能回答输入法的三类问题:
//
//	① 光标前后是什么字 (getTextBeforeCursor / getExtractedText)
//	   —— 选词、联想、全选、复制粘贴都靠它;
//	② 光标在屏幕的哪儿 (updateCursorAnchorInfo)
//	   —— 候选词窗必须贴着光标, 否则它会盖住输入框本身;
//	③ 现在该不该弹键盘 (imeController.SetIMEEnabled)。
//
// ①② 要求内核把编辑框的**当前状态**回传出去, 这就是本节。
//
// 为什么不让宿主自己记账: 内核是受控模型 —— 文本真源是 JS 的 value prop, 光标
// 位置在节点上, 宿主手里只可能有一份影子。而"影子过期"的症状恰好是最难查的
// 那一类 (输入法拿旧文本做联想、候选窗贴着旧位置), 所以宁可每帧核对一次。

// IMEEditor 是编辑框状态快照 (内核 → 宿主)。字段与 Android 的
// InputConnection / CursorAnchorInfo 一一对应, 宿主侧不需要再猜。
type IMEEditor struct {
	// Text 是全文 (textarea 含 '\n')。宿主拿它实现 getExtractedText /
	// getTextBeforeCursor; 所有偏移一律是 **rune 下标** (不是字节、不是 UTF-16
	// 单元) —— 宿主侧要按平台口径换算, 别直接当 Java 的 char 下标用。
	Text string
	// SelStart / SelEnd 是选区两端 (rune 下标)。v1 内核没有范围选择
	// (没有 shift+方向键), 所以恒有 SelStart == SelEnd; 字段先留着, 宿主据此
	// 填 updateSelection, 将来加了范围选择不必再改协议。
	SelStart, SelEnd int
	// CaretX/Y/W/H 是光标矩形, **窗口像素坐标** (与 Surface.Size、鼠标事件
	// 同一坐标系; 不是 dp)。W 恒为 1 (竖线光标)。
	CaretX, CaretY, CaretW, CaretH int
	// Focused = false 表示焦点已不在可编辑控件上 (宿主应收起输入法并清空影子)。
	Focused bool
	// Multiline = 编辑框是 textarea。宿主据此决定 inputType 是否带
	// TYPE_TEXT_FLAG_MULTI_LINE —— 写死单行的话多行框里回车会被当成"完成",
	// 用户按回车就再也换不了行。
	Multiline bool
}

// imeEditorReporter 是 Surface 的**可选能力**: 回传编辑框状态。
//
// 与 imeController 同一思路 —— 不扩 Surface; 没实现的后端 (win32 走
// WM_IME_*, cocoa 走 NSTextInputClient, X11 与测试用的假 Surface) 直接落空,
// 行为与加这个接口之前完全一样。
type imeEditorReporter interface {
	ReportIMEEditor(e IMEEditor)
}

// caretRectOf 返回编辑框光标在窗口坐标系里的矩形。
//
// **必须与绘制侧同一口径** (input.go 的 paintField / search.go 的 paintSearch /
// textarea.go 的 paintTextarea): 口径一旦漂移, 症状是"候选词窗比光标高半行"
// 这种既不像 bug、又很难归因的观感问题。它只此一处实现, 就是为了防漂移。
func caretRectOf(n *GuiNode) (x, y, w, h int) {
	b := n.Box
	size := n.FontSize()
	if n.Tag == "textarea" {
		area := n.taArea()
		lh := n.taLineHeight()
		lines := n.taLines()
		line := n.taLineIndex(len(lines))
		col := n.taCaretCol(lines)
		rs := []rune(lines[line])
		cx := area.X + runeWidth(string(rs[:col]), size)
		cy := area.Y + line*lh - n.offsetY
		return cx, cy + (lh-size)/2, 1, size
	}
	// input / search: 与 paintField 完全同序 —— 字段左留白 + 放大镜让位 +
	// "光标之前那截文本"的宽度。placeholder 不参与 (值是空时光标在最左边)。
	cx := b.X + fieldPadX + searchLeading(n)
	if n.hasInputValue() {
		runes := []rune(n.inputValue())
		cw, _ := MeasureText(string(runes[:n.caretIndex(runes)]), size)
		cx += cw
	}
	_, th := MeasureText("M", size)
	if th < 1 {
		th = 1
	}
	return cx, b.Y + (b.H-th)/2, 1, th
}

// imeEditorSnapshot 给编辑框拍一张状态快照 (纯函数, 不碰全局状态)。
func imeEditorSnapshot(n *GuiNode) IMEEditor {
	e := IMEEditor{Focused: true, Multiline: n.Tag == "textarea"}
	if n.Tag == "textarea" {
		lines := n.taLines()
		e.Text = strings.Join(lines, "\n")
		off := taOffsetOf(lines, n.taLineIndex(len(lines)), n.taCaretCol(lines))
		e.SelStart, e.SelEnd = off, off
	} else {
		e.Text = n.inputValue()
		off := n.caretIndex([]rune(e.Text))
		e.SelStart, e.SelEnd = off, off
	}
	e.CaretX, e.CaretY, e.CaretW, e.CaretH = caretRectOf(n)
	return e
}

// reportIMEEditor 把当前焦点编辑框的状态发给宿主 (每帧光栅化时调一次)。
//
// 为什么挂在**每帧**而不是挂在"焦点/文本/光标会变的那些调用点": 能改编辑框
// 状态的地方有七八处 (点击定位、方向键、Home/End、退格、输入法提交、受控值被
// JS 改写…), 逐个挂必然漏一处 —— 而漏掉的那一处不会有任何报错, 只表现成
// "输入法偶尔拿旧文本"。挂在帧上天然覆盖全部路径 (任何一种变化都会标脏 →
// 下一帧必到), 再按**快照内容去重**, 没变就不发, 于是也不会每帧跨一次
// 语言边界 (光标闪烁不改变快照, 不会诱发重复上报)。
func (a *app) reportIMEEditor() {
	a.mu.Lock()
	s := a.surface
	n := a.focused
	a.mu.Unlock()
	r, ok := s.(imeEditorReporter)
	if !ok {
		return
	}
	var e IMEEditor
	if t := imeTarget(n); t != nil {
		e = imeEditorSnapshot(t)
	}
	a.mu.Lock()
	same := a.imeReported && a.lastIME == e
	a.lastIME, a.imeReported = e, true
	a.mu.Unlock()
	if same {
		return
	}
	r.ReportIMEEditor(e)
}
