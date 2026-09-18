package gfx

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// 文字渲染子系统 (P3)。
//
// 字体来源: 系统字体文件 (微软雅黑优先, CJK 覆盖最好), 经
// x/image/font/opentype 解析。按字号懒建 face, rune → glyph 掩码做
// LRU 缓存 (键 = 字号|rune)。不做复杂 shaping: 中西文按码位直排
// (任务书 P3 约定; 连字/复杂脚本不支持)。

// fontCandidates 系统字体候选, 依次尝试首个可解析者。
var fontCandidates = []string{
	`C:\Windows\Fonts\msyh.ttc`,   // 微软雅黑
	`C:\Windows\Fonts\msyhbd.ttc`, // 微软雅黑 粗体
	`C:\Windows\Fonts\simsun.ttc`, // 宋体
	`C:\Windows\Fonts\segoeui.ttf`,
}

var (
	fontMu     sync.Mutex
	baseFont   *opentype.Font        // 解析后的基础字体
	faceBySize = map[int]font.Face{} // 字号 → face
	glyphLRU   = newGlyphCache(1024) // rune 掩码缓存
)

// loadBaseFont 加载首个可用系统字体 (幂等, 自行加锁)。
func loadBaseFont() (*opentype.Font, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	return loadBaseFontLocked()
}

// loadBaseFontLocked 加载首个可用系统字体 (调用方必须已持有 fontMu;
// fontFace 在持锁路径里调用, 避免不可重入锁死锁)。
func loadBaseFontLocked() (*opentype.Font, error) {
	if baseFont != nil {
		return baseFont, nil
	}
	var lastErr error
	for _, path := range fontCandidates {
		if filepath.Base(path) == "" {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			lastErr = err
			continue
		}
		col, err := opentype.ParseCollection(data)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", filepath.Base(path), err)
			continue
		}
		f, err := col.Font(0)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", filepath.Base(path), err)
			continue
		}
		baseFont = f
		return baseFont, nil
	}
	return nil, fmt.Errorf("no usable system font (tried %d candidates, last: %v)",
		len(fontCandidates), lastErr)
}

// fontFace 返回指定像素字号的 face (懒建并缓存)。
func fontFace(size int) (font.Face, error) {
	if size < 8 {
		size = 8
	}
	fontMu.Lock()
	defer fontMu.Unlock()
	if f, ok := faceBySize[size]; ok {
		return f, nil
	}
	base, err := loadBaseFontLocked()
	if err != nil {
		return nil, err
	}
	// Size 单位是 pt, DPI=72 时 1pt = 1px, 直接以像素当字号。
	face, err := opentype.NewFace(base, &opentype.FaceOptions{
		Size:    float64(size),
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	faceBySize[size] = face
	return face, nil
}

// glyphEntry 是缓存的 glyph 渲染结果 (相对 dot 原点)。
type glyphEntry struct {
	mask    *image.Alpha // glyph 掩码
	offX    int          // 掩码绘制偏移 (dr.Min 相对 dot)
	offY    int
	advance int // 前进宽度 (px)
}

// glyphCache 是 rune → glyph 的 LRU 缓存 (单线程 GUI 访问, 锁仅为防御)。
type glyphCache struct {
	mu    sync.Mutex
	cap   int
	order []glyphKey
	entry map[glyphKey]*glyphEntry
}

type glyphKey struct {
	size int
	r    rune
}

func newGlyphCache(capacity int) *glyphCache {
	return &glyphCache{cap: capacity, entry: map[glyphKey]*glyphEntry{}}
}

func (c *glyphCache) get(size int, r rune) *glyphEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := glyphKey{size, r}
	if e, ok := c.entry[k]; ok {
		return e
	}
	return nil
}

func (c *glyphCache) put(size int, r rune, e *glyphEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := glyphKey{size, r}
	if _, exists := c.entry[k]; exists {
		return
	}
	if len(c.order) >= c.cap {
		// 淘汰最旧
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.entry, old)
	}
	c.order = append(c.order, k)
	c.entry[k] = e
}

