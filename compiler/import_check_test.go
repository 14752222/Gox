package compiler

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/parser"
)

// ===== 内置模块命名导入的编译期校验 =====
//
// 这里注册两个只属于本包测试的假内置模块 —— 跨模块提示 ("它在 gx/xxx") 与
// 拼写建议都依赖"注册表里有别的模块", 用真模块会把断言绑死在 stdlib/gfx 上。

func init() {
	object.RegisterBuiltinModule("gx/unit-calc", func() map[string]object.Value {
		return map[string]object.Value{"add": object.NewBuiltin("add", nil)}
	})
	object.RegisterBuiltinModule("gx/unit-other", func() map[string]object.Value {
		return map[string]object.Value{"helper": object.NewBuiltin("helper", nil)}
	})
}

// compileErr 编译并返回错误。compile() 是 t.Fatalf 版本, 错误路径只能用这个。
func compileErr(t *testing.T, input string) error {
	t.Helper()
	p := parser.New(lexer.New(input))
	program := p.ParseProgram()
	if p.Errors().HasErrors() {
		t.Fatalf("parser errors: %s", p.Errors().String())
	}
	c := New()
	return c.Compile(program)
}

// TestBuiltinImportMissingNameFailsLoudly 名字不在导出表里 → 编译期报错,
// 且报错要带上"这个模块有哪些导出" (否则用户只能靠猜)。
func TestBuiltinImportMissingNameFailsLoudly(t *testing.T) {
	err := compileErr(t, `import { nope } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal(`import 一个不存在的导出应当编译失败 (运行时只会静默拿到 undefined)`)
	}
	msg := err.Error()
	if !strings.Contains(msg, `"nope"`) || !strings.Contains(msg, "没有导出") {
		t.Fatalf("报错没说清是哪个名字不存在: %v", err)
	}
	if !strings.Contains(msg, "add") {
		t.Fatalf("报错该列出可用导出 (至少含 add): %v", err)
	}
}

// TestBuiltinImportSuggestsNearName 拼错时给出最接近的导出名。
func TestBuiltinImportSuggestsNearName(t *testing.T) {
	err := compileErr(t, `import { addd } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("拼错的导出名应当编译失败")
	}
	if !strings.Contains(err.Error(), `"add"`) {
		t.Fatalf("应当提示最接近的导出名 add: %v", err)
	}
}

// TestBuiltinImportPointsToOwningModule 名字存在于**别的**内置模块 → 直说在哪。
// 典型真实案例: import { alert } from "gx/gfx" (alert 在 gx/dialog)。
func TestBuiltinImportPointsToOwningModule(t *testing.T) {
	err := compileErr(t, `import { helper } from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("导出名不属于该模块时应当编译失败")
	}
	if !strings.Contains(err.Error(), "它在 gx/unit-other") {
		t.Fatalf("应当指出导出真正所在的模块: %v", err)
	}
}

// TestBuiltinImportHintsElementDirectives 元素级指令 (each / show) 不是导出,
// 被 import 时要点明"写成 JSX 属性"。
func TestBuiltinImportHintsElementDirectives(t *testing.T) {
	for name, want := range map[string]string{
		"each": "元素级指令",
		"show": "元素级指令",
		"For":  "each",
		"Show": "show",
	} {
		err := compileErr(t, `import { `+name+` } from "gx/unit-calc";`)
		if err == nil {
			t.Fatalf("import { %s } 应当编译失败", name)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("import { %s } 的报错应含 %q: %v", name, want, err)
		}
	}
}

// TestBuiltinImportNoDefault 内置模块没有 default 导出, 默认导入要当场报错。
func TestBuiltinImportNoDefault(t *testing.T) {
	err := compileErr(t, `import calc from "gx/unit-calc";`)
	if err == nil {
		t.Fatal("内置模块没有 default, 默认导入应当编译失败")
	}
	if !strings.Contains(err.Error(), "default") {
		t.Fatalf("报错应指出是 default 的问题: %v", err)
	}
}

// TestBuiltinImportAllowsUnknownAndFileModules 未注册的 / 文件模块一律放行:
// 导出表不可知, 不能凭空判死 (编译结果不能因为编译进程的链接情况而变红)。
func TestBuiltinImportAllowsUnknownAndFileModules(t *testing.T) {
	for _, src := range []string{
		`import { nope } from "gx/not-registered";`,
		`import { nope } from "./local.js";`,
		`import { add } from "gx/unit-calc";`, // 存在 → 照旧通过
	} {
		if err := compileErr(t, src); err != nil {
			t.Fatalf("%s 不该报错: %v", src, err)
		}
	}
}
