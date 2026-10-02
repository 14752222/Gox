package perf

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	rdebug "runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ============================================================================
// 帧率 / GC 毛刺基准 (M9 出口验收 2)
//
// 跑法:
//   go test ./perf/                      # 轻量探针 + 重压 (默认)
//   go test ./perf/ -short               # 只跑轻量探针 (跳过重压)
//   go test ./perf/ -run TestUIFrameGCProbe -v
//
// 产物: bench-results/perf-<name>-<UTC 时间>.json (口径见下方 metric_defs 字段)
// ============================================================================

// ---- 测量现场: 假 Surface ----
//
// perfSurface 是 gfx.Surface 的无窗口替身。它与 gfx 包内测试用的 fakeSurface
// 有两点关键差异 (因此**不复用**, 那是 package gfx 内部类型, 外部也拿不到):
//   - WaitEvents **永不阻塞**: 这是"尽量快跑"的压测, 帧节奏由 Go 侧泵循环
//     决定, 不引入任何 sleep; 也让 GC 停顿暴露成帧间隔的尖峰。
//   - 记录每次上屏的时间戳: 帧间隔 = 相邻两次上屏的差。
type perfSurface struct {
	mu        sync.Mutex
	events    chan gfx.Event
	w, h      int
	recording bool
	shows     []time.Time
}

func newPerfSurface(w, h int) *perfSurface {
	if w <= 0 {
		w = 640
	}
	if h <= 0 {
		h = 480
	}
	return &perfSurface{events: make(chan gfx.Event, 256), w: w, h: h}
}

func (s *perfSurface) Show(img *image.RGBA) { s.ShowRegions(img, nil) }

func (s *perfSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	s.mu.Lock()
	if s.recording {
		s.shows = append(s.shows, time.Now())
	}
	s.mu.Unlock()
}

func (s *perfSurface) Size() (int, int) { return s.w, s.h }

// WaitEvents 立即返回 —— 压测不需要等外部事件。
func (s *perfSurface) WaitEvents(maxWait time.Duration) bool { return true }

func (s *perfSurface) Events() <-chan gfx.Event { return s.events }

func (s *perfSurface) push(ev gfx.Event) { s.events <- ev }

func (s *perfSurface) setRecording(b bool) {
	s.mu.Lock()
	s.recording = b
	s.mu.Unlock()
}

func (s *perfSurface) timestamps() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Time, len(s.shows))
	copy(out, s.shows)
	return out
}

// perfFactory 在窗口创建时把 Surface 记下来, 供测试读取上屏时间戳。
type perfFactory struct {
	mu sync.Mutex
	s  *perfSurface
}

func (f *perfFactory) Create(cfg gfx.WindowConfig) (gfx.Surface, error) {
	s := newPerfSurface(cfg.Width, cfg.Height)
	f.mu.Lock()
	f.s = s
	f.mu.Unlock()
	return s, nil
}

func (f *perfFactory) surface() *perfSurface {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.s
}

// ---- 场景与结果 ----

type scenario struct {
	Name    string
	Rows    int // 列表行数
	Updates int // 每帧更新多少个行的 value
	Warmup  int // 预热帧 (不计入统计)
	Measure int // 测量帧
}

type intervalStats struct {
	Count  int     `json:"count"`
	MeanMs float64 `json:"mean_ms"`
	P50Ms  float64 `json:"p50_ms"`
	P95Ms  float64 `json:"p95_ms"`
	P99Ms  float64 `json:"p99_ms"`
	P999Ms float64 `json:"p99_9_ms"`
	MaxMs  float64 `json:"max_ms"`
	// OverBudget167 是帧间隔超过 60fps 单帧预算 (16.7ms) 的帧数 / 占比。
	// 它比孤立的 max 更能反映"持续掉帧"的程度 —— max 会被一次 OS 调度
	// 抖动拉高 (实测同一负载 max 在 2.3ms 与 141ms 之间摆), 单看它会误判。
	OverBudget167   int     `json:"over_16_7ms_count"`
	OverBudgetRatio float64 `json:"over_16_7ms_ratio"`
}

