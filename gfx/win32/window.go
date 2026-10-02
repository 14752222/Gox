//go:build windows

package win32

import (
	"unsafe"

	"github.com/14752222/Gox/gfx"
)

// 窗口管理 (§四 窗口/系统缺口): 位置 / 层级 / 尺寸约束 / 全屏 / 缩放开关 /
// 激活 / 光标形状 —— gfx 侧 windowManager + cursorHost + boundsProvider
// 三个可选能力的 win32 实现。
//
// 为什么单独一个文件而不是堆在 win32.go 里: win32.go 已经 1150 行, 那些是
// "建窗 + 消息 + DIB + 剪贴板 + 对话框"这些**正交**的东西。窗口管理自己就是
// 一组, 它的状态 (原样式 / 原矩形 / 约束 / 光标) 也自成一类。
//
// 两条约束一直成立:
//   - **零 cgo**: 全部走 syscall.NewLazyDLL。
//   - **不执行 JS**: 这些方法都由 JS 经 gfx 调进来, 调用线程就是 GUI 线程
//     (脚本/事件泵/VM 同线程), 所以直接调 user32 是安全的; WndProc 那边
//     仍然只投递事件 (WM_MOVE / WM_SETCURSOR 的新分支也一样)。

var (
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	// 32 位进程里 SetWindowLongPtrW 并不导出 (它只是头文件里的宏)。
	// 单独持有 32 位回退项, 调用前 Find 一次。
	procSetWindowLongW = user32.NewProc("SetWindowLongW")
	procGetWindowLongW = user32.NewProc("GetWindowLongW")
	procSetForegroundW = user32.NewProc("SetForegroundWindow")
	procBringWindowTop = user32.NewProc("BringWindowToTop")
	procLoadCursorW    = user32.NewProc("LoadCursorW")
	procSetCursorW     = user32.NewProc("SetCursor")
	procIsIconic       = user32.NewProc("IsIconic")
)

// 窗口样式位 (GWL_STYLE 的读写用)。
const (
	gwlStyle = ^uintptr(15) // -16

	wsThickFrame  = 0x00040000 // 可拖边框 (缩放)
	wsMaximizeBox = 0x00010000
	wsPopup       = 0x80000000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsMinimizeBox = 0x00020000
)

// SetWindowPos 的 flags / 特殊 z 序句柄。
const (
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoZOrder    = 0x0004
	swpNoActivate  = 0x0010
	swpFrameChange = 0x0020
	swpShowWindow  = 0x0040

	hwndTopmost    = ^uintptr(0) // (HWND)-1
	hwndNotTopmost = ^uintptr(1) // (HWND)-2
	hwndBottom     = 1
)

// 窗口管理相关的消息。
const (
	wmMove          = 0x0003
	wmGetMinMaxInfo = 0x0024
	wmSetCursor     = 0x0020
)

// minMaxInfo 对应 Win32 MINMAXINFO (5 个 POINT)。
type minMaxInfo struct {
	PtReserved     point32
	PtMaxSize      point32
	PtMaxPosition  point32
	PtMinTrackSize point32
	PtMaxTrackSize point32
}

// windowConstraints 是一组尺寸约束 (0 = 不约束)。
type windowConstraints struct {
	minW, minH, maxW, maxH int
}

// cursorHidden 是"隐藏光标"的哨兵值 (SetCursor(NULL) 的语义)。
const cursorHidden = ^uintptr(0)

// 缺省光标 (IDC_ARROW) 的 MAKEINTRESOURCE 取值。
const idcArrow = 32512

// defaultCursorID 是"没有设置过任何形状"时的句柄 (按需加载一次)。
var defaultCursorID uintptr

