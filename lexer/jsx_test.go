package lexer

import "testing"

// collectTokens 收集令牌并带上限保护: 词法器若在某条路径上不消费字符,
// 会在上限处失败而不是挂死测试进程。
func collectTokens(t *testing.T, input string) []Token {
	t.Helper()
	l := New(input)
	var toks []Token
	for {
		tok := l.NextToken()
		toks = append(toks, tok)
		if tok.Type == EOF || len(toks) > 10000 {
			break
		}
	}
	if len(toks) > 10000 {
		t.Fatalf("lexer did not terminate for input %q", input)
	}
	return toks
}

// types 提取令牌类型序列, 便于断言。
func types(toks []Token) []TokenType {
	out := make([]TokenType, len(toks))
	for i, tok := range toks {
		out[i] = tok.Type
	}
	return out
}

func equalTypes(got, want []TokenType) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestJSXBasicTokens 验证一个完整 JSX 元素的令牌序列:
// 开标签 (标识符/属性/{表达式}/GT) → 子文本 → 闭合标签。
func TestJSXBasicTokens(t *testing.T) {
	input := `x = <text font={20}>hi</text>;`
	got := types(collectTokens(t, input))
	want := []TokenType{
		IDENTIFIER, ASSIGN, JSX_LT, IDENTIFIER, // x = < text
		IDENTIFIER, ASSIGN, LBRACE, INT_LITERAL, RBRACE, // font={20}
		GT,        // >
		JSX_TEXT,  // hi
		JSX_CLOSE, // </text>
		SEMICOLON, EOF,
	}
	if !equalTypes(got, want) {
		t.Fatalf("token mismatch:\n got  %v\n want %v", got, want)
	}
}

// TestJSXSelfCloseAndBareAttr 自闭合与裸属性。
func TestJSXSelfCloseAndBareAttr(t *testing.T) {
	input := `x = <input disabled />;`
	got := types(collectTokens(t, input))
	want := []TokenType{
		IDENTIFIER, ASSIGN, JSX_LT, IDENTIFIER,
		IDENTIFIER, // disabled (裸属性)
		JSX_SELF_CLOSE,
		SEMICOLON, EOF,
	}
	if !equalTypes(got, want) {
		t.Fatalf("token mismatch:\n got  %v\n want %v", got, want)
	}
}

// TestJSXAttrStringAndDashedName 字符串属性值与带 - 的属性名。
func TestJSXAttrStringAndDashedName(t *testing.T) {
	input := `x = <label data-role="name" />;`
	toks := collectTokens(t, input)
	got := types(toks)
	want := []TokenType{
		IDENTIFIER, ASSIGN, JSX_LT, IDENTIFIER,
		IDENTIFIER, ASSIGN, STRING_LITERAL, // data-role="name"
		JSX_SELF_CLOSE,
		SEMICOLON, EOF,
	}
	if !equalTypes(got, want) {
		t.Fatalf("token mismatch:\n got  %v\n want %v", got, want)
	}
	if toks[4].Literal != "data-role" {
		t.Fatalf("dashed attr name: got %q", toks[4].Literal)
	}
	if toks[6].Literal != "name" {
		t.Fatalf("attr string value: got %q", toks[6].Literal)
	}
}

// TestJSXComparisonNotAmbient 普通 `<` 比较运算不受 JSX 模式影响。
func TestJSXComparisonNotAmbient(t *testing.T) {
	for _, input := range []string{
		`a < b`,   // 标识符后 → 比较
		`i<n`,     // 无空格紧贴 → 比较
		`f(x)<y`,  // 括号后 → 比较
		`a<<b`,    // 位移 → SHIFT_LEFT
		`x = <3;`, // < 后是数字 → 仍是 LT (不是 JSX)
	} {
		for _, tok := range collectTokens(t, input) {
			switch tok.Type {
			case JSX_LT, JSX_CLOSE, JSX_TEXT, JSX_SELF_CLOSE:
				t.Fatalf("%q: unexpected JSX token %v", input, tok.Type)
			}
		}
	}
}

// TestJSXExprBraceMatching 属性插值表达式内的花括号配对:
// 对象字面量 attr={{a:1}} 的内层花括号不应提前闭合插值。
func TestJSXExprBraceMatching(t *testing.T) {
	input := `x = <b style={{color: 'red', n: {deep: 1}}} />;`
	got := types(collectTokens(t, input))
	want := []TokenType{
		IDENTIFIER, ASSIGN, JSX_LT, IDENTIFIER,
		IDENTIFIER, ASSIGN, LBRACE, // style={{
		LBRACE, IDENTIFIER, COLON, STRING_LITERAL, // {color: 'red'
		COMMA, IDENTIFIER, COLON, LBRACE, IDENTIFIER, COLON, INT_LITERAL, RBRACE, // , n: {deep: 1}}
		RBRACE, // } 关闭外层对象
		RBRACE, // } 关闭插值表达式 (弹栈)
		JSX_SELF_CLOSE,
		SEMICOLON, EOF,
	}
	if !equalTypes(got, want) {
		t.Fatalf("token mismatch:\n got  %v\n want %v", got, want)
	}
}

// TestJSXMalformedTerminates 各类非法 JSX 输入必须终止词法 (不挂死、不爆内存)。
func TestJSXMalformedTerminates(t *testing.T) {
	for _, input := range []string{
		`x = <div`,                         // 未闭合标签
		`x = <div>text`,                    // 未闭合子文本
		`x = <div>{unclosed`,               // 未闭合插值
		`x = <>frag</>;`,                   // 片段语法 (v1 不支持)
		`x = <div 5></div>;`,               // 标签内非法字符
		`x = <div data-role="unterminated`, // 未闭合属性字符串
	} {
		collectTokens(t, input) // 上限保护在 helper 内断言
	}
}

// TestJSXNestedElementChildren 嵌套元素: 子文本区的 `<` 开启子元素,
// 闭合后回到外层子文本。
func TestJSXNestedElementChildren(t *testing.T) {
	input := `x = <a>t1<b>t2</b>t3</a>;`
	got := types(collectTokens(t, input))
	want := []TokenType{
		IDENTIFIER, ASSIGN, JSX_LT, IDENTIFIER, GT,
		JSX_TEXT,
		JSX_LT, IDENTIFIER, GT, JSX_TEXT, JSX_CLOSE, // <b>t2</b>
		JSX_TEXT,
		JSX_CLOSE,
		SEMICOLON, EOF,
	}
	if !equalTypes(got, want) {
		t.Fatalf("token mismatch:\n got  %v\n want %v", got, want)
	}
}
