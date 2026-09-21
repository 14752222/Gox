package gfx

import (
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
)

// ===== gx/screen: 显示器 / 屏幕信息 / 折叠姿态 (多屏与折叠屏适配的地基) =====
//
// 这个模块解决的是 agent_doc/gui-responsive-screen-options.md 里 G-b / G-d 那一类
// 缺口: 脚本此前完全看不见"我在哪块屏幕上、多大、缩放多少、是不是折着的"。
// 响应式布局的全部机制 (signal + 条件渲染 + 函数 prop) 早就到位, 缺的只是
// "屏幕尺寸/姿态成为信号" 这一步 —— 本文件补上它, 并把它做成 gx/router
// 折叠适配的输入 (路由重建由 gx/router 消费, 见 router_view.go 的双栏部分)。
//
// ## 三层结构 (与仓库的既有分层一致)
//
//	Display 数据模型   —— 纯 Go 结构, 不依赖 VM; 显示器枚举/缩放/折痕/分段
//	displayProvider    —— Surface 的**可选能力** (与 windowController /
//	                      capturer / nativeDialogHost 同一模式: 不扩 Surface
//	                      接口, 类型断言落空即退化为"单块虚拟屏")
//	gx/screen 模块     —— JS 可见面: screens()/useScreen()/posture()/…
//
// ## 为什么没有"桌面折叠屏检测"
//
// Windows / X11 上**没有**可用的折叠姿态查询 API (WinRT 的
// Windows.Devices.Sensors 不在纯 syscall + 零 cgo 的可达范围内)。所以本模块
// 的立场是: 框架提供**姿态通道**, 而不是假装能检测姿态。
//
//   - 移动宿主 (Android / 鸿蒙 / iOS, 见 agent_doc/mobile-port-plan.md) 从系统
//     API 读到姿态后调 reportPosture 上报;
//   - 桌面上的折叠屏模拟器 / 开发者工具 / 自动化测试同样调 reportPosture;
//   - 没有任何上报时姿态恒为 "flat" —— 与今天的桌面行为完全一致 (零影响)。
//
// 这条纪律让"折叠态切换"这条链路**可以在桌面单测里完整验证** (不需要真机),
// 也避免了"框架猜错姿态导致界面乱跳"的风险。

// ===== 数据模型 =====

// DisplayHinge 是折叠屏的折痕 (铰链) 区域。
type DisplayHinge struct {
	X, Y, W, H  int
	Orientation string // "vertical" = 左右折 (铰链是竖条); "horizontal" = 上下折
}

// DisplayRegion 是折叠屏的一段可用面板 (第一屏 / 第二屏 / 展开后的整块屏)。
type DisplayRegion struct {
	ID         string
	X, Y, W, H int
}

// Display 是一台显示器, 或折叠屏在某一姿态下的一"块"逻辑显示。
type Display struct {
	ID      string
	Name    string
	X, Y    int // 在虚拟桌面坐标系里的位置 (多屏拼接时才有意义)
	W, H    int
	WorkX   int // 工作区 (排除任务栏/状态栏后的可用区域)
	WorkY   int
	WorkW   int
	WorkH   int
	Scale   float64 // 设备像素比 (1.0 / 1.25 / 1.5 / 2.0 …)
	Primary bool
	// Foldable + Posture + Hinge 是折叠屏三件套。非折叠屏恒为
	// false / "flat" / nil。
	Foldable bool
	Posture  string
	Hinge    *DisplayHinge
	Regions  []DisplayRegion
}

// 折叠姿态取值 (与 Android Jetpack WindowManager 的 posture 词汇对齐)。
const (
	postureFlat     = "flat"      // 平展 (或非折叠屏)
	postureHalfOpen = "half-open" // 半折: 屏幕被折痕分成两段, 双栏的用武之地
	postureFolded   = "folded"    // 合起: 只剩一段可用
	postureUnknown  = "unknown"
)

// normalizedPosture 把脚本/宿主给的姿态归一化 (不认识的值 → "unknown")。
func normalizedPosture(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case postureFlat, "":
		return postureFlat
	case postureHalfOpen, "halfopen", "half_open":
		return postureHalfOpen
	case postureFolded:
		return postureFolded
	default:
		return postureUnknown
	}
}

