package gfx

import (
	"image"
	"strings"
	"time"

	"github.com/14752222/Gox/object"
)

// textarea 多行文本编辑器 (P2-6b)。
//
// 受控语义与 input 完全一致: `value` prop 是唯一显示来源, 每次"内容发生变化"
// 的按键派发 onInput({value}), JS 侧写回 signal。区别在两点:
//   1. **光标是二维的** `{line, col}` (节点字段 caretLine + caret), 而不是
//      单行的一维下标;
//   2. **Enter 被消费**(插入换行) —— 单行 input 放行 Enter 让上层提交,
//      多行输入框里 Enter 就是内容的一部分, 与 DOM 的 textarea 一致。
//
// "行" 的划分只有显式 '\n' 一种 (硬换行)。v1 **不做软换行**: 软换行会让
// 光标的一维字符下标与二维行号之间多一层映射, 而横向只需要按内容区裁剪即可
// 满足"能编辑"这个最低目标 (渲染层的 `<text wrap>` 才有软换行)。超长行会被
// 右侧裁掉, 这是已知取舍。
//
// 内容超出可视高度时**纵向滚动**, 复用 scroll 的 offset 思路但不需要视图
// 容器: offsetY 是本节点的运行时字段, 由"编辑后把光标带回视野"
// (taEnsureCaretVisible) 与滚轮共同维护, 布局时统一钳位。
//
// v1 不做: 选区/拖选、软换行、横向滚动、滚动条绘制(多行框通常不需要)、
// Tab 插入缩进、撤销重做、IME (P2-7)、剪贴板 (P3-3)。

const (
	// textareaDefaultRows 是无显式 height 时的缺省行数。
	textareaDefaultRows = 4
	// textareaMinW 是无显式 width 时的缺省宽度 (理由同 inputMinW: 内容变长
	// 不该让框宽跟着跳)。
	textareaMinW = 240
	// textareaPadX / textareaPadY 是文本框内容与边框的间距。
	textareaPadX = 6
	textareaPadY = 4
)

// textareaInChain 从 n 起沿祖先链找第一个 textarea (键盘/滚轮/点击分流用)。
func textareaInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "textarea" {
			return p
		}
	}
	return nil
}

// taValue 读取受控值 (缺失/非标量时为空串)。
func (n *GuiNode) taValue() string {
	v, ok := n.Props["value"]
	if !ok {
		return ""
	}
	return valueText(v)
}

// taPlaceholder 读取占位文本 (缺省空串)。
func (n *GuiNode) taPlaceholder() string {
	s, _ := n.PropStr("placeholder")
	return s
}

// taLines 把受控值切成逻辑行。空串得到一行空串 —— 与 textarea 里"一个空行"
// 的观感一致, 调用方不必特判。
func (n *GuiNode) taLines() []string {
	return strings.Split(n.taValue(), "\n")
}

// taLineHeight 返回行高 (与换行/多行测量同一口径)。
func (n *GuiNode) taLineHeight() int { return lineHeight(n.FontSize()) }

// taLineIndex 把光标行号钳到 [0, 行数-1] (受控值可能被 JS 改短)。
func (n *GuiNode) taLineIndex(count int) int {
	l := n.caretLine
	if l < 0 {
		l = 0
	}
	if l > count-1 {
		l = count - 1
	}
	if l < 0 {
		l = 0
	}
	return l
}

// taCaretCol 把光标列号钳到 [0, 本行长度]。
func (n *GuiNode) taCaretCol(lines []string) int {
	if len(lines) == 0 {
		return 0
	}
	line := lines[n.taLineIndex(len(lines))]
	c := n.caret
	if c < 0 {
		c = 0
	}
	if rl := len([]rune(line)); c > rl {
		c = rl
	}
	return c
}

// taArea 返回可编辑/可绘制的内容区 (盒内缩 textareaPadX/PadY)。
func (n *GuiNode) taArea() Rect {
	w := n.Box.W - 2*textareaPadX
	h := n.Box.H - 2*textareaPadY
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return Rect{X: n.Box.X + textareaPadX, Y: n.Box.Y + textareaPadY, W: w, H: h}
}

// taContentHeight 是全部逻辑行的总高。
func (n *GuiNode) taContentHeight() int {
	return len(n.taLines()) * n.taLineHeight()
}

