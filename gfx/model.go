package gfx

import (
	"sync"

	"github.com/14752222/Gox/object"
)

// ===== model: 受控组件的双向绑定糖 (v-model 等价物) =====
//
// ## 问题
//
// Gox 的受控组件是**完全受控**的: <input> 显示的文本只从 Props["value"] 读,
// 打字只是把新文本算出来然后派发 onInput —— 值真正落地要等脚本写回 signal。
// 于是"让一个输入框能用"至少是两件事:
//
//	<input value={() => draft()} onInput={(e) => setDraft(e.value)} />
//
// 两件事都能写错, 而且**都不报错**:
//   - value 写成 value={draft()} (漏了函数): 拿到的是第一帧的快照, prop 永不更新;
//   - 忘了写 onInput: 编辑结果无处可去 —— 表现是"输入框打不进字", 没有警告。
//
// ## 解
//
// model 把这两件事收成一条指令:
//
//	<input model={draft} />
//
// 语义按标签一张表, 没有例外 (读方向 prop / 写方向事件 / 写进去的值):
//
//	input · textarea  model ⇄ value      onInput({value})   字符串
//	slider            model ⇄ value      onInput({value})   数字 (控件给什么就是什么, 不做转换)
//	select            model ⇄ value      onChange({value})  字符串
//	checkbox · switch model ⇄ checked    onClick()          布尔 (写入 = 当前值取反)
//	radio             model ⇄ checked    onClick()          选中时把 value 属性写进 model
//	                  ^ checked 是**派生**的: model() === value, 互斥由"共用一个 model"天然成立
//
// model 收两种东西:
//
//	model={draft}        signal —— createSignal 的 getter 自带 setter (见 stdlib/solid.go)
//	model={[get, set]}   显式二元组 —— 自定义来源: 派生值 / 嵌套字段 / 别人的存储
//	                     (形状与 createSignal 的返回值一致, `const [v,setV] = ...` 原样转手即可)
//
// 自定义组件可以原样透传: `const MyField = (p) => <input model={p.model} />` ——
// setter 长在 getter 身上, 跟着函数值一起传, 不需要第二根线。
//
// ## 两条不静默的规则
//
//   - 同一标签同时给了 model 与 value/checked: **model 覆盖**, 并警告一次
//     (两个值来源同时存在是笔误, 不是配置);
//   - 同时给了 onInput/onChange/onClick: **两个都跑**, model 的写回在先, 脚本的
//     处理器在后 —— 于是 "存进 signal + 顺便做个副作用" 是合法组合:
//     `<input model={q} onInput={(e) => search(e.value)} />`
//
// 形状不合规时降级而不炸 (与内核一贯口径一致, 但这次**打 stderr 警告**):
//   - 可调用但没有 setter (memo / 手写取值函数): 只接读方向, 写方向不生效;
//   - 标量 (多半是漏了函数括号): 当静态值接上去, 界面照旧渲染, 只是写不回去;
//   - 不认识的形状 / 不支持的标签: 忽略该指令。
//
// ## 实现口径
//
// 整条指令是 **h() 里的一次 props 改写**, 不碰任何其它层: 补上去的 value/checked
// 是函数值 ⇒ 走原有的"响应式 prop"接线; 补上去的 onInput/onChange/onClick 以 on
// 开头 ⇒ 走原有的"事件回调"接线。input.go / textarea.go / slider.go / select.go /
// raster.go / layout.go 一行都不用改 —— 内核只是把"用户本来要手写的那两行"写好了。
//
// 也正因为是 h() 里的改写: h() 在每次重建时都会跑, 所以警告必须去重
// (modelWarnOnce), 否则热路径刷屏。

// ===== 属性读写 (Object 的 Properties 存的是描述符) =====

// modelProp 读 JS 属性对象里的一个键 (直接取 .Value, 与 gfx/view.go 的 viewProp
// 同一口径)。缺失返回 nil。
func modelProp(props *object.Object, name string) object.Value {
	if desc, ok := props.Properties[name]; ok {
		return desc.Value
	}
	return nil
}

// modelSetProp 覆盖/补一个属性, 显式 Writable=true: 这些键后面还有别的层要读
// (select 的展开逻辑会读 onClick, 事件分发读 onInput), 不能变成只读属性。
func modelSetProp(props *object.Object, name string, v object.Value) {
	props.Properties[name] = object.PropertyDescriptor{Value: v, Writable: true}
}

// modelHasProp 报告脚本自己有没有写这个键 (用于"两个值来源"的冲突警告)。
func modelHasProp(props *object.Object, name string) bool {
	_, ok := props.Properties[name]
	return ok
}

// ===== 绑定的解析 =====

// modelBinding 是一对绑定: 读方向 (必有) + 写方向 (可为 nil = 只读)。
type modelBinding struct {
	get object.Value
	set object.Value
}

