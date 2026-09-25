package vm

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/stdlib"
)

// MaxFrames 是调用栈最大深度 (防止无限递归)。
const MaxFrames = 2048

// ThrowError 包装 JS throw 抛出的值，用于在 Go 错误返回链中传递。
type ThrowError struct {
	Value object.Value
}

func (e *ThrowError) Error() string {
	return e.Value.Inspect()
}

// YieldSignal 表示 generator 执行到 yield 时的暂停信号。
// genResume 捕获该信号后保存状态并返回 (value, done=false)。
type YieldSignal struct {
	gen   *object.Generator
	value object.Value
}

func (e *YieldSignal) Error() string {
	return "yield"
}

// tryEntry 是 try-catch-finally 的处理器条目。
type tryEntry struct {
	catchPC   int // catch 块的 PC (0 = 无 catch)
	finallyPC int // finally 块的 PC (0 = 无 finally)
	stackBase int // 进入 try 时的栈高度
	frameIdx  int // 进入 try 时的帧索引
}

// ModuleExports 存储模块的导出。
type ModuleExports struct {
	Default object.Value
	Named   map[string]object.Value
}

// currentVM 是当前正在执行的 VM 实例。
// 用于从 stdlib 回调 JS 闭包时找到正确的 VM。
var currentVM *VM

func init() {
	// 注册回调桥: stdlib → object → vm
	//
	// 错误信号统一走 object 层的 SetCallbackError/TakeCallbackError:
	// 消费即清除。过去 VM 还有一份自己的 callbackErr 字段，只在个别
	// OP_CALL 位点被顺手消费 —— stdlib 层消费错误信号后 VM 层的副本
	// 会永久残留，之后任何一个内建函数调用都会被这个陈旧错误"击落"
	// (表现为回调链莫名中断且无任何报告)。
	object.SetCallFunction(func(fn object.Value, this object.Value, args []object.Value) object.Value {
		if currentVM == nil {
			return object.UndefinedSingleton
		}
		result, err := currentVM.callFunction(fn, this, args)
		if err != nil {
			// 向 object 层暴露错误信号: 返回值无法区分
			// "函数正常返回" 与 "函数抛出了非 Error 异常"。
			object.SetCallbackError(err)
			// 保留原始抛出值 (throw x 的 x), 供 rejection reason 使用
			if te, ok := err.(*ThrowError); ok {
				object.SetCallbackErrorValue(te.Value)
			} else if jt, ok := err.(*jsThrow); ok {
				// 栈溢出等结构化错误: 恢复为对应类型的 Error 对象，
				// 供 stdlib 的 callbackThrown 保留错误类型传播
				object.SetCallbackErrorValue(object.NewErrorWithName(jt.Name, jt.Message))
			}
			return object.UndefinedSingleton
		}
		return result
	})
	// 注册 generator 驱动回调: object.GeneratorNext → vm.genResume
	object.SetGeneratorNext(func(gen *object.Generator, arg object.Value) (object.Value, bool) {
		if currentVM == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := currentVM.genResume(gen, arg)
		if err != nil {
			object.SetCallbackError(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
	// 注册 generator 异常恢复回调: object.GeneratorThrow → vm.genThrow
	object.SetGeneratorThrow(func(gen *object.Generator, throwVal object.Value) (object.Value, bool) {
		if currentVM == nil {
			return object.UndefinedSingleton, true
		}
		val, done, err := currentVM.genThrow(gen, throwVal)
		if err != nil {
			object.SetCallbackError(err)
			return object.UndefinedSingleton, true
		}
		return val, done
	})
}

// VM 是 JavaScript 字节码虚拟机。
type VM struct {
	frames     []*Frame             // 调用栈
	frameIdx   int                  // 当前帧索引 (栈顶)
	stack      *Stack               // 操作数栈
	globals    *runtime.Environment // 全局变量环境
	constants  *bytecode.ConstantPool
	lastPopped object.Value // 最后弹出的值 (用于测试)

	// try-catch-finally 支持
	tryStack     []tryEntry   // try 处理器栈
	pendingThrow object.Value // finally 块中待重新抛出的错误 (nil = 无)

	// 模块系统
	modules        map[string]*ModuleExports // 模块缓存 (按绝对路径)
	moduleBase     string                    // 模块基准路径 (用于解析相对路径)
	currentExports *ModuleExports            // 当前模块的导出对象

	// generator 支持
	currentGenerator *object.Generator // 当前正在执行的 generator (OP_YIELD 时使用)

	// throwBoundary 是当前 runFrom 子执行允许解退到的最低帧索引。
	// 回调桥 (callFunction→runFrom) 的子帧里抛出的异常，不允许直接
	// 解退到外层帧的 try 处理器 —— 否则帧/PC 被改写而 Go 侧的内建
	// 循环 (如 map) 仍在继续执行，错误值会被误当作回调返回值。
	// 边界外的处理器留给错误传播回外层后、由外层的抛出路径匹配。
	throwBoundary int

	// 模板字面量分段收集器 (支持嵌套): 每层对应一个 OP_TEMPLATE_START，
	// 该层内 quasi/表达式产生的字符串依次 append，OP_TEMPLATE_END 时 join 入栈。
	// 不用操作数栈保存段的原因是模板可能作为二元运算的操作数出现——
	// 栈上模板段之外还有外层操作数，无法区分边界。
	tplParts [][]object.Value
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
	}
}

// Globals 返回全局环境。
func (vm *VM) Globals() *runtime.Environment { return vm.globals }

// RunTimers 运行定时器事件循环。
// 阻塞等待下一个定时器到期并执行其回调，直到没有活跃定时器。
// 返回执行期间遇到的错误 (如有)。
func (vm *VM) RunTimers() error {
	return vm.RunTimersUntil(time.Time{})
}

// RunTimersUntil 运行定时器事件循环直到指定时间 (或没有活跃任务)。
// 用于等待定时器回调执行完成。
// 同时驱动 requestIdleCallback: 事件循环空闲 (无到期任务) 时派发空闲回调,
// 空闲回调的 deadline.timeRemaining() 预算为距下一个定时任务的时间 (上限 50ms)。
// 同时消费严格定时器 (setStrictInterval/setStrictTimeout) 的到期信号:
// 在等待普通定时器的空闲期内批量执行严格定时器回调, 保证两者在同一线程串行执行。
func (vm *VM) RunTimersUntil(until time.Time) error {
	return vm.runTimersLoopProtected(until, nil)
}

// RunTimersWithPump 运行事件循环, 空闲等待交给外部事件泵。
//
// pump(maxWait) 应在最多 maxWait 时间内等待并处理外部事件 (如窗口消息):
//   - maxWait > 0: 等待外部事件或超时, 二者先到即返回
//   - maxWait <= 0: 无定时任务时无限期等待外部事件
//   - 返回 false 表示外部事件源已关闭 (如窗口销毁), 事件循环退出
//
// 与 RunTimers 的关键差异: 没有定时器任务时循环不会退出 —— 生命周期由
// pump 决定。GUI 模式用它把消息泵接入定时器调度 (所有回调仍在同一线程
// 串行执行)。
func (vm *VM) RunTimersWithPump(pump func(maxWait time.Duration) bool) error {
	return vm.runTimersLoopProtected(time.Time{}, pump)
}

// runTimersLoopProtected 注册 currentVM 并在 panic 保护下运行事件循环。
// pump 为 nil 时是纯定时器语义 (RunTimersUntil), 非 nil 时由 pump 接管
// 所有空闲等待。
func (vm *VM) runTimersLoopProtected(until time.Time, pump func(maxWait time.Duration) bool) error {
	// 事件循环期间注册 currentVM，保证回调链中的 object.CallFunction 桥
	// (如 Promise resolve 触发的 .then 回调) 能找到正确的 VM 实例。
	// 主脚本执行结束后 currentVM 会被恢复为 nil，若不在此处重新注册，
	// 定时器回调里 resolve 的 Promise 的 then 回调会被静默丢弃。
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()

	return vm.runProtected(func() error { return vm.runTimersLoop(until, pump) })
}

// runProtected 在 defer recover 中执行 fn。
// VM/stdlib 某处缺陷导致的 Go panic 不再直接杀死整个进程，而是转为
// InternalError 结束当前执行。注意: panic 后 VM 状态可能已损坏，
// 恢复出的错误必须向外传播、不能吞掉后继续用同一 VM 跑。
func (vm *VM) runProtected(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("InternalError: VM panic: %v", r)
		}
	}()
	return fn()
}

// runTimersLoop 是事件循环主循环体 (由 runTimersLoopProtected 包裹执行)。
// pump 非 nil 时 (GUI 模式), 所有空闲等待交给 pump 处理外部事件。
func (vm *VM) runTimersLoop(until time.Time, pump func(maxWait time.Duration) bool) error {
	scheduler := object.GlobalScheduler()
	strict := object.GlobalStrictScheduler()
	for {
		// 0) 严格定时器到期信号优先消费 (排队/抢占模式共用此入口)。
		//    有信号时先执行, 保证回调尽早运行。
		if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
			if err := vm.runStrictDispatches(interrupts, until); err != nil {
				return err
			}
			continue
		}

		// 退出时间检查
		if !until.IsZero() {
			now := time.Now()
			if !until.After(now) {
				return nil
			}
		}

		// 1) 已超时的空闲回调: 无论是否空闲都强制派发 (didTimeout=true)
		if expired := scheduler.ExpiredIdleCallbacks(); len(expired) > 0 {
			if err := vm.dispatchIdleCallbacks(expired, true, 0); err != nil {
				return err
			}
			continue
		}

		// 计算下一次唤醒源: 普通定时器/空闲超时 与 严格定时器 取最早。
		wait, found := scheduler.NextFireIn()
		strictWait, strictFound := strict.NextStrictFireIn()
		if strictFound && (!found || strictWait < wait) {
			wait = strictWait
			found = true
		}

		// 2) 空闲期派发: 无任何任务, 或下一个任务前有足够间隙
		if (!found || wait >= object.IdleMinGap) && scheduler.HasIdleCallbacks() {
			if cd := scheduler.IdleDispatchCooldown(); cd > 0 {
				if !until.IsZero() && time.Now().Add(cd).After(until) {
					return nil
				}
				if pump != nil {
					if !pump(cd) {
						return nil
					}
				} else {
					time.Sleep(cd)
				}
				continue
			}
			budget := object.IdleBudget
			if found && wait < budget {
				budget = wait
			}
			cbs := scheduler.TakeIdleCallbacks()
			if err := vm.dispatchIdleCallbacks(cbs, false, budget); err != nil {
				return err
			}
			continue
		}

		if !found {
			// 无普通定时器、无即将到期的严格定时器、无空闲回调。
			// 但派发队列里可能仍有残留信号 (一次性严格定时器触发后已从
			// 调度器移除, NextStrictFireIn 返回 false, 信号却还在队列中),
			// 必须先消费完再退出, 否则回调被静默丢弃。
			if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
				if err := vm.runStrictDispatches(interrupts, until); err != nil {
					return err
				}
				continue
			}
			if pump == nil {
				return nil
			}
			// GUI 模式: 无定时器任务也持续泵外部事件, 直到事件源关闭。
			// maxWait<=0 表示无限期等待外部事件。
			if !pump(0) {
				return nil
			}
			continue
		}

		// 等待到最早唤醒点。
		if pump != nil {
			// GUI 模式: 等待期间由 pump 处理窗口消息; 严格定时器信号在
			// 循环顶部下一轮消费, 不会积压丢失。
			if !pump(wait) {
				return nil
			}
		} else {
			// 等待期间持续检查严格定时器信号,
			// 避免排队模式信号积压 (回调尽量及时, 绝不丢弃)。
			for wait > 0 {
				if interrupts := strict.TakeStrictInterrupts(64); len(interrupts) > 0 {
					if err := vm.runStrictDispatches(interrupts, until); err != nil {
						return err
					}
					break
				}
				sleepFor := wait
				if !until.IsZero() {
					if remain := time.Until(until); remain < sleepFor {
						sleepFor = remain
					}
				}
				if sleepFor <= 0 {
					break
				}
				time.Sleep(sleepFor)
				wait = 0
			}
		}

		// 执行到期的普通定时器
		due := scheduler.DueTimers()
		for _, t := range due {
			if !t.Active || t.Callback == nil {
				continue
			}
			// 超过退出时间则不再执行新回调 (正在执行的回调无法中断)
			if !until.IsZero() && !time.Now().Before(until) {
				scheduler.Reschedule(t)
				continue
			}
			_, err := vm.callFunction(t.Callback, nil, nil)
			if err != nil {
				scheduler.Reschedule(t)
				return err
			}
			scheduler.Reschedule(t)
		}
	}
}

