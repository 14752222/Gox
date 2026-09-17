package gfx

import (
	"image"
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== P2-2: 层叠 / 绝对定位 / 逃逸裁剪 / 半透明 =====
//
// 层叠的核心风险是"绘制顺序与命中顺序不一致" —— 看到的和点到的不在一层。
// 因此每个顺序类用例都同时断言两边: 像素归属 + HitTest 结果。

// withNum 给节点补一个数值属性 (mkNode 已支持, 这里用于挂载后再改)。
func withNum(n *GuiNode, name string, v float64) *GuiNode {
	n.Props[name] = object.NewNumber(v)
	return n
}

// mountChildren 把子节点挂到 root 上并设好 Parent。
func mountChildren(root *GuiNode, kids ...*GuiNode) *GuiNode {
	for _, c := range kids {
		c.Parent = root
		root.Children = append(root.Children, c)
	}
	return root
}

// 三个兄弟节点: 同位置同尺寸, 用不同颜色区分谁画在上面。
var (
	zRed   = "#c0392b"
	zGreen = "#27ae60"
	zBlue  = "#1a5fb4"
)

func TestZIndexDrawAndHitOrder(t *testing.T) {
	// 声明序: 红(0) 绿(5) 蓝(2) → 绘制顺序应是 红(0) → 蓝(2) → 绿(5)
	root := mkNode("column", nil)
	red := withClick(withStr(mkNode("rect", map[string]float64{"width": 60, "height": 40}), "background", zRed))
	green := withClick(withStr(mkNode("rect", map[string]float64{"width": 60, "height": 40}), "background", zGreen))
	blue := withClick(withStr(mkNode("rect", map[string]float64{"width": 60, "height": 40}), "background", zBlue))
	withNum(green, "zIndex", 5)
	withNum(blue, "zIndex", 2)
	// 三者叠在 column 的同一格: 用 absolute + left/top 归零
	for _, n := range []*GuiNode{red, green, blue} {
		withStr(n, "position", "absolute")
		withNum(n, "left", 0)
		withNum(n, "top", 0)
	}
	mountChildren(root, red, green, blue)

	img := renderTree(root, 200, 120)

	// zIndex 最大的绿色必须画在最上面 (盖住另外两个)
	assertPx(t, img, 30, 20, color.RGBA{R: 0x27, G: 0xAE, B: 0x60, A: 255}, "zIndex=5 应绘制在最上层")

	// 命中顺序与绘制顺序一致: 最上面的绿色先被命中
	if hit := HitTest(root, 30, 20); hit != green {
		t.Fatalf("HitTest 应命中 zIndex 最高的绿块, got %v", hit)
	}

	// 把绿色降到最低 → 蓝色成为最上层 (绘制与命中都要跟着变)
	withNum(green, "zIndex", -1)
	img = renderTree(root, 200, 120)
	assertPx(t, img, 30, 20, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "蓝块 zIndex=2 应成为最上层")
	if hit := HitTest(root, 30, 20); hit != blue {
		t.Fatalf("HitTest 应改为命中蓝块, got %v", hit)
	}

	// 同 zIndex 时保持声明序 (后声明在上): 让红蓝同为 2
	withNum(red, "zIndex", 2)
	img = renderTree(root, 200, 120)
	assertPx(t, img, 30, 20, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "同 zIndex 后声明者在上 (蓝)")
	if hit := HitTest(root, 30, 20); hit != blue {
		t.Fatalf("同 zIndex 应命中后声明的蓝块, got %v", hit)
	}
}

func TestAbsolutePositionAndSizing(t *testing.T) {
	// 外层用 row + alignItems:start, 让 column 按固有尺寸摆放 (Layout 会把
	// 画布尺寸直接给根节点, 所以不能在根上断言"容器没被撑大")。
	root := mkNode("row", nil)
	withStr(root, "alignItems", "start")
	col := mkNode("column", map[string]float64{"padding": 10, "gap": 6})
	// 流内子节点: 撑起容器尺寸
	flow := withStr(mkNode("rect", map[string]float64{"width": 80, "height": 30}), "background", zRed)
	// 绝对定位子节点: 不占位、不撑大容器, 相对父内容区定位
	abs := withStr(mkNode("rect", map[string]float64{"width": 40, "height": 16}), "background", zBlue)
	withStr(abs, "position", "absolute")
	withNum(abs, "left", 20)
	withNum(abs, "top", 40)
	mountChildren(col, flow, abs)
	mountChildren(root, col)

	Layout(root, 300, 200)

	// 容器尺寸只由流内子节点决定: 80+20 = 100 宽, 30+20 = 50 高
	if col.Box.W != 100 || col.Box.H != 50 {
		t.Fatalf("绝对定位子节点不应撑大容器: got %dx%d, want 100x50", col.Box.W, col.Box.H)
	}
	// 流内子节点在内容区原点
	if flow.Box.X != col.Box.X+10 || flow.Box.Y != col.Box.Y+10 {
		t.Fatalf("流内子节点位置错误: %+v (容器 %+v)", flow.Box, col.Box)
	}
	// 绝对定位: 内容区原点 + (20,40)
	if abs.Box.X != col.Box.X+10+20 || abs.Box.Y != col.Box.Y+10+40 {
		t.Fatalf("绝对定位坐标错误: got (%d,%d), want (%d,%d)",
			abs.Box.X, abs.Box.Y, col.Box.X+30, col.Box.Y+50)
	}
	if abs.Box.W != 40 || abs.Box.H != 16 {
		t.Fatalf("绝对定位尺寸应取自身固有尺寸: %+v", abs.Box)
	}
	// 绝对定位子节点不该影响主轴富余分配
	if flow.Box.H != 30 {
		t.Fatalf("流内子节点高度被绝对定位节点影响: %+v", flow.Box)
	}
}

func TestAbsoluteClippedByParent(t *testing.T) {
	// 父盒 100x20, 绝对定位子节点高 60 且 top=0 → 只有前 20px 可见
	root := mkNode("column", nil)
	parent := withStr(mkNode("rect", map[string]float64{"width": 100, "height": 20}), "background", zRed)
	overhang := withClick(withStr(mkNode("rect", map[string]float64{"width": 100, "height": 60}), "background", zBlue))
	withStr(overhang, "position", "absolute")
	withNum(overhang, "left", 0)
	withNum(overhang, "top", 0)
	mountChildren(parent, overhang)
	mountChildren(root, parent)

	img := renderTree(root, 200, 120)

	// 父盒内: 蓝块盖住父的红底
	assertPx(t, img, 50, 10, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "父盒内可见绝对定位子节点")
	// 父盒外: 被裁剪, 露出白底 (绝对定位默认不逃逸父盒)
	assertPx(t, img, 50, 40, pxWhite, "父盒外应被裁剪")
	// 命中测试同步: 父盒外的点不该命中被裁掉的区域
	if hit := HitTest(root, 50, 40); hit != nil {
		t.Fatalf("父盒外不应命中被裁剪的绝对定位子节点, got %v", hit)
	}
	if hit := HitTest(root, 50, 10); hit != overhang {
		t.Fatalf("父盒内应命中绝对定位子节点, got %v", hit)
	}
}

func TestEscapeClippingHoistedToRoot(t *testing.T) {
	root := mkNode("column", nil)
	parent := withStr(mkNode("rect", map[string]float64{"width": 100, "height": 20}), "background", zRed)
	popup := withClick(withStr(mkNode("rect", map[string]float64{"width": 100, "height": 60}), "background", zBlue))
	withStr(popup, "position", "absolute")
	withBool(popup, "escapeClipping", true)
	withNum(popup, "left", 0)
	withNum(popup, "top", 0)
	// 另一个后面的兄弟, 用来验证弹层确实画在它上面
	below := withStr(mkNode("rect", map[string]float64{"width": 100, "height": 80}), "background", zGreen)
	mountChildren(parent, popup)
	mountChildren(root, parent, below)

	img := renderTree(root, 200, 120)

	// 逃逸子树不被父盒裁剪: 溢出的部分照画
	assertPx(t, img, 50, 40, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "逃逸子树不应被父盒裁剪")
	// 且盖在后面的兄弟节点之上
	assertPx(t, img, 50, 10, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "逃逸子树应画在后续兄弟之上")
	// 命中同步: 溢出区域可命中
	if hit := HitTest(root, 50, 40); hit != popup {
		t.Fatalf("逃逸区域应可命中, got %v", hit)
	}
}

func TestEscapeClippingStackingOrder(t *testing.T) {
	// 两个逃逸子树: 后声明的压在前声明的上面
	root := mkNode("column", nil)
	first := withClick(withStr(mkNode("rect", map[string]float64{"width": 60, "height": 40}), "background", zRed))
	second := withClick(withStr(mkNode("rect", map[string]float64{"width": 60, "height": 40}), "background", zBlue))
	for _, n := range []*GuiNode{first, second} {
		withStr(n, "position", "absolute")
		withBool(n, "escapeClipping", true)
		withNum(n, "left", 0)
		withNum(n, "top", 0)
	}
	mountChildren(root, first, second)

	img := renderTree(root, 100, 60)
	assertPx(t, img, 30, 20, color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}, "后声明的逃逸子树在上")
	if hit := HitTest(root, 30, 20); hit != second {
		t.Fatalf("应命中后声明的逃逸子树, got %v", hit)
	}

	// 给先声明的更大 zIndex → 反超
	withNum(first, "zIndex", 9)
	img = renderTree(root, 100, 60)
	assertPx(t, img, 30, 20, color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255}, "zIndex 更大的逃逸子树在上")
	if hit := HitTest(root, 30, 20); hit != first {
		t.Fatalf("应命中 zIndex 更大的逃逸子树, got %v", hit)
	}
}

