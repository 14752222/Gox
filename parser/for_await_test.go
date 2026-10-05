package parser

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// for await...of / 裸 await / 无声明 for-of 头部的 parser 层测试。
//
// 基于 3542fbb 的增量 (for-await 主路径已落地 main)。口径全部来自
// node v22 实测 (2026-10-05):
//   - for await 头部只接受 of: (…in…) 与带初始化器的声明都是 SyntaxError;
//   - for await (async of xs) 合法 (for-await 头部没有 async-of 前瞻限制),
//     而同步 for (async of xs) 是 SyntaxError;
//   - for (await x of y) 同步/异步都是 SyntaxError, for (await of y) 同步合法
//     (await 作普通标识符绑定名);
//   - 非 async 上下文里 await 是普通标识符 (var await = 1 / await(1) /
//     await - 1 都合法), 只有 await 后跟操作数才是 await 表达式 → SyntaxError。

// parseAsyncFnBody 解析 async function f(){ … } 并返回函数体语句。
func parseAsyncFnBody(t *testing.T, src string) []ast.Statement {
	t.Helper()
	p := New(lexer.New(src))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	if len(program.Statements) != 1 {
		t.Fatalf("%q: 语句数 = %d, want 1", src, len(program.Statements))
	}
	fn, ok := program.Statements[0].(*ast.FunctionDeclaration)
	if !ok || !fn.IsAsync {
		t.Fatalf("%q: 期望 async 函数声明, got %T", src, program.Statements[0])
	}
	return fn.Body.Statements
}

// parseForAwaitOf 解析包在 async function 里的 for await 语句。
func parseForAwaitOf(t *testing.T, src string) *ast.ForOfStatement {
	t.Helper()
	stmts := parseAsyncFnBody(t, "async function f(){ "+src+" }")
	if len(stmts) != 1 {
		t.Fatalf("%q: 函数体语句数 = %d, want 1", src, len(stmts))
	}
	forOf, ok := stmts[0].(*ast.ForOfStatement)
	if !ok {
		t.Fatalf("%q: 期望 ForOfStatement, got %T", src, stmts[0])
	}
	return forOf
}

// parseForOf 解析顶层同步 for-of 语句。
func parseForOf(t *testing.T, src string) *ast.ForOfStatement {
	t.Helper()
	p := New(lexer.New(src))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	if len(program.Statements) != 1 {
		t.Fatalf("%q: 语句数 = %d, want 1", src, len(program.Statements))
	}
	forOf, ok := program.Statements[0].(*ast.ForOfStatement)
	if !ok {
		t.Fatalf("%q: 期望 ForOfStatement, got %T", src, program.Statements[0])
	}
	return forOf
}

// assertError 断言 src 解析报错, 且首条错误信息包含 want 子串。
func assertError(t *testing.T, src, want string) {
	t.Helper()
	p := New(lexer.New(src))
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatalf("%q: 期望报错 (want %q), 但解析通过", src, want)
	}
	got := p.Errors().Errors[0].Message
	if !strings.Contains(got, want) {
		t.Fatalf("%q: 首条错误 = %q, want 包含 %q", src, got, want)
	}
}

// ===== for await / for-of 头部形态 =====

