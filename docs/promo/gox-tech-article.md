# 我用 Go 写了个 JavaScript 引擎和原生 GUI 框架

> 从词法分析到字节码虚拟机，再到一层自己写的光栅化 GUI —— 零 cgo、单二进制、无外部运行时依赖。
> 这篇文章讲三个我自己觉得最值得说的点，以及一堆我踩过的坑和一群我明确决定"不做"的东西。

---

## 一、为什么要重复造轮子

起因很朴素：我想写一个跨平台的小桌面工具，需求只有两句话 ——

1. 界面逻辑用脚本写，改一行就能看到效果，不用重新编译；
2. 交付给用户的是一个文件，双击就能跑，不需要先装 .NET / JRE / Node。

第一条把范围收窄到"嵌一个脚本语言"。第二条把 Electron 这类方案直接排除了：它满足第一条，但第二条做不到 —— 一个空壳 Electron 应用就上百兆。

剩下的选择就只剩 Go：静态编译、交叉编译一条命令、产物就是一个二进制。但 Go 生态里现成的 JS 引擎（goja 等）只解决"执行 JS"，不解决"画界面"。而当时能用的 Go GUI 方案要么依赖 cgo（于是失去交叉编译），要么是把 Web 技术栈塞进去（于是回到原点）。

所以最后的问题是：**能不能在纯 Go 里，把这两层都自己写出来？**

Gox 就是这个问题的答案。仓库地址在文末。下面先给一张全景图，然后聊三个我认为最值得说的点。

---

## 二、架构总览：一条从源码到像素的管线

整个仓库可以分成两段。上半段是**语言**，下半段是**界面**。

```text
  .js 源码
      │
      │  lexer/       词法分析（1845 行）
      ▼
  Token 流
      │
      │  parser/      语法分析（7401 行）
      ▼
  AST  (ast/)                                    1598 行
      │
      │  compiler/    作用域/符号表解析 + 字节码生成（7453 行）
      ▼
  字节码 (bytecode/)   定长 3 字节：[opcode 1B][operand 2B 大端]
      │
      │  vm/          栈式调用帧 + 事件循环 + 模块加载（8792 行）
      ▼
  执行结果 ──────► object/（对象模型）  ──►  stdlib/（内建对象）
      │
      │  gfx/         软件光栅化 + flex 布局 + 命中测试 + 脏矩形重绘（50333 行）
      ▼
  像素（win32 / X11 / cocoa / Android / iOS / 鸿蒙 六个后端）
```

括号里的行数是非测试 Go 源码行数，我自己数的：

| 包 | 源码行数 | 测试行数 | 干什么 |
|---|---:|---:|---|
| `lexer/` | 1845 | 760 | 词法分析 |
| `parser/` | 7401 | 4121 | 语法分析 |
| `ast/` | 1598 | 0 | AST 节点定义 |
| `bytecode/` | 944 | 139 | 指令集与常量池 |
| `compiler/` | 7453 | 877 | 字节码生成 |
| `vm/` | 8792 | 17938 | 虚拟机与事件循环 |
| `object/` | 13236 | 641 | JS 对象模型 |
| `stdlib/` | 17995 | 750 | 内建对象 |
| `gfx/` | 50333 | 38667 | GUI 渲染层与平台后端 |

这张表本身就在讲一个事实：**GUI 那一层的代码量是语言层的三倍多**。写引擎是有明确教材可循的（词法、语法、字节码、栈式 VM），写渲染层没有 —— flex 布局、文本整形、脏矩形合并、多屏坐标口径、输入法协议，全是琐碎的平台细节。这是我在开工前严重低估的部分。

---

## 三、钩子一：零 cgo 的纯 Go 字节码 VM

### 3.1 管线是教科书式的，但指令编码很省事

`lexer → parser → AST → bytecode → VM` 是标准套路，没什么好吹的。真正让我省了大量麻烦的是**指令定长 3 字节**：

```text
[ opcode 1B ][ operand 2B，大端 uint16 ]
```

