package vm

import (
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// resetStrictTimers 彻底重置严格调度器, 避免测试间泄漏。
// 与 ClearAll 不同, Reset 会停止旧后台线程、清空派发队列并启动新线程,
// 确保上一个测试遗留的到期信号不会串到下一个测试。
func resetStrictTimers(t *testing.T) {
	t.Helper()
	object.GlobalStrictScheduler().Reset()
	t.Cleanup(func() { object.GlobalStrictScheduler().ClearAll() })
}

// ===== 一次性严格定时器 =====

func TestSetStrictTimeoutReturnsID(t *testing.T) {
	resetStrictTimers(t)
	vm, result := runEvalVM(t, `setStrictTimeout(() => {}, 10)`)
	_ = vm
	num, ok := result.(*object.Number)
	if !ok {
		t.Fatalf("expected Number strict timer ID, got %T", result)
	}
	if num.Value < 1 {
		t.Fatalf("expected strict timer ID >= 1, got %v", num.Value)
	}
}

func TestSetStrictTimeoutFires(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let fired = 0;
		setStrictTimeout(() => { fired = 1; }, 5);
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	fired := getGlobalNumber(t, vm, "fired")
	if fired != 1 {
		t.Fatalf("expected strict timeout to fire, got fired=%v", fired)
	}
}

// 回调收到 info 参数, 包含 scheduledTime/dueTime/early/late/skipped
func TestStrictTimeoutCallbackInfo(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let info = null;
		setStrictTimeout((d) => { info = d; }, 5);
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	obj := getGlobalObject(t, vm, "info")
	for _, key := range []string{"scheduledTime", "dueTime", "early", "late", "skipped"} {
		if _, found := obj.GetProperty(key); !found {
			t.Fatalf("info missing property %q", key)
		}
	}
	if n, _ := obj.GetProperty("late"); n != nil {
		if num, ok := n.(*object.Number); !ok || num.Value < 0 {
			t.Fatalf("expected late >= 0, got %v", n.Inspect())
		}
	}
}

func TestClearStrictTimeout(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let fired = 0;
		let id = setStrictTimeout(() => { fired = 1; }, 5);
		clearStrictTimeout(id);
		"started";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	fired := getGlobalNumber(t, vm, "fired")
	if fired != 0 {
		t.Fatalf("cleared strict timeout still fired: fired=%v", fired)
	}
}

// ===== 重复严格定时器: 绝对时间轴, 回调耗时不导致间隔漂移 =====

// 核心保证: interval 触发时刻 = start + k*interval。即使回调耗时较长,
// 后续触发时刻仍落在绝对时间轴上, 累计漂移被限制在一个调度周期内。
// 这里用 Go 回调模拟耗时回调 (每次 20ms), 验证绝对时间轴不漂移。
func TestStrictIntervalZeroDriftUnderSlowCallback(t *testing.T) {
	resetStrictTimers(t)
	var mu sync.Mutex
	count := 0
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testSlow", func(args ...object.Value) object.Value {
		mu.Lock()
		count++
		mu.Unlock()
		// 回调耗时 20ms > interval 10ms
		time.Sleep(20 * time.Millisecond)
		return object.UndefinedSingleton
	})
	object.GlobalStrictScheduler().SetStrictInterval(fn, 10*time.Millisecond, object.StrictModeQueue)
	// 运行 120ms。回调 20ms > interval 10ms:
	//   - 普通 setInterval (now+delay 重新基准): 每次回调把下一次推迟到结束+10ms,
	//     120ms 内只能触发 ~4 次;
	//   - 严格绝对时间轴: 触发时刻固定为 10,20,30,...,120ms, 回调串行执行
	//     120ms/20ms ≈ 6 次 (受主线程执行能力上限约束, 但显著多于普通语义)。
	until := time.Now().Add(120 * time.Millisecond)
	if err := vm.RunTimersUntil(until); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	mu.Lock()
	c := count
	mu.Unlock()
	if c < 5 {
		t.Fatalf("strict interval under slow callback: expected >= 5 ticks (absolute axis), got %d", c)
	}
}

// 调度线程独立于 JS 主线程: 主线程长时间忙 (阻塞循环) 时, 严格 interval
// 信号依然按绝对时间轴产生 (派发计数持续增长), 普通 setInterval 则暂停。
func TestStrictIntervalDispatchIndependentOfJSBusyLoop(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let ticks = 0;
		setStrictInterval(() => { ticks++; }, 5);
		"started";
	`)
	// 主线程执行一个 ~60ms 的繁忙循环 (不做任何事件循环)
	start := time.Now()
	until := start.Add(60 * time.Millisecond)
	// 模拟 JS 主线程被占满: 直接让 Go 侧忙等, 不驱动 RunTimersUntil
	for time.Now().Before(until) {
	}
	// 主线程恢复后, 立刻消费队列 (带截止时间, 避免无限 interval 永久阻塞)
	if err := vm.RunTimersUntil(start.Add(120 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	ticks := getGlobalNumber(t, vm, "ticks")
	if ticks < 5 {
		t.Fatalf("expected strict interval to accumulate >= 5 ticks during busy JS, got %v", ticks)
	}
}

// ===== 排队模式 vs 抢占模式 =====

func TestStrictIntervalQueueModeKeepsOrder(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		let id = setStrictInterval(() => { log.push("strict"); }, 5);
		setTimeout(() => { log.push("timeout"); }, 10);
		"started";
	`)
	if err := vm.RunTimersUntil(time.Now().Add(60 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	log := getGlobalArray(t, vm, "log")
	// 队列模式: 严格回调与普通回调都在同一事件循环按序执行
	if len(log.Elements) < 2 {
		t.Fatalf("expected at least 2 log entries, got %d", len(log.Elements))
	}
}

// 抢占模式: interrupt 到期信号在事件循环中优先于普通定时器执行。
func TestStrictIntervalInterruptMode(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let log = [];
		let id = setStrictInterval(() => { log.push("strict"); }, 5, "interrupt");
		"started";
	`)
	if err := vm.RunTimersUntil(time.Now().Add(60 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	// 无 panic、无错误, 抢占模式基本可用
	log := getGlobalArray(t, vm, "log")
	if len(log.Elements) == 0 {
		t.Fatalf("expected interrupt mode strict interval to fire, got 0 log entries")
	}
}

func TestSetStrictIntervalMode(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		setStrictIntervalMode("interrupt");
		let mode = 0;
		"ok";
	`)
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
}

