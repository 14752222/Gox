package gfx

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/14752222/Gox/object"
)

// 未注册属性 / 未实现事件的诊断（看板 rgQsDD）。
//
// ## 为什么需要它
//
// wireProp 的兜底是 `n.Props[name] = val` —— 任何键名都会静默落进 map。按 DOM
// 直觉写 `<button label="确定">` 得到的是 16px 宽的空白壳（button 的内容走子
// 节点），写了内核还没实现的事件则回调永不触发；两种情形控制台都一声不响。
// 用户会以为是自己脚本写错了 —— **比报错更伤采纳**。
//
// ## 分级
//
//	默认（生产）    一声不响 —— 生产构建不该为内部细节刷用户的 stderr
//	dev 模式        warning（GOX_DEV=1 或 `gox dev`，与 devmode.go 同一开关）
//	strictAPI=true  error —— h() 直接抛，即"写错就别跑起来"
//
// 先出 warning 再出 error 是刻意的：一上来就 error，白名单哪怕只有一处没抽准，
// 用户第一反应就是把整个开关关掉（而且再也不会打开）。warning 阶段的任务就是
// 把白名单喂准。
//
// ## 白名单的真源
//
// knownprops_gen.go 由 scripts/gen-props-golden.py 从 gfx/*.go 的**读取点**
// 反推（tools/propdump，go/ast + 调用图），不在本文件里手抄 —— 手抄必然错，
// 抽错就误报，而误报是这个开关的头号死因。

// ── strictAPI 开关 ──────────────────────────────────────────────────────────

// strictAPI 是 gox.json 的 strictAPI 字段在库里的镜像。
//
// 与 devMode 同一条纪律（见 devmode.go）：只用显式 setter 改，不在包 init()
// 里读环境/读文件 —— 库保持"被调用才动"的惰性，测试也能直接开关。
// 用 atomic 而非普通 bool：测试会并发断言诊断出口。
var strictAPI atomic.Bool

// SetStrictAPI 开关严格模式。由 cmd/gox（读 gox.json 的 strictAPI）与测试调用。
func SetStrictAPI(on bool) { strictAPI.Store(on) }

// StrictAPI 报告是否处于严格模式。
func StrictAPI() bool { return strictAPI.Load() }

// ── 诊断 ────────────────────────────────────────────────────────────────────

// propDiagnostic 报告脚本在 tag 上写的这个键名是不是"内核认得的"。
// 返回空串表示没问题；非空是要给用户看的说明。
//
// 判据的次序是有讲究的：
//  1. 先放掉**不该由这一层管**的键（指令键 / aria-* / 内核内部键）—— 脚本写
//     它们是正确用法，报了就是纯粹的噪音；
//  2. 事件单独一档：它的"未知"有两种完全不同的成因（拼错 vs 内核还没写），
//     文案必须分开，否则用户会去查自己脚本的拼写，查一天也查不出来；
//  3. 剩下才是普通属性：先查白名单，再查"有明确替代写法"的提示表。
func propDiagnostic(tag, name string) string {
	// 两个开关都关 = 一声不响。放在最前面：这是热路径（h() 每次重建都跑），
	// 生产口径下不该为诊断付任何代价。
	if !DevMode() && !StrictAPI() {
		return ""
	}
	if _, fw := frameworkProps[name]; fw {
		return ""
	}
	for _, pre := range propPrefixExempt {
		if strings.HasPrefix(name, pre) {
			return ""
		}
	}
	if isEventPropName(name) {
		return eventDiagnostic(tag, name)
	}
	if knownProp(tag, name) {
		return ""
	}
	// 有明确替代写法的常见误解：文案要给"那我该怎么写"，只说"未知属性"等于没说
	if hint, ok := propHints["*|"+name]; ok {
		return hint
	}
	if hint, ok := propHints[tag+"|"+name]; ok {
		return hint
	}
	return "内核不读这个属性（写了不会报错，也不会有任何效果）；" +
		tag + " 认得的全部属性见 docs/props.golden.json"
}

// eventDiagnostic 是事件那一档的诊断。
func eventDiagnostic(tag, name string) string {
	if _, ok := eventExempt[name]; ok {
		return ""
	}
	if knownEvent(tag, name) {
		return ""
	}
	if why, ok := eventsNotImplemented[name]; ok {
		// **必须明说"内核还没实现"**：把它混进"未知属性"会让人去查脚本的拼写，
		// 而问题根本不在脚本这一侧。
		return "内核还没有实现这个事件（回调永远不会被调用）—— " + why
	}
	return "内核没有这个事件的派发点（回调永远不会被调用）；" +
		tag + " 能收到的事件见 docs/props.golden.json"
}

// knownProp 报告内核是否在 tag 上读这个属性。
func knownProp(tag, name string) bool {
	if _, ok := knownPropsUniversal[name]; ok {
		return true
	}
	if byTag, ok := knownPropsByTag[tag]; ok {
		if _, ok := byTag[name]; ok {
			return true
		}
	}
	// 未知标签（拼错的 / 用户自定义的）：它的属性无从判断，交给 warnUnknownTag。
	// 否则 `<button>` 拼成 `<buton>` 会同时收到"未知标签"和一堆"未知属性"。
	if _, known := knownTags[tag]; !known {
		return true
	}
	return false
}

// knownEvent 报告内核是否给 tag 派发这个事件。与 knownProp 同一套口径。
func knownEvent(tag, name string) bool {
	if _, ok := knownEventsUniversal[name]; ok {
		return true
	}
	if byTag, ok := knownEventsByTag[tag]; ok {
		if _, ok := byTag[name]; ok {
			return true
		}
	}
	if _, known := knownTags[tag]; !known {
		return true
	}
	return false
}

// ── 出口（去重 + 可替换，与 warnUnknownTag 同一条纪律）──────────────────────

var (
	propWarnMu   sync.Mutex
	propWarnSeen = map[string]struct{}{}
)

// warnUnknownPropOut 是"未注册属性/未实现事件"的警告出口。做成变量而非直接
// 写 stderr，便于单测替换为计数器断言。
var warnUnknownPropOut = func(tag, name, msg string) {
	recordWarn("<%s> 上的 %s：%s", tag, name, msg)
}

// propWarnOnce 同一个 (标签, 键名) 只警告一次：h() 在每次重建时都会跑（列表
// 行的渲染函数、响应式分支换代），不去重会在热路径上刷屏 —— 与
// warnUnknownTagOnce 同一条纪律。
func propWarnOnce(tag, name, msg string) {
	key := tag + "|" + name
	propWarnMu.Lock()
	_, seen := propWarnSeen[key]
	if !seen {
		propWarnSeen[key] = struct{}{}
	}
	propWarnMu.Unlock()
	if seen {
		return
	}
	warnUnknownPropOut(tag, name, msg)
}

// resetPropWarns 清空去重表（仅测试用：警告环是跨用例共享的进程级状态）。
func resetPropWarns() {
	propWarnMu.Lock()
	propWarnSeen = map[string]struct{}{}
	propWarnMu.Unlock()
}

// propError 是 strictAPI 下的出口：不是警告，是真的报错（h() 会把它抛给脚本）。
//
// 文案与 warning 那一档共用 propDiagnostic：同一个问题，两种严重程度，用户
// 不该看到两副解释。
func propError(tag, name, msg string) object.Value {
	return object.NewTypeError("strictAPI: <%s> 上的 %s：%s", tag, name, msg)
}
