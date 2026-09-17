package gfx

import (
	"image"
	"image/color"

	"github.com/14752222/Gox/object"
)

// select 下拉框 (P2-3)。
//
// 结构约定:
//   - 闭合态就是一行"字段"(白底 + 1px 边框 + 当前值/placeholder + 右侧箭头),
//     固定 28px 行高, 与 input 同一套外观常量。
//   - 展开态在 select 下挂一个子节点 select-popup: 它 escapeClipping, 于是被
//     提升到根层级绘制, 既不被 select 那 28px 的盒子裁掉, 也不会被后面的
//     兄弟节点盖住 (这正是 P2-2 层叠模型要解决的场景)。
//   - 展开状态与弹层指针是节点的运行时字段 (expanded / popup), 不来自 props:
//     它是渲染层的临时 UI 状态, 脚本只通过 onChange 表达"选中了什么"。
//
// 下拉项由 Go 侧建成节点树而不是要求 JS 手写 children: options 是数据
// (字符串数组或 {value,label} 数组), 把它当声明式 children 展开没有额外
// 表达力, 却让每个使用点都要写一遍 map。选项行的 onClick 用 object.NewBuiltin
// 挂在节点上, 于是整条"命中 → 回调"链路复用现有事件系统, 没有并行机制。
//
// 受控语义: value 完全由 JS 驱动 (与 checkbox/radio 一致)。点击选项只是
// 触发 onChange({value}) 并收起弹层, 显示内容仍取决于下一轮 value prop。

// 字段类组件的公共尺寸 (select / 后续 input 共用)。
const (
	selectRowH   = 28 // 字段行高
	fieldPadX    = 8  // 字段内水平留白
	selectArrowW = 18 // 右侧下拉箭头占位宽度
	selectMinW   = 80 // 无显式宽度时的最小宽度
)

// selectPopupZ 是下拉弹层的 zIndex: 弹层之间仍可互相压盖 (弹层标签另有
// overlayZBase 的抬升, 见 layer.go)。
const selectPopupZ = 100

// selectOption 是一个下拉项 (value 供脚本使用, label 用于显示)。
type selectOption struct {
	value string
	label string
}

// selectOptions 读取 options prop: 字符串数组或 {value, label} 数组。
// 非数组/非法元素直接跳过 (渲染层不因数据形状不对而 panic)。
func (n *GuiNode) selectOptions() []selectOption {
	v, ok := n.Props["options"]
	if !ok {
		return nil
	}
	arr, ok := v.(*object.Array)
	if !ok {
		return nil
	}
	out := make([]selectOption, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		switch x := e.(type) {
		case *object.String:
			out = append(out, selectOption{value: x.Value, label: x.Value})
		case *object.Number:
			s := object.ToString(x)
			out = append(out, selectOption{value: s, label: s})
		case *object.Object:
			o := selectOption{}
			if pv, ok := x.GetProperty("value"); ok {
				o.value = valueText(pv)
			}
			if pl, ok := x.GetProperty("label"); ok {
				o.label = valueText(pl)
			} else {
				o.label = o.value
			}
			out = append(out, o)
		}
	}
	return out
}

// valueText 把 prop 值转成显示/比较用的字符串 (只处理标量, 其余走 toString)。
func valueText(v object.Value) string {
	switch x := v.(type) {
	case *object.String:
		return x.Value
	case nil:
		return ""
	}
	return object.ToString(v)
}

// selectValue 读取当前受控值 (缺失/非标量时为空串)。
func (n *GuiNode) selectValue() string {
	v, ok := n.Props["value"]
	if !ok {
		return ""
	}
	return valueText(v)
}

// selectLabel 返回闭合态应显示的文本; ok=false 表示"没有值" (调用方改用
// placeholder)。值不在 options 里时直接把值当文本显示 —— 数据源换了但
// value 还没跟上时, 至少让人看见当前值是什么。
func (n *GuiNode) selectLabel() (string, bool) {
	val := n.selectValue()
	if val == "" {
		return "", false
	}
	for _, o := range n.selectOptions() {
		if o.value == val {
			return o.label, true
		}
	}
	return val, true
}

// selectPlaceholder 读取占位文本 (缺省空串)。
func (n *GuiNode) selectPlaceholder() string {
	s, _ := n.PropStr("placeholder")
	return s
}

// selectInChain 从 n 起沿祖先链找第一个 select (键盘分流用: 焦点可能落在
// select 的文本子节点或弹层选项上)。
func selectInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "select" {
			return p
		}
	}
	return nil
}

