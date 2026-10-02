//go:build linux

// 多显示器枚举 (factory 级 displayProvider 的 x11 实现, M4 2026-10-02)。
//
// gfx 内核把"屏幕信息"抽象成 factory 上的两个方法 (displayProvider, 见
// gfx/screen.go): Displays() / DisplayOf()。本文件补上 x11 的实现 —— 此前
// Linux 没有任何 displayProvider, 于是 gx/screen 永远只看到一块"虚拟屏",
// 多屏/换屏事件在 X11 上整体不可用。
//
// ## 优先 XRandR, 退化单屏
//
// 现代 X11 的多屏靠 RandR 扩展:
//
//	randr.GetMonitors(root, true)  —— 一次拿到所有"显示器"的几何与主屏标记
//	                                 (RandR 1.5 的 monitor 概念, 比逐个 output
//	                                 拼更贴近用户眼里的"一块屏")
//
// RandR 拿不到 (老服务器 / 没有扩展) 时退化为 `xproto.Setup` 的**单块逻辑屏**
// (screen.WidthInPixels × HeightInPixels), 保证 Displays() 永远非空。
//
// ## 与 win32 / cocoa 的口径对齐
//
//   - X/Y 是虚拟桌面的左上原点坐标。X11 里根窗口覆盖整张虚拟桌面, monitor 的
//     x/y 就是它相对根的坐标 —— 天然就是左上原点。
//   - W/H 是像素 (X11 没有 Retina 那种"点 × scale", 恒 scale=1)。**注意**:
//     高 DPI 的 X11 会话里窗口尺寸与屏幕尺寸都用物理像素, 缩放是应用自己按
//     DPI 做的 —— 所以 dp 换算在 X11 上通常是 1:1。
//   - 工作区 = 显示器区域 (X11 没有向客户端暴露"排除面板后的可用区"的标准途径;
//     _NET_WORKAREA 是 EWMH 约定, 需要读根窗口属性, 且只在合规 WM 上存在 ——
//     v1 不做, 见文末 TODO)。
//
// ## TODO (v1 边界)
//
//   - _NET_WORKAREA: 拿任务栏/Dock 让出的可用区 (目前 WorkX/Y/W/H = monitor 的)。
//   - 按 output/CRTC 而非 monitor 枚举: 某些驱动不报 RandR 1.5 monitors。
//   - displayScale: X11 无标准 API, 恒 1.0。
package x11

import (
	"sync"

	"github.com/14752222/Gox/gfx"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
)

// ===== 查询连接 (只为枚举显示器, 与窗口的 surface 连接分开) =====

var (
	queryOnce sync.Once
	queryConn *xgb.Conn
	queryRoot xproto.Window
	queryW    uint16
	queryH    uint16
	queryOK   bool
)

// ensureQueryConn 惰性建立一条"只用于查询显示器"的连接。
//
// 为什么不复用某个窗口的 surface.conn: 枚举显示器在**还没有任何窗口**时就会被
// 调用 (gx/screen 的 screens() 可以在 render 之前跑)。开一条独立连接是 X11 里
// 查询用连接的标准做法, 代价可忽略 (只在首次调用时建立)。
func ensureQueryConn() (*xgb.Conn, xproto.Window, uint16, uint16, bool) {
	queryOnce.Do(func() {
		conn, err := xgb.NewConn()
		if err != nil {
			return
		}
		setup := xproto.Setup(conn)
		scr := setup.DefaultScreen(conn)
		if scr == nil {
			return
		}
		queryConn, queryRoot = conn, scr.Root
		queryW, queryH = scr.WidthInPixels, scr.HeightInPixels
		queryOK = true
	})
	return queryConn, queryRoot, queryW, queryH, queryOK
}

// randrReady 记录某条连接是否已 init 过 RANDR (randr.Init 每次都会发一次
// QueryExtension 往返, 不该在每次 DisplayOf 里重复)。
var randrReady sync.Map // *xgb.Conn → bool

func ensureRandr(conn *xgb.Conn) bool {
	if v, ok := randrReady.Load(conn); ok {
		return v.(bool)
	}
	ok := randr.Init(conn) == nil
	randrReady.Store(conn, ok)
	return ok
}

