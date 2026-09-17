package vm

import (
	"math"
	"testing"

	"github.com/14752222/Gox/object"
)

// 本文件是针对标准库 API 实现修复的回归测试。
// 每个用例锁定一处被修复的缺陷，防止回退。

// assertJS 求值 JS 表达式，并把结果经 String() 转换后与期望值比较。
// 这样同一个断言可覆盖数字、布尔、字符串、undefined、null 等各种类型。
func assertJS(t *testing.T, expr, expected string) {
	t.Helper()
	got := evalJS(t, "String("+expr+")")
	s, ok := got.(*object.String)
	if !ok {
		t.Fatalf("%s: expected String result, got %T (%v)", expr, got, got)
	}
	if s.Value != expected {
		t.Fatalf("%s: expected %q, got %q", expr, expected, s.Value)
	}
}

// assertJSNaN 断言 JS 表达式的结果是 NaN。
func assertJSNaN(t *testing.T, expr string) {
	t.Helper()
	got := evalJS(t, expr)
	num, ok := got.(*object.Number)
	if !ok || !math.IsNaN(num.Value) {
		t.Fatalf("%s: expected NaN, got %v", expr, got)
	}
}

// assertJSThrows 断言 JS 表达式抛出指定 name 的错误。
func assertJSThrows(t *testing.T, expr, errName string) {
	t.Helper()
	got := evalJS(t, "try { "+expr+"; 'no-throw' } catch (e) { e.name }")
	s, ok := got.(*object.String)
	if !ok {
		t.Fatalf("%s: expected String, got %T", expr, got)
	}
	if s.Value != errName {
		t.Fatalf("%s: expected throw %q, got %q", expr, errName, s.Value)
	}
}

// ===== 数值强制转换 (ECMAScript ToNumber) =====

func TestToNumberStrictness(t *testing.T) {
	// 不得截断解析: "12px" 整体不是合法数字字面量
	assertJSNaN(t, `Number("12px")`)
	assertJSNaN(t, `Number("12e")`)
	assertJS(t, `Number("")`, "0")
	assertJS(t, `Number("   ")`, "0")
	assertJS(t, `Number("  42  ")`, "42")
	// 只接受精确的 Infinity 拼写
	assertJS(t, `Number("Infinity")`, "Infinity")
	assertJS(t, `Number("-Infinity")`, "-Infinity")
	assertJSNaN(t, `Number("infinity")`)
	assertJSNaN(t, `Number("nan")`)
	// 数组先转字符串
	assertJS(t, `Number([])`, "0")
	assertJS(t, `Number([5])`, "5")
	assertJSNaN(t, `Number([1,2])`)
	// null / undefined / 布尔
	assertJS(t, `Number(null)`, "0")
	assertJSNaN(t, `Number(undefined)`)
	assertJS(t, `Number(true)`, "1")
}

func TestParseIntAndParseFloat(t *testing.T) {
	// parseInt 截断尾随非法字符
	assertJS(t, `parseInt("12abc")`, "12")
	assertJS(t, `parseInt("12.5px")`, "12")
	// radix 未指定时识别 0x 前缀；明确指定 10 时不识别
	assertJS(t, `parseInt("0x10")`, "16")
	assertJS(t, `parseInt("0x10", 10)`, "0")
	assertJS(t, `parseInt("0x10", 16)`, "16")
	assertJS(t, `parseInt("7", 8)`, "7")
	assertJS(t, `parseInt("ff", 16)`, "255")
	assertJSNaN(t, `parseInt("zz", 10)`)
	// parseFloat 同样截断
	assertJS(t, `parseFloat("3.14abc")`, "3.14")
	assertJSNaN(t, `parseFloat("abc")`)
	assertJS(t, `parseFloat("Infinity")`, "Infinity")
	// Number.parseInt / Number.parseFloat 与全局版本行为一致
	assertJS(t, `Number.parseInt("12abc")`, "12")
	assertJS(t, `Number.parseFloat("3.14abc")`, "3.14")
}

// ===== Array =====

func TestArraySpliceSingleArgNoPanic(t *testing.T) {
	// 修复前: deleteCount 默认取 n 而非 n-start，导致切片越界 panic
	assertJS(t, `[1,2,3].splice(1).join(",")`, "2,3")
	assertJS(t, `[1,2,3].splice(0).join(",")`, "1,2,3")
	assertJS(t, `[1,2,3].splice(2).join(",")`, "3")
	assertJS(t, `[1,2,3].splice(5).join(",")`, "")
	assertJS(t, `[1,2,3].splice(-1).join(",")`, "3")
}

