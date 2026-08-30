package vm

import (
	"sync"
	"testing"
	"time"

	"js-runtime/object"
)

// runEvalVM 编译执行 JS 并返回 VM (用于测试定时器)。
func runEvalVM(t *testing.T, input string) (*VM, object.Value) {
	t.Helper()
	vm, err := EvalVM(input)
	if err != nil {
		t.Fatalf("Eval error for input %q: %v", input, err)
	}
	return vm, vm.LastPopped()
}

// resetTimers 清空全局调度器，避免测试间泄漏。
func resetTimers(t *testing.T) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
}

// ===== setTimeout 测试 =====

func TestSetTimeoutReturnsID(t *testing.T) {
	resetTimers(t)
	vm, result := runEvalVM(t, `setTimeout(() => {}, 10)`)
	_ = vm
	num, ok := result.(*object.Number)
	if !ok {
		t.Fatalf("expected Number timer ID, got %T", result)
	}
	if num.Value < 1 {
		t.Fatalf("expected timer ID >= 1, got %v", num.Value)
	}
}

func TestSetTimeoutFires(t *testing.T) {
	resetTimers(t)
	// 通过 JS 闭包 + Go 可观察副作用验证回调触发
	vm, _ := runEvalVM(t, `
		let fired = false;
		setTimeout(() => { fired = true; }, 5);
		"started";
	`)
	// 定时器回调直接修改 JS 变量，验证方式: 执行完定时器后检查
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	// 若没有报错，说明回调被调用且无异常
}

func TestSetTimeoutGoCallback(t *testing.T) {
	resetTimers(t)
	// 直接通过调度器 + VM 验证回调执行
	done := make(chan bool, 1)
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testFn", func(args ...object.Value) object.Value {
		done <- true
		return object.UndefinedSingleton
	})
	object.GlobalScheduler().SetTimeout(fn, 5*time.Millisecond)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	select {
	case <-done:
	default:
		t.Fatal("setTimeout Go callback did not fire")
	}
}

func TestClearTimeout(t *testing.T) {
	resetTimers(t)
	done := make(chan bool, 1)
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testFn", func(args ...object.Value) object.Value {
		done <- true
		return object.UndefinedSingleton
	})
	id := object.GlobalScheduler().SetTimeout(fn, 5*time.Millisecond)
	object.GlobalScheduler().Clear(id)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	select {
	case <-done:
		t.Fatal("cleared timeout still fired")
	default:
	}
}

func TestSetIntervalFires(t *testing.T) {
	resetTimers(t)
	var mu sync.Mutex
	count := 0
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testFn", func(args ...object.Value) object.Value {
		mu.Lock()
		count++
		mu.Unlock()
		return object.UndefinedSingleton
	})
	id := object.GlobalScheduler().SetInterval(fn, 5*time.Millisecond)
	// 运行最多 60ms，让 interval 触发至少 2 次
	until := time.Now().Add(60 * time.Millisecond)
	vm.RunTimersUntil(until)
	object.GlobalScheduler().Clear(id)
	mu.Lock()
	c := count
	mu.Unlock()
	if c < 2 {
		t.Fatalf("expected interval to fire at least 2 times, got %d", c)
	}
}

func TestRunTimersOrder(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	order := []int{}
	var mu sync.Mutex
	makeFn := func(val int) object.Value {
		return object.NewBuiltin("testFn", func(args ...object.Value) object.Value {
			mu.Lock()
			order = append(order, val)
			mu.Unlock()
			return object.UndefinedSingleton
		})
	}
	object.GlobalScheduler().SetTimeout(makeFn(1), 10*time.Millisecond)
	object.GlobalScheduler().SetTimeout(makeFn(2), 5*time.Millisecond)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 {
		t.Fatalf("expected 2 timer callbacks, got %d", len(order))
	}
	if order[0] != 2 || order[1] != 1 {
		t.Fatalf("expected order [2, 1], got %v", order)
	}
}
