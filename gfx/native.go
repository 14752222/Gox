package gfx

// ===== 统一应用能力层 (gx/device · gx/app · gx/geo · gx/media · gx/permission) =====
//
// ## 为什么需要这一层
//
// 到 2026-09-21 为止, 脚本能碰到的平台能力是**一个一个长出来**的: 剪贴板挂在
// Surface 上 (clipboardHost), 原生对话框挂 Surface 上 (nativeDialogHost), 存储
// 是纯脚本模块 (gx/storage), 屏幕/姿态是另一套 (gx/screen)。桌面够用 —— 因为
// 桌面只有一种语言 (Go), 每个能力都能在 Go 侧写完。
//
// 移动端把这个前提打破了: 电池、定位、相机、相册、权限**只存在于宿主语言里**
// (Kotlin / Swift / ArkTS), Go 侧写不出实现, 只能"请宿主代做"。若沿用桌面那套
// "每个能力一个可选接口", 移动端每加一个能力都要动一次 JNI/ObjC 的 ABI
// (新增 native 方法、改头文件、重新对齐参数) —— 那是几十次破坏性改动。所以
// 这里换一条路:
//
//	一个宿主契约 (NativeHost) + 一套命名约定 + 一条异步回填通道
//
// 新增能力 = 内核加一个方法名 + 宿主加一个分支, **ABI 永远是一次调用**。
// 词汇参照 uniapp 的 getSystemInfo/getLocation/chooseImage 那一族, 但不承诺
// 逐 API 对齐 (选型见 agent_doc/gui-device-api-options.md §3.2 方案 B)。
//
// ## 四件套
//
//  1. **宿主契约** `NativeHost`: 两个方法 —— `Capabilities()` 报告"我会哪些
//     方法", `Call(method, args)` 实际执行。**进程级唯一实例**, 不是 Surface
//     的可选接口: 定位/电池/震动不需要窗口, 而移动端"一个进程 = 一个 App"。
//     桌面上没有第二层语言边界, 所以桌面宿主就是后端包自己 (见 gfx/win32/host.go)
//     —— 它同时是"宿主该怎么写"的活样板。
//  2. **命名约定**: 方法名是 `<能力>.<动作>` ("camera.takePhoto"), 能力 ID 就是
//     第一个点之前那段。canIUse / 错误消息 / 文档全部从这一条推导, 不需要第二
//     张映射表 (两处各写一份必然先分叉后矛盾)。
//  3. **pending 回填**: 相机/定位要等用户或系统, 宿主当场给不出结果。`Call`
//     返回 `Pending`, 内核把 Promise 存进待决表, 宿主稍后经 `gfx.Post` +
//     `ResolveNative(id, …)` 回填。**这就是移动桥的全部机制** —— 同一个 id 通道
//     还兼管"持续推送" (定位监听), 见 nativeCall.stream。
//  4. **上报通道**: 电池/网络/前后台/安全区这些"宿主主动告知"的状态不需要
//     Call, 宿主调 `ReportBattery` / `ReportNetwork` / `ReportAppState` /
//     `ReportViewport`, 内核存起来 + 抬高环境版本, 脚本侧用 `useBattery()` 这类
//     取值函数自动跟着变 —— 与 gx/screen 的 reportPosture 完全同构, 因此
//     **整条链路能在桌面单测里跑完, 不需要真机**。
//
// ## 降级口径 (与剪贴板那条线不同)
//
// 剪贴板缺能力返回空串 / false —— 沿用浏览器语义, "没有剪贴板"是正常状态。
// 相机/定位**不能软降级**: 静默返回假数据会污染业务逻辑 (你以为拍到了, 其实
// 没有)。所以这一层的规矩是:
//
//	能力缺失 → Promise 被 reject, 错误码 `unsupported`;
//	写代码的人先用 `canIUse("camera")` 事前判断, 而不是靠 catch 兜底。
//
// 唯一例外是"纯上报型"状态 (电池/网络/前后台/安全区): 没人上报时读到的是一份
// **明确的缺省值** (`supported:false` / `connected:false` / insets 全 0),
// 不是错误 —— 桌面没有宿主时它们照样可读, 这与 canIUse 的结论一致。
//
// ## 线程纪律 (复用仓库唯一的跨线程入口)
//
// `Call` 在 GUI 线程被调用 (脚本调进去的)。宿主实现里:
//
//   - 需要用户交互 / 系统回调的 (相机、定位、权限弹窗) —— **当场返回 Pending**,
//     在系统回调里 `gfx.Post(func(){ gfx.ResolveNative(id, …) })`;
//   - 同步可得的 (设备型号、震动、屏幕常亮) —— 直接返回结果。
//
// 绝不在宿主自己的线程里 resolve Promise: `Promise.Resolve` 会**同步**跑 then
// 回调 (见 object/promise.go), 而 JS 必须只在 GUI 线程执行 (dialog.go 文件头
// 记着同一条教训)。`ResolveNative` 因此与 `NotifyDisplaysChanged` 同档:
// **约定在 GUI 线程调用**, 跨线程必须先 `Post`。

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/14752222/Gox/object"
)