type gcOut struct {
	NumGC          int     `json:"num_gc"`
	PauseTotalMs   float64 `json:"pause_total_ms"`
	PauseP50Ms     float64 `json:"pause_p50_ms"`
	PauseP95Ms     float64 `json:"pause_p95_ms"`
	PauseP99Ms     float64 `json:"pause_p99_ms"`
	PauseMaxMs     float64 `json:"pause_max_ms"`
	PauseSamples   int     `json:"pause_samples"`
	PauseSampleCap int     `json:"pause_sample_cap"`
	Note           string  `json:"note"`
}

type allocOut struct {
	TotalMB      float64 `json:"total_mb"`
	AvgMBPerSec  float64 `json:"avg_mb_per_s"`
	PeakMBPerSec float64 `json:"peak_mb_per_s"`
	PeakWindowMs int     `json:"peak_window_ms"`
	HeapPeakKB   float64 `json:"heap_peak_kb"`
}

type verdictOut struct {
	P95FrameBudgetMs float64 `json:"p95_frame_budget_ms"`
	MaxPauseBudgetMs float64 `json:"max_gc_pause_budget_ms"`
	NoGCStutter      bool    `json:"no_gc_stutter"`
	Reason           string  `json:"reason"`
	// FrameTailNote 解释为什么判定用 p95 而不是 p99/max。
	FrameTailNote string `json:"frame_tail_note"`
}

type report struct {
	GeneratedAt string            `json:"generated_at"`
	Benchmark   string            `json:"benchmark"`
	Command     string            `json:"command"`
	Runs        int               `json:"runs"`
	Platform    map[string]any    `json:"platform"`
	Scenario    map[string]any    `json:"scenario"`
	Frames      intervalStats     `json:"frame_interval_ms"`
	GC          gcOut             `json:"gc"`
	Alloc       allocOut          `json:"alloc"`
	Shows       int               `json:"shows_measured"`
	Verdict     verdictOut        `json:"verdict"`
	MetricDefs  map[string]string `json:"metric_defs"`
}

// ---- 单次场景运行 ----

type memSample struct {
	t          time.Time
	totalAlloc uint64
	heapAlloc  uint64
}

