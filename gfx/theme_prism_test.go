package gfx

// ===== Prism 预设的接线自测（S2）=====
//
// 干什么: 守住"预设名 → 真的换成了那套色"这条链路。contrast_test.go 验的是
// **色值本身**好不好，这里验的是**色值有没有真的被用上** —— 两者漏一个，另
// 一个都会伪装成绿的（色板再好，SetThemeNamed 不认这个名字也是白搭）。
//
// 为什么单独一个文件: 主题测试 (theme_test.go) 的 resetTheme 把状态钉在
// light，而这里要反复在四个预设之间跳；混进去会让两边的清理互相干扰。

import (
	"image/color"
	"reflect"
	"testing"
)

// TestPrismPresetNamesAreAccepted 断言两个新预设名能被 SetThemeNamed 认下。
func TestPrismPresetNamesAreAccepted(t *testing.T) {
	cases := []struct {
		name  string
		want  string
		theme Theme
	}{
		{"prism-light", "#0e7490ff", prismLight()},
		{"prism-dark", "#22d3eeff", prismDark()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			resetTheme(t)
			if !SetThemeNamed(c.name) {
				t.Fatalf("SetThemeNamed(%q) 返回 false —— 预设名没被认下", c.name)
			}
			if got := ThemeName(); got != c.name {
				t.Fatalf("ThemeName() = %q, want %q", got, c.name)
			}
			// 走 ThemeTokens() 而不是直接比 struct: current() 给脚本的就是
			// 这张表, 它是"用户实际拿到什么"的口径。
			if got := ThemeTokens()["accent"]; got != c.want {
				t.Fatalf("ThemeTokens()[accent] = %s, want %s", got, c.want)
			}
			// 投影缓存也要跟上, 否则组件绘制读到的还是上一套色
			if colorAccent != c.theme.Accent {
				t.Fatalf("投影变量 colorAccent = %v, want %v（syncThemeVars 没跟到 prism）", colorAccent, c.theme.Accent)
			}
		})
	}
}

// TestPrismPresetNamesDoNotBreakLegacy 断言 v1 的两个名字与未知名行为不变。
//
// 这条看起来多余，但"加新分支"最容易犯的错就是顺手改了 default 分支 —— 那会
// 让 setTheme("nonsense") 从"返回 false" 变成"换了个主题"。
func TestPrismPresetNamesDoNotBreakLegacy(t *testing.T) {
	resetTheme(t)
	if !SetThemeNamed("light") || !SetThemeNamed("dark") {
		t.Fatal("light / dark 仍须是合法预设名")
	}
	resetTheme(t)
	if SetThemeNamed("prism") || SetThemeNamed("") || SetThemeNamed("PRISM-LIGHT") {
		t.Fatal("未知名 / 空串 / 大小写不同都必须返回 false")
	}
	if got := ThemeName(); got != "light" {
		t.Fatalf("失败的 SetThemeNamed 不应改动当前主题名, got %q", got)
	}
}

// TestPrismToggleDarkStaysInFamily 断言 toggleDark 在 Prism 家族内往返。
//
// 守的是"观感断裂": 如果 prism-dark 一按 toggleDark 就跳回 v1 的 light，用户
// 看到的是一整套配色在两个设计系统之间弹跳，而不是开夜间模式。
func TestPrismToggleDarkStaysInFamily(t *testing.T) {
	for _, start := range []string{"prism-light", "prism-dark"} {
		t.Run(start, func(t *testing.T) {
			resetTheme(t)
			if !SetThemeNamed(start) {
				t.Fatalf("SetThemeNamed(%q) 失败", start)
			}
			want := "prism-dark"
			if start == "prism-dark" {
				want = "prism-light"
			}
			ToggleDark()
			if got := ThemeName(); got != want {
				t.Fatalf("从 %s 出发 toggleDark() 后 = %q, want %q（跳出了 Prism 家族）", start, got, want)
			}
			ToggleDark()
			if got := ThemeName(); got != start {
				t.Fatalf("往返之后 = %q, want %q", got, start)
			}
		})
	}
}

// TestPrismToggleDarkLegacyUnchanged 断言 v1 家族的往返行为一个字都没动。
func TestPrismToggleDarkLegacyUnchanged(t *testing.T) {
	resetTheme(t)
	ToggleDark()
	if got := ThemeName(); got != "dark" {
		t.Fatalf("light → toggleDark() = %q, want dark", got)
	}
	ToggleDark()
	if got := ThemeName(); got != "light" {
		t.Fatalf("dark → toggleDark() = %q, want light", got)
	}
}

// TestPrismPresetsFillEveryColorField 断言两套预设**每个色值字段都填了**。
//
// 为什么值得一条用例: 61 个字段逐个手填，漏一个得到的是 color.RGBA{} ——
// 全透明黑。它不会报错、不会 nil panic，只表现为"这块东西怎么没了"。反射
// 遍历是唯一能自动发现它的办法。
func TestPrismPresetsFillEveryColorField(t *testing.T) {
	rgbaType := reflect.TypeOf(color.RGBA{})
	for _, tc := range []struct {
		preset string
		theme  Theme
	}{{"prism-light", prismLight()}, {"prism-dark", prismDark()}} {
		v := reflect.ValueOf(tc.theme)
		filled := 0
		for i := 0; i < v.NumField(); i++ {
			f := v.Field(i)
			if f.Type() != rgbaType {
				continue
			}
			c := f.Interface().(color.RGBA)
			filled++
			if c.A == 0 {
				t.Errorf("%s.%s 是全透明 (%s) —— 画出来等于没画（漏填？）",
					tc.preset, v.Type().Field(i).Name, contrastHex(c))
			}
		}
		// 61 = 33 个历史 token + 28 个 Prism 扩展字段（扩展字段数比立项时的
		// "27" 多一个, 因为 DangerSubtle/DangerText 与 Success/Warning 各三件
		// 套加起来是 9 个而非 8 个）。这里写死 61 而不是 >=60: 字段集是 S1
		// 冻结过的, 加减字段都该先改这条用例, 让它逼着人想一次。
		if filled != 61 {
			t.Errorf("%s 填了 %d 个色值字段, 预期 61（Theme 的字段集被改了？）", tc.preset, filled)
		}
	}
}

// TestPrismPresetsDifferFromLegacy 断言 Prism 确实是另一套色, 不是 v1 的别名。
func TestPrismPresetsDifferFromLegacy(t *testing.T) {
	if reflect.DeepEqual(prismLight(), themeLight()) {
		t.Fatal("prism-light 与 light 完全相同 —— 新预设没起作用")
	}
	if reflect.DeepEqual(prismDark(), themeDark()) {
		t.Fatal("prism-dark 与 dark 完全相同 —— 新预设没起作用")
	}
	// 反过来: 旧预设不许被这轮改动碰到（theme-v1.json 的基线比的是同一件事,
	// 这里从 Go 侧再钉一次, 免得有人为了"让 prism 好看"去改 v1 的值）
	if prismLight().Accent == themeLight().Accent || prismDark().Accent == themeDark().Accent {
		t.Fatal("prism 与 v1 的 accent 相同 —— 两套预设必须是独立的色板")
	}
}
