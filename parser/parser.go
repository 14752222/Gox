package parser

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
)

// prefixParseFn 是前缀解析函数，用于解析以特定令牌开头的表达式。
type prefixParseFn func() ast.Expression

// infixParseFn 是中缀解析函数，用于解析在已解析表达式后跟特定令牌的表达式。
type infixParseFn func(ast.Expression) ast.Expression

// Parser 是递归下降解析器，使用 Pratt parsing 技术处理运算符优先级。
// 采用预分词方案：先将整个输入分词为 token 切片，再基于切片解析。
// 这样可以支持任意前瞻 (lookahead)，便于区分箭头函数和分组表达式等歧义语法。
type Parser struct {
	tokens []lexer.Token // 预分词的令牌切片
	pos    int           // 当前令牌位置
	errors *ErrorList

	prefixParseFns map[lexer.TokenType]prefixParseFn
	infixParseFns  map[lexer.TokenType]infixParseFn

	inLoop        bool // 是否在循环体内 (用于 break/continue)
	allowAwait    bool // 是否处于 async 上下文 (用于裸 await 早错; 每进一个函数体按该函数自身的 async 与否重置)
	// allowYield 标记当前函数体是否 generator / async-generator 体
	// (规范的 [Yield] 参数上下文)。LabelIdentifier : Identifier 的早错误:
	// It is a Syntax Error if this production has a [Yield] parameter and
	// StringValue of Identifier is "yield" —— 生成器体内 yield 不得作标签名
	// (test262 {language/expressions,language/statements}/async-generator/
	// [named-]yield-as-label-identifier.js)。每进一个**非箭头**函数体按该
	// 函数自身是否 generator 重置; 箭头函数没有自己的 [Yield] 参数, 词法
	// 继承外层值 (同 newTargetAllowed 的口径)。默认 false (script/module
	// 顶层与非生成器函数体)。
	allowYield bool
	depth      int // 当前语法嵌套深度 (表达式/语句递归层数)
	depthExceeded bool // 已触发嵌套深度上限 (后续解析短路，防错误洪水)

	// coverInitPending 收集本语句中遇到的 CoverInitializedName 属性
	// (`{ x = 默认值 }`)。它是解构赋值目标 cover grammar 的一部分, 只有被
	// literalToPattern 消费才合法; 语句收尾时仍有残留即 SyntaxError
	// (真正的对象字面量不得含初始化名, sec-object-initializer-early-errors)。
	coverInitPending []*ast.Property

	// awaitReservedInParams 标记正在解析 **async 函数/箭头/方法的形参列表**
	// (含默认表达式)。此窗口内 await 是保留字: `async f(x = await)` 与
	// `({ async m(x = await) {} })` / `class C { async m(x = await) {} }` 必须
	// 在解析期报 SyntaxError (规范 FormalParameters[~Await] + node 22 实测:
	// "await is only valid in async functions" 家族; test262
	// early-errors-{object-method,class-method,arrow}-await-in-formals-default.js)。
	// 嵌套函数/箭头**体**经 setAllowAwait 清零 (那里 await 回归普通标识符,
	// node 实测 `(async function(x = () => await) {})` 合法); 对象/类计算键
	// 不在函数边界内, 保留保留字判据 (node 同样报错)。
	awaitReservedInParams bool

	// yieldReservedInParams 标记正在解析 **生成器上下文下的形参列表**
	// (FormalParameters[+Yield] 窗口, 含默认表达式的整个形参区)。此窗口内
	// 不得出现 YieldExpression —— 规范 Generator/AsyncGenerator/Arrow/方法
	// 定义均有早错: "It is a Syntax Error if FormalParameters Contains
	// YieldExpression is true" (test262 generators/param-dflt-yield.js、
	// arrow-function/param-dflt-yield-expr.js 一族, node 22 实测:
	// `function*(x = yield){}` / `function*(x = [yield]){}` / 形参默认值里
	// 类·对象计算键 `[yield]` 皆 SyntaxError)。
	// 与 awaitReservedInParams 同一范式: 由 parseParameters 设位, 嵌套函数/
	// 箭头的**体**经 setAllowYield 清零 (那里 yield 回到外层语境, node 实测
	// `function*(x = () => yield){}` / `function*(x = function*(){ yield }){}`
	// 合法); 对象/类计算键不在函数边界内, 保留该判据。
	yieldReservedInParams bool

	// strict 是当前的严格模式上下文 (script 顶层默认 sloppy; 命中 "use strict"
	// 指令、进入 class 体、或 module 顶层时置 true，函数体按继承值向下传播)。
	// 解析期早错据此判定 (重复形参 / eval·arguments 作绑定名与赋值目标 /
	// legacy 八进制 / delete 标识符)。
	strict bool
	// module 标记本编译单元素是 ES module: 模块顶层恒严格 (无需指令)。
	module bool

	// usedJSXFactory 记录"本文件出现过需要 h 的 JSX"(小写标签被降级成
	// h(...) 调用)。ParseProgram 把它交到 ast.Program.UsesJSX 上，由 compiler
	// 决定要不要补 `import { h } from "gx/gfx"` —— 见 parser/jsx.go 的约定说明。
	usedJSXFactory bool

	// stmtPos 在 parseStatement 单点收集语句起始位置 (T05 运行时错误
	// 源码帧)。ParseProgram 把它交到 ast.Program.Positions。
	stmtPos ast.PositionTable

	// privEnvStack 是 class 私有名环境栈（class 嵌套链）。
	// 每进一个 class 体压一层; 引用点记进最内层 pending, class 收尾时
	// 未命中本层声明表的名字上抛外层 —— 全落空即「未在包围类中声明」早错。
	// 见 class_early_errors.go。
	privEnvStack []*privEnv

	// moduleTopLevel 仅在解析模块**顶层**语句时为 true。
	// import/export 声明只允许出现在模块顶层 (spec: ModuleItem), 嵌进块/函数
	// 体里都是 SyntaxError。parseStatementBody 在此为 false 时对 IMPORT/EXPORT
	// 报位置早错。见 block_early_errors.go。
	moduleTopLevel bool

	// lastBodyUsesStrict 记录**最近一次** parseFunctionBodyWithStrict 解析的
	// 函数体自身是否含 "use strict" 指令 (不含继承)。用于 14.1.2 早错:
	// 非简单形参列表 + 函数体含 use strict 指令 ⇒ SyntaxError。
	// 该函数在返回前写入, 故外层调用读到的总是自己体的值 (嵌套函数体的写入
	// 已被本层覆盖)。
	lastBodyUsesStrict bool
	// moduleEE 标记「按模块语义做早期错误检查」。它与 module 的区别:
	// module 还额外开启恒严格 + +Await 顶层上下文 (真模块编译用); moduleEE
	// 只影响**早期错误判定** —— test262 的 module 用例在 Gox 里是「以脚本
	// 方式执行」的 (入口走 EvalFileVM → compileSource(src,false)), 但语义上
	// 是模块, 需要按 Module 的早错规则拦截 (重复导出名/未声明导出/顶层
	// return 等)。SetModule(true) 会一并置位; 宿主也可经 SetModuleEarlyErrors
	// 单独开启 (见 vm.EvalFileVMModuleEarlyErrors)。见 module_early_errors.go。
	moduleEE bool

	// fnDepth 是当前所处的**函数体**嵌套层数 (每进一个 function/箭头/方法体
	// +1)。顶层为 0 —— 模块顶层的 `return` / `yield` 据此判定为早错。
	fnDepth int

	// blockOrFnDepth 统计当前解析位置位于多深的 Block/FunctionBody 之内
	// (script/eval 顶层为 0)。parseBlockImpl / parseBlockWithDirectives 进出时
	// 加减。仅用于 eval 顶层的 using 判定 (见 usingDeclAllowed): Gox 的 eval 把
	// 源码包进合成函数体, 该函数体是深度 1, 其直接语句即 eval 顶层。
	blockOrFnDepth int

	// usingAllowed 报告当前是否处于「规范的 StatementListItem 位置」——
	// 只有 Block 的 StatementList / FunctionBody / ClassStaticBlockBody /
	// ClassBody / ModuleItemList 允许 using / await using 声明。下列位置一律
	// 禁止 (sec-using-declaration-static-semantics-early-errors 等):
	//   - Script / eval 顶层 (仅 Module 顶层允许);
	//   - if/else/while/do/for/label 的**无花括号**单语句体;
	//   - CaseClause / DefaultClause 的语句列表。
	// 默认 false。parseBlockImpl / parseBlockWithDirectives / 模块顶层置 true;
	// parseBody 的单语句分支、parseLabeledStatement 的非块分支、
	// parseSwitchStatement 的子句体显式置 false。
	usingAllowed bool

	// evalTopLevel 标记「本编译单元是 eval 的源码」。Gox 的 eval 把源码包进
	// 合成函数体 (见 stdlib/eval.go 的 runGlobalEval), 该函数体经
	// parseBlockWithDirectives 会把 usingAllowed 置 true —— 若不特判, eval 顶层
	// 的 `using x = null;` 会被误认为合法函数体语句。本标志由 eval 编译桥置位,
	// 配合 blockOrFnDepth<=1 (即合成函数体的直接语句) 拒绝之。
	// new Function 的体是真正的 FunctionBody, **不**置本标志 (using 合法)。
	evalTopLevel bool
	// newTargetAllowed 标记当前位置 `new.target` 是否语法合法。
	// 规范 (sec-scripts-static-semantics-early-errors): NewTarget 只能出现在
	// **非箭头函数体**内 —— 箭头函数对 Contains 透明, 会一路冒泡到脚本/模块顶层
	// 才报错, 所以 `() => new.target` 处于顶层时非法、嵌在函数里时合法。
	// 进入非箭头函数体时置 true (保存/恢复), 进入箭头体时**保持**外层值。
	// 默认 false (script/module/eval 顶层)。
	newTargetAllowed bool

	// newTargetForbidden 是给「eval / Function 构造器编译单元」的强制禁止开关。
	// Gox 的 eval 实现为 stdlib 侧的**全局包装函数** (见 stdlib/eval.go), 编译器
	// 只看到文本上的 `(function(){ ... })`, 无从区分直接/间接 eval 与调用者是否
	// 非箭头函数 —— 而规范要求 eval 源码含 new.target 只在「非箭头函数内的直接
	// eval」语境合法。为避免把包装函数误当成合法函数体 (会让 global/indirect/
	// arrow eval 里的 new.target 静默变 undefined 而非 SyntaxError), 编译桥把该
	// 单元整体标为禁止。此标志独立于 newTargetAllowed, 不受函数体进出影响。
	newTargetForbidden bool
}

// SetNewTargetForbidden 强制本编译单元内禁止 new.target (供 eval/Function 编译桥)。
func (p *Parser) SetNewTargetForbidden(v bool) { p.newTargetForbidden = v }

// enterNewTargetScope 进入一段函数体前设置 new.target 合法性上下文, 返回恢复函数。
// nonArrow=true (函数声明/表达式/方法/getter/setter/构造器/generator/async):
// 置 true; nonArrow=false (箭头函数): 保持外层值 (词法继承)。
func (p *Parser) enterNewTargetScope(nonArrow bool) func() {
	prev := p.newTargetAllowed
	if nonArrow {
		p.newTargetAllowed = true
	}
	return func() { p.newTargetAllowed = prev }
}

// maxNestingDepth 是语法嵌套深度上限。
// 递归下降解析器对深层嵌套输入会同步加深 Go 调用栈，无上限时
// `((((((...` 这类输入最终耗尽 Go 栈 (fatal，不可 recover)；
// 同时配合 isArrowFunction 的扫描上限，把嵌套输入的解析代价
// 从 O(n²) 压到线性。正常代码 (含机器生成) 远达不到该深度。
const maxNestingDepth = 2000

// maxArrowScanLimit 限定 isArrowFunction 的前向扫描距离。
// 括号不闭合时扫描会一路走到 EOF，配合大量 `(` 构成平方级开销。
const maxArrowScanLimit = 100_000

// enterNesting 进入一层语法嵌套，超深时报错 (对应 SyntaxError)。
func (p *Parser) enterNesting(where string) bool {
	p.depth++
	if p.depth > maxNestingDepth {
		if !p.depthExceeded {
			p.depthExceeded = true
			p.addError(fmt.Sprintf("maximum nesting depth exceeded (%d) while parsing %s", maxNestingDepth, where))
		}
		return false
	}
	return true
}

// leaveNesting 离开一层语法嵌套。
func (p *Parser) leaveNesting() {
	p.depth--
}

// setAllowAwait 进入一个函数体前设置 async 上下文, 返回恢复函数。
// 任何函数体 (含同步函数、同步 generator) 都必须经过这里把 allowAwait
// 重置为「该函数自身的 async 与否」—— 否则 async 函数里嵌套的同步函数
// 会错误继承 async 上下文 (裸 await / for await 的上下文判定失效)。
// 同时清零 awaitReservedInParams: 函数体不是形参窗口, 嵌套函数体里的
// await 回归普通标识符 (见该字段注释)。
func (p *Parser) setAllowAwait(isAsync bool) func() {
	prev := p.allowAwait
	p.allowAwait = isAsync
	prevAwaitReserved := p.awaitReservedInParams
	p.awaitReservedInParams = false
	return func() {
		p.allowAwait = prev
		p.awaitReservedInParams = prevAwaitReserved
	}
}

// setAllowYield 进入一个函数体前设置 yield 语境 ([Yield] 参数), 返回恢复函数。
// 与 setAllowAwait 同一范式: 保存/恢复, 保证嵌套函数退出后回到外层上下文。
// 非箭头函数体按自身是否 generator 重置 (同步/async 函数体 = ~Yield, yield
// 恢复为普通标识符); 箭头函数由调用方把当前值原样传入以继承外层语境
// (箭头没有自己的 [Yield] 参数)。
func (p *Parser) setAllowYield(isGenerator bool) func() {
	prev := p.allowYield
	p.allowYield = isGenerator
	// 函数体不是形参窗口: 进入实体即清零 yieldReservedInParams, 使嵌套函数/
	// 箭头体里的 yield 回到该体自身的语境 (见该字段注释)。
	prevYieldReserved := p.yieldReservedInParams
	p.yieldReservedInParams = false
	return func() {
		p.allowYield = prev
		p.yieldReservedInParams = prevYieldReserved
	}
}

// yieldIsIdentifier 报告当前上下文里 yield 是否按**普通标识符**处理
// (IdentifierReference / BindingIdentifier), 而非保留字/关键字。
//
// 规范: yield 只在三种情形下是保留字 —— ① 生成器/async-generator 函数体
// (含其中箭头的继承) [+Yield]; ② 严格模式代码; ③ 模块代码 (模块恒严格)。
// 其余 (sloppy script / 普通函数体 / eval sloppy) 里 yield 是合法标识符
// (test262 language/expressions/{generators,async-generator,object/method-
// definition,class}/**/yield-identifier-non-strict.js 一族: 生成器**内部
// 嵌套的普通函数**体里 yield 仍可作 var 名/标识符引用)。
//
// 判据即上述三种语境取非: 既非生成器体 (allowYield), 又非严格 (strict),
// 也非模块 (module/moduleEE; 模块顶层恒严格, 故 module 为真时 strict 也应为真,
// 这里再并列 moduleEE/module 以稳妥)。
func (p *Parser) yieldIsIdentifier() bool {
	return !p.allowYield && !p.strict && !p.moduleEE && !p.module
}

// SetModule 标记本编译单元是 ES module (模块顶层恒严格, 无需 "use strict")。
// 必须在 ParseProgram 之前调用。
func (p *Parser) SetModule(v bool) {
	p.module = v
	if v {
		p.moduleEE = true
	}
}

// SetModuleEarlyErrors 单独开启「按模块语义做早期错误检查」, 不改变严格模式 /
// 顶层 await 上下文。用于把 test262 的 module 用例 (语义是模块, 但 Gox 按脚本
// 执行) 纳入 ModuleItemList 的早错拦截。必须在 ParseProgram 之前调用。
func (p *Parser) SetModuleEarlyErrors(v bool) { p.moduleEE = v }

// setStrict 进入一个 (函数/类) 体前设置严格上下文, 返回恢复函数。
// 与 setAllowAwait 同一范式: 保存/恢复, 保证嵌套函数退出后回到外层上下文。
func (p *Parser) setStrict(v bool) func() {
	prev := p.strict
	p.strict = v
	return func() { p.strict = prev }
}

// isBindingName 报告当前 token 能否作绑定名 (var/let/const 的名字、参数名、
// for-in/for-of 的迭代变量)。
// sloppy script 里 await 不是保留字 (node 实测: var await = 1 / let await /
// function f(await) 都合法), 所以 AWAIT 也能作绑定名 —— 否则裸 await 早错
// 会把 `var await = 1` 这种合法写法一起拦掉。
// async 同理不是保留字 (node 22 实测: let async = 1 合法; test262
// for-await-of/head-lhs-async.js), ASYNC token 在绑定位置按标识符接受。
func (p *Parser) isBindingName() bool {
	// async 形参区内 await 是保留字 (见 awaitReservedInParams): 绑定名判定
	// 直接否掉, 由调用方报 "expected parameter name" 类 SyntaxError。
	if p.awaitReservedInParams && p.curTokenIs(lexer.AWAIT) {
		return false
	}
	// yield 在 sloppy 非生成器代码里是合法绑定名 (var yield = 1 / function
	// f(yield){} / for (var yield of xs); test262 generators 一族
	// yield-identifier-non-strict.js)。生成器体 / 严格 / 模块里仍是保留字,
	// 由 yieldIsIdentifier 判据否掉 —— 调用方随后报 "expected identifier."
	if p.curTokenIs(lexer.YIELD) {
		return p.yieldIsIdentifier()
	}
	// 转义拼出的保留字不是关键字也不是合法 Identifier, 不得作绑定名
	// (test262 identifiers/val-*-via-escape-hex*.js、future-reserved-words/
	// *-strict-escaped.js、{await,yield}-as-binding-identifier-escaped.js 一族)。
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().IdentHasEscape &&
		p.escapedNameForbiddenAsIdent(p.curToken().Literal, p.strict) {
		return false
	}
	return p.curTokenIs(lexer.IDENTIFIER) || p.curTokenIs(lexer.AWAIT) ||
		p.curTokenIs(lexer.ASYNC)
}

// peekStartsAwaitOperand 报告 await 之后的 token 是否「必为操作数」——
// 即在非 async 上下文里构成裸 await 早错的形态。
//
// 口径 (node 22 sloppy script 实测): await 是普通标识符, await(1) 调用、
// await[0] 索引、await`x` 带标签模板、await++ / await-- 后缀、await - 1
// 二元、await in obj 都是合法的**标识符用法**; 而 await x / await 1 /
// await {} / await function(){} 这些「后面直接跟操作数」的才是 await
// 表达式 → SyntaxError。所以 MINUS/PLUS (兼作二元)、LPAREN (调用)、
// LBRACKET (索引)、BACKTICK (模板)、INC/DEC (后缀) 都**不算**操作数。
func (p *Parser) peekStartsAwaitOperand() bool {
	switch p.peekToken().Type {
	case lexer.IDENTIFIER,
		lexer.INT_LITERAL, lexer.FLOAT_LITERAL, lexer.BIGINT_LITERAL,
		lexer.STRING_LITERAL, lexer.REGEX_LITERAL,
		lexer.TRUE, lexer.FALSE, lexer.NULL, lexer.UNDEFINED,
		lexer.LBRACE, lexer.BANG, lexer.BIT_NOT,
		lexer.TYPEOF, lexer.DELETE, lexer.VOID,
		lexer.NEW, lexer.FUNCTION, lexer.CLASS, lexer.THIS, lexer.SUPER,
		lexer.IMPORT, lexer.ASYNC, lexer.YIELD, lexer.AWAIT,
		lexer.JSX_LT, lexer.PRIVATE_NAME:
		return true
	}
	return false
}

// New 创建一个新的 Parser 实例，预分词整个输入。
func New(l *lexer.Lexer) *Parser {
	p := &Parser{
		errors: NewErrorList(),
	}

	// 预分词整个输入
	for {
		tok := l.NextToken()
		p.tokens = append(p.tokens, tok)
		if tok.Type == lexer.EOF {
			break
		}
	}

	// 注册前缀解析函数
	p.prefixParseFns = make(map[lexer.TokenType]prefixParseFn)
	p.registerPrefix(lexer.IDENTIFIER, p.parseIdentifier)
	p.registerPrefix(lexer.INT_LITERAL, p.parseIntegerLiteral)
	p.registerPrefix(lexer.FLOAT_LITERAL, p.parseFloatLiteral)
	p.registerPrefix(lexer.BIGINT_LITERAL, p.parseBigIntLiteral)
	p.registerPrefix(lexer.STRING_LITERAL, p.parseStringOrTemplate)
	p.registerPrefix(lexer.BACKTICK, p.parseStringOrTemplate)
	p.registerPrefix(lexer.IMPORT, p.parseDynamicImport)
	p.registerPrefix(lexer.SUPER, p.parseSuperExpression)
	p.registerPrefix(lexer.REGEX_LITERAL, p.parseRegexLiteral)
	p.registerPrefix(lexer.TRUE, p.parseBooleanLiteral)
	p.registerPrefix(lexer.FALSE, p.parseBooleanLiteral)
	p.registerPrefix(lexer.NULL, p.parseNullLiteral)
	p.registerPrefix(lexer.UNDEFINED, p.parseUndefinedLiteral)
	p.registerPrefix(lexer.BANG, p.parseUnaryExpression)
	p.registerPrefix(lexer.MINUS, p.parseUnaryExpression)
	p.registerPrefix(lexer.PLUS, p.parseUnaryExpression)
	p.registerPrefix(lexer.BIT_NOT, p.parseUnaryExpression)
	p.registerPrefix(lexer.TYPEOF, p.parseUnaryExpression)
	p.registerPrefix(lexer.DELETE, p.parseUnaryExpression)
	p.registerPrefix(lexer.VOID, p.parseUnaryExpression)
	p.registerPrefix(lexer.INC, p.parseUnaryExpression)
	p.registerPrefix(lexer.DEC, p.parseUnaryExpression)
	p.registerPrefix(lexer.LPAREN, p.parseGroupedOrArrow)
	p.registerPrefix(lexer.LBRACKET, p.parseArrayLiteral)
	p.registerPrefix(lexer.LBRACE, p.parseObjectLiteral)
	p.registerPrefix(lexer.FUNCTION, p.parseFunctionExpression)
	p.registerPrefix(lexer.THIS, p.parseThisExpression)
	p.registerPrefix(lexer.NEW, p.parseNewExpression)
	p.registerPrefix(lexer.YIELD, p.parseYieldExpression)
	p.registerPrefix(lexer.AWAIT, p.parseAwaitExpression)
	p.registerPrefix(lexer.PRIVATE_NAME, p.parsePrivateIdentifier)
	p.registerPrefix(lexer.ASYNC, p.parseAsyncExpression)
	p.registerPrefix(lexer.CLASS, p.parseClassExpression)
	p.registerPrefix(lexer.JSX_LT, p.parseJSXElement)

	// 注册中缀解析函数
	p.infixParseFns = make(map[lexer.TokenType]infixParseFn)
	p.registerInfix(lexer.PLUS, p.parseBinaryExpression)
	p.registerInfix(lexer.MINUS, p.parseBinaryExpression)
	p.registerInfix(lexer.ASTERISK, p.parseBinaryExpression)
	p.registerInfix(lexer.SLASH, p.parseBinaryExpression)
	p.registerInfix(lexer.PERCENT, p.parseBinaryExpression)
	p.registerInfix(lexer.EXPONENT, p.parseBinaryExpression)
	p.registerInfix(lexer.EQ, p.parseBinaryExpression)
	p.registerInfix(lexer.NOT_EQ, p.parseBinaryExpression)
	p.registerInfix(lexer.STRICT_EQ, p.parseBinaryExpression)
	p.registerInfix(lexer.STRICT_NOT_EQ, p.parseBinaryExpression)
	p.registerInfix(lexer.LT, p.parseBinaryExpression)
	p.registerInfix(lexer.GT, p.parseBinaryExpression)
	p.registerInfix(lexer.LTE, p.parseBinaryExpression)
	p.registerInfix(lexer.GTE, p.parseBinaryExpression)
	p.registerInfix(lexer.AND, p.parseLogicalExpression)
	p.registerInfix(lexer.OR, p.parseLogicalExpression)
	p.registerInfix(lexer.NULL_COALESCE, p.parseLogicalExpression)
	p.registerInfix(lexer.BIT_AND, p.parseBinaryExpression)
	p.registerInfix(lexer.BIT_OR, p.parseBinaryExpression)
	p.registerInfix(lexer.BIT_XOR, p.parseBinaryExpression)
	p.registerInfix(lexer.SHIFT_LEFT, p.parseBinaryExpression)
	p.registerInfix(lexer.SHIFT_RIGHT, p.parseBinaryExpression)
	p.registerInfix(lexer.UNSIGNED_SHR, p.parseBinaryExpression)
	p.registerInfix(lexer.LPAREN, p.parseCallExpression)
	p.registerInfix(lexer.LBRACKET, p.parseIndexExpression)
	p.registerInfix(lexer.DOT, p.parseMemberExpression)
	p.registerInfix(lexer.OPTIONAL_CHAIN, p.parseOptionalChainExpression)
	p.registerInfix(lexer.QUESTION, p.parseConditionalExpression)
	p.registerInfix(lexer.ASSIGN, p.parseAssignmentExpression)
	p.registerInfix(lexer.PLUS_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.MINUS_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.ASTERISK_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.SLASH_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.PERCENT_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.EXPONENT_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.AND_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.OR_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.XOR_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.SHIFT_LEFT_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.SHIFT_RIGHT_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.UNSIGNED_SHR_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.AND_AND_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.OR_OR_EQ, p.parseAssignmentExpression)
	p.registerInfix(lexer.NULLISH_ASSIGN, p.parseAssignmentExpression)
	p.registerInfix(lexer.INSTANCEOF, p.parseBinaryExpression)
	p.registerInfix(lexer.IN, p.parseBinaryExpression)
	p.registerInfix(lexer.BACKTICK, p.parseTaggedTemplate)
	p.registerInfix(lexer.INC, p.parsePostfixExpression)
	p.registerInfix(lexer.DEC, p.parsePostfixExpression)
	p.registerInfix(lexer.COMMA, p.parseSequenceExpression)

	return p
}

func (p *Parser) registerPrefix(t lexer.TokenType, fn prefixParseFn) {
	p.prefixParseFns[t] = fn
}
func (p *Parser) registerInfix(t lexer.TokenType, fn infixParseFn) {
	p.infixParseFns[t] = fn
}

// curToken 返回当前位置的令牌。
func (p *Parser) curToken() lexer.Token {
	if p.pos < len(p.tokens) {
		return p.tokens[p.pos]
	}
	return lexer.Token{Type: lexer.EOF}
}

// peekToken 返回下一令牌。
func (p *Parser) peekToken() lexer.Token {
	if p.pos+1 < len(p.tokens) {
		return p.tokens[p.pos+1]
	}
	return lexer.Token{Type: lexer.EOF}
}

// peekTokenAt 返回偏移 n 处的令牌 (0=当前, 1=下一, ...)。
func (p *Parser) peekTokenAt(n int) lexer.Token {
	idx := p.pos + n
	if idx < len(p.tokens) {
		return p.tokens[idx]
	}
	return lexer.Token{Type: lexer.EOF}
}

// nextToken 前进到下一个令牌。
func (p *Parser) nextToken() {
	p.pos++
}

func (p *Parser) curTokenIs(t lexer.TokenType) bool   { return p.curToken().Type == t }
func (p *Parser) peekTokenIs(t lexer.TokenType) bool  { return p.peekToken().Type == t }
func (p *Parser) peek2TokenIs(t lexer.TokenType) bool { return p.peekTokenAt(2).Type == t }
func (p *Parser) peek3TokenIs(t lexer.TokenType) bool { return p.peekTokenAt(3).Type == t }

