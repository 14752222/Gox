package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 同步 for-of 的 abrupt 完成与 IteratorClose (rseS4J) =====
//
// for-of 循环体经 break / return / throw 穿出时, 规范要求执行 IteratorClose
// (调 iterator.return(), 规范 7.4.6)。此前同步 for-of 完全不调 return(),
// test262 iterator-close-via-{break,return,throw} 全挂。口径全部由 node 22
// 与 test262 language/statements/for-of/iterator-close-* 钉死:
//
//   - 只在**已经取到迭代器之后**的 abrupt 完成才关闭; GetIterator 自身抛错
//     不关 (没有迭代器);
//   - break / return / body 抛异常都关; 正常 done 出口与 continue 不关;
//   - 标签 continue 跳出本循环 (L: do { for…continue L } while) 视为穿出, 要关;
//   - return 方法缺失 ⇒ 静默跳过; 非可调用 ⇒ TypeError;
//   - return 方法抛错: break/return 路径向上抛; body 抛异常路径原异常优先
//     (return 的异常被吞);
//   - return 是 getter 且 getter 抛错 ⇒ GetMethod abrupt, 同上优先级;
//   - return 返回非对象 ⇒ TypeError (非 throw completion 路径);
//   - 关闭后原完成值 (return 的值 / 原异常) 继续传播, 不被覆盖;
//   - 迭代器正常产出的 null/undefined 不再被误判成「迭代结束」(废弃的
//     JUMP_IF_NULL 启发式)。
//
// 测试脚本一律是**顶层**脚本 + 末表达式返回字符串 (与 destructuring_iter_test
// 同口径): 不把场景包进回调 —— gox 现有 depth>=3 的闭包捕获缺陷会让计数器
// 读到 0 (base 二进制同样复现, 与本特性无关)。

const foIterHelper = `
function mkFO(vals, hooks) {
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
// mkIt 用给定的 next / (可选) return 组一个可迭代对象。
function mkIt(nextFn, retFn) {
  var it = { next: nextFn };
  if (retFn) { it.return = retFn; }
  var o = {};
  o[Symbol.iterator] = function () { return it; };
  return o;
}
`

// foResult 跑一段顶层 for-of 脚本, 返回末表达式 (字符串)。
func foResult(t *testing.T, src string) string {
	t.Helper()
	v := evalWithStdlib(t, foIterHelper+src)
	s, ok := v.(*object.String)
	if !ok {
		t.Fatalf("结果应为字符串, got %T (%s)", v, v.Inspect())
	}
	return s.Value
}