// taMaxOffset 是 offsetY 的上限 (内容高 - 可视高); 内容不满一屏时为 0。
func (n *GuiNode) taMaxOffset() int {
	max := n.taContentHeight() - n.taArea().H
	if max < 0 {
		max = 0
	}
	return max
}

// taClampOffset 把 offsetY 钳进 [0, taMaxOffset()]。布局阶段每帧调用一次:
// 受控值缩短后旧的偏移可能已经越界 (不钳的话内容会被"滚到看不见")。
func (n *GuiNode) taClampOffset() {
	max := n.taMaxOffset()
	if n.offsetY > max {
		n.offsetY = max
	}
	if n.offsetY < 0 {
		n.offsetY = 0
	}
}

// taEnsureCaretVisible 调整 offsetY 让光标所在行落在可视区内。
// 编辑之后必须调用 (否则在底部回车时光标会跑到框外, 用户以为输入丢了)。
//
// 注意调用时机: 要在 onInput 派发**之后**调用 —— 内容高度是按受控值算的,
// 而在 JS 写回 signal 之前读到的还是旧值。
func (n *GuiNode) taEnsureCaretVisible() {
	area := n.taArea()
	if area.H <= 0 {
		return
	}
	lh := n.taLineHeight()
	line := n.taLineIndex(len(n.taLines()))
	top := line * lh
	bottom := top + lh
	if bottom-top > area.H {
		// 可视区连一行都放不下: 对齐行首, 至少让用户看到这一行的开头
		if n.offsetY != top {
			n.offsetY = top
			markNodeDirty(n)
		}
		return
	}
	old := n.offsetY
	if top < n.offsetY {
		n.offsetY = top
	} else if bottom > n.offsetY+area.H {
		n.offsetY = bottom - area.H
	}
	n.taClampOffset()
	if n.offsetY != old {
		markNodeDirty(n)
	}
}

// taOffsetBy 按像素滚动 (正数 = 内容上移, 即向下滚)。返回偏移是否真的改变
// —— 已经在边界上返回 false, 调用方据此决定滚轮要不要继续往外传。
func (n *GuiNode) taOffsetBy(dy int) bool {
	max := n.taMaxOffset()
	if max <= 0 {
		return false
	}
	old := n.offsetY
	v := old + dy
	if v < 0 {
		v = 0
	}
	if v > max {
		v = max
	}
	if v == old {
		return false
	}
	n.offsetY = v
	markNodeDirty(n)
	return true
}

// layoutTextarea 只做两件事: 钳位滚动偏移 + 摆放绝对定位子节点。
// 文本行不产生子节点 (由 paintTextarea 直接绘制), 所以没有常规流布局。
func layoutTextarea(n *GuiNode) {
	area := n.taArea()
	if area.W <= 0 || area.H <= 0 {
		n.offsetY = 0
		placeAbsoluteIn(n, area)
		return
	}
	n.taClampOffset()
	placeAbsoluteIn(n, area)
}

// taEdit 是一次按键对文本产生的结果 (纯数据, 无副作用)。
type taEdit struct {
	lines    []string // 新的逻辑行
	line     int      // 新光标行
	col      int      // 新光标列
	changed  bool     // 文本内容变了 → 要派发 onInput
	consumed bool     // 按键被编辑器接管 (false 时继续沿祖先链派发)
}

