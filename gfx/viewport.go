package gfx

// ===== gx/viewport: 分屏 / 自由窗口 / 安全区 / 软键盘 =====
//
// ## 这一层补的是"折叠适配"缺的那一半
//
// gx/screen 已经把**设备侧**的环境做完了: 有哪几块屏、多大、缩放多少、折没折
// (posture / hinge / regions), 于是路由能在半折时切成双栏。但真正的移动端布局
// 还要回答另外三个问题, 而它们都**不是设备属性, 而是窗口属性**:
//
//  1. 安全区 (insets): 状态栏、导航栏、刘海/挖孔、圆角占掉了多少像素。折叠屏
//     展开后刘海在左上、折起来在顶部, 同一台设备两种值 —— 所以它挂在窗口上。
//  2. 软键盘: 弹出来之后可用高度还剩多少 (聊天页、表单页的第一需求)。
//  3. 分屏/自由窗口: Android 的分屏、iPad 的 Split View / Stage Manager、桌面的
//     窗口管理 —— 此时"窗口尺寸"才是真相, 而且窗口可能只占屏幕的一侧。
//
// ## 与 gx/screen 的分工 (一句话)
//
//	gx/screen   答"我这台设备是什么样"  —— 显示器 / 姿态 / 折痕
//	gx/viewport 答"我这个窗口被怎么摆"  —— 安全区 / 键盘 / 分屏形态
//
// ## 又是一个"只提供通道, 不猜"的模块
//
// insets 与分屏形态**只有宿主知道** (Android 的 WindowInsetsCompat /
// onMultiWindowModeChanged, iOS 的 safeAreaInsets / UIScene 尺寸)。桌面没有任何
// API 能给出"状态栏占了多少像素"。所以:
//
//	宿主: gfx.ReportViewport(...)       ← 系统回调里调 (GUI 线程)
//	脚本: useInsets() / safeAreaStyle() ← 响应式读
//
// 与 gx/screen 的 reportPosture 完全同构 —— 于是**整条响应式链路在桌面单测里
// 就能跑完**, 不必等真机。
//
// ## 为什么单独一套版本号 (不复用 screen / native 的)
//
// 软键盘每弹一次都会改 insets。若与 gx/screen 共用一个版本号, 每次弹键盘都会
// 触发所有 onDisplayChange 回调; 与 gx/device 共用则会惊动 onBatteryChange。
// 三套订阅各自独立, 代价只是多一个 signal, 换来的是"谁的订阅被唤醒"完全可推理。
//
// ## 锁的复用说明
//
// 本文件的包级状态与 gfx/native.go 共用 `nativeMu`。这不是偷懒: 两者从不互相
// 调用 (viewport 不碰宿主的 Call, native 不碰 viewport 表), 共用一个互斥锁反而
// 消除了"两个锁的获取顺序"这个错误来源。