解码就是"读 1 字节拿到操作码、读 2 字节拿到操作数、PC 前进 3 字节"，没有变长指令的分支预测，也没有需要对齐的跳转补偿。操作数的含义按指令类别约定：常量加载是常量池下标、变量指令是符号表槽位、跳转是字节偏移、函数调用是参数个数。目前 `bytecode/opcode.go` 里定义了 149 个操作码，按 16 个一组分区（栈操作 / 常量加载 / 变量操作 / 运算 / 跳转 / 函数调用 ……）。

代价也很直白：一个操作数只有 16 位，所以常量池、局部变量槽位、跳转偏移都有 65535 的天花板。对"小工具"这个定位来说够用，我不是在设计通用 JIT 后端。

### 3.2 性能：我把真实数字摆出来，包括难看的那部分

自研引擎最容易翻车的地方就是"跑个 fib 然后吹性能"。所以我把基准做成了仓库里的一条命令，并且**把两个对照系都放进来了**：goja（同样是纯解释器，可作为同类的对照组）和 node/V8（JIT，作为上限参照）。

最近一次跑批（`bench-results/bench-20261009-131018.json`，Linux / Intel Xeon 6981E-C / 32 核，每项外部 wall time 跑 3 次取中位数）：

| 栈 | 启动 (ms) | fib28 + 10 万次调用 (ms) | 1 万次定时器 (ms) | 空载 RSS (KB) |
|---|---:|---:|---:|---:|
| **gox** | 8 | **788** | 192 | 13364 |
| goja | 6 | **250** | 128 | 7632 |
| node (V8) | 35 | 47 | 152 | 46488 |

另一次在 Apple M2 / 8 核 / macOS 上跑的（`bench-results/bench-20261002-024818.json`，runs=5）：

| 栈 | 启动 (ms) | fib28 (ms) | 1 万次定时器 (ms) | 空载 RSS (KB) |
|---|---:|---:|---:|---:|
| **gox** | 11 | **322** | 124 | 22592 |
| goja | 7 | **137** | 121 | 7792 |
| node (V8) | 55 | 45 | 152 | 48432 |

**结论必须诚实地说**：

- 扣掉启动开销后，gox 的纯计算比 goja **慢约 2.4 倍**（M2 那组：311ms vs 130ms；Xeon 那组更差，780ms vs 244ms ≈ 3.2 倍）。这是我最该对标、也最暴露实现差距的一组数字。goja 同样是纯解释器，它比我们快这么多，说明差距来自实现（缺少 inline cache、热路径分配过多），不是"解释器 vs JIT"的宿命。
- 与 V8 的差距是 ~60 倍量级（纯计算口径）。这里面绝大部分是"解释器 vs JIT"的固有差距。
- **我们并不处处落后**：启动开销 8ms（比 goja 的 6ms 略慢，比 node 的 35ms 快 4 倍），1 万次定时器 192ms（与 goja 的 128ms 同量级）。短板集中在**纯计算热循环**这一块。

那些"比 V8 快 N 倍"的说法我一个都不敢写，因为写不出来。

复现命令（脚本在 `scripts/bench/`，零依赖，计时交给外部 harness）：

```bash
# 一条命令跑完全部基准（解释器对比 + 帧率/GC）
./scripts/bench-all.sh --runs 5

# 只跑 gox / node / goja 解释器对比
./scripts/bench-compare.sh --runs 5

# 首次联网准备 goja 对照（之后可离线）
cd bench/goja && go mod download && cd ../..

# UI 帧率 / GC 毛刺基准
go test ./perf/ -v
```

goja 的对照放在 `bench/goja/` 一个**独立的 Go module** 里（`goxbench/goja`），这样主模块的 `go build ./...` 不会被 goja 的依赖污染。它对标的口径也写在 `docs/performance.md` §8 里，包括一条重要的限制：goja 内核不提供宿主定时器，所以它的"1 万次定时器"那一列是用一个最小事件循环补齐的，只能作参考，不能当成 goja 调度器的性能。

### 3.3 UI 不掉链子，靠的是渲染侧的三件事

