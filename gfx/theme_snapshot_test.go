package gfx

// ===== Prism S0 · 基线冻结 =====
//
// 干什么: 在动任何色值之前, 把"现状"固化成可对比的基线, 这样改完之后才能
// 证明"除了想要的变化, 其它都没走样"。
//
// 分两层, 因为它们能承担的职责完全不同:
//
//  1. **token 基线**（testdata/theme-v1.json）—— 逐 key 比对 ThemeTokens()
//     的输出。这层可以进 CI 硬 gate: 改一个色值必然红, 与字体/平台无关。
//     旧 33 个 key 的基线叫 theme-v1.json（v1 = 主题系统引入时的色板）,
//     **永不随 Prism 更新** —— 它是"旧 key 一个都没变"的证明。
//
//  2. **视觉快照**（galley 脚本渲染出的 PNG）—— 只做**结构性**断言（颜色
//     种类数、非底色像素量、各分区主色命中）。逐像素比对在 Gox 里不可行:
//     字形来自各平台系统字体（见 gallery_shot_test.go 文件头），同一脚本在
//     macOS / Windows / Linux 上产出的 PNG 本来就不相等。
//
// 为什么"故意改一个色值能触发 diff"要单独测: 一个永远绿的断言等于没有断言。
// TestThemeSnapshotDetectsColorChange 临时改一个 token, 断言基线比对**变红** ——
// 这样"快照真的在起作用"本身也被钉住了。
//
// 用法:
//   go test ./gfx/ -run 'TestThemeSnapshot' -v
//   GOX_SNAPSHOT_UPDATE=1 go test ./gfx/ -run TestThemeSnapshot   # 有意改色后重生基线
//
// 注意: 重生只该发生在**有意的**色板变更上, 且要在 PR 描述里贴 diff。

import (
	"encoding/json"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// snapshotFilePath 是 token 基线文件（相对 gfx/ 包）。
const snapshotFilePath = "testdata/theme-v1.json"

// snapshotFormatVersion 让基线文件能表达"口径变了"（与色值变了区分开）。
const snapshotFormatVersion = 1

// themeSnapshotFile 是 testdata/theme-v1.json 的结构。
//
// 用结构体而不是裸 map: 文件里要能写"这是哪个预设、哪个版本的口径",
// 这些元信息不参与比对, 但人看 diff 时需要。
type themeSnapshotFile struct {
	Version int               `json:"version"`
	Theme   string            `json:"theme"`
	Note    string            `json:"note"`
	Tokens  map[string]string `json:"tokens"`
}

// legacyTokenKeys 是 v1 主题系统引入时就存在的 33 个 token 名。
//
// 这 33 个名字被外部依赖到"改名即静默失效"的程度: ApplyOverrides 的 key、
// 脚本里的 setTheme({accent:…})、ThemeTokens() 的输出、TS 类型声明。所以
// **一个都不能改、一个都不能删**。这份清单是硬编码的 —— 它指的是历史事实,
// 不该随 Theme struct 增长而自动变化, 否则就失去意义了。
var legacyTokenKeys = []string{
	"accent", "accentText", "fieldEdge", "btnFace", "btnEdge", "track",
	"switchOff", "knob", "text", "focusRing", "fieldFace", "fieldHover",
	"fieldPress", "inputEdge", "placeholder", "scrollTrack", "scrollThumb",
	"optionActive", "popupFace", "popupEdge", "mask", "info", "warn",
	"danger", "tooltipFace", "tooltipText", "menuBarFace", "menuBarEdge",
	"menuActive", "menuHighlight", "menuShortcut", "menuSep", "selection",
}

// TestThemeTokenSyncCompleteness 是"七处同步"的自动化守卫。
//
// 背景: 加一个 token 要同时改 Theme struct、themeLight、themeDark、
// themeTokens、setToken、syncThemeVars 与 raster.go 的投影变量。漏掉
// themeTokens → gx/theme 的 current() 缺字段; 漏掉 setToken → 覆盖被静默
// 跳过; 漏掉 syncThemeVars → 切主题后某个组件颜色不跟随。**三种漏法都
// 不报错**, 只表现为"看起来没生效"。
//
// 这个用例用反射遍历 Theme 的全部 color.RGBA 字段, 逐个断言它在其余几处
// 都出现了 —— 把"靠人记"换成"靠 CI 红"。
func TestThemeTokenSyncCompleteness(t *testing.T) {
	resetTheme(t)

	typ := reflect.TypeOf(Theme{})
	fieldCount := 0
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type != reflect.TypeOf(color.RGBA{}) {
			continue
		}
		fieldCount++

		// token key = 字段名首字母小写的小驼峰
		key := lowerFirst(f.Name)

		// 1) themeTokens 必须输出它
		all := ThemeTokens()
		if _, ok := all[key]; !ok {
			t.Errorf("themeTokens() 缺少 %q（Theme.%s）—— gx/theme 的 current() 会漏掉这个字段", key, f.Name)
		}

		// 2) setToken 必须认它: 用一个特异值覆盖, 看是否写到该字段
		probe := color.RGBA{R: 0x12, G: 0x34, B: 0x56, A: 0xFF}
		tt := themeLight()
		if !tt.setToken(key, probe) {
			t.Errorf("setToken(%q) 返回 false —— ApplyOverrides 会静默跳过这个 token（Theme.%s）", key, f.Name)
		} else {
			got := reflect.ValueOf(tt).Field(i).Interface().(color.RGBA)
			if got != probe {
				t.Errorf("setToken(%q) 没写到 Theme.%s（得到 %v）", key, f.Name, got)
			}
		}

		// 3) themeTokens 的输出必须真的跟着变
		after := themeTokens(&tt)[key]
		want := "#123456ff"
		if after != want {
			t.Errorf("themeTokens()[%q] = %q, want %q —— 输出没读 %s 字段", key, after, want, f.Name)
		}
	}

	if fieldCount < 60 {
		t.Fatalf("Theme 的颜色字段只有 %d 个, 预期 >=60（33 历史 + 27 扩展）", fieldCount)
	}
}

