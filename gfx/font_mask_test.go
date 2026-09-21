package gfx

import (
	"bytes"
	"image"
	"image/color"
	"testing"
)

// 字形缓存别名回归 (2026-09-21)。
//
// 背景: x/image/font/opentype 的 Face 每次 Glyph 都返回**同一个** *image.Alpha,
// 并复写它那块 Pix (文档允许: 掩码只保证在下次 Glyph 调用前有效)。font.go 的
// glyph() 曾把这个指针直接塞进 LRU ⇒ 所有缓存项别名同一块显存, 谁最后被光栅化
// 谁就"赢"。现象: 整屏文字都长成同一个字形 (字距、布局、字号全对)。
//
// 这两个用例故意把"渲染"拆成**多次 DrawText 调用** —— 同一个 DrawText 内部
// 每个字形是"刚光栅化完就落笔", 所以旧实现也能通过 (这正是它长期潜伏的原因)。

// renderOnce 把 text 画进一张新图并返回位图。
func renderOnce(text string, size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, 260, 48))
	FillRect(img, Rect{0, 0, 260, 48}, color.RGBA{R: 255, G: 255, B: 255, A: 255})
	DrawText(img, img.Bounds(), text, 2, 2, size, color.RGBA{A: 255}, 0)
	return img
}

// TestGlyphCacheNoAliasing 同一串文本渲染两次必须逐像素一致。
//
// 旧实现: 第二遍全是缓存命中 ⇒ 每个字形都画成"最后一次光栅化"的那块 Pix,
// 于是同一串文本两遍结果不同 (第二遍整串变成同一个字)。
func TestGlyphCacheNoAliasing(t *testing.T) {
	requireFont(t)
	texts := []string{"设", "置", "设置", "设置设置", "小程序 设置值", "about"}
	for _, text := range texts {
		for _, size := range []int{12, 16, 24} {
			// 第一遍之前先渲染别的字符, 保证缓存里已有污染源。
			renderOnce("置程序", size)
			first := renderOnce(text, size)
			renderOnce("关于我们", size)
			second := renderOnce(text, size)
			if !bytes.Equal(first.Pix, second.Pix) {
				t.Errorf("%q @%d: 两遍渲染像素不一致 —— 字形缓存别名了 face 的掩码缓冲", text, size)
			}
		}
	}
}

// TestGlyphCacheRepeatedRuneIdentical 同一行里重复出现的字符必须画出相同位图,
// 不同字符必须画出不同位图 (后者防"全都一样的字形"这类退化)。
func TestGlyphCacheRepeatedRuneIdentical(t *testing.T) {
	requireFont(t)
	const size = 16
	img := renderOnce("设置设置", size)
	boxes := glyphBoxes(img)
	if len(boxes) != 4 {
		t.Fatalf("切成 %d 个字形, want 4 (box=%v)", len(boxes), boxes)
	}
	crop := func(i int) []byte { return cropBox(img, boxes[i]) }
	if !bytes.Equal(crop(0), crop(2)) {
		t.Error("第 1、3 个字 (同为 '设') 位图不同")
	}
	if !bytes.Equal(crop(1), crop(3)) {
		t.Error("第 2、4 个字 (同为 '置') 位图不同")
	}
	if bytes.Equal(crop(0), crop(1)) {
		t.Error("'设' 与 '置' 位图相同 —— 所有字形退化成同一个")
	}
	// 全部两两比较: 四个字形应恰好两种
	seen := [][]byte{}
	for i := 0; i < 4; i++ {
		dup := false
		for _, s := range seen {
			if bytes.Equal(s, crop(i)) {
				dup = true
			}
		}
		if !dup {
			seen = append(seen, crop(i))
		}
	}
	if len(seen) != 2 {
		t.Errorf("4 个字形里有 %d 种位图, want 2", len(seen))
	}
}

// glyphBoxes 按列方向墨迹把一行文本切成字形矩形 (测试专用, 故意写得简单)。
func glyphBoxes(img *image.RGBA) []image.Rectangle {
	b := img.Bounds()
	ink := make([]bool, b.Dx())
	for x := 0; x < b.Dx(); x++ {
		for y := 0; y < b.Dy(); y++ {
			if img.RGBAAt(b.Min.X+x, b.Min.Y+y).R < 128 {
				ink[x] = true
				break
			}
		}
	}
	var out []image.Rectangle
	start := -1
	for x := 0; x <= len(ink); x++ {
		on := x < len(ink) && ink[x]
		if on && start < 0 {
			start = x
		} else if !on && start >= 0 {
			y0, y1 := b.Dy(), 0
			for yy := 0; yy < b.Dy(); yy++ {
				for xx := start; xx < x; xx++ {
					if img.RGBAAt(b.Min.X+xx, b.Min.Y+yy).R < 128 {
						if yy < y0 {
							y0 = yy
						}
						if yy > y1 {
							y1 = yy
						}
						break
					}
				}
			}
			out = append(out, image.Rect(start, y0, x, y1+1))
			start = -1
		}
	}
	return out
}

// cropBox 抠出一块矩形, 返回归一化后的像素 (左上角对齐), 便于按位比较字形。
func cropBox(img *image.RGBA, r image.Rectangle) []byte {
	out := make([]byte, 0, r.Dx()*r.Dy())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			out = append(out, img.RGBAAt(x, y).R)
		}
	}
	return out
}
