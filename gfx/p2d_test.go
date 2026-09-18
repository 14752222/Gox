package gfx

import (
	"testing"
)

// P2-5 滚动容器 <scroll>。
//
// 设计要点 (与任务书的差异见 scroll.go 顶部注释): 子节点的 Box 直接是
// **屏幕坐标** (布局时已减去 offsetY), 而不是"Box 不变 + 绘制变换"。
// 好处是偏移只存在于一个地方: 命中测试、脏矩形比对、光标定位都不必再
// 单独处理。所以这里的断言既看 offsetY, 也看子节点实际的 Box。

// mkScroll 造一个 scroll (宽 w 高 h) 内装 rows 个高 rowH 的行。
func mkScroll(w, h, rows, rowH int) (*GuiNode, *GuiNode, []*GuiNode) {
	root := mkNode("column", nil)
	sc := mkNode("scroll", map[string]float64{"width": float64(w), "height": float64(h)})
	mountChildren(root, sc)
	var kids []*GuiNode
	for i := 0; i < rows; i++ {
		r := mkNode("rect", map[string]float64{"height": float64(rowH)})
		mountChildren(sc, r)
		kids = append(kids, r)
	}
	Layout(root, 400, 400)
	return root, sc, kids
}

func TestScrollContentHeightAndViewport(t *testing.T) {
	_, sc, kids := mkScroll(240, 120, 20, 36)

	if sc.contentH != 20*36 {
		t.Fatalf("contentH = %d, want %d", sc.contentH, 20*36)
	}
	if sc.offsetY != 0 {
		t.Fatalf("初始 offsetY = %d, want 0", sc.offsetY)
	}
	if got := sc.scrollMaxOffset(); got != 20*36-120 {
		t.Fatalf("maxOffset = %d, want %d", got, 20*36-120)
	}
	// 内容超高 → 视口右侧让出滚动条宽度, 子节点宽度也跟着收窄
	vp := sc.scrollViewport()
	if vp.W != 240-scrollTrackW {
		t.Fatalf("视口宽 = %d, want %d", vp.W, 240-scrollTrackW)
	}
	if kids[0].Box.W != 240-scrollTrackW {
		t.Fatalf("子内容宽 = %d, want %d (应避开滚动条)", kids[0].Box.W, 240-scrollTrackW)
	}
	if kids[0].Box.Y != sc.Box.Y || kids[2].Box.Y != sc.Box.Y+2*36 {
		t.Fatalf("未滚动时子节点位置不对: row0=%v row2=%v", kids[0].Box, kids[2].Box)
	}

	// 内容不足一屏: 无滚动条、无偏移可滚
	_, small, smallKids := mkScroll(240, 120, 2, 36)
	if small.contentH != 72 {
		t.Fatalf("contentH = %d, want 72", small.contentH)
	}
	if small.scrollMaxOffset() != 0 {
		t.Fatalf("内容不足一屏时 maxOffset 应为 0")
	}
	if _, ok := small.scrollThumb(); ok {
		t.Fatalf("内容不足一屏不该有滚动条")
	}
	if small.scrollBy(50) {
		t.Fatalf("内容不足一屏不该能滚动")
	}
	if smallKids[0].Box.W != 240 {
		t.Fatalf("不出滚动条时子内容应占满宽度: %d", smallKids[0].Box.W)
	}
}

func TestScrollByClampsAndShiftsChildren(t *testing.T) {
	_, sc, kids := mkScroll(200, 100, 5, 40)
	if sc.scrollMaxOffset() != 100 {
		t.Fatalf("maxOffset = %d, want 100", sc.scrollMaxOffset())
	}

	if !sc.scrollBy(30) {
		t.Fatalf("内容超出时 scrollBy(30) 应返回 true")
	}
	if sc.offsetY != 30 {
		t.Fatalf("offsetY = %d, want 30", sc.offsetY)
	}
	Layout(sc, 200, 100)
	if kids[0].Box.Y != sc.Box.Y-30 {
		t.Fatalf("子内容未随偏移上移: row0.Y = %d, want %d", kids[0].Box.Y, sc.Box.Y-30)
	}

	// 下界钳位
	sc.offsetY = 0
	if sc.scrollBy(-50) {
		t.Fatalf("已在顶部, scrollBy(-50) 应为 false")
	}
	if sc.offsetY != 0 {
		t.Fatalf("顶部越界: offsetY = %d", sc.offsetY)
	}
	// 上界钳位
	sc.offsetY = 0
	sc.scrollBy(9999)
	if sc.offsetY != 100 {
		t.Fatalf("底部越界: offsetY = %d, want 100", sc.offsetY)
	}
	if sc.scrollBy(10) {
		t.Fatalf("已在底部, 继续下滚应为 false (滚轮要能继续往外传)")
	}
}

