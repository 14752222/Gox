package stdlib

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupObjectGlobal 创建 Object 构造器。
//
// 与 Array 同理，返回 *object.BuiltinFunction: Object 的 typeof 必须是
// "function"，且 Object() / new Object() 必须可调用。
func setupObjectGlobal() *object.BuiltinFunction {
	o := object.NewBuiltin("Object", func(args ...object.Value) object.Value {
		// Object() / Object(null) / Object(undefined) → 新的空对象
		if len(args) == 0 {
			return object.NewObject()
		}
		v := args[0]
		switch v.(type) {
		case *object.Null, *object.Undefined:
			return object.NewObject()
		case *object.Object, *object.Array, *object.Map, *object.Set,
			*object.RegExp, *object.Promise, *object.Error, *object.Proxy,
			*object.Closure, *object.CompiledFunction,
			*object.BuiltinFunction, *object.BuiltinMethod:
			// 已是对象: 原样返回
			return v
		}
		// 原始值: 规范返回对应的包装对象。本运行时不实现包装对象
		// (new Number(5) 得到的也是 number)，因此此处原样返回原始值。
		return v
	})
	// Object(x) 从不抛异常: 传入 Error 对象时它是"值"而非异常
	// (否则 Object(Error()) 会被 VM 当成 throw，call-error 用例失败)。
	o.ReturnIsValue = true
	o.SetProperty("name", object.NewString("Object"))

	// Object.keys(obj): 自有可枚举属性的键
	o.SetProperty("keys", object.NewBuiltin("keys", func(args ...object.Value) object.Value {
		keys, errVal := ownKeysArg(args, "Object.keys")
		if errVal != nil {
			return errVal
		}
		result := make([]object.Value, len(keys))
		for i, k := range keys {
			result[i] = object.NewString(k)
		}
		return object.NewArray(result)
	}))

	// Object.values(obj)
	o.SetProperty("values", object.NewBuiltin("values", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		keys, errVal := ownKeys(args[0])
		if errVal != nil {
			return errVal
		}
		result := make([]object.Value, 0, len(keys))
		for _, k := range keys {
			v, _ := getOwnProperty(args[0], k)
			result = append(result, v)
		}
		return object.NewArray(result)
	}))

	// Object.entries(obj)
	// 旧实现漏掉了数组分支，导致 Object.entries([1,2]) 返回 []。
	o.SetProperty("entries", object.NewBuiltin("entries", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		keys, errVal := ownKeys(args[0])
		if errVal != nil {
			return errVal
		}
		result := make([]object.Value, 0, len(keys))
		for _, k := range keys {
			v, _ := getOwnProperty(args[0], k)
			result = append(result, object.NewArray([]object.Value{
				object.NewString(k),
				v,
			}))
		}
		return object.NewArray(result)
	}))

	// Object.assign(target, ...sources)
	o.SetProperty("assign", object.NewBuiltin("assign", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewObject()
		}
		target, ok := args[0].(*object.Object)
		if !ok {
			return args[0]
		}
		for i := 1; i < len(args); i++ {
			// 规范 ToObject(source) 后复制可枚举自有属性; 函数类/数组等
			// 走 OwnPropertyStore 统一接口, 不再只认 *object.Object。
			src := args[i]
			if src == object.UndefinedSingleton || src == object.NullSingleton {
				continue
			}
			if store, ok := src.(object.OwnPropertyStore); ok {
				for _, k := range store.EnumerableOwnKeys() {
					// 规范 19.1.2.1: Get(from, nextKey) —— 必须经 GetProperty
					// 调用访问器 (getter 抛错要向上传播, 见
					// source-get-attr-error.js)。
					val, _ := src.(interface {
						GetProperty(string) (object.Value, bool)
					}).GetProperty(k)
					target.SetProperty(k, val)
				}
			}
		}
		return target
	}))

	// Object.freeze(obj): 不可扩展，且所有自有数据属性变为不可写、不可配置。
	// 旧实现只设置 Extensible 与 Writable，漏了 Configurable。
	o.SetProperty("freeze", object.NewBuiltin("freeze", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			return args[0]
		}
		obj.Extensible = false
		for k, desc := range obj.Properties {
			desc.Writable = false
			desc.Configurable = false
			obj.Properties[k] = desc
		}
		return args[0]
	}))

	// Object.isFrozen(obj): 不可扩展且所有自有属性均不可写、不可配置
	o.SetProperty("isFrozen", object.NewBuiltin("isFrozen", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			return object.NewBoolean(true)
		}
		if obj.Extensible {
			return object.NewBoolean(false)
		}
		for _, desc := range obj.Properties {
			if desc.Writable || desc.Configurable {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// Object.seal(obj): 不可扩展，所有自有属性不可配置 (但保持可写)。
	o.SetProperty("seal", object.NewBuiltin("seal", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if obj, ok := args[0].(*object.Object); ok {
			obj.Extensible = false
			for k, desc := range obj.Properties {
				desc.Configurable = false
				obj.Properties[k] = desc
			}
		}
		return args[0]
	}))

	// Object.isSealed(obj): 不可扩展且所有自有属性均不可配置
	o.SetProperty("isSealed", object.NewBuiltin("isSealed", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		if obj, ok := args[0].(*object.Object); ok {
			if obj.Extensible {
				return object.NewBoolean(false)
			}
			for _, desc := range obj.Properties {
				if desc.Configurable {
					return object.NewBoolean(false)
				}
			}
		}
		return object.NewBoolean(true)
	}))

	// Object.create(proto)
	o.SetProperty("create", object.NewBuiltin("create", func(args ...object.Value) object.Value {
		var proto object.Value = object.NullSingleton
		if len(args) > 0 {
			if _, isNull := args[0].(*object.Null); isNull {
				proto = object.NullSingleton
			} else if _, isUndef := args[0].(*object.Undefined); isUndef {
				proto = object.NullSingleton
			} else {
				proto = args[0]
			}
		}
		return object.NewObjectWithProto(proto)
	}))

	// Object.getPrototypeOf(obj)
	o.SetProperty("getPrototypeOf", object.NewBuiltin("getPrototypeOf", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NullSingleton
		}
		if g, ok := args[0].(*object.GlobalObject); ok {
			if g.Proto != nil {
				return g.Proto
			}
			return object.NullSingleton
		}
		if obj, ok := args[0].(*object.Object); ok {
			if obj.Proto != nil {
				return obj.Proto
			}
			return object.NullSingleton
		}
		if arr, ok := args[0].(*object.Array); ok {
			if p := arr.GetProto(); p != nil {
				return p
			}
			return object.NullSingleton
		}
		// Date 实例的 [[Prototype]] 取自包级注册表 (object.DateProto),
		// 与 Temporal 各类型同法 —— 缺这条分支会退化成 null, 于是
		// `Object.getPrototypeOf(new Date()) === Date.prototype` 为 false
		// (ryGXAJ)。
		if d, ok := args[0].(*object.Date); ok {
			if p := d.GetProto(); p != nil {
				return p
			}
			return object.NullSingleton
		}
		// RegExp 实例的 [[Prototype]] = %RegExp.prototype% (rEXjyz)。
		// *RegExp 的原型放在包级注册表 (object.RegExpProto), 缺这条分支会
		// 退化成 null, 于是 `Object.getPrototypeOf(/a/) === RegExp.prototype`
		// 为 false —— 与 *Date 同法。
		if re, ok := args[0].(*object.RegExp); ok {
			if p := re.GetProto(); p != nil {
				return p
			}
			return object.NullSingleton
		}
		// async generator 实例的 [[Prototype]] = 该 async generator 函数
		// 自己的 .prototype 对象 (其 [[Prototype]] 才是 %AsyncGeneratorPrototype%)。
		if ag, ok := args[0].(*object.AsyncGenerator); ok {
			if ag.Proto != nil {
				return ag.Proto
			}
			return object.NullSingleton
		}
		// 函数对象: [[Prototype]] 按种类指向 Function/GeneratorFunction/
		// AsyncFunction/AsyncGeneratorFunction.prototype。
		if fp := object.FuncPrototypeOf(args[0]); fp != nil {
			return fp
		}
		return object.NullSingleton
	}))

	// Object.entries already above

	// Object.fromEntries(iterable)
	o.SetProperty("fromEntries", object.NewBuiltin("fromEntries", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewObject()
		}
		if arr, ok := args[0].(*object.Array); ok {
			obj := object.NewObject()
			for _, entry := range arr.Elements {
				if entryArr, ok := entry.(*object.Array); ok && len(entryArr.Elements) >= 2 {
					key := toStr(entryArr.Elements[0])
					val := entryArr.Elements[1]
					obj.SetProperty(key, val)
				}
			}
			return obj
		}
		return object.NewObject()
	}))

	// Object.is(a, b): 同值相等比较
	o.SetProperty("is", object.NewBuiltin("is", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(objectIs(args[0], args[1]))
	}))

	// Object.hasOwn(obj, prop): 检查对象是否有自身属性
	o.SetProperty("hasOwn", object.NewBuiltin("hasOwn", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewBoolean(false)
		}
		key := toStr(args[1])
		// 统一自有属性接口: 普通对象 / globalThis / 数组 / 函数类一视同仁
		// (数组索引与 length、函数的 name/length/prototype 都是自有属性)。
		if store, ok := args[0].(object.OwnPropertyStore); ok {
			return object.NewBoolean(store.HasOwn(key))
		}
		return object.NewBoolean(false)
	}))

	// Object.defineProperty(obj, prop, descriptor)
	o.SetProperty("defineProperty", object.NewBuiltin("defineProperty", func(args ...object.Value) object.Value {
		if len(args) < 3 {
			return object.UndefinedSingleton
		}
		// globalThis: 在全局环境上定义自有属性 (var/函数声明之外的新绑定)。
		if g, ok := args[0].(*object.GlobalObject); ok {
			if _, isSym := args[1].(*object.Symbol); isSym {
				return args[0] // 全局环境无符号键绑定，静默忽略
			}
			desc, errVal := descriptorFromJS(args[2])
			if errVal != nil {
				return errVal
			}
			g.DefineGlobal(toStr(args[1]), desc)
			return args[0]
		}
		// 统一自有属性接口: 普通对象 / 数组 / 函数类都经 OwnPropertyStore
		// 落地描述符 —— 数组索引访问器、函数 name/length 重定义等此前进
		// 非 *Object 分支一律静默 no-op, 现在走各类型自己的描述符通道。
		if store, ok := args[0].(object.OwnPropertyStore); ok {
			if sym, isSym := args[1].(*object.Symbol); isSym {
				// Symbol 键: *Object 有 SymbolProperties; *Array/*TypedArray
				// 经 SymbolPropertyStore 亦有符号键槽。其余实现
				// OwnPropertyStore 的类型 (函数类等) 无符号键属性, 静默忽略。
				if sp, ok := args[0].(object.SymbolPropertyStore); ok {
					if errVal := defineOneSymbolProperty(sp, sym, args[2]); errVal != nil {
						return errVal
					}
				}
				return args[0]
			}
			if errVal := defineOneProperty(store, toStr(args[1]), args[2]); errVal != nil {
				return errVal
			}
			return args[0]
		}
		// 原始值等: 本运行时不实现包装对象, 保持 no-op (规范里对基本值
		// 应先 ToObject; Gox 的 new Number(5) 得到的也是 number)。
		return args[0]
	}))

	// Object.getOwnPropertyDescriptor(obj, prop)
	o.SetProperty("getOwnPropertyDescriptor", object.NewBuiltin("getOwnPropertyDescriptor", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		// globalThis: 自有属性来自全局环境记录 (顶层 var/函数声明/隐式赋值/
		// 内建全局); 顶层 let/const/class 只存在于词法环境，不是自有属性。
		if g, ok := args[0].(*object.GlobalObject); ok {
			if _, isSym := args[1].(*object.Symbol); isSym {
				return object.UndefinedSingleton // 全局环境无符号键绑定
			}
			desc, exists := g.OwnDescriptor(toStr(args[1]))
			if !exists {
				return object.UndefinedSingleton
			}
			return describeProperty(desc)
		}
		// Symbol 键: *Object 与 *Array/*TypedArray 经 SymbolPropertyStore 查到
		// 各自符号键描述符; 其余实现 OwnPropertyStore 的类型无符号键槽, 按
		// 规范返回 undefined (而非 TypeError)。
		if sym, isSym := args[1].(*object.Symbol); isSym {
			if sobj, ok := args[0].(object.SymbolPropertyStore); ok {
				sdesc, found := sobj.GetSymbolPropertyDescriptor(sym)
				if !found {
					return object.UndefinedSingleton
				}
				return describeProperty(sdesc)
			}
			if _, ok := args[0].(object.OwnPropertyStore); ok {
				return object.UndefinedSingleton
			}
			return object.NewTypeError("Object.getOwnPropertyDescriptor called on non-object")
		}
		// 字符串键: 统一走自有属性接口 —— 普通对象查 Properties, 数组查
		// 索引/length/defineProperty 描述符, 函数查 name/length/prototype。
		if store, ok := args[0].(object.OwnPropertyStore); ok {
			desc, exists := store.OwnDescriptor(toStr(args[1]))
			if !exists {
				return object.UndefinedSingleton
			}
			return describeProperty(desc)
		}
		return object.NewTypeError("Object.getOwnPropertyDescriptor called on non-object")
	}))

	// Object.setPrototypeOf(obj, proto)
	o.SetProperty("setPrototypeOf", object.NewBuiltin("setPrototypeOf", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			return args[0]
		}
		if _, isNull := args[1].(*object.Null); isNull {
			obj.Proto = nil
		} else {
			obj.Proto = args[1]
		}
		return args[0]
	}))

	// Object.getOwnPropertyNames(obj)
	o.SetProperty("getOwnPropertyNames", object.NewBuiltin("getOwnPropertyNames", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		if g, ok := args[0].(*object.GlobalObject); ok {
			ks := g.OwnKeys()
			result := make([]object.Value, len(ks))
			for i, k := range ks {
				result[i] = object.NewString(k)
			}
			return object.NewArray(result)
		}
		// 统一自有属性接口: 普通对象 / 数组 (索引+length+defineProperty 键) /
		// 函数类 (length/name/prototype/静态成员) 一视同仁。
		if store, ok := args[0].(object.OwnPropertyStore); ok {
			ks := store.OwnKeys()
			result := make([]object.Value, len(ks))
			for i, k := range ks {
				result[i] = object.NewString(k)
			}
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	// Object.getOwnPropertyDescriptors(obj)
	o.SetProperty("getOwnPropertyDescriptors", object.NewBuiltin("getOwnPropertyDescriptors", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewObject()
		}
		if g, ok := args[0].(*object.GlobalObject); ok {
			result := object.NewObject()
			for _, k := range g.OwnKeys() {
				if d, ok := g.OwnDescriptor(k); ok {
					result.SetProperty(k, describeProperty(d))
				}
			}
			return result
		}
		if obj, ok := args[0].(*object.Object); ok {
			result := object.NewObject()
			for _, k := range obj.Keys() {
				propDesc, ok := obj.Properties[k]
				if !ok {
					continue
				}
				desc := object.NewObject()
				if acc, isAcc := propDesc.Value.(*object.Accessor); isAcc {
					if acc.Getter != nil {
						desc.SetProperty("get", acc.Getter)
					} else {
						desc.SetProperty("get", object.UndefinedSingleton)
					}
					if acc.Setter != nil {
						desc.SetProperty("set", acc.Setter)
					} else {
						desc.SetProperty("set", object.UndefinedSingleton)
					}
				} else {
					// 反映真实的 writable，而不是硬编码 true
					desc.SetProperty("value", propDesc.Value)
					desc.SetProperty("writable", object.NewBoolean(propDesc.Writable))
				}
				desc.SetProperty("enumerable", object.NewBoolean(propDesc.Enumerable))
				desc.SetProperty("configurable", object.NewBoolean(propDesc.Configurable))
				result.SetProperty(k, desc)
			}
			return result
		}
		// 统一自有属性接口: 数组 / 函数类同样返回逐属性描述符表。
		if store, ok := args[0].(object.OwnPropertyStore); ok {
			result := object.NewObject()
			for _, k := range store.OwnKeys() {
				if d, ok := store.OwnDescriptor(k); ok {
					result.SetProperty(k, describeProperty(d))
				}
			}
			return result
		}
		return object.NewObject()
	}))

	// Object.isExtensible(obj)
	o.SetProperty("isExtensible", object.NewBuiltin("isExtensible", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if obj, ok := args[0].(*object.Object); ok {
			return object.NewBoolean(obj.Extensible)
		}
		// 其它对象类型 (函数/数组/代理/async generator 等) 默认可扩展 ——
		// 之前只认 *Object, 于是 Object.isExtensible(AsyncGeneratorFunction)
		// 误报 false (test262 built-ins/AsyncGeneratorFunction/extensibility.js)。
		if object.IsObjectValue(args[0]) {
			return object.NewBoolean(true)
		}
		return object.NewBoolean(false)
	}))

	// Object.preventExtensions(obj)
	o.SetProperty("preventExtensions", object.NewBuiltin("preventExtensions", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if obj, ok := args[0].(*object.Object); ok {
				obj.Extensible = false
			}
		}
		if len(args) > 0 {
			return args[0]
		}
		return object.UndefinedSingleton
	}))

	// Object.defineProperties(obj, descriptors) (ES5)
	// 批量 defineProperty: descriptors 的每个自有属性 {value, get, set...}
	// 都定义到 obj 上，返回 obj。
	o.SetProperty("defineProperties", object.NewBuiltin("defineProperties", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		// 统一自有属性接口: 普通对象 / 数组 / 函数类都可作 target。
		// 原始值等非对象仍抛 TypeError (规范 ToObject 前的目标校验)。
		store, ok := args[0].(object.OwnPropertyStore)
		if !ok {
			return object.NewTypeError("Object.defineProperties: target must be an object")
		}
		props, ok := args[1].(*object.Object)
		if !ok {
			return args[0] // 非对象描述符: 规范按 ToObject 处理，简化为无属性
		}
		for _, key := range props.EnumerableKeys() {
			descVal, found := props.GetProperty(key)
			if !found {
				continue
			}
			if errVal := defineOneProperty(store, key, descVal); errVal != nil {
				return errVal
			}
		}
		return args[0]
	}))

	// Object.getOwnPropertySymbols(obj) (ES6): 返回 Symbol 自有键
	o.SetProperty("getOwnPropertySymbols", object.NewBuiltin("getOwnPropertySymbols", func(args ...object.Value) object.Value {
		result := []object.Value{}
		if len(args) > 0 {
			if obj, ok := args[0].(object.SymbolPropertyStore); ok {
				for _, sym := range obj.SymbolKeys() {
					result = append(result, sym)
				}
			}
		}
		return object.NewArray(result)
	}))

	// Object.groupBy(items, callback) (ES2024): 按回调返回的键分组
	o.SetProperty("groupBy", object.NewBuiltin("groupBy", func(args ...object.Value) object.Value {
		return groupByImpl(args, false)
	}))

	return o
}

