// Package mobile 是移动端 (Android / iOS) 后端的**可移植内核部分**:
// 事件队列、触摸→指针映射、帧上传钩子、显示器 (density) 上报、事件泵唤醒。
//
// 为什么把它单独拆出来而不是全塞进 gfx/android:
//   - 这里全是纯 Go —— 在开发机 (Windows) 上就能 `go test ./gfx/mobile` 跑通,
//     而 gfx/android 带 cgo + jni.h, 只能在 android 目标下编译, 完全没法单测;
//   - iOS 的 ObjC 桥之后复用同一份逻辑, 只换"怎么上传像素/怎么收事件"这两层胶水。
//
// 分层的边界 (谁负责什么):
//
//	┌── 宿主壳 (Kotlin / Swift) ──┐  创建 SurfaceView/UIView, 每帧调一次 Tick,
//	│                             │  把触摸/按键/生命周期回调转成下面的方法调用
//	└─────────────┬───────────────┘
//	   gfx/android (cgo+JNI) / gfx/ios (ObjC)   ← 薄胶水: 只做类型转换与线程附加
//	┌─────────────▼───────────────┐
//	│ gfx/mobile (本包, 纯 Go)     │  Surface / Factory / 触摸映射 / 事件泵
//	└─────────────┬───────────────┘
//	              │ gfx.Surface + displayProvider + capturer
//	         gfx 内核 (node/layout/raster/font, 零改动)
//
// 线程模型沿用内核纪律: 脚本、事件泵、VM 回调全在同一个 OS 线程串行。宿主回调
// **只往事件队列里塞事件**, 绝不直接执行 JS —— 与 win32 的 WndProc 同一约束。
//
// v1 边界 (明确写下, 免得被当成 bug):
//   - 单窗口: Factory.Create 一律返回同一个已绑定的 Surface (Android 上"窗口"
//     就是 Activity 的 SurfaceView, 引擎无权创建第二个);
//   - 单指触摸: 多指手势 (捏合/旋转) 不识别, 第二根手指的事件按 Cancel 处理;
//   - 长按不映射右键菜单 (要的是"按住不放"语义, 与鼠标右键不同);
//   - density 只**上报** (Display.Scale / gfx/device 的 pixelRatio), 内核的
//     逻辑像素换算 (layout 缩放) 是 M1 的剩余部分, 不在这里偷偷做。
package mobile

import (
	"errors"
	"image"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
)

// 触摸动作, 与 Android MotionEvent 的动作语义对齐 (取值为本包自定义的稳定常量,
// 胶水层负责把平台的枚举翻过来)。
const (
	TouchDown = iota // 手指按下
	TouchMove        // 移动
	TouchUp          // 抬起
	// TouchCancel 是"这根手指已作废"(被系统抢走 / 被父视图截获 / 第二根手指按下)。
	// 它必须**当成抬起来处理**: 只丢弃不给 MouseUp 的话, 按压态会永久残留
	// (按钮一直是按下去的样子)。
	TouchCancel
)

// Uploader 是宿主注入的像素上传钩子 (Android: 写进 Java 侧的 direct ByteBuffer
// 后调一次"刷新脏区"; iOS: 拷进 CGContext)。
//
// rects 为 nil 表示整帧。**契约**: img 只在本次调用内有效, 实现不得留存指针;
// rects 是脏区 (像素坐标), 实现可以只上传这些区域。
type Uploader func(img *image.RGBA, rects []image.Rectangle)

// Config 是移动端表面的一次性配置。
type Config struct {
	// Width / Height 是**设备像素**, 与脚本看到的窗口尺寸同一个坐标系
	// (Android: SurfaceView 的像素尺寸, 不是 dp)。
	Width, Height int
	// Density 是设备像素比 (1.0 / 2.0 / 2.75 / 3.0 …), <=0 视为 1。
	Density float64
	// ID / Name 是上报给 gx/screen 的显示器标识, 都为空时用 "mobile" / "Mobile"。
	ID, Name string
	// Uploader 见其定义; 为 nil 时上屏变成空操作 (桌面测试里就靠这个观察调用)。
	Uploader Uploader
}

// Surface 实现 gfx.Surface (+ displayProvider / capturer), 由宿主驱动。
type Surface struct {
	mu       sync.Mutex
	w, h     int
	density  float64
	id, name string
	closed   bool

	uploader Uploader

	events chan gfx.Event
	// wake 是 WaitEvents 的唤醒信号, 容量 1: 宿主每帧 Tick 一次叫醒泵,
	// 重复的 Tick 合并成一次 (泵醒来后会把该做的事做完, 不需要计数)。
	wake chan struct{}
	done chan struct{}

	// lastX / lastY 是最近一次触摸位置。Cancel 时用它补一次 MouseUp ——
	// 平台可能不给坐标, 而 MouseUp 的坐标决定了"点在哪"。
	lastX, lastY int
	// fingerDown 记录当前是否有手指按着 (决定 Move 要不要派发)。
	fingerDown bool
}

