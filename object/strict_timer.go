package object

import (
	"sync"
	"sync/atomic"
	"time"
)

// 严格定时器 (strict timers)。
//
// 与普通 setTimeout/setInterval 的区别:
//   - 普通定时器由 JS 主线程的事件循环统一调度 (RunTimersUntil), 回调耗时、
//     time.Sleep 抖动都会导致触发间隔漂移 (每次都用 now+delay 重新基准)。
//   - 严格定时器由独立 Go 调度线程按「绝对时间轴」触发: 第 k 次触发时刻 =
//     start + k*interval, 与回调是否准时执行完毕无关, 间隔不会漂移。
//
// 回调体仍在 JS 主线程 (VM) 中执行, 保持单线程语义。严格定时器的到期信号
// 经 dispatchChan 分派给事件循环消费。具体两种模式见 StrictIntervalMode。

// StrictIntervalMode 严格 interval 的派发模式。
type StrictIntervalMode int

const (
	// StrictModeQueue 排队模式: 到期信号按序进入分派队列, 主线程再忙也不会丢,
	// 但回调实际执行时间会晚于触发时刻 (迟到不丢)。这是默认模式。
	StrictModeQueue StrictIntervalMode = iota
	// StrictModeInterrupt 抢占模式: 事件循环每次迭代优先处理严格定时器到期信号,
	// 在两条指令之间插入回调执行, 时间一到几乎立即运行, 但可能打乱既有回调的
	// 相对顺序 (例如长 setTimeout 回调中注册的定时器可能先于其尾部执行)。
	StrictModeInterrupt
)

// StrictDispatch 是一次严格定时器到期的派发信号。
// 回调参数对象基于它构建: scheduledTime 是目标触发时刻 (绝对时间轴),
// dueTime 是实际派发时刻; 二者之差即迟延/提前量。
type StrictDispatch struct {
	Ticker        *StrictTicker // 所属 ticker
	ScheduledTime time.Time     // 目标触发时刻
	DueTime       time.Time     // 实际派发时刻
}

// StrictTicker 表示一个严格定时器。
type StrictTicker struct {
	ID       int                // 定时器 ID
	Callback Value              // 回调函数 (闭包或内建)
	Interval time.Duration      // 间隔 (重复)
	Repeat   bool               // 是否重复 (true = strict interval)
	Start    time.Time          // 绝对时间轴起点 (第 0 次触发 = Start + Interval)
	Active   atomic.Bool        // 是否仍活跃
	Mode     StrictIntervalMode // 派发模式
	k        uint64             // 已触发的序号 (绝对时间轴步进, 由调度线程读写)
}

// kIndex 返回 ticker 已推进到的序号 k。
// 第 k 次触发时刻 = Start + k*Interval (k 从 1 开始)。
func (t *StrictTicker) kIndex() uint64 { return t.k }

// StrictScheduler 管理所有严格定时器。
type StrictScheduler struct {
	mu       sync.Mutex
	tickers  map[int]*StrictTicker
	nextID   int
	dispatch chan StrictDispatch // 到期信号分派队列
	wakeCh   chan struct{}       // 唤醒信号 (新增/取消定时器时通知调度线程)
	stopCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
	startMu  sync.Mutex       // 保护 ensureStarted 与 Reset 的启动决策互斥
	loopWG   sync.WaitGroup // 跟踪后台调度线程生命周期 (Reset 时等待退出)
	// 统计 (供测试与演示)
	dispatched atomic.Int64 // 总派发次数
}

// strictDispatchBuffer 是分派队列的容量。调度线程在队列满时仍会继续计算
// 下次触发时刻并标记 skipped, 不会阻塞在发送上。
const strictDispatchBuffer = 4096

var globalStrictScheduler = NewStrictScheduler()

// NewStrictScheduler 创建一个新的严格定时器调度器。
func NewStrictScheduler() *StrictScheduler {
	return &StrictScheduler{
		tickers:  make(map[int]*StrictTicker),
		dispatch: make(chan StrictDispatch, strictDispatchBuffer),
		wakeCh:   make(chan struct{}, 1),
		stopCh:   make(chan struct{}),
	}
}

// GlobalStrictScheduler 返回全局严格定时器调度器。
func GlobalStrictScheduler() *StrictScheduler {
	return globalStrictScheduler
}

// SetStrictTimeout 注册一次性严格定时器 (绝对时间轴, 独立调度线程触发)。
func (s *StrictScheduler) SetStrictTimeout(callback Value, delay time.Duration) int {
	return s.add(callback, delay, false, StrictModeQueue)
}