引擎算力是短板，但用 Gox 写界面**并不会卡** —— 这是两件事。渲染侧已经落地的机制（`docs/performance.md` §4、§5）有三处：

1. **image / glyph LRU 缓存**：解码后的图片按张数上限 16 淘汰，字形掩码容量 1024。把"重复解码图片"和"重复栅格化字形"这两个最容易每帧爆炸的分配源挡在热路径之外。
2. **`<scroll vlist>` 虚拟化**：只物化视口 + 上下 buffer。实测（`bench-results/vlist-20261001.json`，10 万行）：首帧从全量的 **2187ms** 降到 **89ms**，滚动帧从 **998.8ms** 降到 **0.115ms**。
3. **GC 停顿可控**：`docs/performance.md` §5 记录的 UI 重压基准（800 行、每帧更新 200 行、2400 帧 × 3 次聚合）里，帧间隔 p95 是 **5.95ms**（60fps 的单帧预算是 16.7ms），单次 GC 停顿最大 **1.382ms**。

关于 GC，v1 的决策是**不自研 GC，依赖 Go 运行时**，理由写在 `docs/performance.md` §6：JS 对象全是普通 Go 对象，要自研增量 GC 就得接管分配器、界定精确根集，而实测 GC 停顿离 5ms 可感知阈值还有 3.6 倍余量 —— 收益近乎为零，风险极大。这不是偷懒，是算过账的。

---

## 四、钩子二：为什么"v1 明确不做"的清单，比合规率数字本身更有价值

### 4.1 现在的真实数字

先给数字，两个套件分开跑、分开记基线（分母不同，混在一起会互相污染）：

| 套件 | 通过 / 总数 | 跳过 | 合规率 | 数据来源 |
|---|---:|---:|---:|---|
| `language` | 18620 / 23726 | 41 | **78.48%** | `docs/test262-baseline.json`（2026-10-09） |
| `built-ins` | 8503 / 23823 | 369 | **35.69%** | `docs/test262-baseline-builtins.json`（2026-10-09） |

language 这一路是 15.32% → 31.56%（T04 那一轮，见 `docs/test262-t04-report.md`）→ 现在 78.48%。built-ins 的 35.69% 里也含最近一次引擎修复的净收益 +541 例 / +2.27pp。

### 4.2 别盯百分比，盯失败面的形状

35.69% 听起来很差。但**"差"的分布才是决策依据**。我把 built-ins 的全量失败按形态切了一遍（`docs/test262-builtins-clusters.md`，脚本 `scripts/test262-cluster.py`，带 `--self-test` 钉住判据）：

| 失败形态 | 条数 | 占失败面 | 排期含义 |
|---|---:|---:|---|
| 语义偏差 | 9694 | 63.28% | 能力在，值/顺序/边界/this 校验与规范不符 —— **主战场** |
| 功能零实现 | 5185 | 33.84% | 停在编译前端或运行期报"没有这个东西" —— **成块补实现** |
| harness 依赖 | 369 | 2.41% | 用例根本没进引擎 —— 成本最低，先清 |
| crash | 52 | 0.34% | 超时挂死 —— 优先级最高，会污染整个分片 |
| 假阳性退潮 | 20 | 0.13% | negative 用例判定失配 |

一句话读法：**六成是"做得不对"，三成是"没做"**。这两种的修法完全不同：前者只能按方法逐个对齐规范（慢），后者可以先决定"做不做"。

再往下钻一层，522 个簇里 **Top 10 就占了失败面的 42.75%**：

| # | 目录族 | 形态 | 簇规模 | 族基线（通过/总数） |
|---|---|---|---:|---:|
| 1 | `built-ins/Array/prototype` | 语义偏差 | 1708 | 967/2812 = 34.39% |
| 2 | `built-ins/TypedArray/prototype` | 功能零实现 | 1311 | 0/1411 = **0.00%** |
| 3 | `built-ins/Temporal/ZonedDateTime` | 语义偏差 | 605 | 277/901 = 30.74% |
| 4 | `built-ins/Temporal/PlainDateTime` | 语义偏差 | 531 | 226/773 = 29.24% |
| 5 | `built-ins/Temporal/PlainDate` | 语义偏差 | 452 | 184/652 = 28.22% |