func TestScrollClampsOffsetWhenContentShrinks(t *testing.T) {
	_, sc, kids := mkScroll(200, 100, 5, 40)
	sc.offsetY = 100
	Layout(sc, 200, 100)

	// 内容缩到不足一屏: 旧的偏移必须被钳回 0, 否则内容会被"滚到看不见"
	sc.Children = sc.Children[:1]
	sc.Children[0].Parent = sc
	Layout(sc, 200, 100)

	if sc.contentH != 40 {
		t.Fatalf("contentH = %d, want 40", sc.contentH)
	}
	if sc.offsetY != 0 {
		t.Fatalf("内容变短后 offsetY 未钳位: %d", sc.offsetY)
	}
	if kids[0].Box.Y != sc.Box.Y {
		t.Fatalf("钳位后内容应回到顶部: row0.Y = %d", kids[0].Box.Y)
	}
}

func TestScrollClipsContentOutsideViewport(t *testing.T) {
	root := mkNode("column", nil)
	sc := mkNode("scroll", map[string]float64{"width": 200, "height": 100})
	mountChildren(root, sc)
	for i := 0; i < 5; i++ {
		r := mkNode("rect", map[string]float64{"height": 40})
		withStr(r, "background", "#c0392b") // = pxRed, 避免与调色板比较时比错色值
		mountChildren(sc, r)
	}
	img := renderTree(root, 400, 400)

	// 视口内: 红
	if got := img.RGBAAt(10, 10); got != pxRed {
		t.Fatalf("视口内 (10,10) = %v, want 红", got)
	}
	// 第 3 行 (y 80..120) 被视口下沿裁掉: 99 处仍是红, 101 处必须干净
	if got := img.RGBAAt(10, 99); got != pxRed {
		t.Fatalf("视口内 (10,99) = %v, want 红", got)
	}
	if got := img.RGBAAt(10, 101); got != pxWhite {
		t.Fatalf("视口外 (10,101) = %v, want 白 (内容应被裁剪)", got)
	}
	// 滚动条占位那一列不该出现内容色 (视口宽 = 200-8)
	if got := img.RGBAAt(195, 50); got == pxRed {
		t.Fatalf("滚动条区域被内容染色了")
	}
}

func TestScrollHitTestRespectsViewport(t *testing.T) {
	root := mkNode("column", nil)
	sc := mkNode("scroll", map[string]float64{"width": 200, "height": 100})
	mountChildren(root, sc)
	// 一个比视口又宽又高的子节点: 它盖住了滚动条占位那一列与视口下沿以下,
	// 但那两处都是"画不出来"的区域, 因此也不该点中。
	// 注意必须先让内容真正溢出, 否则容器不出滚动条, 右侧也就不算视口之外。
	wide := withClick(mkNode("rect", map[string]float64{"height": 300, "width": 300}))
	mountChildren(sc, wide)
	Layout(root, 400, 400)

	if sc.contentH <= inner(sc).H {
		t.Fatalf("前置条件不成立: 内容未溢出, 视口不会让出滚动条")
	}
	if HitTest(root, 10, 10) != wide {
		t.Fatalf("视口内的点应能命中子节点")
	}
	// x=195 落在滚动条占位区 (视口宽 192): 画不出来就不该点中
	if got := HitTest(root, 195, 10); got != nil {
		t.Fatalf("滚动条占位区的点不该命中子内容, got %v", got)
	}
	// 视口下方同理
	if got := HitTest(root, 10, 150); got != nil {
		t.Fatalf("视口下方的点不该命中子内容, got %v", got)
	}
}