// ===== 错误模型 =====

// 错误码枚举。跨平台统一 —— 平台差异必须被压进这几个词里, 否则每个应用都要
// 为一平台写特判 (选型文档 §1.3 的 C4)。
const (
	// ErrUnsupported: 宿主没实现这个能力 (canIUse 会是 false)。
	ErrUnsupported = "unsupported"
	// ErrPermissionDenied: 用户或系统拒绝了权限。可提示用户去设置页开。
	ErrPermissionDenied = "permission-denied"
	// ErrCancelled: 用户在系统 UI 里主动取消 (拍照取消、选图返回)。
	// **它不是失败**: 与 gx/dialog 的 openFile 取消返回 null 同一立场 —— 但相机
	// 无法用 null 表达"没拍到"与"拍到了但读不出来"的区别, 所以走错误码。
	ErrCancelled = "cancelled"
	// ErrTimeout: 宿主在约定时间内没回填结果。
	ErrTimeout = "timeout"
	// ErrBusy: 上一次还没结束 (相机被占用、定位正在更新中)。
	ErrBusy = "busy"
	// ErrUnavailable: 设备/系统当前不可用 (飞行模式下的定位、没有相机硬件)。
	ErrUnavailable = "unavailable"
	// ErrPlatform: 原生侧报错, 细节在 errMsg 里 (不要解析 errMsg, 只用于显示)。
	ErrPlatform = "platform-error"
	// ErrInvalidArg: 参数不合法 (count 为负数、kind 不认识)。
	ErrInvalidArg = "invalid-arg"
)

// NativeError 是这一层唯一的错误形状, 到 JS 侧变成
// `{errCode, errMsg, name, message}`。
//
// 同时给 errCode/errMsg (uniapp 词汇) 与 name/message (JS Error 的既有习惯)
// 两套键: 前者便于 `e.errCode === "cancelled"` 这种判断, 后者让
// `catch (e) { console.log(e.message) }` 不至于打印出 undefined。
type NativeError struct {
	Code string
	Msg  string
}

func (e *NativeError) Error() string { return e.Code + ": " + e.Msg }

