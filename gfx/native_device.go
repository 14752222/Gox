package gfx

// ===== gx/device: 设备信息 / 电池 / 网络 / 震动 / 系统设置 =====
//
// 这一族 API 分成两类, 分界线就是它们的**数据来源**:
//
//	宿主拉取型 (device.info / device.id / device.brightness / device.openSettings)
//	   —— 需要问平台, 走 NativeHost.Call;
//	宿主推送型 (battery / network) —— 状态会在运行中变化, 让宿主主动 Report,
//	   脚本侧用 battery() / useBattery() 读缓存快照。**推送型不需要宿主实现任何
//	   Call**, 所以桌面 / 无宿主环境下它们照样有明确缺省值, 不会变成错误。
//
// 为什么"电池"是推送型而不是 `getBattery()` 拉取型: 电量是连续变化的量, 轮询式
// API 必然把应用引向 setInterval 轮询 (耗电, 且变化时刻永远慢半拍)。推送 + 响应式
// 取值函数才是这个框架里正确的用法:
//
//	<text>{() => battery().levelPercent + "%"}</text>
//
// —— 与 gx/screen 的 posture 是同一个决定。

import (
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== 数据模型 =====

// DeviceInfo 是设备与应用信息。
//
// 字段名一次定好 (与 agent_doc/gui-device-api-options.md §3.2 的清单对齐)。
// 屏幕尺寸用 screenWidth/screenHeight —— 与 gx/screen 的 windowInfo 保持同一
// 词汇: **窗口**尺寸叫 width/height, **屏幕**尺寸叫 screenWidth/screenHeight,
// 两组名字不同是因为它们回答的是不同的问题, 混用一个名字是以后最容易踩的坑。
type DeviceInfo struct {
	Platform     string // "windows" | "linux" | "darwin" | "android" | "ios" | "harmony"
	OS           string // 人类可读的系统版本, 如 "Android 14" / "Windows 11"
	OSVersion    string // 机器可比较的版本号, 如 "14" / "10.0.22631"
	Arch         string // "arm64" / "amd64" / "386"
	Model        string // 机型, 如 "Pixel 8"
	Brand        string // 品牌, 如 "google"
	Manufacturer string
	DeviceID     string // 稳定的机器标识 (隐私口径: 无哈希、可反查 ⇒ 上报前需告知用户)
	IsEmulator   bool
	Locale       string // "zh-CN"
	Language     string // "zh"
	Region       string // "CN"
	Timezone     string // "Asia/Shanghai"
	TZOffset     int    // 与 UTC 的分钟差 (东八区 = 480)
	AppName      string
	AppVersion   string
	AppBuild     string
	SDKVersion   int  // Android API level / iOS 主版本
	IsTablet     bool // 大屏/平板形态 (分屏与折叠适配的常见分支依据)
	ScreenW      int
	ScreenH      int
	PixelRatio   float64
}

// BatteryState 是电池快照。Supported 为 false 表示这台机器没有电池
// (台式机 / 无宿主), 此时 Level 为 -1 —— 与"电量 0%" 必须能区分开。
type BatteryState struct {
	Supported    bool
	Level        float64 // 0..1; -1 = 未知
	Charging     bool
	ChargingType string  // "usb" | "ac" | "wireless" | "none" | "unknown"
	Temperature  float64 // 摄氏度; -1 = 未知
	LowPowerMode bool    // 省电模式
}

// NetworkState 是网络快照。
type NetworkState struct {
	Connected          bool
	Type               string // "wifi" | "cellular" | "ethernet" | "bluetooth" | "vpn" | "other" | "none"
	Metered            bool   // 计费网络 (移动数据/热点) —— 决定能不能下大文件
	SSID               string
	Strength           int // 0..100; -1 = 未知
	Carrier            string
	CellularGeneration string // "2g" | "3g" | "4g" | "5g" | ""
}

// ===== 方法名与事件名 =====

const (
	nmDeviceInfo       = "device.info"
	nmDeviceID         = "device.id"
	nmDeviceVibrate    = "device.vibrate"
	nmDeviceBrightness = "device.brightness"
	nmDeviceKeepOn     = "device.keepScreenOn"
	nmDeviceSettings   = "device.openSettings"
)

// 推送型的两个事件名 (回调表与 signal 的键)。
const (
	evBattery = "battery"
	evNetwork = "network"
)

// ===== 状态 =====

var (
	deviceBattery = BatteryState{Supported: false, Level: -1, Temperature: -1, ChargingType: "unknown"}
	deviceNetwork = NetworkState{Type: "none", Strength: -1}
	// deviceOverlay 缓存宿主给出的 device.info。设备信息是**不变**的, 每帧重问
	// 一次宿主纯属浪费 —— useDeviceInfo() 会被放进渲染路径。
	deviceOverlay   *object.Object
	deviceOverlayOK bool
)

// ===== 本机缺省值 (没有宿主时也要能回答) =====

// localDeviceInfo 组装"内核自己知道的部分"。
//
// 为什么要有这一半: 桌面没有宿主 (或宿主没实现 device.info) 时, getSystemInfo
// 这类调用不该整体失败 —— 平台/架构/语言/时区/屏幕尺寸这些 Go 侧本来就知道,
// 白白丢掉只会让应用被迫为"桌面"写特例。
func localDeviceInfo() DeviceInfo {
	d := DeviceInfo{
		Platform: normalizePlatform(runtime.GOOS),
		Arch:     runtime.GOARCH,
		Timezone: time.Now().Location().String(),
	}
	_, off := time.Now().Zone()
	d.TZOffset = off / 60
	if lang, region := localeFromEnv(); lang != "" {
		d.Language, d.Region = lang, region
		d.Locale = lang
		if region != "" {
			d.Locale = lang + "-" + region
		}
	}
	prim := mustPrimary()
	d.ScreenW, d.ScreenH = prim.W, prim.H
	d.PixelRatio = prim.Scale
	if d.PixelRatio <= 0 {
		d.PixelRatio = 1
	}
	// 大屏判据只作为**缺省猜测**: 宿主报了 isTablet 就以宿主为准 (桌面窗口大
	// 不等于想走平板布局)。
	d.IsTablet = prim.W >= 900 && isMobilePlatform(d.Platform)
	return d
}

// normalizePlatform 把 GOOS/宿主给的名字归一到文档承诺的词汇表。
//
// 用 runtime.GOOS 而不是 platformName(): 后者报的是**窗口后端包名**
// ("win32"/"x11"/"cocoa"), 那是内核内部词汇; 而这里要的是应用看得见的平台名。
// 移动端 GOOS 恰好就是 "android"/"ios", 所以这个映射在三个平台都对。
func normalizePlatform(goos string) string {
	if goos == "" {
		return "unknown"
	}
	return goos
}

// isMobilePlatform 报告平台名是不是移动端。
func isMobilePlatform(p string) bool {
	switch p {
	case "android", "ios", "harmony":
		return true
	}
	return false
}

// localeFromEnv 从环境变量猜语言/地区 (桌面缺省; 移动端由宿主报)。
//
// 解析 "zh_CN.UTF-8" / "en_US" / "C" 三种常见形态; 认不出就返回空 —— 宁可留空,
// 也不要瞎猜一个语言塞给应用去显示。
func localeFromEnv() (lang, region string) {
	raw := os.Getenv("LC_ALL")
	if raw == "" {
		raw = os.Getenv("LC_MESSAGES")
	}
	if raw == "" {
		raw = os.Getenv("LANG")
	}
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, '.'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, '@'); i >= 0 {
		raw = raw[:i]
	}
	if raw == "" || raw == "C" || raw == "POSIX" {
		return "", ""
	}
	lang, region, _ = strings.Cut(raw, "_")
	return strings.ToLower(lang), strings.ToUpper(region)
}

