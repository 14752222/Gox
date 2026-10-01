package vm

import (
	"path/filepath"
	"testing"
)

// ===== 命名导入别名 (`import { x as y }`) 的端到端行为 =====
//
// 编译期断言在 compiler/import_alias_test.go; 这里钉的是**运行时** ——
// 老 bug 的症状是"编译通过、运行时静默 undefined", 只有真跑一遍才抓得住。
//
//	import { x as y } from "./m.js";   →  y 恒为 undefined (不报错)
//
// 根因: parser 把 `as` 当普通标识符收进 []string, `{ x as y }` 变成三个
// 独立"名字" [x, as, y] —— 实际声明 x / as / y 三个绑定, 而 y 取的是模块上
// 并不存在的 "y" 属性。命名空间导入 (`* as ns`) 不受影响, 所以它当年是好的。

// TestImportAliasBindsValue 别名要真的拿到模块导出的值。
func TestImportAliasBindsValue(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const x = 42;
export function hello() { return "hi"; }`,
		"entry.js": `import { x as y } from "./m.js";
y`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("别名导入不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 42)
}

// TestImportAliasBindsFunction 别名的函数也要可调用 (不只是取到 undefined)。
func TestImportAliasBindsFunction(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export function hello() { return "hi"; }`,
		"entry.js": `import { hello as greet } from "./m.js";
greet()`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("别名导入函数不该报错: %v", err)
	}
	assertString(t, vm.LastPopped(), "hi")
}

// TestImportAliasDoesNotLeakOriginalName 别名之后, **模块导出名不该被绑进本文件** ——
// 老 bug 的形态恰恰是 x / as / y 三个全被声明。这里用 typeof 探针:
// 原名应当是 undefined (未声明), 别名才有值。
func TestImportAliasDoesNotLeakOriginalName(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const x = 42;`,
		"entry.js": `import { x as y } from "./m.js";
typeof x`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	// 模块作用域内 `x` 未被声明 ⇒ typeof 为 "undefined"
	assertString(t, vm.LastPopped(), "undefined")
}

// TestImportAliasMixedWithPlainAndDefault 别名与普通命名导入 / 默认导出混用。
func TestImportAliasMixedWithPlainAndDefault(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export default "D";
export const a = 1;
export const b = 2;`,
		"entry.js": `import def, { a, b as bee } from "./m.js";
def + a + bee`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("混合导入不该报错: %v", err)
	}
	// "D" + 1 + 2 → 字符串拼接
	assertString(t, vm.LastPopped(), "D12")
}

// TestImportAliasRenameToAnotherExportName 别名可以撞上另一个真实导出名 ——
// 这是 `import { createSignal as createEffect }` 的形态, 曾经被内置校验误报。
func TestImportAliasRenameToAnotherExportName(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const a = 1;
export const b = 2;`,
		"entry.js": `import { a as b } from "./m.js";
b`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("别名撞名不该报错: %v", err)
	}
	// 必须拿到模块的 a (值 1), 而不是模块的 b (值 2)
	assertNumber(t, vm.LastPopped(), 1)
}

// TestImportAliasSameNameAsPlain 同名字别名 `{ a as a }` 等价于 `{ a }`。
func TestImportAliasSameNameAsPlain(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const a = 7;`,
		"entry.js": `import { a as a } from "./m.js";
a`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("同名字别名不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 7)
}

// TestImportNamespaceStillWorks 回归防线: 命名空间导入照旧 (当年唯一能用的形式)。
func TestImportNamespaceStillWorks(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const x = 42;`,
		"entry.js": `import * as ns from "./m.js";
ns.x`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("命名空间导入不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 42)
}

// TestImportAliasAcrossModules 别名在"被导入的模块内部"同样生效 ——
// 确保修的是编译管线而不是入口文件特例。
func TestImportAliasAcrossModules(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"base.js": `export const v = 5;`,
		"mid.js": `import { v as base } from "./base.js";
export const doubled = base * 2;`,
		"entry.js": `import { doubled as d } from "./mid.js";
d`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("跨模块别名不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 10)
}