// nativeErr 造一个错误 (短名, 调用点密度高)。
func nativeErr(code, format string, args ...interface{}) *NativeError {
	return &NativeError{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// toJS 把错误转成脚本侧的普通对象。
func (e *NativeError) toJS() object.Value {
	if e == nil {
		return object.NullSingleton
	}
	o := object.NewObject()
	o.SetProperty("errCode", object.NewString(e.Code))
	o.SetProperty("errMsg", object.NewString(e.Msg))
	o.SetProperty("name", object.NewString(e.Code))
	o.SetProperty("message", object.NewString(e.Msg))
	return o
}

// ===== 宿主契约 =====

// NativeCallResult 是 `Call` 的返回。
//
// 三选一, 顺序即优先级: Pending > Err > Result。
// 用结构体而不是 `(object.Value, error)`: Go 的 error 在这里表达力不够 ——
// "稍后回填"既不是成功也不是失败, 硬塞进 err 会让每个调用点都要写
// `if err == errPending` 的特判 (而那个哨兵一旦被包装就失效)。
type NativeCallResult struct {
	// Result 是成功时的结果; nil 视同 undefined。
	Result object.Value
	// Err 非空表示失败 (用 nativeErr / NativeFailure 造)。
	Err *NativeError
	// Pending 为 true 表示"结果稍后经 ResolveNative 回填", 此时忽略上面两个字段。
	Pending bool
}

// NativeResult / NativePending / NativeFailure 是宿主侧的三个构造助手。
func NativeResult(v object.Value) NativeCallResult { return NativeCallResult{Result: v} }
func NativePending() NativeCallResult              { return NativeCallResult{Pending: true} }

// NativeFailure 造一个失败结果 (code 取上面的错误码常量)。
func NativeFailure(code, format string, args ...interface{}) NativeCallResult {
	return NativeCallResult{Err: nativeErr(code, format, args...)}
}

// NativeHost 是宿主原生能力的唯一入口。
//
// 实现者只需回答两个问题: "我会哪些方法" 与 "执行一个方法"。**不要**在这个
// 接口上按能力加方法 —— 那正是桌面那套做法在移动端会失控的地方 (见文件头)。
//
// Capabilities 的元素有两种形态, 二者都合法:
//
//	"camera.takePhoto"  方法名 —— 能力 ID 取第一个点之前那段 ("camera")
//	"insets"            能力名 —— 用于"纯上报型"能力 (只有 Report 没有 Call),
//	                    或"由别的模块实现、但我能确认平台支持"的能力
//	                    (桌面上剪贴板/原生对话框就是这样)
//
// 宿主可以多报 (报了但 Call 时返回 unsupported 也不会崩, 只是 canIUse 会说谎),
// 但不许少报 —— canIUse 的结论就来自这张表。
type NativeHost interface {
	Capabilities() []string
	Call(method string, args object.Value) NativeCallResult
}

var (
	nativeMu       sync.Mutex
	nativeHostInst NativeHost
	nativeCapSet   map[string]bool // 宿主声明的方法名与能力名 (注册时快照)
	nativeCalls    map[string]*nativeCall
	nativeSeq      int
	// nativeRevision 是"原生环境版本"(电池/网络/前后台/安全区任一变化 → +1)。
	// 与 screen.go 的 screenRevision 分开: **display 变化不该惊动
	// onBatteryChange 之类的回调**, 两套订阅各自独立。
	nativeRevision int
	nativeEnvGet   object.Value
	nativeEnvSet   object.Value
	nativeHooks    map[string][]object.Value // 事件名 → 回调表
)

// nativeCall 是一次待决的原生调用。
type nativeCall struct {
	method  string
	promise *object.Promise // stream 时为 nil
	cb      object.Value    // 可选回调 (err-first); 与 promise 二选一
	sink    object.Value    // stream: 每次推送调它
	timer   int             // 超时定时器 (0 = 没设)
	stream  bool            // true = 持续推送 (定位监听), 回填后**不**摘除
	closed  bool
}

// SetNativeHost 注册宿主原生实现 (由后端包 / 移动壳的 init 调用)。
//
// 传 nil 表示撤销宿主, 此时**所有待决调用立刻被 reject(unsupported)**:
// 不这么做的话, 换了宿主 (或测试拆卸) 之后那些 Promise 永远 pending —— 而
// pending 的 Promise 不会挂住事件循环, 症状是"await 之后再也没动静", 比报错
// 难查得多。
func SetNativeHost(h NativeHost) {
	nativeMu.Lock()
	nativeHostInst = h
	nativeCapSet = nil
	if h != nil {
		set := make(map[string]bool)
		for _, c := range h.Capabilities() {
			c = strings.TrimSpace(c)
			if c != "" {
				set[c] = true
			}
		}
		nativeCapSet = set
	}
	victims := make([]*nativeCall, 0, len(nativeCalls))
	for id, c := range nativeCalls {
		victims = append(victims, c)
		delete(nativeCalls, id)
	}
	nativeMu.Unlock()
	// 摘除之后在锁外收尾 (回调是脚本函数, 持锁调脚本是死锁配方)。
	for _, c := range victims {
		closeNativeCall(c, nil, nativeErr(ErrUnsupported, "宿主已卸载, 能力不可用"))
	}
}

// HasNativeHost 报告当前有没有宿主 (测试与 gx/dev 用)。
func HasNativeHost() bool {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return nativeHostInst != nil
}

// nativeDeclared 报告宿主是否声明了某个方法名或能力名。
func nativeDeclared(name string) bool {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return nativeCapSet[name]
}

// nativeMethodCapability 取方法名对应的能力 ID ("camera.takePhoto" → "camera")。
// 没有点的名字按能力名处理 (纯上报型)。
func nativeMethodCapability(method string) string {
	if i := strings.IndexByte(method, '.'); i > 0 {
		return method[:i]
	}
	return method
}

// NativeCapabilities 返回宿主声明的能力 ID (去重 + 排序), 供 `capabilities()`
// 使用。**只报能力级** —— 方法名是内核与宿主之间的契约细节, 不该成为应用代码
// 的依赖面 (应用要问"有没有"就用 canIUse)。
func NativeCapabilities() []string {
	nativeMu.Lock()
	seen := make(map[string]bool)
	for c := range nativeCapSet {
		seen[nativeMethodCapability(c)] = true
	}
	nativeMu.Unlock()
	out := make([]string, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// CanIUse 是 `canIUse(cap)` 的 Go 侧实现。
//
// 粒度: cap 既可以是能力 ID ("camera"), 也可以是具体方法名
// ("camera.takePhoto") —— 后者对"只想确认某个 API 有没有"的场合更直接。
//
// 判据顺序 (先平台后内核, 因为平台能力是"真做得到"的那部分):
//
//  1. 宿主声明过 → 可用。**双向匹配**: 声明 "camera" 能回答 canIUse("camera")
//     也能回答 canIUse("camera.takePhoto"); 声明 "camera.takePhoto" 则两者
//     反过来也能回答。宿主"整能力都会"用能力名声明、"只会一个方法"用方法名
//     声明, 两种写法对应用都该透明。
//  2. 存在同名内置模块 `gx/<cap>` → 可用 (solid/view/router/screen/storage/dev …);
//  3. 都没有 → 不可用。
func CanIUse(cap string) bool {
	cap = strings.TrimSpace(cap)
	if cap == "" {
		return false
	}
	nativeMu.Lock()
	declared := nativeCapSet[cap]
	if !declared {
		capID := nativeMethodCapability(cap)
		for name := range nativeCapSet {
			if name == cap || nativeMethodCapability(name) == cap || capID == name {
				declared = true
				break
			}
		}
	}
	nativeMu.Unlock()
	if declared {
		return true
	}
	_, ok := object.LookupBuiltinModule("gx/" + cap)
	return ok
}

// ===== 环境状态: 版本号 signal + 回调表 =====

// nativeEnvSignal 返回原生环境版本号的 signal getter (惰性建)。
//
// 与 screen.go 的 envSignal 同一套路 (复用 gx/solid): 脚本侧"读它 = 订阅它",
// 于是 useBattery() 这类取值函数放进函数 prop / 函数子节点里就自动响应式。
// gx/solid 不可用时返回 nil, 调用方退化为"只读一次快照"。
func nativeEnvSignal() object.Value {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return nativeEnvSignalLocked()
}

func nativeEnvSignalLocked() object.Value {
	if nativeEnvGet != nil {
		return nativeEnvGet
	}
	exports, ok := object.LookupBuiltinModule("gx/solid")
	if !ok {
		return nil
	}
	createSignal, ok := exports["createSignal"]
	if !ok || !object.IsCallable(createSignal) {
		return nil
	}
	res := object.CallFunction(createSignal, nil, object.NewNumber(0))
	arr, ok := res.(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		return nil
	}
	nativeEnvGet, nativeEnvSet = arr.Elements[0], arr.Elements[1]
	return nativeEnvGet
}

// NativeEnvRevision 报告当前原生环境版本 (测试断言"变更确实抬高了版本")。
func NativeEnvRevision() int {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return nativeRevision
}

// notifyNativeChanged 抬高版本 + 派发某个事件的回调。
//
// event 为空表示"状态变了但没有专属事件" (仍要刷版本号让 useXxx 重算)。
// 与 screen.go 的 notifyEnvChanged 一样是**同步**通知: 回调里常见的动作就是
// 写 signal → 标脏 → 本帧重绘, 跨轮次反而多一帧延迟。代价是回调抛错必须收走
// (recordWarn), 否则一次笔误会让整帧挂掉。
func notifyNativeChanged(event string) {
	nativeMu.Lock()
	nativeRevision++
	set := nativeEnvSet
	hooks := append([]object.Value(nil), nativeHooks[event]...)
	nativeMu.Unlock()

	if set != nil {
		object.CallFunction(set, nil, object.NewNumber(float64(nativeRevision)))
	}
	for _, fn := range hooks {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/native %s 回调抛错: %v", event, err)
		}
	}
}

// addNativeHook 登记一个事件回调, 返回注销函数 (可重复调用)。
func addNativeHook(event, apiName string, fn object.Value) object.Value {
	if !object.IsCallable(fn) {
		return object.NewTypeError("%s: 需要回调函数", apiName)
	}
	nativeMu.Lock()
	if nativeHooks == nil {
		nativeHooks = map[string][]object.Value{}
	}
	nativeHooks[event] = append(nativeHooks[event], fn)
	nativeMu.Unlock()
	return object.NewBuiltin("off"+apiName, func(args ...object.Value) object.Value {
		removeNativeHook(event, fn)
		return object.UndefinedSingleton
	})
}

// removeNativeHook 注销 (找不到就静默)。
func removeNativeHook(event string, fn object.Value) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	list := nativeHooks[event]
	for i, h := range list {
		if h == fn {
			nativeHooks[event] = append(list[:i], list[i+1:]...)
			break
		}
	}
}

// nativeHookAPI 是 onXxx/offXxx 两个入口的公共实现 (off 为 true 时注销)。
func nativeHookAPI(event, apiName string, off bool) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		if len(args) == 0 || !object.IsCallable(args[0]) {
			return object.NewTypeError("%s: 需要回调函数", apiName)
		}
		if off {
			removeNativeHook(event, args[0])
			return object.UndefinedSingleton
		}
		return addNativeHook(event, apiName, args[0])
	}
}