// cursorIDs 是 gfx 侧形状名 → 系统光标 ID。
//
// 用系统内置光标而不是自绘: 它们自带各版本 Windows 的原生气质 (大小 / DPI /
// 主题都跟随系统), 自绘一份只会显得"不像这个系统的"。
var cursorIDs = map[string]uintptr{
	"default":     idcArrow,
	"text":        32513, // IDC_IBEAM
	"wait":        32514, // IDC_WAIT
	"crosshair":   32515, // IDC_CROSS
	"move":        32646, // IDC_SIZEALL
	"grab":        32649, // IDC_HAND (没有"张开的手", 用手型代替)
	"grabbing":    32649,
	"pointer":     32649, // IDC_HAND
	"progress":    32650, // IDC_APPSTARTING (箭头 + 小沙漏)
	"help":        32651, // IDC_HELP
	"not-allowed": 32648, // IDC_NO
	"ew-resize":   32644, // IDC_SIZEWE
	"col-resize":  32644,
	"ns-resize":   32645, // IDC_SIZENS
	"row-resize":  32645,
	"nwse-resize": 32642, // IDC_SIZENWSE
	"nesw-resize": 32643, // IDC_SIZENESW
	// "none" 不在表里: 它是"隐藏光标" (SetCursor(0)), 见 SetCursor。
}

// ===== boundsProvider =====

// Bounds 读窗口外框的屏幕坐标与尺寸 (gfx 的 boundsProvider)。
func (s *surface) Bounds() (int, int, int, int) {
	if s == nil || s.hwnd == 0 {
		return 0, 0, 0, 0
	}
	var rc rect32
	if r, _, _ := procGetWindowRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&rc))); r == 0 {
		return 0, 0, 0, 0
	}
	return int(rc.Left), int(rc.Top), int(rc.Right - rc.Left), int(rc.Bottom - rc.Top)
}

// ===== windowManager =====
//
// 合流备注 (2026-10-02): 这里原本还有一个 MoveTo(x, y) (无返回值版)。
// 与 win32.go 里实现 windowMover 的 MoveTo(x, y) error 同名同签名冲突,
// 保留后者 —— 位置上移失败要能报错, 且 gfx 层的 windowMover 就是这么用的。

// SetLevel 设 z 序层级。
//
// "top" → HWND_TOPMOST (永远在最前, 连别的应用也盖不住 —— 这是"悬浮球 /
// 监控浮窗"要的语义); "bottom" → HWND_BOTTOM (桌面挂件); "normal" →
// HWND_NOTOPMOST (取消置顶, 回到普通层)。
func (s *surface) SetLevel(level string) {
	if s == nil || s.hwnd == 0 {
		return
	}
	insertAfter := hwndNotTopmost
	switch level {
	case "top":
		insertAfter = hwndTopmost
	case "bottom":
		insertAfter = hwndBottom
	}
	procSetWindowPos.Call(uintptr(s.hwnd), insertAfter, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoActivate)
}

// SetSizeConstraints 设用户拖边框时的尺寸钳位。真正生效点在 WM_GETMINMAXINFO
// (见 win32.go 的消息分支) —— Windows 只在用户开始拖拽时问一次"允许的范围",
// 所以存起来等它问, 而不是立即 SetWindowPos。
func (s *surface) SetSizeConstraints(minW, minH, maxW, maxH int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.constraints = &windowConstraints{minW, minH, maxW, maxH}
	s.mu.Unlock()
}

// SetResizable 开关 WS_THICKFRAME (能否拖边框缩放) 与 WS_MAXIMIZEBOX
// (最大化按钮跟着一起变灰 —— 只关缩放却留着最大化按钮是很怪的组合)。
func (s *surface) SetResizable(on bool) {
	if s == nil || s.hwnd == 0 {
		return
	}
	style := getWindowStyle(uintptr(s.hwnd))
	if on {
		style |= wsThickFrame | wsMaximizeBox
	} else {
		style &^= wsThickFrame | wsMaximizeBox
	}
	setWindowStyle(uintptr(s.hwnd), style)
	// SWP_FRAMECHANGED: 不重算非客户区的话新样式不生效 (拖边框的手感还是
	// 老样子, 要等下次重建窗口才变)。
	procSetWindowPos.Call(uintptr(s.hwnd), 0, 0, 0, 0, 0,
		swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpFrameChange)
}

