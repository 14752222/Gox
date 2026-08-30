package vm

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"js-runtime/bytecode"
	"js-runtime/compiler"
	"js-runtime/lexer"
	"js-runtime/object"
	"js-runtime/parser"
	"js-runtime/runtime"
	"js-runtime/stdlib"
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
	catchPC   int  // catch 块的 PC (0 = 无 catch)
	finallyPC int  // finally 块的 PC (0 = 无 finally)
	stackBase int  // 进入 try 时的栈高度
	frameIdx  int  // 进入 try 时的帧索引
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
	object.SetCallFunction(func(fn object.Value, this object.Value, args []object.Value) object.Value {
		if currentVM == nil {
			return object.UndefinedSingleton
		}
		result, err := currentVM.callFunction(fn, this, args)
		if err != nil {
			currentVM.callbackErr = err
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
			currentVM.callbackErr = err
			return object.UndefinedSingleton, true
		}
		return val, done
	})
}

// VM 是 JavaScript 字节码虚拟机。
type VM struct {
	frames      []*Frame            // 调用栈
	frameIdx    int                 // 当前帧索引 (栈顶)
	stack       *Stack              // 操作数栈
	globals     *runtime.Environment // 全局变量环境
	constants   *bytecode.ConstantPool
	lastPopped  object.Value        // 最后弹出的值 (用于测试)
	callbackErr error              // 回调执行中产生的错误

	// try-catch-finally 支持
	tryStack     []tryEntry  // try 处理器栈
	pendingThrow object.Value // finally 块中待重新抛出的错误 (nil = 无)

	// 模块系统
	modules      map[string]*ModuleExports // 模块缓存 (按绝对路径)
	moduleBase   string                    // 模块基准路径 (用于解析相对路径)
	currentExports *ModuleExports          // 当前模块的导出对象

	// generator 支持
	currentGenerator *object.Generator // 当前正在执行的 generator (OP_YIELD 时使用)
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
	// 事件循环期间注册 currentVM，保证回调链中的 object.CallFunction 桥
	// (如 Promise resolve 触发的 .then 回调) 能找到正确的 VM 实例。
	// 主脚本执行结束后 currentVM 会被恢复为 nil，若不在此处重新注册，
	// 定时器回调里 resolve 的 Promise 的 then 回调会被静默丢弃。
	saved := currentVM
	currentVM = vm
	defer func() { currentVM = saved }()

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
				time.Sleep(cd)
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
			return nil
		}

		// 等待到最早唤醒点。等待期间持续检查严格定时器信号,
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
	return vm.runFrom(0)
}

