package object

// AsyncGenerator 表示 JavaScript 的异步生成器对象 (async function* 的实例)。
//
// 与同步 Generator 的区别:
//   - next()/return()/throw() 都返回 Promise<{value, done}>;
//   - 体内 await 与 yield 是两种不同的挂起点 (await 内部自动恢复,
//     yield 结算 next() 的 Promise);
//   - 多个并发请求按 FIFO 排队, 依次结算。
//
// 本结构只承载状态与方法装配; 真正的驱动循环 (等待 Promise、区分
// await/yield、错误桥) 由 stdlib 通过 SetAsyncGeneratorMethod 注册,
// 与 object ↔ vm 的回调桥模式一致 (避免 object → stdlib 循环依赖)。
type AsyncGenerator struct {
	Gen   *Generator // 内部同步 generator (await/yield 都编译为挂起点)
	Proto Value      // %AsyncGeneratorPrototype%（可为 nil）

	// Requests 是待处理请求队列 (FIFO)。
	Requests []*AsyncGenRequest
	// Running 表示当前是否正在驱动 (executing 状态)。
	Running bool
	// Done 表示异步生成器是否已完成。
	Done bool
}

// AsyncGenRequest 是一次 next()/return()/throw() 请求。
type AsyncGenRequest struct {
	Kind    int      // AGNextKind / AGReturnKind / AGThrowKind
	Arg     Value    // 传入的参数 (next(v) 的 v / return(v) / throw(e))
	Promise *Promise // 结算用 Promise
}

// 请求种类。
const (
	AGNextKind = iota
	AGReturnKind
	AGThrowKind
)

// asyncGeneratorProto 是全局共享的 %AsyncGeneratorPrototype%。
// 由 stdlib 在 setup 时装配, __async_generator helper 取它赋给实例。
var asyncGeneratorProto Value

// SetAsyncGeneratorProto 设置全局 AsyncGenerator 原型 (由 stdlib 调用)。
func SetAsyncGeneratorProto(v Value) { asyncGeneratorProto = v }

// GetAsyncGeneratorProto 返回全局 AsyncGenerator 原型。
func GetAsyncGeneratorProto() Value { return asyncGeneratorProto }

// asyncIteratorProto 是全局共享的 %AsyncIteratorPrototype%。
//
// 它是异步迭代器原型链的**顶端**: %AsyncGeneratorPrototype% 的
// [[Prototype]] 就是它, 上面挂着 @@asyncIterator (返回 this) 与
// @@asyncDispose (explicit-resource-management)。由 stdlib 在装配
// %AsyncGeneratorPrototype% 时一并注册 —— 两者是同一条链上的两段,
// 分开装配必然出现"链断在中间"。
var asyncIteratorProto Value

// SetAsyncIteratorProto 注册 %AsyncIteratorPrototype% (由 stdlib 调用)。
func SetAsyncIteratorProto(v Value) { asyncIteratorProto = v }

// GetAsyncIteratorProto 返回 %AsyncIteratorPrototype%, 未装配时为 nil。
func GetAsyncIteratorProto() Value { return asyncIteratorProto }

// NewAsyncGenerator 创建异步生成器对象。
func NewAsyncGenerator(gen *Generator) *AsyncGenerator {
	return &AsyncGenerator{Gen: gen}
}

func (g *AsyncGenerator) Type() ObjectType { return ASYNC_GENERATOR_OBJ }
func (g *AsyncGenerator) Inspect() string  { return "[AsyncGenerator]" }
func (g *AsyncGenerator) IsTruthy() bool   { return true }

func (g *AsyncGenerator) GetProperty(name string) (Value, bool) {
	// next/return/throw 由 [[Prototype]] (该函数自己的 .prototype → AGP) 提供,
	// 这样 `ag.next` 与 `AsyncGeneratorPrototype.next` 是同一个函数对象
	// (test262 prop-desc / this-val 系列据此判定), brand check 也落在方法里。
	if g.Proto != nil {
		if v, ok := g.Proto.GetProperty(name); ok {
			return v, true
		}
	}
	// 无原型时的兜底 (老码路径): 仍提供绑定到本实例的 next/return/throw。
	switch name {
	case "next":
		return g.method("next", AGNextKind), true
	case "return":
		return g.method("return", AGReturnKind), true
	case "throw":
		return g.method("throw", AGThrowKind), true
	}
	return nil, false
}