import (
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// Insets 是窗口四边的占位像素 (状态栏 / 导航栏 / 刘海 / 圆角)。
type Insets struct {
	Top    int
	Right  int
	Bottom int
	Left   int
}

// viewport 形态取值 (与 Android WindowManager / iOS UIScene 的词汇对齐)。
const (
	ViewportFullscreen = "fullscreen" // 独占整个屏幕
	ViewportSplit      = "split"      // 分屏 (Android 分屏 / iPad Split View)
	ViewportPIP        = "pip"        // 画中画
	ViewportFreeform   = "freeform"   // 自由窗口 (桌面窗口管理 / DeX / Stage Manager)
	ViewportUnknown    = "unknown"
)

// 尺寸类 (iOS 的 size class; Android 用同样的两个词表达同一个意思)。
const (
	SizeCompact = "compact"
	SizeRegular = "regular"
)

// Viewport 是一个窗口的可视区域环境。
type Viewport struct {
	Insets   Insets // 安全区: 内容不该画进去的区域
	Keyboard int    // 软键盘占的高度 (0 = 没弹)

	MultiWindow    bool    // 是否处于系统多窗口 (分屏/画中画/自由窗口)
	Mode           string  // 见上面的 Viewport* 常量
	Stage          string  // 分屏中的位置: "primary" | "secondary" | "tertiary"
	StageID        int     // 平台给的舞台编号 (Android 有, iOS/桌面可为 0)
	SplitDirection string  // "horizontal" | "vertical"
	SplitRatio     float64 // 本窗口在分屏里占的比例; 0 = 未知
	WidthClass     string  // "compact" | "regular"
	HeightClass    string
	// Updated 报告这份数据是"宿主报过"还是"内核缺省"。应用通常不需要它, 但
	// 排查"安全区为什么是 0"时它是第一手线索。
	Updated bool
}

// evViewport 是"窗口环境变了"的事件名。
const evViewport = "viewport"

// 安全区的量级合理性上限 (像素)。超过就当成宿主写错单位 (把 dp 当 px 之类)。
//
// 为什么要有这条: 一个单位写错的 insets (比如 24 而不是 72) 不会崩, 只会让内容
// 被状态栏盖住 —— 而且只在某些机型上盖住。夹一个上限至少让"整数倍的错误"表现
// 为可疑数值, 而不是看似正常的错误数值。
const viewportInsetMax = 400

var (
	viewports      = map[string]Viewport{}
	viewportRev    int
	viewportEnvGet object.Value
	viewportEnvSet object.Value
	viewportHooks  []object.Value
)

// viewportKey 取一个窗口的存储键 (nil → 全局缺省键 "")。
//
// 为什么允许空键: 无头宿主 / 单测 / 尚未挂载窗口时也要能上报与读取 (否则每条
// 用例都得先造一个真窗口)。查找顺序是"该窗口 → 全局缺省 → 零值"。
func viewportKey(win *Window) string {
	if win == nil {
		return ""
	}
	return strconv.Itoa(win.ID())
}

// viewportFor 取窗口的 Viewport (先精确后缺省)。
func viewportFor(win *Window) Viewport {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if v, ok := viewports[viewportKey(win)]; ok {
		return v
	}
	if v, ok := viewports[""]; ok {
		return v
	}
	return Viewport{Mode: ViewportFullscreen}
}

// viewportEnvSignal 是 viewport 版本号的 signal getter (惰性建, 同 gx/screen)。
func viewportEnvSignal() object.Value {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if viewportEnvGet != nil {
		return viewportEnvGet
	}
	exports, ok := object.LookupBuiltinModule("gx/solid")
	if !ok {
		return nil
	}
	createSignal, ok := exports["createSignal"]
	if !ok || !object.IsCallable(createSignal) {
		return nil
	}
	res := object.CallFunction(createSignal, nil, object.NewNumber(0))
	arr, ok := res.(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		return nil
	}
	viewportEnvGet, viewportEnvSet = arr.Elements[0], arr.Elements[1]
	return viewportEnvGet
}

// notifyViewportChanged 抬高版本 + 派发 onViewportChange 回调。
//
// 连续上报 (三星的分屏拖动会连着报几十次比例) 不在这里防抖是**有意的**: 脚本侧
// 的订阅者本来就只是标脏 → 下一帧重绘, 真正的节流在渲染层 (脏矩形 + 每帧一次
// 上屏)。在这里加防抖反而会漏掉"拖到最后停下"的那一次上报。
func notifyViewportChanged() {
	nativeMu.Lock()
	viewportRev++
	set := viewportEnvSet
	hooks := append([]object.Value(nil), viewportHooks...)
	nativeMu.Unlock()
	if set != nil {
		object.CallFunction(set, nil, object.NewNumber(float64(viewportRev)))
	}
	for _, fn := range hooks {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/viewport onViewportChange 回调抛错: %v", err)
		}
	}
}

