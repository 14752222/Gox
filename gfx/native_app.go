package gfx

// ===== gx/app: 应用生命周期 / 返回键 / 内存警告 / 分享 / 退出 =====
//
// mobile-port-plan §三 把"生命周期"列为六个硬缺口之一 (缺口 #5): 没有它, 切到
// 后台的应用仍会全速跑 rAF 与定时器, 被系统降权甚至杀掉。这一族 API 就是那个
// 缺口的脚本面。
//
// ## 一个刻意的分工: 前后台由**宿主**上报, 而不是让框架去猜
//
// Android 的 `onResume/onPause`、iOS 的 `applicationDidBecomeActive` 只有宿主
// 知道 —— 与 gfx/screen 拒绝"猜折叠姿态"是同一条理由 (猜错比不报更糟: 一个
// 猜出来的 "background" 会让应用把自己的定时器全停了, 而它其实还在前台)。
// 所以这里只提供**通道**:
//
//	宿主: gfx.ReportAppState("background")   ← 系统回调里调 (GUI 线程)
//	脚本: onAppStateChange((s) => { ... })   ← 或 useAppState() 响应式读
//
// ## 返回键 (Android): 一个必须"能说不"的入口
//
// Android 的返回键语义是"应用先有机会处理, 不处理则系统关 Activity"。所以
// `onBackPress(fn)` 的**返回值有意义**: 返回真值 = 我处理了, 宿主不该关界面
// (Kotlin 侧据此设置 `onBackPressedDispatcher` 的 `isEnabled`)。这是这一层里
// 唯一"回调有返回值语义"的 API, 用错了的症状是"按返回键直接退出, 路由栈里的
// 上一页白设了"。

import (
	"strings"

	"github.com/14752222/Gox/object"
)

// 应用状态取值 (与 Android Lifecycle / iOS AppState 的词汇对齐)。
const (
	AppStateActive     = "active"     // 前台且可交互
	AppStateBackground = "background" // 完全进入后台
	AppStateInactive   = "inactive"   // 前台但不可交互 (来电、下拉通知栏、切任务)
	AppStateUnknown    = "unknown"
)

// 方法名与事件名。
const (
	nmAppShare       = "app.share"
	nmAppExit        = "app.exit"
	nmAppOrientation = "app.orientation"

	evAppState = "appstate"
	evMemory   = "memory"
	evBack     = "back"
)

var currentAppState = AppStateUnknown

// normalizeAppState 归一化宿主给的字符串 (不认识 → "unknown", 不猜)。
func normalizeAppState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case AppStateActive, "foreground", "resumed":
		return AppStateActive
	case AppStateBackground, "paused", "stopped":
		return AppStateBackground
	case AppStateInactive, "willresign", "willresignactive":
		return AppStateInactive
	default:
		return AppStateUnknown
	}
}

// ReportAppState 由宿主上报应用生命周期状态 (GUI 线程)。
//
// 重复上报同一个状态**不会**抬高版本号: 宿主在每次 onResume/onPause 都报一遍是
// 常见写法, 而"状态没变"不该让整棵订阅树重算。
func ReportAppState(state string) {
	s := normalizeAppState(state)
	nativeMu.Lock()
	same := currentAppState == s
	currentAppState = s
	nativeMu.Unlock()
	if same {
		return
	}
	notifyNativeChanged(evAppState)
}

// CurrentAppState 读当前应用状态 (Go 侧)。
func CurrentAppState() string {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return currentAppState
}

// ReportMemoryWarning 由宿主上报"内存吃紧" (Android onTrimMemory /
// iOS didReceiveMemoryWarning)。
//
// 脚本侧应当在这里**释放可重建的缓存** (图片缩略图、离线数据), 而不是保存状态
// —— 保存状态是 background 的职责, 两者混在一起会让"内存警告"变成一次意外的
// 网络/磁盘写。
func ReportMemoryWarning() {
	notifyNativeChanged(evMemory)
}

// ReportBackPress 由宿主上报"用户按了返回键", 返回脚本是否已消费这次返回。
//
// 宿主用法 (Android Kotlin):
//
//	dispatcher.addCallback(object : OnBackPressedCallback(true) {
//	    override fun handleOnBackPressed() {
//	        val handled = Gox.reportBackPress()   // → JNI → 本函数
//	        if (!handled) { isEnabled = false; dispatcher.onBackPressed() }
//	    }
//	})
//
// 多个回调时**任一返回真值即算已处理** (与浏览器事件冒泡里 stopPropagation 的
// "有一个处理者就够了"一致)。
func ReportBackPress() bool {
	nativeMu.Lock()
	hooks := append([]object.Value(nil), nativeHooks[evBack]...)
	nativeMu.Unlock()
	handled := false
	for _, fn := range hooks {
		res := object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/app onBackPress 回调抛错: %v", err)
			continue
		}
		if res != nil && res.IsTruthy() {
			handled = true
		}
	}
	return handled
}

// ===== JS 入口 =====

func jsAppState(args ...object.Value) object.Value {
	return object.NewString(CurrentAppState())
}

