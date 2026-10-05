//go:build js && wasm

// Command goxwasm 是 Gox 运行时面向浏览器的**最小** wasm 入口（可行性探针）。
//
// 目标：验证「纯 Go 字节码 VM 能否编成 wasm、体积多大、浏览器里还缺什么」，
// 而不是交付 Playground 功能。因此刻意只依赖编译/执行管线的必需包：
//
//	lexer / parser / ast / compiler / bytecode / vm / object / runtime / stdlib
//
// 明确**不引入** gfx（浏览器无原生窗口后端）、cmd/gox 的宿主代码、
// packager / scaffold / update 等平台相关包。
//
// 对外暴露两个 JS 全局符号（挂在 globalThis 上）：
//
//	goxReady: boolean                 运行时已就绪标记
//	goxRunSource(src) -> Promise      编译并执行一段 JS，resolve 出结果字符串
//
// ⚠️ 必须是「异步（返回 Promise）」而非同步返回字符串：os.Stdout 在 js/wasm
// 上走 syscall.fsCall（异步事件），若在 JS→Go 的同步回调里阻塞等待，调度器会
// 判定 "all goroutines are asleep - deadlock" 崩掉（console.log 必触发）。
// 走 goroutine + Promise 后 JS 侧 await 让出事件循环，fsCall 才能被处理。
//
// 用法（Node）：
//
//	const go = new Go()
//	const mod = await WebAssembly.instantiate(wasmBytes, go.importObject)
//	go.run(mod.instance)                 // 会一直阻塞在 select{}
//	const out = await globalThis.goxRunSource("[1,2,3].map(x => x*2)")
package main

import (
	"strings"
	"syscall/js"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/stdlib"
	"github.com/14752222/Gox/vm"
)

// evalSource 编译并执行一段 JS，返回结果字符串（对齐 REPL 的呈现）。
// 每次调用用一个全新的全局环境（REPL 的 :clear 语义），避免状态串味。
func evalSource(src string) string {
	globals := stdlib.SetupGlobals()
	result, err := vm.EvalWithGlobals(src, globals)
	if err != nil {
		// 去掉内部前缀，对齐浏览器报错呈现（与 REPL 一致）。
		return "error: " + strings.TrimPrefix(err.Error(), "vm error: ")
	}
	if result == nil {
		return "undefined"
	}
	if _, isUndef := result.(*object.Undefined); isUndef {
		return "undefined"
	}
	return result.Inspect()
}

// runSource 对应 JS 侧的 goxRunSource(src)，返回 Promise<string>。
func runSource(_ js.Value, args []js.Value) any {
	var handler js.Func
	handler = js.FuncOf(func(_ js.Value, pargs []js.Value) any {
		resolve := pargs[0]
		reject := pargs[1]

		if len(args) < 1 {
			reject.Invoke("goxRunSource(src) 需要一个源码字符串参数")
			return nil
		}
		src := args[0].String()

		// 放到独立 goroutine 里跑，让调用方 JS 能 await 并让出事件循环，
		// 从而处理 os.Stdout 写入等 fsCall 异步事件。
		go func() {
			// Promise 结算后释放 executor 回调，避免 js.FuncOf 逐次调用泄漏。
			defer handler.Release()
			defer func() {
				if r := recover(); r != nil {
					reject.Invoke(r)
				}
			}()
			resolve.Invoke(evalSource(src))
		}()
		return nil
	})
	return js.Global().Get("Promise").New(handler)
}

func main() {
	js.Global().Set("goxRunSource", js.FuncOf(runSource))
	js.Global().Set("goxReady", true)

	// 保持 wasm 实例存活，供宿主反复调用 —— 与 Go 官方 wasm_exec 的惯用法一致。
	select {}
}
