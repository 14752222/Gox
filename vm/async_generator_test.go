package vm

import (
	"strings"
	"testing"
)

// ===== AsyncGenerator 协议闭环 (rODXmB P1) =====
//
// async function* 过去被编译成「返回 Promise 的 wrapper」(忽略 IsGenerator),
// 调用结果不是带 [Symbol.asyncIterator] 的 AsyncGenerator。本组用例锁定修复后
// 的协议行为: next/return/throw 返回 Promise<{value,done}>、await 与 yield 区分、
// throw 经 Promise reject 传递且 catch 侧 === 抛出侧、return 提前结束。
//
// 期望值均以 Node 实测为准 (语义权威)。

// (a) for await...of 遍历 async generator, 收集出全部 yield 值。
func TestAsyncGeneratorForAwaitCollects(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 1; yield 2; }
		(async function(){
			const out = [];
			for await (const x of g()) out.push(x);
			__out.push("collected:" + JSON.stringify(out));
		})();
	`)
	if !strings.Contains(got, "collected:[1,2]") {
		t.Errorf("for await 应收集 [1,2], got:\n%s", got)
	}
}

// (a') for await over 同步可迭代对象 (回退 [Symbol.iterator] 包装)。
func TestAsyncGeneratorForAwaitSyncFallback(t *testing.T) {
	got := runAsyncEval(t, `
		(async function(){
			const out = [];
			for await (const x of [10, 20]) out.push(x);
			__out.push("sync:" + JSON.stringify(out));
		})();
	`)
	if !strings.Contains(got, "sync:[10,20]") {
		t.Errorf("for await over 数组应得 [10,20], got:\n%s", got)
	}
}

// (b) g() 返回 AsyncGenerator (非 Promise); next() 返回 Promise, await 后
// 为 {value:1,done:false}; [Symbol.asyncIterator]() 返回自身。
func TestAsyncGeneratorNextReturnsPromise(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 1; yield 2; }
		(async function(){
			const it = g();
			__out.push("isPromise:" + (g() instanceof Promise));
			__out.push("iterSelf:" + (it[Symbol.asyncIterator]() === it));
			const p = it.next();
			__out.push("nextIsPromise:" + (p instanceof Promise));
			const r = await p;
			__out.push("r:" + r.value + "," + r.done);
		})();
	`)
	for _, want := range []string{"isPromise:false", "iterSelf:true", "nextIsPromise:true", "r:1,false"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q, got:\n%s", want, got)
		}
	}
}

// (c) 连续 next(): 按 FIFO 顺序结算 (第二次在第一次 resolve 之后)。
func TestAsyncGeneratorNextQueueOrder(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 'first'; yield 'second'; }
		(async function(){
			const it = g();
			const order = [];
			const p1 = it.next().then(r => order.push("1:" + r.value + ":" + r.done));
			const p2 = it.next().then(r => order.push("2:" + r.value + ":" + r.done));
			const p3 = it.next().then(r => order.push("3:" + r.value + ":" + r.done));
			await p1; await p2; await p3;
			__out.push(order.join("|"));
		})();
	`)
	if !strings.Contains(got, "1:first:false|2:second:false|3:undefined:true") {
		t.Errorf("next 结算顺序/值不正确, got:\n%s", got)
	}
}

// (d) await 与 yield 混用: yield 的值被 await (AsyncGeneratorYield 语义),
// await 的表达式结果继续执行。
func TestAsyncGeneratorAwaitYieldMix(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){
			yield Promise.resolve(5);
			const a = await Promise.resolve(10);
			yield a + 1;
			return 'end';
		}
		(async function(){
			const it = g();
			const r1 = await it.next();
			const r2 = await it.next();
			const r3 = await it.next();
			__out.push(JSON.stringify([r1, r2, r3]));
		})();
	`)
	if !strings.Contains(got, `[{"value":5,"done":false},{"value":11,"done":false},{"value":"end","done":true}]`) {
		t.Errorf("await/yield 混用结果不正确, got:\n%s", got)
	}
}