func TestZOrderDoesNotMutateChildren(t *testing.T) {
	// 排序只影响遍历顺序, Children 数组本身必须保持声明序
	root := mkNode("column", nil)
	a := withNum(mkNode("rect", nil), "zIndex", 5)
	b := withNum(mkNode("rect", nil), "zIndex", 1)
	c := mkNode("rect", nil)
	mountChildren(root, a, b, c)

	kids := zOrderedChildren(root)
	if len(kids) != 3 || kids[0] != c || kids[1] != b || kids[2] != a {
		t.Fatalf("zOrderedChildren 顺序错误 (应为 c,b,a): %v", kids)
	}
	if root.Children[0] != a || root.Children[1] != b || root.Children[2] != c {
		t.Fatalf("Children 数组被排序改动: %v", root.Children)
	}

	// 全部 zIndex 为 0 时不应产生新切片 (零分配快路径)
	flat := mkNode("column", nil)
	mountChildren(flat, mkNode("rect", nil), mkNode("rect", nil))
	if got := zOrderedChildren(flat); &got[0] != &flat.Children[0] {
		t.Fatalf("无 zIndex 时应复用原切片")
	}
}

// ===== 半透明: ParseColor 新色值 + FillRect 真混合 =====

func TestParseColorAlpha(t *testing.T) {
	cases := []struct {
		in   string
		want color.RGBA
	}{
		{"#c0392b", color.RGBA{0xC0, 0x39, 0x2B, 255}},
		{"#f00", color.RGBA{255, 0, 0, 255}},
		{"#c0392b80", color.RGBA{0xC0, 0x39, 0x2B, 0x80}},
		{"#f008", color.RGBA{255, 0, 0, 0x88}},
		{"#f000", color.RGBA{255, 0, 0, 0}},
		{"rgb(192,57,43)", color.RGBA{0xC0, 0x39, 0x2B, 255}},
		{"rgba(192,57,43,128)", color.RGBA{0xC0, 0x39, 0x2B, 128}},
		{"rgba(192,57,43,0.5)", color.RGBA{0xC0, 0x39, 0x2B, 128}},
		{"rgba(0, 0, 0, 0)", color.RGBA{0, 0, 0, 0}},
		{"RGBA(0,0,0,0.4)", color.RGBA{0, 0, 0, 102}},
		{" rgba( 255 , 255 , 255 , 1 ) ", color.RGBA{255, 255, 255, 255}},
		{"#1a5fb4", color.RGBA{0x1A, 0x5F, 0xB4, 255}},
		{"red", namedColors["red"]},
	}
	for _, c := range cases {
		got, ok := ParseColor(c.in)
		if !ok {
			t.Errorf("ParseColor(%q) 解析失败", c.in)
			continue
		}
		if got != c.want {
			t.Errorf("ParseColor(%q) = %v, want %v", c.in, got, c.want)
		}
	}

	for _, bad := range []string{"", "#", "#gg0000", "#12345", "rgb(1,2)", "rgb(1,2,3,4,5)", "rgba(a,b,c,d)", "#ff00ff00ff"} {
		if got, ok := ParseColor(bad); ok {
			t.Errorf("ParseColor(%q) 应失败, got %v", bad, got)
		}
	}
}

