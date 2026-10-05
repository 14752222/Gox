package vm

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 动态函数构造 CreateDynamicFunction (r4fzy8) =====
//
// GeneratorFunction / AsyncFunction / AsyncGeneratorFunction 通过
// CreateDynamicFunction 从源码字符串构造函数对象。本组用例锁定:
//   - 三种种类 (含 new 形式) 造出的函数种类与静态声明一致；
//   - 造出的 function* 的 [[Prototype]] 与 .prototype 实例原型链正确；
//   - 语法错误路径 (非法函数体 / 非法形参) 抛 SyntaxError；
//   - generator / async 函数不是构造器 (new 抛 TypeError)。
//
// 期望值均以 Node 22 实测为准 (语义权威)。

// evalStrOrFatal 求值多语句脚本并要求完成值为字符串。
func evalStrOrFatal(t *testing.T, src string) string {
	t.Helper()
	got := evalWithStdlib(t, src)
	s, ok := got.(*object.String)
	if !ok {
		t.Fatalf("expected String from %q, got %T (%v)", src, got, got)
	}
	return s.Value
}

// GeneratorFunction 造出的函数可执行且种类正确。
func TestDynamicGeneratorFunction(t *testing.T) {
	// 调用形式与 new 形式都能造出可执行的生成器。
	assertJS(t,
		`(function(){ var GF = Object.getPrototypeOf(function*(){}).constructor; var g = GF('x','y','yield x + y;'); return g(2,3).next().value; })()`,
		"5")
	assertJS(t,
		`(function(){ var GF = Object.getPrototypeOf(function*(){}).constructor; var g = new GF(); return g().next().done; })()`,
		"true")
	// 种类 / 原型链。
	assertJS(t,
		`(function(){ var GF = Object.getPrototypeOf(function*(){}).constructor; var g = GF('yield 1'); return Object.getPrototypeOf(g) === GF.prototype; })()`,
		"true")
	assertJS(t,
		`(function(){ var GF = Object.getPrototypeOf(function*(){}).constructor; var g = GF('yield 1'); return Object.getPrototypeOf(g.prototype) === GF.prototype.prototype; })()`,
		"true")
	// name / length。
	assertJS(t, `Object.getPrototypeOf(function*(){}).constructor().name`, "anonymous")
	assertJS(t, `Object.getPrototypeOf(function*(){}).constructor('x','y','').length`, "2")
}

// AsyncFunction 造出的函数是 async 函数: 返回 Promise 且可 await。
func TestDynamicAsyncFunction(t *testing.T) {
	got := runAsyncEval(t, `
		const AF = Object.getPrototypeOf(async function(){}).constructor;
		const f = AF('x','return x + 1;');
		const f2 = new AF('return 7;');
		(async function(){
			__out.push("dyn-af:" + (await f(41)));
			__out.push("dyn-af-new:" + (await f2()));
			__out.push("dyn-af-isPromise:" + (f(1) instanceof Promise));
			__out.push("dyn-af-ctor:" + f.constructor.name);
		})();
	`)
	for _, want := range []string{"dyn-af:42", "dyn-af-new:7", "dyn-af-isPromise:true", "dyn-af-ctor:AsyncFunction"} {
		if !strings.Contains(got, want) {
			t.Errorf("期望包含 %q, got:\n%s", want, got)
		}
	}
	// async 函数没有 .prototype 属性。
	assertJS(t, `Object.getPrototypeOf(async function(){}).constructor('await 1').prototype`, "undefined")
}

// AsyncGeneratorFunction 造出的函数是 async generator: next() 返回 Promise。
func TestDynamicAsyncGeneratorFunction(t *testing.T) {
	got := runAsyncEval(t, `
		const AGF = Object.getPrototypeOf(async function*(){}).constructor;
		const ag = AGF('yield 1; yield 2;');
		const agNew = new AGF('yield 9;');
		(async function(){
			const it = ag();
			const r1 = await it.next();
			const r2 = await it.next();
			__out.push("dyn-ag:" + r1.value + "," + r1.done + "," + r2.value + "," + r2.done);
			__out.push("dyn-ag-new:" + (await agNew().next()).value);
			__out.push("dyn-ag-ctor:" + ag.constructor.name);
			__out.push("dyn-ag-self:" + (it[Symbol.asyncIterator]() === it));
		})();
	`)
	for _, want := range []string{"dyn-ag:1,false,2,false", "dyn-ag-new:9", "dyn-ag-ctor:AsyncGeneratorFunction", "dyn-ag-self:true"} {
		if !strings.Contains(got, want) {
			t.Errorf("期望包含 %q, got:\n%s", want, got)
		}
	}
	// async generator 有 .prototype，其 [[Prototype]] 指向 %AsyncGeneratorPrototype%。
	assertJS(t,
		`(function(){ var AGF = Object.getPrototypeOf(async function*(){}).constructor; var ag = AGF('yield 1'); return Object.getPrototypeOf(ag.prototype) === AGF.prototype.prototype; })()`,
		"true")
}

// 语法错误: 种类与函数体不匹配 / 非法形参 → SyntaxError。
func TestDynamicFunctionSyntaxErrors(t *testing.T) {
	// generator 体内出现 await (非 async): node 实测 SyntaxError。
	assertJSThrows(t, `Object.getPrototypeOf(function*(){}).constructor("await 1")`, "SyntaxError")
	// 非法形参: 含结构字符。
	assertJSThrows(t, `Object.getPrototypeOf(function*(){}).constructor("a b", "yield 1")`, "SyntaxError")
	assertJSThrows(t, `Object.getPrototypeOf(async function(){}).constructor("x)")`, "SyntaxError")
	// 函数体本身语法错误。
	assertJSThrows(t, `Object.getPrototypeOf(function*(){}).constructor("yield 1 +")`, "SyntaxError")
}

// generator / async 函数不是构造器: new 抛 TypeError (规范 [[Construct]] 缺失)。
func TestDynamicInstanceNotConstructor(t *testing.T) {
	got := evalStrOrFatal(t, `
		const GF = Object.getPrototypeOf(function*(){}).constructor;
		const AF = Object.getPrototypeOf(async function(){}).constructor;
		const AGF = Object.getPrototypeOf(async function*(){}).constructor;
		const r = [];
		function probe(f){ try { new f(); r.push("no-throw"); } catch(e){ r.push(e.name); } }
		probe(GF());
		probe(AF("await 1"));
		probe(AGF("yield 1"));
		r.join(",");
	`)
	if got != "TypeError,TypeError,TypeError" {
		t.Errorf("new generator/async 实例应统一抛 TypeError, got %q", got)
	}
}

// 动态普通 Function 仍与静态一致 (回归保护)。
func TestDynamicFunctionStillWorks(t *testing.T) {
	assertJS(t, `Function("a","b","return a + b;")(1,2)`, "3")
	assertJS(t, `(new Function("return 5;"))()`, "5")
	assertJS(t, `Function("a","b","return a+b;").name`, "anonymous")
	assertJS(t, `Function("a","b","return a+b;").length`, "2")
	assertJS(t, `Object.getPrototypeOf(Function("return 1;")) === Function.prototype`, "true")
}
