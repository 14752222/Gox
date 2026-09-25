package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/compiler"
	"github.com/14752222/Gox/lexer"
	"github.com/14752222/Gox/parser"
)

// wantFiles 是脚手架**默认输出**的完整清单。
//
// 它是"目录布局与脚手架默认输出一致"这句话的凭据: 改模板就必须同步改这里，
// 多一个文件少一个文件都会红 —— 否则"默认布局"会随着模板改动悄悄漂移，
// 而用户是照 README 里的目录树去理解工程的。
//
// 顺序与 Create 的返回一致（按路径升序）。
var wantFiles = []string{
	".gitignore",
	"README.md",
	"android/AndroidManifest.xml",
	"android/build.gradle.kts",
	"android/res/mipmap-anydpi-v26/ic_launcher.xml",
	"android/res/mipmap-anydpi-v26/ic_launcher_round.xml",
	"android/res/mipmap-hdpi/ic_launcher.png",
	"android/res/mipmap-hdpi/ic_launcher_foreground.png",
	"android/res/mipmap-mdpi/ic_launcher.png",
	"android/res/mipmap-mdpi/ic_launcher_foreground.png",
	"android/res/mipmap-xhdpi/ic_launcher.png",
	"android/res/mipmap-xhdpi/ic_launcher_foreground.png",
	"android/res/mipmap-xxhdpi/ic_launcher.png",
	"android/res/mipmap-xxhdpi/ic_launcher_foreground.png",
	"android/res/mipmap-xxxhdpi/ic_launcher.png",
	"android/res/mipmap-xxxhdpi/ic_launcher_foreground.png",
	"android/res/values/colors.xml",
	"android/res/values/strings.xml",
	"assets/icon.png",
	"desktop/Info.plist",
	"desktop/icon.icns",
	"desktop/icon.ico",
	"favicon.png",
	"gox.json",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-120.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-152.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-167.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-180.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-40.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-58.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-60.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-80.png",
	"ios/Assets.xcassets/AppIcon.appiconset/AppIcon-87.png",
	"ios/Assets.xcassets/AppIcon.appiconset/Contents.json",
	"ios/Assets.xcassets/Contents.json",
	"ios/Info.plist",
	"package.json",
	"src/app.js",
	"src/components/counter.js",
	"src/components/status-bar.js",
	"src/components/todo-list.js",
	"src/main.js",
	"src/store.js",
	"src/theme.js",
}

func TestCreateLayout(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-app")

	files, err := Create(Options{Dir: dir})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, f.Path)
		if f.Bytes == 0 {
			t.Errorf("%s 是空文件", f.Path)
		}
	}
	if strings.Join(got, ",") != strings.Join(wantFiles, ",") {
		t.Fatalf("返回的文件清单不符:\n got %v\nwant %v", got, wantFiles)
	}

	// 落盘结果必须与返回的清单一致（WriteFile 的路径拼错时只有这里有感知）
	onDisk := map[string]bool{}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(dir, p)
		if relErr != nil {
			return relErr
		}
		onDisk[filepath.ToSlash(rel)] = true
		return nil
	})
	if err != nil {
		t.Fatalf("遍历生成结果: %v", err)
	}
	if len(onDisk) != len(wantFiles) {
		t.Fatalf("磁盘上的文件数 %d != 期望 %d: %v", len(onDisk), len(wantFiles), onDisk)
	}
	for _, want := range wantFiles {
		if !onDisk[want] {
			t.Errorf("缺少文件 %s", want)
		}
	}
}

