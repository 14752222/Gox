// tools/rssmon —— GUI 常驻进程 RSS 曲线采样器（r95WjJ）。
//
// 无 GC 设计的渲染层（gfx/runtime 的帧缓冲与对象池）长时内存可观测性此前为零：
// bench 只测吞吐与帧间隔，没有任何工具回答「窗口挂 10 分钟 RSS 会不会爬」。
// rssmon 补上这个缺口：拉起一个常驻进程（典型是 `gox <gui-app>`），按固定
// 间隔采样它**整个进程树**的 RSS 合计，落成 CSV，供画曲线 / 做回归比对。
//
// 用法：
//
//	rssmon -interval 1s -duration 60s -out bench-results/rss-notes.csv -- \
//	    ./gox apps/notes
//
//	-duration 0 表示一直采到子进程自行退出（或被外部杀掉）。
//	采样的是进程树（gox dev 会再起子进程），非单 PID。
//
// CSV 列：elapsed_s,rss_kb,rss_bytes。退出时在 stderr 打 peak/末值/增量摘要，
// 并画一条 ASCII 迷你曲线，CI 日志里不下载 artifact 也能看出趋势。
//
// 平台：依赖 Linux /proc（CI 为 ubuntu-latest，与 bench.yml 的 xvfb 环境一致）；
// 其他平台直接报错退出，不影响 `go build ./...`。
package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// readRSSKb 读单个 PID 的 VmRSS（kB）。进程刚退出时读不到，返回 0,false。
func readRSSKb(pid int) (int64, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			kb, err := strconv.ParseInt(f[1], 10, 64)
			return kb, err == nil
		}
	}
	return 0, false
}

// treePIDs 返回 root 及其全部后代 PID（扫 /proc 的 PPid 链，采样频率低，够用）。
func treePIDs(root int) []int {
	type pc struct{ pid, ppid int }
	var all []pc
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return []int{root}
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// /proc/<pid>/stat: pid (comm) ppid ...，comm 可能含空格，取最后一个 ')' 之后
		s := string(b)
		i := strings.LastIndex(s, ")")
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 2 {
			continue
		}
		ppid, err := strconv.Atoi(f[1])
		if err != nil {
			continue
		}
		all = append(all, pc{pid, ppid})
	}
	byParent := map[int][]int{}
	for _, p := range all {
		byParent[p.ppid] = append(byParent[p.ppid], p.pid)
	}
	out := []int{}
	queue := []int{root}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		out = append(out, p)
		queue = append(queue, byParent[p]...)
	}
	return out
}

// sparkline 画一条 ASCII 趋势（8 级块字符），CI 日志直读。
func sparkline(vals []int64) string {
	if len(vals) == 0 {
		return ""
	}
	const blocks = "▁▂▃▄▅▆▇█"
	lo, hi := vals[0], vals[0]
	for _, v := range vals {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	var sb strings.Builder
	for _, v := range vals {
		lvl := 0
		if hi > lo {
			lvl = int((v - lo) * 7 / (hi - lo))
		}
		sb.WriteByte(blocks[lvl])
	}
	return sb.String()
}

func main() {
	interval := flag.Duration("interval", time.Second, "采样间隔")
	duration := flag.Duration("duration", 0, "采样总时长；0 = 直到子进程退出")
	out := flag.String("out", "", "CSV 输出路径（空 = stdout）")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "用法: rssmon [-interval 1s] [-duration 60s] [-out x.csv] -- <cmd...>")
		os.Exit(2)
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(os.Stderr, "启动子进程失败:", err)
		os.Exit(1)
	}

	var w *csv.Writer
	f := os.Stdout
	if *out != "" {
		var err error
		f, err = os.Create(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "建 CSV 失败:", err)
			os.Exit(1)
		}
		defer f.Close()
	}
	w = csv.NewWriter(f)
	defer w.Flush()
	w.Write([]string{"elapsed_s", "rss_kb", "rss_bytes"})

	start := time.Now()
	deadline := start.Add(*duration)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var hist []int64
	var peak int64
	sample := func() {
		var total int64
		for _, pid := range treePIDs(cmd.Process.Pid) {
			if kb, ok := readRSSKb(pid); ok {
				total += kb
			}
		}
		el := time.Since(start).Seconds()
		w.Write([]string{strconv.FormatFloat(el, 'f', 1, 64),
			strconv.FormatInt(total, 10), strconv.FormatInt(total*1024, 10)})
		w.Flush()
		hist = append(hist, total)
		if total > peak {
			peak = total
		}
	}

	tick := time.NewTicker(*interval)
	defer tick.Stop()
	sample()
loop:
	for {
		select {
		case <-tick.C:
			if *duration > 0 && time.Now().After(deadline) {
				break loop
			}
			sample()
		case <-done:
			break loop
		}
	}

	// 到点主动收尾：杀整个进程组不现实（没 Setpgid），杀直接子进程即可——
	// gox dev 的子进程随父退出被回收。
	if cmd.ProcessState == nil {
		cmd.Process.Kill()
		<-done
	}

	last := hist[len(hist)-1]
	fmt.Fprintf(os.Stderr, "\n[rssmon] 样本 %d 个，首 %d kB，末 %d kB，峰值 %d kB，Δ %+d kB\n",
		len(hist), hist[0], last, peak, last-hist[0])
	fmt.Fprintf(os.Stderr, "[rssmon] %s (%.1f–%.1f MB)\n",
		sparkline(hist), float64(hist[0])/1024, float64(peak)/1024)
}
