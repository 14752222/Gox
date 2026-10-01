//go:build harmony

// Package harmony 是 Gox 在 HarmonyOS 上的 **NAPI 通道层**: 只负责"怎么把像素
// 交给宿主"与"怎么回调宿主", 不含任何交互逻辑 (那在 gfx/mobile, 可在开发机单测)。
//
// 三个包的分工 (与 Android 完全对称):
//
//	gfx/mobile          纯 Go: 事件队列 / 触摸映射 / 事件泵唤醒 / density 上报  ← 可单测
//	gfx/harmony         本包: NAPI 通道 (ArrayBuffer 写入、flush 回调、错误码翻译)
//	gfx/harmony/libgox  package main: //export 出来的 NAPI 入口 + 装配 (Surface + VM)
//
// ── 与 Android 通道层的三处结构性差异 (照抄会踩) ───────────────────────────
//
//  1. **没有线程附加这回事**。JNI 要从 Go 线程回调 Java 必须先
//     AttachCurrentThread; NAPI 不需要 —— `napi_env` 在 libgox 初始化时拿到并
//     存下来, 之后任何线程都能用它调 `napi_call_function`。所以本包没有
//     env()/detach 那一整套, 但仍必须把线程纪律守住: **回调只在 GUI 线程发起**
//     (见下面 flush 的注释)。
//  2. **跨语言引用要手动管生命周期**。JNI 用 NewGlobalRef 让对象活过调用;
//     NAPI 的 `napi_ref` 同理, 但**它是线程安全的引用**这件事要靠
//     `napi_create_reference` + 显式 delete。本包用 napi_ref 持住宿主回调对象。
//  3. **异常不叫异常**。NAPI 用 `napi_status` 返回值 + `napi_get_and_clear_last_exception`。
//     跨语言调用后一定检查 status —— 静默的 status 忽略是这类桥接最常见的坑。
//
// ── ArkTS 侧需要满足的契约 (改动要三处同步) ───────────────────────────────
//
// 宿主用一个对象承接回调 (arkts 的 `TextEncoder`/`Uint8Array` 都是 JS 内置, 不
// 需要额外导入):
//
//	// 由 Go 调, 都是同步的
//	interface GoxHost {
//	  /** 把屏幕刷新到 UI。rects 为 undefined = 整帧, 否则是 [x,y,w,h,...] 像素矩形。 */
//	  flush(rects?: Int32Array): void
//	  /** 脚本跑完 (含异常) 时调。 */
//	  finished(code: number, error?: string): void
//	  /** 软键盘开关 (M2)。 */
//	  imeShow(show: boolean): void
//	  /** 脚本侧的原生调用下行。 */
//	  nativeCapabilities(): string
//	  nativeCall(method: string, argsJson: string): string
//	}
//
// frameBuffer 必须是 **ArrayBuffer**（Go 侧用 `napi_get_arraybuffer_info` 拿裸地址
// 直写, 零拷贝）。ArkTS 侧拿到 buffer 后包成 PixelMap 上屏 —— 用的就是
// `OH_NativeXComponent` 给的 native window (见 libgox/main.go 的
// OnSurfaceCreated)。
package harmony

