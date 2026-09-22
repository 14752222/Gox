//go:build windows

package win32

// ===== 桌面 NativeHost 实现 (gfx.NativeHost 的 win32 样板, 2026-09-21) =====
//
// ## 这个文件是什么
//
// gfx 内核把"脚本要用的平台能力"收敛到一个契约里 (gfx/native.go):
//
//	Capabilities() —— 宿主会哪些方法
//	Call(method, args) —— 执行一个方法 (可当场返回, 也可 Pending 后经
//	    gfx.ResolveNative 回填)
//
// 移动端这个契约是给 Kotlin/Swift 宿主实现的 (agent_doc/mobile-port-plan.md);
// 桌面上没有第二层语言边界, 宿主就是后端包自己 —— 本文件就是"宿主该怎么写"的
// 活样板, 同时也是无宿主场景的对照基线: 移动端能报的能力 (电池/定位/相机…)
// 在桌面上要么给真实值 (电量、网络), 要么诚实地缺省 (定位 -> unavailable),
// 绝不瞎编。
//
// ## 能力声明口径 (重要)
//
// 内核只会调用这些方法 (native_device.go / native_app.go / native_permission.go
// 的 nativeHostLocked + host.Call 调用点, 逐个核对过):
//
//	device.info         —— getSystemInfo() 走这里 (deviceHostOverlay)
//	device.vibrate      —— vibrate() 软调用
//	device.brightness   —— getBrightness() / setBrightness() 拉取型
//	device.openSettings —— openSystemSettings(kind) 拉取型
//	app.orientation     —— setOrientation() (内核没实现, 见下)
//	permission.get      —— permission.get() 拉取型
//
// 两个**不在**列表里的:
//
//	battery / network  —— 纯上报型, 内核从不 Call 宿主 (native_device.go 文件头
//	                      的设计), 只由宿主 ReportBattery / ReportNetwork 喂
//	                      缓存快照。所以宿主不必声明它们, 更不用实现 Call。
//	device.id          —— jsDeviceID() 直接读 deviceInfo() 的 DeviceID 字段,
//	                      而 deviceInfo() 来自 device.info 的结果覆盖, 不需要
//	                      单独的 device.id 方法。
//
// **不许多报**: 声明了但 Call 返回 unsupported 会让 canIUse 说谎
// (native.go 文件头)。下表与内核调用点严格一一对应, 加方法名先加调用点。
//
// ## 主动上报
//
// 电池/网络是推送型, 但桌面没有系统级推送事件。所以 init 时主动报一次当前值,
// 让脚本启动后立刻读 battery()/network() 就能拿到真值 (而不是"没人报过"的
// 缺省)。之后不再主动重报 —— 桌面场景电量/网络变化不频繁, 需要实时性时由
// 应用自己定时 Report (或等后续接 WM_POWERBROADCAST)。
//
// ## 线程纪律
//
// Call 在 GUI 线程被调 (native.go 文件头)。本宿主的 Call 全是同步可得的
// (GetSystemPowerStatus / 注册表不阻塞), 直接返回即可; init 里的主动上报发生在
// 进程启动阶段, 天然在 GUI 线程。
//
// ## 零 cgo / 零新依赖
//
// 只用标准库 syscall 调 kernel32/user32/advapi32/ntdll/iphlpapi, 与 win32.go
// 的既有范式一致 (NewLazyDLL + NewProc + 结构体 ABI 对齐 + runtime.KeepAlive)。

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
)

// ===== 能力声明 =====

// win32Capabilities 是桌面宿主会实现的方法名。与内核调用点一一对应 (见文件头)。
var win32Capabilities = []string{
	"device.info",
	"device.vibrate",
	"device.brightness",
	"device.openSettings",
	// app.orientation / permission.get 由移动壳报; 桌面内核调用点存在但
	// 语义上桌面没有对应实现 (见 Call 里的分支注释)。
}

// win32Host 是 gfx.NativeHost 的 win32 实现。
//
// 无状态: 所有数据都现取, 不需要持锁。宿主是进程级唯一实例, 由 init 注册一次。
type win32Host struct{}