// objectIs 实现同值相等 (Object.is)。
func objectIs(a, b object.Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	// 处理 NaN: Object.is(NaN, NaN) === true
	if aNum, ok := a.(*object.Number); ok {
		if bNum, ok := b.(*object.Number); ok {
			if isNaN(aNum.Value) && isNaN(bNum.Value) {
				return true
			}
			// 处理 +0 和 -0: Object.is(+0, -0) === false
			if aNum.Value == 0 && bNum.Value == 0 {
				return signbit(aNum.Value) == signbit(bNum.Value)
			}
			return aNum.Value == bNum.Value
		}
	}
	return strictEqualValues(a, b)
}

// isNaN 检查是否为 NaN
func isNaN(f float64) bool {
	return f != f
}

// signbit 检查符号位
func signbit(f float64) bool {
	return f < 0 || (f == 0 && 1/f < 0)
}

// ownKeysArg 解析 Object.keys/values/entries 的参数并返回键列表。
// 参数为 undefined/null 时抛 TypeError (规范要求)。
func ownKeysArg(args []object.Value, api string) ([]string, object.Value) {
	if len(args) == 0 {
		return nil, object.NewTypeError("%s: undefined cannot be converted to an object", api)
	}
	return ownKeys(args[0])
}

// ownKeys 返回值的自有可枚举键列表，顺序遵循 OrdinaryOwnPropertyKeys。
// 支持普通对象、数组、函数 (Closure/BuiltinFunction/BuiltinMethod) 与字符串
// (字符串按 UTF-16 码元索引展开)。
// 对象型一律优先走 OwnPropertyStore 统一接口 —— 与 defineProperty /
// getOwnPropertyDescriptor / hasOwnProperty 同一收口点, 不再按类型穷举。
func ownKeys(v object.Value) ([]string, object.Value) {
	switch v.(type) {
	case *object.Undefined, *object.Null:
		return nil, object.NewTypeError("Cannot convert undefined or null to object")
	case *object.String:
		// 规范: Object.keys("ab") === ["0", "1"]
		s := v.(*object.String)
		n := object.UTF16Len(s.Value)
		keys := make([]string, n)
		for i := 0; i < n; i++ {
			keys[i] = strconv.Itoa(i)
		}
		return keys, nil
	}
	// 统一接口: 普通对象 / 数组 / 函数类 / globalThis 都实现 OwnPropertyStore。
	if store, ok := v.(object.OwnPropertyStore); ok {
		return store.EnumerableOwnKeys(), nil
	}
	return nil, nil
}

