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
//
// ── NativeHost 通道 (gx/native.go 的宿主契约, Kotlin 实现) ───────────────────
//
// 内核在 GUI 线程 (本文件的脚本线程) 调 host.Call(method, args); 本文件把它
// 翻译成一次对 Kotlin 的 JNI 调用。协议 (两端字符串逐字一致, 改动要三处同步:
// 本文件 / GoxRuntime.kt / GoxNativeHost.kt):
//
//	Capabilities: Kotlin `nativeCapabilities(): String` → 逗号分隔的方法名/
//	              能力名列表 ("device.info,device.vibrate,…,insets,battery")
//	Call:         Kotlin `nativeCall(method: String, argsJson: String): String`,
//	              args 是内核 nativeArgsWithID 给的参数对象 (含 __id) 的 JSON。
//	              返回值三选一:
//	                "__pending__"                        → NativePending
//	                {"__errCode":"…","__errMsg":"…"}     → NativeFailure(8 码之一)
//	                其它 (结果 JSON, 任意形状)            → NativeResult
//	              同步可得的 (device.info / permission.get …) 当场返回 JSON;
//	              要等系统 UI 的 (相机/定位/权限弹窗) 返回 "__pending__", 稍后由
//	              Kotlin 经 nativeResolveNative 回填 (任意线程, Go 侧 gfx.Post)。
//	Resolve:      Kotlin `GoxRuntime.nativeResolveNative(id, resultJson, errCode,
//	              errMsg)` — id 照抄 args 里的 __id; errCode 非空即失败。
//	上报通道:     nativeReportBattery/Network/Location(JSON) / nativeReportAppState
//	              / nativeReportMemoryWarning / nativeReportPermission /
//	              nativeReportBackPress(): Boolean (同步等脚本答复, 见实现)。
package main