func TestForAwaitHeaderForms(t *testing.T) {
	tests := []struct {
		src   string
		check func(t *testing.T, s *ast.ForOfStatement)
	}{
		// ---- 声明绑定形态 ----
		{
			src: "for await (const x of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Variable == nil || s.Variable.Value != "x" {
					t.Fatalf("期望变量 x, got %+v", s.Variable)
				}
				if _, ok := s.VarDecl.(*ast.ConstStatement); !ok {
					t.Fatalf("期望 ConstStatement, got %T", s.VarDecl)
				}
			},
		},
		{
			src: "for await (var x of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if _, ok := s.VarDecl.(*ast.VarStatement); !ok {
					t.Fatalf("期望 VarStatement, got %T", s.VarDecl)
				}
			},
		},
		{
			src: "for await (const [a, b] of pairs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Pattern == nil {
					t.Fatalf("期望解构 Pattern")
				}
				if got := s.Pattern.String(); got != "[a, b]" {
					t.Fatalf("Pattern.String() = %q, want %q", got, "[a, b]")
				}
			},
		},
		// ---- 无声明 (LHS 赋值目标) 形态 ----
		{
			src: "for await (x of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.VarDecl != nil || s.Pattern != nil {
					t.Fatalf("LHS 形态不应有 VarDecl/Pattern, got %T / %T", s.VarDecl, s.Pattern)
				}
				ident, ok := s.Target.(*ast.Identifier)
				if !ok || ident.Value != "x" {
					t.Fatalf("期望 Target=Identifier(x), got %T", s.Target)
				}
			},
		},
		{
			src: "for await (obj.k of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if _, ok := s.Target.(*ast.MemberExpression); !ok {
					t.Fatalf("期望 Target=MemberExpression, got %T", s.Target)
				}
			},
		},
		{
			src: "for await ([a, b] of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Pattern == nil || s.VarDecl != nil {
					t.Fatalf("解构赋值目标: 期望 Pattern 非 nil 且 VarDecl 为 nil, got %T / %T",
						s.Pattern, s.VarDecl)
				}
			},
		},
		// node 实测: for await (async of xs) 合法 —— async 作绑定名
		{
			src: "for await (async of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				ident, ok := s.Target.(*ast.Identifier)
				if !ok || ident.Value != "async" {
					t.Fatalf("期望 Target=Identifier(async), got %T", s.Target)
				}
			},
		},
		// ---- IsAwait (Await 字段) ----
		{
			src: "for await (x of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if !s.Await {
					t.Fatalf("for await 头部 Await = false, want true")
				}
				if s.TokenLiteral() != "for" {
					t.Fatalf("Token.Literal = %q, want \"for\"", s.TokenLiteral())
				}
			},
		},
	}
	for _, tt := range tests {
		s := parseForAwaitOf(t, tt.src)
		if s.Iterable == nil {
			t.Fatalf("%q: 期望 Iterable", tt.src)
		}
		if s.Body == nil {
			t.Fatalf("%q: 期望 Body", tt.src)
		}
		tt.check(t, s)
	}
}

func TestForOfLHSHeaderForms(t *testing.T) {
	// 同步 for (x of xs) —— 3542fbb 之前的既有缺口, 本次补齐
	tests := []struct {
		src   string
		check func(t *testing.T, s *ast.ForOfStatement)
	}{
		{
			src: "for (x of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Await {
					t.Fatalf("同步 for-of Await = true, want false")
				}
				ident, ok := s.Target.(*ast.Identifier)
				if !ok || ident.Value != "x" {
					t.Fatalf("期望 Target=Identifier(x), got %T", s.Target)
				}
				if s.VarDecl != nil || s.Pattern != nil || s.Variable != nil {
					t.Fatalf("LHS 形态不应有 VarDecl/Pattern/Variable")
				}
			},
		},
		{
			src: "for (obj.k of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if _, ok := s.Target.(*ast.MemberExpression); !ok {
					t.Fatalf("期望 Target=MemberExpression, got %T", s.Target)
				}
			},
		},
		{
			src: "for (m['of'] of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if _, ok := s.Target.(*ast.MemberExpression); !ok {
					t.Fatalf("期望 Target=MemberExpression (m['of']), got %T", s.Target)
				}
			},
		},
		{
			src: "for ([a, b] of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Pattern == nil || s.VarDecl != nil {
					t.Fatalf("解构赋值目标: 期望 Pattern 非 nil 且 VarDecl 为 nil, got %T / %T",
						s.Pattern, s.VarDecl)
				}
			},
		},
		{
			src: "for ({a} of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				if s.Pattern == nil || s.VarDecl != nil {
					t.Fatalf("解构赋值目标: 期望 Pattern 非 nil 且 VarDecl 为 nil, got %T / %T",
						s.Pattern, s.VarDecl)
				}
			},
		},
		// node 实测: 同步 for (await of xs) 合法 —— await 作标识符绑定名
		{
			src: "for (await of xs) {}",
			check: func(t *testing.T, s *ast.ForOfStatement) {
				ident, ok := s.Target.(*ast.Identifier)
				if !ok || ident.Value != "await" {
					t.Fatalf("期望 Target=Identifier(await), got %T", s.Target)
				}
			},
		},
	}
	for _, tt := range tests {
		s := parseForOf(t, tt.src)
		if s.Iterable == nil {
			t.Fatalf("%q: 期望 Iterable", tt.src)
		}
		if s.Body == nil {
			t.Fatalf("%q: 期望 Body", tt.src)
		}
		tt.check(t, s)
	}
}

