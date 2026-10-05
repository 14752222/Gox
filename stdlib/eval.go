package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupEvalAndMisc 注册 eval、AggregateError，并把 Function.prototype
// 变成可调用对象 (规范: Function.prototype() 返回 undefined)。
//
// eval 语义说明: 实现为全局 eval (间接 eval)。direct eval 需要访问调用
// 处的局部作用域，而本运行时函数局部变量由 VM 栈槽承载、不进环境链，
// 因此 eval 中只能访问全局绑定 —— 这与大多数脚本化用途 (解析表达式/
// JSON 片段/动态构建对象) 兼容。
func setupEvalAndMisc(env *runtime.Environment) {
	evalFn := object.NewBuiltin("eval", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		src, ok := args[0].(*object.String)
		if !ok {
			// 规范: 非字符串参数原样返回
			return args[0]
		}
		return runGlobalEval(env, src.Value)
	})
	evalFn.SetProperty("name", object.NewString("eval"))
	evalFn.SetProperty("length", object.NewNumber(1))
	env.Declare("eval", evalFn, false)

	// ===== AggregateError (ES2021) =====
	aggFn := object.NewBuiltin("AggregateError", func(args ...object.Value) object.Value {
		msg := ""
		if len(args) > 1 {
			msg = toStr(args[1])
		}
		err := object.NewErrorWithName("AggregateError", msg)
		errors := object.NewArray([]object.Value{})
		if len(args) > 0 {
			if next, ok := object.Iterate(args[0]); ok {
				var list []object.Value
				for {
					v, done := next()
					if done {
						break
					}
					list = append(list, v)
				}
				errors = object.NewArray(list)
			}
		}
		err.SetProperty("errors", errors)
		return err
	})
	aggFn.ReturnIsValue = true
	aggFn.SetProperty("prototype", object.NewObject())
	env.Declare("AggregateError", aggFn, false)

	// 注: %Function.prototype% 的可调用性 (Function.prototype() 返回 undefined)
	// 已由 setupFunctionIntrinsics 在装配期直接建好 (见 stdlib/function_proto.go)，
	// 此处不再替换该对象 —— 否则会与函数对象 [[Prototype]] 链上的同一对象失配。
}

// runGlobalEval 编译并同步执行源码，返回最后一个语句的值。
// 源码在全局环境中执行 (eval 内声明的 var/let 进入全局)。
//
// 编译经 object.CompileSource 桥完成 (由 vm 包注册实现), stdlib 不直接
// 依赖 lexer/parser/compiler 前端包。
//
// 完成值策略: 若源码是单个表达式，包装为 `return (<expr>)` 捕获其值
// (覆盖 eval 的绝大多数用途)；多语句源码退回普通函数包装，完成值为
// undefined (函数体结尾是隐式 return void，编译器不保留语句完成值)。
func runGlobalEval(env *runtime.Environment, src string) object.Value {
	// parseErr 记录最后一次解析/编译失败的原因, 用于拼进 SyntaxError 帮助定位
	var parseErr string
	buildAndRun := func(body string) object.Value {
		fn, err := object.CompileSource(body)
		if err != nil {
			// 记录最近一次失败原因 (多语句包装那轮的错误最贴近源码位置)
			parseErr = err.Error()
			return nil // 由外层换包装重试
		}
		closure := &object.Closure{Fn: fn, Env: env}
		result := object.CallFunction(closure, object.UndefinedSingleton)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			// 回调桥报告了 eval 代码里的 JS 异常。必须把异常重新交给 VM 的
			// throw 流程 —— 直接 return result 会把它静默吞成 undefined
			// (调用方拿到 undefined 而非抛出)。典型症状: eval("未声明名")
			// 本应抛 ReferenceError 却返回 undefined (rYVgne)。
			//
			// 优先取原始抛出值还原错误对象: 保住错误类型与 catch 侧 === 语义；
			// 取不到才按 Go 侧错误字符串兜底包一层 (非 Error 抛出值无法经内建
			// 返回值原样抛出，这里与 callbackThrown/solidCallbackError 同口径)。
			if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
				if e, ok := thrown.(*object.Error); ok {
					return e
				}
			}
			return object.NewErrorWithName("Error", cbErr.Error())
		}
		return result
	}

	// 1) 表达式包装: return (<src>)
	if r := buildAndRun("(function(){ return (" + src + ") })"); r != nil {
		return r
	}
	// 2) 多语句包装: 函数体
	if r := buildAndRun("(function(){\n" + src + "\n})"); r != nil {
		return r
	}
	if parseErr != "" {
		return object.NewErrorWithName("SyntaxError", "eval: invalid source ("+parseErr+")")
	}
	return object.NewErrorWithName("SyntaxError", "eval: invalid source")
}
