# 单文件产物体积与自解压开销基准（T3-6）

「单文件分发」是卖点，但它有两个隐含承诺：产物别太大、启动别被自解压拖慢。
这份文档把两个承诺都变成可复现的数字和门槛，改一行代码后重跑一条命令就能看出
是赚是赔。

## 结论（先说要点）

1. **最小单文件产物 15.91 MB**（linux/amd64，内嵌 90 B 脚本），gzip -9 后
   **6.49 MB** —— 这就是用户实际下载的体积。但它是**动态链接 glibc** 的
   （`CGO_ENABLED` 继承宿主环境，本机为 1）：扔到 musl/Alpine 上跑不起来。
   `CGO_ENABLED=0` 重打即 **15.86 MB 静态产物（只省 49 KB，但换成真正可搬运）**。
2. **自解压不拖慢启动**：单文件 4.08 ms（中位）vs 裸 gox 二进制 4.21 ms（中位），
   差值 −0.14 ms，落在测量噪声内（三次独立运行分别 −0.10 / −0.17 / −0.14 ms，
   范围完全重叠）。单文件反而略快，因为它链接的包比 `cmd/gox` 少（见下文归因）。
3. **真正会拖慢启动的是脚本本身，不是解压**：脚本从 90 B 涨到 1 MiB，启动
   +4.27 ms/次（解压落盘 + 词法扫描合计，未拆分）。而把解压目标从 overlay 的
   `/tmp` 换成 tmpfs 的 `/dev/shm` 只差 0.06 ms —— **写盘这部分可以忽略**。
4. 体积的最大一块不是 Go runtime，是 **esbuild 及其连带的编译期依赖**
   （单独 text+data 2.27 MB，连带剔除后实际省 4.01 MB）：它经
   `vm → tools/tstransform` 被链进运行时，只为支持"直接跑 .ts"。而单文件产物在
   打包时已把 TS 预转译成 JS（`packager` 的 `prepareAppFiles`），这部分体积在
   发布产物里是纯浪费。实验实测：把 `tstransform` 换成空实现后 **11.91 MB
   （−4.01 MB，−25.2%）**。这构成下面最值得做的瘦身目标。

## 单文件产物是怎么造出来的

读 `cmd/gox/cmd_build.go` + `packager/main.go` 得到的事实：

- `gox build` 只接受 `android|ios|windows|macos`（`cmd_build.go:94`）——**没有
  linux 目标**。桌面目标走 `buildDesktop` → 在仓库根 `go run ./packager <entry>
  --gui --name ... --target <os>/<arch>`。
- `packager`（jsbuild）的流程：收集入口的**相对 import** 模块 → 若入口是 TS，先在
  构建机预转译（故意不把 tstransform 链进产物模板，见 `prepareAppFiles` 注释）→
  在临时目录生成一个 Go 模块（`go:embed all:app` + 运行时模板 `appMainGo` /
  `appMainGoGUI`，`go.mod` 用 `replace` 指回本仓库）→ `go mod tidy` →
  `go build -trimpath -ldflags "-s -w"`。
- 所以**单文件产物本来就已经 strip 过**（`-s -w` 恒开），且 `-trimpath` 也在。
- 运行时行为：`main()` 把 `embed.FS` 里的脚本 `extractFS` 到 `os.MkdirTemp("",
  "jsapp-*")`，读回入口文件，走 lexer → parser → compiler → `vm.RunCompiled`，
  退出时 `RemoveAll`。**没有 fork 子进程、没有二次 exec**，"自解压"只是"写一遍
  临时文件"，所以它的成本上限就是一次几 KB 的 write + 一次 read。
- 可调标志（`packager/main.go` 的手工参数解析）：`--gui`（GUI 模板，带窗口消息泵）、
  `--target os/arch`、`--windowed`（Windows 无控制台，追加 `-H windowsgui`）、
  `--icon`、`--version`、`--appid`。**没有暴露 ldflags 开关**，`-s -w` 写死。

本基准用 **CLI 模板**（不带 `--gui`）：GUI 模板要窗口/GPU，本机无法无头跑真实
窗口循环，测出来的是"引擎 + 解压"这条公共路径。差异见"未验证项"。

## 数字

全部为本次实测，原始数据见 `bench-results/binary-size-20261028T010322Z.json`。
体积单位 **MB = 10^6 B**（括号内为 MiB）。

### 体积

| 产物 | 原始 | gzip -9 | 相对裸 gox（-s -w） |
| --- | ---: | ---: | ---: |
| 裸 gox，默认 ldflags | 28.27 MB (26.96 MiB) | 14.97 MB | +38.8% |
| 裸 gox，`-ldflags="-s -w"` | 20.36 MB (19.42 MiB) | 8.32 MB | 基线 |
| **单文件，内嵌 90 B 脚本（动态链接）** | **15.91 MB (15.17 MiB)** | **6.49 MB (6.19 MiB)** | **−21.8%** |
| 单文件，同上但 `CGO_ENABLED=0`（静态链接） | 15.86 MB (15.13 MiB) | 6.47 MB | −22.1% |
| 单文件，内嵌 1 MiB 脚本 | 16.97 MB (16.19 MiB) | 6.49 MB | −16.6% |

