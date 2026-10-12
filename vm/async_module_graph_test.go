package vm

import (
	"path/filepath"
	"testing"
	"time"
)

// ===== 异步模块图求值的端到端行为 =====
//
// 只要模块图里有模块含顶层 await, 整图按 leaf-to-root 求值: 依赖了未完成异步
// 模块的模块, 其本体要等依赖完成才运行 (规范 InnerModuleEvaluation 的 async
// 分支)。下面用 globalThis.logs / 动态 import 观察求值与完成顺序。

// TestAsyncModuleGraphFulfillmentOrder b 含 TLA, a 依赖 b: a 的本体必须等 b
// 完成之后才运行 ⇒ 日志顺序 ["B","A"] (leaf-to-root)。
func TestAsyncModuleGraphFulfillmentOrder(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"setup.js": `globalThis.logs = [];
globalThis.pB = Promise.withResolvers();`,
		"b.js": `await globalThis.pB.promise;
globalThis.logs.push("B");`,
		"a.js": `import "./b.js";
globalThis.logs.push("A");`,
		"entry.js": `import "./setup.js";
import("./b.js");
import("./a.js");
globalThis.pB.resolve();
setTimeout(() => { globalThis.result = globalThis.logs.join(","); }, 0)`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if err := vm.RunTimersUntil(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	// 微任务语义 (rrpZIw): resolve 只入队, 模块本体在事件循环 drain 时才
	// 运行, 入口顶层的 join 必然读到 [] —— 旧断言 (LastPopped) 本身就是
	// 同步语义的产物, 改为从全局 result 取最终顺序。
	got, ok := vm.Globals().Get("result")
	if !ok {
		t.Fatalf("result 未设置: 异步模块图未在事件循环内完成")
	}
	assertString(t, got, "B,A")
}

// TestAsyncModuleGraphRejectionSkipsDependents b 的 TLA reject 后, 依赖它的 a
// 本体不应运行; 拒绝沿依赖边传播到 a 的求值。
func TestAsyncModuleGraphRejectionSkipsDependents(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"setup.js": `globalThis.logs = [];
globalThis.pB = Promise.withResolvers();`,
		"b.js": `await globalThis.pB.promise;
globalThis.logs.push("B-body");`,
		"a.js": `import "./b.js";
globalThis.logs.push("A-body");`,
		"entry.js": `import "./setup.js";
import("./b.js").catch(() => globalThis.logs.push("B-reject"));
import("./a.js").catch(() => globalThis.logs.push("A-reject"));
globalThis.pB.reject(new Error("boom"));
setTimeout(() => { globalThis.result = globalThis.logs.join(","); }, 0)`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if err := vm.RunTimersUntil(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	// b/a 的本体都不运行; 两个动态 import 都被拒绝, 顺序 B 先于 A。
	// (同上: 结果从全局 result 取, 不再依赖入口顶层 join 的同步读。)
	got, ok := vm.Globals().Get("result")
	if !ok {
		t.Fatalf("result 未设置: 拒绝未在事件循环内传播")
	}
	assertString(t, got, "B-reject,A-reject")
}

// TestAsyncModuleGraphNoTLAUnchanged 不含 TLA 的普通模块仍同步求值, 导入值可用
// (回归防线: 异步图求值不误伤同步模块)。
func TestAsyncModuleGraphNoTLAUnchanged(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"m.js":     `export const v = 42;`,
		"entry.js": `import { v } from "./m.js"; v`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	assertNumber(t, vm.LastPopped(), 42)
}
