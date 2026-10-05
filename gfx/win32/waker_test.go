//go:build windows

package win32

import (
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
)

// TestWaitEventsWakeOnPost 在**真机真窗口**上验证 win32 的 waker: 建一个真
// 窗口, 让另一 goroutine 延迟 gfx.Post 一个任务, 用**无限期**预算睡进
// WaitEventsWake(0, gfx.WakeChan())。
//
// 为什么必须是无限期预算: 只有把 gfx 的唤醒通道并进
// MsgWaitForMultipleObjectsEx 的等待集合, 无限期等待才会在 Post 那一刻醒来;
// 若用有限预算, "被唤醒"与"超时返回"在时间上分不开, 这条断言就失去意义。
//
// 为什么单开真机用例: 假 Surface (gfx/waker_test.go) 验的是内核 Pump 的调度,
// 而"唤醒到底有没有落到平台等待集合"只有真窗口 + 真消息泵能验。真机没桌面
// (无头 CI) 时建窗失败, 这里 t.Skip 而不是判红。
//
// 为什么是**循环**等任务执行而不是等一次返回: 窗口建在测试 goroutine 当时的
// OS 线程 T1 上, hwnd 的消息 (创建后异步到达的 WM_PAINT 等) 进 T1 的线程
// 队列; WaitEventsWake 的 goroutine 若恰好也被调度到 T1, MsgWait 的
// MWMO_INPUTAVAILABLE 会因队列里已有未分发消息而**立即返回** —— 这是平台
// 层的正常假唤醒, 不是唤醒失效。循环把它吸收掉: 假唤醒 → DrainTasks 为空 →
// 再等。而"唤醒真的坏了"表现为 WaitEventsWake(0) 永不返回, 被外层 3s
// deadline 兜底判红 —— falsification 口径不变。
func TestWaitEventsWakeOnPost(t *testing.T) {
	s, err := newSurface(gfx.WindowConfig{Title: "gox-waker-test", Width: 160, Height: 100})
	if err != nil {
		t.Skipf("建窗失败 (无桌面环境?): %v", err)
	}
	surf := s.(*surface)
	defer procDestroyWindow.Call(uintptr(surf.hwnd))

	ran := make(chan struct{})
	go func() {
		time.Sleep(80 * time.Millisecond)
		gfx.Post(func() { close(ran) })
	}()

	// WaitEventsWake 阻塞等待, 放到 goroutine 里跑并循环投递每次返回, 外层
	// 用硬超时兜底 (实现坏掉时不会把整个 go test 挂到 timeout)。
	// 事件唤醒是跨线程的, 不需要落在建窗线程上 —— 这里刻意不 LockOSThread。
	done := make(chan bool, 1)
	go func() {
		for surf.WaitEventsWake(0, gfx.WakeChan()) {
			done <- true
		}
		done <- false
	}()

	start := time.Now()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-ran:
			if elapsed := time.Since(start); elapsed > 2*time.Second {
				t.Fatalf("Post 的任务执行太晚 (%v), 疑似靠轮询而非唤醒", elapsed)
			}
			return
		case <-deadline:
			t.Fatal("Post 的任务未在 3s 内执行 —— 唤醒通道没有并进 MsgWait 等待集合")
		case alive := <-done:
			if !alive {
				t.Fatal("WaitEventsWake 报告窗口已关闭")
			}
			gfx.DrainTasks()
		}
	}
}
