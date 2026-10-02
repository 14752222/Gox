//go:build linux

package x11

import (
	"encoding/binary"

	"github.com/14752222/Gox/gfx"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// 窗口管理 (§四 窗口/系统缺口) 的 X11 实现: 位置 / 层级 / 尺寸约束 /
// 缩放开关 / 全屏 / 激活 / 读几何。
//
// ## 为什么这里几乎没有"自己画"的成分
//
// X11 是**客户端-窗口管理器**分工的协议: 窗口位置、层叠顺序、全屏这些
// 全都不是窗口自己说了算, 而是"客户端申报意向 + WM 执行"。所以这里的每条
// 实现都是发一条请求或设一个属性:
//
//	位置      ConfigureWindow(x, y)             —— 这个 WM 一定照办
//	层级      _NET_WM_STATE ABOVE/BELOW         —— EWMH, 现代 WM 都认
//	全屏      _NET_WM_STATE FULLSCREEN          —— 同上
//	激活      _NET_ACTIVE_WINDOW                —— 同上
//	尺寸约束  WM_NORMAL_HINTS 的 PMinSize/PMaxSize —— 核心协议, 无需 EWMH
//
// WM 不认 EWMH 时那几条**静默无效** —— 这是 X11 的现实, 与 gfx 的
// "后端不支持就降级"是同一件事, 不额外造错误通道。
//
// ## 状态
//
// 与 x11.go 的其余部分一致: **代码已实现但未经 Linux 实机验证**
// (见 agent_doc/gui-guide.md §2 的平台矩阵)。可编译、协议用法确定,
// 但观感与 WM 差异需要实机确认。
//
// 光标形状 (cursorHost) **本后端不实现**: Xlib 的 XCreateFontCursor 是
// 便捷层, 纯协议要做"打开 cursor 字体 + CreateGlyphCursor + 查字形码"三步,
// 而字形码表 (XC_xxx) 的取值必须实机核对, 不能凭记忆写常量 —— 宁可不做,
// 也不留一份"看起来能用、其实指错字形"的映射。桌面端的形状支持见
// win32 (已实现) 与 cocoa (已实现)。

// EWMH 属性名 (intern 一次即缓存)。
var (
	atomNetWmState      xproto.Atom
	atomNetWmStateAbove xproto.Atom
	atomNetWmStateBelow xproto.Atom
	atomNetWmStateFull  xproto.Atom
	atomNetActiveWindow xproto.Atom
	atomNetWmStateMaxH  xproto.Atom
	atomNetWmStateMaxV  xproto.Atom
	ewmhOnce            bool
)

// ewmhInit 惰性 intern EWMH 原子 (只做一次)。
func (s *surface) ewmhInit() {
	if ewmhOnce {
		return
	}
	ewmhOnce = true
	atomNetWmState = internAtom(s.conn, "_NET_WM_STATE")
	atomNetWmStateAbove = internAtom(s.conn, "_NET_WM_STATE_ABOVE")
	atomNetWmStateBelow = internAtom(s.conn, "_NET_WM_STATE_BELOW")
	atomNetWmStateFull = internAtom(s.conn, "_NET_WM_STATE_FULLSCREEN")
	atomNetActiveWindow = internAtom(s.conn, "_NET_ACTIVE_WINDOW")
	atomNetWmStateMaxH = internAtom(s.conn, "_NET_WM_STATE_MAXIMIZED_HORZ")
	atomNetWmStateMaxV = internAtom(s.conn, "_NET_WM_STATE_MAXIMIZED_VERT")
}

// ewmhSendState 发一条 _NET_WM_STATE 客户端消息。
//
// 格式是 EWMH 规定的: data[0] = 0(删除)/1(添加)/2(切换),
// data[1]/data[2] = 两个属性原子, data[3] = 1 (来源: 普通应用)。
func (s *surface) ewmhSendState(action uint32, a, b xproto.Atom) {
	if atomNetWmState == 0 {
		return
	}
	root := xproto.Setup(s.conn).DefaultScreen(s.conn).Root
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: s.win,
		Type:   atomNetWmState,
		// **必须走 Data32New 构造器**: 联合体的 Bytes() 只序列化 Data8,
		// 直接填 Data32 字段会写出 20 个零字节 (消息发出去但内容全空,
		// 表现为"全屏/置顶请求石沉大海")。这是 xgb 生成代码的一个坑。
		Data: xproto.ClientMessageDataUnionData32New([]uint32{action, uint32(a), uint32(b), 1, 0}),
	}
	// eventMask 用 SubstructureRedirect|SubstructureNotify: WM 通常只监听
	// 这两条中的一条, 两条都给才不会漏。
	mask := uint32(xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify)
	xproto.SendEvent(s.conn, false, root, mask, string(ev.Bytes()))
}

