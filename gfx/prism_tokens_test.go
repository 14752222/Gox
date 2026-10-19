package gfx

// Prism 设计系统 token 的**数值来源** (Prism S8)。
//
// 为什么要有这个用例: docs/prism-design-system.md 里的六张 token 总表如果手打,
// 第一次就会错, 之后每次改代码都会漂移 —— 而"文档里的 token 表"是设计系统的
// 门面, 它错一次, 后面所有按它写脚本的人都要错一遍。所以表是**这里印出来的**:
//
//	GOX_PRISM_DUMP=1 go test ./gfx/ -run TestPrismTokenTables -count=1 -v
//
// 平时它是一条普通的断言: 锁住"表该有几行、每个 key 都有值、TouchMin 是物理量"
// 这类**结构性**不变量。印表与断言共用同一份数据, 于是"印出来的"必然是"被测
// 过的" —— 反过来则不然, 这也是为什么它是测试而不是一个独立的 main 包
// (独立包会因为没有断言而在半年内失去维护)。

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
)

// prismColorGroups 是文档的分组顺序 (组名 → 该组的 token key)。
//
// 组是**语义分组**而不是字段顺序: 60 个颜色按首字段顺序读出来是一份"调色板",
// 按语义分组才是一份能照着写组件的表。这也是 Suite 存在的唯一理由。
var prismColorGroups = []struct {
	name string
	keys []string
}{
	{"surface（表面）", []string{
		"surfaceBase", "surfaceSunken", "surfaceRaised", "surfaceOverlay", "surfaceInset",
		"fieldFace", "btnFace", "popupFace", "tooltipFace", "menuBarFace", "fieldHover", "fieldPress",
	}},
	{"text（文字）", []string{
		"text", "textSecondary", "textMuted", "textDisabled", "textInverse", "placeholder",
		"tooltipText", "menuShortcut", "accentText", "infoText", "successText", "warningText", "dangerText",
	}},
	{"border / edge（边界）", []string{
		"borderControl", "borderDecorative", "inputEdge", "btnEdge", "popupEdge", "menuBarEdge", "menuSep", "fieldEdge",
	}},
	{"accent（强调）", []string{"accent", "accentSubtle", "accentText", "focusRing", "selection", "knob"}},
	{"semantic（语义四件套）", []string{
		"info", "infoSubtle", "infoText",
		"success", "successSubtle", "successText",
		"warning", "warningSubtle", "warningText",
		"danger", "dangerSubtle", "dangerText", "warn",
	}},
	{"state（运行时状态）", []string{
		"stateHover", "statePress", "stateSelected", "stateRowHover",
		"menuActive", "menuHighlight", "optionActive",
	}},
	{"scroll / scrim（滚动与遮罩）", []string{
		"scrollTrack", "scrollThumb", "scrollThumbHover", "track", "switchOff", "scrim", "mask",
	}},
}

// prismSpaceSteps 是间距表的 dp 序列 (Space0..Space10)。
var prismSpaceSteps = []struct {
	name string
	dp   int
}{
	{"Space0", Space0}, {"Space1", Space1}, {"Space2", Space2}, {"Space3", Space3},
	{"Space4", Space4}, {"Space5", Space5}, {"Space6", Space6}, {"Space7", Space7},
	{"Space8", Space8}, {"Space9", Space9}, {"Space10", Space10},
}

// prismRadiusSteps 是圆角表的序列。
var prismRadiusSteps = []struct {
	name  string
	token RadiusToken
}{
	{"RadiusNone", RadiusNone}, {"RadiusXS", RadiusXS}, {"RadiusSM", RadiusSM},
	{"RadiusMD", RadiusMD}, {"RadiusLG", RadiusLG}, {"RadiusXL", RadiusXL},
	{"RadiusFull", RadiusFull},
}

