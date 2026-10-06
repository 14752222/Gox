package vm

// with 语句 (sloppy 动态作用域) 的运行时语义测试。
//
// 覆盖看板单 rlXn83 的验收点: 读写命中 with 对象 / 穿透到外层 / 局部槽回退 /
// Symbol.unscopables / 嵌套 with / this 不受影响 / 对象表达式只求值一次 /
// 闭包捕获 with 环境 / 函数作用域与 delete 语义。

import (
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// TestWithReadWriteHit: with 体内读写先命中对象属性, 该对象被改写。
func TestWithReadWriteHit(t *testing.T) {
	res := evalJS(t, `
		var o = { x: 1 };
		var r;
		with (o) { x = 5; r = x; }
		r + o.x;
	`)
	assertNumber(t, res, 10)
}

// TestWithPenetratesOuter: 属性不存在时穿透到外层全局绑定。
func TestWithPenetratesOuter(t *testing.T) {
	res := evalJS(t, `
		var outer = 7;
		var o = {};
		with (o) { outer = outer + 1; }
		outer;
	`)
	assertNumber(t, res, 8)
}

// TestWithPenetratesLocal: 外层是函数局部绑定时, 未命中回退到局部槽位。
func TestWithPenetratesLocal(t *testing.T) {
	res := evalJS(t, `
		(function () {
			var local = 41;
			var o = {};
			with (o) { local = local + 1; }
			return local;
		})();
	`)
	assertNumber(t, res, 42)
}

// TestWithLocalShadowedByObject: 外层局部绑定与 with 对象同名时, 命中对象。
func TestWithLocalShadowedByObject(t *testing.T) {
	res := evalJS(t, `
		(function () {
			var v = "outer";
			var o = { v: "inner" };
			var r;
			with (o) { r = v; v = "changed"; }
			return r + "/" + o.v + "/" + v;
		})();
	`)
	assertString(t, res, "inner/changed/outer")
}

// TestWithUnscopables: Symbol.unscopables 为真的属性按不存在处理 (穿透外层)。
func TestWithUnscopables(t *testing.T) {
	res := evalJS(t, `
		var x = 0;
		var env = { x: 1 };
		env[Symbol.unscopables] = { x: true };
		var r;
		with (env) { r = x; }
		r;
	`)
	assertNumber(t, res, 0)
}

// TestWithUnscopablesFalsey: 非真值的 unscopables 属性不排除绑定。
func TestWithUnscopablesFalsey(t *testing.T) {
	res := evalJS(t, `
		var x = 0;
		var env = { x: 1 };
		env[Symbol.unscopables] = { x: false };
		var r;
		with (env) { r = x; }
		r;
	`)
	assertNumber(t, res, 1)
}

// TestWithUnscopablesNotReferencedWhenMissing: 对象没有该属性时不读 unscopables。
func TestWithUnscopablesNotReferencedWhenMissing(t *testing.T) {
	res := evalJS(t, `
		var x = 0;
		var env = {};
		var calls = 0;
		Object.defineProperty(env, Symbol.unscopables, {
			get: function () { calls += 1; return {}; }
		});
		with (env) { x; }
		calls;
	`)
	assertNumber(t, res, 0)
}

// TestWithNested: 嵌套 with, 内层未命中穿透到外层 with 对象。
func TestWithNested(t *testing.T) {
	res := evalJS(t, `
		var a = { x: 1 };
		var b = { y: 2 };
		var r1, r2;
		with (a) { with (b) { r1 = x; r2 = y; } }
		r1 + r2;
	`)
	assertNumber(t, res, 3)
}

// TestWithNestedInnerWins: 内外层同名属性时内层对象优先。
func TestWithNestedInnerWins(t *testing.T) {
	res := evalJS(t, `
		var a = { x: 1 };
		var b = { x: 2 };
		var r;
		with (a) { with (b) { r = x; } }
		r;
	`)
	assertNumber(t, res, 2)
}

// TestWithThisUnaffected: with 不改变 this 绑定。
func TestWithThisUnaffected(t *testing.T) {
	res := evalJS(t, `
		var o = { x: 1 };
		function f() {
			with (o) { return this.tag; }
		}
		f.call({ tag: "T" });
	`)
	assertString(t, res, "T")
}

// TestWithObjectEvaluatedOnce: 对象表达式只求值一次, 即使体内多次访问。
func TestWithObjectEvaluatedOnce(t *testing.T) {
	res := evalJS(t, `
		var n = 0;
		var o = { x: 1 };
		function get() { n = n + 1; return o; }
		with (get()) { x = x + 1; x = x + 1; }
		n * 100 + o.x;
	`)
	assertNumber(t, res, 103)
}

// TestWithDoesNotLeakIntoOuterClosure: 在 with 之外定义的函数即使被 with 体内
// 调用, 也看不到 with 对象 (环境按定义处捕获)。
func TestWithDoesNotLeakIntoOuterClosure(t *testing.T) {
	res := evalJS(t, `
		var p1 = 1;
		var myObj = { p1: "a" };
		var f = function () { p1 = "x1"; };
		with (myObj) { f(); }
		p1 + ":" + myObj.p1;
	`)
	assertString(t, res, "x1:a")
}

// TestWithClosureCaptures: 在 with 体内定义的函数捕获 with 环境, 之后调用仍
// 读该对象 (即使已退出 with)。
func TestWithClosureCaptures(t *testing.T) {
	res := evalJS(t, `
		var x = 1;
		var obj = { x: 2 };
		var probe;
		with (obj) { probe = function () { return x; }; }
		x = 5;
		probe();
	`)
	assertNumber(t, res, 2)
}

// TestWithLetShadowsObject: 语句体内 let 声明遮蔽 with 对象 (对象环境在内)。
func TestWithLetShadowsObject(t *testing.T) {
	res := evalJS(t, `
		var r;
		var o = { x: "obj" };
		with (o) { let x = "block"; r = x; }
		r + "/" + o.x;
	`)
	assertString(t, res, "block/obj")
}

// TestWithVarHoistsToFunction: with 不改变声明作用域 —— 体内 var 提升到函数层。
func TestWithVarHoistsToFunction(t *testing.T) {
	res := evalJS(t, `
		var o = {};
		var f = function () {
			with (o) { var foo = "12.10"; }
			return foo;
		};
		f();
	`)
	assertString(t, res, "12.10")
}

// TestWithDelete: delete 标识符在 with 体内删除对象属性。
func TestWithDelete(t *testing.T) {
	res := evalJS(t, `
		var o = { p: "a", keep: 1 };
		var d;
		with (o) { d = delete p; }
		d + ":" + (o.p === undefined);
	`)
	assertString(t, res, "true:true")
}

// TestWithPrimitiveObject: with 对象是原始值时按其包装对象处理 (无该属性则穿透)。
func TestWithPrimitiveObject(t *testing.T) {
	res := evalJS(t, `
		var o = 2;
		var foo = 1;
		with (o) { foo = 42; }
		foo;
	`)
	assertNumber(t, res, 42)
}

// TestWithCompoundAndIncDec: 复合赋值与自增在 with 体内写回对象。
func TestWithCompoundAndIncDec(t *testing.T) {
	res := evalJS(t, `
		var o = { n: 10 };
		with (o) { n += 5; n++; ++n; }
		o.n;
	`)
	assertNumber(t, res, 17)
}

// TestWithUndefinedThrowsTypeError: with 对象表达式求值为 undefined 时,
// ToObject 抛 TypeError (规范 14.11.2), 且 with 体不执行。
func TestWithUndefinedThrowsTypeError(t *testing.T) {
	res := evalJS(t, `
		var r;
		var ran = false;
		try { with (undefined) { ran = true; } r = "no-throw"; }
		catch (e) { r = e.name; }
		r + ":" + ran;
	`)
	assertString(t, res, "TypeError:false")
}

// TestWithNullThrowsTypeError: with(null) 同样抛 TypeError。
func TestWithNullThrowsTypeError(t *testing.T) {
	res := evalJS(t, `
		var r;
		try { with (null) { } r = "no-throw"; }
		catch (e) { r = e.name; }
		r;
	`)
	assertString(t, res, "TypeError")
}

// TestWithCommaSequenceObject: with 头是完整 Expression —— 允许逗号序列。
func TestWithCommaSequenceObject(t *testing.T) {
	res := evalJS(t, `
		var r = 0;
		var obj;
		with (1, obj = { v: 9 }) { r = v; }
		r;
	`)
	assertNumber(t, res, 9)
}

// TestWithDeclarationBodyIsSyntaxError: let/const/class/function 声明不能作
// with 体 (必须是 Statement), 解析期报 SyntaxError。
func TestWithDeclarationBodyIsSyntaxError(t *testing.T) {
	bodies := []string{
		"with ({}) let x;",
		"with ({}) const x = 1;",
		"with ({}) class C {}",
		"with ({}) function f() {}",
		"with ({}) function* g() {}",
		"with ({}) async function f() {}",
	}
	for _, src := range bodies {
		p := parser.New(lexer.New(src))
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Fatalf("expected parse error for %q, got none", src)
		}
	}
	// var 是 VariableStatement (属 Statement), 仍合法。
	p := parser.New(lexer.New("with ({}) var x = 1;"))
	p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("var as with-body should parse, got: %s", p.Errors().String())
	}
}

