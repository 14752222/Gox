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
#include <jni.h>
#include <stdlib.h>
#include <string.h>

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
// 方法名与签名写死在 C 里, 免得每次绑定都 C.CString 一遍再想不起来 free。
static jmethodID gox_flush_mid(JNIEnv *env, jobject c) {
	return (*env)->GetMethodID(env, c, "flush", "([I)V");
}
static jmethodID gox_finished_mid(JNIEnv *env, jobject c) {
	return (*env)->GetMethodID(env, c, "finished", "(ILjava/lang/String;)V");
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
	// hostObj / flushMID / finMID 是宿主回调 GoxHost 的全局引用与方法 id
	hostObj  unsafe.Pointer
	flushMID C.jmethodID
	finMID   C.jmethodID
	// frameBuf 是 Java 侧 allocateDirect 的那块内存 (全局引用, 只为持有它),
	// framePtr 是它的裸地址 —— 引擎直接往里写。
	frameBuf unsafe.Pointer
	framePtr unsafe.Pointer
	frameCap int

	warnedNoBuffer bool
	warnedFlushErr bool
)

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
	if C.gox_clear_exception(e) != 0 || mid == nil {
		return fmt.Errorf("android: GoxHost.flush([I)V 找不到 (Kotlin 侧签名必须逐字符对上)")
	}
	if fin == nil {
		C.gox_clear_exception(e)
	}

	mu.Lock()
	hostObj, flushMID, finMID = obj, mid, fin
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
	mu.Lock()
	ptr, capacity := framePtr, frameCap
	mu.Unlock()

	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if ptr == nil || capacity < w*h*4 {
		// 只出声一次: 每次上屏都打会把 logcat 淹掉。
		if !warnedNoBuffer {
			warnedNoBuffer = true
			fmt.Fprintf(os.Stderr,
				"android: 帧缓冲不可用 (cap=%d need=%d), 上屏被跳过; 检查 nativeInit 是否已调用\n",
				capacity, w*h*4)
		}
		return
	}
	// 按行拷: img.Stride 是分配对齐后的行宽, 可能大于 w*4, 整体 memcpy 会把
	// 行尾填充算进画面 (表现为图像斜切)。
	row := w * 4
	for y := 0; y < h; y++ {
		dst := unsafe.Pointer(uintptr(ptr) + uintptr(y*row))
		src := unsafe.Pointer(&img.Pix[y*img.Stride])
		C.gox_copy_pixels(dst, src, C.int(row))
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
		fmt.Fprintln(os.Stderr, "android: GoxHost.flush 抛了异常 (已清除, 上屏可能停在旧帧)")
	}
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
