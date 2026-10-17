//go:build linux

package softmedia

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
)

// ===== softmedia 测试 =====
//
// 分四层, 从确定性到时序:
//  1. 纯函数: fitRect 几何 / blitScaled 像素 (逐像素断言);
//  2. 编解码: GIF disposal 合成 / MJPEG 切帧 / WAV 各格式往返 + 格式拒绝;
//  3. 播放器: attach → composite → play/pause/seek 的**时序**行为
//     (用 30ms 级的 GIF 与 0.2s 的 WAV, 断言放宽到几百 ms);
//  4. 包装器: Wrap / ShowRegions 转发 / WaitEvents(false) 停机。
//
// 音频输出层**不挑环境**: 有 PulseAudio 就走真 sink, 没有 (无头 CI) 就走
// null sink —— 两条路的时钟与状态机完全同构, 所以断言不依赖环境。

// =====================================================================
// 第 1 层: 纯函数
// =====================================================================

// TestFitRect 与 gfx.videoFitRect 同一张几何表 (两份实现, 各钉各的)。
func TestFitRect(t *testing.T) {
	box := gfx.Rect{X: 10, Y: 20, W: 100, H: 100}
	cases := []struct {
		name   string
		sw, sh int
		mode   string
		want   gfx.Rect
	}{
		{"contain 宽图: 上下留边", 200, 100, "contain", gfx.Rect{X: 10, Y: 45, W: 100, H: 50}},
		{"contain 高图: 左右留边", 100, 200, "contain", gfx.Rect{X: 35, Y: 20, W: 50, H: 100}},
		{"cover 宽图: 溢出左右", 200, 100, "cover", gfx.Rect{X: -40, Y: 20, W: 200, H: 100}},
		{"cover 高图: 溢出上下", 100, 200, "cover", gfx.Rect{X: 10, Y: -30, W: 100, H: 200}},
		{"fill: 铺满整盒", 200, 100, "fill", box},
		{"方图进方盒: 恰好占满", 100, 100, "contain", box},
		{"不认识的模式按 contain", 200, 100, "bogus", gfx.Rect{X: 10, Y: 45, W: 100, H: 50}},
	}
	for _, c := range cases {
		got, ok := fitRect(box, c.sw, c.sh, c.mode)
		if !ok {
			t.Fatalf("%s: 不该退化", c.name)
		}
		if got != c.want {
			t.Fatalf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	for _, c := range []struct {
		name   string
		box    gfx.Rect
		sw, sh int
	}{
		{"盒宽 0", gfx.Rect{W: 0, H: 50}, 10, 10},
		{"盒高 0", gfx.Rect{W: 50, H: 0}, 10, 10},
		{"源 0", gfx.Rect{W: 50, H: 50}, 0, 10},
	} {
		if _, ok := fitRect(c.box, c.sw, c.sh, "contain"); ok {
			t.Fatalf("%s: 应返回 ok=false", c.name)
		}
	}
}

// TestBlitScaled 最近邻缩放: 2x2 四象限源进 4x4 盒, 四角必须分别对应源四角
// (同时抓住"缩放比反了"与"行列对调"两种错位); 再验越界裁剪不 panic。
func TestBlitScaled(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2, 2))
	src.SetRGBA(0, 0, color.RGBA{R: 255, A: 255}) // 左上红
	src.SetRGBA(1, 0, color.RGBA{G: 255, A: 255}) // 右上绿
	src.SetRGBA(0, 1, color.RGBA{B: 255, A: 255}) // 左下蓝
	src.SetRGBA(1, 1, color.RGBA{R: 255, G: 255, A: 255}) // 右下黄

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	blitScaled(img, gfx.Rect{X: 2, Y: 2, W: 4, H: 4}, src)
	assertRGBA(t, img, 2, 2, color.RGBA{R: 255, A: 255}, "放大后左上")
	assertRGBA(t, img, 5, 2, color.RGBA{G: 255, A: 255}, "放大后右上")
	assertRGBA(t, img, 2, 5, color.RGBA{B: 255, A: 255}, "放大后左下")
	assertRGBA(t, img, 5, 5, color.RGBA{R: 255, G: 255, A: 255}, "放大后右下")

	// 越界矩形: 裁掉画布外的部分, 不 panic 也不写界外
	blitScaled(img, gfx.Rect{X: 6, Y: 6, W: 10, H: 10}, src) // 右下角越界
	blitScaled(img, gfx.Rect{X: -4, Y: -4, W: 4, H: 4}, src) // 完全在界外
	blitScaled(img, gfx.Rect{X: 0, Y: 0, W: 0, H: 0}, src)   // 退化
}

