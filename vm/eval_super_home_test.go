package vm

import "testing"

// ===== 直接 eval 里 super.x (SuperProperty) 的 home 语境 (roiE5Z) =====
//
// 规范 sec-performeval 18.2.1.1.1 / 18.2.1.1.2:
//   - eval 源码含 SuperProperty (super.x) 只在「调用者函数有 [[HomeObject]]」
//     的直接 eval 里合法 (inMethod); 全局代码 / 无 home 的普通函数 / 箭头
//     函数内的直接 eval 一律 SyntaxError;
//   - SuperCall (super()) 在 eval 源码里恒 SyntaxError (18.2.1.1.2);
//   - 间接 eval 按全局 eval: super.x / super() 都 SyntaxError。
//
// Gox 落地 (本看板单): eval 源码被 stdlib 编成独立单元 (全局脚本), 编译期
// 看不到外层函数的 home object。故:
//   - 编译器在直接 eval 调用点, 当外层编译语境有 home (类方法/字段初始化器/
//     静态语境/对象字面量方法) 时, 随 OP_EVAL_MARK 家族发射 home 上下文
//     (类名 + 实例/静态/this 三态 + 静态父类名);
//   - VM 消费标记时把该上下文经 object 桥传给 eval 内建 (SetDirectEvalSuperHome);
//   - eval 内建经带选项的编译桥 (CompileSourceWithOpts) 把它注入 eval 单元:
//     SuperProperty 合法, 按 home 解析 (实例: <类>.prototype 的原型;
//     静态: 父类构造器 / 基类的 Function.prototype; 对象方法: this 的原型)。
//
// 已知近似 (与既有 super 静态名模型同边界, 见 docs/roiE5Z):
//   - home 靠 LOAD_GLOBAL(类名) 解析 —— 顶层 class / var 赋值的类表达式
//     可解析; 外层函数内的局部类名运行期 ReferenceError (旧行为: SyntaxError);
//   - 对象字面量方法的 home 取 this (接收者) —— o.m() 直接调用正确,
//     o.m.call(x) 会读错宿主 (规范仍读 o);
//   - 嵌套普通函数沿用外层 home (规范应断) —— 与 Gox 既有 super 行为一致。