// getOwnProperty 取值的自有属性，未找到时返回 undefined。
// 只查自有 (不沿原型链): 数组的索引/length/defineProperty 描述符、
// globalThis 的非词法绑定、函数的 Props/描述符、字符串的码元索引。
func getOwnProperty(v object.Value, key string) (object.Value, bool) {
	switch val := v.(type) {
	case *object.GlobalObject:
		if _, ok := val.OwnDescriptor(key); !ok {
			return object.UndefinedSingleton, false
		}
		got, found := val.GetProperty(key)
		if !found || got == nil {
			return object.UndefinedSingleton, true
		}
		return got, true
	case *object.Object:
		if desc, ok := val.Properties[key]; ok {
			if desc.Value == nil {
				return object.UndefinedSingleton, true
			}
			return desc.Value, true
		}
		return object.UndefinedSingleton, false
	case *object.Array:
		// defineProperty 定义过的描述符优先: 访问器调 getter,
		// 数据属性取描述符值 (此时 Elements 同步占位, 不能直接读)。
		if d, ok := val.PropDescs[key]; ok {
			if acc, isAcc := d.Value.(*object.Accessor); isAcc {
				if acc.Getter != nil && object.IsCallable(acc.Getter) {
					return object.CallFunction(acc.Getter, val), true
				}
				return object.UndefinedSingleton, true
			}
			if d.Value == nil {
				return object.UndefinedSingleton, true
			}
			return d.Value, true
		}
		if i, err := strconv.Atoi(key); err == nil && i >= 0 && i < len(val.Elements) {
			if e := val.Elements[i]; e != nil {
				return e, true
			}
			return object.UndefinedSingleton, true
		}
		return object.UndefinedSingleton, false
	case *object.String:
		if i, err := strconv.Atoi(key); err == nil {
			if ch, ok := object.CharAtUTF16(val.Value, i); ok {
				return object.NewString(ch), true
			}
		}
		return object.UndefinedSingleton, false
	}
	// 函数类 (Closure/BuiltinFunction/BuiltinMethod): 有自有描述符即取值,
	// 访问器描述符调 getter。取不到按未找到处理 (不暴露 undefined 自有)。
	if store, ok := v.(object.OwnPropertyStore); ok {
		if _, has := store.OwnDescriptor(key); has {
			got, found := v.(interface{ GetProperty(string) (object.Value, bool) }).GetProperty(key)
			if !found || got == nil {
				return object.UndefinedSingleton, true
			}
			return got, true
		}
	}
	return object.UndefinedSingleton, false
}

