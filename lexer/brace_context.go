package lexer

// ==================== 花括号上下文追踪 (rUZN3k 第 2 块) ====================
//
// 问题: `/` 是除法还是正则, 词法层只看**前一个 token 类型** (isRegexContext)。
// 但 `}` (RBRACE) 之后的 `/` 语义取决于这个 `}` 闭合的是什么:
//   - 闭合**语句块** (BlockStatement)         → `/` 是正则开始
//       `if (x) {} /re/.test(s)`  (node 22 合法)
//   - 闭合**对象字面量 / 函数体 / 类体** (表达式) → `/` 是除法
//       `var x = function(){return 1} / {}`  (node 22 合法, 除法)
//       `({valueOf(){...}} / 1)`
// 单看 token 类型无法区分 —— 于是需要跟踪每个 `{` 到底属于哪种。
//
// 做法: 维护一个花括号**种类栈**。遇到 `{` 时, 依据当时的前一个 token
// 判定它是 block 还是 expr 并压栈; 遇到 `}` 时弹栈, 并把「刚闭合的 `}` 是
// 否为语句块」记进 l.lastRbraceIsBlock。isRegexContext 在 RBRACE 后据此
// 决定 `/` 是正则还是除法。
//
// 判定 `{` 种类 (保守, 只在**明确**语句位置才判 block):
//   block ({ 出现在语句/语句表位置):
//     - 前一个是 `;` `{` `}` (语句表分隔/收尾);
//     - 前一个是控制关键字 else / do / try / finally;
//     - 前一个是**控制头括号**的 `)` (if/while/for/switch/catch/with 的 `)`);
//     - 输入开头 (prevTokenType 为初始零值 ILLEGAL)。
//   其余一律 expr (对象字面量 / 函数体 / 类体 / 箭头体 / 属性值等)。
//
// 「控制头括号」由 parenKind 栈记录: 遇到 `(` 时看它前面是不是控制关键字,
// 是则标记该 `(` 为 control; 配对的 `)` 即「控制头 `)`」。函数/方法/箭头
// 参数的 `(` 不入 control, 于是 `function(){}` / `() => {}` 的 `{` 判 expr
// —— 与 `function(){return 1} / {}` 需要除法一致。

// braceKind 表示一个 `{` 的语义种类。
type braceKind int

const (
	braceExpr  braceKind = iota // 表达式: 对象字面量 / 函数体 / 类体 / 箭头体
	braceBlock                  // 语句块 (BlockStatement)
)

// parenKind 表示一个 `(` 的语义种类。
type parenKind int

const (
	parenExpr    parenKind = iota // 普通分组 / 调用实参 / 箭头参数
	parenControl                  // 控制结构头: if/while/for/switch/catch/with
)

// noteOpenBrace 在遇到 `{` 时压入其种类。
func (l *Lexer) noteOpenBrace() {
	l.braceStack = append(l.braceStack, l.classifyOpenBrace())
}

// classifyOpenBrace 依据当前 prevTokenType (以及 parenKind 栈) 判定 `{` 种类。
func (l *Lexer) classifyOpenBrace() braceKind {
	// 类体: `class ... {`。声明形态的类体之后 `/` 起正则 (`class C{} /re/`),
	// 表达式形态则是除法 (`var C = class {} / 2`)。判定见 noteClass。
	if l.pendingClassBody {
		l.pendingClassBody = false
		if l.pendingClassIsDecl {
			return braceBlock
		}
		return braceExpr
	}
	switch l.prevTokenType {
	case ILLEGAL: // 输入开头 (零值): 顶层 `{` 是块
		return braceBlock
	case SEMICOLON, LBRACE, RBRACE:
		// 语句表分隔/收尾位置。
		// 注意 RBRACE 后跟 `{`: 若前一个是**表达式**的 `}` (如
		// `({a:1})` 之后的 `{` 不太可能), 这里取保守 block —— 语句位置
		// 的 `{` 绝大多数是块。
		return braceBlock
	case ELSE, DO, TRY, FINALLY:
		return braceBlock
	case RPAREN:
		// 刚闭合的是控制头括号 (if/while/for/switch/catch) ⇒ 这是块。
		if l.lastRparenWasControl {
			return braceBlock
		}
		return braceExpr
	}
	return braceExpr
}

// noteClass 在遇到 class 关键字时调用: 记录「即将到来的类体是否为声明形态」。
// 声明与表达式的分野看 class 之前的 token: 若处于语句位置 (输入开头 / `;`
// / `{` / `}` / `else` / 控制头 `)` 之后等) 则为声明, 否则为表达式。
//
// 该标记在下一个 `{` (类体) 处消费 (见 classifyOpenBrace), 因此 `class C
// extends X { ... }` 里的 name / extends 表达式不会误清标记。
func (l *Lexer) noteClass() {
	l.pendingClassBody = true
	switch l.prevTokenType {
	case ILLEGAL, SEMICOLON, LBRACE, RBRACE, ELSE, DO, TRY, FINALLY:
		l.pendingClassIsDecl = true
	case RPAREN:
		l.pendingClassIsDecl = l.lastRparenWasControl
	default:
		l.pendingClassIsDecl = false
	}
}

// noteCloseParen 在遇到 `)` 时弹 parenKind 栈, 并记录刚闭合的 `)` 是否为
// 控制头括号 —— 供紧随其后的 `{` (如 `if (x) {`) 判定块种类。
func (l *Lexer) noteCloseParen() {
	if n := len(l.parenStack); n > 0 {
		l.lastRparenWasControl = l.parenStack[n-1] == parenControl
		l.parenStack = l.parenStack[:n-1]
	} else {
		l.lastRparenWasControl = false
	}
}

// noteCloseBrace 在遇到 `}` 时弹栈, 并记录刚闭合的 `}` 是否为语句块。
func (l *Lexer) noteCloseBrace() {
	if n := len(l.braceStack); n > 0 {
		l.lastRbraceIsBlock = l.braceStack[n-1] == braceBlock
		l.braceStack = l.braceStack[:n-1]
	} else {
		l.lastRbraceIsBlock = false
	}
}

// noteOpenParen 在遇到 `(` 时压入其种类 (控制头 / 普通)。
func (l *Lexer) noteOpenParen() {
	l.parenStack = append(l.parenStack, l.classifyOpenParen())
}

// classifyOpenParen 依据前一个 token 判定 `(` 是否为控制头括号。
// 单行语句位置的控制关键字: if / while / for / switch / catch / with。
// (Gox 无 with, 但保留判据以对齐规范。)
func (l *Lexer) classifyOpenParen() parenKind {
	switch l.prevTokenType {
	case IF, WHILE, FOR, SWITCH, CATCH:
		return parenControl
	}
	return parenExpr
}