func (p *Parser) expectPeek(t lexer.TokenType) bool {
	if p.peekTokenIs(t) {
		p.nextToken()
		return true
	}
	p.addError(fmt.Sprintf("expected %s, got %s", t, p.peekToken().Type))
	return false
}

func (p *Parser) addError(msg string) {
	// 嵌套深度超限后错误恢复会产生海量重复报错 (每层解退都补一条)，
	// 保留首个深度错误即可，其余短路丢弃。
	if p.depthExceeded && len(p.errors.Errors) > 0 {
		return
	}
	tok := p.curToken()
	p.errors.Add(msg, tok.Line, tok.Column)
}

// clearCoverInit 从待报错清单移除一个已被解构目标消费的 CoverInitializedName 属性。
func (p *Parser) clearCoverInit(prop *ast.Property) {
	for i, e := range p.coverInitPending {
		if e == prop {
			p.coverInitPending = append(p.coverInitPending[:i], p.coverInitPending[i+1:]...)
			return
		}
	}
}

// reportUnconsumedCoverInit 在语句收尾时对仍未被消费的 CoverInitializedName 报早错。
func (p *Parser) reportUnconsumedCoverInit() {
	for _, prop := range p.coverInitPending {
		if key, ok := prop.Key.(*ast.Identifier); ok {
			p.addError(fmt.Sprintf("SyntaxError: CoverInitializedName '%s' is only valid in destructuring assignment target", key.Value))
		} else {
			p.addError("SyntaxError: CoverInitializedName is only valid in destructuring assignment target")
		}
	}
	p.coverInitPending = p.coverInitPending[:0]
}

// isBlockStart 判断当前 LBRACE 是否开始块语句 (而非对象字面量)。
//
// 规范 (Statement : BlockStatement) 与 12.2 ExpressionStatement 的
// lookahead 限制: 语句位置的 `{` **一律**是 BlockStatement 的开始 ——
// 对象字面量若要做语句, 必须加括号写成 `({ ... })`。因此这里恒为 true。
//
// 历史: 早期用「peek 是语句关键字/赋值号才算块」的启发式 (r9HBA8 前身),
// 于是 `{length: 3000}` 这类被误判成对象字面量, 语句位置的 `{ f: g }`
// (label) / `{ var x }` (块) 解析错。按规范统一成块后, 之前靠对象字面量
// 路径「碰巧通过」的负例 (块级早错等) 由第 1 块的早错检查接管。
func (p *Parser) isBlockStart() bool {
	return true
}

func (p *Parser) Errors() *ErrorList { return p.errors }

// ==================== 主解析入口 ====================

func (p *Parser) ParseProgram() *ast.Program {
	program := &ast.Program{}
	program.Statements = []ast.Statement{}

	// 模块顶层恒严格; script 顶层默认 sloppy, 除非 Directive Prologue 命中
	// 精确 "use strict"。
	p.strict = p.module
	inPrologue := true
	// 顶层语句允许 import/export (ModuleItem); 进块/函数体后置 false。
	p.moduleTopLevel = true
	// 顶层是 StatementListItem 位置: 只有模块 (含按模块语义判早错的 test262
	// module 用例) 允许 using 声明; script/eval 顶层不允许。
	p.usingAllowed = p.module || p.moduleEE
	// 模块顶层是 +Await 上下文 (top-level await, ES2022):
	// ModuleItem 的语法参数带 +Await, 故模块顶层的 `await expr` 合法, 且
	// for-await 头部也合法。函数/类体经 setAllowAwait 重置, 不会外泄 —— 见
	// setAllowAwait 的说明。script 顶层仍是 ~Await, await 只是普通标识符。
	if p.module {
		p.allowAwait = true
	}
	for !p.curTokenIs(lexer.EOF) {
		startIsString := p.curTokenIs(lexer.STRING_LITERAL)
		stmt := p.parseStatement()
		if !isNilStmt(stmt) {
			program.Statements = append(program.Statements, stmt)
			if inPrologue {
				if sl, ok := directiveString(stmt, startIsString); ok {
					if isUseStrictDirective(sl) {
						program.Strict = true
						p.strict = true
					}
				} else {
					inPrologue = false
				}
			}
		}
		p.nextToken()
	}
	// 用了小写标签的 JSX 就要有 h: 带上标记, 交给 compiler 补缺省工厂导入
	program.UsesJSX = p.usedJSXFactory
	program.Positions = p.stmtPos
	// ModuleItemList 的早期错误 (重复导出名 / 未声明导出 / 顶层 lexical 重声明 /
	// 顶层 return·yield / 严格保留字绑定 …)。见 module_early_errors.go。
	if p.moduleEE {
		p.checkModuleEarlyErrors(program)
	}
	return program
}

// directiveString 报告 stmt 是否是一条**指令** (整个表达式语句就是单个字符串
// 字面量), 是则返回该字面量。startIsString 表示语句首 token 就是字符串字面量
// (用来排除 `("use strict")` 这种括号包裹 —— 括号改变了语法形状, 不再是
// 规范的 Directive)。非指令返回 false, 表示 Directive Prologue 到此结束。
func directiveString(stmt ast.Statement, startIsString bool) (*ast.StringLiteral, bool) {
	if !startIsString {
		return nil, false
	}
	es, ok := stmt.(*ast.ExpressionStatement)
	if !ok {
		return nil, false
	}
	sl, ok := es.Expression.(*ast.StringLiteral)
	if !ok {
		return nil, false
	}
	return sl, true
}

// isUseStrictDirective 报告字符串字面量是否为精确 "use strict" 指令。
// 含转义的 `"use\u0020strict"` 解码后虽等于 "use strict", 但按规范**不是**
// 指令 (Directive 要求源码里就是精确字符序列), 故必须看 HadEscape。
// isNilStmt 报告语句是否为 nil 或「typed-nil」(接口里持有 nil 指针)。
//
// Go 的经典陷阱: 解析器多处 `return p.parseClassDeclaration()` 在失败时返回
// (*ast.ClassDeclaration)(nil) —— 装箱进 ast.Statement 接口后 `stmt != nil`
// **为真**, 于是被 append 进语句列表, 后续按具体类型解引用 (如块级重声明早错
// 的 stmtLexicalEntries) 即 nil panic。这里统一在语句入列处用 Kind()==Ptr &&
// IsNil() 判掉, 覆盖所有「返回 typed nil 的 parseXxx」入口。
func isNilStmt(s ast.Statement) bool {
	if s == nil {
		return true
	}
	v := reflect.ValueOf(s)
	return v.Kind() == reflect.Ptr && v.IsNil()
}

func isUseStrictDirective(sl *ast.StringLiteral) bool {
	return !sl.Token.HadEscape && sl.Value == "use strict"
}

// parseStatement 包装语句解析入口: 记录语句首 token 的行列位置到
// side-table (T05 运行时错误源码帧的位置来源)。真正的分发在
// parseStatementBody —— 嵌套块内的语句同样经过这里, 因此全程序
// 每条语句都有位置。
func (p *Parser) parseStatement() ast.Statement {
	startLine, startCol := p.curToken().Line, p.curToken().Column
	p.coverInitPending = p.coverInitPending[:0]
	stmt := p.parseStatementBody()
	// 语句收尾: 未被解构赋值目标消费的 CoverInitializedName 即早错
	// (真正的对象字面量不得含 `{ x = 默认值 }`, 见 object/cover-initialized-name.js)。
	p.reportUnconsumedCoverInit()
	if !isNilStmt(stmt) {
		if p.stmtPos == nil {
			p.stmtPos = ast.PositionTable{}
		}
		if _, dup := p.stmtPos[stmt]; !dup {
			p.stmtPos[stmt] = ast.Pos{Line: startLine, Col: startCol}
		}
	}
	return stmt
}

// ==================== 语句解析 ====================

// parseStatementBody 是语句解析分发主体 (原 parseStatement 的 switch)。
func (p *Parser) parseStatementBody() ast.Statement {
	switch p.curToken().Type {
	case lexer.LET:
		// `let` 与 `{` 之间有换行: 声明要求绑定与 let 同行 ⇒ 不构成 let 声明;
		// ExpressionStatement 的 lookahead 限制只含 `let [`, 不含 `let {` ⇒
		// `let` 按标识符表达式语句处理 (ASI 补分号), `{}` 是后续独立语句, 交还
		// 语句循环按块/对象字面量重新分发。
		// (test262 for-await-of/let-block-with-newline.js: for await (...) let \n {})
		if p.peekTokenIs(lexer.LBRACE) && p.peekToken().Line != p.curToken().Line {
			return &ast.ExpressionStatement{
				Token:      p.curToken(),
				Expression: &ast.Identifier{Token: p.curToken(), Value: "let"},
			}
		}
		return p.parseLetStatement()
	case lexer.CONST:
		return p.parseConstStatement()
	case lexer.VAR:
		// var 声明 (2026-09-29 落地, T04/Test262 前置): 函数作用域 + 提升 +
		// 允许重复声明, 由编译器登记到最近的函数作用域层 (见
		// compiler.compileVarStatement 与 symbol_table 的 FuncLayer)。
		// 词法层本就识别 VAR, var 作为属性名 (obj.var, {var: 1}) 不受影响。
		return p.parseVarStatement()
	case lexer.RETURN:
		return p.parseReturnStatement()
	case lexer.IF:
		return p.parseIfStatement()
	case lexer.FOR:
		stmt := p.parseForStatement()
		// for / for-in / for-of 头部词法声明 (let/const/using) 的 BoundNames
		// 与循环体 VarDeclaredNames 相交给早错 (sec-for-*-static-semantics-early-errors)。
		// 在唯一的 FOR 分派点统一收口, 覆盖全部头部形态 (含 for await / using)。
		p.checkForHeadRedeclaration(stmt)
		return stmt
	case lexer.WHILE:
		return p.parseWhileStatement()
	case lexer.DO:
		return p.parseDoWhileStatement()
	case lexer.WITH:
		return p.parseWithStatement()
	case lexer.BREAK:
		return p.parseBreakStatement()
	case lexer.CONTINUE:
		return p.parseContinueStatement()
	case lexer.FUNCTION:
		// function name / function* name → 函数声明
		if p.peekTokenIs(lexer.IDENTIFIER) || p.peekTokenIs(lexer.ASTERISK) {
			return p.parseFunctionDeclaration(false)
		}
		stmt := &ast.ExpressionStatement{Token: p.curToken()}
		stmt.Expression = p.parseExpression(LOWEST)
		p.checkSameLineASI()
		p.consumeSemicolon()
		return stmt
	case lexer.ASYNC:
		// async function 声明 / async function 表达式
		if p.peekTokenIs(lexer.FUNCTION) {
			p.nextToken() // 移到 function
			return p.parseFunctionDeclaration(true)
		}
		return p.parseExpressionStatement()
	case lexer.THROW:
		return p.parseThrowStatement()
	case lexer.TRY:
		return p.parseTryStatement()
	case lexer.SWITCH:
		return p.parseSwitchStatement()
	case lexer.IMPORT:
		if p.peekTokenIs(lexer.LPAREN) {
			// 动态 import(): import("./mod.js") 作为表达式
			return p.parseExpressionStatement()
		}
		// import 声明只在模块顶层合法 (spec: ModuleItem)。块/函数体内的
		// `import` 是 SyntaxError —— 报位置早错, 但仍继续解析以免错误恢复
		// 产生噪音。
		if !p.moduleTopLevel {
			p.addError("import declarations may only appear at the top level of a module")
		}
		return p.parseImportDeclaration()
	case lexer.EXPORT:
		if !p.moduleTopLevel {
			p.addError("export declarations may only appear at the top level of a module")
		}
		return p.parseExportDeclaration()
	case lexer.SEMICOLON:
		return &ast.ExpressionStatement{Token: p.curToken()}
	case lexer.CLASS:
		return p.parseClassDeclaration()
	case lexer.IDENTIFIER:
		if p.peekTokenIs(lexer.COLON) {
			return p.parseLabeledStatement()
		}
		// using 是上下文关键字: 仅当后面同行紧跟绑定标识符时才当声明;
		// 且只在允许的位置 (块/函数体/模块顶层/for 头) 成立。
		if p.isUsingDeclStart() {
			if !p.usingDeclAllowed() {
				p.addError("SyntaxError: using declarations are not allowed at the top level of a script or eval")
				return nil
			}
			return p.parseUsingStatement(false)
		}
		return p.parseExpressionStatement()
	case lexer.AWAIT:
		// await using x = ... (async 上下文 / 模块顶层的显式资源管理声明)。
		if p.isAwaitUsingDeclStart() {
			if !p.usingDeclAllowed() {
				p.addError("SyntaxError: await using declarations are not allowed here")
				return nil
			}
			return p.parseUsingStatement(true)
		}
		return p.parseExpressionStatement()
	case lexer.YIELD:
		// yield 后紧跟 ':' 一律按标签语句解析: sloppy 非生成器代码里 yield 是
		// 合法 IdentifierName (`yield: ;` 是标签语句); 生成器/async-generator
		// 体内 yield 是保留字, 作标签名为早错 —— 由 parseLabeledStatement 判定
		// (test262 {language/expressions,language/statements}/async-generator/
		// [named-]yield-as-label-identifier.js)。其余位置留给表达式路径
		// (yield / yield x / yield* x)。
		if p.peekTokenIs(lexer.COLON) {
			return p.parseLabeledStatement()
		}
		return p.parseExpressionStatement()
	case lexer.LBRACE:
		if p.isBlockStart() {
			return p.parseBlockStatement()
		}
		return p.parseExpressionStatement()
	default:
		return p.parseExpressionStatement()
	}
}

// parseVarStatement 解析 var 声明: var x = 5 / var a = 1, b; / var [x, y] = p。
// 语法形状与 let 完全同构 (复用解构模式的解析), 差别在编译语义: var 登记到
// 函数作用域层 (见 compiler.compileVarStatement)。
func (p *Parser) parseVarStatement() *ast.VarStatement {
	stmt := &ast.VarStatement{Token: p.curToken()}
	p.nextToken()

	// 首项: 普通标识符, 或解构模式 ([...] / {...})。解构首项借用合成名路径,
	// 真实绑定名在模式树里 (与历来的 parseDestructuringLet 同构); 之后的
	// 逗号续接项统一走 parseDeclaratorItem, 于是
	// `var [p] = [1], [q] = [2];` / `var p = 1, [q] = [2];` 都能正确解析。
	first, ok := p.parseDeclaratorItem(stmt.Token, false)
	if !ok {
		return nil
	}
	stmt.Name = first.Name
	stmt.Value = first.Value

	// 多条声明: var a = 1, b = 2; / var [p] = [1], [q] = [2];
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // 移到 ,
		p.nextToken() // 移到下一个声明项开头
		decl, ok := p.parseDeclaratorItem(stmt.Token, false)
		if !ok {
			return nil
		}
		stmt.More = append(stmt.More, decl)
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// parseDeclaratorItem 解析一条声明项 (逗号后的续接项), 返回时 cur 停在该项的
// 最后一个 token (普通项是名字/初始值末尾, 解构项是 '=' 右侧表达式末尾)。
//
// 进入时 cur 停在声明项开头。两种形态:
//   - 普通绑定标识符: Declarator{Name: 标识符, Value: 初始值或 nil};
//   - 解构模式 ([...] / {...}): 借用声明脱糖约定 —— Name 置合成名
//     ast.DestructureSyntheticName, Value 为 AssignmentExpression{Left: 模式},
//     真实绑定名只存在于模式树里 (与首项的解构路径, 以及 let/const 的既有
//     处理完全同构; 编译器 More 循环据此路由到 compilePatternBind)。
//
// mustInit 为 true 时解构项必须有初始化器 (spec: 解构声明不得无初始化器)。
// declToken 是所在声明语句的引导 token (var/let/const), 用作合成名字面位置的
// 兜底 (合成项无真实标识符 token)。解析出错时返回 ok=false。
func (p *Parser) parseDeclaratorItem(declToken lexer.Token, mustInit bool) (ast.Declarator, bool) {
	if p.curTokenIs(lexer.LBRACKET) || p.curTokenIs(lexer.LBRACE) {
		pattern := p.parseDestructuringPattern(p.curTokenIs(lexer.LBRACKET))
		if pattern == nil {
			return ast.Declarator{}, false
		}
		if !p.peekTokenIs(lexer.ASSIGN) {
			p.addError(fmt.Sprintf("expected = after destructuring, got %s", p.peekToken().Type))
			return ast.Declarator{}, false
		}
		p.nextToken() // 移到 '='
		p.nextToken() // 移到初始值开头
		value := p.parseExpression(LOWEST)
		if value == nil {
			return ast.Declarator{}, false
		}
		return ast.Declarator{
			Name: &ast.Identifier{Token: declToken, Value: ast.DestructureSyntheticName},
			Value: &ast.AssignmentExpression{
				Token: declToken, Left: pattern, Operator: "=", Right: value,
			},
		}, true
	}

	if !p.isBindingName() {
		p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
		return ast.Declarator{}, false
	}
	decl := ast.Declarator{Name: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}}
	if p.peekTokenIs(lexer.ASSIGN) {
		p.nextToken()
		p.nextToken()
		decl.Value = p.parseExpression(LOWEST)
	} else if mustInit {
		p.addError("const declaration must have an initializer")
		return ast.Declarator{}, false
	}
	return decl, true
}

func (p *Parser) parseLetStatement() *ast.LetStatement {
	stmt := &ast.LetStatement{Token: p.curToken()}
	letLine := p.curToken().Line
	p.nextToken()

	// ExpressionStatement 对 `let [` 有 lookahead 限制 (规范 14.5.1):
	// 语句位置上 `let` 与 `[` 之间**有换行**时, 它不构成 let 声明, 而整体
	// 作为 ExpressionStatement 又被该限制禁止 —— 是 SyntaxError。
	// (test262: for-await-of/let-array-with-newline.js 等一批 negative 用例;
	// 同行 let [a] = x 仍是正常解构声明。)
	if p.curTokenIs(lexer.LBRACKET) && p.curToken().Line != letLine {
		p.addError("SyntaxError: 'let' followed by '[' on a new line is not allowed as an expression statement")
		return nil
	}

	// 首项: 普通标识符或解构模式。解构首项 (含 `let [a] = x` / `let {a} = x`)
	// 借用合成名路径; 之后的逗号续接项统一走 parseDeclaratorItem, 于是
	// `let [a] = [1], [b] = [2];` / `let {x} = o, z = 3;` 都能解析。
	first, ok := p.parseDeclaratorItem(stmt.Token, false)
	if !ok {
		return nil
	}
	stmt.Name = first.Name
	stmt.Value = first.Value

	// 多条声明: let a = 1, b = 2; / let {x} = o, [y] = a, z = 3;
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // 移到 ,
		p.nextToken() // 移到下一个声明项开头
		decl, ok := p.parseDeclaratorItem(stmt.Token, false)
		if !ok {
			return nil
		}
		stmt.More = append(stmt.More, decl)
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseConstStatement() *ast.ConstStatement {
	stmt := &ast.ConstStatement{Token: p.curToken()}
	p.nextToken()

	// 首项: 普通标识符或解构模式 (const 的每一项都必须有初始化器)。
	// 解构首项借用合成名路径; 之后逗号续接项统一走 parseDeclaratorItem,
	// 于是 `const [a] = [1], [b] = [2];` / `const {x} = o, [y] = a;` 都能解析。
	//
	// 注意: parseDeclaratorItem **不**消费分号, 分号只在下面统一消费一次 ——
	// 这修掉了旧 parseDestructuringLet 在解构首项分支里先消费一次的隐患
	// (`for (const [a] = [1];;)` 空 condition 被多走一格)。
	first, ok := p.parseDeclaratorItem(stmt.Token, true)
	if !ok {
		return nil
	}
	stmt.Name = first.Name
	stmt.Value = first.Value

	// 多条声明: const a = 1, b = 2; / const [a, b] = x, {c} = y;
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // 移到 ,
		p.nextToken() // 移到下一个声明项开头
		decl, ok := p.parseDeclaratorItem(stmt.Token, true)
		if !ok {
			return nil
		}
		stmt.More = append(stmt.More, decl)
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// ==================== using / await using 声明 (ES2023) ====================

// usingDeclAllowed 报告当前位置是否允许出现 using 声明。
// 规范 sec-let-const-using-and-await-using-declarations-static-semantics-
// early-errors: goal 为 Script 时, UsingDeclaration 必须被 Block /
// ForStatement / ForInOfStatement / FunctionBody / ClassStaticBlockBody /
// ClassBody 包含; 换言之 script/eval 顶层、单语句位置、CaseClause/
// DefaultClause 语句列表都不允许 (usingAllowed 已按位置维护), 模块顶层允许。
func (p *Parser) usingDeclAllowed() bool {
	if !p.usingAllowed {
		return false
	}
	// eval 顶层: 合成函数体的直接语句 (blockOrFnDepth==1) 就是 eval 顶层。
	if p.evalTopLevel && p.blockOrFnDepth <= 1 {
		return false
	}
	return true
}

// SetEvalTopLevel 标记本编译单元是 eval 源码 (供 eval 编译桥)。
// 只影响 using / await using 的顶层早错判定 (见 evalTopLevel 字段说明)。
func (p *Parser) SetEvalTopLevel(v bool) { p.evalTopLevel = v }

// isUsingDeclStart 报告当前 token (标识符 using) 是否开启一个 using 声明。
// 上下文关键字判定: 仅当 `using` 之后**同一行**紧跟一个绑定标识符时才当声明,
// 否则 using 仍是普通标识符 (using = 1 / using.length / using[x] 都合法)。
//
// 允许的绑定名: IDENTIFIER / await / async (isBindingName), 以及 of —— 后者在
// 传统 for 头部写作 `for (using of = null;;)` (using-for-statement.js);
// 要求 of 之后是 `=` 才认, 避免把 `for (using of of xs)` 误判成声明。
func (p *Parser) isUsingDeclStart() bool {
	if !p.curTokenIs(lexer.IDENTIFIER) || p.curToken().Literal != "using" {
		return false
	}
	peek := p.peekToken()
	if peek.Line != p.curToken().Line {
		return false // 换行 ⇒ ASI, using 是独立标识符
	}
	switch peek.Type {
	case lexer.IDENTIFIER, lexer.AWAIT, lexer.ASYNC:
		return true
	case lexer.OF:
		return p.peek2TokenIs(lexer.ASSIGN)
	}
	return false
}

// isAwaitUsingDeclStart 报告当前 token (await) 是否开启一个 await using 声明。
// 仅在 await 作关键字 (async 上下文 / 模块顶层, allowAwait 为真) 时才成立。
func (p *Parser) isAwaitUsingDeclStart() bool {
	if !p.curTokenIs(lexer.AWAIT) || !p.allowAwait {
		return false
	}
	usingTok := p.peekToken()
	if usingTok.Type != lexer.IDENTIFIER || usingTok.Literal != "using" {
		return false
	}
	if usingTok.Line != p.curToken().Line {
		return false
	}
	bind := p.peekTokenAt(2)
	if bind.Line != usingTok.Line {
		return false
	}
	switch bind.Type {
	case lexer.IDENTIFIER, lexer.AWAIT, lexer.ASYNC:
		return true
	case lexer.OF:
		return p.peek3TokenIs(lexer.ASSIGN)
	}
	return false
}

// parseUsingStatement 解析 using / await using 声明。
// 进入时: isAwait=false 时 cur 在 `using`; isAwait=true 时 cur 在 `await`
// (peek 是 using)。BindingList 里每一项都必须有初始化器, 且绑定名只允许
// 标识符 (不支持解构 —— 规范如此, `using [a] = x` 是 SyntaxError)。
func (p *Parser) parseUsingStatement(isAwait bool) ast.Statement {
	stmt := &ast.UsingStatement{IsAwait: isAwait}
	if isAwait {
		p.nextToken() // cur = using
	}
	stmt.Token = p.curToken() // using 令牌
	p.nextToken()             // cur = 绑定名

	if !p.isUsingBindingName() {
		p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
		return nil
	}
	stmt.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	if !p.peekTokenIs(lexer.ASSIGN) {
		p.addError("SyntaxError: using declaration requires an initializer")
		return nil
	}
	p.nextToken() // =
	p.nextToken() // 初始化器首 token
	stmt.Value = p.parseExpression(LOWEST)

	// 多条声明: using a = f(), b = g();
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // ,
		p.nextToken() // 下一个绑定名
		if !p.isUsingBindingName() {
			p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
			return nil
		}
		decl := ast.Declarator{Name: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}}
		if !p.peekTokenIs(lexer.ASSIGN) {
			p.addError("SyntaxError: using declaration requires an initializer")
			return nil
		}
		p.nextToken() // =
		p.nextToken()
		decl.Value = p.parseExpression(LOWEST)
		stmt.More = append(stmt.More, decl)
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// isUsingBindingName 报告当前 token 能否作 using 声明项的绑定名。
// 与 isBindingName 同口径, 额外放行 of (contextual, 见 isUsingDeclStart)。
func (p *Parser) isUsingBindingName() bool {
	return p.isBindingName() || p.curTokenIs(lexer.OF)
}

func (p *Parser) parseReturnStatement() *ast.ReturnStatement {
	stmt := &ast.ReturnStatement{Token: p.curToken()}

	// 模块顶层不允许 return (spec: ModuleItem : StatementListItem[~Yield, ~Return])。
	// Gox 把 test262 的 module 用例按脚本执行, 顶层 return 在脚本里被容忍
	// (CommonJS 风格), 故只在 moduleEE 上下文里报早错。
	if p.moduleEE && p.fnDepth == 0 {
		p.addError("SyntaxError: return not in function")
	}

	if p.peekTokenIs(lexer.SEMICOLON) || p.peekTokenIs(lexer.RBRACE) || p.peekTokenIs(lexer.EOF) {
		p.consumeSemicolon()
		return stmt
	}
	// ASI 规则: return 后换行则自动插入分号 (return\nx 等价于 return; x),
	// 下一行的表达式不能被当作返回值
	if p.peekToken().Line != p.curToken().Line {
		return stmt
	}
	p.nextToken()
	stmt.ReturnValue = p.parseExpression(LOWEST)
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseExpressionStatement() *ast.ExpressionStatement {
	stmt := &ast.ExpressionStatement{Token: p.curToken()}
	stmt.Expression = p.parseCommaSequence()
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// parseCommaSequence 解析可能含逗号运算符的表达式 (a, b, c)。
// parseExpression(LOWEST) 在逗号处停止 (逗号是分隔符), 这里显式收集逗号序列。
func (p *Parser) parseCommaSequence() ast.Expression {
	expr := p.parseExpression(LOWEST)
	// parseExpression 后 curToken 停在最后一个表达式 token 上, 逗号是 peek。
	// 若 peek 是逗号, 前进到逗号再收集序列。
	if p.peekTokenIs(lexer.COMMA) {
		p.nextToken()
		return p.collectSequence(expr)
	}
	return expr
}

// collectSequence 把逗号后续表达式收集成 SequenceExpression。
// 调用时 curToken 必须是逗号, left 是已解析的逗号前表达式。
func (p *Parser) collectSequence(left ast.Expression) ast.Expression {
	seq := &ast.SequenceExpression{Token: p.curToken()}
	seq.Expressions = []ast.Expression{left}
	for p.curTokenIs(lexer.COMMA) {
		p.nextToken()
		right := p.parseExpression(LOWEST)
		if right != nil {
			seq.Expressions = append(seq.Expressions, right)
		}
		// parseExpression 后 curToken 停在最后一个表达式 token 上, 逗号是 peek。
		if p.peekTokenIs(lexer.COMMA) {
			p.nextToken() // 前进到逗号, 以便下次迭代收集
		}
	}
	return seq
}

func (p *Parser) parseIfStatement() *ast.IfStatement {
	stmt := &ast.IfStatement{Token: p.curToken()}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	stmt.Consequence = p.parseBody()

	if p.peekTokenIs(lexer.ELSE) {
		p.nextToken()
		if p.peekTokenIs(lexer.IF) {
			p.nextToken()
			stmt.Alternative = &ast.BlockStatement{
				Token: p.curToken(), Statements: []ast.Statement{p.parseIfStatement()},
			}
		} else {
			p.nextToken()
			stmt.Alternative = p.parseBody()
		}
	}
	return stmt
}

func (p *Parser) parseForStatement() ast.Statement {
	forToken := p.curToken() // FOR
	// for await (...of...): 异步迭代。for 之后紧跟 await 且再后面是 '(',
	// 头部形状与 for-of 相同 (绑定/赋值目标 + of + 可迭代表达式)。注意
	// `for (await x;;)` 是合法的传统 for (init 里有 await 表达式) —— 所以
	// 判定必须要求 await 后**紧跟** '(' 而不是出现在头部内。
	if p.peekTokenIs(lexer.AWAIT) && p.peek2TokenIs(lexer.LPAREN) {
		p.nextToken() // cur = await
		return p.parseForAwaitOfStatement(forToken)
	}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()

	// for (using x = expr; ...) / for (using x of y): 头部是 using 声明。
	// 必须在下面的 isForOfLHS 之前判定 —— 否则 `for (using of = null;;)`
	// 会被 isForOfLHS 误判成 for-of (深度 0 处出现 of)。
	// 注意: ForStatement 本身就是规范允许 using 的容器, 故 script/eval 顶层
	// 的 for 头部也合法 (test262 using-invalid-assignment-next-expression-for.js),
	// 这里不做 usingDeclAllowed 检查。
	if p.isUsingDeclStart() || p.isAwaitUsingDeclStart() {
		return p.parseForWithUsingHead(forToken)
	}

	// for...of / for...in 的头部: let/const/var 后跟**绑定**, 绑定之后是 of / in。
	// 绑定可以是标识符, 也可以是解构模式 —— 后者要跳过配对的 ]/} 才看得到关键字,
	// 所以判定统一交给 forBindingKeyword。
	if p.curTokenIs(lexer.LET) || p.curTokenIs(lexer.CONST) || p.curTokenIs(lexer.VAR) {
		kw := p.forBindingKeyword()
		destructuring := p.peekTokenIs(lexer.LBRACKET) || p.peekTokenIs(lexer.LBRACE)
		if kw == lexer.OF {
			return p.parseForOfStatement()
		}
		if kw == lexer.IN {
			if destructuring {
				// ES 允许 for (const [k, v] in obj); 本运行时只做了解构 + for-of。
				// 显式报出来 —— 否则它会掉进 parseForInStatement, 报一句
				// "expected variable name in for...in", 看不出真正的原因。
				p.addError("destructuring binding in for...in is not supported, " +
					"use for...of or bind a single key variable")
				return nil
			}
			return p.parseForInStatement()
		}
	}
	// 无声明关键字头部: `for (x of xs)` / `for (obj.k of xs)` /
	// `for ([a, b] of xs)` 这类赋值目标形态, 或传统 for 三段式。
	if p.isForOfLHS() {
		return p.parseForOfLHS(forToken, false)
	}
	return p.parseTraditionalFor()
}

// isForOfLHS 判定当前无声明关键字的 for 头部是不是「LHSExpression of ...」形态。
// 从 cur 扫描到 for 头部收口的 ')' (深度 0 的 RPAREN): 途中在深度 0 处出现 OF
// 即为 for-of (先扫后判, 因为 parseExpression 一旦吃掉头部就无法回退到传统
// for 三段式)。
//
// 两个排除项: OF 前一个是 DOT / OPTIONAL_CHAIN 时它是属性名 (for (i = a.of; ...));
// 深度 > 0 的 OF (for (m['of'] of xs)) 不算。
func (p *Parser) isForOfLHS() bool {
	depth := 0
	prev := lexer.ILLEGAL
	for i := 0; i <= maxArrowScanLimit; i++ {
		tok := p.peekTokenAt(i)
		switch tok.Type {
		case lexer.EOF:
			return false
		case lexer.LPAREN, lexer.LBRACE, lexer.LBRACKET:
			depth++
		case lexer.RPAREN, lexer.RBRACE, lexer.RBRACKET:
			if depth == 0 {
				return false // for 头部收口 ')'
			}
			depth--
		case lexer.OF:
			if depth == 0 && prev != lexer.DOT && prev != lexer.OPTIONAL_CHAIN {
				return true
			}
		}
		prev = tok.Type
	}
	return false
}

// forBindingKeyword 报告 `for (` 之后的 let/const 头部到底是 for...of / for...in
// (返回 OF / IN), 还是传统 for 的第一段 (返回 ILLEGAL)。
//
// 为什么不能只做 peek2 判定: 解构绑定的 `[a, b]` / `{a}` 后面隔着一整个模式才是
// 关键字, peek2 看到的是 `[` / `{`。当初就是因为只看 peek2, `for (const [a, b] of
// pairs)` 被判成传统 for, 于是 `[a, b]` 被当成解构声明去要 `=`, 报出
// 「expected = after destructuring, got OF」—— 照这句话排查会以为自己少写了等号。
func (p *Parser) forBindingKeyword() lexer.TokenType {
	peek := p.peekToken()
	// 绑定名可以是标识符, 也可以是上下文关键字 (yield 在 sloppy 非生成器代码里
	// 是合法绑定名: `for (var yield of xs)`; async 同理)。这三种 token 之后
	// peek2 若是 of/in 即对应形态。
	if peek.Type == lexer.IDENTIFIER ||
		(peek.Type == lexer.YIELD && p.yieldIsIdentifier()) {
		if p.peek2TokenIs(lexer.OF) {
			return lexer.OF
		}
		if p.peek2TokenIs(lexer.IN) {
			return lexer.IN
		}
		return lexer.ILLEGAL
	}
	if peek.Type != lexer.LBRACKET && peek.Type != lexer.LBRACE {
		return lexer.ILLEGAL
	}
	open, close := lexer.LBRACKET, lexer.RBRACKET
	if peek.Type == lexer.LBRACE {
		open, close = lexer.LBRACE, lexer.RBRACE
	}
	// 扫描到配对的闭合符 (有距离上限: 不闭合的写法会一路扫到 EOF)
	depth := 0
	for i := 1; i <= maxArrowScanLimit; i++ {
		switch p.peekTokenAt(i).Type {
		case lexer.EOF:
			return lexer.ILLEGAL
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				switch p.peekTokenAt(i + 1).Type {
				case lexer.OF:
					return lexer.OF
				case lexer.IN:
					return lexer.IN
				}
				return lexer.ILLEGAL
			}
		}
	}
	return lexer.ILLEGAL
}

func (p *Parser) parseForInStatement() *ast.ForInStatement {
	stmt := &ast.ForInStatement{Token: p.curToken()}
	isLet := p.curTokenIs(lexer.LET)
	isVar := p.curTokenIs(lexer.VAR)
	p.nextToken() // skip let/const/var

	if !p.isBindingName() {
		p.addError("expected variable name in for...in")
		return nil
	}
	variable := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	stmt.Variable = variable

	switch {
	case isLet:
		stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: variable}
	case isVar:
		stmt.VarDecl = &ast.VarStatement{Token: stmt.Token, Name: variable}
	default:
		stmt.VarDecl = &ast.ConstStatement{Token: stmt.Token, Name: variable}
	}

	if !p.expectPeek(lexer.IN) {
		return nil
	}
	p.nextToken()
	stmt.Iterable = p.parseExpression(LOWEST)

	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop
	return stmt
}

func (p *Parser) parseForOfStatement() *ast.ForOfStatement {
	stmt := &ast.ForOfStatement{Token: p.curToken()}
	isLet := p.curTokenIs(lexer.LET)
	isVar := p.curTokenIs(lexer.VAR)
	p.nextToken() // skip let/const/var

	if p.curTokenIs(lexer.LBRACKET) || p.curTokenIs(lexer.LBRACE) {
		// 解构绑定: for (const [a, b] of pairs) / for (const {a} of objs)
		pattern := p.parseDestructuringPattern(p.curTokenIs(lexer.LBRACKET))
		if pattern == nil {
			return nil
		}
		stmt.Pattern = pattern
		// VarDecl 是个只有合成名的空壳: 编译器只用它分辨 let / const (与
		// let [a, b] = … 的口径一致), 这个名字本身不代表任何绑定。
		name := &ast.Identifier{Token: stmt.Token, Value: "__destructure__"}
		switch {
		case isLet:
			stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: name}
		case isVar:
			stmt.VarDecl = &ast.VarStatement{Token: stmt.Token, Name: name}
		default:
			stmt.VarDecl = &ast.ConstStatement{Token: stmt.Token, Name: name}
		}
	} else {
		if !p.isBindingName() {
			p.addError(fmt.Sprintf(
				"expected variable name or destructuring pattern in for...of, got %s",
				p.curToken().Type))
			return nil
		}
		variable := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		stmt.Variable = variable

		switch {
		case isLet:
			stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: variable}
		case isVar:
			stmt.VarDecl = &ast.VarStatement{Token: stmt.Token, Name: variable}
		default:
			stmt.VarDecl = &ast.ConstStatement{Token: stmt.Token, Name: variable}
		}
	}

	if !p.expectPeek(lexer.OF) {
		return nil
	}
	stmt.Keyword = p.curToken()
	p.nextToken()
	stmt.Iterable = p.parseExpression(LOWEST)

	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop
	return stmt
}

