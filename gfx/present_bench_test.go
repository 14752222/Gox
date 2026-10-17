//go:build linux

package gfx_test

// ===== 4K 呈现路径基准: CPU blit (x11 PutImage) =====
//
// 回答验收指标里唯一没有数字的那条: 「4K 窗口下 ShowRegions 的 CPU 占用」。
// GPU 侧的同口径基准在 gfx/gpu/present_bench_test.go (需 -tags gpu),
// 一把跑的脚本是 scripts/bench-present-4k.sh。
//
// 口径 (与 vlist 基准同一纪律: 趋势可移植, 绝对值随机器):
//   - 只量「上屏一跳」: 帧缓冲预先填好, 光栅化两条路径相同, 不计入。
//   - 主指标是 **应用进程 CPU 时间** (getrusage 前后差, 含全部线程) ——
//     「CPU 占用」问的是 Gox 自己烧了多少; 框架的 ns/op (墙钟) 作副指标,
//     它含 X 服务器往返与驱动排队, 波动更大。
//   - 全帧 = 最坏情况 (33MB 过 socket); 脏矩形 = 典型局部重绘。
//     x11 后端每区都要 RGBAToXZPixmap 逐像素换字节序 + Sync 往返,
//     所以脏矩形的收益在这里同样显著。
//
// 需要 DISPLAY (Xvfb): 屏幕至少 3840x2160, 否则建窗失败自动 skip:
//   xvfb-run -a -s "-screen 0 3840x2160x24" \
//     go test ./gfx -run '^$' -bench BenchmarkPresent4K -benchtime 20x
//
// 两个包结构上的坑 (都踩过):
//   1. 本文件必须在**外部测试包** gfx_test: 包内测试 (package gfx) 导入
//      gfx/x11 会形成 gfx → gfx/x11 → gfx 的导入循环。
//   2. gfx 包自己不导入任何平台后端 (init 副作用由使用方决定), 所以必须
//      显式 import _ gfx/x11, 否则 DefaultFactory() 为 nil 整体 skip ——
//      第一版漏了它, 输出一个 "ok 0.005s" 一行基准都没有, 看着像通过。

import (
	"image"
	"math/rand"
	"os"
	"syscall"
	"testing"

	"github.com/14752222/Gox/gfx"
	// 注册 x11 工厂 (init 副作用; 不导入则默认后端为 nil, 基准全 skip)
	_ "github.com/14752222/Gox/gfx/x11"
)

const (
	bench4KW = 3840
	bench4KH = 2160
	// 典型 UI 脏矩形: 一次局部重绘覆盖的区域量级 (滑块/按钮/文本行)。
	benchDirtyW = 512
	benchDirtyH = 512
)

// bench4KImg 造一张 4K 帧缓冲。均匀噪声而非全零: 全零/全常量缓冲可能命中
// X 服务器或驱动的同色块快路径, 量出来的不是真实帧的熵。
func bench4KImg() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, bench4KW, bench4KH))
	r := rand.New(rand.NewSource(42))
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	return img
}

// benchDirtyRect 帧缓冲中部一块脏区 (避开 0,0: 某些实现对原点有快路径)。
func benchDirtyRect() image.Rectangle {
	x := (bench4KW - benchDirtyW) / 2
	y := (bench4KH - benchDirtyH) / 2
	return image.Rect(x, y, x+benchDirtyW, y+benchDirtyH)
}

// rusageCPU 取本进程 (含所有线程) 累计 CPU 时间, 纳秒。
func rusageCPU() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}

// benchX11Surface 建真 x11 窗口; 任何前置条件不满足都 skip (带原因)。
func benchX11Surface(b *testing.B) gfx.Surface {
	if os.Getenv("DISPLAY") == "" {
		b.Skip("需要 DISPLAY: xvfb-run -a -s '-screen 0 3840x2160x24' ...")
	}
	f := gfx.DefaultFactory()
	if f == nil {
		b.Skip("无平台窗口后端 (gfx/x11 未注册?)")
	}
	s, err := f.Create(gfx.WindowConfig{Title: "gox-bench", Width: bench4KW, Height: bench4KH})
	if err != nil {
		b.Skipf("建 4K 窗口失败 (Xvfb 屏幕至少 3840x2160): %v", err)
	}
	return s
}

func BenchmarkPresent4K_CPU_X11(b *testing.B) {
	s := benchX11Surface(b)
	img := bench4KImg()
	b.ReportMetric(float64(len(img.Pix)), "framebuf-B")

	s.ShowRegions(img, nil) // 预热: 建窗后的首次调用含 X 侧分配, 不计时
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, nil) // 全帧: 33MB 逐像素换序 + PutImage + Sync
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}

func BenchmarkPresent4K_CPU_X11_Dirty(b *testing.B) {
	s := benchX11Surface(b)
	img := bench4KImg()
	dirty := []image.Rectangle{benchDirtyRect()}

	s.ShowRegions(img, nil) // 先铺全帧, 之后只重绘脏区 (与真实局部重绘同构)
	s.ShowRegions(img, dirty)
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, dirty)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}
