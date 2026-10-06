package stdlib

import (
	"math"
	"strconv"

	"github.com/14752222/Gox/object"
)

// setupObjectPrototype 构建 Object.prototype 及其标准方法。
//
// 此前的注册流程里 Object 构造器根本没有 prototype 属性: Object.prototype
// 求值结果是 undefined，Object.prototype.toString.call(...) 这类反射写法
// 因而全部失效。这不是"某个方法缺 .call"，而是整条 Object.prototype 链
// 都不存在 —— 先有 prototype，才谈得上取上面的方法。
func setupObjectPrototype(o *object.BuiltinFunction) {
	proto := object.NewObject()

	// constructor: 反向引用 (与 Array/String 的 proto 结构保持一致)
	proto.SetBuiltinProperty("constructor", o)

	// toString(): 输出 "[object Tag]"。
	// 它接收任意 this —— 通过 .call/.apply 反射时可以作用于任何值，
	// 因此实现必须对所有内置类型有标签，而不能假设 this 是 *object.Object。
	//
	// 格式为规范的 "[object " + tag + "]" (小写 object + 空格)。此前实现输出
	// "[" + tag + "]" —— 丢掉 "object " 前缀，导致
	// Object.prototype.toString.call({}) === "[Object]"，与规范要求的
	// "[object Object]" 不符 (built-ins/Object/prototype/toString/* 全族失败)。
	proto.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if revokedProxyIn(this) {
			return object.NewTypeError("Cannot perform 'IsArray' on a proxy that has been revoked")
		}
		return object.NewString(objectPrototypeStringOf(this))
	}))

	// toLocaleString: 本运行时无 Intl，语义与 toString 相同。
	proto.SetBuiltinProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		if revokedProxyIn(this) {
			return object.NewTypeError("Cannot perform 'IsArray' on a proxy that has been revoked")
		}
		return object.NewString(objectPrototypeStringOf(this))
	}))

	// valueOf(): 返回对象本身。
	proto.SetBuiltinProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if this == nil {
			return object.UndefinedSingleton
		}
		return this
	}))

	// hasOwnProperty(): 只查自有属性，不沿原型链。
	proto.SetBuiltinProperty("hasOwnProperty", object.NewBuiltinMethod("hasOwnProperty", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(hasOwnPropertyImpl(this, args[0]))
	}))

	// propertyIsEnumerable(): 自有且 Enumerable=true 的属性。
	proto.SetBuiltinProperty("propertyIsEnumerable", object.NewBuiltinMethod("propertyIsEnumerable", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(propertyIsEnumerableImpl(this, args[0]))
	}))

	// isPrototypeOf(): 检查 this 是否出现在参数的原型链上。
	proto.SetBuiltinProperty("isPrototypeOf", object.NewBuiltinMethod("isPrototypeOf", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(isPrototypeOfImpl(this, args[0]))
	}))

	o.SetProperty("prototype", proto)
}

// objectPrototypeStringOf 返回 Object.prototype.toString 的完整结果:
// 规范的 "[object " + tag + "]"。所有走 toString/toLocaleString 的路径
// 统一经此，避免前缀格式在多处漂移。
func objectPrototypeStringOf(v object.Value) string {
	return "[object " + objectPrototypeTagFor(v) + "]"
}

// toStringTagSymbol 是 Symbol.toStringTag 全局符号 (与 JS 侧 Symbol.toStringTag 同一实例)。
func toStringTagSymbol() *object.Symbol {
	return object.GetGlobalSymbol("Symbol.toStringTag")
}

// revokedProxyIn 判断值是否为"已撤销代理"(可嵌套)。规范中 IsArray /
// Get(@@toStringTag) 遇到已撤销代理一律抛 TypeError，必须先于标签解析检查。
func revokedProxyIn(v object.Value) bool {
	for i := 0; i < 64; i++ {
		p, ok := v.(*object.Proxy)
		if !ok {
			return false
		}
		if p.IsRevoked {
			return true
		}
		v = p.Target
	}
	return false
}