其中两个观察值得单独说：

- **Temporal 是"一族七个簇"**：Top 10 里 6 个都是它。按目录切开会拆成 7 张单，同一套实现要面对 7 次，所以它应该合并成一张单（基线 1360/4605 = 29.53%）。
- **#2 的族基线是 0.00%**：1411 条全灭。全族零通过不是"偏差"，是"门没开" —— 1306 条报的是同一个根因（`%TypedArray%.prototype.resize` 未实现）。分母大、现值 0、根因单一，这种簇的投入产出比最高。

排期就是照这个形状定的：先清 52 条 crash（它会污染整轮跑批的可信度）→ 再清 369 条 harness（runner 侧，成本最低）→ 然后打"全族 0%"的功能零实现簇 → 最后按方法族推进语义偏差。

### 4.3 一个具体的拍板：不做 Intl

这是"取舍哲学"最典型的一例。Intl 是 built-ins 里面唯一一个"要引入一整个数据生态"的领域（CLDR + ICU 级别的本地化实现）。但实测下来：

- `intl402` 套件**不在**我们的口径内（`suiteDirs` 只有 language / built-ins / annexB）；
- built-ins 里路径命中 Intl 关键字的只有 205 条（占全量 0.86%）；
- 但**源码真的引用 `Intl`** 的只有 **11 条**（集中在 Temporal 的日历/时区分支）。

11 条 vs 一整个 CLDR 数据栈。所以拍板：**不做**，并把理由写进 `docs/test262-builtins-clusters.md` §四 —— 包括"如果将来 Temporal 推到日历那一步必须碰，就只做 `Intl.DateTimeFormat` 的 `resolvedOptions().timeZone` 一个取值"。

还有一层考虑是**口径一致性**：`gox test262` 的分母是"实际收集到的用例"，把未实现的领域灌进分母，等于让上游新增用例就能把合规率打下去几个百分点 —— 那是噪声不是回归。

我认为这份"明确不做什么"的清单，比 78.48% 这个数字本身更有价值。数字会随跑批变动，清单不会。它回答的是"这个运行时能用来干什么、不能用来干什么"，而这才是使用者真正要问的。

复现命令：

```bash
# language 套件
gox test262 -root /opt/test262 -suite language -json report.json
python3 scripts/check-compliance.py --current report.json \
        --baseline docs/test262-baseline.json

# built-ins 全量跑批（约 60s @ -jobs 16）
gox test262 -root /workspace/test262 -suite built-ins -jobs 16 -json /tmp/builtins.json -quiet

# 聚类分析：把 15000 条失败压成 10 个可开单的簇
python3 scripts/test262-cluster.py --results /tmp/builtins.json --top 10
```

---

## 五、钩子三：自研 GUI 的分层纪律 —— 换后端零改动，可选能力走可选接口

这一节是我最想分享的工程经验，因为它和"怎么写一个 JS 引擎"完全无关，是纯架构问题。

### 5.1 内核与后端之间只有一个接口，而且很窄

整个渲染内核对"平台"的全部要求，就是 `gfx/gfx.go` 里的 `Surface` 接口 —— 五个方法：

```go
type Surface interface {
	Show(img *image.RGBA)                            // 整帧上屏
	ShowRegions(img *image.RGBA, rects []image.Rectangle) // 脏矩形局部上屏
	Size() (w, h int)                                // 客户区尺寸
	WaitEvents(maxWait time.Duration) bool           // 等事件（并分发）
	Events() <-chan Event                            // 事件流
}

type WindowFactory interface {
	Create(cfg WindowConfig) (Surface, error)
}
```

**内核给后端的是一张已经画好的 `*image.RGBA` 和一组脏矩形。** 后端不需要懂布局、不需要懂 JSX、不需要懂命中测试 —— 它只要"把这块像素贴上去"和"把平台消息翻译成 Event 投递进通道"。

选哪个后端，靠 `gfx/backend/` 里按 build tag 分发的一张表：

