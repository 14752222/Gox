package object

// String 表示 JavaScript 的字符串类型。
type String struct {
	Value string
}

// StringProto 是所有字符串实例的原型对象。
// 由 stdlib 包初始化时设置。包含 toUpperCase, split 等方法。
var StringProto Value

// SetStringProto 设置全局字符串原型 (由 stdlib 调用)。
func SetStringProto(p Value) { StringProto = p }

func (s *String) Type() ObjectType { return STRING_OBJ }
func (s *String) Inspect() string {
	return s.Value
}

func (s *String) IsTruthy() bool {
	return s.Value != ""
}

func (s *String) GetProperty(name string) (Value, bool) {
	switch name {
	case "length":
		return NewInt(int64(len(s.Value))), true
	}
	// 原型链查找
	if StringProto != nil {
		return StringProto.GetProperty(name)
	}
	return nil, false
}

func (s *String) SetProperty(name string, val Value) {
	// 字符串是不可变的
}

// NewString 创建字符串值的便捷函数
func NewString(v string) *String {
	return &String{Value: v}
}
