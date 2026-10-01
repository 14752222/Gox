package gfx

import (
	"fmt"
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

// ===== tag =====

func TestTagDefaultAndCustomColor(t *testing.T) {
	root := mkNode("row", map[string]float64{"padding": 10})
	def := mkNode("tag", nil)
	mountChildren(def, mkTextNode("默认"))
	custom := mkNode("tag", nil)
	withStr(custom, "color", "#3355aa")
	mountChildren(custom, mkTextNode("加粗"))
	mountChildren(root, def, custom)

	img := renderTree(root, 300, 100)

	// 诊断 (临时): 打印实际 Box 与左上角像素网格, 用于定位跨平台差异。
	t.Logf("DIAG theme=%s colorTrack=%v def.Box=%+v custom.Box=%+v",
		ThemeName(), colorTrack, def.Box, custom.Box)
	for dy := -1; dy <= 3; dy++ {
		row := ""
		for dx := -1; dx <= 6; dx++ {
			c := img.RGBAAt(def.Box.X+dx, def.Box.Y+dy)
			row += fmt.Sprintf("(%d,%d)=%d,%d,%d ", dx, dy, c.R, c.G, c.B)
		}
		t.Logf("DIAG row %+d: %s", dy, row)
	}

	// 缺省: 浅灰底 (取左上角内侧一点, 避开居中的文字)
	assertPx(t, img, def.Box.X+2, def.Box.Y+2, pxTrack, "tag 缺省浅灰底")
	// 自定义色: 圆角块的**中段实心**处 (避开圆角 AA 与居中文字, 取靠左 6px)
	assertPx(t, img, custom.Box.X+6, custom.Box.Y+2,
		color.RGBA{R: 0x33, G: 0x55, B: 0xAA, A: 255}, "tag 自定义色")
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
