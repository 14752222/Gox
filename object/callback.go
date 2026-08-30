package object

// CallFunc 是从 Go 代码回调 JS 函数的函数类型。
// fn: 要调用的函数 (Closure 或 BuiltinFunction)
// this: this 绑定 (可为 nil)
// args: 参数列表
// 返回: 函数的返回值
type CallFunc func(fn Value, this Value, args []Value) Value

// callFunction 是 VM 注册的回调函数，用于从 stdlib 调用 JS 闭包。
var callFunction CallFunc

// SetCallFunction 注册 VM 的回调函数。
// 由 vm 包在初始化时调用，建立 stdlib → object → vm 的回调桥。
func SetCallFunction(f CallFunc) {
	callFunction = f
}

// CallFunction 调用 JS 函数 (闭包或内建函数)。
// 返回函数的执行结果。如果未注册回调，返回 undefined。
func CallFunction(fn Value, this Value, args ...Value) Value {
	if callFunction == nil {
		return UndefinedSingleton
	}
	return callFunction(fn, this, args)
}

// IsCallable 检查值是否可调用 (是 Closure 或 BuiltinFunction)。
func IsCallable(v Value) bool {
	switch v.(type) {
	case *Closure, *BuiltinFunction, *BuiltinMethod:
		return true
	}
	return false
}