// modelShape 描述 model= 收到的是什么形状 —— 决定"正常绑定"还是"降级 + 警告"。
type modelShape int

const (
	modelBound    modelShape = iota // signal 或 [get, set]: 读写都通
	modelReadOnly                   // 可调用但没带 setter (memo / 手写函数)
	modelScalar                     // 标量 (多半是漏了函数括号)
	modelInvalid                    // 完全不认识的形状
)

// modelFromValue 解 model= 的值。
//
// 形态 1 认 "getter 自带 .set": 这条约定由 createSignal 建立 (stdlib/solid.go 的
// newSignalPair), 是 model={draft} 能同时拿到读与写的唯一凭据。手写取值函数没有
// 它就天然是只读的 —— 这不是缺陷, 是诚实: 没有 setter 就写不回去。
func modelFromValue(v object.Value) (modelBinding, modelShape) {
	if v == nil {
		return modelBinding{}, modelInvalid
	}
	if object.IsCallable(v) {
		if s, ok := v.GetProperty("set"); ok && object.IsCallable(s) {
			return modelBinding{get: v, set: s}, modelBound
		}
		return modelBinding{get: v}, modelReadOnly
	}
	if arr, ok := v.(*object.Array); ok && len(arr.Elements) == 2 {
		g, s := arr.Elements[0], arr.Elements[1]
		if object.IsCallable(g) && object.IsCallable(s) {
			return modelBinding{get: g, set: s}, modelBound
		}
	}
	return modelBinding{}, modelScalar
}

// modelShapeText 把形状说成人话 (警告文案的一部分)。
func modelShapeText(s modelShape) string {
	switch s {
	case modelReadOnly:
		return "一个没有 setter 的函数 (memo / 手写取值函数)"
	case modelScalar:
		return "一个标量 (多半是漏了函数括号: 传 signal 本身, 不要写 draft())"
	}
	return "一个无法识别的值"
}

// modelRead 求值读方向。
//
// 双轨与 callScriptFn 同口径: Go 内建直接调 (不经 VM 回调桥), 脚本闭包走桥。
// 抛错记一条警告并按 undefined 处理 —— 与 gx/view 的 viewCall 同一策略:
// 一个坏取值的控件不该端掉整个窗口。
func modelRead(get object.Value) object.Value {
	if get == nil {
		return object.UndefinedSingleton
	}
	if bf, ok := get.(*object.BuiltinFunction); ok {
		if v := bf.Fn(); v != nil {
			return v
		}
		return object.UndefinedSingleton
	}
	v := object.CallFunction(get, nil)
	if err := takeCallbackErr(); err != nil {
		recordWarn("model: 取值函数抛错: %v", err)
		return object.UndefinedSingleton
	}
	if v == nil {
		return object.UndefinedSingleton
	}
	return v
}

// modelWrite 写回。setter 抛错记警告但不中断 (写回失败不该让事件分发挂掉)。
func modelWrite(set object.Value, val object.Value) {
	if set == nil {
		return
	}
	callScriptFn(set, val)
	if err := takeCallbackErr(); err != nil {
		recordWarn("model: 写回函数抛错: %v", err)
	}
}

// modelPickPayload 从事件载荷里取新值: input / textarea / slider / select 都派发
// 一个 `{value}` 对象, 这里原样转手 —— 类型由控件决定, model 不做任何转换
// (不做转换才不会偷偷把 "3" 变成 3, 也不会把布尔变成 "true")。
func modelPickPayload(arg object.Value) object.Value {
	if arg == nil {
		return object.UndefinedSingleton
	}
	if v, ok := arg.GetProperty("value"); ok {
		return v
	}
	return object.UndefinedSingleton
}

// modelToggle 造"取反"写值器: checkbox / switch 的 onClick **没有载荷**,
// 新值只能由当前值取反得到 (这也是它们必须双向绑定才顺手的根本原因)。
func modelToggle(get object.Value) func(object.Value) object.Value {
	return func(object.Value) object.Value {
		return object.NewBoolean(!viewTruthy(modelRead(get)))
	}
}

// ===== 展开 (h() 的入口) =====

