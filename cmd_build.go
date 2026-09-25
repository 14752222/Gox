// cmd_build.go 实现 `gox build <target>`: 统一构建入口。
//
// 流程: sync（权限注入）→ icon（图标生成）→ 平台打包:
//
//	android: 交叉编译 libgox.so（scripts/build-android.sh）→ 尝试 gradle assembleDebug
//	ios:     交叉编译 libgox.a（scripts/build-ios.sh）→ 提示 xcodebuild 步骤
//	windows: jsbuild 打包桌面可执行文件（.syso 内嵌图标+版本资源）
//	macos:   jsbuild 打包并组装 .app bundle（Info.plist + .icns）
//
// 桌面打包依赖 Gox 源码仓库（jsbuild 用 go.mod replace 引用运行时）;
// 定位顺序: GOX_REPO 环境变量 → 从当前目录逐级向上找。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/icongen"
)

func runBuild(args []string) {
	var target, dir string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox build <android|ios|windows|macos> [目录]

统一构建入口: 先 sync（权限注入）与 icon（图标生成）, 再做平台打包。
  android  libgox.so（NDK 交叉编译）+ gradle assembleDebug（有工具链时）
  ios      libgox.a（Xcode 交叉编译）+ xcodebuild 步骤提示
  windows  桌面 exe（图标与版本信息内嵌）
  macos    .app bundle（可执行 + Info.plist + 图标）

桌面打包需要 Gox 源码仓库（自动向上查找, 或设 GOX_REPO 指定）。
`)
			return
		default:
			if target == "" {
				target = a
				continue
			}
			dir = a
		}
	}
	if dir == "" {
		dir = "."
	}
	switch target {
	case "android", "ios", "windows", "macos":
	default:
		if target == "" {
			buildFatal("缺少构建目标: gox build <android|ios|windows|macos>")
		}
		buildFatal("未知构建目标 %q （可用: android|ios|windows|macos）", target)
	}

	// 第 1、2 步对全部平台一致: sync → icon
	cfg, err := config.Load(dir)
	if err != nil {
		buildFatal("%v", err)
	}
	if _, err := config.Sync(dir, cfg); err != nil {
		buildFatal("权限注入失败: %v", err)
	}
	fmt.Println("==> sync: 权限已注入 AndroidManifest / Info.plist")

	icon := cfg.Icon
	if !filepath.IsAbs(icon) {
		icon = filepath.Join(dir, icon)
	}
	if _, err := os.Stat(icon); err != nil {
		buildFatal("源图标不可读: %s（先跑 gox create 或补上 assets/icon.png）", icon)
	}
	if _, err := icongen.GenerateAll(dir, icon, cfg.Android.AdaptiveBackground); err != nil {
		buildFatal("图标生成失败: %v", err)
	}
	fmt.Println("==> icon: 全平台图标已生成")

	switch target {
	case "android":
		buildAndroid(dir)
	case "ios":
		buildIOS(dir)
	case "windows":
		buildDesktop(dir, cfg, "windows")
	case "macos":
		buildDesktop(dir, cfg, "darwin")
	}
}

// buildAndroid: libgox.so → jniLibs → gradle（有工具链时）。
func buildAndroid(dir string) {
	repo := goxRepoRoot()
	if repo == "" {
		fmt.Fprintln(os.Stderr, "gox build android: 找不到 Gox 源码仓库（设 GOX_REPO 或在仓库内运行）, 跳过 libgox.so 交叉编译")
	} else if err := runStep(dir, "bash", filepath.Join(repo, "scripts", "build-android.sh")); err != nil {
		fmt.Fprintf(os.Stderr, "gox build android: libgox.so 交叉编译失败: %v（检查 NDK 配置）\n", err)
	}

	// gradle: 项目里有 wrapper 或系统 gradle 时执行; 否则给出明确指引
	gradleArgs := []string{"assembleDebug"}
	if _, err := os.Stat(filepath.Join(dir, "android", "gradlew")); err == nil {
		if err := runStep(filepath.Join(dir, "android"), "./gradlew", gradleArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "gox build android: gradle 构建失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("完成: android/app/build/outputs/apk/debug/")
		return
	}
	if _, err := exec.LookPath("gradle"); err == nil {
		if err := runStep(filepath.Join(dir, "android"), "gradle", gradleArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "gox build android: gradle 构建失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("完成: android/app/build/outputs/apk/debug/")
		return
	}
	fmt.Println("未检测到 gradle —— APK 构建留给用户执行:")
	fmt.Println("  cd android && gradle assembleDebug")
	fmt.Println("真机验收步骤见 docs/platform-config.md（Phase 5 验收清单）")
}

// buildIOS: libgox.a → xcodebuild 提示（iOS 工程需 Xcode 工程, 详见文档）。
func buildIOS(dir string) {
	repo := goxRepoRoot()
	if repo == "" {
		fmt.Fprintln(os.Stderr, "gox build ios: 找不到 Gox 源码仓库, 跳过 libgox.a 交叉编译")
	} else if err := runStep(dir, "bash", filepath.Join(repo, "scripts", "build-ios.sh")); err != nil {
		fmt.Fprintf(os.Stderr, "gox build ios: libgox.a 交叉编译失败: %v（检查 Xcode 配置）\n", err)
	}
	fmt.Println("libgox.a 就绪后, 用 Xcode 打开 ios/ 工程构建安装:")
	fmt.Println("  xcodebuild -project ios/*.xcodeproj -scheme App -configuration Debug build")
	fmt.Println("真机验收步骤见 docs/platform-config.md（Phase 5 验收清单）")
}

// buildDesktop: 走 jsbuild（packager）, windows 内嵌图标/版本, darwin 出 .app。
func buildDesktop(dir string, cfg config.Config, goos string) {
	repo := goxRepoRoot()
	if repo == "" {
		buildFatal("桌面打包需要 Gox 源码仓库（jsbuild 机制依赖它）—— 设 GOX_REPO 环境变量或在仓库内运行")
	}
	entry := filepath.Join(dir, "src", "main.js")
	if _, err := os.Stat(entry); err != nil {
		buildFatal("找不到入口 %s", entry)
	}

	dist := filepath.Join(dir, "dist")
	if err := os.MkdirAll(dist, 0o755); err != nil {
		buildFatal("%v", err)
	}
	out, err := filepath.Abs(filepath.Join(dist, cfg.Name))
	if err != nil {
		buildFatal("%v", err)
	}
	entry, err = filepath.Abs(entry)
	if err != nil {
		buildFatal("%v", err)
	}
	iconIco, err := filepath.Abs(filepath.Join(dir, "desktop", "icon.ico"))
	if err != nil {
		buildFatal("%v", err)
	}
	packagerArgs := []string{
		"run", filepath.ToSlash(filepath.Join(repo, "packager")), entry,
		"--gui", "--name", cfg.Title, "-o", out,
		"--icon", iconIco,
		"--version", cfg.Version,
		"--appid", cfg.AppID,
	}
	if goos == "windows" {
		packagerArgs = append(packagerArgs, "--target", "windows/amd64")
		if cfg.Desktop.IsWindowed() {
			packagerArgs = append(packagerArgs, "--windowed")
		}
	} else {
		iconIcns, err := filepath.Abs(filepath.Join(dir, "desktop", "icon.icns"))
		if err != nil {
			buildFatal("%v", err)
		}
		packagerArgs = append(packagerArgs, "--target", "darwin/arm64")
		packagerArgs = append(packagerArgs, "--icon", iconIcns)
	}
	// go run 必须在仓库模块内执行（packager 依赖 Gox 模块解析）;
	// 传给 packager 的路径全部用绝对路径。
	if err := runStep(repo, "go", packagerArgs...); err != nil {
		buildFatal("jsbuild 打包失败: %v", err)
	}
	if goos == "darwin" {
		fmt.Printf("完成: dist/%s.app\n", cfg.Name)
	} else {
		fmt.Printf("完成: dist/%s（Windows exe, 图标与版本信息已内嵌）\n", cfg.Name)
	}
}

// runStep 运行外部命令, 实时透传输出。把 go 可执行文件所在目录临时加进
// PATH —— gox 二进制自身可能由嵌入式工具链（如 npm 分发包）启动, 父进程
// 环境里不一定有 go。
func runStep(dir, name string, args ...string) error {
	fmt.Printf("==> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if goBin, err := exec.LookPath("go"); err == nil {
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(goBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// goxRepoRoot 定位 Gox 源码仓库: GOX_REPO 环境变量优先, 其次从当前目录
// 逐级向上找带 packager/main.go 的 go.mod 目录。
func goxRepoRoot() string {
	if p := os.Getenv("GOX_REPO"); p != "" && isGoxRepo(p) {
		return p
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for d := dir; ; d = filepath.Dir(d) {
		if isGoxRepo(d) {
			return d
		}
		if parent := filepath.Dir(d); parent == d {
			return ""
		}
	}
}

func isGoxRepo(dir string) bool {
	return fileExists(filepath.Join(dir, "go.mod")) &&
		fileExists(filepath.Join(dir, "packager", "main.go"))
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func buildFatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "gox build: "+format+"\n", args...)
	os.Exit(1)
}
