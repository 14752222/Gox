// cmd_sync.go 实现 `gox sync`: 读取 gox.json 的权限声明, 幂等注入
// android/AndroidManifest.xml 与 ios/Info.plist 的标记区块。
//
// 用法: gox sync [目录]     （目录缺省为当前目录）
package main

import (
	"fmt"
	"os"

	"github.com/14752222/Gox/config"
)

func runSync(args []string) {
	dir := "."
	for _, a := range args {
		if a == "-h" || a == "--help" {
			fmt.Fprint(os.Stdout, `用法: gox sync [目录]

读取项目根的 gox.json, 把 permissions 字段幂等注入:
  android/AndroidManifest.xml  <!--GOX:PERMISSIONS--> 区块
  ios/Info.plist               <!--GOX:USAGE--> 区块（NSxxxUsageDescription 用途描述）

重复执行不会重复追加（标记区块整体替换）。权限支持自定义用途文案:
  "permissions": [{ "name": "camera", "desc": "用于拍摄头像" }]
默认最小权限: 未声明任何权限时仅 INTERNET。
`)
			return
		}
		dir = a
	}

	cfg, err := config.Load(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox sync: %v\n", err)
		os.Exit(1)
	}
	changed, err := config.Sync(dir, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox sync: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("已同步权限（%d 项声明）:\n", cfg.Permissions.Len())
	for _, p := range cfg.Permissions.List() {
		fmt.Printf("  - %s\n", p.Name)
	}
	for _, f := range changed {
		fmt.Printf("  注入: %s\n", f)
	}
}
