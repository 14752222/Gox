package lexer

import (
	"fmt"
	"strings"
	"unicode"
)

// Lexer 将 JavaScript 源代码字符流转换为 Token 序列。
// 这是一个手写的词法分析器，逐字符扫描源码。
type Lexer struct {
	input        string  // 源代码
	position     int     // 当前字符位置 (指向当前字符)
	readPosition int     // 下一个字符位置 (指向下一个待读字符)
	ch           rune    // 当前字符 (unicode 码点)
	line         int     // 当前行号
	column       int     // 当前列号

	// 模板字面量状态管理
	// 当处理模板字面量 `...${expr}...` 时，lexer 需要在字符串字面量和
	// JavaScript 表达式之间切换。templateDepth 记录嵌套的模板层数 (支持嵌套模板)。
	// templateBraceDepth 记录当前插值 ${...} 内的花括号嵌套深度。
	inTemplate         bool
	templateDepth      int
	templateBraceDepth int

	// 正则字面量上下文: 追踪前一个 token 类型以区分 / 是除法还是正则开始
	prevTokenType TokenType
}

// New 创建一个新的 Lexer 实例。
func New(input string) *Lexer {
	l := &Lexer{
		input:  input,
		line:   1,
		column: 0,
	}
	l.readChar() // 预读第一个字符
	return l
}

// readChar 读取下一个字符，更新 position 和 readPosition。
// 使用 rune 类型正确处理 Unicode 字符。
func (l *Lexer) readChar() {
	if l.readPosition >= len(l.input) {
		l.ch = 0 // EOF 标记 (NUL 字符)
	} else {
		l.ch = rune(l.input[l.readPosition])
	}
	l.position = l.readPosition
	l.readPosition++
	l.column++
}

// peekChar 查看下一个字符但不移动位置 (前瞻)。
func (l *Lexer) peekChar() rune {
	if l.readPosition >= len(l.input) {
		return 0
	}
	return rune(l.input[l.readPosition])
}

// peekCharAt 查看 offset 偏移处的字符 (不移动位置)。
func (l *Lexer) peekCharAt(offset int) rune {
	pos := l.readPosition + offset - 1
	if pos >= len(l.input) {
		return 0
	}
	return rune(l.input[pos])
}

// NextToken 返回输入中的下一个 Token。
// 这是词法分析器的主循环，每次调用返回一个 Token。
// 包装层统一更新 prevTokenType，确保所有返回路径（包括提前返回的
// 数字/字符串/模板/正则路径）都正确追踪前一个 token，以便区分
// 除法 / 与正则字面量 / 的上下文。
func (l *Lexer) NextToken() Token {
	tok := l.nextToken()
	l.prevTokenType = tok.Type
	return tok
}

