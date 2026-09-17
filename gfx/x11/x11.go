//go:build linux

// Package x11 是 gfx 的 Linux X11 窗口后端。
//
// 基于 jezek/xgb (纯 Go 的 X11 协议实现, 无 cgo): 直接建窗口、PutImage
// 上屏 (ZPixmap 24 位深度, 小端字节序下每像素 4 字节 = B,G,R,X)、
// 按钮/按键/关闭事件转 gfx.Event。等待采用独立读事件 goroutine + 定时
// select, WaitEvents 可限时。关闭按钮经 WM_PROTOCOLS/WM_DELETE_WINDOW。
// xgb 的请求多为 unchecked (返回 Cookie 不返回错误), 协议错误经
// WaitForEvent 以 Event/XError 形式回来。
package x11

import (
	"encoding/binary"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func init() {
	gfx.SetDefaultFactory(&factory{})
}

type factory struct{}

func (f *factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	return newSurface(cfg)
}

type surface struct {
	conn *xgb.Conn
	win  xproto.Window
	gc   xproto.Gcontext

	wmProtocols xproto.Atom
	wmDelete    xproto.Atom

	events chan gfx.Event
	evch   chan xgb.Event // 读事件 goroutine → WaitEvents
	errch  chan error

	// keyNames: 键码 → 键名 (启动时拉取键盘映射构建)
	keyNames map[xproto.Keycode]string

	mu     sync.Mutex
	closed bool
	w, h   int
}

func newSurface(cfg gfx.WindowConfig) (gfx.Surface, error) {
	conn, err := xgb.NewConn() // 连接 $DISPLAY
	if err != nil {
		return nil, fmt.Errorf("connect X server: %w (DISPLAY set?)", err)
	}
	setup := xproto.Setup(conn)
	screen := setup.DefaultScreen(conn)

	w, h := cfg.Width, cfg.Height
	if w <= 0 {
		w = 400
	}
	if h <= 0 {
		h = 300
	}

	win, err := xproto.NewWindowId(conn)
	if err != nil {
		return nil, err
	}
	// 事件: 结构变化/按键/按钮 (曝光不需要, gfx 自管重绘)
	const eventMask = xproto.EventMaskStructureNotify |
		xproto.EventMaskKeyPress | xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease
	xproto.CreateWindow(conn, screen.RootDepth, win, screen.Root,
		0, 0, uint16(w), uint16(h), 1,
		xproto.WindowClassInputOutput, screen.RootVisual,
		xproto.CwEventMask, []uint32{eventMask})

	gc, err := xproto.NewGcontextId(conn)
	if err != nil {
		return nil, err
	}
	xproto.CreateGC(conn, gc, xproto.Drawable(win), 0, nil)

	s := &surface{
		conn: conn, win: win, gc: gc,
		events: make(chan gfx.Event, 256),
		evch:   make(chan xgb.Event, 256),
		errch:  make(chan error, 1),
		w:      w, h: h,
	}

	// 标题 (WM_NAME)
	xproto.ChangeProperty(conn, xproto.PropModeReplace, win, xproto.AtomWmName,
		xproto.AtomString, 8, uint32(len(cfg.Title)), []byte(cfg.Title))

	// WM_DELETE_WINDOW: 点关闭按钮发 ClientMessage 而不是硬断连接
	s.wmProtocols = internAtom(conn, "WM_PROTOCOLS")
	s.wmDelete = internAtom(conn, "WM_DELETE_WINDOW")
	if s.wmProtocols != 0 && s.wmDelete != 0 {
		var delBuf [4]byte
		binary.LittleEndian.PutUint32(delBuf[:], uint32(s.wmDelete))
		xproto.ChangeProperty(conn, xproto.PropModeReplace, win, s.wmProtocols,
			xproto.AtomAtom, 32, 1, delBuf[:])
	}

	s.loadKeymap(setup)

	xproto.MapWindow(conn, win)
	conn.Sync() // 冲刷建窗请求并确认连接可用

	// 独立读事件 goroutine: xgb 的 WaitForEvent 阻塞, 借通道转成可 select
	go func() {
		for {
			ev, err := conn.WaitForEvent()
			if err != nil {
				select {
				case s.errch <- err:
				default:
				}
				return
			}
			if ev == nil {
				select {
				case s.errch <- fmt.Errorf("x11 connection closed"):
				default:
				}
				return
			}
			s.evch <- ev
		}
	}()
	return s, nil
}

// loadKeymap 启动时一次性拉取键码→键符映射, 构建键码→键名表。
func (s *surface) loadKeymap(setup *xproto.SetupInfo) {
	s.keyNames = map[xproto.Keycode]string{}
	count := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	if count == 0 {
		return
	}
	reply, err := xproto.GetKeyboardMapping(s.conn, setup.MinKeycode, count).Reply()
	if err != nil {
		return
	}
	symsPer := int(reply.KeysymsPerKeycode)
	if symsPer == 0 {
		return
	}
	for i := 0; i < int(count); i++ {
		ks := uint32(reply.Keysyms[i*symsPer]) // 首键符 (未修饰)
		if name := KeysymName(ks); name != "" {
			s.keyNames[setup.MinKeycode+xproto.Keycode(i)] = name
		}
	}
}

// KeysymName 键符 → 键名 (与 win32 后端同一套命名)。
// 可打印 ASCII 直接取字符; 功能键按 X11 标准键符区间映射。
func KeysymName(ks uint32) string {
	switch {
	case ks >= 0x20 && ks <= 0x7E:
		return string(rune(ks))
	case ks >= 0x01000100 && ks <= 0x0100ffff: // Latin-1 补充起的 Unicode 区
		return string(rune(ks - 0x01000000))
	}
	switch ks {
	case 0xFF08:
		return "Backspace"
	case 0xFF09:
		return "Tab"
	case 0xFF0D:
		return "Enter"
	case 0xFF1B:
		return "Escape"
	case 0xFF50:
		return "Home"
	case 0xFF51:
		return "ArrowLeft"
	case 0xFF52:
		return "ArrowUp"
	case 0xFF53:
		return "ArrowRight"
	case 0xFF54:
		return "ArrowDown"
	case 0xFF55:
		return "PageUp"
	case 0xFF56:
		return "PageDown"
	case 0xFF57:
		return "End"
	case 0xFFFF:
		return "Delete"
	}
	if ks >= 0xFFBE && ks <= 0xFFC9 { // F1..F12
		return fmt.Sprintf("F%d", ks-0xFFBE+1)
	}
	return ""
}

func internAtom(conn *xgb.Conn, name string) xproto.Atom {
	r, err := xproto.InternAtom(conn, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0
	}
	return r.Atom
}

func (s *surface) Events() <-chan gfx.Event { return s.events }

// Size 返回当前窗口尺寸 (ConfigureNotify 维护)。
func (s *surface) Size() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w, s.h
}