// deviceInfo 取当前设备信息 (本机缺省 + 宿主覆盖)。
func deviceInfo() DeviceInfo {
	d := localDeviceInfo()
	if o := deviceHostOverlay(); o != nil {
		applyDeviceOverlay(&d, o)
	}
	return d
}

// deviceHostOverlay 取并缓存宿主的 device.info 结果 (只在成功时缓存)。
func deviceHostOverlay() *object.Object {
	if deviceOverlayOK {
		return deviceOverlay
	}
	host, ok := nativeHostLocked(nmDeviceInfo)
	if host == nil || !ok {
		return nil
	}
	res := host.Call(nmDeviceInfo, nativeArgsWithID(nil, ""))
	if res.Err != nil || res.Pending {
		return nil
	}
	o, ok := res.Result.(*object.Object)
	if !ok {
		return nil
	}
	deviceOverlay, deviceOverlayOK = o, true
	return o
}

// applyDeviceOverlay 把宿主报上来的字段覆盖到本机缺省之上。
//
// 逐个字段判空再覆盖 (而不是整体替换): 宿主通常只报它知道的那几个 (机型/系统),
// 平台/屏幕尺寸这类内核已经算对了 —— 整体替换会让"宿主没提的字段"全变成空串。
func applyDeviceOverlay(d *DeviceInfo, o *object.Object) {
	if v := objPropStr(o, "platform"); v != "" {
		d.Platform = normalizePlatform(v)
	}
	setIfStr(&d.OS, o, "os")
	setIfStr(&d.OSVersion, o, "osVersion")
	setIfStr(&d.Arch, o, "arch")
	setIfStr(&d.Model, o, "model")
	setIfStr(&d.Brand, o, "brand")
	setIfStr(&d.Manufacturer, o, "manufacturer")
	setIfStr(&d.DeviceID, o, "deviceId")
	setIfStr(&d.Locale, o, "locale")
	setIfStr(&d.Language, o, "language")
	setIfStr(&d.Region, o, "region")
	setIfStr(&d.Timezone, o, "timezone")
	setIfStr(&d.AppName, o, "appName")
	setIfStr(&d.AppVersion, o, "appVersion")
	setIfStr(&d.AppBuild, o, "appBuild")
	if n := objPropNum(o, "tzOffset"); n != 0 {
		d.TZOffset = int(n)
	}
	if n := objPropNum(o, "sdkVersion"); n > 0 {
		d.SDKVersion = int(n)
	}
	if n := objPropNum(o, "screenWidth"); n > 0 {
		d.ScreenW = int(n)
	}
	if n := objPropNum(o, "screenHeight"); n > 0 {
		d.ScreenH = int(n)
	}
	if n := objPropNum(o, "pixelRatio"); n > 0 {
		d.PixelRatio = n
	}
	if v := objProp(o, "isEmulator"); v != nil {
		d.IsEmulator = nativeBool(v)
	}
	if v := objProp(o, "isTablet"); v != nil {
		d.IsTablet = nativeBool(v)
	}
}