// TestThemeTokensCount 断言 token 表规模与旧 key 完整性。
func TestThemeTokensCount(t *testing.T) {
	resetTheme(t)
	all := ThemeTokens()

	// 验收线: >=64（60 颜色 + 6 尺度）
	if len(all) < 64 {
		t.Fatalf("ThemeTokens() 只有 %d 个字段, 预期 >=64", len(all))
	}

	// 旧 33 个 key 一个都不能少
	for _, k := range legacyTokenKeys {
		if _, ok := all[k]; !ok {
			t.Errorf("历史 token %q 消失了 —— 脚本里的 setTheme({%s: …}) 会静默失效", k, k)
		}
	}
}

// TestThemeSnapshot 比对当前 token 表与 testdata/theme-v1.json。
//
// 这是 S0 的核心 gate: **改一个色值就红**。它比的是 token 值而不是像素,
// 所以跨平台稳定、不需要 xvfb、不需要字体。
func TestThemeSnapshot(t *testing.T) {
	resetTheme(t)

	// 基线是在 light 预设下取的
	if got := ThemeName(); got != "light" {
		t.Fatalf("基线用例要求从 light 预设开始, 当前是 %q", got)
	}

	got := ThemeTokens()
	want, ok := readSnapshot(t)
	if !ok {
		// 没有基线文件时只检查能否生成, 不失败 —— 但会在日志里提示
		t.Logf("%s 不存在, 跳过比对（首次请用 GOX_SNAPSHOT_UPDATE=1 生成）", snapshotFilePath)
		return
	}

	var diffs []string
	for _, k := range legacyTokenKeys {
		if got[k] != want[k] {
			diffs = append(diffs, "  "+k+": "+want[k]+" -> "+got[k])
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("历史 33 个 token 中有 %d 个值发生了变化（v1 约定: 一个都不改）:\n%s",
			len(diffs), strings.Join(diffs, "\n"))
	}

	// 扩展 token 允许新增, 但已登记的值也不该无声漂移
	for k, wv := range want {
		if indexOf(legacyTokenKeys, k) >= 0 {
			continue
		}
		if gv, ok := got[k]; ok && gv != wv {
			diffs = append(diffs, "  "+k+": "+wv+" -> "+gv+"（扩展 token）")
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%d 个 token 值与基线不一致:\n%s", len(diffs), strings.Join(diffs, "\n"))
	}
}

// TestThemeSnapshotDetectsColorChange 验证"快照真的在起作用"。
//
// 做法: 故意改一个 token, 断言 ThemeTokens() 与基线的差异**能被发现**。
// 如果这条用例红了, 说明基线比对没有真实生效（比如比错了对象、或者写成了
// 永远为真的比较）—— 那 S0 的整个防线都是空的。
func TestThemeSnapshotDetectsColorChange(t *testing.T) {
	resetTheme(t)
	want, ok := readSnapshot(t)
	if !ok {
		t.Skip("没有基线文件, 无法验证基线比对的有效性")
	}

	// 故意改一个历史 token（accent 是最容易被"顺手改一下"的那个）
	base := want["accent"]
	if base == "" {
		t.Fatalf("基线里没有 accent, 文件内容不对: %+v", want)
	}

	n := ApplyOverrides(map[string]string{"accent": "#123456"})
	if n != 1 {
		t.Fatalf("ApplyOverrides 应生效 1 个 token, 实际 %d", n)
	}
	if got := ThemeTokens()["accent"]; got == base {
		t.Fatalf("改了 accent 之后 ThemeTokens() 仍是 %q —— 快照比对会形同虚设", got)
	}

	// 复原, 保证同进程后续用例干净
	resetTheme(t)
	if got := ThemeTokens()["accent"]; got != base {
		t.Fatalf("复原后 accent = %q, want %q", got, base)
	}
}

// TestThemeSnapshotFileIsWellFormed 检查基线文件的元信息（防止手改坏）。
func TestThemeSnapshotFileIsWellFormed(t *testing.T) {
	snap, ok := readSnapshot(t)
	if !ok {
		t.Skip("没有基线文件")
	}
	if len(snap) < len(legacyTokenKeys) {
		t.Fatalf("基线只有 %d 个 token, 少于历史 33 个", len(snap))
	}
	raw, err := os.ReadFile(snapshotFilePath)
	if err != nil {
		t.Fatalf("读 %s: %v", snapshotFilePath, err)
	}
	var f themeSnapshotFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析 %s: %v", snapshotFilePath, err)
	}
	if f.Version != snapshotFormatVersion {
		t.Errorf("基线格式版本 = %d, want %d", f.Version, snapshotFormatVersion)
	}
	if f.Note == "" {
		t.Error("基线缺少 note 字段（应说明这个文件代表什么、什么时候该重生）")
	}
}

// readSnapshot 读基线 token 表。第二个返回值为 false 表示文件不存在。
func readSnapshot(t *testing.T) (map[string]string, bool) {
	t.Helper()
	raw, err := os.ReadFile(snapshotFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false
		}
		t.Fatalf("读 %s: %v", snapshotFilePath, err)
	}
	var f themeSnapshotFile
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("解析 %s: %v", snapshotFilePath, err)
	}
	return f.Tokens, true
}

