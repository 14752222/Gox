package vm

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== ESM 导出声明面 / 语义 端到端测试 =====
//
// 本文件钉住 M2 (TS 一等公民) 与 M5 (npm 纯 JS 包可用) 两条出口共同依赖的
// 导出能力。逐条对应需求表:
//
//	export let/const/var          ✅ 多 declarator / 解构 / 无初值
//	export function / function*   ✅
//	export async function / async function*  ✅
//	export class [extends]        ✅
//	export default 表达式/具名/匿名 function/class ✅ (具名只在模块内可见)
//	export * from                 ✅ default 不转发、本地同名不被覆盖
//	export * as ns from           ✅
//	export { a as b } from        ✅ 取值时读取源模块导出槽 (活绑定等效语义)
//	export {}                     ✅
//
// 说明: 未加 from 的本地 `export { a }` / `export let a` 在模块模式下是**活绑定**
// (OP_EXPORT_BINDING 记录槽位, 读时取值), 见 TestExportLocalLiveBinding。

// esmFixture 拼出 testdata/esm 下夹具的路径。
func esmFixture(parts ...string) string {
	return filepath.Join(append([]string{"..", "testdata", "esm"}, parts...)...)
}

// runESMFixture 执行一个夹具入口, 返回最后一条表达式语句的值 (出错即失败)。
func runESMFixture(t *testing.T, parts ...string) object.Value {
	t.Helper()
	v, err := EvalFile(esmFixture(parts...))
	if err != nil {
		t.Fatalf("%s: %v", filepath.Join(parts...), err)
	}
	return v
}

func inspect(v object.Value) string {
	if v == nil {
		return "<nil>"
	}
	return v.Inspect()
}

// --- 声明形式 ---

// TestExportDeclForms 覆盖 export var/let/const/class/async function。
func TestExportDeclForms(t *testing.T) {
	v := runESMFixture(t, "decl_forms.js")
	assertNumber(t, v, 15)
}

// TestExportAsyncFunctionAndGenerator 异步函数/异步生成器导出后可 import。
func TestExportAsyncFunctionAndGenerator(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export async function af() { return 1; }
export async function* ag() { yield 1; }`,
		"entry.js": `import { af, ag } from "./m.js";
typeof af + "/" + typeof ag`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("async export: %v", err)
	}
	assertString(t, v, "function/function")
}

// TestExportClassExtends 导出带继承的 class, 入口 new 后调用父类方法。
func TestExportClassExtends(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `class Base { greet() { return "base"; } }
export class C extends Base { }`,
		"entry.js": `import { C } from "./m.js";
new C().greet()`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("class extends export: %v", err)
	}
	assertString(t, v, "base")
}

// TestExportVarImportable 导出 var 可被 import (模块模式)。
func TestExportVarImportable(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export var v = 9;`,
		"entry.js": `import { v } from "./m.js"; v`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export var: %v", err)
	}
	assertNumber(t, v, 9)
}

// TestExportMultipleDeclaratorsAndDestructuring 覆盖多 declarator、解构、无初值。
func TestExportMultipleDeclaratorsAndDestructuring(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1, b = 2, c;
export const { x, y } = { x: 10, y: 20 };
c = 3;`,
		"entry.js": `import { a, b, c, x, y } from "./m.js";
a + b + c + x + y`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("multi/destructuring export: %v", err)
	}
	assertNumber(t, v, 36) // 1+2+3+10+20
}

// --- export default ---

// TestExportDefaultNamedFunctionTDZ 修复: 具名默认函数被 import 后立即调用,
// 不再抛 "Cannot access lexical declaration before initialization"。
func TestExportDefaultNamedFunctionTDZ(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export default function f() { return "FD"; }`,
		"entry.js": `import d from "./m.js"; d()`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("具名默认函数 import 后调用失败 (TDZ?): %v", err)
	}
	assertString(t, v, "FD")
}

// TestExportDefaultNamedClassInstantiate 具名默认类被 import 后立即实例化。
func TestExportDefaultNamedClassInstantiate(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export default class C { constructor() { this.v = 9; } }`,
		"entry.js": `import C from "./m.js"; new C().v`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("具名默认类 import 后实例化失败: %v", err)
	}
	assertNumber(t, v, 9)
}

// TestExportDefaultNameIsNotNamedExport 具名默认函数/类的名字**不作为命名导出**
// (规范 16.2.3.7): 应当能用 default 拿到值, 但不存在同名命名导出。
// 用命名空间对象的键序验证 —— 只有 "default", 没有 "f"。
func TestExportDefaultNameIsNotNamedExport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export default function f() { return 1; }`,
		"entry.js": `import * as ns from "./m.js";
Object.keys(ns).join(",")`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("default namespace keys: %v", err)
	}
	assertString(t, v, "default")
}

