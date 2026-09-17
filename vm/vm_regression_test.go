package vm

import (
	"strconv"
	"strings"
	"testing"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/stdlib"
)

// 本文件是安全/健壮性修复的回归测试。
//
// 覆盖的历史问题:
//  1. 字符串拼接无长度上限 → OOM 硬崩溃 (fatal, 不可 recover)
//  2. 各类运行时错误 (ReferenceError/TypeError/栈溢出 RangeError)
//     以裸 Go error 返回，绕过 tryStack，try/catch 无法捕获
//  3. 回调桥 (getter/Proxy/数组方法回调) 里的异常逃逸 try/catch，
//     且数组方法回调抛错后循环不中止、异常被静默吞掉
//  4. null/undefined 属性访问不抛 TypeError
//  5. 命名函数表达式不绑定自身名字，无法递归
//  6. 解析器对深层嵌套无上限 (Go 栈溢出) 且存在 O(n²) 解析开销
//  7. 条件表达式被错解析为左结合 `(a ? b : c) ? d : e`，导致嵌套三元取到
//     错误分支 (gfx 的 tabs_demo 面板渲染错颜色就是踩到这个)

// testEvalCatch 编译并执行源码，返回脚本输出与执行错误。
// 与 testEval 不同: 不把 vm error 视为测试失败，交由调用方断言。
// 使用 stdlib 全局环境 (测试里需要 Error/TypeError/Object/Proxy 等)。
func testEvalCatch(t *testing.T, input string) (object.Value, error) {
	t.Helper()
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors:\n%s", p.Errors().String())
	}
	c := compiler.New()
	if err := c.Compile(program); err != nil {
		t.Fatalf("compiler error: %v", err)
	}
	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), stdlib.SetupGlobals())
	err := vm.Run()
	return vm.LastPopped(), err
}

// evalWithStdlib 在 stdlib 全局环境下执行源码，出错即失败，返回结果值。
func evalWithStdlib(t *testing.T, input string) object.Value {
	t.Helper()
	res, err := testEvalCatch(t, input)
	if err != nil {
		t.Fatalf("vm error: %v", err)
	}
	return res
}

// ===== 1. 字符串长度上限 =====

func TestStringConcatLengthLimit(t *testing.T) {
	// 翻倍拼接最终触发上限: 必须抛 RangeError 而不是 OOM 崩溃
	_, err := testEvalCatch(t, `let s = "a"; for (let i = 0; i < 60; i++) s = s + s; s;`)
	if err == nil {
		t.Fatal("expected RangeError from string length limit, got none")
	}
	if !strings.Contains(err.Error(), "RangeError") || !strings.Contains(err.Error(), "Invalid string length") {
		t.Fatalf("expected RangeError: Invalid string length, got: %v", err)
	}
}

func TestTemplateLiteralLengthLimit(t *testing.T) {
	_, err := testEvalCatch(t, "let s = \"a\"; for (let i = 0; i < 60; i++) s = `${s}${s}`; s;")
	if err == nil || !strings.Contains(err.Error(), "Invalid string length") {
		t.Fatalf("expected RangeError: Invalid string length, got: %v", err)
	}
}

func TestStringConcatUnderLimit(t *testing.T) {
	// 正常长度拼接不受影响
	res := testEval(t, `"ab" + "cd" + "ef"`)
	testString(t, res, "abcdef")
}

// ===== 2/3. try/catch 必须能捕获所有 JS 语义错误 =====

