package parser

import (
	"testing"

	"github.com/14752222/Gox/lexer"
)

// ===== sloppy 非生成器代码里 yield 作普通标识符 (rmg9Qy, 2026-10-07) =====
//
// 规范: yield 是保留字**仅在** ① 生成器/async-generator 函数体 (含形参, 即
// [+Yield] 上下文)、② 严格模式代码、③ 模块代码 (模块恒严格)。其余 sloppy
// 代码里 yield 是合法 IdentifierReference / BindingIdentifier。
// (test262 language/{expressions,statements}/**/yield-identifier-non-strict.js、
// dstr/**/yield-ident-valid.js、global-code/yield-non-strict.js 一族。)
//
// 本文件覆盖四条边界: sloppy 合法 / 生成器体·形参报错 / 严格报错 / obj.yield
// 属性名不受影响 (属性名位置恒为 IdentifierName, 与保留字无关)。

// TestYieldAsIdentifierSloppyValid sloppy 非生成器里 yield 作绑定名/引用名的
// 全部常见形态都应通过解析。
func TestYieldAsIdentifierSloppyValid(t *testing.T) {
	valid := []string{
		`var yield = 1;`,
		`var yield;`,
		`let yield = 1;`,
		`const yield = 1;`,
		`yield = 2;`,
		`yield += 1;`,
		`yield++;`,
		`yield+1;`,
		`yield(1);`,
		`yield[0];`,
		`yield instanceof Object;`,
		`function f(yield) { return yield; }`,
		`(function (yield) { return yield; });`,
		`(yield) => yield;`,
		`function f() { var yield = 1; return yield; }`,
		`function* g() { function f(yield) { return yield; } }`,
		`function* g() { function f() { var x = yield; } }`, // 嵌套非生成器函数体 reset 为 ~Yield
		`for (var yield of [1, 2]) {}`,
		`for (let yield of [1, 2]) {}`,
		`for (yield of [1, 2]) {}`,
		`{ let yield = 1; }`,
		`var o = { yield: 1 };`,  // 属性名 (带冒号) 恒合法
		`var o = { yield: 1 }.yield;`,
		`var yield = 1; ({ yield });`, // sloppy shorthand 合法
	}
	for _, src := range valid {
		t.Run(src, func(t *testing.T) {
			_, ok := parseSrc(t, src)
			if !ok {
				t.Errorf("sloppy 下应合法, 但报了语法错误\nsrc: %s", src)
			}
		})
	}
}

// TestYieldAsIdentifierGeneratorError 生成器/async-generator 体与其形参里
// yield 是保留字 —— 作绑定名/标识符引用/形参名/默认值/标签名皆 SyntaxError。
func TestYieldAsIdentifierGeneratorError(t *testing.T) {
	invalid := []string{
		// 生成器体内作绑定名 / 引用名
		`function* g() { var yield = 1; }`,
		`function* g() { yield = 1; }`,
		`(function* () { let yield = 1; });`,
		// 生成器形参 [+Yield] (FormalParameters[+Yield])
		`function* g(yield) {}`,
		`function* g(x = yield) {}`,
		`(function* (yield) {});`,
		`({ *m(yield) {} });`,
		`({ async *m(yield) {} });`,
		`class C { *m(yield) {} }`,
		`class C { async *m(yield) {} }`,
		// 生成器体内 yield 作标签名 (allowYield 语境)
		`function* g() { yield: ; }`,
	}
	for _, src := range invalid {
		t.Run(src, func(t *testing.T) {
			_, ok := parseSrc(t, src)
			if ok {
				t.Errorf("生成器语境下应报 SyntaxError, 但静默通过\nsrc: %s", src)
			}
		})
	}
}

// TestYieldAsIdentifierStrictError 严格模式代码里 yield 是保留字。
func TestYieldAsIdentifierStrictError(t *testing.T) {
	invalid := []string{
		`"use strict"; var yield = 1;`,
		`"use strict"; let yield = 1;`,
		`"use strict"; function f(yield) {}`,
		`function f() { "use strict"; var yield = 1; }`,
		`"use strict"; var yield = 1; ({ yield });`, // 严格 shorthand 早错
		// 模块顶层恒严格
	}
	for _, src := range invalid {
		t.Run(src, func(t *testing.T) {
			_, ok := parseSrc(t, src)
			if ok {
				t.Errorf("严格模式下应报 SyntaxError, 但静默通过\nsrc: %s", src)
			}
		})
	}
	// 模块: SetModule(true) 恒严格, yield 作绑定名应报错。
	t.Run("module-top-level-binding", func(t *testing.T) {
		p := New(lexer.New(`var yield = 1;`))
		p.SetModule(true)
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Error("模块里 var yield = 1 应报 SyntaxError")
		}
	})
}

// TestYieldAsPropertyNameValid obj.yield / {yield: v} 的属性名位置恒为
// IdentifierName, 与保留字无关, 任何上下文都合法 (含生成器体内)。
func TestYieldAsPropertyNameValid(t *testing.T) {
	valid := []string{
		`var o = {}; o.yield = 1;`,
		`var o = { yield: 1 };`,
		`var o = { yield: 1 }.yield;`,
		`function* g() { return { yield: 1 }.yield; }`, // 生成器体内属性名仍合法
		`"use strict"; var o = { yield: 1 }.yield;`,   // 严格下属性名仍合法
		`({ yield: function () {} });`,
	}
	for _, src := range valid {
		t.Run(src, func(t *testing.T) {
			_, ok := parseSrc(t, src)
			if !ok {
				t.Errorf("属性名位置的 yield 应合法, 但报了语法错误\nsrc: %s", src)
			}
		})
	}
}

// TestYieldIdentifierShorthandStrictError `{ yield }` 简写的键同时是
// IdentifierReference —— 严格模式代码里是早错 (test262
// object/identifier-shorthand-yield-invalid-strict-mode.js), sloppy 合法。
func TestYieldIdentifierShorthandStrictError(t *testing.T) {
	if _, ok := parseSrc(t, `var yield = 1; ({ yield });`); !ok {
		t.Error("sloppy 下 { yield } 简写应合法")
	}
	src := `var yield = 1; (function () { "use strict"; ({ yield }); });`
	if _, ok := parseSrc(t, src); ok {
		t.Error("严格模式代码里 { yield } 简写应报 SyntaxError")
	}
}
