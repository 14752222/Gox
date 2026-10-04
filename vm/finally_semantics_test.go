package vm

import (
	"testing"
)

// ===== try/finally 语义 (riUpgO, 2026-10-03 修) =====
//
// 旧实现的 compileTryStatement 给「无 catch 但有 finally」的形状伪造了一个
// 只 POP 错误值的假 catch 处理器, 且正常路径的 skipCatch 跳过整个 finally 块。
// 后果是 finally 的三条路径全坏:
//   1. 无 catch + throw  ⇒ finally 跑了但异常被丢弃 (本单标题的症状);
//   2. 正常完成          ⇒ finally 从不执行;
//   3. catch 里再 throw  ⇒ 直接穿透, finally 不跑。
// 修法: 纯 finally 形状 PUSH_TRY 0 (不造假 catch), 三条路径汇入共享 finally 块,
// 由 handleThrowInner 的 finallyPC 分支 (标记条目 inFinally + 记下挂起值 →
// 跳进 finally → END_FINALLY 重抛) 驱动异常传播。挂起值挂在 tryStack 条目上
// 而不是 VM 全局 —— 见 tryEntry.inFinally 的注释与 TestFinallyPendingThrowNoLeak。

func runFinallyEval(t *testing.T, src string) string {
	t.Helper()
	vm, err := EvalVM("let __out=[];\n" + src + "\n__out.forEach(function(s){console.log(s)});")
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	_ = vm
	return ""
}

// outLines 读取脚本写进 __out 的行 (与 async_throw_test.go 共用的约定不同,
// 这里直接返回 __out 的内容由调用方断言, 避免依赖 console 实现)。
func evalOut(t *testing.T, src string) string {
	t.Helper()
	vm, err := EvalVM("let __out=[];\n" + src)
	if err != nil {
		t.Fatalf("Eval error: %v", err)
	}
	return outLines(t, vm)
}

