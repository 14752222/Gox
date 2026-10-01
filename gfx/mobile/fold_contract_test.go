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

// TestAndroidFoldWiringInHost 钉住 Android 宿主侧把折叠上报真的接起来了。
//
// 光有 external fun 声明不算接通 —— 必须有人**调**它, 且必须在正确的时机。
// 这几条断言拦的正是"声明了但忘了接"这一类 (症状只是"折叠屏上没适配",
// 从画面很难反推回"漏了一行接线")。
func TestAndroidFoldWiringInHost(t *testing.T) {
	fold := readRepoFile(t, "app/android/app/src/main/kotlin/com/gox/GoxDisplayFold.kt")
	main := readRepoFile(t, "app/android/app/src/main/kotlin/com/gox/MainActivity.kt")

	// 实现体里必须真的调导出。
	if !strings.Contains(fold, "GoxRuntime.nativeSetDisplayFold(") {
		t.Errorf("GoxDisplayFold.kt 里没有调用 nativeSetDisplayFold")
	}
	// isSeparating → kind 的判据必须在 (这是 division/occlusion 的唯一来源)。
	if !strings.Contains(fold, "isSeparating") {
		t.Errorf("没有用 isSeparating 判 division/occlusion")
	}
	// 姿态必须按 State 判, 不能按宽度自己反推。
	if !strings.Contains(fold, "FoldingFeature.State.HALF_OPENED") {
		t.Errorf("没有按 FoldingFeature.State 判姿态")
	}
	// 宿主侧: 必须 start() 且在 onDestroy 里 stop() (不摘监听会打回调到已销毁的 Activity)。
	if !strings.Contains(main, "displayFold.start()") {
		t.Errorf("MainActivity 没有启动折叠监听")
	}
	if !strings.Contains(main, "displayFold.stop()") {
		t.Errorf("MainActivity 没有在 onDestroy 里摘掉折叠监听")
	}
	if !strings.Contains(main, "override fun onConfigurationChanged") {
		t.Errorf("MainActivity 没有 onConfigurationChanged —— 旋转/折叠/内外屏切换时不会重报")
	}
	// 清单必须声明 density (否则内外屏密度切换会重建 Activity, 会话整个作废)。
	manifest := readRepoFile(t, "app/android/app/src/main/AndroidManifest.xml")
	if !strings.Contains(manifest, "android:configChanges") ||
		!strings.Contains(manifest, "density") {
		t.Errorf("AndroidManifest 的 configChanges 没有声明 density")
	}
	// useAndroidX 必须开 (androidx.window 是硬依赖)。
	if gp := readRepoFile(t, "app/android/gradle.properties"); !strings.Contains(gp, "android.useAndroidX=true") {
		t.Errorf("gradle.properties 里 android.useAndroidX 不是 true")
	}
}

// TestAndroidFoldSymbolNameMatchesKotlinTail 是上面那条的"全量体检": 把 Kotlin
// 里**每一个** external fun 都回查到 Go 的 //export 行。
//
// 为什么值得全查: 批 C 只是这条链路的最近一次改动, 而这套名字是逐字契约;
// 一次漏改会在这里立刻现形, 而不是等到某台安卓设备上。
func TestAndroidFoldSymbolNameMatchesKotlinTail(t *testing.T) { //nolint:gocyclo
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

// ===== 鸿蒙 NAPI 契约 (HF1 / HF2) =====
//
// 鸿蒙这条链路两端同样**编译不到彼此**, 而且与 Android 有本质差别:
//
//   Android:  JNI 符号名 (Java_com_gox_GoxRuntime_nativeXxx) 由 JVM 自动绑定
//             名字写错 → 运行期 UnsatisfiedLinkError
//   鸿蒙:     NAPI **没有**自动绑定 —— 必须自己 napi_module_register 注册模块,
//             方法靠**序号**分发。名字写错 → ArkTS 拿到 undefined, 调用即
//             "not a function"; 序号错位 → 调 A 却执行了 B。两种都不报编译错。
//
// 所以这里钉三件事:
//   ① 模块真的注册了 (bridge.c 的 napi_module_register + nm_modname);
//   ② ArkTS 侧调用的每个名字都在 Go 的 goxMethods 表里 (d.ts 双向核对);
//   ③ 折叠链路真的接起来了 (监听 → 上报 → libgox.setDisplayFold)。
// 外加一条 cgo preamble 的结构性守卫 —— 那个坑本工程踩过四次, 每次的报错都
// 指向别处 ("build constraints exclude all Go files" / "C source files not
// allowed when not using cgo" / "unexpected character <U+2014>"), 值得静态钉死。

// harmonyGoxMethods 从 Go 的 goxMethods 表里按**出现顺序**抽出导出名。
func harmonyGoxMethods(t *testing.T) []string {
	t.Helper()
	src := readRepoFile(t, "gfx/harmony/libgox/main.go")
	idx := strings.Index(src, "var goxMethods = []string{")
	if idx < 0 {
		t.Fatal("gfx/harmony/libgox/main.go 里找不到 goxMethods 表")
	}
	body := src[idx:]
	if end := strings.Index(body, "\n}"); end >= 0 {
		body = body[:end]
	}
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "\"") {
			continue
		}
		rest := line[1:]
		cut := strings.Index(rest, "\"")
		if cut <= 0 {
			t.Fatalf("goxMethods 表里有解析不了的行: %q", line)
		}
		out = append(out, rest[:cut])
	}
	if len(out) < 15 {
		t.Fatalf("goxMethods 只解析出 %d 项, 明显不对 (表结构变了?)", len(out))
	}
	return out
}

