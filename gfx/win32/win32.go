//go:build windows

// Package win32 是 gfx 的 Windows 窗口后端: 纯标准库 syscall 调用
// user32/gdi32, 不引入 cgo 与任何第三方依赖。
//
// 窗口: RegisterClassExW + CreateWindowExW; 帧缓冲: CreateDIBSection
// (32 位 BGRA), Show 时把 image.RGBA 逐像素转为 BGRA 拷入 DIB 并 BitBlt;
// 消息: MsgWaitForMultipleObjectsEx 限时等待 + PeekMessage 排空。
// WndProc 只投递 Event, 绝不执行 JS。
package win32

import (
	"errors"
	"fmt"
	"image"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/14752222/Gox/gfx"
)

// ===== Win32 过程与常量 =====

var (
	user32                            = syscall.NewLazyDLL("user32.dll")
	gdi32                             = syscall.NewLazyDLL("gdi32.dll")
	kernel32                          = syscall.NewLazyDLL("kernel32.dll")
	procRegisterClassW                = user32.NewProc("RegisterClassW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procPeekMessageW                  = user32.NewProc("PeekMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procMsgWaitForMultipleObjectsEx   = user32.NewProc("MsgWaitForMultipleObjectsEx")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procBeginPaint                    = user32.NewProc("BeginPaint")
	procEndPaint                      = user32.NewProc("EndPaint")
	procInvalidateRect                = user32.NewProc("InvalidateRect")
	procGetDC                         = user32.NewProc("GetDC")
	procReleaseDC                     = user32.NewProc("ReleaseDC")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetProcessDPIAware            = user32.NewProc("SetProcessDPIAware")
	procScreenToClient                = user32.NewProc("ScreenToClient")
	procGetAsyncKeyState              = user32.NewProc("GetAsyncKeyState")
	procTrackMouseEvent               = user32.NewProc("TrackMouseEvent")
	// P2-8: 拖动期间的鼠标捕获 (滑块拖出窗口仍跟手)
	procSetCapture     = user32.NewProc("SetCapture")
	procReleaseCapture = user32.NewProc("ReleaseCapture")

	// P2-7: 输入法 (IME)。imm32 在极老的 Windows 上可能缺席, 懒加载 +
	// 返回值判空即可 (取不到上下文就当这次没有输入法)。
	imm32                        = syscall.NewLazyDLL("imm32.dll")
	procImmGetContext            = imm32.NewProc("ImmGetContext")
	procImmReleaseContext        = imm32.NewProc("ImmReleaseContext")
	procImmGetCompositionStringW = imm32.NewProc("ImmGetCompositionStringW")
	procImmAssociateContext      = imm32.NewProc("ImmAssociateContext")

	// P3-3: 剪贴板
	procOpenClipboard    = user32.NewProc("OpenClipboard")
	procCloseClipboard   = user32.NewProc("CloseClipboard")
	procEmptyClipboard   = user32.NewProc("EmptyClipboard")
	procGetClipboardData = user32.NewProc("GetClipboardData")
	procSetClipboardData = user32.NewProc("SetClipboardData")
	procGlobalAlloc      = kernel32.NewProc("GlobalAlloc")
	procGlobalFree       = kernel32.NewProc("GlobalFree")
	procGlobalLock       = kernel32.NewProc("GlobalLock")
	procGlobalUnlock     = kernel32.NewProc("GlobalUnlock")
	procGlobalSize       = kernel32.NewProc("GlobalSize")

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

	// P3-4 原生对话框
	procMessageBoxW = user32.NewProc("MessageBoxW")
	// comdlg32 在极老的 Windows 上也可能缺席, 懒加载即可 (调用返回 0 会走降级)
	comdlg32             = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFileNameW = comdlg32.NewProc("GetOpenFileNameW")

	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procBitBlt             = gdi32.NewProc("BitBlt")
)

const (
	WM_DESTROY     = 0x0002
	WM_SIZE        = 0x0005
	WM_PAINT       = 0x000F
	WM_CLOSE       = 0x0010
	WM_QUIT        = 0x0012
	WM_KEYDOWN     = 0x0100
	WM_KEYUP       = 0x0101
	WM_CHAR        = 0x0102
	WM_MOUSEMOVE   = 0x0200
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202
	WM_RBUTTONUP   = 0x0205
	WM_MOUSEWHEEL  = 0x020A
	WM_MOUSELEAVE  = 0x02A3
	WM_ACTIVATE    = 0x0006
	WA_INACTIVE    = 0

	WS_OVERLAPPEDWINDOW = 0x00CF0000
	WS_VISIBLE          = 0x10000000

	PM_REMOVE = 0x0001

	QS_ALLINPUT         = 0x04FF
	MWMO_INPUTAVAILABLE = 0x0002
	INFINITE_MS         = 0xFFFFFFFF
	WAIT_TIMEOUT        = 258

	SRCCOPY = 0x00CC0020

	BI_RGB         = 0
	DIB_RGB_COLORS = 0

	// 修饰键虚拟键码 (GetAsyncKeyState 查询用)
	VK_SHIFT   = 0x10
	VK_CONTROL = 0x11
	VK_MENU    = 0x12 // Alt

	TME_LEAVE = 0x00000002

	// P2-7 输入法消息与标志位
	WM_IME_SETCONTEXT  = 0x0281 // 系统要往窗口挂/摘输入上下文
	WM_IME_COMPOSITION = 0x010F // 组合串变化; lParam 的位说明变的是什么
	WM_IME_CHAR        = 0x0286 // 未处理 WM_IME_COMPOSITION 时系统补发的字符
	GCS_RESULTSTR      = 0x0800 // 结果串 (用户已选定的文本) 可用
	// ISC_SHOWUICOMPOSITIONWINDOW 让系统把"正在拼的字"那个小窗画出来
	// (v1 不在输入框里内联预编辑, 全靠这个窗给用户回显)。
	ISC_SHOWUICOMPOSITIONWINDOW = 0x80000000

	// P3-3 剪贴板
	CF_UNICODETEXT = 13
	GMEM_MOVEABLE  = 0x0002
	// clipboardOpenTries / clipboardOpenDelay 是 OpenClipboard 的重试参数:
	// 别的应用正捏着剪贴板时会失败, 不重试就表现为"偶尔复制不到"。
	// 重试期间 GUI 线程是阻塞的, 所以上限压得很小 (5 × 20ms = 100ms)。
	clipboardOpenTries = 5
	clipboardOpenDelay = 20 * time.Millisecond

	// P3-4 原生对话框
	MB_OK             = 0x00000000
	MB_OKCANCEL       = 0x00000001
	MB_ICONINFO       = 0x00000040
	IDOK              = 1
	IDCANCEL          = 2
	OFN_FILEMUSTEXIST = 0x00001000
	OFN_PATHMUSTEXIST = 0x00000800
	OFN_NOCHANGEDIR   = 0x00000008
	OFN_EXPLORER      = 0x00080000
	// dialogPathMax 是 OPENFILENAMEW.lpstrFile 缓冲区的容量。
	// Windows 的 MAX_PATH 是 260, 但长路径可达 32767; 给 1024 是折中 ——
	// 足够覆盖日常路径, 又不至于在栈上开太大。
	dialogPathMax = 1024
)

// vkNames 常用虚拟键 → 键名 (WM_KEYDOWN 路径; 可打印字符走 WM_CHAR)。
var vkNames = map[uintptr]string{
	0x08: "Backspace", 0x09: "Tab", 0x0D: "Enter", 0x10: "Shift",
	0x11: "Control", 0x12: "Alt", 0x1B: "Escape", 0x20: " ",
	0x21: "PageUp", 0x22: "PageDown", 0x23: "End", 0x24: "Home",
	0x25: "ArrowLeft", 0x26: "ArrowUp", 0x27: "ArrowRight", 0x28: "ArrowDown",
	0x2D: "Insert", 0x2E: "Delete",
	0x70: "F1", 0x71: "F2", 0x72: "F3", 0x73: "F4", 0x74: "F5", 0x75: "F6",
	0x76: "F7", 0x77: "F8", 0x78: "F9", 0x79: "F10", 0x7A: "F11", 0x7B: "F12",
}

// 结构体定义 (与 Win32 ABI 对齐)。

// wndClassW 对应 Win32 WNDCLASSW (RegisterClassW 用; 无 cbSize 字段,
// 避开 RegisterClassExW 对 cbSize 的校验兼容问题)。
type wndClassW struct {
	Style                            uint32
	LpfnWndProc                      uintptr
	CbClsExtra, CbWndExtra           int32
	HInstance, HIcon, HCursor, HbrBg uintptr
	LpszMenuName, LpszClassName      *uint16
}

type msg struct {
	Hwnd     syscall.Handle
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       struct{ X, Y int32 }
	LPrivate uint32
}

type bitmapInfoHeader struct {
	BiSize          uint32
	BiWidth         int32
	BiHeight        int32 // 负值 = 自顶向下
	BiPlanes        uint16
	BiBitCount      uint16
	BiCompression   uint32
	BiSizeImage     uint32
	BiXPelsPerMeter int32
	BiYPelsPerMeter int32
	BiClrUsed       uint32
	BiClrImportant  uint32
}

type bitmapInfo struct {
	BmiHeader bitmapInfoHeader
	// BI_RGB 不需要调色板
}

type paintStruct struct {
	Hdc         syscall.Handle
	FErase      int32
	RcPaint     struct{ Left, Top, Right, Bottom int32 }
	FRestore    int32
	FIncUpdate  int32
	RGBReserved [32]byte
}

type rect32 struct {
	Left, Top, Right, Bottom int32
}

// openFileNameW 对应 Win32 OPENFILENAMEW (comdlg32 的 GetOpenFileNameW)。
//
// **布局要点 (x64)**: 结构体里混着 DWORD (4 字节)、指针 (8 字节) 与
// WORD (2 字节), 编译器在 DWORD 与指针之间会插入 4 字节填充 —— 所以
// **字段声明顺序不能按文档顺序随便调**, 也不能用"看起来紧凑"的顺序。
// 下面严格按照 Win32 头文件的声明序排列, 让 Go 的对齐规则与 C 一致:
//
//	lStructSize  DWORD      0    (末尾补 4 字节, 使 hwndOwner 8 字节对齐)
//	hwndOwner    HWND       8
//	hInstance    HINSTANCE  16
//	lpstrFilter  LPCWSTR    24
//	lpstrCustomFilter LPWSTR 32
//	nMaxCustFilter DWORD    40   (+4 填充 → 48)
//	nFilterIndex DWORD      48
//	lpstrFile    LPWSTR     56
//	nMaxFile     DWORD      64   (+4 填充 → 72)
//	lpstrFileTitle LPWSTR   72
//	nMaxFileTitle DWORD     80   (+4 填充 → 88)
//	lpstrInitialDir LPWSTR  88
//	lpstrTitle   LPCWSTR    96
//	Flags        DWORD      104
//	... (之后是 v1 不用的一堆字段, 但**必须留足空间**: GetOpenFileNameW
//	     会按 lStructSize 校验并整体读写, 结构体开小了会踩到后面的内存)
//
// 上面这段布局推演直接对应下面的字段顺序与 uint32/uintptr 类型选择;
// 改字段时先回来读一遍。
type openFileNameW struct {
	StructSize        uint32
	_                 uint32 // 对齐填充 (勿删)
	HwndOwner         uintptr
	HInstance         uintptr
	LpstrFilter       *uint16
	LpstrCustomFilter *uint16
	NMaxCustFilter    uint32
	_                 uint32
	NFilterIndex      uint32
	_                 uint32
	LpstrFile         *uint16
	NMaxFile          uint32
	_                 uint32
	LpstrFileTitle    *uint16
	NMaxFileTitle     uint32
	_                 uint32
	LpstrInitialDir   *uint16
	LpstrTitle        *uint16
	Flags             uint32

	// 以下字段 v1 不使用, 但必须保留占位 (见上方说明)。
	NFileOffset    uint16
	NFileExtension uint16
	LpstrDefExt    *uint16
	LCustData      uintptr
	LpfnHook       uintptr
	LpTemplateName *uint16
	PvReserved     uintptr
	DwReserved     uint32
	FlagsEx        uint32
}

// trackMouseEventStruct 对应 Win32 TRACKMOUSEEVENT (用于订阅 WM_MOUSELEAVE)。
type trackMouseEventStruct struct {
	CbSize      uint32
	DwFlags     uint32
	HwndTrack   syscall.Handle
	DwHoverTime uint32
}

// point32 对应 Win32 POINT。
type point32 struct{ X, Y int32 }

// modifiers 读当前修饰键状态。键消息 lParam 的状态位在部分输入路径下
// 不刷新, 统一用 GetAsyncKeyState 取"当下是否按下"(返回值为负 = 按下)。
func modifiers() (ctrl, shift, alt bool) {
	down := func(vk uintptr) bool {
		r, _, _ := procGetAsyncKeyState.Call(vk)
		return int16(r) < 0
	}
	return down(VK_CONTROL), down(VK_SHIFT), down(VK_MENU)
}

// ===== 工厂注册 =====

func init() {
	gfx.SetDefaultFactory(&factory{})
	enableDPIAwareness()
}

type factory struct{}

func (f *factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	return newSurface(cfg)
}

// enableDPIAwareness 尽力开启 DPI 感知 (Per-Monitor V2 → 逐级回退)。
func enableDPIAwareness() {
	if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = (HANDLE)-4
		procSetProcessDpiAwarenessContext.Call(^uintptr(3))
		return
	}
	procSetProcessDPIAware.Call()
}

