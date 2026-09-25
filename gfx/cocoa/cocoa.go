//go:build darwin

// Package cocoa 是 gfx 的 macOS 窗口后端 —— 基于 purego (纯 Go, 无 cgo) 的
// objc runtime 桥, 直接驱动 AppKit。
//
// ## 为什么这条路线现在走得通 (推翻 IDLE_TASK_REPORT_P4 的降级结论)
//
// 当时卡在两点: ① 无 cgo 时 darwin 缺 dlopen/objc_msgSend 入口;
// ② 仓库没有 macOS 环境, 交叉编译只能保证"编译通过"。两点都已解除:
// 引入 github.com/ebitengine/purego (纯 Go, 含 objc 子包) 提供 objc_msgSend
// 的 ABI 蹦床; 开发机换成真 macOS 后每一步都有运行时验证。
//
// ## 分层设计 (谁负责什么)
//
//	脚本 (JSX + 信号) → gfx 内核 (node/layout/raster, 零改动) → gfx.Surface
//	    → 本包: NSWindow + NSView(事件) + CALayer(上屏), 全部经 objc.Send
//
// 关键决策与理由:
//
//  1. 事件走 NSView 子类方法 (mouseDown:/keyDown:/scrollWheel: …), 用
//     objc.RegisterClass + objc.NewIMP 注册 —— AppKit 原生分发路径, 与
//     win32 的 WndProc 同一地位。避开所有结构体参数的 IMP (NSRect 这类
//     HFA 结构在回调侧的 ABI 打包是 purego 未覆盖的区域): 事件方法只收
//     NSEvent 指针, 坐标经 convertPoint:fromView: (Send 侧完整支持结构体
//     的传参与返回) 转换; drawRect 完全不重载。
//
//  2. 上屏走 CALayer.contents = NSImage (NSBitmapImageRep 提供像素):
//     rep 的 bitmapData 缓冲一次分配并被 Go 侧据为己有, ShowRegions 只
//     拷贝脏区字节, 然后重新挂 contents 由 Core Animation 合成。不碰
//     CoreGraphics 的 C API (免掉 CGImageCreate 11 参数桥接与字节序/预乘
//     的坑), "首行即顶行"的位图语义也天然匹配 gfx 的 image.RGBA。代价是
//     整帧上传 —— 软光栅化的窗口本就不大, 换实现简单是值得的。
//
//  3. 坐标系: view 重载 isFlipped 返回 YES, 于是 convertPoint 直接给出
//     "左上原点、y 向下" —— 与 gfx 内核同一坐标系, 只差 Retina 的 scale
//     倍换算 (点 → 设备像素, 与 Size() 同一口径)。
//
//  4. 事件循环: 不进 NSApp.run (那会永久占住线程), 而是在 WaitEvents 里
//     用 nextEventMatchingMask:untilDate:inMode:dequeue: 做有界泵 ——
//     maxWait 自然映射成 untilDate (绝对时刻, 重复取事件不会顺延),
//     与 x11/win32 的 WaitEvents 契约一致; 取到的事件统一 sendEvent: 走
//     正常响应链 (于是我们的 view 方法被调用)。每轮包一个
//     NSAutoreleasePool, 避免 stringWithUTF8String 之类临时对象堆积。
//
//  5. 关窗: windowWillClose: (delegate) 投递 EventClose 并置 closed 标记
//     —— 与 win32 的 WM_DESTROY → EventClose 对齐; 之后 WaitEvents 返回
//     false, gfx 层走 markSurfaceClosed 的常规关闭流程。
//
//  6. 脚本侧 w.close() 的语义与 win32 相同: gfx 只解除注册不再泵这个
//     窗口, 平台窗口本身不销毁 (Surface 无销毁回调可走)。
//
// v1 边界 (明确写下, 免得被当成 bug):
//   - IME 中文输入不可用 (需 NSTextInputClient 协议, 与 x11 的 TODO 同因);
//     英文/符号键入与全部功能键经 event.characters/keyCode 直通可用;
//   - 显示器枚举只报主屏 (gx/screen 的多屏语义等有真机多屏需求再补)。
package cocoa

