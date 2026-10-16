//go:build gpu

// GPU 呈现后端的接入点 (阶段 1, 接缝 A)。
//
// 整个文件只有 -tags gpu 才编译: 默认构建连 gfx/gpu 都不 import,
// 「GUI 没开 GPU = PC 零 cgo」由 build tag 保证, 不靠运行时判断。
//
// ## 包装策略: 只包装硬件适配器
//
// 无 GPU 硬件时 wgpu 会拿软件渲染器 (纯 Go 软件 HAL / SwiftShader /
// llvmpipe)。实测 (scripts/bench-present-4k.sh, 4K 全帧): 软件后端做
// 合成的应用 CPU 约 168ms/帧, 而 x11 PutImage 只要 32ms/帧 —— **慢 5 倍**,
// 因为纯 Go 软光栅把合成接了过去, 且每 Draw 全量重建管线/深度缓冲
// (约 100MB/帧分配)。所以生产路径默认不包装软件适配器; GOX_GPU=software
// 可强制打开 (CI 像素级验证 / 无头调试用)。
//
// 为什么读环境变量的代码在 cmd 而不是库里: 见 initDevMode 同款纪律 ——
// 库不该因为「被链接进某个进程」就改变行为, 配置由 CLI 边界显式传入。

package main

import (
	"fmt"
	"os"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/gfx/gpu"
)

func init() {
	// Available() 会触发 device 创建 (含软件回退); 建不起来就安静退出,
	// 窗口照常走平台 CPU blit —— GPU 是加速器, 不是依赖。
	if !gpu.Available() {
		return
	}
	// 软件适配器: 生产不包装 (见文件头)。显式 GOX_GPU=software 时强制,
	// 让 CI 与无头环境能走 GPU 合成路径做像素级验证。
	if gpu.BackendKind() == "software" && os.Getenv("GOX_GPU") != "software" {
		fmt.Fprintf(os.Stderr,
			"gox: GPU 呈现未启用 —— 适配器为软件渲染 (%s), 实测比 CPU blit 慢; "+
				"设 GOX_GPU=software 可强制启用\n", gpu.Backend())
		return
	}
	// 硬件适配器: 包装。失败 (驱动兼容问题等) 时 wrapPresent 静默降级,
	// 用户无感, 窗口继续用平台后端。
	gfx.SetPresentWrapper(func(inner gfx.Surface) (gfx.Surface, bool) {
		s, err := gpu.New(inner)
		return s, err == nil
	})
}