// ===== 待决调用的回填通道 =====

// nativeNextIDLocked 分配一个调用 id (持 nativeMu)。
func nativeNextIDLocked() string {
	if nativeCalls == nil {
		nativeCalls = map[string]*nativeCall{}
	}
	nativeSeq++
	return fmt.Sprintf("n%d", nativeSeq)
}

// nativeHostLocked 取宿主并判断它是否声明了该方法 (方法名精确匹配, 或它的
// 能力 ID 被整体声明 —— 后者是"这个能力我全都会"的简写)。
func nativeHostLocked(method string) (NativeHost, bool) {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if nativeHostInst == nil {
		return nil, false
	}
	if nativeCapSet[method] || nativeCapSet[nativeMethodCapability(method)] {
		return nativeHostInst, true
	}
	return nativeHostInst, false
}

// callNative 发起一次原生调用, 返回 Promise (传了回调时返回 undefined)。
//
// 三种终局都在这里统一处理: 能力缺失 / 宿主当即给结果 / 宿主 pending。
func callNative(method string, opts *object.Object, cb object.Value) object.Value {
	p := object.NewPromise()

	host, ok := nativeHostLocked(method)
	if host == nil || !ok {
		// 能力缺失 → 显式失败 (文件头的降级口径)。这里**不**延迟到微任务:
		// 调用当场就知道没戏, 让 catch 尽早跑到更符合直觉, 且纯 Go 单测里
		// 不依赖事件循环也能看到结果。
		reason := nativeErr(ErrUnsupported, "当前平台不支持 %s (canIUse(\"%s\") 为 false)",
			method, nativeMethodCapability(method)).toJS()
		if cb != nil {
			object.CallFunction(cb, nil, reason, object.UndefinedSingleton)
			return object.UndefinedSingleton
		}
		p.Reject(reason)
		return p
	}

	nativeMu.Lock()
	id := nativeNextIDLocked()
	c := &nativeCall{method: method, promise: p, cb: cb}
	nativeCalls[id] = c
	nativeMu.Unlock()
	if ms := nativeTimeoutOf(opts); ms > 0 {
		c.timer = scheduleNativeTimeout(id, ms)
	}

	res := host.Call(method, nativeArgsWithID(opts, id))
	switch {
	case res.Pending:
		// 等 ResolveNative —— 什么都不做。
	case res.Err != nil:
		nativeMu.Lock()
		delete(nativeCalls, id)
		nativeMu.Unlock()
		settleNative(c, nil, res.Err)
	default:
		nativeMu.Lock()
		delete(nativeCalls, id)
		nativeMu.Unlock()
		settleNative(c, res.Result, nil)
	}
	if cb != nil {
		return object.UndefinedSingleton
	}
	return p
}

