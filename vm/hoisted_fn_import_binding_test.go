package vm

import (
	"path/filepath"
	"strings"
	"testing"
)

// ===== 提升编译的函数体里, 「import 绑定」与「export function」必须可见 =====
//
// 两个同族缺陷, 根因都是 compiler.prescanScope 漏登记 —— 与
// destructure_scope_binding_test.go (rLGyHa) 是同一个坑的另外两个入口。
//
// 背景 (compiler/compiler.go 的 compileStatements):
//
//	1. prescanScope(stmts)        先登记本层的 let/const/using/function 名字;
//	2. 再把**列表中所有 function 声明提升编译** (编译其函数体);
//	3. 最后按源码序编译其余语句。
//
// 步骤 2 要求"函数体里能解析到本层后面才出现的绑定"; 靠的就是步骤 1 的预登记。
// prescanScope 少登记谁, 谁就在函数体里退化成 OP_LOAD_GLOBAL → 运行期
// `ReferenceError: X is not defined` —— 而 X 明明就在同一文件里写得清清楚楚。
//
// 缺陷 A: ImportDeclaration 从不预登记。
//
//	import { FORMS } from "./store.js";
//	function Bar() { return FORMS.length; }   // ReferenceError: FORMS is not defined
//
// 缺陷 B: ExportDeclaration 从不拆包。
//
//	export function boot() { return apply(); }
//	export function apply() { return 42; }    // ReferenceError: apply is not defined
//
// 两者的"侥幸不中"边界完全一致, 也解释了为什么它们长期没被发现:
//   - 脚本**入口**顶层不中: 全局作用域的 import/function 落共享全局环境, 按名
//     查全局侥幸可用 (下面 TestGlobalEntryStillWorks 钉住这条不能回退);
//   - 函数**表达式 / 箭头**不中: 它们按源码序编译, 不参与提升;
//   - 被引用的绑定写在**函数声明之前**时也不中 (解析时已登记)。
//
// 修法: prescanScope 补两个 case —— ImportDeclaration 按本地名预登记
// (仅非全局作用域, 见该处注释), ExportDeclaration 拆包后递归预登记其
// 声明式导出。

// TestHoistedFnSeesImportedBinding 缺陷 A: 提升函数体内读 import 绑定。
func TestHoistedFnSeesImportedBinding(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export const FORMS = [1, 2, 3];`,
		"entry.js": `import { FORMS } from "./dep.js";
function count() { return FORMS.length; }
count()`,
	})
	v, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("提升函数体内访问 import 绑定不该报错: %v", err)
	}
	assertNumber(t, v.LastPopped(), 3)
}

// TestHoistedFnSeesImportedFunction 同缺陷 A, 绑定是函数而非 const。
func TestHoistedFnSeesImportedFunction(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export function double(n) { return n * 2; }`,
		"entry.js": `import { double } from "./dep.js";
function calc() { return double(21); }
calc()`,
	})
	v, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("提升函数体内访问 import 的函数不该报错: %v", err)
	}
	assertNumber(t, v.LastPopped(), 42)
}

// TestHoistedFnSeesImportedDefaultAndNamespace 默认导入与命名空间导入同样要可见。
func TestHoistedFnSeesImportedDefaultAndNamespace(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export default 7;
export const k = 5;`,
		"entry.js": `import d from "./dep.js";
import * as ns from "./dep.js";
function sum() { return d + ns.k; }
sum()`,
	})
	v, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("提升函数体内访问默认/命名空间导入不该报错: %v", err)
	}
	assertNumber(t, v.LastPopped(), 12)
}

// TestHoistedFnSeesExportFunction 缺陷 B: 提升函数体内读同级 export function。
//
// 注意"先调用方、后被调方"是自然写法, 所以这个形态在真实代码里非常常见。
func TestHoistedFnSeesExportFunction(t *testing.T) {
	_, err := runModuleEntry(t, `export function boot() { return apply(); }
export function apply() { return 42; }
boot()`)
	if err != nil {
		t.Fatalf("提升函数体内访问同级 export function 不该报错: %v", err)
	}
}

// TestHoistedFnSeesExportConst 缺陷 B 的 const 面: export const 也走拆包路径。
func TestHoistedFnSeesExportConst(t *testing.T) {
	_, err := runModuleEntry(t, `export function read() { return LIMIT; }
export const LIMIT = 9;
read()`)
	if err != nil {
		t.Fatalf("提升函数体内访问同级 export const 不该报错: %v", err)
	}
}

// TestExportFunctionMutualRecursion 互相递归的两个 export function 都要能解析。
func TestExportFunctionMutualRecursion(t *testing.T) {
	v, err := runModuleEntry(t, `export function isEven(n) { return n === 0 ? true : isOdd(n - 1); }
export function isOdd(n) { return n === 0 ? false : isEven(n - 1); }
isEven(10)`)
	if err != nil {
		t.Fatalf("export function 互相递归不该报错: %v", err)
	}
	if got := v.LastPopped().Inspect(); got != "true" {
		t.Fatalf("isEven(10) = %s, want true", got)
	}
}

// TestGlobalEntryImportStillGlobalWritable 入口脚本 (全局作用域) 的 import 不能
// 因此被改成局部槽位 —— 那会破坏脚本"顶层绑定落共享全局环境"的语义。
//
// 这条钉住 prescanScope 里 `if c.isGlobalScope() { continue }` 那个分支:
// 全局层预登记会让后续 compileImportDeclaration 的 declareOnce 认领到同名符号,
// 于是 import 改走 OP_STORE 局部槽位, 顶层再按名读全局就读不到了。
//
// 走 EvalFileVM (脚本语义, moduleMode=false) 而非 EvalModuleFileVM —— 只有脚本
// 模式才命中全局作用域那条分支。
func TestGlobalEntryImportStillGlobalWritable(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export const v = 7;`,
		"entry.js": `import { v } from "./dep.js";
function read() { return v; }
read()`,
	})
	v, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("脚本模式 import + 提升函数访问不该报错: %v", err)
	}
	assertNumber(t, v.LastPopped(), 7)
}

// TestShadowingLocalStillWins 预登记不能破坏遮蔽: 函数内同名局部声明仍优先于
// import 绑定 (这是普通作用域规则, 只是确认预登记没把符号提前泄漏进来)。
func TestShadowingLocalStillWins(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export const v = "imported";`,
		"entry.js": `import { v } from "./dep.js";
function f() { const v = "local"; return v; }
f()`,
	})
	v, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("遮蔽 import 绑定的局部声明不该报错: %v", err)
	}
	if got := v.LastPopped().Inspect(); got != "local" {
		t.Fatalf("局部声明应遮蔽 import 绑定, got %s", got)
	}
}

// TestImportRedeclareStillSyntaxError 预登记后, 同一层重名声明仍必须是
// SyntaxError (而不是被静默接受) —— 回归 prescanDeclare 的重复声明检查。
func TestImportRedeclareStillSyntaxError(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"dep.js": `export const a = 1;`,
		"entry.js": `import { a } from "./dep.js";
const a = 2;`,
	})
	_, err := EvalModuleFileVM(filepath.Join(dir, "entry.js"))
	if err == nil {
		t.Fatal("import 绑定与 const 重名应报 SyntaxError")
	}
	if !strings.Contains(err.Error(), "already been declared") {
		t.Fatalf("期望 already been declared, got: %v", err)
	}
}
