// run_node.js —— 跑通 gox.wasm 的最小 Node 宿主（不引入任何 npm 依赖）。
//
// 它做三件事：
//   1. 从当前 GOROOT 的 lib/wasm/wasm_exec.js 加载 Go 官方 JS 胶水（不 vendoring）；
//   2. 实例化 gox.wasm 并 go.run（main 阻塞在 select{}，实例常驻）；
//   3. 调 globalThis.goxRunSource(src)（返回 Promise）跑一组 JS 求值用例并打印。
//
// 用法:
//   GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o /tmp/gox.wasm ./cmd/goxwasm
//   node cmd/goxwasm/run_node.js /tmp/gox.wasm
"use strict";

const fs = require("fs");
const path = require("path");
const util = require("util");

globalThis.require = require;
globalThis.fs = fs;
globalThis.path = path;
globalThis.TextEncoder = util.TextEncoder;
globalThis.TextDecoder = util.TextDecoder;
globalThis.performance ??= require("performance");
globalThis.crypto ??= require("crypto");

// 定位 Go 官方 wasm_exec.js：Go 1.26 在 $GOROOT/lib/wasm，旧版在 $GOROOT/misc/wasm。
// 优先 WASM_EXEC 环境变量；否则扫 GOROOT / 常见安装位置（避免 spawn 子进程，Windows 上易 EBUSY）。
function findGlue() {
	if (process.env.WASM_EXEC && fs.existsSync(process.env.WASM_EXEC)) return process.env.WASM_EXEC;
	const roots = [process.env.GOROOT, "C:/Program Files/Go", "/usr/local/go"].filter(Boolean);
	for (const r of roots) {
		for (const sub of ["lib/wasm", "misc/wasm"]) {
			const p = path.join(r, sub, "wasm_exec.js");
			if (fs.existsSync(p)) return p;
		}
	}
	throw new Error("找不到 wasm_exec.js：请设置 GOROOT 或 WASM_EXEC 指向它");
}
require(findGlue()); // 定义 globalThis.Go

const wasmPath = process.argv[2] || path.join(require("os").tmpdir(), "gox.wasm");

const go = new Go();
go.argv = [];
go.env = {};

WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject).then((result) => {
	// 不 await：main 阻塞在 select{}，go.run 的 Promise 永不 resolve，正是常驻语义。
	go.run(result.instance);

	// 等一小拍让 main 完成 goxRunSource/goxReady 注册。
	setTimeout(async () => {
		console.log("[goxReady] =", globalThis.goxReady);
		const cases = [
			"1 + 1",
			"[1,2,3].map(x => x*2)",
			"function f(n){return n<=1?1:n*f(n-1)} f(5)",
			'console.log("hello from gox wasm")',
			'JSON.stringify({a:1, b:[2,3]})',
			'`sum=${[1,2,3,4].reduce((a,b)=>a+b,0)}`',
			'Object.keys({x:1,y:2}).join(",")',
			'(() => { const o = {n:1}; o.n += 41; return o.n; })()',
			// 微任务（Promise）会被 VM 在本次执行结束前 drain；setTimeout 属于宏任务，
			// 需宿主继续驱动 vm.RunTimers（本探针未做，故返回 0）。
			'(() => { let r = 0; Promise.resolve(7).then(v => { r = v; }); return r; })()',
			'(() => { let r = 0; setTimeout(() => { r = 9; }, 0); return r; })()',
			// 错误路径：抛错 / 语法错误 / 未定义引用都应变成 "error: ..." 返回。
			'throw new Error("boom")',
			"let =",
			"undefinedFn()",
		];
		for (const src of cases) {
			console.log("SRC: " + src);
			try {
				console.log("OUT: " + (await globalThis.goxRunSource(src)));
			} catch (e) {
				console.log("ERR: " + e);
			}
		}
		process.exit(0);
	}, 50);
}).catch((err) => {
	console.error("instantiate failed:", err);
	process.exit(1);
});
