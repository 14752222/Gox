# DevTools Inspector v1（`gx/dev`）

> 里程碑 [M3] 交付文档。出口验收：**可查看节点树与运行时状态**（元素树 / REPL 求值 / console 留存）。
>
> 落点：`gfx/dev.go`（模块注册）+ `gfx/dev_tree.go` / `gfx/dev_console.go` / `gfx/dev_eval.go`；
> console 留存缓冲在 `object/devlog.go`（被 `stdlib/console.go` 写入）。
> 演示：`testdata/devtools_demo.js`（脚本自绘 Inspector 面板）。

`gx/dev` 是**开发期只读数据面**：面板是普通脚本，用拉取式刷新（`setInterval`）读取快照后自绘。
内核不推送、不内建面板、不引入新的窗口/协议。生产零成本：不在脚本里 `import` 就没有任何开销。

---

## 1. 能力清单

### 1.1 `devSnapshot()`

既有能力（devtools 方案 A）。返回一帧的运行时统计快照。

```js
import { devSnapshot } from "gx/dev";
const snap = devSnapshot();
// snap.frame       → { count, full, partial, fullRatio }
// snap.imageCache  → { size, cap, hits, misses, evicts }
// snap.glyphCache  → { size, cap, hits, misses, evicts }
// snap.tree        → { windows, nodes, depth }
// snap.solid       → { effects }        (gx/solid 未注册时 effects = -1)
// snap.warnings    → [{ at, text }]     最近 64 条内核警告
```

### 1.2 `devTree(opts?)` — 元素树（新增）

返回全部活动窗口的节点树快照。

```js
import { devTree } from "gx/dev";

const t = devTree();                      // 默认边界：深度 8 / 节点数 2000
const t2 = devTree({ maxDepth: 4 });      // 只看上四层
const t3 = devTree({ maxNodes: 300 });    // 总节点预算
```

返回：

```js
{
  windows: [
    {
      id: 1,                 // 稳定窗口号（Mount 时分配，永不复用）
      title: "",             // 见 §3 边界：窗口标题在当前架构下不可达，恒为空串
      scope: "win:1",        // gx/router 的路由作用域标识
      root: <node>
    }
  ],
  truncated: false           // 任一窗口被截断即为 true
}
```

节点形状：

```js
{
  tag: "column",             // 元素名；文本节点为 "#text"
  path: "0/2/1",             // 稳定寻址串（见 1.3）；根恒为 "0"
  key: "row-3",              // 仅当 props 里带字符串/数字 key 时出现
  text: "hello",             // 仅 "#text" 节点出现（内容不在 props 里）
  props: { ... },            // 安全序列化后的 props（见 §3）
  box: { x, y, w, h },       // 布局结果（客户区像素）
  focusable: false,          // 是否可进 Tab 序（gx/a11y 的同一判据）
  role: "button",            // 仅当有非空 ARIA role 时出现
  truncated: true,           // 仅当该节点的子节点被截断时出现
  children: [ ... ]
}
```

**有界**：默认最大深度 8、最大节点数 2000（全部窗口合计），可用 `opts` 覆盖。
超限的节点自身带 `truncated: true`，顶层也带一个 `truncated` 汇总位 —— 元素树面板
不能因为拉取数据把自己撑爆（虚拟化长列表的树仍可能上千节点）。

### 1.3 `devNode(path, windowId?)` / `devProps(path, windowId?)` — 按路径取节点（新增）

`path` 是每次快照当场算出的稳定寻址串：`"0/2/1"` = 根 → 第 2 个子节点 → 第 1 个子节点，
根恒为 `"0"`。配 `devNode` / `devProps` 做同一次快照内的二次取值。

```js
import { devNode, devProps } from "gx/dev";

const btn = devNode("0/0/0");       // 单节点（含受同一有界规则约束的子树）；未命中 → null
const p   = devProps("0/0/0");      // 只取该节点的 props；未命中 → null
const w2  = devNode("0/0", 2);      // 多窗口下按窗口号消歧（不传则取第一个匹配窗口）
```

### 1.4 `devLogs(level?, limit?)` — console 留存（新增）

`console.log / info / warn / error` 在**照旧写 stdout/stderr 之后**，追加一条到
`object` 里的固定容量环形缓冲（默认 500 条）。`devLogs` 读这份留存。

