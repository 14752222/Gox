package icongen

import (
	"fmt"
	"path/filepath"
)

// GenerateAll 是 `gox icon` 的主入口: 读入源图, 一键生成全部平台的图标资源。
// root 是项目根（含 gox.json 的那层目录）。返回写入的文件清单（斜杠相对路径）。
func GenerateAll(root, srcPath, androidBG string) ([]string, error) {
	src, err := LoadSource(srcPath)
	if err != nil {
		return nil, err
	}
	if androidBG == "" {
		androidBG = "#18243B"
	}

	var written []string
	android, err := src.GenerateAndroid(filepath.Join(root, "android"), androidBG)
	if err != nil {
		return nil, fmt.Errorf("Android 图标生成失败: %w", err)
	}
	for _, rel := range android {
		// GenerateAndroid 的清单相对其自身 root（= <项目>/android）, 这里补前缀
		written = append(written, "android/"+rel)
	}

	ios, err := src.GenerateIOS(filepath.Join(root, "ios", "Assets.xcassets", "AppIcon.appiconset"))
	if err != nil {
		return nil, fmt.Errorf("iOS 图标生成失败: %w", err)
	}
	written = append(written, ios...)

	desktop, err := src.GenerateDesktop(filepath.Join(root, "desktop"))
	if err != nil {
		return nil, fmt.Errorf("桌面图标生成失败: %w", err)
	}
	written = append(written, desktop...)

	favicon := filepath.Join(root, "favicon.png")
	if err := src.GenerateFavicon(favicon); err != nil {
		return nil, fmt.Errorf("生成 favicon 失败: %w", err)
	}
	written = append(written, "favicon.png")
	return written, nil
}
