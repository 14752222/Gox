package main

// gox dev —— 开发期间热更新。
//
// 两条路径 (M2 P1-6):
//
//  1. **进程内重载** (非 GUI 脚本, 或显式要求): 监听入口所在目录的
//     .js/.ts/.tsx 变更, 丢弃旧 VM、重建新 VM 并重新执行入口。适用于
//     "执行完即返回" 的脚本。
//
//  2. **进程级热重启** (GUI/常驻脚本, 或 --restart): 把应用作为**子进程**运行;
//     文件变更 → 杀旧进程 + 起新进程。原因: render(...) 之后脚本阻塞在窗口
//     消息泵里, 进程内的 dev 循环根本拿不到控制权 (旧实现明确写着这个 TODO)。
//     进程级重启能立刻用上"改一行 → 界面更新", 代价是**丢内存状态** —— 这与
//     组件级 HMR (保留 signal/状态) 是两回事, 边界见 docs/typescript.md。
//
// 用法:
//
//	gox dev                    探测默认入口 (main.js 优先, 回落 main.tsx/ts/jsx)
//	gox dev <入口>             监听该文件所在目录 (递归含子目录)
//	gox dev --restart <入口>   强制进程级热重启 (即使没检测到 GUI 工程)
//
// GUI 工程判定: 入口向上查找 gox.json —— 脚手架生成的工程都有, 是"这是常驻
// 应用"最可靠的静态信号。判定不了的常驻脚本可用 --restart 明确指定。

import (
	"fmt"
	"os"
	"os/exec"
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

// devSpawn 启动一个子进程跑入口脚本。抽成变量以便测试替换 (不真起 GUI)。
var devSpawn = defaultDevSpawn

// defaultDevSpawn 用当前 gox 可执行文件拉起 `gox <entry>`。
func defaultDevSpawn(entry string) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, entry)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()
	return cmd, nil
}