// ===== Surface 实现 =====

var (
	className  *uint16
	wndProcPtr uintptr
	clsOnce    sync.Once
	clsErr     error
)

type surface struct {
	hwnd   syscall.Handle
	events chan gfx.Event
	mu     sync.Mutex
	closed bool

	// trackingLeave 标记已订阅 WM_MOUSELEAVE (TrackMouseEvent 的订阅只报一次,
	// 光标再次进入后需要重新订阅)
	trackingLeave bool

	// P2-7 输入法: imeOn 是本窗口当前的输入法开关 (由 gfx 按焦点设置),
	// himc 是"关掉时被换下来的系统默认输入上下文" —— 重新打开只能还回它。
	imeOn bool
	himc  uintptr

	// DIB 帧缓冲 (与窗口客户区同尺寸)
	dibMem  unsafe.Pointer // DIB 内存首址 (生命周期由 dibBmp 句柄持有)
	dibSize int
	dibW    int
	dibH    int
	dibBmp  syscall.Handle
	memDC   syscall.Handle
	oldBmp  syscall.Handle
}

func newSurface(cfg gfx.WindowConfig) (gfx.Surface, error) {
	clsOnce.Do(func() {
		className, _ = syscall.UTF16PtrFromString("GoxGfxWindow")
		wndProcPtr = syscall.NewCallback(globalWndProc)
		hInst, _, _ := procGetModuleHandleW.Call(0)
		wc := wndClassW{
			LpfnWndProc:   wndProcPtr,
			HInstance:     hInst,
			LpszClassName: className,
		}
		atom, _, err := procRegisterClassW.Call(uintptr(unsafe.Pointer(&wc)))
		if atom == 0 {
			clsErr = fmt.Errorf("RegisterClassW failed: %w", err)
		}
	})
	if clsErr != nil {
		return nil, clsErr
	}

	title, _ := syscall.UTF16PtrFromString(cfg.Title)
	hInst, _, _ := procGetModuleHandleW.Call(0)
	hwnd, _, err := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		WS_OVERLAPPEDWINDOW|WS_VISIBLE,
		0x80000000, 0x80000000, // CW_USEDEFAULT
		uintptr(cfg.Width), uintptr(cfg.Height),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		return nil, err
	}

	s := &surface{
		hwnd:   syscall.Handle(hwnd),
		events: make(chan gfx.Event, 256),
	}
	// P2-7: 建窗即关输入法, 让 imeOn=false 与真实状态一致 (gfx 在焦点落到
	// input/textarea 上时才开)。返回值是系统挂上来的默认输入上下文, 存起来
	// 供重新启用时还回去 —— 传别的值会让输入法挂错上下文。
	if r, _, _ := procImmAssociateContext.Call(hwnd, 0); r != 0 {
		s.himc = r
	}
	surfacesMu.Lock()
	surfaces[hwnd] = s
	surfacesMu.Unlock()

	w, h := s.Size()
	s.reallocDIB(w, h)
	return s, nil
}

