package gfx

// ===== gx/permission: 权限查询 / 申请 / 状态变更 =====
//
// ## 为什么权限要在 API 层面预留位置 (而不是等真机再补)
//
// 相机、定位、麦克风在 Android 6+ / iOS 全是**运行时权限**: 调用系统 API 之前
// 必须先申请, 而申请结果可能是"拒绝"或"永久拒绝"(后者只能引导用户去设置页)。
// 如果 API 形态里没有权限位, 那么移动端落地时就要给所有相关签名做破坏性变更
// (选型文档 §1.2 把这个列为"移动端的前置")。所以这里先把三件事定下来:
//
//	getSetting()     一次拿到全部权限状态 (渲染"授权引导页"用)
//	authorize(kind)  触发起系统弹窗, 返回申请后的状态
//	checkPermission()同步判断 (从缓存读, 不碰宿主 —— 渲染路径上可以随便调)
//
// ## 状态词表 (六个词, 一个都不能省)
//
//	granted        已授权
//	denied         已拒绝 (**可以再弹一次**)
//	not-determined 还没问过 —— 与 denied 必须分开: 前者该弹窗, 后者弹了也没用
//	restricted     系统策略禁止 (家长控制、MDM), 用户改不了
//	limited        部分授权 (iOS 相册的"仅选中的照片"、Android 14 的部分照片访问)
//	unknown        问不到 (桌面 / 没有宿主)
//
// `limited` 是 iOS 相册的常态, 把它当成 `denied` 会让应用错误地退化成"没有照片
// 权"从而不显示任何图片; 当成 `granted` 又会在读全量照片时失败。所以它必须
// 有独立取值, 应用侧自行决定怎么处理。

import (
	"strings"

	"github.com/14752222/Gox/object"
)

// 权限状态取值。
const (
	PermGranted       = "granted"
	PermDenied        = "denied"
	PermNotDetermined = "not-determined"
	PermRestricted    = "restricted"
	PermLimited       = "limited"
	PermUnknown       = "unknown"
)

// 方法名。
const (
	nmPermGet     = "permission.get"
	nmPermRequest = "permission.request"
)

// evPermission 是权限状态变化的事件名。
const evPermission = "permission"

// permissionKindList 是可查询的权限种类 (白名单)。
//
// 白名单而不是自由字符串: kind 最终映射到 Android 的 Manifest.permission.* 或
// iOS 的 AVAuthorizationStatus 系列, 拼错在各平台表现不一。列在这里也让
// `getSetting()` 的返回**形状稳定** —— 应用可以直接解构而不必判空。
var permissionKindList = []string{
	"location", "camera", "gallery", "microphone",
	"notification", "contacts", "calendar", "bluetooth", "storage",
}

// permissionStates 是各权限的缓存状态 (初始 unknown)。
var permissionStates = map[string]string{}

// normalizePermissionKind 归一化并校验权限种类 (返回 ok=false 表示不认识)。
func normalizePermissionKind(k string) (string, bool) {
	k = strings.ToLower(strings.TrimSpace(k))
	// 常见别名归一: 让应用少踩"photos / photo / image 到底该写哪个"的坑。
	switch k {
	case "photos", "photo", "images", "image", "album", "media":
		k = "gallery"
	case "mic", "record":
		k = "microphone"
	case "gps":
		k = "location"
	case "notifications":
		k = "notification"
	}
	for _, known := range permissionKindList {
		if k == known {
			return k, true
		}
	}
	return k, false
}

// normalizePermissionState 归一化状态词 (不认识 → unknown, 不猜)。
func normalizePermissionState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case PermGranted, "authorized", "allow", "allowed", "true":
		return PermGranted
	case PermDenied, "deny", "rejected", "false":
		return PermDenied
	case PermNotDetermined, "undetermined", "prompt", "unasked":
		return PermNotDetermined
	case PermRestricted:
		return PermRestricted
	case PermLimited, "partial":
		return PermLimited
	default:
		return PermUnknown
	}
}

// PermissionState 读某个权限的缓存状态 (Go 侧)。
func PermissionState(kind string) string {
	k, ok := normalizePermissionKind(kind)
	if !ok {
		return PermUnknown
	}
	nativeMu.Lock()
	defer nativeMu.Unlock()
	if s, ok := permissionStates[k]; ok {
		return s
	}
	return PermUnknown
}

