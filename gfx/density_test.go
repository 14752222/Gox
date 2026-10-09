package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 密度换算: 显式尺寸属性按 Display.Scale 放大 (rpr9zf §1) =====
//
// 缺口现场: Android 高密度屏上 `font={20}` 曾经就是 20 个物理像素 —— 3x/2x
// 屏上界面小到没法用。兜底字号早就按 Scale 放大了, 但**显式写出来的**尺寸
// 没有。这里把这条换算钉住。
//
// 三条硬口径:
//  1. Scale=1 (桌面 x11/win32): 换算恒等, 渲染结果与改动前逐像素一致;
//  2. Scale=2: 脚本写的每个尺寸数字翻倍;
//  3. 跨屏: 同一个窗口从 1x 屏挪到 2x 屏, 下一帧的布局就按新系数算 (系数是
//     每帧刷的, 不能是建树时一次性写进 props 的)。

// withScaledDisplay 注册一个"窗口落在 scale 屏上"的环境, 用完还原。
//
// 还原必须彻底 (清 app / 清工厂 / 清密度缓存): 密度是包级状态, 漏一项就会让
// 后面的用例拿到上个用例的 scale —— 那种失败离现场十万八千里。
func withScaledDisplay(t *testing.T, scale float64) *scaledFactory {
	t.Helper()
	fake := newFakeSurface()
	f := &scaledFactory{s: fake, scale: scale}
	SetDefaultFactory(f)
	a := &app{surface: fake, dirtyNodes: map[*GuiNode]struct{}{}}
	registerApp(a)
	t.Cleanup(func() {
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
		SetDefaultFactory(nil)
		ResetDensityForTest()
	})
	return f
}

// TestDensityScaleOneIsIdentity: Scale=1 时换算必须恒等。
//
// 这是本次改动的硬约束 —— 桌面 1x 上任何一条渲染路径都不许漂移 (既有
// golden / 像素断言都建立在这个前提上)。恒等不只是"×1", 还包括取整方式
// 不变: 既有代码是 int() 截断, 所以 dpToPx 在 Scale<=1 时也走截断。
func TestDensityScaleOneIsIdentity(t *testing.T) {
	withScaledDisplay(t, 1)

	if got := displayScale(); got != 1 {
		t.Fatalf("1x 屏系数应为 1, got %v", got)
	}
	if got := mkNode("text", map[string]float64{"font": 20}).FontSize(); got != 20 {
		t.Fatalf("1x 屏 font=20 应为 20, got %d", got)
	}
	if got := defaultFontSize(); got != 16 {
		t.Fatalf("1x 屏兜底字号应为 16, got %d", got)
	}
	// 截断语义: 10.7 在非 1x 时会被四舍五入, 1x 时必须仍是 10。
	if got := dpToPx(10.7); got != 10 {
		t.Fatalf("1x 屏 dpToPx(10.7) 应保持截断语义 = 10, got %d", got)
	}

	// 布局层面: 显式 width/height/padding/gap/margin 全部原样。
	root := mkNode("column", map[string]float64{"width": 200, "height": 100, "padding": 12, "gap": 8})
	child := mkNode("view", map[string]float64{"width": 40, "height": 20, "margin": 5})
	root.Children = []*GuiNode{child}
	child.Parent = root
	Layout(root, 400, 300)
	if child.Box.W != 40 || child.Box.H != 20 {
		t.Fatalf("1x 屏子节点尺寸应原样 (40×20), got %d×%d", child.Box.W, child.Box.H)
	}
	// 内容区左上角 = padding (12) + margin (5)。
	if child.Box.X != 17 || child.Box.Y != 17 {
		t.Fatalf("1x 屏内容区原点应为 (17,17), got (%d,%d)", child.Box.X, child.Box.Y)
	}
}

