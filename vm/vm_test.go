package vm

import (
	"os"
	"testing"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
	"github.com/14752222/Gox/runtime"
	"github.com/14752222/Gox/stdlib"
)

// testEval 等共享测试工具定义在 testutil_test.go (断言统一用 assert* 命名)。

// ===== 算术运算测试 =====

func TestArithmetic(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"1 + 2;", 3},
		{"10 - 3;", 7},
		{"4 * 5;", 20},
		{"100 / 4;", 25},
		{"10 % 3;", 1},
		{"2 ** 10;", 1024},
		{"-5;", -5},
		{"1 + 2 * 3;", 7},   // 优先级
		{"(1 + 2) * 3;", 9}, // 括号
		{"10 / 3;", 3.3333333333333335},
		{"3 + 4 - 2;", 5},
		{"2 * 3 * 4;", 24},
	}

	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

func TestBitwise(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"5 & 3;", 1},
		{"5 | 3;", 7},
		{"5 ^ 3;", 6},
		{"1 << 4;", 16},
		{"256 >> 2;", 64},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

// ===== 变量测试 =====

func TestLetStatement(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"let x = 5; x;", 5},
		{"let x = 10; let y = 20; x + y;", 30},
		{"let a = 1; let b = a + 1; b;", 2},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

func TestConstStatement(t *testing.T) {
	result := testEval(t, "const PI = 3.14; PI;")
	assertNumber(t, result, 3.14)
}

func TestAssignmentExpression(t *testing.T) {
	result := testEval(t, "let x = 5; x = 10; x;")
	assertNumber(t, result, 10)
}

// ===== 比较和逻辑 =====

func TestComparisons(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"1 < 2;", true},
		{"2 < 1;", false},
		{"2 > 1;", true},
		{"1 > 2;", false},
		{"1 <= 1;", true},
		{"1 >= 1;", true},
		{"1 == 1;", true},
		{"1 == '1';", true},   // 宽松相等
		{"1 === '1';", false}, // 严格相等
		{"1 != 2;", true},
		{"1 !== '1';", true},
		{"true && true;", true},
		{"true && false;", false},
		{"false || true;", true},
		{"!false;", true},
		{"!true;", false},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertBoolean(t, result, tt.expected)
	}
}

// ===== 控制流 =====

func TestIfStatement(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"if (true) { 1; } else { 2; }", 1},
		{"if (false) { 1; } else { 2; }", 2},
		{"if (1 > 0) { 42; }", 42},
		{"if (0 > 1) { 42; } else { 99; }", 99},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

func TestWhileLoop(t *testing.T) {
	input := `
		let sum = 0;
		let i = 1;
		while (i <= 10) {
			sum = sum + i;
			i = i + 1;
		}
		sum;
	`
	result := testEval(t, input)
	assertNumber(t, result, 55)
}

func TestForLoop(t *testing.T) {
	input := `
		let total = 0;
		let i = 0;
		while (i < 5) {
			total = total + i;
			i = i + 1;
		}
		total;
	`
	result := testEval(t, input)
	assertNumber(t, result, 10)
}

func TestTraditionalForLoop(t *testing.T) {
	input := `
		let sum = 0;
		for (let i = 1; i <= 5; i++) {
			sum = sum + i;
		}
		sum;
	`
	result := testEval(t, input)
	assertNumber(t, result, 15)
}

func TestTraditionalForEmptyCondition(t *testing.T) {
	// 空 condition + 内部 break: for (let i = 0; ; i++)
	input := `
		let n = 0;
		for (let i = 0; ; i++) {
			if (i >= 3) { break; }
			n = n + i;
		}
		n;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3) // 0 + 1 + 2

	// 空 condition + 空 update: for (let i = 0; ; )
	input = `
		let n = 0;
		let i = 0;
		for (; ; ) {
			if (i >= 3) { break; }
			n = n + i;
			i = i + 1;
		}
		n;
	`
	result = testEval(t, input)
	assertNumber(t, result, 3)
}

func TestTraditionalForEmptyUpdate(t *testing.T) {
	// 空 update + 循环体内手动递增
	input := `
		let m = 0;
		for (let i = 0; i < 3; ) {
			m = m + i;
			i = i + 1;
		}
		m;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3)
}