// parseForAwaitOfStatement 解析 for await (binding of iterable) { body }。
// curToken 在 AWAIT 上 (peek 是 '(')。头部与 for-of 同构: 声明绑定复用
// parseForOfStatement, 无声明赋值目标 (for await (x of xs) / for await
// (async of xs), node 实测合法 —— for-await 头部没有 async-of 前瞻限制)
// 复用 parseForOfLHS。解析完成后给结果补 Await 标志。
//
// for await 只接受 of 形态: (…in…) 与带初始化器的声明头部都是早错。
// for await 只允许在 async 函数体内 —— 编译器在编译 for-await 时校验
// (parser 不跟踪 async 上下文, 避免两套状态源)。
func (p *Parser) parseForAwaitOfStatement(forToken lexer.Token) ast.Statement {
	p.nextToken() // cur = (
	p.nextToken() // cur = 头部第一 token

	var stmt *ast.ForOfStatement
	if p.curTokenIs(lexer.LET) || p.curTokenIs(lexer.CONST) || p.curTokenIs(lexer.VAR) {
		switch p.forBindingKeyword() {
		case lexer.OF:
			stmt = p.parseForOfStatement()
		case lexer.IN:
			// for-in 无异步形态 (node: Unexpected token 'in')
			p.addError("SyntaxError: for await loops must iterate with 'of', not 'in'")
			return nil
		default:
			// 含 for await (var x = 1 of y) 与传统三段式 (node: for-await-of
			// loop variable declaration may not have an initializer)
			p.addError("SyntaxError: for-await-of loop variable declaration may not have an initializer")
			return nil
		}
	} else if p.isForOfLHS() {
		stmt = p.parseForOfLHS(forToken, true)
	} else {
		p.addError("SyntaxError: for await loops must iterate with 'of'")
		return nil
	}
	if stmt == nil {
		return nil
	}
	stmt.Await = true
	return stmt
}

// parseForOfLHS 解析无声明关键字的 for-of 头部 (cur 是头部第一个 token):
//
//	for (x of xs)            标识符赋值目标
//	for (obj.k of xs)        成员访问赋值目标
//	for ([a, b] of xs)       数组解构目标 (每轮是**赋值**, 写入外部已有绑定)
//	for ({a} of xs)          对象解构目标
//
// isAwait 表示这是 for await 头部。node 22 实测口径:
//   - for (async of y):   同步 for-of 明确报 "left-hand side may not be 'async'";
//   - for await (async of y): for-await 头部没有该前瞻限制, async 作绑定名合法。
//
// await 开头的头部 (for (await of xs) / for await (await x of xs)) 不在这里
// 特判: parseExpression 会进 parseAwaitExpression, 非 async 上下文按标识符
// 处理 (node: for (await of xs) 合法)、await 操作数形态报早错, 而 awaited
// 目标会被下面的赋值目标白名单拒绝 (node: Invalid left-hand side)。
func (p *Parser) parseForOfLHS(forToken lexer.Token, isAwait bool) *ast.ForOfStatement {
	stmt := &ast.ForOfStatement{Token: forToken, Await: isAwait}

	if p.curTokenIs(lexer.ASYNC) && p.peekTokenIs(lexer.OF) {
		if !isAwait {
			p.addError("SyntaxError: The left-hand side of a for-of loop may not be 'async'")
			return nil
		}
		stmt.Target = &ast.Identifier{Token: p.curToken(), Value: "async"}
		p.nextToken() // cur = of
		stmt.Keyword = p.curToken()
		return p.finishForOf(stmt)
	}
	if p.curTokenIs(lexer.LBRACKET) || p.curTokenIs(lexer.LBRACE) {
		// 解构赋值目标: for ([a, b] of xs) —— 与声明绑定共用 Pattern 字段,
		// 由 VarDecl 是否为 nil 区分 (nil = 赋值解构, 每轮写外部绑定)
		pattern := p.parseDestructuringPattern(p.curTokenIs(lexer.LBRACKET))
		if pattern == nil {
			return nil
		}
		stmt.Pattern = pattern
		if !p.expectPeek(lexer.OF) {
			return nil
		}
		stmt.Keyword = p.curToken()
		return p.finishForOf(stmt)
	}

	target := p.parseExpression(LOWEST)
	if target == nil {
		return nil
	}
	switch target.(type) {
	case *ast.Identifier, *ast.MemberExpression:
		// 可作 for-of 赋值目标: 标识符 / 成员访问
	default:
		p.addError("SyntaxError: invalid left-hand side in for...of loop")
		return nil
	}
	stmt.Target = target

	if !p.expectPeek(lexer.OF) {
		return nil
	}
	stmt.Keyword = p.curToken()
	return p.finishForOf(stmt)
}

// finishForOf 从 OF 上继续: 解析可迭代对象、循环体。parseForOfStatement 与
// parseForOfLHS 的公共收尾。
func (p *Parser) finishForOf(stmt *ast.ForOfStatement) *ast.ForOfStatement {
	p.nextToken()
	stmt.Iterable = p.parseExpression(LOWEST)

	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop
	return stmt
}

func (p *Parser) parseTraditionalFor() *ast.ForStatement {
	stmt := &ast.ForStatement{Token: p.curToken()}

	// ---- init ----
	// 注意: parseExpression 结束后 curToken 停在最后一个表达式 token 上;
	// consumeSemicolon() 只把 curToken 移到 ';' 上 (而非越过它)。
	// 因此每段解析完必须再 nextToken 一次, 才能让 curToken 落在下一段的开头。
	if p.curTokenIs(lexer.LET) || p.curTokenIs(lexer.CONST) {
		var varStmt ast.Statement
		if p.curTokenIs(lexer.LET) {
			varStmt = p.parseLetStatement()
		} else {
			varStmt = p.parseConstStatement()
		}
		stmt.Init = varStmt
		// parseLetStatement 内 consumeSemicolon 后 curToken 停在 ';' 上,
		// 前进一步使其落在 condition 开头。
		p.nextToken()
	} else if p.curTokenIs(lexer.VAR) {
		// for (var i = 0; ...): var 声明语法与 let 同构, 语义 (函数作用域)
		// 由编译器落点决定
		varStmt := p.parseVarStatement()
		if varStmt == nil {
			return nil
		}
		stmt.Init = varStmt
		// parseVarStatement 内 consumeSemicolon 后 curToken 停在 ';' 上,
		// 前进一步使其落在 condition 开头。
		p.nextToken()
	} else if !p.curTokenIs(lexer.SEMICOLON) {
		expr := p.parseCommaSequence()
		stmt.Init = &ast.ExpressionStatement{Token: p.curToken(), Expression: expr}
		p.consumeSemicolon()
		// 同上: curToken 停在 ';' 上, 前进一步到 condition 开头。
		p.nextToken()
	} else {
		p.nextToken() // 空 init: 越过第一个 ';' 到 condition 开头 (或第二个 ';')
	}

	return p.finishTraditionalFor(stmt)
}

// finishTraditionalFor 解析传统 for 的 condition / update / 循环体。
// 进入时 stmt.Init 已就绪, curToken 停在 init 之后的 condition 开头。
func (p *Parser) finishTraditionalFor(stmt *ast.ForStatement) *ast.ForStatement {
	// ---- condition ----
	// 解析后 curToken 停在最后一个 condition token 上, peek 是 ';',
	// consumeSemicolon 把 curToken 移到 ';' 上。
	if !p.curTokenIs(lexer.SEMICOLON) {
		stmt.Condition = p.parseExpression(LOWEST)
		p.consumeSemicolon()
	}

	// 此时 curToken 要么在 ';' 上 (有 condition 或空 condition), 要么已在 update 开头。
	if p.curTokenIs(lexer.SEMICOLON) {
		p.nextToken() // 越过 ';' 到 update 开头 (或 ')' 表示无 update)
	}

	// ---- update ----
	// 解析后 curToken 停在 update 最后一个 token 上, peek 是 ')'。
	if !p.curTokenIs(lexer.RPAREN) {
		expr := p.parseCommaSequence()
		stmt.Update = &ast.ExpressionStatement{Token: p.curToken(), Expression: expr}
	}

	// ---- 收尾: 确保 curToken 在 ')' 上 ----
	if !p.curTokenIs(lexer.RPAREN) {
		if !p.expectPeek(lexer.RPAREN) {
			return nil
		}
	}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop
	return stmt
}

// parseForWithUsingHead 解析 for 头部以 using / await using 声明开头的形态:
//
//	for (using x = expr; cond; upd) body   传统三段式 (释放落点是整个 for)
//	for (using x of iterable) body          for-of (每轮重新登记并释放)
//	for (await using x = expr; ...)         async 上下文的 await using
//
// 进入时 curToken 在 using (await using 时在 await)。for-in 头部用 using 是
// 早错 (test262 using-invalid-for-in.js)。
func (p *Parser) parseForWithUsingHead(forToken lexer.Token) ast.Statement {
	isAwait := false
	if p.curTokenIs(lexer.AWAIT) {
		isAwait = true
		p.nextToken() // cur = using
	}
	usingTok := p.curToken()
	p.nextToken() // cur = 绑定名

	if !p.isUsingBindingName() {
		p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
		return nil
	}
	name := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}

	switch p.peekToken().Type {
	case lexer.OF:
		// for (using x of iterable): 头部声明项无初始化器 (迭代变量)。
		decl := &ast.UsingStatement{Token: usingTok, Name: name, IsAwait: isAwait, InForHead: true}
		stmt := &ast.ForOfStatement{Token: forToken, VarDecl: decl, Variable: name}
		p.nextToken() // cur = of
		stmt.Keyword = p.curToken()
		p.nextToken() // 迭代表达式首 token
		stmt.Iterable = p.parseExpression(LOWEST)
		if !p.expectPeek(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		prevInLoop := p.inLoop
		p.inLoop = true
		stmt.Body = p.parseBody()
		p.inLoop = prevInLoop
		return stmt
	case lexer.ASSIGN:
		// for (using x = expr; cond; upd): 传统三段式。
		p.nextToken() // =
		p.nextToken() // 初始化器首 token
		value := p.parseExpression(LOWEST)
		decl := &ast.UsingStatement{Token: usingTok, Name: name, Value: value, IsAwait: isAwait, InForHead: true}
		stmt := &ast.ForStatement{Token: forToken, Init: decl}
		p.consumeSemicolon()
		p.nextToken() // 越过 ';' 到 condition
		return p.finishTraditionalFor(stmt)
	case lexer.IN:
		p.addError("SyntaxError: for-in loop variable declaration may not use 'using'")
		return nil
	default:
		p.addError("SyntaxError: using declaration requires an initializer")
		return nil
	}
}

func (p *Parser) parseWhileStatement() *ast.WhileStatement {
	stmt := &ast.WhileStatement{Token: p.curToken()}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop
	return stmt
}

// parseDoWhileStatement 解析 do { body } while (cond);
func (p *Parser) parseDoWhileStatement() *ast.DoWhileStatement {
	stmt := &ast.DoWhileStatement{Token: p.curToken()}
	p.nextToken()
	prevInLoop := p.inLoop
	p.inLoop = true
	stmt.Body = p.parseBody()
	p.inLoop = prevInLoop

	// 期望 while 关键字
	if !p.expectPeek(lexer.WHILE) {
		return nil
	}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()
	stmt.Condition = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	// 注意: 这里不能消费 while (...) 之后的 token。
	// 约定是语句解析结束后 curToken 停留在语句的最后一个 token (RPAREN),
	// 由 ParseProgram / parseBlockStatement 循环统一 nextToken 前进。
	// 若在此处提前 nextToken 会越过下一条语句的首个 token,
	// 导致 do {...} while (c); \n console.log(...) 这类无分号写法解析错乱。
	return stmt
}

// withStrictForbidden 报告当前解析上下文是否应因严格模式禁止 with 语句。
//
// 本仓库尚无严格模式基础设施 (另一路 agent 正在实现)。这里刻意做成一个
// **单一挂载点** 且当前恒为 false —— 即暂不判定 strict。合流时只需把下面
// 这一行替换为「读取当前解析单元/函数的 strict 状态」的实现即可, 其余
// 代码 (parseWithStatement 的调用点) 无需改动。
var withStrictForbidden = func() bool { return false }

// parseWithStatement 解析 with ( 表达式 ) 语句。
//
// 语法: WithStatement : `with` `(` Expression `)` Statement。
// 早期错误: 严格模式下 with 语句是 SyntaxError (见 withStrictForbidden)。
func (p *Parser) parseWithStatement() *ast.WithStatement {
	if withStrictForbidden() {
		p.addError("SyntaxError: 'with' statements are not allowed in strict mode")
		return nil
	}
	stmt := &ast.WithStatement{Token: p.curToken()}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()
	// with 头是 Expression —— 允许逗号序列: with (a, b, obj) {...}。
	// (test262 statements/with/scope-var-open.js 依赖此点; parseExpression
	// 本身刻意不吞逗号, 故用 parseCommaSequence 收集。)
	stmt.Object = p.parseCommaSequence()
	if stmt.Object == nil {
		return nil
	}
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	// 语法: WithStatement 的语句体必须是 Statement —— 不能是 Declaration。
	// `let`/`const`/`class`/function 声明作 with 体是**早期 SyntaxError**
	// (test262 statements/with/decl-*)。`var` 是 VariableStatement, 属
	// Statement, 仍合法。
	// 例外: `let` 后紧跟换行时按标识符表达式处理 (ASI), 与 parseStatement 的
	// LET 分支同口径 —— let-block/let-identifier-with-newline.js 依赖此点。
	if p.curTokenIs(lexer.CONST) || p.curTokenIs(lexer.CLASS) || p.curTokenIs(lexer.FUNCTION) ||
		(p.curTokenIs(lexer.ASYNC) && p.peekTokenIs(lexer.FUNCTION)) ||
		(p.curTokenIs(lexer.LET) && p.peekToken().Line == p.curToken().Line) {
		p.addError("SyntaxError: declaration is not allowed as the body of a 'with' statement")
		return nil
	}
	// 语句体: 块或单条语句。语句位置上的 '{' 一定是块 (而非对象字面量),
	// 与 parseLabeledStatement 同口径 —— 不能直接交给 parseStatement, 否则
	// 会落进 isBlockStart 的启发式判定, 把 `{ x; }` 误当对象字面量。
	// parseBlockStatement / parseStatement 返回时 curToken 停在该语句的末
	// token 上 (与 if/while 的 parseBody 一致), 由调用循环统一前进。
	prevUsingAllowed := p.usingAllowed
	p.usingAllowed = false // with 体单语句位置不是 Block ⇒ using 非法
	if p.curTokenIs(lexer.LBRACE) {
		stmt.Body = p.parseBlockStatement()
	} else {
		stmt.Body = p.parseStatement()
	}
	p.usingAllowed = prevUsingAllowed
	return stmt
}

