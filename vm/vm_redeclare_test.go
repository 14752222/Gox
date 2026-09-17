package vm

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/stdlib"
)

// 本文件是 let/const/class 重声明检查的回归测试。
//
// 历史问题: 顶层声明 (OP_DECLARE/OP_DECLARE_CONST) 直接覆盖全局环境中的
// 旧绑定，REPL 里 `let a = 10` 后再 `const a = obs(100)` 静默成功；
// 同一作用域内的 let/let 重复声明也不报错。
//
// 修复后的语义 (对齐浏览器/Node):
//   - 全局: 词法声明 (let/const/class/import) 与已有词法声明或函数声明
//     冲突 → SyntaxError；REPL 跨行场景由运行时对照持久化全局环境检查
//   - 同一作用域: prescanScope 在编译期发现重复声明 → SyntaxError
//   - 例外: 函数声明允许互相重定义；let 可遮蔽内置全局 (console/Math 等)

// expectSyntaxError 断言求值以 SyntaxError (重声明) 失败。
func expectSyntaxError(t *testing.T, input string) {
	t.Helper()
	_, err := EvalWithGlobals(input, stdlib.SetupGlobals())
	if err == nil {
		t.Fatalf("expected SyntaxError for %q, got success", input)
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("expected SyntaxError for %q, got: %v", input, err)
	}
}

// TestGlobalRedeclareAcrossLines 跨行全局重声明必须报 SyntaxError (REPL 场景)。
func TestGlobalRedeclareAcrossLines(t *testing.T) {
	cases := [][2]string{
		{`let a = 10`, `const a = 20`},
		{`let a = 10`, `let a = 20`},
		{`const a = 10`, `let a = 20`},
		{`const a = 10`, `const a = 20`},
		{`class C {}`, `let C = 1`},
		{`let C = 1`, `class C {}`},
		{`class C {}`, `class C {}`},
		{`let f = 1`, `function f() {}`},
		{`function f() {}`, `let f = 1`},
		{`function f() {}`, `const f = 1`},
		{`let [a] = [1]`, `let a = 2`},
	}
	for _, c := range cases {
		globals := stdlib.SetupGlobals()
		if _, err := EvalWithGlobals(c[0], globals); err != nil {
			t.Fatalf("first line %q unexpected error: %v", c[0], err)
		}
		_, err := EvalWithGlobals(c[1], globals)
		if err == nil {
			t.Fatalf("redeclare %q after %q should be a SyntaxError", c[1], c[0])
		}
		if !strings.Contains(err.Error(), "SyntaxError") {
			t.Fatalf("redeclare %q after %q: expected SyntaxError, got: %v", c[1], c[0], err)
		}
	}
}

// TestGlobalRedeclareAllowed 合法的跨行操作不得误伤。
func TestGlobalRedeclareAllowed(t *testing.T) {
	globals := stdlib.SetupGlobals()
	mustEval := func(input string) {
		t.Helper()
		if _, err := EvalWithGlobals(input, globals); err != nil {
			t.Fatalf("eval %q unexpected error: %v", input, err)
		}
	}

	// 函数声明允许重定义
	mustEval(`function f() { return 1 }`)
	mustEval(`function f() { return 2 }`)
	res, err := EvalWithGlobals(`f()`, globals)
	if err != nil || res.Inspect() != "2" {
		t.Fatalf("function redefinition broken: %v %v", res, err)
	}

	// let 可遮蔽内置全局 (与浏览器一致)
	mustEval(`let obs2 = 123`)
	res, err = EvalWithGlobals(`obs2`, globals)
	if err != nil || res.Inspect() != "123" {
		t.Fatalf("let shadowing builtin value broken: %v %v", res, err)
	}

	// let 声明后可重新赋值
	mustEval(`let x = 1`)
	mustEval(`x = 2`)
	res, err = EvalWithGlobals(`x`, globals)
	if err != nil || res.Inspect() != "2" {
		t.Fatalf("let reassignment broken: %v %v", res, err)
	}

	// 块作用域遮蔽不影响外层
	mustEval(`let y = 1`)
	mustEval(`{ let y = 99; }`)
	res, err = EvalWithGlobals(`y`, globals)
	if err != nil || res.Inspect() != "1" {
		t.Fatalf("block shadowing leaked: %v %v", res, err)
	}
}

// TestGlobalConstAssign 全局 const 重新赋值必须抛 TypeError (可捕获)。
func TestGlobalConstAssign(t *testing.T) {
	globals := stdlib.SetupGlobals()
	if _, err := EvalWithGlobals(`const a = 1`, globals); err != nil {
		t.Fatalf("const declare: %v", err)
	}
	_, err := EvalWithGlobals(`a = 2`, globals)
	if err == nil || !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("const reassignment should be TypeError, got: %v", err)
	}
	// 赋值失败后值不变
	res, err2 := EvalWithGlobals(`a`, globals)
	if err2 != nil || res.Inspect() != "1" {
		t.Fatalf("const value changed after failed assign: %v %v", res, err2)
	}
}