func runScenario(t *testing.T, sc scenario) report {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src := loadScript(t, sc.Rows, sc.Updates)

	factory := &perfFactory{}
	gfx.SetDefaultFactory(factory)
	t.Cleanup(func() { gfx.SetDefaultFactory(nil) })

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	root := gfx.ActiveRoot()
	if root == nil {
		t.Fatalf("脚本没有建立窗口 (ActiveRoot 为空)")
	}
	fake := factory.surface()
	if fake == nil {
		t.Fatalf("窗口工厂没有被调用 (Surface 为空)")
	}

	tickVal, ok := v.Globals().Get("__tick")
	if !ok || !object.IsCallable(tickVal) {
		t.Fatalf("脚本没有定义可调用的 globalThis.__tick")
	}

	// 测量前先跑一次 GC, 把之前测试残留的垃圾清掉, 让 NumGC 基线干净。
	runtime.GC()
	var gcBefore, gcAfter rdebug.GCStats
	// debug.GCStats.Pause 的容量由调用方决定; 这里不设 PauseQuantiles,
	// 只取运行时保留的最近若干次停顿 (见 gcOut.Note)。
	rdebug.ReadGCStats(&gcBefore)
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	// 分配采样 goroutine: 每 2ms 记一次 TotalAlloc/HeapAlloc。
	// 放独立 goroutine 是为了不把 ReadMemStats 的开销插进渲染循环。
	stopSample := make(chan struct{})
	var sampleMu sync.Mutex
	var samples []memSample
	var samplerDone sync.WaitGroup
	samplerDone.Add(1)
	go func() {
		defer samplerDone.Done()
		tk := time.NewTicker(2 * time.Millisecond)
		defer tk.Stop()
		for {
			select {
			case <-stopSample:
				return
			case <-tk.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				sampleMu.Lock()
				samples = append(samples, memSample{t: time.Now(), totalAlloc: ms.TotalAlloc, heapAlloc: ms.HeapAlloc})
				sampleMu.Unlock()
			}
		}
	}()

	measuredStart := time.Time{}
	measuredEnd := time.Time{}
	frame := 0
	stopAt := sc.Warmup + sc.Measure
	var tickErr error

	pump := func(maxWait time.Duration) bool {
		if frame >= stopAt {
			// 收尾: 注入关闭事件, 让 RunTimersWithPump 正常退出。
			fake.push(gfx.Event{Kind: gfx.EventClose})
			return gfx.Pump(maxWait)
		}
		if frame == sc.Warmup {
			measuredStart = time.Now()
			fake.setRecording(true)
		}
		// 一帧: JS 侧高频更新 (信号 → 响应式 effect 标脏) + 真实重绘。
		object.CallFunction(tickVal, nil)
		if err := object.TakeCallbackError(); err != nil {
			tickErr = err
			frame = stopAt // 触发收尾
			fake.push(gfx.Event{Kind: gfx.EventClose})
			return gfx.Pump(maxWait)
		}
		ok := gfx.Pump(0)
		frame++
		if frame == stopAt {
			measuredEnd = time.Now()
		}
		return ok
	}

	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	close(stopSample)
	samplerDone.Wait()
	if tickErr != nil {
		t.Fatalf("__tick 抛错: %v", tickErr)
	}
	if measuredStart.IsZero() || measuredEnd.IsZero() {
		t.Fatalf("测量窗口没有闭合 (start=%v end=%v, frames=%d/%d)", measuredStart, measuredEnd, frame, stopAt)
	}

	rdebug.ReadGCStats(&gcAfter)
	runtime.ReadMemStats(&memAfter)
	sampleMu.Lock()
	samplesCopy := append([]memSample(nil), samples...)
	sampleMu.Unlock()

	// ---- 帧间隔 (ms) ----
	ts := fake.timestamps()
	intervals := make([]float64, 0, len(ts))
	for i := 1; i < len(ts); i++ {
		intervals = append(intervals, float64(ts[i].Sub(ts[i-1]))/float64(time.Millisecond))
	}
	if len(intervals) == 0 {
		t.Fatalf("测量窗口内没有采集到帧间隔 (上屏 %d 次; 期望每帧一次重绘)", len(ts))
	}
	sorted := append([]float64(nil), intervals...)
	sort.Float64s(sorted)
	mean := 0.0
	for _, x := range intervals {
		mean += x
	}
	mean /= float64(len(intervals))

	over167 := 0
	for _, x := range intervals {
		if x > 16.7 {
			over167++
		}
	}
	frames := intervalStats{
		Count:           len(intervals),
		MeanMs:          round3(mean),
		P50Ms:           round3(quantile(sorted, 0.50)),
		P95Ms:           round3(quantile(sorted, 0.95)),
		P99Ms:           round3(quantile(sorted, 0.99)),
		P999Ms:          round3(quantile(sorted, 0.999)),
		MaxMs:           round3(sorted[len(sorted)-1]),
		OverBudget167:   over167,
		OverBudgetRatio: round3(float64(over167) / float64(len(intervals))),
	}

	// ---- GC 停顿 ----
	nGC := int(gcAfter.NumGC - gcBefore.NumGC)
	pauses := gcAfter.Pause
	if nGC < len(pauses) {
		pauses = pauses[:nGC]
	}
	pausesMs := make([]float64, 0, len(pauses))
	for _, p := range pauses {
		pausesMs = append(pausesMs, float64(p)/float64(time.Millisecond))
	}
	sort.Float64s(pausesMs)
	gc := gcOut{
		NumGC:          nGC,
		PauseTotalMs:   round3(float64(gcAfter.PauseTotal-gcBefore.PauseTotal) / float64(time.Millisecond)),
		PauseP50Ms:     round3(quantile(pausesMs, 0.50)),
		PauseP95Ms:     round3(quantile(pausesMs, 0.95)),
		PauseP99Ms:     round3(quantile(pausesMs, 0.99)),
		PauseMaxMs:     round3(lastOrZero(pausesMs)),
		PauseSamples:   len(pausesMs),
		PauseSampleCap: cap(gcAfter.Pause),
		Note: "逐次停顿取自动测量窗口内新增的 GC (debug.GCStats.Pause 前 NumGC 差 项); " +
			"Pause 是运行时保留的最近若干次, 若窗口内 GC 次数超过容量则只覆盖最近一部分",
	}

	// ---- 分配 ----
	elapsed := measuredEnd.Sub(measuredStart).Seconds()
	alloc := allocOut{
		TotalMB:      round3(float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1e6),
		PeakWindowMs: 1000,
	}
	if elapsed > 0 {
		alloc.AvgMBPerSec = round3(float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / elapsed / 1e6)
	}
	alloc.PeakMBPerSec, alloc.HeapPeakKB = peakAlloc(samplesCopy)

	// ---- 判定 ----
	verdict := makeVerdict(frames, gc)

	// 结构性硬闸门: 只拦"数量级劣化"与"没真的跑起来", 不拦正常抖动。
	if frames.P99Ms > 200 {
		t.Fatalf("p99 帧间隔 %.1fms 超过 200ms 上限 (结构性问题, 不是抖动)", frames.P99Ms)
	}
	if gc.PauseMaxMs > 100 {
		t.Fatalf("最大 GC 停顿 %.1fms 超过 100ms 上限", gc.PauseMaxMs)
	}
	if len(ts) < sc.Measure*9/10 {
		t.Fatalf("测量帧上屏次数 %d 明显少于测量帧数 %d (渲染路径可能没每帧重绘)", len(ts), sc.Measure)
	}

	rep := report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Benchmark:   "perf/" + sc.Name,
		Command:     fmt.Sprintf("go test ./perf/ -run %s -v", currentTestName(t)),
		Runs:        1,
		Platform:    platformInfo(),
		Scenario: map[string]any{
			"rows":              sc.Rows,
			"updates_per_frame": sc.Updates,
			"frames_warmup":     sc.Warmup,
			"frames_measured":   sc.Measure,
			"gogc":              gogcPercent(),
		},
		Frames:  frames,
		GC:      gc,
		Alloc:   alloc,
		Shows:   len(ts),
		Verdict: verdict,
		MetricDefs: map[string]string{
			"frame_interval_ms":                          "相邻两次 Surface.ShowRegions(上屏) 的时间差; 含 JS 更新+响应式 effect+Layout+脏区 Draw+GC 停顿, 是掉帧的用户侧代理量",
			"frame_interval_ms.p95":                      "判定用尾部口径: 95% 的帧在 60fps 预算内; 比 p99/max 稳健 (后两者被 OS 调度主导)",
			"frame_interval_ms.p99":                      "尾部 1% 帧间隔; 报告用, 非独占运行时受 OS 调度波动大, 不参与判定",
			"frame_interval_ms.max":                      "观测到的最大帧间隔; 参考值, 会被 OS 调度抖动主导, 不参与判定 (见 verdict.frame_tail_note)",
			"frame_interval_ms.over_16_7ms_ratio":        "帧间隔超过 60fps 单帧预算 (16.7ms) 的帧占比",
			"gc.num_gc":                                  "测量窗口内新增的 GC 次数 (debug.GCStats.NumGC 差分; 多次运行取和)",
			"gc.pause_*":                                 "测量窗口内新增的逐次 STW 停顿的分布 (非平均值; 多次运行取各分位中位, max 取最坏)",
			"alloc.avg_mb_per_s":                         "窗口内 TotalAlloc 差分 / 窗口墙钟秒数 (多次运行取中位)",
			"alloc.peak_mb_per_s":                        "2ms 采样下, 任意 1s 滑动窗口内 TotalAlloc 差分的最大速率 (多次运行取中位)",
			"alloc.heap_peak_kb":                         "采样期间 HeapAlloc 的峰值 (多次运行取最坏)",
			"verdict.no_gc_stutter":                      "p95≤16.7ms 且 最大 GC 停顿≤5ms 时判为'无 GC 毛刺' (阈值理由见 docs/performance.md §5.3)",
			"runs":                                       "本报告聚合的重复运行次数; 各分位取跨运行中位, max/heap 峰值取最坏",
		},
	}
	return rep
}