// displayProvider 是 Surface 的**可选能力**: 多显示器枚举 + "窗口在哪块屏上"。
//
// 为什么把 DisplayOf 收在 Surface 上而不是让 gx/screen 自己拿窗口坐标去算:
// 只有后端知道 hwnd 与显示器句柄的对应关系 (win32 的 MonitorFromWindow 会把
// "横跨两块屏的窗口"归到**主显示器**, 而用坐标算的话规则完全不同)。把判定
// 留在后端, 是"平台细节不进内核"的延续。
type displayProvider interface {
	Displays() []Display
	DisplayOf(s Surface) (string, bool)
}

// ===== 环境状态 (显示器表 + 版本号 + 信号) =====
//
// 为什么要一个"版本号信号"而不是把整张显示器表做成 signal: 脚本真正关心的是
// "变了"这件事 (尺寸/姿态/所在屏), 而表本身结构较大。版本号 + 取值函数
// (useScreen/usePosture) 的组合既省事又天然可订阅:
//
//	var r = usePosture();     // 取值函数
//	<text>{() => r()}</text>  // 姿态一变, 这行文本跟着变
//
// 版本号全局唯一 (不是每台屏一个), 因为它只用来"触发重算", 用不着分辨是谁变了
// —— 上一次的读数已经记在调用方自己的 signal 里了。
var (
	screenMu       sync.Mutex
	screenRevision int            // 环境版本 (变化时 +1)
	screenOverride []Display      // 宿主上报/测试注入的显示器表 (非 nil 时优先于后端)
	screenEnvGet   object.Value   // 惰性构造的 signal getter
	screenEnvSet   object.Value   // 对应的 setter
	screenHooks    []object.Value // onDisplayChange 注册的回调
)

// envSignal 返回环境版本号的 signal getter (惰性建)。
//
// 经 gx/solid 造 signal 而不是自己写观察者: 路由/脚本侧要的是"读它就是订阅",
// 而复用 gx/solid 意味着这套订阅与 createEffect / 函数 prop / 函数子节点
// 全都能自动接通 —— 没有第二套响应式。
//
// gx/solid 未注册 (纯 Go 单测/无头宿主) 时返回 nil, 调用方 (router) 退化为
// "只读一次快照" —— 与 runEffect 的降级口径一致。
func envSignal() object.Value {
	screenMu.Lock()
	defer screenMu.Unlock()
	return envSignalLocked()
}

func envSignalLocked() object.Value {
	if screenEnvGet != nil {
		return screenEnvGet
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
	screenEnvGet, screenEnvSet = arr.Elements[0], arr.Elements[1]
	return screenEnvGet
}

// screenEnvRevision 读当前环境版本 (不订阅)。
func screenEnvRevision() int {
	screenMu.Lock()
	defer screenMu.Unlock()
	return screenRevision
}

// EnvRevisionForTest 报告当前环境版本 (测试用: 断言"变更确实抬高了版本")。
func EnvRevisionForTest() int { return screenEnvRevision() }

// bumpEnvLocked 抬高版本号并通知 (调用时必须持有 screenMu; 通知在解锁后跑,
// 因为回调是脚本函数, 里面有重入本模块的自由 —— 持锁调脚本是死锁配方)。
//
// 通知是**同步**的: 脚本在 GUI 线程串行执行, 而回调里常见的动作就是写 signal
// (→ 标脏 → 本帧重绘), 同步调用省掉一次跨轮次的延迟。代价是回调抛错必须被
// 收走 (recordWarn), 否则一次笔误会让整帧挂掉。
func notifyEnvChanged() {
	screenMu.Lock()
	screenRevision++
	set := screenEnvSet
	hooks := append([]object.Value(nil), screenHooks...)
	screenMu.Unlock()

	if set != nil {
		object.CallFunction(set, nil, object.NewNumber(float64(screenRevision)))
	}
	for _, fn := range hooks {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/screen onDisplayChange: 回调抛错: %v", err)
		}
	}
}