四件事值得记下：

- `-s -w` 值 7.91 MB（28.27 → 20.36，−28%），而 `packager` 恒开它，所以单文件
  没有"忘了 strip"的风险。
- 单文件比裸 `-s -w` 还小 21.8%，因为它只链接 CLI 模板要的包
  （compiler / lexer / parser / stdlib / vm），不背 `gfx`、`wgpu`、`naga` 和 gox 的
  子命令。**这个差值不是优化出来的，是包集合差异**，别把它当成瘦身成绩。
- **默认产物是动态链接的**：`file` 报 `dynamically linked`，`ldd` 依赖
  `libc.so.6`。这不是 `packager` 的选择，而是它把宿主 `CGO_ENABLED=1` 原样传给
  内部的 `go build`。`CGO_ENABLED=0` 只省 49 KB，但把"单文件"变成真正的"一个
  文件" —— 对分发是质变，不是量变。
- 1 MiB 脚本只让原始体积 +1.06 MB（内嵌是未压缩的），gzip 后只 +5.6 KB（填充是
  注释，压缩率极高）——**下载体积几乎不随脚本增大**，但启动会（见下）。

### 启动 wall time（25 次，交替采样，ms）

| 组合 | 中位数 | 最小 | 最大 | 相对裸二进制 |
| --- | ---: | ---: | ---: | ---: |
| 裸 gox `-s -w` + 同一脚本 | 4.21 | 3.78 | 5.08 | 基线 |
| **单文件** | **4.08** | **3.56** | **4.45** | **−3.3%** |
| 单文件（1 MiB 脚本） | 8.34 | 7.76 | 15.80 | +98.0% |

- 自解压净开销 = −0.14 ms（单文件 − 裸二进制）。**负值**，意为单文件在这个口径下
  不慢于裸二进制。
- 绝对值在同机不同批次间会漂（另两次运行：3.69/3.86、3.59/3.69 ms），所以**只信
  同批次内的差值**，不要跨批次比绝对值。
- 1 MiB 脚本的边际开销 4.27 ms/MiB。为了判断这 4 ms 里有多少是"解压写盘"，做了
  一个只改 `TMPDIR` 的对照实验（同一产物，25 次）：
  默认 `/tmp`（overlay）3.32 ms vs `/dev/shm`（tmpfs）3.26 ms，差 0.06 ms。
  **结论：解压写盘可以忽略，4 ms 几乎全在词法扫描 1 MiB 注释上。**

## 体积归因（为什么是 esbuild）

对同一模板去掉 `-s -w` 反编译符号表（`go tool nm -size`，只统计 T/t/R/r/D/d，
排除 BSS），原始 22.81 MB 未 strip 产物里 text+data 共 9.25 MB：

| 包前缀 | text+data |
| --- | ---: |
| `github.com/evanw/esbuild` | 2.27 MB |
| `github.com/14752222/Gox` | 2.15 MB |
| `go:func.*`（函数元数据） | 1.08 MB |
| `crypto/internal/fips140` | 0.49 MB |
| `vendor/golang.org/x` | 0.23 MB |
| `net/http/internal` | 0.20 MB |

esbuild 的来源是 `vm/eval.go`、`vm/module.go` → `tools/tstransform` → esbuild，
即"运行时直接跑 .ts"这个开发期特性。它对**发布产物**是死重：打包时 TS 已被
`prepareAppFiles` 预转译为 JS。

隔离验证（**不改仓库**，用 `go build -overlay` 把 `tools/tstransform` 三个文件
替换成空实现，其余一字不动）：

| 变体 | 原始 | gzip -9 | 说明 |
| --- | ---: | ---: | --- |
| 现状（同模板 `-trimpath -s -w`） | 15.91 MB | 6.49 MB | 与 jsbuild 产物逐字节同尺寸 |
| 去 esbuild（overlay 空实现） | **11.91 MB** | **4.87 MB** | −4.01 MB / −25.2%，脚本仍正常输出 `hello gox 6` |

## 可验证的瘦身目标

门槛都落在 `scripts/binary-size.sh` 的 JSON 字段上，`jq` 一条命令即可判定，
不需要人肉读表。

| # | 目标 | 当前 | 目标值 | 验证 |
| --- | --- | ---: | ---: | --- |
| G1 | 单文件不回退（防体积漂移） | 15.91 MB | **≤ 16.0 MB** | `jq -e '.sizes_bytes.single_file.raw <= 16000000'` |
| G2 | 打包路径去掉 esbuild（`-tags nots`） | 15.91 MB | **≤ 12.5 MB** | 同上，阈值 12500000（实验值 11.91 MB，已实测） |
| G3 | 下载体积（gzip） | 6.49 MB | **≤ 5.0 MB** | `jq -e '.sizes_bytes.single_file.gzip <= 5000000'`（G2 后实验值 4.87 MB） |
| G4 | 启动不因自解压退化 | −0.14 ms | **≤ 裸二进制 + 1.0 ms** | `jq -e '.delta_us.single_vs_bare <= 1000'` |
| G5 | 产物真可搬运（静态链接） | 动态链接 glibc | **statically linked** | `jq -e '.sizes_bytes.single_file.linkage == "statically linked"'` |

