package gfx

import "strconv"

// ===== 窗口几何 / 移动 (M4 多窗口·多屏, 2026-10-02) =====
//
// ## 坐标口径 (唯一一处定义, 后端与脚本都以此为准)
//
//	windowInfo().x / .y, win.position(), win.bounds().x/.y
//	    = 窗口外框左上角 相对 **其所在显示器工作区左上角** 的偏移 (设备像素)
//
// 为什么用"工作区相对"而不是"虚拟桌面绝对": 脚本关心的是"相对这块屏我还要
// 挪多少" (居中、贴边、留边距), 而不是"我在 3840 拼屏里排第几"。绝对坐标随
// 屏的排列/插拔而变, 工作区相对量不会 —— 同一段"居中"代码在任何多屏拼接下
// 都成立。
//
// ## 位置单位: 脚本侧恒为设备像素, 后端侧是"后端位置单位"
//
// 脚本侧读到的 x/y 一律换算成**设备像素**; 但各平台的原生窗口坐标单位不同:
//
//	win32 / x11  虚拟桌面坐标 = 设备像素      (Display.PosInPoints = false)
//	cocoa        AppKit 全局坐标 = 点 (左下原点, Display.PosInPoints = true)
//
// Display.PosInPoints 标出这个差别, posScale() 把"设备像素 ↔ 后端位置单位"
// 互转 (cocoa 上除以 backing scale)。**这是本模块最容易静默出错的一步**:
// 少了换算, Retina(scale=2) 上 moveTo/position 会差一倍, 窗口挪到错误位置而
// 不报任何错。后端承接的是**绝对坐标** (见下), 但单位随 PosInPoints 走。
//
// 后端层 (win32 / x11 / cocoa) 承接的则是**绝对坐标** (见下), 因为
// SetWindowPos / ConfigureWindow / setFrameOrigin 要的就是绝对坐标。换算
// (工作区 + 相对量 + 单位) 只发生在 gfx 这一层, 平台层不做二次解释。
//
// ## 可选能力接口 (与 windowController / displayProvider 同款)
//
// 不扩 Surface 接口: 没实现的后端静默降级 (读不到位置就返回 0, 移动就 no-op)。
// 三个接口刻意分开:
//
//	windowMover         写: MoveTo(绝对坐标)
//	windowBoundsProvider 读: WindowBounds()
//	windowActiveProvider 读: IsActive()
//
// **为什么 MoveTo 不并进 windowController**: windowController 的既有方法
// (SetTitle / ResizeClient) 已经有多处实现与测试替身 (gfx/helpers_test.go 的
// fakeSurface), 往里加方法会让"没有 MoveTo 的替身"连 SetTitle/Resize 一起
// 断言落空 —— 静默降级的面被不必要地放大。分开成独立的可选接口既守住
// "缺能力就降级"的纪律, 又不动既有实现。
//
// ## 线程纪律
//
// MoveTo 必须经 gfx.Post 排到 GUI 线程 (与 close() 完全同款): 它最终调
// 平台 API (SetWindowPos 等), 必须在拥有窗口的线程上做; 而且解析"当前在哪块
// 屏"要调后端 DisplayOf (会读 NSWindow / hwnd), 也不能跨线程。所以
// Window.MoveTo 在任意 goroutine 上都是安全的 —— 它只做一次 Post。

// windowMover 是 Surface 的**可选能力**: 把窗口外框左上角移到给定的
// **虚拟桌面绝对坐标** (坐标口径与 displayProvider 的 Display.X/Y 一致:
// win32/x11 是设备像素, cocoa 是点)。
type windowMover interface {
	MoveTo(x, y int) error
}

// windowBoundsProvider 是 Surface 的**可选能力**: 读窗口外框左上角的
// **绝对坐标**与客户区尺寸。返回的 w/h 是客户区 (与 Surface.Size 同口径),
// 免得"外框宽"与"内容宽"两个尺寸口径在多处打架。
type windowBoundsProvider interface {
	WindowBounds() (x, y, w, h int, ok bool)
}

// windowActiveProvider 是 Surface 的**可选能力**: 窗口此刻是否拥有系统焦点
// (前台窗口)。用于 gx/screen 的 windows()[].focused —— 拿不到的平台上
// 退化为"是否最近挂载" (见 windowInfoToJS 的口径说明)。
type windowActiveProvider interface {
	IsActive() bool
}

// posScale 返回"后端位置单位 → 设备像素"的换算系数。
//
// 位置单位本来就是设备像素的后端 (win32/x11) 返回 1; AppKit (cocoa) 的位置
// 是点, 返回 backing scale —— 见 Display.PosInPoints 的说明。
func posScale(d Display) float64 {
	if d.PosInPoints && d.Scale > 1 {
		return d.Scale
	}
	return 1
}

