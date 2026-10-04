package parser

import (
	"testing"

	"github.com/14752222/Gox/lexer"
)

// ===== 私有成员 #name 的解析形状（rNZO0b）=====

func parseSrc(t *testing.T, src string) (*Parser, bool) {
	t.Helper()
	p := New(lexer.New(src))
	p.ParseProgram()
	return p, !p.Errors().HasErrors()
}

// TestPrivateNameTokenShapes 词法: #x 切成 PRIVATE_NAME, 字面 # 仍 ILLEGAL。
func TestPrivateNameTokenShapes(t *testing.T) {
	l := lexer.New("a.#b #")
	toks := []lexer.Token{}
	for {
		tok := l.NextToken()
		if tok.Type == lexer.EOF {
			break
		}
		toks = append(toks, tok)
	}
	found := false
	for _, tok := range toks {
		if tok.Type == lexer.PRIVATE_NAME {
			if tok.Literal != "#b" {
				t.Errorf("PRIVATE_NAME Literal 应含 #, got %q", tok.Literal)
			}
			found = true
		}
	}
	if !found {
		t.Error("a.#b 应切出 PRIVATE_NAME")
	}
	// 末尾孤立 # 是 ILLEGAL
	last := toks[len(toks)-1]
	if last.Type != lexer.ILLEGAL {
		t.Errorf("孤立 # 应为 ILLEGAL, got %s", last.Type)
	}
}

// TestPrivateMemberShapes 解析矩阵: 方法/字段/静态/访问器/带参方法。
func TestPrivateMemberShapes(t *testing.T) {
	cases := []string{
		"class E { #m(){ return 1 } }",
		"class E { #m(v){ return v } }",
		"class E { #f = 1 }",
		"class E { #f }",
		"class E { static #sv = 7 }",
		"class E { get #g(){ return 1 } }",
		"class E { set #s(v){ } }",
		"class E { #a = 1; #b = 2; sum(){ return this.#a + this.#b } }",
		"const C = class { #f = 1; get(){ return this.#f } };",
	}
	for _, src := range cases {
		if _, ok := parseSrc(t, src); !ok {
			t.Errorf("应可解析: %q, errs=%v", src, New(lexer.New(src)).Errors())
		}
	}
}

// TestPrivateMemberNegative 负向: #constructor 非法、#[x] 非法。
func TestPrivateMemberNegative(t *testing.T) {
	bad := []string{
		"class X { #constructor() {} }",
		// delete / super 位置的私有名是规范早错, 必须**解析期**拒绝
		// (也是编译器 Property 断言 panic 的入口, 见 vm 侧同名守卫用例)。
		"class X { #x = 1; m(){ return delete this.#x } }",
		"class X { static #s = 1; static m(){ return delete X.#s } }",
		"class D { #m(){ return 1 } } class E extends D { n(){ return super.#m() } }",
	}
	for _, src := range bad {
		if _, ok := parseSrc(t, src); ok {
			t.Errorf("应被拒绝: %q", src)
		}
	}
}

// TestPrivateExpressionInContext `#x in obj` 解析为 In 表达式 + PrivateIdentifier。
func TestPrivateExpressionInContext(t *testing.T) {
	p := New(lexer.New("class C { static has(o){ return #z in o } }"))
	prog := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("errs=%v", p.Errors())
	}
	if len(prog.Statements) != 1 {
		t.Fatalf("stmts=%d", len(prog.Statements))
	}
}