// surfaces 把 hwnd 映射回 surface (WndProc 回调查找)。
var (
	surfacesMu sync.Mutex
	surfaces   = map[uintptr]*surface{}
)

// globalWndProc 窗口过程: 只投递事件/处理绘制, 不执行任何 JS。
func globalWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	surfacesMu.Lock()
	s := surfaces[hwnd]
	surfacesMu.Unlock()
	if s == nil {
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	}

	switch msg {
	case WM_LBUTTONDOWN:
		s.trySend(gfx.Event{Kind: gfx.EventMouseDown, X: lo16(lParam), Y: hi16(lParam)})
		return 0
	case WM_LBUTTONUP:
		s.trySend(gfx.Event{Kind: gfx.EventMouseUp, X: lo16(lParam), Y: hi16(lParam)})
		return 0
	case WM_RBUTTONUP:
		s.trySend(gfx.Event{Kind: gfx.EventMouseRightUp, X: lo16(lParam), Y: hi16(lParam)})
		return 0
	case WM_MOUSEMOVE:
		s.trackLeave(hwnd)
		s.trySend(gfx.Event{Kind: gfx.EventMouseMove, X: lo16(lParam), Y: hi16(lParam)})
		return 0
	case WM_MOUSELEAVE:
		s.mu.Lock()
		s.trackingLeave = false
		s.mu.Unlock()
		s.trySend(gfx.Event{Kind: gfx.EventMouseLeave})
		return 0
	case WM_ACTIVATE:
		// 切到别的窗口时鼠标已经不在我们这儿了, 主动清掉悬停/按压态,
		// 否则回到窗口前按钮一直是"悬停/按住"的假状态。
		if lo16(wParam) == WA_INACTIVE {
			s.trySend(gfx.Event{Kind: gfx.EventMouseLeave})
		}
		return 0
	case WM_MOUSEWHEEL:
		// 高 16 位是带符号的滚轮增量 (WHEEL_DELTA = 120 一格, 向上为正);
		// lParam 是屏幕坐标, 而 gfx 事件坐标一律是客户区坐标, 需转换。
		delta := int(int16((wParam >> 16) & 0xFFFF))
		var pt point32
		pt.X, pt.Y = int32(lo16(lParam)), int32(hi16(lParam))
		procScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt)))
		s.trySend(gfx.Event{Kind: gfx.EventMouseWheel, X: int(pt.X), Y: int(pt.Y), DeltaY: delta})
		return 0
	case WM_KEYDOWN:
		// 可打印字符交给 WM_CHAR (避免重复投递); 其余映射为键名
		if name, ok := vkNames[wParam]; ok {
			if wParam == 0x20 { // 空格两者都发, 这里跳过留给 WM_CHAR
				return 0
			}
			ctrl, shift, alt := modifiers()
			s.trySend(gfx.Event{Kind: gfx.EventKeyDown, Key: name, Ctrl: ctrl, Shift: shift, Alt: alt})
		}
		return 0
	case WM_KEYUP:
		if name, ok := vkNames[wParam]; ok {
			if wParam == 0x20 {
				return 0
			}
			ctrl, shift, alt := modifiers()
			s.trySend(gfx.Event{Kind: gfx.EventKeyUp, Key: name, Ctrl: ctrl, Shift: shift, Alt: alt})
		}
		return 0
	case WM_CHAR:
		// wParam 是 UTF-16 单元; v1 仅处理 BMP 字符
		ch := rune(wParam & 0xFFFF)
		if ch >= 0xD800 && ch <= 0xDFFF || ch < 0x20 && ch != 0x20 {
			if ch == 0x0D { // Enter 在部分输入流里只来 CHAR
				s.trySend(gfx.Event{Kind: gfx.EventKeyDown, Key: "Enter"})
			}
			return 0
		}
		s.trySend(gfx.Event{Kind: gfx.EventKeyDown, Key: string(ch)})
		return 0
	case WM_IME_SETCONTEXT:
		// P2-7: 系统想往本窗口挂输入上下文。焦点不在可编辑控件上时吞掉 ——
		// 否则在按钮/画布上敲字也会弹候选窗。开着的时候必须走 DefWindowProc
		// (真正把上下文关联起来的是它), 顺带要求显示组合窗。
		if !s.imeOn {
			return 0
		}
		if wParam != 0 {
			lParam |= ISC_SHOWUICOMPOSITIONWINDOW
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	case WM_IME_COMPOSITION:
		if lParam&GCS_RESULTSTR != 0 {
			if str := s.imeResultString(); str != "" {
				s.trySend(gfx.Event{Kind: gfx.EventIMECommit, Text: str})
				return 0
			}
		}
		r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
		return r
	case WM_IME_CHAR:
		// 已由 WM_IME_COMPOSITION 投递过结果串, 这里必须吞掉: 不然同一批
		// 字符会再走一次 WM_CHAR, 每个字被插两遍 (输入"你好"变成"你你好好")。
		return 0
	case WM_SIZE:
		w, h := int(lo16(lParam)), int(hi16(lParam))
		s.reallocDIB(w, h)
		s.trySend(gfx.Event{Kind: gfx.EventResize, W: w, H: h})
		return 0
	case WM_PAINT:
		var ps paintStruct
		hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		s.mu.Lock()
		valid := s.dibMem != nil && s.memDC != 0
		w, h := s.dibW, s.dibH
		memDC := uintptr(s.memDC)
		s.mu.Unlock()
		if valid {
			procBitBlt.Call(hdc, 0, 0, uintptr(w), uintptr(h), memDC, 0, 0, SRCCOPY)
		}
		procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
		return 0
	case WM_CLOSE:
		procDestroyWindow.Call(hwnd)
		return 0
	case WM_DESTROY:
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.trySend(gfx.Event{Kind: gfx.EventClose})
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, uintptr(msg), wParam, lParam)
	return r
}