// TestForAwaitHeaderEarlyErrors: for await 头部只接受 of (parser 层早错,
// 与 3542fbb compiler 的 inAsyncFunction 校验互不重复)。
func TestForAwaitHeaderEarlyErrors(t *testing.T) {
	for _, kw := range []string{"var", "let", "const"} {
		assertError(t,
			"async function f(){ for await ("+kw+" x = 1 of xs) {} }",
			"may not have an initializer")
	}
	// 同样拒绝 for await (var i = 0; ...) 这类传统三段式
	assertError(t,
		"async function f(){ for await (var i = 0; i < 3; i++) {} }",
		"may not have an initializer")
	// in 无异步形态 (node: Unexpected token 'in')
	assertError(t,
		"async function f(){ for await (const x in obj) {} }",
		"'of', not 'in'")
	// 非 of 形态的赋值目标头部
	assertError(t, "async function f(){ for await (x; ;) {} }", "must iterate with 'of'")
}

// TestForOfLHSEarlyErrors: 同步 for-of 头部的赋值目标早错。
func TestForOfLHSEarlyErrors(t *testing.T) {
	// node: The left-hand side of a for-of loop may not be 'async'
	// (同步 for-of 有 async-of 前瞻限制, 只有 for await (async of xs) 合法)
	assertError(t, "for (async of xs) {}", "may not be 'async'")
	// node: await 表达式不能作 for-of 赋值目标 (同步上下文走裸 await 早错)
	assertError(t, "for (await x of xs) {}", "await")
	assertError(t, "async function f(){ for (await x of xs) {} }", "invalid left-hand side")
	// 字面量 / 调用不是可赋值目标 (node: Invalid left-hand side)
	assertError(t, "for (1 of xs) {}", "left-hand side")
	assertError(t, "for (f() of xs) {}", "left-hand side")
	assertError(t, "async function f(){ for await (1 of xs) {} }", "left-hand side")
}

// ===== 裸 await 早错 =====

func TestBareAwaitNotAllowedOutsideAsync(t *testing.T) {
	// node: "await is only valid in async functions and async generators"
	for _, src := range []string{
		"await x;",                  // 顶层 (script 无顶层 await)
		"function f(){ await x; }",  // 同步函数
		"function* g(){ await x; }", // 同步 generator
		"async function f(){ function g(){ await x; } }", // async 里嵌套的同步函数
		"let f = () => { await x; };",                    // 同步箭头
		"let o = { m(){ await x; } };",                   // 同步对象方法
		"class C { m(){ await x; } }",                    // 同步类方法
		"await 1;",                                       // await 字面量
		"let f2 = () => { await 1; };",                   // 括号体的同步箭头
	} {
		assertError(t, src, "await is only valid in async functions")
	}
}

func TestAwaitAllowedInAsyncContexts(t *testing.T) {
	// async 上下文里 await 表达式正常解析 (不报早错)
	for _, src := range []string{
		"async function f(){ await x; }",
		"async function* g(){ await x; }",
		"async () => { await x; };",
		"async x => await x;",
		"let o = { async m(){ await x; } };",
		"class C { async m(){ await x; } }",
		"async function f(){ for await (const x of xs) { await x; } }",
		"async function f(p){ for (let i = await p; i < 3; i++) {} }",
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Fatalf("%q: 不应报错, got: %s", src, p.Errors().String())
		}
	}
}

