//go:build gpu

package gpu_test

// GPU 呈现后端的像素级 A/B 验证。
//
// 方法: 同一个 UI 脚本挂载两遍 —— 一遍用普通捕获 Surface (CPU 路径的参照帧),
// 一遍用 gpu.Surface (GPU 合成 + 回读) —— 两帧逐像素比对。
// 同一台机器、同一份字体, 两条路径的差异只能是 GPU 合成引入的。
//
// 这对应仓库里既有的验证文化: gallery_shot_test.go 用「真实上屏的那一帧」
// 做组件回归, 这里用同一手法验证「GPU 上屏 ≡ CPU 上屏」。
//
// 运行: go test ./gfx/gpu/ -run TestGPUPresentPixelExact -v

import (
	"image"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	gpupkg "github.com/14752222/Gox/gfx/gpu"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// captureSurface 复刻 gfx 包内 fakeSurface 的最小事件/捕获行为
// (gpu 包在 gfx 包外, 不能用它的测试替身)。
type captureSurface struct {
	events  chan gfx.Event
	arrived chan struct{}
	mu      sync.Mutex
	img     *image.RGBA
	w, h    int
	shown   int
}

func newCaptureSurface(w, h int) *captureSurface {
	return &captureSurface{
		events:  make(chan gfx.Event, 16),
		arrived: make(chan struct{}, 16),
		w:       w, h: h,
	}
}

func (c *captureSurface) Show(img *image.RGBA) { c.ShowRegions(img, nil) }

func (c *captureSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	c.mu.Lock()
	defer c.mu.Unlock()
	cp := *image.NewRGBA(img.Bounds())
	copy(cp.Pix, img.Pix)
	c.img = &cp
	c.shown++
}

func (c *captureSurface) Size() (int, int) { return c.w, c.h }

func (c *captureSurface) WaitEvents(maxWait time.Duration) bool {
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	select {
	case <-c.arrived:
		return true
	case <-time.After(maxWait):
		return true
	}
}

func (c *captureSurface) Events() <-chan gfx.Event { return c.events }

func (c *captureSurface) push(ev gfx.Event) {
	c.events <- ev
	c.arrived <- struct{}{}
}

func (c *captureSurface) frame() *image.RGBA {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.img
}

type factory struct{ s gfx.Surface }

func (f *factory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) { return f.s, nil }

// mountAndSettle 把 src 挂到 s 上并驱动事件循环走到稳定态 (与画廊测试同构:
// 两轮无害事件空转, 让挂载副作用落到画面上; 然后关窗收尾)。
//
// ev 是事件注入源 —— GPU 路径下 s 是 *gpu.Surface, 事件必须由**内层**平台
// Surface 提供 (gpu.Surface 只接管 Show/ShowRegions, 事件仍走内层)。
//
// interactions 是到达稳定态之前要注入的交互序列 (悬停/点击), 用它们把链路
// 逼进**局部帧 (脏矩形)** 分支 —— 全帧分支挂载那一下就走过了。
func mountAndSettle(t *testing.T, src string, s gfx.Surface, ev *captureSurface, interactions ...gfx.Event) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	gfx.SetDefaultFactory(&factory{s})
	t.Cleanup(func() { gfx.SetDefaultFactory(nil) })

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	round := 0
	pump := func(maxWait time.Duration) bool {
		switch {
		case round < len(interactions): // 交互: 触发状态变化 → 局部帧
			ev.push(interactions[round])
		case round == len(interactions): // 空转一轮, 让副作用落到画面上
			ev.push(gfx.Event{Kind: gfx.EventMouseLeave})
		case round == len(interactions) + 1: // 取帧后关窗 (关窗轮会把窗口摘掉)
			ev.push(gfx.Event{Kind: gfx.EventClose})
		}
		round++
		return gfx.Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
}

func TestGPUPresentPixelExact(t *testing.T) {
	if !gpupkg.Available() {
		t.Skip("GPU 呈现路径不可用 (无适配器)")
	}

	// 一个覆盖多类图元 (盒模型/文字/图标/表单/装饰) 的真实组件脚本。
	src := `
import { h, render } from "gx/gfx";
render(
  <window title="GPU A/B" width={400} height={300}>
    <column gap={10} style={{padding: 12}}>
      <row gap={8}>
        <button>保存</button>
        <button kind="primary">提交</button>
      </row>
      <input placeholder="请输入名称" />
      <checkbox checked={true}>记住我</checkbox>
      <slider value={60} />
      <tag kind="success">运行中</tag>
      <text>软件光栅化 → GPU 合成</text>
    </column>
  </window>
);
`

	const w, h = 400, 300

	// 悬停第一个按钮 → hover 态变化 → 只有那个按钮的盒子变脏 → 局部帧。
	// 这条支路是 GPU 呈现最容易错的地方 (LoadOp=Load 保留 + scissor 只重画脏区),
	// 所以测试必须真的走到它, 而不是只验证首帧。
	hover := gfx.Event{Kind: gfx.EventMouseMove, X: 30, Y: 26}

	// ---- 路径 A: CPU 参照帧 ----
	cpuCap := newCaptureSurface(w, h)
	mountAndSettle(t, src, cpuCap, cpuCap, hover)
	cpuFrame := cpuCap.frame()
	if cpuFrame == nil {
		t.Fatal("CPU 路径一帧都没上屏")
	}
	if cpuCap.shown < 2 {
		t.Fatalf("CPU 路径只上屏 %d 帧, 悬停没有产生局部帧 (测试没走到脏矩形路径)", cpuCap.shown)
	}

	// ---- 路径 B: GPU 合成帧 ----
	gpuInner := newCaptureSurface(w, h)
	gs, err := gpupkg.New(gpuInner)
	if err != nil {
		t.Fatalf("gpu.Surface: %v", err)
	}
	mountAndSettle(t, src, gs, gpuInner, hover)
	gpuFrame, err := gs.Readback()
	if err != nil {
		t.Fatalf("GPU readback: %v", err)
	}
	st := gs.Stats()
	t.Logf("CPU 上屏 %d 帧; GPU 呈现 %d 帧 (整帧 %d / 局部 %d, 合成矩形 %d 个)",
		cpuCap.shown, st.Frames, st.FullFrames, st.RegionFrames, st.UploadedDrawn)
	if st.FullFrames == 0 || st.RegionFrames == 0 {
		t.Fatalf("GPU 路径没有同时覆盖整帧与局部两种帧 (整帧 %d / 局部 %d) —— 比对说服力不足",
			st.FullFrames, st.RegionFrames)
	}

	// ---- 逐像素比对 ----
	if cpuFrame.Bounds() != gpuFrame.Bounds() {
		t.Fatalf("尺寸不一致: CPU %v vs GPU %v", cpuFrame.Bounds(), gpuFrame.Bounds())
	}
	diff := 0
	firstDiff := image.Point{-1, -1}
	b := cpuFrame.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if cpuFrame.RGBAAt(x, y) != gpuFrame.RGBAAt(x, y) {
				diff++
				if firstDiff.X < 0 {
					firstDiff = image.Point{X: x, Y: y}
				}
			}
		}
	}
	t.Logf("CPU 帧 vs GPU 合成帧: 差异像素 %d / %d (首个差异 %v)", diff, w*h, firstDiff)
	if diff != 0 {
		t.Fatalf("GPU 呈现路径与 CPU 帧缓冲不一致 (%d 像素)", diff)
	}
}