// peakAlloc 从采样序列算 (峰值分配速率 MB/s, 堆峰值 KB)。
func peakAlloc(samples []memSample) (float64, float64) {
	if len(samples) < 2 {
		return 0, 0
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i].t.Before(samples[j].t) })
	var heapPeak uint64
	for _, s := range samples {
		if s.heapAlloc > heapPeak {
			heapPeak = s.heapAlloc
		}
	}
	best := 0.0
	j := 0
	for i := 1; i < len(samples); i++ {
		for j < i && samples[i].t.Sub(samples[j].t) > time.Second {
			j++
		}
		if j == i {
			continue
		}
		dt := samples[i].t.Sub(samples[j].t).Seconds()
		if dt < 0.2 { // 窗口太短, 速率噪声大
			continue
		}
		if samples[i].totalAlloc < samples[j].totalAlloc {
			continue
		}
		rate := float64(samples[i].totalAlloc-samples[j].totalAlloc) / dt / 1e6
		if rate > best {
			best = rate
		}
	}
	return round3(best), round3(float64(heapPeak) / 1024)
}

// makeVerdict 由帧间隔分布与 GC 停顿分布给出"无 GC 毛刺"判定。
//
// 口径 (为什么用 p95 + GC 停顿判, 不用 p99/max):
//   在**无独占**的桌面机上, 帧间隔的尾部 (p99/p99.9/max) 由 OS 调度抖动
//   主导。同日多次实测同一份重压负载: p99 跨运行在 6.7~36.8ms、max 在
//   109~367ms 间摆动, 而同期 GC 停顿 max 始终 <2.6ms。也就是说
//   p99/max 反映的是"进程被抢了 CPU", 不是"GC 毛刺"。
//   因此判定取两个稳健量:
//     1) p95 帧间隔 ≤ 16.7ms  (95% 的帧在 60fps 单帧预算内; 跨运行实测
//        p95 稳定在 4~9ms, 比 p99 稳得多);
//     2) 单次 GC 停顿 max ≤ 5ms (直接量 GC 本身, 与调度噪声无关)。
//   p99 / p99.9 / max 与"超 16.7ms 帧占比"仍完整报告, 供人工判断。
func makeVerdict(frames intervalStats, gc gcOut) verdictOut {
	const (
		p95BudgetMs   = 16.7
		pauseBudgetMs = 5.0
	)
	v := verdictOut{
		P95FrameBudgetMs: p95BudgetMs,
		MaxPauseBudgetMs: pauseBudgetMs,
		FrameTailNote: fmt.Sprintf("p99=%.2fms p99.9=%.2fms max=%.2fms 仅供参考、不参与判定: "+
			"无独占运行时它们由 OS 调度主导 (同负载 3 次实测 p99 在 7~37ms、max 在 109~367ms 间摆动), "+
			"而同期 GC 停顿 max 仅 %.3fms", frames.P99Ms, frames.P999Ms, frames.MaxMs, gc.PauseMaxMs),
	}
	v.NoGCStutter = frames.P95Ms <= p95BudgetMs && gc.PauseMaxMs <= pauseBudgetMs
	if v.NoGCStutter {
		v.Reason = fmt.Sprintf("p95 帧间隔 %.2fms ≤ %.1f 且 最大 GC 停顿 %.3fms ≤ %.1f",
			frames.P95Ms, p95BudgetMs, gc.PauseMaxMs, pauseBudgetMs)
		return v
	}
	reasons := []string{}
	if frames.P95Ms > p95BudgetMs {
		reasons = append(reasons, fmt.Sprintf("p95 帧间隔 %.2fms > %.1f", frames.P95Ms, p95BudgetMs))
	}
	if gc.PauseMaxMs > pauseBudgetMs {
		reasons = append(reasons, fmt.Sprintf("最大 GC 停顿 %.3fms > %.1f", gc.PauseMaxMs, pauseBudgetMs))
	}
	v.Reason = "未达标: " + strings.Join(reasons, "; ")
	return v
}