func assertRGBA(t *testing.T, img *image.RGBA, x, y int, want color.RGBA, what string) {
	t.Helper()
	if got := img.RGBAAt(x, y); got != want {
		t.Fatalf("%s: (%d,%d) = %v, want %v", what, x, y, got, want)
	}
}

// =====================================================================
// 第 2 层: 编解码
// =====================================================================

// mkTestGIF 造一个 4x4 三帧 GIF, 专门钉 disposal 语义:
//
//	帧0: 全红, disposal none
//	帧1: 左上 2x2 绿, disposal previous (3) —— 播完回退到帧0的画布
//	帧2: 右下 2x2 蓝, disposal none
//
// 于是合成结果必须是:
//
//	帧0: 全红
//	帧1: 左上绿 + 其余红
//	帧2: 左上**红**(回退生效, 不是绿) + 右下蓝 + 其余红
//
// 第 3 帧左上角那一格是整条 disposal 链的判据。
func mkTestGIF(t *testing.T) []byte {
	t.Helper()
	pal := color.Palette{
		color.RGBA{R: 255, A: 255},
		color.RGBA{G: 255, A: 255},
		color.RGBA{B: 255, A: 255},
	}
	frame := func(fill int) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 4, 4), pal)
		for i := range p.Pix {
			p.Pix[i] = uint8(fill)
		}
		return p
	}
	partial := func(x0, y0, fill int) *image.Paletted {
		p := image.NewPaletted(image.Rect(0, 0, 4, 4), pal)
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				if x >= x0 && x < x0+2 && y >= y0 && y < y0+2 {
					p.SetColorIndex(x, y, uint8(fill))
				} else {
					p.SetColorIndex(x, y, 0)
				}
			}
		}
		return p
	}
	g := &gif.GIF{
		Image:     []*image.Paletted{frame(0), partial(0, 0, 1), partial(2, 2, 2)},
		Delay:     []int{10, 10, 10},
		Disposal:  []byte{gif.DisposalNone, gif.DisposalPrevious, gif.DisposalNone},
		LoopCount: 0,
	}
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("EncodeAll: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeGIFDisposal(t *testing.T) {
	data := mkTestGIF(t)
	path := filepath.Join(t.TempDir(), "t.gif")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	frames, delays, err := decodeVideo(path)
	if err != nil {
		t.Fatalf("decodeVideo(gif): %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("应有 3 帧, 实际 %d", len(frames))
	}
	for i, d := range delays {
		if d != 100*time.Millisecond {
			t.Fatalf("第 %d 帧延迟 = %v, want 100ms", i, d)
		}
	}
	red := color.RGBA{R: 255, A: 255}
	green := color.RGBA{G: 255, A: 255}
	blue := color.RGBA{B: 255, A: 255}

	// 帧0: 全红
	if c := frames[0].RGBAAt(3, 3); c != red {
		t.Fatalf("帧0 (3,3) = %v, want red", c)
	}
	// 帧1: 左上绿, 其余红
	if c := frames[1].RGBAAt(0, 0); c != green {
		t.Fatalf("帧1 (0,0) = %v, want green", c)
	}
	if c := frames[1].RGBAAt(3, 3); c != red {
		t.Fatalf("帧1 (3,3) = %v, want red", c)
	}
	// 帧2: 左上回退成红 (disposal previous 生效!), 右下蓝
	if c := frames[2].RGBAAt(0, 0); c != red {
		t.Fatalf("帧2 (0,0) = %v, want red (回退到 previous 生效)", c)
	}
	if c := frames[2].RGBAAt(3, 3); c != blue {
		t.Fatalf("帧2 (3,3) = %v, want blue", c)
	}
}

// mkTestMJPEG 造两帧裸 JPEG 流 (红 / 蓝, 4x4)。
func mkTestMJPEG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	for _, c := range []color.RGBA{{R: 255, A: 255}, {B: 255, A: 255}} {
		img := image.NewRGBA(image.Rect(0, 0, 4, 4))
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				img.SetRGBA(x, y, c)
			}
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 100}); err != nil {
			t.Fatalf("jpeg.Encode: %v", err)
		}
	}
	return buf.Bytes()
}

