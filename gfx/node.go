package gfx

import (
	"image/color"
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

	// cleanups 记录响应式子树 (slot) 各代登记的 onCleanup 回调 (gx/solid)。
	// 只会挂在 slot 节点上: 重建时由 wireReactiveChild 在拆树前执行,
	// 整树销毁时由 disposeNode 执行。见 object/wiring.go 的机制说明。
	cleanups []object.Value

	// 运行时交互状态: 渲染层专有, 不来自 props, 也不暴露给脚本。
	// hovered/pressed 由 Pump 按鼠标事件维护 (P1-4), 绘制时读。
	hovered bool
	pressed bool

	// 下拉框状态 (P2-3): expanded 是展开态, popup 是挂在 select 下的弹层
	// 子节点 (未展开为 nil), highlight 是键盘光标的选项下标 (-1 = 无)。
	expanded  bool
	popup     *GuiNode
	highlight int

	// 菜单状态 (P3-5): 复用 expanded 表达"下拉是否打开", 另有两组专职字段。
	//   - menuPopup 是挂在 menu 下的下拉弹层 (menu-popup);
	//   - menuHighlight 是键盘光标的项下标 (-1 = 无);
	//   - ctxMenu/ctxX/ctxY 标记"这是一个就地弹出的右键菜单"及其落点;
	//   - menuIndex/menuOwner/menuLabel/... 是**弹出层行**的自有数据 ——
	//     菜单项的文字是数据而不是元素 (脚本写 <menuitem label="Save">),
	//     所以不像 select-option 那样有 #text 子节点可画。
	menuPopup     *GuiNode
	menuHighlight int
	ctxMenu       bool
	ctxX, ctxY    int

	menuIndex        int
	menuOwner        *GuiNode
	menuLabel        string
	menuShortcutText string
	menuDisabled     bool
	menuSep          bool
	menuSub          bool
	menuChildMenu    *GuiNode
	menuDirDown      bool

	// 下拉项状态: 在兄弟中的下标, 以及所属 select (绘制高亮时要回查
	// highlight, 用指针比"沿 Parent 走两层"更稳 —— 弹层一旦被拆链就找不到)。
	optIndex int
	owner    *GuiNode

	// 输入框状态 (P2-1): focused 是"是否持有键盘焦点"(由 setFocus 维护,
	// 绘制时决定边框颜色与是否画光标), caret 是光标位置 (rune 下标)。
	// textarea (P2-6) 复用这两个字段: caret 表示"列", caretLine 表示"行"
	// (单行 input 永远不用 caretLine —— 一行没有行号可言)。
	focused   bool
	caret     int
	caretLine int

	// 滚动状态 (P2-5): offsetY 是当前纵向偏移, contentH 是上一次布局测出的
	// 内容总高 (两者都由布局/滚轮维护, 不来自 props)。
	// textarea (P2-6) 只用 offsetY 做内部纵向滚动 (行数直接由 value 数出来,
	// 不需要 contentH 缓存)。
	offsetY  int
	contentH int

	// canvas 自绘回调 (P3-1): onDraw 是脚本给的函数, 每次绘制时用一个
	// 落笔 ctx 调用一次 (依赖收集另有一遍空跑, 见 canvas.go 的说明)。
	onDraw object.Value

	// 拖动状态 (P2-8): slideVal 是**上一次派发出去的**量化值, slideValSet
	// 区分"还没派发过"与"上一次恰好是 0"。用它挡掉"鼠标每移一像素就派发一次
	// 相同的 value" —— 注意不能拿 value prop 来比: 脚本不回写时 prop 永远不变,
	// 那等于没挡。
	slideVal    float64
	slideValSet bool

	// 过渡动画状态 (P3-2): prop 名 → 进行中的补间。nil = 该节点无动画。
	// **注意受控模型的这个分离**: 一旦起动画, Props[prop] 就已被写成**终值**,
	// 布局/绘制必须读 effectivePropNum (动画期间只认 animState), 否则会跳变。
	anim map[string]*animState
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
	// 布局容器 (P3 / §四): 纵横堆叠 + 折行 + 等宽网格
	"column": {}, "row": {}, "grid": {},
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
	// P2-5 滚动容器
	"scroll": {},
	// P2-6 多行文本: textarea (编辑器) + text 的 wrap/ellipsis 已在 text 上
	"textarea": {},
	// P2-9 图片
	"image": {},
	// P3-1 自绘画布
	"canvas": {},
	// P2-8 滑块
	"slider": {},
	// P3-5 菜单栏与右键菜单 (menu-popup / menu-item 由 Go 侧构造, 脚本写不到)
	"menubar": {}, "menu": {}, "menuitem": {},
	"menu-popup": {}, "menu-item": {},
	// 窗口根元素: 只在 render() 的根位置有意义 (拆包成窗口配置, 不进树),
	// 登记在这里是为了让 h() 不对它的正常用法报未知标签警告
	"window": {},
	// view: **布局透明的容器** (Fragment / Vue 的 <template>)。语义与内核内部的
	// slot 占位节点完全相同 —— 单子时尺寸与交叉轴行为完全跟随子节点, 多子时按
	// 父容器方向堆叠, 自己不占盒子; 想装饰 (background/border) 也能画。
	// 它是 each / show 指令的"无盒子模板": <view each={rows}> 等价于旧的 <For>,
	// 而 <row each={rows}> 会为每一项多出一层 row 盒子 (那是它字面上的意思)。
	"view": {},
}

