package gfx

// ===== 文本编辑内核 (input / textarea 共用) =====
//
// 这一层把"按键 → 新文本 + 新光标 + 新选区"写成**纯函数**: 入参是当前文本、
// 光标、选区与软换行视图, 出参是新的四件套, 不碰节点、不派发回调、不读剪贴板。
// 两个好处:
//  1. 编辑语义 (行首退格合并、跨行删除、选区替换、视觉行上下移动) 可以直接单测,
//     不需要真 VM 去承接 onInput 写回;
//  2. 受控值只在一个地方读一次, 不会出现"边改边读"的中间态 —— 受控组件最怕
//     的就是这个: 读到的值可能是上一帧的。
//
// ## 为什么 input 与 textarea 共用一个内核
//
// 两者只差**一个开关**: 单行框的 Enter 是"提交"信号 (放行给上层), 多行框的
// Enter 是内容。其余 (插入/退格/删除/方向键/Home/End/选区/替换) 逐字相同。
// 分成两份实现的话, "中文退格"这类边界修了一处漏一处, 而症状只在某一个控件上
// 出现 —— 这正是富文本做不下去的那类坑。
//
// ## 软换行只影响三类动作
//
// 换行是**纯显示变换**: 逻辑列序 = 视觉列序, 所以字符插入、左右移动、退格
// 全都不需要知道换行。只有下面三类必须走视觉行表 (taVisual):
//
//	↑ / ↓        跨视觉行, 且要保持"期望像素 x"而不是"期望列号"
//	Home / End   跳到**视觉行**的首尾 (屏幕上那一行的首尾, 不是逻辑行)
//	点击定位      先按 y 找视觉行, 再按 x 找列
//
// 光标绘制与滚动跟随同理 (都要知道光标在第几视觉行)。

import "strings"

// taPos 是编辑框里的一个插入点: 逻辑行号 + 行内 rune 列。
type taPos struct{ line, col int }

// taPosLess 给出插入点的全序 (先比行、再比列) —— 选区规范化只需要这一点。
func taPosLess(a, b taPos) bool {
	return a.line < b.line || (a.line == b.line && a.col < b.col)
}

// taSel 是选区的**锚点** (不动的那一头) 与激活标记。
// 另一头是节点上的 caret —— 它是主状态, 锚点只是"从哪儿开始选"。
type taSel struct {
	line, col int
	active    bool
}

// taVisual 是一条视觉行: 屏幕上占一行的那截文本, 用"逻辑行号 + 段内 rune
// 区间 [start, end)"表示。软换行让逻辑行与屏幕行不再一一对应, 于是所有
// "按屏幕行"的动作都要查这张表。
type taVisual struct {
	line       int
	start, end int
}

// taEditIn 是一次按键处理需要的全部输入 (纯数据)。
type taEditIn struct {
	lines     []string // 逻辑行 (受控值按 '\n' 切)
	line, col int      // 当前光标
	vm        []taVisual
	// vm 是软换行视图; nil = 不换行, 此时逻辑行即视觉行 (单行 input 恒为 nil,
	// 手写单测也走这条: 于是"不换行"这条路径不需要字体度量参与)。
	multi            bool      // 多行: Enter 插入换行; 单行: Enter 放行给上层
	st               TextStyle // 只在软换行视图下用于 x ↔ 列 换算
	aimX             int       // ↑↓ 的期望像素 x; < 0 = 未设定 → 退回"按列号钳位"
	sel              taSel
	ctrl, alt, shift bool
}

// taEdit 是一次按键的结果 (纯数据, 无副作用)。
//
// 字段是**超集**: 老的单测只读 lines/line/col/changed/consumed, 新增的选区
// 三件套在后两种用法 (Ctrl+A、Shift+移动) 里才有意义。
type taEdit struct {
	lines    []string
	line     int
	col      int
	anchor   taPos
	sel      bool
	changed  bool // 文本内容变了 → 要派发 onInput
	consumed bool // 按键被编辑器接管 (false 时继续沿祖先链派发)
}

