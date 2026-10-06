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

// EvalSuperHome 描述「直接 eval 调用点的 super home 上下文」(看板 roiE5Z)。
// 规范 PerformEval 18.2.1.1.1: eval 源码含 SuperProperty (super.x) 只在
// 调用者函数有 [[HomeObject]] 时合法。Gox 的 super 靠编译期静态名解析,
// eval 单元是独立编译单元, 拿不到外层函数的 home object —— 故由编译器在
// 直接 eval 调用点把 home 上下文随 OP_EVAL_MARK 家族的指令带出, VM 经本
// 桥传给 stdlib 的 eval 内建, 再随编译选项进入 eval 单元的编译。
//
// 字段语义 (与编译器侧的 home 上下文一致):
//   - Name:     home 所在类名。instance 语境 super base =
//     Object.getPrototypeOf(<Name>.prototype); 无 extends 的基类也能解析
//     (落到 Object.prototype)。
//   - Static:   home 是类本身 (static 方法 / 静态初始化块) 而非 prototype。
//   - ThisHome: home 是对象字面量方法宿主 —— 没有可加载的名字, 运行期取
//     eval 包装函数的 this (调用者帧的 this) 作 home。近似成立条件:
//     接收者即宿主 (o.m() 直接调用); o.m.call(x) 会读错宿主 (规范仍读 o)。
//   - SuperName: 仅 Static 且类有 extends 时非空: Gox 未链接 ctor.__proto__,
//     静态 super base 只能是父类构造器本身 (LOAD_GIAL SuperName)。
//   - Has:      false 表示本次直接 eval 的调用点没有 home 语境 (全局/普通
//     函数/箭头函数内) —— eval 源码含 super.x 仍按 SyntaxError 处理。
type EvalSuperHome struct {
	Name      string
	Static    bool
	ThisHome  bool
	SuperName string
	Has       bool
}

// EvalCompileOptions 是 eval 单元编译选项 (object.CompileSourceWithOpts)。
type EvalCompileOptions struct {
	// AllowNewTarget: eval 源码允许出现 new.target (仅「非箭头函数体内的
	// 直接 eval」语境为真, 值由 VM 经闭包写入)。
	AllowNewTarget bool
	// EvalTopLevel: 本单元是 eval 源码 (eval 顶层 using / await using 是
	// SyntaxError, 看板 rabcWh); new Function 的体是真 FunctionBody, 不置。
	EvalTopLevel bool
	// SuperHome: 直接 eval 调用点的 super home 上下文。SuperProperty 是否
	// 合法由 Has 决定; SuperCall (super()) 恒不合法 (18.2.1.1.2)。
	SuperHome EvalSuperHome
}

var compileSourceHook CompileSourceFunc

// compileSourceNTAllowedHook 是「允许 new.target」的编译实现。仅「非箭头函数
// 体内的直接 eval」这一语境需要: 该语境下 eval 源码的 new.target 合法 (规范
// sec-scripts-static-semantics-early-errors), 其余 (global/indirect/箭头 eval、
// Function 构造器) 一律禁止, 走 compileSourceHook。
var compileSourceNTAllowedHook CompileSourceFunc

// compileSourceEvalHook 是「eval 源码」专用的编译实现。与 compileSourceHook
// 的差别仅在解析期的 eval 顶层早错判定 (parser.SetEvalTopLevel): eval 源码
// 按 Script goal 解析, `using` / `await using` 声明出现在其顶层是 SyntaxError
// (规范 sec-let-const-using-and-await-using-declarations-static-semantics-
// early-errors; test262 using-not-allowed-at-top-level-of-eval.js)。
// 而 new Function 的体是真正的 FunctionBody (using 合法), 必须走
// compileSourceHook, 故两者分开注册 (看板 rabcWh)。
var compileSourceEvalHook CompileSourceFunc

// SetCompileSource 注册编译实现, 由 vm 包在初始化时调用。
func SetCompileSource(f CompileSourceFunc) {
	compileSourceHook = f
}

// SetCompileSourceEval 注册 eval 源码专用的编译实现, 由 vm 包在初始化时调用
// (eval 内建经 CompileSourceEval / CompileSourceAllowingNewTarget 使用)。
func SetCompileSourceEval(f CompileSourceFunc) {
	compileSourceEvalHook = f
}

// SetCompileSourceAllowingNewTarget 注册「允许 new.target」的编译实现,
// 由 vm 包在初始化时调用 (仅直接 eval 语境使用)。
func SetCompileSourceAllowingNewTarget(f CompileSourceFunc) {
	compileSourceNTAllowedHook = f
}

// compileSourceOptsHook 是「带编译选项」的编译实现 (见 EvalCompileOptions)。
// 由 vm 包在初始化时注册; stdlib 的 eval 在需要 super home / new.target
// 组合语境时经 CompileSourceWithOpts 调用。
var compileSourceOptsHook func(src string, opts EvalCompileOptions) (*CompiledFunction, error)

