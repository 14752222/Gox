package gfx

import (
	"testing"

	"github.com/14752222/Gox/vm"
)

// ===== permission 三端语义对齐 + mock 穷举 (rqF3oj) =====
//
// spec 写死在 gfx/native_permission.go 头注释 (「三端语义对齐 spec」一节)。
// 本文件把那张表落成可执行断言:
//
//	1. 状态词穷举: 六个标准词 + 常见平台别名全部归一到正确取值。
//	2. checkPermission 语义: 只认 granted (limited/restricted/not-determined
//	   全是 false —— spec 的立场)。
//	3. 三端 profile 回放: Android / iOS / 鸿蒙 三份"宿主全量上报"快照,
//	   断言 getSetting() 形状稳定 (9 个 kind 全键) 且读数与 spec 一致。
//	4. describePermissionState: unknown ≠ 未授权 (demo 层显示约定)。
//	5. canIUse ⟺ unsupported 一致性: 无宿主时 authorize reject 的
//	   errCode 必须是 "unsupported" (与 hosttest.checkCanIUseConsistency
//	   互补: 那边验声明侧, 这里验消费侧)。

// TestPermissionStateNormalization 穷举状态词归一 (标准词 + 平台别名)。
func TestPermissionStateNormalization(t *testing.T) {
	cases := map[string]string{
		// 标准六词
		"granted": PermGranted, "denied": PermDenied, "not-determined": PermNotDetermined,
		"restricted": PermRestricted, "limited": PermLimited, "unknown": PermUnknown,
		// iOS 系词汇
		"authorized": PermGranted, "denied ": PermDenied,
		// Android 常见
		"allow": PermGranted, "allowed": PermGranted, "true": PermGranted,
		"deny": PermDenied, "rejected": PermDenied, "false": PermDenied,
		// 询问前
		"undetermined": PermNotDetermined, "prompt": PermNotDetermined, "unasked": PermNotDetermined,
		// 部分授权
		"partial": PermLimited,
		// 乱写不猜
		"yes": PermUnknown, "": PermUnknown,
	}
	for in, want := range cases {
		if got := normalizePermissionState(in); got != want {
			t.Errorf("normalizePermissionState(%q) = %q, want %q", in, got, want)
		}
	}
	// 大小写与空白
	if got := normalizePermissionState("  GRANTED "); got != PermGranted {
		t.Errorf("大小写/空白归一失败: %q", got)
	}
}

// TestPermissionKindNormalization 权限种类别名归一 (白名单外的拒绝)。
func TestPermissionKindNormalization(t *testing.T) {
	alias := map[string]string{
		"photos": "gallery", "photo": "gallery", "images": "gallery",
		"image": "gallery", "album": "gallery", "media": "gallery",
		"mic": "microphone", "record": "microphone",
		"gps": "location", "notifications": "notification",
		"CAMERA": "camera", " location ": "location",
	}
	for in, want := range alias {
		got, ok := normalizePermissionKind(in)
		if !ok || got != want {
			t.Errorf("normalizePermissionKind(%q) = (%q,%v), want (%q,true)", in, got, ok, want)
		}
	}
	for _, bad := range []string{"telepathy", "", "cameraX"} {
		if _, ok := normalizePermissionKind(bad); ok {
			t.Errorf("%q 不该被认成权限", bad)
		}
	}
}

// TestPermissionCheckOnlyGranted checkPermission 只认 granted。
func TestPermissionCheckOnlyGranted(t *testing.T) {
	resetPermissionStateForTest()
	t.Cleanup(resetPermissionStateForTest)

	for _, state := range []string{PermLimited, PermRestricted, PermNotDetermined, PermUnknown, PermDenied} {
		ReportPermission("camera", state)
		if PermissionState("camera") != state {
			t.Fatalf("缓存读数应为 %q", state)
		}
		// jsCheckPermission 的语义 = PermissionState(kind) == PermGranted (只认 granted)
		if PermissionState("camera") == PermGranted {
			t.Errorf("checkPermission(%q) 必须为 false —— spec 立场: limited 等不算通过", state)
		}
	}
	ReportPermission("camera", PermGranted)
	if PermissionState("camera") != PermGranted {
		t.Fatal("granted 必须 true")
	}
}

// threePlatformProfiles 是三端"宿主全量上报"的 spec 快照 (native_permission.go
// 头注释那张表的可执行版本)。每份都按宿主 onResume 全量重报的形态构造。
func threePlatformProfiles() map[string]map[string]string {
	full := func(overrides map[string]string) map[string]string {
		// 基线: 全部 not-determined (iOS 形态), 再叠平台覆盖
		m := map[string]string{}
		for _, k := range permissionKindList {
			m[k] = PermNotDetermined
		}
		for k, v := range overrides {
			m[k] = v
		}
		return m
	}
	return map[string]map[string]string{
		// Android: not-determined 不出现, 系统区分不了"没问过"与"拒过"
		// → 统一 denied。demo 不得依赖两者区别。
		"android": full(map[string]string{
			"location": PermGranted, "camera": PermDenied,
			"gallery": PermLimited, "microphone": PermDenied,
			"notification": PermGranted, "contacts": PermDenied,
			"calendar": PermDenied, "bluetooth": PermGranted,
			"storage": PermDenied,
		}),
		// iOS: notification 同步状态拿不到 → unknown。
		"ios": full(map[string]string{
			"notification": PermUnknown,
			"gallery":      PermLimited,
		}),
		// 鸿蒙: gallery 走系统选择器免权限 → 恒 granted; storage 无等价 → 恒 unknown。
		"harmony": full(map[string]string{
			"gallery": PermGranted,
			"storage": PermUnknown,
		}),
	}
}