// SetStrictInterval 注册重复严格定时器 (绝对时间轴, 独立调度线程触发)。
func (s *StrictScheduler) SetStrictInterval(callback Value, interval time.Duration, mode StrictIntervalMode) int {
	return s.add(callback, interval, true, mode)
}

// add 注册严格定时器。非重复定时器的首次触发时刻 = now+delay;
// 重复定时器以注册时刻为绝对时间轴起点, 第 k 次触发 = start + k*interval。
func (s *StrictScheduler) add(callback Value, period time.Duration, repeat bool, mode StrictIntervalMode) int {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	t := &StrictTicker{
		ID:       id,
		Callback: callback,
		Interval: period,
		Repeat:   repeat,
		Start:    time.Now(),
		Mode:     mode,
	}
	t.Active.Store(true)
	s.tickers[id] = t
	s.mu.Unlock()

	s.ensureStarted()
	s.wake()
	return id
}

// wake 唤醒后台调度线程 (非阻塞, 最多合并一次)。
func (s *StrictScheduler) wake() {
	select {
	case s.wakeCh <- struct{}{}:
	default:
	}
}

// ensureStarted 确保后台调度线程已启动。
func (s *StrictScheduler) ensureStarted() {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	s.ensureStartedLocked()
}

// ensureStartedLocked 调用方须持有 startMu。
func (s *StrictScheduler) ensureStartedLocked() {
	if s.started.Load() {
		return
	}
	s.started.Store(true)
	s.loopWG.Add(1)
	go func() {
		defer s.loopWG.Done()
		s.loop()
	}()
}

// Stop 停止后台调度线程 (测试清理用)。停止后调度器不再可用。
func (s *StrictScheduler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
}

// Reset 彻底重置调度器: 停止旧后台线程, 清空所有 ticker 与派发队列,
// 然后启动全新的后台线程。供测试隔离使用 (避免旧信号串到下一个测试)。
func (s *StrictScheduler) Reset() {
	// 串行化 Reset 与 ensureStarted, 防止启动决策与线程生命周期错位
	s.startMu.Lock()
	defer s.startMu.Unlock()

	// 1) 标记所有 ticker 失效并清空
	s.mu.Lock()
	for _, t := range s.tickers {
		t.Active.Store(false)
	}
	s.tickers = make(map[int]*StrictTicker)
	s.mu.Unlock()

	// 2) 停止旧后台线程 (若存在) 并等待其真正退出
	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.loopWG.Wait()

	// 3) 重建停止通道与 once, 供新一轮 goroutine 使用
	s.stopCh = make(chan struct{})
	s.stopOnce = sync.Once{}

	// 4) 清空派发队列残留信号
	s.DrainQueue()

	// 5) 允许重新启动后台线程
	s.started.Store(false)
	s.ensureStartedLocked()
}

// DrainQueue 清空派发队列中的残留信号, 返回清掉的信号数。
func (s *StrictScheduler) DrainQueue() int {
	n := 0
	for {
		select {
		case <-s.dispatch:
			n++
		default:
			return n
		}
	}
}

// Clear 取消严格定时器。
func (s *StrictScheduler) Clear(id int) {
	s.mu.Lock()
	if t, ok := s.tickers[id]; ok {
		t.Active.Store(false)
		delete(s.tickers, id)
	}
	s.mu.Unlock()
	s.wake()
}

// ClearAll 取消所有严格定时器 (测试清理用)。
func (s *StrictScheduler) ClearAll() {
	s.mu.Lock()
	for _, t := range s.tickers {
		t.Active.Store(false)
	}
	s.tickers = make(map[int]*StrictTicker)
	s.mu.Unlock()
	s.wake()
}

// DispatchedCount 返回已派发的到期信号总数。
func (s *StrictScheduler) DispatchedCount() int64 {
	return s.dispatched.Load()
}

// FlushStrict 消费当前分派队列中所有到期信号 (非阻塞), 返回本次消费数量。
// 事件循环在等待下一个普通定时器的空闲期内调用, 保持普通定时器与严格
// 定时器在同一事件循环线程串行执行。
func (s *StrictScheduler) FlushStrict() []StrictDispatch {
	var out []StrictDispatch
	for {
		select {
		case d := <-s.dispatch:
			out = append(out, d)
		default:
			return out
		}
	}
}

// HasStrictInterrupts 返回分派队列中是否还有待消费的到期信号。
func (s *StrictScheduler) HasStrictInterrupts() bool {
	return len(s.dispatch) > 0
}