// runStrictDispatches 执行一批严格定时器到期信号。
// 为每个信号构造 info 参数对象 (scheduledTime/dueTime/early/late/skipped),
// 并在当前 VM 上下文调用其回调。队列模式与抢占模式共用此执行路径。
// until 非零时, 超过该时间不再启动新的回调 (单个正在执行的回调无法中断)。
func (vm *VM) runStrictDispatches(dispatches []object.StrictDispatch, until time.Time) error {
	for _, d := range dispatches {
		t := d.Ticker
		if t == nil || !t.Active.Load() || t.Callback == nil {
			continue
		}
		if !until.IsZero() && !time.Now().Before(until) {
			return nil
		}
		info := newStrictInfo(d.ScheduledTime, d.DueTime)
		if _, err := vm.callFunction(t.Callback, nil, []object.Value{info}); err != nil {
			return err
		}
		// 一次性定时器执行后失效
		if !t.Repeat {
			t.Active.Store(false)
		}
	}
	return nil
}

// newStrictInfo 构造严格定时器回调的 info 参数对象。
func newStrictInfo(scheduled, due time.Time) *object.Object {
	info := object.NewObject()
	info.SetProperty("scheduledTime", object.NewNumber(float64(scheduled.UnixMilli())))
	info.SetProperty("dueTime", object.NewNumber(float64(due.UnixMilli())))
	early := scheduled.Sub(due)
	if early < 0 {
		early = 0
	}
	late := due.Sub(scheduled)
	if late < 0 {
		late = 0
	}
	info.SetProperty("early", object.NewNumber(float64(early)/float64(time.Millisecond)))
	info.SetProperty("late", object.NewNumber(float64(late)/float64(time.Millisecond)))
	info.SetProperty("skipped", object.NewNumber(0))
	return info
}

// dispatchIdleCallbacks 派发空闲回调, 为每个回调构造 deadline 参数对象。
// didTimeout=true 表示因 options.timeout 到期被迫派发 (此时预算为 0)。
func (vm *VM) dispatchIdleCallbacks(cbs []*object.IdleCallback, didTimeout bool, budget time.Duration) error {
	for _, cb := range cbs {
		if !cb.Active || cb.Callback == nil {
			continue
		}
		deadline := newIdleDeadline(budget, didTimeout)
		if _, err := vm.callFunction(cb.Callback, nil, []object.Value{deadline}); err != nil {
			return err
		}
	}
	return nil
}

// newIdleDeadline 构造 requestIdleCallback 的 deadline 参数对象:
//   - didTimeout: 是否因超时被迫派发
//   - timeRemaining(): 剩余时间预算 (毫秒), 随时间递减, 最小为 0
func newIdleDeadline(budget time.Duration, didTimeout bool) *object.Object {
	start := time.Now()
	d := object.NewObject()
	d.SetProperty("didTimeout", object.NewBoolean(didTimeout))
	d.SetProperty("timeRemaining", object.NewBuiltin("timeRemaining", func(args ...object.Value) object.Value {
		rem := budget - time.Since(start)
		if rem < 0 {
			rem = 0
		}
		return object.NewNumber(float64(rem) / float64(time.Millisecond))
	}))
	return d
}

// LastPopped 返回最后从栈弹出的值。
func (vm *VM) LastPopped() object.Value { return vm.lastPopped }

// currentFrame 返回当前执行帧。
func (vm *VM) currentFrame() *Frame { return vm.frames[vm.frameIdx] }

// pushFrame 压入新帧。
func (vm *VM) pushFrame(f *Frame) {
	vm.frameIdx++
	vm.frames[vm.frameIdx] = f
}

// popFrame 弹出当前帧。
// 传播闭包变量修改到上一帧: 仅当闭包创建于上一帧时传播修改过的 slot。
func (vm *VM) popFrame() *Frame {
	f := vm.frames[vm.frameIdx]
	vm.frames[vm.frameIdx] = nil
	vm.frameIdx--

	// 传播闭包变量修改 (closure → outer frame)
	// 仅当闭包创建于上一帧时才传播，避免跨帧变量错位
	if f.Closure != nil && f.ModifiedSlots != nil && len(f.ModifiedSlots) > 0 && vm.frameIdx >= 0 {
		if f.Closure.CreatedAtFrame == vm.frameIdx {
			outerFrame := vm.frames[vm.frameIdx]
			if outerFrame != nil {
				for slot := range f.ModifiedSlots {
					if slot < len(f.Closure.CapturedLocals) && slot < len(outerFrame.Locals) {
						val := f.Closure.CapturedLocals[slot]
						outerFrame.Locals[slot] = val
						if outerFrame.Closure != nil && slot < len(outerFrame.Closure.CapturedLocals) {
							outerFrame.Closure.CapturedLocals[slot] = val
						}
					}
				}
			}
		}
	}

	return f
}

// Run 启动 VM 执行循环。
func (vm *VM) Run() error {
	return vm.execute()
}

// RunCompiled 从编译器输出启动执行。
func (vm *VM) RunCompiled(c *compiler.Compiler) error {
	vm.frames[0] = NewFrame(c.Bytes(), c.Constants(), c.NumLocals())
	vm.frameIdx = 0
	return vm.execute()
}

// execute 设置当前 VM 并从主帧开始执行。
func (vm *VM) execute() error {
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()
	return vm.runProtected(func() error { return vm.runFrom(0) })
}

