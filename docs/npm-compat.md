# npm 兼容面（M5 定稿）

> 本文是 Gox 对 npm 生态「兼容到什么程度、不兼容什么」的**单一权威口径**。
> 装包工具用法见 [`docs/gox-npm.md`](./gox-npm.md)；模块解析实现见
> `vm/vm.go` 的 `resolveNodeModule`。

Gox 是**零 cgo 的 ESM-first JS 运行时**，不是 Node。兼容 npm 生态意味着三件事
分别定调：**语言级兼容**、**Node API 子集**、**native addon**。下面三条红线先
写死，再给逐函数清单。

---

## 红线一：语言级兼容全力追（仅 ESM 包）

**Gox 只跑 ESM（`import` / `export`）。v0 不做 CommonJS。**

| 形态 | 支持 | 说明 |
|---|---|---|
| ESM：`import x from "pkg"` / `import { a } from "pkg"` / `import * as ns` | ✅ | 主力形态 |
| ESM：`export default` / 具名导出 / 重导出 | ✅ | 与 VM 的模块表一致 |
| 顶层 `await` | ✅ | ESM 语义 |
| CommonJS：`require(...)` / `module.exports` / `exports.x =` | ❌ | **v0 显式不支持** |
| CommonJS：`.cjs` 文件 | ❌ 能解析、运行报错 | 见下"代价" |

### CJS 取舍与代价（写死，别当 bug）

- **不做 CJS 的代价**：只提供 CJS 入口（`main` 指向 `.js`/`.cjs` 且内部用
  `require`）的包**装得下来、解析得到，但一运行就报 `require is not defined`**。
  这是刻意的 **loud failure**：`resolveNodeModule` 仍按 `exports > module >
  main > browser` 解析，不会因为"是 CJS"就静默跳过——静默跳过会让用户以为包
  不存在，比运行期报错更难排。
- **为什么不顺带做一层 `require` 垫片**：`require` 的语义（同步、可动态求值、
  可 require 内置与 JSON、可循环、`module.exports` 可被整体替换）与 ESM 的静态
  绑定模型冲突；半吊子垫片会让「有的包能跑、有的包诡异地拿到半个导出」，把
  问题推迟到用户现场。**v0 选择"要么全 ESM，要么明确报错"。**
- **选包建议**：优先选 `exports` 里有 `import` 条件、或带 `module` 字段的包
  （双格式包基本都有）。纯 CJS 老包不在 v0 支持范围。

> 后续若要支持 CJS，应当是独立的里程碑（引入 CJS 求值器 + `module.exports`
> 互操作），而不是在 `loadModule` 里打补丁。

---

## 红线二：Node API 只挑高频子集（未实现即显式 unsupported）

Gox 只把 **`fs` / `path` / `http` / `process`** 四个核心模块做成内置模块
（既能 `import` 也能当全局对象用）。清单如下，**未列出的 API 一律不存在**——
调用会得到 `undefined`/`TypeError`，**不做软降级、不返回假数据**。

### `fs`

| 已实现（20） | 同步 | 异步 |
|---|---|---|
| 读 | `readFileSync` `readBytesSync` | `readFile` |
| 写 | `writeFileSync` `appendFileSync` | `writeFile` `appendFile` |
| 元信息 | `existsSync` `statSync` `readdirSync` | `stat` `readdir` |
| 目录 | `mkdirSync` `rmdirSync` `rmSync` | `mkdir` |
| 删除/移动/复制 | `unlinkSync` `renameSync` `copyFileSync` | `unlink` |

- 异步形态：末参传回调 → `callback(err, data)`；不传回调 → 返回 `Promise`。
- **未实现**（示例，非穷举）：`createReadStream` `createWriteStream` `watch`
  `open` `readFile` 的 `FileHandle`/`fd` 变体 `chmod` `chown` `utimes`
  `symlink` `realpath` `cp` `truncate` `writev` …… 调用即不可用。
- 编码子集：`utf8`(默认) / `base64` / `hex`。其它编码（`latin1`/`utf16le` 等）
  按 utf8 处理——**这是已实现的宽松点，不是"未实现 API"的软降级**。

### `path`

已实现（8）：`join` `resolve` `dirname` `basename` `extname` `isAbsolute`
`sep` `delimiter`。

