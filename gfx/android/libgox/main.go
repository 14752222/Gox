//go:build android

// Command libgox 是 Android 上的 Go 侧入口: 编成 libgox.so (buildmode=c-shared),
// 由 Kotlin 通过 JNI 调用, 内核 (lexer→vm→stdlib→gfx) 与桌面版完全同一份。
//
// 为什么会有一个 main 包: `-buildmode=c-shared` 只接受 main 包, 且 //export 出来的
// 符号必须从这个包产生 (非 main 包里 //export 的可用性依赖工具链细节, 不赌)。
// 这个 main 是占位的, 宿主**不**调用它。
//
// 构建 (NDK 路径按本机调整; CC 必须是 Windows 绝对路径 + 版本化 wrapper):
//
//	GOOS=android GOARCH=arm64 CGO_ENABLED=1 \
//	  CC="<NDK>/toolchains/llvm/prebuilt/windows-x86_64/bin/aarch64-linux-android21-clang.cmd" \
//	  go build -buildmode=c-shared -o libgox.so ./gfx/android/libgox
//
// 或者直接跑 `bash scripts/build-android.sh`。
//
// 线程模型 (与内核纪律一致): nativeRunScript 起一条 Go 线程跑脚本与事件泵并
// LockOSThread; Kotlin 侧的 tick/触摸回调在**别的**线程上只往队列里塞事件 ——
// 与 win32 的 WndProc 同一约束, 绝不跨线程执行 JS。
package main

/*
#include <jni.h>
#include <stdlib.h>

static const char *gox_utf_chars(JNIEnv *env, jstring s) {
	return (*env)->GetStringUTFChars(env, s, NULL);
}
static void gox_release_utf(JNIEnv *env, jstring s, const char *c) {
	(*env)->ReleaseStringUTFChars(env, s, c);
}
*/
import "C"

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	gfxandroid "github.com/14752222/Gox/gfx/android"
	"github.com/14752222/Gox/gfx/mobile"
	"github.com/14752222/Gox/vm"
)

// Android MotionEvent 的动作值 (actionMasked)。直接照搬平台常量: Kotlin 侧
// 一行 `event.actionMasked` 就能传过来, 不必自己维护一张映射表。
const (
	actDown        = 0
	actUp          = 1
	actMove        = 2
	actCancel      = 3
	actPointerDown = 5 // 第二根手指按下 → v1 视为整个手势作废
	actPointerUp   = 6 // 第二根手指抬起 → 同上
)

var (
	mu      sync.Mutex
	surface *mobile.Surface
	running bool // 脚本是否已在跑 (重复 start 直接忽略)
)

// main 是占位: c-shared 模式下由 Kotlin 通过下面导出的函数驱动。**不要**在这里
// 写 select{} —— 全阻塞会让 Go 运行时的死锁检测把宿主进程带走。
func main() {}

//export JNI_OnLoad
func JNI_OnLoad(vmPtr *C.JavaVM, reserved unsafe.Pointer) C.jint {
	gfxandroid.SetJavaVM(unsafe.Pointer(vmPtr))
	return C.JNI_VERSION_1_6
}

//export Java_com_gox_GoxRuntime_nativeInit
func Java_com_gox_GoxRuntime_nativeInit(e *C.JNIEnv, clazz C.jclass,
	w C.jint, h C.jint, density C.jfloat, frameBuf C.jobject, host C.jobject) C.jint {

	// 绑定顺序不能换: 先接上帧缓冲与回调, 再建表面 —— 反之首帧渲染时钩子还是空的。
	if err := gfxandroid.BindFrameBuffer(unsafe.Pointer(frameBuf), int(w)*int(h)*4); err != nil {
		return jerr(err)
	}
	if err := gfxandroid.BindHost(unsafe.Pointer(host)); err != nil {
		return jerr(err)
	}
	s := mobile.New(mobile.Config{
		Width:    int(w),
		Height:   int(h),
		Density:  float64(density),
		ID:       "0",
		Name:     "Android",
		Uploader: gfxandroid.Uploader(),
	})
	// 注册成默认窗口后端。移动端没有"进程启动即注册"的时机 (后端要靠宿主给的
	// 表面才能成立), 所以必须在这里显式做 —— 这也是 gfx/backend 在 android 上
	// 不做自动 init 的原因 (见 backend_android.go)。
	s.Register()

	mu.Lock()
	surface = s
	mu.Unlock()
	return 0
}

//export Java_com_gox_GoxRuntime_nativeBindFrameBuffer
func Java_com_gox_GoxRuntime_nativeBindFrameBuffer(e *C.JNIEnv, clazz C.jclass,
	w C.jint, h C.jint, frameBuf C.jobject) C.jint {

	// 尺寸变化时 Kotlin 重新 allocateDirect 一块缓冲, 必须重新绑一次 —— 旧地址
	// 指向的内存可能已被回收, 继续写就是写坏 JVM 的堆。
	if err := gfxandroid.BindFrameBuffer(unsafe.Pointer(frameBuf), int(w)*int(h)*4); err != nil {
		return jerr(err)
	}
	return 0
}

