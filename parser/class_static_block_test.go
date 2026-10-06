package parser

import (
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// ===== ES2022 静态初始化块 static { ... } 的解析与早错（看板单 rJ56bZ）=====

// parseProgram 解析源码, 返回程序 AST 与解析器。
func parseProgram(t *testing.T, src string) (*ast.Program, *Parser) {
	t.Helper()
	p := New(lexer.New(src))
	prog := p.ParseProgram()
	return prog, p
}

// firstClassStatics 解析源码并取出第一个 class 的静态成员列表。
func firstClassStatics(t *testing.T, src string) []*ast.ClassMethod {
	t.Helper()
	prog, p := parseProgram(t, src)
	if p.Errors().HasErrors() {
		t.Fatalf("期望解析成功, 实际报错: %s", p.Errors().String())
	}
	if len(prog.Statements) == 0 {
		t.Fatal("没有语句")
	}
	cls, ok := prog.Statements[0].(*ast.ClassDeclaration)
	if !ok {
		t.Fatalf("第一条语句应为 class 声明, got %T", prog.Statements[0])
	}
	return cls.Statics
}

// TestStaticBlockParses 基本形状: static { ... } 进 Statics, 标记 IsStaticBlock,
// 与静态字段/方法保序。
func TestStaticBlockParses(t *testing.T) {
	statics := firstClassStatics(t, `class C { static a = 1; static { this.z = 7 } static m(){} }`)
	if len(statics) != 3 {
		t.Fatalf("期望 3 个静态成员, got %d", len(statics))
	}
	if !statics[1].IsStaticBlock || statics[1].Body == nil {
		t.Fatalf("第 2 个静态成员应为 static block, got IsStaticBlock=%v Body=%v",
			statics[1].IsStaticBlock, statics[1].Body)
	}
	if statics[0].IsStaticBlock || statics[2].IsStaticBlock {
		t.Fatal("首/尾静态成员不应被误标为 static block")
	}
}

// TestStaticBlockEmptySingleLine 空块与单行形式（statement-list-optional）。
func TestStaticBlockEmptySingleLine(t *testing.T) {
	statics := firstClassStatics(t, `class C { static {} }`)
	if len(statics) != 1 || !statics[0].IsStaticBlock {
		t.Fatalf("空静态块解析失败: %+v", statics)
	}
	if statics[0].Body == nil || len(statics[0].Body.Statements) != 0 {
		t.Fatalf("空块体应无语句, got %+v", statics[0].Body)
	}
}

// TestStaticBlockClassExpression class 表达式同样支持。
func TestStaticBlockClassExpression(t *testing.T) {
	prog, p := parseProgram(t, `var C = class { static { this.x = 1 } };`)
	if p.Errors().HasErrors() {
		t.Fatalf("class 表达式里的静态块应可解析: %s", p.Errors().String())
	}
	varExpr, ok := prog.Statements[0].(*ast.VarStatement)
	if !ok {
		t.Fatalf("首条语句应为 var 声明, got %T", prog.Statements[0])
	}
	ce, ok := varExpr.Value.(*ast.ClassExpression)
	if !ok {
		t.Fatalf("右值应为 class 表达式, got %T", varExpr.Value)
	}
	if len(ce.Statics) != 1 || !ce.Statics[0].IsStaticBlock {
		t.Fatalf("class 表达式的静态块未进 Statics: %+v", ce.Statics)
	}
}

// TestStaticBlockEarlyErrors 负例矩阵: 上下文敏感早错必须报 SyntaxError。
func TestStaticBlockEarlyErrors(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"return", `class C { static { return; } }`},
		{"yield", `function* g() { class C { static { yield; } } }`},
		{"await", `async function f() { class C { static { await 0; } } }`},
		{"super-call", `class C extends Object { static { super(); } }`},
		{"arguments", `class C { static { arguments; } }`},
		{"dup-label", `class C { static { x: x: 0; } }`},
		{"lex-dup", `class C { static { let x; let x; } }`},
		{"lex-var", `class C { static { let x; var x; } }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, p := parseProgram(t, tc.src)
			if !p.Errors().HasErrors() {
				t.Fatalf("期望早错, 实际解析成功")
			}
		})
	}
}

// TestStaticBlockBoundaries 跨函数/类边界不应误报（早错只在静态块自身上下文判定）。
func TestStaticBlockBoundaries(t *testing.T) {
	ok := []string{
		// 箭头函数: return 是箭头自己的; 普通函数体完全是边界。
		`class C { static { var f = () => { return 1; }; f(); } }`,
		`class C { static { function f() { return 1; } f(); } }`,
		// 内层函数的 arguments 是自己的, 不触发 ContainsArguments。
		`class C { static { (function(){ return arguments; })(); } }`,
		// 对象字面量的非计算键名不是标识符引用。
		`class C { static { var o = { arguments: 1 }; } }`,
		// super.x 属性访问合法, 只有 super() 调用才早错。
		`class C extends Object { static { var f = () => super.toString; } }`,
	}
	for _, src := range ok {
		_, p := parseProgram(t, src)
		if p.Errors().HasErrors() {
			t.Fatalf("不应报错但失败了 (%s): %s", src, p.Errors().String())
		}
	}
}
