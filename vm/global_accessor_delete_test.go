package vm

// 全局访问器绑定 / 计算成员键 / delete 的三组不变量回归。
//
// 三条均由 2026-10-06 夜间合流批次七引入后被 lead 复核实测发现，
// 分别对应 test262 语言套件的真实用例，修复见本文件各用例注释。

import "testing"

// ===== 1. 全局访问器绑定的读/写必须显式调 getter/setter =====
//
// Object.defineProperty(globalThis, "x", {get}) 在全局环境记录里存的是
// *object.Accessor。此前 OP_LOAD_GLOBAL 直接把该对象当值返回 ——
// typeof x 得 "object"、`x ^= 3` 把 Accessor 参与按位异或。
//
// test262: language/expressions/compound-assignment/
//          compound-assignment-operator-calls-putvalue-lref--v--*.js

func TestGlobalAccessorBindingGetterIsCalled(t *testing.T) {
	got := evalWithStdlib(t, "Object.defineProperty(this, \"acc1\", {"+
		"configurable: true,"+
		"get: function () { return 42; }"+
		"});"+
		"(function () { \"use strict\"; return acc1; })();")
	assertNumber(t, got, 42)
}

func TestGlobalAccessorBindingSetterIsCalled(t *testing.T) {
	got := evalWithStdlib(t, "var seen = -1;"+
		"Object.defineProperty(this, \"acc2\", {"+
		"configurable: true,"+
		"get: function () { return 0; },"+
		"set: function (v) { seen = v; }"+
		"});"+
		"(function () { acc2 = 7; })();"+
		"seen;")
	assertNumber(t, got, 7)
}

// 用例形状: 全局访问器属性的 getter 自删该属性 (delete this.x)。
// 之后严格模式下对 x 的复合赋值必须抛 ReferenceError (Object Environment
// Record SetMutableBinding 在绑定不存在且 S=true 时抛), 且属性不得被重建。
func TestGlobalAccessorSelfDeleteThenStrictCompoundAssignThrows(t *testing.T) {
	got := evalWithStdlib(t, "var count = 0;"+
		"Object.defineProperty(this, \"acc3\", {"+
		"configurable: true,"+
		"get: function () { delete this.acc3; return 2; }"+
		"});"+
		"var threwName = \"\";"+
		"(function () {"+
		"\"use strict\";"+
		"try {"+
		"count++;"+
		"acc3 ^= 3;"+
		"count++;"+
		"} catch (e) {"+
		"threwName = e && e.name ? e.name : \"?\";"+
		"}"+
		"})();"+
		"count++;"+
		"threwName + \"|\" + count + \"|\" + (\"acc3\" in this);")
	assertString(t, got, "ReferenceError|2|false")
}

// ===== 2. 简单赋值的 ToPropertyKey 必须晚于右值求值 =====
//
// 规范: base[prop] = expr() 中 prop 求值只得到 propertyNameValue，
// ToPropertyKey 属于 PutValue 内部 ⇒ prop.toString() 在 expr() 之后才调用。
// 此前编译器在成员引用建立时就发 OP_TO_PROPERTY_KEY ⇒ toString 被提前调用。
//
// test262: language/expressions/assignment/target-member-computed-reference*.js

func TestComputedKeyToPropertyKeyRunsAfterRHS(t *testing.T) {
	got := evalWithStdlib(t, "var order = \"\";"+
		"var prop = { toString: function () { order += \"key\"; return \"k\"; } };"+
		"var expr = function () { order += \"rhs\"; return 1; };"+
		"var base = {};"+
		"base[prop] = expr();"+
		"order;")
	assertString(t, got, "rhskey")
}

// 复合赋值的键只允许转换一次 (与上一条并存):
// test262: language/expressions/compound-assignment/S11.13.2_A7.*_T4.js
func TestCompoundAssignToPropertyKeyCalledOnce(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var base = { k: 2 };"+
		"base[prop] *= 3;"+
		"n + \"|\" + base.k;")
	assertString(t, got, "1|6")
}

// ===== 3. delete 在 globalThis / 数组索引上必须遵循可配置性 =====
//
// test262: language/arguments-object/mapped/
//          mapped-arguments-nonconfigurable-delete-1.js

func TestDeleteGlobalThisOwnProperty(t *testing.T) {
	got := evalWithStdlib(t, "Object.defineProperty(this, \"d1\", { configurable: true, value: 1 });"+
		"var first = delete this.d1;"+
		"var second = (\"d1\" in this);"+
		"Object.defineProperty(this, \"d2\", { configurable: false, value: 2 });"+
		"var third = delete this.d2;"+
		"first + \"|\" + second + \"|\" + third;")
	assertString(t, got, "true|false|false")
}

func TestDeleteNonConfigurableArrayIndexReturnsFalse(t *testing.T) {
	got := evalWithStdlib(t, "function f(a) {"+
		"Object.defineProperty(arguments, \"0\", { configurable: false });"+
		"return String(delete arguments[0]) + \"|\" + a + \"|\" + arguments[0];"+
		"}"+
		"f(1);")
	assertString(t, got, "false|1|1")
}

// 可配置的数组索引: delete 返回 true 且元素被清空。
//
// 注: Gox 的数组用 Elements 切片表示, 没有真正的"稀疏洞" —— 元素位被置
// undefined, 因此 `0 in arr` 仍为 true (既有表示局限, 非本次改动引入;
// 见看板关于稀疏数组的条目)。断言只钉住本次要保证的两点: 返回值与清空。
func TestDeleteConfigurableArrayIndexSucceeds(t *testing.T) {
	got := evalWithStdlib(t, "var arr = [10, 20];"+
		"var r = delete arr[0];"+
		"r + \"|\" + String(arr[0]) + \"|\" + arr.length;")
	assertString(t, got, "true|undefined|2")
}