// ===== P3-3: 剪贴板 =====

// openClipboard 带重试地打开剪贴板 (被别的进程占用时会失败)。
// 调用方成功后必须 `defer procCloseClipboard.Call()` —— Open/Close 必须成对,
// 否则剪贴板会一直被本进程锁着, 其它应用再也复制不了。
func (s *surface) openClipboard() bool {
	for i := 0; i < clipboardOpenTries; i++ {
		if r, _, _ := procOpenClipboard.Call(uintptr(s.hwnd)); r != 0 {
			return true
		}
		time.Sleep(clipboardOpenDelay)
	}
	return false
}

// ReadClipboardText 取剪贴板里的文本 (UTF-8), 实现 gfx 的 clipboardHost。
//
// 数据块是 **UTF-16 + NUL 结尾**, 长度只能从 GlobalSize 反推: 直接用
// `syscall.UTF16ToString` 需要提前知道长度, 而这里只有句柄。读到 NUL 即停,
// 并以 GlobalSize/2 为上限 —— 没有上限的话, 一个没有终止符的坏块会让这里
// 一直读进别人的内存。
func (s *surface) ReadClipboardText() (string, error) {
	if !s.openClipboard() {
		return "", errors.New("gfx: 打不开剪贴板 (可能被别的进程占用)")
	}
	defer procCloseClipboard.Call()

	h, _, _ := procGetClipboardData.Call(uintptr(CF_UNICODETEXT))
	if h == 0 {
		return "", errors.New("gfx: 剪贴板里没有文本")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		return "", errors.New("gfx: GlobalLock 失败")
	}
	defer procGlobalUnlock.Call(h)
	sz, _, _ := procGlobalSize.Call(h)
	max := int(sz) / 2
	if max <= 0 {
		return "", nil
	}
	// uintptr → unsafe.Pointer 直接转换会被 vet 判为 "possible misuse",
	// 这里用 reallocDIB 那一手: 经指针间接完成 (内存由剪贴板句柄持有保活)。
	// 拿到切片后按值索引, 就不需要任何指针算术了。
	sl := unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), max)
	n := 0
	for n < len(sl) && sl[n] != 0 {
		n++
	}
	return string(utf16.Decode(sl[:n])), nil
}

