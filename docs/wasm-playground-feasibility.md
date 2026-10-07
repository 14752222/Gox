# WASM Playground 可行性结论（rzTQml 收尾）

> 探针基线：`f1a46fb`（wt/docs2）。探针实现：`cmd/goxwasm/`（`main.go` + `main_other.go` + `run_node.js`，
> 源自 2026-10-05 探针提交，内容已在基线上等价存在，本次为**重新验证**而非重做）。
> 验证环境：go1.26.2 windows/amd64、Node v22.22.2、`-ldflags="-s -w"`。

## 1. 结论速览

| 问题 | 答案 |
|---|---|
| 纯 VM 管线能编成 wasm 吗？ | **能**。`GOOS=js GOARCH=wasm go build` 一次通过，无报错无警告 |
| 体积多少？ | **26,844,643 B ≈ 25.6 MiB**（`-s -w`）；不 strip 为 27,368,177 B ≈ 26.1 MiB，strip 仅省 ~0.5 MiB |
| 与桌面 15–24MB 对比 | **wasm 反而略大约 1–10MB**。不是功能更多，而是 js/wasm 目标少了若干原生优化、且把 Go wasm 运行时元数据 + stdlib 全量（含桌面专用的 `update` 包）打了进去 |
| 浏览器里能跑 JS 吗？ | **能**。Node 宿主 12 个用例全部通过（见 §3），含 `console.log`、模板串、`reduce`、错误路径 |
| 能不能做浏览器 Playground？ | **能做 console-only 版**（编辑器 + `goxRunSource`）；图形版不可行（gfx 无浏览器后端，见 §4.1） |

## 2. 复现命令与原始输出

```bash
export PATH="/c/Program Files/Go/bin:/c/Windows/System32:/usr/bin:/bin:$PATH"
export GOCACHE=F:/gocache TMP=F:/tmp GOTMPDIR=F:/tmp

# 构建（js/wasm）
GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o F:/tmp/gox.wasm ./cmd/goxwasm
# → BUILD OK

# 体积
ls -l F:/tmp/gox.wasm
# -rw-r--r-- 1 13649 197609 26844643 Oct  6 21:48 F:/tmp/gox.wasm

# 本机占位目标不受影响
go build ./cmd/goxwasm/   # → goxwasm 仅面向 GOOS=js GOARCH=wasm 构建（当前 windows/amd64）
```

### hello-world 原始输出（Node 宿主，未删减）

```
$ node cmd/goxwasm/run_node.js F:/tmp/gox.wasm
[goxReady] = true
SRC: 1 + 1
OUT: 2
SRC: [1,2,3].map(x => x*2)
OUT: [2, 4, 6]
SRC: function f(n){return n<=1?1:n*f(n-1)} f(5)
OUT: 120
SRC: console.log("hello from gox wasm")
hello from gox wasm
OUT: undefined
SRC: JSON.stringify({a:1, b:[2,3]})
OUT: {"a":1,"b":[2,3]}
SRC: `sum=${[1,2,3,4].reduce((a,b)=>a+b,0)}`
OUT: sum=10
SRC: Object.keys({x:1,y:2}).join(",")
OUT: x,y
SRC: (() => { const o = {n:1}; o.n += 41; return o.n; })()
OUT: 42
SRC: (() => { let r = 0; Promise.resolve(7).then(v => { r = v; }); return r; })()
OUT: 7
SRC: (() => { let r = 0; setTimeout(() => { r = 9; }, 0); return r; })()
OUT: 0
SRC: throw new Error("boom")
OUT: error: Error: boom
SRC: let =
OUT: error: parser errors:
line 1:5: expected identifier, got ASSIGN
SRC: undefinedFn()
OUT: error: ReferenceError: undefinedFn is not defined
```

两点超出探针原始预期：① 微任务用例实测返回 `7`（探针注释预期 0）—— VM 在本次执行结束前 drain 了 job queue；② `setTimeout` 宏任务返回 `0`（预期内）——宿主不驱动 `vm.RunTimers` 时宏任务不执行，这是后续 Playground 要补的宿主任务。

## 3. 探针做了什么