func TestArraySortDefaultIsStringOrder(t *testing.T) {
	// 规范要求无比较器时按字符串比较: "10" < "9"
	assertJS(t, `[10,9,1,2].sort().join(",")`, "1,10,2,9")
	// 传入比较器时按比较器结果排序
	assertJS(t, `[10,9,1,2].sort(function(a,b){return a-b}).join(",")`, "1,2,9,10")
}

func TestArrayIndexOfUsesSameValueZero(t *testing.T) {
	// indexOf / includes / lastIndexOf 必须能匹配 NaN
	assertJS(t, `[NaN].indexOf(NaN)`, "0")
	assertJS(t, `[NaN].includes(NaN)`, "true")
	assertJS(t, `[1,NaN,3].lastIndexOf(NaN)`, "1")
	// +0 / -0 在 SameValueZero 下相等
	assertJS(t, `[0].indexOf(-0)`, "0")
}

func TestArrayHigherOrderThisArg(t *testing.T) {
	assertJS(t, `[1,2,3].map(function(x){return x*this.f}, {f:2}).join(",")`, "2,4,6")
	assertJS(t, `[1,2,3,4].filter(function(x){return x>this.min}, {min:2}).join(",")`, "3,4")
	assertJS(t, `[1,2].some(function(x){return x===this.v}, {v:2})`, "true")
	assertJS(t, `[1,2].every(function(x){return x<this.v}, {v:3})`, "true")
}

func TestArrayErrors(t *testing.T) {
	// 空数组且无 initialValue 时抛 TypeError，而不是返回 undefined
	assertJSThrows(t, `[].reduce(function(a,b){return a+b})`, "TypeError")
	assertJSThrows(t, `[].reduceRight(function(a,b){return a+b})`, "TypeError")
	// 回调不可调用时抛 TypeError，而不是静默返回默认值
	assertJSThrows(t, `[1,2].map(null)`, "TypeError")
	// this 不是数组时抛 TypeError
	assertJSThrows(t, `Array.prototype.push.call(null, 1)`, "TypeError")
	assertJSThrows(t, `Array.prototype.map.call({}, 1)`, "TypeError")
}

func TestArrayFlatAndFrom(t *testing.T) {
	assertJS(t, `[1,[2,[3,[4]]]].flat(Infinity).join(",")`, "1,2,3,4")
	assertJS(t, `[1,[2,[3]]].flat(2).join(",")`, "1,2,3")
	assertJS(t, `[1,[2]].flat(0).length`, "2")
	// 含非 ASCII 字符的字符串: 按 UTF-16 码元拆分，不得产生空洞
	assertJS(t, `Array.from("a\u4e16").length`, "2")
	assertJS(t, `Array.from([1,2], function(x){return x*2}).join(",")`, "2,4")
	assertJS(t, `Array.from(new Set([1,2,2])).join(",")`, "1,2")
}

// ===== String =====

func TestStringUTF16Indexing(t *testing.T) {
	// "a\u4e16" 的 UTF-16 长度是 2，不是 UTF-8 的 4 字节
	assertJS(t, `"a\u4e16".length`, "2")
	assertJS(t, `"a\u4e16".charAt(1)`, "\u4e16")
	assertJS(t, `"a\u4e16".charCodeAt(1)`, "19990")
	assertJS(t, `"a\u4e16".split("").length`, "2")
	assertJS(t, `"a\u4e16".substring(1)`, "\u4e16")
	assertJS(t, `"a\u4e16".slice(-1)`, "\u4e16")
	assertJS(t, `"a\u4e16".at(-1)`, "\u4e16")
	assertJS(t, `"a\u4e16".indexOf("\u4e16")`, "1")
}

func TestStringIndexErrorsAndDefaults(t *testing.T) {
	// 越界 charCodeAt 返回 NaN (旧实现返回 0x10FFFF)
	assertJSNaN(t, `"ab".charCodeAt(99)`)
	assertJS(t, `"ab".charAt(99)`, "")
	// fromIndex 越界不得 panic
	assertJS(t, `"abc".indexOf("a", 99)`, "-1")
	assertJS(t, `"abc".indexOf("a", -1)`, "0")
}