func setIfStr(dst *string, o *object.Object, key string) {
	if v := objPropStr(o, key); v != "" {
		*dst = v
	}
}

// ===== JS 对象构造 =====

func deviceInfoToJS(d DeviceInfo) object.Value {
	o := object.NewObject()
	nativeSet(o, "platform", d.Platform)
	nativeSet(o, "os", d.OS)
	nativeSet(o, "osVersion", d.OSVersion)
	nativeSet(o, "arch", d.Arch)
	nativeSet(o, "model", d.Model)
	nativeSet(o, "brand", d.Brand)
	nativeSet(o, "manufacturer", d.Manufacturer)
	nativeSet(o, "deviceId", d.DeviceID)
	nativeSet(o, "locale", d.Locale)
	nativeSet(o, "language", d.Language)
	nativeSet(o, "region", d.Region)
	nativeSet(o, "timezone", d.Timezone)
	nativeSet(o, "appName", d.AppName)
	nativeSet(o, "appVersion", d.AppVersion)
	nativeSet(o, "appBuild", d.AppBuild)
	o.SetProperty("tzOffset", object.NewNumber(float64(d.TZOffset)))
	o.SetProperty("sdkVersion", object.NewNumber(float64(d.SDKVersion)))
	o.SetProperty("screenWidth", object.NewNumber(float64(d.ScreenW)))
	o.SetProperty("screenHeight", object.NewNumber(float64(d.ScreenH)))
	o.SetProperty("pixelRatio", object.NewNumber(d.PixelRatio))
	o.SetProperty("isEmulator", object.NewBoolean(d.IsEmulator))
	o.SetProperty("isTablet", object.NewBoolean(d.IsTablet))
	// 两个最常用的派生量: 应用里 `deviceInfo().isMobile` 比
	// `platform === "android" || platform === "ios"` 好写也好读。
	o.SetProperty("isMobile", object.NewBoolean(isMobilePlatform(d.Platform)))
	o.SetProperty("isDesktop", object.NewBoolean(!isMobilePlatform(d.Platform)))
	return o
}

