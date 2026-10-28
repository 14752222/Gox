package perf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	_ "github.com/14752222/Gox/gfx/bind" // 注册 gx/theme 等 JS 绑定模块
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ============================================================================
// 冷启动到首帧基准 (T2-5, 2026-10-28)
//
// 口径: **进程内**冷启动 —— 从 vm.EvalVM 之前一刻起表, 到 perfSurface 上
// 第一次 ShowRegions (上屏) 为止。含:
//   - 解释器初始化 + 全局对象构建
//   - 脚本解析 / 编译 / 求值 (含 ES 模块装载: gx/gfx、gx/theme、gx/dev …)
//   - 窗口创建 + 元素树挂载 + 首次 Layout + 首次 Draw + 上屏
//
// 不含: 进程 exec、Go runtime 启动、二进制动态链接 —— 那部分属于
// "进程级冷启动", 由 scripts/coldstart.sh (外部 wall time + 真实窗口)
// 另测, 两个数字**不可混用** (见 docs/performance.md §12)。
//
// 为什么用 perfSurface 而不是真窗口: 与 §5 帧率基准同一理由 —— CI 无显示
// 环境也要能跑; 且无窗口管理器干扰, 数字更稳。真窗口口径的数字由
// scripts/coldstart.sh 提供 (有显示环境/Xvfb 时跑)。
//
// 跑法:
//   go test ./tools/perf/ -run TestColdStartToFirstFrame -v
//   PERF_COLDSTART_REPS=15 go test ./tools/perf/ -run ColdStart -v   # 加密重复
//
// 产物: bench-results/coldstart-<UTC时间>.json
// ============================================================================

// coldRun 是单次冷启动的分段耗时。
//
// 关键事实: 首帧发生在 render() 求值**期间** —— gfx.Mount 末尾会同步调
// a.redraw() (gfx/render.go 的"首帧"注释), 所以 first_frame_ms ≤ eval_ms。
type coldRun struct {
	// EvalMs: t0 → EvalVM 返回 (解释器初始化 + 脚本求值 + 窗口挂载, 含首帧)。
	EvalMs float64 `json:"eval_ms"`
	// FirstFrameMs: t0 → 首次上屏 (对外主口径; 发生在求值内)。
	FirstFrameMs float64 `json:"first_frame_ms"`
	// PostFrameEvalMs: 首次上屏 → EvalVM 返回 (首帧之后求值里的剩余工作,
	// 应接近 0; 若明显大于 0 说明首帧路径变了, 值得人工看一眼)。
	PostFrameEvalMs float64 `json:"post_frame_eval_ms"`
	// TotalAllocMB: 冷启动期间 (t0 → 首次上屏) 的累计分配字节数。
	TotalAllocMB float64 `json:"total_alloc_mb"`
}

type coldstartStats struct {
	EvalMs          float64 `json:"eval_ms"`
	FirstFrameMs    float64 `json:"first_frame_ms"`
	PostFrameEvalMs float64 `json:"post_frame_eval_ms"`
	TotalAllocMB    float64 `json:"total_alloc_mb"`
}

type coldstartReport struct {
	GeneratedAt string            `json:"generated_at"`
	Benchmark   string            `json:"benchmark"`
	Command     string            `json:"command"`
	Runs        int               `json:"runs"`
	Platform    map[string]any    `json:"platform"`
	Scenario    map[string]any    `json:"scenario"`
	Phases      coldstartStats    `json:"phases_ms"`
	All         []coldRun         `json:"all_runs"`
	MetricDefs  map[string]string `json:"metric_defs"`
	Note        string            `json:"note"`
}

