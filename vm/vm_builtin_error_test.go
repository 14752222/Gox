package vm

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 内建函数错误抛出语义测试 =====
// 修复前: builtin 返回 *object.Error 只会被当作普通值压栈，
// JSON.parse("{") 不抛异常、new Error("x") 反而会抛异常。

// TestBuiltinErrorThrows: 内建函数返回的 Error 应作为异常抛出
func TestBuiltinErrorThrows(t *testing.T) {
	_, err := EvalVM(`JSON.parse("{invalid")`)
	if err == nil {
		t.Fatalf("expected JSON.parse error to be thrown")
	}
	if !strings.Contains(err.Error(), "SyntaxError") {
		t.Fatalf("expected SyntaxError, got %v", err)
	}
}

// TestBuiltinMethodErrorThrows: 内建方法返回的 Error 应作为异常抛出
func TestBuiltinMethodErrorThrows(t *testing.T) {
	_, err := EvalVM(`[1,2,3].map("not a function")`)
	if err == nil {
		t.Fatalf("expected map TypeError to be thrown")
	}
	if !strings.Contains(err.Error(), "TypeError") {
		t.Fatalf("expected TypeError, got %v", err)
	}
}

// TestErrorConstructorReturnsValue: Error 构造器返回的 Error 是普通值
func TestErrorConstructorReturnsValue(t *testing.T) {
	for _, src := range []string{
		`const e = new Error("boom"); e.message`,
		`const e = Error("boom"); e.message`,
		`const e = new TypeError("t"); e.name`,
		`const e = SyntaxError("s"); e.name`,
	} {
		_, result := runEvalVM(t, src)
		if result == nil {
			t.Fatalf("expected value for %q, got nil", src)
		}
	}
	// new Error 应返回 Error 对象本身 (而不是抛出)
	_, result := runEvalVM(t, `const e = new Error("x"); e instanceof Error`)
	b, ok := result.(*object.Boolean)
	if !ok || !b.Value {
		t.Fatalf("expected new Error() instanceof Error to be true, got %v", result)
	}
}

// TestCaughtBuiltinErrorPreservesName: try/catch 捕获后 name/message 完整
func TestCaughtBuiltinErrorPreservesName(t *testing.T) {
	_, result := runEvalVM(t, `
		let name = "", msg = "";
		try {
			setTimeout(42);
		} catch (e) {
			name = e.name;
			msg = e.message;
		}
		name + "|" + msg
	`)
	s, ok := result.(*object.String)
	if !ok {
		t.Fatalf("expected String result, got %T", result)
	}
	if s.Value != "TypeError|setTimeout: first argument must be a function" {
		t.Fatalf("unexpected caught error info: %q", s.Value)
	}
}
