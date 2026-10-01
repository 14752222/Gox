package mobile

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// ===== 跨语言符号契约的静态校验 (批 C) =====
//
// 折叠上报这条链路的每一端都在**编译不到彼此**的地方:
//   Go 的 //export 名 ←→ Kotlin 的 external fun / Swift 的 @_silgen_name
//
// 名字写错不会有任何编译错误 —— Android 侧只是运行期 UnsatisfiedLinkError,
// iOS 侧更是直接链接失败或者更糟: 静默调不到。而这个仓库的既有纪律是
// "android tag 下的代码只有交叉编译才看得见错误", 本机连 iOS 都编不了。
//
// 所以这里用**读源码**的方式把契约钉住: 从 Kotlin/Swift 声明里抽出符号名,
// 回到 Go 的 //export 行里找。它拦不住所有错误, 但拦得住"改名漏改一边"这一
// 类最常见也最难查的失配。

// repoRoot 从本测试文件的位置回溯到仓库根 (gfx/mobile → ../..)。
func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("定位仓库根: %v", err)
	}
	return abs
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	p := filepath.Join(repoRoot(t), rel)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读 %s: %v", rel, err)
	}
	return string(b)
}

// TestAndroidFoldSymbolNameMatchesKotlin 钉住 Android 侧的 JNI 符号名。
//
// Kotlin `com.gox.GoxRuntime.nativeSetDisplayFold` 对应的 Go 导出必须是
// `Java_com_gox_GoxRuntime_nativeSetDisplayFold` —— JNI 的符号名规则是
// `Java_<包名点换下划线>_<类名>_<方法名>`, 一处拼错就是运行期
// UnsatisfiedLinkError (而 Gradle 构建照样绿)。
func TestAndroidFoldSymbolNameMatchesKotlin(t *testing.T) {
	kt := readRepoFile(t, "app/android/app/src/main/kotlin/com/gox/GoxRuntime.kt")
	goSrc := readRepoFile(t, "gfx/android/libgox/main.go")

	m := regexp.MustCompile(`(?m)^\s*external fun (nativeSetDisplayFold)\s*\(`).FindStringSubmatch(kt)
	if m == nil {
		t.Fatalf("Kotlin 侧找不到 external fun nativeSetDisplayFold —— 契约的另一端没了")
	}
	exportName := "Java_com_gox_GoxRuntime_" + m[1]
	if !strings.Contains(goSrc, "//export "+exportName) {
		t.Fatalf("Go 侧缺少 `//export %s` (Kotlin 声明了 %s)", exportName, m[1])
	}
}

// TestAndroidFoldSymbolNameMatchesKotlinTail 是上面那条的"全量体检": 把 Kotlin
// 里**每一个** external fun 都回查到 Go 的 //export 行。
//
// 为什么值得全查: 批 C 只是这条链路的最近一次改动, 而这套名字是逐字契约;
// 一次漏改会在这里立刻现形, 而不是等到某台安卓设备上。
func TestAndroidFoldSymbolNameMatchesKotlinTail(t *testing.T) {
	kt := readRepoFile(t, "app/android/app/src/main/kotlin/com/gox/GoxRuntime.kt")
	goSrc := readRepoFile(t, "gfx/android/libgox/main.go")

	re := regexp.MustCompile(`(?m)^\s*external fun (\w+)\s*\(`)
	names := re.FindAllStringSubmatch(kt, -1)
	if len(names) < 15 {
		t.Fatalf("Kotlin 只解析出 %d 个 external fun, 明显不对 (解析正则或文件结构变了)", len(names))
	}
	for _, m := range names {
		exportName := "Java_com_gox_GoxRuntime_" + m[1]
		if !strings.Contains(goSrc, "//export "+exportName) {
			t.Errorf("Kotlin 声明了 %s 但 Go 侧没有 `//export %s`", m[1], exportName)
		}
	}
}