// runColdStart 跑一次"冷启动到首帧"。每次都新建 VM / 新建 Surface 替身,
// 保证是真正的"冷"启动 (不留上一次的窗口、定时器与缓存)。
func runColdStart(t *testing.T) coldRun {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src := firstFrameScript(t)

	// recordOnInit: 首帧发生在 EvalVM 期间 (Mount → a.redraw()), Surface
	// 必须出生即录制, 否则会漏掉那次上屏 (实测漏掉后 10s 等不到首帧)。
	factory := &perfFactory{recordOnInit: true}
	gfx.SetDefaultFactory(factory)
	t.Cleanup(func() { gfx.SetDefaultFactory(nil) })

	// 冷启动前 GC 一次: 把前面测试/前一次运行的垃圾清掉, 分配数字才干净。
	runtime.GC()
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)

	t0 := time.Now()

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	tEval := time.Now()
	if gfx.ActiveRoot() == nil {
		t.Fatalf("脚本没有建立窗口 (ActiveRoot 为空)")
	}
	fake := factory.surface()
	if fake == nil {
		t.Fatalf("窗口工厂没有被调用 (Surface 为空)")
	}

	// 首帧时间戳: 求值内应已记录 (Mount → redraw → ShowRegions)。
	// 若没有 (未来首帧改成延迟到 Pump), 退回泵循环去等, 口径不变。
	var firstShow time.Time
	var pumpErr error
	if ts := fake.timestamps(); len(ts) > 0 {
		firstShow = ts[0]
	} else {
		start := time.Now()
		pump := func(maxWait time.Duration) bool {
			if !firstShow.IsZero() {
				fake.push(gfx.Event{Kind: gfx.EventClose})
			}
			ok := gfx.Pump(maxWait)
			if firstShow.IsZero() {
				if ts := fake.timestamps(); len(ts) > 0 {
					firstShow = ts[0]
					fake.push(gfx.Event{Kind: gfx.EventClose})
				} else if time.Since(start) > 10*time.Second {
					pumpErr = fmt.Errorf("10s 内没有等到首次上屏")
					fake.push(gfx.Event{Kind: gfx.EventClose})
				}
			}
			return ok
		}
		if err := v.RunTimersWithPump(pump); err != nil && pumpErr == nil {
			pumpErr = err
		}
	}
	// 收尾: 注入关闭事件并泵空, 让窗口正常关闭 (不给下一次运行留活动窗口)。
	fake.push(gfx.Event{Kind: gfx.EventClose})
	for i := 0; i < 16 && gfx.WindowCount() > 0; i++ {
		if !gfx.Pump(0) {
			break
		}
	}
	if pumpErr != nil {
		t.Fatalf("冷启动泵循环失败: %v", pumpErr)
	}
	if firstShow.IsZero() {
		t.Fatalf("没有采集到首次上屏时间戳")
	}
	runtime.ReadMemStats(&memAfter)

	return coldRun{
		EvalMs:          round3(float64(tEval.Sub(t0)) / float64(time.Millisecond)),
		FirstFrameMs:    round3(float64(firstShow.Sub(t0)) / float64(time.Millisecond)),
		PostFrameEvalMs: round3(float64(tEval.Sub(firstShow)) / float64(time.Millisecond)),
		TotalAllocMB:    round3(float64(memAfter.TotalAlloc-memBefore.TotalAlloc) / 1e6),
	}
}

