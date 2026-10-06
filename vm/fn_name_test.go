package vm

import "testing"

// ===== SetFunctionName (函数名推断) 回归测试 =====
//
// 锁定 NamedEvaluation 的正反两面:
//   - 该命名的形态 (变量/常量声明初始化、赋值给标识符、对象字面量属性/方法、
//     解构默认值、类) 要得到正确的 name;
//   - 不该命名的形态 (成员赋值右侧、实参、下标、逗号表达式、裸括号表达式)
//     必须保持 ""。误命名的危害大于漏命名, 故正反两面都在此钉住。

func TestFnNameVariableDeclarations(t *testing.T) {
	assertJS(t, `(function(){var f = function(){};return f.name;})()`, "f")
	assertJS(t, `(function(){let f = function(){};return f.name;})()`, "f")
	assertJS(t, `(function(){const f = function(){};return f.name;})()`, "f")
	// 括号透明: (function(){}) 仍是匿名函数定义
	assertJS(t, `(function(){var f = (function(){});return f.name;})()`, "f")
	// 箭头 / 生成器 / async 同样按绑定名命名
	assertJS(t, `(function(){var f = () => {};return f.name;})()`, "f")
	assertJS(t, `(function(){var f = function*(){};return f.name;})()`, "f")
	assertJS(t, `(function(){var f = async function(){};return f.name;})()`, "f")
}

func TestFnNameIdentifierAssignment(t *testing.T) {
	assertJS(t, `(function(){var f;f = function(){};return f.name;})()`, "f")
	assertJS(t, `(function(){var a;a = () => {};return a.name;})()`, "a")
	assertJS(t, `(function(){var g;g = function*(){};return g.name;})()`, "g")
}

func TestFnNameObjectLiteral(t *testing.T) {
	assertJS(t, `(function(){var o = {m: function(){}};return o.m.name;})()`, "m")
	assertJS(t, `(function(){var o = {m(){}};return o.m.name;})()`, "m")
	assertJS(t, `(function(){var o = {m: () => {}};return o.m.name;})()`, "m")
	assertJS(t, `(function(){var o = {p: (function(){})};return o.p.name;})()`, "p")
}

func TestFnNameDestructuringDefaults(t *testing.T) {
	assertJS(t, `(function(){var a = [];var [x = function(){}] = a;return x.name;})()`, "x")
	assertJS(t, `(function(){var o = {};var {y = function(){}} = o;return y.name;})()`, "y")
	assertJS(t, `(function(){var x;var a=[];[x = function(){}] = a;return x.name;})()`, "x")
	// 参数默认值
	assertJS(t, `(function(){return (function(p = function(){}){return p.name;})();})()`, "p")
}

func TestFnNameClassExpression(t *testing.T) {
	// 具名/匿名类表达式与类声明的 name
	assertJS(t, `(function(){var c = class {};return c.name;})()`, "c")
	assertJS(t, `(function(){var c = class X {};return c.name;})()`, "X")
	assertJS(t, `(function(){class X{};return X.name;})()`, "X")
}

// TestFnNameNotAssigned 锁定"不该有名字"的形态: 一律保持 ""。
func TestFnNameNotAssigned(t *testing.T) {
	// 裸匿名表达式
	assertJS(t, `(function(){return (function(){}).name;})()`, "")
	assertJS(t, `(function(){return (() => {}).name;})()`, "")
	// 逗号表达式 (匿名函数定义不穿透)
	assertJS(t, `(function(){var f = (0, function(){});return f.name;})()`, "")
	// 成员赋值右侧 (IsIdentifierRef 为假, 不命名)
	assertJS(t, `(function(){var o={};o.k = function(){};return o.k.name;})()`, "")
	assertJS(t, `(function(){var o={};o["k"] = function(){};return o["k"].name;})()`, "")
	// 实参与下标
	assertJS(t, `(function(){return [function(){}][0].name;})()`, "")
}

// TestFnNamePropertyDescriptor 锁定 name 是 {writable:false, enumerable:false,
// configurable:true} 的自有属性。
func TestFnNamePropertyDescriptor(t *testing.T) {
	assertJS(t, `(function(){var d = Object.getOwnPropertyDescriptor(function(){}, 'name');
		return d.writable + '/' + d.enumerable + '/' + d.configurable + '/' + d.value;})()`,
		"false/false/true/")
	assertJS(t, `Object.prototype.hasOwnProperty.call(function(){}, 'name')`, "true")
	assertJS(t, `(function(){var f = function(){};
		return Object.prototype.hasOwnProperty.call(f, 'name') + ':' + f.name;})()`, "true:f")
}
