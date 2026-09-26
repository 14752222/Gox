// certgen 的 round-trip 测试: 生成出来的 keystore/p12 必须能被标准库 +
// pkcs12 解码回读 —— 这是"生成产物可用"的最低门槛（keytool/signtool 等价）。
package certgen

import (
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"encoding/pem"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/pkcs12"
)

func bigOne() *big.Int { return big.NewInt(1) }

func randomReader() io.Reader { return rand.Reader }

// KDF 已知答案向量（取自 x/crypto/pkcs12 的官方测试, 源头是 OpenSSL 行为）。
// 没有它我们无法区分"KDF 错"还是"结构错"。
func TestKDFKnownAnswers(t *testing.T) {
	// 向量 1: 长密钥派生
	salt := []byte("\xff\xff\xff\xff\xff\xff\xff\xff")
	key := pkcs12KDF(bmpString("sesame"), salt, 1, 2048, 24, sha1.New)
	expected := []byte("\x7c\xd9\xfd\x3e\x2b\x3b\xe7\x69\x1a\x44\xe3\xbe\xf0\xf9\xea\x0f\xb9\xb8\x97\xd4\xe3\x25\xd9\xd1")
	if !bytes.Equal(key, expected) {
		t.Fatalf("向量1不匹配:\n got %x\nwant %x", key, expected)
	}

	// 向量 2: 触发 I_j 前导零 (曾让 big.Int 实现踩坑的场景)
	key2 := pkcs12KDF([]byte("\x00\x00"), []byte("\xf3\x7e\x05\xb5\x18\x32\x4b\x4b"), 1, 2048, 24, sha1.New)
	expected2 := []byte("\x00\xf7\x59\xff\x47\xd1\x4d\xd0\x36\x65\xd5\x94\x3c\xb3\xc4\xa3\x9a\x25\x55\xc0\x2a\xed\x66\xe1")
	if !bytes.Equal(key2, expected2) {
		t.Fatalf("向量2不匹配:\n got %x\nwant %x", key2, expected2)
	}
}

func testDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestGenerateAndroidRoundTrip(t *testing.T) {
	dir := testDir(t)
	res, err := GenerateAndroid(dir, Options{CommonName: "demo", Password: "test1234"})
	if err != nil {
		t.Fatalf("生成失败: %v", err)
	}
	if res.Alias != "gox" || res.Password != "test1234" {
		t.Fatalf("alias/password 回传不对: %+v", res)
	}

	data, err := os.ReadFile(filepath.Join(dir, "certs", "android.keystore"))
	if err != nil {
		t.Fatalf("keystore 不存在: %v", err)
	}
	_, cert, err := pkcs12.Decode(data, "test1234")
	if err != nil {
		t.Fatalf("keystore 解码失败（密码或格式错）: %v", err)
	}
	if cert.Subject.CommonName != "demo" {
		t.Fatalf("CN 不对: %s", cert.Subject.CommonName)
	}

	// meta 落盘且 build 流程能读回
	m, err := LoadAndroidMeta(dir)
	if err != nil || m == nil {
		t.Fatalf("meta 缺失或解析失败: %v", err)
	}
	if m.StorePassword != "test1234" || m.Alias != "gox" {
		t.Fatalf("meta 内容不对: %+v", m)
	}
}

func TestGenerateAndroidNoOverwrite(t *testing.T) {
	dir := testDir(t)
	opt := Options{Password: "x12345678"}
	if _, err := GenerateAndroid(dir, opt); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateAndroid(dir, opt); err == nil {
		t.Fatal("缺省必须拒绝覆盖已存在证书")
	}
	if _, err := GenerateAndroid(dir, Options{Password: "x12345678", Force: true}); err != nil {
		t.Fatalf("--force 应允许覆盖: %v", err)
	}
}

