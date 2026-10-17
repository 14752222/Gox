//go:build linux && !gpu && !gpu_cgo

package softmedia

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// ===== PulseAudio simple API 绑定 (purego, 零 cgo) =====
//
// 为什么是 PulseAudio 而不是 ALSA: simple API 一个 pa_simple_write 就够
// (采样格式/声道/速率在 pa_simple_new 一次定好), 不需要摆周期/参数/
// mmap 那一套; 桌面 Linux (含 pipewire 的 pulse 兼容层) 上它基本都在。
// 没有 libpulse / 没有服务 / 没有设备时**全部静默降级 null sink** ——
// 不放声, 但播放时钟与事件照真 (无头环境照样能验证播放语义)。
//
// 与 gfx/cocoa 同一手法: purego.Dlopen + RegisterLibFunc。

// paSampleSpec 对应 C 的 pa_sample_spec。
//
// 布局必须逐字节对齐 (purego 不自动补 padding):
//
//	struct pa_sample_spec {
//	    pa_sample_format_t format;  // enum → int32, 偏移 0
//	    uint32_t rate;              // 偏移 4
//	    uint8_t channels;           // 偏移 8
//	};                             // 补齐到 12
type paSampleSpec struct {
	Format   int32
	Rate     uint32
	Channels uint8
	_        [3]byte
}

// pa_stream_direction_t: PA_STREAM_PLAYBACK = 1 (唯一用到的方向)。
const paStreamPlayback = 1

// pa_sample_format_t: PA_SAMPLE_FLOAT32LE = 5 (小端; softmedia 只跑在小端
// Linux 上, 大端直接不装音频能力)。
const paSampleFloat32LE = 5

var (
	pulseOnce sync.Once
	pulseLib  uintptr
	pulseLoad error

	procPaSimpleNew   func(server, name string, dir int, dev, stream string, ss *paSampleSpec, cmap, attr unsafe.Pointer, errno *int32) uintptr
	procPaSimpleFree  func(s uintptr)
	procPaSimpleWrite func(s uintptr, data []byte, errno *int32) int32
	procPaSimpleDrain func(s uintptr, errno *int32) int32
	procPaStrerror    func(errno int32) string
)

// loadPulse 惰性 dlopen libpulse 并绑符号 (整进程一次)。
func loadPulse() error {
	pulseOnce.Do(func() {
		lib, err := purego.Dlopen("libpulse.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			// 有些发行版只装开发链: 再试无版本后缀的
			lib, err = purego.Dlopen("libpulse.so", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		}
		if err != nil {
			pulseLoad = fmt.Errorf("softmedia: dlopen libpulse 失败: %w", err)
			return
		}
		pulseLib = lib
		// RegisterLibFunc 找不到符号会 panic —— 那意味着 libpulse 版本不对,
		// 属于"装了个假的" , 当成加载失败处理 (恢复 pulseLoad, 后面的音频
		// 能力全部走 null sink)。
		ok := true
		defer func() {
			if !ok {
				pulseLoad = fmt.Errorf("softmedia: libpulse 符号缺失")
			}
		}()
		safeReg := func(fptr any, name string) {
			defer func() {
				if recover() != nil {
					ok = false
				}
			}()
			purego.RegisterLibFunc(fptr, lib, name)
		}
		safeReg(&procPaSimpleNew, "pa_simple_new")
		safeReg(&procPaSimpleFree, "pa_simple_free")
		safeReg(&procPaSimpleWrite, "pa_simple_write")
		safeReg(&procPaSimpleDrain, "pa_simple_drain")
		safeReg(&procPaStrerror, "pa_strerror")
	})
	return pulseLoad
}

// pulseSink 是一块连着 PulseAudio 的输出。
type pulseSink struct {
	s        uintptr
	channels int
	rate     int
}

// openPulse 尝试开一个 playback 流; 任何一步失败都返回 nil (降级 null sink)。
func openPulse(channels, rate int) *pulseSink {
	if err := loadPulse(); err != nil {
		return nil
	}
	if channels < 1 || channels > 2 || rate <= 0 {
		return nil // simple API 场景只认单/双声道
	}
	spec := paSampleSpec{Format: paSampleFloat32LE, Rate: uint32(rate), Channels: uint8(channels)}
	var errno int32
	s := procPaSimpleNew("", "gox", paStreamPlayback, "", "softmedia",
		&spec, nil, nil, &errno)
	if s == 0 {
		return nil // 没有服务/没有设备: null sink
	}
	return &pulseSink{s: s, channels: channels, rate: rate}
}

// write 写一块 float32 交错样本 (LE 字节序)。
func (p *pulseSink) write(samples []float32) {
	if p == nil || len(samples) == 0 {
		return
	}
	b := unsafe.Slice((*byte)(unsafe.Pointer(&samples[0])), len(samples)*4)
	var errno int32
	if procPaSimpleWrite(p.s, b, &errno) != 0 {
		// 写失败 (设备拔出等): 当次丢弃。流本身留着 —— 设备可能回来。
		// 反复失败的下界由"整条流坏掉后用户听不到"兜底, 不做重连风暴。
		_ = errno
	}
}

// close drain 并释放 (设备拔出时 drain 可能报错, 无所谓: 已经要走了)。
func (p *pulseSink) close() {
	if p == nil || p.s == 0 {
		return
	}
	var errno int32
	_ = procPaSimpleDrain(p.s, &errno)
	procPaSimpleFree(p.s)
	p.s = 0
}

// nullSink 是"没有 PulseAudio"时的静默输出: 接口同 pulseSink (nil 即 null),
// 时钟由 audioPlayer 自己的 sleep 驱动, 所以不放声也照真走时。