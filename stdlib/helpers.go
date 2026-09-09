package stdlib

import (
	"math"
	"strconv"
	"strings"

	"js-runtime/object"
)

// toFloat 将任意 object.Value 转换为 float64。
//
// 遵循 ECMAScript 的 ToNumber 抽象操作:
//
//	Number("")         -> 0
//	Number("  42  ")   -> 42
//	Number("12px")     -> NaN   (不截断解析)
//	Number("Infinity") -> +Inf
//	Number([])         -> 0
//	Number([5])        -> 5
//	Number([1, 2])     -> NaN
//	Number(null)       -> 0
//	Number(undefined)  -> NaN
//	Number(true)       -> 1
func toFloat(v object.Value) float64 {
	if v == nil {
		return math.NaN()
	}
	switch val := v.(type) {
	case *object.Number:
		return val.Value
	case *object.Boolean:
		if val.Value {
			return 1
		}
		return 0
	case *object.Null:
		return 0
	case *object.Undefined:
		return math.NaN()
	case *object.String:
		return stringToNumber(val.Value)
	case *object.Array:
		// 数组先按 ToPrimitive 转为字符串再转数字:
		// [] -> "" -> 0, [5] -> "5" -> 5, [1,2] -> "1,2" -> NaN
		switch len(val.Elements) {
		case 0:
			return 0
		case 1:
			return toFloat(val.Elements[0])
		default:
			return math.NaN()
		}
	}
	return math.NaN()
}

// stringToNumber 实现 ECMAScript 的 StringToNumber。
//
// 关键点: Go 的 strconv.ParseFloat 会截断式接受 "inf"/"infinity"/"nan" 等
// 拼写，而 ECMAScript 只接受精确的 "Infinity"/"-Infinity"/"+Infinity"。
// 因此这里先做精确匹配，其余以字母开头的输入一律判为 NaN。
func stringToNumber(s string) float64 {
	t := trimJSSpace(s)
	if t == "" {
		return 0
	}
	switch t {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}
	// 十六进制整数字面量: 0x1F -> 31
	if len(t) > 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		if v, err := strconv.ParseInt(t[2:], 16, 64); err == nil {
			return float64(v)
		}
		return math.NaN()
	}
	// 拒绝 Go 特有的 inf/nan 拼写 (ECMAScript 不识别)
	if startsWithInfOrNaN(t) {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// startsWithInfOrNaN 判断去掉符号后的字面量是否以 inf/nan 开头。
// 用于屏蔽 strconv.ParseFloat 对 "inf"/"nan" 的额外支持。
func startsWithInfOrNaN(t string) bool {
	body := t
	if body != "" && (body[0] == '+' || body[0] == '-') {
		body = body[1:]
	}
	if body == "" {
		return false
	}
	c := body[0]
	return c == 'i' || c == 'I' || c == 'n' || c == 'N'
}

// trimJSSpace 去除 ECMAScript 定义的空白字符 (StrWhiteSpace)。
// 比 strings.TrimSpace 更严格: 只处理 JS 认可的空白，避免把
// Unicode 空白 (如 U+00A0) 也吃掉。
func trimJSSpace(s string) string {
	return strings.Trim(s, "\t\n\v\f\r \u00a0\ufeff")
}

// toInt 将任意 object.Value 转换为 int64。
//
// 遵循 ECMAScript 的 ToIntegerOrInfinity: NaN 与 ±0 都映射为 0，
// 无穷大钳位到 int64 边界，其余向零取整。
func toInt(v object.Value) int64 {
	f := toFloat(v)
	switch {
	case math.IsNaN(f), f == 0:
		return 0
	case math.IsInf(f, 1):
		return math.MaxInt64
	case math.IsInf(f, -1):
		return math.MinInt64
	}
	truncated := math.Trunc(f)
	// float64(math.MaxInt64) 向上取整为 2^63，用作保守边界
	const int64Limit = 9223372036854775808.0
	if truncated >= int64Limit {
		return math.MaxInt64
	}
	if truncated <= -int64Limit {
		return math.MinInt64
	}
	return int64(truncated)
}

// toStr 将任意 object.Value 转换为 string。
// 使用 ECMAScript ToString 语义 (数组 join、对象 [object Object]、
// 自定义 toString 优先)，见 object.ToString。
func toStr(v object.Value) string {
	return object.ToString(v)
}

// isFalsy 判断值是否为假值。
func isFalsy(v object.Value) bool {
	return object.IsFalsy(v)
}

// toBool 将任意 object.Value 转换为 bool。
func toBool(v object.Value) bool {
	return !isFalsy(v)
}

// thisTypeError 为原型方法生成统一的 TypeError，用于替代
// "this 类型不匹配时静默返回默认值" 的写法。
//
// 形如: "Array.prototype.push called on incompatible receiver string"
func thisTypeError(proto, method string, this object.Value) object.Value {
	if this == nil {
		return object.NewTypeError("%s.prototype.%s called on null or undefined", proto, method)
	}
	return object.NewTypeError("%s.prototype.%s called on incompatible receiver %s",
		proto, method, object.TypeOf(this))
}

// isArrayValue 判断值是否为数组。
func isArrayValue(v object.Value) bool {
	_, ok := v.(*object.Array)
	return ok
}

// isUndefinedValue 判断值是否为 undefined (含 nil)。
func isUndefinedValue(v object.Value) bool {
	if v == nil {
		return true
	}
	_, ok := v.(*object.Undefined)
	return ok
}

// isNullValue 判断值是否为 null。
func isNullValue(v object.Value) bool {
	_, ok := v.(*object.Null)
	return ok
}

// clampIndex 将可能为负的数组索引规范化到 [0, n] 区间。
// 遵循 ECMAScript 语义: 负数表示从末尾倒数 (-1 是最后一个元素)。
func clampIndex(idx int64, n int) int {
	if idx < 0 {
		idx = int64(n) + idx
		if idx < 0 {
			idx = 0
		}
	}
	if idx > int64(n) {
		idx = int64(n)
	}
	return int(idx)
}
