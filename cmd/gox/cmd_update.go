// cmd_update.go 实现 `gox update`: gox 自身的自更新命令。
//
// 数据源是一个 JSON 清单 (格式见 docs/auto-update.md §清单格式): 应用作者把
// 清单托管在任意静态服务器上即可, 本命令按 "检查 → 流式下载 → 三段换名"
// 的顺序把新二进制就位到当前 gox 可执行文件。
//
// 用法:
//
//	gox update                     检查并安装稳定通道的最新版
//	gox update --check             只检查不安装
//	gox update --pre               pre-release 通道也参与选版本
//	gox update --manifest <URL>    显式指定清单地址 (缺省读 GOX_UPDATE_MANIFEST
//	                               环境变量, 再缺省用官方 npm 发布元数据不可用,
//	                               因此无默认值 —— 没配就提示怎么配)
//
// 更新成功后当前进程仍跑旧映像, 重新打开终端/重启 gox 生效; .bak 备份
// 保留到下次启动 (gox 启动即调 CleanupBackup), 想立刻清理: gox update --cleanup。
package main

import (
	"fmt"
	"os"

	"github.com/14752222/Gox/update"
)

func runUpdate(args []string) {
	var (
		manifestURL string
		checkOnly   bool
		preRelease  bool
		cleanupOnly bool
	)
	rest := args
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-h" || a == "--help":
			printUpdateUsage()
			return
		case a == "--check":
			checkOnly = true
		case a == "--pre" || a == "--pre-release":
			preRelease = true
		case a == "--cleanup":
			cleanupOnly = true
		case a == "--manifest":
			i++
			if i >= len(rest) {
				updateFatal("--manifest 需要一个 URL 参数")
			}
			manifestURL = rest[i]
		default:
			updateFatal("未知参数 %q (gox update --help 查看用法)", a)
		}
	}

	if cleanupOnly {
		exe, err := os.Executable()
		if err != nil {
			updateFatal("定位当前可执行文件失败: %v", err)
		}
		if err := update.CleanupBackup(exe); err != nil {
			updateFatal("%v", err)
		}
		fmt.Println("已清理 .bak 备份 (如存在)。")
		return
	}

	if manifestURL == "" {
		manifestURL = os.Getenv("GOX_UPDATE_MANIFEST")
	}
	if manifestURL == "" {
		updateFatal("缺少清单地址: gox update --manifest <URL> 或设置 GOX_UPDATE_MANIFEST 环境变量。\n" +
			"清单格式见 docs/auto-update.md。")
	}

	rel, _, err := update.SelfUpdate("", manifestURL, version, update.CheckOptions{PreRelease: preRelease}, nil)
	if err != nil {
		updateFatal("%v", err)
	}
	if rel == nil {
		fmt.Printf("已是最新版本 (%s)。\n", version)
		return
	}
	if checkOnly {
		fmt.Printf("发现新版本 %s (%s):\n%s\n仅检查未安装 —— 去掉 --check 执行安装。\n",
			rel.Version, rel.Channel, rel.Notes)
		return
	}
	fmt.Printf("已更新到 %s —— 当前进程仍是旧版本, 重启 gox 生效。\n旧版本备份在 gox.bak, 下次启动自动清理。\n",
		rel.Version)
}

func printUpdateUsage() {
	fmt.Print(`gox update — gox 自更新

用法:
  gox update                     检查并安装稳定通道最新版
  gox update --check             只检查, 不安装
  gox update --pre               pre-release 通道也参与
  gox update --manifest <URL>    指定清单地址 (也可用 GOX_UPDATE_MANIFEST)
  gox update --cleanup           清理上次更新残留的 gox.bak

说明:
  - 清单是 JSON 文件, 格式见 docs/auto-update.md (含 pre-release 通道)。
  - 下载为流式落盘 + sha256 校验 + 断点续传; 更新走三段换名, 中断自愈。
  - 安装完成后需重启 gox 才运行新版本。
`)
}

func updateFatal(format string, a ...interface{}) {
	fmt.Fprintf(os.Stderr, "gox update: "+format+"\n", a...)
	os.Exit(1)
}
