package parser

import (
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

func TestLetStatements(t *testing.T) {
	input := `
	let x = 5;
	let y = 10;
	let foobar = 838383;
	`

	l := lexer.New(input)
	p := New(l)

	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(program.Statements))
	}

	for i, stmt := range program.Statements {
		letStmt, ok := stmt.(*ast.LetStatement)
		if !ok {
			t.Fatalf("statement %d: expected *ast.LetStatement, got %T", i, stmt)
		}
		if letStmt.TokenLiteral() != "let" {
			t.Fatalf("statement %d: expected 'let', got %q", i, letStmt.TokenLiteral())
		}
	}
}

func TestConstStatements(t *testing.T) {
	input := `
	const PI = 3.14;
	const msg = "hello";
	`

	l := lexer.New(input)
	p := New(l)

	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 2 {
		t.Fatalf("expected 2 statements, got %d", len(program.Statements))
	}

	for i, stmt := range program.Statements {
		constStmt, ok := stmt.(*ast.ConstStatement)
		if !ok {
			t.Fatalf("statement %d: expected *ast.ConstStatement, got %T", i, stmt)
		}
		if constStmt.TokenLiteral() != "const" {
			t.Fatalf("statement %d: expected 'const', got %q", i, constStmt.TokenLiteral())
		}
	}
}

func TestVarDeclarationRejected(t *testing.T) {
	// var 是**有意**不支持的边界, 不是漏做 (2026-09-24 评估后维持; 理由见
	// parser.go 的 VAR 分支与官网 guide.html 的「为什么不支持 var?」)。
	// 这里除了"拒绝", 还钉住**文案**: 它本身就是给用户的行动指引, 改了会让
	// 排障的人失去唯一线索。声明在语句层被拒绝, 包括 for (var ...) 形式。
	inputs := []string{
		`var x = 5;`,
		`for (var i = 0; i < 3; i++) { }`,
		`for (var v of [1, 2]) { }`,
	}
	for _, input := range inputs {
		l := lexer.New(input)
		p := New(l)
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Fatalf("expected parse error for %q, but got none", input)
		}
		if got := p.Errors().Errors[0].Message; got != "var is not supported, use let or const instead" {
			t.Fatalf("%q: 报错文案变了 (它对用户就是行动指引): %q", input, got)
		}
	}
}

func TestVarAsPropertyName(t *testing.T) {
	// var 作为属性名是合法的 (obj.var, {var: 1}, a?.var)
	input := `{ var: 1 }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("expected no parse error for %q, got %v", input, p.Errors())
	}
	if len(program.Statements) == 0 {
		t.Fatal("expected one statement")
	}
}

func TestReturnStatements(t *testing.T) {
	input := `
	function foo() {
		return 5;
		return 10;
		return 993322;
	}
	`

	l := lexer.New(input)
	p := New(l)

	program := p.ParseProgram()
	checkParserErrors(t, p)

	fnDecl, ok := program.Statements[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", program.Statements[0])
	}

	if len(fnDecl.Body.Statements) != 3 {
		t.Fatalf("expected 3 return statements, got %d", len(fnDecl.Body.Statements))
	}

	for i, stmt := range fnDecl.Body.Statements {
		retStmt, ok := stmt.(*ast.ReturnStatement)
		if !ok {
			t.Fatalf("statement %d: expected *ast.ReturnStatement, got %T", i, stmt)
		}
		if retStmt.TokenLiteral() != "return" {
			t.Fatalf("statement %d: expected 'return', got %q", i, retStmt.TokenLiteral())
		}
	}
}

func TestIdentifierExpression(t *testing.T) {
	input := `foobar;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	stmt, ok := program.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("expected ExpressionStatement, got %T", program.Statements[0])
	}

	ident, ok := stmt.Expression.(*ast.Identifier)
	if !ok {
		t.Fatalf("expected Identifier, got %T", stmt.Expression)
	}
	if ident.Value != "foobar" {
		t.Fatalf("expected 'foobar', got %q", ident.Value)
	}
}

