package object

import (
	"sort"
	"sync"
	"time"
)

// Timer 表示一个定时器。
type Timer struct {
	ID       int           // 定时器 ID
	Callback Value         // 回调函数
	Delay    time.Duration // 延迟/间隔
	Repeat   bool          // 是否重复 (setInterval)
	FireAt   time.Time     // 下次触发时间
	Active   bool          // 是否仍活跃
}

// IdleCallback 表示一个空闲回调 (requestIdleCallback)。
type IdleCallback struct {
	ID        int       // 回调 ID (与定时器共用同一 ID 计数器)
	Callback  Value     // 回调函数, 接收 deadline 对象参数
	TimeoutAt time.Time // 超时截止时间 (零值 = 无超时, 只能等空闲期)
	Active    bool      // 是否仍活跃
}

// IdleBudget 是单个空闲期分配给回调的最大时间预算 (浏览器为 50ms)。
const IdleBudget = 50 * time.Millisecond

// IdleMinGap 是启动空闲期所需的最小事件循环间隙。
// 间隙小于该值时不派发空闲回调 (类比浏览器: 间隙太短不值得切空闲期),
// 此时带 timeout 的回调只能等超时强制派发。
const IdleMinGap = 10 * time.Millisecond

// pendingTaskPoll 是存在外部挂起任务时事件循环的轮询间隔。
// goroutine 完成工作后通过 SetTimeout 把回调交给事件循环执行，
// 主线程最迟在一个轮询周期后就能发现它。
const pendingTaskPoll = 10 * time.Millisecond

// idleDispatchGap 是两次空闲派发之间的最小间隔,
// 防止回调内再次 requestIdleCallback 造成热自旋 (类比浏览器的帧节流)。
const idleDispatchGap = time.Millisecond

// TimerScheduler 管理所有定时器。
type TimerScheduler struct {
	mu     sync.Mutex
	timers map[int]*Timer
	// inFlight 记录已被 DueTimers 取出、正在等待执行或重新调度的定时器。
	//
	// 为什么需要: DueTimers 会把到期定时器从 timers 中移除。若回调内部
	// 调用 clearInterval(自身 id) 或 clearTimeout(同批到期的兄弟 id)，
	// Clear 在 timers 里查不到就什么都不做，t.Active 仍为 true，
	// 随后 Reschedule 又把它塞回 timers —— 定时器无法在回调内取消。
	inFlight map[int]*Timer
	idle     map[int]*IdleCallback
	// pendingTasks 记录由 Go 侧 goroutine 承载的外部任务数 (HTTP 服务器
	// 监听、进行中的 fetch 等)。事件循环把它们当作唤醒源保活，否则
	// "没有到期定时器" 时循环直接退出，服务器刚 listen 完进程就结束了。
	pendingTasks     int
	nextTaskToken    int
	nextID           int
	lastIdleDispatch time.Time // 上次空闲派发时间 (零值 = 从未)
}

var globalScheduler = NewTimerScheduler()

// NewTimerScheduler 创建一个新的定时器调度器。
func NewTimerScheduler() *TimerScheduler {
	return &TimerScheduler{
		timers:   make(map[int]*Timer),
		inFlight: make(map[int]*Timer),
		idle:     make(map[int]*IdleCallback),
	}
}

// GlobalScheduler 返回全局定时器调度器。
func GlobalScheduler() *TimerScheduler {
	return globalScheduler
}

// SetTimeout 注册一次性定时器。
func (s *TimerScheduler) SetTimeout(callback Value, delay time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	t := &Timer{
		ID:       s.nextID,
		Callback: callback,
		Delay:    delay,
		Repeat:   false,
		FireAt:   time.Now().Add(delay),
		Active:   true,
	}
	s.timers[t.ID] = t
	return t.ID
}

// SetInterval 注册重复定时器。
func (s *TimerScheduler) SetInterval(callback Value, interval time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	t := &Timer{
		ID:       s.nextID,
		Callback: callback,
		Delay:    interval,
		Repeat:   true,
		FireAt:   time.Now().Add(interval),
		Active:   true,
	}
	s.timers[t.ID] = t
	return t.ID
}

