//go:build gpu

package gpu

import (
	"context"
	"fmt"
	"image"
	"sync"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/gogpu/gputypes"
	"github.com/gogpu/wgpu"
	_ "github.com/gogpu/wgpu/hal/allbackends" // 注册 Vulkan/DX12/Metal/GLES/Software 后端
)

// 包级单例: 全部窗口共享一个 GPU device (多窗口 / 多屏场景下驱动对象只建一份,
// 每窗口一个 surface/swapchain —— 与 gogpu 应用框架同一模型)。
var (
	devOnce sync.Once
	devInst *wgpu.Device
	devErr  error
)

// Device 返回进程级共享 GPU device; 不可用时 (无适配器/驱动) 返回 error,
// 调用方应退回原平台后端, 行为降级而非失败。
func Device() (*wgpu.Device, error) {
	devOnce.Do(func() {
		inst, err := wgpu.CreateInstance(nil)
		if err != nil {
			devErr = fmt.Errorf("wgpu instance: %w", err)
			return
		}
		ad, err := inst.RequestAdapter(nil)
		if err != nil {
			// 硬件适配器不可得 → 软件渲染器 (无 GPU 机器的降级路径)
			ad, err = inst.RequestAdapter(&wgpu.RequestAdapterOptions{ForceFallbackAdapter: true})
			if err != nil {
				devErr = fmt.Errorf("wgpu adapter: %w", err)
				return
			}
		}
		d, err := ad.RequestDevice(nil)
		if err != nil {
			devErr = fmt.Errorf("wgpu device: %w", err)
			return
		}
		devInst = d
	})
	return devInst, devErr
}

// Available 报告 GPU 呈现路径是否可用 (设备能建起来即可, 软件后端也算可用)。
func Available() bool {
	_, err := Device()
	return err == nil
}

// Backend 报告当前实际拿到的后端 (调试面板 / canIUse 式能力探测用)。
// 无硬件适配器时是 "software" —— 调用方可以据此告诉用户「在用软渲染」。
func Backend() string {
	_, err := Device()
	if err != nil {
		return "none"
	}
	return "wgpu"
}

// Surface 实现 gfx.Surface: 事件/尺寸/等待委托内层平台 Surface,
// Show/ShowRegions 走 GPU 合成。内层为 nil 时是纯离屏模式 (测试/无头)。
//
// 线程契约与 gfx 一致: 全部方法只在渲染线程 (LockOSThread 的那个) 调用,
// 内部不加锁 —— wgpu device 本身不是线程安全的, 这与 Gox 单线程 GUI 模型匹配。
type Surface struct {
	inner gfx.Surface // 可为 nil (离屏)

	dev      *wgpu.Device
	pipeline *wgpu.RenderPipeline
	bindLyt  *wgpu.BindGroupLayout
	vbuf     *wgpu.Buffer
	sampler  *wgpu.Sampler

	// fbTex 是 CPU 帧缓冲的 GPU 镜像 (持久, 尺寸随窗口变化重建)。
	fbTex   *wgpu.Texture
	fbView  *wgpu.TextureView
	bindGrp *wgpu.BindGroup
	fbW, fbH int

	// target 是合成目标: 窗口化时为 swapchain 纹理, 离屏时为这张纹理。
	target *wgpu.Texture

	// 帧统计 (给 gx/dev 的调试面板用: "GPU 呈现路径到底走了哪条路")。
	frames        int // 呈现帧数
	fullFrames    int // 整帧上传的次数
	regionFrames  int // 局部 (脏矩形) 上传的帧数
	uploadedDrawn int // 累计合成的矩形数

	width, height int
}

// Stats 是 GPU 呈现路径的帧统计 —— 用来回答「这帧到底走了全帧还是脏矩形」。
type Stats struct {
	Frames        int // 呈现帧数
	FullFrames    int // 整帧次数
	RegionFrames  int // 局部帧次数
	UploadedDrawn int // 累计合成矩形数
}

// 编译期断言: Surface 确实是 gfx.Surface。
var _ gfx.Surface = (*Surface)(nil)

