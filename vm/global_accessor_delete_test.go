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

// ===== 2b. ToPropertyKey 必须晚于基的 RequireObjectCoercible =====
//
// 规范 GetValue/PutValue 先 ToObject(base) (null/undefined 立即抛 TypeError),
// 再做 ToPropertyKey(键)。此前 OP_TO_PROPERTY_KEY 被排在 GET_INDEX 之前, 于是
// 基为 null 的复合赋值会先把对象键 toString 跑掉 —— 键抛出的 Test262Error 顶替了
// 本该出现的 TypeError。看板 rknvx2。
//
// test262: language/expressions/compound-assignment/S11.13.2_A7.*_T1|T2.js
//          language/expressions/logical-assignment/lgcl-*-lhs-before-rhs.js
//          language/expressions/{postfix,prefix}-{increment,decrement}/S11.*_A6_T1|T2.js
func TestCompoundAssignNullBaseThrowsTypeErrorWithoutKeyConversion(t *testing.T) {
	// null 基: 必须 TypeError, 且键的 toString 一次都不被调用。
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var name = \"\";"+
		"try { var base = null; base[prop] *= 1; } catch (e) { name = e.name; }"+
		"name + \"|\" + n;")
	assertString(t, got, "TypeError|0")
}

func TestCompoundAssignUndefinedBaseThrowsTypeErrorWithoutKeyConversion(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var name = \"\";"+
		"try { var base = undefined; base[prop] += 1; } catch (e) { name = e.name; }"+
		"name + \"|\" + n;")
	assertString(t, got, "TypeError|0")
}

// 键表达式本身仍必须被求值 (只求到 propertyNameValue, 不做 ToPropertyKey):
// null[抛出 DummyError 的键表达式] *= 1 必须看到 DummyError 而非 TypeError。
func TestCompoundAssignNullBaseStillEvaluatesKeyExpression(t *testing.T) {
	got := evalWithStdlib(t, "function DummyError() {}"+
		"var seen = \"\";"+
		"try { var base = null; base[(function () { throw new DummyError(); })()] *= 1; }"+
		"catch (e) { seen = (e instanceof DummyError) ? \"DummyError\" : \"other\"; }"+
		"seen;")
	assertString(t, got, "DummyError")
}

// 逻辑赋值 / ++ / -- 的 null 基同样: TypeError 且不转换键。
func TestLogicalAssignAndIncDecNullBaseThrowsTypeError(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var mk = function () { return { toString: function () { n++; return \"k\"; } }; };"+
		"var names = [];"+
		"try { var a = null; a[mk()] ??= 1; } catch (e) { names.push(e.name); }"+
		"try { var b = null; b[mk()]++; } catch (e) { names.push(e.name); }"+
		"try { var c = undefined; ++c[mk()]; } catch (e) { names.push(e.name); }"+
		"names.join(\",\") + \"|\" + n;")
	assertString(t, got, "TypeError,TypeError,TypeError|0")
}

// 有效基上的「键只转一次」在三种路径都不回归 (复合赋值 / 逻辑赋值 / ++)。
func TestToPropertyKeyOnceOnValidBaseAllPaths(t *testing.T) {
	got := evalWithStdlib(t, "var out = [];"+
		"var n1 = 0; var p1 = { toString: function () { n1++; return \"k\"; } };"+
		"var o1 = { k: 2 }; o1[p1] *= 3; out.push(n1 + \":\" + o1.k);"+
		"var n2 = 0; var p2 = { toString: function () { n2++; return \"v\"; } };"+
		"var o2 = { v: null }; o2[p2] ??= 7; out.push(n2 + \":\" + o2.v);"+
		"var n3 = 0; var p3 = { toString: function () { n3++; return \"x\"; } };"+
		"var o3 = { x: 10 }; o3[p3]++; out.push(n3 + \":\" + o3.x);"+
		"out.join(\",\");")
	assertString(t, got, "1:6,1:7,1:11")
}

// ===== 2c. 读取/写入 null 基的 TypeError 消息不得触发键的用户代码 =====
//
// test262: language/expressions/member-expression/
//          computed-reference-null-or-undefined.js
func TestComputedReadNullBaseThrowsTypeErrorWithoutKeyToString(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var name = \"\";"+
		"try { null[prop]; } catch (e) { name = e.name; }"+
		"name + \"|\" + n;")
	assertString(t, got, "TypeError|0")
}

func TestComputedWriteNullBaseThrowsTypeErrorWithoutKeyToString(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var name = \"\";"+
		"try { null[prop] = 1; } catch (e) { name = e.name; }"+
		"name + \"|\" + n;")
	assertString(t, got, "TypeError|0")
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

// 基为 null/undefined 的 delete 必须抛 TypeError (先 ToObject(ref.[[Base]])),
// 且**完全不碰键** —— 此前会先做 ToPropertyKey (触发键 toString) 再静默返回 true。
//
// test262: language/expressions/delete/
//          member-{computed,identifier}-reference-{null,undefined}.js
func TestDeleteNullBaseThrowsTypeErrorWithoutKeyToString(t *testing.T) {
	got := evalWithStdlib(t, "var n = 0;"+
		"var prop = { toString: function () { n++; return \"k\"; } };"+
		"var names = [];"+
		"try { delete null[prop]; } catch (e) { names.push(e.name); }"+
		"try { delete undefined[prop]; } catch (e) { names.push(e.name); }"+
		"names.join(\",\") + \"|\" + n;")
	assertString(t, got, "TypeError,TypeError|0")
}
