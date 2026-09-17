package gfx

import (
	"fmt"
	"image/color"
	"os"
	"sync"

	"github.com/14752222/Gox/object"
)

// GuiNode 是元素树节点。JS 侧 h(tag, props, ...children) 构建它;
// 函数值的 props/子节点由 gx/solid 的 createEffect 接线, signal 变化时
// effect 重跑 → 写回 Props/Text → Invalidate 触发重绘。
type GuiNode struct {
	Tag      string // 元素名: column/row/rect/text...; 文本节点为 "#text"
	Props    map[string]object.Value
	Text     string // #text 节点内容
	Children []*GuiNode
	Parent   *GuiNode
	Box      Rect // 布局结果 (layout.go 写入)
	PrevBox  Rect // 上一帧布局 (脏矩形 diff 用)

	// effects 记录本节点接线的 effect dispose 函数 (节点销毁时由 disposeNode
	// 递归调用; 条件渲染 (P1-2) 的插槽子节点会走这条路径)
	effects []object.Value

	// 运行时交互状态: 渲染层专有, 不来自 props, 也不暴露给脚本。
	// hovered/pressed 由 Pump 按鼠标事件维护 (P1-4), 绘制时读。
	hovered bool
	pressed bool

	// 下拉框状态 (P2-3): expanded 是展开态, popup 是挂在 select 下的弹层
	// 子节点 (未展开为 nil), highlight 是键盘光标的选项下标 (-1 = 无)。
	expanded  bool
	popup     *GuiNode
	highlight int

	// 下拉项状态: 在兄弟中的下标, 以及所属 select (绘制高亮时要回查
	// highlight, 用指针比"沿 Parent 走两层"更稳 —— 弹层一旦被拆链就找不到)。
	optIndex int
	owner    *GuiNode

	// 输入框状态 (P2-1): focused 是"是否持有键盘焦点"(由 setFocus 维护,
	// 绘制时决定边框颜色与是否画光标), caret 是光标位置 (rune 下标)。
	focused bool
	caret   int
}

// Rect 是布局矩形 (客户区像素坐标)。
type Rect struct {
	X, Y, W, H int
}

// Contains 判断点是否在矩形内。
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// GuiNode 实现 object.Value, 使 h() 的结果能作为 JS 值在脚本中传递。
func (n *GuiNode) Type() object.ObjectType { return object.ObjectType("GUI_NODE") }
func (n *GuiNode) Inspect() string         { return "<GuiNode " + n.Tag + ">" }
func (n *GuiNode) IsTruthy() bool          { return true }
func (n *GuiNode) GetProperty(string) (object.Value, bool) {
	return object.UndefinedSingleton, false
}
func (n *GuiNode) SetProperty(string, object.Value) {}

// ===== 标签白名单与未知标签警告 (P0-4) =====

// knownTags 是已实现的内置元素集合。h() 收到集合外的标签时输出一次警告,
// 消除"未实现组件静默渲染成空盒子"的陷阱 (不会报错, 用户只能看到空白)。
// 渲染行为不因告警改变: 未知标签仍走通用盒子分支 (背景/边框 + 子节点叠放)。
//
// !!! 每新增一个内置组件 (含布局/绘制分支) 时必须同步在此登记 !!!
var knownTags = map[string]struct{}{
	"column": {}, "row": {},
	"text": {}, "#text": {},
	"rect":   {},
	"button": {},
	// P0-1 表单控件
	"checkbox": {}, "radio": {}, "switch": {},
	// P0-2 展示与占位
	"progress": {}, "separator": {}, "spacer": {},
	// P2-3 下拉框 (select-popup / select-option 由 Go 侧构造, 脚本写不到)
	"select": {}, "select-popup": {}, "select-option": {},
	// P2-4 弹层
	"dialog": {}, "toast": {},
	// P2-1 单行文本输入
	"input": {},
}

var (
	warnMu       sync.Mutex
	warnSeenTags = map[string]struct{}{}
)

// warnUnknownTag 是未知标签的警告出口。做成变量而非直接写 stderr,
// 便于单测替换为计数器断言 (同一标签只警告一次的行为见 warnUnknownTagOnce)。
var warnUnknownTag = func(tag string) {
	fmt.Fprintf(os.Stderr,
		"gfx: unknown tag %q (rendered as a plain box; see docs/gui-component-status.md)\n", tag)
}