// objectPrototypeTagFor 返回 Object.prototype.toString 所用的标签
// (不含 "[object " 前缀与 "]" 后缀)。
//
// 规范 Object.prototype.toString (ES2024 20.1.3.6) 的顺序:
//  1. this 是 undefined / null → 直接返回 "Undefined" / "Null";
//  2. tag = Get(O, @@toStringTag); 若为字符串则用它 (优先级最高);
//  3. 否则用 builtinTag (Array / Function / Error / Number / ... / Object)。
//
// 注意 @@toStringTag 是**沿原型链**查找的: Map 实例的 "Map" 标签其实来自
// %Map.prototype%[@@toStringTag]，因此 `delete Map.prototype[Symbol.toStringTag]`
// 之后同一实例回落成 "Object" —— 这条语义是 symbol-tag-*-builtin 用例的核心。
func objectPrototypeTagFor(v object.Value) string {
	if v == nil {
		return "Undefined"
	}
	switch v.(type) {
	case *object.Undefined:
		return "Undefined"
	case *object.Null:
		return "Null"
	}
	// @@toStringTag 优先 (字符串才生效，非字符串回落 builtinTag)。
	if tag, ok := lookupToStringTag(v); ok {
		return tag
	}
	return builtinTagFor(v)
}

// lookupToStringTag 沿原型链读取 v 的 @@toStringTag，仅当它是**原始字符串**
// 时返回 (规范: Type(tag) is not String → 忽略)。
func lookupToStringTag(v object.Value) (string, bool) {
	sym := toStringTagSymbol()
	if sym == nil {
		return "", false
	}
	// 代理: @@toStringTag 是一次普通 Get，会转发到目标 (可嵌套)。
	// 已撤销代理不再可用 —— 交由调用方在 IsArray 阶段抛 TypeError。
	for i := 0; i < 64; i++ {
		p, ok := v.(*object.Proxy)
		if !ok {
			break
		}
		if p.IsRevoked || p.Target == nil {
			return "", false
		}
		v = p.Target
	}
	// 普通对象 (含命名空间 / 各 *Object 原型 / 用户对象): 完整原型链查找。
	if o, ok := v.(*object.Object); ok {
		return stringTagOf(object.LookupSymbolProperty(o, sym))
	}
	// 独立 struct 类型: 各自的原型对象 (无则视为未定义)。
	if p := protoObjectOf(v); p != nil {
		return stringTagOf(p.GetSymbolProperty(sym))
	}
	return "", false
}

// stringTagOf 把 Get 的结果按 "是否为字符串" 解释: 只有原始 String 才算命中。
func stringTagOf(tv object.Value, found bool) (string, bool) {
	if !found {
		return "", false
	}
	if s, ok := tv.(*object.String); ok {
		return s.Value, true
	}
	return "", false
}

// protoObjectOf 返回值的原型对象 (仅当原型本身是 *Object 时) —— 独立 struct
// 类型没有通用的 Symbol 键查找入口，@@toStringTag 只能落在原型对象上。
func protoObjectOf(v object.Value) *object.Object {
	asObj := func(p object.Value) *object.Object {
		if o, ok := p.(*object.Object); ok {
			return o
		}
		return nil
	}
	switch t := v.(type) {
	case *object.Array:
		if o := asObj(t.GetProto()); o != nil {
			return o
		}
		return asObj(object.ArrayProto)
	case *object.Map:
		if o := asObj(t.GetProto()); o != nil {
			return o
		}
		return asObj(object.MapProto)
	case *object.Set:
		if o := asObj(t.GetProto()); o != nil {
			return o
		}
		return asObj(object.SetProto)
	case *object.Promise:
		return asObj(object.PromiseProto)
	case *object.RegExp:
		return asObj(object.RegExpProto)
	case *object.String:
		return asObj(object.StringProto)
	case *object.Number:
		return asObj(object.NumberProto)
	case *object.Boolean:
		return asObj(object.GetBooleanProto())
	case *object.BigInt:
		return asObj(object.GetBigIntProto())
	case *object.Symbol:
		return asObj(object.GetSymbolProto())
	case *object.Generator:
		return asObj(object.GetGeneratorPrototype())
	case *object.AsyncGenerator:
		if t.Proto != nil {
			return asObj(t.Proto)
		}
		return asObj(object.GetAsyncGeneratorProto())
	case *object.Closure, *object.CompiledFunction, *object.BuiltinFunction, *object.BuiltinMethod:
		// 函数对象的 @@toStringTag 来自 %GeneratorFunction.prototype% /
		// %AsyncFunction.prototype% / %AsyncGeneratorFunction.prototype%
		// ("GeneratorFunction" / "AsyncFunction" / "AsyncGeneratorFunction")。
		// 普通函数落在 %Function.prototype% 上，后者没有 @@toStringTag，
		// 于是回落 builtinTag "Function"。
		return asObj(object.FuncPrototypeOf(v))
	}
	return nil
}

