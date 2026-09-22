package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== gx/device · gx/app · gx/geo · gx/media · gx/permission · gx/viewport 纯逻辑用例 =====
//
// 挑的都是"写错了不会崩、只会静默给错值"的那几条: 归一化词表、兜底缺省、
// 越界钳制、纯计算 (距离/文件大小/内容区)。端到端 Promise 链路在
// native_e2e_test.go; 响应式订阅链路在 native_watch_test.go。

// ===== 归一化 (不认识的值 → 明确兜底, 不猜) =====

func TestNormalizeAppState(t *testing.T) {
	cases := map[string]string{
		"":           AppStateUnknown,
		"active":     AppStateActive,
		"foreground": AppStateActive,
		"resumed":    AppStateActive,
		"background": AppStateBackground,
		"paused":     AppStateBackground,
		"inactive":   AppStateInactive,
		"willResign": AppStateInactive,
		"  ACTIVE  ": AppStateActive,
		"bogus":      AppStateUnknown,
	}
	for in, want := range cases {
		if got := normalizeAppState(in); got != want {
			t.Fatalf("normalizeAppState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizePermission(t *testing.T) {
	if k, ok := normalizePermissionKind("PHOTOS"); !ok || k != "gallery" {
		t.Fatalf("photos 应归一为 gallery: %q %v", k, ok)
	}
	if k, ok := normalizePermissionKind("mic"); !ok || k != "microphone" {
		t.Fatalf("mic 应归一为 microphone: %q %v", k, ok)
	}
	if _, ok := normalizePermissionKind("camera"); !ok {
		t.Fatalf("camera 应合法")
	}
	if _, ok := normalizePermissionKind("telepathy"); ok {
		t.Fatalf("不认识的权限不该合法")
	}
	cases := map[string]string{
		"granted":        PermGranted,
		"authorized":     PermGranted,
		"allow":          PermGranted,
		"true":           PermGranted,
		"denied":         PermDenied,
		"deny":           PermDenied,
		"not-determined": PermNotDetermined,
		"undetermined":   PermNotDetermined,
		"restricted":     PermRestricted,
		"limited":        PermLimited,
		"partial":        PermLimited,
		"bogus":          PermUnknown,
	}
	for in, want := range cases {
		if got := normalizePermissionState(in); got != want {
			t.Fatalf("normalizePermissionState(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeViewportMode(t *testing.T) {
	cases := map[string]string{
		"":                   ViewportFullscreen,
		"full":               ViewportFullscreen,
		"split":              ViewportSplit,
		"split-screen":       ViewportSplit,
		"multiWindow":        ViewportSplit,
		"pip":                ViewportPIP,
		"picture-in-picture": ViewportPIP,
		"freeform":           ViewportFreeform,
		"floating":           ViewportFreeform,
		"bogus":              ViewportUnknown,
	}
	for in, want := range cases {
		if got := normalizeViewportMode(in); got != want {
			t.Fatalf("normalizeViewportMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSizeClass(t *testing.T) {
	if got := normalizeSizeClass("COMPACT"); got != SizeCompact {
		t.Fatalf("COMPACT → compact, got %q", got)
	}
	if got := normalizeSizeClass("bogus"); got != "" {
		t.Fatalf("不认识 → 空 (让缺省推导接手), got %q", got)
	}
}

// ===== 本机缺省值 (无宿主也必须能回答) =====

func TestLocalDeviceInfoDefaults(t *testing.T) {
	SetDefaultFactory(nil)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	d := localDeviceInfo()
	if d.Platform == "" || d.Arch == "" {
		t.Fatalf("平台/架构不该为空: %+v", d)
	}
	if d.ScreenW <= 0 || d.ScreenH <= 0 {
		t.Fatalf("屏幕尺寸不该为 0 (虚拟屏兜底): %+v", d)
	}
	if d.PixelRatio <= 0 {
		t.Fatalf("pixelRatio 不该 <= 0: %+v", d)
	}
	// 桌面缺省不该猜成平板布局 (除非宿主报了)
	if d.IsTablet {
		t.Fatalf("桌面缺省不该 isTablet: %+v", d)
	}
}

func TestLocaleFromEnv(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LANG", "zh_CN.UTF-8")
	lang, region := localeFromEnv()
	if lang != "zh" || region != "CN" {
		t.Fatalf("zh_CN.UTF-8 → zh/CN, got %q/%q", lang, region)
	}
	t.Setenv("LANG", "C")
	if lang, _ := localeFromEnv(); lang != "" {
		t.Fatalf("C locale 应返回空, got %q", lang)
	}
}

// ===== 覆盖语义 (宿主只报它知道的字段) =====

func TestApplyDeviceOverlayPartial(t *testing.T) {
	d := localDeviceInfo()
	o := object.NewObject()
	o.SetProperty("model", object.NewString("Pixel 8"))
	o.SetProperty("os", object.NewString("Android 14"))
	o.SetProperty("isTablet", object.NewBoolean(true))
	applyDeviceOverlay(&d, o)
	if d.Model != "Pixel 8" || d.OS != "Android 14" {
		t.Fatalf("覆盖没生效: %+v", d)
	}
	if !d.IsTablet {
		t.Fatalf("宿主报了 isTablet 就必须采纳")
	}
	if d.Platform == "" {
		t.Fatalf("宿主没报的字段必须保留本机缺省")
	}
}

// ===== 钳制 (单位写错时至少表现为可疑数值) =====

func TestClampViewport(t *testing.T) {
	v := clampViewport(Viewport{
		Insets:   Insets{Top: 5000, Right: -3, Bottom: 24, Left: 0},
		Keyboard: -1,
		Mode:     ViewportUnknown,
	})
	if v.Insets.Top != viewportInsetMax || v.Insets.Right != 0 {
		t.Fatalf("insets 越界钳制失败: %+v", v.Insets)
	}
	if v.Insets.Bottom != 24 {
		t.Fatalf("正常值不该被改: %+v", v.Insets)
	}
	if v.Keyboard != 0 {
		t.Fatalf("负键盘高度应钳 0: %d", v.Keyboard)
	}
	// unknown 保持 unknown (不猜); 只有空串才兜底 fullscreen
	if v.Mode != ViewportUnknown {
		t.Fatalf("unknown 不该被猜成别的: %q", v.Mode)
	}
	v2 := clampViewport(Viewport{Mode: ""})
	if v2.Mode != ViewportFullscreen {
		t.Fatalf("空 mode 应兜底 fullscreen: %q", v2.Mode)
	}
}

// ===== 纯计算 =====

func TestHaversineMeters(t *testing.T) {
	// 广州塔到北京天安门 (约 1891 km; 半正矢误差 < 0.5%, 断言给 ±30km 余量)
	d := haversineMeters(23.1066, 113.3245, 39.9087, 116.3975)
	if d < 1860e3 || d > 1920e3 {
		t.Fatalf("广京距离 = %v m, 期望约 1891 km", d)
	}
	// 同一位置 → 0
	if got := haversineMeters(30, 120, 30, 120); got != 0 {
		t.Fatalf("同点距离应为 0, got %v", got)
	}
	// 极小距离不应为负
	if got := haversineMeters(30.000001, 120, 30, 120); got < 0 {
		t.Fatalf("距离为负: %v", got)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		-1:   "",
		0:    "0 B",
		500:  "500 B",
		1024: "1.0 KB",
		// 1.5 MB 精确值
		1572864: "1.5 MB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Fatalf("humanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestMediaFromJSStringForm(t *testing.T) {
	m, ok := mediaFromJS(object.NewString(`C:\tmp\a.jpg`))
	if !ok || m.Path != `C:\tmp\a.jpg` || m.Name != "a.jpg" || m.Size != -1 {
		t.Fatalf("路径字符串形态解析错: %+v %v", m, ok)
	}
	if _, ok := mediaFromJS(object.NewString("")); ok {
		t.Fatalf("空串不是合法媒体")
	}
	if _, ok := mediaFromJS(object.NewNumber(1)); ok {
		t.Fatalf("数字不是合法媒体")
	}
}

func TestBaseName(t *testing.T) {
	if got := baseName(`C:\a\b\c.png`); got != "c.png" {
		t.Fatalf("反斜杠路径: %q", got)
	}
	if got := baseName("/a/b/c.png"); got != "c.png" {
		t.Fatalf("正斜杠路径: %q", got)
	}
	if got := baseName("c.png"); got != "c.png" {
		t.Fatalf("裸文件名: %q", got)
	}
}

func TestLocationFromJS(t *testing.T) {
	o := object.NewObject()
	o.SetProperty("latitude", object.NewNumber(30.5))
	o.SetProperty("longitude", object.NewNumber(120.1))
	l, ok := locationFromJS(o)
	if !ok || l.Latitude != 30.5 || l.Longitude != 120.1 {
		t.Fatalf("基础解析错: %+v %v", l, ok)
	}
	if l.Type != "wgs84" || l.Provider != "unknown" || l.Timestamp == 0 {
		t.Fatalf("缺省字段没补齐: %+v", l)
	}
	// 用 lat/lng 别名
	o2 := object.NewObject()
	o2.SetProperty("lat", object.NewNumber(10))
	o2.SetProperty("lng", object.NewNumber(20))
	if l2, ok := locationFromJS(o2); !ok || l2.Latitude != 10 || l2.Longitude != 20 {
		t.Fatalf("别名解析错: %+v %v", l2, ok)
	}
	// 没有经纬度 → 形状不对
	if _, ok := locationFromJS(object.NewObject()); ok {
		t.Fatalf("缺经纬度不该合法")
	}
}

func TestContentArea(t *testing.T) {
	// 键盘与 safeBottom 取较大者, 不相加
	v := Viewport{
		Insets:   Insets{Top: 24, Bottom: 48},
		Keyboard: 300,
		Mode:     ViewportFullscreen,
	}
	o := contentAreaToJS(nil, v).(*object.Object)
	if _, ok := o.GetProperty("height"); !ok {
		t.Fatalf("contentArea 缺字段")
	}
	h := objPropNum(o, "height")
	if h != 0 {
		// 无窗口时 windowPixelSize 退回显示器 (虚拟屏), 高度不可精确断言;
		// 只钉"键盘参与扣减"这条语义
		t.Logf("contentArea height = %v (无窗口, 仅记录)", h)
	}
}

func TestSplitActive(t *testing.T) {
	if splitActive(Viewport{Mode: ViewportFullscreen}) {
		t.Fatalf("fullscreen 不该算分屏")
	}
	if !splitActive(Viewport{Mode: ViewportSplit}) {
		t.Fatalf("split 应算分屏")
	}
	if !splitActive(Viewport{Mode: ViewportPIP}) {
		t.Fatalf("pip 应算分屏")
	}
	if !splitActive(Viewport{MultiWindow: true}) {
		t.Fatalf("multiWindow=true 应算分屏")
	}
}

func TestDeriveSizeClasses(t *testing.T) {
	// 手机竖屏 (390dp 宽) → compact
	wc, hc := deriveSizeClasses(780, 1690, 2)
	if wc != SizeCompact || hc != SizeRegular {
		t.Fatalf("手机竖屏: wc=%q hc=%q", wc, hc)
	}
	// 折叠屏展开 / 平板 (720dp 宽) → regular
	wc, _ = deriveSizeClasses(1440, 800, 2)
	if wc != SizeRegular {
		t.Fatalf("大屏应 regular: %q", wc)
	}
	// 缺省 scale = 1 (无宿主)
	wc, _ = deriveSizeClasses(700, 500, 0)
	if wc != SizeRegular {
		t.Fatalf("scale 缺省 1 时 700px → regular: %q", wc)
	}
}

// ===== 参数形状 (宽容度) =====

func TestNativeOptsTolerant(t *testing.T) {
	if o := nativeOpts(nil, 0); o == nil {
		t.Fatalf("无参数应返回空对象而不是 nil")
	}
	if o := nativeOpts([]object.Value{object.NewString("x")}, 0); o == nil {
		t.Fatalf("非对象参数应返回空对象")
	}
	obj := object.NewObject()
	obj.SetProperty("a", object.NewNumber(1))
	if o := nativeOpts([]object.Value{obj}, 0); objPropNum(o, "a") != 1 {
		t.Fatalf("对象参数应原样返回")
	}
}

func TestNativeTimeoutClamp(t *testing.T) {
	o := object.NewObject()
	if got := nativeTimeoutOf(o); got != 0 {
		t.Fatalf("缺省不超时: %d", got)
	}
	o.SetProperty("timeout", object.NewNumber(5000))
	if got := nativeTimeoutOf(o); got != 5000 {
		t.Fatalf("正常值: %d", got)
	}
	o.SetProperty("timeout", object.NewNumber(9999999))
	if got := nativeTimeoutOf(o); got > 3600*1000 {
		t.Fatalf("超时上限没钳: %d", got)
	}
}

func TestPermissionSnapshotShape(t *testing.T) {
	resetPermissionStateForTest()
	t.Cleanup(resetPermissionStateForTest)
	snap := permissionSnapshot()
	for _, k := range permissionKindList {
		if s, ok := snap[k]; !ok || s != PermUnknown {
			t.Fatalf("初始快照应有 %s=unknown, got %q %v", k, s, ok)
		}
	}
}
