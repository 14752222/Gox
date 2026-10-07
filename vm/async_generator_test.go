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
