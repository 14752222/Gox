package icongen

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// genGoldenICO 先生成一份 .ico 供 syso 测试复用。
func genGoldenICO(t *testing.T, dir string) string {
	t.Helper()
	src, err := LoadSource(filepath.Join("testdata", "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "icon.ico")
	if err := src.GenerateICO(p); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestBuildWindowsSYSO 核对 COFF 头与 .rsrc 段可解析:
// 资源目录三层可走通、数据条目不越界、VERSIONINFO 签名在数据区里。
func TestBuildWindowsSYSO(t *testing.T) {
	dir := t.TempDir()
	ico := genGoldenICO(t, dir)
	out := filepath.Join(dir, "rsrc_windows_amd64.syso")
	if err := BuildWindowsSYSO(out, ico, "1.2.3", "demo", "演示应用", "amd64"); err != nil {
		t.Fatalf("BuildWindowsSYSO: %v", err)
	}

	f, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	// COFF 头
	if machine := binary.LittleEndian.Uint16(f[0:]); machine != 0x8664 {
		t.Errorf("Machine = %#x, want 0x8664", machine)
	}
	if n := binary.LittleEndian.Uint16(f[2:]); n != 1 {
		t.Errorf("NumberOfSections = %d", n)
	}
	// 段头: 名字必须是 .rsrc
	if string(f[20:25]) != ".rsrc" {
		t.Errorf("段名 = %q", f[20:28])
	}
	rawSize := binary.LittleEndian.Uint32(f[20+16:])
	rawOff := binary.LittleEndian.Uint32(f[20+20:])
	if int(rawOff+rawSize) != len(f) {
		t.Errorf("段数据边界不符: off=%d size=%d file=%d", rawOff, rawSize, len(f))
	}
	sec := f[rawOff:]

	// 根目录: 3 个类型条目（RT_ICON 聚成 1 组 + GROUP_ICON + VERSION）
	rootCount := binary.LittleEndian.Uint16(sec[12:])
	if rootCount != 3 {
		t.Fatalf("根目录条目数 = %d, want 3", rootCount)
	}
	// VERSIONINFO 键名是 UTF-16LE, 直接按编码后的字节找
	if !bytesContains(sec, utf16EncodeStr("VS_VERSION_INFO")) {
		t.Error("资源段里没有 VS_VERSION_INFO")
	}
	// FileVersion 字符串（UTF-16LE 编码的 "1.2.3.0"）必须存在
	want := utf16EncodeStr("1.2.3.0")
	if !bytesContains(sec, want) {
		t.Error("VERSIONINFO 缺少 FileVersion 值 1.2.3.0")
	}
	// 键名 UTF-16LE 存在
	if !bytesContains(sec, utf16EncodeStr("FileDescription")) {
		t.Error("VERSIONINFO 缺少 FileDescription")
	}
	if !bytesContains(sec, utf16EncodeStr("演示应用")) {
		t.Error("VERSIONINFO 缺少中文标题")
	}
}

// TestBuildWindowsSYSODeterministic: 两次生成逐字节一致（可复现构建）。
func TestBuildWindowsSYSODeterministic(t *testing.T) {
	dir := t.TempDir()
	ico := genGoldenICO(t, dir)
	a := filepath.Join(dir, "a.syso")
	b := filepath.Join(dir, "b.syso")
	if err := BuildWindowsSYSO(a, ico, "1.0.0", "demo", "Demo", "amd64"); err != nil {
		t.Fatal(err)
	}
	if err := BuildWindowsSYSO(b, ico, "1.0.0", "demo", "Demo", "amd64"); err != nil {
		t.Fatal(err)
	}
	da, _ := os.ReadFile(a)
	db, _ := os.ReadFile(b)
	if !bytesEqual(da, db) {
		t.Error(".syso 两次生成不一致, 破坏可复现构建")
	}
}

func TestBuildWindowsSYSOBadArch(t *testing.T) {
	dir := t.TempDir()
	ico := genGoldenICO(t, dir)
	if err := BuildWindowsSYSO(filepath.Join(dir, "x.syso"), ico, "1.0.0", "d", "d", "riscv"); err == nil {
		t.Fatal("不支持的 GOARCH 应当报错")
	}
}

// TestBuildMacAppBundle 核对 .app 结构与 Info.plist 内容。
func TestBuildMacAppBundle(t *testing.T) {
	dir := t.TempDir()
	src, err := LoadSource(filepath.Join("testdata", "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	icns := filepath.Join(dir, "icon.icns")
	if err := src.GenerateICNS(icns); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "app-bin")
	if err := os.WriteFile(bin, []byte("fake-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	appDir, err := BuildMacAppBundle(bin, filepath.Join(dir, "Demo"), "demo", "演示", "com.gox.demo", "1.0.0", icns)
	if err != nil {
		t.Fatalf("BuildMacAppBundle: %v", err)
	}
	if !strings.HasSuffix(appDir, "Demo.app") {
		t.Errorf("bundle 路径 = %s", appDir)
	}
	for _, rel := range []string{"Contents/MacOS/demo", "Contents/Info.plist", "Contents/PkgInfo", "Contents/Resources/AppIcon.icns"} {
		if _, err := os.Stat(filepath.Join(appDir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("缺少 %s: %v", rel, err)
		}
	}
	info, _ := os.ReadFile(filepath.Join(appDir, "Contents", "Info.plist"))
	for _, want := range []string{"<string>com.gox.demo</string>", "<string>1.0.0</string>", "<string>演示</string>"} {
		if !strings.Contains(string(info), want) {
			t.Errorf("Info.plist 缺少 %s:\n%s", want, info)
		}
	}
	pkg, _ := os.ReadFile(filepath.Join(appDir, "Contents", "PkgInfo"))
	if string(pkg) != "APPL????" {
		t.Errorf("PkgInfo = %q", pkg)
	}
}

// ---- 小工具 ----

func utf16EncodeStr(s string) []byte {
	out := make([]byte, 0, len(s)*2+2)
	for _, r := range s + "\x00" {
		out = binary.LittleEndian.AppendUint16(out, uint16(r))
	}
	return out
}

func bytesContains(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if bytesEqual(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