func TestIntegerLiteralExpression(t *testing.T) {
	input := `5;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	stmt, ok := program.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("expected ExpressionStatement, got %T", program.Statements[0])
	}

	lit, ok := stmt.Expression.(*ast.IntegerLiteral)
	if !ok {
		t.Fatalf("expected IntegerLiteral, got %T", stmt.Expression)
	}
	if lit.Value != 5 {
		t.Fatalf("expected 5, got %d", lit.Value)
	}
}

func TestBooleanLiteralExpression(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"true;", true},
		{"false;", false},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		lit, ok := stmt.Expression.(*ast.BooleanLiteral)
		if !ok {
			t.Fatalf("expected BooleanLiteral, got %T", stmt.Expression)
		}
		if lit.Value != tt.expected {
			t.Fatalf("expected %v, got %v", tt.expected, lit.Value)
		}
	}
}

func TestStringLiteralExpression(t *testing.T) {
	input := `"hello world";`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	lit, ok := stmt.Expression.(*ast.StringLiteral)
	if !ok {
		t.Fatalf("expected StringLiteral, got %T", stmt.Expression)
	}
	if lit.Value != "hello world" {
		t.Fatalf("expected 'hello world', got %q", lit.Value)
	}
}

func TestTemplateLiteral(t *testing.T) {
	input := "`Hello ${name}!`"

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	tl, ok := stmt.Expression.(*ast.TemplateLiteral)
	if !ok {
		t.Fatalf("expected TemplateLiteral, got %T", stmt.Expression)
	}

	if len(tl.Quasis) != 2 {
		t.Fatalf("expected 2 quasis, got %d", len(tl.Quasis))
	}
	if tl.Quasis[0] != "Hello " {
		t.Fatalf("expected 'Hello ', got %q", tl.Quasis[0])
	}
	if tl.Quasis[1] != "!" {
		t.Fatalf("expected '!', got %q", tl.Quasis[1])
	}

	if len(tl.Expressions) != 1 {
		t.Fatalf("expected 1 expression, got %d", len(tl.Expressions))
	}
	ident, ok := tl.Expressions[0].(*ast.Identifier)
	if !ok {
		t.Fatalf("expected Identifier, got %T", tl.Expressions[0])
	}
	if ident.Value != "name" {
		t.Fatalf("expected 'name', got %q", ident.Value)
	}
}

func TestArrowFunction(t *testing.T) {
	input := `let f = (a, b) => a + b;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	af, ok := letStmt.Value.(*ast.ArrowFunctionExpression)
	if !ok {
		t.Fatalf("expected ArrowFunctionExpression, got %T", letStmt.Value)
	}

	if len(af.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(af.Parameters))
	}
	if af.Parameters[0].Name != "a" {
		t.Fatalf("expected 'a', got %q", af.Parameters[0].Name)
	}
	if af.Parameters[1].Name != "b" {
		t.Fatalf("expected 'b', got %q", af.Parameters[1].Name)
	}

	// Body should be an expression (implicit return)
	binExpr, ok := af.Body.(*ast.BinaryExpression)
	if !ok {
		t.Fatalf("expected BinaryExpression body, got %T", af.Body)
	}
	if binExpr.Operator != "+" {
		t.Fatalf("expected '+', got %q", binExpr.Operator)
	}
}

func TestArrowFunctionWithBlockBody(t *testing.T) {
	input := `let f = (x) => { return x * 2; };`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	af, ok := letStmt.Value.(*ast.ArrowFunctionExpression)
	if !ok {
		t.Fatalf("expected ArrowFunctionExpression, got %T", letStmt.Value)
	}

	// Body should be a BlockStatement
	block, ok := af.Body.(*ast.BlockStatement)
	if !ok {
		t.Fatalf("expected BlockStatement body, got %T", af.Body)
	}
	if len(block.Statements) != 1 {
		t.Fatalf("expected 1 statement in block, got %d", len(block.Statements))
	}
}

