package object

import (
	"testing"
	"time"
)

// 外部挂起任务 (HTTP 服务器监听、进行中的 fetch 等) 是事件循环的唤醒源:
// 计数 > 0 时 NextFireIn 以轮询间隔保活循环，归零后循环可正常退出。

func TestPendingTasksKeepLoopAlive(t *testing.T) {
	s := NewTimerScheduler()

	if _, found := s.NextFireIn(); found {
		t.Fatal("empty scheduler should have no wake source")
	}

	token := s.AddPendingTask()
	wait, found := s.NextFireIn()
	if !found {
		t.Fatal("pending task should keep loop alive")
	}
	if wait <= 0 || wait > pendingTaskPoll {
		t.Fatalf("pending task poll wait = %v, want (0, %v]", wait, pendingTaskPoll)
	}

	// 重复归还未知令牌是安全操作，计数只被归还一次
	s.FinishPendingTask(token)
	s.FinishPendingTask(token)
	if s.PendingTasks() != 0 {
		t.Fatalf("PendingTasks = %d, want 0", s.PendingTasks())
	}
	if _, found := s.NextFireIn(); found {
		t.Fatal("loop should exit after all pending tasks done")
	}
}

func TestPendingTaskCounts(t *testing.T) {
	s := NewTimerScheduler()
	if s.PendingTasks() != 0 {
		t.Fatalf("initial PendingTasks = %d", s.PendingTasks())
	}
	t1 := s.AddPendingTask()
	s.AddPendingTask()
	if s.PendingTasks() != 2 {
		t.Fatalf("PendingTasks = %d, want 2", s.PendingTasks())
	}
	s.FinishPendingTask(t1)
	if s.PendingTasks() != 1 {
		t.Fatalf("PendingTasks = %d, want 1", s.PendingTasks())
	}
	// 未知令牌: 忽略
	s.FinishPendingTask(-1)
	s.FinishPendingTask(99999)
	if s.PendingTasks() != 1 {
		t.Fatalf("unknown token should be ignored, PendingTasks = %d", s.PendingTasks())
	}
	s.ClearAll()
	if s.PendingTasks() != 0 {
		t.Fatalf("ClearAll should reset PendingTasks, got %d", s.PendingTasks())
	}
}

// 挂起任务存在时, 唤醒间隔被截断到轮询间隔 (保证 goroutine 侧注册的
// 回调定时器最迟一个轮询周期后被事件循环发现)。
func TestPendingTasksCapWait(t *testing.T) {
	s := NewTimerScheduler()
	s.AddPendingTask()
	s.SetTimeout(nil, 50*time.Millisecond)
	wait, found := s.NextFireIn()
	if !found {
		t.Fatal("timer should be a wake source")
	}
	if wait > pendingTaskPoll {
		t.Fatalf("wait = %v should be capped at poll interval %v", wait, pendingTaskPoll)
	}
}
