//go:build harmony

// Command libgox 是 HarmonyOS 上的 Go 侧入口: 编成 libgox.so
// (buildmode=c-shared), 由 ArkTS 通过 **NAPI** 调用, 内核 (lexer→vm→stdlib→gfx)
// 与桌面版完全同一份。
//
// 为什么会有一个 main 包: `-buildmode=c-shared` 只接受 main 包, 且 //export 出来
// 的符号必须从这个包产生 (非 main 包里 //export 的可用性依赖工具链细节, 不赌)。
// 这个 main 是占位的, 宿主**不**调用它。
//
// ── 构建 ─────────────────────────────────────────────────────────────────
//
// 鸿蒙没有 `GOOS=openharmony` (官方 Go 发行版不含该 target, 见
// scripts/build-harmony.sh 文件头)。本模块用 linux target + OHOS clang/sysroot
// 交叉编译:
//
//	GOOS=linux GOARCH=arm64 CGO_ENABLED=1 CC="<OHOS SDK>/…/cc-ohos-arm64.cmd" \
//	  go build -buildmode=c-shared -o libgox.so ./gfx/harmony/libgox
//
// 或者直接跑 `bash scripts/build-harmony.sh`。
//
// ── 与 Android 装配层的结构性差异 ─────────────────────────────────────────
//
//  1. **入口不是 `JNI_OnLoad` 而是 NAPI 模块注册**。ArkTS 侧
//     `import gox from 'libgox.so'` 时, 运行时会调用我们注册的 `napi_module`。
//     本文件的 `GoxModuleRegister` 就是那个 register 函数 —— ArkTS 侧拿到的
//     对象上的每个方法都是 `//export` 出来的 Go 函数包的一层。
//  2. **没有"启动即注册"的时机**, 与 Android 同理: 后端要靠宿主给的表面才能
//     成立, 所以 `gfx/backend/backend_harmony.go` 故意是空的, 注册发生在
//     `NativeInit` 里。
//  3. **上屏走 ArrayBuffer + PixelMap, 不用 XComponent 的 native window**。
//     ArkTS 侧 `new ArrayBuffer(w*h*4)` 交给 Go 直写 (零拷贝), Go 写完调
//     `host.flush()`; 宿主把这块 buffer 包成 PixelMap 画进 `<Canvas>` 即可。
//     这条路径与 Android 的 direct ByteBuffer + Bitmap 是同构的, 好处是**两端
//     能共用一份像素校验工具**。逐帧直接往 native window 写是后续优化, v1 不做
//     —— 存一个不消费的 `OHNativeWindow*` 只会变成悬垂指针隐患。
//
// ── NativeHost 通道 (gx/native.go 的宿主契约, ArkTS 实现) ───────────────────
//
// 协议与 Android 逐字一致 (两端字符串必须完全对上, 改动要三处同步: 本文件 /
// app/harmony/native/GoxNativeHost.ets / app/NATIVE-HOST.md):
//
//	Capabilities: ArkTS `nativeCapabilities(): string` → 逗号分隔的方法名/能力名
//	Call:         ArkTS `nativeCall(method: string, argsJson: string): string`,
//	              args 是内核 nativeArgsWithID 给的参数对象 (含 __id) 的 JSON。
//	              返回值三选一:
//	                "__pending__"                        → NativePending
//	                {"__errCode":"…","__errMsg":"…"}     → NativeFailure(8 码之一)
//	                其它 (结果 JSON, 任意形状)            → NativeResult
//	Resolve:      ArkTS 侧 `nativeResolveNative(id, resultJson, errCode, errMsg)`
//	              —— 与 Android 的命名保持一致, 免得两端文档对不上。
//	上报通道:     nativeReportBattery/Network/Location(JSON) / nativeReportAppState
//	              / nativeReportMemoryWarning / nativeReportPermission
//	              / nativeReportBackPress(): boolean (同步等脚本答复)
//	              / **nativeSetDisplayFold(JSON)** ← 折叠屏上报 (HF2)
package main

