package gfx

import (
	"image"
	"image/color"
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

// inputInChain 从 n 起沿祖先链找第一个 input (键盘分流用: 焦点可能落在
// input 本身, 将来也可能落在它内部的子节点上)。
func inputInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "input" {
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
// 只有"文本内容发生变化"的按键才派发 onInput: 移动光标不是编辑行为
// (与 DOM 的 input 事件一致 —— 在浏览器里按方向键不会触发 input)。
// 光标移动仍然标脏, 因为屏幕上的竖线要跟着跑。
func (a *app) handleInputKey(in *GuiNode, key string, ev Event) bool {
	// 带 Ctrl/Alt 的组合键留给脚本 (Ctrl+C 之类), input 不抢。
	if ev.Ctrl || ev.Alt {
		return false
	}
	val := []rune(in.inputValue())
	caret := in.caretIndex(val)
	changed := false

	switch key {
	case "Backspace":
		if caret > 0 {
			val = append(val[:caret-1], val[caret:]...)
			caret--
			changed = true
		}
	case "Delete":
		if caret < len(val) {
			val = append(val[:caret], val[caret+1:]...)
			changed = true
		}
	case "ArrowLeft":
		if caret > 0 {
			caret--
		}
	case "ArrowRight":
		if caret < len(val) {
			caret++
		}
	case "Home":
		caret = 0
	case "End":
		caret = len(val)
	default:
		r, ok := printableRune(key)
		if !ok {
			// Enter / Escape / Tab / F1.. / Shift 等: 不消费, 让上层看得到。
			return false
		}
		// 显式重建切片, 不依赖 append 的原地扩容行为 (val 可能被外部持有)。
		nv := make([]rune, 0, len(val)+1)
		nv = append(nv, val[:caret]...)
		nv = append(nv, r)
		nv = append(nv, val[caret:]...)
		val = nv
		caret++
		changed = true
	}

	in.caret = caret
	markNodeDirty(in)
	if changed {
		a.inputEdited(in, string(val))
	}
	return true
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
func (a *app) setCaretFromX(in *GuiNode, x int) {
	runes := []rune(in.inputValue())
	size := in.FontSize()
	caret := len(runes)
	cur := 0
	for i, r := range runes {
		w, _ := MeasureText(string(r), size)
		if x < in.Box.X+fieldPadX+cur+w/2 {
			caret = i
			break
		}
		cur += w
	}
	if in.caret == caret {
		return
	}
	in.caret = caret
	markNodeDirty(in)
}

// paintInput 绘制单行输入框: 字段底 + 边框 (获焦转强调色) + 文本/placeholder +
// 1px 竖线光标 (仅在获焦且相位可见时画)。
func paintInput(img *image.RGBA, n *GuiNode, disabled bool) {
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

	size := n.FontSize()
	text, textColor := n.inputText()
	maxW := b.W - 2*fieldPadX
	if maxW < 0 {
		maxW = 0
	}

	// 光标 x = 字段左留白 + "光标之前那截文本"的宽度。placeholder 不参与
	// 计算: 值是空的时候光标就在最左边, 不能因为占了位的灰字而右移。
	caretX := b.X + fieldPadX
	if n.hasInputValue() {
		runes := []rune(text)
		caret := n.caretIndex(runes)
		cw, _ := MeasureText(string(runes[:caret]), size)
		caretX += cw
	}

	if text != "" {
		_, th := MeasureText(text, size)
		DrawText(img, img.Bounds(), text, b.X+fieldPadX, b.Y+(b.H-th)/2, size,
			tint(textColor, disabled), maxW)
	}

	if n.isFocused() && !disabled && caretVisibleAt(time.Now()) {
		// 竖线高度取字号: 与文字同高看起来才像插入符 (不是整行边框)。
		_, th := MeasureText("M", size)
		if th < 1 {
			th = 1
		}
		FillRect(img, Rect{X: caretX, Y: b.Y + (b.H-th)/2, W: 1, H: th},
			tint(n.textColor(), disabled))
	}
}
