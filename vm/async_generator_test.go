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
