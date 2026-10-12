// Package certgen 为 `gox cert` 提供纯 Go 的证书生成能力。
//
// 设计目标: 用户 `gox build` 完不需要再装 JDK keytool / openssl / hap-sign-tool
// 任何外部工具链 —— 密钥、自签证书、PKCS12 打包全部在本包内完成
//（crypto/x509 生成 + golang.org/x/crypto/pkcs12 编码）。
//
// v1 覆盖四类产物（对应 `gox cert <platform>`）:
//
//	android: certs/android.keystore   PKCS12 (RSA 2048, 自签 30 年)
//	         —— Gradle/AGP 原生支持 PKCS12, keytool list 也能读。
//	windows: certs/windows.pfx        自签代码签名证书 (EKU CodeSigning)
//	         —— 本机测试/内网分发; 正式分发要买 EV 证书, 否则 SmartScreen 拦。
//	harmony: certs/harmony.p12 + harmony.cer
//	         —— ECC P-256 自签 CA 证书, 可用 hap-sign-tool 本地 sign-profile
//	         调试; 正式上架必须换 AGC 签发的发布证书, 这里只解决"调试能装"。
//	ios:     certs/ios.key.pem + ios.csr
//	         —— iOS 证书只能 Apple 签发, 我们生成密钥对 + CSR, 用户拿 CSR 去
//	         developer.apple.com 换 .cer 后, 用 gox cert ios --cer 换发 .p12。
//
// 安全边界（v1 明确不做）: 密码支持随机生成并落盘到 certs/<platform>-cert.json,
// 该目录必须 gitignore（scaffold 模板已带）。工具不隐藏密码的目的是让 CI/gradle
// 能自动读到 —— 对开发调试证书这是可接受的取舍, 正式证书请用户自备并在 gox.json
// 的 cert 段引用（密码走环境变量, 不入库）。
package certgen

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"

)

// OutDirName 是各平台证书的缺省输出目录（相对项目根）。
const OutDirName = "certs"

// Options 控制一次证书生成。零值可用 —— 全部字段有缺省逻辑。
type Options struct {
	// CommonName 是证书主体 CN。缺省用项目名（调用方传入）。
	CommonName string
	// Org 是证书主体 O。缺省 "Gox"。
	Org string
	// Alias 是 keystore 内的 key 别名（android/harmony）。缺省 "gox"。
	Alias string
	// Password 是 keystore/pfx 密码。空则随机生成 16 位并写入 meta json。
	Password string
	// Years 是证书有效期（年）。0 取各平台缺省（android 30, 其余 3）。
	Years int
	// OutDir 是输出目录（相对项目根）。空取 "certs"。
	OutDir string
	// Force 为 true 时允许覆盖已存在的证书文件。缺省拒绝 —— 防止手滑把
	// 已经绑到应用签名的证书重置掉（安卓换签名 = 应用无法覆盖安装）。
	Force bool
}

// Result 是一次生成的产出报告。
type Result struct {
	// Files 是生成的文件路径（相对项目根）。
	Files []string
	// Alias 是 keystore 别名（android/harmony 有意义）。
	Alias string
	// Password 是最终生效的密码（随机生成时回传给调用方展示/落盘）。
	Password string
	// Note 是给人看的后续操作提示（如 iOS 要拿 CSR 去 Apple 换证书）。
	Note string
}

// meta 是落盘到 certs/<platform>-cert.json 的元数据。
// 存在的意义: `gox build android` 要能不问用户就拿到别名/密码拼 keystore.properties。
type meta struct {
	Platform      string `json:"platform"`
	File          string `json:"file"`
	Alias         string `json:"alias,omitempty"`
	StorePassword string `json:"storePassword"`
	KeyPassword   string `json:"keyPassword,omitempty"`
	CommonName    string `json:"cn,omitempty"`
	Org           string `json:"org,omitempty"`
	NotAfter      string `json:"notAfter,omitempty"`
}

// AndroidMeta 是 android-cert.json 的公开形态, 供 build 流程读取。
type AndroidMeta = meta

// LoadAndroidMeta 读取项目 certs/android-cert.json。不存在返回 (nil, nil)。
func LoadAndroidMeta(projectDir string) (*AndroidMeta, error) {
	return loadMeta(projectDir, "android")
}

