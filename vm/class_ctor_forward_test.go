package vm

import "testing"

// rCzckg 回归：隐式 constructor 必须转发父类构造，语义等价于
// constructor(...args){ super(...args) }。期望值全部取自 Node 实测。
//
// 依赖的两处既有缺陷已随本单一起修（2026-10-05）：
//   - 父类与子类在**同一嵌套函数作用域**内时 super() 报 TDZ —— rIIXSR，
//     根因是 OP_NEW 重建闭包时丢了 CapturedLocals；见
//     vm/new_captured_locals_test.go。
//   - 构造器抛异常时异常逃出外层 catch —— rVI6Eb，根因是 OP_NEW 的错误
//     直接 return 未过 handleThrow；见 vm/new_throw_catch_test.go。
// 下面用例主要在顶层脚本（与单子原始复现形态一致），另含一条嵌套作用域用例。
func TestImplicitConstructorForwardsToSuper(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			"父类构造执行且实例字段存在",
			`
			var ran = 0;
			class D { constructor() { ran++; this.d = 1 } m() { return "dm" } }
			class F extends D {}
			var f = new F();
			__out.push(ran); __out.push(f.d); __out.push(f.m()); __out.push(f instanceof D);
			`,
			"1\n1\ndm\ntrue\n",
		},
		{
			"实参转发给父构造",
			`
			class D { constructor(a, b) { this.a = a; this.b = b } }
			class F extends D {}
			var f = new F(1, 2);
			__out.push(f.a); __out.push(f.b);
			`,
			"1\n2\n",
		},
		{
			"父类无显式 constructor",
			`
			class D {}
			class F extends D {}
			__out.push(new F() instanceof D);
			`,
			"true\n",
		},
		{
			"三层继承链父构造只执行一次",
			`
			var ran = 0;
			class A { constructor(x) { ran++; this.x = x } }
			class B extends A {}
			class C extends B {}
			var c = new C(7);
			__out.push(ran); __out.push(c.x); __out.push(c instanceof A);
			`,
			"1\n7\ntrue\n",
		},
		{
			"子类字段在父字段之后初始化(同名子类赢)",
			`
			class D { constructor() { this.v = "D"; this.onlyD = 1 } }
			class F extends D { constructor() { super(); this.v = "F"; this.onlyF = 2 } }
			var f = new F();
			__out.push(f.v); __out.push(f.onlyD); __out.push(f.onlyF);
			`,
			"F\n1\n2\n",
		},
		{
			"隐式子类继承父字段且自有字段仍生效",
			`
			class D { constructor() { this.v = "D" } }
			class F extends D { x = 9 }
			var f = new F();
			__out.push(f.v); __out.push(f.x);
			`,
			"D\n9\n",
		},
		{
			"用户手写 super(...arguments)",
			`
			class D { constructor(a, b) { this.a = a; this.b = b } }
			class F extends D { constructor() { super(...arguments) } }
			var f = new F(3, 4);
			__out.push(f.a); __out.push(f.b);
			`,
			"3\n4\n",
		},
		{
			"用户手写 super(...rest)",
			`
			class D { constructor(a, b, c) { this.sum = a + b + c } }
			class F extends D { constructor() { var r = [1, 2, 3]; super(...r) } }
			__out.push(new F().sum);
			`,
			"6\n",
		},
		{
			"显式 super(定长实参) 不回归",
			`
			class D { constructor(a, b) { this.a = a; this.b = b } }
			class F extends D { constructor() { super(8, 9) } }
			var f = new F();
			__out.push(f.a); __out.push(f.b);
			`,
			"8\n9\n",
		},
		{
			"隐式构造下原型方法可调用",
			`
			class D { constructor() { this.k = 5 } hi() { return "hi" + this.k } }
			class F extends D {}
			__out.push(new F().hi());
			`,
			"hi5\n",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := evalOut(t, tc.src); got != tc.want {
				t.Fatalf("got=%q want=%q", got, tc.want)
			}
		})
	}
}

// TestExplicitSuperSpreadWithoutSpreadArgsStillUsesMethodCall 守卫：
// 无 spread 的 super(...) 仍走定长 OP_CALL_METHOD 路径（this 绑定 + 返回值
// 语义不变），只有带 spread 时才切到 OP_CALL_METHOD_SPREAD。
func TestExplicitSuperSpreadWithoutSpreadArgsStillUsesMethodCall(t *testing.T) {
	got := evalOut(t, `
		class D { constructor(v) { this.v = v } }
		class F extends D { constructor() { super(1 + 2) } }
		__out.push(new F().v);
	`)
	if got != "3\n" {
		t.Fatalf("got=%q want=%q", got, "3\n")
	}
}

// TestImplicitCtorInNestedScopeForwardsToSuper 是 rCzckg × rIIXSR 的交叉回归:
// 父/子类声明在**同一嵌套函数作用域**内 —— 这正是 rIIXSR 曾让 super() 报 TDZ
// 的形状。隐式构造转发必须在这里也成立。
func TestImplicitCtorInNestedScopeForwardsToSuper(t *testing.T) {
	got := evalOut(t, `
		function make(){ class Base { constructor(n){ this.n = n } } class C extends Base {} return new C(5).n }
		__out.push("nested:" + make());
	`)
	if got != "nested:5\n" {
		t.Fatalf("got=%q want=%q", got, "nested:5\n")
	}
}

// TestImplicitCtorParentThrowCaughtByCaller 是 rCzckg × rVI6Eb 的交叉回归:
// 隐式构造转发 super 后, 父构造抛出的异常必须能被 `new 子类` 的调用方 catch
// (rVI6Eb 曾让 new 路径上的异常穿出外层 catch)。
func TestImplicitCtorParentThrowCaughtByCaller(t *testing.T) {
	got := evalOut(t, `
		class P2 { constructor(){ throw new Error("p2") } }
		class Q2 extends P2 { }
		try { new Q2(); __out.push("no") } catch (e) { __out.push("caught:" + e.message) }
		function wrap(){ class P3 { constructor(){ throw new Error("p3") } } class Q3 extends P3 {} try { new Q3() } catch (e) { return "inner:" + e.message } return "no" }
		__out.push(wrap());
	`)
	if got != "caught:p2\ninner:p3\n" {
		t.Fatalf("got=%q want=%q", got, "caught:p2\ninner:p3\n")
	}
}
