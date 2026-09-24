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

func TestMeasureText(t *testing.T) {
	requireFont(t)
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
	t.Cleanup(func() {
		fontMu.Lock()
		injectedFonts = nil
		scannedFonts = nil
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
