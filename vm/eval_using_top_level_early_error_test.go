package vm

import "testing"

// ===== eval 顶层 using / await using 早错 (rabcWh, 2026-10-06) =====
//
// 规范 sec-let-const-using-and-await-using-declarations-static-semantics-
// early-errors: UsingDeclaration 出现在 goal 为 Script 的顶层、且不被
// Block / ForStatement / ForInOfStatement / FunctionBody / ... 包含时是
// SyntaxError。Eval 的 ParseText 按 Script goal 进行, 故 eval 源码顶层的
// using / await using 声明必须早错 (test262
// language/statements/using/syntax/using-not-allowed-at-top-level-of-eval.js
// 与 await-using/syntax/await-using-not-allowed-at-top-level-of-eval.js)。
//
// Gox 的 eval 由 stdlib 把源码包进合成函数体 `(function(){ ... })` 再经
// object.CompileSource 桥编译, 编译期看到的"函数体直接语句"即 eval 顶层。
// 落地: vm 为 eval 语境注册专用编译桥 (compileForBridgeEval /
// compileForBridgeAllowingNewTarget), 置 parser.SetEvalTopLevel(true);
// parser.usingDeclAllowed 据此对 blockOrFnDepth<=1 (合成函数体的直接语句)
// 上的 using / await using 报早错。块内/嵌套函数内的 using 仍合法;
// new Function 的体是真正的 FunctionBody, 走普通桥不置该标志 (using 合法)。

// TestEvalTopLevelUsingEarlyError eval 源码顶层的 using / await using 是
// SyntaxError, 且错误在 eval 调用时抛出 (可被 try/catch 捕获), 体内副作用
// 不得发生。
func TestEvalTopLevelUsingEarlyError(t *testing.T) {
	got := evalOut(t, `
		function probe(src) {
			try { eval(src); return "no-throw"; }
			catch (e) { return e.name; }
		}
		__out.push("using:" + probe("using x = null;"));
		__out.push("awaitUsing:" + probe("await using x = null;"));
		__out.push("sideEffect:" + globalThis.__rabc1);
	`)
	want := "using:SyntaxError\nawaitUsing:SyntaxError\nsideEffect:undefined\n"
	if got != want {
		t.Errorf("eval 顶层 using 早错\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalBlockScopedUsingStillLegal eval 源码**块内**的 using 合法
// (规范: Block 是允许的容器), 别误杀。
func TestEvalBlockScopedUsingStillLegal(t *testing.T) {
	got := evalOut(t, `
		let r = "no-throw";
		try { eval("{ using x = null; }"); r = "ok"; } catch (e) { r = e.name; }
		__out.push("block:" + r);

		// 嵌套函数体内的 using 同样合法
		r = "no-throw";
		try { eval("(function(){ using y = null; })()"); r = "ok"; } catch (e) { r = e.name; }
		__out.push("nestedFn:" + r);

		// for-of 头部的 using 合法 (ForInOfStatement 是允许的容器)
		r = "no-throw";
		try { eval("for (using z of [null]) {}"); r = "ok"; } catch (e) { r = e.name; }
		__out.push("forOfHead:" + r);
	`)
	want := "block:ok\nnestedFn:ok\nforOfHead:ok\n"
	if got != want {
		t.Errorf("eval 块内 using 应合法\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNonTopLevelEvalCallUsingStillEarlyErrors eval 调用点不在程序顶层
// (函数体内/箭头内的直接 eval) 时, eval 源码仍是 Script goal —— 顶层
// using 依旧早错; 同时调用者自己的函数体内 using 正常不受影响。
func TestNonTopLevelEvalCallUsingStillEarlyErrors(t *testing.T) {
	got := evalOut(t, `
		// 函数体内的直接 eval (走 allowNewTarget 桥) —— 依旧早错
		let r = "no-throw";
		function f() { try { eval("using x = null;"); } catch (e) { r = e.name; } }
		f();
		__out.push("inFn:" + r);

		// 箭头内的直接 eval —— 依旧早错
		r = "no-throw";
		(() => { try { eval("using x = null;"); } catch (e) { r = e.name; } })();
		__out.push("inArrow:" + r);

		// 间接 eval —— 依旧早错
		r = "no-throw";
		var ind = eval;
		try { ind("using x = null;"); } catch (e) { r = e.name; }
		__out.push("indirect:" + r);

		// 函数体自身的 using 正常 (含资源释放语义)
		let disposed = "no";
		function g() {
			using res = { [Symbol.dispose]() { disposed = "yes"; } };
			return 42;
		}
		__out.push("fnBodyUsing:" + g() + "/" + disposed);
	`)
	want := "inFn:SyntaxError\ninArrow:SyntaxError\nindirect:SyntaxError\nfnBodyUsing:42/yes\n"
	if got != want {
		t.Errorf("非顶层 eval 调用与函数体 using\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestNewFunctionBodyUsingStillLegal new Function 的体是真正的 FunctionBody
// (规范允许容器), 顶层 using 合法 —— eval 专用桥不能波及本路径。
func TestNewFunctionBodyUsingStillLegal(t *testing.T) {
	got := evalOut(t, `
		let r = "no-throw";
		try { r = "" + new Function("using x = null; return x;")(); } catch (e) { r = e.name; }
		__out.push("newFunction:" + r);
	`)
	want := "newFunction:null\n"
	if got != want {
		t.Errorf("new Function 体 using 应合法\nwant:\n%s\ngot:\n%s", want, got)
	}
}
