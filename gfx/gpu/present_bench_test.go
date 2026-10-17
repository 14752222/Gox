//go:build gpu && linux

package gpu

// ===== 4K 呈现路径基准: GPU 合成 (gfx/gpu 后端) =====
//
// 与 gfx/present_bench_test.go 的 CPU 路径同口径 (4K 帧, 只量上屏一跳,
// 主指标应用进程 CPU 时间), 脚本 scripts/bench-present-4k.sh 一把跑两边。
//
// **为什么 GPU 路径要分两个基准** (这是本文件存在的全部理由):
//
//	wgpu 的 Submit 是异步的 —— 应用线程的 CPU 只花在「编码 + 上传 + 提交」,
//	真正的合成由 GPU (或软件后端的执行体) 完成。而沙箱/CI 里没有硬件适配器,
//	Device() 会拿 ForceFallbackAdapter 的软件渲染器, 它的「GPU 工作」由
//	**本进程**完成, 且只在 Poll 时推进 (见 Readback 里那句 Poll 注释)。
//
//于是同一个 ShowRegions 有两种诚实口径, 混在一起量会得出误导性结论:
//
//	AppCPU  (不 Poll): 应用侧编码+提交成本。≈ 真实硬件上应用烧的 CPU
//	                    (真实 GPU 上驱动/硬件干完剩下的活, 不算应用 CPU)
//	Total   (PollWait): 含软件后端执行体的全部进程 CPU。
//	                    = 无 GPU 硬件时的兜底成本, 也是「工作没消失,
//	                    只是换了执行者」的证据
//
// 两个数一起看才能回答验收指标: 硬件上 CPU 占用降多少 = AppCPU 对比 x11;
// 没硬件时最坏多少 = Total 对比 x11 (x11 侧 X 服务器的 CPU 另算, 它是
// 独立进程, 不进本进程 rusage —— 公平, 因为 gox 确实不为它烧 CPU)。

import (
	"image"
	"math/rand"
	"syscall"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/gogpu/wgpu"
)

const (
	bench4KW    = 3840
	bench4KH    = 2160
	benchDirtyW = 512
	benchDirtyH = 512
)

func bench4KImg() *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, bench4KW, bench4KH))
	r := rand.New(rand.NewSource(42)) // 与 gfx 侧同种子: 两边帧缓冲逐字节一致
	for i := range img.Pix {
		img.Pix[i] = uint8(r.Intn(256))
	}
	return img
}

func benchDirtyRect() image.Rectangle {
	x := (bench4KW - benchDirtyW) / 2
	y := (bench4KH - benchDirtyH) / 2
	return image.Rect(x, y, x+benchDirtyW, y+benchDirtyH)
}

func rusageCPU() int64 {
	var ru syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru); err != nil {
		return 0
	}
	return ru.Utime.Nano() + ru.Stime.Nano()
}

// benchInner 是满足 gfx.Surface 的最小桩: 呈现路径 (Show/ShowRegions)
// 不碰内层, New() 只问一次 Size。事件/等待在基准里永远不会被调用。
// (不能复用 surface_test.go 的 captureSurface —— 那个文件在 gpu_test
// 外部包里, 内部测试看不到; 呈现基准又必须进内部包才能 Poll s.dev。)
type benchInner struct{ w, h int }

func (b *benchInner) Show(img *image.RGBA)                                 {}
func (b *benchInner) ShowRegions(img *image.RGBA, rects []image.Rectangle) {}
func (b *benchInner) Size() (int, int)                                     { return b.w, b.h }
func (b *benchInner) WaitEvents(maxWait time.Duration) bool                { return false }
func (b *benchInner) Events() <-chan gfx.Event                             { return nil }

// benchPresentGPU 建一个包着桩的 GPU Surface, 回报实际后端名 ——
// 数字怎么解释全凭它 (软件后端 vs 硬件适配器, 结论完全不同)。
func benchPresentGPU(b *testing.B) *Surface {
	b.Logf("GPU 后端: %s", Backend())
	s, err := New(&benchInner{bench4KW, bench4KH})
	if err != nil {
		b.Skipf("GPU 后端不可用: %v", err)
	}
	return s
}

