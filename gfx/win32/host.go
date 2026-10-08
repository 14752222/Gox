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
// 移动端这个契约是给 Kotlin/Swift 宿主实现的 (agent_doc/mobile-port-plan.md; agent_doc/ 已迁往项目共享资产盘, 仓库不留副本);
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
// 电池/网络是推送型, 但桌面没有系统级推送事件, 所以由宿主补一次当前值。
// 两者**时机不同** (2026-10-08 事故后分裂):
//
//	battery —— init 时主动报一次。GetSystemPowerStatus 是纯读、不会崩, 让脚本
//	           启动后立刻读 battery() 就拿到真值。
//	network —— **懒加载**, 不在 init 里枚举。网卡枚举要动 GetAdaptersInfo 的变长
//	           缓冲, 一旦布局或边界判断出错就是 SIGSEGV; 放在 init 里会把"这个
//	           能力崩了"放大成"进程起不来"。改到宿主首次被 Call 时才枚举
//	           (reportNetworkLazy), CLI/test262 路径因此完全不触碰网卡。
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
	// 网卡枚举的懒加载点: 走到这里说明宿主能力真的被用到了 (CLI/跑测路径
	// 从不 Call 宿主 ⇒ 永不枚举, 见 reportNetworkLazy 的注释)。
	reportNetworkLazy()
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

	// errBufferOverflow 是 Win32 ERROR_BUFFER_OVERFLOW (111): 传 nil 缓冲探尺寸
	// 时 GetAdaptersInfo 用这个码告诉你"要多少字节"。用字面量是因为 syscall
	// 包里没有这个常量 (Errno 在 Windows 上与 errno 不同源)。
	errBufferOverflow = 111
)

// IP_ADAPTER_INFO / IP_ADDR_STRING 的**实测**布局常量。
//
// 为什么不用 Go struct: 手写 struct 靠的是"Go 的自动 padding 恰好等于 C 的
// ABI"这一假设, 而该假设在这里**不成立**, 症状是启动即 SIGSEGV。
//
// 2026-10-08 实测 (Windows 10.0.19045, Go 1.2x, 用 GetAdaptersInfo 回填缓冲
// 逐字段交叉验证; 判据是 AdapterName/Description 的 ASCII 串位置 + Next 指针链
// 给出的记录跨度):
//
//	AdapterName  @12  (128 字节, 结束于 @139) —— 与 Go 布局一致
//	Description  @144 (128 字节, 内容实测落在 @272 = 144+128) —— Go 布局恰好
//	             也把 Description 放在 @144, 但那是**巧合**: Go 因为看到后面
//	             有 uint32/指针而给两个 [132]byte 数组补了 4 字节尾巴, C 侧则是
//	             AdapterName 之后补 4 字节、Description 之后**不补**。
//	AddressLength@276 / Address@280 / Index@288 / Type@292 / DhcpEnabled@296
//	CurrentIpAddress @304 / IpAddressList @648
//	单条记录跨度 = 704 字节 (IpAddressList 起 656, 到记录尾 704, 即 IP_ADDR_STRING
//	链尾之后还有 GatewayList/DhcpServer/... 各 48 字节)
//
// 要命的是最后一行: Go 布局把 IpAddressList 放在 @312, 真实位置是 @648, 差 336
// 字节 ⇒ 旧代码 `a.IpAddressList.Next` 取到的是缓冲区里一段**普通数据**, 当成
// 指针解引用, 稳定命中 fault addr 0x322b。
//
// 结论: 一律按显式偏移读, 并让偏移越界判断成为获取数据的前置条件。
const (
	ipAdapterInfoSize = 704 // 单条 IP_ADAPTER_INFO 的实测总跨度 (按 Next 链量得)

	offAdapterName    = 12
	offDescription    = 144
	offAddressLength  = 276
	offIndex          = 288
	offAdapterType    = 292
	offDhcpEnabled    = 296
	offIpAddressList  = 648 // 最后一个字段, 是内嵌的 IP_ADDR_STRING (非指针)
	ipAddrStringSize  = 48  // IP_ADDR_STRING 总长: Next(8)+IpAddress(16)+IpMask(16)+Context(4)+padding(4)
	offAddrStringNext = 0
	offAddrStringIP   = 8 // IpAddress 在 IP_ADDR_STRING 内
	offAddrStringMask = 24
)