/*
#cgo LDFLAGS: -lace_napi.z -lhilog_ndk.z

#include <node_api.h>
#include <hilog/log.h>
#include <stdlib.h>
#include <string.h>

// gox_log_write 把一条消息写进 hilog (tag "Gox")。
//
// 为什么不直接用 os.Stderr: 鸿蒙上**应用进程的 stdout/stderr 在真机上并不通向
// 任何能看到的地方** —— 原生代码往 fd 2 写的字节查不出来, 而这类日志恰恰是首帧
// 失败时唯一能说明原因的东西。tag 固定 "Gox"、domain 取 0x0, 与 ArkTS 侧
// hilog.info(0x0, 'Gox', ...) 一致 ⇒ `hdc shell hilog -T Gox` 一网打尽。
//
// 注意用 OH_LOG_Print 而不是 OH_LOG_INFO 宏: 宏展开依赖 LOG_DOMAIN/LOG_TAG 两个
// 编译期定义, 在 cgo 的注释头里必须自己 #define, 直接调函数更少一层隐形约定。
static void gox_log_write(const char *msg) {
	OH_LOG_Print(LOG_APP, LOG_INFO, 0x0, "Gox", "%{public}s", msg);
}

// gox_copy_pixels 走 libc memcpy。分出来只是为了和 android.go 的形状对齐
// (那边用它绕开 cgo 对切片指针的限制), 这里也留一层, 免得后人两边对不上。
static void gox_copy_pixels(void *dst, const void *src, int n) {
	memcpy(dst, src, (size_t)n);
}

// ── NAPI 助手 ──
//
// 全部返回 napi_status 或裸指针, 由 Go 侧判空/判状态 —— 这里不做 "自动报错"，
// 因为一个失败往往要在 Go 侧决定"重试还是静默降级"。
static napi_status gox_napi_status_ok(void) { return napi_ok; }

// gox_arraybuffer_data 取 ArrayBuffer 的裸地址 + 长度。
// 返回值: napi_ok 且 *out != NULL 才算真拿到。SharedArrayBuffer / 已 detach 的
// buffer 都会给 NULL —— Go 侧据此拒绝绑定, 而不是往空指针写。
static napi_status gox_arraybuffer_data(napi_env env, napi_value v, void **out, size_t *len) {
	*out = NULL;
	*len = 0;
	bool isAb = false;
	napi_status st = napi_is_arraybuffer(env, v, &isAb);
	if (st != napi_ok || !isAb) {
		return napi_invalid_arg;
	}
	return napi_get_arraybuffer_info(env, v, out, len);
}

// gox_create_ref / gox_get_ref / gox_delete_ref 是宿主回调对象的生命周期。
// 用 napi_ref 而不是裸 napi_value: 后者的有效期只到本次 native 调用返回为止,
// 存下来下次用就是悬垂引用 (症状是偶发崩溃, 且栈里看不出跟 JS 有关)。
static napi_status gox_create_ref(napi_env env, napi_value v, napi_ref *out) {
	return napi_create_reference(env, v, 1, out);
}
static napi_status gox_get_ref_value(napi_env env, napi_ref ref, napi_value *out) {
	return napi_get_reference_value(env, ref, out);
}
static napi_status gox_delete_ref(napi_env env, napi_ref ref) {
	return napi_delete_reference(env, ref);
}

// gox_call_fn 调一个 JS 函数 (napi_call_function 的薄封装)。
// recv 传 undefined 即可 (宿主回调不依赖 this)。
static napi_status gox_call_fn(napi_env env, napi_value fn, size_t argc,
                               const napi_value *argv, napi_value *result) {
	napi_value undef = NULL;
	napi_get_undefined(env, &undef);
	return napi_call_function(env, undef, fn, argc, argv, result);
}

// gox_get_prop 取对象属性 (getter 抛异常时返回非 napi_ok)。
static napi_status gox_get_prop(napi_env env, napi_value obj, const char *name, napi_value *out) {
	return napi_get_named_property(env, obj, name, out);
}

// gox_typeof_is_function 判断某个属性是不是可调用的函数 —— 宿主没实现
// imeShow 时是 undefined, 直接调会抛 TypeError。
static bool gox_is_function(napi_env env, napi_value v) {
	napi_valuetype t = napi_undefined;
	if (napi_typeof(env, v, &t) != napi_ok) {
		return false;
	}
	return t == napi_function;
}

// gox_create_string 建 JS 字符串 (返回值用完由 GC 管, 不需要手动释放)。
static napi_status gox_create_string(napi_env env, const char *s, napi_value *out) {
	return napi_create_string_utf8(env, s, NAPI_AUTO_LENGTH, out);
}

// gox_create_int32_array 建 Int32Array (给 flush 传脏区)。
// 用 TypedArray 而不是普通数组: ArkTS 侧拿到就能直接读, 不必再包一层。
static napi_status gox_create_int32_array(napi_env env, const int32_t *vals, size_t n, napi_value *out) {
	void *data = NULL;
	napi_status st = napi_create_arraybuffer(env, n * sizeof(int32_t), &data, out);
	if (st != napi_ok || data == NULL) {
		return st;
	}
	if (n > 0) {
		memcpy(data, vals, n * sizeof(int32_t));
	}
	// 用 napi_create_typedarray 把 ArrayBuffer 包成 Int32Array 视图。
	napi_value ab = *out;
	return napi_create_typedarray(env, napi_int32_array, n, ab, 0, out);
}

// gox_console_log 是"最后一道日志": 只在 hilog 调用本身有问题时才需要它,
// 保留这个口子方便排查 hilog 没输出时到底有没有走到 C 侧。
static void gox_console_log(napi_env env, const char *msg) {
	napi_value global = NULL;
	if (napi_get_global(env, &global) != napi_ok) {
		return;
	}
	napi_value console = NULL;
	if (napi_get_named_property(env, global, "console", &console) != napi_ok) {
		return;
	}
	napi_value log = NULL;
	if (napi_get_named_property(env, console, "log", &log) != napi_ok) {
		return;
	}
	napi_value s = NULL;
	if (napi_create_string_utf8(env, msg, NAPI_AUTO_LENGTH, &s) != napi_ok) {
		return;
	}
	napi_value res = NULL;
	napi_call_function(env, console, log, 1, &s, &res);
}
*/
import "C"