var (
	warnMu       sync.Mutex
	warnSeenTags = map[string]struct{}{}
)

// warnUnknownTag 是未知标签的警告出口。做成变量而非直接写 stderr,
// 便于单测替换为计数器断言 (同一标签只警告一次的行为见 warnUnknownTagOnce;
// 默认出口经 recordWarn 进警告环形缓冲, gx/dev 可读)。
var warnUnknownTag = func(tag string) {
	recordWarn("unknown tag %q (rendered as a plain box; see docs/gui-component-status.md)", tag)
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
	//
	// **两轮接线, 静态值优先** —— 这不是微优化, 而是语义要求:
	// Go 的 map 迭代顺序是随机的, 而响应式 prop (函数值) 会**在接线当场
	// 立刻跑一次 effect**。若先接 `width={w}` 再接 `transition={{width:150}}`,
	// 那次 effect 读到的 transition 还是空的, 属性就被当成"不过渡"直接跳变了
	// —— 而且这个 bug 是**随机复现**的 (取决于 map 顺序), 最难查。
	// 静态 prop (含 transition / min / max 这些"配置项") 必须全部就位,
	// 再跑任何 effect。
	if len(args) > 1 {
		if props, ok := args[1].(*object.Object); ok {
			// 元素级指令 (each / show) 先展开 (directive.go): 它们决定"这棵树长
			// 什么样" (复制元素 / 决定建不建), 所以排在一切接线之前。返回非 nil
			// 就表示这个元素被指令接管了 —— 元素副本由指令自己造, 这里直接收工。
			if host := expandElementDirective(tagStr.Value, props, args[2:]); host != nil {
				return host
			}
			// model= 再展开成该标签的受控 prop (model.go): 它补上去的
			// value/checked 与 onInput/onChange/onClick 要一起参与下面两轮接线,
			// 所以必须排在这之前 —— 顺序错了这两个键就白补了。
			expandModelProp(node, props)
			for _, reactive := range []bool{false, true} {
				for name, desc := range props.Properties {
					if isReactiveProp(name, desc.Value) != reactive {
						continue
					}
					node.wireProp(name, desc.Value)
				}
			}
		}
	}

	// children
	// props 参数可省 (h("column") 与 h("column", null) 等价): 只有 tag 时
	// args[2:] 会越界, JSX 不会生成这种调用, 但手拼脚本会。
	if len(args) > 2 {
		for _, c := range args[2:] {
			node.wireChild(c)
		}
	}
	// 需要内置交互的标签在这里补上 Go 侧处理器 (select 的展开、菜单的展开)。
	// 放在 props/children 都接好之后: 包装脚本自己的 onClick 时要能读到它。
	if node.Tag == "select" {
		attachSelectHandler(node)
	}
	// 只有菜单栏直属的 menu 才是"点标题展开下拉"的一级菜单; 嵌在 menuitem
	// 里的 menu 是子菜单, 由它的父项驱动展开 (见 openSubmenu)。
	if node.Tag == "menu" {
		if node.Parent != nil && node.Parent.Tag == "menubar" {
			attachMenuHandler(node)
		}
	}
	return node
}