//export Java_com_gox_GoxRuntime_nativeRunScript
func Java_com_gox_GoxRuntime_nativeRunScript(e *C.JNIEnv, clazz C.jclass,
	src C.jstring, name C.jstring) {

	source := goString(e, src)
	label := goString(e, name)
	if label == "" {
		label = "android-script"
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
		gfxandroid.NotifyFinished(1, "nativeInit 未调用")
		return
	}

	// 立即返回, 真正的执行放在 Go 自己的线程上: JNI 调用线程是宿主 UI 线程,
	// 在里面跑事件泵会把 UI 冻住。
	go func() {
		// 内核纪律: 脚本、事件泵、VM 回调串行在同一条 OS 线程上。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := runScript(source, label); err != nil {
			fmt.Fprintf(os.Stderr, "gox: %s: %v\n", label, err)
			gfxandroid.NotifyFinished(1, err.Error())
			return
		}
		gfxandroid.NotifyFinished(0, "")
	}()
}

//export Java_com_gox_GoxRuntime_nativeTick
func Java_com_gox_GoxRuntime_nativeTick(e *C.JNIEnv, clazz C.jclass) {
	if s := currentSurface(); s != nil {
		s.Tick()
	}
}

//export Java_com_gox_GoxRuntime_nativeTouch
func Java_com_gox_GoxRuntime_nativeTouch(e *C.JNIEnv, clazz C.jclass,
	action C.jint, x C.jfloat, y C.jfloat) {

	s := currentSurface()
	if s == nil {
		return
	}
	s.Touch(androidAction(int(action)), int(x), int(y))
}

//export Java_com_gox_GoxRuntime_nativeKey
func Java_com_gox_GoxRuntime_nativeKey(e *C.JNIEnv, clazz C.jclass, key C.jstring, down C.jboolean) {
	s := currentSurface()
	if s == nil {
		return
	}
	kind := gfx.EventKeyUp
	if down != 0 {
		kind = gfx.EventKeyDown
	}
	// 软键盘走 IME 提交 (M2), 这里只服务外接键盘/按键。
	s.Post(gfx.Event{Kind: kind, Key: goString(e, key)})
}

//export Java_com_gox_GoxRuntime_nativeResize
func Java_com_gox_GoxRuntime_nativeResize(e *C.JNIEnv, clazz C.jclass,
	w C.jint, h C.jint, density C.jfloat) {

	if s := currentSurface(); s != nil {
		// 尺寸真变了才会投 EventResize (内核据此整帧重绘 + 派发 onResize)。
		s.Resize(int(w), int(h), float64(density))
	}
}

//export Java_com_gox_GoxRuntime_nativeDestroy
func Java_com_gox_GoxRuntime_nativeDestroy(e *C.JNIEnv, clazz C.jclass) {
	s := currentSurface()
	if s == nil {
		return
	}
	// Close 唤醒睡在 WaitEvents 里的泵 → RunTimersWithPump 返回 → 脚本线程收尾。
	s.Close()
	mu.Lock()
	surface = nil
	running = false
	mu.Unlock()
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

// androidAction 把 MotionEvent 的动作值翻译成 gfx/mobile 的动作常量。
// 不认识的一律当 Cancel —— 那是"丢掉这次手势"里最安全的一档 (不会留下按压态)。
func androidAction(v int) int {
	switch v {
	case actDown:
		return mobile.TouchDown
	case actMove:
		return mobile.TouchMove
	case actUp:
		return mobile.TouchUp
	case actCancel, actPointerDown, actPointerUp:
		return mobile.TouchCancel
	default:
		return mobile.TouchCancel
	}
}

func currentSurface() *mobile.Surface {
	mu.Lock()
	defer mu.Unlock()
	return surface
}

// jerr 把 Go 侧初始化错误变成 JNI 返回码并打印。Kotlin 拿到非 0 应当直接报错
// 退出, 而不是继续跑一个没有帧缓冲的会话 (那样的症状是"界面全黑但日志正常")。
func jerr(err error) C.jint {
	fmt.Fprintf(os.Stderr, "gox: android init 失败: %v\n", err)
	return 1
}

// goString 把 jstring 转成 Go 字符串。
//
// 注意 JNI 的 "UTF-8" 是 modified UTF-8 (CESU-8): 基本多文种平面之外的字符
// (emoji) 会被编成代理对。脚本源码里出现 emoji 时可能解析异常 —— v1 记录不处理
// (与 Android 上其它嵌入式脚本引擎的取舍一致)。
func goString(e *C.JNIEnv, s C.jstring) string {
	// cgo 把 jstring 映射成"底层是 unsafe.Pointer 的具名类型", 不能直接和 nil 比。
	if unsafe.Pointer(s) == nil {
		return ""
	}
	c := C.gox_utf_chars(e, s)
	if c == nil {
		return ""
	}
	defer C.gox_release_utf(e, s, c)
	return C.GoString(c)
}