func TestArrowFunctionDefaultParams(t *testing.T) {
	input := `let f = (a, b = 5) => a + b;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	af := letStmt.Value.(*ast.ArrowFunctionExpression)

	if len(af.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(af.Parameters))
	}
	if af.Parameters[1].Default == nil {
		t.Fatal("expected default value for parameter b")
	}
}

func TestArrayLiteral(t *testing.T) {
	input := `[1, 2, 3];`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	arr, ok := stmt.Expression.(*ast.ArrayLiteral)
	if !ok {
		t.Fatalf("expected ArrayLiteral, got %T", stmt.Expression)
	}
	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}
}

func TestArrayWithSpread(t *testing.T) {
	input := `[1, ...rest, 3];`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	arr := stmt.Expression.(*ast.ArrayLiteral)

	if len(arr.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(arr.Elements))
	}

	spread, ok := arr.Elements[1].(*ast.SpreadElement)
	if !ok {
		t.Fatalf("expected SpreadElement at index 1, got %T", arr.Elements[1])
	}
	ident, ok := spread.Argument.(*ast.Identifier)
	if !ok {
		t.Fatalf("expected Identifier in spread, got %T", spread.Argument)
	}
	if ident.Value != "rest" {
		t.Fatalf("expected 'rest', got %q", ident.Value)
	}
}

func TestObjectLiteral(t *testing.T) {
	input := `{ name: "Alice", age: 30 };`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	obj, ok := stmt.Expression.(*ast.ObjectLiteral)
	if !ok {
		t.Fatalf("expected ObjectLiteral, got %T", stmt.Expression)
	}
	if len(obj.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(obj.Properties))
	}
}

func TestObjectShorthand(t *testing.T) {
	input := `let name = "Bob"; let obj = { name };`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[1].(*ast.LetStatement)
	obj := letStmt.Value.(*ast.ObjectLiteral)

	if len(obj.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(obj.Properties))
	}
	if !obj.Properties[0].Shorthand {
		t.Fatal("expected shorthand property")
	}
}

func TestObjectComputedProperty(t *testing.T) {
	input := `let key = "foo"; let obj = { [key]: 42 };`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[1].(*ast.LetStatement)
	obj := letStmt.Value.(*ast.ObjectLiteral)

	if len(obj.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(obj.Properties))
	}
	if !obj.Properties[0].Computed {
		t.Fatal("expected computed property")
	}
}

func TestObjectMethod(t *testing.T) {
	input := `let obj = { greet() { return "hello"; } };`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	obj := letStmt.Value.(*ast.ObjectLiteral)

	if len(obj.Properties) != 1 {
		t.Fatalf("expected 1 property, got %d", len(obj.Properties))
	}
	if obj.Properties[0].Kind != ast.PROP_METHOD {
		t.Fatal("expected method property")
	}
	if _, ok := obj.Properties[0].Value.(*ast.FunctionExpression); !ok {
		t.Fatalf("expected FunctionExpression as method value, got %T", obj.Properties[0].Value)
	}
}

func TestBinaryExpression(t *testing.T) {
	tests := []struct {
		input    string
		operator string
	}{
		{"1 + 2;", "+"},
		{"1 - 2;", "-"},
		{"1 * 2;", "*"},
		{"1 / 2;", "/"},
		{"1 % 2;", "%"},
		{"1 ** 2;", "**"},
		{"1 == 2;", "=="},
		{"1 === 2;", "==="},
		{"1 != 2;", "!="},
		{"1 !== 2;", "!=="},
		{"1 < 2;", "<"},
		{"1 > 2;", ">"},
		{"1 <= 2;", "<="},
		{"1 >= 2;", ">="},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		expr, ok := stmt.Expression.(*ast.BinaryExpression)
		if !ok {
			t.Fatalf("input %q: expected BinaryExpression, got %T", tt.input, stmt.Expression)
		}
		if expr.Operator != tt.operator {
			t.Fatalf("input %q: expected %q, got %q", tt.input, tt.operator, expr.Operator)
		}
	}
}

func TestOperatorPrecedence(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			"1 + 2 * 3;",
			"(1 + (2 * 3))",
		},
		{
			"(1 + 2) * 3;",
			"((1 + 2) * 3)",
		},
		{
			"1 + 2 + 3;",
			"((1 + 2) + 3)",
		},
		{
			"a == b == c;",
			"((a == b) == c)",
		},
		{
			"2 ** 3 ** 2;",
			"(2 ** (3 ** 2))",
		},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		actual := stmt.Expression.String()
		if actual != tt.expected {
			t.Fatalf("input %q: expected %q, got %q", tt.input, tt.expected, actual)
		}
	}
}

func TestCallExpression(t *testing.T) {
	input := `foo(1, 2, 3);`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	call, ok := stmt.Expression.(*ast.CallExpression)
	if !ok {
		t.Fatalf("expected CallExpression, got %T", stmt.Expression)
	}
	if len(call.Arguments) != 3 {
		t.Fatalf("expected 3 arguments, got %d", len(call.Arguments))
	}
}

func TestMemberExpression(t *testing.T) {
	tests := []struct {
		input    string
		computed bool
	}{
		{"foo.bar;", false},
		{"foo[0];", true},
		{"foo[bar];", true},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		member, ok := stmt.Expression.(*ast.MemberExpression)
		if !ok {
			t.Fatalf("input %q: expected MemberExpression, got %T", tt.input, stmt.Expression)
		}
		if member.Computed != tt.computed {
			t.Fatalf("input %q: expected computed=%v, got %v", tt.input, tt.computed, member.Computed)
		}
	}
}

func TestConditionalExpression(t *testing.T) {
	input := `x > 0 ? "pos" : "neg";`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt := program.Statements[0].(*ast.ExpressionStatement)
	if _, ok := stmt.Expression.(*ast.ConditionalExpression); !ok {
		t.Fatalf("expected ConditionalExpression, got %T", stmt.Expression)
	}
}

// parseFirstExpr 解析以 `;` 结尾的单表达式源码，返回表达式节点。
func parseFirstExpr(t *testing.T, input string) ast.Expression {
	t.Helper()
	p := New(lexer.New(input))
	program := p.ParseProgram()
	checkParserErrors(t, p)
	stmt, ok := program.Statements[0].(*ast.ExpressionStatement)
	if !ok {
		t.Fatalf("expected ExpressionStatement, got %T", program.Statements[0])
	}
	return stmt.Expression
}

// condOf 断言节点是条件表达式并返回，否则测试失败。
func condOf(t *testing.T, e ast.Expression, ctx string) *ast.ConditionalExpression {
	t.Helper()
	c, ok := e.(*ast.ConditionalExpression)
	if !ok {
		t.Fatalf("%s: expected ConditionalExpression, got %T", ctx, e)
	}
	return c
}

// TestConditionalExpressionAssociativity 锁定三元的右结合性。
//
// 回归背景: 原先分支用 parseExpression(TERNARY) 解析, 中缀循环条件
// `precedence < peekPrecedence()` 使得 `TERNARY < TERNARY` 为假, 后续的 `?`
// 不被分支消费而被外层循环捡走, 于是 `a ? b : c ? d : e` 错解析为
// `(a ? b : c) ? d : e`。表面看是"渲染错分支", 实为语法树结构错误。
func TestConditionalExpressionAssociativity(t *testing.T) {
	// 嵌套三元在 alternative 位置 -> 必须挂在 Alternative 下 (右结合)
	t.Run("alternative位置", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `a ? b : c ? d : e;`), "outer")
		if id, ok := outer.Consequence.(*ast.Identifier); !ok || id.Value != "b" {
			t.Fatalf("Consequence 应为 b, got %T", outer.Consequence)
		}
		inner := condOf(t, outer.Alternative, "Alternative")
		if id := inner.Condition.(*ast.Identifier); id.Value != "c" {
			t.Fatalf("内层 Condition 应为 c, got %s", id.Value)
		}
		if id := inner.Consequence.(*ast.Identifier); id.Value != "d" {
			t.Fatalf("内层 Consequence 应为 d, got %s", id.Value)
		}
		if id := inner.Alternative.(*ast.Identifier); id.Value != "e" {
			t.Fatalf("内层 Alternative 应为 e, got %s", id.Value)
		}
	})

	// 嵌套三元在 consequence 位置 -> 挂在 Consequence 下
	t.Run("consequence位置", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `a ? b ? c : d : e;`), "outer")
		inner := condOf(t, outer.Consequence, "Consequence")
		if id := inner.Condition.(*ast.Identifier); id.Value != "b" {
			t.Fatalf("内层 Condition 应为 b, got %s", id.Value)
		}
		if id, ok := outer.Alternative.(*ast.Identifier); !ok || id.Value != "e" {
			t.Fatalf("外层 Alternative 应为 e, got %T", outer.Alternative)
		}
	})

	// 括号显式改变结合方向
	t.Run("括号覆盖", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `(a ? b : c) ? d : e;`), "outer")
		condOf(t, outer.Condition, "Condition")
	})

	// 条件位置先绑 && / || (条件按 ShortCircuitExpression 解析)
	t.Run("条件吸收逻辑运算", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `a || b ? c : d;`), "outer")
		if _, ok := outer.Condition.(*ast.LogicalExpression); !ok {
			t.Fatalf("Condition 应为 LogicalExpression, got %T", outer.Condition)
		}
	})

	// 逗号优先级低于三元: 逗号把三元当左操作数, 不能被分支吞掉
	t.Run("不吞逗号", func(t *testing.T) {
		seq, ok := parseFirstExpr(t, `a ? b : c, d;`).(*ast.SequenceExpression)
		if !ok {
			t.Fatalf("expected SequenceExpression, got %T", parseFirstExpr(t, `a ? b : c, d;`))
		}
		condOf(t, seq.Expressions[0], "第一个元素")
	})

	// 赋值优先级低于三元: x = (a ? b : c)
	t.Run("赋值右侧", func(t *testing.T) {
		asg := parseFirstExpr(t, `x = a ? b : c;`).(*ast.AssignmentExpression)
		condOf(t, asg.Right, "Right")
	})

	// 分支按 AssignmentExpression 解析, 可吞赋值: a ? b : (c = d)
	t.Run("分支吸收赋值", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `a ? b : c = d;`), "outer")
		if _, ok := outer.Alternative.(*ast.AssignmentExpression); !ok {
			t.Fatalf("Alternative 应为 AssignmentExpression, got %T", outer.Alternative)
		}
	})

	// consequence 位置同理: a ? (b = c) : d
	t.Run("consequence吸收赋值", func(t *testing.T) {
		outer := condOf(t, parseFirstExpr(t, `a ? b = c : d;`), "outer")
		if _, ok := outer.Consequence.(*ast.AssignmentExpression); !ok {
			t.Fatalf("Consequence 应为 AssignmentExpression, got %T", outer.Consequence)
		}
	})
}

func TestIfStatement(t *testing.T) {
	input := `if (x > 0) { return 1; } else { return 2; }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	stmt, ok := program.Statements[0].(*ast.IfStatement)
	if !ok {
		t.Fatalf("expected IfStatement, got %T", program.Statements[0])
	}
	if stmt.Consequence == nil {
		t.Fatal("expected consequence block")
	}
	if stmt.Alternative == nil {
		t.Fatal("expected alternative block")
	}
}

