package object

import "strconv"

// Array 表示 JavaScript 的数组类型。
// 数组是特殊的对象，其属性名为数字索引，有 length 属性。
type Array struct {
	Elements []Value
	// proto 是数组对象的原型 (指向 ArrayProto)
	// 在标准库初始化时通过 SetArrayProto 设置
	proto Value
	// ExtraProps 存储非数字索引的额外属性 (如 exec 返回的 index, input)
	ExtraProps map[string]Value
}

// ArrayProto 是所有数组实例的原型对象。
// 由 stdlib 包初始化时设置。包含 push, pop, map 等方法。
var ArrayProto Value

// SetArrayProto 设置全局数组原型 (由 stdlib 调用)。
func SetArrayProto(p Value) { ArrayProto = p }

func (a *Array) Type() ObjectType { return ARRAY_OBJ }
func (a *Array) Inspect() string {
	var elems []string
	for _, e := range a.Elements {
		if e == nil {
			elems = append(elems, "null")
			continue
		}
		// 字符串在数组中显示带引号
		if s, ok := e.(*String); ok {
			elems = append(elems, `"`+s.Value+`"`)
		} else {
			elems = append(elems, e.Inspect())
		}
	}
	return "[" + joinStrings(elems, ", ") + "]"
}

func (a *Array) IsTruthy() bool { return true }

func (a *Array) GetProperty(name string) (Value, bool) {
	switch name {
	case "length":
		return NewInt(int64(len(a.Elements))), true
	}

	// 额外属性 (如 index, input)
	if a.ExtraProps != nil {
		if val, ok := a.ExtraProps[name]; ok {
			return val, true
		}
	}

	// 数字索引访问
	if idx, err := strconv.Atoi(name); err == nil {
		if idx >= 0 && idx < len(a.Elements) {
			if a.Elements[idx] == nil {
				return UndefinedSingleton, true
			}
			return a.Elements[idx], true
		}
		return UndefinedSingleton, true
	}

	// 原型链查找
	if a.proto != nil {
		return a.proto.GetProperty(name)
	}
	return nil, false
}

func (a *Array) SetProperty(name string, val Value) {
	if name == "length" {
		// 可以通过设置 length 截断或扩展数组
		if n, ok := val.(*Number); ok {
			newLen := int(n.Value)
			if newLen >= 0 {
				if newLen < len(a.Elements) {
					a.Elements = a.Elements[:newLen]
				} else {
					for len(a.Elements) < newLen {
						a.Elements = append(a.Elements, UndefinedSingleton)
					}
				}
			}
		}
		return
	}

	// 数字索引设置
	if idx, err := strconv.Atoi(name); err == nil {
		if idx >= 0 {
			for len(a.Elements) <= idx {
				a.Elements = append(a.Elements, UndefinedSingleton)
			}
			a.Elements[idx] = val
		}
		return
	}

	// 额外属性
	if a.ExtraProps == nil {
		a.ExtraProps = make(map[string]Value)
	}
	a.ExtraProps[name] = val
}

// SetProto 设置数组的原型
func (a *Array) SetProto(p Value) { a.proto = p }

// GetProto 返回数组的原型
func (a *Array) GetProto() Value { return a.proto }

// NewArray 创建数组值的便捷函数。
// 自动设置数组原型。
func NewArray(elements []Value) *Array {
	return &Array{Elements: elements, proto: ArrayProto}
}

// joinStrings 连接字符串切片 (避免引入 strings 包)
func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
