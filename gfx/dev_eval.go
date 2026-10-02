package gfx

import (
	"errors"
	"sync"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/stdlib"
	"github.com/14752222/Gox/vm"
)

// gx/dev 的 REPL 求值 (devtools [M3] v1, 2026-10-02 落地)。
//
//	import { devEval } from "gx/dev";
//	const r = devEval("let a = 1");  // { ok: true, value: "1", error: "" }
//	const s = devEval("a + 1");      // { ok: true, value: "2", error: "" }  ← 跨调用保持状态
//	const e = devEval("1 +");        // { ok: false, value: "", error: "vm error: ..." }
//
// ## 环境从哪来 (接线点, 也是本文件的边界)
//
// 理想语义是"在**当前运行的 app VM 环境**里求值"。但 gfx 侧**拿不到**那个环境:
// 应用 VM 的 *runtime.Environment 只存在于宿主入口 (cmd/gox 的 runFile/devRun)
// 的局部变量里, gfx/render.go 也没有包级引用 (本次审计确认, 且 render.go
// 属于并行会话的所有权, 不能改)。所以本能力按"能力 + 接线点"落地:
//
//	宿主在挂载脚本之后、进事件循环之前调用一次:
//	    gfx.SetDevEnv(vmInst.Globals())
//
// 接线后 devEval 就在真实 app 环境里求值, REPL 里 `let a=1` 与 app 自己的
// 全局共享同一名字空间 (这正是 REPL 该有的语义)。
//
// ## 没接线时的退化
//
// 若宿主没接线, 第一次 devEval 会**惰性建一个独立的 REPL 沙盒**
// (stdlib.SetupGlobals), 让面板在纯演示/单测里也能用 —— 代价是它的全局与
// app 隔离 (读不到 app 自己的变量)。这是刻意的取舍: "REPL 能跑但看不到 app
// 全局"比"面板里输入什么都是 not attached 错误"更有用; 文档里写明确。
//
// ## 为什么 gfx 可以 import vm
//
// 需要 `vm.EvalWithGlobals(code, env)`: 它把源码按**顶层程序**编译执行
// (top-level), 只有这样 `let a=1` 才会在 env 里留下**全局**绑定, 下次调用
// 才看得到 (REPL 的关键)。对象包装 (function(){...}) 会把 let 变成局部,
// 状态留不下来 —— 所以不能走 object.CompileSource 那条桥。
//
// 依赖方向核对: vm 不 import gfx (也不 import 任何 gfx 子包), 所以
// gfx → vm 无环; vm 已依赖 stdlib, gfx 直接 import stdlib 同样无环。
// 代价是链接 gfx 的宿主会一并带上 VM —— GUI 宿主本来就跑 VM, 可接受。

var devEvalState struct {
	mu  sync.Mutex
	env *runtime.Environment
}

// SetDevEnv 接线"REPL 求值所用的运行时环境"。
//
// 接线点 (宿主入口, gfx 不能改 render.go / main.go):
//
//	v, err := vm.EvalFileVM(path)      // 或 vm.EvalVM(src)
//	gfx.SetDevEnv(v.Globals())         // ← 挂载后、RunTimersWithPump 之前
//	_ = v.RunTimersWithPump(gfx.Pump)
//
// 传 nil 表示"取消接线", 下次 devEval 重新落回惰性沙盒。
func SetDevEnv(env *runtime.Environment) {
	devEvalState.mu.Lock()
	devEvalState.env = env
	devEvalState.mu.Unlock()
}

// devEnv 取当前求值环境: 优先用宿主接线的 app 环境, 否则惰性建 REPL 沙盒。
func devEnv() *runtime.Environment {
	devEvalState.mu.Lock()
	defer devEvalState.mu.Unlock()
	if devEvalState.env == nil {
		devEvalState.env = stdlib.SetupGlobals()
	}
	return devEvalState.env
}

// DevEval 在当前 dev 环境里求值并返回结果 (Go 侧入口, 单测直接调它注入语义)。
//
// 返回值是最后一个顶层表达式的值 (EvalWithGlobals 的口径); 求值/编译错误
// 原样返回 (调用方决定怎么呈现)。
func DevEval(code string) (object.Value, error) {
	env := devEnv()
	if env == nil {
		return nil, errors.New("gfx: dev eval environment unavailable")
	}
	return vm.EvalWithGlobals(code, env)
}

// jsDevEval 是 devEval(code) 的实现: 把 Go 侧结果包成 { ok, value, error }。
//
// value 是结果值的 **Inspect 文本**而不是活值: 面板要的是"能显示的一行",
// 而且把一个可能带环/带句柄的对象塞回 JS 反而会让面板自己撑爆 (与 devTree
// 的序列化口径一致)。undefined 也照常呈现为 "undefined"。
func jsDevEval(args ...object.Value) object.Value {
	r := object.NewObject()
	if len(args) == 0 {
		r.SetProperty("ok", object.NewBoolean(false))
		r.SetProperty("value", object.NewString(""))
		r.SetProperty("error", object.NewString("devEval: code required"))
		return r
	}
	src, ok := args[0].(*object.String)
	if !ok {
		r.SetProperty("ok", object.NewBoolean(false))
		r.SetProperty("value", object.NewString(""))
		r.SetProperty("error", object.NewString("devEval: code must be a string"))
		return r
	}

	val, err := DevEval(src.Value)
	if err != nil {
		r.SetProperty("ok", object.NewBoolean(false))
		r.SetProperty("value", object.NewString(""))
		r.SetProperty("error", object.NewString(err.Error()))
		return r
	}
	r.SetProperty("ok", object.NewBoolean(true))
	r.SetProperty("value", object.NewString(devSafeInspect(val)))
	r.SetProperty("error", object.NewString(""))
	return r
}
