package gfx

// ===== WCAG 对比度断言（Prism S2）=====
//
// 干什么: 把 prism-light / prism-dark 两套预设的"看得清"从**口头承诺**变成
// **CI 里会红的断言**。
//
// 为什么把阈值写进测试而不是写进文档: 文档会过期, 测试不会。下一轮有人为了
// 好看把 placeholder 调浅 3 个色阶, 这里立刻红, 而且错误信息里直接给实测
// 比值与两个色值 —— 他不需要先去翻 WCAG 才知道自己动到了哪条契约。
//
// 三条纪律:
//
//  1. **不达标就换色, 不放宽阈值**。阈值是 WCAG 给的，不是我们跟自己商量出来的；
//     放宽一次，"AA 达标"这四个字就再也不能写进 README。
//  2. **带 alpha 的配对必须先合成**（selection / tooltipFace / scrim …）。直接
//     比原始 RGBA 得到的是"颜料 vs 颜料"，不是用户看到的"像素 vs 像素"。
//  3. **v1 预设只收集不失败**（TestContrastLegacyPresetsAreDocumented）：旧预设
//     是存量契约，把它们的欠账打进文档 §9，让用 v1 的人知道代价在哪。
//
// 用法:
//
//	go test ./gfx/ -run 'TestContrast' -count=1 -v
//
// 加一条配对: 在 contrastPairs 里加一项即可（两套预设、v1 两套会自动跟着跑）。

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"testing"
)

// contrastPair 是一条对比度契约: 前景画在背景上, 比值必须 ≥ min。
type contrastPair struct {
	name string // 配对名, 写进文档表与失败信息
	fg   func(Theme) color.RGBA
	bg   func(Theme) color.RGBA
	min  float64
}