func TestTraditionalForInfiniteLoop(t *testing.T) {
	// for (;;) 纯无限循环靠 break 退出
	input := `
		let t = 0;
		for (;;) {
			t = t + 1;
			if (t == 3) { break; }
		}
		t;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3)
}

func TestTraditionalForContinue(t *testing.T) {
	// continue 应跳到 update 段: i 仍递增, 只累加奇数
	input := `
		let sum = 0;
		for (let i = 0; i < 6; i++) {
			if (i % 2 == 0) { continue; }
			sum = sum + i;
		}
		sum;
	`
	result := testEval(t, input)
	assertNumber(t, result, 9) // 1 + 3 + 5
}

func TestTraditionalForExpressionInit(t *testing.T) {
	// 表达式 init (非 let/const)
	input := `
		let i = 0;
		let sum = 0;
		for (i = 0; i < 3; i++) {
			sum = sum + i;
		}
		sum;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3)
}

func TestBreakContinue(t *testing.T) {
	input := `
		let sum = 0;
		let i = 0;
		while (i < 100) {
			i = i + 1;
			if (i % 2 == 0) {
				continue;
			}
			if (i > 10) {
				break;
			}
			sum = sum + i;
		}
		sum;
	`
	// 1+3+5+7+9 = 25
	result := testEval(t, input)
	assertNumber(t, result, 25)
}

// ===== 函数调用 =====

func TestFunctionDeclaration(t *testing.T) {
	input := `
		function add(a, b) {
			return a + b;
		}
		add(3, 4);
	`
	result := testEval(t, input)
	assertNumber(t, result, 7)
}

func TestFunctionExpression(t *testing.T) {
	input := `
		let mul = function(a, b) { return a * b; };
		mul(6, 7);
	`
	result := testEval(t, input)
	assertNumber(t, result, 42)
}

func TestArrowFunction(t *testing.T) {
	input := `
		let add = (a, b) => a + b;
		add(10, 20);
	`
	result := testEval(t, input)
	assertNumber(t, result, 30)
}

func TestArrowFunctionBlock(t *testing.T) {
	input := `
		let square = (x) => {
			return x * x;
		};
		square(9);
	`
	result := testEval(t, input)
	assertNumber(t, result, 81)
}

func TestClosure(t *testing.T) {
	input := `
		let x = 10;
		function getX() {
			return x;
		}
		getX();
	`
	result := testEval(t, input)
	assertNumber(t, result, 10)
}

func TestRecursiveFunction(t *testing.T) {
	input := `
		function fib(n) {
			if (n < 2) {
				return n;
			}
			return fib(n - 1) + fib(n - 2);
		}
		fib(10);
	`
	result := testEval(t, input)
	assertNumber(t, result, 55)
}

// ===== 字符串和模板字面量 =====

func TestStringConcatenation(t *testing.T) {
	result := testEval(t, `"hello" + " " + "world";`)
	assertString(t, result, "hello world")
}

func TestStringNumberConcat(t *testing.T) {
	result := testEval(t, `"answer: " + 42;`)
	assertString(t, result, "answer: 42")
}

func TestTemplateLiteral(t *testing.T) {
	input := "let name = 'World'; `Hello, ${name}!`;"
	result := testEval(t, input)
	assertString(t, result, "Hello, World!")
}

// ===== 数组 =====

func TestArrayCreation(t *testing.T) {
	result := testEval(t, "[1, 2, 3];")
	arr, ok := result.(*object.Array)
	if !ok {
		t.Fatalf("expected Array, got %T", result)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
}

func TestArrayIndex(t *testing.T) {
	result := testEval(t, "[10, 20, 30][1];")
	assertNumber(t, result, 20)
}

func TestArrayLength(t *testing.T) {
	result := testEval(t, "[1, 2, 3, 4].length;")
	assertNumber(t, result, 4)
}

// ===== 对象 =====

func TestObjectCreation(t *testing.T) {
	result := testEval(t, "let obj = { name: 'Alice', age: 30 }; obj;")
	obj, ok := result.(*object.Object)
	if !ok {
		t.Fatalf("expected Object, got %T (%s)", result, result.Inspect())
	}
	nameVal, found := obj.GetProperty("name")
	if !found {
		t.Fatal("expected property 'name'")
	}
	if nameVal.Inspect() != "Alice" {
		t.Fatalf("expected 'Alice', got %s", nameVal.Inspect())
	}
}

func TestObjectPropertyAccess(t *testing.T) {
	result := testEval(t, "let obj = { x: 10, y: 20 }; obj.x + obj.y;")
	assertNumber(t, result, 30)
}

// ===== for...of =====

func TestForOf(t *testing.T) {
	input := `
		let sum = 0;
		for (let n of [1, 2, 3, 4, 5]) {
			sum = sum + n;
		}
		sum;
	`
	result := testEval(t, input)
	assertNumber(t, result, 15)
}

// ===== typeof =====

func TestTypeof(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"typeof 42;", "number"},
		{"typeof 'hello';", "string"},
		{"typeof true;", "boolean"},
		{"typeof undefined;", "undefined"},
		{"typeof null;", "object"},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertString(t, result, tt.expected)
	}
}