func TestDecodeMJPEG(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.mjpeg")
	if err := os.WriteFile(path, mkTestMJPEG(t), 0o644); err != nil {
		t.Fatal(err)
	}
	frames, delays, err := decodeVideo(path)
	if err != nil {
		t.Fatalf("decodeVideo(mjpeg): %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("应有 2 帧, 实际 %d", len(frames))
	}
	if delays[0] != time.Second/25 {
		t.Fatalf("MJPEG 帧率该是 25fps, 实际 %v", delays[0])
	}
	// JPEG 是有损的, 断言"主色调"而不是精确值
	if c := frames[0].RGBAAt(1, 1); c.R < 200 || c.G > 60 || c.B > 60 {
		t.Fatalf("第 1 帧该是红的, 实际 %v", c)
	}
	if c := frames[1].RGBAAt(1, 1); c.B < 200 || c.R > 60 || c.G > 60 {
		t.Fatalf("第 2 帧该是蓝的, 实际 %v", c)
	}
}

// mkTestWAV 按参数拼一个 RIFF/WAVE (测试自己造, 不依赖任何 fixture)。
func mkTestWAV(t *testing.T, format uint16, bits, channels, rate int, samples []float32) []byte {
	t.Helper()
	var data []byte
	switch {
	case format == 1 && bits == 16:
		for _, s := range samples {
			v := int16(s * 32767)
			data = append(data, byte(v), byte(uint16(v)>>8))
		}
	case format == 1 && bits == 24:
		for _, s := range samples {
			v := int32(s * 8388607)
			data = append(data, byte(v), byte(v>>8), byte(v>>16))
		}
	case format == 3 && bits == 32:
		for _, s := range samples {
			b := math.Float32bits(s)
			data = append(data, byte(b), byte(b>>8), byte(b>>16), byte(b>>24))
		}
	default:
		t.Fatalf("mkTestWAV: 不支持的组合 format=%d bits=%d", format, bits)
	}
	byteRate := rate * channels * bits / 8
	blockAlign := channels * bits / 8
	fmtBody := make([]byte, 16)
	putLe16(fmtBody[0:], format)
	putLe16(fmtBody[2:], uint16(channels))
	putLe32(fmtBody[4:], uint32(rate))
	putLe32(fmtBody[8:], uint32(byteRate))
	putLe16(fmtBody[12:], uint16(blockAlign))
	putLe16(fmtBody[14:], uint16(bits))

	var chunks []byte
	chunks = append(chunks, []byte("fmt ")...)
	chunks = append(chunks, le32b(uint32(len(fmtBody)))...)
	chunks = append(chunks, fmtBody...)
	chunks = append(chunks, []byte("data")...)
	chunks = append(chunks, le32b(uint32(len(data)))...)
	chunks = append(chunks, data...)

	out := append([]byte("RIFF"), le32b(uint32(4+len(chunks)))...)
	out = append(out, []byte("WAVE")...)
	return append(out, chunks...)
}

func putLe16(b []byte, v uint16) { b[0] = byte(v); b[1] = byte(v >> 8) }
func putLe32(b []byte, v uint32) {
	b[0] = byte(v)
	b[1] = byte(v >> 8)
	b[2] = byte(v >> 16)
	b[3] = byte(v >> 24)
}
func le32b(v uint32) []byte { b := make([]byte, 4); putLe32(b, v); return b }

func TestDecodeWAVFormats(t *testing.T) {
	// 双声道 8kHz: [0.5, -0.5, 0.25, -0.25] (L/R 交错)
	samples := []float32{0.5, -0.5, 0.25, -0.25}
	cases := []struct {
		name   string
		format uint16
		bits   int
		tol    float32 // 量化容差 (8/16/24 位精度不同)
	}{
		{"16-bit PCM", 1, 16, 1.0 / 32768},
		{"24-bit PCM", 1, 24, 1.0 / 8388608},
		{"32-bit float", 3, 32, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "t.wav")
			if err := os.WriteFile(path, mkTestWAV(t, c.format, c.bits, 2, 8000, samples), 0o644); err != nil {
				t.Fatal(err)
			}
			clip, err := decodeWAV(path)
			if err != nil {
				t.Fatalf("decodeWAV: %v", err)
			}
			if clip.Channels != 2 || clip.Rate != 8000 {
				t.Fatalf("channels/rate = %d/%d, want 2/8000", clip.Channels, clip.Rate)
			}
			if len(clip.Samples) != 4 {
				t.Fatalf("样本数 = %d, want 4", len(clip.Samples))
			}
			for i, want := range samples {
				got := clip.Samples[i]
				if math.Abs(float64(got-want)) > float64(c.tol)+1e-6 {
					t.Fatalf("样本 %d = %v, want %v (±%v)", i, got, want, c.tol)
				}
			}
			if secs := clip.Seconds(); math.Abs(secs-2.0/8000) > 1e-9 {
				t.Fatalf("时长 = %v, want %v", secs, 2.0/8000)
			}
		})
	}
}