func (l *Lexer) nextToken() Token {
	l.skipWhitespaceAndComments()

	// 记录 Token 起始位置
	line := l.line
	col := l.column

	var tok Token

	// 根据当前字符分派
	switch l.ch {
	case '=':
		if l.peekChar() == '=' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: STRICT_EQ, Literal: "===", Line: line, Column: col}
			} else {
				tok = Token{Type: EQ, Literal: "==", Line: line, Column: col}
			}
		} else if l.peekChar() == '>' {
			l.readChar()
			tok = Token{Type: ARROW, Literal: "=>", Line: line, Column: col}
		} else {
			tok = Token{Type: ASSIGN, Literal: "=", Line: line, Column: col}
		}
	case '!':
		if l.peekChar() == '=' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: STRICT_NOT_EQ, Literal: "!==", Line: line, Column: col}
			} else {
				tok = Token{Type: NOT_EQ, Literal: "!=", Line: line, Column: col}
			}
		} else {
			tok = Token{Type: BANG, Literal: "!", Line: line, Column: col}
		}
	case '+':
		if l.peekChar() == '+' {
			l.readChar()
			tok = Token{Type: INC, Literal: "++", Line: line, Column: col}
		} else if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: PLUS_EQ, Literal: "+=", Line: line, Column: col}
		} else {
			tok = Token{Type: PLUS, Literal: "+", Line: line, Column: col}
		}
	case '-':
		if l.peekChar() == '-' {
			l.readChar()
			tok = Token{Type: DEC, Literal: "--", Line: line, Column: col}
		} else if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: MINUS_EQ, Literal: "-=", Line: line, Column: col}
		} else {
			tok = Token{Type: MINUS, Literal: "-", Line: line, Column: col}
		}
	case '*':
		if l.peekChar() == '*' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: EXPONENT_EQ, Literal: "**=", Line: line, Column: col}
			} else {
				tok = Token{Type: EXPONENT, Literal: "**", Line: line, Column: col}
			}
		} else if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: ASTERISK_EQ, Literal: "*=", Line: line, Column: col}
		} else {
			tok = Token{Type: ASTERISK, Literal: "*", Line: line, Column: col}
		}
	case '/':
		// 判断是除法还是正则字面量开始
		if l.isRegexContext() {
			return l.scanRegexLiteral(line, col)
		}
		if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: SLASH_EQ, Literal: "/=", Line: line, Column: col}
		} else {
			tok = Token{Type: SLASH, Literal: "/", Line: line, Column: col}
		}
	case '%':
		if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: PERCENT_EQ, Literal: "%=", Line: line, Column: col}
		} else {
			tok = Token{Type: PERCENT, Literal: "%", Line: line, Column: col}
		}
	case '<':
		if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: LTE, Literal: "<=", Line: line, Column: col}
		} else if l.peekChar() == '<' {
			l.readChar()
			tok = Token{Type: SHIFT_LEFT, Literal: "<<", Line: line, Column: col}
		} else {
			tok = Token{Type: LT, Literal: "<", Line: line, Column: col}
		}
	case '>':
		if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: GTE, Literal: ">=", Line: line, Column: col}
		} else if l.peekChar() == '>' {
			l.readChar()
			if l.peekChar() == '>' {
				l.readChar()
				tok = Token{Type: UNSIGNED_SHR, Literal: ">>>", Line: line, Column: col}
			} else {
				tok = Token{Type: SHIFT_RIGHT, Literal: ">>", Line: line, Column: col}
			}
		} else {
			tok = Token{Type: GT, Literal: ">", Line: line, Column: col}
		}
	case '&':
		if l.peekChar() == '&' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: AND_AND_EQ, Literal: "&&=", Line: line, Column: col}
			} else {
				tok = Token{Type: AND, Literal: "&&", Line: line, Column: col}
			}
		} else if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: AND_EQ, Literal: "&=", Line: line, Column: col}
		} else {
			tok = Token{Type: BIT_AND, Literal: "&", Line: line, Column: col}
		}
	case '|':
		if l.peekChar() == '|' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: OR_OR_EQ, Literal: "||=", Line: line, Column: col}
			} else {
				tok = Token{Type: OR, Literal: "||", Line: line, Column: col}
			}
		} else if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: OR_EQ, Literal: "|=", Line: line, Column: col}
		} else {
			tok = Token{Type: BIT_OR, Literal: "|", Line: line, Column: col}
		}
	case '^':
		if l.peekChar() == '=' {
			l.readChar()
			tok = Token{Type: XOR_EQ, Literal: "^=", Line: line, Column: col}
		} else {
			tok = Token{Type: BIT_XOR, Literal: "^", Line: line, Column: col}
		}
	case '~':
		tok = Token{Type: BIT_NOT, Literal: "~", Line: line, Column: col}
	case '?':
		if l.peekChar() == '?' {
			l.readChar()
			if l.peekChar() == '=' {
				l.readChar()
				tok = Token{Type: NULLISH_ASSIGN, Literal: "??=", Line: line, Column: col}
			} else {
				tok = Token{Type: NULL_COALESCE, Literal: "??", Line: line, Column: col}
			}
		} else if l.peekChar() == '.' {
			// 可选链 ?. (注意排除 ?. 数字, 如 a ?.5: 那是条件表达式)
			nc := l.peekCharAt(1)
			if !isDigit(nc) {
				l.readChar()
				tok = Token{Type: OPTIONAL_CHAIN, Literal: "?.", Line: line, Column: col}
			} else {
				tok = Token{Type: QUESTION, Literal: "?", Line: line, Column: col}
			}
		} else {
			tok = Token{Type: QUESTION, Literal: "?", Line: line, Column: col}
		}
	case '.':
		// 检查是否是 ... (扩展/剩余运算符)
		if l.peekChar() == '.' && l.peekCharAt(1) == '.' {
			l.readChar()
			l.readChar()
			tok = Token{Type: SPREAD_REST, Literal: "...", Line: line, Column: col}
		} else if isDigit(l.peekChar()) {
			// 以 . 开头的浮点数: .5, .25
			return l.readNumber(true, line, col)
		} else {
			tok = Token{Type: DOT, Literal: ".", Line: line, Column: col}
		}
	case ',':
		tok = Token{Type: COMMA, Literal: ",", Line: line, Column: col}
	case ':':
		tok = Token{Type: COLON, Literal: ":", Line: line, Column: col}
	case ';':
		tok = Token{Type: SEMICOLON, Literal: ";", Line: line, Column: col}
	case '(':
		tok = Token{Type: LPAREN, Literal: "(", Line: line, Column: col}
	case ')':
		tok = Token{Type: RPAREN, Literal: ")", Line: line, Column: col}
	case '{':
		// 在模板字面量表达式上下文中，追踪花括号嵌套深度
		if l.inTemplate {
			l.templateBraceDepth++
		}
		tok = Token{Type: LBRACE, Literal: "{", Line: line, Column: col}
	case '}':
		// 在模板字面量中，如果花括号深度为 0，说明这个 } 闭合了 ${} 插值表达式
		if l.inTemplate && l.templateBraceDepth == 0 {
			// 消费 } 并读取模板字符串的下一部分
			l.readChar() // 消费 }
			return l.readTemplateString(line, col, false)
		} else if l.inTemplate {
			l.templateBraceDepth--
			tok = Token{Type: RBRACE, Literal: "}", Line: line, Column: col}
		} else {
			tok = Token{Type: RBRACE, Literal: "}", Line: line, Column: col}
		}
	case '[':
		tok = Token{Type: LBRACKET, Literal: "[", Line: line, Column: col}
	case ']':
		tok = Token{Type: RBRACKET, Literal: "]", Line: line, Column: col}
	case '@':
		tok = Token{Type: AT, Literal: "@", Line: line, Column: col}

	case '$':
		// 在模板字面量中，${ 开始插值表达式
		if l.inTemplate && l.peekChar() == '{' {
			l.readChar() // 消费 $
			l.readChar() // 消费 {
			l.templateBraceDepth = 0
			// 提前返回，避免 trailing readChar 消费下一个字符
			return Token{Type: DOLLAR_BRACE, Literal: "${", Line: line, Column: col}
		}
		// 否则 $ 是标识符的一部分 ($foo, $$bar 等)
		ident := l.readIdentifier()
		tokType := LookupIdentifier(ident)
		tok = Token{Type: tokType, Literal: ident, Line: line, Column: col}
		return tok

	case '"':
		tok = l.readString('"', line, col)
	case '\'':
		tok = l.readString('\'', line, col)
	case '`':
		// 开始或结束模板字面量
		// readTemplateLiteral 内部处理了字符消费，需要提前返回
		return l.readTemplateLiteral(line, col)

	case 0:
		tok = Token{Type: EOF, Literal: "", Line: line, Column: col}

	default:
		if isDigit(l.ch) {
			return l.readNumber(false, line, col)
		}
		if isIdentifierStart(l.ch) {
			ident := l.readIdentifier()
			tokType := LookupIdentifier(ident)
			tok = Token{Type: tokType, Literal: ident, Line: line, Column: col}
			// 如果是 var，返回 ILLEGAL
			if tokType == ILLEGAL {
				tok.Literal = fmt.Sprintf("var is not supported, use let or const instead")
			}
			return tok
		}
		// 无法识别的字符
		tok = Token{Type: ILLEGAL, Literal: string(l.ch), Line: line, Column: col}
	}

	l.readChar()
	return tok
}