// scheduleNativeTimeout 起一个超时定时器 (到点把调用判成 timeout)。
func scheduleNativeTimeout(id string, ms int) int {
	return object.GlobalScheduler().SetTimeout(object.NewBuiltin("__native_timeout",
		func(args ...object.Value) object.Value {
			nativeMu.Lock()
			c := nativeCalls[id]
			if c != nil {
				delete(nativeCalls, id)
			}
			nativeMu.Unlock()
			if c != nil {
				settleNative(c, nil, nativeErr(ErrTimeout, "%s 超时 (%dms 内没有结果)", c.method, ms))
			}
			return object.UndefinedSingleton
		}), durationMS(ms))
}

// settleNative 收尾一次非流式调用: 摘除回调 (err-first) 或 resolve/reject Promise。
//
// **走 resolveDeferred**: Promise 是微任务, 就地 resolve 会让 `await` 之后的
// 代码在本次脚本执行途中同步跑起来 (dialog.go 记着同一条)。回调式也一样 ——
// 调用方刚拿到返回值就看见回调在跑, 会踩到"变量还没赋值"的坑。
func settleNative(c *nativeCall, result object.Value, err *NativeError) {
	nativeMu.Lock()
	if c.closed {
		nativeMu.Unlock()
		return
	}
	c.closed = true
	nativeMu.Unlock()

	if c.timer != 0 {
		object.GlobalScheduler().Clear(c.timer)
	}
	resolveDeferred(func() {
		if err != nil {
			if c.cb != nil {
				object.CallFunction(c.cb, nil, err.toJS(), object.UndefinedSingleton)
			} else if c.promise != nil {
				c.promise.Reject(err.toJS())
			}
			return
		}
		if result == nil {
			result = object.UndefinedSingleton
		}
		if c.cb != nil {
			object.CallFunction(c.cb, nil, object.NullSingleton, result)
			return
		}
		if c.promise != nil {
			c.promise.Resolve(result)
		}
	})
}