```js
import { devLogs } from "gx/dev";

devLogs();            // 全部留存（旧→新，最多 500 条）
devLogs("error");     // 只看 error 一档（log / info / warn / error）
devLogs("", 20);      // 最近 20 条
// → [{ level, text, at }]   at 为 "15:04:05"（与 devSnapshot().warnings.at 同格式）
```

### 1.5 `devEval(code)` — REPL 求值（新增）

在当前 dev 环境里求值，**跨调用保持状态**（REPL 语义）。

```js
import { devEval } from "gx/dev";

devEval("let a = 1");     // → { ok: true,  value: "1",  error: "" }
devEval("a + 1");         // → { ok: true,  value: "2",  error: "" }   ← 状态保住了
devEval("1 +");           // → { ok: false, value: "",   error: "vm error: ..." }
devEval(123);             // → { ok: false, value: "",   error: "devEval: code must be a string" }
```

`value` 是结果值的 **`Inspect()` 文本**（例如字符串结果带引号），而不是活值 ——
面板要的是"能显示的一行"，把可能带环/带句柄的对象塞回 JS 反而会让面板自己撑爆
（与 `devTree` 的序列化口径一致）。

---

## 2. 接线点：让 `devEval` 看到真实的 app 环境

理想语义是"在**当前运行的 app VM 环境**里求值"。但 gfx 侧**拿不到**那个环境：
应用 VM 的 `*runtime.Environment` 只存在于宿主入口（`cmd/gox` 的 `runFile` / `devRun`）
的局部变量里，`gfx/render.go` 也没有包级引用（本次审计确认，且 `render.go` 属于
并行会话的所有权，不能改）。因此本能力按"能力 + 接线点"落地。

宿主（拥有 VM 实例的一方）在**挂载脚本之后、进事件循环之前**调用一次：

```go
v, err := vm.EvalFileVM(path)   // 或 vm.EvalVM(src)
if err != nil { /* ... */ }
gfx.SetDevEnv(v.Globals())      // ← 接线：之后 devEval 就在真实 app 环境里求值
_ = v.RunTimersWithPump(gfx.Pump)
```

- 接线后：REPL 里的 `let a = 1` 与 app 自己的全局共享同一名字空间（这正是 REPL 该有的语义），
  `devEval("a + 1")` 能读到 app 里定义的变量。
- 传 `nil` 取消接线。

**未接线时的退化**：首次 `devEval` 会惰性建一个独立的 REPL 沙盒（`stdlib.SetupGlobals`），
让面板在纯演示 / 单测里也能用。代价是它的全局与 app 隔离（读不到 app 变量）——
这是刻意取舍："REPL 能跑但看不到 app 全局" 比 "面板里输入什么都是 not attached 错误" 更有用。
`testdata/devtools_demo.js` 走的就是这条退化路径（演示脚本无法调用 Go 侧 `SetDevEnv`）。

> 依赖方向核对：`gfx → vm`（为 `vm.EvalWithGlobals`）与 `gfx → stdlib`（沙盒）均无环 ——
> `vm` 不 import `gfx`，也不 import 任何 `gfx` 子包；`vm` 本身已依赖 `stdlib`。
> 为什么必须走 `vm.EvalWithGlobals`（顶层程序编译）而不是对象包装：只有顶层 `let`
> 才会在环境里留下**全局**绑定，包装成 `(function(){...})` 会把 `let` 变成局部，
> 状态就留不下来。

---

## 3. 边界（v1）

### 3.1 有界截断（第一原则）

- `devTree` 默认 `maxDepth=8` / `maxNodes=2000`，超限截断并标 `truncated`。
  这是**强制**的：一次拉取会把整份数据经 JSON 形状对象复制一遍，不设上限的
  元素树面板会把自己撑爆。
- props 值序列化同样有界：函数 → `"[Function]"`，GuiNode → `"[Element <tag>]"`
  （不把另一棵树拖进来），循环引用 → `"[Circular]"`，超过 3 层 → `"{…}"` /
  `"[Array n]"`，数组最多 20 项、对象最多 32 键、字符串最多 200 字符，超出以 `"…"` 收尾。
  兜底走 `Inspect` 且带 `recover` —— **任何值都不允许让快照 panic**。

### 3.2 只读

`devTree` / `devNode` / `devProps` / `devLogs` 只读内核状态（`appsSnapshot` + 节点字段 +
日志缓冲副本），不修改任何状态、不触发重绘、不注册任何回调。

### 3.3 生产零成本