// TestEvalSuperPropertyInClassMethod 派生/基类方法内直接 eval 的 super.x:
// 合法且按 home 解析 (home = C.prototype, base = Object.getPrototypeOf(home))。
//
// 注意: home 名走 LOAD_GLOBAL 解析, 故用例一律用**顶层** class 声明 ——
// 函数内的局部类名在 eval 单元里不可见 (既有 super 静态名模型同边界,
// 见文件头「已知近似」)。
func TestEvalSuperPropertyInClassMethod(t *testing.T) {
	got := evalOut(t, `
		class A { get g(){ return 42 } }
		class C extends A { m(){ return eval('super.g') } }
		__out.push('g:' + new C().m());

		class A2 { get g(){ return 42 } }
		class C2 extends A2 { m(){ return eval('super.g + 1') } }
		__out.push('g1:' + new C2().m());

		class B1 { m(){ return typeof eval('super.toString') } }
		__out.push('base:' + new B1().m());

		class A3 { p = 5 }
		class C3 extends A3 { m(){ return eval("super.toString != null") } }
		__out.push('toml:' + new C3().m());

		class A4 { get g(){ return 7 } }
		class C4 extends A4 { m(){ return eval('() => super.g')() } }
		__out.push('arrow:' + new C4().m());

		// 字段 (实例自有属性) 不在原型链上: super.x 得 undefined (规范行为)。
		class A5 { x = 1 }
		class C5 extends A5 { m(){ return eval('super.x') } }
		__out.push('field:' + new C5().m());
	`)
	want := "g:42\ng1:43\nbase:function\ntoml:true\narrow:7\nfield:undefined\n"
	if got != want {
		t.Errorf("类方法内直接 eval 的 super.x\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalSuperPropertyInStaticContext static 方法 / 静态块内直接 eval 的
// super.x: 派生类 base = 父类构造器本身; 基类 base = Function.prototype。
func TestEvalSuperPropertyInStaticContext(t *testing.T) {
	got := evalOut(t, `
		class A { static sx = 5 }
		class C extends A { static m(){ return eval('super.sx') } }
		__out.push('sx:' + C.m());

		class A2 { static sm(){ return 'ok' } }
		class C2 extends A2 { static m(){ return eval('super.sm()') } }
		__out.push('sm:' + C2.m());

		// 基类静态方法: super base = Function.prototype。
		class B1 { static m(){ return typeof eval('super.toString') } }
		__out.push('base:' + B1.m());

		// 静态初始化块内的直接 eval。
		class A3 { static sx = 9 }
		class C3 extends A3 { static { globalThis.__sb = eval('super.sx'); } }
		__out.push('block:' + globalThis.__sb);
	`)
	want := "sx:5\nsm:ok\nbase:function\nblock:9\n"
	if got != want {
		t.Errorf("静态语境内直接 eval 的 super.x\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalSuperPropertyInObjectMethod 对象字面量方法内直接 eval 的 super.x:
// home 无名字, 运行期取 this 的原型链 (test262 super-prop-method.js 同款)。
func TestEvalSuperPropertyInObjectMethod(t *testing.T) {
	got := evalOut(t, `
		var superProp = null;
		var o = { test262: null, method() { superProp = eval('super.test262;'); } };
		o.method();
		__out.push('first:' + superProp);
		Object.setPrototypeOf(o, { test262: 262 });
		o.method();
		__out.push('second:' + superProp);
	`)
	want := "first:undefined\nsecond:262\n"
	if got != want {
		t.Errorf("对象字面量方法内直接 eval 的 super.x\nwant:\n%s\ngot:\n%s", want, got)
	}
	// getter 同样有 home。
	assertJS(t, `(() => { var o = { get g(){ return eval('super.n') } }; Object.setPrototypeOf(o, { n: 3 }); return o.g; })()`, "3")
}

// TestEvalSuperPropertyInFieldInitializer 字段初始化器内直接 eval 的
// super.x (test262 derived-cls-direct-eval-contains-superproperty-1.js):
// 合法 (super() 仍被 evalInitRejectsRestricted 拦)。
func TestEvalSuperPropertyInFieldInitializer(t *testing.T) {
	got := evalOut(t, `
		var executed = false;
		var A = class { x = 1 }
		var C = class extends A { x = eval('executed = true; super.x;') };
		new C();
		__out.push('executed:' + executed);
		__out.push('x:' + new C().x);
	`)
	want := "executed:true\nx:undefined\n"
	if got != want {
		t.Errorf("字段初始化器内直接 eval 的 super.x\nwant:\n%s\ngot:\n%s", want, got)
	}
	// 字段初始化器内箭头体的直接 eval (箭头继承 home)。经全局副作用取出箭头
	// —— 多语句 eval 的完成值在 Gox 里恒 undefined (既有缺口, 与 super 无关),
	// 故不能依赖 eval 的返回值。
	got = evalOut(t, `
		var executed = false;
		var A = class {}
		var C = class extends A { x = eval('globalThis.__f = () => super.x; executed = true;') }
		new C();
		globalThis.__f();
		__out.push('executed:' + executed);
	`)
	if got != "executed:true\n" {
		t.Errorf("字段初始化器内箭头体的直接 eval\nwant: executed:true\ngot: %q", got)
	}
}

// TestEvalSuperCallStaysSyntaxError super() (SuperCall) 在 eval 源码里恒
// SyntaxError (18.2.1.1.2), 无论调用点有没有 home。
// 注意区分: super() 是**调用 super 本身** (SuperCall, 恒拦); super.method()
// 是 SuperProperty 上的方法调用, home 语境下合法 (见下个用例)。
func TestEvalSuperCallStaysSyntaxError(t *testing.T) {
	assertJSThrows(t, `(() => { var o = { method() { eval('super(1);'); } }; o.method(); })()`, "SyntaxError")
	assertJSThrows(t, `(() => { class A {} class C extends A { m(){ return eval('super()'); } } new C().m(); })()`, "SyntaxError")
	// 静态块 / 字段初始化器语境同样拦 super()。
	assertJSThrows(t, `(() => { class A {} class C extends A { static m(){ return eval('super()'); } } C.m(); })()`, "SyntaxError")
}

// TestEvalSuperMethodCallInEval home 语境下 eval 源码的 super.method()
// (SuperProperty 上的调用) 合法, this 绑定当前 this。
func TestEvalSuperMethodCallInEval(t *testing.T) {
	got := evalOut(t, `
		class A { sm(a){ return 'sm:' + a + ':' + (this === c); } }
		class C extends A { m(){ return eval('super.sm(7)'); } }
		var c = new C();
		__out.push(c.m());
	`)
	if got != "sm:7:true\n" {
		t.Errorf("eval 内 super.method()\nwant: sm:7:true\ngot: %q", got)
	}
}

// TestEvalSuperPropertyWithoutHomeStaysSyntaxError 无 home 语境的直接 eval:
// super.x 仍 SyntaxError (全局代码 / 普通函数 / 箭头函数 / 间接 eval)。
func TestEvalSuperPropertyWithoutHomeStaysSyntaxError(t *testing.T) {
	assertJSThrows(t, `eval('super.property;')`, "SyntaxError")
	assertJSThrows(t, `(function f(){ return eval('super.x;'); })()`, "SyntaxError")
	assertJSThrows(t, `(() => eval('super.property;'))()`, "SyntaxError")
	assertJSThrows(t, `(0, eval)('super.property;')`, "SyntaxError")
	// 类方法里的间接 eval: 全局 eval, 无 home。
	assertJSThrows(t, `(() => { class A {} class C extends A { m(){ return (0, eval)('super.x'); } } new C().m(); })()`, "SyntaxError")
	// 被局部变量遮蔽的 eval 不是直接 eval: 连 this/home 都不继承
	// (这里只钉「不报 super 早错、走正常引用」这一契约)。
	assertJS(t, `(function(){ var eval2 = eval; return eval2('1 + 1'); })()`, "2")
}

// TestEvalSuperPropertyMultiStatement 多语句包装轮 (非单表达式源码) 同样
// 带 home 语境: super.x 在语句表里合法, 且 super() 的 SyntaxError 不被
// 后退的包装掩盖。
func TestEvalSuperPropertyMultiStatement(t *testing.T) {
	got := evalOut(t, `
		class A { get g(){ return 'gg' } }
		class C extends A { m(){ var v = eval('var t = super.g; globalThis.__ms = t + "!"'); return v; } }
		new C().m();
		__out.push('ms:' + globalThis.__ms);
	`)
	if got != "ms:gg!\n" {
		t.Errorf("多语句 eval 的 super.x\nwant: ms:gg!\ngot: %q", got)
	}
	got = evalOut(t, `
		class A {}
		class C extends A { m(){ return eval('var t = 1; super();'); } }
		var threw = 'no';
		try { new C().m(); } catch (e) { threw = e.name; }
		__out.push('supercall:' + threw);
	`)
	if got != "supercall:SyntaxError\n" {
		t.Errorf("多语句 eval 的 super() 早错\nwant: supercall:SyntaxError\ngot: %q", got)
	}
}

// TestEvalSuperPropertyNewTargetCombo home 语境 + new.target 允许的组合
// (非箭头函数体内的直接 eval): 两个编译选项同时经 opts 桥进入单元。
// 普通方法调用里 new.target 运行期为 undefined (规范: 非构造调用), 故
// typeof 得 "undefined"; 若两者冲突 (new.target 被拦) 会是 SyntaxError。
func TestEvalSuperPropertyNewTargetCombo(t *testing.T) {
	got := evalOut(t, `
		class A { get g(){ return 'nt' } }
		class C extends A { m(){ return eval('super.g + ":" + (typeof new.target)'); } }
		__out.push('direct:' + new C().m());
	`)
	want := "direct:nt:undefined\n"
	if got != want {
		t.Errorf("home + new.target 组合\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalSuperPropertyComputed 计算式 SuperProperty `super[expr]`
// (sec-super-property: super . IdentifierName | super [ Expression ]) ——
// 与 `super.prop` 同族, 规范在方法/eval 内的合法性判据完全一致。
//
// roiE5Z 遗留边界: 前缀解析此前只认 super( / super. , 直接把 super[expr]
// 判成 SyntaxError (test262 *-contains-superproperty-2.js 一族恒失败)。
// 实为纯解析缺口 —— 中缀下标解析 (parseIndexExpression) 与编译器 super
// 成员访问两分支 (compileMemberExpression) 早已支持 Computed, 故放开
// `super[` 前缀即可, 无新增代码生成路径。
func TestEvalSuperPropertyComputed(t *testing.T) {
	got := evalOut(t, `
		// 原生 (带 extends 的派生类方法), 静态键与动态键
		class A { get x(){ return 42 } }
		class C extends A { m(){ return super['x'] } }
		__out.push('native:' + new C().m());
		class D extends A { m(k){ return super[k] } }
		__out.push('dynamic:' + new D().m('x'));

		// 直接 eval 单元 (roiE5Z home 桥) 的 super[expr]
		class E extends A { m(){ return eval("super['x']") } }
		__out.push('eval:' + new E().m());

		// 字段初始化器内的直接 eval (computed)
		var executed = false;
		var B = class {};
		var F = class extends B { y = eval("executed = true; super['x'];") };
		new F();
		__out.push('field:' + executed);

		// 对象字面量方法内直接 eval 的 super[expr] (home = this)
		var o = { method(){ return eval("super['test262']") } };
		Object.setPrototypeOf(o, { test262: 262 });
		__out.push('obj-eval:' + o.method());
	`)
	want := "native:42\ndynamic:42\neval:42\nfield:true\nobj-eval:262\n"
	if got != want {
		t.Errorf("计算式 super[expr]\nwant:\n%s\ngot:\n%s", want, got)
	}

	// 无 home 语境: super[expr] 与 super.prop 一样仍是早错 (放行的是解析,
	// 不是合法性判定)。
	assertJSThrows(t, `eval("super['x'];")`, "SyntaxError")
	assertJSThrows(t, `(function f(){ return eval("super['x'];"); })()`, "SyntaxError")
	// 裸 super (后不接 ( / . / [) 仍是解析错。
	if _, err := EvalVM("var z = super;"); err == nil {
		t.Errorf("裸 super 应报解析错, 实际未报")
	}
}