```go
//go:build windows
import _ "github.com/14752222/Gox/gfx/win32"   // 纯 syscall，无 cgo

//go:build linux
import _ "github.com/14752222/Gox/gfx/x11"     // 纯 Go X11 协议绑定，无 cgo

//go:build darwin
import _ "github.com/14752222/Gox/gfx/cocoa"   // purego 桥接 Objective-C Runtime，无 cgo
```

应用侧只有一句 `import _ "github.com/14752222/Gox/gfx/backend"`，换平台不改一行代码。

### 5.2 关键纪律：可选能力**不进** Surface 接口

真实世界里，平台能力是不齐的：Windows 有原生文件对话框，X11（在我们的实现里）没有；桌面有窗口层级，移动端根本没有"窗口位置"这个概念。

最自然的写法是把所有方法塞进 `Surface`，不支持的返回 error 或 no-op。这条路走过一次就回不来了 —— 每加一个新能力，所有后端都要补一个空方法，而"某个后端悄悄没实现"会变成运行期的静默降级，极难发现。

Gox 的做法是**可选接口 + 类型断言**：

```go
// 改标题 / 改客户区尺寸 —— 每个后端都做得到
type windowController interface {
	SetTitle(title string)
	ResizeClient(w, h int)
}

// 层级 / 尺寸约束 / 全屏 / 激活 —— 差异极大，X11 要 WM 配合，移动端没有
type windowManager interface {
	SetLevel(level string)
	SetSizeConstraints(minW, minH, maxW, maxH int)
	SetResizable(on bool)
	SetFullscreen(on bool)
	Activate()
}

// 光标形状 —— 纯观感，没有就算了
type cursorHost interface {
	SetCursor(shape string)
}
```

内核侧的用法是"断言一下，落空就降级"：

```go
if wc, ok := surf.(windowController); ok {
	wc.SetTitle(title)
}
// 落空：静默 no-op —— 改不了标题不该让应用崩
```

三条纪律是从踩坑里长出来的：

1. **拆到"平台差异的同质粒度"**。`windowController`（每个后端都做得到）和 `windowManager`（差异极大）必须分开，否则后端为了补一个能力被迫实现一堆空方法。
2. **方法名必须导出**。win32 / x11 / cocoa 是另外的包，Go 不允许跨包实现未导出方法 —— 这条是编译错误教我的。
3. **后端包里写编译期断言**。"少了就是运行时静默降级"这种 bug 最难查，所以让它在编译期就炸：

```go
// gfx/win32/window.go
var (
	_ gfx.Surface = (*surface)(nil)
	_ interface {
		SetLevel(level string)
		SetSizeConstraints(minW, minH, maxW, maxH int)
		SetResizable(on bool)
		SetFullscreen(on bool)
		Activate()
	} = (*surface)(nil)
)
```

这套纪律的代价写在注释里：可选接口未导出，后端包引用不到它的名字，只能按方法集自证（多写上面那一小块）。换来的是**内核的既有接口永远不动**。

### 5.3 同一份内核，六个后端

| 平台 | 后端 / 宿主 | 现状 |
|---|---|---|
| Windows | `gfx/win32`（纯 syscall） | 窗口、IME、剪贴板、原生对话框全支持 |
| Linux | `gfx/x11`（Wayland 下走 XWayland） | 窗口全支持；IME 与剪贴板**暂不支持** |
| macOS | `gfx/cocoa`（purego 桥 objc runtime） | 全支持；多屏枚举与 `onDisplayChange` 已落地 |
| Android | Kotlin 壳（`SurfaceView` + `Choreographer`） | 六个能力模块基本齐备；模拟器 x86_64 / API 34 **实测通过** |
| iOS | Swift 壳（`UIView` + `CADisplayLink`） | 模拟器链路已验收（Xcode 27 / iPhone 15 Pro）；**真机验收待做** |
| 鸿蒙 | ArkTS 壳（`PixelMap` + `onTouch`） | 只通了安全区与折叠上报两条纯上报通道；交叉编译 + HAP 构建通过，**设备上尚未实跑** |