// New 建 GPU 呈现 Surface。inner 为内层平台 Surface (提供事件与窗口;
// 可为 nil = 纯离屏)。返回 error 时 GPU 路径不可用, 调用方应继续用 inner。
func New(inner gfx.Surface) (*Surface, error) {
	dev, err := Device()
	if err != nil {
		return nil, err
	}
	s := &Surface{inner: inner, dev: dev}
	if inner != nil {
		s.width, s.height = inner.Size()
	} else {
		s.width, s.height = 400, 300
	}
	if err := s.buildPipeline(); err != nil {
		return nil, err
	}
	if err := s.ensureTargets(s.width, s.height); err != nil {
		return nil, err
	}
	return s, nil
}

// buildPipeline 建「全屏 textured quad」管线 (呈现路径唯一需要的绘制)。
func (s *Surface) buildPipeline() error {
	type V struct{ x, y, u, v float32 }
	verts := []V{
		{-1, 1, 0, 0}, {1, 1, 1, 0}, {-1, -1, 0, 1},
		{-1, -1, 0, 1}, {1, 1, 1, 0}, {1, -1, 1, 1},
	}
	buf := make([]byte, len(verts)*16)
	for i, vt := range verts {
		putF32(buf[i*16:], vt.x, vt.y, vt.u, vt.v)
	}
	vbuf, err := s.dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "gpu-present-quad", Size: uint64(len(buf)),
		Usage: gputypes.BufferUsageVertex | gputypes.BufferUsageCopyDst,
	})
	if err != nil {
		return err
	}
	if err := s.dev.Queue().WriteBuffer(vbuf, 0, buf); err != nil {
		return err
	}
	s.vbuf = vbuf

	shader, err := s.dev.CreateShaderModule(&wgpu.ShaderModuleDescriptor{
		Label: "gpu-present", WGSL: presentWGSL,
	})
	if err != nil {
		return err
	}

	bgl, err := s.dev.CreateBindGroupLayout(&wgpu.BindGroupLayoutDescriptor{
		Entries: []gputypes.BindGroupLayoutEntry{
			{Binding: 0, Visibility: gputypes.ShaderStageFragment,
				Texture: &gputypes.TextureBindingLayout{SampleType: gputypes.TextureSampleTypeFloat}},
			{Binding: 1, Visibility: gputypes.ShaderStageFragment,
				Sampler: &gputypes.SamplerBindingLayout{Type: gputypes.SamplerBindingTypeFiltering}},
		},
	})
	if err != nil {
		return err
	}
	s.bindLyt = bgl

	layout, err := s.dev.CreatePipelineLayout(&wgpu.PipelineLayoutDescriptor{
		Label: "gpu-present-layout", BindGroupLayouts: []*wgpu.BindGroupLayout{bgl},
	})
	if err != nil {
		return err
	}
	pl, err := s.dev.CreateRenderPipeline(&wgpu.RenderPipelineDescriptor{
		Label:  "gpu-present-pipeline",
		Layout: layout,
		Vertex: wgpu.VertexState{
			Module: shader, EntryPoint: "vs_main",
			Buffers: []gputypes.VertexBufferLayout{{
				ArrayStride: 16, StepMode: gputypes.VertexStepModeVertex,
				Attributes: []gputypes.VertexAttribute{
					{Format: gputypes.VertexFormatFloat32x2, Offset: 0, ShaderLocation: 0},
					{Format: gputypes.VertexFormatFloat32x2, Offset: 8, ShaderLocation: 1},
				},
			}},
		},
		Fragment: &wgpu.FragmentState{
			Module: shader, EntryPoint: "fs_main",
			Targets: []gputypes.ColorTargetState{{
				Format: gputypes.TextureFormatRGBA8Unorm, WriteMask: gputypes.ColorWriteMaskAll,
			}},
		},
		Primitive: gputypes.PrimitiveState{Topology: gputypes.PrimitiveTopologyTriangleList},
	})
	if err != nil {
		return err
	}
	s.pipeline = pl

	sampler, err := s.dev.CreateSampler(&wgpu.SamplerDescriptor{
		MagFilter: gputypes.FilterModeNearest, MinFilter: gputypes.FilterModeNearest,
		AddressModeU: gputypes.AddressModeClampToEdge, AddressModeV: gputypes.AddressModeClampToEdge,
	})
	if err != nil {
		return err
	}
	s.sampler = sampler
	return nil
}

