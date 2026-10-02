//go:build darwin

// 多显示器枚举 (factory 级 displayProvider 的 cocoa 实现)。
//
// gfx 内核把"屏幕信息"抽象成 factory 上的两个方法 (displayProvider, 见
// gfx/screen.go): Displays() / DisplayOf()。此前 cocoa 只有"主屏兜底"的
// 存根实现, 本文件补全:
//
//	Displays()  —— 枚举 [NSScreen screens], 每块屏上报 frame / visibleFrame /
//	               backingScaleFactor / 是否主屏
//	DisplayOf() —— 窗口落在哪块屏上 (经 NSWindow.screen, 判定留在后端, 与
//	               win32 的 MonitorFromWindow 同一立场)
//
// 字段口径 (与 win32/display.go 对齐):
//   - W/H 是**设备像素** (点 × backingScaleFactor): gfx 的窗口尺寸是设备像素,
//     屏幕尺寸用同一口径脚本侧才不用区分平台;
//   - X/Y/WorkX/WorkY 是**左上原点**的虚拟桌面坐标: NSScreen 的全局坐标是
//     底左原点、y 向上, 这里统一换算 (以主屏顶边为基准线);
//   - Work* 来自 visibleFrame (排除菜单栏与 Dock), 同样乘 scale;
//   - ID 用 deviceDescription 里的 NSScreenNumber (CGDirectDisplayID):
//     稳定标识, 插拔/重排后仍指向同一块屏 —— 与 win32 用设备名的理由相同;
//     序号 (screens 数组下标) 会变, 不能当 ID。
//
// 注意 (上一轮的教训): 屏幕几何来自 NSScreen.frame, **不是**窗口尺寸;
// NSScreen 的几何 selector 是 frame (bounds 是 NSView/NSWindow 的), 用错
// 会 objc 异常 "unrecognized selector sent to instance"。
//
// 折叠姿态 (posture/hinge) 不在这里: macOS 没有折叠屏, 恒报 "flat"。
package cocoa

