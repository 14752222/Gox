package gfx

import (
	"image"
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4/T09 展示类五件套: alert / badge / tag / avatar / empty =====
//
// 断言口径与其它组件测试一致: 几何 (布局框) + 像素 (画了什么) + 交互
// (点到谁、派发什么)。字体渲染受平台影响, 凡验"有东西"处用 hasInkIn /
// hasColorNear, 不断言具体字形。
// (mkNode / withStr / withNum / withBool / mountChildren / renderTree /
//  mountTestApp / pushAndPump / assertPx 见 helpers_test.go。)

// mkTextNode 造一个 #text 节点 (由父容器绘制)。
func mkTextNode(s string) *GuiNode {
	return &GuiNode{Tag: "#text", Text: s}
}

// channelDist 是两色的逐通道最大差 (Chebyshev 距离)。给"这个像素更靠近
// 两个候选色里的哪一个"做归属判定用 —— 比欧氏距离便宜, 且对"底色 vs 文字
// 色"这种通道同向拉开的配对足够区分。
func channelDist(a, b color.RGBA) int {
	d := func(x, y uint8) int {
		if x > y {
			return int(x) - int(y)
		}
		return int(y) - int(x)
	}
	m := d(a.R, b.R)
	if v := d(a.G, b.G); v > m {
		m = v
	}
	if v := d(a.B, b.B); v > m {
		m = v
	}
	return m
}

// newTestHandler 造一个记录调用的 Go 侧处理器。
func newTestHandler(name string, fn func()) object.Value {
	return object.NewBuiltin(name, func(args ...object.Value) object.Value {
		fn()
		return object.UndefinedSingleton
	})
}

// hasColorNear 报告 (x,y) 邻域有没有指定颜色的像素。
func hasColorNear(img *image.RGBA, x, y int, c color.RGBA, radius int) bool {
	for dy := -radius; dy <= radius; dy++ {
		for dx := -radius; dx <= radius; dx++ {
			px, py := x+dx, y+dy
			if !(image.Point{px, py}.In(img.Bounds())) {
				continue
			}
			if img.RGBAAt(px, py) == c {
				return true
			}
		}
	}
	return false
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// mkMountWrap 把节点包进一个 column 便于 renderTree 布局。
func mkMountWrap(n *GuiNode) *GuiNode {
	root := mkNode("column", nil)
	mountChildren(root, n)
	return root
}

// 测试用色板 (直连源码常量, 与 helpers_test.go 的 px* 同源)。
var (
	pxWarnC   = colorWarn
	pxDangerC = colorDanger
	pxInfoC   = colorInfo
)

// ===== alert =====

func TestAlertLevelsPaintAccentStrip(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	ok := mkNode("alert", nil)
	withStr(ok, "level", "success")
	mountChildren(ok, mkTextNode("保存成功"))
	warn := mkNode("alert", nil)
	withStr(warn, "level", "warn")
	mountChildren(warn, mkTextNode("磁盘空间不足"))
	errA := mkNode("alert", nil)
	withStr(errA, "level", "error")
	mountChildren(errA, mkTextNode("失败"))
	info := mkNode("alert", nil) // 缺省 info
	mountChildren(info, mkTextNode("提示"))
	mountChildren(root, ok, warn, errA, info)

	img := renderTree(root, 500, 320)

	assertPx(t, img, ok.Box.X+1, ok.Box.Y+ok.Box.H/2, pxAccent, "success 色条")
	assertPx(t, img, warn.Box.X+1, warn.Box.Y+warn.Box.H/2, pxWarnC, "warn 色条")
	assertPx(t, img, errA.Box.X+1, errA.Box.Y+errA.Box.H/2, pxDangerC, "error 色条")
	assertPx(t, img, info.Box.X+1, info.Box.Y+info.Box.H/2, pxInfoC, "info 色条")
	if !hasInkIn(img, Rect{ok.Box.X + 30, ok.Box.Y, ok.Box.W - 30, ok.Box.H}) {
		t.Fatalf("alert 未绘制文本")
	}
}

func TestAlertClosableDispatchesOnClose(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	al := mkNode("alert", nil)
	withBool(al, "closable", true)
	mountChildren(al, mkTextNode("可关闭的提示"))
	calls := 0
	al.Props["onClose"] = newTestHandler("onClose", func() { calls++ })
	mountChildren(root, al)

	fake, a := mountTestApp(t, root, 400, 200)

	r := al.alertCloseRect()
	if r.W == 0 {
		t.Fatalf("closable 的 alert 应有关闭叉命中矩形")
	}
	if !al.Box.Contains(r.X+r.W/2, r.Y+r.H/2) {
		t.Fatalf("关闭叉应落在 alert 盒内: close=%v box=%v", r, al.Box)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: r.X + r.W/2, Y: r.Y + r.H/2})
	if calls != 1 {
		t.Fatalf("点关闭叉应派发一次 onClose, got %d", calls)
	}
	// 点文本区不派发
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: al.Box.X + 30, Y: al.Box.Y + al.Box.H/2})
	if calls != 1 {
		t.Fatalf("点文本区不该派发 onClose, got %d", calls)
	}
}

