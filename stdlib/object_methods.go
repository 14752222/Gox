package stdlib

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"js-runtime/bytecode"
	"js-runtime/compiler"
	"js-runtime/lexer"
	"js-runtime/object"
	"js-runtime/parser"
	"js-runtime/runtime"
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
			if src, ok := args[i].(*object.Object); ok {
				for _, k := range src.Keys() {
					val, _ := src.GetProperty(k)
					target.SetProperty(k, val)
				}
			}
		}
		return target
	}))

	// Object.freeze(obj): 不可扩展，且所有自有数据属性变为不可写。
	// 旧实现只设置 Extensible，已存在的属性仍可修改。
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
			obj.Properties[k] = desc
		}
		return args[0]
	}))

	// Object.isFrozen(obj): 不可扩展且所有自有属性均不可写
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
			if desc.Writable {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// Object.seal(obj): 不可扩展，但已有属性保持可写
	o.SetProperty("seal", object.NewBuiltin("seal", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if obj, ok := args[0].(*object.Object); ok {
			obj.Extensible = false
		}
		return args[0]
	}))

	// Object.isSealed(obj)
	o.SetProperty("isSealed", object.NewBuiltin("isSealed", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		if obj, ok := args[0].(*object.Object); ok {
			return object.NewBoolean(!obj.Extensible)
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
		obj, ok := args[0].(*object.Object)
		if !ok {
			return object.NewBoolean(false)
		}
		key := toStr(args[1])
		_, exists := obj.Properties[key]
		return object.NewBoolean(exists)
	}))

	// Object.defineProperty(obj, prop, descriptor)
	o.SetProperty("defineProperty", object.NewBuiltin("defineProperty", func(args ...object.Value) object.Value {
		if len(args) < 3 {
			return object.UndefinedSingleton
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			return args[0]
		}
		key := toStr(args[1])
		defineOneProperty(obj, key, args[2])
		return args[0]
	}))

	// Object.getOwnPropertyDescriptor(obj, prop)
	o.SetProperty("getOwnPropertyDescriptor", object.NewBuiltin("getOwnPropertyDescriptor", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		obj, ok := args[0].(*object.Object)
		if !ok {
			return object.NewTypeError("Object.getOwnPropertyDescriptor called on non-object")
		}
		desc, exists := obj.Properties[toStr(args[1])]
		if !exists {
			return object.UndefinedSingleton
		}
		result := object.NewObject()
		result.SetProperty("configurable", object.NewBoolean(false))
		result.SetProperty("enumerable", object.NewBoolean(true))
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
		if obj, ok := args[0].(*object.Object); ok {
			keys := obj.Keys()
			result := make([]object.Value, len(keys))
			for i, k := range keys {
				result[i] = object.NewString(k)
			}
			return object.NewArray(result)
		}
		if arr, ok := args[0].(*object.Array); ok {
			n := len(arr.Elements)
			result := make([]object.Value, n+1)
			for i := 0; i < n; i++ {
				result[i] = object.NewString(strconv.Itoa(i))
			}
			result[n] = object.NewString("length")
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	// Object.getOwnPropertyDescriptors(obj)
	o.SetProperty("getOwnPropertyDescriptors", object.NewBuiltin("getOwnPropertyDescriptors", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewObject()
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
				desc.SetProperty("enumerable", object.NewBoolean(true))
				desc.SetProperty("configurable", object.NewBoolean(false))
				result.SetProperty(k, desc)
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
		obj, ok := args[0].(*object.Object)
		if !ok {
			return object.NewTypeError("Object.defineProperties: target must be an object")
		}
		props, ok := args[1].(*object.Object)
		if !ok {
			return args[0] // 非对象描述符: 规范按 ToObject 处理，简化为无属性
		}
		for _, key := range props.Keys() {
			descVal, found := props.GetProperty(key)
			if !found {
				continue
			}
			defineOneProperty(obj, key, descVal)
		}
		return obj
	}))

	// Object.getOwnPropertySymbols(obj) (ES6): 返回 Symbol 自有键
	o.SetProperty("getOwnPropertySymbols", object.NewBuiltin("getOwnPropertySymbols", func(args ...object.Value) object.Value {
		result := []object.Value{}
		if len(args) > 0 {
			if obj, ok := args[0].(*object.Object); ok {
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

// ownKeys 返回值的自有键列表，顺序遵循 OrdinaryOwnPropertyKeys。
// 支持普通对象、数组与字符串 (字符串按 UTF-16 码元索引展开)。
func ownKeys(v object.Value) ([]string, object.Value) {
	switch val := v.(type) {
	case *object.Object:
		return val.Keys(), nil
	case *object.Array:
		keys := make([]string, len(val.Elements))
		for i := range val.Elements {
			keys[i] = strconv.Itoa(i)
		}
		return keys, nil
	case *object.String:
		// 规范: Object.keys("ab") === ["0", "1"]
		n := object.UTF16Len(val.Value)
		keys := make([]string, n)
		for i := 0; i < n; i++ {
			keys[i] = strconv.Itoa(i)
		}
		return keys, nil
	case *object.Undefined, *object.Null:
		return nil, object.NewTypeError("Cannot convert undefined or null to object")
	}
	return nil, nil
}

// getOwnProperty 取值的自有属性，未找到时返回 undefined。
func getOwnProperty(v object.Value, key string) (object.Value, bool) {
	switch val := v.(type) {
	case *object.Object:
		if desc, ok := val.Properties[key]; ok {
			if desc.Value == nil {
				return object.UndefinedSingleton, true
			}
			return desc.Value, true
		}
		return object.UndefinedSingleton, false
	case *object.Array:
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
	env.Declare("Boolean", boolFn, false)

	// ===== Function() 构造器 =====
	// new Function("a", "b", "return a+b") 将参数与函数体字符串编译为可调用闭包。
	// 函数方法 (call/apply/bind/toString) 由各可调用类型的 GetProperty 直接
	// 提供 (见 object/funcproto.go)，Function 构造器额外承载 prototype 属性。
	funcFn := object.NewBuiltin("Function", func(args ...object.Value) object.Value {
		return newDynamicFunction(env, args)
	})
	funcFn.SetProperty("name", object.NewString("Function"))
	funcFn.SetProperty("length", object.NewNumber(1))
	funcFn.SetProperty("prototype", object.NewObject())
	env.Declare("Function", funcFn, false)

	// NaN, Infinity, undefined 全局常量
	env.Declare("NaN", object.NewNumber(math.NaN()), true)
	env.Declare("Infinity", object.NewNumber(math.Inf(1)), true)
	env.Declare("undefined", object.UndefinedSingleton, true)
}

// newDynamicFunction 实现 new Function([p1, ..., pn], body)。
// 规范语义: 除最后一个参数外都是形参名，最后一个参数是函数体源码。
// 将其包装为函数表达式编译，取编译产物中的 FunctionMetadata 构建
// 闭包；闭包的 Env 是全局环境 (动态函数只访问全局作用域)。
func newDynamicFunction(env *runtime.Environment, args []object.Value) object.Value {
	params := make([]string, 0, len(args))
	body := ""
	for i, a := range args {
		if i < len(args)-1 {
			params = append(params, toStr(a))
		} else {
			body = toStr(a)
		}
	}
	// 基本合法性检查: 形参不能含 ) { 等破坏结构的内容
	for _, p := range params {
		for _, ch := range p {
			if ch == ')' || ch == '{' || ch == '}' || ch == ',' || ch == '[' || ch == ']' {
				return object.NewErrorWithName("SyntaxError", fmt.Sprintf("Unexpected character %q in argument list", string(ch)))
			}
		}
	}
	src := "(function anonymous(" + strings.Join(params, ",") + ") {\n" + body + "\n})"

	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return object.NewErrorWithName("SyntaxError", p.Errors().String())
	}
	c := compiler.New()
	if err := c.Compile(program); err != nil {
		return object.NewErrorWithName("SyntaxError", err.Error())
	}

	// 从常量池取出编译产物中的函数元数据
	var meta *bytecode.FunctionMetadata
	for i := 0; i < c.Constants().Len(); i++ {
		if fm, ok := c.Constants().Get(uint16(i)).(*bytecode.FunctionMetadata); ok {
			meta = fm
		}
	}
	if meta == nil {
		return object.NewErrorWithName("SyntaxError", "Function constructor: failed to compile body")
	}

	fn := compiledFunctionFromMeta(meta, c.Constants().Constants)
	return &object.Closure{Fn: fn, Env: env}
}

// defineOneProperty 按 property descriptor 定义一个属性。
// 访问器描述符 (get/set) 注册为访问器；数据描述符 (value/writable)
// 注册为数据属性。供 defineProperty / defineProperties 共用。
func defineOneProperty(obj *object.Object, key string, descVal object.Value) {
	desc, ok := descVal.(*object.Object)
	if !ok {
		obj.SetProperty(key, descVal)
		return
	}
	// 访问器描述符: get / set —— 注册为访问器属性，而不是立即求值
	getter, hasGet := desc.GetProperty("get")
	setter, hasSet := desc.GetProperty("set")
	if hasGet || hasSet {
		var g, s object.Value
		if hasGet && !isUndefinedValue(getter) {
			if !object.IsCallable(getter) {
				return
			}
			g = getter
		}
		if hasSet && !isUndefinedValue(setter) {
			if !object.IsCallable(setter) {
				return
			}
			s = setter
		}
		obj.DefineAccessor(key, g, s)
		return
	}

	// 数据描述符: value + writable
	writable := false
	if w, found := desc.GetProperty("writable"); found {
		writable = toBool(w)
	}
	var val object.Value = object.UndefinedSingleton
	if v, found := desc.GetProperty("value"); found {
		val = v
	}
	obj.DefineOwnProperty(key, object.PropertyDescriptor{Value: val, Writable: writable})
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