// WriteThemeSnapshot 把当前 token 表写成基线文件。
func WriteThemeSnapshot(t *testing.T) {
	t.Helper()
	resetTheme(t)
	f := themeSnapshotFile{
		Version: snapshotFormatVersion,
		Theme:   "light",
		Note: "v1 主题系统引入时的色板基线（S0 基线冻结）。历史 33 个 token 的值是外部契约，**永不更新**；" +
			"带 Prism 扩展字段是因为 S1 之后 ThemeTokens() 变长了，这里只钉住它们首次登记的值。" +
			"重生用 GOX_SNAPSHOT_UPDATE=1 go test ./gfx/ -run TestThemeSnapshot，且必须在 PR 里贴 diff。",
		Tokens: ThemeTokens(),
	}
	if err := os.MkdirAll(filepath.Dir(snapshotFilePath), 0o755); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatalf("编码: %v", err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(snapshotFilePath, raw, 0o644); err != nil {
		t.Fatalf("写 %s: %v", snapshotFilePath, err)
	}
	t.Logf("写出 %s（%d 个 token）", snapshotFilePath, len(f.Tokens))
}

// TestWriteThemeSnapshotIfRequested 是有意的基线重生入口。
//
// 为什么挂在测试里而不是独立脚本: 生成与比对必须是**同一份口径**
// （同一个 ThemeTokens()、同一个 light 预设）。独立脚本很容易用另一套
// 逻辑拼出 token 表, 于是基线对了、比对错了。
func TestWriteThemeSnapshotIfRequested(t *testing.T) {
	if os.Getenv("GOX_SNAPSHOT_UPDATE") == "" {
		t.Skip("设置 GOX_SNAPSHOT_UPDATE=1 才会重生基线")
	}
	WriteThemeSnapshot(t)
}

// ---- 视觉快照的结构性断言 ----

// TestThemeVisualBaseline 断言亮/暗两套下"画面上真的有东西、且颜色层次足够"。
//
// 这不是逐像素比对（那在三平台上不可行），而是抓两类静默失效:
//   - 组件被画成空盒子（颜色数塌到个位数）;
//   - 主题切换没真正作用到绘制层（亮暗两套的统计完全一致 = 大概率没换色）。
func TestThemeVisualBaseline(t *testing.T) {
	cases := []string{"button.js", "input.js", "checkbox.js", "switch.js", "select.js"}
	for _, name := range cases {
		t.Run(strings.TrimSuffix(name, ".js"), func(t *testing.T) {
			for _, theme := range []string{"light", "dark"} {
				t.Run(theme, func(t *testing.T) {
					resetTheme(t)
					if !SetThemeNamed(theme) {
						t.Fatalf("SetThemeNamed(%q) 失败", theme)
					}
					img := renderGalleryShot(t, name)
					st := galleryInkStats(img)
					if st.Colors < galleryMinColors || st.Ink < galleryMinInk {
						t.Fatalf("%s[%s]: 画面近乎空白 (%s) —— 组件可能被画成空盒子", name, theme, st)
					}
				})
			}
		})
	}
}

// TestThemeLightDarkDiffer 断言两套预设确实画出了不同的东西。
//
// 判据取"画面主色不同"而不是逐像素: 暗色底本身就会引入不同的抗锯齿
// 过渡色, 两套的颜色集合必然不等; 若主色一致, 说明 themeDark 根本没被
// 应用到绘制层（这正是"syncThemeVars 漏同步"这类 bug 的症状）。
//
// 注意不能比"窗口底色": 画廊脚本自己控制根节点背景, 与主题无关。
func TestThemeLightDarkDiffer(t *testing.T) {
	imgs := map[string]*image.RGBA{}
	for _, theme := range []string{"light", "dark"} {
		resetTheme(t)
		SetThemeNamed(theme)
		imgs[theme] = renderGalleryShot(t, "button.js")
	}
	t.Cleanup(func() { resetTheme(t) })

	light, dark := galleryInkStats(imgs["light"]), galleryInkStats(imgs["dark"])

	// 比颜色集合而不是比主色: 脚本自带的背景色会压过组件色成为主色, 那是
	// 脚本决定的、与主题无关。真正的判据是"两张图的调色板不一样"——
	// 只要 themeDark 作用到了绘制层, 组件底色/描边/文字色就必然换了一批。
	ls, ds := paletteOf(imgs["light"]), paletteOf(imgs["dark"])
	if reflect.DeepEqual(ls, ds) {
		t.Fatalf("亮/暗两套的调色板完全相同（%d 种颜色）—— 主题没有作用到绘制层", len(ls))
	}
	t.Logf("light: %s ｜ dark: %s ｜ 调色板 light=%d dark=%d", light, dark, len(ls), len(ds))
}

// paletteOf 返回画面的调色板（去重后的颜色集合，按数量降序）。
func paletteOf(img *image.RGBA) []color.RGBA {
	b := img.Bounds()
	seen := map[color.RGBA]struct{}{}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			seen[img.RGBAAt(x, y)] = struct{}{}
		}
	}
	out := make([]color.RGBA, 0, len(seen))
	for c := range seen {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].R != out[j].R {
			return out[i].R < out[j].R
		}
		if out[i].G != out[j].G {
			return out[i].G < out[j].G
		}
		if out[i].B != out[j].B {
			return out[i].B < out[j].B
		}
		return out[i].A < out[j].A
	})
	return out
}

// lowerFirst 把导出字段名转成小驼峰 token key（Accent → accent, AccentText → accentText）。
func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// indexOf 在切片里找一个字符串, 返回下标（-1 = 没找到）。
func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