// runFrom 从指定帧索引开始执行指令循环。
// startFrameIdx: 起始帧索引，循环持续到 vm.frameIdx < startFrameIdx。
// 用于主程序执行 (startFrameIdx=0) 和回调子帧执行。
func (vm *VM) runFrom(startFrameIdx int) error {
	savedBoundary := vm.throwBoundary
	vm.throwBoundary = startFrameIdx
	defer func() { vm.throwBoundary = savedBoundary }()
	for vm.frameIdx >= startFrameIdx {
		frame := vm.currentFrame()

		// 检查是否到达字节码末尾
		if frame.PC >= len(frame.Instructions) {
			if vm.frameIdx == 0 {
				// 主程序结束: 返回栈顶值 (如果有)
				if vm.stack.Len() > 0 {
					vm.lastPopped = vm.stack.Pop()
				}
				return nil
			}
			// 函数没有显式 return (应该有 OP_RETURN_VOID)
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
			continue
		}

		// 取指
		op := bytecode.ReadOpcode(frame.Instructions, frame.PC)
		operand := bytecode.ReadOperand(frame.Instructions, frame.PC+1)
		frame.PC += bytecode.InstructionSize

		// 解码-执行
		switch op {
		// ===== 栈操作 =====
		case bytecode.OP_NOP:
			// 空操作
		case bytecode.OP_POP:
			vm.lastPopped = vm.stack.Pop()
		case bytecode.OP_DUP:
			vm.stack.Push(vm.stack.Peek())
		case bytecode.OP_SWAP:
			a := vm.stack.Pop()
			b := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_ITER_BOUNDARY:
			// 迭代边界: 换一组 binding cell，本轮迭代创建的闭包就此"定版"。
			//
			// 闭包共享创建帧的 Locals 数组 (而非快照)，所以每一次 STORE 都会被
			// 前几轮创建的闭包看到。这里把 Locals 换成当前值的一份拷贝:
			//   - 已创建的闭包仍引用旧数组 → 保留本轮迭代的值；
			//   - 后续迭代写入新数组 → 不再回溯影响它们。
			// 即 ECMAScript 的 per-iteration binding，在本 VM 的 cell 模型下的等价实现。
			old := frame.Locals
			fresh := make([]object.Value, len(old))
			copy(fresh, old)
			frame.Locals = fresh
			// 同时切断 OP_STORE 的"向本帧创建的子闭包传播"这条更老的路径:
			// 它会直接写 c.CapturedLocals[slot]，而那正是旧数组，等同于把后续
			// 迭代的值写回已经定版的闭包。
			frame.CreatedClosures = nil
		case bytecode.OP_DUP_BELOW2:
			// [a, b, c] → [c, a, b, c]: 栈顶值复制一份并插到下方两个值之下
			cVal := vm.stack.Pop()
			bVal := vm.stack.Pop()
			aVal := vm.stack.Pop()
			vm.stack.Push(cVal)
			vm.stack.Push(aVal)
			vm.stack.Push(bVal)
			vm.stack.Push(cVal)
		case bytecode.OP_DUP2:
			// 复制栈顶两个值并保持顺序: [a, b] → [a, b, a, b]。
			// 成员复合赋值 (obj.k += v) 需要它: obj/key 各留一份供 SET_INDEX，
			// 同时顶部保留一份供 GET_INDEX 取旧值。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(a)
			vm.stack.Push(b)
			vm.stack.Push(a)
			vm.stack.Push(b)
		case bytecode.OP_POP_N:
			n := int(operand)
			for i := 0; i < n; i++ {
				vm.stack.Pop()
			}

		// ===== 常量加载 =====
		case bytecode.OP_CONST:
			vm.stack.Push(frame.Constants.Get(operand))
		case bytecode.OP_NULL:
			vm.stack.Push(object.NullSingleton)
		case bytecode.OP_UNDEFINED:
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_TRUE:
			vm.stack.Push(object.NewBoolean(true))
		case bytecode.OP_FALSE:
			vm.stack.Push(object.NewBoolean(false))
		case bytecode.OP_INT:
			// operand 直接作为 int16 值
			val := int16(operand)
			vm.stack.Push(object.NewNumber(float64(val)))

		// ===== 变量操作 =====
		case bytecode.OP_LOAD:
			slot := int(operand)
			if slot >= len(frame.Locals) {
				return fmt.Errorf("VM: LOAD slot %d out of range (locals: %d)", slot, len(frame.Locals))
			}
			val := frame.Locals[slot]
			if val == nil {
				// TDZ: let/const 绑定在执行到声明语句前不可访问。
				// 与 ECMAScript 一致抛 ReferenceError，而非返回 undefined。
				if terr := vm.throwJSError(&jsThrow{
					"ReferenceError",
					"Cannot access lexical declaration before initialization",
				}); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(val)
		case bytecode.OP_STORE:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell (兄弟闭包与后续调用据此观察到新值)
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量 (closure → 同一闭包下次调用)
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向本帧创建的子闭包传播外层变量修改 (outer → closure)
			for _, c := range frame.CreatedClosures {
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记 slot 为已修改 (用于 popFrame 时向上一帧传播)
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_STORE_CONST:
			slot := int(operand)
			val := vm.stack.Pop()
			if slot >= len(frame.Locals) {
				for len(frame.Locals) <= slot {
					frame.Locals = append(frame.Locals, object.UndefinedSingleton)
				}
			}
			frame.Locals[slot] = val
			// 0. 写回共享 binding cell
			if slot < len(frame.SharedCells) {
				frame.SharedCells[slot] = val
			}
			// 1. 更新当前帧闭包的捕获变量
			if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
				frame.Closure.CapturedLocals[slot] = val
			}
			// 2. 向子闭包传播
			for _, c := range frame.CreatedClosures {
				if slot < len(c.CapturedLocals) {
					c.CapturedLocals[slot] = val
				}
			}
			// 3. 标记为已修改
			if frame.ModifiedSlots == nil {
				frame.ModifiedSlots = make(map[int]bool)
			}
			frame.ModifiedSlots[slot] = true
		case bytecode.OP_LOAD_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val, found := vm.globals.Get(s.Value)
				if !found {
					if err := vm.throwNamedError("ReferenceError", "%s is not defined", s.Value); err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(val)
			}
		case bytecode.OP_STORE_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				// 弹出值 (与 OP_STORE 语义一致)。若需保留表达式结果, 编译器会在存储前 DUP。
				val := vm.stack.Pop()
				if vm.globals.IsConst(s.Value) {
					if err := vm.throwNamedError("TypeError", "Assignment to constant variable: %s", s.Value); err != nil {
						return err
					}
					continue
				}
				if _, exists := vm.globals.Get(s.Value); exists {
					vm.globals.Set(s.Value, val)
				} else {
					vm.globals.Declare(s.Value, val, false)
				}
			}
		case bytecode.OP_DECLARE:
			// 顶层 let/class/import 声明: 弹出值写入全局环境。
			// 与持久化的全局词法绑定冲突时报 SyntaxError (不可被 try/catch 捕获，
			// 与规范的早期错误语义一致)。REPL 每行独立编译，跨行重声明在此发现。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_CONST:
			// 顶层 const 声明: 弹出值写入全局环境 (const 词法绑定)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, true, false); err != nil {
					return err
				}
			}
		case bytecode.OP_DECLARE_FUNC:
			// 顶层函数声明: 允许函数互相重定义，但不可覆盖已有词法声明。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if err := vm.globals.DeclareGlobal(s.Value, val, false, true); err != nil {
					return err
				}
			}

		// ===== 算术运算 =====
		case bytecode.OP_ADD:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.addValues(a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_SUB, bytecode.OP_MUL, bytecode.OP_DIV, bytecode.OP_MOD,
			bytecode.OP_POW, bytecode.OP_BIT_AND, bytecode.OP_BIT_OR, bytecode.OP_BIT_XOR,
			bytecode.OP_SHL, bytecode.OP_SHR, bytecode.OP_USHR:
			// BigInt 参与时走独立分支: JS 禁止 BigInt 与 Number 隐式混合运算。
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.binaryArithmetic(op, a, b)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_NEG, bytecode.OP_BIT_NOT:
			a := vm.stack.Pop()
			result, err := vm.unaryArithmetic(op, a)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)

		// ===== 比较和逻辑 =====
		case bytecode.OP_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(looseEquals(a, b)))
		case bytecode.OP_NOT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!looseEquals(a, b)))
		case bytecode.OP_STRICT_EQ:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(strictEquals(a, b)))
		case bytecode.OP_STRICT_NE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(!strictEquals(a, b)))
		case bytecode.OP_LT, bytecode.OP_GT, bytecode.OP_LTE, bytecode.OP_GTE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			res, err := relationalCompare(op, a, b)
			if err != nil {
				return err
			}
			vm.stack.Push(object.NewBoolean(res))
		case bytecode.OP_NOT:
			a := vm.stack.Pop()
			vm.stack.Push(object.NewBoolean(object.IsFalsy(a)))
		case bytecode.OP_AND, bytecode.OP_OR:
			// 短路逻辑由编译器处理，这里不应该直接执行
			// 但如果出现，按二元操作处理
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if op == bytecode.OP_AND {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() && b.IsTruthy()))
			} else {
				vm.stack.Push(object.NewBoolean(a.IsTruthy() || b.IsTruthy()))
			}
		case bytecode.OP_NULL_COALESCE:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			if _, isNull := a.(*object.Null); isNull {
				vm.stack.Push(b)
			} else if _, isUndef := a.(*object.Undefined); isUndef {
				vm.stack.Push(b)
			} else {
				vm.stack.Push(a)
			}

		// ===== 跳转 =====
		case bytecode.OP_JUMP:
			frame.PC = int(operand)
		case bytecode.OP_JUMP_IF_TRUE:
			if vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_FALSE:
			if !vm.stack.Peek().IsTruthy() {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NULL:
			val := vm.stack.Peek()
			if _, isNull := val.(*object.Null); isNull {
				frame.PC = int(operand)
			} else if _, isUndef := val.(*object.Undefined); isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_JUMP_IF_NOT_NULL:
			val := vm.stack.Peek()
			_, isNull := val.(*object.Null)
			_, isUndef := val.(*object.Undefined)
			if !isNull && !isUndef {
				frame.PC = int(operand)
			}
		case bytecode.OP_LOOP:
			frame.PC = int(operand)

		// ===== 函数 =====
		case bytecode.OP_FUNCTION, bytecode.OP_ARROW_FUNC:
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d, got %T", operand, fnMeta)
			}
			// 创建 Closure
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CLOSURE:
			// 同 OP_FUNCTION
			fnMeta := frame.Constants.Get(operand)
			meta, ok := fnMeta.(*bytecode.FunctionMetadata)
			if !ok {
				return fmt.Errorf("VM: expected FunctionMetadata at constant %d", operand)
			}
			closure := vm.createClosure(meta, frame)
			vm.trackClosure(closure)
			vm.stack.Push(closure)
		case bytecode.OP_CALL:
			numArgs := int(operand)
			// 弹出函数
			fn := vm.stack.Pop()
			// 收集参数 (栈上是 arg1, arg2, ..., argN, 逆序弹出)
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}

			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				// 返回 *object.Error 的内建函数: 作为异常抛出 (Error 构造器除外)
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}

			case *object.Closure:
				// generator 函数调用: 不执行函数体, 返回 Generator 对象
				if callee.Fn != nil && callee.Fn.IsGenerator {
					vm.stack.Push(object.NewGenerator(callee, args))
				} else {
					if err := vm.callClosure(callee, args); err != nil {
						if terr := vm.throwJSError(err); terr != nil {
							return terr
						}
						continue
					}
				}

			case *object.Proxy:
				// 代理: 转发到 apply trap (this 为 undefined)
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)

			case object.ObservableState:
				// Rx 单元直接调用: count() 等价 count.value (GetX 语义)
				vm.stack.Push(callee.RxValue())

			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		case bytecode.OP_RETURN:
			val := vm.stack.Pop()
			base := frame.StackBase
			vm.popFrame()
			// 截断本帧残留的栈值, 防止污染调用方栈
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(val)
		case bytecode.OP_RETURN_VOID:
			base := frame.StackBase
			vm.popFrame()
			if vm.stack.Len() > base {
				vm.stack.Truncate(base)
			}
			vm.stack.Push(object.UndefinedSingleton)
		case bytecode.OP_YIELD:
			// generator 的 yield: 弹出表达式值, 保存帧状态, 暂停执行。
			// 恢复时 (genResume) 压入传入的 arg 作为 yield 表达式的值。
			val := vm.stack.Pop()
			gen := vm.currentGenerator
			if gen == nil {
				return fmt.Errorf("TypeError: yield outside generator")
			}
			curFrame := vm.currentFrame()
			gen.PC = curFrame.PC // 恢复点: yield 之后的下一条指令
			gen.Locals = curFrame.Locals
			gen.Constants = curFrame.Constants.Constants
			gen.Instructions = curFrame.Instructions
			// 保存属于 generator 帧的 try 处理器条目。挂起期间不能留在全局
			// tryStack 上: 恢复时帧深度可能不同，且无关代码抛出的异常绝不能
			// 被挂起中的 generator 捕获。栈基址/帧索引保存相对值。
			gen.PendingTries = nil
			for len(vm.tryStack) > 0 && vm.tryStack[len(vm.tryStack)-1].frameIdx >= vm.frameIdx {
				te := vm.tryStack[len(vm.tryStack)-1]
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
				gen.PendingTries = append([]object.GenTryEntry{{
					CatchPC:      te.catchPC,
					FinallyPC:    te.finallyPC,
					RelStackBase: te.stackBase - curFrame.StackBase,
					RelFrameIdx:  te.frameIdx - vm.frameIdx,
				}}, gen.PendingTries...)
			}
			// 保存帧栈残留的中间值 (如 2 + (yield 3) 中的 2)
			if vm.stack.Len() > curFrame.StackBase {
				n := vm.stack.Len() - curFrame.StackBase
				gen.SavedStack = make([]object.Value, n)
				for i := 0; i < n; i++ {
					gen.SavedStack[i] = vm.stack.PeekAt(n - 1 - i)
				}
				vm.stack.Truncate(curFrame.StackBase)
			} else {
				gen.SavedStack = nil
			}
			vm.popFrame()
			return &YieldSignal{gen: gen, value: val}
		case bytecode.OP_CALL_SPREAD:
			// 参数在数组中，栈: [args_array, func]
			fn := vm.stack.Pop()
			arr := vm.stack.Pop()
			var args []object.Value
			if a, ok := arr.(*object.Array); ok {
				args = a.Elements
			}
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Proxy:
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_CALL_METHOD:
			// 方法调用: 栈 [fn, this, arg1, ..., argN]
			numArgs := int(operand)
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			thisVal := vm.stack.Pop()
			fn := vm.stack.Pop()

			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				// 内建函数: 不传 this，直接传参数
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.BuiltinMethod:
				// 内建方法: this 作为第一个参数传递
				result := callee.Fn(thisVal, args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				if thrown, err := vm.throwIfError(result, false); thrown {
					if err != nil {
						return err
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			case *object.Closure:
				// 创建绑定了 this 的新闭包
				methodClosure := &object.Closure{
					Fn:             callee.Fn,
					Env:            callee.Env,
					This:           thisVal,
					IsArrow:        callee.IsArrow,
					CapturedLocals: callee.CapturedLocals,
				}
				// generator 方法调用: 创建 Generator (this 绑定保留在闭包中)
				if methodClosure.Fn != nil && methodClosure.Fn.IsGenerator {
					vm.stack.Push(object.NewGenerator(methodClosure, args))
				} else {
					if err := vm.callClosure(methodClosure, args); err != nil {
						if terr := vm.throwJSError(err); terr != nil {
							return terr
						}
						continue
					}
				}
			case *object.Proxy:
				// 代理方法调用: 转发到 apply trap，this 为 thisVal
				result, err := vm.proxyApply(callee, thisVal, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_NEW:
			// new Constructor(args...) — 简化实现
			numArgs := int(operand)
			fn := vm.stack.Pop()
			args := make([]object.Value, numArgs)
			for i := numArgs - 1; i >= 0; i-- {
				args[i] = vm.stack.Pop()
			}
			// 创建新对象
			newObj := object.NewObject()
			if closure, ok := fn.(*object.Closure); ok {
				// generator 不能 new (简化: 当作普通调用创建 generator)
				if closure.Fn != nil && closure.Fn.IsGenerator {
					vm.stack.Push(object.NewGenerator(closure, args))
					continue
				}
				// 设置新对象的原型为构造函数的 prototype (支持 instanceof)
				if !closure.IsArrow {
					if cp, _ := closure.GetProperty("prototype"); cp != nil {
						if protoObj, ok := cp.(*object.Object); ok {
							newObj.Proto = protoObj
						} else {
							newObj.Proto = cp
						}
					}
				}
				// 设置 this 为新对象
				newClosure := &object.Closure{
					Fn:      closure.Fn,
					Env:     closure.Env,
					This:    newObj,
					IsArrow: closure.IsArrow,
				}
				// 调用构造函数 (同步执行到返回)
				startIdx := vm.frameIdx + 1
				if err := vm.callClosure(newClosure, args); err != nil {
					return err
				}
				if err := vm.runFrom(startIdx); err != nil {
					return err
				}
				// 如果构造函数返回对象，使用返回的对象
				result := vm.stack.Pop()
				if _, isObj := result.(*object.Object); isObj {
					// 使用构造函数返回的对象
					vm.stack.Push(result)
				} else {
					// 使用 newObj
					vm.stack.Push(newObj)
				}
			} else if builtin, ok := fn.(*object.BuiltinFunction); ok {
				result := builtin.Fn(args...)
				if result == nil {
					result = newObj
				}
				// 内建构造器返回 Error 对象时抛出异常 (如 new RegExp("[") → SyntaxError)
				// ReturnIsValue 的构造器 (如 Error) 返回的 Error 是值，不抛出
				if errObj, isErr := result.(*object.Error); isErr && !builtin.ReturnIsValue {
					if !vm.handleThrow(errObj) {
						return &ThrowError{Value: errObj}
					}
					continue
				}
				vm.stack.Push(result)
				if err := vm.checkCallbackErr(); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
			} else if proxy, ok := fn.(*object.Proxy); ok {
				// 代理构造: 转发到 construct trap
				result, err := vm.proxyConstruct(proxy, args)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(result)
			} else {
				if err := vm.throwNamedError("TypeError", "%s is not a constructor", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}

		// ===== 对象和数组 =====
		case bytecode.OP_NEW_ARRAY:
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				elements[i] = vm.stack.Pop()
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_NEW_OBJECT:
			vm.stack.Push(object.NewObject())
		case bytecode.OP_SET_PROTO:
			// 栈: [obj, parent] → obj.Proto = parent
			parent := vm.stack.Pop()
			obj := vm.stack.Peek()
			if o, ok := obj.(*object.Object); ok {
				o.Proto = parent
			}
		case bytecode.OP_GET_PROP:
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			obj := vm.stack.Pop()
			// null/undefined 属性读取抛 TypeError (规范要求；
			// 静默返回 undefined 会掩盖程序错误)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 get trap
			if proxy, ok := obj.(*object.Proxy); ok {
				val, err := vm.proxyGet(proxy, propName, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			val, found := obj.GetProperty(propName)
			// getter 可能经回调桥执行用户代码并抛出异常: 立即消费
			// 挂起的错误信号并走抛出流程，避免残留到之后的内建调用点
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
			if !found {
				vm.stack.Push(object.UndefinedSingleton)
			} else {
				vm.stack.Push(val)
			}
		case bytecode.OP_SET_PROP:
			val := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			// 属性名在常量池中
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			// null/undefined 属性写入抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), propName); err != nil {
					return err
				}
				continue
			}
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, propName, val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				continue
			}
			obj.SetProperty(propName, val)
			// setter 可能经回调桥执行用户代码并抛出异常: 立即消费
			if err := vm.checkCallbackErr(); err != nil {
				if terr := vm.rethrowBridgeError(err); terr != nil {
					return terr
				}
				continue
			}
		case bytecode.OP_SET_GETTER, bytecode.OP_SET_SETTER:
			// 栈: [obj, fn]; 设置 getter/setter 属性
			fn := vm.stack.Pop()
			obj := vm.stack.Peek() // 保留对象在栈上
			propNameVal := frame.Constants.Get(operand)
			propName := ""
			if s, ok := propNameVal.(*object.String); ok {
				propName = s.Value
			}
			if o, ok := obj.(*object.Object); ok {
				if op == bytecode.OP_SET_GETTER {
					o.DefineAccessor(propName, fn, nil)
				} else {
					o.DefineAccessor(propName, nil, fn)
				}
			}
		case bytecode.OP_TAGGED_TEMPLATE:
			// 栈: [tag, expr1, expr2, ...]; 先弹出插值表达式, 再弹出 tag
			stringsVal := frame.Constants.Get(operand)
			quasisArr, ok := stringsVal.(*object.Array)
			if !ok {
				return fmt.Errorf("VM: expected strings array constant for tagged template")
			}
			// 运行时重建 strings 数组: 编译期数组的原型 (ArrayProto) 尚未初始化,
			// 此处用 object.NewArray 使原型绑定到 ArrayProto (数组方法可用)
			stringsArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			rawArr := object.NewArray(append([]object.Value{}, quasisArr.Elements...))
			stringsArr.SetProperty("raw", rawArr)

			numExprs := len(stringsArr.Elements) - 1
			args := make([]object.Value, 0, numExprs+1)
			args = append(args, stringsArr)
			// 栈上插值顺序为 [e1, e2, ...]，而弹栈是逆序 (先 eN)。
			// 直接 append 会得到 [eN, ..., e1]，插值顺序整体反转。
			vals := make([]object.Value, numExprs)
			for i := numExprs - 1; i >= 0; i-- {
				vals[i] = vm.stack.Pop()
			}
			args = append(args, vals...)
			fn := vm.stack.Pop()
			// 调用 tag 函数
			switch callee := fn.(type) {
			case *object.BuiltinFunction:
				result := callee.Fn(args...)
				if result == nil {
					result = object.UndefinedSingleton
				}
				vm.stack.Push(result)
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					if terr := vm.throwJSError(err); terr != nil {
						return terr
					}
					continue
				}
			default:
				if err := vm.throwNamedError("TypeError", "%s is not a function", describeCallee(fn)); err != nil {
					return err
				}
				continue
			}
		case bytecode.OP_DYNAMIC_IMPORT:
			// 动态 import(): 弹出模块路径, 加载模块, 包装为 resolved Promise
			specVal := vm.stack.Pop()
			spec := toJSString(specVal)
			modExports, err := vm.loadModule(spec)
			p := object.NewPromise()
			if err != nil {
				// 加载失败: reject Promise
				p.Reject(object.NewErrorWithName("Error", err.Error()))
			} else {
				modObj := object.NewObject()
				if modExports.Default != nil {
					modObj.SetProperty("default", modExports.Default)
				}
				for name, val := range modExports.Named {
					modObj.SetProperty(name, val)
				}
				p.Resolve(modObj)
			}
			vm.stack.Push(p)
		case bytecode.OP_GET_INDEX:
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 get trap (键转为字符串)
			if proxy, ok := obj.(*object.Proxy); ok {
				key := toJSString(index)
				val, err := vm.proxyGet(proxy, key, obj)
				if err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引读取抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot read properties of %s (reading '%s')", obj.Inspect(), toJSString(index)); err != nil {
					return err
				}
				continue
			}
			val := vm.getIndex(obj, index)
			vm.stack.Push(val)
		case bytecode.OP_SET_INDEX:
			val := vm.stack.Pop()
			index := vm.stack.Pop()
			obj := vm.stack.Pop()
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, toJSString(index), val, obj); err != nil {
					if terr := vm.rethrowBridgeError(err); terr != nil {
						return terr
					}
					continue
				}
				vm.stack.Push(val)
				continue
			}
			// null/undefined 索引写入抛 TypeError (规范要求)
			if obj == object.NullSingleton || obj == object.UndefinedSingleton {
				if err := vm.throwNamedError("TypeError",
					"Cannot set properties of %s (setting '%s')", obj.Inspect(), toJSString(index)); err != nil {
					return err
				}
				continue
			}
			vm.setIndex(obj, index, val)
			vm.stack.Push(val)
		case bytecode.OP_ARRAY_PUSH:
			// 弹出值，追加到栈顶下方的数组
			val := vm.stack.Pop()
			arr := vm.stack.Peek()
			if a, ok := arr.(*object.Array); ok {
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SPREAD:
			// 弹出可迭代对象，展开所有元素追加到栈顶下方的数组
			iterable := vm.stack.Pop()
			arr := vm.stack.Peek()
			a, ok := arr.(*object.Array)
			if !ok {
				continue
			}
			// generator 只能在 VM 里推进 (每次 next 都要恢复它的字节码帧)，
			// runtime.GetIterable 是纯 Go 层，覆盖不到它 —— 单独处理。
			if gen, ok := iterable.(*object.Generator); ok {
				// 栈布局与 OP_ITER_NEXT 保持一致: 把 generator 放回栈顶再驱动
				vm.stack.Push(gen)
				for {
					val, done, err := vm.genResume(gen, object.UndefinedSingleton)
					if err != nil {
						return err
					}
					if done {
						break
					}
					a.Elements = append(a.Elements, val)
				}
				vm.stack.Pop() // 弹出 generator，留下数组
				continue
			}
			iter, hasIter := runtime.GetIterable(iterable)
			if !hasIter {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", iterable.Inspect()); err != nil {
					return err
				}
				continue
			}
			for {
				val, done := iter.Next()
				if done {
					break
				}
				a.Elements = append(a.Elements, val)
			}
		case bytecode.OP_ARRAY_SLICE:
			// 栈: [arr, start] → 弹出 start, arr, 推入 arr[start:] 新数组 (解构 rest)
			start := int(toNumber(vm.stack.Pop()))
			src := vm.stack.Pop()
			a, ok := src.(*object.Array)
			if !ok {
				vm.stack.Push(object.NewArray(nil))
				continue
			}
			if start < 0 {
				start = 0
			}
			if start > len(a.Elements) {
				start = len(a.Elements)
			}
			rest := make([]object.Value, len(a.Elements)-start)
			copy(rest, a.Elements[start:])
			vm.stack.Push(object.NewArray(rest))
		case bytecode.OP_OBJECT_SPREAD:
			// 弹出源对象, 复制其自有属性到栈顶下方的目标对象
			src := vm.stack.Pop()
			dst := vm.stack.Peek()
			to, ok := dst.(*object.Object)
			if !ok {
				continue
			}
			if so, ok := src.(*object.Object); ok {
				for k, desc := range so.Properties {
					to.SetProperty(k, desc.Value)
				}
			} else if arr, ok := src.(*object.Array); ok {
				for i, v := range arr.Elements {
					to.SetProperty(fmt.Sprintf("%d", i), v)
				}
			}

		// ===== 模板字面量 =====
		case bytecode.OP_TEMPLATE_START:
			// 打开一层分段收集器 (operand = 总部分数，实际用不上:
			// 各段值已按 quasi → PART / 表达式 → PART 的顺序各自入栈后被消费)
			vm.tplParts = append(vm.tplParts, nil)
		case bytecode.OP_TEMPLATE_PART:
			// 栈顶一定是当前模板的某一段 (quasi 常量或表达式结果)，
			// 转为字符串后只进收集器，不再碰栈 —— 这是与旧实现的本质区别:
			// 旧代码向下 peek 并弹走"看起来像字符串"的值，会把模板外的
			// 操作数 (如二元加法的左操作数) 误并进模板，最终造成栈下溢。
			val := vm.stack.Pop()
			str := toJSString(val)
			depth := len(vm.tplParts)
			if depth == 0 {
				// 防御: 字节码不完整时退化为直接把段值压栈
				vm.stack.Push(object.NewString(str))
				continue
			}
			vm.tplParts[depth-1] = append(vm.tplParts[depth-1], object.NewString(str))
		case bytecode.OP_TEMPLATE_END:
			// 拼接本层所有段，压回操作数栈
			depth := len(vm.tplParts)
			if depth == 0 {
				continue
			}
			parts := vm.tplParts[depth-1]
			vm.tplParts = vm.tplParts[:depth-1]
			total := 0
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					total += len(s.Value)
				}
			}
			if total > maxStringLength {
				if terr := vm.throwJSError(&jsThrow{
					Name: "RangeError", Message: "Invalid string length"}); terr != nil {
					return terr
				}
				continue
			}
			var sb strings.Builder
			for _, p := range parts {
				if s, ok := p.(*object.String); ok {
					sb.WriteString(s.Value)
				}
			}
			vm.stack.Push(object.NewString(sb.String()))

		// ===== 解构和展开 =====
		case bytecode.OP_DESTRUCTURE:
			// 简化实现: 后续完善
		case bytecode.OP_SPREAD:
			// 简化实现: 后续完善
		case bytecode.OP_PACK_ARRAY:
			// 收集剩余参数到数组
			n := int(operand)
			elements := make([]object.Value, n)
			for i := n - 1; i >= 0; i-- {
				elements[i] = vm.stack.Pop()
			}
			vm.stack.Push(object.NewArray(elements))
		case bytecode.OP_PACK_OBJECT:
			// 简化实现

		// ===== 迭代器 =====
		case bytecode.OP_GET_ITERATOR:
			val := vm.stack.Pop()
			// generator 对象本身可作为迭代器 (由 OP_ITER_NEXT 驱动)
			if _, ok := val.(*object.Generator); ok {
				vm.stack.Push(val)
				continue
			}
			iter, ok := runtime.GetIterable(val)
			if !ok {
				if err := vm.throwNamedError("TypeError", "%s is not iterable", val.Inspect()); err != nil {
					return err
				}
				continue
			}
			vm.stack.Push(iter)
		case bytecode.OP_ITER_NEXT:
			// 不弹出迭代器，只读取栈顶的迭代器并获取下一个值
			iter := vm.stack.Peek()
			// generator: 通过 VM 驱动前进一步
			if gen, ok := iter.(*object.Generator); ok {
				val, done, err := vm.genResume(gen, object.UndefinedSingleton)
				if err != nil {
					return err
				}
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
				continue
			}
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_FOR_IN_INIT:
			// 弹出对象, 推入对象键迭代器
			val := vm.stack.Pop()
			switch v := val.(type) {
			case *object.Object:
				vm.stack.Push(runtime.NewObjectKeysIterator(v))
			case *object.Array:
				// for...in 遍历数组的索引键 ("0", "1", ...)
				keys := make([]string, len(v.Elements))
				for i := range v.Elements {
					keys[i] = strconv.Itoa(i)
				}
				iter := runtime.NewObjectKeysIteratorWithKeys(v, keys)
				vm.stack.Push(iter)
			default:
				vm.stack.Push(runtime.NewObjectKeysIterator(object.NewObject()))
			}
		case bytecode.OP_FOR_IN_NEXT:
			// 读取栈顶迭代器, 取下一个键
			iter := vm.stack.Peek()
			if it, ok := iter.(*runtime.Iterator); ok {
				val, done := it.Next()
				if done {
					vm.stack.Push(object.UndefinedSingleton)
				} else {
					vm.stack.Push(val)
				}
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_FOR_IN_END:
			// 弹出迭代器 (清理栈)
			vm.stack.Pop()

		// ===== 作用域 =====
		case bytecode.OP_PUSH_SCOPE, bytecode.OP_POP_SCOPE:
			// 局部变量使用 slot 管理，作用域操作在 VM 中是 NOP

		// ===== 类型操作 =====
		case bytecode.OP_TO_NUMBER:
			val := vm.stack.Pop()
			result, err := toNumberValue(val)
			if err != nil {
				if terr := vm.throwJSError(err); terr != nil {
					return terr
				}
				continue
			}
			vm.stack.Push(result)
		case bytecode.OP_TYPEOF:
			val := vm.stack.Pop()
			vm.stack.Push(object.NewString(object.TypeOf(val)))
		case bytecode.OP_TYPEOF_GLOBAL:
			// typeof 作用于编译期未绑定的标识符。
			// 该标识符可能是运行时注入的全局对象 (Math/Number/Array/...)，
			// 也可能是真正的未声明变量 —— 后者按规范返回 "undefined" 而非抛错，
			// 因此这里走非抛出的全局查找。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				if val, found := vm.globals.Get(s.Value); found {
					vm.stack.Push(object.NewString(object.TypeOf(val)))
				} else {
					vm.stack.Push(object.NewString("undefined"))
				}
			} else {
				vm.stack.Push(object.NewString("undefined"))
			}
		case bytecode.OP_INSTANCEOF:
			// instanceof: 栈顶是 Constructor (右), 下方是 obj (左)。
			// 检查 obj 的原型链是否包含 Constructor.prototype。
			right := vm.stack.Pop()
			left := vm.stack.Pop()
			vm.stack.Push(vm.instanceOf(left, right))
		case bytecode.OP_IN:
			// in 运算符: key in obj。左操作数(key)先压, 右操作数(obj)后压, 栈顶是 obj。
			obj := vm.stack.Pop()
			key := vm.stack.Pop()
			vm.stack.Push(vm.inOperator(obj, key))
		case bytecode.OP_THIS:
			frame := vm.currentFrame()
			if frame.Closure != nil && frame.Closure.This != nil {
				vm.stack.Push(frame.Closure.This)
			} else {
				vm.stack.Push(object.UndefinedSingleton)
			}
		case bytecode.OP_DELETE:
			// 栈: [obj, key] → 删除 obj 上的 key 属性, 推入 true/false
			key := vm.stack.Pop()
			obj := vm.stack.Pop()
			if o, ok := obj.(*object.Object); ok {
				if s, ok := key.(*object.String); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteProperty(s.Value)))
				} else if sym, ok := key.(*object.Symbol); ok {
					vm.stack.Push(object.NewBoolean(o.DeleteSymbolProperty(sym)))
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else if arr, ok := obj.(*object.Array); ok {
				// 数组删除 (简化): 仅当索引存在时删除并压缩
				if s, ok := key.(*object.String); ok {
					if idx, err := strconv.Atoi(s.Value); err == nil && idx >= 0 && idx < len(arr.Elements) {
						arr.Elements[idx] = object.UndefinedSingleton
						vm.stack.Push(object.NewBoolean(true))
					} else {
						vm.stack.Push(object.NewBoolean(false))
					}
				} else {
					vm.stack.Push(object.NewBoolean(false))
				}
			} else {
				vm.stack.Push(object.NewBoolean(true))
			}

		// ===== 控制 =====
		case bytecode.OP_BREAK, bytecode.OP_CONTINUE:
			// break/continue 已由编译器转换为 OP_JUMP/OP_LOOP
			// 如果直接出现，跳转到 operand
			frame.PC = int(operand)

		// ===== try/catch/finally =====
		case bytecode.OP_PUSH_TRY:
			// operand = catchPC (0 = 无 catch)
			vm.tryStack = append(vm.tryStack, tryEntry{
				catchPC:   int(operand),
				finallyPC: 0,
				stackBase: vm.stack.Len(),
				frameIdx:  vm.frameIdx,
			})
		case bytecode.OP_PUSH_FINALLY:
			// operand = finallyPC，设置在栈顶 try 条目上
			if len(vm.tryStack) > 0 {
				vm.tryStack[len(vm.tryStack)-1].finallyPC = int(operand)
			}
		case bytecode.OP_POP_TRY:
			// try 块正常完成，弹出处理器
			if len(vm.tryStack) > 0 {
				vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			}
		case bytecode.OP_THROW:
			val := vm.stack.Pop()
			if !vm.handleThrow(val) {
				// 无处理器: 返回错误
				return &ThrowError{Value: val}
			}
			// 异常已被捕获，继续执行 (PC 已被 handleThrow 设置)
		case bytecode.OP_END_FINALLY:
			// finally 块结束: 如果有待重新抛出的错误，重新抛出
			if vm.pendingThrow != nil {
				val := vm.pendingThrow
				vm.pendingThrow = nil
				if !vm.handleThrow(val) {
					return &ThrowError{Value: val}
				}
			}
			// 无 pending error: 正常继续

		// ===== 模块系统 =====
		case bytecode.OP_IMPORT:
			// operand = 模块路径常量索引
			specVal := frame.Constants.Get(operand)
			spec := ""
			if s, ok := specVal.(*object.String); ok {
				spec = s.Value
			}
			modExports, err := vm.loadModule(spec)
			if err != nil {
				// 模块加载失败作为异常
				errVal := object.NewErrorWithName("Error", err.Error())
				if !vm.handleThrow(errVal) {
					return &ThrowError{Value: errVal}
				}
				continue
			}
			// 推入模块导出对象
			modObj := object.NewObject()
			if modExports.Default != nil {
				modObj.SetProperty("default", modExports.Default)
			}
			for name, val := range modExports.Named {
				modObj.SetProperty(name, val)
			}
			vm.stack.Push(modObj)

		case bytecode.OP_EXPORT:
			// operand = 导出名常量索引，栈顶是导出值
			nameVal := frame.Constants.Get(operand)
			exportName := ""
			if s, ok := nameVal.(*object.String); ok {
				exportName = s.Value
			}
			val := vm.stack.Pop()
			if vm.currentExports == nil {
				vm.currentExports = &ModuleExports{Named: map[string]object.Value{}}
			}
			if exportName == "default" {
				vm.currentExports.Default = val
			} else {
				vm.currentExports.Named[exportName] = val
			}

		default:
			return fmt.Errorf("VM: unknown opcode 0x%02x (%s)", op, op.Name())
		}
	}
	return nil
}