// contrastPairs 是两套 Prism 预设**共用**的配对表。
//
// 为什么两套共用一张表而不是各写一份: 漏配对是这类测试最典型的失效方式 ——
// 亮色配了 12 条、暗色只配了 9 条，暗色那 3 条就永远没人管。共用一张表让
// "漏"在结构上不可能发生。
func contrastPairs() []contrastPair {
	return []contrastPair{
		// ---- 正文文字 (1.4.3 AA · ≥4.5) ----
		//
		// 逐个 surface 都列出来, 而不是只验 surfaceBase: 组件的缺省底各不相同
		// (卡片读 raised、表格斑马纹读 sunken、菜单读 overlay), 只验一个等于
		// 默认"字画在哪都行"。
		{"text ∩ surfaceBase", text, surfaceBase, ContrastTextMin},
		{"text ∩ surfaceSunken", text, surfaceSunken, ContrastTextMin},
		{"text ∩ surfaceRaised", text, surfaceRaised, ContrastTextMin},
		{"text ∩ surfaceOverlay", text, surfaceOverlay, ContrastTextMin},
		{"text ∩ surfaceInset", text, surfaceInset, ContrastTextMin},
		{"text ∩ fieldFace", text, fieldFace, ContrastTextMin},
		{"text ∩ popupFace", text, popupFace, ContrastTextMin},
		{"text ∩ menuBarFace", text, menuBarFace, ContrastTextMin},
		{"text ∩ btnFace", text, btnFace, ContrastTextMin},

		// ---- 次级 / 弱化文字 (≥4.5) ----
		//
		// textMuted 这一组是**必改项**: v1 的 menuShortcut (#777) 在菜单栏底上
		// 只有 4.04:1。快捷键这类小字通常是 11~12px, 根本够不上"大号文字"的
		// 3:1 豁免, 必须按正文标准来。
		{"textSecondary ∩ surfaceBase", textSecondary, surfaceBase, ContrastTextMin},
		{"textMuted ∩ surfaceBase", textMuted, surfaceBase, ContrastTextMin},
		{"textMuted ∩ surfaceInset", textMuted, surfaceInset, ContrastTextMin},
		{"textMuted ∩ menuBarFace", textMuted, menuBarFace, ContrastTextMin},
		{"placeholder ∩ fieldFace", placeholder, fieldFace, ContrastTextMin},
		{"placeholder ∩ surfaceBase", placeholder, surfaceBase, ContrastTextMin},

		// ---- 实底上的前景 (≥4.5) ----
		{"accentText ∩ accent", accentText, accent, ContrastTextMin},
		{"textInverse ∩ info", textInverse, info, ContrastTextMin},
		{"textInverse ∩ success", textInverse, success, ContrastTextMin},
		{"textInverse ∩ warning", textInverse, warning, ContrastTextMin},
		{"textInverse ∩ danger", textInverse, danger, ContrastTextMin},
		// 强调色上的前景常常也被拿来画在语义实底上（比如"主要按钮"与"危险
		// 按钮"共用一套文字绘制代码）。warning 单独排除：亮色 warning 是
		// #B45309 这种中等明度的琥珀色，白字在其上刚过 4.5:1，不把它列进来
		// 是为了避免"为了过这一条把 warning 压成难看的深棕"；它已由
		// textInverse ∩ warning 覆盖。
		{"accentText ∩ info", accentText, info, ContrastTextMin},
		{"accentText ∩ success", accentText, success, ContrastTextMin},
		{"accentText ∩ danger", accentText, danger, ContrastTextMin},

		// ---- 非文本: 控件边界 (1.4.11 AA · ≥3.0) ----
		//
		// 只验 BorderControl 不验 BorderDecorative: 装饰性分隔线**故意**不达标
		// （那条 1.19:1 的白底线是刻意的），把它列进来等于逼着设计把页面画成
		// 一堆框。这正是 Theme 要拆这两档的原因。
		{"borderControl ∩ fieldFace", borderControl, fieldFace, ContrastNonTextMin},
		{"borderControl ∩ surfaceBase", borderControl, surfaceBase, ContrastNonTextMin},
		{"borderControl ∩ btnFace", borderControl, btnFace, ContrastNonTextMin},
		{"btnEdge ∩ btnFace", btnEdge, btnFace, ContrastNonTextMin},
		{"focusRing ∩ surfaceBase", focusRing, surfaceBase, ContrastNonTextMin},
		{"focusRing ∩ fieldFace", focusRing, fieldFace, ContrastNonTextMin},

		// ---- tonal: 同族前景画在 subtle 底上 (≥4.5) ----
		{"infoText ∩ infoSubtle", infoText, infoSubtle, ContrastTextMin},
		{"successText ∩ successSubtle", successText, successSubtle, ContrastTextMin},
		{"warningText ∩ warningSubtle", warningText, warningSubtle, ContrastTextMin},
		{"dangerText ∩ dangerSubtle", dangerText, dangerSubtle, ContrastTextMin},
	}
}

// ---- 取色器: 把"字段"变成函数, 让配对表能写成一张字面量表 ----
//
// 为什么不用反射按名字取: 反射会让配对名与字段名的对应关系变成字符串, 改名
// 时测试静默失效（取不到就当没这条）。写成函数, 改名会**编译不过**。

