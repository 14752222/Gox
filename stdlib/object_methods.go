package stdlib

import (
	"math"
	"strings"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupObjectGlobal 创建 Object 构造函数对象。
func setupObjectGlobal() *object.Object {
	o := object.NewObject()
	o.SetProperty("name", object.NewString("Object"))

	// Object.keys(obj)
	o.SetProperty("keys", object.NewBuiltin("keys", func(args ...object.Value) object.Value {
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
			result := make([]object.Value, len(arr.Elements))
			for i := range arr.Elements {
				result[i] = object.NewString(intToString(i))
			}
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	// Object.values(obj)
	o.SetProperty("values", object.NewBuiltin("values", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		if obj, ok := args[0].(*object.Object); ok {
			keys := obj.Keys()
			result := make([]object.Value, len(keys))
			for i, k := range keys {
				val, _ := obj.GetProperty(k)
				result[i] = val
			}
			return object.NewArray(result)
		}
		if arr, ok := args[0].(*object.Array); ok {
			result := make([]object.Value, len(arr.Elements))
			copy(result, arr.Elements)
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	// Object.entries(obj)
	o.SetProperty("entries", object.NewBuiltin("entries", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		if obj, ok := args[0].(*object.Object); ok {
			keys := obj.Keys()
			result := make([]object.Value, len(keys))
			for i, k := range keys {
				val, _ := obj.GetProperty(k)
				result[i] = object.NewArray([]object.Value{
					object.NewString(k),
					val,
				})
			}
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
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

	// Object.freeze(obj) - 简化: 标记为不可扩展
	o.SetProperty("freeze", object.NewBuiltin("freeze", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if obj, ok := args[0].(*object.Object); ok {
			obj.Extensible = false
		}
		return args[0]
	}))

	// Object.isFrozen(obj)
	o.SetProperty("isFrozen", object.NewBuiltin("isFrozen", func(args ...object.Value) object.Value {
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
		desc, ok := args[2].(*object.Object)
		if !ok {
			return args[0]
		}
		// 从描述符获取 value
		if val, exists := desc.GetProperty("value"); exists {
			obj.SetProperty(key, val)
		} else {
			// getter/setter 简化处理: 如果有 get，则调用 get 获取值
			if getter, exists := desc.GetProperty("get"); exists {
				if object.IsCallable(getter) {
					val := object.CallFunction(getter, nil)
					obj.SetProperty(key, val)
				}
			}
		}
		return args[0]
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
				result[i] = object.NewString(intToString(i))
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
				val, _ := obj.GetProperty(k)
				desc := object.NewObject()
				desc.SetProperty("value", val)
				desc.SetProperty("writable", object.NewBoolean(true))
				desc.SetProperty("enumerable", object.NewBoolean(true))
				desc.SetProperty("configurable", object.NewBoolean(true))
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

// intToString 将整数转换为字符串 (用于数组索引)。
func intToString(i int) string {
	if i == 0 {
		return "0"
	}
	negative := false
	if i < 0 {
		negative = true
		i = -i
	}
	var digits []byte
	for i > 0 {
		digits = append(digits, byte('0'+i%10))
		i /= 10
	}
	// 反转
	for j, k := 0, len(digits)-1; j < k; j, k = j+1, k-1 {
		digits[j], digits[k] = digits[k], digits[j]
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
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

// setupNumberGlobal 创建 Number 构造函数对象。
func setupNumberGlobal() *object.Object {
	n := object.NewObject()
	n.SetProperty("name", object.NewString("Number"))
	n.SetProperty("MAX_SAFE_INTEGER", object.NewNumber(9007199254740991))
	n.SetProperty("MIN_SAFE_INTEGER", object.NewNumber(-9007199254740991))
	n.SetProperty("MAX_VALUE", object.NewNumber(1.7976931348623157e+308))
	n.SetProperty("MIN_VALUE", object.NewNumber(5e-324))
	n.SetProperty("POSITIVE_INFINITY", object.NewNumber(math.Inf(1)))
	n.SetProperty("NEGATIVE_INFINITY", object.NewNumber(math.Inf(-1)))
	n.SetProperty("NaN", object.NewNumber(math.NaN()))
	n.SetProperty("EPSILON", object.NewNumber(2.220446049250313e-16))
	n.SetProperty("isInteger", object.NewBuiltin("isInteger", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if num, ok := args[0].(*object.Number); ok {
			if num.Value != num.Value { // NaN
				return object.NewBoolean(false)
			}
			if num.Value == float64(int64(num.Value)) {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))
	n.SetProperty("isFinite", object.NewBuiltin("isFinite", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if num, ok := args[0].(*object.Number); ok {
			if num.Value != num.Value { // NaN
				return object.NewBoolean(false)
			}
			if num.Value > 1e308 || num.Value < -1e308 {
				return object.NewBoolean(false)
			}
			return object.NewBoolean(true)
		}
		return object.NewBoolean(false)
	}))
	n.SetProperty("isNaN", object.NewBuiltin("isNaN", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		if num, ok := args[0].(*object.Number); ok {
			return object.NewBoolean(num.Value != num.Value)
		}
		return object.NewBoolean(true)
	}))
	n.SetProperty("parseFloat", object.NewBuiltin("parseFloat", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(toFloat(args[0]))
	}))
	n.SetProperty("parseInt", object.NewBuiltin("parseInt", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		radix := 10
		if len(args) > 1 {
			radix = int(toFloat(args[1]))
		}
		s := toStr(args[0])
		result, ok := parseIntString(s, radix)
		if !ok {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(float64(result))
	}))
	return n
}

// setupBooleanGlobal 创建 Boolean 构造函数对象。
func setupBooleanGlobal() *object.Object {
	b := object.NewObject()
	b.SetProperty("name", object.NewString("Boolean"))
	return b
}

// setupNumberFunctions 设置全局数字相关函数。
func setupNumberFunctions(env *runtime.Environment) {
	env.Declare("parseInt", object.NewBuiltin("parseInt", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		radix := 10
		if len(args) > 1 {
			radix = int(toFloat(args[1]))
		}
		s := toStr(args[0])
		result, ok := parseIntString(s, radix)
		if !ok {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(float64(result))
	}), false)

	env.Declare("parseFloat", object.NewBuiltin("parseFloat", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(math.NaN())
		}
		return object.NewNumber(toFloat(args[0]))
	}), false)

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
		if v != v { // NaN
			return object.NewBoolean(false)
		}
		if v > 1e308 || v < -1e308 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(true)
	}), false)
}

// setupGlobalFunctions 设置全局函数。
func setupGlobalFunctions(env *runtime.Environment) {
	// String() 全局函数，带静态方法
	strFn := object.NewBuiltin("String", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewString("")
		}
		return object.NewString(toStr(args[0]))
	})
	// String.fromCharCode
	strFn.SetProperty("fromCharCode", object.NewBuiltin("fromCharCode", func(args ...object.Value) object.Value {
		var b strings.Builder
		for _, arg := range args {
			code := int(toFloat(arg))
			if code >= 0 && code <= 0x10FFFF {
				b.WriteRune(rune(code))
			}
		}
		return object.NewString(b.String())
	}))
	env.Declare("String", strFn, false)

	// Number() 全局函数，带静态属性和方法
	numFn := object.NewBuiltin("Number", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewNumber(0)
		}
		return object.NewNumber(toFloat(args[0]))
	})
	numFn.SetProperty("MAX_SAFE_INTEGER", object.NewNumber(9007199254740991))
	numFn.SetProperty("MIN_SAFE_INTEGER", object.NewNumber(-9007199254740991))
	numFn.SetProperty("POSITIVE_INFINITY", object.NewNumber(math.Inf(1)))
	numFn.SetProperty("NEGATIVE_INFINITY", object.NewNumber(math.Inf(-1)))
	numFn.SetProperty("NaN", object.NewNumber(math.NaN()))
	numFn.SetProperty("EPSILON", object.NewNumber(2.220446049250313e-16))
	numFn.SetProperty("isInteger", object.NewBuiltin("isInteger", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if num, ok := args[0].(*object.Number); ok {
			if num.Value != num.Value {
				return object.NewBoolean(false)
			}
			if num.Value == float64(int64(num.Value)) {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))
	numFn.SetProperty("isFinite", object.NewBuiltin("isFinite", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if num, ok := args[0].(*object.Number); ok {
			if num.Value != num.Value {
				return object.NewBoolean(false)
			}
			if num.Value > 1e308 || num.Value < -1e308 {
				return object.NewBoolean(false)
			}
			return object.NewBoolean(true)
		}
		return object.NewBoolean(false)
	}))
	numFn.SetProperty("isNaN", object.NewBuiltin("isNaN", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(true)
		}
		if num, ok := args[0].(*object.Number); ok {
			return object.NewBoolean(num.Value != num.Value)
		}
		return object.NewBoolean(true)
	}))
	env.Declare("Number", numFn, false)

	// Boolean() 全局函数
	env.Declare("Boolean", object.NewBuiltin("Boolean", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(toBool(args[0]))
	}), false)

	// NaN, Infinity, undefined 全局常量
	env.Declare("NaN", object.NewNumber(math.NaN()), true)
	env.Declare("Infinity", object.NewNumber(math.Inf(1)), true)
	env.Declare("undefined", object.UndefinedSingleton, true)
}