// ===== 三元表达式 =====

func TestConditionalExpression(t *testing.T) {
	result := testEval(t, "1 < 2 ? 'yes' : 'no';")
	assertString(t, result, "yes")
}

// ===== 嵌套作用域 =====

func TestBlockScope(t *testing.T) {
	input := `
		let x = 1;
		{
			let x = 2;
		}
		x;
	`
	result := testEval(t, input)
	assertNumber(t, result, 1)
}

// ===== null 和 undefined =====

func TestNullUndefined(t *testing.T) {
	result := testEval(t, "null;")
	if _, ok := result.(*object.Null); !ok {
		t.Fatalf("expected Null, got %T", result)
	}

	result = testEval(t, "undefined;")
	if _, ok := result.(*object.Undefined); !ok {
		t.Fatalf("expected Undefined, got %T", result)
	}
}

// ===== ES6 特性测试 =====

// 默认参数
func TestDefaultParams(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{
			`function add(a, b = 5) { return a + b; } add(3);`,
			8,
		},
		{
			`function add(a, b = 5) { return a + b; } add(3, 10);`,
			13,
		},
		{
			`function greet(name = 'World') { return name; } greet();`,
			0, // will be string test separately
		},
	}
	for i, tt := range tests {
		result := testEval(t, tt.input)
		if i < 2 {
			assertNumber(t, result, tt.expected)
		} else {
			assertString(t, result, "World")
		}
	}
}

// rest 参数
func TestRestParams(t *testing.T) {
	input := `
		function sum(...nums) {
			let total = 0;
			for (let n of nums) {
				total = total + n;
			}
			return total;
		}
		sum(1, 2, 3, 4, 5);
	`
	result := testEval(t, input)
	assertNumber(t, result, 15)
}

// 数组展开
func TestArraySpread(t *testing.T) {
	input := `
		let a = [1, 2, 3];
		let b = [...a, 4, 5];
		b[3] + b[4];
	`
	result := testEval(t, input)
	assertNumber(t, result, 9)
}

func TestArraySpreadOnly(t *testing.T) {
	input := `
		let a = [10, 20, 30];
		let b = [...a];
		b[0] + b[1] + b[2];
	`
	result := testEval(t, input)
	assertNumber(t, result, 60)
}

// 函数调用展开
func TestCallSpread(t *testing.T) {
	input := `
		function add(a, b, c) {
			return a + b + c;
		}
		let args = [1, 2, 3];
		add(...args);
	`
	result := testEval(t, input)
	assertNumber(t, result, 6)
}

// 对象简写
func TestObjectShorthand(t *testing.T) {
	input := `
		let name = 'Alice';
		let age = 30;
		let obj = { name, age };
		obj.name;
	`
	result := testEval(t, input)
	assertString(t, result, "Alice")
}

// 对象计算属性
func TestObjectComputedProperty(t *testing.T) {
	input := `
		let key = 'dynamic';
		let obj = { [key]: 42 };
		obj.dynamic;
	`
	result := testEval(t, input)
	assertNumber(t, result, 42)
}

// 对象方法
func TestObjectMethod(t *testing.T) {
	input := `
		let obj = {
			x: 10,
			getX() { return this.x; }
		};
		obj.getX();
	`
	result := testEval(t, input)
	assertNumber(t, result, 10)
}

// 自增自减
func TestIncrement(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"let i = 5; ++i; i;", 6},
		{"let i = 5; i++; i;", 6},
		{"let i = 5; ++i;", 6}, // 前缀返回新值
		{"let i = 5; i++;", 5}, // 后缀返回旧值
		{"let i = 5; --i; i;", 4},
		{"let i = 5; i--; i;", 4},
		{"let i = 5; --i;", 4},
		{"let i = 5; i--;", 5},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

