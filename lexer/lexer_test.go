package lexer

import (
	"testing"
)

// TestNextToken 测试各种词法结构。
func TestNextToken(t *testing.T) {
	input := `
	let x = 5;
	const y = 10;
	let add = (a, b) => a + b;
	let name = "world";
	let tpl = ` + "`Hello ${name}!`" + `;
	let arr = [1, 2, ...rest];
	let {a, b: c} = obj;
	`

	tests := []struct {
		expectedType    TokenType
		expectedLiteral string
	}{
		{LET, "let"},
		{IDENTIFIER, "x"},
		{ASSIGN, "="},
		{INT_LITERAL, "5"},
		{SEMICOLON, ";"},
		{CONST, "const"},
		{IDENTIFIER, "y"},
		{ASSIGN, "="},
		{INT_LITERAL, "10"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{IDENTIFIER, "add"},
		{ASSIGN, "="},
		{LPAREN, "("},
		{IDENTIFIER, "a"},
		{COMMA, ","},
		{IDENTIFIER, "b"},
		{RPAREN, ")"},
		{ARROW, "=>"},
		{IDENTIFIER, "a"},
		{PLUS, "+"},
		{IDENTIFIER, "b"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{IDENTIFIER, "name"},
		{ASSIGN, "="},
		{STRING_LITERAL, "world"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{IDENTIFIER, "tpl"},
		{ASSIGN, "="},
		{BACKTICK, "Hello "}, // 模板首段: BACKTICK 类型 (与普通字符串区分)
		{DOLLAR_BRACE, "${"},
		{IDENTIFIER, "name"},
		{STRING_LITERAL, "!"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{IDENTIFIER, "arr"},
		{ASSIGN, "="},
		{LBRACKET, "["},
		{INT_LITERAL, "1"},
		{COMMA, ","},
		{INT_LITERAL, "2"},
		{COMMA, ","},
		{SPREAD_REST, "..."},
		{IDENTIFIER, "rest"},
		{RBRACKET, "]"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{LBRACE, "{"},
		{IDENTIFIER, "a"},
		{COMMA, ","},
		{IDENTIFIER, "b"},
		{COLON, ":"},
		{IDENTIFIER, "c"},
		{RBRACE, "}"},
		{ASSIGN, "="},
		{IDENTIFIER, "obj"},
		{SEMICOLON, ";"},
		{EOF, ""},
	}

	l := New(input)
	for i, tt := range tests {
		tok := l.NextToken()
		if tok.Type != tt.expectedType {
			t.Fatalf("tests[%d] - token type wrong. expected=%s, got=%s (literal=%q)",
				i, tt.expectedType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.expectedLiteral {
			t.Fatalf("tests[%d] - literal wrong. expected=%q, got=%q",
				i, tt.expectedLiteral, tok.Literal)
		}
	}
}

func TestVarIsKeywordToken(t *testing.T) {
	// var 词法层识别为 VAR 关键字 (声明在 parser 语句层被拒绝),
	// Literal 保留原始词素, 作为属性名 (obj.var, {var: 1}) 时键名正确。
	input := `var x = 5;`

	l := New(input)
	tok := l.NextToken()
	if tok.Type != VAR {
		t.Fatalf("expected VAR for 'var', got %s (%q)", tok.Type, tok.Literal)
	}
	if tok.Literal != "var" {
		t.Fatalf("expected literal %q, got %q", "var", tok.Literal)
	}
}

func TestArithmeticOperators(t *testing.T) {
	input := `1 + 2 - 3 * 4 / 5 % 6 ** 7;`

	expected := []TokenType{
		INT_LITERAL, PLUS, INT_LITERAL, MINUS, INT_LITERAL,
		ASTERISK, INT_LITERAL, SLASH, INT_LITERAL, PERCENT,
		INT_LITERAL, EXPONENT, INT_LITERAL, SEMICOLON, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

func TestComparisonOperators(t *testing.T) {
	input := `a == b != c === d !== e < f > g <= h >= i;`

	expected := []TokenType{
		IDENTIFIER, EQ, IDENTIFIER, NOT_EQ, IDENTIFIER,
		STRICT_EQ, IDENTIFIER, STRICT_NOT_EQ, IDENTIFIER,
		LT, IDENTIFIER, GT, IDENTIFIER,
		LTE, IDENTIFIER, GTE, IDENTIFIER, SEMICOLON, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

func TestLogicalOperators(t *testing.T) {
	input := `a && b || c; x & y | z ^ w;`

	expected := []TokenType{
		IDENTIFIER, AND, IDENTIFIER, OR, IDENTIFIER, SEMICOLON,
		IDENTIFIER, BIT_AND, IDENTIFIER, BIT_OR, IDENTIFIER, BIT_XOR, IDENTIFIER,
		SEMICOLON, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

func TestIncrementDecrement(t *testing.T) {
	input := `x++; y--; x += 5; y -= 3; x *= 2; y /= 4; z %= 2;`

	expected := []TokenType{
		IDENTIFIER, INC, SEMICOLON,
		IDENTIFIER, DEC, SEMICOLON,
		IDENTIFIER, PLUS_EQ, INT_LITERAL, SEMICOLON,
		IDENTIFIER, MINUS_EQ, INT_LITERAL, SEMICOLON,
		IDENTIFIER, ASTERISK_EQ, INT_LITERAL, SEMICOLON,
		IDENTIFIER, SLASH_EQ, INT_LITERAL, SEMICOLON,
		IDENTIFIER, PERCENT_EQ, INT_LITERAL, SEMICOLON,
		EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

func TestNumberFormats(t *testing.T) {
	tests := []struct {
		input   string
		tokType TokenType
		literal string
	}{
		{"42", INT_LITERAL, "42"},
		{"3.14", FLOAT_LITERAL, "3.14"},
		{".5", FLOAT_LITERAL, ".5"},
		{"1e5", FLOAT_LITERAL, "1e5"},
		{"6.022e23", FLOAT_LITERAL, "6.022e23"},
		{"0xFF", INT_LITERAL, "0xFF"},
		{"0b1010", INT_LITERAL, "0b1010"},
		{"0o755", INT_LITERAL, "0o755"},
	}

	for _, tt := range tests {
		l := New(tt.input)
		tok := l.NextToken()
		if tok.Type != tt.tokType {
			t.Errorf("input %q: expected type %s, got %s", tt.input, tt.tokType, tok.Type)
		}
		if tok.Literal != tt.literal {
			t.Errorf("input %q: expected literal %q, got %q", tt.input, tt.literal, tok.Literal)
		}
	}
}

func TestStringEscapes(t *testing.T) {
	input := `"hello\nworld\ttab"`

	l := New(input)
	tok := l.NextToken()
	if tok.Type != STRING_LITERAL {
		t.Fatalf("expected STRING_LITERAL, got %s", tok.Type)
	}
	expected := "hello\nworld\ttab"
	if tok.Literal != expected {
		t.Fatalf("expected %q, got %q", expected, tok.Literal)
	}
}

func TestTemplateLiteral(t *testing.T) {
	input := "`Hello, ${name}! You are ${age} years old.`"

	expected := []struct {
		tokType TokenType
		literal string
	}{
		{BACKTICK, "Hello, "}, // 模板首段: BACKTICK 类型
		{DOLLAR_BRACE, "${"},
		{IDENTIFIER, "name"},
		{STRING_LITERAL, "! You are "},
		{DOLLAR_BRACE, "${"},
		{IDENTIFIER, "age"},
		{STRING_LITERAL, " years old."},
		{EOF, ""},
	}

	l := New(input)
	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.tokType {
			t.Fatalf("tests[%d] - expected %s, got %s (literal=%q)",
				i, tt.tokType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] - expected literal %q, got %q",
				i, tt.literal, tok.Literal)
		}
	}
}

func TestTemplateLiteralWithBraces(t *testing.T) {
	// 模板表达式中包含花括号 (对象字面量)
	input := "`Result: ${{a: 1}.a}`"

	expected := []struct {
		tokType TokenType
		literal string
	}{
		{BACKTICK, "Result: "}, // 模板首段: BACKTICK 类型
		{DOLLAR_BRACE, "${"},
		{LBRACE, "{"},
		{IDENTIFIER, "a"},
		{COLON, ":"},
		{INT_LITERAL, "1"},
		{RBRACE, "}"},
		{DOT, "."},
		{IDENTIFIER, "a"},
		{STRING_LITERAL, ""},
		{EOF, ""},
	}

	l := New(input)
	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.tokType {
			t.Fatalf("tests[%d] - expected %s, got %s (literal=%q)",
				i, tt.tokType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] - expected literal %q, got %q",
				i, tt.literal, tok.Literal)
		}
	}
}

func TestEmptyTemplateLiteral(t *testing.T) {
	input := "``"

	l := New(input)
	tok := l.NextToken()
	if tok.Type != BACKTICK {
		t.Fatalf("expected BACKTICK, got %s", tok.Type)
	}
	if tok.Literal != "" {
		t.Fatalf("expected empty string, got %q", tok.Literal)
	}
	tok = l.NextToken()
	if tok.Type != EOF {
		t.Fatalf("expected EOF, got %s", tok.Type)
	}
}

func TestComments(t *testing.T) {
	input := `
	// single line comment
	let x = 5; // trailing comment
	/* multi
	   line */
	let y = 10;
	`

	expected := []struct {
		tokType TokenType
		literal string
	}{
		{LET, "let"},
		{IDENTIFIER, "x"},
		{ASSIGN, "="},
		{INT_LITERAL, "5"},
		{SEMICOLON, ";"},
		{LET, "let"},
		{IDENTIFIER, "y"},
		{ASSIGN, "="},
		{INT_LITERAL, "10"},
		{SEMICOLON, ";"},
		{EOF, ""},
	}

	l := New(input)
	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.tokType {
			t.Fatalf("tests[%d] - expected %s, got %s (literal=%q)",
				i, tt.tokType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] - expected literal %q, got %q",
				i, tt.literal, tok.Literal)
		}
	}
}

func TestKeywords(t *testing.T) {
	keywords := []string{
		"let", "const", "if", "else", "for", "of", "while", "do",
		"break", "continue", "function", "return", "true", "false",
		"null", "undefined", "typeof", "instanceof", "new", "this",
		"delete", "in", "try", "catch", "finally", "throw",
		"switch", "case", "default",
	}

	expectedTypes := []TokenType{
		LET, CONST, IF, ELSE, FOR, OF, WHILE, DO,
		BREAK, CONTINUE, FUNCTION, RETURN, TRUE, FALSE,
		NULL, UNDEFINED, TYPEOF, INSTANCEOF, NEW, THIS,
		DELETE, IN, TRY, CATCH, FINALLY, THROW,
		SWITCH, CASE, DEFAULT,
	}

	for i, kw := range keywords {
		l := New(kw)
		tok := l.NextToken()
		if tok.Type != expectedTypes[i] {
			t.Errorf("keyword %q: expected %s, got %s", kw, expectedTypes[i], tok.Type)
		}
	}
}

func TestForOfToken(t *testing.T) {
	input := `for (let x of arr) {}`

	expected := []TokenType{
		FOR, LPAREN, LET, IDENTIFIER, OF, IDENTIFIER, RPAREN, LBRACE, RBRACE, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

func TestSpreadRest(t *testing.T) {
	input := `let [a, ...rest] = arr; f(...args);`

	expected := []TokenType{
		LET, LBRACKET, IDENTIFIER, COMMA, SPREAD_REST, IDENTIFIER, RBRACKET,
		ASSIGN, IDENTIFIER, SEMICOLON,
		IDENTIFIER, LPAREN, SPREAD_REST, IDENTIFIER, RPAREN, SEMICOLON, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}

// 多字节字符必须作为一个整体 rune 被读取。
// 逐字节读取会把 "😀" 的 4 个字节拆成 4 个非法码点，
// 字符串字面量在词法阶段就被破坏，后续 UTF-16 语义全部失效。
func TestMultibyteCharacters(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`"世"`, "世"},     // 3 字节 BMP 字符
		{`"😀"`, "😀"},     // 4 字节 astral 字符 (代理对)
		{`"a世b"`, "a世b"}, // 多字节与非 ASCII 混排
	}

	for _, tt := range tests {
		l := New(tt.input)
		tok := l.NextToken()
		if tok.Type != STRING_LITERAL {
			t.Fatalf("input %s: expected STRING_LITERAL, got %s", tt.input, tok.Type)
		}
		if tok.Literal != tt.expected {
			t.Fatalf("input %s: expected %q (%d bytes), got %q (%d bytes)",
				tt.input, tt.expected, len(tt.expected), tok.Literal, len(tok.Literal))
		}
		// 长度按 rune 计，而非字节
		if got := len([]rune(tok.Literal)); got != len([]rune(tt.expected)) {
			t.Fatalf("input %s: expected %d runes, got %d",
				tt.input, len([]rune(tt.expected)), got)
		}
	}
}

// 多字节字符后面的 token 必须能被正确识别
// (readPosition 需要按 rune 宽度推进)。
func TestMultibyteFollowedByToken(t *testing.T) {
	l := New(`"世" + 1;`)

	expected := []struct {
		tokType TokenType
		literal string
	}{
		{STRING_LITERAL, "世"},
		{PLUS, "+"},
		{INT_LITERAL, "1"},
		{SEMICOLON, ";"},
		{EOF, ""},
	}

	for i, tt := range expected {
		tok := l.NextToken()
		if tok.Type != tt.tokType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)", i, tt.tokType, tok.Type, tok.Literal)
		}
		if tok.Literal != tt.literal {
			t.Fatalf("tests[%d] - expected literal %q, got %q", i, tt.literal, tok.Literal)
		}
	}
}

func TestArrowFunction(t *testing.T) {
	input := `let f = () => 42; let g = (a) => a + 1;`

	expected := []TokenType{
		LET, IDENTIFIER, ASSIGN, LPAREN, RPAREN, ARROW, INT_LITERAL, SEMICOLON,
		LET, IDENTIFIER, ASSIGN, LPAREN, IDENTIFIER, RPAREN, ARROW,
		IDENTIFIER, PLUS, INT_LITERAL, SEMICOLON, EOF,
	}

	l := New(input)
	for i, expectedType := range expected {
		tok := l.NextToken()
		if tok.Type != expectedType {
			t.Fatalf("tests[%d] - expected %s, got %s (%q)",
				i, expectedType, tok.Type, tok.Literal)
		}
	}
}