// Capabilities 报告宿主会哪些方法 (gfx.NativeHost 接口)。
func (h *win32Host) Capabilities() []string { return win32Capabilities }

// Call 执行一个原生方法 (gfx.NativeHost 接口)。
//
// 同步可得 → 直接返回; 不可得 → 返回明确的错误 (ErrUnsupported / ErrUnavailable),
// 绝不用 Pending 吊着脚本 (桌面没有异步回填路径, Pending 只会让 Promise 永远
// 悬着)。所有分支都返回确定性结果 —— 与"降级口径"一致 (native.go 文件头)。
func (h *win32Host) Call(method string, args object.Value) gfx.NativeCallResult {
	switch method {
	case "device.info":
		return gfx.NativeResult(hostDeviceInfoJS())
	case "device.vibrate":
		// 桌面没有震动硬件。返回 unsupported 而不是静默成功:
		// 调用方写 vibrate() 是想震, 假成功会让他以为震了。
		return gfx.NativeFailure(gfx.ErrUnsupported, "桌面没有震动硬件 (vibrate 仅移动端支持)")
	case "device.brightness":
		// 读亮度需要 WMI / 显示器 DDC, 纯 syscall 拿不到稳定值 —— 诚实返回
		// unavailable 而不是瞎猜 (假数据比没有更糟)。
		return gfx.NativeFailure(gfx.ErrUnavailable, "桌面无法读取屏幕亮度 (请用系统设置)")
	case "device.openSettings":
		return hostOpenSettings(args)
	case "app.orientation":
		// 桌面窗口的旋转/方向由用户控制, 应用不该替用户改。返回 unsupported,
		// setOrientation() 会得到 false。
		return gfx.NativeFailure(gfx.ErrUnsupported, "桌面不支持应用级方向锁定")
	case "permission.get":
		// 桌面没有运行时权限系统 (Win32 没有 Android/iOS 那套权限模型)。
		// 返回 unsupported, permission.get() 会 reject —— 应用用
		// canIUse("permission") 事前判断。
		return gfx.NativeFailure(gfx.ErrUnsupported, "桌面没有运行时权限模型 (permission 仅移动端支持)")
	default:
		return gfx.NativeFailure(gfx.ErrUnsupported, "宿主未实现方法 %q", method)
	}
}

// ===== device.info =====

// hostDeviceInfoJS 组装完整的 device.info 结果对象。
//
// 字段约定见 gfx/native_device.go 的 DeviceInfo 与 deviceInfoToJS (内核的
// deviceHostOverlay 会把这层结果覆盖到本机缺省之上, 所以这里只补"内核算不准
// 或不知道"的字段: 机型/系统名/版本/设备ID/应用名)。
func hostDeviceInfoJS() object.Value {
	o := object.NewObject()
	now := time.Now()
	_, tzOff := now.Zone()
	lang, region := hostLocale()
	osName, osVer := hostOSVersion()
	prim := primaryDisplay()

	set := func(k string, v string) { o.SetProperty(k, object.NewString(v)) }
	set("platform", "windows")
	set("os", osName)
	set("osVersion", osVer)
	set("arch", runtime.GOARCH)
	set("model", hostModel())
	set("brand", "microsoft")
	set("manufacturer", "microsoft")
	set("deviceId", hostDeviceID())
	set("locale", lang)
	set("language", lang)
	set("region", region)
	set("timezone", now.Location().String())
	set("appName", hostAppName())
	set("appVersion", "0.0.0") // 桌面壳不承诺版本号; 由宿主/打包器覆盖
	set("appBuild", "")
	o.SetProperty("tzOffset", object.NewNumber(float64(tzOff/60)))
	o.SetProperty("sdkVersion", object.NewNumber(0))
	o.SetProperty("screenWidth", object.NewNumber(float64(prim.W)))
	o.SetProperty("screenHeight", object.NewNumber(float64(prim.H)))
	o.SetProperty("pixelRatio", object.NewNumber(prim.Scale))
	o.SetProperty("isEmulator", object.NewBoolean(false))
	// 桌面窗口大不等于平板形态: isTablet 是移动端大屏判据, 桌面恒 false。
	o.SetProperty("isTablet", object.NewBoolean(false))
	return o
}