// toDevicePx 把后端位置单位的值换算成设备像素 (四舍五入)。
func toDevicePx(v int, d Display) int {
	ps := posScale(d)
	if ps == 1 {
		return v
	}
	return roundHalf(float64(v) * ps)
}

// fromDevicePx 把设备像素换算成后端位置单位 (四舍五入)。toDevicePx 的逆运算。
func fromDevicePx(v int, d Display) int {
	ps := posScale(d)
	if ps == 1 {
		return v
	}
	return roundHalf(float64(v) / ps)
}

// roundHalf 对正负都做四舍五入 (int() 是向零截断, 直接 +0.5 在负数上会错).
func roundHalf(f float64) int {
	if f < 0 {
		return int(f - 0.5)
	}
	return int(f + 0.5)
}

// windowPosSpecified 报告 WindowConfig 是否显式指定了位置。
//
// 规则 (与 WindowConfig.X/Y 的注释一致): X/Y 任一 < -10000 (DefaultWindowPos
// 一族) 视为未指定; 全零 (Go 零值 / render 缺省) 也视为未指定。
func windowPosSpecified(cfg WindowConfig) bool {
	if cfg.X < -10000 || cfg.Y < -10000 {
		return false
	}
	return cfg.X != 0 || cfg.Y != 0
}

// ResolveWindowPlacement 把配置里的 X/Y/Display 解析成**虚拟桌面绝对坐标**
// (供后端 Create 使用)。ok=false 表示"未指定位置, 用平台默认"。
//
// 返回的坐标单位是**目标显示器的位置单位** (Display.PosInPoints): win32/x11
// 是设备像素, cocoa 是点 —— 后端拿到的就是它能直接喂给平台 API 的形式。
//
// 三种情形:
//   - Display 空 + X/Y 未指定 → (0,0,false): 平台自选 (Windows 级联 / cocoa 居中)
//   - Display 给定 + X/Y 未指定 → 该屏工作区**居中**
//   - X/Y 指定 (Display 空则用主屏) → 该屏工作区 + (X,Y)   (X/Y 是设备像素)
//
// Display 的解析: 先按 id 精确匹配; 失败再试"数字当序号" (render 里写
// display={1} 这种) —— 两种写法都常见, 都收。都失败则退回主屏 (不报错:
// 显示器拔掉了不该让窗口开不出来)。
func ResolveWindowPlacement(cfg WindowConfig) (x, y int, ok bool) {
	if cfg.Display == "" && !windowPosSpecified(cfg) {
		return 0, 0, false
	}
	d := placementDisplay(cfg.Display)
	if !windowPosSpecified(cfg) {
		// 按屏居中: 用配置尺寸 (拿不到就用 render 缺省 400x300, 与后端一致)。
		w, h := cfg.Width, cfg.Height
		if w <= 0 {
			w = 400
		}
		if h <= 0 {
			h = 300
		}
		cw, ch := d.WorkW, d.WorkH
		if cw <= 0 {
			cw = d.W
		}
		if ch <= 0 {
			ch = d.H
		}
		// 居中偏移在设备像素里算 (WorkW 与 cfg.Width 同口径), 再换成后端位置单位。
		return d.WorkX + fromDevicePx(maxInt(0, (cw-w)/2), d),
			d.WorkY + fromDevicePx(maxInt(0, (ch-h)/2), d), true
	}
	// X/Y 是设备像素; WorkX/WorkY 是后端位置单位。
	return d.WorkX + fromDevicePx(cfg.X, d), d.WorkY + fromDevicePx(cfg.Y, d), true
}

// placementDisplay 解析目标显示器: id → 数字序号 → 主屏。
func placementDisplay(id string) Display {
	if id == "" {
		return mustPrimary()
	}
	if d, ok := findDisplay(id); ok {
		return d
	}
	if n, err := strconv.Atoi(id); err == nil {
		list := allDisplays()
		if n >= 0 && n < len(list) {
			return list[n]
		}
	}
	return mustPrimary()
}

// ===== 读取窗口几何 =====

// windowAbsoluteBounds 取窗口的绝对位置与客户区尺寸 (后端不支持 → ok=false)。
func windowAbsoluteBounds(w *Window) (x, y, cw, ch int, ok bool) {
	if w == nil {
		return 0, 0, 0, 0, false
	}
	s := w.Surface()
	if s == nil {
		return 0, 0, 0, 0, false
	}
	p, has := s.(windowBoundsProvider)
	if !has {
		return 0, 0, 0, 0, false
	}
	return p.WindowBounds()
}