// TestColdStartToFirstFrame 跑 N 次冷启动取中位数, 落盘一份 JSON。
// N 默认 7 (PERF_COLDSTART_REPS 可覆盖): 单次冷启动 <100ms, 7 次中位数
// 在无独占机器上已足够稳, 且总耗时可控 (<10s)。
func TestColdStartToFirstFrame(t *testing.T) {
	reps := 7
	if v := os.Getenv("PERF_COLDSTART_REPS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			reps = n
		}
	}

	// 预热: 跑一次不计入 (页缓存 / 字体栈 / 模块注册表的首次构建在预热里发生)。
	runColdStart(t)

	runs := make([]coldRun, 0, reps)
	for i := 0; i < reps; i++ {
		r := runColdStart(t)
		t.Logf("[第 %d/%d 次] eval=%.2fms 首帧=%.2fms 首帧后余量=%.2fms 分配=%.1fMB",
			i+1, reps, r.EvalMs, r.FirstFrameMs, r.PostFrameEvalMs, r.TotalAllocMB)
		runs = append(runs, r)
	}

	stats := coldstartStats{
		EvalMs:          round3(medianFloat(collectCold(runs, func(r coldRun) float64 { return r.EvalMs }))),
		FirstFrameMs:    round3(medianFloat(collectCold(runs, func(r coldRun) float64 { return r.FirstFrameMs }))),
		PostFrameEvalMs: round3(medianFloat(collectCold(runs, func(r coldRun) float64 { return r.PostFrameEvalMs }))),
		TotalAllocMB:    round3(medianFloat(collectCold(runs, func(r coldRun) float64 { return r.TotalAllocMB }))),
	}
	t.Logf("冷启动到首帧 (中位, n=%d): eval=%.2fms 首帧=%.2fms 首帧后求值余量=%.2fms 分配=%.1fMB",
		reps, stats.EvalMs, stats.FirstFrameMs, stats.PostFrameEvalMs, stats.TotalAllocMB)

	rep := coldstartReport{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Benchmark:   "perf/ColdStartToFirstFrame",
		Command:     "go test ./tools/perf/ -run TestColdStartToFirstFrame -v",
		Runs:        reps,
		Platform:    platformInfo(),
		Scenario: map[string]any{
			"script":      "testdata/perf/first_frame.js",
			"window":      "320x200",
			"surface":     "perfSurface (无窗口替身, 无合成器干扰)",
			"gogc":        gogcPercent(),
			"runtime_tag": "gox 本仓库构建 (版本号见 cmd/gox/main.go 的 version 常量; 跨版本对比时以 gox version 输出为准)",
		},
		Phases: stats,
		All:    runs,
		MetricDefs: map[string]string{
			"phases_ms.first_frame_ms":     "主口径: EvalVM 起点前的 t0 → 首次 Surface.ShowRegions (上屏)。含解释器初始化+脚本求值(含模块装载)+窗口挂载+首次 Layout/Draw/上屏。首帧发生在 render() 求值内 (Mount 末尾同步 redraw), 因此 first_frame_ms ≤ eval_ms。不含进程 exec 与 Go runtime 启动 (那是进程级口径, scripts/coldstart.sh)",
			"phases_ms.eval_ms":            "t0 → vm.EvalVM 返回: 解释器初始化 + 脚本解析/编译/求值 (含 ES 模块 gx/gfx、gx/dev 等装载) + 窗口创建挂载 + 首帧",
			"phases_ms.post_frame_eval_ms": "首次上屏 → EvalVM 返回 (首帧后求值里的剩余工作, 应接近 0; 明显大于 0 说明首帧路径变了)",
			"phases_ms.total_alloc_mb":     "冷启动期间 (t0→首次上屏) 的累计分配 (TotalAlloc 差分, 中位)",
			"runs":                         "重复次数; 各分段取跨运行中位数 (预热 1 次不计入)",
		},
		Note: "进程内口径, 不含进程 exec/动态链接/Go runtime 启动; 真窗口进程级数字见 scripts/coldstart.sh 的 coldstart-process-*.json",
	}

	dir := filepath.Join("..", "..", "bench-results")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("创建 bench-results: %v", err)
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("coldstart-%s.json", stamp))
	buf, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		t.Fatalf("序列化报告: %v", err)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatalf("写 %s: %v", path, err)
	}
	t.Logf("结果已存档: %s", path)
}

func collectCold(runs []coldRun, f func(coldRun) float64) []float64 {
	out := make([]float64, 0, len(runs))
	for _, r := range runs {
		out = append(out, f(r))
	}
	return out
}

// firstFrameScript 读 testdata/perf/first_frame.js (与 frame_load 同一占位纪律:
// 文件是真实可读的 JS, check-imports.py 可扫)。
func firstFrameScript(t *testing.T) string {
	t.Helper()
	buf, err := os.ReadFile(filepath.Join("..", "..", "testdata", "perf", "first_frame.js"))
	if err != nil {
		t.Fatalf("读取首帧脚本: %v", err)
	}
	return string(buf)
}