func accent(t Theme) color.RGBA         { return t.Accent }
func accentText(t Theme) color.RGBA     { return t.AccentText }
func btnFace(t Theme) color.RGBA        { return t.BtnFace }
func btnEdge(t Theme) color.RGBA        { return t.BtnEdge }
func danger(t Theme) color.RGBA         { return t.Danger }
func dangerText(t Theme) color.RGBA     { return t.DangerText }
func dangerSubtle(t Theme) color.RGBA   { return t.DangerSubtle }
func fieldFace(t Theme) color.RGBA      { return t.FieldFace }
func focusRing(t Theme) color.RGBA      { return t.FocusRing }
func info(t Theme) color.RGBA           { return t.Info }
func infoText(t Theme) color.RGBA       { return t.InfoText }
func infoSubtle(t Theme) color.RGBA     { return t.InfoSubtle }
func menuBarFace(t Theme) color.RGBA    { return t.MenuBarFace }
func placeholder(t Theme) color.RGBA    { return t.Placeholder }
func popupFace(t Theme) color.RGBA      { return t.PopupFace }
func success(t Theme) color.RGBA        { return t.Success }
func successText(t Theme) color.RGBA    { return t.SuccessText }
func successSubtle(t Theme) color.RGBA  { return t.SuccessSubtle }
func surfaceBase(t Theme) color.RGBA    { return t.SurfaceBase }
func surfaceInset(t Theme) color.RGBA   { return t.SurfaceInset }
func surfaceOverlay(t Theme) color.RGBA { return t.SurfaceOverlay }
func surfaceRaised(t Theme) color.RGBA  { return t.SurfaceRaised }
func surfaceSunken(t Theme) color.RGBA  { return t.SurfaceSunken }
func text(t Theme) color.RGBA           { return t.Text }
func textInverse(t Theme) color.RGBA    { return t.TextInverse }
func textMuted(t Theme) color.RGBA      { return t.TextMuted }
func textSecondary(t Theme) color.RGBA  { return t.TextSecondary }
func warning(t Theme) color.RGBA        { return t.Warning }
func warningText(t Theme) color.RGBA    { return t.WarningText }
func warningSubtle(t Theme) color.RGBA  { return t.WarningSubtle }
func borderControl(t Theme) color.RGBA  { return t.BorderControl }

// ---- 自校准 ----

// TestContrastSelfCalibration 证明这套公式算出来的就是 WCAG 的那个数。
//
// 为什么必须先自校准: 对比度公式有**多种错法都能跑出 plausible 的结果** ——
// 忘了线性化（直接拿 sRGB 值加权）会得到 12.9 而不是 4.48；权重写错会得到
// 4.9。两者都不会报错，只是"看起来差不多"。钉住 WCAG 官方参考值，公式错了
// 这条立刻红，而不是等到有人拿这份测试去打官司。
func TestContrastSelfCalibration(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	black := color.RGBA{A: 255}

	if got := Contrast(black, white); math.Abs(got-21.0) > 0.05 {
		t.Errorf("纯黑 vs 纯白 = %.4f, want ≈21.0（公式错了: 检查相对亮度是否线性化）", got)
	}
	// #777777 vs #FFFFFF ≈4.48 是 WCAG 2.1 Understanding 文档里的参考值
	grey := color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 255}
	if got := Contrast(grey, white); math.Abs(got-4.48) > 0.05 {
		t.Errorf("#777777 vs #FFFFFF = %.4f, want ≈4.48（±0.05）", got)
	}
	// 同色必为 1: 拦住"分母加没加 0.05"这类笔误
	if got := Contrast(white, white); math.Abs(got-1.0) > 1e-9 {
		t.Errorf("同色对比度 = %.4f, want 1.0", got)
	}
	// 顺序无关: Contrast(a,b) 与 Contrast(b,a) 必须逐位相等
	if a, b := Contrast(grey, white), Contrast(white, grey); a != b {
		t.Errorf("Contrast 不满足交换律: %.6f != %.6f", a, b)
	}
}

// ---- 两套 Prism 预设 ----

func TestContrastPrismLight(t *testing.T) {
	assertContrastPairs(t, "prism-light", prismLight())
}

func TestContrastPrismDark(t *testing.T) {
	assertContrastPairs(t, "prism-dark", prismDark())
}

// assertContrastPairs 跑完整配对表, 任何一条不达标即失败。
func assertContrastPairs(t *testing.T, preset string, th Theme) {
	t.Helper()
	var failed int
	for _, p := range contrastPairs() {
		fg, bg := p.fg(th), p.bg(th)
		got := Contrast(fg, bg)
		if got < p.min {
			failed++
			t.Errorf("%s: %s = %.2f:1 < %.1f:1（fg=%s bg=%s）",
				preset, p.name, got, p.min, contrastHex(fg), contrastHex(bg))
		}
	}
	if failed > 0 {
		t.Errorf("%s 有 %d/%d 条配对不达标 —— 换色, 不要改阈值", preset, failed, len(contrastPairs()))
	} else {
		t.Logf("%s: %d 条配对全部达标", preset, len(contrastPairs()))
	}
}