// WriteClipboardText 把文本放到剪贴板, 实现 gfx 的 clipboardHost。
//
// 两个易错点:
//  1. 分配 **字节数** 时要给结尾的 NUL 留一个 UTF-16 单元
//     (emoji / 扩展区汉字一个 rune 占两个单元, 所以先 `utf16.Encode` 再算);
//  2. `SetClipboardData` **成功之后内存归系统**, 绝不能再 `GlobalFree` ——
//     只有它失败 (或 GlobalLock 失败) 时才需要自己释放。
func (s *surface) WriteClipboardText(text string) error {
	units := utf16.Encode([]rune(text))
	size := uintptr((len(units) + 1) * 2)
	if !s.openClipboard() {
		return errors.New("gfx: 打不开剪贴板 (可能被别的进程占用)")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()

	h, _, _ := procGlobalAlloc.Call(GMEM_MOVEABLE, size)
	if h == 0 {
		return errors.New("gfx: GlobalAlloc 失败")
	}
	p, _, _ := procGlobalLock.Call(h)
	if p == 0 {
		procGlobalFree.Call(h)
		return errors.New("gfx: GlobalLock 失败")
	}
	// 同上: 转成切片后按值写入 (末位留给结尾的 NUL)
	sl := unsafe.Slice((*uint16)(*(*unsafe.Pointer)(unsafe.Pointer(&p))), len(units)+1)
	copy(sl, units)
	sl[len(units)] = 0
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(uintptr(CF_UNICODETEXT), h); r == 0 {
		procGlobalFree.Call(h)
		return errors.New("gfx: SetClipboardData 失败")
	}
	return nil
}

// ===== P3-4: 原生对话框 =====

// utf16Ptr 把 Go 字符串转成 NUL 结尾的 UTF-16 缓冲, 返回首元素指针。
//
// 返回切片而不是裸指针: 让调用方显式持有它, 避免"转完就没人引用、
// 被 GC 回收而 Windows 还在读"的经典悬空指针问题。
func utf16Ptr(s string) (*uint16, []uint16) {
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	return &u[0], u
}

// ShowMessage 弹消息框, 实现 gfx 的 nativeDialogHost。
//
// **这里不需要另开 goroutine, 也不需要手动泵消息**: 传入 hwndOwner 后
// Windows 会自动禁用该窗口并把对话框归一到本线程的消息队列,
// 直到它返回 —— 重绘、拖动、其它窗口的输入都照常。
// (若在别的 goroutine 里调用, 就没有这个保证, 而且跨线程碰 UI 本身就是
// Win32 的雷区。见 gfx/dialog.go 文件头关于线程模型的说明。)
func (s *surface) ShowMessage(kind gfx.NativeDialogKind, title, message string) (bool, error) {
	flags := uintptr(MB_OK | MB_ICONINFO)
	if kind == gfx.DialogConfirm {
		flags = MB_OKCANCEL | MB_ICONINFO
	}
	if title == "" {
		title = "Gox"
	}
	tp, t := utf16Ptr(title)
	mp, m := utf16Ptr(message)
	// 保留引用直到调用返回 (防 GC)
	runtime.KeepAlive(t)
	runtime.KeepAlive(m)

	ret, _, _ := procMessageBoxW.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(mp)),
		uintptr(unsafe.Pointer(tp)), flags)
	// 返回 0 表示失败 (通常是内存不足); 其它情况返回被按下按钮的 ID。
	if ret == 0 {
		return false, errors.New("gfx: MessageBoxW 失败")
	}
	if kind == gfx.DialogConfirm {
		return ret == uintptr(IDOK), nil
	}
	return true, nil
}