// callFunction 从 Go 代码调用 JS 函数 (闭包或内建函数)。
// 用于 stdlib 回调桥: 当 BuiltinMethod (如 Array.prototype.map) 需要
// 调用用户传入的 JS 闭包时，通过此方法执行子帧。
func (vm *VM) callFunction(fn object.Value, this object.Value, args []object.Value) (object.Value, error) {
	// Rx 单元可直接调用: count() 等价 count.value (GetX 语义)
	if obs, ok := fn.(object.ObservableState); ok {
		return obs.RxValue(), nil
	}
	switch callee := fn.(type) {
	case *object.BuiltinFunction:
		result := callee.Fn(args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		return result, nil

	case *object.BuiltinMethod:
		result := callee.Fn(this, args...)
		if result == nil {
			return object.UndefinedSingleton, nil
		}
		return result, nil

	case *object.Closure:
		// 绑定 this (箭头函数复用自身 this)
		bound := callee
		if this != nil && !callee.IsArrow {
			bound = &object.Closure{
				Fn:             callee.Fn,
				Env:            callee.Env,
				This:           this,
				IsArrow:        callee.IsArrow,
				CapturedLocals: callee.CapturedLocals,
				CreatedAtFrame: callee.CreatedAtFrame,
			}
		}
		// 记录当前帧索引，新帧从这里 +1
		startIdx := vm.frameIdx + 1
		if err := vm.callClosure(bound, args); err != nil {
			// 子帧内抛出且未被捕获: 回收已压入的帧, 否则帧栈损坏,
			// 调用方 (回调桥) 继续执行时会跑飞
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 执行子帧直到返回
		if err := vm.runFrom(startIdx); err != nil {
			vm.unwindFramesTo(startIdx)
			return nil, err
		}
		// 返回值在栈顶
		return vm.stack.Pop(), nil
	}
	return nil, fmt.Errorf("TypeError: %s is not a function", describeCallee(fn))
}

// unwindFramesTo 回收 frameIdx >= startIdx 的所有帧。
// 用于嵌套调用 (回调桥) 抛出未捕获异常后的帧栈恢复。
// 每层帧在压栈时保留了调用点的栈高度, 回收时把本帧残留的栈值一并清掉。
func (vm *VM) unwindFramesTo(startIdx int) {
	for vm.frameIdx >= startIdx {
		f := vm.frames[vm.frameIdx]
		base := f.StackBase
		vm.popFrame()
		// 清理本帧执行期间残留的栈值 (含嵌套帧遗留)
		for vm.stack.Len() > base {
			vm.stack.Pop()
		}
	}
}

// checkCallbackErr 检查回调执行中是否产生了错误，如有则返回并清除。
// 错误信号由 object 层持有 (消费即清除)，这里只做读取转发。
func (vm *VM) checkCallbackErr() error {
	return object.TakeCallbackError()
}

// throwIfError 判断内建函数的返回值是否应作为异常抛出，是则执行 throw 流程。
//
// 语义: 内建函数返回 *object.Error 时，通常表示"操作失败，请抛出异常"
// (如 JSON.parse 的语法错误、Array.prototype.map 收到非函数回调)。
// 例外: ReturnIsValue 标记的内建函数 (如 Error/TypeError 构造器) 返回的
// Error 是普通值——new Error("x") 应返回错误对象而不是抛出它。
//
// 返回值: thrown=true 表示已进入 throw 流程 (调用方应 continue 或返回 err，
// 切勿再把 result 压栈); thrown=false 表示 result 是普通值，正常压栈。
func (vm *VM) throwIfError(result object.Value, returnsRaw bool) (thrown bool, err error) {
	errObj, isErr := result.(*object.Error)
	if !isErr || returnsRaw {
		return false, nil
	}
	if !vm.handleThrow(errObj) {
		return true, &ThrowError{Value: errObj}
	}
	return true, nil
}

// throwJSError 把结构化运行时错误 (*jsThrow) 转换为 JS 异常并走正常抛出流程。
//
// 返回值语义:
//   - nil: 异常已被 catch/finally 捕获，PC 已改写，调用方应 continue
//     (切勿再把运算结果压栈——栈已被 handleThrow 恢复到 try 入口高度)
//   - 非 nil: 没有匹配的处理器，异常继续向上传播，调用方应 return
//
// 非 *jsThrow 的普通 Go error 不属于 JS 语言级异常，原样返回。
func (vm *VM) throwJSError(err error) error {
	var jt *jsThrow
	if !errors.As(err, &jt) {
		return err
	}
	errObj := object.NewErrorWithName(jt.Name, jt.Message)
	if !vm.handleThrow(errObj) {
		return &ThrowError{Value: errObj}
	}
	return nil
}

// throwNamedError 构造命名 JS 错误并走正常抛出流程。
// 与裸 return fmt.Errorf 的区别: 这里的错误能被 try/catch 捕获。
// 返回 nil 表示已被 catch/finally 接住 (调用方应 continue)；
// 非 nil 表示异常继续向外传播 (调用方应 return)。
func (vm *VM) throwNamedError(name, format string, a ...any) error {
	errObj := object.NewErrorWithName(name, fmt.Sprintf(format, a...))
	if !vm.handleThrow(errObj) {
		return &ThrowError{Value: errObj}
	}
	return nil
}

// rethrowBridgeError 把回调桥 (object.CallFunction) 报告的错误转回 JS
// 抛出流程。桥另一侧的异常此前以裸 Go error 直接 return，导致
// getter/Proxy/数组方法回调里的任何异常都逃出 try/catch。
//   - *ThrowError: 恢复其原始抛出值 (throw x 的 x)
//   - *jsThrow:    构造命名错误 (如栈溢出 RangeError)
//   - 其余:        非 JS 语言级异常，原样返回
func (vm *VM) rethrowBridgeError(err error) error {
	if te, ok := err.(*ThrowError); ok {
		if !vm.handleThrow(te.Value) {
			return te
		}
		return nil
	}
	return vm.throwJSError(err)
}

// ===== Proxy trap 转发 =====

// proxyTrap 调用 handler 上的 trap 函数。
// 参数: proxy=代理对象, trapName=trap 名, args=trap 参数。
// 返回: (trap 返回值, 是否存在 trap, 执行错误)。
// 如果 handler 没有该 trap 或 handler 不是对象，返回 (nil, false, nil)，
// 调用方应执行默认 (绕过) 行为。
func (vm *VM) proxyTrap(proxy *object.Proxy, trapName string, args []object.Value) (object.Value, bool, error) {
	// 已撤销的代理: 所有操作抛 TypeError
	if proxy.IsRevoked {
		return nil, true, fmt.Errorf("TypeError: Cannot perform '%s' on a proxy that has been revoked", trapName)
	}
	handler := proxy.Handler
	if handler == nil {
		return nil, false, nil
	}
	trap := object.GetProxyTrap(handler, trapName)
	if trap == nil {
		return nil, false, nil
	}
	// 调用 trap，this 绑定为 handler
	result, err := vm.callFunction(trap, handler, args)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// proxyGet 处理对代理的属性读取。
// receiver 是接收者 (通常就是代理本身)。
func (vm *VM) proxyGet(proxy *object.Proxy, key string, receiver object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "get", []object.Value{proxy.Target, object.NewString(key), receiver})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接从目标读取
		val, found := proxy.Target.GetProperty(key)
		if !found || val == nil {
			return object.UndefinedSingleton, nil
		}
		return val, nil
	}
	if res == nil {
		return object.UndefinedSingleton, nil
	}
	return res, nil
}

// proxySet 处理对代理的属性写入。
func (vm *VM) proxySet(proxy *object.Proxy, key string, val, receiver object.Value) error {
	res, handled, err := vm.proxyTrap(proxy, "set", []object.Value{proxy.Target, object.NewString(key), val, receiver})
	if err != nil {
		return err
	}
	if !handled {
		// 无 trap: 直接写入目标
		proxy.Target.SetProperty(key, val)
		return nil
	}
	// 有 trap: 若 trap 返回 falsy，视为静默失败 (非严格模式)
	_ = res
	return nil
}

// proxyHas 处理对代理的属性存在性检查 (in 操作符)。
func (vm *VM) proxyHas(proxy *object.Proxy, key string) (bool, error) {
	res, handled, err := vm.proxyTrap(proxy, "has", []object.Value{proxy.Target, object.NewString(key)})
	if err != nil {
		return false, err
	}
	if !handled {
		_, found := proxy.Target.GetProperty(key)
		return found, nil
	}
	return object.IsTruthyValue(res), nil
}

// inOperator 实现 `key in obj` 语义。
// obj 为 Proxy 时走 has trap; 否则沿属性/原型链检查 key 是否存在。
func (vm *VM) inOperator(obj, key object.Value) object.Value {
	k := propKey(key)

	// Proxy: 走 has trap
	if p, ok := obj.(*object.Proxy); ok {
		found, err := vm.proxyHas(p, k)
		if err != nil {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(found)
	}

	// 普通对象: 沿原型链检查
	if obj == nil {
		return object.NewBoolean(false)
	}

	// 数组索引需检查是否真正存在: 越界索引不算属性存在 (GetProperty 对越界
	// 返回 Undefined+found=true, 用于 arr[i] 取 undefined, 但 in 语义要求 false)。
	if arr, ok := obj.(*object.Array); ok {
		if idx, err := strconv.Atoi(k); err == nil {
			return object.NewBoolean(idx >= 0 && idx < len(arr.Elements))
		}
	}

	_, found := obj.GetProperty(k)
	return object.NewBoolean(found)
}

// instanceOf 实现 `obj instanceof Constructor` 语义。
// 通过原型链检查: Constructor.prototype 是否出现在 obj 的原型链上。
// 该运行时未实现标准内置构造器的统一原型模型, 因此做了实用化处理:
// 对通过 new 构造的对象 (其 Proto 被设为构造器原型) 沿链查找; 对
// 已知的内置类型 (Array/Map/Set/RegExp/Promise/Error 等) 做类型名匹配。
func (vm *VM) instanceOf(left, right object.Value) object.Value {
	// 右侧必须是可调用构造器; 但内置构造器 (如 Array) 是 *object.Object,
	// IsCallable 不识别, 因此 Object 类型也允许继续匹配。
	_, isObj := right.(*object.Object)
	if !object.IsCallable(right) && !isObj {
		return object.NewBoolean(false)
	}

	// Proxy 作为构造器: 递归解包
	if p, ok := right.(*object.Proxy); ok {
		if pt, ok := p.Target.(*object.Proxy); ok {
			return vm.instanceOf(left, pt)
		}
	}

	// 右侧构造器的 prototype 属性
	var ctorProto object.Value
	switch c := right.(type) {
	case *object.Object:
		ctorProto, _ = c.GetProperty("prototype")
	case *object.BuiltinFunction:
		// 内建构造器 (Object/Array/String/Number/...) 的 prototype
		ctorProto, _ = c.GetProperty("prototype")
	case *object.Closure:
		// 闭包构造器: 取 prototype 属性 (new A() 时实例的原型)
		ctorProto, _ = c.GetProperty("prototype")
	}

	// 沿 left 的原型链查找 ctorProto
	if ctorProto != nil {
		cur := left
		// 最多沿链查 64 层防止循环
		for i := 0; i < 64 && cur != nil; i++ {
			if cur == ctorProto {
				return object.NewBoolean(true)
			}
			cur = protoOf(cur)
		}
	}

	// 内置类型匹配兜底
	return object.NewBoolean(matchBuiltinType(left, right))
}

// protoOf 返回值的原型对象 (沿对象模型的原型字段)。
func protoOf(v object.Value) object.Value {
	switch t := v.(type) {
	case *object.Object:
		return t.Proto
	case *object.Array:
		return t.GetProto()
	// Temporal 类型把原型放在类型注册表里 (见 object.SetTemporalProto)，
	// 各自实现了 GetProto()。缺了这些分支，instanceof 会退化成名称匹配，
	// 而构造器名 ("Instant") 与类型标识并不对应。
	case *object.TemporalInstant:
		return t.GetProto()
	case *object.TemporalPlainDateTime:
		return t.GetProto()
	case *object.TemporalPlainDate:
		return t.GetProto()
	case *object.TemporalPlainTime:
		return t.GetProto()
	case *object.TemporalPlainYearMonth:
		return t.GetProto()
	case *object.TemporalPlainMonthDay:
		return t.GetProto()
	case *object.TemporalZonedDateTime:
		return t.GetProto()
	case *object.TemporalDuration:
		return t.GetProto()
	case *object.TemporalTimeZone:
		return t.GetProto()
	case *object.TemporalCalendar:
		return t.GetProto()
	}
	return nil
}

// matchBuiltinType 对内置类型做名称匹配 (instanceof Array/Map/Set/...)。
func matchBuiltinType(left, right object.Value) bool {
	// 通过构造器内建名判断
	ctorName := builtinName(right)
	if ctorName == "" {
		return false
	}
	lt := left.Type()
	switch ctorName {
	case "Array":
		return lt == object.ARRAY_OBJ
	case "Object":
		// 数组、对象、函数等都是 Object 的实例
		return lt == object.OBJECT_OBJ || lt == object.ARRAY_OBJ || object.IsCallable(left) ||
			lt == object.MAP_OBJ || lt == object.SET_OBJ || lt == object.REGEXP_OBJ ||
			lt == object.PROMISE_OBJ || lt == object.ERROR_OBJ
	case "Map":
		return lt == object.MAP_OBJ
	case "Set":
		return lt == object.SET_OBJ
	case "RegExp":
		return lt == object.REGEXP_OBJ
	case "Promise":
		return lt == object.PROMISE_OBJ
	case "Error", "TypeError", "RangeError", "ReferenceError", "SyntaxError":
		return lt == object.ERROR_OBJ
	case "String":
		return lt == object.STRING_OBJ
	case "Number":
		return lt == object.NUMBER_OBJ
	case "Boolean":
		return lt == object.BOOLEAN_OBJ
	case "Function":
		return object.IsCallable(left)
	}
	return false
}

// builtinName 返回内置构造器/函数的名称。
func builtinName(v object.Value) string {
	switch f := v.(type) {
	case *object.BuiltinFunction:
		if f.Name != "" {
			return f.Name
		}
	case *object.BuiltinMethod:
		if f.Name != "" {
			return f.Name
		}
	}
	// 通过属性名兜底 (closure 构造器一般带 name)
	if o, ok := v.(*object.Object); ok {
		if nv, found := o.GetProperty("name"); found {
			if s, ok := nv.(*object.String); ok {
				return s.Value
			}
		}
	}
	return ""
}

// describeCallee 生成错误信息中对"被当作函数调用的值"的简短描述。
//
// 直接用 Inspect 会把整个内建对象 (含 prototype 上的几十个方法) 塞进错误信息，
// 例如 `Array(3)` 未定义时会打印几千字符。这里对超长描述做截断，
// 并优先使用构造器/函数的名字。
func describeCallee(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	if name := builtinName(v); name != "" {
		return name
	}
	s := v.Inspect()
	if len(s) > 64 {
		s = s[:64] + "..."
	}
	return s
}

// propKey 将值转换为属性键字符串 (与 stdlib.toPropKey 一致)。
func propKey(v object.Value) string {
	switch k := v.(type) {
	case *object.String:
		return k.Value
	case *object.Number:
		return k.Inspect()
	case *object.Symbol:
		return k.Inspect()
	}
	return v.Inspect()
}

func (vm *VM) proxyApply(proxy *object.Proxy, thisArg object.Value, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "apply", []object.Value{proxy.Target, thisArg, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接调用目标
		return vm.callFunction(proxy.Target, thisArg, args)
	}
	return res, nil
}

// proxyConstruct 处理对代理 (目标为构造函数) 的 new 调用。
func (vm *VM) proxyConstruct(proxy *object.Proxy, args []object.Value) (object.Value, error) {
	res, handled, err := vm.proxyTrap(proxy, "construct", []object.Value{proxy.Target, object.NewArray(args)})
	if err != nil {
		return nil, err
	}
	if !handled {
		// 无 trap: 直接以 new 方式调用目标
		newObj := object.NewObject()
		result, err := vm.callFunction(proxy.Target, newObj, args)
		if err != nil {
			return nil, err
		}
		// 构造语义: 若构造器返回对象则用之，否则用新对象
		if result != nil && object.IsObjectLike(result) {
			return result, nil
		}
		return newObj, nil
	}
	return res, nil
}

// isCallable 检查值是否可作为函数调用 (含函数目标代理)。
func isCallable(v object.Value) bool {
	if object.IsCallable(v) {
		return true
	}
	if p, ok := v.(*object.Proxy); ok {
		return isCallable(p.Target)
	}
	return false
}

// handleThrow 处理异常抛出。
// 检查 tryStack 中是否有匹配的 catch/finally 处理器。
// 如果找到: 设置 PC 和栈，返回 true。
// 如果未找到: 返回 false (异常将传播到上层)。
func (vm *VM) handleThrow(val object.Value) bool {
	for len(vm.tryStack) > 0 {
		entry := vm.tryStack[len(vm.tryStack)-1]

		// 边界检查: 处理器在外层帧 (低于当前子执行的起始帧) 时不在此处
		// 解退。让异常以 ThrowError 返回给回调桥，由外层的抛出路径
		// (throwIfError/rethrowBridgeError) 在正确的嵌套层级匹配它。
		if entry.frameIdx < vm.throwBoundary {
			return false
		}

		// 如果 try 条目在不同的帧中，先弹出帧
		if vm.frameIdx > entry.frameIdx {
			for vm.frameIdx > entry.frameIdx {
				vm.popFrame()
				// 弹出 popFrame 推入的返回值
				if vm.stack.Len() > 0 {
					vm.stack.Pop()
				}
			}
		}

		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]

		// 恢复栈到 try 开始时的高度
		for vm.stack.Len() > entry.stackBase {
			vm.stack.Pop()
		}

		if entry.catchPC > 0 {
			// 有 catch: 推入错误值，跳到 catch 块
			vm.stack.Push(val)
			vm.currentFrame().PC = entry.catchPC
			return true
		}
		if entry.finallyPC > 0 {
			// 有 finally 但无 catch: 保存待抛出值，跳到 finally 块
			vm.pendingThrow = val
			vm.currentFrame().PC = entry.finallyPC
			return true
		}
		// 无 catch 无 finally: 继续向上查找
	}
	return false
}

// loadModule 加载并执行模块，返回导出对象。
// 使用模块缓存避免重复加载。
func (vm *VM) loadModule(spec string) (*ModuleExports, error) {
	// 内置模块 (如 "gx/solid"、"gox") 优先于文件系统解析
	if exports, ok := object.LookupBuiltinModule(spec); ok {
		mod := &ModuleExports{Named: exports}
		vm.modules[spec] = mod
		return mod, nil
	}

	// "gox" 与 "gx/..." 是保留的内置模块命名空间: 未命中注册表时直接
	// 报错并列出可用模块, 不再落到文件系统解析 —— 否则拼写错误会变成
	// 莫名其妙的 "Cannot find module 'gx/dialg'" 文件读取错误。
	if spec == "gox" || strings.HasPrefix(spec, "gx/") {
		return nil, fmt.Errorf("Cannot find module '%s' (unknown builtin module; available: %s)",
			spec, strings.Join(object.RegisteredBuiltinModules(), ", "))
	}

	// 解析模块路径
	absPath := spec
	if vm.moduleBase != "" {
		absPath = resolvePath(vm.moduleBase, spec)
	}

	// 检查缓存
	if mod, ok := vm.modules[absPath]; ok {
		return mod, nil
	}

	// 读取文件
	source, err := osReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("Cannot find module '%s'", spec)
	}

	// 编译模块
	c, err := compileSource(string(source), true)
	if err != nil {
		if se, ok := err.(*sourceError); ok && se.parse {
			return nil, fmt.Errorf("Module parse error: %s", se.msg)
		}
		return nil, fmt.Errorf("Module compile error: %v", err)
	}

	// 执行模块
	savedExports := vm.currentExports
	vm.currentExports = &ModuleExports{Named: map[string]object.Value{}}

	// 保存当前模块路径并设置新基准
	savedBase := vm.moduleBase
	vm.moduleBase = dirOf(absPath)

	// 循环导入防线: 先注册导出对象再执行 —— 执行期间模块再 import 自己/形成
	// 环时, 命中缓存拿到这份(填充中的)导出对象, 而不是无限重新编译执行
	// (此前缓存写在执行后, 循环导入会一路递归到栈溢出)。
	vm.modules[absPath] = vm.currentExports

	modVM := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), vm.globals)
	modVM.modules = vm.modules
	modVM.moduleBase = vm.moduleBase
	modVM.currentExports = vm.currentExports
	if err := modVM.RunCompiled(c); err != nil {
		// 执行失败不缓存半成品, 便于上层重试时报出同样错误
		delete(vm.modules, absPath)
		vm.currentExports = savedExports
		vm.moduleBase = savedBase
		return nil, fmt.Errorf("Module execution error: %v", err)
	}

	// 恢复状态
	vm.currentExports = savedExports
	vm.moduleBase = savedBase

	return vm.modules[absPath], nil
}

