package bind

import (
	"testing"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// 从 gfx/theme_test.go 逐字迁出 (绑定层拆分的配套): gx/theme 的注册点现在
// 在本包, 用例跟着门面走。主题模块不依赖窗口, 直接 EvalVM 即可。
func TestThemeJSSetPresetOverrideAndSnapshot(t *testing.T) {
	gfx.SetThemeNamed("light")
	t.Cleanup(func() { gfx.SetThemeNamed("light") })

	v, err := vm.EvalVM(`
		import { setTheme, toggleDark, current } from "gx/theme";
		globalThis.g_preset = setTheme("dark");
		globalThis.g_darkFace = current().btnFace;
		globalThis.g_override = setTheme({ btnFace: "#abcdef" });
		globalThis.g_customFace = current().btnFace;
		globalThis.g_bad = setTheme("nonsense");
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	get := func(name string) object.Value {
		val, _ := v.Globals().Get(name)
		return val
	}
	if b, _ := get("g_preset").(*object.Boolean); b == nil || !b.Value {
		t.Fatalf("setTheme(\"dark\") 应为 true: %s", get("g_preset").Inspect())
	}
	if s, _ := get("g_darkFace").(*object.String); s == nil || s.Value != "#2d2d2dff" {
		t.Fatalf("暗色 btnFace 快照 = %s, want #2d2d2dff", get("g_darkFace").Inspect())
	}
	if n, _ := get("g_override").(*object.Number); n == nil || n.Value != 1 {
		t.Fatalf("对象覆盖应生效 1 个: %s", get("g_override").Inspect())
	}
	// 6 位 hex 的 alpha 补 FF → 快照带 8 位
	if s, _ := get("g_customFace").(*object.String); s == nil || s.Value != "#abcdefff" {
		t.Fatalf("覆盖后快照 = %s, want #abcdefff", get("g_customFace").Inspect())
	}
	if b, _ := get("g_bad").(*object.Boolean); b == nil || b.Value {
		t.Fatalf("未知名应 false: %s", get("g_bad").Inspect())
	}
	// toggleDark 从 custom 状态出发落到预设
	if _, err := vm.EvalVM(`import { toggleDark } from "gx/theme"; toggleDark();`); err != nil {
		t.Fatalf("EvalVM toggleDark: %v", err)
	}
	if gfx.ThemeName() != "dark" && gfx.ThemeName() != "light" {
		t.Fatalf("toggleDark 后应落在预设上: %s", gfx.ThemeName())
	}
}
