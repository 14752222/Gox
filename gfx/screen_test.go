package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== gx/screen 的纯逻辑用例 (不需要 VM) =====
//
// 挑出来的都是"写错了不会崩、只会静默给错值"的那几条: 折痕方向/比例钳制、
// 姿态词表归一化、上报的 upsert 语义、无后端时的平台名与虚拟屏。

func TestDisplaySplitRatio(t *testing.T) {
	fold := Display{ID: "d", Foldable: true, W: 1600, H: 1000}
	cases := []struct {
		name  string
		hinge *DisplayHinge
		want  float64
	}{
		{"无折痕 → 等分", nil, 0.5},
		{"左右折 700px 处", &DisplayHinge{Orientation: "vertical", X: 700, W: 24}, 0.4375},
		{"上下折 300px 处", &DisplayHinge{Orientation: "horizontal", Y: 300, H: 20}, 0.3},
		{"比例过小被钳到 0.2", &DisplayHinge{Orientation: "vertical", X: 10, W: 4}, 0.2},
		{"比例过大被钳到 0.8", &DisplayHinge{Orientation: "vertical", X: 1500, W: 4}, 0.8},
	}
	for _, c := range cases {
		d := fold
		d.Hinge = c.hinge
		if got := displaySplitRatio(d); got != c.want {
			t.Fatalf("%s: splitRatio = %v, 期望 %v", c.name, got, c.want)
		}
	}
	// 不是折叠屏 (即便带了 hinge) 也不分栏
	d := Display{ID: "d", Foldable: false, Hinge: &DisplayHinge{Orientation: "vertical", X: 100, W: 10}}
	if got := displaySplitRatio(d); got != 0.5 {
		t.Fatalf("非折叠屏应等分, 实际 %v", got)
	}
}

func TestDisplayHingeOrientation(t *testing.T) {
	if got := displayHingeOrientation(Display{}); got != "vertical" {
		t.Fatalf("无折痕时缺省应为 vertical (最常见形态), 实际 %q", got)
	}
	if got := displayHingeOrientation(Display{Hinge: &DisplayHinge{Orientation: "horizontal"}}); got != "horizontal" {
		t.Fatalf("横向折痕读错: %q", got)
	}
	if got := displayHingeOrientation(Display{Hinge: &DisplayHinge{Orientation: "weird"}}); got != "vertical" {
		t.Fatalf("未知方向应退化为 vertical: %q", got)
	}
}

func TestNormalizedPosture(t *testing.T) {
	cases := map[string]string{
		"":            postureFlat,
		"flat":        postureFlat,
		"half-open":   postureHalfOpen,
		"half_open":   postureHalfOpen,
		"HalfOpen":    postureHalfOpen,
		"  FOLDED  ":  postureFolded,
		"half-closed": postureUnknown, // 不认识的词 → unknown, 而不是猜
	}
	for in, want := range cases {
		if got := normalizedPosture(in); got != want {
			t.Fatalf("normalizedPosture(%q) = %q, 期望 %q", in, got, want)
		}
	}
}

// TestReportPostureUpsert 验证上报的 upsert 语义与"第一次上报定义整张表"。
func TestReportPostureUpsert(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	SetDefaultFactory(nil)

	// 无后端、无上报: 一块虚拟屏, 且是主屏
	list := allDisplays()
	if len(list) != 1 || !list[0].Primary {
		t.Fatalf("无任何信息时应有一块虚拟主屏: %+v", list)
	}

	// 第一次上报: 定义整张表 (虚拟屏不该再占着主屏位置)
	reportPostureGo(routeObj("display", "fold-1", "foldable", true,
		"width", object.NewNumber(1600), "height", object.NewNumber(1000),
		"posture", "half-open",
		"hinge", routeObj("x", object.NewNumber(700), "y", object.NewNumber(0),
			"w", object.NewNumber(24), "h", object.NewNumber(1000), "orientation", "vertical")))
	list = allDisplays()
	if len(list) != 1 {
		t.Fatalf("第一次上报应定义整张表, 实际 %d 块: %+v", len(list), list)
	}
	d := list[0]
	if d.ID != "fold-1" || !d.Primary || !d.Foldable || d.Posture != postureHalfOpen {
		t.Fatalf("上报表不对: %+v", d)
	}
	if d.Hinge == nil || d.Hinge.X != 700 || d.Hinge.Orientation != "vertical" {
		t.Fatalf("折痕没记上: %+v", d.Hinge)
	}
	if got := displaySplitRatio(d); got != 0.4375 {
		t.Fatalf("上报后的分割比例 = %v", got)
	}

	// 第二次上报: 只改姿态, 几何与折痕保留 (upsert 而不是覆盖成新条目)
	reportPostureGo(routeObj("display", "fold-1", "posture", "flat"))
	list = allDisplays()
	if len(list) != 1 || list[0].Posture != postureFlat {
		t.Fatalf("第二次上报应改同一块屏: %+v", list)
	}
	if list[0].W != 1600 || list[0].Hinge == nil {
		t.Fatalf("改姿态不该丢几何/折痕: %+v", list[0])
	}
	// 再折回去 (宿主只报姿态, 不重报折痕): 比例必须还是记得的那个
	reportPostureGo(routeObj("display", "fold-1", "posture", "half-open"))
	if got := displaySplitRatio(allDisplays()[0]); got != 0.4375 {
		t.Fatalf("折回去后分割比例应沿用记忆的折痕, 实际 %v", got)
	}

	// resetDisplays 交还给后端 (无后端 → 回到虚拟屏)
	resetDisplaysGo()
	list = allDisplays()
	if len(list) != 1 || list[0].ID != "virtual-0" {
		t.Fatalf("reset 后应回到虚拟屏: %+v", list)
	}
}

