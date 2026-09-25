# Gox

> 用 Go 从零实现的 JavaScript 运行时：词法分析 → 语法分析 → 字节码编译 → 栈式虚拟机执行。
> 单二进制、零 cgo、零外部运行时依赖，自带软件光栅化 GUI 与脚本打包器。

[![Go](https://img.shields.io/badge/Go-1.26.2%2B-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)
[![npm](https://img.shields.io/npm/v/@goxjs/goxjs)](https://www.npmjs.com/package/@goxjs/goxjs)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20Linux%20%7C%20macOS-lightgrey)](#环境要求)
[![Release](https://github.com/14752222/Gox/actions/workflows/release.yml/badge.svg)](https://github.com/14752222/Gox/actions/workflows/release.yml)

📖 **[官网与使用教程](https://14752222.github.io/Gox/)** ｜ **[GUI 开发指南](docs/gui-guide.md)** ｜ **[npm 包](https://www.npmjs.com/package/@goxjs/goxjs)**

## 目录

- [项目简介](#项目简介)
- [功能特性](#功能特性)
- [快速开始](#快速开始)
- [使用方法](#使用方法)
- [配置说明](#配置说明)
- [技术架构](#技术架构)
- [开发与测试](#开发与测试)
- [发版流程](#发版流程)
- [贡献指南](#贡献指南)
- [相关文档](#相关文档)
- [许可证](#许可证)

## 项目简介

Gox 是一个用**纯 Go** 实现的 JavaScript（ES6+ 子集）运行时。整条编译管线 —— 词法分析、语法分析、
字节码生成、栈式虚拟机 —— 都从零实现，不依赖 V8/QuickJS 等任何现成引擎，也不依赖 cgo。

在此之上，Gox 补齐了脚本语言通常缺失的那一层：一套**自研的桌面 GUI 渲染层**（纯 Go 软件光栅化，
flex 布局 + JSX + 信号驱动更新，win32 / X11 / cocoa 窗口后端）、一组宿主能力模块（文件、HTTP、剪贴板、
原生对话框、持久化存储），以及**把脚本打包成独立可执行文件**的工具链。

它的定位是"小而完整"：一个 `go build` 得到一个可执行文件，可以把 JS 当脚本层嵌进 Go 程序，
也可以直接用它写带界面的小工具并打包分发。

**设计取向**

- **单二进制分发** —— 静态编译、无动态库依赖；跨平台只需换 `GOOS`/`GOARCH`，无需目标机工具链。
- **零 cgo** —— 后端直连系统 API（Windows 走 syscall、Linux 走 X 协议），代价是不支持 `-race` 检测。
- **GUI 不是外挂** —— 渲染、布局、命中测试、脏矩形重绘全在运行时内部，脚本侧只有 JSX 与信号。
- **确定性单线程** —— VM 单线程执行，网络 I/O 在 goroutine 中完成后投递回主线程，回调无需加锁。

**适用场景**：用 JS 写跨平台桌面小工具；把 JS 作为配置与插件层嵌入 Go 应用；学习编译器与虚拟机实现。

## 功能特性

- **完整编译管线** —— 自研 lexer / parser / compiler / bytecode VM，109 个操作码，定长 3 字节指令编码
  （`[操作码 1B][操作数 2B 大端]`），解码即取即用
- **ES6+ 语言子集** —— `let`/`const`（不支持 `var`）、函数与箭头函数、闭包、`class`、`async`/`await`、
  解构赋值、剩余/默认参数、展开、模板字符串、`for...of`、`try`/`catch`/`throw`、可选链 `?.`、
  空值合并 `??`、ES 模块 `import`/`export`，以及 **JSX 语法**（编译期降级为 `h(tag, props, ...children)` 调用）
- **丰富的内置对象** —— `Array` / `String` / `Number` / `Object` / `Boolean` / `Math` / `JSON` /
  `Map` / `Set` / `WeakMap` / `WeakSet` / `Symbol` / `BigInt` / `RegExp` / `Proxy` / `Reflect` /
  `Iterator` / `Promise` / `ArrayBuffer` / `DataView`（TypedArray 家族）/ `WeakRef` /
  `FinalizationRegistry` / 完整错误类型族 / `Temporal`（现代日期时间 API，本运行时不含 `Date`）
- **宿主能力模块** —— 以全局对象注入：`fs`（Node 风格，同步 + 异步两套）、`path`、`http`
  （客户端 `get`/`request` + 服务端 `createServer`）、`fetch`、`process`、`stats`
- **自研 GUI 渲染层** —— `gx/gfx` 模块：flex 风格布局（`column`/`row`/`grid`、百分比尺寸、min-max 钳位、
  `flexShrink`、折行）、圆角/线性渐变/阴影装饰、命中测试、脏矩形局部重绘；win32（纯 syscall）、X11 与 macOS（cocoa, purego）
  窗口后端。详见 **[GUI 开发指南](docs/gui-guide.md)**
- **原生感的交互组件** —— 表单控件（`input`/`textarea`/`select`/`slider`/`checkbox`/`radio`/`switch`）、
  弹层（`dialog`/`toast`）、滚动容器、自绘画布，以及**自绘菜单栏与右键菜单**（下拉/子菜单/快捷键/禁用项，
  不依赖系统菜单 API）
- **原生能力层** —— 六个模块共用一个宿主契约（`NativeHost`）：`gx/device`（设备信息 / 电量 / 网络 /
  震动 / 屏幕亮度）、`gx/app`（前后台 / 返回键 / 分享）、`gx/geo`（定位，含持续监听）、
  `gx/media`（拍照 / 选图 / 选视频 / 保存）、`gx/permission`（权限查询与申请）、`gx/viewport`
  （安全区 / 软键盘高度 / 分屏与多窗口形态）。桌面由 win32 宿主直接实现，移动端原生壳只需实现
  同一个契约即可接入；**缺能力时明说缺**（`canIUse` + 8 个统一错误码），不返回假数据
- **事件循环** —— `setTimeout` / `setInterval` / `requestIdleCallback`，以及精度可控的严格定时器变体
  （`setStrictTimeout` 等）；GUI 模式下事件循环接入窗口消息泵
- **响应式编程** —— Dart GetX 风格的 `obs` / `computed` / `ever` / `once`，以及 SolidJS 风格的
  `gx/solid` 信号（`createSignal` / `createEffect` / `createMemo` / `createResource` / `onMount` /
  `onCleanup`）；声明式界面是**元素级指令**：`model` 双向绑定、`each` 列表（keyed 复用）、
  `show` 条件保活显隐，多分支用 `gx/view` 的 `Switch` + `Match`
- **npm 分发** —— [`@goxjs/goxjs`](https://www.npmjs.com/package/@goxjs/goxjs) 内置
  macOS x64/arm64、Linux x64/arm64、Windows x64 五个平台的预编译二进制，
  `npm i -g @goxjs/goxjs` 即得 `goxjs` 命令
- **工具链** —— 交互式 REPL、jsbuild 打包器（JS → 独立可执行文件，支持 GUI 应用与纯 Go 交叉编译）、
  `gx/dev` 运行时快照（帧统计 / 缓存命中 / 最近警告，可做调试面板）

## 快速开始

### 环境要求

| 场景 | 要求 |
|---|---|
| 从源码构建 | Go 1.26.2 或更高（零 cgo，无需 C 工具链） |
| 仅运行脚本 | 无 —— npm 包已内置各平台预编译二进制，只需 Node.js ≥ 14 |
| GUI 应用 | Windows（win32）、Linux（X11）或 macOS（cocoa） |

### 从源码构建

> `npm/` · `website/` · `gox-logo-concepts/` 是三个**独立仓库**，以 git submodule 挂在
> 本仓库里。克隆必须带 `--recurse-submodules`；已有克隆补 `git submodule update --init`。
> 否则这三个目录是空的，`scripts/check-registries.py` 会因为读不到 `npm/package.json` 而失败。

```bash
git clone --recurse-submodules https://github.com/14752222/Gox.git
cd Gox
go build          # 生成可执行文件；Windows 下为 Gox.exe
```

### 通过 npm 安装

```bash
npm i -g @goxjs/goxjs    # 安装后可直接使用 goxjs 命令
npx goxjs app.js         # 或不安装，直接运行
```

### 交互式 REPL

```bash
./Gox
```

```
Gox REPL (ES6 subset, no var)
Type :exit to quit, :help for help

> let x = 10
> let y = 20
> x + y
  30
> [1, 2, 3].map(v => v * 2)
  [2, 4, 6]
```

REPL 命令：`:help` 查看帮助、`:clear` 重置环境、`:exit` 退出。

### 运行脚本

```bash
./Gox example.js    # npm 安装的 goxjs 命令用法相同
```

脚本执行完毕后会回显最后一个顶层表达式的值（`undefined` 除外），并等待定时器与异步回调
全部执行完再退出。

## 使用方法

### 语言示例

以下示例均实际运行验证过。

**基础语法：**

```js
let x = 10
let y = 20
console.log(x + y)                                                 // 30
console.log([1, 2, 3].map(v => v * 2))                             // [2, 4, 6]
console.log([1, 2, 3].filter(v => v > 1).reduce((a, b) => a + b))  // 5
console.log(Math.hypot(3, 4))                                      // 5

let name = "Gox"
console.log(`Hello, ${name}!`)                                     // Hello, Gox!

let [a, b] = [1, 2]                                                // 解构赋值
console.log(a + b)                                                 // 3
```

**class 与异步：**

```js
class Point {
  constructor(x, y) { this.x = x; this.y = y }
  norm() { return Math.hypot(this.x, this.y) }
}
console.log(new Point(3, 4).norm())       // 5

async function main() {
  let v = await delay(10, "timer done")   // delay(ms, value) 返回 Promise
  console.log(v)
}
main()
```

```
5
Promise { <pending> }     ← 顶层回显：main() 调用的返回值
timer done                ← 定时器到期后，await 继续执行
```

> 语法提示：异步函数写 `async function` 或 async 箭头 `async () => {}` 都行；顶层不能 `await`。

**ES 模块** —— `lib.js`：

```js
export const PI = 3.14
export function double(x) { return x * 2 }
```

`main.js`：

```js
import { PI, double } from "./lib.js"
setTimeout(() => console.log("tick"), 10)
console.log(double(PI))
```

```
6.28
tick
```

**响应式（GetX 风格）：**

```js
let count = obs(0)
ever(count, v => console.log("count =", v))   // 订阅时立即以当前值回调一次
count.value = 1
count.value = 2
count.value = 2   // 值未变化，不触发通知
```

```
count = 0
count = 1
count = 2
2                  ← 顶层回显：最后一条赋值表达式的值
```

### 宿主能力模块

`fs` / `path` / `http` / `fetch` / `process` / `stats` 以**全局对象**形式注入，直接使用，无需 `import`：

```js
const text = fs.readFileSync("a.txt")          // 同步：失败抛 JS 异常
const data = await fs.readFile("a.txt")        // 异步：无回调时返回 Promise
fs.mkdirSync("a/b", { recursive: true })

const server = http.createServer(function (req, res) {
  res.writeHead(200, { "Content-Type": "text/plain" })
  res.end("hello")
})
server.listen(0, function () { console.log(server.port) })

const res = await fetch("https://example.com")
console.log(res.status, await res.text())
```

异步 I/O 的实现模型是**跨 goroutine I/O + 投递回 VM 单线程**：网络读写不触碰 VM 状态，结果经
零延时定时器回到事件循环执行 JS 回调，因此回调里访问 VM 无需加锁。

一份完整的 HTTP 示例（起服务于端口 0、路由/查询参数/JSON 请求体/404-405-500 各状态码、
回调式 `http.get`·`http.request` 与 Promise 式 `fetch` 对照、异步响应与 `server.close` 收尾）：
[`testdata/http_demo.js`](testdata/http_demo.js) —— `gox testdata/http_demo.js` 即可运行。

### 原生能力模块 `gx/*`

`gx/device` / `gx/app` / `gx/geo` / `gx/media` / `gx/permission` / `gx/viewport` 需要 `import`
（不是全局对象）。三种调用形态要在写代码时区分开：

```js
import { deviceInfo, battery, isOnline, canIUse } from "gx/device";   // 拉取型 + 上报型
import { getLocation, watchLocation } from "gx/geo";
import { takePhoto } from "gx/media";                                  // 动作型

const info = deviceInfo();                    // 同步可得
console.log(info.platform, info.model, battery().level + "%", isOnline());
// battery()/isOnline() 读的是快照；useBattery() 返回取值函数，宿主上报时自动刷新

if (canIUse("camera")) {                      // 事前判断能力，不要靠 catch 兜底
  const photo = await takePhoto({ count: 1 });      // 失败会 reject，带 errCode
}

const stop = watchLocation((loc) => console.log(loc.latitude, loc.longitude));
try {
  await getLocation({ highAccuracy: true });
} catch (e) {
  if (e.errCode === "permission-denied") console.warn(e.message);
}
```

桌面后端直接实现其中能实现的部分（电量 / 网络 / 亮度 / 屏幕常亮 / 打开系统设置页），
给不出的一律诚实报 `unsupported`；**移动端原生壳实现同一个 `NativeHost` 契约即可接入**，
不必改内核。语义、8 个错误码与各模块导出表见
[GUI 开发指南 §9.6](docs/gui-guide.md#96-原生能力层)；三种形态的完整演示：
[`testdata/native_demo.js`](testdata/native_demo.js)。

### GUI 桌面应用

`gx/gfx` + `gx/solid` 提供 JSX 声明式 UI 与信号驱动的响应式更新，渲染器为纯 Go 软件光栅化
（无 cgo、无动态库依赖）。

用脚手架起一个新工程 —— 生成出来的默认工程开箱即跑，目录布局就是脚手架的默认输出：

```bash
gox create my-app                          # 同为 goxjs create / npx @goxjs/goxjs create
cd my-app && npm install && npm run dev
```

生成 `package.json`、入口 `src/main.js`、根组件 `src/app.js`、共享状态 `src/store.js`、
设计令牌 `src/theme.js` 与 `src/components/` 下三个示例组件（计数器 / 待办列表 / 多状态），
把信号、元素级指令（`each` / `show`）、`model` 双向绑定与 `Switch` / `Match` 各演示一遍。
模板是 [`scaffold/template/`](scaffold/template/) 下的**真实文件**，由 `go:embed` 嵌进二进制
（改模板要重新编译才生效；`scaffold/scaffold_test.go` 会保证生成出来的 JS 仍能过
lexer → parser → compiler，`gfx/scaffold_project_test.go` 会真的把它挂载起来点一遍）。

最省事的最小手写版：

```js
import { h, render, createSignal } from "gox"        // 聚合入口：所有 gx/* 导出的并集
// 等价的细分写法：import { h, render } from "gx/gfx"; import { createSignal } from "gx/solid"

const [count, setCount] = createSignal(0)

render(
  <window title="Counter" width={400} height={300}>
    <column gap={8} padding={16}>
      <text font={20}>{() => `count: ${count()}`}</text>
      <button onClick={() => setCount(c => c + 1)}>加一</button>
    </column>
  </window>
)
```

```bash
./Gox counter.js          # 直接运行，弹出 400x300 窗口
```

点击按钮 → 信号更新 → 依赖它的属性/文本节点标脏 → 脏矩形合并后只重绘受影响区域。`render()`
返回窗口句柄，可调用多次开多窗口（各有独立元素树与焦点，全关才退出进程）。

完整参考 —— 事件模型、内置元素属性表、弹性布局与装饰绘制、受控组件与输入法、过渡动画、
元素级指令（`model` / `each` / `show`）与 `gx/view` 多分支、**路由（`gx/router`：路由表 / 参数匹配 /
三级守卫 / 懒加载 / 历史栈 / 多窗口作用域 / 折叠屏双栏）与屏幕信息（`gx/screen`）**、
原生对话框 / 剪贴板 / 持久化存储 / 菜单栏、多窗口语义与平台差异 ——
见 **[docs/gui-guide.md](docs/gui-guide.md)**，可直接运行的示例见
[`testdata/`](testdata/)（`counter_demo.js`、`form_demo.js`、`multiwindow_demo.js`、
`menu_demo.js`、`view_demo.js`、`router_demo.js`、`router_window_demo.js` 等 35+ 个）。

路由与屏幕适配另有一份专门手册：**[docs/gui-router.md](docs/gui-router.md)**。

### 打包为独立可执行文件

`jsbuild`（`packager/`）把入口脚本及其相对 `import` 的模块嵌入一个生成的 Go 工程，编译成单文件
可执行程序，自带完整运行时：

```bash
go run ./packager app.js -o app.exe                    # CLI 应用
go run ./packager counter.js --gui -o counter.exe      # GUI 应用
go run ./packager app.js --gui --target linux/amd64    # 纯 Go 交叉编译，无需目标机工具链
```

```
Usage: jsbuild <input.js> [options]

Options:
  -o, --out <path>     输出文件路径 (默认: <输入文件名>.exe)
      --name <name>    应用名 (用于错误信息显示, 默认取输入文件名)
      --windowed       窗口模式: 不显示控制台窗口 (仅 Windows)
      --gui            GUI 应用: 窗口消息泵事件循环 (配合 gx/gfx render)
      --target <os>/<arch>  交叉编译目标 (windows|linux|darwin / amd64|arm64|386)
      --icon <path>    应用图标 (.png 1024 源图或现成 .ico/.icns):
                       Windows 内嵌图标+版本资源 (.syso), darwin 产出 .app bundle
      --version <v>    版本号 (x.y.z, 写进 Windows 版本资源与 .app Info.plist)
  -v, --verbose        显示构建过程输出
```

各平台的分发注意事项（Windows 图标与签名、Linux 打包格式、macOS `.app` bundle）见
[docs/desktop-distribution.md](docs/desktop-distribution.md)。

### 多平台配置（gox.json / 权限 / 图标）

`gox create` 生成的工程自带 `gox.json`（应用名、appId、版本、权限、各平台子配置）
与完整的 Android / iOS / 桌面资源骨架 + 默认图标：

- `gox sync` —— 把 gox.json 里的 `permissions` 幂等注入 AndroidManifest 与
  iOS Info.plist（默认最小权限, 仅 INTERNET; 未声明的不写入）;
- `gox icon` —— 以 `assets/icon.png`（1024×1024）为单源生成 Android mipmap、
  iOS AppIconSet、Windows `.ico`、macOS `.icns`、favicon;
- `gox build windows|macos|android|ios` —— 统一构建入口（sync → icon → 平台打包）。

字段说明、权限清单与图标规范见 [docs/platform-config.md](docs/platform-config.md)。

## 配置说明

Gox 运行时本身不使用配置文件，全部行为由**命令行参数**、**少量环境变量**与**脚本内 API** 控制。
工程级配置（应用名、appId、版本、权限、图标）由 `gox create` 生成的 `gox.json` 承担，
见下文「多平台配置」一节。

### 命令行参数

| 命令 | 参数 | 说明 |
|---|---|---|
| `Gox` / `goxjs` | 无 | 启动交互式 REPL |
| `Gox` / `goxjs` | `create <目录>` | 按默认模板生成一个 GUI 工程；别名 `new` / `init` |
| `Gox` / `goxjs` | `sync [目录]` | 把 gox.json 的权限声明幂等注入 Android/iOS 清单 |
| `Gox` / `goxjs` | `icon [目录]` | 从 1024 源图一键生成全平台图标 |
| `Gox` / `goxjs` | `build <android\|ios\|windows\|macos>` | 统一构建入口（sync → icon → 平台打包） |
| `Gox` / `goxjs` | `<script.js>` | 执行脚本文件；报错写 stderr 并以非零码退出 |
| `Gox` / `goxjs` | `help` / `version` | 显示用法 / 版本号 |
| `go run ./packager` | 见 [打包](#打包为独立可执行文件) | jsbuild 打包器参数表 |

`create` 的选项：`--name <名字>`（覆盖项目名，缺省取目录名）、`-f` / `--force`
（目标目录已存在且非空时才需要，缺省拒绝覆盖）。参数只在本参数**不像脚本路径**时
才当子命令认（判据是扩展名与路径分隔符），所以 `gox help.js`、`gox src/create.js`
仍然老老实实当脚本执行。

REPL 内建命令：`:help`（帮助）、`:clear`（重置全局环境）、`:exit` / `:quit`（退出）。

### 环境变量

| 变量 | 作用 | 缺省 |
|---|---|---|
| `GOX_STORAGE_DIR` | 覆盖 `gx/storage` 的数据根目录，测试与多实例隔离常用 | 空（走系统配置目录） |
| `GOOS` / `GOARCH` / `CGO_ENABLED` | 仅在构建期影响交叉编译（见 `scripts/build-npm.sh`） | 宿主平台 |

### 应用级配置

| 配置项 | 设置方式 | 说明 |
|---|---|---|
| 应用名 / 数据目录 | `setAppName("MyApp")`（`gx/storage`） | 数据落在 `os.UserConfigDir()/Gox/<应用名>` —— Windows `%AppData%`、Linux `~/.config`、macOS `~/Library/Application Support`；未设置时从脚本文件名推导 |
| 窗口标题与尺寸 | JSX `<window title width height>` 或 `render(tree, {title, width, height})` | 缺省 `Gox` 400×300 |
| 字体 | 平台自动探测 | Linux 惰性扫描系统字体目录（`/usr/share/fonts`、`~/.local/share/fonts` 等），CJK 字体优先，条目上限 2000 |
| 主题 / 颜色 | 元素属性（`background` / `border` / `color`） | 支持命名色与 `#rgb` / `#rgba` / `#rrggbb` / `#rrggbbaa` / `rgb()` / `rgba()` |

### npm 包信息

| 项 | 值 |
|---|---|
| 包名 / 命令 | `@goxjs/goxjs` → `goxjs` |
| 内含平台 | macOS x64 / arm64、Linux x64 / arm64、Windows x64 |
| Node 版本 | `>=14`（仅用于分发二进制，运行期不依赖 Node） |
| 版本真源 | [`npm/package.json`](npm/package.json) 的 `version` 字段 |

## 技术架构

### 编译与执行管线

```
 .js 源码
    │  lexer        词法分析
    ▼
 Token 流
    │  parser       语法分析
    ▼
 AST (ast/)
    │  compiler     符号表解析 + 字节码生成 (常量池)
    ▼
 字节码 (bytecode/)   定长 3 字节: [opcode 1B][operand 2B 大端]
    │  vm           栈式调用帧执行 + 定时器事件循环 + 模块加载
    ▼
 结果
```

### 关键设计决策

- **无 GC 的显式内存管理** —— 对象生命周期由运行时显式控制，循环引用有专门处理。
- **单线程 VM + 跨 goroutine 调度** —— `http.createServer` 等网络回调跨 goroutine 捕获后调度回
  VM 单线程执行，无需锁。挂起任务保活事件循环，计数归零才允许进程退出。
- **定长指令编码** —— 无需变长解码状态机，直取操作数；常量池承担大对象引用。
- **可选能力走可选接口** —— GUI 后端通过 `Surface` + `WindowFactory` 抽象，换后端零改动脚本；
  平台独有能力（IME、剪贴板、原生对话框）按后端探测，缺失时静默降级而非报错。

### 目录结构

| 目录 | 职责 |
|---|---|
| `lexer/` | 词法分析器（含 JSX 词法支持） |
| `parser/` | 语法分析器，生成 AST（含 JSX 语法降级） |
| `ast/` | AST 节点定义 |
| `compiler/` | AST → 字节码编译器（含符号表） |
| `bytecode/` | 操作码与字节码格式定义 |
| `vm/` | 栈式字节码虚拟机（调用帧、模块加载、定时器调度） |
| `object/` | 运行时对象系统（Number / Array / Map / Promise / Observable…） |
| `runtime/` | 全局环境 Environment |
| `stdlib/` | 标准库与宿主 API 实现（含 `gx/solid` 响应式信号） |
| `gfx/` | 自研 GUI 渲染层（软件光栅化、布局、命中测试、win32 / X11 / cocoa 后端） |
| `packager/` | jsbuild 打包器（GUI 应用、交叉编译） |
| `scaffold/` | `gox create` 的项目脚手架：`template/` 是**真实文件**（`go:embed` 进二进制），`scaffold.go` 负责占位符替换与目录校验 |
| `test/` | 测试相关：`bench/` 性能剖析基准（fib、函数调用、对象操作、数值解析） |
| `testdata/` | 可直接运行的示例脚本（语言特性、宿主 API、GUI 示例） |
| `docs/` | **对外文档**（GUI 指南、运行时 API 教程、分发与发版手册）——过程性材料在 `agent_doc/`（不随仓库发布） |
| `npm/` | **子模块** → [gox-npm](https://github.com/14752222/gox-npm)：npm 包 `@goxjs/goxjs` 的**定义**（`package.json` / `bin/gox.js` / 包说明），二进制由发版流水线现场编译，不进仓库 |
| `scripts/` | 构建与检查脚本：`build-npm.sh` 交叉编译五个平台的二进制；`check-site.py` 官网静态检查；`check-registries.py` 注册表一致性（内置组件四处 / gx 模块三处 / 版本号 / npm 清单），配 `check-registries-selftest.py` 做负向自测 |
| `.github/workflows/` | CI：`ci.yml` 常规闸门（注册表一致性 + `go build`/`vet`/`test`）；`release.yml` 发版流水线（push main / tag / Release → npm）；`pages.yml` 官网发布 |
| `website/` | **子模块** → [gox-website](https://github.com/14752222/gox-website)：官网源码，推 main 后由 `pages.yml` 发布到 GitHub Pages |
| `gox-logo-concepts/` | **子模块** → [gox-logo-concepts](https://github.com/14752222/gox-logo-concepts)：logo 概念稿与官网资产生成脚本（`make_assets.py`） |

## 开发与测试

```bash
go test ./...          # 运行全部测试 (bytecode / compiler / lexer / object / parser / runtime / vm)
go run ./test/bench    # 生成 cpu.prof 性能剖析
```

> 由于零 cgo，`go test -race` 在本项目不可用（Go 会要求 cgo 支持）。

深入参与开发（新增标准库 API、理解回调桥与内存管理）请阅读
[docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md)。

## 发版流程

`@goxjs/goxjs` 由 GitHub Actions 自动发布（[.github/workflows/release.yml](.github/workflows/release.yml)），
认证走 npm Trusted Publishing (OIDC)，仓库里不需要任何 secret / token：

```bash
# 1. 改版本号 —— npm/package.json 的 version 是唯一真源，而 npm/ 是独立仓库：
$EDITOR npm/package.json
git -C npm commit -am "chore(release): 0.2.1" && git -C npm push
# 2. 回主仓库提交子模块指针并推送（这一推才触发发版）
git add npm && git commit -m "chore(release): 0.2.1" && git push origin main
# 3. 剩下交给 CI：交叉编译五平台二进制 → 冒烟测试 → 打包校验 → npm publish --provenance
#    发布成功后自动打 v0.2.1 标签并建 Release
```

- **触发**：push 到 `main`、推送 `v*` 标签、发布 Release，以及手动 `workflow_dispatch`
  （可勾 `dry_run` 只构建打包、不上传）
- **幂等**：CI 先查 registry，该版本已存在则跳过并留一条 notice —— 重复推送、重跑历史工作流
  都不会报红，也不会重复发布
- **版本号**：日常提交不会触发真发布，只有 registry 上还没有的版本才会被发出去；打标签时标签号
  必须与 `package.json` 一致，否则直接失败
- **本地手工发版**（应急用，需要 OTP）：
  `bash scripts/build-npm.sh && cd npm && npm publish --access public`
- **排障**：OIDC 失败与"Trusted Publisher 没配对"返回的是同一个误导性 404，先核对 npm 侧那四个
  字段（见 workflow 头部注释）；每次发布的日志里都会打印 npm 版本、`NODE_AUTH_TOKEN` 是否为空、
  OIDC 端点是否可用

## 贡献指南

欢迎提交 Issue 与 Pull Request。

**开发环境**：Go 1.26.2+，零 cgo（无需 C 工具链）。提交前请确保：

```bash
gofmt -l .             # 应无输出
go build ./...
go test ./...
```

**提交规范**：沿用仓库现有的 Conventional Commits 风格，scope 用受影响的模块或主题，
中文描述：

```
feat(gfx): 新增 <slider> 的键盘调节支持
fix(vm): 修正 SET_INDEX 对非对象类型静默丢弃的问题
docs(gui-guide): 补充多窗口事件泵的轮询语义
ci(release): 打包闸改回 tar 校验，不再解析 npm 的输出
```

**新增内置 GUI 组件时须同步四处**（漏改会静默失效，不出编译错误）：

| 位置 | 作用 |
|---|---|
| `gfx/node.go` 的 `knownTags` | 注册标签名，否则 `h()` 会打印"未知标签"警告 |
| `gfx/layout.go` 的 `intrinsicSize` | 声明固有尺寸 |
| `gfx/layout.go` 的布局分派 | 声明子节点如何参与布局 |
| `gfx/raster.go` 的 `drawNode` | 实现绘制；被 `case` 截走的标签需自行补画 background / border |

**其他约定**：

- 事件回调统一经 `callHandler` 分发；涉及窗口的操作从节点出发经 `appOfNode(n)` 取所属窗口（支持多窗口）
- `gx/*` 的 JSX 属性与子节点在调用当场求值一次 —— 需要响应式就必须传**函数**（`value: () => sig()`），
  传值只是一张快照
- 新增标准库 API 请同步更新 [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md)；
  未决与未实现项记入 `agent_doc/undecided-and-unimplemented.md`（过程性台账，不随仓库发布）
- 涉及 GUI 组件的改动，请在 `agent_doc/gui-component-status.md` 追加一条落地记录（同上）
- 示例脚本放在 `testdata/` 并确保可直接运行

## 相关文档

> 下表只列 `docs/` 里**已定稿、对外发布**的文档。技术选型、开发计划、现状台账等
> **未定稿的过程性材料**统一放在 `agent_doc/`，已被 `.gitignore` 排除，不会进远端。

| 文档 | 内容 |
|---|---|
| [docs/tutorial.md](docs/tutorial.md) | **实战教程**：API 调用与参数、内置模块导入、`gox create` 建工程、路由定义与注册（配可直接运行的示例脚本） |
| [docs/gui-guide.md](docs/gui-guide.md) | GUI 开发指南：元素/事件参考、布局、动画、宿主能力、示例索引 |
| [docs/platform-config.md](docs/platform-config.md) | 多平台配置：gox.json 字段说明、权限清单表、图标规范、`gox build` 各目标与真机验收步骤 |
| [docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md) | 运行时 API 教程：函数类型、回调桥、内存管理、新增 API 的完整流程 |
| [docs/gui-patterns.md](docs/gui-patterns.md) | 用户态模式手册（路由、状态、主题等惯用法） |
| [docs/gui-model-binding.md](docs/gui-model-binding.md) | `model` 双向绑定：接口设计、语义表、与 Vue 的对照、反例 |
| [docs/npm-release.md](docs/npm-release.md) | `@goxjs/goxjs` 发版手册：版本号策略、构建步骤、OIDC 配置要求、验收口径 |
| [docs/desktop-distribution.md](docs/desktop-distribution.md) | 桌面应用分发：图标、签名、各平台打包格式 |
| [官网](https://14752222.github.io/Gox/) | 安装、运行脚本、写 GUI 应用与打包的在线教程 |

## 许可证

[Apache License 2.0](LICENSE)