// (e) 体内 throw 经 next() 的 Promise reject 传递, 且 catch 侧 === 抛出侧。
func TestAsyncGeneratorThrowRejects(t *testing.T) {
	got := runAsyncEval(t, `
		const err = new Error('boom');
		async function* t(){ throw err; }
		(async function(){
			const it = t();
			try {
				await it.next();
				__out.push("no-throw");
			} catch (e) {
				__out.push("caught same:" + (e === err) + " msg:" + e.message);
			}
			__out.push("after:" + JSON.stringify(await it.next()));
		})();
	`)
	if !strings.Contains(got, "caught same:true msg:boom") {
		t.Errorf("catch 侧应拿到原始 Error 对象(===), got:\n%s", got)
	}
	if !strings.Contains(got, "after:{\"done\":true}") {
		t.Errorf("抛出后生成器应已完成, got:\n%s", got)
	}
}

// (e') yield 的 Promise reject 时把原因抛回 yield 点, 体内 try/catch 可捕获。
func TestAsyncGeneratorYieldRejectThrowsIntoBody(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){
			try { yield Promise.reject('rej'); } catch(e){ yield 'caught:' + e; }
			yield 9;
		}
		(async function(){
			const it = g();
			const r1 = await it.next();
			const r2 = await it.next();
			const r3 = await it.next();
			__out.push(JSON.stringify([r1, r2, r3]));
		})();
	`)
	if !strings.Contains(got, `[{"value":"caught:rej","done":false},{"value":9,"done":false},{"done":true}]`) {
		t.Errorf("yield 拒绝应抛回体内, got:\n%s", got)
	}
}

// (f) return(v) 提前结束并展开 finally; 之后 next() 返回 done。
func TestAsyncGeneratorReturn(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){
			try { yield 1; yield 2; } finally { __out.push('finally ran'); }
		}
		(async function(){
			const it = g();
			const r1 = await it.next();
			const rr = await it.return(42);
			const r2 = await it.next();
			__out.push("r1:" + r1.value + "," + r1.done);
			__out.push("rr:" + rr.value + "," + rr.done);
			__out.push("r2:" + r2.value + "," + r2.done);
		})();
	`)
	for _, want := range []string{"finally ran", "r1:1,false", "rr:42,true", "r2:undefined,true"} {
		if !strings.Contains(got, want) {
			t.Errorf("return 行为缺少 %q, got:\n%s", want, got)
		}
	}
}