// expandModelProp 把 model= 展开成该标签要的受控 prop。就地改 props:
// 删掉 model 本身 (它是指令, 不该留在 Props 里当数据), 补上读方向与写方向。
// 必须在 h() 的接线循环**之前**调用 —— 补上去的键要一起参与那两轮接线。
func expandModelProp(n *GuiNode, props *object.Object) {
	if !modelHasProp(props, "model") {
		return
	}
	raw := modelProp(props, "model")
	delete(props.Properties, "model")

	mb, shape := modelFromValue(raw)
	if shape == modelReadOnly || shape == modelScalar {
		modelWarnOnce("shape:"+n.Tag, "model: <%s> 收到了%s, 只接读方向 (写不回去)",
			n.Tag, modelShapeText(shape))
	}
	if shape == modelInvalid {
		modelWarnOnce("shape:"+n.Tag, "model: <%s> 收到无法识别的值, 该指令已忽略", n.Tag)
		return
	}

	// 读方向的值: 正常绑定用 getter (函数值 ⇒ 自动成为响应式 prop);
	// 降级时把标量当静态值接上, 界面至少能渲染出当前值。
	readVal := raw
	writable := shape == modelBound
	if shape == modelBound || shape == modelReadOnly {
		readVal = mb.get
	}

	switch n.Tag {
	case "input", "textarea", "slider":
		modelOverrideWarn(n.Tag, props, "value")
		modelSetProp(props, "value", readVal)
		modelSetProp(props, "onInput", modelEvent(props, "onInput", mb, writable, modelPickPayload))

	case "select":
		modelOverrideWarn(n.Tag, props, "value")
		modelSetProp(props, "value", readVal)
		modelSetProp(props, "onChange", modelEvent(props, "onChange", mb, writable, modelPickPayload))

	case "checkbox", "switch":
		modelOverrideWarn(n.Tag, props, "checked")
		modelSetProp(props, "checked", readVal)
		modelSetProp(props, "onClick", modelEvent(props, "onClick", mb, writable, modelToggle(mb.get)))

	case "radio":
		// radio 的 checked 是派生的 (model() === value), 所以 value 属性不能像别的
		// 标签那样被 model 覆盖 —— 它正是"选中时写回什么"的载荷。
		own := modelProp(props, "value")
		if own == nil {
			modelWarnOnce("radio:value", "model: <radio> 缺 value 属性 (选中时写回什么), 将恒不选中")
		}
		get := mb.get
		modelSetProp(props, "checked", object.NewBuiltin("model.radioChecked",
			func(args ...object.Value) object.Value {
				return object.NewBoolean(get != nil && viewSame(modelRead(get), own))
			}))
		modelSetProp(props, "onClick", modelEvent(props, "onClick", mb, writable,
			func(object.Value) object.Value { return own }))

	default:
		modelWarnOnce("tag:"+n.Tag,
			"model: 只支持受控组件 (input/textarea/slider/select/checkbox/switch/radio), 标签 %q 已忽略", n.Tag)
	}
}

// modelEvent 造写方向的事件处理器。
//
// 若脚本自己也写了同名处理器, **两个都跑**: 先写回 model, 再把载荷原样交给脚本
// 的处理器。覆盖掉脚本的处理器等于静默吞掉用户代码, 合成才既简洁又不损失表达力。
func modelEvent(props *object.Object, name string, mb modelBinding, writable bool,
	pick func(object.Value) object.Value) object.Value {

	user := modelProp(props, name)
	if user != nil && !object.IsCallable(user) {
		user = nil
	}
	set := mb.set
	if !writable {
		set = nil
	}
	return object.NewBuiltin("model."+name, func(args ...object.Value) object.Value {
		var arg object.Value
		if len(args) > 0 {
			arg = args[0]
		}
		if set != nil {
			modelWrite(set, pick(arg))
		}
		if user != nil {
			callScriptFn(user, arg)
			if err := takeCallbackErr(); err != nil {
				warnEventError(name, err)
			}
		}
		return object.UndefinedSingleton
	})
}

// ===== 警告 (去重) =====

var (
	modelWarnMu   sync.Mutex
	modelWarnSeen = map[string]struct{}{}
)

// modelWarnOnce 同一类警告只说一次: h() 在每次重建时都会跑 (列表行的渲染函数、
// 响应式分支换代), 不去重会在热路径上刷屏 —— 与 warnUnknownTagOnce 同一条纪律。
func modelWarnOnce(key, format string, args ...interface{}) {
	modelWarnMu.Lock()
	_, seen := modelWarnSeen[key]
	if !seen {
		modelWarnSeen[key] = struct{}{}
	}
	modelWarnMu.Unlock()
	if !seen {
		recordWarn(format, args...)
	}
}

// resetModelWarns 清空去重表 (仅测试用: 警告环是跨用例共享的进程级状态)。
func resetModelWarns() {
	modelWarnMu.Lock()
	modelWarnSeen = map[string]struct{}{}
	modelWarnMu.Unlock()
}

// modelOverrideWarn 报告"model 与显式 value/checked 同时存在"。
// 这是笔误 (两个值来源), 不是配置 —— 所以覆盖要出声。
func modelOverrideWarn(tag string, props *object.Object, other string) {
	if modelHasProp(props, other) {
		modelWarnOnce("dup:"+tag+":"+other,
			"model: <%s> 同时给了 model 与 %s, 以 model 为准", tag, other)
	}
}