// Show 整帧上屏。
func (s *surface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

// ShowRegions 逐区域 PutImage 上屏 (rects 空 = 整帧)。
func (s *surface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	if len(rects) == 0 {
		rects = []image.Rectangle{img.Bounds()}
	}
	for _, r := range rects {
		r = r.Intersect(img.Bounds())
		if r.Empty() {
			continue
		}
		xproto.PutImage(s.conn, xproto.ImageFormatZPixmap, xproto.Drawable(s.win),
			s.gc, uint16(r.Dx()), uint16(r.Dy()), int16(r.Min.X), int16(r.Min.Y),
			0, 24, RGBAToXZPixmap(img, r))
	}
	s.conn.Sync() // 确保请求已送达 (xgb 无独立 flush 入口)
}

// RGBAToXZPixmap 把 img 的 r 区域转为 X11 ZPixmap(24 深度, 32bpp)字节流:
// 小端客户端字节序下每像素 4 字节 = B,G,R,unused。与 win32 DIB 同构。
func RGBAToXZPixmap(img *image.RGBA, r image.Rectangle) []byte {
	w := r.Dx()
	buf := make([]byte, w*r.Dy()*4)
	for y := 0; y < r.Dy(); y++ {
		src := img.Pix[(r.Min.Y+y)*img.Stride:]
		dst := buf[y*w*4:]
		for x := 0; x < w; x++ {
			i := (r.Min.X + x) * 4
			j := x * 4
			dst[j] = src[i+2]   // B
			dst[j+1] = src[i+1] // G
			dst[j+2] = src[i]   // R
			dst[j+3] = 0        // unused
		}
	}
	return buf
}

// WaitEvents 等待 X 事件至多 maxWait (<=0 无限期), 翻译为 gfx.Event 投递。
// 连接断开/关闭返回 false。
func (s *surface) WaitEvents(maxWait time.Duration) bool {
	deadline := time.Time{}
	if maxWait > 0 {
		deadline = time.Now().Add(maxWait)
	}
	for {
		var timer <-chan time.Time
		if !deadline.IsZero() {
			d := time.Until(deadline)
			if d <= 0 {
				return true // 超时: 交回事件循环 (可能有定时器到期)
			}
			timer = time.After(d)
		}
		select {
		case <-s.errch:
			s.markClosed()
			return false
		case ev := <-s.evch:
			if !s.translate(ev) {
				return false
			}
		case <-timer:
			return true
		}
	}
}

// markClosed 关闭标记 (连接已不可用)。
func (s *surface) markClosed() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
}

// translate 把 X 事件翻译为 gfx.Event; 返回 false 表示应关闭。
func (s *surface) translate(ev xgb.Event) bool {
	switch e := ev.(type) {
	case *xproto.ClientMessageEvent:
		// WM_DELETE_WINDOW → 关闭
		if e.Type == s.wmProtocols && len(e.Data.Data32) > 0 &&
			xproto.Atom(e.Data.Data32[0]) == s.wmDelete {
			s.trySend(gfx.Event{Kind: gfx.EventClose})
		}
	case *xproto.ButtonPressEvent:
		if e.Detail == 1 { // 左键
			s.trySend(gfx.Event{Kind: gfx.EventMouseDown, X: int(e.EventX), Y: int(e.EventY)})
		}
	case *xproto.ButtonReleaseEvent:
		if e.Detail == 1 {
			s.trySend(gfx.Event{Kind: gfx.EventMouseUp, X: int(e.EventX), Y: int(e.EventY)})
		}
	case *xproto.KeyPressEvent:
		if name, ok := s.keyNames[e.Detail]; ok {
			s.trySend(gfx.Event{Kind: gfx.EventKeyDown, Key: name})
		}
	case *xproto.ConfigureNotifyEvent:
		s.mu.Lock()
		resized := int(e.Width) != s.w || int(e.Height) != s.h
		s.w, s.h = int(e.Width), int(e.Height)
		s.mu.Unlock()
		if resized {
			s.trySend(gfx.Event{Kind: gfx.EventResize, W: int(e.Width), H: int(e.Height)})
		}
	}
	return true
}

// trySend 非阻塞投递事件 (通道满则丢弃)。
func (s *surface) trySend(ev gfx.Event) {
	select {
	case s.events <- ev:
	default:
	}
}