// attachSelectHandler 给 select 装上内置的展开处理器 (由 JSBuiltinH 在建节点
// 时调用)。
//
// 为什么展开不能交给脚本写 onClick: 展开/收起是组件语义, 不是用户回调。
// 让每个使用点自己写 toggle, 既啰嗦又必然有人漏掉"点已展开的字段应收起"。
// 但也不能因此抢走 onClick —— 脚本确实给了回调时会被包一层: 先走组件的
// 展开逻辑, 再调脚本的回调, 于是 on* 上的"用户回调"语义仍然成立。
//
// 命中测试只认 "onClick 有处理器" 的节点 (hitNode), 所以这个处理器同时
// 承担了"让 select 可被点中"的职责。
func attachSelectHandler(n *GuiNode) {
	user := n.PropHandler("onClick")
	n.Props["onClick"] = object.NewBuiltin("selectToggle", func(args ...object.Value) object.Value {
		a := currentApp()
		if a == nil {
			return object.UndefinedSingleton
		}
		a.toggleSelect(n)
		a.callHandlerValue(user, "onClick", nil)
		return object.UndefinedSingleton
	})
}

// toggleSelect 展开 / 收起下拉 (点字段本身, 或键盘 Enter/Space)。
func (a *app) toggleSelect(sel *GuiNode) {
	if sel.expanded {
		a.closeSelect(sel)
		return
	}
	a.setFocus(sel)
	a.openSelect(sel)
}

// selectRows 返回弹层里的选项行 (未展开时为空)。
func selectRows(sel *GuiNode) []*GuiNode {
	if sel.popup == nil {
		return nil
	}
	return sel.popup.Children
}

// expandedSelects 收集树中所有处于展开态的 select。
// 打开一个下拉时用它先收起其它的: 同时展开两个弹层在视觉和交互上都没有意义。
func expandedSelects(root *GuiNode) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "select" && n.expanded {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return out
}

// ===== 展开 / 收起 =====

// openSelect 展开下拉弹层。已经在展开态则不重复建树。
//
// 整帧标脏而不是只标 select 自己: 弹层新覆盖的那片区域不属于任何"框变了的
// 节点", 局部重绘的脏矩形表达不了"凭空多出一块", 硬凑只会漏画。
// 弹层展开是低频的用户动作, 整帧重绘的代价可以接受。
func (a *app) openSelect(sel *GuiNode) {
	if sel.expanded {
		return
	}
	root := a.rootNode()
	for _, other := range expandedSelects(root) {
		a.closeSelect(other)
	}
	popup := buildSelectPopup(sel)
	if popup == nil {
		return // 没有可选项: 展开一个空盒子没有意义
	}
	sel.expanded = true
	sel.popup = popup
	sel.highlight = 0
	markFullDirty()
}

// closeSelect 收起下拉弹层并销毁它 (递归注销选项行上的 effect)。
func (a *app) closeSelect(sel *GuiNode) {
	if !sel.expanded {
		return
	}
	sel.expanded = false
	sel.highlight = -1
	popup := sel.popup
	sel.popup = nil
	if popup == nil {
		return
	}
	// 焦点可能正落在即将销毁的选项行上: 先收回 select 自身, 否则焦点会
	// 指向一个已经不在树上的节点, 键盘事件从此无处可去。
	a.mu.Lock()
	f := a.focused
	a.mu.Unlock()
	if f != nil && underNode(f, popup) {
		a.setFocus(sel)
	}
	disposeNode(popup) // 会把它从 sel.Children 里摘掉 (Parent 保持有效)
	markFullDirty()
}

// chooseOption 选中一个选项: 收起弹层 → 焦点收回 select → 派发 onChange。
//
// 这里不写回 value: select 是受控组件, 值由 JS 的 signal 决定。
// 回调抛异常照旧打印不中断 (与其它事件回调一致)。
func (a *app) chooseOption(sel *GuiNode, value string) {
	a.closeSelect(sel)
	a.setFocus(sel)
	if sel.PropHandler("onChange") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(value))
	a.callHandler(sel, "onChange", arg)
}

// buildSelectPopup 造出下拉弹层 (select-popup + N 个 select-option)。
// 返回 nil 表示无可选项。
func buildSelectPopup(sel *GuiNode) *GuiNode {
	opts := sel.selectOptions()
	if len(opts) == 0 {
		return nil
	}
	popup := &GuiNode{Tag: "select-popup", Props: map[string]object.Value{}}
	withStrProp(popup, "background", colorPopupFaceHex)
	withStrProp(popup, "border", colorPopupEdgeHex)
	withNumProp(popup, "padding", 1) // 让 1px 边框不压在首行上
	withBoolProp(popup, "escapeClipping", true)
	withNumProp(popup, "zIndex", selectPopupZ)
	popup.Parent = sel

	for i, opt := range opts {
		// 显式取一份循环变量: 回调在事件到来时才执行, 捕获的必须是当轮的值
		idx, o := i, opt
		row := &GuiNode{Tag: "select-option", Props: map[string]object.Value{}}
		row.optIndex = idx
		row.owner = sel
		row.Parent = popup
		row.Props["onClick"] = object.NewBuiltin("selectOption", func(args ...object.Value) object.Value {
			if app := currentApp(); app != nil {
				app.chooseOption(sel, o.value)
			}
			return object.UndefinedSingleton
		})
		row.appendTextNode(o.label)
		popup.Children = append(popup.Children, row)
	}
	sel.Children = append(sel.Children, popup)
	return popup
}

