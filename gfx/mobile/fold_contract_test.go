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

// TestIOSFoldExportNameMatchesSwift 钉住 iOS 侧折叠上报的导出名。
//
// Swift 经 bridging header 声明的 C 函数名必须与 Go 的 //export 一致。
func TestIOSFoldExportNameMatchesSwift(t *testing.T) {
	goSrc := readRepoFile(t, "gfx/ios/libgox/main.go")
	const name = "gox_set_display_fold"
	if !strings.Contains(goSrc, "//export "+name) {
		t.Fatalf("Go 侧缺少 `//export %s`", name)
	}
	// Swift 侧的调用点: 折叠上报必然出现在视图控制器里 (reservedRegions 读取处)。
	root := repoRoot(t)
	var found bool
	_ = filepath.Walk(filepath.Join(root, "app", "ios"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".swift") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if strings.Contains(string(b), name) {
			found = true
		}
		return nil
	})
	if !found {
		// 宿主侧胶水属于批 H1; 本用例在批 C 阶段只保证 Go 侧导出存在,
		// 一旦 Swift 侧接了调用就要能对上 —— 所以这里只提示不失败。
		t.Logf("提示: app/ios 下还没出现 %s 的调用点 (批 H1 待做)", name)
	}
}