// wireProp 接线一个属性:
//   - on* 开头且值为函数 → 事件回调, 原样存储
//   - 其他函数值 → 响应式 prop: createEffect 求值写回
//   - 其余 → 静态值
func (n *GuiNode) wireProp(name string, val object.Value) {
	// onDraw 必须排在最前: 它虽然也以 "on" 开头, 但语义不是"事件回调"
	// (没有事件源), 而是"响应式绘制函数" —— 要包 effect 收集依赖, 不能
	// 当成普通 prop 存下来 (存下来就只是躺着, 画布永远不刷新)。
	// 也不能落到下面的 reactiveProp: 那会把 onDraw 的**返回值**写回 Props,
	// 而我们要的是"函数体执行一遍以登记 signal 依赖"。
	if name == "onDraw" {
		n.wireDraw(val)
		return
	}
	if isReactiveProp(name, val) {
		n.reactiveProp(name, val)
		return
	}
	n.Props[name] = val
}

// isReactiveProp 报告这个 prop 会不会走 reactiveProp (包 effect 求值)。
//
// **必须与 wireProp 的分派条件保持一致** (两处判定同一个语义, 改动时要一起改):
// 函数值 + 非事件属性才算响应式; `onDraw` 虽然也是函数, 但它走 wireDraw
// 这条专门通道 (见 wireProp 顶部的说明), 也不算"普通响应式 prop"。
//
// h() 用它做两轮接线 (静态优先) 的分堆判断 —— 判定若与 wireProp 分叉,
// 某些 prop 会在两轮里都被跳过 (直接丢失), 或都被接两次 (effect 重复注册)。
func isReactiveProp(name string, val object.Value) bool {
	return object.IsCallable(val) && name != "onDraw" && !isEventPropName(name)
}

// isEventPropName 判断是否事件回调属性 (Solid 约定: on 开头)。
func isEventPropName(name string) bool {
	return len(name) > 2 && name[0] == 'o' && name[1] == 'n'
}