// runDev 实现 `gox dev [入口] [--restart]`。
func runDev(args []string) {
	entry := devDefaultEntry()
	forceRestart := false
	for _, a := range args {
		switch {
		case a == "-h" || a == "--help":
			fmt.Fprint(os.Stdout, `用法: gox dev [入口] [--restart]

监听入口所在目录 (递归含子目录) 的 .js/.jsx/.ts/.tsx 变更并热更新。

两种模式 (自动选择):
  - 普通脚本: 进程内丢弃旧 VM、重建并重跑入口 (快, 保留进程)。
  - GUI/常驻脚本: 进程级热重启 —— 杀旧子进程 + 起新子进程。
    判据: 入口向上能找到 gox.json; 或用 --restart 强制。

默认入口依次探测: src/main.js → src/main.tsx → src/main.ts → src/main.jsx。
TS/TSX 在加载前自动完成类型剥离 (esbuild), JSX 原样保留; 转译产物带
持久化缓存 (~/.gox/cache/transpile), 重启时命中缓存基本不重算。

注意: 进程级热重启会**丢失内存状态** (signal/表单输入等)。组件级 HMR
(保留状态、只换元素树) 是后续里程碑, 当前未实现。详见 docs/typescript.md。
`)
			return
		case a == "--restart":
			forceRestart = true
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

	restartMode := forceRestart || isGUIProject(abs)
	if restartMode {
		fmt.Printf("[dev] watching %s (entry: %s) — 进程级热重启\n", root, filepath.Base(abs))
		devRunRestartLoop(watcher, abs)
		return
	}

	fmt.Printf("[dev] watching %s (entry: %s)\n", root, filepath.Base(abs))
	devRunInProcessLoop(watcher, abs)
}

// devRunInProcessLoop 是原行为: 每次变更重建 VM 重跑入口 (执行完即返回的脚本)。
func devRunInProcessLoop(watcher *fsnotify.Watcher, entry string) {
	devRun(entry, 0)

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
		devRun(entry, n)
	}

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !devHandleEvent(watcher, ev) {
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

// devRestarter 管理"一个入口 = 一个子进程"的杀旧起新。
type devRestarter struct {
	entry string
	child *exec.Cmd
}

// kill 杀掉当前子进程 (若有)。Kill (SIGKILL) 跨平台可用; GUI 进程收不到优雅
// 退出的机会, 但 dev 场景可接受 —— 窗口资源随进程消失, 下次启动重开。
func (r *devRestarter) kill() {
	if r.child == nil || r.child.Process == nil {
		return
	}
	_ = r.child.Process.Kill()
	_, _ = r.child.Process.Wait()
	r.child = nil
}

// start 杀掉旧子进程并拉起新子进程, 返回启动耗时 (供实测打印)。
func (r *devRestarter) start() (time.Duration, error) {
	r.kill()
	t0 := time.Now()
	cmd, err := devSpawn(r.entry)
	if err != nil {
		return 0, err
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	r.child = cmd
	return time.Since(t0), nil
}

// devRunRestartLoop 以子进程方式运行入口, 变更时杀旧起新。
//
// 计时口径: 从**第一个变更事件到达**到新子进程 Start() 返回 —— 这才是用户
// 感知的"改一行 → 界面更新"延迟 (含防抖 + 杀进程 + 拉起)。打印出来便于实测。
func devRunRestartLoop(watcher *fsnotify.Watcher, entry string) {
	var (
		mu       sync.Mutex
		pending  int
		timer    *time.Timer
		changeAt time.Time
	)
	restarter := &devRestarter{entry: entry}
	restart := func(changes int) {
		mu.Lock()
		t0 := changeAt
		if t0.IsZero() {
			t0 = time.Now()
		}
		changeAt = time.Time{}
		pending = 0
		timer = nil
		mu.Unlock()

		if _, err := restarter.start(); err != nil {
			fmt.Fprintf(os.Stderr, "[dev] 启动子进程失败: %v\n", err)
			return
		}
		d := time.Since(t0)
		if changes > 0 {
			fmt.Printf("[dev] restarted %s in %s (%d change%s)\n",
				filepath.Base(entry), d.Round(time.Millisecond), changes, plural(changes))
		} else {
			fmt.Printf("[dev] started %s in %s\n", filepath.Base(entry), d.Round(time.Millisecond))
		}
	}

	restart(0)
	defer restarter.kill()

	for {
		select {
		case ev, ok := <-watcher.Events:
			if !ok {
				return
			}
			if !devHandleEvent(watcher, ev) {
				continue
			}
			mu.Lock()
			if pending == 0 {
				changeAt = time.Now()
			}
			pending++
			if timer != nil {
				timer.Stop()
			}
			timer = time.AfterFunc(devDebounce, func() { restart(1) })
			mu.Unlock()
		case err, ok := <-watcher.Errors:
			if !ok {
				return
			}
			fmt.Fprintf(os.Stderr, "[dev] watcher error: %v\n", err)
		}
	}
}

// devHandleEvent 处理单个 fsnotify 事件: 过滤 + 给新目录挂监听。
// 返回 true 表示"这是值得触发重载的变更"。
func devHandleEvent(watcher *fsnotify.Watcher, ev fsnotify.Event) bool {
	if ev.Op&fsnotify.Chmod != 0 {
		return false
	}
	// 新建子目录也要挂上监听, 否则新增目录里的改动收不到
	if ev.Op&fsnotify.Create != 0 {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			devWatchDir(watcher, ev.Name)
			return false
		}
	}
	return devInteresting(ev.Name)
}

// isGUIProject 报告入口是否属于一个 GUI 工程 (向上找 gox.json)。
func isGUIProject(entry string) bool {
	dir := filepath.Dir(entry)
	for {
		if _, err := os.Stat(filepath.Join(dir, "gox.json")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}

// devRun 执行一次入口脚本 (进程内模式): 新建 VM → EvalFile → 跑事件循环。
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
