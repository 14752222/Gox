package lexer

import "strings"

// JSX 词法支持。
//
// JSX 是上下文敏感语法: 同一个 `<`、`{`、`>` 在普通代码、标签内、子文本里
// 的含义完全不同。parser 在构造时预分词整个输入, 无法在解析中途驱动 lexer
// 切换模式, 因此由 lexer 自管一个上下文栈:
//
//	jsxTag   标签内部: <div attr={x} ...> 中 `<div` 之后的部分。
//	         产出标识符(含 - : 的属性名)、=、字符串、{ (进插值表达式)。
//	jsxChild 子文本区: 开标签 `>` 之后到 `</tag>` 之间。
//	         产出裸文本 JSX_TEXT、{ (插值表达式)、< (嵌套元素/闭合标签)。
//	jsxExpr  插值表达式: 标签属性或子文本中的 {...} 内部。
//	         除顶层 { } 由本层配对外, 其余 token 走普通词法。
//
// 进入 JSX 的判定 (jsxStartsHere): `<` 处于操作数位置 —— 复用正则上下文
// 判定 isRegexContext (前一个 token 不能结束一个表达式), 且后面紧跟标识符
// 起始字符。此前这类位置上的 `<` 一律是语法错误, 因此不会改变既有合法
// 程序的含义。`<>` 片段语法 v1 暂不支持。

// jsxCtxKind 是 JSX 上下文栈帧的种类。
type jsxCtxKind int

const (
	jsxTag jsxCtxKind = iota
	jsxChild
	jsxExpr
)

// jsxFrame 是 JSX 上下文栈的一帧。braceDepth 只对 jsxExpr 有意义:
// 记录插值表达式内部的 { } 嵌套深度, 归零后的 } 闭合插值并弹栈。
type jsxFrame struct {
	kind       jsxCtxKind
	braceDepth int
}

// pushJSXFrame 压入一个 JSX 上下文帧。
func (l *Lexer) pushJSXFrame(kind jsxCtxKind) {
	l.jsxStack = append(l.jsxStack, jsxFrame{kind: kind})
}

// popJSXFrame 弹出栈顶 JSX 上下文帧。
func (l *Lexer) popJSXFrame() {
	if n := len(l.jsxStack); n > 0 {
		l.jsxStack = l.jsxStack[:n-1]
	}
}

// jsxStartsHere 判断普通词法中的一个 `<` 是否开启 JSX 元素。
func (l *Lexer) jsxStartsHere() bool {
	// 只有顶层与插值表达式中可能开启; 标签内/子文本由各自分派处理。
	if n := len(l.jsxStack); n > 0 && l.jsxStack[n-1].kind != jsxExpr {
		return false
	}
	// 操作数位置判定复用正则上下文: 能出现正则字面量的位置同样只能出现 JSX。
	if !l.isRegexContext() {
		return false
	}
	// `<` 之后必须紧跟标签名首字符; <> 片段不支持。
	return isIdentifierStart(l.peekChar())
}

// beginJSXElement 消费 `<` 并进入标签上下文, 返回 JSX_LT 令牌。
func (l *Lexer) beginJSXElement(line, col int) Token {
	l.readChar() // 消费 '<'
	l.pushJSXFrame(jsxTag)
	return Token{Type: JSX_LT, Literal: "<", Line: line, Column: col}
}

// nextJSXTagToken 产出标签内部的一个令牌。
func (l *Lexer) nextJSXTagToken() Token {
	l.skipWhitespaceAndComments()
	line, col := l.line, l.column

	switch {
	case l.ch == 0:
		return Token{Type: EOF, Line: line, Column: col}
	case l.ch == '=':
		l.readChar()
		return Token{Type: ASSIGN, Literal: "=", Line: line, Column: col}
	case l.ch == '{':
		l.readChar()
		l.pushJSXFrame(jsxExpr)
		return Token{Type: LBRACE, Literal: "{", Line: line, Column: col}
	case l.ch == '"' || l.ch == '\'':
		// readString 返回时停在闭引号上 (普通路径靠 nextToken 末尾的
		// readChar 消费它), 这里必须自行消费, 否则词法错位
		tok := l.readString(l.ch, line, col)
		l.readChar()
		return tok
	case l.ch == '/':
		if l.peekChar() == '>' {
			l.readChar() // 消费 '/'
			l.readChar() // 消费 '>'
			l.popJSXFrame()
			return Token{Type: JSX_SELF_CLOSE, Literal: "/>", Line: line, Column: col}
		}
		return Token{Type: ILLEGAL, Literal: "unexpected '/' in JSX tag (expected '/>' )", Line: line, Column: col}
	case l.ch == '>':
		l.readChar()
		// 开标签结束, 进入子文本区
		l.popJSXFrame()
		l.pushJSXFrame(jsxChild)
		return Token{Type: GT, Literal: ">", Line: line, Column: col}
	case isIdentifierStart(l.ch):
		name := l.readJSXName()
		// 标签/属性名不做关键字转换, `class`、`for` 等一律按标识符处理
		return Token{Type: IDENTIFIER, Literal: name, Line: line, Column: col}
	default:
		// 消费掉非法字符, 保证词法器始终前进 (parser 会预分词到 EOF,
		// 不消费会无限产出同一令牌)
		bad := l.ch
		l.readChar()
		return Token{Type: ILLEGAL, Literal: "unexpected character '" + string(bad) + "' in JSX tag", Line: line, Column: col}
	}
}