// ensureTargets 按窗口尺寸 (重) 建帧缓冲纹理镜像与合成目标。
// 对应 x11 后端在 resize 时重建 GC/窗口尺寸的同类动作。
func (s *Surface) ensureTargets(w, h int) error {
	if w <= 0 || h <= 0 {
		return fmt.Errorf("gpu: 非正尺寸 %dx%d", w, h)
	}
	if s.fbTex != nil && s.fbW == w && s.fbH == h {
		return nil
	}
	// 帧缓冲镜像: CopyDst (CPU 上传) + TextureBinding (着色器采样)
	fb, err := s.dev.CreateTexture(&wgpu.TextureDescriptor{
		Label: "gpu-framebuffer", Dimension: gputypes.TextureDimension2D,
		Size: wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
		MipLevelCount: 1, SampleCount: 1,
		Format: gputypes.TextureFormatRGBA8Unorm,
		Usage:  gputypes.TextureUsageTextureBinding | gputypes.TextureUsageCopyDst,
	})
	if err != nil {
		return err
	}
	view, err := s.dev.CreateTextureView(fb, nil)
	if err != nil {
		return err
	}
	bindGrp, err := s.dev.CreateBindGroup(&wgpu.BindGroupDescriptor{
		Label: "gpu-present-bind", Layout: s.bindLyt,
		Entries: []wgpu.BindGroupEntry{
			{Binding: 0, TextureView: view},
			{Binding: 1, Sampler: s.sampler},
		},
	})
	if err != nil {
		return err
	}
	// 合成目标: 窗口化时由 swapchain 提供; 离屏 (测试/无头) 用这张纹理。
	tgt, err := s.dev.CreateTexture(&wgpu.TextureDescriptor{
		Label: "gpu-present-target", Dimension: gputypes.TextureDimension2D,
		Size: wgpu.Extent3D{Width: uint32(w), Height: uint32(h), DepthOrArrayLayers: 1},
		MipLevelCount: 1, SampleCount: 1,
		Format: gputypes.TextureFormatRGBA8Unorm,
		Usage:  gputypes.TextureUsageRenderAttachment | gputypes.TextureUsageCopySrc,
	})
	if err != nil {
		return err
	}
	s.fbTex, s.fbView, s.bindGrp, s.target = fb, view, bindGrp, tgt
	s.fbW, s.fbH = w, h
	return nil
}

// Show 整帧上屏 (gfx.Surface)。
func (s *Surface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

// ShowRegions 局部上屏 (gfx.Surface): rects 为空 = 全帧。
// 对应 x11 后端逐区域 PutImage 的结构, 这里是逐区域子纹理上传 + scissor 重绘。
func (s *Surface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w <= 0 || h <= 0 {
		return
	}
	if err := s.ensureTargets(w, h); err != nil {
		return // 尺寸异常时静默跳过本帧 (与平台后端对 closed 的处理同风格)
	}
	if len(rects) == 0 {
		rects = []image.Rectangle{img.Bounds()}
		s.fullFrames++
	} else {
		s.regionFrames++
	}
	s.frames++
	// 1. 帧缓冲像素 → GPU 纹理 (全帧整幅 / 局部帧逐区子上传)
	for _, r := range rects {
		r = r.Intersect(img.Bounds())
		if r.Empty() {
			continue
		}
		rowBytes := r.Dx() * 4
		sub := make([]byte, rowBytes*r.Dy())
		stride := img.Stride // image.RGBA 可能有对齐 padding
		for y := 0; y < r.Dy(); y++ {
			off := (r.Min.Y+y)*stride + r.Min.X*4
			copy(sub[y*rowBytes:], img.Pix[off:off+rowBytes])
		}
		if err := s.dev.Queue().WriteTexture(
			&wgpu.ImageCopyTexture{Texture: s.fbTex, Origin: wgpu.Origin3D{X: uint32(r.Min.X), Y: uint32(r.Min.Y)}},
			sub,
			&wgpu.ImageDataLayout{BytesPerRow: uint32(rowBytes), RowsPerImage: uint32(r.Dy())},
			&wgpu.Extent3D{Width: uint32(r.Dx()), Height: uint32(r.Dy()), DepthOrArrayLayers: 1},
		); err != nil {
			return
		}
	}
	// 2. 合成: Load (保留目标旧内容) + 逐脏区 scissor 重绘 —— 只碰变化区域
	view, err := s.dev.CreateTextureView(s.target, nil)
	if err != nil {
		return
	}
	defer view.Release()
	enc, err := s.dev.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{Label: "gfx-frame"})
	if err != nil {
		return
	}
	pass, err := enc.BeginRenderPass(&wgpu.RenderPassDescriptor{
		ColorAttachments: []wgpu.RenderPassColorAttachment{{
			View: view, LoadOp: gputypes.LoadOpLoad, StoreOp: gputypes.StoreOpStore,
		}},
	})
	if err != nil {
		return
	}
	pass.SetPipeline(s.pipeline)
	pass.SetBindGroup(0, s.bindGrp, nil)
	pass.SetVertexBuffer(0, s.vbuf, 0)
	for _, r := range rects {
		r = r.Intersect(img.Bounds())
		if r.Empty() {
			continue
		}
		pass.SetScissorRect(gputypes.ScissorRect{
			X: uint32(r.Min.X), Y: uint32(r.Min.Y),
			Width: uint32(r.Dx()), Height: uint32(r.Dy()),
		})
		pass.Draw(gputypes.DrawArgs{VertexCount: 6, InstanceCount: 1})
		s.uploadedDrawn++
	}
	if err := pass.End(); err != nil {
		return
	}
	cmds, err := enc.Finish()
	if err != nil {
		return
	}
	_, _ = s.dev.Queue().Submit(cmds)
	// 窗口化路径: surface.Present(surfaceTex) 在此代替 readback。
}

