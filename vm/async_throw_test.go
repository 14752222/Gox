package vm

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== async 函数体 throw 的传播 (rZn4IS) =====
//
// 背景: async 函数被编译成「wrapper + 内层 generator」两段, await 编译为 yield,
// 由 stdlib 的 __spawn 驱动 generator。函数体里未被catch 捕获的 throw 曾经
// **既不产生 rejection, 也不抛给调用方**, 而是让整个 async 调用返回 undefined ——
// 于是 `try { await api() } catch (e) { ... }` 里 catch 抓不到 api 内部的错误,
// 代码带着 undefined 继续往下跑。
//
// 根因: generator 因异常终止时, 它那一帧仍留在帧栈上(runLoop 只有跑到边界的
// 正常路径才自然 popFrame)。外层 async 的 wrapper 帧被它压住, wrapper 的
// OP_RETURN 在错误栈基上取值 ⇒ 返回 undefined 而不是 __spawn 交出的 promise。
// 修复见 vm.finishGenRun / vm.genThrow 的 unwindGenFrame。

// runAsyncEval 跑一段 async 场景, 返回脚本写进 __out 的各行。
//
// 用 __out 数组而不是直接看 console 输出: 这样断言的是脚本可观察到的值,
// 不受宿主 console 实现影响。
func runAsyncEval(t *testing.T, src string) string {
	t.Helper()
	prelude := "let __out = [];\n"
	postlude := "\n__out.forEach(function(s){ console.log(s) });\n"
	vm, err := EvalVM(prelude + src + postlude)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	return outLines(t, vm)
}

// TestAsyncThrowProducesRejectedPromise 是本组的核心断言:
// async 函数体throw ⇒ 调用结果是 rejected Promise, 且 rejection reason
// 就是被抛出的那个 Error 对象本身 (规范要求 catch侧 === 抛出侧)。
func TestAsyncThrowProducesRejectedPromise(t *testing.T) {
	got := runAsyncEval(t, `
		const err = new Error("boom");
		async function thr(){ throw err }
		__out.push("isPromise:" + (thr() instanceof Promise));
		thr().then(
			function(v){ __out.push("resolved:" + v) },
			function(e){ __out.push("rejected:" + e.message + " same:" + (e === err)) }
		);
	`)
	if !strings.Contains(got, "isPromise:true") {
		t.Errorf("async 函数体 throw 后应返回 Promise, got:\n%s", got)
	}
	if !strings.Contains(got, "rejected:boom same:true") {
		t.Errorf("rejection reason 应是原始 Error 对象(===), got:\n%s", got)
	}
}

// TestAsyncThrowCaughtByAwait 覆盖最初报告的形态:
// try/catch 里 await 一个会throw 的 async 函数, catch 必须抓得到。
func TestAsyncThrowCaughtByAwait(t *testing.T) {
	got := runAsyncEval(t, `
		async function thr(){ throw new Error("x") }
		async function g(){
			try { await thr(); return "no-throw" } catch(e){ return "caught:" + e.message }
		}
		g().then(function(v){ __out.push(v) });
	`)
	if !strings.Contains(got, "caught:x") {
		t.Errorf("await 处应能catch 到 async 函数体抛出的错误, got:\n%s", got)
	}
}

// TestAsyncThrowNotAwaitedStillRejects 覆盖「不 await 直接调」:
// 调用方拿到的仍必须是 rejected Promise（不是 undefined, 也不是函数体的值）。
func TestAsyncThrowNotAwaitedStillRejects(t *testing.T) {
	got := runAsyncEval(t, `
		async function thr(){ throw new Error("y") }
		async function b(){ const r = await thr(); return "no:" + r }
		const p = b();
		__out.push("isPromise:" + (p instanceof Promise));
		p.then(
			function(v){ __out.push("resolved:" + v) },
			function(e){ __out.push("rejected:" + e.message) }
		);
	`)
	if !strings.Contains(got, "isPromise:true") {
		t.Errorf("await 撞上 rejection 时 async 调用仍应返回 Promise, got:\n%s", got)
	}
	if !strings.Contains(got, "rejected:y") {
		t.Errorf("await 撞上 rejection 应传播为 rejection, got:\n%s", got)
	}
}

// TestAsyncThrowAfterAwait 覆盖「先正常 await 一次, 再 throw」:
// 走的是恢复路径 (runSuspendedGen) 而非首次启动路径, 是另一条代码路径。
func TestAsyncThrowAfterAwait(t *testing.T) {
	got := runAsyncEval(t, `
		async function ok(){ return 1 }
		async function b(){ const r = await ok(); throw new Error("after") }
		b().then(
			function(v){ __out.push("resolved:" + v) },
			function(e){ __out.push("rejected:" + e.message) }
		);
	`)
	if !strings.Contains(got, "rejected:after") {
		t.Errorf("await 之后 throw 应传播为 rejection, got:\n%s", got)
	}
}

// TestAsyncThrowNonErrorValue 确认 throw 非 Error 值时原样传递,
// 不能被包装成 Error（规范: rejection reason 就是抛出的值）。
func TestAsyncThrowNonErrorValue(t *testing.T) {
	got := runAsyncEval(t, `
		async function thr(){ throw "plain" }
		thr().then(
			function(v){ __out.push("resolved:" + v) },
			function(e){ __out.push("rejected:" + e) }
		);
	`)
	if !strings.Contains(got, "rejected:plain") {
		t.Errorf("throw 非 Error 值应原样作为 rejection reason, got:\n%s", got)
	}
}

// TestAsyncNoErrorUnaffected 是防回归对照: 正常 async 路径不能被本次修复带坏。
func TestAsyncNoErrorUnaffected(t *testing.T) {
	got := runAsyncEval(t, `
		async function one(){ return 1 }
		async function two(){ return 2 }
		async function sum(){ const a = await one(); const b = await two(); return a + b }
		async function arrow(){ return "arrow-ok" }
		sum().then(function(v){ __out.push("sum:" + v) });
		arrow().then(function(v){ __out.push(v) });
	`)
	if !strings.Contains(got, "sum:3") {
		t.Errorf("多次 await 求和应得 3, got:\n%s", got)
	}
	if !strings.Contains(got, "arrow-ok") {
		t.Errorf("async 箭头函数应正常, got:\n%s", got)
	}
}

// outLines 读取测试脚本写进 __out 的行。
func outLines(t *testing.T, vm *VM) string {
	t.Helper()
	var sb strings.Builder
	vals, ok := vm.Globals().Get("__out")
	if !ok {
		t.Fatal("global __out not found")
	}
	arr, ok := vals.(*object.Array)
	if !ok {
		t.Fatalf("__out 不是数组: %T", vals)
	}
	for _, v := range arr.Elements {
		sb.WriteString(v.Inspect())
		sb.WriteString("\n")
	}
	return sb.String()
}