func TestAlertNotClosableHasNoCloseRect(t *testing.T) {
	al := mkNode("alert", nil)
	mountChildren(al, mkTextNode("普通提示"))
	renderTree(mkMountWrap(al), 400, 100)
	if r := al.alertCloseRect(); r.W != 0 {
		t.Fatalf("不可关闭的 alert 不该有关闭叉, got %v", r)
	}
}

// ===== badge =====

func TestBadgeTransparentAndPaintsCount(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 20})
	btn := withClick(mkNode("button", map[string]float64{"width": 90, "height": 30}))
	bd := mkNode("badge", map[string]float64{"count": 5})
	mountChildren(bd, btn)
	mountChildren(root, bd)

	img := renderTree(root, 300, 160)

	if bd.Box != btn.Box {
		t.Fatalf("badge 未跟随子节点: badge=%v btn=%v", bd.Box, btn.Box)
	}
	ax, ay := bd.badgeAnchor()
	if !hasColorNear(img, ax-6, ay, pxDangerC, 14) {
		t.Fatalf("未在右上角找到徽标色 (锚点 %d,%d)", ax, ay)
	}
	// 徽标是"锚点在右上角、向左展开"的胶囊: 锚点允许在盒内, 但绘制内容
	// 会越过宿主上缘 (ay 在盒内的上部), 装饰不占流内空间。
	if ay >= btn.Box.Y+btn.Box.H {
		t.Fatalf("徽标锚点应贴在宿主上部: ay=%d 宿主=%v", ay, btn.Box)
	}
}

func TestBadgeDotAndMax(t *testing.T) {
	root := mkNode("row", nil)
	d1 := mkNode("badge", map[string]float64{"count": 128})
	mountChildren(d1, mkNode("button", map[string]float64{"width": 60, "height": 28}))
	d2 := mkNode("badge", nil)
	withBool(d2, "dot", true)
	mountChildren(d2, mkNode("button", map[string]float64{"width": 60, "height": 28}))
	mountChildren(root, d1, d2)
	renderTree(root, 300, 120)

	if got := d1.badgeText(); got != "99+" {
		t.Fatalf("超上限的徽标文本 = %q, want %q", got, "99+")
	}
	if d2.badgeText() != "" {
		t.Fatalf("dot 形态不该有数字文本, got %q", d2.badgeText())
	}
	if !d2.badgeVisible() {
		t.Fatalf("dot 形态应可见")
	}
	d0 := mkNode("badge", map[string]float64{"count": 0})
	mountChildren(d0, mkNode("button", map[string]float64{"width": 40, "height": 24}))
	if d0.badgeVisible() {
		t.Fatalf("count=0 不该显示徽标")
	}
}

