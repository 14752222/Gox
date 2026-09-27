// cmd_cert.go 实现 `gox cert <platform>`: 一键生成各平台签名证书。
//
// 定位是"快捷操作": 不装 JDK/openssl/hap-sign-tool 也能出调试证书;
// 正式证书（Apple 签发、AGC 发布、EV 代码签名）仍走官方渠道 —— 用户
// 自备证书时在 gox.json 的 cert 段引用, 不必用本命令。
//
// 用法: gox cert <android|windows|harmony|ios> [目录] [选项]
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/14752222/Gox/certgen"
)

func runCert(args []string) {
	if len(args) == 0 {
		certFatal("缺少平台参数: gox cert <android|windows|harmony|ios> [目录]")
	}
	platform := args[0]
	switch platform {
	case "android", "windows", "harmony", "ios":
	case "-h", "--help":
		printCertUsage()
		return
	default:
		certFatal("未知平台 %q （可用: android|windows|harmony|ios）", platform)
	}

	var dir, cn, org, alias, password, cerPath, outDir string
	var force bool
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-h" || a == "--help":
			printCertUsage()
			return
		case a == "--cn":
			cn = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--org":
			org = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--alias":
			alias = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--password":
			password = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--out":
			outDir = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--cer":
			cerPath = certArg(a, &i, rest) // certArg 内部负责前进 i
		case a == "--force":
			force = true
		default:
			if dir == "" {
				dir = a
				continue
			}
			certFatal("多余参数 %q", a)
		}
	}
	if dir == "" {
		dir = "."
	}
	// 注意: 证书生成刻意不依赖 gox.json —— 用户可能想在 create 之前先备好证书。

	opt := certgen.Options{
		CommonName: cn,
		Org:        org,
		Alias:      alias,
		Password:   password,
		OutDir:     outDir,
		Force:      force,
	}

	var res certgen.Result
	var err error
	switch platform {
	case "android":
		res, err = certgen.GenerateAndroid(dir, opt)
	case "windows":
		res, err = certgen.GenerateWindows(dir, opt)
	case "harmony":
		res, err = certgen.GenerateHarmony(dir, opt)
	case "ios":
		if cerPath != "" {
			if !filepath.IsAbs(cerPath) {
				cerPath = filepath.Join(dir, cerPath)
			}
			res, err = certgen.BundleIOSP12(dir, cerPath, opt)
		} else {
			res, err = certgen.GenerateIOSCSR(dir, opt)
		}
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox cert %s: %v\n", platform, err)
		os.Exit(1)
	}

	fmt.Printf("已生成 %s 证书:\n", platform)
	for _, f := range res.Files {
		fmt.Printf("  %s\n", f)
	}
	if res.Alias != "" {
		fmt.Printf("  别名: %s\n", res.Alias)
	}
	if res.Password != "" {
		fmt.Printf("  密码: %s\n", res.Password)
	}
	if res.Note != "" {
		fmt.Println(res.Note)
	}
}

func printCertUsage() {
	fmt.Fprint(os.Stdout, `用法: gox cert <android|windows|harmony|ios> [目录] [选项]

一键生成各平台签名证书（纯 Go 实现, 无需 JDK/openssl）, 输出到 certs/ 目录。
证书已存在时拒绝覆盖（换签名 = 无法覆盖安装）, 确认重置加 --force。

  android  certs/android.keystore       PKCS12 (RSA 2048, 自签 30 年)
           gradle 发布构建自动读取; 正式上架也推荐用自有 keystore
  windows  certs/windows.pfx            自签代码签名证书 (3 年)
           signtool 直接用; 公开分发建议购买 EV 证书
  harmony  certs/harmony.p12 + .cer     ECC P-256 自签 CA (3 年)
           配合 hap-sign-tool 本地调试; 上架需 AGC 发布证书
  ios      certs/ios.key.pem + ios.csr  密钥对 + CSR (Apple 要求 RSA 2048)
           CSR 上传 developer.apple.com 换 .cer 后:
           gox cert ios --cer <下载的.cer>  → 合成 certs/ios.p12

选项:
  --cn <名>          证书主体 CN (android/windows/harmony; ios 缺省当邮箱用)
  --org <组织>       证书主体 O (缺省 Gox)
  --alias <别名>     keystore 别名 (缺省 gox)
  --password <密码>  keystore/pfx 密码 (缺省随机生成并写入 certs/*-cert.json)
  --out <目录>       输出目录 (缺省 certs)
  --force            覆盖已存在的证书

安全提示: certs/ 目录含密码与私钥, 严禁提交仓库（模板 .gitignore 已包含）。
自备正式证书的用户在 gox.json 的 "cert" 段配置路径与密码即可, 无需本命令。
`)
}

func certArg(flag string, i *int, args []string) string {
	if *i+1 >= len(args) {
		certFatal("%s 需要一个值", flag)
	}
	*i++
	return strings.TrimSpace(args[*i])
}

func certFatal(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "gox cert: "+format+"\n", a...)
	os.Exit(1)
}