func loadMeta(projectDir, platform string) (*meta, error) {
	data, err := os.ReadFile(filepath.Join(projectDir, OutDirName, platform+"-cert.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var m meta
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("解析 %s-cert.json 失败: %w", platform, err)
	}
	return &m, nil
}

// GenerateAndroid 生成 Android 签名用的 PKCS12 keystore（等价于 keytool
// -genkeypair -storetype PKCS12 的产物, AGP 3.x+ / Gradle 原生支持）。
func GenerateAndroid(projectDir string, opt Options) (Result, error) {
	res, err := generatePKCS12(projectDir, "android", opt, androidDefaults{
		years: 30, rsaBits: 2048,
	})
	if err != nil {
		return Result{}, err
	}
	res.Note = "已写入 certs/android-cert.json（含密码）。gradle 发布构建会自动读取;\n" +
		"  ⚠️ certs/ 目录严禁提交仓库（模板 .gitignore 已包含）。换证书 = 无法覆盖安装。"
	return res, nil
}

// GenerateWindows 生成 Windows 代码签名用的自签 .pfx。
func GenerateWindows(projectDir string, opt Options) (Result, error) {
	res, err := generatePKCS12(projectDir, "windows", opt, androidDefaults{
		years: 3, rsaBits: 2048,
		codeSigning: true,
	})
	if err != nil {
		return Result{}, err
	}
	res.Note = "签名: signtool sign /fd SHA256 /f certs/windows.pfx /p <密码> /t http://timestamp.digicert.com app.exe\n" +
		"  ⚠️ 自签证书只适合本机/内网测试; 公开分发需购买 EV 代码签名证书（SmartScreen 信誉）。"
	return res, nil
}

// GenerateHarmony 生成鸿蒙调试用的 ECC P-256 自签证书对（.p12 + .cer）。
// 自签为 CA（CertSign）是为了本地 hap-sign-tool sign-profile 能用同一把钥匙
// 给调试 Profile 签名 —— 正式上架必须换 AGC 发布证书, 这里只管"调试能装"。
func GenerateHarmony(projectDir string, opt Options) (Result, error) {
	alias := defaultStr(opt.Alias, "gox")
	password, generated, err := resolvePassword(opt)
	if err != nil {
		return Result{}, err
	}
	years := opt.Years
	if years <= 0 {
		years = 3
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Result{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          randomSerial(),
		Subject:               subject(opt),
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(years, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true, // 本地 sign-profile 需要能签 Profile
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return Result{}, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return Result{}, err
	}

	// hap-sign-tool 读标准 PKCS12（BouncyCastle 解析, legacy 算法兼容）。
	p12Bytes, err := encodePKCS12(key, cert, password, alias)
	if err != nil {
		return Result{}, fmt.Errorf("编码 harmony.p12 失败: %w", err)
	}
	p12Rel := filepath.Join(relOutDir(opt), "harmony.p12")
	cerRel := filepath.Join(relOutDir(opt), "harmony.cer")
	files := []string{p12Rel, cerRel}
	if err := ensureAbsent(projectDir, files, opt.Force); err != nil {
		return Result{}, err
	}
	if err := writeFiles(projectDir, map[string][]byte{
		p12Rel: p12Bytes,
		cerRel: certDER, // .cer 直接落 DER, hap-sign-tool 缺省 -outForm DER
	}); err != nil {
		return Result{}, err
	}
	m := meta{Platform: "harmony", File: p12Rel, Alias: alias,
		StorePassword: password, KeyPassword: password,
		CommonName: subjectCN(opt), Org: subjectOrg(opt), NotAfter: tpl.NotAfter.Format(time.RFC3339)}
	if err := saveMeta(projectDir, "harmony", m, generated); err != nil {
		return Result{}, err
	}
	return Result{Files: append(files, filepath.Join(relOutDir(opt), "harmony-cert.json")),
		Alias: alias, Password: password,
		Note: "调试签名: hap-sign-tool sign-app 用 harmony.p12 + harmony.cer;\n" +
			"  正式上架请在 AGC 申请发布证书与 Profile, 调试证书不能发商店。"}, nil
}

// GenerateIOSCSR 生成 iOS 上架用的密钥对 + CSR（Apple 要求 RSA 2048 签发 CSR）。
// 用户拿 ios.csr 到 developer.apple.com → Certificates 换回 .cer 后,
// 调 BundleIOSP12（gox cert ios --cer apple.cer）合成 xcode 可导入的 .p12。
func GenerateIOSCSR(projectDir string, opt Options) (Result, error) {
	keyRel := filepath.Join(relOutDir(opt), "ios.key.pem")
	csrRel := filepath.Join(relOutDir(opt), "ios.csr")
	if err := ensureAbsent(projectDir, []string{keyRel, csrRel}, opt.Force); err != nil {
		return Result{}, err
	}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Result{}, err
	}
	// 私钥落 PKCS#8（"PRIVATE KEY"）—— 与算法解耦, openssl/xcode 都认。
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Result{}, err
	}
	csrTemplate := &x509.CertificateRequest{
		Subject:            subject(opt),
		EmailAddresses:     []string{subjectCN(opt)}, // Apple CSR 惯例: CN 填邮箱
		SignatureAlgorithm: x509.SHA256WithRSA,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, key)
	if err != nil {
		return Result{}, err
	}
	if err := writeFiles(projectDir, map[string][]byte{
		keyRel: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
		csrRel: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER}),
	}); err != nil {
		return Result{}, err
	}
	return Result{Files: []string{keyRel, csrRel},
		Note: "下一步: developer.apple.com → Certificates → 创建 iOS Distribution →\n" +
			"  上传 certs/ios.csr → 下载 .cer → gox cert ios --cer <下载的.cer>"}, nil
}