- `cmd/goxwasm/main.go`：js/wasm 最小入口。`goxRunSource(src) -> Promise<string>` 编译并执行一段 JS，
  每次调用全新全局环境（REPL `:clear` 语义）；`goxReady` 标记；`select{}` 常驻。
  刻意只走管线包（lexer/parser/ast/compiler/bytecode/vm/object/runtime/stdlib），**不引 gfx**、不引 `cmd/gox` 宿主代码。
- `cmd/goxwasm/main_other.go`：非 js/wasm 平台的占位 `main`，保证 `go build ./...` / `go vet ./...` 不炸。
- `cmd/goxwasm/run_node.js`：零依赖 Node 宿主（加载 GOROOT 的 `wasm_exec.js` + 12 个求值用例）。

## 4. 关键风险核对（后续 console 版 Playground）

### 4.1 gfx 依赖排除 —— ✅ 已核实可全排除

`GOOS=js GOARCH=wasm go list -deps ./cmd/goxwasm` 中 gfx 出现 **0 次**。全部自有依赖仅 14 个包：

```
github.com/14752222/Gox/{lexer, ast, parser, compiler, bytecode, vm, object,
runtime, stdlib, config, tstransform, update, cmd/goxwasm}
```

gfx 是 Gox 自研 GUI（原生窗口后端），浏览器无对应后端，**图形 Playground 应直接放弃**，只做 console 版。

### 4.2 runtime/object 无 cgo/syscall —— ✅ 已核实

- `object`、`runtime`、`vm` 均为自家纯 Go 包，无 cgo 文件。
- deps 中出现的 `syscall` 是 Go 标准库在 js/wasm 的**纯 Go 实现**（`$GOROOT/src/syscall/syscall_js.go` + `fs_js.go`），
  不是原生系统调用，不引入 cgo/外部链接器。
- 全链接无 C 依赖：js/wasm 目标下 `CGO_ENABLED` 强制为 0，Go 侧不会出现第二套链接路径。

### 4.3 体积与 tree-shaking 空间

当前 25.6 MiB 的构成大头：Go wasm 运行时元数据 + stdlib 全量。已发现的**可砍候选**：

| 候选 | 谁引入的 | wasm 里是否有用 | 备注 |
|---|---|---|---|
| `Gox/update`（自动更新） | **stdlib** 引入 | 无（浏览器无自更新语义） | 最干净的砍伐目标；需把 stdlib 里的 update 引用延迟化/接口化 |
| `Gox/tstransform`（TS 转换） | **vm** 引入 | 看产品定位（Playground 若支持 .ts 输入则有） | 与桌面共用，砍它影响面大，建议保留 |
| `Gox/config` | stdlib 引入 | 部分（路径/环境探测） | 小，风险高收益低，建议保留 |

Go linker 死代码消除默认已开（strip 只再省 0.5 MiB 即为佐证），继续压体积主要靠**拆 stdlib 的 update 引用**，
预计收益 0.5–2 MiB，**不值得为 Playground 单独做**，记录备查即可。

### 4.4 其余已知边界

- `os.Stdout` 在 js/wasm 走异步 fsCall：`goxRunSource` 已按「goroutine + Promise」实现，宿主 `await` 让出事件循环，不会触发 deadlock。
- `setTimeout` 等宏任务需宿主持续驱动 `vm.RunTimers`（探针未做）—— Playground 必须补，否则定时器示例「静默不执行」很难排查。
- 多平台浏览器兼容（Safari/移动端 WebView）未测，留到 console 版验收。

## 5. 分阶段建议

| 阶段 | 内容 | 出口条件 |
|---|---|---|
| P0（可行） | console-only Playground：编辑器 + `goxRunSource` + console.log 桥接 + 宿主驱动 `vm.RunTimers` + 预置示例 ≥10 | 浏览器打开即跑通 10 个示例 |
| P1（可行） | 分享链接：源码 URL 编码进 query/hash，无后端 | 复制链接、新窗口打开得到同一份代码 |
| P2（不建议） | 图形/窗口 Playground（gfx 上浏览器） | 需浏览器后端重写 gfx，成本≈重写一个 GUI 后端，**放弃** |

