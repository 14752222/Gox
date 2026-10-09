package vm

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== IteratorClose 在「控制转移穿出」时的异常取舍 (看板 rHijPa) =====
//
// 规范 7.4.6 IteratorClose 的步骤 6~8:
//
//	6. If completion.[[type]] is throw, return Completion(completion).
//	7. If innerResult.[[type]] is throw, return Completion(innerResult).
//	8. If Type(innerResult.[[value]]) is not Object, throw a TypeError exception.
//
// 即: 穿出的是 **throw** 完成 ⇒ close 自身的错要丢 (步骤 6 早于 7/8);
// 穿出的是 **return / break / continue** 完成 ⇒ close 的错必须传出去。
//
// 修复前 compileArrayPatternBind 与同步 for-of 的收尾落点都是无条件
// swallow (PUSH_TRY + POP), 把「generator 的 return() 穿出解构 / for-of
// 时, iterator.return() 返回非对象 → TypeError」也一起吞了。
//
// 基准: Node v22 (v8 报 "Iterator result null is not an object")。
// 本文件的断言形状与 test262
// language/expressions/assignment/dstr/array-elem-trlg-iter-rest-rtrn-close-null.js
// 一致。

// evalProbeString 执行 src 并返回 __probe 收到的最后一个字符串观测值。
func evalProbeString(t *testing.T, src string) string {
	t.Helper()
	sink := evalWithProbe(t, src)
	v := sink.last()
	if v == nil {
		t.Fatalf("__probe 没有被调用")
	}
	s, ok := v.(*object.String)
	if !ok {
		t.Fatalf("__probe 末值是 %T, 期望 *object.String", v)
	}
	return s.Value
}

// probeOutcome 跑一段「可能抛」的脚本, 把结果归一成 "TypeError" / "no-throw"。
const probeOutcomeSrc = `
function mkiter(retVal) {
  var n = 0;
  return {
    next: function() { n += 1; return { done: n > 10 }; },
    return: function() { return retVal; }
  };
}
function mkvals(retVal) {
  var o = {};
  var it = mkiter(retVal);
  o[Symbol.iterator] = function() { return it; };
  return o;
}
function outcome(fn) {
  try { fn(); return "no-throw"; }
  catch (e) { return e.name; }
}
`

// TestIteratorCloseAbruptGeneratorReturnPropagates 断言 generator 的 return()
// 从 yield 点穿出解构赋值 / for-of 时, IteratorClose 的 TypeError **不被吞掉**。
func TestIteratorCloseAbruptGeneratorReturnPropagates(t *testing.T) {
	src := probeOutcomeSrc + `
// A: 解构赋值 (ArrayAssignmentPattern) 内含 yield
function* gDestr() {
  var x;
  var r = [ x, ...{}[yield] ] = mkvals(null);
}
var a = gDestr();
a.next();
__probe(outcome(function() { a.return(); }));

// B: 同步 for-of 内含 yield
function* gForOf() {
  for (var y of mkvals(null)) { yield; }
}
var b = gForOf();
b.next();
__probe(outcome(function() { b.return(); }));
`
	sink := evalWithProbe(t, src)
	if len(sink.args) != 2 {
		t.Fatalf("期望 2 个观测值, 实得 %d", len(sink.args))
	}
	for i, want := range []string{"TypeError", "TypeError"} {
		got, ok := sink.args[i].(*object.String)
		if !ok {
			t.Fatalf("观测值 %d 是 %T, 期望 *object.String", i, sink.args[i])
		}
		if got.Value != want {
			t.Errorf("形态 %d: 期望 %q, 实得 %q (Node v22 口径: 两者都应抛 TypeError)", i, want, got.Value)
		}
	}
}

// TestIteratorCloseAbruptThrowCompletionKeepsOriginal 断言反向不变式:
// 穿出的是 **throw** 完成时, close 自身的错必须丢弃, 原异常照原样传播。
func TestIteratorCloseAbruptThrowCompletionKeepsOriginal(t *testing.T) {
	src := probeOutcomeSrc + `
// 解构赋值里抛一个哨兵异常, 迭代器 return() 又返回非对象 (本会 TypeError)
function* gThrow() {
  var x;
  var r = [ x, ...{}[boom()] ] = mkvals(null);
}
function boom() { throw new RangeError("original"); }
var g = gThrow();
try {
  g.next();
  __probe("no-throw");
} catch (e) {
  __probe(e.name + ":" + e.message);
}
`
	got := evalProbeString(t, src)
	if got != "RangeError:original" {
		t.Errorf("期望原异常 RangeError:original 胜出, 实得 %q", got)
	}
}

// TestIteratorCloseAbruptBreakPropagates 断言 break 穿出 for-of 时
// (非 throw 完成) close 的 TypeError 同样要传播。
func TestIteratorCloseAbruptBreakPropagates(t *testing.T) {
	src := probeOutcomeSrc + `
__probe(outcome(function() {
  for (var y of mkvals(null)) { break; }
}));
`
	if got := evalProbeString(t, src); got != "TypeError" {
		t.Errorf("break 穿出: 期望 TypeError, 实得 %q", got)
	}
}

// TestIteratorCloseAbruptExhaustedIteratorNoClose 断言迭代器已耗尽时
// 收尾不调 return() —— 与修复前一致, 防止本次改动把「无需 close」的情形
// 误变成「必须 close」。
func TestIteratorCloseAbruptExhaustedIteratorNoClose(t *testing.T) {
	src := probeOutcomeSrc + `
// 目标数 >= 产出数: 迭代器被消费到 done ⇒ 槽内已清空 ⇒ 不调 return()
__probe(outcome(function() {
  var a, b;
  [a, b] = mkvals(null);
}));
`
	// mkvals 的 next 在 n > 10 才 done, 故这里**未**耗尽 —— 期望 TypeError。
	// 真正耗尽 (空数组) 的情形由下面的 probe 覆盖。
	if got := evalProbeString(t, src); got != "TypeError" {
		t.Errorf("未耗尽: 期望 TypeError, 实得 %q", got)
	}

	src2 := probeOutcomeSrc + `
__probe(outcome(function() {
  var a;
  [a] = [];   // 空数组: 迭代器一次就 done
}));
`
	if got := evalProbeString(t, src2); got != "no-throw" {
		t.Errorf("已耗尽 (空数组): 期望 no-throw, 实得 %q", got)
	}
}
