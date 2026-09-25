package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFullConfig(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{
		"name": "my-app",
		"title": "我的应用",
		"appId": "com.example.myapp",
		"version": "2.3.1",
		"icon": "custom/icon.png",
		"permissions": [
			"camera",
			{ "name": "location", "desc": "用于查找附近门店" }
		],
		"android": { "minSdk": 26, "adaptiveBackground": "#112233" },
		"ios": { "deploymentTarget": "16.0" },
		"desktop": { "windowed": false }
	}`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Title != "我的应用" || cfg.AppID != "com.example.myapp" || cfg.Version != "2.3.1" {
		t.Errorf("显式字段解析错误: %+v", cfg)
	}
	if cfg.Icon != "custom/icon.png" {
		t.Errorf("icon = %q", cfg.Icon)
	}
	if cfg.Android.MinSDK != 26 {
		t.Errorf("minSdk = %d", cfg.Android.MinSDK)
	}
	if cfg.Android.TargetSDK != DefaultTargetSDK {
		t.Errorf("targetSdk 未补缺省: %d", cfg.Android.TargetSDK)
	}
	if cfg.IOS.DeploymentTarget != "16.0" {
		t.Errorf("deploymentTarget = %q", cfg.IOS.DeploymentTarget)
	}
	if cfg.Desktop.IsWindowed() {
		t.Error("windowed 应为 false")
	}
	perms := cfg.Permissions.List()
	if len(perms) != 2 || perms[0].Name != "camera" || perms[1].Name != "location" {
		t.Errorf("permissions 解析错误: %+v", perms)
	}
	if perms[0].Desc != "" {
		t.Errorf("camera 的默认 desc 应为空: %q", perms[0].Desc)
	}
	if perms[1].Desc != "用于查找附近门店" {
		t.Errorf("location 自定义文案丢失: %q", perms[1].Desc)
	}
}

func TestLoadDefaults(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, `{"name": "demo"}`)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Title != "demo" {
		t.Errorf("title 应缺省取 name: %q", cfg.Title)
	}
	if cfg.AppID != "com.gox.demo" {
		t.Errorf("appId = %q", cfg.AppID)
	}
	if cfg.Version != DefaultVersion {
		t.Errorf("version = %q", cfg.Version)
	}
	if cfg.Icon != DefaultIcon {
		t.Errorf("icon = %q", cfg.Icon)
	}
	if cfg.Android.MinSDK != DefaultMinSDK || cfg.Android.TargetSDK != DefaultTargetSDK {
		t.Errorf("android 缺省错误: %+v", cfg.Android)
	}
	if cfg.IOS.DeploymentTarget != DefaultDeploymentTgt {
		t.Errorf("ios 缺省错误: %+v", cfg.IOS)
	}
	if !cfg.Desktop.IsWindowed() {
		t.Error("desktop.windowed 应缺省为 true")
	}
	// 显式 false 必须被尊重
	dir2 := t.TempDir()
	writeFile(t, dir2, `{"name":"a","desktop":{"windowed":false}}`)
	cfg2, err := Load(dir2)
	if err != nil {
		t.Fatal(err)
	}
	if cfg2.Desktop.IsWindowed() {
		t.Error("显式 windowed=false 不应被缺省覆盖")
	}
	if cfg.Permissions.Len() != 0 {
		t.Errorf("缺省权限应为空（最小权限原则）: %+v", cfg.Permissions.List())
	}
}

func TestPermissionsStringValues(t *testing.T) {
	// 对象形式: 值可以是字符串（直接当文案）
	dir := t.TempDir()
	writeFile(t, dir, `{"name":"a","permissions":{"camera":"用于拍照","microphone":{}}}`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	perms := cfg.Permissions.List()
	if len(perms) != 2 {
		t.Fatalf("应为 2 条: %+v", perms)
	}
	if perms[0].Name != "camera" || perms[0].Desc != "用于拍照" {
		t.Errorf("camera 解析错误: %+v", perms[0])
	}
	if perms[1].Name != "microphone" || perms[1].Desc != "" {
		t.Errorf("microphone 解析错误: %+v", perms[1])
	}
}

func TestPermissionsRoundTrip(t *testing.T) {
	var p Permissions
	if err := json.Unmarshal([]byte(`["camera", {"name":"location","desc":"导航"}]`), &p); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var again Permissions
	if err := json.Unmarshal(data, &again); err != nil {
		t.Fatal(err)
	}
	if !again.Has("camera") || !again.Has("location") {
		t.Errorf("round-trip 丢权限: %s", data)
	}
}

func TestValidateRejectsBadFields(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"空 name", `{"name":""}`},
		{"坏 appId", `{"name":"a","appId":"myapp"}`},
		{"坏 version", `{"name":"a","version":"1.0"}`},
	}
	for _, c := range cases {
		dir := t.TempDir()
		writeFile(t, dir, c.json)
		if _, err := Load(dir); err == nil {
			t.Errorf("%s: 应当校验失败", c.name)
		}
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(t.TempDir()); err == nil {
		t.Fatal("缺 gox.json 应当报错并提示 create")
	}
}

func TestDefaultAppID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"my-app", "com.gox.my_app"},
		{"我的 App", "com.gox.app"},
		{"foo__bar", "com.gox.foo_bar"},
		{"日本語", "com.gox.app"},
	}
	for _, c := range cases {
		if got := DefaultAppID(c.in); got != c.want {
			t.Errorf("DefaultAppID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSaveAndReload(t *testing.T) {
	cfg := Default("demo-app")
	cfg.Permissions.add("camera", "用于拍摄头像")
	dir := t.TempDir()
	if err := cfg.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AppID != cfg.AppID || got.Permissions.List()[0].Desc != "用于拍摄头像" {
		t.Errorf("save/load 不一致: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Errorf("gox.json 未落盘: %v", err)
	}
}

func writeFile(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