// (f') throw(e) 注入: 体内 catch 可捕获并继续 yield。
func TestAsyncGeneratorThrowIntoBody(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ try { yield 1; } catch(e){ yield 'caught:'+e; } }
		(async function(){
			const it = g();
			const r1 = await it.next();
			const r2 = await it.throw('boom');
			__out.push(JSON.stringify([r1, r2]));
		})();
	`)
	if !strings.Contains(got, `[{"value":1,"done":false},{"value":"caught:boom","done":false}]`) {
		t.Errorf("throw 注入应被体内 catch 捕获, got:\n%s", got)
	}
}

// (g) 既有行为不回归: 普通 async 函数仍返回 Promise; 同步 generator 仍返回
// Generator (next 同步返回 {value,done}, 非 Promise)。
func TestAsyncGeneratorNoRegressionPlainAsyncAndSyncGen(t *testing.T) {
	got := runAsyncEval(t, `
		async function af(){ return 7; }
		function* sg(){ yield 'a'; yield 'b'; }
		(async function(){
			__out.push("afIsPromise:" + (af() instanceof Promise));
			__out.push("afValue:" + (await af()));
			const it = sg();
			const r = it.next();
			__out.push("syncNextIsPromise:" + (r instanceof Promise));
			__out.push("syncR:" + r.value + "," + r.done);
			__out.push("syncR2:" + JSON.stringify(it.next()));
		})();
	`)
	for _, want := range []string{"afIsPromise:true", "afValue:7", "syncNextIsPromise:false", "syncR:a,false", `syncR2:{"value":"b","done":false}`} {
		if !strings.Contains(got, want) {
			t.Errorf("既有行为回归, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// 异步 yield* 委托 (r6e5qp): 委托 async generator, 收集其全部 yield 值,
// 且 yield* 表达式的值为被委托生成器的 return 值。
func TestAsyncGeneratorYieldStarAsyncDelegate(t *testing.T) {
	got := runAsyncEval(t, `
		async function* inner(){ yield 1; yield 2; return "inner-ret"; }
		async function* outer(){ const v = yield* inner(); __out.push("v:" + v); yield 3; }
		(async function(){
			const out = [];
			for await (const x of outer()) out.push(x);
			__out.push("out:" + JSON.stringify(out));
		})();
	`)
	for _, want := range []string{"v:inner-ret", "out:[1,2,3]"} {
		if !strings.Contains(got, want) {
			t.Errorf("异步 yield* 委托结果不正确, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// 异步 yield* 委托: 消费者 next(v) 的值要转发给被委托迭代器的 next(v);
// 同步可迭代对象与同步 generator 也能被异步委托消费。
func TestAsyncGeneratorYieldStarForwardNext(t *testing.T) {
	got := runAsyncEval(t, `
		async function* pass(){ const a = yield "a"; yield "got:" + a; }
		async function* fwd(){ yield* pass(); }
		async function* fwdArr(){ yield* [10, 20]; }
		(async function(){
			const it = fwd();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.next("FWD")));
			__out.push("3:" + JSON.stringify(await it.next()));
			const arr = [];
			for await (const v of fwdArr()) arr.push(v);
			__out.push("arr:" + JSON.stringify(arr));
		})();
	`)
	for _, want := range []string{
		`1:{"value":"a","done":false}`,
		`2:{"value":"got:FWD","done":false}`,
		`3:{"done":true}`,
		"arr:[10,20]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yield* next(v) 转发不正确, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// 异步 yield*: 消费者 throw(e) 转发给被委托迭代器的 throw(e)。
func TestAsyncGeneratorYieldStarThrowForward(t *testing.T) {
	got := runAsyncEval(t, `
		var d = {
			[Symbol.asyncIterator](){ return this; },
			next(){ return {done:false, value:"n1"}; },
			throw(e){ return {done:false, value:"caught:" + e}; }
		};
		async function* g(){ yield* d; }
		(async function(){
			const it = g();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.throw("boom")));
		})();
	`)
	for _, want := range []string{`1:{"value":"n1","done":false}`, `2:{"value":"caught:boom","done":false}`} {
		if !strings.Contains(got, want) {
			t.Errorf("yield* throw 转发不正确, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// 异步 yield*: next() 结果为非对象 → TypeError (不得访问其 then)。
func TestAsyncGeneratorYieldStarNonObjectThrows(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){
			try { yield* { [Symbol.asyncIterator](){ return { next(){ return 42; } }; } }; }
			catch (e) { __out.push("caught:" + e.name); }
		}
		(async function(){ await g().next(); })();
	`)
	if !strings.Contains(got, "caught:TypeError") {
		t.Errorf("非对象迭代结果应抛 TypeError, got:\n%s", got)
	}
}

// ===== yield* 异步委托的三类完成透传 (r6e5qp 第1项) =====
//
// 规范 AsyncGeneratorYieldDelegate 把消费者的 next(v)/throw(e)/return(v) 分别
// 转发给被委托迭代器的同名方法, 并按步进结果的 done 决定「完成生成器」还是
// 「再 yield 一次」。此前 return 完成完全未透传 (生成器被直接关闭), throw
// 缺失方法时既不 AsyncIteratorClose 也不抛 TypeError。
//
// 期望值均以 Node 22 实测为准 (同片段在 Node 下输出与断言一致)。

