package parser

import (
	"testing"

	"github.com/14752222/Gox/lexer"
)

// ===== 模块 (ModuleItemList) 早期错误 (rTI1PN) =====
//
// 这些是 test262 module-code 下 negative:parse 的用例形态; 之前要么被推到
// 运行期 ($DONOTEVALUATE 抛错被记成 runtime), 要么静默通过。门控在
// SetModuleEarlyErrors / SetModule(true) —— 普通脚本不受影响。

// parseModuleEE 以「模块早错」上下文解析 (不开启恒严格 / 顶层 await)。
func parseModuleEE(src string) *Parser {
	p := New(lexer.New(src))
	p.SetModuleEarlyErrors(true)
	p.ParseProgram()
	return p
}

// TestModuleEarlyErrorsRejected 应报 SyntaxError 的形态。
func TestModuleEarlyErrorsRejected(t *testing.T) {
	cases := []string{
		// 重复 ExportedNames
		"var x; export { x }; export { x };",
		"export default 1; export default 2;",
		"var x, y; export default x; export { y as default };",
		"var x, y; export { x as z }; export * as z from './m.js';",
		"export let x; export { x };",
		"export function f() {} export function* f() {}",
		// 未声明的导出绑定
		"export { unresolvable };",
		"export { Number };",
		// 顶层 lexical 重复 / 与 var 冲突
		"function x() {} function x() {}",
		"function x() {} function* x() {}",
		"async function x() {} async function x() {}",
		"var f; function f() {}",
		// 严格保留字 / 受限绑定名
		"var public;",
		"import { eval } from './m.js';",
		"import { arguments } from './m.js';",
		// 顶层 return / yield
		"return;",
		"yield;",
		// import/export 出现在非顶层位置
		"test262: export default null;",
		"switch(0) { case 1: export default null; }",
		"switch(0) { default: import v from './m.js'; }",
		"if (1) export default 1;",
		"function f() { export default 1; }",
		// export 子句缺终止分号
		"export {} null;",
		"export {} from './m.js' null;",
	}
	for _, src := range cases {
		p := parseModuleEE(src)
		if !p.Errors().HasErrors() {
			t.Errorf("模块早错未拦截: %q", src)
			continue
		}
	}
}

// TestModuleEarlyErrorsAllowsValidForms 合法写法不得被误判。
func TestModuleEarlyErrorsAllowsValidForms(t *testing.T) {
	// 注意: 以下都要求导出/引用的绑定确实存在 (用 fixture 名规避「未声明」规则)。
	cases := []string{
		"export { x as y }; var x;",
		"export default function() {}",
		"export default function f() {}",
		"export default class {}",
		"export default class C {}",
		"export * from './m.js';",
		"export * as ns from './m.js';",
		"export {};",
		"import './m.js';",
		"import * as ns from './m.js'; export { ns };",
		"import { a as b } from './m.js'; export { b };",
		"import d, { a } from './m.js'; export { d, a };",
		"export { a } from './m.js';",
		"export { default } from './m.js';",
		"var x; var x;",                     // var 可重复
		"export { x as arguments }; var x;", // 导出名可为 IdentifierName (仅导入绑定名受限)
		"export { x as eval }; var x;",
		"function f() {} var f2;",    // 不同名
		"function f() { return; }",   // 函数体内 return 合法
		"function* g() { yield 1; }", // generator 内 yield 合法
		"var obj = { await: 1 };",
	}
	for _, src := range cases {
		p := parseModuleEE(src)
		if p.Errors().HasErrors() {
			t.Errorf("合法写法被误判: %q -> %s", src, p.Errors().String())
		}
	}
}

// TestNewImportCallIsParseError `new import(...)` 必须在 parse 期报 SyntaxError
// (ImportCall 不是构造器; spec: NewExpression)。r9fWvc。
func TestNewImportCallIsParseError(t *testing.T) {
	cases := []string{
		"new import('./m.js');",
		"new import('./m.js').prop;",
		"{ new import('./m.js'); }",
		"function f() { new import('./m.js'); }",
		"new import.source('./m.js');",
	}
	for _, src := range cases {
		// 脚本上下文也应报错 (dynamic import 在脚本里也合法)。
		p := New(lexer.New(src))
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Errorf("`new import(...)` 未在 parse 期拦截: %q", src)
		}
	}
	// 合法: 动态 import 本身、括号包裹的 new 目标
	for _, src := range []string{
		"import('./m.js');",
		"new (import('./m.js'));",
		"new Foo();",
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Errorf("合法写法被误判: %q -> %s", src, p.Errors().String())
		}
	}
}

// TestModuleEarlyErrorsDisabledForPlainScript 普通脚本 (未开启 moduleEE) 不受
// 这些检查影响 —— 顶层 return / 重复 function 声明在 sloppy 脚本里合法。
func TestModuleEarlyErrorsDisabledForPlainScript(t *testing.T) {
	for _, src := range []string{
		"return 1;",
		"function x() {} function x() {}",
		"var public;",
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Errorf("脚本被模块早错误伤: %q -> %s", src, p.Errors().String())
		}
	}
}