// isRegexContext 判断当前 / 是否为正则字面量的开始。
// 基于前一个 token 类型: 如果前一个 token 是标识符、数字、字符串、
// )、]、}、this、true、false、null、undefined 等，则 / 是除法。
// 否则 / 是正则字面量的开始。
func (l *Lexer) isRegexContext() bool {
	switch l.prevTokenType {
	case IDENTIFIER, INT_LITERAL, FLOAT_LITERAL, STRING_LITERAL, REGEX_LITERAL,
		RPAREN, RBRACKET, RBRACE,
		THIS, TRUE, FALSE, NULL, UNDEFINED,
		INC, DEC:
		return false
	}
	return true
}

// scanRegexLiteral 扫描正则字面量 /pattern/flags。
func (l *Lexer) scanRegexLiteral(line, col int) Token {
	var sb strings.Builder

	// 消费开头的 /
	l.readChar()

	// 扫描正则模式 (在字符类 [...] 内不处理转义)
	inCharClass := false
	for l.ch != 0 {
		if l.ch == '\\' {
			sb.WriteRune(l.ch)
			l.readChar()
			if l.ch != 0 {
				sb.WriteRune(l.ch)
				l.readChar()
			}
			continue
		}
		if l.ch == '[' && !inCharClass {
			inCharClass = true
			sb.WriteRune(l.ch)
			l.readChar()
			continue
		}
		if l.ch == ']' && inCharClass {
			inCharClass = false
			sb.WriteRune(l.ch)
			l.readChar()
			continue
		}
		if l.ch == '/' && !inCharClass {
			break
		}
		if l.ch == '\n' {
			// 正则中不能有未转义的换行
			break
		}
		sb.WriteRune(l.ch)
		l.readChar()
	}

	// 消费结尾的 /
	if l.ch == '/' {
		l.readChar()
	} else {
		// 没有闭合的 /，返回 ILLEGAL
		return Token{Type: ILLEGAL, Literal: "/" + sb.String(), Line: line, Column: col}
	}

	pattern := sb.String()

	// 扫描标志位 (g, i, m, s, u, y)
	var flags strings.Builder
	for l.ch != 0 && isRegexFlag(l.ch) {
		flags.WriteRune(l.ch)
		l.readChar()
	}

	// 完整的正则字面量: pattern + flags
	// Literal 中存储格式: pattern|flags (用 | 分隔，因为 | 不会出现在合法正则标志中)
	literal := pattern + "|" + flags.String()
	return Token{Type: REGEX_LITERAL, Literal: literal, Line: line, Column: col}
}

