//go:build windows

package win32

// ===== 桌面宿主单测 (host.go 的返回值形状验证) =====
//
// 与 display_test.go 同一风格: 在真机上跑一遍宿主实现, 验证"返回值形状"
// (字段存在、类型对、口径对) —— 这些代码写错了不会崩, 只会静默给脚本一个
// 缺字段/假字段的对象, 而脚本端多半不报错 (undefined 一路传下去), 所以值得
// 单独钉住。

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
)

// TestHostCapabilities 验证能力声明与内核调用点一一对应 (host.go 文件头)。
//
// 这里不钉死具体清单 (未来加能力是常态), 但钉住几条不变量:
//   - 桌面宿主**必须**声明 device.info (getSystemInfo 的覆盖来源);
//   - **不得**声明 battery / network (纯上报型, 声明了 canIUse 会说谎);
//   - 不得声明移动端专属能力 (camera / gallery / location …)。
func TestHostCapabilities(t *testing.T) {
	h := &win32Host{}
	caps := h.Capabilities()
	got := map[string]bool{}
	for _, c := range caps {
		got[c] = true
	}
	if !got["device.info"] {
		t.Fatalf("桌面宿主必须声明 device.info, 实际: %v", caps)
	}
	// 下面这批禁止项**不是**冗余断言, 别当重复删掉: CanIUse 的判据第 1 步就是
	// "宿主声明过 → 可用", 所以声明与否**真的会改变** canIUse 的结论。
	//
	//   battery / network —— 纯上报型。内核**从不** Call 宿主 (数据是宿主主动
	//     Report 进来的, 见 native.go 文件头)。声明它们会让 canIUse("battery")
	//     说 true, 而那个 true 对应不到任何一次 Call ⇒ 上层按 canIUse 写分支时,
	//     "能不能用"与"怎么用"就对不上了。
	//   camera / gallery / location / permission —— 移动端专属: 桌面没有对应硬件
	//     或系统模型 (permission 在 win32 根本没有运行时权限那套)。声明了同样是
	//     "报了却做不成"。
	for _, forbidden := range []string{"battery", "network", "battery.get", "network.get",
		"camera", "gallery", "location", "permission"} {
		if got[forbidden] {
			t.Fatalf("桌面宿主不得声明 %q (纯上报型/移动端专属), 实际: %v", forbidden, caps)
		}
	}
	// 所有声明的方法, Call 都必须能给出确定性结果 (不 Pending、不 panic)
	for _, m := range caps {
		res := h.Call(m, object.NewObject())
		if res.Pending {
			t.Fatalf("桌面宿主 %q 不该 Pending (没有异步回填路径)", m)
		}
	}
}

// TestHostDeviceInfo 验证 device.info 返回对象的字段形状。
func TestHostDeviceInfo(t *testing.T) {
	h := &win32Host{}
	res := h.Call("device.info", object.NewObject())
	if res.Err != nil {
		t.Fatalf("device.info 不该失败: %v", res.Err)
	}
	o, ok := res.Result.(*object.Object)
	if !ok {
		t.Fatalf("device.info 应返回对象, 实际 %T", res.Result)
	}
	mustStr := func(k string) string {
		t.Helper()
		v, _ := o.GetProperty(k)
		s, ok := v.(*object.String)
		if !ok || s.Value == "" {
			t.Fatalf("device.info.%s 应为非空字符串, 实际 %#v", k, v)
		}
		return s.Value
	}
	mustNum := func(k string, min float64) float64 {
		t.Helper()
		v, _ := o.GetProperty(k)
		n, ok := v.(*object.Number)
		if !ok {
			t.Fatalf("device.info.%s 应为数字, 实际 %T", k, v)
		}
		if n.Value < min {
			t.Fatalf("device.info.%s 应 >= %v, 实际 %v", k, min, n.Value)
		}
		return n.Value
	}
	if p := mustStr("platform"); p != "windows" {
		t.Fatalf("platform 应为 windows, 实际 %q", p)
	}
	mustStr("os")
	mustStr("osVersion")
	mustStr("arch")
	mustStr("model")
	mustStr("brand")
	mustStr("deviceId")
	mustStr("appName")
	mustNum("screenWidth", 1)
	mustNum("screenHeight", 1)
	mustNum("pixelRatio", 1)
	// 派生量 (内核 deviceInfoToJS 也会加, 宿主这里补一份让覆盖更完整)
	if v, _ := o.GetProperty("isMobile"); v != nil {
		if b, ok := v.(*object.Boolean); ok && b.Value {
			t.Fatalf("windows 不该 isMobile")
		}
	}
}

// TestHostDeviceID 验证 device.id 稳定且带 win: 前缀。
func TestHostDeviceID(t *testing.T) {
	id := hostDeviceID()
	if len(id) < 4 || id[:4] != "win:" {
		t.Fatalf("deviceID 应以 win: 开头, 实际 %q", id)
	}
	if id2 := hostDeviceID(); id2 != id {
		t.Fatalf("同一次会话内 deviceID 应稳定: %q vs %q", id, id2)
	}
}