// builtinTagFor 返回规范内部标签 (builtinTag)。
//
// 规范里 builtinTag 只会是 Array / Arguments / Function / Error / Boolean /
// Number / String / Date / RegExp / Object —— **没有** "Map"/"Set"/"BigInt" 等:
// 那些对象的标签一律来自原型上的 @@toStringTag。此处的 default 即 "Object"。
func builtinTagFor(v object.Value) string {
	switch t := v.(type) {
	case *object.Array:
		return "Array"
	case *object.Error:
		return "Error"
	case *object.Boolean:
		return "Boolean"
	case *object.Number:
		return "Number"
	case *object.String:
		return "String"
	case *object.RegExp:
		return "RegExp"
	case *object.Proxy:
		// 代理: IsArray/[[Call]]/[[ErrorData]] 均转发到目标。
		return proxyBuiltinTagFor(t)
	case *object.Closure, *object.CompiledFunction, *object.BuiltinFunction, *object.BuiltinMethod:
		return "Function"
	case *object.TypedArray:
		if t.Kind.Name != "" {
			return t.Kind.Name
		}
		return "TypedArray"
	case *object.ArrayBuffer:
		return "ArrayBuffer"
	case *object.DataView:
		return "DataView"
	case *object.JSIterator:
		if t.Name != "" {
			return t.Name + " Iterator"
		}
		return "Iterator"
	case *object.WeakRef:
		return "WeakRef"
	case *object.FinalizationRegistry:
		return "FinalizationRegistry"
	case *object.TemporalInstant:
		return "Temporal.Instant"
	case *object.TemporalPlainDateTime:
		return "Temporal.PlainDateTime"
	case *object.TemporalPlainDate:
		return "Temporal.PlainDate"
	case *object.TemporalPlainTime:
		return "Temporal.PlainTime"
	case *object.TemporalPlainYearMonth:
		return "Temporal.PlainYearMonth"
	case *object.TemporalPlainMonthDay:
		return "Temporal.PlainMonthDay"
	case *object.TemporalZonedDateTime:
		return "Temporal.ZonedDateTime"
	case *object.TemporalDuration:
		return "Temporal.Duration"
	case *object.TemporalTimeZone:
		return "Temporal.TimeZone"
	case *object.TemporalCalendar:
		return "Temporal.Calendar"
	}
	return "Object"
}

// proxyBuiltinTagFor 按代理目标的种类返回 builtinTag。目标不可达 (已撤销)
// 时回落 "Object" —— 真实的撤销异常由 Get 路径负责抛出。
//
// 代理可嵌套 (new Proxy(new Proxy([], {}), {})): IsArray 沿 [[ProxyTarget]]
// 链递归，因此这里同样递归解析。
func proxyBuiltinTagFor(p *object.Proxy) string {
	if p.IsRevoked || p.Target == nil {
		return "Object"
	}
	switch t := p.Target.(type) {
	case *object.Array:
		return "Array"
	case *object.Error:
		return "Error"
	case *object.Proxy:
		return proxyBuiltinTagFor(t)
	case *object.Closure, *object.CompiledFunction, *object.BuiltinFunction, *object.BuiltinMethod:
		return "Function"
	}
	return "Object"
}

