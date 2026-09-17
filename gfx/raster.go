package gfx

import (
	"image"
	"image/color"
	"strconv"
	"strings"
)

// 软件光栅化 (P2): 实心矩形、1px 边框、背景色。
// 文字渲染留给 P3 (sfnt 字体), #text 节点当前不绘制。

// namedColors 少量常用命名色 (CSS 子集, 覆盖演示需求)。
var namedColors = map[string]color.RGBA{
	"black": {0, 0, 0, 255}, "white": {255, 255, 255, 255},
	"red": {220, 20, 60, 255}, "green": {34, 139, 34, 255},
	"blue": {30, 144, 255, 255}, "gray": {128, 128, 128, 255},
	"grey": {128, 128, 128, 255}, "silver": {192, 192, 192, 255},
	"yellow": {255, 215, 0, 255}, "orange": {255, 140, 0, 255},
	"purple": {128, 0, 128, 255}, "pink": {255, 105, 180, 255},
	"brown": {139, 69, 19, 255}, "navy": {0, 0, 128, 255},
	"teal": {0, 128, 128, 255}, "crimson": {220, 20, 60, 255},
}

// ParseColor 解析颜色: "#rrggbb"、"#rgb" 或命名色。失败返回 ok=false。
func ParseColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if c, ok := namedColors[s]; ok {
		return c, true
	}
	if strings.HasPrefix(s, "#") {
		hex := s[1:]
		switch len(hex) {
		case 3:
			r, err1 := strconv.ParseUint(string(hex[0]), 16, 8)
			g, err2 := strconv.ParseUint(string(hex[1]), 16, 8)
			b, err3 := strconv.ParseUint(string(hex[2]), 16, 8)
			if err1 == nil && err2 == nil && err3 == nil {
				return color.RGBA{uint8(r * 17), uint8(g * 17), uint8(b * 17), 255}, true
			}
		case 6:
			v, err := strconv.ParseUint(hex, 16, 32)
			if err == nil {
				return color.RGBA{uint8(v >> 16), uint8(v >> 8 & 0xFF), uint8(v & 0xFF), 255}, true
			}
		}
	}
	return color.RGBA{}, false
}

// textColor 返回文本颜色 (prop "color", 默认近黑)。
func (n *GuiNode) textColor() color.RGBA {
	if s, ok := n.PropStr("color"); ok {
		if c, ok := ParseColor(s); ok {
			return c
		}
	}
	return color.RGBA{R: 26, G: 26, B: 26, A: 255}
}

// Draw 把元素树画到 img (先父后子, 后画的覆盖先画的)。
func Draw(img *image.RGBA, root *GuiNode) {
	DrawClipped(img, root, img.Bounds())
}

// DrawClipped 画元素树, 只光栅化与 clip 相交的子树 (脏矩形局部重绘)。
func DrawClipped(img *image.RGBA, root *GuiNode, clip image.Rectangle) {
	if root == nil {
		return
	}
	drawNode(img, root, clip)
}

func drawNode(img *image.RGBA, n *GuiNode, clip image.Rectangle) {
	box := image.Rectangle{
		Min: image.Point{n.Box.X, n.Box.Y},
		Max: image.Point{n.Box.X + n.Box.W, n.Box.Y + n.Box.H},
	}
	// 不与脏区相交的子树整支跳过 (#text 无固定框, 由父级框粗判)
	if n.Tag != "#text" && !box.Overlaps(clip) {
		return
	}
	if n.Tag == "#text" {
		// 文本节点: 在自身框内绘制 (超宽截断)
		DrawText(img, clip, n.Text, n.Box.X, n.Box.Y, n.FontSize(), n.textColor(), n.Box.W)
		return
	}
	if n.Tag == "text" {
		// 文本容器: 拼接 #text 子节点为一行, 在自身框内绘制
		if text := n.TextContent(); text != "" {
			DrawText(img, clip, text, n.Box.X, n.Box.Y, n.FontSize(), n.textColor(), n.Box.W)
		}
	} else {
		if bg, ok := n.PropStr("background"); ok {
			if c, ok := ParseColor(bg); ok {
				FillRect(img, n.Box, c)
			}
		}
		if border, ok := n.PropStr("border"); ok {
			if c, ok := ParseColor(border); ok {
				StrokeRect(img, n.Box, c)
			}
		}
	}
	for _, c := range n.Children {
		drawNode(img, c, clip)
	}
}

// clip 求矩形与画布的交集。
func clip(img *image.RGBA, r Rect) (x0, y0, x1, y1 int, visible bool) {
	b := img.Bounds()
	x0, y0 = max(r.X, b.Min.X), max(r.Y, b.Min.Y)
	x1, y1 = min(r.X+r.W, b.Max.X), min(r.Y+r.H, b.Max.Y)
	return x0, y0, x1, y1, x0 < x1 && y0 < y1
}

// FillRect 填充实心矩形 (越界部分裁剪)。
func FillRect(img *image.RGBA, r Rect, c color.RGBA) {
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	stride := img.Stride
	pix := img.Pix
	ca := uint32(c.A)
	// 预乘 alpha (image.RGBA 语义)
	cr, cg, cb := premult(c.R, ca), premult(c.G, ca), premult(c.B, ca)
	for y := y0; y < y1; y++ {
		row := pix[y*stride:]
		for x := x0; x < x1; x++ {
			i := x * 4
			row[i], row[i+1], row[i+2], row[i+3] = cr, cg, cb, 255
		}
	}
}

// StrokeRect 画 1px 边框 (四条边的实心矩形)。
func StrokeRect(img *image.RGBA, r Rect, c color.RGBA) {
	FillRect(img, Rect{r.X, r.Y, r.W, 1}, c)
	FillRect(img, Rect{r.X, r.Y + r.H - 1, r.W, 1}, c)
	FillRect(img, Rect{r.X, r.Y, 1, r.H}, c)
	FillRect(img, Rect{r.X + r.W - 1, r.Y, 1, r.H}, c)
}

// premult 按 alpha 预乘一个通道 (v * a / 255)。
func premult(v uint8, a uint32) uint8 {
	return uint8(uint32(v) * a / 255)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