// (h) 消费者 return(v) 转发给被委托迭代器的 return(v): 结果为未完成 ⇒
// 以该步进值 yield 给消费者, 之后回到正常委托循环 (不结束生成器)。
func TestAsyncGeneratorYieldStarReturnForwardNotDone(t *testing.T) {
	got := runAsyncEval(t, `
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n1" }; },
			return(v) { return { done: false, value: "ret:" + v }; },
		};
		async function* g() { const r = yield* src; __out.push("after:" + r); }
		(async function(){
			const it = g();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.return("X")));
			__out.push("3:" + JSON.stringify(await it.next()));
		})();
	`)
	for _, want := range []string{
		`1:{"value":"n1","done":false}`,
		`2:{"value":"ret:X","done":false}`,
		`3:{"value":"n1","done":false}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yield* return(v) 未完成时应继续委托, 缺少 %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "after:") {
		t.Errorf("return 未完成时生成器不应结束 (yield* 表达式未取值), got:\n%s", got)
	}
}

// (h') 委托的 return 返回 done:true ⇒ 生成器以该步进值完成。
func TestAsyncGeneratorYieldStarReturnForwardDone(t *testing.T) {
	got := runAsyncEval(t, `
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n" }; },
			return(v) { return { done: true, value: "R-" + v }; },
		};
		async function* g() { yield* src; __out.push("unreachable"); }
		(async function(){
			const it = g();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.return("Y")));
			__out.push("3:" + JSON.stringify(await it.next()));
		})();
	`)
	for _, want := range []string{
		`1:{"value":"n","done":false}`,
		`2:{"value":"R-Y","done":true}`,
		`3:{"done":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("yield* return(v) 完成时应以步进值结束生成器, 缺少 %q, got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "unreachable") {
		t.Errorf("done 的 return 完成后不应继续执行委托之后的语句, got:\n%s", got)
	}
}

