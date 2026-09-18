package gfx

import (
	_ "embed"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"
)

// ===== 图片组件 (P2-9) =====

// testPNG2x2 是嵌入的测试图: 左上红 / 右上绿 / 左下蓝 / 右下白。
// 四角异色是为了让"缩放后哪个角变成哪个色"有唯一答案 —— 单色图只能验"画上
// 东西了", 验不出采样错位。
//
//go:embed testdata/test_2x2.png
var testPNG2x2 []byte

// writeTestImage 把嵌入的测试图落到临时文件并返回**绝对路径**。
// 必须落盘: `src` 的语义就是文件路径, 没有"内置图片"这条路径可走。
func writeTestImage(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, testPNG2x2, 0o644); err != nil {
		t.Fatalf("写测试图片: %v", err)
	}
	return p
}

// mkImage 造一个 image 节点 (src 是字符串属性, mkNode 只收数值, 得另外补)。
func mkImage(src string, props map[string]float64) *GuiNode {
	return withStr(mkNode("image", props), "src", src)
}

func mustLoadImage(t *testing.T, path string) *imageEntry {
	t.Helper()
	e, err := loadImage(path)
	if err != nil {
		t.Fatalf("加载测试图片 %s: %v", path, err)
	}
	return e
}

// TestImageIntrinsicNatural 未给 width/height 时用图片自然尺寸。
func TestImageIntrinsicNatural(t *testing.T) {
	p := writeTestImage(t, "a.png")
	n := mkImage(p, nil)
	if w, h := n.intrinsicSize(); w != 2 || h != 2 {
		t.Fatalf("自然尺寸 = %dx%d, 期望 2x2", w, h)
	}
}

// TestImageExplicitSize 显式尺寸优先, 只给一轴时另一轴仍取自然值
// ("给宽不给高"是最常见的写法, 不该因此把高度变成 0 → 整支不绘制)。
func TestImageExplicitSize(t *testing.T) {
	p := writeTestImage(t, "a.png")
	n := mkImage(p, map[string]float64{"width": 10})
	if w, h := n.intrinsicSize(); w != 10 || h != 2 {
		t.Fatalf("显式宽 10 + 自然高 2, 实际 %dx%d", w, h)
	}
	n2 := mkImage(p, map[string]float64{"width": 10, "height": 7})
	if w, h := n2.intrinsicSize(); w != 10 || h != 7 {
		t.Fatalf("两轴都显式应原样生效, 实际 %dx%d", w, h)
	}
}

// TestImageBlitOneToOne 1:1 快路径要逐像素等价于直接拷贝。
func TestImageBlitOneToOne(t *testing.T) {
	p := writeTestImage(t, "a.png")
	src := mustLoadImage(t, p).img
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	blitNearest(img, Rect{X: 0, Y: 0, W: 2, H: 2}, src)
	for y := 0; y < 2; y++ {
		for x := 0; x < 2; x++ {
			// 期望值从源图现算, 不写死色值
			assertPx(t, img, x, y, src.RGBAAt(x, y), "1:1 拷贝")
		}
	}
}

// TestImageBlitScaledCorners 2x2 → 4x4 最近邻: 每个源像素铺成一个 2x2 块,
// 于是四角分别对应源图四角。用四角断言能同时抓住"缩放比例反了"和"行列对调"
// 这两种最典型的采样错位。
func TestImageBlitScaledCorners(t *testing.T) {
	p := writeTestImage(t, "a.png")
	src := mustLoadImage(t, p).img
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	blitNearest(img, Rect{X: 0, Y: 0, W: 4, H: 4}, src)

	assertPx(t, img, 0, 0, src.RGBAAt(0, 0), "放大后左上")
	assertPx(t, img, 3, 0, src.RGBAAt(1, 0), "放大后右上")
	assertPx(t, img, 0, 3, src.RGBAAt(0, 1), "放大后左下")
	assertPx(t, img, 3, 3, src.RGBAAt(1, 1), "放大后右下")
}

// TestImageBlitRespectsCanvasClip 绘制必须落在画布内 (子图裁剪) —— 组件盒
// 超出窗口时不能越界写像素 (那会 panic 或写坏相邻行)。
func TestImageBlitRespectsCanvasClip(t *testing.T) {
	p := writeTestImage(t, "a.png")
	src := mustLoadImage(t, p).img
	img := image.NewRGBA(image.Rect(0, 0, 3, 3))
	// 目标盒 6x6 超出 3x3 画布
	blitNearest(img, Rect{X: 0, Y: 0, W: 6, H: 6}, src)
	assertPx(t, img, 0, 0, src.RGBAAt(0, 0), "裁剪后左上仍在")
	// 右边界像素: x=2 映射到源 x=2*2/6=0, y=2 → 源 y=0 → 仍然是源左上角色
	assertPx(t, img, 2, 2, src.RGBAAt(0, 0), "越界部分被裁到画布内")
}

// TestImageBlitCompositesAlpha 半透明源像素要 **src-over 合成**, 不是直接替换。
// 直接替换会让带透明通道的 PNG 显示成"黑/白底贴片", 这是最容易漏的一处。
func TestImageBlitCompositesAlpha(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1, 1))
	// image.RGBA 的 Pix 是预乘表示: 50% 不透明度的纯红 = (0x80, 0, 0, 0x80)
	src.SetRGBA(0, 0, color.RGBA{R: 0x80, A: 0x80})

	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	FillRect(img, Rect{0, 0, 1, 1}, color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF})
	blitNearest(img, Rect{0, 0, 1, 1}, src)

	got := img.RGBAAt(0, 0)
	// 白底上叠 50% 预乘红: R = 0x80 + 0xFF*(255-0x80)/255 = 255, G/B = 127
	if got.R != 255 {
		t.Fatalf("合成后 R = %d, 期望 255 (红通道饱和)", got.R)
	}
	if got.G != 127 || got.B != 127 {
		t.Fatalf("合成后 G/B = %d/%d, 期望 127/127 (透出白底)", got.G, got.B)
	}
	if got.A != 255 {
		t.Fatalf("合成后 A = %d, 期望 255", got.A)
	}
}