/*
// Declarations only -- every definition lives in bridge.c (same directory).
//
// Why: this file contains //export directives (GoxModuleRegister / GoxDispatch).
// When //export is present, cgo copies the preamble into TWO generated C files,
// so any definition here becomes a duplicate symbol at link time:
//     ld.lld: error: duplicate symbol: gox_trampoline
//
// How cgo treats this block (worth knowing, it explains most surprises):
//   * cgo STRIPS the block-comment markers and pastes everything between them
//     straight into the generated C file as real C code.  That is exactly why
//     the #include lines below work at all.
//   * Consequence 1: do NOT nest another block comment in here.  Plain C prose
//     must use // line comments (as this text does).
//   * Consequence 2: if the block terminator appears anywhere in this text, the
//     block ends there; the #include lines then become ordinary Go text and the
//     symptom is "C source files not allowed when not using cgo or SWIG" --
//     which reads like "cgo is disabled" but is really a broken preamble.
//   * Consequence 3: this preamble must stay PURE ASCII.  clang compiles it
//     directly and rejects CJK punctuation with "unexpected character <U+2014>".
//     A plain .c file has no such limit, so all Chinese prose lives in bridge.c.
//   * Consequence 4 (same as 2, but it bit twice): never spell out the block
//     terminator in a comment here, not even to warn about it.
#include <node_api.h>
#include <stdlib.h>
#include <string.h>

napi_value gox_trampoline(napi_env env, napi_callback_info info);
char *gox_str(napi_env env, napi_value v, size_t *out_len);
double gox_num(napi_env env, napi_value v, double def, bool *ok);
bool gox_bool(napi_env env, napi_value v, bool def, bool *ok);
napi_value gox_ret_string(napi_env env, const char *s);
napi_value gox_ret_int(napi_env env, int32_t v);
napi_value gox_ret_bool(napi_env env, bool v);
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
	gfxharmony "github.com/14752222/Gox/gfx/harmony"
	"github.com/14752222/Gox/gfx/mobile"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ArkUI 触摸事件的动作值。**故意与 Android 的 MotionEvent 取同一组数字**
// (DOWN=0/UP=1/MOVE=2/CANCEL=3): 两端常量一致时, 移植/对照代码不会因为"数字
// 看起来对但含义不同"而错得很隐蔽。ArkTS 侧 onTouch 的 TouchType 是枚举, 由
// 壳工程做一次映射。
const (
	actDown   = 0
	actUp     = 1
	actMove   = 2
	actCancel = 3
)

var (
	mu      sync.Mutex
	surface *mobile.Surface
	running bool // 脚本是否已在跑 (重复 start 直接忽略)
)

// main 是占位: c-shared 模式下由 ArkTS 通过下面导出的函数驱动。**不要**在这里
// 写 select{} —— 全阻塞会让 Go 运行时的死锁检测把宿主进程带走。
func main() {}

// ── NAPI 模块注册 ─────────────────────────────────────────────────────────

// goxMethods 是导出给 ArkTS 的方法表。**顺序必须与 C 侧 `enum { GOX_M_* }`
// 逐位一致** —— 两端靠序号对话, 中间插一行就会整体错位 (症状是"调 tick 却跑了
// resize"这类极难归因的错)。
//
// 与 Android 的差异: Android 靠 JNI 符号名 (Java_com_gox_GoxRuntime_nativeXxx)
// 由 JVM 自动绑定, 改了名字是"找不到方法"; 鸿蒙这边靠这张表 + 序号, 改了名字
// ArkTS 侧拿到的是 undefined 函数。两种失败都要靠契约测试兜住 —— 见
// gfx/mobile/fold_contract_test.go 里对两端名字的断言。
var goxMethods = []string{
	"init",                // 0  GOX_M_INIT
	"bindFrameBuffer",     // 1
	"runScript",           // 2
	"tick",                // 3
	"touch",               // 4
	"key",                 // 5
	"resize",              // 6
	"setInsets",           // 7
	"imeCommit",           // 8
	"destroy",             // 9
	"resolveNative",       // 10
	"reportBattery",       // 11
	"reportNetwork",       // 12
	"reportLocation",      // 13
	"reportAppState",      // 14
	"reportMemoryWarning", // 15
	"reportPermission",    // 16
	"reportBackPress",     // 17
	"setDisplayFold",      // 18
}

//export GoxModuleRegister
func GoxModuleRegister(env unsafe.Pointer, exports unsafe.Pointer) unsafe.Pointer {
	e := C.napi_env(env)
	gfxharmony.SetEnv(env)
	v := C.napi_value(exports)
	for i, name := range goxMethods {
		installExport(e, v, name, i)
	}
	// 一行启动日志: 真机上"库到底有没有被 import 到"全看它
	// (`hdc shell hilog -T Gox`)。
	gfxharmony.Logf("libgox 模块已注册 (%d 个导出)", len(goxMethods))
	return exports
}

// installExport 把一个方法挂到 exports 对象上。
//
// 用 napi_create_function 的 `data` 参数携带方法序号 —— 这才是"哪个方法"的
// 唯一来源。**不要**试图从 napi_get_cb_info 反查函数对象名: 它不回传函数本身,
// 那条路走不通 (2026-10-01 实测)。
func installExport(env C.napi_env, exports C.napi_value, name string, idx int) {
	cn := C.CString(name)
	defer C.free(unsafe.Pointer(cn))
	var fn C.napi_value
	st := C.napi_create_function(env, cn, C.NAPI_AUTO_LENGTH, C.napi_callback(C.gox_trampoline),
		unsafe.Pointer(uintptr(idx)), &fn)
	if C.int(st) != 0 {
		gfxharmony.Logf("注册导出 %s 失败 (status=%d)", name, int(st))
		return
	}
	C.napi_set_named_property(env, exports, cn, fn)
}

// GoxDispatch 是 C 侧 trampoline 的落地: 按 method 序号分发。
//
// 参数全部是 cgo 认得的扁平形态 (void* / void** / int), 见 C 侧注释里为什么
// 不能直接导出 NAPI 的原生签名。
//
//export GoxDispatch
func GoxDispatch(env unsafe.Pointer, method C.int, argv **C.napi_value, argc C.int) unsafe.Pointer {
	e := C.napi_env(env)

	// args 把 argv 摊成 Go 切片; 越界访问返回零值 (nil)。
	// argc 由 C 侧保证 <= GOX_MAX_ARGS / sizeof 指针, 但这里再夹一道。
	n := int(argc)
	if n < 0 {
		n = 0
	}
	if n > 8 { // 与 C 侧 GOX_MAX_ARGS 对齐
		n = 8
	}
	args := make([]C.napi_value, n)
	if argv != nil {
		// unsafe.Slice(ptr *T, n) 返回 []T, 所以这里要传 *argv (即 *C.napi_value)
		// 而不是 **C.napi_value —— 传后者会得到 []*C.napi_value, 与 args 元素类型
		// 不同, 报 "invalid copy: ... have different element types"。
		raw := unsafe.Slice(*argv, n)
		copy(args, raw)
	}
	arg := func(i int) C.napi_value {
		if i < 0 || i >= len(args) {
			return nil
		}
		return args[i]
	}

	switch int(method) {
	case mInit:
		return unsafe.Pointer(dispatchInit(e, arg))
	case mBindFrameBuffer:
		return unsafe.Pointer(dispatchBindFrameBuffer(e, arg))
	case mRunScript:
		return unsafe.Pointer(dispatchRunScript(e, arg))
	case mTick:
		if s := currentSurface(); s != nil {
			s.Tick()
		}
		return unsafe.Pointer(undef(e))
	case mTouch:
		dispatchTouch(e, arg)
		return unsafe.Pointer(undef(e))
	case mKey:
		dispatchKey(e, arg)
		return unsafe.Pointer(undef(e))
	case mResize:
		dispatchResize(e, arg)
		return unsafe.Pointer(undef(e))
	case mSetInsets:
		dispatchSetInsets(e, arg)
		return unsafe.Pointer(undef(e))
	case mIMECommit:
		if s := currentSurface(); s != nil {
			s.IMECommit(cstr(e, arg(0)))
		}
		return unsafe.Pointer(undef(e))
	case mDestroy:
		dispatchDestroy(e, arg)
		return unsafe.Pointer(undef(e))
	case mResolveNative:
		dispatchResolveNative(e, arg)
		return unsafe.Pointer(undef(e))
	case mReportBattery:
		dispatchReportBattery(e, arg)
		return unsafe.Pointer(undef(e))
	case mReportNetwork:
		dispatchReportNetwork(e, arg)
		return unsafe.Pointer(undef(e))
	case mReportLocation:
		dispatchReportLocation(e, arg)
		return unsafe.Pointer(undef(e))
	case mReportAppState:
		s := cstr(e, arg(0))
		gfx.Post(func() { gfx.ReportAppState(s) })
		return unsafe.Pointer(undef(e))
	case mReportMemoryWarning:
		gfx.Post(gfx.ReportMemoryWarning)
		return unsafe.Pointer(undef(e))
	case mReportPermission:
		k, st := cstr(e, arg(0)), cstr(e, arg(1))
		gfx.Post(func() { gfx.ReportPermission(k, st) })
		return unsafe.Pointer(undef(e))
	case mReportBackPress:
		return unsafe.Pointer(reportBackPress(e))
	case mSetDisplayFold:
		// **折叠屏上报的唯一入口** (HF2)。解析/校验/上报全部委托
		// gfx/mobile.ReportDisplayFold —— 与 Android/iOS 同一份实现。
		if err := mobile.ReportDisplayFold(cstr(e, arg(0))); err != nil {
			gfxharmony.Logf("setDisplayFold: %v", err)
		}
		return unsafe.Pointer(undef(e))
	default:
		gfxharmony.Logf("GoxDispatch: 未知方法序号 %d (两端方法表错位?)", int(method))
		return unsafe.Pointer(undef(e))
	}
}

// 方法序号常量 (与 C 侧 enum 同序; 上表 goxMethods 的下标就是它们)。
const (
	mInit                = 0
	mBindFrameBuffer     = 1
	mRunScript           = 2
	mTick                = 3
	mTouch               = 4
	mKey                 = 5
	mResize              = 6
	mSetInsets           = 7
	mIMECommit           = 8
	mDestroy             = 9
	mResolveNative       = 10
	mReportBattery       = 11
	mReportNetwork       = 12
	mReportLocation      = 13
	mReportAppState      = 14
	mReportMemoryWarning = 15
	mReportPermission    = 16
	mReportBackPress     = 17
	mSetDisplayFold      = 18
)

// ===== 各方法的实现 =====

func dispatchInit(e C.napi_env, arg func(int) C.napi_value) C.napi_value {
	okW, okH := C.bool(false), C.bool(false)
	w := C.gox_num(e, arg(0), 0, &okW)
	h := C.gox_num(e, arg(1), 0, &okH)
	okD := C.bool(false)
	density := C.gox_num(e, arg(2), 1, &okD)
	if !bool(okW) || !bool(okH) {
		gfxharmony.Logf("init: width/height 必须是数字")
		return C.gox_ret_int(e, 1)
	}
	if !bool(okD) || density <= 0 {
		density = 1
	}

	// 绑定顺序不能换: 先接上帧缓冲与回调, 再建表面 —— 反之首帧渲染时钩子还是空的。
	if err := gfxharmony.BindFrameBuffer(unsafe.Pointer(arg(3)), int(w)*int(h)*4); err != nil {
		return herd(e, err)
	}
	if err := gfxharmony.BindHost(unsafe.Pointer(arg(4))); err != nil {
		return herd(e, err)
	}

	s := mobile.New(mobile.Config{
		Width:    int(w),
		Height:   int(h),
		Density:  float64(density),
		ID:       "0",
		Name:     "HarmonyOS",
		Uploader: gfxharmony.Uploader(),
	})
	// 注册成默认窗口后端。移动端没有"进程启动即注册"的时机 (后端要靠宿主给的
	// 表面才能成立), 所以必须在这里显式做。
	s.Register()

	bindNativeHost()
	// 软键盘开关走 GoxHost.imeShow (焦点进/出编辑框时内核调, 见 gfx/mobile)。
	s.SetIMEHost(gfxharmony.CallIMEShow)

	mu.Lock()
	surface = s
	mu.Unlock()
	gfxharmony.Logf("libgox 启动: %dx%d density=%.2f, surface 已注册为默认窗口后端",
		int(w), int(h), float64(density))
	return C.gox_ret_int(e, 0)
}

func dispatchBindFrameBuffer(e C.napi_env, arg func(int) C.napi_value) C.napi_value {
	okW, okH := C.bool(false), C.bool(false)
	w := C.gox_num(e, arg(0), 0, &okW)
	h := C.gox_num(e, arg(1), 0, &okH)
	if !bool(okW) || !bool(okH) {
		gfxharmony.Logf("bindFrameBuffer: width/height 必须是数字")
		return C.gox_ret_int(e, 1)
	}
	if err := gfxharmony.BindFrameBuffer(unsafe.Pointer(arg(2)), int(w)*int(h)*4); err != nil {
		return herd(e, err)
	}
	return C.gox_ret_int(e, 0)
}

func dispatchRunScript(e C.napi_env, arg func(int) C.napi_value) C.napi_value {
	source := cstr(e, arg(0))
	label := cstr(e, arg(1))
	if label == "" {
		label = "harmony-script"
	}

	mu.Lock()
	if running {
		mu.Unlock()
		return C.gox_ret_int(e, 0)
	}
	running = true
	s := surface
	mu.Unlock()
	if s == nil {
		gfxharmony.NotifyFinished(1, "init 未调用")
		return C.gox_ret_int(e, 1)
	}

	// 立即返回, 真正的执行放在 Go 自己的线程上: NAPI 调用线程是 ArkTS 的 UI
	// 线程, 在里面跑事件泵会把界面冻住。
	go func() {
		// 内核纪律: 脚本、事件泵、VM 回调串行在同一条 OS 线程上。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()

		if err := runScript(source, label); err != nil {
			gfxharmony.Logf("%s: %v", label, err)
			gfxharmony.NotifyFinished(1, err.Error())
			return
		}
		gfxharmony.NotifyFinished(0, "")
	}()
	return C.gox_ret_int(e, 0)
}

func dispatchTouch(e C.napi_env, arg func(int) C.napi_value) {
	okA, okX, okY := C.bool(false), C.bool(false), C.bool(false)
	action := C.gox_num(e, arg(0), 0, &okA)
	x := C.gox_num(e, arg(1), 0, &okX)
	y := C.gox_num(e, arg(2), 0, &okY)
	if !bool(okA) || !bool(okX) || !bool(okY) {
		return
	}
	if s := currentSurface(); s != nil {
		s.Touch(harmonyAction(int(action)), int(x), int(y))
	}
}

func dispatchKey(e C.napi_env, arg func(int) C.napi_value) {
	key := cstr(e, arg(0))
	ok := C.bool(false)
	down := C.gox_bool(e, arg(1), false, &ok)
	s := currentSurface()
	if s == nil {
		return
	}
	kind := gfx.EventKeyUp
	if bool(down) {
		kind = gfx.EventKeyDown
	}
	// 软键盘走 IME 提交 (M2), 这里只服务外接键盘/按键。
	s.Post(gfx.Event{Kind: kind, Key: key})
}

func dispatchResize(e C.napi_env, arg func(int) C.napi_value) {
	okW, okH := C.bool(false), C.bool(false)
	w := C.gox_num(e, arg(0), 0, &okW)
	h := C.gox_num(e, arg(1), 0, &okH)
	okD := C.bool(false)
	density := C.gox_num(e, arg(2), 1, &okD)
	if !bool(okW) || !bool(okH) {
		return
	}
	if !bool(okD) || density <= 0 {
		density = 1
	}
	if s := currentSurface(); s != nil {
		s.Resize(int(w), int(h), float64(density))
	}
}

func dispatchSetInsets(e C.napi_env, arg func(int) C.napi_value) {
	vals := [4]int{}
	for i := 0; i < 4; i++ {
		ok := C.bool(false)
		v := C.gox_num(e, arg(i), 0, &ok)
		if bool(ok) {
			vals[i] = int(v)
		}
	}
	// 经 gfx.Post 投回 GUI 线程再报内核: ReportViewport 会同步跑脚本侧的
	// onViewportChange 订阅回调, 事件纪律 —— ArkTS 回调线程绝不直接执行 JS。
	gfx.Post(func() {
		gfx.ReportViewport(nil, gfx.Viewport{Insets: gfx.Insets{
			Top: vals[0], Right: vals[1], Bottom: vals[2], Left: vals[3],
		}})
	})
}

func dispatchDestroy(e C.napi_env, arg func(int) C.napi_value) {
	s := currentSurface()
	if s == nil {
		return
	}
	// Close 唤醒睡在 WaitEvents 里的泵 → 脚本线程收尾。
	s.Close()
	mu.Lock()
	surface = nil
	running = false
	mu.Unlock()
}

func dispatchResolveNative(e C.napi_env, arg func(int) C.napi_value) {
	idStr := cstr(e, arg(0))
	resStr := cstr(e, arg(1))
	codeStr := cstr(e, arg(2))
	msgStr := cstr(e, arg(3))
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

func dispatchReportBattery(e C.napi_env, arg func(int) C.napi_value) {
	o := cstrObj(e, arg(0))
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

func dispatchReportNetwork(e C.napi_env, arg func(int) C.napi_value) {
	o := cstrObj(e, arg(0))
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

func dispatchReportLocation(e C.napi_env, arg func(int) C.napi_value) {
	o := cstrObj(e, arg(0))
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

// reportBackPress 是唯一需要**同步**答案的上报: 返回键语义是"脚本处理了就不
// 退出" (见 native_app.go 文件头)。脚本回调必须跑在 GUI 线程上, 而调用方是
// ArkTS 的主线程, 所以: 投回 GUI 线程 → 用一次 Tick 叫醒可能睡着的泵 → 限时等待。
// 超时按未处理返回 —— 宁可退出也别把主线程吊死 (整窗无响应比"少拦一次返回键"糟)。
func reportBackPress(e C.napi_env) C.napi_value {
	done := make(chan bool, 1)
	gfx.Post(func() { done <- gfx.ReportBackPress() })
	if s := currentSurface(); s != nil {
		s.Tick()
	}
	select {
	case v := <-done:
		if v {
			return C.gox_ret_bool(e, true)
		}
	case <-time.After(500 * time.Millisecond):
		gfxharmony.Logf("reportBackPress: 500ms 内没有得到脚本答复, 按\"未处理\"返回")
	}
	return C.gox_ret_bool(e, false)
}

// undef 取 JS 的 undefined (给"无返回值"的方法当返回)。
func undef(e C.napi_env) C.napi_value {
	var out C.napi_value
	C.napi_get_undefined(e, &out)
	return out
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

// harmonyAction 把 ArkUI 的触摸类型翻译成 gfx/mobile 的动作常量。
// 不认识的一律当 Cancel —— 那是"丢掉这次手势"里最安全的一档 (不会留下按压态)。
func harmonyAction(v int) int {
	switch v {
	case actDown:
		return mobile.TouchDown
	case actMove:
		return mobile.TouchMove
	case actUp:
		return mobile.TouchUp
	default:
		return mobile.TouchCancel
	}
}

func currentSurface() *mobile.Surface {
	mu.Lock()
	defer mu.Unlock()
	return surface
}

// herd 把 Go 侧初始化错误变成返回值并打印。ArkTS 拿到非 0 应当直接报错退出,
// 而不是继续跑一个没有帧缓冲的会话 (那样的症状是"界面全黑但日志正常")。
func herd(e C.napi_env, err error) C.napi_value {
	gfxharmony.Logf("harmony init 失败: %v", err)
	return C.gox_ret_int(e, 1)
}

// cstr 取一个字符串参数 (非字符串返回空串)。
//
// 判 nil 必须先转 unsafe.Pointer: cgo 把 napi_value 映射成"底层是 unsafe.Pointer
// 的具名类型", 不能直接与 nil 比较 (与 Android 的 jobject / jstring 一个道理)。
func cstr(e C.napi_env, v C.napi_value) string {
	if unsafe.Pointer(v) == nil {
		return ""
	}
	var n C.size_t
	p := C.gox_str(e, v, &n)
	if p == nil {
		return ""
	}
	defer C.free(unsafe.Pointer(p))
	return C.GoStringN(p, C.int(n))
}

// ===== NativeHost 通道: Go ↔ ArkTS 的宿主契约翻译 (见文件头协议) =====

// bindNativeHost 注册 gfx.NativeHost 的鸿蒙实现。找不到宿主方法不算致命:
// 注册一个空能力宿主, 内核对所有原生调用显式 reject(unsupported)。
//
// 与 Android 的差异: Android 侧在这里**主动查方法 id 并缓存**; NAPI 侧查属性
// 必须在有 napi_env 的调用栈里做, 而 Call 本来就在 GUI 线程 (有 env), 所以
// 这里只标记"可以尝试", 真正的属性查找放到 Call 里 —— 少一层缓存, 也少一类
// "ref 过期"的坑。
func bindNativeHost() {
	gfx.SetNativeHost(&napiNativeHost{})
}

// napiNativeHost 是 gfx.NativeHost 的 HarmonyOS 实现。
type napiNativeHost struct{}

// Capabilities 报告 ArkTS 宿主声明的方法名/能力名 (逗号分隔字符串 → 切片)。
func (h *napiNativeHost) Capabilities() []string {
	out := []string{}
	s, err := gfxharmony.CallHost("nativeCapabilities")
	if err != nil {
		// 宿主还没绑上 (init 的早期) 不是错误: 此时内核还没开始问能力。
		return out
	}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// pendingMarker 是 ArkTS 侧"稍后回填"的哨兵 (协议见文件头)。
const pendingMarker = "__pending__"

// Call 执行一个原生方法: 参数序列化成 JSON 传给 ArkTS, 回包按协议三态解析。
func (h *napiNativeHost) Call(method string, args object.Value) gfx.NativeCallResult {
	s, err := gfxharmony.CallHost("nativeCall", method, objToJSON(args))
	if err != nil {
		return gfx.NativeFailure(gfx.ErrUnsupported,
			"HarmonyOS 宿主未注册 nativeCall (GoxNativeHost 缺失?), 方法 %s 不可用", method)
	}
	return parseHostReply(s)
}

// parseHostReply 按 ArkTS 侧回包协议三态解析 (文件头: pending / 错误信封 / 结果)。
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

// ── JSON ↔ object.Value (args 下行 / 结果回填上行共用的窄转换) ──
//
// **与 Android 的实现是同一份逻辑, 但刻意各留一份**: 两个包分属不同的 build
// tag (android / harmony), 抽到公共包会让桌面构建也拉进这些只在移动端用的
// 代码; 而这段逻辑只有 ~120 行、且改动频率极低。真要合并, 前提是先有第三处
// 使用点。

// objToJSON 把内核给的参数对象序列化成 JSON 文本。
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

// quoteJSON 带引号字符串 (控制字符与引号/反斜杠转义; 非 BMP 字符原样输出 UTF-8)。
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

// cstrObj 把参数解析成 *object.Object (失败返回 nil)。
func cstrObj(e C.napi_env, v C.napi_value) *object.Object {
	s := cstr(e, v)
	if s == "" {
		return nil
	}
	val, err := jsonToObj(s)
	if err != nil {
		return nil
	}
	o, _ := val.(*object.Object)
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