// BenchmarkPresent4K_GPU_AppCPU 全帧, 只提交不 Poll: 应用侧 CPU 成本。
func BenchmarkPresent4K_GPU_AppCPU(b *testing.B) {
	s := benchPresentGPU(b)
	img := bench4KImg()
	b.ReportMetric(float64(len(img.Pix)), "framebuf-B")

	s.ShowRegions(img, nil) // 预热: 首次调用建纹理/管线, 不计时
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, nil)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}

// BenchmarkPresent4K_GPU_Total 全帧, 每帧 PollWait 等到后端做完: 含软件
// 渲染器执行体的进程总 CPU (无 GPU 硬件时的兜底成本)。
func BenchmarkPresent4K_GPU_Total(b *testing.B) {
	s := benchPresentGPU(b)
	img := bench4KImg()

	s.ShowRegions(img, nil)
	s.dev.Poll(wgpu.PollWait)
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, nil)
		s.dev.Poll(wgpu.PollWait)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}

// BenchmarkPresent4K_GPU_AppCPU_Dirty 512x512 脏矩形, 只提交不 Poll。
// 对应 x11 的 Dirty 基准: 两条路径都只传 1MB 而不是 33MB。
func BenchmarkPresent4K_GPU_AppCPU_Dirty(b *testing.B) {
	s := benchPresentGPU(b)
	img := bench4KImg()
	dirty := []image.Rectangle{benchDirtyRect()}

	s.ShowRegions(img, nil)
	s.ShowRegions(img, dirty)
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, dirty)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}

// BenchmarkPresent4K_GPU_Total_Dirty 脏矩形的兜底口径 (含 Poll)。
func BenchmarkPresent4K_GPU_Total_Dirty(b *testing.B) {
	s := benchPresentGPU(b)
	img := bench4KImg()
	dirty := []image.Rectangle{benchDirtyRect()}

	s.ShowRegions(img, nil)
	s.ShowRegions(img, dirty)
	s.dev.Poll(wgpu.PollWait)
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		s.ShowRegions(img, dirty)
		s.dev.Poll(wgpu.PollWait)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}

// BenchmarkPresent4K_GPU_UploadOnly 只做 ShowRegions 的第 1 步 (子纹理
// 提取 + WriteTexture), 不建 pass 不 Draw。
//
// 为什么单拆它: 软件 HAL 把光栅化**同步塞进 Draw**, AppCPU 与 Total 两个
// 口径在无 GPU 的机器上量到的是同一回事 (实测差 <1%), 分不出来。而这个
// 「提取+上传」的成本在**所有后端上都由应用线程承担** (真实 GPU 上也是
// 应用 CPU 把像素交给驱动, 之后才异步) —— 它是 GPU 路径唯一可迁移到
// 真实硬件的数字: 硬件上的应用 CPU ≈ 本项 + 少量编码/提交。
func BenchmarkPresent4K_GPU_UploadOnly(b *testing.B) {
	s := benchPresentGPU(b)
	img := bench4KImg()
	bounds := img.Bounds()

	s.ShowRegions(img, nil) // 预热 (建纹理/管线)
	s.dev.Poll(wgpu.PollWait)
	b.ResetTimer()
	c0 := rusageCPU()
	for i := 0; i < b.N; i++ {
		rowBytes := bounds.Dx() * 4
		need := rowBytes * bounds.Dy()
		if cap(s.uploadScratch) < need {
			s.uploadScratch = make([]byte, need)
		}
		sub := s.uploadScratch[:need]
		stride := img.Stride
		for y := 0; y < bounds.Dy(); y++ {
			off := y * stride
			copy(sub[y*rowBytes:], img.Pix[off:off+rowBytes])
		}
		_ = s.dev.Queue().WriteTexture(
			&wgpu.ImageCopyTexture{Texture: s.fbTex, Origin: wgpu.Origin3D{}},
			sub,
			&wgpu.ImageDataLayout{BytesPerRow: uint32(rowBytes), RowsPerImage: uint32(bounds.Dy())},
			&wgpu.Extent3D{Width: uint32(bounds.Dx()), Height: uint32(bounds.Dy()), DepthOrArrayLayers: 1},
		)
	}
	b.StopTimer()
	b.ReportMetric(float64(rusageCPU()-c0)/float64(b.N), "cpu-ns/op")
}
