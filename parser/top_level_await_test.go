package parser

import (
	"testing"

	"github.com/14752222/Gox/lexer"
)

// ===== 顶层 await (Top-Level Await, TLA) 的解析 =====
//
// 规范: ModuleItem 的语法参数带 +Await, 所以模块顶层的 `await expr` 与
// `for await (...)` 头部合法; 而 script 顶层是 ~Await (`await` 只是普通
// 标识符)。这个 +Await 只作用于顶层, 不向函数体传播 (函数体各自按自身
// async 与否重置, 见 setAllowAwait)。
//
// 门控在 Parser.module (SetModule(true)): compileSource(src, true) 用于被
// import 的模块; 入口脚本 compileSource(src, false) 不受影响。

// parseModule 以模块模式解析源码, 返回 parser 供断言错误。
func parseModule(t *testing.T, src string) *Parser {
	t.Helper()
	p := New(lexer.New(src))
	p.SetModule(true)
	p.ParseProgram()
	return p
}

// TestTopLevelAwaitParsesInModule 模块顶层 await 表达式 / 声明 / for-await
// 都应解析通过。
func TestTopLevelAwaitParsesInModule(t *testing.T) {
	for _, src := range []string{
		"await 1;",
		"const x = await 1;",
		"export const x = await Promise.resolve(1);",
		"x = await 42; await x;",
		"if (true) { await 1; }",
		"try { await 1; } catch (e) {}",
		"for await (const x of xs) {}",
		"for await (x of xs) {}",
		"export class C extends (await fn()) {}",
		"await import('m');",
	} {
		p := parseModule(t, src)
		if p.Errors().HasErrors() {
			t.Fatalf("模块顶层 %q 应解析通过, got: %s", src, p.Errors().String())
		}
	}
}

// TestTopLevelAwaitNoOperandIsError 模块顶层裸 `await;` 缺操作数 → 解析错
// (test262 top-level-await/no-operand.js)。
func TestTopLevelAwaitNoOperandIsError(t *testing.T) {
	p := parseModule(t, "await;")
	if !p.Errors().HasErrors() {
		t.Fatal("模块顶层 `await;` 应报错")
	}
}

// TestTopLevelAwaitDoesNotPropagateToFunctionBodies 顶层 +Await 不向函数体
// 传播: 模块里的同步函数体/形参里的裸 await 仍是早错
// (test262 top-level-await/syntax/early-does-not-propagate-*).
func TestTopLevelAwaitDoesNotPropagateToFunctionBodies(t *testing.T) {
	for _, src := range []string{
		"function fn() { await 0; }",
		"function fn(a = await 0) {}",
		"const f = function() { await 0; };",
		"const f = function(a = await 0) {};",
		"const f = () => { await 0; };",
		"const o = { m() { await 0; } };",
		"class C { m() { await 0; } }",
	} {
		p := parseModule(t, src)
		if !p.Errors().HasErrors() {
			t.Fatalf("模块内非 async 函数里的 %q 应报错", src)
		}
	}
}

// TestTopLevelAwaitNestedAsyncStillAllowed 模块里嵌套的 async 函数内 await
// 照常合法 (TLA 的开启不影响既有 async 判定)。
func TestTopLevelAwaitNestedAsyncStillAllowed(t *testing.T) {
	for _, src := range []string{
		"async function f() { await 0; }",
		"const f = async () => await 0;",
		"class C { async m() { await 0; } }",
	} {
		p := parseModule(t, src)
		if p.Errors().HasErrors() {
			t.Fatalf("模块内 %q 应合法, got: %s", src, p.Errors().String())
		}
	}
}

// TestTopLevelAwaitScriptStillRejected script 顶层 (module=false) 的 await
// 形态不变 —— TLA 不得泄漏到脚本。
func TestTopLevelAwaitScriptStillRejected(t *testing.T) {
	for _, src := range []string{
		"await 1;",
		"const x = await Promise.resolve(1);",
	} {
		assertError(t, src, "await is only valid in async functions")
	}
	// 非操作数形态仍是标识符用法 (sloppy script)
	for _, src := range []string{"var await = 1; await;", "var await = 1; await(1);"} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Fatalf("script %q 应合法: %s", src, p.Errors().String())
		}
	}
}