// eventsCap 是事件队列容量。契约允许"满时丢弃" —— 移动端一帧可能来上百个
// 触摸点, 宁可丢中间点也不能让宿主线程阻塞。
const eventsCap = 256

// New 创建一个移动端表面。
func New(cfg Config) *Surface {
	s := &Surface{
		w:        cfg.Width,
		h:        cfg.Height,
		density:  cfg.Density,
		id:       cfg.ID,
		name:     cfg.Name,
		uploader: cfg.Uploader,
		events:   make(chan gfx.Event, eventsCap),
		wake:     make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	if s.density <= 0 {
		s.density = 1
	}
	if s.id == "" {
		s.id = "mobile"
	}
	if s.name == "" {
		s.name = "Mobile"
	}
	return s
}

// ===== gfx.Surface =====

// Show 上传整帧。
func (s *Surface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

// ShowRegions 上传脏区 (rects 为空 = 整帧)。
func (s *Surface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	if img == nil {
		return
	}
	s.mu.Lock()
	up := s.uploader
	closed := s.closed
	s.mu.Unlock()
	if closed || up == nil {
		return
	}
	up(img, rects)
}

// Size 返回当前表面尺寸 (设备像素)。
func (s *Surface) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Density 返回设备像素比。
func (s *Surface) Density() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.density
}

// WaitEvents 至多等 maxWait, 期间被 Tick / 新事件 / Close 唤醒。
//
// 移动端没有"消息循环"可跑, 这里的等待纯碎是"等宿主叫我们" —— 宿主每帧
// (Choreographer / CADisplayLink) 调一次 Tick, 于是现有
// vm.RunTimersWithPump(gfx.Pump) 原样可用: rAF 的 16ms 自然落在 vsync 上。
// maxWait <= 0 表示不限时 (仍然会被 Tick/事件唤醒, 不会真的睡死)。
func (s *Surface) WaitEvents(maxWait time.Duration) bool {
	if s.isClosed() {
		return false
	}
	var timeout <-chan time.Time
	if maxWait > 0 {
		t := time.NewTimer(maxWait)
		defer t.Stop()
		timeout = t.C
	}
	select {
	case <-s.wake:
	case <-timeout:
	case <-s.done:
		return false
	}
	return !s.isClosed()
}

// Events 返回事件流。
func (s *Surface) Events() <-chan gfx.Event { return s.events }

func (s *Surface) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ===== 宿主驱动接口 =====

// Tick 由宿主每帧调用一次: 叫醒可能正在 WaitEvents 里睡觉的事件泵。
func (s *Surface) Tick() { s.wakeup() }

// Post 投递一条事件 (宿主的按键/IME/生命周期等"非触摸"回调走这里)。
// 事件队列满时丢弃 —— 与 Surface 的事件语义一致 (宁可丢帧不能卡宿主线程)。
func (s *Surface) Post(ev gfx.Event) {
	if s.isClosed() {
		return
	}
	select {
	case s.events <- ev:
	default:
	}
	s.wakeup()
}

// Touch 把一次触摸动作映射成指针事件并投递。
//
// 单指映射规则 (v1):
//
//	Down   → MouseMove(先补一次, 让 hover 态正确) + MouseDown
//	Move   → MouseMove (未按下时也发, 与桌面"鼠标移入"一致)
//	Up     → MouseUp (由内核派发点击)
//	Cancel → MouseUp (若有按压) —— 必须给，否则按压态永久残留
//
// 坐标是**设备像素的客户区坐标**, 与 Size() 同一坐标系 (内核的命中测试就在
// 这个坐标系里)。
func (s *Surface) Touch(action, x, y int) {
	switch action {
	case TouchDown:
		s.mu.Lock()
		s.lastX, s.lastY = x, y
		s.fingerDown = true
		s.mu.Unlock()
		s.Post(gfx.Event{Kind: gfx.EventMouseMove, X: x, Y: y})
		s.Post(gfx.Event{Kind: gfx.EventMouseDown, X: x, Y: y})
	case TouchMove:
		s.mu.Lock()
		s.lastX, s.lastY = x, y
		s.mu.Unlock()
		s.Post(gfx.Event{Kind: gfx.EventMouseMove, X: x, Y: y})
	case TouchUp:
		s.mu.Lock()
		s.lastX, s.lastY, s.fingerDown = x, y, false
		s.mu.Unlock()
		s.Post(gfx.Event{Kind: gfx.EventMouseUp, X: x, Y: y})
	case TouchCancel:
		s.mu.Lock()
		x, y, down := s.lastX, s.lastY, s.fingerDown
		s.fingerDown = false
		s.mu.Unlock()
		if down {
			// 按下过的才补 MouseUp (没按下就发一条会凭空触发一次点击)。
			s.Post(gfx.Event{Kind: gfx.EventMouseUp, X: x, Y: y})
		}
	}
}

// Resize 报告表面尺寸/密度变化 (旋转、分屏、折叠态切换)。
// 尺寸真变了才投递 EventResize —— 内核收到它会整帧重绘并把 onResize 派发给
// 根节点, 重复投递等于白白重排一次。
func (s *Surface) Resize(w, h int, density float64) {
	if w <= 0 || h <= 0 {
		return
	}
	s.mu.Lock()
	changed := s.w != w || s.h != h
	s.w, s.h = w, h
	if density > 0 {
		s.density = density
	}
	s.mu.Unlock()
	if changed {
		s.Post(gfx.Event{Kind: gfx.EventResize, W: w, H: h})
	}
}

// SetUploader 在运行时替换上传钩子 (Android 侧 attach 到 JVM 之后才能拿到
// direct ByteBuffer 的地址, 所以是"先建表面、后装钩子")。
func (s *Surface) SetUploader(up Uploader) {
	s.mu.Lock()
	s.uploader = up
	s.mu.Unlock()
}

// Close 结束这一段会话: WaitEvents 之后一律返回 false, 事件泵随之退出。
// 幂等 (宿主可能同时收到 destroy 与 detach 两次回调)。
func (s *Surface) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.mu.Unlock()
	close(s.done)
	s.wakeup()
}