// ipBuf 读取 GetAdaptersInfo 回填的变长缓冲。
//
// 全部读取都过 bound 检查: 越界返回零值而不是 panic —— 结构是变长的, 任何
// "按布局算出来的位置"都必须先证明它落在内核回填的 size 之内。
type ipBuf struct {
	b []byte
}

func (r ipBuf) u32(off int) uint32 {
	if off < 0 || off+4 > len(r.b) {
		return 0
	}
	return uint32(r.b[off]) | uint32(r.b[off+1])<<8 | uint32(r.b[off+2])<<16 | uint32(r.b[off+3])<<24
}

// ptr 读 8 字节指针的低位部分 (用于 Next 链; 高位在 64 位进程里非零, 但只需要
// 判断是否为空以及算相对偏移)。
func (r ipBuf) ptr(off int) uint64 {
	if off < 0 || off+8 > len(r.b) {
		return 0
	}
	var v uint64
	for i := 7; i >= 0; i-- {
		v = v<<8 | uint64(r.b[off+i])
	}
	return v
}

// cstr 读 NUL 结尾 ASCII (IP_ADDRESS_STRING / MAX_ADAPTER_*_LENGTH 都是 ASCII)。
func (r ipBuf) cstr(off, n int) string {
	if off < 0 || off+n > len(r.b) {
		return ""
	}
	s := r.b[off : off+n]
	i := 0
	for i < len(s) && s[i] != 0 {
		i++
	}
	return string(s[:i])
}

// hasIPv4 走这块网卡的 IP_ADDR_STRING 链, 任一非空且非 0.0.0.0 即算"有地址"。
//
// 链的每一跳都用回填 size 做硬边界 (链是内核填的, 但指针值本身不可信 —— 旧代码
// 正是死在"信了一个错位处读出的指针")。
func (r ipBuf) hasIPv4(recOff int) bool {
	// 起点是内嵌的 IpAddressList (不是指针), 直接用记录的起始位置算绝对偏移。
	off := recOff + offIpAddressList
	for hop := 0; hop < 64; hop++ {
		if s := r.cstr(off+offAddrStringIP, 16); s != "" && s != "0.0.0.0" {
			return true
		}
		next := r.ptr(off + offAddrStringNext)
		if next == 0 {
			return false
		}
		// Next 是指向同缓冲内的绝对指针 ⇒ 转成相对偏移再校验
		delta := int(next) - int(r.baseAddr())
		if delta < 0 || delta+ipAddrStringSize > len(r.b) {
			return false
		}
		off = delta
	}
	return false
}

// baseAddr 是缓冲首字节的绝对地址 (算 Next 相对偏移用)。
func (r ipBuf) baseAddr() uintptr {
	if len(r.b) == 0 {
		return 0
	}
	return uintptr(unsafe.Pointer(&r.b[0]))
}

