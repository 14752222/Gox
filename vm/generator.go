package vm

import (
	"github.com/14752222/Gox/bytecode"
	"github.com/14752222/Gox/object"
)

func (vm *VM) driveMainUnit(gen *object.Generator) error {
	val, done, err := vm.genResume(gen, object.UndefinedSingleton)
	for !done {
		if err != nil {
			return err
		}
		res, thrown := vm.settleTopLevelAwait(val)
		if thrown != nil {
			// await 的 Promise 被 reject: 作为异常抛回生成器 (体内 try/catch 可捕获)
			val, done, err = vm.genThrow(gen, thrown)
		} else {
			val, done, err = vm.genResume(gen, res)
		}
	}
	savedPopped := vm.lastPopped
	vm.drainMicrotasks()
	vm.lastPopped = savedPopped
	return err
}

// settleTopLevelAwait 结算一个顶层 await 的操作数, 返回 (resolved, thrown)。
// thrown 非 nil 表示该值是被 reject 的 Promise。
//
// 非 Promise 原样透传; 已结算 Promise 由 Then/Catch 回调同步给出值 (Gox
// 同步 Promise 模型); pending Promise 不支持 (见 RunCompiledAsync 说明)。

func (vm *VM) settleTopLevelAwait(val object.Value) (object.Value, object.Value) {
	p, ok := val.(*object.Promise)
	if !ok {
		return val, nil // 非 Promise: await 原样透传
	}
	var got bool
	var resolved object.Value
	var thrown object.Value
	p.Then(object.NewBuiltin("__tla_ok", func(args ...object.Value) object.Value {
		got = true
		if len(args) > 0 {
			resolved = args[0]
		} else {
			resolved = object.UndefinedSingleton
		}
		return object.UndefinedSingleton
	}))
	p.Catch(object.NewBuiltin("__tla_err", func(args ...object.Value) object.Value {
		got = true
		if len(args) > 0 {
			thrown = args[0]
		} else {
			thrown = object.UndefinedSingleton
		}
		return object.UndefinedSingleton
	}))
	// 已结算 promise 的 then 回调是微任务: 立刻 drain 取出结算值,
	// 维持"TLA 只支持同步可结算 promise"的本版口径。
	vm.drainMicrotasks()
	if !got {
		return object.UndefinedSingleton,
			object.NewErrorWithName("Error", "top-level await: promise did not settle synchronously")
	}
	if thrown != nil {
		return object.UndefinedSingleton, thrown
	}
	return resolved, nil
}

// execute 设置当前 VM 并从主帧开始执行。

func genHasPrologue(fn *object.CompiledFunction) bool {
	return fn != nil && fn.IsGenerator && !fn.DeferParams && fn.ParamPrologueEnd > 0
}

// setupGenPrologueFrame 为需要同步绑定形参的生成器建立前导帧。
// callClosure 先把实参放入形参槽位, 前导段 (默认值/解构) 由主循环执行;
// 执行到 StopPC 时 runFrom 会调用 freezeGenPrologue 冻结为 Generator。
// 前导段抛出的异常沿正常抛出流程传播 (生成器形参错误是**同步抛出**)。

func (vm *VM) setupGenPrologueFrame(closure *object.Closure, args []object.Value) error {
	if err := vm.callClosure(closure, args); err != nil {
		return err
	}
	frame := vm.currentFrame()
	frame.PendingGen = object.NewGenerator(closure, args)
	frame.StopPC = closure.Fn.ParamPrologueEnd
	return nil
}

// freezeGenPrologue 把形参前导帧冻结为 Generator:
// 保存帧状态 (PC/Locals/Instructions/Constants) 到 Generator, 弹出前导帧,
// 并把 Generator 放到调用点的结果槽位 (帧基处)。

func (vm *VM) freezeGenPrologue(frame *Frame) {
	gen := frame.PendingGen
	gen.PC = frame.PC
	gen.Locals = frame.Locals
	gen.Instructions = frame.Instructions
	gen.Constants = frame.Constants.Constants
	gen.SavedStack = nil
	gen.PendingTries = nil
	gen.PrologueBound = true

	base := frame.StackBase
	vm.popFrame()
	if vm.stack.Len() > base {
		vm.stack.Truncate(base)
	}
	vm.stack.Push(gen)
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
		// 形参前导段已在调用时执行完毕: 直接重建帧从函数体起始处恢复,
		// 不压入 arg (首次 next 的实参被忽略)。
		if gen.PrologueBound {
			gen.Started = true
			gen.PrologueBound = false
			if gen.PC >= len(gen.Instructions) {
				gen.Done = true
				return object.UndefinedSingleton, true, nil
			}
			vm.rebuildGenFrame(gen, true)
			return vm.runSuspendedGen(gen)
		}
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
	vm.rebuildGenFrame(gen, false)
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
	genFrame := vm.rebuildGenFrame(gen, false)
	genFrameIdx := vm.frameIdx
	genStackBase := genFrame.StackBase

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
		// rebuildGenFrame 刚压入的帧必须回收: 否则它留在帧栈上压住
		// 外层 async wrapper 帧, wrapper 的 OP_RETURN 会在错误栈基上
		// 取值 ⇒ async 调用返回函数体的值而不是 Promise (rZn4IS)。
		// 注意顺序: 先取 frame/索引再 handleThrow, 因为 handleThrow
		// 可能已经把 vm.frameIdx 降到更低。
		vm.unwindGenFrame(genStackBase, genFrameIdx)
		return object.UndefinedSingleton, true, &ThrowError{Value: throwVal}
	}

	return vm.runSuspendedGen(gen)
}

