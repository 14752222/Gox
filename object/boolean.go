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
	return nil, false
}

func (b *Boolean) SetProperty(name string, val Value) {
	// 原始类型不能设置属性
}

// NewBoolean 创建布尔值的便捷函数
func NewBoolean(v bool) *Boolean {
	return &Boolean{Value: v}
}

// 全局布尔单例 (避免重复分配)
var (
	TrueSingleton  = &Boolean{Value: true}
	FalseSingleton = &Boolean{Value: false}
)
