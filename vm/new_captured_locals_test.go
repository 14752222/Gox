package vm

import "testing"

// TestNewPreservesCapturedLocals 是 rIIXSR 的回归。
//
// 旧 OP_NEW 在给构造函数绑定 this 时用
// `&object.Closure{Fn, Env, This, IsArrow}` 重建了一个闭包, 却漏掉
// CapturedLocals ⇒ callClosure 装配新帧时没有外层捕获前缀 ⇒ 构造函数体里
// 对外层作用域绑定的 OP_LOAD(槽位) 读到 nil, 抛
// "Cannot access lexical declaration before initialization"。
//
// 触发面: 任何「构造函数引用 enclosing 作用域绑定」的类经 new 实例化。
// script 模式的顶层类因 super 编成 LOAD_GLOBAL 而侥幸绕开; module 模式
// (父类名走局部槽) 与嵌套在函数内的类都会中招。
//
// 期望值全部与 Node 实测一致 (node tdz_expect.js)。
func TestNewPreservesCapturedLocals(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"构造函数引用外层 const",
			`function outer(){ const tag = "T"; class Boom { constructor(){ this.v = tag + "!" } } return new Boom().v }
			 __out.push("1:" + outer());`,
			"1:T!\n",
		},
		{
			"嵌套类构造里的 super(实参)",
			`function make(){ class Base { constructor(n){ this.n = n } } class C extends Base { constructor(){ super(5) } } return new C().n }
			 __out.push("2:" + make());`,
			"2:5\n",
		},
		{
			"两层闭包捕获",
			`function lvl1(){ const a = "A"; return (function lvl2(){ const b = "B"; class K { constructor(){ this.s = a + b } } return new K().s })() }
			 __out.push("3:" + lvl1());`,
			"3:AB\n",
		},
		{
			"父类也定义在外层作用域",
			`function f4(){ class Base { constructor(){ this.p = "P" } } class Sub extends Base { constructor(){ super() } } return new Sub().p }
			 __out.push("4:" + f4());`,
			"4:P\n",
		},
		{
			// 守卫: 修复不得把真正的 TDZ 也一起放过 ——
			// 引用的外层 let 若在 new 之后才初始化, 仍须抛 ReferenceError。
			"真 TDZ 仍抛 ReferenceError",
			`function f5(){ class X { constructor(){ this.v = q } } try { new X() } catch (e) { __out.push("5:" + e.name) } let q = 1; return "ok" }
			 __out.push("5b:" + f5());`,
			"5:ReferenceError\n5b:ok\n",
		},
		{
			"外层 let 先初始化后 new 正常",
			`function f6(){ class X { constructor(){ this.v = q } } let q = 7; return new X().v }
			 __out.push("6:" + f6());`,
			"6:7\n",
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
