package vm

import (
	"errors"
	"fmt"

	"github.com/14752222/Gox/object"
)

type ThrowError struct {
	Value object.Value
}

func (e *ThrowError) Error() string {
	return e.Value.Inspect()
}

// thrownDisplayString 渲染顶层**未捕获**抛值的展示文本: 优先按 ToString
// 语义, 对非 Error 对象调用其原型链/自有属性上的**用户自定义** toString
// (JS 闭包) 并取其字符串结果; 缺失、非闭包 (如内建 Object.prototype.toString)、
// 调用抛错、panic 或返回非字符串时, 回退到既有 Inspect (对象字面量) 渲染。
//
//   - Error 实例: 维持 Inspect ("Name: message"), 行为不变;
//   - 原始值 (string/number/...): Inspect 即 ToString 展示, 维持既有;
//   - 用户对象带自定义 toString: 调用之 (Test262Error / {toString(){...}});
//   - 其余 (裸对象等): 回退 Inspect, 不退化成 "[object Object]"。
//
// 渲染路径绝不被用户 toString 带崩: callToString 内恢复 currentVM 并以
// recover 兜底。

func (vm *VM) thrownDisplayString(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	if _, isErr := v.(*object.Error); isErr {
		return v.Inspect()
	}
	obj, ok := v.(*object.Object)
	if !ok {
		return v.Inspect()
	}
	ts, exists := obj.GetProperty("toString")
	if !exists {
		return v.Inspect()
	}
	closure, isClosure := ts.(*object.Closure)
	if !isClosure {
		// 内建 toString (如 Object.prototype.toString → "[object Object]")
		// 不算自定义, 回退 Inspect 以免裸对象丢信息。
		return v.Inspect()
	}
	if s, ok := vm.callToString(closure, obj); ok {
		return s
	}
	return v.Inspect()
}

// callToString 在恢复 currentVM 与 panic 保护下调用用户的 toString(闭包),
// 返回其字符串结果; toString 抛错/panic/返回非字符串 ⇒ ok=false (回退 Inspect)。

func (vm *VM) callToString(fn *object.Closure, this object.Value) (result string, ok bool) {
	// 顶层 execute() 已返回、currentVM 被恢复为旧值; 渲染时要调回调桥
	// (object.CallFunction → currentVM.callFunction) 执行用户 toString,
	// 故在此重新注册 currentVM, 退出时恢复。
	restore := state.swap(vm)
	defer func() {
		restore()
		if r := recover(); r != nil {
			result, ok = "", false
		}
	}()
	rv := object.CallFunction(fn, this)
	if object.TakeCallbackError() != nil {
		// 出错即弃用结果 (回退 Inspect)，但**值槽必须跟着清** (看板 rZNsX9):
		// 本处不重抛、上游也看不到这个错误，只清错误槽的话 `throw x` 的 x 会
		// 一直挂着 —— object.CallFunction 每次只重置错误槽、不重置值槽，于是
		// 它能跨任意多次后续桥调用存活，直到被某个两槽消费者当成"本次抛出的
		// 值"读走 (静默错值)。
		object.TakeCallbackErrorValue()
		return "", false
	}
	if s, isStr := rv.(*object.String); isStr {
		return s.Value, true
	}
	return "", false
}

// uncaughtError 渲染顶层未捕获错误: throw 出的值按 thrownDisplayString
// (ToString 语义 + Inspect 回退) 渲染, 其余错误维持既有形式; 尽力附带源码帧。
// 渲染后的消息使错误**首行**即 ToString(thrownValue) 结果 —— test262 runner
// 对 negative.runtime 的 NegType 匹配 (首行) 由此能见到如 "Test262Error"。

