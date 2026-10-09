package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupWellKnownSymbolMembers 装配内建对象的 well-known Symbol 成员
// (看板 rYFTlt 阶段一: "内建对象的 Symbol 键成员读不到")。
//
// 背景: 此前只有少数显式注册过的 Symbol 成员可读 (Math/JSON/Symbol.prototype
// 的 @@toStringTag、Date.prototype 的 @@toPrimitive), 而原型上的 @@iterator、
// 构造器上的 @@species、Symbol.prototype 的 @@toPrimitive、Function.prototype
// 的 @@hasInstance 全部读不到 —— 于是 `arr[Symbol.iterator]()` 直接 TypeError,
// `typeof Array[Symbol.species]` 是 undefined。
//
// 为什么必须放在 SetupGlobals 的最后: 各构造器与原型对象此刻才全部注册完
// 毕, 才能从环境里取回它们; 且 @@iterator 要**复用**已有的 values/entries
// 方法对象 (见下), 那两个方法也必须先就位。
//
// 为什么 @@iterator 不新写实现: 规范里
//   - %Array.prototype%[@@iterator] 的初始值**就是** %Array.prototype.values%
//   - %Set.prototype%[@@iterator]   的初始值**就是** %Set.prototype.values%
//   - %Map.prototype%[@@iterator]   的初始值**就是** %Map.prototype.entries%
//
// 它们是同一个函数对象 (`Array.prototype[Symbol.iterator] ===
// Array.prototype.values` 必须为 true, test262 有断言)。直接引用既有方法
// 既符合规范, 也顺带保证 this 校验 / 迭代器构造行为与 .values() 完全一致。
func setupWellKnownSymbolMembers(env *runtime.Environment) {
	setupSymbolIteratorMembers(env)
	setupSymbolSpeciesMembers(env)
	setupSymbolToPrimitiveMember()
	setupSymbolHasInstanceMember()
}

// ctorProtoOf 从环境里取回某个全局构造器的 prototype 对象。
func ctorProtoOf(env *runtime.Environment, name string) (object.Value, bool) {
	v, ok := env.Get(name)
	if !ok || v == nil {
		return nil, false
	}
	p, ok := v.GetProperty("prototype")
	if !ok || p == nil {
		return nil, false
	}
	return p, true
}

// linkIteratorMember 把已有方法对象 (values / entries) 同时注册为 @@iterator。
func linkIteratorMember(proto object.Value, sym *object.Symbol, method string) {
	if proto == nil {
		return
	}
	m, ok := proto.GetProperty(method)
	if !ok || !object.IsCallable(m) {
		return
	}
	object.SetWellKnownSymbolMethod(proto, sym, m)
}

// setupSymbolIteratorMembers 装配 @@iterator。
func setupSymbolIteratorMembers(env *runtime.Environment) {
	sym := object.GetGlobalSymbol("Symbol.iterator")

	// %Array.prototype%[@@iterator] = %Array.prototype.values%
	if p, ok := ctorProtoOf(env, "Array"); ok {
		linkIteratorMember(p, sym, "values")
	}
	// %Set.prototype%[@@iterator] = %Set.prototype.values%
	if p, ok := ctorProtoOf(env, "Set"); ok {
		linkIteratorMember(p, sym, "values")
	}
	// %Map.prototype%[@@iterator] = %Map.prototype.entries%
	if p, ok := ctorProtoOf(env, "Map"); ok {
		linkIteratorMember(p, sym, "entries")
	}

	// %String.prototype%[@@iterator] —— String.prototype 没有 values(),
	// 规范里它是独立的 %String.prototype%[@@iterator], 返回 %StringIterator%
	// (按码点迭代, 而非 UTF-16 码元)。此处独立实现。
	if p, ok := ctorProtoOf(env, "String"); ok {
		object.SetWellKnownSymbolMethod(p, sym,
			object.NewBuiltinMethod("[Symbol.iterator]", func(this object.Value, args ...object.Value) object.Value {
				s, ok := this.(*object.String)
				if !ok {
					return thisTypeError("String", "[Symbol.iterator]", this)
				}
				return object.NewStringCodePointIterator(s)
			}))
	}
}

// setupSymbolSpeciesMembers 装配 @@species。
//
// 规范形态是**访问器**而非数据属性: get [@@species]() { return this; },
// 无 setter, { enumerable: false, configurable: true }。写成数据属性会让
// `Array[Symbol.species]` 返回 Array 但 `Sub[Symbol.species]` 也返回 Array
// (应为 Sub) —— 子类化 (Array.prototype.map 等返回派生实例) 全错。
func setupSymbolSpeciesMembers(env *runtime.Environment) {
	sym := object.GetGlobalSymbol("Symbol.species")
	getter := object.NewBuiltinMethod("get [Symbol.species]",
		func(this object.Value, args ...object.Value) object.Value {
			if this == nil {
				return object.UndefinedSingleton
			}
			return this
		})
	for _, name := range []string{"Array", "Map", "Set", "Promise"} {
		v, ok := env.Get(name)
		if !ok || v == nil {
			continue
		}
		object.SetWellKnownSymbolAccessor(v, sym, getter)
	}
}

