// run_eval.js —— 把命令行上给定的任意 JS 源码片段喂给 gox.wasm 执行并打印结果。
//
// 与 run_node.js（内置固定 12 用例的回归脚本）互补：本脚本证明「任意一段 JS 源码
// 都能从宿主传进 wasm 引擎并取回输出」，是 console 版 Playground「编辑框 → 运行」
// 这一条链路的最小可用验证。
//
// 用法:
//   GOOS=js GOARCH=wasm go build -o /tmp/gox.wasm ./cmd/goxwasm
//   node cmd/goxwasm/run_eval.js /tmp/gox.wasm "console.log(1+1)" "var a=[1,2,3]; console.log(a.map(x=>x*2).join(','))"
//
// 每个参数是一段独立源码；逐个求值并打印 "SRC:" / "OUT:"。
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

// 定位 Go 官方 wasm_exec.js（Go 1.26 在 $GOROOT/lib/wasm，旧版在 $GOROOT/misc/wasm）。
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

const wasmPath = process.argv[2];
const sources = process.argv.slice(3);
if (!wasmPath || sources.length === 0) {
	console.error("用法: node run_eval.js <gox.wasm> <code1> [code2 ...]");
	process.exit(2);
}

const go = new Go();
go.argv = [];
go.env = {};

WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject).then((result) => {
	// 不 await：main 阻塞在 select{}，go.run 的 Promise 永不 resolve，正是常驻语义。
	go.run(result.instance);

	setTimeout(async () => {
		console.log("[goxReady] =", globalThis.goxReady);
		for (const src of sources) {
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
