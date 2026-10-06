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
