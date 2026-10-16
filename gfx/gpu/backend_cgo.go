//go:build gpu_cgo

// Package gpu 的 cgo 轨: import "C" 直调平台图形 API。
//
// ## 什么时候用这一轨
//
// 默认路线是 wgpu 纯 Go (-tags gpu), 它连交叉编译都能保住。这一轨是**兜底**:
// 当真机上 wgpu 的某个后端遇到驱动兼容问题 (或某个平台后端尚未成熟) 时切换。
//
// ## 代价 (启用前必须知道)
//
//  1. **失去交叉编译**: 桌面发行要从「ubuntu-latest 单机出 5 平台」改成
//     windows / macos / linux 三台 runner 各编各的; release.yml 需加原生构建
//     job (macos runner 已有, 用于签名)。
//  2. **glibc 动态链接**: Linux 上 cgo 构建动态链 glibc, 分发要处理版本兼容
//     (或静态链 libc), 「单二进制」纯度退让。
//  3. **工具链依赖**: 目标平台需要 C 编译器 + GL 头文件。
//
// ## 平台路线
//
//   - Linux:   EGL + GLESv2 (libEGL / libGLESv2) 或 Vulkan
//   - Windows: WGL / EGL + GLESv2, 或 Vulkan
//   - macOS:   CGL / NSOpenGL, 或 Metal (CAMetalLayer)
//
// 本文件是**骨架**: 接口与 wgpu 轨完全一致, 平台绑定待按上面的路线补齐。
// 之所以先落骨架而不是等「真出问题再写」, 是为了让 SetPresentWrapper 的接入
// 代码与发版矩阵的设计现在就定型, 不等到出事才改架构。
package gpu

import (
	"image"

	"github.com/14752222/Gox/gfx"
)

// Available 恒 false 直到 cgo 绑定补齐 —— 骨架阶段不声称可用。
func Available() bool { return false }

// Backend 报告后端名。
func Backend() string { return "cgo" }

// New 暂不可用: 骨架阶段返回 ErrNotBuilt, 宿主退回 CPU blit。
func New(inner gfx.Surface) (*Surface, error) { return nil, ErrNotBuilt }

// Surface 占位 (与 nogpu 轨同形, 见 backend_nogpu.go 的说明)。
type Surface struct{ _ struct{} }

func (s *Surface) Readback() (*image.RGBA, error) { return nil, ErrNotBuilt }
func (s *Surface) Stats() Stats                   { return Stats{} }

type Stats struct {
	Frames        int
	FullFrames    int
	RegionFrames  int
	UploadedDrawn int
}