// 徽标本体的两种写法 (包裹式 / 挂在宿主上) 落成同一个画面。
func TestBadgeOnHostPropMatchesWrapper(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 30, "gap": 40})
	// 包裹式
	wrap := mkNode("badge", map[string]float64{"count": 7})
	wBtn := mkButton("消息")
	withNum(wBtn, "width", 90)
	withNum(wBtn, "height", 36)
	mountChildren(wrap, wBtn)
	// 挂在宿主上
	host := mkButton("消息")
	withNum(host, "width", 90)
	withNum(host, "height", 36)
	withNum(host, "badge", 7)
	mountChildren(root, wrap, host)

	img := renderTree(root, 300, 200)

	// 两枚徽标相对各自宿主的位置一致 (两颗按钮是竖排的, 绝对坐标当然不同)
	wx, wy := wrap.badgeAnchor()
	hx, hy := host.badgeAnchor()
	if wx-wrap.Box.X != hx-host.Box.X || wy-wrap.Box.Y != hy-host.Box.Y {
		t.Fatalf("两种写法的徽标相对宿主的位置不一致: wrap=(%d,%d) host=(%d,%d)",
			wx-wrap.Box.X, wy-wrap.Box.Y, hx-host.Box.X, hy-host.Box.Y)
	}
	for _, c := range []*GuiNode{wrap, host} {
		ax, ay := c.badgeAnchor()
		if !hasColorNear(img, ax-8, ay+8, pxDangerC, 6) {
			t.Fatalf("徽标未画在宿主右上角外 (锚点 %d,%d)", ax, ay)
		}
		// 徽标越过宿主上缘 (否则就是贴在按钮里, 没有"出框"的角标感)
		if ay >= c.Box.Y {
			t.Fatalf("徽标未溢出宿主上缘: ay=%d 宿主 Y=%d", ay, c.Box.Y)
		}
	}
}

// 抠边环: 徽标压在宿主上时, 环把宿主的边线"挖"出一圈缝。
func TestBadgeKnockoutRingSeparatesFromHost(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 30})
	host := mkButton("消息")
	withNum(host, "width", 90)
	withNum(host, "height", 36)
	withNum(host, "badge", 3)
	mountChildren(root, host)

	img := renderTree(root, 200, 140)
	ax, ay := host.badgeAnchor()
	// 徽标正上方的那一格 (徽标顶 + 环) 应是页底色, 而不是徽标色
	top := ay - badgeRingW/2
	if got := img.RGBAAt(ax-8, top); got == pxDangerC {
		t.Fatalf("徽标上方没有抠边环 (直接顶到了徽标色)")
	}
	if !hasColorNear(img, ax-8, top-1, colorSurfaceBase, 2) {
		t.Fatalf("徽标外圈未画页底色的抠边环")
	}
}

// badgeDot 与 max 也走宿主上的写法。
func TestBadgeHostDotAndMax(t *testing.T) {
	dot := mkButton("铃")
	withNum(dot, "width", 60)
	withNum(dot, "height", 28)
	withBool(dot, "badgeDot", true)
	max := mkButton("通知")
	withNum(max, "width", 60)
	withNum(max, "height", 28)
	withNum(max, "badge", 128)
	withNum(max, "max", 99)
	mountChildren(mkNode("row", map[string]float64{"padding": 20}), dot, max)

	if got := dot.badgeSpec().text(); got != "" {
		t.Fatalf("badgeDot 形态不该有数字: %q", got)
	}
	if !dot.badgeSpec().visible() {
		t.Fatalf("badgeDot 形态应可见")
	}
	if got := max.badgeSpec().text(); got != "99+" {
		t.Fatalf("超上限徽标 = %q, want %q", got, "99+")
	}
	// 徽标文字色是按对比度挑的, 且两个候选里必有一个达标
	ink := badgeInkColor(colorDanger)
	if ContrastOn(ink, colorDanger) < ContrastLargeTextMin {
		t.Fatalf("徽标文字色 %v 在底 %v 上的对比度 %f, 未过 %f",
			ink, colorDanger, ContrastOn(ink, colorDanger), ContrastLargeTextMin)
	}
}

