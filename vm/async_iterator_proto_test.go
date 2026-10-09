package vm

import (
	"testing"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/stdlib"
)

// ===== %AsyncIteratorPrototype% (看板 rGmSsi) =====
//
// 它是异步迭代器原型链的**顶端**: %AsyncGeneratorPrototype% 的
// [[Prototype]] 指向它, 上面挂着两个语义成员:
//   - @@asyncIterator: 返回 this (「我自己就是可迭代对象」)
//   - @@asyncDispose:  await using 的释放入口, 调用 O.return() 并把结果
//     折成一个 Promise (永不同步抛)
//
// 装配前 Gox 里根本没有这个对象 —— `Object.getPrototypeOf(Object
// .getPrototypeOf(gen.prototype))` 拿到的是 %Object.prototype%。
//
// 口径说明: 这两条成员来自 explicit-resource-management 提案, **Node v22
// 也还没有**, 所以基准不是 Node 而是提案文本 + test262 用例。本文件断言
// 的形状与 test262 built-ins/AsyncIteratorPrototype/* 13 例一一对应。

// probeSink 收集脚本通过 __probe(...) 回传的观测值。
//
// @@asyncDispose 的结算发生在微任务里, 同步拿不到结果; 用 Go 侧内建把
// 结果接出来, 比"在 .then 里断言"可靠 —— 后者失败时只会变成一个静默的
// unhandled rejection。
type probeSink struct {
	args []object.Value
}

func (s *probeSink) last() object.Value {
	if len(s.args) == 0 {
		return nil
	}
	return s.args[len(s.args)-1]
}

// evalWithProbe 在 stdlib 全局环境里挂一个 __probe 内建并执行 src,
// 跑完定时器后返回收集到的观测值。
func evalWithProbe(t *testing.T, src string) *probeSink {
	t.Helper()
	l := lexer.New(src)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors:\n%s", p.Errors().String())
	}
	c := compiler.New()
	if err := c.Compile(program); err != nil {
		t.Fatalf("compiler error: %v", err)
	}

	sink := &probeSink{}
	env := stdlib.SetupGlobals()
	env.Declare("__probe", object.NewBuiltin("__probe", func(args ...object.Value) object.Value {
		sink.args = append(sink.args, args...)
		return object.UndefinedSingleton
	}), false)

	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), env)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}
	// 结算 @@asyncDispose 交回的 Promise (微任务 / 定时器队列)。
	if err := vm.RunTimers(); err != nil {
		t.Fatalf("RunTimers error: %v", err)
	}
	return sink
}

// aipSrc 取 %AsyncIteratorPrototype% 的标准写法 (与 test262 一致):
// 实例 → fn.prototype → AGP → AIP。
const aipSrc = `
	async function* gen() {}
	const AIP = Object.getPrototypeOf(Object.getPrototypeOf(gen.prototype));
`

// TestAsyncIteratorPrototypeIsAssembled AIP 必须在链上, 且不是 Object.prototype。
func TestAsyncIteratorPrototypeIsAssembled(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		__probe(typeof AIP);
		__probe(AIP !== Object.prototype);
		__probe(typeof AIP[Symbol.asyncIterator]);
		__probe(typeof AIP[Symbol.asyncDispose]);
	`)
	want := []string{"object", "true", "function", "function"}
	if len(sink.args) != len(want) {
		t.Fatalf("期望 %d 个观测值, 实得 %d", len(want), len(sink.args))
	}
	for i, w := range want {
		if got := sink.args[i].Inspect(); got != w {
			t.Fatalf("观测值 #%d: 期望 %s, 实得 %s", i, w, got)
		}
	}
}

// TestAsyncIteratorPrototypeSymbolAsyncIterator @@asyncIterator 原样返回 this。
func TestAsyncIteratorPrototypeSymbolAsyncIterator(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		const get = AIP[Symbol.asyncIterator];
		__probe(get.call({}) !== undefined);   // 原样返回了那个对象
		const o = {};
		__probe(get.call(o) === o);
		__probe(get.call(4) === 4);
		__probe(get.call(undefined) === undefined);
	`)
	want := []string{"true", "true", "true", "true"}
	for i, w := range want {
		if got := sink.args[i].Inspect(); got != w {
			t.Fatalf("观测值 #%d: 期望 %s, 实得 %s", i, w, got)
		}
	}
}

