package gfx

import (
	"image"
	"image/color"
	"strings"
	"time"

	"github.com/14752222/Gox/object"
)

// input 单行文本输入 (P2-1)。
//
// 受控语义: value prop 是显示内容, 每次"内容变化"的按键派发 onInput({value}),
// JS 侧写回 signal (与 checkbox/radio/select 一致)。
//
// 编辑状态 (光标位置) 是节点的运行时字段 caret, 不来自 props —— 它是渲染层
// 的临时输入状态, 与 select 的 expanded/highlight 同类, 脚本无法直接读写。
//
// 键盘分流: 焦点落在 input 子树里时, handleKey 先把按键交给 handleInputKey;
// 没被消费的键 (Enter / Escape / Tab / 带修饰键的组合) 继续沿祖先链找 JS
// 处理器, 于是"输入框放对话框里按 Esc 关掉"照常工作。
//
// 外观复用字段类组件的公共常量 (selectRowH / fieldPadX, 见 select.go):
// 28px 行高、白底、1px #999 边框, 获焦时边框换 #1a5fb4。
//
// v1 不做: 选区与拖选 (Shift+移动)、水平滚动 (超长文本硬截断)、双击选中、
// IME 组合输入 (P2-7)、剪贴板 (P3-3)。

const (
	// inputMinW 是无显式 width 时的缺省宽度。这里不像 select 那样按内容算宽:
	// 输入框的内容会随打字变长, 宽度跟着文字跳变会很难看 (脚本仍可显式
	// 给 width 覆盖)。
	inputMinW = 160

	// caretBlinkMS 是光标闪烁的半周期 (亮 500ms、灭 500ms)。
	caretBlinkMS = 500
)

// caretEpoch 是光标闪烁相位的计时起点。相位用"距起点的半周期个数的奇偶"
// 算出来, 不存可变的开关状态 —— 同一时刻在任何调用点读到的都是同一个值,
// 测试也可以给定时间直接断言。
var caretEpoch = time.Now()

// caretVisibleAt 报告给定时刻输入光标是否可见 (每 caretBlinkMS 翻转一次)。
func caretVisibleAt(now time.Time) bool {
	half := now.Sub(caretEpoch).Milliseconds() / caretBlinkMS
	return half%2 == 0
}

// resetCaretPhase 把闪烁相位归零 (测试用: 让"此刻"光标一定处于可见相)。
func resetCaretPhase(now time.Time) { caretEpoch = now }

// inputInChain 从 n 起沿祖先链找第一个字段类节点 (键盘分流用: 焦点可能落在
// 字段本身, 将来也可能落在它内部的子节点上)。search 是 input 的字段变体,
// 走同一条链。
func inputInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "input" || p.Tag == "search" {
			return p
		}
	}
	return nil
}

// inputValue 读取受控值 (缺失/非标量时为空串)。
func (n *GuiNode) inputValue() string {
	v, ok := n.Props["value"]
	if !ok {
		return ""
	}
	return valueText(v)
}

// inputPlaceholder 读取占位文本 (缺省空串)。
func (n *GuiNode) inputPlaceholder() string {
	s, _ := n.PropStr("placeholder")
	return s
}

// inputText 返回输入框当前该显示的文本与颜色; 值为空时退化成灰色 placeholder。
func (n *GuiNode) inputText() (string, color.RGBA) {
	if val := n.inputValue(); val != "" {
		return val, n.textColor()
	}
	return n.inputPlaceholder(), colorPlaceholder
}

// hasInputValue 报告当前是否有真实值 (区分"空值的灰色 placeholder"与
// "用户真的输入了这些字")。光标的定位要不要把这段文字算进去, 全看它。
func (n *GuiNode) hasInputValue() bool { return n.inputValue() != "" }

// caretIndex 取光标位置并钳到 [0, len(runes)]。钳位是必需的: 受控值由 JS
// 决定, 外部把值改短之后旧的光标位置会越界。
func (n *GuiNode) caretIndex(runes []rune) int {
	c := n.caret
	if c < 0 {
		c = 0
	}
	if c > len(runes) {
		c = len(runes)
	}
	return c
}

// printableRune 判断键名是否是一个可插入的可打印字符 (BMP 直输)。
// 键名长度 >1 的是具名键 (Enter/Backspace/ArrowLeft/...), 单个控制字符
// 也一并排除 —— 它们要么有专门分支, 要么根本不该进文本。
func printableRune(key string) (rune, bool) {
	rs := []rune(key)
	if len(rs) != 1 {
		return 0, false
	}
	r := rs[0]
	if r < 0x20 || r == 0x7F {
		return 0, false
	}
	return r, true
}

