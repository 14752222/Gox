package vm

import "testing"

// ===== 微任务队列 (Promise Jobs, rrpZIw / r63RpV) 的顺序语义 =====
//
// 旧模型是「结算即回调」: promise resolve 后 .then 回调同步执行,
// 不符合 ES 规范 (回调应入队, 当前同步执行完成后、下一个宏任务前按
// FIFO 执行, drain 期间新入队的同轮继续)。test262 request-queue-order
// 等顺序类用例依赖此语义。
//
// 各用例把事件写进全局数组, 最后由定时器 (宏任务) 落定结果到 __r,
// assertJSAsync 在事件循环跑完后读取 —— 顺序与 V8 逐一对齐。

// 顶层脚本: 同步代码全部先于已结算 promise 的 then 回调; 链式 then 与
// 多条链之间按注册序交错 (FIFO)。
func TestMicrotaskOrderTopLevel(t *testing.T) {
	assertJSAsync(t, `
var out = [];
Promise.resolve().then(() => out.push(1));
out.push(2);
Promise.resolve().then(() => out.push(3)).then(() => out.push(4));
Promise.resolve().then(() => { out.push(5); return Promise.resolve(6); }).then(v => out.push(v));
Promise.reject("e").catch(r => out.push("c" + r));
out.push(7);
setTimeout(() => { globalThis.__r = out.join(","); }, 0);
`, "2,7,1,3,5,ce,4,6")
}

// 宏任务 (setTimeout) 回调结束后、下一个宏任务前 drain 微任务。
func TestMicrotaskOrderAfterMacrotask(t *testing.T) {
	assertJSAsync(t, `
var out = [];
setTimeout(() => {
  out.push(8);
  Promise.resolve().then(() => out.push(9));
  out.push(10);
}, 0);
setTimeout(() => { globalThis.__r = out.join(","); }, 5);
`, "8,10,9")
}

// async 函数的 await 恢复也走微任务: await 挂起后同步代码先跑完,
// 每次 await 恢复都是一个独立 job (顺序与 V8 实测一致)。
func TestMicrotaskOrderAsyncAwait(t *testing.T) {
	assertJSAsync(t, `
var out = [];
async function f() { out.push(2); await 1; out.push(4); await Promise.resolve(); out.push(6); }
f().then(() => out.push(7));
out.push(1);
Promise.resolve().then(() => out.push(3)).then(() => out.push(5));
setTimeout(() => { globalThis.__r = out.join(","); }, 10);
`, "2,1,4,3,6,5,7")
}

// drain 期间新入队的 job 同轮继续执行 (request-queue-order 的核心断言):
// then 回调里 resolve 另一个已有监听者的 promise, 后者不得跨宏任务。
func TestMicrotaskOrderEnqueueDuringDrain(t *testing.T) {
	assertJSAsync(t, `
var out = [];
var r;
var p = new Promise(res => { r = res; });
p.then(v => out.push("b" + v));
Promise.resolve().then(() => { out.push("a"); r(1); });
setTimeout(() => { globalThis.__r = out.join(","); }, 0);
`, "a,b1")
}