// TestHarmonyNAPIEntryPointsExported 钉住两个 //export 与 bridge.c 的模块注册。
//
// 模块注册这件事与 Android 的差异最大, 也最容易漏: JNI 那边"写了函数就有",
// 鸿蒙这边**不注册就等于 import 不到** (症状是 ArkTS 侧 `import nativeGox from
// 'libgox.so'` 拿到 null, 而 hvigor 构建全绿)。
func TestHarmonyNAPIEntryPointsExported(t *testing.T) {
	goSrc := readRepoFile(t, "gfx/harmony/libgox/main.go")
	bridge := readRepoFile(t, "gfx/harmony/libgox/bridge.c")

	for _, name := range []string{"GoxModuleRegister", "GoxDispatch"} {
		if !strings.Contains(goSrc, "//export "+name) {
			t.Errorf("gfx/harmony/libgox/main.go 缺少 `//export %s`", name)
		}
	}
	// 重活必须转回 Go —— 定义只能放 bridge.c (preamble 里放定义会被 cgo 复制两份)。
	if !strings.Contains(bridge, "gox_dispatch_go(") {
		t.Errorf("bridge.c 没有调用 gox_dispatch_go —— C 侧的 trampoline 没接回 Go")
	}
	if !strings.Contains(bridge, "napi_module_register(&gox_module)") {
		t.Fatalf("bridge.c 没有调用 napi_module_register —— NAPI 无符号名自动绑定, 不注册就是 import 不到")
	}
	if !strings.Contains(bridge, `.nm_modname = "gox"`) {
		t.Errorf(`bridge.c 的 nm_modname 不是 "gox" (必须与 libgox.so 的库名对应)`)
	}
	if !strings.Contains(bridge, "__attribute__((constructor))") {
		t.Errorf("bridge.c 缺少 constructor 属性 —— 模块注册要在 so 被加载时自动发生, 没有宿主调用时机")
	}
}