import (
	"fmt"
	"image"
	"runtime"
	"structs"
	"sync"
	"time"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

func init() {
	// AppKit 硬性要求主线程: 共享 NSApplication 与全部窗口操作必须落在
	// 进程主线程上。init 在 main() 之前的主 goroutine 上执行, 此刻
	// LockOSThread 把主 goroutine 钉死在主 OS 线程 —— 之后哪怕发生文件
	// IO 等阻塞调用, 调度器也不会把它迁到别的线程 (Mount 里的
	// LockOSThread 只锁"当前线程", 拦不住之前的迁移)。与 purego 官方
	// 示例同一做法。
	runtime.LockOSThread()
	// objc 包只加载 libobjc (runtime 本身); AppKit 的类要显式引入后
	// objc_getClass 才找得到 —— 与 purego 官方示例同一做法。Cocoa 是
	// AppKit/Foundation 的伞框架, 一次加载全齐。
	if _, err := purego.Dlopen("/System/Library/Frameworks/Cocoa.framework/Cocoa", purego.RTLD_GLOBAL|purego.RTLD_LAZY); err != nil {
		panic("cocoa: dlopen Cocoa: " + err.Error())
	}
	gfx.SetDefaultFactory(&factory{})
}

// ===== objc 选择器缓存 =====
// RegisterName 会抢全局 objc 锁, 必须缓存 (objc 包文档要求)。

var (
	selSharedApplication  = objc.RegisterName("sharedApplication")
	selSetActivationPol   = objc.RegisterName("setActivationPolicy:")
	selFinishLaunching    = objc.RegisterName("finishLaunching")
	selActivateIgnoring   = objc.RegisterName("activateIgnoringOtherApps:")
	selNextEvent          = objc.RegisterName("nextEventMatchingMask:untilDate:inMode:dequeue:")
	selSendEvent          = objc.RegisterName("sendEvent:")
	selDistantFuture      = objc.RegisterName("distantFuture")
	selDateWithInterval   = objc.RegisterName("dateWithTimeIntervalSinceNow:")
	selTerminate          = objc.RegisterName("terminate:")
	selAlloc              = objc.RegisterName("alloc")
	selNew                = objc.RegisterName("new")
	selInit               = objc.RegisterName("init")
	selInitWithFrame      = objc.RegisterName("initWithFrame:")
	selInitWithContent    = objc.RegisterName("initWithContentRect:styleMask:backing:defer:")
	selInitWithSize       = objc.RegisterName("initWithSize:")
	selInitWithBitmapData = objc.RegisterName("initWithBitmapDataPlanes:pixelsWide:pixelsHigh:bitsPerSample:samplesPerPixel:hasAlpha:isPlanar:colorSpaceName:bytesPerRow:bitsPerPixel:")
	selInitWithRectOpt    = objc.RegisterName("initWithRect:options:owner:userInfo:")
	selSetTitle           = objc.RegisterName("setTitle:")
	selMakeKeyAndOrder    = objc.RegisterName("makeKeyAndOrderFront:")
	selCenter             = objc.RegisterName("center")
	selSetContentView     = objc.RegisterName("setContentView:")
	selSetDelegate        = objc.RegisterName("setDelegate:")
	selSetAcceptsMoved    = objc.RegisterName("setAcceptsMouseMovedEvents:")
	selSetInitialResp     = objc.RegisterName("setInitialFirstResponder:")
	selSetContentsSize    = objc.RegisterName("setContentSize:")
	selContentView        = objc.RegisterName("contentView")
	selBounds             = objc.RegisterName("bounds")
	selSetWantsLayer      = objc.RegisterName("setWantsLayer:")
	selLayer              = objc.RegisterName("layer")
	selSetContents        = objc.RegisterName("setContents:")
	selSetContentsScale   = objc.RegisterName("setContentsScale:")
	selAddRep             = objc.RegisterName("addRepresentation:")
	selBitmapData         = objc.RegisterName("bitmapData")
	selAddTrackingArea    = objc.RegisterName("addTrackingArea:")
	selConvertPoint       = objc.RegisterName("convertPoint:fromView:")
	selLocationInWindow   = objc.RegisterName("locationInWindow")
	selCharacters         = objc.RegisterName("characters")
	selUTF8String         = objc.RegisterName("UTF8String")
	selKeyCode            = objc.RegisterName("keyCode")
	selModifierFlags      = objc.RegisterName("modifierFlags")
	selDeltaY             = objc.RegisterName("deltaY")
	selScrollingDeltaY    = objc.RegisterName("scrollingDeltaY")
	selPreciseDeltas      = objc.RegisterName("hasPreciseScrollingDeltas")
	selIsFlipped          = objc.RegisterName("isFlipped")
	selAcceptsFirstResp   = objc.RegisterName("acceptsFirstResponder")
	selStringWithUTF8     = objc.RegisterName("stringWithUTF8String:")
	selMainScreen         = objc.RegisterName("mainScreen")
	selBackingScale       = objc.RegisterName("backingScaleFactor")
	selSetSubmenu         = objc.RegisterName("setSubmenu:")
	selAddItem            = objc.RegisterName("addItem:")
	selSetKeyEquivalent   = objc.RegisterName("setKeyEquivalent:")
	selSetAction          = objc.RegisterName("setAction:")
	selSetMainMenu        = objc.RegisterName("setMainMenu:")
	selGeneralPasteboard  = objc.RegisterName("generalPasteboard")
	selStringForType      = objc.RegisterName("stringForType:")
	selClearContents      = objc.RegisterName("clearContents")
	selSetStringForType   = objc.RegisterName("setString:forType:")
	selDrain              = objc.RegisterName("drain")
)

// ===== AppKit 常量 =====

const (
	nsTrackingEnteredExit   = 0x01
	nsTrackingActiveInKey   = 0x02
	nsTrackingInVisibleRect = 0x10
	nsStyleTitled           = 1 << 0
	nsStyleClosable         = 1 << 1
	nsStyleMiniaturizable   = 1 << 2
	nsStyleResizable        = 1 << 3
	nsBackingBuffered       = 2
	nsActivationRegular     = 0
	nsModifierShift         = 1 << 17
	nsModifierControl       = 1 << 18
	nsModifierAlternate     = 1 << 19
	nsRunDefaultMode        = "kCFRunLoopDefaultMode" // NSDefaultRunLoopMode 的字符串值
	clipUTType              = "public.utf8-plain-text"
)

// wheelDelta 与 Windows 的 WHEEL_DELTA 对齐 (一格 120), 让脚本不必区分平台。
// NSEvent.deltaY 滚轮一格 = ±1, 非精确滚动时乘上这个系数与 win32/x11 对齐。
const wheelDelta = 120

// keyCodeNames 特殊键码 → 键名 (与 win32/x11 同一套命名)。
// 可打印字符不走这里 (经 event.characters), 只覆盖功能键。
var keyCodeNames = map[uint64]string{
	36: "Enter", 48: "Tab", 51: "Backspace", 53: "Escape",
	115: "Home", 116: "PageUp", 117: "Delete", 119: "End", 121: "PageDown",
	123: "ArrowLeft", 124: "ArrowRight", 125: "ArrowDown", 126: "ArrowUp",
	122: "F1", 120: "F2", 99: "F3", 118: "F4", 96: "F5", 97: "F6",
	98: "F7", 100: "F8", 101: "F9", 109: "F10", 103: "F11", 111: "F12",
}

// ===== 几何类型 (与 AppKit ABI 对齐; structs.HostLayout 禁用编译器布局调整) =====

type nsPoint struct {
	_    structs.HostLayout
	X, Y float64
}

type nsSize struct {
	_             structs.HostLayout
	Width, Height float64
}

type nsRect struct {
	_      structs.HostLayout
	Origin nsPoint
	Size   nsSize
}

func nsMakeRect(x, y, w, h float64) nsRect {
	return nsRect{Origin: nsPoint{X: x, Y: y}, Size: nsSize{Width: w, Height: h}}
}

// ===== objc 类注册 (view 事件方法 + window delegate) =====

var (
	viewClass     objc.Class
	delegateClass objc.Class
)

// surfReg 是 objc 对象 → Go surface 的注册表 (view 与 delegate 两个身份都
// 指向同一个 surface)。为什么不用 ivar 存裸指针: 把 Go 指针塞进 ObjC 对象
// 再读回来要过 uintptr 中转, 是 vet 明示的 unsafe.Pointer 误用形态;
// sync.Map 注册表同样 O(1) 且类型安全。条目在 close 时移除。
var surfReg sync.Map // objc.ID → *surface

func regSurface(id objc.ID, s *surface) {
	if id != 0 && s != nil {
		surfReg.Store(id, s)
	}
}

func unregSurface(ids ...objc.ID) {
	for _, id := range ids {
		if id != 0 {
			surfReg.Delete(id)
		}
	}
}

func init() {
	var err error
	viewClass, err = objc.RegisterClass("GoxGfxView",
		objc.GetClass("NSView"), nil, nil,
		[]objc.MethodDef{
			{Cmd: selIsFlipped, Fn: impIsFlipped},
			{Cmd: selAcceptsFirstResp, Fn: impAcceptsFirstResp},
			{Cmd: objc.RegisterName("mouseDown:"), Fn: impMouseDown},
			{Cmd: objc.RegisterName("mouseUp:"), Fn: impMouseUp},
			{Cmd: objc.RegisterName("rightMouseUp:"), Fn: impRightMouseUp},
			{Cmd: objc.RegisterName("mouseMoved:"), Fn: impMouseMoved},
			{Cmd: objc.RegisterName("mouseDragged:"), Fn: impMouseMoved},
			{Cmd: objc.RegisterName("rightMouseDragged:"), Fn: impMouseMoved},
			{Cmd: objc.RegisterName("otherMouseDragged:"), Fn: impMouseMoved},
			{Cmd: objc.RegisterName("scrollWheel:"), Fn: impScrollWheel},
			{Cmd: objc.RegisterName("keyDown:"), Fn: impKeyDown},
			{Cmd: objc.RegisterName("keyUp:"), Fn: impKeyUp},
			{Cmd: objc.RegisterName("mouseExited:"), Fn: impMouseExited},
		})
	if err != nil {
		panic("cocoa: register view class: " + err.Error())
	}
	delegateClass, err = objc.RegisterClass("GoxGfxWindowDelegate",
		objc.GetClass("NSObject"), nil, nil,
		[]objc.MethodDef{
			{Cmd: objc.RegisterName("windowWillClose:"), Fn: impWindowWillClose},
			{Cmd: objc.RegisterName("windowDidResize:"), Fn: impWindowDidResize},
			{Cmd: objc.RegisterName("windowDidResignKey:"), Fn: impWindowResignKey},
		})
	if err != nil {
		panic("cocoa: register delegate class: " + err.Error())
	}
}

// surfaceOf 从注册表取回 self 对应的 Go surface (IMP 的 receiver 查找)。
func surfaceOf(self objc.ID) *surface {
	if v, ok := surfReg.Load(self); ok {
		return v.(*surface)
	}
	return nil
}

// ===== IMP: view 事件方法 =====
// 签名纪律: 只收 objc.ID (指针) 参数, 不收结构体 —— 见文件头决策 1。

func impIsFlipped(self objc.ID, cmd objc.SEL) uintptr { return 1 }

func impAcceptsFirstResp(self objc.ID, cmd objc.SEL) uintptr { return 1 }

// pointInView 把 NSEvent 的 locationInWindow 转成 view 坐标 (左上原点,
// 点单位)。isFlipped=YES 的 view 上 convertPoint 直接给出 y 向下的坐标。
func pointInView(self objc.ID, ev objc.ID) (float64, float64) {
	loc := objc.Send[nsPoint](ev, selLocationInWindow)
	p := objc.Send[nsPoint](self, selConvertPoint, loc, objc.ID(0))
	return p.X, p.Y
}

func impMouseDown(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		x, y := pointInView(self, ev)
		s.postDevice(gfx.Event{Kind: gfx.EventMouseDown, X: int(x), Y: int(y)})
	}
	return 0
}

