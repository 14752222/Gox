package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/stdlib"
)

// ===== [M3] Inspector v1: REPL 求值 (gfx.DevEval + gx/dev 的 devEval) =====
//
// 语义核心是"跨调用保持状态" (REPL): 第一次 let 的绑定必须留在**持久化的全局
// 环境**里, 第二次才看得到。实现走 vm.EvalWithGlobals (顶层程序编译), 所以这里
// 直接注入一个 *runtime.Environment 验证 —— 不依赖宿主接线 (SetDevEnv)。

// TestDevEvalREPLStatePersists 直接注入环境, 验证跨调用状态与错误返回。
func TestDevEvalREPLStatePersists(t *testing.T) {
	SetDevEnv(nil) // 先落回 (若前序用例留了环境)
	env := stdlib.SetupGlobals()
	SetDevEnv(env)
	t.Cleanup(func() { SetDevEnv(nil) })

	if _, err := DevEval("let a = 1"); err != nil {
		t.Fatalf("let a = 1: %v", err)
	}
	v, err := DevEval("a + 1")
	if err != nil {
		t.Fatalf("a + 1: %v", err)
	}
	n, ok := v.(*object.Number)
	if !ok || n.Value != 2 {
		t.Fatalf("a + 1 = %s, want 2 (跨调用状态没保住)", v.Inspect())
	}

	// 再跨一次: 赋值后重新读取
	if _, err := DevEval("a = a * 10"); err != nil {
		t.Fatalf("a = a * 10: %v", err)
	}
	v, err = DevEval("a")
	if err != nil || v.Inspect() != "10" {
		t.Fatalf("a = %s (err=%v), want 10", v.Inspect(), err)
	}

	// 错误路径: 语法错误与运行时错误都要以 error 返回, 不能 panic
	if _, err := DevEval("1 +"); err == nil {
		t.Fatalf("语法错误应返回 error")
	}
	if _, err := DevEval("notDefinedXyz + 1"); err == nil {
		t.Fatalf("未定义变量应返回 error")
	}
	// 出错后环境仍然可用 (REPL 不因一次错误报废)
	if v, err := DevEval("a + 1"); err != nil || v.Inspect() != "11" {
		t.Fatalf("错误后环境应仍可用: %s (err=%v)", v.Inspect(), err)
	}
}

// TestDevEvalBuiltinShape 端到端: JS 侧 devEval 返回 { ok, value, error }。
func TestDevEvalBuiltinShape(t *testing.T) {
	SetDevEnv(nil)
	SetDevEnv(stdlib.SetupGlobals())
	t.Cleanup(func() { SetDevEnv(nil) })

	v, _ := evalUI(t, `
		import { devEval } from "gx/dev";
		import { h, render } from "gx/gfx";
		render(h("column", null, h("text", null, "x")));
		globalThis.g_let = devEval("let q = 41");
		globalThis.g_use = devEval("q + 1");
		globalThis.g_err = devEval("1 +");
		globalThis.g_bad = devEval(123);
	`)

	use := asObj(t, globalVal(t, v, "g_use"), "devEval(q+1)")
	if !propBool(t, use, "ok") {
		t.Fatalf("q+1 应 ok: %s", use.Inspect())
	}
	if got := propStr(t, use, "value"); got != "42" {
		t.Fatalf("q+1 value = %q, want 42", got)
	}
	if got := propStr(t, use, "error"); got != "" {
		t.Fatalf("成功时 error 应为空串, 得到 %q", got)
	}

	errRes := asObj(t, globalVal(t, v, "g_err"), "devEval(1 +)")
	if propBool(t, errRes, "ok") {
		t.Fatalf("语法错误应 ok:false")
	}
	if propStr(t, errRes, "error") == "" {
		t.Fatalf("失败时应带 error 文本")
	}

	bad := asObj(t, globalVal(t, v, "g_bad"), "devEval(123)")
	if propBool(t, bad, "ok") || propStr(t, bad, "error") == "" {
		t.Fatalf("非字符串参数应 ok:false 且带 error: %s", bad.Inspect())
	}
}

// TestDevEvalSandboxFallback 未接线时退化为独立 REPL 沙盒 (文档边界)。
func TestDevEvalSandboxFallback(t *testing.T) {
	SetDevEnv(nil)
	t.Cleanup(func() { SetDevEnv(nil) })

	if _, err := DevEval("let sandboxOnly = 7"); err != nil {
		t.Fatalf("沙盒求值失败: %v", err)
	}
	v, err := DevEval("sandboxOnly + 1")
	if err != nil {
		t.Fatalf("沙盒跨调用失败: %v", err)
	}
	if v.Inspect() != "8" {
		t.Fatalf("沙盒值 = %s, want 8", v.Inspect())
	}
}
