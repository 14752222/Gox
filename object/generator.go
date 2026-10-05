package object

// Generator 表示 JavaScript 的生成器对象 (function* 的实例)。
// 生成器调用时不立即执行函数体, 而是返回一个 Generator。
// 每次 next() 从上次 yield 的位置继续执行, 直到下一个 yield 或函数结束。
//
// 执行驱动 (genResume) 在 VM 中实现, 通过 SetGeneratorNext 注册的回调接入:
// object.GeneratorNext(gen, arg) → (value, done)。
// 这样可以避免 object → vm 的循环依赖。
type Generator struct {
	Closure *Closure // 生成器函数的闭包 (含 this 绑定)
	Args    []Value  // 首次启动时传入的参数

	Started bool  // 是否已启动 (第一次 next 之后为 true)
	Done    bool  // 是否已完成
	Value   Value // 最近一次 yield 的值 / 最终返回值

	// LastYieldIsAwait 记录最近一次挂起是 await 还是 yield。
	// async generator 的驱动必须区分二者: await 是内部挂起点 (自动恢复),
	// yield 是消费者可见的挂起点 (结算 next() 的 Promise)。
	// 普通同步 generator 与 async 函数的驱动都忽略它。
	LastYieldIsAwait bool

	// 暂停状态 (Started 后有效):
	PC           int     // 恢复点 (yield 指令之后的下一条指令)
	Locals       []Value // 暂停时的局部变量
	SavedStack   []Value // 暂停时帧栈上残留的中间值 (如 2 + (yield 3) 中的 2)
	StackDepth   int     // 暂停时帧栈深度 (恢复时校验)
	Constants    []Value // 暂停时的常量池 (函数元数据的常量)
	Instructions []byte  // 暂停时的字节码 (恢复帧使用)

	// PendingTries 保存在 yield 时属于本 generator 帧的 try 处理器条目。
	// 全局处理器栈在 generator 挂起期间不能持有它们: 恢复时帧深度可能
	// 不同，且无关代码抛出的异常绝不能被挂起中的 generator 捕获。
	// 恢复帧时由 VM 按相对值换算后重新挂回。
	PendingTries []GenTryEntry
}

// GenTryEntry 是 generator 挂起期间保存的 try 处理器条目 (相对值)。
// 语义由 vm 包解释。
type GenTryEntry struct {
	CatchPC      int // catch 块 PC (0 = 无 catch)
	FinallyPC    int // finally 块 PC (0 = 无 finally)
	RelStackBase int // 相对 generator 帧栈基址的 try 时的栈高度
	RelFrameIdx  int // 相对 generator 帧索引的偏移 (通常为 0)

	// InFinally / PendingVal: 条目已进入自己的 finally 体时挂起的异常值。
	// yield 若发生在 finally 体内 (或它的保护范围内), 这两项必须一并保存 ——
	// 否则恢复后 OP_END_FINALLY 找不到挂起值, 原异常会被静默吞掉。
	InFinally  bool
	PendingVal Value

	// PendingReturn 标记 PendingVal 是一个 return 完成而非 throw 完成
	// (async generator 的 return() 触发的 finally 展开)。
	PendingReturn bool
}

// NewGenerator 创建生成器对象。
func NewGenerator(closure *Closure, args []Value) *Generator {
	if args == nil {
		args = []Value{}
	}
	return &Generator{
		Closure: closure,
		Args:    args,
		Done:    false,
	}
}

func (g *Generator) Type() ObjectType { return GENERATOR_OBJ }
func (g *Generator) Inspect() string  { return "[Generator]" }
func (g *Generator) IsTruthy() bool   { return true }
func (g *Generator) GetProperty(name string) (Value, bool) {
	if name == "next" {
		return &BuiltinFunction{
			Name: "next",
			Fn: func(args ...Value) Value {
				arg := Value(UndefinedSingleton)
				if len(args) > 0 {
					arg = args[0]
				}
				val, done := GeneratorNext(g, arg)
				// 构造 { value, done } 对象
				res := NewObject()
				res.SetProperty("value", val)
				res.SetProperty("done", NewBoolean(done))
				return res
			},
		}, true
	}
	return nil, false
}
func (g *Generator) SetProperty(name string, val Value) {}

// GeneratorNextFunc 是驱动生成器前进的回调函数类型。
// 由 VM 注册: 恢复生成器帧并运行到下一个 yield 或结束。
// 返回: (yield 值/返回值, 是否完成)。
type GeneratorNextFunc func(gen *Generator, arg Value) (Value, bool)

// GeneratorThrowFunc 是把异常抛入生成器 (从 yield 点恢复) 的回调类型。
// 用于 await 的 promise 被 reject 时恢复 async 函数体，让函数体内的
// try/catch 能捕获该异常。
type GeneratorThrowFunc func(gen *Generator, throwVal Value) (Value, bool)

// GeneratorReturnFunc 是把 return 完成注入生成器 (从 yield 点恢复) 的回调类型。
// 用于 async generator 的 return()：等价于在挂起点执行 `return v`,
// 会展开体内的 finally 块。返回值语义同 GeneratorNext (返回值/是否完成)。
type GeneratorReturnFunc func(gen *Generator, returnVal Value) (Value, bool)

// generatorNext 是 VM 注册的回调。
var generatorNext GeneratorNextFunc

// generatorThrow 是 VM 注册的异常恢复回调。
var generatorThrow GeneratorThrowFunc

// generatorReturn 是 VM 注册的 return 完成恢复回调。
var generatorReturn GeneratorReturnFunc

// SetGeneratorNext 注册生成器驱动回调 (由 vm 包初始化时调用)。
func SetGeneratorNext(f GeneratorNextFunc) {
	generatorNext = f
}

// SetGeneratorThrow 注册异常恢复回调 (由 vm 包初始化时调用)。
func SetGeneratorThrow(f GeneratorThrowFunc) {
	generatorThrow = f
}

// SetGeneratorReturn 注册 return 完成恢复回调 (由 vm 包初始化时调用)。
func SetGeneratorReturn(f GeneratorReturnFunc) {
	generatorReturn = f
}

// GeneratorNext 驱动生成器前进一步。
// 未注册回调时返回 (undefined, true) (视为已完成)。
func GeneratorNext(gen *Generator, arg Value) (Value, bool) {
	if generatorNext == nil {
		return UndefinedSingleton, true
	}
	return generatorNext(gen, arg)
}

// GeneratorThrow 把 throwVal 作为异常抛入生成器，从 yield 点恢复执行。
// 未注册回调时视为已完成。
func GeneratorThrow(gen *Generator, throwVal Value) (Value, bool) {
	if generatorThrow == nil {
		return UndefinedSingleton, true
	}
	return generatorThrow(gen, throwVal)
}

// GeneratorReturn 把 returnVal 作为 return 完成注入生成器，从 yield 点恢复。
// 未注册回调时视为已完成。
func GeneratorReturn(gen *Generator, returnVal Value) (Value, bool) {
	if generatorReturn == nil {
		return returnVal, true
	}
	return generatorReturn(gen, returnVal)
}