// setupErrorTypes 设置 Error 构造器到全局环境。
func setupErrorTypes(env *runtime.Environment) {
	// Error 构造器
	env.Declare("Error", func() *object.BuiltinFunction {
		f := object.NewBuiltin("Error", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return object.NewError(msg)
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("Error", f)
		return f
	}(), false)

	env.Declare("TypeError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("TypeError", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return &object.Error{Message: msg, Name: "TypeError"}
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("TypeError", f)
		return f
	}(), false)

	env.Declare("RangeError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("RangeError", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return &object.Error{Message: msg, Name: "RangeError"}
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("RangeError", f)
		return f
	}(), false)

	env.Declare("ReferenceError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("ReferenceError", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return &object.Error{Message: msg, Name: "ReferenceError"}
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("ReferenceError", f)
		return f
	}(), false)

	env.Declare("SyntaxError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("SyntaxError", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return &object.Error{Message: msg, Name: "SyntaxError"}
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("SyntaxError", f)
		return f
	}(), false)

	// SuppressedError (ES2023 explicit resource management): 释放资源时又抛错、
	// 且此前已有挂起异常时的合成错误 (error = 后发生的, suppressed = 被压制的)。
	env.Declare("SuppressedError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("SuppressedError", func(args ...object.Value) object.Value {
			var errV object.Value = object.UndefinedSingleton
			var suppressedV object.Value = object.UndefinedSingleton
			msg := ""
			if len(args) > 0 {
				errV = args[0]
			}
			if len(args) > 1 {
				suppressedV = args[1]
			}
			if len(args) > 2 {
				msg = toStr(args[2])
			}
			e := object.NewSuppressedError(errV, suppressedV)
			if msg != "" {
				e.Message = msg
			}
			return e
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("SuppressedError", f)
		return f
	}(), false)

	// URIError: 此前完全缺失, 用例里 `new URIError()` 会退化成 ReferenceError
	// (test262 dynamic-import eval-rqstd-abrupt-err-uri 断言 error.name)。
	env.Declare("URIError", func() *object.BuiltinFunction {
		f := object.NewBuiltin("URIError", func(args ...object.Value) object.Value {
			msg := ""
			if len(args) > 0 {
				msg = toStr(args[0])
			}
			return &object.Error{Message: msg, Name: "URIError"}
		})
		f.ReturnIsValue = true
		object.RegisterErrorConstructor("URIError", f)
		return f
	}(), false)
}