func batteryToJS(b BatteryState) object.Value {
	o := object.NewObject()
	o.SetProperty("supported", object.NewBoolean(b.Supported))
	o.SetProperty("level", object.NewNumber(b.Level))
	// levelPercent 是**便利字段**: 0..1 给计算用, 0..100 给显示用, 让每个应用
	// 都写一遍 Math.round(level * 100) 没有意义。未知时为 -1。
	pct := -1.0
	if b.Level >= 0 {
		pct = float64(int(b.Level*100 + 0.5))
	}
	o.SetProperty("levelPercent", object.NewNumber(pct))
	o.SetProperty("charging", object.NewBoolean(b.Charging))
	nativeSet(o, "chargingType", b.ChargingType)
	o.SetProperty("temperature", object.NewNumber(b.Temperature))
	o.SetProperty("lowPowerMode", object.NewBoolean(b.LowPowerMode))
	return o
}

func networkToJS(n NetworkState) object.Value {
	o := object.NewObject()
	o.SetProperty("connected", object.NewBoolean(n.Connected))
	nativeSet(o, "type", n.Type)
	o.SetProperty("metered", object.NewBoolean(n.Metered))
	nativeSet(o, "ssid", n.SSID)
	o.SetProperty("strength", object.NewNumber(float64(n.Strength)))
	nativeSet(o, "carrier", n.Carrier)
	nativeSet(o, "generation", n.CellularGeneration)
	// isWifi / isCellular: 业务里 90% 的网络判断都是这两个。
	o.SetProperty("isWifi", object.NewBoolean(n.Connected && n.Type == "wifi"))
	o.SetProperty("isCellular", object.NewBoolean(n.Connected && n.Type == "cellular"))
	return o
}

// ===== 宿主上报入口 (Go 侧导出) =====

// ReportBattery 由宿主上报电池状态。
//
// **必须在 GUI 线程调用** (它最终会执行脚本回调); 跨线程先 `gfx.Post` —— 与
// NotifyDisplaysChanged / reportPosture 同一条纪律: win32 的 WndProc、Android 的
// BroadcastReceiver (主线程外) 都不直接调它。
func ReportBattery(b BatteryState) {
	if b.ChargingType == "" {
		b.ChargingType = "unknown"
	}
	if b.Level > 1 {
		b.Level = 1
	}
	if b.Level < 0 {
		b.Level = -1
	}
	if b.Temperature == 0 {
		// 电池不会真的在 0 摄氏度 —— 0 只可能是"没填"。宁可报未知。
		b.Temperature = -1
	}
	nativeMu.Lock()
	deviceBattery = b
	nativeMu.Unlock()
	notifyNativeChanged(evBattery)
}

// ReportNetwork 由宿主上报网络状态 (GUI 线程纪律同上)。
func ReportNetwork(n NetworkState) {
	if n.Type == "" {
		n.Type = "none"
	}
	if n.Strength == 0 {
		n.Strength = -1
	}
	nativeMu.Lock()
	deviceNetwork = n
	nativeMu.Unlock()
	notifyNativeChanged(evNetwork)
}

// Battery 读电池快照 (Go 侧; 测试与桌面宿主用)。
func Battery() BatteryState {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return deviceBattery
}

// Network 读网络快照 (Go 侧)。
func Network() NetworkState {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return deviceNetwork
}

// ===== JS 取值函数 =====

// nativeGetter 造一个"每次调用都重新读一遍 + 读一次环境版本 signal"的取值函数。
//
// 与 gx/screen 的 screenGetter 同一套路: 把返回值放进函数 prop / 函数子节点里,
// 状态一变那一处就重算 —— 这是 useBattery/useNetwork/useDeviceInfo 的响应式来源。
func nativeGetter[T any](name string, pick func() T, conv func(T) object.Value) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		if g := nativeEnvSignal(); g != nil {
			object.CallFunction(g, nil) // 读一次 = 订阅一次
		}
		return conv(pick())
	}
}

// ===== 模块实现 =====