// NotifyDisplaysChanged 让宿主/后端宣告"显示器环境变了" (插拔屏 / DPI 变化 /
// 姿态变化 / 窗口换屏)。
//
// **必须在 GUI 线程调用** (或在别的 goroutine 里经 gfx.Post 转投): 它最终会
// 执行脚本回调。平台侧的正确写法见 gfx/win32/display.go 的 WM_DISPLAYCHANGE
// 分支 —— WndProc 不直接调它, 而是 Post 一个任务。
func NotifyDisplaysChanged() {
	notifyEnvChanged()
}

// ===== 显示器枚举 =====

// allDisplays 取当前显示器表, 优先级: 宿主上报 > 后端枚举 > 单块虚拟屏。
//
// 为什么上报表是"整表覆盖"而不是"打补丁": 上报的语义是"我知道的显示环境是
// 这样" (移动宿主读系统 API, 桌面模拟器/开发者工具声明一块折叠屏)。一旦上报
// 过, 那张表就是权威 —— 中途再与后端枚举结果做合并, 会让"这块屏是折叠的"
// 这种信息在不该丢的时候丢。要交还给后端就调 resetDisplays()。
func allDisplays() []Display {
	screenMu.Lock()
	ov := append([]Display(nil), screenOverride...)
	screenMu.Unlock()
	if len(ov) > 0 {
		return ov
	}
	if list := providerDisplays(); len(list) > 0 {
		return list
	}
	return []Display{virtualDisplay(0, 0)}
}

// providerDisplays 取后端枚举出的显示器 (后端不支持 → nil)。
func providerDisplays() []Display {
	appMu.Lock()
	f := defaultFactory
	appMu.Unlock()
	if f == nil {
		return nil
	}
	dp, ok := f.(displayProvider)
	if !ok {
		return nil
	}
	return dp.Displays()
}

// virtualDisplay 是"没有任何后端信息"时的单块虚拟屏 (尺寸取自活跃窗口,
// 无窗口时给一个桌面应用常见的缺省值)。
func virtualDisplay(w, h int) Display {
	if a := currentApp(); a != nil && a.surface != nil {
		if sw, sh := a.surface.Size(); sw > 0 && sh > 0 {
			w, h = sw, sh
		}
	}
	if w <= 0 || h <= 0 {
		w, h = 400, 300
	}
	return Display{
		ID: "virtual-0", Name: "virtual",
		W: w, H: h, WorkW: w, WorkH: h,
		Scale: 1, Primary: true, Posture: postureFlat,
	}
}

// displayOfSurface 返回窗口所在显示器的 ID (查不到 → 主显示器的 ID)。
func displayOfSurface(s Surface) string {
	if s != nil && defaultFactory != nil {
		if dp, ok := defaultFactory.(displayProvider); ok {
			if id, ok := dp.DisplayOf(s); ok && id != "" {
				return id
			}
		}
	}
	return primaryDisplayID()
}

func primaryDisplayID() string {
	for _, d := range allDisplays() {
		if d.Primary {
			return d.ID
		}
	}
	if list := allDisplays(); len(list) > 0 {
		return list[0].ID
	}
	return ""
}

// findDisplay 按 ID 找显示器 (空 ID → 第一台)。
func findDisplay(id string) (Display, bool) {
	list := allDisplays()
	if len(list) == 0 {
		return Display{}, false
	}
	if id == "" {
		return list[0], true
	}
	for _, d := range list {
		if d.ID == id {
			return d, true
		}
	}
	return Display{}, false
}

// displayOfWorkWindow 返回某个窗口所在显示器 (win 为 nil → 活跃窗口)。
func displayOfWorkWindow(win *Window) (Display, bool) {
	var s Surface
	if win != nil {
		s = win.Surface()
	} else if a := currentApp(); a != nil {
		a.mu.Lock()
		s = a.surface
		a.mu.Unlock()
	}
	return findDisplay(displayOfSurface(s))
}

