package main

// 常驻进程的剖面入口 (T3-3)。
//
// 为什么是环境变量而不是子命令/flag：gox 的调用形态是「gox <入口.js>」——
// 所有 --allow-* 之外的参数都会被当成脚本路径（见 main.go 的分发逻辑），
// 塞一个全局 --pprof flag 会污染脚本参数解析；而 dev 模式下的热重启子进程
// 靠环境变量继承（GOX_DEV 同款做法，见 cmd_dev.go 的 defaultDevSpawn），
// 环境变量是唯一能自动穿透到子进程的通道。
//
// 为什么默认关闭：常驻进程的 RSS 是用户可感知的成本。关闭时这里只做两次
// os.Getenv，不起 goroutine、不建监听、不打开文件。net/http 本来就被
// stdlib/http.go 链了进来，新增的只是 net/http/pprof 的 handler 代码
// （纯体积，无运行时成本）。
//
// 两条入口各管一段：
//
//	GOX_PPROF=127.0.0.1:6060       起 net/http/pprof，活着就能抓（含分配剖面）
//	GOX_MEMPROFILE=/tmp/heap.pb.gz 退出/被 SIGINT、SIGTERM 时 dump 堆剖面
//
// 为什么 dump 要用信号兜底：go.mod 之外没有任何"退出钩子"，而 gox 的退出
// 路径里混着 os.Exit（脚本报错、process.exit），defer 在这些路径上不执行。
// 信号处理器覆盖 Ctrl-C / kill 这两种"人手动结束常驻进程"的常见方式；
// process.exit / SIGKILL 仍然抓不到，作为已知盲区写进 docs/performance.md。

import (
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof" // 把 /debug/pprof/* 注册到 DefaultServeMux（仅 import 副作用）
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"syscall"
)

// initPprof 按环境变量打开剖面入口，返回退出时的收尾函数。
//
// 返回值必须由 main 以 `defer initPprof()()` 调用——它负责"正常返回"这条
// 路径的 heap dump 与监听关闭；os.Exit 路径由信号处理器兜底。
func initPprof() func() {
	var cleanups []func()

	if addr := strings.TrimSpace(os.Getenv("GOX_PPROF")); addr != "" {
		if fn, err := startPprofServer(addr); err != nil {
			// 剖面是诊断能力，不是运行前提：监听失败只警告，绝不让应用起不来。
			fmt.Fprintf(os.Stderr, "gox: GOX_PPROF 启动失败（%v），继续运行\n", err)
		} else {
			cleanups = append(cleanups, fn)
		}
	}

	if path := strings.TrimSpace(os.Getenv("GOX_MEMPROFILE")); path != "" {
		cleanups = append(cleanups, installHeapDump(path))
	}

	return func() {
		for _, fn := range cleanups {
			fn()
		}
	}
}

// startPprofServer 在 addr 上起 pprof HTTP 服务，返回关闭函数。
//
// 先 Listen 再 Serve：端口被占时能在启动阶段打一行明确的错误，而不是让
// 一个后台 goroutine 悄悄失败。
func startPprofServer(addr string) (func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: http.DefaultServeMux}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "gox: pprof 服务退出（%v）\n", err)
		}
	}()

	// 非回环地址等于把堆内容（可能含业务数据）暴露到网络上，明确提醒一次。
	// 不强制改写地址：容器/远程调试确实有这种用法，但需要用户自己承担。
	if host, _, err := net.SplitHostPort(ln.Addr().String()); err == nil {
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			fmt.Fprintf(os.Stderr, "gox: 警告：pprof 监听在非回环地址 %s，网络可达者均可读取堆内容\n", ln.Addr())
		}
	}

	fmt.Fprintf(os.Stderr, "gox: pprof 已监听 http://%s/debug/pprof/\n", ln.Addr())
	fmt.Fprintf(os.Stderr, "gox: 分配剖面 http://%s/debug/pprof/heap?alloc_space=1（活对象看 inuse_space）\n", ln.Addr())
	return func() { _ = srv.Close() }, nil
}

// installHeapDump 注册退出时的堆剖面 dump，返回收尾函数。
//
// 收尾函数在 main 正常返回时执行；信号路径由本函数装的处理器接管。
// 两条路径共用 sync.Once，避免"先 Ctrl-C 再自然退出"重复写同一个文件。
func installHeapDump(path string) func() {
	var once sync.Once
	dump := func() {
		once.Do(func() { writeHeapProfile(path) })
	}

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-ch
		dump()
		// 保持"被信号杀死"的语义：退出码 128+信号号，父进程（gox dev 的
		// watcher、shell）仍能按惯例判断是中断还是终止。
		code := 128
		if s, ok := sig.(syscall.Signal); ok {
			code += int(s)
		}
		os.Exit(code)
	}()

	return dump
}

// writeHeapProfile 把堆剖面写到 path。
//
// 写之前强制一次 GC：heap profile 同时含 inuse_space 与 alloc_space 两套
// 样本，先 GC 能保证 inuse 反映"写完这一刻仍然活着"的对象——判断泄漏时
// 用它和 alloc_space 的差值区分"分配多"与"没释放"。
func writeHeapProfile(path string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gox: GOX_MEMPROFILE 建文件失败（%v）\n", err)
		return
	}
	defer f.Close()

	runtime.GC()
	if err := pprof.Lookup("heap").WriteTo(f, 0); err != nil {
		fmt.Fprintf(os.Stderr, "gox: heap profile 写入失败（%v）\n", err)
		return
	}
	fmt.Fprintf(os.Stderr, "gox: heap profile 已写入 %s\n", path)
	fmt.Fprintf(os.Stderr, "gox: 查看 go tool pprof -alloc_space <gox 可执行文件> %s\n", path)
}