// warnUnknownTagOnce 同一标签只警告一次: 函数值 prop 驱动的重建会反复
// 调用 h(), 不去重会在热路径上刷屏。
func warnUnknownTagOnce(tag string) {
	warnMu.Lock()
	defer warnMu.Unlock()
	if _, seen := warnSeenTags[tag]; seen {
		return
	}
	warnSeenTags[tag] = struct{}{}
	warnUnknownTag(tag)
}

// ===== JS 侧 h(tag, props, ...children) =====

// JSBuiltinH 是暴露给 JS 的 h 函数实现 (render.go 注册进 gx/gfx 模块)。
func JSBuiltinH(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("h: (tag, props, ...children) required")
	}
	tagVal := args[0]
	tagStr, ok := tagVal.(*object.String)
	if !ok {
		// 组件标签在 parser 层已是直接调用, h 只处理字符串标签
		return object.NewTypeError("h: tag must be a string, got %s", tagVal.Type())
	}
	if _, known := knownTags[tagStr.Value]; !known {
		warnUnknownTagOnce(tagStr.Value)
	}
	node := &GuiNode{Tag: tagStr.Value, Props: map[string]object.Value{}}

	// props: 对象字面量或 null
	if len(args) > 1 {
		if props, ok := args[1].(*object.Object); ok {
			for name, desc := range props.Properties {
				node.wireProp(name, desc.Value)
			}
		}
	}

	// children
	for _, c := range args[2:] {
		node.wireChild(c)
	}
	// 需要内置交互的标签在这里补上 Go 侧处理器 (select 的展开)。
	// 放在 props/children 都接好之后: 包装脚本自己的 onClick 时要能读到它。
	if node.Tag == "select" {
		attachSelectHandler(node)
	}
	return node
}

// wireProp 接线一个属性:
//   - on* 开头且值为函数 → 事件回调, 原样存储
//   - 其他函数值 → 响应式 prop: createEffect 求值写回
//   - 其余 → 静态值
func (n *GuiNode) wireProp(name string, val object.Value) {
	if object.IsCallable(val) && !isEventPropName(name) {
		n.reactiveProp(name, val)
		return
	}
	n.Props[name] = val
}

// isEventPropName 判断是否事件回调属性 (Solid 约定: on 开头)。
func isEventPropName(name string) bool {
	return len(name) > 2 && name[0] == 'o' && name[1] == 'n'
}