// ===== tag =====

func TestTagDefaultAndCustomColor(t *testing.T) {
	root := mkNode("row", map[string]float64{"padding": 10})
	def := mkNode("tag", nil)
	mountChildren(def, mkTextNode("默认"))
	custom := mkNode("tag", nil)
	// S7 起 background 是底、color 是字 (与其它组件同口径; v1 把 color
	// 当底色用, 于是 <tag color=…> 出来的是"绿底绿字")。
	withStr(custom, "background", "#3355aa")
	mountChildren(custom, mkTextNode("加粗"))
	mountChildren(root, def, custom)

	img := renderTree(root, 300, 100)

	// 取样点取**左缘竖直中点**而不是左上角内侧:
	//
	// 1. 圆角 RadiusSM(6) 的角块在 (+1,+1) 上只有几分之一覆盖, 在那里断言
	//    纯底色必然撞进 AA 带 (实测 {253,253,254});
	// 2. 竖直中点永远落在直段上, 与盒子高度、圆角档位都无关 —— 不像 (+1,+1)
	//    那样还要赌"字形顶到哪一行"。2026-10-01 macos ci 上 (+2,+2) 变红的
	//    根因就是字形度量把文字顶高了, 这个取样点对那类差异免疫。
	assertPx(t, img, def.Box.X+1, def.Box.Y+def.Box.H/2, colorTrack, "tag 缺省中性底")
	assertPx(t, img, custom.Box.X+1, custom.Box.Y+custom.Box.H/2,
		color.RGBA{R: 0x33, G: 0x55, B: 0xAA, A: 255}, "tag 自定义底")
}

// tag 的几何: 高 22 左右、圆角 RadiusSM(6)。
func TestTagGeometry(t *testing.T) {
	root := mkNode("row", map[string]float64{"padding": 10})
	tg := mkNode("tag", nil)
	mountChildren(tg, mkTextNode("默认"))
	mountChildren(root, tg)
	renderTree(root, 300, 100)

	// 默认字号 16 → 行高 20, 上下各 1 的内边距 → 22。
	if tg.Box.H != 22 {
		t.Fatalf("tag 高 = %d, want 22", tg.Box.H)
	}
	// 圆角 6: 左上角那一格应当在圆外 (盒内左上角 (0,0) 不落底色)
	img := renderTree(root, 300, 100)
	if img.RGBAAt(tg.Box.X, tg.Box.Y) != pxWhite {
		t.Fatalf("tag 左上角应有圆角 (RadiusSM), 却落了底色")
	}
}

// variant 走 tonal 三件套 (与 S6 的 alert 同一套语言), 且文字色跟随。
func TestTagVariantIsTonal(t *testing.T) {
	cases := []struct {
		variant string
		face    color.RGBA
		ink     color.RGBA
	}{
		{"success", colorSuccessSubtle, colorSuccessText},
		{"info", colorInfoSubtle, colorInfoText},
		{"warning", colorWarningSubtle, colorWarningText},
		{"danger", colorDangerSubtle, colorDangerText},
	}
	for _, tc := range cases {
		root := mkNode("row", map[string]float64{"padding": 10})
		tg := mkNode("tag", nil)
		withStr(tg, "variant", tc.variant)
		mountChildren(tg, mkTextNode("标签"))
		mountChildren(root, tg)
		img := renderTree(root, 300, 100)
		assertPx(t, img, tg.Box.X+1, tg.Box.Y+tg.Box.H/2, tc.face, "variant="+tc.variant+" 的底")
		// 文字色由 textColor 的 tag 分支给出 (子节点 #text 问继承链得到它)
		if got := tagTextColor(tg); got != tc.ink {
			t.Fatalf("variant=%s 的 tag 文字色 = %v, want %v", tc.variant, got, tc.ink)
		}
		// 扫整个盒内而不是中心 ±12px: 中文标签的字形宽度随平台字体变
		// (macOS PingFang 比 Linux 的宽), 中心采样窗在 macOS 上抓不到文字。
		// 盒内找"站在文字色一边"的像素。不能找精确 ink: macOS 上小字号汉字
		// 的笔画全是灰度平滑的混合色 (实测最深 {59 140 94}, 纯 ink 一个都没有),
		// 而 Linux 的渲染能给出 α=1 的主干像素。用逐通道距离判定色相归属 —
		// 更接近 ink 而非 face 即证明同族文字色真的落了地。
		inner := Rect{X: tg.Box.X + 2, Y: tg.Box.Y + 2, W: tg.Box.W - 4, H: tg.Box.H - 4}
		inkSide := 0
		for y := inner.Y; y < inner.Y+inner.H; y++ {
			for x := inner.X; x < inner.X+inner.W; x++ {
				c := img.RGBAAt(x, y)
				if channelDist(c, tc.ink) < channelDist(c, tc.face) {
					inkSide++
				}
			}
		}
		if inkSide == 0 {
			t.Fatalf("variant=%s 的 tag 内没有偏向文字色 %v 的像素 (底 %v)",
				tc.variant, tc.ink, tc.face)
		}
	}
}