// TestImageBadPathPlaceholder 坏路径: 画占位 + 警告一次 + 固有尺寸非 0。
func TestImageBadPathPlaceholder(t *testing.T) {
	var warned []string
	oldWarn := warnImageLoad
	warnImageLoad = func(path string, err error) { warned = append(warned, path) }
	defer func() { warnImageLoad = oldWarn }()

	missing := filepath.Join(t.TempDir(), "definitely-missing.png")

	// 未给尺寸: 必须落到非 0 的占位尺寸, 否则 0 尺寸子树被 drawNode 整支跳过
	fresh := mkImage(missing, nil)
	if w, h := fresh.intrinsicSize(); w != imgPlaceholderW || h != imgPlaceholderH {
		t.Fatalf("加载失败应给非 0 占位尺寸, 实际 %dx%d", w, h)
	}
	// 给了尺寸: 显式值优先, 占位会铺满整盒
	n := mkImage(missing, map[string]float64{"width": 20, "height": 20})
	n.Box = Rect{X: 0, Y: 0, W: 20, H: 20}

	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	paintImage(img, n, false)

	if len(warned) == 0 {
		t.Fatalf("加载失败应触发警告")
	}
	// (0, 10) 刻意避开两条对角线 (对角线上是交叉线色)
	if got := img.RGBAAt(0, 10); got != colorImagePlaceholder {
		t.Fatalf("占位灰底 = %v, 期望 %v", got, colorImagePlaceholder)
	}
	if c := countColor(img, n.Box, colorImageCross); c == 0 {
		t.Fatalf("占位应画出交叉线")
	}
	if c := countColor(img, n.Box, colorImagePlaceholder); c == 0 {
		t.Fatalf("占位应画出灰底")
	}
}

// TestImageWarnOncePerPath 同一路径只警告一次: 绘制每帧都会重试加载,
// 不去重会把 stderr 刷爆。
func TestImageWarnOncePerPath(t *testing.T) {
	count := 0
	oldWarn := warnImageLoad
	warnImageLoad = func(path string, err error) { count++ }
	defer func() { warnImageLoad = oldWarn }()

	missing := filepath.Join(t.TempDir(), "missing.png")
	n := mkImage(missing, map[string]float64{"width": 4, "height": 4})
	n.Box = Rect{X: 0, Y: 0, W: 4, H: 4}
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := 0; i < 3; i++ { // 模拟三帧重绘
		paintImage(img, n, false)
	}
	if count != 1 {
		t.Fatalf("同一路径应只警告一次, 实际 %d 次", count)
	}
}

// TestImageDisabledVeil 禁用态的罩层要真的改变像素 (否则"禁用了"看不出来)。
func TestImageDisabledVeil(t *testing.T) {
	p := writeTestImage(t, "a.png")
	n := mkImage(p, map[string]float64{"width": 2, "height": 2})
	n.Box = Rect{X: 0, Y: 0, W: 2, H: 2}

	plain := image.NewRGBA(image.Rect(0, 0, 2, 2))
	paintImage(plain, n, false)
	veiled := image.NewRGBA(image.Rect(0, 0, 2, 2))
	paintImage(veiled, n, true)

	// 源左上角是纯红 (G=B=0); 罩层是半透明灰, 应把 G/B 抬起来
	if veiled.RGBAAt(0, 0).G <= plain.RGBAAt(0, 0).G {
		t.Fatalf("禁用罩应抬高 G 通道: %v vs %v", veiled.RGBAAt(0, 0), plain.RGBAAt(0, 0))
	}
}

// TestImageDrawsInTree 走完整布局+绘制链路: 图片真的出现在树上对应位置。
func TestImageDrawsInTree(t *testing.T) {
	p := writeTestImage(t, "a.png")
	src := mustLoadImage(t, p).img

	root := mkNode("column", nil)
	mountChildren(root, mkImage(p, map[string]float64{"width": 2, "height": 2}))
	img := renderTree(root, 2, 2)

	assertPx(t, img, 0, 0, src.RGBAAt(0, 0), "树上图片左上角")
	assertPx(t, img, 1, 1, src.RGBAAt(1, 1), "树上图片右下角")
}

// TestImageCacheSharedAndEvicts 缓存要: ① 同路径命中同一实例 (否则每帧重解码);
// ② 超出上限淘汰最旧; ③ 重复 put 同一键不占两个槽位。
func TestImageCacheSharedAndEvicts(t *testing.T) {
	p := writeTestImage(t, "a.png")
	e1 := mustLoadImage(t, p)
	e2 := mustLoadImage(t, p)
	if e1 != e2 {
		t.Fatalf("同一路径应命中缓存 (返回同一实例)")
	}

	c := newImageLRU(2)
	c.put("a", &imageEntry{})
	c.put("b", &imageEntry{})
	c.put("a", &imageEntry{}) // 重复键不该占两个槽位
	c.put("c", &imageEntry{}) // 淘汰最旧的 a
	if c.get("a") != nil {
		t.Fatalf("a 应已被淘汰")
	}
	if c.get("b") == nil || c.get("c") == nil {
		t.Fatalf("b/c 应仍在缓存里")
	}
}