// isRegexFlag 判断字符是否为有效的正则标志。
func isRegexFlag(ch rune) bool {
	switch ch {
	case 'g', 'i', 'm', 's', 'u', 'y':
		return true
	}
	return false
}
// 标识符以字母、下划线或 $ 开头，后续可包含字母、数字、下划线或 $。
func (l *Lexer) readIdentifier() string {
	start := l.position
	for isIdentifierPart(l.ch) {
		l.readChar()
	}
	return l.input[start:l.position]
}

// readNumber 读取一个数字字面量。
// 支持: 整数(42), 浮点数(3.14), 科学计数法(1e5), 二进制(0b1010), 八进制(0o755), 十六进制(0xFF)
// leadingDot: 如果为 true，表示数字以 . 开头 (如 .5)
func (l *Lexer) readNumber(leadingDot bool, line, col int) Token {
	var sb strings.Builder
	isFloat := leadingDot

	if leadingDot {
		sb.WriteByte('.')
		l.readChar() // 消费 '.'
	}

	// 检查十六进制/二进制/八进制前缀
	if l.ch == '0' && (l.peekChar() == 'x' || l.peekChar() == 'X') {
		// 十六进制: 0xFF
		sb.WriteRune(l.ch)
		l.readChar()
		sb.WriteRune(l.ch)
		l.readChar()
		for isHexDigit(l.ch) || l.ch == '_' {
			if l.ch == '_' {
				l.readChar()
				continue
			}
			sb.WriteRune(l.ch)
			l.readChar()
		}
		return Token{Type: INT_LITERAL, Literal: sb.String(), Line: line, Column: col}
	} else if l.ch == '0' && (l.peekChar() == 'b' || l.peekChar() == 'B') {
		// 二进制: 0b1010
		sb.WriteRune(l.ch)
		l.readChar()
		sb.WriteRune(l.ch)
		l.readChar()
		for l.ch == '0' || l.ch == '1' || l.ch == '_' {
			if l.ch == '_' {
				l.readChar()
				continue
			}
			sb.WriteRune(l.ch)
			l.readChar()
		}
		return Token{Type: INT_LITERAL, Literal: sb.String(), Line: line, Column: col}
	} else if l.ch == '0' && (l.peekChar() == 'o' || l.peekChar() == 'O') {
		// 八进制: 0o755
		sb.WriteRune(l.ch)
		l.readChar()
		sb.WriteRune(l.ch)
		l.readChar()
		for (l.ch >= '0' && l.ch <= '7') || l.ch == '_' {
			if l.ch == '_' {
				l.readChar()
				continue
			}
			sb.WriteRune(l.ch)
			l.readChar()
		}
		return Token{Type: INT_LITERAL, Literal: sb.String(), Line: line, Column: col}
	}

	// 十进制整数部分 (支持下划线分隔符 1_000)
	for isDigit(l.ch) || l.ch == '_' {
		if l.ch == '_' {
			l.readChar()
			continue
		}
		sb.WriteRune(l.ch)
		l.readChar()
	}

	// 小数部分
	if l.ch == '.' && !leadingDot {
		isFloat = true
		sb.WriteRune(l.ch)
		l.readChar()
		for isDigit(l.ch) || l.ch == '_' {
			if l.ch == '_' {
				l.readChar()
				continue
			}
			sb.WriteRune(l.ch)
			l.readChar()
		}
	}

	// 科学计数法
	if l.ch == 'e' || l.ch == 'E' {
		isFloat = true
		sb.WriteRune(l.ch)
		l.readChar()
		if l.ch == '+' || l.ch == '-' {
			sb.WriteRune(l.ch)
			l.readChar()
		}
		for isDigit(l.ch) || l.ch == '_' {
			if l.ch == '_' {
				l.readChar()
				continue
			}
			sb.WriteRune(l.ch)
			l.readChar()
		}
	}

	tokType := INT_LITERAL
	if isFloat {
		tokType = FLOAT_LITERAL
	}

	return Token{Type: tokType, Literal: sb.String(), Line: line, Column: col}
}