// jsShare 拉起系统分享面板 (文本 / 链接 / 图片)。
//
// 异步 (Promise): 分享面板是系统 UI, 移动端可能等用户选完目标应用 —— 与
// gx/dialog 的 alert 同族, 不该用同步签名堵住 GUI 线程。
func jsShare(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	cb := nativeOptFunc(args, 1)
	// 字符串参数当成 text 的简写: share("看看这个") 比
	// share({text: "看看这个"}) 常见得多。
	if len(args) > 0 {
		if s, ok := args[0].(*object.String); ok {
			opts = object.NewObject()
			opts.SetProperty("text", s)
		}
	}
	if objPropStr(opts, "text") == "" && objPropStr(opts, "url") == "" &&
		objPropStr(opts, "imagePath") == "" && objProp(opts, "files") == nil {
		return object.NewTypeError("share: 至少要给 text / url / imagePath / files 之一")
	}
	return callNative(nmAppShare, opts, cb)
}

// jsExitApp 退出应用 (Android finishAffinity / iOS 只能"退到后台")。
//
// 软降级: 退出失败没什么可做的, 也不该抛 —— 调用点通常已经在"用户点了退出"
// 的处理函数末尾。iOS 上系统不允许程序自杀, 宿主会退化成"最小化", 这是平台
// 事实, 内核不假装能改变它。
func jsExitApp(args ...object.Value) object.Value {
	callNativeSoft(nmAppExit, object.NewObject())
	return object.UndefinedSingleton
}

// jsSetOrientation 请求屏幕方向: "portrait" | "landscape" | "auto"。
//
// 返回布尔 (宿主认不认这个请求), 返回 Promise 没有意义: 方向变化最终由系统的
// 配置变更广播回来, 而那条路走的是 gx/screen 的尺寸/姿态通道。
func jsSetOrientation(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("setOrientation: 需要 \"portrait\"|\"landscape\"|\"auto\"")
	}
	mode := strings.ToLower(strings.TrimSpace(valueText(args[0])))
	switch mode {
	case "portrait", "portrait-primary", "portrait-upside-down":
		mode = "portrait"
	case "landscape", "landscape-left", "landscape-right":
		mode = "landscape"
	case "auto", "unspecified", "sensor":
		mode = "auto"
	default:
		return object.NewTypeError("setOrientation: 不认识的模式 %q", mode)
	}
	host, ok := nativeHostLocked(nmAppOrientation)
	if host == nil || !ok {
		return object.NewBoolean(false)
	}
	res := host.Call(nmAppOrientation, nativeArgsWithID(propObject("mode", object.NewString(mode)), ""))
	return object.NewBoolean(res.Err == nil && !res.Pending)
}

// jsReportAppState / jsReportMemoryWarning / jsReportBackPress 是给脚本 (桌面
// 模拟器 / 自动化测试) 的上报口 —— 与 reportPosture / reportBattery 同一动机:
// 让"切后台 → 停掉动画"这类链路在桌面上能被端到端验证。
func jsReportAppState(args ...object.Value) object.Value {
	state := AppStateUnknown
	if len(args) > 0 {
		state = valueText(args[0])
	}
	ReportAppState(state)
	return object.UndefinedSingleton
}

func jsReportMemoryWarning(args ...object.Value) object.Value {
	ReportMemoryWarning()
	return object.UndefinedSingleton
}

func jsReportBackPress(args ...object.Value) object.Value {
	return object.NewBoolean(ReportBackPress())
}

// resetAppStateForTest 复位生命周期状态。
func resetAppStateForTest() {
	nativeMu.Lock()
	currentAppState = AppStateUnknown
	nativeMu.Unlock()
}

func init() {
	object.RegisterBuiltinModule("gx/app", func() map[string]object.Value {
		return map[string]object.Value{
			"appState": scr("appState", jsAppState),
			"useAppState": scr("useAppState", nativeGetter("useAppState",
				CurrentAppState, func(s string) object.Value { return object.NewString(s) })),

			"onAppStateChange":  scr("onAppStateChange", nativeHookAPI(evAppState, "onAppStateChange", false)),
			"offAppStateChange": scr("offAppStateChange", nativeHookAPI(evAppState, "onAppStateChange", true)),
			"onMemoryWarning":   scr("onMemoryWarning", nativeHookAPI(evMemory, "onMemoryWarning", false)),
			"offMemoryWarning":  scr("offMemoryWarning", nativeHookAPI(evMemory, "onMemoryWarning", true)),

			// 返回键: 回调返回真值 = 已处理 (宿主据此决定要不要关界面)
			"onBackPress":  scr("onBackPress", nativeHookAPI(evBack, "onBackPress", false)),
			"offBackPress": scr("offBackPress", nativeHookAPI(evBack, "onBackPress", true)),

			"share":          scr("share", jsShare),
			"exitApp":        scr("exitApp", jsExitApp),
			"setOrientation": scr("setOrientation", jsSetOrientation),

			"reportAppState":      scr("reportAppState", jsReportAppState),
			"reportMemoryWarning": scr("reportMemoryWarning", jsReportMemoryWarning),
			"reportBackPress":     scr("reportBackPress", jsReportBackPress),
		}
	})
}
