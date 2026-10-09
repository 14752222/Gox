package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== RegExp 一等对象 (rEXjyz) =====
//
// 背景: 此前 `%RegExp.prototype%` 造出来了却没挂到 RegExp 构造器上,
// *object.RegExp 也不满足 OwnPropertyStore —— 于是
// `typeof RegExp.prototype` 是 undefined、`Object.defineProperty(r, "lastIndex",
// {writable:false})` 是 no-op, 规范里「Set(rx,"lastIndex",v,true) 写不进去就抛
// TypeError」无从实现, built-ins/RegExp/prototype/Symbol.{match,replace} 的
// 8 个用例全是假阳性通过。
//
// 这组测试锁住三件事: 原型装配、自有属性接口、严格 lastIndex 写入。
// 每条断言都与 Node v22 逐条对过。

// TestRegExpPrototypeIsAssembled 覆盖工单里 4 个探针的前两个。
func TestRegExpPrototypeIsAssembled(t *testing.T) {
	assertJS(t, `typeof RegExp.prototype`, "object")
	assertJS(t, `typeof RegExp.prototype.exec`, "function")
	assertJS(t, `RegExp.prototype.constructor === RegExp`, "true")
	// 实例的 [[Prototype]] 必须是 %RegExp.prototype%, 且链要接到
	// %Object.prototype% (此前 *RegExp 缺 GetProto, 链在实例处断掉)。
	assertJS(t, `Object.getPrototypeOf(/a/g) === RegExp.prototype`, "true")
	assertJS(t, `Object.getPrototypeOf(RegExp.prototype) === Object.prototype`, "true")
	assertJS(t, `Object.prototype.toString.call(/a/g)`, "[object RegExp]")
}

// TestRegExpOwnPropertyStore 锁住 *RegExp 的自有属性接口五件套。
// lastIndex 是规范形态的唯一自有属性: 可写、不可枚举、不可配置。
func TestRegExpOwnPropertyStore(t *testing.T) {
	assertJS(t, `JSON.stringify(Object.getOwnPropertyNames(/a/g))`, `["lastIndex"]`)
	// lastIndex 不可枚举 ⇒ Object.keys 恒为空 (与 Node 一致)。
	assertJS(t, `JSON.stringify(Object.keys(/a/g))`, `[]`)
	assertJS(t, `Object.getOwnPropertyDescriptor(/a/g, "lastIndex").writable`, "true")
	assertJS(t, `Object.getOwnPropertyDescriptor(/a/g, "lastIndex").enumerable`, "false")
	assertJS(t, `Object.getOwnPropertyDescriptor(/a/g, "lastIndex").configurable`, "false")
	assertJS(t, `Object.hasOwn(/a/g, "lastIndex")`, "true")
	// source/flags 等按规范挂在原型上, 不是实例自有属性。
	assertJS(t, `Object.getOwnPropertyDescriptor(/a/g, "source")`, "undefined")
	// 用户赋值的自有属性照常可见。
	assertJS(t, `(function(){ var r = /a/g; r.custom = 1; return JSON.stringify(Object.keys(r)); })()`, `["custom"]`)
}

// TestRegExpDefinePropertyLastIndexNonWritable 是 8 个用例的共同前提:
// defineProperty 必须真的把 lastIndex 改成不可写 (此前是 no-op)。
func TestRegExpDefinePropertyLastIndexNonWritable(t *testing.T) {
	assertJS(t, `(function(){
		var r = /a/g;
		Object.defineProperty(r, "lastIndex", { writable: false });
		try { r.lastIndex = 5; } catch (e) { return "threw"; }
		return r.lastIndex === 0 ? "kept" : "written";
	})()`, "kept")
	// 访问器属性同样要能落到实例上 (exec 的 getter 是 test262 的关键手法)。
	assertJS(t, `(function(){
		var r = /a/g, calls = 0;
		Object.defineProperty(r, "probe", { get: function(){ calls++; return 42; } });
		return r.probe + "/" + calls;
	})()`, "42/1")
}

// TestRegExpSymbolMatchSetLastIndexErr 是 8 个用例的语义内核:
// global / sticky 的 @@match 必须走 Set(rx,"lastIndex",v,true), 写不进去即抛
// TypeError (规范 22.2.5.6 步骤 8.c / 15.c)。
func TestRegExpSymbolMatchSetLastIndexErr(t *testing.T) {
	// g-init-lastindex-err: 步骤 8.c 的初始写入就失败。
	assertJSThrows(t, `(function(){
		var r = /./g;
		Object.defineProperty(r, "lastIndex", { writable: false });
		r[Symbol.match]("");
	})()`, "TypeError")
	// builtin-failure-y-set-lastindex-err: sticky 匹配失败后的写入失败。
	assertJSThrows(t, `(function(){
		var r = /a/y;
		Object.defineProperty(r, "lastIndex", { writable: false });
		r[Symbol.match]("ba");
	})()`, "TypeError")
	// builtin-success-g-set-lastindex-err: 靠 exec 的 getter 把 lastIndex
	// 改成不可写, 错误必须发生在**匹配成功之后**的那次写入。
	assertJSThrows(t, `(function(){
		var r = /b/g;
		Object.defineProperty(r, "exec", {
			get: function() { Object.defineProperty(r, "lastIndex", { writable: false }); }
		});
		r[Symbol.match]("abc");
	})()`, "TypeError")
}

