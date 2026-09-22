package parser

import (
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// 解构模式 (含嵌套) 的解析回归。
//
// 2026-09-22 修: 嵌套模式解析完没有越过**内层**闭合符, 内层 '}' ']' 被当成外层
// 的结束, 于是最自然的解构写法直接解析失败:
//
//	const [a, [b]] = x;                    → expected = after destructuring, got RBRACKET
//	const [data, {refetch}] = f();         → unexpected token: RBRACE
//	const {x, y: {z}} = o;                 → expected = after destructuring, got RBRACE
//
// 编译器本就能编译嵌套解构, 缺的只是 parser 这一层。这里按模式文本钉住形状。

// patternOf 解析一条解构声明并返回它的模式。
func patternOf(t *testing.T, src string) ast.Expression {
	t.Helper()
	p := New(lexer.New(src))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	if len(program.Statements) != 1 {
		t.Fatalf("%s: 语句数 = %d, want 1", src, len(program.Statements))
	}
	var value ast.Expression
	switch s := program.Statements[0].(type) {
	case *ast.LetStatement:
		value = s.Value
	case *ast.ConstStatement:
		value = s.Value
	default:
		t.Fatalf("%s: 不是 let/const 声明: %T", src, program.Statements[0])
	}
	assign, ok := value.(*ast.AssignmentExpression)
	if !ok {
		t.Fatalf("%s: 解构声明的值是 %T, want AssignmentExpression", src, value)
	}
	return assign.Left
}

func TestNestedDestructuringPatterns(t *testing.T) {
	cases := map[string]string{
		// 数组里套对象: createResource 那条写法
		"const [data, {refetch}] = f();": "[data, {refetch}]",
		"let [a, {b}] = f();":            "[a, {b}]",
		// 嵌套后面还有元素 / rest —— 曾被"提前结束"吃掉的那种
		"const [a, {b}, c] = f();":       "[a, {b}, c]",
		"const [a, {b}, ...rest] = f();": "[a, {b}, ...rest]",
		// 重命名 / 嵌套默认值
		"const [a, {b: c}] = f();":  "[a, {b: c}]",
		"const [a, {b = 3}] = f();": "[a, {b = 3}]",
		// 数组里套数组
		"const [a, [b, c]] = f();": "[a, [b, c]]",
		"const [[a]] = f();":       "[[a]]",
		// 对象里套对象 / 对象里套数组
		"const {x, y: {z}} = o;":    "{x, y: {z}}",
		"const {x, y: [a, b]} = o;": "{x, y: [a, b]}",
	}
	for src, want := range cases {
		if got := patternOf(t, src).String(); got != want {
			t.Fatalf("%s\n模式 = %s, want %s", src, got, want)
		}
	}
}

// TestDestructuringAssignmentNestedStillWorks 赋值解构 (无声明) 走的是另一条
// 路径 (cover grammar: 字面量目标转模式), 别被上面那处修改带坏。
func TestDestructuringAssignmentNestedStillWorks(t *testing.T) {
	p := New(lexer.New(`let a = 0, b = 0; [a, {b}] = [1, {b: 2}];`))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	if len(program.Statements) != 2 {
		t.Fatalf("语句数 = %d, want 2", len(program.Statements))
	}
	expr, ok := program.Statements[1].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("第二条不是表达式语句: %T", program.Statements[1])
	}
	assign, ok := expr.Expression.(*ast.AssignmentExpression)
	if !ok {
		t.Fatalf("第二条不是赋值: %T", expr.Expression)
	}
	if got := assign.Left.String(); got != "[a, {b}]" {
		t.Fatalf("赋值解构模式 = %s, want [a, {b}]", got)
	}
}