import (
	"fmt"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

var (
	selScreens       = objc.RegisterName("screens")
	selCount         = objc.RegisterName("count")
	selObjectAtIndex = objc.RegisterName("objectAtIndex:")
	selVisibleFrame  = objc.RegisterName("visibleFrame")
	selDeviceDesc    = objc.RegisterName("deviceDescription")
	selObjectForKey  = objc.RegisterName("objectForKey:")
	selIntegerValue  = objc.RegisterName("integerValue")
	selLocalizedName = objc.RegisterName("localizedName")
	selWindowScreen  = objc.RegisterName("screen") // NSWindow.screen (不是 NSView 的)
)

// Displays 枚举全部显示器 (factory 实现 displayProvider)。
func (f *factory) Displays() []gfx.Display {
	screensClass := objc.ID(objc.GetClass("NSScreen"))
	list := screensClass.Send(selScreens)
	if list == 0 {
		return nil
	}
	n := objc.Send[uint64](list, selCount)
	main := screensClass.Send(selMainScreen)

	// 换算基准: 主屏顶边在 AppKit 全局坐标里的 y (主屏 origin 是 (0,0),
	// 底左原点, 所以顶边恰为它的高度)。其它屏的 y_top = topY - (origin.y + 高)。
	var topY float64
	if main != 0 {
		fr := objc.Send[nsRect](main, selScreenFrame)
		topY = fr.Origin.Y + fr.Size.Height
	}

	out := make([]gfx.Display, 0, n)
	for i := uint64(0); i < n; i++ {
		scr := list.Send(selObjectAtIndex, uintptr(i))
		if scr == 0 {
			continue
		}
		frame := objc.Send[nsRect](scr, selScreenFrame)
		vis := objc.Send[nsRect](scr, selVisibleFrame)
		scale := objc.Send[float64](scr, selBackingScale)
		if scale <= 0 {
			scale = 1
		}
		id, name := screenIdentity(scr)
		out = append(out, gfx.Display{
			ID:   id,
			Name: name,
			X:    int(frame.Origin.X),
			Y:    int(topY - (frame.Origin.Y + frame.Size.Height)),
			W:    int(frame.Size.Width*scale + 0.5),
			H:    int(frame.Size.Height*scale + 0.5),
			// visibleFrame 排除菜单栏/Dock: 其 origin.y 就是可用区底边
			// (底左原点), 顶边 = origin.y + 高。
			WorkX:   int(vis.Origin.X),
			WorkY:   int(topY - (vis.Origin.Y + vis.Size.Height)),
			WorkW:   int(vis.Size.Width*scale + 0.5),
			WorkH:   int(vis.Size.Height*scale + 0.5),
			Scale:   scale,
			Primary: scr == main,
			Posture: "flat",
		})
	}
	if len(out) == 0 {
		// 枚举失败 (极端环境): 返回 nil, 让内核退化为"单块虚拟屏" —— 与
		// win32 的同一兜底口径, 空表会让 displayOfWorkWindow 找不到任何屏。
		return nil
	}
	if !hasPrimaryDisplay(out) {
		out[0].Primary = true
	}
	return out
}

// DisplayOf 报告窗口落在哪块显示器上 (factory 实现 displayProvider)。
//
// 经 NSWindow.screen 查窗口所在屏 (窗口已 orderOut 时为 nil, 退化为主屏 ——
// 与"窗口不在任何屏上就按主屏算"的直觉一致), 再取该屏的稳定 ID。
func (f *factory) DisplayOf(surf gfx.Surface) (string, bool) {
	sf, ok := surf.(*surface)
	if !ok || sf == nil || sf.win == 0 {
		return "", false
	}
	scr := sf.win.Send(selWindowScreen)
	if scr == 0 {
		scr = objc.ID(objc.GetClass("NSScreen")).Send(selMainScreen)
	}
	if scr == 0 {
		return "", false
	}
	id, _ := screenIdentity(scr)
	if id == "" {
		return "", false
	}
	return id, true
}

// screenIdentity 取一块屏的稳定 ID (NSScreenNumber) 与显示名。
func screenIdentity(scr objc.ID) (id, name string) {
	if v := scr.Send(selLocalizedName); v != 0 {
		name = nsToGo(v)
	}
	if desc := scr.Send(selDeviceDesc); desc != 0 {
		if num := desc.Send(selObjectForKey, nsString("NSScreenNumber")); num != 0 {
			id = fmt.Sprintf("display:%d", objc.Send[int64](num, selIntegerValue))
		}
	}
	if id == "" {
		// deviceDescription 异常缺失: 用退路保证 ID 非空 (脚本侧拿空 ID
		// 会让 findDisplay 全部落空)。
		id = "display:unknown"
	}
	return id, name
}

func hasPrimaryDisplay(list []gfx.Display) bool {
	for _, d := range list {
		if d.Primary {
			return true
		}
	}
	return false
}

// ===== 显示器变化派发 (gx/screen onDisplayChange 的 cocoa 侧) =====
//
// win32 的对应实现: WndProc 收 WM_DISPLAYCHANGE / WM_DPICHANGED →
// gfx.Post(NotifyDisplaysChanged)。cocoa 这边没有窗口消息, 等价物是
// NSNotificationCenter 的两条通知:
//   - NSApplicationDidChangeScreenParametersNotification
//     插拔屏 / 改分辨率 / 屏幕排列变化 (与 WM_DISPLAYCHANGE 对位)
//   - NSWindowDidChangeScreenNotification
//     窗口被拖到另一块屏 (win32 的 WM_DPICHANGED 场景: 跨屏后缩放变了)
//
// 线程纪律与 win32 完全同构: AppKit 的通知回调**不**就地触发脚本 —— 回调里
// 只 gfx.Post 一个任务, 由 Pump 的 DrainTasks 在正确的执行上下文里调
// NotifyDisplaysChanged → 脚本回调。onDisplayChange 此前在 macOS 上静默失效
// 的原因就是没人派发这条通知, 显示器表本身 (Displays()) 一直是好的。

var (
	screenObserverClass objc.Class

	selDefaultCenter      = objc.RegisterName("defaultCenter")
	selAddObserverSelName = objc.RegisterName("addObserver:selector:name:object:")
	selScreenChanged      = objc.RegisterName("goxScreenParamsChanged:")
)

func init() {
	var err error
	screenObserverClass, err = objc.RegisterClass("GoxGfxScreenObserver",
		objc.GetClass("NSObject"), nil, nil,
		[]objc.MethodDef{
			{Cmd: selScreenChanged, Fn: impScreenParamsChanged},
		})
	if err != nil {
		panic("cocoa: register screen observer class: " + err.Error())
	}
}

func impScreenParamsChanged(self objc.ID, cmd objc.SEL, note objc.ID) uintptr {
	// 通知回调跑在主线程 (NSNotificationCenter 同步派发), 此刻不在 VM 的
	// 执行上下文中 —— 与 win32 的 WndProc 同一立场, 只投递不执行。
	gfx.Post(func() { gfx.NotifyDisplaysChanged() })
	return 0
}

// installScreenObserver 注册显示器变化通知 (GUI 线程, 进程一次 —— 由
// ensureNSApp 的 sync.Once 保证)。观察者实例存活到进程结束: AppKit 对
// defaultCenter 里的观察者是 unretained (weak) 引用, 实例必须有人持着 ——
// 存包级变量, 不随窗口生死, 天然满足。
func installScreenObserver() {
	observer := objc.ID(screenObserverClass).Send(selNew)
	if observer == 0 {
		return
	}
	center := objc.ID(objc.GetClass("NSNotificationCenter")).Send(selDefaultCenter)
	for _, name := range []string{
		"NSApplicationDidChangeScreenParametersNotification",
		"NSWindowDidChangeScreenNotification",
	} {
		center.Send(selAddObserverSelName, observer, selScreenChanged, nsString(name), objc.ID(0))
	}
}