// glyph 渲染 (或取缓存) 一个字符: 以 dot=(0,0) 调 face.Glyph,
// 缓存掩码与偏移, 绘制时平移。
func glyph(size int, r rune) (*glyphEntry, error) {
	if e := glyphLRU.get(size, r); e != nil {
		return e, nil
	}
	face, err := fontFace(size)
	if err != nil {
		return nil, err
	}
	dr, mask, _, advance, ok := face.Glyph(fixed.P(0, 0), r)
	if !ok {
		return &glyphEntry{advance: size / 2}, nil // 缺字形: 占位宽度
	}
	alpha, _ := mask.(*image.Alpha)
	if alpha == nil {
		// sfnt 的掩码总是 *image.Alpha; 其他实现回退为无掩码
		return &glyphEntry{advance: advance.Ceil()}, nil
	}
	e := &glyphEntry{
		mask:    alpha,
		offX:    dr.Min.X,
		offY:    dr.Min.Y,
		advance: advance.Ceil(),
	}
	glyphLRU.put(size, r, e)
	return e, nil
}

// ascentCache 记录每字号 ascent (基线到顶部距离, px)。
var ascentCache = map[int]int{}

// textAscent 返回指定字号的 ascent。
func textAscent(size int) int {
	if a, ok := ascentCache[size]; ok {
		return a
	}
	face, err := fontFace(size)
	if err != nil {
		return size
	}
	m := face.Metrics()
	a := m.Ascent.Ceil()
	if a <= 0 {
		a = size
	}
	ascentCache[size] = a
	return a
}

// MeasureText 测量单行文本 (像素)。h 为行高 (含上下余量)。
func MeasureText(text string, size int) (w, h int) {
	if size < 8 {
		size = 8
	}
	for _, r := range text {
		e, err := glyph(size, r)
		if err != nil {
			return 0, 0
		}
		w += e.advance
	}
	h = size + size/4 // 近似行高 (ascent+descent 简化)
	return w, h
}

// ===== 自动换行 (P2-6) =====

// lineHeight 返回字号对应的行高。与 MeasureText 的 h 同口径 —— 多行文本的
// 总高就是 行数 × lineHeight, 别处不要另算一套。
func lineHeight(size int) int {
	if size < 8 {
		size = 8
	}
	return size + size/4
}

// runeAdvance 返回单个字符的前进宽度; 取不到字形时退化为半个字号宽
// (与 glyph 的缺字形占位一致), 保证换行计算永远有正数可用。
func runeAdvance(size int, r rune) int {
	if r == '\t' {
		// 制表符没有字形: 按 4 个空格算 (与终端习惯一致)
		return 4 * runeAdvance(size, ' ')
	}
	e, err := glyph(size, r)
	if err != nil || e.advance <= 0 {
		return size / 2
	}
	return e.advance
}

// runeWidth 返回字符串在给定字号下的像素宽度 (逐 rune 累加 advance)。
func runeWidth(text string, size int) int {
	w := 0
	for _, r := range text {
		w += runeAdvance(size, r)
	}
	return w
}

// ellipsisMark 是超行截断用的省略号。用三个点而不是 "…": 字体候选里
// 微软雅黑有 U+2026, 但宋体/Segoe 的度量差异会让最后一行宽度抖动。
const ellipsisMark = "..."

// ellipsize 把一行裁到 maxWidth 内并补省略号: 从尾部逐个字符回退, 直到
// "剩余内容 + ..." 放得下为止。maxWidth <= 0 (无约束) 时直接补后缀。
func ellipsize(line string, size, maxWidth int) string {
	if maxWidth <= 0 {
		return line + ellipsisMark
	}
	if runeWidth(line, size)+runeWidth(ellipsisMark, size) <= maxWidth {
		return line + ellipsisMark
	}
	markW := runeWidth(ellipsisMark, size)
	rs := []rune(line)
	used := 0
	for len(rs) > 0 {
		adv := runeAdvance(size, rs[len(rs)-1])
		if used+markW+adv > maxWidth {
			break
		}
		used += adv
		rs = rs[:len(rs)-1]
	}
	return string(rs) + ellipsisMark
}

