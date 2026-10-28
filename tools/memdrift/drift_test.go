//go:build linux

package memdrift

import (
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	_ "github.com/14752222/Gox/gfx/bind" // 注册 gx/theme 等 JS 绑定模块
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ============================================================================
// 长时漂移测试 (T3-3)
//
// 跑法:
//   go test ./tools/memdrift/ -v                       # 默认 ~2 分钟
//   go test ./tools/memdrift/ -short                   # 跳过（长跑不进短测）
//   MEMDRIFT_ITERS=300 go test ./tools/memdrift/ -v    # 缩短自查
//
// 产物: bench-results/memdrift-<UTC>.json（字段口径见 report.metric_defs）
// ============================================================================

// 默认参数。轮数与每轮帧数一起决定长跑时长；7500 轮 ×（200 行换批 + 6 帧）
// 在本机约 2 分钟（实测见 docs/performance.md §11），与"挂一段时间"相比
// 只是时间压缩，不是负载压缩：每轮都真的走 Layout + Draw + 上屏。
const (
	defaultIters     = 7500
	defaultWarmupPct = 30  // 前 30% 只预热，不参与判定
	defaultSampleN   = 30  // 整个跑程的采样次数（每 ITERS/SampleN 轮 GC 后采一次）
	defaultFrames    = 6   // 每轮渲染帧数
	defaultRows      = 200 // 每轮换批的列表行数
	defaultUpdates   = 4   // 每帧更新的行内 signal 个数

	// 阈值：本次实测 + 余量（推导见 docs/performance.md §11）。
	// HeapAlloc 是"GC 后活对象"，噪声比 RSS 小，卡得紧；
	// VmRSS 受 Go 堆向 OS 归还内存的节奏影响，留更宽的余量。
	defaultMaxHeapGrowthKB = 2048  // 2 MB
	defaultMaxRSSGrowthKB  = 16384 // 16 MB
)

// evalSnippet 是每轮"求值"压力的来源：新建 VM、解析、执行、造一批对象。
// 它独立于渲染用的常驻 VM —— 对应"热重载/脚本求值"这条分配路径。
const evalSnippet = `
let sum = 0;
const arr = [];
for (let i = 0; i < 200; i++) arr.push({ id: i, v: i * 2 });
for (const x of arr) sum += x.v;
sum;
`

// driftSurface 是 gfx.Surface 的无窗口替身：只记录尺寸，不碰真实图形后端。
// 与 tools/perf 的同名替身刻意各写一份——两处都是包内部类型，跨包复用只会
// 逼出一个"测试专用导出 API"，不值得。
type driftSurface struct {
	w, h   int
	events chan gfx.Event
}

func (s *driftSurface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

func (s *driftSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {}

func (s *driftSurface) Size() (int, int) { return s.w, s.h }

func (s *driftSurface) WaitEvents(maxWait time.Duration) bool { return true }

func (s *driftSurface) Events() <-chan gfx.Event { return s.events }

// driftFactory 只建窗口，不持有别的引用（整个跑程只建一个）。
type driftFactory struct{}

func (f *driftFactory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	return &driftSurface{w: cfg.Width, h: cfg.Height, events: make(chan gfx.Event, 8)}, nil
}

// loadScript 读负载脚本并代入行数/更新量。
func loadScript(t *testing.T) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "memdrift", "churn.js")
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取负载脚本: %v", err)
	}
	src := string(buf)
	src = strings.ReplaceAll(src, "__ROWS__", strconv.Itoa(defaultRows))
	src = strings.ReplaceAll(src, "__UPDATES__", strconv.Itoa(defaultUpdates))
	return src
}

// globalFn 取脚本里的全局函数，缺失或不可调用直接失败。
func globalFn(t *testing.T, v *vm.VM, name string) object.Value {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok || !object.IsCallable(val) {
		t.Fatalf("脚本缺少可调用的 globalThis.%s", name)
	}
	return val
}

// sample 是一次 GC 后的内存快照。
type sample struct {
	Cycle        int     `json:"cycle"`
	RSSKb        int64   `json:"rss_kb"`
	HeapKB       float64 `json:"heap_alloc_kb"`
	TotalAllocKB float64 `json:"total_alloc_kb"`
}

