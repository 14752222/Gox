package object

import "strconv"

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
	// 整数值不显示小数点: 5.0 → "5"
	if n.Value == float64(int64(n.Value)) && !isInf(n.Value) && !isNaN(n.Value) {
		return strconv.FormatInt(int64(n.Value), 10)
	}
	return strconv.FormatFloat(n.Value, 'f', -1, 64)
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
	return f != f
}

// isInf 检查是否为无穷大
func isInf(f float64) bool {
	return f > 1e308 || f < -1e308
}

// NewNumber 创建数字值的便捷函数
func NewNumber(v float64) *Number {
	return &Number{Value: v}
}

// NewInt 从整数创建数字值
func NewInt(v int64) *Number {
	return &Number{Value: float64(v)}
}