// ReportPermission 由宿主上报某个权限的状态 (GUI 线程)。
//
// 两个典型时机: 应用启动时批量报一遍 (getSetting), 以及**用户从设置页回来**
// (Android onResume / iOS didBecomeActive) —— 后者是最容易漏的: 用户在设置里把
// 相机权限打开了, 应用如果不知道, 界面上那个"去授权"的按钮就永远挂着。
func ReportPermission(kind, state string) {
	k, ok := normalizePermissionKind(kind)
	if !ok {
		return
	}
	s := normalizePermissionState(state)
	nativeMu.Lock()
	prev := permissionStates[k]
	if permissionStates == nil {
		permissionStates = map[string]string{}
	}
	permissionStates[k] = s
	nativeMu.Unlock()
	// 状态没变就不出声 (宿主在每次 onResume 全量重报是常见写法)。
	if prev != s {
		notifyNativeChanged(evPermission)
	}
}

// ReportPermissions 批量上报 (宿主一次性同步全部权限时用)。
func ReportPermissions(states map[string]string) {
	for k, v := range states {
		ReportPermission(k, v)
	}
}

// permissionSnapshot 取全部权限状态的副本 (Go 侧)。
func permissionSnapshot() map[string]string {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	out := make(map[string]string, len(permissionKindList))
	for _, k := range permissionKindList {
		if s, ok := permissionStates[k]; ok {
			out[k] = s
		} else {
			out[k] = PermUnknown
		}
	}
	return out
}

// permissionSnapshotToJS 把快照转成脚本对象。
func permissionSnapshotToJS() object.Value {
	o := object.NewObject()
	for k, v := range permissionSnapshot() {
		o.SetProperty(k, object.NewString(v))
	}
	return o
}

// refreshPermissionsFromHost 向宿主问一次全量状态并合并进缓存 (没有宿主则原样)。
//
// `permission.get` 是**同步**方法: Android 的 checkSelfPermission / iOS 的
// 各 AVAuthorizationStatus 都是同步返回的内存读, 走 Promise 只会给调用点添麻烦。
func refreshPermissionsFromHost() {
	host, ok := nativeHostLocked(nmPermGet)
	if host == nil || !ok {
		return
	}
	res := host.Call(nmPermGet, nativeArgsWithID(nil, ""))
	o, ok := res.Result.(*object.Object)
	if !ok {
		return
	}
	states := map[string]string{}
	for _, k := range permissionKindList {
		if v, ok := o.GetProperty(k); ok {
			states[k] = valueText(v)
		}
	}
	ReportPermissions(states)
}

// jsGetSetting 是 `getSetting()` → {location, camera, gallery, ...}。
//
// 先问宿主再返回缓存: 同一次调用里既刷新又给值, 应用不需要"先刷新再读"。没有
// 宿主时返回全 unknown 的稳定形状 (不是错误 —— 桌面本来就没有运行时权限)。
func jsGetSetting(args ...object.Value) object.Value {
	refreshPermissionsFromHost()
	return permissionSnapshotToJS()
}

// jsPermissionState 是 `permissionState(kind)` → 状态字符串 (只读缓存)。
func jsPermissionState(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewString(PermUnknown)
	}
	return object.NewString(PermissionState(valueText(args[0])))
}

// jsCheckPermission 是 `checkPermission(kind)` → 布尔 (只读缓存)。
//
// 只认 granted 为真: `limited` 刻意**不算**通过 —— iOS 的"仅选中的照片"模式下
// 读全量相册会失败, 把它当 true 会让应用在错误的前提上继续跑。要判 limited 的
// 场合用 permissionState()。
func jsCheckPermission(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewBoolean(false)
	}
	return object.NewBoolean(PermissionState(valueText(args[0])) == PermGranted)
}

// jsAuthorize 是 `authorize(kind, callback?)` → Promise<状态字符串>。
//
// 异步, 因为它会**弹系统弹窗** (与 gx/dialog 的 confirm 同族)。返回申请之后的
// 状态而不是布尔: 调用点常要区分"被拒"与"永久禁止"(后者只能引导去设置页),
// 布尔把这个信息丢了。
func jsAuthorize(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("authorize: 需要权限种类 (如 \"camera\")")
	}
	kind, ok := normalizePermissionKind(valueText(args[0]))
	if !ok {
		return object.NewTypeError("authorize: 不认识的权限 %q (支持: %s)",
			valueText(args[0]), strings.Join(permissionKindList, ", "))
	}
	opts := object.NewObject()
	opts.SetProperty("kind", object.NewString(kind))
	cb := nativeOptFunc(args, 1)
	// 结果可能是字符串, 也可能是 {state} —— 统一在回调里归一化后写缓存。
	return callNativeWithSink(nmPermRequest, opts, cb, func(result object.Value) (object.Value, object.Value) {
		state := normalizePermissionState(extractPermissionState(result, kind))
		ReportPermission(kind, state)
		return object.NewString(state), nil
	})
}

