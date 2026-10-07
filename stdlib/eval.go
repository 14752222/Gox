package stdlib

import (
	"strings"

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
// 直接 eval」的一次性标志。vm 在消费 OP_EVAL_MARK 后、确认被调恰为全局
// eval 内建时经 MarkDirectEvalInit 置位; eval 内建被调用时立即消费。
// 置位与消费在同一次 OP_CALL 分派内紧邻发生, 故不会跨调用泄漏。
var pendingDirectEvalInit bool

// MarkDirectEvalInit 由 vm 包在确认「本次调用是字段初始化器内的直接 eval」
// 后调用 (VM 依赖 stdlib, 故标志放这里; stdlib 不反向依赖 vm)。
func MarkDirectEvalInit() { pendingDirectEvalInit = true }

// evalInitRejectsRestricted 报告「类字段初始化器内直接 eval」的源码是否
// 触发补充早错 (sec-performeval-rules-in-initializer): 直接 eval 的
// StatementList 含 arguments 引用、或含 super(...) 调用时是 SyntaxError。
//
// 实现复用解析器已有的「字段初始化器内 arguments / super(...) 早错」判定:
// 把源码原样嵌进一个合成 class 的字段初始化器 (立即调用的箭头函数体) 编译
// 一遍, 只有解析器报出「... 在字段初始化器内不允许」这一族早错时才算命中。
// 之所以用箭头体而非普通函数体, 是要与规范的 ContainsArguments 保持一致:
// 递归进箭头函数、止于普通函数 (普通函数有自己的 arguments, 其内的
// arguments 不算)。仅按消息判定可避免把 super.x 属性访问 / new.target
// (初始化器内直接 eval 语境下合法) 误判成早错。
//
// 该检查只在初始化器内直接 eval 这一罕见路径上多编译一次, 普通脚本/模块
// 与间接 eval 零开销。
func evalInitRejectsRestricted(src string) bool {
	synthetic := "(class { x = (() => {\n" + src + "\n})() })"
	_, err := object.CompileSource(synthetic)
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "is not allowed in class field initializer")
}