## 6. 看板单 DoD 归属声明

看板单 rzTQml 的 DoD（**浏览器可编辑运行示例 ≥10 个、分享链接带代码**）是
**探针通过后的后续工作，本轮不交付**——本轮只交付「可行性结论 + 探针验证」：
wasm 可构建、25.6 MiB、VM 管线在 Node 宿主全用例通过、gfx/cgo 风险已排除。

---

# 实测复现（b10/wasm · 2026-10-07）

> 本节为**独立重跑**：在独立 worktree `D:/code/wt-b10-wasm`（分支 `b10/wasm`，基线
> `e17baeb` = `Gox/main`）上，从源码重新构建 wasm、重新测体积、在 Node 里重新跑通，
> 用来核对上文（基于 `f1a46fb` 的探针）结论可否**真正复现**。
> 环境：go1.26.2 windows/amd64、**Node v24.0.0**（上文用 v22.22.2）、官方
> `$GOROOT/lib/wasm/wasm_exec.js`；`GOCACHE=F:/gocache`、`TMP=GOTMPDIR=F:/tmp`。
> 全程**未改任何既有 Go 源码 / 逻辑**，只**新增**一个宿主脚本 `cmd/goxwasm/run_eval.js`。

## 实测 1. 构建（js/wasm，两组标志）—— 原始输出

```bash
cd D:/code/wt-b10-wasm
GOOS=js GOARCH=wasm go build                        -o F:/tmp/gox-b10-plain.wasm ./cmd/goxwasm
GOOS=js GOARCH=wasm go build -ldflags="-s -w"       -o F:/tmp/gox-b10-strip.wasm ./cmd/goxwasm
```

```
=== [1/2] 非 strip 构建 ===
BUILD OK (plain)
-rw-r--r-- 1 13649 197609 27801170 Oct  7 19:19 F:/tmp/gox-b10-plain.wasm

=== [2/2] strip (-s -w) 构建 ===
BUILD OK (strip)
-rw-r--r-- 1 13649 197609 27271037 Oct  7 19:19 F:/tmp/gox-b10-strip.wasm
```

**一次通过，无报错无警告**——结论「纯 VM 管线能编成 wasm」在本基线**复现成立**。

## 实测 2. 体积（两组标志 + 宿主对照）

| 产物 | 字节 | MiB | MB(10⁶) | 说明 |
|---|---|---|---|---|
| `gox-b10-plain.wasm` | 27,801,170 | **26.51** | 27.80 | 不 strip |
| `gox-b10-strip.wasm` | 27,271,037 | **26.01** | 27.27 | `-ldflags="-s -w"` |
| strip 节省 | 530,133 | **0.51** | 0.53 | 约 1.9% |
| `gox-b10-base.exe`（宿主对照） | 25,766,400 | 24.57 | 25.77 | windows/amd64 桌面基线 |

**与上文数字的差异（须显式修正，非结论翻案）**：
- 原结论「strip = 26,844,643 B ≈ 25.6 MiB、plain = 27,368,177 B ≈ 26.1 MiB」是在基线
  `f1a46fb` 上测的；本基线 `e17baeb` 上实测**各大约 0.4 MiB**（strip 27,271,037 B ≈ 26.01 MiB，
  plain 27,801,170 B ≈ 26.51 MiB）。**原体积数字已被实测修正**为上面新值。
- **原结论「strip 仅省 ~0.5 MiB」「wasm 约等于/略大于桌面版」「体积大头是 Go wasm 运行时元数据
  + stdlib 全量」三条定性结论，实测全部成立、未修正。**

## 实测 3. 依赖与剔除清单 —— `go list -deps` 权威核对

```bash
GOOS=js GOARCH=wasm go list -deps ./cmd/goxwasm
```

- **`Gox/gfx` 出现 0 次** ✅ —— 图形层在主构建里被完全排除，与上文 §4.1 一致。
- **无 `C` 包、无 cgo** ✅ —— js/wasm 目标下 `CGO_ENABLED=0` 强制成立。
- 自有依赖包精确清单（13 条，与本探针的 import 图一致）：

