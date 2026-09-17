package gfx

import (
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

	// effects 记录本节点接线的 effect dispose 函数 (v1 节点常驻不销毁,
	// 仅测试断言用)
	effects []object.Value
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
//   - 函数 → 响应式文本子节点 (effect 求值写回 Text, Solid 的 computed 约定)
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
	default:
		if object.IsCallable(val) {
			// 响应式子节点: 求值结果按标量文本处理 (v1 不支持返回元素)
			textNode := n.appendTextNode("")
			dispose := runEffect(func() object.Value {
				v := object.CallFunction(val, nil)
				textNode.Text = scalarText(v)
				markNodeDirty(textNode)
				return object.UndefinedSingleton
			})
			if dispose != nil {
				textNode.effects = append(textNode.effects, dispose)
			}
			return
		}
		// 数组等其余类型: 展开为文本 (v1 简化)
		n.appendTextNode(object.ToString(val))
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

// scalarText 把标量值转为文本 (用于文本子节点)。
func scalarText(v object.Value) string {
	switch v.(type) {
	case *object.Null:
		return "null"
	case *object.Undefined:
		return ""
	}
	return object.ToString(v)
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

// PropHandler 读取事件回调属性。
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

// TextContent 拼接直接 #text 子节点的文本 (text 元素的内容)。
func (n *GuiNode) TextContent() string {
	if n.Tag == "#text" {
		return n.Text
	}
	s := ""
	for _, c := range n.Children {
		if c.Tag == "#text" {
			s += c.Text
		}
	}
	return s
}