// ReportViewport 由宿主上报某个窗口的可视区域环境 (GUI 线程)。
//
// 合并语义是 **upsert**: 只覆盖本次给到的字段。这一条很关键 —— Android 会在
// 系统回调里分别报三件事 (insets 变了 / 键盘弹了 / 进分屏了), 若每次上报都是
// "整份替换", 那么"键盘弹起"那一次会把先前报的 insets 清成 0。
func ReportViewport(win *Window, v Viewport) {
	key := viewportKey(win)
	nativeMu.Lock()
	if viewports == nil {
		viewports = map[string]Viewport{}
	}
	prev := viewports[key]
	v.Updated = true
	if v.Mode == "" {
		v.Mode = prev.Mode
		if v.Mode == "" {
			v.Mode = ViewportFullscreen
		}
	}
	if v.WidthClass == "" {
		v.WidthClass = prev.WidthClass
	}
	if v.HeightClass == "" {
		v.HeightClass = prev.HeightClass
	}
	if v.SplitDirection == "" {
		v.SplitDirection = prev.SplitDirection
	}
	if v.SplitRatio == 0 {
		v.SplitRatio = prev.SplitRatio
	}
	if v.Stage == "" {
		v.Stage = prev.Stage
	}
	if v.StageID == 0 {
		v.StageID = prev.StageID
	}
	viewports[key] = clampViewport(v)
	nativeMu.Unlock()
	notifyViewportChanged()
}

// clampViewport 把明显不合理的数值夹回可解释的范围内。
func clampViewport(v Viewport) Viewport {
	clamp := func(n int) int {
		if n < 0 {
			return 0
		}
		if n > viewportInsetMax {
			return viewportInsetMax
		}
		return n
	}
	v.Insets = Insets{
		Top:    clamp(v.Insets.Top),
		Right:  clamp(v.Insets.Right),
		Bottom: clamp(v.Insets.Bottom),
		Left:   clamp(v.Insets.Left),
	}
	if v.Keyboard < 0 {
		v.Keyboard = 0
	}
	if v.Keyboard > 4000 {
		v.Keyboard = 4000
	}
	if v.SplitRatio < 0 {
		v.SplitRatio = 0
	}
	if v.SplitRatio > 1 {
		v.SplitRatio = 1
	}
	if v.Mode == "" {
		v.Mode = ViewportFullscreen
	}
	return v
}

// ResetViewport 清空宿主上报 (测试 / 模拟器退出时回到缺省)。
// win 为 nil → 清空全局缺省键。
func ResetViewport(win *Window) {
	nativeMu.Lock()
	if win == nil {
		viewports = map[string]Viewport{}
	} else {
		delete(viewports, viewportKey(win))
	}
	nativeMu.Unlock()
	notifyViewportChanged()
}

// ViewportForKey 是 Go 侧的读数入口 (后端 / 测试用)。
func ViewportForKey(win *Window) Viewport { return viewportResolved(win) }

// ===== 分屏与尺寸类的缺省推导 =====

// splitActive 报告"这个窗口正处于分屏的多窗格状态"。
func splitActive(v Viewport) bool {
	if v.MultiWindow {
		return true
	}
	switch v.Mode {
	case ViewportSplit, ViewportPIP, ViewportFreeform:
		return true
	}
	return false
}

// deriveSizeClasses 在没有宿主上报尺寸类时, 用窗口宽度 / 缩放换算 dp 再判定。
//
// 600dp / 480dp 这两个断点是 Android 官方的大屏分界 (Jetpack WindowManager 用
// 同一套), iOS 的 regular 起点也在这附近。对一个自研运行时来说, 与其发明自己的
// 断点, 不如沿用"平台会怎么判"。这里只做**缺省推导**: 宿主报了 WidthClass 就以
// 宿主为准 (折叠屏某些状态下系统仍报 compact, 那种情况只有宿主知道)。
func deriveSizeClasses(w, h int, scale float64) (string, string) {
	if scale <= 0 {
		scale = 1
	}
	cls := func(dp, threshold float64) string {
		if dp < threshold {
			return SizeCompact
		}
		return SizeRegular
	}
	return cls(float64(w)/scale, 600), cls(float64(h)/scale, 480)
}

