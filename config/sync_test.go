package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// syncTestDir 造一个带最小清单文件的项目目录（照抄 scaffold 模板里的关键结构）。
func syncTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	manifest := `<?xml version="1.0" encoding="utf-8"?>
<manifest xmlns:android="http://schemas.android.com/apk/res/android">

    <!--GOX:PERMISSIONS:START-->
    <uses-permission android:name="android.permission.INTERNET" />
    <!--GOX:PERMISSIONS:END-->

    <application android:label="demo" />
</manifest>
`
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>com.gox.demo</string>
	<!--GOX:USAGE:START-->
	<!--GOX:USAGE:END-->
	<key>UILaunchScreen</key>
	<dict/>
</dict>
</plist>
`
	mk(t, dir, "android/AndroidManifest.xml", manifest)
	mk(t, dir, "ios/Info.plist", plist)
	return dir
}

func mk(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSyncInjectsPermissions(t *testing.T) {
	dir := syncTestDir(t)
	cfg := Default("demo")
	// 用 add 直接种条目（模拟解析结果）
	cfg.Permissions.add("camera", "用于拍摄头像")
	cfg.Permissions.add("location", "")

	if _, err := Sync(dir, cfg); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	m := readFile(t, filepath.Join(dir, "android", "AndroidManifest.xml"))
	for _, want := range []string{
		"android.permission.INTERNET",
		"android.permission.CAMERA",
		"android.permission.ACCESS_FINE_LOCATION",
		"android.permission.ACCESS_COARSE_LOCATION",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("AndroidManifest 缺少 %s:\n%s", want, m)
		}
	}

	p := readFile(t, filepath.Join(dir, "ios", "Info.plist"))
	if !strings.Contains(p, "<key>NSCameraUsageDescription</key>") {
		t.Errorf("Info.plist 缺少相机用途键:\n%s", p)
	}
	if !strings.Contains(p, "<string>用于拍摄头像</string>") {
		t.Errorf("自定义用途文案丢失:\n%s", p)
	}
	if !strings.Contains(p, "<string>需要获取您的位置信息以提供位置相关功能。</string>") {
		t.Errorf("location 应有默认兜底文案:\n%s", p)
	}
}

func TestSyncIsIdempotent(t *testing.T) {
	// 连跑两次, 第二次文件内容必须一字不差 —— 否则 CI 里反复 sync 会
	// 把 diff 撑爆（这正是"整块替换"要防的事故）。
	dir := syncTestDir(t)
	cfg := Default("demo")
	cfg.Permissions.add("microphone", "")

	if _, err := Sync(dir, cfg); err != nil {
		t.Fatal(err)
	}
	m1 := readFile(t, filepath.Join(dir, "android", "AndroidManifest.xml"))
	p1 := readFile(t, filepath.Join(dir, "ios", "Info.plist"))

	if _, err := Sync(dir, cfg); err != nil {
		t.Fatal(err)
	}
	if m2 := readFile(t, filepath.Join(dir, "android", "AndroidManifest.xml")); m1 != m2 {
		t.Errorf("第二次 sync 改动了 AndroidManifest:\n--- 第一次\n%s\n--- 第二次\n%s", m1, m2)
	}
	if p2 := readFile(t, filepath.Join(dir, "ios", "Info.plist")); p1 != p2 {
		t.Errorf("第二次 sync 改动了 Info.plist:\n--- 第一次\n%s\n--- 第二次\n%s", p1, p2)
	}
}

func TestSyncShrinksBlock(t *testing.T) {
	// 先声明 camera, 再删掉 —— 区块必须收回去, 不能残留旧权限。
	dir := syncTestDir(t)
	cfg := Default("demo")
	cfg.Permissions.add("camera", "")
	if _, err := Sync(dir, cfg); err != nil {
		t.Fatal(err)
	}
	cfg = Default("demo") // 权限清空
	if _, err := Sync(dir, cfg); err != nil {
		t.Fatal(err)
	}
	m := readFile(t, filepath.Join(dir, "android", "AndroidManifest.xml"))
	if strings.Contains(m, "CAMERA") {
		t.Errorf("已删除的权限残留在清单里:\n%s", m)
	}
	if !strings.Contains(m, "INTERNET") {
		t.Error("INTERNET 是最小权限基线, 必须始终存在")
	}
	p := readFile(t, filepath.Join(dir, "ios", "Info.plist"))
	if strings.Contains(p, "NSCameraUsageDescription") {
		t.Errorf("已删除的权限残留在 Info.plist 里:\n%s", p)
	}
}

func TestSyncUnknownPermissionFails(t *testing.T) {
	dir := syncTestDir(t)
	cfg := Default("demo")
	cfg.Permissions.add("telepathy", "")
	if _, err := Sync(dir, cfg); err == nil {
		t.Fatal("未知权限应当报错而不是静默跳过")
	}
}

func TestSyncRefusesMissingBlock(t *testing.T) {
	// 用户把标记删了 → 必须报错, 不能悄悄追加生成坏 XML。
	dir := t.TempDir()
	mk(t, dir, "android/AndroidManifest.xml",
		`<manifest xmlns:android="http://schemas.android.com/apk/res/android">
    <application />
</manifest>`)
	mk(t, dir, "ios/Info.plist", "<dict/>")
	cfg := Default("demo")
	if _, err := Sync(dir, cfg); err == nil {
		t.Fatal("缺标记区块应当报错")
	}
}

func TestSyncMinimalDefault(t *testing.T) {
	// 默认配置（零权限）: Android 只有 INTERNET, iOS 区块保持为空。
	dir := syncTestDir(t)
	if _, err := Sync(dir, Default("demo")); err != nil {
		t.Fatal(err)
	}
	m := readFile(t, filepath.Join(dir, "android", "AndroidManifest.xml"))
	if strings.Count(m, "<uses-permission") != 1 || !strings.Contains(m, "INTERNET") {
		t.Errorf("默认权限应只有 INTERNET:\n%s", m)
	}
	p := readFile(t, filepath.Join(dir, "ios", "Info.plist"))
	if strings.Contains(p, "UsageDescription") {
		t.Errorf("默认配置不应注入任何 iOS 用途键:\n%s", p)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
