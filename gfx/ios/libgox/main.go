//go:build ios

// Command libgox 是 iOS 上的 Go 侧入口: 编成 libgox.a (buildmode=c-archive),
// 由 Xcode 壳工程 (app/ios) 链接调用。内核 (lexer→vm→stdlib→gfx) 与桌面版
// 完全同一份。
//
// 为什么会有一个 main 包: c-archive 同样只接受 main 包, 且 //export 出来的
// 符号必须从这个包产生。这个 main 是占位的, 宿主**不**调用它。
//
// 构建 (需要 Xcode; 详细说明见 scripts/build-ios.sh 与 app/ios/README.md):
//
//	GOOS=ios GOARCH=arm64 CGO_ENABLED=1 go build -buildmode=c-archive \
//	  -o libgox.a ./gfx/ios/libgox
//
// 与 Android (JNI, c-shared) 的差异: iOS 上宿主与 Go 静态链进同一个二进制,
// 导出的是 C 函数 (gox_init / gox_tick / ...), Swift 经 bridging header 调用。
// 回调反向走宿主传入的函数指针 (契约见 gfx/ios 头部注释)。
//
// 线程模型 (与内核纪律一致): gox_run_script 起一条 Go 线程跑脚本与事件泵并
// LockOSThread; Swift 的 tick/触摸回调在别的线程上只往队列里塞事件。
package main

/*
#include <stdlib.h>
*/
import "C"

import (
	"runtime"
	"sync"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	gfxios "github.com/14752222/Gox/gfx/ios"
	"github.com/14752222/Gox/gfx/mobile"
	"github.com/14752222/Gox/vm"
)

// iOS 触摸动作值 (与 gfx/mobile 的 TouchDown/Move/Up/Cancel 对齐)。
// Swift 侧直接传这套值, 两端不各维护一份枚举 —— 与 Android 侧
// "直接透传 MotionEvent actionMasked" 同一取舍, 但 iOS 没有现成枚举可借,
// 所以这里的值就是 mobile 包自己的常量序。
const (
	touchDown   = 0
	touchMove   = 1
	touchUp     = 2
	touchCancel = 3
)

var (
	mu      sync.Mutex
	surface *mobile.Surface
	running bool // 脚本是否已在跑 (重复 start 直接忽略)
)

// main 是占位: c-archive 模式下由 Swift 通过下面导出的函数驱动。**不要**在
// 这里写 select{} —— 全阻塞会让 Go 运行时的死锁检测把宿主进程带走。
func main() {}

//export gox_init
//gox_init 初始化会话: 绑定宿主回调与帧缓冲, 创建表面并注册为默认窗口后端。
//必须在 gox_run_script 之前调用; 返回 0 成功, 非 0 失败 (msg 打到 stderr)。
func gox_init(w, h int32, density float32, buf unsafe.Pointer,
	fCtx unsafe.Pointer, fFn unsafe.Pointer, doneCtx, doneFn unsafe.Pointer) int32 {

	if err := gfxios.Init(int(w), int(h), buf, fCtx, fFn, doneCtx, doneFn); err != nil {
		gfxios.Logf("ios init 失败: %v", err)
		return 1
	}
	s := mobile.New(mobile.Config{
		Width:    int(w),
		Height:   int(h),
		Density:  float64(density),
		ID:       "0",
		Name:     "iOS",
		Uploader: gfxios.Uploader(),
	})
	// 注册成默认窗口后端。移动端没有"进程启动即注册"的时机 (后端要靠宿主给的
	// 表面才能成立), 所以必须在这里显式做 —— 与 Android 的 nativeInit 同理
	// (backend_other.go 在 ios 上不注册任何后端)。
	s.Register()

	mu.Lock()
	surface = s
	mu.Unlock()
	// 一行启动日志: 真机上"什么尺寸、density 多少、有没有真的走到这"全看它。
	gfxios.Logf("libgox 启动: %dx%d density=%.2f, surface 已注册为默认窗口后端",
		int(w), int(h), float64(density))
	return 0
}

//export gox_bind_frame_buffer
//gox_bind_frame_buffer 尺寸变化时重绑宿主缓冲 (Swift 重新 malloc 后调)。
func gox_bind_frame_buffer(w, h int32, buf unsafe.Pointer) int32 {
	if err := gfxios.BindFrameBuffer(buf, int(w)*int(h)*4); err != nil {
		gfxios.Logf("ios rebind 失败: %v", err)
		return 1
	}
	return 0
}

//export gox_run_script
//gox_run_script 跑一段脚本 (立即返回, 真正执行在 Go 自己的线程上)。
//结束 (含异常) 后经宿主的 finished 回调报告。
func gox_run_script(src, name *C.char) {
	source := C.GoString(src)
	label := C.GoString(name)
	if label == "" {
		label = "ios-script"
	}

	mu.Lock()
	if running {
		mu.Unlock()
		return
	}
	running = true
	s := surface
	mu.Unlock()
	if s == nil {
		gfxios.NotifyFinished(1, "gox_init 未调用")
		return
	}

	// 立即返回, 真正的执行放在 Go 自己的线程上: 调用线程是宿主 UI 线程,
	// 在里面跑事件泵会把 UI 冻住。
	go func() {
		// 内核纪律: 脚本、事件泵、VM 回调串行在同一条 OS 线程上。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := runScript(source, label); err != nil {
			// stderr 在 Xcode 控制台可见; finished 回调是宿主侧唯一的
			// "脚本起不来"信号。
			gfxios.Logf("%s: %v", label, err)
			gfxios.NotifyFinished(1, err.Error())
			return
		}
		gfxios.NotifyFinished(0, "")
	}()
}

//export gox_tick
//gox_tick 由宿主每帧调用一次 (CADisplayLink), 叫醒事件泵。
func gox_tick() {
	if s := currentSurface(); s != nil {
		s.Tick()
	}
}

//export gox_touch
//gox_touch 触摸事件: action 见文件头常量, 坐标是设备像素客户区坐标。
func gox_touch(action int32, x, y float32) {
	s := currentSurface()
	if s == nil {
		return
	}
	var a int
	switch action {
	case touchDown:
		a = mobile.TouchDown
	case touchMove:
		a = mobile.TouchMove
	case touchUp:
		a = mobile.TouchUp
	default:
		a = mobile.TouchCancel // 不认识的动作一律作废手势 (最安全的一档)
	}
	s.Touch(a, int(x), int(y))
}

//export gox_resize
//gox_resize 尺寸/密度变化 (旋转、分屏)。尺寸真变了才投 EventResize。
func gox_resize(w, h int32, density float32) {
	if s := currentSurface(); s != nil {
		s.Resize(int(w), int(h), float64(density))
	}
}

//export gox_destroy
//gox_destroy 结束会话: 关闭表面 (唤醒睡在 WaitEvents 里的泵 → 脚本线程收尾)。
func gox_destroy() {
	mu.Lock()
	s := surface
	surface = nil
	running = false
	mu.Unlock()
	if s != nil {
		s.Close()
	}
	gfxios.Reset()
}

// ===== 内部 =====

// runScript 执行脚本并在有窗口时驱动事件泵 (与 CLI 的 runFile 同一条路径)。
func runScript(source, label string) error {
	v, err := vm.EvalVM(source)
	if err != nil {
		return err
	}
	if gfx.Active() {
		if err := v.RunTimersWithPump(gfx.Pump); err != nil {
			return err
		}
	}
	return nil
}

func currentSurface() *mobile.Surface {
	mu.Lock()
	defer mu.Unlock()
	return surface
}