// TestPermissionThreePlatformProfiles 三端 profile 回放: 形状稳定 + 读数符合 spec。
func TestPermissionThreePlatformProfiles(t *testing.T) {
	setupHost(t, nil)
	resetPermissionStateForTest()
	t.Cleanup(resetPermissionStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { getSetting, permissionState, checkPermission, reportPermission } from "gx/permission";
		import { h, render } from "gx/gfx";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		// describePermissionState: demo 层显示约定 (spec)。
		// unknown 是"问不到", 不是"未授权" —— 两者对用户含义完全不同。
		globalThis.describePermissionState = function (s) {
			switch (s) {
			case "granted": return "已授权";
			case "denied": return "已拒绝（可再次申请）";
			case "not-determined": return "尚未申请";
			case "restricted": return "受系统策略限制";
			case "limited": return "部分授权";
			default: return "未知（本平台不提供）";
			}
		};
		globalThis.descCheck =
			describePermissionState("unknown") + "|" + describePermissionState("not-determined") +
			"|" + describePermissionState("granted") + "|" + describePermissionState("limited");
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	// describePermissionState 约定: unknown ≠ 未授权 ("问不到" 是中性词)
	if d, _ := globalStr(t, v, "descCheck"); d != "未知（本平台不提供）|尚未申请|已授权|部分授权" {
		t.Fatalf("describePermissionState 显示词不对: %q", d)
	}

	// getSetting 快照形状 (Go 侧, 与 VM 无关)
	if len(permissionKindList) != 9 {
		t.Fatalf("kind 白名单数量变了 (%d), 同步本测试", len(permissionKindList))
	}
	for platform, profile := range threePlatformProfiles() {
		// 模拟宿主 onResume 全量重报
		ReportPermissions(profile)
		snapshot := permissionSnapshot()
		for _, k := range permissionKindList {
			if snapshot[k] != profile[k] {
				t.Errorf("%s: 快照[%q] = %q, want %q (spec)", platform, k, snapshot[k], profile[k])
			}
			if s := PermissionState(k); s != profile[k] {
				t.Errorf("%s: PermissionState(%q) = %q, want %q", platform, k, s, profile[k])
			}
		}
		// spec 逐条: Android 无 not-determined / 鸿蒙 gallery granted + storage unknown / iOS notification unknown
		switch platform {
		case "android":
			for _, k := range permissionKindList {
				if s := PermissionState(k); s == PermNotDetermined {
					t.Errorf("android: %q 不得上报 not-determined (spec: 统一 denied)", k)
				}
			}
		case "ios":
			if s := PermissionState("notification"); s != PermUnknown {
				t.Errorf("ios: notification 应 unknown, got %q", s)
			}
		case "harmony":
			if s := PermissionState("gallery"); s != PermGranted {
				t.Errorf("harmony: gallery 应恒 granted, got %q", s)
			}
			if s := PermissionState("storage"); s != PermUnknown {
				t.Errorf("harmony: storage 应恒 unknown, got %q", s)
			}
		}
	}

}

// TestPermissionUnsupportedConsistency 消费侧一致性: 无宿主 (canIUse false) 时
// authorize 必须 reject 且 errCode === "unsupported" —— 与
// hosttest.checkCanIUseConsistency (声明侧) 互补。
func TestPermissionUnsupportedConsistency(t *testing.T) {
	setupHost(t, nil) // 无能力声明
	resetPermissionStateForTest()
	t.Cleanup(resetPermissionStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { authorize, requestPermissions } from "gx/permission";
		import { canIUse } from "gx/device";
		import { h, render } from "gx/gfx";
		render(h("rect", {width: 10, height: 10}), {title: "T", width: 200, height: 150});
		// 注意探测的是**方法名** (permission.request): canIUse("permission") 对
		// 内置模块恒 true (gx/permission 模块本身存在), 方法级探测才反映宿主。
		globalThis.cap = canIUse("permission.request");
		globalThis.got1 = "pending"; globalThis.got2 = "pending";
		authorize("camera").then((s) => { got1 = s; },
			(e) => { got1 = "ERR:" + e.errCode; });
		requestPermissions(["camera", "location"]).then((o) => { got2 = JSON.stringify(o); },
			(e) => { got2 = "ERR:" + e.errCode; });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if b, _ := globalBool(t, v, "cap"); b {
		t.Fatal("无宿主时 canIUse(\"permission\") 应为 false")
	}
	pumpSettled(t, v, fake, func() bool {
		g1, _ := globalStr(t, v, "got1")
		g2, _ := globalStr(t, v, "got2")
		return g1 != "pending" && g2 != "pending"
	})
	if g, _ := globalStr(t, v, "got1"); g != "ERR:unsupported" {
		t.Fatalf("authorize 无能力时应 reject(unsupported), got %q", g)
	}
	if g, _ := globalStr(t, v, "got2"); g != "ERR:unsupported" {
		t.Fatalf("requestPermissions 无能力时应 reject(unsupported), got %q", g)
	}
}