// ShowOpenFile 弹"打开文件"对话框, 实现 gfx 的 nativeDialogHost。
//
// 取消时返回 ok=false 且 err=nil —— 用户按取消是正常操作,
// 不是错误 (GetOpenFileNameW 的返回值 0 同时表示"取消"和"出错",
// 要靠 CommDlgExtendedError 才能区分, 而 v1 刻意不引它: 把取消当错误
// 会让每个脚本都被迫写 try/catch)。
func (s *surface) ShowOpenFile(opts gfx.NativeFileOptions) (string, bool, error) {
	buf := make([]uint16, dialogPathMax)
	if opts.Default != "" {
		d := utf16.Encode([]rune(opts.Default))
		if len(d) < dialogPathMax-1 {
			copy(buf, d)
		}
	}
	fp, f := utf16Ptr(buildFilter(opts.Filter))
	runtime.KeepAlive(f)

	var titlePtr *uint16
	var titleKeep []uint16
	if opts.Title != "" {
		titlePtr, titleKeep = utf16Ptr(opts.Title)
		runtime.KeepAlive(titleKeep)
	}
	var dirPtr *uint16
	var dirKeep []uint16
	if opts.Dir != "" {
		dirPtr, dirKeep = utf16Ptr(opts.Dir)
		runtime.KeepAlive(dirKeep)
	}

	ofn := openFileNameW{
		StructSize:      uint32(unsafe.Sizeof(openFileNameW{})),
		HwndOwner:       uintptr(s.hwnd),
		HInstance:       uintptr(moduleHandle()),
		LpstrFilter:     fp,
		NFilterIndex:    1,
		LpstrFile:       &buf[0],
		NMaxFile:        uint32(len(buf)),
		LpstrInitialDir: dirPtr,
		LpstrTitle:      titlePtr,
		// NOCHANGEDIR: 不然对话框会把**进程的当前目录**改到用户选的目录,
		// 而 `<image src="./a.png">` 是相对进程工作目录解析的 —— 选完文件
		// 之后所有相对路径就全错了 (这类副作用极难排查)。
		Flags: OFN_FILEMUSTEXIST | OFN_PATHMUSTEXIST | OFN_NOCHANGEDIR | OFN_EXPLORER,
	}

	r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn)))
	if r == 0 {
		return "", false, nil
	}
	// lpstrFile 是 NUL 结尾的完整路径
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(utf16.Decode(buf[:n])), true, nil
}