// TestDecodeWAVRejects 非 WAV / 不支持的编码必须报 errUnsupportedFormat
// (调用方据此进 rejected 缓存, 不每帧重试)。
func TestDecodeWAVRejects(t *testing.T) {
	dir := t.TempDir()

	bad := filepath.Join(dir, "bad.wav")
	if err := os.WriteFile(bad, []byte("not a riff file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWAV(bad); !isFormatError(err) {
		t.Fatalf("非 RIFF 该报格式错误, 实际 %v", err)
	}

	// ADPCM (format=2): fmt 块认得出, 但 pcmToFloat 不支持 → 格式错误
	fmtBody := make([]byte, 16)
	putLe16(fmtBody[0:], 2) // ADPCM
	putLe16(fmtBody[2:], 1)
	putLe32(fmtBody[4:], 8000)
	putLe16(fmtBody[14:], 4)
	var chunks []byte
	chunks = append(chunks, []byte("fmt ")...)
	chunks = append(chunks, le32b(16)...)
	chunks = append(chunks, fmtBody...)
	chunks = append(chunks, []byte("data")...)
	chunks = append(chunks, le32b(4)...)
	chunks = append(chunks, 0, 0, 0, 0)
	out := append([]byte("RIFF"), le32b(uint32(4+len(chunks)))...)
	out = append(out, []byte("WAVE")...)
	out = append(out, chunks...)
	adpcm := filepath.Join(dir, "adpcm.wav")
	if err := os.WriteFile(adpcm, out, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeWAV(adpcm); !isFormatError(err) {
		t.Fatalf("ADPCM 该报格式错误, 实际 %v", err)
	}

	// mp4 (扩展名直接拒, 连盘都不读)
	if _, _, err := decodeVideo(filepath.Join(dir, "x.mp4")); !isFormatError(err) {
		t.Fatalf("mp4 该报格式错误, 实际 %v", err)
	}
}

// =====================================================================
// 第 3 层: 播放器时序
// =====================================================================

// writeTemp 把字节写进临时目录并返回路径。
func writeTemp(t *testing.T, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// videoStateForTest 读播放器当前帧下标 (测试专用)。
func (p *videoPlayer) curForTest() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

func (p *videoPlayer) playingForTest() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// TestVideoPlayerClock 播/停/跳: 用 30ms/帧的 GIF, 断言放宽到百 ms 级。
func TestVideoPlayerClock(t *testing.T) {
	gifPath := writeTemp(t, "c.gif", mkTestGIF(t))
	frames, delays, err := decodeVideo(gifPath)
	if err != nil {
		t.Fatal(err)
	}
	p := newVideoPlayer(gfx.Rect{X: 0, Y: 0, W: 40, H: 40}, gifPath,
		gfx.VideoSurfaceOptions{Fit: "contain"}, frames, delays)
	defer p.stop()
	p.start()

	if c := p.curForTest(); c != 0 {
		t.Fatalf("未播放时该停在第 0 帧, 实际 %d", c)
	}
	// 暂停中过一会儿也不该动
	time.Sleep(80 * time.Millisecond)
	if c := p.curForTest(); c != 0 {
		t.Fatalf("未 play 不该进帧, 实际 %d", c)
	}

	p.command(gfx.VideoCmdPlay, 0)
	if !p.playingForTest() {
		t.Fatalf("play 之后 playing 该为 true")
	}
	time.Sleep(200 * time.Millisecond)
	if c := p.curForTest(); c == 0 {
		t.Fatalf("播放 200ms (3 帧 ×100ms) 后该进过帧, 实际还在 %d", c)
	}

	// 播完 (3 帧 100ms, 不循环) → 停在最后一帧 + 停机
	time.Sleep(500 * time.Millisecond)
	if p.playingForTest() {
		t.Fatalf("不循环播放该自动停机")
	}
	if c := p.curForTest(); c != len(frames)-1 {
		t.Fatalf("播完该停在最后一帧 (%d), 实际 %d", len(frames)-1, c)
	}

	// seek 回第 0 帧再 play → 重新动
	p.command(gfx.VideoCmdSeek, 0)
	time.Sleep(30 * time.Millisecond)
	if c := p.curForTest(); c != 0 {
		t.Fatalf("seek 0 后该回到第 0 帧, 实际 %d", c)
	}
	p.command(gfx.VideoCmdPlay, 0)
	p.command(gfx.VideoCmdPause, 0)
	time.Sleep(120 * time.Millisecond)
	// pause 在 play 之后立刻到达: 不该进帧 (允许时序上的 1 帧抖动)
	if c := p.curForTest(); c > 1 {
		t.Fatalf("pause 后不该继续播, 实际到第 %d 帧", c)
	}
}

// TestVideoPlayerComposite 播放器把当前帧合成进帧缓冲 (contain 落位)。
func TestVideoPlayerComposite(t *testing.T) {
	gifPath := writeTemp(t, "c.gif", mkTestGIF(t))
	frames, delays, err := decodeVideo(gifPath)
	if err != nil {
		t.Fatal(err)
	}
	p := newVideoPlayer(gfx.Rect{X: 10, Y: 20, W: 100, H: 50}, gifPath,
		gfx.VideoSurfaceOptions{Fit: "contain"}, frames, delays)
	defer p.stop()

	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	r, ok := p.compositeInto(img)
	if !ok {
		t.Fatalf("compositeInto 不该失败")
	}
	if r != (gfx.Rect{X: 10, Y: 20, W: 100, H: 50}) {
		t.Fatalf("合成矩形 = %v, want 节点盒", r)
	}
	// 4x4 源 contain 进 100x50 盒: 按高缩放 → 50x50 居中 (x∈[35,85)),
	// 左右各留 25px。留边**不归 compositeInto 画** —— 那是 drawNode 刚画的
	// 封面/底色的职责, 播放器只盖"画面该在的那一块"。
	if c := img.RGBAAt(40, 25); c != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("contain 画面左上该是帧0的红, 实际 %v", c)
	}
	if c := img.RGBAAt(60, 45); c != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("画面中心该是帧0的红, 实际 %v", c)
	}

	// fit=fill: 铺满整盒
	p.fit = "fill"
	img2 := image.NewRGBA(image.Rect(0, 0, 200, 100))
	p.compositeInto(img2)
	if c := img2.RGBAAt(11, 21); c != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("fill 模式左上角该是画面 (无留边), 实际 %v", c)
	}

	// 盒子退化: 不合成
	p.setRect(gfx.Rect{})
	if _, ok := p.compositeInto(img2); ok {
		t.Fatalf("0 尺寸盒不该合成")
	}
}