func impMouseUp(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		x, y := pointInView(self, ev)
		s.postDevice(gfx.Event{Kind: gfx.EventMouseUp, X: int(x), Y: int(y)})
	}
	return 0
}

func impRightMouseUp(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		x, y := pointInView(self, ev)
		s.postDevice(gfx.Event{Kind: gfx.EventMouseRightUp, X: int(x), Y: int(y)})
	}
	return 0
}

func impMouseMoved(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		x, y := pointInView(self, ev)
		s.postDevice(gfx.Event{Kind: gfx.EventMouseMove, X: int(x), Y: int(y)})
	}
	return 0
}

func impMouseExited(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		s.trySend(gfx.Event{Kind: gfx.EventMouseLeave})
	}
	return 0
}

// impScrollWheel 滚轮: gfx 约定 DeltaY 向上为正, 与 NSEvent.deltaY 同号;
// 非精确滚动 (真滚轮) 按 win32 的 120/格对齐, 精确滚动 (触控板) 按点数直送。
func impScrollWheel(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		x, y := pointInView(self, ev)
		var delta float64
		if objc.Send[bool](ev, selPreciseDeltas) {
			delta = objc.Send[float64](ev, selScrollingDeltaY)
		} else {
			delta = objc.Send[float64](ev, selDeltaY) * wheelDelta
		}
		if delta != 0 {
			s.postDevice(gfx.Event{Kind: gfx.EventMouseWheel, X: int(x), Y: int(y), DeltaY: int(delta)})
		}
	}
	return 0
}