// TestCreateReplacesPlaceholders 断言两个占位符都真的被替换掉了 ——
// 漏替换的后果是用户拿到一个窗口标题写着 __PROJECT_TITLE__ 的工程（不报错）。
func TestCreateReplacesPlaceholders(t *testing.T) {
	root := t.TempDir()

	// 普通名: 包名与标题一致
	plain := filepath.Join(root, "my-app")
	if _, err := Create(Options{Dir: plain}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	txt := readAll(t, plain)
	if strings.Contains(txt, "__PROJECT_") {
		t.Errorf("生成结果里残留占位符:\n%s", excerpt(txt, "__PROJECT_"))
	}
	if strings.Contains(txt, "__APP_ID__") || strings.Contains(txt, "__VERSION__") {
		t.Errorf("平台骨架占位符未替换:\n%s", excerpt(txt, "__APP_ID__"))
	}
	if !strings.Contains(txt, `"name": "my-app"`) {
		t.Errorf("package.json 的 name 不是 my-app")
	}
	if !strings.Contains(txt, `title="my-app"`) {
		t.Errorf("入口的窗口标题不是 my-app")
	}
	// gox.json / gradle / iOS plist 三处必须与占位符推导一致
	gj := readFile(t, filepath.Join(plain, "gox.json"))
	if !strings.Contains(gj, `"appId": "com.gox.my_app"`) || !strings.Contains(gj, `"version": "1.0.0"`) {
		t.Errorf("gox.json 的 appId/version 不符:\n%s", gj)
	}
	gradle := readFile(t, filepath.Join(plain, "android", "build.gradle.kts"))
	if !strings.Contains(gradle, `applicationId = "com.gox.my_app"`) || !strings.Contains(gradle, `versionName = "1.0.0"`) {
		t.Errorf("build.gradle.kts 的 applicationId/versionName 不符:\n%s", gradle)
	}
	plist := readFile(t, filepath.Join(plain, "ios", "Info.plist"))
	if !strings.Contains(plist, "<string>com.gox.my_app</string>") || !strings.Contains(plist, "<string>1.0.0</string>") {
		t.Errorf("Info.plist 的 BundleID/版本不符:\n%s", plist)
	}
	// 二进制资产必须真的存在且非空（占位符替换不能破坏 PNG）
	for _, bin := range []string{"assets/icon.png", "desktop/icon.ico", "desktop/icon.icns", "favicon.png"} {
		if data, err := os.ReadFile(filepath.Join(plain, filepath.FromSlash(bin))); err != nil || len(data) == 0 {
			t.Errorf("二进制资产 %s 缺失或为空: %v", bin, err)
		}
	}

	// 中文 + 大写目录名: package.json 必须被收敛成合法包名，标题保持原样
	cjk := filepath.Join(root, "我的 App")
	if _, err := Create(Options{Dir: cjk}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	pkg := readFile(t, filepath.Join(cjk, "package.json"))
	if !strings.Contains(pkg, `"name": "app"`) {
		t.Errorf("中文目录名的包名没被收敛成合法值:\n%s", pkg)
	}
	if !strings.Contains(readFile(t, filepath.Join(cjk, "src", "main.js")), `title="我的 App"`) {
		t.Errorf("窗口标题应保留用户给的原样名字")
	}

	// --name 覆盖目录名
	named := filepath.Join(root, "whatever")
	if _, err := Create(Options{Dir: named, Name: "Cool App"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if pkg := readFile(t, filepath.Join(named, "package.json")); !strings.Contains(pkg, `"name": "cool-app"`) {
		t.Errorf("--name 没有生效或被错误收敛:\n%s", pkg)
	}
}

// TestGeneratedScriptsCompile 是最重要的一条: 模板里的 JS 必须过
// lexer → parser → compiler 全链路。
//
// 模板是**独立文件**而不是 Go 字符串常量（见 scaffold.go 的包注释），代价是
// 编译器不会检查它们 —— 一次手滑（少个括号、`<view>` 写错）在用户那侧是
// "创建出来的工程跑不起来"，而在这里只是几毫秒的用例。
func TestGeneratedScriptsCompile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if _, err := Create(Options{Dir: dir}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	count := 0
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".js") {
			return err
		}
		count++
		src := readFile(t, p)
		rel, _ := filepath.Rel(dir, p)

		l := lexer.New(src)
		pr := parser.New(l)
		program := pr.ParseProgram()
		if pr.Errors().HasErrors() {
			t.Errorf("%s 解析失败:\n%s", rel, pr.Errors().String())
			return nil
		}
		c := compiler.New()
		if err := c.Compile(program); err != nil {
			t.Errorf("%s 编译失败: %v", rel, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("遍历: %v", err)
	}
	if count != 7 {
		t.Errorf("只检查到 %d 个 .js，模板里的 JS 文件数不对（期望 7）", count)
	}
}

func TestCreateRefusesNonEmptyDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	kept := filepath.Join(dir, "precious.txt")
	if err := os.WriteFile(kept, []byte("do not clobber me"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Create(Options{Dir: dir}); err == nil {
		t.Fatal("非空目录应当报错，否则会在已有工程里覆盖用户代码")
	}
	// 报错之后不能留下半成品
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err == nil {
		t.Error("拒绝生成时不应写出任何文件")
	}

	if _, err := Create(Options{Dir: dir, Force: true}); err != nil {
		t.Fatalf("--force 应当放行: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Errorf("--force 没有写入模板: %v", err)
	}
	if readFile(t, kept) != "do not clobber me" {
		t.Error("--force 只该覆盖同名文件，不该清理目录里的其它文件")
	}
}

func TestCreateRejectsUnnamedTarget(t *testing.T) {
	// 目标目录没有可用的基名 ⇒ 报错而不是生成一个叫 "." 的工程
	if _, err := Create(Options{Dir: "."}); err == nil {
		t.Error("以当前目录为目标且未给 --name 时应当报错")
	}
	if _, err := Create(Options{Dir: ""}); err == nil {
		t.Error("空目录应当报错")
	}
}

func TestNpmSafeName(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-app", "my-app"},
		{"My App", "my-app"},
		{"我的 App", "app"},
		{"foo__bar", "foo-bar"},
		{"--weird--", "weird"},
		{"日本語", "gox-app"},
		{"v2.0", "v2-0"},
	}
	for _, c := range cases {
		if got := npmSafeName(c.in); got != c.want {
			t.Errorf("npmSafeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// readAll 把一棵树里的所有文件按路径拼成一个大字符串（用于"有没有残留占位符"）。
func readAll(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		sb.Write(data)
		sb.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatalf("读取 %s: %v", dir, err)
	}
	return sb.String()
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("读取 %s: %v", p, err)
	}
	return string(data)
}

// excerpt 取 needle 附近的一小段，便于失败时看清残留的是什么。
func excerpt(s, needle string) string {
	i := strings.Index(s, needle)
	if i < 0 {
		return "(未找到)"
	}
	end := i + 60
	if end > len(s) {
		end = len(s)
	}
	return s[i:end]
}
