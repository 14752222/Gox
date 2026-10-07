package vm

import (
	"path/filepath"
	"strings"
	"testing"
)

// ===== 模块顶层 const 不可变 (test262 instn-local-bndng-const.js 真回归) =====
//
// 背景: 模块顶层 const 绑定此前完全可变 —— 赋值不抛错、值被静默覆盖。
// 根因三条 (缺一不可):
//   1. compiler.isGlobalScope() = depth==0 && !moduleMode, 模块模式下恒 false;
//   2. 于是 compileConstStatement 走 else 分支, 用 OP_STORE_CONST 把模块顶层
//      const 当**局部槽位**初始化 (而非全局词法绑定);
//   3. 赋值路径 (x = v / 复合赋值 / ++ / 解构 / for-of 目标) 只发 OP_STORE
//      (纯槽位写入, 从不查 Symbol.IsConst), 故写入永远成功。
//
// 修法: 新增 OP_STORE_CONST_GUARD (0x2A)。编译器在**赋值位置**对 IsConst
// 符号改发它 (见 compiler.emitLocalStore); VM 侧无条件抛
// "TypeError: Assignment to constant variable: <name>" (可被 try/catch 捕获)。
// const **声明** 仍走 OP_STORE_CONST (允许 TDZ 的 nil 槽), 两者分工明确。

// runModuleEntry 以「模块入口」执行源码 (模块顶层恒严格、this=undefined),
// 返回 VM 与错误。
func runModuleEntry(t *testing.T, src string) (*VM, error) {
	t.Helper()
	dir := writeModuleDir(t, map[string]string{"entry.js": src})
	return EvalModuleFileVM(filepath.Join(dir, "entry.js"))
}

// TestModuleConstPlainAssignThrows 最小复现: 模块顶层 const 赋值必须抛 TypeError。
func TestModuleConstPlainAssignThrows(t *testing.T) {
	_, err := runModuleEntry(t, `const q = 1; q = 2;`)
	if err == nil {
		t.Fatal("模块顶层 const 赋值未抛错 (const 绑定可变 —— 回归)")
	}
	if !strings.Contains(err.Error(), "TypeError") ||
		!strings.Contains(err.Error(), "Assignment to constant variable") {
		t.Fatalf("期望 TypeError/Assignment to constant variable, got: %v", err)
	}
}

// TestModuleConstAssignCatchable 且抛出的 TypeError 必须可被 try/catch 捕获。
func TestModuleConstAssignCatchable(t *testing.T) {
	vm, err := runModuleEntry(t, `
const q = 1;
let caught = "none";
try { q = 2; } catch (e) {
  caught = (e instanceof TypeError) + ":" + e.message;
}
export { caught, q };
`)
	if err != nil {
		t.Fatalf("模块执行失败 (TypeError 应被 catch 接住): %v", err)
	}
	_ = vm
}

// moduleConstThrows 断言 src 以模块入口执行时抛 TypeError; 返回该错误文案。
func moduleConstThrows(t *testing.T, src string) string {
	t.Helper()
	_, err := runModuleEntry(t, src)
	if err == nil {
		t.Fatalf("期望抛 TypeError 但无异常: %q", src)
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("期望 TypeError, got: %v  (src=%q)", err, src)
	}
	return err.Error()
}

// TestModuleConstCompoundAssignThrows 复合赋值 (+= 等) 也必须抛。
func TestModuleConstCompoundAssignThrows(t *testing.T) {
	moduleConstThrows(t, `const x = 1; x += 1;`)
	moduleConstThrows(t, `const x = 8; x >>= 1;`)
}

// TestModuleConstIncDecThrows ++/-- (前缀与后缀) 也必须抛。
func TestModuleConstIncDecThrows(t *testing.T) {
	moduleConstThrows(t, `const x = 1; x++;`)
	moduleConstThrows(t, `const x = 1; ++x;`)
	moduleConstThrows(t, `const x = 1; x--;`)
	moduleConstThrows(t, `const x = 1; --x;`)
}