// taClampPos 把一个插入点钳进合法范围。受控值随时可能被 JS 改短, 而光标与
// 锚点都是**跨帧存活**的运行时状态 —— 不钳就会越界 panic (下标直接切字符串)。
func taClampPos(lines []string, p taPos) taPos {
	if len(lines) == 0 {
		return taPos{}
	}
	if p.line < 0 {
		p.line = 0
	}
	if p.line > len(lines)-1 {
		p.line = len(lines) - 1
	}
	n := len([]rune(lines[p.line]))
	if p.col < 0 {
		p.col = 0
	}
	if p.col > n {
		p.col = n
	}
	return p
}

// taNormSel 把"锚点 + 光标"规范化成"前 → 后"两点。未激活或两点重合时
// ok = false (重合 = 没有选区, 与浏览器一致: 点一下不产生选区)。
func taNormSel(lines []string, sel taSel, cur taPos) (a, b taPos, ok bool) {
	if !sel.active {
		return taPos{}, taPos{}, false
	}
	p := taClampPos(lines, taPos{sel.line, sel.col})
	q := taClampPos(lines, cur)
	if p == q {
		return taPos{}, taPos{}, false
	}
	if taPosLess(q, p) {
		p, q = q, p
	}
	return p, q, true
}

// taSelText 取 [a,b) 之间的文本 (跨行用 '\n' 连接, 与 value 的编码一致)。
func taSelText(lines []string, a, b taPos) string {
	if len(lines) == 0 {
		return ""
	}
	a, b = taClampPos(lines, a), taClampPos(lines, b)
	if a.line == b.line {
		rs := []rune(lines[a.line])
		return string(rs[a.col:b.col])
	}
	var sb strings.Builder
	first := []rune(lines[a.line])
	sb.WriteString(string(first[a.col:]))
	for i := a.line + 1; i < b.line; i++ {
		sb.WriteString("\n")
		sb.WriteString(lines[i])
	}
	sb.WriteString("\n")
	last := []rune(lines[b.line])
	sb.WriteString(string(last[:b.col]))
	return sb.String()
}

// taCutRange 删除 [a,b) 之间的文本, 返回新的逻辑行与**新光标** (落在 a)。
//
// 跨行删除要合并成一行: 前段的头 + 后段的尾。中间那些整行一并丢掉 ——
// "选中三行按退格"必须一次删干净, 而不是只删掉第一行的内容。
func taCutRange(lines []string, a, b taPos) ([]string, taPos) {
	if len(lines) == 0 {
		return lines, a
	}
	a, b = taClampPos(lines, a), taClampPos(lines, b)
	if a.line == b.line {
		rs := []rune(lines[a.line])
		merged := make([]rune, 0, len(rs)-(b.col-a.col))
		merged = append(merged, rs[:a.col]...)
		merged = append(merged, rs[b.col:]...)
		out := append([]string(nil), lines...)
		out[a.line] = string(merged)
		return out, a
	}
	head := []rune(lines[a.line])[:a.col]
	tail := []rune(lines[b.line])[b.col:]
	out := make([]string, 0, len(lines)-(b.line-a.line))
	out = append(out, lines[:a.line]...)
	out = append(out, string(head)+string(tail))
	out = append(out, lines[b.line+1:]...)
	return out, a
}