func jsDeviceInfo(args ...object.Value) object.Value { return deviceInfoToJS(deviceInfo()) }
func jsDeviceID(args ...object.Value) object.Value   { return object.NewString(deviceInfo().DeviceID) }
func jsBattery(args ...object.Value) object.Value    { return batteryToJS(Battery()) }
func jsNetwork(args ...object.Value) object.Value    { return networkToJS(Network()) }
func jsIsCharging(args ...object.Value) object.Value {
	b := Battery()
	return object.NewBoolean(b.Supported && b.Charging)
}
func jsIsOnline(args ...object.Value) object.Value     { return object.NewBoolean(Network().Connected) }
func jsCapabilities(args ...object.Value) object.Value { return capabilitiesToJS() }

// capabilitiesToJS 把宿主声明的能力列表转成数组。
func capabilitiesToJS() object.Value {
	list := NativeCapabilities()
	out := make([]object.Value, 0, len(list))
	for _, c := range list {
		out = append(out, object.NewString(c))
	}
	return object.NewArray(out)
}

// jsVibrate 触发震动 (毫秒, 缺省 15)。
//
// **软降级**: 不支持就什么都不做, 不报错 —— 震动是装饰性反馈, 让它抛异常会连累
// 整个点击处理函数。这与相机/定位必须显式失败的立场不同, 分界线是"失败了业务
// 还能不能继续正确运行"。
func jsVibrate(args ...object.Value) object.Value {
	ms := 15
	if len(args) > 0 {
		if d := objPropNum(nativeOpts(args, 0), "duration"); d > 0 {
			ms = int(d)
		} else if n := nativeNumberValue(args[0]); n > 0 {
			ms = int(n)
		}
	}
	callNativeSoft(nmDeviceVibrate, propObject("duration", object.NewNumber(float64(ms))))
	return object.UndefinedSingleton
}

// nativeNumberValue 把恰好是数字的参数取出来 (`vibrate(20)` 这种写法)。
func nativeNumberValue(v object.Value) float64 {
	if n, ok := v.(*object.Number); ok {
		return n.Value
	}
	return 0
}

// propObject 造一个只带一个属性的参数对象 (宿主方法多半只收一个字段)。
func propObject(name string, v object.Value) *object.Object {
	o := object.NewObject()
	o.SetProperty(name, v)
	return o
}

// callNativeSoft 发起一次"失败也无所谓"的原生调用: 不产生 Promise、不报错。
// 用于震动 / 屏幕常亮这类装饰性、尽力而为的能力。
func callNativeSoft(method string, opts *object.Object) {
	host, ok := nativeHostLocked(method)
	if host == nil || !ok {
		return
	}
	nativeMu.Lock()
	id := nativeNextIDLocked()
	// 先占一个 sink: 万一宿主是异步实现, 回填进来也不会打"未知调用"的警告。
	nativeCalls[id] = &nativeCall{method: method, sink: object.NewBuiltin("__native_soft",
		func(args ...object.Value) object.Value { return object.UndefinedSingleton })}
	nativeMu.Unlock()

	res := host.Call(method, nativeArgsWithID(opts, id))
	if !res.Pending {
		nativeMu.Lock()
		delete(nativeCalls, id)
		nativeMu.Unlock()
	}
}

// jsKeepScreenOn 开关屏幕常亮 (`keepScreenOn(false)` 关)。
func jsKeepScreenOn(args ...object.Value) object.Value {
	on := true
	if len(args) > 0 {
		on = nativeBool(args[0])
	}
	callNativeSoft(nmDeviceKeepOn, propObject("on", object.NewBoolean(on)))
	return object.UndefinedSingleton
}

// jsGetBrightness 读当前屏幕亮度 (0..1; 不支持 → -1)。
func jsGetBrightness(args ...object.Value) object.Value {
	host, ok := nativeHostLocked(nmDeviceBrightness)
	if host == nil || !ok {
		return object.NewNumber(-1)
	}
	res := host.Call(nmDeviceBrightness, nativeArgsWithID(nil, ""))
	if o, ok := res.Result.(*object.Object); ok {
		if v := objPropNum(o, "value"); v >= 0 {
			return object.NewNumber(v)
		}
	}
	return object.NewNumber(-1)
}

