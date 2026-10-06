package object

import "errors"

// 编译桥 (依赖反转)。
//
// stdlib 为实现 eval 与 new Function 需要把 JS 源码编译成可执行函数，
// 但 stdlib 不应依赖 lexer/parser/compiler 前端包 —— 否则标准库被钉死在
// 完整工具链上。这里沿用 SetCallFunction (callback.go) 的注册模式:
// vm 包初始化时注册真正的编译实现, stdlib 经 object 包这一中转调用,
// 依赖方向保持 object ← stdlib、object ← vm。
//
// 未链接 vm 的宿主调用 CompileSource 会得到明确错误, 与 CallFunction
// 未注册时的行为同类。

// CompileSourceFunc 编译 JS 源码, 返回顶层包装函数。
// 返回的 CompiledFunction 内嵌编译产物常量池, 调用方只需再补上 Env
// 组成 Closure 即可执行 (见 stdlib 的 runGlobalEval / newDynamicFunction)。
type CompileSourceFunc func(src string) (*CompiledFunction, error)

var compileSourceHook CompileSourceFunc

// SetCompileSource 注册编译实现, 由 vm 包在初始化时调用。
func SetCompileSource(f CompileSourceFunc) {
	compileSourceHook = f
}

// CompileSource 编译源码为顶层函数。
func CompileSource(src string) (*CompiledFunction, error) {
	if compileSourceHook == nil {
		return nil, errors.New("CompileSource: compile hook not registered (vm package not linked)")
	}
	return compileSourceHook(src)
}

// directEvalThis 记录最近一次「直接 eval 调用」的上下文 —— 调用者帧生效的
// thisValue 与调用者是否处于严格模式。规范 sec-performeval:
//   - direct eval 的 this 绑定与调用者一致;
//   - eval 代码的严格性 = 调用者严格 OR 源码含 "use strict" 指令
//     (strictCaller 为真时, eval 代码恒严格, thisValue 原样不归一)。
//
// 为什么需要跨层传: eval 内建在 stdlib, 但"调用者帧的 this 与其严格性"只有
// VM 知道。VM 在 OP_CALL/OP_CALL_SPREAD 分派前 (确认被调恰为全局 %eval% 且
// 本次调用由编译器标为直接 eval) 写入此处, eval 内建在 runGlobalEval 里消费。
//
// has 与 value 分开: this 可能是 undefined (如严格调用者的 thisValue), 不能
// 用"值是否为 nil/undefined"来推断"有没有记录"。消费即清除, 避免泄漏给后续
// 无关的 eval 调用 (与 pendingDirectEvalInit 同纪律)。
var (
	directEvalThis    Value
	directEvalStrict  bool
	hasDirectEvalThis bool
)

// SetDirectEvalThis 由 vm 在确认「本次调用是直接 eval」后调用, 写入调用者帧
// 的 this 与严格性。stdlib 的 eval 内建随后经 TakeDirectEvalThis 消费。
func SetDirectEvalThis(v Value, callerStrict bool) {
	directEvalThis = v
	directEvalStrict = callerStrict
	hasDirectEvalThis = true
}

// TakeDirectEvalThis 取出并清除直接 eval 的上下文。ok 为 false 表示本次 eval
// 不是直接 eval (间接 eval / 从 Go 侧直接调用), 调用方应按全局 eval 处理
// (this = globalThis, 代码严格性只看源码指令)。
func TakeDirectEvalThis() (this Value, callerStrict bool, ok bool) {
	if !hasDirectEvalThis {
		return UndefinedSingleton, false, false
	}
	v, s := directEvalThis, directEvalStrict
	directEvalThis = nil
	directEvalStrict = false
	hasDirectEvalThis = false
	return v, s, true
}
