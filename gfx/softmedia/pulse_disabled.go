//go:build linux && (gpu || gpu_cgo)

package softmedia

// 这一份是 pulse.go 在 **GPU 呈现轨** 下的替身: 不接 PulseAudio, 开不出声卡
// 输出, 音频一律走 null sink (播放时钟与事件照真, 只是听不见)。
//
// 为什么不能照常用 purego 去 dlopen libpulse: `-tags gpu` 拉进来的
// go-webgpu 走 goffi 调 libc, 而 **goffi 与 purego 各自带了一份 fakecgo**
// (都定义 `_cgo_init` 等符号)。两个同时链进同一个二进制时:
//
//	CGO_ENABLED=0 → link: duplicated definition of symbol _cgo_init,
//	                from goffi/internal/fakecgo and purego/internal/fakecgo
//	CGO_ENABLED=1 → unhandled relocation for goffi_errno_location / dlopen / ...
//
// 两边都是链接期硬错误, 躲不掉, 只能二选一。
//
// 取舍: 视频 (GIF / MJPEG) 是纯 stdlib 解码, 一点不受影响 —— GPU 轨下软媒体
// 照样解、照样合成、照样播; 只有音频输出退化成"无声但走时"。而 GPU 轨本身
// 就是呈现后端的实验轨, 默认构建 (用户真正会用的那一条) 完全不受影响。
// 真要在这条轨上听声音, 得先解掉上游的 fakecgo 冲突 (或让 go-webgpu 换
// purego 做 dlopen) —— 那不是 Gox 该替第三方决定的事。

// pulseSink 在 GPU 轨下是个空壳: openPulse 恒返回 nil, 而 nil 就是 null
// sink (audioPlayer 按 `sink *pulseSink; nil = 静默` 处理, 见 audioplayer.go)。
// 字段/方法留着只为形状一致, 谁都不会被调到。
type pulseSink struct{}

// openPulse 在 GPU 轨下恒失败 (nil): 没有 libpulse 绑定可开。
func openPulse(channels, rate int) *pulseSink { return nil }

func (p *pulseSink) write(samples []float32) {}

func (p *pulseSink) close() {}
