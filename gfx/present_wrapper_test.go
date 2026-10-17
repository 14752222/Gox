package gfx

import (
	"image"
	"sync/atomic"
	"testing"
	"time"
)

// ===== 呈现包装器链 (SetPresentWrapper / wrapPresent) =====
//
// 这三个用例钉两件事:
//  1. 多个包装器**按注册顺序叠加** (先注册包在里层), 不是互相覆盖 ——
//     GPU 与软媒体同时启用靠这条 (见 gfx.go 的层次约定);
//  2. 单个包装器返回 ok=false 时被跳过, 链继续 —— 降级而不失败。

// countingWrapper 记录自己被包装过几次, 并把自己的 inner 记下来 (断言层次用)。
type countingWrapper struct {
	name  string
	inner Surface
	hits  int32
}

func (w *countingWrapper) Show(img *image.RGBA) { w.inner.Show(img) }
func (w *countingWrapper) ShowRegions(img *image.RGBA, r []image.Rectangle) {
	w.inner.ShowRegions(img, r)
}
func (w *countingWrapper) Size() (int, int)                      { return w.inner.Size() }
func (w *countingWrapper) WaitEvents(maxWait time.Duration) bool { return w.inner.WaitEvents(maxWait) }
func (w *countingWrapper) Events() <-chan Event                  { return w.inner.Events() }

func TestWrapPresentChainComposesInOrder(t *testing.T) {
	SetPresentWrapper(nil) // 复位 (链是包级状态)
	defer SetPresentWrapper(nil)

	inner := &countingWrapper{name: "inner"}
	outer := &countingWrapper{name: "outer"}
	SetPresentWrapper(func(s Surface) (Surface, bool) {
		atomic.AddInt32(&inner.hits, 1)
		inner.inner = s
		return inner, true
	})
	SetPresentWrapper(func(s Surface) (Surface, bool) {
		atomic.AddInt32(&outer.hits, 1)
		outer.inner = s
		return outer, true
	})

	platform := newFakeSurface()
	got := wrapPresent(platform)

	if got != Surface(outer) {
		t.Fatalf("链终点该是最外层包装器, 实际 %T", got)
	}
	if inner.inner != Surface(platform) {
		t.Fatalf("内层包装器该包住平台面, 实际 %T", inner.inner)
	}
	if outer.inner != Surface(inner) {
		t.Fatalf("外层包装器该包住内层包装器, 实际 %T", outer.inner)
	}
}

func TestWrapPresentSkipsFailingWrapper(t *testing.T) {
	SetPresentWrapper(nil)
	defer SetPresentWrapper(nil)

	SetPresentWrapper(func(s Surface) (Surface, bool) { return nil, false }) // 不包装
	SetPresentWrapper(func(s Surface) (Surface, bool) { return nil, true })  // nil 面也当不包装

	platform := newFakeSurface()
	if got := wrapPresent(platform); got != Surface(platform) {
		t.Fatalf("两个包装器都失败时该原样返回平台面, 实际 %T", got)
	}
}

func TestWrapPresentEmptyChain(t *testing.T) {
	SetPresentWrapper(nil)
	defer SetPresentWrapper(nil)
	platform := newFakeSurface()
	if got := wrapPresent(platform); got != Surface(platform) {
		t.Fatalf("空链该原样返回, 实际 %T", got)
	}
}

// ===== app 分持: 能力断言在平台面, 上屏走包装面 =====
//
// 这是 render() 里 a.surface / a.present 分开持的全部意义: 包装器内嵌的是
// Surface 接口, inner 的可选能力不会被提升 —— 让剪贴板/对话框/光标/IME/waker
// 也走包装面的话, 每加一个包装器就悄悄吃掉一层平台能力。

// dumbWrapper 模仿"只接管上屏"的包装器 (softmedia 的形状): 除 Surface 五个
// 方法外什么都不实现 —— 包括不转发任何可选能力。
type dumbWrapper struct{ Surface }