// jsRequestPermissions 是 `requestPermissions(kinds, callback?)` → Promise<{kind: state}>。
//
// 一次申请多个: 系统会连着弹若干次, 宿主实现里逐个串行申请**再**回填一次总结果
// (让应用只等一次 —— 否则调用点要写一串 await 且无法知道整体何时结束)。
func jsRequestPermissions(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("requestPermissions: 需要权限种类数组")
	}
	var kinds []string
	switch x := args[0].(type) {
	case *object.Array:
		for _, el := range x.Elements {
			if k, ok := normalizePermissionKind(valueText(el)); ok {
				kinds = append(kinds, k)
			}
		}
	default:
		if k, ok := normalizePermissionKind(valueText(args[0])); ok {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return object.NewTypeError("requestPermissions: 没有可识别的权限种类 (支持: %s)",
			strings.Join(permissionKindList, ", "))
	}
	opts := object.NewObject()
	arr := make([]object.Value, 0, len(kinds))
	for _, k := range kinds {
		arr = append(arr, object.NewString(k))
	}
	opts.SetProperty("kinds", object.NewArray(arr))
	opts.SetProperty("kind", object.NewString(kinds[0]))
	cb := nativeOptFunc(args, 1)
	return callNativeWithSink(nmPermRequest, opts, cb, func(result object.Value) (object.Value, object.Value) {
		states := map[string]string{}
		if o, ok := result.(*object.Object); ok {
			for _, k := range kinds {
				states[k] = normalizePermissionState(extractPermissionState(o, k))
			}
		} else {
			for _, k := range kinds {
				states[k] = PermUnknown
			}
		}
		ReportPermissions(states)
		out := object.NewObject()
		for _, k := range kinds {
			out.SetProperty(k, object.NewString(states[k]))
		}
		return out, nil
	})
}

// extractPermissionState 从宿主结果里挖出某个权限的状态。
//
// 接受四种形状 (宿主写起来最省事的那种就行):
//
//	"granted"                   单权限申请的直接返回
//	{state: "granted"}          单权限的结构化返回
//	{kind: "granted"}           用 kind 当键 (宿主容易这么写)
//	{camera: "granted", ...}    批量返回里的那一项
func extractPermissionState(v object.Value, kind string) string {
	switch x := v.(type) {
	case *object.String:
		return x.Value
	case *object.Object:
		for _, key := range []string{"state", kind, "result"} {
			if s, ok := x.GetProperty(key); ok {
				if str, ok := s.(*object.String); ok && str.Value != "" {
					return str.Value
				}
			}
		}
	}
	return PermUnknown
}