// readString 读取一个用引号包围的字符串字面量。
// 支持转义: \n, \t, \r, \\, \", \', \`, \${, \0, \uXXXX
func (l *Lexer) readString(quote rune, line, col int) Token {
	l.readChar() // 消费开始引号

	var sb strings.Builder
	for l.ch != quote {
		if l.ch == 0 {
			// 未终止的字符串
			return Token{Type: ILLEGAL, Literal: "unterminated string", Line: line, Column: col}
		}
		if l.ch == '\\' {
			// 转义序列
			l.readChar()
			switch l.ch {
			case 'n':
				sb.WriteRune('\n')
			case 't':
				sb.WriteRune('\t')
			case 'r':
				sb.WriteRune('\r')
			case '\\':
				sb.WriteRune('\\')
			case '"':
				sb.WriteRune('"')
			case '\'':
				sb.WriteRune('\'')
			case '`':
				sb.WriteRune('`')
			case '$':
				sb.WriteRune('$')
			case '0':
				sb.WriteRune(0)
			case 'u':
				// Unicode 转义: \uXXXX
				var hex string
				for j := 0; j < 4; j++ {
					l.readChar()
					hex += string(l.ch)
				}
				if r, err := hexToRune(hex); err == nil {
					sb.WriteRune(r)
				} else {
					sb.WriteRune('u')
					sb.WriteString(hex)
				}
			default:
				sb.WriteRune('\\')
				sb.WriteRune(l.ch)
			}
		} else if l.ch == '\n' {
			// 普通字符串中不允许换行 (模板字面量除外)
			return Token{Type: ILLEGAL, Literal: "unterminated string", Line: line, Column: col}
		} else {
			sb.WriteRune(l.ch)
		}
		l.readChar()
	}

	return Token{Type: STRING_LITERAL, Literal: sb.String(), Line: line, Column: col}
}

// readTemplateLiteral 开始读取模板字面量。
// 遇到 ` 时调用，返回模板字符串内容或 ${ 插值标记。
// 支持嵌套模板: 在 `${}` 内再次遇到 ` 会进入内层模板, 深度+1。
func (l *Lexer) readTemplateLiteral(line, col int) Token {
	l.readChar() // 消费开始反引号 `

	// 设置模板字面量上下文 (支持嵌套, 深度+1)
	l.inTemplate = true
	l.templateDepth++
	l.templateBraceDepth = 0

	// 模板的首个 token 使用 BACKTICK 类型,
	// 与普通字符串 STRING_LITERAL 区分 (用于 tagged template: tag`...`)
	return l.readTemplateString(line, col, true)
}