import (
	"fmt"
	"image"
	"os"
	"sync"
	"unsafe"

	"github.com/14752222/Gox/gfx/mobile"
)

// napi_status 的常用取值 (与 node_api_types.h 对齐, 避免在 Go 侧到处写 C.xxx)。
const (
	statusOK = 0 // napi_ok
)

var (
	mu sync.Mutex
	// hostRef 是宿主回调对象的 JS 函数集合 (经 napi_ref 持住)。
	// 用 napi_ref 而不是 napi_value: 见 C 侧 gox_create_ref 的注释。
	hostRef C.napi_ref
	// hostFields 是宿主对象上各个回调的**函数值**。这些值只在下行调用 (GUI 线程)
	// 时用, 不在别的线程碰 —— 见 flush 的注释。
	hostFnFlush C.napi_value
	hostFnFin   C.napi_value
	hostFnIME   C.napi_value
	hostFnCap   C.napi_value
	hostFnCall  C.napi_value
	envRef      C.napi_env // 初始化时存下, 之后跨线程用

	// frameBuf 是 ArkTS 侧 new ArrayBuffer(w*h*4) 那块内存的 JS 引用,
	// framePtr 是它的裸地址 —— 引擎直接往里写, 零拷贝。
	frameBuf C.napi_ref
	framePtr unsafe.Pointer
	frameCap int

	warnedNoBuffer bool
	warnedFlushErr bool
)

// Logf 写一条日志到 hilog (tag "Gox"), 同时照旧写一份 stderr —— 真机上 stderr
// 查不到 (见 C 侧 gox_log_write 的注释), 但在开发机的桌面测试/别的宿主里它还能
// 被捕获, 两边都留着成本为零。
//
// 为什么需要它: gfx/harmony 里所有 "跳过了、失败了" 的分支都是**静默降级**,
// 不留痕迹就等于 (例如) 帧缓冲没绑上却只看到一块黑屏。
func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "gox: "+msg)
	cs := C.CString(msg)
	C.gox_log_write(cs)
	C.free(unsafe.Pointer(cs))
}

// SetEnv 保存 libgox 初始化时拿到的 napi_env。
//
// **与 Android 的 SetJavaVM 对应, 但不是一回事**: 这里存的 env 是一次就够的
// 上下文句柄, 不需要"附加当前线程"。它必须在 ArkTS 的 native 入口 (napi_module
// 的 register 函数) 里调用。
func SetEnv(env unsafe.Pointer) {
	mu.Lock()
	envRef = C.napi_env(env)
	mu.Unlock()
}

// Env 返回保存下来的 napi_env (未初始化返回 nil)。libgox 的 //export 入口需要
// 它才能把 Go 侧的结果送回 JS。
func Env() unsafe.Pointer {
	mu.Lock()
	defer mu.Unlock()
	return unsafe.Pointer(envRef)
}