// SetFullscreen 进出全屏。
//
// 实现是"去边框 + 铺满当前显示器": 记录原样式与原外框, 退出时原样还原。
// 不用 ShowWindow(SW_MAXIMIZE) —— 最大化仍留着标题栏与任务栏, 不是全屏。
func (s *surface) SetFullscreen(on bool) {
	if s == nil || s.hwnd == 0 {
		return
	}
	hwnd := uintptr(s.hwnd)
	s.mu.Lock()
	was := s.fullscreen
	if was == on {
		s.mu.Unlock()
		return
	}
	if on {
		// 原样式/原矩形只记第一次: 连续两次 setFullscreen(true) 不能把
		// "已经是全屏的样式"记成"要还原的样子"。
		var rc rect32
		procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))
		s.restoreStyle = getWindowStyle(hwnd)
		s.restoreRect = rc
		s.fullscreen = true
	} else {
		s.fullscreen = false
	}
	restoreStyle, restoreRect := s.restoreStyle, s.restoreRect
	s.mu.Unlock()

	if on {
		style := getWindowStyle(hwnd)
		style &^= wsCaption | wsSysMenu | wsMinimizeBox | wsMaximizeBox | wsThickFrame
		style |= wsPopup
		setWindowStyle(hwnd, style)

		hmon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
		var mon rect32
		if hmon != 0 {
			mi := monitorInfoExW{CbSize: uint32(unsafe.Sizeof(monitorInfoExW{}))}
			if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r != 0 {
				mon = mi.RcMonitor
			}
		}
		if mon.Right <= mon.Left || mon.Bottom <= mon.Top {
			return // 取不到显示器几何: 保留原样, 总比跳到 (0,0,0,0) 好
		}
		procSetWindowPos.Call(hwnd, hwndTopmost,
			uintptr(int32(mon.Left)), uintptr(int32(mon.Top)),
			uintptr(int32(mon.Right-mon.Left)), uintptr(int32(mon.Bottom-mon.Top)),
			swpFrameChange|swpShowWindow)
		return
	}
	setWindowStyle(hwnd, restoreStyle)
	procSetWindowPos.Call(hwnd, hwndNotTopmost,
		uintptr(int32(restoreRect.Left)), uintptr(int32(restoreRect.Top)),
		uintptr(int32(restoreRect.Right-restoreRect.Left)),
		uintptr(int32(restoreRect.Bottom-restoreRect.Top)),
		swpFrameChange|swpShowWindow)
}

// Activate 把窗口带到前台 (模态被挡时点父窗口会调它)。
//
// 两个 API 都调: SetForegroundWindow 负责"键盘焦点也过来", BringWindowToTop
// 负责 z 序 —— 单用前者在某些前台锁定策略下会被拒, 单用后者则窗口跑到最前
// 但输入焦点还在别的应用上。
func (s *surface) Activate() {
	if s == nil || s.hwnd == 0 {
		return
	}
	procBringWindowTop.Call(uintptr(s.hwnd))
	procSetForegroundW.Call(uintptr(s.hwnd))
}

// ===== cursorHost =====

// SetCursor 设鼠标光标形状 (gfx 的 cursorHost)。
//
// 只存句柄并把 SetCursor 立即调一次 —— Windows 在**每次鼠标移动的命中测试**
// 时都会重新询问光标 (WM_SETCURSOR), 所以真正保证"它一直是我要的形状"的是
// win32.go 里那条 WM_SETCURSOR 分支; 这里立即调只是让"设置之后鼠标不动"的
// 那一瞬也立刻变 (否则要等用户动一下鼠标才看到效果)。
func (s *surface) SetCursor(shape string) {
	if s == nil {
		return
	}
	target := cursorHidden
	if id, ok := cursorIDs[shape]; ok {
		if h := loadCursor(id); h != 0 {
			target = h
		}
	} else if shape != "none" {
		// 未知形状: 退到箭头 (gfx 侧已归一, 走到这里说明有新形状还没映射)
		if h := loadCursor(idcArrow); h != 0 {
			target = h
		}
	}
	s.mu.Lock()
	s.cursor = target
	s.mu.Unlock()
	if target == cursorHidden {
		procSetCursorW.Call(0)
		return
	}
	procSetCursorW.Call(target)
}

