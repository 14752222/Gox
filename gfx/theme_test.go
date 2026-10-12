package gfx

import (
	"image/color"
	"testing"
)

// T07 主题系统。注意所有用例都要把全局主题恢复回亮色 —— 包级状态在
// 包内共享, 一个用例切了暗色会把后面的像素断言全部带歪。

// resetTheme 把主题状态恢复回缺省并在用例结束后再恢复一次。
func resetTheme(t *testing.T) {
	t.Helper()
	SetThemeNamed("light")
	t.Cleanup(func() { SetThemeNamed("light") })
}

// forceRed 不是任何缺省色的值, 仅作像素断言哨兵。
var forceRed = color.RGBA{R: 0xFE, G: 0x00, B: 0x01, A: 0xFF}

func TestThemePresetSwitchAndProjection(t *testing.T) {
	resetTheme(t)

	if ThemeName() != "light" {
		t.Fatalf("初始应为 light, got %s", ThemeName())
	}
	// 亮色预设 = raster.go 投影缓存的初值 (逐字迁移保证)
	if colorBtnFace != (themeLight().BtnFace) {
		t.Fatalf("亮色预设与投影初值不一致: %v", colorBtnFace)
	}

	if !SetThemeNamed("dark") {
		t.Fatalf("dark 预设应存在")
	}
	if ThemeName() != "dark" {
		t.Fatalf("切换后名字未更新: %s", ThemeName())
	}
	if colorBtnFace != themeDark().BtnFace {
		t.Fatalf("暗色未投影到色变量: %v", colorBtnFace)
	}
	if CurrentTheme().BtnFace != themeDark().BtnFace {
		t.Fatalf("CurrentTheme 未反映暗色")
	}
	// CurrentTheme 返回副本: 改它不影响生效主题
	snap := CurrentTheme()
	snap.BtnFace = forceRed
	if CurrentTheme().BtnFace == forceRed {
		t.Fatalf("CurrentTheme 不应泄漏内部状态")
	}

	if !SetThemeNamed("light") {
		t.Fatalf("切回 light 失败")
	}
	if colorBtnFace != themeLight().BtnFace {
		t.Fatalf("切回亮色后色变量未恢复")
	}

	// 未知名: false 且状态不动
	if SetThemeNamed("nonsense") {
		t.Fatalf("未知名应返回 false")
	}
	if ThemeName() != "light" {
		t.Fatalf("失败切换不该改主题名: %s", ThemeName())
	}
}

func TestThemeToggleDarkRoundTrip(t *testing.T) {
	resetTheme(t)
	ToggleDark()
	if ThemeName() != "dark" {
		t.Fatalf("toggle 后应为 dark: %s", ThemeName())
	}
	ToggleDark()
	if ThemeName() != "light" {
		t.Fatalf("再 toggle 应回 light: %s", ThemeName())
	}
}

func TestThemeSwitchRecolorsDefaultsButNotProps(t *testing.T) {
	resetTheme(t)
	// 两个 button: 一个用主题缺省面, 一个显式给 background —— 切换暗色后
	// 前者必须变色, 后者必须纹丝不动 (props 优先于主题)。
	root := mkNode("column", nil)
	def := mkNode("button", nil)
	fixed := withStr(mkNode("button", nil), "background", "#c0392b")
	mountChildren(root, def, fixed)

	img := renderTree(root, 300, 120)
	defPx := img.RGBAAt(def.Box.X+4, def.Box.Y+4)
	fixedPx := img.RGBAAt(fixed.Box.X+4, fixed.Box.Y+4)
	if defPx == themeDark().BtnFace {
		t.Fatalf("亮色下默认 button 不应是暗色面")
	}
	if fixedPx != pxRed {
		t.Fatalf("显式 background 未生效 (亮色): %v", fixedPx)
	}

	SetThemeNamed("dark")
	img2 := renderTreeThemed(root, 300, 120)
	if got := img2.RGBAAt(def.Box.X+4, def.Box.Y+4); got != themeDark().BtnFace {
		t.Fatalf("切暗后默认 button 未随主题变色: %v want %v", got, themeDark().BtnFace)
	}
	if got := img2.RGBAAt(fixed.Box.X+4, fixed.Box.Y+4); got != pxRed {
		t.Fatalf("切暗后显式 background 被主题改掉了: %v", got)
	}
}

func TestThemeApplyOverrides(t *testing.T) {
	resetTheme(t)
	lightFace := themeLight().BtnFace

	// 好的覆盖生效 + 名字变 custom
	if n := ApplyOverrides(map[string]string{"accent": "#e67e22", "btnFace": "#abcdef"}); n != 2 {
		t.Fatalf("应生效 2 个覆盖, got %d", n)
	}
	if ThemeName() != "custom" {
		t.Fatalf("覆盖后应为 custom: %s", ThemeName())
	}
	if colorAccent != (color.RGBA{R: 0xE6, G: 0x7E, B: 0x22, A: 0xFF}) {
		t.Fatalf("accent 覆盖未投影: %v", colorAccent)
	}
	if CurrentTheme().BtnFace != (color.RGBA{R: 0xAB, G: 0xCD, B: 0xEF, A: 0xFF}) {
		t.Fatalf("btnFace 覆盖未生效: %v", CurrentTheme().BtnFace)
	}

	// 坏 key / 坏色值静默跳过, 返回实际生效数
	if n := ApplyOverrides(map[string]string{"noSuchToken": "#ffffff", "accent": "not-a-color"}); n != 0 {
		t.Fatalf("无效覆盖不应生效, got %d", n)
	}

	// 回亮色预设恢复
	SetThemeNamed("light")
	if colorBtnFace != lightFace {
		t.Fatalf("回预设未恢复 btnFace")
	}
}