// Clear 取消定时器。
//
// 必须同时清理 timers 与 inFlight: 回调执行期间定时器已不在 timers 中，
// 只删 timers 会导致 clearInterval(自身 id) 无效。
func (s *TimerScheduler) Clear(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.timers[id]; ok {
		t.Active = false
		delete(s.timers, id)
	}
	if t, ok := s.inFlight[id]; ok {
		t.Active = false
		delete(s.inFlight, id)
	}
}

// ClearAll 取消所有定时器和空闲回调 (用于测试清理)。
func (s *TimerScheduler) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.timers {
		t.Active = false
	}
	for _, t := range s.inFlight {
		t.Active = false
	}
	s.timers = make(map[int]*Timer)
	s.inFlight = make(map[int]*Timer)
	for _, cb := range s.idle {
		cb.Active = false
	}
	s.idle = make(map[int]*IdleCallback)
	s.pendingTasks = 0
}

// NextFireIn 返回下一个需要唤醒的等待时间。
// 唤醒源包括: 到期定时器、空闲回调的超时截止时间、外部挂起任务。
// 如果三者都没有，返回 false。
func (s *TimerScheduler) NextFireIn() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	next := time.Duration(0)
	found := false
	consider := func(at time.Time) {
		wait := at.Sub(now)
		if wait < 0 {
			wait = 0
		}
		if !found || wait < next {
			next = wait
			found = true
		}
	}
	for _, t := range s.timers {
		if t.Active {
			consider(t.FireAt)
		}
	}
	for _, cb := range s.idle {
		if cb.Active && !cb.TimeoutAt.IsZero() {
			consider(cb.TimeoutAt)
		}
	}
	// 存在挂起任务时: 有定时器则把等待时间截断到轮询间隔，
	// 保证 goroutine 侧新注册的回调定时器 (如 fetch 完成回调) 能及时被
	// 事件循环发现；无定时器则以轮询间隔保活循环不退出。
	if s.pendingTasks > 0 {
		if !found || next > pendingTaskPoll {
			next = pendingTaskPoll
			found = true
		}
	}
	return next, found
}

// DueTimers 返回所有已到期的活跃定时器 (从调度器中移除，但保留引用供重新调度)。
// 重复定时器由调用方重新调度。
func (s *TimerScheduler) DueTimers() []*Timer {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var due []*Timer
	for id, t := range s.timers {
		if !t.Active {
			continue
		}
		if !t.FireAt.After(now) {
			due = append(due, t)
			delete(s.timers, id)
			s.inFlight[id] = t
		}
	}
	// Go 的 map 遍历顺序是随机的，直接返回会导致 setTimout(a,0);
	// setTimeout(b,0) 的执行顺序不确定。按 (FireAt, ID) 排序保证
	// 到期时间早的先执行，同刻到期的按注册顺序 (FIFO)。
	sort.Slice(due, func(i, j int) bool {
		if due[i].FireAt.Equal(due[j].FireAt) {
			return due[i].ID < due[j].ID
		}
		return due[i].FireAt.Before(due[j].FireAt)
	})
	return due
}

// Reschedule 重新调度重复定时器。
// 调用方 (事件循环) 必须对 DueTimers 返回的每个定时器都调用一次本方法 ——
// 即使是一次性定时器，也需要它来清理 inFlight 记录。
func (s *TimerScheduler) Reschedule(t *Timer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// 执行阶段结束: 移出 inFlight。若期间被 Clear，Active 已为 false，
	// 下面的判断会跳过重新调度。
	delete(s.inFlight, t.ID)
	if !t.Repeat || !t.Active {
		return
	}
	t.FireAt = time.Now().Add(t.Delay)
	s.timers[t.ID] = t
}

// ===== requestIdleCallback 支持 =====

