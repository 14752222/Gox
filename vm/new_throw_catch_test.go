package vm

import "testing"

// TestNewErrorRoutedThroughThrow 是 rVI6Eb 的回归。
//
// 旧 OP_NEW 用嵌套 runFrom 跑构造帧, 异常从嵌套 runLoop 返回后**直接 return**,
// 未经 handleThrow ⇒ 外层 try 条目从未被咨询, 异常穿出调用方预期。
// (OP_CALL / OP_CALL_METHOD 一族都走 throwJSError/handleThrow, 只有 new 漏了。)
//
// 期望值全部与 Node 实测一致。
func TestNewErrorRoutedThroughThrow(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"顶层 new 抛错可被 catch",
			`class DX { constructor(){ throw new Error("x") } }
			 try { new DX(); __out.push("no") } catch (e) { __out.push("caught:" + e.message) }`,
			"caught:x\n",
		},
		{
			"函数内 new 抛错可被 catch",
			`function g(){ class DY { constructor(){ throw new Error("y") } } try { new DY(); __out.push("no") } catch (e) { __out.push("caught2:" + e.message) } }
			 g();`,
			"caught2:y\n",
		},
		{
			"显式 super() 里父构造抛错可被 new 子类的调用方 catch",
			`class P { constructor(){ throw new Error("p") } }
			 class Q extends P { constructor(){ super() } }
			 try { new Q(); __out.push("no") } catch (e) { __out.push("caught3:" + e.message) }`,
			"caught3:p\n",
		},
		{
			// 守卫: 未被捕获时仍须向外传播 (不得被吞掉)。
			"未被 catch 时仍向外传播",
			`class DZ { constructor(){ throw new Error("un") } }
			 function f(){ try { new DZ() } finally { __out.push("fin") } }
			 try { f() } catch (e) { __out.push("outer:" + e.message) }`,
			"fin\nouter:un\n",
		},
		{
			// 守卫: new 之后构造帧被回收 —— 后续普通代码仍能正常执行。
			"catch 之后同帧代码继续正常执行",
			`class DW { constructor(){ throw new Error("w") } }
			 try { new DW() } catch (e) { __out.push("c:" + e.message) }
			 function after(){ return "ok" }
			 __out.push("after:" + after());`,
			"c:w\nafter:ok\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := evalOut(t, tc.src); got != tc.want {
				t.Errorf("want:\n%s\ngot:\n%s", tc.want, got)
			}
		})
	}
}
