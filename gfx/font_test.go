package gfx

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
)

// ===== 字体与文本测量 =====
//
// 由原 p3_test.go 的字体部分与 font_os_test.go 合并而来。

// requireFont 无可用系统字体时跳过测试 (非 Windows 环境等)。
func requireFont(t *testing.T) {
	t.Helper()
	if _, err := loadBaseFont(); err != nil {
		t.Skipf("no system font available: %v", err)
	}
}

// requireCJKFont 在**已加载的字体不含 CJK 字形**时跳过测试。
//
// 为什么需要它: requireFont 只验证"有字体可加载", 但 CI 的 ubuntu runner 上
// 只有 fonts-dejavu-core —— 它是纯拉丁字体, 一个汉字字形都没有。于是
// TestMeasureText 的 "加一" 量出 16px (缺字形时 glyph() 退化成 advance=size/2),
// 而期望 ≈32px (两个全宽字形), 用例红。
//
// 这类用例的语义是"验证 CJK 度量/光栅", 前提是**字体真的覆盖 CJK** —— 前提不成立
// 时应当 Skip (与 requireFont 同一处理), 而不是断言失败。真正要保证的是: 一旦
// 环境提供了 CJK 字体, 这些断言必须成立 (本机 Windows / macOS / 装了 CJK 字体的
// Linux 都会真正跑它们)。fonts-noto-cjk 也在 ci.yml 的 apt 清单里, 让 ubuntu
// 默认就能跑到这些用例而不是一路 Skip。
func requireCJKFont(t *testing.T) {
	t.Helper()
	requireFont(t)
	f, err := loadBaseFont()
	if err != nil {
		t.Skipf("no system font available: %v", err)
	}
	// 用字体自带的 cmap 判断是否真有 CJK 字形, 不靠"量出来的宽度"反推 ——
	// 后者正是被测代码, 用它做前置条件会掩盖真实缺陷。
	if !fontHasCJK(f) {
		t.Skipf("loaded font has no CJK glyphs (need fonts-noto-cjk); skipping CJK metric test")
	}
}

// fontHasCJK 报告字体是否含常用汉字字形 (取几个不同区段的探针字)。
func fontHasCJK(f *opentype.Font) bool {
	for _, r := range []rune{'加', '一', '汉', '字'} {
		idx, err := f.GlyphIndex(nil, r)
		if err != nil || idx == 0 {
			return false
		}
	}
	return true
}

func TestMeasureText(t *testing.T) {
	requireCJKFont(t)
	w, h := MeasureText("Hello", 16)
	if w <= 0 || h <= 0 {
		t.Fatalf("latin measure: w=%d h=%d", w, h)
	}
	// CJK 字形宽度应约为全宽 (≈字号×字符数): "加一"@16 ≈ 32px
	w2, _ := MeasureText("加一", 16)
	if w2 < 30 || w2 > 36 {
		t.Fatalf("CJK measure = %d, want ≈32 (2 full-width glyphs)", w2)
	}
	// 字号越大越宽
	w3, _ := MeasureText("Hello", 32)
	if w3 <= w {
		t.Fatalf("larger font should measure wider: %d vs %d", w3, w)
	}
}