移动端这一格的分工值得单独说：**gfx 不直接开窗口**。Android / iOS 的"窗口"是宿主壳的 `SurfaceView` / `UIView`，必须由宿主先创建画布与帧缓冲，再把表面交给引擎。所以 `gfx/backend/backend_android.go` 和 `backend_ios.go` **刻意是空的** —— 注册动作发生在 Kotlin 调 `nativeInit`、Swift 调 `gox_init` 的时候。留这两个文件是为了让"按平台选后端"这张表在移动端有明确的一格，否则它们会落进 `backend_other.go`（那个文件自称"未支持的平台"，语义是错的）。

界面逻辑仍然全部写在 JS 里，node / layout / raster / font / 事件泵一行不改。

### 5.4 "没有的能力就报没有"

这大概是我在这个项目里最强的执念。原生能力层（设备、定位、相机、权限、媒体）缺能力时，**异步 API 报 `unsupported`，不返回假数据**：

```js
if (canIUse("video")) {
  // 走平台视频层
} else {
  // 先问再选路：跳系统播放器
}
```

`docs/gui-guide.md` 里有一条我特别喜欢的边界：**`upload` 在没有对话框后端时什么都不做，绝不编造文件名**。编造一个文件名让调用方"看起来成功了"，是比报错糟糕得多的失败模式。

主题系统也是同一套思路的产物（`docs/theme.md`）：32 个命名 token，换主题就是换一组 token 值，暗/亮切换不重启、下一帧生效；但**脚本显式写的 props 永远赢过主题** —— 切暗色不会把用户自己写的配色改掉。

---

## 六、踩过的坑

挑五个真实的，其余都在仓库的 triage 文档里。

**1. `var` 解构借用了 `let` 的编译路径**

`compileDestructureAssignment` 的第二参 `isDecl bool` 只能表达两态，`let` / `const` / `var` 三种声明全传 `true`，于是统一走了"登记进**当前块**作用域"的分支。结果：`for (var [p] of [[1],[2],[3]]) {}` 之后读 `p` 抛 `ReferenceError` —— 而同作用域内的 `var [u] = [1]` 却正常。

"半好半坏"的形状是定位的起点。根因是 `var` 的绑定落点必须在**函数作用域层**，不是块层。详见 `docs/r4McL4-var-destructure-scope.md`。

**2. 假虚拟化会骗过断言**

`<scroll vlist>` 写完后我加了个断言"树上剩多少节点"，结果一直是绿的 —— 因为实现是"先全建再销毁"，视口外的节点确实被销毁了，只是销毁前已经全量渲染过一次。

判定纪律改成：**必须数"渲染调用次数"，不能数树上剩多少节点**。改完之后数字才对得上（10 万行首帧 89ms，恒定 64 次渲染调用）。

**3. 多屏坐标口径差一倍，且不报错**

各平台原生窗口坐标的单位不同：win32 / x11 是设备像素，cocoa 是**点**。Retina（scale=2）上不做换算，`moveTo` / `position` 会差整整一倍，窗口挪到错误位置却一声不吭。

修法是只在一处换算（`gfx/window_move.go` 的 `toDevicePx` / `fromDevicePx`），并把"脚本侧恒设备像素"作为唯一对外口径写进 `docs/multi-window.md`。另外脚本看到的位置是**工作区相对**（排除任务栏 / Dock），不是虚拟桌面绝对 —— 因为脚本真正想表达的是"相对这块屏还要挪多少"，这个值在多屏拼接变化时不随排列改变。

**4. 一次"修复"揭出了 7 条假通过**

修正 `new` 的解析之后，built-ins 有 5 条 negative 用例从"通过"变成"失败"。查下去发现根因是 **Gox 的 `new` 没有做 IsConstructor 校验**：旧的错误解析把 `new obj.method()` 变成 `(new obj).method()`，靠"new 一个普通对象报 TypeError"**偶然**通过了这些用例。解析一修，真实缺陷就暴露了。