// TestExportDefaultAnonymousFunction 匿名默认函数 (esbuild 常见产物)。
func TestExportDefaultAnonymousFunction(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export default function () { return "anon"; }`,
		"entry.js": `import d from "./m.js"; d()`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("匿名默认函数: %v", err)
	}
	assertString(t, v, "anon")
}

// --- 具名再导出 (修 ReferenceError) ---

// TestExportNamedReexport 修复 `export { f as ff } from "./dep.js"` 的
// "ReferenceError: f is not defined"。
func TestExportNamedReexport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js":   `export function f() { return 41; } export const g = 1;`,
		"mid.js":   `export { f as ff, g as gg } from "./dep.js";`,
		"entry.js": `import { ff, gg } from "./mid.js"; ff() + gg`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("具名再导出: %v", err)
	}
	assertNumber(t, v, 42)
}

// TestExportFromNamespace `export * as ns from "m"` 转出命名空间对象。
func TestExportFromNamespace(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js":   `export const x = 5; export function dbl() { return 10; }`,
		"mid.js":   `export * as ns from "./src.js";`,
		"entry.js": `import { ns } from "./mid.js"; ns.x + ns.dbl()`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export * as ns: %v", err)
	}
	assertNumber(t, v, 15)
}

// --- 星号再导出 ---

// TestExportStarDefaultNotForwardedAndLocalWins 星号再导出:
//  1. 源模块的 default 不被转发;
//  2. 本模块已存在的同名导出不背星号覆盖 (本地赢)。
func TestExportStarDefaultNotForwardedAndLocalWins(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js": `export default "DEF"; export const a = "A"; export const b = "B";`,
		"mid.js": `export const a = "LOCAL"; export * from "./src.js";`,
		"entry.js": `import * as ns from "./mid.js";
ns.a + "|" + ns.b + "|" + typeof ns.default`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export *: %v", err)
	}
	assertString(t, v, "LOCAL|B|undefined")
}

// TestExportStarNamespaceKeyOrderStable 导出枚举顺序稳定: 同一模块多次物化
// 的 Object.keys 完全一致 (先按登记顺序, 星号源按出现顺序)。
func TestExportStarNamespaceKeyOrderStable(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"src.js": `export const b = "B"; export const c = "C";`,
		"mid.js": `export const a = "A"; export * from "./src.js";`,
		"entry.js": `import * as n1 from "./mid.js";
import * as n2 from "./mid.js";
Object.keys(n1).join(",") + "==" + Object.keys(n2).join(",")`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("namespace key order: %v", err)
	}
	assertString(t, v, "a,b,c==a,b,c")
}

// --- 活绑定 ---

// TestExportLocalLiveBinding 本地 `export let` 是活绑定: 导出后对同名局部变量
// 的再赋值, 在之后物化的命名空间里能读到新值。
func TestExportLocalLiveBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1;
export function bump() { a = 2; }`,
		"entry.js": `import { bump } from "./m.js";
import * as before from "./m.js";
var first = before.a;
bump();
import * as after from "./m.js";
first + "/" + after.a`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("local live binding: %v", err)
	}
	assertString(t, v, "1/2")
}

// TestExportNamedReexportLiveBinding 具名再导出的"取值时读取源模块导出槽":
// 源模块改了值之后, 再导出的命名空间读到的是源模块导出槽的**新值**。
func TestExportNamedReexportLiveBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let a = 1;
export function bump() { a = 2; }`,
		"re.js": `export { a } from "./m.js";`,
		"entry.js": `import { bump } from "./m.js";
import * as before from "./re.js";
var first = before.a;
bump();
import * as after from "./re.js";
first + "/" + after.a`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("再导出取值时解析: %v", err)
	}
	assertString(t, v, "1/2")
}

// --- 循环导入 + 空导出 ---

// TestExportStarCycleTerminates export * 参与循环导入时不死循环。
func TestExportStarCycleTerminates(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js":     `export * from "./b.js"; export const a = "A";`,
		"b.js":     `export * from "./a.js"; export const b = "B";`,
		"entry.js": `import * as ns from "./a.js"; ns.a + ns.b`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export * 循环导入: %v", err)
	}
	assertString(t, v, "AB")
}

// TestExportVarNoInitEnumPattern esbuild 把 `export enum` 降级成的形态:
//
//	export var Color;
//	(function (Color) { Color[Color["Red"] = 0] = "Red"; })(Color || (Color = {}));
//
// `export var` 必须先参与 var 提升 (声明为 undefined) 并作为活绑定导出, 后续对
// Color 的赋值才能被 import 方看到 —— 这是 M2 里 export enum 可用的关键。
func TestExportVarNoInitEnumPattern(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export var Color;
(function (Color) {
  Color[Color["Red"] = 0] = "Red";
  Color[Color["Green"] = 1] = "Green";
})(Color || (Color = {}));`,
		"entry.js": `import { Color } from "./m.js"; Color.Red + "," + Color.Green + "," + Color[0]`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export enum 降级形态: %v", err)
	}
	assertString(t, v, "0,1,Red")
}

