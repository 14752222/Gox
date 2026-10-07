package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 数组解构尊重被覆盖的 Array.prototype[Symbol.iterator] (看板 ryPk0S) =====
//
// 根因: 数组此前没有 Symbol 键属性槽 —— arr[Symbol.iterator] = fn 被 vm.setIndex
// 的 *Array 分支静默丢弃, vm.resolveSymbolIterator 又只接受 *object.Object, 于是
// 数组解构/for-of 永远走 Go 层索引快路径, 无视被用户覆盖的 @@iterator。
//
// 规范口径 (GetIterator(target, sync)): 取 target[Symbol.iterator] (沿原型链) →
// 调用拿迭代器 → 循环 next()。数组只是"恰好是类数组", 绝不能因此绕过迭代协议。
//
// 每个用例都是**顶层**脚本, 末表达式返回复合字符串 (与现有 dstrIterHelper 风格
// 一致: 避免把脚本包进深层回调, gox 现有 depth>=3 闭包捕获缺陷与本特性无关)。

// arrDstrResult 跑一段顶层脚本, 返回末表达式 (字符串)。
func arrDstrResult(t *testing.T, src string) string {
	t.Helper()
	v := evalWithStdlib(t, src)
	s, ok := v.(*object.String)
	if !ok {
		t.Fatalf("结果应为字符串, got %T (%s)", v, v.Inspect())
	}
	return s.Value
}

// TestArrayDstrOverriddenIteratorHonored: 覆盖 arr[Symbol.iterator] 后, 数组
// 解构必须走迭代器协议 —— 迭代器立即 done 则解构得 undefined (而非 arr[0])。
func TestArrayDstrOverriddenIteratorHonored(t *testing.T) {
	got := arrDstrResult(t, `
		var arr = [1, 2, 3];
		arr[Symbol.iterator] = function () { return { next: function () { return { done: true }; } }; };
		var [first] = arr;
		"" + first;
	`)
	if want := "undefined"; got != want {
		t.Errorf("覆盖 @@iterator 后数组解构应得 undefined\nwant %s got %s", want, got)
	}
}

// TestArrayDstrOverriddenIteratorDrivesValues: 覆盖后由自定义迭代器产出的值
// 才是解构结果 (完全不走索引)。
func TestArrayDstrOverriddenIteratorDrivesValues(t *testing.T) {
	got := arrDstrResult(t, `
		var arr = [1, 2, 3];
		arr[Symbol.iterator] = function () {
			var n = 0;
			return { next: function () { n = n + 1; return n <= 2 ? { value: n * 10, done: false } : { done: true }; } };
		};
		var [a, b, c] = arr;
		"" + a + "|" + b + "|" + c;
	`)
	if want := "10|20|undefined"; got != want {
		t.Errorf("自定义迭代器驱动解构\nwant %s got %s", want, got)
	}
}

// TestArrayDstrOverriddenForOf: for-of 同样尊重被覆盖的 @@iterator。
func TestArrayDstrOverriddenForOf(t *testing.T) {
	got := arrDstrResult(t, `
		var arr = [1, 2, 3];
		arr[Symbol.iterator] = function () { return { next: function () { return { done: true }; } }; };
		var seen = [];
		for (var v of arr) { seen.push(v); }
		"" + seen.length;
	`)
	if want := "0"; got != want {
		t.Errorf("for-of 应尊重被覆盖的 @@iterator\nwant %s got %s", want, got)
	}
}

// TestArrayDstrPrototypeIteratorOverridden: 用户覆盖 Array.prototype[
// Symbol.iterator] 时, 未自带覆盖的数组实例也应受影响 (沿原型链查找)。
func TestArrayDstrPrototypeIteratorOverridden(t *testing.T) {
	got := arrDstrResult(t, `
		var orig = Array.prototype[Symbol.iterator];
		Array.prototype[Symbol.iterator] = function () { return { next: function () { return { done: true }; } }; };
		var [x] = [1, 2, 3];
		Array.prototype[Symbol.iterator] = orig;
		"" + x;
	`)
	if want := "undefined"; got != want {
		t.Errorf("覆盖 Array.prototype[@@iterator] 应影响数组解构\nwant %s got %s", want, got)
	}
}

// TestArrayDstrNonCallableIteratorTypeError: @@iterator 存在但非函数 → TypeError
// (不得静默退回索引快路径)。
func TestArrayDstrNonCallableIteratorTypeError(t *testing.T) {
	got := arrDstrResult(t, `
		var msg = "ok";
		var arr = [1, 2, 3];
		arr[Symbol.iterator] = 42;
		try { var [a] = arr; } catch (e) { msg = e.name; }
		msg;
	`)
	if want := "TypeError"; got != want {
		t.Errorf("非可调用 @@iterator 应 TypeError\nwant %s got %s", want, got)
	}
}

// TestArrayDstrIteratorClose: 解构取不满 (迭代器未耗尽) 时必须调 return()。
func TestArrayDstrIteratorClose(t *testing.T) {
	got := arrDstrResult(t, `
		var closed = false;
		var arr = [1, 2, 3];
		arr[Symbol.iterator] = function () {
			var n = 0;
			return { next: function () { n = n + 1; return { value: n, done: false }; },
			         return: function () { closed = true; return {}; } };
		};
		var [a] = arr;
		"" + a + "|" + closed;
	`)
	if want := "1|true"; got != want {
		t.Errorf("数组解构未耗尽应 close\nwant %s got %s", want, got)
	}
}

// TestArrayDstrStillWorksForPlainArray: 未覆盖的普通数组解构/for-of 照旧
// (走默认 @@iterator 或 Go 层兜底, 结果不变)。
func TestArrayDstrStillWorksForPlainArray(t *testing.T) {
	if got, want := arrDstrResult(t, `var [x, y] = [1, 2, 3]; "" + x + "|" + y;`), "1|2"; got != want {
		t.Errorf("普通数组解构\nwant %s got %s", want, got)
	}
	if got, want := arrDstrResult(t, `var t = 0; for (var v of [1, 2, 3]) { t = t + v; } "" + t;`), "6"; got != want {
		t.Errorf("普通数组 for-of\nwant %s got %s", want, got)
	}
}

// TestArrayDstrSymbolPropBasics: 数组符号键属性存取的基础语义 (本次修复的底层)。
func TestArrayDstrSymbolPropBasics(t *testing.T) {
	got := arrDstrResult(t, `
		var arr = [1, 2, 3];
		var fn = function () { return 1; };
		arr[Symbol.iterator] = fn;
		var readBack = arr[Symbol.iterator];
		var syms = Object.getOwnPropertySymbols(arr);
		"" + (readBack === fn) + "|" + syms.length;
	`)
	if want := "true|1"; got != want {
		t.Errorf("数组符号键读写\nwant %s got %s", want, got)
	}
}

// TestArrayDstrDefinePropertySymbol: Object.defineProperty 的符号键形态在数组上
// 也要落地 (此前被静默忽略)。
func TestArrayDstrDefinePropertySymbol(t *testing.T) {
	got := arrDstrResult(t, `
		var arr = [1, 2, 3];
		var fn = function () { return { next: function () { return { done: true }; } }; };
		Object.defineProperty(arr, Symbol.iterator, { value: fn, configurable: true });
		var [a] = arr;
		var d = Object.getOwnPropertyDescriptor(arr, Symbol.iterator);
		"" + a + "|" + (d !== undefined);
	`)
	if want := "undefined|true"; got != want {
		t.Errorf("defineProperty(数组, 符号键)\nwant %s got %s", want, got)
	}
}