// runFrom 从指定帧索引开始执行指令循环。
// startFrameIdx: 起始帧索引，循环持续到 vm.frameIdx < startFrameIdx。
// 用于主程序执行 (startFrameIdx=0) 和回调子帧执行。
func (vm *VM) runFrom(startFrameIdx int) error {
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
			vm.stack.Push(frame.Locals[slot])
	case bytecode.OP_STORE:
		slot := int(operand)
		val := vm.stack.Pop()
		if slot >= len(frame.Locals) {
			for len(frame.Locals) <= slot {
				frame.Locals = append(frame.Locals, object.UndefinedSingleton)
			}
		}
		frame.Locals[slot] = val
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
					return fmt.Errorf("ReferenceError: %s is not defined", s.Value)
				}
				vm.stack.Push(val)
			}
		case bytecode.OP_STORE_GLOBAL:
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				// 弹出值 (与 OP_STORE 语义一致)。若需保留表达式结果, 编译器会在存储前 DUP。
				val := vm.stack.Pop()
				if _, exists := vm.globals.Get(s.Value); exists {
					vm.globals.Set(s.Value, val)
				} else {
					vm.globals.Declare(s.Value, val, false)
				}
			}
		case bytecode.OP_DECLARE:
			// 声明 let 变量到全局环境 (弹出值, 声明不返回结果)。
			// 已存在时更新值 (宽松 REPL 语义)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				if _, exists := vm.globals.Get(s.Value); exists {
					vm.globals.Set(s.Value, val)
				} else {
					vm.globals.Declare(s.Value, val, false)
				}
			}
		case bytecode.OP_DECLARE_CONST:
			// 声明 const 变量到全局环境 (弹出值, 声明不返回结果)。
			name := frame.Constants.Get(operand)
			if s, ok := name.(*object.String); ok {
				val := vm.stack.Pop()
				vm.globals.Declare(s.Value, val, true)
			}

		// ===== 算术运算 =====
		case bytecode.OP_ADD:
			b := vm.stack.Pop()
			a := vm.stack.Pop()
			result, err := vm.addValues(a, b)
			if err != nil {
				return err
			}
			vm.stack.Push(result)
		case bytecode.OP_SUB:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewNumber(a - b))
		case bytecode.OP_MUL:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewNumber(a * b))
		case bytecode.OP_DIV:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			if b == 0 {
				if a == 0 {
					vm.stack.Push(object.NewNumber(math.NaN()))
				} else if a > 0 {
					vm.stack.Push(object.NewNumber(math.Inf(1)))
				} else {
					vm.stack.Push(object.NewNumber(math.Inf(-1)))
				}
			} else {
				vm.stack.Push(object.NewNumber(a / b))
			}
		case bytecode.OP_MOD:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			if b == 0 {
				vm.stack.Push(object.NewNumber(math.NaN()))
			} else {
				vm.stack.Push(object.NewNumber(math.Mod(a, b)))
			}
		case bytecode.OP_POW:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewNumber(math.Pow(a, b)))
		case bytecode.OP_NEG:
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewNumber(-a))
		case bytecode.OP_BIT_AND:
			b := int64(toNumber(vm.stack.Pop()))
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(a & b)))
		case bytecode.OP_BIT_OR:
			b := int64(toNumber(vm.stack.Pop()))
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(a | b)))
		case bytecode.OP_BIT_XOR:
			b := int64(toNumber(vm.stack.Pop()))
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(a ^ b)))
		case bytecode.OP_SHL:
			b := int64(toNumber(vm.stack.Pop()))
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(a << uint(b))))
		case bytecode.OP_SHR:
			b := int64(toNumber(vm.stack.Pop()))
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(a >> uint(b))))
		case bytecode.OP_USHR:
			b := int64(toNumber(vm.stack.Pop()))
			a := uint64(int64(toNumber(vm.stack.Pop())))
			vm.stack.Push(object.NewNumber(float64(a >> uint(b))))
		case bytecode.OP_BIT_NOT:
			a := int64(toNumber(vm.stack.Pop()))
			vm.stack.Push(object.NewNumber(float64(^a)))

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
		case bytecode.OP_LT:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewBoolean(a < b))
		case bytecode.OP_GT:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewBoolean(a > b))
		case bytecode.OP_LTE:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewBoolean(a <= b))
		case bytecode.OP_GTE:
			b := toNumber(vm.stack.Pop())
			a := toNumber(vm.stack.Pop())
			vm.stack.Push(object.NewBoolean(a >= b))
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
					return err
				}

			case *object.Closure:
				// generator 函数调用: 不执行函数体, 返回 Generator 对象
				if callee.Fn != nil && callee.Fn.IsGenerator {
					vm.stack.Push(object.NewGenerator(callee, args))
				} else {
					if err := vm.callClosure(callee, args); err != nil {
						return err
					}
				}

			case *object.Proxy:
				// 代理: 转发到 apply trap (this 为 undefined)
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					return err
				}
				vm.stack.Push(result)

			default:
				return fmt.Errorf("TypeError: %s is not a function", fn.Inspect())
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
					return err
				}
			case *object.Closure:
				if err := vm.callClosure(callee, args); err != nil {
					return err
				}
			case *object.Proxy:
				result, err := vm.proxyApply(callee, object.UndefinedSingleton, args)
				if err != nil {
					return err
				}
				vm.stack.Push(result)
			default:
				return fmt.Errorf("TypeError: %s is not a function", fn.Inspect())
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
				return err
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
				return err
			}
		case *object.Closure:
				// 创建绑定了 this 的新闭包
				methodClosure := &object.Closure{
					Fn:              callee.Fn,
					Env:             callee.Env,
					This:            thisVal,
					IsArrow:         callee.IsArrow,
					CapturedLocals:  callee.CapturedLocals,
				}
				// generator 方法调用: 创建 Generator (this 绑定保留在闭包中)
				if methodClosure.Fn != nil && methodClosure.Fn.IsGenerator {
					vm.stack.Push(object.NewGenerator(methodClosure, args))
				} else {
					if err := vm.callClosure(methodClosure, args); err != nil {
						return err
					}
				}
			case *object.Proxy:
				// 代理方法调用: 转发到 apply trap，this 为 thisVal
				result, err := vm.proxyApply(callee, thisVal, args)
				if err != nil {
					return err
				}
				vm.stack.Push(result)
			default:
				return fmt.Errorf("TypeError: %s is not a function", fn.Inspect())
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
					Fn:     closure.Fn,
					Env:    closure.Env,
					This:   newObj,
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
					return err
				}
			} else if proxy, ok := fn.(*object.Proxy); ok {
				// 代理构造: 转发到 construct trap
				result, err := vm.proxyConstruct(proxy, args)
				if err != nil {
					return err
				}
				vm.stack.Push(result)
			} else {
				return fmt.Errorf("TypeError: %s is not a constructor", fn.Inspect())
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
			// Proxy: 转发到 get trap
			if proxy, ok := obj.(*object.Proxy); ok {
				val, err := vm.proxyGet(proxy, propName, obj)
				if err != nil {
					return err
				}
				vm.stack.Push(val)
				continue
			}
			val, found := obj.GetProperty(propName)
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
			// Proxy: 转发到 set trap
			if proxy, ok := obj.(*object.Proxy); ok {
				if err := vm.proxySet(proxy, propName, val, obj); err != nil {
					return err
				}
				continue
			}
			obj.SetProperty(propName, val)
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
			for i := numExprs - 1; i >= 0; i-- {
				args = append(args, vm.stack.Pop())
			}
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
					return err
				}
			default:
				return fmt.Errorf("TypeError: %s is not a function", fn.Inspect())
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
					return err
				}
				vm.stack.Push(val)
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
					return err
				}
				vm.stack.Push(val)
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
			iter, hasIter := runtime.GetIterable(iterable)
			if !hasIter {
				return fmt.Errorf("TypeError: %s is not iterable", iterable.Inspect())
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
			// operand = 部分数，无需特殊处理
		case bytecode.OP_TEMPLATE_PART:
			// 将栈顶值转为字符串，累积
			// 使用一个简单的策略: 每次将栈顶弹出并暂存
			// TEMPLATE_END 时拼接
			// 实际实现: 用 OP_TEMPLATE_START 时初始化一个 builder
			// 但这里简化为: PART 弹出值并追加到下方的字符串
			val := vm.stack.Pop()
			if vm.stack.Len() > 0 {
				if existing, ok := vm.stack.Peek().(*object.String); ok {
					vm.stack.Pop()
					vm.stack.Push(object.NewString(existing.Value + toJSString(val)))
					continue
				}
			}
			// 如果没有已有的字符串，创建一个
			vm.stack.Push(object.NewString(toJSString(val)))
		case bytecode.OP_TEMPLATE_END:
			// 模板拼接完成，结果已在栈顶
			// 确保栈顶是字符串
			val := vm.stack.Peek()
			if _, ok := val.(*object.String); !ok {
				vm.stack.Pop()
				vm.stack.Push(object.NewString(toJSString(val)))
			}

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
				return fmt.Errorf("TypeError: %s is not iterable", val.Inspect())
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
		case bytecode.OP_TYPEOF:
			val := vm.stack.Pop()
			vm.stack.Push(object.NewString(object.TypeOf(val)))
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
			return nil, err
		}
		// 执行子帧直到返回
		if err := vm.runFrom(startIdx); err != nil {
			return nil, err
		}
		// 返回值在栈顶
		return vm.stack.Pop(), nil
	}
	return nil, fmt.Errorf("TypeError: %s is not a function", fn.Inspect())
}

