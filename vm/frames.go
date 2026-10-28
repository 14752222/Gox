package vm

import (
	"os"

	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

const MaxFrames = 2048

// stackTraceOn: GOX_TRACE_STACK=1 时逐指令打印 op/pc/栈深 (T04 栈失衡定位)。

var stackTraceOn = os.Getenv("GOX_TRACE_STACK") == "1"

// minPumpWait 是 GUI 模式下"有定时器已到期"时交给 pump 的最小等待 (看板
// rczZT2)。gfx.Pump 把 maxWait<=0 当成"无限期等待外部事件", 真机后端据此会
// 永久睡进 WaitEvents —— 于是那个已到期的定时器再也派发不出去 (界面僵住、
// 后续定时器停摆、无任何报错)。这里必须给一个正等待, 让泵马上交回事件循环。
// 1ms 的代价是每轮最多睡 1ms, 比"永久阻塞"便宜得多; 也不影响空闲期那条
// 刻意传 0 (无限期) 的路径。

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

	// 同步解退本帧注册的 try 处理器: try 块内 return / 异常透传等
	// 路径会跳过 OP_POP_TRY, 残留的死条目会在后续 throw 时被
	// handleThrow 消费 —— 把调用帧的 PC 劫持到已退出帧的 catchPC,
	// 字节码跨编译单元串台, 随即栈失衡 panic。
	for len(vm.tryStack) > 0 && vm.tryStack[len(vm.tryStack)-1].frameIdx > vm.frameIdx {
		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
	}

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

	// 帧回收 (T2-4 热点优化): Frame 结构体出栈后无任何长生命周期持有者,
	// 清空引用字段后入池复用。Locals 置 nil 释放底层数组 (可能被逃逸闭包
	// 持有为 binding cell, 绝不能随帧复用)。
	// pinned 帧 (构造调用方在弹栈后还要读 This, 见 Frame.pinned) 不入池。
	if f.pinned {
		f.pinned = false
		return f
	}
	f.Instructions = nil
	f.Locals = nil
	f.Closure = nil
	f.This = nil
	f.NewTarget = nil
	f.Constants = nil
	f.ModifiedSlots = nil
	f.SharedCells = nil
	f.CreatedClosures = f.CreatedClosures[:0]
	f.Epoch = 0
	f.SealMarks = f.SealMarks[:0]
	f.StackBase = 0
	f.PendingGen = nil
	f.StopPC = 0
	vm.frameFree = append(vm.frameFree, f)

	return f
}

// acquireFrame 从复用池取帧 (无则新建)。仅重置 VM 调用时必然覆写的三个
// 字段; 其余字段已由 recycle (popFrame) 清空, 新建帧零值天然满足。
// 注意: Locals 每次都新分配 —— 逃逸闭包会把 frame.Locals 当 binding cell
// 持有, 复用会串值; 且零值 nil 承载 TDZ 语义。
func (vm *VM) acquireFrame(ins bytecode.Instructions, constants *bytecode.ConstantPool, numLocals int) *Frame {
	var f *Frame
	if n := len(vm.frameFree); n > 0 {
		f = vm.frameFree[n-1]
		vm.frameFree[n-1] = nil
		vm.frameFree = vm.frameFree[:n-1]
		f.Instructions = ins
		f.PC = 0
		f.Constants = constants
		f.Locals = make([]object.Value, numLocals)
		return f
	}
	return NewFrame(ins, constants, numLocals)
}

// Run 启动 VM 执行循环。

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
//
// **两槽必须同步消费** (看板 rZNsX9): 回调桥写两个槽 —— 错误槽 (Go error)
// 与值槽 (`throw x` 的 x，object.SetCallbackErrorValue)。桥**只在错误类型是
// *ThrowError / *jsThrow 时才写值槽** (见 setCallbackErrorValueFromThrow)，
// 其余错误只写错误槽 ⇒ 只消费错误槽时，上一次的抛出值会原封不动留在值槽里。
//
// 而 object.CallFunction 每次进入只重置**错误槽** (callbackError = nil)、
// **不重置值槽** —— 残留值于是能跨任意多次后续桥调用存活，直到某个「只写
// 错误槽」的桥失败被两槽消费者 (stdlib/{async,promise,eval,solid}.go) 读走
// ⇒ 用户 catch 到的是**上一次的抛出值**: 不报错，只是值错 (静默错值)。
//
// 这里刻意**只在真的消费到错误时才连带消费值槽**: err == nil 说明本次桥调用
// 没有失败，值槽里的东西不由本次负责，抢先清空会夺走尚未被真正的消费方读走
// 的值。本函数是 vm 侧全部同形态位点的**唯一汇聚点**：vm.go 28 处 +
// async_from_sync.go 3 处 = 31 处调用点，改一处即全覆盖 —— 这是 rr1O8P
// 记下的流程教训（先找汇聚点，再改调用点）。

func (vm *VM) globalThisValue() object.Value {
	if g, ok := vm.globals.Get("globalThis"); ok {
		return g
	}
	return object.UndefinedSingleton
}

