// Package hostbridge 是移动端三端 (android / ios / harmony) **共用**的宿主协议层。
//
// ## 由来: 三份逐字相同的协议实现
//
// 三端的 libgox 装配层各自实现了 gfx.NativeHost, 而它们的协议部分曾经是三份
// 逐字相同的代码 (pendingMarker / parseHostReply / sanitizeErrCode / objToJSON /
// writeJSON / numToJSON / quoteJSON / jsonToObj, 约 180 行 ×3)。harmony 的实现里
// 留过一句交代:
//
//	"与 Android 的实现是同一份逻辑, 但刻意各留一份 … 真要合并, 前提是先有
//	 第三处使用点。"
//
// 现在条件成立了 —— iOS 是第三处, 而「让三端都能跑 gfx/hosttest 契约套件」是
// 第四处需求 (见下面「为什么必须有这一层」)。本包把这些代码收成一份。
//
// ## 为什么必须有这一层 (不只是"少写代码")
//
// 三端的 NativeHost 实现都住在 `package main` 里 (gfx/*/libgox/main.go)。
// package main **不可被 import**, 于是契约套件 gfx/hosttest 根本喂不进三端宿主 ——
// 这就是「宿主契约只能靠真机试错」的结构性原因, 而不是因为懒得写测试。
//
// 本包把宿主切成两半, 沿用 gfx/mobile/fold.go 已经验证过的分工:
//
//	平台相关那一半  = 一次跨语言调用 (JNI / C 函数指针 / NAPI), 退化成两个闭包
//	平台无关那一半  = 协议解析 + 能力串切分 + JSON 窄转换 ← 本包, 可桌面单测
//
// 于是三端宿主变成 `hostbridge.New(caps, call, 文案)` 一行装配, 而协议语义可以
// 在开发机上被 gfx/hosttest 真跑一遍 (见 contract_test.go)。
//
// ## 一个如实记录的语义取舍
//
// 三端原来的 Call 在「通道级故障」上分了两个错误码: 宿主未注册 → ErrUnsupported,
// 而 JNI 附加失败 / 跨语言层抛异常 → ErrPlatform。本包把它们统一成一条 error
// 通道, **一律以 ErrUnsupported + 平台文案应答**。
//
// 理由: 脚本侧看到的效果一致 (这个平台上这次调用走不通), 而且 canIUse 已经声明
// 支持了却调不通, 报 unsupported 比报 platform-error 更贴切 —— platform-error
// 的语义是「平台给了个说不清的错」, 而这里是明确的「没走到宿主」。
//
// 代价: 丢掉了上述两个错误码的区分。做出这个取舍前已确认 gfx 全仓测试对
// ErrPlatform **零命中** (没有断言钉住它), 改写不破坏既有契约; 若将来需要区分,
// 正确的做法是让 CallFn 返回一个携带错误码的错误类型, 而不是把三份实现再拆回去。
//
// ## 不在这个包里的东西
//
//   - 任何 cgo / 平台 SDK 依赖: 本包无 build tag, 纯 Go, 桌面 `go test` 直接跑。
//   - 平台特有的解析助手 (android 的 goJSONObj / harmony 的 cstrObj / ios 的
//     jsonStr 等): 它们读的是各自的跨语言值类型, 留在各自的 libgox 里。
package hostbridge

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
)

// PendingMarker 是宿主侧"稍后回填"的哨兵 (三端协议同一个字面量)。
//
// 宿主返回这个字符串表示「调用已受理, 结果稍后由 ResolveNative 回填」。
// 它是协议的一部分, 三端的 Kotlin / Swift / ArkTS 侧都硬编码了同一个值 ——
// 改这个值必须三端 + 本包四处同步。
const PendingMarker = "__pending__"

// CapsFn 取宿主声明的能力串 (逗号分隔的方法名/能力名)。
//
// err != nil 表示「宿主还没绑上」(典型: init 早期内核还没开始问能力), 这不是
// 错误 —— Host.Capabilities 静默返回空切片, 与三端原实现一致。
type CapsFn func() (string, error)

// CallFn 发起一次跨语言调用, 返回宿主的**原始回包字符串**。
//
// err != nil 表示这次调用没走到宿主 (通道不可用 / 宿主未绑定 / 跨语言层抛错),
// Host.Call 一律以 ErrUnsupported + 平台文案应答 (取舍见包注释)。
type CallFn func(method, argsJSON string) (string, error)

