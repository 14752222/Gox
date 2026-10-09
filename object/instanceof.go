package object

// MatchBuiltinType 对内置类型做名称匹配 (instanceof Array/Map/Set/...)。
//
// 这是 instanceof 的**兜底**: 原型链查不到时按构造器名判断。Gox 的部分内置
// 类型 (*Map / *Set / *Promise / ...) 不在 ProtoOf 的类型穷举里 —— 它们的
// 实例原型链遍历会提前返回 nil, 只有这条兜底能给出正确结论。
//
// 与 OrdinaryHasInstance 共享: `x instanceof C` 与 `C[Symbol.hasInstance](x)`
// 必须给出**同样的结论**, 两处各写一份匹配表迟早分叉。
func MatchBuiltinType(left, right Value) bool {
	ctorName := BuiltinName(right)
	if ctorName == "" {
		return false
	}
	lt := left.Type()
	switch ctorName {
	case "Array":
		return lt == ARRAY_OBJ
	case "Object":
		// 数组、对象、函数等都是 Object 的实例
		return lt == OBJECT_OBJ || lt == ARRAY_OBJ || IsCallable(left) ||
			lt == MAP_OBJ || lt == SET_OBJ || lt == REGEXP_OBJ ||
			lt == PROMISE_OBJ || lt == ERROR_OBJ
	case "Map":
		return lt == MAP_OBJ
	case "Set":
		return lt == SET_OBJ
	case "RegExp":
		return lt == REGEXP_OBJ
	case "Promise":
		return lt == PROMISE_OBJ
	case "Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError":
		return lt == ERROR_OBJ
	case "SuppressedError":
		// explicit resource management: 释放期合成错误。精确按 Name 匹配 ——
		// 不能并进上面那组 (那组对任意 ERROR_OBJ 都返回真)。
		if e, ok := left.(*Error); ok {
			return e.Name == "SuppressedError"
		}
		return false
	case "String":
		return lt == STRING_OBJ
	case "Number":
		return lt == NUMBER_OBJ
	case "Boolean":
		return lt == BOOLEAN_OBJ
	case "Function":
		return IsCallable(left)
	}
	return false
}

// BuiltinName 返回内置构造器/函数的名称。
func BuiltinName(v Value) string {
	switch f := v.(type) {
	case *BuiltinFunction:
		if f.Name != "" {
			return f.Name
		}
	case *BuiltinMethod:
		if f.Name != "" {
			return f.Name
		}
	}
	// 通过属性名兜底 (closure 构造器一般带 name)
	if o, ok := v.(*Object); ok {
		if nv, found := o.GetProperty("name"); found {
			if s, ok := nv.(*String); ok {
				return s.Value
			}
		}
	}
	return ""
}
