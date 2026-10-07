package vm

import "testing"

// TestFunctionLengthExpectedArgumentCount 覆盖函数对象 length = 规范的
// ExpectedArgumentCount (ECMA-262 §15.1.4 / §10.2.11 SetFunctionLength):
// 从左数形参, 遇到第一个带默认值或 rest 的形参即停; rest 本身不计入;
// 解构形参无默认值时算 1 个。
//
// 每个用例的 JS 片段返回实际 length 的数值 (或描述符事实), 直接比对 ——
// 失败时能看出实际值而非仅 "应为真"。
func TestFunctionLengthExpectedArgumentCount(t *testing.T) {
	cases := []struct {
		name string
		js   string // 求值为 Number: 实际 length
		want float64
	}{
		// 无默认值: 全部形参计入。
		{"no-args", `(function(){ return (function(){}).length })()`, 0},
		{"plain-two", `(function(){ return (function(a, b){}).length })()`, 2},
		// 中间有默认值: 数到默认值前停。
		{"mid-default", `(function(){ return (function(a, b = 1, c){}).length })()`, 1},
		// 首参有默认值: length = 0。
		{"first-default", `(function(){ return (function(a = 1, b){}).length })()`, 0},
		// rest 本身不计入, 之前形参计入。
		{"rest", `(function(){ return (function(a, ...r){}).length })()`, 1},
		{"only-rest", `(function(){ return (function(...r){}).length })()`, 0},
		// rest 之前已有默认值: 默认值处停。
		{"default-then-rest", `(function(){ return (function(a, b = 1, ...r){}).length })()`, 1},
		// 解构形参无默认值算 1 个。
		{"destructure-array", `(function(){ return (function([a, b]){}).length })()`, 1},
		{"destructure-object", `(function(){ return (function({x}){}).length })()`, 1},
		{"destructure-two", `(function(){ return (function([a], {x}){}).length })()`, 2},
		// 解构形参带默认值: 该处即停 (length = 前面个数)。
		{"destructure-default", `(function(){ return (function([a] = [], c){}).length })()`, 0},
		{"destructure-obj-default", `(function(){ return (function({x} = {}, c){}).length })()`, 0},
		// 方法与箭头函数同规则。
		{"arrow", `(function(){ const f = (a, b = 2, ...r) => {}; return f.length })()`, 1},
		{"object-method", `(function(){ const o = { m(a, b = 1, c){} }; return o.m.length })()`, 1},
		{"class-method", `(function(){ class C { m(a, ...r){} } return C.prototype.m.length })()`, 1},
		{"class-static", `(function(){ class C { static s(a, b = 1){} } return C.s.length })()`, 1},
		// 各函数种类。
		{"async", `(function(){ async function f(a, b = 1){} return f.length })()`, 1},
		{"generator", `(function(){ function* f(a, b = 1){} return f.length })()`, 1},
		{"async-generator", `(function(){ async function* f(a, ...r){} return f.length })()`, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertNumber(t, evalWithStdlib(t, tc.js), tc.want)
		})
	}
}

// TestFunctionLengthDescriptor 覆盖 length 自有属性的属性描述符:
// { writable:false, enumerable:false, configurable:true } (ECMA-262 §10.2.11)。
func TestFunctionLengthDescriptor(t *testing.T) {
	js := `
	(function(){
		const f = function(a, b = 1, c){};
		const d = Object.getOwnPropertyDescriptor(f, "length");
		return "" + d.writable + "," + d.enumerable + "," + d.configurable;
	})()`
	assertString(t, evalWithStdlib(t, js), "false,false,true")
}