// TestExportDefaultReexport `export { default } from "m"` / `export { default as d }`。
// 这是 npm/TS 包把子模块 default 提升为 barrel default 的常见形态。
func TestExportDefaultReexport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export default "D"; export const x = 1;`,
		"re.js":    `export { default, x } from "./m.js";`,
		"entry.js": `import d, { x } from "./re.js"; d + x`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("default 再导出: %v", err)
	}
	assertString(t, v, "D1")
}

// TestExportLocalAsDefault `export { a as default }` 把本地绑定作为 default 导出。
func TestExportLocalAsDefault(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export const a = 5; export { a as default };`,
		"entry.js": `import d from "./m.js"; d`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("a as default: %v", err)
	}
	assertNumber(t, v, 5)
}

// TestExportNamespaceCycleTerminates `export * as ns` 双向成环也不能栈溢出
// (命名空间物化必须共享"构造中"集合来打断环)。
func TestExportNamespaceCycleTerminates(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js":     `export * as b from "./b.js"; export const a = "A";`,
		"b.js":     `export * as a from "./a.js"; export const b = "B";`,
		"entry.js": `import * as ns from "./a.js"; ns.a + ns.b.b`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("命名空间再导出环: %v", err)
	}
	assertString(t, v, "AB")
}

// TestExportEmpty export {} 合法且不影响后续代码。
func TestExportEmpty(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export {}; export const keep = "K";`,
		"entry.js": `import { keep } from "./m.js"; keep`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("export {}: %v", err)
	}
	assertString(t, v, "K")
}

// --- barrel: npm 包 index 的典型形态 ---

// TestExportBarrelNamedImport 真实 barrel 套件 (M5 关键证据):
//
//	index.js: export * from "./a.js"; export { b } from "./b.js";
//	entry.js: import { a, b } from "./index.js";
func TestExportBarrelNamedImport(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"a.js":     `export const a = "A";`,
		"b.js":     `export const b = "B";`,
		"index.js": `export * from "./a.js"; export { b } from "./b.js";`,
		"entry.js": `import { a, b } from "./index.js"; a + b`,
	})
	v, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("barrel named import: %v", err)
	}
	assertString(t, v, "AB")
}

// --- 持久化夹具 ---

// TestESMFixtures 逐个跑 testdata/esm 下的持久夹具 (可被人肉打开)。
func TestESMFixtures(t *testing.T) {
	cases := []struct {
		file string
		want string
	}{
		{"decl_forms.js", "15"},
		{"export_default_named_function.js", "named"},
		{"export_default_named_class.js", "cls"},
		{"export_named_alias.js", "7"},
		{"export_empty.js", "empty-ok"},
		{"reexport/named_entry.js", "43"},
		{"star/star_entry.js", "a,b|LOCALB|undefined"},
		{"ns/ns_entry.js", "15"},
		{"cycle/cyc_entry.js", "A|B"},
		{"barrel/entry.js", "AB"},
		{"default_call/def_fn_entry.js", "FD"},
		{"default_call/def_cls_entry.js", "9"},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			v := runESMFixture(t, filepath.FromSlash(tc.file))
			if got := inspect(v); got != tc.want {
				t.Fatalf("%s = %q, 期望 %q", tc.file, got, tc.want)
			}
		})
	}
}

// TestExportFromMissingModuleReportsError 再导出目标缺失时, 错误信息要能看出来
// 是"模块找不到", 而不是静默 undefined。
func TestExportFromMissingModuleReportsError(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": `export { x } from "./nope.js";`,
	})
	_, err := EvalFile(filepath.Join(dir, "entry.js"))
	if err == nil {
		t.Fatal("再导出缺失模块应当报错")
	}
	if !strings.Contains(err.Error(), "nope.js") {
		t.Fatalf("错误应提到缺失模块名: %v", err)
	}
}
