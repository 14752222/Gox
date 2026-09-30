package main

// gox dev —— 开发期间热更新: 监听入口所在 src/ 目录的 .js/.ts/.tsx 变更,
// 进程内丢弃旧 VM、重建新 VM 并重新执行入口文件。
// TS/TSX 文件在进引擎前自动过 esbuild 类型剥离（JSX 原样保留）。
//
// 用法:
//
//	gox dev               等价于 gox dev <默认入口>（main.js 优先, 回落 main.tsx/ts/jsx）
//	gox dev <入口.js|入口.ts|入口.tsx>   监听该文件所在目录 (递归含子目录)
//
// 适用边界: 见 docs/dev-workflow.md。GUI 常驻脚本 (render(...) 后依赖
// 消息泵保活) 会在泵循环里阻塞, dev 循环拿不到控制权 —— 当前实现只对
// "执行完即返回" 的脚本提供热更新。
//
// TODO(dev-gui): GUI 常驻脚本的热替换需要复用已有窗口句柄、只替换根元素
// 树 (窗口是进程级资源, 重建 VM 不能重建窗口)。元素树状态语义 (signal
// 订阅如何迁移、旧树何时销毁) 需要先在 gfx 上游讨论, 本次不做。

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/vm"
)

// devDebounce 是防抖窗口: 窗口内的连续变更 (编辑器保存多个文件、
// 写入 + 重命名成对事件) 合并为一次重载。
const devDebounce = 250 * time.Millisecond

// runDev 实现 `gox dev [入口.js]`。
func runDev(args []string) {
	entry := devDefaultEntry()
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox dev [入口.js]

监听入口文件所在目录 (递归含子目录) 的 .js/.jsx/.ts/.tsx 变更, 每次变更后
丢弃旧 VM、重建 VM 并重新执行入口文件。默认入口依次探测:
src/main.js → src/main.tsx → src/main.ts → src/main.jsx。
TS/TSX 文件在进引擎前自动完成类型剥离 (esbuild 转译), JSX 原样保留。

适用于执行完即返回的脚本; GUI 常驻脚本暂不支持热更新,
见 docs/dev-workflow.md。
`)
			return
		case strings.HasPrefix(a, "-"):
			devFatal("未知选项: " + a)
		default:
			entry = a
		}
	}

	abs, err := filepath.Abs(entry)
	if err != nil {
		devFatal(err.Error())
	}
	if _, err := os.Stat(abs); err != nil {
		devFatal("读不到入口文件: " + entry)
	}
	root := filepath.Dir(abs)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		devFatal(err.Error())
	}
	defer watcher.Close()
	if err := devWatchDir(watcher, root); err != nil {
		devFatal(err.Error())
	}

	fmt.Printf("[dev] watching %s (entry: %s)\n", root, filepath.Base(abs))

	// 首次运行
	devRun(abs, 0)

	// 防抖状态: 变更先计数, 停止变更 devDebounce 后统一重载一次。
	var (
		mu      sync.Mutex
		pending int
		timer   *time.Timer
	)
	reload := func() {
		mu.Lock()
		n := pending
		pending = 0
		timer = nil
		mu.Unlock()
		devRun(abs, n)
	}

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if ev.Op&fsnotify.Chmod != 0 {
				continue
			}
			// 新建子目录也要挂上监听, 否则新增目录里的改动收不到
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					devWatchDir(watcher, ev.Name)
					continue
				}
			}
			if !devInteresting(ev.Name) {
				continue
			}
			mu.Lock()
			pending++
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(devDebounce, reload)
			mu.Unlock()
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[dev] watcher error: %v\n", err)
		}
	}
}

// devRun 执行一次入口脚本: 新建 VM (模块缓存随实例重建) → EvalFile → 跑定时器。
// 出错打印但不退出, 继续等下一次变更。
func devRun(entry string, changes int) {
	if changes > 0 {
		fmt.Printf("[dev] reloaded %s (%d change%s)\n", filepath.Base(entry), changes, plural(changes))
	}
	vmInst, err := vm.EvalFileVM(entry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[dev] %v\n", err)
		return
	}
	// 与 runFile 一致: GUI 活跃时用消息泵, 否则跑定时器
	var loopErr error
	if gfx.Active() {
		loopErr = vmInst.RunTimersWithPump(gfx.Pump)
	} else {
		loopErr = vmInst.RunTimers()
	}
	if loopErr != nil {
		fmt.Fprintf(os.Stderr, "[dev] timer error: %v\n", loopErr)
	}
}

// devDefaultEntry 探测默认入口: JS 工程保持 src/main.js 不变;
// TS 工程按 src/main.tsx → main.ts → main.jsx 依次回落。
func devDefaultEntry() string {
	for _, cand := range []string{"src/main.js", "src/main.tsx", "src/main.ts", "src/main.jsx"} {
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return "src/main.js" // 都没有时维持原默认, 让报错落在熟悉的入口名上
}

// devInteresting 报告该事件是否值得触发重载: 认 JS/TS 家族扩展名
// (.js/.jsx/.ts/.tsx/.mts/.cts), 忽略编辑器临时文件 (vim 的 4913/swap、
// ~ 备份、隐藏锁文件等)。
func devInteresting(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mts", ".cts":
		return !isTempFile(filepath.Base(path))
	}
	return false
}

// isTempFile 报告文件名是否是编辑器/系统产生的临时文件。
func isTempFile(name string) bool {
	switch name {
	case "4913": // vim 探测可写性的探测文件
		return true
	}
	if strings.HasPrefix(name, ".#") || strings.HasSuffix(name, "~") {
		return true
	}
	for _, ext := range []string{".swp", ".swo", ".swx", ".tmp", ".rej", ".orig"} {
		if strings.HasSuffix(name, ext) {
			return true
		}
	}
	return false
}

// devWatchDir 把 dir 与其所有子目录挂到 watcher 上 (fsnotify 不支持递归)。
func devWatchDir(watcher *fsnotify.Watcher, dir string) error {
	return filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // 单个不可读目录不该杀掉整个 dev 会话
		}
		if d.IsDir() {
			if err := watcher.Add(path); err != nil {
				fmt.Fprintf(os.Stderr, "[dev] watch %s: %v\n", path, err)
			}
		}
		return nil
	})
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func devFatal(msg string) {
	fmt.Fprintf(os.Stderr, "gox dev: %s\n", msg)
	os.Exit(1)
}
