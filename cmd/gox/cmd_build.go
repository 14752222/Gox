// cmd_build.go 实现 `gox build <target>`: 统一构建入口。
//
// 流程: sync（权限注入）→ icon（图标生成）→ 平台打包:
//
//	android: 交叉编译 libgox.so（scripts/build-android.sh）→ 尝试 gradle assembleDebug
//	ios:     交叉编译 libgox.a → xcodebuild 构建壳工程 → 组装 dist/<name>.app
//	         （scripts/build-ios.sh, 支持 --device/--simulator/--arch/--entry）
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

	"github.com/14752222/Gox/certgen"
	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/icongen"
)

func runBuild(args []string) {
	var target, dir string
	// iOS 专属参数（其他目标忽略, 便于脚本里统一写 gox build <t> ...）
	var iosTarget, iosArch, iosEntry string
	// release 目前只对 android 生效（assembleRelease, 需签名; 见 buildAndroid）
	release := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox build <android|ios|windows|macos> [目录] [选项]

统一构建入口: 先 sync（权限注入）与 icon（图标生成）, 再做平台打包。
  android  libgox.so（NDK 交叉编译）+ gradle assembleDebug（有工具链时）
  ios      libgox.a（Xcode 交叉编译）+ xcodebuild 壳工程 → dist/<name>.app
  windows  桌面 exe（图标与版本信息内嵌）
  macos    .app bundle（可执行 + Info.plist + 图标）

android 选项:
  --release        打 release 包（assembleRelease; 签名自动接入, 见 gox cert）

ios 选项:
  --simulator    构建模拟器包（缺省, 免签名, 可直接 simctl install）
  --device       构建真机包（需要签名证书, 无证书时给出配置指引）
  --arch <列表>  模拟器架构, 逗号分隔（如 arm64,x86_64 合成 fat 库; 缺省 arm64）
  --entry <js>   打进 .app 的入口脚本（缺省 src/main.js; iOS 壳只支持单文件入口,
                 可 import 内置 gx/* 模块, 不能 import 相对路径文件）

macos 选项:
  --arch <arch>  cpu 架构: arm64（缺省, Apple Silicon）/ amd64（Intel）/
                 universal（fat 二进制, 同时支持两种 Mac）

桌面打包需要 Gox 源码仓库（自动向上查找, 或设 GOX_REPO 指定）。
`)
			return
		case a == "--device":
			iosTarget = "device"
		case a == "--simulator":
			iosTarget = "simulator"
		case a == "--release":
			release = true
		case a == "--arch":
			if i+1 >= len(args) {
				buildFatal("--arch 需要参数（如 arm64 或 arm64,x86_64）")
			}
			i++
			iosArch = args[i]
		case a == "--entry":
			if i+1 >= len(args) {
				buildFatal("--entry 需要参数（入口 .js 路径）")
			}
			i++
			iosEntry = args[i]
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
		buildAndroid(dir, cfg, release)
	case "ios":
		if iosTarget == "" {
			iosTarget = "simulator"
		}
		buildIOS(dir, cfg, iosTarget, iosArch, iosEntry)
	case "windows":
		buildDesktop(dir, cfg, "windows", "")
	case "macos":
		buildDesktop(dir, cfg, "darwin", iosArch)
	}
}

// buildAndroid: libgox.so → jniLibs → gradle（有工具链时）。
// release=true 走 assembleRelease（需要签名: 优先 gox.json cert 段的自备证书,
// 其次 certs/ 下 gox cert 生成的证书, 都没有就自动生成调试证书 —— 见 ensureAndroidSigning）。
func buildAndroid(dir string, cfg config.Config, release bool) {
	if err := ensureAndroidSigning(dir, cfg); err != nil {
		buildFatal("gox build android: 签名配置失败: %v", err)
	}
	repo := goxRepoRoot()
	if repo == "" {
		fmt.Fprintln(os.Stderr, "gox build android: 找不到 Gox 源码仓库（设 GOX_REPO 或在仓库内运行）, 跳过 libgox.so 交叉编译")
	} else if err := runStep(dir, "bash", filepath.Join(repo, "scripts", "build-android.sh")); err != nil {
		fmt.Fprintf(os.Stderr, "gox build android: libgox.so 交叉编译失败: %v（检查 NDK 配置）\n", err)
	}

	// gradle: 项目里有 wrapper 或系统 gradle 时执行; 否则给出明确指引
	task := "assembleDebug"
	outKind := "debug"
	if release {
		task = "assembleRelease"
		outKind = "release"
	}
	gradleArgs := []string{task}
	if _, err := os.Stat(filepath.Join(dir, "android", "gradlew")); err == nil {
		if err := runStep(filepath.Join(dir, "android"), "./gradlew", gradleArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "gox build android: gradle 构建失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("完成: android/app/build/outputs/apk/%s/\n", outKind)
		return
	}
	if _, err := exec.LookPath("gradle"); err == nil {
		if err := runStep(filepath.Join(dir, "android"), "gradle", gradleArgs...); err != nil {
			fmt.Fprintf(os.Stderr, "gox build android: gradle 构建失败: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("完成: android/app/build/outputs/apk/%s/\n", outKind)
		return
	}
	fmt.Println("未检测到 gradle —— APK 构建留给用户执行:")
	fmt.Printf("  cd android && gradle %s\n", task)
	fmt.Println("真机验收步骤见 docs/platform-config.md（Phase 5 验收清单）")
}

// ensureAndroidSigning 决定签名来源并落 android/keystore.properties（gradle
// 模板读它给 release 签名）。优先级:
//  1. gox.json cert.android —— 用户自备 keystore（JKS/PKCS12 均可）
//  2. certs/android-cert.json —— gox cert 生成的证书元数据
//  3. 都没有 → 自动生成调试证书（快捷操作, 对标 Android debug.keystore 体验;
//     正式上架前记得换成自有 keystore, 调试证书也能发商店但换证书即断代）
func ensureAndroidSigning(dir string, cfg config.Config) error {
	var props [][2]string // 保持固定写入顺序, diff 友好
	if s := cfg.Cert.Android; s != nil {
		if s.Keystore == "" || s.Alias == "" || s.StorePassword == "" {
			return fmt.Errorf("gox.json 的 cert.android 配置不完整: keystore / alias / storePassword 必填")
		}
		ks := s.Keystore
		if !filepath.IsAbs(ks) {
			ks = filepath.Join(dir, ks)
		}
		if _, err := os.Stat(ks); err != nil {
			return fmt.Errorf("cert.android.keystore 不可读: %s", ks)
		}
		keyPwd := s.KeyPassword
		if keyPwd == "" {
			keyPwd = s.StorePassword
		}
		props = [][2]string{
			{"storeFile", ks}, {"storePassword", s.StorePassword},
			{"keyAlias", s.Alias}, {"keyPassword", keyPwd},
		}
	} else {
		meta, err := certgen.LoadAndroidMeta(dir)
		if err != nil {
			return err
		}
		if meta == nil {
			fmt.Println("==> cert: 未配置签名, 自动生成调试证书（自定义: gox cert android）")
			if _, err := certgen.GenerateAndroid(dir, certgen.Options{CommonName: cfg.Name}); err != nil {
				return err
			}
			if meta, err = certgen.LoadAndroidMeta(dir); err != nil || meta == nil {
				return fmt.Errorf("读取 certs/android-cert.json 失败")
			}
		}
		ks := meta.File
		if !filepath.IsAbs(ks) {
			ks = filepath.Join(dir, ks)
		}
		if _, err := os.Stat(ks); err != nil {
			return fmt.Errorf("keystore 文件丢失: %s（删除 certs/android-cert.json 后重跑可重新生成）", ks)
		}
		props = [][2]string{
			{"storeFile", ks}, {"storePassword", meta.StorePassword},
			{"keyAlias", meta.Alias}, {"keyPassword", meta.KeyPassword},
		}
	}

	var b strings.Builder
	b.WriteString("# 由 gox build 自动生成（含密码, 严禁提交仓库, 已 gitignore）。\n")
	b.WriteString("# 改签名: gox.json 的 cert 段（自备证书）或 gox cert android（快捷生成）。\n")
	for _, kv := range props {
		fmt.Fprintf(&b, "%s=%s\n", kv[0], kv[1])
	}
	// 写进 android/（gradle 模块根）—— file("keystore.properties") 相对模块目录解析
	path := filepath.Join(dir, "android", "keystore.properties")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// buildIOS: libgox.a 交叉编译 → xcodebuild 构建壳工程 → 组装 dist/<name>.app。
// 全部由 scripts/build-ios.sh 承担, 这里只做参数透传与产物路径确认。
//
// 目标选择: --simulator（缺省, 免签名）/ --device（需签名证书, 无证书时脚本给指引）。
// 壳工程是仓库里的 app/ios/Gox.xcodeproj; 用户工程的 gox.json / ios/Info.plist /
// AppIconSet / 入口脚本会在打包最后一步合并进产物（见 build-ios.sh 尾段）。
func buildIOS(dir string, cfg config.Config, target, arch, entry string) {
	repo := goxRepoRoot()
	if repo == "" {
		buildFatal("gox build ios 需要 Gox 源码仓库（壳工程与交叉编译都在仓库内）—— 设 GOX_REPO 环境变量或在仓库内运行")
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		buildFatal("%v", err)
	}
	env := []string{
		"GOX_PROJECT_DIR=" + absDir,
		"GOX_IOS_TARGET=" + target,
	}
	if arch != "" {
		env = append(env, "GOX_IOS_ARCH="+arch)
	}
	if entry != "" {
		if !filepath.IsAbs(entry) {
			entry = filepath.Join(absDir, entry)
		}
		if !fileExists(entry) {
			buildFatal("入口脚本不存在: %s", entry)
		}
		env = append(env, "GOX_ENTRY="+entry)
	}
	script := filepath.Join(repo, "scripts", "build-ios.sh")
	if err := runStepEnv(dir, env, "bash", script); err != nil {
		buildFatal("iOS 打包失败: %v", err)
	}
	fmt.Printf("完成: dist/%s.app\n", cfg.Name)
	fmt.Println("模拟器安装: xcrun simctl boot <设备> && xcrun simctl install booted dist/" + cfg.Name + ".app")
}

// buildDesktop: 走 jsbuild（packager）, windows 内嵌图标/版本, darwin 出 .app。
// darwin 的 arch 支持 arm64（缺省）/ amd64 / universal（lipo 合并 fat 二进制）。
func buildDesktop(dir string, cfg config.Config, goos, arch string) {
	if goos == "darwin" {
		switch arch {
		case "", "arm64", "amd64", "universal":
		default:
			buildFatal("--arch 仅支持 arm64|amd64|universal（macos）")
		}
	}
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
	packagerArgs := []string{
		"run", filepath.ToSlash(filepath.Join(repo, "packager")), entry,
		"--gui", "--name", cfg.Title, "-o", out,
		"--version", cfg.Version,
		"--appid", cfg.AppID,
	}
	if goos == "windows" {
		iconIco, err := filepath.Abs(filepath.Join(dir, "desktop", "icon.ico"))
		if err != nil {
			buildFatal("%v", err)
		}
		packagerArgs = append(packagerArgs, "--icon", iconIco, "--target", "windows/amd64")
		if cfg.Desktop.IsWindowed() {
			packagerArgs = append(packagerArgs, "--windowed")
		}
	} else {
		iconIcns, err := filepath.Abs(filepath.Join(dir, "desktop", "icon.icns"))
		if err != nil {
			buildFatal("%v", err)
		}
		if arch == "" {
			arch = "arm64"
		}
		packagerArgs = append(packagerArgs, "--icon", iconIcns, "--target", "darwin/"+arch)
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
	return runStepEnv(dir, nil, name, args...)
}

// runStepEnv 同 runStep, 额外注入 env 环境变量（KEY=VALUE 形式）。
func runStepEnv(dir string, env []string, name string, args ...string) error {
	fmt.Printf("==> %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if goBin, err := exec.LookPath("go"); err == nil {
		cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(goBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	cmd.Env = append(cmd.Env, env...)
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