func (s *Surface) wakeup() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// ===== displayProvider (显示器 + density 上报) =====

// Displays 上报本设备唯一的一块屏。
//
// 关键字段是 Scale (设备像素比): gx/screen 的 windowInfo()/screen() 与 gx/device
// 的 pixelRatio 都从它派生, 脚本据此做"dp → px"的换算 —— 这是移动端相对桌面
// 唯一真正新增的屏幕参数。
func (s *Surface) Displays() []gfx.Display {
	s.mu.Lock()
	w, h, density, id, name := s.w, s.h, s.density, s.id, s.name
	s.mu.Unlock()
	return []gfx.Display{{
		ID:      id,
		Name:    name,
		W:       w,
		H:       h,
		WorkX:   0,
		WorkY:   0,
		WorkW:   w,
		WorkH:   h,
		Scale:   density,
		Primary: true,
		// Foldable/Posture/Hinge 留给折叠屏宿主显式上报 (reportPosture),
		// 这里不猜: 猜错的代价是布局按错误姿态分栏。
	}}
}

// DisplayOf 报告窗口在哪块屏上 —— 移动端只有一个表面, 恒命中。
func (s *Surface) DisplayOf(surf gfx.Surface) (string, bool) {
	if surf != s {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id, true
}

// ===== capturer (可选能力) =====

// CapturePointer / ReleasePointer: Android 的触摸事件天然只送给被按住的 View,
// "拖出窗口"这件事在移动端不存在 ⇒ 恒真, 两个方法是空操作。
//
// 它必须实现: 不实现的话 hasPointerCapture() 为 false, 拖动会在手指离开
// 元素范围时被内核主动放弃 (代码里写着"没有捕获的后端永远等不到 MouseUp")。
func (s *Surface) CapturePointer() {}

// ReleasePointer 见 CapturePointer。
func (s *Surface) ReleasePointer() {}

// ===== WindowFactory =====

// Factory 把宿主已创建好的 Surface 交给内核。
//
// Android 上窗口的生命周期归 Activity, 引擎只能"接管"而不能"创建" —— 所以
// 这里不做任何创建动作, 只在 Create 时返回那个已绑定的表面。
type Factory struct {
	mu sync.Mutex
	s  *Surface
}

// NewFactory 绑定一个表面。
func NewFactory(s *Surface) *Factory { return &Factory{s: s} }

// Create 返回绑定的表面 (v1 单窗口: cfg 只用来记录标题, 不新建窗口)。
func (f *Factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.s == nil || f.s.isClosed() {
		return nil, errors.New("mobile: surface 未绑定或已关闭")
	}
	return f.s, nil
}

// Register 把自己注册成 gfx 的默认窗口后端 (宿主在启动脚本前调一次)。
// 与 win32 的 init() 等价, 只是移动端的时机必须由宿主决定 ("JNI attach 完成、
// Surface 已创建" 才成立)。
func (s *Surface) Register() { gfx.SetDefaultFactory(NewFactory(s)) }
