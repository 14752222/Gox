package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== avoidReserved (折叠保留区避让) 的布局用例 =====
//
// 这些用例全部走**纯 Go 布局**: 用 reportPostureGo 注入一块带保留区的屏
// (不需要真窗口/VM), 然后 Layout 一棵节点树, 断言 Box。
//
// 用 reportPostureGo 而不是 ReportPostureFromFold 是为了顺带覆盖"脚本上报
// 的 regions 能被布局层读到"这条链路 —— 若哪天屏表与布局层之间的字段名漂移,
// 这里会先红。

// withFoldRegion 注入一块"半折"屏 + 一条竖直折痕带, 并注册清场。
//
// d 参数是显示器尺寸 (与 area 同坐标系); band 是折痕带 (显示器坐标)。
func withFoldRegion(t *testing.T, dw, dh int, bandX, bandW int) {
	t.Helper()
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	SetDefaultFactory(nil)

	reportPostureGo(routeObj(
		"display", "fold-test", "posture", "half-open",
		"width", object.NewNumber(float64(dw)), "height", object.NewNumber(float64(dh)),
		"hinge", routeObj("x", object.NewNumber(float64(bandX)), "y", object.NewNumber(0),
			"width", object.NewNumber(float64(bandW)), "height", object.NewNumber(float64(dh))),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "division",
				"x", object.NewNumber(float64(bandX)), "y", object.NewNumber(0),
				"width", object.NewNumber(float64(bandW)), "height", object.NewNumber(float64(dh)),
				"active", object.NewBoolean(true)),
		}),
	))
}

// TestAvoidReservedNotSetNoEffect 是本特性最重要的一条防线:
// **没有写 avoidReserved 的节点必须一个像素都不动** —— 全屏视频/背景图正是
// 靠这条才能延伸到折痕下面 (需求里"默认不避让"的立场)。
func TestAvoidReservedNotSetNoEffect(t *testing.T) {
	withFoldRegion(t, 400, 300, 190, 20)

	root := mkNode("column", nil)
	a := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)

	// 普通纵排: a 在 y=0, b 在 y=10 —— 即使折痕带横在 x=190..210 也不受影响。
	if a.Box.X != 0 || a.Box.Y != 0 || b.Box.Y != 10 {
		t.Fatalf("未声明 avoidReserved 不得改变布局: a=%v b=%v", a.Box, b.Box)
	}
}

// TestAvoidReservedEmptyValueNoEffect 钉住"取值不认识 → 不避"。
//
// 拼错时**静默不生效**是有意的: 误用的代价是"莫名多出一段空白", 而用户唯一的
// 线索就是自己写下的那个词 —— 收窄词表比"猜对了但猜错方向"更好排查。
func TestAvoidReservedEmptyValueNoEffect(t *testing.T) {
	withFoldRegion(t, 400, 300, 190, 20)

	for _, bad := range []string{"", "foldd", "vertical", "yes"} {
		root := mkNode("column", nil)
		root.Props["avoidReserved"] = object.NewString(bad)
		a := mkNode("rect", map[string]float64{"width": 50, "height": 10})
		root.Children = []*GuiNode{a}
		Layout(root, 400, 300)
		if a.Box.X != 0 {
			t.Fatalf("avoidReserved=%q 不该生效: x=%d", bad, a.Box.X)
		}
	}
}

// TestAvoidReservedShrinksMainAxis 验证主轴避让: 纵向容器的**折痕是竖条**
// (影响 X) 时, 主轴 (Y) 不受影响, 但一个横向 row 子节点的可用宽要被挖掉。
//
// 这条用例覆盖"先挖后算"的核心价值: flexGrow 必须在**收缩后**的宽度上分配,
// 否则元素会被撑到折痕底下。
func TestAvoidReservedShrinksMainAxis(t *testing.T) {
	withFoldRegion(t, 400, 300, 190, 20)

	// 一个 row: 左边固定 50, 右边 flexGrow 吃满剩余。
	// 折痕带在 x=190..210 ⇒ 可用宽被切成 [0,190) 与 [210,400)。
	// 取较长一侧 = 后面那截 (190 vs 190 相等, 实现取"带之后"), 即可用区
	// 从 x=210 开始、宽 190。
	root := mkNode("row", nil)
	root.Props["avoidReserved"] = object.NewString("fold")
	a := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 20, "height": 10, "flexGrow": 1})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)

	if a.Box.X < 210 {
		t.Fatalf("避让后第一个元素越过了折痕带: a.x=%d (应 >= 210)", a.Box.X)
	}
	// 两者都不与 [190,210) 相交。
	for _, n := range []*GuiNode{a, b} {
		if n.Box.X < 210 && n.Box.X+n.Box.W > 190 {
			t.Fatalf("元素压在折痕带上: %v", n.Box)
		}
	}
	// flexGrow 吃满了收缩后的可用宽 (不是原始的 400)。
	if b.Box.W <= 0 || b.Box.X+b.Box.W > 400 {
		t.Fatalf("grow 应落在收缩后的可用区内: b=%v", b.Box)
	}
}

