package hosttest

// ===== hosttest 自用的最小假后端 =====
//
// 两个用途: ① RunNativeHostContract 的 set-nil 子测试经它挂窗口跑事件泵;
// ② 包内自测拿它跑 RunSurfaceContract。它的 WaitEventsWake 真正把 wake 并入
// select, 是「waker 该怎么写」的参照实现 (gfx.go waker 注释点名批评的是切片
// 轮询, 这里给出对照组)。

import (
	"image"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
)

// fakeSurface 是 Surface + waker 的测试替身: 无真窗口, 事件由 push 注入,
// close 后 WaitEvents/WaitEventsWake 返回 false (演示「false = 事件源关闭」)。
type fakeSurface struct {
	events  chan gfx.Event
	arrived chan struct{} // WaitEvents 的唤醒信号 (不消费 events)
	w, h    int

	mu        sync.Mutex
	closed    bool
	shown     int
	lastRects []image.Rectangle
	fullFrame bool // 最近一次 ShowRegions 的 rects 为空 (= 全帧)
}

func newFakeSurface() *fakeSurface {
	return &fakeSurface{
		events:  make(chan gfx.Event, 16),
		arrived: make(chan struct{}, 16),
		w:       400, h: 300,
	}
}

func (f *fakeSurface) Show(img *image.RGBA) { f.ShowRegions(img, nil) }

func (f *fakeSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	f.mu.Lock()
	f.shown++
	f.lastRects = rects
	f.fullFrame = len(rects) == 0
	f.mu.Unlock()
}

func (f *fakeSurface) Size() (int, int) { return f.w, f.h }

// isClosed 读关闭标志 (锁内快照)。
func (f *fakeSurface) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeSurface) WaitEvents(maxWait time.Duration) bool {
	if f.isClosed() {
		return false
	}
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	select {
	case <-f.arrived:
	case <-time.After(maxWait):
	}
	return !f.isClosed()
}

// WaitEventsWake 实现 waker 可选接口: wake 与平台事件 (arrived) 在同一个
// select 里 —— 这就是「并入等待集合」在 Go channel 世界的形状。
func (f *fakeSurface) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	if f.isClosed() {
		return false
	}
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	select {
	case <-f.arrived:
	case <-wake:
	case <-time.After(maxWait):
	}
	return !f.isClosed()
}

func (f *fakeSurface) Events() <-chan gfx.Event { return f.events }

// push 注入一个窗口事件并唤醒 WaitEvents (与 gfx 包测试假 Surface 同款)。
func (f *fakeSurface) push(ev gfx.Event) {
	f.events <- ev
	f.arrived <- struct{}{}
}

// close 模拟事件源关闭 (窗口销毁): 之后的 WaitEvents/WaitEventsWake 返回 false。
func (f *fakeSurface) close() {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	select {
	case f.arrived <- struct{}{}: // 唤醒睡着的 WaitEvents 让它看到关闭
	default:
	}
}

// fakeFactory 把 fakeSurface 适配成 gfx.WindowFactory。
type fakeFactory struct{ s *fakeSurface }

func (f *fakeFactory) Create(gfx.WindowConfig) (gfx.Surface, error) { return f.s, nil }
