package vm

import (
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== 事件泵与定时器调度的交接 (看板 rczZT2) =====
//
// 症状: "GUI + 定时器 + 异步 I/O" 一撞上就界面僵住 —— 第一个 tick 之后
// 再无任何回调, 也不报错。
//
// 根因是两套语义在同一个 0 上撞车:
//   - runTimersLoop 里 wait==0 的含义是 "已经有定时器到期了, 别等, 马上交回
//     事件循环" (NextFireIn 把已到期的时间差钳成 0)。
//   - gfx.Pump(maxWait) 的契约把 maxWait<=0 解释成 "无限期等待外部事件",
//     真机后端 (X11/win32) 据此永久睡进 WaitEvents。
//
// 于是那个已经到期的定时器永远派发不出去, 而且后续定时器全部停摆。
// 触发只需要一个 0ms 定时器 —— 异步 fs / fetch 的回调都是
// SetTimeout(..., 0) 注册的 (stdlib/fs.go 的 fsSchedule)。
//
// 本文件用 "原样建模真机后端" 的泵来锁死这个契约: 收到 maxWait<=0 就永远
// 不返回。修复前这些测试会超时, 修复后定时器照常派发。

// blockingPump 按真机后端契约建模的事件泵。
//
// maxWait<=0 时永久阻塞 (对应 WaitEvents 的无限期等待), 直到 release 被
// 关闭才返回 false (对应事件源关闭)。
//
// maxWait>0 时**立刻返回** —— 契约写的是 "等待外部事件或超时, 二者先到
// 即返回", 所以 "队列里已有事件、马上返回" 是合法实现, 而且是事件循环的
// 最坏情况: 泵还没等到定时器到期就回来了, 循环重新算 wait, 下一次必然算
// 出 0。真机上鼠标一动就是这个局面。
//
// 收到的每次 maxWait 都记进 waits, 便于失败时给出可诊断的信息。
type blockingPump struct {
	release chan struct{}

	mu    sync.Mutex
	waits []time.Duration
}

func newBlockingPump() *blockingPump {
	return &blockingPump{release: make(chan struct{})}
}

// pump 是交给 RunTimersWithPump 的回调。
func (p *blockingPump) pump(maxWait time.Duration) bool {
	p.mu.Lock()
	p.waits = append(p.waits, maxWait)
	p.mu.Unlock()

	if maxWait <= 0 {
		// 真机后端: 永久睡进 WaitEvents, 直到窗口被销毁。
		<-p.release
		return false
	}
	return true // 已有事件, 不等到超时就返回
}

// snapshot 返回到目前为止收到的所有 maxWait。
func (p *blockingPump) snapshot() []time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]time.Duration(nil), p.waits...)
}

// tail 返回最后 n 次收到的 maxWait —— 失败信息里用; 急切泵会转出成千上万
// 条记录, 全量打印只会把真正的线索淹掉。
func (p *blockingPump) tail(n int) []time.Duration {
	all := p.snapshot()
	if len(all) > n {
		all = all[len(all)-n:]
	}
	return all
}

// stop 释放被阻塞的泵, 让事件循环干净退出 (避免 goroutine 泄漏)。
func (p *blockingPump) stop() { close(p.release) }

// TestPumpDueTimerDoesNotStarve 已到期的定时器不能因为泵收到 0 而饿死。
//
// 这是 rczZT2 的最小复现: 一个 0ms 定时器 (异步 I/O 回调的注册方式) +
// 一个会永久阻塞的泵。修复前泵收到 wait=0 后不再返回, 回调永远不执行;
// 修复后 wait 被钳成正值, 泵马上交回事件循环, 回调照常派发。
func TestPumpDueTimerDoesNotStarve(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `let z = 0; z;`)

	p := newBlockingPump()
	defer p.stop()

	fired := make(chan struct{}, 1)
	object.GlobalScheduler().SetTimeout(
		object.NewBuiltin("asyncCb", func(args ...object.Value) object.Value {
			select {
			case fired <- struct{}{}:
			default:
			}
			return object.UndefinedSingleton
		}),
		0, // 已到期 —— 与 stdlib/fs.go fsSchedule 的注册方式一致
	)

	errCh := make(chan error, 1)
	go func() { errCh <- vm.RunTimersWithPump(p.pump) }()

	select {
	case <-fired:
		// 预期路径: 到期的定时器派发出去了。
	case <-time.After(3 * time.Second):
		t.Fatalf("已到期的定时器被饿死: 泵收到 maxWait=%v 后永久阻塞, 回调再没派发 (看板 rczZT2)",
			p.tail(5))
	}
}

// TestPumpIntervalKeepsTicking 重复定时器在永久阻塞的泵下不能冻住。
//
// 对应真机上的完整症状: 界面完全僵住、后续 tick 全部停摆。修复前第一个
// tick 之后泵被 0 卡死, count 停在极低值; 修复后按 interval 持续触发。
func TestPumpIntervalKeepsTicking(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `let z = 0; z;`)

	p := newBlockingPump()
	defer p.stop()

	var mu sync.Mutex
	count := 0
	id := object.GlobalScheduler().SetInterval(
		object.NewBuiltin("tick", func(args ...object.Value) object.Value {
			mu.Lock()
			count++
			mu.Unlock()
			return object.UndefinedSingleton
		}),
		2*time.Millisecond,
	)
	t.Cleanup(func() { object.GlobalScheduler().Clear(id) })

	errCh := make(chan error, 1)
	go func() { errCh <- vm.RunTimersWithPump(p.pump) }()

	deadline := time.After(3 * time.Second)
	for {
		mu.Lock()
		c := count
		mu.Unlock()
		if c >= 5 {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("interval 被冻住: 3 秒内只触发 %d 次 (泵收到 maxWait=%v, 看板 rczZT2)",
				c, p.tail(5))
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestPumpStillGetsInfiniteWaitWhenIdle 反向护栏: 没有定时器时仍必须传 0。
//
// 修 rczZT2 时容易 "顺手" 把钳位加到所有 pump 调用上, 那会毁掉 GUI 空闲
// 期的语义 —— 没有定时任务时泵本该无限期等待外部事件 (只靠鼠标/键盘唤醒),
// 一旦被钳成 1ms 就退化成忙轮询, CPU 空转。这条测试锁住那条路径。
func TestPumpStillGetsInfiniteWaitWhenIdle(t *testing.T) {
	resetTimers(t)
	vm, _ := runEvalVM(t, `let z = 0; z;`)

	got := make(chan time.Duration, 4)
	// 收到一次就返回 false, 让事件循环立即退出 (无需 goroutine/超时)。
	pump := func(maxWait time.Duration) bool {
		select {
		case got <- maxWait:
		default:
		}
		return false
	}

	if err := vm.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump error: %v", err)
	}
	select {
	case w := <-got:
		if w != 0 {
			t.Fatalf("空闲期泵应收到 0 (无限期等待外部事件), 实际收到 %v —— 钳位误伤了空闲路径", w)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("空闲期泵没有被调用")
	}
}

// TestMinPumpWaitIsBounded 钳位常量本身必须是 "正的且足够小"。
//
// 0 会退回到被修的那个 bug; 过大则无谓拖慢事件循环的空转周期。
func TestMinPumpWaitIsBounded(t *testing.T) {
	if minPumpWait <= 0 {
		t.Fatalf("minPumpWait 必须为正, 0 会退回 rczZT2 的永久阻塞, 当前 %v", minPumpWait)
	}
	if minPumpWait > 10*time.Millisecond {
		t.Fatalf("minPumpWait 过大会拖慢事件循环, 当前 %v", minPumpWait)
	}
}