// checkCallbackErr 检查回调执行中是否产生了错误，如有则返回并清除。
func (vm *VM) checkCallbackErr() error {
	if vm.callbackErr != nil {
		err := vm.callbackErr
		vm.callbackErr = nil
		return err
	}
	return nil
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
	if o, ok := right.(*object.Object); ok {
		ctorProto, _ = o.GetProperty("prototype")
	} else if f, ok := right.(*object.Closure); ok {
		// 闭包构造器: 取 prototype 属性 (new A() 时实例的原型)
		ctorProto, _ = f.GetProperty("prototype")
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
	l := lexer.New(string(source))
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, fmt.Errorf("Module parse error: %s", p.Errors().String())
	}

	c := compiler.New()
	c.SetModuleMode(true) // 模块有自己的命名空间, 顶层变量不写入共享全局环境
	if err := c.Compile(program); err != nil {
		return nil, fmt.Errorf("Module compile error: %v", err)
	}

	// 执行模块
	savedExports := vm.currentExports
	vm.currentExports = &ModuleExports{Named: map[string]object.Value{}}

	// 保存当前模块路径并设置新基准
	savedBase := vm.moduleBase
	vm.moduleBase = dirOf(absPath)

	modVM := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), vm.globals)
	modVM.modules = vm.modules
	modVM.moduleBase = vm.moduleBase
	modVM.currentExports = vm.currentExports
	if err := modVM.RunCompiled(c); err != nil {
		vm.currentExports = savedExports
		vm.moduleBase = savedBase
		return nil, fmt.Errorf("Module execution error: %v", err)
	}

	// 缓存模块
	exports := vm.currentExports
	vm.modules[absPath] = exports

	// 恢复状态
	vm.currentExports = savedExports
	vm.moduleBase = savedBase

	return exports, nil
}

