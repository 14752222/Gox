package hosttest

// ===== Surface 契约 (依据均为 gfx/gfx.go 的 Surface / waker 注释, 见包注释清单) =====

import (
	"image"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
)

// SurfaceFactory 与 gfx.WindowFactory.Create 同签名 —— 平台侧传 factory.Create
// (或等价的包级创建函数) 即可。
type SurfaceFactory func(gfx.WindowConfig) (gfx.Surface, error)

// RunSurfaceContract 对窗口后端跑全套 Surface 契约断言 (清单见包注释)。
func RunSurfaceContract(t *testing.T, factory SurfaceFactory) {
	t.Helper()
	if factory == nil {
		t.Fatalf("hosttest: 传入 nil factory —— 契约测的是真实窗口后端")
	}
	s, err := factory(gfx.WindowConfig{Title: "hosttest-contract", Width: 320, Height: 240})
	if err != nil {
		t.Fatalf("工厂创建 Surface 失败: %v", err)
	}
	if s == nil {
		t.Fatalf("工厂返回 (nil, nil) —— Surface 与 error 不能同时为空")
	}

	t.Run("size-positive", func(t *testing.T) {
		w, h := s.Size()
		if w <= 0 || h <= 0 {
			t.Errorf("Size() = (%d, %d): 客户区尺寸应为正 (gfx.go Surface 注释)", w, h)
		}
	})
	t.Run("show-full-and-regions", func(t *testing.T) {
		checkShowSemantics(t, s)
	})
	t.Run("wait-events-alive-true", func(t *testing.T) {
		checkWaitEventsAlive(t, s)
	})
	t.Run("waker-if-implemented", func(t *testing.T) {
		checkWaker(t, s)
	})
}

// checkShowSemantics 断言三种上屏形态都被接受 (依据: Show 整帧; ShowRegions
// 脏矩形局部呈现, rects 为空 = 全帧)。黑盒只能断言「接受、不 panic」—— 脏矩形
// 有没有真的只重绘那几块, 是后端内部实现, 平台自测另想办法 (真后端可以肉眼/截图)。
func checkShowSemantics(f failer, s gfx.Surface) {
	f.Helper()
	w, h := s.Size()
	if w <= 0 || h <= 0 {
		w, h = 64, 64 // 尺寸问题由 size-positive 报; 这里别让 NewRGBA 连锁 panic
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	s.Show(img)
	s.ShowRegions(img, []image.Rectangle{
		image.Rect(0, 0, w/2, h/2),
		image.Rect(w/2, h/2, w, h),
	})
	s.ShowRegions(img, nil)                 // nil = 全帧
	s.ShowRegions(img, []image.Rectangle{}) // 空切片同样是「rects 为空」= 全帧
}

// checkWaitEventsAlive 断言 WaitEvents 的活窗口语义 (依据: 「事件源关闭 (窗口
// 销毁) 时返回 false」—— 反过来, 活着时超时醒来必须返回 true, 否则 Pump 会把
// 活窗口当成已销毁)。关闭方向黑盒无法触发 (Surface 接口没有 Close), 由自测的
// 假 Surface 演示 (hosttest_test.go 的 TestFakeSurfaceCloseSemantics)。
func checkWaitEventsAlive(f failer, s gfx.Surface) {
	f.Helper()
	start := time.Now()
	if !s.WaitEvents(20 * time.Millisecond) {
		f.Errorf("窗口活着时 WaitEvents 返回 false —— false 的唯一含义是事件源关闭 (gfx.go Surface 注释), 活着返回 false 会让 Pump 以为窗口没了")
	}
	if d := time.Since(start); d > 5*time.Second {
		f.Errorf("WaitEvents(20ms) 花了 %v 才返回 —— maxWait 是等待上限, 不是建议值", d)
	}
}

// waker 是 gfx 的未导出可选接口的**结构镜像**: 接口类型未导出但方法是导出的,
// 包外用匿名接口断言即可 (gfx.go waker 注释: 方法名必须导出, 与 capturer /
// clipboardHost 同理)。
type waker interface {
	WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool
}

// checkWaker 断言 waker 的「真并入等待集合」语义 (依据: gfx.go waker 注释 ——
// 「而不是把 maxWait 切成小片轮询, 后者只是把无限期睡死换成有界轮询」)。
//
// 手法: 调用后 100ms 才投递 wake token。真并入的实现 ~100ms 醒; 粗切片轮询的
// 实现要等下一个切片边界 (≥ 一个片长)。阈值 1s —— 片长 <1s 的轮询黑盒测不出,
// 如实承认 (包注释黑盒极限)。
func checkWaker(f failer, s gfx.Surface) {
	f.Helper()
	w, ok := s.(waker)
	if !ok {
		f.Logf("该后端未实现 waker 可选接口: 合法退化 (gfx.go waker 注释: 自动退化为 WaitEvents + postDrainCap 上限, 空闲期多几次空转)")
		return
	}
	wake := make(chan struct{}, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		wake <- struct{}{}
	}()
	start := time.Now()
	if !w.WaitEventsWake(30*time.Second, wake) {
		f.Errorf("窗口活着时 WaitEventsWake 返回 false —— false 只表示事件源关闭")
	}
	if d := time.Since(start); d > time.Second {
		f.Errorf("wake 在 100ms 时投递, WaitEventsWake 却花了 %v 才返回 —— 像是把 maxWait 切片轮询, 而不是把 wake 并入等待集合 (gfx.go waker 注释禁止)", d)
	}
	// 超时分支: 空 wake + 小 maxWait, 应按点醒来并返回 true (超时 ≠ 关闭)。
	start = time.Now()
	if !w.WaitEventsWake(30*time.Millisecond, nil) {
		f.Errorf("窗口活着时 WaitEventsWake(空 wake, 30ms) 返回 false —— 超时醒来不是事件源关闭")
	}
	if d := time.Since(start); d > 5*time.Second {
		f.Errorf("WaitEventsWake(30ms) 花了 %v 才返回 —— maxWait 是等待上限", d)
	}
}
