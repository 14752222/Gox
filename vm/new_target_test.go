package vm

import (
	"testing"
)

// ===== new.target 元属性 (rvi5ZG, 2026-10-06) =====
//
// 期望值全部对齐 Node 22 实测。覆盖: 普通调用 undefined / new 调用为构造器 /
// super() 沿链传递 (含两跳与隐式构造) / apply·call·reflect.apply·tagged template
// ·getter 均为 undefined / new 与 .target 间的空白·换行·注释 / 一元前缀运算 /
// 箭头词法继承 / Reflect.construct 的 newTarget 桥 / 直接 eval 继承 (rNAtZs)。
//
// 已知边界 (见文件末尾注释): 类字段初始化器内、类静态初始化块内的 new.target。

// TestNewTargetValues new.target 的取值语义。
func TestNewTargetValues(t *testing.T) {
	got := evalOut(t, `
		function plain() { return new.target; }
		__out.push('plain:' + plain());

		function Ctor() { this.nt = new.target; }
		__out.push('new:' + (new Ctor().nt === Ctor));

		class Base { constructor() { this.nt = new.target; } }
		class Derived extends Base {}
		let d = new Derived();
		__out.push('sub-derived:' + (d.nt === Derived));
		__out.push('sub-not-base:' + (d.nt === Base));

		class A { constructor() { this.nt = new.target; } }
		class B extends A {}
		class C extends B {}
		__out.push('twohop:' + (new C().nt === C));

		// 隐式构造器 (无显式 constructor) 的 super(...arguments) 也传递
		class ImplicitBase { constructor() { this.nt = new.target; } }
		class ImplicitDerived extends ImplicitBase {}
		__out.push('implicit:' + (new ImplicitDerived().nt === ImplicitDerived));

		let nt = null;
		function f() { nt = new.target; }
		f(); __out.push('call:' + nt);
		f.apply({}); __out.push('apply:' + nt);
		f.call({}); __out.push('call2:' + nt);
		Reflect.apply(f, {}, []); __out.push('reflectapply:' + nt);

		let g = null;
		let obj = { get m() { g = new.target; } };
		obj.m;
		__out.push('getter:' + g);
	`)
	want := "plain:undefined\n" +
		"new:true\n" +
		"sub-derived:true\n" +
		"sub-not-base:false\n" +
		"twohop:true\n" +
		"implicit:true\n" +
		"call:undefined\n" +
		"apply:undefined\n" +
		"call2:undefined\n" +
		"reflectapply:undefined\n" +
		"getter:undefined\n"
	if got != want {
		t.Errorf("new.target 取值语义\nwant:\n%s\ngot:\n%s", want, got)
	}

	// tagged template 调用: new.target = undefined。
	assertJS(t, "(function () { let r; function f() { r = new.target; } f`t`; return String(r); })()", "undefined")
}