// SetModuleBase 设置模块基准路径。
func (vm *VM) SetModuleBase(path string) {
	vm.moduleBase = path
}

// ===== 辅助方法 =====

// createClosure 从 FunctionMetadata 创建闭包。
// 捕获当前帧的外层局部变量 (slots 0..BaseSlot-1)。
func (vm *VM) createClosure(meta *bytecode.FunctionMetadata, frame *Frame) *object.Closure {
	// 保存当前帧的常量池引用
	fn := metaToCompiledFunction(meta, frame.Constants.Constants)

	// 捕获外层局部变量: 共享当前帧的 Locals 数组本身，而不是拷一份快照。
	//
	// 拷快照会让兄弟闭包各自持有副本 —— 离开创建帧后它们就彻底失联，于是
	// `const [inc, get] = mk(); inc(); get()` 读不到彼此的修改。
	// 共享同一个数组后，写操作 (OP_STORE → SharedCells) 对所有捕获者同时可见；
	// 该数组随闭包存活 (GC 保活)，等价于 ECMAScript 的 binding cell 逃逸到堆。
	var captured []object.Value
	if meta.BaseSlot > 0 {
		// 必须裁剪到 BaseSlot: 闭包只会访问 slot < BaseSlot，
		// 若不裁剪，整帧 Locals 会在调用时被拷进内层slot 区，
		// 把尚未初始化的绑定"填上"外层残留值，TDZ 检测随之失效。
		if meta.BaseSlot <= len(frame.Locals) {
			captured = frame.Locals[:meta.BaseSlot]
		} else {
			captured = frame.Locals
		}
	}

	// this 绑定: 箭头函数复用外层 this
	var thisVal object.Value = object.UndefinedSingleton
	if frame.Closure != nil && frame.Closure.This != nil {
		thisVal = frame.Closure.This
	}

	return &object.Closure{
		Fn:             fn,
		Env:            vm.globals,
		This:           thisVal,
		IsArrow:        meta.IsArrow,
		CapturedLocals: captured,
		CreatedAtFrame: vm.frameIdx,
	}
}