// closeNativeCall 是 SetNativeHost(nil) 的收尾路径 (与 settleNative 同一形状)。
func closeNativeCall(c *nativeCall, result object.Value, err *NativeError) {
	settleNative(c, result, err)
}

// ResolveNative 由宿主调用, 交付一次异步调用的结果。
//
// id 来自 `Call` 收到的参数对象里的 `__id` 字段 (宿主照抄即可) —— 用下划线前缀
// 是为了让它在脚本侧一看就是"内部字段", 不会有人手写它。
//
// **约定在 GUI 线程调用**。宿主在自己的线程里拿到系统回调时, 正确写法是:
//
//	gfx.Post(func() { gfx.ResolveNative(id, gfx.NativeResult(jsObj), nil) })
//
// 对**流式**调用 (定位监听) 语义不同: 同一个 id 可以反复回填, 每次把结果投给
// sink 回调, 不摘除条目; 直到宿主回填一个"致命"错误 (unsupported / cancelled /
// permission-denied / unavailable) 或脚本调 clearWatch。
func ResolveNative(id string, result object.Value, err *NativeError) {
	nativeMu.Lock()
	c := nativeCalls[id]
	if c != nil && !c.stream {
		delete(nativeCalls, id)
	}
	nativeMu.Unlock()
	if c == nil {
		// 迟到的回填 (超时之后宿主才答, 或脚本已经 clearWatch): 出声但不报错。
		recordWarn("gx/native: 收到未知调用 %s 的结果 (已超时/已取消?), 已忽略", id)
		return
	}
	if c.stream {
		if err != nil && nativeErrFatal(err.Code) {
			nativeMu.Lock()
			delete(nativeCalls, id)
			nativeMu.Unlock()
			c.closed = true
		}
		sink := c.sink
		resolveDeferred(func() {
			var errVal object.Value = object.NullSingleton
			if err != nil {
				errVal = err.toJS()
			}
			if result == nil {
				result = object.UndefinedSingleton
			}
			object.CallFunction(sink, nil, result, errVal)
		})
		return
	}
	settleNative(c, result, err)
}