func (p *Parser) parseBreakStatement() *ast.BreakStatement {
	stmt := &ast.BreakStatement{Token: p.curToken()}
	// 可选标签: break outer;
	// ASI 规则: 标签必须与 break 同一行, 换行后的标识符是下一条语句,
	// 不能当作标签 (否则 break\narr.push(...) 会被误解析为 break arr)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Line == p.curToken().Line {
		p.nextToken()
		stmt.Label = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseContinueStatement() *ast.ContinueStatement {
	stmt := &ast.ContinueStatement{Token: p.curToken()}
	// 可选标签: continue outer;
	// ASI 规则: 标签必须与 continue 同一行 (同 parseBreakStatement)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Line == p.curToken().Line {
		p.nextToken()
		stmt.Label = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// parseLabeledStatement 解析标签语句: label: statement
func (p *Parser) parseLabeledStatement() *ast.LabeledStatement {
	// LabelIdentifier : Identifier 的早错误 (规范 14.13.1 / 看板单 rDc5ui):
	// It is a Syntax Error if this production has a [Yield] parameter and
	// StringValue of Identifier is "yield" —— 生成器/async-generator 体内
	// (allowYield=true) yield 不得作标签名 (test262 {language/expressions,
	// language/statements}/async-generator/[named-]yield-as-label-identifier.js)。
	// sloppy 非生成器代码里 yield 是普通 IdentifierName, `yield: ;` 仍是
	// 合法标签语句; `var yield = 1` / `yield = 2` 不受影响。
	if p.curTokenIs(lexer.YIELD) {
		if p.allowYield {
			p.addError("SyntaxError: yield is not allowed as a label identifier in a generator function")
		} else if p.strict {
			// 严格模式代码里 yield 是保留字, 同样不得作标签名
			// (规范 12.1.1; test262 language/statements/labeled/value-yield-strict.js,
			// flags: onlyStrict —— sloppy 的 `yield: 1;` 仍合法, 见 -non-strict 一条)。
			p.addError("SyntaxError: yield is a reserved word and may not be used as a label in strict mode code")
		}
	}
	// 转义拼出的保留字作标签名同样是早错 (LabelIdentifier : Identifier):
	// test262 reserved-words/label-ident-{false,null,true}-escaped.js、
	// */{await,yield}-as-label-identifier-escaped.js、labeled/value-{await-module,
	// yield-strict}-escaped.js; sloppy 的 value-{await-non-module,yield-non-strict}-
	// escaped.js 必须放行 (由 escapedNameForbiddenAsIdent 的上下文判据区分)。
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().IdentHasEscape &&
		p.escapedNameForbiddenAsIdent(p.curToken().Literal, p.strict) {
		p.addError("SyntaxError: Keyword must not contain escaped characters")
	}
	stmt := &ast.LabeledStatement{
		Token: p.curToken(),
		Label: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal},
	}
	p.nextToken() // 跳过 label 标识符
	p.nextToken() // 跳过 ':'
	// 标签体不是模块顶层 —— `test262: export default null;` 非法
	// (spec: ModuleItem, test262 module-code/parse-err-decl-pos-export-labeled.js)。
	prevTop := p.moduleTopLevel
	p.moduleTopLevel = false
	defer func() { p.moduleTopLevel = prevTop }()
	// 标签后的 '{' 一定是块 (语句位置), 而非对象字面量
	prevUsingAllowed := p.usingAllowed
	p.usingAllowed = false // label: Statement 的语句位置不是 Block ⇒ using 非法
	if p.curTokenIs(lexer.LBRACE) {
		stmt.Body = p.parseBlockStatement()
	} else {
		stmt.Body = p.parseStatement()
	}
	p.usingAllowed = prevUsingAllowed
	return stmt
}

// parseBlockStatement 解析普通块语句 { ... }, 做块级早错检查。
func (p *Parser) parseBlockStatement() *ast.BlockStatement {
	return p.parseBlockImpl(false)
}

// parseBlockStatementAt 是 parseBlockStatement 的显式参数版本。
// asFunctionBody 为 true 时豁免本层块的「lexical ∩ var」重声明早错 (函数体
// 专用: `function g(){ var f; function f(){} }` 合法)。内层普通块不受影响。
func (p *Parser) parseBlockStatementAt(asFunctionBody bool) *ast.BlockStatement {
	return p.parseBlockImpl(asFunctionBody)
}

// parseFunctionBody 解析函数体 { ... }: 豁免本层的「lexical ∩ var」重声明早错。
// 所有调用方都是**非箭头**函数体 (类方法/访问器/构造器/export default 匿名
// 函数), 期间 new.target 合法, 故在此统一置位 (而不是逐调用点包装)。
// isGenerator 决定体内 yield 语境 (生成器方法体内 yield 不得作标签标识符),
// 存取器/构造器恒传 false。
func (p *Parser) parseFunctionBody(isGenerator bool) *ast.BlockStatement {
	p.fnDepth++
	defer func() { p.fnDepth-- }()
	restoreNT := p.enterNewTargetScope(true)
	defer restoreNT()
	restoreYield := p.setAllowYield(isGenerator)
	defer restoreYield()
	return p.parseBlockImpl(true)
}

func (p *Parser) parseBlockImpl(asFunctionBody bool) *ast.BlockStatement {
	if !p.enterNesting("block") {
		return nil
	}
	defer p.leaveNesting()

	block := &ast.BlockStatement{Token: p.curToken()}
	block.Statements = []ast.Statement{}

	if !p.curTokenIs(lexer.LBRACE) {
		p.addError(fmt.Sprintf("expected '{', got %s", p.curToken().Type))
		return nil
	}
	p.nextToken()

	// 块内的语句不是模块顶层 —— import/export 在此非法 (spec: ModuleItem)。
	prevTop := p.moduleTopLevel
	p.moduleTopLevel = false
	defer func() { p.moduleTopLevel = prevTop }()

	// 块/函数体内的 `using` 声明合法 (规范允许 Block / FunctionBody)。
	p.blockOrFnDepth++
	prevUsingAllowed := p.usingAllowed
	p.usingAllowed = true
	defer func() {
		p.blockOrFnDepth--
		p.usingAllowed = prevUsingAllowed
	}()

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		stmt := p.parseStatement()
		if !isNilStmt(stmt) {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
	}
	// 块级早错: LexicallyDeclaredNames ∩ VarDeclaredNames ≠ ∅ ⇒ SyntaxError
	// (sec-block-static-semantics-early-errors)。见 block_early_errors.go。
	// 函数体 (asFunctionBody=true) 豁免 —— 那里 var 与同名 function 声明合法;
	// 但函数体**内层**的普通块仍照查 (豁免只作用于这一层)。
	if !asFunctionBody {
		p.checkBlockRedeclaration(block)
	}
	return block
}

// parseBlockWithDirectives 解析函数体块 { ... }, 并在开头识别 Directive
// Prologue: 命中精确 "use strict" 时**立即**把 p.strict 置 true, 使体内
// 其后语句按严格模式解析 (早错生效); 返回该体是否含指令。只用于
// FunctionBody/ClassBody —— 普通块 (if/while/try 体) 里的 "use strict"
// **不是**指令, 必须走 parseBlockStatement, 绝不能共用本函数。
func (p *Parser) parseBlockWithDirectives() (*ast.BlockStatement, bool) {
	if !p.enterNesting("block") {
		return nil, false
	}
	defer p.leaveNesting()

	block := &ast.BlockStatement{Token: p.curToken()}
	block.Statements = []ast.Statement{}
	if !p.curTokenIs(lexer.LBRACE) {
		p.addError(fmt.Sprintf("expected '{', got %s", p.curToken().Type))
		return nil, false
	}
	p.nextToken()

	// 函数体内的语句不是模块顶层 —— import/export 在此非法 (spec: ModuleItem)。
	// 与 parseBlockImpl 一致 (block-ee rUZN3k 第1块); 本函数专用于 FunctionBody。
	prevTop := p.moduleTopLevel
	p.moduleTopLevel = false
	defer func() { p.moduleTopLevel = prevTop }()

	// 函数体内 `using` 声明合法。
	p.blockOrFnDepth++
	prevUsingAllowed := p.usingAllowed
	p.usingAllowed = true
	defer func() {
		p.blockOrFnDepth--
		p.usingAllowed = prevUsingAllowed
	}()

	inPrologue := true
	bodyStrict := false
	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		startIsString := p.curTokenIs(lexer.STRING_LITERAL)
		stmt := p.parseStatement()
		if !isNilStmt(stmt) {
			block.Statements = append(block.Statements, stmt)
			if inPrologue {
				if sl, ok := directiveString(stmt, startIsString); ok {
					if isUseStrictDirective(sl) {
						bodyStrict = true
						p.strict = true
					}
				} else {
					inPrologue = false
				}
			}
		}
		p.nextToken()
	}
	// 函数体豁免本层「lexical ∩ var」重声明早错: 在**函数体**里 function
	// 声明是 var 作用域 (不像块里那样算 lexical), 故 `function g(){ var f;
	// function f(){} }` 合法 —— 与 parseBlockImpl(asFunctionBody=true) 一致
	// (block-ee rUZN3k 第1块)。内层普通块仍走 parseBlockStatement 照查。
	return block, bodyStrict
}

// parseFunctionBodyWithStrict 解析函数体块 (function/箭头/方法体), 处理
// async 上下文与 Directive Prologue。返回值: 体本身, 以及该函数**生效**的
// strict (继承值 || 体自身含 "use strict" 指令)。退出后 p.strict 恢复为
// 进入时的继承值 —— 指令只作用于本函数体, 不泄漏给后续兄弟语句。
// isGenerator 决定体内的 yield 语境 (生成器体内 yield 是保留字, 不得作
// 标签标识符); 箭头函数调用方传**继承值** p.allowYield (见 setAllowYield)。
func (p *Parser) parseFunctionBodyWithStrict(isAsync bool, isGenerator bool) (*ast.BlockStatement, bool) {
	inherited := p.strict
	restoreAwait := p.setAllowAwait(isAsync)
	restoreYield := p.setAllowYield(isGenerator)
	p.fnDepth++
	body, bodyStrict := p.parseBlockWithDirectives()
	p.fnDepth--
	restoreAwait()
	restoreYield()
	p.strict = inherited
	// 记下「本体自身含 use strict 指令」(区别于继承来的 strict), 供
	// checkUseStrictNonSimpleParams 判定 14.1.2 早错。
	p.lastBodyUsesStrict = bodyStrict
	return body, inherited || bodyStrict
}

// checkUseStrictWithNonSimpleParams 报告「函数体含 use strict 指令」与
// 「非简单形参列表」并存的早错 (规范 14.1.2 Static Semantics: Early Errors):
//
//	FunctionBody : FunctionStatementList
//	  - It is a Syntax Error if FunctionBodyContainsUseStrict is true and
//	    IsSimpleParameterList of FormalParameters is false.
//
// 必须在 parseFunctionBodyWithStrict 返回后立即调用 (此时 lastBodyUsesStrict
// 即本次体自身的值)。继承来的 strict 不触发本规则。
func (p *Parser) checkUseStrictWithNonSimpleParams(params []*ast.Parameter) {
	if p.lastBodyUsesStrict && !isSimpleParameterList(params) {
		p.addError("SyntaxError: 'use strict' directive is not allowed with a non-simple parameter list")
	}
}

// parseNonArrowFunctionBody 解析**非箭头**函数体 (function 声明/表达式、async
// 函数、对象方法/访问器): 期间 new.target 合法。与 parseFunctionBodyWithStrict
// 分开, 是因为箭头函数体也走后者, 而箭头对 new.target 是词法透明的 (要继承外层
// 合法性, 不能在此置 true)。
func (p *Parser) parseNonArrowFunctionBody(isAsync bool, isGenerator bool) (*ast.BlockStatement, bool) {
	restoreNT := p.enterNewTargetScope(true)
	defer restoreNT()
	return p.parseFunctionBodyWithStrict(isAsync, isGenerator)
}

// parseBody 解析语句体: 若当前是 { 则解析代码块, 否则解析单条语句并包装为块。
// 用于支持 if (x) y++; 这类不带花括号的单语句体。
func (p *Parser) parseBody() *ast.BlockStatement {
	if p.curTokenIs(lexer.LBRACE) {
		return p.parseBlockStatement()
	}
	// 单条语句: 包装为 BlockStatement。
	// 这里同样不是模块顶层 —— `if (x) { } else export default null;` 非法
	// (test262 module-code/parse-err-decl-pos-export-if-else.js)。
	// 单语句体不是 Block ⇒ using 声明在此非法 (`if (x) using y = null;`)。
	prevTop := p.moduleTopLevel
	p.moduleTopLevel = false
	prevUsingAllowed := p.usingAllowed
	p.usingAllowed = false
	defer func() {
		p.moduleTopLevel = prevTop
		p.usingAllowed = prevUsingAllowed
	}()
	block := &ast.BlockStatement{Token: p.curToken(), Statements: []ast.Statement{}}
	stmt := p.parseStatement()
	if !isNilStmt(stmt) {
		block.Statements = append(block.Statements, stmt)
	}
	// 单语句后不自动消费分号外的 token (由调用方处理)
	return block
}

// parseFunctionDeclaration 解析 function / async function 声明。
// isAsync 由调用方给出 (async function 的 ASYNC 前缀在调用前已消费):
// 它同时决定函数体内是否允许 await。
func (p *Parser) parseFunctionDeclaration(isAsync bool) *ast.FunctionDeclaration {
	// 进入时 curToken 是 FUNCTION (async 分支已把 curToken 移到 FUNCTION)
	fn := &ast.FunctionDeclaration{Token: p.curToken(), IsAsync: isAsync}
	if p.peekTokenIs(lexer.ASTERISK) {
		fn.IsGenerator = true
		p.nextToken() // 移到 *
		p.nextToken() // 移到函数名
	} else if !p.expectPeek(lexer.IDENTIFIER) {
		return nil
	}
	fn.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	fn.Parameters = p.parseParameters(lexer.RPAREN, isAsync, fn.IsGenerator)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	ntRestore := p.enterNewTargetScope(true)
	fn.Body, fn.Strict = p.parseFunctionBodyWithStrict(isAsync, fn.IsGenerator)
	p.checkUseStrictWithNonSimpleParams(fn.Parameters)
	ntRestore()
	if fn.Strict {
		p.checkStrictFunctionParams(fn.Parameters)
	}
	return fn
}

// ==================== 参数解析 ====================

// parseParameters 解析形参列表。isAsyncFn 是**所属函数自身的 async 与否**
// (非外层上下文): 为真时形参区 await 是保留字 (见 awaitReservedInParams)。
// isGeneratorFn 是**所属函数自身的 generator 与否**: 生成器的形参是
// FormalParameters[+Yield], 故其形参区里 yield 是保留字 —— function*(yield){}
// / function*(x = yield){} / ({*m(yield){}}) / class{*m(yield){}} 皆 SyntaxError
// (test262 generators/yield-as-parameter.js、param-dflt-yield.js 一族)。
// 非生成器函数形参是 [~Yield], 重置为 false。
// 箭头函数没有自己的 [Yield] 参数, 调用方传 p.allowYield 继承外层 (见下)。
// 覆盖普通函数/箭头/async/生成器/对象方法/类方法全部入参点。
func (p *Parser) parseParameters(close lexer.TokenType, isAsyncFn bool, isGeneratorFn bool) []*ast.Parameter {
	// 形参区恒为 ~Await 上下文 (规范 FormalParameters[~Yield, ~Await]):
	// 即便所在函数是 async、即便处于模块顶层 (+Await) 或另一个 async 体内,
	// 形参默认值里出现 await 表达式都是 SyntaxError
	// (test262 top-level-await/syntax/early-does-not-propagate-to-fn-declaration-params.js)。
	// 显式重置, 避免继承外层 +Await。
	// awaitReservedInParams 随 setAllowAwait 的保存/恢复通道一起结转。
	restoreAwait := p.setAllowAwait(false)
	p.awaitReservedInParams = isAsyncFn
	defer restoreAwait()
	// 形参区的 [Yield]: 生成器 [+Yield] (yield 作形参名/默认值为早错);
	// 其余按所属函数自身 generator 与否重置, 箭头由调用方传继承值。
	restoreYield := p.setAllowYield(isGeneratorFn)
	defer restoreYield()
	// 形参窗口: 此窗口内不得出现 YieldExpression (见 yieldReservedInParams)。
	// 仅在 yield 为保留字的语境里才可能触发 (sloppy 非生成器里 yield 是普通
	// 标识符, 走 parseYieldExpression 的标识符分支, 不报错)。
	p.yieldReservedInParams = true

	params := []*ast.Parameter{}

	if p.peekTokenIs(close) {
		p.nextToken()
		return params
	}
	p.nextToken()

	for {
		param := p.parseParameter()
		if param != nil {
			params = append(params, param)
		}
		// Early error (规范 15.1 FormalsList / 14.1.2): rest 参数之后不得再有任何
		// 参数, 连尾逗号也不行 —— function f(...a, b){} 与 f(...a,) 都是 SyntaxError
		// (Node 实测一致)。注意内层解构模式里的 rest 不受影响 (f([x, ...y], z) 合法)。
		if param != nil && param.Rest {
			if p.peekTokenIs(lexer.COMMA) {
				p.addError("SyntaxError: rest parameter must be the last formal parameter")
				return nil
			}
			break
		}
		if !p.peekTokenIs(lexer.COMMA) {
			break
		}
		p.nextToken() // skip ,
		// 尾逗号 function (a, b,) {} / (a, b,) => … —— 与 parseArguments
		// 同一口径 (ES 允许, 语义无差异; 字面量与解构早就支持)。
		// 不收它时的报错是 "expected parameter name, got RPAREN", 看着像
		// 参数写漏了, 实际只是多了一个逗号。
		if p.peekTokenIs(close) {
			break // 交给下面统一的 expectPeek(close) 收尾
		}
		p.nextToken()
	}

	if !p.expectPeek(close) {
		return nil
	}
	// Early error (规范 14.1.2): 非简单形参列表里出现重复绑定名 ⇒ SyntaxError,
	// 与 strict 无关。在 parseParameters 收尾处统一校验, 覆盖普通函数/箭头/
	// async/生成器/对象方法/类方法全部入参点 (此检查纯语法, 不依赖函数体)。
	p.checkNonSimpleDuplicateParams(params)
	return params
}

func (p *Parser) parseParameter() *ast.Parameter {
	param := &ast.Parameter{Token: p.curToken()}

	if p.curTokenIs(lexer.SPREAD_REST) {
		param.Rest = true
		p.nextToken()
	}

	// 解构模式参数: function f({a, b}, [c, d])
	if p.curTokenIs(lexer.LBRACE) || p.curTokenIs(lexer.LBRACKET) {
		param.Pattern = p.parseDestructuringPattern(p.curTokenIs(lexer.LBRACKET))
		if param.Pattern == nil {
			return nil
		}
		// 整个模式可带默认值: function f({a} = {a: 1})
		if p.peekTokenIs(lexer.ASSIGN) {
			p.nextToken()
			p.nextToken()
			param.Default = p.parseExpression(LOWEST)
		}
		p.checkRestParamInitializer(param)
		return param
	}

	if !p.isBindingName() {
		p.addError(fmt.Sprintf("expected parameter name, got %s", p.curToken().Type))
		return nil
	}
	param.Name = p.curToken().Literal

	if p.peekTokenIs(lexer.ASSIGN) {
		p.nextToken()
		p.nextToken()
		param.Default = p.parseExpression(LOWEST)
	}
	p.checkRestParamInitializer(param)
	return param
}

// checkRestParamInitializer 报告 rest 形参带初始化器这一早错。
// 规范 (ES2023 §14.1 FunctionRestParameter / §13.3.3 BindingRestElement):
// BindingRestElement 只允许 `...BindingIdentifier` / `...BindingPattern`,
// **不带** Initializer —— function f(...x = []) {} 是 SyntaxError (Node 实测一致)。
// 注意 rest 后接**解构模式** (function f(...[a]) {}) 本身合法, 只有再接 `= 默认值`
// 才是早错, 故判据是 Rest && Default != nil 而非 Rest && Pattern != nil。
func (p *Parser) checkRestParamInitializer(param *ast.Parameter) {
	if param != nil && param.Rest && param.Default != nil {
		p.addError("SyntaxError: rest parameter may not have a default initializer")
	}
}

// ==================== 表达式解析 (Pratt Parsing) ====================

func (p *Parser) parseExpression(precedence Precedence) ast.Expression {
	if !p.enterNesting("expression") {
		return nil
	}
	defer p.leaveNesting()

	prefix := p.prefixParseFns[p.curToken().Type]
	if prefix == nil {
		p.addError(fmt.Sprintf("no prefix parse function for %s found", p.curToken().Type))
		return nil
	}

	leftExp := prefix()
	// 空 yield (YieldExpression : yield, 无操作数) 自身已是完整的
	// AssignmentExpression, 不能作任何中缀运算符的操作数 —— 中缀循环必须
	// 在此立即收尾。否则 `yield\n* 1` 会被误当作 `(yield) * 1` 而静默通过,
	// 而规范要求它报 SyntaxError (test262 generators/yield-star-after-newline.js:
	// 换行后单独的 `*` 无法起头语句); `yield\n+1` 同理走 ASI 成为两条语句。
	// 非空 yield (Value≠nil) 与委托 yield* (Delegate) 不受影响。
	if ye, ok := leftExp.(*ast.YieldExpression); ok && ye.Value == nil && !ye.Delegate {
		return leftExp
	}

	for !p.peekTokenIs(lexer.SEMICOLON) && !p.peekTokenIs(lexer.RBRACE) && !p.peekTokenIs(lexer.EOF) &&
		!p.peekTokenIs(lexer.RPAREN) && !p.peekTokenIs(lexer.RBRACKET) && !p.peekTokenIs(lexer.COMMA) &&
		!p.peekTokenIs(lexer.COLON) && !p.peekTokenIs(lexer.ARROW) &&
		(precedence < getPrecedence(p.peekToken().Literal) || p.peekTokenIs(lexer.BACKTICK)) {
		// 后缀 ++/-- 的规范限制 (ES2023 §13.4 PostfixExpression): 操作数与运算符
		// 之间**不得有行终止符**。`x\n++y` 必须是 `x; ++y;`, 而不是 `(x++); y`。
		// 这里在把 ++/-- 当中缀前拦一道: 跨行就收尾本表达式, 交给语句层把
		// ++/-- 当作下一条语句的前缀运算符 (缺少操作数时自然报 SyntaxError)。
		if (p.peekTokenIs(lexer.INC) || p.peekTokenIs(lexer.DEC)) &&
			p.peekToken().Line != p.curToken().Line {
			break
		}
		// tagged template: 表达式后紧跟模板 (BACKTICK 类型, 优先级同函数调用)
		if p.peekTokenIs(lexer.BACKTICK) {
			p.nextToken()
			leftExp = p.parseTaggedTemplate(leftExp)
			continue
		}
		infix := p.infixParseFns[p.peekToken().Type]
		if infix == nil {
			return leftExp
		}
		p.nextToken()
		leftExp = infix(leftExp)
	}
	return leftExp
}

// --- 前缀解析函数 ---

func (p *Parser) parseIdentifier() ast.Expression {
	// 单参数箭头函数: identifier => body
	if p.peekTokenIs(lexer.ARROW) {
		ident := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		p.rejectEscapedReservedIdentifier()
		p.nextToken() // consume =>
		return p.parseArrowFunctionBody([]*ast.Parameter{{
			Token: ident.Token, Name: ident.Value,
		}}, false)
	}
	p.rejectEscapedReservedIdentifier()
	return &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
}

// rejectEscapedReservedIdentifier 报告「用 unicode 转义拼出保留字作
// IdentifierReference」的早错 (11.6.1: Identifier : IdentifierName but not
// ReservedWord; 转义拼出的保留字不是关键字但也不是合法 Identifier)。
// 只在**引用**位置调用 —— 属性名 (IdentifierName) / 成员名允许转义拼出保留字,
// 那些路径不经过 parseIdentifier。绑定名 (isBindingName) 与标签名
// (parseLabeledStatement) 各自有同口径的调用点。
func (p *Parser) rejectEscapedReservedIdentifier() {
	tok := p.curToken()
	if tok.Type != lexer.IDENTIFIER || !tok.IdentHasEscape {
		return
	}
	p.rejectEscapedReservedName(tok.Literal)
}

// rejectEscapedReservedName 判定一个 (由转义拼出的) 名字是否为保留字并记错。
// 语境按当前 p.strict 取; 类名/类体恒严格, 见 rejectEscapedReservedNameIn。
func (p *Parser) rejectEscapedReservedName(name string) {
	p.rejectEscapedReservedNameIn(name, p.strict)
}

// rejectEscapedReservedNameIn 与 rejectEscapedReservedName 同义, strict 允许
// 调用方覆盖模式判定 —— 类名/类体恒严格 (规范 10.2.1: ClassDeclaration /
// ClassExpression 整体是严格模式代码), 而类名的解析发生在 setStrict(true) 之前
// (test262 class-name-ident-{let,static,yield}-escaped.js)。
func (p *Parser) rejectEscapedReservedNameIn(name string, strict bool) {
	if p.escapedNameForbiddenAsIdent(name, strict) {
		p.addError("SyntaxError: Keyword must not contain escaped characters")
	}
}

// escapedNameForbiddenAsIdent 报告「用 unicode 转义拼出的名字」能否作
// Identifier (BindingIdentifier / IdentifierReference / LabelIdentifier)。
// 关键字不得含转义 (规范 5.1.5: 终结符必须原样出现), 故转义拼出的保留字既不是
// 关键字也不是合法 Identifier。**属性名**位置 (IdentifierName: 成员访问 .name /
// 对象键 / 方法名 / get·set 的 name) 不受此限 —— 那些路径不经过本函数。
//
// 与 isAlwaysReservedWordName 的差别: 后者只覆盖「任何模式恒保留」的词, 这里还
// 按上下文接纳 yield / await 两个 [+Yield]·[+Await] 参数化的保留字, 以及 strict
// 专属的一档 (implements/interface/let/package/private/protected/public/static/yield)。
func (p *Parser) escapedNameForbiddenAsIdent(name string, strict bool) bool {
	if isAlwaysReservedWordName(name) {
		return true
	}
	if strict && isStrictReservedWordName(name) {
		return true
	}
	switch name {
	case "yield":
		// yield 是 [+Yield] 上下文保留字: 生成器/async-generator 体内、严格模式、
		// 模块里作 Identifier 是早错; sloppy 非生成器里是普通标识符
		// (test262 labeled/value-yield-non-strict-escaped.js 必须放行)。
		return !p.yieldIsIdentifier()
	case "await":
		// await 在模块 (顶层 +Await) 与 async 上下文 (函数体 / 形参窗口) 里是
		// 保留字; sloppy script / 普通函数里是普通标识符
		// (test262 labeled/value-await-non-module-escaped.js 必须放行)。
		return p.allowAwait || p.awaitReservedInParams || p.module || p.moduleEE
	}
	return false
}

// isAlwaysReservedWordName 报告名字是否为**任何模式**下都不可作 Identifier 的
// 保留字 (含 IdentifierName 层保留字 enum/extends/debugger 与字面量 null/true/false)。
func isAlwaysReservedWordName(name string) bool {
	switch name {
	case "break", "case", "catch", "class", "const", "continue", "debugger",
		"default", "delete", "do", "else", "enum", "export", "extends", "false",
		"finally", "for", "function", "if", "import", "in", "instanceof", "new",
		"null", "return", "super", "switch", "this", "throw", "true", "try",
		"typeof", "var", "void", "while", "with":
		return true
	}
	return false
}

// isStrictReservedWordName 报告名字是否为严格模式专属保留字
// (FutureReservedWord 中非 always 的一档)。
func isStrictReservedWordName(name string) bool {
	switch name {
	case "implements", "interface", "let", "package", "private", "protected",
		"public", "static", "yield":
		return true
	}
	return false
}

func (p *Parser) parseIntegerLiteral() ast.Expression {
	lit := &ast.IntegerLiteral{Token: p.curToken()}
	literal := p.curToken().Literal
	var value int64
	var err error

	if strings.HasPrefix(literal, "0x") || strings.HasPrefix(literal, "0X") {
		value, err = strconv.ParseInt(literal[2:], 16, 64)
	} else if strings.HasPrefix(literal, "0b") || strings.HasPrefix(literal, "0B") {
		value, err = strconv.ParseInt(literal[2:], 2, 64)
	} else if strings.HasPrefix(literal, "0o") || strings.HasPrefix(literal, "0O") {
		value, err = strconv.ParseInt(literal[2:], 8, 64)
	} else if legacyOctalValue(literal) != "" {
		// legacy 八进制字面量: `0777` / `08` / `09` (后者是 NonOctalDecimal)。
		// 严格模式下是 SyntaxError; 非严格下按规范求值 (0 开头 → 八进制,
		// 但含 8/9 时是十进制 —— 例如 08 → 8, 0777 → 511)。
		if p.strict {
			p.addError("SyntaxError: legacy octal literals are not allowed in strict mode")
			return nil
		}
		value, err = strconv.ParseInt(legacyOctalValue(literal), legacyOctalBase(literal), 64)
	} else {
		value, err = strconv.ParseInt(literal, 10, 64)
	}

	if err != nil {
		p.addError(fmt.Sprintf("could not parse %q as integer", literal))
		return nil
	}
	lit.Value = value
	return lit
}

// legacyOctalValue 报告 literal 是否为 legacy 八进制字面量 (以 0 开头且
// 长度 >1 的纯十进制数字串, 排除 0x/0o/0b 前缀与浮点)。是则返回去掉前导 0
// 的数字串 (供 strconv 解析), 否则返回 ""。
func legacyOctalValue(literal string) string {
	if len(literal) < 2 || literal[0] != '0' {
		return ""
	}
	for _, c := range literal {
		if c < '0' || c > '9' {
			return ""
		}
	}
	return literal[1:]
}

// legacyOctalBase 返回 legacy 八进制字面量的解析进制: 只含 0-7 时按八进制
// (0777 → 511), 含 8/9 时按十进制 (08 → 8)。
func legacyOctalBase(literal string) int {
	for _, c := range literal {
		if c == '8' || c == '9' {
			return 10
		}
	}
	return 8
}

func (p *Parser) parseFloatLiteral() ast.Expression {
	lit := &ast.FloatLiteral{Token: p.curToken()}
	value, err := strconv.ParseFloat(p.curToken().Literal, 64)
	if err != nil {
		p.addError(fmt.Sprintf("could not parse %q as float", p.curToken().Literal))
		return nil
	}
	lit.Value = value
	return lit
}

// parseBigIntLiteral 解析 BigInt 字面量。
//
// 词法阶段已保证不含小数点/指数，这里只做进制与分隔符的合法性校验；
// 真正的数值转换交给编译期，避免在 AST 层引入 math/big 依赖。
func (p *Parser) parseBigIntLiteral() ast.Expression {
	lit := &ast.BigIntLiteral{Token: p.curToken(), Raw: p.curToken().Literal}
	if _, ok := object.ParseBigIntLiteral(lit.Raw); !ok {
		p.addError(fmt.Sprintf("could not parse %q as BigInt", lit.Raw))
		return nil
	}
	return lit
}

func (p *Parser) parseStringOrTemplate() ast.Expression {
	// 模板开始 token (BACKTICK) 或字符串后跟 ${ 都是模板字面量
	if p.curTokenIs(lexer.BACKTICK) || p.peekTokenIs(lexer.DOLLAR_BRACE) {
		return p.parseTemplateLiteral()
	}
	return &ast.StringLiteral{Token: p.curToken(), Value: p.curToken().Literal}
}

// parseTaggedTemplate 处理 tagged template: tag`...`
// 作为中缀解析函数注册: 表达式后紧跟模板开始 token (BACKTICK)。
func (p *Parser) parseTaggedTemplate(tag ast.Expression) ast.Expression {
	tt := &ast.TaggedTemplateExpression{
		Token: p.curToken(),
		Tag:   tag,
	}
	if tl, ok := p.parseTemplateLiteral().(*ast.TemplateLiteral); ok {
		tt.Template = tl
	}
	return tt
}

