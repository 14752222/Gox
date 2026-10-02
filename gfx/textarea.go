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
// ## 逻辑行与视觉行 (软换行, ru628k)
//
// 内容的"行"只有显式 '\n' 一种 (逻辑行, 也就是光标列号所在的那套坐标);
// **屏幕上的行**则是把逻辑行按内容区宽度贪心折出来的结果 (视觉行)。
// 换行算法与 `<text wrap>` 共用同一份 (wrapRuneSpans) —— 两处各写一份的话,
// 漂移的症状是"光标画的位置和字不在同一格", 只在特定字符组合下出现。
//
// 只有三类动作需要视觉行 (见 textedit.go 的说明): ↑↓、Home/End、点击定位;
// 再加上光标绘制与滚动跟随。插入/退格/左右移动在逻辑行里做就够了 ——
// 换行是纯显示变换, 不会改变字符顺序。
//
// 软换行缺省**开** (与 CSS textarea 一致), `wrap={false}` 关掉。关掉之后
// 超长行被右侧裁掉, 那是 v1 的已知取舍。
//
// 内容超出可视高度时**纵向滚动**, 复用 scroll 的 offset 思路但不需要视图
// 容器: offsetY 是本节点的运行时字段, 由"编辑后把光标带回视野"
// (taEnsureCaretVisible) 与滚轮共同维护, 布局时统一钳位。
//
// 选区 / 复制见 selection.go; 编辑语义的纯逻辑见 textedit.go。
//
// 仍不做: 横向滚动、滚动条绘制(多行框通常不需要)、Tab 插入缩进、撤销重做;
// IME 见 ime.go (P2-7), 剪贴板见 clipboard.go (P3-3)。

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

// taTextStyle 是编辑框生效的文本样式 (字号/字体族/粗斜体/行高/字距)。
// 与 `<text>` 同一套解析 —— 于是编辑框里能写 fontFamily="monospace" 做代码框。
func (n *GuiNode) taTextStyle() TextStyle { return resolveTextStyle(n) }

// taWraps 报告编辑框是否软换行 (缺省**开**, 与 CSS textarea 一致)。
func (n *GuiNode) taWraps() bool { return propBoolOr(n, "wrap", true) }

// taLineHeight 返回行高 (与换行/多行测量同一口径)。
func (n *GuiNode) taLineHeight() int { return lineHeightStyled(n.taTextStyle()) }

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

// taCaretPos 是光标在**逻辑坐标**里的插入点 (已钳位)。
func (n *GuiNode) taCaretPos() taPos {
	lines := n.taLines()
	return taClampPos(lines, taPos{n.caretLine, n.caret})
}

// ===== 视觉行 (软换行) =====

// taVisualWrapWidth 返回折行用的内容宽度。宽到没边 / 没布局过 (<= 0) 时返回 0
// = 不折 —— 让"还没布局"的中间态退化成不换行, 而不是把每个字都单独折成一行
// (那会让光标在首帧跳到很奇怪的位置)。
func (n *GuiNode) taVisualWrapWidth() int {
	if !n.taWraps() {
		return 0
	}
	w := n.taArea().W
	if w <= 0 {
		return 0
	}
	return w
}

// taVisualModel 算出当前宽度下的视觉行表 —— 所有"按屏幕行"的动作都查它。
//
// 返回的表**每个逻辑行至少一段**: 空行也占一段 (屏幕上它确实占一行),
// 于是"视觉行号 × 行高"永远等于内容高度, 滚动与光标定位不必特判空行。
func (n *GuiNode) taVisualModel() []taVisual {
	lines := n.taLines()
	st := n.taTextStyle()
	maxW := n.taVisualWrapWidth()
	out := make([]taVisual, 0, len(lines))
	adv := func(r rune) int { return runeAdvanceStyled(st, r) }
	for i, ln := range lines {
		rs := []rune(ln)
		for _, sp := range wrapRuneSpans(rs, adv, maxW) {
			out = append(out, taVisual{line: i, start: sp.start, end: sp.end})
		}
	}
	if len(out) == 0 {
		out = append(out, taVisual{})
	}
	return out
}

// taVisualCount 是视觉行总数 (= 内容高度的行数)。
func (n *GuiNode) taVisualCount() int { return len(n.taVisualModel()) }

// taCaretVisual 返回光标所在的视觉行号 (在 model 里的下标)。
func (n *GuiNode) taCaretVisual(model []taVisual) int {
	p := n.taCaretPos()
	return taVisualIndex(model, p.line, p.col)
}

