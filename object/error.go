package object

import "fmt"

// Error 表示 JavaScript 的错误对象。
type Error struct {
	Message string
	Name    string // "Error", "TypeError", "RangeError" 等
	// Properties 存储附加属性。
	// 用于 AggregateError.errors 等标准错误子类型的额外字段 ——
	// 过去 SetProperty 是空实现，导致这些属性写入后被静默丢弃。
	Properties map[string]Value
}

func (e *Error) Type() ObjectType { return ERROR_OBJ }
func (e *Error) Inspect() string {
	if e.Name == "" {
		return "Error: " + e.Message
	}
	return e.Name + ": " + e.Message
}

func (e *Error) IsTruthy() bool { return true }

func (e *Error) GetProperty(name string) (Value, bool) {
	switch name {
	case "message":
		return NewString(e.Message), true
	case "name":
		n := e.Name
		if n == "" {
			n = "Error"
		}
		return NewString(n), true
	case "stack":
		return NewString(e.Inspect()), true
	case "toString":
		// 返回一个内建函数，调用时返回错误字符串
		return &BuiltinFunction{
			Name: "toString",
			Fn: func(args ...Value) Value {
				return NewString(e.Inspect())
			},
		}, true
	}
	if e.Properties != nil {
		if v, ok := e.Properties[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// SetProperty 写入附加属性 (如 AggregateError.errors)。
// 内置的 name/message/stack 由 GetProperty 优先返回，不受影响。
func (e *Error) SetProperty(name string, val Value) {
	if e.Properties == nil {
		e.Properties = make(map[string]Value)
	}
	e.Properties[name] = val
}

// NewError 创建普通错误
func NewError(msg string) *Error {
	return &Error{Message: msg, Name: "Error"}
}

// NewErrorWithName 创建带类型名的错误
func NewErrorWithName(name, msg string) *Error {
	return &Error{Message: msg, Name: name}
}

// NewTypeError 创建类型错误
func NewTypeError(format string, args ...interface{}) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Name: "TypeError"}
}

// NewRangeError 创建范围错误
func NewRangeError(format string, args ...interface{}) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Name: "RangeError"}
}

// NewReferenceError 创建引用错误
func NewReferenceError(format string, args ...interface{}) *Error {
	return &Error{Message: fmt.Sprintf(format, args...), Name: "ReferenceError"}
}