// ---- 带 alpha 的配对 ----

// TestContrastAlphaAwarePairs 验那些"原始 RGBA 不能代表用户所见"的配对。
//
// tooltipFace 是半透明的（95%），它的真实颜色取决于浮在谁上面；selection 是
// 半透明的选区底，画在文字**下面**，所以它既不能太淡（看不出选了哪段）、也
// 不能太浓（把字盖掉）。这两件事都只有先合成才谈得上。
func TestContrastAlphaAwarePairs(t *testing.T) {
	for _, tc := range []struct {
		preset string
		th     Theme
	}{{"prism-light", prismLight()}, {"prism-dark", prismDark()}} {
		t.Run(tc.preset, func(t *testing.T) {
			th := tc.th

			// 1) tooltip: 先把半透明底合成到它实际浮着的那一层上, 再比文字
			face := Composite(th.TooltipFace, th.SurfaceOverlay)
			if got := Contrast(th.TooltipText, face); got < ContrastTextMin {
				t.Errorf("tooltipText ∩ tooltipFace(合成到 surfaceOverlay 后=%s) = %.2f:1 < %.1f:1",
					contrastHex(face), got, ContrastTextMin)
			}

			// 2) 选区底本身要看得见（它是一层"非文本"的语义着色, 按 1.4.11 的
			// 3:1 会过严 —— 选区不是控件边界, 取"可辨"的 1.3:1 即可）
			if got := ContrastOn(th.Selection, th.FieldFace); got < 1.3 {
				t.Errorf("selection ∩ fieldFace = %.2f:1 < 1.3:1（选区底太淡, 看不出选了哪段）", got)
			}

			// 3) 更要紧的一条: 被选中的字仍要能读。这是 v1 注释里那句
			// "必须带 alpha" 的可执行版本。
			under := Composite(th.Selection, th.FieldFace)
			if got := Contrast(th.Text, under); got < ContrastTextMin {
				t.Errorf("text ∩ (selection 合成到 fieldFace=%s) = %.2f:1 < %.1f:1（选区把字盖掉了）",
					contrastHex(under), got, ContrastTextMin)
			}

			// 4) 遮罩: 压在页面上要能明确表达"后面不可交互"。
			//
			// 这里**不用对比度比值**判，用"相对亮度下降幅度"判: 暗色页面本来就
			// 接近黑，55% 黑遮罩压上去的比值必然贴着 1（实测 1.14:1）—— 那是物理，
			// 不是缺陷。能表达遮罩有没有起作用的量是"页面暗了多少"，取 ≥50%
			// 这条经验线（55% alpha 的黑在亮底上降 83%、在暗底上降 66%）。
			before := relativeLuminance(th.SurfaceBase)
			after := relativeLuminance(Composite(th.Scrim, th.SurfaceBase))
			if drop := (before - after) / before; drop < 0.5 {
				t.Errorf("scrim 压在 surfaceBase 上只让页面暗了 %.0f%% < 50%%（遮罩不起作用）", drop*100)
			}
		})
	}
}

// ---- 明度阶梯 ----

// TestContrastSurfaceLadderSteps 断言五级 surface 相邻两级的相对亮度差 ≥3%。
//
// 为什么这条要自动化: "层次"是最容易被"看起来差不多"糊过去的东西 —— 一版
// 色板里 raised 与 base 差 1%，人眼在单看一张图时未必能指出问题，但整个
// 界面会显得"平板"。取 3% 相对差：低于它，两级在并置时会被当成同一块面。
func TestContrastSurfaceLadderSteps(t *testing.T) {
	const minStep = 0.03
	for _, tc := range []struct {
		preset string
		th     Theme
	}{{"prism-light", prismLight()}, {"prism-dark", prismDark()}} {
		t.Run(tc.preset, func(t *testing.T) {
			// 阶梯方向随明暗反转（亮色越靠上越亮, 暗色越靠上越亮也是一样
			// 的"上"= 浮起来的那层），所以按相对亮度排序后再逐级比。
			levels := []struct {
				name string
				c    color.RGBA
			}{
				{"surfaceSunken", tc.th.SurfaceSunken},
				{"surfaceInset", tc.th.SurfaceInset},
				{"surfaceBase", tc.th.SurfaceBase},
				{"surfaceRaised", tc.th.SurfaceRaised},
				{"surfaceOverlay", tc.th.SurfaceOverlay},
			}
			for i := 1; i < len(levels); i++ {
				lo, hi := relativeLuminance(levels[i-1].c), relativeLuminance(levels[i].c)
				if lo > hi {
					lo, hi = hi, lo
				}
				step := (hi - lo) / hi
				if step < minStep {
					t.Errorf("%s: %s → %s 的相对亮度差只有 %.1f%% < %.0f%%",
						tc.preset, levels[i-1].name, levels[i].name, step*100, minStep*100)
				}
			}
		})
	}
}