// hasOwnPropertyImpl 实现 hasOwnProperty 的"自有属性"查询。
// 实现 OwnPropertyStore 的值 (普通对象 / globalThis / 数组 / 函数类)
// 统一走接口; 字符串 / Error 等保留各自的自有键规则。
func hasOwnPropertyImpl(this object.Value, key object.Value) bool {
	if this == nil {
		return false
	}
	// Symbol 键: 查 SymbolProperties (与字符串键空间隔离)。
	if sym, ok := key.(*object.Symbol); ok {
		if o, ok := this.(*object.Object); ok {
			return o.HasOwnSymbolProperty(sym)
		}
		return false
	}
	name := propertyKeyString(key)
	if store, ok := this.(object.OwnPropertyStore); ok {
		return store.HasOwn(name)
	}
	switch t := this.(type) {
	case *object.String:
		if name == "length" {
			return true
		}
		if idx, err := strconv.Atoi(name); err == nil && idx >= 0 {
			return idx < len([]rune(t.Value))
		}
		return false
	case *object.Error:
		switch name {
		case "name", "message", "stack":
			return true
		}
		return false
	}
	return false
}

// propertyIsEnumerableImpl 实现 propertyIsEnumerable: 自有且 Enumerable=true。
// 调用方已按 ToObject/ToPropertyKey 处理 this 与 key (经 propertyKeyString)。
// 实现 OwnPropertyStore 的值统一经 OwnDescriptor 判枚举性 (数组 length 因此
// 不再被误判为可枚举); 字符串保持码元索引口径。
func propertyIsEnumerableImpl(this object.Value, key object.Value) bool {
	if this == nil {
		return false
	}
	// Symbol 键: 自有且 Enumerable=true。
	if sym, ok := key.(*object.Symbol); ok {
		if o, ok := this.(*object.Object); ok {
			d, found := o.GetSymbolPropertyDescriptor(sym)
			return found && d.Enumerable
		}
		return false
	}
	name := propertyKeyString(key)
	if store, ok := this.(object.OwnPropertyStore); ok {
		desc, ok := store.OwnDescriptor(name)
		return ok && desc.Enumerable
	}
	if _, ok := this.(*object.String); ok {
		// 字符串索引均为可枚举自有属性 (简化模型)。
		return hasOwnPropertyImpl(this, key)
	}
	return false
}

// isPrototypeOfImpl 沿参数的原型链查找 this 对象。
func isPrototypeOfImpl(proto object.Value, v object.Value) bool {
	if v == nil || proto == nil {
		return false
	}
	cur := v
	for i := 0; i < 64; i++ {
		next, ok := valueProtoOf(cur)
		if !ok {
			return false
		}
		if next == proto {
			return true
		}
		cur = next
	}
	return false
}

// valueProtoOf 返回值的原型对象 (仅覆盖有原型字段的类型)。
func valueProtoOf(v object.Value) (object.Value, bool) {
	switch t := v.(type) {
	case *object.Object:
		if t.Proto == nil || t.Proto == object.NullSingleton {
			return nil, false
		}
		return t.Proto, true
	case *object.Array:
		p := t.GetProto()
		if p == nil || p == object.NullSingleton {
			return nil, false
		}
		return p, true
	}
	return nil, false
}

// propertyKeyString 把属性键转成普通属性名字符串。
func propertyKeyString(v object.Value) string {
	switch k := v.(type) {
	case *object.String:
		return k.Value
	case *object.Number:
		return numberKeyString(k.Value)
	case *object.Boolean:
		if k.Value {
			return "true"
		}
		return "false"
	case *object.BigInt:
		return k.Value.String()
	}
	return ""
}

// numberKeyString 数字索引字符串: 整数不带小数点，符合 ToString 的数组键规则。
func numberKeyString(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	return object.ToString(object.NewNumber(f))
}