// TestAsyncIteratorPrototypeAsyncDisposeReturnsPromise 返回值必须是 Promise。
func TestAsyncIteratorPrototypeAsyncDisposeReturnsPromise(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		__probe(AIP[Symbol.asyncDispose]() instanceof Promise);
	`)
	assertBooleanValue(t, sink.last(), true)
}

// TestAsyncIteratorPrototypeAsyncDisposeInvokesReturn 调用 O.return(), 零参数。
func TestAsyncIteratorPrototypeAsyncDisposeInvokesReturn(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		const iter = Object.create(AIP);
		let called = false, argc = -1;
		iter.return = async function () { called = true; argc = arguments.length; return { done: true }; };
		AIP[Symbol.asyncDispose].call(iter).then(() => __probe("called=" + called + " argc=" + argc));
	`)
	assertString(t, sink.last(), "called=true argc=0")
}

// TestAsyncIteratorPrototypeAsyncDisposeNoReturn 没有 return 方法时兑现 undefined。
func TestAsyncIteratorPrototypeAsyncDisposeNoReturn(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		AIP[Symbol.asyncDispose].call({}).then(v => __probe("resolved:" + String(v)));
	`)
	assertString(t, sink.last(), "resolved:undefined")
}

// TestAsyncIteratorPrototypeAsyncDisposeRejectsWithThrownValue
// return 抛错 ⇒ 以**原始抛出值** reject (不是新造的 Error)。
//
// 这条对应 r6OWbQ 的口径: 非 Error 抛出值必须原样传出, 否则
// `assert.throwsAsync(CatchError)` 这类按构造函数断言的用例过不去。
func TestAsyncIteratorPrototypeAsyncDisposeRejectsWithThrownValue(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		function CatchError() {}
		const obj = { return() { throw new CatchError(); } };
		AIP[Symbol.asyncDispose].call(obj).then(
			() => __probe("不该兑现"),
			e => __probe("rejected instanceof=" + (e instanceof CatchError)),
		);
	`)
	assertString(t, sink.last(), "rejected instanceof=true")
}

// TestAsyncIteratorPrototypeAsyncDisposeGetterThrows
// return 的 **getter** 抛错 ⇒ 同样以原始值 reject (GetMethod 中断)。
func TestAsyncIteratorPrototypeAsyncDisposeGetterThrows(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		function CatchError() {}
		let gets = 0;
		const obj = { get return() { gets++; throw new CatchError(); } };
		AIP[Symbol.asyncDispose].call(obj).then(
			() => __probe("不该兑现"),
			e => __probe("rejected instanceof=" + (e instanceof CatchError) + " gets=" + gets),
		);
	`)
	assertString(t, sink.last(), "rejected instanceof=true gets=1")
}

// TestAsyncIteratorPrototypeAsyncDisposeRejectedPromise
// return 返回被 reject 的 Promise ⇒ reason 原样传出。
func TestAsyncIteratorPrototypeAsyncDisposeRejectedPromise(t *testing.T) {
	sink := evalWithProbe(t, aipSrc+`
		function CatchError() {}
		const obj = { return() { return Promise.reject(new CatchError()); } };
		AIP[Symbol.asyncDispose].call(obj).then(
			() => __probe("不该兑现"),
			e => __probe("rejected instanceof=" + (e instanceof CatchError)),
		);
	`)
	assertString(t, sink.last(), "rejected instanceof=true")
}

// assertBooleanValue 断言布尔值 (避免与既有 assertBoolean 的签名冲突)。
func assertBooleanValue(t *testing.T, obj object.Value, expected bool) {
	t.Helper()
	b, ok := obj.(*object.Boolean)
	if !ok {
		t.Fatalf("expected Boolean, got %T (%s)", obj, obj.Inspect())
	}
	if b.Value != expected {
		t.Fatalf("expected %v, got %v", expected, b.Value)
	}
}