func TestScrollThumbRatioAndPosition(t *testing.T) {
	_, sc, _ := mkScroll(200, 100, 5, 40) // contentH 200, 视口 100
	area := inner(sc)

	thumb, ok := sc.scrollThumb()
	if !ok {
		t.Fatalf("内容超高应出现滚动条")
	}
	// 高度 = 视口/内容 比例 = 100/200 → 50
	if thumb.H != 50 {
		t.Fatalf("滑块高 = %d, want 50", thumb.H)
	}
	if thumb.Y != area.Y {
		t.Fatalf("偏移 0 时滑块应在顶部: %d", thumb.Y)
	}
	if thumb.X < area.X+area.W-scrollTrackW {
		t.Fatalf("滑块应在右侧轨道内: %+v", thumb)
	}

	sc.offsetY = 50 // 一半
	if _, ok := sc.scrollThumb(); !ok {
		t.Fatalf("offsetY 变化后仍应有滚动条")
	}
	if got := sc.mustThumbY(t); got != area.Y+25 {
		t.Fatalf("半程滑块位置 = %d, want %d", got, area.Y+25)
	}
	sc.offsetY = 100 // 到底
	if got := sc.mustThumbY(t); got+50 != area.Y+100 {
		t.Fatalf("到底时滑块下沿应贴住轨道底部: y=%d h=50", got)
	}

	// 内容极长 → 滑块不小于 scrollMinThumb
	_, long, _ := mkScroll(200, 100, 20, 36)
	thumb2, ok2 := long.scrollThumb()
	if !ok2 || thumb2.H != scrollMinThumb {
		t.Fatalf("超长内容的滑块应被钳到 %d, got %d", scrollMinThumb, thumb2.H)
	}
}

// mustThumbY 取滑块 y (缺失即用例失败)。
func (n *GuiNode) mustThumbY(t *testing.T) int {
	t.Helper()
	th, ok := n.scrollThumb()
	if !ok {
		t.Fatalf("应有滚动条")
	}
	return th.Y
}

func TestScrollWheelNotchesAndClamp(t *testing.T) {
	// 10 行 × 40 = 400 内容高, 视口 100 → maxOffset 300, 两格 (120) 还在范围内。
	root, sc, kids := mkScroll(200, 100, 10, 40)
	fake, a := mountTestApp(t, root, 400, 400)

	cx, cy := sc.Box.X+10, sc.Box.Y+10
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: -120})
	if sc.offsetY != scrollNotch {
		t.Fatalf("向下滚一格 offsetY = %d, want %d", sc.offsetY, scrollNotch)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: -120})
	if sc.offsetY != 2*scrollNotch {
		t.Fatalf("两格 offsetY = %d, want %d", sc.offsetY, 2*scrollNotch)
	}
	// 子节点随之上移 (重绘时 Layout 重排)
	if kids[0].Box.Y != sc.Box.Y-2*scrollNotch {
		t.Fatalf("子内容未上移: row0.Y = %d", kids[0].Box.Y)
	}
	// 向上滚 → 偏移减小
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: 120})
	if sc.offsetY != scrollNotch {
		t.Fatalf("向上滚一格 offsetY = %d, want %d", sc.offsetY, scrollNotch)
	}
	// 反复上滚 → 钳在 0 (事件量远超内容高)
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: 120})
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: 120})
	if sc.offsetY != 0 {
		t.Fatalf("顶部未钳位: offsetY = %d", sc.offsetY)
	}
	// 光标不在 scroll 内: 不影响它
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: 350, Y: 350, DeltaY: -120})
	if sc.offsetY != 0 {
		t.Fatalf("滚轮落在容器外时不该滚动: offsetY = %d", sc.offsetY)
	}
}