// genReturn 把 returnVal 作为 return 完成注入暂停在 yield 点的 generator。
// async generator 的 return() 走这里: 展开体内 finally 后返回 (returnVal, true)。
// 尚未启动或体内无 finally 时直接关闭, 等价规范中 suspendedStart/completed
// 状态下 return 立即结算 {value: returnVal, done: true}。

func (vm *VM) genReturn(gen *object.Generator, returnVal object.Value) (object.Value, bool, error) {
	if gen.Done {
		return returnVal, true, nil
	}
	if !gen.Started {
		gen.Done = true
		return returnVal, true, nil
	}
	if gen.PC >= len(gen.Instructions) {
		gen.Done = true
		return returnVal, true, nil
	}
	// 需要重建帧执行收尾的条件: 体内挂有 finally, 或挂有 yield* 异步委托的
	// return 拦截 PC (委托期 return(v) 必须转发给被委托迭代器)。否则直接关闭。
	hasFinally := false
	for _, te := range gen.PendingTries {
		if te.FinallyPC > 0 || te.CatchReturnPC > 0 {
			hasFinally = true
			break
		}
	}
	if !hasFinally {
		gen.Done = true
		return returnVal, true, nil
	}
	genFrame := vm.rebuildGenFrame(gen, false)
	genFrameIdx := vm.frameIdx
	genStackBase := genFrame.StackBase
	if !vm.handleReturn(returnVal) {
		gen.Done = true
		vm.unwindGenFrame(genStackBase, genFrameIdx)
		return returnVal, true, nil
	}
	return vm.runSuspendedGen(gen)
}

// rebuildGenFrame 重建 generator 暂停时保存的帧 (含恢复 try 处理器条目)。
//
// 帧基必须在压入 SavedStack **之前**取当前栈高: SavedStack 是挂起时
// 「帧基之上」的中间值 (如 2 + (yield 3) 中的 2), 恢复时它们仍应位于
// 帧基之上。若像早期实现那样在压入之后再取栈高 (帧基被抬到 SavedStack
// 之上), 则 SavedStack 落到帧基之下, 一旦上层表达式把残留值消费掉、栈
// 降回帧基之下, try 条目的绝对 stackBase 就会小于帧基 —— async generator
// 在求值外层表达式时恢复生成器 (操作数栈带中间值) 的场景会因此越界崩溃。
//
// reuseLocals 为真时直接复用 gen.Locals (不拷贝): 形参前导段在**调用时**
// 由独立的"前导帧"执行, 该帧里默认值表达式创建的闭包捕获的是前导帧
// Locals 的切片 (= gen.Locals)。若此处拷贝成新数组, 函数体对形参的 STORE
// 只会写进副本, 前导段闭包仍读到旧数组 —— `function* g(a, f = () => a)
// { a = 6; yield f() }` 会错读到形参初值。复用同一数组使这些闭包与函数体
// 共享绑定, 与"前导段与函数体同一帧"的语义一致。