// TestDensityScaleTwoDoublesExplicitSizes: Scale=2 时脚本写的尺寸翻倍。
func TestDensityScaleTwoDoublesExplicitSizes(t *testing.T) {
	withScaledDisplay(t, 2)

	if got := displayScale(); got != 2 {
		t.Fatalf("2x 屏系数应为 2, got %v", got)
	}
	if got := mkNode("text", map[string]float64{"font": 20}).FontSize(); got != 40 {
		t.Fatalf("2x 屏 font=20 应为 40, got %d", got)
	}
	if got := defaultFontSize(); got != 32 {
		t.Fatalf("2x 屏兜底字号应为 32, got %d", got)
	}

	root := mkNode("column", map[string]float64{"width": 200, "height": 100, "padding": 12, "gap": 8})
	child := mkNode("view", map[string]float64{"width": 40, "height": 20, "margin": 5})
	root.Children = []*GuiNode{child}
	child.Parent = root
	Layout(root, 400, 300)
	if child.Box.W != 80 || child.Box.H != 40 {
		t.Fatalf("2x 屏子节点尺寸应翻倍 (80×40), got %d×%d", child.Box.W, child.Box.H)
	}
	// (padding 12 + margin 5) × 2 = 34
	if child.Box.X != 34 || child.Box.Y != 34 {
		t.Fatalf("2x 屏内容区原点应为 (34,34), got (%d,%d)", child.Box.X, child.Box.Y)
	}

	// 四边覆盖值也各自换算 (`padding={12} paddingTop={40}` → 80 / 24)。
	t2, r2, b2, l2 := paddingOf(mkNode("view", map[string]float64{
		"padding": 12, "paddingTop": 40,
	}))
	if t2 != 80 || r2 != 24 || b2 != 24 || l2 != 24 {
		t.Fatalf("2x 屏 padding 覆盖应为 (80,24,24,24), got (%d,%d,%d,%d)", t2, r2, b2, l2)
	}

	// min/max 钳位同样按逻辑值解释: minWidth={30} 在 2x 上是 60。
	clamped := mkNode("view", map[string]float64{"width": 10, "minWidth": 30})
	if got := clampDim(clamped, 20, "minWidth", "maxWidth"); got != 60 {
		t.Fatalf("2x 屏 minWidth=30 应为 60, got %d", got)
	}
}

// TestDensityScaleFollowsWindowAcrossScreens: 跨屏 (1x → 2x) 下一帧就生效。
//
// 为什么要专门测这条: 系数如果是一次性写进 props 的 (建树时乘一次), 或者
// 缓存按"窗口"而不是"每帧"作废, 拖屏就不会重新换算 —— 症状是"重启应用就
// 对了, 拖过去就不对", 而且只在真机上出现。
func TestDensityScaleFollowsWindowAcrossScreens(t *testing.T) {
	f := withScaledDisplay(t, 1)

	root := mkNode("column", map[string]float64{"padding": 10})
	child := mkNode("view", map[string]float64{"width": 50, "height": 25})
	root.Children = []*GuiNode{child}
	child.Parent = root

	// 1x 屏上: 原样。
	Layout(root, 400, 300)
	if child.Box.W != 50 || child.Box.X != 10 {
		t.Fatalf("1x 屏上应为 50 宽 / x=10, got %d / %d", child.Box.W, child.Box.X)
	}

	// 窗口被拖到 2x 屏 (后端报告的还是同一块屏的 ID, 但 Scale 变了)。
	f.scale = 2
	Layout(root, 400, 300)
	if child.Box.W != 100 || child.Box.X != 20 {
		t.Fatalf("拖到 2x 屏后应为 100 宽 / x=20, got %d / %d", child.Box.W, child.Box.X)
	}

	// 拖回去也要跟着回落 (不是"只能变大一次")。
	f.scale = 1
	Layout(root, 400, 300)
	if child.Box.W != 50 || child.Box.X != 10 {
		t.Fatalf("拖回 1x 屏后应回到 50 宽 / x=10, got %d / %d", child.Box.W, child.Box.X)
	}
}