G2 的落地方式（留给后续任务，本任务未改 Go 代码）：给 `tools/tstransform` 加一个
`nots` build tag 的薄实现，`packager` 生成产物时追加 `-tags nots`。风险点：
`gox <file.ts>` 与运行时 `import "./x.ts"` 依赖它，所以 tag 只能在**产物模板**上开，
开发期二进制必须保留真实实现。

**G4 是这次实测最有说服力的门槛**：它把"体积变大是否拖慢启动"这个卖点疑虑钉死
在 1 ms 以内，且当前还有 1.14 ms 余量。

G5 落地方式同样是一行级别的：`packager` 的 `buildOnce` 目前把宿主环境原样传给
内部的 `go build`，显式补一条 `CGO_ENABLED=0` 即可；代价是体积反而小 49 KB，
收益是产物不再依赖目标机的 glibc 版本（"单文件"才名副其实）。本任务未改动
`packager`，只给出实测的两种结果。

## 口径说明（复现这些数字需要知道的）

- 工具链：`go1.27.3 linux/amd64`。`CGO_ENABLED` **继承宿主环境（本机为 1）**：
  默认产物动态链接 glibc；静态变体是显式 `CGO_ENABLED=0` 打的（见体积表）。
- 仓库：commit `a2e8935`（`baseline: main @ 2a0e146`）。
- 机器：AMD EPYC 9K65，32 核，Linux 6.6.117 x86_64（容器）。
- 体积：`stat -c %s`；gzip 为 `gzip -9 -c <bin> | wc -c`（**gzip 不是 zstd/xz**，
  换压缩器数字会变）。
- 启动：外部 wall time，bash 5 内建 `EPOCHREALTIME`（微秒），从 fork 到进程退出，
  stdout/stderr 丢弃；每组预热 3 次后测 25 次，报中位数/最小/最大。
  A/B 交替采样（不是先跑完 A 再跑 B），把机器负载漂移摊到两边。
- **page cache 是热的**：预热已把二进制读进内存。真实首次启动会更慢，但两个产物
  同样受影响，差值口径不变；冷启动数字见"未验证项"。
- 单文件由 `packager` 真实产出（`--target linux/amd64`），不是手工拼模板：
  实测 `go build -trimpath -ldflags "-s -w"` 复现出的同类模板产物与 jsbuild 产物
  字节数完全一致（15,913,223 B），说明基准没有偏离发布路径。

## 复现

```bash
# 一条命令：产出 JSON + Markdown 表（构建失败会记进 skipped[] 并继续）
bash scripts/binary-size.sh                 # 默认 25 次
RUNS=11 bash scripts/binary-size.sh         # 快点
KEEP=1 bash scripts/binary-size.sh          # 留中间产物排查

# 体积归因（未 strip 才看得到符号）
# 1) 用 packager 同一模板建一个未 strip 的产物：
#    取 packager/main.go 里 appMainGo 常量 + 最小 app/main.js，go.mod 用 replace 指回仓库
go tool nm -size <bin> | awk '$3 ~ /^[TtRrDd]$/ {split($4,a,"/"); p=""; for(i=1;i<=3;i++) if(i<=length(a)) p=p (i>1?"/":"") a[i]; s[p]+=$2} END{for(k in s) printf "%9.2f MB %s\n", s[k]/1048576, k}' | sort -rn | head
```

## 未验证项（没有数字，不要引用成结论）

- **Windows / macOS 产物体积**：本机无对应工具链/签名环境，未构建。Windows 还需
  额外 `.syso`（图标+版本资源），体积会略增。
- **GUI 模板（`--gui`）产物**：桌面产品实际走 GUI 模板，会额外链入 `gfx`/`wgpu`/
  `naga`，体积和启动都应显著高于本文的 CLI 模板数字。**本文所有体积数字都不适用于
  GUI 产物**，只能作为"引擎 + 自解压"公共路径的下界。
- **冷启动**（page cache 冷）：未测。
- **1 MiB 边际开销的拆分**：4.27 ms 里解压 vs 词法扫描各占多少，只知道写盘≈0.06 ms，
  词法占大头，但未做逐项隔离。
- **`nots` tag**：只有 overlay 空实现的体积实验，没有可用的功能分支；11.91 MB 是
  "体积上限收益"，不代表功能无损方案一定能做到同尺寸。
- **arm64 / 交叉编译产物**：未测（仅 linux/amd64）。
- **G5（默认静态链接）未落地**：只测了显式 `CGO_ENABLED=0` 的结果；把 `packager`
  默认改成静态会同时影响 Windows/macOS/移动端打包，需要一次完整回归才敢改。
- **桌面产物的图标/元数据开销**：Windows `.syso`、macOS `.app` bundle 等未计入。
