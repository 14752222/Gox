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
//
// ── NativeHost 通道 (gx/native.go 的宿主契约, Swift 实现) ───────────────────
//
// 内核在 GUI 线程 (本文件的脚本线程) 调 host.Call(method, args); 本文件把它
// 翻译成一次对 Swift 注册的 C 函数指针的调用。协议 (两端字符串逐字一致, 改动要
// 三处同步: 本文件 / Gox-Bridging-Header.h / NativeHost.swift):
//
//	注册:   gox_set_native_host(capCtx, capFn, callCtx, callFn) — Swift 在
//	        gox_init 之后调用一次。两个函数指针都是"无捕获 @convention(c) +
//	        ctx 还原 self" (与 flush/finished 回调同一模式)。
//	        typedef const char *(*gox_cap_fn)(void *ctx);
//	            返回逗号分隔的方法名/能力名 ("device.info,…,insets,battery"),
//	            strdup 分配, Go 侧负责 free。
//	        typedef const char *(*gox_call_fn)(void *ctx, const char *method,
//	                                           const char *argsJson);
//	            args 是内核参数对象 (含 __id) 的 JSON。返回值三选一:
//	              "__pending__"                      → NativePending
//	              {"__errCode":"…","__errMsg":"…"}   → NativeFailure(8 码之一)
//	              其它 (结果 JSON, 任意形状)          → NativeResult
//	            strdup 分配, Go 侧负责 free。同步方法 (device.info /
//	            permission.get …) 当场返回 JSON; 要等系统 UI 的返回
//	            "__pending__", 稍后经 gox_resolve_native 回填。
//	Resolve: gox_resolve_native(id, resultJson, errCode, errMsg) — id 照抄
//	         args 里的 __id; errCode 非空即失败。任意线程可调 (Go 侧 gfx.Post)。
//	上报:   gox_report_battery/network/location (JSON)、gox_report_app_state、
//	         gox_report_memory_warning、gox_report_permission —— 任意线程可调。
//
// JSON ↔ object.Value 的转换与 gfx/android/libgox 各有一份 (文件边界: 桥接
// 改动只允许落在这两个 libgox 包里, 不新增共享包)。两边协议逐字一致。
package main