// displaySplitRatio 给出折叠屏在 half-open 姿态下的横向/纵向分割比例
// (第一屏占总长的比例)。给 gx/router 的双栏布局用。
//
// 退化规则: 没有 hinge / hinge 尺寸不合理 → 0.5 (等分); 比例钳到 [0.2, 0.8],
// 免得一条写错的折痕把一栏压成 1 像素。
func displaySplitRatio(d Display) float64 {
	if d.Hinge == nil || !d.Foldable {
		return 0.5
	}
	// 分母是**显示器**的长度, 不是折痕自身的长度: hinge 的 (x,y) 是折痕在
	// 显示器坐标系里的位置 (左栏 = 0..x), 拿 hinge.W 当分母会得到接近 1 的
	// 荒谬比例 —— 早期版本正是这么写的, 症状是"折了之后左栏被挤成一条"。
	var total, at int
	if displayHingeOrientation(d) == "vertical" {
		total, at = d.W, d.Hinge.X
	} else {
		total, at = d.H, d.Hinge.Y
	}
	if total <= 0 {
		return 0.5
	}
	r := float64(at) / float64(total)
	if r < 0.2 {
		r = 0.2
	}
	if r > 0.8 {
		r = 0.8
	}
	return r
}

// displayHingeOrientation 报告折痕方向 (无 hinge → "vertical" 即左右折,
// 这是折叠屏最常见的形态)。
func displayHingeOrientation(d Display) string {
	if d.Hinge == nil {
		return "vertical"
	}
	if d.Hinge.Orientation == "horizontal" {
		return "horizontal"
	}
	return "vertical"
}

// ===== 宿主上报 =====

