package vm

import (
	"testing"
)

// gx/solid 响应式模块的行为测试。
// 全部在单个脚本内完成 (effect 同步执行, 无需事件循环驱动)。

// TestSolidSignalBasics getter/setter、更新函数 setter、相同值不通知。
func TestSolidSignalBasics(t *testing.T) {
	assertNumber(t, evalJS(t, `
		import { createSignal } from "gx/solid";
		const [n, setN] = createSignal(2);
		setN(5);
		n();
	`), 5)
	// 更新函数形式
	assertNumber(t, evalJS(t, `
		import { createSignal } from "gx/solid";
		const [n, setN] = createSignal(5);
		setN(x => x + 1);
		n();
	`), 6)
	// === 相同值不通知订阅者
	assertString(t, evalJS(t, `
		import { createSignal, createEffect } from "gx/solid";
		const [n, setN] = createSignal(5);
		let log = "";
		createEffect(() => { log += n(); });
		setN(5); // 相同值, 不触发
		log;
	`), "5")
}

// TestSolidEffectLifecycle effect 立即执行、依赖变化重跑、dispose 注销。
func TestSolidEffectLifecycle(t *testing.T) {
	assertString(t, evalJS(t, `
		import { createSignal, createEffect } from "gx/solid";
		const [n, setN] = createSignal(0);
		let log = "";
		createEffect(() => { log += n(); });
		setN(1);
		setN(2);
		log;
	`), "012")

	// dispose 后不再响应
	assertString(t, evalJS(t, `
		import { createSignal, createEffect } from "gx/solid";
		const [n, setN] = createSignal(1);
		let log = "";
		const dispose = createEffect(() => { log += n(); });
		setN(2);
		dispose();
		setN(3); // 已注销, 不触发
		log;
	`), "12")
}

// TestSolidEffectDependencyResync 每轮重新收集依赖:
// 分支切换后, 不再被引用的依赖自动退订。
func TestSolidEffectDependencyResync(t *testing.T) {
	assertString(t, evalJS(t, `
		import { createSignal, createEffect } from "gx/solid";
		const [a, setA] = createSignal(1);
		const [b, setB] = createSignal(1);
		let useA = true;
		let runs = 0;
		createEffect(() => { runs++; useA ? a() : b(); });
		// 初始: runs=1, 依赖只有 a
		setB(9);          // b 不是依赖 → 不触发, runs=1
		setA(2);          // 触发 → runs=2 (仍订阅 a)
		useA = false;
		setA(3);          // 触发重跑, 本轮引用 b → 依赖切换为 b, runs=3
		setA(4);          // a 已退订 → 不触发, runs=3
		setB(9);          // 值相同 → 不触发
		setB(10);         // b 是依赖 → 触发, runs=4
		"" + runs;
	`), "4")
}

// TestSolidMemo memo 缓存与惰性重算。
func TestSolidMemo(t *testing.T) {
	assertNumber(t, evalJS(t, `
		import { createSignal, createMemo } from "gx/solid";
		const [n, setN] = createSignal(2);
		const double = createMemo(() => n() * 2);
		const a = double();
		setN(6);
		double();
	`), 12)

	// 计算次数: 只有读取才重算 (惰性)
	assertString(t, evalJS(t, `
		import { createSignal, createMemo } from "gx/solid";
		const [n, setN] = createSignal(1);
		let computes = 0;
		const m = createMemo(() => { computes++; return n() + 1; });
		m(); m();          // 缓存, 不重算: computes=1
		setN(2);           // 标脏, 但未读取: computes=1
		const v1 = computes;
		m();               // 重算: computes=2
		"" + v1 + "," + computes + "," + m();
	`), "1,2,3")
}

// TestSolidMemoInEffect effect 依赖 memo: memo 的依赖变化要传导到 effect。
func TestSolidMemoInEffect(t *testing.T) {
	assertString(t, evalJS(t, `
		import { createSignal, createEffect, createMemo } from "gx/solid";
		const [n, setN] = createSignal(1);
		const d = createMemo(() => n() * 10);
		let log = "";
		createEffect(() => { log += "," + d(); });
		setN(2);
		log;
	`), ",10,20")
}

// TestSolidEffectErrorPropagates 观察者抛出的异常冒泡到触发点。
func TestSolidEffectErrorPropagates(t *testing.T) {
	assertString(t, evalJS(t, `
		import { createSignal, createEffect } from "gx/solid";
		const [n, setN] = createSignal(1);
		createEffect(() => { if (n() > 1) throw new Error("boom"); });
		let r = "no-throw";
		try { setN(2); } catch (e) { r = e.message; }
		r;
	`), "boom")
}