func (g *AsyncGenerator) SetProperty(name string, val Value) {}

// GetProto 返回异步生成器实例的 [[Prototype]]。
//
// 与 *Map / *Set / *String / *Object 同口径补齐: 原型链遍历的事实来源是
// 鸭子类型入口 `interface{ GetProto() Value }`, 缺了这个方法链就在此处断掉
// —— `it[Symbol.toStringTag]` 会读回 undefined, 尽管 AGP 上明明有。
func (g *AsyncGenerator) GetProto() Value { return g.Proto }

// GetSymbolProperty 使 async generator 满足异步迭代协议:
// [Symbol.asyncIterator]() 返回自身。
//
// 其余 Symbol 键**必须沿完整原型链查找** —— 实例 → fn.prototype →
// %AsyncGeneratorPrototype%。此前只向 g.Proto (即 fn.prototype, 一个 *Object)
// 委托一层, 而 *Object.GetSymbolProperty 只查自身 SymbolProperties, 于是落在
// 更外层 AGP 上的 @@toStringTag 永不可达:
//
//	it[Symbol.toStringTag]                       // 应是 "AsyncGenerator"
//	Object.prototype.toString.call(it)           // 应是 "[object AsyncGenerator]"
//
// 二者当时分别得到 undefined / "[object Object]" (test262 未覆盖, 见 commit 说明)。
func (g *AsyncGenerator) GetSymbolProperty(sym *Symbol) (Value, bool) {
	if sym != nil && sym.ID == GetGlobalSymbol("Symbol.asyncIterator").ID {
		return NewBuiltin("[Symbol.asyncIterator]", func(args ...Value) Value { return g }), true
	}
	// 沿 [[Prototype]] 链向上: 起点可能是 *Object (fn.prototype) 或其它实现
	// GetSymbolProperty 的类型; *Object 段由 LookupSymbolProperty 接力走完。
	for cur := g.Proto; cur != nil; {
		switch p := cur.(type) {
		case *Object:
			return LookupSymbolProperty(p, sym)
		case interface {
			GetSymbolProperty(*Symbol) (Value, bool)
		}:
			if v, ok := p.GetSymbolProperty(sym); ok {
				return v, true
			}
			// 该类型内部已自行向上代为查找 (如嵌套 AsyncGenerator),
			// 未命中即视为整条链未命中, 避免重复走链。
			return nil, false
		default:
			return nil, false
		}
	}
	return nil, false
}

func (g *AsyncGenerator) SetSymbolProperty(sym *Symbol, val Value) {}

// method 构造一个绑定到本实例的 next/return/throw 方法。
func (g *AsyncGenerator) method(name string, kind int) Value {
	return NewBuiltin(name, func(args ...Value) Value {
		arg := Value(UndefinedSingleton)
		if len(args) > 0 {
			arg = args[0]
		}
		return AsyncGeneratorMethod(g, kind, arg)
	})
}

// AsyncGeneratorMethodFunc 是驱动异步生成器一次请求的回调类型。
// 返回该请求对应的 Promise。由 stdlib 注册。
type AsyncGeneratorMethodFunc func(g *AsyncGenerator, kind int, arg Value) *Promise

var asyncGeneratorMethod AsyncGeneratorMethodFunc

// SetAsyncGeneratorMethod 注册异步生成器驱动回调 (由 stdlib 初始化时调用)。
func SetAsyncGeneratorMethod(f AsyncGeneratorMethodFunc) {
	asyncGeneratorMethod = f
}

// AsyncGeneratorMethod 驱动异步生成器处理一次请求。
// 未注册回调时返回一个 rejected Promise (而非 panic)。
func AsyncGeneratorMethod(g *AsyncGenerator, kind int, arg Value) *Promise {
	if asyncGeneratorMethod == nil {
		p := NewPromise()
		p.Reject(NewErrorWithName("TypeError", "async generator driver not registered"))
		return p
	}
	return asyncGeneratorMethod(g, kind, arg)
}

// NewAsyncGeneratorIterResult 构造迭代结果对象 { value, done }。
func NewAsyncGeneratorIterResult(value Value, done bool) *Object {
	res := NewObject()
	res.SetProperty("value", value)
	res.SetProperty("done", NewBoolean(done))
	return res
}
