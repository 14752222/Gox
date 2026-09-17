package lexer

// TokenType 表示词法令牌的类型。
// 使用整数枚举而非字符串，以提高比较效率。
type TokenType int

const (
	// ==================== 特殊令牌 ====================
	ILLEGAL TokenType = iota // 非法/无法识别的字符
	EOF                      // 文件结束

	// ==================== 字面量 ====================
	IDENTIFIER     // 标识符: foo, bar, x, myVar
	INT_LITERAL    // 整数字面量: 42, 0xFF, 0b1010, 0o755
	FLOAT_LITERAL  // 浮点数字面量: 3.14, 1e5, 6.022e23
	BIGINT_LITERAL // BigInt 字面量: 1n, 0xFFn, 0b1010n (词法阶段已去掉 n 后缀)
	STRING_LITERAL // 字符串字面量: "hello", 'world'
	REGEX_LITERAL  // 正则字面量: /pattern/flags

	// ==================== ES6 特有令牌 ====================
	BACKTICK     // ` 模板字面量开始/结束
	DOLLAR_BRACE // ${ 模板插值开始
	SPREAD_REST  // ... 扩展/剩余运算符
	ARROW        // => 箭头函数

	// ==================== 单字符运算符 ====================
	ASSIGN    // =
	PLUS      // +
	MINUS     // -
	BANG      // !
	ASTERISK  // *
	SLASH     // /
	PERCENT   // %
	LT        // <
	GT        // >
	DOT       // .
	COMMA     // ,
	COLON     // :
	SEMICOLON // ;
	LPAREN    // (
	RPAREN    // )
	LBRACE    // {
	RBRACE    // }
	LBRACKET  // [
	RBRACKET  // ]
	QUESTION  // ?
	TILDE     // ~
	AT        // @ (装饰器预留)

	// ==================== 多字符运算符 ====================
	EQ              // ==
	NOT_EQ          // !=
	STRICT_EQ       // ===
	STRICT_NOT_EQ   // !==
	LTE             // <=
	GTE             // >=
	AND             // &&
	OR              // ||
	NULL_COALESCE   // ?? (ES2020, 预留)
	PLUS_EQ         // +=
	MINUS_EQ        // -=
	ASTERISK_EQ     // *=
	SLASH_EQ        // /=
	PERCENT_EQ      // %=
	INC             // ++
	DEC             // --
	EXPONENT        // ** (ES2016)
	EXPONENT_EQ     // **=
	AND_EQ          // &=
	OR_EQ           // |=
	XOR_EQ          // ^=
	AND_AND_EQ      // &&= (逻辑与赋值, ES2021)
	OR_OR_EQ        // ||= (逻辑或赋值, ES2021)
	NULLISH_ASSIGN  // ??= (逻辑空赋值, ES2021)
	BIT_AND         // &
	BIT_OR          // |
	BIT_XOR         // ^
	BIT_NOT         // ~ (同 TILDE, 但语义不同)
	SHIFT_LEFT      // <<
	SHIFT_RIGHT     // >>
	UNSIGNED_SHR    // >>> (无符号右移)
	SHIFT_LEFT_EQ   // <<=
	SHIFT_RIGHT_EQ  // >>=
	UNSIGNED_SHR_EQ // >>>=
	OPTIONAL_CHAIN  // ?. (可选链)

	// ==================== 关键字 ====================
	// 注意: VAR 是合法 token，但本运行时在解析器语句层拒绝 var 声明 (仅支持 let/const)。
	// 词法层保留 VAR 是为了允许 var 作为属性名 (obj.var, {var: 1})，
	// 与 class/default 等关键字的属性名用法保持一致。
	LET        // let
	CONST      // const
	VAR        // var (声明在 parser 层被拒绝, 仅允许作为属性名)
	IF         // if
	ELSE       // else
	FOR        // for
	OF         // of (for...of)
	WHILE      // while
	DO         // do
	BREAK      // break
	CONTINUE   // continue
	FUNCTION   // function
	RETURN     // return
	TRUE       // true
	FALSE      // false
	NULL       // null
	UNDEFINED  // undefined
	TYPEOF     // typeof
	INSTANCEOF // instanceof
	NEW        // new
	THIS       // this
	DELETE     // delete
	VOID       // void
	IN         // in
	TRY        // try
	CATCH      // catch
	FINALLY    // finally
	THROW      // throw
	SWITCH     // switch
	CASE       // case
	DEFAULT    // default
	CLASS      // class (预留扩展)
	SUPER      // super (预留扩展)
	IMPORT     // import (预留扩展)
	EXPORT     // export (预留扩展)
	YIELD      // yield (预留扩展)
	ASYNC      // async (预留扩展)
	AWAIT      // await (预留扩展)

	// ==================== JSX 令牌 ====================
	// 注意: 必须追加在枚举末尾, 不得插入中间 —— 已有常量的数值编号
	// 被测试与调试输出依赖。
	JSX_LT         // < 在表达式位置开始一个 JSX 元素
	JSX_SELF_CLOSE // /> 标签自闭合
	JSX_CLOSE      // </tag> 闭合标签 (Literal 为标签名)
	JSX_TEXT       // JSX 子文本 (Literal 为原始文本, 未做空白规整)
)

