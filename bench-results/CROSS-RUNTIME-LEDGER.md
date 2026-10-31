# 跨运行时差距台账（gox / node / bun / goja）— 首次跑通（T2-2）

> 数据文件：`bench-results/bench-20261028-121344.json` 与 `bench-results/bench-20261028-121423.json`（两次独立运行，各 runs=5 取中位数，用于验证可复现性）
> 结论同时在 `docs/performance.md` §0 / §3.2 引用，本文是台账本体。**复验记录见文末 §7（2026-10-31，T2-4 优化后）——引用 gox 侧倍数时以 §7 为准。**

## 1. 口径说明（引用本文数字前必读）

- **gox 是纯解释器**（零 cgo、直接遍历字节码）。它与 node(V8/JIT)、bun(JavaScriptCore/JIT) 的差距里，绝大部分是 **"解释器 vs JIT" 的固有差距**，不能都算成 gox 的实现缺陷，也不能拿 V8 的绝对值当优化目标。
- **goja 同为纯解释器，才是 gox 的同类基线**。gox 与 goja 的差距才是实现层面的可优化空间（inline cache / 快速路径等，见 T2-4）。
- 四项口径完全同源：同一组 `scripts/bench/*.js`，**外部 wall time 计时**（脚本本体零依赖），每项跑 5 次取中位数。
  - `startup_ms`：引擎启动 + 最小脚本求值（`startup_idle.js`）
  - `fib28_ms`：fib(28) 递归 + 10 万次调用（`fib28.js`），整脚本 wall time，含启动
  - `timers10k_ms`：1 万次 `setTimeout(0)` 全部触发（`timers_10k.js`）；goja 用最小事件循环补齐宿主定时器，该列对 goja 只作参考
  - `rss_kb`：空载常驻内存峰值（GNU time VmHWM），衡量"空壳内存"，不是算力

## 2. 机器与版本（复现所需元数据）

| 项 | 值 |
|---|---|
| 机器 | 云沙箱，AMD EPYC 9754 128-Core（32 vCPU 可见），x86_64 |
| 系统 | Linux 6.6.117，glibc 2.39 |
| gox | master @ `74c8320`（2026-10-28，`go build ./cmd/gox`），Go 1.26.2 |
| node | v22.13.1（V8，JIT） |
| bun | 1.4.4（JavaScriptCore，JIT） |
| goja | `bench/goja` 独立 module（纯解释器） |
| 日期 | 2026-10-28 12:13 / 12:14 UTC |

## 3. 四项对比表（两次运行，单位 ms / KB）

| 栈 | 启动 (ms) | fib28+10万次调用 (ms) | 1万次定时器 (ms) | 空载 RSS (KB) |
|----|-----------|----------------------|------------------|---------------|
| gox  | 5–6   | 618–620 | 166–168 | 16204–16376 |
| node (V8) | 22–23 | 29      | 133     | 46548–46568 |
| bun (JSC) | 5–6   | 10–11   | 109     | 13844–13900 |
| goja | 3     | 177–181 | 117     | 7632        |

## 4. 名次与倍数区间（fib28 以 gox=1.00× 为基准，纯计算口径 = 原值 − startup）

| 项目 | 名次 | 相对 gox 的区间 | 备注 |
|---|---|---|---|
| fib28 纯计算 | ① bun ≈ ② node ≫ ③ goja ≫ ④ gox | bun ~55–60×、node(V8) ~80–95×、goja **0.28–0.29×（gox 慢 ~3.5×）** | bun/node 的差距主体是 JIT vs 解释器的固有差距；**goja 的 ~3.5× 是 gox 实现层面的真实差距**，是 T2-4 优化的靶子 |
| 启动 startup | ① goja ② gox≈bun ③ node | gox 与 bun 同档（~1×），node 慢 ~4× | gox 单文件、无大运行时堆，启动是强项 |
| timers10k | ① bun ② goja ③ node ④ gox | gox 慢 node ~1.25×、慢 bun ~1.5×；与 goja 差 ~1.4× | gox 定时器有实现差距但量级不大 |
| 空载 RSS | ① goja ② bun ③ gox ④ node | gox ≈ bun（~1.2×），node 是 gox 的 ~2.8× | node/V8 自带大堆，口径是空壳内存 |

> 与 Apple M2 上 2026-10-02 的数据（docs/performance.md §2，gox 慢 goja 2.4×）相比，本机 gox/goja 差距扩大到 ~3.5×，方向一致、数值随机器/频率浮动 —— **跨机器引用必须连机器信息一起引用**。

## 5. 复现命令

```bash
# 环境：Go 1.26+（gox 构建），node / bun 任装（缺失自动跳过），goja 依赖首次联网拉取
git clone https://github.com/14752222/Gox.git && cd Gox
(cd bench/goja && go mod download)   # goja 对照系依赖，联网一次即可
./scripts/bench-compare.sh --runs 5  # 一条命令出表 + JSON 存 bench-results/
```

结果 JSON 自带 host 元数据（platform / cpu / cores）；两个栈数据是否可比，先看 host 是否同机。

## 6. 一句话结论

**bun/node 的 fib28 优势主要是 JIT vs 解释器的固有差距；gox 对同类纯解释器 goja 慢 ~3.5×（M2 上 ~2.4×），这才是可优化的真实差距；gox 的启动与空载内存与 bun 同档、远优于 node。**

## 7. 复验记录（2026-10-31，T2-4 优化批次落地后）

同一机器、同一组脚本、按 §5 命令复验（`./scripts/bench-compare.sh --runs 5`，
两次独立运行）。gox 已含 T2-4 的帧池化 / 参数复用 / arguments 惰性化等优化
（main@`3b3fad2`，v0.15.17，Go 1.26.2）：

| 栈 | 启动 (ms) | fib28+10万次调用 (ms) | 1万次定时器 (ms) | 空载 RSS (KB) |
|----|-----------|----------------------|------------------|---------------|
| gox  | 4–5 | **197–204** | 138–139 | 16872–17132 |
| node (V8) | 14–15 | 20 | 122–124 | 46580 |
| bun (JSC) | 4 | 7 | 106 | 13728–13816 |
| goja | 2 | 115–119 | 111–112 | 7632 |

机器：AMD EPYC 9K85 128-Core（32 vCPU 可见，x86_64），Linux 6.6.117（glibc 2.39）；node v22.13.1、bun 1.4.4。
数据文件：`bench-results/bench-20261031-120436.json`、`bench-results/bench-20261031-120505.json`（自带 host 元数据）。

复验要点（fib28 纯计算口径，gox=1.00×）：

- gox fib28 从首跑的 618–620ms 降到 **197–204ms**（T2-4 优化生效，~3.1×）。
- **gox 对 goja（同类纯解释器）的纯计算差距从 ~3.5× 收窄到 ~1.7×**
  （goja 纯计算 ≈113–117ms vs gox ≈193–199ms）。按 docs/performance.md §3.1
  的 ×1.25 阈值**仍未达标**，但已是量级改善。
- 对 JIT 栈的固有差距随之收窄（fib28 原值口径）：bun 从 ~55–60× → **~28×**，
  node(V8) 从 ~20× → **~10×**。定性结论不变：这部分是"解释器 vs JIT"的固有差距。
- gox 的启动与空载 RSS 保持强项：启动与 bun 同档，RSS ≈ node 的 ~0.37×、bun 的 ~1.2×。

> 本节为当前 main 的最新口径，**引用 gox 侧倍数以本节为准**；§3–§6 保留
> 2026-10-28 首跑基线（gox@`74c8320`）作历史对照。结论同步进
> `docs/performance.md` §0 / §3.2。