// TestForOfIterCloseViaBreak: break 后调 return()。
func TestForOfIterCloseViaBreak(t *testing.T) {
	// 单次迭代 + break ⇒ closed=1
	got := foResult(t, `
		var st = { closed: 0, cnt: 0 };
		for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) {
			st.cnt = st.cnt + 1; break;
		}
		st.cnt + "|" + st.closed;
	`)
	if want := "1|1"; got != want {
		t.Errorf("break 应关闭迭代器\nwant %s got %s", want, got)
	}

	// 外层 try/catch 不应掩盖 for-of 体的正常异常
	got = foResult(t, `
		var E = new Error("body");
		var caught = null;
		try { for (const x of mkFO([1], {})) { throw E; } } catch (e) { caught = e; }
		(caught === E) + "";
	`)
	if want := "true"; got != want {
		t.Errorf("外层 catch 应抓到同一个异常对象\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseViaReturn: 函数内提前 return 时调 return(), 且 return 值
// 不被覆盖。
func TestForOfIterCloseViaReturn(t *testing.T) {
	got := foResult(t, `
		var st = { closed: 0 };
		function f() {
			for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) { return 42; }
			return 0;
		}
		var r = f();
		r + "|" + st.closed;
	`)
	if want := "42|1"; got != want {
		t.Errorf("return 应关闭迭代器且返回值不被覆盖\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseViaThrow: 循环体抛异常后调 return(), 且原异常胜出。
func TestForOfIterCloseViaThrow(t *testing.T) {
	got := foResult(t, `
		var st = { closed: 0 };
		var E = new Error("boom");
		var caught = null;
		try { for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) { throw E; } }
		catch (e) { caught = e; }
		(caught === E) + "|" + st.closed;
	`)
	if want := "true|1"; got != want {
		t.Errorf("throw 应关闭迭代器且原异常胜出\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseReturnMethodMissing: return 方法缺失 ⇒ 静默跳过。
func TestForOfIterCloseReturnMethodMissing(t *testing.T) {
	got := foResult(t, `
		var it = { next: function () { return { value: 1, done: false }; } };
		var o = {}; o[Symbol.iterator] = function () { return it; };
		var res = "ok";
		try { for (const x of o) { break; } } catch (e) { res = "threw"; }
		res;
	`)
	if want := "ok"; got != want {
		t.Errorf("缺 return 方法应静默跳过\nwant %s got %s", want, got)
	}

	// return 方法本身为 null / undefined 同样跳过 (GetMethod 把 null/undefined
	// 归一为 undefined)。注意区别于「return() 返回 null」—— 后者按规范
	// step 6 要 TypeError, 见 TestForOfIterCloseReturnMustReturnObject。
	got = foResult(t, `
		var it = { next: function () { return { value: 1, done: false }; }, return: null };
		var o = {}; o[Symbol.iterator] = function () { return it; };
		var res = "ok";
		try { for (const x of o) { break; } } catch (e) { res = e.name; }
		res;
	`)
	if want := "ok"; got != want {
		t.Errorf("return 方法为 null 应跳过\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseNonCallableReturn: return 非可调用 ⇒ TypeError。
func TestForOfIterCloseNonCallableReturn(t *testing.T) {
	got := foResult(t, `
		var it = { next: function () { return { value: 1, done: false }; }, return: 42 };
		var o = {}; o[Symbol.iterator] = function () { return it; };
		var res = "ok";
		try { for (const x of o) { break; } } catch (e) { res = e.name; }
		res;
	`)
	if want := "TypeError"; got != want {
		t.Errorf("return 非可调用应 TypeError\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseReturnMustReturnObject: return 返回非对象 ⇒ TypeError
// (非 throw completion 路径)。
func TestForOfIterCloseReturnMustReturnObject(t *testing.T) {
	got := foResult(t, `
		var res = "ok";
		try { for (const x of mkIt(function () { return { value: 1, done: false }; }, function () { return 42; })) { break; } }
		catch (e) { res = e.name; }
		res;
	`)
	if want := "TypeError"; got != want {
		t.Errorf("return 返回非对象应 TypeError\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseReturnThrows: return 抛错时, break 路径该错误上抛;
// body 抛异常路径原异常优先。
func TestForOfIterCloseReturnThrows(t *testing.T) {
	// break 路径: return 的异常照常传播
	got := foResult(t, `
		var E2 = new Error("ret-boom");
		var caught = null;
		try { for (const x of mkIt(function () { return { value: 1, done: false }; }, function () { throw E2; })) { break; } }
		catch (e) { caught = e; }
		(caught === E2) + "";
	`)
	if want := "true"; got != want {
		t.Errorf("break 路径 return 的异常应上抛\nwant %s got %s", want, got)
	}

	// throw 路径: 原异常优先, return 的异常被吞
	got = foResult(t, `
		var Eo = new Error("orig");
		var Er = new Error("ret");
		var caught = null;
		try { for (const x of mkIt(function () { return { value: 1, done: false }; }, function () { throw Er; })) { throw Eo; } }
		catch (e) { caught = e; }
		(caught === Eo ? "origWins" : "retWins");
	`)
	if want := "origWins"; got != want {
		t.Errorf("throw 路径原异常优先\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseReturnGetterAbrupt: return 是 getter 且 getter 抛错
// ⇒ GetMethod abrupt, 与 return 方法抛错同口径 (break 上抛 / throw 被吞)。
func TestForOfIterCloseReturnGetterAbrupt(t *testing.T) {
	got := foResult(t, `
		var it = { next: function () { return { value: 1, done: false }; }, get return() { throw new Error("getter"); } };
		var o = {}; o[Symbol.iterator] = function () { return it; };
		var caught = null;
		try { for (const x of o) { break; } } catch (e) { caught = e; }
		(caught ? "t:" + caught.message : "none");
	`)
	if want := "t:getter"; got != want {
		t.Errorf("break 路径 return getter 抛错应上抛\nwant %s got %s", want, got)
	}

	got = foResult(t, `
		var Eo = new Error("orig");
		var it = { next: function () { return { value: 1, done: false }; }, get return() { throw new Error("inner"); } };
		var o = {}; o[Symbol.iterator] = function () { return it; };
		var caught = null;
		try { for (const x of o) { throw Eo; } } catch (e) { caught = e; }
		(caught === Eo) + "";
	`)
	if want := "true"; got != want {
		t.Errorf("throw 路径原异常应优先于 getter 异常\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseGetIteratorThrows: GetIterator 自身抛错时没有迭代器,
// 不进入任何 close 路径。
func TestForOfIterCloseGetIteratorThrows(t *testing.T) {
	got := foResult(t, `
		var called = 0;
		var o = {};
		o[Symbol.iterator] = function () { throw new Error("getiter"); };
		o.return = function () { called = called + 1; return {}; };
		var caught = null;
		try { for (const x of o) { break; } } catch (e) { caught = e; }
		(caught ? "caught" : "none") + "|" + called;
	`)
	if want := "caught|0"; got != want {
		t.Errorf("GetIterator 抛错不应调 return\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseNormalDoneAndContinue: 正常 done 出口与 continue 不关闭。
func TestForOfIterCloseNormalDoneAndContinue(t *testing.T) {
	got := foResult(t, `
		var st = { closed: 0, cnt: 0 };
		for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) { st.cnt = st.cnt + 1; }
		st.cnt + "|" + st.closed;
	`)
	if want := "3|0"; got != want {
		t.Errorf("正常 done 出口不应关闭\nwant %s got %s", want, got)
	}

	got = foResult(t, `
		var st = { closed: 0, cnt: 0 };
		for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) { st.cnt = st.cnt + 1; continue; }
		st.cnt + "|" + st.closed;
	`)
	if want := "3|0"; got != want {
		t.Errorf("continue 不应关闭迭代器\nwant %s got %s", want, got)
	}

	// 标签 continue 跳出本循环 ⇒ 视为穿出, 要关闭 (test262 via-continue 口径)
	got = foResult(t, `
		var st = { closed: 0, cnt: 0 };
		L: do {
			for (const x of mkFO([1, 2, 3], { ret: function () { st.closed = st.closed + 1; } })) { st.cnt = st.cnt + 1; continue L; }
		} while (false);
		st.cnt + "|" + st.closed;
	`)
	if want := "1|1"; got != want {
		t.Errorf("标签 continue 跳出循环应关闭\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseNested: 嵌套 for-of 的 break 由内向外各自关闭。
func TestForOfIterCloseNested(t *testing.T) {
	got := foResult(t, `
		var si = { c: 0 }, so = { c: 0 };
		for (const a of mkFO([1, 2], { ret: function () { so.c = so.c + 1; } })) {
			for (const b of mkFO([1, 2], { ret: function () { si.c = si.c + 1; } })) { break; }
			break;
		}
		si.c + "|" + so.c;
	`)
	if want := "1|1"; got != want {
		t.Errorf("嵌套 for-of 应各关各的\nwant %s got %s", want, got)
	}
}

// TestForOfIterCloseThroughUserFinally: break/return 穿出带 finally 的 try 时,
// 用户 finally 与本轮 IteratorClose 都要执行。
func TestForOfIterCloseThroughUserFinally(t *testing.T) {
	// break 穿 finally
	got := foResult(t, `
		var fin = 0, closed = 0;
		var o = mkIt(function () { return { value: 1, done: false }; }, function () { closed = closed + 1; return {}; });
		for (const x of o) { try { break; } finally { fin = fin + 1; } }
		fin + "|" + closed;
	`)
	if want := "1|1"; got != want {
		t.Errorf("break 穿 finally: finally 与 close 都要跑\nwant %s got %s", want, got)
	}

	// return 穿 finally
	got = foResult(t, `
		var fin = 0, closed = 0;
		var o = mkIt(function () { return { value: 1, done: false }; }, function () { closed = closed + 1; return {}; });
		function g() { try { for (const x of o) { return 7; } } finally { fin = fin + 1; } return 0; }
		var r = g();
		r + "|" + fin + "|" + closed;
	`)
	if want := "7|1|1"; got != want {
		t.Errorf("return 穿 finally: 返回值/finally/close 三者都要对\nwant %s got %s", want, got)
	}

	// throw 穿 finally: 原异常优先, finally 与 close 都跑
	got = foResult(t, `
		var fin = 0, closed = 0;
		var E = new Error("x");
		var o = mkIt(function () { return { value: 1, done: false }; }, function () { closed = closed + 1; return {}; });
		var caught = null;
		try { for (const x of o) { try { throw E; } finally { fin = fin + 1; } } } catch (e) { caught = e; }
		(caught === E) + "|" + fin + "|" + closed;
	`)
	if want := "true|1|1"; got != want {
		t.Errorf("throw 穿 finally: 原异常优先且 finally/close 都跑\nwant %s got %s", want, got)
	}
}

// TestForOfValuesNullUndefinedNotDone: 迭代器正常产出的 null / undefined 不再
// 被当成「迭代结束」(旧的 JUMP_IF_NULL 启发式已废弃)。
func TestForOfValuesNullUndefinedNotDone(t *testing.T) {
	got := foResult(t, `
		var seen = [];
		for (const x of [1, null, undefined, 3]) { seen.push(String(x)); }
		seen.join(",");
	`)
	if want := "1,null,undefined,3"; got != want {
		t.Errorf("null/undefined 不应终止迭代\nwant %s got %s", want, got)
	}

	got = foResult(t, `
		var n = 0, cnt = 0;
		for (const x of mkIt(function () { n = n + 1; return { value: undefined, done: n > 3 }; }, null)) { cnt = cnt + 1; }
		n + "|" + cnt;
	`)
	if want := "4|3"; got != want {
		t.Errorf("产出 undefined 的迭代器应继续步进到 done\nwant %s got %s", want, got)
	}
}
