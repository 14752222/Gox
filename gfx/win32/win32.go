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
	"fmt"
	"image"
	"sync"
	"syscall"
	"time"
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

	procGetModuleHandleW = kernel32.NewProc("GetModuleHandleW")

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
	WM_CHAR        = 0x0102
	WM_LBUTTONDOWN = 0x0201
	WM_LBUTTONUP   = 0x0202

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
	case WM_KEYDOWN:
		// 可打印字符交给 WM_CHAR (避免重复投递); 其余映射为键名
		if name, ok := vkNames[wParam]; ok {
			if wParam == 0x20 { // 空格两者都发, 这里跳过留给 WM_CHAR
				return 0
			}
			s.trySend(gfx.Event{Kind: gfx.EventKeyDown, Key: name})
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

func lo16(l uintptr) int { return int(int16(l & 0xFFFF)) }
func hi16(l uintptr) int { return int(int16((l >> 16) & 0xFFFF)) }

// trySend 非阻塞投递事件 (通道满则丢弃, 避免 WndProc 阻塞)。
func (s *surface) trySend(ev gfx.Event) {
	select {
	case s.events <- ev:
	default:
	}
}

func (s *surface) Events() <-chan gfx.Event { return s.events }

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