func TestScrollDemoWheelScrollsClipsAndOverscrolls(t *testing.T) {
	// 假 Surface 的唤醒通道 (arrived, 容量 16) 每轮 pump 只被消费一个 —— 所以
	// "一步之内连着推 N 个事件"会攒爆通道, 表现是整个包挂死 (本用例最早一次推
	// 40 格就是这样挂的)。
	// 正确姿势: **每推一个就跟一次 Pump**, 让这个事件当场被处理掉 (既清掉唤醒
	// 信号, 又保证后面的断言看到的是最新布局)。
	wheel := func(fake *fakeSurface, x, y, n, sign int) {
		for i := 0; i < n; i++ {
			fake.push(Event{Kind: EventMouseWheel, X: x, Y: y, DeltaY: -120 * sign})
			if !Pump(0) {
				return
			}
		}
	}

	runDemoSteps(t, "scroll_demo.js", []func(*GuiNode, *fakeSurface){
		// 1) 首帧: 20 行 × 36 = 720 内容高, 视口 120 → 一屏只看得见前几行
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirst(root, "scroll")
			if sc == nil {
				t.Fatalf("scroll_demo 缺少 scroll 节点")
			}
			if sc.Box.H != 120 || sc.Box.W != 240 {
				t.Fatalf("scroll 未按 240x120 布局: %v", sc.Box)
			}
			// {rows} 这样的静态数组子节点必须被逐个展开 (否则只剩一行文本)
			if got := len(findAll(root, "rect")); got != 20 {
				t.Fatalf("行数 = %d, want 20 (数组子节点未展开)", got)
			}
			if sc.contentH != 20*36 {
				t.Fatalf("contentH = %d, want %d", sc.contentH, 20*36)
			}
			if sc.offsetY != 0 {
				t.Fatalf("初始 offsetY = %d", sc.offsetY)
			}
			// 10 格 × 60 = 600 = maxOffset, 正好到底
			wheel(fake, sc.Box.X+8, sc.Box.Y+8, 10, 1)
		},
		// 2) 滚到底: 子内容上移, 已滚出视口的行既画不到也点不中
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirst(root, "scroll")
			if sc.offsetY != 600 || sc.offsetY != sc.scrollMaxOffset() {
				t.Fatalf("未滚到底: offsetY = %d, max = %d", sc.offsetY, sc.scrollMaxOffset())
			}
			rows := findAll(root, "rect")
			if len(rows) != 20 {
				t.Fatalf("行数 = %d, want 20", len(rows))
			}
			if rows[0].Box.Y != sc.Box.Y-600 {
				t.Fatalf("首行未随偏移上移: y=%d, want %d", rows[0].Box.Y, sc.Box.Y-600)
			}
			// 第 0 行整行已在视口上沿之外 → 那个位置不该再命中它
			if got := HitTestDeep(root, sc.Box.X+4, sc.Box.Y+4); got == rows[0] {
				t.Fatalf("已滚出视口的行不该被命中")
			}
			// 底部边界: 这一格不该被消费, 应冒泡给脚本 onWheel
			wheel(fake, sc.Box.X+8, sc.Box.Y+8, 1, 1)
		},
		// 3) 脚本收到那次冒泡; 再上滚 10 格正好回到顶部
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirst(root, "scroll")
			if sc.offsetY != 600 {
				t.Fatalf("底部越界: offsetY = %d", sc.offsetY)
			}
			if !textContainsAny(root, "overscroll events = 1") {
				t.Fatalf("边界上的滚轮未冒泡到脚本 onWheel")
			}
			wheel(fake, sc.Box.X+8, sc.Box.Y+8, 10, -1)
		},
		// 4) 回到顶部并钳位: 这一格同样冒泡
		func(root *GuiNode, fake *fakeSurface) {
			sc := findFirst(root, "scroll")
			if sc.offsetY != 0 {
				t.Fatalf("未回到顶部: offsetY = %d", sc.offsetY)
			}
			wheel(fake, sc.Box.X+8, sc.Box.Y+8, 10, -1)
		},
		// 5) 顶部溢出的 10 格也逐个冒泡
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "overscroll events = 11") {
				t.Fatalf("顶部溢出未冒泡 (1 格到底 + 10 格到顶)")
			}
		},
	})
}
