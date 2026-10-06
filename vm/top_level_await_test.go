package vm

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== 顶层 await (Top-Level Await, TLA) 的端到端行为 =====
//
// 顶层 await 只在**模块**里合法 (ModuleItem 的语法参数带 +Await)。Gox 的
// 入口文件一律按脚本编译 (EvalFileVM → compileSource(code, false)), 模块
// 语义只在被 import 的文件上生效 (loadModuleFile → compileSource(code, true))。
//
// 因此这里用"入口脚本 import 一个含 TLA 的模块"来触发: 被 import 的模块编译
// 时 moduleMode=true, 顶层 await 被编成主单元里的 OP_YIELD —— 主单元本身
// 成为生成器, 由 vm.RunCompiledAsync 同步驱动到完成。
//
// 解析层断言在 parser/top_level_await_test.go; 编译层 (HasTopLevelAwait)
// 在 compiler; 这里钉的是**求值层**真的把值算对、导出真的可见、reject 真的
// 传播。

// TestTopLevelAwaitResolvesValue 顶层 await 非 Promise 与已结算 Promise 的值。
func TestTopLevelAwaitResolvesValue(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export const a = await 42;
export const b = await Promise.resolve(8);
export const c = await Promise.resolve(1).then(v => v * 2).then(v => v * 3);
export const d = await null;`,
		"entry.js": `import { a, b, c, d } from "./m.js"; a + b + c + d`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("顶层 await 模块不该报错: %v", err)
	}
	// 42 + 8 + 6 + null(=0) = 56
	assertNumber(t, vm.LastPopped(), 56)
}

// TestTopLevelAwaitExportAfterAwait await 之后声明的导出也要可见 (槽位复用)。
func TestTopLevelAwaitExportAfterAwait(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `const x = await 40;
export const y = x + 2;`,
		"entry.js": `import { y } from "./m.js"; y`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 42)
}

// TestTopLevelAwaitExportReassignedAfterAwait 导出绑定在 await 之后被重赋值,
// 导入方应看到新值 (OP_EXPORT_BINDING 的读取器与主单元共用同一份 Locals)。
func TestTopLevelAwaitExportReassignedAfterAwait(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `export let n = 1;
await 0;
n = 2;`,
		"entry.js": `import { n } from "./m.js"; n`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 2)
}

// TestTopLevelAwaitRejectionThrows await 的 Promise 被 reject 时, 模块求值
// 以该 reason 失败 (顶层 await 未捕获的 rejection 等价于模块抛异常)。
func TestTopLevelAwaitRejectionThrows(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `await Promise.reject(new Error("boom"));`,
		"entry.js": `import "./m.js"; 1`,
	})
	_, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err == nil {
		t.Fatal("期望顶层 await 的 rejection 让模块加载失败")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("错误消息应含 reason \"boom\", got: %v", err)
	}
}

// TestTopLevelAwaitTryCatch 顶层 await 的 rejection 可被模块顶层 try/catch 捕获。
func TestTopLevelAwaitTryCatch(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `let msg = "unset";
try {
  await Promise.reject(new Error("caught"));
} catch (e) {
  msg = e.message;
}
export const got = msg;`,
		"entry.js": `import { got } from "./m.js"; got`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertString(t, vm.LastPopped(), "caught")
}

// TestTopLevelForAwait 顶层 for await...of 也应被驱动。
func TestTopLevelForAwait(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js": `let sum = 0;
for await (const v of [1, 2, 3]) { sum += v; }
export const total = sum;`,
		"entry.js": `import { total } from "./m.js"; total`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("顶层 for await 不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 6)
}

// TestTopLevelAwaitNoAwaitModuleStillSync 不含 TLA 的模块仍走同步路径,
// 回归防线: HasTopLevelAwait 不误判普通模块。
func TestTopLevelAwaitNoAwaitModuleStillSync(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export const k = 7;`,
		"entry.js": `import { k } from "./m.js"; k`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 7)
}

// TestTopLevelAwaitScriptStillRejected 脚本顶层 await 仍是 SyntaxError ——
// TLA 只在模块 (moduleMode) 下放行, 不污染 sloppy script。
func TestTopLevelAwaitScriptStillRejected(t *testing.T) {
	_, err := EvalVM(`var x = await 1;`)
	if err == nil {
		t.Fatal("脚本顶层 await 应报 SyntaxError")
	}
	if !strings.Contains(err.Error(), "await is only valid in async functions") {
		t.Fatalf("错误消息不符: %v", err)
	}
}

// TestTopLevelAwaitPendingPromiseSuspends 依赖定时器/宏任务才能结算的
// pending Promise: 顶层 await 挂起, 模块求值暂停但不报错; 事件循环里 promise
// 结算后再恢复并完成 (异步模块图求值, 不再"明确报错")。
func TestTopLevelAwaitPendingPromiseSuspends(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `globalThis.__tla_v = await new Promise(r => setTimeout(() => r("tick"), 0));`,
		"entry.js": `import "./m.js"; 0`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("pending TLA 不该报错 (应挂起): %v", err)
	}
	if v, ok := vm.Globals().Get("__tla_v"); ok {
		if s, isStr := v.(*object.String); isStr && s.Value == "tick" {
			t.Fatal("pending TLA 不应在定时器结算前完成")
		}
	}
	if err := vm.RunTimersUntil(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	v, ok := vm.Globals().Get("__tla_v")
	if !ok {
		t.Fatal("__tla_v 未设置")
	}
	assertString(t, v, "tick")
}

// TestTopLevelAwaitGlobalSideEffect 顶层 await 期间的副作用 (写 globalThis)
// 应被真实执行 (驱动不是"假装跳过")。
func TestTopLevelAwaitGlobalSideEffect(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `globalThis.__tla_seen = await Promise.resolve("ok");`,
		"entry.js": `import "./m.js"; globalThis.__tla_seen`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertString(t, vm.LastPopped(), "ok")
	if v, ok := vm.Globals().Get("__tla_seen"); !ok {
		t.Fatal("globalThis.__tla_seen 未设置")
	} else if s, ok := v.(*object.String); !ok || s.Value != "ok" {
		t.Fatalf("__tla_seen = %v, want \"ok\"", v.Inspect())
	}
}