// wrapText 把文本按 maxWidth 切成多行 (P2-6 的核心):
//   - 显式 '\n' 强制换行, 连续换行保留空行;
//   - 其余贪心逐 rune 累加 advance, 若加上下一个字符会超宽就折行 ——
//     中西文一视同仁 (CJK 字符 advance 约等于字号, 自动按字折行);
//   - maxWidth <= 0 表示"没有宽度约束": 只按 '\n' 切, 不折行;
//   - 单字符本身就宽于 maxWidth 时让它独占一行 —— 否则内层无法收尾,
//     maxWidth 比一个汉字还窄时会死循环;
//   - maxLines > 0 时只保留前 maxLines 行, 末行补 "..." 并裁到放得下。
//
// 返回值至少一行 (空串文本也会得到 [""]), 调用方不必再判空。
func wrapText(text string, size, maxWidth, maxLines int) []string {
	if size < 8 {
		size = 8
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		if maxWidth <= 0 {
			lines = append(lines, para)
			continue
		}
		cur := make([]rune, 0, 32)
		curW := 0
		for _, r := range para {
			adv := runeAdvance(size, r)
			if curW > 0 && curW+adv > maxWidth {
				lines = append(lines, string(cur))
				cur = cur[:0]
				curW = 0
			}
			cur = append(cur, r)
			curW += adv
		}
		lines = append(lines, string(cur))
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsize(lines[maxLines-1], size, maxWidth)
	}
	return lines
}

// MeasureTextMulti 多行测量: 宽 = 最长行, 高 = 行数 × 行高。
// maxWidth <= 0 时按 '\n' 分行, maxLines > 0 时按省略号截断口径测量
// (与 wrapText 完全一致, 所以布局尺寸和绘制结果不会打架)。
func MeasureTextMulti(text string, size, maxWidth, maxLines int) (w, h int) {
	if size < 8 {
		size = 8
	}
	lines := wrapText(text, size, maxWidth, maxLines)
	for _, ln := range lines {
		if lw := runeWidth(ln, size); lw > w {
			w = lw
		}
	}
	return w, len(lines) * lineHeight(size)
}

// DrawText 在 img 的 (x,y) (左上角) 画单行文本, 限制在 clip 矩形内;
// maxWidth > 0 时超出截断 (v1: 硬截断, 不加省略号)。
// 返回实际绘制的宽度。
func DrawText(img *image.RGBA, clip image.Rectangle, text string, x, y, size int, c color.RGBA, maxWidth int) int {
	if size < 8 {
		size = 8
	}
	ascent := textAscent(size)
	dotY := y + ascent
	drawn := 0
	for _, r := range text {
		e, err := glyph(size, r)
		if err != nil {
			return drawn
		}
		if maxWidth > 0 && drawn+e.advance > maxWidth {
			break
		}
		drawn += e.advance
		if e.mask != nil {
			blitGlyph(img, clip, e, x+e.offX, dotY+e.offY, c)
		}
		x += e.advance
	}
	return drawn
}

// blitGlyph 把 glyph 掩码按颜色 alpha 混合写入 img (clip 裁剪)。
func blitGlyph(img *image.RGBA, clip image.Rectangle, e *glyphEntry, dx, dy int, c color.RGBA) {
	b := e.mask.Bounds()
	for my := b.Min.Y; my < b.Max.Y; my++ {
		iy := dy + my - b.Min.Y
		if iy < clip.Min.Y || iy >= clip.Max.Y || iy < img.Rect.Min.Y || iy >= img.Rect.Max.Y {
			continue
		}
		for mx := b.Min.X; mx < b.Max.X; mx++ {
			ix := dx + mx - b.Min.X
			if ix < clip.Min.X || ix >= clip.Max.X || ix < img.Rect.Min.X || ix >= img.Rect.Max.X {
				continue
			}
			a := uint32(e.mask.AlphaAt(mx, my).A)
			if a == 0 {
				continue
			}
			// src-over: dst = src*a + dst*(1-a)
			off := img.PixOffset(ix, iy)
			p := img.Pix[off:]
			na := 255 - a
			p[0] = uint8((uint32(c.R)*a + uint32(p[0])*na) / 255)
			p[1] = uint8((uint32(c.G)*a + uint32(p[1])*na) / 255)
			p[2] = uint8((uint32(c.B)*a + uint32(p[2])*na) / 255)
		}
	}
}