- 不 `import "gx/dev"`：模块导出表惰性构建，没有实例化、没有拉取、没有开销。
- console 留存是一次常数级 append（容量固定，超出从头裁剪）；不读就没有别的成本。

### 3.4 已知不可达 / 未做

- **窗口标题恒为空串**：标题存在 `Window` 句柄上（`gfx/window.go`），而句柄不进
  `app` 注册表；`app` 结构体在 `render.go`（非本里程碑所有权），无法追加字段。
  需要标题的宿主可在 `SetDevEnv` 的接线处顺带自建一张 `id → title` 表。
- **多窗口同路径**：`devNode` / `devProps` 不传 `windowId` 时取第一个匹配窗口；
  多窗口下同一 `path` 可能存在于多个窗口，需要精确时传第二个参数。
- **面板与 app 同生共死**：元素树坏掉时面板一起坏（方案 A 的已知边界）；需要
  "卡死现场可看"时应评估旁路方案（见 §4 的通道讨论），v1 不做。
- **REPL 是全局作用域求值**：函数局部变量由 VM 栈槽承载、不进环境链，所以
  `devEval` 只能访问全局绑定（与 `eval` 的间接求值口径一致）。

---

## 4. CDP 兼容可行性评估

**结论：当前不可行。** 现在无法把 DevTools Inspector 暴露成 Chrome DevTools Protocol
（CDP）端点供 VSCode / Chrome DevTools 直连。缺三块，且都在本次工作之外：

### (a) 通道：WebSocket / JSON-RPC —— 运行时无 WebSocket

仓库里没有 WebSocket 实现（无 `gorilla/websocket`、无自研握手/帧解析），也没有监听
本机的 JSON-RPC 服务端。`gx/dev` 的数据面是**进程内、同线程、同步返回**的 JS 对象，
不是可被外部进程按协议拉取的端点。CDP 的第一步（`ws://.../devtools/page/...` 握手 +
`Runtime.evaluate` / `DOM.getDocument` 等 JSON-RPC 消息）就没有承载物。

### (b) 出口：节点树与运行时状态的序列化 —— 正是本次新做的

CDP 的 `DOM.*` / `Runtime.getProperties` 需要把"元素树 + 运行时状态"序列化成协议
要求的 JSON 形状。本次的 `devTree` / `devNode` / `devProps` / `devLogs` / `devEval`
给出的正是这套**序列化出口的雏形**（有界、只读、可寻址）。但它现在是 JS 侧对象，
不是 CDP domain 的 wire format，也没有把"节点身份"映射成 CDP 的 `nodeId` / `backendNodeId`。

### (c) 本体：VM 断点 / 单步 / 调用栈 —— `vm/` 里零命中

CDP 的 `Debugger.*`（`setBreakpoint` / `resume` / `stepInto` / `getStackTrace` / `paused`
事件）需要 VM 本体具备：断点表与 PC 级匹配、暂停/恢复状态机、调用栈（frames）与作用域
链的按需序列化、异常暂停。这些在 `vm/` 里目前**一处都没有**（`vm/` 只有生成器恢复、
panic 诊断的 `dbgPC` 等内部调试字段，不是可供外部控制的调试协议）。

### 若要做 VSCode 直连，需要哪三块

1. **调试协议服务端**：在本机监听一个 WebSocket（或 stdio）端点，解析/生成 CDP 的
   JSON-RPC 子集（至少 `Runtime`、`Debugger`、`DOM`），并把消息投回 VM 线程串行执行。
2. **VM 调试本体**：断点表 + PC 匹配、暂停/恢复/单步状态机、调用栈与作用域链的序列化、
   以及"暂停时快照全局/局部"的稳定出口 —— 这是工作量最大的一块。
3. **两套 ID 的稳定性**：CDP 需要稳定的 `nodeId` / `scriptId` / `frameId`。元素树会因
   keyed 复用/条件渲染整体重建而抖动，需要一套"跨重建的稳定身份"（可复用列表 `key`
   的思路），否则断点与节点选择会在每次重渲染后失效。

### v1 范围建议

**不做 CDP。** 先用 `gx/dev` 这套进程内数据面 + 脚本自绘面板（本里程碑）覆盖
"看节点树 / 看日志 / 试求值"这三件事；把 (a)(b) 拆成"本地调试旁路"独立里程碑，
(c) 单独立项。CDP 只有在"要接现成 IDE/浏览器工具链"时才值得付这个代价，当前
需求（内核自查 + 面板展示）用方案 A 已经满足。