// reactiveProp 用 createEffect 包一层响应式属性: 求值 → 写回 Props → 标脏。
//
// P3-2 起多一条分流: 若该属性在 animatableProps 里**且**节点配了 transition,
// 就不直接跳过去 —— 以"当前显示值"为起点起一段过渡 (startTransition 内部
// 会把 Props 写成终值)。这样 `<rect transition={{width:150}} width={w()}>`
// 在 w() 变化时才是平滑的, 而不是"一帧跳到位"。
func (n *GuiNode) reactiveProp(name string, getter object.Value) {
	dispose := runEffect(func() object.Value {
		v := object.CallFunction(getter, nil)
		if n.startPropTransition(name, v) {
			// 已由过渡接管显示值: Props 已被写成终值, 这里只需记账
			return object.UndefinedSingleton
		}
		n.Props[name] = v
		markNodeDirty(n)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		n.effects = append(n.effects, dispose)
	}
}

// startPropTransition 在"属性可动画 + 节点配了 transition + 新值是数字"
// 三条同时成立时启动过渡, 并返回 true (调用方就不要再写回/标脏了)。
//
// 为什么把判断收在这里而不是塞进 reactiveProp 的内联 if: 这段逻辑要读
// 三个来源 (属性白名单 / transition 配置 / 目标值类型), 内联进 effect 闭包
// 会把"响应式写回"这条主线上最该保持直观的一段搅浑。
func (n *GuiNode) startPropTransition(name string, v object.Value) bool {
	if !animatableProps[name] || !n.hasTransition() {
		return false
	}
	target, ok := v.(*object.Number)
	if !ok {
		// 目标不是数字 (脚本传了字符串/undefined): 老老实实按原路径写回。
		// animState 只做数值插值, 硬塞进去只会得到 NaN 几何。
		return false
	}
	// startTransition 负责"先读显示值、再写 prop、最后起表"这个顺序,
	// 调用方不必(也不能)自己提前写 prop —— 那会让 from 读成终值。
	startTransition(n, name, target.Value)
	return true
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
		if arr, ok := val.(*object.Array); ok {
			// 静态数组子节点 (JSX 里的 <scroll>{rows}</scroll> 这种): 逐个展开,
			// 与 mountValue 的列表渲染语义保持一致。**必须在这里展开** ——
			// 否则整个数组会走 ToString 变成一行文本 ("[object Object],..."),
			// 表现为"塞了 20 行却只渲染出一行"。
			for _, e := range arr.Elements {
				n.wireChild(e)
			}
			return
		}
		// 其余类型: 退化为文本
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
		// 接线作用域: getter 求值期间 (组件体在这里跑) 调用的 onMount/onCleanup
		// 登记到 sc, 归属"这一代"子树。见 object/wiring.go。
		sc := object.PushWiringScope()
		v := object.CallFunction(getter, nil)
		if node, ok := v.(*GuiNode); ok && node == mounted {
			// 同一元素对象: 组件体没有重跑 (vnode 在闭包外创建), 本次没有
			// 新登记 —— 出栈即丢弃, 语义正确。
			object.PopWiringScope()
			return object.UndefinedSingleton
		}
		if text, ok := scalarString(v); ok && textNode != nil &&
			len(slot.Children) == 1 && slot.Children[0] == textNode {
			if textNode.Text != text {
				textNode.Text = text
				markNodeDirty(textNode)
			}
			object.PopWiringScope()
			return object.UndefinedSingleton
		}
		// 先跑上一代的 onCleanup 再拆树: 清理回调可能还要读一眼即将销毁的
		// 子树 (顺序反过来它看到的就是空树)。
		runCleanups(slot)
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
		object.PopWiringScope()
		// 出栈后消费登记表: Mounts 立即执行 (子树已挂上); Cleanups 存进
		// slot, 等下一次重建或整树销毁。
		slot.cleanups = append(slot.cleanups, sc.Cleanups...)
		for _, fn := range sc.Mounts {
			object.CallFunction(fn, nil)
			if err := takeCallbackErr(); err != nil {
				recordWarn("onMount error: %v", err)
			}
		}
		return object.UndefinedSingleton
	})
	if dispose != nil {
		slot.effects = append(slot.effects, dispose)
	}
}