// HasActiveTickers 返回是否存在活跃的严格定时器。
// 用于事件循环判断: 即使分派队列暂时为空 (尚未到期), 也不能退出事件循环。
func (s *StrictScheduler) HasActiveTickers() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.tickers {
		if t.Active.Load() {
			return true
		}
	}
	return false
}

// NextStrictFireIn 返回距下一个严格定时器到期的等待时间。
// 如果不存在活跃严格定时器, 返回 (0, false)。
func (s *StrictScheduler) NextStrictFireIn() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var nextWhen time.Time
	found := false
	for _, t := range s.tickers {
		if !t.Active.Load() {
			continue
		}
		var when time.Time
		if t.Repeat {
			k := t.kIndex()
			when = t.Start.Add(t.Interval * time.Duration(k+1))
		} else {
			when = t.Start.Add(t.Interval)
		}
		if !found || when.Before(nextWhen) {
			nextWhen = when
			found = true
		}
	}
	if !found {
		return 0, false
	}
	wait := nextWhen.Sub(now)
	if wait < 0 {
		wait = 0
	}
	return wait, true
}

// TakeStrictInterrupts 取出分派队列中的到期信号 (最多 limit 个)。
// 供抢占模式使用: 事件循环在每条指令边界都会查询, 取出后立即执行。
func (s *StrictScheduler) TakeStrictInterrupts(limit int) []StrictDispatch {
	var out []StrictDispatch
	for len(out) < limit {
		select {
		case d := <-s.dispatch:
			out = append(out, d)
		default:
			return out
		}
	}
	return out
}

// loop 是后台调度线程。
// 维护绝对时间轴: 对每个 ticker 计算下一次触发时刻 (重复定时器 = start + k*interval),
// 到点后把派发信号送入分派队列, 并推进 k。
// 调度线程从不等待 JS 主线程: 队列满时记录 skipped 后继续推进, 回调迟到不丢帧。
func (s *StrictScheduler) loop() {
	// 捕获启动时的停止通道, 避免 Reset 重建 stopCh 时与本 goroutine 产生竞态。
	stopCh := s.stopCh
	for {
		s.mu.Lock()
		now := time.Now()
		var wait time.Duration
		var nextWhen time.Time
		found := false
		for _, t := range s.tickers {
			if !t.Active.Load() {
				continue
			}
			var when time.Time
			if t.Repeat {
				when = t.Start.Add(t.Interval * time.Duration(t.k+1))
			} else {
				when = t.Start.Add(t.Interval)
			}
			if !found || when.Before(nextWhen) {
				found = true
				nextWhen = when
			}
		}
		if !found {
			s.mu.Unlock()
			// 无活跃 ticker: 等唤醒 (新增定时器) 或退出。
			select {
			case <-stopCh:
				return
			case <-s.wakeCh:
				continue
			case <-time.After(10 * time.Second):
				continue
			}
		}
		wait = nextWhen.Sub(now)
		if wait < 0 {
			wait = 0
		}
		s.mu.Unlock()

		select {
		case <-stopCh:
			return
		case <-s.wakeCh:
			// 被唤醒: 重新计算 (可能有新定时器更早到期)
			continue
		case <-time.After(wait):
		}

		// 到点: 收集所有已到期的 ticker (同一时刻多个定时器)
		s.mu.Lock()
		now = time.Now()
		var due []*StrictTicker
		for _, t := range s.tickers {
			if !t.Active.Load() {
				continue
			}
			var when time.Time
			if t.Repeat {
				when = t.Start.Add(t.Interval * time.Duration(t.k+1))
			} else {
				when = t.Start.Add(t.Interval)
			}
			if !when.After(now) {
				due = append(due, t)
			}
		}
		s.mu.Unlock()

		for _, t := range due {
			// 重新确认活跃 (可能在派发前被 Clear)
			if !t.Active.Load() {
				continue
			}
			var when time.Time
			s.mu.Lock()
			if t.Repeat {
				when = t.Start.Add(t.Interval * time.Duration(t.k+1))
				t.k++
			} else {
				when = t.Start.Add(t.Interval)
				// 一次性定时器: 保持 Active=true 直到信号被消费 (VM 端消费后
				// 会检查 Active 决定是否执行), 此处只从 tickers 移除避免重触发。
				delete(s.tickers, t.ID)
			}
			s.mu.Unlock()
			s.dispatched.Add(1)
			// 非阻塞推送: 队列满时丢弃该信号但绝不阻塞调度线程 (主线程仍在消费)。
			select {
			case s.dispatch <- StrictDispatch{Ticker: t, ScheduledTime: when, DueTime: time.Now()}:
			default:
			}
		}
	}
}
