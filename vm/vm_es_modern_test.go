package vm

import "testing"

// 本文件锁定本次补齐的 ES5→ES2026 API 行为。

// ===== Function.prototype 方法 =====

func TestFunctionPrototypeMethods(t *testing.T) {
	assertNumber(t, evalJS(t, `(function(a,b){ return a+b }).call(null, 1, 2)`), 3)
	assertNumber(t, evalJS(t, `(function(a,b){ return a+b }).apply(null, [3,4])`), 7)
	// bind: this 绑定 + 前置参数
	assertNumber(t, evalJS(t, `function f(a,b){ return this.v + a + b }
		let g = f.bind({v: 100}, 20);
		g(3)`), 123)
	// 原生方法上的 call/apply (BuiltinMethod 继承 Function.prototype 方法)
	assertNumber(t, evalJS(t, `let a = [1,2];
		Array.prototype.push.call(a, 3);
		a.length`), 3)
	// push.call(null) 抛 TypeError
	assertJS(t, `(function(){ try { Array.prototype.push.call(null, 1); return "no-throw" } catch (e) { return e.name } })()`, "TypeError")
	// toFixed.call 严格接收者检查
	assertJS(t, `(function(){ try { Number.prototype.toFixed.call("x", 2); return "no-throw" } catch (e) { return e.name } })()`, "TypeError")
	// Function.prototype 可调用且返回 undefined
	assertJS(t, `String(Function.prototype())`, "undefined")
}

// ===== new Function / eval =====

func TestDynamicCode(t *testing.T) {
	assertNumber(t, evalJS(t, `let f = new Function("a", "b", "return a + b"); f(2, 3)`), 5)
	assertNumber(t, evalJS(t, `new Function("return 99")()`), 99)
	assertNumber(t, evalJS(t, `eval("1 + 2")`), 3)
	assertString(t, evalJS(t, `eval("'a' + 'b'")`), "ab")
	// eval 语法错误抛 SyntaxError
	assertJS(t, `(function(){ try { eval("let("); return "no-throw" } catch (e) { return e.name } })()`, "SyntaxError")
}

// ===== 属性赋值语义 =====

func TestPropertyAssignment(t *testing.T) {
	// Error 实例支持自定义属性
	assertNumber(t, evalJS(t, `let e = new Error("m"); e.code = 42; e.code`), 42)
	// RegExp.lastIndex 可写 (y 标志依赖它)
	assertNumber(t, evalJS(t, `let re = /a/y; re.lastIndex = 1; re.lastIndex`), 1)
	// sticky: 匹配必须正好发生在 lastIndex
	assertJS(t, `(function(){ let re = /a/y; re.lastIndex = 1; return String(re.test("abc")) })()`, "false")
	assertJS(t, `(function(){ let re = /a/y; re.lastIndex = 1; return String(re.test("xabc")) })()`, "true")
	// 数组 expando
	assertNumber(t, evalJS(t, `let a = [1]; a.foo = 7; a.foo`), 7)
}

// ===== ToString 转换 =====

func TestToStringSemantics(t *testing.T) {
	assertJS(t, `String([1,[2,3]])`, "1,2,3")
	assertJS(t, `String(Object.entries([1,2]))`, "0,1,1,2")
	assertJS(t, `"" + {a: 1}`, "[object Object]")
}

// ===== ES2023 数组方法 =====

func TestArrayChangeByCopy(t *testing.T) {
	assertJS(t, `[3,1,2].toSorted().join(",")`, "1,2,3")
	assertJS(t, `[1,2,3].toReversed().join(",")`, "3,2,1")
	assertJS(t, `[1,2,3,4].toSpliced(1,2,9).join(",")`, "1,9,4")
	assertJS(t, `[1,2,3].with(1, 9).join(",")`, "1,9,3")
	assertNumber(t, evalJS(t, `[5,1,8].findLast(function(v){ return v < 5 })`), 1)
	assertNumber(t, evalJS(t, `[5,1,8].findLastIndex(function(v){ return v < 5 })`), 1)
}

// ===== Set 组合方法 (ES2025) =====

func TestSetCombinators(t *testing.T) {
	assertJS(t, `new Set([1,2]).union(new Set([2,3])).toArray ? "arr" : [...new Set([1,2]).union(new Set([2,3]))].sort().join(",")`, "1,2,3")
	assertJS(t, `[...new Set([1,2,3]).intersection(new Set([2,3,4]))].sort().join(",")`, "2,3")
	assertJS(t, `[...new Set([1,2,3]).difference(new Set([2]))].sort().join(",")`, "1,3")
	assertJS(t, `[...new Set([1,2]).symmetricDifference(new Set([2,3]))].sort().join(",")`, "1,3")
	assertJS(t, `String(new Set([1,2]).isSubsetOf(new Set([1,2,3])))`, "true")
	assertJS(t, `String(new Set([1,2,3]).isSupersetOf(new Set([1,2])))`, "true")
	assertJS(t, `String(new Set([1,2]).isDisjointFrom(new Set([3,4])))`, "true")
}

// ===== Iterator helpers (ES2025) =====