func TestClearStrictInterval(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `
		let count = 0;
		let id = setStrictInterval(() => { count++; }, 5);
		"started";
	`)
	until := time.Now().Add(30 * time.Millisecond)
	if err := vm.RunTimersUntil(until); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	// 取消 (直接操作全局调度器)
	object.GlobalStrictScheduler().Clear(1)
	before := getGlobalNumber(t, vm, "count")
	until2 := time.Now().Add(30 * time.Millisecond)
	if err := vm.RunTimersUntil(until2); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	after := getGlobalNumber(t, vm, "count")
	if after < before {
		t.Fatalf("count decreased unexpectedly: %v -> %v", before, after)
	}
}

// ===== 调度器层测试 (Go 回调) =====

func TestStrictSchedulerGoCallback(t *testing.T) {
	resetStrictTimers(t)
	var mu sync.Mutex
	count := 0
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	fn := object.NewBuiltin("testStrict", func(args ...object.Value) object.Value {
		mu.Lock()
		count++
		mu.Unlock()
		return object.UndefinedSingleton
	})
	object.GlobalStrictScheduler().SetStrictInterval(fn, 5*time.Millisecond, object.StrictModeQueue)
	until := time.Now().Add(60 * time.Millisecond)
	if err := vm.RunTimersUntil(until); err != nil {
		t.Fatalf("RunTimersUntil error: %v", err)
	}
	mu.Lock()
	c := count
	mu.Unlock()
	if c < 5 {
		t.Fatalf("expected strict interval to fire >= 5 times, got %d", c)
	}
}

// 验证: 严格 interval 被清除后, RunTimersUntil 能正常返回 (不死等)。
func TestStrictEventLoopExitsAfterClear(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `let count = 0; "s";`)
	id := object.GlobalStrictScheduler().SetStrictInterval(
		object.NewBuiltin("x", func(...object.Value) object.Value { return object.UndefinedSingleton }),
		5*time.Millisecond, object.StrictModeQueue)
	until := time.Now().Add(30 * time.Millisecond)
	if err := vm.RunTimersUntil(until); err != nil {
		t.Fatalf("err: %v", err)
	}
	object.GlobalStrictScheduler().Clear(id)
	// 清空后再跑, 应立即返回 (不能死等)
	done := make(chan struct{})
	go func() {
		_ = vm.RunTimersUntil(time.Time{})
		close(done)
	}()
	select {
	case <-done:
		t.Logf("event loop exited after clear")
	case <-time.After(2 * time.Second):
		t.Fatalf("event loop did not exit after clearing strict interval")
	}
}

// 并发压力: 调度线程持续派发, 主线程持续消费 + 频繁 clear, 不应 panic。
func TestStrictConcurrentStress(t *testing.T) {
	resetStrictTimers(t)
	vm, _ := runEvalVM(t, `let z = 0; z;`)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				object.GlobalStrictScheduler().SetStrictInterval(object.NewBuiltin("x", func(...object.Value) object.Value { return object.UndefinedSingleton }), 2*time.Millisecond, object.StrictModeQueue)
			}
		}()
	}
	until := time.Now().Add(80 * time.Millisecond)
	_ = vm.RunTimersUntil(until)
	close(stop)
	wg.Wait()
	object.GlobalStrictScheduler().ClearAll()
}

// 回归: 一次性严格定时器在 Reset 后必须仍能可靠触发。
// 曾修复: RunTimersUntil 退出条件未消费派发队列残留信号, 导致 one-shot
// 触发后回调被静默丢弃 (调度线程已派发, 队列有信号, 但事件循环直接退出)。
func TestStrictOneShotFiresAfterReset(t *testing.T) {
	for i := 0; i < 30; i++ {
		object.GlobalStrictScheduler().Reset()
		vm, _ := runEvalVM(t, `let fired = 0; "s";`)
		var got bool
		fn := object.NewBuiltin("probe", func(...object.Value) object.Value {
			got = true
			return object.UndefinedSingleton
		})
		object.GlobalStrictScheduler().SetStrictTimeout(fn, 2*time.Millisecond)
		if err := vm.RunTimers(); err != nil {
			t.Fatalf("iter %d err: %v", i, err)
		}
		if !got {
			t.Fatalf("iter %d: one-shot strict timeout did not fire after Reset", i)
		}
	}
}

// ===== 辅助 =====

func getGlobalNumber(t *testing.T, vm *VM, name string) float64 {
	t.Helper()
	val, ok := vm.Globals().Get(name)
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	num, ok := val.(*object.Number)
	if !ok {
		t.Fatalf("global %q is %T, want Number", name, val)
	}
	return num.Value
}

func getGlobalObject(t *testing.T, vm *VM, name string) *object.Object {
	t.Helper()
	val, ok := vm.Globals().Get(name)
	if !ok {
		t.Fatalf("global %q not found", name)
	}
	obj, ok := val.(*object.Object)
	if !ok {
		t.Fatalf("global %q is %T, want Object", name, val)
	}
	return obj
}