// Displays 枚举全部显示器 (factory 实现 gfx 的 displayProvider)。
func (f *factory) Displays() []gfx.Display {
	conn, root, fw, fh, ok := ensureQueryConn()
	if !ok {
		return nil // 没有 X 连接 (DISPLAY 未设 / 无 X server): 交给内核虚拟屏兜底
	}
	if list := randrDisplays(conn, root); len(list) > 0 {
		return list
	}
	// 退化: 单块逻辑屏 (RandR 不可用)。ID 用 "x11:0" —— 稳定且不与 monitor
	// 名冲突, 插拔后仍指向这块根屏。
	w, h := int(fw), int(fh)
	if w <= 0 || h <= 0 {
		w, h = 1024, 768
	}
	return []gfx.Display{{
		ID: "x11:0", Name: "screen0",
		W: w, H: h, WorkW: w, WorkH: h,
		Scale: 1, Primary: true, Posture: "flat",
	}}
}

// randrDisplays 用 RandR 1.5 的 monitors 枚举 (拿不到 → nil)。
func randrDisplays(conn *xgb.Conn, root xproto.Window) []gfx.Display {
	if !ensureRandr(conn) {
		return nil
	}
	qv, err := randr.QueryVersion(conn, 1, 5).Reply()
	if err != nil || qv == nil || qv.MajorVersion < 1 ||
		(qv.MajorVersion == 1 && qv.MinorVersion < 5) {
		return nil
	}
	reply, err := randr.GetMonitors(conn, root, true).Reply()
	if err != nil || reply == nil || len(reply.Monitors) == 0 {
		return nil
	}
	out := make([]gfx.Display, 0, len(reply.Monitors))
	for i, m := range reply.Monitors {
		id := atomName(conn, m.Name)
		if id == "" {
			id = "x11-monitor:" + itoaX(i)
		}
		w, h := int(m.Width), int(m.Height)
		out = append(out, gfx.Display{
			ID: id, Name: id,
			X: int(m.X), Y: int(m.Y),
			W: w, H: h,
			// 工作区 = 显示器区域 (见文件头 TODO: 未读 _NET_WORKAREA)。
			WorkX: int(m.X), WorkY: int(m.Y), WorkW: w, WorkH: h,
			Scale: 1, Primary: m.Primary, Posture: "flat",
		})
	}
	if !hasPrimaryDisplay(out) && len(out) > 0 {
		out[0].Primary = true
	}
	return out
}

// DisplayOf 报告窗口落在哪块显示器上 (factory 实现 displayProvider)。
//
// 用窗口**中心点**落在哪个 monitor 矩形里判定 —— 与"面积最大重叠"相比, 中心点
// 与用户直觉更一致 (一块屏只盖住窗口一角时, 用户仍认为窗口在中心那块屏上),
// 且不需要把整张 monitor 列表与窗口矩形做面积交叠的 O(n) 计算。
func (f *factory) DisplayOf(surf gfx.Surface) (string, bool) {
	sf, ok := surf.(*surface)
	if !ok || sf == nil || sf.win == 0 || sf.conn == nil {
		return "", false
	}
	// 窗口中心在根坐标系里的位置 (TranslateCoordinates 跨过 WM 的 frame)。
	tr, err := xproto.TranslateCoordinates(sf.conn, sf.win, sf.root, 0, 0).Reply()
	if err != nil || tr == nil {
		return "", false
	}
	cx := int(tr.DstX) + sf.w/2
	cy := int(tr.DstY) + sf.h/2

	if list := randrDisplays(sf.conn, sf.root); len(list) > 0 {
		for _, d := range list {
			if cx >= d.X && cx < d.X+d.W && cy >= d.Y && cy < d.Y+d.H {
				return d.ID, true
			}
		}
		// 中心落在所有 monitor 之外 (窗口被拖到拼接空隙 / 负坐标): 给主屏,
		// 与 win32 MonitorFromWindow 的 MONITOR_DEFAULTTOPRIMARY 口径一致。
		return list[0].ID, true
	}
	// 退化: 单屏 (RandR 不可用) —— 窗口只要在根上就是它。
	return "x11:0", true
}

// atomName 取原子对应的名字 (失败 → 空串)。
func atomName(conn *xgb.Conn, atom xproto.Atom) string {
	if atom == 0 {
		return ""
	}
	reply, err := xproto.GetAtomName(conn, atom).Reply()
	if err != nil || reply == nil {
		return ""
	}
	return reply.Name
}

func hasPrimaryDisplay(list []gfx.Display) bool {
	for _, d := range list {
		if d.Primary {
			return true
		}
	}
	return false
}

// itoaX 是一个最小整数→字符串 (避免为了一个序号 import strconv;
// 与 win32/display.go 的 itoa 同款)。
func itoaX(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
