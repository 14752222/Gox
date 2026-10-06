package vm

import (
	"path/filepath"
	"testing"
	"time"
)

// then 回调返回一个**最终 reject** 的 promise 时, 派生 promise 必须继承该
// rejection (thenable adoption 的 rejected 分支)。
//
// 回归: object.Promise.Resolve 在「内层 promise 仍 pending」分支里把 rejection
// 回调注册进 CatchCallbacks 时漏标 IsCatch, 而 invokePromiseCallbacks 只对
// IsCatch 的回调走 rejection 分支 ⇒ 该回调被静默跳过, 派生 promise 永久停留
// pending。上游的 Promise.all / 模块图动态 import 因此永不结算。
func TestThenAdoptsRejectingThenable(t *testing.T) {
	vm, err := EvalVM(`
		globalThis.__state = "pending";
		const d = Promise.withResolvers();
		const outer = Promise.withResolvers();
		outer.promise.then(() => d.promise).catch(() => { globalThis.__state = "rejected"; });
		outer.resolve();
		d.reject("boom");
		"synced"
	`)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if err := vm.RunTimersUntil(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	v, _ := vm.Globals().Get("__state")
	assertString(t, v, "rejected")
}

// 与上同源, 但内层 promise 的 reject 晚于派生链建立 (setTimeout 里 reject),
// 覆盖「回调已注册、稍后才被拒绝」的时序。
func TestThenAdoptsRejectingThenableDeferred(t *testing.T) {
	vm, err := EvalVM(`
		globalThis.__state = "pending";
		const d = Promise.withResolvers();
		const outer = Promise.withResolvers();
		outer.promise.then(() => d.promise).catch(() => { globalThis.__state = "rejected"; });
		outer.resolve();
		setTimeout(() => d.reject("boom"), 0);
		"synced"
	`)
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if err := vm.RunTimersUntil(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	v, _ := vm.Globals().Get("__state")
	assertString(t, v, "rejected")
}

// 端到端复现 test262 top-level-await/rejection-order.js 的时序:
// b 含 TLA 且最终被 reject, 两个动态 import 的拒绝按 B→A 结算; 第一个
// import 的 promise 经 then 回调返回 (而非 .catch 就地吞掉), 派生 promise
// 必须继承 rejection, 否则 Promise.all 永不结算、收尾 then 不触发。
func TestAsyncModuleGraphRejectionOrderSettles(t *testing.T) {
	dir := writeModuleDir(t, map[string]string{
		"setup.js": `export const p1 = Promise.withResolvers();
export const pA_start = Promise.withResolvers();
export const pB_start = Promise.withResolvers();`,
		"a-sentinel.js": `import { pA_start } from "./setup.js";
pA_start.resolve();`,
		"b-sentinel.js": `import { pB_start } from "./setup.js";
pB_start.resolve();`,
		"b.js": `import "./b-sentinel.js";
import { p1 } from "./setup.js";
await p1.promise;`,
		"a.js": `import "./a-sentinel.js";
import "./b.js";`,
		"entry.js": `import { p1, pA_start, pB_start } from "./setup.js";
globalThis.logs = [];
globalThis.done = false;
const importsP = Promise.all([
  pB_start.promise.then(() => import("./a.js").finally(() => globalThis.logs.push("A"))).catch(() => {}),
  import("./b.js").finally(() => globalThis.logs.push("B")).catch(() => {}),
]);
Promise.all([pA_start.promise, pB_start.promise]).then(p1.reject);
importsP.then(() => { globalThis.done = true; globalThis.logsStr = globalThis.logs.join(","); });
"entry"`,
	})
	vm, err := EvalFileVM(filepath.Join(dir, "entry.js"))
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if err := vm.RunTimersUntil(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatalf("RunTimers: %v", err)
	}
	done, _ := vm.Globals().Get("done")
	assertBoolean(t, done, true)
	logsStr, _ := vm.Globals().Get("logsStr")
	assertString(t, logsStr, "B,A")
}