// ewmhStateOn 查询某个 _NET_WM_STATE 原子当前是否在窗口的状态列表里。
//
// 用它而不是自己记"现在是不是全屏": 用户按 F11 / 双击标题栏 / WM 快捷键
// 改变的状态, 客户端记的账立刻就错了, 而"我是不是全屏"会影响
// setFullscreen(false) 该不该发请求。读属性是唯一可靠的来源。
func (s *surface) ewmhStateOn(atom xproto.Atom) bool {
	if atomNetWmState == 0 || atom == 0 {
		return false
	}
	rep, err := xproto.GetProperty(s.conn, false, s.win, atomNetWmState,
		xproto.AtomAtom, 0, 1024).Reply()
	if err != nil || rep == nil {
		return false
	}
	for i := 0; i+4 <= len(rep.Value); i += 4 {
		if xproto.Atom(binary.LittleEndian.Uint32(rep.Value[i:])) == atom {
			return true
		}
	}
	return false
}

// MoveTo 把窗口左上角移到 (x, y) —— 根窗口坐标系, 设备像素 (与 win32 的
// SetWindowPos / cocoa 的 setFrameOrigin: 同口径: **外框**左上角)。
//
// 坐标可以是负数 (多屏拼接) —— 所以用 int16 传 (ConfigureWindow 的 value
// list 是 32 位有符号, 但窗口坐标在协议里按 int16 解释; 传 int32 补码的低
// 16 位就是它要的值)。
//
// **未实机验证的一处落差**: 无 WM (或非 reparenting WM) 时这里的窗口就是
// 外框, 位置与 Bounds() 严格互逆; 但在 reparenting WM (GNOME/KDE 等绝大多数)
// 下面, 本窗口是被 WM 装进一个 frame 窗口里的**客户区**, 于是 "MoveTo 设的
// 位置" 与 "TranslateCoordinates 读回的位置" 会差一圈窗口装饰 (标题栏/边框)。
// 真要完全对齐, 需要读 _NET_FRAME_EXTENTS 把装饰宽度补上 —— 但那组属性
// 由 WM 决定是否提供, 补法必须实机核对。这里先按"协议原义"实现 (客户端
// 窗口坐标), 与文件头"未经 Linux 实机验证"的声明一致。
func (s *surface) MoveTo(x, y int) {
	if s == nil || s.win == 0 {
		return
	}
	xproto.ConfigureWindow(s.conn, s.win,
		uint16(xproto.ConfigWindowX|xproto.ConfigWindowY),
		[]uint32{uint32(int32(x)), uint32(int32(y))})
	s.conn.Sync()
}

// SetLevel 设层级: top → _NET_WM_STATE_ABOVE, bottom → _NET_WM_STATE_BELOW,
// normal → 两个都删。
func (s *surface) SetLevel(level string) {
	if s == nil || s.win == 0 {
		return
	}
	s.ewmhInit()
	switch level {
	case "top":
		s.ewmhSendState(1, atomNetWmStateAbove, 0)
		s.ewmhSendState(0, atomNetWmStateBelow, 0)
	case "bottom":
		s.ewmhSendState(1, atomNetWmStateBelow, 0)
		s.ewmhSendState(0, atomNetWmStateAbove, 0)
	default:
		s.ewmhSendState(0, atomNetWmStateAbove, atomNetWmStateBelow)
	}
	s.conn.Sync()
}

// SetSizeConstraints 设尺寸约束 (WM_NORMAL_HINTS 的 PMinSize / PMaxSize)。
//
// 手写 XSizeHints 的原因: xgb 只生成请求级 API, 没有 Xlib 的
// XSetWMNormalHints 便捷函数。结构是 18 个 CARD32 (X 协议里 long = 32 位,
// 与宿主架构无关 —— 这点最容易写错, 64 位机器上会想当然地按 8 字节排)。
func (s *surface) SetSizeConstraints(minW, minH, maxW, maxH int) {
	if s == nil || s.win == 0 {
		return
	}
	// flags: PMinSize / PMaxSize (各自只有给了才置位)
	const (
		pMinSize = 1 << 4
		pMaxSize = 1 << 5
	)
	var flags uint32
	if minW > 0 || minH > 0 {
		flags |= pMinSize
	}
	if maxW > 0 || maxH > 0 {
		flags |= pMaxSize
	}
	hints := make([]uint32, 18)
	hints[0] = flags
	// 顺序: x, y, width, height, min_width, min_height, max_width,
	// max_height, width_inc, height_inc, min_aspect, max_aspect, ...
	hints[5] = uint32(clampPos(minW))
	hints[6] = uint32(clampPos(minH))
	hints[7] = uint32(clampPos(maxW))
	hints[8] = uint32(clampPos(maxH))

	buf := make([]byte, 0, 18*4)
	for _, v := range hints {
		var b [4]byte
		binary.LittleEndian.PutUint32(b[:], v)
		buf = append(buf, b[:]...)
	}
	xproto.ChangeProperty(s.conn, xproto.PropModeReplace, s.win,
		xproto.AtomWmNormalHints, xproto.AtomInteger, 32, 18, buf)
	s.conn.Sync()
}

