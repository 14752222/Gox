package object

import "strings"

// ToString 实现 ECMAScript 的 ToString 抽象转换。
//
// 与 Inspect (调试格式, 供 console.log 使用) 不同，ToString 遵循语言规范:
//   - 数组: 元素递归 ToString 后以 "," 连接，null/undefined 元素计为空串
//     (即 Array.prototype.toString / join 的语义)
//   - 普通对象: 若定义了 toString 方法则调用它，否则 "[object Object]"
//   - 其余类型与 Inspect 一致
//
// depth 防御 toString 返回自身导致的无限递归。
func ToString(v Value) string { return toString(v, 0) }

const maxToStringDepth = 8

func toString(v Value, depth int) string {
	if v == nil {
		return "undefined"
	}
	if depth > maxToStringDepth {
		return "..." // 递归超限，避免栈溢出
	}
	switch t := v.(type) {
	case *BigInt:
		// BigInt 的语言层字符串转换不带 "n" 后缀 (区别于 Inspect)。
		return t.Value.String()
	case *Array:
		var b strings.Builder
		for i, e := range t.Elements {
			if i > 0 {
				b.WriteByte(',')
			}
			switch e.(type) {
			case *Null, *Undefined:
				// join 语义: null/undefined 元素为空串
			default:
				b.WriteString(toString(e, depth+1))
			}
		}
		return b.String()
	case *Object:
		// 用户对象: 优先调用其 toString 方法
		if tv, ok := t.GetProperty("toString"); ok && IsCallable(tv) {
			r := CallFunction(tv, v)
			if r != nil && r != UndefinedSingleton && r != v {
				return toString(r, depth+1)
			}
		}
		return "[object Object]"
	}
	return v.Inspect()
}