// reportPostureGo 处理 reportPosture(opts) 的 Go 侧实现。
//
// 它做两件事: (1) 把上报的显示器写进 screenOverride (upsert);
// (2) 抬高环境版本并通知 —— 于是所有订阅者 (含 gx/router 的视图) 当场重算。
func reportPostureGo(args ...object.Value) object.Value {
	opts, ok := args[0].(*object.Object)
	if !ok {
		return object.NewTypeError("reportPosture: 需要 opts 对象, 如 {posture, hinge}")
	}
	id := objPropStr(opts, "display")
	if id == "" {
		// 不指定显示器: 作用于当前窗口所在的那块 (再退化为第一块)。
		id = displayOfSurface(nil)
	}

	screenMu.Lock()
	// 先从"当前生效表"里取一份底稿 (可能是后端枚举结果), 再叠加本次上报。
	// 注意区分两件事: "以前上报过"(prevOverride) 与 "当前表非空" —— 后者
	// 永远为真 (没有信息时内核会造一块虚拟屏), 混用会让"第一次上报定义整张表"
	// 这条规则永远不生效。
	prevOverride := len(screenOverride) > 0
	base := screenOverride
	if !prevOverride {
		screenMu.Unlock()
		base = allDisplays()
		screenMu.Lock()
	}
	// 只有"既没有过上报, 后端也说不出有哪几块屏"时, 本次上报才定义整张表 ——
	// 那种环境下虚拟屏只是占位, 把它留在表里会让"窗口所在显示器"永远解析到
	// 虚拟屏, 上报的姿态读不到 (折叠双栏静默失效)。有后端枚举时底稿取全表:
	// 上报只改其中一块, 其余屏照旧。
	replaceTable := !prevOverride && len(providerDisplays()) == 0
	out := append([]Display(nil), base...)
	idx := -1
	for i := range out {
		if out[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		// 上报了一块表里没有的屏。**第一次上报时直接换掉整张表**: 宿主说
		// "我这块屏叫 fold-0", 意思就是"我的显示环境是这样" —— 此时把内核
		// 自动推导出的虚拟屏留在表里, 会让"窗口所在显示器"仍然解析到虚拟屏,
		// 于是上报的姿态永远读不到 (折叠双栏静默失效)。
		// 已经有过上报时则是"再加一块屏" (模拟器/多屏宿主), 追加即可。
		if replaceTable {
			out = nil
		}
		out = append(out, Display{ID: id, Name: id, Scale: 1})
		idx = len(out) - 1
	}
	d := out[idx]
	if v := objPropStr(opts, "posture"); v != "" {
		d.Posture = normalizedPosture(v)
	} else if d.Posture == "" {
		d.Posture = postureFlat
	}
	if b, ok := objProp(opts, "foldable").(*object.Boolean); ok {
		d.Foldable = b.Value
	} else if objProp(opts, "foldable") == nil {
		// 报了姿态就等于声明了这是折叠屏 (否则 hinge/posture 无从谈起)
		if d.Posture != postureFlat {
			d.Foldable = true
		}
	}
	if n := objPropNum(opts, "width"); n > 0 {
		d.W = int(n)
		if d.WorkW == 0 {
			d.WorkW = d.W
		}
	}
	if n := objPropNum(opts, "height"); n > 0 {
		d.H = int(n)
		if d.WorkH == 0 {
			d.WorkH = d.H
		}
	}
	if h := objProp(opts, "hinge"); h != nil {
		if ho, ok := h.(*object.Object); ok {
			d.Hinge = &DisplayHinge{
				X: int(objPropNum(ho, "x")), Y: int(objPropNum(ho, "y")),
				W: int(objPropNum(ho, "w")), H: int(objPropNum(ho, "h")),
				Orientation: objPropStr(ho, "orientation"),
			}
			if d.Hinge.Orientation == "" {
				d.Hinge.Orientation = "vertical"
			}
			d.Foldable = true
		}
	}
	// 折痕**不随姿态清除**: 折痕是设备的几何属性, 不是姿态的属性。宿主常见的
	// 上报方式是"只改姿态" (reportPosture({posture:"flat"}) / {posture:"half-open"}),
	// 若在回到平展时把折痕丢掉, 下一次半折就只能等分 (0.5), 用户看到的是
	// "折回去再折回来, 分栏比例变了"。是否分栏由姿态 (dualActive) 决定, 折痕
	// 只负责"怎么分"。
	if regs, ok := objProp(opts, "regions").(*object.Array); ok {
		d.Regions = nil
		for _, r := range regs.Elements {
			ro, ok := r.(*object.Object)
			if !ok {
				continue
			}
			d.Regions = append(d.Regions, DisplayRegion{
				ID: objPropStr(ro, "id"),
				X:  int(objPropNum(ro, "x")), Y: int(objPropNum(ro, "y")),
				W: int(objPropNum(ro, "w")), H: int(objPropNum(ro, "h")),
			})
		}
	}
	out[idx] = d
	// 一定保证有一块主屏: 否则 primaryDisplayID 落空, "窗口在哪块屏上"就
	// 无法回答 (虚拟屏被换掉之后尤其容易出现)。
	hasPrimary := false
	for _, x := range out {
		if x.Primary {
			hasPrimary = true
			break
		}
	}
	if !hasPrimary && len(out) > 0 {
		out[0].Primary = true
	}
	screenOverride = out
	screenMu.Unlock()

	notifyEnvChanged()
	return object.UndefinedSingleton
}

// resetDisplaysGo 清空宿主上报 (测试/模拟器退出时回到后端真实枚举)。
func resetDisplaysGo(args ...object.Value) object.Value {
	screenMu.Lock()
	screenOverride = nil
	screenMu.Unlock()
	notifyEnvChanged()
	return object.UndefinedSingleton
}

// ===== JS 对象构造 =====

func displayToJS(d Display) object.Value {
	o := object.NewObject()
	o.SetProperty("id", object.NewString(d.ID))
	o.SetProperty("name", object.NewString(d.Name))
	o.SetProperty("x", object.NewNumber(float64(d.X)))
	o.SetProperty("y", object.NewNumber(float64(d.Y)))
	o.SetProperty("width", object.NewNumber(float64(d.W)))
	o.SetProperty("height", object.NewNumber(float64(d.H)))
	o.SetProperty("workX", object.NewNumber(float64(d.WorkX)))
	o.SetProperty("workY", object.NewNumber(float64(d.WorkY)))
	o.SetProperty("workWidth", object.NewNumber(float64(d.WorkW)))
	o.SetProperty("workHeight", object.NewNumber(float64(d.WorkH)))
	o.SetProperty("scale", object.NewNumber(d.Scale))
	o.SetProperty("primary", object.NewBoolean(d.Primary))
	o.SetProperty("foldable", object.NewBoolean(d.Foldable))
	o.SetProperty("posture", object.NewString(d.Posture))
	o.SetProperty("hinge", hingeToJS(d.Hinge))
	regs := make([]object.Value, 0, len(d.Regions))
	for _, r := range d.Regions {
		ro := object.NewObject()
		ro.SetProperty("id", object.NewString(r.ID))
		ro.SetProperty("x", object.NewNumber(float64(r.X)))
		ro.SetProperty("y", object.NewNumber(float64(r.Y)))
		ro.SetProperty("width", object.NewNumber(float64(r.W)))
		ro.SetProperty("height", object.NewNumber(float64(r.H)))
		regs = append(regs, ro)
	}
	o.SetProperty("regions", object.NewArray(regs))
	return o
}

func hingeToJS(h *DisplayHinge) object.Value {
	if h == nil {
		return object.NullSingleton
	}
	o := object.NewObject()
	o.SetProperty("x", object.NewNumber(float64(h.X)))
	o.SetProperty("y", object.NewNumber(float64(h.Y)))
	o.SetProperty("width", object.NewNumber(float64(h.W)))
	o.SetProperty("height", object.NewNumber(float64(h.H)))
	o.SetProperty("orientation", object.NewString(h.Orientation))
	return o
}

func displaysToJS(list []Display) object.Value {
	out := make([]object.Value, 0, len(list))
	for _, d := range list {
		out = append(out, displayToJS(d))
	}
	return object.NewArray(out)
}

// screenGetter 造一个"每次调用都重新读一遍当前屏幕状态"的 getter。
//
// **它同时读环境版本 signal**: 于是把它放进函数 prop / 函数子节点里,
// 显示器或姿态一变就会触发那一处重算 —— 这就是 useScreen/usePosture 的
// 响应式来源 (与 useRoute 的做法完全同构)。
func screenGetter[T any](pick func() T, conv func(T) object.Value) object.Value {
	return object.NewBuiltin("screen", func(args ...object.Value) object.Value {
		if g := envSignal(); g != nil {
			object.CallFunction(g, nil) // 读一次 = 订阅一次
		}
		return conv(pick())
	})
}

// ===== gx/screen 模块 =====

// scr 是内置函数的统一包装 (object.Value 必须是实现了 Value 接口的对象,
// Go 裸函数值不算 —— 与 view.go 里逐个 NewBuiltin 的写法一致)。
func scr(name string, fn func(args ...object.Value) object.Value) object.Value {
	return object.NewBuiltin(name, fn)
}

func init() {
	object.RegisterBuiltinModule("gx/screen", func() map[string]object.Value {
		return map[string]object.Value{
			"screens":       scr("screens", jsScreens),
			"primaryScreen": scr("primaryScreen", jsPrimaryScreen),
			"screen":        scr("screen", jsScreenByID),
			// screenOf(win?): 窗口所在显示器。win 省略 → "最近挂载的窗口"。
			"screenOf": scr("screenOf", jsScreenOf),
			"useScreen": scr("useScreen", func(args ...object.Value) object.Value {
				w := windowArg(args)
				return screenGetter(func() Display {
					d, _ := displayOfWorkWindow(w)
					return d
				}, displayToJS)
			}),
			"useScreens": scr("useScreens", func(args ...object.Value) object.Value {
				return screenGetter(allDisplays, displaysToJS)
			}),
			// 尺寸/缩放的直接读数 (最常用的一组, 单列出来省得脚本自己拼对象)
			"windowInfo": scr("windowInfo", jsWindowInfo),
			"useWindowInfo": scr("useWindowInfo", func(args ...object.Value) object.Value {
				w := windowArg(args)
				return object.NewBuiltin("useWindowInfo", func(args ...object.Value) object.Value {
					if g := envSignal(); g != nil {
						object.CallFunction(g, nil) // 读一次 = 订阅一次
					}
					return windowInfoToJS(w)
				})
			}),
			"posture": scr("posture", func(args ...object.Value) object.Value {
				d, _ := displayOfWorkWindow(windowArg(args))
				return object.NewString(d.Posture)
			}),
			"usePosture": scr("usePosture", func(args ...object.Value) object.Value {
				w := windowArg(args)
				return screenGetter(func() string {
					d, _ := displayOfWorkWindow(w)
					return d.Posture
				}, func(s string) object.Value { return object.NewString(s) })
			}),
			"hinge":   scr("hinge", jsHinge),
			"regions": scr("regions", jsRegions),
			"platform": scr("platform", func(args ...object.Value) object.Value {
				return object.NewString(platformName())
			}),
			"reportPosture": scr("reportPosture", func(args ...object.Value) object.Value {
				if len(args) == 0 {
					return object.NewTypeError("reportPosture: 需要 opts 对象")
				}
				return reportPostureGo(args...)
			}),
			"resetDisplays": scr("resetDisplays", resetDisplaysGo),
			"onDisplayChange": scr("onDisplayChange", func(args ...object.Value) object.Value {
				return addScreenHook(args)
			}),
			"offDisplayChange": scr("offDisplayChange", func(args ...object.Value) object.Value {
				return removeScreenHook(args)
			}),
		}
	})
}

func jsScreens(args ...object.Value) object.Value {
	return displaysToJS(allDisplays())
}

func jsPrimaryScreen(args ...object.Value) object.Value {
	return displayToJS(mustPrimary())
}

func jsScreenByID(args ...object.Value) object.Value {
	id := ""
	if len(args) > 0 {
		id = object.ToString(args[0])
	}
	d, ok := findDisplay(id)
	if !ok {
		return object.NullSingleton
	}
	return displayToJS(d)
}

func jsScreenOf(args ...object.Value) object.Value {
	d, ok := displayOfWorkWindow(windowArg(args))
	if !ok {
		return object.NullSingleton
	}
	return displayToJS(d)
}

func jsWindowInfo(args ...object.Value) object.Value {
	return windowInfoToJS(windowArg(args))
}

func jsHinge(args ...object.Value) object.Value {
	d, _ := displayOfWorkWindow(windowArg(args))
	return hingeToJS(d.Hinge)
}

func jsRegions(args ...object.Value) object.Value {
	d, _ := displayOfWorkWindow(windowArg(args))
	regs := make([]object.Value, 0, len(d.Regions))
	for _, r := range d.Regions {
		ro := object.NewObject()
		ro.SetProperty("id", object.NewString(r.ID))
		ro.SetProperty("x", object.NewNumber(float64(r.X)))
		ro.SetProperty("y", object.NewNumber(float64(r.Y)))
		ro.SetProperty("width", object.NewNumber(float64(r.W)))
		ro.SetProperty("height", object.NewNumber(float64(r.H)))
		regs = append(regs, ro)
	}
	return object.NewArray(regs)
}

func mustPrimary() Display {
	for _, d := range allDisplays() {
		if d.Primary {
			return d
		}
	}
	list := allDisplays()
	if len(list) > 0 {
		return list[0]
	}
	return virtualDisplay(0, 0)
}

// windowArg 从参数里取窗口句柄 (省略/不是句柄 → nil, 由调用方按活跃窗口处理)。
func windowArg(args []object.Value) *Window {
	if len(args) == 0 {
		return nil
	}
	return windowHandleOf(args[0])
}

// windowHandleOf 判断一个 JS 值是不是窗口句柄。
//
// 判据是"对象上有一个可调用的 goxWindowId 属性" —— 这是 jsObject() 里
// 埋的标记 (见 window.go)。用一个专用标记而不是"看起来像句柄", 是为了让
// 传错的参数立刻退化为 nil (= 活跃窗口), 而不是在某次调用里静默取到零值。
func windowHandleOf(v object.Value) *Window {
	o, ok := v.(*object.Object)
	if !ok {
		return nil
	}
	marker, ok := o.GetProperty("__goxWindow")
	if !ok || !object.IsCallable(marker) {
		return nil
	}
	res := object.CallFunction(marker, nil)
	if w, ok := res.(*windowRefValue); ok {
		return w.w
	}
	return nil
}

// windowRefValue 是 __goxWindow() 的返回值: 一个只用来"把 Go 指针递出来"的
// 内部值。脚本看不到它 (没有文档、Inspect 也不暴露内容), 但它让窗口句柄
// 在 Go 侧可还原 —— 这是"JS 对象 ↔ Go 句柄"的唯一桥。
type windowRefValue struct {
	w *Window
}

func (r *windowRefValue) Type() object.ObjectType { return object.ObjectType("WINDOW_REF") }
func (r *windowRefValue) Inspect() string         { return "<window ref>" }
func (r *windowRefValue) IsTruthy() bool          { return true }
func (r *windowRefValue) GetProperty(string) (object.Value, bool) {
	return object.UndefinedSingleton, false
}
func (r *windowRefValue) SetProperty(string, object.Value) {}

// windowInfoToJS 组装 {width,height,scale,screenWidth,screenHeight,platform}。
//
// 字段名一次定好 (agent_doc/gui-responsive-screen-options.md §3.1 列的那条): 窗口尺寸用
// width/height (与 render 的窗口配置、onResize 事件同词), **屏幕**尺寸用
// screenWidth/screenHeight —— 两组名字不同, 因为它们回答的是不同的问题,
// 混用一个名字是以后最容易踩的坑。
func windowInfoToJS(win *Window) object.Value {
	o := object.NewObject()
	var w, h int
	var s Surface
	if win != nil {
		s = win.Surface()
	} else if a := currentApp(); a != nil {
		a.mu.Lock()
		s = a.surface
		a.mu.Unlock()
	}
	if s != nil {
		w, h = s.Size()
	}
	d, _ := displayOfWorkWindow(win)
	o.SetProperty("width", object.NewNumber(float64(w)))
	o.SetProperty("height", object.NewNumber(float64(h)))
	o.SetProperty("scale", object.NewNumber(d.Scale))
	o.SetProperty("screenWidth", object.NewNumber(float64(d.W)))
	o.SetProperty("screenHeight", object.NewNumber(float64(d.H)))
	o.SetProperty("workWidth", object.NewNumber(float64(d.WorkW)))
	o.SetProperty("workHeight", object.NewNumber(float64(d.WorkH)))
	o.SetProperty("screenId", object.NewString(d.ID))
	o.SetProperty("platform", object.NewString(platformName()))
	return o
}

// platformName 报告当前窗口后端名 ("windows" / "x11" / "cocoa" / "headless")。
//
// 不引 reflect 之外的任何东西: 后端包 (win32/x11/cocoa) 通过 init 把工厂注册
// 进来, 而它们的包路径末段就是名字。这样 gfx 内核不用反向 import 任何一个
// 后端包 (那会成环), 也不用让每个后端多写一行"我叫什么"。
func platformName() string {
	appMu.Lock()
	f := defaultFactory
	appMu.Unlock()
	if f == nil {
		return "headless"
	}
	t := reflect.TypeOf(f)
	if t == nil {
		return "headless"
	}
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	pkg := t.PkgPath()
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		pkg = pkg[i+1:]
	}
	if pkg == "" {
		return "headless"
	}
	return pkg
}

