package gfx

import (
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== gx/solid 扩展: createResource / onMount / onCleanup (2026-09-19 拍板落地) =====
//
// 驱动纪律: 从 Go 侧调用脚本函数 (settle/refetch/switchTo) 必须发生在
// v.RunTimersWithPump 的执行期内 —— 回调桥依赖 currentVM, 脚本主跑结束后
// 它是 nil, 调用会静默变 undefined (与 p1b 的 callGlobal "必须在步骤里调用"
// 同一条纪律)。这里用 runPumpSteps 把每步塞进一个泵轮次。

// TestCreateResourceSyncFetcher 同步 fetcher: 包装成立即可用的资源 (v1 减法
// 语义之一), state 直接 ready。
func TestCreateResourceSyncFetcher(t *testing.T) {
	v := evalWithGlobals(t, `
		import { createResource } from "gx/solid";
		const [data, res] = createResource(() => ({ n: 7 }));
		globalThis.g_state = res.state();
		globalThis.g_n = data().n;
	`)
	assertGlobal(t, v, "g_state", "ready")
	assertGlobal(t, v, "g_n", "7")
}

// TestCreateResourcePromiseLifecycle 异步 fetcher 的完整状态机:
// pending → ready; refetch 保留旧值 (refreshing) → ready。
func TestCreateResourcePromiseLifecycle(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createResource } from "gx/solid";
		import { h, render } from "gx/gfx";
		let resolver = null;
		const [data, res] = createResource(() =>
			new Promise((resolve) => { resolver = resolve; }));
		globalThis.g_phase1 = res.state();  // pending (取数未回来)
		render(h("column", null, h("text", {font: 14}, () => "n=" + data())));
		globalThis.settle = (val) => resolver(val);
		globalThis.refetch = res.refetch;
		globalThis.state = res.state;
		globalThis.data = data;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobal(t, v, "g_phase1", "pending")

	runPumpSteps(t, v, fake, []func(){
		func() { callGlobalFn(t, v, "settle", object.NewNumber(41)) },
		func() {
			if got := callGlobalInspect(t, v, "state"); got != "ready" {
				t.Fatalf("resolve 后 state = %v, want ready", got)
			}
			assertGlobalText(t, v, "n=41")
		},
		func() { callGlobalFn(t, v, "refetch") },
		func() {
			if got := callGlobalInspect(t, v, "state"); got != "refreshing" {
				t.Fatalf("refetch 后 state = %v, want refreshing", got)
			}
			if got := callGlobalInspect(t, v, "data"); got != "41" {
				t.Fatalf("refreshing 期间 data() = %v, want 旧值 41", got)
			}
		},
		func() { callGlobalFn(t, v, "settle", object.NewNumber(42)) },
		func() { assertGlobalText(t, v, "n=42") },
	})
}

// TestCreateResourceLatestWins 快速连续 refetch: 旧响应**后**到, 必须丢弃,
// 否则界面会闪回旧值 (S-a 竞态痛点的结构性收编)。
func TestCreateResourceLatestWins(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createResource } from "gx/solid";
		const resolvers = [];
		const [data, res] = createResource(() =>
			new Promise((resolve) => { resolvers.push(resolve); }));
		globalThis.settle = (i, val) => resolvers[i](val);
		globalThis.refetch = res.refetch;
		globalThis.state = res.state;
		globalThis.data = data;
		globalThis.g_first = res.state();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobal(t, v, "g_first", "pending")

	runPumpSteps(t, v, fake, []func(){
		func() { callGlobalFn(t, v, "settle", object.NewNumber(0), object.NewNumber(1)) }, // 第一代 → ready(1)
		func() {
			callGlobalFn(t, v, "refetch") // 第二代 (resolvers 下标 1)
			callGlobalFn(t, v, "refetch") // 第三代 (下标 2): 第二代已过期
		},
		func() { callGlobalFn(t, v, "settle", object.NewNumber(1), object.NewNumber(2)) }, // 过期响应
		func() {
			if got := callGlobalInspect(t, v, "state"); got != "refreshing" {
				t.Fatalf("过期响应不该改状态, state = %v", got)
			}
			if got := callGlobalInspect(t, v, "data"); got != "1" {
				t.Fatalf("过期响应不该改值, data() = %v, want 1", got)
			}
		},
		func() { callGlobalFn(t, v, "settle", object.NewNumber(2), object.NewNumber(3)) }, // 最新代 → ready(3)
		func() {
			if got := callGlobalInspect(t, v, "state"); got != "ready" {
				t.Fatalf("最新响应后 state = %v, want ready", got)
			}
			if got := callGlobalInspect(t, v, "data"); got != "3" {
				t.Fatalf("最新响应后 data() = %v, want 3", got)
			}
		},
	})
}

