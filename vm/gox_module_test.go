package vm

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 聚合模块 "gox" 与保留命名空间 =====

// TestGoxUmbrellaExportsSubmoduleAPIs 聚合模块 "gox" 能拿到细分模块的
// 导出 (gx/solid 在 stdlib 内注册, 无 gfx 链接也可验证)。
// 注意用 Eval (完整管线, 安装标准库) 而不是 testEval (裸环境无 stdlib)。
func TestGoxUmbrellaExportsSubmoduleAPIs(t *testing.T) {
	val, err := Eval(`
		import { createSignal, createEffect } from "gox";
		const [n, setN] = createSignal(7);
		setN(n() + 5);
		n();
	`)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	num, ok := val.(*object.Number)
	if !ok {
		t.Fatalf("expected Number, got %T (%s)", val, val.Inspect())
	}
	if num.Value != 12 {
		t.Fatalf("n() = %v, want 12", num.Value)
	}
}

// TestGxNamespaceUnknownModuleReportsAvailable gx/ 前缀是保留的内置模块
// 命名空间: 拼写错误直接报 "unknown builtin module" 并列出可用模块,
// 不再落回文件系统解析变成含糊的 Cannot find module。
func TestGxNamespaceUnknownModuleReportsAvailable(t *testing.T) {
	for _, spec := range []string{`gx/dialg`, `gx/nonexistent`} {
		src := `import { x } from "` + spec + `";`
		_, err := Eval(src)
		if err == nil {
			t.Fatalf("import %q 应当失败 (未注册的内置模块)", spec)
		}
		if !strings.Contains(err.Error(), "unknown builtin module") {
			t.Fatalf("import %q 错误应指明是未知的内置模块, got: %v", spec, err)
		}
		if !strings.Contains(err.Error(), "gx/solid") {
			t.Fatalf("import %q 错误应列出可用模块 (至少含 gx/solid), got: %v", spec, err)
		}
	}
}
