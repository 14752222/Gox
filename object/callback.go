package object

// CallFunc 是从 Go 代码回调 JS 函数的函数类型。
// fn: 要调用的函数 (Closure 或 BuiltinFunction)
// this: this 绑定 (可为 nil)
// args: 参数列表
// 返回: 函数的返回值
type CallFunc func(fn Value, this Value, args []Value) Value

// callFunction 是 VM 注册的回调函数，用于从 stdlib 调用 JS 闭包。
var callFunction CallFunc

// callbackError 记录最近一次 CallFunction 期间 JS 代码抛出的异常。
//
// 为什么需要它: VM 的错误是通过返回值传播的 (*Error 且非 ReturnIsValue)，
// 但 `throw "string"` 抛出的不是 Error 对象，Go 侧无法从返回值判断
// "函数正常返回" 还是 "函数抛出了异常"。回调桥在这里补一个明确的错误信号。
//
// 该字段在每次 CallFunction 开始时清空，因此只在紧随其后读取才有意义。
var callbackError error

// SetCallFunction 注册 VM 的回调函数。
// 由 vm 包在初始化时调用，建立 stdlib → object → vm 的回调桥。
func SetCallFunction(f CallFunc) {
	callFunction = f
}

// SetCallbackError 由回调桥在 JS 代码抛出异常时调用。
func SetCallbackError(err error) { callbackError = err }

// TakeCallbackError 取出并清空最近一次 CallFunction 产生的异常。
// 返回 nil 表示回调正常返回。
func TakeCallbackError() error {
	err := callbackError
	callbackError = nil
	return err
}

// CallFunction 调用 JS 函数 (闭包或内建函数)。
// 返回函数的执行结果。如果未注册回调，返回 undefined。
//
// 调用后可用 TakeCallbackError() 判断被调函数是否抛出了异常 —— 这对于
// 需要区分"返回值"与"异常"的场景 (如 Promise executor) 是必需的。
func CallFunction(fn Value, this Value, args ...Value) Value {
	if callFunction == nil {
		return UndefinedSingleton
	}
	callbackError = nil
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

// callbackErrorValue 记录最近一次 CallFunction 抛出的原始 JS 值
// (throw x 的 x)。cbErr 只有 Go error 字符串, 丢失了值本身;
// Promise.try/executor 等需要把原始值作为 rejection reason。
var callbackErrorValue Value

// SetCallbackErrorValue 由回调桥在 JS 抛出异常时记录原始抛出值。
func SetCallbackErrorValue(v Value) { callbackErrorValue = v }

// TakeCallbackErrorValue 取出并清除原始抛出值。
// 返回 undefined 表示没有记录。
func TakeCallbackErrorValue() Value {
	v := callbackErrorValue
	callbackErrorValue = nil
	if v == nil {
		return UndefinedSingleton
	}
	return v
}