// applyCursor 在 WM_SETCURSOR 里重新申明光标 (见 SetCursor 的说明)。
// 返回是否已自行处理 (true 表示不再走 DefWindowProc)。
func (s *surface) applyCursor() bool {
	s.mu.Lock()
	cur := s.cursor
	s.mu.Unlock()
	if cur == 0 {
		return false // 从没设过: 交给系统缺省
	}
	if cur == cursorHidden {
		procSetCursorW.Call(0)
		return true
	}
	procSetCursorW.Call(cur)
	return true
}

// loadCursor 加载系统光标 (0 表示失败)。
func loadCursor(id uintptr) uintptr {
	h, _, _ := procLoadCursorW.Call(0, id)
	return h
}

// isIconic 报告窗口是否处于最小化态 (WM_MOVE 的过滤条件)。
//
// 为什么要过滤: 最小化时 Windows 会送一条坐标为 (-32000, -32000) 的
// WM_MOVE (它是文档里的哨兵值)。转发出去会同时坏两件事 —— 窗口位置缓存
// 被写成 -32000 (之后 bounds() 一直报错值), 而 onMove 也会收到一个假位置
// 让脚本把"记住的窗口位置"存成垃圾。恢复时系统会再送一条真实坐标的
// WM_MOVE, 所以丢掉这条不丢信息。
func isIconic(hwnd uintptr) bool {
	r, _, _ := procIsIconic.Call(hwnd)
	return r != 0
}

// ===== 样式 / 长指针读写 =====

// getWindowStyle 读 GWL_STYLE。
func getWindowStyle(hwnd uintptr) uintptr {
	if err := procGetWindowLongPtrW.Find(); err == nil {
		v, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
		return v
	}
	v, _, _ := procGetWindowLongW.Call(hwnd, gwlStyle)
	return v
}

// setWindowStyle 写 GWL_STYLE。
//
// SetWindowLongPtrW 在 64 位 Windows 上存在, 32 位上不存在 (头文件里它是
// SetWindowLongW 的宏)。窗口样式是 32 位量, 32 位版完全够 —— 回退不是降级。
func setWindowStyle(hwnd, style uintptr) {
	if err := procSetWindowLongPtrW.Find(); err == nil {
		procSetWindowLongPtrW.Call(hwnd, gwlStyle, style)
		return
	}
	procSetWindowLongW.Call(hwnd, gwlStyle, style)
}

// 编译期断言: *surface 必须满足这三个可选能力。
//
// **为什么要写**: 可选接口的失配是静默的 (类型断言落空 → 退化成 no-op),
// "改个方法名"这种笔误在别的后端上表现为"功能没了但不报错"。用接口字面量
// 断言后, 不满足时**编译**就报错 —— 与 fakeSurface 必须同步转发
// nativeDialogHost 新方法那条教训同源 (见 helpers_test.go 的注释)。
var (
	_ gfx.Surface = (*surface)(nil)
	_ interface {
		MoveTo(x, y int)
		SetLevel(level string)
		SetSizeConstraints(minW, minH, maxW, maxH int)
		SetResizable(on bool)
		SetFullscreen(on bool)
		Activate()
	} = (*surface)(nil)
	_ interface {
		Bounds() (x, y, w, h int)
	} = (*surface)(nil)
	_ interface {
		SetCursor(shape string)
	} = (*surface)(nil)
)
