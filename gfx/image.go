package gfx

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"  // image.Decode 按文件头自动选解码器, 静态帧取首帧
	_ "image/jpeg" //
	_ "image/png"  //
	"os"
	"path/filepath"
	"sync"
)

// 图片组件 `<image src width height>` (P2-9)。
//
// 解码全走 Go 标准库 (png / jpeg / gif), 不引入第三方依赖。结果按路径进
// 包级 LRU —— **绘制期解码是每帧都会走到的路径**, 不缓存的话每次局部重绘都
// 要重新读文件 + 解码整张图, 滚动一个含图列表会直接卡死。
//
// v1 只做最近邻缩放 (无插值/无圆角/无缓存到纹理), 这是刻意的: 先把"图能显示
// 出来、尺寸对、坏路径不炸"这三件事做扎实, 采样质量是纯优化。

// 占位外观: 加载失败时画的东西。"什么都不画"是错的 —— 那样用户看到的和
// "本来就没放图"完全一样, 分不清是脚本写错了路径还是组件坏了。
var (
	colorImagePlaceholder = color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 255}  // 占位灰底
	colorImageCross       = color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 255}  // 占位交叉线
	colorImageDisabled    = color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 0x99} // 禁用态的罩层
)

// 占位尺寸: 加载失败时给的非 0 兜底尺寸。
//
// 必须非 0 —— 0 尺寸子树会被 drawNode 整支跳过, 用户看到的就是"什么都没有",
// 而不是"这里本该有张图"。
const (
	imgPlaceholderW = 16
	imgPlaceholderH = 16
)

// ===== 解码 + LRU 缓存 =====

// imageEntry 是缓存的解码结果。自然尺寸从 img.Bounds() 取, 不必另存一份
// (两个字段就有互相不一致的可能)。
type imageEntry struct {
	img *image.RGBA
}

// imageLRU 是 路径 → 解码结果 的 LRU。
//
// 结构与 font.go 的 glyphCache 一致 (order 切片 + entry map, 满了淘汰最旧):
// 单线程 GUI 访问, 锁只是防御 —— 用 container/list 换来的常数级淘汰在这里
// 没有意义, 而"和旁边那份缓存长得一样"对读代码的人价值更大。
type imageLRU struct {
	mu    sync.Mutex
	cap   int
	order []string
	entry map[string]*imageEntry
}

func newImageLRU(capacity int) *imageLRU {
	return &imageLRU{cap: capacity, entry: map[string]*imageEntry{}}
}

func (c *imageLRU) get(path string) *imageEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entry[path]
}

func (c *imageLRU) put(path string, e *imageEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entry[path]; exists {
		return
	}
	if len(c.order) >= c.cap {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.entry, old)
	}
	c.order = append(c.order, path)
	c.entry[path] = e
}

// imageCacheCap 是缓存张数上限。按张数而不是字节数: 反正 v1 连"按字节淘汰"
// 的度量都没做, 写一个假的字节估算反而更误导。
const imageCacheCap = 16

var imageCache = newImageLRU(imageCacheCap)

// loadImage 取解码结果 (先查缓存)。调用方负责把路径解析成实际文件路径。
func loadImage(path string) (*imageEntry, error) {
	if path == "" {
		return nil, fmt.Errorf("empty src")
	}
	if e := imageCache.get(path); e != nil {
		return e, nil
	}
	img, err := decodeImageFile(path)
	if err != nil {
		return nil, err
	}
	e := &imageEntry{img: img}
	imageCache.put(path, e)
	return e, nil
}

// decodeImageFile 读文件并解码为 *image.RGBA (统一表示, 便于后续采样)。
func decodeImageFile(path string) (*image.RGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, err
	}
	return toRGBA(src), nil
}