func TestTryCatchRuntimeErrors(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		want  string
		unerr bool // 未捕获时应有的错误片段
	}{
		{"reference", `try { undefinedVar123; } catch (e) { "caught:" + e.name }`, "caught:ReferenceError", false},
		{"plain recursion", `function f() { return f(); } try { f(); } catch (e) { "caught:" + e.name }`, "caught:RangeError", false},
		{"map recursion", `let m = (a) => [1].map(m); try { m(1); } catch (e) { "caught:" + e.name }`, "caught:RangeError", false},
		{"map throw", `try { [1,2].map(function(x){ if (x === 2) throw new TypeError("boom"); return x; }); } catch (e) { "caught:" + e.name + ":" + e.message }`, "caught:TypeError:boom", false},
		{"forEach throw", `let n = 0; try { [1,2,3].forEach(function(x){ n++; if (x === 2) throw new Error("stop"); }); } catch (e) { "caught:" + n }`, "caught:2", false},
		{"sort throw", `try { [3,1,2].sort(function(a,b){ throw new Error("cmp"); }); } catch (e) { "caught:" + e.message }`, "caught:cmp", false},
		{"proxy recursion", `let p = new Proxy({}, { get(t,k){ return p[k]; } }); try { p.x; } catch (e) { "caught:" + e.name }`, "caught:RangeError", false},
		{"getter recursion", `let o = {}; Object.defineProperty(o, "s", { get: function(){ return o.s; } }); try { o.s; } catch (e) { "caught:" + e.name }`, "caught:RangeError", false},
		{"not a function", `try { let q = null; q(); } catch (e) { "caught:" + e.name }`, "caught:TypeError", false},
		{"not iterable", `try { for (const x of 5) {} } catch (e) { "caught:" + e.name }`, "caught:TypeError", false},
		{"not a constructor", `try { new (5)(); } catch (e) { "caught:" + e.name }`, "caught:TypeError", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := testEvalCatch(t, tc.src)
			if err != nil {
				t.Fatalf("error escaped try/catch: %v", err)
			}
			s, ok := res.(*object.String)
			if !ok {
				t.Fatalf("expected String result, got %T (%s)", res, res.Inspect())
			}
			if !strings.Contains(s.Value, tc.want) {
				t.Fatalf("expected %q to contain %q", s.Value, tc.want)
			}
		})
	}
}

// 回调抛错时数组方法必须立即中止: 后续元素不再调用回调。
func TestArrayCallbackAbortsOnThrow(t *testing.T) {
	res := testEval(t, `
		let calls = [];
		try {
			[1,2,3].map(function(x){ calls.push(x); if (x === 1) throw new Error("stop"); return x; });
		} catch (e) {}
		calls.join(",");`)
	testString(t, res, "1")
}

// map 回调抛错后，catch 收到的必须是错误对象本身 (而非塞进结果的数组)。
func TestArrayCallbackThrowsErrorObject(t *testing.T) {
	res := evalWithStdlib(t, `
		try {
			[1,2].map(function(x){ if (x === 2) throw new TypeError("boom"); return x * 10; });
		} catch (e) { e.name + ":" + e.message + ":" + (e instanceof Error); }`)
	testString(t, res, "TypeError:boom:true")
}

// 未捕获时错误正常向外传播 (vm error)。
func TestUncatchedExceptionStillPropagates(t *testing.T) {
	_, err := testEvalCatch(t, `undefinedVar123;`)
	if err == nil || !strings.Contains(err.Error(), "ReferenceError") {
		t.Fatalf("expected ReferenceError to propagate, got: %v", err)
	}
}

// ===== 4. null/undefined 属性访问 =====

func TestNullOrUndefinedPropertyAccess(t *testing.T) {
	res := evalWithStdlib(t, `try { null.x; } catch (e) { e.name + "|" + e.message }`)
	s, ok := res.(*object.String)
	if !ok || !strings.Contains(s.Value, "TypeError") {
		t.Fatalf("expected TypeError for null.x, got: %s", res.Inspect())
	}
	res = evalWithStdlib(t, `try { let u; u.y = 1; } catch (e) { e.name }`)
	s, ok = res.(*object.String)
	if !ok || s.Value != "TypeError" {
		t.Fatalf("expected TypeError for undefined property set, got: %s", res.Inspect())
	}
	// typeof 未声明变量仍应安全返回 "undefined" (不得因此抛 ReferenceError)
	res = evalWithStdlib(t, `typeof notDeclaredAtAll`)
	testString(t, res, "undefined")
}

// ===== 5. 命名函数表达式自绑定 =====

func TestNamedFunctionExpression(t *testing.T) {
	// 名字在函数体内可见
	res := testEval(t, `let h = function named(){ return typeof named; }; h();`)
	testString(t, res, "function")
	// 名字可用于递归
	res = testEval(t, `let f = function fact(n){ return n <= 1 ? 1 : n * fact(n-1); }; f(5);`)
	testNumber(t, res, 120)
	// 名字不泄漏到外层作用域
	res = testEval(t, `let g = function named(){}; typeof named;`)
	testString(t, res, "undefined")
	// 同名参数遮蔽函数名 (规范行为)
	res = testEval(t, `let q = function f(f){ return f; }; q(42);`)
	testNumber(t, res, 42)
	// 命名函数表达式递归的栈溢出可捕获
	res = testEval(t, `try { let r = function f(){ return f(); }; r(); } catch (e) { e.name }`)
	testString(t, res, "RangeError")
}

// ===== 6. 解析器嵌套深度上限 =====