// TestPrismTokenTables 是"文档数值 <-> 代码"的双向闸门。
//
// 默认行为 = 断言 (CI 跑的就是它); GOX_PRISM_DUMP=1 时额外把 md 表印到标准输出,
// 供 docs/prism-design-system.md 更新时直接取用。
func TestPrismTokenTables(t *testing.T) {
	tokens := themeTokens(&themeCurrent)
	// 61 = 60 个语义色 + `warn` 这一个 v1 别名 (它与 `warning` 同值, 见下)。
	// 这个数字被嵌进断言, 是为了让"往 Theme 里加了一个颜色字段却没写进文档"
	// 在 CI 里红 —— 而不是等到有人照着文档写组件时发现找不到那一格。
	if n := len(prismColorKeys()); n != 61 {
		t.Errorf("themeTokens() 的颜色 key 数 = %d, want 61 (60 语义色 + warn 别名)", n)
	}
	// warn 是 v1 的名字, Prism 的正名是 warning, 两者必须始终同值 ——
	// 一旦漂移, 老脚本与新组件会画出两种橙色。
	if tokens["warn"] != tokens["warning"] {
		t.Errorf("warn %q 与 warning %q 必须同值 (前者是 v1 别名)", tokens["warn"], tokens["warning"])
	}
	// 每个分组里的 key 都必须在 themeTokens 里存在且非空 —— "表里有一行空值"
	// 是这类文档最常见的失效形态。
	for _, g := range prismColorGroups {
		for _, k := range g.keys {
			v, ok := tokens[k]
			if !ok || v == "" {
				t.Errorf("token %q 不在 themeTokens() 里 (或为空): ok=%v %q", k, ok, v)
			}
		}
	}
	// 反向: themeTokens 的颜色 key 必须都被分组表覆盖 (否则改了颜色字段但文档
	// 没跟上 —— 这正是 S1 的 TestThemeTokenSyncCompleteness 想治的同一类病)。
	grouped := map[string]bool{}
	for _, g := range prismColorGroups {
		for _, k := range g.keys {
			if _, ok := tokens[k]; ok {
				grouped[k] = true
			}
		}
	}
	var missing []string
	for _, k := range prismColorKeys() {
		if !grouped[k] {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("以下颜色 token 未出现在文档分组里 (docs 会缺行): %s", strings.Join(missing, ", "))
	}

	// 尺度 token: 1x 必须恒等 (桌面逐像素不变的前提)
	for _, s := range prismSpaceSteps {
		if got := Space(s.dp); got != s.dp {
			t.Errorf("1x 下 Space(%s=%d) = %d, want 恒等", s.name, s.dp, got)
		}
	}
	for _, r := range prismRadiusSteps {
		if r.token == RadiusFull {
			continue
		}
		if got := Radius(r.token); got != int(r.token) {
			t.Errorf("1x 下 Radius(%s) = %d, want 恒等", r.name, got)
		}
	}
	if TouchMin != 44 {
		t.Errorf("TouchMin = %d, want 44", TouchMin)
	}
	if MotionFast != 120 || MotionBase != 180 || MotionSlow != 260 {
		t.Errorf("Motion 三档被改动了: %d/%d/%d", MotionFast, MotionBase, MotionSlow)
	}

	if dumpTheme := os.Getenv("GOX_PRISM_THEME"); dumpTheme != "" {
		prevName := themeName
		prev := themeCurrent
		t.Cleanup(func() { SetThemeNamed(prevName); SetTheme(prev) })
		if !SetThemeNamed(dumpTheme) {
			t.Fatalf("未知主题 %q (可用: light / dark / prism-light / prism-dark)", dumpTheme)
		}
	}
	if os.Getenv("GOX_PRISM_DUMP") != "" {
		fmt.Print(prismDumpMarkdown(themeTokens(&themeCurrent)))
	}
}

// prismColorKeys 返回 themeTokens 里的**全部颜色 key** (排序稳定)。
//
// 判据是"以 # 开头": 非颜色 token (space*/radius*/motion*/touchMin) 是十进制
// 字符串, 两者不会互相误会。
func prismColorKeys() []string {
	tokens := themeTokens(&themeCurrent)
	var keys []string
	for k, v := range tokens {
		if strings.HasPrefix(v, "#") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// prismShortHex 把 `#rrggbbaa` 在**不透明**时简写成 `#rrggbb`。
//
// 与 docs/accessibility.md 的写法保持一致: 那份文档里的色值全是 6 位。带
// alpha 的 token (selection / scrim / tooltipFace / mask) 保留 8 位 —— 少了
// 那两位就会让人误以为它是实色。
func prismShortHex(v string) string {
	if len(v) == 9 && strings.HasSuffix(strings.ToLower(v), "ff") {
		return strings.ToLower(v[:7])
	}
	return strings.ToLower(v)
}

// prismDumpMarkdown 印出 docs/prism-design-system.md 里那几张表。
func prismDumpMarkdown(tokens map[string]string) string {
	var b strings.Builder
	// 当前主题: 表格里带的是"此刻生效的主题"的值, 文档里的 Prism 两列应由
	// SetThemeNamed("prism-light") / ("prism-dark") 各跑一次取得。
	b.WriteString("\n===== BEGIN PRISM TOKEN DUMP =====\n")
	b.WriteString(fmt.Sprintf("theme=%s\n\n", themeName))

	b.WriteString("| token | " + themeName + " |\n| --- | --- |\n")
	for _, g := range prismColorGroups {
		b.WriteString(fmt.Sprintf("| **%s** | |\n", g.name))
		for _, k := range g.keys {
			if v, ok := tokens[k]; ok {
				b.WriteString(fmt.Sprintf("| `%s` | `%s` |\n", k, prismShortHex(v)))
			}
		}
	}

	b.WriteString("\n| token | dp | 1x px | 2x px | 3x px |\n| --- | --- | --- | --- | --- |\n")
	for _, s := range prismSpaceSteps {
		b.WriteString(fmt.Sprintf("| `%s` | %d | %d | %d | %d |\n",
			s.name, s.dp, s.dp, s.dp*2, s.dp*3))
	}

	b.WriteString("\n| token | dp | 1x px | 2x px |\n| --- | --- | --- | --- |\n")
	for _, r := range prismRadiusSteps {
		if r.token == RadiusFull {
			b.WriteString("| `RadiusFull` | — | `-1`（哨兵，绘制侧按 min(w,h)/2 算） | `-1` |\n")
			continue
		}
		b.WriteString(fmt.Sprintf("| `%s` | %d | %d | %d |\n", r.name, int(r.token), int(r.token), int(r.token)*2))
	}

	b.WriteString(fmt.Sprintf("\n| token | ms |\n| --- | --- |\n| `MotionFast` | %d |\n| `MotionBase` | %d |\n| `MotionSlow` | %d |\n| `TouchMin` | %d px（物理像素，不随密度换算） |\n",
		MotionFast, MotionBase, MotionSlow, TouchMin))
	b.WriteString("===== END PRISM TOKEN DUMP =====\n")
	return b.String()
}
