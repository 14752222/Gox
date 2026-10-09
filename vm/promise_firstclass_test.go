package vm

// ===== Promise 提为一等对象: JS 侧可观测契约 (看板 rj9MwH) =====
//
// 此前 *Promise 的 GetProperty 只认 then/catch/finally, SetProperty 是空函数,
// 也没有自有属性存储。用户往 promise 实例上 defineProperty / 赋值 / 取原型
// 全都是"看不见"的 —— 规范里靠"取 constructor 抛错"驱动的分支
// (PromiseResolve 步骤 1.a) 在 Gox 里走不到。
//
// 本文件钉住 JS 侧的可观测面; 描述符存取的机制本身在
// object/promise_ownprop_test.go。

import "testing"

// TestPromiseDefinePropertyGetterThrows defineProperty 落得下去, 且取该属性会
// 触发 getter —— 抛出值必须原样冒出来 (broken-promise 三例靠的就是这条)。
func TestPromiseDefinePropertyGetterThrows(t *testing.T) {
	src := `
var p = Promise.resolve(1);
Object.defineProperty(p, 'constructor', {
  get: function() { throw new Error('broken promise'); },
  configurable: true
});
var got;
try {
  p.constructor;
  got = 'no-throw';
} catch (e) {
  got = e.message;
}
got;
`
	assertString(t, evalJS(t, src), "broken promise")
}

// TestPromiseOwnPropertyDescriptorRoundTrip defineProperty / getOwnPropertyDescriptor
// 在实例上往返, 且标志位如实。
func TestPromiseOwnPropertyDescriptorRoundTrip(t *testing.T) {
	src := `
var p = Promise.resolve(1);
Object.defineProperty(p, 'x', { value: 42, writable: true, enumerable: true, configurable: true });
var d = Object.getOwnPropertyDescriptor(p, 'x');
[d.value, d.writable, d.enumerable, d.configurable].join(',');
`
	assertString(t, evalJS(t, src), "42,true,true,true")
}

// TestPromiseOwnKeysAndHasOwnProperty 自有键参与 Object.keys /
// getOwnPropertyNames / hasOwnProperty, 且**不**污染原型上的键。
func TestPromiseOwnKeysAndHasOwnProperty(t *testing.T) {
	src := `
var p = Promise.resolve(1);
Object.defineProperty(p, 'own', { value: 1, enumerable: true, configurable: true });
Object.defineProperty(p, 'hidden', { value: 2, enumerable: false, configurable: true });
[
  Object.keys(p).join('|'),
  Object.getOwnPropertyNames(p).join('|'),
  p.hasOwnProperty('own'),
  p.hasOwnProperty('then'),
  p.own
].join(' ; ');
`
	// then 在原型上, 不是自有键; own 可读到 1。
	assertString(t, evalJS(t, src), "own ; own|hidden ; true ; false ; 1")
}

// TestPromiseAssignmentAndDelete 赋值落成自有数据属性, delete 能删掉可配置的。
func TestPromiseAssignmentAndDelete(t *testing.T) {
	src := `
var p = Promise.resolve(1);
p.foo = 'bar';
var before = p.foo;
var deleted = delete p.foo;
[before, deleted, p.foo === undefined, Object.keys(p).length].join(',');
`
	assertString(t, evalJS(t, src), "bar,true,true,0")
}

// TestPromiseProtoChain 原型链通道打通: getPrototypeOf 拿到
// %Promise.prototype%, 原型上的 then/constructor 通过实例可访问。
func TestPromiseProtoChain(t *testing.T) {
	src := `
var p = Promise.resolve(1);
[
  Object.getPrototypeOf(p) === Promise.prototype,
  p.constructor === Promise,
  typeof p.then,
  Object.prototype.toString.call(p)
].join(',');
`
	assertString(t, evalJS(t, src), "true,true,function,[object Promise]")
}

// TestPromiseThenStillWorksAfterOwnProps 加了自有属性之后, 原型上的 then 仍
// 然照常工作 (两级查找 不得 遮蔽原型方法)。
func TestPromiseThenStillWorksAfterOwnProps(t *testing.T) {
	src := `
var log = [];
var p = Promise.resolve(7);
p.tag = 'mine';
p.then(function(v) {
  log.push('tag=' + p.tag);
  log.push('v=' + v);
});
log.join(',');
`
	assertString(t, evalJS(t, src), "tag=mine,v=7")
}

// TestPromiseRejectReasonKeepsOwnProps 一等对象化不得干扰结算语义: 带自有属性
// 的 reason 原样传到 catch (不换皮、不丢自有键)。
//
// 用普通对象而非 Error: Error 实例目前还不是一等属性存储
// (Object.defineProperty(new Error(), …) 会抛), 那是另一个缺口, 不在本任务内。
func TestPromiseRejectReasonKeepsOwnProps(t *testing.T) {
	src := `
var got = 'unset';
var reason = { code: 'E1' };
Object.defineProperty(reason, 'extra', { value: 1, enumerable: false, configurable: true });
Promise.reject(reason).catch(function(r) {
  got = [r === reason, r.code, r.extra].join(',');
});
got;
`
	assertString(t, evalJS(t, src), "true,E1,1")
}