// =====================================================================
// 第 4 层: 包装器
// =====================================================================

// fakeInner 是测试用的内层 Surface (记录上屏调用)。
type fakeInner struct {
	mu      sync.Mutex
	w, h    int
	regions []image.Rectangle
	shown   int
	closed  bool
	events  chan gfx.Event
}

func newFakeInner(w, h int) *fakeInner {
	return &fakeInner{w: w, h: h, events: make(chan gfx.Event)}
}

func (f *fakeInner) Show(img *image.RGBA) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shown++
	f.regions = nil
}

func (f *fakeInner) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shown++
	f.regions = rects
}

func (f *fakeInner) Size() (int, int) { return f.w, f.h }

func (f *fakeInner) WaitEvents(maxWait time.Duration) bool {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	return !closed
}

func (f *fakeInner) Events() <-chan gfx.Event { return f.events }

func (f *fakeInner) shownCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown
}

func (f *fakeInner) lastRegions() []image.Rectangle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.regions
}

// TestWrapAndVideoLifecycle 端到端: 包装一个假窗口, 挂 GIF, 验证
// (1) 可选能力命中 (VideoInlineSupported); (2) 不支持的格式被拒且进缓存
// (不重复读文件); (3) ShowRegions 把视频帧合成进去并转发 inner;
// (4) Detach 后不再合成。
func TestWrapAndVideoLifecycle(t *testing.T) {
	inner := newFakeInner(200, 100)
	wrapped, ok := Wrap(inner)
	if !ok {
		t.Fatalf("Wrap 该包装")
	}
	m, ok := wrapped.(*mediaSurface)
	if !ok {
		t.Fatalf("Wrap 该返回 mediaSurface")
	}
	if !m.VideoInlineSupported() {
		t.Fatalf("softmedia 的 VideoInlineSupported 该恒真")
	}

	// 不支持的格式: 报错 + 缓存。缓存要用**读盘之后**才失败的用例证明:
	// 一个坏 .gif —— 第一次读盘失败, 删掉文件, 第二次必须还拿到同一个错
	// (否则说明它又去读盘了, 缓存没生效)。
	if err := m.VideoSurfaceAttach(1, gfx.Rect{W: 40, H: 40}, "x.mp4", gfx.VideoSurfaceOptions{}); !isFormatError(err) {
		t.Fatalf("mp4 该被拒, 实际 %v", err)
	}
	corrupt := writeTemp(t, "bad.gif", []byte("GIF89a 但不是真的 GIF"))
	err1 := m.VideoSurfaceAttach(2, gfx.Rect{W: 40, H: 40}, corrupt, gfx.VideoSurfaceOptions{})
	if !isFormatError(err1) {
		t.Fatalf("坏 gif 该被拒, 实际 %v", err1)
	}
	os.Remove(corrupt)
	err2 := m.VideoSurfaceAttach(3, gfx.Rect{W: 40, H: 40}, corrupt, gfx.VideoSurfaceOptions{})
	if err2 == nil || err2.Error() != err1.Error() {
		t.Fatalf("第二次该拿缓存的错 (%v), 实际 %v", err1, err2)
	}

	// 真 GIF: 挂上, 合成进帧缓冲
	gifPath := writeTemp(t, "w.gif", mkTestGIF(t))
	// 句柄由内核分配 (进程内唯一), 这里手编一个 —— 包内测试直接对后端说话,
	// 唯一性由内核那侧保证。
	const gifID uint64 = 41
	if err := m.VideoSurfaceAttach(gifID, gfx.Rect{X: 10, Y: 10, W: 80, H: 40}, gifPath, gfx.VideoSurfaceOptions{Fit: "fill"}); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	id := gifID

	img := image.NewRGBA(image.Rect(0, 0, 200, 100))
	m.ShowRegions(img, []image.Rectangle{{Min: image.Point{X: 0, Y: 0}, Max: image.Point{X: 200, Y: 100}}})
	if got := inner.shownCount(); got != 1 {
		t.Fatalf("inner 该收到一次上屏, 实际 %d", got)
	}
	if c := img.RGBAAt(11, 11); c != (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("视频帧该合成进 img (fill 模式左上), 实际 %v", c)
	}

	// rects=nil (整帧) 必须保持 nil: 并进去等于把整帧 blit 降级成局部
	m.ShowRegions(img, nil)
	if r := inner.lastRegions(); r != nil {
		t.Fatalf("整帧上屏的 regions 该保持 nil, 实际 %v", r)
	}

	// Detach: 之后 ShowRegions 不再合成那个框
	if err := m.VideoSurfaceDetach(id); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	img2 := image.NewRGBA(image.Rect(0, 0, 200, 100))
	m.ShowRegions(img2, nil)
	if c := img2.RGBAAt(11, 11); c == (color.RGBA{R: 255, A: 255}) {
		t.Fatalf("Detach 后不该再合成视频帧")
	}
}