func TestStringPositionalArgs(t *testing.T) {
	assertJS(t, `"abc".startsWith("bc", 1)`, "true")
	assertJS(t, `"abc".endsWith("ab", 2)`, "true")
	assertJS(t, `"abc".includes("b", 2)`, "false")
	assertJS(t, `"abc".includes("b", 1)`, "true")
	assertJS(t, `"abcabc".lastIndexOf("a", 2)`, "0")
}

func TestStringRepeatAndReplaceAllErrors(t *testing.T) {
	assertJSThrows(t, `"ab".repeat(-1)`, "RangeError")
	assertJSThrows(t, `"aaa".replaceAll(/a/, "b")`, "TypeError")
	assertJS(t, `"aaa".replaceAll(/a/g, "b")`, "bbb")
	assertJS(t, `"abc".replace(/b/, "[$0]")`, "a[b]c")
	assertJS(t, `"John Smith".replace(/(\w+)\s(\w+)/, "$2, $1")`, "Smith, John")
}

// ===== Object =====

func TestObjectEntriesSupportsArrays(t *testing.T) {
	// Object.entries 过去漏掉数组分支，返回空数组
	assertJS(t, `Object.entries([1,2]).length`, "2")
	assertJS(t, `String(Object.entries([1,2]))`, "0,1,1,2")
	assertJS(t, `Object.keys("ab").join(",")`, "0,1")
	assertJS(t, `Object.values({a:1,b:2}).join(",")`, "1,2")
}

func TestObjectKeyOrderIsInsertionOrder(t *testing.T) {
	// ECMAScript OrdinaryOwnPropertyKeys: 整数索引键升序在前，其余按插入顺序
	assertJS(t, `Object.keys({b:1,a:2}).join(",")`, "b,a")
	assertJS(t, `Object.keys({b:1,a:2,2:0,1:0}).join(",")`, "1,2,b,a")
}

func TestObjectFreezeAndDefineProperty(t *testing.T) {
	// freeze 后已有属性不可写
	assertJS(t, `(function(){let o={a:1};Object.freeze(o);o.a=99;return o.a})()`, "1")
	assertJS(t, `(function(){let o={a:1};Object.freeze(o);return Object.isFrozen(o)})()`, "true")
	// defineProperty 的 writable 标志生效
	assertJS(t, `(function(){let o={};Object.defineProperty(o,"x",{value:1,writable:false});return Object.getOwnPropertyDescriptor(o,"x").writable})()`, "false")
	// getter 注册为访问器，而不是立即求值后当作普通值
	assertJS(t, `(function(){let o={};Object.defineProperty(o,"v",{get:function(){return 7}});return o.v})()`, "7")
}

func TestNumberGlobalStaticMembersPresent(t *testing.T) {
	// 过去 setupGlobalFunctions 的重复 Declare 覆盖了 setupNumberGlobal，
	// 导致这些静态成员在全局不可见
	assertJS(t, `typeof Number.MAX_VALUE`, "number")
	assertJS(t, `typeof Number.MIN_VALUE`, "number")
	assertJS(t, `typeof Number.parseInt`, "function")
	assertJS(t, `typeof Number.parseFloat`, "function")
	assertJS(t, `typeof Number.isSafeInteger`, "function")
}

func TestNumberIsNaNIsTypeStrict(t *testing.T) {
	// Number.isNaN 只对真正的 Number NaN 返回 true
	assertJS(t, `Number.isNaN("x")`, "false")
	assertJS(t, `Number.isNaN(NaN)`, "true")
	assertJS(t, `Number.isInteger(1.5)`, "false")
	assertJS(t, `Number.isInteger(3)`, "true")
	assertJS(t, `Number.isFinite(Infinity)`, "false")
}

func TestNumberPrototypeErrors(t *testing.T) {
	assertJSThrows(t, `Number.prototype.toFixed.call("x", 2)`, "TypeError")
	assertJSThrows(t, `(1).toExponential(101)`, "RangeError")
	assertJSThrows(t, `(1).toFixed(101)`, "RangeError")
}

// ===== Math =====