func (p *Parser) parseTemplateLiteral() ast.Expression {
	tl := &ast.TemplateLiteral{Token: p.curToken()}
	tl.Quasis = []string{}
	tl.Expressions = []ast.Expression{}

	tl.Quasis = append(tl.Quasis, p.curToken().Literal)

	for {
		if !p.peekTokenIs(lexer.DOLLAR_BRACE) {
			break
		}
		p.nextToken() // consume DOLLAR_BRACE
		p.nextToken() // enter expression

		expr := p.parseExpression(LOWEST)
		if expr == nil {
			return nil
		}
		tl.Expressions = append(tl.Expressions, expr)

		// 词法分析器在处理模板字面量时，当 templateBraceDepth == 0 遇到 }，
		// 会消费 } 并直接调用 readTemplateString 读取下一段字符串。
		// 因此 } 不会作为独立 token 出现，表达式之后紧跟的是 STRING_LITERAL。
		if !p.peekTokenIs(lexer.STRING_LITERAL) {
			p.addError(fmt.Sprintf("expected string in template after expression, got %s", p.peekToken().Type))
			return nil
		}
		p.nextToken() // 消费表达式的最后一个 token，curToken 变为 STRING_LITERAL (下一段模板字符串)
		tl.Quasis = append(tl.Quasis, p.curToken().Literal)
	}
	return tl
}

func (p *Parser) parseBooleanLiteral() ast.Expression {
	return &ast.BooleanLiteral{Token: p.curToken(), Value: p.curTokenIs(lexer.TRUE)}
}

func (p *Parser) parseNullLiteral() ast.Expression {
	return &ast.NullLiteral{Token: p.curToken()}
}

func (p *Parser) parseUndefinedLiteral() ast.Expression {
	return &ast.UndefinedLiteral{Token: p.curToken()}
}

func (p *Parser) parseRegexLiteral() ast.Expression {
	// Literal 格式: "pattern\x00flags" (NUL 分隔，见 lexer.scanRegexLiteral：
	// `|` 是合法正则字符，不能用作分隔符)。
	literal := p.curToken().Literal
	pattern := literal
	flags := ""
	if i := strings.IndexByte(literal, 0); i >= 0 {
		pattern = literal[:i]
		flags = literal[i+1:]
	}
	return &ast.RegexLiteral{
		Token:   p.curToken(),
		Pattern: pattern,
		Flags:   flags,
	}
}

func (p *Parser) parseThisExpression() ast.Expression {
	return &ast.ThisExpression{Token: p.curToken()}
}

func (p *Parser) parseUnaryExpression() ast.Expression {
	expr := &ast.UnaryExpression{
		Token: p.curToken(), Operator: p.curToken().Literal, Prefix: true,
	}
	p.nextToken()
	expr.Right = p.parseExpression(UNARY)
	// delete obj.#x: 规范定义为**早错**（私有成员不可删除，ES2022 ClassFieldDefinitionEvaluation 起）。
	// 必须在解析期拦下 —— 编译器的 compileDelete 只认 Property 是 *ast.Identifier，
	// 而私有访问的名字在 MemberExpression.Private（Property 为 nil），漏到编译期会 panic。
	if expr.Operator == "delete" {
		if mem, ok := expr.Right.(*ast.MemberExpression); ok && mem.Private != "" {
			p.addError(fmt.Sprintf("SyntaxError: 'delete' of private member '#%s' is not allowed",
				strings.TrimPrefix(mem.Private, "#")))
		}
	}
	// ++/-- 的操作数必须是有效简单赋值目标 (sec-update-expressions-static-
	// semantics-early-errors)。new.target 的 AssignmentTargetType 为 invalid,
	// `++new.target` / `++(new.target)` 都是早错。括号形式 (new.target) 解析后
	// 直接是 MetaProperty 节点, 因此类型判定已覆盖 cover 形态。
	if expr.Operator == "++" || expr.Operator == "--" {
		if _, ok := expr.Right.(*ast.MetaProperty); ok {
			p.addError("SyntaxError: Invalid left-hand side expression in prefix operation")
		}
	}
	return expr
}

// parseGroupedOrArrow 解析括号表达式或箭头函数。
// 通过扫描到匹配的 ) 后检查是否跟 => 来区分。
func (p *Parser) parseGroupedOrArrow() ast.Expression {
	if p.isArrowFunction() {
		return p.parseArrowFunction(false)
	}
	p.nextToken() // consume (
	expr := p.parseCommaSequence()
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	// 检查 => 是否紧跟 (单个参数箭头函数的回退)
	if p.peekTokenIs(lexer.ARROW) {
		// (expr) => 是箭头函数，expr 作为唯一参数
		// 这种情况不太常见，但需要处理
		// 简化: 如果 (identifier) => ，转换为箭头函数
		if ident, ok := expr.(*ast.Identifier); ok {
			p.nextToken() // consume =>
			return p.parseArrowFunctionBody([]*ast.Parameter{{
				Token: ident.Token, Name: ident.Value,
			}}, false)
		}
	}
	return expr
}

// isArrowFunction 报告「cur 位置上的 ( 」是否为箭头函数的参数列表。
func (p *Parser) isArrowFunction() bool {
	return p.curTokenIs(lexer.LPAREN) && p.parenGroupFollowedByArrow(0)
}

// parenGroupFollowedByArrow 从偏移 start 上的 ( 开始扫描到配对的 ), 报告它后面
// 是否紧跟 =>。
//
// start 是 peekTokenAt 的口径 (相对 cur 的偏移): 0 = cur 自己, 1 = peek。
// 之所以要带偏移: `async (a) => …` 里 `(` 落在 peek 上 (cur 是 async), 判定逻辑
// 与普通箭头逐字相同, 只有起点不同 —— 与其抄一份, 不如把起点参数化。
//
// 扫描有距离上限: 括号不闭合时会一路扫到 EOF, 配合大量 `(` 构成平方级解析开销。
func (p *Parser) parenGroupFollowedByArrow(start int) bool {
	depth := 0
	for i := start; i <= start+maxArrowScanLimit; i++ {
		tok := p.peekTokenAt(i)
		if tok.Type == lexer.EOF {
			return false
		}
		if tok.Type == lexer.LPAREN {
			depth++
		} else if tok.Type == lexer.RPAREN {
			depth--
			if depth == 0 {
				// 检查 ) 后是否跟 =>
				return p.peekTokenAt(i+1).Type == lexer.ARROW
			}
		}
	}
	return false
}

// parseArrowFunction 解析括号参数列表形式的箭头函数 (cur = '(')。
// isAsync 供 async 箭头传入, 决定函数体内是否允许 await。
func (p *Parser) parseArrowFunction(isAsync bool) ast.Expression {
	// curToken = LPAREN, parseParameters will advance past it
	// 箭头函数没有自己的 [Yield] 参数: 形参区继承外层 yield 语境 (生成器体内的
	// 箭头形参仍 [+Yield], 形参名/默认值里 yield 是早错; sloppy 里 ~Yield)。
	params := p.parseParameters(lexer.RPAREN, isAsync, p.allowYield)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}

	if !p.peekTokenIs(lexer.ARROW) {
		p.addError(fmt.Sprintf("expected '=>', got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken() // consume =>
	return p.parseArrowFunctionBody(params, isAsync)
}

// parseArrowFunctionBody 解析箭头函数体 (cur 已越过 =>)。
// isAsync 决定体内的 async 上下文: async 箭头允许 await, 同步箭头体内
// 裸 await 是早错。
func (p *Parser) parseArrowFunctionBody(params []*ast.Parameter, isAsync bool) *ast.ArrowFunctionExpression {
	af := &ast.ArrowFunctionExpression{Token: p.curToken(), Parameters: params, IsAsync: isAsync}
	// 箭头函数的形参形状是 UniqueFormalParameters: 重复绑定名恒为 SyntaxError
	// (sloppy 简单列表也报), 与 checkStrictFunctionParams 的 strict 分支无关。
	p.checkUniqueParamNames(params)

	if p.peekTokenIs(lexer.LBRACE) {
		p.nextToken()
		// 箭头函数没有自己的 [Yield] 参数: 原样传当前值继承外层语境
		// (生成器体内的箭头仍是 [+Yield] —— yield 作标签名一样早错)。
		af.Body, af.Strict = p.parseFunctionBodyWithStrict(isAsync, p.allowYield)
		p.checkUseStrictWithNonSimpleParams(af.Parameters)
	} else {
		p.nextToken()
		af.Strict = p.strict // 表达式体无指令, 严格性继承自外层
		restore := p.setAllowAwait(isAsync)
		// 箭头表达式体是函数边界, 不再是形参窗口: 生成器形参默认值里的
		// `() => yield` 合法 (node 实测), 故此处清零该标记 (块体走
		// parseFunctionBodyWithStrict -> setAllowYield 一并清零)。
		prevYieldReserved := p.yieldReservedInParams
		p.yieldReservedInParams = false
		af.Body = p.parseExpression(LOWEST)
		p.yieldReservedInParams = prevYieldReserved
		restore()
	}
	if af.Strict {
		p.checkStrictFunctionParams(af.Parameters)
	}
	return af
}

func (p *Parser) parseFunctionExpression() ast.Expression {
	fn := &ast.FunctionExpression{Token: p.curToken()}
	// function*: generator 函数
	if p.peekTokenIs(lexer.ASTERISK) {
		fn.IsGenerator = true
		p.nextToken() // 移到 *
	}
	if p.peekTokenIs(lexer.IDENTIFIER) {
		p.nextToken()
		fn.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	fn.Parameters = p.parseParameters(lexer.RPAREN, false, fn.IsGenerator)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	// function 表达式永远是同步上下文 (async function 表达式走 parseAsyncExpression)
	fn.Body, fn.Strict = p.parseNonArrowFunctionBody(false, fn.IsGenerator)
	p.checkUseStrictWithNonSimpleParams(fn.Parameters)
	if fn.Strict {
		p.checkStrictFunctionParams(fn.Parameters)
	}
	return fn
}

// parseAsyncExpression 处理 async 前缀 (cur 落在 async 上):
//   - async function f() {}  → async 函数表达式/声明
//   - async (a, b) => …       → async 箭头函数
//   - async x => …            → 单参数不加括号的 async 箭头
//
// 前两类在 peeking 时的区别就是「`function` 还是 `(`」, 第三类是「标识符 + =>」。
// 三种都不匹配时按写法分别报错: 括号组后面缺 `=>` 与其它乱写分开说, 因为前者是
// 真的漏了 `=>`, 后者才是"根本没实现这种形状"。
func (p *Parser) parseAsyncExpression() ast.Expression {
	if p.peekTokenIs(lexer.FUNCTION) {
		p.nextToken() // 移到 function
		fn := &ast.FunctionExpression{Token: p.curToken()}
		fn.IsAsync = true
		if p.peekTokenIs(lexer.ASTERISK) {
			fn.IsGenerator = true
			p.nextToken()
		}
		if p.peekTokenIs(lexer.IDENTIFIER) {
			p.nextToken()
			fn.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		}
		if !p.expectPeek(lexer.LPAREN) {
			return nil
		}
		fn.Parameters = p.parseParameters(lexer.RPAREN, fn.IsAsync, fn.IsGenerator)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		fn.Body, fn.Strict = p.parseNonArrowFunctionBody(true, fn.IsGenerator)
			p.checkUseStrictWithNonSimpleParams(fn.Parameters)
		if fn.Strict {
			p.checkStrictFunctionParams(fn.Parameters)
		}
		return fn
	}

	// async 箭头函数: async () => … / async (a, b) => …
	// cur 是 async, 所以 `(` 落在 peek 上 —— 扫描起点用 1。
	if p.peekTokenIs(lexer.LPAREN) {
		if !p.parenGroupFollowedByArrow(1) {
			// ES 里 `async(x)` 是「调用一个名叫 async 的函数」, 但本运行时 async 是
			// 保留字, 那个函数不可能存在 —— 所以这里一定是漏写了 =>, 直接点名,
			// 别让人去猜 "unsupported async expression" 到底哪里不支持。
			p.addError("expected '=>' after async parameter list")
			return nil
		}
		p.nextToken() // cur = (
		return p.finishAsyncArrow(p.parseArrowFunction(true))
	}

	// async x => … (单参数不带括号)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peek2TokenIs(lexer.ARROW) {
		p.nextToken() // cur = 标识符
		ident := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		p.nextToken() // cur = =>
		return p.finishAsyncArrow(p.parseArrowFunctionBody([]*ast.Parameter{{
			Token: ident.Token, Name: ident.Value,
		}}, true))
	}

	// 其余形状: async 回退为普通标识符引用 (test262 head-lhs-async.js:
	// console.log("async=", async) —— async 不是保留字)。但后面紧跟「必为
	// 操作数」的 token (async 42 / async x) 不构成任何合法形状, 仍报错。
	if !p.peekStartsAwaitOperand() {
		return &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}

	p.addError("unsupported async expression after 'async' (only 'async function' " +
		"and async arrow functions are supported)")
	return nil
}

// finishAsyncArrow 给箭头函数打上 async 前缀。
// parseArrowFunction / parseArrowFunctionBody 返回 Expression, 失败时是 nil 且
// 内部已经报过具体错, 这里统一转换一次, 免得两个分支各写一遍类型断言。
func (p *Parser) finishAsyncArrow(expr ast.Expression) ast.Expression {
	af, ok := expr.(*ast.ArrowFunctionExpression)
	if !ok {
		return nil
	}
	af.IsAsync = true
	return af
}

// parseYieldExpression 解析 yield 表达式
// (YieldExpression : yield [no LineTerminator here] AssignmentExpression /
//  yield * AssignmentExpression)。
//
// 在 sloppy 非生成器代码里 yield **恒**是普通标识符 (IdentifierReference),
// 不开启 yield 表达式 —— node 22 实测: `var yield=4; yield+1 / yield-1 /
// yield*2 / yield/2 / yield(1) / yield[0] / yield.x / yield++` 全是标识符用法,
// 而 `yield 1` / `yield !x` 是 SyntaxError (标识符后直接跟操作数, 同行不能 ASI)。
// 故此处直接返回 Identifier{cur=YIELD}, 不消费任何 token, 由中缀循环继续处理
// (调用/索引/成员/二元/更新/箭头……)。`yield 1` 这种非法形态自会在后续解析中
// 因「表达式后跟多余 token」报错, 与 node 口径一致。
//
// 判据只在 yieldIsIdentifier() 为真时启用 (sloppy 非生成器非模块);
// 生成器/严格/模块里 yield 恒是关键字, 走下面的表达式路径 (含空 yield)。
//
// ⚠ 全局不变量: parseExpression 返回后 curToken 停在表达式的**末 token**上。
// 空 yield 的末 token 就是 YIELD, 故其提前 return 时 curToken 必须仍在 YIELD
// (判定只看 peekToken); 越位消费终结符会让调用方 (数组/对象字面量/实参表/
// 括号/条件表达式/解构模式) 的终结符判定错位。历史上 dc15392/aeaf943 撤回的
// 版本确实把 cur 停在 YIELD, 但当时**形参区早错**靠终结符越位侥幸触发, 于是
// 一撤就出 46 例回归 —— 现已把该早错显式改用 yieldReservedInParams 承接
// (见 parseYieldExpression 内注释), 两条约束不再互相牵制。
func (p *Parser) parseYieldExpression() ast.Expression {
	if p.yieldIsIdentifier() {
		// 单参数箭头: yield => body (node 实测合法, yield 是合法绑定名)
		if p.peekTokenIs(lexer.ARROW) {
			ident := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken() // consume =>
			return p.parseArrowFunctionBody([]*ast.Parameter{{
				Token: ident.Token, Name: ident.Value,
			}}, false)
		}
		return &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
	ye := &ast.YieldExpression{Token: p.curToken()}
	// 走到这里说明 yield 在**当前语境里是保留字** (yieldIsIdentifier() 为假):
	// 即 生成器体 / 严格模式 / 模块 / class 体。此时若不在生成器体 (allowYield
	// 为假) —— 严格模式函数体、class 体、模块体 —— yield 既不能作标识符也
	// 不能构成 YieldExpression, 裸 yield 一律 SyntaxError。
	// 规范: AssignmentExpression[~Yield] 不含 YieldExpression (test262
	// dstr/*-yield{,-ident}-invalid.js 一族, flags:[onlyStrict]:
	// `0, [ x = yield ] = [];` / class 体内裸 yield 皆早错)。
	// sloppy 非生成器里 yieldIsIdentifier() 为真, 已在上面的标识符分支返回,
	// 不会到达此处。
	if !p.allowYield {
		if p.moduleEE && p.fnDepth == 0 {
			// 模块顶层不是 generator 上下文 (spec:
			// ModuleItem : StatementListItem[~Yield, ~Return], parse-err-yield.js)。
			p.addError("SyntaxError: yield expression not allowed in module body")
		} else {
			p.addError("SyntaxError: yield is a reserved word in this context")
		}
	}
	// 形参窗口里不得出现 YieldExpression (见 yieldReservedInParams)。
	if p.yieldReservedInParams {
		p.addError("SyntaxError: yield expression is not allowed in formal parameters")
	}
	// ⚠ 判定一律基于 **peekToken**, 且空 yield 分支**不消费**下一个 token ——
	// curToken 必须停在 YIELD 上, 以维持「parseExpression 返回后 curToken 是
	// 表达式末 token」的全局约定。调用方 (数组/对象字面量元素、实参表、括号、
	// 条件表达式、解构模式终止符判定) 都据该约定用 peekToken 找终结符;
	// 一旦越位消费终结符, `[yield]` / `f(yield)` / `(yield)` / `a ? yield : b`
	// 就会在调用方报「expected ']' / ')' / ':' got ...」而解析失败。
	// (历史上 dc15392/aeaf943 撤回的正是把 cur 停在 YIELD 的版本; 那次回归的
	//  真正根因是**形参区早错**当时靠终结符越位侥幸触发 —— 现已由
	//  yieldReservedInParams 显式承接, 故此处可安全保持 cur=YIELD。)
	//
	// `yield` 与操作数之间禁止换行 ([no LineTerminator here]): peek 一旦换行
	// 即为空 yield (ASI), 后续 token 另起一条语句。必须在消费 `*` **之前**判定
	// —— `yield *` 与其右操作数之间**允许**换行 (规范 YieldExpression :
	// yield * AssignmentExpression 无该限制), 否则 `yield *\ng()` 会被误判成
	// 空 yield, 丢掉委托目标 (test262: async-generator/
	// expression-yield-star-before-newline.js)。
	if p.peekToken().Line > p.curToken().Line {
		return ye
	}
	// yield* iterable: 委托给另一个生成器/可迭代对象
	if p.peekTokenIs(lexer.ASTERISK) {
		ye.Delegate = true
		p.nextToken() // cur = *
		p.nextToken() // cur = 操作数首 token
		ye.Value = p.parseExpression(LOWEST)
		return ye
	}
	// 空 yield: peek 为终结符 (分号/闭合符/逗号/冒号/EOF)。
	// 规范: YieldExpression : yield [no LineTerminator here] AssignmentExpression。
	// 缺 RBRACKET/COMMA/COLON 会让 `[yield]` / `f(yield, 1)` / `a ? yield : b`
	// 误入操作数分支。
	if p.peekTokenIs(lexer.SEMICOLON) || p.peekTokenIs(lexer.RPAREN) ||
		p.peekTokenIs(lexer.RBRACKET) || p.peekTokenIs(lexer.RBRACE) ||
		p.peekTokenIs(lexer.COMMA) || p.peekTokenIs(lexer.COLON) ||
		p.peekTokenIs(lexer.EOF) {
		return ye
	}
	p.nextToken() // cur = 操作数首 token
	ye.Value = p.parseExpression(LOWEST)
	return ye
}

// parseAwaitExpression 解析 await 表达式。
//
// 裸 await 早错 (node 22 实测口径): 非 async 上下文里 await 是普通标识符
// (sloppy script 的 var await = 1 / await(1) / await - 1 都合法), 只有
// 「await 后直接跟操作数」才是 await 表达式, 此时按 node 报 SyntaxError
// ("await is only valid in async functions ...")。此前该形态会被解析成
// AwaitExpression, 编译成 OP_YIELD 后运行时才报 "yield outside generator"。
func (p *Parser) parseAwaitExpression() ast.Expression {
	if !p.allowAwait {
		// async 形参窗口 (~Await + await 保留字): 此处 await 一律 SyntaxError,
		// 覆盖 `x = await` / `x = await.foo` / 对象·类计算键 `[await]` 等形态
		// (test262 early-errors-*-await-in-formals-default.js 家族)。
		if p.awaitReservedInParams {
			p.addError("SyntaxError: await is a reserved word in async function parameters")
			return nil
		}
		if !p.peekStartsAwaitOperand() {
			// 标识符用法: 交给中缀循环继续 (调用/索引/二元/后缀……)
			return &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		}
		p.addError("SyntaxError: await is only valid in async functions and async generators")
		return nil
	}
	ae := &ast.AwaitExpression{Token: p.curToken()}
	p.nextToken()
	ae.Argument = p.parseExpression(LOWEST)
	return ae
}

func (p *Parser) parseNewExpression() ast.Expression {
	newTok := p.curToken()
	expr := &ast.NewExpression{Token: newTok}
	p.nextToken()
	// ImportCall 是 CallExpression, 不是 MemberExpression —— `new import(...)`
	// 在语法上就不成立 (spec: NewExpression), 必须 parse 期 SyntaxError, 不能
	// 编成 `new <promise>` 推到运行期抛 TypeError。见 test262
	// dynamic-import/syntax/invalid/*-no-new-call-expression{,-prop-access}.js。
	if p.curTokenIs(lexer.IMPORT) && p.peekTokenIs(lexer.LPAREN) {
		p.addError("SyntaxError: import call is not a constructor (cannot be preceded by 'new')")
	}
	// 元属性 new.target: `new` 后紧跟 `.target`。token 之间的空白/换行/注释
	// 都只是分隔 (规范里 NewTarget 无 [no LineTerminator here] 限制, 见 test262
	// new.target/asi.js), 词法层已把它们剥掉, 这里只需看 cur/peek 两个 token。
	// `target` 必须是**未转义**的标识符 (test262 new.target 早错: 关键字不得含
	// 转义)。词法层现在会解码 \u 转义, 故显式排除 IdentHasEscape。
	if p.curTokenIs(lexer.DOT) && p.peekTokenIs(lexer.IDENTIFIER) &&
		p.peekToken().Literal == "target" && !p.peekToken().IdentHasEscape {
		p.nextToken() // cur: DOT → target
		if p.newTargetForbidden || !p.newTargetAllowed {
			p.addError("SyntaxError: new.target expression is not allowed here")
		}
		return &ast.MetaProperty{Token: newTok}
	}
	expr.Callee = p.parseExpression(MEMBER)
	if expr.Callee == nil {
		return nil
	}
	if p.peekTokenIs(lexer.LPAREN) {
		p.nextToken()
		expr.Arguments = p.parseArguments()
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
	}
	return expr
}

func (p *Parser) parseArrayLiteral() ast.Expression {
	arr := &ast.ArrayLiteral{Token: p.curToken()}
	arr.Elements = []ast.Expression{}
	p.nextToken()

	if p.curTokenIs(lexer.RBRACKET) {
		return arr
	}
	for !p.curTokenIs(lexer.RBRACKET) && !p.curTokenIs(lexer.EOF) {
		// 空洞 (elision): `[, a]` / `[a, , b]` —— 占位但无元素。
		// 字面量位置直接跳过 (元素保留为 nil, 供解构覆盖语法转模式时识别)。
		if p.curTokenIs(lexer.COMMA) {
			arr.Elements = append(arr.Elements, nil)
			p.nextToken()
			continue
		}
		if p.curTokenIs(lexer.SPREAD_REST) {
			p.nextToken()
			arr.Elements = append(arr.Elements, &ast.SpreadElement{
				Token: p.curToken(), Argument: p.parseExpression(LOWEST),
			})
		} else {
			elem := p.parseExpression(LOWEST)
			if elem != nil {
				arr.Elements = append(arr.Elements, elem)
			}
		}
		if p.peekTokenIs(lexer.COMMA) {
			p.nextToken() // 指向逗号
			p.nextToken() // 指向下一个元素 (或 ']' ⇒ 这是尾逗号)
			if p.curTokenIs(lexer.RBRACKET) {
				arr.TrailingComma = true
			}
		} else if p.peekTokenIs(lexer.RBRACKET) {
			p.nextToken()
			break
		} else {
			p.addError(fmt.Sprintf("expected ',' or ']', got %s", p.peekToken().Type))
			return nil
		}
	}
	if !p.curTokenIs(lexer.RBRACKET) {
		return nil
	}
	return arr
}

func (p *Parser) parseObjectLiteral() ast.Expression {
	obj := &ast.ObjectLiteral{Token: p.curToken()}
	obj.Properties = []*ast.Property{}
	p.nextToken()

	if p.curTokenIs(lexer.RBRACE) {
		return obj
	}
	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		lastWasSpread := false
		if p.curTokenIs(lexer.SPREAD_REST) {
			// 对象展开 {...a}
			p.nextToken()
			obj.Spread = append(obj.Spread, p.parseExpression(LOWEST))
			lastWasSpread = true
		} else {
			prop := p.parseProperty()
			if prop != nil {
				obj.Properties = append(obj.Properties, prop)
			}
		}
		if p.peekTokenIs(lexer.COMMA) {
			p.nextToken()
			p.nextToken()
		} else if p.peekTokenIs(lexer.RBRACE) {
			p.nextToken()
			if lastWasSpread {
				obj.SpreadIsLast = true
			}
			break
		} else {
			p.addError(fmt.Sprintf("expected ',' or '}', got %s", p.peekToken().Type))
			return nil
		}
	}
	if !p.curTokenIs(lexer.RBRACE) {
		return nil
	}
	return obj
}

func (p *Parser) parseProperty() *ast.Property {
	prop := &ast.Property{Token: p.curToken(), Kind: ast.PROP_INIT}
	isGenerator := false
	isAsync := false

	// 非法 token 不能作属性键 (如裸 `#`/`#!`: 词法判 ILLEGAL)。下方通用分支
	// 会把任意 token 的 Literal 直接当标识符键, 不拦会让 `{ #! }` 蒙混成
	// 对象字面量并落到运行期 (test262 hashbang/statement-block 期望 parse 早错)。
	if p.curTokenIs(lexer.ILLEGAL) {
		p.addError(fmt.Sprintf("unexpected token %s in object literal", p.curToken().Literal))
		return nil
	}

	// 生成器方法简写: *m() {} / *[expr]() {}
	if p.curTokenIs(lexer.ASTERISK) {
		isGenerator = true
		p.nextToken()
	}

	// async 方法简写: async m() {} / async [expr]() {} / async async() {}
	// async 只有构成修饰符前缀时才是关键字; 否则它是 PropertyName 位置上的
	// **名字** (`{ async(){} }` 是名为 async 的方法, `{ async: 1 }` 是键)。
	// 判据见 asyncModifierAhead (r81aQt: 名字可以是任意 PropertyName, 含 async)。
	if (p.curTokenIs(lexer.ASYNC) ||
		(p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "async" &&
			!p.curToken().IdentHasEscape)) &&
		p.asyncModifierAhead() {
		isAsync = true
		p.nextToken()
		if p.curTokenIs(lexer.ASTERISK) {
			isGenerator = true
			p.nextToken()
		}
	}

	// getter/setter: get name() {} / set name(v) {} / get [expr]() / set [expr](v)
	// 名字可以是关键字 (get return() {} —— test262 for-await-of 的 close 用例)。
	// 仅在 "get"/"set" 后紧跟 名字+( 或 [ 时识别为访问器,
	// 避免与 { get: 1 }, { get() {} }, { get } 混淆。
	if !isGenerator && !isAsync &&
		(p.curTokenIs(lexer.IDENTIFIER) && !p.curToken().IdentHasEscape &&
			(p.curToken().Literal == "get" || p.curToken().Literal == "set")) &&
		((p.peekTokenIs(lexer.IDENTIFIER) || isKeywordProperty(p.peekToken().Type)) &&
			p.peek2TokenIs(lexer.LPAREN) ||
			p.peekTokenIs(lexer.LBRACKET)) {
		if p.curToken().Literal == "get" {
			prop.Kind = ast.PROP_GETTER
		} else {
			prop.Kind = ast.PROP_SETTER
		}
		p.nextToken() // 到属性名 / [
		if p.curTokenIs(lexer.LBRACKET) {
			// 计算属性访问器: get [expr]() {}
			prop.Computed = true
			p.nextToken()
			prop.Key = p.parseExpression(LOWEST)
			if prop.Key == nil {
				return nil
			}
			// parseExpression 返回后表达式末 token 在 cur, ] 在 peek
			if !p.peekTokenIs(lexer.RBRACKET) {
				p.addError(fmt.Sprintf("expected ']' in computed property, got %s", p.peekToken().Type))
				return nil
			}
			p.nextToken() // consume ] -> cur=]
			p.nextToken() // cur=(
		} else {
			prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken() // 到 (
		}
		fn := &ast.FunctionExpression{Token: prop.Token}
		// 访问器不能是 async —— 同步上下文; 也不能是 generator
		restore := p.setAllowAwait(false)
		fn.Parameters = p.parseParameters(lexer.RPAREN, false, false)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken() // 到 {
		fn.Body, fn.Strict = p.parseNonArrowFunctionBody(false, false)
			p.checkUseStrictWithNonSimpleParams(fn.Parameters)
		restore()
		if fn.Strict {
			p.checkStrictFunctionParams(fn.Parameters)
		}
		prop.Value = fn
		return prop
	}

	if p.curTokenIs(lexer.LBRACKET) {
		// 计算属性: [expr]: value / [expr]() {}
		prop.Computed = true
		p.nextToken()
		prop.Key = p.parseExpression(LOWEST)
		if prop.Key == nil {
			return nil
		}
		if !p.peekTokenIs(lexer.RBRACKET) {
			p.addError(fmt.Sprintf("expected ']' in computed property, got %s", p.peekToken().Type))
			return nil
		}
		p.nextToken() // consume ]
	} else {
		prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}

	// 简写: { name } 或 CoverInitializedName: { name = 默认值 }
	if p.peekTokenIs(lexer.COMMA) || p.peekTokenIs(lexer.RBRACE) ||
		p.peekTokenIs(lexer.ASSIGN) {
		prop.Shorthand = true
		id, ok := prop.Key.(*ast.Identifier)
		if !ok {
			p.addError("shorthand property must be identifier")
			return nil
		}
		// shorthand 的键同时是 IdentifierReference: 严格模式/生成器/模块里
		// yield 是保留字, `({ yield })` 在此为 SyntaxError
		// (test262 object/identifier-shorthand-yield-invalid-strict-mode.js,
		// flags: noStrict 但体内 "use strict" —— p.strict 已随指令置位)。
		if p.curTokenIs(lexer.YIELD) && !p.yieldIsIdentifier() {
			p.addError("SyntaxError: 'yield' cannot be used as a shorthand property in strict mode code")
			return nil
		}
		// 转义拼出的保留字作 shorthand (`{ bre\u0061k }`): shorthand 键同时是
		// IdentifierReference, 转义标识符 token 类型是 IDENTIFIER 但仍非法。
		if id.Token.IdentHasEscape {
			before := len(p.errors.Errors)
			p.rejectEscapedReservedName(id.Value)
			if len(p.errors.Errors) > before {
				return nil
			}
		}
		prop.Value = id
		// CoverInitializedName `{ x = 默认值 }`: 仅当整个对象字面量被用作解构
		// 赋值目标时才合法 (ObjectAssignmentPattern 的 cover grammar)。这里先
		// 按 cover 形态生成 AssignmentExpression{x = 默认值}, 并登记到
		// coverInitPending; 若最终没被 literalToPattern 消费 (即它是真正的
		// 对象字面量), 由 parseExpressionStatement 收尾时报
		// "CoverInitializedName in object literal" 早错
		// (test262 object/cover-initialized-name.js)。
		if p.peekTokenIs(lexer.ASSIGN) {
			p.nextToken() // cur = '='
			p.nextToken() // cur = 默认值首 token
			def := p.parseExpression(LOWEST)
			assign := &ast.AssignmentExpression{
				Token:    prop.Token,
				Left:     id,
				Operator: "=",
				Right:    def,
			}
			prop.Value = assign
			prop.CoverInitialized = true
			p.coverInitPending = append(p.coverInitPending, prop)
		}
		return prop
	}

	// 不是简写，推进到 key 之后的 token
	p.nextToken()

	// 方法定义: method() {} / [expr]() {} / *gen() {} / async m() {}
	if p.curTokenIs(lexer.LPAREN) {
		prop.Kind = ast.PROP_METHOD
		fn := &ast.FunctionExpression{Token: prop.Token}
		fn.IsGenerator = isGenerator
		fn.IsAsync = isAsync
		restore := p.setAllowAwait(isAsync)
		fn.Parameters = p.parseParameters(lexer.RPAREN, isAsync, isGenerator)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken()
		fn.Body, fn.Strict = p.parseNonArrowFunctionBody(isAsync, isGenerator)
			p.checkUseStrictWithNonSimpleParams(fn.Parameters)
		restore()
		if fn.Strict {
			p.checkStrictFunctionParams(fn.Parameters)
		}
		prop.Value = fn
		return prop
	}
	if isGenerator || isAsync {
		// *m / async m 后面不是 ( —— 语法非法 (生成器/async 只能是方法)
		p.addError(fmt.Sprintf("expected '(' after %s method name, got %s",
			map[bool]string{true: "generator", false: "async"}[isGenerator], p.curToken().Type))
		return nil
	}

	// 普通: key: value
	if !p.curTokenIs(lexer.COLON) {
		p.addError(fmt.Sprintf("expected ':' after property, got %s", p.curToken().Type))
		return nil
	}
	p.nextToken()
	prop.Value = p.parseExpression(LOWEST)
	return prop
}

// --- 中缀解析函数 ---

func (p *Parser) parseBinaryExpression(left ast.Expression) ast.Expression {
	expr := &ast.BinaryExpression{
		Token: p.curToken(), Left: left, Operator: p.curToken().Literal,
	}
	precedence := getPrecedence(p.curToken().Literal)
	if p.curTokenIs(lexer.EXPONENT) {
		precedence-- // 右结合
	}
	p.nextToken()
	expr.Right = p.parseExpression(precedence)
	return expr
}

func (p *Parser) parseLogicalExpression(left ast.Expression) ast.Expression {
	expr := &ast.LogicalExpression{
		Token: p.curToken(), Left: left, Operator: p.curToken().Literal,
	}
	precedence := getPrecedence(p.curToken().Literal)
	p.nextToken()
	expr.Right = p.parseExpression(precedence)
	return expr
}

// parseSequenceExpression 解析逗号运算符 (a, b, c)。
// left 是逗号前已解析的表达式; 返回依次求值所有表达式、值为最后一项的 SequenceExpression。
func (p *Parser) parseSequenceExpression(left ast.Expression) ast.Expression {
	return p.collectSequence(left)
}

// isValidAssignmentTarget 判断表达式能否作为赋值目标。
// 规范 Static Semantics AssignmentTargetType: Identifier / MemberExpression
// 为 simple, 其余 (数字字面量、二元表达式、函数调用等) 为 invalid ——
// 必须在解析期报 SyntaxError (early error), 否则会一路漏到运行时。
func isValidAssignmentTarget(e ast.Expression) bool {
	switch e.(type) {
	case *ast.Identifier, *ast.MemberExpression, *ast.SuperExpression:
		return true
	}
	return false
}

func (p *Parser) parseAssignmentExpression(left ast.Expression) ast.Expression {
	// 解构赋值目标: [a, b] = v / ({ x } = v)。
	// 目标先按数组/对象字面量解析 (cover grammar)，确认是简单赋值后
	// 转换为解构模式；此前编译器不认识字面量目标，静默不发射指令导致栈失衡。
	if p.curTokenIs(lexer.ASSIGN) {
		switch left.(type) {
		case *ast.ArrayLiteral, *ast.ObjectLiteral:
			left = p.literalToPattern(left)
			// 严格模式下解构赋值目标的绑定名不得是 eval/arguments
			// (sec-assignment-operators-static-semantics-early-errors:
			//  AssignmentPattern 的 It is a Syntax Error if ... 含 eval/arguments)。
			// test262 dstr/{array-elem-target-simple-strict,obj-id-simple-strict,
			// obj-id-init-simple-strict}.js (flags: onlyStrict, negative/parse)。
			p.checkStrictPatternTargets(left)
		}
	}
	// 赋值左值校验 (AssignmentTargetType): 解构模式与简单/成员目标合法,
	// 其余 (如 `x - y = 1`、`1 = 2`) 解析期直接报错。
	if !isValidAssignmentTarget(left) {
		isDestructuring := false
		switch left.(type) {
		case *ast.ArrayPattern, *ast.ObjectPattern:
			isDestructuring = true
		}
		if !isDestructuring {
			p.addError(fmt.Sprintf("SyntaxError: Invalid left-hand side in assignment"))
			return left
		}
	}
	expr := &ast.AssignmentExpression{
		Token: p.curToken(), Left: left, Operator: p.curToken().Literal,
	}
	p.nextToken()
	expr.Right = p.parseExpression(ASSIGN - 1)
	return expr
}

// literalToPattern 把赋值目标位置的数组/对象字面量转换为解构模式。
// 只接受合法的绑定目标 (标识符、嵌套模式、默认值)；遇到不支持的
// 目标 (成员表达式、对象 rest 等) 记录解析错误并原样返回。
func (p *Parser) literalToPattern(expr ast.Expression) ast.Expression {
	switch lit := expr.(type) {
	case *ast.ArrayLiteral:
		pattern := &ast.ArrayPattern{Token: lit.Token, Elements: []*ast.PatternElement{}}
		for i, el := range lit.Elements {
			if el == nil {
				// 空洞 (elision): [, a] / [a, , b]
				pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: lit.Token})
				continue
			}
			switch e := el.(type) {
			case *ast.Identifier:
				pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: e})
			case *ast.MemberExpression: // [x.y] / [x[k]] 赋值目标
				pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: e})
			case *ast.SpreadElement: // [a, ...rest] / [...[a,b]] / [...obj.k]
				// Early error: 赋值模式的 rest 同样必须是最后一项
				// ([a, ...b, c] = x / [...b,] = x 都是 SyntaxError)。
				// 数组**字面量**里的 spread 位置随意, 这里只在转模式时拦。
				if i != len(lit.Elements)-1 || lit.TrailingComma {
					p.addError("SyntaxError: rest element must be the last element in array pattern")
					return expr
				}
				switch r := e.Argument.(type) {
				case *ast.Identifier:
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: r, Rest: true})
				case *ast.MemberExpression:
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: r, Rest: true})
				case *ast.ArrayLiteral, *ast.ObjectLiteral:
					nested := p.literalToPattern(r)
					switch nested.(type) {
					case *ast.ArrayPattern, *ast.ObjectPattern:
						pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: nested, Rest: true})
					default:
						return expr // 错误已由递归记录
					}
				default:
					p.addError("invalid rest target in destructuring assignment")
					return expr
				}
			case *ast.AssignmentExpression: // [a = 默认值] / [x.y = 默认值] / [{a} = 默认值] / [[a] = 默认值]
				if id, ok := e.Left.(*ast.Identifier); ok {
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: id, Default: e.Right})
					continue
				}
				if m, ok := e.Left.(*ast.MemberExpression); ok {
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: m, Default: e.Right})
					continue
				}
				// 嵌套模式作赋值元素左值并带默认值: [ {a} = 默认值 ] / [ [a] = 默认值 ]
				// 内层 `{}`/`[]` 已在 parseAssignmentExpression 里被转成
				// ObjectPattern/ArrayPattern (cover 转模式), 这里直接复用。
				switch e.Left.(type) {
				case *ast.ArrayPattern, *ast.ObjectPattern:
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: e.Left, Default: e.Right})
					continue
				}
				p.addError("invalid destructuring assignment target")
				return expr
			default: // 嵌套模式 [[a], {b}]
				switch nested := p.literalToPattern(el).(type) {
				case *ast.ArrayPattern:
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: nested.Token, Target: nested})
				case *ast.ObjectPattern:
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: nested.Token, Target: nested})
				default:
					return expr // 错误已由递归记录
				}
			}
		}
		return pattern
	case *ast.ObjectLiteral:
		pattern := &ast.ObjectPattern{Token: lit.Token, Properties: []*ast.PatternProperty{}}
		// 对象 rest (赋值模式): {...target} —— 必须唯一且最后; 赋值语境的
		// rest 目标还允许成员表达式 ({...src.y} = x)。
		if len(lit.Spread) > 0 {
			if len(lit.Spread) > 1 || !lit.SpreadIsLast {
				p.addError("SyntaxError: object rest must be the last property")
				return expr
			}
			switch r := lit.Spread[0].(type) {
			case *ast.Identifier:
				pattern.RestTarget = r
			case *ast.MemberExpression:
				pattern.RestTarget = r
			default:
				p.addError("invalid rest target in destructuring assignment")
				return expr
			}
		}
		for _, prop := range lit.Properties {
			pp := &ast.PatternProperty{Token: prop.Token, Key: prop.Key, Shorthand: prop.Shorthand, Computed: prop.Computed}
			// 赋值模式的 shorthand 键同样是绑定/引用名, 不得是保留字
			// ({ default } = x / ({ extends } = x) 都是 SyntaxError)。
			if pp.Shorthand && !p.checkShorthandKey(pp) {
				return expr
			}
			// CoverInitializedName 被解构目标消费掉了 ⇒ 从待报错清单移除。
			if prop.CoverInitialized {
				pp.CoverInitialized = true
				p.clearCoverInit(prop)
			}
			switch v := prop.Value.(type) {
			case *ast.Identifier:
				pp.Value = v
			case *ast.MemberExpression: // { a: obj.k }
				pp.Value = v
			case *ast.AssignmentExpression: // { x = 默认 } / { a: obj.k = 默认 } / { a: [b] = [] }
				switch lhs := v.Left.(type) {
				case *ast.Identifier:
					pp.Value, pp.Default = lhs, v.Right
				case *ast.MemberExpression:
					pp.Value, pp.Default = lhs, v.Right
				case *ast.ArrayLiteral, *ast.ObjectLiteral:
					nested := p.literalToPattern(lhs)
					switch nested.(type) {
					case *ast.ArrayPattern, *ast.ObjectPattern:
						pp.Value, pp.Default = nested, v.Right
					default:
						p.addError("invalid destructuring assignment target")
						return expr
					}
				default:
					p.addError("invalid destructuring assignment target")
					return expr
				}
			default: // { x: 嵌套模式 }
				switch nested := p.literalToPattern(prop.Value).(type) {
				case *ast.ArrayPattern:
					pp.Value = nested
				case *ast.ObjectPattern:
					pp.Value = nested
				default:
					continue // 错误已由递归记录
				}
			}
			pattern.Properties = append(pattern.Properties, pp)
		}
		return pattern
	}
	p.addError("invalid destructuring assignment target")
	return expr
}