// aggregateReports 把 reps 次单跑聚合成一份报告。
//
// 聚合口径 (与 docs/performance.md §5.3 一致):
//   - 各分位 (p50/p95/p99/p99.9) 与占比: 取跨运行的**中位数** —— 单次运行的
//     尾部估计在无独占机器上不稳, 中位数比"其中一次"更可复现;
//   - max / 堆峰值 / GC 停顿 max: 取跨运行的**最坏值** (安全侧);
//   - GC 次数与停顿总计: 多次运行的**和**;
//   - 判定按聚合后的 p95 中位与 GC 停顿最坏值重新计算。
func aggregateReports(runs []report) report {
	base := runs[0]
	fram := intervalStats{
		Count:           base.Frames.Count,
		MeanMs:          round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.MeanMs }))),
		P50Ms:           round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.P50Ms }))),
		P95Ms:           round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.P95Ms }))),
		P99Ms:           round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.P99Ms }))),
		P999Ms:          round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.P999Ms }))),
		MaxMs:           round3(maxFloat(collectFrames(runs, func(f intervalStats) float64 { return f.MaxMs }))),
		OverBudgetRatio: round3(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return f.OverBudgetRatio }))),
	}
	fram.OverBudget167 = int(medianFloat(collectFrames(runs, func(f intervalStats) float64 { return float64(f.OverBudget167) })))

	g := gcOut{
		NumGC:          sumInt(runs),
		PauseTotalMs:   round3(sumFloat(runs, func(r report) float64 { return r.GC.PauseTotalMs })),
		PauseP50Ms:     round3(medianFloat(collectGC(runs, func(g gcOut) float64 { return g.PauseP50Ms }))),
		PauseP95Ms:     round3(medianFloat(collectGC(runs, func(g gcOut) float64 { return g.PauseP95Ms }))),
		PauseP99Ms:     round3(medianFloat(collectGC(runs, func(g gcOut) float64 { return g.PauseP99Ms }))),
		PauseMaxMs:     round3(maxFloat(collectGC(runs, func(g gcOut) float64 { return g.PauseMaxMs }))),
		PauseSampleCap: base.GC.PauseSampleCap,
		Note: "逐次停顿取自动测量窗口内新增的 GC; 多次运行取各分位中位、max 取最坏 (见 aggregateReports)",
	}
	sampleSum := 0
	for _, r := range runs {
		sampleSum += r.GC.PauseSamples
	}
	g.PauseSamples = sampleSum

	alloc := allocOut{
		TotalMB:      round3(medianFloat(collectAlloc(runs, func(a allocOut) float64 { return a.TotalMB }))),
		AvgMBPerSec:  round3(medianFloat(collectAlloc(runs, func(a allocOut) float64 { return a.AvgMBPerSec }))),
		PeakMBPerSec: round3(medianFloat(collectAlloc(runs, func(a allocOut) float64 { return a.PeakMBPerSec }))),
		PeakWindowMs: base.Alloc.PeakWindowMs,
		HeapPeakKB:   round3(maxFloat(collectAlloc(runs, func(a allocOut) float64 { return a.HeapPeakKB }))),
	}

	rep := base
	rep.GeneratedAt = time.Now().UTC().Format(time.RFC3339)
	rep.Runs = len(runs)
	rep.Frames = fram
	rep.GC = g
	rep.Alloc = alloc
	rep.Verdict = makeVerdict(fram, g)
	return rep
}