// windowSurfaceOf 取窗口对应的 Surface (win 为 nil → 活跃窗口)。
func windowSurfaceOf(win *Window) Surface {
	if win != nil {
		return win.Surface()
	}
	if a := currentApp(); a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.surface
	}
	return nil
}

// windowPixelSize 取窗口客户区像素尺寸 (拿不到则退回所在显示器尺寸)。
func windowPixelSize(win *Window) (int, int) {
	if s := windowSurfaceOf(win); s != nil {
		if w, h := s.Size(); w > 0 && h > 0 {
			return w, h
		}
	}
	d, _ := displayOfWorkWindow(win)
	return d.W, d.H
}

// viewportResolved 取"带有兜底推导"的完整 Viewport: 尺寸类未上报时按窗口 dp 推。
func viewportResolved(win *Window) Viewport {
	v := viewportFor(win)
	if v.WidthClass != "" && v.HeightClass != "" {
		return v
	}
	w, h := windowPixelSize(win)
	scale := 1.0
	if d, ok := displayOfWorkWindow(win); ok && d.Scale > 0 {
		scale = d.Scale
	}
	wc, hc := deriveSizeClasses(w, h, scale)
	if v.WidthClass == "" {
		v.WidthClass = wc
	}
	if v.HeightClass == "" {
		v.HeightClass = hc
	}
	return v
}

// ===== JS 对象构造 =====

func insetsToJS(i Insets) object.Value {
	o := object.NewObject()
	o.SetProperty("top", object.NewNumber(float64(i.Top)))
	o.SetProperty("right", object.NewNumber(float64(i.Right)))
	o.SetProperty("bottom", object.NewNumber(float64(i.Bottom)))
	o.SetProperty("left", object.NewNumber(float64(i.Left)))
	return o
}

func viewportToJS(v Viewport) object.Value {
	o := object.NewObject()
	o.SetProperty("insets", insetsToJS(v.Insets))
	// 四个平铺便利字段: 布局代码里 `insets.top` 与 `safeTop` 的出现频率差不多,
	// 而 `paddingTop: safeTop` 比 `paddingTop: insets.top` 短且不容易写错。
	o.SetProperty("safeTop", object.NewNumber(float64(v.Insets.Top)))
	o.SetProperty("safeRight", object.NewNumber(float64(v.Insets.Right)))
	o.SetProperty("safeBottom", object.NewNumber(float64(v.Insets.Bottom)))
	o.SetProperty("safeLeft", object.NewNumber(float64(v.Insets.Left)))
	o.SetProperty("keyboard", object.NewNumber(float64(v.Keyboard)))
	o.SetProperty("keyboardVisible", object.NewBoolean(v.Keyboard > 0))
	o.SetProperty("multiWindow", object.NewBoolean(v.MultiWindow))
	o.SetProperty("split", object.NewBoolean(splitActive(v)))
	nativeSet(o, "mode", v.Mode)
	nativeSet(o, "stage", v.Stage)
	o.SetProperty("stageId", object.NewNumber(float64(v.StageID)))
	nativeSet(o, "splitDirection", v.SplitDirection)
	o.SetProperty("splitRatio", object.NewNumber(v.SplitRatio))
	nativeSet(o, "widthClass", v.WidthClass)
	nativeSet(o, "heightClass", v.HeightClass)
	// 三个最常用的派生判断: 折叠/分屏适配的分支几乎都落在这三条上。
	o.SetProperty("compactWidth", object.NewBoolean(v.WidthClass == SizeCompact))
	o.SetProperty("regularWidth", object.NewBoolean(v.WidthClass == SizeRegular))
	o.SetProperty("reported", object.NewBoolean(v.Updated))
	return o
}

