package vm

import (
	"strings"
	"testing"
)

// ===== 类私有字段/方法 #name（rNZO0b, 2026-10-03）=====
//
// 实现方式: 私有成员落成普通属性, 但键是 "\x00<类名>\x01<序号>:<裸名>" 的
// 混编码 (compiler.privateKey)。\x00 前缀让外部常规访问 (obj.k / obj["#x"] /
// Object.keys / JSON.stringify / 展开运算) 全部摸不到; 每类唯一前缀保证
// 子类与同名类互不串槽。this.#x 编译为运行时动态键 (GET_INDEX/SET_INDEX),
// 类外的 #x 是编译期错误。
//
// ⚠️ 已知既有缺陷（**非**本特性引入, 已建单 rCzckg）: 子类**未显式声明
// constructor** 时, 隐式 constructor 不调用父类构造 —— 父类实例字段在子实例上
// 是 undefined。根因在 compiler.compileClassConstructor: 形参 superName 零引用,
// `ctor == nil` 分支不发射 super(...)。
// 注意边界: 子类**显式**写 constructor(){ super() } 时父类构造正常执行,
// 所以不是「extends 一律坏」。
// 涉及隐式构造的用例只断言「子类摸不到父类私有」这一隔离语义, 不断言父字段的值。

// evalP 是 evalOut 的别名（本文件用短名）。
func evalP(t *testing.T, src string) string {
	t.Helper()
	return evalOut(t, src)
}