// withStrProp / withNumProp / withBoolProp 是 Go 侧建节点时的属性写入辅助
// (JS 侧走 wireProp, Go 侧没有对应的公开方法)。
func withStrProp(n *GuiNode, name, v string) *GuiNode {
	n.Props[name] = object.NewString(v)
	return n
}

func withNumProp(n *GuiNode, name string, v float64) *GuiNode {
	n.Props[name] = object.NewNumber(v)
	return n
}

func withBoolProp(n *GuiNode, name string, v bool) *GuiNode {
	n.Props[name] = object.NewBoolean(v)
	return n
}

// ===== 键盘 =====

// 字段类组件的键盘分流统一在 input.go 的 handleFieldKey 里 (它先问 input,
// 再问 select), 本文件只负责 select 那一半的编辑动作。

// setHighlight 移动键盘光标并只标脏受影响的两行 (局部重绘下, 只重画变化
// 的那两行比整帧便宜得多; 弹层展开/收起才需要整帧)。
func (a *app) setHighlight(sel *GuiNode, idx int) {
	old := sel.highlight
	sel.highlight = idx
	rows := selectRows(sel)
	markOptionRowDirty(rows, old)
	markOptionRowDirty(rows, idx)
}

func markOptionRowDirty(rows []*GuiNode, idx int) {
	if idx >= 0 && idx < len(rows) {
		markNodeDirty(rows[idx])
	}
}

// wrapIndex 把下标循环到 [0,n); n<=0 时返回 -1 (无项可高亮)。
func wrapIndex(i, n int) int {
	if n <= 0 {
		return -1
	}
	return ((i % n) + n) % n
}

// ===== 布局 / 绘制 =====

// layoutSelect 摆放下拉框: select 自身没有流内子节点 (选项来自 options),
// 弹层则由这里显式定位 —— 贴在字段正下方、与字段等宽。
//
// 放在布局阶段而不是"展开时算一次坐标": 布局每帧都跑, 窗口 resize 后
// 弹层自然跟着走, 不需要额外的失效通知。
func layoutSelect(n *GuiNode) {
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() || c == n.popup {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
		layoutNode(c)
	}
	if n.popup == nil || !n.expanded {
		return
	}
	// 弹层高度取内容固有高度 (选项行数 × 28 + padding), 宽度跟随字段。
	pw, ph := stackContentSize(n.popup, false)
	if w := n.Box.W; w > 0 {
		pw = w
	}
	n.popup.Box = Rect{X: n.Box.X, Y: n.Box.Y + n.Box.H, W: pw, H: ph}
	layoutNode(n.popup)
}

// paintSelect 画闭合态字段: 白底 + 1px 边框 + 当前值 (无值则灰色 placeholder)
// + 右侧 v 形箭头。展开时边框换成强调色, 让"这个下拉正开着"有反馈。
func paintSelect(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(n.fieldFace(colorFieldFace), disabled))
	edge := colorInputEdge
	if n.expanded {
		edge = colorFocusRing
	}
	StrokeRect(img, b, tint(edge, disabled))

	size := n.FontSize()
	text, ok := n.selectLabel()
	textColor := colorText
	if !ok {
		text, textColor = n.selectPlaceholder(), colorPlaceholder
	}
	if text != "" {
		_, th := MeasureText(text, size)
		maxW := b.W - 2*fieldPadX - selectArrowW
		if maxW < 0 {
			maxW = 0
		}
		DrawText(img, img.Bounds(), text, b.X+fieldPadX, b.Y+(b.H-th)/2, size,
			tint(textColor, disabled), maxW)
	}
	paintChevron(img, b, tint(colorInputEdge, disabled))
}

// paintChevron 画下拉箭头 (v 形): 两段 2px 粗线拼成, 不追求抗锯齿。
func paintChevron(img *image.RGBA, b Rect, c color.RGBA) {
	cx := b.X + b.W - selectArrowW/2 - 1
	cy := b.Y + b.H/2
	fillLine(img, cx-4, cy-2, cx, cy+2, 2, c)
	fillLine(img, cx, cy+2, cx+4, cy-2, 2, c)
}

// paintSelectOption 画下拉项。高亮 (鼠标悬停或键盘光标) 时铺一层浅蓝底,
// 文字由 #text 子节点照常绘制。
func paintSelectOption(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	if n.optionHighlighted() {
		FillRect(img, n.Box, tint(colorOptionActive, disabled))
		return
	}
	if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(bg, disabled))
	}
}

// optionHighlighted 报告下拉项是否处于高亮态。键盘光标与鼠标悬停共用一个
// 视觉表现 (两者都是"当前指向的项"), 但状态分开存: 用同一个字段的话,
// 鼠标一动就会把键盘光标冲掉。
func (n *GuiNode) optionHighlighted() bool {
	if n.hovered {
		return true
	}
	return n.owner != nil && n.owner.expanded && n.owner.highlight == n.optIndex
}