func TestIteratorHelpers(t *testing.T) {
	assertJS(t, `Iterator.from([1,2,3]).map(function(x){ return x * 2 }).toArray().join(",")`, "2,4,6")
	assertJS(t, `Iterator.from([1,2,3,4]).take(2).toArray().join(",")`, "1,2")
	assertJS(t, `Iterator.from([1,2,3,4]).drop(2).toArray().join(",")`, "3,4")
	assertJS(t, `Iterator.from([1,2,3]).filter(function(x){ return x > 1 }).toArray().join(",")`, "2,3")
	assertNumber(t, evalJS(t, `Iterator.from([1,2,3]).reduce(function(a,b){ return a + b }, 0)`), 6)
	assertJS(t, `Iterator.zip([[1,2],[3,4]]).toArray().map(function(p){ return p.join(":") }).join(",")`, "1:3,2:4")
	assertJS(t, `Iterator.concat([1],[2,3]).toArray().join(",")`, "1,2,3")
	assertJS(t, `[...[1,2,3].entries()].map(function(e){ return e.join(":") }).join(",")`, "0:1,1:2,2:3")
}

// ===== TypedArray =====

func TestTypedArrayBasics(t *testing.T) {
	assertJS(t, `(function(){ let a = new Uint8Array(4); a[0] = 255; a[1] = 128; return a.join(",") })()`, "255,128,0,0")
	// 溢出按规范回绕 (模 2^8); 仅 Uint8ClampedArray 截断
	assertNumber(t, evalJS(t, `let a = new Uint8Array(1); a[0] = 300; a[0]`), 44)
	assertNumber(t, evalJS(t, `let a = new Int8Array(1); a[0] = 200; a[0]`), -56)
	assertNumber(t, evalJS(t, `let a = new Uint8ClampedArray(1); a[0] = 300; a[0]`), 255)
	// Float64 精度
	assertNumber(t, evalJS(t, `let f = new Float64Array(1); f[0] = 3.14; f[0] * 100`), 314)
	// subarray 是视图
	assertJS(t, `(function(){ let a = new Uint8Array([1,2,3,4]); let s = a.subarray(1,3); s[0] = 9; return a.join(",") })()`, "1,9,3,4")
	// 展开
	assertJS(t, `[...new Uint8Array([9,8,7])].join(",")`, "9,8,7")
}

func TestTypedArrayBase64(t *testing.T) {
	assertJS(t, `Uint8Array.from([104,105]).toBase64()`, "aGk=")
	assertNumber(t, evalJS(t, `Uint8Array.fromBase64("aGk=")[0]`), 104)
	assertNumber(t, evalJS(t, `Uint8Array.fromBase64("aGk=").length`), 2)
}

func TestDataView(t *testing.T) {
	// DataView 默认大端
	assertJS(t, `(function(){ let b = new ArrayBuffer(4);
		let v = new DataView(b);
		v.setUint16(0, 0x1234); return v.getUint16(0).toString(16) })()`, "1234")
	// 显式小端
	assertJS(t, `(function(){ let b = new ArrayBuffer(4);
		let v = new DataView(b);
		v.setUint16(0, 0x1234, true); return v.getUint16(0, false).toString(16) })()`, "3412")
}

// ===== Object 静态方法 =====

func TestObjectStatics(t *testing.T) {
	assertNumber(t, evalJS(t, `let o = {};
		Object.defineProperties(o, {a: {value: 1, writable: true}, b: {value: 2}});
		o.a + o.b`), 3)
	assertJS(t, `(function(){ let o = {}; let s = Symbol("s");
		o[s] = 7;
		return Object.getOwnPropertySymbols(o)[0] === s })()`, "true")
	assertJS(t, `JSON.stringify(Object.groupBy(["ab","cd","e"], function(x){ return x.length }))`, `{"1":["e"],"2":["ab","cd"]}`)
	assertNumber(t, evalJS(t, `let m = Map.groupBy([1,2,3], function(x){ return x % 2 });
		(m.get(1).length) + (m.get(0).length)`), 3)
}

// ===== Promise 静态方法 =====

func TestPromiseStatics(t *testing.T) {
	assertJS(t, `String(Promise.withResolvers().promise instanceof Promise)`, "true")
	// Promise.try 把同步异常转为 rejection
	assertJS(t, `(function(){ let out = "";
		Promise.try(function(){ throw "boom" }).catch(function(e){ out = "caught:" + e });
		return out })()`, "caught:boom")
}

// ===== 其他 =====

func TestMiscNewAPIs(t *testing.T) {
	// globalThis 读写穿透到全局
	assertNumber(t, evalJS(t, `globalThis.gv = 77; globalThis.gv`), 77)
	// RegExp.escape
	assertJS(t, `new RegExp(RegExp.escape("a.b")).test("a.b")`, "true")
	assertJS(t, `new RegExp(RegExp.escape("a.b")).test("axb")`, "false")
	// String.isWellFormed / toWellFormed
	assertJS(t, `String("hello").isWellFormed()`, "true")
	// (运行时字符串以 UTF-8 存储, 无法构造含孤立代理项的字符串,
	//  isWellFormed 恒 true; toWellFormed 仍可验证)
	assertNumber(t, evalJS(t, `String.fromCharCode(0xD800).toWellFormed().charCodeAt(0)`), 0xFFFD)
	// WeakRef
	assertJS(t, `(function(){ let o = {v: 1}; return new WeakRef(o).deref() === o })()`, "true")
	// AggregateError
	assertJS(t, `new AggregateError([1,2], "multi").name`, "AggregateError")
}