func (p *Parser) parsePostfixExpression(left ast.Expression) ast.Expression {
	// new.target 的 AssignmentTargetType 为 invalid, `new.target++` / `(new.target)++`
	// 是早错 (sec-update-expressions-static-semantics-early-errors)。
	if _, ok := left.(*ast.MetaProperty); ok {
		p.addError("SyntaxError: Invalid left-hand side expression in postfix operation")
	}
	return &ast.UnaryExpression{
		Token: p.curToken(), Operator: p.curToken().Literal, Right: left, Prefix: false,
	}
}

func (p *Parser) parseCallExpression(function ast.Expression) ast.Expression {
	exp := &ast.CallExpression{Token: p.curToken(), Function: function}
	exp.Arguments = p.parseArguments()
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	return exp
}

func (p *Parser) parseArguments() []ast.Expression {
	args := []ast.Expression{}
	if p.peekTokenIs(lexer.RPAREN) {
		p.nextToken()
		return args
	}
	p.nextToken()
	for {
		if p.curTokenIs(lexer.SPREAD_REST) {
			p.nextToken()
			args = append(args, &ast.SpreadElement{Token: p.curToken(), Argument: p.parseExpression(LOWEST)})
		} else {
			arg := p.parseExpression(LOWEST)
			if arg != nil {
				args = append(args, arg)
			}
		}
		if p.peekTokenIs(lexer.COMMA) {
			p.nextToken() // 移到 ,
			// 尾逗号 f(a, b,)：ES 允许，语义与 f(a, b) 逐字节相同，没有
			// 任何"支持了就要解释差异"的负担。而**不收**它是有代价的：
			// 数组 / 对象字面量与解构本来就收尾逗号（那几个循环用
			// `for !curTokenIs(闭括号)` 收尾），只有参数/实参列表例外 ——
			// 同一个文件里两种口径，用户会以为是别的地方写错了。
			if p.peekTokenIs(lexer.RPAREN) {
				break // 交给下面统一的 expectPeek(RPAREN) 收尾
			}
			p.nextToken()
		} else {
			break
		}
	}
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	return args
}

func (p *Parser) parseIndexExpression(left ast.Expression) ast.Expression {
	mexp := &ast.MemberExpression{Token: p.curToken(), Object: left, Computed: true}
	p.nextToken()
	mexp.Property = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RBRACKET) {
		return nil
	}
	return mexp
}

func (p *Parser) parseMemberExpression(left ast.Expression) ast.Expression {
	mexp := &ast.MemberExpression{Token: p.curToken(), Object: left}
	p.nextToken()
	// obj.#x: 属性名位置是私有名 (Lexer 切出 PRIVATE_NAME, Literal 含 #)
	if p.curTokenIs(lexer.PRIVATE_NAME) {
		return p.parsePrivateMemberExpression(left)
	}
	// 属性名可以是标识符或关键字 (如 obj.default, obj.class)
	if !p.curTokenIs(lexer.IDENTIFIER) && !isKeywordProperty(p.curToken().Type) {
		p.addError(fmt.Sprintf("expected property name, got %s", p.curToken().Type))
		return nil
	}
	mexp.Property = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	mexp.Computed = false
	return mexp
}

// parsePrivateIdentifier 解析裸私有名 (仅 `#x in obj` 左侧合法)。
// 其余场景的 #x 由成员访问/类体分支处理, 走不到这;
// 万一走到 (如 + #x), 产生的节点在编译期由 emitPrivateKey 校验类上下文。
func (p *Parser) parsePrivateIdentifier() ast.Expression {
	lit := p.curToken().Literal
	name := lit
	if len(name) > 0 && name[0] == '#' {
		name = name[1:]
	}
	return &ast.PrivateIdentifier{Token: p.curToken(), Name: name}
}

// parsePrivateAccessor 解析 get #name() / set #name(v) 私有访问器。
// curToken 在 PRIVATE_NAME 上; IsGetter/IsSetter 已由调用方设置。
func (p *Parser) parsePrivateAccessor(member *ast.ClassMethod) *ast.ClassMethod {
	member.IsPrivate = true
	member.Name = p.curToken().Literal
	if p.peekTokenIs(lexer.LPAREN) {
		p.nextToken() // cur = (
		// 私有访问器不能是 async —— 同步上下文
	// 私有访问器 / 类访问器 / constructor: 同步上下文
	restore := p.setAllowAwait(false)
	member.Parameters = p.parseParameters(lexer.RPAREN, false, false)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken() // cur = {
		member.Body = p.parseFunctionBody(false)
		restore()
		p.nextToken() // 前进到下一成员/分隔符
		return member
	}
	p.addError("private accessor must be followed by '('")
	return nil
}

// parsePrivateMember 解析类体中的 #name 成员 (字段 / 方法 / static 字段)。
// curToken 在 PRIVATE_NAME 上 (Literal 含前导 #), static 前缀已由 parseClassMember 识别。
// 返回约定与 parseClassMember 一致: 方法 (Body != nil)、字段 (FieldValue != nil 或裸字段)。
// 私有名无计算属性形式 (#[expr] 非法), 也不能叫 #constructor。
func (p *Parser) parsePrivateMember(member *ast.ClassMethod) *ast.ClassMethod {
	member.IsPrivate = true
	member.Name = p.curToken().Literal // "#x" 含 # 完整形式

	if member.Name == "#constructor" {
		p.addError("private name '#constructor' is not allowed")
		return nil
	}

	// #name(...) {} 私有方法 (可为 async / 生成器 —— IsAsync/IsGenerator 已
	// 由 parseClassMember 解析 * / async 前缀时置好)。
	if p.peekTokenIs(lexer.LPAREN) {
		p.nextToken() // cur = (
	restore := p.setAllowAwait(member.IsAsync)
	member.Parameters = p.parseParameters(lexer.RPAREN, member.IsAsync, member.IsGenerator)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken() // cur = {
		member.Body = p.parseFunctionBody(member.IsGenerator)
		restore()
		// parseBlockStatement 返回时 cur 停在 } 上 (与 ctor/getter 路径一致),
		// 再前进一格到下一成员/分隔符。
		p.nextToken()
		return member
	}

	// #name = expr 私有字段
	if p.peekTokenIs(lexer.ASSIGN) {
		p.nextToken() // cur = =
		p.nextToken() // cur = 表达式首
		member.FieldValue = p.parseExpression(LOWEST)
		p.nextToken() // 前进到分隔符/下一个成员
		p.checkClassFieldTermination()
		return member
	}

	// 裸私有字段 #name;
	p.nextToken() // 前进到分隔符/下一个成员
	p.checkClassFieldTermination()
	return member
}

// parsePrivateMemberExpression 解析点访问后的私有名: obj.#x。
// curToken 已在 PRIVATE_NAME 上 (Literal 含前导 #)。
// 私有访问编译为运行时动态键 (前缀由外围类决定, 编译期不可知),
// 这里只记录裸名; 是否处于合法类体上下文由编译器校验。
func (p *Parser) parsePrivateMemberExpression(left ast.Expression) ast.Expression {
	mexp := &ast.MemberExpression{Token: p.curToken(), Object: left}
	mexp.Private = p.curToken().Literal // "#x" 形式, 编译器去 #
	// 私有名引用记录到当前最内层 class 环境（栈式早错校验, 见
	// class_early_errors.go）。顶层引用立即报早错。
	p.notePrivateRef(mexp.Private)
	// super.#x: 规范早错 —— super 的属性访问只接受公有名。
	// 同 delete：不拦会在 super 方法调用分支触发 Property 断言 panic。
	if _, isSuper := left.(*ast.SuperExpression); isSuper {
		p.addError(fmt.Sprintf("SyntaxError: private member '#%s' is not allowed on 'super'",
			strings.TrimPrefix(mexp.Private, "#")))
	}
	return mexp
}

// parseOptionalChainExpression 处理可选链 a?.b, a?.[b], a?.()。
func (p *Parser) parseOptionalChainExpression(left ast.Expression) ast.Expression {
	// curToken 是 ?.
	switch {
	case p.peekTokenIs(lexer.IDENTIFIER) || isKeywordProperty(p.peekToken().Type):
		// a?.b 或 a?.b()
		mexp := &ast.OptionalMemberExpression{Token: p.curToken(), Object: left, Computed: false}
		p.nextToken() // 移到属性名
		mexp.Property = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		// 检查是否后跟调用 a?.b(...)
		if p.peekTokenIs(lexer.LPAREN) {
			p.nextToken() // 移到 (
			return p.parseOptionalCall(mexp)
		}
		return mexp
	case p.peekTokenIs(lexer.LBRACKET):
		// a?.[expr]
		mexp := &ast.OptionalMemberExpression{Token: p.curToken(), Object: left, Computed: true}
		p.nextToken() // 移到 [
		p.nextToken() // 移到表达式
		mexp.Property = p.parseExpression(LOWEST)
		if !p.expectPeek(lexer.RBRACKET) {
			return nil
		}
		return mexp
	case p.peekTokenIs(lexer.LPAREN):
		// a?.(args)
		return p.parseOptionalCall(left)
	default:
		p.addError(fmt.Sprintf("invalid optional chain after '?.', got %s", p.peekToken().Type))
		return nil
	}
}

// parseOptionalCall 解析可选链调用 a?.b(args) 或 a?.(args)。
func (p *Parser) parseOptionalCall(fn ast.Expression) ast.Expression {
	call := &ast.OptionalCallExpression{Token: p.curToken(), Function: fn}
	p.nextToken() // 移到 (
	call.Arguments = p.parseArguments()
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	return call
}

// isKeywordProperty 判断 token 类型是否为可用作属性名的关键字。
// 含 ASYNC: `async` 在 IdentifierName 位置恒为名字 (`obj.async` /
// `{ async: 1 }` / `get async(){}` 均合法 —— r81aQt)。
func isKeywordProperty(t lexer.TokenType) bool {
	switch t {
	case lexer.LET, lexer.CONST, lexer.VAR, lexer.IF, lexer.ELSE, lexer.FOR, lexer.OF, lexer.WHILE,
		lexer.BREAK, lexer.CONTINUE, lexer.FUNCTION, lexer.RETURN,
		lexer.UNDEFINED, lexer.TYPEOF, lexer.INSTANCEOF, lexer.NEW, lexer.THIS,
		lexer.DELETE, lexer.IN, lexer.TRY, lexer.CATCH, lexer.FINALLY, lexer.THROW,
		lexer.SWITCH, lexer.CASE, lexer.DEFAULT, lexer.CLASS, lexer.SUPER,
		lexer.IMPORT, lexer.EXPORT, lexer.YIELD, lexer.AWAIT, lexer.ASYNC, lexer.WITH,
		lexer.TRUE, lexer.FALSE, lexer.NULL:
		return true
	}
	return false
}

// isPropertyNameToken 报告 token 类型能否作为 PropertyName 的首 token
// (IdentifierName / StringLiteral / NumericLiteral)。用于 async 修饰符前瞻:
// `async async(){}` / `async 'x'(){}` 里第二个位置上的名字可以是任意
// PropertyName, 不限于 IDENTIFIER (r81aQt)。
func isPropertyNameToken(t lexer.TokenType) bool {
	switch t {
	case lexer.IDENTIFIER, lexer.STRING_LITERAL, lexer.INT_LITERAL,
		lexer.FLOAT_LITERAL, lexer.BIGINT_LITERAL, lexer.ASYNC, lexer.AWAIT:
		return true
	}
	return isKeywordProperty(t)
}

// asyncModifierAhead 报告 curToken 处的 `async` 是否构成 async 方法/生成器的
// **修饰符前缀**, 而不是 PropertyName 位置上名叫 `async` 的名字 (r81aQt)。
//
// 判据 (node 22 实测):
//   - `async *`        → async 生成器方法;
//   - `async [`        → async 计算属性名方法;
//   - `async <PropertyName> (` → async 方法;
//   - 其余 (直接跟 `(` / 跟 `name:` / 换行后跟名字) → `async` 是名字, 不是修饰符。
//
// [no LineTerminator here]: `async` 与 `*` / `[` / 名字之间不得换行
// (`({async\nfoo(){}})` 非法, 但 `class C { async\nfoo(){} }` 里 async 退化为字段名)。
//
// 调用前提: curToken 为 ASYNC token 或文本 "async" 的 IDENTIFIER。
func (p *Parser) asyncModifierAhead() bool {
	asyncTok := p.curToken()
	peek := p.peekToken()
	if peek.Line != asyncTok.Line {
		return false
	}
	switch peek.Type {
	case lexer.ASTERISK, lexer.LBRACKET:
		return true
	}
	return isPropertyNameToken(peek.Type) && p.peekTokenAt(2).Type == lexer.LPAREN
}


