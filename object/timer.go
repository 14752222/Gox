package object

import (
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

// TimerScheduler 管理所有定时器。
type TimerScheduler struct {
	mu      sync.Mutex
	timers  map[int]*Timer
	nextID  int
}

var globalScheduler = NewTimerScheduler()

// NewTimerScheduler 创建一个新的定时器调度器。
func NewTimerScheduler() *TimerScheduler {
	return &TimerScheduler{
		timers: make(map[int]*Timer),
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
func (s *TimerScheduler) Clear(id int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.timers[id]; ok {
		t.Active = false
		delete(s.timers, id)
	}
}

// ClearAll 取消所有定时器 (用于测试清理)。
func (s *TimerScheduler) ClearAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.timers {
		t.Active = false
	}
	s.timers = make(map[int]*Timer)
}

// NextFireIn 返回下一个定时器触发的等待时间。
// 如果没有定时器，返回 false。
func (s *TimerScheduler) NextFireIn() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	next := time.Duration(0)
	found := false
	for _, t := range s.timers {
		if !t.Active {
			continue
		}
		wait := t.FireAt.Sub(now)
		if wait < 0 {
			wait = 0
		}
		if !found || wait < next {
			next = wait
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
		}
	}
	return due
}

// Reschedule 重新调度重复定时器。
func (s *TimerScheduler) Reschedule(t *Timer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !t.Repeat || !t.Active {
		return
	}
	t.FireAt = time.Now().Add(t.Delay)
	s.timers[t.ID] = t
}
