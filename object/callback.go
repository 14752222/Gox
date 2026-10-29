package object

// CallFunc 是从 Go 代码回调 JS 函数的函数类型。
// fn: 要调用的函数 (Closure 或 BuiltinFunction)
// this: this 绑定 (可为 nil)
// args: 参数列表
// 返回: 函数的返回值
type CallFunc func(fn Value, this Value, args []Value) Value

// Bridge 收敛 object 包回调桥的可变状态:
// callFunction / callbackError / callbackErrorValue 及三个 generator 驱动回调。
// 原实现是 6 个散落的包级变量; 收拢为显式 struct 后,
// 测试可用 NewBridge + SetDefaultBridge 隔离实例, 避免用例间相互污染。
type Bridge struct {
	// callFunction 是 VM 注册的回调函数，用于从 stdlib 调用 JS 闭包。
	callFunction CallFunc
	// callbackError 记录最近一次 CallFunction 期间 JS 代码抛出的异常。
	callbackError error
	// callbackErrorValue 记录最近一次 CallFunction 抛出的原始 JS 值 (throw x 的 x)。
	callbackErrorValue Value
	// generatorNext / generatorThrow / generatorReturn 是 VM 注册的生成器驱动回调。
	generatorNext   GeneratorNextFunc
	generatorThrow  GeneratorThrowFunc
	generatorReturn GeneratorReturnFunc
}

// NewBridge 返回一个空的回调桥实例 (供测试隔离使用)。
func NewBridge() *Bridge { return &Bridge{} }

// defaultBridge 是包级函数默认使用的回调桥实例。
var defaultBridge = NewBridge()

// DefaultBridge 返回当前默认回调桥实例。
func DefaultBridge() *Bridge { return defaultBridge }

// SetDefaultBridge 替换默认回调桥实例, 返回原实例 (测试可用 defer 恢复)。
func SetDefaultBridge(b *Bridge) (prev *Bridge) {
	prev, defaultBridge = defaultBridge, b
	return prev
}

// SetCallFunction 注册 VM 的回调函数。
// 由 vm 包在初始化时调用，建立 stdlib → object → vm 的回调桥。
func SetCallFunction(f CallFunc) {
	defaultBridge.callFunction = f
}

// SetCallbackError 由回调桥在 JS 代码抛出异常时调用。
func SetCallbackError(err error) { defaultBridge.callbackError = err }

// clearCallbackErrorSlots 成对清空两个错误槽。
// callbackError 与 callbackErrorValue 在抛错时同时置位, 消费时也必须
// 同时清除 —— 只清其一会让另一个槽残留陈旧错误, 泄漏到后续任意
// TakeCallbackError* 判定 (rFXXbq: 同分片跨用例污染, 表现为
// Iterator/from/supports-iterator 等用例判定随邻居翻转)。
func clearCallbackErrorSlots() {
	defaultBridge.callbackError = nil
	defaultBridge.callbackErrorValue = nil
}

// TakeCallbackError 取出并清空最近一次 CallFunction 产生的异常。
// 返回 nil 表示回调正常返回。
func TakeCallbackError() error {
	err := defaultBridge.callbackError
	defaultBridge.callbackError = nil
	return err
}

// CallFunction 调用 JS 函数 (闭包或内建函数)。
// 返回函数的执行结果。如果未注册回调，返回 undefined。
//
// 调用后可用 TakeCallbackError() 判断被调函数是否抛出了异常 —— 这对于
// 需要区分"返回值"与"异常"的场景 (如 Promise executor) 是必需的。
func CallFunction(fn Value, this Value, args ...Value) Value {
	if defaultBridge.callFunction == nil {
		return UndefinedSingleton
	}
	return defaultBridge.callFunction(fn, this, args)
}

// ConstructFunc 是从 Go 代码构造 JS 对象 (new fn(...args)) 的函数类型。
type ConstructFunc func(fn Value, args []Value) Value

// constructFunction 是 VM 注册的构造回调 (ArraySpeciesCreate 等使用)。
var constructFunction ConstructFunc

// SetConstructFunction 注册 VM 的构造回调。
func SetConstructFunction(f ConstructFunc) { constructFunction = f }

// ConstructFunction 以 fn 为构造器构造对象。错误信号同 CallFunction 走回调桥。
func ConstructFunction(fn Value, args ...Value) Value {
	if constructFunction == nil {
		return UndefinedSingleton
	}
	return constructFunction(fn, args)
}

// IsConstructor 实现规范的 IsConstructor (sec-ecmascript-data-types-and-values)。
// 判断值是否实现 [[Construct]] —— 与"可调用"不等价: 箭头函数可调用但不可构造，
// generator/async 函数同理；内建函数里原型方法/getter/命名空间函数也没有
// [[Construct]] (靠 NotConstructable 标记)。
// Proxy 一律视为可构造 (规范由 target 决定，这里放宽)。
func IsConstructor(v Value) bool {
	switch f := v.(type) {
	case *Closure:
		if f.IsArrow {
			return false
		}
		if f.Fn != nil && (f.Fn.IsGenerator || f.Fn.IsAsync) {
			return false
		}
		return true
	case *BuiltinFunction:
		return !f.NotConstructable
	case *BuiltinMethod:
		// BuiltinMethod 无 NotConstructable 标记: 原型方法无 [[Construct]]。
		return false
	case *Proxy:
		return true
	}
	return false
}

// IsCallable 检查值是否可调用 (是 Closure 或 BuiltinFunction)。
func IsCallable(v Value) bool {
	switch v.(type) {
	case *Closure, *BuiltinFunction, *BuiltinMethod:
		return true
	}
	return false
}

// SetCallbackErrorValue 由回调桥在 JS 抛出异常时记录原始抛出值。
func SetCallbackErrorValue(v Value) { defaultBridge.callbackErrorValue = v }

// TakeCallbackErrorValue 取出并清除原始抛出值。
// 返回 undefined 表示没有记录。
func TakeCallbackErrorValue() Value {
	v := defaultBridge.callbackErrorValue
	defaultBridge.callbackErrorValue = nil
	if v == nil {
		return UndefinedSingleton
	}
	return v
}
