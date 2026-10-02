package gfx

// ===== 文本选区与剪贴板 (input / textarea 共用) =====
//
// 选区状态只有三件事: 锚点 (不动的那一头, 存在节点上)、光标 (跟着走的那一头)、
// 以及"有没有选区"。真正干活的两条路径:
//
//	键盘   Shift + 方向/Home/End 扩展  → 纯逻辑, 在 textedit.go 里 (可单测)
//	鼠标   按下钉锚点, 拖动移动光标      → 需要几何 (视觉行 + 字符中点),
//	                                      所以落在这里 (依赖字体度量)
//
// 剪贴板三键 (Ctrl/Cmd + C/X/V) 也在这里: 它们要读写真机剪贴板 (副作用),
// 不该混进纯函数内核。**Ctrl+A 在核心里** —— 它只需要文本, 不需要剪贴板。
//
// ## 为什么复制/剪切在"没有选区"时不消费按键
//
// 有些编辑器把"没选中时 Ctrl+C"解释成"复制整行"。这里刻意不做: 那会**覆盖
// 用户自己的剪贴板**, 而用户按 Ctrl+C 时的心智是"我选中了什么才复制什么"。
// 返回 false 让按键继续冒泡, 脚本可以自己实现整行复制。

import "strings"

// fieldLines 返回编辑框的逻辑行 (单行 input/search 恒为一行)。
// 有了它, 选区的所有运算 (取文本、删除、插入) 在单行与多行上是**同一套代码**。
func fieldLines(n *GuiNode) []string {
	if n.Tag == "textarea" {
		lines := n.taLines()
		if len(lines) == 0 {
			return []string{""}
		}
		return lines
	}
	return []string{n.inputValue()}
}

// fieldCaret 是编辑框当前的插入点 (已按**当前**受控值钳位)。
func fieldCaret(n *GuiNode) taPos {
	return taClampPos(fieldLines(n), taPos{n.caretLine, n.caret})
}

// fieldSetCaretOn 把插入点落在**给定的那份新文本**上。
//
// 一定要按新文本钳位, 不能按节点上现有的受控值 —— 踩过的坑: 先钳位再写回,
// 插入位置会被**旧**长度截断, 而旧长度恰好是上一次输入的长度, 于是症状是
// "第一次输入对, 从第二次开始每次都插到最前面"。受控组件里这种"读到的还是
// 上一帧的值"是最常见的一类错, 所以这里把"按哪份文本"写进函数名。
func fieldSetCaretOn(n *GuiNode, lines []string, p taPos) {
	p = taClampPos(lines, p)
	n.caretLine, n.caret = p.line, p.col
}

// fieldSel 返回规范化的选区 (无选区时 ok = false)。
func fieldSel(n *GuiNode) (lines []string, a, b taPos, ok bool) {
	lines = fieldLines(n)
	a, b, ok = taNormSel(lines,
		taSel{line: n.selAnchorLine, col: n.selAnchorCol, active: n.selActive},
		fieldCaret(n))
	return lines, a, b, ok
}

// fieldSetText 把新文本写回受控值并派发 onInput (两种控件的回写入口不同)。
func (a *app) fieldSetText(n *GuiNode, text string) {
	if n.Tag == "textarea" {
		a.taEdited(n, text)
		return
	}
	a.inputEdited(n, text)
}

// fieldPickedAnchor 把锚点钉在当前位置 (拖选/Shift 选区的起点)。
func fieldPickedAnchor(n *GuiNode) {
	n.selAnchorLine, n.selAnchorCol = n.caretLine, n.caret
	n.selActive = true
}

// ===== 剪贴板 (字段内) =====

// handleFieldClipboard 处理 Ctrl/Cmd + A/C/X/V, 返回是否已消费。
//
// Alt 组合一律放行: AltGr 在欧语键盘上会带着 Alt 送普通字符, 抢下来会打不出字。
func (a *app) handleFieldClipboard(n *GuiNode, key string, ev Event) bool {
	if !ev.Ctrl || ev.Alt {
		return false
	}
	switch strings.ToLower(key) {
	case "a":
		return a.fieldSelectAll(n)
	case "c":
		return a.fieldCopy(n)
	case "x":
		return a.fieldCut(n)
	case "v":
		return a.fieldPaste(n)
	}
	return false
}

// fieldSelectAll 是 Ctrl/Cmd+A: 全选。
func (a *app) fieldSelectAll(n *GuiNode) bool {
	lines := fieldLines(n)
	last := len(lines) - 1
	n.selAnchorLine, n.selAnchorCol = 0, 0
	n.selActive = true
	n.caretLine, n.caret = last, len([]rune(lines[last]))
	n.taAimSet = false
	markNodeDirty(n)
	return true
}