// TestHarmonyArkTSCallNamesExistInGoxMethods 把 ArkTS 侧**实际调用**的每个名字
// 回查到 Go 的方法表。
//
// 拦的是"改了名字漏改一边": ArkTS 侧拿到 undefined, 调用即 not a function,
// 而构建照样绿、日志里只有一句含糊报错。
func TestHarmonyArkTSCallNamesExistInGoxMethods(t *testing.T) {
	known := map[string]bool{}
	for _, n := range harmonyGoxMethods(t) {
		known[n] = true
	}

	root := filepath.Join(repoRoot(t), "app", "harmony", "entry", "src", "main", "ets")
	re := regexp.MustCompile(`nativeGox\.(\w+)\(`)
	seen := map[string]bool{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".ets") {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			seen[m[1]] = true
			if !known[m[1]] {
				t.Errorf("%s 调用了 nativeGox.%s, 但 goxMethods 表里没有这个名字",
					filepath.Base(p), m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历 ArkTS 源码: %v", err)
	}
	if len(seen) < 10 {
		t.Fatalf("只扫到 %d 个 nativeGox 调用, 明显不对 (宿主没接起来?)", len(seen))
	}
}

// TestHarmonyTypeDeclMatchesGoxMethods 双向核对 index.d.ts 与 goxMethods。
//
// 单向不够: 表里有而 d.ts 里没有 → ArkTS 拿不到类型 (只能靠 any 兜);
// d.ts 里有而表里没有 → 声明了一个永远不会被导出的函数, 调用即崩溃。
func TestHarmonyTypeDeclMatchesGoxMethods(t *testing.T) {
	methods := harmonyGoxMethods(t)
	dts := readRepoFile(t, "app/harmony/entry/src/main/cpp/types/libgox/index.d.ts")

	known := map[string]bool{}
	for _, n := range methods {
		known[n] = true
	}
	declared := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^export const (\w+):`).FindAllStringSubmatch(dts, -1) {
		declared[m[1]] = true
		if !known[m[1]] {
			t.Errorf("index.d.ts 声明了 %s, 但 Go 的 goxMethods 表里没有", m[1])
		}
	}
	if len(declared) < 15 {
		t.Fatalf("index.d.ts 只解析出 %d 个 export const, 明显不对 (声明格式变了?)", len(declared))
	}
	for _, n := range methods {
		if !declared[n] {
			t.Errorf("index.d.ts 里没有声明 %s (Go 侧已导出)", n)
		}
	}
}

// TestHarmonyFoldWiringInHost 钉住鸿蒙宿主把折叠上报真的接起来了。
//
// "声明了但忘了接"这一类在画面上只表现为"折叠屏上没适配", 几乎不可能反推到
// 漏了哪一行接线 —— 所以逐条钉。
func TestHarmonyFoldWiringInHost(t *testing.T) {
	fold := readRepoFile(t, "app/harmony/entry/src/main/ets/native/GoxDisplayFold.ets")
	ability := readRepoFile(t, "app/harmony/entry/src/main/ets/entryability/EntryAbility.ets")

	// 唯一的上报出口 (与 Android 的 nativeSetDisplayFold / iOS 的
	// gox_set_display_fold 三端同构, 最终都进 gfx/mobile.ReportDisplayFold)。
	if !strings.Contains(fold, "nativeGox.setDisplayFold(") {
		t.Errorf("GoxDisplayFold.ets 没有调用 nativeGox.setDisplayFold")
	}
	if !strings.Contains(fold, "display.isFoldable()") {
		t.Errorf("没有用 display.isFoldable() 判定可折叠 —— 直板机上也会去注册监听")
	}
	if !strings.Contains(fold, "display.on('foldStatusChange'") {
		t.Errorf("没有订阅 foldStatusChange —— 折起来不会重报")
	}
	// 约束①: 只有半折叠态才更新折痕缓存, 其余状态**保留**上次的。
	if !strings.Contains(fold, "FOLD_STATUS_HALF_FOLDED") {
		t.Errorf("没有按 FOLD_STATUS_HALF_FOLDED 判折痕有效 —— 折痕会随平展被清掉, hasFold 会 flip-flop")
	}
	// 约束③: 布局响应式走尺寸, 不走姿态。宽度/高度断点必须与内核
	// deriveSizeClasses (gfx/viewport.go) 同一组, 否则会出现"宿主说 expanded、
	// 内核算 medium"这种很难查的布局抖动。
	for _, dp := range []string{"600", "840", "480"} {
		if !strings.Contains(fold, dp) {
			t.Errorf("GoxDisplayFold.ets 里没有断点 %s —— 与内核 deriveSizeClasses 对不上", dp)
		}
	}
	// 宿主侧: 必须 start() 且在 onDestroy 里 stop()
	// (不摘监听会把回调打到已销毁的 Ability 上)。
	if !strings.Contains(ability, "fold.start()") {
		t.Errorf("EntryAbility 没有启动折叠监听")
	}
	if !strings.Contains(ability, "fold.stop()") {
		t.Errorf("EntryAbility 没有在 onDestroy 里摘掉折叠监听")
	}
}

// TestHarmonyPreambleIsPureASCIIAndDeclarationOnly 守卫 cgo preamble 的结构性约束。
//
// 这个坑本工程踩过四次, 而**四种症状指向的都是别处**:
//   - preamble 被自己提前闭合 → "C source files not allowed when not using cgo"
//   - preamble 含非 ASCII      → "unexpected character <U+2014>" / 标识符里出现中文标点
//   - preamble 里放了定义      → "duplicate symbol: gox_trampoline" (cgo 复制两份)
//   - 忘了 -tags harmony        → "build constraints exclude all Go files"
//
// 所以用一条静态断言钉死, 而不是靠下次再踩一遍。
func TestHarmonyPreambleIsPureASCIIAndDeclarationOnly(t *testing.T) {
	src := readRepoFile(t, "gfx/harmony/libgox/main.go")
	imp := strings.Index(src, `import "C"`)
	if imp < 0 {
		t.Fatal(`gfx/harmony/libgox/main.go 里找不到 import "C"`)
	}
	head := src[:imp]
	open := strings.LastIndex(head, "/*")
	if open < 0 {
		t.Fatal("找不到 cgo preamble 的起始 /*")
	}
	rel := strings.Index(head[open:], "*/")
	if rel < 0 {
		t.Fatal("cgo preamble 没有闭合 —— 之后的 C 代码会变成 Go 源码, 症状是 C source files not allowed")
	}
	preamble := head[open : open+rel]
	if strings.Count(preamble, "/*") != 1 || strings.Count(preamble, "*/") != 0 {
		t.Errorf("cgo preamble 里出现了嵌套的注释标记 —— cgo 会剥掉外层标记, 嵌套会让它提前/推后闭合")
	}
	for i, r := range preamble {
		if r > 0x7f {
			t.Fatalf("cgo preamble 第 %d 字节含非 ASCII 字符 %q —— clang 直接编译这段, 会报 unexpected character", i, r)
		}
	}
	if strings.Contains(preamble, "{") {
		t.Errorf("cgo preamble 里出现了 { —— 这里只能放**声明**; 定义会被 cgo 复制进两个 C 文件, 链接期 duplicate symbol")
	}
}