// clampPos 把约束值转成非负的协议值 (负数在上层已归零, 这里兜底)。
func clampPos(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

// SetResizable 开关缩放。
//
// X11 没有"能不能缩放"这个开关 (那由 WM 的装饰与 Alt+拖拽决定), 纯协议的
// 等效做法是**把 min 与 max 都钉到当前尺寸** —— WM 看到 min == max 就
// 不再允许改变大小。恢复可缩放时清掉 PMinSize/PMaxSize。
//
// 已知取舍: 它与 SetSizeConstraints 共用同一份 WM_NORMAL_HINTS, 所以
// 先 setConstraints 再 setResizable(false) 会把约束覆盖成"固定尺寸"。
// 反过来 setResizable(true) 会清掉约束 —— 这是协议本身的表达力限制
// (一份 hints 里只有一组 min/max), 不是实现偷懒; win32 上两者是分开的,
// 因为那边是两套机制 (MINMAXINFO / 窗口样式)。
func (s *surface) SetResizable(on bool) {
	if s == nil || s.win == 0 {
		return
	}
	s.mu.Lock()
	w, h := s.w, s.h
	s.mu.Unlock()
	if on {
		s.SetSizeConstraints(0, 0, 0, 0)
		return
	}
	s.SetSizeConstraints(w, h, w, h)
}

// SetFullscreen 进出全屏 (_NET_WM_STATE_FULLSCREEN)。
//
// 先查当前状态再决定发不发: 用户可能已经用 WM 的方式进了全屏, 那时再发一次
// "添加"是幂等的 (没事), 但"退出"必须真的能退 —— 所以用属性表而不是本地
// 记账来判断 (见 ewmhStateOn)。
func (s *surface) SetFullscreen(on bool) {
	if s == nil || s.win == 0 {
		return
	}
	s.ewmhInit()
	cur := s.ewmhStateOn(atomNetWmStateFull)
	if on == cur {
		return
	}
	action := uint32(1) // 添加
	if !on {
		action = 0 // 删除
	}
	s.ewmhSendState(action, atomNetWmStateFull, 0)
	s.conn.Sync()
}

// Activate 请求 WM 把窗口激活 (_NET_ACTIVE_WINDOW)。
func (s *surface) Activate() {
	if s == nil || s.win == 0 {
		return
	}
	s.ewmhInit()
	if atomNetActiveWindow == 0 {
		return
	}
	root := xproto.Setup(s.conn).DefaultScreen(s.conn).Root
	ev := xproto.ClientMessageEvent{
		Format: 32,
		Window: s.win,
		Type:   atomNetActiveWindow,
		// data[0]=1 (来源: 普通应用), data[1]=时间戳 (0 = 用当前时间)
		Data: xproto.ClientMessageDataUnionData32New([]uint32{1, 0, 0, 0, 0}),
	}
	mask := uint32(xproto.EventMaskSubstructureRedirect | xproto.EventMaskSubstructureNotify)
	xproto.SendEvent(s.conn, false, root, mask, string(ev.Bytes()))
	s.conn.Sync()
}

// Bounds 读窗口在根坐标系里的位置与尺寸 (gfx 的 boundsProvider)。
//
// 两步: GetGeometry 拿尺寸 (X/Y 是**相对父窗口**的, 有 WM 重定父时不可用),
// TranslateCoordinates 把窗口原点换算到根坐标。xgb 的回复由独立的读协程
// 按序号分发, 所以这里从 VM 线程做两次往返是安全的 (不会与事件读打架)。
func (s *surface) Bounds() (int, int, int, int) {
	if s == nil || s.win == 0 {
		return 0, 0, 0, 0
	}
	root := xproto.Setup(s.conn).DefaultScreen(s.conn).Root
	rep, err := xproto.TranslateCoordinates(s.conn, s.win, root, 0, 0).Reply()
	if err != nil || rep == nil {
		return 0, 0, 0, 0
	}
	s.mu.Lock()
	w, h := s.w, s.h
	s.mu.Unlock()
	return int(rep.DstX), int(rep.DstY), w, h
}

// 编译期断言: *surface 必须满足 windowManager + boundsProvider。
// (刻意**不**断言 cursorHost —— 本后端不实现光标形状, 见文件头。)
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
)

// 让 xgb 的导入在只编译本文件时也成立 (internAtom / Surface 都在 x11.go)。
var _ = xgb.Get32