// BundleIOSP12 把 Apple 签发的 .cer 与本地私钥合成 .p12（xcode/DCloud 打包都要这个）。
func BundleIOSP12(projectDir, cerPath string, opt Options) (Result, error) {
	keyRel := filepath.Join(relOutDir(opt), "ios.key.pem")
	keyPEM, err := os.ReadFile(filepath.Join(projectDir, keyRel))
	if err != nil {
		return Result{}, fmt.Errorf("找不到 %s —— 先执行 gox cert ios 生成密钥对: %w", keyRel, err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return Result{}, fmt.Errorf("%s 不是合法的 PEM 私钥", keyRel)
	}
	var key crypto.PrivateKey
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		key = k
	} else if k8, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		key = k8
	} else {
		return Result{}, fmt.Errorf("%s 私钥格式无法识别（支持 PKCS#1/PKCS#8）", keyRel)
	}

	cerDER, err := os.ReadFile(cerPath)
	if err != nil {
		return Result{}, fmt.Errorf("读取 Apple 证书失败: %w", err)
	}
	// Apple 后台下载的是 DER; 有人会转 PEM 再来, 两种都认。
	if cert, _ := x509.ParseCertificate(cerDER); cert != nil {
		// DER 解析成功, 继续用 cerDER
	} else if blk, _ := pem.Decode(cerDER); blk != nil {
		cerDER = blk.Bytes
	}
	cert, err := x509.ParseCertificate(cerDER)
	if err != nil {
		return Result{}, fmt.Errorf("%s 不是合法的证书（DER/PEM 都试过了）: %w", cerPath, err)
	}

	password, _, err := resolvePassword(opt)
	if err != nil {
		return Result{}, err
	}
	p12Bytes, err := encodePKCS12(key, cert, password, cert.Subject.CommonName)
	if err != nil {
		return Result{}, err
	}
	p12Rel := filepath.Join(relOutDir(opt), "ios.p12")
	if err := ensureAbsent(projectDir, []string{p12Rel}, opt.Force); err != nil {
		return Result{}, err
	}
	if err := writeFiles(projectDir, map[string][]byte{p12Rel: p12Bytes}); err != nil {
		return Result{}, err
	}
	return Result{Files: []string{p12Rel}, Password: password,
		Note: "xcode 签名或云打包时导入 certs/ios.p12（密码见上方输出）"}, nil
}

// ---- android/windows 共用的 PKCS12 生成路径 ----

type androidDefaults struct {
	years       int
	rsaBits     int
	codeSigning bool
}