// impKeyDown/impKeyUp 键盘: 功能键按 keyCode 查表, 可打印字符取
// event.characters。Ctrl 组合产生的控制字符 (<0x20) 还原成对应字母,
// 修饰键状态随事件附带 —— 与 win32 后端同一约定。
func impKeyDown(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		s.sendKeyEvent(ev, gfx.EventKeyDown)
	}
	return 0
}

func impKeyUp(self objc.ID, cmd objc.SEL, ev objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		s.sendKeyEvent(ev, gfx.EventKeyUp)
	}
	return 0
}

// ===== IMP: window delegate =====

func impWindowWillClose(self objc.ID, cmd objc.SEL, note objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		s.onWindowWillClose()
	}
	return 0
}

func impWindowDidResize(self objc.ID, cmd objc.SEL, note objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		s.onWindowDidResize()
	}
	return 0
}

func impWindowResignKey(self objc.ID, cmd objc.SEL, note objc.ID) uintptr {
	if s := surfaceOf(self); s != nil {
		// 窗口失活: 清悬停态 (与 win32 失焦 → MouseLeave 同思路)
		s.trySend(gfx.Event{Kind: gfx.EventMouseLeave})
	}
	return 0
}

// ===== surface =====

type factory struct{}

func (f *factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	return newSurface(cfg)
}

