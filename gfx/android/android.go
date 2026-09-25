//go:build android

// Package android 是 Gox 在 Android 上的 **JNI 通道层**: 只负责"怎么把像素交给
// Java"与"怎么回调 Java", 不含任何交互逻辑 (那在 gfx/mobile, 可在开发机单测)。
//
// 三个包的分工:
//
//	gfx/mobile          纯 Go: 事件队列 / 触摸映射 / 事件泵唤醒 / density 上报  ← 可单测
//	gfx/android         本包: JNI 通道 (JVM 附加、direct ByteBuffer 写入、flush 回调)
//	gfx/android/libgox  package main: //export 出来的 JNI 入口 + 装配 (Surface + VM)
//
// ── Kotlin 侧需要提供的两个类 (契约, 改动要三处同步) ───────────────────────
//
//	package com.gox
//
//	/** 宿主回调: 由 Go 调。 */
//	class GoxHost {
//	    /** 把屏幕刷新到 UI: rects 为 null=整帧, 否则是 [x,y,w,h, ...] 像素矩形。 */
//	    fun flush(rects: IntArray?)
//	    /** 脚本跑完 (含异常) 时调, Kotlin 据此结束 Activity。 */
//	    fun finished(code: Int, error: String?)
//	}
//
//	/** 引擎入口: 方法体由 libgox.so 提供 (external)。 */
//	class GoxRuntime {
//	    external fun nativeInit(width: Int, height: Int, density: Float,
//	                            frameBuffer: java.nio.ByteBuffer, host: GoxHost)
//	    external fun nativeBindFrameBuffer(w: Int, h: Int, frameBuffer: java.nio.ByteBuffer)
//	    external fun nativeTick()                       // Choreographer 每帧一次
//	    external fun nativeTouch(action: Int, x: Float, y: Float)
//	    external fun nativeKey(key: String, down: Boolean)
//	    external fun nativeResize(w: Int, h: Int, density: Float)
//	    external fun nativeDestroy()
//	    /** 跑一段脚本 (立即返回, 真正执行在 Go 自己的线程上)。 */
//	    external fun nativeRunScript(source: String, name: String)
//	}
//
// frameBuffer 必须是 `ByteBuffer.allocateDirect(w*h*4)`。渲染时 Go 直接把 RGBA
// 写进这块内存 (零拷贝), 然后调 `flush` —— Kotlin 侧用
// `Bitmap.copyPixelsFromBuffer` + `invalidate(脏区)` 上屏, 这就是方案里说的
// "一次额外拷贝"。
package android

