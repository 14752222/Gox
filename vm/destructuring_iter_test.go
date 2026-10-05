package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 数组解构的迭代器驱动与早退 IteratorClose (rKWmkc) =====
//
// 数组解构绑定/赋值改为走迭代器协议: GetIterator(xs) → 逐步 next() →
// done 判定 → 提前完成时调 return() (规范 7.4.6 IteratorClose)。口径全部由
// node 22 实测钉死 (见 F:/tmp/probe-dstr.mjs), 关键点:
//
//   - 非可迭代 (null / 42 / {} / 有 length 无 Symbol.iterator) → TypeError;
//   - GetIterator 自身抛错 → 不 close;
//   - 目标是数组字面量形状但迭代器产出得更少: 收到 done 后**不再步进**,
//     剩余绑定拿 undefined; 且因已耗尽, 收尾**不**调 return();
//   - 目标数与产出数恰好相等 (未真正读到 done): 收尾**要**调 return();
//   - rest 会一直步进到 done: 耗尽, 不 close;
//   - 绑定中途抛异常 (如默认值求值抛): close 一次, 原异常胜出 (close 自身
//     异常被吞); 正常路径上 close 抛异常则照常传播;
//   - return 非函数 / 缺失: 缺失跳过, 非函数 → TypeError;
//   - return 以迭代器为 this、无实参;
//   - 嵌套解构由内向外 close; generator 目标走 genReturn 触发 finally。
//
// 约束: 每个用例都是**顶层**脚本, 末表达式返回复合字符串, 与 node 输出逐字
// 对齐。不要把脚本包进回调 —— gox 现有 depth>=3 的闭包捕获缺陷会让计数器读
// 到 0 (base 二进制同样复现, 与本特性无关, 见 F:/tmp/probe-clo.js)。

const dstrIterHelper = `
function mkIter(vals, hooks) {
  var i = 0;
  var it = {
    next: function () {
      if (hooks && hooks.next) { hooks.next(); }
      if (i < vals.length) { var v = vals[i]; i = i + 1; return { value: v, done: false }; }
      return { value: undefined, done: true };
    },
    return: function () { if (hooks && hooks.ret) { hooks.ret(); } return {}; }
  };
  var o = {};
  o[Symbol.iterator] = function () { return it; };
  return o;
}
`

// dstrResult 跑一段顶层解构脚本, 返回末表达式 (字符串)。
func dstrResult(t *testing.T, src string) string {
	t.Helper()
	v := evalWithStdlib(t, dstrIterHelper+src)
	s, ok := v.(*object.String)
	if !ok {
		t.Fatalf("结果应为字符串, got %T (%s)", v, v.Inspect())
	}
	return s.Value
}

// TestDestructuringNonIterableTypeError: 非可迭代值一律 TypeError。
func TestDestructuringNonIterableTypeError(t *testing.T) {
	cases := map[string]string{
		`var res = "ok"; try { var [a] = { length: 1, 0: 'x' }; } catch (e) { res = e.name; } res;`: "TypeError",
		`var res = "ok"; try { var [a] = null; } catch (e) { res = e.name; } res;`:                   "TypeError",
		`var res = "ok"; try { var [a] = 42; } catch (e) { res = e.name; } res;`:                     "TypeError",
		`var res = "ok"; try { var [a] = {}; } catch (e) { res = e.name; } res;`:                     "TypeError",
	}
	for src, want := range cases {
		if got := dstrResult(t, src); got != want {
			t.Errorf("非可迭代应 TypeError\nsrc: %s\nwant: %s got: %s", src, want, got)
		}
	}
}

// TestDestructuringShortAndExhaust: 目标数与产出数的两种边界 —— 读到 done
// (short) 不再步进且不 close; 恰好取满 (exhaust) 未读到 done 要 close。
func TestDestructuringShortAndExhaust(t *testing.T) {
	short := `
		var n = 0, r = 0;
		var [a, b] = mkIter([1], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		"" + a + "|" + b + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, short), "1|undefined|2|0"; got != want {
		t.Errorf("short: 收到 done 后不再 next、耗尽不 close\nwant %s got %s", want, got)
	}

	exhaust := `
		var n = 0, r = 0;
		var [x, y] = mkIter([1, 2], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		"" + x + "|" + y + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, exhaust), "1|2|2|1"; got != want {
		t.Errorf("exhaust: 恰好取满未读到 done, 要 close\nwant %s got %s", want, got)
	}
}

// TestDestructuringAssignmentTarget: 赋值解构 (无声明) 与声明同一套口径。
func TestDestructuringAssignmentTarget(t *testing.T) {
	got := dstrResult(t, `
		var n = 0, r = 0;
		var a, b;
		[a, b] = mkIter([1], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		"" + a + "|" + b + "|" + n + "|" + r;
	`)
	if want := "1|undefined|2|0"; got != want {
		t.Errorf("赋值解构 short\nwant %s got %s", want, got)
	}
}