// Displays 实现 factory 级 displayProvider: 还没有任何窗口时给 gx/screen
// 兜底的主屏信息 (有窗口后走 surface 的同名方法)。
func (f *factory) Displays() []gfx.Display {
	scr := objc.ID(objc.GetClass("NSScreen")).Send(selMainScreen)
	scale, w, h := 1.0, 1280.0, 800.0
	if scr != 0 {
		if s := objc.Send[float64](scr, selBackingScale); s > 0 {
			scale = s
		}
		frame := objc.Send[nsRect](scr, selBounds)
		w, h = frame.Size.Width*scale, frame.Size.Height*scale
	}
	return []gfx.Display{{
		ID: "main", Name: "Main Display",
		W: int(w), H: int(h),
		WorkW: int(w), WorkH: int(h),
		Scale: scale, Primary: true,
	}}
}

// DisplayOf 报告窗口在哪块屏上 —— v1 只报主屏, 恒命中。
func (f *factory) DisplayOf(surf gfx.Surface) (string, bool) {
	if _, ok := surf.(*surface); ok {
		return "main", true
	}
	return "", false
}

type surface struct {
	win      objc.ID
	view     objc.ID
	layer    objc.ID
	delegate objc.ID

	rep   objc.ID // NSBitmapImageRep (像素缓冲的宿主)
	nsimg objc.ID // NSImage (挂到 layer.contents 的载体)

	// buf 是持久后台缓冲 (w*h*4, RGBA): ShowRegions 只拷脏区进来,
	// 上屏仍整帧 —— 见文件头决策 2。实际宿主内存是 rep 的 bitmapData。
	buf []byte

	events chan gfx.Event

	mu     sync.Mutex
	closed bool
	w, h   int // 客户区尺寸, 设备像素 (与 Size()/EventResize 同一口径)
	scale  float64
}

func newSurface(cfg gfx.WindowConfig) (gfx.Surface, error) {
	w, h := cfg.Width, cfg.Height
	if w <= 0 {
		w = 400
	}
	if h <= 0 {
		h = 300
	}

	app := ensureNSApp()

	// Retina: gfx 的 Width/Height 是设备像素, 窗口内容尺寸用点
	// (px/scale), layer.contentsScale 补回像素比 —— 图像与屏幕 1:1。
	scale := 1.0
	if scr := objc.ID(objc.GetClass("NSScreen")).Send(selMainScreen); scr != 0 {
		if s := objc.Send[float64](scr, selBackingScale); s > 0 {
			scale = s
		}
	}
	ptW, ptH := float64(w)/scale, float64(h)/scale

	win := objc.ID(objc.GetClass("NSWindow")).Send(selAlloc)
	win = win.Send(selInitWithContent,
		nsMakeRect(0, 0, ptW, ptH),
		uintptr(nsStyleTitled|nsStyleClosable|nsStyleMiniaturizable|nsStyleResizable),
		uintptr(nsBackingBuffered), false)
	if win == 0 {
		return nil, fmt.Errorf("cocoa: create NSWindow failed")
	}
	win.Send(selSetTitle, nsString(cfg.Title))
	win.Send(selSetAcceptsMoved, true)

	// 内容 view: 事件响应者 + 上屏载体
	view := objc.ID(viewClass).Send(selAlloc)
	view = view.Send(selInitWithFrame, nsMakeRect(0, 0, ptW, ptH))
	view.Send(selSetWantsLayer, true)

	s := &surface{
		win:    win,
		view:   view,
		events: make(chan gfx.Event, 256),
		w:      w, h: h,
		scale: scale,
	}
	// 注册表: view 与 delegate 两个 ObjC 身份都映射到 s (IMP 回调经
	// surfaceOf 查回); close 时解除。
	regSurface(view, s)

	// layer-backed 上屏 (见文件头决策 2)
	if layer := view.Send(selLayer); layer != 0 {
		layer.Send(selSetContentsScale, scale)
		s.layer = layer
	}
	// 鼠标进/出跟踪 (EventMouseLeave 的来源): InVisibleRect 让 AppKit 自动
	// 跟随可见区, 无需在 resize 时重建 tracking area。
	area := objc.ID(objc.GetClass("NSTrackingArea")).Send(selAlloc)
	area = area.Send(selInitWithRectOpt, nsRect{}, uintptr(nsTrackingEnteredExit|nsTrackingActiveInKey|nsTrackingInVisibleRect), view, objc.ID(0))
	view.Send(selAddTrackingArea, area)

	win.Send(selSetContentView, view)
	win.Send(selSetInitialResp, view)

	delegate := objc.ID(delegateClass).Send(selNew)
	s.delegate = delegate
	regSurface(delegate, s)
	win.Send(selSetDelegate, delegate)

	s.allocBackbuffer(w, h)

	win.Send(selMakeKeyAndOrder, objc.ID(0))
	win.Send(selCenter)
	app.Send(selActivateIgnoring, true)
	return s, nil
}