func TestForOfStatement(t *testing.T) {
	input := `for (let x of arr) { console.log(x); }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	forOf, ok := program.Statements[0].(*ast.ForOfStatement)
	if !ok {
		t.Fatalf("expected ForOfStatement, got %T", program.Statements[0])
	}
	if forOf.Variable == nil || forOf.Variable.Value != "x" {
		t.Fatalf("expected variable 'x'")
	}
}

func TestTraditionalForStatement(t *testing.T) {
	input := `for (let i = 0; i < 3; i++) { sum = sum + i; }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	if len(program.Statements) != 1 {
		t.Fatalf("expected 1 statement, got %d", len(program.Statements))
	}

	fs, ok := program.Statements[0].(*ast.ForStatement)
	if !ok {
		t.Fatalf("expected ForStatement, got %T", program.Statements[0])
	}

	// init: let i = 0
	letStmt, ok := fs.Init.(*ast.LetStatement)
	if !ok {
		t.Fatalf("expected LetStatement init, got %T", fs.Init)
	}
	if letStmt.Name.Value != "i" {
		t.Fatalf("expected init variable 'i', got %q", letStmt.Name.Value)
	}

	// condition: i < 3
	if fs.Condition == nil {
		t.Fatal("expected condition")
	}
	if fs.Condition.String() != "(i < 3)" {
		t.Fatalf("expected condition '(i < 3)', got %q", fs.Condition.String())
	}

	// update: i++
	if fs.Update == nil {
		t.Fatal("expected update")
	}
	if fs.Update.String() != "(i++);" {
		t.Fatalf("expected update '(i++);', got %q", fs.Update.String())
	}

	if fs.Body == nil || len(fs.Body.Statements) != 1 {
		t.Fatalf("expected 1 body statement, got %d", len(fs.Body.Statements))
	}
}