// BindHost 绑定宿主回调对象 (ArkTS 的 GoxHost 实例)。
//
// **必须在有 napi_env 的线程上调用** (即 ArkTS 调 nativeInit 的那个调用栈里),
// 之后从 Go 线程调 napi_call_function 只依赖 env 本身, 不再需要别的附加动作。
func BindHost(host unsafe.Pointer) error {
	env := C.napi_env(envVal())
	if env == nil {
		return fmt.Errorf("harmony: napi_env 尚未设置 (SetEnv 没跑?)")
	}
	obj := C.napi_value(host)
	if obj == nil {
		return fmt.Errorf("harmony: GoxHost 不能为空")
	}

	// 先建成 ref, 之后所有取值都从这个 ref 走 —— 否则本次调用一返回, obj 就失效。
	var ref C.napi_ref
	if st := C.gox_create_ref(env, obj, &ref); C.int(st) != statusOK || ref == nil {
		return fmt.Errorf("harmony: napi_create_reference(GoxHost) 失败 (status=%d)", int(st))
	}
	root, err := refValue(env, ref)
	if err != nil {
		return err
	}

	// flush 是必需的 (没有它等于渲染结果上不了屏), 缺了就当绑定失败。
	flushFn, err := prop(env, root, "flush")
	if err != nil || !C.gox_is_function(env, flushFn) {
		// 注意 C.bool 在 Go 侧是 C._Bool, 不能直接和 Go 的 true 比较,
		// 用 bool(...) 转一道。
		return fmt.Errorf("harmony: GoxHost.flush 不是函数 (ArkTS 侧签名必须逐字符对上)")
	}

	// 其余三个是可选能力: 缺了就降级, 但要留痕 (老宿主 / 半成品壳工程)。
	fin, _ := prop(env, root, "finished")
	ime, _ := prop(env, root, "imeShow")
	cap, _ := prop(env, root, "nativeCapabilities")
	cal, _ := prop(env, root, "nativeCall")

	if !bool(C.gox_is_function(env, fin)) {
		Logf("GoxHost.finished 缺失: 脚本结束不会通知宿主 (壳工程可能不会自己退出)")
	}
	if !bool(C.gox_is_function(env, ime)) {
		Logf("GoxHost.imeShow 缺失: 软键盘不可开关")
	}
	if !bool(C.gox_is_function(env, cal)) {
		Logf("GoxHost.nativeCall 缺失: 原生能力不可用 (GoxNativeHost.ets 缺失?)")
	}

	mu.Lock()
	// 重复 bind 时先放掉旧的 ref, 否则泄漏一个引用 (M1 期间反复热重载很常见)。
	if hostRef != nil {
		C.gox_delete_ref(env, hostRef)
	}
	hostRef = ref
	hostFnFlush, hostFnFin, hostFnIME = flushFn, fin, ime
	hostFnCap, hostFnCall = cap, cal
	mu.Unlock()
	return nil
}

// BindFrameBuffer 绑定 ArkTS 侧的 ArrayBuffer 并取到它的裸地址。
//
// **必须是 ArrayBuffer**, 而不是任意 TypedArray 的视图 —— 视图的 byteOffset 会
// 让"裸地址 + 长度"算错 (症状是画面整体错位一行)。Go 侧调用方拿到的应当就是
// ArkTS `new ArrayBuffer(n)` 的结果, 或者是 `u8.buffer`。
func BindFrameBuffer(buf unsafe.Pointer, capBytes int) error {
	env := C.napi_env(envVal())
	if env == nil {
		return fmt.Errorf("harmony: napi_env 尚未设置 (SetEnv 没跑?)")
	}
	v := C.napi_value(buf)
	if v == nil {
		return fmt.Errorf("harmony: frameBuffer 不能为空")
	}

	var data unsafe.Pointer
	var length C.size_t
	if st := C.gox_arraybuffer_data(env, v, &data, &length); C.int(st) != statusOK || data == nil {
		return fmt.Errorf("harmony: frameBuffer 必须是 ArrayBuffer 且未 detach (status=%d)", int(st))
	}
	if int(length) < capBytes {
		return fmt.Errorf("harmony: ArrayBuffer 太小: %d < %d", int(length), capBytes)
	}

	var ref C.napi_ref
	if st := C.gox_create_ref(env, v, &ref); C.int(st) != statusOK || ref == nil {
		return fmt.Errorf("harmony: napi_create_reference(frameBuffer) 失败")
	}

	// 换地址与新引用在**同一段临界区**里落地, 且 uploadFrame 是持同一把锁做整段
	// 拷贝的 —— 所以这里一定等"正往旧地址写的那一帧"写完才换。这与 Android 侧
	// 的做法逐字一致 (那里是"旧缓冲区失去最后一个 JNI 全局引用"的镜像问题)。
	mu.Lock()
	if frameBuf != nil {
		// 老的 ArrayBuffer 由 JS 侧 GC 管; 这里只是放掉我们的引用计数。
		C.gox_delete_ref(env, frameBuf)
	}
	frameBuf, framePtr, frameCap = ref, data, capBytes
	mu.Unlock()
	return nil
}