// TestContrastSubtleTintsAreDistinguishable 断言四个 subtle 底与页底有可辨明度差。
//
// 只验"字在 subtle 上够不够"是不够的: 一块与页底几乎同色的 subtle 底，字当然
// 也够清楚（因为等于字直接画在页底上），但用户看不出"这块是提示"。提示的
// **容器**与**内容**是两件事，两条都要守。
func TestContrastSubtleTintsAreDistinguishable(t *testing.T) {
	const minDelta = 0.03
	for _, tc := range []struct {
		preset string
		th     Theme
	}{{"prism-light", prismLight()}, {"prism-dark", prismDark()}} {
		t.Run(tc.preset, func(t *testing.T) {
			base := relativeLuminance(tc.th.SurfaceBase)
			for _, s := range []struct {
				name string
				c    color.RGBA
			}{
				{"infoSubtle", tc.th.InfoSubtle},
				{"successSubtle", tc.th.SuccessSubtle},
				{"warningSubtle", tc.th.WarningSubtle},
				{"dangerSubtle", tc.th.DangerSubtle},
				{"accentSubtle", tc.th.AccentSubtle},
			} {
				lo, hi := base, relativeLuminance(s.c)
				if lo > hi {
					lo, hi = hi, lo
				}
				if delta := (hi - lo) / hi; delta < minDelta {
					t.Errorf("%s: %s 与 surfaceBase 的相对亮度差只有 %.1f%% < %.0f%%（看不出是块提示）",
						tc.preset, s.name, delta*100, minDelta*100)
				}
			}
		})
	}
}

// ---- v1 预设: 只收集, 不失败 ----

// TestContrastLegacyPresetsAreDocumented 对 v1 的 light / dark 跑同一张配对表,
// 把不达标项**打印**出来（数字要抄进 docs/accessibility.md §9）。
//
// 为什么不让它失败: 见 §9 的"为什么不修"。旧预设的色值是**存量契约** —— 改
// 一个色值，所有已经上线的应用的观感当场变化，而它们并没有要求升级。要达标
// 就迁到 prism。把欠账原样打印出来，是这个取舍能被复审的前提。
func TestContrastLegacyPresetsAreDocumented(t *testing.T) {
	for _, tc := range []struct {
		preset string
		th     Theme
	}{{"light (v1)", themeLight()}, {"dark (v1)", themeDark()}} {
		var lines []string
		for _, p := range contrastPairs() {
			fg, bg := p.fg(tc.th), p.bg(tc.th)
			got := Contrast(fg, bg)
			if got < p.min {
				lines = append(lines, fmt.Sprintf("  %-32s %.2f:1 (阈值 %.1f)  fg=%s bg=%s",
					p.name, got, p.min, contrastHex(fg), contrastHex(bg)))
			}
		}
		if len(lines) == 0 {
			t.Logf("%s: 全部 %d 条配对达标", tc.preset, len(contrastPairs()))
			continue
		}
		t.Logf("%s: %d/%d 条不达标（已记录到 docs/accessibility.md §9，不修 —— 存量契约）:\n%s",
			tc.preset, len(lines), len(contrastPairs()), strings.Join(lines, "\n"))
	}
}