func (vm *VM) rebuildGenFrame(gen *object.Generator, reuseLocals bool) *Frame {
	// 主单元生成器 (模块顶层 TLA, Closure==nil): 全程复用同一份 Locals
	// 数组。顶层导出绑定 (OP_EXPORT_BINDING) 的读取器持有该数组引用,
	// 恢复时若拷贝成新数组, await 之后的赋值对导出方不可见。
	if gen.Closure == nil {
		reuseLocals = true
	}
	// 帧基 = 当前栈高 (SavedStack 之前的顶端)。必须先取再压 SavedStack:
	// SavedStack 是挂起时「帧基之上」的中间值 (如 2 + (yield 3) 中的 2),
	// 恢复后仍应位于帧基之上; 若压入后再取栈高, 帧基被抬到 SavedStack 之上,
	// 这些值就落到帧基之下 —— 生成器 return/catch 时按帧基截断会把它们
	// 留给调用方 (栈残留), 且其绝对 stackBase 与 try 条目错位可致下溢。
	stackBase := vm.stack.Len()
	// 压入保存的帧栈残留值 (顺序与保存时一致), 位于帧基之上
	for _, v := range gen.SavedStack {
		vm.stack.Push(v)
	}
	locals := gen.Locals
	if !reuseLocals {
		locals = make([]object.Value, len(gen.Locals))
		copy(locals, gen.Locals)
	}
	frame := &Frame{
		Instructions: gen.Instructions,
		PC:           gen.PC,
		Locals:       locals,
		Closure:      gen.Closure,
		This:         vm.resolveFrameThis(gen.Closure),
		Constants:    &bytecode.ConstantPool{Constants: gen.Constants},
		StackBase:    stackBase,
	}
	if reuseLocals {
		// 复用同一数组 ⇒ 把它同时作为 binding cell, 使前导段闭包与
		// 函数体的 STORE 走同一份存储 (见函数头注释)。
		frame.SharedCells = gen.Locals
	}
	vm.pushFrame(frame)
	// 把 yield 时保存的 try 处理器条目按相对值换算后重新挂回
	for _, te := range gen.PendingTries {
		vm.tryStack = append(vm.tryStack, tryEntry{
			catchPC:       te.CatchPC,
			finallyPC:     te.FinallyPC,
			catchReturnPC: te.CatchReturnPC,
			stackBase:     frame.StackBase + te.RelStackBase,
			frameIdx:      vm.frameIdx + te.RelFrameIdx,
			inFinally:     te.InFinally,
			pendingVal:    te.PendingVal,
			pendingReturn: te.PendingReturn,
		})
	}
	gen.PendingTries = nil
	return frame
}

// runSuspendedGen 运行已重建帧的 generator 直到下一个 yield 或结束。
//

func (vm *VM) runSuspendedGen(gen *object.Generator) (object.Value, bool, error) {
	genFrameIdx := vm.frameIdx
	frameStackBase := vm.currentFrame().StackBase
	prevGen := vm.currentGenerator
	vm.currentGenerator = gen
	err := vm.runFrom(genFrameIdx)
	vm.currentGenerator = prevGen
	return vm.finishGenRun(gen, frameStackBase, genFrameIdx, err)
}

// finishGenRun 处理 generator 一次恢复运行的收尾。
// frameStackBase 是 generator 帧入栈时的栈基 —— 必须在 runFrom 之前捕获:
// 正常结束路径的 popFrame 已把帧回收进复用池, 此后再读 frame.StackBase
// 读到的是清零后的值 (T2-4 帧池化)。

func (vm *VM) finishGenRun(gen *object.Generator, frameStackBase, genFrameIdx int, err error) (object.Value, bool, error) {
	if err != nil {
		if ysig, ok := err.(*YieldSignal); ok {
			gen.Value = ysig.value
			return ysig.value, false, nil
		}
		// 其他错误: generator 终止。
		//
		// 必须把 generator 帧弹掉。runLoop 因异常返回时该帧仍留在帧栈上
		// (只有跑到边界的正常路径才会自然 popFrame), 而外层 async 的
		// wrapper 帧正等着执行 OP_RETURN 把 __spawn 交出的 promise 返回给
		// 调用方。帧泄漏会让 wrapper 帧被压住、OP_RETURN 在错误栈基上取值
		// ⇒ async 调用返回 undefined 而不是 Promise (2026-10-02, rZn4IS)。
		gen.Done = true
		vm.unwindGenFrame(frameStackBase, genFrameIdx)
		return object.UndefinedSingleton, true, err
	}

	// 正常结束: 弹出返回值
	gen.Done = true
	var result object.Value = object.UndefinedSingleton
	if vm.stack.Len() > frameStackBase {
		result = vm.stack.Pop()
	}
	gen.Value = result
	return result, true, nil
}

// unwindGenFrame 回收因异常终止而仍留在帧栈上的 generator 帧。
//
// 只弹到 generator 自己那一帧为止 (含), 绝不动外层帧 —— 外层的 async
// wrapper 还要继续执行 OP_RETURN。栈按 generator 帧的 StackBase 截断,
// 与 OP_RETURN 的收尾语义一致。stackBase 由调用方在弹帧前捕获 (T2-4
// 帧池化后, 弹出的帧立即回收, 不可再读其字段)。

func (vm *VM) unwindGenFrame(stackBase, genFrameIdx int) {
	for vm.frameIdx > genFrameIdx {
		vm.popFrame()
	}
	if vm.frameIdx == genFrameIdx {
		vm.popFrame()
	}
	if vm.stack.Len() > stackBase {
		vm.stack.Truncate(stackBase)
	}
}

// addValues 实现 JavaScript 的 + 运算符 (数字加法或字符串拼接)。
// maxStringLength 是 VM 侧字符串长度上限 (与 stdlib.maxStringLength 一致，
// 1<<30 字节)。字符串拼接无上限时，`s = s + s` 翻倍可在数秒内请求到
// TB 级分配，Go 的 OOM 是不可 recover 的致命错误 —— 必须在拼接前拦截。
