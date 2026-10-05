package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 函数对象 [[Prototype]] 建模 (rmdv40 缺口 1) =====
//
// 此前函数对象没有建模 [[Prototype]]: Object.getPrototypeOf(fn) 对闭包返回
// null，对内置函数也返回 null；函数种类 (普通 / 生成器 / async / async generator)
// 也无法从原型链上区分。本组用例锁定函数对象 [[Prototype]] 按种类指向正确的
// 内建原型，且与实例侧的 .prototype 严格区分。
//
// 期望值均以 Node 22 实测为准 (语义权威)。

// evalBoolStmts 执行多语句脚本，要求其完成值为布尔并断言。
func evalBoolStmts(t *testing.T, src string, expected bool) {
	t.Helper()
	got := evalWithStdlib(t, src)
	b, ok := got.(*object.Boolean)
	if !ok {
		t.Fatalf("expected Boolean from %q, got %T (%v)", src, got, got)
	}
	if b.Value != expected {
		t.Fatalf("src %q: expected %v, got %v", src, expected, b.Value)
	}
}

// 普通函数 / 箭头函数 / 方法 / 类构造器的 [[Prototype]] 都是 %Function.prototype%。
func TestFuncProtoPlainKinds(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf(function(){}) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(() => ({})) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(({ m(){} }).m) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(class {}) === Function.prototype`, "true")
}

// 生成器函数: [[Prototype]] = %GeneratorFunction.prototype%，
// %GeneratorFunction.prototype%.[[Prototype]] = %Function.prototype%，
// %GeneratorFunction%.[[Prototype]] = %Function%。
func TestFuncProtoGeneratorFunction(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf(function*(){}) === Object.getPrototypeOf(function*(){}).constructor.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(function*(){})) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(function*(){}).constructor) === Function`, "true")
	assertJS(t, `Object.getPrototypeOf(function*(){}).constructor.name`, "GeneratorFunction")
}

// async 函数: [[Prototype]] = %AsyncFunction.prototype%。
func TestFuncProtoAsyncFunction(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf(async function(){}) === Object.getPrototypeOf(async function(){}).constructor.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(async function(){})) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(async function(){}).constructor) === Function`, "true")
	assertJS(t, `Object.getPrototypeOf(async function(){}).constructor.name`, "AsyncFunction")
}

// async generator 函数: [[Prototype]] = %AsyncGeneratorFunction.prototype%。
func TestFuncProtoAsyncGeneratorFunction(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf(async function*(){}) === Object.getPrototypeOf(async function*(){}).constructor.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(async function*(){})) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Object.getPrototypeOf(async function*(){}).constructor) === Function`, "true")
	assertJS(t, `Object.getPrototypeOf(async function*(){}).constructor.name`, "AsyncGeneratorFunction")
}

// 内置函数的 [[Prototype]] 也是 %Function.prototype%。
func TestFuncProtoBuiltins(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf(Math.abs) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Array) === Function.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(Function) === Function.prototype`, "true")
}

// %Function.prototype% 自身: 可调用、[[Prototype]] = %Object.prototype%。
func TestFunctionPrototypeSelf(t *testing.T) {
	assertJS(t, `typeof Function.prototype`, "function")
	assertJS(t, `Object.getPrototypeOf(Function.prototype) === Object.prototype`, "true")
	assertJS(t, `Function.prototype()`, "undefined")
}

// 普通函数可通过 [[Prototype]] 取到 .constructor。
func TestFunctionConstructorFallback(t *testing.T) {
	assertJS(t, `(function(){}).constructor === Function`, "true")
	assertJS(t, `(() => ({})).constructor === Function`, "true")
}

// 边界: 函数的 .prototype (实例侧) 与 [[Prototype]] (函数自身) 是两回事。
func TestFuncPrototypeVsInstancePrototype(t *testing.T) {
	evalBoolStmts(t, `
		function f(){}
		f.__marker = 1;
		Object.getPrototypeOf(f) !== f.prototype;
	`, true)
	// 普通函数实例原型的 [[Prototype]] 是 Object.prototype。
	assertJS(t, `Object.getPrototypeOf((function(){}).prototype) === Object.prototype`, "true")
}

// async 函数(非生成器)与箭头函数没有 .prototype 属性。
func TestNoPrototypeForAsyncAndArrow(t *testing.T) {
	assertJS(t, `(async function(){}).prototype`, "undefined")
	assertJS(t, `(() => ({})).prototype`, "undefined")
	// async generator 与 generator 都有 .prototype。
	assertJS(t, `typeof (function*(){}).prototype`, "object")
	assertJS(t, `typeof (async function*(){}).prototype`, "object")
}

// 生成器 / async generator 的实例原型 [[Prototype]] 指向对应 *Prototype 内建。
func TestGeneratorInstancePrototypeChain(t *testing.T) {
	assertJS(t, `Object.getPrototypeOf((function*(){}).prototype) === Object.getPrototypeOf(function*(){}).prototype`, "true")
	assertJS(t, `Object.getPrototypeOf((async function*(){}).prototype) === Object.getPrototypeOf(async function*(){}).prototype`, "true")
}

// instanceof 沿函数对象 [[Prototype]] 链工作。
func TestInstanceofFunctionConstructors(t *testing.T) {
	evalBoolStmts(t, `function* g(){}; g instanceof Object.getPrototypeOf(g).constructor;`, true)
	evalBoolStmts(t, `function* g(){}; g instanceof Function;`, true)
	evalBoolStmts(t, `async function f(){}; f instanceof Function;`, true)
	evalBoolStmts(t, `async function* f(){}; f instanceof Function;`, true)
	assertJS(t, `Function instanceof Function`, "true")
}

// Reflect.getPrototypeOf 与 Object.getPrototypeOf 口径一致。
func TestReflectGetPrototypeOfFunction(t *testing.T) {
	assertJS(t, `Reflect.getPrototypeOf(function(){}) === Function.prototype`, "true")
	assertJS(t, `Reflect.getPrototypeOf(function*(){}) === Object.getPrototypeOf(function*(){}).constructor.prototype`, "true")
}