// trackClosure 记录闭包到当前帧的 CreatedClosures 列表。
// 用于 OP_STORE 时将外层变量修改传播到所有子闭包 (outer → closure)。
func (vm *VM) trackClosure(closure *object.Closure) {
	frame := vm.currentFrame()
	frame.CreatedClosures = append(frame.CreatedClosures, closure)
}

// callClosure 调用闭包函数。
// 创建新帧，复制捕获的局部变量，放置参数。
func (vm *VM) callClosure(closure *object.Closure, args []object.Value) error {
	fn := closure.Fn
	if fn == nil {
		return fmt.Errorf("VM: closure has no function")
	}

	// 检查调用栈深度 (jsThrow 使栈溢出 RangeError 能被 try/catch 捕获，
	// 也能经回调桥的 rethrowBridgeError 正确转回 JS 抛出流程)
	if vm.frameIdx >= MaxFrames-1 {
		return &jsThrow{Name: "RangeError", Message: "Maximum call stack size exceeded"}
	}

	// 创建新帧: 使用闭包自带的常量池 (跨模块时不同于 vm.constants)
	constants := vm.constants
	if fn.Constants != nil {
		constants = &bytecode.ConstantPool{Constants: fn.Constants}
	}
	frame := NewFrame(fn.Instructions, constants, fn.NumLocals)
	frame.Closure = closure
	// 记录进入本帧时的栈高度, 返回时据此截断清理本帧残留栈值
	frame.StackBase = vm.stack.Len()

	// 复制捕获的外层局部变量作为初值
	if len(closure.CapturedLocals) > 0 {
		copy(frame.Locals, closure.CapturedLocals)
	}
	// 但写入必须回流到共享的 binding cell，供兄弟闭包与后续调用观察
	frame.SharedCells = closure.CapturedLocals

	// 放置参数: 参数从 BaseSlot 开始排列
	baseSlot := fn.BaseSlot

	// 检查是否有 rest 参数 (最后一个参数)
	hasRest := len(fn.Parameters) > 0 && fn.Parameters[len(fn.Parameters)-1].Rest
	restParamIndex := len(fn.Parameters) - 1

	if hasRest {
		// 非 rest 参数正常放置
		numRegular := restParamIndex
		for i := 0; i < numRegular; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				if i < len(args) {
					frame.Locals[slot] = args[i]
				} else {
					frame.Locals[slot] = object.UndefinedSingleton
				}
			}
		}
		// rest 参数: 收集剩余参数到数组
		var restElements []object.Value
		if len(args) > numRegular {
			restElements = make([]object.Value, len(args)-numRegular)
			copy(restElements, args[numRegular:])
		}
		restSlot := baseSlot + restParamIndex
		if restSlot < len(frame.Locals) {
			frame.Locals[restSlot] = object.NewArray(restElements)
		}
	} else {
		// 无 rest: 仅放置声明的前 NumParameters 个参数, 多余实参只进入 arguments
		placeCount := len(args)
		if placeCount > fn.NumParameters {
			placeCount = fn.NumParameters
		}
		for i := 0; i < placeCount; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = args[i]
			}
		}
		// 剩余参数填充 undefined
		for i := placeCount; i < fn.NumParameters; i++ {
			slot := baseSlot + i
			if slot < len(frame.Locals) {
				frame.Locals[slot] = object.UndefinedSingleton
			}
		}
	}

	// 创建 arguments 对象 (类数组, 含全部实参)。放在参数放置之后, 避免被参数覆盖。
	if fn.ArgumentsSlot >= 0 && fn.ArgumentsSlot < len(frame.Locals) {
		argsCopy := make([]object.Value, len(args))
		copy(argsCopy, args)
		frame.Locals[fn.ArgumentsSlot] = object.NewArray(argsCopy)
	}

	// 命名函数表达式的自引用: 名字槽位指向闭包自身 (递归入口)
	if fn.SelfSlot >= 0 && fn.SelfSlot < len(frame.Locals) {
		frame.Locals[fn.SelfSlot] = closure
	}

	vm.pushFrame(frame)
	return nil
}