func TestDrawTextPixels(t *testing.T) {
	requireFont(t)
	img := image.NewRGBA(image.Rect(0, 0, 200, 50))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	FillRect(img, Rect{0, 0, 200, 50}, white)

	drawn := DrawText(img, img.Bounds(), "Gox 加一", 4, 4, 20, color.RGBA{R: 200, G: 30, B: 30, A: 255}, 0)
	if drawn <= 0 {
		t.Fatalf("no pixels drawn")
	}
	// 画过的区域应有非白像素
	dark := 0
	for y := 0; y < 50; y++ {
		for x := 0; x < 200; x++ {
			c := img.RGBAAt(x, y)
			if c.R != 255 || c.G != 255 {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Fatalf("text did not leave any pixels")
	}

	// 截断: maxWidth 限制绘制宽度
	img2 := image.NewRGBA(image.Rect(0, 0, 200, 50))
	FillRect(img2, Rect{0, 0, 200, 50}, white)
	full, _ := MeasureText("Hello World", 16)
	half := DrawText(img2, img2.Bounds(), "Hello World", 0, 0, 16, color.RGBA{R: 0, A: 255}, full/2)
	if half > full/2+16 { // 允许一个字符的余量
		t.Fatalf("truncation failed: drawn=%d limit≈%d", half, full/2)
	}
}

// ===== 系统字体候选与平台扫描 (P3-7) =====
//
// P3-7 修的是"Linux 上 fontCandidates 全是 C:\Windows\Fonts 路径 → 找不到字体
// → 文字完全不渲染"。所以这里的断言重点是"候选与平台匹配"和"扫描真的按上限
// 收工", 而不是"某台机器上恰好有这个文件"。

// TestFontCandidatesMatchOS 候选列表不得混入其它平台的路径。
// 混入的症状很隐蔽: 候选永远读不到文件, 却永远排在最前面把真正可用的字体挤到
// 后面 (loadBaseFontLocked 逐个试, 前面的失败只累积 lastErr)。
func TestFontCandidatesMatchOS(t *testing.T) {
	if len(fontCandidates) == 0 {
		t.Fatalf("fontCandidates 为空: 至少要有静态候选兜底")
	}
	for _, p := range fontCandidates {
		switch runtime.GOOS {
		case "windows":
			if strings.HasPrefix(p, "/usr/") || strings.HasPrefix(p, "/System/") ||
				strings.HasPrefix(p, "/Library/") {
				t.Fatalf("Windows 候选混入了 Unix 路径: %s", p)
			}
		default:
			if strings.HasPrefix(p, `C:\`) || strings.HasPrefix(p, `C:/`) {
				t.Fatalf("%s 候选混入了 Windows 路径: %s", runtime.GOOS, p)
			}
		}
	}
}

// TestFontScanDirsSkipsWindows Windows 刻意不扫描: 静态候选可靠, 而扫
// C:\Windows\Fonts 要解析几百个 ttc, 每个进程启动都付一次不划算。
func TestFontScanDirsSkipsWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("该断言只对 Windows 分支有意义")
	}
	if dirs := fontScanDirs(); len(dirs) != 0 {
		t.Fatalf("Windows 不该有扫描目录, 实际 %v", dirs)
	}
}

// firstUsableFontPath 返回一个当前机器上确实能解析的字体路径 (找不到则跳过)。
// 用真实字体做样本, 是为了让"扫描能认出真字体"这件事有真凭据 —— 拿假数据
// 只能证明"都没认出来"。
func firstUsableFontPath(t *testing.T) string {
	t.Helper()
	initFontCandidates() // 确保扫描型候选已补齐 (Linux 上全靠它)
	for _, p := range fontCandidates {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if _, err := parseFontFile(p); err == nil {
			return p
		}
	}
	t.Skip("当前机器没有可解析的系统字体, 跳过")
	return ""
}

// TestScanSystemFontsPrefersCJKAndSkipsGarbage 扫描要: ① 只收真能解析的字体;
// ② CJK 字体排在拉丁字体之前。
//
// ② 的理由: 拉丁字体同样能正常解析加载, 只是中文全画成豆腐块 —— 那比"完全
// 不出字"更难排查, 所以宁可让"看起来能覆盖中文"的字体先被选中。
func TestScanSystemFontsPrefersCJKAndSkipsGarbage(t *testing.T) {
	data, err := os.ReadFile(firstUsableFontPath(t))
	if err != nil {
		t.Fatalf("读取字体样本: %v", err)
	}
	dir := t.TempDir()
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatalf("写样本 %s: %v", name, err)
		}
	}
	write("DejaVuSans.ttf", data)          // 拉丁名
	write("NotoSansCJK-Regular.ttf", data) // CJK 名, 应与上面同内容
	write("Broken.ttf", []byte("not a font at all"))
	write("README.txt", data) // 非字体扩展名

	got := scanSystemFonts([]string{dir}, 100)
	if len(got) != 2 {
		t.Fatalf("扫描结果 = %v, 期望恰好 2 个真字体 (垃圾 .ttf 与非字体扩展名都要跳过)", got)
	}
	if !looksCJK(filepath.Base(got[0])) {
		t.Fatalf("CJK 字体应排在首位, 实际首个是 %s (完整结果 %v)", filepath.Base(got[0]), got)
	}
	if filepath.Base(got[1]) != "DejaVuSans.ttf" {
		t.Fatalf("非 CJK 字体应排在后面, 实际 %v", got)
	}
}

// TestScanSystemFontsRespectsLimit 条目上限必须真的生效。
//
// 观测方式刻意做成"结果差异"而不是"没卡住": 把 20 个垃圾文件排在真字体之前
// (ReadDir 按文件名排序), 上限 5 时扫不到真字体, 上限足够时扫得到 —— 只有
// 上限被真正执行, 才会出现这个区别。
func TestScanSystemFontsRespectsLimit(t *testing.T) {
	data, err := os.ReadFile(firstUsableFontPath(t))
	if err != nil {
		t.Fatalf("读取字体样本: %v", err)
	}
	dir := t.TempDir()
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, fmt.Sprintf("a%02d.ttf", i))
		if err := os.WriteFile(name, []byte("junk"), 0o644); err != nil {
			t.Fatalf("写垃圾文件 %s: %v", name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "zzz_real.ttf"), data, 0o644); err != nil {
		t.Fatalf("写真字体: %v", err)
	}

	if got := scanSystemFonts([]string{dir}, 5); len(got) != 0 {
		t.Fatalf("上限 5 时不该扫到排在第 21 位的真字体, 实际 %v", got)
	}
	if got := scanSystemFonts([]string{dir}, 1000); len(got) != 1 {
		t.Fatalf("上限足够时应恰好扫到那一个真字体, 实际 %v", got)
	}
}

// TestScanSystemFontsMissingDir 目录不存在/无权限不是错误 (Linux 上
// ~/.fonts 常常不存在), 必须静默跳过而不是 panic。
func TestScanSystemFontsMissingDir(t *testing.T) {
	got := scanSystemFonts([]string{filepath.Join(t.TempDir(), "definitely-missing")}, 100)
	if len(got) != 0 {
		t.Fatalf("不存在的目录应返回空, 实际 %v", got)
	}
}

// TestFontLexiconHelpers 收口两个小判定逻辑, 防止后续调整正则/后缀时静默走偏。
func TestFontLexiconHelpers(t *testing.T) {
	cjk := []string{"NotoSansCJK-Regular.ttc", "wqy-zenhei.ttc", "DroidSansFallbackFull.ttf", "SourceHanSansSC.otf"}
	for _, n := range cjk {
		if !looksCJK(n) {
			t.Fatalf("%s 应被判定为 CJK 字体", n)
		}
	}
	for _, n := range []string{"DejaVuSans.ttf", "Arial.ttf", "segoeui.ttf"} {
		if looksCJK(n) {
			t.Fatalf("%s 不该被判定为 CJK 字体", n)
		}
	}
	for _, n := range []string{"a.ttf", "a.otf", "a.ttc", "a.otc", "A.TTF"} {
		if !fontExtOK(n) {
			t.Fatalf("%s 应是字体扩展名", n)
		}
	}
	for _, n := range []string{"README", "fonts.dir", "a.txt", "a.png"} {
		if fontExtOK(n) {
			t.Fatalf("%s 不该被当成字体文件", n)
		}
	}
}

// TestSetFontPathInjectsHostFont 宿主注入字体 (移动端唯一可控的字体来源)。
//
// 移动端为什么必须走它: Android 定制 ROM 的字体命名无规律、iOS 沙箱读不到系统
// 字体 —— 唯一可控的做法是随包带一份字体、由宿主把沙箱路径交进来。所以这里要
// 断言的是**优先级与生效性**, 不是"某个文件在不在"。
func TestSetFontPathInjectsHostFont(t *testing.T) {
	host := firstUsableFontPath(t)
	if host == "" {
		t.Skip("本机没有可解析的字体文件")
	}

	// 测试会改包级字体状态 (候选表/基础字体/掩码缓存), 收尾必须还原 ——
	// 否则后续用例会拿着被改过的候选表下结论。
	//
	// 还原 = **存快照再放回**, 不是"清成空"。scannedFonts 是 initFontCandidates
	// 一次性扫描的结果, 而那个 sync.Once 已经用掉 —— 清掉就再也补不回来, 候选表
	// 会永久退化成静态兜底。那在 Linux 上是灾难: 兜底是 DejaVu/Liberation (没有
	// CJK 字形), 于是**后面所有渲染中文的用例都看不见中文**。Windows/macOS 的静态
	// 兜底本身就是 CJK 字体 (msyh / PingFang), 所以这个坑只在 Linux 上现形 ——
	// gfx 画廊的非空白断言就是这么连续红了几个批次。
	savedInjected, savedScanned := injectedFonts, scannedFonts
	t.Cleanup(func() {
		fontMu.Lock()
		injectedFonts, scannedFonts = savedInjected, savedScanned
		rebuildCandidatesLocked()
		baseFont = nil
		// 就地清空, 免得为了写 map[int]font.Face{} 再引入一个 import
		for k := range faceBySize {
			delete(faceBySize, k)
		}
		fontMu.Unlock()
		glyphLRU.reset()
	})

	before := len(fontCandidates)
	SetFontPath(host)
	if len(fontCandidates) != before+1 || fontCandidates[0] != host {
		t.Fatalf("注入的字体应排在最前: len=%d first=%q", len(fontCandidates), fontCandidates[0])
	}
	SetFontPath(host) // 重复注入同一个路径应幂等
	if len(fontCandidates) != before+1 {
		t.Fatalf("重复注入不该重复添加: len=%d want=%d", len(fontCandidates), before+1)
	}
	SetFontPath("") // 空路径忽略
	if len(fontCandidates) != before+1 {
		t.Fatalf("空路径应被忽略: len=%d", len(fontCandidates))
	}

	// 先让字体真的加载过 (缓存非空), 再注入**另一个**可解析的字体:
	// 此时必须清掉 face 与字形掩码 —— 掩码是旧字体渲染的, 留着会中新 face 配
	// 旧字形。这是注入"看起来没生效"最常见的根因。
	if _, err := loadBaseFont(); err != nil {
		t.Fatalf("loadBaseFont: %v", err)
	}
	if _, err := fontFace(14); err != nil {
		t.Fatalf("fontFace: %v", err)
	}
	second := ""
	for _, p := range fontCandidates {
		if p == host {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			if _, err := parseFontFile(p); err == nil {
				second = p
				break
			}
		}
	}
	if second == "" {
		t.Skip("本机只有一个可解析字体, 跳过缓存重置断言")
	}
	SetFontPath(second)
	fontMu.Lock()
	base, faces := baseFont, len(faceBySize)
	fontMu.Unlock()
	if base != nil || faces != 0 {
		t.Fatalf("注入新字体后应清掉基础字体与 face 缓存: base=%v faces=%d", base != nil, faces)
	}
	// 清完必须还能立刻重建 (不能出现"缓存清了但重载失败"的坏状态)
	if _, err := fontFace(14); err != nil {
		t.Fatalf("重置后 fontFace 应能重建: %v", err)
	}
	// 注入顺序即优先级: 先注入的仍排在前面 (后注入的不会插队)
	if fontCandidates[0] != host || fontCandidates[1] != second {
		t.Fatalf("两次注入应按先后顺序排在候选最前: %v", fontCandidates[:2])
	}
}

// TestFontScanNotSilentlyEmpty 扫描目录真实存在、却一条字体都没扫到 —— 那是
// "候选表退化成静态兜底"的症状, 不是"这台机器没字体", 必须报出来。
//
// 为什么单独立一条: 这个状态曾经把 Linux 的 gfx 画廊断言连续染红几个批次
// (中文整体不渲染 ⇒ 只剩扁平色块 ⇒ 报"画面近乎空白"), 而报错完全指向组件
// 注册表, 查了很久才发现根因是共享状态被后面用例清掉 (见
// TestSetFontPathInjectsHostFont 收尾的那条教训)。
//
// 扫描命中 0 有两个可能: ① 状态被清空; ② 条目预算 fontScanLimit 被位图字体
// 目录吃光 (walk 对每个条目都计数, 含 .pcf.gz)。两者的表现一样, 而这台机器上
// 目录确实存在 —— 无论哪种都该立刻炸出来, 不该静默留一个没有 CJK 的兜底字体。
func TestFontScanNotSilentlyEmpty(t *testing.T) {
	initFontCandidates()
	dirs := fontScanDirs()
	if len(dirs) == 0 {
		t.Skip("本平台不做目录扫描 (Windows: 静态候选已足够可靠)")
	}
	sawDir := false
	for _, d := range dirs {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			sawDir = true
			break
		}
	}
	if !sawDir {
		t.Skipf("本机没有可扫描的字体目录 (%v)", dirs)
	}
	if len(scannedFonts) == 0 {
		t.Fatalf("字体目录 %v 存在, 但目录扫描命中 0 条 —— 候选表已退化为静态兜底 "+
			"(fontCandidates=%v)。静态兜底若没有 CJK 字体, 整屏中文会静默不渲染; "+
			"先查有没有用例把 scannedFonts 清空了, 再看 fontScanLimit(%d) 是不是被位图字体目录吃光。",
			dirs, fontCandidates, fontScanLimit)
	}
}

// TestBaseFontIsNotBoldFace 默认字体必须是**正体面**。
//
// 为什么单独立一条 (2026-10-07, 看板 rXrGfu): CI 的 ubuntu runner 上目录扫描按
// 字母序把 NotoSansCJK-Bold.ttc 排在了最前, 于是 baseFont 成了粗体面 —— 整个应用
// 的正文全变粗, 而且"加粗"这个样式再也做不出来 (族索引里 {bold:true} 也指到它
// 自己, 正体与粗体逐像素相同)。这条用例就是那次的护栏。
//
// 判据用**面内容** (子族名) 而不是文件名: Linux 上文件名与真实字重对不上的情况
// 很常见 (实测 NotoSansCJK-Bold.ttc 的面 0 子族名写着 Bold, 内容却与 Regular 面 0
// 逐字节相同 —— 正因如此才必须靠"跳过粗体面"来挡, 不能靠信任文件名)。
func TestBaseFontIsNotBoldFace(t *testing.T) {
	requireFont(t)
	f, err := loadBaseFont()
	if err != nil {
		t.Fatalf("loadBaseFont: %v", err)
	}
	var buf sfnt.Buffer
	sub := fontNameOf(f, &buf, sfnt.NameIDSubfamily)
	if a := axisFromFont(f, 0, sub); a.bold {
		t.Fatalf("默认字体不应是粗体面: 子族名=%q, 候选表前 3 条=%v。\n"+
			"默认字体是粗体 ⇒ 正文整体变粗, 且 bold 样式再也做不出来 "+
			"(索引里 {bold:true} 会指到同一个面, 正体与粗体逐像素相同)。\n"+
			"查 loadBaseFontLocked 的「跳过粗体面」分支是否还在。",
			sub, firstN(fontCandidates, 3))
	}
}

// TestFaceIndexDoesNotImplyWeight 集合字体的面**不能**按序号判字重。
//
// 为什么: 曾按"面 0 正体、面 >0 粗体"来兜 TTC 里子族名不区分的族, 实测是错的 ——
// 集合字体的**每个面都有自己的族名**:
//
//	simsun.ttc 面0 族=SimSun  子族=Regular;  面1 族=NSimSun  子族=Regular
//	msyh.ttc   面0 族=Microsoft YaHei;      面1 族=Microsoft YaHei UI
//
// 即 NSimSun 是"另一个族", 不是 SimSun 的粗体面。按序号硬判粗体的后果: nsimsun
// 只剩 {b=1} 一格, 而 genericMono 兜底 (firstMonoLocked) 恰好返回它 ⇒
// lookup("monospace") 查不到正体轴, 泛型等宽族整体失效 (实测连带两条用例一起红)。
func TestFaceIndexDoesNotImplyWeight(t *testing.T) {
	f, err := parseFontFile(`C:\Windows\Fonts\simsun.ttc`)
	if err != nil {
		t.Skip("本机没有 simsun.ttc (非 Windows)")
	}
	var buf sfnt.Buffer
	// 面 1 是 NSimSun: 它的子族名是 Regular, 必须判成正体。
	sub1 := fontNameOf(f, &buf, sfnt.NameIDSubfamily)
	if a := axisFromFont(f, 1, sub1); a.bold {
		t.Fatalf("面序号不该影响字重判定: 面 1 子族名=%q 却判成了粗体", sub1)
	}
}

// firstN 返回列表前 n 条 (不足则全给), 只用于错误消息。
func firstN(list []string, n int) []string {
	if len(list) < n {
		n = len(list)
	}
	return list[:n]
}

// TestBundledFontPair 随包字体必须是"正体 + 粗体"一对, 且族名一致。
//
// 为什么要有随包字体 (2026-10-07, 看板 rXrGfu): 精简镜像 (CI 的 ubuntu runner) 只装
// TTF 版 Noto CJK, 而系统里那一份 .ttc 的面序与名字并不可信 —— 实测
// NotoSansCJK-Bold.ttc 的面 0 子族名写着 Bold, 内容却与 Regular 面 0 逐字节相同,
// 结果"加粗"永远画不出区别。与其去猜发行版的布局, 不如自己带一对确定的。
//
// 断言的是"这一对能被正确识别", 不是"系统里有没有它": 缺文件时跳过, 因为随包字体
// 是**可选兜底** (SetBaseAssetsDir 没调用就不用), 不该让没带资源的构建红。
func TestBundledFontPair(t *testing.T) {
	abs, err := filepath.Abs(filepath.Join("..", "assets", "fonts"))
	if err != nil {
		t.Skipf("定位随包字体目录失败: %v", err)
	}
	reg := filepath.Join(abs, "NotoSansSC-Regular.otf")
	bold := filepath.Join(abs, "NotoSansSC-Bold.otf")
	if _, err := os.Stat(reg); err != nil {
		t.Skipf("本构建不带随包字体: %v", err)
	}
	if _, err := os.Stat(bold); err != nil {
		t.Skipf("本构建不带随包字体: %v", err)
	}

	var buf sfnt.Buffer
	fr, err := parseFontFile(reg)
	if err != nil {
		t.Fatalf("随包正体读不出: %v", err)
	}
	fb, err := parseFontFile(bold)
	if err != nil {
		t.Fatalf("随包粗体读不出: %v", err)
	}
	sr := fontNameOf(fr, &buf, sfnt.NameIDSubfamily)
	sb := fontNameOf(fb, &buf, sfnt.NameIDSubfamily)
	if axisFromFont(fr, 0, sr).bold {
		t.Fatalf("随包正体被判成了粗体: 子族=%q；默认字体若落到这一份, 整个应用正文都会变粗。", sr)
	}
	if !axisFromFont(fb, 0, sb).bold {
		t.Fatalf("随包粗体没被判成粗体: 子族=%q；后果是加粗退化成合成 (或干脆无效)。", sb)
	}
	familyR := fontNameOf(fr, &buf, sfnt.NameIDFamily)
	familyB := fontNameOf(fb, &buf, sfnt.NameIDFamily)
	if familyR != familyB {
		t.Fatalf("随包正体(%q)与粗体(%q)的族名必须一致, 否则族索引里「加粗」查不到同一族", familyR, familyB)
	}
}
