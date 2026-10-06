package vm

import (
	"testing"
)

// ===== new.target 元属性 (rvi5ZG, 2026-10-06) =====
//
// 期望值全部对齐 Node 22 实测。覆盖: 普通调用 undefined / new 调用为构造器 /
// super() 沿链传递 (含两跳与隐式构造) / apply·call·reflect.apply·tagged template
// ·getter 均为 undefined / new 与 .target 间的空白·换行·注释 / 一元前缀运算。
//
// 已知边界 (见文件末尾注释): 箭头函数词法继承、类字段初始化器、Reflect.construct
// 的显式 newTarget、直接 eval 继承 —— 均未覆盖。

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

// ===== 已知边界 (未实现, 供后续补课; 不写成绿色断言以免固化错误行为) =====
//
//  1. 箭头函数词法继承 new.target: 规范里箭头不建立自己的 new.target,
//     继承外层函数的值。Gox 需要把外层 new.target 在创建箭头闭包时按词法捕获
//     (与 this 的捕获同型), 但 object.Closure 无对应字段 (本路线文件区禁止改
//     object/), 故箭头的 new.target 恒为 undefined。可能受影响的 test262:
//     expressions/arrow-function/lexical-new.target*.js、各
//     returns-async-arrow-returns-newtarget.js。
//  2. 类字段初始化器内 new.target: Node 允许且为 undefined; 本路线把
//     parseClassMember 的字段初始化器视为非函数上下文 (解析期即早错)。
//     (test262 相关为 eval 形态, 另受 eval 实现限制。)
//  3. Reflect.construct(F, [], G) 显式 newTarget: 需 stdlib/reflect 把 newTarget
//     传入 VM 构造路径 (本路线禁止改 stdlib/), 目前 new.target 恒 undefined。
//  4. 直接 eval 内 new.target 继承调用者: eval 源码被编为全局脚本 (顶层),
//     解析期即早错。需在 eval 编译期告知「调用者是非箭头函数」并桥接其
//     new.target (另见看板 roiE5Z: eval 里 super.x 被误报)。
//  5. 类静态初始化块内的 new.target: Gox 尚未实现 class static block。