func TestTraditionalForVariants(t *testing.T) {
	tests := []struct {
		input     string
		hasCond   bool
		hasUpdate bool
	}{
		{`for (let i = 0; i < 3; i++) {}`, true, true},
		{`for (let i = 0; ; i++) {}`, false, true},
		{`for (let i = 0; i < 3; ) {}`, true, false},
		{`for (;;) {}`, false, false},
		{`for (i = 0; i < 3; i++) {}`, true, true},
		{`for (const x = 0; x < 2; x = x + 1) {}`, true, true},
	}
	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		if len(program.Statements) != 1 {
			t.Fatalf("%q: expected 1 statement, got %d", tt.input, len(program.Statements))
		}
		fs, ok := program.Statements[0].(*ast.ForStatement)
		if !ok {
			t.Fatalf("%q: expected ForStatement, got %T", tt.input, program.Statements[0])
		}
		if (fs.Condition != nil) != tt.hasCond {
			t.Fatalf("%q: condition presence mismatch, got %v", tt.input, fs.Condition != nil)
		}
		if (fs.Update != nil) != tt.hasUpdate {
			t.Fatalf("%q: update presence mismatch, got %v", tt.input, fs.Update != nil)
		}
		if fs.Body == nil {
			t.Fatalf("%q: expected body", tt.input)
		}
	}
}