func generatePKCS12(projectDir, platform string, opt Options, d androidDefaults) (Result, error) {
	alias := defaultStr(opt.Alias, "gox")
	password, generated, err := resolvePassword(opt)
	if err != nil {
		return Result{}, err
	}
	years := opt.Years
	if years <= 0 {
		years = d.years
	}

	key, err := rsa.GenerateKey(rand.Reader, d.rsaBits)
	if err != nil {
		return Result{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber: randomSerial(),
		Subject:      subject(opt),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(years, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	if d.codeSigning {
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return Result{}, err
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return Result{}, err
	}

	p12Bytes, err := encodePKCS12(key, cert, password, alias)
	if err != nil {
		return Result{}, fmt.Errorf("编码 keystore 失败: %w", err)
	}
	fileRel := filepath.Join(relOutDir(opt), platform+".keystore")
	if platform == "windows" {
		fileRel = filepath.Join(relOutDir(opt), "windows.pfx")
	}
	if err := ensureAbsent(projectDir, []string{fileRel}, opt.Force); err != nil {
		return Result{}, err
	}
	if err := writeFiles(projectDir, map[string][]byte{fileRel: p12Bytes}); err != nil {
		return Result{}, err
	}
	m := meta{Platform: platform, File: fileRel, Alias: alias,
		StorePassword: password, KeyPassword: password,
		CommonName: subjectCN(opt), Org: subjectOrg(opt), NotAfter: tpl.NotAfter.Format(time.RFC3339)}
	if err := saveMeta(projectDir, platform, m, generated); err != nil {
		return Result{}, err
	}
	return Result{Files: []string{fileRel, filepath.Join(relOutDir(opt), platform+"-cert.json")},
		Alias: alias, Password: password}, nil
}

// ---- 公共小工具 ----

func resolveOutDir(projectDir string, opt Options) string {
	return filepath.Join(projectDir, defaultStr(opt.OutDir, OutDirName))
}

func relOutDir(opt Options) string {
	return defaultStr(opt.OutDir, OutDirName)
}

func subject(opt Options) pkix.Name {
	return pkix.Name{
		CommonName:         subjectCN(opt),
		Organization:       []string{subjectOrg(opt)},
		Country:            []string{"CN"},
	}
}

func subjectCN(opt Options) string { return defaultStr(opt.CommonName, "Gox App") }
func subjectOrg(opt Options) string { return defaultStr(opt.Org, "Gox") }

func defaultStr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

// resolvePassword 返回生效密码; 第二个返回值报告是否为随机生成（生成时才落 meta 提示）。
func resolvePassword(opt Options) (string, bool, error) {
	if opt.Password != "" {
		return opt.Password, false, nil
	}
	const alphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789" // 去 0/O/1/l/i 防抄错
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", false, err
	}
	out := make([]byte, len(b))
	for i, c := range b {
		out[i] = alphabet[int(c)%len(alphabet)]
	}
	return string(out), true, nil
}

func randomSerial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return big.NewInt(time.Now().UnixNano())
	}
	return n
}

// ensureAbsent 在 !force 时拦截覆盖。安卓证书尤其不能覆盖: 换签名 = 老用户无法升级。
func ensureAbsent(projectDir string, relFiles []string, force bool) error {
	if force {
		return nil
	}
	var exists []string
	for _, rel := range relFiles {
		if _, err := os.Stat(filepath.Join(projectDir, rel)); err == nil {
			exists = append(exists, rel)
		}
	}
	if len(exists) == 0 {
		return nil
	}
	return fmt.Errorf("证书已存在: %v —— 覆盖会作废旧签名（安卓换签名 = 无法覆盖安装）。确认要重置请加 --force", exists)
}

func writeFiles(projectDir string, files map[string][]byte) error {
	for rel, data := range files {
		abs := filepath.Join(projectDir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return err
		}
		// 私钥类文件收紧权限到 0600, 别的进程/用户别读。
		if err := os.WriteFile(abs, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func saveMeta(projectDir, platform string, m meta, randomPwd bool) error {
	if err := os.MkdirAll(filepath.Join(projectDir, OutDirName), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(projectDir, OutDirName, platform+"-cert.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	if randomPwd {
		fmt.Fprintf(os.Stderr, "  ⚠️ 未指定 --password, 已随机生成并存入 %s（该文件与 certs/ 整体都不可提交仓库）\n", path)
	}
	return nil
}