// TestWrapStopsOnWindowClose 窗口关闭 (WaitEvents 返回 false) 必须停机:
// 内核的 a.close() 不销毁渲染树, 不会来 Detach —— 不停机的话音乐会一路
// 放到进程退出。
func TestWrapStopsOnWindowClose(t *testing.T) {
	inner := newFakeInner(200, 100)
	wrapped, ok := Wrap(inner)
	if !ok {
		t.Fatalf("Wrap 该包装")
	}
	m, ok := wrapped.(*mediaSurface)
	if !ok {
		t.Fatalf("Wrap 该返回 mediaSurface")
	}

	// 先挂一个"永远播不完"的: loop GIF + play
	gifPath := writeTemp(t, "loop.gif", mkTestGIF(t))
	frames, delays, err := decodeVideo(gifPath)
	if err != nil {
		t.Fatal(err)
	}
	p := newVideoPlayer(gfx.Rect{W: 40, H: 40}, gifPath, gfx.VideoSurfaceOptions{}, frames, delays)
	p.loop = true
	p.id = 1
	m.videos[1] = p
	p.start()
	p.command(gfx.VideoCmdPlay, 0)

	time.Sleep(50 * time.Millisecond)
	if !p.playingForTest() {
		t.Fatalf("该在播放")
	}

	inner.mu.Lock()
	inner.closed = true
	inner.mu.Unlock()
	if m.WaitEvents(10 * time.Millisecond) {
		t.Fatalf("closed 的 inner 该返回 false")
	}

	// 停机后: playing 不再变化 (时钟 goroutine 退出), 句柄表清空
	time.Sleep(120 * time.Millisecond)
	p.mu.Lock()
	stopped := p.stopped
	p.mu.Unlock()
	if !stopped {
		t.Fatalf("窗口关闭后播放器该被标记停机")
	}
	m.mu.Lock()
	n := len(m.videos)
	m.mu.Unlock()
	if n != 0 {
		t.Fatalf("停机后句柄表该清空, 实际 %d", n)
	}
}