// TestAvoidReservedCrossAxisShifts 覆盖**交叉轴**避让: 竖排容器 (主轴 Y) 里的
// 元素压在竖直折痕带上时, 应该被顺到带之后。
func TestAvoidReservedCrossAxisShifts(t *testing.T) {
	withFoldRegion(t, 400, 300, 190, 20)

	root := mkNode("column", nil)
	root.Props["avoidReserved"] = object.NewString("fold")
	// 显式宽的 rect: 交叉轴不 stretch, 默认 left 对齐 ⇒ 会压在带上。
	a := mkNode("rect", map[string]float64{"width": 300, "height": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)

	// 300 宽的块从 x=0 起就会横跨 190..210, 必须被顺走。
	if a.Box.X < 210 {
		t.Fatalf("交叉轴未避让: a.x=%d (应 >= 210)", a.Box.X)
	}
}

// TestAvoidReservedOcclusionKindFilter 验证 kind 过滤: avoidReserved="fold"
// 不该避让 occlusion 区 (那是完全不同的东西 —— 屏下摄像头 vs 折痕)。
func TestAvoidReservedOcclusionKindFilter(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	SetDefaultFactory(nil)

	// 只报一条 occlusion, 不报 division。
	reportPostureGo(routeObj(
		"display", "occ-test", "posture", "flat",
		"width", object.NewNumber(400), "height", object.NewNumber(300),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "occlusion",
				"x", object.NewNumber(190), "y", object.NewNumber(0),
				"width", object.NewNumber(20), "height", object.NewNumber(300),
				"active", object.NewBoolean(true)),
		}),
	))

	// avoidReserved="fold" → 不认 occlusion, 不受影响。
	root := mkNode("column", nil)
	root.Props["avoidReserved"] = object.NewString("fold")
	a := mkNode("rect", map[string]float64{"width": 300, "height": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 0 {
		t.Fatalf("fold 不该避让 occlusion: x=%d", a.Box.X)
	}

	// avoidReserved="occlusion" → 生效。
	root2 := mkNode("column", nil)
	root2.Props["avoidReserved"] = object.NewString("occlusion")
	b := mkNode("rect", map[string]float64{"width": 300, "height": 10})
	root2.Children = []*GuiNode{b}
	Layout(root2, 400, 300)
	if b.Box.X < 210 {
		t.Fatalf("occlusion 应被避让: x=%d (应 >= 210)", b.Box.X)
	}

	// avoidReserved="all" → 也生效。
	root3 := mkNode("column", nil)
	root3.Props["avoidReserved"] = object.NewString("all")
	c := mkNode("rect", map[string]float64{"width": 300, "height": 10})
	root3.Children = []*GuiNode{c}
	Layout(root3, 400, 300)
	if c.Box.X < 210 {
		t.Fatalf("all 应避让 occlusion: x=%d", c.Box.X)
	}
}

// TestAvoidReservedIgnoresInactiveRegion 钉住"平展时零宽 division 不产生避让"。
//
// 这是把 active 字段一路带到布局层的**唯一理由**: 不判 active 的话, 平展时
// 那条宽度为 0 的折痕也会被当成一条带, 虽然宽度 0 的带在 avoidShift 里因为
// lo==hi 不会命中 —— 但一旦宿主报的是"宽度 24 但 active=false"(折起但折痕
// 尺寸还是旧的), 不判 active 就会凭空多出一段空白。
func TestAvoidReservedIgnoresInactiveRegion(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	SetDefaultFactory(nil)

	reportPostureGo(routeObj(
		"display", "inactive-test", "posture", "flat",
		"width", object.NewNumber(400), "height", object.NewNumber(300),
		"regions", object.NewArray([]object.Value{
			routeObj("kind", "division",
				"x", object.NewNumber(190), "y", object.NewNumber(0),
				"width", object.NewNumber(20), "height", object.NewNumber(300),
				"active", object.NewBoolean(false)),
		}),
	))

	root := mkNode("column", nil)
	root.Props["avoidReserved"] = object.NewString("fold")
	a := mkNode("rect", map[string]float64{"width": 300, "height": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 0 {
		t.Fatalf("inactive 的保留区不该触发避让: x=%d", a.Box.X)
	}
}

// TestAvoidReservedOnScrollIgnoredWithWarning 是需求"陷阱 3"的回归。
//
// scroll 上的 avoidReserved 必须被忽略**并出声**: 静默忽略会让写属性的人以为
// 生效了, 而"滚动内容整篇跳动"这个症状肉眼极易放过。
//
// 断言走**像素级**: 同一棵树在"有折痕带"与"无折痕带"下渲染, 两帧必须逐字节
// 相同。这是"连续滚动内容不跳"在单测里唯一站得住的证明 —— Box 断言只覆盖单个
// 节点, 而"整篇跳动"是整棵子树一起位移。
func TestAvoidReservedOnScrollIgnoredWithWarning(t *testing.T) {
	// 先在有折痕的环境下渲染一次。
	withFoldRegion(t, 400, 300, 190, 20)
	resetWarnRing()
	t.Cleanup(resetWarnRing)

	build := func() *GuiNode {
		root := mkNode("scroll", nil)
		root.Props["avoidReserved"] = object.NewString("fold")
		col := mkNode("column", nil)
		for i := 0; i < 12; i++ {
			col.Children = append(col.Children, mkNode("rect", map[string]float64{
				"width": 380, "height": 20, "margin": 2,
			}))
		}
		root.Children = []*GuiNode{col}
		return root
	}
	withFold := renderTree(build(), 400, 300)

	// 必须留了一条警告。
	found := false
	for _, w := range warnSnapshot() {
		if contains(w.Text, "avoidReserved") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("<scroll> 上的 avoidReserved 应产生警告, 实际: %v", warnSnapshot())
	}
	// 再布局一次, 警告**不重复** (热路径去重)。
	resetWarnRing()
	Layout(build(), 400, 300)
	for _, w := range warnSnapshot() {
		if contains(w.Text, "avoidReserved") {
			t.Fatalf("同一节点重复布局不该重复警告: %v", w.Text)
		}
	}

	// 清掉折痕后再渲染一次: 两帧必须逐像素一致。
	resetScreenStateForTest()
	SetDefaultFactory(nil)
	noFold := renderTree(build(), 400, 300)

	if len(withFold.Pix) != len(noFold.Pix) {
		t.Fatalf("画布尺寸不一致")
	}
	for i := range withFold.Pix {
		if withFold.Pix[i] != noFold.Pix[i] {
			t.Fatalf("scroll 内容随折痕改变了 (第 %d 字节: %d vs %d) —— "+
				"连续滚动内容必须完全不受保留区影响", i, withFold.Pix[i], noFold.Pix[i])
		}
	}
}

// containing 是本文件要的极简子串判断 (避免为一个断言引 strings)。
func contains(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestAvoidShiftUnit 单测位移算法本身 (含"让过之后又与后面的带相交"这条)。
func TestAvoidShiftUnit(t *testing.T) {
	bands := []reservedBand{
		{vertical: true, lo: 100, hi: 120},
		{vertical: true, lo: 130, hi: 150},
	}
	// 元素 [90,110) 压在第一条上 ⇒ 推到 120; 120+20=140 又压在第二条 ⇒ 推到 150。
	if got := avoidShift(90, 20, bands, true); got != 150 {
		t.Fatalf("连环避让应推到 150: %d", got)
	}
	// 不同轴的带不影响。
	if got := avoidShift(90, 20, bands, false); got != 90 {
		t.Fatalf("不同轴不该影响: %d", got)
	}
	// 不相交时原样返回。
	if got := avoidShift(0, 50, bands, true); got != 0 {
		t.Fatalf("不相交应原样返回: %d", got)
	}
	// 空带 / 零尺寸直接返回。
	if got := avoidShift(7, 10, nil, true); got != 7 {
		t.Fatalf("空带应原样返回: %d", got)
	}
	if got := avoidShift(7, 0, bands, true); got != 7 {
		t.Fatalf("零尺寸应原样返回: %d", got)
	}
}

// TestAvoidedAreaUnit 单测可用区收缩 (取较长的一侧)。
func TestAvoidedAreaUnit(t *testing.T) {
	area := Rect{X: 0, Y: 0, W: 400, H: 300}
	bands := []reservedBand{{vertical: true, lo: 100, hi: 120}}
	got := avoidedArea(area, bands, true)
	// 带之前 100, 带之后 280 ⇒ 取之后。
	if got.X != 120 || got.W != 280 {
		t.Fatalf("应取较长一侧 (带之后): %#v", got)
	}
	// 带靠右时取带之前。
	bands = []reservedBand{{vertical: true, lo: 380, hi: 400}}
	got = avoidedArea(area, bands, true)
	if got.X != 0 || got.W != 380 {
		t.Fatalf("应取带之前: %#v", got)
	}
	// 带完全在 area 之外时不影响。
	bands = []reservedBand{{vertical: true, lo: 500, hi: 520}}
	got = avoidedArea(area, bands, true)
	if got != area {
		t.Fatalf("带在区外应原样返回: %#v", got)
	}
	// 横向容器的带按 Y 判。
	bands = []reservedBand{{vertical: false, lo: 100, hi: 120}}
	got = avoidedArea(area, bands, false)
	if got.Y != 120 || got.H != 180 {
		t.Fatalf("横向容器应按 Y 收缩: %#v", got)
	}
}