/*
#cgo LDFLAGS: -llog
#include <jni.h>
#include <android/log.h>
#include <stdlib.h>
#include <string.h>

// gox_log_write 把一条消息写进 logcat。
//
// 为什么不直接用 os.Stderr: Android 上 **zygote 在 fork 应用进程前把 stdout/stderr
// 接到了 /dev/null** —— 原生代码往 fd 2 写的字节在真机上人间蒸发, 而这类日志恰恰是
// 首帧失败时唯一能说明原因的东西 ("黑屏 + 无日志" 是没法查的)。
// tag 固定 "Gox", 跟 Kotlin 侧 Log.i(TAG) 一致 ⇒ `adb logcat -s Gox:I` 一网打尽。
static void gox_log_write(const char *msg) {
	__android_log_write(ANDROID_LOG_INFO, "Gox", msg);
}

static JavaVM *gox_jvm = NULL;

static void gox_set_jvm(JavaVM *vm) { gox_jvm = vm; }

// gox_env 取当前线程的 JNIEnv; 返回值: 0 = 本来就有, 1 = 本次附加 (用完要 detach),
// -1 = 拿不到 (JVM 没注册 / 附加失败)。渲染线程是 Go 自己起的, 不在 JVM 里,
// 所以这里的 AttachCurrentThread 是必需的 —— 少了它, 第一次上屏就会 JNI 崩溃。
static int gox_env(JNIEnv **env) {
	if (gox_jvm == NULL) {
		return -1;
	}
	jint r = (*gox_jvm)->GetEnv(gox_jvm, (void **)env, JNI_VERSION_1_6);
	if (r == JNI_OK) {
		return 0;
	}
	if (r == JNI_EDETACHED) {
		if ((*gox_jvm)->AttachCurrentThread(gox_jvm, env, NULL) != JNI_OK) {
			return -1;
		}
		return 1;
	}
	return -1;
}

static void gox_detach_current(void) {
	if (gox_jvm != NULL) {
		(*gox_jvm)->DetachCurrentThread(gox_jvm);
	}
}

static jobject gox_object_class(JNIEnv *env, jobject o) {
	return (*env)->GetObjectClass(env, o);
}
static jobject gox_new_global_ref(JNIEnv *env, jobject o) {
	return (*env)->NewGlobalRef(env, o);
}
static void *gox_direct_buffer(JNIEnv *env, jobject b) {
	return (*env)->GetDirectBufferAddress(env, b);
}
static jintArray gox_new_int_array(JNIEnv *env, jsize n) {
	return (*env)->NewIntArray(env, n);
}
static void gox_set_int_region(JNIEnv *env, jintArray a, jsize start, jsize len, const jint *buf) {
	(*env)->SetIntArrayRegion(env, a, start, len, buf);
}
static void gox_call_void(JNIEnv *env, jobject o, jmethodID m, jobject arg) {
	(*env)->CallVoidMethod(env, o, m, arg);
}
static void gox_call_void_int_str(JNIEnv *env, jobject o, jmethodID m, jint i, jstring s) {
	(*env)->CallVoidMethod(env, o, m, i, s);
}
static void gox_call_void_bool(JNIEnv *env, jobject o, jmethodID m, jboolean b) {
	(*env)->CallVoidMethod(env, o, m, b);
}
// 方法名与签名写死在 C 里, 免得每次绑定都 C.CString 一遍再想不起来 free。
static jmethodID gox_flush_mid(JNIEnv *env, jobject c) {
	return (*env)->GetMethodID(env, c, "flush", "([I)V");
}
static jmethodID gox_finished_mid(JNIEnv *env, jobject c) {
	return (*env)->GetMethodID(env, c, "finished", "(ILjava/lang/String;)V");
}
// 软键盘开关 (M2): GoxHost.imeShow(show: Boolean) —— 找不到不算错 (老宿主
// 没有, 降级成"软键盘不可开关"), 用 gox_clear_exception 兜住。
static jmethodID gox_ime_mid(JNIEnv *env, jobject c) {
	return (*env)->GetMethodID(env, c, "imeShow", "(Z)V");
}
static jstring gox_new_utf(JNIEnv *env, const char *utf) {
	return (*env)->NewStringUTF(env, utf);
}
// gox_clear_exception 清掉挂起异常并报告是否清过。跨语言调用出错时宁可继续渲染,
// 也不能让悬空异常在下一次 JNI 调用上炸掉整个进程 —— 但必须留下痕迹。
static jint gox_clear_exception(JNIEnv *env) {
	if ((*env)->ExceptionCheck(env)) {
		(*env)->ExceptionClear(env);
		return 1;
	}
	return 0;
}
static void gox_copy_pixels(void *dst, const void *src, int n) {
	memcpy(dst, src, (size_t)n);
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

// cgo 的 jobject / jmethodID 在 Go 侧分别是 unsafe.Pointer / uintptr,
// 都不能和 nil 比较 —— 所以句柄一律以 unsafe.Pointer 保存, 用前再转回去。
var (
	mu sync.Mutex
	// hostObj / flushMID / finMID 是宿主回调 GoxHost 的全局引用与方法 id;
	// imeMID 是软键盘开关 (老宿主没有则为 nil, 降级)
	hostObj  unsafe.Pointer
	flushMID C.jmethodID
	finMID   C.jmethodID
	imeMID   C.jmethodID
	// frameBuf 是 Java 侧 allocateDirect 的那块内存 (全局引用, 只为持有它),
	// framePtr 是它的裸地址 —— 引擎直接往里写。
	frameBuf unsafe.Pointer
	framePtr unsafe.Pointer
	frameCap int

	warnedNoBuffer bool
	warnedFlushErr bool
)

// Logf 写一条日志到 logcat (tag "Gox"), 同时照旧写一份 stderr —— 真机上 stderr
// 是 /dev/null (见 C 侧 gox_log_write 的注释), 但在开发机的桌面测试/别的宿主里
// 它还能被捕获, 两边都留着成本为零。
//
// 为什么需要它: gfx/android 里所有 "跳过了、失败了" 的分支都是**静默降级**,
// 不留痕迹就等于 (例如) 帧缓冲没绑上却只看到一块黑屏。
func Logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintln(os.Stderr, "gox: "+msg)
	cs := C.CString(msg)
	C.gox_log_write(cs)
	C.free(unsafe.Pointer(cs))
}

// SetJavaVM 保存 JVM 指针 (由 libgox 的 JNI_OnLoad 调用一次)。
func SetJavaVM(vm unsafe.Pointer) {
	mu.Lock()
	C.gox_set_jvm((*C.JavaVM)(vm))
	mu.Unlock()
}

// BindHost 绑定宿主回调对象 (Kotlin 的 GoxHost 实例)。必须在 Java 线程上调用
// (它的 GetEnv 依赖调用线程已附加到 JVM)。
func BindHost(host unsafe.Pointer) error {
	e, done, err := env()
	if err != nil {
		return err
	}
	defer done()

	obj := unsafe.Pointer(C.gox_new_global_ref(e, C.jobject(host)))
	if obj == nil {
		return fmt.Errorf("android: NewGlobalRef(GoxHost) 失败")
	}
	clazz := C.gox_object_class(e, C.jobject(obj))
	mid := C.gox_flush_mid(e, clazz)
	fin := C.gox_finished_mid(e, clazz)
	ime := C.gox_ime_mid(e, clazz)
	if C.gox_clear_exception(e) != 0 || mid == nil {
		return fmt.Errorf("android: GoxHost.flush([I)V 找不到 (Kotlin 侧签名必须逐字符对上)")
	}
	if fin == nil {
		C.gox_clear_exception(e)
	}
	if ime == nil {
		C.gox_clear_exception(e)
		Logf("GoxHost.imeShow(Z)V 找不到: 软键盘不可开关 (老宿主?)")
	}

	mu.Lock()
	hostObj, flushMID, finMID, imeMID = obj, mid, fin, ime
	mu.Unlock()
	return nil
}

// BindFrameBuffer 绑定 Java 侧的直接缓冲区并取到它的裸地址。
//
// **必须**是 allocateDirect 的缓冲区: 堆内数组的地址会随 GC 搬家, 往里写等于
// 把 JVM 写坏; GetDirectBufferAddress 对非直接缓冲区返回 NULL —— 这里据此拒绝。
func BindFrameBuffer(buf unsafe.Pointer, capBytes int) error {
	e, done, err := env()
	if err != nil {
		return err
	}
	defer done()

	if buf == nil {
		return fmt.Errorf("android: frameBuffer 不能为空")
	}
	ptr := C.gox_direct_buffer(e, C.jobject(buf))
	if ptr == nil {
		return fmt.Errorf("android: frameBuffer 必须是 allocateDirect 的 ByteBuffer")
	}
	obj := unsafe.Pointer(C.gox_new_global_ref(e, C.jobject(buf)))
	mu.Lock()
	// 换地址与新全局引用在**同一段临界区**里落地, 且 uploadFrame 是持同一把锁做
	// 整段拷贝的 —— 所以这里一定等"正往旧地址写的那一帧"写完才换。
	// 同时, 旧缓冲的最后一个 JNI 全局引用也在此刻被替换掉: 从这一刻起旧地址不再
	// 被任何一方碰, Java 侧可以放心让它被回收 (见 uploadFrame 的注释)。
	frameBuf, framePtr, frameCap = obj, ptr, capBytes
	mu.Unlock()
	return nil
}

// Uploader 返回实现 gfx/mobile.Uploader 的上屏钩子: 把 RGBA 写进 Java 的直接
// 缓冲区, 再调一次 GoxHost.flush(脏区), 让 Kotlin 把 bitmap 刷到屏幕。
//
// 这里不做"只拷脏区"的优化: 契约明确允许一次额外拷贝, 而按行拷贝整帧已经是
// 这段跨语言代码里最容易写错的部分 (行跨距、裁剪、负坐标)。要优化的前提是先
// 有真机上的帧耗时数据。
func Uploader() mobile.Uploader { return uploadFrame }

func uploadFrame(img *image.RGBA, rects []image.Rectangle) {
	if img == nil {
		return
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	need := w * h * 4

	// **整段拷贝都在锁里** —— BindFrameBuffer 换地址时拿的是同一把锁, 于是
	// "宿主把裸地址交给引擎"与"引擎还在往旧地址写"不可能重叠。少了这层, 旋转/
	// 分屏时旧缓冲区会在换绑那一刻失去最后一个 JNI 全局引用 (可被 JVM 释放),
	// 而渲染线程可能仍有一帧正往它的地址上写: 症状是**偶发**的 JVM 堆损坏
	// (最难查的一类崩溃), 而加固成本只是一次 mutex。
	//
	// 锁**不能跨到 flush**: flush 自己要拿 mu 读回调句柄, 而 Go 的 Mutex 不可重入。
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
		// 只出声一次: 每次上屏都打会把 logcat 淹掉。
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
	e, done, err := env()
	if err != nil {
		return
	}
	defer done()

	mu.Lock()
	obj, mid := hostObj, flushMID
	mu.Unlock()
	if obj == nil || mid == nil {
		return
	}

	var arr unsafe.Pointer
	if len(rects) > 0 {
		n := len(rects) * 4
		arr = unsafe.Pointer(C.gox_new_int_array(e, C.jsize(n)))
		if arr == nil {
			return
		}
		// 矩形用 image.Rectangle 的 Min/Max 语义: 输出 [x, y, w, h] 给 Java 的
		// invalidate。image.Rectangle 没有 X/Y/W/H 字段, 别照 GDK 的习惯写。
		vals := make([]int32, n)
		for i, r := range rects {
			vals[i*4+0] = int32(r.Min.X)
			vals[i*4+1] = int32(r.Min.Y)
			vals[i*4+2] = int32(r.Dx())
			vals[i*4+3] = int32(r.Dy())
		}
		// SetIntArrayRegion 一次拷进去: 逐元素设会让每次都在 JNI 边界往返一趟。
		C.gox_set_int_region(e, C.jintArray(arr), 0, C.jsize(n),
			(*C.jint)(unsafe.Pointer(&vals[0])))
	}
	C.gox_call_void(e, C.jobject(obj), mid, C.jobject(arr))
	if C.gox_clear_exception(e) != 0 && !warnedFlushErr {
		warnedFlushErr = true
		Logf("GoxHost.flush 抛了异常 (已清除, 上屏可能停在旧帧)")
	}
}

// CallIMEShow 通知宿主开/收软键盘 (焦点进/出编辑框时内核调)。
// 没绑到 imeShow 方法 (老宿主) 时静默跳过。
func CallIMEShow(on bool) {
	e, done, err := env()
	if err != nil {
		return
	}
	defer done()

	mu.Lock()
	obj, mid := hostObj, imeMID
	mu.Unlock()
	if obj == nil || mid == nil {
		return
	}
	// jboolean 是 uint8, cgo 不会把 Go bool 自动转过去, 手动归一。
	var o C.jboolean = 0
	if on {
		o = 1
	}
	C.gox_call_void_bool(e, C.jobject(obj), mid, o)
}

// NotifyFinished 通知宿主脚本已结束 (errMsg 为空表示正常结束)。
func NotifyFinished(code int, errMsg string) {
	e, done, err := env()
	if err != nil {
		return
	}
	defer done()

	mu.Lock()
	obj, mid := hostObj, finMID
	mu.Unlock()
	if obj == nil || mid == nil {
		return
	}
	var js C.jstring
	if errMsg != "" {
		cs := C.CString(errMsg)
		js = C.gox_new_utf(e, cs)
		C.free(unsafe.Pointer(cs))
	}
	C.gox_call_void_int_str(e, C.jobject(obj), mid, C.jint(code), js)
	C.gox_clear_exception(e)
}

// env 取当前线程的 JNIEnv, 第二个返回值是"用完要还"的收尾函数 (本次真的附加了
// 才会 detach)。**每次跨语言调用结束都必须调它** —— 漏了会一直持有线程附加
// 状态, 而线程结束时 JVM 会直接 abort 整个进程。
func env() (*C.JNIEnv, func(), error) {
	var e *C.JNIEnv
	switch C.gox_env(&e) {
	case 0:
		return e, func() {}, nil
	case 1:
		// 附加状态下的局部引用会在 detach 时统一释放, 这也是必须 detach 的原因之一。
		return e, func() { C.gox_detach_current() }, nil
	default:
		return nil, func() {}, fmt.Errorf("android: 当前线程无法附加到 JVM (JNI_OnLoad 还没跑?)")
	}
}