// handleFieldKey 把键盘事件先交给焦点链上的"字段类组件"内部处理。
// 返回 true 表示按键已被消费, 不再走 JS 回调 (脚本仍可用 onKeyDown 观察
// 未被消费的键)。
func (a *app) handleFieldKey(n *GuiNode, key string, ev Event) bool {
	// 顺序: 多行编辑框 → 单行输入框 → 下拉框。三者互斥 (一个焦点链上不会
	// 同时出现两个), 顺序只影响"万一嵌了"时的优先级。
	if ta := textareaInChain(n); ta != nil {
		return a.handleTextareaKey(ta, key, ev)
	}
	if in := inputInChain(n); in != nil {
		return a.handleInputKey(in, key, ev)
	}
	sel := selectInChain(n)
	if sel == nil {
		return false
	}
	switch key {
	case "Enter", " ":
		if sel.expanded {
			if idx := sel.highlight; idx >= 0 {
				if opts := sel.selectOptions(); idx < len(opts) {
					a.chooseOption(sel, opts[idx].value)
					return true
				}
			}
			a.closeSelect(sel)
			return true
		}
		a.openSelect(sel)
		return true
	case "ArrowDown", "ArrowUp":
		delta := 1
		if key == "ArrowUp" {
			delta = -1
		}
		if !sel.expanded {
			a.openSelect(sel)
			return true
		}
		opts := sel.selectOptions()
		a.setHighlight(sel, wrapIndex(sel.highlight+delta, len(opts)))
		return true
	case "Escape":
		if sel.expanded {
			a.closeSelect(sel)
			return true
		}
	}
	return false
}

// handleInputKey 处理输入框内的按键, 返回是否已消费。
//
// 编辑语义本身走**与 textarea 共用的内核** (textedit.go), 这里只做两件单行
// 特有的接驳: search 的 Enter 提交、以及把内核结果落到一维光标上。
//
// 只有"文本内容发生变化"的按键才派发 onInput: 移动光标不是编辑行为
// (与 DOM 的 input 事件一致 —— 在浏览器里按方向键不会触发 input)。
// 光标移动仍然标脏, 因为屏幕上的竖线要跟着跑。
func (a *app) handleInputKey(in *GuiNode, key string, ev Event) bool {
	// 剪贴板与全选 (Ctrl/Cmd + A/C/X/V) 先于内核: 它们要碰系统剪贴板。
	if a.handleFieldClipboard(in, key, ev) {
		return true
	}
	// search 的 Enter 是"整段提交": 单行框的内核本来就不消费 Enter, 这里只是
	// 把 search 的额外语义接上 (逐键 onInput 之外的明确提交点)。
	if key == "Enter" && in.Tag == "search" && !ev.Ctrl && !ev.Alt {
		a.dispatchSearch(in)
		return true
	}
	res := taApplyKeyEx(taEditIn{
		lines: []string{in.inputValue()}, line: 0, col: in.caret,
		multi: false, aimX: -1,
		sel:  taSel{line: in.selAnchorLine, col: in.selAnchorCol, active: in.selActive},
		ctrl: ev.Ctrl, alt: ev.Alt, shift: ev.Shift,
	}, key)
	if !res.consumed {
		return false
	}
	a.applyInputEdit(in, res)
	if res.changed {
		a.inputEdited(in, strings.Join(res.lines, "\n"))
	}
	return true
}

// applyInputEdit 把内核结果落到单行框上。单行只有一列坐标可用, 所以接口
// 依旧是 caret 一个字段 (caretLine 恒为 0)。
func (a *app) applyInputEdit(in *GuiNode, res taEdit) {
	moved := res.col != in.caret || res.sel != in.selActive ||
		(res.sel && (res.anchor.line != in.selAnchorLine || res.anchor.col != in.selAnchorCol))
	in.caretLine = 0
	in.caret = res.col
	in.selAnchorLine, in.selAnchorCol, in.selActive = res.anchor.line, res.anchor.col, res.sel
	if moved {
		markNodeDirty(in)
	}
}

// inputEdited 派发 onInput({value}) (受控回写入口; 文本已由本地编辑算出)。
func (a *app) inputEdited(in *GuiNode, value string) {
	markNodeDirty(in)
	if in.PropHandler("onInput") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(value))
	a.callHandler(in, "onInput", arg)
}