// Token 表示一个词法令牌。
// 使用值类型而非指针，因为令牌是扁平数据，值切片提供更好的缓存局部性。
type Token struct {
	Type    TokenType
	Literal string // 原始词素文本
	Line    int    // 源码行号 (从1开始)
	Column  int    // 源码列号 (从1开始)
}

// keywords 将关键字字符串映射到 TokenType。
// "var" 映射到 VAR: 词法层正常识别, 由解析器在语句层拒绝声明用法,
// 同时允许 var 作为属性名 (obj.var, {var: 1})。
var keywords = map[string]TokenType{
	"var":        VAR,
	"let":        LET,
	"const":      CONST,
	"if":         IF,
	"else":       ELSE,
	"for":        FOR,
	"of":         OF,
	"while":      WHILE,
	"do":         DO,
	"break":      BREAK,
	"continue":   CONTINUE,
	"function":   FUNCTION,
	"return":     RETURN,
	"true":       TRUE,
	"false":      FALSE,
	"null":       NULL,
	"undefined":  UNDEFINED,
	"typeof":     TYPEOF,
	"instanceof": INSTANCEOF,
	"new":        NEW,
	"this":       THIS,
	"delete":     DELETE,
	"void":       VOID,
	"in":         IN,
	"try":        TRY,
	"catch":      CATCH,
	"finally":    FINALLY,
	"throw":      THROW,
	"switch":     SWITCH,
	"case":       CASE,
	"default":    DEFAULT,
	"class":      CLASS,
	"super":      SUPER,
	"import":     IMPORT,
	"export":     EXPORT,
	"yield":      YIELD,
	"async":      ASYNC,
	"await":      AWAIT,
	// "var" 被故意排除！
}

// LookupIdentifier 查找标识符是否为关键字。
// 如果是关键字，返回对应的 TokenType；否则返回 IDENTIFIER。
func LookupIdentifier(identifier string) TokenType {
	if tokType, ok := keywords[identifier]; ok {
		return tokType
	}
	return IDENTIFIER
}