// TestDensityNoWindowFallsBackToOne: 没有活动窗口时系数为 1。
//
// 纯节点单测与无头宿主 (headless) 走这条路径 —— font_inherit_test 的
// "默认 16"口径就靠它, 不能被 hi-dpi 的用例串味污染。
func TestDensityNoWindowFallsBackToOne(t *testing.T) {
	ResetDensityForTest()
	if got := displayScale(); got != 1 {
		t.Fatalf("无窗口时系数应为 1, got %v", got)
	}
	if got := defaultFontSize(); got != 16 {
		t.Fatalf("无窗口时兜底字号应为 16, got %d", got)
	}
}

// TestDensitySafeAreaStyleIsDp: 安全区样式不能被换算两次。
//
// 这是"换算集中在取值口"的代价里最容易漏的一条: 宿主上报的 insets / 键盘高
// 是**设备像素** (Android WindowInsetsCompat 口径, 模拟器实测键盘 883px),
// 而 safeAreaStyle() 的产出是"铺到 props 上的 padding"。padding 现在按 dp
// 解释, 所以 safeAreaStyle 必须先除回 dp —— 否则 2x 屏上状态栏让位会翻倍。
func TestDensitySafeAreaStyleIsDp(t *testing.T) {
	withScaledDisplay(t, 2)

	o := safeAreaStyleToJS(nil, Viewport{
		Insets:   Insets{Top: 60, Right: 0, Bottom: 40, Left: 0},
		Keyboard: 883,
	}, true).(*object.Object)

	num := func(name string) float64 {
		v, ok := o.GetProperty(name)
		if !ok {
			t.Fatalf("safeAreaStyle 应给出 %s", name)
		}
		n, ok := v.(*object.Number)
		if !ok {
			t.Fatalf("%s 应是数值, got %T", name, v)
		}
		return n.Value
	}
	if got := num("paddingTop"); got != 30 {
		t.Fatalf("2x 屏上 60px 状态栏应报 30dp, got %v", got)
	}
	// 键盘比底栏高, 于是下边距取键盘: 883px → 441.5dp。
	if got := num("paddingBottom"); got != 441.5 {
		t.Fatalf("2x 屏上 883px 键盘应报 441.5dp, got %v", got)
	}

	// 端到端: 把这组样式原样填回 props, 布局出来的设备像素正好等于宿主上报值。
	node := mkNode("column", nil)
	for _, k := range []string{"paddingTop", "paddingRight", "paddingBottom", "paddingLeft"} {
		if v, ok := o.GetProperty(k); ok {
			node.Props[k] = v
		}
	}
	top, _, bottom, _ := paddingOf(node)
	if top != 60 || bottom != 883 {
		t.Fatalf("铺回 props 后应回到宿主上报的 60 / 883, got %d / %d", top, bottom)
	}
}

// TestDensityPropsNotScaled: 不该被换算的量保持原样。
//
// 换算的边界必须两边都钉: 只钉"该算的算了"会漏掉"不该算的也被算了"。
// 百分比是相对量 (再乘一次 Scale 就重复换算了), flex 系数是无量纲的。
func TestDensityPropsNotScaled(t *testing.T) {
	withScaledDisplay(t, 2)

	pct := mkNode("view", nil)
	withStr(pct, "width", "50%")
	if w, _ := pct.intrinsicSize(); w != 0 {
		t.Fatalf("百分比 width 在 intrinsicSize 里应贡献 0 (相对量), got %d", w)
	}
	if p, ok := pct.percentProp("width"); !ok || p != 0.5 {
		t.Fatalf("百分比 width 应保持 0.5, got %v/%v", p, ok)
	}

	grow := mkNode("view", map[string]float64{"flexGrow": 2})
	if v, _ := grow.PropNum("flexGrow"); v != 2 {
		t.Fatalf("flexGrow 是无量纲的, 不该被换算, got %v", v)
	}
}
