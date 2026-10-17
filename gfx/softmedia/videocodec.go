//go:build linux

package softmedia

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ===== softmedia 的视频格式口径 =====
//
// 内核不含解码器 (docs/video-decision.md), softmedia 也只解**标准库够得着**
// 的两种:
//
//	.gif     → image/gif (逐帧 + disposal 合成)
//	.mjpeg   → 裸 JPEG 帧拼接流 (FFD8…FFD9 反复出现), 每帧 image/jpeg 解
//
// .mjpeg 没有标准容器 —— 这里是 softmedia 自己定义的**最小流格式**: 文件里
// 除 JPEG 帧外没有任何东西, 帧率固定 25fps (没有元数据可放帧率, 也不为它
// 发明一个头)。转换一行就够: `ffmpeg -i in.mp4 -c:v mjpeg -q:v 4 out.mjpeg`
// 得到的是 mov 容器里的 mjpeg, 需要先抽出来 (mov 里的 mdat 剥壳即可) ——
// 这条路径给"我有一段小动画想先跑起来"用, 不是产品级方案。
//
// mp4 / mov / webm / mkv (h264/hevc/vp9/av1) 一律 errUnsupportedFormat:
// 那是平台媒体层 (nativeVideoHost 的活), 见 docs/media-decision.md。

const mjpegFPS = 25

// errUnsupportedFormat 标记"这个文件 softmedia 永远解不了" (与 IO 错误区分:
// 前者进 rejected 缓存, 后者每帧重试 —— 文件可能被换掉)。
var errUnsupportedFormat = errors.New("softmedia: 不支持的容器/编码")

// isFormatError 判永久性格式错误 (调用方据此决定要不要缓存)。
func isFormatError(err error) bool { return errors.Is(err, errUnsupportedFormat) }

// decodeVideo 按扩展名路由解码, 返回全帧 RGBA 序列与每帧停留时长。
//
// 扩展名判定在**读文件之前**: 不支持的格式连盘都不碰 (内核每帧重试时,
// 这一刀把重复 IO 也省了; 真正的读取错误留给 IO 错误通道, 不进缓存)。
func decodeVideo(path string) (frames []*image.RGBA, delays []time.Duration, err error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gif", ".mjpeg", ".mjpg":
		// 支持: 继续
	default:
		return nil, nil, fmt.Errorf("%w: %s (h264/vp9/av1 请接平台视频层, 见 docs/media-decision.md)",
			errUnsupportedFormat, filepath.Ext(path))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("softmedia: 读视频文件: %w", err)
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".gif":
		return decodeGIF(data)
	case ".mjpeg", ".mjpg":
		return decodeMJPEG(data)
	default:
		return nil, nil, fmt.Errorf("%w: %s", errUnsupportedFormat, filepath.Ext(path))
	}
}

// decodeMJPEG 把裸 JPEG 拼接流切成逐帧解码。
//
// 切分判据是 JPEG 的 SOI/EOI marker (FFD8 / FFD9): 从头找 FFD8, 配对到之后
// 第一个 FFD9, 中间那一段就是一帧。熵编码数据里的 0xFF 按 JPEG 规定必须
// 跟 0x00 (byte stuffing), 所以 spec 合规的流里不会出现伪 FFD9 —— 这就是
// "第一个 EOI 就是帧尾"的依据 (与 ffmpeg 的 mjpeg 解析同一条规则)。
func decodeMJPEG(data []byte) ([]*image.RGBA, []time.Duration, error) {
	var frames []*image.RGBA
	i := 0
	for i < len(data) {
		// 找下一个 SOI
		for i+1 < len(data) && !(data[i] == 0xFF && data[i+1] == 0xD8) {
			i++
		}
		if i+1 >= len(data) {
			break
		}
		// SOI 之后第一个 EOI 即帧尾
		j := i + 2
		for j+1 < len(data) && !(data[j] == 0xFF && data[j+1] == 0xD9) {
			j++
		}
		if j+1 >= len(data) {
			return nil, nil, fmt.Errorf("softmedia: MJPEG 流在偏移 %d 处残缺 (只有 SOI 没有 EOI) (%w)",
				i, errUnsupportedFormat)
		}
		img, err := jpeg.Decode(bytes.NewReader(data[i : j+2]))
		if err != nil {
			return nil, nil, fmt.Errorf("softmedia: MJPEG 第 %d 帧解码失败: %w (%w)",
				len(frames)+1, err, errUnsupportedFormat)
		}
		frames = append(frames, toRGBA(img))
		i = j + 2
	}
	if len(frames) == 0 {
		return nil, nil, fmt.Errorf("softmedia: MJPEG 流里没有帧 (%w)", errUnsupportedFormat)
	}
	d := time.Second / mjpegFPS
	delays := make([]time.Duration, len(frames))
	for i := range delays {
		delays[i] = d
	}
	return frames, delays, nil
}

