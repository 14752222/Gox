package compiler

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// compileSrcErr 解析+编译源码, 返回编译错误 (若无错则 nil)。
func compileSrcErr(t *testing.T, src string) error {
	t.Helper()
	p := parser.New(lexer.New(src))
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("unexpected parser error for %q: %s", src, p.Errors().String())
	}
	c := New()
	return c.Compile(program)
}

// TestDuplicateLabelIsSyntaxError 同一语句链上重复标签 ⇒ 编译期 SyntaxError
// (ContainsDuplicateLabels); 消息须含 "SyntaxError" 以便 test262 negative:parse
// 判定 (module-code/early-dup-lables.js)。
func TestDuplicateLabelIsSyntaxError(t *testing.T) {
	for _, src := range []string{
		"label: { label: 0; }",
		"a: { b: { a: 0; } }",
		"a: while (1) { a: while (1) { break; } }",
	} {
		err := compileSrcErr(t, src)
		if err == nil {
			t.Errorf("重复标签应报 SyntaxError: %q", src)
			continue
		}
		if !strings.Contains(err.Error(), "SyntaxError") {
			t.Errorf("错误消息应含 SyntaxError: %q -> %v", src, err)
		}
	}
}

// TestDistinctLabelsAllowed 不同标签 (哪怕嵌套) 合法。
func TestDistinctLabelsAllowed(t *testing.T) {
	for _, src := range []string{
		"a: { }",
		"a: while (1) { b: while (1) { break a; } }",
		"a: for (;;) { break a; }",
	} {
		if err := compileSrcErr(t, src); err != nil {
			t.Errorf("不同标签应通过: %q -> %v", src, err)
		}
	}
}

// TestUpdateExpressionNonTargetIsSyntaxError `1++` 是语法错误 (InvalidAssignment
// TargetType), 消息须含 SyntaxError (module-code/parse-err-syntax-2.js)。
func TestUpdateExpressionNonTargetIsSyntaxError(t *testing.T) {
	err := compileSrcErr(t, "1++;")
	if err == nil {
		t.Fatal("`1++` 应报 SyntaxError")
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("错误消息应含 SyntaxError: %v", err)
	}
}