```
github.com/14752222/Gox/{cmd/goxwasm, object, config, runtime, update, stdlib,
                        lexer, ast, bytecode, compiler, parser, tstransform, vm}
```

- 依赖总数 227（含 Go 标准库）。**无任何包在 js/wasm 下编译失败**，因此
  **「需要剔除/替换才能编译」的包清单为空**——本目标下没有硬编译阻塞。
- 死重确认：`update`（桌面自动更新）被 **stdlib** 引入（`stdlib/update_module.go:39`），
  且 `update/update.go:29` 拉进 `net/http`；浏览器里语义无用。这是唯一的
  **体积 tree-shaking 候选**（上文 §4.3 判断成立）。

> 注：`strings` 在本 Go wasm 产物上**不能**用作判据——连 `Gox/vm` 正对照也数出 0 条，
> 说明 Go wasm linker 不以 ASCII 明文保留包路径。**包排除必须以 `go list -deps` 为准**。

## 实测 4. Node 里真跑一遍 —— 原始输出

```
$ C:/nvm4w/nodejs/node.exe --version
v24.0.0
```

### 4a. 回归 12 用例（`cmd/goxwasm/run_node.js`，逐字原始输出）

```
$ node cmd/goxwasm/run_node.js F:/tmp/gox-b10-strip.wasm
[goxReady] = true
SRC: 1 + 1
OUT: 2
SRC: [1,2,3].map(x => x*2)
OUT: [2, 4, 6]
SRC: function f(n){return n<=1?1:n*f(n-1)} f(5)
OUT: 120
SRC: console.log("hello from gox wasm")
hello from gox wasm
OUT: undefined
SRC: JSON.stringify({a:1, b:[2,3]})
OUT: {"a":1,"b":[2,3]}
SRC: `sum=${[1,2,3,4].reduce((a,b)=>a+b,0)}`
OUT: sum=10
SRC: Object.keys({x:1,y:2}).join(",")
OUT: x,y
SRC: (() => { const o = {n:1}; o.n += 41; return o.n; })()
OUT: 42
SRC: (() => { let r = 0; Promise.resolve(7).then(v => { r = v; }); return r; })()
OUT: 7
SRC: (() => { let r = 0; setTimeout(() => { r = 9; }, 0); return r; })()
OUT: 0
SRC: throw new Error("boom")
OUT: error: Error: boom
SRC: let =
OUT: error: parser errors:
line 1:5: expected identifier, got ASSIGN
SRC: undefinedFn()
OUT: error: ReferenceError: undefinedFn is not defined
```

**与上文 12 行逐字一致**（含 `console.log` 经 `os.Stdout` 桥接到宿主 stdout、微任务 drain 得 7、
宏任务 `setTimeout` 不驱动得 0、三类错误路径）。上文结论在本基线 + Node v24 **复现成立**。

### 4b. 任务点名用例（新脚本 `cmd/goxwasm/run_eval.js`，任意源码喂入）

```bash
node cmd/goxwasm/run_eval.js F:/tmp/gox-b10-strip.wasm \
  "console.log(1+1)" \
  "var a=[1,2,3]; console.log(a.map(x=>x*2).join(','))"
```

```
[goxReady] = true
SRC: console.log(1+1)
2
OUT: undefined
SRC: var a=[1,2,3]; console.log(a.map(x=>x*2).join(','))
2,4,6
OUT: undefined
```

**引擎在 wasm 里真跑通并打印结果** ✅ ——「宿主传任意 JS → 引擎执行 → 拿回 stdout/结果」
这条最小链路已实测可复现（即 Playground「编辑框 → 运行」的最小内核）。

## 实测 5. 阻塞点（本机实测，比上文更硬的一条）

上文 §4.4 列了 os.Stdout 异步、宏任务需宿主驱动；本轮**新实测出更硬的一条**：

```bash
node cmd/goxwasm/run_eval.js F:/tmp/gox-b10-strip.wasm "process.exit(0)" "1+1"
```

```
[goxReady] = true
SRC: process.exit(0)
ERR: TypeError: Cannot read properties of undefined (reading 'exports')
SRC: 1+1
ERR: Error: Go program has already exited
```