// TestRegExpSymbolReplaceSetLastIndexErr 同上, 覆盖 @@replace 一侧。
func TestRegExpSymbolReplaceSetLastIndexErr(t *testing.T) {
	assertJSThrows(t, `(function(){
		var r = /c/y;
		Object.defineProperty(r, "lastIndex", { writable: false });
		r[Symbol.replace]("abc", "x");
	})()`, "TypeError")
	// result-coerce-groups-err: 用户覆写 exec 返回 groups: null ⇒
	// ToObject(null) 抛 TypeError。
	assertJSThrows(t, `(function(){
		var r = /./;
		r.exec = function() { return { length: 1, 0: '', index: 0, groups: null }; };
		r[Symbol.replace]("bar", "");
	})()`, "TypeError")
}

// TestRegExpMethodsRejectNonRegExpThis 覆盖规范步骤 1~2 的 this 校验。
//
// 此前 `%RegExp.prototype%` 不存在, 这些用例靠 `RegExp.prototype[Symbol.match]`
// 求值为 undefined 而"抛"出 TypeError 侥幸通过 (this-val-non-obj 一族)。
// 原型装配好之后必须由实现显式判定。
func TestRegExpMethodsRejectNonRegExpThis(t *testing.T) {
	for _, expr := range []string{
		`RegExp.prototype[Symbol.match].call(undefined)`,
		`RegExp.prototype[Symbol.match].call(null)`,
		`RegExp.prototype[Symbol.match].call(86)`,
		`RegExp.prototype[Symbol.match].call("string")`,
		`RegExp.prototype[Symbol.replace].call(undefined)`,
		`RegExp.prototype[Symbol.replace].call(86)`,
		`RegExp.prototype[Symbol.split].call(undefined)`,
		`RegExp.prototype[Symbol.split].call(86)`,
		// no-regexp-matcher.js: %RegExp.prototype% 自身没有 [[RegExpMatcher]]。
		`RegExp.prototype.exec("")`,
	} {
		assertJSThrows(t, expr, "TypeError")
	}
}

// TestRegExpSymbolMatchCoerceArgErr 锁住「ToString(参数) 的抛出不得被吞」。
//
// 实现陷阱: 经 object.CallFunction 调用 exec 会**清空**回调错误槽, 而
// ToString(参数) 阶段挂起的抛出正记在这个槽里 —— 无谓地调一次 CallFunction
// 就把那个抛出抹掉了 (coerce-arg-err 一族)。
func TestRegExpSymbolMatchCoerceArgErr(t *testing.T) {
	assertJSThrows(t, `/./[Symbol.match]({ toString: function() { throw new RangeError("boom"); } })`, "RangeError")
	assertJSThrows(t, `/./[Symbol.replace]({ toString: function() { throw new RangeError("boom"); } }, "x")`, "RangeError")
}

// TestRegExpLastIndexSemanticsUnchanged 是不变量护栏: 常规路径 (未改描述符)
// 的 lastIndex 推进 / exec 循环行为不得因这次改造而变。
func TestRegExpLastIndexSemanticsUnchanged(t *testing.T) {
	assertJS(t, `JSON.stringify("abc".match(/[a-c]/g))`, `["a","b","c"]`)
	assertJS(t, `(function(){ var r = /a/g; r.exec("aa"); return r.lastIndex; })()`, "1")
	assertJS(t, `(function(){ var r = /a/g; r.test("aa"); return r.lastIndex; })()`, "1")
	assertJS(t, `JSON.stringify("a1b2".split(/\d/))`, `["a","b",""]`)
	assertJS(t, `"abc".replace(/b/g, "X")`, "aXc")
	// 非 global 的 @@match 只返回首个匹配 (含捕获组 / index / input)。
	assertJS(t, `"abc".match(/b/).index`, "1")
}

// TestRegExpObjectPointerIdentity 直接验 Go 侧接口: *object.RegExp 必须满足
// OwnPropertyStore 与 PropDeleter, lastIndex 不可配置 ⇒ delete 返回 false。
func TestRegExpObjectPointerIdentity(t *testing.T) {
	re, err := object.NewRegExp("a", "g")
	if err != nil {
		t.Fatalf("NewRegExp: %v", err)
	}
	store, ok := interface{}(re).(object.OwnPropertyStore)
	if !ok {
		t.Fatalf("*object.RegExp must implement OwnPropertyStore")
	}
	if !store.HasOwn("lastIndex") {
		t.Fatalf("HasOwn(lastIndex) = false")
	}
	if store.HasOwn("source") {
		t.Fatalf("HasOwn(source) = true, want false (规范里它在原型上)")
	}
	deleter, ok := interface{}(re).(object.PropDeleter)
	if !ok {
		t.Fatalf("*object.RegExp must implement PropDeleter")
	}
	if deleter.DeleteOwn("lastIndex") {
		t.Fatalf("DeleteOwn(lastIndex) = true, want false (不可配置)")
	}
	// 严格写入: 可写时成功并把值落到字段上。
	if !re.SetLastIndexStrict(7) {
		t.Fatalf("SetLastIndexStrict(7) = false on a writable lastIndex")
	}
	if re.LastIndex != 7 {
		t.Fatalf("LastIndex = %d, want 7", re.LastIndex)
	}
	// 改成不可写之后, 严格写入必须失败且不改字段。
	d := object.PropertyDescriptor{Value: object.NewNumber(7), Writable: false, Enumerable: false, Configurable: false}
	if !store.DefineOwn("lastIndex", d) {
		t.Fatalf("DefineOwn returned false")
	}
	if re.SetLastIndexStrict(9) {
		t.Fatalf("SetLastIndexStrict = true on a non-writable lastIndex")
	}
	if re.LastIndex != 7 {
		t.Fatalf("LastIndex = %d, want 7 (失败写入不得改值)", re.LastIndex)
	}
}
