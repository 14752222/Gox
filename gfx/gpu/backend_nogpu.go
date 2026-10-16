//go:build !gpu && !gpu_cgo

// Package gpu 的默认轨: 空实现。
//
// 这个文件没有 build tag 之外的任何条件, 但**刻意不用 `//go:build !gpu` 写反**:
// 它只是「三轨之一」的默认形态, 与 backend_wgpu.go / backend_cgo.go 三者
// 恰好互斥。写成 `!gpu && !gpu_cgo` 是为了在「未来加第四轨」时立刻编译报错,
// 而不是静默多出一份实现互相冲突。
//
// 本轨的全部意义是「什么都不做」: 不 import gogpu、不 dlopen 任何图形库、
// 零体积、零 cgo —— 默认构建的发行性质由这个文件保证。
package gpu

import (
	"image"

	"github.com/14752222/Gox/gfx"
)

// Available 恒 false: 默认构建没有 GPU 后端。
func Available() bool { return false }

// Backend 报告当前构建的 GPU 后端名 (调试面板 / canIUse 式能力探测用)。
func Backend() string { return "none" }

// New 恒返回 ErrNotBuilt。宿主拿到 error 就不安装呈现包装器, 窗口照常用
// 平台后端的 CPU blit —— 这是「没开 GPU = PC 零 cgo」的执行点。
func New(inner gfx.Surface) (*Surface, error) { return nil, ErrNotBuilt }

// Surface 在默认轨下**不实现 gfx.Surface**: 它根本不应该被实例化。
// 占位定义只为让三轨的包级 API 形状一致 (New 的返回类型), 避免宿主接入
// 代码在不同 tag 下类型不同。
type Surface struct{ _ struct{} }

// Readback / Stats 在默认轨下不可达, 保留签名是为了编译兼容。
func (s *Surface) Readback() (*image.RGBA, error) { return nil, ErrNotBuilt }
func (s *Surface) Stats() Stats                   { return Stats{} }

// Stats 帧统计 (三轨同形)。
type Stats struct {
	Frames        int
	FullFrames    int
	RegionFrames  int
	UploadedDrawn int
}