// TestDestructuringRestExhaustion: rest 步进到 done → 迭代器耗尽 → 不 close。
func TestDestructuringRestExhaustion(t *testing.T) {
	rest := `
		var n = 0, r = 0;
		var [x, ...y] = mkIter([1, 2, 3], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		"" + x + "|" + y.join(",") + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, rest), "1|2,3|4|0"; got != want {
		t.Errorf("rest 耗尽不 close\nwant %s got %s", want, got)
	}

	empty := `
		var n = 0, r = 0;
		var [...y] = mkIter([], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		"" + y.length + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, empty), "0|1|0"; got != want {
		t.Errorf("空 rest 一次 next 即 done\nwant %s got %s", want, got)
	}
}

// TestDestructuringThrowClosesIterator: 绑定中途抛 (默认值求值抛) → close 一次,
// 原异常胜出 (close 自身抛被吞)。
func TestDestructuringThrowClosesIterator(t *testing.T) {
	plain := `
		var n = 0, r = 0;
		var msg = "none";
		try {
			var [a = (function () { throw new Error("boom"); })()] = mkIter([undefined], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
		} catch (e) { msg = e.message; }
		msg + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, plain), "boom|1|1"; got != want {
		t.Errorf("默认值抛: close 一次 + 原异常\nwant %s got %s", want, got)
	}

	closeThrows := `
		var n = 0, r = 0;
		var msg = "none";
		var o = {};
		o[Symbol.iterator] = function () {
			return { next: function () { n = n + 1; return { done: false, value: undefined }; },
			         return: function () { r = r + 1; throw new Error("close"); } };
		};
		try {
			var [a = (function () { throw new Error("boom"); })()] = o;
		} catch (e) { msg = e.message; }
		msg + "|" + n + "|" + r;
	`
	if got, want := dstrResult(t, closeThrows), "boom|1|1"; got != want {
		t.Errorf("close 也抛时原异常胜出\nwant %s got %s", want, got)
	}
}

// TestDestructuringNormalCloseThrowsPropagates: 正常路径上 close 抛异常 → 传播。
func TestDestructuringNormalCloseThrowsPropagates(t *testing.T) {
	got := dstrResult(t, `
		var n = 0, r = 0;
		var msg = "none";
		var o = {};
		o[Symbol.iterator] = function () {
			return { next: function () { n = n + 1; return { done: false, value: 1 }; },
			         return: function () { r = r + 1; throw new Error("close"); } };
		};
		try { var [x] = o; } catch (e) { msg = e.message; }
		msg + "|" + n + "|" + r;
	`)
	if want := "close|1|1"; got != want {
		t.Errorf("正常路径 close 抛异常应传播\nwant %s got %s", want, got)
	}
}

// TestDestructuringElision: 空洞也占一次步进; 尾部空洞同理。
func TestDestructuringElision(t *testing.T) {
	cases := []struct{ src, want, why string }{
		{`
			var n = 0, r = 0;
			var [, b] = mkIter([1, 2], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
			"" + b + "|" + n + "|" + r;
		`, "2|2|1", "前导洞: 步进两次, 未耗尽 → close"},
		{`
			var n = 0, r = 0;
			var [, b] = mkIter([1], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
			"" + b + "|" + n + "|" + r;
		`, "undefined|2|0", "前导洞读尽 → 耗尽不 close"},
		{`
			var n = 0, r = 0;
			var [a, , ] = mkIter([1], { next: function () { n = n + 1; }, ret: function () { r = r + 1; } });
			"" + a + "|" + n + "|" + r;
		`, "1|2|0", "尾部洞: 步进到 done → 耗尽不 close"},
	}
	for _, c := range cases {
		if got := dstrResult(t, c.src); got != c.want {
			t.Errorf("%s\nwant %s got %s", c.why, c.want, got)
		}
	}
}

// TestDestructuringGetIteratorThrows: Symbol.iterator 取迭代器时抛 → 不 close。
func TestDestructuringGetIteratorThrows(t *testing.T) {
	got := dstrResult(t, `
		var n = 0;
		var msg = "none";
		var o = {};
		o[Symbol.iterator] = function () { n = n + 1; throw new Error("getiter"); };
		try { var [a] = o; } catch (e) { msg = e.message; }
		msg + "|" + n;
	`)
	if want := "getiter|1"; got != want {
		t.Errorf("GetIterator 抛错不 close\nwant %s got %s", want, got)
	}
}

// TestDestructuringNextNonObjectTypeError: next() 返回非 Object → TypeError
// (规范 7.4.2 IteratorNext step 2)。yield* 同口径 —— 见 test262
// star-rhs-iter-nrml-next-call-non-obj。
func TestDestructuringNextNonObjectTypeError(t *testing.T) {
	got := dstrResult(t, `
		var msg = "ok";
		var o = {};
		o[Symbol.iterator] = function () {
			return { next: function () { return 8; } };
		};
		try { var [a] = o; } catch (e) { msg = e.name; }
		msg;
	`)
	if want := "TypeError"; got != want {
		t.Errorf("next 返回非对象应 TypeError\nwant %s got %s", want, got)
	}
}

// TestDestructuringReturnNonCallable: return 存在但非函数 → TypeError (必须调)。
func TestDestructuringReturnNonCallable(t *testing.T) {
	got := dstrResult(t, `
		var msg = "ok";
		var o = {};
		o[Symbol.iterator] = function () {
			return { next: function () { return { value: 9, done: false }; }, return: 42 };
		};
		try { var [a] = o; } catch (e) { msg = e.name; }
		msg;
	`)
	if want := "TypeError"; got != want {
		t.Errorf("return 非函数应 TypeError\nwant %s got %s", want, got)
	}
}

// TestDestructuringReturnMustReturnObject: 规范 7.4.6 step 9 —— return() 的
// 返回值必须 Object, 否则 TypeError (null/undefined/数字都算); 数组/对象通过。
func TestDestructuringReturnMustReturnObject(t *testing.T) {
	mk := func(retExpr string) string {
		return `
			var msg = "ok";
			var o = {};
			o[Symbol.iterator] = function () {
				return { next: function () { return { value: 1, done: false }; }, return: ` + retExpr + ` };
			};
			try { var [a] = o; } catch (e) { msg = e.name; }
			msg;
		`
	}
	cases := []struct {
		retExpr, want string
	}{
		{"function () { return null; }", "TypeError"},
		{"function () { return undefined; }", "TypeError"},
		{"function () { }", "TypeError"}, // 隐式返回 undefined
		{"function () { return 5; }", "TypeError"},
		{"function () { return {}; }", "ok"},
		{"function () { return []; }", "ok"},
	}
	for _, c := range cases {
		if got := dstrResult(t, mk(c.retExpr)); got != c.want {
			t.Errorf("return %s: want %s got %s", c.retExpr, c.want, got)
		}
	}
}

// TestDestructuringReturnThisAndArgs: return 以迭代器为 this、无实参。
func TestDestructuringReturnThisAndArgs(t *testing.T) {
	got := dstrResult(t, `
		var thisVal = null, argsLen = null;
		var it = { next: function () { return { value: 1, done: false }; },
		           return: function () { thisVal = this; argsLen = arguments.length; return {}; } };
		var o = {};
		o[Symbol.iterator] = function () { return it; };
		var [x] = o;
		"" + (thisVal === it) + "|" + argsLen + "|" + x;
	`)
	if want := "true|0|1"; got != want {
		t.Errorf("return 的 this/实参\nwant %s got %s", want, got)
	}
}

// TestDestructuringNestedCloseOrder: 嵌套解构由内向外 close。
func TestDestructuringNestedCloseOrder(t *testing.T) {
	got := dstrResult(t, `
		var order = [];
		var inner = { next: function () { return { value: 1, done: false }; }, return: function () { order.push("inner"); return {}; } };
		var ino = {}; ino[Symbol.iterator] = function () { return inner; };
		var outer = { next: function () { return { value: ino, done: false }; }, return: function () { order.push("outer"); return {}; } };
		var oo = {}; oo[Symbol.iterator] = function () { return outer; };
		var [[a]] = oo;
		order.join(",") + "|" + a;
	`)
	if want := "inner,outer|1"; got != want {
		t.Errorf("嵌套 close 由内向外\nwant %s got %s", want, got)
	}
}

// TestDestructuringStringAndArrayHole: 字符串是天然可迭代; 数组自身带洞照旧。
func TestDestructuringStringAndArrayHole(t *testing.T) {
	if got, want := dstrResult(t, `var [c1, c2] = "ab"; "" + c1 + "|" + c2;`), "a|b"; got != want {
		t.Errorf("字符串解构\nwant %s got %s", want, got)
	}
	if got, want := dstrResult(t, `var [x, y, z] = [1, , 3]; "" + x + "|" + y + "|" + z;`), "1|undefined|3"; got != want {
		t.Errorf("数组洞作为源\nwant %s got %s", want, got)
	}
}

// TestDestructuringGeneratorClose: generator 目标的 close 走 gen.return,
// 触发其 finally。
func TestDestructuringGeneratorClose(t *testing.T) {
	got := dstrResult(t, `
		var closed = false;
		function* g() { try { yield 1; yield 2; } finally { closed = true; } }
		var [x] = g();
		"" + x + "|" + closed;
	`)
	if want := "1|true"; got != want {
		t.Errorf("generator 目标 close 触发 finally\nwant %s got %s", want, got)
	}
}