func collectFrames(runs []report, f func(intervalStats) float64) []float64 {
	out := make([]float64, 0, len(runs))
	for _, r := range runs {
		out = append(out, f(r.Frames))
	}
	return out
}
func collectGC(runs []report, f func(gcOut) float64) []float64 {
	out := make([]float64, 0, len(runs))
	for _, r := range runs {
		out = append(out, f(r.GC))
	}
	return out
}
func collectAlloc(runs []report, f func(allocOut) float64) []float64 {
	out := make([]float64, 0, len(runs))
	for _, r := range runs {
		out = append(out, f(r.Alloc))
	}
	return out
}
func sumInt(runs []report) int {
	n := 0
	for _, r := range runs {
		n += r.GC.NumGC
	}
	return n
}
func sumFloat(runs []report, f func(report) float64) float64 {
	s := 0.0
	for _, r := range runs {
		s += f(r)
	}
	return s
}
func medianFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	return quantile(s, 0.5)
}
func maxFloat(vals []float64) float64 {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// ---- 两个用例 ----

// TestUIFrameGCProbe 是轻量探针 (默认与 -short 都跑), 用于日常/CI 快速回归。
// 单次运行即可 (轻量、快)。
func TestUIFrameGCProbe(t *testing.T) {
	sc := scenario{Name: "UIFrameGCProbe", Rows: 300, Updates: 30, Warmup: 60, Measure: 600}
	runAndReport(t, "perf-probe", sc, 1)
}

// TestUIFrameGCStress 是重压 (默认跑, -short 跳过): 更大列表 + 更高更新频率,
// 专门把 GC 频率顶上去, 逼出尾部停顿。默认重复 3 次取聚合 (见 aggregateReports),
// 可用 PERF_STRESS_REPS 覆盖。
func TestUIFrameGCStress(t *testing.T) {
	if testing.Short() {
		t.Skip("短模式跳过 UI 重压测 (-short)")
	}
	sc := scenario{Name: "UIFrameGCStress", Rows: 800, Updates: 200, Warmup: 120, Measure: 2400}
	reps := 3
	if v := os.Getenv("PERF_STRESS_REPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			reps = n
		}
	}
	runAndReport(t, "perf-stress", sc, reps)
}