func TestAppCapabilityAssertionStaysOnPlatformSurface(t *testing.T) {
	resetClipboard()
	clipMu.Lock()
	clipText = "平台剪贴板"
	clipMu.Unlock()

	fake := newFakeSurface()
	fake.dialog = &fakeDialogHost{}
	a := &app{
		surface:    fake,
		present:    &dumbWrapper{Surface: fake},
		root:       mkNode("column", nil),
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	registerApp(a)
	defer unregisterApp(a)

	// 剪贴板能力: 断言发生在 a.surface (平台面), 包装面没有该方法也该找得到
	c, ok := clipboardBackend()
	if !ok {
		t.Fatal("包装器挡掉了剪贴板能力 —— 能力断言该走平台面")
	}
	got, err := c.ReadClipboardText()
	if err != nil || got != "平台剪贴板" {
		t.Fatalf("剪贴板读取该来自平台面, 实际 %q, %v", got, err)
	}

	// 原生对话框同理
	d, ok := dialogBackend()
	if !ok {
		t.Fatal("包装器挡掉了原生对话框能力")
	}
	if _, err := d.ShowMessage(DialogInfo, "t", "m"); err != nil {
		t.Fatalf("对话框该由平台面应答, 实际 %v", err)
	}

	// 上屏路径: 该走包装面 (视频帧合成就挂在这一跳上)
	if ps := a.presentSurface(); ps == Surface(fake) {
		t.Fatal("present 非 nil 时 presentSurface 该返回包装面")
	}
}

func TestAppPresentSurfaceFallback(t *testing.T) {
	// 测试直构 &app{} 不设 present: 回退到 surface (呈现路径两者同一个)
	fake := newFakeSurface()
	a := &app{surface: fake}
	if ps := a.presentSurface(); ps != Surface(fake) {
		t.Fatalf("present 为 nil 该回退到 surface, 实际 %T", ps)
	}
}

// wakerForwarder 是"正确转发 waker"的包装器 (softmedia 的形状): 等待路径
// 经它走, 于是它能在 WaitEvents 返回 false 时看到窗口关闭。
type wakerForwarder struct {
	Surface
	wakeHits int32
}

func (w *wakerForwarder) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	atomic.AddInt32(&w.wakeHits, 1)
	inner, ok := w.Surface.(interface {
		WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool
	})
	if !ok {
		return w.Surface.WaitEvents(maxWait)
	}
	return inner.WaitEventsWake(maxWait, wake)
}

func TestAppWaitEventsGoesThroughWrapperWaker(t *testing.T) {
	drainWake()
	t.Cleanup(func() { DrainTasks(); drainWake() })

	inner := &wakerSurface{fakeSurface: newFakeSurface()}
	wrapped := &wakerForwarder{Surface: inner}
	a := &app{surface: inner, present: wrapped}
	registerApp(a)
	defer unregisterApp(a)

	Post(func() {}) // 唤醒 token: wakerSurface 只在 wake 上醒 (见 waker_test.go)
	if !a.waitEvents(2 * time.Second) {
		t.Fatal("窗口活着时 waitEvents 不该报关闭")
	}
	if !inner.usedWake.Load() {
		t.Fatal("waker 该经包装器转发到平台面 (usedWake 未被置起)")
	}
	if atomic.LoadInt32(&wrapped.wakeHits) != 1 {
		t.Fatal("包装器的 WaitEventsWake 该被调用 (等待路径要看得见关闭)")
	}
}

func TestAppWaitEventsNoWrapperHitsPlatformWaker(t *testing.T) {
	drainWake()
	t.Cleanup(func() { DrainTasks(); drainWake() })

	// 无包装器: waker 断言直接命中平台面, 行为与重构前一致
	inner := &wakerSurface{fakeSurface: newFakeSurface()}
	a := &app{surface: inner}
	registerApp(a)
	defer unregisterApp(a)

	Post(func() {})
	if !a.waitEvents(2 * time.Second) {
		t.Fatal("窗口活着时 waitEvents 不该报关闭")
	}
	if !inner.usedWake.Load() {
		t.Fatal("无包装器时 waker 该直接命中平台面 (usedWake 未被置起)")
	}
}