func TestParserNestingDepthLimit(t *testing.T) {
	// 超过上限 → 解析错误 (而非 Go 栈溢出崩溃)
	deep := strings.Repeat("(", 3000) + "1" + strings.Repeat(")", 3000) + ";"
	l := lexer.New(deep)
	p := parser.New(l)
	p.ParseProgram()
	if !p.Errors().HasErrors() {
		t.Fatal("expected parse error for deeply nested input")
	}
	if !strings.Contains(p.Errors().String(), "nesting depth") {
		t.Fatalf("expected nesting depth error, got:\n%s", p.Errors().String())
	}

	// 深度在上限内的正常代码不受影响
	ok := strings.Repeat("(", 1500) + "1" + strings.Repeat(")", 1500) + ";"
	l2 := lexer.New(ok)
	p2 := parser.New(l2)
	p2.ParseProgram()
	if p2.Errors().HasErrors() {
		t.Fatalf("expected valid nesting to parse, got:\n%s", p2.Errors().String())
	}
}

// ===== VM panic 防护 =====

// 构造一个能触发底层 panic 的场景较难从 JS 层稳定构造，
// 这里直接调用 runProtected 验证 recover 生效。
func TestRunProtectedRecoversPanic(t *testing.T) {
	v := New(nil, nil, 0)
	err := v.runProtected(func() error {
		var s []int
		_ = s[10] // index out of range panic
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "InternalError") {
		t.Fatalf("expected InternalError from recovered panic, got: %v", err)
	}
}

// ===== stdlib 全局兼容性冒烟 =====

func TestStdlibGlobalsSmoke(t *testing.T) {
	globals := stdlib.SetupGlobals()
	l := lexer.New(`JSON.stringify({a:1}) + "|" + [1,2,3].reduce((a,b)=>a+b, 0)`)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors:\n%s", p.Errors().String())
	}
	c := compiler.New()
	if err := c.Compile(program); err != nil {
		t.Fatalf("compiler error: %v", err)
	}
	vm := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), globals)
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}
	testString(t, vm.LastPopped(), `{"a":1}|6`)
}

// ===== 条件表达式右结合 (回归 7) =====

// TestConditionalExpressionRightAssociative 锁定嵌套三元的求值正确性。
//
// 历史缺陷: 分支按 TERNARY 自身优先级解析, 使 `?` 无法被分支消费而被外层
// 循环捡走, `a ? b : c ? d : e` 变成 `(a ? b : c) ? d : e`。
// 对 t=0 这类最常见的取值, 错的语法树恰好会取到第二个分支, 表现为
// "分支判断莫名其妙偏一位", 排查时极难定位到解析器。
func TestConditionalExpressionRightAssociative(t *testing.T) {
	// 三分支三取值 —— 每个取值都必须命中自己的分支
	got := map[float64]string{}
	for _, tv := range []float64{0, 1, 2} {
		_, result := runEvalVM(t, `
			const t = `+formatNum(tv)+`;
			const f = (x) => x;
			t === 0 ? f("a") : t === 1 ? f("b") : f("c");
		`)
		got[tv] = result.(*object.String).Value
	}
	want := map[float64]string{0: "a", 1: "b", 2: "c"}
	for tv, w := range want {
		if got[tv] != w {
			t.Errorf("t=%v: 取到分支 %q, 期望 %q", tv, got[tv], w)
		}
	}

	// 纯字面量 (无函数调用) 同样右结合
	for _, tc := range []struct{ t, want float64 }{{0, 1}, {1, 2}, {9, 3}} {
		_, result := runEvalVM(t, `const t = `+formatNum(tc.t)+
			`; t === 0 ? 1 : t === 1 ? 2 : 3;`)
		testNumber(t, result, tc.want)
	}

	// 四层嵌套: 每层都取到正确分支
	_, result := runEvalVM(t, `
		const t = 2;
		const f = (x) => x;
		t === 0 ? f("a") : t === 1 ? f("b") : t === 2 ? f("c") : f("d");
	`)
	testString(t, result, "c")

	// consequence 位置嵌套三元: a ? (b ? c : d) : e
	_, result = runEvalVM(t, `const f = (x) => x; 1 ? 0 ? f("inner") : f("mid") : f("outer");`)
	testString(t, result, "mid")

	// 括号可覆盖默认结合方向
	_, result = runEvalVM(t, `const f = (x) => x; (1 ? 0 : f("x")) ? f("hi") : f("lo");`)
	testString(t, result, "lo")
}

// formatNum 把测试用的整数渲染成 JS 字面量。
func formatNum(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}