func TestTagClosableDispatches(t *testing.T) {
	root := mkNode("row", map[string]float64{"padding": 10})
	tg := mkNode("tag", nil)
	withBool(tg, "closable", true)
	mountChildren(tg, mkTextNode("可关闭"))
	calls := 0
	tg.Props["onClose"] = newTestHandler("onClose", func() { calls++ })
	mountChildren(root, tg)

	fake, a := mountTestApp(t, root, 300, 100)
	r := tg.tagCloseRect()
	if r.W == 0 {
		t.Fatalf("closable 的 tag 应有关闭叉")
	}
	if r.X < tg.Box.X {
		t.Fatalf("关闭叉越出 tag 左缘: %v", r)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: r.X + r.W/2, Y: r.Y + r.H/2})
	if calls != 1 {
		t.Fatalf("点 tag 关闭叉应派发 onClose, got %d", calls)
	}
}

// ===== avatar =====

func TestAvatarSizeShapeAndInitial(t *testing.T) {
	root := mkNode("row", map[string]float64{"padding": 10})
	a1 := mkNode("avatar", map[string]float64{"size": 40})
	mountChildren(a1, mkTextNode("alice"))
	a2 := mkNode("avatar", map[string]float64{"size": 40})
	withStr(a2, "shape", "square")
	mountChildren(a2, mkTextNode("bob"))
	mountChildren(root, a1, a2)

	img := renderTree(root, 300, 120)

	if a1.Box.W != 40 || a1.Box.H != 40 {
		t.Fatalf("avatar 尺寸 = %dx%d, want 40x40", a1.Box.W, a1.Box.H)
	}
	if got := a1.avatarInitial(); got != "A" {
		t.Fatalf("avatar 首字母 = %q, want %q", got, "A")
	}
	// 圆形: 中心有底色, 左上角外留白
	if img.RGBAAt(a1.Box.X+a1.Box.W/2, a1.Box.Y+a1.Box.H/2) == pxWhite {
		t.Fatalf("圆形头像中心未绘制底色")
	}
	if img.RGBAAt(a1.Box.X+1, a1.Box.Y+1) != pxWhite {
		t.Fatalf("圆形头像左上角外应留白 (圆角)")
	}
	// 方形: 左上角有底色
	if img.RGBAAt(a2.Box.X+3, a2.Box.Y+3) == pxWhite {
		t.Fatalf("方形头像左上角应绘制底色")
	}
}

// ===== empty =====