// ensureNSApp 惰性初始化共享 NSApplication (必须在 GUI 线程 = Mount 锁定
// 的主 goroutine 上调用)。顺带装一个最小菜单 (含 Cmd+Q 退出), 没有菜单的
// AppKit 应用连窗口焦点行为都不完整。
func ensureNSApp() objc.ID {
	app := objc.ID(objc.GetClass("NSApplication")).Send(selSharedApplication)
	if app == 0 {
		panic("cocoa: NSApplication sharedApplication failed")
	}
	if app.Send(selFinishLaunching) != 0 {
		// finishLaunching 无返回值, Send 恒返回 id; 这里只为统一写法
	}
	app.Send(selSetActivationPol, uintptr(nsActivationRegular))
	// 菜单: [Gox] → 退出 (Cmd+Q)。terminate: 在无 delegate 时直接结束进程,
	// Pump 循环随进程一起结束 —— v1 可接受。
	mainMenu := objc.ID(objc.GetClass("NSMenu")).Send(selAlloc)
	mainMenu = mainMenu.Send(selInit)
	appMenu := objc.ID(objc.GetClass("NSMenu")).Send(selAlloc)
	appMenu = appMenu.Send(selInit)
	appItem := objc.ID(objc.GetClass("NSMenuItem")).Send(selAlloc)
	appItem = appItem.Send(selInit)
	appItem.Send(selSetSubmenu, appMenu)
	mainMenu.Send(selAddItem, appItem)
	quitItem := objc.ID(objc.GetClass("NSMenuItem")).Send(selAlloc)
	quitItem = quitItem.Send(selInit)
	quitItem.Send(objc.RegisterName("setTitle:"), nsString("退出 Gox"))
	quitItem.Send(selSetKeyEquivalent, nsString("q"))
	quitItem.Send(selSetAction, selTerminate)
	appMenu.Send(selAddItem, quitItem)
	app.Send(selSetMainMenu, mainMenu)
	return app
}

// ===== gfx.Surface =====

// Size 返回客户区尺寸 (设备像素)。
func (s *surface) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Events 返回事件流。
func (s *surface) Events() <-chan gfx.Event { return s.events }

// WaitEvents 有界泵 AppKit 事件 (见文件头决策 4)。
// 返回 false 表示事件源已关闭 (窗口销毁)。
func (s *surface) WaitEvents(maxWait time.Duration) bool {
	if s.isClosed() {
		return false
	}
	pool := objc.ID(objc.GetClass("NSAutoreleasePool")).Send(selNew)
	defer pool.Send(selDrain)

	// untilDate: 有限等待映射成绝对时刻的 NSDate; 无限等待用 distantFuture。
	// nextEventMatchingMask 在等待期间跑 run loop —— Core Animation 的合成
	// 事务正是在这里提交, layer.contents 的变更由此真正上屏。untilDate 是
	// 绝对时刻, 循环里重复取事件不会把截止时间往后推。
	until := objc.ID(objc.GetClass("NSDate")).Send(selDistantFuture)
	if maxWait > 0 {
		until = objc.Send[objc.ID](objc.ID(objc.GetClass("NSDate")), selDateWithInterval, maxWait.Seconds())
	}
	app := objc.ID(objc.GetClass("NSApplication")).Send(selSharedApplication)
	for {
		if s.isClosed() {
			return false
		}
		ev := app.Send(selNextEvent, ^uintptr(0), until, nsString(nsRunDefaultMode), true)
		if ev == 0 {
			return true // 超时/无事件: 交回 Pump (定时器可能到期)
		}
		app.Send(selSendEvent, ev) // 正常响应链 → 我们的 view 方法被调用
	}
}