// reactiveProp 用 createEffect 包一层响应式属性: 求值 → 写回 Props → 标脏。
func (n *GuiNode) reactiveProp(name string, getter object.Value) {
	dispose := runEffect(func() object.Value {
		v := object.CallFunction(getter, nil)
		n.Props[name] = v
		markNodeDirty(n)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		n.effects = append(n.effects, dispose)
	}
}

// wireChild 接线一个子节点:
//   - GuiNode → 直接挂载
//   - 字符串/数字 → #text 文本节点
//   - 函数 → 响应式子节点 (返回到元素/数组/null 就是条件渲染与列表渲染)
func (n *GuiNode) wireChild(val object.Value) {
	switch c := val.(type) {
	case *GuiNode:
		c.Parent = n
		n.Children = append(n.Children, c)
	case *object.String:
		n.appendTextNode(c.Value)
	case *object.Number:
		n.appendTextNode(object.ToString(c))
	case *object.Null, *object.Undefined:
		// null/undefined 子节点忽略
	case *object.Boolean:
		// 布尔子节点 (如 JSX 里 {cond && <x/>} 的 false 分支) 忽略
	default:
		if object.IsCallable(val) {
			n.wireReactiveChild(val)
			return
		}
		// 数组等其余类型: 展开为文本 (静态数组子节点)
		n.appendTextNode(object.ToString(val))
	}
}

// wireReactiveChild 接线"函数子节点": 每次求值把结果挂进一个 slot 占位节点。
// 求值结果按类型分派 (见 mountValue): 元素 → 挂上, 数组 → 逐元素递归,
// 标量 → 文本, null/undefined/布尔 → 空插槽。这就是声明式条件渲染与
// 列表渲染。
//
// 为什么用 slot 占位而不是直接替换父节点的 Children: 父容器的布局按声明序
// 分配位置, 动态子节点必须有固定的"坑", 兄弟节点的位置才不会跟着抖动。
// slot 对布局透明 (单子时尺寸与交叉轴行为都跟随子节点, 见 layout.go)。
//
// 两种"稳定"结果原地更新, 不拆树:
//  1. 同一个元素对象 (vnode 在闭包外创建) → 直接返回;
//  2. 标量且当前挂的正是那个文本节点 → 只改 Text (这是 P1-2 之前的行为,
//     纯动态文本不该因为引入了 slot 就每次都重建节点)。
//
// 其余情形 (换成元素 / 数组 / 类型改变) 整组重建: v1 不做 diff/key,
// 长列表的增量更新留待后续版本。
func (n *GuiNode) wireReactiveChild(getter object.Value) {
	slot := &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
	slot.Parent = n
	n.Children = append(n.Children, slot)

	var (
		mounted  *GuiNode // 上次挂的单元素
		textNode *GuiNode // 上次挂的文本节点 (标量结果)
	)
	dispose := runEffect(func() object.Value {
		v := object.CallFunction(getter, nil)
		if node, ok := v.(*GuiNode); ok && node == mounted {
			return object.UndefinedSingleton
		}
		if text, ok := scalarString(v); ok && textNode != nil &&
			len(slot.Children) == 1 && slot.Children[0] == textNode {
			if textNode.Text != text {
				textNode.Text = text
				markNodeDirty(textNode)
			}
			return object.UndefinedSingleton
		}
		clearSlot(slot)
		mounted, textNode = nil, nil
		mountValue(slot, v)
		switch {
		case isElement(v):
			mounted = slot.Children[0]
		case len(slot.Children) == 1 && slot.Children[0].Tag == "#text":
			textNode = slot.Children[0]
		}
		markNodeDirty(slot)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		slot.effects = append(slot.effects, dispose)
	}
}

// isElement 报告求值结果是否是单个元素 (GuiNode)。
func isElement(v object.Value) bool {
	_, ok := v.(*GuiNode)
	return ok
}

// scalarString 把标量值转成文本; 非标量返回 ok=false (走拆树重建)。
func scalarString(v object.Value) (string, bool) {
	switch x := v.(type) {
	case *object.String:
		return x.Value, true
	case *object.Number:
		return object.ToString(x), true
	}
	return "", false
}

// mountValue 把一个求值结果挂进 slot。
func mountValue(slot *GuiNode, v object.Value) {
	switch x := v.(type) {
	case *GuiNode:
		x.Parent = slot
		slot.Children = append(slot.Children, x)
	case *object.Array:
		// 列表渲染: 逐元素递归分派 (元素可以是元素/标量/嵌套数组)
		for _, e := range x.Elements {
			mountValue(slot, e)
		}
	case *object.Null, *object.Undefined, *object.Boolean:
		// 空插槽: 条件渲染的 false 分支 / 列表里的空洞
	case *object.String:
		slot.appendTextNode(x.Value)
	case *object.Number:
		slot.appendTextNode(object.ToString(x))
	default:
		slot.appendTextNode(object.ToString(v))
	}
}

// clearSlot 清空 slot 的已挂子树 (递归注销其 effect), 保留 slot 自身。
func clearSlot(slot *GuiNode) {
	old := slot.Children
	slot.Children = nil
	for _, c := range old {
		c.Parent = nil
		disposeNode(c)
	}
}

// disposeNode 递归销毁子树: 先递归子节点, 再注销本节点接线的 effect,
// 最后从父节点摘除。
//
// 为什么必须显式注销: 节点不会自己离开响应式系统 —— 旧子树若不 dispose,
// 它订阅的 signal 变化仍会跑来写这个已经不在树上的节点 (白跑一轮 effect +
// 标脏)。v1 无条件重建的动态子树全靠这里收尾。
func disposeNode(n *GuiNode) {
	if n == nil {
		return
	}
	for _, c := range n.Children {
		disposeNode(c)
	}
	if len(n.effects) > 0 {
		// 没有 VM (纯 Go 单测) 时 runEffect 返回 nil 且不入册, 这里是空表
		for _, d := range n.effects {
			object.CallFunction(d, nil)
		}
		n.effects = nil
	}
	if n.Parent != nil {
		n.Parent.removeChild(n)
		n.Parent = nil
	}
	// 交互状态一并复位: 悬停/按压链里可能还留着这个已经离开树的节点
	n.hovered = false
	n.pressed = false
	// 下拉框: 弹层是子节点, 上面那轮递归已经把它 dispose 过了, 这里只需
	// 断开指针并复位展开态 (否则节点被回收前仍指着已销毁的子树)。
	n.expanded = false
	n.popup = nil
	n.owner = nil
	// 输入框状态: 节点离树后不该再被当成"持有焦点" (否则 setFocus 的
	// 旧节点标脏会对一个游离节点做无意义的重绘)。
	n.focused = false
	n.caret = 0
}

// removeChild 从 Children 里摘掉一个子节点 (存在才摘)。
func (n *GuiNode) removeChild(c *GuiNode) {
	for i, x := range n.Children {
		if x == c {
			n.Children = append(n.Children[:i], n.Children[i+1:]...)
			return
		}
	}
}

func (n *GuiNode) appendTextNode(text string) *GuiNode {
	t := &GuiNode{Tag: "#text", Text: text, Props: map[string]object.Value{}}
	t.Parent = n
	n.Children = append(n.Children, t)
	return t
}

// runEffect 经 gx/solid 的 createEffect 建立响应式接线, 返回 dispose。
// solid 模块未注册 (纯 Go 单测无 VM) 时返回 nil 并立即执行一次 fn,
// 保证非响应环境下行为可预期。
func runEffect(fn func() object.Value) object.Value {
	exports, ok := object.LookupBuiltinModule("gx/solid")
	if !ok {
		fn()
		return nil
	}
	createEffect, ok := exports["createEffect"]
	if !ok || !object.IsCallable(createEffect) {
		fn()
		return nil
	}
	wrapper := object.NewBuiltin("gfx-bind", func(args ...object.Value) object.Value {
		return fn()
	})
	return object.CallFunction(createEffect, nil, wrapper)
}

// ===== 属性读取辅助 (布局/光栅化用) =====

// PropNum 读取数值属性 (不存在或类型不符返回 0, has=false)。
func (n *GuiNode) PropNum(name string) (float64, bool) {
	v, ok := n.Props[name]
	if !ok {
		return 0, false
	}
	if num, ok := v.(*object.Number); ok {
		return num.Value, true
	}
	return 0, false
}

// PropStr 读取字符串属性。
func (n *GuiNode) PropStr(name string) (string, bool) {
	v, ok := n.Props[name]
	if !ok {
		return "", false
	}
	if s, ok := v.(*object.String); ok {
		return s.Value, true
	}
	return "", false
}

// PropBool 读取布尔属性 (不存在或类型不符返回 false, has=false)。
func (n *GuiNode) PropBool(name string) (bool, bool) {
	v, ok := n.Props[name]
	if !ok {
		return false, false
	}
	if b, ok := v.(*object.Boolean); ok {
		return b.Value, true
	}
	return false, false
}

func (n *GuiNode) PropHandler(name string) object.Value {
	v, ok := n.Props[name]
	if !ok || !object.IsCallable(v) {
		return nil
	}
	return v
}

// ===== 文本相关辅助 =====

// FontSize 返回节点字号 (prop "font", 默认 16)。
func (n *GuiNode) FontSize() int {
	if v, ok := n.PropNum("font"); ok && v >= 8 {
		return int(v)
	}
	return 16
}

// TextContent 拼接直接或经 slot 间接的 #text 子节点文本 (text 元素的内容)。
// 动态文本挂在 slot 里 (见 wireReactiveChild), 布局与绘制都按同一棵树遍历,
// 这里也必须看穿 slot, 否则 `<text>{() => count()}</text>` 会渲染成空。
func (n *GuiNode) TextContent() string {
	if n.Tag == "#text" {
		return n.Text
	}
	s := ""
	for _, c := range n.Children {
		switch c.Tag {
		case "#text":
			s += c.Text
		case "slot":
			s += c.TextContent()
		}
	}
	return s
}

// ===== slot: 动态子节点的占位容器 (P1-2) =====
//
// slot 由 wireReactiveChild 内部创建, 不对应任何内置标签, JS 侧看不到。
// 布局语义是"透明": 单子时尺寸与交叉轴行为完全跟随子节点, 多子 (列表)
// 时按父容器的方向堆叠。

// slotChild 返回 slot 的唯一子节点 (非 slot / 空 / 多子时返回 nil)。
func (n *GuiNode) slotChild() *GuiNode {
	if n.Tag != "slot" || len(n.Children) != 1 {
		return nil
	}
	return n.Children[0]
}

// slotHorizontal 返回 slot 内多子节点的排布方向: 跟随父 flex 容器的方向,
// 于是同一个列表渲染写法放进 column 就是竖排, 放进 row 就是横排。
func (n *GuiNode) slotHorizontal() bool {
	return n.Parent != nil && n.Parent.Tag == "row"
}

// ===== 内置组件的属性语义 (布局与绘制共用) =====

// checked 读取受控组件的选中态。非受控: 控件自身不保存状态, 完全由 JS 的
// signal 经 checked prop 驱动 (与 Solid 的受控组件一致)。
func (n *GuiNode) checked() bool {
	v, _ := n.PropBool("checked")
	return v
}

// vertical 读取 separator 的方向 (默认横向)。
func (n *GuiNode) vertical() bool {
	v, _ := n.PropBool("vertical")
	return v
}

// disabledInChain 判断节点是否处于禁用子树内 (disabled 沿祖先链继承,
// 这样按钮内的文本子节点也会跟着变灰)。
func (n *GuiNode) disabledInChain() bool {
	for p := n; p != nil; p = p.Parent {
		if v, ok := p.PropBool("disabled"); ok && v {
			return true
		}
	}
	return false
}

// progressValue 读取 progress 的进度并钳位到 [0,1]。
func (n *GuiNode) progressValue() float64 {
	v, ok := n.PropNum("value")
	if !ok {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// buttonPadX / buttonPadY 是 button 缺省内边距 (水平 8px 为任务约定;
// 纵向给 6px 让默认外观有可点击的实体高度)。
const (
	buttonPadX = 8
	buttonPadY = 6
)

// buttonPadding 返回 button 的内容内边距: padding prop 优先, 缺省 8/6。
func (n *GuiNode) buttonPadding() (padX, padY int) {
	if v, ok := n.PropNum("padding"); ok {
		p := int(v)
		if p < 0 {
			p = 0
		}
		return p, p
	}
	return buttonPadX, buttonPadY
}

// ===== 交互反馈语义 (P1-4) =====

// hoverable 报告节点是否应呈现悬停反馈。只覆盖"有实体外观的交互组件":
// 普通盒子挂 onClick 时也可以被点, 但画面上没有可提亮的"面", 标脏只会
// 造成无谓重绘 (鼠标移动是频率最高的事件)。
func (n *GuiNode) hoverable() bool {
	switch n.Tag {
	case "button", "checkbox", "radio", "switch", "select", "select-option", "input":
		return true
	}
	return false
}

// pressable 与 hoverable 是同一批组件: 有"面"才谈得上按压反馈。
func (n *GuiNode) pressable() bool { return n.hoverable() }

// interactiveFace 叠加悬停/按压视觉反馈 (P1-4): 按住优先于悬停, 禁用态
// 不做反馈 (禁用按钮"按下去变深"只会让人以为可点)。
func (n *GuiNode) interactiveFace(c color.RGBA) color.RGBA {
	if n.disabledInChain() {
		return c
	}
	if n.pressed {
		return darken(c, 24)
	}
	if n.hovered {
		return brighten(c, 12)
	}
	return c
}

// faceColor 读取组件交互面颜色 (background prop → fallback) 并叠加
// 悬停/按压反馈, 供 button/checkbox/radio/switch 的绘制共用。
func (n *GuiNode) faceColor(fallback color.RGBA) color.RGBA {
	return n.interactiveFace(n.propColor("background", fallback))
}

// fieldFace 读字段类组件 (select / input) 的交互面颜色。
//
// 与 faceColor 的区别: 字段底色是纯白, 而"提亮"对 255 无从下手 (通道已经
// 顶到上限), 悬停会变成毫无反馈。这里改成轻微染色 —— 意图与 button 的
// 提亮/压暗一样 (悬停更亮、按压更沉), 只是换成白色能表达的方式。
func (n *GuiNode) fieldFace(fallback color.RGBA) color.RGBA {
	c := n.propColor("background", fallback)
	if n.disabledInChain() {
		return c
	}
	if n.pressed {
		return colorFieldPress
	}
	if n.hovered {
		return colorFieldHover
	}
	return c
}