// newParseIntBuiltin 构造 parseInt 内建函数。
// 全局 parseInt 与 Number.parseInt 共用同一份实现，避免两处语义漂移。
func newParseIntBuiltin() *object.BuiltinFunction {
	return object.NewBuiltin("parseInt", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		radix := 0 // 0 表示调用方未指定基数
		if len(args) > 1 {
			radix = int(toInt(args[1]))
		}
		result, ok := parseIntString(toStr(args[0]), radix)
		if !ok {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(float64(result))
	})
}

// newParseFloatBuiltin 构造 parseFloat 内建函数。
// 全局 parseFloat 与 Number.parseFloat 共用同一份实现。
func newParseFloatBuiltin() *object.BuiltinFunction {
	return object.NewBuiltin("parseFloat", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		f, ok := parseFloatString(toStr(args[0]))
		if !ok {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(f)
	})
}

// setupNumberFunctions 设置全局数字相关函数。
func setupNumberFunctions(env *runtime.Environment) {
	env.Declare("parseInt", newParseIntBuiltin(), false)
	env.Declare("parseFloat", newParseFloatBuiltin(), false)

	env.Declare("isNaN", object.NewBuiltin("isNaN", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		v := toFloat(args[0])
		return object.NewBoolean(v != v)
	}), false)

	env.Declare("isFinite", object.NewBuiltin("isFinite", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		v := toFloat(args[0])
		// NaN 与 ±Infinity 都不是有限数
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(true)
	}), false)
}

