package object

import "fmt"

// Error 表示 JavaScript 的错误对象。
type Error struct {
	Message string
	Name    string // "Error", "TypeError", "RangeError" 等
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
	case "toString":
		// 返回一个内建函数，调用时返回错误字符串
		return &BuiltinFunction{
			Name: "toString",
			Fn: func(args ...Value) Value {
				return NewString(e.Inspect())
			},
		}, true
	}
	return nil, false
}

func (e *Error) SetProperty(name string, val Value) {
	// Error 属性不可修改
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