// contentAreaToJS 算"扣掉安全区与键盘之后还能用的内容矩形"。
//
// 这个 API 的存在意义: 折叠屏 + 分屏 + 软键盘三个因素叠加时, 手算可用区域是最
// 容易出错的一步 (少减一个 insets 的症状是"底部按钮被键盘盖住", 而且只在某些
// 机型上出现)。内核算一次, 所有应用共用。
func contentAreaToJS(win *Window, v Viewport) object.Value {
	w, h := windowPixelSize(win)
	// 键盘占的是底部区域, 与 safeBottom 取**较大者**而不是相加: Android 的键盘
	// insets 通常已经包含了导航栏高度, 相加会把内容再往上顶一截。
	bottom := v.Insets.Bottom
	if v.Keyboard > bottom {
		bottom = v.Keyboard
	}
	usableW, usableH := w-v.Insets.Left-v.Insets.Right, h-v.Insets.Top-bottom
	if usableW < 0 {
		usableW = 0
	}
	if usableH < 0 {
		usableH = 0
	}
	o := object.NewObject()
	o.SetProperty("x", object.NewNumber(float64(v.Insets.Left)))
	o.SetProperty("y", object.NewNumber(float64(v.Insets.Top)))
	o.SetProperty("width", object.NewNumber(float64(usableW)))
	o.SetProperty("height", object.NewNumber(float64(usableH)))
	o.SetProperty("windowWidth", object.NewNumber(float64(w)))
	o.SetProperty("windowHeight", object.NewNumber(float64(h)))
	o.SetProperty("keyboard", object.NewNumber(float64(v.Keyboard)))
	nativeSet(o, "mode", v.Mode)
	return o
}

// safeAreaStyleToJS 生成可直接放进 JSX 的内边距。
//
// 本模块里"最省事"的一个 API:
//
//	<column {...safeAreaStyle()}>…</column>
//
// 它把"给定像素内边距"这件事从每个应用手里收回来。注意**只有内边距**: 定位
// (top/left) 与尺寸 (width/height) 不该由它决定 —— 那取决于应用自己的布局
// (固定头 + 可滚内容, 还是全屏叠层), 替应用做决定必然有一半场景是错的。
func safeAreaStyleToJS(v Viewport, includeKeyboard bool) object.Value {
	bottom := v.Insets.Bottom
	if includeKeyboard && v.Keyboard > bottom {
		bottom = v.Keyboard
	}
	o := object.NewObject()
	o.SetProperty("paddingTop", object.NewNumber(float64(v.Insets.Top)))
	o.SetProperty("paddingLeft", object.NewNumber(float64(v.Insets.Left)))
	o.SetProperty("paddingRight", object.NewNumber(float64(v.Insets.Right)))
	o.SetProperty("paddingBottom", object.NewNumber(float64(bottom)))
	return o
}

// ===== 取值函数 =====

// viewportUse 造一个 `useXxx(win?)` 取值函数: 每次调用解析可选窗口参数 + 读一次
// 版本号 signal (= 订阅)。窗口参数必须**在调用时**解析 —— 注册时还没有调用方。
func viewportUse(pick func(Viewport) object.Value) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		if g := viewportEnvSignal(); g != nil {
			object.CallFunction(g, nil) // 读一次 = 订阅一次
		}
		return pick(viewportResolved(windowArg(args)))
	}
}

// ===== JS 入口 =====