// setupGlobalFunctions 设置全局函数。
//
// 这里是 String / Number / Boolean 三个构造器的**唯一注册点**。
// 过去 stdlib.go 会先用 setupStringGlobal/setupNumberGlobal/setupBooleanGlobal
// 注册一次，这里再注册一次，而 Environment.Declare 是覆盖写，导致前者的
// 静态成员 (Number.MAX_VALUE、Number.parseInt 等) 全部丢失。
func setupGlobalFunctions(env *runtime.Environment) {
	// ===== String() 构造器 =====
	strFn := object.NewBuiltin("String", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewString("")
		}
		return object.NewString(toStr(args[0]))
	})
	strFn.SetProperty("name", object.NewString("String"))
	// String.fromCharCode(...codes) — 规范: 每个码位按 ToUint16 截断
	strFn.SetProperty("fromCharCode", object.NewBuiltin("fromCharCode", func(args ...object.Value) object.Value {
		var b strings.Builder
		for _, arg := range args {
			code := int(toInt(arg)) & 0xFFFF
			b.WriteRune(rune(code))
		}
		return object.NewString(b.String())
	}))
	// String.fromCodePoint(...codePoints) — 越界码位抛 RangeError
	strFn.SetProperty("fromCodePoint", object.NewBuiltin("fromCodePoint", func(args ...object.Value) object.Value {
		var b strings.Builder
		for _, arg := range args {
			cp := toInt(arg)
			if cp < 0 || cp > 0x10FFFF {
				return object.NewRangeError("Invalid code point %d", cp)
			}
			b.WriteRune(rune(cp))
		}
		return object.NewString(b.String())
	}))
	// String.raw(template, ...substitutions)
	strFn.SetProperty("raw", object.NewBuiltin("raw", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewString("")
		}
		tpl, ok := args[0].(*object.Object)
		if !ok {
			return object.NewString("")
		}
		rawVal, found := tpl.GetProperty("raw")
		if !found {
			return object.NewString("")
		}
		rawArr, ok := rawVal.(*object.Array)
		if !ok {
			return object.NewString("")
		}
		var b strings.Builder
		for i, e := range rawArr.Elements {
			b.WriteString(toStr(e))
			if i < len(args)-1 {
				b.WriteString(toStr(args[i+1]))
			}
		}
		return object.NewString(b.String())
	}))
	env.Declare("String", strFn, false)

	// ===== Number() 构造器 =====
	numFn := object.NewBuiltin("Number", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(0)
		}
		return object.NewNumber(toFloat(args[0]))
	})
	numFn.SetProperty("name", object.NewString("Number"))
	numFn.SetProperty("MAX_SAFE_INTEGER", object.NewNumber(9007199254740991))
	numFn.SetProperty("MIN_SAFE_INTEGER", object.NewNumber(-9007199254740991))
	numFn.SetProperty("MAX_VALUE", object.NewNumber(math.MaxFloat64))
	numFn.SetProperty("MIN_VALUE", object.NewNumber(5e-324))
	numFn.SetProperty("POSITIVE_INFINITY", object.NewNumber(math.Inf(1)))
	numFn.SetProperty("NEGATIVE_INFINITY", object.NewNumber(math.Inf(-1)))
	numFn.SetProperty("NaN", object.NewNumber(math.NaN()))
	numFn.SetProperty("EPSILON", object.NewNumber(2.220446049250313e-16))
	// 以下 isXxx 方法遵循规范: 非 Number 类型的参数一律返回 false
	numFn.SetProperty("isInteger", object.NewBuiltin("isInteger", func(args ...object.Value) object.Value {
		num, ok := args[0].(*object.Number)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		if math.IsNaN(num.Value) || math.IsInf(num.Value, 0) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(num.Value == math.Trunc(num.Value))
	}))
	numFn.SetProperty("isSafeInteger", object.NewBuiltin("isSafeInteger", func(args ...object.Value) object.Value {
		num, ok := args[0].(*object.Number)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		if math.IsNaN(num.Value) || math.IsInf(num.Value, 0) {
			return object.NewBoolean(false)
		}
		if num.Value != math.Trunc(num.Value) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(math.Abs(num.Value) <= 9007199254740991)
	}))
	numFn.SetProperty("isFinite", object.NewBuiltin("isFinite", func(args ...object.Value) object.Value {
		num, ok := args[0].(*object.Number)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(!math.IsNaN(num.Value) && !math.IsInf(num.Value, 0))
	}))
	numFn.SetProperty("isNaN", object.NewBuiltin("isNaN", func(args ...object.Value) object.Value {
		num, ok := args[0].(*object.Number)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(math.IsNaN(num.Value))
	}))
	numFn.SetProperty("parseFloat", newParseFloatBuiltin())
	numFn.SetProperty("parseInt", newParseIntBuiltin())
	env.Declare("Number", numFn, false)

	// ===== Boolean() 构造器 =====
	boolFn := object.NewBuiltin("Boolean", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(toBool(args[0]))
	})
	boolFn.SetProperty("name", object.NewString("Boolean"))
	// Boolean.prototype: Boolean 原始值没有自有属性，属性访问 (含
	// @@toStringTag) 一律沿它查找。规范里它**没有** @@toStringTag，
	// 因此 toString.call(true) 的 "Boolean" 标签来自 builtinTag。
	boolProto := object.NewObjectWithProto(objectPrototype)
	boolProto.SetBuiltinProperty("constructor", boolFn)
	// Boolean.prototype.toString / valueOf (ES2024 20.3.3.2 / 20.3.3.3)：
	// 此前原型上只有 constructor，`true.toString()` 因此取不到方法。
	boolProto.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if b, ok := this.(*object.Boolean); ok {
			if b.Value {
				return object.NewString("true")
			}
			return object.NewString("false")
		}
		return object.NewTypeError("Boolean.prototype.toString requires that 'this' be a Boolean")
	}))
	boolProto.SetBuiltinProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if b, ok := this.(*object.Boolean); ok {
			return b
		}
		return object.NewTypeError("Boolean.prototype.valueOf requires that 'this' be a Boolean")
	}))
	boolFn.SetProperty("prototype", boolProto)
	object.SetBooleanProto(boolProto)
	env.Declare("Boolean", boolFn, false)

	// Function() 构造器已移至 setupFunctionIntrinsics (stdlib/function_proto.go)，
	// 因为它要参与函数对象 [[Prototype]] 链的早期装配。

	// NaN, Infinity, undefined 全局常量
	env.Declare("NaN", object.NewNumber(math.NaN()), true)
	env.Declare("Infinity", object.NewNumber(math.Inf(1)), true)
	env.Declare("undefined", object.UndefinedSingleton, true)
}

// dynamicFuncKind 是 CreateDynamicFunction 的函数种类。
type dynamicFuncKind int

const (
	dynFuncNormal dynamicFuncKind = iota // Function
	dynFuncGenerator                     // GeneratorFunction
	dynFuncAsync                         // AsyncFunction
	dynFuncAsyncGenerator                // AsyncGeneratorFunction
)

// newDynamicFunction 实现 CreateDynamicFunction: Function / GeneratorFunction /
// AsyncFunction / AsyncGeneratorFunction 共用的动态函数构造。
// 规范语义: 除最后一个参数外都是形参名，最后一个参数是函数体源码；
// 将 (形参, 函数体) 拼成对应种类的函数表达式源码，经 object.CompileSource 桥
// 编译 (vm 包注册实现)，取顶层包装函数构建闭包。
//
// 闭包的 Env 是全局环境 (动态函数只访问全局作用域)；[[Prototype]] 按编译出的
// 函数种类装配 (FuncPrototypeForCompiled)，故造出的 GeneratorFunction 实例
// 继承 %GeneratorFunction.prototype%、其 .prototype 的 [[Prototype]] 指向
// %GeneratorPrototype% —— 与静态声明完全一致。
func newDynamicFunction(env *runtime.Environment, args []object.Value, kind dynamicFuncKind) object.Value {
	params := make([]string, 0, len(args))
	body := ""
	for i, a := range args {
		if i < len(args)-1 {
			params = append(params, toStr(a))
		} else {
			body = toStr(a)
		}
	}
	// 基本合法性检查: 形参不能含 ) { 等破坏结构的内容 (node 实测此类为 SyntaxError)。
	// 逗号**不在**此列 —— 它是合法的形参分隔符 (Function('a, b') 就是两个形参),
	// 误拒会让 Function/GeneratorFunction/AsyncGeneratorFunction 的多形参形式挂。
	for _, p := range params {
		for _, ch := range p {
			if ch == ')' || ch == '{' || ch == '}' || ch == '[' || ch == ']' {
				return object.NewErrorWithName("SyntaxError", fmt.Sprintf("Unexpected character %q in argument list", string(ch)))
			}
		}
	}
	var head string
	switch kind {
	case dynFuncGenerator:
		head = "function* anonymous"
	case dynFuncAsync:
		head = "async function anonymous"
	case dynFuncAsyncGenerator:
		head = "async function* anonymous"
	default:
		head = "function anonymous"
	}
	src := "(" + head + "(" + strings.Join(params, ",") + ") {\n" + body + "\n})"

	fn, err := object.CompileSource(src)
	if err != nil {
		return object.NewErrorWithName("SyntaxError", err.Error())
	}
	return &object.Closure{
		Fn:            fn,
		Env:           env,
		FuncPrototype: object.FuncPrototypeForCompiled(fn),
	}
}