// TestHostBattery 验证电池快照口径:
//   - 系统返回有效电源状态 (ok) → Supported=true;
//   - Level 合法取值: 0..1 (读到了) 或 -1 (读不到百分比, 255 是合法"未知")。
//
// 注意: 真实笔记本接电源时 ACLineStatus=1 但 BatteryLifePercent 可能是 255
// (驱动没报), 所以"Supported && Level==-1"是合法组合, 不是错误 —— 这与
// native_device.go 的缺省口径 (Level=-1=未知) 一致。
func TestHostBattery(t *testing.T) {
	s, ok := querySystemPowerStatus()
	if !ok {
		t.Skip("GetSystemPowerStatus 不可用 (无头/沙箱环境)")
	}
	b := hostBatteryState(s)
	if !b.Supported {
		t.Fatalf("系统返回了电源状态, Supported 应为 true")
	}
	if b.Level < -1 || b.Level > 1 {
		t.Fatalf("level 应 ∈ {-1} ∪ [0,1], 实际 %v", b.Level)
	}
	if b.ChargingType == "" {
		t.Fatalf("充电类型不应为空")
	}
	// Call 路径: battery.get **不**在能力表里 (纯上报型, 见文件头), 所以宿主必须
	// 给出**确定性的失败** —— 不能 Pending 吊着 (桌面没有异步回填路径, 悬着的
	// Promise 永不结算, 症状是"await 之后再也没动静", 比报错难查得多)。
	h := &win32Host{}
	if res := h.Call("battery.get", object.NewObject()); res.Err == nil || res.Pending {
		t.Fatalf("未声明的方法必须确定性失败, 实际 err=%v pending=%v", res.Err, res.Pending)
	}
}
// TestHostNetwork 验证网络快照口径: connected=false 时 type 应为 none。
func TestHostNetwork(t *testing.T) {
	connected, typ := hostNetworkType()
	switch typ {
	case "wifi", "ethernet", "vpn", "other", "none":
	default:
		t.Fatalf("未知网络类型 %q", typ)
	}
	if !connected && typ != "none" {
		t.Fatalf("未连接时 type 应为 none, 实际 %q", typ)
	}
	if connected && typ == "none" {
		t.Fatalf("已连接时 type 不应为 none")
	}
}

// TestSettingURICoversKernelWhitelist 逐个核对映射表是否覆盖内核白名单。
//
// 这是本文件最值钱的一条: 内核放行而宿主没映射的 kind 会静默退化成"这个平台
// 没实现" (openSystemSettings 返回 false, 应用只知道失败了, 查不出原因)。
// "privacy" 就这样漏过一次 —— 内核 settingKindList 有 11 项, 而这里的表只有 10
// 项, 长期没人发现, 因为当时的测试只覆盖了失败路径。补上 privacy 时加的这条断言。
//
// 全部是纯函数断言: 不碰 ShellExecuteW (真会弹出系统设置页, 单测里不能走)。
func TestSettingURICoversKernelWhitelist(t *testing.T) {
	kinds := gfx.SettingKinds()
	if len(kinds) == 0 {
		t.Fatalf("内核白名单为空 (gfx.SettingKinds 的导出断了?)")
	}
	for _, kind := range kinds {
		uri, ok := settingURI(kind)
		if !ok {
			t.Errorf("内核放行的 kind %q 在 win32 映射表里没有对应页面 "+
				"(症状: openSystemSettings(%q) 返回 false 且说不出原因)", kind, kind)
			continue
		}
		if !strings.HasPrefix(uri, "ms-settings:") {
			t.Errorf("kind %q 的 URI %q 不是 ms-settings: 协议", kind, uri)
		}
	}
	// 反向: 映射表不该有内核不放行的 kind。多出来的是死代码 —— 内核会先拦,
	// 永远到不了宿主 (与"宿主不许多报能力"是同一条纪律: 声明了却做不到的
	// 东西会让上层判断出错)。
	allowed := make(map[string]bool, len(kinds))
	for _, k := range kinds {
		allowed[k] = true
	}
	for kind := range settingsURIs {
		if !allowed[kind] {
			t.Errorf("映射表里的 kind %q 不在内核白名单里 (内核会先拦, 这条永远走不到)", kind)
		}
	}
}

// TestHostOpenSettingsUnknownKind 验证宿主对"没实现"的 kind 的兜底口径:
// 报 unsupported, 而不是 invalid-arg。
//
// 判据: 调用链上内核会用 SettingKinds() 先拦一遍, 所以能走到宿主的 kind 一定是
// 内核认可的 —— 此时再报"参数不合法"就把排查方向带偏到调用方身上了。真实含义
// 是"这个平台没实现这个页"。
//
// **不测成功路径**: hostOpenSettings 会真的调 ShellExecuteW 弹出系统设置页。
// 映射表的正确性由 TestSettingURICoversKernelWhitelist 覆盖。
func TestHostOpenSettingsUnknownKind(t *testing.T) {
	h := &win32Host{}
	bad := object.NewObject()
	bad.SetProperty("kind", object.NewString("nonexistent-page"))
	res := h.Call("device.openSettings", bad)
	if res.Err == nil || res.Err.Code != gfx.ErrUnsupported {
		t.Fatalf("宿主未实现的设置页应返回 unsupported, 实际 %+v", res.Err)
	}
}