/*
#include <stdlib.h>
#include <string.h>

// ── NativeHost 通道的 C 助手 ──
typedef const char *(*gox_cap_fn)(void *ctx);
typedef const char *(*gox_call_fn)(void *ctx, const char *method, const char *argsJson);

static const char *gox_h_call_cap(void *ctx, void *fn) {
	return ((gox_cap_fn)fn)(ctx);
}
static const char *gox_h_call_native(void *ctx, void *fn, const char *m, const char *a) {
	return ((gox_call_fn)fn)(ctx, m, a);
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
	"unsafe"

	"github.com/14752222/Gox/gfx"
	gfxios "github.com/14752222/Gox/gfx/ios"
	"github.com/14752222/Gox/gfx/mobile"
	"github.com/14752222/Gox/object"
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

// gox_init 初始化会话: 绑定宿主回调与帧缓冲, 创建表面并注册为默认窗口后端。
// 必须在 gox_run_script 之前调用; 返回 0 成功, 非 0 失败 (msg 打到 stderr)。
//
//export gox_init
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

// gox_bind_frame_buffer 尺寸变化时重绑宿主缓冲 (Swift 重新 malloc 后调)。
//
//export gox_bind_frame_buffer
func gox_bind_frame_buffer(w, h int32, buf unsafe.Pointer) int32 {
	if err := gfxios.BindFrameBuffer(buf, int(w)*int(h)*4); err != nil {
		gfxios.Logf("ios rebind 失败: %v", err)
		return 1
	}
	return 0
}

// gox_run_script 跑一段脚本 (立即返回, 真正执行在 Go 自己的线程上)。
// 结束 (含异常) 后经宿主的 finished 回调报告。
//
//export gox_run_script
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

// gox_tick 由宿主每帧调用一次 (CADisplayLink), 叫醒事件泵。
//
//export gox_tick
func gox_tick() {
	if s := currentSurface(); s != nil {
		s.Tick()
	}
}

// gox_touch 触摸事件: action 见文件头常量, 坐标是设备像素客户区坐标。
//
//export gox_touch
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

// gox_resize 尺寸/密度变化 (旋转、分屏)。尺寸真变了才投 EventResize。
//
//export gox_resize
func gox_resize(w, h int32, density float32) {
	if s := currentSurface(); s != nil {
		s.Resize(int(w), int(h), float64(density))
	}
}

// gox_set_insets 宿主上报安全区 (设备像素): 状态栏/刘海/圆角/Home 指示条。
// 内部经 gfx.Post 投回 GUI 线程再报给内核 (gx/viewport 的订阅回调只准在
// GUI 线程跑), 所以本函数在任意线程调用都安全。
//
//export gox_set_insets
func gox_set_insets(top, right, bottom, left int32) {
	gfxios.SetInsets(int(top), int(right), int(bottom), int(left))
}

// gox_bind_ime 绑定软键盘开关回调: 焦点进/出 input/textarea 时内核经此开/收
// 软键盘 (on 非 0 = 弹出)。回调发生在 Go 的 GUI 线程, UIKit 调用必须由 Swift
// dispatch 到主队列。在 gox_init 之后、gox_run_script 之前调用。
//
//export gox_bind_ime
func gox_bind_ime(ctx unsafe.Pointer, fn unsafe.Pointer) {
	gfxios.SetIMEHost(ctx, fn)
	s := currentSurface()
	if s != nil {
		s.SetIMEHost(func(on bool) { gfxios.CallIME(on) })
	}
}

// gox_ime_commit 输入法提交一批文本 (软键盘完成一次输入, 可能是整词)。
// 整批插入语义在内核 gfx/ime.go; 本函数只投事件, 任意线程可调。
//
//export gox_ime_commit
func gox_ime_commit(text *C.char) {
	s := currentSurface()
	if s == nil {
		return
	}
	s.IMECommit(C.GoString(text))
}

// gox_key 软键盘的"非文本"按键: 退格 (Backspace) 等。down 非 0 = 按下。
//
//export gox_key
func gox_key(key *C.char, down int32) {
	s := currentSurface()
	if s == nil {
		return
	}
	kind := gfx.EventKeyUp
	if down != 0 {
		kind = gfx.EventKeyDown
	}
	s.Post(gfx.Event{Kind: kind, Key: C.GoString(key)})
}

// gox_destroy 结束会话: 关闭表面 (唤醒睡在 WaitEvents 里的泵 → 脚本线程收尾)。
//
//export gox_destroy
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

// ===== NativeHost 通道: Go ↔ Swift 的宿主契约翻译 (协议见文件头) =====

var (
	nhMu      sync.Mutex
	nhCapCtx  unsafe.Pointer
	nhCapFn   unsafe.Pointer
	nhCallCtx unsafe.Pointer
	nhCallFn  unsafe.Pointer
)

// gox_set_native_host 注册 Swift 侧的宿主回调 (gox_init 之后调一次)。注册成功
// 即向内核声明全部能力 —— Swift 的 capabilities 回调在本调用内被同步问一遍
// (发生在 Swift 主线程, 调回 Swift 无线程问题)。
//
//export gox_set_native_host
func gox_set_native_host(capCtx, capFn, callCtx, callFn unsafe.Pointer) int32 {
	if capFn == nil || callFn == nil {
		gfxios.Logf("gox_set_native_host: capFn/callFn 不能为空, NativeHost 未注册")
		return 1
	}
	nhMu.Lock()
	nhCapCtx, nhCapFn = capCtx, capFn
	nhCallCtx, nhCallFn = callCtx, callFn
	nhMu.Unlock()
	gfx.SetNativeHost(&cfnNativeHost{})
	return 0
}

// cfnNativeHost 是 gfx.NativeHost 的 iOS 实现: 把内核的 Call 翻译成一次对
// Swift C 函数指针的调用。
type cfnNativeHost struct{}

// Capabilities 报告 Swift 宿主声明的方法名/能力名 (逗号分隔字符串 → 切片)。
// Swift 返回的字符串是 strdup 分配的, 这里负责 free。
func (h *cfnNativeHost) Capabilities() []string {
	out := []string{}
	nhMu.Lock()
	ctx, fn := nhCapCtx, nhCapFn
	nhMu.Unlock()
	if fn == nil {
		return out
	}
	cs := C.gox_h_call_cap(ctx, fn)
	if cs == nil {
		return out
	}
	s := C.GoString(cs)
	C.free(unsafe.Pointer(cs))
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pendingMarker 是 Swift 侧"稍后回填"的哨兵 (协议见文件头)。
const pendingMarker = "__pending__"

// Call 执行一个原生方法: 参数序列化成 JSON 传给 Swift, 回包按协议三态解析。
//
// 同步路径 (device.info / permission.get …) 会阻塞脚本线程直到 Swift 返回 ——
// 内核的 deviceHostOverlay / jsGetBrightness / jsOpenSystemSettings /
// refreshPermissionsFromHost 只认同步返回值 (gfx/native_device.go:212 等)。
// Swift 侧的同步实现凡是碰 UIKit 的 (亮度/设置页) 都用 DispatchQueue.main.sync
// 完成 —— 主线程从不同步等待 Go 线程 (gox_tick 只叫醒泵就返回), 不会死锁。
func (h *cfnNativeHost) Call(method string, args object.Value) gfx.NativeCallResult {
	nhMu.Lock()
	ctx, fn := nhCallCtx, nhCallFn
	nhMu.Unlock()
	if fn == nil {
		return gfx.NativeFailure(gfx.ErrUnsupported,
			"iOS 宿主未注册 nativeCall, 方法 %s 不可用", method)
	}

	cm := C.CString(method)
	defer C.free(unsafe.Pointer(cm))
	aj := C.CString(objToJSON(args))
	defer C.free(unsafe.Pointer(aj))

	res := C.gox_h_call_native(ctx, fn, cm, aj)
	if res == nil {
		return gfx.NativeFailure(gfx.ErrPlatform, "goxCall(%s) 返回空", method)
	}
	s := C.GoString(res)
	C.free(unsafe.Pointer(res))
	return parseHostReply(s)
}

// parseHostReply 按 Swift 侧回包协议三态解析 (文件头: pending / 错误信封 / 结果)。
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

// ── Swift → Go 的回填与上报导出 ──
//
// 都从 Swift 的任意线程调用; 内核要求 ResolveNative / Report* 在 GUI 线程执行
// (会同步跑脚本回调), 所以一律 gfx.Post 投回 —— 与 gox_set_insets 同一条纪律。

// gox_resolve_native Swift 交付一次异步调用的结果 (id 照抄 args 里的 __id;
// errCode 非空即失败)。任意线程可调。
//
//export gox_resolve_native
func gox_resolve_native(id, result, errCode, errMsg *C.char) {
	idStr := cStr(id)
	if idStr == "" {
		return
	}
	resStr := cStr(result)
	codeStr := cStr(errCode)
	msgStr := cStr(errMsg)
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

// gox_report_battery 宿主上报电池状态。JSON 字段: supported/level/charging/
// chargingType/temperature/lowPowerMode (与 gfx.BatteryState 对齐)。
//
//export gox_report_battery
func gox_report_battery(data *C.char) {
	o := cJSONObj(data)
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

// gox_report_network 宿主上报网络状态。JSON 字段: connected/type/metered/ssid/
// strength/carrier/generation (与 gfx.NetworkState 对齐)。
//
//export gox_report_network
func gox_report_network(data *C.char) {
	o := cJSONObj(data)
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

// gox_report_location 宿主上报一个定位结果 (流式 watch 与一次定位共用)。
//
//export gox_report_location
func gox_report_location(data *C.char) {
	o := cJSONObj(data)
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

// gox_report_app_state 宿主上报应用生命周期 ("active"|"background"|"inactive")。
//
//export gox_report_app_state
func gox_report_app_state(state *C.char) {
	s := cStr(state)
	gfx.Post(func() { gfx.ReportAppState(s) })
}

// gox_report_memory_warning 宿主上报内存警告。
//
//export gox_report_memory_warning
func gox_report_memory_warning() {
	gfx.Post(gfx.ReportMemoryWarning)
}

// gox_report_permission 宿主上报一个权限状态 (启动时批量报 / 从设置页回来报)。
//
//export gox_report_permission
func gox_report_permission(kind, state *C.char) {
	k, st := cStr(kind), cStr(state)
	gfx.Post(func() { gfx.ReportPermission(k, st) })
}

// ── JSON ↔ object.Value (args 下行 / 结果回填上行共用的窄转换) ──
// 与 gfx/android/libgox 的同名助手协议一致 (两份拷贝的原因见文件头)。

// cStr 把 C 字符串转 Go 字符串 (nil → 空串)。
func cStr(s *C.char) string {
	if s == nil {
		return ""
	}
	return C.GoString(s)
}

// cJSONObj 把导出函数收到的 C 字符串解析成 *object.Object (失败返回 nil)。
func cJSONObj(s *C.char) *object.Object {
	v, err := jsonToObj(cStr(s))
	if err != nil {
		return nil
	}
	o, _ := v.(*object.Object)
	return o
}

// objToJSON 把内核给的参数对象序列化成 JSON 文本 (给 Swift JSONSerialization
// 解析)。函数/闭包等不可序列化的值跳过或写成 null。
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
