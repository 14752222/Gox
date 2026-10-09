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

// GetProto 返回字符串实例的 [[Prototype]] (即 %String.prototype%)。
//
// 此前 *String 没有这个方法 —— 于是原型链遍历的鸭子类型入口
// (LookupSymbolPropertyDescriptorChain 靠 `interface{ GetProto() Value }`
// 续走下一环) 在字符串处**断链**: 即便 String.prototype 上已装配
// @@iterator, `"abc"[Symbol.iterator]` 仍恒为 undefined。数组 (*Array) 有
// GetProto 所以没这个问题 —— 同一个缺口在不同类型上表现不一致, 正是它
// 长期没被发现的原因。
func (s *String) GetProto() Value { return StringProto }

func (s *String) IsTruthy() bool {
	return s.Value != ""
}

func (s *String) GetProperty(name string) (Value, bool) {
	switch name {
	case "length":
		// JavaScript 的字符串长度以 UTF-16 码元计，而非 UTF-8 字节数。
		// "世" 的 length 是 1 而不是 3；"😀" 的 length 是 2 而不是 4。
		return NewInt(int64(UTF16Len(s.Value))), true
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