// hostModel 从注册表取设备型号 (BIOS SystemFamily / SystemProductName, 如
// "XPS 13 9340")。读不到就回退 "Windows PC" —— 型号是展示性字段, 不该让
// device.info 整体失败。
func hostModel() string {
	if v, ok := readRegistryString(syscall.HKEY_LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, "SystemFamily"); ok && v != "" {
		return v
	}
	if v, ok := readRegistryString(syscall.HKEY_LOCAL_MACHINE,
		`HARDWARE\DESCRIPTION\System\BIOS`, "SystemProductName"); ok && v != "" {
		return v
	}
	return "Windows PC"
}

// hostAppName 从模块名猜应用名 (去掉 .exe 后缀)。没有更可靠的入口 —— 应用名
// 是展示性字段, 猜不到就 "Gox App"。
func hostAppName() string {
	exe, err := os.Executable()
	if err != nil {
		return "Gox App"
	}
	base := exe
	if i := strings.LastIndexByte(base, '\\'); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(base, ".exe")
	if base == "" {
		return "Gox App"
	}
	return base
}

// ===== device.id =====

// hostDeviceID 取一个稳定的设备标识。
//
// 实现: 注册表 MachineGuid (HKEY_LOCAL_MACHINE\SOFTWARE\Microsoft\Cryptography)
// —— Windows 安装时生成、重装系统才变, 正好满足"同一台机器稳定"的语义。
//
// **隐私口径 (写在这里而不是外链文档)**: 这个值是稳定的机器标识, 且不含任何
// 哈希 —— 它可被反查、可跨应用拼接同一台机器。回退分支用的主机名 + 用户名更是
// 直接可识别个人的信息。内核只负责"给应用一个稳定的机器标识", **不替应用决定
// 要不要上报** —— 应用若把它发到服务端, 告知与同意由应用自己承担。
func hostDeviceID() string {
	if v, ok := readRegistryString(syscall.HKEY_LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Cryptography`, "MachineGuid"); ok && v != "" {
		return "win:" + v
	}
	return "win:" + hostnameFallback()
}

// hostnameFallback 是 MachineGuid 读不到时的兜底: 主机名 + 用户名 (尽力稳定,
// 但不承诺跨重装不变)。
func hostnameFallback() string {
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}
	return strings.TrimSpace(host) + "/" + strings.TrimSpace(user)
}

// ===== 注册表读取 (只读, 懒加载) =====

var (
	regAdvapi       = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyW = regAdvapi.NewProc("RegOpenKeyExW")
	procRegQueryVal = regAdvapi.NewProc("RegQueryValueExW")
	procRegCloseKey = regAdvapi.NewProc("RegCloseKey")
)

const (
	regKeyRead = 0x20019 // KEY_READ
)

// readRegistryString 读一个 REG_SZ 值。失败返回 ok=false (调用点决定回退)。
func readRegistryString(root syscall.Handle, path, name string) (string, bool) {
	var h syscall.Handle
	r, _, _ := procRegOpenKeyW.Call(uintptr(root),
		uintptr(unsafe.Pointer(mustUTF16Ptr(path))), 0, regKeyRead,
		uintptr(unsafe.Pointer(&h)))
	if r != 0 || h == 0 {
		return "", false
	}
	defer procRegCloseKey.Call(uintptr(h))
	var size uint32
	// 第一次调用只问长度 (lpcbData 传 0 会返回 ERROR_MORE_DATA)
	r, _, _ = procRegQueryVal.Call(uintptr(h),
		uintptr(unsafe.Pointer(mustUTF16Ptr(name))), 0, 0,
		0, uintptr(unsafe.Pointer(&size)))
	if r != 0 || size == 0 {
		return "", false
	}
	buf := make([]uint16, (size+1)/2+1)
	r, _, _ = procRegQueryVal.Call(uintptr(h),
		uintptr(unsafe.Pointer(mustUTF16Ptr(name))), 0, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		return "", false
	}
	// 去掉结尾 NUL
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return syscall.UTF16ToString(buf[:n]), true
}

// mustUTF16Ptr 转 UTF16 指针 (调用点全是常量/环境变量, 不会含内嵌 NUL)。
func mustUTF16Ptr(s string) *uint16 {
	p, _ := syscall.UTF16PtrFromString(s)
	return p
}

// ===== 系统版本 / 语言 =====

// hostOSVersion 返回 (人类可读名, 机器可比较版本号)。
//
// RtlGetVersion (ntdll) 比 GetVersionEx 可靠: GetVersionEx 从 Win8.1 起被
// 版本遮蔽 (manifest 声明前恒返回 6.2), RtlGetVersion 不受影响。
var (
	ntdll       = syscall.NewLazyDLL("ntdll.dll")
	procRtlGetV = ntdll.NewProc("RtlGetVersion")
	// GetUserDefaultUILanguage 在 kernel32.dll (Vista+), 不在 user32。
	procGetUserD = kernel32.NewProc("GetUserDefaultUILanguage")
)

type osVersionInfoExW struct {
	OSVersionInfoSize uint32
	MajorVersion      uint32
	MinorVersion      uint32
	BuildNumber       uint32
	PlatformID        uint32
	CSDVersion        [128]uint16
}

// hostBuild 缓存一次 RtlGetVersion 的 build 号 (判定 Win11 用, 避免每次
// device.info 都重查)。
var hostBuild uint32

// osBuild 读一次 RtlGetVersion 并缓存 build 号。返回 (名称, 版本号)。
func hostOSVersion() (name, version string) {
	vi := osVersionInfoExW{OSVersionInfoSize: uint32(unsafe.Sizeof(osVersionInfoExW{}))}
	if r, _, _ := procRtlGetV.Call(uintptr(unsafe.Pointer(&vi))); r == 0 {
		version = fmt.Sprintf("%d.%d.%d", vi.MajorVersion, vi.MinorVersion, vi.BuildNumber)
		hostBuild = vi.BuildNumber
		name = "Windows " + winVersionName(vi.MajorVersion, vi.MinorVersion)
	} else {
		version = "unknown"
		name = "Windows"
	}
	return name, version
}

// winVersionName 把主/次版本号映射成人类可读的名字 (只覆盖常见值, 其余
// 回退空串, 由调用点拼 "Windows " 前缀)。
func winVersionName(major, minor uint32) string {
	switch {
	case major == 10 && minor >= 0 && hostBuild >= 22000:
		return "11"
	case major == 10:
		return "10"
	case major == 6 && minor == 3:
		return "8.1"
	case major == 6 && minor == 2:
		return "8"
	case major == 6 && minor == 1:
		return "7"
	case major == 6 && minor == 0:
		return "Vista"
	case major == 5 && minor == 1:
		return "XP"
	default:
		return ""
	}
}

// hostLocale 从用户默认 UI 语言取语言/地区 (如 0x0804 → zh/CN)。
func hostLocale() (lang, region string) {
	return localeFromLCID(hostUserDefaultUILanguage())
}

func hostUserDefaultUILanguage() uint32 {
	r, _, _ := procGetUserD.Call()
	return uint32(r)
}

// localeFromLCID 把 Windows LCID 映射成语言/地区代码。
//
// 只覆盖常见值; 认不出返回空 (与内核 localeFromEnv 同一口径: 宁可留空不瞎猜)。
func localeFromLCID(lcid uint32) (lang, region string) {
	switch lcid & 0xFFFF {
	case 0x0804: // zh-CN
		return "zh", "CN"
	case 0x0404: // zh-TW
		return "zh", "TW"
	case 0x0409: // en-US
		return "en", "US"
	case 0x0411: // ja-JP
		return "ja", "JP"
	case 0x0412: // ko-KR
		return "ko", "KR"
	case 0x040c: // fr-FR
		return "fr", "FR"
	case 0x0407: // de-DE
		return "de", "DE"
	case 0x0410: // it-IT
		return "it", "IT"
	case 0x040a: // es-ES
		return "es", "ES"
	case 0x0419: // ru-RU
		return "ru", "RU"
	case 0x0416: // pt-BR
		return "pt", "BR"
	default:
		return "", ""
	}
}

// primaryDisplay 取主屏几何 (复用本包 factory 的多屏枚举, 与 display.go 同源;
// 找不到退回 1920x1080@1)。
func primaryDisplay() gfx.Display {
	var f factory
	ds := f.Displays()
	for _, d := range ds {
		if d.Primary {
			return d
		}
	}
	if len(ds) > 0 {
		return ds[0]
	}
	return gfx.Display{W: 1920, H: 1080, Scale: 1, Primary: true}
}

// ===== 电池 (推送型, 但桌面没有系统推送 → 由本文件主动报) =====

// systemPowerStatus 调 GetSystemPowerStatus (kernel32)。
var procGetPowerStatus = kernel32.NewProc("GetSystemPowerStatus")

// systemPowerStatus 对应 SYSTEM_POWER_STATUS (字节对齐, 与文档一致)。
type systemPowerStatus struct {
	ACLineStatus        byte
	BatteryFlag         byte
	BatteryLifePercent  byte
	SystemStatusFlag    byte
	BatteryLifeTime     uint32
	BatteryFullLifeTime uint32
}

func querySystemPowerStatus() (systemPowerStatus, bool) {
	var s systemPowerStatus
	r, _, _ := procGetPowerStatus.Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return s, false
	}
	return s, true
}

// hostBatteryState 把 SYSTEM_POWER_STATUS 原始结构转成内核 BatteryState。
//
// 口径与 gfx/native_device.go 的缺省值一致: 无电池 (台式机) 时 Supported=false、
// Level=-1 —— 脚本不需要为桌面写特判。
func hostBatteryState(s systemPowerStatus) gfx.BatteryState {
	b := gfx.BatteryState{
		Supported:    true,
		ChargingType: "unknown",
		Temperature:  -1,
	}
	// ACLineStatus: 0 = 电池供电, 1 = 接 AC, 2 = UPS
	b.Charging = s.ACLineStatus == 1
	if s.ACLineStatus == 1 {
		b.ChargingType = "ac"
	}
	// BatteryLifePercent: 0..100, 255 = 未知
	if s.BatteryLifePercent != 255 {
		b.Level = float64(s.BatteryLifePercent) / 100.0
	} else {
		b.Level = -1
	}
	return b
}

// ===== 网络 (推送型, 同上) =====

// hostNetworkType 枚举网卡判断"有没有连上 + 什么类型"。
//
// GetAdaptersInfo 是纯 syscall 可达的最轻方案 (iphlpapi.dll)。判断规则:
//
//	connected —— 任一网卡分配了**有效 IPv4 地址** (非空且非 0.0.0.0)。
//	             为什么不用 OperStatus: IP_ADAPTER_INFO **没有** OperStatus
//	             字段 (那是更重的 IP_ADAPTER_ADDRESSES 里的), "有地址"是
//	             纯 syscall 下判断"这块网卡能用"最可靠且跨版本的信号。
//	type      —— 按网卡类型: 有地址的无线网卡 → wifi; 有线 → ethernet;
//	              PPP → vpn; 其他 → other。
var (
	iphlpapi            = syscall.NewLazyDLL("iphlpapi.dll")
	procGetAdaptersInfo = iphlpapi.NewProc("GetAdaptersInfo")
)

const (
	ifTypeEthernetCSMACD = 6
	ifTypeIEEE80211      = 71
	ifTypePPP            = 23
	// ipHelperBufSize 枚举缓冲 (家用/办公机网卡数极少, 16 条 × 4KB 足够)。
	ipHelperBufSize = 16 * 4096
)

// ipAddrString 对应 IP_ADDR_STRING (IPv4 地址链, 紧凑结构无 padding 风险)。
type ipAddrString struct {
	Next      *ipAddrString
	IpAddress [16]byte // IP_ADDRESS_STRING, NUL 结尾 ASCII, 如 "192.168.1.5"
	IpMask    [16]byte
	Context   uint32
}

// ipAdapterInfo 对应 IP_ADAPTER_INFO 头部的定长字段 (到 IpAddressList 为止)。
//
// 只声明到 IpAddressList 足够: 判断"有没有地址"只需遍历这条链; 再往后的
// GatewayList/Lease* 用不到。字段类型/顺序/尺寸严格按 MSDN 文档, 指针字段的
// 8 字节对齐由 Go 自动处理, 与 C 布局一致。
type ipAdapterInfo struct {
	Next             *ipAdapterInfo
	ComboIndex       uint32
	AdapterName      [132]byte // MAX_ADAPTER_NAME_LENGTH(128)+4
	Description      [132]byte // MAX_ADAPTER_DESCRIPTION_LENGTH(128)+4
	AddressLength    uint32
	Address          [8]byte // MAX_ADAPTER_ADDRESS_LENGTH
	Index            uint32
	Type             uint32
	DhcpEnabled      uint32
	CurrentIpAddress *ipAddrString
	IpAddressList    ipAddrString
}

// adapterHasIP 遍历这块网卡的 IPv4 地址链, 任一非空且非 0.0.0.0 即算"有地址"。
func adapterHasIP(a *ipAdapterInfo) bool {
	addr := &a.IpAddressList
	for addr != nil {
		if s := asciiCStr(&addr.IpAddress); s != "" && s != "0.0.0.0" {
			return true
		}
		addr = addr.Next
	}
	return false
}

// asciiCStr 把 NUL 结尾的 ASCII 字节数组转 string (IP_ADDRESS_STRING 是 ASCII)。
func asciiCStr(b *[16]byte) string {
	n := 0
	for n < len(b) && b[n] != 0 {
		n++
	}
	return string(b[:n])
}

func hostNetworkType() (connected bool, typ string) {
	var buf [ipHelperBufSize]byte
	size := uint32(len(buf))
	r, _, _ := procGetAdaptersInfo.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)))
	if r != 0 {
		// 枚举失败 (无网卡 / 权限不足): 诚实报未连接
		return false, "none"
	}
	hasWifi, hasEth, hasPPP := false, false, false
	p := (*ipAdapterInfo)(unsafe.Pointer(&buf[0]))
	for p != nil {
		if adapterHasIP(p) {
			connected = true
			switch p.Type {
			case ifTypeIEEE80211:
				hasWifi = true
			case ifTypeEthernetCSMACD:
				hasEth = true
			case ifTypePPP:
				hasPPP = true
			}
		}
		p = p.Next
	}
	if !connected {
		return false, "none"
	}
	switch {
	case hasWifi:
		return true, "wifi"
	case hasEth:
		return true, "ethernet"
	case hasPPP:
		return true, "vpn"
	default:
		return true, "other"
	}
}

// ===== device.openSettings =====

// hostOpenSettings 处理 device.openSettings —— 用系统协议打开指定设置页。
//
// 参数是 `kind` (内核 jsOpenSystemSettings 传 propObject("kind", …), 见
// native_device.go:551)。白名单与 settingKindList (native_device.go:560) 对齐,
// 桌面能打开的页有限, 映射到 ms-settings: 协议:
//
//	"app"          → 设置 > 应用
//	"wifi"         → 设置 > 网络 > Wi-Fi
//	"bluetooth"    → 设置 > 蓝牙
//	"location"     → 设置 > 隐私 > 位置
//	"notification" → 设置 > 通知
//	"display"      → 设置 > 显示
//	"sound"        → 设置 > 声音
//	"battery"      → 设置 > 电源与电池
//	"date"         → 设置 > 日期和时间
//	"privacy"      → 设置 > 隐私和安全性
//	"storage"      → 设置 > 存储
//
// 不认识的页返回 invalid-arg (openSystemSettings 返回 false)。
//
// **这张表必须覆盖 gfx.SettingKinds() 的每一项** —— 内核放行而这里没有的 kind
// 会静默退化成"这个平台没实现"(openSystemSettings 返回 false), 应用查不出原因。
// "privacy" 就这样漏过一次 (补上时 win32 的单测同时加了逐项核对)。定位用的是
// 更深的 privacy-location 页, 而 "privacy" 指隐私总览 —— 两者不是同一个页面,
// 别为了"省一行"把它们合并。
var settingsURIs = map[string]string{
	"app":          "ms-settings:appsfeatures",
	"wifi":         "ms-settings:network-wifi",
	"bluetooth":    "ms-settings:bluetooth",
	"location":     "ms-settings:privacy-location",
	"notification": "ms-settings:notifications",
	"display":      "ms-settings:display",
	"sound":        "ms-settings:sound",
	"battery":      "ms-settings:powersleep",
	"date":         "ms-settings:dateandtime",
	"privacy":      "ms-settings:privacy",
	"storage":      "ms-settings:storagesense",
}

// settingURI 把 kind 映射成 ms-settings: URI (纯函数)。
//
// 拆出来是为了可测: hostOpenSettings 会真的调 ShellExecuteW 弹出系统设置页,
// 所以它的成功路径**不能在单测里走** (真会打开窗口)。把查表独立出来之后,
// "映射表是否正确/完整"就能脱离副作用单独断言 —— 这正是上面那张表漂移
// (少 privacy) 却长期没人发现的原因。
func settingURI(kind string) (string, bool) {
	uri, ok := settingsURIs[kind]
	return uri, ok
}

func hostOpenSettings(args object.Value) gfx.NativeCallResult {
	kind := nativeArgString(args, "kind")
	if kind == "" {
		kind = "app"
	}
	uri, ok := settingURI(kind)
	if !ok {
		// 走到这里**只可能是宿主漏覆盖**: 内核的 jsOpenSystemSettings 会用
		// SettingKinds() 先拦一遍, 它不认识的 kind 根本到不了宿主。所以这不是
		// "调用方写错参数", 而是"这个平台没实现这个页" ⇒ 报 unsupported
		// (报 invalid-arg 会把排查方向带偏到调用方身上)。
		return gfx.NativeFailure(gfx.ErrUnsupported, "桌面未实现设置页 %q", kind)
	}
	// 用 ShellExecuteW (shell32) 打开 ms-settings: 协议 —— 比 rundll32 的
	// FileProtocolHandler 更直接, 且不依赖 cmd。
	procShellExecute := shell32.NewProc("ShellExecuteW")
	uriPtr, _ := syscall.UTF16PtrFromString(uri)
	// ShellExecuteW(hwnd, op, file, params, dir, show) —— op=open,
	// file=uri, 其余 nil。返回值 >32 表示成功。
	r, _, _ := procShellExecute.Call(0, 0, uintptr(unsafe.Pointer(uriPtr)), 0, 0, 1)
	if r > 32 {
		return gfx.NativeResult(object.NewBoolean(true))
	}
	return gfx.NativeFailure(gfx.ErrPlatform, "打开设置页失败 (%s)", uri)
}

// shell32 懒加载 (hostOpenSettings 用)。
var shell32 = syscall.NewLazyDLL("shell32.dll")

// nativeArgString 从 args 对象取字符串属性 (缺省空串)。
func nativeArgString(args object.Value, key string) string {
	o, ok := args.(*object.Object)
	if !ok {
		return ""
	}
	v, _ := o.GetProperty(key)
	if s, ok := v.(*object.String); ok {
		return s.Value
	}
	return ""
}

// ===== 注册与主动上报 =====

// hostOnce 保证宿主只注册一次 (init 可能被测试多次触发)。
var hostOnce sync.Once

func init() {
	hostOnce.Do(func() {
		gfx.SetNativeHost(&win32Host{})
		// 主动上报一次当前值: 脚本在启动后立刻读 battery()/network() 时,
		// 拿到的是缓存快照而不是"还没人报过"的缺省值。
		//
		// 桌面没有电池/网络变化的系统级推送, 这里只报初值; 之后的更新靠
		// surface 收到 WM_POWERBROADCAST / WM_WTSSESSION_CHANGE 时再报
		// (目前未接, 缺省即"启动时状态")。
		if s, ok := querySystemPowerStatus(); ok {
			gfx.ReportBattery(hostBatteryState(s))
		}
		connected, typ := hostNetworkType()
		gfx.ReportNetwork(gfx.NetworkState{
			Connected:          connected,
			Type:               typ,
			Metered:            false, // 桌面默认不计费
			SSID:               "",
			Strength:           -1,
			Carrier:            "",
			CellularGeneration: "",
		})
	})
}