// TestModuleConstLogicalAssignThrows &&= / ||= / ??= 在**真正走到赋值**时
// 必须抛 (短路不赋值的情形不抛, 符合规范)。
func TestModuleConstLogicalAssignThrows(t *testing.T) {
	moduleConstThrows(t, `const x = 0; x ||= 2;`)    // 0 假 → 赋值 → 抛
	moduleConstThrows(t, `const x = 1; x &&= 2;`)    // 1 真 → 赋值 → 抛
	moduleConstThrows(t, `const x = null; x ??= 2;`) // null → 赋值 → 抛
}

// TestModuleConstLogicalAssignShortCircuitNoThrow 短路未赋值时**不得**抛。
func TestModuleConstLogicalAssignShortCircuitNoThrow(t *testing.T) {
	if _, err := runModuleEntry(t, `const x = 1; x ||= 2; export const r = x;`); err != nil {
		t.Fatalf("||= 短路 (x=1 真) 不应赋值/抛错: %v", err)
	}
	if _, err := runModuleEntry(t, `const x = 0; x &&= 2; export const r = x;`); err != nil {
		t.Fatalf("&&= 短路 (x=0 假) 不应赋值/抛错: %v", err)
	}
}

// TestModuleConstDestructureAssignThrows 解构赋值目标为 const 也必须抛。
//
// 注: 这里用**普通 const 声明** (const a = 1) 作为目标 —— 它经
// declareOnce(…, isConst=true) 正确标记 IsConst。反之 `const [a] = …`
// 的解构**声明**路径 (bindPatternTarget isDecl) 目前按 let 语义登记
// (isConst=false), 是该路径的既有边界, 不在本次修复范围内。
func TestModuleConstDestructureAssignThrows(t *testing.T) {
	moduleConstThrows(t, `const a = 1; [a] = [9];`)
	moduleConstThrows(t, `const p = 1; ({ p } = { p: 9 });`)
}

// TestModuleConstForOfTargetThrows for-of 以 const 为赋值目标也必须抛。
func TestModuleConstForOfTargetThrows(t *testing.T) {
	moduleConstThrows(t, `const x = 0; for (x of [1, 2]) {}`)
}

// TestModuleConstForOfLoopVarIsImmutable for (const x of ...) 的循环变量
// 也必须是每轮新的不可变绑定 —— 循环体内赋值必须抛。
func TestModuleConstForOfLoopVarIsImmutable(t *testing.T) {
	moduleConstThrows(t, `for (const x of [1, 2]) { x = 9; }`)
}

// TestModuleLetRemainsMutable 守卫: let 必须仍然可变 (不能把 guard 发射到 let)。
func TestModuleLetRemainsMutable(t *testing.T) {
	vm, err := runModuleEntry(t, `let x = 1; x = 42; export const r = x;`)
	if err != nil {
		t.Fatalf("let 赋值不应报错: %v", err)
	}
	_ = vm
}

// TestModuleClassBindingMutable 守卫: class 声明的绑定是**可变**词法绑定
// (规范 CreateMutableBinding, 与 let 同类), 赋值必须成功。
// 这正是 test262 eval-gtbndng-local-bndng-cls.js 的断言 —— 若把 class 误标
// 成 const (历史上如此), 赋值会被 OP_STORE_CONST_GUARD 误拒。
func TestModuleClassBindingMutable(t *testing.T) {
	if _, err := runModuleEntry(t, `
class classBinding { valueOf() { return 33; } }
classBinding = 44;
export const r = classBinding;
`); err != nil {
		t.Fatalf("class 绑定赋值应成功 (类绑定可变): %v", err)
	}
}

// TestModuleConstNoAssignStillWorks 守卫: 不改动 const 的正常读写不受影响。
func TestModuleConstNoAssignStillWorks(t *testing.T) {
	if _, err := runModuleEntry(t, `const x = 7; export const y = x + 1;`); err != nil {
		t.Fatalf("const 正常读写不应报错: %v", err)
	}
}

// TestScriptConstStillThrows 对照: script 顶层 const 赋值同样抛 TypeError
// (此前已正确 —— 走 OP_STORE_GLOBAL 的全局 const 检查), 不得被本次改动破坏。
func TestScriptConstStillThrows(t *testing.T) {
	_, err := EvalVM(`const q = 1; q = 2;`)
	if err == nil {
		t.Fatal("script 顶层 const 赋值未抛错")
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("期望 TypeError, got: %v", err)
	}
}
