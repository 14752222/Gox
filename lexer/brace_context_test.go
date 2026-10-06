package lexer

import "testing"

// ===== 花括号上下文: `}` 后 `/` 是正则还是除法 (rUZN3k 第 2 块) =====
//
// isRegexContext 只看前一个 token 类型无法区分 `}` 之后的 `/` —— 需靠
// braceStack 追踪该 `}` 闭合的是语句块 (→ 正则) 还是表达式 (→ 除法)。
// 这里逐用例断言 `/` 被切成正则还是除法。

func firstSlashKind(t *testing.T, src string) (TokenType, string) {
	t.Helper()
	l := New(src)
	for i := 0; i < 200; i++ {
		tok := l.NextToken()
		if tok.Type == EOF {
			break
		}
		if tok.Type == REGEX_LITERAL {
			return REGEX_LITERAL, tok.Literal
		}
		if tok.Type == SLASH || tok.Type == SLASH_EQ {
			return tok.Type, tok.Literal
		}
	}
	return ILLEGAL, ""
}

func TestBraceContextSlashKind(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want TokenType
	}{
		// `}` 闭合语句块 ⇒ `/` 是正则
		{"空块后正则", `{} /a/`, REGEX_LITERAL},
		{"if 块后正则", `if (x) {} /a/`, REGEX_LITERAL},
		{"while 块后正则", `while (x) {} /a/`, REGEX_LITERAL},
		{"for 块后正则", `for (;;) {} /a/`, REGEX_LITERAL},
		{"switch 块后正则", `switch (x) {} /a/`, REGEX_LITERAL},
		{"try 块后正则", `try {} catch (e) {} /a/`, REGEX_LITERAL},
		{"else 块后正则", `if (x) {} else {} /a/`, REGEX_LITERAL},
		{"do 块后正则", `do {} while (x) /a/`, REGEX_LITERAL},
		{"if 头括号后正则", `if (x) /a/`, REGEX_LITERAL},
		{"class 声明体后正则", `class C {} /a/`, REGEX_LITERAL},
		{"class extends 声明体后正则", `class D extends B {} /a/`, REGEX_LITERAL},

		// `)` 闭合普通分组/调用括号 ⇒ `/` 是除法
		{"分组括号后除法", `(a) / 2`, SLASH},
		{"调用括号后除法", `foo() / 2`, SLASH},

		// `}` 闭合表达式 ⇒ `/` 是除法
		{"函数体后除法", `var x = function () { return 1 } / 2`, SLASH},
		{"对象字面量后除法", `var o = { a: 1 } / 2`, SLASH},
		{"分组对象后除法", `({}) / 2`, SLASH},
		{"类表达式体后除法", `var C = class {} / 2`, SLASH},
		{"箭头体后除法", `var f = () => {} / 2`, SLASH},
		{"方法体后除法", `({ m() {} } / 2)`, SLASH},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, lit := firstSlashKind(t, tc.src)
			if got != tc.want {
				t.Errorf("want %s, got %s (lit=%q)\nsrc: %s", tc.want, got, lit, tc.src)
			}
		})
	}
}
