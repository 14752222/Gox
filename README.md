# Gox

> 用 Go 从零实现的 JavaScript 运行时：词法分析 → 语法分析 → 字节码编译 → 栈式虚拟机执行。
> 单二进制、零外部依赖，还能把 JS 脚本打包成独立可执行文件。

## 特性一览

- **完整编译管线** — 自研 lexer / parser / compiler / bytecode VM，108 个操作码，定长 3 字节指令编码（`[操作码 1B][操作数 2B 大端]`），解码即取即用
- **ES6+ 语言子集** — `let`/`const`（不支持 `var`）、函数与箭头函数、闭包、`class`、`async`/`await`、解构赋值、剩余/默认参数、展开、模板字符串、`for...of`、`try`/`catch`/`throw`、可选链 `?.`、空值合并 `??`、ES 模块 `import`/`export`
- **丰富的内置对象** — `Array` / `String` / `Number` / `Object` / `Boolean` / `Math` / `JSON` / `Map` / `Set` / `WeakMap` / `WeakSet` / `Symbol` / `BigInt` / `RegExp` / `Proxy` / `Reflect` / `Iterator` / `Promise` / `ArrayBuffer` / `DataView`（TypedArray 家族）/ `WeakRef` / `FinalizationRegistry` / 完整错误类型族 / `Temporal`（取代 `Date` 的现代日期时间 API）
- **宿主能力模块** — `fs`（Node 风格，同步 + 异步两套）、`http`（客户端 `get`/`request` + 服务端 `createServer`）、`fetch`、`path`、`process`、`stats`
- **事件循环** — `setTimeout` / `setInterval` / `requestIdleCallback`，以及精度可控的严格定时器变体（`setStrictTimeout` 等）
- **响应式编程** — Dart GetX 风格的 `obs` / `computed` / `ever` / `once`
- **工具链** — 交互式 REPL、jsbuild 打包器（JS → 独立 .exe）、dbgtool 词法调试器

## 快速开始

环境要求：Go 1.26+

```bash
git clone https://github.com/14752222/Gox.git
cd Gox
go build          # Windows 下生成 Gox.exe
```

**REPL：**

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

**运行脚本：**

```bash
./Gox example.js
```

脚本执行完毕后会回显最后一个顶层表达式的值（`undefined` 除外），并等待定时器与异步回调全部执行完再退出。

## 语言示例

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

**ES 模块** — `lib.js`：

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

## 打包成独立可执行文件

`jsbuild`（packager 目录）把入口脚本及其相对 import 的模块嵌入一个生成的 Go 工程，编译成单文件 .exe，自带完整运行时：

```bash
go run ./packager app.js -o app.exe
```

```
Usage: jsbuild <input.js> [options]

Options:
  -o, --out <path>     输出文件路径 (默认: <输入文件名>.exe)
      --name <name>    应用名 (用于错误信息显示, 默认取输入文件名)
      --windowed       窗口模式: 不显示控制台窗口 (仅 Windows)
  -v, --verbose        显示构建过程输出
```

## 架构

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

- **无 GC 的显式内存管理** — 对象生命周期由运行时显式控制，循环引用有专门处理
- **单线程 VM + 跨 goroutine 调度** — `http.createServer` 等网络回调跨 goroutine 捕获后调度回 VM 单线程执行，无需锁

### 目录结构

| 目录 | 职责 |
|---|---|
| `lexer/` | 词法分析器 |
| `parser/` | 语法分析器，生成 AST |
| `ast/` | AST 节点定义 |
| `compiler/` | AST → 字节码编译器（含符号表） |
| `bytecode/` | 操作码与字节码格式定义 |
| `vm/` | 栈式字节码虚拟机（调用帧、模块加载、定时器调度） |
| `object/` | 运行时对象系统（Number/Array/Map/Promise/Observable...） |
| `runtime/` | 全局环境 Environment |
| `stdlib/` | 标准库与宿主 API 实现 |
| `packager/` | jsbuild 打包器 |
| `dbgtool/` | 词法分析调试工具（打印 Token 流） |
| `docs/` | 文档 |
| `test/` | 测试相关：`bench/` 性能剖析基准（fib、函数调用、对象操作、数值解析），`testdata/` 示例与测试脚本 |

## 开发

```bash
go test ./...     # 运行全部测试 (bytecode/compiler/lexer/object/parser/runtime/vm)
go run ./test/bench   # 生成 cpu.prof 性能剖析
go run ./dbgtool     # 查看词法分析的 Token 流
```

深入参与开发（新增标准库 API、理解回调桥与内存管理）请阅读
[docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md)。

## 许可证

[Apache License 2.0](LICENSE)