// taInsertText 在 pos 处插入一段文本 (可含 '\n'), 返回新行与新光标 ——
// 光标落在插入内容之后。
//
// 打字、Enter、粘贴、IME 提交四个入口共用它: 各自手写一遍切行逻辑的话,
// "在中间行粘贴多行文本"这种边界必然只在其中一两个入口上是对的。
func taInsertText(lines []string, pos taPos, text string) ([]string, taPos) {
	if text == "" {
		return lines, pos
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	pos = taClampPos(lines, pos)
	rs := []rune(lines[pos.line])
	parts := strings.Split(text, "\n")
	if len(parts) == 1 {
		merged := make([]rune, 0, len(rs)+len(parts[0]))
		merged = append(merged, rs[:pos.col]...)
		merged = append(merged, []rune(parts[0])...)
		merged = append(merged, rs[pos.col:]...)
		out := append([]string(nil), lines...)
		out[pos.line] = string(merged)
		return out, taPos{pos.line, pos.col + len([]rune(parts[0]))}
	}
	out := make([]string, 0, len(lines)+len(parts)-1)
	out = append(out, lines[:pos.line]...)
	out = append(out, string(rs[:pos.col])+parts[0])
	out = append(out, parts[1:len(parts)-1]...)
	last := parts[len(parts)-1]
	out = append(out, last+string(rs[pos.col:]))
	out = append(out, lines[pos.line+1:]...)
	return out, taPos{pos.line + len(parts) - 1, len([]rune(last))}
}

// clearSel 清空选区。节点离树时也会被调 (见 node.go 的 teardown)。
func (n *GuiNode) clearSel() {
	n.selActive = false
	n.selAnchorLine, n.selAnchorCol = 0, 0
}

// ===== 视觉行表的换算 =====

// taViewOf 返回生效的视觉行表。调用方没给 (vm == nil) 时按逻辑行现造一张,
// 于是 ↑↓ / Home / End 只有**一条**代码路径 —— 不必在每处判"换没换行"。
func taViewOf(in taEditIn) []taVisual {
	if in.vm != nil {
		return in.vm
	}
	out := make([]taVisual, 0, len(in.lines))
	for i, ln := range in.lines {
		out = append(out, taVisual{line: i, start: 0, end: len([]rune(ln))})
	}
	if len(out) == 0 {
		out = append(out, taVisual{})
	}
	return out
}

// taVisualIndex 找 (逻辑行, 列) 落在第几条视觉行。
//
// 判定是"列落在 [start, end) 内即归该段": 软换行把一列切成多段时, 段边界上
// 的那一列归**后一段** —— 于是"恰好折在边界"的光标画在下一行的行首, 与
// 主流编辑器一致 (它们会为此显示一个尾随的空行)。空段 (start == end) 命中
// 不了区间判定, 兜底归该逻辑行的最后一段。
func taVisualIndex(view []taVisual, line, col int) int {
	last := -1
	for i, v := range view {
		if v.line != line {
			continue
		}
		last = i
		if col >= v.start && col < v.end {
			return i
		}
	}
	if last >= 0 {
		return last
	}
	return 0
}

// taColAtX 在视觉行 v 内找最接近像素 x 的插入列 (取字符中点判定, 与点击定位
// 同一口径 —— 两处口径不一致的症状是"点一下光标跳到隔壁那格")。
func taColAtX(rs []rune, v taVisual, x int, st TextStyle) int {
	cur := 0
	for i := v.start; i < v.end && i < len(rs); i++ {
		w := runeAdvanceStyled(st, rs[i])
		// 判据写成 2*x < 2*cur+w 而不是 x < cur+w/2：后者是**整数除法**，
		// 窄字形的 w/2 会被截断（macOS 16pt 下 'l' 的 advance 实测是 3 ⇒ w/2=1）
		// ⇒ 「落在该格左半」永不成立 ⇒ 列→x→列 的往返算到隔壁列
		// （实测列 6 → x 56 → 回读列 7，macOS CI 上稳定红）。
		//
		// 为什么只有 mac 红：同一串 "Wim hello 中文" 在 Linux（DejaVu 16pt）实测
		// 最小 advance 是 4（'i' 与空格），w/2=2 尚能区分左右半；macOS 的字形更
		// 窄，截断直接吃掉整个左半。所以这条在 Linux 上反向验证不出来 ——
		// 只能靠同乘 2 消除截断本身，而不是去调字体相关的阈值。
		//
		// 两边同乘 2 保留了中点的真实位置，又不引入浮点。
		if 2*x < 2*cur+w {
			return i
		}
		cur += w
	}
	return v.end
}

// taXOfCol 是 taColAtX 的反函数: 插入列在该视觉行里的像素偏移 (用于 ↑↓ 的
// "期望 x"与光标的绘制位置)。
func taXOfCol(rs []rune, v taVisual, col int, st TextStyle) int {
	if col < v.start {
		col = v.start
	}
	if col > v.end {
		col = v.end
	}
	x := 0
	for i := v.start; i < col && i < len(rs); i++ {
		x += runeAdvanceStyled(st, rs[i])
	}
	return x
}

// taApplyKeyEx 是编辑的全部逻辑 (见文件头的设计说明)。
func taApplyKeyEx(in taEditIn, key string) taEdit {
	lines := in.lines
	if len(lines) == 0 {
		lines = []string{""}
	}
	cur := taClampPos(lines, taPos{in.line, in.col})
	sel := in.sel
	sa, sb, hasSel := taNormSel(lines, sel, cur)
	changed := false
	consumed := true

	// Ctrl+A 全选: 不需要系统剪贴板, 放核心里 (于是可单测)。
	// Ctrl+C/X/V 要碰剪贴板 (副作用), 在 app 层处理 —— 见 handleFieldClipboard。
	if in.ctrl && !in.alt && strings.EqualFold(key, "a") {
		last := len(lines) - 1
		return taEdit{lines: lines, line: last, col: len([]rune(lines[last])),
			anchor: taPos{0, 0}, sel: true, consumed: true}
	}
	// 其余组合键留给脚本 (Ctrl+S 之类)。
	if in.ctrl || in.alt {
		return taEdit{lines: lines, line: cur.line, col: cur.col,
			anchor: taPos{sel.line, sel.col}, sel: sel.active, consumed: false}
	}

	// 删除类按键: 有选区就先吃选区 (一次删干净), 没选区才做单字符/跨行删除。
	cutSel := func() bool {
		if !hasSel {
			return false
		}
		nl, np := taCutRange(lines, sa, sb)
		lines, cur, sel, hasSel, changed = nl, np, taSel{}, false, true
		return true
	}

	switch key {
	case "ArrowLeft":
		if hasSel && !in.shift {
			// 有选区时 ← 是"收起选区并落到起点", 不再多退一格 (浏览器语义)
			cur, sel = sa, taSel{}
		} else if cur.col > 0 {
			cur.col--
		} else if cur.line > 0 {
			cur.line--
			cur.col = len([]rune(lines[cur.line]))
		}
	case "ArrowRight":
		if hasSel && !in.shift {
			cur, sel = sb, taSel{}
		} else if cur.col < len([]rune(lines[cur.line])) {
			cur.col++
		} else if cur.line < len(lines)-1 {
			cur.line++
			cur.col = 0
		}
	case "ArrowUp", "ArrowDown":
		// 不带 Shift 时, 有选区先收到对应那一头 (上→起点, 下→终点) 再走一行。
		// 带 Shift 时**不能收**: 锚点要留住, 否则"Shift+↑ 扩展选区"会退化成
		// "从光标往上选一行" —— 每按一次选区都变, 永远扩不出去。
		if hasSel && !in.shift {
			if key == "ArrowUp" {
				cur = sa
			} else {
				cur = sb
			}
			sel = taSel{}
		}
		view := taViewOf(in)
		idx := taVisualIndex(view, cur.line, cur.col)
		delta := -1
		if key == "ArrowDown" {
			delta = 1
		}
		if t := idx + delta; t >= 0 && t < len(view) {
			v := view[t]
			rs := []rune(lines[v.line])
			if in.aimX >= 0 {
				// 有期望 x → 按像素定位 (比例字体下"第几列"与像素不是线性关系,
				// 按列号上下走会让光标左右横跳)
				cur = taPos{v.line, taColAtX(rs, v, in.aimX, in.st)}
			} else {
				// 没有期望 x (单行框 / 未指定的调用方): 退回"按列号钳位",
				// 也就是"光标不会跑到目标行外面"这条老语义。
				col := cur.col
				if col < v.start {
					col = v.start
				}
				if col > v.end {
					col = v.end
				}
				cur = taPos{v.line, col}
			}
		}
	case "Home", "End":
		view := taViewOf(in)
		v := view[taVisualIndex(view, cur.line, cur.col)]
		if key == "Home" {
			cur.col = v.start
		} else {
			cur.col = v.end
		}
	case "Backspace":
		if !cutSel() {
			rs := []rune(lines[cur.line])
			if cur.col > 0 {
				merged := make([]rune, 0, len(rs)-1)
				merged = append(merged, rs[:cur.col-1]...)
				merged = append(merged, rs[cur.col:]...)
				lines = append([]string(nil), lines...)
				lines[cur.line] = string(merged)
				cur.col--
				changed = true
			} else if cur.line > 0 {
				// 行首退格 = 与上一行合并 (光标停在合并点, 与主流编辑器一致)
				prev := []rune(lines[cur.line-1])
				lines = append([]string(nil), lines...)
				lines[cur.line-1] = string(prev) + lines[cur.line]
				lines = append(lines[:cur.line], lines[cur.line+1:]...)
				cur = taPos{cur.line - 1, len(prev)}
				changed = true
			}
		}
	case "Delete":
		if !cutSel() {
			rs := []rune(lines[cur.line])
			if cur.col < len(rs) {
				merged := make([]rune, 0, len(rs)-1)
				merged = append(merged, rs[:cur.col]...)
				merged = append(merged, rs[cur.col+1:]...)
				lines = append([]string(nil), lines...)
				lines[cur.line] = string(merged)
				changed = true
			} else if cur.line < len(lines)-1 {
				// 行尾删除 = 把下一行接上来
				lines = append([]string(nil), lines...)
				lines[cur.line] = lines[cur.line] + lines[cur.line+1]
				lines = append(lines[:cur.line+1], lines[cur.line+2:]...)
				changed = true
			}
		}
	case "Enter":
		if !in.multi {
			// 单行输入框不消费 Enter: 留给上层做提交 (search 的整段提交依赖它)。
			consumed = false
			break
		}
		cutSel()
		lines, cur = taInsertText(lines, cur, "\n")
		changed = true
	default:
		r, ok := printableRune(key)
		if !ok {
			// Escape / Tab / F1.. / Shift: 不消费, 让上层看得到。
			consumed = false
			break
		}
		// 打字直接替换选区 (输入即覆盖, 与所有编辑器一致)
		cutSel()
		lines, cur = taInsertText(lines, cur, string(r))
		changed = true
	}

	if !consumed {
		// 没消费 = 这次按键不算数: 光标与选区都保持原样 (上层可能还要用)。
		return taEdit{lines: lines, line: cur.line, col: cur.col,
			anchor: taPos{in.sel.line, in.sel.col}, sel: in.sel.active, consumed: false}
	}
	switch {
	case changed:
		// 编辑过: 新文本上不该再有选区 (cutSel 已经清过, 这里统一归一 ——
		// 否则 Shift+退格 会在旧位置留一个锚点, 选区凭空出现)
		sel = taSel{}
	case taMovesCaret(key) && in.shift:
		// Shift + 移动 = 扩展选区: 锚点取"这次移动之前"的光标位置。
		// 已经在选区里时锚点**不动** —— 否则每按一次就重设一次起点,
		// 症状是"选区永远只有一格"。
		if !sel.active {
			sel = taSel{in.line, in.col, true}
		}
	default:
		sel = taSel{}
	}
	return taEdit{lines: lines, line: cur.line, col: cur.col,
		anchor: taPos{sel.line, sel.col}, sel: sel.active,
		changed: changed, consumed: true}
}

// taMovesCaret 报告按键是不是"只移动光标"的那一类 —— 只有它们配 Shift
// 才有"扩展选区"的含义 (Shift+退格 仍然是退格, 不是选一格)。
func taMovesCaret(key string) bool {
	switch key {
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End":
		return true
	}
	return false
}