// windowWorkAreaPos 取窗口外框左上角相对其所在显示器工作区左上角的偏移
// (**设备像素** —— 脚本口径; 后端位置单位经 toDevicePx 换算)。
//
// 后端不支持读位置时 ok=false (调用方给 0 —— "位置未知"退化为"贴着工作区
// 原点", 至少与 MoveTo(0,0) 的语义自洽)。
func windowWorkAreaPos(w *Window) (x, y int, ok bool) {
	ax, ay, _, _, ok := windowAbsoluteBounds(w)
	if !ok {
		return 0, 0, false
	}
	d, has := displayOfWorkWindow(w)
	if !has {
		return 0, 0, false
	}
	return toDevicePx(ax-d.WorkX, d), toDevicePx(ay-d.WorkY, d), true
}

// windowIsFocused 报告窗口此刻是否拥有系统焦点。
//
// 后端不支持 (windowActiveProvider 落空) 时退化为"是否是最近挂载的窗口"
// (activeApp) —— 这比恒 false 有意义: 单窗口场景下它恒真, 与直觉一致。
func windowIsFocused(w *Window) bool {
	if w == nil {
		return false
	}
	if p, ok := w.Surface().(windowActiveProvider); ok {
		return p.IsActive()
	}
	return w.a != nil && w.a == currentApp()
}

// ===== 窗口句柄方法 (JS 侧: moveTo/center/bounds/position/display) =====

// MoveTo 把窗口移到"当前显示器工作区"内的 (x,y) 偏移处。可在任意 goroutine
// 调用 (内部经 Post 投回 GUI 线程)。后端不支持移动时静默 no-op。
//
// **为什么是"当前显示器"**: moveTo 的坐标系随窗口所在屏走 —— 把窗口从主屏
// 拖到副屏后, moveTo(0,0) 是"副屏工作区左上角", 而不是"回到主屏"。这样
// 脚本一套居中/贴边代码在多屏下不用改。要跨屏请先 moveTo 到超出当前屏工作区
// 的坐标 (会落到相邻屏), 或由用户拖动 (后端会投递换屏事件)。
func (w *Window) MoveTo(x, y int) {
	if w == nil {
		return
	}
	wx, wy := x, y
	Post(func() { w.moveOnGUI(wx, wy) })
}

// moveOnGUI 是 MoveTo 的 GUI 线程实现 (解析所在屏 + 换算绝对坐标 + 调后端)。
func (w *Window) moveOnGUI(x, y int) {
	if w.closed() {
		return
	}
	s := w.Surface()
	if s == nil {
		return
	}
	m, ok := s.(windowMover)
	if !ok {
		return // 后端不支持移动: 静默降级
	}
	ax, ay := x, y
	if d, has := displayOfWorkWindow(w); has {
		// x/y 是设备像素的工作区相对量; 后端要的是绝对坐标 (位置单位随后端走)。
		ax = d.WorkX + fromDevicePx(x, d)
		ay = d.WorkY + fromDevicePx(y, d)
	}
	_ = m.MoveTo(ax, ay)
}

// Center 把窗口在工作区内居中 (当前所在显示器)。
func (w *Window) Center() {
	if w == nil {
		return
	}
	Post(func() { w.centerOnGUI() })
}

func (w *Window) centerOnGUI() {
	if w.closed() {
		return
	}
	d, ok := displayOfWorkWindow(w)
	if !ok {
		return
	}
	_, _, cw, ch, has := windowAbsoluteBounds(w)
	if !has || cw <= 0 || ch <= 0 {
		if s := w.Surface(); s != nil {
			cw, ch = s.Size()
		}
	}
	waW, waH := d.WorkW, d.WorkH
	if waW <= 0 {
		waW = d.W
	}
	if waH <= 0 {
		waH = d.H
	}
	w.moveOnGUI(maxInt(0, (waW-cw)/2), maxInt(0, (waH-ch)/2))
}

// Bounds 返回窗口的工作区相对位置与客户区尺寸 (后端不支持时全 0)。
func (w *Window) Bounds() (x, y, width, height int, displayID string, scale float64) {
	x, y, _ = windowWorkAreaPos(w)
	if s := w.Surface(); s != nil {
		width, height = s.Size()
	}
	if d, ok := displayOfWorkWindow(w); ok {
		displayID = d.ID
		scale = d.Scale
	}
	return
}

// Position 返回工作区相对位置 (后端不支持时 ok=false)。
func (w *Window) Position() (x, y int, ok bool) {
	return windowWorkAreaPos(w)
}

// Display 返回窗口所在显示器的 id (拿不到 → 空串)。
func (w *Window) Display() string {
	if d, ok := displayOfWorkWindow(w); ok {
		return d.ID
	}
	return ""
}
