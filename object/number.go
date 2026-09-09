package object

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Number 表示 JavaScript 的数字类型。
// JavaScript 不区分整数和浮点数，所有数字都是 IEEE 754 double (float64)。
type Number struct {
	Value float64
}

// NumberProto 是所有数字实例的原型对象。
// 由 stdlib 包初始化时设置。包含 toFixed, toPrecision 等方法。
var NumberProto Value

// SetNumberProto 设置全局数字原型 (由 stdlib 调用)。
func SetNumberProto(p Value) { NumberProto = p }

func (n *Number) Type() ObjectType { return NUMBER_OBJ }
func (n *Number) Inspect() string {
	return NumberToString(n.Value)
}

// NumberToString 实现 ECMAScript 的 Number::toString (十进制)。
//
// 与 Go 的 strconv 默认格式化有两处关键差异:
//   - NaN / ±Infinity 输出 "NaN" / "Infinity" / "-Infinity"。
//     Go 的 FormatFloat 会输出 "+Inf"/"-Inf"，不符合 JS 字面量。
//   - |x| >= 1e21 或 0 < |x| < 1e-6 时必须使用指数记法。
//     Go 会把 1e21 展开成 "1000000000000000000000"，而 JS 输出 "1e+21"。
func NumberToString(x float64) string {
	switch {
	case math.IsNaN(x):
		return "NaN"
	case math.IsInf(x, 1):
		return "Infinity"
	case math.IsInf(x, -1):
		return "-Infinity"
	case x == 0:
		// +0 与 -0 都字符串化为 "0"
		return "0"
	}
	abs := math.Abs(x)
	if abs >= 1e21 || abs < 1e-6 {
		return formatExponential(x)
	}
	return strconv.FormatFloat(x, 'f', -1, 64)
}

// formatExponential 按 ECMAScript 的指数记法格式化数字。
// Go 的 'e' 格式输出 "1e+21"/"1.5e-07"，而 JS 的指数不补前导零:
// "1e+21"/"1.5e-7"。
func formatExponential(x float64) string {
	s := strconv.FormatFloat(x, 'e', -1, 64)
	eIdx := strings.IndexByte(s, 'e')
	if eIdx < 0 {
		return s
	}
	mantissa := s[:eIdx]
	exp := s[eIdx+1:]

	sign := ""
	if exp != "" && (exp[0] == '+' || exp[0] == '-') {
		sign = exp[:1]
		exp = exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mantissa + "e" + sign + exp
}

func (n *Number) IsTruthy() bool {
	return n.Value != 0 && !isNaN(n.Value)
}

func (n *Number) GetProperty(name string) (Value, bool) {
	// 原型链查找
	if NumberProto != nil {
		return NumberProto.GetProperty(name)
	}
	return nil, false
}

func (n *Number) SetProperty(name string, val Value) {
	// 原始类型不能设置属性
}

// isNaN 检查是否为 NaN
func isNaN(f float64) bool {
	return math.IsNaN(f)
}

// isInf 检查是否为无穷大
func isInf(f float64) bool {
	return math.IsInf(f, 0)
}

// NewNumber 创建数字值的便捷函数
func NewNumber(v float64) *Number {
	// 小整数缓存: 循环计数器/索引是最常见的装箱来源 ([-1, 256])。
	// 命中时零分配, 显著降低解释循环的 GC 压力。
	if v == float64(int64(v)) && v >= -1 && v <= 256 {
		return &smallIntCache[int(v)+1]
	}
	return &Number{Value: v}
}

// NewInt 从整数创建数字值
func NewInt(v int64) *Number {
	if v >= -1 && v <= 256 {
		return &smallIntCache[v+1]
	}
	return &Number{Value: float64(v)}
}

// smallIntCache 由包初始化填充。
var smallIntCache [258]Number

func init() {
	for i := range smallIntCache {
		smallIntCache[i] = Number{Value: float64(i - 1)}
	}
}

// ParseJSNumber 实现 ECMAScript 的 StringToNumber (字符串转数字)。
//
// 语义: 空白裁剪后空串为 0; 仅接受 "Infinity"/"-Infinity";
// 支持 0x/0X 十六进制整数字面量; 拒绝 Go 特有的 "inf"/"nan" 拼写;
// 其余走 strconv.ParseFloat, 非法输入返回 NaN。
//
// 这是所有 "字符串 → 数字" 路径的唯一实现 (VM 的 ToNumber、
// TypedArray 元素写入、Number() 全局等), 避免各处语义漂移,
// 也替换掉原先解释热路径上的 fmt.Sscanf (慢一个数量级以上)。
func ParseJSNumber(s string) float64 {
	t := jsTrimSpace(s)
	if t == "" {
		return 0
	}
	switch t[0] {
	case 'I', 'i', '+', '-', 'n', 'N':
		switch t {
		case "Infinity", "+Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		// 拒绝 Go 特有的 inf/nan 拼写 (ECMAScript 不识别)
		body := t
		if body[0] == '+' || body[0] == '-' {
			body = body[1:]
		}
		if body != "" {
			switch body[0] {
			case 'i', 'I', 'n', 'N':
				return math.NaN()
			}
		}
	}
	// 十六进制整数字面量: 0x1F -> 31
	if len(t) > 2 && t[0] == '0' && (t[1] == 'x' || t[1] == 'X') {
		if v, err := strconv.ParseInt(t[2:], 16, 64); err == nil {
			return float64(v)
		}
		return math.NaN()
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return math.NaN()
	}
	return f
}

// jsTrimSpace 去除 ECMAScript 定义的空白字符 (StrWhiteSpace):
// \t \n \v \f \r 空格 U+00A0 U+FEFF。
// 按 rune 解码扫描, 多字节空白整段跳过。
func jsTrimSpace(s string) string {
	isWS := func(r rune) bool {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x00A0, 0xFEFF:
			return true
		}
		return false
	}
	start := 0
	for start < len(s) {
		r, size := utf8.DecodeRuneInString(s[start:])
		if !isWS(r) {
			break
		}
		start += size
	}
	end := len(s)
	for end > start {
		r, size := utf8.DecodeLastRuneInString(s[:end])
		if !isWS(r) {
			break
		}
		end -= size
	}
	return s[start:end]
}