// TestCreateResourceErrorNoThrow error 减法语义 (公共 API 承诺): data() 不抛,
// 返回上一次的值 (从未成功过则 undefined); 错误从 res.error() 读。
func TestCreateResourceErrorNoThrow(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createResource } from "gx/solid";
		let mode = "ok";
		let n = 1;
		const [data, res] = createResource(() =>
			new Promise((resolve, reject) => { mode === "ok" ? resolve(n) : reject("boom " + n); }));
		globalThis.g_err_initial = res.error();
		globalThis.refetch = res.refetch;
		globalThis.state = res.state;
		globalThis.data = data;
		globalThis.error = res.error;
		globalThis.setBoom = (m) => { mode = m; n++; };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobal(t, v, "g_err_initial", "undefined")

	runPumpSteps(t, v, fake, []func(){
		func() {
			// fetcher 是"构造即 resolve"的 Promise: 初次取数已经同步完成
			if got := callGlobalInspect(t, v, "state"); got != "ready" {
				t.Fatalf("首次取数 state = %v, want ready", got)
			}
			if got := callGlobalInspect(t, v, "data"); got != "1" {
				t.Fatalf("首次取数 data() = %v, want 1", got)
			}
		},
		func() {
			callGlobalFn(t, v, "setBoom", object.NewString("bad"))
			callGlobalFn(t, v, "refetch")
		},
		func() {
			if got := callGlobalInspect(t, v, "state"); got != "error" {
				t.Fatalf("失败后 state = %v, want error", got)
			}
			if got := callGlobalInspect(t, v, "data"); got != "1" {
				t.Fatalf("error 态 data() = %v, want 保留旧值 1 (不抛不丢)", got)
			}
			if got := callGlobalInspect(t, v, "error"); got != "boom 2" {
				t.Fatalf("error() = %v, want boom 2", got)
			}
		},
	})
}

// TestOnMountOnCleanupLifecycle 条件渲染切换时 onMount/onCleanup 成对触发,
// 序列 = mount A → cleanup A → mount B → cleanup B → mount A (每一代独立)。
func TestOnMountOnCleanupLifecycle(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createSignal, onMount, onCleanup } from "gx/solid";
		import { h, render } from "gx/gfx";
		let log = "";
		const [which, setWhich] = createSignal("A");
		const Panel = (p) => {
			onMount(() => { log += "mount:" + p.name + ";"; });
			onCleanup(() => { log += "cleanup:" + p.name + ";"; });
			return h("column", {gap: 4}, h("text", {font: 14}, "panel " + p.name));
		};
		render(h("column", {gap: 6},
			() => which() === "A" ? Panel({name: "A"}) : Panel({name: "B"})));
		globalThis.switchTo = (w) => setWhich(w);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobal(t, v, "log", "mount:A;") // 初始挂载即触发 onMount

	runPumpSteps(t, v, fake, []func(){
		func() { callGlobalFn(t, v, "switchTo", object.NewString("B")) },
		func() { assertGlobal(t, v, "log", "mount:A;cleanup:A;mount:B;") },
		func() { callGlobalFn(t, v, "switchTo", object.NewString("A")) },
		func() { assertGlobal(t, v, "log", "mount:A;cleanup:A;mount:B;cleanup:B;mount:A;") },
	})
}

// evalWithGlobals 跑一段脚本 (脚本自己把断言值挂到 globalThis), 需要挂载的
// 脚本由它配好假窗口工厂。
func evalWithGlobals(t *testing.T, src string) *vm.VM {
	t.Helper()
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })
	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	return v
}

func assertGlobal(t *testing.T, v *vm.VM, name, want string) {
	t.Helper()
	val, _ := v.Globals().Get(name)
	if val == nil || val.Inspect() != want {
		got := "<nil>"
		if val != nil {
			got = val.Inspect()
		}
		t.Fatalf("%s = %s, want %q", name, got, want)
	}
}

// runPumpSteps 把 Go 侧的驱动/断言步骤逐个塞进泵轮次 (与 p2b runDemoSteps
// 同构, 但步骤不带 root/fake 参数 —— 这里驱动的是脚本全局函数)。
func runPumpSteps(t *testing.T, v *vm.VM, fake *fakeSurface, steps []func()) {
	t.Helper()
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 无害唤醒: 只做断言的步骤自己不产生事件 (语义同 runDemoSteps)
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1]()
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
}

// callGlobalFn 调用挂在 globalThis 上的函数并取返回值 (与 p1b 的 callGlobal
// 区分: 那个不取返回值)。必须在泵轮次内调用 (currentVM 纪律, 见文件头)。
func callGlobalFn(t *testing.T, v *vm.VM, name string, args ...object.Value) object.Value {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok || !object.IsCallable(fn) {
		t.Fatalf("global %s 不是函数", name)
	}
	return object.CallFunction(fn, nil, args...)
}

// callGlobalInspect 调全局函数并返回结果的 Inspect 文本。
func callGlobalInspect(t *testing.T, v *vm.VM, name string, args ...object.Value) string {
	t.Helper()
	res := callGlobalFn(t, v, name, args...)
	if err := takeCallbackErr(); err != nil {
		t.Fatalf("调用 %s 抛错: %v", name, err)
	}
	if res == nil {
		return "<nil>"
	}
	return res.Inspect()
}

// assertGlobalText 断言窗口里第一个 text 节点的内容 (响应式文本上屏的最短路径)。
func assertGlobalText(t *testing.T, v *vm.VM, want string) {
	t.Helper()
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()
	tx := findFirst(root, "text")
	if tx == nil || tx.TextContent() != want {
		got := "<nil>"
		if tx != nil {
			got = tx.TextContent()
		}
		t.Fatalf("text 内容 = %q, want %q", got, want)
	}
}