/*
#cgo LDFLAGS: -llog
#include <jni.h>
#include <android/log.h>
#include <stdlib.h>

static const char *gox_utf_chars(JNIEnv *env, jstring s) {
	return (*env)->GetStringUTFChars(env, s, NULL);
}
static void gox_release_utf(JNIEnv *env, jstring s, const char *c) {
	(*env)->ReleaseStringUTFChars(env, s, c);
}

// ── NativeHost 通道的 JNI 助手 ──
// 与 gfx/android/android.go 的通道相互独立 (那边绑定 flush/finished, 这边绑定
// nativeCapabilities/nativeCall): 本文件是 libgox 的装配层, 允许自带 JNI 胶水。
static JavaVM *gox_h_jvm = NULL;

static void gox_h_set_jvm(JavaVM *vm) { gox_h_jvm = vm; }

// gox_h_env 取当前线程 JNIEnv; 返回 0 = 已附加, 1 = 本次附加 (用完 detach),
// -1 = 拿不到。脚本线程不在 JVM 里, Call 期间的 AttachCurrentThread 是必需的。
static int gox_h_env(JNIEnv **env) {
	if (gox_h_jvm == NULL) {
		return -1;
	}
	jint r = (*gox_h_jvm)->GetEnv(gox_h_jvm, (void **)env, JNI_VERSION_1_6);
	if (r == JNI_OK) {
		return 0;
	}
	if (r == JNI_EDETACHED) {
		if ((*gox_h_jvm)->AttachCurrentThread(gox_h_jvm, env, NULL) != JNI_OK) {
			return -1;
		}
		return 1;
	}
	return -1;
}

static void gox_h_detach_current(void) {
	if (gox_h_jvm != NULL) {
		(*gox_h_jvm)->DetachCurrentThread(gox_h_jvm);
	}
}

static jobject gox_h_gref(JNIEnv *env, jobject o) {
	return (*env)->NewGlobalRef(env, o);
}
// 方法名与签名写在 C 字面量里: Kotlin 侧方法名 (nativeCapabilities/nativeCall)
// 是契约, 写死在这里可以避免每次查找都 C.CString/free 一遍。
static jmethodID gox_h_cap_mid(JNIEnv *env, jobject o) {
	jclass c = (*env)->GetObjectClass(env, o);
	return (*env)->GetMethodID(env, c, "nativeCapabilities", "()Ljava/lang/String;");
}
static jmethodID gox_h_call_mid(JNIEnv *env, jobject o) {
	jclass c = (*env)->GetObjectClass(env, o);
	return (*env)->GetMethodID(env, c, "nativeCall", "(Ljava/lang/String;Ljava/lang/String;)Ljava/lang/String;");
}
static jstring gox_h_utf(JNIEnv *env, const char *s) {
	return (*env)->NewStringUTF(env, s);
}
static jobject gox_h_call0(JNIEnv *env, jobject o, jmethodID m) {
	return (*env)->CallObjectMethod(env, o, m);
}
static jobject gox_h_call2(JNIEnv *env, jobject o, jmethodID m, jstring a, jstring b) {
	return (*env)->CallObjectMethod(env, o, m, a, b);
}
// 跨语言调用出错时清掉挂起异常: 悬空异常会在下一次 JNI 调用上炸掉整个进程。
static jint gox_h_ex(JNIEnv *env) {
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionClear(env);
		return 1;
	}
	return 0;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	gfxandroid "github.com/14752222/Gox/gfx/android"
	"github.com/14752222/Gox/gfx/hostbridge"
	"github.com/14752222/Gox/gfx/mobile"
	"github.com/14752222/Gox/object"
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
	C.gox_h_set_jvm(vmPtr)
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

	// 注册宿主原生实现 (gx/native.go): Kotlin 侧的 nativeCapabilities /
	// nativeCall 从这里开始被内核看见。绑定失败 (Kotlin 侧没这些方法) 时注册的
	// 宿主能力表为空 —— 内核会把所有原生调用显式 reject(unsupported), 绝不会
	// 静默给假数据。
	bindNativeHost(unsafe.Pointer(host))
	// 软键盘开关走 GoxHost.imeShow (焦点进/出编辑框时内核调, 见 gfx/mobile)。
	s.SetIMEHost(gfxandroid.CallIMEShow)
	// M2: 编辑框状态回传 (光标前后文本/选区/光标矩形) 走 GoxHost.imeEditor ——
	// 宿主据此实现 InputConnection 的文本查询与 updateCursorAnchorInfo, 输入法的
	// 候选词窗才会贴着光标而不是盖住输入框。缺这个方法时宿主降级, 不致命。
	s.SetIMEEditorHost(gfxandroid.CallIMEEditor)

	mu.Lock()
	surface = s
	mu.Unlock()
	// 一行启动日志: 真机上"什么尺寸、density 多少、有没有真的走到这"全看它
	// (`adb logcat -s Gox:I`)。
	gfxandroid.Logf("libgox 启动: %dx%d density=%.2f, surface 已注册为默认窗口后端",
		int(w), int(h), float64(density))
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
			// 走 Logf 而不是 fmt.Fprintf(os.Stderr): 真机上 stderr 被接到 /dev/null,
			// 而这里正是"脚本起不来"唯一能留下原因的地方。
			gfxandroid.Logf("%s: %v", label, err)
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

//export Java_com_gox_GoxRuntime_nativeSetInsets
func Java_com_gox_GoxRuntime_nativeSetInsets(e *C.JNIEnv, clazz C.jclass,
	top C.jint, right C.jint, bottom C.jint, left C.jint) {

	// 经 gfx.Post 投回 GUI 线程再报内核: 上报会**同步**跑脚本侧的 onViewportChange
	// 订阅回调, 事件纪律 —— JNI 回调线程绝不直接执行 JS。
	//
	// **必须是 ReportInsets, 不能用 ReportViewport**: 后者是 patchAll 语义, 而
	// 宿主在键盘弹起后还会再分发一次 insets —— 那一次会把刚报上来的键盘高度一起
	// 清成 0, 界面上"键盘高(px)"于是永远是 0 (2026-10-02 模拟器实测, 见
	// gfx.ReportInsets 的注释)。
	gfx.Post(func() {
		gfx.ReportInsets(nil, gfx.Insets{
			Top: int(top), Right: int(right), Bottom: int(bottom), Left: int(left),
		})
	})
}

//export Java_com_gox_GoxRuntime_nativeIMECommit
func Java_com_gox_GoxRuntime_nativeIMECommit(e *C.JNIEnv, clazz C.jclass, text C.jstring) {
	s := currentSurface()
	if s == nil {
		return
	}
	// 整批插入的语义在内核 gfx/ime.go —— 这里只投事件 (任意线程可调)。
	s.IMECommit(goString(e, text))
}

// Java_com_gox_GoxRuntime_nativeSetKeyboard 上报软键盘高度 (设备像素, 0 = 收起)。
//
// 为什么要有这条通道: 引擎不是靠"窗口变小"来感知键盘的 (那要开
// SOFT_INPUT_ADJUST_RESIZE, 而 SurfaceView 会跟着重排, 整棵树的尺寸都变) ——
// 键盘高度是一个**环境读数**, 脚本侧用 useKeyboardHeight() 自己决定抬多少。
// 不上报的话它恒为 0, "输入框不被键盘遮挡"根本无从实现。
//
// 单独一条而不是复用 nativeSetInsets: Viewport 的合并是 upsert, 而键盘弹起时
// insets 通常没变 —— 复用会把安全区清成 0 (见 gfx.ReportKeyboardHeight)。
//
// 任意线程可调 (内部 gfx.Post 投回 GUI 线程再报内核)。
//
//export Java_com_gox_GoxRuntime_nativeSetKeyboard
func Java_com_gox_GoxRuntime_nativeSetKeyboard(e *C.JNIEnv, clazz C.jclass, height C.jint) {
	h := int(height)
	// 打一条日志: 键盘高度是"界面没有被顶掉两倍/完全没让位"这类问题的第一现场,
	// 而真机上除了 logcat 没有别的读法 (脚本侧读到的值要截图才看得见)。
	// 只有**变化时**才会调到这里, 所以不会刷屏。
	if h > 0 {
		gfxandroid.Logf("软键盘弹出: 高度 %d px", h)
	} else {
		gfxandroid.Logf("软键盘收起")
	}
	gfx.Post(func() {
		gfx.ReportKeyboardHeight(nil, h)
	})
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
	gfxandroid.Logf("android init 失败: %v", err)
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

// ===== NativeHost 通道: Go ↔ Kotlin 的宿主契约翻译 (见文件头协议) =====

// nativeHostBind 是 Kotlin 侧 GoxHost 对象的全局引用 + 两个方法 id。
// nativeInit 在 Kotlin 主线程上调用, 绑定动作也发生在那个线程 —— 之后 Call 从
// 脚本线程来, 只读这几个值, 由 nativeHostMu 保护写入。
var (
	nativeHostMu    sync.Mutex
	nativeHostObj   unsafe.Pointer
	nativeHostCapM  C.jmethodID
	nativeHostCallM C.jmethodID
)

// bindNativeHost 绑定 Kotlin 侧宿主对象并查找契约方法。必须在 Java 线程调用
// (NewGlobalRef / GetObjectClass 依赖当前线程已附加 JVM)。找不到方法不算致命:
// 注册一个空能力宿主, 内核对所有原生调用显式 reject(unsupported)。
func bindNativeHost(host unsafe.Pointer) {
	e, done, err := jniEnv()
	if err != nil {
		gfxandroid.Logf("NativeHost 绑定失败: %v", err)
		return
	}
	defer done()

	obj := unsafe.Pointer(C.gox_h_gref(e, C.jobject(host)))
	if obj == nil {
		gfxandroid.Logf("NativeHost 绑定失败: NewGlobalRef 返回空")
		return
	}
	capM := C.gox_h_cap_mid(e, C.jobject(obj))
	C.gox_h_ex(e) // 查找失败可能挂异常, 清掉 (下面统一按缺方法处理)
	callM := C.gox_h_call_mid(e, C.jobject(obj))
	if C.gox_h_ex(e) != 0 || callM == nil {
		gfxandroid.Logf("NativeHost: Kotlin 侧缺 nativeCall(String,String)String —— 原生能力不可用 (GoxNativeHost.kt 缺失?)")
	}

	nativeHostMu.Lock()
	nativeHostObj, nativeHostCapM, nativeHostCallM = obj, capM, callM
	nativeHostMu.Unlock()

	gfx.SetNativeHost(nativeHost)
}

// nativeHostRefs 取绑定好的宿主引用与方法 id (无绑定 → nil)。
func nativeHostRefs() (unsafe.Pointer, C.jmethodID, C.jmethodID) {
	nativeHostMu.Lock()
	defer nativeHostMu.Unlock()
	return nativeHostObj, nativeHostCapM, nativeHostCallM
}

// nativeHost 是 gfx.NativeHost 的 Android 实现。
//
// **协议层不在本文件**: 回包三态解析 (pending / 错误信封 / 结果)、能力串切分、
// JSON 窄转换全在平台无关的 gfx/hostbridge 里 —— 三端共用同一份, 而且能被
// gfx/hosttest 的契约套件在开发机上直接跑到 (见 gfx/hostbridge/contract_test.go:
// 它从 Kotlin 源码解析真实能力清单后跑全套断言)。这里只剩跨语言那一跳。
var nativeHost = hostbridge.New(
	// caps: 调 Kotlin 的 nativeCapabilities(), 取逗号分隔的能力串。
	func() (string, error) {
		obj, capM, _ := nativeHostRefs()
		if obj == nil || capM == nil {
			return "", errors.New("宿主未绑定 nativeCapabilities")
		}
		e, done, err := jniEnv()
		if err != nil {
			return "", err
		}
		defer done()
		res := C.gox_h_call0(e, C.jobject(obj), capM)
		if C.gox_h_ex(e) != 0 || unsafe.Pointer(res) == nil {
			return "", errors.New("nativeCapabilities 抛了异常或返回空")
		}
		return goString(e, C.jstring(res)), nil
	},
	// call: 调 Kotlin 的 nativeCall(method, argsJson), 取原始回包字符串。
	//
	// 同步路径 (device.info / permission.get …) 会阻塞脚本线程直到 Kotlin 返回 ——
	// 这正是内核要求的: 这几个方法只有同步返回值才会被 deviceHostOverlay /
	// jsGetBrightness / jsOpenSystemSettings / refreshPermissionsFromHost 采用
	// (native_device.go:212, native_permission.go:181 都是直接读 res.Result)。
	// Kotlin 侧的同步实现都是纯内存/系统读, 不会卡住。
	func(method, argsJSON string) (string, error) {
		obj, _, callM := nativeHostRefs()
		if obj == nil || callM == nil {
			return "", errors.New("宿主未绑定 nativeCall")
		}
		e, done, err := jniEnv()
		if err != nil {
			return "", err
		}
		defer done()

		cm := C.CString(method)
		defer C.free(unsafe.Pointer(cm))
		aj := C.CString(argsJSON)
		defer C.free(unsafe.Pointer(aj))

		res := C.gox_h_call2(e, C.jobject(obj), callM, C.gox_h_utf(e, cm), C.gox_h_utf(e, aj))
		if C.gox_h_ex(e) != 0 || unsafe.Pointer(res) == nil {
			return "", errors.New("nativeCall 抛了异常或返回空")
		}
		return goString(e, C.jstring(res)), nil
	},
	"Android 宿主未注册 nativeCall (GoxNativeHost 缺失?), 方法 %s 不可用",
)

// ── Kotlin → Go 的回填与上报导出 ──
//
// 这些都从 Kotlin 的任意线程调用; 内核要求 ResolveNative / Report* 在 GUI 线程
// 执行 (会同步跑脚本回调), 所以一律 gfx.Post 投回 —— 与触摸/tick 回调同一条
// 线程纪律。

//export Java_com_gox_GoxRuntime_nativeResolveNative
func Java_com_gox_GoxRuntime_nativeResolveNative(e *C.JNIEnv, clazz C.jclass,
	id C.jstring, result C.jstring, errCode C.jstring, errMsg C.jstring) {

	idStr := goString(e, id)
	resStr := goString(e, result)
	codeStr := goString(e, errCode)
	msgStr := goString(e, errMsg)
	if idStr == "" {
		return
	}
	gfx.Post(func() {
		var nativeErr *gfx.NativeError
		if codeStr != "" {
			nativeErr = &gfx.NativeError{Code: hostbridge.SanitizeErrCode(codeStr), Msg: msgStr}
		}
		var v object.Value
		if nativeErr == nil && resStr != "" {
			parsed, err := hostbridge.JSONToObj(resStr)
			if err != nil {
				nativeErr = &gfx.NativeError{Code: gfx.ErrPlatform,
					Msg: fmt.Sprintf("回填 JSON 解析失败: %v", err)}
			} else {
				v = parsed
			}
		}
		gfx.ResolveNative(idStr, v, nativeErr)
	})
}

//export Java_com_gox_GoxRuntime_nativeReportBattery
func Java_com_gox_GoxRuntime_nativeReportBattery(e *C.JNIEnv, clazz C.jclass, data C.jstring) {
	o := goJSONObj(e, data)
	if o == nil {
		return
	}
	gfx.Post(func() {
		gfx.ReportBattery(gfx.BatteryState{
			Supported:    jsonBool(o, "supported", false),
			Level:        jsonNum(o, "level", -1),
			Charging:     jsonBool(o, "charging", false),
			ChargingType: jsonStr(o, "chargingType", "unknown"),
			Temperature:  jsonNum(o, "temperature", -1),
			LowPowerMode: jsonBool(o, "lowPowerMode", false),
		})
	})
}

//export Java_com_gox_GoxRuntime_nativeReportNetwork
func Java_com_gox_GoxRuntime_nativeReportNetwork(e *C.JNIEnv, clazz C.jclass, data C.jstring) {
	o := goJSONObj(e, data)
	if o == nil {
		return
	}
	gfx.Post(func() {
		gfx.ReportNetwork(gfx.NetworkState{
			Connected:          jsonBool(o, "connected", false),
			Type:               jsonStr(o, "type", "none"),
			Metered:            jsonBool(o, "metered", false),
			SSID:               jsonStr(o, "ssid", ""),
			Strength:           int(jsonNum(o, "strength", -1)),
			Carrier:            jsonStr(o, "carrier", ""),
			CellularGeneration: jsonStr(o, "generation", ""),
		})
	})
}

//export Java_com_gox_GoxRuntime_nativeReportLocation
func Java_com_gox_GoxRuntime_nativeReportLocation(e *C.JNIEnv, clazz C.jclass, data C.jstring) {
	o := goJSONObj(e, data)
	if o == nil {
		return
	}
	gfx.Post(func() {
		gfx.ReportLocation(gfx.Location{
			Latitude:         jsonNum(o, "latitude", 0),
			Longitude:        jsonNum(o, "longitude", 0),
			Altitude:         jsonNum(o, "altitude", 0),
			Accuracy:         jsonNum(o, "accuracy", 0),
			AltitudeAccuracy: jsonNum(o, "altitudeAccuracy", 0),
			Speed:            jsonNum(o, "speed", 0),
			Heading:          jsonNum(o, "heading", 0),
			Timestamp:        int64(jsonNum(o, "timestamp", 0)),
			Provider:         jsonStr(o, "provider", "unknown"),
			Mocked:           jsonBool(o, "mocked", false),
			Type:             jsonStr(o, "type", "wgs84"),
		})
	})
}

//export Java_com_gox_GoxRuntime_nativeReportAppState
func Java_com_gox_GoxRuntime_nativeReportAppState(e *C.JNIEnv, clazz C.jclass, state C.jstring) {
	s := goString(e, state)
	gfx.Post(func() { gfx.ReportAppState(s) })
}

//export Java_com_gox_GoxRuntime_nativeReportMemoryWarning
func Java_com_gox_GoxRuntime_nativeReportMemoryWarning(e *C.JNIEnv, clazz C.jclass) {
	gfx.Post(gfx.ReportMemoryWarning)
}

//export Java_com_gox_GoxRuntime_nativeReportPermission
func Java_com_gox_GoxRuntime_nativeReportPermission(e *C.JNIEnv, clazz C.jclass,
	kind C.jstring, state C.jstring) {

	k, st := goString(e, kind), goString(e, state)
	gfx.Post(func() { gfx.ReportPermission(k, st) })
}

//export Java_com_gox_GoxRuntime_nativeReportBackPress
func Java_com_gox_GoxRuntime_nativeReportBackPress(e *C.JNIEnv, clazz C.jclass) C.jboolean {
	// 唯一需要**同步**答案的上报: 返回键语义是"脚本处理了就不退出" (native_app.go
	// 文件头)。脚本回调必须跑在 GUI 线程上, 而调用方是 Android 主线程, 所以:
	// 投回 GUI 线程 → 用一次 Tick 叫醒可能睡着的泵 → 限时等待答复。
	// 超时按未处理返回 —— 宁可退出也别把主线程吊死 (ANR 比"少拦一次返回键"糟)。
	done := make(chan bool, 1)
	gfx.Post(func() { done <- gfx.ReportBackPress() })
	if s := currentSurface(); s != nil {
		s.Tick()
	}
	select {
	case v := <-done:
		if v {
			return 1
		}
	case <-time.After(500 * time.Millisecond):
		gfxandroid.Logf("reportBackPress: 500ms 内没有得到脚本答复, 按\"未处理\"返回")
	}
	return 0
}

// Java_com_gox_GoxRuntime_nativeSetDisplayFold 宿主上报折叠屏状态 (JSON, 字段
// 见 gfx/mobile.FoldInfo)。任意线程可调 (内部 gfx.Post 回 GUI 线程)。
//
// Kotlin 侧在 onConfigurationChanged / WindowInfoTracker 的回调里, 由
// androidx.window 的 FoldingFeature (bounds / orientation / state) 与
// WindowMetricsCalculator 的 widthDp 拼出:
//
//	{"display":"0","posture":"half-open","width":1000,"height":800,
//	 "widthClass":"expanded","heightClass":"regular",
//	 "hinge":{"x":480,"y":0,"width":40,"height":800,"orientation":"vertical"},
//	 "regions":[{"kind":"division","x":480,"y":0,"width":40,"height":800,"active":true}]}
//
// 判据: FoldingFeature.State.HALF_OPENED → posture=half-open + kind=division;
// State.FLAT → posture=flat + 同一条折痕但 width=0/active=false (这样
// hasFold 仍为 true, 界面列数不会在折叠/平展间跳)。isSeparating=false 的
// 折痕是 occlusion (铰链只遮住一小块, 不分割窗口) → kind=occlusion。
// 解析/校验/上报全部委托 gfx/mobile.ReportDisplayFold, 与 iOS 同一份实现。
//
//export Java_com_gox_GoxRuntime_nativeSetDisplayFold
func Java_com_gox_GoxRuntime_nativeSetDisplayFold(e *C.JNIEnv, clazz C.jclass, data C.jstring) {
	if err := mobile.ReportDisplayFold(goString(e, data)); err != nil {
		gfxandroid.Logf("nativeSetDisplayFold: %v", err)
	}
}

// goJSONObj 把导出函数收到的 jstring 解析成 *object.Object (失败返回 nil)。
func goJSONObj(e *C.JNIEnv, s C.jstring) *object.Object {
	v, err := hostbridge.JSONToObj(goString(e, s))
	if err != nil {
		return nil
	}
	o, _ := v.(*object.Object)
	return o
}

// jsonStr / jsonNum / jsonBool 是上报 JSON 的字段读取助手 (缺字段用缺省值)。
func jsonStr(o *object.Object, key, def string) string {
	if v, ok := o.GetProperty(key); ok {
		if s, ok := v.(*object.String); ok && s.Value != "" {
			return s.Value
		}
	}
	return def
}

func jsonNum(o *object.Object, key string, def float64) float64 {
	if v, ok := o.GetProperty(key); ok {
		if n, ok := v.(*object.Number); ok {
			return n.Value
		}
	}
	return def
}

func jsonBool(o *object.Object, key string, def bool) bool {
	if v, ok := o.GetProperty(key); ok {
		if b, ok := v.(*object.Boolean); ok {
			return b.Value
		}
	}
	return def
}

// jniEnv 取当前线程 JNIEnv (与 gfx/android 的 env 同一套附加/脱离纪律,
// 见 gox_h_env 的 C 注释)。**每次跨语言调用结束都必须脱离**。
func jniEnv() (*C.JNIEnv, func(), error) {
	var e *C.JNIEnv
	switch C.gox_h_env(&e) {
	case 0:
		return e, func() {}, nil
	case 1:
		return e, func() { C.gox_h_detach_current() }, nil
	default:
		return nil, func() {}, fmt.Errorf("android: 当前线程无法附加到 JVM (JNI_OnLoad 还没跑?)")
	}
}