// callNativeWithSink 是 callNative 的"结果需要先加工"版本。
//
// 为什么单独一个函数: 权限族的方法返回的是**平台的**状态词 ("authorized" /
// "allow"), 必须先归一化 + 写缓存才能交给脚本。把这段逻辑塞进每个调用点会导致
// 归一口径分散, 而它是那种"错了不会崩、只会静默给出错的权限状态"的代码。
//
// cb 非空时仍然尊重双模: 加工后的值通过 cb(err, 加工后的值) 给出去。
//
// transform 返回 (加工后的值, 错误)。错误非空时整体按失败处理 (reject / 走
// 回调的 err 位) —— 加工阶段发现"宿主给的形状不对"这类问题在这里报, 而不是
// 把坏值静默交给应用。
func callNativeWithSink(method string, opts *object.Object, cb object.Value,
	transform func(object.Value) (object.Value, object.Value)) object.Value {

	outer := object.NewPromise()
	inner := object.NewBuiltin("__native_sink", func(args ...object.Value) object.Value {
		var errVal object.Value = object.NullSingleton
		if len(args) > 0 {
			errVal = args[0]
		}
		var result object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			result = args[1]
		}
		if errVal != object.NullSingleton && errVal != object.UndefinedSingleton {
			if cb != nil {
				object.CallFunction(cb, nil, errVal, object.UndefinedSingleton)
			} else {
				outer.Reject(errVal)
			}
			return object.UndefinedSingleton
		}
		// 宿主可能给 null 表示"问不到" —— 按 unknown 处理, 而不是把 null 交给应用。
		if result == object.NullSingleton {
			result = object.NewObject()
		}
		conv, convErr := transform(result)
		if convErr != nil {
			if cb != nil {
				object.CallFunction(cb, nil, convErr, object.UndefinedSingleton)
			} else {
				outer.Reject(convErr)
			}
			return object.UndefinedSingleton
		}
		if cb != nil {
			object.CallFunction(cb, nil, object.NullSingleton, conv)
		} else {
			outer.Resolve(conv)
		}
		return object.UndefinedSingleton
	})
	// 注意: 这里**不能**把 inner 直接交给 callNative 当"用户回调"再包一层 ——
	// callNative 在能力缺失时会同步调回调, 而 inner 里的 transform 会跑在脚本
	// 还在执行的时候 (Promise 语义被破坏)。所以缺能力的情况自己先判掉。
	if !CanIUse(method) {
		reason := nativeErr(ErrUnsupported,
			"当前平台不支持 %s (canIUse(\"%s\") 为 false)", method, nativeMethodCapability(method)).toJS()
		if cb != nil {
			object.CallFunction(cb, nil, reason, object.UndefinedSingleton)
		} else {
			deferNativeReject(outer, reason)
		}
		return chooseValue(cb, outer)
	}
	callNative(method, opts, inner)
	return chooseValue(cb, outer)
}

// chooseValue 双模返回: 有回调 → undefined, 否则 → Promise。
func chooseValue(cb object.Value, p *object.Promise) object.Value {
	if cb != nil {
		return object.UndefinedSingleton
	}
	return p
}

// deferNativeReject 把一次 reject 投回事件循环 (保持 Promise 是微任务的语义)。
func deferNativeReject(p *object.Promise, reason object.Value) {
	resolveDeferred(func() { p.Reject(reason) })
}

// jsOpenAppSettings 打开本应用的系统设置页 (`openAppSettings()`)。
//
// 复用 `device.openSettings` 而不是新增一个宿主方法: 能力 ID 是 device, 但语义
// 上应用代码更愿意从 permission 模块里找它 (用户被拒权限后要引导去的地方)。
func jsOpenAppSettings(args ...object.Value) object.Value {
	return jsOpenSystemSettings(object.NewString("app"))
}

// jsReportPermission 是宿主 / 模拟器 / 测试的上报口。
func jsReportPermission(args ...object.Value) object.Value {
	if len(args) < 2 {
		return object.NewTypeError("reportPermission: 需要 (kind, state)")
	}
	kind, ok := normalizePermissionKind(valueText(args[0]))
	if !ok {
		return object.NewTypeError("reportPermission: 不认识的权限 %q", valueText(args[0]))
	}
	ReportPermission(kind, valueText(args[1]))
	return object.UndefinedSingleton
}

// resetPermissionStateForTest 复位权限缓存。
func resetPermissionStateForTest() {
	nativeMu.Lock()
	permissionStates = map[string]string{}
	nativeMu.Unlock()
}

func init() {
	object.RegisterBuiltinModule("gx/permission", func() map[string]object.Value {
		return map[string]object.Value{
			"getSetting":         scr("getSetting", jsGetSetting),
			"permissionState":    scr("permissionState", jsPermissionState),
			"checkPermission":    scr("checkPermission", jsCheckPermission),
			"authorize":          scr("authorize", jsAuthorize),
			"requestPermissions": scr("requestPermissions", jsRequestPermissions),
			"openAppSettings":    scr("openAppSettings", jsOpenAppSettings),

			"onPermissionChange":  scr("onPermissionChange", nativeHookAPI(evPermission, "onPermissionChange", false)),
			"offPermissionChange": scr("offPermissionChange", nativeHookAPI(evPermission, "onPermissionChange", true)),

			"reportPermission": scr("reportPermission", jsReportPermission),
			"permissionKinds": scr("permissionKinds", func(args ...object.Value) object.Value {
				out := make([]object.Value, 0, len(permissionKindList))
				for _, k := range permissionKindList {
					out = append(out, object.NewString(k))
				}
				return object.NewArray(out)
			}),
		}
	})
}
