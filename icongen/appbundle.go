package icongen

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuildMacAppBundle 把已编译的 darwin 二进制组装成 .app bundle:
//
//	<out>.app/
//	  Contents/
//	    MacOS/<name>          可执行文件（拷贝 + chmod 0755）
//	    Info.plist            由 name/appID/version 渲染
//	    PkgInfo               "APPL????"
//	    Resources/AppIcon.icns
//
// 返回 .app 目录路径。bin 已链接完成, bundle 组装只是搬文件 + 写两个小
// 文本文件, 所以放这个包里与 .icns 生成配套。
func BuildMacAppBundle(binPath, outDir, name, title, appID, version, icnsPath string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("bundle 需要应用名")
	}
	if title == "" {
		title = name
	}
	appDir := outDir + ".app"
	contents := filepath.Join(appDir, "Contents")
	for _, d := range []string{filepath.Join(contents, "MacOS"), filepath.Join(contents, "Resources")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return "", err
		}
	}

	// 可执行文件
	binDest := filepath.Join(contents, "MacOS", name)
	if err := copyExecutable(binPath, binDest); err != nil {
		return "", fmt.Errorf("拷贝二进制失败: %w", err)
	}

	// Info.plist
	display := title
	if display == "" {
		display = name
	}
	infoPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>zh_CN</string>
	<key>CFBundleDisplayName</key>
	<string>%s</string>
	<key>CFBundleExecutable</key>
	<string>%s</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleIdentifier</key>
	<string>%s</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>%s</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>%s</string>
	<key>CFBundleVersion</key>
	<string>1</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSPrincipalClass</key>
	<string>NSApplication</string>
</dict>
</plist>
`, xmlEscape(display), xmlEscape(name), xmlEscape(appID), xmlEscape(display), xmlEscape(version))
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(infoPlist), 0o644); err != nil {
		return "", err
	}

	// PkgInfo: 类型 APPL + 未注册创建者签名
	if err := os.WriteFile(filepath.Join(contents, "PkgInfo"), []byte("APPL????"), 0o644); err != nil {
		return "", err
	}

	// 图标
	if icnsPath != "" {
		data, err := os.ReadFile(icnsPath)
		if err != nil {
			return "", fmt.Errorf("读取 .icns 失败: %w", err)
		}
		if err := os.WriteFile(filepath.Join(contents, "Resources", "AppIcon.icns"), data, 0o644); err != nil {
			return "", err
		}
	}
	return appDir, nil
}

// copyExecutable 拷贝并保留可执行位。
func copyExecutable(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o755)
}

// xmlEscape 转义 XML 文本节点的五个保留字符（应用名里可能出现 & / <）。
func xmlEscape(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '&':
			b = append(b, "&amp;"...)
		case '<':
			b = append(b, "&lt;"...)
		case '>':
			b = append(b, "&gt;"...)
		case '\'':
			b = append(b, "&#39;"...)
		case '"':
			b = append(b, "&#34;"...)
		default:
			b = append(b, c)
		}
	}
	return string(b)
}