// readTemplateString 读取模板字面量中的字符串部分。
// 遇到 ${ 时返回 DOLLAR_BRACE 令牌，遇到结束 ` 时返回 STRING_LITERAL 令牌。
// isFirst 为 true 时 (模板第一个 token) 返回 BACKTICK 类型,
// 以与普通字符串区分 (tagged template 需要)。
func (l *Lexer) readTemplateString(line, col int, isFirst bool) Token {
	var sb strings.Builder

	for {
		if l.ch == 0 {
			return Token{Type: ILLEGAL, Literal: "unterminated template literal", Line: line, Column: col}
		}

		if l.ch == '`' {
			// 模板字面量结束 (当前层)
			l.readChar() // 消费结束反引号
			l.templateDepth--
			if l.templateDepth == 0 {
				l.inTemplate = false
			}
			if isFirst {
				return Token{Type: BACKTICK, Literal: sb.String(), Line: line, Column: col}
			}
			return Token{Type: STRING_LITERAL, Literal: sb.String(), Line: line, Column: col}
		}

		if l.ch == '$' && l.peekChar() == '{' {
			// 模板插值开始 ${...}
			// 不消费 ${，只返回字符串部分 (可能为空)
			// 下次 NextToken 调用时由 case '$' 处理 DOLLAR_BRACE
			if isFirst {
				return Token{Type: BACKTICK, Literal: sb.String(), Line: line, Column: col}
			}
			return Token{Type: STRING_LITERAL, Literal: sb.String(), Line: line, Column: col}
		}

		if l.ch == '\\' {
			// 转义序列 (与普通字符串相同)
			l.readChar()
			switch l.ch {
			case 'n':
				sb.WriteRune('\n')
			case 't':
				sb.WriteRune('\t')
			case 'r':
				sb.WriteRune('\r')
			case '\\':
				sb.WriteRune('\\')
			case '`':
				sb.WriteRune('`')
			case '$':
				sb.WriteRune('$')
			case '0':
				sb.WriteRune(0)
			default:
				sb.WriteRune('\\')
				sb.WriteRune(l.ch)
			}
		} else {
			sb.WriteRune(l.ch)
		}

		l.readChar()
	}
}

// skipWhitespaceAndComments 跳过空白字符和注释。
// 支持: 空格, 制表符, 换行, 回车, 单行注释 (//), 多行注释 (/* */)
func (l *Lexer) skipWhitespaceAndComments() {
	for {
		switch {
		case l.ch == ' ' || l.ch == '\t' || l.ch == '\r':
			l.readChar()
		case l.ch == '\n':
			l.line++
			l.column = 0
			l.readChar()
		case l.ch == '/' && l.peekChar() == '/':
			// 单行注释
			for l.ch != '\n' && l.ch != 0 {
				l.readChar()
			}
		case l.ch == '/' && l.peekChar() == '*':
			// 多行注释
			l.readChar() // 消费 /
			l.readChar() // 消费 *
			for {
				if l.ch == 0 {
					return // 未终止的注释，直接返回
				}
				if l.ch == '*' && l.peekChar() == '/' {
					l.readChar() // 消费 *
					l.readChar() // 消费 /
					break
				}
				if l.ch == '\n' {
					l.line++
					l.column = 0
				}
				l.readChar()
			}
		default:
			return
		}
	}
}

// === 辅助函数 ===

func isDigit(ch rune) bool {
	return ch >= '0' && ch <= '9'
}

func isHexDigit(ch rune) bool {
	return (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') || (ch >= 'A' && ch <= 'F')
}

func isIdentifierStart(ch rune) bool {
	return unicode.IsLetter(ch) || ch == '_' || ch == '$'
}

func isIdentifierPart(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_' || ch == '$'
}

func hexToRune(hex string) (rune, error) {
	var r rune
	for _, c := range hex {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r += c - '0'
		case c >= 'a' && c <= 'f':
			r += c - 'a' + 10
		case c >= 'A' && c <= 'F':
			r += c - 'A' + 10
		default:
			return 0, fmt.Errorf("invalid hex digit: %c", c)
		}
	}
	return r, nil
}

// Tokenize 一次性将整个输入转换为 Token 切片。
// 这是一个便利方法，用于测试和 CLI 工具。
func Tokenize(input string) []Token {
	l := New(input)
	var tokens []Token

	for {
		tok := l.NextToken()
		tokens = append(tokens, tok)
		if tok.Type == EOF {
			break
		}
	}

	return tokens
}