// TestWrapNilInner nil inner 不包装 (调用方继续用原 Surface)。
func TestWrapNilInner(t *testing.T) {
	if s, ok := Wrap(nil); ok || s != nil {
		t.Fatalf("nil inner 该返回 ok=false, 实际 %v/%v", s, ok)
	}
}

// =====================================================================
// 音频播放器时序
// =====================================================================

func (p *audioPlayer) posForTest() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pos
}

func (p *audioPlayer) playingForTest() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

// TestAudioPlayerClock 0.2s 的单声道 WAV: play → 走到末尾 → 自动 ended 停机;
// loop 模式下不停机。null sink 与真 sink 同一条时钟, 所以无头环境也成立。
func TestAudioPlayerClock(t *testing.T) {
	rate := 8000
	n := rate / 5 // 0.2s
	samples := make([]float32, n)
	for i := range samples {
		samples[i] = float32(math.Sin(2 * math.Pi * 440 * float64(i) / float64(rate)))
	}
	wavPath := writeTemp(t, "t.wav", mkTestWAV(t, 1, 16, 1, rate, samples))
	clip, err := decodeWAV(wavPath)
	if err != nil {
		t.Fatal(err)
	}

	// 不循环: 播完自动停
	p := newAudioPlayer(wavPath, gfx.AudioPlayOptions{Volume: 1}, clip)
	defer p.stop()
	p.start()
	if p.playingForTest() {
		t.Fatalf("未 play 不该在播")
	}
	p.command(gfx.AudioCmdPlay, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !p.playingForTest() && p.posForTest() >= len(clip.Samples) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p.playingForTest() {
		t.Fatalf("0.2s 的片段播完该自动停机 (pos=%d/%d)", p.posForTest(), len(clip.Samples))
	}
	if pos := p.posForTest(); pos < len(clip.Samples) {
		t.Fatalf("播完 pos 该到末尾 %d, 实际 %d", len(clip.Samples), pos)
	}

	// 循环: 播完继续 (pos 回绕, 不停)
	p2 := newAudioPlayer(wavPath, gfx.AudioPlayOptions{Loop: true, Volume: 1}, clip)
	defer p2.stop()
	p2.start()
	p2.command(gfx.AudioCmdPlay, 0)
	time.Sleep(400 * time.Millisecond)
	if !p2.playingForTest() {
		t.Fatalf("loop 模式不该停机")
	}

	// seek 到末尾附近再 play: 很快结束
	p3 := newAudioPlayer(wavPath, gfx.AudioPlayOptions{Volume: 1}, clip)
	defer p3.stop()
	p3.start()
	p3.command(gfx.AudioCmdSeek, 0.15)
	time.Sleep(50 * time.Millisecond)
	if pos := p3.posForTest(); pos == 0 {
		t.Fatalf("seek 后 pos 该动, 实际 %d", pos)
	}
}
