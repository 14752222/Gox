# Gox 性能基准与 GC 毛刺判定（M9）

本文是 **M9「并发与性能」的可复现基准设施**的说明与数据存档。它只回答两件事、
并且**只用数字说话**：

1. **解释器算力**：gox 相对 goja（同类纯解释器）与 node/V8（JIT）到底差多少？
   → 见 [§2](#2-解释器算力基线) / [§3](#3-算力真相扣掉启动开销后)。
2. **UI 有没有 GC 毛刺**：持续高频更新下帧间隔尾部与 GC 停顿有多大？
   → 见 [§5](#5-ui-帧率--gc-毛刺基准)。

> 本轮**只做基准设施与数据**，不改 `vm/` 与 `gfx/` 的内核。内核优化（inline
> cache、降分配等）由下一轮做；本文给出的差距数字就是下一轮的验收靶子。
>
> **真机**侧指标（屏幕密度、触控与多指手势、帧率、内存 PSS、冷启动）不在本文
> 范围内，见 `docs/mobile-perf-baseline.md`（采集脚本
> `scripts/mobile-device-accept.sh`）。两套数字**不可直接相减**：口径、单位与
> 负载形态都不同，对比方式见那篇 §4。

---

## 0. 一句话结论

| 出口验收 | 判定 | 依据 |
|---|---|---|
| 解释器性能达 goja 同级 | ❌ **未达标** | fib28 纯计算 gox ≈ 311ms vs goja ≈ 130ms → **慢 2.4 倍**（阈值 ±25%） |
| UI 无 GC 毛刺 | ✅ **达标** | 重压 3 次聚合：p95 帧间隔 **5.95ms** ≤ 16.7ms；单次 GC 停顿 max **1.382ms** ≤ 5ms |

---

## 1. 机器、日期与命令

所有数字都在同一台机器、同一天、同一批脚本下测得（脚本零依赖、外部计时）。

| 项 | 值 |
|---|---|
| 机器 | Apple M2，8 核，8 GB，macOS 26.6.2（arm64） |
| Go | go1.26.2 |
| Node | v22.22.2 |
| goja | `v0.0.0-20261001174550-3ccc9c78af18`（`bench/goja` 独立 module） |
| 日期 | 2026-10-02（UTC） |
| 结果文件 | `bench-results/bench-20261002-024818.json`（runs=5）、`bench-results/perf-stress-20261002-025455.json`（重压 3 次聚合）、`bench-results/perf-probe-20261002-025423.json` |

> 说明：`bench-results/bench-20260929-*.json` 是**另一台机器**（Windows / i7）
> 的旧数据，仅作历史趋势参考，不与本文数字直接相减。

---

## 2. 解释器算力基线

**命令**（详见 [§9](#9-复现命令)）：

```bash
export PATH="/Users/apple/.workbuddy/binaries/go/bin:$PATH"
export GOPROXY=https://goproxy.cn,direct
./scripts/bench-all.sh --runs 5
```

三个脚本见 `scripts/bench/`：`startup_idle.js`（引擎启动 + 最小求值）、
`fib28.js`（`fib(28)` 递归 + 10 万次函数调用）、`timers_10k.js`（1 万次
`setTimeout(…,0)` 全部触发）。**每项外部 wall time 跑 5 次取中位数**，
RSS 取进程常驻峰值（GNU `time -v` 或 BSD `time -l`）。

| 栈 | 启动 (ms) | fib28+10万次调用 (ms) | 1万次定时器 (ms) | 空载 RSS (KB) |
|----|-----------|----------------------|------------------|---------------|
| **gox** | 11 | **322** | 124 | 22592 |
| **node (V8)** | 55 | 45 | 152 | 48432 |
| **goja** | 7 | **137** | 121 | 7792 |

口径诚实性：

- `fib28_ms` 是**整脚本 wall time**，含引擎启动 + 解析 + 求值。
- `timers10k_ms` 对外部计时；gox/node 用各自真实定时器子系统，**goja 用一个
  最小事件循环补齐**（goja 内核不提供宿主定时器，见 [§8](#8-goja-对照的口径与局限)），
  因此 goja 的定时器列**不能**当作"goja 运行时调度器性能"，只作参考。
- RSS 是**空载启动后常驻峰值**，不同后端的基线差异大（node/V8 自带一个大堆），
  它衡量的是"空壳内存"，不是算力。

---

## 3. 算力真相：扣掉启动开销后

`fib28` 的 wall time 里混着启动，直接拿原始值比会失真——尤其 node 的启动
（55ms）比它的 `fib28` 总耗时（45ms）还大，说明这一列几乎全是启动噪声。

用 `startup_idle` 的中位数做启动基线相减，再用 node 的 `process.hrtime.bigint()`
量**纯计算段**（`node -e` 内联同一段 fib/loop，排掉进程与文件加载）：

| 栈 | fib28 纯计算估算 | 相对 gox |
|----|------------------|----------|
| gox | 322 − 11 ≈ **311 ms** | 1.00× |
| goja | 137 − 7 ≈ **130 ms** | **0.42×**（goja 比 gox 快 2.4 倍） |
| node/V8 | ≈ **5 ms**（hrtime 内联口径） | **0.016×**（V8 比 gox 快约 62 倍） |

**诚实陈述**：

- gox 是**纯解释器**（零 cgo、直接遍历字节码）。它与 V8 的差距里，绝大部分是
  **"解释器 vs JIT"的固有差距**，不能都算成 gox 的实现缺陷。
- 真正该对标、也真正暴露实现差距的是 **goja（同样纯解释器）**：gox 的 fib28
  比 goja **慢约 2.4 倍**。这是 M9 下一轮 inline cache / 快速路径优化的直接靶子。
- 换个角度：仓库旧数据里的"落后 V8 ~15 倍"是**原始 wall 比值**（332/19）；按
  **纯计算**口径，gox 与 V8 的差距是 **~60 倍**量级。两个数都对，但口径不同，
  引用时必须说清用的是哪个（本文以纯计算口径为准）。
- gox 的**定时器吞吐已与 goja 同级**（124ms vs 121ms，差 2%），甚至快于本机
  node 的 152ms；**启动开销也小**（11ms vs goja 7ms、node 55ms），空载 RSS
  7.8MB（goja）/22.6MB（gox）/48.4MB（node）。**短板集中在纯计算热循环**。

### 3.1 "同级"的量化定义（本文采用）

`fib28` 纯计算满足下式即视为"与 goja 同级"：

```
gox_fib28_compute ≤ goja_fib28_compute × 1.25     // ±25% 容差
```

- 达标：≤ ×1.25；优良：≤ ×1.10；未达标：> ×1.25。
- 用纯计算而非原始 wall，是因为两者启动都在 10ms 量级、对 ~300ms 的测量影响 <4%，
  而原始值在 node 那一列会被启动噪声主导。
- 当前：`311 / 130 ≈ 2.39× > 1.25` → **未达标**。下一轮需把纯计算速度提升约
  **2.4 倍**（或更多）才能落闸。

---

## 4. 现有性能机制盘点

这些是**已经存在**的机制，本文只盘点、不改。它们是"为什么 gox 在 UI 侧不掉链子"
的答案，也标出了 M9 剩余工作（算力）的位置。

### 4.1 `gx/dev` 帧埋点（只数帧，不测耗时）

- 实现：`gfx/dev.go` 的 `devFrameTick(full bool)`，在 `redraw` 真正上屏的三个出口
  计数（`gfx/render.go`：整帧路径、>85% 面积退化、局部脏区路径）。
- 暴露：`gx/dev` 模块 `devSnapshot().frame` → `{ count, full, partial, fullRatio }`
  （`gfx/dev.go`；字段形状被 `TestDevSnapshotShape` 锁死）。
- **刻意的边界**：它**只做单调计数，不测 layout/draw 耗时**（源码注释原文：
  "耗时滑动平均等有真实需求再加"）。也就是说，仓库里**此前没有任何**帧间隔分布
  或 GC 停顿指标——这正是本次新增 `tools/perf/` 要补的那块。

### 4.2 image / glyph LRU 缓存

- **image LRU**（`gfx/image.go`）：`path → 解码后 *image.RGBA`，按**张数**上限
  `imageCacheCap = 16` 淘汰，统计 `{size, cap, hits, misses, evicts}`，经
  `devSnapshot().imageCache` 暴露。按张数而非字节数是有意为之（v1 不做按字节淘汰）。
- **glyph LRU**（`gfx/font.go`）：`(size, rune) → 字形掩码`，容量 `1024`，
  单线程 GUI 访问（锁仅防御），经 `devSnapshot().glyphCache` 暴露。
- 意义：把"重复解码图片""重复栅格化字形"这两个每帧最容易爆炸的分配源挡在渲染
  热路径之外——这一点在 [§5](#5-ui-帧率--gc-毛刺基准) 的分配速率里能看到效果。

### 4.3 `<scroll vlist>` 虚拟化

- 实现：`gfx/vlist.go`。只物化**视口 + 上下 buffer（缺省 2 行）**，上下用撑高
  垫片补出内容总高，滚动条/行程与全量渲染逐像素一致；容器已知但视口高未定时
  最多物化 `vlistPendingRows = 64` 行。
- 实测（`bench-results/vlist-20261001.json`，Windows / i7 机器）：
  | 行数 | 首帧 全量 | 首帧 vlist | 滚动帧 全量 | 滚动帧 vlist |
  |---|---|---|---|---|
  | 10 万 | 2187 ms | **89 ms** | 998.8 ms | **0.115 ms** |
  - 首帧与行数脱钩（vlist 恒 64 次渲染调用），滚动帧与行数完全脱钩。
- 判定纪律（原文结论）：**必须数"渲染调用次数"，不能数树上剩多少节点**——
  否则"先全建再销毁"的假虚拟化会骗过断言。

> 这是 GUI 渲染路径的成果，**不是解释器算力**。不要拿它去回答"fib28 慢"。

---

## 5. UI 帧率 / GC 毛刺基准

**位置**：`tools/perf/frame_gc_test.go`（包 `perf`）+ 压测脚本 `testdata/perf/frame_load.js`。

**跑法**：

```bash
go test ./tools/perf/            # 轻量探针 + 重压（默认）
go test ./tools/perf/ -short     # 只跑轻量探针（跳过重压）
```

### 5.1 测什么、为什么这么测

- **真实渲染**：脚本走真实 JS 建树（`gox` 模块 `h/render/createSignal`），
  每个 Go 侧"帧"调用一次 `__tick()`（更新若干行的独立 signal → 响应式 effect
  标脏），随后 `gfx.Pump` 走真实 `Layout` + 脏区 `Draw` + 上屏。**不建平台窗口**
  （用一个无窗口 `gfx.Surface` 替身），与 `gfx/vlist_bench_test.go` 的既有口径一致。
- **帧间隔**：相邻两次 `Surface.ShowRegions`（上屏）的时间差。它包含
  JS 更新 + effect + Layout + Draw + 任何 GC 停顿，是"用户眼里的掉帧"的直接代理量。
  用上屏时刻而非循环迭代时刻，是为了把"循环空转"排除在外。
- **GC 停顿**：`runtime/debug.ReadGCStats`，取**测量窗口内新增**的逐次 STW 停顿
  （`after.Pause` 的前 `NumGC` 差项）。只看分布尾部，不看平均值——毛刺只出现在尾部。
- **分配速率**：独立 goroutine 每 2ms 采一次 `TotalAlloc`/`HeapAlloc`，避免把
  `ReadMemStats` 的开销插进渲染循环。峰值 = 任意 1s 滑动窗口内 `TotalAlloc` 差分
  的最大速率。

> **退化说明**：gfx 包内测试用的 `fakeSurface` 是 `package gfx` 的**内部类型**，
> 外部包拿不到，因此 `perf` 自己实现了等价的 `perfSurface`（非阻塞 `WaitEvents`
> + 记录上屏时间戳）。这不影响测量对象（仍是真实 Layout/Draw 路径）。

> **重复运行与聚合口径**：重压默认重复 **3 次**（`PERF_STRESS_REPS` 可覆盖），
> 各分位取跨运行的**中位数**、`max`/GC 停顿 max/堆峰值取**最坏值**、GC 次数取**和**。
> 单次运行的尾部估计在无独占的桌面机上不稳，聚合后才可复现。

### 5.2 实测数据（Apple M2，2026-10-02，GOGC=100）

**轻量探针**（300 行、每帧更新 30 行、600 测量帧，单次，`perf-probe-20261002-025423.json`）：

| 指标 | 值 |
|---|---|
| 帧间隔 p50 / p95 / p99 | 0.77 / 1.67 / **1.71** ms |
| 帧间隔 p99.9 / max | 1.84 / 1.98 ms |
| GC 次数 / 停顿 max | 1 / **0.046** ms |
| 分配 平均/峰值 / 堆峰值 | 90.3 / 82.7 MB/s / 53.2 MB |

**重压**（800 行、每帧更新 200 行、2400 测量帧 × 3 次聚合，`perf-stress-20261002-025455.json`）：

| 指标 | 值 |
|---|---|
| 帧间隔 p50 | 3.35 ms |
| 帧间隔 **p95**（判定用） | **5.95** ms |
| 帧间隔 p99 | 9.68 ms |
| 帧间隔 p99.9 / max | 114.36 / 194.59 ms（参考） |
| 超 16.7ms 帧 | **6 / 2399 = 0.30%** |
| GC 次数（3 次合计） | 61 |
| GC 停顿 p50 / p95 / p99 / max | 0.100 / 0.329 / 0.628 / **1.382** ms |
| GC 停顿总计（3 次合计） | 10.71 ms（摊到 7200 帧 ≈ 1.5µs/帧） |
| 分配 平均 / 峰值 / 堆峰值 | 86.6 / 102.4 MB/s / 98.4 MB |

3 次单跑的离散度（说明为什么判定不能只看 p99/max）：

| 单跑 | p95 | p99 | max | GC 停顿 max |
|---|---|---|---|---|
| #1 | 7.40 | 12.42 | 194.59 | 1.382 ms |
| #2 | 4.39 | 6.74 | 132.53 | 0.316 ms |
| #3 | 5.95 | 9.68 | 120.72 | 0.664 ms |

可见 **GC 停顿始终亚毫秒~1.4ms**，而帧间隔的 p99/max 跨运行摆动很大（p99 6.7~12.4ms、
max 120~195ms；同日更早的运行里甚至出现 p99 36.8ms、max 366.8ms）。差异来自 OS 调度，
不是 GC。

### 5.3 "UI 无 GC 毛刺"的判定阈值

```
判定为「无 GC 毛刺」当且仅当:
  ① p95 帧间隔 ≤ 16.7 ms  (95% 的帧落在 60fps 单帧预算内)
  ② 单次 GC 停顿 max ≤ 5 ms (软件渲染 + 交互的可感知阈值)
```

- **为什么 16.7ms**：一帧 16.7ms = 60fps。用它作单帧预算；只要绝大多数帧不越界，
  用户就感知不到周期性掉帧。
- **为什么用 p95 而不是 p99/max**：无独占的桌面机上，p99/max 由 OS 调度抖动主导
  （同负载 3 次实测 p99 在 7~37ms、max 在 109~367ms 间摆动），拿它判会把"进程被别的
  进程抢了 CPU"误判成"GC 毛刺"。p95 跨运行稳定（实测 4.4~7.4ms），既覆盖了 95% 的帧，
  又不被尾部噪声绑架。
- **为什么 5ms**：交互/动画里 5ms 级停顿通常不可感知，10ms+ 才会在快速滚动时"顿一下"。
  本机重压实测 GC 停顿 max 仅 1.382ms，离阈值有 ~3.6 倍余量。
- p99 / p99.9 / max / 超预算帧占比仍**完整报告**，供人工判断；若某天真出现 GC 导致的
  长帧，它必然同时体现在"GC 停顿 max"上（因为长帧的 GC 成分直接可测）。

**当前判定**：① 5.95 ≤ 16.7 ✅；② 1.382 ≤ 5 ✅ → **无 GC 毛刺（达标）**。

> 硬闸门 vs 软判定：`perf` 测试只对"数量级劣化"硬失败（p99 > 200ms、
> GC 停顿 max > 100ms、重绘帧数异常），正常抖动不会让 CI 误报；上面的 16.7/5ms
> 阈值作为 `verdict` 写进 JSON，供验收引用。

---

## 6. GC 策略现状与 v1 决策

**现状**：gox 的 JS 对象全部是**普通 Go 对象**（`object.Value` / `*object.Object`
/ `*object.Array` …），由 Go 运行时分配与回收。**没有自研 GC**。

**v1 决策：依赖 Go GC，本轮不引入自研增量 GC。** 理由：

1. **零分配器控制权**：要自研增量 GC，前提是接管对象分配（自建 arena/空闲表）。
   但 `vm/`、`object/`、`stdlib/`、`gfx/` 全线用的是普通 Go `new/make`，接管等于
   重写分配器与所有对象生命周期，改动面与回归风险都极大。
2. **没有对象图所有权**：JS 对象与宿主 Go 对象（回调桥、闭包、signal effect）互相
   引用，精确根集无法界定；增量/分代 GC 需要 write barrier 与精确根，而 Go 运行时
   不对外暴露这些能力。
3. **收益/风险比不划算**：实测 UI 重压下 GC 停顿 max 仅 **1.382ms**（3 次聚合最坏值）、
   总计 10.71ms/7200 帧，离 5ms 可感知阈值有 ~3.6 倍余量、离"卡顿级"（>16.7ms）更是
   一个数量级——自研 GC 的收益近乎为零，却会引入 STW 正确性、并发正确性风险。
   自研 GC 与"单二进制 + 零 cgo"**并不冲突**（纯 Go 可写），但**不值**。

**后续可做的降分配与调优方向**（下一轮，按性价比排序）：

- **对象池**：热路径（fib 调用帧、数组/字符串临时对象、effect 闭包）复用，直接压低
  每秒分配字节数（当前 100 MB/s 量级）。
- **逃逸分析**：`go build -gcflags=-m` 找热路径上"本该在栈上却逃逸到堆"的分配，
  逐个消除。
- **旋钮调优**：`GOGC` / `debug.SetGCPercent` 权衡吞吐与堆占用；必要时用
  `debug.SetMemoryLimit`（Go 1.19+）给内存设上限，避免长时间运行后堆无限增长。
- 这些都**不是**"自研 GC"，而是把 Go GC 喂得更省。

---

## 7. Worker / SharedArrayBuffer 口径

`docs/v1-roadmap.md` §七「明确"不做"」已拍板：

> **BigInt / SharedArrayBuffer / 真 `String.normalize` 刻意不实现**
> （`undecided-and-unimplemented.md` §三）。

因此：

- **SharedArrayBuffer 不做**——本轮不实现、后续也不实现。M9 任务书名里的
  "Worker + SharedArrayBuffer"里，**SAB 这半项按拍板排除**，实现 Worker 时也**不**
  以共享内存为通信基础。
- **Worker 若做，用消息传递**（结构化克隆式的消息投递，postMessage/onmessage 语义），
  不做共享内存。Worker 本体不在本轮范围（下一轮）。

---

## 8. goja 对照的口径与局限

- 位置：`bench/goja/`，**独立 Go module**（`goxbench/goja`），使主模块依赖面完全
  不受 goja 依赖（regexp2/sourcemap/pprof/x/text）污染。主模块 `go build ./...`
  不会进入嵌套 module。
- `bench/goja/main.go`：读 `scripts/bench/*.js` 同一批脚本，在 goja 里执行；
  **计时一律交给外层 harness**（与 gox/node 同源），runner 自身不打印耗时。
- 局限（如实写明）：
  1. goja 内核**不提供宿主定时器**，`timers_10k.js` 需要的 `setTimeout` 由一个
     **最小事件循环**（`container/heap` 按到期时间派发）补齐。因此 goja 的
     `timers10k_ms` **不是** goja 运行时调度器的性能，只作参考；gox/node 的该列
     才是各自真实定时器子系统的数字。
  2. goja 是纯解释器、无 JIT；这也正是把它作为 gox 同类基线的理由。
- 依赖拉取：从 `goproxy.cn` 成功拉取 `github.com/dop251/goja@v0.0.0-20261001174550-3ccc9c78af18`。
  离线时 `bench-compare.sh` 会**自动跳过 goja 栈**，gox/node 部分照常可跑。

---

## 9. 复现命令

```bash
# 0) 环境（本机 Go 不在默认 PATH；goproxy 走国内镜像）
export PATH="/Users/apple/.workbuddy/binaries/go/bin:$PATH"
export GOPROXY=https://goproxy.cn,direct

# 1) 一条命令跑完对比表 + 帧率/GC 基准（runs=5 取中位数）
./scripts/bench-all.sh --runs 5

# 2) 只跑 gox/node/goja 解释器对比（离线可跑 gox/node；goja 需已下载依赖）
./scripts/bench-compare.sh --runs 5

# 2a) 首次/联网时准备 goja 依赖（之后可离线）
cd bench/goja && go mod download && cd ../..

# 3) 只跑 UI 帧率 / GC 毛刺基准
go test ./tools/perf/ -v                 # 探针 + 重压
go test ./tools/perf/ -short             # 只跑轻量探针

# 4) 单独看某档数据
go test ./tools/perf/ -run TestUIFrameGCStress -v

# 5) 结果落盘位置
ls -l bench-results/               # bench-*.json (算力) / perf-*.json (帧率+GC)
```

产物字段口径：算力 JSON 见 `bench-results/bench-*.json` 的 `method`/`host` 字段；
帧率/GC JSON 每个都带 `metric_defs`，逐字段说明测量口径。

---

## 10. 新增/涉及文件

| 路径 | 作用 |
|---|---|
| `bench/goja/go.mod`、`go.sum`、`main.go` | goja 纯解释器对照 runner（独立 module） |
| `tools/perf/doc.go` | `perf` 包的测量口径与阈值说明 |
| `tools/perf/frame_gc_test.go` | 帧率 / GC 毛刺基准（`TestUIFrameGCProbe` / `TestUIFrameGCStress`） |
| `testdata/perf/frame_load.js` | 高频更新长列表压测脚本（`__ROWS__`/`__UPDATES__` 占位可调） |
| `scripts/bench-compare.sh` | 新增 `goja` 栈、跨平台 RSS、预热、机器信息入 JSON |
| `scripts/bench-all.sh` | 一条命令跑完全部基准 |
| `bench-results/bench-20261002-024818.json` | 算力基线（gox/node/goja，runs=5） |
| `bench-results/perf-probe-20261002-025423.json` | 轻量帧率/GC 探针结果 |
| `bench-results/perf-stress-20261002-025455.json` | 重压帧率/GC 结果（3 次聚合） |
| `cmd/gox/pprof.go` | `GOX_PPROF` / `GOX_MEMPROFILE` 剖面入口（§11） |
| `tools/memdrift/` | 长时 RSS 漂移闸门 `TestRSSDrift`（§11） |
| `testdata/memdrift/churn.js` | 漂移负载：窗口挂到底 + 整批组件换挂 |
| `scripts/rss-curve.sh` | 增加 `--max-rss` / `--max-slope` 闸门与机器可读摘要 |
| `bench-results/memdrift-20261028-012358.json` | 漂移基线（7500 轮） |
| `docs/performance.md` | 本文 |

---

## 11. 内存剖面与 RSS 闸门（T3-3）

§5 解决的是「帧有没有卡」，本节解决「挂久了内存会不会爬」。Gox 的渲染层是
无 GC 设计（帧缓冲 / 对象池手工复用），泄漏不表现为崩溃，只表现为 RSS 单调
上行——没有剖面和闸门，这种问题只能等用户投诉。

### 11.1 怎么开 pprof（常驻进程）

入口在 `cmd/gox/pprof.go`，由 `main()` 里的 `defer initPprof()()` 安装。
**默认关闭**：没设环境变量时只有两次 `Getenv`，不起 goroutine、不监听、不建
文件。两个变量各管一段：

```bash
# 1) 活着的进程：起 net/http/pprof（推荐只绑回环地址）
GOX_PPROF=127.0.0.1:6060 ./gox apps/notes/src/main.js
#    分配剖面（按分配字节数，找"谁在造垃圾"）
curl -s -o heap.pb.gz 'http://127.0.0.1:6060/debug/pprof/heap?alloc_space=1'
#    活对象（按当前占用，找"谁没释放"）
curl -s -o inuse.pb.gz 'http://127.0.0.1:6060/debug/pprof/heap'
#    火焰图 / 排行
go tool pprof -top -alloc_space ./gox heap.pb.gz
go tool pprof -top -inuse_space ./gox inuse.pb.gz

# 2) 抓退出那一刻：正常返回 或 SIGINT/SIGTERM 时 dump 一份堆剖面
GOX_MEMPROFILE=/tmp/heap.pb.gz ./gox apps/notes/src/main.js
#    Ctrl-C 后进程以 130 退出，文件已写好；-alloc_space 看分配，默认看 inuse
go tool pprof -alloc_space ./gox /tmp/heap.pb.gz
```

- 为什么用环境变量而不是 flag：`gox <入口.js>` 后面所有非 `--allow-*` 参数都
  会被当成脚本路径（`main.go` 的分发规则），加全局 flag 会污染脚本参数解析；
  而 `gox dev` 的热重启子进程只继承环境变量（`GOX_DEV` 同款通道）。
- 为什么 dump 要接管信号：仓库没有退出钩子，`os.Exit`（脚本报错 /
  `process.exit`）会跳过 `defer`；信号处理器覆盖了「人手动结束常驻进程」这条
  最常见的路径。
- 非回环地址会在 stderr 打一行警告：堆内容可能含业务数据，别随手绑 `0.0.0.0`。

### 11.2 怎么跑 RSS 闸门

在既有曲线采集上扩展，不另起一套：`tools/rssmon` 仍按进程树采 VmRSS 落 CSV，
`scripts/rss-curve.sh` 读完 CSV 再判定。

```bash
# 只采曲线（nightly 用这条，永远绿）
DURATION=120 INTERVAL=2 bash scripts/rss-curve.sh

# 带闸门（任一越界退出码 1；参数/环境错误退出码 2）
bash scripts/rss-curve.sh --max-rss 196608 --max-slope 512
# 等价的环境变量写法（CI 里更顺手）
RSS_MAX_KB=196608 RSS_MAX_SLOPE_KB_PER_MIN=512 bash scripts/rss-curve.sh
```

判定口径：

- **峰值**取全段 `max(rss_kb)`，包含 `gox dev` 的 watcher 父进程与子进程合计。
- **斜率**只回归**稳态段**（默认跳过前 30% 的样本）：启动期是一次性线性爬升
  （加载 JS、建窗口、解码图标/字体），算进去会得到一条假斜率。样本 < 10 个时
  不跳过（点太少，跳过更不稳）。
- 机器可读摘要：一组 `RSS_*` 键值行 + 一行 `RSS_SUMMARY {…}` JSON，例如：

```
RSS_PEAK_KB=141284
RSS_SLOPE_KB_PER_MIN=0.0
RSS_VERDICT=pass
RSS_SUMMARY {"csv":"bench-results/rss-notes-20261028T011459Z.csv","samples":60,
  "first_kb":4692,"last_kb":141284,"peak_kb":141284,"slope_kb_per_min":0.0,
  "max_rss_kb":196608,"max_slope_kb_per_min":512,"rss_ok":true,"slope_ok":true,"pass":true}
```

> 阈值为空 = 该侧不判（`null` 写进 JSON，不用 0 冒充"设过 0"）。
> 为什么闸门不默认打开：绝对 RSS 随机型/镜像/字体栈漂移，nightly 硬判会制造
> 噪声性红（同 `rss-curve.yml` 头部的理由）。

### 11.3 长时漂移测试

`tools/memdrift`（Linux；`-short` 跳过）：

```bash
go test ./tools/memdrift/ -v                       # 默认 7500 轮，约 2 分钟
MEMDRIFT_ITERS=500 go test ./tools/memdrift/ -v    # 缩短自查
go test ./tools/memdrift/ -short                   # 跳过
```

一轮 = 三件事，缺一件就漏掉一个泄漏面：

1. **求值**：新建一个 VM 执行一段小脚本（解析 / 编译 / 全局对象 / 200 个对象）；
2. **组件挂载**：`__churn()` 整批换掉列表数据，key 全变 → 旧行卸载、新行挂载；
3. **渲染**：`__tick()` 改行内 signal → 真实 Layout + 脏区 Draw + 上屏，每轮 6 帧。

判定前的纪律：

- 每 K 轮先 `runtime.GC()` 再采样。泄漏的定义是"GC 之后还活着的对象"，
  不 GC 只能测到"这一轮分配了多少"（那由 TotalAlloc 单独报）。
- 前 30% 只预热。字体/字形缓存、对象池、Go 堆首次增长都在这里一次性发生。
- 同时看两个信号：`HeapAlloc`（Go 堆活对象）与 `VmRSS`（进程实际占用）。
  后者能抓住"堆没涨但内存没还给 OS"——无 GC 设计最容易出这种。

**本次实测**（`bench-results/memdrift-20261028-012358.json`，本机 Linux，
640×480，200 行换批 + 6 帧/轮）：

| 指标 | 值 |
|---|---|
| 轮次 / 耗时 | 7500 轮 / 113.5s（15.0ms/轮，共 45000 帧） |
| 分配速率 | 261.5 MB/s |
| 稳态 HeapAlloc | 25144 → 25147 kB（**Δ +3 kB**，+2.0 kB/min；同日另一轮 +9 kB） |
| 稳态 VmRSS | 78016 → 78336 kB（**Δ +320 kB**，+318.3 kB/min；同日另一轮 +1524 kB） |
| 判定 | heap 3 ≤ 2048、rss 320 ≤ 16384 → **pass** |

> RSS 的 ±1 MB 摆动来自 Go 堆向 OS 归还页的节奏（采样序列在 77–80 MB 之间
> 来回），2 分钟窗口内回归出的斜率会随首尾点漂到几百 kB/min；因此闸门用
> **增量**判定，斜率只作报告（与 `--max-slope` 同单位，便于横向对照）。

### 11.4 阈值依据

| 指标 | 实测基线 | 闸门 | 余量 | 为什么这么定 |
|---|---|---|---|---|
| `rss-curve` 峰值 | 141284 kB（138.0 MB） | 196608 kB（192 MB） | ×1.39 | 覆盖字体栈/驱动差异；再高就说明常驻足迹确实涨了 |
| `rss-curve` 稳态斜率 | 0.0 kB/min | 512 kB/min | — | 8 小时挂机累计 ≤ 240 MB；能拦住"每帧漏一点"，又不被单次抖动触发 |
| 漂移 HeapAlloc | Δ +3~9 kB | Δ 2048 kB | ≥×227 | GC 后活对象很稳，卡紧才抓得住缓慢泄漏 |
| 漂移 VmRSS | Δ +320~1524 kB | Δ 16384 kB | ≥×10.7 | 留给堆高水位与归还节奏的摆动（跨机差异大） |

阈值 = **本次实测基线 + 余量**，不是拍脑袋的整数。机器/负载变了就重测重定，
不要直接把这两组数字抄到别的机器上。

### 11.5 已知盲区

1. **只覆盖 Go 堆**：`pprof` 看不到 gfx 手工池（帧缓冲、字形掩码）之外的
   cgo / GPU 显存；RSS 曲线能看到总量，但定位不到那里。
2. **`pprof` 抓不到硬退出**：`process.exit()`（内部是 `os.Exit`）与 `SIGKILL`
   不会触发 dump；只有正常返回、`SIGINT`、`SIGTERM` 会。需要必抓时用
   `GOX_PPROF` 活着抓。
3. **堆剖面是采样的**：默认每 512 KB 采一次，单个小对象泄漏可能一次 dump
   看不出来；要抓小泄漏就多跑一会儿、比较两次 dump 的差值。
4. **RSS 口径是 Linux `/proc`**：`rssmon` 与漂移测试都依赖它；macOS/Windows
   只有 §2 的外部 `time` 口径。`rssmon` 采的是**进程树**，`gox dev` 的父进程
   与子进程会合在一起，不能拿来拆"谁占了多少"。
5. **曲线含启动段**：`rss-curve.sh` 的峰值是全段峰值，包含启动期；闸门斜率
   已排除前 30%，但峰值没有——想只看稳态就自己裁 CSV。
6. **窗口反复开关存在已知泄漏（未定位根因，本任务范围外）**：临时探针实测
   （Linux，640×480，同进程内循环"新建 VM → 挂窗 → 关窗"）：
   - 平台关窗（`EventClose`，即用户点关闭 / 泵收到关闭事件）：40 轮后
     `windowHandles` 仍有 40 条（`gfx/render.go` 的 `a.close()` 只调
     `unregisterApp`，摘句柄只在 `Window.Close()` 里做），每轮额外留存一个句柄；
   - 脚本显式 `__root.close()`（句柄表为 0）：最小脚本仍约 **537 kB/轮**，
     200 行树约 **2.1 MB/轮** —— 说明还有一条未定位的保留路径，且与树大小
     相关，不是单纯帧缓冲（1.2 MB）。
   正因如此，`tools/memdrift` **整场只挂一个窗口**，把测量目标锁在"帧循环 +
   组件换挂"的稳态；窗口 churn 的修复需要改 `gfx` 内核，留给后续任务。

---

## 12. 冷启动到首帧（T2-5，2026-10-28）

单文件分发是 Gox 的卖点，"启动快"必须变成**可对外引用的数字**。本节补上
"冷启动到首帧"口径：**用户双击 exe 到看见第一帧像素**。两个口径，**不可混用**：

| 口径 | 位置 | 含进程 exec？ | 含真实窗口？ | 用途 |
|---|---|---|---|---|
| **进程级**（对外主口径） | `scripts/coldstart.sh` → `bench-results/coldstart-process-*.json` | ✅ | ✅（X11 真实上屏） | 宣传 / 发布说明 |
| **进程内**（CI 回归口径） | `tools/perf/coldstart_test.go` → `bench-results/coldstart-*.json` | ❌ | ❌（无窗口替身 Surface） | 每次提交的回归盯梢 |

### 12.1 对外可引用的数字

| 指标 | 值 | 说明 |
|---|---|---|
| **冷启动到首帧（含 GUI）** | **84 ms**（中位，n=15，单次 84–88 ms） | 进程 exec → 真实 X11 窗口首帧上屏 |
| 纯引擎启动（无 GUI） | ~4 ms（中位，n=5） | 进程 exec → 最小脚本求值完成 |
| GUI 首帧差额 | ~80 ms | 初步归因：**字体栈 ~65ms** > 窗口创建/X11 ~14ms > 引擎 ~4ms，见 12.3 |

**测量条件（引用时必须一并给出）**：

| 项 | 值 |
|---|---|
| 机器 | AMD EPYC 9K65（32 核），Linux 6.6 x86_64，123 GB 内存 |
| 显示 | Xvfb 虚拟显示 1024×768×24（软件渲染，无窗口管理器 / 合成器） |
| 版本 | gox 0.15.0（go1.26.2 构建，无 VCS 元数据），`scripts/bench/coldstart_gui.js` |
| 负载 | 最小 GUI 应用：一个 3 行 `column`，320×200 窗口 |
| 方法 | 外部 wall time，预热 2 次后跑 15 次取中位数 |
| 日期 | 2026-10-28（UTC） |
| 结果文件 | `bench-results/coldstart-process-20261028-122204.json`（n=15）、`coldstart-process-20261028-122100.json`（n=7）、`coldstart-20261028-122042.json`（进程内）、`bench-20261028-122136.json`（四栈对照） |

**进程内口径参考值**（同一机器同日，n=7 中位）：首帧 **0.79 ms**、求值全程 0.80 ms、
冷启动期分配 **1.0 MB**。这个数字不含进程 exec / Go runtime 启动 / 真实窗口创建，
只回答"引擎 + 模块装载 + 挂载 + Layout/Draw/上屏"本身多快，供 CI 回归盯梢。

### 12.2 跨运行时对照（JS 引擎侧）

与 node / bun / goja 的**同口径对照**只取引擎侧（`startup_ms`，外部 wall time 量
"进程启动 + 最小脚本求值"，四栈同源脚本）——JS 引擎本身可比：

| 栈 | 引擎启动 (ms) | 空载 RSS (KB) |
|---|---|---|
| **gox** | **3** | 16008 |
| node (V8/JIT) | 15 | 46580 |
| bun (JavaScriptCore/JIT) | 4 | 13624 |
| goja (纯解释器) | 2 | 7412 |

（同一机器同一批脚本，n=7 中位，2026-10-28，`bench-20261028-122136.json`。）

**诚实陈述**：

- gox 的引擎启动与 bun / goja **同级**（个位数 ms），明显快于 node —— 这一层
  **可以对外讲**："单文件分发 + 3ms 引擎启动"。
- **GUI 部分只对内不对外**：gox 的 84ms 首帧没有同口径的外部对照（electron /
  tauri 空壳需要各自的 GUI 环境与显示栈，本次未测，见 `bench-compare.sh` 的
  跳过纪律）。对外引用 84ms 时只说 gox 自己的条件，**不要**拿它和 electron 的
  启动数字直接相减 —— 显示栈不同，口径不可比。
- `fib28` / 定时器等算力列不在此引用（那是 §2/§3 的地盘）；只引用启动列时
  也要注意 gox 是纯解释器，不要与 JIT 栈的算力数字混为一谈。

### 12.3 口径细节与已知边界

1. **首帧信号是"像素真的出了"**：JS 侧用 1ms `setInterval` 轮询 `gx/dev` 的
   `devSnapshot().frame.count`（它只在 `redraw` 真正上屏的出口计数，见 §4.1），
   首帧后 ≤1ms 内被发现，打印标记后关窗退出 —— 全程含 **≤2ms** 探测/退出开销，
   已含在 84ms 里。**不用 `requestAnimationFrame`**：内核里它只是 16ms 的
   setTimeout（`gfx/render.go` 的 `jsRAF`），与真实上屏无关。
2. **首帧发生在 `render()` 求值内**：`gfx.Mount` 末尾同步调 `a.redraw()`
   （gfx/render.go "首帧"注释），所以进程内口径里 `first_frame_ms ≤ eval_ms`
   （实测 0.79 vs 0.80ms）。冷启动的主成本在求值前（引擎初始化 + 模块装载）。
3. **~80ms 的 GUI 差额：初步归因指向字体栈（2026-10-28 同机三档探针，n=7 中位）**：

   | 负载 | 首帧 (中位) | 增量 |
   |---|---|---|
   | 纯引擎（无窗口，`startup_idle.js`） | 4 ms | — |
   | 窗口 + 色块，**无文字**（`/tmp` 探针，未入库） | 18 ms | 窗口创建 + X11 往返 + 布局/绘制 ≈ **14 ms** |
   | 窗口 + 3 行 `text`（`coldstart_gui.js`） | 83 ms | **字体栈（查找/装载/栅格化）≈ 65 ms** |

   即首帧的最大成本是**画第一段文字**，不是窗口也不是引擎。这是后续启动优化
   的第一个靶子（候选方向：字体查找结果缓存、按需装载、栅格化延迟）。
   注意这是黑盒差分归因（没有 pprof 逐函数证据），精确归因待做；探针脚本
   未入库，复现时按 §12.1 的两个入库脚本自行二分即可。
4. **Xvfb ≠ 真桌面**：无 WM/合成器的软渲染环境数字**稳定可复现**（这是选它的
   理由），但真桌面/移动端真机的首帧口径不同（`docs/mobile-perf-baseline.md`），
   两套数字不可直接相减。换机器/显示栈须重测重写条件。
5. **进程内口径用无窗口替身 Surface**：与 §5 帧率基准同源（`perfSurface`），
   CI 无显示环境可跑；它测不到真实窗口创建的成本，所以不能替代进程级口径。

### 12.4 复现命令

```bash
# 1) 进程级冷启动到首帧（对外口径；无 DISPLAY 时自动走 Xvfb，两者皆无则跳过不失败）
./scripts/coldstart.sh --runs 15

# 2) 进程内口径（CI 回归；无显示环境可跑）
go test ./tools/perf/ -run TestColdStartToFirstFrame -v
PERF_COLDSTART_REPS=15 go test ./tools/perf/ -run ColdStart -v   # 加密重复

# 3) 引擎侧四栈对照（本节表格的来源）
./scripts/bench-compare.sh --runs 7
```

本节新增文件：

| 路径 | 作用 |
|---|---|
| `scripts/coldstart.sh` | 进程级 harness：构建 → 预热 → 外部计时 × N → 中位数 + JSON 落盘 |
| `scripts/bench/coldstart_gui.js` | 最小 GUI 首帧探测脚本（1ms 轮询 `devSnapshot().frame.count`） |
| `tools/perf/coldstart_test.go` | 进程内冷启动基准（`TestColdStartToFirstFrame`） |
| `testdata/perf/first_frame.js` | 进程内口径的最小首帧脚本 |
| `tools/perf/frame_gc_test.go` | `perfFactory` 增加 `recordOnInit`（首帧发生在求值内，Surface 须出生即录制） |
7. **漂移测试的负载是压缩的**：2 分钟 7500 轮不等于"挂一夜"，它放大的是
   每轮分配/回收次数，抓不住"只在特定交互路径上触发"的泄漏。
