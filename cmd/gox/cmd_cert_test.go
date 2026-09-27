// cmd_cert / ensureAndroidSigning 的行为测试: 快捷生成与 build 签名接入。
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14752222/Gox/config"
)

func TestRunCertAndroidSmoke(t *testing.T) {
	dir := t.TempDir()
	// runCert 成功路径只打印不退出; 失败路径 os.Exit 会直接终止测试进程,
	// 所以这里只覆盖成功分支（失败分支的逻辑都在 certgen 测过）。
	runCert([]string{"android", dir, "--password", "test1234", "--cn", "demo"})
	for _, f := range []string{"certs/android.keystore", "certs/android-cert.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("%s 未生成: %v", f, err)
		}
	}
}

func TestEnsureAndroidSigningAutoGenerate(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default("demo")
	if err := ensureAndroidSigning(dir, cfg); err != nil {
		t.Fatalf("自动生成签名失败: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "android", "keystore.properties"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, want := range []string{"storeFile=", "storePassword=", "keyAlias=gox", "keyPassword="} {
		if !strings.Contains(s, want) {
			t.Fatalf("keystore.properties 缺字段 %s:\n%s", want, s)
		}
	}
	// keystore 文件必须真实存在
	line := ""
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "storeFile=") {
			line = strings.TrimPrefix(l, "storeFile=")
		}
	}
	if _, err := os.Stat(line); err != nil {
		t.Fatalf("storeFile 指向的 keystore 不存在: %s", line)
	}
}

func TestEnsureAndroidSigningUserKeystore(t *testing.T) {
	dir := t.TempDir()
	// 自备 keystore: 文件不存在必须报错, 不能静默跳过
	cfg := config.Default("demo")
	cfg.Cert.Android = &config.AndroidSigning{
		Keystore:      "my.jks",
		Alias:         "mine",
		StorePassword: "p1",
	}
	if err := ensureAndroidSigning(dir, cfg); err == nil {
		t.Fatal("keystore 不存在时应报错")
	}
	if err := os.WriteFile(filepath.Join(dir, "my.jks"), []byte("dummy"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAndroidSigning(dir, cfg); err != nil {
		t.Fatalf("自备 keystore 配置失败: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "android", "keystore.properties"))
	s := string(data)
	if !strings.Contains(s, "keyAlias=mine") || !strings.Contains(s, "keyPassword=p1") {
		t.Fatalf("自备 keystore 的字段没写对:\n%s", s)
	}
}