// fieldCopy 是 Ctrl/Cmd+C: 把选区写进系统剪贴板。
func (a *app) fieldCopy(n *GuiNode) bool {
	_, sa, sb, ok := fieldSel(n)
	if !ok {
		return false
	}
	text := taSelText(fieldLines(n), sa, sb)
	if text == "" {
		return false
	}
	writeClipboardText(text)
	return true
}

// fieldCut 是 Ctrl/Cmd+X: 复制 + 删除选区。
func (a *app) fieldCut(n *GuiNode) bool {
	if !a.fieldCopy(n) {
		return false
	}
	a.fieldDeleteSelection(n)
	return true
}

// fieldPaste 是 Ctrl/Cmd+V: 把剪贴板文本插到光标处 (有选区则替换)。
//
// 单行框上的换行折成空格 —— 直接把 '\n' 塞进单行值会让它变成多行, 而单行框
// 连渲染多行的能力都没有 (后续行被裁掉, 用户以为字丢了)。
func (a *app) fieldPaste(n *GuiNode) bool {
	text := readClipboardText()
	if text == "" {
		return false
	}
	if n.Tag != "textarea" {
		repl := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")
		text = repl.Replace(text)
	}
	a.fieldInsertText(n, text)
	return true
}

// fieldDeleteSelection 删除当前选区 (剪切/退格/打字替换共用), 返回是否删掉了东西。
func (a *app) fieldDeleteSelection(n *GuiNode) bool {
	lines, sa, sb, ok := fieldSel(n)
	if !ok {
		return false
	}
	lines, cur := taCutRange(lines, sa, sb)
	n.clearSel()
	n.taAimSet = false
	fieldSetCaretOn(n, lines, cur)
	markNodeDirty(n)
	a.fieldSetText(n, strings.Join(lines, "\n"))
	return true
}

// fieldInsertText 是"在光标处插一段文本"的统一入口 (粘贴与 IME 共用):
// 有选区先删掉 (粘贴覆盖选中内容), 再把文本按 '\n' 拆行插进去。
func (a *app) fieldInsertText(n *GuiNode, text string) {
	lines, sa, sb, ok := fieldSel(n)
	cur := fieldCaret(n)
	if ok {
		lines, cur = taCutRange(lines, sa, sb)
	}
	lines, cur = taInsertText(lines, cur, text)
	n.clearSel()
	n.taAimSet = false
	fieldSetCaretOn(n, lines, cur)
	markNodeDirty(n)
	a.fieldSetText(n, strings.Join(lines, "\n"))
}

// ===== 鼠标拖选 =====

// moveCaretTo 是"把插入点落到屏幕坐标"的统一入口 (点击 / 拖动 / Shift+点击)。
// 两种控件的几何算法不同 (多行是按视觉行 + 列, 单行只有列), 分流放在这里,
// 上层 (render.go 的鼠标路径) 就不必到处判标签。
func (a *app) moveCaretTo(n *GuiNode, x, y int) {
	if n.Tag == "textarea" {
		a.taSetCaretFromXY(n, x, y)
		return
	}
	a.setCaretFromX(n, x)
}

// beginTextDrag 开始"在编辑框里拖选": 按下即定位插入点, 并把锚点钉在那里。
//
// 按下的瞬间就设锚点 (而不是等第一次 MouseMove) 是有意的: 这样"按下 → 拖 →
// 松手"与"按下 → 松手"共用一条路径, 后者得到的是空选区 (锚点 == 光标),
// 正好是"点一下只定位不选中"。
func (a *app) beginTextDrag(n *GuiNode, x, y int) {
	a.setFocus(n)
	a.moveCaretTo(n, x, y)
	fieldPickedAnchor(n)
	a.mu.Lock()
	a.textDrag = n
	s := a.surface
	a.mu.Unlock()
	if c, ok := s.(capturer); ok {
		// 捕获指针: 拖出窗口再拖回来也能接着选 (没有捕获的后端在
		// EventMouseLeave 里放弃拖动, 见 render.go)。
		c.CapturePointer()
	}
}

// textDragTarget 返回正在被拖选的编辑框 (没有则 nil)。
func (a *app) textDragTarget() *GuiNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.textDrag
}

// fieldSelText 是节点上"当前选中的文本" (没有选区时空串)。脚本与绘制共用。
func (n *GuiNode) fieldSelText() string {
	lines, sa, sb, ok := fieldSel(n)
	if !ok {
		return ""
	}
	return taSelText(lines, sa, sb)
}

// hasSel 报告编辑框上是否真的有选区 (两端重合 / 越界钳位后重合都算"没有")。
func (n *GuiNode) hasSel() bool {
	_, _, _, ok := fieldSel(n)
	return ok
}
