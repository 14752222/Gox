//go:build darwin

package cocoa

import (
	"math"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

// 窗口管理 (§四 窗口/系统缺口) 的 cocoa 实现: 位置 / 层级 / 尺寸约束 /
// 缩放开关 / 全屏 / 激活 / 读几何 / 光标形状 —— gfx 侧 windowManager +
// cursorHost + boundsProvider 三个可选能力的实现。
//
// ## 坐标系: 这是本文件唯一容易出错的地方
//
// AppKit 的全局坐标是**底左原点、y 向上**, 而 gfx 全栈 (以及 win32/x11)
// 用的是**左上原点、y 向下**。两组换算都要过"主屏顶边"这条基准线:
//
//	appKitY = topY - (gfxY + 高度)      topY = 主屏 frame.origin.y + 高度
//
// 主屏 (带菜单栏那块) 的 origin 恰是 (0,0), 所以 topY 就是它的高度 ——
// 但与 display.go 一样用 `frame.origin.y + frame.size.height` 算, 免得将来
// 某块屏被摆在主屏上方时这里变成特例。
//
// 单位: NSWindow.frame 是**点**, gfx 的尺寸是**设备像素**, 所以进出都要
// 乘/除 backingScaleFactor (scale)。约束 (setMinSize/setMaxSize) 同样是点。
//
// ## 全屏用 setStyleMask: 而不是 toggleFullScreen:
//
// toggleFullScreen: 是**异步动画**且不带参数 —— 想"设成指定状态"就得先查
// 当前状态再决定要不要切, 中间还夹着动画期, 语义上变成"可能没生效"。
// 直接改 styleMask 里的 NSWindowStyleMaskFullScreen 位是同步的, 且
// isFullscreen() 读回即时正确。

var (
	// selSetFrameOrigin 已在 cocoa.go 的 selector 表里声明 (M4 那一批),
	// 这里不再重复 RegisterName —— 同名 selector 注册两次会编译不过。

	selSetFrameDisplay   = objc.RegisterName("setFrame:display:")
	selSetMinSize        = objc.RegisterName("setMinSize:")
	selMinSize           = objc.RegisterName("minSize") // 只读回 (断言跨屏后约束是否重推)
	selSetMaxSize        = objc.RegisterName("setMaxSize:")
	selSetStyleMask      = objc.RegisterName("setStyleMask:")
	selStyleMask         = objc.RegisterName("styleMask")
	selSetLevel          = objc.RegisterName("setLevel:")
	selOrderFront        = objc.RegisterName("orderFront:")
	selOrderBack         = objc.RegisterName("orderBack:")
	selSetHidesOnDeactiv = objc.RegisterName("setHidesOnDeactivate:")
	// NSCursor 的类方法 (按形状取单例, 再 -set 生效)
	selArrowCursor    = objc.RegisterName("arrowCursor")
	selPointingHand   = objc.RegisterName("pointingHandCursor")
	selIBeamCursor    = objc.RegisterName("IBeamCursor")
	selCrosshairCurs  = objc.RegisterName("crosshairCursor")
	selOpenHandCursor = objc.RegisterName("openHandCursor")
	selClosedHandCur  = objc.RegisterName("closedHandCursor")
	selNotAllowedCur  = objc.RegisterName("operationNotAllowedCursor")
	selResizeLRCursor = objc.RegisterName("resizeLeftRightCursor")
	selResizeUDCursor = objc.RegisterName("resizeUpDownCursor")
	selBusyCursor     = objc.RegisterName("busyButClickableCursor")
	selCursorSet      = objc.RegisterName("set")
)

// AppKit 窗口样式位 (与 cocoa.go 里的 nsStyle* 同源, 这里只补全屏位)。
const nsStyleFullScreen = 1 << 14

// 窗口层级 (NSWindowLevel)。
const (
	nsNormalWindowLevel   = 0
	nsFloatingWindowLevel = 3
)

// primaryTopY 返回主屏顶边在 AppKit 全局坐标里的 y (坐标换算的基准线, 单位点)。
func primaryTopY() float64 {
	main := objc.ID(objc.GetClass("NSScreen")).Send(selMainScreen)
	if main == 0 {
		return 0
	}
	fr := objc.Send[nsRect](main, selScreenFrame)
	return fr.Origin.Y + fr.Size.Height
}

// appKitOriginX 把 gfx 的窗口外框左边 x (设备像素) 换成 AppKit 的原点 x (点)。
func appKitOriginX(x int, scale float64) float64 {
	if scale <= 0 {
		scale = 1
	}
	return float64(x) / scale
}

// appKitOriginY 把 gfx 的"外框顶边距主屏顶边的距离 y"(设备像素) 换成
// AppKit 的窗口原点 y (底左原点, 点)。frameHeightPt 是**外框**高度 (点)。
//
// **单位与内外框是这里唯一容易错的两件事**:
//   - AppKit 的 frame 全用点, gfx 全栈 (以及 win32 的 GetWindowRect / X11 的
//     root 坐标) 用设备像素 ⇒ y 先除 scale 换成点, 再走基准线。
//   - 落点是**外框**口径 (与 win32 的 SetWindowPos 一致), 而 AppKit 的
//     initWithContentRect: 收的是**内容区**矩形 —— 两者差一条标题栏高度。
//     所以调用方必须传 frame 高度, 不能传内容区高度 (实测差 32 点, 直接
//     表现成"窗口比要求的位置高了一条标题栏")。
func appKitOriginY(y int, frameHeightPt float64, scale float64) float64 {
	if scale <= 0 {
		scale = 1
	}
	return primaryTopY() - float64(y)/scale - frameHeightPt
}

// gfxOriginY 是 appKitOriginY 的逆运算 (点 → 设备像素)。
func gfxOriginY(originY, frameHeightPt, scale float64) int {
	if scale <= 0 {
		scale = 1
	}
	return int(math.Round((primaryTopY() - originY - frameHeightPt) * scale))
}

// px 把 AppKit 的点长度换成 gfx 的设备像素 (四舍五入: 1pt = 1.5px 之类的
// 比例下, 截断会每窗口丢 1 像素, 累积到多窗口布局里就成了可见的缝)。
func px(pt, scale float64) int {
	if scale <= 0 {
		scale = 1
	}
	return int(math.Round(pt * scale))
}

// onWindowDidMove 窗口被移动: 投 EventMove (屏幕坐标, 左上原点)。
//
// 去重理由与 onWindowDidResize 相同: AppKit 在拖动过程中会连续发
// windowDidMove, 而"位置没变"的那些不该惊动脚本 (每次 onMove 都标脏 +
// 派发回调, 拖动时是每像素一次)。
//
// 建窗期 (placing) 的移动不投事件, 只记基线: AppKit 在 initWithContentRect:
// 与 center 上都会发 windowDidMove, 而这两次"位置落地"脚本本来就知道
// (它给过 X/Y, 或者接受了居中) —— 当成真实移动上报, 会让所有"建窗后按
// 顺序收头几个事件"的调用方平白多收一串 EventMove。
func (s *surface) onWindowDidMove() {
	if s.isClosed() {
		return
	}
	x, y, _, _ := s.Bounds()
	s.mu.Lock()
	placing := s.placing
	changed := x != s.lastPosX || y != s.lastPosY || !s.posKnown
	s.lastPosX, s.lastPosY, s.posKnown = x, y, true
	s.mu.Unlock()
	if changed && !placing {
		s.trySend(gfx.Event{Kind: gfx.EventMove, X: x, Y: y})
	}
}

// finishPlacement 结束"建窗期": 把落地后的真实位置记成 EventMove 去重基线,
// 之后第一次真实移动才会投事件。newSurface 末尾调用一次。
func (s *surface) finishPlacement() {
	x, y, _, _ := s.Bounds()
	s.mu.Lock()
	s.lastPosX, s.lastPosY, s.posKnown = x, y, true
	s.placing = false
	s.mu.Unlock()
}

// ===== 缩放比 (scale) 的保鲜 =====
//
// cocoa 是三后端里**唯一缓存了"点 → 设备像素"比值**的后端:
//   - win32 开了 Per-Monitor V2 DPI 感知, GetWindowRect / 客户区坐标 / 鼠标
//     坐标本来就是物理像素, 没有可过期的缓存;
//   - x11 的坐标是像素, 缩放比恒 1。
// AppKit 则全用点, 而 gfx 全栈用设备像素。M4 合流后**位置**的单位换算已收归
// gfx 层 (Display.PosInPoints / posScale, 见 window_move.go), 后端承接的是
// 点; 但**客户区尺寸与输入坐标**仍由后端自己乘/除 scale:
//
//	postDevice        鼠标/滚轮坐标 → 设备像素 (命中测试用)
//	Size() / w,h      客户区尺寸 (设备像素)
//	onWindowDidResize 点尺寸 × scale ⇒ EventResize
//	ResizeClient      设备像素 → 点
//	SetSizeConstraints 设备像素 → 点
//	allocBackbuffer   contentsScale / nsimg 点尺寸
//	Bounds()          外框设备像素 (只用于 EventMove 的载荷)
//
// 这份缓存过期时的表现是"点不准 + 客户区尺寸读回错单位 + Retina 上发虚",
// 而不是报错 —— 所以两处都要保鲜: 建窗落点之后对齐一次 (窗口最终落在哪块屏
// 决定了起点比值), 之后靠 AppKit 的 windowDidChangeBackingProperties: 刷新。
//
// 为什么初始那次不能省: 建窗时只能按**主屏**缩放比把 cfg.Width/Height 换算成
// 点 (initWithContentRect: 收点), 而窗口最终落到哪块屏要 placement 之后才知道。
// 笔记本 Retina 主屏 (2x) + 外接 1080p (1x) 是最常见的组合, 猜错就是整块屏的
// 窗口鼠标坐标差一倍。

// currentScale 读窗口当前的 backingScaleFactor (AppKit 侧的真值)。
func (s *surface) currentScale() float64 {
	if s == nil {
		return 1
	}
	v := s.view
	if v == 0 {
		v = s.win
	}
	if v == 0 {
		return 1
	}
	scale := objc.Send[float64](v, selBackingScale)
	if scale <= 0 {
		return 1
	}
	return scale
}

// applyScale 是"改比值"的公共步骤: 更新缓存并按 (w, h) 设备像素重建缓冲。
// 返回是否真的改了 —— 没改就一个平台调用都不发。
func (s *surface) applyScale(newScale float64, w, h int) bool {
	if s == nil || s.win == 0 || newScale <= 0 || w <= 0 || h <= 0 {
		return false
	}
	s.mu.Lock()
	same := newScale == s.scale && w == s.w && h == s.h
	s.scale = newScale
	s.mu.Unlock()
	if same {
		return false
	}
	// allocBackbuffer 内部会用**新的** s.scale 定 nsimg 的点尺寸与
	// layer.contentsScale (两者比值必须一致, 否则 Retina 上会发虚)。
	s.allocBackbuffer(w, h)
	return true
}

// setScale 改比值, 客户区的**点**尺寸不变 —— 设备像素尺寸按新比值重算
// (同一个窗口挪到 Retina 屏上, 点尺寸不动、设备像素翻倍)。返回新的设备像素
// 尺寸与是否真的改了。
func (s *surface) setScale(newScale float64) (w, h int, changed bool) {
	if s == nil || s.win == 0 || newScale <= 0 {
		return 0, 0, false
	}
	b := objc.Send[nsRect](s.win.Send(selContentView), selBounds)
	w = int(b.Size.Width*newScale + 0.5)
	h = int(b.Size.Height*newScale + 0.5)
	if !s.applyScale(newScale, w, h) {
		return w, h, false
	}
	return w, h, true
}

// pinClientSize 改比值, 并把客户区尺寸**钉在** wantW/wantH 设备像素 (点尺寸按
// 新比值现算)。建窗落点之后用这个 —— 那时才知道窗口真正落在哪块屏上。
//
// 与 setScale 的差别: setScale 保持"点尺寸不变", 它适合**运行期**跨屏 (窗口
// 的点尺寸本就不该因为挪了个屏而变); pinClientSize 保持"设备像素尺寸不变",
// 适合**建窗期** (脚本给的是设备像素尺寸, 不能因为落在不同缩放的屏上就变)。
func (s *surface) pinClientSize(newScale float64, wantW, wantH int) bool {
	if s == nil || s.win == 0 || newScale <= 0 || wantW <= 0 || wantH <= 0 {
		return false
	}
	if !s.applyScale(newScale, wantW, wantH) {
		return false
	}
	s.win.Send(selSetContentsSize, nsSize{
		Width:  float64(wantW) / newScale,
		Height: float64(wantH) / newScale,
	})
	return true
}

// onBackingChanged 窗口的 backing 属性变了: 取 AppKit 侧的真值重新对齐。
//
// 拆成"读真值"与 applyBackingScale("按新比值重算") 两层, 是为了让后者能脱离
// "真去插一块缩放不同的屏"被完整测到 (见 window_e2e_test.go)。
func (s *surface) onBackingChanged() {
	if s == nil {
		return
	}
	s.applyBackingScale(s.currentScale())
}

// applyBackingScale 按新的"点 → 设备像素"比值重算全部派生量, 并把**随比值改变
// 的量**补报给脚本 —— 不补的话内核那边存的位置/尺寸会一直停在旧单位上:
//   - 尺寸: 点尺寸没动、设备像素变了 ⇒ EventResize (内核据此重排/重绘)
//   - 位置: 点原点没动、设备像素坐标变了 ⇒ EventMove (内核的位置缓存靠它)
//
// 还有一件必须做的事是**重推尺寸约束**: 内核给的是设备像素, 落到 AppKit 是点,
// 比值一变旧的点值就整体偏一倍 (400 设备像素在 1x 屏是 400pt、2x 屏只有 200pt)。
func (s *surface) applyBackingScale(newScale float64) {
	if s == nil || s.isClosed() {
		return
	}
	w, h, changed := s.setScale(newScale)
	if !changed {
		return
	}
	if s.conSet {
		s.pushConstraints()
	}
	s.trySend(gfx.Event{Kind: gfx.EventResize, W: w, H: h})
	// 位置: 交给 onWindowDidMove 用**新** scale 重算并上报 (去重账也在那里维护)。
	// 先把 posKnown 作废, 否则"点坐标没变"会被判成没动、事件投不出去。
	s.mu.Lock()
	s.posKnown = false
	s.mu.Unlock()
	s.onWindowDidMove()
}

// ===== boundsProvider =====

// Bounds 读窗口外框的屏幕坐标 (左上原点, 设备像素) 与尺寸 (设备像素) ——
// 与 win32 的 GetWindowRect / X11 的 root 坐标同口径。
func (s *surface) Bounds() (int, int, int, int) {
	if s == nil || s.win == 0 {
		return 0, 0, 0, 0
	}
	fr := objc.Send[nsRect](s.win, selScreenFrame) // NSWindow.frame (点)
	if fr.Size.Width <= 0 || fr.Size.Height <= 0 {
		return 0, 0, 0, 0
	}
	s.mu.Lock()
	scale := s.scale
	s.mu.Unlock()
	return px(fr.Origin.X, scale),
		gfxOriginY(fr.Origin.Y, fr.Size.Height, scale),
		px(fr.Size.Width, scale), px(fr.Size.Height, scale)
}

// ===== windowManager =====
//
// 合流备注 (2026-10-02): 这里原本还有一个 MoveTo(x, y) (吃**设备像素**屏幕
// 坐标, 内部除 scale)。它与 cocoa.go 里实现 windowMover 的 MoveTo(x, y) error
// (吃"点", 由 gfx 层统一换算) 是同一个后端类型上的同名方法, 无法共存。
// 保留后者: 位置口径由 gfx 层一处定义 (ResolveWindowPlacement / moveOnGUI),
// 后端不做二次解释 —— 与 win32 / x11 也一致。

// SetLevel 设窗口层级。
//
// top → NSFloatingWindowLevel (浮在普通窗口之上, 不被别的应用盖住 ——
// 悬浮窗/工具面板的语义); bottom → 回到普通层并 orderBack: (排到同层
// 所有窗口之后); normal → 普通层。
func (s *surface) SetLevel(level string) {
	if s == nil || s.win == 0 {
		return
	}
	switch level {
	case "top":
		s.win.Send(selSetLevel, uintptr(nsFloatingWindowLevel))
	case "bottom":
		s.win.Send(selSetLevel, uintptr(nsNormalWindowLevel))
		s.win.Send(selOrderBack, objc.ID(0))
	default:
		s.win.Send(selSetLevel, uintptr(nsNormalWindowLevel))
	}
}

// SetSizeConstraints 设用户缩放时的尺寸钳位 (入参是**设备像素**)。
//
// 只**记住设备像素原值**, 换算成点推给 AppKit 的动作交给 pushConstraints:
// 窗口跨到缩放不同的屏上时比值会变 (见 applyBackingScale), 只推一次的话旧点值
// 会整体偏一倍。
func (s *surface) SetSizeConstraints(minW, minH, maxW, maxH int) {
	if s == nil || s.win == 0 {
		return
	}
	s.mu.Lock()
	s.conMinW, s.conMinH, s.conMaxW, s.conMaxH = minW, minH, maxW, maxH
	s.conSet = true
	s.mu.Unlock()
	s.pushConstraints()
}

// pushConstraints 按**当前** scale 把记住的设备像素约束换成点推给 AppKit。
//
// maxW/maxH 为 0 表示"不约束": AppKit 的 setMaxSize: 对 0 的解释是
// "最大 0 点", 会把窗口钳成不可用 —— 所以必须显式给一个大值而不是原样传 0。
// 用 1e9 点 (≈ 3.5 亿像素宽) 当"无限": 比 FLT_MAX 安全, 不会在 AppKit
// 内部的取整/相加里溢出。
func (s *surface) pushConstraints() {
	s.mu.Lock()
	scale := s.scale
	minW, minH, maxW, maxH := s.conMinW, s.conMinH, s.conMaxW, s.conMaxH
	s.mu.Unlock()
	if scale <= 0 {
		scale = 1
	}
	const unlimited = 1e9
	size := nsSize{Width: unlimited, Height: unlimited}
	if minW > 0 || minH > 0 {
		s.win.Send(selSetMinSize, nsSize{Width: float64(minW) / scale, Height: float64(minH) / scale})
	} else {
		s.win.Send(selSetMinSize, nsSize{Width: 1, Height: 1})
	}
	if maxW > 0 {
		size.Width = float64(maxW) / scale
	}
	if maxH > 0 {
		size.Height = float64(maxH) / scale
	}
	s.win.Send(selSetMaxSize, size)
}

// SetResizable 开关 NSWindowStyleMaskResizable 位。
//
// 与 win32 同一取舍: 同时去掉/加上 Maximizable 语义 —— AppKit 的可缩放位
// 本身就同时管"拖边框"与"绿灯最大化", 不需要额外处理。
func (s *surface) SetResizable(on bool) {
	if s == nil || s.win == 0 {
		return
	}
	mask := objc.Send[uint64](s.win, selStyleMask)
	if on {
		mask |= nsStyleResizable
	} else {
		mask &^= nsStyleResizable
	}
	s.win.Send(selSetStyleMask, mask)
}

// SetFullscreen 进出全屏 (同步改 styleMask, 见文件头说明)。
func (s *surface) SetFullscreen(on bool) {
	if s == nil || s.win == 0 {
		return
	}
	mask := objc.Send[uint64](s.win, selStyleMask)
	if on {
		mask |= nsStyleFullScreen
	} else {
		mask &^= nsStyleFullScreen
	}
	s.win.Send(selSetStyleMask, mask)
}

// Activate 把窗口带到前台并激活应用。
func (s *surface) Activate() {
	if s == nil || s.win == 0 {
		return
	}
	s.win.Send(selMakeKeyAndOrder, objc.ID(0))
	app := objc.ID(objc.GetClass("NSApplication")).Send(selSharedApplication)
	if app != 0 {
		app.Send(selActivateIgnoring, true)
	}
}

// ===== cursorHost =====

// cursorFor 取形状名对应的 NSCursor 单例 (未知形状 → nil)。
//
// "none" 刻意**不做**: NSCursor 的隐藏是一对难的 hide/unhide 配对 (系统
// 自己也会 hide/unhide, 手动记数一旦漏配就永久没光标), 而且 AppKit 会在
// 鼠标移动/切换应用时自己重置。与其留一个"偶尔把用户光标弄丢"的实现,
// 不如让它落回箭头 —— win32 那边 hide 是单次调用 (SetCursor(NULL)), 风险
// 结构不同, 所以两边不必强求一致。
func cursorFor(shape string) objc.ID {
	cls := objc.ID(objc.GetClass("NSCursor"))
	if cls == 0 {
		return 0
	}
	switch shape {
	case "pointer", "grab", "grabbing":
		return cls.Send(selPointingHand)
	case "text":
		return cls.Send(selIBeamCursor)
	case "crosshair":
		return cls.Send(selCrosshairCurs)
	case "grab-open":
		return cls.Send(selOpenHandCursor)
	case "move":
		return cls.Send(selClosedHandCur)
	case "not-allowed":
		return cls.Send(selNotAllowedCur)
	case "ew-resize", "col-resize":
		return cls.Send(selResizeLRCursor)
	case "ns-resize", "row-resize":
		return cls.Send(selResizeUDCursor)
	case "wait", "progress":
		return cls.Send(selBusyCursor)
	case "default":
		return cls.Send(selArrowCursor)
	}
	return 0
}

// SetCursor 设鼠标光标形状 (gfx 的 cursorHost)。
//
// AppKit 与 win32 的机制差异: 没有"窗口级光标"这个概念, 只有
// "把某个 NSCursor 设为当前光标" + 视图的 cursor rects。gfx 的形状本来
// 就是随鼠标移动而变的, 所以存下句柄并在每次 mouseMoved 时重申一次
// (见 reapplyCursor) 就够了 —— 对应 win32 的 WM_SETCURSOR 分支。
func (s *surface) SetCursor(shape string) {
	if s == nil {
		return
	}
	cur := cursorFor(shape)
	s.mu.Lock()
	s.cursor = cur
	// 形状未知 (含 "none") 时清空: 之后不再重申, 交给 AppKit 缺省箭头。
	s.cursorUnset = cur == 0
	s.mu.Unlock()
	if cur != 0 {
		cur.Send(selCursorSet)
	}
}

// reapplyCursor 在每次鼠标移动时重申光标 (AppKit 会在光标跨视图边界/切换
// 应用时把光标复位成箭头, 只设一次是留不住的)。
func (s *surface) reapplyCursor() {
	s.mu.Lock()
	cur, unset := s.cursor, s.cursorUnset
	s.mu.Unlock()
	if unset || cur == 0 {
		return
	}
	cur.Send(selCursorSet)
}

// 编译期断言: *surface 必须满足这三个可选能力 (与 win32 同一纪律:
// 可选接口的失配是静默的, 用编译期断言把它变成硬错误)。
var (
	_ gfx.Surface = (*surface)(nil)
	// 窗口系统态 (§四): 层级 / 约束 / 缩放开关 / 全屏 / 激活。
	//
	// **刻意不含 MoveTo** —— 位移归 M4 的 windowMover (cocoa.go 里的
	// MoveTo(x, y) error), gfx 层只认带 error 的那个; 同一个类型上不可能
	// 有两个同名方法 (合流时踩过, 见文件末尾的合流备注)。
	_ interface {
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
