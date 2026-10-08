package vm

import (
	"path/filepath"
	"testing"
)

// ===== 解构声明的绑定在「提升编译的函数体」里必须可见 (P0 回归) =====
//
// 看板单 rLGyHa。症状: 模块顶层
//
//	const [x, y] = [1, 2];
//	function f() { return x; }   // ReferenceError: x is not defined
//
// 顶层**直接**读 x 正常, 但同模块函数体内读 x 抛 ReferenceError。
//
// 根因 (compiler/compiler.go):
//   - compileStatements 先把列表里所有函数声明**提升编译**(编译其函数体),
//     再按源码序编译其余语句 (compiler.go:556-584);
//   - 函数体能解析到"列表后面才出现的词法绑定"靠的是 prescanScope 预先
//     登记名字 (compiler.go:543-546 的注释即为此而立);
//   - 但 prescanScope 对解构声明项 (AST 里 Name 是合成名 "__destructure__",
//     真实名字藏在 Value 的模式里) 只跳过合成名、**没有登记模式里的真实名**,
//     于是 f 编译时 x 尚未登记 → compileIdentifier 走 emitGlobalLoad 退化路径
//     → 运行期在模块作用域 (无该全局) 抛 ReferenceError。
//
// 关键边界: 与 module/createSignal/const 都无关 —— **凡是非全局作用域**都中:
//   - 模块顶层 (模块顶层绑定是局部槽位, 不是全局属性) 中;
//   - 函数体内 `const [a,b]=…; function g(){return a;}` 同样中;
//   - 脚本**顶层**不中, 只因脚本顶层绑定是全局属性, 按名查全局侥幸可用;
//   - 箭头函数 / 函数表达式不中, 因为它们按源码序在解构之后编译 (不提升)。
//
// 修法: prescanScope 经 prescanDeclarator 也用 ast.PatternBoundNames 登记
// 解构模式里的真实绑定名。

// TestModuleArrayDestructureConstVisibleInHoistedFn 数组解构 const + 提升函数。
func TestModuleArrayDestructureConstVisibleInHoistedFn(t *testing.T) {
	vm, err := runModuleEntry(t, `const [x, y] = [1, 2];
function f() { return x + y; }
f()`)
	if err != nil {
		t.Fatalf("模块内提升函数访问解构 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 3)
}

// TestModuleArrayDestructureLetVisibleInHoistedFn 数组解构 let + 提升函数。
func TestModuleArrayDestructureLetVisibleInHoistedFn(t *testing.T) {
	vm, err := runModuleEntry(t, `let [lx, ly] = [3, 4];
function f() { return lx + ly; }
f()`)
	if err != nil {
		t.Fatalf("模块内提升函数访问解构 let 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 7)
}

// TestModuleObjectDestructureConstVisibleInHoistedFn 对象解构 const + 提升函数。
func TestModuleObjectDestructureConstVisibleInHoistedFn(t *testing.T) {
	vm, err := runModuleEntry(t, `const { oa, ob } = { oa: 5, ob: 6 };
function f() { return oa + ob; }
f()`)
	if err != nil {
		t.Fatalf("模块内提升函数访问对象解构 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 11)
}

// TestModuleDestructureAfterPlainConstVisibleInHoistedFn 解构声明出现在普通
// 声明**之后** (仍有其它普通绑定), 提升函数同时引用两者 —— 锁死不是"首个
// 声明"特例。
func TestModuleDestructureAfterPlainConstVisibleInHoistedFn(t *testing.T) {
	vm, err := runModuleEntry(t, `const plain = 7;
const [d1, d2] = [8, 9];
function f() { return plain + d1 + d2; }
f()`)
	if err != nil {
		t.Fatalf("模块内提升函数访问后置解构 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 24)
}

// TestModulePlainConstStillVisibleInHoistedFn 守卫: 普通 const 不受影响。
func TestModulePlainConstStillVisibleInHoistedFn(t *testing.T) {
	vm, err := runModuleEntry(t, `const z = 9;
function f() { return z; }
f()`)
	if err != nil {
		t.Fatalf("模块内提升函数访问普通 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 9)
}

// TestFnScopeDestructureVisibleInHoistedFn 非模块 (脚本) 的**函数体作用域**内
// 同样要能工作 —— 这是根因(提升编译早于解构声明登记)与模块无关的直接证据。
func TestFnScopeDestructureVisibleInHoistedFn(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": `function outer() {
  const [a, b] = [1, 2];
  function g() { return a + b; }
  return g();
}
outer()`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("函数体作用域内提升函数访问解构 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 3)
}

// TestFnScopeObjectDestructureVisibleInHoistedFn 函数体作用域 + 对象解构。
func TestFnScopeObjectDestructureVisibleInHoistedFn(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": `function outer() {
  const { m, n } = { m: 5, n: 6 };
  function g() { return m + n; }
  return g();
}
outer()`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("函数体作用域内提升函数访问对象解构 const 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 11)
}

// TestScriptTopLevelDestructureVisibleInHoistedFn 防反向回归: 脚本**顶层**
// 解构 + 提升函数此前就可用 (全局按名解析), 修后必须仍然可用。
func TestScriptTopLevelDestructureVisibleInHoistedFn(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"entry.js": `const [x, y] = [10, 20];
function g() { return x + y; }
g()`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("脚本顶层解构 + 提升函数不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 30)
}

// TestArrowAfterDestructureStillWorks 防反向回归: 箭头函数 (不提升, 源码序
// 在解构之后) 本来就正常, 修后保持正常。
func TestArrowAfterDestructureStillWorks(t *testing.T) {
	vm, err := runModuleEntry(t, `const { m, n } = { m: 3, n: 4 };
const h = () => m * n;
h()`)
	if err != nil {
		t.Fatalf("箭头函数访问解构绑定不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 12)
}

// TestModuleDestructureBindingWritableAcrossFn 解构声明 (按既有 let 语义) 仍
// 可在函数内被赋值写入 —— prescan 只登记名字, 不改变可写性边界。
func TestModuleDestructureBindingWritableAcrossFn(t *testing.T) {
	vm, err := runModuleEntry(t, `let [a] = [1];
function bump() { a = a + 10; return a; }
bump()`)
	if err != nil {
		t.Fatalf("解构 let 绑定在函数内赋值不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 11)
}