// (h'') 委托对象没有 return 方法 ⇒ 生成器以 return(v) 的实参 (await 后)
// 完成, 不抛 TypeError (规范 7.c.ii)。
func TestAsyncGeneratorYieldStarReturnMissingMethod(t *testing.T) {
	got := runAsyncEval(t, `
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n" }; },
		};
		async function* g() { yield* src; __out.push("unreachable"); }
		(async function(){
			const it = g();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.return(Promise.resolve("CV"))));
			__out.push("3:" + JSON.stringify(await it.next()));
		})();
	`)
	for _, want := range []string{
		`1:{"value":"n","done":false}`,
		`2:{"value":"CV","done":true}`,
		`3:{"done":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("缺 return 方法时应以实参 (await 后) 完成, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// (h''') return 属性为 null ⇒ 按 GetMethod 语义等同「无 return 方法」。
func TestAsyncGeneratorYieldStarReturnMethodIsNull(t *testing.T) {
	got := runAsyncEval(t, `
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n" }; },
			return: null,
		};
		async function* g() { yield* src; }
		(async function(){
			const it = g();
			__out.push("1:" + JSON.stringify(await it.next()));
			__out.push("2:" + JSON.stringify(await it.return("DV")));
		})();
	`)
	for _, want := range []string{
		`1:{"value":"n","done":false}`,
		`2:{"value":"DV","done":true}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("return:null 应等同缺 return 方法, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// (i) 委托对象没有 throw 方法 ⇒ 先 AsyncIteratorClose (调用其可调 return),
// 再以 TypeError reject (规范 7.b.iii)。
func TestAsyncGeneratorYieldStarNoThrowMethodClosesThenTypeError(t *testing.T) {
	got := runAsyncEval(t, `
		var closed = 0;
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n" }; },
			return() { closed++; return { done: true }; },
		};
		async function* g() { yield* src; }
		(async function(){
			const it = g();
			await it.next();
			try { await it.throw("boom"); __out.push("no-throw"); }
			catch (e) { __out.push("caught:" + e.name + " closed:" + closed); }
		})();
	`)
	if !strings.Contains(got, "caught:TypeError closed:1") {
		t.Errorf("缺 throw 方法时应 AsyncIteratorClose 后抛 TypeError, got:\n%s", got)
	}
}

// (j) 委托对象的 return 方法本身抛错 ⇒ 该错误原样传播 (其余无关错误不得掩盖)。
func TestAsyncGeneratorYieldStarReturnAbruptPropagates(t *testing.T) {
	got := runAsyncEval(t, `
		var err = new Error("return-boom");
		var src = {
			[Symbol.asyncIterator]() { return this; },
			next() { return { done: false, value: "n" }; },
			return() { throw err; },
		};
		async function* g() { yield* src; }
		(async function(){
			const it = g();
			await it.next();
			try { await it.return("z"); __out.push("no-throw"); }
			catch (e) { __out.push("caught:" + (e === err) + ":" + e.message); }
		})();
	`)
	if !strings.Contains(got, "caught:true:return-boom") {
		t.Errorf("委托 return 抛出的错误应原样传播 (===), got:\n%s", got)
	}
}

// (g') 委托对象的 next 是抛错的访问器 ⇒ 抛出的是该原始值, 不得退化成
// "no callable next()" 的 TypeError。
func TestAsyncGeneratorYieldStarNextGetterAbruptPropagates(t *testing.T) {
	got := runAsyncEval(t, `
		var err = new Error("getter-boom");
		async function* g(){
			yield* {
				[Symbol.asyncIterator]() { return this; },
				get next() { throw err; },
			};
		}
		(async function(){
			try { await g().next(); __out.push("no-throw"); }
			catch (e) { __out.push("caught:" + (e === err) + ":" + e.message); }
		})();
	`)
	if !strings.Contains(got, "caught:true:getter-boom") {
		t.Errorf("get next 抛出的原始值应原样传播, got:\n%s", got)
	}
}

// (k) 委托「同步」可迭代对象/同步 generator: 步进值按
// %AsyncFromSyncIteratorPrototype%.next 步骤 14 解包 (PromiseResolve)。
func TestAsyncGeneratorYieldStarSyncSourceUnwrapsValue(t *testing.T) {
	got := runAsyncEval(t, `
		function* syncGen() { yield Promise.resolve("SV"); yield "second"; }
		async function* gArr() { yield* [Promise.resolve("EV"), "plain"]; }
		async function* gGen() { yield* syncGen(); }
		(async function(){
			const it = gArr();
			__out.push("arr1:" + JSON.stringify(await it.next()));
			__out.push("arr2:" + JSON.stringify(await it.next()));
			const itg = gGen();
			__out.push("gen1:" + JSON.stringify(await itg.next()));
			__out.push("gen2:" + JSON.stringify(await itg.next()));
		})();
	`)
	for _, want := range []string{
		`arr1:{"value":"EV","done":false}`,
		`arr2:{"value":"plain","done":false}`,
		`gen1:{"value":"SV","done":false}`,
		`gen2:{"value":"second","done":false}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("同步源的委托值应被解包, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// (k') 委托「原生 @@asyncIterator」: 步进值**不**解包 (yield* 不做
// AsyncGeneratorYield(Await) 之外的额外解包 —— test262
// yield-star-promise-not-unwrapped)。
func TestAsyncGeneratorYieldStarNativeAsyncSourceDoesNotUnwrap(t *testing.T) {
	got := runAsyncEval(t, `
		var innerPromise = Promise.resolve("FV");
		async function* g(){
			yield* {
				[Symbol.asyncIterator]() { return this; },
				next() { return { done: false, value: innerPromise }; },
			};
		}
		(async function(){
			const r = await g().next();
			__out.push("done:" + r.done);
			__out.push("same:" + (r.value === innerPromise));
		})();
	`)
	for _, want := range []string{"done:false", "same:true"} {
		if !strings.Contains(got, want) {
			t.Errorf("原生 @@asyncIterator 的值不得解包, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// (l) 同步源 (CreateAsyncFromSyncIterator) 的三条路径都要把步进结果的 value
// 按 PromiseResolve 解包: next 的 done 步 (yield* 表达式值)、return、throw。
// 规范 %AsyncFromSyncIteratorPrototype%.{next,return,throw} 步骤同款
// (valueWrapper = PromiseResolve(%Promise%, value), 与 done 无关)。
func TestAsyncGeneratorYieldStarSyncWrapperResolvesStepValue(t *testing.T) {
	got := runAsyncEval(t, `
		function mkIter() {
			return {
				next() { return { done: false, value: 1 }; },
				return(v) { return { done: false, value: Promise.resolve("RV") }; },
				throw(e) { return { done: false, value: Promise.resolve("TV") }; },
			};
		}
		var src = { [Symbol.iterator]() { return mkIter(); } };
		var doneSrc = { [Symbol.iterator]() { return { next() { return { done: true, value: Promise.resolve("XV") }; } }; } };
		async function* gRet() { yield* src; }
		async function* gThr() { yield* src; }
		async function* gDone() { const r = yield* doneSrc; __out.push("doneValue:" + r); }
		(async function(){
			const it = gRet();
			await it.next();
			const r = await it.return("z");
			__out.push("R done:" + r.done + " eqRV:" + (r.value === "RV"));
			const it2 = gThr();
			await it2.next();
			const t = await it2.throw("boom");
			__out.push("T done:" + t.done + " eqTV:" + (t.value === "TV"));
			await gDone().next();
		})();
	`)
	for _, want := range []string{
		"R done:false eqRV:true",
		"T done:false eqTV:true",
		"doneValue:XV",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("同步源步进值应解包, 缺少 %q, got:\n%s", want, got)
		}
	}
}

// ===== 子项3: @@toStringTag 沿原型链可达 (r6e5qp) =====
//
// async generator 实例的 @@toStringTag 落在 %AsyncGeneratorPrototype% (AGP) 上,
// 实例到 AGP 之间还隔着一层该函数自己的 .prototype。规范里 Get(@@toStringTag)
// 是**完整原型链**查找, 故 it[Symbol.toStringTag] 与 Object.prototype.toString
// 都必须看得到它。此前 Gox 只向 fn.prototype 委托一层即止, *Object 的
// GetSymbolProperty 又不走链, 于是两者分别得到 undefined / "[object Object]"。
// (期望值以 Node 22 实测为准。)

// it[Symbol.toStringTag] === "AsyncGenerator" (跨两层原型可达)。
func TestAsyncGeneratorInstanceToStringTag(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 1; }
		const it = g();
		__out.push("tag:" + it[Symbol.toStringTag]);
		__out.push("str:" + Object.prototype.toString.call(it));
		__out.push("strAgp:" + Object.prototype.toString.call(Object.getPrototypeOf(Object.getPrototypeOf(it))));
	`)
	for _, want := range []string{"tag:AsyncGenerator", "str:[object AsyncGenerator]", "strAgp:[object AsyncGenerator]"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q, got:/n%s", want, got)
		}
	}
}

// 删除 AGP[@@toStringTag] 后: 实例标签回落 undefined, toString 回落 "[object Object]"
// (证明是**链式**查找而非硬编码标签)。
func TestAsyncGeneratorToStringTagChainFallback(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 1; }
		const AGP = Object.getPrototypeOf(Object.getPrototypeOf(g()));
		delete AGP[Symbol.toStringTag];
		__out.push("tag:" + g()[Symbol.toStringTag]);
		__out.push("str:" + Object.prototype.toString.call(g()));
	`)
	for _, want := range []string{"tag:undefined", "str:[object Object]"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q, got:/n%s", want, got)
		}
	}
}

// 用户在该函数自己的 .prototype 上定义 @@toStringTag → 覆盖 AGP 的 (近者优先)。
func TestAsyncGeneratorToStringTagShadowing(t *testing.T) {
	got := runAsyncEval(t, `
		async function* g(){ yield 1; }
		const it = g();
		Object.defineProperty(Object.getPrototypeOf(it), Symbol.toStringTag, { value: "Custom", configurable: true });
		__out.push("tag:" + it[Symbol.toStringTag]);
		__out.push("str:" + Object.prototype.toString.call(it));
	`)
	for _, want := range []string{"tag:Custom", "str:[object Custom]"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少 %q, got:/n%s", want, got)
		}
	}
}