// focused 标记是"该节点当前持有键盘焦点", 由 app.setFocus 维护 (渲染层
// 专有, 与 hovered/pressed 同类)。绘制时用它决定输入框的边框颜色与光标。
// caret 是输入框的光标位置 (rune 下标)。
func (n *GuiNode) isFocused() bool { return n.focused }

// setCaretFromX 把光标放到点击位置最近的字符边界上 (点击定位光标)。
//
// 逐字符累加宽度而不是对每个前缀调一次 MeasureText: 后者是 O(n²) 次
// 字形测量, 长文本下白烧 CPU; 累加只是按字符测量后求和, 线性且够准。
// 判定规则取"点到字符中点之前算前一个边界", 与常见编辑器的观感一致。
//
// 实际换算交给 taColAtX —— 与 textarea 点击定位、↑↓ 的 x→列 换算是**同一个
// 函数**。三处各写一份的话, 症状是"点一下光标跳到隔壁那格", 只在比例字体下
// 出现, 极难查。
func (a *app) setCaretFromX(in *GuiNode, x int) {
	runes := []rune(in.inputValue())
	textX := in.Box.X + fieldPadX + searchLeading(in)
	// 单行框没有软换行: 整行就是一个视觉段, 于是可以复用同一套换算。
	whole := taVisual{line: 0, start: 0, end: len(runes)}
	caret := taColAtX(runes, whole, x-textX, resolveTextStyle(in))
	if in.caret == caret {
		return
	}
	in.caret = caret
	markNodeDirty(in)
}

// paintInput 绘制单行输入框: 字段底 + 边框 (获焦转强调色) + 文本/placeholder +
// 1px 竖线光标 (仅在获焦且相位可见时画)。
func paintInput(img *image.RGBA, n *GuiNode, disabled bool) {
	paintField(img, n, disabled, 0)
}

// paintField 是 input / search 共用的字段绘制核心: 字段底 + 边框 (获焦转
// 强调色) + 文本/placeholder + 1px 竖线光标 (仅在获焦且相位可见时画)。
// leading 是"文字区左移量": input 恒为 0, search 让位给左侧放大镜
// (见 gfx/search.go 的 searchLeading) —— 光标定位 (setCaretFromX) 与
// 这里必须用同一个口径, 不然点击落点和光标画的位置对不上。
func paintField(img *image.RGBA, n *GuiNode, disabled bool, leading int) {
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

	st := resolveTextStyle(n)
	text, textColor := n.inputText()
	maxW := b.W - 2*fieldPadX - leading
	if maxW < 0 {
		maxW = 0
	}
	textX := b.X + fieldPadX + leading

	// 光标 x = 字段左留白 + 让位 + "光标之前那截文本"的宽度。placeholder
	// 不参与计算: 值是空的时候光标就在最左边, 不能因为占了位的灰字而右移。
	runes := []rune(text)
	whole := taVisual{line: 0, start: 0, end: len(runes)}
	caret := 0
	if n.hasInputValue() {
		caret = n.caretIndex(runes)
	}

	// 选区高亮画在文字**之下**: 颜色带 alpha, 盖在字上会让选中的字看不见。
	// 高亮后只画一次文字, 顺序反过来就得画两遍。
	if !disabled {
		if sa, sb, ok := taSelRangeOf(n, []string{text}); ok {
			x1 := taXOfCol(runes, whole, sa.col, st)
			x2 := taXOfCol(runes, whole, sb.col, st)
			if x2 > maxW {
				x2 = maxW
			}
			if x2 > x1 {
				FillRect(img, Rect{X: textX + x1, Y: b.Y + 2, W: x2 - x1, H: b.H - 4},
					colorSelection)
			}
		}
	}

	if text != "" {
		_, th := MeasureTextStyled(text, st)
		DrawTextStyled(img, img.Bounds(), text, textX, b.Y+(b.H-th)/2, st,
			tint(textColor, disabled), maxW)
	}

	if n.isFocused() && !disabled && caretVisibleAt(time.Now()) {
		// 竖线高度取行高: 与文字同高看起来才像插入符 (不是整行边框)。
		_, th := MeasureTextStyled("M", st)
		if th < 1 {
			th = 1
		}
		FillRect(img, Rect{X: textX + taXOfCol(runes, whole, caret, st),
			Y: b.Y + (b.H-th)/2, W: 1, H: th},
			tint(n.textColor(), disabled))
	}
}