// buildFilter 把结构化过滤规则拼成 Win32 要求的过滤串。
//
// 格式是 `描述\x00通配符\x00描述\x00通配符\x00\x00` —— **结尾是双 NUL**,
// 少一个 Windows 就会一直往后读。这是本文件里最容易漏的一处。
// 没给过滤时返回 "所有文件\0*.*\0\0" (不给它, 对话框会没有类型下拉框)。
func buildFilter(filters []gfx.NativeFileFilter) string {
	if len(filters) == 0 {
		filters = []gfx.NativeFileFilter{{Name: "所有文件", Pattern: "*.*"}}
	}
	var b strings.Builder
	for _, f := range filters {
		name := f.Name
		if name == "" {
			name = f.Pattern
		}
		pat := f.Pattern
		if pat == "" {
			pat = "*.*"
		}
		// 描述里不能夹 NUL (会把过滤串提前截断, 表现为"类型下拉框少了几项")
		b.WriteString(strings.ReplaceAll(name, "\x00", ""))
		b.WriteByte(0)
		b.WriteString(strings.ReplaceAll(pat, "\x00", ""))
		b.WriteByte(0)
	}
	b.WriteByte(0) // 结尾的第二个 NUL
	return b.String()
}

// moduleHandle 取本进程的模块句柄 (OPENFILENAMEW.hInstance 用;
// 传 0 也能工作, 但显式给更规范)。
func moduleHandle() syscall.Handle {
	h, _, _ := procGetModuleHandleW.Call(0)
	return syscall.Handle(h)
}

// ===== P2-7: 输入法 =====

// imeResultString 取本次提交的结果串 (UTF-8)。
//
// 长度只能按**字节**算: ImmGetCompositionStringW 第一次传空缓冲返回的是
// 所需字节数 (UTF-16 单元数 × 2), 当字符数用会把代理对 (emoji / 扩展区汉字)
// 截掉一半。
func (s *surface) imeResultString() string {
	himc, _, _ := procImmGetContext.Call(uintptr(s.hwnd))
	if himc == 0 {
		return ""
	}
	defer procImmReleaseContext.Call(uintptr(s.hwnd), himc)
	size, _, _ := procImmGetCompositionStringW.Call(himc, GCS_RESULTSTR, 0, 0)
	if size <= 0 {
		return ""
	}
	buf := make([]uint16, size/2+1)
	got, _, _ := procImmGetCompositionStringW.Call(himc, GCS_RESULTSTR,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*2))
	if got <= 0 {
		return ""
	}
	return string(utf16.Decode(buf[:got/2]))
}

// SetIMEEnabled 开/关本窗口的输入法, 实现 gfx 的可选 imeController 接口。
// 由 gfx 在键盘焦点变化时调用 (只有焦点在 input/textarea 上才开)。
//
// 关 = 把输入上下文换成 0, 返回值是被换下来的那个 (系统默认上下文), 存起来:
// 重新打开时只能还回这个句柄, 传别的值会让输入法挂错上下文 (表现为"切回
// 输入框后输入法失灵")。
func (s *surface) SetIMEEnabled(on bool) {
	s.mu.Lock()
	hwnd := s.hwnd
	s.imeOn = on
	prev := s.himc
	s.mu.Unlock()
	if on {
		if prev != 0 {
			procImmAssociateContext.Call(uintptr(hwnd), prev)
		}
		return
	}
	if r, _, _ := procImmAssociateContext.Call(uintptr(hwnd), 0); r != 0 {
		s.mu.Lock()
		s.himc = r
		s.mu.Unlock()
	}
}

func lo16(l uintptr) int { return int(int16(l & 0xFFFF)) }
func hi16(l uintptr) int { return int(int16((l >> 16) & 0xFFFF)) }

// trackLeave 订阅一次 WM_MOUSELEAVE: 光标离开客户区时系统回调过来,
// gfx 据此清掉悬停/按压态。TrackMouseEvent 的订阅只报一次, 需重新订阅。
func (s *surface) trackLeave(hwnd uintptr) {
	s.mu.Lock()
	already := s.trackingLeave
	s.trackingLeave = true
	s.mu.Unlock()
	if already {
		return
	}
	tme := trackMouseEventStruct{
		CbSize:    uint32(unsafe.Sizeof(trackMouseEventStruct{})),
		DwFlags:   TME_LEAVE,
		HwndTrack: syscall.Handle(hwnd),
	}
	procTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
}

// trySend 非阻塞投递事件 (通道满则丢弃, 避免 WndProc 阻塞)。
func (s *surface) trySend(ev gfx.Event) {
	select {
	case s.events <- ev:
	default:
	}
}

func (s *surface) Events() <-chan gfx.Event { return s.events }

// CapturePointer / ReleasePointer 实现 gfx 的可选 capturer 接口 (P2-8):
// 捕获期间鼠标事件全部送到本窗口, 滑块拖出客户区仍然跟手, 松手也能收到。
func (s *surface) CapturePointer() { procSetCapture.Call(uintptr(s.hwnd)) }

func (s *surface) ReleasePointer() { procReleaseCapture.Call() }

// Size 返回客户区尺寸。
func (s *surface) Size() (int, int) {
	var rc rect32
	procGetClientRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&rc)))
	return int(rc.Right), int(rc.Bottom)
}

