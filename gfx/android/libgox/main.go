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
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/14752222/Gox/gfx"
	gfxandroid "github.com/14752222/Gox/gfx/android"
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

	// 经 gfx.Post 投回 GUI 线程再报内核: ReportViewport 会同步跑脚本侧的
	// onViewportChange 订阅回调, 事件纪律 —— JNI 回调线程绝不直接执行 JS。
	gfx.Post(func() {
		gfx.ReportViewport(nil, gfx.Viewport{Insets: gfx.Insets{
			Top: int(top), Right: int(right), Bottom: int(bottom), Left: int(left),
		}})
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

	gfx.SetNativeHost(&jniNativeHost{})
}

// nativeHostRefs 取绑定好的宿主引用与方法 id (无绑定 → nil)。
func nativeHostRefs() (unsafe.Pointer, C.jmethodID, C.jmethodID) {
	nativeHostMu.Lock()
	defer nativeHostMu.Unlock()
	return nativeHostObj, nativeHostCapM, nativeHostCallM
}

// jniNativeHost 是 gfx.NativeHost 的 Android 实现: 把内核的 Call 翻译成一次
// JNI 调用。协议见文件头。
type jniNativeHost struct{}

// Capabilities 报告 Kotlin 宿主声明的方法名/能力名 (逗号分隔字符串 → 切片)。
func (h *jniNativeHost) Capabilities() []string {
	out := []string{}
	obj, capM, _ := nativeHostRefs()
	if obj == nil || capM == nil {
		return out
	}
	e, done, err := jniEnv()
	if err != nil {
		return out
	}
	defer done()
	res := C.gox_h_call0(e, C.jobject(obj), capM)
	if C.gox_h_ex(e) != 0 || unsafe.Pointer(res) == nil {
		return out
	}
	s := goString(e, C.jstring(res))
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pendingMarker 是 Kotlin 侧"稍后回填"的哨兵 (协议见文件头)。
const pendingMarker = "__pending__"

// Call 执行一个原生方法: 参数序列化成 JSON 传给 Kotlin, 回包按协议三态解析。
//
// 同步路径 (device.info / permission.get …) 会阻塞脚本线程直到 Kotlin 返回 ——
// 这正是内核要求的: 这几个方法只有同步返回值才会被 deviceHostOverlay /
// jsGetBrightness / jsOpenSystemSettings / refreshPermissionsFromHost 采用
// (native_device.go:212, native_permission.go:181 都是直接读 res.Result)。
// Kotlin 侧的同步实现都是纯内存/系统读, 不会卡住。
func (h *jniNativeHost) Call(method string, args object.Value) gfx.NativeCallResult {
	obj, _, callM := nativeHostRefs()
	if obj == nil || callM == nil {
		return gfx.NativeFailure(gfx.ErrUnsupported,
			"Android 宿主未注册 nativeCall (GoxNativeHost 缺失?), 方法 %s 不可用", method)
	}
	e, done, err := jniEnv()
	if err != nil {
		return gfx.NativeFailure(gfx.ErrPlatform, "NativeHost JNI 通道不可用: %v", err)
	}
	defer done()

	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	aj := C.CString(objToJSON(args))
	defer C.free(unsafe.Pointer(aj))

	res := C.gox_h_call2(e, C.jobject(obj), callM, C.gox_h_utf(e, cm), C.gox_h_utf(e, aj))
	if C.gox_h_ex(e) != 0 || unsafe.Pointer(res) == nil {
		return gfx.NativeFailure(gfx.ErrPlatform,
			"nativeCall(%s) 抛了异常或返回空", method)
	}
	return parseHostReply(goString(e, C.jstring(res)))
}

// parseHostReply 按 Kotlin 侧回包协议三态解析 (文件头: pending / 错误信封 / 结果)。
func parseHostReply(s string) gfx.NativeCallResult {
	s = strings.TrimSpace(s)
	if s == pendingMarker {
		return gfx.NativePending()
	}
	var envelope struct {
		ErrCode string `json:"__errCode"`
		ErrMsg  string `json:"__errMsg"`
	}
	if err := json.Unmarshal([]byte(s), &envelope); err == nil && envelope.ErrCode != "" {
		return gfx.NativeFailure(sanitizeErrCode(envelope.ErrCode), "%s", envelope.ErrMsg)
	}
	v, err := jsonToObj(s)
	if err != nil {
		return gfx.NativeFailure(gfx.ErrPlatform, "宿主返回了无法解析的 JSON: %v", err)
	}
	return gfx.NativeResult(v)
}

// sanitizeErrCode 把宿主给的错误码夹回 8 个统一错误码 —— 错误码是跨平台契约,
// 多出来的词一律归 platform-error, 不让平台私造词汇穿透到脚本侧。
func sanitizeErrCode(code string) string {
	switch code {
	case gfx.ErrUnsupported, gfx.ErrPermissionDenied, gfx.ErrCancelled,
		gfx.ErrTimeout, gfx.ErrBusy, gfx.ErrUnavailable,
		gfx.ErrPlatform, gfx.ErrInvalidArg:
		return code
	}
	return gfx.ErrPlatform
}

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
			nativeErr = &gfx.NativeError{Code: sanitizeErrCode(codeStr), Msg: msgStr}
		}
		var v object.Value
		if nativeErr == nil && resStr != "" {
			parsed, err := jsonToObj(resStr)
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

// ── JSON ↔ object.Value (args 下行 / 结果回填上行共用的窄转换) ──

// objToJSON 把内核给的参数对象序列化成 JSON 文本 (给 Kotlin org.json 解析)。
// 函数/闭包等不可序列化的值跳过或写成 null。
func objToJSON(v object.Value) string {
	var b strings.Builder
	writeJSON(&b, v)
	return b.String()
}

func writeJSON(b *strings.Builder, v object.Value) {
	switch x := v.(type) {
	case nil, *object.Null, *object.Undefined:
		b.WriteString("null")
	case *object.Boolean:
		if x.Value {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case *object.Number:
		b.WriteString(numToJSON(x.Value))
	case *object.String:
		b.WriteString(quoteJSON(x.Value))
	case *object.Array:
		b.WriteByte('[')
		for i, el := range x.Elements {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJSON(b, el)
		}
		b.WriteByte(']')
	case *object.Object:
		b.WriteByte('{')
		first := true
		for _, k := range x.Keys() {
			pv, ok := x.GetProperty(k)
			if !ok {
				continue
			}
			switch pv.(type) {
			case *object.Undefined, *object.Closure, *object.BuiltinFunction, *object.CompiledFunction:
				continue // undefined 与函数不进 JSON (与 JSON.stringify 语义一致)
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(quoteJSON(k))
			b.WriteByte(':')
			writeJSON(b, pv)
		}
		b.WriteByte('}')
	default:
		b.WriteString("null")
	}
}

// numToJSON 数字输出: NaN/Inf → null; 整数值不带小数点; 其余 'g' 紧凑格式。
func numToJSON(f float64) string {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "null"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e15 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// quoteJSON 带引号字符串 (控制字符与引号/反斜杠转义; 非 BMP 字符原样输出 UTF-8,
// JSON 允许)。
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString("\\\"")
		case '\\':
			b.WriteString("\\\\")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			if r < 0x20 {
				b.WriteString(fmt.Sprintf("\\u%04x", r))
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// jsonToObj 把宿主回包的 JSON 解析成 object.Value (UseNumber 保数字精度)。
func jsonToObj(s string) (object.Value, error) {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	return rawToValue(raw), nil
}

func rawToValue(raw interface{}) object.Value {
	switch x := raw.(type) {
	case nil:
		return object.NullSingleton
	case bool:
		return object.NewBoolean(x)
	case string:
		return object.NewString(x)
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return object.NullSingleton
		}
		return object.NewNumber(f)
	case []interface{}:
		arr := make([]object.Value, 0, len(x))
		for _, el := range x {
			arr = append(arr, rawToValue(el))
		}
		return object.NewArray(arr)
	case map[string]interface{}:
		o := object.NewObject()
		for k, v := range x {
			o.SetProperty(k, rawToValue(v))
		}
		return o
	}
	return object.NullSingleton
}

// goJSONObj 把导出函数收到的 jstring 解析成 *object.Object (失败返回 nil)。
func goJSONObj(e *C.JNIEnv, s C.jstring) *object.Object {
	v, err := jsonToObj(goString(e, s))
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