// genResume 驱动生成器前进:
// - 首次: 以 gen.Args 调用闭包创建帧
// - 恢复: 重建保存的帧, 压入 arg 作为 yield 表达式的结果
// 运行到下一个 yield (返回 YieldSignal) 或函数结束。
// 返回: (yield 值/返回值, 是否完成)。
func (vm *VM) genResume(gen *object.Generator, arg object.Value) (object.Value, bool, error) {
	if gen.Done {
		return object.UndefinedSingleton, true, nil
	}

	if !gen.Started {
		// 首次启动: 正常调用闭包 (参数来自创建时的 Args)
		if err := vm.callClosure(gen.Closure, gen.Args); err != nil {
			gen.Done = true
			return object.UndefinedSingleton, true, err
		}
		gen.Started = true
		return vm.runSuspendedGen(gen)
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return object.UndefinedSingleton, true, nil
	}
	// 重建帧，压入 arg 作为 yield 表达式的值 (栈顶)
	vm.rebuildGenFrame(gen)
	vm.stack.Push(arg)
	return vm.runSuspendedGen(gen)
}

// genThrow 把 throwVal 作为异常抛入暂停在 yield 点的 generator。
// await 的 promise 被 reject 时的恢复路径: generator 体内的 try/catch
// 可以捕获该异常；未捕获时 generator 终止并返回错误。
func (vm *VM) genThrow(gen *object.Generator, throwVal object.Value) (object.Value, bool, error) {
	if gen.Done {
		return object.UndefinedSingleton, true, nil
	}
	if !gen.Started {
		// 尚未启动: 无帧可恢复，视为立即抛出
		gen.Done = true
		return object.UndefinedSingleton, true, &ThrowError{Value: throwVal}
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return object.UndefinedSingleton, true, nil
	}
	vm.rebuildGenFrame(gen)

	// 仅当 generator 帧自身挂有 try 处理器时才查找处理器:
	// tryStack 中残留的更深/更浅帧条目属于无关执行上下文，
	// 把异常交给它们会破坏帧栈。
	hasOwnHandler := false
	for _, te := range vm.tryStack {
		if te.frameIdx == vm.frameIdx {
			hasOwnHandler = true
			break
		}
	}
	if !hasOwnHandler || !vm.handleThrow(throwVal) {
		gen.Done = true
		return object.UndefinedSingleton, true, &ThrowError{Value: throwVal}
	}

	return vm.runSuspendedGen(gen)
}

// rebuildGenFrame 重建 generator 暂停时保存的帧 (含恢复 try 处理器条目)。
func (vm *VM) rebuildGenFrame(gen *object.Generator) *Frame {
	// 压入保存的帧栈残留值 (顺序与保存时一致)
	for _, v := range gen.SavedStack {
		vm.stack.Push(v)
	}
	locals := make([]object.Value, len(gen.Locals))
	copy(locals, gen.Locals)
	frame := &Frame{
		Instructions: gen.Instructions,
		PC:           gen.PC,
		Locals:       locals,
		Closure:      gen.Closure,
		Constants:    &bytecode.ConstantPool{Constants: gen.Constants},
		StackBase:    vm.stack.Len(),
	}
	vm.pushFrame(frame)
	// 把 yield 时保存的 try 处理器条目按相对值换算后重新挂回
	for _, te := range gen.PendingTries {
		vm.tryStack = append(vm.tryStack, tryEntry{
			catchPC:   te.CatchPC,
			finallyPC: te.FinallyPC,
			stackBase: frame.StackBase + te.RelStackBase,
			frameIdx:  vm.frameIdx + te.RelFrameIdx,
		})
	}
	gen.PendingTries = nil
	return frame
}