func (p *Parser) parseConditionalExpression(left ast.Expression) ast.Expression {
	expr := &ast.ConditionalExpression{Token: p.curToken(), Condition: left}
	p.nextToken() // skip ?
	// 两个分支都按 ternaryOperand 解析 (见 precedence.go 的常量说明)。
	//
	// 这是右结合的关键: 中缀循环的条件是 `precedence < peekPrecedence()`, 若分支用
	// TERNARY 自身解析, 后面紧跟的 `?` 因 `TERNARY < TERNARY` 为假而不会被消费,
	// 外层循环会把它捡走, 于是 `a ? b : c ? d : e` 被错解析为 `(a ? b : c) ? d : e`
	// —— 这正是 tabs_demo 面板渲染错分支的根因。
	// 降到比 TERNARY 更松的层级后: `?` 优先于当前层 → 分支能继续吞掉后续三元
	// (右结合); 而 `,` 与当前层同级且循环里有显式 COMMA 守卫 → 不会误吞逗号。
	expr.Consequence = p.parseExpression(ternaryOperand)
	if !p.expectPeek(lexer.COLON) {
		return nil
	}
	p.nextToken() // skip :
	expr.Alternative = p.parseExpression(ternaryOperand)
	return expr
}

// ==================== 解构模式解析 ====================

func (p *Parser) parseDestructuringPattern(isArray bool) ast.Expression {
	// 必须显式判空后再包成接口: 直接把 nil 的 *ast.ArrayPattern / *ast.ObjectPattern
	// 作为 ast.Expression 返回会得到**非 nil 的 typed nil**, 于是调用方的
	// `if param.Pattern == nil` 判空失效, 后续 collectPatternNames 在 nil 上取
	// 字段直接 panic。解析失败时统一返回真 nil 接口。
	if isArray {
		if pat := p.parseArrayPattern(); pat != nil {
			return pat
		}
		return nil
	}
	if pat := p.parseObjectPattern(); pat != nil {
		return pat
	}
	return nil
}

// parseMemberSuffix 解析已拿到对象表达式后的成员后缀链 (cur 停在 '.' 或 '[')。
// 用于赋值解构 / for-of 目标位置的成员目标 (x.y / x[k])。返回时 cur 停在
// 成员表达式之后的 token (分隔符) 上, 与标识符元素「cur 停在分隔符」的约定一致。
// 声明位置出现成员目标由编译器报早错 (解析期无法区分声明/赋值)。
func (p *Parser) parseMemberSuffix(left ast.Expression) (ast.Expression, bool) {
	expr := left
	for {
		if p.curTokenIs(lexer.DOT) {
			p.nextToken()
			if !p.curTokenIs(lexer.IDENTIFIER) && !isKeywordProperty(p.curToken().Type) {
				p.addError(fmt.Sprintf("expected property name, got %s", p.curToken().Type))
				return nil, false
			}
			expr = &ast.MemberExpression{
				Token:    p.curToken(),
				Object:   expr,
				Property: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal},
			}
			p.nextToken()
			continue
		}
		if p.curTokenIs(lexer.LBRACKET) {
			lb := p.curToken()
			p.nextToken()
			prop := p.parseExpression(LOWEST)
			if prop == nil {
				return nil, false
			}
			expr = &ast.MemberExpression{Token: lb, Object: expr, Property: prop, Computed: true}
			if !p.expectPeek(lexer.RBRACKET) {
				return nil, false
			}
			p.nextToken()
			continue
		}
		break
	}
	return expr, true
}

// shorthandKeyIsReserved 报告对象模式里 shorthand 属性 (如 `{ default }` / `{ extends }`)
// 的键是否为 ReservedWord。规范 (§12.6.2 + BindingIdentifier/IdentifierReference):
// shorthand 的键同时充当绑定名/引用名, 必须是 Identifier, 而 Identifier 不允许
// ReservedWord —— 故 `{ default }` / `{ extends }` / `{ if }` 都是 SyntaxError,
// 而 `{ default: x }` (带冒号) 的键只是 PropertyName, 合法。
//
// 只拦「总是保留」的词: let / yield / await / async / of / undefined 是上下文
// 关键字或普通标识符, 在对应 sloppy 语境里可作标识符, 不在此列。extends / enum /
// debugger 在词法层是 IDENTIFIER (未关键字化), 需按文本判。
// isReservedWordName 报告一个**名字字符串**是否为保留字 (含 IdentifierName
// 层面的保留字 enum/extends/debugger 与严格保留字 implements 等)。
// 与 shorthandKeyIsReserved 的差别: 后者靠 token 类型, 此处靠解码后的名字,
// 用于「转义拼出的保留字」判定 (转义标识符的 token 类型是 IDENTIFIER)。
func shorthandKeyIsReserved(tok lexer.Token) bool {
	switch tok.Type {
	case lexer.BREAK, lexer.CASE, lexer.CATCH, lexer.CLASS, lexer.CONST,
		lexer.CONTINUE, lexer.DEFAULT, lexer.DELETE, lexer.DO, lexer.ELSE,
		lexer.EXPORT, lexer.FINALLY, lexer.FOR, lexer.FUNCTION, lexer.IF,
		lexer.IMPORT, lexer.IN, lexer.INSTANCEOF, lexer.NEW, lexer.NULL,
		lexer.RETURN, lexer.SUPER, lexer.SWITCH, lexer.THIS, lexer.THROW,
		lexer.TRUE, lexer.FALSE, lexer.TRY, lexer.TYPEOF, lexer.VAR,
		lexer.VOID, lexer.WHILE, lexer.WITH:
		return true
	case lexer.IDENTIFIER:
		switch tok.Literal {
		case "enum", "extends", "debugger":
			return true
		}
	}
	return false
}

// checkStrictPatternTargets 对严格模式下的解构赋值模式做早错校验:
// 任一绑定目标名是 eval / arguments ⇒ SyntaxError
// (sec-assignment-operators-static-semantics-early-errors, AssignmentPattern:
// "It is a Syntax Error if AssignmentTargetType ... is not simple" 与
// 严格模式下 eval/arguments 不得作为赋值目标)。
// 只在 p.strict 为真时报错; sloppy 下 `{ eval = 0 } = {}` 合法。
func (p *Parser) checkStrictPatternTargets(pattern ast.Expression) {
	if !p.strict {
		return
	}
	switch n := pattern.(type) {
	case *ast.ArrayPattern:
		for _, e := range n.Elements {
			if e == nil || e.Target == nil {
				continue
			}
			p.checkStrictPatternTargets(e.Target)
		}
	case *ast.ObjectPattern:
		for _, pr := range n.Properties {
			if pr == nil || pr.Value == nil {
				continue
			}
			p.checkStrictPatternTargets(pr.Value)
		}
	case *ast.Identifier:
		if n.Value == "eval" || n.Value == "arguments" {
			p.addError(fmt.Sprintf("SyntaxError: Unexpected eval or arguments in strict mode"))
		}
	}
}

// checkShorthandKey 在对象模式的 shorthand 分支校验键不是保留字。
// 合法返回 true; 非法记错并返回 false (调用方应中止解析)。
func (p *Parser) checkShorthandKey(prop *ast.PatternProperty) bool {
	if id, ok := prop.Key.(*ast.Identifier); ok && shorthandKeyIsReserved(id.Token) {
		p.addError(fmt.Sprintf("SyntaxError: '%s' cannot be used as a shorthand binding name", id.Value))
		return false
	}
	// 转义拼出的保留字 shorthand (`{ bre\u0061k }`): token 类型是 IDENTIFIER
	// (非关键字), 但作 IdentifierReference 仍非法 —— 见 rejectEscapedReservedName。
	if id, ok := prop.Key.(*ast.Identifier); ok && id.Token.IdentHasEscape {
		before := len(p.errors.Errors)
		p.rejectEscapedReservedName(id.Value)
		if len(p.errors.Errors) > before {
			return false
		}
	}
	// yield 是上下文保留字: 严格模式/生成器/模块里作 shorthand 绑定/引用名是早错
	// (sloppy 非生成器里合法)。
	if id, ok := prop.Key.(*ast.Identifier); ok && id.Token.Type == lexer.YIELD && !p.yieldIsIdentifier() {
		p.addError("SyntaxError: 'yield' cannot be used as a shorthand binding name in strict mode code")
		return false
	}
	return true
}

func (p *Parser) parseArrayPattern() *ast.ArrayPattern {
	pattern := &ast.ArrayPattern{Token: p.curToken()}
	pattern.Elements = []*ast.PatternElement{}
	if !p.curTokenIs(lexer.LBRACKET) {
		p.addError("expected '[' in array pattern")
		return nil
	}
	p.nextToken()
	// 空模式 `[]` 不提前返回 —— 与下面的循环体保持同一约定: 返回时 cur 停在
	// **自己的闭合符** ']' 上, 由调用方决定何时越过。提前 nextToken 会让
	// `[[]]` / `const [] = x` / `for ([] of y)` 全部错位 (空内层把 outer 的
	// ']' 也吃掉, 于是 '=' 被当成默认值的开始)。
	for !p.curTokenIs(lexer.RBRACKET) && !p.curTokenIs(lexer.EOF) {
		// 空洞 (elision): `[, a]` / `[a, , b]` —— 占一个元素位但无绑定目标。
		// 编译器把它当成「取一次 next() 后丢弃」，Target 为 nil。
		if p.curTokenIs(lexer.COMMA) {
			pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: p.curToken()})
			p.nextToken()
			continue
		}
		elem := &ast.PatternElement{Token: p.curToken()}
		if p.curTokenIs(lexer.SPREAD_REST) {
			elem.Rest = true
			p.nextToken()
		}
		if p.curTokenIs(lexer.IDENTIFIER) {
			elem.Target = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken()
			// 成员目标 (for-of 赋值 LHS): x.y / x[k]。解析器无法在此处判定
			// 这是声明还是赋值 —— 声明位置出现成员目标由编译器报早错。
			if p.curTokenIs(lexer.DOT) || p.curTokenIs(lexer.LBRACKET) {
				member, ok := p.parseMemberSuffix(elem.Target)
				if !ok {
					return nil
				}
				elem.Target = member
			}
		} else if p.curTokenIs(lexer.LBRACKET) {
			elem.Target = p.parseArrayPattern()
			// 嵌套模式解析器把 cur 停在**它自己**的闭合符上 (与顶层调用约定一致),
			// 所以这里必须越过它才能回到本层的元素流 —— 不越过的话本层会在
			// 内层 ']' 上误判"元素结束", 于是 `const [a, [b]] = x` 报
			// "expected = after destructuring, got RBRACKET"。
			p.nextToken()
		} else if p.curTokenIs(lexer.LBRACE) {
			elem.Target = p.parseObjectPattern()
			p.nextToken() // 同上: 越过内层 '}'
		} else {
			p.addError(fmt.Sprintf("unexpected token: %s", p.curToken().Type))
			return nil
		}
		if p.curTokenIs(lexer.ASSIGN) {
			// Early error (规范 13.3.3): rest 元素不得有初始化器
			// —— [...x = []] 是 SyntaxError。
			if elem.Rest {
				p.addError("SyntaxError: rest element may not have a default initializer")
				return nil
			}
			p.nextToken()
			elem.Default = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符 (逗号或右括号)
		}
		pattern.Elements = append(pattern.Elements, elem)
		// Early error (规范 13.3.3 ArrayBindingPattern): rest 元素之后不得再有任何
		// 元素, 连尾逗号也不行 —— [...a, b] / [...a,] 都是 SyntaxError (Node 实测一致)。
		// rest 带初始化器已在上面单独拦截。
		if elem.Rest && p.curTokenIs(lexer.COMMA) {
			p.addError("SyntaxError: rest element must be the last element in array pattern")
			return nil
		}
		if p.curTokenIs(lexer.COMMA) {
			p.nextToken()
		}
	}
	if !p.curTokenIs(lexer.RBRACKET) {
		p.addError("expected ']' in array pattern")
		return nil
	}
	return pattern
}

func (p *Parser) parseObjectPattern() *ast.ObjectPattern {
	pattern := &ast.ObjectPattern{Token: p.curToken()}
	pattern.Properties = []*ast.PatternProperty{}
	if !p.curTokenIs(lexer.LBRACE) {
		p.addError("expected '{' in object pattern")
		return nil
	}
	p.nextToken()
	// 空模式 `{}` 同样不提前越过闭合符 —— 见 parseArrayPattern 的同款说明。
	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		// 对象 rest: {...rest} —— 必须是最后一项, 绑定位置只接受标识符
		// (声明语境; 赋值语境的成员目标在 literalToPattern 里处理)。
		if p.curTokenIs(lexer.SPREAD_REST) {
			p.nextToken()
			if !p.curTokenIs(lexer.IDENTIFIER) {
				p.addError(fmt.Sprintf("unexpected token: %s", p.curToken().Type))
				return nil
			}
			pattern.RestTarget = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken()
			if !p.curTokenIs(lexer.RBRACE) {
				p.addError("SyntaxError: object rest must be the last property")
				return nil
			}
			break
		}
		prop := &ast.PatternProperty{Token: p.curToken()}
		// 键与对象字面量同理: 标识符、字符串、数字字面量、可作属性名的关键字、
		// 或 [计算表达式]。
		switch {
		case p.curTokenIs(lexer.LBRACKET):
			prop.Computed = true
			p.nextToken()
			prop.Key = p.parseExpression(LOWEST)
			if prop.Key == nil {
				return nil
			}
			if !p.peekTokenIs(lexer.RBRACKET) {
				p.addError(fmt.Sprintf("expected ']' in computed property, got %s", p.peekToken().Type))
				return nil
			}
			p.nextToken() // consume ]
			p.nextToken() // cur = ':' / '(' ...
		case p.curTokenIs(lexer.IDENTIFIER) || p.curTokenIs(lexer.STRING_LITERAL) ||
			p.curTokenIs(lexer.INT_LITERAL) || p.curTokenIs(lexer.FLOAT_LITERAL) ||
			isKeywordProperty(p.curToken().Type):
			prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken()
		default:
			p.addError(fmt.Sprintf("unexpected token: %s", p.curToken().Type))
			return nil
		}
		switch {
		case p.curTokenIs(lexer.COMMA) || p.curTokenIs(lexer.RBRACE):
			if prop.Computed {
				p.addError("computed property name must be followed by ':'")
				return nil
			}
			if !p.checkShorthandKey(prop) {
				return nil
			}
			prop.Shorthand = true
			id, _ := prop.Key.(*ast.Identifier)
			prop.Value = id
		case p.curTokenIs(lexer.ASSIGN):
			// 简写 + 默认值: { a = 默认 }
			if prop.Computed {
				p.addError("computed property name must be followed by ':'")
				return nil
			}
			if !p.checkShorthandKey(prop) {
				return nil
			}
			prop.Shorthand = true
			id, _ := prop.Key.(*ast.Identifier)
			prop.Value = id
			p.nextToken()
			prop.Default = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符 (逗号或右花括号)
		case p.curTokenIs(lexer.COLON):
			p.nextToken()
			if !p.parseObjectPatternTarget(prop) {
				return nil
			}
		default:
			p.addError(fmt.Sprintf("expected ':' after property, got %s", p.curToken().Type))
			return nil
		}
		pattern.Properties = append(pattern.Properties, prop)
		if p.curTokenIs(lexer.COMMA) {
			p.nextToken()
		}
	}
	if !p.curTokenIs(lexer.RBRACE) {
		p.addError("expected '}' in object pattern")
		return nil
	}
	return pattern
}

// parseObjectPatternTarget 解析对象模式 `key:` 之后的绑定目标与可选默认值。
// 进入时 cur 停在目标首 token; 返回时 cur 停在目标(及其默认值)之后的
// 分隔符上 (逗号/右花括号), 与 parseObjectPattern 循环体约定一致。
func (p *Parser) parseObjectPatternTarget(prop *ast.PatternProperty) bool {
	nested := false
	switch {
	case p.curTokenIs(lexer.IDENTIFIER):
		var val ast.Expression = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		p.nextToken()
		// 成员目标 ({ key: obj.k }): 赋值解构里合法, 声明位置由编译器报早错。
		if p.curTokenIs(lexer.DOT) || p.curTokenIs(lexer.LBRACKET) {
			member, ok := p.parseMemberSuffix(val)
			if !ok {
				return false
			}
			val = member
		}
		prop.Value = val
	case p.curTokenIs(lexer.LBRACKET):
		prop.Value = p.parseArrayPattern()
		p.nextToken() // 越过内层 ']'
		nested = true
	case p.curTokenIs(lexer.LBRACE):
		prop.Value = p.parseObjectPattern()
		p.nextToken() // 越过内层 '}'
		nested = true
	default:
		p.addError(fmt.Sprintf("unexpected token: %s", p.curToken().Type))
		return false
	}
	// 嵌套模式/别名都可带默认值: { a: [b] = [] } / { a: obj.k = 1 }
	if p.curTokenIs(lexer.ASSIGN) {
		_ = nested
		p.nextToken()
		prop.Default = p.parseExpression(LOWEST)
		p.nextToken() // 前进到分隔符
	}
	return true
}

// ==================== throw / try-catch / switch / import-export 解析 ====================

func (p *Parser) parseThrowStatement() *ast.ThrowStatement {
	stmt := &ast.ThrowStatement{Token: p.curToken()}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseTryStatement() *ast.TryStatement {
	stmt := &ast.TryStatement{Token: p.curToken()}
	p.nextToken() // skip 'try'
	stmt.Body = p.parseBlockStatement()

	// catch
	if p.peekTokenIs(lexer.CATCH) {
		p.nextToken() // skip 'catch'
		// 可选 catch binding: catch { ... } (无参数)
		if p.peekTokenIs(lexer.LPAREN) {
			p.nextToken() // skip (
			switch {
			case p.peekTokenIs(lexer.IDENTIFIER):
				p.nextToken()
				stmt.CatchParam = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			case p.peekTokenIs(lexer.LBRACKET):
				// catch 解构绑定: catch ([a, b]) / catch ({x})
				p.nextToken()
				stmt.CatchParam = p.parseArrayPattern()
				if stmt.CatchParam == nil {
					return nil
				}
			case p.peekTokenIs(lexer.LBRACE):
				p.nextToken()
				stmt.CatchParam = p.parseObjectPattern()
				if stmt.CatchParam == nil {
					return nil
				}
			default:
				p.addError("expected identifier in catch clause")
				return nil
			}
			if !p.expectPeek(lexer.RPAREN) {
				return nil
			}
			p.nextToken() // skip ')' → 前进到 '{'
		} else {
			// 无参数 catch: 前进到 '{'
			p.nextToken()
		}
		stmt.CatchBody = p.parseBlockStatement()
	}

	// finally
	if p.peekTokenIs(lexer.FINALLY) {
		p.nextToken() // skip 'finally'
		p.nextToken()
		stmt.FinallyBody = p.parseBlockStatement()
	}

	return stmt
}

func (p *Parser) parseSwitchStatement() *ast.SwitchStatement {
	stmt := &ast.SwitchStatement{Token: p.curToken()}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()
	stmt.Discriminant = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		return nil
	}
	if !p.expectPeek(lexer.LBRACE) {
		return nil
	}
	p.nextToken()

	prevInLoop := p.inLoop
	p.inLoop = true // switch 内部允许 break
	// case 体不是模块顶层 —— `switch(0){case 1: export default null;}` 非法
	// (spec: ModuleItem, 见 parse-err-decl-pos-export-switch-*.js)。
	prevTop := p.moduleTopLevel
	p.moduleTopLevel = false
	defer func() { p.moduleTopLevel = prevTop }()

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		sc := &ast.SwitchCase{Token: p.curToken()}
		if p.curTokenIs(lexer.DEFAULT) {
			sc.Test = nil
		} else if p.curTokenIs(lexer.CASE) {
			p.nextToken()
			sc.Test = p.parseExpression(LOWEST)
		} else {
			p.addError(fmt.Sprintf("expected 'case' or 'default', got %s", p.curToken().Type))
			return nil
		}
		if !p.expectPeek(lexer.COLON) {
			return nil
		}
		p.nextToken()

		// 解析 case 体直到遇到 case/default/}
		// CaseClause/DefaultClause 的 StatementList **直接**包含 using 声明是早错
		// (sec-let-const-using-and-await-using-declarations-static-semantics-
		// early-errors 第 2 条)。嵌套块内的 using 仍合法 (parseBlockImpl 置 true)。
		prevUsingAllowed := p.usingAllowed
		p.usingAllowed = false
		for !p.curTokenIs(lexer.CASE) && !p.curTokenIs(lexer.DEFAULT) && !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
			s := p.parseStatement()
			if s != nil {
				sc.Statements = append(sc.Statements, s)
			}
			p.nextToken()
		}
		p.usingAllowed = prevUsingAllowed
		stmt.Cases = append(stmt.Cases, sc)
	}

	p.inLoop = prevInLoop
	return stmt
}

// parseDynamicImport 解析动态 import() 表达式。
// import("./mod.js") 返回 Promise。
func (p *Parser) parseDynamicImport() ast.Expression {
	di := &ast.DynamicImportExpression{Token: p.curToken()}
	if !p.expectPeek(lexer.LPAREN) {
		p.addError("expected '(' after import")
		return nil
	}
	p.nextToken()
	di.Source = p.parseExpression(LOWEST)
	if !p.expectPeek(lexer.RPAREN) {
		p.addError("expected ')' after import specifier")
		return nil
	}
	return di
}

