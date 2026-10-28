package vm

import (
	"sync"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

type tryEntry struct {
	catchPC   int // catch 块的 PC (0 = 无 catch)
	finallyPC int // finally 块的 PC (0 = 无 finally)
	stackBase int // 进入 try 时的栈高度
	frameIdx  int // 进入 try 时的帧索引

	// catchReturnPC: return 完成拦截 PC (0 = 不拦截)。由 OP_PUSH_RET_TRY 置位。
	// 只有 async generator 的 yield* 异步委托用它 —— 委托期消费者 return(v)
	// 必须转发给被委托迭代器 (规范 YieldExpression: yield* 6.c), 而不是直接
	// 结束生成器。throw 完成仍走 catchPC, 二者共存于同一条目。
	catchReturnPC int

	// inFinally 标记本条目已进入自己的 finally 体 (由 handleThrowInner 置位)。
	//
	// 此时条目**留在 tryStack 上**, 由 pendingVal 承载挂起的异常值, 直到:
	//   - finally 体正常结束 ⇒ OP_END_FINALLY 弹出它并重新抛出 pendingVal;
	//   - finally 体里又抛出异常 ⇒ 展开经过本条目时把它丢弃 (新异常覆盖旧异常)。
	//
	// 挂起值不放在 VM 全局变量里, 是因为旧的全局 pendingThrow 有两类坏形状:
	//   (1) finally 里 return / throw 直接离开本帧 ⇒ 挂起值没机会被清, 残留到
	//       之后某个毫不相干的 END_FINALLY 上被误重抛;
	//   (2) 嵌套 finally 互相覆盖 ⇒ 外层挂起值被内层清掉, 原异常被静默吞掉。
	// 挂在条目上则天然随条目出栈而消失, 嵌套时各条目各持一份。
	inFinally bool
	// pendingVal 是进入 finally 时挂起的异常值 (inFinally 为真时有效)。
	pendingVal object.Value

	// pendingReturn 标记挂起值是一个 **return 完成** 而非 throw 完成
	// (inFinally 为真时有效)。async generator 的 return() 从挂起点注入
	// `return v` 时需要展开体内 finally: 进入 finally 时置位, OP_END_FINALLY
	// 结束时据此把完成向外传播而不是重新抛出。
	pendingReturn bool
}

// ModuleExports 存储模块的导出。
//
// 导出有三种来源, 解析优先级 (规范: 显式命名导出 > export *):
//  1. Named/Default/bindings: 本模块的直接导出 (default 走 Default, 词法声明走
//     bindings 活绑定);
//  2. forwards: 具名再导出 `export {a as b} from "m"` 与命名空间再导出
//     `export * as ns from "m"` —— 读时才去源模块导出槽取值, 是"取值时解析"的
//     活绑定等效语义 (见 resolve);
//  3. stars: `export * from "m"` —— 转发源模块的自有可枚举导出, **不含 default**,
//     且**不覆盖**本模块已存在的同名导出 (规范 16.2.3.5 GetExportedNames /
//     ResolveExport)。
//
// 枚举顺序口径 (Object.keys(namespace)): 先按本模块导出登记顺序 (order),
// 再按星号源出现顺序拼接各自登记顺序并去重; 没有登记顺序的导出 (内置模块直接
// 塞进 Named 的 map) 按名字字典序补在末尾。Go map 迭代是随机的, 不排序会让
// namespace 每次物化的键序都不同 —— 这里给一个稳定口径。

type VM struct {
	frames     []*Frame             // 调用栈
	frameIdx   int                  // 当前帧索引 (栈顶)
	stack      *Stack               // 操作数栈
	globals    *runtime.Environment // 全局变量环境
	constants  *bytecode.ConstantPool
	lastPopped object.Value // 最后弹出的值 (用于测试)

	// try-catch-finally 支持。
	// 「进入 finally 后待重抛的异常」不是 VM 全局状态: 它挂在 tryStack 的条目上
	// (tryEntry.inFinally / pendingVal), 这样控制转移离开 finally 时不会残留。
	tryStack []tryEntry // try 处理器栈

	// 模块系统
	modules        map[string]*ModuleExports // 模块缓存 (按绝对路径)
	moduleBase     string                    // 模块基准路径 (用于解析相对路径)
	currentExports *ModuleExports            // 当前模块的导出对象
	// moduleMode 标记"本 VM 的主帧是模块顶层"。仅影响主帧 this 取值:
	// script 顶层 this = globalThis, module 顶层 this = undefined (见 OP_THIS)。
	moduleMode bool

	// generator 支持
	currentGenerator *object.Generator // 当前正在执行的 generator (OP_YIELD 时使用)

	// throwBoundary 是当前 runFrom 子执行允许解退到的最低帧索引。
	// 回调桥 (callFunction→runFrom) 的子帧里抛出的异常，不允许直接
	// 解退到外层帧的 try 处理器 —— 否则帧/PC 被改写而 Go 侧的内建
	// 循环 (如 map) 仍在继续执行，错误值会被误当作回调返回值。
	// 边界外的处理器留给错误传播回外层后、由外层的抛出路径匹配。
	throwBoundary int

	// pendingDirectEval 标记「紧接着的这次调用是直接 eval 候选」(OP_EVAL_MARK /
	// OP_EVAL_MARK_INIT 置位)。OP_CALL / OP_CALL_SPREAD 取出后立即清零, 仅当
	// 被调恰为全局 %eval% 内建时才把调用者帧的 this 传给 eval 内建。按调用
	// 取用 (而非 stdlib 包级全局常驻) 可避免多 VM 并发互相干扰与遮蔽泄漏。
	pendingDirectEval bool

	// pendingEvalInit 是 pendingDirectEval 的细化: 本次直接 eval 发生在类
	// 字段初始化器内 (OP_EVAL_MARK_INIT 置位)。命中时额外让 eval 内建进入
	// 受限早错模式。随 pendingDirectEval 一同清零。
	pendingEvalInit bool

	// pendingEvalHome* / hasPendingEvalHome: OP_EVAL_MARK_HOME 家族的
	// 指令携带的「直接 eval 调用点 super home 上下文」(roiE5Z)。名字取自
	// 指令操作数 (常量池里的类名/父类名, 执行标记的帧解析)。与 this /
	// new.target 两桥同纪律: 紧跟的 OP_CALL / OP_CALL_SPREAD 经
	// consumeEvalMark 取出并清零 (无论被调是否恰为 %eval%), 不会泄漏给
	// 后续无关调用; 不带 home 语境的 OP_EVAL_MARK 也会清空它们 (表示
	// 「该语境无 home」)。注意 OP_EVAL_MARK_INIT **不清** home —— 它可
	// 叠加在 HOME 标记之后 (字段初始化器 + home 语境)。
	pendingEvalHomeName   string
	pendingEvalHomeStatic bool
	pendingEvalHomeThis   bool
	pendingEvalSuperName  string
	hasPendingEvalHome    bool

	// pendingNewTarget / hasPendingNewTarget: super() 调用的 new.target 传递。
	// OP_NEW_TARGET_MARK (编译器在 super(...) 前发射) 把**当前帧的 new.target**
	// 记到这里; 紧随其后的方法调用装配父构造器帧时消费 (见 callClosure)。
	// 规范 sec-super-keyword: Construct(func, argList, GetNewTarget()) —— 父构造器
	// 里的 new.target 即派生类最初的构造目标。OP_NEW 也复用同一通道 (写被 new 的
	// 构造器)。消费即清零, 非 super 调用不置位。
	pendingNewTarget    object.Value
	hasPendingNewTarget bool

	// 模板字面量分段收集器 (支持嵌套): 每层对应一个 OP_TEMPLATE_START，
	// 该层内 quasi/表达式产生的字符串依次 append，OP_TEMPLATE_END 时 join 入栈。
	// 不用操作数栈保存段的原因是模板可能作为二元运算的操作数出现——
	// 栈上模板段之外还有外层操作数，无法区分边界。
	tplParts [][]object.Value

	// 微任务队列 (Promise Jobs, 见 microtask.go): promise settle / 已结算
	// then 注册的反应回调在此排队, 于脚本顶层结束、每个宏任务后 drain。
	microtasks         []func()
	microtaskMu        sync.Mutex
	drainingMicrotasks bool

	// ── 运行时错误源码帧 (T05 / M2) ──
	// mainPositions 是主脚本"语句首条指令 offset → 源码位置"的升序表;
	// 函数体错误查抛出帧闭包的 Positions 表 (offset 空间按编译单元隔离)。
	//
	// M2: 位置表记录的是**编译单元 (转译后 JS)** 的行列, 而展示要给用户 .ts 原文。
	// 所以源码信息按"编译单元"建模为 srcUnit:
	//   mainUnit  当前脚本 (入口/模块顶层) 的单元;
	//   units     *CompiledFunction → 它所属单元的注册表 (跨 VM 共享 —— 模块 A
	//             导出的函数可能被入口调用, 那时抛错帧属于 A 的单元, 而当前 VM
	//             是入口)。注册发生在 createClosure。
	srcFile        string // 顶层脚本文件名 (兼容 SetSourceInfo 的读取方)
	srcText        string // 顶层脚本文本
	mainUnit       *srcUnit
	units          *unitRegistry
	mainPositions  []object.SrcPos
	lastThrowPC    int
	hasThrowPC     bool
	lastThrowFrame *Frame

	// ── panic 诊断 (T04) ──
	// 主循环每取指一条记录一次, panic 恢复时输出, 用于定位 VM 缺陷。
	dbgOp     bytecode.Opcode // 最近取出的 opcode
	dbgPC     int             // 该指令的起始 PC (取指前)
	dbgFrames int             // 当前帧数

	// frameFree 是弹出帧的复用池 (T2-4 热点优化)。
	// Frame 结构体本身不被任何长生命周期对象持有 (生成器只保存 Locals 切片
	// 与 PC/指令等状态, 不保存 *Frame), 弹出后可整体回收复用。池上限天然
	// 受 MaxFrames 约束, 不会无界增长。Locals 切片**不**进池: 逃逸闭包以
	// frame.Locals 为 binding cell 持有其底层数组 (见 createClosure), 复用
	// 会让后续调用改写旧闭包可见的绑定。
	frameFree []*Frame

	// callArgsScratch 是 OP_CALL 的参数装填复用缓冲 (T2-4 热点优化)。
	// 每次调用 make 一次 args 切片是 fib28 剖面里 ~1.19M 次/run 的分配源。
	// 仅 *object.Closure 非生成器分支直接使用 (callClosure 同步拷贝消费,
	// 不保留切片); 内建/生成器/代理分支各自拷贝后才使用。
	callArgsScratch []object.Value
}

// New 创建虚拟机。
// ins: 主程序字节码, constants: 常量池, numLocals: 主程序局部变量数。

func New(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int) *VM {
	frames := make([]*Frame, MaxFrames)
	frames[0] = NewFrame(ins, constants, numLocals)
	return &VM{
		frames:    frames,
		frameIdx:  0,
		stack:     NewStack(),
		globals:   runtime.NewEnvironment(),
		constants: constants,
		modules:   map[string]*ModuleExports{},
		units:     newUnitRegistry(),
	}
}

// NewWithGlobals 创建带预设全局变量的虚拟机。

func NewWithGlobals(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int, globals *runtime.Environment) *VM {
	frames := make([]*Frame, MaxFrames)
	frames[0] = NewFrame(ins, constants, numLocals)
	return &VM{
		frames:    frames,
		frameIdx:  0,
		stack:     NewStack(),
		globals:   globals,
		constants: constants,
		modules:   map[string]*ModuleExports{},
		units:     newUnitRegistry(),
	}
}

// Globals 返回全局环境。

func (vm *VM) Globals() *runtime.Environment { return vm.globals }

// RunTimers 运行定时器事件循环。
// 阻塞等待下一个定时器到期并执行其回调，直到没有活跃定时器。
// 返回执行期间遇到的错误 (如有)。

func (vm *VM) LastPopped() object.Value { return vm.lastPopped }

// currentFrame 返回当前执行帧。

func (vm *VM) Run() error {
	return vm.execute()
}

// RunCompiled 从编译器输出启动执行。

func (vm *VM) RunCompiled(c *compiler.Compiler) error {
	vm.frames[0] = NewFrame(c.Bytes(), c.Constants(), c.NumLocals())
	vm.frameIdx = 0
	return vm.execute()
}

// RunCompiledAsync 以"主单元即生成器"的方式驱动含顶层 await 的模块。
//
// 模块顶层是 +Await 上下文 (TLA): 顶层 await 被编译成主单元字节码里的
// OP_YIELD 挂起点。这里把主单元包装成一个无闭包生成器 (Closure==nil),
// 逐次恢复; 每次挂起后按 await 语义结算操作数。
//
// 已知边界 (本版刻意不做): 结算建立在 Gox 的"同步 Promise 模型"上 ——
// 非 Promise 原样透传, 已结算的 Promise 由 Then 回调同步取值。
// **pending** 的 Promise (典型: `await new Promise(r => setTimeout(r, 0))`)
// 需要真实异步模块图求值 (§16.2.1.5.2 ExecuteAsyncModule /
// AsyncModuleExecutionFulfilled), 本版不实现: 直接报错而不是在模块加载期
// 重入事件循环 —— 模块加载发生在入口脚本执行期, 期间跑事件循环会污染
// runner 的包级状态并导致分片超时 (实测)。
//
// 与 RunCompiled 的差别仅在驱动方式: 主帧仍是 frame#0, 顶层声明的槽位与
// OP_EXPORT_BINDING 的读取器都指向同一份 Locals (rebuildGenFrame 对
// Closure==nil 的生成器强制复用数组)。

func (vm *VM) RunCompiledAsync(c *compiler.Compiler) error {
	gen := &object.Generator{
		PC:            0,
		Started:       false,
		PrologueBound: true, // 首帧由 gen.PC/Locals 直接重建, 无闭包可调用
		Locals:        make([]object.Value, c.NumLocals()),
		Constants:     c.Constants().Constants,
		Instructions:  c.Bytes(),
	}
	defer state.swap(vm)()
	return vm.runProtected(func() error { return vm.driveMainUnit(gen) })
}

// driveMainUnit 驱动主单元生成器直到完成。

func (vm *VM) storeGlobalBinding(name string, val object.Value) error {
	if cur, exists := vm.globals.Get(name); exists {
		if acc, isAcc := cur.(*object.Accessor); isAcc {
			if acc.Setter != nil && object.IsCallable(acc.Setter) {
				object.CallFunction(acc.Setter, vm.globalThisValue(), val)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
				}
			}
			// 无 setter 的访问器: 写入被忽略 (见上注)。
			return nil
		}
		return vm.globals.Set(name, val)
	}
	// 隐式赋值创建的全局: globalThis 的自有可配置属性。
	vm.globals.DeclareImplicit(name, val)
	return nil
}