// Uploader 返回实现 gfx/mobile.Uploader 的上屏钩子: 把 RGBA 写进 ArkTS 的
// ArrayBuffer, 再调一次 GoxHost.flush(脏区), 让宿主把 PixelMap 刷到屏幕。
//
// **调用时机**: 引擎在 GUI 线程渲染完一帧后调它 —— 而 napi_call_function 是
// 线程无关的, 所以这里不需要像 JNI 那样附加。但**绝不能**从别的线程直接调
// (那会让 JS 代码在非 JS 线程上跑): 本函数只被渲染路径调用, 渲染路径与事件泵
// 同在 GUI 线程。
func Uploader() mobile.Uploader { return uploadFrame }

func uploadFrame(img *image.RGBA, rects []image.Rectangle) {
	if img == nil {
		return
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	need := w * h * 4

	// 整段拷贝都在锁里 —— BindFrameBuffer 换地址时拿的是同一把锁。
	// 锁不能跨到 flush: flush 自己要拿 mu 读回调句柄, 而 Go 的 Mutex 不可重入。
	mu.Lock()
	ptr, capacity := framePtr, frameCap
	ok := ptr != nil && capacity >= need
	if ok {
		// 按行拷: img.Stride 是分配对齐后的行宽, 可能大于 w*4, 整体 memcpy 会把
		// 行尾填充算进画面 (表现为图像斜切)。
		row := w * 4
		for y := 0; y < h; y++ {
			dst := unsafe.Pointer(uintptr(ptr) + uintptr(y*row))
			src := unsafe.Pointer(&img.Pix[y*img.Stride])
			C.gox_copy_pixels(dst, src, C.int(row))
		}
	}
	mu.Unlock()

	if !ok {
		// 只出声一次: 每次上屏都打会把 hilog 淹掉。
		if !warnedNoBuffer {
			warnedNoBuffer = true
			Logf("帧缓冲不可用 (cap=%d need=%d), 上屏被跳过; 检查 nativeInit 是否已调用", capacity, need)
		}
		return
	}
	flush(rects)
}

// flush 通知宿主上屏。没有宿主回调时静默跳过 (init 与 start 之间会这样)。
func flush(rects []image.Rectangle) {
	env := C.napi_env(envVal())
	if env == nil {
		return
	}

	mu.Lock()
	fn := hostFnFlush
	mu.Unlock()
	if fn == nil {
		return
	}

	var arg C.napi_value
	if len(rects) > 0 {
		n := len(rects) * 4
		vals := make([]C.int32_t, n)
		// 矩形用 image.Rectangle 的 Min/Max 语义: 输出 [x, y, w, h] 给宿主做局部
		// 刷新。image.Rectangle 没有 X/Y/W/H 字段, 别照 GDK 的习惯写。
		for i, r := range rects {
			vals[i*4+0] = C.int32_t(r.Min.X)
			vals[i*4+1] = C.int32_t(r.Min.Y)
			vals[i*4+2] = C.int32_t(r.Dx())
			vals[i*4+3] = C.int32_t(r.Dy())
		}
		if st := C.gox_create_int32_array(env, &vals[0], C.size_t(n), &arg); C.int(st) != statusOK {
			Logf("flush: 建脏区数组失败 (status=%d), 本次按整帧上屏", int(st))
			arg = nil
		}
	}
	// arg 为 nil 时传 undefined: ArkTS 侧 `flush(rects?: Int32Array)` 会收到
	// undefined ⇒ 整帧刷新。这是设计里的两条路径, 不是错误分支。
	var argv *C.napi_value
	argc := C.size_t(0)
	if arg != nil {
		argv = &arg
		argc = 1
	}
	var result C.napi_value
	st := C.gox_call_fn(env, fn, argc, argv, &result)
	if C.int(st) != statusOK {
		clearException(env)
		if !warnedFlushErr {
			warnedFlushErr = true
			Logf("GoxHost.flush 调用失败 (status=%d, 异常已清除), 上屏可能停在旧帧", int(st))
		}
	}
}

// CallIMEShow 通知宿主开/收软键盘 (焦点进/出编辑框时内核调)。
// 没绑到 imeShow (老宿主) 时静默跳过。
func CallIMEShow(on bool) {
	env := C.napi_env(envVal())
	if env == nil {
		return
	}
	mu.Lock()
	fn := hostFnIME
	mu.Unlock()
	if fn == nil || !bool(C.gox_is_function(env, fn)) {
		return
	}
	arg, err := boolValue(env, on)
	if err != nil {
		return
	}
	var result C.napi_value
	if C.int(C.gox_call_fn(env, fn, 1, &arg, &result)) != statusOK {
		clearException(env)
	}
}

// NotifyFinished 通知宿主脚本已结束 (errMsg 为空表示正常结束)。
func NotifyFinished(code int, errMsg string) {
	env := C.napi_env(envVal())
	if env == nil {
		return
	}
	mu.Lock()
	fn := hostFnFin
	mu.Unlock()
	if fn == nil || !bool(C.gox_is_function(env, fn)) {
		return
	}
	c1, err := intValue(env, code)
	if err != nil {
		return
	}
	// finished(code: number, error?: string) —— 正常结束时不传第二个参数,
	// 让 ArkTS 侧的 `error?: string` 保持 undefined (与 Android 传 null 对应)。
	argv := make([]C.napi_value, 0, 2)
	argv = append(argv, c1)
	if errMsg != "" {
		c2, err := strValue(env, errMsg)
		if err != nil {
			return
		}
		argv = append(argv, c2)
	}
	var result C.napi_value
	if C.int(C.gox_call_fn(env, fn, C.size_t(len(argv)), &argv[0], &result)) != statusOK {
		clearException(env)
	}
}

// CallHost 调宿主对象上的一个回调 (按名字), 传入若干字符串参数, 返回字符串。
//
// 这是 libgox 装配层实现 gfx.NativeHost 用的底层: **Capabilities() 与 Call() 都
// 走它** —— 宿主侧的方法名 (nativeCapabilities / nativeCall) 只在装配层出现一次,
// 这里保持"按名字调"的通用形状。
//
// 为什么要变参: 宿主协议里 `nativeCall` 是 (method, argsJson) **两个**参数, 与
// Android 的 JNI 签名 `nativeCall(String,String)String` 逐字对应。早期版本只传
// 一个参数 (把 method 与 args 合成一个信封), 那会让两端的协议表对不上 ——
// "文档里写着一套、代码里跑着另一套"是这类跨平台桥最贵的债。
//
// 线程: 由调用方保证在 GUI 线程 (内核的 Call 本来就从 GUI 线程来)。
func CallHost(name string, args ...string) (string, error) {
	env := C.napi_env(envVal())
	if env == nil {
		return "", fmt.Errorf("harmony: napi_env 尚未设置")
	}
	mu.Lock()
	root := hostRef
	mu.Unlock()
	if root == nil {
		return "", fmt.Errorf("harmony: 宿主未绑定")
	}
	obj, err := refValue(env, root)
	if err != nil {
		return "", err
	}
	fn, err := prop(env, obj, name)
	if err != nil {
		return "", err
	}
	if !bool(C.gox_is_function(env, fn)) {
		return "", fmt.Errorf("harmony: 宿主没有实现 %s", name)
	}

	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	argv := make([]C.napi_value, 0, len(args))
	for _, a := range args {
		v, err := strValue(env, a)
		if err != nil {
			return "", err
		}
		argv = append(argv, v)
	}
	var argvPtr *C.napi_value
	if len(argv) > 0 {
		argvPtr = &argv[0]
	}
	var result C.napi_value
	if st := C.gox_call_fn(env, fn, C.size_t(len(argv)), argvPtr, &result); C.int(st) != statusOK {
		clearException(env)
		return "", fmt.Errorf("harmony: 调 %s 失败 (status=%d)", name, int(st))
	}
	return goString(env, result), nil
}

// ===== 内部助手 =====
//
// cgo 的 napi_env / napi_value / napi_ref 都是"底层指针的具名类型", 不能与 nil
// 直接比较 —— 统一在 envVal / 这些助手函数里转成 unsafe.Pointer 再判。

func envVal() unsafe.Pointer {
	mu.Lock()
	defer mu.Unlock()
	return unsafe.Pointer(envRef)
}

func refValue(env C.napi_env, ref C.napi_ref) (C.napi_value, error) {
	var v C.napi_value
	if st := C.gox_get_ref_value(env, ref, &v); C.int(st) != statusOK || v == nil {
		return nil, fmt.Errorf("harmony: napi_get_reference_value 失败 (status=%d)", int(st))
	}
	return v, nil
}

func prop(env C.napi_env, obj C.napi_value, name string) (C.napi_value, error) {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var out C.napi_value
	if st := C.gox_get_prop(env, obj, cn, &out); C.int(st) != statusOK {
		clearException(env)
		return nil, fmt.Errorf("harmony: 读属性 %s 失败 (status=%d)", name, int(st))
	}
	return out, nil
}

func strValue(env C.napi_env, s string) (C.napi_value, error) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	var out C.napi_value
	if st := C.gox_create_string(env, cs, &out); C.int(st) != statusOK {
		return nil, fmt.Errorf("harmony: 建字符串失败 (status=%d)", int(st))
	}
	return out, nil
}