// SetModuleBase 设置模块基准路径。
func (vm *VM) SetModuleBase(path string) {
	vm.moduleBase = path
}

// ===== 辅助方法 =====

// createClosure 从 FunctionMetadata 创建闭包。
// 捕获当前帧的外层局部变量 (slots 0..BaseSlot-1)。
func (vm *VM) createClosure(meta *bytecode.FunctionMetadata, frame *Frame) *object.Closure {
	// 将 FunctionMetadata 转换为 CompiledFunction
	fn := &object.CompiledFunction{
		Instructions:  meta.Instructions,
		NumLocals:     meta.NumLocals,
		NumParameters: meta.NumParameters,
		Name:          meta.Name,
		IsArrow:       meta.IsArrow,
		IsGenerator:   meta.IsGenerator,
		IsAsync:       meta.IsAsync,
		BaseSlot:      meta.BaseSlot,
		ArgumentsSlot: meta.ArgumentsSlot,
		Constants:     frame.Constants.Constants, // 保存当前帧的常量池引用
	}
	// 转换参数信息
	for _, ps := range meta.Parameters {
		fn.Parameters = append(fn.Parameters, object.ParameterInfo{
			Name:    ps.Name,
			Default: ps.HasDefault,
			Rest:    ps.IsRest,
		})
	}

	// 捕获外层局部变量 (slots 0..BaseSlot-1)
	var captured []object.Value
	if meta.BaseSlot > 0 {
		captured = make([]object.Value, meta.BaseSlot)
		for i := 0; i < meta.BaseSlot && i < len(frame.Locals); i++ {
			captured[i] = frame.Locals[i]
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

	// 检查调用栈深度
	if vm.frameIdx >= MaxFrames-1 {
		return fmt.Errorf("RangeError: Maximum call stack size exceeded")
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

	// 复制捕获的外层局部变量
	if len(closure.CapturedLocals) > 0 {
		copy(frame.Locals, closure.CapturedLocals)
	}

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
	} else {
		// 恢复: 重建帧
		if gen.PC >= len(gen.Instructions) {
			gen.Done = true
			return object.UndefinedSingleton, true, nil
		}
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
		// 压入 arg 作为 yield 表达式的值 (栈顶)
		vm.stack.Push(arg)
	}

	prevGen := vm.currentGenerator
	vm.currentGenerator = gen
	err := vm.runFrom(vm.frameIdx)
	vm.currentGenerator = prevGen

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
	if vm.stack.Len() > 0 {
		result = vm.stack.Pop()
	}
	gen.Value = result
	return result, true, nil
}

// addValues 实现 JavaScript 的 + 运算符 (数字加法或字符串拼接)。
func (vm *VM) addValues(a, b object.Value) (object.Value, error) {
	// 字符串拼接
	if aStr, ok := a.(*object.String); ok {
		return object.NewString(aStr.Value + toJSString(b)), nil
	}
	if bStr, ok := b.(*object.String); ok {
		return object.NewString(toJSString(a) + bStr.Value), nil
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
			}
		}
	case *object.Object:
		if s, ok := index.(*object.String); ok {
			o.SetProperty(s.Value, val)
		} else if sym, ok := index.(*object.Symbol); ok {
			o.SetSymbolProperty(sym, val)
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
		if val.Value == "" {
			return 0
		}
		var f float64
		_, err := fmt.Sscanf(val.Value, "%f", &f)
		if err != nil {
			return math.NaN()
		}
		return f
	default:
		return math.NaN()
	}
}

// toJSString 将任意值转换为 JavaScript 字符串表示。
func toJSString(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	return v.Inspect()
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
	}
	return false
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
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, fmt.Errorf("parser errors:\n%s", p.Errors().String())
	}

	c := compiler.New()
	if err := c.Compile(program); err != nil {
		return nil, fmt.Errorf("compiler error: %v", err)
	}

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	if err := vm.RunCompiled(c); err != nil {
		return nil, fmt.Errorf("vm error: %v", err)
	}
	return vm, nil
}

// EvalWithGlobals 使用预设全局变量编译并执行 JS 源码。
func EvalWithGlobals(input string, globals *runtime.Environment) (object.Value, error) {
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, fmt.Errorf("parser errors:\n%s", p.Errors().String())
	}

	c := compiler.New()
	if err := c.Compile(program); err != nil {
		return nil, fmt.Errorf("compiler error: %v", err)
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

	l := lexer.New(string(source))
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		return nil, fmt.Errorf("parser errors:\n%s", p.Errors().String())
	}

	c := compiler.New()
	if err := c.Compile(program); err != nil {
		return nil, fmt.Errorf("compiler error: %v", err)
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