func (vm *VM) uncaughtError(err error) error {
	framed := vm.AttachFrame(err) // 先在有 throw 位置状态时算好源码帧
	frame := ""
	if fe, ok := framed.(*FrameError); ok {
		frame = fe.Frame
	}
	var te *ThrowError
	if errors.As(err, &te) {
		msg := "vm error: " + vm.thrownDisplayString(te.Value)
		if frame != "" {
			return &FrameError{Inner: errors.New(msg), Frame: frame}
		}
		return errors.New(msg)
	}
	return fmt.Errorf("vm error: %v", framed)
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

// pendingCompletionIsThrow 判断「当前正在执行的 finally 体」所服务的挂起
// 完成是否为 **throw 完成**。
//
// 只用于 IteratorClose 的异常取舍 (规范 7.4.6):
//
//  6. If completion.[[type]] is throw, return Completion(completion).
//  7. If innerResult.[[type]] is throw, return Completion(innerResult).
//  8. If Type(innerResult.[[value]]) is not Object, throw a TypeError.
//
// 即 throw 穿出时 close 的错要丢 (步骤 6 早于 7/8), 而 return / break /
// continue 穿出时 close 的错必须传。判据取自栈顶那条 inFinally 条目:
// pendingReturn 为真 ⇒ return 完成; pendingVal 非空且非 pendingReturn ⇒
// throw 完成; 两者皆无 ⇒ break/continue 完成 (非 throw, 要传)。

func (vm *VM) pendingCompletionIsThrow() bool {
	for i := len(vm.tryStack) - 1; i >= 0; i-- {
		e := &vm.tryStack[i]
		if !e.inFinally {
			continue
		}
		return e.pendingVal != nil && !e.pendingReturn
	}
	return false
}

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

func (vm *VM) handleThrow(val object.Value) bool {
	if vm.frameIdx >= 0 && vm.frameIdx < len(vm.frames) && vm.frames[vm.frameIdx] != nil {
		vm.lastThrowPC = vm.currentFrame().PC
		vm.lastThrowFrame = vm.currentFrame()
		vm.hasThrowPC = true
	}
	matched := vm.handleThrowInner(val)
	if matched {
		vm.hasThrowPC = false
	}
	return matched
}

func (vm *VM) handleThrowInner(val object.Value) bool {
	for len(vm.tryStack) > 0 {
		entry := vm.tryStack[len(vm.tryStack)-1]

		// 边界检查: 处理器在外层帧 (低于当前子执行的起始帧) 时不在此处
		// 解退。让异常以 ThrowError 返回给回调桥，由外层的抛出路径
		// (throwIfError/rethrowBridgeError) 在正确的嵌套层级匹配它。
		if entry.frameIdx < vm.throwBoundary {
			return false
		}

		// 防御: 丢弃指向"尚未进入的帧"的死条目。try 块内 return 跳过
		// OP_POP_TRY 会留下 frameIdx 大于当前帧的死条目, 若被 handleThrow
		// 当作处理器消费, 会把当前帧 PC 劫持到已退出帧的 catchPC —— 字节码
		// 跨编译单元串台, 随即栈失衡 panic (T04 panic 家族根因之一)。
		// 死条目在这里被安全丢弃, 继续向外层找真正的处理器。
		if entry.frameIdx > vm.frameIdx {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		// 本条目正在跑自己的 finally: 现在这个异常正是从那个 finally 体里抛出来
		// 并穿出去的 ⇒ 丢弃本条目挂起的旧异常 (新异常覆盖旧异常), 继续向外找。
		// 这就是「finally 里 throw 会替换原异常」的落点; 同时保证不会有挂起值
		// 残留到之后某个 END_FINALLY 上被误重抛。
		if entry.inFinally {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		// 如果 try 条目在不同的帧中，先弹出帧。
		// 栈恢复以帧的 StackBase 为准截断 (与 OP_RETURN 一致) ——
		// 旧的"每帧无条件 Pop 一次"假设帧恰好遗留一个值, 在调用方
		// 实参求值阶段发生异常时会误弹调用方压在栈上的中间值。
		if vm.frameIdx > entry.frameIdx {
			for vm.frameIdx > entry.frameIdx {
				base := vm.frames[vm.frameIdx].StackBase
				vm.popFrame()
				if vm.stack.Len() > base {
					vm.stack.Truncate(base)
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
			// 有 finally 但无 catch: 条目**留在 tryStack 上**并标记 inFinally,
			// 由 pendingVal 承载挂起值, 然后跳进 finally 体。
			entry.inFinally = true
			entry.pendingVal = val
			vm.tryStack = append(vm.tryStack, entry)
			vm.currentFrame().PC = entry.finallyPC
			return true
		}
		// 无 catch 无 finally: 继续向上查找
	}
	return false
}

// handleReturn 把 return 完成沿 try 处理器栈向外传播。
//
// 与 handleThrow 的关键区别: catch 块**不**拦截 return 完成 (规范:
// Completion 为 return 时只展开 finally)。因此这里跳过 catch-only 条目,
// 只对有 finally 的条目进入其 finally 体。返回 true 表示已找到 finally
// 并把 PC 劫持到 finallyPC。

func (vm *VM) handleReturn(val object.Value) bool {
	return vm.handleReturnInner(val)
}

func (vm *VM) handleReturnInner(val object.Value) bool {
	for len(vm.tryStack) > 0 {
		entry := vm.tryStack[len(vm.tryStack)-1]

		// 与 handleThrowInner 同款边界/死条目防御 (见其注释)。
		if entry.frameIdx < vm.throwBoundary {
			return false
		}
		if entry.frameIdx > vm.frameIdx {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}
		// 已在自身 finally 体内: return 从 finally 里穿出 ⇒ 丢弃挂起完成, 继续向外。
		if entry.inFinally {
			vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]
			continue
		}

		if vm.frameIdx > entry.frameIdx {
			for vm.frameIdx > entry.frameIdx {
				base := vm.frames[vm.frameIdx].StackBase
				vm.popFrame()
				if vm.stack.Len() > base {
					vm.stack.Truncate(base)
				}
			}
		}

		vm.tryStack = vm.tryStack[:len(vm.tryStack)-1]

		for vm.stack.Len() > entry.stackBase {
			vm.stack.Pop()
		}

		// return 完成拦截 (yield* 异步委托): 把 return 值压栈并劫持 PC 到
		// 委托的 return 处理段。条目已被弹出 —— 与 catchPC 拦截 throw 对称。
		if entry.catchReturnPC > 0 {
			vm.stack.Push(val)
			vm.currentFrame().PC = entry.catchReturnPC
			return true
		}

		if entry.finallyPC > 0 {
			entry.inFinally = true
			entry.pendingReturn = true
			entry.pendingVal = val
			vm.tryStack = append(vm.tryStack, entry)
			vm.currentFrame().PC = entry.finallyPC
			return true
		}
		// 无 finally: return 完成不被 catch 捕获, 继续向外展开。
	}
	return false
}

// ensureCurrentExports 返回当前模块的导出表, 必要时惰性创建。
// 导出指令 (OP_EXPORT 系列) 统一走它, 避免各处重复判空。

func errorToJSValue(err error) object.Value {
	if te, ok := err.(*ThrowError); ok {
		return te.Value
	}
	if jt, ok := err.(*jsThrow); ok {
		return object.NewErrorWithName(jt.Name, jt.Message)
	}
	if err == nil {
		return object.UndefinedSingleton
	}
	return object.NewErrorWithName("Error", err.Error())
}

// SetModuleBase 设置模块基准路径。

func (vm *VM) throwJSErrorValue(err error) object.Value {
	var jt *jsThrow
	if errors.As(err, &jt) {
		return object.NewErrorWithName(jt.Name, jt.Message)
	}
	return nil
}

// 语义 (规范 7.3.25): 新建对象, 复制 source 的自有可枚举属性 (字符串键 + Symbol
// 键, 按 OrdinaryOwnPropertyKeys 顺序, 触发 getter), 跳过 excluded 中的键。
// 非对象源 (Number/Boolean/Symbol/BigInt) 得空对象; 字符串按 UTF-16 索引字符复制。

var errHandledThrow = errors.New("vm: throw handled")