// nativeErrFatal 报告该错误码是否终止一个流 (能恢复的不终止: 一次定位失败
// 不该让整个监听停掉)。
func nativeErrFatal(code string) bool {
	switch code {
	case ErrUnsupported, ErrCancelled, ErrPermissionDenied, ErrUnavailable:
		return true
	}
	return false
}

// startNativeStream 起一个流式原生调用, 成功返回 id。
//
// 与 callNative 的区别: 同步、无 Promise —— 脚本侧 `watchLocation(cb)` 拿到的
// 是一个 id, 用 `clearWatch(id)` 停。之所以不做成"返回一个订阅对象", 是因为 id
// 能直接当 Map 的键, 而对象还要额外约定相等语义。
func startNativeStream(method string, opts *object.Object, sink object.Value) (string, *NativeError) {
	host, ok := nativeHostLocked(method)
	if host == nil || !ok {
		return "", nativeErr(ErrUnsupported, "当前平台不支持 %s", method)
	}
	nativeMu.Lock()
	id := nativeNextIDLocked()
	nativeCalls[id] = &nativeCall{method: method, sink: sink, stream: true}
	nativeMu.Unlock()

	res := host.Call(method, nativeArgsWithID(opts, id))
	if res.Err != nil {
		nativeMu.Lock()
		delete(nativeCalls, id)
		nativeMu.Unlock()
		return "", res.Err
	}
	// Pending 是常态 (流在系统回调里持续推送)。结果型返回也允许: 宿主当场就有
	// 一份 (比如缓存里的定位), 直接投给 sink。
	if !res.Pending {
		ResolveNative(id, res.Result, nil)
	}
	return id, nil
}

// stopNativeStream 停一个流: 摘除条目 + 通知宿主别再推了。
func stopNativeStream(id string) {
	nativeMu.Lock()
	c := nativeCalls[id]
	if c != nil && c.stream {
		delete(nativeCalls, id)
	}
	host := nativeHostInst
	nativeMu.Unlock()
	if c == nil || !c.stream {
		return
	}
	c.closed = true
	if host != nil {
		// 停流是"尽力而为": 宿主没实现 unwatch 也不该报错 (它会自己在下一次
		// 推送时收到未知 id 而忽略)。
		_ = host.Call(nativeStreamStopMethod(c.method), nativeArgsWithID(nil, id))
	}
}

