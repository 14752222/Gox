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

// 尺寸类 (iOS 的 size class; Android 用同样的词表达同一个意思)。
//
// **为什么是三档而不是两档**: 折叠屏"展开"这一态 (典型 600–840dp) 既不是手机
// (compact) 也不是平板 (expanded) —— 用手机布局会浪费掉一半屏幕, 用平板布局
// 又会让信息密度过低。Android 官方的大屏分界就在这里 (WindowManager /
// Material 的 600 / 840), 单折设备展开后几乎全部落在 medium 档。
//
// 代价 (docs/mobile-adaptation.md 有 breaking note): 折叠展开态从 "regular"
// 变成 "medium" —— 这是 **breaking**, 依赖字符串相等的脚本会坏。缓解办法是
// `isTabletLayout()` 的语义保持不变 (>= medium 即真, 见其定义), 按它写的分支
// 不受影响; `regularWidth` 也保留 (medium 时它为 true, 见 viewportToJS)。
const (
	SizeCompact  = "compact"  // < 600dp (手机竖屏)
	SizeMedium   = "medium"   // 600..840dp (折叠屏展开 / 小平板)
	SizeExpanded = "expanded" // > 840dp (平板横屏 / 展开的大折叠屏)
	// SizeRegular 保留给"宿主只报两档"的旧上报: 它表示"大", 归一化时映射成
	// expanded 的语义位 (见 normalizeSizeClass)。**新代码不要再用它做比较** ——
	// 拿它去比 `widthClass()` 的返回值在折叠展开态会全部落空。
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
	WidthClass     string  // "compact" | "medium" | "expanded" ("regular" = 旧两档上报)
	HeightClass    string  // 高度**仍是两档**: "compact" | "regular"
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

// viewportPatchMask 标记"本次上报**显式给到**了哪些无法用零值区分的字段"。
//
// 为什么需要它: Insets / Keyboard / MultiWindow 的零值都是**合法取值** (安全区
// 可以真的是 0, 键盘可以真的没弹, 多窗口可以真的是 false) —— 所以不能像 Mode /
// WidthClass 那样用 `if v.X == ""` 判断"没报"。少了这层标记, 一次只报键盘的
// 上报就会把先前报的安全区清成 0 (Android 正是在系统回调里分三次报的:
// insets 变了 / 键盘弹了 / 进分屏了)。
type viewportPatchMask uint8

const (
	patchInsets viewportPatchMask = 1 << iota
	patchKeyboard
	patchMultiWindow
	// patchAll 表示"我这份就是全量" —— ReportViewport 的公开语义。
	patchAll = patchInsets | patchKeyboard | patchMultiWindow
)

// ReportViewport 由宿主上报某个窗口的可视区域环境 (GUI 线程)。
//
// 合并语义是 **upsert**: 只覆盖本次给到的字段。这一条很关键 —— Android 会在
// 系统回调里分别报三件事 (insets 变了 / 键盘弹了 / 进分屏了), 若每次上报都是
// "整份替换", 那么"键盘弹起"那一次会把先前报的 insets 清成 0。
//
// 对 Go 调用方 (后端 / 平台层) 而言, 传进来的 Viewport 就是"全量", 所以这里走
// patchAll。脚本侧的 reportViewport 则按"实际出现了哪些键"逐字段上报 ——
// 见 reportViewportPatch 与 jsReportViewport。
func ReportViewport(win *Window, v Viewport) {
	reportViewportPatch(win, v, patchAll)
}

// reportViewportPatch 是带"显式字段掩码"的上报实现。
//
// mask 之外的字段一律沿用 prev 的旧值; mask 之内的字段即使是零值也照样写入
// (显式报 0 生效)。
func reportViewportPatch(win *Window, v Viewport, mask viewportPatchMask) {
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
	// 零值即合法取值的三个字段: 没显式报就沿用旧值 (不是"清成 0")。
	if mask&patchInsets == 0 {
		v.Insets = prev.Insets
	}
	if mask&patchKeyboard == 0 {
		v.Keyboard = prev.Keyboard
	}
	if mask&patchMultiWindow == 0 {
		v.MultiWindow = prev.MultiWindow
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
// 600dp / 840dp 这两个断点是 Android 官方的大屏分界 (WindowManager / Material
// 用同一套), iOS 的 regular 起点也在这附近。对一个自研运行时来说, 与其发明自己的
// 断点, 不如沿用"平台会怎么判"。这里只做**缺省推导**: 宿主报了 WidthClass 就以
// 宿主为准 (折叠屏某些状态下系统仍报 compact, 那种情况只有宿主知道)。
//
// 高度**仍是两档** (480dp): 三档的意义在宽度 (横向空间决定要不要加栏/加大留白),
// 而竖向分三档会让"折叠屏横过来"落进 medium 从而触发横向布局 —— 那正是最不想要
// 的结果。宽三档 / 高两档是故意的, 不是漏改。
func deriveSizeClasses(w, h int, scale float64) (string, string) {
	if scale <= 0 {
		scale = 1
	}
	dp := float64(w) / scale
	var wc string
	switch {
	case dp < 600:
		wc = SizeCompact
	case dp <= 840:
		wc = SizeMedium
	default:
		wc = SizeExpanded
	}
	hc := SizeRegular
	if float64(h)/scale < 480 {
		hc = SizeCompact
	}
	return wc, hc
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
	// 派生判断: 折叠/分屏适配的分支几乎都落在这几条上。
	//
	// **regularWidth 的语义是"宽档 >= medium"**(不是"== regular"): 三档化之后
	// 若还按等号判, 折叠屏展开时它会从 true 静默变成 false —— 旧脚本里
	// "平板才显示侧栏" 这类分支会突然消失, 且没有任何报错。保成"至少 medium"
	// 让旧代码在新增 medium 档时行为不变。
	o.SetProperty("compactWidth", object.NewBoolean(v.WidthClass == SizeCompact))
	o.SetProperty("mediumWidth", object.NewBoolean(v.WidthClass == SizeMedium))
	o.SetProperty("expandedWidth", object.NewBoolean(v.WidthClass == SizeExpanded || v.WidthClass == SizeRegular))
	o.SetProperty("regularWidth", object.NewBoolean(sizeClassRank(v.WidthClass) >= sizeClassRank(SizeMedium)))
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
	// 逐字段记录"这次显式报了哪些" —— 三个零值即合法的字段必须靠它区分
	// "报了个 0" 与 "没报" (见 viewportPatchMask)。四种平铺边与嵌套 insets
	// 任一出现即算报了 insets。
	var mask viewportPatchMask
	if objProp(opts, "top") != nil || objProp(opts, "right") != nil ||
		objProp(opts, "bottom") != nil || objProp(opts, "left") != nil {
		mask |= patchInsets
	}
	if objProp(opts, "keyboard") != nil {
		mask |= patchKeyboard
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
		mask |= patchInsets
	}
	if val := objProp(opts, "multiWindow"); val != nil {
		v.MultiWindow = nativeBool(val)
		mask |= patchMultiWindow
	}
	v.Mode = normalizeViewportMode(objPropStr(opts, "mode"))
	v.Stage = strings.ToLower(strings.TrimSpace(objPropStr(opts, "stage")))
	v.StageID = int(objPropNum(opts, "stageId"))
	v.SplitDirection = strings.ToLower(strings.TrimSpace(objPropStr(opts, "splitDirection")))
	v.SplitRatio = objPropNum(opts, "splitRatio")
	v.WidthClass = normalizeSizeClass(objPropStr(opts, "widthClass"))
	v.HeightClass = normalizeSizeClass(objPropStr(opts, "heightClass"))
	// 报了 mode 或 stage 就等于声明"我在分屏里" —— 与 gx/screen 的"报了姿态就等于
	// 声明这是折叠屏"同一条省事规则。这条推导也要算作显式报了 multiWindow,
	// 否则"报 mode=split"会被后面的 mask 兜底当成"没报"而丢掉。
	if v.Mode == ViewportSplit || v.Mode == ViewportPIP || v.Mode == ViewportFreeform || v.Stage != "" {
		v.MultiWindow = true
		mask |= patchMultiWindow
	}
	reportViewportPatch(win, v, mask)
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
//
// `SizeRegular` 原样保留: 它是**旧上报的词**, 宿主 (尤其 iOS 的 UITraitCollection
// 只有 compact/regular 两档) 今天仍然会报它。把它折成 expanded 会丢掉"宿主其实是
// 两档语义"这一事实, 而保留原值再让 `isTabletLayout()` 用 `!= compact` 判断,
// 两档与三档的宿主都能得到正确结果。
func normalizeSizeClass(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case SizeCompact:
		return SizeCompact
	case SizeMedium:
		return SizeMedium
	case SizeExpanded:
		return SizeExpanded
	case SizeRegular:
		return SizeRegular
	}
	return ""
}

// sizeClassRank 把尺寸类映射成有序档位, 供"至少是 X 档"这类比较用。
//
// 为什么要有它: 三档之后 `== SizeRegular` 这种写法必然漏掉 medium —— 而漏掉的
// 症状是"折叠屏展开时还算手机布局", 看起来像没适配。有了序数, 判定就能写成
// `rank(v.WidthClass) >= rank(SizeMedium)`, 与具体词表无关。
// regular (旧两档的"大") 等同于 expanded: 它表达的就是"够大, 不是手机"。
func sizeClassRank(s string) int {
	switch s {
	case SizeCompact:
		return 0
	case SizeMedium:
		return 1
	case SizeExpanded, SizeRegular:
		return 2
	}
	return -1 // "没报" —— 与任何档位都比不了
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

// ===== 折叠保留区的 JS 可见面 (批 E, 2026-10-01) =====
//
// 数据源是**窗口所在显示器**的 Display.Regions (gx/screen 的屏表)。这里不复用
// gfx/mobile 的同名函数 —— 那会形成 import 环 (gfx/mobile 已经 import 了 gfx),
// 所以判定在两边各有一份最小的实现。**两边的判据必须逐字一致**, 由
// gfx/mobile/fold.go 的注释与两处测试共同看住。

// displayRegionsOf 取窗口所在显示器的保留区 (拿不到显示器 → nil)。
func displayRegionsOf(win *Window) []DisplayRegion {
	d, ok := displayOfWorkWindow(win)
	if !ok {
		return nil
	}
	return d.Regions
}

// structuralRegions 是"设备结构上存在"的保留区 —— **忽略 Active**。
//
// 判据是 kind 语义而不是几何: division 是结构性的 (有折痕的机器永远有一条,
// 平展时尺寸为 0 而已), occlusion 才是几何性的 (屏下摄像头那块只在真有面积
// 时才占地方)。这样 `hasFold()` 就不会随折叠/平展 flip-flop —— 而那正是它
// 存在的意义 (列数不跟着姿态跳)。
//
// 与 gfx/mobile.StructuralRegions 同一判据 (那边不能在这里复用, 见上方注释)。
func structuralRegions(rs []DisplayRegion) []DisplayRegion {
	var out []DisplayRegion
	for _, r := range rs {
		if r.Kind == RegionDivision || (r.W > 0 && r.H > 0) {
			out = append(out, r)
		}
	}
	return out
}

// regionsByKindToJS 把保留区按 kind 分组输出成
// `{division:[...], occlusion:[...], all:[...]}`。
//
// 为什么要分组而不是只给一个数组: 脚本侧的避让策略几乎总是"只避折痕"或
// "两种都避", 每次自己 filter 一遍既啰嗦又容易写成 `r.kind === 'fold'`
// (内核的词是 "division")。三个键都是稳定存在的数组 (无内容时是空数组),
// 于是 `reservedRegions().division.length` 这类写法永远可用。
func regionsByKindToJS(list []DisplayRegion) object.Value {
	var div, occ []object.Value
	for _, r := range list {
		ro := regionToJS(r)
		switch r.Kind {
		case RegionDivision:
			div = append(div, ro)
		case RegionOcclusion:
			occ = append(occ, ro)
		}
	}
	o := object.NewObject()
	o.SetProperty("division", object.NewArray(div))
	o.SetProperty("occlusion", object.NewArray(occ))
	o.SetProperty("all", regionsToJS(list))
	return o
}

// hasFoldGo 报告设备结构上是否存在折痕 (忽略 active)。
func hasFoldGo(win *Window) bool {
	return len(structuralRegions(displayRegionsOf(win))) > 0
}

// layoutModeToJS 给出一个"该用哪种布局"的判定入口。
//
// **立场: 只给判定, 不改布局。** 框架不去替应用决定要不要分栏 —— 那取决于
// 页面自己的信息架构 (聊天界面该双栏, 沉浸式视频不该), 而框架猜错的代价是
// "某个页面莫名变成两栏"。这里只把三件事 (姿态 / 宽度档 / 有没有折痕) 合成
// 一个建议词, 让应用一行拿到。
//
//	suggested:
//	  "single" —— 单栏 (compact 或未折叠)
//	  "dual"   —— 双栏 (半折且有可用折痕: 折痕两侧是两块物理屏, 天然的双栏)
//	  "tablet" —— 平板布局 (宽档 >= medium 但不是半折: 空间大, 但一块连续屏)
func layoutModeToJS(win *Window) object.Value {
	v := viewportResolved(win)
	d, _ := displayOfWorkWindow(win)

	suggested := "single"
	switch {
	case d.Posture == postureHalfOpen && hasFoldGo(win):
		suggested = "dual"
	case sizeClassRank(v.WidthClass) >= sizeClassRank(SizeMedium):
		suggested = "tablet"
	}
	o := object.NewObject()
	nativeSet(o, "posture", d.Posture)
	nativeSet(o, "widthClass", v.WidthClass)
	o.SetProperty("foldAware", object.NewBoolean(hasFoldGo(win)))
	nativeSet(o, "suggested", suggested)
	return o
}

// regionToJS 是单条保留区的字段出口 (regionsToJS 与分组输出共用同一份字段表,
// 免得两处漂移)。
func regionToJS(r DisplayRegion) object.Value {
	ro := object.NewObject()
	ro.SetProperty("id", object.NewString(r.ID))
	ro.SetProperty("kind", object.NewString(r.Kind))
	ro.SetProperty("x", object.NewNumber(float64(r.X)))
	ro.SetProperty("y", object.NewNumber(float64(r.Y)))
	ro.SetProperty("width", object.NewNumber(float64(r.W)))
	ro.SetProperty("height", object.NewNumber(float64(r.H)))
	ro.SetProperty("active", object.NewBoolean(r.Active))
	return ro
}

// resetViewportStateForTest 清空上报与回调。
func resetViewportStateForTest() {
	nativeMu.Lock()
	viewports = map[string]Viewport{}
	viewportHooks = nil
	viewportRev++
	viewportEnvGet, viewportEnvSet = nil, nil
	breakpointCustom = nil
	nativeMu.Unlock()
}

// resetBreakpointsForTest 把断点表复位 (用例之间不许串味)。
func resetBreakpointsForTest() {
	nativeMu.Lock()
	breakpointCustom = nil
	nativeMu.Unlock()
}

// ===== 断点系统 (M4 自适应断点布局, 2026-10-02) =====
//
// ## 与尺寸类的关系 (为什么不是"第二套阈值")
//
// 尺寸类 (SizeCompact/Medium/Expanded, 见 deriveSizeClasses) 是**物理分类**:
// 它回答"这块窗口在平台眼里算什么" (手机 / 折叠展开 / 平板), 阈值 600 / 840dp
// 直接抄 Android WindowManager / Material 的官方分界, 宿主报了 WidthClass 就以
// 宿主为准。
//
// 断点是**命名阈值**: 它回答"脚本想在哪几个宽度上换布局", 是应用层的语言
// (sm / md / lg / xl 这种叫法比 compact/medium/expanded 更适合表达"窄栏 /
// 常规 / 宽栏 / 超宽")。默认表刻意**复用同一组数字** (md:600 / lg:840), 于是
// 默认情况下两者互相印证而不是打架:
//
//	sm  (0..599)   ↔ compact
//	md  (600..839) ↔ medium
//	lg  (840..1199)┐
//	xl  (≥1200)    ┘↔ expanded (断点把 expanded 再切一刀, 给超宽桌面留出 xl)
//
// **边界差 1dp 的说明**: deriveSizeClasses 把 840dp 判成 medium (闭区间),
// 而断点表 md:600 / lg:840 按"下界"语义把 840dp 归到 lg。两处阈值数值完全
// 一致, 只是"命中的那一格"不同 —— 这是"物理分类"与"命名阈值"的语义差异, 不是
// 两套互相矛盾的阈值表。若要严格对齐, `setBreakpoints({sm:0, md:600, lg:841})`
// 即可 (断点本来就是可覆盖的)。历史包袱更少的做法是把 lg 设成 840 的下一格,
// 但那会让"lg 门槛是 840"这条常识失效, 所以默认表选择直觉优先。
//
// 断点随**窗口宽度**走 (不是屏幕宽度): 分屏 / 自由窗口下窗口才是布局的真相 ——
// 与 gx/viewport 的立场一致 ("gx/screen 答设备, gx/viewport 答窗口")。

// breakpointEntry 是断点表里的一档: 名字 + 该档的**下界** (dp)。
type breakpointEntry struct {
	name string
	dp   float64
}

// defaultBreakpointTable 是默认断点表 (dp)。顺序按 dp 升序 —— 求值依赖这个序。
var defaultBreakpointTable = []breakpointEntry{
	{"sm", 0}, {"md", 600}, {"lg", 840}, {"xl", 1200},
}

// breakpointCustom 是宿主/脚本覆盖过的断点表 (nil/空 = 用默认表)。
// 与 viewports 共用 nativeMu (同文件、同一条纪律, 见文件头"锁的复用说明")。
var breakpointCustom []breakpointEntry

// breakpointTableLocked 取"当前生效表"的副本 (调用时须持有 nativeMu)。
func breakpointTableLocked() []breakpointEntry {
	if len(breakpointCustom) > 0 {
		return append([]breakpointEntry(nil), breakpointCustom...)
	}
	return append([]breakpointEntry(nil), defaultBreakpointTable...)
}

// breakpointTableSnapshot 取当前生效断点表 (加锁)。
func breakpointTableSnapshot() []breakpointEntry {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return breakpointTableLocked()
}

// breakpointNameAt 求 dp 落在哪一档: 取"下界 <= dp 中最大的那一档"。
//
// dp 低于所有下界时 (只可能出现在自定义表把最小档抬到了 0 以上的情况) 归到
// 最小档, 而不是返回空串 —— "没有断点命中"对调用方没有任何可用的含义, 而最小档
// 兜底与 CSS 的 "mobile first" 直觉一致。
func breakpointNameAt(dp float64, table []breakpointEntry) string {
	name := ""
	best := -1.0
	for _, b := range table {
		if dp >= b.dp && b.dp >= best {
			best = b.dp
			name = b.name
		}
	}
	if name == "" && len(table) > 0 {
		name = table[0].name
	}
	return name
}

// breakpointThreshold 取某一档的下界 (未知档 → false)。
func breakpointThreshold(name string, table []breakpointEntry) (float64, bool) {
	for _, b := range table {
		if b.name == name {
			return b.dp, true
		}
	}
	return 0, false
}

// windowWidthDP 取窗口客户区宽度换算成 dp (拿不到窗口则退回 0)。
//
// 换算基准是**窗口所在显示器**的缩放 (与 deriveSizeClasses 同一口径), 而
// displayOfWorkWindow 在后端不支持时退化为虚拟屏 —— 此时 scale=1, dp=px。
func windowWidthDP(win *Window) float64 {
	w, _ := windowPixelSize(win)
	scale := 1.0
	if d, ok := displayOfWorkWindow(win); ok && d.Scale > 0 {
		scale = d.Scale
	}
	return float64(w) / scale
}

// currentBreakpointName 求窗口当前命中哪一档断点。
func currentBreakpointName(win *Window) string {
	return breakpointNameAt(windowWidthDP(win), breakpointTableSnapshot())
}

// breakpointAbove / below / between 是三个区间判定 (未知档名 → false)。
//
// between 用**半开区间** [a 的下界, b 的下界): 于是 between("md","lg") 精确
// 等于"当前档是 md"那一格, 与 breakpointNameAt 的语义逐字对齐。a 的下界大于
// b 时自动交换 (参数顺序无关)。
func breakpointAbove(name string, win *Window) bool {
	thr, ok := breakpointThreshold(name, breakpointTableSnapshot())
	return ok && windowWidthDP(win) >= thr
}

func breakpointBelow(name string, win *Window) bool {
	thr, ok := breakpointThreshold(name, breakpointTableSnapshot())
	return ok && windowWidthDP(win) < thr
}

func breakpointBetween(a, b string, win *Window) bool {
	table := breakpointTableSnapshot()
	lo, okA := breakpointThreshold(a, table)
	hi, okB := breakpointThreshold(b, table)
	if !okA || !okB {
		return false
	}
	if lo > hi {
		lo, hi = hi, lo
	}
	dp := windowWidthDP(win)
	return dp >= lo && dp < hi
}

// breakpointMatch 实现 matchBreakpoint({sm:val, md:val, …})。
//
// 取值规则: 在"给了值且是合法档名"的键里, 取**下界 <= dp 中最大的一档**的值;
// 一个都没命中 (比如只给了 md/lg 而窗口在 sm) 返回 undefined —— 让"没覆盖到"
// 显式暴露, 而不是悄悄退回一个不相关的档。
func breakpointMatch(obj *object.Object, win *Window) object.Value {
	table := breakpointTableSnapshot()
	dp := windowWidthDP(win)
	best := -1.0
	var picked object.Value
	for name, desc := range obj.Properties {
		thr, ok := breakpointThreshold(name, table)
		if !ok || dp < thr {
			continue
		}
		if thr >= best {
			best = thr
			picked = desc.Value
		}
	}
	if picked == nil {
		return object.UndefinedSingleton
	}
	return picked
}

// breakpointsToJS 把断点表输出成普通对象 (name → dp)。
func breakpointsToJS(table []breakpointEntry) object.Value {
	o := object.NewObject()
	for _, b := range table {
		o.SetProperty(b.name, object.NewNumber(b.dp))
	}
	return o
}

// jsSetBreakpoints 实现 setBreakpoints({sm:0, md:600, …}): 整表替换。
//
// 校验口径 (与仓库其它上报一致): 名字非空、数值 >= 0、至少一档。不合规的项
// 静默跳过; 若最后一项都不剩则**保持原表不变并告警** —— 把表清空会让
// breakpoint() 永远返回空串, 那比"这次设置没生效"更难排查。
func jsSetBreakpoints(args ...object.Value) object.Value {
	var entries []breakpointEntry
	if o, ok := argOrNil(args).(*object.Object); ok {
		for name, desc := range o.Properties {
			if name == "" {
				continue
			}
			if n, ok := desc.Value.(*object.Number); ok && n.Value >= 0 {
				entries = append(entries, breakpointEntry{name: name, dp: n.Value})
			}
		}
	}
	if len(entries) == 0 {
		recordWarn("gx/viewport setBreakpoints: 没有可用的断点项, 保持原表不变")
		return object.UndefinedSingleton
	}
	// 按 dp 升序排 (求值依赖顺序); 同值保持稳定 (Go sort.SliceStable)。
	sortBreakpoints(entries)
	nativeMu.Lock()
	breakpointCustom = entries
	nativeMu.Unlock()
	notifyViewportChanged()
	return object.UndefinedSingleton
}

// sortBreakpoints 按 dp 升序做稳定插入排序 (档数通常 <10, 插入排序足够)。
func sortBreakpoints(entries []breakpointEntry) {
	for i := 1; i < len(entries); i++ {
		for j := i; j > 0 && entries[j].dp < entries[j-1].dp; j-- {
			entries[j], entries[j-1] = entries[j-1], entries[j]
		}
	}
}

// jsResetBreakpoints 恢复默认断点表。
func jsResetBreakpoints(args ...object.Value) object.Value {
	nativeMu.Lock()
	breakpointCustom = nil
	nativeMu.Unlock()
	notifyViewportChanged()
	return object.UndefinedSingleton
}

// NotifyViewportChanged 让宿主/后端宣告"窗口可视环境变了" —— 典型场景是
// **窗口 resize**: 尺寸类与断点都取决于窗口宽度, 而 resize 不经过
// ReportViewport (宿主没报任何字段)。所以后端在 WM_SIZE / windowDidResize /
// ConfigureNotify 里 (经 gfx.Post) 调它, 让 useBreakpoint/useViewport 这类
// 取值函数重算。它**不改任何数据**, 只抬版本号 + 派发订阅。
//
// **必须在 GUI 线程调用** (会跑脚本回调), 平台回调先 gfx.Post 转投。
func NotifyViewportChanged() { notifyViewportChanged() }

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
			"isMediumWidth": scr("isMediumWidth", func(args ...object.Value) object.Value {
				return object.NewBoolean(viewportResolved(windowArg(args)).WidthClass == SizeMedium)
			}),
			"isExpandedWidth": scr("isExpandedWidth", func(args ...object.Value) object.Value {
				c := viewportResolved(windowArg(args)).WidthClass
				return object.NewBoolean(c == SizeExpanded || c == SizeRegular)
			}),
			// isTabletLayout 的语义**刻意保持不变**: "不是手机竖屏" (>= medium)。
			// 三档之前它等价于 == regular, 三档之后若仍写等号, 折叠屏展开态会
			// 静默变成 false —— 那正是这次改动最危险的回归点。
			"isTabletLayout": scr("isTabletLayout", func(args ...object.Value) object.Value {
				return object.NewBoolean(sizeClassRank(viewportResolved(windowArg(args)).WidthClass) >= sizeClassRank(SizeMedium))
			}),

			// ---- 折叠保留区 (批 E) ----
			"reservedRegions": scr("reservedRegions", func(args ...object.Value) object.Value {
				return regionsByKindToJS(displayRegionsOf(windowArg(args)))
			}),
			// useReservedRegions 的可订阅版: **同时**读两个版本号 —— 数据本身
			// 在 gx/screen 的屏表下 (姿态/保留区变化走 envSignal), 但窗口环境
			// (换屏/尺寸类) 变化走 viewportEnvSignal。只读一个是常见疏漏, 症状是
			// "折一下界面不更新, 非得再转个屏"。
			"useReservedRegions": scr("useReservedRegions", func(args ...object.Value) object.Value {
				win := windowArg(args)
				return object.NewBuiltin("useReservedRegions", func(args ...object.Value) object.Value {
					if g := viewportEnvSignal(); g != nil {
						object.CallFunction(g, nil)
					}
					if g := envSignal(); g != nil {
						object.CallFunction(g, nil)
					}
					return regionsByKindToJS(displayRegionsOf(win))
				})
			}),
			// hasFold: **结构性**信号, 不随折叠/平展 flip-flop (见 structuralRegions)。
			"hasFold": scr("hasFold", func(args ...object.Value) object.Value {
				return object.NewBoolean(hasFoldGo(windowArg(args)))
			}),
			"layoutMode": scr("layoutMode", func(args ...object.Value) object.Value {
				return layoutModeToJS(windowArg(args))
			}),
			"useLayoutMode": scr("useLayoutMode", func(args ...object.Value) object.Value {
				win := windowArg(args)
				return object.NewBuiltin("useLayoutMode", func(args ...object.Value) object.Value {
					if g := viewportEnvSignal(); g != nil {
						object.CallFunction(g, nil)
					}
					if g := envSignal(); g != nil {
						object.CallFunction(g, nil)
					}
					return layoutModeToJS(win)
				})
			}),

			"onViewportChange": scr("onViewportChange", jsOnViewportChange),
			"offViewportChange": scr("offViewportChange", func(args ...object.Value) object.Value {
				if len(args) == 0 {
					return object.UndefinedSingleton
				}
				return jsOffViewportChange(args[0])
			}),

			// ---- 断点系统 (M4) ----
			"breakpoints": scr("breakpoints", func(args ...object.Value) object.Value {
				return breakpointsToJS(breakpointTableSnapshot())
			}),
			"breakpoint": scr("breakpoint", func(args ...object.Value) object.Value {
				return object.NewString(currentBreakpointName(windowArg(args)))
			}),
			// useBreakpoint(): 取值函数 + 订阅。窗口宽度变化 (resize) 或宿主
			// 上报 viewport 都会让读它的函数 prop / 函数子节点重算。
			"useBreakpoint": scr("useBreakpoint", func(args ...object.Value) object.Value {
				w := windowArg(args)
				return object.NewBuiltin("useBreakpoint", func(args ...object.Value) object.Value {
					if g := viewportEnvSignal(); g != nil {
						object.CallFunction(g, nil) // 读一次 = 订阅一次
					}
					return object.NewString(currentBreakpointName(w))
				})
			}),
			"above": scr("above", func(args ...object.Value) object.Value {
				return object.NewBoolean(breakpointAbove(object.ToString(argOrNil(args)), windowArg(args[1:])))
			}),
			"below": scr("below", func(args ...object.Value) object.Value {
				return object.NewBoolean(breakpointBelow(object.ToString(argOrNil(args)), windowArg(args[1:])))
			}),
			"between": scr("between", func(args ...object.Value) object.Value {
				return object.NewBoolean(breakpointBetween(object.ToString(argOrNil(args)),
					object.ToString(argOrNil(args[1:])), windowArg(args[2:])))
			}),
			// matchBreakpoint({sm: "窄", lg: "宽"}): 返回命中档的值。
			"matchBreakpoint": scr("matchBreakpoint", func(args ...object.Value) object.Value {
				obj, ok := argOrNil(args).(*object.Object)
				if !ok {
					return object.UndefinedSingleton
				}
				return breakpointMatch(obj, windowArg(args[1:]))
			}),
			"setBreakpoints":   scr("setBreakpoints", jsSetBreakpoints),
			"resetBreakpoints": scr("resetBreakpoints", jsResetBreakpoints),

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