// String 返回 TokenType 的可读名称，用于调试。
func (t TokenType) String() string {
	switch t {
	case ILLEGAL:
		return "ILLEGAL"
	case EOF:
		return "EOF"
	case IDENTIFIER:
		return "IDENTIFIER"
	case INT_LITERAL:
		return "INT_LITERAL"
	case FLOAT_LITERAL:
		return "FLOAT_LITERAL"
	case BIGINT_LITERAL:
		return "BIGINT_LITERAL"
	case STRING_LITERAL:
		return "STRING_LITERAL"
	case REGEX_LITERAL:
		return "REGEX_LITERAL"
	case BACKTICK:
		return "BACKTICK"
	case DOLLAR_BRACE:
		return "DOLLAR_BRACE"
	case SPREAD_REST:
		return "SPREAD_REST"
	case ARROW:
		return "ARROW"
	case ASSIGN:
		return "ASSIGN"
	case PLUS:
		return "PLUS"
	case MINUS:
		return "MINUS"
	case BANG:
		return "BANG"
	case ASTERISK:
		return "ASTERISK"
	case SLASH:
		return "SLASH"
	case PERCENT:
		return "PERCENT"
	case LT:
		return "LT"
	case GT:
		return "GT"
	case DOT:
		return "DOT"
	case COMMA:
		return "COMMA"
	case COLON:
		return "COLON"
	case SEMICOLON:
		return "SEMICOLON"
	case LPAREN:
		return "LPAREN"
	case RPAREN:
		return "RPAREN"
	case LBRACE:
		return "LBRACE"
	case RBRACE:
		return "RBRACE"
	case LBRACKET:
		return "LBRACKET"
	case RBRACKET:
		return "RBRACKET"
	case QUESTION:
		return "QUESTION"
	case EQ:
		return "EQ"
	case NOT_EQ:
		return "NOT_EQ"
	case STRICT_EQ:
		return "STRICT_EQ"
	case STRICT_NOT_EQ:
		return "STRICT_NOT_EQ"
	case LTE:
		return "LTE"
	case GTE:
		return "GTE"
	case AND:
		return "AND"
	case OR:
		return "OR"
	case NULL_COALESCE:
		return "NULL_COALESCE"
	case PLUS_EQ:
		return "PLUS_EQ"
	case MINUS_EQ:
		return "MINUS_EQ"
	case ASTERISK_EQ:
		return "ASTERISK_EQ"
	case SLASH_EQ:
		return "SLASH_EQ"
	case PERCENT_EQ:
		return "PERCENT_EQ"
	case INC:
		return "INC"
	case DEC:
		return "DEC"
	case EXPONENT:
		return "EXPONENT"
	case EXPONENT_EQ:
		return "EXPONENT_EQ"
	case BIT_AND:
		return "BIT_AND"
	case BIT_OR:
		return "BIT_OR"
	case BIT_XOR:
		return "BIT_XOR"
	case AND_EQ:
		return "AND_EQ"
	case OR_EQ:
		return "OR_EQ"
	case XOR_EQ:
		return "XOR_EQ"
	case SHIFT_LEFT:
		return "SHIFT_LEFT"
	case SHIFT_RIGHT:
		return "SHIFT_RIGHT"
	case UNSIGNED_SHR:
		return "UNSIGNED_SHR"
	case SHIFT_LEFT_EQ:
		return "SHIFT_LEFT_EQ"
	case SHIFT_RIGHT_EQ:
		return "SHIFT_RIGHT_EQ"
	case UNSIGNED_SHR_EQ:
		return "UNSIGNED_SHR_EQ"
	case OPTIONAL_CHAIN:
		return "OPTIONAL_CHAIN"
	case AND_AND_EQ:
		return "AND_AND_EQ"
	case OR_OR_EQ:
		return "OR_OR_EQ"
	case NULLISH_ASSIGN:
		return "NULLISH_ASSIGN"
	case LET:
		return "LET"
	case CONST:
		return "CONST"
	case VAR:
		return "VAR"
	case IF:
		return "IF"
	case ELSE:
		return "ELSE"
	case FOR:
		return "FOR"
	case OF:
		return "OF"
	case WHILE:
		return "WHILE"
	case DO:
		return "DO"
	case BREAK:
		return "BREAK"
	case CONTINUE:
		return "CONTINUE"
	case FUNCTION:
		return "FUNCTION"
	case RETURN:
		return "RETURN"
	case TRUE:
		return "TRUE"
	case FALSE:
		return "FALSE"
	case NULL:
		return "NULL"
	case UNDEFINED:
		return "UNDEFINED"
	case TYPEOF:
		return "TYPEOF"
	case INSTANCEOF:
		return "INSTANCEOF"
	case NEW:
		return "NEW"
	case THIS:
		return "THIS"
	case DELETE:
		return "DELETE"
	case VOID:
		return "VOID"
	case IN:
		return "IN"
	case TRY:
		return "TRY"
	case CATCH:
		return "CATCH"
	case FINALLY:
		return "FINALLY"
	case THROW:
		return "THROW"
	case SWITCH:
		return "SWITCH"
	case CASE:
		return "CASE"
	case DEFAULT:
		return "DEFAULT"
	case JSX_LT:
		return "JSX_LT"
	case JSX_SELF_CLOSE:
		return "JSX_SELF_CLOSE"
	case JSX_CLOSE:
		return "JSX_CLOSE"
	case JSX_TEXT:
		return "JSX_TEXT"
	default:
		return "UNKNOWN"
	}
}