// runAndReport 跑 reps 次场景, 逐次打印, 再聚合成一份 JSON 落盘。
func runAndReport(t *testing.T, prefix string, sc scenario, reps int) {
	t.Helper()
	if reps < 1 {
		reps = 1
	}
	runs := make([]report, 0, reps)
	for i := 0; i < reps; i++ {
		r := runScenario(t, sc)
		r.Runs = 1
		logRun(t, prefix, i+1, reps, r)
		runs = append(runs, r)
	}
	rep := aggregateReports(runs)
	rep.Command = fmt.Sprintf("go test ./perf/ -run %s -v", currentTestName(t))
	logReport(t, rep)
	writeReport(t, prefix, rep)
}

func logRun(t *testing.T, prefix string, i, n int, rep report) {
	t.Helper()
	if n <= 1 {
		return
	}
	f := rep.Frames
	t.Logf("[%s 第 %d/%d 次] p50=%.2f p95=%.2f p99=%.2f max=%.2f | GC 次数=%d 停顿 max=%.3f",
		prefix, i, n, f.P50Ms, f.P95Ms, f.P99Ms, f.MaxMs, rep.GC.NumGC, rep.GC.PauseMaxMs)
}

// ---- 辅助 ----

func logReport(t *testing.T, rep report) {
	t.Helper()
	f := rep.Frames
	g := rep.GC
	a := rep.Alloc
	t.Logf("场景: %v", rep.Scenario)
	t.Logf("帧间隔(ms): p50=%.2f p95=%.2f p99=%.2f p99.9=%.2f max=%.2f mean=%.2f (n=%d, 超16.7ms=%d/%.2f%%)",
		f.P50Ms, f.P95Ms, f.P99Ms, f.P999Ms, f.MaxMs, f.MeanMs, f.Count, f.OverBudget167, f.OverBudgetRatio*100)
	t.Logf("GC: 次数=%d 停顿总=%.2fms p50=%.3f p95=%.3f p99=%.3f max=%.3f (样本 %d)",
		g.NumGC, g.PauseTotalMs, g.PauseP50Ms, g.PauseP95Ms, g.PauseP99Ms, g.PauseMaxMs, g.PauseSamples)
	t.Logf("分配: 总=%.1fMB 平均=%.1fMB/s 峰值=%.1fMB/s 堆峰值=%.0fKB",
		a.TotalMB, a.AvgMBPerSec, a.PeakMBPerSec, a.HeapPeakKB)
	if rep.Verdict.NoGCStutter {
		t.Logf("判定: ✅ 无 GC 毛刺 (%s)", rep.Verdict.Reason)
	} else {
		t.Logf("判定: ❌ %s", rep.Verdict.Reason)
	}
}