// SetCompileSourceOpts 注册带选项的编译实现, 由 vm 包在初始化时调用。
func SetCompileSourceOpts(f func(src string, opts EvalCompileOptions) (*CompiledFunction, error)) {
	compileSourceOptsHook = f
}

// CompileSourceWithOpts 按选项编译 eval 源码为顶层函数 (super home /
// new.target 等语境信息见 EvalCompileOptions)。
func CompileSourceWithOpts(src string, opts EvalCompileOptions) (*CompiledFunction, error) {
	if compileSourceOptsHook == nil {
		return nil, errors.New("CompileSourceWithOpts: compile hook not registered (vm package not linked)")
	}
	return compileSourceOptsHook(src, opts)
}

// CompileSource 编译源码为顶层函数。
func CompileSource(src string) (*CompiledFunction, error) {
	if compileSourceHook == nil {
		return nil, errors.New("CompileSource: compile hook not registered (vm package not linked)")
	}
	return compileSourceHook(src)
}

// CompileSourceAllowingNewTarget 编译允许 new.target 的 eval 源码为顶层函数。
// 只有在调用方确认为「非箭头函数体内的直接 eval」时才应使用。
func CompileSourceAllowingNewTarget(src string) (*CompiledFunction, error) {
	if compileSourceNTAllowedHook == nil {
		return nil, errors.New("CompileSourceAllowingNewTarget: compile hook not registered (vm package not linked)")
	}
	return compileSourceNTAllowedHook(src)
}

// CompileSourceEval 编译 eval 源码为顶层函数 (eval 顶层 using / await using
// 早错生效)。只有调用方确认为「eval 语境」时才应使用; new Function 等
// FunctionBody 语境请使用 CompileSource。
func CompileSourceEval(src string) (*CompiledFunction, error) {
	if compileSourceEvalHook == nil {
		return nil, errors.New("CompileSourceEval: compile hook not registered (vm package not linked)")
	}
	return compileSourceEvalHook(src)
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

// directEvalNewTarget 记录「直接 eval 调用者帧生效的 new.target」以及该语境
// 是否允许 eval 源码出现 new.target。规范 sec-scripts-static-semantics-early-
// errors: NewTarget 只在「非箭头函数体内的直接 eval」合法 —— 由 VM 依调用者帧
// 的闭包种类判定, 与 this 桥同纪律 (一次性, 消费即清除, 避免泄漏)。
var (
	directEvalNewTarget Value
	directEvalNTAllowed bool
	hasDirectEvalNT     bool
)

// SetDirectEvalNewTarget 由 vm 在确认「本次调用是直接 eval」后调用, 写入调用者
// 帧的 new.target 与「调用者是否为非箭头函数」。eval 内建随之消费: allowed 决定
// 编译期是否放行 new.target, newTarget 写入包装闭包供取值。
func SetDirectEvalNewTarget(newTarget Value, allowed bool) {
	directEvalNewTarget = newTarget
	directEvalNTAllowed = allowed
	hasDirectEvalNT = true
}

// TakeDirectEvalNewTarget 取出并清除直接 eval 的 new.target 上下文。
// ok 为 false 表示本次 eval 不是直接 eval (间接 / Go 侧调用)。
func TakeDirectEvalNewTarget() (newTarget Value, allowed bool, ok bool) {
	if !hasDirectEvalNT {
		return UndefinedSingleton, false, false
	}
	v, a := directEvalNewTarget, directEvalNTAllowed
	directEvalNewTarget = nil
	directEvalNTAllowed = false
	hasDirectEvalNT = false
	return v, a, true
}

// directEvalSuperHome 记录「直接 eval 调用点的 super home 上下文」(roiE5Z)。
// 与 this / new.target 两桥同纪律: VM 在确认本次调用是直接 eval 后写入
// (随 OP_EVAL_MARK 家族的 home 标记), eval 内建在 runGlobalEval 里取出并
// 转交编译桥; 消费即清除, 无 home 语境的调用置 Has=false。
var (
	directEvalSuperHome    EvalSuperHome
	hasDirectEvalSuperHome bool
)

// SetDirectEvalSuperHome 由 vm 在确认「本次调用是直接 eval」后调用, 写入
// 调用点的 super home 上下文 (无 home 语境时传 Has=false 的空值)。
func SetDirectEvalSuperHome(h EvalSuperHome) {
	directEvalSuperHome = h
	hasDirectEvalSuperHome = true
}

// TakeDirectEvalSuperHome 取出并清除直接 eval 的 super home 上下文。
// ok 为 false 表示本次 eval 不是直接 eval (间接 / Go 侧调用)。
func TakeDirectEvalSuperHome() (home EvalSuperHome, ok bool) {
	if !hasDirectEvalSuperHome {
		return EvalSuperHome{}, false
	}
	h := directEvalSuperHome
	directEvalSuperHome = EvalSuperHome{}
	hasDirectEvalSuperHome = false
	return h, true
}