// addScreenHook 登记 onDisplayChange(fn), 返回一个注销函数 (可重复调用)。
func addScreenHook(args []object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("onDisplayChange: 需要回调函数")
	}
	fn := args[0]
	screenMu.Lock()
	screenHooks = append(screenHooks, fn)
	screenMu.Unlock()
	return object.NewBuiltin("offDisplayChange", func(args ...object.Value) object.Value {
		screenMu.Lock()
		for i, h := range screenHooks {
			if h == fn {
				screenHooks = append(screenHooks[:i], screenHooks[i+1:]...)
				break
			}
		}
		screenMu.Unlock()
		return object.UndefinedSingleton
	})
}

func removeScreenHook(args []object.Value) object.Value {
	if len(args) == 0 {
		return object.UndefinedSingleton
	}
	fn := args[0]
	screenMu.Lock()
	for i, h := range screenHooks {
		if h == fn {
			screenHooks = append(screenHooks[:i], screenHooks[i+1:]...)
			break
		}
	}
	screenMu.Unlock()
	return object.UndefinedSingleton
}

// SortedDisplayIDs 返回排好序的显示器 ID (测试与文档用: 让枚举顺序可断言)。
func SortedDisplayIDs() []string {
	list := allDisplays()
	ids := make([]string, 0, len(list))
	for _, d := range list {
		ids = append(ids, d.ID)
	}
	sort.Strings(ids)
	return ids
}

// resetScreenStateForTest 清空上报与回调 (用例之间不许串味: 本模块的状态是
// 包级单例, 与窗口注册表同一纪律)。
func resetScreenStateForTest() {
	screenMu.Lock()
	screenOverride = nil
	screenHooks = nil
	screenRevision++
	screenEnvGet, screenEnvSet = nil, nil
	screenMu.Unlock()
}

func objPropNum(o *object.Object, name string) float64 {
	if n, ok := objProp(o, name).(*object.Number); ok {
		return n.Value
	}
	return 0
}