// TestFinallyThrowPropagates 是本单核心: 无 catch 的 try/finally 里 throw,
// 异常必须传播给外层 catch (旧实现: 异常被假 catch 的 POP 丢弃)。
func TestFinallyThrowPropagates(t *testing.T) {
	got := evalOut(t, `
		function s(){ try { throw new Error("S") } finally { __out.push("fin") } }
		let caught = "NOT-CAUGHT";
		try { s() } catch(e) { caught = "caught:" + e.message }
		__out.push(caught);
	`)
	want := "fin\ncaught:S\n"
	if got != want {
		t.Errorf("无catch的try/finally: 异常应传播且finally先跑\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyRunsOnNormalPath 正常完成时 finally 必须执行
// (旧实现: skipCatch 跳过整个 finally 块)。
func TestFinallyRunsOnNormalPath(t *testing.T) {
	got := evalOut(t, `
		function a(){ try { __out.push("body") } finally { __out.push("fin") } }
		a();
	`)
	want := "body\nfin\n"
	if got != want {
		t.Errorf("正常路径 finally 应执行\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyWithCatchAllPaths catch+finally 的三条路径。
func TestFinallyWithCatchAllPaths(t *testing.T) {
	got := evalOut(t, `
		function b(){ try { __out.push("b-body") } catch(e){ __out.push("b-catch") } finally { __out.push("b-fin") } }
		b();
		function c(){ try { throw new Error("c") } catch(e){ __out.push("c-catch") } finally { __out.push("c-fin") } }
		c();
	`)
	want := "b-body\nb-fin\nc-catch\nc-fin\n"
	if got != want {
		t.Errorf("catch+finally 三路径\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyRethrowFromCatch catch 里再 throw: finally 必须兜住后把新异常传出。
func TestFinallyRethrowFromCatch(t *testing.T) {
	got := evalOut(t, `
		function e(){ try { throw new Error("e1") } catch(err){ throw new Error("e2") } finally { __out.push("e-fin") } }
		try { e() } catch(x){ __out.push("outer:" + x.message) }
	`)
	want := "e-fin\nouter:e2\n"
	if got != want {
		t.Errorf("catch里再throw: finally应兜住后传播\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyNested 嵌套 try/finally: 内外 finally 都要按序执行再传播。
func TestFinallyNested(t *testing.T) {
	got := evalOut(t, `
		function i(){ try { try { throw new Error("inner") } finally { __out.push("i-inner") } } finally { __out.push("i-outer") } }
		try { i() } catch(x){ __out.push("i-caught:" + x.message) }
	`)
	want := "i-inner\ni-outer\ni-caught:inner\n"
	if got != want {
		t.Errorf("嵌套try/finally\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyReturnOverridesError ES6: finally 里 return 会吞掉异常。
// (这条同时覆盖「finally 里的 return」与「异常被覆盖」两个语义。)
func TestFinallyReturnOverridesError(t *testing.T) {
	got := evalOut(t, `
		function g(){ try { throw new Error("g") } finally { return "g-ret" } }
		__out.push(g());
	`)
	want := "g-ret\n"
	if got != want {
		t.Errorf("finally里return应覆盖异常\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyThrowInsideFinallyCatchPath finally 自己 throw (从 catch 路径进入):
// 新异常替代原状态传播。
func TestFinallyThrowInsideFinallyCatchPath(t *testing.T) {
	got := evalOut(t, `
		function h(){ try { throw new Error("h1") } catch(e){} finally { throw new Error("h-fin") } }
		try { h() } catch(x){ __out.push("h-caught:" + x.message) }
	`)
	want := "h-caught:h-fin\n"
	if got != want {
		t.Errorf("finally里throw应传播(catch路径)\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyControlTransferRunsFinally 是 rMkA8D 的回归: return / break /
// continue 穿过 **try 体** 时必须先把 finally 跑完再转移控制。
//
// 旧实现只在「try 正常完成」与「抛异常」两条路径跑 finally, 控制转移路径直接
// 跳走 ⇒ finally 整段被跳过, 且被跳过的 OP_POP_TRY 让 vm.tryStack 留下死条目。
// 修法 (compiler.emitTryUnwind): 为这三类转移生成「逐层 POP_TRY + 内联 finally
// 体」的收尾链, return 的值经隐藏槽暂存后再 OP_RETURN。
//
// 期望值全部与 Node 实测一致 (见 docs / 看板 rMkA8D 的取证记录)。
func TestFinallyControlTransferRunsFinally(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"return 穿 try 体",
			`function a(){ try { return "a-ret" } finally { __out.push("a-fin") } }
			 __out.push("A:" + a());`,
			"a-fin\nA:a-ret\n",
		},
		{
			"裸 return 穿 try 体",
			`function b(){ try { return } finally { __out.push("b-fin") } }
			 __out.push("B:" + b());`,
			"b-fin\nB:undefined\n",
		},
		{
			"嵌套 try/finally + return: 内层 finally 先跑",
			`function e(){ try { try { return "e-ret" } finally { __out.push("e-inner") } } finally { __out.push("e-outer") } }
			 __out.push("E:" + e());`,
			"e-inner\ne-outer\nE:e-ret\n",
		},
		{
			"finally 里的 return 覆盖 try 里的 return",
			`function f(){ try { return "f-try" } finally { return "f-fin" } }
			 __out.push("F:" + f());`,
			"F:f-fin\n",
		},
		{
			"finally 里的 throw 覆盖 try 里的 return",
			`function k(){ try { return "k-ret" } finally { throw new Error("k-thrown") } }
			 try { __out.push("K:" + k()) } catch(x){ __out.push("K-caught:" + x.message) }`,
			"K-caught:k-thrown\n",
		},
		{
			"catch 体里 return 同样要跑 finally",
			`function i(){ try { throw new Error("x") } catch(err){ return "i-ret" } finally { __out.push("i-fin") } }
			 __out.push("I:" + i());`,
			"i-fin\nI:i-ret\n",
		},
		{
			"finally 改外层变量后再 return",
			`let n = 0;
			 function nn(){ try { return "n-ret" } finally { n = 42 } }
			 __out.push("N:" + nn() + "," + n);`,
			"N:n-ret,42\n",
		},
		{
			"break 穿 try 体",
			`let c = ""; for (let k = 0; k < 3; k++) { try { if (k === 1) break; c += k } finally { c += "f" } }
			 __out.push("C:" + c);`,
			"C:0ff\n",
		},
		{
			"continue 穿 try 体",
			`let d = ""; for (let k = 0; k < 3; k++) { try { if (k === 1) continue; d += k } finally { d += "f" } }
			 __out.push("D:" + d);`,
			"D:0ff2f\n",
		},
		{
			"带标签的 break 穿两层 try",
			`let h = ""; outer: for (let i = 0; i < 3; i++) { for (let k = 0; k < 3; k++) { try { if (i === 1) break outer; h += "" + i + k } finally { h += "F" } } }
			 __out.push("H:" + h);`,
			"H:00F01F02FF\n",
		},
		{
			"switch 里的 break 穿 try",
			`function o(){ let s = ""; switch (1) { case 1: try { s += "o1"; break } finally { s += "o-f" } default: s += "o3" } return s }
			 __out.push("O:" + o());`,
			"O:o1o-f\n",
		},
		{
			"只 break 内层循环时不碰外层 try 的 finally 归属",
			`let pv = ""; for (let i = 0; i < 2; i++) { try { for (let k = 0; k < 2; k++) { if (k === 1) break; pv += "" + i + k } if (i === 1) break } finally { pv += "F" } }
			 __out.push("P:" + pv);`,
			"P:00F10F\n",
		},
		{
			"finally 体里再嵌套 try/return",
			`function q(){ try { return "q1" } finally { try { return "q2" } finally { __out.push("q-inner") } } }
			 __out.push("Q:" + q());`,
			"q-inner\nQ:q2\n",
		},
		{
			"finally 含 var/let/function/class 声明: 内联重复编译不得误报重复声明",
			`function r(){ try { return "r" } finally { var v = 1; let l = 2; function g(){ return 3 } class K {} } }
			 __out.push("R:" + r());`,
			"R:r\n",
		},
		{
			"无 finally 的 try/catch 里 return 不受影响 (回归守卫)",
			`function m(){ try { return "m-try" } catch(e2) { return "m-catch" } }
			 __out.push("M:" + m());`,
			"M:m-try\n",
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

// TestFinallyLoopContinue 循环里 continue: 本轮 finally 必须执行 (rMkA8D)。
// k=0 → 0+100; k=1 (continue) → finally 仍 +100; k=2 → 2+100, 合计 302。
// 旧实现跳过 continue 轮的 finally, 得 202。
func TestFinallyLoopContinue(t *testing.T) {
	got := evalOut(t, `
		function j(){ let s=0; for(let k=0;k<3;k++){ try { if(k===1) continue; s+=k } finally { s+=100 } } return s }
		__out.push("j:" + j());
	`)
	want := "j:302\n"
	if got != want {
		t.Errorf("循环+finally\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// TestFinallyPendingThrowNoLeak 是「进入 finally 后挂起的异常不得残留 / 不得被覆盖」
// 的回归。
//
// 旧实现把它放在 VM 全局 pendingThrow, 两类坏形状:
//  1. finally 里 return / throw 直接离开 ⇒ 挂起值没机会被清, 残留到之后某个
//     毫不相干的 END_FINALLY 上被误重抛 (最恶劣: 正常代码突然"抛出"一个
//     几十行前的老异常);
//  2. 嵌套 finally 互相覆盖 ⇒ 外层挂起值被内层清掉, 原异常被静默吞掉。
// 修法: 挂起值改挂在 tryStack 条目上 (tryEntry.inFinally / pendingVal),
// 随条目出栈自然消失, 嵌套时各持一份。
func TestFinallyPendingThrowNoLeak(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{
			"finally 里 throw 逃出后, 同帧后续 finally 不得误重抛",
			`function f1(){
				try { try { throw "A" } finally { throw "B" } } catch(e) { __out.push("caught:" + e) }
				try { __out.push("body2") } finally { __out.push("fin2") }
				__out.push("done")
			 }
			 f1();`,
			"caught:B\nbody2\nfin2\ndone\n",
		},
		{
			"内层 catch 吃掉新异常后, 外层 finally 仍要重抛原异常",
			`function f2(){
				try { throw "A2" } finally {
					try { throw "B2" } catch(e) { __out.push("caught:" + e) }
					__out.push("finbody")
				}
			 }
			 try { f2() } catch(e) { __out.push("outer:" + e) }`,
			"caught:B2\nfinbody\nouter:A2\n",
		},
		{
			"内层带 finally 的新异常覆盖外层挂起值",
			`function f3(){
				try { throw "A3" } finally {
					try { throw "B3" } finally { __out.push("inner-fin") }
				}
			 }
			 try { f3() } catch(e) { __out.push("outer:" + e) }`,
			"inner-fin\nouter:B3\n",
		},
		{
			"try{throw}finally{return} 之后同帧 finally 不得误重抛",
			`function g(){ try { throw "A4" } finally { return "g-ret" } }
			 __out.push("g:" + g());
			 function after(){ try { __out.push("body4") } finally { __out.push("fin4") } }
			 after();
			 __out.push("done4");`,
			"g:g-ret\nbody4\nfin4\ndone4\n",
		},
		{
			"跨帧: 子函数挂起的异常不得泄漏到调用方",
			`function h(){ try { throw "x1" } finally { throw "x2" } }
			 try { h() } catch(e) { __out.push("caught:" + e) }
			 function after(){ try { __out.push("body5") } finally { __out.push("fin5") } }
			 after();
			 __out.push("done5");`,
			"caught:x2\nbody5\nfin5\ndone5\n",
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

// TestCatchDoesNotPopOuterTryEntry 是「catch 体不得弹掉外层 try 条目」的回归。
//
// 编译器曾在 catch 体末尾**无条件**发一条 POP_TRY。无 finally 的 try/catch 里
// catch 体开头并没有压入 finally 保护条目, 而进入 catch 时原始 try 条目已被
// handleThrowInner 弹出 ⇒ 这条 POP_TRY 弹掉的是更外层 try 的条目, 后果是
// 外层 finally 整段被跳过 (异常也不再被外层 finally 兜住)。
//
// 修法: 该 POP_TRY 只在 hasFinally 时发射 (与 catch 体开头那次 PUSH_TRY 0 配对)。
// 形状取自 probe4; 期望值与 Node 实测一致。
func TestCatchDoesNotPopOuterTryEntry(t *testing.T) {
	// A: 内层 catch 之后外层 try 体继续抛 —— 外层 finally 必须仍然生效
	got := evalOut(t, `
		function a(){
			try {
				try { throw "in" } catch(e) { __out.push("caught:" + e) }
				throw "boom";
			} finally { __out.push("outer-fin") }
		}
		try { a() } catch(e) { __out.push("outer-caught:" + e) }
	`)
	if want := "caught:in\nouter-fin\nouter-caught:boom\n"; got != want {
		t.Errorf("外层 finally 被内层 catch 顶掉\nwant:\n%s\ngot:\n%s", want, got)
	}

	// D: 三层 — 最内层 catch, 外面两层 finally 都要跑, 且新异常穿出两层 finally
	got = evalOut(t, `
		function d(){
			try {
				try {
					try { throw "in4" } catch(e) { __out.push("caught:" + e) }
					throw "boom4";
				} finally { __out.push("mid-fin") }
			} finally { __out.push("outer-fin") }
		}
		try { d() } catch(e) { __out.push("caught-outer:" + e) }
	`)
	if want := "caught:in4\nmid-fin\nouter-fin\ncaught-outer:boom4\n"; got != want {
		t.Errorf("多层 finally 被内层 catch 顶掉\nwant:\n%s\ngot:\n%s", want, got)
	}
}

// 防未使用告警 (runFinallyEval 供外部场景用; 保留以便后续测试扩展)。
var _ = runFinallyEval
