package vm

// ===== vm 包共享测试工具 (唯一定义点) =====
//
// 此前 eval 入口与断言家族散落在 vm_test (testEval/testNumber...)、
// stdlib_test (evalJS/assertNumber...)、vm_regression_test (testEvalCatch)
// 三处且语义重叠。现集中到本文件; 语义差异保留为不同入口:
//   - evalJS          完整管线 (安装 stdlib), 出错即失败, 返回结果值
//   - testEval        裸环境 (无 stdlib) 的手动管线 —— 验证"不依赖宿主 API"
//     的语言核心语义时用它, 污染面最小
//   - testEvalCatch   同完整管线但不把 vm error 视为失败 (交由调用方断言)
//   - evalWithStdlib  testEvalCatch 的"出错即失败"包装
// 断言统一用 assert* 命名 (test* 旧名已全量替换)。

import (
	"testing"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/stdlib"
)

// evalJS 编译并执行 JS 源码，返回结果。
func evalJS(t *testing.T, input string) object.Value {
	result, err := Eval(input)
	if err != nil {
		t.Fatalf("Eval error for input %q: %v", input, err)
	}
	return result
}

// assertJSAsync 断言 Promise 反应回调的异步结果 (rrpZIw 微任务语义)。
//
// 微任务队列引入后, .then/.catch 回调在脚本同步执行完成后才运行,
// 旧式 "(function(){ promise.then(v=>r=...); return r })()" 的写法必然
// 读到初始值 (V8 同样如此) —— 那些断言本身就建立在错误的同步语义上。
// 约定: 脚本把最终结果写入 globalThis.__r; 本函数在 drain 微任务并跑完
// 事件循环后读取 __r 与 expected 比较。
func assertJSAsync(t *testing.T, input, expected string) {
	t.Helper()
	v, err := EvalVM(input)
	if err != nil {
		t.Fatalf("Eval error for input %q: %v", input, err)
	}
	if err := v.RunTimers(); err != nil {
		t.Fatalf("RunTimers error for input %q: %v", input, err)
	}
	got, ok := v.Globals().Get("__r")
	if !ok {
		t.Fatalf("回调未执行 (__r 未设置) for input %q", input)
	}
	assertString(t, got, expected)
}

// testEval 在裸环境 (不装 stdlib) 里编译并执行，返回最后一个表达式的值。
func testEval(t *testing.T, input string) object.Value {
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

	vm := New(c.Bytes(), c.Constants(), c.NumLocals())
	if err := vm.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}

	return vm.LastPopped()
}

// testEvalCatch 在 stdlib 全局环境下编译并执行源码，返回结果与执行错误。
// 与 evalJS 不同: 不把 vm error 视为测试失败，交由调用方断言。
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

// assertNumber 检查值是否为指定数字。
func assertNumber(t *testing.T, obj object.Value, expected float64) {
	t.Helper()
	num, ok := obj.(*object.Number)
	if !ok {
		t.Fatalf("expected Number, got %T (%s)", obj, obj.Inspect())
	}
	if num.Value != expected {
		t.Fatalf("expected %v, got %v", expected, num.Value)
	}
}

// assertString 检查值是否为指定字符串。
func assertString(t *testing.T, obj object.Value, expected string) {
	t.Helper()
	str, ok := obj.(*object.String)
	if !ok {
		t.Fatalf("expected String, got %T (%s)", obj, obj.Inspect())
	}
	if str.Value != expected {
		t.Fatalf("expected %q, got %q", expected, str.Value)
	}
}

// assertBoolean 检查值是否为指定布尔值。
func assertBoolean(t *testing.T, obj object.Value, expected bool) {
	t.Helper()
	b, ok := obj.(*object.Boolean)
	if !ok {
		t.Fatalf("expected Boolean, got %T (%s)", obj, obj.Inspect())
	}
	if b.Value != expected {
		t.Fatalf("expected %v, got %v", expected, b.Value)
	}
}