// Show 整帧上屏。
func (s *surface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

// ShowRegions 拷贝脏区进后台缓冲并整帧挂上 layer (rects 空 = 全帧)。
func (s *surface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	if img == nil {
		return
	}
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	bounds := img.Bounds()
	if bounds.Dx() != s.w || bounds.Dy() != s.h {
		s.allocBackbuffer(bounds.Dx(), bounds.Dy())
	}
	if len(rects) == 0 {
		rects = []image.Rectangle{bounds}
	}
	for _, r := range rects {
		r = r.Intersect(bounds)
		if r.Empty() {
			continue
		}
		s.copyRegion(img, r)
	}
	s.present()
}

// copyRegion 把 img 的 r 区域拷进持久缓冲 (行内连续拷贝, RGBA↔RGBA)。
// 调用方保证 r 已与 img.Bounds() 求交且非空。
func (s *surface) copyRegion(img *image.RGBA, r image.Rectangle) {
	dstStride := s.w * 4
	for y := 0; y < r.Dy(); y++ {
		src := img.Pix[(r.Min.Y+y-img.Rect.Min.Y)*img.Stride+(r.Min.X-img.Rect.Min.X)*4:]
		dst := s.buf[(r.Min.Y+y)*dstStride+r.Min.X*4:]
		copy(dst[:r.Dx()*4], src[:r.Dx()*4])
	}
}

// present 把缓冲交给 Core Animation。rep 的 bitmapData 就是 s.buf 的宿主
// 内存 —— copyRegion 写的就是它, 这里只需重新挂一次 contents 触发合成。
func (s *surface) present() {
	if s.layer == 0 || s.nsimg == 0 {
		return
	}
	s.layer.Send(selSetContents, s.nsimg)
}

// allocBackbuffer (重)分配 rep/image/缓冲。尺寸变化 (首帧/resize) 时调用,
// 需在 GUI 线程调用。
func (s *surface) allocBackbuffer(w, h int) {
	s.mu.Lock()
	s.w, s.h = w, h
	s.mu.Unlock()

	rep := objc.ID(objc.GetClass("NSBitmapImageRep")).Send(selAlloc)
	rep = rep.Send(selInitWithBitmapData,
		unsafe.Pointer(nil), // planes=NULL: rep 自己分配, 经 bitmapData 拿回
		uintptr(w), uintptr(h),
		uintptr(8), uintptr(4), // 8 bit/样本, 4 样本 (RGBA)
		true, false, // hasAlpha, 非平面
		nsString("NSCalibratedRGBColorSpace"),
		uintptr(w*4), uintptr(32))
	if rep == 0 {
		return
	}
	// 把 rep 的像素缓冲据为己有: 后续 ShowRegions 直接写这块内存。
	p := objc.Send[unsafe.Pointer](rep, selBitmapData)
	if p == nil {
		return
	}
	nsimg := objc.ID(objc.GetClass("NSImage")).Send(selAlloc)
	// NSImage 尺寸用点 (px/scale), rep 是完整像素 —— 合成时按 contentsScale
	// 1:1 映射到 Retina 屏。
	nsimg = nsimg.Send(selInitWithSize, nsSize{Width: float64(w) / s.scale, Height: float64(h) / s.scale})
	nsimg.Send(selAddRep, rep)

	s.mu.Lock()
	s.rep = rep
	s.buf = unsafe.Slice((*byte)(p), w*h*4)
	s.nsimg = nsimg
	s.mu.Unlock()
	if s.layer != 0 {
		s.layer.Send(selSetContentsScale, s.scale)
	}
}

// ===== 可选能力: windowController =====

// SetTitle 实现 windowController: 改窗口标题。
func (s *surface) SetTitle(title string) {
	s.win.Send(selSetTitle, nsString(title))
}

// ResizeClient 实现 windowController: 改窗口内容尺寸 (点)。
// windowDidResize: 随之到来 → EventResize。
func (s *surface) ResizeClient(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	s.win.Send(selSetContentsSize, nsSize{Width: float64(w) / s.scale, Height: float64(h) / s.scale})
}

// ===== 可选能力: displayProvider =====

// Displays 报告本机唯一的一块屏 (设备像素口径)。
func (s *surface) Displays() []gfx.Display {
	s.mu.Lock()
	scale := s.scale
	s.mu.Unlock()
	w, h := s.Size()
	dw, dh := int(float64(w)*scale), int(float64(h)*scale)
	return []gfx.Display{{
		ID: "main", Name: "Main Display",
		W: dw, H: dh, WorkW: dw, WorkH: dh,
		Scale: scale, Primary: true,
	}}
}

// DisplayOf 报告窗口在哪块屏上 —— v1 只有一块表面, 恒命中。
func (s *surface) DisplayOf(surf gfx.Surface) (string, bool) {
	if surf == gfx.Surface(s) {
		return "main", true
	}
	return "", false
}

// ===== 可选能力: clipboardHost =====

// ReadClipboardText 读系统剪贴板文本 (非文本/无内容返回空串, 不报错)。
func (s *surface) ReadClipboardText() (string, error) {
	pb := objc.ID(objc.GetClass("NSPasteboard")).Send(selGeneralPasteboard)
	str := pb.Send(selStringForType, nsString(clipUTType))
	if str == 0 {
		return "", nil
	}
	return nsToGo(str), nil
}

// WriteClipboardText 写文本到系统剪贴板。
func (s *surface) WriteClipboardText(text string) error {
	pb := objc.ID(objc.GetClass("NSPasteboard")).Send(selGeneralPasteboard)
	pb.Send(selClearContents)
	if pb.Send(selSetStringForType, nsString(text), nsString(clipUTType)) == 0 {
		return fmt.Errorf("cocoa: write clipboard failed")
	}
	return nil
}

// ===== 内部: 事件投递与翻译 =====

// trySend 非阻塞投递 (通道满则丢弃) —— 与 x11/win32 同契约。
func (s *surface) trySend(ev gfx.Event) {
	if s.isClosed() {
		return
	}
	select {
	case s.events <- ev:
	default:
	}
}

// postDevice 把"点坐标"事件乘上 scale 换成设备像素后投递 (gfx 内核的
// 命中测试坐标与 Size() 同一口径, 都是设备像素)。
func (s *surface) postDevice(ev gfx.Event) {
	s.mu.Lock()
	scale := s.scale
	s.mu.Unlock()
	ev.X = int(float64(ev.X) * scale)
	ev.Y = int(float64(ev.Y) * scale)
	s.trySend(ev)
}

// sendKeyEvent 翻译 NSEvent → gfx.Event (KeyDown/KeyUp)。
func (s *surface) sendKeyEvent(ev objc.ID, kind gfx.EventKind) {
	keyCode := objc.Send[uint64](ev, selKeyCode)
	flags := objc.Send[uint64](ev, selModifierFlags)
	key := ""
	if name, ok := keyCodeNames[keyCode]; ok {
		key = name
	} else {
		key = keyFromCharacters(ev)
	}
	s.trySend(gfx.Event{
		Kind:  kind,
		Key:   key,
		Ctrl:  flags&nsModifierControl != 0,
		Shift: flags&nsModifierShift != 0,
		Alt:   flags&nsModifierAlternate != 0,
	})
}

// keyFromCharacters 取 event.characters 的首个 Unicode 字符;
// Ctrl 组合产生的控制字符 (<0x20) 还原成对应字母。
func keyFromCharacters(ev objc.ID) string {
	str := ev.Send(selCharacters)
	if str == 0 {
		return ""
	}
	ch := nsToGo(str)
	if ch == "" {
		return ""
	}
	r := []rune(ch)[0]
	if r < 0x20 {
		// Ctrl+A → \x01: 还原字母形态, Ctrl 状态由事件标志位携带
		if r >= 1 && r <= 26 {
			return string(rune(r-1) + 'a')
		}
		return ""
	}
	return string(r)
}

// onWindowWillClose 用户点了红色关闭钮: 投 EventClose 并置关闭标记 ——
// gfx 层在 processEvents 里走常规关闭流程, 下一次 WaitEvents 返回 false。
func (s *surface) onWindowWillClose() {
	s.mu.Lock()
	wasClosed := s.closed
	s.closed = true
	s.mu.Unlock()
	unregSurface(s.view, s.delegate)
	if !wasClosed {
		s.trySend(gfx.Event{Kind: gfx.EventClose})
	}
}

// onWindowDidResize 窗口尺寸变化: 读新内容尺寸换算回设备像素,
// 真变了才投 EventResize (去重, 与 x11 的 ConfigureNotify 同思路)。
func (s *surface) onWindowDidResize() {
	if s.isClosed() {
		return
	}
	content := s.win.Send(selContentView)
	b := objc.Send[nsRect](content, selBounds)
	w := int(b.Size.Width*s.scale + 0.5)
	h := int(b.Size.Height*s.scale + 0.5)
	s.mu.Lock()
	changed := w > 0 && h > 0 && (w != s.w || h != s.h)
	s.mu.Unlock()
	if changed {
		s.allocBackbuffer(w, h) // 缓冲随之扩容, 下一次 Show 全帧重绘
		s.trySend(gfx.Event{Kind: gfx.EventResize, W: w, H: h})
	}
}

func (s *surface) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// ===== 工具 =====

// nsString Go 字符串 → NSString (stringWithUTF8String: 需要 NUL 结尾的
// C 字符串, Go 字符串内部没有 NUL, 这里补一个临时拷贝)。
func nsString(s string) objc.ID {
	b := append([]byte(s), 0)
	return objc.ID(objc.GetClass("NSString")).Send(selStringWithUTF8, unsafe.Pointer(&b[0]))
}

// nsToGo NSString → Go 字符串 (经 UTF8String 读 C 字符串)。
// 注意: UTF8String 的返回值由 autorelease 机制管理, 调用方应在同一个
// NSAutoreleasePool 的生命周期内消费 (WaitEvents 每轮建池)。
func nsToGo(id objc.ID) string {
	p := objc.Send[unsafe.Pointer](id, selUTF8String)
	if p == nil {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(p, n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(p), n))
}

// 编译期断言: *surface 满足 gfx.Surface 与全部已实现的可选能力。
var (
	_ gfx.Surface = (*surface)(nil)
	_ interface {
		SetTitle(string)
		ResizeClient(int, int)
	} = (*surface)(nil)
	_ interface {
		Displays() []gfx.Display
		DisplayOf(gfx.Surface) (string, bool)
	} = (*surface)(nil)
	_ interface {
		ReadClipboardText() (string, error)
		WriteClipboardText(string) error
	} = (*surface)(nil)
)