// describeProperty 把内部 PropertyDescriptor 转成 JS 描述符对象
// (Object.getOwnPropertyDescriptor 的返回形态)。
func describeProperty(desc object.PropertyDescriptor) object.Value {
	result := object.NewObject()
	result.SetProperty("configurable", object.NewBoolean(desc.Configurable))
	result.SetProperty("enumerable", object.NewBoolean(desc.Enumerable))
	if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
		if acc.Getter != nil {
			result.SetProperty("get", acc.Getter)
		} else {
			result.SetProperty("get", object.UndefinedSingleton)
		}
		if acc.Setter != nil {
			result.SetProperty("set", acc.Setter)
		} else {
			result.SetProperty("set", object.UndefinedSingleton)
		}
		return result
	}
	result.SetProperty("value", desc.Value)
	result.SetProperty("writable", object.NewBoolean(desc.Writable))
	return result
}

// defineOneSymbolProperty 按 property descriptor 定义一个 Symbol 键自有属性。
// 与 defineOneProperty 同一套 ToPropertyDescriptor 语义, 但落点在持有者的符号
// 键存储 (*Object.SymbolProperties 或 *Array/*TypedArray 同构槽), 与字符串键
// 空间隔离。
func defineOneSymbolProperty(obj object.SymbolPropertyStore, sym *object.Symbol, descVal object.Value) object.Value {
	if !object.IsObjectValue(descVal) {
		return object.NewTypeError("Property description must be an object")
	}
	get := func(n string) (object.Value, bool) {
		return descVal.GetProperty(n)
	}
	accField := func(v object.Value) object.Value {
		if v == nil || isUndefinedValue(v) {
			return object.UndefinedSingleton
		}
		return v
	}

	existing, exists := obj.GetSymbolPropertyDescriptor(sym)
	nd := existing
	if !exists {
		nd = object.PropertyDescriptor{}
	}

	_, hasGet := get("get")
	_, hasSet := get("set")
	if hasGet || hasSet {
		g, _ := get("get")
		s, _ := get("set")
		nd.Value = &object.Accessor{Getter: accField(g), Setter: accField(s)}
	} else {
		if v, found := get("value"); found {
			nd.Value = v
		} else if !exists {
			nd.Value = object.UndefinedSingleton
		}
		if v, found := get("writable"); found {
			nd.Writable = toBool(v)
		}
	}
	if v, found := get("enumerable"); found {
		nd.Enumerable = toBool(v)
	}
	if v, found := get("configurable"); found {
		nd.Configurable = toBool(v)
	}

	obj.DefineOwnSymbolProperty(sym, nd)
	return nil
}

// defineOneProperty 按 property descriptor 定义一个属性。
// 访问器描述符 (get/set) 注册为访问器；数据描述符 (value/writable)
// 注册为数据属性。供 defineProperty / defineProperties 共用。
// descriptorFromJS 把 JS 描述符对象解析为内部 PropertyDescriptor
// (Object.defineProperty(globalThis, ...) 用)。非对象描述符抛 TypeError。
// 缺省字段的默认值与规范 ToPropertyDescriptor 一致 (均为 false/undefined)。
func descriptorFromJS(descVal object.Value) (object.PropertyDescriptor, object.Value) {
	if !object.IsObjectValue(descVal) {
		return object.PropertyDescriptor{}, object.NewTypeError("Property description must be an object")
	}
	get := func(n string) (object.Value, bool) { return descVal.GetProperty(n) }
	nd := object.PropertyDescriptor{}
	_, hasGet := get("get")
	_, hasSet := get("set")
	if hasGet || hasSet {
		var g, s object.Value
		if v, found := get("get"); found && !isUndefinedValue(v) {
			if !object.IsCallable(v) {
				return nd, object.NewTypeError("Getter must be a function")
			}
			g = v
		}
		if v, found := get("set"); found && !isUndefinedValue(v) {
			if !object.IsCallable(v) {
				return nd, object.NewTypeError("Setter must be a function")
			}
			s = v
		}
		nd.Value = object.NewAccessor(g, s)
		nd.Writable = false
		if v, found := get("enumerable"); found {
			nd.Enumerable = toBool(v)
		}
		if v, found := get("configurable"); found {
			nd.Configurable = toBool(v)
		}
		return nd, nil
	}
	if v, found := get("value"); found {
		nd.Value = v
	}
	if v, found := get("writable"); found {
		nd.Writable = toBool(v)
	}
	if v, found := get("enumerable"); found {
		nd.Enumerable = toBool(v)
	}
	if v, found := get("configurable"); found {
		nd.Configurable = toBool(v)
	}
	if nd.Value == nil {
		nd.Value = object.UndefinedSingleton
	}
	return nd, nil
}

