package vm

import "testing"

// ===== 直接/间接 eval 的 this 绑定 (this-semantics, 2026-10-06) =====
//
// 规范 sec-performeval:
//   - direct eval 的 this 绑定与调用者一致 (thisValue 取自调用者帧);
//   - 间接 eval (0, eval) / eval 别名调用: 在全局 this 下执行, this = globalThis;
//   - eval 代码的严格性 = 调用者严格 OR 源码含 "use strict" 指令;
//     严格 eval 代码的 thisValue **原样保留** (undefined 不归一为 globalThis),
//     非严格 eval 代码按 sloppy 归一。
//
// 落点:
//   - 编译器对所有直接 eval 调用 (`eval(...)`, 被调表达式是标识符 eval) 发射
//     OP_EVAL_MARK; 类字段初始化器内则发射 OP_EVAL_MARK_INIT (额外受限);
//   - VM 在 OP_CALL/OP_CALL_SPREAD 分派前 (确认被调恰为全局 %eval%) 把调用者帧
//     的 this 与严格性经 object 桥 (SetDirectEvalThis) 传给 eval 内建;
//   - eval 内建 (stdlib) 把该 this 作为包装函数的接收者, 并据「源码指令 ||
//     调用者严格」决定包装函数是否严格 —— 包装严格则 callClosure 原样保留
//     thisValue, 否则按 sloppy 归一。
//
// 另: eval 完成值必须处理后缀分号与严格指令 —— `eval("this;")` 与 `eval("this")`
// 等价; `eval('"use strict"; 1+1')` 应为 2 (Gox 解析器不支持 `(a; b)` 序列,
// 故严格指令必须置于包装函数体首部, 不能塞进括号)。

// TestDirectEvalThisInheritsSloppyCaller 非严格调用者: direct eval 的 this =
// 调用者帧 this (裸调用 → globalThis)。
func TestDirectEvalThisInheritsSloppyCaller(t *testing.T) {
	_, res := runEvalVM(t, `var f = function(){ return eval("this"); }; f() === globalThis`)
	assertBoolean(t, res, true)

	// 全局作用域直接 eval: this = globalThis。
	_, res = runEvalVM(t, `eval("this") === globalThis`)
	assertBoolean(t, res, true)
}

// TestDirectEvalThisInheritsMethodReceiver 方法内 direct eval 的 this = 接收者。
func TestDirectEvalThisInheritsMethodReceiver(t *testing.T) {
	_, res := runEvalVM(t, `var o = { m: function(){ return eval("this"); } }; o.m() === o`)
	assertBoolean(t, res, true)
}

// TestDirectEvalThisInheritsStrictCallerThis 严格调用者的 direct eval:
// thisValue 原样保留 (此处为 call(42) 的 42)。
func TestDirectEvalThisInheritsStrictCallerThis(t *testing.T) {
	_, res := runEvalVM(t, `var g = function(){ "use strict"; return eval("this"); }; g.call(42) === 42`)
	assertBoolean(t, res, true)
}

// TestDirectEvalStrictCallerThisUndefined 严格调用者裸调用: thisValue = undefined,
// 且 eval 代码因调用者严格而恒严格 —— undefined 不得被归一为 globalThis。
func TestDirectEvalStrictCallerThisUndefined(t *testing.T) {
	_, res := runEvalVM(t, `var g = function(){ "use strict"; return eval("this"); }; g() === undefined`)
	assertBoolean(t, res, true)
}

// TestIndirectEvalThisIsGlobalThis 间接 eval (别名调用) 的 this = globalThis,
// 即便源码含 "use strict" 指令 (与 node 一致)。
func TestIndirectEvalThisIsGlobalThis(t *testing.T) {
	_, res := runEvalVM(t, `var my_eval = eval; my_eval("this") === globalThis`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `var my_eval = eval; my_eval("\"use strict\";\nthis") === globalThis`)
	assertBoolean(t, res, true)

	// 严格调用者里的间接 eval 仍是 globalThis。
	_, res = runEvalVM(t, `var g = function(){ "use strict"; var my_eval = eval; return my_eval("this"); }; g() === globalThis`)
	assertBoolean(t, res, true)
}

// TestEvalCompletionValueTrailingSemicolon 完成值: 末尾分号不改变结果
// (`eval("this;")` 等价 `eval("this")`)。
func TestEvalCompletionValueTrailingSemicolon(t *testing.T) {
	_, res := runEvalVM(t, `eval("1 + 1;")`)
	assertNumber(t, res, 2)

	_, res = runEvalVM(t, `(function(){ return eval("this;"); })() === globalThis`)
	assertBoolean(t, res, true)

	_, res = runEvalVM(t, `var o = { m: function(){ return eval("this;"); } }; o.m() === o`)
	assertBoolean(t, res, true)
}

// TestEvalCompletionValueStrictDirective eval 源码带 "use strict" 指令时的
// 完成值: 指令不能吞掉表达式结果 (Gox 解析器不支持 (a; b) 序列)。
func TestEvalCompletionValueStrictDirective(t *testing.T) {
	_, res := runEvalVM(t, `eval("\"use strict\"; 1 + 1")`)
	assertNumber(t, res, 2)

	_, res = runEvalVM(t, `eval("\"use strict\"; 1 + 1;")`)
	assertNumber(t, res, 2)

	// 严格 eval 的 this: 全局作用域 → globalThis。
	_, res = runEvalVM(t, `eval("\"use strict\"; this") === globalThis`)
	assertBoolean(t, res, true)
}

// TestEvalSloppyStillCreatesGlobal 非严格间接 eval 仍按 sloppy 语义创建全局,
// 不因包装而变严格。
func TestEvalSloppyStillCreatesGlobal(t *testing.T) {
	_, res := runEvalVM(t, `var my_eval = eval; my_eval("__sloppy_global = 7"); __sloppy_global`)
	assertNumber(t, res, 7)
}

// TestEvalStatementPositionBraceIsBlock eval 源码以 `{` 开头时命中
// ExpressionStatement 的 lookahead 限制, 必须按**语句表**解析为块, 不能走
// `return (<expr>)` 表达式包装 —— 否则 `{length: 3000}/1/g;` 会被读成对象
// 字面量除法并落到运行期 (报 `g is not defined`)。见看板 r9HBA8 /
// test262 language/statementList/eval-block-with-statment-regexp-literal-flags.js。
func TestEvalStatementPositionBraceIsBlock(t *testing.T) {
	_, res := runEvalVM(t, `(function(){
		try { eval("{length: 3000}/1/g;"); return "ok"; }
		catch (e) { return e.name; }
	})() === "ok"`)
	assertBoolean(t, res, true)

	// 同一形状 (块后跟空块) 也不得因表达式包装而解析失败/报错。
	_, res = runEvalVM(t, `(function(){
		try { eval("{length: 3000}{}"); return "ok"; }
		catch (e) { return e.name; }
	})() === "ok"`)
	assertBoolean(t, res, true)
}

// TestEvalShadowedIdentifierDoesNotInherit 被局部变量遮蔽的 eval 不是直接
// eval: 既不继承调用者 this, 也不受限 (标记不得泄漏给后续无关调用)。
func TestEvalShadowedIdentifierDoesNotInherit(t *testing.T) {
	// 局部 eval 返回普通函数: this 由其自身调用形态决定, 与调用者无关。
	_, res := runEvalVM(t, `
		var o = { m: function(){ var eval = function(){ return this; }; return eval(); } };
		o.m() === globalThis`)
	assertBoolean(t, res, true)
}
