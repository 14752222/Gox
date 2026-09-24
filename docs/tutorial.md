# Gox 实战教程

> 面向"照着做就能跑起来"的教程: **API 怎么调** → **内置模块怎么导入** → **怎么用
> `gox create` 起工程** → **路由怎么定义与注册**。
>
> 所有代码块都是 `testdata/` 下**真实存在、可直接运行**的脚本, 并由
> `gfx/tutorial_test.go` 逐条回归 (挂载 / 断言 / 交互), 不是文档里贴的伪代码。
>
> 参考手册(不重复本文): [gui-guide.md](gui-guide.md)(元素/事件/布局全表)、
> [gui-router.md](gui-router.md)(路由权威参考)、[js-runtime-api-tutorial.md](js-runtime-api-tutorial.md)(运行时内核)。

## 目录

- [0. 前置: 拿到 gox 命令](#0-前置-拿到-gox-命令)
- [1. API 的调用方式与参数说明](#1-api-的调用方式与参数说明)
- [2. 内置模块的导入及使用方法](#2-内置模块的导入及使用方法)
- [3. 用 `gox create` 初始化工程](#3-用-gox-create-初始化工程)
- [4. 路由的定义与注册](#4-路由的定义与注册)
- [5. 示例与回归清单](#5-示例与回归清单)

---

## 0. 前置: 拿到 gox 命令

| 方式 | 命令 | 得到什么 |
|---|---|---|
| npm 安装 | `npm i -g @goxjs/goxjs` | `goxjs` 命令 (各平台预编译二进制) |
| 不安装 | `npx @goxjs/goxjs app.js` | 临时运行 |
| 源码构建 | `git clone https://github.com/14752222/Gox.git && cd Gox && go build` | 仓库根的 `Gox` / `Gox.exe` |

下文统一写 `gox`, 等价于 npm 包的 `goxjs` 与源码构建出的 `./Gox`(Windows `Gox.exe`)。

```bash
gox                    # 交互式 REPL (:help / :clear / :exit)
gox testdata/counter_demo.js   # 跑脚本; GUI 脚本会开窗口
gox create my-app      # 脚手架建工程 (§3)
gox help               # 用法; gox version 看版本号
```

> `create` / `new` / `init` 只在第一个参数**不像脚本路径**时才当子命令认(判据是
> 扩展名与路径分隔符) ⇒ `gox create.js`、`gox src/create.js` 仍然老老实实当脚本执行。

---

## 1. API 的调用方式与参数说明

### 1.1 三类 API, 三种拿法

| 类别 | 例子 | 怎么拿 |
|---|---|---|
| **全局对象** | `fs` `path` `http` `fetch` `process` `stats` `console` `obs` `computed` `ever` `once` `setTimeout` `delay` … | 不用 import, 直接就是全局 |
| **ES 模块** | `gx/solid` `gx/gfx` `gx/view` `gx/router` `gx/screen` `gx/dialog` `gx/storage` `gx/dev`, 以及聚合入口 `gox` | 必须 `import {...} from "gx/xxx"` (§2) |
| **宿主配置** | 命令行 (`create` / `<file.js>` / REPL)、环境变量 `GOX_STORAGE_DIR`、脚本内 `setAppName()` | 见 [README 配置说明](../README.md#配置说明) |

**判断口诀**: 名字看起来像 Node 内置模块(`fs`/`path`/`process`)= 全局; 名字带 `gx/` 前缀或
属于界面/路由/存储 = 模块, 要 import。唯一的例外是聚合入口 `gox`, 它把 `gx/*` 的导出并成一行。

### 1.2 同步 API: 成功返回值, 失败抛异常

调用方式与浏览器/Node 一致, 参数位置固定, **失败不会返回错误码而是抛 JS 异常**, 用 `try/catch` 接:

```js
const dir = path.join(process.cwd(), "tutorial-tmp")   // path.join(...段) → string
fs.mkdirSync(dir, { recursive: true })                 // {recursive:true}: 建多级目录, 已存在不报错
const f = path.join(dir, "note.txt")
fs.writeFileSync(f, "hello gox\n")                     // 返回 undefined

const st = fs.statSync(f)                              // {size, mtimeMs, isFile, isDirectory}
console.log(st.size, st.isFile, fs.readdirSync(dir))   // 10 true ["note.txt"]
console.log(JSON.stringify(fs.readFileSync(f)))        // "hello gox\n"

try {
  fs.readFileSync(path.join(dir, "missing.txt"))
} catch (e) {
  console.log(e.name + ": " + e.message)               // Error: fs.readFileSync "...": open ...: 找不到文件
}
```

**同步 API 速查**(常用子集, 全表见 [js-runtime-api-tutorial.md](js-runtime-api-tutorial.md)):

| API | 参数 | 返回 |
|---|---|---|
| `fs.readFileSync(path, encoding?)` | `encoding` 缺省 `"utf8"`, 可给 `"base64"` / `"hex"` | `string` |
| `fs.readBytesSync(path)` | — | `number[]`(0–255) |
| `fs.writeFileSync(path, data, encoding?)` / `fs.appendFileSync` | `data`: 字符串或字节数组 | `undefined` |
| `fs.mkdirSync(path, {recursive})` / `fs.rmSync(path, {recursive, force})` | 选项对象, 也可直接传 `true` | `undefined` |
| `fs.existsSync(path)` / `fs.statSync(path)` / `fs.readdirSync(path)` | — | `boolean` / 状态对象 / `string[]` |
| `fs.unlinkSync` / `fs.renameSync` / `fs.copyFileSync` | 同 Node 语义 | `undefined` |
| `path.join / resolve / dirname / basename / extname / isAbsolute` | 变参字符串 | `string` |
| `path.sep` / `path.delimiter` | — | 平台分隔符 |
| `process.argv / env / platform / pid` | — | 数组 / 对象 / 字符串 / 数字 |
| `process.cwd()` / `chdir(dir)` / `exit(code)` | — | 字符串 / `undefined` |
| `stats.sum(arr)` / `stats.describe(arr)` | 数字数组 | 数字 / `{count, min, max, mean}` |
| `console.log / info`(stdout) `error / warn`(stderr) | 变参, 各自 `Inspect()` 后空格连接 | `undefined` |

### 1.3 异步 API: 省略回调返回 Promise, 传回调走回调式

同一个 API 两种形态, **判据是"最后一个参数是不是函数"**:

```js
// 形态 A: 省略回调 → 返回 Promise (推荐, 配 await)
const text = await fs.readFile(f)

// 形态 B: 末参传函数 → 回调 (err, data); 成功时 err 为 null, 失败时为 Error
fs.readFile(f, "utf8", (err, data) => {
  console.log(err ? "ERR " + err.message : data.trim())
})
```

两条语法纪律:

- **顶层不能 `await`**, 异步逻辑要放进 `async function`; (顶层 `main()` 的返回值会被
  回显成 `Promise { <pending> }` —— 不想要这行噪音就写成 `const boot = main()`, 赋值语句
  不算"顶层表达式回显"。)
- 异步函数两种写法都支持: `async function () { ... }` 与 `async () => { ... }`
  —— 要与外层共用 `this` 就用箭头形式。

**异步 API 速查**: `fs.readFile / writeFile / appendFile / stat / readdir / mkdir / unlink / rm`
的参数与同步版一一对应, 只是多了末位回调; `http.get(url, options?, cb?)` 与
`http.request(url, {method, headers, body}, cb?)` 同构; `delay(ms, value)` 是纯 Promise 的
定时器(参数非法时 **reject** 而不是同步抛错, 便于统一 `try/await`); `fetch(url, options?)`
返回 `Promise<response>`, `response` 上有 `status / statusText / ok / headers / url / body`,
方法 `text()` / `json()`。

### 1.4 定时器与事件循环

```js
setTimeout(fn, ms, ...args)      // → id;  clearTimeout(id)
setInterval(fn, ms, ...args)     // → id;  clearInterval(id); 不清掉进程不会退出
requestIdleCallback(fn) / cancelIdleCallback(id)
setStrictTimeout(fn, ms) / setStrictInterval(...) / setStrictIntervalMode(...)  // 精度可控变体
await delay(ms, value)           // → Promise<value>
```

**"脚本执行到末尾"不等于"进程退出"**: 执行完主脚本后会继续跑事件循环, 直到
**所有定时器清掉、所有挂起任务(如 `http` 服务端)结束**才退出。GUI 模式下事件循环接入窗口
消息泵, **所有窗口都关掉**才退出。

### 1.5 HTTP 服务端与客户端 (本机回环示例)

```js
const server = http.createServer(function (req, res) {
  res.writeHead(200, { "Content-Type": "text/plain; charset=utf-8" })
  res.end("path=" + req.path + " query=" + JSON.stringify(req.query))
})
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve))  // 端口 0 = 系统分配
console.log(server.listening, server.port)                             // true 52946

const resp = await fetch("http://127.0.0.1:" + server.port + "/hello?a=1")
console.log(resp.status, resp.ok, await resp.text())                   // 200 true path=/hello query={"a":"1"}

await new Promise((resolve) => server.close(resolve))                  // 收好事件循环
```

服务端 `req` 上可读 `method / url / path / query / headers / body / getHeader(name)`;
`res` 上有 `statusCode`、`setHeader/getHeader/removeHeader`、`writeHead(code, headers?)`、
`write(chunk)`、`end(body?)`。网络 I/O 在 goroutine 里完成后投递回 VM 单线程执行回调,
所以**回调里访问脚本状态不需要加锁**。

### 1.6 语言层面的边界 (踩之前先看这张表)

| 不支持的写法 | 替代 |
|---|---|
| `var` | 一律 `let` / `const` |
| 顶层 `await` | 放进 `async function` 后调用 |
| `import { x as y } from "..."` | **别名会被静默忽略**(`y` 拿到 `undefined`, 还多声明一个 `as` 绑定) ⇒ 用原名, 或用命名空间 `import * as m` |
| `getStorage(key, 默认值)` | 第二参数被忽略、缺失时返回 `undefined` ⇒ `getStorage(k) ?? 默认值` |

### 1.7 完整示例: [`testdata/tutorial_api.js`](../testdata/tutorial_api.js)

```bash
gox testdata/tutorial_api.js
```

真实输出(节选):

```
== 1.1 全局对象: 不用 import, 直接可用 ==
process.platform = windows
process.cwd()    = F:\desktop\go
process.argv     = ["F:\desktop\go\Gox.exe", "testdata/tutorial_api.js"]
path.sep         = \

== 1.2 同步 API: 返回值 / 异常 ==
statSync         = size 10 isFile true isDirectory false
readdirSync      = ["note.txt"]
readFileSync     = "hello gox\n"
读缺失文件 -> Error: fs.readFileSync "F:\\desktop\\go\\tutorial-tmp\\missing.txt": open ...: The system cannot find the file specified.

== 1.3 异步 API: await / 回调式 ==
await fs.readFile        = "hello gox\n"
回调式 fs.readFile       = hello gox | second line
stats.describe([...])    = {"count":5,"min":1,"max":5,"mean":2.8}

== 1.4 定时器与事件循环 ==
await delay(15, v)       = delay(ms, value) 的返回值
setInterval 第 1 次 ...
== 1.5 HTTP 服务端 + 客户端 ==
server.listening = true port = 52946
fetch -> status 200 ok true
fetch -> body   path=/hello query={"a":"1"}

== 1.6 收尾 ==
所有注册过的定时器都已清掉 ⇒ 事件循环归零 ⇒ 进程正常退出
```

---

## 2. 内置模块的导入及使用方法

### 2.1 两条注入路径, 不要混着记

```
全局对象 (无 import)      fs · path · http · fetch · process · stats · console
                          obs · computed · ever · once · 定时器族
ES 模块 (要 import)       gx/solid · gx/gfx · gx/view · gx/router · gx/screen
                          gx/dialog · gx/storage · gx/dev
                          聚合入口: gox  (= 上面 8 个模块导出的并集)
```

### 2.2 四种 import 形态

```js
import { createSignal, createMemo, createEffect } from "gox";  // ① 聚合入口: 一行拿全常用 API
import { untrack } from "gx/solid";                            // ② 细分模块: 按需导入
import * as solid from "gx/solid";                             // ③ 命名空间: 拿到整个导出对象
import describe, { VERSION, shout } from "./tutorial_util.js"; // ④ 相对路径: 本地模块
```

规则与纪律:

- **相对路径必须写 `./`(或 `../`)前缀与 `.js` 后缀**; 基准目录是**入口脚本所在的目录**
  (等价于 Go 侧 `vm.EvalFile` 的行为 —— 它载入入口时把模块基准路径设成入口所在目录)。
- `import` 一律写在**文件顶部**。写在中间虽然能编译, 但绑定在那之前读到的是 `undefined`;
  而且"文件最后一条语句是 import"会把模块对象当顶层表达式的值回显出来。
- 命名导出用 `{}`, 默认导出改名写 (`import 名字 from`)。`export { a, b }` 这类**解构后再导出**
  是推荐的共享状态写法(`export const [x, setX] = ...` 不走)。
- 用了 JSX 的文件**推荐**显式写 `import { h, render } from "gx/gfx"`(或 `gox`): JSX 在
  parser 层降级成 `h(...)` 调用, `h` 就是渲染工厂。忘了也不会炸 —— 文件里没有 `h` 时
  编译器会自动补一条 `import { h } from "gx/gfx"`(2026-09-22 起), 自己定义/导入的 `h`
  优先, 一个字节都不动。

### 2.3 各内置模块的导出清单 (实测输出)

| 模块 | 导出 |
|---|---|
| `gx/solid` | `createSignal` `createEffect` `createMemo` `createResource` `onMount` `onCleanup` `untrack` `devStats` |
| `gx/gfx` | `h` `render` `requestAnimationFrame` `animate` `openContextMenu` `clipboardReadText` `clipboardWriteText` |
| `gx/view` | `Switch` `Match`（`each` / `show` 是元素级指令, **不用 import**） |
| `gx/router` | `createRouter` `RouterView` `RouterLink` `useRoute` `useRouter` `useRouteState` `lazy` |
| `gx/screen` | `screens` `primaryScreen` `screen` `screenOf` `useScreen` `useScreens` `windowInfo` `useWindowInfo` `posture` `usePosture` `hinge` `regions` `platform` `reportPosture` `resetDisplays` `onDisplayChange` `offDisplayChange` |
| `gx/dialog` | `alert` `confirm` `openFile`（async, 用 `await`） |
| `gx/storage` | `setAppName` `appDataDir` `setStorage` `getStorage` `removeStorage` `clearStorage` `getStorageInfo` |
| `gx/dev` | `devSnapshot` |
| `gox` | 以上全部导出的并集(重名不存在; 未链接 GUI 的宿主自动退化为实际提供的部分) |

### 2.4 用法示例

```js
import { createSignal, createMemo, createEffect } from "gox";
import * as storage from "gx/storage";

// 信号与渲染无关: 无界面也能建、能算、能订阅 (逻辑与界面分离的落点)
const [count, setCount] = createSignal(2);
const doubled = createMemo(() => count() * 2);      // 派生值: 惰性, 依赖变化只标脏

const seen = [];
createEffect(() => { seen.push(count()); });        // 立即跑一次, 之后依赖变化再跑
setCount(3);                                        // 新值 → 通知
setCount(3);                                        // 与当前值相同(===) → 不通知
setCount((c) => c + 1);                             // setter 也接受"旧值 → 新值"函数
console.log(seen)                                   // [2, 3, 4]

// 存储: 数据落在 <UserConfigDir>/Gox/<应用名>, 可用 GOX_STORAGE_DIR 整体改根目录
storage.setAppName("gox-tutorial-demo");
storage.setStorage("theme", "dark");
console.log(storage.getStorage("theme") ?? "light") // dark; 缺失时 getStorage 返回 undefined
storage.removeStorage("theme");
```

两条实测口径(容易误判成 bug, 先记下来):

- **`getStorage` 没有默认值参数**: `getStorage("k", "default")` 的第二参数被忽略, 缺失时返回
  `undefined`(与 [gui-guide §9.3](gui-guide.md) 的文字描述不一致, 以代码为准)。
- **(2026-09-24 已修) 同一个 effect 里既读 signal 又读它的 memo**: 旧实现每轮跑两次 —— 该 effect
  同时经两条路订阅了同一次变更(直接订阅 signal + 经 memo 的 cell), 各自叫它一次, 症状是"计数类
  断言多了一倍"。现在通知按"一趟"去重(先把链上 memo 标脏, 再跑 effect), **每轮只跑一次**, 且读到的
  memo 值必然已是新的。实测输出见下方 §2.3。

完整可运行示例: [`testdata/tutorial_modules.js`](../testdata/tutorial_modules.js) (配
[`testdata/tutorial_util.js`](../testdata/tutorial_util.js) 演示相对路径导入)。

```bash
GOX_STORAGE_DIR=./tutorial-storage gox testdata/tutorial_modules.js
```

```
== 2.1 导入形态 ==
聚合入口  gox        -> h: function render: function createSignal: function
命名空间  gx/router  -> 导出: useRouteState, createRouter, RouterView, RouterLink, lazy, useRoute, useRouter
同一份实现?          createSignal === solid.createSignal → true
本地模块  ./xxx.js   -> VERSION: 1.0.0 | shout: OK | default: util@1.0.0

== 2.3 gx/solid 无界面用法 ==
effect 收到的值序列: [2,3,4]
memo 惰性求值: count = 4 doubled = 8
同时读 signal 与 memo → ["1|10","2|20"] (每轮一次)

== 2.4 gx/storage 持久化 ==
数据目录: tutorial-storage\gox-tutorial-demo
getStorage('theme') = dark
getStorage('缺失') ?? 'light' = light
```

---

## 3. 用 `gox create` 初始化工程

脚手架对标 `npm create vite`: 一条命令铺出一个**开箱即跑**的 GUI 工程, 目录布局就是模板的
真实输出(`scaffold/template/`, 由 `go:embed` 进二进制)。

### 3.1 命令与选项

```bash
gox create <目录> [--name <名字>] [-f|--force]
```

| 参数 | 说明 |
|---|---|
| `<目录>` | **必填**。可以嵌套 (`gox create apps/demo`), 父目录会被创建 |
| `--name <名字>` | 覆盖项目名。缺省取目录名。**目录名不合法的字符会被收敛成短横线**后写进 `package.json` 的 `name`(窗口标题仍用原样的名字) |
| `-f` / `--force` | 目标目录已存在**且非空**时才需要, 缺省直接拒绝覆盖 |
| `-h` / `--help` | 看用法 |
| 别名 | `create` = `new` = `init` |

### 3.2 完整步骤

```bash
# ① 拿命令 (只做一次)
npm i -g @goxjs/goxjs

# ② 生成工程
gox create my-app

# ③ 进入工程
cd my-app

# ④ npm install 只为拿到 goxjs 命令 (Gox 运行时本身零依赖)
npm install

# ⑤ 跑起来: 弹出窗口
npm run dev          # 等价于 goxjs src/main.js
```

真实输出:

```
已生成项目: F:\desktop\go\dist\scaffold-tutorial\my-app

  .gitignore
  README.md
  package.json
  src/app.js
  src/components/counter.js
  src/components/status-bar.js
  src/components/todo-list.js
  src/main.js
  src/store.js
  src/theme.js

下一步:
  cd my-app
  npm install
  npm run dev

不用 npm 也行: gox my-app/src/main.js
完整 API 见 docs/gui-guide.md
```

**不想装 npm 也行**——生成出来的就是普通 Gox 工程, 用二进制直接跑同一条路径:

```bash
gox my-app/src/main.js
```

### 3.3 生成物说明

```
my-app/
├── package.json          # 元信息 + dev/start 两个脚本 (devDependencies: @goxjs/goxjs)
├── README.md             # 工程说明 + 五条最容易踩的坑
├── .gitignore            # node_modules / dist / *.exe
└── src/
    ├── main.js           # 入口: 建窗口 + 挂根组件 (render(<window ...><App/></window>))
    ├── app.js            # 根组件: 路由表 + 页签(RouterLink) + 页面出口(RouterView)
    ├── store.js          # 共享状态: signal 建在模块作用域, 组件读它就订阅
    ├── theme.js          # 设计令牌: 颜色 / 间距 / 字号
    └── components/
        ├── counter.js     # 局部状态 + 受控滑块 (响应式 prop 要传函数)
        ├── todo-list.js   # <view each={todos} key="id"> + <input model={draft} />
        └── status-bar.js  # 派生值 + Switch / Match 多分支 + useRoute 读本页路由
```

生成后 `make it yours` 的推荐改动顺序: `src/theme.js` 换配色 → `src/app.js` 减页签 →
`src/store.js` 换成自己的状态 → 再拆 `src/components/`。**改模板文件要重新编译脚手架**
(模板是 `go:embed` 进二进制的真实文件)。

### 3.4 三条运行 / 交付路径

| 目标 | 命令 |
|---|---|
| 开发期跑窗口 | `npm run dev` (或 `gox src/main.js`) |
| 打包成单文件可执行程序 | `go run ./packager src/main.js --gui -o my-app.exe` |
| 交叉编译到别的平台 | `go run ./packager src/main.js --gui --target linux/amd64 -o my-app` |

分发注意事项(Windows 图标与签名 / Linux 打包格式 / macOS `.app`)见
[desktop-distribution.md](desktop-distribution.md)。

### 3.5 常见错误与处理

| 现象 | 原因 / 处理 |
|---|---|
| `gox create: 目录 my-app 已存在且非空，拒绝覆盖（换一个目录名，或加 --force 明确覆盖）` | 脚手架最常见的误操作是在已有工程里再跑一次 create。换目录名, 或确认可覆盖后加 `-f`(会覆盖同名文件, **不清理其它文件**) |
| `gox create: 无法从目录 "." 推导项目名，请用 --name 指定` | 目标写成了 `.` / `..` 这类没有名字的位置 ⇒ 给 `--name` |
| `gox create: 只能指定一个目标目录` | 多写了一个路径参数 |
| `package.json` 里 `name` 变成 `gox-app` | 目录名/`--name` 全是中文等非字母数字字符, 收敛后为空 ⇒ 用 `--name` 指定一个 ASCII 名字 |
| `ReferenceError: h is not defined`（旧版本才会见到） | 用了 JSX 却没 import `h`。**2026-09-22 起编译器会自动补** `import { h } from "gx/gfx"`；老引擎上加一行 `import { h } from "gox"` 即可 |
| `alert is not a function` / 某个导入名恒为 `undefined` | 名字取错了模块（`alert` / `confirm` / `openFile` 在 `gx/dialog`，不在 `gx/gfx`）—— 见 §2.3 的导出清单。**现在从内置模块 import 不存在的名字会直接编译报错**，并告诉你它在哪个模块 |
| `import { each } from "gx/view"` 拿到 `undefined` | `each` / `show` 是**元素级指令**（写 JSX 属性，不用 import），任何模块都不导出它们；现在会编译期报错 |
| `const [a, {b}] = …` 报 "unexpected token" | 嵌套解构在 2026-09-22 前解析不了；现已支持，可直接写 |
| 点了按钮界面没反应 | 响应式属性写成了快照 —— `value={x()}` / `show={x()}` / `each={xs()}` 要传**函数**: `value={() => x()}` / `show={x}` / `each={xs}` |

---

## 4. 路由的定义与注册

模块: `gx/router`(路由) + `gx/screen`(屏幕/折叠姿态)。权威参考是
[gui-router.md](gui-router.md), 本节是"最短可用路径"。

**心智模型**: 路由是**模块层能力**, 不是渲染层概念 —— 页面注册与匹配、历史栈、守卫都在
`gx/router` 里; 页面切换仍然走内核既有的"函数子节点 + keep-alive 分支"。所以出错时的排查路径
与手写切页完全一样。

### 4.1 三步走

```js
import { h, render, createRouter, RouterView, RouterLink, useRoute } from "gox";

// ① 页面就是普通函数组件 (大写标签 = 当场调用, 不用注册)
function HomePage() { return <text font={15}>home page</text>; }
function DetailPage(props) { return <text font={15}>{"detail id=" + props.param.id}</text>; }

// ② 路由表: 注册就是声明一张数组
const router = createRouter({
  routes: [
    { path: "/",           name: "home",   component: HomePage },
    { path: "/detail/:id", name: "detail", component: DetailPage, props: true },
    { path: "*",           name: "nf",     component: HomePage },
  ],
  initial: "/",
});

// ③ 挂载: RouterView 是"当前页面的出口", RouterLink 是声明式导航
render(
  <window title="demo" width={480} height={360}>
    <column gap={8} padding={12}>
      <text font={12}>{() => "route: " + router.currentRoute().path}</text>
      <row gap={10}>
        <RouterLink to="/"><text font={13}>home</text></RouterLink>
        <RouterLink to={{ name: "detail", params: { id: 7 } }}><text font={13}>detail 7</text></RouterLink>
      </row>
      <RouterView />
    </column>
  </window>
);
```

`createRouter` 两种形态都收: `createRouter({ routes, initial, backKeys, foldable })` 与
`createRouter(routes, opts)`。可选项: `backKeys`(缺省 `true`, 绑定 `Alt+←/→`)、
`foldable`(缺省 `true`, 折叠屏双栏)。

### 4.2 路由记录字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `path` | string | **必填**, 见下面路径语法 |
| `name` | string | 命名路由: `push({name:"detail", params:{id:1}})`、`resolve`。重名先声明者胜(会出声) |
| `component` | 函数 / `lazy(…)` | 页面组件; 不填则这条记录只是分组或重定向 |
| `children` | 数组 | 嵌套路由, 子路径不以 `/` 开头时拼在父路径后 |
| `meta` | 对象 | 随路由传递的任意数据 |
| `redirect` | 字符串/对象/函数 | 进入这条记录时改去别处(有 8 次上限防死循环) |
| `props` | 对象 / `true` | 合并进页面 props; `true` = 把路径参数平铺成 props。页面**总是**能收到平铺的 `props.param` |
| `beforeEnter` | 函数 | 路由级守卫 |
| `keepAlive` | bool | 离开时不销毁子树(见 4.5) |
| `dualPane` | bool | 折叠态下能否作为一栏, 缺省 `true` |

**路径语法(支持的全部)**

| 写法 | 匹配 | 参数 |
|---|---|---|
| `/items` | 静态段 | — |
| `/items/:id` | 一段非空路径 | `params.id` |
| `/items/:id?` | 只允许在末尾; 缺省时该段消失 | `undefined` |
| `/files/*` | 吞掉剩余全部路径 | `params.pathMatch` |
| `/raw/*rest` | 同上, 参数名自定 | `params.rest` |
| `*` | 兜底(写在最后) | `params.pathMatch` |

匹配优先级**不依赖声明序**: 静态段 > `:param` > `:param?` > 通配。**不做**正则约束
(`:id(\d+)`)、`alias`、相对路径 `./sub`(一律按绝对路径解析) —— 参数校验写在 `beforeEnter` 里。

### 4.3 导航方式

```js
<RouterLink to="/list">列表</RouterLink>                       // 声明式
<RouterLink to={{name:"detail", params:{id:7}}} replace>详情</RouterLink>
```

`RouterLink` 额外属性: `to`(string 或 `{name, params}`)、`replace`(bool, 只换当前栈项)、
`activeBackground`(命中当前路由时的背景色)、`disabled`、`scope`。

```js
await router.push("/detail/7")     // → {ok:true, route} | {ok:false, reason:"aborted"|"exhausted"}
await router.replace("/list")
await router.back() / router.forward() / router.go(n)
router.currentRoute()              // 取值函数(不是属性), 读它就是订阅
router.resolve(to)                 // 纯解析, 不导航
router.history() / router.index()  // 历史栈 / 当前指针
router.onError(fn) / router.beforeEach(fn) / router.afterEach(fn)   // 返回注销函数
router.rebuild()                   // 强制视图重算(窗口换屏后用)
```

> 每个窗口**一套独立导航栈**: `router.push("/list", wa)` 只动窗口 A 的栈。
> 窗口根上读 `currentRoute()` 会落到"最近用到的会话"; 要显示**本窗口**的路径,
> 请在**页面体**里用 `useRoute()`。

### 4.4 守卫: 三级, 执行顺序与 vue-router 一致

```
beforeRouteLeave(当前链, 由内向外) → beforeEach(全局, 注册序)
→ beforeEnter(目标链新出现的记录, 由外向内) → beforeRouteUpdate(同记录参数变)
→ beforeRouteEnter(目标链, 由外向内) → 提交(改栈) → afterEach
```

**时机**: 守卫全部通过之前**不动历史栈** ⇒ 被拦下的导航不会留下半截状态。

| 守卫返回 | 结果 |
|---|---|
| `undefined` / `true` / 其它普通值 | 放行 |
| `false` | 中止 (`push` 的 Promise 以 `{ok:false, reason:"aborted"}` 解决) |
| `"/login"` / `{path}` / `{name, params}` | 重定向, 重新走一遍守卫链(上限 8 次) |
| 抛错 / Error | 失败: `onError` 收到错误, Promise 被 reject |
| Promise | 异步守卫, 结算后按上面规则解释其值 |

组件级守卫写在哪: (a) 懒加载模块的**命名导出** `export function beforeRouteLeave(to, from) {}`;
(b) 组件函数对象上的属性 `Home.beforeRouteLeave = (to, from) => {}`; (c) 记录字段
`beforeEnter`。`(a)/(b)` 优先于同名记录字段。

### 4.5 页面状态保留(必须显式选一档)

**销毁后的静态子树无法复活**(`disposeNode` 会摘掉响应式接线), 所以"切页即销毁"不可逆:

| 档位 | 写法 | 保住什么 | 适合 |
|---|---|---|---|
| 子树保活 | 记录上 `keepAlive: true` | 子树原样: 滚动位置、输入焦点、草稿、页面内局部 signal | 经常来回切的页(列表/详情) |
| 状态袋 | `useRouteState()` | 显式 `st.set/get` 进去的值(挂在**历史栈项**上, 子树重建也还在) | 随姿态/换屏会重建的页 |

不保活的页面离开即销毁, 回到**同一条**栈项时组件体重跑, 但 `st.get` 仍读得回之前写的值。
保活页面常驻内存(节点 + effect), 只给真正需要的页开。

### 4.6 懒加载

```js
import { lazy } from "gx/router";
{ path: "/detail/:id", component: lazy(() => import("./pages/detail.js")) }
```

**推荐显式写 `lazy()`**: 守卫链要在提交前拿到组件级守卫, 而它只能从"已加载模块的命名导出"里读;
裸写 `() => import(...)` 首次进入会漏掉 `beforeRouteEnter`(会在 `gx/dev` 警告缓冲里出声)。
`RouterView` 支持 `loading` / `error` 两个分支属性; **加载失败不拦导航**。

### 4.7 完整可运行示例: [`testdata/tutorial_router.js`](../testdata/tutorial_router.js)

```bash
gox testdata/tutorial_router.js
```

演示覆盖: 静态/参数/兜底路径、命名路由 `to`、`redirect` 记录、全局 `beforeEach/afterEach`、
路由级 `beforeEnter`(未登录重定向回首页、登录后放行)、`keepAlive` 页面回来不重跑、
`useRoute()` 在页面里读自己的路径。

排障(症状 → 原因, 完整表见 [gui-router.md §11](gui-router.md)):

| 症状 | 十有八九是 |
|---|---|
| 页面一片空白, 控制台什么都没有 | 页面组件抛错了, 或记录的 `component` 不是函数 |
| 懒加载页第一次进去空、第二次正常 | 没包 `lazy()` ⇒ `beforeRouteEnter` 没生效 |
| 顶部路由永远停在初始值 | 在窗口根上读了 `currentRoute()` 却期望"本窗口" ⇒ 改用页面里的 `useRoute()` |
| 页面里改个 signal 就整页重建 | 页面体里直接读了 signal(`const n = count()`) ⇒ 放进函数 `{() => count()}` 或用 `untrack` |
| 回来时草稿/滚动没了 | 没开 `keepAlive`, 状态也没进 `useRouteState()` |
| `Alt+←` 被应用自己的快捷键抢了 | 本模块**包装**根节点 `onKeyDown`(两层都跑); 要独占就 `createRouter({ routes, backKeys: false })` |

---

## 5. 示例与回归清单

| 文件 | 内容 | 回归用例 |
|---|---|---|
| [`testdata/tutorial_api.js`](../testdata/tutorial_api.js) | §1 同步/异步/回调/定时器/HTTP | `TestTutorialHeadlessScripts` |
| [`testdata/http_demo.js`](../testdata/http_demo.js) | §1.5 展开: `http.createServer` 最小 REST 服务 + 两种客户端风格全链路 | `TestHTTPDemoScript` |
| [`testdata/tutorial_modules.js`](../testdata/tutorial_modules.js) + [`tutorial_util.js`](../testdata/tutorial_util.js) | §2 四种导入形态 + 各模块导出 + gx/solid + gx/storage | `TestTutorialHeadlessScripts` |
| [`testdata/tutorial_gui.js`](../testdata/tutorial_gui.js) | §1 GUI 骨架: render + signal + model + each + show | `TestTutorialGuiScript` |
| [`testdata/tutorial_router.js`](../testdata/tutorial_router.js) | §4 路由表 / 守卫 / keepAlive / 重定向 / 兜底 | `TestTutorialRouterScript` |

```bash
go test ./gfx -run TestTutorial -v      # 四个示例脚本全部跑一遍
```

其它可运行示例见 [gui-guide.md §11 示例索引](gui-guide.md)(`counter_demo.js` / `form_demo.js` /
`menu_demo.js` / `multiwindow_demo.js` / `router_demo.js` 等 35+ 个)。