// readVmRSSKb 读本进程 VmRSS（kB）；读不到返回 -1。
func readVmRSSKb() int64 {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) < 2 {
				return -1
			}
			kb, err := strconv.ParseInt(f[1], 10, 64)
			if err != nil {
				return -1
			}
			return kb
		}
	}
	return -1
}

// snapshot 强制 GC 后采样。
//
// 为什么必须 GC：泄漏的定义是"GC 之后仍然活着的对象"。不 GC 只能测到
// "这一轮分配了多少"，而那正是 TotalAlloc 的事，两者分开报。
func snapshot(cycle int) sample {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return sample{
		Cycle:        cycle,
		RSSKb:        readVmRSSKb(),
		HeapKB:       float64(ms.HeapAlloc) / 1024,
		TotalAllocKB: float64(ms.TotalAlloc) / 1024,
	}
}

// trend 对稳态段的 (cycle, value) 做最小二乘，返回每分钟斜率。
//
// 用斜率而不是"末值−首值"：单点采样会被一次 GC 时机 / OS 回收节奏带偏，
// 回归能摊掉噪声；单位换算成每分钟，与 rss-curve.sh 的 --max-slope 同口径。
func trend(samples []sample, pick func(sample) float64, perCycle time.Duration) float64 {
	if len(samples) < 2 || perCycle <= 0 {
		return 0
	}
	var sx, sy float64
	for _, s := range samples {
		sx += float64(s.Cycle)
		sy += pick(s)
	}
	n := float64(len(samples))
	mx, my := sx/n, sy/n
	var num, den float64
	for _, s := range samples {
		dx := float64(s.Cycle) - mx
		num += dx * (pick(s) - my)
		den += dx * dx
	}
	if den == 0 {
		return 0
	}
	return num / den * (float64(time.Minute) / float64(perCycle))
}

type report struct {
	GeneratedAt string            `json:"generated_at"`
	Benchmark   string            `json:"benchmark"`
	Command     string            `json:"command"`
	Platform    map[string]any    `json:"platform"`
	Params      map[string]any    `json:"params"`
	Steady      map[string]any    `json:"steady"`
	Verdict     map[string]any    `json:"verdict"`
	MetricDefs  map[string]string `json:"metric_defs"`
	Samples     []sample          `json:"samples"`
}

func envInt(name string, def int) int {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return def
}