// parseClassDeclaration 解析 class 声明。
// class Name [extends Super] { ... }
func (p *Parser) parseClassDeclaration() *ast.ClassDeclaration {
	cls := &ast.ClassDeclaration{Token: p.curToken()}

	if !p.expectPeek(lexer.IDENTIFIER) {
		p.addError("expected class name")
		return nil
	}
	cls.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	// 类名恒按严格模式判 (规范 10.2.1: 类整体是严格模式代码), 而此处尚未
	// setStrict(true) —— 显式传 strict=true
	// (test262 class-name-ident-{let,static,yield,await-module}-escaped.js)。
	if cls.Name.Token.IdentHasEscape {
		p.rejectEscapedReservedNameIn(cls.Name.Value, true)
	}

	// 可选 extends 子句 (extends 作为标识符处理, 无专用 token)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Literal == "extends" {
		p.nextToken()
		p.nextToken()
		p.checkClassHeritageEarlyError() // 裸箭头 heritage 早错（带括号的合法形式放行）
		cls.SuperClass = p.parseExpression(LOWEST)
		if cls.SuperClass == nil {
			return nil
		}
	}

	// class 主体
	if !p.expectPeek(lexer.LBRACE) {
		p.addError(fmt.Sprintf("expected '{' in class, got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken()   // 进入成员区域
	// 类体恒严格 (ClassBody 内的方法/字段初始化器按 strict 解析, 与是否
	// 含指令无关) —— extends 表达式在外层上下文里求值, 故包裹从成员区域开始。
	restoreStrict := p.setStrict(true)
	defer restoreStrict()
	p.pushPrivEnv() // 私有名环境压栈; defer 弹出保证早退路径也平衡
	defer p.popPrivEnv()

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		if p.curTokenIs(lexer.SEMICOLON) {
			// 空成员 (字段分隔符)
			p.nextToken()
			continue
		}
		member := p.parseClassMember()
		if member == nil {
			// 解析失败: 强制推进避免死循环
			p.nextToken()
			continue
		}
		if member.IsStatic {
			cls.Statics = append(cls.Statics, member)
		} else if member.IsConstructor {
			cls.Methods = append(cls.Methods, member)
		} else if member.Name == "constructor" {
			member.IsConstructor = true
			cls.Methods = append(cls.Methods, member)
		} else if member.IsGetter || member.IsSetter {
			cls.Methods = append(cls.Methods, member)
		} else if member.Body != nil {
			// 方法定义
			cls.Methods = append(cls.Methods, member)
		} else {
			// 实例字段: name = value
			field := &ast.ClassField{Token: member.Token, Name: member.Name, IsPrivate: member.IsPrivate, ComputedKey: member.ComputedKey, Value: member.FieldValue}
			cls.Fields = append(cls.Fields, field)
		}
		// 跳过成员间的分隔符 (分号)
		for p.curTokenIs(lexer.SEMICOLON) {
			p.nextToken()
		}
	}

	// 私有名相关早错集中校验（重复私有名 / 字段初始化器含 arguments·super /
	// 引用未声明私有名）。必须在成员循环之后: 判重与判未声明引用都需要
	// 先看全所有成员（元素顺序上引用可以先于声明）。
	// 类方法恒严格: 形参名 strict 早错 (重复名 / eval·arguments 作形参名)。
	p.checkClassMethodsStrictParams(cls.Methods, cls.Statics)
	p.checkClassEarlyErrors(cls.Methods, cls.Statics, cls.Fields)
	// 语法级早错（static prototype / 特殊方法名 constructor / 重复构造器 /
	// HasDirectSuper 的 super() 误用）。同类集中校验, 见 class_grammar_early_errors.go。
	p.checkClassGrammarEarlyErrors(cls.SuperClass, cls.Methods, cls.Statics, cls.Fields)

	if !p.curTokenIs(lexer.RBRACE) {
		p.addError(fmt.Sprintf("expected '}' in class, got %s", p.curToken().Type))
		return nil
	}
	return cls
}

// parseClassExpression 解析 class 表达式 (prefix 解析入口)。
// class [Name] [extends Super] { ... } —— 与声明共用成员循环;
// 差异: 类名可选 (匿名 class {}), 解析产物是有值的表达式。
func (p *Parser) parseClassExpression() ast.Expression {
	cls := &ast.ClassExpression{Token: p.curToken()}

	// 可选类名: peek 为 IDENTIFIER 且不是 extends 才是类名
	// (匿名带继承的 "class extends Base {}" 中 extends 也是 IDENTIFIER)。
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Literal != "extends" {
		p.nextToken()
		cls.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		// 类名恒严格, 见 parseClassDeclaration 同款说明。
		if cls.Name.Token.IdentHasEscape {
			p.rejectEscapedReservedNameIn(cls.Name.Value, true)
		}
	}

	// 可选 extends 子句 (与声明一致: 仅支持 Identifier 形式)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Literal == "extends" {
		p.nextToken() // cur = extends
		p.nextToken() // cur = 父类首 token
		p.checkClassHeritageEarlyError() // 裸箭头 heritage 早错（带括号的合法形式放行）
		cls.SuperClass = p.parseExpression(LOWEST)
		if cls.SuperClass == nil {
			return nil
		}
	}

	// class 主体 (与 parseClassDeclaration 的成员循环一致)
	if !p.expectPeek(lexer.LBRACE) {
		p.addError(fmt.Sprintf("expected '{' in class, got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken()   // 进入成员区域
	// 类体恒严格 (ClassBody 内的方法/字段初始化器按 strict 解析, 与是否
	// 含指令无关) —— extends 表达式在外层上下文里求值, 故包裹从成员区域开始。
	restoreStrict := p.setStrict(true)
	defer restoreStrict()
	p.pushPrivEnv() // 私有名环境压栈; defer 弹出保证早退路径也平衡
	defer p.popPrivEnv()

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		if p.curTokenIs(lexer.SEMICOLON) {
			p.nextToken()
			continue
		}
		member := p.parseClassMember()
		if member == nil {
			p.nextToken() // 解析失败: 强制推进避免死循环
			continue
		}
		if member.IsStatic {
			cls.Statics = append(cls.Statics, member)
		} else if member.IsConstructor {
			cls.Methods = append(cls.Methods, member)
		} else if member.Name == "constructor" {
			member.IsConstructor = true
			cls.Methods = append(cls.Methods, member)
		} else if member.IsGetter || member.IsSetter {
			cls.Methods = append(cls.Methods, member)
		} else if member.Body != nil {
			cls.Methods = append(cls.Methods, member)
		} else {
			field := &ast.ClassField{Token: member.Token, Name: member.Name, IsPrivate: member.IsPrivate, ComputedKey: member.ComputedKey, Value: member.FieldValue}
			cls.Fields = append(cls.Fields, field)
		}
		for p.curTokenIs(lexer.SEMICOLON) {
			p.nextToken()
		}
	}

	// 私有名早错校验（与 parseClassDeclaration 同一处挂载点, 口径一致）。
	// 类方法恒严格: 形参名 strict 早错 (重复名 / eval·arguments 作形参名)。
	p.checkClassMethodsStrictParams(cls.Methods, cls.Statics)
	p.checkClassEarlyErrors(cls.Methods, cls.Statics, cls.Fields)
	// 语法级早错（与 parseClassDeclaration 同一处挂载点, 口径一致）。
	p.checkClassGrammarEarlyErrors(cls.SuperClass, cls.Methods, cls.Statics, cls.Fields)

	if !p.curTokenIs(lexer.RBRACE) {
		p.addError(fmt.Sprintf("expected '}' in class, got %s", p.curToken().Type))
		return nil
	}
	return cls
}

// parseClassMember 解析 class 主体中的一个成员。
// 返回的成员可能: 是方法 (Body != nil)、static 方法、或字段 (FieldValue != nil)。
// 约定: 返回时 curToken 位于成员结束后的下一个 token (分隔符/下一成员/class 结束 })。
func (p *Parser) parseClassMember() *ast.ClassMethod {
	member := &ast.ClassMethod{Token: p.curToken()}

	// 私有成员: #name (字段/方法/访问器都支持)。# 是名字的一部分,
	// Name 存含 # 的完整形式, IsPrivate 标记供编译器分发。
	if p.curTokenIs(lexer.PRIVATE_NAME) {
		return p.parsePrivateMember(member)
	}

	// static 关键字
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "static" &&
		!p.curToken().IdentHasEscape &&
		!p.peekTokenIs(lexer.LPAREN) && !p.peekTokenIs(lexer.ASSIGN) {
		member.IsStatic = true
		p.nextToken()
		// static #name ...: static 之后是私有成员, 进入私有分支
		// (IsStatic 已带上, parsePrivateMember 里会作为静态处理)
		if p.curTokenIs(lexer.PRIVATE_NAME) {
			return p.parsePrivateMember(member)
		}
		// static { ... }: 静态初始化块 (ES2022 ClassStaticBlock)。
		// static 后紧跟 '{' 只可能是静态块 (字段初始化器需要 '=' 或终止符,
		// 方法需要名字/参数表), 故这里无歧义。
		if p.curTokenIs(lexer.LBRACE) {
			return p.parseStaticBlock(member)
		}
	}

	// async 方法/生成器: async name() {} / async *name() {} / async [expr]() {}
	// async 只有构成修饰符前缀时才是关键字 (判据见 asyncModifierAhead); 否则
	// 它是成员名 (`async(){}` 是名为 async 的方法, `async = 1` 是名为 async
	// 的字段)。名字可以是任意 PropertyName, 含 `async` / `await` (r81aQt)。
	isAsyncTok := p.curTokenIs(lexer.ASYNC) ||
		(p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "async" &&
			!p.curToken().IdentHasEscape)
	if isAsyncTok && p.asyncModifierAhead() {
		member.IsAsync = true
		if p.peekTokenIs(lexer.ASTERISK) {
			member.IsGenerator = true
			p.nextToken() // cur = *
		}
		p.nextToken() // cur = 方法名 / [
	}

	// 生成器方法: *name() {} / *[expr]() {} —— 剩余路径与方法一致
	if p.curTokenIs(lexer.ASTERISK) {
		member.IsGenerator = true
		p.nextToken() // cur = 方法名 / [
	}

	// 私有生成器方法: *#name() {} / async *#name() {} —— IsGenerator/IsAsync
	// 已带上, 名字是 PRIVATE_NAME, 交 parsePrivateMember 收尾。
	if p.curTokenIs(lexer.PRIVATE_NAME) {
		return p.parsePrivateMember(member)
	}

	// 计算属性名: [expr] —— 方法名/字段名以表达式求值结果为准
	if p.curTokenIs(lexer.LBRACKET) {
		p.nextToken()
		member.ComputedKey = p.parseExpression(LOWEST)
		if member.ComputedKey == nil {
			return nil
		}
		if !p.peekTokenIs(lexer.RBRACKET) {
			p.addError(fmt.Sprintf("expected ']' after computed property name, got %s", p.peekToken().Type))
			return nil
		}
		p.nextToken() // consume ]
		p.nextToken() // cur = ( 或 =
	}

	// get/set 访问器: get name() {} / set name(v) {} / get [expr]() / set [expr](v)
	// get/set 后跟 IDENTIFIER+( 或 [ 时按访问器处理, 其余情况它是字段名。
	// get #name() / set #name(v): 私有访问器 —— 分发进 parsePrivateMember
	// 之前的特判 (其内部按访问器形状解析)。
	if p.curTokenIs(lexer.IDENTIFIER) && !p.curToken().IdentHasEscape &&
		(p.curToken().Literal == "get" || p.curToken().Literal == "set") &&
		p.peekTokenIs(lexer.PRIVATE_NAME) {
		isGet := p.curToken().Literal == "get"
		if isGet {
			member.IsGetter = true
		} else {
			member.IsSetter = true
		}
		p.nextToken() // cur = #name
		return p.parsePrivateAccessor(member)
	}
	if p.curTokenIs(lexer.IDENTIFIER) && !p.curToken().IdentHasEscape &&
		(p.curToken().Literal == "get" || p.curToken().Literal == "set") &&
		((p.peekTokenIs(lexer.IDENTIFIER) && p.peek2TokenIs(lexer.LPAREN)) ||
			p.peekTokenIs(lexer.LBRACKET)) {
		isGet := p.curToken().Literal == "get"
		if isGet {
			member.IsGetter = true
		} else {
			member.IsSetter = true
		}
		p.nextToken() // cur = 属性名 / [
		if p.curTokenIs(lexer.LBRACKET) {
			// 计算属性访问器: get [expr]() {}
			p.nextToken()
			member.ComputedKey = p.parseExpression(LOWEST)
			if member.ComputedKey == nil {
				return nil
			}
			if !p.peekTokenIs(lexer.RBRACKET) {
				p.addError(fmt.Sprintf("expected ']' after computed property name, got %s", p.peekToken().Type))
				return nil
			}
			p.nextToken() // consume ]
			p.nextToken() // cur = (
		} else {
			member.Name = p.curToken().Literal
			p.nextToken()
		}
		// 访问器不能是 async —— 同步上下文
	// 私有访问器 / 类访问器 / constructor: 同步上下文
	restore := p.setAllowAwait(false)
	member.Parameters = p.parseParameters(lexer.RPAREN, false, false)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken()
		member.Body = p.parseFunctionBody(false)
		restore()
		p.nextToken() // 前进到下一个成员/分隔符
		return member
	}

	// constructor 特殊处理 (标识符 constructor 后跟 '(')
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "constructor" && p.peekTokenIs(lexer.LPAREN) {
		member.Name = "constructor"
		p.nextToken()
		// constructor 不能是 async —— 同步上下文
	// 私有访问器 / 类访问器 / constructor: 同步上下文
	restore := p.setAllowAwait(false)
	member.Parameters = p.parseParameters(lexer.RPAREN, false, false)
		if !p.curTokenIs(lexer.RPAREN) {
			restore()
			return nil
		}
		p.nextToken()
		member.Body = p.parseFunctionBody(false)
		restore()
		p.nextToken() // 前进到下一个成员/分隔符
		return member
	}
	// 字段名不得为 constructor (规范 ClassElement 早错误: FieldDefinition
	// 的 PropName 为 "constructor" 是 SyntaxError)。不拦的话它会以
	// IsConstructor=true + Body=nil 进 Methods, 编译器读 Body.Statements
	// 直接 panic (2026-10-05 由 test262 fields-literal-name-propname-
	// constructor 2 例 crashed 抓出)。
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "constructor" &&
		(p.peekTokenIs(lexer.SEMICOLON) || p.peekTokenIs(lexer.RBRACE) || p.peekTokenIs(lexer.ASSIGN) || p.peekTokenIs(lexer.COMMA)) {
		p.addError("SyntaxError: class field must not be named 'constructor'")
		return nil
	}

	// 方法或字段 (计算属性名已在前面解析时, cur 已停在 ( 或 =)
	// 成员名允许 IDENTIFIER 以及 `async` / `await` (二者在 sloppy 下可作
	// IdentifierName; 之前只认 IDENTIFIER, 于是 `async async(){}` /
	// `async(){}` 都在此被拒 —— r81aQt)。
	if member.ComputedKey != nil || p.curTokenIs(lexer.IDENTIFIER) ||
		p.curTokenIs(lexer.ASYNC) || p.curTokenIs(lexer.AWAIT) {
		if member.ComputedKey == nil {
			member.Name = p.curToken().Literal
			p.nextToken()
		}
		if p.curTokenIs(lexer.LPAREN) {
			// 方法定义: name(params) { body } / [expr](params) { body }
			restore := p.setAllowAwait(member.IsAsync)
			member.Parameters = p.parseParameters(lexer.RPAREN, member.IsAsync, member.IsGenerator)
			if !p.curTokenIs(lexer.RPAREN) {
				restore()
				return nil
			}
			p.nextToken()
			member.Body = p.parseFunctionBody(member.IsGenerator)
			restore()
			p.nextToken() // 前进到下一个成员/分隔符
			return member
		}
		if p.curTokenIs(lexer.ASSIGN) {
			// 实例字段: name = expr / [expr] = expr
			p.nextToken()
			member.FieldValue = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符/下一个成员
			p.checkClassFieldTermination()
			return member
		}
		// 裸字段（命名或计算，无初始化器）: name / [expr]。
		// 命名裸字段: 名字已在上面 nextToken 越过，此时 cur 即字段后的终止
		// token —— 不能再前进一次，否则会吞掉下一个成员（历史缺陷:
		// class C { x } / 多裸字段换行都因此被破坏）。
		// 计算裸字段: [expr] 之后 cur 停在 ';' / '}' / 下一成员，与命名裸字段
		// 共用同一套 ASI/终止校验（class C { static ["prototype"]; } 合法）。
		p.checkClassFieldTermination()
		return member
	}

	p.addError(fmt.Sprintf("unexpected token in class body: %s", p.curToken().Type))
	return nil
}

// parseSuperExpression 解析 super 关键字。
// super(...) → 父构造函数调用; super.method / super[expr] → 父类成员访问。
//
// SuperProperty 的两种形态 (sec-super-property): super . IdentifierName 与
// super [ Expression ]。后者此前被前缀解析直接拒 (只认 '(' / '.')，
// 使 test262 *-contains-superproperty-2.js 一族 (computed 形式) 恒 SyntaxError
// (roiE5Z 遗留边界)。实为纯解析缺口: 中缀下标解析 (parseIndexExpression) 与
// 编译器 super 成员访问分支 (compileMemberExpression / emitEvalSuperProperty)
// 早已支持 Computed。
func (p *Parser) parseSuperExpression() ast.Expression {
	super := &ast.SuperExpression{Token: p.curToken()}

	// super(...) / super.method / super[expr]
	if p.peekTokenIs(lexer.LPAREN) || p.peekTokenIs(lexer.DOT) || p.peekTokenIs(lexer.LBRACKET) {
		// 返回 SuperExpression 本身, 由中缀解析继续处理
		// (LPAREN → 调用, DOT → 点访问, LBRACKET → 计算成员访问)
		return super
	}
	p.addError("super must be followed by '(', '.' or '['")
	return nil
}

func (p *Parser) parseImportDeclaration() *ast.ImportDeclaration {
	stmt := &ast.ImportDeclaration{Token: p.curToken()}
	p.nextToken()

	// import "module.js" (副作用导入)
	if p.curTokenIs(lexer.STRING_LITERAL) {
		stmt.Source = p.curToken().Literal
		p.checkSameLineASI()
		p.consumeSemicolon()
		return stmt
	}

	// import defaultName from "..."
	if p.curTokenIs(lexer.IDENTIFIER) {
		stmt.DefaultName = p.curToken().Literal
		p.nextToken()
		if p.curTokenIs(lexer.COMMA) {
			p.nextToken()
		}
	}

	// import * as ns from "..."
	if p.curTokenIs(lexer.ASTERISK) {
		p.nextToken()
		if !p.curTokenIs(lexer.IDENTIFIER) || p.curToken().Literal != "as" ||
			p.curToken().IdentHasEscape {
			p.addError("expected 'as' after '*' in import")
			return nil
		}
		p.nextToken()
		if !p.curTokenIs(lexer.IDENTIFIER) {
			p.addError("expected namespace name after 'as'")
			return nil
		}
		stmt.Namespace = p.curToken().Literal
		p.nextToken()
	} else if p.curTokenIs(lexer.LBRACE) {
		// import { a, b as c } from "..."
		p.nextToken()
		for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
			if !p.curTokenIs(lexer.IDENTIFIER) {
				p.addError("expected identifier in import")
				return nil
			}
			// 首段是"模块导出的名字", 别名 (as 之后) 才是"本文件绑定的名字"。
			// 两者不能混成一个 string —— 否则 `{ x as y }` 会被拆成三个独立
			// 名字 [x, as, y], y 永远绑不上 (见 ast.NamedImport 的注释)。
			item := ast.NamedImport{Imported: p.curToken().Literal, Local: p.curToken().Literal}
			p.nextToken()
			if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "as" &&
				!p.curToken().IdentHasEscape {
				p.nextToken()
				if !p.curTokenIs(lexer.IDENTIFIER) {
					p.addError("expected name after 'as' in import")
					return nil
				}
				item.Local = p.curToken().Literal
				p.nextToken()
			}
			stmt.NamedImports = append(stmt.NamedImports, item)
			// `{ a, }` / `{ a, b }` 都允许; 但 specifier 之间必须有逗号分隔 ——
			// 缺分隔符时若继续循环, 会把转义拼出的 `as` (`{a \u0061s b}`) 当成
			// 第二个 ImportedBinding 静默接受 (test262
			// import/escaped-as-import-specifier.js 要求早错)。
			if p.curTokenIs(lexer.COMMA) {
				p.nextToken()
				continue
			}
			if !p.curTokenIs(lexer.RBRACE) {
				p.addError(fmt.Sprintf("expected ',' or '}' in import, got %s", p.curToken().Type))
				return nil
			}
		}
		if !p.curTokenIs(lexer.RBRACE) {
			p.addError("expected '}' in import")
			return nil
		}
		p.nextToken()
	}

	// from
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "from" &&
		!p.curToken().IdentHasEscape {
		p.nextToken()
	} else {
		p.addError("expected 'from' in import")
		return nil
	}

	if !p.curTokenIs(lexer.STRING_LITERAL) {
		p.addError("expected module path string in import")
		return nil
	}
	stmt.Source = p.curToken().Literal
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// parseExportDeclaration 解析 export 声明。
//
// ES 规范里 export 后面可以跟的东西比早期实现覆盖的多得多, 这里逐一补齐
// (TS 产物与 npm 包 barrel 都重度依赖这些形式 —— 详见 docs/typescript.md):
//
//	export default <expr | function | class>
//	export * from "m" / export * as ns from "m"
//	export { a, b as c } [from "m"] / export {}
//	export var/let/const/function/class ...
//	export async function / async function* ...
//
// 解析失败时 addError 会带上当前 token 的准确行列, 而不是笼统的 "syntax error"。
func (p *Parser) parseExportDeclaration() *ast.ExportDeclaration {
	stmt := &ast.ExportDeclaration{Token: p.curToken()}
	p.nextToken()

	// export default ...
	if p.curTokenIs(lexer.DEFAULT) {
		return p.parseExportDefault(stmt)
	}

	// export * from "..."  /  export * as ns from "..."
	if p.curTokenIs(lexer.ASTERISK) {
		return p.parseExportStar(stmt)
	}

	// export { ... } [from "..."]
	if p.curTokenIs(lexer.LBRACE) {
		return p.parseExportNamed(stmt)
	}

	// export var/let/const/function/class/async function ...
	if p.isExportDeclStart() {
		if !p.parseExportDeclInto(&stmt.Declaration) {
			return nil
		}
		return stmt
	}

	p.addError(fmt.Sprintf("unexpected token after export: %s", p.curToken().Type))
	return nil
}

// isExportDeclStart 判断 export 后当前 token 能否开始一个"可导出声明"。
// 注意 ASYNC 只有在后跟 FUNCTION 时才算声明 —— 模块顶层的 export 不允许裸
// 表达式 (只有 default 允许), 所以这里收紧成只认 async function, 让
// `export async ...` 落到"准确报错"分支而不是被当成表达式静默吞掉。
func (p *Parser) isExportDeclStart() bool {
	switch p.curToken().Type {
	case lexer.VAR, lexer.LET, lexer.CONST, lexer.FUNCTION, lexer.CLASS:
		return true
	case lexer.ASYNC:
		return p.peekTokenIs(lexer.FUNCTION)
	}
	return false
}

// parseExportDeclInto 解析 export 后的声明并写入 out。
// 返回 false 表示下层解析已经报错 (调用方直接返回 nil, 避免把半截 AST 交出去)。
//
// 不直接返回 ast.Statement 是因为各 parseXxx 返回的是具体指针类型: 一个 nil 的
// *ast.LetStatement 装进 Statement 接口后"不等于 nil", 调用方没法用 == nil 判空。
func (p *Parser) parseExportDeclInto(out *ast.Statement) bool {
	switch p.curToken().Type {
	case lexer.VAR:
		if s := p.parseVarStatement(); s != nil {
			*out = s
			return true
		}
	case lexer.LET:
		if s := p.parseLetStatement(); s != nil {
			*out = s
			return true
		}
	case lexer.CONST:
		if s := p.parseConstStatement(); s != nil {
			*out = s
			return true
		}
	case lexer.FUNCTION:
		if s := p.parseFunctionDeclaration(false); s != nil {
			*out = s
			return true
		}
	case lexer.CLASS:
		if s := p.parseClassDeclaration(); s != nil {
			*out = s
			return true
		}
	case lexer.ASYNC:
		p.nextToken() // cur = function
		if fn := p.parseFunctionDeclaration(true); fn != nil {
			*out = fn
			return true
		}
	}
	return false
}

// parseExportDefault 解析 export default 后的部分。
//
// 具名默认导出的语义 (规范 16.2.3.7): `export default function f(){}` 里的 f
// 只作为**模块内局部绑定**存在, 不是命名导出 (命名导出只有 "default")。
// 因此具名时走 FunctionDeclaration/ClassDeclaration (会登记局部绑定),
// 匿名时走表达式路径。这样既保证 f 在模块内可用, 又不会把 f 混进导出表。
func (p *Parser) parseExportDefault(stmt *ast.ExportDeclaration) *ast.ExportDeclaration {
	stmt.IsDefault = true
	p.nextToken() // 越过 default

	switch p.curToken().Type {
	case lexer.FUNCTION:
		if p.defaultFunctionIsNamed() {
			fn := p.parseFunctionDeclaration(false)
			if fn == nil {
				return nil
			}
			stmt.Declaration = fn
			return stmt
		}
		fn := p.parseAnonymousFunctionExpression(false)
		if fn == nil {
			return nil
		}
		stmt.Declaration = &ast.ExpressionStatement{Token: fn.Token, Expression: fn}
		p.consumeSemicolon()
		return stmt

	case lexer.ASYNC:
		p.nextToken() // cur = function
		if p.defaultFunctionIsNamed() {
			fn := p.parseFunctionDeclaration(true)
			if fn == nil {
				return nil
			}
			stmt.Declaration = fn
			return stmt
		}
		fn := p.parseAnonymousFunctionExpression(true)
		if fn == nil {
			return nil
		}
		stmt.Declaration = &ast.ExpressionStatement{Token: fn.Token, Expression: fn}
		p.consumeSemicolon()
		return stmt

	case lexer.CLASS:
		// 具名类 (class C {...}) 与匿名类 (class {...}) 分开; extends 也是
		// 标识符, 别把 `class extends Base {}` 的 extends 当类名 (见
		// parseClassExpression 的同类判断)。
		if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Literal != "extends" {
			cls := p.parseClassDeclaration()
			if cls == nil {
				return nil
			}
			stmt.Declaration = cls
			return stmt
		}
		cls := p.parseClassExpression()
		if cls == nil {
			return nil
		}
		stmt.Declaration = &ast.ExpressionStatement{Token: p.curToken(), Expression: cls}
		p.consumeSemicolon()
		return stmt

	default:
		expr := p.parseExpression(LOWEST)
		if expr == nil {
			return nil
		}
		stmt.Declaration = &ast.ExpressionStatement{Token: p.curToken(), Expression: expr}
		p.checkSameLineASI()
		p.consumeSemicolon()
		return stmt
	}
}

// defaultFunctionIsNamed 判断 export default 后的 function 是否具名。
// 具名: function f / function* f; 匿名: function() / function*()。
func (p *Parser) defaultFunctionIsNamed() bool {
	if p.peekTokenIs(lexer.IDENTIFIER) {
		return true
	}
	return p.peekTokenIs(lexer.ASTERISK) && p.peek2TokenIs(lexer.IDENTIFIER)
}

// parseAnonymousFunctionExpression 从 FUNCTION 起始解析一个匿名(或可选具名)
// 函数表达式, 供 export default 的匿名默认函数使用。cur 位于 FUNCTION。
// parseAnonymousFunctionExpression 解析 export default 后的匿名函数表达式。
// isAsync 决定函数体内的 async 上下文 (export default async function(){})。
func (p *Parser) parseAnonymousFunctionExpression(isAsync bool) *ast.FunctionExpression {
	fn := &ast.FunctionExpression{Token: p.curToken(), IsAsync: isAsync}
	if p.peekTokenIs(lexer.ASTERISK) {
		fn.IsGenerator = true
		p.nextToken() // cur = *
	}
	if p.peekTokenIs(lexer.IDENTIFIER) {
		p.nextToken()
		fn.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	fn.Parameters = p.parseParameters(lexer.RPAREN, isAsync, fn.IsGenerator)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	restore := p.setAllowAwait(isAsync)
	fn.Body = p.parseFunctionBody(fn.IsGenerator)
	restore()
	return fn
}

// parseExportStar 解析 export * from "m" 与 export * as ns from "m"。
// cur 位于 ASTERISK。
func (p *Parser) parseExportStar(stmt *ast.ExportDeclaration) *ast.ExportDeclaration {
	p.nextToken() // 越过 *

	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "as" &&
		!p.curToken().IdentHasEscape {
		// export * as ns from "m": 以命名空间对象的形式再导出。
		p.nextToken()
		if !p.curTokenIs(lexer.IDENTIFIER) {
			p.addError("expected namespace name after 'as' in export *")
			return nil
		}
		stmt.Specifiers = []ast.ExportSpecifier{{Local: "*", Exported: p.curToken().Literal}}
		p.nextToken()
	} else {
		stmt.IsStar = true
	}

	if !p.curTokenIs(lexer.IDENTIFIER) || p.curToken().Literal != "from" ||
		p.curToken().IdentHasEscape {
		p.addError(fmt.Sprintf("expected 'from' in export *, got %s", p.curToken().Type))
		return nil
	}
	p.nextToken()
	if !p.curTokenIs(lexer.STRING_LITERAL) {
		p.addError("expected module path string in export *")
		return nil
	}
	stmt.Source = p.curToken().Literal
	p.checkSameLineASI()
	p.consumeSemicolon()
	return stmt
}

// parseExportNamed 解析 export { ... } [from "..."] (含空导出 export {})。
// cur 位于 LBRACE。
func (p *Parser) parseExportNamed(stmt *ast.ExportDeclaration) *ast.ExportDeclaration {
	p.nextToken() // 越过 {

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		// `default` 也是合法的导出名 (ES 把它当保留的导出名):
		//   export { default } from "m"        // 再导出源模块的 default
		//   export { a as default }            // 把 a 作为本模块的 default
		// 它在词法层是专门的 DEFAULT token, 不是 IDENTIFIER。
		if !p.curTokenIs(lexer.IDENTIFIER) && !p.curTokenIs(lexer.DEFAULT) {
			p.addError(fmt.Sprintf("expected identifier in export, got %s", p.curToken().Type))
			return nil
		}
		sp := ast.ExportSpecifier{Local: p.curToken().Literal, Exported: p.curToken().Literal}
		p.nextToken()
		if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "as" &&
			!p.curToken().IdentHasEscape {
			p.nextToken()
			if !p.curTokenIs(lexer.IDENTIFIER) && !p.curTokenIs(lexer.DEFAULT) {
				p.addError("expected name after 'as' in export")
				return nil
			}
			sp.Exported = p.curToken().Literal
			p.nextToken()
		}
		stmt.Specifiers = append(stmt.Specifiers, sp)
		// 分隔符: 逗号或结束花括号。缺分隔符 (`{ a b }`) 是明确语法错误,
		// 不能悄悄当成两个导出 —— 否则别名写漏 as 会被静默接受。
		if p.curTokenIs(lexer.COMMA) {
			p.nextToken()
			continue
		}
		if p.curTokenIs(lexer.RBRACE) {
			break
		}
		p.addError(fmt.Sprintf("expected ',' or '}' in export, got %s", p.curToken().Type))
		return nil
	}
	if !p.curTokenIs(lexer.RBRACE) {
		p.addError("expected '}' in export")
		return nil
	}
	endLine := p.curToken().Line // `}` 所在行 —— ASI 判定基准
	p.nextToken()

	// 可选 `from "..."` → 具名再导出
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "from" &&
		!p.curToken().IdentHasEscape {
		p.nextToken()
		if !p.curTokenIs(lexer.STRING_LITERAL) {
			p.addError("expected module path in export from")
			return nil
		}
		stmt.Source = p.curToken().Literal
		endLine = p.curToken().Line // 有 from 时以源串所在行为基准
		p.nextToken()
	}

	// 规范: export NamedExports [FromClause] 必须由 ';' 或换行终止。`export {} null;`
	// 里 null 与子句同行 ⇒ SyntaxError (test262 parse-err-semi-named-export{,-from}.js)。
	p.checkExportClauseTerminator(endLine)
	p.consumeSemicolon()
	return stmt
}

// ==================== 辅助方法 ====================

// consumeSemicolon 消费可选的分号。
//
// 注意: 这里**不**做「同一行缺分号」的 ASI 早错 —— 调用点太多且 cur/peek
// 状态不一致 (如 parseExportNamed 在调用前已把 cur 推过 `}`)。同一行缺分号
// 的检查收拢在 parseExpressionStatement 单点 (见 checkSameLineASI)。
func (p *Parser) consumeSemicolon() {
	if p.peekTokenIs(lexer.SEMICOLON) {
		p.nextToken()
	}
}

// checkExportClauseTerminator 检查 export 子句是否被 ';' 或换行终止。
// 调用时 curToken 是子句之后紧跟的 token (peek 才是它的后继), endLine 是子句
// 末 token 所在行。同行的非终止 token ⇒ SyntaxError —— 若不报, `export {} null;`
// 会被静默当成 export {} 然后 null 当独立语句执行 (test262
// parse-err-semi-named-export.js / parse-err-semi-named-export-from.js)。
func (p *Parser) checkExportClauseTerminator(endLine int) {
	if p.curTokenIs(lexer.SEMICOLON) || p.curTokenIs(lexer.RBRACE) || p.curTokenIs(lexer.EOF) {
		return
	}
	if p.curToken().Line == endLine {
		p.addError(fmt.Sprintf("SyntaxError: export declaration requires a trailing semicolon, got %s", p.curToken().Type))
	}
}

// checkSameLineASI 在表达式语句收尾处检查「同一行缺分号」。
//
// ASI (spec 12.9 / S7.9): 分号只在三种情形下自动插入 ——
//   1) 下一 token 前有换行 (LineTerminator);
//   2) 下一 token 是 `}` ;
//   3) 到达输入末尾。
// 因此「同一行、下一 token 既非 `;` 也非 `}`/EOF」= 缺分号 ⇒ SyntaxError。
// 例如 `{1 2} 3` 里第一个 1 之后紧跟同行的 2, 既不换行也不收块, 必须报错
// (test262 asi/S7.9_A10_T8.js); 而 `{ 1 \n 2 } 3` 换行可插入分号, 合法
// (asi/S7.9.2_A1_T2.js)。
//
// 调用前提 (parseExpressionStatement 满足): curToken 是表达式的最后一个
// token, peekToken 是紧随其后的 token。
func (p *Parser) checkSameLineASI() {
	if p.peekTokenIs(lexer.SEMICOLON) || p.peekTokenIs(lexer.RBRACE) || p.peekTokenIs(lexer.EOF) {
		return
	}
	if p.peekToken().Line != p.curToken().Line {
		return // 换行 ⇒ 允许 ASI
	}
	p.addError(fmt.Sprintf("missing semicolon before %s", p.peekToken().Type))
}
