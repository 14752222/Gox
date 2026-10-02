package parser

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// firstExport 解析一段源码并返回第一条导出声明 (有解析错误即失败)。
func firstExport(t *testing.T, src string) *ast.ExportDeclaration {
	t.Helper()
	p := New(lexer.New(src))
	prog := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("%q: 解析报错: %s", src, p.Errors().String())
	}
	if len(prog.Statements) == 0 {
		t.Fatalf("%q: 没有语句", src)
	}
	ed, ok := prog.Statements[0].(*ast.ExportDeclaration)
	if !ok {
		t.Fatalf("%q: 首语句不是 ExportDeclaration, 是 %T", src, prog.Statements[0])
	}
	return ed
}

// TestParseExportDeclForms export 后的各类声明均被接受 (此前多会
// "unexpected token after export")。
func TestParseExportDeclForms(t *testing.T) {
	srcs := []string{
		`export var v = 9;`,
		`export var a = 1, b;`,
		`export let x;`,
		`export const c = 2;`,
		`export const { p, q } = o;`,
		`export function f() {}`,
		`export function* g() {}`,
		`export async function h() {}`,
		`export async function* ah() {}`,
		`export class C {}`,
		`export class D extends B {}`,
	}
	for _, src := range srcs {
		firstExport(t, src)
	}
}

// TestParseExportDefaultForms export default 的表达式/具名/匿名形式。
func TestParseExportDefaultForms(t *testing.T) {
	named := firstExport(t, `export default function f() { return 1; }`)
	if !named.IsDefault {
		t.Fatal("应标记 IsDefault")
	}
	if _, ok := named.Declaration.(*ast.FunctionDeclaration); !ok {
		t.Fatalf("具名默认函数应是 FunctionDeclaration, got %T", named.Declaration)
	}

	anon := firstExport(t, `export default function () { return 1; }`)
	if _, ok := anon.Declaration.(*ast.ExpressionStatement); !ok {
		t.Fatalf("匿名默认函数应是表达式语句, got %T", anon.Declaration)
	}

	cls := firstExport(t, `export default class C {}`)
	if _, ok := cls.Declaration.(*ast.ClassDeclaration); !ok {
		t.Fatalf("具名默认类应是 ClassDeclaration, got %T", cls.Declaration)
	}

	expr := firstExport(t, `export default 7;`)
	if _, ok := expr.Declaration.(*ast.ExpressionStatement); !ok {
		t.Fatalf("默认表达式应是表达式语句, got %T", expr.Declaration)
	}
}

// TestParseExportSpecifiers 命名导出/再导出/星号导出/空导出。
func TestParseExportSpecifiers(t *testing.T) {
	star := firstExport(t, `export * from "./a.js";`)
	if !star.IsStar || star.Source != "./a.js" {
		t.Fatalf("星号再导出解析错误: %+v", star)
	}

	ns := firstExport(t, `export * as ns from "./a.js";`)
	if len(ns.Specifiers) != 1 || ns.Specifiers[0].Local != "*" || ns.Specifiers[0].Exported != "ns" {
		t.Fatalf("命名空间再导出解析错误: %+v", ns.Specifiers)
	}
	if ns.Source != "./a.js" {
		t.Fatalf("命名空间再导出的 Source 丢了: %q", ns.Source)
	}

	named := firstExport(t, `export { f as ff } from "./b.js";`)
	if len(named.Specifiers) != 1 || named.Specifiers[0].Local != "f" || named.Specifiers[0].Exported != "ff" {
		t.Fatalf("具名再导出别名解析错误: %+v", named.Specifiers)
	}

	local := firstExport(t, `export { a, b as c };`)
	if len(local.Specifiers) != 2 || local.Specifiers[1].Local != "b" || local.Specifiers[1].Exported != "c" {
		t.Fatalf("本地命名导出解析错误: %+v", local.Specifiers)
	}
	if local.Source != "" {
		t.Fatalf("无 from 的本地导出不应有 Source: %q", local.Source)
	}

	empty := firstExport(t, `export {};`)
	if len(empty.Specifiers) != 0 || empty.Source != "" {
		t.Fatalf("空导出解析错误: %+v", empty)
	}

	// default 作为导出名 (default 是专门的 token, 不是 identifier)
	defRe := firstExport(t, `export { default } from "./m.js";`)
	if len(defRe.Specifiers) != 1 || defRe.Specifiers[0].Local != "default" || defRe.Specifiers[0].Exported != "default" {
		t.Fatalf("default 再导出解析错误: %+v", defRe.Specifiers)
	}
	asDef := firstExport(t, `export { a as default };`)
	if len(asDef.Specifiers) != 1 || asDef.Specifiers[0].Exported != "default" {
		t.Fatalf("a as default 解析错误: %+v", asDef.Specifiers)
	}
}

// TestParseExportUnexpectedTokenHasPosition 非法形式要报准确行列, 而不是笼统
// "unexpected token"。
func TestParseExportUnexpectedTokenHasPosition(t *testing.T) {
	// export 42 → 数字不能作为声明开始
	src := "export 42;"
	p := New(lexer.New(src))
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatal("`export 42` 应该报错")
	}
	msg := p.Errors().Errors[0].Error()
	if !strings.Contains(msg, "line 1:8") {
		t.Fatalf("错误应带准确行列 (1:8), got %q", msg)
	}
	if !strings.Contains(msg, "after export") {
		t.Fatalf("错误应说明 export 后跟了什么, got %q", msg)
	}
}

// TestParseExportSpecifierMissingComma 缺分隔符 `{ a b }` 是语法错误, 且定位到 b。
func TestParseExportSpecifierMissingComma(t *testing.T) {
	src := "export {\n  a b\n};\n"
	p := New(lexer.New(src))
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatal("`{ a b }` 应报错")
	}
	msg := p.Errors().Errors[0].Error()
	if !strings.Contains(msg, "line 2:5") {
		t.Fatalf("错误应定位到 b (2:5), got %q", msg)
	}
}

// TestParseExportStarMissingFrom `export *` 缺 from 要报错且定位。
func TestParseExportStarMissingFrom(t *testing.T) {
	p := New(lexer.New("export *\n"))
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatal("`export *` 应报错")
	}
	if !strings.Contains(p.Errors().Errors[0].Error(), "from") {
		t.Fatalf("错误应提到 from: %q", p.Errors().Errors[0].Error())
	}
}