// TestSameScopeRedeclare 同一作用域内的重复声明是编译期 SyntaxError。
func TestSameScopeRedeclare(t *testing.T) {
	cases := []string{
		`let a = 1; let a = 2`,
		`let a = 1; const a = 2`,
		`const a = 1; let a = 2`,
		`const a = 1; const a = 2`,
		`let a = 1, a = 2`,
		`{ let a = 1; let a = 2 }`,
		`function f() { let a = 1; let a = 2 }`,
		`function f(x) { let x = 1 }`,
		`class C {}; class C {}`,
		`let C = 1; class C {}`,
		`class C {}; let C = 1`,
		`let f = 1; function f() {}`,
		`function f() {} let f = 1`,
		`function f() {} const f = 1`,
		`let [a] = [1]; let a = 2`,
		`let a = 1; let [a] = [2]`,
		`let {a} = {}; let a = 1`,
		`for (let i = 0; i < 1; i++) { let j = i; let j = 2 }`,
	}
	for _, c := range cases {
		expectSyntaxError(t, c)
	}
}

// TestAssignDestructure 赋值解构 ([x] = v / ({x} = v)) 的回归测试。
// 历史问题: 解析器把赋值目标产成数组/对象字面量，编译器只识别解构模式，
// 导致不发射任何指令、表达式语句的 OP_POP 空栈 panic。赋值解构此前完全不可用。
func TestAssignDestructure(t *testing.T) {
	cases := []struct{ input, expected string }{
		{`let x = 1; [x] = [5]; x`, "5"},
		{`let x = 1; ({x} = {x: 5}); x`, "5"},
		{`let x = 1; let y = ([x] = [9]); "" + x + y`, "99"},
		{`let a = 1, b = 2; [b, a] = [a, b]; "" + a + b`, "21"},
		{`let r = []; [q9, ...r] = [1, 2, 3]; "" + r`, "2,3"},
		{`let o1 = 1, o2 = 2; ({x: o1, y: o2} = {x: 10, y: 20}); "" + o1 + o2`, "1020"},
		{`let d; [d = 7] = []; d`, "7"},
		{`let m = 1, n = 2; [[m], {x: n}] = [[5], {x: 6}]; "" + m + n`, "56"},
	}
	for _, tt := range cases {
		res, err := EvalWithGlobals(tt.input, stdlib.SetupGlobals())
		if err != nil {
			t.Fatalf("eval %q unexpected error: %v", tt.input, err)
		}
		if res == nil || res.Inspect() != tt.expected {
			t.Fatalf("eval %q = %v, want %s", tt.input, res, tt.expected)
		}
	}
}

// TestRedeclareLegalPatterns 合法代码不得被重声明检查误伤。
func TestRedeclareLegalPatterns(t *testing.T) {
	cases := []struct{ input, expected string }{
		// 函数重定义
		{`function f() { return 1 } function f() { return 2 } f()`, "2"},
		// 块作用域遮蔽
		{`let a = 1; { let a = 2; } a`, "1"},
		{`let a = 1; if (true) { let a = 2; } a`, "1"},
		// for 循环同名变量各在自己的作用域
		{`let s = 0; for (let i = 0; i < 2; i++) s += i; for (let i = 0; i < 2; i++) s += i; s`, "2"},
		{`let out = ""; for (const x of [1,2]) { out += x } for (const x of [3]) { out += x } out`, "123"},
		// 声明提升仍工作
		{`f(); function f() { return 42 }`, "42"},
		{`let x = 1; g(); function g() { return x } g()`, "1"},
		// 解构声明
		{`let [a, b] = [1, 2]; a + b`, "3"},
		{`const { p, q } = { p: 1, q: 2 }; p + q`, "3"},
		{`const [m, n] = [1, 2]; const [u, v] = [3, 4]; m + n + u + v`, "10"},
		// 赋值解构 (非声明) 写已有绑定
		{`let x = 1; [x] = [5]; x`, "5"},
		{`let x = 1, y = 2; [y, x] = [x, y]; "" + x + y`, "21"},
		// 函数参数解构
		{`function f([a, b]) { return a + b } f([1, 2])`, "3"},
		// catch 参数与 catch 体内 let 遮蔽
		{`let r = ""; try { throw "e" } catch (e) { let e2 = 1; r = e } r`, "e"},
		{`try {} catch (e) {} try {} catch (e) {} "ok"`, "ok"},
		// 命名函数表达式自引用
		{`const f = function g() { return typeof g }; f()`, "function"},
		// 类与静态/实例方法
		{`class C { m() { return 1 } } new C().m()`, "1"},
		{`class A {} class B { constructor() { this.x = 1 } } new B().x + 1`, "2"},
		// 嵌套函数各自作用域同名
		{`function o() { let v = 1; function i() { let v = 2; return v } return v + i() } o()`, "3"},
		// 闭包捕获外层 let
		{`let c = 0; function inc() { c++ } inc(); inc(); c`, "2"},
	}
	for _, tt := range cases {
		globals := stdlib.SetupGlobals()
		res, err := EvalWithGlobals(tt.input, globals)
		if err != nil {
			t.Fatalf("eval %q unexpected error: %v", tt.input, err)
		}
		if res == nil || res.Inspect() != tt.expected {
			t.Fatalf("eval %q = %v, want %s", tt.input, res, tt.expected)
		}
	}
}