// taApplyKey 是编辑的全部逻辑, 写成**纯函数**: 入参是当前文本与光标, 出参是
// 新文本与新光标, 不碰节点、不派发回调。这么做的好处有两个:
//  1. 编辑语义 (尤其"行首退格要合并上一行"这类边界) 可以直接单测, 不需要
//     真 VM 去承接 onInput 写回;
//  2. 受控值只在一个地方被读一次, 不会出现"边改边读"的中间态。
//
// 按键分流与 input 一致, 唯一的区别是 **Enter 被消费**(插入换行): 多行框里
// Enter 就是内容, 单行输入框才需要把它放行给上层做提交。
func taApplyKey(lines []string, line, col int, key string, ctrl, alt bool) taEdit {
	if len(lines) == 0 {
		lines = []string{""}
	}
	// 先钳位: 受控值可能被 JS 从外部改短
	if line < 0 {
		line = 0
	}
	if line > len(lines)-1 {
		line = len(lines) - 1
	}
	if line < 0 {
		line = 0
	}
	cur := []rune(lines[line])
	if col < 0 {
		col = 0
	}
	if col > len(cur) {
		col = len(cur)
	}
	// 组合键留给脚本 (Ctrl+C / Ctrl+V 之类), 编辑器不抢。
	if ctrl || alt {
		return taEdit{lines: lines, line: line, col: col}
	}

	out := taEdit{lines: lines, line: line, col: col, consumed: true}
	switch key {
	case "Enter":
		head, tail := string(cur[:col]), string(cur[col:])
		out.lines = append([]string(nil), out.lines...)
		out.lines[line] = head
		out.lines = append(out.lines, "")
		copy(out.lines[line+2:], out.lines[line+1:])
		out.lines[line+1] = tail
		out.line = line + 1
		out.col = 0
		out.changed = true
	case "Backspace":
		if col > 0 {
			out.lines = append([]string(nil), out.lines...)
			out.lines[line] = string(append(cur[:col-1], cur[col:]...))
			out.col = col - 1
			out.changed = true
		} else if line > 0 {
			// 行首退格 = 与上一行合并 (光标停在合并点, 与主流编辑器一致)
			prev := []rune(out.lines[line-1])
			out.lines = append([]string(nil), out.lines...)
			out.lines[line-1] = string(prev) + out.lines[line]
			out.lines = append(out.lines[:line], out.lines[line+1:]...)
			out.line = line - 1
			out.col = len(prev)
			out.changed = true
		}
	case "Delete":
		if col < len(cur) {
			out.lines = append([]string(nil), out.lines...)
			out.lines[line] = string(append(cur[:col], cur[col+1:]...))
			out.changed = true
		} else if line < len(out.lines)-1 {
			// 行尾删除 = 把下一行接上来
			out.lines = append([]string(nil), out.lines...)
			out.lines[line] = out.lines[line] + out.lines[line+1]
			out.lines = append(out.lines[:line+1], out.lines[line+2:]...)
			out.changed = true
		}
	case "ArrowLeft":
		if col > 0 {
			out.col = col - 1
		} else if line > 0 {
			out.line = line - 1
			out.col = len([]rune(out.lines[out.line]))
		}
	case "ArrowRight":
		if col < len(cur) {
			out.col = col + 1
		} else if line < len(out.lines)-1 {
			out.line = line + 1
			out.col = 0
		}
	case "ArrowUp":
		if line > 0 {
			out.line = line - 1
			if rl := len([]rune(out.lines[out.line])); out.col > rl {
				out.col = rl // 上下移动时列号按新行长度钳位 (短行不会把光标顶到行外)
			}
		}
	case "ArrowDown":
		if line < len(out.lines)-1 {
			out.line = line + 1
			if rl := len([]rune(out.lines[out.line])); out.col > rl {
				out.col = rl
			}
		}
	case "Home":
		out.col = 0
	case "End":
		out.col = len(cur)
	default:
		r, ok := printableRune(key)
		if !ok {
			// Escape / Tab / F1.. / Shift: 不消费, 让上层看得到。
			out.consumed = false
			return out
		}
		nv := make([]rune, 0, len(cur)+1)
		nv = append(nv, cur[:col]...)
		nv = append(nv, r)
		nv = append(nv, cur[col:]...)
		out.lines = append([]string(nil), out.lines...)
		out.lines[line] = string(nv)
		out.col = col + 1
		out.changed = true
	}
	return out
}

// handleTextareaKey 把按键交给 taApplyKey, 再把结果落到节点上并派发 onInput。
//
// 与 input 一致: **只有"内容变化"的按键才派发 onInput** (移动光标不是编辑
// 行为), 但移动光标仍要标脏 —— 屏幕上的竖线要跟着跑。
func (a *app) handleTextareaKey(ta *GuiNode, key string, ev Event) bool {
	lines := ta.taLines()
	if len(lines) == 0 {
		lines = []string{""}
	}
	res := taApplyKey(lines, ta.taLineIndex(len(lines)), ta.taCaretCol(lines),
		key, ev.Ctrl, ev.Alt)
	if !res.consumed {
		return false
	}
	if moved := res.line != ta.caretLine || res.col != ta.caret; moved || res.changed {
		markNodeDirty(ta)
	}
	ta.caretLine, ta.caret = res.line, res.col
	if res.changed {
		a.taEdited(ta, strings.Join(res.lines, "\n"))
	}
	// 放在写回之后: 内容高度按受控值算, 这时候读到的才是新值。
	ta.taEnsureCaretVisible()
	return true
}

