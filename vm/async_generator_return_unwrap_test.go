package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== AsyncGenerator.prototype.return 的值要过 PromiseResolve (看板 r6e5qp) =====
//
// 规范 AsyncGeneratorAwaitReturn 步骤 6:
//
//	Let promise be Completion(PromiseResolve(%Promise%, completion.[[Value]])).
//
// 之后再 PerformPromiseThen 把**解包后**的值放进 iterResult。故
// `it.return(somePromise)` 拿到的是解包后的值, 而不是 promise 本身。
//
// 本次改动把解包**只**加在 return 请求上, 故下面的反向不变式断言
// throw 的完成值原样 reject (不过 PromiseResolve)。
//
// 另注: async generator 里 `yield somePromise` 的产出在 Node v22 实测是
// **会**被解包的 (async generator 的 yield 操作数走 await 语义), 这与
// `yield*` 委托同步源时不解包 (test262 yield-star-promise-not-unwrapped)
// 是两回事, 别混为一谈。
//
// 基准: Node v22。对应 test262
// built-ins/AsyncGeneratorPrototype/return/return-{suspendedStart,suspendedYield}-promise.js

// TestAsyncGeneratorReturnUnwrapsPromiseValue 断言 return 的 Promise 值被解包。
func TestAsyncGeneratorReturnUnwrapsPromiseValue(t *testing.T) {
	src := `
// suspendedStart: 生成器还没启动, return 直接关闭它, 值要解包
var g1 = async function*() {};
var it1 = g1();
var resolve1;
var p1 = new Promise(function(r) { resolve1 = r; });
it1.return(p1).then(function(ret) {
  __probe(ret.value);
  __probe(ret.done);
});
resolve1('unwrapped-value');

// suspendedYield: 生成器停在 yield, return 也要解包
var g2 = async function*() { yield 1; };
var it2 = g2();
var resolve2;
var p2 = new Promise(function(r) { resolve2 = r; });
it2.next().then(function() {
  it2.return(p2).then(function(ret) {
    __probe(ret.value);
    __probe(ret.done);
  });
});
resolve2('unwrapped-2');
`
	sink := evalWithProbe(t, src)
	if len(sink.args) != 4 {
		t.Fatalf("期望 4 个观测值, 实得 %d (%v)", len(sink.args), sink.args)
	}
	// [suspendedStart value, suspendedStart done, suspendedYield value, suspendedYield done]
	want := []any{"unwrapped-value", true, "unwrapped-2", true}
	for i, w := range want {
		got := sink.args[i]
		switch wv := w.(type) {
		case string:
			s, ok := got.(*object.String)
			if !ok || s.Value != wv {
				t.Errorf("观测值 %d: 期望字符串 %q, 实得 %v", i, wv, got)
			}
		case bool:
			b, ok := got.(*object.Boolean)
			if !ok || b.Value != wv {
				t.Errorf("观测值 %d: 期望布尔 %v, 实得 %v", i, wv, got)
			}
		}
	}
}

// TestAsyncGeneratorThrowDoesNotUnwrapPromise 断言本次改动的**反向不变式**:
// 解包只加在 return 请求上, throw 的完成值必须**原样** reject (规范
// AsyncGeneratorCompleteStep 对 throw completion 直接 reject
// completion.[[Value]], 不过 PromiseResolve)。
//
// 基准: Node v22 实测 —— `it.throw(pr)` 的 rejection reason === pr 本身。
func TestAsyncGeneratorThrowDoesNotUnwrapPromise(t *testing.T) {
	src := `
var pr = Promise.resolve('keep-throw');
var g = async function*() { yield 1; };
var it = g();
it.next().then(function() {
  it.throw(pr).then(
    function() { __probe('fulfilled'); },
    function(err) { __probe(err === pr); }
  );
});
`
	sink := evalWithProbe(t, src)
	if len(sink.args) != 1 {
		t.Fatalf("期望 1 个观测值, 实得 %d", len(sink.args))
	}
	b, ok := sink.args[0].(*object.Boolean)
	if !ok || !b.Value {
		t.Errorf("throw 的 promise 不得解包: 期望 reason === pr 为 true, 实得 %v", sink.args[0])
	}
}

// TestAsyncGeneratorReturnNonPromiseUnchanged 断言非 Promise 的 return 值
// 原样透传 (不被解包逻辑误伤)。
func TestAsyncGeneratorReturnNonPromiseUnchanged(t *testing.T) {
	src := `
var g = async function*() {};
var it = g();
it.return(42).then(function(ret) {
  __probe(ret.value);
  __probe(ret.done);
});
`
	sink := evalWithProbe(t, src)
	if len(sink.args) != 2 {
		t.Fatalf("期望 2 个观测值, 实得 %d", len(sink.args))
	}
	if n, ok := sink.args[0].(*object.Number); !ok || n.Value != 42 {
		t.Errorf("期望 42, 实得 %v", sink.args[0])
	}
	if b, ok := sink.args[1].(*object.Boolean); !ok || !b.Value {
		t.Errorf("期望 done=true, 实得 %v", sink.args[1])
	}
}

// TestAsyncGeneratorReturnRejectedPromiseRejects 断言 return 的 Promise 被
// reject 时, it.return() 的结果也 reject (解包路径的 reject 分支)。
func TestAsyncGeneratorReturnRejectedPromiseRejects(t *testing.T) {
	src := `
var g = async function*() {};
var it = g();
var pr = Promise.reject(new Error('boom'));
it.return(pr).then(
  function() { __probe('fulfilled'); },
  function(err) { __probe('rejected:' + err.message); }
);
`
	sink := evalWithProbe(t, src)
	if len(sink.args) != 1 {
		t.Fatalf("期望 1 个观测值, 实得 %d", len(sink.args))
	}
	if s, ok := sink.args[0].(*object.String); !ok || s.Value != "rejected:boom" {
		t.Errorf("期望 rejected:boom, 实得 %v", sink.args[0])
	}
}