// TestPlatformNameFallback 验证平台名在没有后端时不会瞎报。
func TestPlatformNameFallback(t *testing.T) {
	SetDefaultFactory(nil)
	t.Cleanup(func() { SetDefaultFactory(nil) })
	if got := platformName(); got != "headless" {
		t.Fatalf("无后端时平台名应为 headless, 实际 %q", got)
	}
	// 注册一个本包的假工厂 → 平台名取包名末段 (真后端注册时是 "win32"/"x11")
	f := newFakeSurface()
	SetDefaultFactory(&fakeFactory{f})
	if got := platformName(); got != "gfx" {
		t.Fatalf("假工厂属于本包, 平台名应为 gfx, 实际 %q", got)
	}
}

// TestWindowHandleIntrospection 验证窗口句柄的内省口 (scope/id/__goxWindow):
// gx/router 的 sync([wa, wb]) 与 gx/screen 的 screenOf(win) 都靠它还原 Go 指针。
func TestWindowHandleIntrospection(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	win, err := Mount(root, WindowConfig{Title: "t", Width: 100, Height: 80})
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if win.ID() <= 0 {
		t.Fatalf("窗口号应已分配, 实际 %d", win.ID())
	}
	if got := appScope(win.App()); got == "" || got == "win:0" {
		t.Fatalf("窗口作用域名不合理: %q", got)
	}
	// 三个内省口必须都在 (调用它们需要 VM — object.CallFunction 在无 VM 时是
	// 空操作, 所以这里只钉"属性存在", 端到端行为由 router_window_test.go 覆盖:
	// 那边真的用窗口句柄做了 push/sync)。
	obj, ok := win.jsObject().(*object.Object)
	if !ok {
		t.Fatalf("句柄不是对象")
	}
	for _, name := range []string{"id", "scope", "__goxWindow"} {
		if v, ok := obj.GetProperty(name); !ok || !object.IsCallable(v) {
			t.Fatalf("句柄缺少内省口 %q", name)
		}
	}
	if windowHandleOf(object.NewString("nope")) != nil {
		t.Fatalf("字符串不该被当成窗口句柄")
	}
}

// TestReportPostureAcceptsJSGeometryKeyNames 钉住"同一个量的两种拼法都认"。
//
// hinge() / regions() 的输出用 width/height (与 displayToJS 的显示器几何一致),
// 而 reportPosture 的入参最初只读 w/h —— 于是最自然的回填
//
//	reportPosture({ hinge: hinge() });
//
// 会把折痕宽度读成 0, 但它只影响双栏分割比例, 不报任何错 (静默失效)。
// 这里同时钉住: 长名能读进来; 短名与长名同给时短名优先。
func TestReportPostureAcceptsJSGeometryKeyNames(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	SetDefaultFactory(nil)

	// 这一坨就是 hinge() / regions() 的字面输出形状 (键名一个不改)。
	reportPostureGo(routeObj("display", "fold-1", "foldable", true,
		"width", object.NewNumber(1600), "height", object.NewNumber(1000),
		"posture", "half-open",
		"hinge", routeObj("x", object.NewNumber(700), "y", object.NewNumber(0),
			"width", object.NewNumber(24), "height", object.NewNumber(1000),
			"orientation", "vertical"),
		"regions", object.NewArray([]object.Value{
			routeObj("id", "A", "x", object.NewNumber(0), "y", object.NewNumber(0),
				"width", object.NewNumber(700), "height", object.NewNumber(1000)),
			routeObj("id", "B", "x", object.NewNumber(724), "y", object.NewNumber(0),
				"width", object.NewNumber(876), "height", object.NewNumber(1000)),
		})))

	d := allDisplays()[0]
	if d.Hinge == nil || d.Hinge.W != 24 || d.Hinge.H != 1000 || d.Hinge.X != 700 {
		t.Fatalf("width/height 形式的折痕没读进来: %+v", d.Hinge)
	}
	if len(d.Regions) != 2 || d.Regions[0].W != 700 || d.Regions[1].H != 1000 {
		t.Fatalf("width/height 形式的区域没读进来: %+v", d.Regions)
	}

	// 显式短名优先: 两种拼法都不缺时以 w/h 为准 (避免"多写了一个键就换语义")
	reportPostureGo(routeObj("display", "fold-1", "hinge",
		routeObj("x", object.NewNumber(700), "w", object.NewNumber(30), "width", object.NewNumber(24),
			"h", object.NewNumber(1000), "height", object.NewNumber(900))))
	if got := allDisplays()[0].Hinge.W; got != 30 {
		t.Fatalf("两种拼法同给时短名 w 应优先, 实际 W=%d", got)
	}
}
