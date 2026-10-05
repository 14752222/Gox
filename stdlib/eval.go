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
// pendingDirectEvalInit 是「本次 eval 调用发生在类字段初始化器内、且是
// 直接 eval」的一次性标志。vm 执行 OP_EVAL_MARK 时经 MarkDirectEvalInit
// 置位; eval 内建被调用时立即消费。标记指令与被标记的调用在字节码里紧邻
// (编译器只在字段初始化表达式里的 `eval(...)` 前发射), 因此一一对应。
var pendingDirectEvalInit bool

// MarkDirectEvalInit 由 vm 包在执行 bytecode.OP_EVAL_MARK 时调用
// (VM 依赖 stdlib, 故标志放这里; stdlib 不反向依赖 vm)。
func MarkDirectEvalInit() { pendingDirectEvalInit = true }

// evalInitRejectsArguments 报告「类字段初始化器内直接 eval」的源码是否
// 触发补充早错 (sec-performeval-rules-in-initializer): 直接 eval 的
// StatementList 含 arguments 引用时是 SyntaxError。
//
// 实现复用解析器已有的「字段初始化器内 arguments / super(...) 早错」判定:
// 把源码原样嵌进一个合成 class 的字段初始化器 (立即调用的箭头函数体) 编译
// 一遍 —— 编译失败即判为早错。之所以用箭头体而非普通函数体, 是要与规范
// 的 ContainsArguments 保持一致: 递归进箭头函数、止于普通函数 (普通函数
// 有自己的 arguments, 其内的 arguments 不算)。super(...) 调用同样是该
// 上下文早错, 也一并由这条路径拦下 (与 Node 一致)。
//
// 该检查只在初始化器内直接 eval 这一罕见路径上多编译一次, 普通脚本/模块
// 与间接 eval 零开销。
func evalInitRejectsArguments(src string) bool {
	synthetic := "(class { x = (() => {\n" + src + "\n})() })"
	_, err := object.CompileSource(synthetic)
	return err != nil
}

func setupEvalAndMisc(env *runtime.Environment) {
	evalFn := object.NewBuiltin("eval", func(args ...object.Value) object.Value {
		// 立即消费一次性标志 (无论本参数是否为字符串, 标记都只属于本次调用)。
		restrictedInit := pendingDirectEvalInit
		pendingDirectEvalInit = false
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		src, ok := args[0].(*object.String)
		if !ok {
			// 规范: 非字符串参数原样返回
			return args[0]
		}
		if restrictedInit && evalInitRejectsArguments(src.Value) {
			// 返回 *object.Error → VM 作为异常抛出 (与 runGlobalEval 同口径)。
			// 在编译/执行 eval 体之前抛出, 故体副作用 (如 executed=true) 不会发生。
			return object.NewErrorWithName("SyntaxError",
				"SyntaxError: 'arguments' is not allowed in class field initializer")
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