func TestFillRectAlphaBlend(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	// 底色: 纯白
	FillRect(img, Rect{0, 0, 10, 10}, color.RGBA{255, 255, 255, 255})

	// 50% 黑盖上去: 预乘合成后应是中性灰 (127 = 255*127/255)
	FillRect(img, Rect{2, 2, 4, 4}, color.RGBA{0, 0, 0, 128})
	if got := img.RGBAAt(3, 3); got != (color.RGBA{127, 127, 127, 255}) {
		t.Fatalf("半透明覆盖应透出底色: got %v, want {127 127 127 255}", got)
	}
	// 未覆盖区域不受影响
	assertPx(t, img, 0, 0, pxWhite, "覆盖区域外应保持底色")

	// 全透明不该改动任何像素
	before := img.RGBAAt(5, 5)
	FillRect(img, Rect{0, 0, 10, 10}, color.RGBA{255, 0, 0, 0})
	if got := img.RGBAAt(5, 5); got != before {
		t.Fatalf("全透明色不该改动画面: got %v, want %v", got, before)
	}
}

func TestFillRectOpaqueStillOverwrites(t *testing.T) {
	// 不透明路径必须保持"覆盖"语义 (旧行为不能因引入混合而改变)
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	FillRect(img, Rect{0, 0, 4, 4}, color.RGBA{0, 0, 255, 255})
	FillRect(img, Rect{1, 1, 2, 2}, color.RGBA{255, 0, 0, 255})
	assertPx(t, img, 2, 2, color.RGBA{255, 0, 0, 255}, "不透明色应完全覆盖")
}

func TestParseColorAlphaInNodeProps(t *testing.T) {
	// 端到端: 节点上写 rgba() 背景色, 绘制时真的半透明
	root := mkNode("column", nil)
	back := withStr(mkNode("rect", map[string]float64{"width": 40, "height": 40}), "background", "#1a5fb4")
	front := withStr(mkNode("rect", map[string]float64{"width": 40, "height": 40}), "background", "rgba(0,0,0,0.5)")
	withStr(front, "position", "absolute")
	withNum(front, "left", 0)
	withNum(front, "top", 0)
	mountChildren(root, back, front)

	img := renderTree(root, 60, 60)
	// 蓝 #1a5fb4 与 50% 黑混合 → 各通道减半
	// alpha=128 → inv=127; 各通道 = base*127/255 整数截断
	want := color.RGBA{0x0C, 0x2F, 0x59, 255}
	assertPx(t, img, 20, 20, want, "rgba() 背景应半透明混合")
}
