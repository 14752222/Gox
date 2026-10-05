//go:build windows

package win32

import (
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
)

// TestWaitEventsWakeOnPost 在**真机真窗口**上验证 win32 的 waker: 建一个真
// 窗口, 让另一 goroutine 延迟 gfx.Post 一个任务, 这里用**无限期**预算睡进
// WaitEventsWake(0, gfx.WakeChan())。
//
// 为什么必须是无限期预算: 只有把 gfx 的唤醒通道并进
// MsgWaitForMultipleObjectsEx 的等待集合, 无限期等待才会在 Post 那一刻醒来;
// 若用有限预算, "被唤醒"与"超时返回"在时间上分不开, 这条断言就失去意义。
//
// 为什么单开真机用例: 假 Surface (gfx/waker_test.go) 验的是内核 Pump 的调度,
// 而"唤醒到底有没有落到平台等待集合"只有真窗口 + 真消息泵能验。真机没桌面
// (无头 CI) 时建窗失败, 这里 t.Skip 而不是判红。
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

	// WaitEventsWake 阻塞等待, 放到 goroutine 里跑, 这样外层能用硬超时兜底
	// (实现坏掉时不会把整个 go test 挂到 timeout)。
	// 事件唤醒是跨线程的, 不需要落在建窗线程上 —— 这里刻意不 LockOSThread。
	done := make(chan bool, 1)
	start := time.Now()
	go func() { done <- surf.WaitEventsWake(0, gfx.WakeChan()) }()

	select {
	case alive := <-done:
		if !alive {
			t.Fatal("WaitEventsWake 报告窗口已关闭")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("WaitEventsWake 没有在 Post 唤醒下醒来, 耗时 %v", elapsed)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitEventsWake 未在 3s 内被 Post 唤醒 —— 唤醒通道没有并进 MsgWait 等待集合")
	}

	gfx.DrainTasks()
	select {
	case <-ran:
	default:
		t.Fatal("Post 的任务未被 DrainTasks 执行")
	}
}