// runSuspendedGen 运行已重建帧的 generator 直到下一个 yield 或结束。
func (vm *VM) runSuspendedGen(gen *object.Generator) (object.Value, bool, error) {
	frame := vm.currentFrame()
	prevGen := vm.currentGenerator
	vm.currentGenerator = gen
	err := vm.runFrom(vm.frameIdx)
	vm.currentGenerator = prevGen
	return vm.finishGenRun(gen, frame, err)
}

// finishGenRun 处理 generator 一次恢复运行的收尾。
func (vm *VM) finishGenRun(gen *object.Generator, frame *Frame, err error) (object.Value, bool, error) {
	if err != nil {
		if ysig, ok := err.(*YieldSignal); ok {
			gen.Value = ysig.value
			return ysig.value, false, nil
		}
		// 其他错误: generator 终止
		gen.Done = true
		return object.UndefinedSingleton, true, err
	}

	// 正常结束: 弹出返回值
	gen.Done = true
	var result object.Value = object.UndefinedSingleton
	if vm.stack.Len() > frame.StackBase {
		result = vm.stack.Pop()
	}
	gen.Value = result
	return result, true, nil
}

// addValues 实现 JavaScript 的 + 运算符 (数字加法或字符串拼接)。
// maxStringLength 是 VM 侧字符串长度上限 (与 stdlib.maxStringLength 一致，
// 1<<30 字节)。字符串拼接无上限时，`s = s + s` 翻倍可在数秒内请求到
// TB 级分配，Go 的 OOM 是不可 recover 的致命错误 —— 必须在拼接前拦截。
const maxStringLength = 1 << 30

// concatStrings 带上限的字符串拼接。超限时返回 RangeError (Invalid string
// length)，与 String.prototype.repeat/padStart 的既有行为一致。
func concatStrings(a, b string) (string, error) {
	if len(a)+len(b) > maxStringLength {
		return "", &jsThrow{Name: "RangeError", Message: "Invalid string length"}
	}
	return a + b, nil
}

func (vm *VM) addValues(a, b object.Value) (object.Value, error) {
	// 字符串拼接
	if aStr, ok := a.(*object.String); ok {
		s, err := concatStrings(aStr.Value, toJSString(b))
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	if bStr, ok := b.(*object.String); ok {
		s, err := concatStrings(toJSString(a), bStr.Value)
		if err != nil {
			return nil, err
		}
		return object.NewString(s), nil
	}
	// 数组拼接 (简化)
	if aArr, ok := a.(*object.Array); ok {
		if bArr, ok := b.(*object.Array); ok {
			elements := make([]object.Value, 0, len(aArr.Elements)+len(bArr.Elements))
			elements = append(elements, aArr.Elements...)
			elements = append(elements, bArr.Elements...)
			return object.NewArray(elements), nil
		}
	}
	// BigInt 加法。
	// 字符串拼接已在上面处理 (BigInt 与 String 相加时走 ToString 得到 "1")，
	// 因此这里只需区分 BigInt+BigInt 与 BigInt+非 BigInt 两种情况。
	aBig, aIsBig := a.(*object.BigInt)
	bBig, bIsBig := b.(*object.BigInt)
	if aIsBig || bIsBig {
		if aIsBig && bIsBig {
			return aBig.Add(bBig), nil
		}
		return nil, errMixBigInt
	}

	// 数字加法
	aNum := toNumber(a)
	bNum := toNumber(b)
	return object.NewNumber(aNum + bNum), nil
}

// getIndex 实现索引访问 obj[index]。
func (vm *VM) getIndex(obj, index object.Value) object.Value {
	switch o := obj.(type) {
	case *object.Array:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Elements) {
				if o.Elements[idx] == nil {
					return object.UndefinedSingleton
				}
				return o.Elements[idx]
			}
		}
		// 字符串索引
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.String:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < len(o.Value) {
				return object.NewString(string(o.Value[idx]))
			}
		}
		return object.UndefinedSingleton

	case *object.TypedArray:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 && idx < o.Length {
				return o.GetElement(idx)
			}
			return object.UndefinedSingleton
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			return o.GetIndexedElement(int(n.Value))
		}
		if s, ok := index.(*object.String); ok {
			val, _ := o.(object.Value).GetProperty(s.Value)
			return val
		}
		return object.UndefinedSingleton

	case *object.Object:
		if s, ok := index.(*object.String); ok {
			val, found := o.GetProperty(s.Value)
			if !found {
				return object.UndefinedSingleton
			}
			return val
		}
		// Symbol 键: 检查自身及原型链上的 Symbol 属性
		if sym, ok := index.(*object.Symbol); ok {
			if val, found := lookupSymbolProperty(o, sym); found {
				return val
			}
			return object.UndefinedSingleton
		}
		return object.UndefinedSingleton
	}
	return object.UndefinedSingleton
}

// lookupSymbolProperty 沿对象原型链查找以 Symbol 为键的属性。
func lookupSymbolProperty(o *object.Object, sym *object.Symbol) (object.Value, bool) {
	for cur := o; cur != nil; {
		if val, found := cur.GetSymbolProperty(sym); found {
			return val, true
		}
		if cur.Proto == nil {
			break
		}
		next, ok := cur.Proto.(*object.Object)
		if !ok {
			break
		}
		cur = next
	}
	return nil, false
}

// setIndex 实现索引赋值 obj[index] = val。
func (vm *VM) setIndex(obj, index, val object.Value) {
	switch o := obj.(type) {
	case *object.Array:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				for len(o.Elements) <= idx {
					o.Elements = append(o.Elements, object.UndefinedSingleton)
				}
				o.Elements[idx] = val
				return
			}
		}
		// 非数字索引 (如 arr.foo = 1) 走通用属性设置
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case *object.TypedArray:
		if n, ok := index.(*object.Number); ok {
			idx := int(n.Value)
			if idx >= 0 {
				// 越界写按规范静默忽略 (setElement 内部处理)
				o.SetElement(idx, val)
			}
			return
		}
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		}
	case object.RxIndexed:
		if n, ok := index.(*object.Number); ok {
			o.SetIndexedElement(int(n.Value), val)
		}
	case *object.Object:
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
		}
	default:
		// 其余类型 (Error/RegExp/Closure/Promise 等) 的字符串键赋值
		// 统一走 Value 接口。基础类型 (String/Number/null/undefined) 的
		// SetProperty 是无操作，与 JS 原始值语义一致。
		if s, ok := index.(*object.String); ok {
			obj.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			if sp, ok := obj.(interface {
				SetSymbolProperty(*object.Symbol, object.Value)
			}); ok {
				sp.SetSymbolProperty(sym, val)
			}
		}
	}
}

// ===== 类型转换辅助函数 =====

// toNumber 将任意值转换为 float64。
func toNumber(v object.Value) float64 {
	switch val := v.(type) {
	case *object.Number:
		return val.Value
	case *object.Boolean:
		if val.Value {
			return 1
		}
		return 0
	case *object.Null:
		return 0
	case *object.Undefined:
		return math.NaN()
	case *object.String:
		return object.ParseJSNumber(val.Value)
	default:
		return math.NaN()
	}
}

// toJSString 将任意值转换为 JavaScript 字符串表示。
// 使用 ECMAScript ToString 语义 (数组 join、对象 [object Object]、
// 自定义 toString 优先)，见 object.ToString。
func toJSString(v object.Value) string {
	return object.ToString(v)
}

// looseEquals 实现 JavaScript 的 == (宽松相等)。
func looseEquals(a, b object.Value) bool {
	// 同类型直接比较
	if a.Type() == b.Type() {
		return strictEquals(a, b)
	}
	// null == undefined
	if isNullish(a) && isNullish(b) {
		return true
	}
	// BigInt == BigInt/Number/String (数学值比较)
	if a.Type() == object.BIGINT_OBJ || b.Type() == object.BIGINT_OBJ {
		return bigIntLooseEqual(a, b)
	}
	// number == string
	if a.Type() == object.NUMBER_OBJ && b.Type() == object.STRING_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if a.Type() == object.STRING_OBJ && b.Type() == object.NUMBER_OBJ {
		return toNumber(a) == toNumber(b)
	}
	// boolean == other
	if a.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	if b.Type() == object.BOOLEAN_OBJ {
		return toNumber(a) == toNumber(b)
	}
	return false
}

// strictEquals 实现 JavaScript 的 === (严格相等)。
func strictEquals(a, b object.Value) bool {
	if a.Type() != b.Type() {
		return false
	}
	switch av := a.(type) {
	case *object.Number:
		if bv, ok := b.(*object.Number); ok {
			return av.Value == bv.Value
		}
	case *object.String:
		if bv, ok := b.(*object.String); ok {
			return av.Value == bv.Value
		}
	case *object.Boolean:
		if bv, ok := b.(*object.Boolean); ok {
			return av.Value == bv.Value
		}
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	case *object.Symbol:
		if bv, ok := b.(*object.Symbol); ok {
			return av.ID == bv.ID
		}
	case *object.BigInt:
		if bv, ok := b.(*object.BigInt); ok {
			return bigIntStrictEqual(av, bv)
		}
	}
	// 引用类型 (对象/数组/函数/Map/Set/RegExp/...): 按引用同一性比较。
	// ECMAScript 的 IsStrictlyEqual 对 Object 类型即"是否同一个引用"，
	// 缺少这一分支时连 `o === o` 都会得到 false。
	// 所有 Value 实现都是指针类型，接口比较即指针比较。
	return a == b
}

// isNullish 检查值是否为 null 或 undefined。
func isNullish(v object.Value) bool {
	_, isNull := v.(*object.Null)
	_, isUndef := v.(*object.Undefined)
	return isNull || isUndef
}

// ===== 便捷函数 =====

// Eval 编译并执行 JS 源码，返回最后一个表达式的值。
func Eval(input string) (object.Value, error) {
	vm, err := EvalVM(input)
	if err != nil {
		return nil, err
	}
	return vm.LastPopped(), nil
}

// EvalVM 编译并执行 JS 源码，返回 VM 实例 (用于访问定时器等运行时状态)。
func EvalVM(input string) (*VM, error) {
	c, err := compileSource(input, false)
	if err != nil {
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	if err := vm.RunCompiled(c); err != nil {
		return nil, fmt.Errorf("vm error: %v", err)
	}
	return vm, nil
}

// EvalWithGlobals 使用预设全局变量编译并执行 JS 源码。
func EvalWithGlobals(input string, globals *runtime.Environment) (object.Value, error) {
	c, err := compileSource(input, false)
	if err != nil {
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), globals)
	if err := vm.RunCompiled(c); err != nil {
		return nil, fmt.Errorf("vm error: %v", err)
	}

	return vm.LastPopped(), nil
}

// EvalFile 读取并执行 JS 脚本文件。
// 与 Eval 的区别: 会以文件所在目录作为模块基准路径, 使入口脚本中的相对
// import/export (如 `import x from "./mod.js"`) 能正确解析。
func EvalFile(path string) (object.Value, error) {
	vm, err := EvalFileVM(path)
	if err != nil {
		return nil, err
	}
	return vm.LastPopped(), nil
}

// EvalFileVM 读取并执行 JS 脚本文件, 返回 VM 实例。
// 供需要继续驱动事件循环 (严格定时器回调等) 的调用方使用。
func EvalFileVM(path string) (*VM, error) {
	source, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %v", err)
	}

	c, err := compileSource(string(source), false)
	if err != nil {
		return nil, evalEntryError(err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	vm.SetModuleBase(filepath.Dir(path))
	if err := vm.RunCompiled(c); err != nil {
		return nil, fmt.Errorf("vm error: %v", err)
	}

	return vm, nil
}

// joinStrings 连接字符串切片。
func joinStrings(strs []string, sep string) string {
	return strings.Join(strs, sep)
}

// osReadFile 读取文件内容。
func osReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

// resolvePath 解析模块相对路径。
func resolvePath(base, spec string) string {
	if filepath.IsAbs(spec) {
		return filepath.Clean(spec)
	}
	return filepath.Clean(filepath.Join(base, spec))
}

// dirOf 返回路径的目录部分。
func dirOf(path string) string {
	return filepath.Dir(path)
}