// decodeGIF 解 GIF 并按 disposal 规则合成出**全帧**序列。
//
// GIF 的帧可以是"局部帧" (只含变化区域), 直接拿来画会缺内容; 三味 disposal
// (0/1 保留、2 清为背景、3 回退到上一帧) 必须自己实现 —— 这就是整个函数
// 存在的理由。
func decodeGIF(data []byte) ([]*image.RGBA, []time.Duration, error) {
	g, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		// 解码失败 = 这个文件本身坏了 (重试不会好) → 算格式错误, 进缓存
		return nil, nil, fmt.Errorf("softmedia: GIF 解码失败: %w (%w)", err, errUnsupportedFormat)
	}
	if len(g.Image) == 0 {
		return nil, nil, fmt.Errorf("softmedia: GIF 没有帧 (%w)", errUnsupportedFormat)
	}

	// 画布尺寸取第一帧的逻辑屏幕 (GIF 的 frame 可能比它小)
	w, h := g.Config.Width, g.Config.Height
	if w <= 0 || h <= 0 {
		w, h = g.Image[0].Bounds().Dx(), g.Image[0].Bounds().Dy()
	}
	canvas := image.NewRGBA(image.Rect(0, 0, w, h)) // 全透明起步
	var prev *image.RGBA                                  // disposal 3 的回退快照

	frames := make([]*image.RGBA, 0, len(g.Image))
	delays := make([]time.Duration, 0, len(g.Image))
	for i, pf := range g.Image {
		if g.Disposal[i] == gif.DisposalPrevious {
			if prev == nil {
				prev = image.NewRGBA(canvas.Bounds())
			}
			copy(prev.Pix, canvas.Pix)
		}
		// 把调色板帧画进画布 (带 alpha: 调色板的透明项保持透明)
		drawPaletted(canvas, pf)

		snap := image.NewRGBA(canvas.Bounds())
		copy(snap.Pix, canvas.Pix)
		frames = append(frames, snap)

		d := time.Duration(g.Delay[i]) * 10 * time.Millisecond
		if d <= 0 {
			d = 100 * time.Millisecond // 0 延迟按 100ms (各浏览器的共同取值)
		}
		delays = append(delays, d)

		switch g.Disposal[i] {
		case gif.DisposalBackground:
			r := pf.Bounds()
			clearRect(canvas, r)
		case gif.DisposalPrevious:
			copy(canvas.Pix, prev.Pix)
		}
		// DisposalNone / DisposalUnspecified: 保留
	}
	return frames, delays, nil
}

// drawPaletted 把调色板帧画进 RGBA 画布 (帧内透明像素不动画布)。
func drawPaletted(dst *image.RGBA, src *image.Paletted) {
	b := src.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			ci := src.ColorIndexAt(x, y)
			if int(ci) >= len(src.Palette) {
				continue
			}
			c := src.Palette[ci]
			if _, _, _, a := c.RGBA(); a == 0 {
				continue // 透明: 保留画布原样
			}
			dst.Set(x, y, c)
		}
	}
}

func clearRect(img *image.RGBA, r image.Rectangle) {
	r = r.Intersect(img.Bounds())
	if r.Empty() {
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, color.RGBA{})
		}
	}
}

// toRGBA 把任意 image 转成 *image.RGBA (已经是的则原样返回)。
func toRGBA(src image.Image) *image.RGBA {
	if r, ok := src.(*image.RGBA); ok {
		return r
	}
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			dst.Set(x-b.Min.X, y-b.Min.Y, src.At(x, y))
		}
	}
	return dst
}

// le16 / le32 是小端读取 (RIFF 全小端, 放这里给 wav.go 用)。
func le16(b []byte) uint16      { return binary.LittleEndian.Uint16(b) }
func le32(b []byte) uint32      { return binary.LittleEndian.Uint32(b) }