// normalizedThis 实现 sloppy 模式的 this 归一: 非箭头函数被以 undefined/null
// (含"无接收者的裸调用"——等价于 undefined 接收者) 调用时, this 替换为
// globalThis; 有真实接收者 (对象 / 构造实例 / 基类 this) 原样返回。
//
// Gox 目前没有 strict 模式, 一律按 sloppy 处理 (不引入 strict 分支)。

func (vm *VM) normalizedThis(t object.Value) object.Value {
	if t == nil || t == object.UndefinedSingleton || t == object.NullSingleton {
		return vm.globalThisValue()
	}
	return t
}

// resolveFrameThis 由被调闭包算出该帧生效的 this:
//   - 箭头函数: 词法绑定, 取闭包携带的 This (创建时从所在帧捕获);
//   - 严格函数: 原样保留 (undefined/null 不归一 —— 规范 strict 语义);
//   - 非箭头 sloppy 函数: 按 sloppy 归一 (undefined/null → globalThis)。

func (vm *VM) resolveFrameThis(closure *object.Closure) object.Value {
	if closure == nil {
		return nil
	}
	if closure.IsArrow {
		return closure.This
	}
	if closure.Fn != nil && closure.Fn.IsStrict {
		return closure.This
	}
	return vm.normalizedThis(closure.This)
}

// frameThis 求"当前帧的 this", 供箭头函数在创建时做词法捕获。
// 优先取帧上已归一的 This; 主帧再按 script(globalThis)/module(undefined) 区分。

func (vm *VM) frameThis(frame *Frame) object.Value {
	if frame.This != nil {
		return frame.This
	}
	if frame.Closure == nil {
		if vm.moduleMode {
			return object.UndefinedSingleton
		}
		return vm.globalThisValue()
	}
	if frame.Closure.This != nil {
		return frame.Closure.This
	}
	return object.UndefinedSingleton
}

// frameNewTarget 求"当前帧的 new.target", 供箭头函数在创建时做词法捕获。
// 帧上已归一的 NewTarget 优先; 主帧 (frame.Closure == nil, script/module 顶层)
// 语法上不允许 new.target, 但兜底返回 undefined。普通函数帧的 NewTarget 恒非
// nil (callClosure 装配时写入), 故这里返回的非 nil 值可直接作为捕获结果。

func (vm *VM) frameNewTarget(frame *Frame) object.Value {
	if frame != nil && frame.NewTarget != nil {
		return frame.NewTarget
	}
	return object.UndefinedSingleton
}

// ===== with 语句运行时支持 (对象环境记录) =====

// withRefOf 从常量池取出 OP_WITH_* 的操作数所指的 *WithRef。

func (vm *VM) storeLocalSlot(frame *Frame, slot int, val object.Value) {
	for len(frame.Locals) <= slot {
		frame.Locals = append(frame.Locals, object.UndefinedSingleton)
	}
	frame.Locals[slot] = val
	if slot < len(frame.SharedCells) {
		frame.SharedCells[slot] = val
	}
	if frame.Closure != nil && slot < len(frame.Closure.CapturedLocals) {
		frame.Closure.CapturedLocals[slot] = val
	}
	floor := frame.cellFloor(slot)
	for _, cl := range frame.CreatedClosures {
		if floor >= 0 && cl.IterEpoch < floor {
			continue
		}
		if slot < len(cl.CapturedLocals) {
			cl.CapturedLocals[slot] = val
		}
	}
	if frame.ModifiedSlots == nil {
		frame.ModifiedSlots = make(map[int]bool)
	}
	frame.ModifiedSlots[slot] = true
}

// createClosure 从 FunctionMetadata 创建闭包。
// 捕获当前帧的外层局部变量 (slots 0..BaseSlot-1)。

func (vm *VM) setLocalSlot(slot int, v object.Value) {
	if slot < 0 {
		return
	}
	f := vm.currentFrame()
	for len(f.Locals) <= slot {
		f.Locals = append(f.Locals, object.UndefinedSingleton)
	}
	if v == nil {
		v = object.UndefinedSingleton
	}
	f.Locals[slot] = v
}

// getLocalSlot 读取当前帧的局部槽 (越界返回 undefined)。

func (vm *VM) getLocalSlot(slot int) object.Value {
	f := vm.currentFrame()
	if slot < 0 || slot >= len(f.Locals) || f.Locals[slot] == nil {
		return object.UndefinedSingleton
	}
	return f.Locals[slot]
}

// disposeExit 实现 DisposeResources (规范): 逆序释放 scope 里的全部资源,
// 每个资源以 ResourceValue 为 this 调用其方法。
//
// 错误合成口径 (与本帧挂起异常的关系):
//   - 若释放方法抛错, 且当前帧正**带着挂起异常**跑 finally (tryStack 栈顶
//     是本帧的 inFinally 条目), 则把新错误与原挂起异常合成 SuppressedError
//     (error = 新错误, suppressed = 原挂起异常), 并写回条目 pendingVal ——
//     随后 OP_END_FINALLY 会重抛它;
//   - 若无挂起异常, 释放方法抛错直接向外传播 (替换正常/return 完成)。
//
// 返回值非 nil 表示需要抛出的异常 (仅无挂起异常的情况)。