- **`process.exit()` 是致命阻塞点**：`stdlib/process.go:76` 的 `os.Exit(code)` 在 js/wasm 下
  直接终止 Go 运行时，**wasm 实例从此死亡**，后续任何求值都抛 `Go program has already exited`。
  Playground **必须**在宿主层屏蔽/替换 `process.exit`（或让 stdlib 在 wasm 下把它降级为
  抛出可捕获的 `ProcessExit` 异常），否则用户一句 `process.exit(0)` 就能把整个 Playground 打死。
  *（此点上文未覆盖，属本轮新增阻塞点。）*
- 其余阻塞点复核：
  - **cgo / syscall**：自有包 0 处直接 `import "syscall"`；deps 里的 `syscall` 是 Go 标准库
    js 纯实现，无原生链接路径。✅ 不阻塞。
  - **os.Stdout 异步 fsCall**：已由「goroutine + Promise」方案解掉（4a/4b 实测不 deadlock）。✅
  - **宏任务（`setTimeout`）**：宿主不驱动 `vm.RunTimers()`（`vm/vm.go:623`）就静默不执行。⚠️
    Playground 必须补宿主任务泵。
  - **文件系统 / time / 并发**：核心包无直接 `syscall`，未出现 `os/exec`、`plugin`；`time`
    为 js/wasm 纯实现。仅在 `process.exit`、`process.chdir` 等 `process.*` 边界会触到宿主语义。
    ⚠️ 这些 `process.*` 面需在 Playground 层做白名单/桩化。

## 实测 6. 结论（实测修正后的最终版）

| 问题 | 实测结论 |
|---|---|
| ① 能不能做？ | **能**（console-only）。证据：js/wasm 一次构建通过；`go list -deps` 无 gfx/cgo；Node v24 下 12+2 用例全跑通并打印结果 |
| ② 体积基线 | **strip 27,271,037 B ≈ 26.01 MiB**（plain 27,801,170 B ≈ 26.51 MiB）；比宿主 24.57 MiB 大约 1.4 MiB |
| ③ 需剔除/替换的包 | **编译层面为空**（无包失败）。**体积层面**唯一候选 = `update`（+它拉入的 `net/http`），由 stdlib 引入 |
| ④ 阻塞点 | `process.exit()` **打死实例**（实测，须屏蔽）；`setTimeout` 宏任务需宿主驱动 `RunTimers`；`process.*` 面需桩化；os.Stdout 异步已解 |
| ⑤ 下一步 | 见 §5 分阶段建议（P0 console 版 → P1 分享链接 → P2 图形版放弃），**实测后维持原建议**；另**必须**加「process.exit 屏蔽 + 定时器泵」两项工程 |

**对既有结论的处置**：①③④⑤ 与上文一致、实测**复现成立**；② 的**具体字节数**被实测**修正**
为 26.51/26.01 MiB（原 26.1/25.6 MiB，基线与 Node 版本不同所致，定性结论不变）；
§4.4 阻塞点清单被实测**新增**一条 `process.exit` 致命项。

## 实测 7. 尚未覆盖的点

- **浏览器真机未测**：本节仍是 **Node 宿主**（Node v24），未在 Chrome/Firefox/Safari/移动
  WebView 的 DOM 环境实跑——console 版的 DOM 侧接线（编辑器、输出面板、分享链接）未验证。
- **`vm.RunTimers` 定时器泵未接**：`setTimeout` 用例仍返回 0，宿主泵的工作量未实测。
- **`process.exit` 屏蔽方案未实现**：只定性给出「宿主屏蔽 / stdlib 降级」，未落地代码。
- **首屏加载未测**：26 MiB wasm 在真实网络的下载/实例化耗时、gzip/brotli 压缩后大小、流式
  编译可行性均未测（体积仍是 Playground 的主要风险）。
- **未跑 test262 A/B**（本单不需要）；宿主平台 `go build ./...` + `go test ./...` 已全绿
  （`cmd/goxwasm [no test files]`，其余包全 ok，耗时约 2m6s）。
