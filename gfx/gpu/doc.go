// Package gpu 是 gfx 的 GPU 呈现后端, 把「上屏」接缝从 CPU blit
// (XPutImage / StretchDIBits / CALayer.contents) 换成 GPU 合成。
//
// ## 双轨构建 (本包的核心设计)
//
// GPU 渲染是 **opt-in 能力**。零 cgo 是**默认构建**的发行红利 (桌面发行靠
// ubuntu-latest 单机交叉编译出 5 个平台), 不能因为 GPU 把它丢掉; 但开启 GPU
// 之后不是零 cgo 完全可接受 —— 于是本包按 build tag 拆成三轨:
//
//	构建                           含本包实现      cgo  交叉编译
//	go build ./cmd/gox              nogpu 空实现     无    ✅ (默认, 现状不变)
//	go build -tags gpu ./cmd/gox    wgpu 纯 Go       无    ✅ (Vulkan/DX12/Metal/GLES)
//	go build -tags gpu_cgo ...      cgo 原生        有    ❌ (需目标平台工具链)
//
// 未启用任何 tag 时本包是**空壳**: New 恒失败, Available 恒 false, 不引入
// gogpu 任何依赖 ——「没开 GPU = PC 零 cgo」这条性质由构建系统自动保证,
// 不依赖任何人记得删代码。
//
// 三轨共用同一个入口签名, 宿主接入代码在三种构建下逐字不变:
//
//	gfx.SetPresentWrapper(func(inner gfx.Surface) (gfx.Surface, bool) {
//	    s, err := gpu.New(inner)
//	    return s, err == nil   // nogpu 轨 err != nil → 不包装 → 退回 CPU blit
//	})
//
// ## 三轨的取舍
//
//   - wgpu 轨: 纯 Go WebGPU (gogpu/wgpu), 自带 WGSL 着色器编译器,
//     Vulkan / DX12 / Metal / GLES / Software 五后端, 无 GPU 硬件时退到
//     Software 后端。体积 +5.89 MB (实测), 但交叉编译不丢。
//   - cgo 轨: import "C" 直调 GLES / Metal。驱动兼容出问题时切这条,
//     代价是发版矩阵要从「1 台 Linux 交叉编译」改成「3 台 runner 各编各的」,
//     且 Linux 上动态链 glibc。
//
// ## 在管线里的位置
//
//	app.redraw():
//	  Layout → FillRect/Draw (CPU 光栅化, 不动)
//	           → surface.ShowRegions(a.img, rects)   ← 本包替换的就是这一跳
//
// a.img 是持久帧缓冲, ShowRegions 收到「整帧像素 + 本次变化矩形」, GPU 侧因此
// 维护同尺寸纹理: 全帧整幅上传 + 全屏 quad; 局部帧逐区子上传 + scissor 重绘
// (LoadOp=Load 保留旧内容)。与 x11 后端逐区域 PutImage 的结构一一对应。
//
// 事件 / 尺寸 / 窗口操作始终委托内层平台 Surface, 本包只接管上屏两跳 ——
// 跟 nativeVideoHost 那类「可选能力走可选接口」同一套做法, 不扩 Surface 契约。
package gpu

// ErrNotBuilt 表示「这个构建没有编入 GPU 后端」—— 宿主据此不安装呈现包装器,
// 继续用平台后端的 CPU blit。降级而非失败: GPU 拿不到不该让窗口开不出来。
var ErrNotBuilt = errNotBuilt()

type gpuNotBuiltError struct{}

func (gpuNotBuiltError) Error() string {
	return "gpu: this build has no GPU backend (build with -tags gpu or -tags gpu_cgo)"
}

func errNotBuilt() error { return gpuNotBuiltError{} }