// TestNewTargetASI new.target 的三个 token 之间允许空白/换行/注释
// (规范 NewTarget 无 [no LineTerminator here] 限制; test262 new.target/asi.js)。
func TestNewTargetASI(t *testing.T) {
	got := evalOut(t, `
		let nt = null;
		let withSpaces = function() { nt = new   .   target; };
		withSpaces(); __out.push('spaces:' + nt);
		new withSpaces(); __out.push('spaces-new:' + (nt === withSpaces));

		let withLineBreaks = function() {
			nt = new

.

target;
		};
		withLineBreaks(); __out.push('lines:' + nt);
		new withLineBreaks(); __out.push('lines-new:' + (nt === withLineBreaks));

		let withComments = function() { nt = new/* */./* */target; };
		withComments(); __out.push('comments:' + nt);
		new withComments(); __out.push('comments-new:' + (nt === withComments));
	`)
	want := "spaces:undefined\n" +
		"spaces-new:true\n" +
		"lines:undefined\n" +
		"lines-new:true\n" +
		"comments:undefined\n" +
		"comments-new:true\n"
	if got != want {
		t.Errorf("new.target token 分隔\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNewTargetUnary 一元/前缀运算作用于 new.target (test262 unary-expr.js)。
func TestNewTargetUnary(t *testing.T) {
	got := evalOut(t, `
		__out.push('delete:' + (function() { return delete (new.target); })());
		__out.push('void:' + (function() { return void new.target; })());
		__out.push('not:' + (function() { return !new.target; })());
		__out.push('bitnot:' + (function() { return ~new.target; })());
		__out.push('neg:' + (function() { return -(new.target); })());
		function T() { this.v = typeof new.target; }
		__out.push('typeof-new:' + (new T()).v);
		function Pp() { this.v = +(new.target); }
		__out.push('plus-new:' + (new Pp()).v);
		function Chain() { this.v = delete void typeof +-~!(new.target); }
		__out.push('chain-new:' + (new Chain()).v);
	`)
	want := "delete:true\n" +
		"void:undefined\n" +
		"not:true\n" +
		"bitnot:-1\n" +
		"neg:NaN\n" +
		"typeof-new:function\n" +
		"plus-new:NaN\n" +
		"chain-new:true\n"
	if got != want {
		t.Errorf("new.target 一元运算\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNewTargetSyntaxErrors new.target 的解析期早错 (spec 15.1.1 / AssignmentTargetType
// / UpdateExpression 早错 / 关键字不得含 Unicode 转义)。
func TestNewTargetSyntaxErrors(t *testing.T) {
	bad := []string{
		"new.target;",                        // 脚本顶层
		"() => { new.target; };",             // 顶层箭头 (Contains 冒泡到脚本)
		"function f() { new.target = 1; }",   // AssignmentTargetType = invalid
		"function f() { (new.target) = 1; }", // covered 形态同样 invalid
		"function f() { new.target++; }",     // 后缀自增
		"function f() { new.target--; }",     // 后缀自减
		"function f() { ++(new.target); }",   // 前缀自增 (covered)
		"function f() { --(new.target); }",   // 前缀自减 (covered)
		`function f() { new.t\u0061rget; }`,  // target 不得含转义
		`function f() { n\u0065w.target; }`,  // new 不得含转义
	}
	for _, src := range bad {
		if _, err := EvalVM(src); err == nil {
			t.Errorf("应报 SyntaxError, 但解析通过: %q", src)
		}
	}
}

// TestNewTargetParseAccepted 合法形态必须能解析 (不报错), 含函数内箭头
// (箭头对 new.target 词法透明 —— 解析合法性继承外层函数)。
func TestNewTargetParseAccepted(t *testing.T) {
	good := []string{
		"function f() { return new.target; }",
		"function f() { return (() => new.target)(); }",
		"function f() { return () => new.target; }",
		"let o = { m() { return new.target; } };",
		"class C { m() { return new.target; } }",
		"class C { constructor() { this.x = new.target; } }",
		"class C { get x() { return new.target; } }",
	}
	for _, src := range good {
		if _, err := EvalVM(src); err != nil {
			t.Errorf("应能解析/执行, 但报错: %q -> %v", src, err)
		}
	}
}

// TestNewTargetArrowLexical 箭头函数对 new.target 词法透明: 在**创建时**捕获
// 所在帧的 new.target, 被返回后于别处调用仍取到创建处的值
// (test262 lexical-new.target.js / lexical-new.target-closure-returned.js)。
func TestNewTargetArrowLexical(t *testing.T) {
	got := evalOut(t, `
		function F() {
			this.af = () => (new.target ? 1 : 2);
		}
		__out.push('returned-closure:' + new F().af());

		let functionInvocationCount = 0;
		let newInvocationCount = 0;
		function G() {
			if ((() => new.target)() !== undefined) { newInvocationCount++; }
			functionInvocationCount++;
		}
		G(); new G();
		__out.push('fcount:' + functionInvocationCount);
		__out.push('ncount:' + newInvocationCount);

		// 箭头嵌箭头: 逐层词法继承。
		function H() { this.f = () => (() => new.target)(); }
		__out.push('nested-arrow:' + (new H().f() === H));

		// 箭头在普通函数内, 但该函数被普通调用 → undefined。
		let plain = function () { return () => new.target; };
		__out.push('normal-call:' + plain()());
	`)
	want := "returned-closure:1\n" +
		"fcount:2\n" +
		"ncount:1\n" +
		"nested-arrow:true\n" +
		"normal-call:undefined\n"
	if got != want {
		t.Errorf("箭头词法继承 new.target\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNewTargetReflectConstruct Reflect.construct 的 newTarget 桥: 省略时等于
// target, 显式给定时构造器内 new.target 即该值, 且实例原型取自 newTarget.prototype
// (test262 value-via-reflect-construct.js)。
func TestNewTargetReflectConstruct(t *testing.T) {
	got := evalOut(t, `
		let nt = null;
		let custom = function () {};
		custom.prototype = { tag: 'custom' };
		function f() { nt = new.target; }

		Reflect.construct(f, []);
		__out.push('default:' + (nt === f));

		Reflect.construct(f, [], custom);
		__out.push('explicit:' + (nt === custom));

		let inst = Reflect.construct(f, [], custom);
		__out.push('proto:' + (Object.getPrototypeOf(inst) === custom.prototype));

		// 不影响原函数的后续普通调用。
		f();
		__out.push('after:' + nt);
	`)
	want := "default:true\n" +
		"explicit:true\n" +
		"proto:true\n" +
		"after:undefined\n"
	if got != want {
		t.Errorf("Reflect.construct newTarget 桥\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNewTargetDirectEval 直接 eval 的 new.target 继承: 非箭头函数体内的直接
// eval 合法且取调用者 new.target; 箭头/间接/顶层 eval 仍为 SyntaxError
// (test262 eval-code/direct/new.target-fn.js / new.target-arrow.js)。
func TestNewTargetDirectEval(t *testing.T) {
	got := evalOut(t, `
		let nt = 'unset';
		let getNT = function () { nt = eval('new.target;'); };
		getNT(); __out.push('plain:' + nt);
		new getNT(); __out.push('new:' + (nt === getNT));

		// eval 内嵌套箭头继承 eval 的 new.target。
		let kOut = 'unset';
		let k = function () { kOut = eval('(() => new.target)()'); };
		new k(); __out.push('arrow-in-eval:' + (kOut === k));

		try { (() => eval('new.target;'))(); __out.push('arrow-eval:no-throw'); }
		catch (e) { __out.push('arrow-eval:' + (e instanceof SyntaxError)); }

		try { (0, eval)('new.target;'); __out.push('indirect-eval:no-throw'); }
		catch (e) { __out.push('indirect-eval:' + (e instanceof SyntaxError)); }

		try { eval('new.target;'); __out.push('top-eval:no-throw'); }
		catch (e) { __out.push('top-eval:' + (e instanceof SyntaxError)); }
	`)
	want := "plain:undefined\n" +
		"new:true\n" +
		"arrow-in-eval:true\n" +
		"arrow-eval:true\n" +
		"indirect-eval:true\n" +
		"top-eval:true\n"
	if got != want {
		t.Errorf("直接 eval 继承 new.target\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// ===== 已知边界 (未实现, 供后续补课; 不写成绿色断言以免固化错误行为) =====
//
//  1. 类字段初始化器内 new.target: Node 允许且为 undefined; 本路线把
//     parseClassMember 的字段初始化器视为非函数上下文 (解析期即早错)。
//     (test262 相关为 eval 形态, 另受 eval 实现限制。)
//  2. 类静态初始化块内的 new.target: Gox 尚未实现 class static block。
//
// 已由 rNAtZs 落地 (原边界 1/3/4): 箭头函数词法继承 new.target、Reflect.construct
// 的显式 newTarget、非箭头函数体内直接 eval 的 new.target 继承 —— 见上方三个
// 测试。注: test262 unary-expr.js 仍不通过, 但与 new.target 语义无关 —— 该用例
// 的 async 段经 asyncHelpers 的 asyncTest 调用 hasOwnProperty.call(globalThis,
// "$DONE"), 而 Gox 的 globalThis (GlobalObject) 未实现自有属性查询, 该 helper
// 在全仓库 139 个用例里 0 通过 (属全局对象属性模型路线, 不在本路线)。