// reallocDIB 重建 DIB 帧缓冲 (尺寸变化时)。
func (s *surface) reallocDIB(w, h int) {
	if w <= 0 || h <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dibW == w && s.dibH == h && s.dibMem != nil {
		return
	}
	s.releaseDIBLocked()

	hdc, _, _ := procGetDC.Call(uintptr(s.hwnd))
	defer procReleaseDC.Call(uintptr(s.hwnd), hdc)

	bi := bitmapInfo{bitmapInfoHeader{
		BiSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		BiWidth:       int32(w),
		BiHeight:      -int32(h), // 自顶向下
		BiPlanes:      1,
		BiBitCount:    32,
		BiCompression: BI_RGB,
	}}
	var bits uintptr
	memDC, _, _ := procCreateCompatibleDC.Call(hdc)
	bmp, _, _ := procCreateDIBSection.Call(hdc, uintptr(unsafe.Pointer(&bi)), DIB_RGB_COLORS, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || memDC == 0 {
		return
	}
	old, _, _ := procSelectObject.Call(memDC, bmp)
	s.memDC = syscall.Handle(memDC)
	s.oldBmp = syscall.Handle(old)
	s.dibBmp = syscall.Handle(bmp)
	// uintptr → unsafe.Pointer 用位转换绕过 vet 检查是非法的;
	// 这里在系统调用现场经指针间接完成转换, 内存由 dibBmp 持有保活。
	s.dibMem = *(*unsafe.Pointer)(unsafe.Pointer(&bits))
	s.dibSize = w * h * 4
	s.dibW, s.dibH = w, h
}

// releaseDIBLocked 释放旧帧缓冲 (调用方持锁)。
func (s *surface) releaseDIBLocked() {
	if s.memDC != 0 {
		if s.oldBmp != 0 {
			procSelectObject.Call(uintptr(s.memDC), uintptr(s.oldBmp))
		}
		procDeleteDC.Call(uintptr(s.memDC))
		s.memDC, s.oldBmp = 0, 0
	}
	if s.dibBmp != 0 {
		procDeleteObject.Call(uintptr(s.dibBmp))
		s.dibBmp = 0
	}
	s.dibMem = nil
	s.dibSize, s.dibW, s.dibH = 0, 0, 0
}

// Show 把一整帧上屏 (等价 ShowRegions(img, nil))。
func (s *surface) Show(img *image.RGBA) {
	s.ShowRegions(img, nil)
}

// ShowRegions 把一帧像素拷入 DIB, 只对给定区域请求重绘 (脏矩形局部呈现;
// rects 为空 = 整帧)。WM_PAINT 时按 ps.rcPaint 范围 BitBlt。
func (s *surface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	s.mu.Lock()
	mem, size, w, h, valid := s.dibMem, s.dibSize, s.dibW, s.dibH, s.dibMem != nil
	s.mu.Unlock()
	if !valid {
		return
	}
	// 尺寸不匹配时取交集 (resize 事件未及重建的过渡帧)
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	cw, ch := min2(iw, w), min2(ih, h)

	dst := (*[1 << 30]byte)(mem)[:size:size]
	for y := 0; y < ch; y++ {
		srcRow := img.Pix[y*img.Stride:]
		dstRow := dst[y*w*4:]
		for x := 0; x < cw; x++ {
			i := x * 4
			dstRow[i] = srcRow[i+2]   // B
			dstRow[i+1] = srcRow[i+1] // G
			dstRow[i+2] = srcRow[i]   // R
			dstRow[i+3] = 255         // X → 不透明
		}
	}

	// 局部呈现: 逐矩形 InvalidateRect; Windows 合并更新区,
	// WM_PAINT 按 ps.rcPaint 包围盒 BitBlt
	if len(rects) == 0 {
		procInvalidateRect.Call(uintptr(s.hwnd), 0, 1)
		return
	}
	for _, r := range rects {
		if r.Min.X < 0 {
			r.Min.X = 0
		}
		if r.Min.Y < 0 {
			r.Min.Y = 0
		}
		if r.Max.X > w {
			r.Max.X = w
		}
		if r.Max.Y > h {
			r.Max.Y = h
		}
		if r.Min.X >= r.Max.X || r.Min.Y >= r.Max.Y {
			continue
		}
		rc := rect32{int32(r.Min.X), int32(r.Min.Y), int32(r.Max.X), int32(r.Max.Y)}
		procInvalidateRect.Call(uintptr(s.hwnd), uintptr(unsafe.Pointer(&rc)), 1)
	}
}

// WaitEvents 等待消息至多 maxWait (<=0 无限期), 排空并分发。
// 窗口销毁后返回 false。
func (s *surface) WaitEvents(maxWait time.Duration) bool {
	timeout := uint32(INFINITE_MS)
	if maxWait > 0 {
		if ms := maxWait.Milliseconds(); ms > 0 {
			timeout = uint32(ms)
		}
	}
	procMsgWaitForMultipleObjectsEx.Call(0, 0, uintptr(timeout), QS_ALLINPUT, MWMO_INPUTAVAILABLE)

	// 排空消息队列 (DispatchMessage → WndProc → 事件投递)
	var m msg
	for {
		ret, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, PM_REMOVE)
		if ret == 0 {
			break
		}
		if m.Message == WM_QUIT {
			return false
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}

	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	return !closed
}

func min2(a, b int) int {
	if a < b {
		return a
	}
	return b
}