// nativeStreamStopMethod 把 "location.watch" 映射到 "location.unwatch"。
func nativeStreamStopMethod(method string) string {
	if i := strings.IndexByte(method, '.'); i > 0 {
		return method[:i] + ".unwatch"
	}
	return method
}

// PendingNativeCalls 报告待决调用数 (测试用: 断言"没有泄漏的 pending")。
func PendingNativeCalls() int {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return len(nativeCalls)
}

// resetNativeStateForTest 清空宿主、待决表与回调表。
//
// 本模块的状态全是**包级单例** (宿主、信号、回调), 跨用例必然串味 —— 与
// resetScreenStateForTest / resetRouterStateForTest 同一纪律。要在 t.Cleanup 里
// 调, 且调之前别留 pending (否则上一条用例的 Promise 永不结算)。
func resetNativeStateForTest() {
	nativeMu.Lock()
	nativeHostInst = nil
	nativeCapSet = nil
	for _, c := range nativeCalls {
		c.closed = true
		if c.timer != 0 {
			object.GlobalScheduler().Clear(c.timer)
		}
	}
	nativeCalls = nil
	nativeHooks = nil
	nativeSeq = 0
	nativeRevision++
	nativeEnvGet, nativeEnvSet = nil, nil
	nativeMu.Unlock()
}

// ===== 参数解析与 JS 对象构造的公共助手 =====

// nativeOpts 取第 i 个参数当选项对象 (不是对象 / 缺失 → 空对象, 不报错)。
//
// 宽容处理的理由与 gx/dialog 的 parseFileOptions 一致: 选项写错最坏的后果
// 应该是"选项没生效", 而不是"整个调用抛异常"。
func nativeOpts(args []object.Value, i int) *object.Object {
	if i >= len(args) {
		return object.NewObject()
	}
	if o, ok := args[i].(*object.Object); ok {
		return o
	}
	return object.NewObject()
}

// nativeOptFunc 从参数里取可选回调 (第 from 个起的第一个函数参数)。
//
// 双模约定与 stdlib/http.go 一致: **有回调走回调, 没回调返回 Promise**。
// 回调是 err-first: `cb(err, result)`。
func nativeOptFunc(args []object.Value, from int) object.Value {
	for i := from; i < len(args); i++ {
		if object.IsCallable(args[i]) {
			return args[i]
		}
	}
	return nil
}

// nativeArgsWithID 造宿主收到的参数对象: 调用方给的选项 + 内部字段 `__id`。
//
// 为什么给宿主看 `__id`: 异步回填 (ResolveNative) 需要它当钥匙。给的是**副本**
// —— 脚本改自己那份选项不该影响宿主那一份。
func nativeArgsWithID(opts *object.Object, id string) object.Value {
	out := object.NewObject()
	if opts != nil {
		for _, k := range opts.Keys() {
			if v, ok := opts.GetProperty(k); ok {
				out.SetProperty(k, v)
			}
		}
	}
	out.SetProperty("__id", object.NewString(id))
	return out
}

// nativeTimeoutOf 读 opts.timeout (毫秒; <=0 或缺失 → 0 = 不超时)。
//
// 缺省**不超时**是有意的: 相机取景、权限弹窗可能等用户很久, 一个兜底超时会
// 把"用户还在挑照片"判成失败。想要兜底的调用方自己传 timeout。
func nativeTimeoutOf(opts *object.Object) int {
	n := objPropNum(opts, "timeout")
	if n <= 0 {
		return 0
	}
	if n > 3600*1000 {
		return 3600 * 1000
	}
	return int(n)
}

// nativeBool 把任意 Value 转布尔 (缺失 → true, 用于"选项默认开"的场合)。
func nativeBool(v object.Value) bool {
	return v != nil && v.IsTruthy()
}

// nativeSet 给对象写一个字符串属性 (批量构造时少写一半样板)。
func nativeSet(o *object.Object, key, val string) {
	o.SetProperty(key, object.NewString(val))
}

// durationMS 把毫秒转成 time.Duration。
func durationMS(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}
