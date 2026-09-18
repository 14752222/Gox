package gfx

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

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