func TestMathHypotIsSquareRoot(t *testing.T) {
	// 过去 Math.hypot 被注册三次，最终生效版本返回平方和 (25)
	assertJS(t, `Math.hypot(3,4)`, "5")
	assertJS(t, `Math.hypot(3,4,12)`, "13")
	// 非标准的 hypot2 不得存在
	assertJS(t, `typeof Math.hypot2`, "undefined")
}

func TestMathES6Extras(t *testing.T) {
	assertJS(t, `Math.imul(-1, 5)`, "-5")
	assertJS(t, `Math.clz32(1)`, "31")
	assertJS(t, `Math.clz32(0)`, "32")
	assertJS(t, `Math.cbrt(27)`, "3")
	assertJS(t, `typeof Math.EPSILON`, "number")
	assertJS(t, `Math.round(-1.5)`, "-1")
	assertJS(t, `Math.max()`, "-Infinity")
	assertJS(t, `Math.min()`, "Infinity")
}

// ===== JSON =====

func TestJSONCircularReferenceThrows(t *testing.T) {
	// 修复前会无限递归直至栈溢出
	assertJSThrows(t, `(function(){let a={};a.self=a;return JSON.stringify(a)})()`, "TypeError")
	// 同一对象在不同分支重复出现是合法的，不算环
	assertJS(t, `(function(){let x={v:1};return JSON.stringify({p:x,q:x})})()`,
		`{"p":{"v":1},"q":{"v":1}}`)
}

func TestJSONParseErrorNameAndKeyOrder(t *testing.T) {
	// 错误 name 必须是 SyntaxError，而不是默认的 Error
	assertJSThrows(t, `JSON.parse("{bad")`, "SyntaxError")
	// 键顺序必须与输入一致 (encoding/json 的 map 遍历会打乱顺序)
	assertJS(t, `JSON.stringify(JSON.parse('{"name":"t","value":100}'))`,
		`{"name":"t","value":100}`)
}

func TestJSONReviverReceivesValues(t *testing.T) {
	// 修复前数组分支用 "" 存入却用索引取出，reviver 收到 nil
	assertJS(t, `JSON.parse('{"a":1,"b":[2,3]}', function(k,v){return typeof v==="number"?v*10:v}).b.join(",")`,
		"20,30")
	assertJS(t, `JSON.parse('{"a":1}', function(k,v){return typeof v==="number"?v*10:v}).a`, "10")
}

// ===== Map / Set / WeakMap / WeakSet =====

func TestWeakMapHasMethods(t *testing.T) {
	// 过去 WeakMap 既无 prototype 属性也无实例原型，实例上没有任何方法
	assertJS(t, `typeof new WeakMap().set`, "function")
	assertJS(t, `(function(){let m=new WeakMap();let k={};m.set(k,42);return m.get(k)})()`, "42")
	assertJS(t, `(function(){let m=new WeakMap();let k={};m.set(k,1);return m.has(k)})()`, "true")
	assertJS(t, `typeof new WeakSet().add`, "function")
	assertJS(t, `(function(){let s=new WeakSet();let k={};s.add(k);return s.has(k)})()`, "true")
}

func TestMapSetSize(t *testing.T) {
	assertJS(t, `new Map().size`, "0")
	assertJS(t, `(function(){let m=new Map();m.set("a",1);return m.size})()`, "1")
	assertJS(t, `new Set([1,2,2,3]).size`, "3")
}

// ===== Promise =====

func TestPromiseRejectionPropagatesThroughThen(t *testing.T) {
	// Then 过去不注册 rejection 回调，导致链式 Promise 永久 pending
	assertJS(t, `(function(){let r="pending";Promise.reject("boom").then(function(){}).catch(function(e){r=e});return r})()`, "boom")
	// then 的第二个参数同样可用
	assertJS(t, `(function(){let r="pending";Promise.reject("x").then(null, function(e){r=e});return r})()`, "x")
}

func TestPromiseCombinatorsRejectOnNonIterable(t *testing.T) {
	// 过去 race/allSettled 对非数组参数返回永久 pending 的 Promise
	assertJS(t, `(function(){let r="pending";Promise.race("bad").catch(function(e){r=e.name});return r})()`, "TypeError")
	assertJS(t, `(function(){let r="pending";Promise.allSettled("bad").catch(function(e){r=e.name});return r})()`, "TypeError")
	assertJS(t, `(function(){let r="pending";Promise.all("bad").catch(function(e){r=e.name});return r})()`, "TypeError")
}

