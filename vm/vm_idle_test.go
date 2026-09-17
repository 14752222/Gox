package vm

import (
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== requestIdleCallback 测试 =====

func TestRequestIdleCallbackReturnsID(t *testing.T) {
	resetTimers(t)
	_, result := runEvalVM(t, `requestIdleCallback(() => {})`)
	num, ok := result.(*object.Number)
	if !ok {
		t.Fatalf("expected Number idle ID, got %T", result)
	}
	if num.Value < 1 {
		t.Fatalf("expected idle ID >= 1, got %v", num.Value)
	}
}

func TestRequestIdleCallbackTypeError(t *testing.T) {
	resetTimers(t)
	// 内建函数错误抛出语义修复后，非函数参数应作为 TypeError 异常抛出，
	// 而不是把 Error 对象当作普通返回值压栈。
	_, err := EvalVM(`requestIdleCallback(42)`)
	if err == nil {
		t.Fatalf("expected TypeError to be thrown for non-function argument")
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("expected TypeError, got %v", err)
	}
}

// getGlobalArray 从 VM 全局环境取数组变量。
func getGlobalArray(t *testing.T, vm *VM, name string) *object.Array {
	t.Helper()
	val, ok := vm.Globals().Get(name)
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("global %q is %T, want Array", name, val)
	}
	return arr
}

func TestRequestIdleCallbackFiresWithDeadline(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		requestIdleCallback((deadline) => {
			log.push(deadline.didTimeout);
			log.push(deadline.timeRemaining());
		});
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	log := getGlobalArray(t, vm, "log")
	if len(log.Elements) != 2 {
		t.Fatalf("expected idle callback to run once (2 log entries), got %d", len(log.Elements))
	}
	if b, ok := log.Elements[0].(*object.Boolean); !ok || b.Value {
		t.Fatalf("expected didTimeout=false, got %v", log.Elements[0].Inspect())
	}
	rem, ok := log.Elements[1].(*object.Number)
	if !ok {
		t.Fatalf("expected timeRemaining() to return Number, got %T", log.Elements[1])
	}
	// 无其他任务时预算为 IdleBudget (50ms), 回调内立即查询应接近 50
	if rem.Value <= 0 || rem.Value > float64(object.IdleBudget/time.Millisecond) {
		t.Fatalf("expected 0 < timeRemaining() <= 50, got %v", rem.Value)
	}
}

func TestCancelIdleCallback(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		let id = requestIdleCallback(() => { log.push("fired"); });
		cancelIdleCallback(id);
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	log := getGlobalArray(t, vm, "log")
	if len(log.Elements) != 0 {
		t.Fatalf("cancelled idle callback still fired: %v", log.Inspect())
	}
}

// 空闲回调应在下一个定时器之前的空闲间隙触发 (类似浏览器行为)。
func TestIdleCallbackFiresBeforeLaterTimer(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		setTimeout(() => { log.push("timer"); }, 30);
		requestIdleCallback(() => { log.push("idle"); });
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	log := getGlobalArray(t, vm, "log")
	if len(log.Elements) != 2 {
		t.Fatalf("expected 2 callbacks, got %d: %v", len(log.Elements), log.Inspect())
	}
	if s := log.Elements[0].Inspect(); s != "idle" {
		t.Fatalf("expected idle callback first, got %v", s)
	}
	if s := log.Elements[1].Inspect(); s != "timer" {
		t.Fatalf("expected timer callback second, got %v", s)
	}
}

// options.timeout: 持续繁忙 (interval 不断到期, 事件循环永远没有空闲间隙) 时,
// 超时到期的空闲回调必须被强制派发, 且 deadline.didTimeout === true。
func TestIdleCallbackTimeoutForcesDispatch(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		let busy = setInterval(() => {}, 1);
		requestIdleCallback((deadline) => {
			clearInterval(busy);
			log.push(deadline.didTimeout);
			log.push(deadline.timeRemaining());
		}, { timeout: 30 });
		"started";
	`)
	until := time.Now().Add(300 * time.Millisecond)
	if err := vm.RunTimersUntil(until); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	log := getGlobalArray(t, vm, "log")
	if len(log.Elements) != 2 {
		t.Fatalf("expected idle callback to fire by timeout, got %d log entries", len(log.Elements))
	}
	if b, ok := log.Elements[0].(*object.Boolean); !ok || !b.Value {
		t.Fatalf("expected didTimeout=true, got %v", log.Elements[0].Inspect())
	}
	if rem, ok := log.Elements[1].(*object.Number); !ok || rem.Value != 0 {
		t.Fatalf("expected timeRemaining()=0 on timeout dispatch, got %v", log.Elements[1].Inspect())
	}
}

// 调度器层: Go 回调直接注册。
func TestRequestIdleGoCallback(t *testing.T) {
	resetTimers(t)
	done := make(chan object.Value, 1)
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testIdle", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			done <- args[0]
		} else {
			done <- nil
		}
		return object.UndefinedSingleton
	})
	object.GlobalScheduler().RequestIdle(fn, 0)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	select {
	case deadline := <-done:
		obj, ok := deadline.(*object.Object)
		if !ok {
			t.Fatalf("expected deadline Object argument, got %T", deadline)
		}
		if _, found := obj.GetProperty("timeRemaining"); !found {
			t.Fatal("deadline object missing timeRemaining")
		}
		if _, found := obj.GetProperty("didTimeout"); !found {
			t.Fatal("deadline object missing didTimeout")
		}
	default:
		t.Fatal("idle Go callback did not fire")
	}
}
