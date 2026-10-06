package vm

import "testing"

// 本文件锁定 Function.prototype.call/apply/bind 的「运行时 this」语义。
//
// 回归点 (rFvpFz 真根因): 这三个方法曾是**无 this 的 BuiltinFunction**，把
// GetProperty 的查找接收者词法捕获进闭包。普通方法调用 `f.call(x)` 恰好让
// 查找接收者等于目标 f，于是看不出问题；一旦方法被间接使用，目标就错位:
//
//	Function.prototype.call.bind(Object.prototype.hasOwnProperty)
//
// 会把手写 call 的目标当成 Function.prototype 而非 hasOwnProperty，
// 组合出的函数恒返回 undefined。test262 的 harness/propertyHelper.js 正是
// 用这个组合捕获 `__hasOwnProperty` / `__propertyIsEnumerable`，
// 于是 verifyProperty 全线失效，拖垮 language/**/dstr 一票用例。
//
// 修复后这些方法改为 BuiltinMethod (感知运行时 this)，目标由 this 决定。

// TestCallBindComposition: Function.prototype.call.bind(target) 组合。
func TestCallBindComposition(t *testing.T) {
	// propertyHelper.js 捕获 __hasOwnProperty 的原始写法
	assertJS(t, `(function(){
		var __hasOwnProperty = Function.prototype.call.bind(Object.prototype.hasOwnProperty);
		return __hasOwnProperty({a:1}, "a");
	})()`, "true")
	assertJS(t, `(function(){
		var __hasOwnProperty = Function.prototype.call.bind(Object.prototype.hasOwnProperty);
		return __hasOwnProperty({a:1}, "b");
	})()`, "false")
	// propertyHelper.js 捕获 __propertyIsEnumerable 的原始写法
	assertJS(t, `(function(){
		var __pie = Function.prototype.call.bind(Object.prototype.propertyIsEnumerable);
		return __pie({a:1}, "a");
	})()`, "true")
	// 组合出的函数也应能被再次 .call / .apply
	assertJS(t, `(function(){
		var __hop = Function.prototype.call.bind(Object.prototype.hasOwnProperty);
		return __hop.call(null, {a:1}, "a");
	})()`, "true")
}

// TestCallCallIndirect: 显式 call.call / apply.call 链的目标转发。
func TestCallCallIndirect(t *testing.T) {
	assertJS(t, `(function(){
		var hop = Object.prototype.hasOwnProperty;
		return Function.prototype.call.call(hop, {a:1}, "a");
	})()`, "true")
	assertJS(t, `(function(){
		var hop = Object.prototype.hasOwnProperty;
		return Function.prototype.apply.call(hop, {a:1}, ["a"]);
	})()`, "true")
	// 取出方法后再以显式 call 赋予接收者
	assertJS(t, `(function(){
		var call = Function.prototype.call;
		return call.call(function(v){ return this.x + v }, {x:10}, 5);
	})()`, "15")
}

// TestCallApplyDirectUnaffected: 直接方法调用路径不得回归。
func TestCallApplyDirectUnaffected(t *testing.T) {
	assertNumber(t, evalJS(t, `(function(a,b){ return a+b }).call(null, 1, 2)`), 3)
	assertNumber(t, evalJS(t, `(function(a,b){ return a+b }).apply(null, [3,4])`), 7)
	// 原生方法上的 call/apply 仍工作
	assertNumber(t, evalJS(t, `let a = [1,2]; Array.prototype.push.call(a, 3); a.length`), 3)
	// 严格接收者检查仍抛 TypeError
	assertJSThrows(t, `Array.prototype.push.call(null, 1)`, "TypeError")
}

// TestBoundFunctionNameAndLength: 绑定函数的 name / length 按规范。
func TestBoundFunctionNameAndLength(t *testing.T) {
	assertJS(t, `(function foo(a, b){}).bind(null).name`, "bound foo")
	assertJS(t, `(function foo(a, b){}).bind(null, 1).name`, "bound foo")
	assertJS(t, `(function foo(a, b){}).bind(null, 1, 2).name`, "bound foo")
	// length = 目标 length - 前置参数个数 (下限 0)
	assertNumber(t, evalJS(t, `(function foo(a, b){}).bind(null).length`), 2)
	assertNumber(t, evalJS(t, `(function foo(a, b){}).bind(null, 1).length`), 1)
	assertNumber(t, evalJS(t, `(function foo(a, b){}).bind(null, 1, 2).length`), 0)
	assertNumber(t, evalJS(t, `(function foo(a, b){}).bind(null, 1, 2, 3).length`), 0)
}
