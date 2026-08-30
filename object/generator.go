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

	Started bool    // 是否已启动 (第一次 next 之后为 true)
	Done    bool    // 是否已完成
	Value   Value   // 最近一次 yield 的值 / 最终返回值

	// 暂停状态 (Started 后有效):
	PC           int     // 恢复点 (yield 指令之后的下一条指令)
	Locals      []Value // 暂停时的局部变量
	SavedStack  []Value // 暂停时帧栈上残留的中间值 (如 2 + (yield 3) 中的 2)
	StackDepth  int     // 暂停时帧栈深度 (恢复时校验)
	Constants   []Value // 暂停时的常量池 (函数元数据的常量)
	Instructions []byte // 暂停时的字节码 (恢复帧使用)
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

func (g *Generator) Type() ObjectType       { return GENERATOR_OBJ }
func (g *Generator) Inspect() string         { return "[Generator]" }
func (g *Generator) IsTruthy() bool          { return true }
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

// generatorNext 是 VM 注册的回调。
var generatorNext GeneratorNextFunc

// SetGeneratorNext 注册生成器驱动回调 (由 vm 包初始化时调用)。
func SetGeneratorNext(f GeneratorNextFunc) {
	generatorNext = f
}

// GeneratorNext 驱动生成器前进一步。
// 未注册回调时返回 (undefined, true) (视为已完成)。
func GeneratorNext(gen *Generator, arg Value) (Value, bool) {
	if generatorNext == nil {
		return UndefinedSingleton, true
	}
	return generatorNext(gen, arg)
}
