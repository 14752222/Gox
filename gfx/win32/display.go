//go:build windows

package win32

import (
	"syscall"
	"unsafe"

	"github.com/14752222/Gox/gfx"
)

// ===== 多显示器 (gfx/displayProvider 的 win32 实现, 2026-09-21) =====
//
// gfx 内核把"屏幕信息"抽象成两个可选接口方法 (displayProvider):
//
//	Displays()            —— 现在有哪几块屏, 各自的几何与缩放
//	DisplayOf(surface)    —— 某个窗口落在哪块屏上
//
// 为什么判定"窗口在哪块屏"必须由后端做: 只有这里知道 hwnd。用窗口坐标自己算
// 会在两个场景直接错 —— 横跨两块屏的窗口 (坐标算出来是"面积重叠最多的那块",
// 而 Windows 的官方语义是 MonitorFromWindow 的**最近/主屏**), 以及负坐标副屏
// (Left/Top 为负时各种手写比较都容易翻车)。所以判据留在平台层, 这里是唯一
// 实现点。
//
// 姿态 (posture / hinge) 不在这里: Windows 没有折叠屏姿态查询 API (WinRT 的
// Windows.Devices.Sensors 不在纯 syscall 的可达范围内), 而且**假的比没有更
// 危险** —— 猜一个"半折"出来会让界面在没有折痕的屏上分栏。所以本文件只给
// 几何 + 缩放, 姿态由宿主/模拟器经 gx/screen 的 reportPosture 上报。

var (
	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procMonitorFromWindow   = user32.NewProc("MonitorFromWindow")
	procGetDeviceCaps       = gdi32.NewProc("GetDeviceCaps")

	// shcore 的 GetDpiForMonitor (Win8.1+)。单独一个 DLL 而不是并进 user32:
	// 老系统上这个库不一定在, Find() 失败就退回 DC 口径的系统 DPI。
	shcore               = syscall.NewLazyDLL("shcore.dll")
	procGetDpiForMonitor = shcore.NewProc("GetDpiForMonitor")
)

// MonitorFromWindow 的 flags。
const (
	monitorDefaultToNearest = 2
)

// MDT_EFFECTIVE_DPI: 取"当前生效"的 DPI (随用户缩放设置变化)。
const mdtEffectiveDPI = 0

// MONITORINFOF_PRIMARY: 该显示器是主显示器。
const monitorInfoPrimary = 0x1

// monitorInfoExW 对应 MONITORINFOEXW (cbSize 必须等于 unsafe.Sizeof 自身)。
type monitorInfoExW struct {
	CbSize    uint32
	RcMonitor rect32
	RcWork    rect32
	DwFlags   uint32
	SzDevice  [32]uint16
}

// deviceName 取 szDevice (形如 `\\.\DISPLAY1`), 用作显示器 ID。
//
// 用它而不是"第 N 块屏"这种序号: 序号会随插拔变化, 而设备名稳定 —— 脚本里
// `screenOf(win).id` 记住的 ID 在热插拔之后仍然指向同一块屏。
func (mi *monitorInfoExW) deviceName() string {
	n := 0
	for n < len(mi.SzDevice) && mi.SzDevice[n] != 0 {
		n++
	}
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(mi.SzDevice[:n])
}

// Displays 枚举全部显示器 (工厂实现 displayProvider)。
func (f *factory) Displays() []gfx.Display {
	var out []gfx.Display
	cb := syscall.NewCallback(func(hmon, _ uintptr, _ uintptr, _ uintptr) uintptr {
		mi := monitorInfoExW{CbSize: uint32(unsafe.Sizeof(monitorInfoExW{}))}
		r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi)))
		if r == 0 {
			return 1 // 继续枚举
		}
		id := mi.deviceName()
		if id == "" {
			id = "monitor:" + itoa(len(out))
		}
		out = append(out, gfx.Display{
			ID:      id,
			Name:    id,
			X:       int(mi.RcMonitor.Left),
			Y:       int(mi.RcMonitor.Top),
			W:       int(mi.RcMonitor.Right - mi.RcMonitor.Left),
			H:       int(mi.RcMonitor.Bottom - mi.RcMonitor.Top),
			WorkX:   int(mi.RcWork.Left),
			WorkY:   int(mi.RcWork.Top),
			WorkW:   int(mi.RcWork.Right - mi.RcWork.Left),
			WorkH:   int(mi.RcWork.Bottom - mi.RcWork.Top),
			Scale:   monitorScale(hmon),
			Primary: mi.DwFlags&monitorInfoPrimary != 0,
			// 姿态恒 flat: Windows 没有折叠屏姿态 API (见文件头的说明)。
			Posture: "flat",
		})
		return 1
	})
	procEnumDisplayMonitors.Call(0, 0, cb, 0)
	if len(out) == 0 {
		// 枚举失败 (极端环境): 返回 nil, 让内核退化为"单块虚拟屏"而不是给一张空表
		// —— 空表会让 displayOfWorkWindow 找不到任何显示器。
		return nil
	}
	if !hasPrimary(out) {
		out[0].Primary = true
	}
	return out
}

// DisplayOf 报告某个窗口落在哪块显示器上 (工厂实现 displayProvider)。
func (f *factory) DisplayOf(s gfx.Surface) (string, bool) {
	sf, ok := s.(*surface)
	if !ok || sf == nil || sf.hwnd == 0 {
		return "", false
	}
	hmon, _, _ := procMonitorFromWindow.Call(uintptr(sf.hwnd), monitorDefaultToNearest)
	if hmon == 0 {
		return "", false
	}
	mi := monitorInfoExW{CbSize: uint32(unsafe.Sizeof(monitorInfoExW{}))}
	if r, _, _ := procGetMonitorInfoW.Call(hmon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return "", false
	}
	if id := mi.deviceName(); id != "" {
		return id, true
	}
	return "", false
}

// monitorScale 求一块屏的设备像素比 (1.0 / 1.25 / 1.5 / 2.0 …)。
//
// 两级回退, 顺序有意: GetDpiForMonitor 是**按显示器**的 (4K 副屏 + 1080p 主屏
// 的混合配置下唯一正确的口径), DC 口径只能给系统级 DPI —— 聊胜于无, 但比
// 恒返回 1.0 诚实 (1.0 会让 150% 缩放的高分屏上字号小到看不清, 而且不报错)。
func monitorScale(hmon uintptr) float64 {
	if err := procGetDpiForMonitor.Find(); err == nil {
		var x, y uint32
		if r, _, _ := procGetDpiForMonitor.Call(hmon, mdtEffectiveDPI,
			uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); r == 0 && x > 0 {
			return float64(x) / 96.0
		}
	}
	// 回退: 屏幕 DC 的 LOGPIXELSX (96 = 100%)
	hdc, _, _ := procGetDC.Call(0)
	if hdc == 0 {
		return 1
	}
	defer procReleaseDC.Call(0, hdc)
	const logPixelsX = 88
	dpi, _, _ := procGetDeviceCaps.Call(hdc, logPixelsX)
	if dpi == 0 {
		return 1
	}
	return float64(dpi) / 96.0
}

func hasPrimary(list []gfx.Display) bool {
	for _, d := range list {
		if d.Primary {
			return true
		}
	}
	return false
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
