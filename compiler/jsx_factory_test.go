package compiler

import (
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
)

// ===== JSX 缺省工厂 (h) 的自动补齐 =====
//
// JSX 在 parser 层降级成 h(...) 调用, 所以"用了 JSX 却没绑定 h"曾经是
// 编译通过、挂载才 ReferenceError: h is not defined 的一类故障。Compile 现在
// 按需补一条 `import { h } from "gx/gfx"`; 这里钉住"补"与"不补"的分界,
// 端到端行为 (窗口真的起得来) 由 gfx 包的 TestJSXWithoutImportHMounts 覆盖。

// compiledConstants 收集常量池里的字符串常量 —— 补进来的那条 import 的模块路径
// 是常量池里唯一能观察到它的地方。
func compiledConstants(t *testing.T, input string) []string {
	t.Helper()
	c := compile(t, input)
	out := []string{}
	for i := 0; i < c.Constants().Len(); i++ {
		if s, ok := c.Constants().Get(uint16(i)).(*object.String); ok {
			out = append(out, s.Value)
		}
	}
	return out
}

func hasConstant(consts []string, want string) bool {
	for _, s := range consts {
		if s == want {
			return true
		}
	}
	return false
}

// TestJSXAutoImportsFactoryWhenUnbound 用了小写标签 JSX 而本文件没有 h
// → 自动补 `import { h } from "gx/gfx"`。
func TestJSXAutoImportsFactoryWhenUnbound(t *testing.T) {
	consts := compiledConstants(t, `const ui = <column gap={8}><text>hi</text></column>;`)
	if !hasConstant(consts, jsxFactoryModule) {
		t.Fatalf("缺 h 时应当自动补 import { h } from %q, 常量池里没有它: %v", jsxFactoryModule, consts)
	}
}

// TestJSXKeepsExplicitFactory 本文件自己绑定了 h (任何一种绑定形式)
// → 一个字节都不动: 自定义工厂优先。
func TestJSXKeepsExplicitFactory(t *testing.T) {
	cases := map[string]string{
		"import 命名":   `import { h } from "gox"; const ui = <column />;`,
		"import 默认":   `import h from "./my-h.js"; const ui = <column />;`,
		"import 命名空间": `import * as h from "gox"; const ui = <column />;`,
		"function 声明": `function h(tag, props) { return null; } const ui = <column />;`,
		"const 声明":    `const h = (tag, props) => null; const ui = <column />;`,
		"解构声明":        `const { h } = { h: 1 }; const ui = <column />;`,
		"数组解构":        `const [h] = [1]; const ui = <column />;`,
		"class 声明":    `class h {} const ui = <column />;`,
	}
	for name, src := range cases {
		if hasConstant(compiledConstants(t, src), jsxFactoryModule) {
			t.Fatalf("%s: 本文件已绑定 h, 不该自动补工厂导入", name)
		}
	}
}

// TestJSXComponentOnlyTagsDoNotNeedFactory 纯组件标签 (<Comp/>) 走的是标识符
// 调用, 用不到 h → 不该补。
func TestJSXComponentOnlyTagsDoNotNeedFactory(t *testing.T) {
	consts := compiledConstants(t, `function Comp(props) { return props; } const ui = <Comp show={1} />;`)
	if hasConstant(consts, jsxFactoryModule) {
		t.Fatalf("只有大写标签时不该补工厂导入: %v", consts)
	}
}

// TestJSXProgramFlagSetByParser ast.Program.UsesJSX 是"补不补"的唯一判据,
// 它由 parser 设置: 小写标签置位, 只有大写标签 / 没有 JSX 都不置位。
func TestJSXProgramFlagSetByParser(t *testing.T) {
	cases := map[string]bool{
		`const ui = <column><text>hi</text></column>;`: true,
		`const ui = <Comp />;`:                         false,
		`const x = 1 + 1;`:                             false,
	}
	for src, want := range cases {
		p := parser.New(lexer.New(src))
		program := p.ParseProgram()
		if p.Errors().HasErrors() {
			t.Fatalf("parser errors: %s", p.Errors().String())
		}
		if program.UsesJSX != want {
			t.Fatalf("%s: UsesJSX = %v, want %v", src, program.UsesJSX, want)
		}
	}
}

// TestJSXAutoImportSurvivesFullPipeline 补进来的那条 import 必须真的能过编译
// (别是"补了一条语法上通不过的语句"): 带指令的 JSX 也走一遍。
func TestJSXAutoImportSurvivesFullPipeline(t *testing.T) {
	src := `const ui = <view each={() => [1]} show={() => true}>{() => <text>hi</text>}</view>;`
	if err := compileErr(t, src); err != nil {
		t.Fatalf("带元素级指令的 JSX 也应能自动补工厂并编译通过: %v", err)
	}
}