// TestIOSFoldExportNameMatchesSwift 钉住 iOS 侧折叠上报的导出名与上报时机。
//
// Swift 经 bridging header 声明的 C 函数名必须与 Go 的 //export 一致;
// 同时校验"四个上报时机"都在 —— 少一处就是某些姿态下报不上去 (而画面
// 看起来只是"没适配", 很难反推到"漏了一个回调")。
func TestIOSFoldExportNameMatchesSwift(t *testing.T) {
	goSrc := readRepoFile(t, "gfx/ios/libgox/main.go")
	const name = "gox_set_display_fold"
	if !strings.Contains(goSrc, "//export "+name) {
		t.Fatalf("Go 侧缺少 `//export %s`", name)
	}

	vc := readRepoFile(t, "app/ios/App/Sources/GoxViewController.swift")
	fold := readRepoFile(t, "app/ios/App/Sources/GoxDisplayFold.swift")

	// 实现体里必须真的调导出 (只声明不调用 = 永远不报)。
	if !strings.Contains(fold, name) {
		t.Fatalf("GoxDisplayFold.swift 里没有调用 %s", name)
	}
	// 版本门槛必须是 27.1 (写 27.0 在 27.0 真机上是 unrecognized selector → 崩)。
	if !strings.Contains(fold, "#available(iOS 27.1, *)") {
		t.Errorf("折叠 API 的门槛不是 iOS 27.1 —— 写宽了会在 27.0 真机上崩")
	}
	// includeInactive 必须显式传 (Apple 两份文档对默认行为说法不一致)。
	if !strings.Contains(fold, ".includeInactive") {
		t.Errorf("缺少 .includeInactive —— 平展态就看不到那条零宽 division, hasFold 会退化成 false")
	}
	// 四个上报时机。
	for _, sig := range []string{
		"override func traitCollectionDidChange",
		"override func viewWillTransition",
		"override func viewSafeAreaInsetsDidChange",
	} {
		if !strings.Contains(vc, sig) {
			t.Errorf("GoxViewController 缺少 %s", sig)
		}
	}
	// startEngine 里的补报 (首次布局早于 gox_init 时被吞掉的那一份)。
	if n := strings.Count(vc, "reportDisplayFold()"); n < 3 {
		t.Errorf("reportDisplayFold() 只被调了 %d 处, 少于三处时机", n)
	}
	// UIScreen.main.scale 这条兜底在折叠屏上有歧义 (内外屏各有 UIScreen),
	// 必须已经换掉 —— 否则画面会按错屏的 scale 缩放。
	// 注意剥掉注释再查: 上面那句"兜底**不能**用 UIScreen.main.scale"的说明
	// 本身就是一段好注释, 不该被这条断言误伤。
	if strings.Contains(stripSwiftComments(vc), "UIScreen.main.scale") {
		t.Errorf("仍然在用 UIScreen.main.scale 兜底 (折叠屏上取到的可能是另一块屏)")
	}
}

// stripSwiftComments 去掉 // 行注释与 /* */ 块注释, 只留代码。
func stripSwiftComments(src string) string {
	var b strings.Builder
	lines := strings.Split(src, "\n")
	inBlock := false
	for _, line := range lines {
		t := strings.TrimSpace(line)
		if inBlock {
			if i := strings.Index(line, "*/"); i >= 0 {
				inBlock = false
				line = line[i+2:]
			} else {
				continue
			}
		}
		if strings.HasPrefix(t, "//") {
			continue
		}
		if strings.HasPrefix(t, "/*") {
			if !strings.Contains(t, "*/") {
				inBlock = true
			}
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// TestIOSInvalidatesOnSizeClassChange 单看"尺寸类变化也要上报"这一条。
//
// Split View 改分栏比例时 viewSize 可以一模一样, 只靠 viewWillTransition
// 会漏 —— 这是资料里明说的一条 (#1), 也是本文件最值得看住的一处。
func TestIOSInvalidatesOnSizeClassChange(t *testing.T) {
	vc := readRepoFile(t, "app/ios/App/Sources/GoxViewController.swift")
	idx := strings.Index(vc, "override func traitCollectionDidChange")
	if idx < 0 {
		t.Fatal("没有 traitCollectionDidChange")
	}
	// 该方法体内必须出现尺寸类比较 + 折叠上报。
	body := vc[idx:]
	if end := strings.Index(body[1:], "\n    override func "); end >= 0 {
		body = body[:end+1]
	}
	if !strings.Contains(body, "horizontalSizeClass") {
		t.Errorf("traitCollectionDidChange 里没有比较尺寸类")
	}
	if !strings.Contains(body, "reportDisplayFold()") {
		t.Errorf("traitCollectionDidChange 里没有触发折叠上报")
	}
}

