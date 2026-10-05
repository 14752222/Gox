package vm

import (
	"strings"
	"testing"
)

// ===== 生成器/异步生成器形参绑定时机 (r6e5qp 第 4 项) =====
//
// 规范: 形参绑定 (默认值求值 / 解构 / 形参作用域建立) 在**调用时**同步完成,
// 只有函数体推迟到首次 next()。Gox 旧实现把整个函数 (含前导段) 都推迟到
// 首次 next()/驱动, 于是:
//   - 默认值里的副作用发生在 next() 而非调用时;
//   - 形参抛错时机错位 —— 生成器/异步生成器须**同步抛出**。
//
// 与之相对, 普通 async 函数的形参求值抛错必须走 **rejected Promise**
// (而非同步抛出) —— 其形参绑定留在 __spawn 驱动路径内 (DeferParams)。
//
// 期望值均以 Node 22 实测为准 (语义权威)。

// 生成器: 默认值副作用在调用时同步发生; 函数体仍推迟到首次 next()。
func TestGeneratorParamDefaultEvalAtCall(t *testing.T) {
	got := runAsyncEval(t, `
		var order = [];
		function* g(a = order.push("param")) { order.push("body"); yield 1; }
		g();
		order.push("after-call");
		__out.push("call:" + JSON.stringify(order));
		var it = g(); it.next();
		__out.push("next:" + JSON.stringify(order));
	`)
	if !strings.Contains(got, `call:["param","after-call"]`) {
		t.Errorf("生成器默认值应在调用时求值, got:\n%s", got)
	}
	if !strings.Contains(got, `next:["param","after-call","param","body"]`) {
		t.Errorf("生成器体应推迟到 next(), got:\n%s", got)
	}
}

// 生成器: 形参默认值抛错同步抛出。
func TestGeneratorParamThrowSync(t *testing.T) {
	got := runAsyncEval(t, `
		function boom(){ throw new Error("pb"); }
		function* g(a = boom()) { yield 1; }
		var sync = "none";
		try { g(); } catch(e){ sync = e.message; }
		__out.push("sync:" + sync);
	`)
	if !strings.Contains(got, "sync:pb") {
		t.Errorf("生成器形参抛错应同步抛出, got:\n%s", got)
	}
}

// 生成器: 形参解构抛错同步抛出。
func TestGeneratorParamDestructureThrowSync(t *testing.T) {
	got := runAsyncEval(t, `
		function* g({x}) { yield x; }
		var sync = "none";
		try { g(null); } catch(e){ sync = "threw"; }
		__out.push("sync:" + sync);
		__out.push("ok:" + g({x:5}).next().value);
	`)
	if !strings.Contains(got, "sync:threw") {
		t.Errorf("生成器形参解构抛错应同步抛出, got:\n%s", got)
	}
	if !strings.Contains(got, "ok:5") {
		t.Errorf("生成器形参解构正常值应可读, got:\n%s", got)
	}
}

// async generator: 形参副作用在调用时同步发生; 体推迟; 形参抛错同步抛出。
func TestAsyncGeneratorParamBindingAtCall(t *testing.T) {
	got := runAsyncEval(t, `
		var order = [];
		async function* g(a = order.push("param")) { order.push("body"); yield 1; }
		g();
		order.push("after-call");
		__out.push("call:" + JSON.stringify(order));

		function boom(){ throw new Error("pb"); }
		async function* h(a = boom()) { yield 1; }
		var sync = "none";
		try { h(); } catch(e){ sync = e.message; }
		__out.push("sync:" + sync);
	`)
	if !strings.Contains(got, `call:["param","after-call"]`) {
		t.Errorf("async generator 形参应在调用时求值 (体推迟), got:\n%s", got)
	}
	if !strings.Contains(got, "sync:pb") {
		t.Errorf("async generator 形参抛错应同步抛出, got:\n%s", got)
	}
}

// 普通 async 函数: 形参副作用同步求值 (回归守卫)。
func TestAsyncFunctionParamDefaultEvalAtCall(t *testing.T) {
	got := runAsyncEval(t, `
		var order = [];
		async function f(a = order.push("param")) {}
		f();
		order.push("after-call");
		__out.push("call:" + JSON.stringify(order));
	`)
	if !strings.Contains(got, `call:["param","after-call"]`) {
		t.Errorf("async 函数形参应在调用时求值, got:\n%s", got)
	}
}

// 普通 async 函数: 形参默认值抛错必须是 rejected Promise, **不是**同步抛出。
// (与生成器的同步抛出形成对照 —— DeferParams 路径的回归守卫。)
func TestAsyncFunctionParamThrowRejects(t *testing.T) {
	got := runAsyncEval(t, `
		function boom(){ throw new Error("pb"); }
		async function f(a = boom()) {}
		var sync = "none";
		var p;
		try { p = f(); } catch(e){ sync = "sync-throw"; }
		__out.push("sync:" + sync);
		__out.push("isPromise:" + (p instanceof Promise));
		p.then(
			function(){ __out.push("resolved"); },
			function(e){ __out.push("rejected:" + e.message); }
		);
	`)
	for _, want := range []string{"sync:none", "isPromise:true", "rejected:pb"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q, got:\n%s", want, got)
		}
	}
}

// 生成器: 默认值创建的闭包捕获形参后, 函数体内对形参的重新赋值必须对
// 该闭包可见 (前导帧与函数体共享同一 binding cell —— 见 rebuildGenFrame 的
// reuseLocals)。若不共享, 闭包会读到形参初值而非重新赋值后的值。
func TestGeneratorPrologueClosureSharesBinding(t *testing.T) {
	got := runAsyncEval(t, `
		function* g(a, f = () => a) { a = 6; yield f(); }
		__out.push("v:" + g(1).next().value);
	`)
	if !strings.Contains(got, "v:6") {
		t.Errorf("前导段闭包应与函数体共享形参绑定 (期望 6), got:\n%s", got)
	}
}

// async generator 同款: 前导段闭包与函数体共享形参绑定。
func TestAsyncGeneratorPrologueClosureSharesBinding(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(a, f = () => a) { a = 9; yield f(); }
		g(1).next().then(function(r){ __out.push("v:" + r.value); });
	`)
	if !strings.Contains(got, "v:9") {
		t.Errorf("async 生成器前导段闭包应共享形参绑定 (期望 9), got:\n%s", got)
	}
}

// 生成器形参副作用顺序: 多个默认值按形参顺序求值, 先于函数体。
func TestGeneratorParamDefaultsOrder(t *testing.T) {
	got := runAsyncEval(t, `
		var order = [];
		function* g(a = order.push("a"), b = order.push("b")) { order.push("body"); yield 1; }
		g();
		__out.push("at-call:" + JSON.stringify(order));
	`)
	if !strings.Contains(got, `at-call:["a","b"]`) {
		t.Errorf("多个默认值应按形参顺序在调用时求值, got:\n%s", got)
	}
}