// taEdited 派发 onInput({value}) (受控回写入口; 新文本已由本地编辑算出)。
func (a *app) taEdited(ta *GuiNode, value string) {
	markNodeDirty(ta)
	if ta.PropHandler("onInput") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(value))
	a.callHandler(ta, "onInput", arg)
}

// taSetCaretFromXY 把光标落到点击位置 (行按 y、列按 x 找最近的字符边界)。
// 行的判定要加上 offsetY —— 屏幕上的第 k 行对应内容的第 k+offset/lh 行。
func (a *app) taSetCaretFromXY(ta *GuiNode, x, y int) {
	area := ta.taArea()
	lh := ta.taLineHeight()
	if lh <= 0 {
		return
	}
	if area.H <= 0 {
		area.H = lh
	}
	lines := ta.taLines()
	line := (y - area.Y + ta.offsetY) / lh
	if line < 0 {
		line = 0
	}
	if line > len(lines)-1 {
		line = len(lines) - 1
	}
	if line < 0 {
		line = 0
	}
	rs := []rune(lines[line])
	size := ta.FontSize()
	col := len(rs)
	cur := 0
	for i, r := range rs {
		w := runeAdvance(size, r)
		if x < area.X+cur+w/2 {
			col = i
			break
		}
		cur += w
	}
	if ta.caretLine == line && ta.caret == col {
		return
	}
	ta.caretLine = line
	ta.caret = col
	markNodeDirty(ta)
}

// paintTextarea 绘制多行编辑框: 底 + 边框(获焦转强调色) + 可见行 + 竖线光标。
func paintTextarea(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(n.fieldFace(colorFieldFace), disabled))
	edge := colorInputEdge
	if n.isFocused() && !disabled {
		edge = colorFocusRing
	}
	StrokeRect(img, b, tint(edge, disabled))

	area := n.taArea()
	if area.W <= 0 || area.H <= 0 {
		return
	}
	// 内容必须裁在编辑区内: img 本身已经是"脏矩形"子图, 再与内容区求交,
	// 这样滚出去的整行不会画到边框外面去。
	clip := img.Bounds().Intersect(image.Rect(area.X, area.Y, area.X+area.W, area.Y+area.H))
	if clip.Empty() {
		return
	}

	size := n.FontSize()
	lh := n.taLineHeight()
	textCol := tint(n.textColor(), disabled)

	if !n.hasTaValue() {
		if ph := n.taPlaceholder(); ph != "" {
			_, th := MeasureText(ph, size)
			DrawText(img, clip, ph, area.X, area.Y+(lh-th)/2, size,
				tint(colorPlaceholder, disabled), area.W)
		}
		return
	}

	lines := n.taLines()
	for i := range lines {
		y := area.Y + i*lh - n.offsetY
		if y >= area.Y+area.H {
			break // 后面的行都在可视区下方
		}
		if y+lh <= area.Y {
			continue // 已滚出上方
		}
		DrawText(img, clip, lines[i], area.X, y, size, textCol, area.W)
	}

	if n.isFocused() && !disabled && caretVisibleAt(time.Now()) {
		line := n.taLineIndex(len(lines))
		col := n.taCaretCol(lines)
		rs := []rune(lines[line])
		cx := area.X + runeWidth(string(rs[:col]), size)
		cy := area.Y + line*lh - n.offsetY
		if cy+lh > area.Y && cy < area.Y+area.H {
			FillRect(img, Rect{X: cx, Y: cy + (lh-size)/2, W: 1, H: size}, textCol)
		}
	}
}

// hasTaValue 报告是否有真实值 (区分"空值的灰色 placeholder"与"用户真输入了")。
func (n *GuiNode) hasTaValue() bool { return n.taValue() != "" }
