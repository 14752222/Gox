package object

// Boolean 表示 JavaScript 的布尔类型。
type Boolean struct {
	Value bool
}

func (b *Boolean) Type() ObjectType { return BOOLEAN_OBJ }
func (b *Boolean) Inspect() string {
	if b.Value {
		return "true"
	}
	return "false"
}

func (b *Boolean) IsTruthy() bool {
	return b.Value
}

func (b *Boolean) GetProperty(name string) (Value, bool) {
	// Boolean 原始值没有自有属性 (typeof / valueOf 等一律沿 %Boolean.prototype%
	// 查找)。此前直接返回 (nil,false) —— 于是 `true.toString()` /
	// `true.hasOwnProperty` 全部取不到。
	if BooleanProto != nil {
		return BooleanProto.GetProperty(name)
	}
	return nil, false
}

func (b *Boolean) SetProperty(name string, val Value) {
	// 原始类型不能设置属性
}

// BooleanProto 是 Boolean 原始值的原型对象 (Boolean.prototype)。
// 由 stdlib 包初始化时设置 —— Boolean 原始值没有自有属性，
// 属性访问 (含 @@toStringTag) 一律沿它查找。
var BooleanProto Value

// SetBooleanProto 设置全局布尔原型 (由 stdlib 调用)。
func SetBooleanProto(p Value) { BooleanProto = p }

// GetBooleanProto 返回全局布尔原型。
func GetBooleanProto() Value { return BooleanProto }

// NewBoolean 创建布尔值的便捷函数
func NewBoolean(v bool) *Boolean {
	return &Boolean{Value: v}
}

// 全局布尔单例 (避免重复分配)
var (
	TrueSingleton  = &Boolean{Value: true}
	FalseSingleton = &Boolean{Value: false}
)