// jsSetBrightness 设置屏幕亮度 (0..1)。越界直接夹住而不是报错 —— 亮度是
// "尽最大努力设置的显示属性", 与震动同档。
func jsSetBrightness(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("setBrightness: 需要 0..1 之间的数值")
	}
	v := nativeNumberValue(args[0])
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	callNativeSoft(nmDeviceBrightness, propObject("value", object.NewNumber(v)))
	return object.NewNumber(v)
}

// jsOpenSystemSettings 打开系统设置页 (kind 白名单见 settingKindList)。
//
// 返回布尔而不是 Promise: 它是"让系统去开一个界面", 原生调用瞬间返回, 无所谓
// 异步; 拿不到结果的应用也没有可做的事。
func jsOpenSystemSettings(args ...object.Value) object.Value {
	kind := ""
	if len(args) > 0 {
		kind = valueText(args[0])
	}
	if kind == "" {
		kind = "app"
	}
	if !knownSettingKind(kind) {
		return object.NewTypeError("openSystemSettings: 不认识的 kind %q (支持: %s)",
			kind, strings.Join(settingKindList, ", "))
	}
	host, ok := nativeHostLocked(nmDeviceSettings)
	if host == nil || !ok {
		return object.NewBoolean(false)
	}
	res := host.Call(nmDeviceSettings, nativeArgsWithID(propObject("kind", object.NewString(kind)), ""))
	return object.NewBoolean(res.Err == nil && !res.Pending)
}

// settingKindList 是允许的 kind 白名单。
//
// 白名单而不是"原样透传给宿主": kind 最终会变成 Android 的 Settings.ACTION_*
// 或 iOS 的 UIApplication.openSettingsURLString, 传错字符串在各平台上表现不一
// (有的静默无反应, 有的直接崩)。在内核这一层拦住, 错误信息才能一致且可读。
var settingKindList = []string{
	"app", "wifi", "bluetooth", "location", "notification",
	"display", "sound", "battery", "date", "privacy", "storage",
}

func knownSettingKind(kind string) bool {
	for _, k := range settingKindList {
		if k == kind {
			return true
		}
	}
	return false
}

// SettingKinds 返回允许的 kind 列表 (副本)。
//
// **导出并非为了方便应用, 而是它同时是宿主契约**: 内核放行哪个 kind, 宿主就必须
// 为哪个 kind 提供实现 —— 否则内核放行、请求到宿主却变成 invalid-arg, 应用只会
// 看到 openSystemSettings() 返回 false, 完全不知道是"这个平台没实现"。
//
// 这条漏过一次: win32 宿主的映射表少了 "privacy" (内核白名单有), 而当时的测试
// 只覆盖了失败路径 ⇒ 没人发现。现在 win32 的单测拿这个列表逐个核对映射表,
// 宿主再想漂移就会当场红。
func SettingKinds() []string {
	return append([]string(nil), settingKindList...)
}

// jsCanIUse 是 `canIUse(cap)`。
func jsCanIUse(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewBoolean(false)
	}
	return object.NewBoolean(CanIUse(valueText(args[0])))
}

// jsReportBattery 让脚本 (或桌面模拟器 / 自动化测试) 上报电池状态。
//
// 为什么把它暴露给脚本: 与 gx/screen 的 reportPosture 同一个理由 —— 低电量这类
// **设备状态在桌面上没法自然产生**, 给一个上报口就能让"整条响应式链路"在桌面
// 单测 / 开发者工具里跑完, 不必等真机。
func jsReportBattery(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	b := BatteryState{Supported: true, Level: -1, ChargingType: "unknown", Temperature: -1}
	if v := objProp(opts, "supported"); v != nil {
		b.Supported = nativeBool(v)
	}
	if v := objProp(opts, "level"); v != nil {
		n := objPropNum(opts, "level")
		// 允许 0..100 的写法 (宿主常常直接给百分比): >1 时按百分比归一化。
		if n > 1 {
			n /= 100
		}
		b.Level = n
	}
	if v := objProp(opts, "charging"); v != nil {
		b.Charging = nativeBool(v)
	}
	if s := objPropStr(opts, "chargingType"); s != "" {
		b.ChargingType = s
	}
	if v := objProp(opts, "temperature"); v != nil {
		b.Temperature = objPropNum(opts, "temperature")
	}
	if v := objProp(opts, "lowPowerMode"); v != nil {
		b.LowPowerMode = nativeBool(v)
	}
	if b.Level < 0 && b.Supported {
		b.Level = 0
	}
	ReportBattery(b)
	return object.UndefinedSingleton
}