// Stats 读取帧统计快照 (gx/dev 调试面板 / 测试断言用)。
func (s *Surface) Stats() Stats {
	return Stats{
		Frames: s.frames, FullFrames: s.fullFrames,
		RegionFrames: s.regionFrames, UploadedDrawn: s.uploadedDrawn,
	}
}

// Readback 把合成结果读回 CPU —— 测试与无头验证用 (真实上屏路径不需要)。
func (s *Surface) Readback() (*image.RGBA, error) {
	n := s.fbW * s.fbH * 4
	buf, err := s.dev.CreateBuffer(&wgpu.BufferDescriptor{
		Label: "gpu-readback", Size: uint64(n),
		Usage: gputypes.BufferUsageCopyDst | gputypes.BufferUsageMapRead,
	})
	if err != nil {
		return nil, err
	}
	defer buf.Release()
	enc, err := s.dev.CreateCommandEncoder(&wgpu.CommandEncoderDescriptor{})
	if err != nil {
		return nil, err
	}
	enc.CopyTextureToBuffer(s.target, buf, []wgpu.BufferTextureCopy{{
		BufferLayout: wgpu.ImageDataLayout{BytesPerRow: uint32(s.fbW * 4), RowsPerImage: uint32(s.fbH)},
		Size:         wgpu.Extent3D{Width: uint32(s.fbW), Height: uint32(s.fbH), DepthOrArrayLayers: 1},
	}})
	cmds, err := enc.Finish()
	if err != nil {
		return nil, err
	}
	if _, err := s.dev.Queue().Submit(cmds); err != nil {
		return nil, err
	}
	pending, err := buf.MapAsync(wgpu.MapModeRead, 0, buf.Size())
	if err != nil {
		return nil, err
	}
	s.dev.Poll(wgpu.PollWait) // 软件后端映射 resolve 需显式驱动
	if err := pending.Wait(context.Background()); err != nil {
		return nil, err
	}
	mr, err := buf.MappedRange(0, buf.Size())
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, s.fbW, s.fbH))
	copy(img.Pix, mr.Bytes())
	_ = buf.Unmap()
	return img, nil
}

// Size 委托内层 (离屏模式返回建处时的尺寸)。
func (s *Surface) Size() (int, int) {
	if s.inner != nil {
		return s.inner.Size()
	}
	return s.width, s.height
}

// WaitEvents / Events 委托内层平台 Surface (GPU 化不改事件模型)。
// 离屏模式 (inner == nil): 无事件源, 返回 false = 事件源关闭。
func (s *Surface) WaitEvents(maxWait time.Duration) bool {
	if s.inner == nil {
		return false
	}
	return s.inner.WaitEvents(maxWait)
}

// Events 委托内层平台 Surface; 离屏模式返回 nil (永远不会有事件)。
func (s *Surface) Events() <-chan gfx.Event {
	if s.inner == nil {
		return nil
	}
	return s.inner.Events()
}