func TestEmptyDescAndChild(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	em := mkNode("empty", nil)
	withStr(em, "desc", "还没有数据")
	btn := mkNode("button", map[string]float64{"width": 80, "height": 28})
	mountChildren(em, btn)
	mountChildren(root, em)

	img := renderTree(root, 360, 260)

	if em.emptyDesc() != "还没有数据" {
		t.Fatalf("empty desc 读取失败: %q", em.emptyDesc())
	}
	if !hasInkIn(img, Rect{em.Box.X, em.Box.Y, em.Box.W, emptyIconSize}) {
		t.Fatalf("empty 未绘制图示")
	}
	if btn.Box.Y <= em.Box.Y+emptyIconSize {
		t.Fatalf("empty 的子节点应排在图示下方: btn.Y=%d", btn.Box.Y)
	}
	cx := em.Box.X + em.Box.W/2
	if absDiff(btn.Box.X+btn.Box.W/2, cx) > 1 {
		t.Fatalf("empty 的子节点应水平居中: btn 中心=%d 容器中心=%d", btn.Box.X+btn.Box.W/2, cx)
	}
}

func TestEmptyGraphicChildReplacesBuiltinGlyph(t *testing.T) {
	// 插图子节点 (<icon>) 要落在**描述之上**, 而不是被排进操作区;
	// 且有了它就不该再画内置简笔箱 (两个图形摞一起是互相抢戏)。
	root := mkNode("column", map[string]float64{"padding": 8})
	em := mkNode("empty", nil)
	withStr(em, "desc", "还没有上传文件")
	ic := mkNode("icon", map[string]float64{"size": 40})
	withStr(ic, "name", "folder")
	// 纯蓝: 它与白底的抗锯齿混色 B 通道恒为 255, 不可能恰好等于
	// colorTrack (#D0D0D0) —— 于是"插图带里出现 #D0D0D0"就只可能是
	// 内置简笔箱被重复画了, 不会有假阳性。
	withStr(ic, "color", "#0000ff")
	btn := mkNode("button", map[string]float64{"width": 80, "height": 28})
	mountChildren(em, ic, btn)
	mountChildren(root, em)

	img := renderTree(root, 360, 300)

	if ic.Box.Y != em.Box.Y+Space(Space6) {
		t.Fatalf("插图应贴在内容区顶部: icon.Y=%d, want %d", ic.Box.Y, em.Box.Y+Space(Space6))
	}
	if btn.Box.Y <= ic.Box.Y+ic.Box.H {
		t.Fatalf("操作按钮应排在插图下方: btn.Y=%d, 插图底=%d", btn.Box.Y, ic.Box.Y+ic.Box.H)
	}
	// 插图带 (内容区顶部起的 44 行, 含插图两侧留白) 里不该出现简笔箱的墨
	band := Rect{X: em.Box.X, Y: em.Box.Y + Space(Space6), W: em.Box.W, H: 44}
	if n := countColor(img, band, colorTrack); n != 0 {
		t.Fatalf("有 <icon> 插图时仍画了内置简笔箱: 插图带里 %d 个 #D0D0D0 像素", n)
	}
	// 固有高度 = 上下内边距 + 插图 + 间距 + 文字 + 间距 + 按钮:
	// 40 的插图替代 44 的内置箱, 且插图不被重复计入操作区。
	_, th := MeasureText("还没有上传文件", em.FontSize())
	wantH := 2*Space(Space6) + ic.Box.H + emptyGap + th + emptyGap + btn.Box.H
	if em.Box.H != wantH {
		t.Fatalf("empty 高 = %d, want %d", em.Box.H, wantH)
	}
}

func TestEmptyDefaultPaddingIsSpace6(t *testing.T) {
	// 同样的内容, 默认盒子应该比 padding={0} 的盒子四周各大 Space6。
	build := func(pad float64) *GuiNode {
		root := mkNode("column", map[string]float64{"padding": 6})
		em := mkNode("empty", nil)
		withStr(em, "desc", "暂无数据")
		if pad >= 0 {
			withNum(em, "padding", pad)
		}
		mountChildren(root, em)
		renderTree(root, 400, 300)
		return em
	}
	plain := build(-1)
	zero := build(0)
	if got := plain.Box.H - zero.Box.H; got != 2*Space(Space6) {
		t.Fatalf("默认内边距 = %d, want 2*Space6=%d", got, 2*Space(Space6))
	}
	// 显式 padding 仍然完全由脚本说了算 (不叠加默认的 16)
	twelve := build(12)
	if got := twelve.Box.H - zero.Box.H; got != 0 {
		t.Fatalf("显式 padding 不该与默认值叠加: 12 → 高差 %d", got)
	}
}