// TestAwaitIdentifierInSloppyScript: 非 async 上下文里 await 作普通标识符
// (node 实测全部合法, 早错不得误伤)。
func TestAwaitIdentifierInSloppyScript(t *testing.T) {
	for _, src := range []string{
		"var await = 1;",
		"let await = 1;",
		"const await = 1;",
		"var await = 1; await;",
		"var await = 1; await(1);",   // 标识符调用
		"var await = 1; await[0];",   // 标识符索引
		"var await = 1; await - 1;",  // 二元减 (不是 await 表达式)
		"var await = 1; await in o;", // in 二元
		"var await = 1; await++;",
		"var o = { await: 1 };", // 属性名不受影响
		"function f(await){ return await; }",
		// for-of 头部的标识符形态
		"for (await of xs) {}",
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Fatalf("%q: 不应报错, got: %s", src, p.Errors().String())
		}
	}
}

// TestAsyncContextRestoredInNestedSyncFunction: async 上下文按函数体重置 ——
// async 函数里嵌套的同步函数内裸 await 仍是早错。
func TestAsyncContextRestoredInNestedSyncFunction(t *testing.T) {
	assertError(t,
		"async function f(){ function g(){ await x; } }",
		"await is only valid in async functions")
	// 反向: 同步函数里嵌套 async 函数, await 在内层合法
	p := New(lexer.New("function f(){ async function g(){ await x; } }"))
	p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("嵌套 async 函数内的 await 不应报错: %s", p.Errors().String())
	}
}

// TestForOfLHSDoesNotSwallowTraditionalFor 钉住传统 for 不被 LHS 形态误判:
// 初始化器里出现 .of 属性访问时, 深度 0 的 OF 前一个是 DOT, 必须落回三段式。
func TestForOfLHSDoesNotSwallowTraditionalFor(t *testing.T) {
	src := "for (i = 0; i < a.of.length; i++) {}"
	p := New(lexer.New(src))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	if len(program.Statements) != 1 {
		t.Fatalf("语句数 = %d, want 1", len(program.Statements))
	}
	if _, ok := program.Statements[0].(*ast.ForStatement); !ok {
		t.Fatalf("期望 ForStatement (传统三段式), got %T", program.Statements[0])
	}
}

// TestAsyncIdentifierFallback 钉住 async 非保留字的另一面 (node 22 实测):
// async 后面不跟操作数/函数形状时是普通标识符引用 —— let async 绑定、
// async 标识符引用、async = 5 赋值都合法; async 42 仍报错。
// (test262 for-await-of/head-lhs-async.js 的前置: let async; … console.log(async))
func TestAsyncIdentifierFallback(t *testing.T) {
	for _, src := range []string{
		"let async = 1;",
		"let async; async = 5;",
		"function f(x){ return x + async; }",
		"var async; console.log(async);",
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Fatalf("%q: 不应报错: %s", src, p.Errors().String())
		}
	}
	p := New(lexer.New("let f = async 42;"))
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatal(`"let f = async 42;" 应报错`)
	}
}

// TestForAwaitHeadAsyncBinding 钉住 for await 头部 async 作赋值目标
// (test262 head-lhs-async.js): let async; for await (async of [7]) 合法,
// 每轮把值赋给外部绑定 —— Target 应是 Identifier{async} 而非 async 表达式。
func TestForAwaitHeadAsyncBinding(t *testing.T) {
	src := "let async; async function fn(){ for await (async of [7]); }"
	p := New(lexer.New(src))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	fn := program.Statements[1].(*ast.FunctionDeclaration)
	loop := fn.Body.Statements[0].(*ast.ForOfStatement)
	if !loop.Await {
		t.Fatal("应为 for await 循环")
	}
	ident, ok := loop.Target.(*ast.Identifier)
	if !ok || ident.Value != "async" {
		t.Fatalf("Target = %T %+v, want Identifier{async}", loop.Target, loop.Target)
	}
}

// TestLetBlockWithNewlineASI 钉住 `let` + 换行 + `{` 的 ASI 形态
// (test262 let-block-with-newline.js): let 声明要求绑定与 let 同行,
// 换行后 `let` 是标识符表达式语句, `{}` 是后续独立块语句。
func TestLetBlockWithNewlineASI(t *testing.T) {
	src := "async function* f(){ for await (var x of []) let \n {} }"
	p := New(lexer.New(src))
	p.ParseProgram()
	checkParserErrors(t, p)
}
