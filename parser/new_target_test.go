package parser

import (
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// TestMetaPropertyParsed 函数体内 `new.target` 解析为 MetaProperty 节点
// (此前 `new` 关键字分支不认识 `.target`, 直接报 "no prefix parse function for DOT")。
func TestMetaPropertyParsed(t *testing.T) {
	p := New(lexer.New("function f() { return new.target; }"))
	prog := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("函数体内 new.target 应能解析: %s", p.Errors().String())
	}
	fn, ok := prog.Statements[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("首语句应为 FunctionDeclaration, got %T", prog.Statements[0])
	}
	ret, ok := fn.Body.Statements[0].(*ast.ReturnStatement)
	if !ok {
		t.Fatalf("函数体首语句应为 ReturnStatement, got %T", fn.Body.Statements[0])
	}
	if _, ok := ret.ReturnValue.(*ast.MetaProperty); !ok {
		t.Fatalf("返回值应为 *ast.MetaProperty, got %T", ret.ReturnValue)
	}
}

// TestMetaPropertyAllowedOnlyInFunction new.target 的合法性由「是否处于非箭头
// 函数体内」决定 (箭头对 Contains 透明, 顶层箭头也非法)。
func TestMetaPropertyAllowedOnlyInFunction(t *testing.T) {
	reject := []string{
		"new.target;",
		"() => new.target;",
		"() => { new.target; };",
		"export default new.target;",
	}
	for _, src := range reject {
		p := New(lexer.New(src))
		p.SetModule(true)
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Errorf("顶层 new.target 应报 SyntaxError: %q", src)
		}
	}

	accept := []string{
		"function f() { return new.target; }",
		"function f() { return () => new.target; }",
		"function f() { return () => { return new.target; }; }",
		"let o = { m() { return new.target; } };",
		"class C { constructor() { this.x = new.target; } }",
		"class C { static m() { return new.target; } }",
		"let g = function* () { return new.target; };",
		"async function h() { return new.target; }",
	}
	for _, src := range accept {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Errorf("函数体内 new.target 不应报错: %q -> %s", src, p.Errors().String())
		}
	}
}

// TestMetaPropertyInvalidTargets new.target 的无效赋值/自增目标与转义
// 关键字形态都是解析期早错。
func TestMetaPropertyInvalidTargets(t *testing.T) {
	bad := []string{
		"function f() { new.target = 1; }",
		"function f() { (new.target) = 1; }",
		"function f() { new.target++; }",
		"function f() { ++(new.target); }",
		`function f() { new.t\u0061rget; }`,
		`function f() { n\u0065w.target; }`,
	}
	for _, src := range bad {
		p := New(lexer.New(src))
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Errorf("应报 SyntaxError: %q", src)
		}
	}
}