func envKB(name string, def int64) int64 {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// TestRSSDrift 是长时漂移闸门：稳态段 HeapAlloc / VmRSS 的增长必须低于阈值。
func TestRSSDrift(t *testing.T) {
	if testing.Short() {
		t.Skip("长时漂移测试（约 2 分钟）；-short 跳过")
	}

	iters := envInt("MEMDRIFT_ITERS", defaultIters)
	warmup := iters * defaultWarmupPct / 100
	if w := envInt("MEMDRIFT_WARMUP", 0); w > 0 {
		warmup = w
	}
	if warmup >= iters {
		warmup = iters / 3
	}
	sampleN := defaultSampleN
	if iters/10 < sampleN {
		sampleN = iters / 10
	}
	if sampleN < 4 {
		sampleN = 4
	}
	every := iters / sampleN
	if every < 1 {
		every = 1
	}

	maxHeapKB := envKB("MEMDRIFT_MAX_HEAP_KB", defaultMaxHeapGrowthKB)
	maxRSSKB := envKB("MEMDRIFT_MAX_RSS_KB", defaultMaxRSSGrowthKB)

	src := loadScript(t)
	factory := &driftFactory{}
	gfx.SetDefaultFactory(factory)
	t.Cleanup(func() { gfx.SetDefaultFactory(nil) })

	// 建一个常驻 VM：窗口只在这里挂一次，后面全是帧循环 + 组件换批。
	// 关窗放在跑程末尾（__close），不在每轮做（见 testdata/memdrift/churn.js
	// 头部对"为什么不是每轮新建窗口"的说明）。
	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if gfx.ActiveRoot() == nil {
		t.Fatalf("脚本没有建立窗口（ActiveRoot 为空）")
	}
	churn := globalFn(t, v, "__churn")
	tick := globalFn(t, v, "__tick")
	closeWin := globalFn(t, v, "__close")

	samples := []sample{snapshot(0)}
	var jsErr error
	cycle := 0
	frames := 0

	start := time.Now()
	// 泵回调就是"一轮"：求值（独立 VM）+ 组件换批 + 若干帧真实渲染。
	// 所有 JS 调用都在泵内做——currentVM 只在 RunTimersWithPump 期间注册，
	// 在泵外调 object.CallFunction 会静默返回 undefined（见 vm/callback.go）。
	pump := func(maxWait time.Duration) bool {
		if cycle >= iters {
			// 收尾：显式关窗 → Post → DrainTasks 摘句柄；全部窗口关闭后
			// 泵返回 false，RunTimersWithPump 随之结束。
			object.CallFunction(closeWin, nil)
			return gfx.Pump(maxWait)
		}
		if _, err := vm.EvalVM(evalSnippet); err != nil {
			jsErr = fmt.Errorf("第 %d 轮求值失败: %w", cycle+1, err)
			object.CallFunction(closeWin, nil)
			return gfx.Pump(maxWait)
		}
		object.CallFunction(churn, nil)
		if err := object.TakeCallbackError(); err != nil {
			jsErr = fmt.Errorf("第 %d 轮换批失败: %w", cycle+1, err)
		}
		for f := 0; f < defaultFrames && jsErr == nil; f++ {
			object.CallFunction(tick, nil)
			if err := object.TakeCallbackError(); err != nil {
				jsErr = fmt.Errorf("第 %d 轮第 %d 帧更新失败: %w", cycle+1, f+1, err)
				break
			}
			gfx.Pump(0)
			frames++
		}
		cycle++
		if cycle%every == 0 || cycle == iters {
			s := snapshot(cycle)
			if s.RSSKb < 0 {
				jsErr = fmt.Errorf("读不到 /proc/self/status 的 VmRSS")
			} else {
				samples = append(samples, s)
			}
		}
		return jsErr == nil
	}

	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	elapsed := time.Since(start)
	if jsErr != nil {
		t.Fatalf("%v", jsErr)
	}
	if cycle < iters {
		t.Fatalf("只跑了 %d/%d 轮（泵提前结束）", cycle, iters)
	}
	if gfx.WindowCount() != 0 {
		t.Fatalf("跑程结束后仍有 %d 个窗口未关闭", gfx.WindowCount())
	}
	perCycle := elapsed / time.Duration(iters)

	// 稳态段 = 预热之后的采样点。基线样本（cycle 0）不参与趋势：它测的是
	// 测试进程启动时的存量，与循环负载不是同一口径。
	var steady []sample
	for _, s := range samples {
		if s.Cycle > warmup {
			steady = append(steady, s)
		}
	}
	if len(steady) < 4 {
		t.Fatalf("稳态段采样点不足（%d 个）；调大 MEMDRIFT_ITERS 或调小 MEMDRIFT_WARMUP", len(steady))
	}

	first, last := steady[0], steady[len(steady)-1]
	heapGrowth := last.HeapKB - first.HeapKB
	rssGrowth := float64(last.RSSKb - first.RSSKb)
	heapSlope := trend(steady, func(s sample) float64 { return s.HeapKB }, perCycle)
	rssSlope := trend(steady, func(s sample) float64 { return float64(s.RSSKb) }, perCycle)

	allocRateMB := 0.0
	if elapsed > 0 {
		allocRateMB = (last.TotalAllocKB - samples[0].TotalAllocKB) / 1024 / elapsed.Seconds()
	}
	heapOK := heapGrowth <= float64(maxHeapKB)
	rssOK := rssGrowth <= float64(maxRSSKB)

	rep := report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Benchmark:   "memdrift",
		Command:     "go test ./tools/memdrift/ -run TestRSSDrift -v",
		Platform: map[string]any{
			"os": runtime.GOOS + "/" + runtime.GOARCH, "cpus": runtime.NumCPU(),
			"go": runtime.Version(), "gogc": os.Getenv("GOGC"),
		},
		Params: map[string]any{
			"iters": iters, "warmup": warmup, "sample_every": every,
			"frames_per_cycle": defaultFrames, "rendered_frames": frames,
			"rows": defaultRows, "updates_per_frame": defaultUpdates,
			"elapsed_s": elapsed.Seconds(), "ms_per_cycle": float64(perCycle.Milliseconds()),
		},
		Steady: map[string]any{
			"first_cycle": first.Cycle, "last_cycle": last.Cycle,
			"heap_first_kb": round2(first.HeapKB), "heap_last_kb": round2(last.HeapKB),
			"heap_growth_kb": round2(heapGrowth), "heap_slope_kb_per_min": round2(heapSlope),
			"rss_first_kb": first.RSSKb, "rss_last_kb": last.RSSKb,
			"rss_growth_kb": rssGrowth, "rss_slope_kb_per_min": round2(rssSlope),
			"alloc_mb_per_s": round2(allocRateMB),
		},
		Verdict: map[string]any{
			"max_heap_growth_kb": maxHeapKB, "max_rss_growth_kb": maxRSSKB,
			"heap_ok": heapOK, "rss_ok": rssOK, "pass": heapOK && rssOK,
		},
		MetricDefs: map[string]string{
			"heap_growth_kb":     "稳态段末次采样的 HeapAlloc 减首次；每次采样前强制 runtime.GC，只算 GC 后活对象",
			"rss_growth_kb":      "同上，取 /proc/self/status 的 VmRSS（进程实际占用，含未归还 OS 的堆页）",
			"*_slope_kb_per_min": "稳态段对 (cycle, 值) 做最小二乘，再按每轮耗时换算成每分钟",
			"steady":             "预热段（默认前 30% 轮次）之后；启动期一次性增长不计入",
		},
		Samples: samples,
	}
	if err := writeReport(rep); err != nil {
		t.Logf("写报告失败（不影响判定）: %v", err)
	}

	t.Logf("轮次 %d（预热 %d，每 %d 轮采样），耗时 %.1fs（%.1fms/轮，共 %d 帧），分配 %.1f MB/s",
		iters, warmup, every, elapsed.Seconds(), float64(perCycle.Milliseconds()), frames, allocRateMB)
	t.Logf("稳态段 %d→%d：HeapAlloc %.0f→%.0f kB（Δ%+.0f，%+.1f kB/min），VmRSS %d→%d kB（Δ%+.0f，%+.1f kB/min）",
		first.Cycle, last.Cycle, first.HeapKB, last.HeapKB, heapGrowth, heapSlope,
		first.RSSKb, last.RSSKb, rssGrowth, rssSlope)
	t.Logf("MEMDRIFT {\"iters\":%d,\"heap_growth_kb\":%.0f,\"rss_growth_kb\":%.0f,\"heap_slope_kb_per_min\":%.1f,\"rss_slope_kb_per_min\":%.1f,\"pass\":%t}",
		iters, heapGrowth, rssGrowth, heapSlope, rssSlope, heapOK && rssOK)

	if !heapOK {
		t.Errorf("HeapAlloc 增长 %.0f kB 超过阈值 %d kB", heapGrowth, maxHeapKB)
	}
	if !rssOK {
		t.Errorf("VmRSS 增长 %.0f kB 超过阈值 %d kB", rssGrowth, maxRSSKB)
	}
}

func round2(v float64) float64 {
	return float64(int64(v*100+0.5)) / 100
}

// writeReport 落一份 JSON 到 bench-results/（与 tools/perf 的产物同目录，
// 便于同一次排查里横向对照）。
func writeReport(rep report) error {
	if err := os.MkdirAll(filepath.Join("..", "..", "bench-results"), 0o755); err != nil {
		return err
	}
	name := "memdrift-" + time.Now().UTC().Format("20060102-150405") + ".json"
	path := filepath.Join("..", "..", "bench-results", name)
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}