// toRGBA 把任意 image.Image 转成 *image.RGBA, **并把原点归一化到 (0,0)**。
//
// 归一化是必须的: 解码出来的图可能带非零 Bounds().Min (尤其 gif/子图),
// 而下面的采样循环一律按"源原点即 (0,0)"推导源坐标, 不归一化就会整体错位。
func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok && r.Rect.Min == (image.Point{}) {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// ===== 加载失败警告 (同一路径只报一次) =====

// warnImageLoad 是加载失败的警告出口。做成变量便于单测替换成计数器。
var warnImageLoad = func(path string, err error) {
	fmt.Fprintf(os.Stderr, "gfx: image %q load failed: %v\n", path, err)
}

var (
	imageWarnMu   sync.Mutex
	imageWarnSeen = map[string]struct{}{}
)

// warnImageLoadOnce 同一路径只警告一次: 绘制每帧都会重试加载, 不去重会把
// stderr 刷爆 (和未知标签警告同一个理由)。
func warnImageLoadOnce(path string, err error) {
	imageWarnMu.Lock()
	defer imageWarnMu.Unlock()
	if _, seen := imageWarnSeen[path]; seen {
		return
	}
	imageWarnSeen[path] = struct{}{}
	warnImageLoad(path, err)
}

// ===== src 解析 =====

// imageSrcPath 把 src prop 解析成实际文件路径。
//
// 相对路径按**进程 cwd** 解释, 而不是"脚本所在目录" —— 脚本是喂给 VM 的
// 输入, 没有"所在目录"这个概念 (可以从 stdin、从字符串、从打包产物来)。
func imageSrcPath(n *GuiNode) string {
	src, _ := n.PropStr("src")
	if src == "" {
		return ""
	}
	if filepath.IsAbs(src) {
		return src
	}
	wd, err := os.Getwd()
	if err != nil || wd == "" {
		return filepath.Clean(src)
	}
	return filepath.Join(wd, src)
}

// imageNaturalSize 返回图片自然尺寸; 加载失败返回 0,0 (由调用方决定兜底)。
func imageNaturalSize(n *GuiNode) (w, h int) {
	e, err := loadImage(imageSrcPath(n))
	if err != nil {
		return 0, 0
	}
	b := e.img.Bounds()
	return b.Dx(), b.Dy()
}

// ===== 绘制 =====

// paintImage 绘制 image 组件: 成功则缩放到 Box, 失败则画占位。
func paintImage(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	path := imageSrcPath(n)
	e, err := loadImage(path)
	if err != nil {
		warnImageLoadOnce(path, err)
		paintImagePlaceholder(img, n.Box, disabled)
		return
	}
	blitNearest(img, n.Box, e.img)
	if disabled {
		// 图片不像颜色那样能"整体降饱和": 逐像素染色要遍历整张图, 明显不划算。
		// 罩一层半透明灰是等价表达, 且只在禁用子树里才付这个代价。
		FillRect(img, n.Box, colorImageDisabled)
	}
}

// paintImagePlaceholder 画"这里本该有张图"的占位: 灰底 + 两条对角线。
//
// 用整盒对角线而不是固定尺寸的图标: 盒子多大交叉线就多大, 任何尺寸下都看得懂,
// 也不会出现"大盒子里角落里一个小图标"的错位观感。
func paintImagePlaceholder(img *image.RGBA, r Rect, disabled bool) {
	FillRect(img, r, tint(colorImagePlaceholder, disabled))
	if r.W < 3 || r.H < 3 {
		return // 太小了画线只会糊成一团
	}
	c := tint(colorImageCross, disabled)
	fillLine(img, r.X, r.Y, r.X+r.W-1, r.Y+r.H-1, 1, c)
	fillLine(img, r.X+r.W-1, r.Y, r.X, r.Y+r.H-1, 1, c)
}

// blitNearest 把 src 缩放绘制到 img 的 r 区域 (最近邻采样)。
//
// 两条路径:
//   - 1:1 (尺寸恰好相等) 走逐像素直拷 —— 最常见的情形是"按原尺寸显示",
//     不该为它付每像素一次除法的代价;
//   - 缩放时先算一张列映射表, 把除法从"每像素一次"降到"每列一次"。
//
// 索引一律相对子图 Rect.Min 计算, 于是调用方传入脏区子图时自动被裁剪
// (与 FillRect / DrawText 同一套约定)。
//
// 子树不透明度 (P3-2): 这里绕过了 FillRect, 所以淡出要自己接一手 ——
// 用 fadeOf() 取当前因子, 展开成逐像素的 alpha 缩放。**不透明时走原路径**,
// 常见的"没开 opacity"场景不多花一分钱。
func blitNearest(img *image.RGBA, r Rect, src *image.RGBA) {
	if r.W <= 0 || r.H <= 0 {
		return
	}
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if sw <= 0 || sh <= 0 {
		return
	}
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	fade := fadeOf()
	dstStride := img.Stride
	dstPix := img.Pix
	ox, oy := img.Rect.Min.X, img.Rect.Min.Y
	srcMinX, srcMinY := src.Rect.Min.X, src.Rect.Min.Y

	if fade >= 1 {
		if sw == r.W && sh == r.H {
			for y := y0; y < y1; y++ {
				srow := src.Pix[(sb.Min.Y+(y-r.Y)-srcMinY)*src.Stride:]
				drow := dstPix[(y-oy)*dstStride:]
				for x := x0; x < x1; x++ {
					blitPixel(drow, (x-ox)*4, srow, (sb.Min.X+(x-r.X)-srcMinX)*4)
				}
			}
			return
		}

		// 缩放: 预先把每列对应的源 x 算好
		xs := make([]int, r.W)
		for i := range xs {
			xs[i] = (sb.Min.X + i*sw/r.W - srcMinX) * 4
		}
		for y := y0; y < y1; y++ {
			sy := sb.Min.Y + (y-r.Y)*sh/r.H
			srow := src.Pix[(sy-srcMinY)*src.Stride:]
			drow := dstPix[(y-oy)*dstStride:]
			for x := x0; x < x1; x++ {
				blitPixel(drow, (x-ox)*4, srow, xs[x-r.X])
			}
		}
		return
	}

	// 淡出路径: 与上面同构, 只是每个像素多乘一次 fade。
	if sw == r.W && sh == r.H {
		for y := y0; y < y1; y++ {
			srow := src.Pix[(sb.Min.Y+(y-r.Y)-srcMinY)*src.Stride:]
			drow := dstPix[(y-oy)*dstStride:]
			for x := x0; x < x1; x++ {
				blitPixelFade(drow, (x-ox)*4, srow, (sb.Min.X+(x-r.X)-srcMinX)*4, fade)
			}
		}
		return
	}
	xs := make([]int, r.W)
	for i := range xs {
		xs[i] = (sb.Min.X + i*sw/r.W - srcMinX) * 4
	}
	for y := y0; y < y1; y++ {
		sy := sb.Min.Y + (y-r.Y)*sh/r.H
		srow := src.Pix[(sy-srcMinY)*src.Stride:]
		drow := dstPix[(y-oy)*dstStride:]
		for x := x0; x < x1; x++ {
			blitPixelFade(drow, (x-ox)*4, srow, xs[x-r.X], fade)
		}
	}
}

// blitPixelFade 与 blitPixel 同义, 但先按 fade 缩放源像素的 alpha (P3-2 淡出)。
//
// 预乘表示下"降 alpha"就是四个通道同比例缩小 —— 不能只改 A, 那会让颜色
// 变亮 (预乘约定被破坏, 表现为"淡出过程中图片发白")。
func blitPixelFade(dst []byte, di int, sp []byte, si int, fade float64) {
	a := uint32(float64(sp[si+3]) * fade)
	switch a {
	case 0:
		return
	case 255:
		dst[di], dst[di+1], dst[di+2], dst[di+3] = sp[si], sp[si+1], sp[si+2], 255
	default:
		r := uint8(uint32(sp[si]) * a / 255)
		g := uint8(uint32(sp[si+1]) * a / 255)
		b := uint8(uint32(sp[si+2]) * a / 255)
		inv := 255 - int(a)
		dst[di] = blendChannel(r, dst[di], inv)
		dst[di+1] = blendChannel(g, dst[di+1], inv)
		dst[di+2] = blendChannel(b, dst[di+2], inv)
		dst[di+3] = clamp8(int(a) + int(dst[di+3])*inv/255)
	}
}

// blitPixel 把一个源像素以 src-over 合成到目标。
//
// 两侧都是 image.RGBA 的**预乘**表示, 所以合成就是 out = src + dst*(1-a),
// 不需要再除一次 alpha —— 与 FillRect 的半透明路径同一套数学。
// 不透明像素 (绝大多数) 走直拷分支, 不白算混合。
func blitPixel(dst []byte, di int, sp []byte, si int) {
	switch a := sp[si+3]; a {
	case 0:
		return
	case 255:
		dst[di], dst[di+1], dst[di+2], dst[di+3] = sp[si], sp[si+1], sp[si+2], 255
	default:
		inv := 255 - int(a)
		dst[di] = blendChannel(sp[si], dst[di], inv)
		dst[di+1] = blendChannel(sp[si+1], dst[di+1], inv)
		dst[di+2] = blendChannel(sp[si+2], dst[di+2], inv)
		dst[di+3] = clamp8(int(a) + int(dst[di+3])*inv/255)
	}
}