func TestGenerateWindowsPFX(t *testing.T) {
	dir := testDir(t)
	res, err := GenerateWindows(dir, Options{Password: "test1234"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "certs", "windows.pfx"))
	if err != nil {
		t.Fatal(err)
	}
	_, cert, err := pkcs12.Decode(data, "test1234")
	if err != nil {
		t.Fatalf("pfx 解码失败: %v", err)
	}
	hasCodeSigning := false
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageCodeSigning {
			hasCodeSigning = true
		}
	}
	if !hasCodeSigning {
		t.Fatal("windows.pfx 必须带 CodeSigning EKU（signtool 才认）")
	}
	_ = res
}

func TestGenerateHarmony(t *testing.T) {
	dir := testDir(t)
	res, err := GenerateHarmony(dir, Options{Password: "test1234"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "certs", "harmony.p12"))
	if err != nil {
		t.Fatal(err)
	}
	_, cert, err := pkcs12.Decode(data, "test1234")
	if err != nil {
		t.Fatalf("harmony.p12 解码失败: %v", err)
	}
	if !cert.IsCA {
		t.Fatal("harmony 调试证书必须是 CA（本地 sign-profile 要签名 Profile）")
	}
	// .cer 落的是 DER, 标准库要能解析
	cerData, err := os.ReadFile(filepath.Join(dir, "certs", "harmony.cer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x509.ParseCertificate(cerData); err != nil {
		t.Fatalf("harmony.cer 解析失败: %v", err)
	}
	_ = res
}

func TestGenerateIOSCSRAndBundle(t *testing.T) {
	dir := testDir(t)
	// 1. CSR 生成
	if _, err := GenerateIOSCSR(dir, Options{CommonName: "dev@example.com"}); err != nil {
		t.Fatal(err)
	}
	csrPEM, err := os.ReadFile(filepath.Join(dir, "certs", "ios.csr"))
	if err != nil {
		t.Fatal(err)
	}
	blk, _ := pem.Decode(csrPEM)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		t.Fatal("ios.csr 不是合法 PEM CSR")
	}
	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		t.Fatalf("CSR 解析失败: %v", err)
	}
	if len(csr.EmailAddresses) != 1 || csr.EmailAddresses[0] != "dev@example.com" {
		t.Fatalf("CSR email 不对: %v", csr.EmailAddresses)
	}

	// 2. 模拟 Apple 签发: 用 CSR 公钥自造一张证书当 .cer, 走 bundle 流程
	// （真实链路里 .cer 来自 Apple; 这里只验证 bundle 的文件编排正确）
	keyPEM, _ := os.ReadFile(filepath.Join(dir, "certs", "ios.key.pem"))
	keyBlk, _ := pem.Decode(keyPEM)
	parsedKey, err := x509.ParsePKCS8PrivateKey(keyBlk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: bigOne()}
	cerDER, err := x509.CreateCertificate(randomReader(), tpl, tpl, csr.PublicKey, parsedKey)
	if err != nil {
		t.Fatal(err)
	}
	cerPath := filepath.Join(dir, "apple.cer")
	if err := os.WriteFile(cerPath, cerDER, 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := BundleIOSP12(dir, cerPath, Options{Password: "test1234"})
	if err != nil {
		t.Fatalf("bundle 失败: %v", err)
	}
	p12Data, err := os.ReadFile(filepath.Join(dir, "certs", "ios.p12"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := pkcs12.Decode(p12Data, "test1234"); err != nil {
		t.Fatalf("ios.p12 解码失败: %v", err)
	}
	_ = res
}

func TestRandomPassword(t *testing.T) {
	p1, _, err := resolvePassword(Options{})
	if err != nil {
		t.Fatal(err)
	}
	p2, _, err := resolvePassword(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(p1) != 16 || p1 == p2 {
		t.Fatalf("随机密码长度/唯一性不对: %q %q", p1, p2)
	}
	if p, _, _ := resolvePassword(Options{Password: "fixed"}); p != "fixed" {
		t.Fatal("显式密码必须原样生效")
	}
}
