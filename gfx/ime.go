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
func (a *app) insertIMEChars(n *GuiNode, s string) {
	if s == "" {
		return
	}
	if n.Tag == "textarea" {
		lines := n.taLines()
		if len(lines) == 0 {
			lines = []string{""}
		}
		off := taOffsetOf(lines, n.taLineIndex(len(lines)), n.taCaretCol(lines))
		nv, noff := imeSplice(strings.Join(lines, "\n"), off, s)
		n.caretLine, n.caret = taLineColOf(nv, noff)
		a.taEdited(n, nv)
		// 与 handleTextareaKey 一致: 内容高度按受控值算, 必须写在回写之后。
		n.taEnsureCaretVisible()
		return
	}
	val := []rune(n.inputValue())
	nv, noff := imeSplice(string(val), n.caretIndex(val), s)
	n.caret = noff
	a.inputEdited(n, nv)
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