// jsReportViewport 是 `reportViewport(options)` —— 宿主/模拟器上报 (GUI 线程)。
//
// 可传 `window` 句柄指定窗口 (省略 → 全局缺省, 作用于所有未单独上报的窗口)。
func jsReportViewport(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	win := windowArg(args)
	v := Viewport{
		Insets: Insets{
			Top:    int(objPropNum(opts, "top")),
			Right:  int(objPropNum(opts, "right")),
			Bottom: int(objPropNum(opts, "bottom")),
			Left:   int(objPropNum(opts, "left")),
		},
		Keyboard: int(objPropNum(opts, "keyboard")),
	}
	// insets 也允许写成嵌套对象 ({insets: {top: 24}}) —— 两种写法都常见, 都收,
	// 优先级给嵌套 (更明确)。
	if ins := nativePropObj(opts, "insets"); ins != nil {
		v.Insets = Insets{
			Top:    int(objPropNum(ins, "top")),
			Right:  int(objPropNum(ins, "right")),
			Bottom: int(objPropNum(ins, "bottom")),
			Left:   int(objPropNum(ins, "left")),
		}
	}
	if val := objProp(opts, "multiWindow"); val != nil {
		v.MultiWindow = nativeBool(val)
	}
	v.Mode = normalizeViewportMode(objPropStr(opts, "mode"))
	v.Stage = strings.ToLower(strings.TrimSpace(objPropStr(opts, "stage")))
	v.StageID = int(objPropNum(opts, "stageId"))
	v.SplitDirection = strings.ToLower(strings.TrimSpace(objPropStr(opts, "splitDirection")))
	v.SplitRatio = objPropNum(opts, "splitRatio")
	v.WidthClass = normalizeSizeClass(objPropStr(opts, "widthClass"))
	v.HeightClass = normalizeSizeClass(objPropStr(opts, "heightClass"))
	// 报了 mode 或 stage 就等于声明"我在分屏里" —— 与 gx/screen 的"报了姿态就等于
	// 声明这是折叠屏"同一条省事规则。
	if v.Mode == ViewportSplit || v.Mode == ViewportPIP || v.Mode == ViewportFreeform || v.Stage != "" {
		v.MultiWindow = true
	}
	ReportViewport(win, v)
	return object.UndefinedSingleton
}

// normalizeViewportMode 归一化形态词 (不认识 → unknown, 不猜)。
func normalizeViewportMode(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case ViewportFullscreen, "full", "":
		return ViewportFullscreen
	case ViewportSplit, "multiwindow", "multi-window", "split-screen":
		return ViewportSplit
	case ViewportPIP, "picture-in-picture", "pictureinpicture":
		return ViewportPIP
	case ViewportFreeform, "floating", "windowed", "resizable":
		return ViewportFreeform
	default:
		return ViewportUnknown
	}
}

// normalizeSizeClass 归一化尺寸类 (不认识 → 空 = "没报", 让缺省推导接手)。
func normalizeSizeClass(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case SizeCompact:
		return SizeCompact
	case SizeRegular:
		return SizeRegular
	}
	return ""
}

// jsResetViewport 清空上报 (`resetViewport(win?)`)。
func jsResetViewport(args ...object.Value) object.Value {
	ResetViewport(windowArg(args))
	return object.UndefinedSingleton
}

func jsOnViewportChange(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("onViewportChange: 需要回调函数")
	}
	fn := args[0]
	nativeMu.Lock()
	viewportHooks = append(viewportHooks, fn)
	nativeMu.Unlock()
	return object.NewBuiltin("offViewportChange", func(args ...object.Value) object.Value {
		jsOffViewportChange(fn)
		return object.UndefinedSingleton
	})
}

func jsOffViewportChange(fn object.Value) object.Value {
	nativeMu.Lock()
	for i, h := range viewportHooks {
		if h == fn {
			viewportHooks = append(viewportHooks[:i], viewportHooks[i+1:]...)
			break
		}
	}
	nativeMu.Unlock()
	return object.UndefinedSingleton
}

// resetViewportStateForTest 清空上报与回调。
func resetViewportStateForTest() {
	nativeMu.Lock()
	viewports = map[string]Viewport{}
	viewportHooks = nil
	viewportRev++
	viewportEnvGet, viewportEnvSet = nil, nil
	nativeMu.Unlock()
}