// Host 是把两件跨语言动作注入后即可工作的 gfx.NativeHost 实现。
//
// 三端的宿主本体现在只是它的一行装配: 平台相关的部分全在 caps/call 两个闭包里,
// 协议语义全在本包 —— 于是 gfx/hosttest 能在桌面对它跑全套契约断言。
type Host struct {
	caps CapsFn
	call CallFn
	// unsupportedFmt 是「没走到宿主」时的失败文案, 带一个 %s 占位 (方法名)。
	unsupportedFmt string
}

// New 装配一个桥接宿主。
//
// unsupportedFmt 必须含且仅含一个 %s (方法名), 例:
// "Android 宿主未注册 nativeCall (GoxNativeHost 缺失?), 方法 %s 不可用"。
func New(caps CapsFn, call CallFn, unsupportedFmt string) *Host {
	return &Host{caps: caps, call: call, unsupportedFmt: unsupportedFmt}
}

// Capabilities 报告宿主声明的方法名/能力名 (gfx.NativeHost 接口)。
func (h *Host) Capabilities() []string {
	s, err := h.caps()
	if err != nil {
		return []string{}
	}
	return SplitCapabilities(s)
}

// Call 执行一个原生方法 (gfx.NativeHost 接口): 参数序列化成 JSON 传给宿主,
// 回包按协议三态解析。
//
// 同步路径 (device.info / permission.get …) 会阻塞脚本线程直到宿主返回 —— 这正是
// 内核要求的: 这几个方法只有同步返回值才会被 deviceHostOverlay /
// jsGetBrightness / jsOpenSystemSettings / refreshPermissionsFromHost 采用
// (gfx/native_device.go 与 gfx/native_permission.go 都直接读 res.Result)。
// 三端宿主的同步实现都是纯内存/系统读 (碰 UI 的用主线程同步派发), 不会卡住。
func (h *Host) Call(method string, args object.Value) gfx.NativeCallResult {
	reply, err := h.call(method, ObjToJSON(args))
	if err != nil {
		return gfx.NativeFailure(gfx.ErrUnsupported, h.unsupportedFmt, method)
	}
	return ParseReply(reply)
}

// SplitCapabilities 把宿主给的逗号分隔能力串切成声明表 (去空白、丢空项)。
//
// 三端原实现逐字相同; 收在这里是因为「去不去空白」直接影响契约: 内核注册时
// (SetNativeHost) 会 Trim + 丢空, 若声明表自己不清干净, 声明与内核快照就对不上
// (gfx/hosttest 的断言 1 抓的正是这个)。
func SplitCapabilities(s string) []string {
	out := []string{}
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseReply 按宿主回包协议三态解析 (pending / 错误信封 / 结果)。
func ParseReply(s string) gfx.NativeCallResult {
	s = strings.TrimSpace(s)
	if s == PendingMarker {
		return gfx.NativePending()
	}
	var envelope struct {
		ErrCode string `json:"__errCode"`
		ErrMsg  string `json:"__errMsg"`
	}
	if err := json.Unmarshal([]byte(s), &envelope); err == nil && envelope.ErrCode != "" {
		return gfx.NativeFailure(SanitizeErrCode(envelope.ErrCode), "%s", envelope.ErrMsg)
	}
	v, err := JSONToObj(s)
	if err != nil {
		return gfx.NativeFailure(gfx.ErrPlatform, "宿主返回了无法解析的 JSON: %v", err)
	}
	return gfx.NativeResult(v)
}

// SanitizeErrCode 把宿主给的错误码夹回 8 个统一错误码 —— 错误码是跨平台契约,
// 多出来的词一律归 platform-error, 不让平台私造词汇穿透到脚本侧。
func SanitizeErrCode(code string) string {
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
// 这一段三端也曾经各留一份。它是「窄转换」而不是通用 JSON 库: 只支持能过桥的
// 那几种值, 函数/undefined 按 JSON.stringify 的语义跳过。

// ObjToJSON 把内核给的参数对象序列化成 JSON 文本 (给宿主侧的 JSON 解析器)。
// 函数/闭包等不可序列化的值跳过或写成 null。
func ObjToJSON(v object.Value) string {
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

// JSONToObj 把宿主回包的 JSON 解析成 object.Value (UseNumber 保数字精度)。
func JSONToObj(s string) (object.Value, error) {
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