// TestPrivateFieldBasics 读/写/this 绑定。
func TestPrivateFieldBasics(t *testing.T) {
	got := evalP(t, `
		class A { #x = 1; get(){ return this.#x } set(v){ this.#x = v } }
		const a = new A();
		__out.push("field:" + a.get());
		a.set(5);
		__out.push("write:" + a.get());
		class I { #n = 3; double(){ return this.#n + this.#n } }
		__out.push("this:" + new I().double());
	`)
	want := "field:1\nwrite:5\nthis:6\n"
	if got != want {
		t.Errorf("私有字段基础语义\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateExternalIsolation 外部不可见: 方括号/枚举/JSON/展开。
func TestPrivateExternalIsolation(t *testing.T) {
	got := evalP(t, `
		class A { #x = 1; pub = 2; get(){ return this.#x } }
		const a = new A();
		__out.push("bracket:" + (a["#x"] === undefined));
		__out.push("keys:" + Object.keys(a).join(","));
		__out.push("json:" + JSON.stringify(a));
		__out.push("spread:" + JSON.stringify({...a}));
	`)
	want := "bracket:true\nkeys:pub\njson:{\"pub\":2}\nspread:{\"pub\":2}\n"
	if got != want {
		t.Errorf("私有字段对外隔离\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateMethodsAndStatics 私有方法/静态私有字段。
func TestPrivateMethodsAndStatics(t *testing.T) {
	got := evalP(t, `
		class B { #m(v){ return v + 1 } static #sv = 9;
			call(){ return this.#m(1) } static sget(){ return B.#sv } }
		__out.push("method:" + new B().call());
		__out.push("static:" + B.sget());
	`)
	want := "method:2\nstatic:9\n"
	if got != want {
		t.Errorf("私有方法/静态\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateInOperator #z in obj 按接收者实例判有。
func TestPrivateInOperator(t *testing.T) {
	got := evalP(t, `
		class C { #z = 1; static has(o){ return #z in o } }
		__out.push("in-self:" + C.has(new C()));
		__out.push("in-plain:" + C.has({}));
	`)
	want := "in-self:true\nin-plain:false\n"
	if got != want {
		t.Errorf("#z in obj\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateInheritanceIsolation 子类摸不到父类的同名私有
// (只断言隔离; 父字段在子实例上的值受既有 extends 缺陷影响, 不断言)。
func TestPrivateInheritanceIsolation(t *testing.T) {
	got := evalP(t, `
		class D { #p = 1; }
		class E extends D { probe(){ return #p in this } }
		__out.push("probe:" + new E().probe());
		class F { #same = "F"; get(){ return this.#same } }
		class G { #same = "G"; get(){ return this.#same } }
		__out.push("same-name:" + (new F().get() === "F" && new G().get() === "G"));
	`)
	want := "probe:false\nsame-name:true\n"
	if got != want {
		t.Errorf("继承/同名隔离\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateAccessors get #v / set #v。
func TestPrivateAccessors(t *testing.T) {
	got := evalP(t, `
		class H { #v = 1;
			get #val(){ return this.#v * 10 } set #val(v){ this.#v = v }
			rd(){ return this.#val } wr(v){ this.#val = v } }
		const h = new H(); h.wr(7);
		__out.push("accessor:" + h.rd());
	`)
	want := "accessor:70\n"
	if got != want {
		t.Errorf("私有访问器\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateSameNameAsPublic #x 与公有 x 是同名但独立的两份槽
// (验收项: 同名不冲突)。写一边不能影响另一边。
func TestPrivateSameNameAsPublic(t *testing.T) {
	got := evalP(t, `
		class S { #x = "priv"; x = "pub";
			readPriv(){ return this.#x } readPub(){ return this.x }
			writePriv(v){ this.#x = v } writePub(v){ this.x = v } }
		const s = new S();
		__out.push("init:" + s.readPriv() + "/" + s.readPub());
		s.writePriv("P1"); s.writePub("P2");
		__out.push("after:" + s.readPriv() + "/" + s.readPub());
		__out.push("keys:" + Object.keys(s).join(","));
		__out.push("bracket:" + String(s["#x"]));
	`)
	want := "init:priv/pub\nafter:P1/P2\nkeys:x\nbracket:undefined\n"
	if got != want {
		t.Errorf("私有与公有同名不冲突\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestPrivateOutsideClassIsCompileError 类外 #x 是编译期错误。
func TestPrivateOutsideClassIsCompileError(t *testing.T) {
	_, err := EvalVM("function f(o){ return o.#x }")
	if err == nil {
		t.Fatal("类外私有访问应报编译错")
	}
	if !strings.Contains(err.Error(), "not allowed outside class") {
		t.Errorf("错误文案应指明类外非法, got: %v", err)
	}
}

// TestPrivateDeleteAndSuperAreEarlyErrors delete / super 位置的私有名是规范早错,
// 且**绝不能让编译器 panic** —— 私有访问的 MemberExpression.Property 是 nil,
// 而 compileDelete 与 super 方法调用分支都硬断言 Property.(*ast.Identifier)。
// 曾实测 `delete this.#x` 把整个进程 abort:
//
//	panic: interface conversion: ast.Expression is nil, not *ast.Identifier
//
// (2026-10-04 由 test262 的 elements/syntax/early-errors/delete/* 158 例 "crashed" 抓出)
func TestPrivateDeleteAndSuperAreEarlyErrors(t *testing.T) {
	cases := []string{
		"class C { #x = 1; m(){ return delete this.#x } }",
		"class C { static #x = 1; static m(){ return delete C.#x } }",
		"class C { #x = 1; m(){ return (delete this.#x, 5) } }",
		"class D { #m(){ return 1 } } class E extends D { n(){ return super.#m() } }",
	}
	for _, src := range cases {
		_, err := EvalVM(src)
		if err == nil {
			t.Errorf("应报错但通过了: %q", src)
			continue
		}
		msg := err.Error()
		if !strings.Contains(msg, "SyntaxError") {
			t.Errorf("错误应含 SyntaxError（test262 negative 判定要求）: %q -> %v", src, err)
		}
	}
	// 反向守卫: 同位置的合法私有访问不能被误伤。
	got := evalP(t, `
		class C { #x = 1; m(){ return this.#x } }
		__out.push("ok:" + new C().m());
	`)
	if got != "ok:1\n" {
		t.Errorf("合法私有访问被误伤, got %q", got)
	}
}

// TestPrivateConstructorNameRejected #constructor 私有名非法。
func TestPrivateConstructorNameRejected(t *testing.T) {
	_, err := EvalVM("class X { #constructor() {} }")
	if err == nil {
		t.Fatal("#constructor 应被拒绝")
	}
}
