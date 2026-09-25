//go:build ios

// Package ios 是 Gox 在 iOS 上的 **宿主通道层**: 只负责"怎么把像素交给 Swift"
// 与"怎么回调宿主", 不含任何交互逻辑 (那在 gfx/mobile, 可在开发机单测)。
//
// 与 gfx/android 的对照 (分工完全同构, 只是传输层不同):
//
//	gfx/mobile          纯 Go: 事件队列 / 触摸映射 / 事件泵唤醒 / density 上报  ← 可单测
//	gfx/android         JNI 通道 (JVM 附加 / direct ByteBuffer / flush 回调)
//	gfx/ios             本包: C 函数指针通道 (宿主缓冲写入 / flush / finished 回调)
//	gfx/ios/libgox      package main: //export 出来的 C 入口 + 装配 (Surface + VM)
//
// 为什么不用 JNI 那套反射查找: iOS 侧宿主是 Swift, 与 Go 同链一个二进制
// (c-archive), 没有"运行时按名字找方法"的需求 —— 直接让宿主把 C 函数指针 +
// 上下文指针传进来, 类型在编译期对上, 少一整层签名匹配的坑。
//
// ── Swift 侧需要提供的契约 (改动要三处同步: 本文件 / libgox / GoxRuntime.swift) ──
//
//	typedef void (*gox_flush_fn)(void *ctx, const int32_t *rects, int32_t n);
//	    把屏幕刷新到 UI: rects 为 NULL/n=0 表示整帧, 否则是 [x,y,w,h, ...] 像素矩形。
//	    **调用发生在 Go 的渲染线程**, Swift 侧必须自己 dispatch 到主队列。
//	typedef void (*gox_finished_fn)(void *ctx, int32_t code, const char *msg);
//	    脚本跑完 (含异常) 时调, Swift 据此弹错误提示。
//
// 帧缓冲: Swift 用 malloc 分配 w*h*4 字节, 把地址交给 gox_init —— 渲染时 Go 直接
// 把 RGBA 写进这块内存 (零拷贝), 然后调 flush; Swift 侧从缓冲拷出 CGImage 上屏
// ("一次额外拷贝", 与 Android 壳同一取舍)。
//
// 线程模型 (与内核纪律一致): gox_run_script 起一条 Go 线程跑脚本与事件泵并
// LockOSThread; Swift 的 tick/触摸回调在**别的**线程上只往队列里塞事件 ——
// 与 win32 的 WndProc 同一约束, 绝不跨线程执行 JS。
package ios

