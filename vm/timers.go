package vm

import (
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

const minPumpWait = time.Millisecond

// ThrowError 包装 JS throw 抛出的值，用于在 Go 错误返回链中传递。

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
	defer state.swap(vm)()

	return vm.runProtected(func() error { return vm.runTimersLoop(until, pump) })
}

// runProtected 在 defer recover 中执行 fn。
// VM/stdlib 某处缺陷导致的 Go panic 不再直接杀死整个进程，而是转为
// InternalError 结束当前执行。注意: panic 后 VM 状态可能已损坏，
// 恢复出的错误必须向外传播、不能吞掉后继续用同一 VM 跑。

func (vm *VM) runProtected(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			// GOX_PANIC_TRACE=1 时输出 Go 调用栈: VM 内部的越界/空指针
			// 是引擎缺陷, 栈是定位它的第一手资料 (T04 修复辅助)。
			if os.Getenv("GOX_PANIC_TRACE") != "" {
				fmt.Fprintln(os.Stderr, "=== GOX panic trace ===")
				fmt.Fprintf(os.Stderr, "vm state: op=%v pc=%d frames=%d stackDepth=%d\n",
					vm.dbgOp, vm.dbgPC, vm.dbgFrames, vm.stack.Len())
				// 反汇编 panic 帧附近指令窗口: PC 已越过当前指令 3 字节。
				if vm.frameIdx >= 0 && vm.frames[vm.frameIdx] != nil {
					fi := vm.frames[vm.frameIdx]
					start := vm.dbgPC - 10*bytecode.InstructionSize
					if start < 0 {
						start = 0
					}
					end := vm.dbgPC + 6*bytecode.InstructionSize
					if end > len(fi.Instructions) {
						end = len(fi.Instructions)
					}
					fmt.Fprintf(os.Stderr, "--- frame#%d instructions around pc=%d ---\n", vm.frameIdx, vm.dbgPC)
					for pc := start; pc < end; pc += bytecode.InstructionSize {
						op := bytecode.ReadOpcode(fi.Instructions, pc)
						mark := "   "
						if pc == vm.dbgPC {
							mark = ">> "
						}
						fmt.Fprintf(os.Stderr, "%s%04d  %-18s %d\n", mark, pc, op.Name(), bytecode.ReadOperand(fi.Instructions, pc+1))
					}
					for i := 0; i <= vm.frameIdx; i++ {
						if fr := vm.frames[i]; fr != nil {
							fmt.Fprintf(os.Stderr, "frame#%d StackBase=%d locals=%d\n", i, fr.StackBase, len(fr.Locals))
						}
					}
					fmt.Fprintf(os.Stderr, "tryStack=%d entries:", len(vm.tryStack))
					for _, te := range vm.tryStack {
						fmt.Fprintf(os.Stderr, " {catch:%d frame:%d base:%d}", te.catchPC, te.frameIdx, te.stackBase)
					}
					fmt.Fprintln(os.Stderr)
				}
				debug.PrintStack()
			}
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
		// 微任务检查点: 上一轮宏任务 (定时器/空闲/严格回调, 或 GUI pump
		// 派发的外部事件) 产生的 Promise Jobs 在下一个宏任务前全部 drain;
		// 也兜底"无定时器但队列非空"的退出口 (drain 期间新入队的同轮消费)。
		vm.drainMicrotasks()

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
			//
			// **wait==0 不能原样交给 pump** (看板 rczZT2): wait==0 在这条分支上
			// 的含义是"已经有定时器到期了, 别等", 而 gfx.Pump 的契约把
			// maxWait<=0 解释成"无限期等待外部事件" —— 真机后端 (X11/win32) 收到
			// 0 就永久睡进 WaitEvents, 于是**那个到期的定时器永远派发不出去**:
			// 界面僵住、后续定时器全部停摆, 且没有任何报错。
			//
			// 触发它只需要一个 0ms 定时器: 异步 fs / fetch 的回调都是
			// SetTimeout(..., 0) 注册的 (见 stdlib/fs.go 的 fsSchedule), 所以
			// "GUI + 定时器 + 异步 I/O" 这个组合一撞上就是必现。钳到一个最小
			// 正等待: 既保住"马上交回事件循环", 又不会让等外部事件的那条路
			// (上面 !found 分支刻意传 0 表示无限期) 被误伤 —— 那条路必须
			// 原样保留, 否则 GUI 空闲期会退化成忙轮询。
			if wait <= 0 {
				wait = minPumpWait
			}
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