// taCaretX 返回光标在自己那条视觉行里的像素偏移 (相对内容区左边界)。
func (n *GuiNode) taCaretX(model []taVisual, st TextStyle) int {
	p := n.taCaretPos()
	line := n.taLines()[p.line]
	i := taVisualIndex(model, p.line, p.col)
	return taXOfCol([]rune(line), model[i], p.col, st)
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

// taContentHeight 是全部**视觉**行的总高 (软换行之后行数才是屏幕行数)。
func (n *GuiNode) taContentHeight() int {
	return n.taVisualCount() * n.taLineHeight()
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

// taEnsureCaretVisible 调整 offsetY 让光标所在**视觉行**落在可视区内。
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
	if lh <= 0 {
		return
	}
	idx := n.taCaretVisual(n.taVisualModel())
	top := idx * lh
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

// ===== 按键 → 编辑结果 =====

// taApplyKey 是"不换行 / 无选区 / 无修饰键"这一档的兼容壳, 供单测与老调用方
// 使用 (语义与加软换行之前逐字一致: 逻辑行即视觉行, ↑↓ 按列号钳位)。
func taApplyKey(lines []string, line, col int, key string, ctrl, alt bool) taEdit {
	return taApplyKeyEx(taEditIn{
		lines: lines, line: line, col: col,
		multi: true, aimX: -1, ctrl: ctrl, alt: alt,
	}, key)
}

// taEditInput 按当前节点状态组装内核需要的输入。
//
// "期望 x" (aimX) 的维护是这里唯一有点绕的地方: 连续上下移动期间必须锁住
// 同一个 x, 否则比例字体下光标会左右横跳 (每一行的"第 5 列"对应的像素位置
// 都不同); 而任何一次非上下按键都要把它作废 —— 左右移动/打字之后, 原来的
// 那个 x 已经不该再约束光标了。
func (a *app) taEditInput(ta *GuiNode, lines []string, key string, ev Event) taEditIn {
	st := ta.taTextStyle()
	model := ta.taVisualModel()
	aim := -1
	if key == "ArrowUp" || key == "ArrowDown" {
		if ta.taAimSet {
			aim = ta.taAimX
		} else {
			aim = ta.taCaretX(model, st)
			ta.taAimX, ta.taAimSet = aim, true
		}
	} else {
		ta.taAimSet = false
	}
	return taEditIn{
		lines: lines, line: ta.caretLine, col: ta.caret,
		vm: model, multi: true, st: st, aimX: aim,
		sel:   taSel{line: ta.selAnchorLine, col: ta.selAnchorCol, active: ta.selActive},
		ctrl:  ev.Ctrl,
		alt:   ev.Alt,
		shift: ev.Shift,
	}
}

// taApplyEdit 把内核结果落到节点上 (标脏口径: 光标/选区动了也要重绘, 因为
// 屏幕上的竖线与高亮块要跟着跑)。
func taApplyEdit(ta *GuiNode, res taEdit) {
	moved := res.line != ta.caretLine || res.col != ta.caret ||
		res.sel != ta.selActive ||
		(res.sel && (res.anchor.line != ta.selAnchorLine || res.anchor.col != ta.selAnchorCol))
	ta.caretLine, ta.caret = res.line, res.col
	ta.selAnchorLine, ta.selAnchorCol, ta.selActive = res.anchor.line, res.anchor.col, res.sel
	if moved {
		markNodeDirty(ta)
	}
}

// handleTextareaKey 把按键交给内核, 再把结果落到节点上并派发 onInput。
//
// 与 input 一致: **只有"内容变化"的按键才派发 onInput** (移动光标不是编辑
// 行为), 但移动光标仍要标脏 —— 屏幕上的竖线要跟着跑。
func (a *app) handleTextareaKey(ta *GuiNode, key string, ev Event) bool {
	// 剪贴板与全选 (Ctrl/Cmd + A/C/X/V) 要先于内核: 它们要碰系统剪贴板,
	// 而内核是纯函数。没被认领的修饰键组合照旧放行给脚本。
	if a.handleFieldClipboard(ta, key, ev) {
		return true
	}
	lines := ta.taLines()
	if len(lines) == 0 {
		lines = []string{""}
	}
	res := taApplyKeyEx(a.taEditInput(ta, lines, key, ev), key)
	if !res.consumed {
		return false
	}
	taApplyEdit(ta, res)
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

// taSetCaretFromXY 把光标落到点击位置: 先按 y 找**视觉行**, 再按 x 用
// taColAtX 找列 (与 ↑↓ 的换算同一口径)。
//
// y 要加上 offsetY 折算: 屏幕上的第 k 行对应内容的第 k + offsetY/lh 行。
func (a *app) taSetCaretFromXY(ta *GuiNode, x, y int) {
	lh := ta.taLineHeight()
	if lh <= 0 {
		return
	}
	area := ta.taArea()
	if area.H <= 0 {
		area.H = lh
	}
	model := ta.taVisualModel()
	idx := (y - area.Y + ta.offsetY) / lh
	if idx < 0 {
		idx = 0
	}
	if idx > len(model)-1 {
		idx = len(model) - 1
	}
	if idx < 0 {
		idx = 0
	}
	v := model[idx]
	rs := []rune(ta.taLines()[v.line])
	pos := taPos{v.line, taColAtX(rs, v, x-area.X, ta.taTextStyle())}
	if ta.caretLine == pos.line && ta.caret == pos.col {
		return
	}
	ta.caretLine, ta.caret = pos.line, pos.col
	markNodeDirty(ta)
}

// ===== 绘制 =====

// paintTextarea 绘制多行编辑框: 底 + 边框(获焦转强调色) + 选区底 + 可见视觉行
// + 竖线光标。
//
// 绘制顺序**不能换**: 选区高亮必须在文字之下。反过来写 (高亮盖在字上) 的
// 症状是"一选中就看不见字" —— 而且只在选区颜色不透明时出现。
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

	st := n.taTextStyle()
	lh := n.taLineHeight()
	textCol := tint(n.textColor(), disabled)

	if !n.hasTaValue() {
		if ph := n.taPlaceholder(); ph != "" {
			_, th := MeasureTextStyled(ph, st)
			DrawTextStyled(img, clip, ph, area.X, area.Y+(lh-th)/2, st,
				tint(colorPlaceholder, disabled), area.W)
		}
		return
	}

	lines := n.taLines()
	model := n.taVisualModel()

	// 选区区间 (可能跨逻辑行; 空选区时 ok = false)
	sa, sb, selOn := taSelRangeOf(n, lines)
	if selOn && !disabled {
		for i, v := range model {
			y := area.Y + i*lh - n.offsetY
			if y >= area.Y+area.H {
				break
			}
			if y+lh <= area.Y {
				continue
			}
			a, bb, full := taSelSpanOn(v, sa, sb)
			if a == bb && !full {
				continue
			}
			rs := []rune(lines[v.line])
			x1 := area.X + taXOfCol(rs, v, a, st)
			w := area.W
			if !full {
				w = taXOfCol(rs, v, bb, st) - taXOfCol(rs, v, a, st)
			}
			if w > 0 {
				FillRect(img, Rect{X: x1, Y: y, W: w, H: lh}, colorSelection)
			}
		}
	}

	for i, v := range model {
		y := area.Y + i*lh - n.offsetY
		if y >= area.Y+area.H {
			break // 后面的行都在可视区下方
		}
		if y+lh <= area.Y {
			continue // 已滚出上方
		}
		text := string([]rune(lines[v.line])[v.start:v.end])
		if text == "" {
			continue
		}
		DrawTextStyled(img, clip, text, area.X, y, st, textCol, area.W)
	}

	if n.isFocused() && !disabled && caretVisibleAt(time.Now()) {
		idx := taVisualIndex(model, n.taCaretPos().line, n.taCaretPos().col)
		v := model[idx]
		cx := area.X + taXOfCol([]rune(lines[v.line]), v, n.taCaretPos().col, st)
		cy := area.Y + idx*lh - n.offsetY
		if cy+lh > area.Y && cy < area.Y+area.H {
			FillRect(img, Rect{X: cx, Y: cy + (lh-st.Size)/2, W: 1, H: st.Size}, textCol)
		}
	}
}

// taSelRangeOf 取编辑框当前的选区 (规范化), 没有选区时 ok = false。
func taSelRangeOf(n *GuiNode, lines []string) (a, b taPos, ok bool) {
	return taNormSel(lines,
		taSel{line: n.selAnchorLine, col: n.selAnchorCol, active: n.selActive},
		taPos{n.caretLine, n.caret})
}

// taSelSpanOn 返回某条视觉行上被选中的列区间 [a,b] 与"是否整行选中"。
//
// 整行选中 (两端都不在本行) 时高亮要**铺到行尾**: 否则跨行选区在中间那些行上
// 只高亮到最后一个字符, 看起来像"只选了一半"; 空行更是整行都不亮。
func taSelSpanOn(v taVisual, sa, sb taPos) (a, b int, full bool) {
	if v.line < sa.line || v.line > sb.line {
		return 0, 0, false
	}
	a, b = v.start, v.end
	if v.line == sa.line && sa.col > a {
		a = sa.col
	}
	if v.line == sb.line && sb.col < b {
		b = sb.col
	}
	if b < a {
		b = a
	}
	full = v.line > sa.line && v.line < sb.line
	return a, b, full
}

// hasTaValue 报告是否有真实值 (区分"空值的灰色 placeholder"与"用户真输入了")。
func (n *GuiNode) hasTaValue() bool { return n.taValue() != "" }