func TestEmptyCentersVerticallyInTallBox(t *testing.T) {
	// 盒子被拉高 (卡片里的一整块) 时, 内容整体垂直居中而不是粘在上边。
	root := mkNode("column", map[string]float64{"padding": 4})
	em := mkNode("empty", map[string]float64{"height": 200})
	withStr(em, "desc", "搜索无结果")
	mountChildren(root, em)
	img := renderTree(root, 400, 300)

	area := inner(em)
	wantTop := area.Y + (area.H-em.emptyContentH())/2
	// 内容顶 = 简笔箱顶; 用"第一处有墨的行"验它落在居中位置 (±2 容差,
	// 抗锯齿会把边线抹开一两行)。
	top := -1
	for y := area.Y; y < area.Y+area.H && top < 0; y++ {
		if hasInkIn(img, Rect{X: area.X, Y: y, W: area.W, H: 1}) {
			top = y
		}
	}
	if top < 0 {
		t.Fatal("empty 在高盒子里没画出任何内容")
	}
	if absDiff(top, wantTop) > 2 {
		t.Fatalf("内容未垂直居中: 首墨行=%d, want %d", top, wantTop)
	}
}

func TestEmptySubDescIsMuted(t *testing.T) {
	// 主说明 secondary / 副说明 muted: 两级灰, 同色会被读成"一句话折行"。
	root := mkNode("column", map[string]float64{"padding": 8})
	em := mkNode("empty", nil)
	withStr(em, "desc", "还没有数据")
	withStr(em, "subDesc", "换个筛选条件试试")
	mountChildren(root, em)
	img := renderTree(root, 500, 300)

	size := em.FontSize()
	_, th := MeasureText("还没有数据", size)
	subY := em.Box.Y + Space(Space6) + emptyIconSize + emptyGap + th + emptySubGap
	if !hasInkIn(img, Rect{X: em.Box.X, Y: subY, W: em.Box.W, H: th}) {
		t.Fatalf("副说明未绘制 (y=%d)", subY)
	}
	// 副说明比主说明更淡: muted 的相对亮度高于 secondary (墨更浅)
	if relativeLuminance(colorTextMuted) <= relativeLuminance(colorTextSecondary) {
		t.Fatalf("副说明的 muted 应比主说明的 secondary 更淡: %f vs %f",
			relativeLuminance(colorTextMuted), relativeLuminance(colorTextSecondary))
	}
}

// ===== 闸门: 五件套都进树 =====

func TestFeedbackComponentsMountClean(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12, "gap": 8})
	al := mkNode("alert", nil)
	withStr(al, "level", "success")
	mountChildren(al, mkTextNode("ok"))
	tg := mkNode("tag", nil)
	mountChildren(tg, mkTextNode("tag"))
	av := mkNode("avatar", map[string]float64{"size": 32})
	mountChildren(av, mkTextNode("z"))
	em := mkNode("empty", nil)
	withStr(em, "desc", "空的")
	bd := mkNode("badge", map[string]float64{"count": 3})
	mountChildren(bd, mkNode("button", map[string]float64{"width": 50, "height": 24}))
	mountChildren(root, al, tg, av, em, bd)

	img := renderTree(root, 400, 400)
	if img == nil {
		t.Fatalf("渲染失败")
	}
	for _, tag := range []string{"alert", "tag", "avatar", "empty", "badge"} {
		if countTag(root, tag) != 1 {
			t.Fatalf("树里 %s 数量 != 1", tag)
		}
	}
}