/*
#include <stdint.h>
#include <string.h>
#include <stdlib.h>

typedef void (*gox_flush_fn)(void *ctx, const int32_t *rects, int32_t n);
typedef void (*gox_finished_fn)(void *ctx, int32_t code, const char *msg);
typedef void (*gox_ime_fn)(void *ctx, int32_t on);

static void gox_call_flush(void *ctx, void *fn, const int32_t *rects, int32_t n) {
	((gox_flush_fn)fn)(ctx, rects, n);
}

static void gox_call_finished(void *ctx, void *fn, int32_t code, const char *msg) {
	((gox_finished_fn)fn)(ctx, code, msg);
}

static void gox_call_ime(void *ctx, void *fn, int32_t on) {
	((gox_ime_fn)fn)(ctx, on);
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

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/gfx/mobile"
)

var (
	mu sync.Mutex
	// flushCtx / flushFn 是宿主回调的上下文与函数指针 (Swift 传入, 终身有效)
	flushCtx unsafe.Pointer
	flushFn  unsafe.Pointer
	finCtx   unsafe.Pointer
	finFn    unsafe.Pointer
	// framePtr/frameCap 是 Swift malloc 的帧缓冲 (归宿主所有, 引擎只写)
	framePtr unsafe.Pointer
	frameCap int
	// imeCtx/imeFn 是软键盘开关回调 (SetIMEHost 绑定)
	imeCtx unsafe.Pointer
	imeFn  unsafe.Pointer

	warnedNoBuffer bool
	warnedFlushErr bool
)

// Logf 写一条日志到 stderr。iOS 上从 Xcode 启动 / `simctl launch --console`
// 时 stderr 直接进控制台; 真机独立启动时看不到 —— v1 接受 (需要 os_log 再接)。
func Logf(format string, args ...any) {
	fmt.Fprintln(os.Stderr, "gox: "+fmt.Sprintf(format, args...))
}

// Init 绑定宿主回调与帧缓冲。必须在任何渲染发生前调用 (Swift 的 viewDidLoad)。
// buf 是宿主 malloc 的 w*h*4 字节缓冲; flush/finished 为 nil 时对应回调静默跳过。
func Init(w, h int, buf unsafe.Pointer, fCtx, fFn, doneCtx, doneFn unsafe.Pointer) error {
	if buf == nil {
		return fmt.Errorf("ios: 帧缓冲不能为空")
	}
	mu.Lock()
	defer mu.Unlock()
	flushCtx, flushFn = fCtx, fFn
	finCtx, finFn = doneCtx, doneFn
	framePtr, frameCap = buf, w*h*4
	return nil
}

// SetInsets 宿主上报安全区 (设备像素): 状态栏 / 刘海 / 圆角 / Home 指示条
// 占掉的边缘。脚本侧经 useInsets() / safeAreaStyle() 响应式读 (gx/viewport)。
//
// 必须经 gfx.Post 投回 GUI 线程: ReportViewport 会**同步**跑脚本侧订阅回调
// —— 事件纪律: 平台回调只投递任务, 绝不跨线程执行 JS (与触摸/tick 回调
// 只塞事件是同一条纪律)。win 传 nil 走全局缺省键, 移动端单窗口够用。
func SetInsets(top, right, bottom, left int) {
	gfx.Post(func() {
		gfx.ReportViewport(nil, gfx.Viewport{Insets: gfx.Insets{
			Top: top, Right: right, Bottom: bottom, Left: left,
		}})
	})
}

// BindFrameBuffer 尺寸变化时重绑宿主缓冲 (Swift 重新 malloc 后调)。
// 与 uploadFrame 持同一把锁: 换地址一定等"正往旧地址写的那一帧"写完。
func BindFrameBuffer(buf unsafe.Pointer, capBytes int) error {
	if buf == nil {
		return fmt.Errorf("ios: 帧缓冲不能为空")
	}
	mu.Lock()
	framePtr, frameCap = buf, capBytes
	mu.Unlock()
	return nil
}

// Uploader 返回实现 gfx/mobile.Uploader 的上屏钩子: 把 RGBA 写进宿主缓冲,
// 再调一次宿主 flush(脏区), 让 Swift 把缓冲刷到屏幕。
//
// 这里不做"只拷脏区"的优化 (理由与 gfx/android 相同): 按行拷贝整帧已经是
// 这段跨语言代码里最容易写错的部分, 优化等真机帧耗时数据说话。
func Uploader() mobile.Uploader { return uploadFrame }

func uploadFrame(img *image.RGBA, rects []image.Rectangle) {
	if img == nil {
		return
	}
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	need := w * h * 4

	// 整段拷贝都在锁里 —— BindFrameBuffer 换地址拿同一把锁, "换绑"与
	// "写旧地址"不可能重叠 (详见 gfx/android 的同段注释)。
	mu.Lock()
	ptr, capacity := framePtr, frameCap
	ok := ptr != nil && capacity >= need
	if ok {
		// 按行拷: img.Stride 是分配对齐后的行宽, 可能大于 w*4, 整体 memcpy
		// 会把行尾填充算进画面 (表现为图像斜切)。
		row := w * 4
		for y := 0; y < h; y++ {
			dst := unsafe.Pointer(uintptr(ptr) + uintptr(y*row))
			src := unsafe.Pointer(&img.Pix[y*img.Stride])
			C.gox_copy_pixels(dst, src, C.int(row))
		}
	}
	fCtx, fFn := flushCtx, flushFn
	mu.Unlock()

	if !ok {
		// 只出声一次: 每次上屏都打会把控制台淹掉。
		if !warnedNoBuffer {
			warnedNoBuffer = true
			Logf("帧缓冲不可用 (cap=%d need=%d), 上屏被跳过; 检查 gox_init 是否已调用", capacity, need)
		}
		return
	}
	flush(fCtx, fFn, rects)
}

// flush 通知宿主上屏。没有宿主回调时静默跳过 (init 与 start 之间会这样)。
func flush(ctx, fn unsafe.Pointer, rects []image.Rectangle) {
	if fn == nil {
		return
	}
	var arr *C.int32_t
	n := C.int32_t(0)
	if len(rects) > 0 {
		vals := make([]int32, len(rects)*4)
		for i, r := range rects {
			// 矩形用 image.Rectangle 的 Min/Max 语义: 输出 [x, y, w, h] 给
			// Swift 的 invalidate。image.Rectangle 没有 X/Y/W/H 字段。
			vals[i*4+0] = int32(r.Min.X)
			vals[i*4+1] = int32(r.Min.Y)
			vals[i*4+2] = int32(r.Dx())
			vals[i*4+3] = int32(r.Dy())
		}
		arr = (*C.int32_t)(unsafe.Pointer(&vals[0]))
		n = C.int32_t(len(vals))
	}
	C.gox_call_flush(ctx, fn, arr, n)
}

// SetIMEHost 绑定软键盘开关回调 (Swift 在 gox_init 后、gox_run_script 前调;
// fn 为 nil 时不开关软键盘)。回调发生在内核 GUI 线程 —— UIKit 操作必须由
// Swift 侧 dispatch 到主队列。
func SetIMEHost(ctx, fn unsafe.Pointer) {
	mu.Lock()
	imeCtx, imeFn = ctx, fn
	mu.Unlock()
}

// CallIME 通知宿主开关软键盘 (on=false 收起)。没有绑定回调时静默跳过。
func CallIME(on bool) {
	mu.Lock()
	ctx, fn := imeCtx, imeFn
	mu.Unlock()
	if fn == nil {
		return
	}
	o := C.int32_t(0)
	if on {
		o = 1
	}
	C.gox_call_ime(ctx, fn, o)
}

// NotifyFinished 通知宿主脚本已结束 (errMsg 为空表示正常结束)。
func NotifyFinished(code int, errMsg string) {
	mu.Lock()
	ctx, fn := finCtx, finFn
	mu.Unlock()
	if fn == nil {
		return
	}
	var cs *C.char
	if errMsg != "" {
		cs = (*C.char)(unsafe.Pointer(&[]byte(errMsg + "\x00")[0]))
	}
	C.gox_call_finished(ctx, fn, C.int32_t(code), cs)
}

// Reset 清理会话状态 (宿主销毁视图后重入用)。Surface 的关闭由 libgox 负责。
func Reset() {
	mu.Lock()
	framePtr, frameCap = nil, 0
	flushCtx, flushFn = nil, nil
	finCtx, finFn = nil, nil
	mu.Unlock()
}
