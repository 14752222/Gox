package vm

import "testing"

// ===== 类字段初始化器内直接 eval 的 PerformEval 早错 (rdyGNY, 2026-10-05) =====
//
// 规范 sec-performeval-rules-in-initializer: 直接 eval 发生在类字段初始化器
// 内时, 其 StatementList 的补充早错规则生效 —— 含 arguments 引用是
// SyntaxError (super(...) 调用亦然)。间接 eval 不受这些规则约束。
//
// Gox 的 eval 统一实现为全局 eval (间接语义), 无法天然区分直接/间接。落地:
//  - 编译器在「类字段初始化器表达式」上下文里遇到直接 eval 调用
//    (`eval(...)`, 即被调表达式是标识符 eval; 非 (0, eval)/eval.call)
//    时, 在该调用前发射 OP_EVAL_MARK;
//  - vm 执行 OP_EVAL_MARK 时置位 stdlib 一次性标志, eval 内建消费它后
//    进入受限模式, 复用解析器已有的「字段初始化器 arguments 早错」判定
//    (把源码嵌进合成 class 的字段初始化器再编译一遍)。
//
// 用例覆盖: 早错类型/时机、间接 eval 不受限、方法体/嵌套普通函数不受限、
// 无 arguments 时不受影响。

// TestEvalInitializerArgumentsEarlyError 早错必须在 eval 体执行前抛出,
// 因此体内的副作用不得发生。
func TestEvalInitializerArgumentsEarlyError(t *testing.T) {
	got := evalOut(t, `
		// 1) 实例字段初始化器 + 直接 eval + arguments → SyntaxError
		let C = class { x = eval('globalThis.__se1 = true; arguments;') };
		let threw1 = 'no';
		try { new C(); } catch (e) { threw1 = e.name; }
		__out.push('threw1:' + threw1);
		__out.push('se1:' + globalThis.__se1);

		// 2) 初始化器内箭头体内的直接 eval + arguments → 仍受限
		let D = class { x = () => { let f = eval('globalThis.__se2 = true; arguments;'); f(); } };
		let threw2 = 'no';
		try { new D().x(); } catch (e) { threw2 = e.name; }
		__out.push('threw2:' + threw2);
		__out.push('se2:' + globalThis.__se2);

		// 3) 初始化器内 eval 源码里的箭头引用 arguments → 即便箭头不求值也早错
		let E = class { x = eval('globalThis.__se3 = true; () => arguments;'); };
		let threw3 = 'no';
		try { new E(); } catch (e) { threw3 = e.name; }
		__out.push('threw3:' + threw3);
		__out.push('se3:' + globalThis.__se3);
	`)
	want := "threw1:SyntaxError\nse1:undefined\n" +
		"threw2:SyntaxError\nse2:undefined\n" +
		"threw3:SyntaxError\nse3:undefined\n"
	if got != want {
		t.Errorf("初始化器内直接 eval 的 arguments 早错\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalInitializerRestrictionScope 早错只作用于「初始化器内直接 eval」:
// 间接 eval / 方法体 / 初始化器内嵌套普通函数都不受限。
func TestEvalInitializerRestrictionScope(t *testing.T) {
	got := evalOut(t, `
		// 间接 eval: 不受初始化器规则约束 —— 虽含 arguments 也不报早错
		// (值本身是 Gox 全局 eval 包装器的 arguments, 与早错无关)。
		let indErr = 'no';
		try { class A { x = (0, eval)('arguments;') } new A(); } catch (e) { indErr = e.name; }
		__out.push('ind:' + indErr);

		// 方法体内的直接 eval: 有自己的 arguments, 不受限
		class M { m() { let f = eval('globalThis.__mth = typeof arguments; arguments;'); return f; } }
		new M().m();
		__out.push('mth:' + globalThis.__mth);

		// 初始化器内嵌套普通函数: 有独立 arguments 作用域, 不受限
		class N { x = (function () { return eval('globalThis.__fn = typeof arguments; arguments;'); })() }
		new N();
		__out.push('fn:' + globalThis.__fn);

		// 初始化器内直接 eval 但不含 arguments: 正常执行
		class P { x = eval('1 + 1') }
		__out.push('plain:' + new P().x);
	`)
	want := "ind:no\n" +
		"mth:object\n" +
		"fn:object\n" +
		"plain:2\n"
	if got != want {
		t.Errorf("初始化器内直接 eval 受限范围\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestEvalInitializerEarlyErrorType 早错的错误类型是 SyntaxError。
func TestEvalInitializerEarlyErrorType(t *testing.T) {
	assertJSThrows(t, `(() => { class C { x = eval('arguments;') } new C(); })()`, "SyntaxError")
	assertJSThrows(t, `(() => { class C { x = () => eval('arguments;') } new C().x(); })()`, "SyntaxError")
	// 间接 eval 与无 arguments 的直接 eval 都不应抛。
	assertJS(t, `(() => { class C { x = (0, eval)('1 + 1') } return new C().x; })()`, "2")
	assertJS(t, `(() => { class C { x = eval('2 + 3') } return new C().x; })()`, "5")
}