// setupSymbolToPrimitiveMember 装配 %Symbol.prototype%[@@toPrimitive]。
//
// 规范 20.4.3.5: 返回 thisSymbolValue(this) —— 这就是 Symbol 在
// ToPrimitive 里不被转成字符串的原因 (`${sym}` 抛 TypeError, 但
// `+sym` 之外的隐式转换路径走到这里拿到原值)。
func setupSymbolToPrimitiveMember() {
	sym := object.GetGlobalSymbol("Symbol.toPrimitive")
	p := object.GetSymbolProto()
	if p == nil {
		return
	}
	object.SetWellKnownSymbolMethod(p, sym,
		object.NewBuiltinMethod("[Symbol.toPrimitive]", func(this object.Value, args ...object.Value) object.Value {
			// 规范 20.4.3.5 的第一步是 thisSymbolValue(this value), 不是"原样
			// 返回 this": Symbol 原值返回自身; Symbol wrapper object 返回被
			// 包装的 Symbol; **其余一律 TypeError**。
			//
			// 少了这层校验, `Symbol.prototype[Symbol.toPrimitive].call({})`
			// 会静默返回那个普通对象 —— test262
			// Symbol/prototype/Symbol.toPrimitive/this-val-obj-non-symbol-wrapper.js
			// 正是断言它必须抛。
			if s, ok := this.(*object.Symbol); ok {
				return s
			}
			return thisTypeError("Symbol", "[Symbol.toPrimitive]", this)
		}))
}

// setupSymbolHasInstanceMember 装配 %Function.prototype%[@@hasInstance]。
//
// 规范 20.2.3.8: Function.prototype[@@hasInstance](V) 即 OrdinaryHasInstance
// (this, V)。注意它刻意**不**递归查 @@hasInstance —— 否则用户覆写会无限
// 递归。instanceof 运算符是否改走本方法属于独立议题 (会改变既有运算符
// 行为), 本提交只补方法本身。
func setupSymbolHasInstanceMember() {
	sym := object.GetGlobalSymbol("Symbol.hasInstance")
	p := object.GetFunctionPrototype()
	if p == nil {
		return
	}
	object.SetWellKnownSymbolMethod(p, sym,
		object.NewBuiltinMethod("[Symbol.hasInstance]", func(this object.Value, args ...object.Value) object.Value {
			var v object.Value = object.UndefinedSingleton
			if len(args) > 0 {
				v = args[0]
			}
			return ordinaryHasInstance(this, v)
		}))
}

// ordinaryHasInstance 实现规范 7.3.19 OrdinaryHasInstance(C, O)。
// 与 vm.instanceOf 共享 object.ProtoOf 这条原型链遍历路径。
func ordinaryHasInstance(c, o object.Value) object.Value {
	// 1. C 不可调用 ⇒ false
	if !object.IsCallable(c) {
		return object.NewBoolean(false)
	}
	// 2-3. O 不是对象 ⇒ false (且不读 C.prototype, 不触发 getter)
	if !object.IsObjectLike(o) {
		return object.NewBoolean(false)
	}
	// 4. 取 C.prototype
	var ctorProto object.Value
	switch t := c.(type) {
	case *object.Object:
		ctorProto, _ = t.GetProperty("prototype")
	case *object.BuiltinFunction:
		ctorProto, _ = t.GetProperty("prototype")
	case *object.Closure:
		ctorProto, _ = t.GetProperty("prototype")
	}
	if ctorProto == nil {
		return object.NewBoolean(false)
	}
	// 5. 沿 O 的原型链找 C.prototype
	for i, cur := 0, o; i < 64 && cur != nil; i++ {
		if cur == ctorProto {
			return object.NewBoolean(true)
		}
		cur = object.ProtoOf(cur)
	}
	// 与 instanceof 运算符同口径的兜底: 部分内置类型 (*Map / *Set / *Promise ...)
	// 不在 object.ProtoOf 的类型穷举里, 原型链遍历会提前断掉, 只有名称匹配能
	// 给出正确结论。少了这条, `new Map() instanceof Map` 为 true 而
	// `Map[Symbol.hasInstance](new Map())` 为 false —— 同一个问题的两个答案。
	return object.NewBoolean(object.MatchBuiltinType(o, c))
}
