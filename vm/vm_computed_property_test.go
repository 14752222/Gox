package vm

import "testing"

// ===== 计算属性名 (computed property names, ES6 §12.2.5) =====
//
// 覆盖对象字面量与 class 的计算键五形态: 键值 / 方法 / 生成器 / getter / setter,
// 以及静态成员、Symbol 键 (for-of 迭代协议)。用 stdlib 环境 (Symbol 迭代协议)。
// 多语句用例一律包 IIFE —— lastPopped 的语义是"主程序结束时的栈顶",
// 中间语句的子帧 (getter 调用等) 会污染它, return 值是唯一可靠的结果通道。

func TestComputedPropertyObject(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		// 键值
		{`var o = {["a" + "c"]: 1}; o.ac`, 1},
		{`var k = "b"; var o = {[k]: 1}; o.b`, 1},
		{`var o = {[1+1]: "two"}; o[2].length`, 3}, // "two".length
		// 方法
		{`var k = "m"; var o = {[k]() { return this.v; }, v: 3}; o.m()`, 3},
		// getter/setter (LastPopped 会被 getter 子帧的取值污染, 一律 IIFE 化)
		{`(() => { var k = "v"; var o = {a: 1, get [k]() { return this.a + 10; }}; return o.v; })()`, 11},
		{`(() => { var k = "w"; var o = {a: 1, set [k](x) { this.a = x; }}; o.w = 5; return o.a; })()`, 5},
		// 生成器方法 (直接调用)
		{`var k = "it"; var o = {*[k]() { yield 1; yield 2; }}; var g = o.it(); g.next().value + g.next().value`, 3},
		// Symbol 键
		{`var s = Symbol.iterator; var o = {}; o[s] = 42; o[s]`, 42},
	}

	for _, tt := range tests {
		assertNumber(t, evalWithStdlib(t, tt.input), tt.expected)
	}
}

func TestComputedPropertyObjectForOf(t *testing.T) {
	// 对象实现 [Symbol.iterator] 生成器方法 → for-of 迭代
	assertNumber(t, evalWithStdlib(t, `
		var g = { *[Symbol.iterator]() { yield 1; yield 2; yield 3; } };
		var sum = 0;
		for (var x of g) { sum += x; }
		sum;
	`), 6)

	// 展开 (spread) 走同一迭代协议
	assertString(t, evalWithStdlib(t, `
		var o = { *[Symbol.iterator]() { yield "a"; yield "b"; } };
		[...o].join("-");
	`), "a-b")
}

func TestComputedPropertyClass(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		// 实例方法
		{`var k = 1; class C { [k === 1 ? "a" : "b"]() { return 7; } } new C().a()`, 7},
		// 实例 getter
		{`var k = "v"; class C { get [k]() { return 42; } } new C().v`, 42},
		// 实例字段
		{`var k = "f"; class C { [k] = 7; } new C().f`, 7},
		// 静态方法
		{`var k = "s"; class C { static [k]() { return 3; } } C.s()`, 3},
		// 静态 getter/setter (普通 + 计算)
		{`class C { static get x() { return 1; } } C.x`, 1},
		{`var k = "x"; class C { static get [k]() { return 2; } } C.x`, 2},
		{`(() => { class C { static set x(v) { this._v = v; } } C.x = 9; return C._v; })()`, 9},
		// 静态 getter 链式 this
		{`class C { static get a() { return this.b; } static get b() { return 5; } } C.a`, 5},
	}

	for _, tt := range tests {
		assertNumber(t, evalWithStdlib(t, tt.input), tt.expected)
	}
}

func TestComputedPropertyClassExpression(t *testing.T) {
	assertString(t, evalWithStdlib(t, `
		var k = "n";
		var C = class { [k + "ick"]() { return "expr"; } };
		new C().nick();
	`), "expr")
}