// RequestIdle 注册一个空闲回调。
// timeout > 0 时, 即使始终未进入空闲期, 回调最迟也会在 timeout 到期时被派发。
func (s *TimerScheduler) RequestIdle(callback Value, timeout time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	cb := &IdleCallback{
		ID:       s.nextID,
		Callback: callback,
		Active:   true,
	}
	if timeout > 0 {
		cb.TimeoutAt = time.Now().Add(timeout)
	}
	s.idle[cb.ID] = cb
	return cb.ID
}

// CancelIdle 取消空闲回调 (cancelIdleCallback)。
func (s *TimerScheduler) CancelIdle(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cb, ok := s.idle[id]; ok {
		cb.Active = false
		delete(s.idle, id)
	}
}

// HasIdleCallbacks 返回是否存在待派发的空闲回调。
func (s *TimerScheduler) HasIdleCallbacks() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, cb := range s.idle {
		if cb.Active {
			return true
		}
	}
	return false
}

// TakeIdleCallbacks 取出所有待派发的空闲回调 (从调度器移除)。
// 同时记录本次派发时间用于节流 (两次派发至少间隔 idleDispatchGap)。
// 返回值 didTimeout 标记每个回调是否因超时被迫派发。
func (s *TimerScheduler) TakeIdleCallbacks() []*IdleCallback {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []*IdleCallback
	for id, cb := range s.idle {
		if !cb.Active {
			continue
		}
		out = append(out, cb)
		delete(s.idle, id)
	}
	if len(out) > 0 {
		s.lastIdleDispatch = now
	}
	return out
}

// ExpiredIdleCallbacks 取出所有已超时 (TimeoutAt 已过) 的空闲回调。
// 这些回调无论是否空闲都将被强制派发 (deadline.didTimeout = true)。
func (s *TimerScheduler) ExpiredIdleCallbacks() []*IdleCallback {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var out []*IdleCallback
	for id, cb := range s.idle {
		if !cb.Active {
			continue
		}
		if !cb.TimeoutAt.IsZero() && !cb.TimeoutAt.After(now) {
			out = append(out, cb)
			delete(s.idle, id)
		}
	}
	if len(out) > 0 {
		s.lastIdleDispatch = now
	}
	return out
}

// IdleDispatchCooldown 返回距下一次空闲派发还需等待的时间。
// 0 表示可以立即派发。用于对回调内反复 requestIdleCallback 节流。
func (s *TimerScheduler) IdleDispatchCooldown() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastIdleDispatch.IsZero() {
		return 0
	}
	wait := idleDispatchGap - time.Since(s.lastIdleDispatch)
	if wait < 0 {
		return 0
	}
	return wait
}

// ===== 外部挂起任务 (事件循环保活) =====
//
// HTTP 服务器监听、进行中的 fetch 等任务由 Go 侧 goroutine 承载，
// 在调度器里没有对应的定时器。若不显式保活，事件循环在"无到期定时器、
// 无空闲回调"时返回，脚本里刚 listen 完进程就退出了。
//
// 约定: goroutine 启动前 AddPendingTask() 领取令牌，任务结束或取消时
// FinishPendingTask(token) 归还。计数归零后事件循环即可正常退出。
// goroutine 完成后应通过 SetTimeout 把 JS 回调交回主线程执行，
// 不要在 goroutine 里直接调用 CallFunction。

// AddPendingTask 注册一个外部挂起任务，返回归还用的令牌。
func (s *TimerScheduler) AddPendingTask() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingTasks++
	s.nextTaskToken++
	return s.nextTaskToken
}

// FinishPendingTask 归还 AddPendingTask 领取的令牌。
// 未知令牌会被忽略，因此重复归还是安全的。
func (s *TimerScheduler) FinishPendingTask(token int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token <= 0 || token > s.nextTaskToken {
		return
	}
	if s.pendingTasks > 0 {
		s.pendingTasks--
	}
}

// PendingTasks 返回当前外部挂起任务数。
func (s *TimerScheduler) PendingTasks() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pendingTasks
}
