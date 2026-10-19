package gfx

// Prism S8 —— 响应式自适应与触控可达性的用例。
//
// 这一批断言盯的是**两件都可能导致"看起来没事、上线才炸"的事**:
//
//  1. 密度: 1x 桌面必须逐像素不变 (既有 golden 的前提), 而 2x/3x 上所有参与
//     排版的尺度必须跟着走 —— 否则的结果不是"稍微小一点", 而是行高 32px
//     装不下 32dp 的字时针脚一样飞出界;
//  2. 触控: 视觉尺寸与命中尺寸必须能各自演进。这里把"设备销售渠道 KR 44px"
//     钉成**可断言的几何**而不是文档里的一句话 —— 文档会过期, 断言不会。

import (
	"runtime"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== A3 密度适配 =====

// TestSpaceTokensScaleWithDensity: Space() 在 1x 恒等、2x 翻倍、3x 三倍。
//
// tokens.go 的注释承诺"Space / Radius 经 Space()/Radius() 换算成物理像素",
// 这条就是那份承诺的执行者。Space(4) 在 1x = 4、2x = 8 是这个包的入门知识。
func TestSpaceTokensScaleWithDensity(t *testing.T) {
	cases := []struct {
		dp   int
		want int
	}{{Space1, 2}, {Space2, 4}, {Space3, 6}, {Space4, 8}, {Space5, 12},
		{Space6, 16}, {Space7, 20}, {Space8, 24}, {Space9, 32}, {Space10, 40}}

	// 1x: 恒等 (Scale<=1 走 int() 截断的快路径)
	withScaledDisplay(t, 1)
	for _, c := range cases {
		if got := Space(c.dp); got != c.want {
			t.Errorf("1x Space(%d) = %d, want %d", c.dp, got, c.want)
		}
	}
	// 2x: 翻倍
	withScaledDisplay(t, 2)
	for _, c := range cases {
		if got := Space(c.dp); got != c.want*2 {
			t.Errorf("2x Space(%d) = %d, want %d", c.dp, got, c.want*2)
		}
	}
	if got := Space(Space2); got != 8 {
		t.Fatalf("2x 屏 Space(Space2=4dp) = %d, want 8", got)
	}
	// 3x: 三倍
	withScaledDisplay(t, 3)
	if got := Space(Space4); got != 24 {
		t.Errorf("3x 屏 Space(Space4=8dp) = %d, want 24", got)
	}
	// 半档不能因为四舍五入而丢失: 6dp 在 3x 上是 18
	if got := Space(Space3); got != 18 {
		t.Errorf("3x 屏 Space(Space3=6dp) = %d, want 18", got)
	}
}

// TestRadiusTokensScaleWithDensity: 圆角同样随密度走, 但两个哨兵不能动。
func TestRadiusTokensScaleWithDensity(t *testing.T) {
	withScaledDisplay(t, 2)
	for _, r := range []RadiusToken{RadiusXS, RadiusSM, RadiusMD, RadiusLG, RadiusXL} {
		if got := Radius(r); got != int(r)*2 {
			t.Errorf("2x 屏 Radius(%d) = %d, want %d", int(r), got, int(r)*2)
		}
	}
	// RadiusFull 是"胶囊形"哨兵, 与密度无关 —— 它是负常数而不是一个尺寸
	if got := Radius(RadiusFull); got != -1 {
		t.Errorf("Radius(RadiusFull) = %d, want -1 (哨兵值不参与换算)", got)
	}
	// RadiusNone 是直角, 0 乘任何系数都还是 0
	if got := Radius(RadiusNone); got != 0 {
		t.Errorf("Radius(RadiusNone) = %d, want 0", got)
	}
}

// TestTouchMinIgnoresDensity: TouchMin 是物理像素, 不随 density 放大。
//
// 这是最容易被人"顺手统一"的一处: 一旦给 TouchMin 也乘上系数, 3x 屏上的命
// 中区会变成 132px —— 一排按钮的命中区会互相吃掉三个身位。
func TestTouchMinIgnoresDensity(t *testing.T) {
	for _, scale := range []float64{1, 2, 3} {
		withScaledDisplay(t, scale)
		if TouchMin != 44 {
			t.Fatalf("%vx 屏 TouchMin = %d, want 恒 44 (物理像素口径)", scale, TouchMin)
		}
		if got := TouchInflate(Rect{X: 0, Y: 0, W: 10, H: 10}, true, true); got.H != 44 || got.W != 44 {
			t.Errorf("%vx 屏 TouchInflate(10x10) = %dx%d, want 44x44", scale, got.W, got.H)
		}
	}
}

// TestRowMetricsScaleWithDensity: 集合/菜单的行高必须随密度走。
//
// 这是 S8 修掉的**真 bug**: 这些行高曾经是裸的物理像素常量, 于是 2x 屏上
// 行还是 32px, 而行里的字已经被 dpToPx 放大到 32px —— 一行 32 装一个 32 的
// 字, 上下留白归零, 中文还会在下一帧被裁掉一点。修之前这条必红。
func TestRowMetricsScaleWithDensity(t *testing.T) {
	withScaledDisplay(t, 2)
	if got := Space(tableRowH); got != 64 {
		t.Errorf("2x 屏 table 行高 = %d, want 64 (32dp)", got)
	}
	if got := Space(listRowH); got != 64 {
		t.Errorf("2x 屏 list-item 行高 = %d, want 64 (32dp)", got)
	}
	if got := Space(treeRowH); got != 64 {
		t.Errorf("2x 屏 tree 行高 = %d, want 64 (32dp)", got)
	}
	if got := Space(menuItemH); got != 64 {
		t.Errorf("2x 屏 menu-item 行高 = %d, want 64 (32dp)", got)
	}
	if got := Space(pageBtnSize); got != 64 {
		t.Errorf("2x 屏 pagination 按钮边长 = %d, want 64 (32dp)", got)
	}
	withScaledDisplay(t, 1)
	if Space(tableRowH) != 32 || Space(menuItemH) != 32 || Space(pageBtnSize) != 32 {
		t.Fatalf("1x 屏必须回到 32 (桌面逐像素不变): %d/%d/%d",
			Space(tableRowH), Space(menuItemH), Space(pageBtnSize))
	}
}

// ===== A2 触摸模式 =====

// touchNode 造一个带 onClick 的节点 (命中区扩展只认"自己会响应点击"的节点)。
func touchNode(tag string) *GuiNode {
	n := &GuiNode{Tag: tag, Props: map[string]object.Value{}}
	n.Props["onClick"] = object.NewBuiltin(tag+"Click", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	return n
}

func touchBox(tag string, box Rect) Rect {
	n := touchNode(tag)
	n.Box = box
	return touchHitBox(n)
}

// TestTouchModeDefaultOffOnDesktop: 桌面 (windows/linux/darwin) 默认关闭。
//
// **这条是整批改动的安全阀**: 只要它成立, 触摸模式对既有桌面应用的命中行为
// 就是零影响 —— 所有既有点选测试都跑在关闭态。
func TestTouchModeDefaultOffOnDesktop(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	t.Setenv("GOX_TOUCH_MODE", "")
	if TouchModeEnabled() {
		t.Fatalf("桌面 GOOS (%s) 不该默认进触摸模式", runtime.GOOS)
	}
	if guessTouchPlatform() {
		t.Fatalf("guessTouchPlatform() 在 %s 上应为 false", runtime.GOOS)
	}
}

// TestTouchModeSources: 环境变量与显式开关的优先级。
func TestTouchModeSources(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()

	t.Setenv("GOX_TOUCH_MODE", "1")
	if !TouchModeEnabled() {
		t.Error("GOX_TOUCH_MODE=1 应开启触摸模式")
	}
	t.Setenv("GOX_TOUCH_MODE", "0")
	if TouchModeEnabled() {
		t.Error("GOX_TOUCH_MODE=0 不该开启触摸模式")
	}

	// 显式开关优先级最高: 环境变量开着也能关掉
	t.Setenv("GOX_TOUCH_MODE", "1")
	off := false
	SetTouchMode(&off)
	if TouchModeEnabled() {
		t.Error("显式 SetTouchMode(false) 必须压过 GOX_TOUCH_MODE=1")
	}
	on := true
	SetTouchMode(&on)
	if !TouchModeEnabled() {
		t.Error("显式 SetTouchMode(true) 必须开启")
	}
	SetTouchMode(nil) // nil = 回落
	if !TouchModeEnabled() {
		t.Error("SetTouchMode(nil) 应回落到环境变量 (此时为 1)")
	}
}

// TestTouchHitBoxSmallControlsAlwaysExpanded: checkbox / radio 无条件补到 44。
//
// S4 就把这条发出去了 (18×18 连鼠标都嫌小), 这里把它从"注释里的承诺"变成
// 断言 —— 免得有人为了"统一两张表的口径"顺手把它改成"仅触摸模式"。
func TestTouchHitBoxSmallControlsAlwaysExpanded(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	t.Setenv("GOX_TOUCH_MODE", "")

	got := touchBox("checkbox", Rect{X: 100, Y: 100, W: 18, H: 18})
	if got.H != TouchMin || got.W != TouchMin {
		t.Fatalf("checkbox 18×18 的命中区 = %d×%d, want %d×%d", got.W, got.H, TouchMin, TouchMin)
	}
	// 以中心外扩: 盒子不动, 往四周各补 (44-18)/2 = 13
	if got.X != 87 || got.Y != 87 {
		t.Errorf("命中区应按中心外扩到 (87,87), got (%d,%d)", got.X, got.Y)
	}
}

// TestTouchHitBoxGatedByTouchMode: 其余控件只在触摸模式下补。
//
// 桌面**绝不能**因为这些按钮只有 36 高就把命中区扩出去 —— 工具栏上相邻两个
// 36 高的按钮会互相吃点击, 而这类 bug 只在"点了 A 却选中了 B"时才被注意到。
func TestTouchHitBoxGatedByTouchMode(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	t.Setenv("GOX_TOUCH_MODE", "")
	small := Rect{X: 0, Y: 0, W: 80, H: 32}

	if got := touchBox("button", small); got != small {
		t.Fatalf("桌面模式下 button 命中区不该被扩: got %v, want %v", got, small)
	}
	on := true
	SetTouchMode(&on)
	got := touchBox("button", small)
	if got.H != TouchMin {
		t.Fatalf("触摸模式下 button 命中区高 = %d, want %d", got.H, TouchMin)
	}
	if got.W != 80 {
		t.Errorf("宽 80 已经够了, 不该被改动: got %d", got.W)
	}
	if got.Y != (32-TouchMin)/2 {
		t.Errorf("外扩应以中心为准: Y = %d, want %d", got.Y, (32-TouchMin)/2)
	}
}

// TestTouchHitBoxRowsVerticalOnly: 整行类只补垂直方向。
//
// 行本来就撑满容器宽, 横向再扩出去的那几像素落在父容器之外 —— 既画不出来
// 也点不到, 只会把"点 Repo 外侧空白"误判成"点到了这一行"。
func TestTouchHitBoxRowsVerticalOnly(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	on := true
	SetTouchMode(&on)

	row := Rect{X: 0, Y: 100, W: 300, H: 32}
	got := touchBox("list-item", row)
	if got.H != TouchMin {
		t.Errorf("list-item 命中区高 = %d, want %d", got.H, TouchMin)
	}
	if got.W != 300 || got.X != 0 {
		t.Errorf("list-item 横向不该被扩: X=%d W=%d, want 0/300", got.X, got.W)
	}
	if got.Y != 100+(32-TouchMin)/2 {
		t.Errorf("list-item 应以中心外扩: Y=%d", got.Y)
	}
}

// TestTouchHitBoxIgnoresInertNodes: 没有 onClick 的节点不参与扩展。
func TestTouchHitBoxIgnoresInertNodes(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	on := true
	SetTouchMode(&on)

	n := &GuiNode{Tag: "button", Props: map[string]object.Value{}}
	n.Box = Rect{X: 0, Y: 0, W: 80, H: 32}
	if got := touchHitBox(n); got != n.Box {
		t.Fatalf("无 onClick 的节点不该被扩: got %v, want %v", got, n.Box)
	}
}

// TestTouchHitKeepsVisualBoxUntouched: 命中区扩展**不改** Box。
//
// 这是整套方案的前提: 绘制读 Box, 命中读 touchHitBox。若哪天有人写成
// "在 touchHitBox 里直接改 Box", 视觉尺寸会随 tapping 模式忽大忽小。
func TestTouchHitKeepsVisualBoxUntouched(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	on := true
	SetTouchMode(&on)

	n := touchNode("checkbox")
	n.Box = Rect{X: 50, Y: 50, W: 18, H: 18}
	expanded := touchHitBox(n)
	if n.Box.W != 18 || n.Box.H != 18 {
		t.Fatalf("Box 被改动了: %v", n.Box)
	}
	if expanded.Contains(40, 60) != true || n.Box.Contains(40, 60) != false {
		t.Errorf("视觉框外的点应命中扩展区而不命中视觉框 (expanded=%v box=%v)", expanded, n.Box)
	}
}

// TestTouchModeExpandsCompositeHits: 组件自绘区 (标签条 / 页码按钮) 也跟着扩。
//
// 这两处的按钮是"画在盒子里的矩形", 不是节点 —— HitTest 的节点遍历看不见
// 它们, 漏掉这两处等于触摸模式只覆盖了半套 UI, 而最容易点空的恰恰是它们。
func TestTouchModeExpandsCompositeHits(t *testing.T) {
	t.Cleanup(ResetTouchModeForTest)
	ResetTouchModeForTest()
	t.Setenv("GOX_TOUCH_MODE", "")

	strip := Rect{X: 0, Y: 0, W: 60, H: 32}
	// (30,36): 标签条下方 4px, 在 (44-32)/2 = 6 的补贴范围内
	if touchHitContains(strip, 30, 36) {
		t.Fatal("桌面模式下标签条外不该命中")
	}
	on := true
	SetTouchMode(&on)
	if !touchHitContains(strip, 30, 36) {
		t.Error("触摸模式下标签条外 6px (=(44-32)/2) 内应命中")
	}
	if touchHitContains(strip, 30, 44) {
		t.Error("补贴是精确的 TouchMin, 不该无限外扩")
	}
}

// TestOverlayAndFeedbackMetricsScaleWithDensity: S8 第二批换算。
//
// 与 TestRowMetricsScaleWithDensity 同一类bug, 只是这批不盛文字而是"定尺寸":
// toast 边距、tooltip 内边距与封顶宽、抽屉缺省宽、alert 图标、badge 最小宽、
// avatar 缺省与状态点、empty 图示。2x 上它们必须翻倍, 1x 上必须逐像素不变。
func TestOverlayAndFeedbackMetricsScaleWithDensity(t *testing.T) {
	type pair struct {
		name string
		dp   int
	}
	items := []pair{
		{"toastMargin", toastMargin},
		{"tipPadX", tipPadX},
		{"tipPadY", tipPadY},
		{"tipMaxW", tipMaxW},
		{"drawerDefaultW", drawerDefaultW},
		{"alertIconW", alertIconW},
		{"badgeMinW", badgeMinW},
		{"avatarDefault", avatarDefault},
		{"avatarStatusD", avatarStatusD},
		{"emptyIconSize", emptyIconSize},
	}
	withScaledDisplay(t, 2)
	for _, it := range items {
		if got := Space(it.dp); got != it.dp*2 {
			t.Errorf("2x 屏 %s = %d, want %d", it.name, got, it.dp*2)
		}
	}
	// avatarSize 缺省走 Space(avatarDefault), 显式 size 走 dpToPx —— 两条路
	// 在 2x 上必须同量级, 否则"写了 size 反而更小"。
	n := &GuiNode{Tag: "avatar", Props: map[string]object.Value{}}
	if got, want := n.avatarSize(), 2*avatarDefault; got != want {
		t.Errorf("2x 屏 avatar 缺省尺寸 = %d, want %d", got, want)
	}
	withScaledDisplay(t, 1)
	for _, it := range items {
		if got := Space(it.dp); got != it.dp {
			t.Errorf("1x 屏 %s = %d, want %d (桌面逐像素不变)", it.name, got, it.dp)
		}
	}

	// 上面的表只证明"常量是 dp"; 这一段证明"读数**也**走了换算" ——
	// tooltip 与 toast 都是"文字外面套一圈"的结构, 任一侧漏了 Space() 都会让
	// 2x 上的比例失衡而不报错。
	//
	// 比的是**扣掉文字后剩下的那一圈**而不是总尺寸: 字形按物理像素取整, 文字
	// 宽度本身并不随密度严格线性 (实测 68 → 144 而不是 136), 把字体度量拉进
	// 断言只会得到一条"允许误差 ±8"的软断言; 而留白那一圈是纯整数运算, 必须
	// 精确翻倍 —— 它正是"测量侧与绘制侧是否同口径"的探针。
	host := &GuiNode{Tag: "button", Props: map[string]object.Value{}}
	withStr(host, "text", "提示文本")
	toast := &GuiNode{Tag: "toast", Props: map[string]object.Value{}}
	withStr(toast, "message", "已保存")
	msg := host.tipText()
	tipW1, tipH1 := tooltipPopupSize(host)
	tw1, th1 := MeasureText(msg, tipFontSize(host))
	toastW1, toastH1 := toast.intrinsicSize()

	withScaledDisplay(t, 2)
	tipW2, tipH2 := tooltipPopupSize(host)
	tw2, th2 := MeasureText(msg, tipFontSize(host))
	tipPad1, tipPad2 := tipW1-tw1, tipW2-tw2
	if tipPad2 != 2*tipPad1 {
		t.Errorf("2x tooltip 左右留白合计 = %d, want %d (1x %d 的两倍)",
			tipPad2, 2*tipPad1, tipPad1)
	}
	if got, want := tipH2-th2, 2*(tipH1-th1); got != want {
		t.Errorf("2x tooltip 上下留白合计 = %d, want %d", got, want)
	}
	toastW2, toastH2 := toast.intrinsicSize()
	if toastW2 != 2*toastW1 || toastH2 != 2*toastH1 {
		t.Errorf("2x toast 卡片 = %dx%d, want %dx%d (1x 的两倍)",
			toastW2, toastH2, 2*toastW1, 2*toastH1)
	}
}

// TestPopupMetricsScaleWithDensity: 两个 picker 的弹层几何必须整体随密度翻倍。
//
// 上一条断言盯"常量单位"，这条盯"常量被谁用了": dateMetrics / cpMetrics 这两个
// 中间层存在的意义就是让布局 / 绘制 / 命中三处读同一份物理像素 —— 而唯一能证明
// 它们真的被用上了的证据，是**弹层尺寸整体翻倍**（漏掉任何一处换算都会让这个
// 等式不成立）。日历这类网格还多一层说服力: 格子里的日期数字按 dpToPx 翻倍，
// 格子不翻倍就变成"28px 的格子装 32px 的字"。
func TestPopupMetricsScaleWithDensity(t *testing.T) {
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}

	withScaledDisplay(t, 1)
	dw1, dh1 := datepickerPopupSize(dp)
	cw1, ch1 := colorpickerPopupSize(cp)
	// 1x 上的数字必须是"常量照着注释算出来的那一个" —— 这条同时盯住回头被
	// 改成 Space(x) 之外写法的可能 (桌面 1x 逐像素不变是硬约束)。
	if want := 2*dpPopupPad + 7*dpCellW; dw1 != want {
		t.Errorf("1x 日历弹层宽 = %d, want %d", dw1, want)
	}
	if want := 2*cpPopupPad + 8*cpCellSize + 7*cpCellGap; cw1 != want {
		t.Errorf("1x 色板弹层宽 = %d, want %d", cw1, want)
	}

	withScaledDisplay(t, 2)
	dw2, dh2 := datepickerPopupSize(dp)
	cw2, ch2 := colorpickerPopupSize(cp)
	if dw2 != 2*dw1 || dh2 != 2*dh1 {
		t.Errorf("2x 日历弹层应为 1x 的两倍: got %dx%d, want %dx%d", dw2, dh2, 2*dw1, 2*dh1)
	}
	if cw2 != 2*cw1 || ch2 != 2*ch1 {
		t.Errorf("2x 色板弹层应为 1x 的两倍: got %dx%d, want %dx%d", cw2, ch2, 2*cw1, 2*ch1)
	}
	// 网格步进变了, 命中反算必须跟着变: 同一年的同一天在两个密度下的格子
	// 中心应当落在"同一格"上 —— 丢了这条就不是点歪一格, 是整片错位。
	m2 := dateMetricsOf()
	if m2.cellW != 2*dpCellW || m2.cellH != 2*dpCellH {
		t.Errorf("2x 日期格 = %dx%d, want %dx%d", m2.cellW, m2.cellH, 2*dpCellW, 2*dpCellH)
	}
	if got := cpMetricsOf(); got.step != 2*(cpCellSize+cpCellGap) || got.cellSize != 2*cpCellSize {
		t.Errorf("2x 色板步进/格子 = %d/%d, want %d/%d",
			got.step, got.cellSize, 2*(cpCellSize+cpCellGap), 2*cpCellSize)
	}
}
