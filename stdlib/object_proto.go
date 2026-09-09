package stdlib

import (
	"math"
	"strconv"

	"js-runtime/object"
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
	proto.SetProperty("constructor", o)

	// toString(): 输出 "[object Tag]"。
	// 它接收任意 this —— 通过 .call/.apply 反射时可以作用于任何值，
	// 因此实现必须对所有内置类型有标签，而不能假设 this 是 *object.Object。
	proto.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		return object.NewString("[" + objectPrototypeTagFor(this) + "]")
	}))

	// toLocaleString: 本运行时无 Intl，语义与 toString 相同。
	proto.SetProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		return object.NewString("[" + objectPrototypeTagFor(this) + "]")
	}))

	// valueOf(): 返回对象本身。
	proto.SetProperty("valueOf", object.NewBuiltinMethod("valueOf", func(this object.Value, args ...object.Value) object.Value {
		if this == nil {
			return object.UndefinedSingleton
		}
		return this
	}))

	// hasOwnProperty(): 只查自有属性，不沿原型链。
	proto.SetProperty("hasOwnProperty", object.NewBuiltinMethod("hasOwnProperty", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(hasOwnPropertyImpl(this, args[0]))
	}))

	// propertyIsEnumerable(): 自有且可枚举 (本运行时属性均视为可枚举)。
	proto.SetProperty("propertyIsEnumerable", object.NewBuiltinMethod("propertyIsEnumerable", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(hasOwnPropertyImpl(this, args[0]))
	}))

	// isPrototypeOf(): 检查 this 是否出现在参数的原型链上。
	proto.SetProperty("isPrototypeOf", object.NewBuiltinMethod("isPrototypeOf", func(this object.Value, args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(isPrototypeOfImpl(this, args[0]))
	}))

	o.SetProperty("prototype", proto)
}

// objectPrototypeTagFor 返回 Object.prototype.toString 所用的内置标签
// (不含方括号)。与规范内部标签表一致。
func objectPrototypeTagFor(v object.Value) string {
	if v == nil {
		return "Undefined"
	}
	switch t := v.(type) {
	case *object.Undefined:
		return "Undefined"
	case *object.Null:
		return "Null"
	case *object.Number:
		return "Number"
	case *object.String:
		return "String"
	case *object.Boolean:
		return "Boolean"
	case *object.BigInt:
		return "BigInt"
	case *object.Symbol:
		return "Symbol"
	case *object.Array:
		return "Array"
	case *object.Map:
		return "Map"
	case *object.Set:
		return "Set"
	case *object.WeakRef:
		return "WeakRef"
	case *object.FinalizationRegistry:
		return "FinalizationRegistry"
	case *object.RegExp:
		return "RegExp"
	case *object.Promise:
		return "Promise"
	case *object.Error:
		return "Error"
	case *object.Generator:
		return "Generator"
	case *object.Proxy:
		return "Object"
	case *object.ArrayBuffer:
		return "ArrayBuffer"
	case *object.DataView:
		return "DataView"
	case *object.TypedArray:
		if t.Kind.Name != "" {
			return t.Kind.Name
		}
		return "TypedArray"
	case *object.JSIterator:
		return "Iterator"
	case *object.Closure, *object.CompiledFunction, *object.BuiltinFunction, *object.BuiltinMethod:
		return "Function"
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
	case *object.Object:
		// 普通对象: Symbol.toStringTag 优先。
		// 对象字面量的 [Symbol.toStringTag] 存进 SymbolProperties，但同一
		// 全局符号可能因注册时机不同而有多个实例 (ID 不同)，故第一遍按
		// 全局符号精确查，第二遍按键的 Description 兜底扫描。
		tagSym := object.GetGlobalSymbol("Symbol.toStringTag")
		if tagSym != nil {
			if sv, ok := t.GetSymbolProperty(tagSym); ok {
				if s, ok := sv.(*object.String); ok && s.Value != "" {
					return s.Value
				}
			}
		}
		for _, sym := range t.SymbolKeyList {
			if sym.Description != "Symbol.toStringTag" {
				continue
			}
			if sv, ok := t.GetSymbolProperty(sym); ok {
				if s, ok := sv.(*object.String); ok && s.Value != "" {
					return s.Value
				}
			}
		}
		return "Object"
	}
	return "Object"
}

// hasOwnPropertyImpl 实现 hasOwnProperty 的"自有属性"查询。
func hasOwnPropertyImpl(this object.Value, key object.Value) bool {
	if this == nil {
		return false
	}
	name := propertyKeyString(key)
	switch t := this.(type) {
	case *object.Object:
		return t.HasOwnProperty(name)
	case *object.Array:
		if name == "length" {
			return true
		}
		if idx, err := strconv.Atoi(name); err == nil {
			return idx >= 0 && idx < len(t.Elements)
		}
		return false
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
