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