func intValue(env C.napi_env, n int) (C.napi_value, error) {
	var out C.napi_value
	if st := C.napi_create_int32(env, C.int32_t(n), &out); C.int(st) != statusOK {
		return nil, fmt.Errorf("harmony: 建 int32 失败 (status=%d)", int(st))
	}
	return out, nil
}

func boolValue(env C.napi_env, b bool) (C.napi_value, error) {
	var out C.napi_value
	cv := C.bool(false)
	if b {
		cv = C.bool(true)
	}
	if st := C.napi_get_boolean(env, cv, &out); C.int(st) != statusOK {
		return nil, fmt.Errorf("harmony: 建 boolean 失败 (status=%d)", int(st))
	}
	return out, nil
}

// goString 把 napi_value 里的字符串取成 Go 字符串。非字符串值返回空串。
func goString(env C.napi_env, v C.napi_value) string {
	if env == nil || v == nil {
		return ""
	}
	// napi_valuetype 必须**显式声明类型**, 不能用 `:=` 从常量推导:
	// cgo 把 C 的枚举**常量**映射成 Go 的 int, 而把枚举**类型的指针参数**
	// 要求 C.napi_valuetype —— 于是 `t := C.napi_undefined` 推出 int,
	// 传给 napi_typeof 就报 "cannot use &t (value of type *int)"。
	// 这个报错读起来像"头文件里没有这个类型", 实际只是少了类型标注。
	var t C.napi_valuetype = C.napi_undefined
	if C.int(C.napi_typeof(env, v, &t)) != statusOK || t != C.napi_string {
		return ""
	}
	var n C.size_t
	if C.int(C.napi_get_value_string_utf8(env, v, nil, 0, &n)) != statusOK || n == 0 {
		return ""
	}
	buf := make([]byte, int(n)+1)
	if C.int(C.napi_get_value_string_utf8(env, v, (*C.char)(unsafe.Pointer(&buf[0])), n+1, &n)) != statusOK {
		return ""
	}
	return string(buf[:int(n)])
}

// clearException 清掉挂起的 JS 异常并留痕。
//
// 跨语言调用出错时宁可继续渲染, 也不能让悬空异常在下一次 NAPI 调用上炸掉 ——
// 但必须留一条日志, 否则"画面停在旧帧且没有任何提示"是没法查的。
func clearException(env C.napi_env) {
	if env == nil {
		return
	}
	var thrown C.napi_value
	if C.int(C.napi_get_and_clear_last_exception(env, &thrown)) != statusOK || thrown == nil {
		return
	}
	if msg := goString(env, thrown); msg != "" {
		Logf("宿主回调抛了异常 (已清除): %s", msg)
	} else {
		Logf("宿主回调抛了异常 (已清除, 消息非字符串)")
	}
}

// ConsoleLog 把一条消息同时送 hilog 与 JS console —— 排查"hilog 里什么都没有"
// 时用它区分"没走到 C"与"hilog 没输出"。
func ConsoleLog(msg string) {
	env := C.napi_env(envVal())
	if env == nil {
		return
	}
	cs := C.CString(msg)
	defer C.free(unsafe.Pointer(cs))
	C.gox_console_log(env, cs)
}