func setupEvalAndMisc(env *runtime.Environment) {
	evalFn := object.NewBuiltin("eval", func(args ...object.Value) object.Value {
		// 立即消费一次性标志 (无论本参数是否为字符串, 标记都只属于本次调用)。
		restrictedInit := pendingDirectEvalInit
		pendingDirectEvalInit = false
		// 直接 eval 的上下文: 由 VM 在确认「本次调用是直接 eval」后写入调用者
		// 帧的 this 与严格性。取不到 (间接 eval / Go 侧调用) 时按**全局 eval**
		// 处理: this = globalThis (间接 eval 恒在全局 this 下执行, 与 node
		// 一致 —— 即便源码含 "use strict" 指令也如此), 严格性只看源码指令。
		evalThis, callerStrict, isDirect := object.TakeDirectEvalThis()
		// 直接 eval 的 new.target 上下文: 调用者帧的 new.target 与「该语境是否
		// 允许 new.target」(仅非箭头函数体内直接 eval 允许)。与 this 桥同纪律,
		// 一并消费。非直接 eval 时 ok=false, 按全局 eval 处理 (禁止 new.target)。
		evalNewTarget, evalNTAllowed, hasEvalNT := object.TakeDirectEvalNewTarget()
		if !hasEvalNT {
			evalNTAllowed = false
		}
		// 直接 eval 的 super home 上下文 (roiE5Z): 调用点函数有 [[HomeObject]]
		// (类方法/对象方法/字段初始化器等) 时, eval 源码的 super.x 合法并按该
		// home 解析; Has=false 的语境 (全局/普通函数/箭头内) super.x 仍
		// SyntaxError。super() 恒不合法 (18.2.1.1.2), 不随桥传递。
		superHome, _ := object.TakeDirectEvalSuperHome()
		if !isDirect {
			evalThis = object.UndefinedSingleton
			if g, ok := env.Get("globalThis"); ok {
				evalThis = g
			}
		}
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		src, ok := args[0].(*object.String)
		if !ok {
			// 规范: 非字符串参数原样返回
			return args[0]
		}
		if restrictedInit && evalInitRejectsRestricted(src.Value) {
			// 返回 *object.Error → VM 作为异常抛出 (与 runGlobalEval 同口径)。
			// 在编译/执行 eval 体之前抛出, 故体副作用 (如 executed=true) 不会发生。
			return object.NewErrorWithName("SyntaxError",
				"SyntaxError: 'arguments' or 'super' call is not allowed in class field initializer")
		}
		return runGlobalEval(env, src.Value, evalThis, callerStrict, evalNewTarget, evalNTAllowed, superHome)
	})
	evalFn.SetProperty("name", object.NewString("eval"))
	evalFn.SetFunctionLength(1)
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

// trimTrailingSemicolons 去掉源码首尾空白与末尾分号, 让单个表达式能进
// `return (<expr>)` 包装。规范里 eval 的完成值取最后一条语句的值, 而
// `eval("this;")` 与 `eval("this")` 等价 —— 末尾分号不应改变结果。
//
// 只处理后缀分号 (可多个, 容忍中间空白); 不试图处理多语句 (那由多语句
// 包装兜底)。全部是分号/空白时返回空串。
func trimTrailingSemicolons(src string) string {
	s := strings.TrimSpace(src)
	for strings.HasSuffix(s, ";") {
		s = strings.TrimSpace(s[:len(s)-1])
	}
	return s
}

// startsWithBrace 报告已 TrimSpace 的源码是否以 `{` 开头 —— 即命中
// ExpressionStatement 的 lookahead 限制 (规范 12.2: lookahead ∉ { {, function,
// async function, class, let [ })。这类源码不能走 `return (<expr>)` 包装。
//
// 本函数只判 `{` 这一种 (r9HBA8 的实际触发形态); 其余 lookahead 关键字
// (function/class/let [) 未纳入 —— 它们的表达式包装在 Gox 里与规范的分野
// 尚未有测试覆盖, 留作后续 (见本次改动说明的「未做项」)。
func startsWithBrace(src string) bool {
	return len(src) > 0 && src[0] == '{'
}

// splitStrictDirective 从源码里剥出前导的 "use strict" 指令 (若存在)。
// 返回 (strict, rest): strict 表示源码带严格指令; rest 是剥掉指令与紧随其
// 分号后的剩余源码。
//
// 只认**最前**位置、无转义的精确 `use strict` 字符串字面量 (与解析器
// isUseStrictDirective 同口径); 且只剥一条 —— 规范里严格指令必须落在
// Directive Prologue 首部, 这里够用。用途: 表达式包装要把指令放在包装
// **函数体首部**再 return 表达式, 而不是塞进括号变序列表达式 —— Gox 解析
// 器不支持 `(a; b)` 序列, 塞进去会整段编译失败退回多语句包装、丢掉完成值。
func splitStrictDirective(src string) (bool, string) {
	s := strings.TrimLeft(src, " \t\r\n")
	if len(s) == 0 {
		return false, src
	}
	q := s[0]
	if q != '"' && q != '\'' {
		return false, src
	}
	end := strings.IndexByte(s[1:], q)
	if end < 0 {
		return false, src
	}
	body := s[1 : 1+end]
	if body != "use strict" {
		return false, src
	}
	rest := strings.TrimLeft(s[end+2:], " \t\r\n")
	// 指令后可有 (也可没有) 分号; 有则连同它一起剥掉。
	if strings.HasPrefix(rest, ";") {
		rest = rest[1:]
	}
	return true, rest
}

// runGlobalEval 编译并同步执行源码，返回最后一个语句的值。
// 源码在全局环境中执行 (eval 内声明的 var/let 进入全局)。
//
// 编译经 object.CompileSource 桥完成 (由 vm 包注册实现), stdlib 不直接
// 依赖 lexer/parser/compiler 前端包。
//
// this 语义 (规范 sec-performeval): eval 代码的 thisValue 由调用方传入:
//   - 直接 eval (evalThis = 调用者帧的 this): this 绑定与调用者一致;
//   - 间接 eval / Go 侧调用 (evalThis = undefined): 全局 eval, this = globalThis。
//
// 包装函数自身是 sloppy 函数, 会把 undefined/null 接收者按 sloppy 归一为
// globalThis —— 这正好覆盖「间接 eval / 全局直接 eval」的全局 this 口径。
// 当 eval 源码自带 "use strict" 指令时, 包装函数 (因函数体首个指令即严格
// 指令) 编译出的 Fn.IsStrict 为 true, callClosure 遂**原样保留** thisValue
// (undefined 不被归一) —— 与规范「严格 eval 代码的 thisValue 原样」一致。
//
// superHome 是直接 eval 调用点的 super home 上下文 (roiE5Z, 见
// object.EvalSuperHome): Has 为真时经带选项的编译桥进入 eval 单元, 让
// 源码里的 SuperProperty (super.x) 合法并按 home 解析; SuperCall (super())
// 恒 SyntaxError (编译单元不放行)。
//
// 完成值策略: 若源码是单个表达式，包装为 `return (<expr>)` 捕获其值
// (覆盖 eval 的绝大多数用途)；多语句源码退回普通函数包装，完成值为
// undefined (函数体结尾是隐式 return void，编译器不保留语句完成值)。
func runGlobalEval(env *runtime.Environment, src string, evalThis object.Value, callerStrict bool, evalNewTarget object.Value, allowNewTarget bool, superHome object.EvalSuperHome) object.Value {
	// parseErr 记录最后一次解析/编译失败的原因, 用于拼进 SyntaxError 帮助定位
	var parseErr string
	buildAndRun := func(body string) object.Value {
		// 编译: 语境三选一, 全部走 eval 专用编译桥 (evalTopLevel=true,
		// 看板 rabcWh: eval 源码按 Script goal 解析, 顶层 using / await using
		// 声明是 SyntaxError, 块内/函数体内的 using 仍合法) ——
		//   1) superHome.Has: 带选项编译桥 (super home [+ new.target] 语境,
		//      roiE5Z: 方法内直接 eval 的 super.x 合法, super() 恒拦);
		//   2) allowNewTarget: 仅「非箭头函数体内的直接 eval」放行 new.target;
		//   3) 其余 (global/indirect/箭头 eval): 整单元禁止 new.target,
		//      无 home 语境时 super.x 也维持 SyntaxError。
		// 禁止时含 new.target / super.x 的源码报 SyntaxError —— 与规范早错
		// 一致 (本函数多语句包装那轮会再次尝试并保留该错误消息, 故不会被后退
		// 的包装掩盖)。
		var fn *object.CompiledFunction
		var err error
		switch {
		case superHome.Has:
			fn, err = object.CompileSourceWithOpts(body, object.EvalCompileOptions{
				AllowNewTarget: allowNewTarget,
				EvalTopLevel:   true,
				SuperHome:      superHome,
			})
		case allowNewTarget:
			fn, err = object.CompileSourceAllowingNewTarget(body)
		default:
			fn, err = object.CompileSourceEval(body)
		}
		if err != nil {
			// 记录最近一次失败原因 (多语句包装那轮的错误最贴近源码位置)
			parseErr = err.Error()
			return nil // 由外层换包装重试
		}
		closure := &object.Closure{Fn: fn, Env: env}
		// 直接 eval 的 new.target 与调用者一致 (词法继承): 把调用者帧生效的
		// new.target 写到包装闭包上, callClosure 装配帧时据此写入帧 (见 vm)。
		if allowNewTarget {
			closure.NewTarget = evalNewTarget
		}
		result := object.CallFunction(closure, evalThis)
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

	// 前导 "use strict" 指令: 剥出来放进包装体首部 —— 这样包装函数本身成为
	// 严格函数, callClosure 会原样保留 thisValue (不把 undefined 归一为
	// globalThis), 与规范「严格 eval 代码的 thisValue 原样」一致。剩下的是
	// 不含该指令的源码 (通常只剩一个表达式)。
	//
	// eval 代码的严格性 = 源码含指令 OR 调用者严格 (规范 PerformEval:
	// strictCaller 为真时 eval 代码恒严格)。两条来源任一成立就要让包装函数
	// 严格, 否则 thisValue 会被错误地 sloppy 归一。
	strictSource, body := splitStrictDirective(src)
	directive := ""
	if strictSource || callerStrict {
		directive = "\"use strict\";\n"
	}

	// 1) 表达式包装: <directive> return (<expr>) —— 去掉末尾分号, 否则
	// `return (expr;)` 是语法错误, 会错误地退回多语句包装并丢掉完成值
	// (典型症状: eval("this;") 本应返回 this 却得 undefined)。
	//
	// 但 ExpressionStatement 有 lookahead 限制 (规范 12.2): 以 `{` 开头的源码
	// 在语句位置**不是** ExpressionStatement 而是 BlockStatement。若仍塞进
	// `return (...)`, `{length: 3000}/1/g;` 会被读成对象字面量除法并落到运行期
	// (报 `g is not defined`)。这类源码必须交给下面的多语句包装按语句表解析。
	// 见 test262 language/statementList/eval-block-with-statment-regexp-literal-flags.js
	// (看板 r9HBA8)。
	if expr := trimTrailingSemicolons(body); expr != "" && !startsWithBrace(expr) {
		if r := buildAndRun("(function(){\n" + directive + " return (" + expr + ") })"); r != nil {
			return r
		}
	}
	// 2) 多语句包装: 函数体 (指令仍置于体首, 保证严格语义)
	if r := buildAndRun("(function(){\n" + directive + src + "\n})"); r != nil {
		return r
	}
	if parseErr != "" {
		return object.NewErrorWithName("SyntaxError", "eval: invalid source ("+parseErr+")")
	}
	return object.NewErrorWithName("SyntaxError", "eval: invalid source")
}
