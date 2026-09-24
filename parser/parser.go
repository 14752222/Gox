package parser

import (
	"fmt"
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
	depth         int  // 当前语法嵌套深度 (表达式/语句递归层数)
	depthExceeded bool // 已触发嵌套深度上限 (后续解析短路，防错误洪水)

	// usedJSXFactory 记录"本文件出现过需要 h 的 JSX"(小写标签被降级成
	// h(...) 调用)。ParseProgram 把它交到 ast.Program.UsesJSX 上，由 compiler
	// 决定要不要补 `import { h } from "gx/gfx"` —— 见 parser/jsx.go 的约定说明。
	usedJSXFactory bool
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
	p.registerPrefix(lexer.ASYNC, p.parseAsyncExpression)
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

// isBlockStart 判断当前 LBRACE 是否开始块语句 (而非对象字面量)。
// 当 { 后跟 } (空块) 或语句关键字时，判定为块语句。
func (p *Parser) isBlockStart() bool {
	if p.peekTokenIs(lexer.RBRACE) {
		return true // 空块 {}
	}
	switch p.peekToken().Type {
	case lexer.LET, lexer.CONST, lexer.RETURN, lexer.IF, lexer.FOR,
		lexer.WHILE, lexer.BREAK, lexer.CONTINUE, lexer.FUNCTION,
		lexer.TRY, lexer.THROW, lexer.SWITCH,
		lexer.SEMICOLON, lexer.DO, lexer.CASE, lexer.DEFAULT:
		return true
	}
	// 若 peek 是标识符且其后是赋值号/自增/自减 (如 { x = 2; }, { x++; }),
	// 则是块而非对象字面量 (对象属性是 x: ... 而非 x = ...)
	if p.peekTokenIs(lexer.IDENTIFIER) {
		switch p.peekTokenAt(2).Type {
		case lexer.ASSIGN, lexer.PLUS_EQ, lexer.MINUS_EQ, lexer.ASTERISK_EQ,
			lexer.SLASH_EQ, lexer.PERCENT_EQ, lexer.EXPONENT_EQ, lexer.AND_EQ,
			lexer.OR_EQ, lexer.XOR_EQ, lexer.SHIFT_LEFT_EQ, lexer.SHIFT_RIGHT_EQ,
			lexer.UNSIGNED_SHR_EQ, lexer.AND_AND_EQ, lexer.OR_OR_EQ,
			lexer.NULLISH_ASSIGN, lexer.INC, lexer.DEC:
			return true
		}
	}
	return false
}

func (p *Parser) Errors() *ErrorList { return p.errors }

// ==================== 主解析入口 ====================

func (p *Parser) ParseProgram() *ast.Program {
	program := &ast.Program{}
	program.Statements = []ast.Statement{}

	for !p.curTokenIs(lexer.EOF) {
		stmt := p.parseStatement()
		if stmt != nil {
			program.Statements = append(program.Statements, stmt)
		}
		p.nextToken()
	}
	// 用了小写标签的 JSX 就要有 h: 带上标记, 交给 compiler 补缺省工厂导入
	program.UsesJSX = p.usedJSXFactory
	return program
}

// ==================== 语句解析 ====================

func (p *Parser) parseStatement() ast.Statement {
	switch p.curToken().Type {
	case lexer.LET:
		return p.parseLetStatement()
	case lexer.CONST:
		return p.parseConstStatement()
	case lexer.VAR:
		// **有意不支持的边界, 不是漏做** (2026-09-24 评估后维持; 与官网
		// api.html / guide.html 的「语言边界」表、README 的口径一致)。
		//
		// 理由: var 的函数级作用域 + 变量提升 + 允许重复声明, 与 let/const 的
		// 块级作用域是两套语义。真做得在编译器里另加一套作用域解析; 而"当 let
		// 用"是最省事也最坏的选项 —— 没有提升/重复声明的写法下看起来完全正常,
		// 只在用到那两条语义时才露馅, 属于本仓库一直在剿的**静默语义偏差**。
		// 所以这里给的是编译期明确报错, 并且文案本身就是行动指引。
		//
		// 词法层仍识别 VAR, 以支持 var 作为属性名 (obj.var, {var: 1})。
		p.addError("var is not supported, use let or const instead")
		return nil
	case lexer.RETURN:
		return p.parseReturnStatement()
	case lexer.IF:
		return p.parseIfStatement()
	case lexer.FOR:
		return p.parseForStatement()
	case lexer.WHILE:
		return p.parseWhileStatement()
	case lexer.DO:
		return p.parseDoWhileStatement()
	case lexer.BREAK:
		return p.parseBreakStatement()
	case lexer.CONTINUE:
		return p.parseContinueStatement()
	case lexer.FUNCTION:
		// function name / function* name → 函数声明
		if p.peekTokenIs(lexer.IDENTIFIER) || p.peekTokenIs(lexer.ASTERISK) {
			return p.parseFunctionDeclaration()
		}
		stmt := &ast.ExpressionStatement{Token: p.curToken()}
		stmt.Expression = p.parseExpression(LOWEST)
		p.consumeSemicolon()
		return stmt
	case lexer.ASYNC:
		// async function 声明 / async function 表达式
		if p.peekTokenIs(lexer.FUNCTION) {
			p.nextToken() // 移到 function
			fn := p.parseFunctionDeclaration()
			if fn != nil {
				fn.IsAsync = true
			}
			return fn
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
		return p.parseImportDeclaration()
	case lexer.EXPORT:
		return p.parseExportDeclaration()
	case lexer.SEMICOLON:
		return &ast.ExpressionStatement{Token: p.curToken()}
	case lexer.CLASS:
		return p.parseClassDeclaration()
	case lexer.IDENTIFIER:
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

func (p *Parser) parseLetStatement() *ast.LetStatement {
	stmt := &ast.LetStatement{Token: p.curToken()}
	p.nextToken()

	if p.curTokenIs(lexer.LBRACKET) {
		return p.parseDestructuringLet(stmt, true)
	}
	if p.curTokenIs(lexer.LBRACE) {
		return p.parseDestructuringLet(stmt, false)
	}

	if !p.curTokenIs(lexer.IDENTIFIER) {
		p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
		return nil
	}
	stmt.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}

	if p.peekTokenIs(lexer.ASSIGN) {
		p.nextToken()
		p.nextToken()
		stmt.Value = p.parseExpression(LOWEST)
	}

	// 多条声明: let a = 1, b = 2;
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // 移到 ,
		p.nextToken() // 移到下一个名字
		if !p.curTokenIs(lexer.IDENTIFIER) {
			p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
			return nil
		}
		decl := ast.Declarator{Name: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}}
		if p.peekTokenIs(lexer.ASSIGN) {
			p.nextToken()
			p.nextToken()
			decl.Value = p.parseExpression(LOWEST)
		}
		stmt.More = append(stmt.More, decl)
	}
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseDestructuringLet(stmt *ast.LetStatement, isArray bool) *ast.LetStatement {
	pattern := p.parseDestructuringPattern(isArray)
	if pattern == nil {
		return nil
	}
	if !p.peekTokenIs(lexer.ASSIGN) {
		p.addError(fmt.Sprintf("expected = after destructuring, got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken()
	p.nextToken()
	stmt.Name = &ast.Identifier{Token: stmt.Token, Value: "__destructure__"}
	stmt.Value = &ast.AssignmentExpression{
		Token: stmt.Token, Left: pattern, Operator: "=", Right: p.parseExpression(LOWEST),
	}
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseConstStatement() *ast.ConstStatement {
	stmt := &ast.ConstStatement{Token: p.curToken()}
	p.nextToken()

	if p.curTokenIs(lexer.LBRACKET) || p.curTokenIs(lexer.LBRACE) {
		letStmt := &ast.LetStatement{Token: stmt.Token}
		letStmt = p.parseDestructuringLet(letStmt, p.curTokenIs(lexer.LBRACKET))
		if letStmt == nil {
			return nil
		}
		stmt.Name = letStmt.Name
		stmt.Value = letStmt.Value
		// 此处**不能**再调一次 consumeSemicolon: parseDestructuringLet 末尾已经调过。
		// 而 consumeSemicolon 只在 peek 是 ';' 时才前进 (见文件末尾的定义), 于是
		// 第二次调用时 cur 正停在那一个 ';' 上, 一旦**紧跟另一个 ';'** 就会多走一步。
		// 典型写法就是传统 for 的头部 `for (const [a] = [1];;)`: 空 condition 被
		// 跳过一格, ')' 落进 parseExpression, 报 "no prefix parse function for
		// RPAREN found" —— 错在"少写了个分号"上, 而写法本身没问题; 若是空 update
		// 那一支 (`for (const {a} = {a: 1};; n = n + 1)`) 更糟: 不报错, 而是把
		// update 表达式静默吃成 condition。
		return stmt
	}

	if !p.curTokenIs(lexer.IDENTIFIER) {
		p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
		return nil
	}
	stmt.Name = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}

	if !p.peekTokenIs(lexer.ASSIGN) {
		p.addError("const declaration must have an initializer")
		return nil
	}
	p.nextToken()
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)

	// 多条声明: const a = 1, b = 2;
	for p.peekTokenIs(lexer.COMMA) {
		p.nextToken() // 移到 ,
		p.nextToken() // 移到下一个名字
		if !p.curTokenIs(lexer.IDENTIFIER) {
			p.addError(fmt.Sprintf("expected identifier, got %s", p.curToken().Type))
			return nil
		}
		decl := ast.Declarator{Name: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}}
		if !p.peekTokenIs(lexer.ASSIGN) {
			p.addError("const declaration must have an initializer")
			return nil
		}
		p.nextToken()
		p.nextToken()
		decl.Value = p.parseExpression(LOWEST)
		stmt.More = append(stmt.More, decl)
	}
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseReturnStatement() *ast.ReturnStatement {
	stmt := &ast.ReturnStatement{Token: p.curToken()}

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
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseExpressionStatement() *ast.ExpressionStatement {
	stmt := &ast.ExpressionStatement{Token: p.curToken()}
	stmt.Expression = p.parseCommaSequence()
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
	if !p.expectPeek(lexer.LPAREN) {
		return nil
	}
	p.nextToken()

	// for...of / for...in 的头部: let/const 后跟**绑定**, 绑定之后是 of / in。
	// 绑定可以是标识符, 也可以是解构模式 —— 后者要跳过配对的 ]/} 才看得到关键字,
	// 所以判定统一交给 forBindingKeyword。
	if p.curTokenIs(lexer.LET) || p.curTokenIs(lexer.CONST) {
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
	return p.parseTraditionalFor()
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
	if peek.Type == lexer.IDENTIFIER {
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
	p.nextToken() // skip let/const

	if !p.curTokenIs(lexer.IDENTIFIER) {
		p.addError("expected variable name in for...in")
		return nil
	}
	variable := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	stmt.Variable = variable

	if isLet {
		stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: variable}
	} else {
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
	p.nextToken() // skip let/const

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
		if isLet {
			stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: name}
		} else {
			stmt.VarDecl = &ast.ConstStatement{Token: stmt.Token, Name: name}
		}
	} else {
		if !p.curTokenIs(lexer.IDENTIFIER) {
			p.addError(fmt.Sprintf(
				"expected variable name or destructuring pattern in for...of, got %s",
				p.curToken().Type))
			return nil
		}
		variable := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		stmt.Variable = variable

		if isLet {
			stmt.VarDecl = &ast.LetStatement{Token: stmt.Token, Name: variable}
		} else {
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
		// for (var ...) 与 var 声明同样被拒绝
		p.addError("var is not supported, use let or const instead")
		return nil
	} else if !p.curTokenIs(lexer.SEMICOLON) {
		expr := p.parseCommaSequence()
		stmt.Init = &ast.ExpressionStatement{Token: p.curToken(), Expression: expr}
		p.consumeSemicolon()
		// 同上: curToken 停在 ';' 上, 前进一步到 condition 开头。
		p.nextToken()
	} else {
		p.nextToken() // 空 init: 越过第一个 ';' 到 condition 开头 (或第二个 ';')
	}

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

func (p *Parser) parseBreakStatement() *ast.BreakStatement {
	stmt := &ast.BreakStatement{Token: p.curToken()}
	// 可选标签: break outer;
	// ASI 规则: 标签必须与 break 同一行, 换行后的标识符是下一条语句,
	// 不能当作标签 (否则 break\narr.push(...) 会被误解析为 break arr)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Line == p.curToken().Line {
		p.nextToken()
		stmt.Label = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}
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
	p.consumeSemicolon()
	return stmt
}

// parseLabeledStatement 解析标签语句: label: statement
func (p *Parser) parseLabeledStatement() *ast.LabeledStatement {
	stmt := &ast.LabeledStatement{
		Token: p.curToken(),
		Label: &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal},
	}
	p.nextToken() // 跳过 label 标识符
	p.nextToken() // 跳过 ':'
	// 标签后的 '{' 一定是块 (语句位置), 而非对象字面量
	if p.curTokenIs(lexer.LBRACE) {
		stmt.Body = p.parseBlockStatement()
	} else {
		stmt.Body = p.parseStatement()
	}
	return stmt
}

func (p *Parser) parseBlockStatement() *ast.BlockStatement {
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

	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		stmt := p.parseStatement()
		if stmt != nil {
			block.Statements = append(block.Statements, stmt)
		}
		p.nextToken()
	}
	return block
}

// parseBody 解析语句体: 若当前是 { 则解析代码块, 否则解析单条语句并包装为块。
// 用于支持 if (x) y++; 这类不带花括号的单语句体。
func (p *Parser) parseBody() *ast.BlockStatement {
	if p.curTokenIs(lexer.LBRACE) {
		return p.parseBlockStatement()
	}
	// 单条语句: 包装为 BlockStatement
	block := &ast.BlockStatement{Token: p.curToken(), Statements: []ast.Statement{}}
	stmt := p.parseStatement()
	if stmt != nil {
		block.Statements = append(block.Statements, stmt)
	}
	// 单语句后不自动消费分号外的 token (由调用方处理)
	return block
}

func (p *Parser) parseFunctionDeclaration() *ast.FunctionDeclaration {
	// 进入时 curToken 是 FUNCTION (async 分支已把 curToken 移到 FUNCTION)
	fn := &ast.FunctionDeclaration{Token: p.curToken()}
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
	fn.Parameters = p.parseParameters(lexer.RPAREN)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	fn.Body = p.parseBlockStatement()
	return fn
}

// ==================== 参数解析 ====================

func (p *Parser) parseParameters(close lexer.TokenType) []*ast.Parameter {
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
		return param
	}

	if !p.curTokenIs(lexer.IDENTIFIER) {
		p.addError(fmt.Sprintf("expected parameter name, got %s", p.curToken().Type))
		return nil
	}
	param.Name = p.curToken().Literal

	if p.peekTokenIs(lexer.ASSIGN) {
		p.nextToken()
		p.nextToken()
		param.Default = p.parseExpression(LOWEST)
	}
	return param
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

	for !p.peekTokenIs(lexer.SEMICOLON) && !p.peekTokenIs(lexer.RBRACE) && !p.peekTokenIs(lexer.EOF) &&
		!p.peekTokenIs(lexer.RPAREN) && !p.peekTokenIs(lexer.RBRACKET) && !p.peekTokenIs(lexer.COMMA) &&
		!p.peekTokenIs(lexer.COLON) && !p.peekTokenIs(lexer.ARROW) &&
		(precedence < getPrecedence(p.peekToken().Literal) || p.peekTokenIs(lexer.BACKTICK)) {
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
		p.nextToken() // consume =>
		return p.parseArrowFunctionBody([]*ast.Parameter{{
			Token: ident.Token, Name: ident.Value,
		}})
	}
	return &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
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
	// Literal 格式: "pattern|flags"
	literal := p.curToken().Literal
	parts := strings.SplitN(literal, "|", 2)
	pattern := parts[0]
	flags := ""
	if len(parts) > 1 {
		flags = parts[1]
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
	return expr
}

// parseGroupedOrArrow 解析括号表达式或箭头函数。
// 通过扫描到匹配的 ) 后检查是否跟 => 来区分。
func (p *Parser) parseGroupedOrArrow() ast.Expression {
	if p.isArrowFunction() {
		return p.parseArrowFunction()
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
			}})
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

func (p *Parser) parseArrowFunction() ast.Expression {
	// curToken = LPAREN, parseParameters will advance past it
	params := p.parseParameters(lexer.RPAREN)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}

	if !p.peekTokenIs(lexer.ARROW) {
		p.addError(fmt.Sprintf("expected '=>', got %s", p.peekToken().Type))
		return nil
	}
	p.nextToken() // consume =>
	return p.parseArrowFunctionBody(params)
}

func (p *Parser) parseArrowFunctionBody(params []*ast.Parameter) *ast.ArrowFunctionExpression {
	af := &ast.ArrowFunctionExpression{Token: p.curToken(), Parameters: params}

	if p.peekTokenIs(lexer.LBRACE) {
		p.nextToken()
		af.Body = p.parseBlockStatement()
	} else {
		p.nextToken()
		af.Body = p.parseExpression(LOWEST)
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
	fn.Parameters = p.parseParameters(lexer.RPAREN)
	if !p.curTokenIs(lexer.RPAREN) {
		return nil
	}
	p.nextToken()
	fn.Body = p.parseBlockStatement()
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
		fn.Parameters = p.parseParameters(lexer.RPAREN)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		fn.Body = p.parseBlockStatement()
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
		return p.finishAsyncArrow(p.parseArrowFunction())
	}

	// async x => … (单参数不带括号)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peek2TokenIs(lexer.ARROW) {
		p.nextToken() // cur = 标识符
		ident := &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		p.nextToken() // cur = =>
		return p.finishAsyncArrow(p.parseArrowFunctionBody([]*ast.Parameter{{
			Token: ident.Token, Name: ident.Value,
		}}))
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

// parseYieldExpression 解析 yield 表达式。
// yield; 或 yield expr;
func (p *Parser) parseYieldExpression() ast.Expression {
	ye := &ast.YieldExpression{Token: p.curToken()}
	p.nextToken()
	// yield* iterable: 委托给另一个生成器/可迭代对象
	if p.curTokenIs(lexer.ASTERISK) {
		ye.Delegate = true
		p.nextToken()
	}
	// 空 yield (后跟分号/换行): 值为 undefined
	if p.curTokenIs(lexer.SEMICOLON) || p.curTokenIs(lexer.RPAREN) ||
		p.curTokenIs(lexer.RBRACE) || p.curTokenIs(lexer.EOF) {
		return ye
	}
	ye.Value = p.parseExpression(LOWEST)
	return ye
}

// parseAwaitExpression 解析 await 表达式。
func (p *Parser) parseAwaitExpression() ast.Expression {
	ae := &ast.AwaitExpression{Token: p.curToken()}
	p.nextToken()
	ae.Argument = p.parseExpression(LOWEST)
	return ae
}

func (p *Parser) parseNewExpression() ast.Expression {
	expr := &ast.NewExpression{Token: p.curToken()}
	p.nextToken()
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
			p.nextToken()
			p.nextToken()
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
		if p.curTokenIs(lexer.SPREAD_REST) {
			// 对象展开 {...a}
			p.nextToken()
			obj.Spread = append(obj.Spread, p.parseExpression(LOWEST))
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

	// getter/setter: get name() {} / set name(v) {}
	// 仅在 "get"/"set" 后紧跟标识符且再后是 '(' 时识别为访问器,
	// 避免与 { get: 1 }, { get() {} }, { get } 混淆。
	if (p.curTokenIs(lexer.IDENTIFIER) &&
		(p.curToken().Literal == "get" || p.curToken().Literal == "set")) &&
		p.peekTokenIs(lexer.IDENTIFIER) && p.peek2TokenIs(lexer.LPAREN) {
		if p.curToken().Literal == "get" {
			prop.Kind = ast.PROP_GETTER
		} else {
			prop.Kind = ast.PROP_SETTER
		}
		p.nextToken() // 到属性名
		prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
		p.nextToken() // 到 (
		fn := &ast.FunctionExpression{Token: prop.Token}
		fn.Parameters = p.parseParameters(lexer.RPAREN)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken() // 到 {
		fn.Body = p.parseBlockStatement()
		prop.Value = fn
		return prop
	}

	if p.curTokenIs(lexer.LBRACKET) {
		// 计算属性: [expr]: value
		prop.Computed = true
		p.nextToken()
		prop.Key = p.parseExpression(LOWEST)
		if !p.peekTokenIs(lexer.RBRACKET) {
			p.addError(fmt.Sprintf("expected ']' in computed property, got %s", p.peekToken().Type))
			return nil
		}
		p.nextToken() // consume ]
	} else {
		prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	}

	// 简写: { name }
	if p.peekTokenIs(lexer.COMMA) || p.peekTokenIs(lexer.RBRACE) {
		prop.Shorthand = true
		id, ok := prop.Key.(*ast.Identifier)
		if !ok {
			p.addError("shorthand property must be identifier")
			return nil
		}
		prop.Value = id
		return prop
	}

	// 不是简写，推进到 key 之后的 token
	p.nextToken()

	// 方法定义: method() {}
	if p.curTokenIs(lexer.LPAREN) {
		prop.Kind = ast.PROP_METHOD
		fn := &ast.FunctionExpression{Token: prop.Token}
		fn.Parameters = p.parseParameters(lexer.RPAREN)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		fn.Body = p.parseBlockStatement()
		prop.Value = fn
		return prop
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

func (p *Parser) parseAssignmentExpression(left ast.Expression) ast.Expression {
	// 解构赋值目标: [a, b] = v / ({ x } = v)。
	// 目标先按数组/对象字面量解析 (cover grammar)，确认是简单赋值后
	// 转换为解构模式；此前编译器不认识字面量目标，静默不发射指令导致栈失衡。
	if p.curTokenIs(lexer.ASSIGN) {
		switch left.(type) {
		case *ast.ArrayLiteral, *ast.ObjectLiteral:
			left = p.literalToPattern(left)
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
		for _, el := range lit.Elements {
			switch e := el.(type) {
			case *ast.Identifier:
				pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: e})
			case *ast.SpreadElement: // [a, ...rest]
				if id, ok := e.Argument.(*ast.Identifier); ok {
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: id, Rest: true})
					continue
				}
				p.addError("invalid rest target in destructuring assignment")
				return expr
			case *ast.AssignmentExpression: // [a = 默认值]
				if id, ok := e.Left.(*ast.Identifier); ok {
					pattern.Elements = append(pattern.Elements, &ast.PatternElement{Token: e.Token, Target: id, Default: e.Right})
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
		if len(lit.Spread) > 0 {
			p.addError("object rest in destructuring assignment is not supported")
			return expr
		}
		pattern := &ast.ObjectPattern{Token: lit.Token, Properties: []*ast.PatternProperty{}}
		for _, prop := range lit.Properties {
			pp := &ast.PatternProperty{Token: prop.Token, Key: prop.Key, Shorthand: prop.Shorthand}
			switch v := prop.Value.(type) {
			case *ast.Identifier:
				pp.Value = v
			case *ast.AssignmentExpression: // { x = 默认值 }
				id, ok := v.Left.(*ast.Identifier)
				if !ok {
					p.addError("invalid destructuring assignment target")
					return expr
				}
				pp.Value, pp.Default = id, v.Right
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
	// 属性名可以是标识符或关键字 (如 obj.default, obj.class)
	if !p.curTokenIs(lexer.IDENTIFIER) && !isKeywordProperty(p.curToken().Type) {
		p.addError(fmt.Sprintf("expected property name, got %s", p.curToken().Type))
		return nil
	}
	mexp.Property = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
	mexp.Computed = false
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
func isKeywordProperty(t lexer.TokenType) bool {
	switch t {
	case lexer.LET, lexer.CONST, lexer.VAR, lexer.IF, lexer.ELSE, lexer.FOR, lexer.OF, lexer.WHILE,
		lexer.BREAK, lexer.CONTINUE, lexer.FUNCTION, lexer.RETURN,
		lexer.UNDEFINED, lexer.TYPEOF, lexer.INSTANCEOF, lexer.NEW, lexer.THIS,
		lexer.DELETE, lexer.IN, lexer.TRY, lexer.CATCH, lexer.FINALLY, lexer.THROW,
		lexer.SWITCH, lexer.CASE, lexer.DEFAULT, lexer.CLASS, lexer.SUPER,
		lexer.IMPORT, lexer.EXPORT, lexer.YIELD, lexer.AWAIT,
		lexer.TRUE, lexer.FALSE, lexer.NULL:
		return true
	}
	return false
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
	if isArray {
		return p.parseArrayPattern()
	}
	return p.parseObjectPattern()
}

func (p *Parser) parseArrayPattern() *ast.ArrayPattern {
	pattern := &ast.ArrayPattern{Token: p.curToken()}
	pattern.Elements = []*ast.PatternElement{}
	if !p.curTokenIs(lexer.LBRACKET) {
		p.addError("expected '[' in array pattern")
		return nil
	}
	p.nextToken()
	if p.curTokenIs(lexer.RBRACKET) {
		p.nextToken()
		return pattern
	}
	for !p.curTokenIs(lexer.RBRACKET) && !p.curTokenIs(lexer.EOF) {
		elem := &ast.PatternElement{Token: p.curToken()}
		if p.curTokenIs(lexer.SPREAD_REST) {
			elem.Rest = true
			p.nextToken()
		}
		if p.curTokenIs(lexer.IDENTIFIER) {
			elem.Target = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken()
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
			p.nextToken()
			elem.Default = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符 (逗号或右括号)
		}
		pattern.Elements = append(pattern.Elements, elem)
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
	if p.curTokenIs(lexer.RBRACE) {
		p.nextToken()
		return pattern
	}
	for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
		prop := &ast.PatternProperty{Token: p.curToken()}
		// 键与对象字面量同理: 标识符、字符串、或可作属性名的关键字 (如 { var: x })
		if p.curTokenIs(lexer.IDENTIFIER) || p.curTokenIs(lexer.STRING_LITERAL) || isKeywordProperty(p.curToken().Type) {
			prop.Key = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
			p.nextToken()
		} else {
			p.addError(fmt.Sprintf("unexpected token: %s", p.curToken().Type))
			return nil
		}
		if p.curTokenIs(lexer.COMMA) || p.curTokenIs(lexer.RBRACE) {
			prop.Shorthand = true
			id, _ := prop.Key.(*ast.Identifier)
			prop.Value = id
		} else if p.curTokenIs(lexer.COLON) {
			p.nextToken()
			if p.curTokenIs(lexer.IDENTIFIER) {
				prop.Value = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
				p.nextToken()
				// 别名 + 默认值: { key: val = default }
				if p.curTokenIs(lexer.ASSIGN) {
					p.nextToken()
					prop.Default = p.parseExpression(LOWEST)
					p.nextToken() // 前进到分隔符
				}
			} else if p.curTokenIs(lexer.LBRACKET) {
				prop.Value = p.parseArrayPattern()
				p.nextToken() // 越过内层 ']' (见 parseArrayPattern 里的同一说明)
			} else if p.curTokenIs(lexer.LBRACE) {
				prop.Value = p.parseObjectPattern()
				p.nextToken() // 越过内层 '}'
			}
		} else if p.curTokenIs(lexer.ASSIGN) {
			prop.Shorthand = true
			id, _ := prop.Key.(*ast.Identifier)
			prop.Value = id
			p.nextToken()
			prop.Default = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符 (逗号或右花括号)
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

// ==================== throw / try-catch / switch / import-export 解析 ====================

func (p *Parser) parseThrowStatement() *ast.ThrowStatement {
	stmt := &ast.ThrowStatement{Token: p.curToken()}
	p.nextToken()
	stmt.Value = p.parseExpression(LOWEST)
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
			if !p.peekTokenIs(lexer.IDENTIFIER) {
				p.addError("expected identifier in catch clause")
				return nil
			}
			p.nextToken()
			stmt.CatchParam = &ast.Identifier{Token: p.curToken(), Value: p.curToken().Literal}
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
		for !p.curTokenIs(lexer.CASE) && !p.curTokenIs(lexer.DEFAULT) && !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
			s := p.parseStatement()
			if s != nil {
				sc.Statements = append(sc.Statements, s)
			}
			p.nextToken()
		}
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

	// 可选 extends 子句 (extends 作为标识符处理, 无专用 token)
	if p.peekTokenIs(lexer.IDENTIFIER) && p.peekToken().Literal == "extends" {
		p.nextToken()
		p.nextToken()
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
	p.nextToken() // 进入成员区域

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
			field := &ast.ClassField{Token: member.Token, Name: member.Name, Value: member.FieldValue}
			cls.Fields = append(cls.Fields, field)
		}
		// 跳过成员间的分隔符 (分号)
		for p.curTokenIs(lexer.SEMICOLON) {
			p.nextToken()
		}
	}

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

	// static 关键字
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "static" &&
		!p.peekTokenIs(lexer.LPAREN) && !p.peekTokenIs(lexer.ASSIGN) {
		member.IsStatic = true
		p.nextToken()
	}

	// get/set 访问器: get name() {} / set name(v) {}
	if p.curTokenIs(lexer.IDENTIFIER) &&
		(p.curToken().Literal == "get" || p.curToken().Literal == "set") &&
		p.peekTokenIs(lexer.IDENTIFIER) && p.peek2TokenIs(lexer.LPAREN) {
		if p.curToken().Literal == "get" {
			member.IsGetter = true
		} else {
			member.IsSetter = true
		}
		p.nextToken()
		member.Name = p.curToken().Literal
		p.nextToken()
		member.Parameters = p.parseParameters(lexer.RPAREN)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		member.Body = p.parseBlockStatement()
		p.nextToken() // 前进到下一个成员/分隔符
		return member
	}

	// constructor 特殊处理 (标识符 constructor 后跟 '(')
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "constructor" && p.peekTokenIs(lexer.LPAREN) {
		member.Name = "constructor"
		p.nextToken()
		member.Parameters = p.parseParameters(lexer.RPAREN)
		if !p.curTokenIs(lexer.RPAREN) {
			return nil
		}
		p.nextToken()
		member.Body = p.parseBlockStatement()
		p.nextToken() // 前进到下一个成员/分隔符
		return member
	}

	// 方法或字段
	if p.curTokenIs(lexer.IDENTIFIER) {
		member.Name = p.curToken().Literal
		p.nextToken()
		if p.curTokenIs(lexer.LPAREN) {
			// 方法定义: name(params) { body }
			member.Parameters = p.parseParameters(lexer.RPAREN)
			if !p.curTokenIs(lexer.RPAREN) {
				return nil
			}
			p.nextToken()
			member.Body = p.parseBlockStatement()
			p.nextToken() // 前进到下一个成员/分隔符
			return member
		}
		if p.curTokenIs(lexer.ASSIGN) {
			// 实例字段: name = expr
			p.nextToken()
			member.FieldValue = p.parseExpression(LOWEST)
			p.nextToken() // 前进到分隔符/下一个成员
			return member
		}
		// 裸字段: name; (无初始化)
		p.nextToken() // 前进到分隔符/下一个成员
		return member
	}

	p.addError(fmt.Sprintf("unexpected token in class body: %s", p.curToken().Type))
	return nil
}

// parseSuperExpression 解析 super 关键字。
// super(...) → 父构造函数调用; super.method / super.prop → 父类成员访问。
func (p *Parser) parseSuperExpression() ast.Expression {
	super := &ast.SuperExpression{Token: p.curToken()}

	// super(...) 或 super.method
	if p.peekTokenIs(lexer.LPAREN) || p.peekTokenIs(lexer.DOT) {
		// 返回 SuperExpression 本身, 由中缀解析继续处理 (LPAREN → 调用, DOT → 成员)
		return super
	}
	p.addError("super must be followed by '(' or '.'")
	return nil
}

func (p *Parser) parseImportDeclaration() *ast.ImportDeclaration {
	stmt := &ast.ImportDeclaration{Token: p.curToken()}
	p.nextToken()

	// import "module.js" (副作用导入)
	if p.curTokenIs(lexer.STRING_LITERAL) {
		stmt.Source = p.curToken().Literal
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
		if !p.curTokenIs(lexer.IDENTIFIER) || p.curToken().Literal != "as" {
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
		// import { a, b } from "..."
		p.nextToken()
		for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
			if p.curTokenIs(lexer.IDENTIFIER) {
				stmt.NamedImports = append(stmt.NamedImports, p.curToken().Literal)
			}
			p.nextToken()
			if p.curTokenIs(lexer.COMMA) {
				p.nextToken()
			}
		}
		if !p.curTokenIs(lexer.RBRACE) {
			p.addError("expected '}' in import")
			return nil
		}
		p.nextToken()
	}

	// from
	if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "from" {
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
	p.consumeSemicolon()
	return stmt
}

func (p *Parser) parseExportDeclaration() *ast.ExportDeclaration {
	stmt := &ast.ExportDeclaration{Token: p.curToken()}
	p.nextToken()

	// export default ...
	if p.curTokenIs(lexer.DEFAULT) {
		stmt.IsDefault = true
		p.nextToken()
		// default 可以导出表达式或声明
		if p.curTokenIs(lexer.FUNCTION) {
			fn := p.parseFunctionDeclaration()
			stmt.Declaration = fn
		} else {
			expr := p.parseExpression(LOWEST)
			stmt.Declaration = &ast.ExpressionStatement{Token: p.curToken(), Expression: expr}
			p.consumeSemicolon()
		}
		return stmt
	}

	// export { a, b }
	if p.curTokenIs(lexer.LBRACE) {
		p.nextToken()
		for !p.curTokenIs(lexer.RBRACE) && !p.curTokenIs(lexer.EOF) {
			if p.curTokenIs(lexer.IDENTIFIER) {
				stmt.NamedExports = append(stmt.NamedExports, p.curToken().Literal)
			}
			p.nextToken()
			if p.curTokenIs(lexer.COMMA) {
				p.nextToken()
			}
		}
		if !p.curTokenIs(lexer.RBRACE) {
			p.addError("expected '}' in export")
			return nil
		}
		p.nextToken()
		// optional from "..."
		if p.curTokenIs(lexer.IDENTIFIER) && p.curToken().Literal == "from" {
			p.nextToken()
			if !p.curTokenIs(lexer.STRING_LITERAL) {
				p.addError("expected module path in export from")
				return nil
			}
			// re-export: 我们简单处理，直接忽略来源模块
		}
		p.consumeSemicolon()
		return stmt
	}

	// export const/let/function
	if p.curTokenIs(lexer.LET) || p.curTokenIs(lexer.CONST) || p.curTokenIs(lexer.FUNCTION) {
		var decl ast.Statement
		switch p.curToken().Type {
		case lexer.LET:
			decl = p.parseLetStatement()
		case lexer.CONST:
			decl = p.parseConstStatement()
		case lexer.FUNCTION:
			decl = p.parseFunctionDeclaration()
		}
		stmt.Declaration = decl
		return stmt
	}

	p.addError(fmt.Sprintf("unexpected token after export: %s", p.curToken().Type))
	return nil
}

// ==================== 辅助方法 ====================

func (p *Parser) consumeSemicolon() {
	if p.peekTokenIs(lexer.SEMICOLON) {
		p.nextToken()
	}
}