// jsReportNetwork 让脚本 / 模拟器上报网络状态。
func jsReportNetwork(args ...object.Value) object.Value {
	opts := nativeOpts(args, 0)
	n := NetworkState{Type: "none", Strength: -1}
	n.Connected = nativeBool(objProp(opts, "connected"))
	if s := objPropStr(opts, "type"); s != "" {
		n.Type = s
	} else if n.Connected {
		n.Type = "unknown"
	}
	if v := objProp(opts, "metered"); v != nil {
		n.Metered = nativeBool(v)
	}
	n.SSID = objPropStr(opts, "ssid")
	if v := objProp(opts, "strength"); v != nil {
		n.Strength = int(objPropNum(opts, "strength"))
	}
	n.Carrier = objPropStr(opts, "carrier")
	n.CellularGeneration = objPropStr(opts, "generation")
	ReportNetwork(n)
	return object.UndefinedSingleton
}

// nativePropObj 取一个嵌套对象属性 (缺失 / 不是对象 → nil)。
func nativePropObj(o *object.Object, name string) *object.Object {
	if o == nil {
		return nil
	}
	if v, ok := o.GetProperty(name); ok {
		if sub, ok := v.(*object.Object); ok {
			return sub
		}
	}
	return nil
}

// resetDeviceStateForTest 复位设备快照 (与 resetNativeStateForTest 分开:
// 前者只清数据, 后者清宿主与待决表)。
func resetDeviceStateForTest() {
	nativeMu.Lock()
	deviceBattery = BatteryState{Supported: false, Level: -1, Temperature: -1, ChargingType: "unknown"}
	deviceNetwork = NetworkState{Type: "none", Strength: -1}
	deviceOverlay, deviceOverlayOK = nil, false
	nativeMu.Unlock()
}

func init() {
	object.RegisterBuiltinModule("gx/device", func() map[string]object.Value {
		return map[string]object.Value{
			"deviceInfo": scr("deviceInfo", jsDeviceInfo),
			"useDeviceInfo": scr("useDeviceInfo", nativeGetter("useDeviceInfo",
				deviceInfo, deviceInfoToJS)),
			"deviceId": scr("deviceId", jsDeviceID),

			"battery":          scr("battery", jsBattery),
			"useBattery":       scr("useBattery", nativeGetter("useBattery", Battery, batteryToJS)),
			"isCharging":       scr("isCharging", jsIsCharging),
			"onBatteryChange":  scr("onBatteryChange", nativeHookAPI(evBattery, "onBatteryChange", false)),
			"offBatteryChange": scr("offBatteryChange", nativeHookAPI(evBattery, "onBatteryChange", true)),

			"network":          scr("network", jsNetwork),
			"useNetwork":       scr("useNetwork", nativeGetter("useNetwork", Network, networkToJS)),
			"isOnline":         scr("isOnline", jsIsOnline),
			"onNetworkChange":  scr("onNetworkChange", nativeHookAPI(evNetwork, "onNetworkChange", false)),
			"offNetworkChange": scr("offNetworkChange", nativeHookAPI(evNetwork, "onNetworkChange", true)),

			"vibrate":      scr("vibrate", jsVibrate),
			"vibrateShort": scr("vibrateShort", func(args ...object.Value) object.Value { return jsVibrate() }),
			"vibrateLong":  scr("vibrateLong", func(args ...object.Value) object.Value { return jsVibrate(object.NewNumber(400)) }),

			"keepScreenOn":       scr("keepScreenOn", jsKeepScreenOn),
			"getBrightness":      scr("getBrightness", jsGetBrightness),
			"setBrightness":      scr("setBrightness", jsSetBrightness),
			"openSystemSettings": scr("openSystemSettings", jsOpenSystemSettings),

			"canIUse":      scr("canIUse", jsCanIUse),
			"capabilities": scr("capabilities", jsCapabilities),

			// 上报口 (宿主/模拟器/测试; 桌面没有真设备时靠它驱动)
			"reportBattery": scr("reportBattery", jsReportBattery),
			"reportNetwork": scr("reportNetwork", jsReportNetwork),
		}
	})
}
