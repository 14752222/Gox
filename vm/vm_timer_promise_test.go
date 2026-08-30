package vm

import (
	"testing"

	"js-runtime/object"
)

// ===== 定时器 + Promise 集成回归测试 =====
// 修复前: currentVM 只在主脚本执行期间有效，RunTimers 期间为 nil，
// 定时器回调中 resolve 的 Promise 的 .then 回调经 object.CallFunction
// 桥调用时被静默丢弃 (返回 undefined)。
// 修复: RunTimersUntil 执行期间注册 currentVM。

// TestTimerPromiseThen: setTimeout 回调中 resolve → .then 回调应执行
func TestTimerPromiseThen(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let got = "";
		let p = new Promise(function(resolve) {
			setTimeout(function() { resolve("done"); }, 5);
		});
		p.then(function(v) { got = v; });
		got;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	val, ok := vm.Globals().Get("got")
	if !ok {
		t.Fatalf("global %q not found", "got")
	}
	s, ok := val.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T", val)
	}
	if s.Value != "done" {
		t.Fatalf("expected then callback to set got=\"done\", got %q", s.Value)
	}
}

// TestAsyncAwaitWithTimer: async/await 等待定时器驱动的 Promise 应完成
func TestAsyncAwaitWithTimer(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let phase = "start";
		async function main() {
			await delay(5);
			phase = "awaited";
		}
		main();
		phase;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	val, _ := vm.Globals().Get("phase")
	s, ok := val.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T", val)
	}
	if s.Value != "awaited" {
		t.Fatalf("expected phase=\"awaited\", got %q", s.Value)
	}
}

// TestPromiseChainAcrossTimers: 跨定时器的 Promise 链应完整执行
func TestPromiseChainAcrossTimers(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let result = 0;
		delay(5, 1)
			.then(function(v) { result = v + 1; return result; })
			.then(function(v) { result = v * 10; });
		result;
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	num := getGlobalNumber(t, vm, "result")
	if num != 20 {
		t.Fatalf("expected chained result 20, got %v", num)
	}
}