func TestWhileStatement(t *testing.T) {
	input := `while (x < 10) { x = x + 1; }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	_, ok := program.Statements[0].(*ast.WhileStatement)
	if !ok {
		t.Fatalf("expected WhileStatement, got %T", program.Statements[0])
	}
}

func TestFunctionDeclaration(t *testing.T) {
	input := `function add(a, b) { return a + b; }`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	fn, ok := program.Statements[0].(*ast.FunctionDeclaration)
	if !ok {
		t.Fatalf("expected FunctionDeclaration, got %T", program.Statements[0])
	}
	if fn.Name.Value != "add" {
		t.Fatalf("expected 'add', got %q", fn.Name.Value)
	}
	if len(fn.Parameters) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(fn.Parameters))
	}
}

func TestDestructuringArray(t *testing.T) {
	input := `let [a, b, ...rest] = arr;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	// Value should be an AssignmentExpression with ArrayPattern on the left
	assign, ok := letStmt.Value.(*ast.AssignmentExpression)
	if !ok {
		t.Fatalf("expected AssignmentExpression, got %T", letStmt.Value)
	}
	pattern, ok := assign.Left.(*ast.ArrayPattern)
	if !ok {
		t.Fatalf("expected ArrayPattern, got %T", assign.Left)
	}
	if len(pattern.Elements) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(pattern.Elements))
	}
	if !pattern.Elements[2].Rest {
		t.Fatal("expected 3rd element to be rest")
	}
}

func TestDestructuringObject(t *testing.T) {
	input := `let {a, b: c} = obj;`

	l := lexer.New(input)
	p := New(l)
	program := p.ParseProgram()
	checkParserErrors(t, p)

	letStmt := program.Statements[0].(*ast.LetStatement)
	assign := letStmt.Value.(*ast.AssignmentExpression)
	pattern, ok := assign.Left.(*ast.ObjectPattern)
	if !ok {
		t.Fatalf("expected ObjectPattern, got %T", assign.Left)
	}
	if len(pattern.Properties) != 2 {
		t.Fatalf("expected 2 properties, got %d", len(pattern.Properties))
	}
	if !pattern.Properties[0].Shorthand {
		t.Fatal("expected 1st property to be shorthand")
	}
}

func TestAssignmentExpression(t *testing.T) {
	tests := []struct {
		input    string
		operator string
	}{
		{"x = 5;", "="},
		{"x += 5;", "+="},
		{"x -= 5;", "-="},
		{"x *= 5;", "*="},
		{"x /= 5;", "/="},
		{"x %= 5;", "%="},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		assign, ok := stmt.Expression.(*ast.AssignmentExpression)
		if !ok {
			t.Fatalf("input %q: expected AssignmentExpression, got %T", tt.input, stmt.Expression)
		}
		if assign.Operator != tt.operator {
			t.Fatalf("input %q: expected %q, got %q", tt.input, tt.operator, assign.Operator)
		}
	}
}