// nextJSXChildToken 产出子文本区的一个令牌。
func (l *Lexer) nextJSXChildToken() Token {
	line, col := l.line, l.column

	switch l.ch {
	case 0:
		return Token{Type: EOF, Line: line, Column: col}
	case '{':
		l.readChar()
		l.pushJSXFrame(jsxExpr)
		return Token{Type: LBRACE, Literal: "{", Line: line, Column: col}
	case '<':
		if l.peekChar() == '/' {
			return l.scanJSXCloseTag(line, col)
		}
		l.readChar() // 消费 '<'
		l.pushJSXFrame(jsxTag)
		return Token{Type: JSX_LT, Literal: "<", Line: line, Column: col}
	}

	// 裸文本: 直到 '<' 或 '{' 为止, 内容原样保留 (空白规整交给 parser)
	var sb strings.Builder
	for l.ch != 0 && l.ch != '<' && l.ch != '{' {
		if l.ch == '\n' {
			l.line++
			l.column = 0
		}
		sb.WriteRune(l.ch)
		l.readChar()
	}
	return Token{Type: JSX_TEXT, Literal: sb.String(), Line: line, Column: col}
}

// scanJSXCloseTag 扫描闭合标签 </name>, 消费整段并弹出子文本帧。
// 进入时 l.ch 是 '<' 且下一字符是 '/'。
func (l *Lexer) scanJSXCloseTag(line, col int) Token {
	l.readChar() // 消费 '<' (l.ch == '/')
	l.readChar() // 消费 '/' (l.ch == 名字首字符)

	if !isIdentifierStart(l.ch) {
		return Token{Type: ILLEGAL, Literal: "JSX fragment </> is not supported yet", Line: line, Column: col}
	}
	name := l.readJSXName()
	l.skipWhitespaceAndComments()
	if l.ch != '>' {
		return Token{Type: ILLEGAL, Literal: "expected '>' after closing tag name </" + name, Line: line, Column: col}
	}
	l.readChar() // 消费 '>'
	l.popJSXFrame()
	return Token{Type: JSX_CLOSE, Literal: name, Line: line, Column: col}
}

// nextJSXExprBrace 处理插值表达式顶层的 { } 配对。
// 返回 ok=false 表示当前字符不是 { / }, 交给普通词法处理。
func (l *Lexer) nextJSXExprBrace() (Token, bool) {
	// 模板字面量的 ${} 插值优先: 那对花括号属于模板机制, 不能在此弹栈
	if l.inTemplate {
		return Token{}, false
	}
	line, col := l.line, l.column
	top := &l.jsxStack[len(l.jsxStack)-1]

	switch l.ch {
	case '{':
		top.braceDepth++
		l.readChar()
		return Token{Type: LBRACE, Literal: "{", Line: line, Column: col}, true
	case '}':
		l.readChar()
		if top.braceDepth > 0 {
			top.braceDepth--
			return Token{Type: RBRACE, Literal: "}", Line: line, Column: col}, true
		}
		// 闭合插值表达式, 回到标签内或子文本
		l.popJSXFrame()
		return Token{Type: RBRACE, Literal: "}", Line: line, Column: col}, true
	}
	return Token{}, false
}

// readJSXName 读取 JSX 标签名/属性名。
// 除普通标识符字符外还允许 '-' (data-id) 与 ':' (xlink:href)。
func (l *Lexer) readJSXName() string {
	start := l.position
	for isIdentifierPart(l.ch) || l.ch == '-' || l.ch == ':' {
		l.readChar()
	}
	return l.input[start:l.position]
}
