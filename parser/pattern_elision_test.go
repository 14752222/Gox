package parser

import (
	"testing"

	"github.com/14752222/Gox/ast"
	"github.com/14752222/Gox/lexer"
)

// ===== 数组解构模式里的空洞 (elision) 解析 (rKWmkc) =====
//
// `[, a]` / `[a, , b]` / `[a, , ]` 里的连续逗号占一个元素位但没有绑定目标。
// 解析器把空洞建成 Target == nil 的 PatternElement (由编译器解释为「取一次
// next() 后丢弃」)。同时 `[]` / `{}` 空模式不得提前越过自己的闭合符 —— 否则
// `[[]]` / `for ([] of y)` 会错位 (见 parseArrayPattern 注释)。

// arrayPatternOf 解析一条解构声明, 断言是 ArrayPattern 后返回它。
func arrayPatternOf(t *testing.T, src string) *ast.ArrayPattern {
	t.Helper()
	ap, ok := patternOf(t, src).(*ast.ArrayPattern)
	if !ok {
		t.Fatalf("%s: 模式不是 ArrayPattern: %T", src, patternOf(t, src))
	}
	return ap
}

func TestArrayPatternElision(t *testing.T) {
	// 前导空洞: [, b] → 2 个元素, 第 0 个是洞
	ap := arrayPatternOf(t, "const [, b] = f();")
	if len(ap.Elements) != 2 {
		t.Fatalf("[, b] 元素数 = %d, want 2", len(ap.Elements))
	}
	if ap.Elements[0].Target != nil {
		t.Errorf("[, b] 第 0 个元素应为空洞 (Target==nil), got %s", ap.Elements[0].Target)
	}
	if got := ap.Elements[1].Target.String(); got != "b" {
		t.Errorf("[, b] 第 1 个元素 = %q, want b", got)
	}

	// 中间空洞: [a, , b] → 3 个元素, 第 1 个是洞
	ap = arrayPatternOf(t, "const [a, , b] = f();")
	if len(ap.Elements) != 3 {
		t.Fatalf("[a, , b] 元素数 = %d, want 3", len(ap.Elements))
	}
	if ap.Elements[1].Target != nil {
		t.Errorf("[a, , b] 第 1 个元素应为空洞, got %s", ap.Elements[1].Target)
	}

	// 尾部空洞: [a, , ] → 2 个元素 (尾逗号是分隔符, 不额外占位)
	ap = arrayPatternOf(t, "const [a, , ] = f();")
	if len(ap.Elements) != 2 {
		t.Fatalf("[a, , ] 元素数 = %d, want 2", len(ap.Elements))
	}
	if ap.Elements[1].Target != nil {
		t.Errorf("[a, , ] 第 1 个元素应为空洞, got %s", ap.Elements[1].Target)
	}
}

// TestArrayPatternElisionString: 空洞的 String() 不 panic (Target==nil 保护),
// 形状与源码一致。
func TestArrayPatternElisionString(t *testing.T) {
	cases := map[string]string{
		"const [, b] = f();":    "[, b]",
		"const [a, , b] = f();": "[a, , b]",
		"const [] = f();":       "[]",
	}
	for src, want := range cases {
		if got := patternOf(t, src).String(); got != want {
			t.Errorf("%s\n模式 = %s, want %s", src, got, want)
		}
	}
}

// TestEmptyArrayPatternDoesNotEatCloser: 空模式必须停在**自己**的闭合符上。
// 曾经的「提前 nextToken 返回」快捷键会让 `[[]]` 的内层把外层 ']' 也吃掉。
func TestEmptyArrayPatternDoesNotEatCloser(t *testing.T) {
	// const [[]] = f(); —— 内外两层空数组模式
	ap := arrayPatternOf(t, "const [[]] = f();")
	if len(ap.Elements) != 1 {
		t.Fatalf("[[]] 外层元素数 = %d, want 1", len(ap.Elements))
	}
	inner, ok := ap.Elements[0].Target.(*ast.ArrayPattern)
	if !ok {
		t.Fatalf("[[]] 内层不是 ArrayPattern: %T", ap.Elements[0].Target)
	}
	if len(inner.Elements) != 0 {
		t.Errorf("[[]] 内层元素数 = %d, want 0", len(inner.Elements))
	}

	// const [] = f(); 空模式后仍能正常读到 '='
	empty := arrayPatternOf(t, "const [] = f();")
	if len(empty.Elements) != 0 {
		t.Errorf("[] 元素数 = %d, want 0", len(empty.Elements))
	}
}

// TestNestedArrayPatternElision: 嵌套里的空洞。
func TestNestedArrayPatternElision(t *testing.T) {
	ap := arrayPatternOf(t, "const [[, b]] = f();")
	if len(ap.Elements) != 1 {
		t.Fatalf("外层元素数 = %d, want 1", len(ap.Elements))
	}
	inner, ok := ap.Elements[0].Target.(*ast.ArrayPattern)
	if !ok {
		t.Fatalf("内层不是 ArrayPattern: %T", ap.Elements[0].Target)
	}
	if len(inner.Elements) != 2 || inner.Elements[0].Target != nil || inner.Elements[1].Target.String() != "b" {
		t.Errorf("内层空洞解析错误: %s", inner.String())
	}
}

// TestAssignmentDestructuringElision: 赋值解构 (无声明) 的空洞走 cover grammar
// (字面量 → 模式) 另一条路径, 同样要建出 nil Target 的元素。
func TestAssignmentDestructuringElision(t *testing.T) {
	p := New(lexer.New(`let a = 0; [, a] = [1, 2];`))
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
	ap, ok := assign.Left.(*ast.ArrayPattern)
	if !ok {
		t.Fatalf("赋值左侧不是 ArrayPattern: %T", assign.Left)
	}
	if len(ap.Elements) != 2 {
		t.Fatalf("[, a] 元素数 = %d, want 2", len(ap.Elements))
	}
	if ap.Elements[0].Target != nil {
		t.Errorf("[, a] 第 0 个元素应为空洞, got %s", ap.Elements[0].Target)
	}
}