func TestLogicalExpression(t *testing.T) {
	tests := []struct {
		input    string
		operator string
	}{
		{"a && b;", "&&"},
		{"a || b;", "||"},
	}

	for _, tt := range tests {
		l := lexer.New(tt.input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		expr, ok := stmt.Expression.(*ast.LogicalExpression)
		if !ok {
			t.Fatalf("input %q: expected LogicalExpression, got %T", tt.input, stmt.Expression)
		}
		if expr.Operator != tt.operator {
			t.Fatalf("input %q: expected %q, got %q", tt.input, tt.operator, expr.Operator)
		}
	}
}

func TestUnaryExpression(t *testing.T) {
	tests := []string{
		"!x;",
		"-x;",
		"+x;",
		"typeof x;",
		"++x;",
		"--x;",
		"x++;",
		"x--;",
	}

	for _, input := range tests {
		l := lexer.New(input)
		p := New(l)
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt := program.Statements[0].(*ast.ExpressionStatement)
		_, ok := stmt.Expression.(*ast.UnaryExpression)
		if !ok {
			t.Fatalf("input %q: expected UnaryExpression, got %T", input, stmt.Expression)
		}
	}
}

// 尾逗号: 实参列表与形参列表。
//
// 2026-09-24 修: 数组/对象字面量与解构**一直**收尾逗号 (那几个循环用
// `for !curTokenIs(闭括号)` 收尾), 只有实参/形参列表不收 —— 同一个文件里两种
// 口径。ES 里尾逗号与不收时语义逐字节相同, 所以该收; 而**不收**时的报错还特别
// 有误导性, 报错点落在逗号之后:
//
//	f(a, b,)              → no prefix parse function for RPAREN
//	function (a, b,) {}   → expected parameter name, got RPAREN
//
// 看着像"参数写漏了"。
//
// 这里同时钉住两侧: "该收的收" 与 "非法写法仍然报错"。放宽实现只是把一处
// `p.nextToken()` 换成 `break`, 顺手把 f(,) / f(a,,b) 也放过去太容易了。
func TestTrailingCommaInArgumentAndParameterLists(t *testing.T) {
	// 收了尾逗号, 但形状必须与不收时逐字一致 —— 实参个数不能变成 3
	for _, tc := range []struct {
		src  string
		want int
	}{
		{`f(a, b,);`, 2},
		{`f(a,);`, 1},
		{`f(a, b, c,);`, 3},
		{`f();`, 0},
		{`f(...rest,);`, 1},
		{`new F(a, b,);`, 2},
	} {
		p := New(lexer.New(tc.src))
		program := p.ParseProgram()
		checkParserErrors(t, p)

		stmt, ok := program.Statements[0].(*ast.ExpressionStatement)
		if !ok {
			t.Fatalf("%s: 不是表达式语句: %T", tc.src, program.Statements[0])
		}
		var args []ast.Expression
		switch e := stmt.Expression.(type) {
		case *ast.CallExpression:
			args = e.Arguments
		case *ast.NewExpression:
			args = e.Arguments
		default:
			t.Fatalf("%s: 期望调用/new 表达式, got %T", tc.src, stmt.Expression)
		}
		if len(args) != tc.want {
			t.Fatalf("%s: 实参个数 = %d, want %d", tc.src, len(args), tc.want)
		}
	}

	// 形参列表: 声明式 / 函数表达式 / 箭头 / 方法简写 四处都要能过
	for _, src := range []string{
		`function g(a, b,) { return a; }`,
		`const h = function (a, b,) { return a; };`,
		`const k = (a, b,) => a;`,
		`const o = { m(a, b,) { return a; } };`,
		`function r(a, ...rest,) { return a; }`,
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		checkParserErrors(t, p)
	}

	// 放宽的那一处不能把非法写法也放过去
	for _, src := range []string{
		`f(,);`,
		`f(a,, b);`,
		`f(a,,);`,
		`function g(,) {}`,
		`function g(a, , b) {}`,
		`const k = (, ) => 1;`,
	} {
		p := New(lexer.New(src))
		p.ParseProgram()
		if !p.Errors().HasErrors() {
			t.Fatalf("%s: 应当报错但通过了", src)
		}
	}
}

// checkParserErrors 检查解析器是否有错误，如果有则失败测试。
func checkParserErrors(t *testing.T, p *Parser) {
	t.Helper()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors: %s", p.Errors().String())
	}
}