func writeReport(t *testing.T, prefix string, rep report) {
	t.Helper()
	dir := filepath.Join("..", "bench-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建 bench-results: %v", err)
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.json", prefix, stamp))
	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("序列化报告: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
	t.Logf("结果已存档: %s", path)
}

// loadScript 读 testdata/perf/frame_load.js 并替换行数/更新量占位符。
func loadScript(t *testing.T, rows, updates int) string {
	t.Helper()
	path := filepath.Join("..", "testdata", "perf", "frame_load.js")
	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取压测脚本: %v", err)
	}
	src := string(buf)
	src = strings.ReplaceAll(src, "__ROWS__", strconv.Itoa(rows))
	src = strings.ReplaceAll(src, "__UPDATES__", strconv.Itoa(updates))
	return src
}

func platformInfo() map[string]any {
	info := map[string]any{
		"os":     runtime.GOOS + "/" + runtime.GOARCH,
		"cpus":   runtime.NumCPU(),
		"go":     runtime.Version(),
		"gogc":   gogcPercent(),
	}
	if m := extCmd("sysctl", "-n", "machdep.cpu.brand_string"); m != "" {
		info["cpu"] = m
	} else if m := linuxCPUModel(); m != "" {
		info["cpu"] = m
	}
	if m := extCmd("sysctl", "-n", "hw.memsize"); m != "" {
		if n, err := strconv.ParseInt(strings.TrimSpace(m), 10, 64); err == nil {
			info["mem_bytes"] = n
		}
	}
	return info
}

func extCmd(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func linuxCPUModel() string {
	buf, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(buf), "\n") {
		if strings.HasPrefix(line, "model name") {
			if i := strings.Index(line, ":"); i >= 0 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

// gogcPercent 读当前 GOGC (用一次 -1 往返拿到旧值再还原; 不改动最终设置)。
func gogcPercent() int {
	old := rdebug.SetGCPercent(-1)
	rdebug.SetGCPercent(old)
	return old
}

func currentTestName(t *testing.T) string {
	t.Helper()
	return strings.TrimPrefix(t.Name(), "Test")
}

func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if q <= 0 {
		return sorted[0]
	}
	if q >= 1 {
		return sorted[len(sorted)-1]
	}
	idx := q * float64(len(sorted)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(idx-float64(lo))
}

func lastOrZero(sorted []float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	return sorted[len(sorted)-1]
}

func round3(x float64) float64 {
	if math.IsNaN(x) || math.IsInf(x, 0) {
		return 0
	}
	return math.Round(x*1000) / 1000
}