// 复合赋值
func TestCompoundAssignment(t *testing.T) {
	tests := []struct {
		input    string
		expected float64
	}{
		{"let x = 10; x += 5; x;", 15},
		{"let x = 10; x -= 3; x;", 7},
		{"let x = 10; x *= 2; x;", 20},
		{"let x = 10; x /= 4; x;", 2.5},
		{"let x = 10; x %= 3; x;", 1},
	}
	for _, tt := range tests {
		result := testEval(t, tt.input)
		assertNumber(t, result, tt.expected)
	}
}

// 数组解构
func TestArrayDestructuring(t *testing.T) {
	input := `
		let [a, b] = [1, 2];
		a + b;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3)
}

// 对象解构
func TestObjectDestructuring(t *testing.T) {
	input := `
		let { x, y } = { x: 10, y: 20 };
		x + y;
	`
	result := testEval(t, input)
	assertNumber(t, result, 30)
}

func TestObjectDestructuringRename(t *testing.T) {
	input := `
		let { x: a, y: b } = { x: 1, y: 2 };
		a + b;
	`
	result := testEval(t, input)
	assertNumber(t, result, 3)
}

// 成员表达式赋值
func TestMemberAssignment(t *testing.T) {
	input := `
		let obj = { x: 0 };
		obj.x = 42;
		obj.x;
	`
	result := testEval(t, input)
	assertNumber(t, result, 42)
}

func TestArrayIndexAssignment(t *testing.T) {
	input := `
		let arr = [1, 2, 3];
		arr[1] = 20;
		arr[1];
	`
	result := testEval(t, input)
	assertNumber(t, result, 20)
}

// 嵌套闭包
func TestNestedClosure(t *testing.T) {
	input := `
		function counter() {
			let count = 0;
			return function() {
				count = count + 1;
				return count;
			};
		}
		let inc = counter();
		inc();
		inc();
	`
	result := testEval(t, input)
	assertNumber(t, result, 2)
}

// ===== REPL 跨输入状态持久化 =====

// TestREPLStatePersistence 验证 REPL 场景: 多条输入共享同一 globals 环境,
// 前一条输入声明的全局变量在后一条输入中可见 (OP_DECLARE/OP_LOAD_GLOBAL 按名字访问)。
func TestREPLStatePersistence(t *testing.T) {
	globals := runtime.NewEnvironment()

	// let 声明跨输入可见
	evalREPLLine(t, globals, `let x = 10;`)
	result := evalREPLLine(t, globals, `x * 2;`)
	assertNumber(t, result, 20)

	// const 声明跨输入可见
	evalREPLLine(t, globals, `const PI = 3.14;`)
	result = evalREPLLine(t, globals, `PI * 2;`)
	assertNumber(t, result, 6.28)

	// 函数声明跨输入可见
	evalREPLLine(t, globals, `function add(a, b) { return a + b; }`)
	result = evalREPLLine(t, globals, `add(3, 4);`)
	assertNumber(t, result, 7)

	// 函数可读取之前声明的全局变量
	evalREPLLine(t, globals, `let base = 100;`)
	evalREPLLine(t, globals, `function f(n) { return n + base; }`)
	result = evalREPLLine(t, globals, `f(7);`)
	assertNumber(t, result, 107)

	// 闭包修改全局计数器
	evalREPLLine(t, globals, `let counter = 0;`)
	evalREPLLine(t, globals, `function inc() { counter = counter + 1; return counter; }`)
	assertNumber(t, evalREPLLine(t, globals, `inc();`), 1)
	assertNumber(t, evalREPLLine(t, globals, `inc();`), 2)
	assertNumber(t, evalREPLLine(t, globals, `inc();`), 3)

	// 复合赋值跨输入
	evalREPLLine(t, globals, `let n = 0;`)
	evalREPLLine(t, globals, `n += 5;`)
	result = evalREPLLine(t, globals, `n;`)
	assertNumber(t, result, 5)
}

// evalREPLLine 模拟 REPL 单行输入: 复用同一 globals 环境执行。
func evalREPLLine(t *testing.T, globals *runtime.Environment, input string) object.Value {
	t.Helper()
	l := lexer.New(input)
	p := parser.New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors:/n%s", p.Errors().String())
	}
	c := compiler.New()
	if err := c.Compile(program); err != nil {
		t.Fatalf("compiler error: %v", err)
	}
	machine := NewWithGlobals(c.Bytes(), c.Constants(), c.NumLocals(), globals)
	if err := machine.Run(); err != nil {
		t.Fatalf("vm error: %v", err)
	}
	return machine.LastPopped()
}

// ===== 模块系统 =====

// TestModuleImportBinding 验证入口脚本通过 EvalFile 相对导入时,
// import 绑定在全局作用域按名声明, 后续引用可解析。
// 回归: 修复前 import 绑定存局部 slot, 但引用走 LOAD_GLOBAL, 导致 ReferenceError。
func TestModuleImportBinding(t *testing.T) {
	dir := t.TempDir()
	modPath := dir + "/math.js"
	mainPath := dir + "/main.js"

	if err := os.WriteFile(modPath, []byte(`
		export const PI = 3.14159;
		export function double(x) { return x * 2; }
		export default function square(x) { return x * x; }
	`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mainPath, []byte(`
		import sq, { PI, double } from "./math.js";
		let out = double(PI);
		out;
	`), 0644); err != nil {
		t.Fatal(err)
	}

	result, err := EvalFile(mainPath)
	if err != nil {
		t.Fatalf("EvalFile error: %v", err)
	}
	// double(PI) = 3.14159 * 2
	assertNumber(t, result, 3.14159*2)
}

// ===== in / instanceof 运算符 =====

// TestInOperator 验证 `key in obj` 运算符 (含数组索引、缺失键、原型链)。
func TestInOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`"x" in { x: 1 };`, true},
		{`"y" in { x: 1 };`, false},
		{`let a = [10, 20]; 0 in a;`, true},
		{`let a = [10, 20]; 5 in a;`, false},
		// in 沿原型链查找: Object.create 的原型对象上的属性也命中
		{`let o = Object.create({ parent: 1 }); "parent" in o;`, true},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := evalWithGlobals(t, tt.input)
			assertBoolean(t, result, tt.expected)
		})
	}
}

// TestInstanceofOperator 验证 instanceof 运算符 (内置类型 + 原型链)。
func TestInstanceofOperator(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`let a = [1,2,3]; a instanceof Array;`, true},
		{`let a = [1,2,3]; a instanceof Object;`, true},
		{`let s = "hi"; s instanceof String;`, true},
		{`let m = new Map(); m instanceof Map;`, true},
		{`let r = /ab/; r instanceof RegExp;`, true},
		{`let p = Promise.resolve(1); p instanceof Promise;`, true},
		{`let a = [1,2,3]; a instanceof RegExp;`, false},
		{`let a = [1,2,3]; a instanceof Number;`, false},
	}
	for _, tt := range tests {
		result := evalWithGlobals(t, tt.input)
		assertBoolean(t, result, tt.expected)
	}
}

// evalWithGlobals 使用完整 stdlib 全局环境执行 (用于需要 Array/Map/RegExp 等全局构造器的测试)。
func evalWithGlobals(t *testing.T, input string) object.Value {
	t.Helper()
	globals := stdlib.SetupGlobals()
	result, err := EvalWithGlobals(input, globals)
	if err != nil {
		t.Fatalf("eval error for %q: %v", input, err)
	}
	return result
}

// TestYieldDelegate 覆盖 yield* 委托: 迭代器跨挂起点存活且返回时栈平衡。
func TestYieldDelegate(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`function* g(){ yield* [1,2]; yield 3; } let a = [...g()]; "" + a[0] + a[1] + a[2]`, "123"},
		{`function* inner(){ yield 1; yield 2; } function* outer(){ yield* inner(); yield 3; } let a = [...outer()]; "" + a[0] + a[1] + a[2]`, "123"},
		{`let out = ""; function* g(){ yield* [1,2]; } for (const x of g()) { out += x; } out`, "12"},
		{`function* g(){ yield* [1,2]; yield 3; } let it = g(); let s = ""; s += it.next().value; s += it.next().value; s += it.next().value; s += it.next().done; s`, "123true"},
	}
	for _, tt := range tests {
		assertString(t, testEval(t, tt.input), tt.expected)
	}
}

// TestDestructureIterable 数组解构必须走迭代协议 (generator/字符串)，
// 非可迭代值抛 TypeError。
func TestDestructureIterable(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{`function* g(){ yield 1; yield 2; } const [a, b] = g(); "" + a + b`, "12"},
		{`const [a, b] = "xy"; "" + a + b`, "xy"},
		{`function* g(){ yield 1; yield 2; } const [a, ...rest] = g(); "" + a + rest.length`, "11"},
	}
	for _, tt := range tests {
		assertString(t, testEval(t, tt.input), tt.expected)
	}
}