// hostNetworkType 枚举网卡判断"有没有连上 + 什么类型"。
//
// 注意**不**在包 init() 里调 (见 init 的注释): 它是懒加载的, 只有真正查询
// network() 时才走这里 —— 从根上把"网卡枚举崩掉"的影响面收回到单个能力。
func hostNetworkType() (connected bool, typ string) {
	// 先探所需尺寸: 传 nil 缓冲, 规范行为是返回 ERROR_BUFFER_OVERFLOW(111)
	// 并回填所需字节数。
	//
	// ⚠️ 不能像旧代码那样"传一个大缓冲然后信回填 size": 实测在本机上直接传
	// 64KB 缓冲会返回 0(成功) 而 **size 保持传入值不变**, 于是"size 就是真实
	// 长度"这个前提根本不成立。
	var need uint32
	if r, _, _ := procGetAdaptersInfo.Call(0, uintptr(unsafe.Pointer(&need))); r != errBufferOverflow {
		// 无网卡 / 枚举不可用: 诚实报未连接
		return false, "none"
	}
	if need == 0 || need > 1<<20 {
		return false, "none" // 尺寸荒谬, 不分配
	}
	buf := make([]byte, need)
	size := need
	if r, _, _ := procGetAdaptersInfo.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size))); r != 0 {
		return false, "none"
	}
	// 硬边界 = 内核回填的 size 与 分配长度 的较小者 (回填可能被改成更大的值)
	n := int(size)
	if n <= 0 || n > len(buf) {
		n = len(buf)
	}
	rec := ipBuf{b: buf[:n]}

	hasWifi, hasEth, hasPPP := false, false, false
	for off, hop := 0, 0; hop < 32; hop++ {
		// 记录头必须完整落在边界内, 否则停止 (不是 panic)
		if off < 0 || off+offIpAddressList+ipAddrStringSize > n {
			break
		}
		if rec.hasIPv4(off) {
			connected = true
			switch rec.u32(off + offAdapterType) {
			case ifTypeIEEE80211:
				hasWifi = true
			case ifTypeEthernetCSMACD:
				hasEth = true
			case ifTypePPP:
				hasPPP = true
			}
		}
		next := rec.ptr(off)
		if next == 0 {
			break
		}
		delta := int(next) - int(rec.baseAddr())
		if delta <= 0 || delta+offIpAddressList+ipAddrStringSize > n {
			break
		}
		off = delta
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
		// 主动上报一次电池初值: 脚本启动后立刻读 battery() 拿到的是缓存快照
		// 而不是"还没人报过"的缺省值。
		//
		// 桌面没有电池/网络变化的系统级推送, 这里只报初值; 之后的更新靠
		// surface 收到 WM_POWERBROADCAST / WM_WTSSESSION_CHANGE 时再报
		// (目前未接, 缺省即"启动时状态")。
		if s, ok := querySystemPowerStatus(); ok {
			gfx.ReportBattery(hostBatteryState(s))
		}
		// ⚠️ **不**在 init 里枚举网卡 —— 网络快照走懒加载 (reportNetworkLazy)。
		//
		// 2026-10-08 事故: 网卡枚举放在 init 里, 而它一旦崩 (当时是 IP_ADAPTER_INFO
		// 布局错位) **任何**进程启动路径都会 SIGSEGV —— test262 跑测完全用不到
		// GUI, 却因为包 init 秒崩, 全量 A/B 验收链路整条失效。
		// 收敛做法: 枚举推迟到**宿主第一次被真正调用**时 (Call 入口), CLI/跑测
		// 路径根本不触碰 ⇒ 枚举崩了也只崩这一个能力, 不影响进程启动。
		//
		// 代价 (已知并接受): 脚本在做过任何一次 native 调用之前读 network() 会
		// 拿到缺省值(未连接)。换来的收益是"进程一定起得来"。
	})
}

// networkOnce 保证网卡枚举**至多一次**, 且只在宿主首次被真正调用时才发生。
var networkOnce sync.Once

// reportNetworkLazy 懒加载版的上报网络快照: 枚举网卡并上报一次。
//
// 只有 win32Host.Call 会走这里 —— 即"宿主能力真的被用到"的时刻。CLI / test262
// 跑测从不 Call 宿主, 于是网卡枚举在这些路径上**根本不发生**, 这是把 2026-10-08
// 那次"包 init 里枚举 ⇒ 进程起不来"的崩溃面收回来的关键。
//
// 之所以不做成"读 network() 时才枚举": network 是**上报型** (内核从不 Call 宿主,
// 只由 ReportNetwork 喂缓存, 见文件头), 而 gfx.Network() 是持 nativeMu 的纯读
// —— 在它里面回调宿主会与 ReportNetwork 的加锁**重入死锁**。要真正做到"读时才
// 枚举"得改内核加无锁钩子, 属后续项; 当前取舍是"宁可首次读拿到缺省值, 也要保
// 证进程能起来"。
func reportNetworkLazy() {
	networkOnce.Do(func() {
		if connected, typ := hostNetworkType(); connected || typ != "none" {
			reportNetworkSnapshot(connected, typ)
		}
	})
}

// reportNetworkSnapshot 上报一次网络快照 (懒加载路径与初值共用)。
func reportNetworkSnapshot(connected bool, typ string) {
	gfx.ReportNetwork(gfx.NetworkState{
		Connected:          connected,
		Type:               typ,
		Metered:            false, // 桌面默认不计费
		SSID:               "",
		Strength:           -1,
		Carrier:            "",
		CellularGeneration: "",
	})
}