// runCleanups 逆序执行并清空节点登记的 onCleanup 回调 (后注册的先执行,
// 与栈式解构一致), 异常打日志不中断。
func runCleanups(n *GuiNode) {
	if len(n.cleanups) == 0 {
		return
	}
	for i := len(n.cleanups) - 1; i >= 0; i-- {
		object.CallFunction(n.cleanups[i], nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("onCleanup error: %v", err)
		}
	}
	n.cleanups = nil
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
	// onCleanup (gx/solid) 与 effect dispose 同一时刻执行: 子树离开树了,
	// 组件登记的生命周期清理就该跑 (slot 上的登记见 wireReactiveChild)。
	runCleanups(n)
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
	// 菜单 (P3-5) 同理: 下拉弹层是子节点, 已由上面那轮递归销毁, 这里断指针。
	n.menuPopup = nil
	n.menuChildMenu = nil
	n.menuOwner = nil
	n.menuHighlight = -1
	// 输入框状态: 节点离树后不该再被当成"持有焦点" (否则 setFocus 的
	// 旧节点标脏会对一个游离节点做无意义的重绘)。
	n.focused = false
	n.caret = 0
	n.caretLine = 0
	// 滚动状态同样复位 (偏移属于"这一棵子树自己的视图状态")
	n.offsetY = 0
	n.contentH = 0
	// canvas 的 onDraw 一并断开: 节点离树后它的 effect 已被注销 (上面那轮),
	// 留着这个引用只会让脚本函数对象多活一轮 GC, 没有别的意义。
	n.onDraw = nil
	// 拖动值缓存复位 (节点可能正被拖着时就被卸载了)
	n.slideVal = 0
	n.slideValSet = false
	// 过渡动画 (P3-2) 一并摘掉: 心跳定时器只认 animNodes 里的节点,
	// 离树节点留在表里会被每帧标脏 (而且标的是脏矩形流程看不见的节点)。
	cancelAnim(n)
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

// PropHas 报告该属性是否存在 (不关心类型)。
func (n *GuiNode) PropHas(name string) bool {
	if n == nil {
		return false
	}
	_, ok := n.Props[name]
	return ok
}

func (n *GuiNode) PropHandler(name string) object.Value {
	v, ok := n.Props[name]
	if !ok || !object.IsCallable(v) {
		return nil
	}
	return v
}

// ===== 文本相关辅助 =====

// FontSize 返回节点字号 (prop "font", 未写则**沿父链继承**, 都没有才落默认 16)。
//
// 为什么继承: `#text` 节点自身永远不带 font prop (它是内容载体, 由 JSX 的字符串
// 生成), 所以"不继承"等于父元素上写的 font 一辈子不生效 —— 典型症状是
// `<button font={13}>标签</button>` 的标签仍按 16 画 (kit_demo 就是这么写的),
// 而且测量 (intrinsicSize) 与绘制 (drawNode) 都走同一个 FontSize(), 所以两边
// 一致地"错", 不会自己暴露出来。
//
// 继承是"最近祖先优先": 显式 font 就近生效, 中间任何一层都能覆盖。
func (n *GuiNode) FontSize() int {
	if v, ok := n.PropNum("font"); ok && v >= 8 {
		return int(v)
	}
	for p := n.Parent; p != nil; p = p.Parent {
		if v, ok := p.PropNum("font"); ok && v >= 8 {
			return int(v)
		}
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
		case "slot", "view":
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
//
// 2026-09-20: 同一套语义对外开了一个公开标签 `view` (见 knownTags 与
// isPassthrough) —— 脚本需要一个"不占盒子"的容器时用它, each / show 指令
// 也需要它来保持"列表宿主 / 条件分支不凭空多一个盒子"这条既有承诺。

// isPassthrough 报告节点是不是**布局透明的容器**: 内部 slot 占位节点与公开的
// view 标签走同一套透明语义 (尺寸跟随单子 / 多子按父向堆叠 / gap 缺省跟随父容器 /
// stretch 委托给单子)。所有原来写 `n.Tag == "slot"` 的地方都该用这个判定。
func (n *GuiNode) isPassthrough() bool {
	return n.Tag == "slot" || n.Tag == "view"
}

// slotChild 返回透明容器的唯一子节点 (非透明容器 / 空 / 多子时返回 nil)。
func (n *GuiNode) slotChild() *GuiNode {
	if !n.isPassthrough() || len(n.Children) != 1 {
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
	case "button", "checkbox", "radio", "switch", "select", "select-option", "input", "textarea", "slider",
		"menu", "menu-item":
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