func init() {
	object.RegisterBuiltinModule("gx/viewport", func() map[string]object.Value {
		return map[string]object.Value{
			"viewport": scr("viewport", func(args ...object.Value) object.Value {
				return viewportToJS(viewportResolved(windowArg(args)))
			}),
			"useViewport": scr("useViewport", viewportUse(viewportToJS)),

			"insets": scr("insets", func(args ...object.Value) object.Value {
				return insetsToJS(viewportResolved(windowArg(args)).Insets)
			}),
			"useInsets": scr("useInsets", viewportUse(func(v Viewport) object.Value {
				return insetsToJS(v.Insets)
			})),

			"keyboardHeight": scr("keyboardHeight", func(args ...object.Value) object.Value {
				return object.NewNumber(float64(viewportResolved(windowArg(args)).Keyboard))
			}),
			"useKeyboardHeight": scr("useKeyboardHeight", viewportUse(func(v Viewport) object.Value {
				return object.NewNumber(float64(v.Keyboard))
			})),
			"keyboardVisible": scr("keyboardVisible", func(args ...object.Value) object.Value {
				return object.NewBoolean(viewportResolved(windowArg(args)).Keyboard > 0)
			}),

			"multiWindow": scr("multiWindow", func(args ...object.Value) object.Value {
				return object.NewBoolean(viewportResolved(windowArg(args)).MultiWindow)
			}),
			"useMultiWindow": scr("useMultiWindow", viewportUse(func(v Viewport) object.Value {
				o := object.NewObject()
				o.SetProperty("multiWindow", object.NewBoolean(v.MultiWindow))
				o.SetProperty("split", object.NewBoolean(splitActive(v)))
				nativeSet(o, "mode", v.Mode)
				return o
			})),
			"isSplit": scr("isSplit", func(args ...object.Value) object.Value {
				return object.NewBoolean(splitActive(viewportResolved(windowArg(args))))
			}),
			"splitInfo": scr("splitInfo", func(args ...object.Value) object.Value {
				v := viewportResolved(windowArg(args))
				o := object.NewObject()
				o.SetProperty("active", object.NewBoolean(splitActive(v)))
				nativeSet(o, "mode", v.Mode)
				nativeSet(o, "stage", v.Stage)
				o.SetProperty("stageId", object.NewNumber(float64(v.StageID)))
				nativeSet(o, "direction", v.SplitDirection)
				o.SetProperty("ratio", object.NewNumber(v.SplitRatio))
				return o
			}),

			"contentArea": scr("contentArea", func(args ...object.Value) object.Value {
				win := windowArg(args)
				return contentAreaToJS(win, viewportResolved(win))
			}),
			"safeAreaStyle": scr("safeAreaStyle", func(args ...object.Value) object.Value {
				// 第 2 个参数可以是 true, 表示"把键盘高度也算进下边距"。
				withKeyboard := len(args) > 1 && nativeBool(args[1])
				return safeAreaStyleToJS(viewportResolved(windowArg(args)), withKeyboard)
			}),

			"widthClass": scr("widthClass", func(args ...object.Value) object.Value {
				return object.NewString(viewportResolved(windowArg(args)).WidthClass)
			}),
			"isCompactWidth": scr("isCompactWidth", func(args ...object.Value) object.Value {
				return object.NewBoolean(viewportResolved(windowArg(args)).WidthClass == SizeCompact)
			}),
			"isTabletLayout": scr("isTabletLayout", func(args ...object.Value) object.Value {
				return object.NewBoolean(viewportResolved(windowArg(args)).WidthClass == SizeRegular)
			}),

			"onViewportChange": scr("onViewportChange", jsOnViewportChange),
			"offViewportChange": scr("offViewportChange", func(args ...object.Value) object.Value {
				if len(args) == 0 {
					return object.UndefinedSingleton
				}
				return jsOffViewportChange(args[0])
			}),

			"reportViewport": scr("reportViewport", jsReportViewport),
			"resetViewport":  scr("resetViewport", jsResetViewport),

			// 形态与尺寸类词表 (文档与校验共用)。
			"viewportModes": scr("viewportModes", func(args ...object.Value) object.Value {
				return object.NewArray([]object.Value{
					object.NewString(ViewportFullscreen), object.NewString(ViewportSplit),
					object.NewString(ViewportPIP), object.NewString(ViewportFreeform),
				})
			}),
		}
	})
}