这类"假阳性退潮"我单独记在聚类报告的遗留里 —— 数字变难看，但信号是真的。

**5. 模块入口的异常渲染丢了类型名**

`Test262Error` 实例在 Go 侧是 `*object.Object`（不是 `*object.Error`），模块入口的未捕获异常渲染走了 `Value.Inspect()`，得到 `{ message: "" }` —— 构造器类型名完全丢失，而 runner 的 negative 判据要匹配类型名。缺的不是语义（`message` 为空是正确行为），缺的是**渲染没走 JS 的 `ToString`**。修法是让模块入口与 script 入口共用同一个 `vm.uncaughtError`。

---

## 七、v1 明确不做什么

这份清单写在 `docs/v1-roadmap.md` §七，是"不是欠账"的意思，不是"待办"：

- **不做视频解码**。`<video>` 的标签与宿主契约已落地，播放交给后端可选实现的 `nativeVideoHost`；桌面三后端目前都没接，于是降级为封面/占位 + 一次 `onError({code:"unsupported"})`。内核不解码、也不假装在播。
- **`gx/update` 不做差分更新、无回滚 UI、不做进程热替换**（v1.2 候选）。
- **SharedArrayBuffer 与真 `String.normalize` 刻意不实现**。`BigInt` 只做到 Temporal 需要的程度（`stdlib/bigint.go` 的注释写得很清楚：它是 `Instant.prototype.epochNanoseconds` 的前置依赖），`toLocaleString` 也退化为 `toString` —— 不是完整的 BigInt 规范实现。
- **`grid` 不做轨道语法 / colSpan**；内置图标只 15 个。
- **无 `Date`**（用 `Temporal`）；**零 cgo ⇒ `-race` 不可用**。
- **不自研 GC**（依赖 Go 运行时，理由见 §3.3）。
- **不做 Intl**（理由见 §4.3）。
- 移动端：**iOS 真机签名验收、鸿蒙设备上实跑**这两项还没做 —— 我不假装它们做完了。

顺带说一句桌面分发（`docs/desktop-distribution.md`）：Windows / Linux 产物是静态单文件，Linux 上是纯 Go + xgb 走 X11 协议，任何发行版直接跑；macOS 出 `.app` bundle，支持 `--arch universal`（编译两次后 `lipo` 合并）。签名与公证在 `release.yml` 里做了"缺凭证就优雅跳过"。移动端不适用这套 —— 那边是"预编译库 + 一层宿主壳工程"，需要 NDK / DevEco / Xcode（但不需要 Go）。

---

## 八、最后

回头看，这件事真正难的不是"写一个 JS 引擎"。词法、语法、字节码、栈式 VM 都有成熟教材，照着做就能跑起来，跑 test262 还会给你一个清晰的进度条。

难的是**在每一处都老实回答"做还是不做"**：Intl 做不做、自研 GC 做不做、视频解码做不做、某个后端没有这个能力时是降级还是编造一个返回值。这些决定没法从教材里抄，而且每一个都会在项目里留下长期影响 —— 写错了，后面每个新后端都要为它买单。

如果你也在写解释器、写渲染层，或者只是想找个能"一个二进制交付"的脚本化桌面方案，欢迎来看看。

- **GitHub**：<https://github.com/14752222/Gox>
- **官网与教程**：<https://14752222.github.io/Gox/>
- **npm**：`npm i -g @goxjs/goxjs`

欢迎 issue / star，尤其是那些"这个能力你没有"的 issue —— 我会明确回复"未做"还是"计划做"，不会假装已经做了。

<!-- 发布前替换为你的署名与主页 -->

---

> 本文中所有数字均可在仓库里核对：`bench-results/bench-20261009-131018.json`、
> `bench-results/bench-20261002-024818.json`、`bench-results/vlist-20261001.json`、
> `docs/test262-baseline.json`、`docs/test262-baseline-builtins.json`、
> `docs/test262-builtins-clusters.md`、`docs/performance.md`、`docs/gui-guide.md`。
> 行数统计为非测试 `*.go` 源码行数，取自本文写作时的 main 分支（0bb8418）。