- 语义对齐 Node：`extname(".bashrc") === ""`、`basename` 去尾分隔符。
- 分隔符跟随宿主（Windows 为 `\`），但输入里的 `/` 全平台接受。
- **未实现**：`normalize` `relative` `parse` `format` `toNamespacedPath`
  `win32` / `posix` 子对象 `matchesGlob`。

### `http`

已实现（3）：`createServer` `get` `request`；另有**全局 `fetch`**。

- `import http from "http"` 拿到的**没有 `fetch`**（对齐 Node 18+ 的 global
  fetch 口径）；`fetch` 只能当全局函数用。
- 服务器处理器收到 `(req, res)`：`req` 有 `method/url/path/headers/query/body/
  getHeader`，`res` 有 `statusCode/setHeader/getHeader/removeHeader/writeHead/
  write/end`。
- 客户端响应对象：`status/statusText/headers/body`（`fetch` 另有
  `ok/text()/json()`）。
- **未实现**：`https` `http2` `Agent` `createServer` 的 TLS/`upgrade`/WebSocket
  `res.sendFile`/`pipe`/流 `ServerResponse` 完整接口、`req.on("data")` 事件式流。
  **请求体一次性缓冲**（上限 16 MiB），不走流。

### `process`

已实现（7）：`argv` `env` `platform` `pid` `cwd()` `chdir()` `exit()`。

- `process.env` 是**进程级快照**（首次构建内置模块时取一次），不是 Node 那样
  的实时视图；`process.argv` 同样是启动时快照。
- **未实现**：`stdin/stdout/stderr` 流、`nextTick`、`hrtime`、`kill`、
  `on("exit")` 事件、`exitCode` 赋值等。

### 内置模块与全局对象的关系

四个模块**同时**以两种形态存在，共用同一批实现函数（不复制）：

```js
// 两者等价：全局对象是历史扩展（脚本不开模块也能用），import 是 Node 兼容面
const data = fs.readFileSync("a.txt");          // 全局
import fs from "fs";                             // 默认导入 = 模块命名空间
const data2 = fs.readFileSync("a.txt");
import { readFileSync } from "fs";               // 也支持具名导入
```

> `import x from "fs"` 的默认导出 = 模块命名空间对象（Node 互操作）。这**不是**
> `module.exports` 互操作——它是给 ESM 默认导入一个落点，`require` 依旧不可用。

---

## 红线三：native addon 永不支持（零 cgo 承诺）

**任何含 `.node` 原生扩展的包，Gox 一律判为不可用，永不支持。**

- 零 cgo 是 Gox 的硬约束（见 [`docs/v1-roadmap.md`](./v1-roadmap.md) §七）。
  `.node` 是平台相关的 ELF/Mach-O/PE 动态库，加载它必须走 cgo/dlopen，
  直接违背该约束。
- **行为**：`gox install` 不做特殊拦截（静态无法可靠枚举包内是否含 `.node`）；
  运行期一旦 `import` 到尝试加载原生扩展的包，会以模块解析 / 编译 / 运行错误
  结束，不会静默降级。
- **报错文案**（面向用户，写死于此）：

  ```
  Cannot load native addon 'pkg/build/Release/addon.node':
  Gox 零 cgo，永不支持 .node 原生扩展。请改用纯 JS 替代包，
  或把原生能力下移到 Gox 宿主的 native host 契约（gx/* 模块）。
  ```

- **替代路径**：需要原生能力时，用 Gox 自己的 `gx/*` 宿主模块 + `nativeHost`
  契约（平台能力在宿主 Go 侧实现并桥接给 JS），而不是引入 npm 原生扩展。

---

## 刻意不做（v1）

以下**不是欠账，是明确不做**，口径与 [`docs/v1-roadmap.md`](./v1-roadmap.md)
§七一致：

| 项 | 原因 |
|---|---|
| `SharedArrayBuffer` | 无共享内存 / 无多线程 JS，零 cgo 下无意义 |
| `BigInt` | 刻意不实现（`docs/undecided-and-unimplemented.md` §三） |
| `Date` | 无 `Date`，用 `Temporal`（路线图 §七） |
| 真 `String.normalize` | 同上，刻意不实现 |
| CommonJS `require` | 见红线一 |
| native addon `.node` | 见红线三，永不支持 |
| 视频解码 | 路线图 §七（`<video>` 标签与宿主契约在，解码不做） |

---

## node_modules 解析支持矩阵

装机后的包怎么被 `import` 到，取决于包的入口字段。Gox 的解析顺序与支持面：

| 入口字段 | 支持 | 说明 |
|---|---|---|
| `exports`（字符串） | ✅ | 等价于 `{".": "..."}`，仅对根入口生效 |
| `exports`（`{".": ...}` / `{"./sub": ...}`） | ✅ | 含子路径键 |
| `exports` 条件对象 | ✅ `import` > `default` | **`require` 跳过**（不做 CJS）；`browser`/`node`/`types` 等其它条件 v1 不认 |
| `exports` 数组 | ✅ 取第一个可用项 | |
| `exports` 通配子路径（`"./*"`） | ❌ | 落空后退化到直接文件解析 |
| `module` | ✅ | 打包器事实标准，ESM 入口；优先于 `main` |
| `main` | ✅ | 经典入口；指向 CJS 时解析成功、运行报错（红线一） |
| `browser`（字符串） | ✅ | 对象映射形式 ❌（v1 不解析） |
| 无任何入口字段 | ✅ | 兜底 `index.js` / `index.ts` / `index.tsx` |
| 子路径 `pkg/sub` | ✅ | 先查 `exports` 的 `./sub`，未命中则直接当文件解析（v1 比 Node 宽松） |
| 作用域包 `@scope/pkg[/sub]` | ✅ | |
| 逐级向上查找 `node_modules` | ✅ | 从当前文件目录一路到根 |

> 解析失败时，报错会**列出所有尝试过的候选路径**（含每一级 `node_modules`
> 目录），便于自行排错。这也是"未实现即显式失败"的体现。

---

## 相关文档

- 装机命令 `gox install` / `gox add`：[`docs/gox-npm.md`](./gox-npm.md)
- v1 明确不做清单：[`docs/v1-roadmap.md`](./v1-roadmap.md) §七
- 未决/未实现总表：`docs/undecided-and-unimplemented.md`
- 核心模块 API（全局形态）：`docs/tutorial.md` 与 `website/api/host.md`