func TestPromiseAllAllSettledAny(t *testing.T) {
	assertJS(t, `(function(){let r="pending";Promise.all([Promise.resolve(1),2]).then(function(v){r=v.join(",")});return r})()`, "1,2")
	assertJS(t, `(function(){let r="pending";Promise.allSettled([Promise.resolve(1),Promise.reject("e")]).then(function(v){r=v[1].status});return r})()`, "rejected")
	assertJS(t, `(function(){let r="pending";Promise.any([Promise.reject("a"),Promise.resolve("b")]).then(function(v){r=v});return r})()`, "b")
	// 全部 rejected 时以 AggregateError 拒绝，并带 errors 数组
	assertJS(t, `(function(){let r="pending";Promise.any([Promise.reject("a"),Promise.reject("b")]).catch(function(e){r=e.name+":"+e.errors.join(",")});return r})()`,
		"AggregateError:a,b")
}

func TestPromisePrototypeMethodsTypeCheck(t *testing.T) {
	assertJSThrows(t, `Promise.prototype.then.call({}, function(){})`, "TypeError")
}

// ===== RegExp =====

func TestRegExpTestAdvancesLastIndexUnderGlobalFlag(t *testing.T) {
	// 全局模式下 test 必须像 exec 一样推进 lastIndex
	assertJS(t, `(function(){let re=/a/g;let first=re.test("abc");return String(first)+","+re.lastIndex})()`, "true,1")
	assertJS(t, `(function(){let re=/a/g;re.test("abc");return re.test("abc")})()`, "false")
	// 非全局模式不影响 lastIndex
	assertJS(t, `(function(){let re=/a/;re.test("abc");return re.lastIndex})()`, "0")
}

func TestRegExpStickyFlag(t *testing.T) {
	assertJS(t, `/a/y.sticky`, "true")
	assertJS(t, `/a/.sticky`, "false")
	// sticky 必须匹配在 lastIndex 处
	assertJS(t, `(function(){let re=/a/y;re.lastIndex=1;return re.test("abc")})()`, "false")
	assertJS(t, `(function(){let re=/a/y;re.lastIndex=0;return re.test("abc")})()`, "true")
}

// ===== Reflect =====

func TestReflectConstructBindsPrototype(t *testing.T) {
	// 过去 Reflect.construct 直接返回函数调用结果，丢失新对象与原型绑定
	assertJS(t, `(function(){function P(x){this.x=x}P.prototype.get=function(){return this.x};
		let o=Reflect.construct(P,[7]);return o.x+","+o.get()})()`, "7,7")
}

func TestReflectTypeErrorAndSemantics(t *testing.T) {
	assertJSThrows(t, `Reflect.get(null, "a")`, "TypeError")
	// 数组越界索引不算存在
	assertJS(t, `Reflect.has([1,2], "99")`, "false")
	assertJS(t, `Reflect.has([1,2], "1")`, "true")
	// 冻结对象上 Reflect.set 应失败
	assertJS(t, `(function(){let o={a:1};Object.freeze(o);return Reflect.set(o,"a",99)})()`, "false")
	// deleteProperty 必须同步清理属性顺序记录
	assertJS(t, `(function(){let o={a:1,b:2};Reflect.deleteProperty(o,"a");return Object.keys(o).join(",")})()`, "b")
}

// ===== 构造器 prototype 属性 =====

func TestConstructorsExposePrototype(t *testing.T) {
	// 过去构造器没有 prototype 属性，导致 Array.prototype.map.call(...) 类
	// 通用调用与 Number.prototype 反射访问全部失效
	assertJS(t, `typeof Array.prototype.map`, "function")
	assertJS(t, `typeof String.prototype.trim`, "function")
	assertJS(t, `typeof Number.prototype.toFixed`, "function")
	assertJS(t, `[].constructor === Array`, "true")
}

// ===== 错误对象附加属性 =====

func TestErrorSupportsCustomProperties(t *testing.T) {
	// 过去 Error.SetProperty 是空实现，附加属性被静默丢弃
	assertJS(t, `(function(){let e=new Error("m");e.code=42;return e.code})()`, "42")
	assertJS(t, `String(new Error("boom"))`, "Error: boom")
	assertJS(t, `new TypeError("t").name`, "TypeError")
}
