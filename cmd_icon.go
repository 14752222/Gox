// cmd_icon.go 实现 `gox icon`: 以 gox.json 的 icon 字段指向的 1024×1024
// 源图为单源, 一键生成全部平台图标（Android mipmap + 自适应前景、
// iOS AppIconSet、Windows .ico、macOS .icns、favicon）。
//
// 用法: gox icon [目录] [--icon <路径>]   （目录缺省为当前目录）
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/icongen"
)

func runIcon(args []string) {
	dir := "."
	iconOverride := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox icon [目录] [--icon <路径>]

读取项目根的 gox.json, 以 icon 字段指向的源图（建议 1024×1024 PNG）生成:
  android/res/mipmap-*          启动图标 + 自适应图标前景层（5 档密度）
  ios/Assets.xcassets/AppIcon.appiconset   全尺寸图标 + Contents.json（1024 去 alpha）
  desktop/icon.ico / icon.icns  Windows / macOS 图标
  favicon.png

重复执行幂等 —— 同一源图永远产出同一批文件。
`)
			return
		case a == "--icon":
			if i+1 >= len(args) {
				iconFatal("--icon 需要一个值")
			}
			i++
			iconOverride = args[i]
		default:
			dir = a
		}
	}

	cfg, err := config.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox icon: %v\n", err)
		os.Exit(1)
	}
	icon := iconOverride
	if icon == "" {
		icon = cfg.Icon
	}
	if !filepath.IsAbs(icon) {
		icon = filepath.Join(dir, icon)
	}
	if _, err := os.Stat(icon); err != nil {
		fmt.Fprintf(os.Stderr, "gox icon: 源图标不可读: %s（替换 assets/icon.png 或改 gox.json 的 icon 字段）\n", icon)
		os.Exit(1)
	}

	written, err := icongen.GenerateAll(dir, icon, cfg.Android.AdaptiveBackground)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox icon: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已生成 %d 个图标文件（源: %s）:\n", len(written), icon)
	for _, w := range written {
		fmt.Printf("  %s\n", w)
	}
}

func iconFatal(msg string) {
	fmt.Fprintf(os.Stderr, "gox icon: %s\n", msg)
	os.Exit(1)
}