// defineOneProperty 按 property descriptor 在任意 OwnPropertyStore 上定义
// 一个属性: 访问器描述符 (get/set) 注册为访问器；数据描述符
// (value/writable) 注册为数据属性。供 defineProperty / defineProperties
// 共用 (普通对象 / 数组 / 函数类一视同仁 —— 各类型的描述符落点由
// OwnPropertyStore.DefineOwn 负责)。
func defineOneProperty(store object.OwnPropertyStore, key string, descVal object.Value) object.Value {
	if !object.IsObjectValue(descVal) {
		// 规范: ToPropertyDescriptor 对非对象 (原始值) 抛 TypeError。
		return object.NewTypeError("Property description must be an object")
	}
	// ToPropertyDescriptor 读写描述符字段用 HasProperty + Get 语义:
	// 沿原型链查找并调用访问器。描述符可以是普通对象，也可以是函数
	// (函数对象可在 Function.prototype 上挂 get/enumerable 等字段)。
	get := func(n string) (object.Value, bool) {
		return descVal.GetProperty(n)
	}
	_, hasGet := get("get")
	_, hasSet := get("set")
	isAccessorDesc := hasGet || hasSet

	// accField 归一化访问器字段: 缺失/undefined 统一为 undefined。
	accField := func(v object.Value) object.Value {
		if v == nil || isUndefinedValue(v) {
			return object.UndefinedSingleton
		}
		return v
	}

	existing, exists := store.OwnDescriptor(key)
	// 已存在属性: 缺省字段保持原值 (从 existing 起步)。
	nd := existing
	if !exists {
		nd = object.PropertyDescriptor{}
	}

	// 非可配置属性的兼容性校验 (规范 ValidateAndApplyPropertyDescriptor 的核心)。
	if exists && !existing.Configurable {
		if v, found := get("configurable"); found && toBool(v) {
			return object.NewTypeError("Cannot redefine property: %s", key)
		}
		if v, found := get("enumerable"); found && toBool(v) != existing.Enumerable {
			return object.NewTypeError("Cannot redefine property: %s", key)
		}
		oldAcc, wasAcc := existing.Value.(*object.Accessor)
		if wasAcc != isAccessorDesc {
			return object.NewTypeError("Cannot redefine property: %s", key)
		}
		if !wasAcc {
			if v, found := get("writable"); found && toBool(v) && !existing.Writable {
				return object.NewTypeError("Cannot redefine property: %s", key)
			}
			// 不可写的数据属性: 只允许写成同值。
			if !existing.Writable {
				if v, found := get("value"); found && !objectIs(v, existing.Value) {
					return object.NewTypeError("Cannot redefine property: %s", key)
				}
			}
		} else {
			// 不可配置的访问器属性: get/set 只允许写成相同函数 (同值)。
			if v, found := get("get"); found && !objectIs(accField(v), accField(oldAcc.Getter)) {
				return object.NewTypeError("Cannot redefine property: %s", key)
			}
			if v, found := get("set"); found && !objectIs(accField(v), accField(oldAcc.Setter)) {
				return object.NewTypeError("Cannot redefine property: %s", key)
			}
		}
	}

	if isAccessorDesc {
		// 数据 → 访问器切换需可配置。
		if exists {
			if _, wasAcc := existing.Value.(*object.Accessor); !wasAcc && !existing.Configurable {
				return object.NewTypeError("Cannot redefine property: %s", key)
			}
		}
		var g, s object.Value
		if v, found := get("get"); found && !isUndefinedValue(v) {
			if !object.IsCallable(v) {
				return object.NewTypeError("Getter must be a function")
			}
			g = v
		}
		if v, found := get("set"); found && !isUndefinedValue(v) {
			if !object.IsCallable(v) {
				return object.NewTypeError("Setter must be a function")
			}
			s = v
		}
		nd.Value = object.NewAccessor(g, s)
		nd.Writable = false
		if v, found := get("enumerable"); found {
			nd.Enumerable = toBool(v)
		}
		if v, found := get("configurable"); found {
			nd.Configurable = toBool(v)
		}
		store.DefineOwn(key, nd)
		return nil
	}

	// 访问器 → 数据切换需可配置。
	if exists {
		if _, wasAcc := existing.Value.(*object.Accessor); wasAcc && !existing.Configurable {
			return object.NewTypeError("Cannot redefine property: %s", key)
		}
	}
	if v, found := get("value"); found {
		nd.Value = v
	} else if !exists {
		nd.Value = object.UndefinedSingleton
	}
	if v, found := get("writable"); found {
		nd.Writable = toBool(v)
	} else if !exists {
		nd.Writable = false
	}
	if v, found := get("enumerable"); found {
		nd.Enumerable = toBool(v)
	}
	if v, found := get("configurable"); found {
		nd.Configurable = toBool(v)
	}
	store.DefineOwn(key, nd)
	return nil
}

// groupByImpl 实现 Object.groupBy / Map.groupBy 的共同逻辑。
// useMap=false 返回普通对象 (键经 ToString)，true 返回 Map (键保持原值)。
func groupByImpl(args []object.Value, useMap bool) object.Value {
	if len(args) < 2 || !object.IsCallable(args[1]) {
		return object.NewTypeError("groupBy: callback must be a function")
	}
	callback := args[1]
	result := object.NewMap()
	if !useMap {
		result = object.NewMap() // 先收集到 Map，最后按需转为对象
	}
	next, ok := object.Iterate(args[0])
	if !ok {
		return object.NewTypeError("groupBy: first argument is not iterable")
	}
	i := 0
	for {
		item, done := next()
		if done {
			break
		}
		key := object.CallFunction(callback, object.UndefinedSingleton, item, object.NewInt(int64(i)))
		i++
		// 追加到该键的组
		entryMap := result
		var groupKey object.Value = key
		if !useMap {
			groupKey = object.NewString(toStr(key))
		}
		existing, found := entryMap.Get(groupKey)
		if found {
			if arr, isArr := existing.(*object.Array); isArr {
				arr.Elements = append(arr.Elements, item)
				continue
			}
		}
		entryMap.Set(groupKey, object.NewArray([]object.Value{item}))
	}
	if useMap {
		return result
	}
	// Map → 普通对象
	obj := object.NewObject()
	for _, entry := range result.Entries {
		obj.SetProperty(toStr(entry.Key), entry.Value)
	}
	return obj
}
