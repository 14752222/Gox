# TypeScript / TSX 一等公民

> 里程碑 M2。本文是 **用户可用行为的真源**：支持什么、边界在哪、为什么这么选、
> 如何自证。实现细节分散在 `tstransform/`、`vm/srcframe_units.go`、`packager/`、
> `cmd/gox/cmd_types.go`、`cmd/gox/cmd_dev.go`，注释里逐条写了"为什么 / 坑 / v1 边界"。

## 1. 选型：Go 内嵌 esbuild，不是 swc

`.ts` / `.tsx` 在**入库前**由 `tstransform` 剥掉类型，落到 parser/compiler 的永远
是 JS（TSX 的 JSX 原样保留，交给引擎既有的 JSX 降级管线，与 `.js` 模板走同一条
运行时语义）。

转译器是 [esbuild](https://github.com/evanw/esbuild) 的 Go 内嵌 API
（`api.Transform`），路线图定的是它而不是 swc：

- **零 cgo、单二进制分发**：esbuild 的 Go 包纯 Go、进程内运行，编译进 `gox`
  单文件（体积约 +9MB），不需要 node、不需要 `node_modules`、不需要动态库。
  swc 是 Rust 实现，走 napi 绑定意味着 cgo / 预编译 `.so` / 平台矩阵，直接破坏
  "单文件分发"这个核心承诺。
- **覆盖完整 TS 语法**：enum / namespace / 参数属性 / 装饰器都按 esbuild 语义做
  **等价转换**，不用自研剥离器去追语法长尾。
- **类型检查不属于运行时**：esbuild 只剥类型、不做检查。IDE / `tsc` 负责检查；
  运行时只保证语法过闸。

> 注：仓库里没有任何 swc / napi / 第三方 sourcemap 依赖。VLQ 解码是 `tstransform/
> sourcemap.go` 里自己写的（约 30 行），符合"零新第三方依赖"的硬约束。

## 2. 支持矩阵

| 能力 | 状态 | 说明 |
| --- | --- | --- |
| 类型注解 / `interface` / `type` / 泛型 / `as` / `satisfies` / 非空断言 | ✅ | 剥离 |
| `enum`（非导出） / `namespace` | ✅ | esbuild 展开为等价 JS（会**改变行数**，由行映射兜住） |
| TSX（JSX 保留） | ✅ | 与 `.jsx` 同一条通道；小写标签自动补 `import { h } from "gx/gfx"` |
| 跨 `.ts` / `.tsx` 相对 import | ✅ | `.js` 说明符回落 `.ts/.tsx`；无后缀按 `.js→.ts→.tsx`；目录走 `index.*` |
| legacy 装饰器 `@dec` | ✅ | 见 §7 |
| 报错定位到 `.ts` 源码行 | ✅ | 入口 + 被 import 的模块；见 §3 |
| 转译缓存 | ✅ | 见 §4 |
| `gox build` TS 入口 | ✅ | 见 §5 |
| `gox types` 生成 `.d.ts` | ✅ | 见 §6 |
| **`export enum` / `export class`** | ✅ | 见下方边界（再导出是"取值时读取源槽"的等效语义） |
| 组件级 HMR | ⛔（后续里程碑） | `gox dev` 目前是**进程级热重启**，见 §8 |

### 已知边界（诚实清单）

1. ~~**`export enum` / `export class` 不可用**~~ —— **已修复（2026-10）**。
   parser 现已覆盖 `export var/let/const`（含多 declarator / 解构 / 无初值）、
   `export class [extends]`、`export async function` / `export async function*`、
   `export default function/class`（具名名字只在模块内可见）、`export * from`、
   `export * as ns from`、`export { a as b } from` 与空导出 `export {}`
   （见 `parser/parser.go` 的 `parseExportDeclaration`）。esbuild 把 `export enum`
   降级成的 `export var Color; (function(Color){…})` 现在可正常执行，夹具
   `testdata/ts/enum.ts` 已复测通过。再导出会真的转发源模块的导出（不再是过去的
   `ReferenceError: x is not defined`）。
2. **再导出不是**完全**规范的 live binding（诚实标注）**。引擎的模块绑定是**值
   快照**，没有绑定 cell，所以实现的是"取值时读取源模块导出槽"的等效语义：
   `export { a } from "m"` / `export * from "m"` 在**导入方物化命名空间时**才去读
   `m` 的导出槽，`m` 在导出后对同名绑定再赋值能反映到之后物化的命名空间；但**已经
   拷进导入方局部变量的绑定不会再更新**（与直接 `import { a }` 的既有边界一致）。
   `export *` 的同名冲突按规范判为 ambiguous（不导出），不做 first-wins。测试见
   `vm/vm_export_test.go` 的 `TestExportNamedReexportLiveBinding` 与
   `TestExportLocalLiveBinding`。
3. **类型检查不做**。`gox` 只保证语法；错误的类型用法不会被运行时拦住。
4. **用户模块的 `.d.ts` 只是"清单"**（§6），不是可被 tsc 直接吸入的 ambient 声明。
5. **单文件入口的 iOS 壳**是否支持 TS 取决于壳是否链接 `vm` 的 TS 通道；桌面
   (`gox build windows|macos`) 已支持。

## 3. 报错定位到 `.ts` 源码行（P0-1）

### 机制

1. `tstransform.Transform` 用 `Sourcemap: SourceMapExternal` 让 esbuild 额外产出
   sourcemap，`DecodeLineMap` 自写 VLQ 解码成 `LineMap`（`JS 行 → .ts 行列`）。
2. VM 把源码信息按**编译单元**建模为 `srcUnit{file, text(原文), js(转译后), lineMap}`，
   并挂到 VM 上：
   - `mainUnit`：入口 / 当前模块顶层；
   - `units`：`*object.CompiledFunction → srcUnit` 注册表，**跨 VM 共享**
     （模块 A 导出的函数被入口调用时，抛错帧属于 A 的单元，而不是入口 VM）。
     注册发生在 `createClosure`。
3. 运行时错误渲染帧时，把位置表的 JS 行列用 `LineMap.MapPos` 翻译回 `.ts` 行列，
   再取 **`.ts` 原文**那一行渲染。

### 覆盖范围

| 场景 | 是否映射 | 证据 |
| --- | --- | --- |
| 入口 `.ts` 运行时错误 | ✅ | `vm/vm_ts_sourcemap_test.go:TestTSRuntimeFrameEntry` |
| 被 import 的 `.ts` 运行时错误 | ✅ | `TestTSRuntimeFrameModule`、`TestFixtureModuleRuntimeFrame` |
| `enum` / `namespace` 改变行数 | ✅ | `TestLineMapEnumShiftsLines`、`TestTSRuntimeFrameNamespaceAfterShift` |
| esbuild 转译错误 | ✅（esbuild 原生带 `.ts` 行列） | `TestFixtureBroken` |
| 引擎 parser 在转译后 JS 上报错 | ✅ 尽力 | `LineMap.RemapMessage`（把 `line N:M` 改写回 `.ts`） |
| 映射缺失 / 失败 | ⚠️ **回退并标注** | `TestRenderFrameFallbackAnnotated`：回退到转译后 JS，打印 `(line mapped from JS: …)` |

映射不可用时**绝不假装精确**：帧会显式标注它展示的是转译后 JS 的第几行。宁可
暴露"不精确"，也不能拿 JS 行号去索引 `.ts` 原文给出一个看似正确、其实错位的帧。

### 真实输出（证据）

```
$ gox testdata/ts/throw_entry.ts
vm error: Error: fixture-boom
    --> testdata/ts/throw_module.ts:11:3
   |
 11 |   throw new Error("fixture-boom");
   |   ^
```

`throw_module.ts` 里 `throw` 确实在第 11 行（前面有个会改变行数的 `enum`）；帧展示
的是用户写的 TS 原文，而不是剥离后的 JS。

```
$ gox testdata/ts/broken.ts
broken.ts: TypeScript 转译失败
  broken.ts:4:14: Unexpected "="
      const broken: = 1;
```

## 4. 转译缓存（P0-2）

- **位置**：`$GOX_CACHE_DIR/transpile/v<版本>/`；未设 `GOX_CACHE_DIR` 时是
  `~/.gox/cache/transpile/v<版本>/`。
- **键**：`sha256(缓存版本 + 文件绝对路径 + 转译选项 + 文件内容)`。内容一变键就变，
  不存在脏读；选项一变 `cacheVersion` bump，整个版本目录自然析构。
- **值**：转译产物 JS + 原始 sourcemap JSON；命中时现场解码成 `LineMap`。
- **写入**：临时文件 + `rename` 原子替换；缓存目录不可写 / 文件损坏 / JSON 非法时
  **安全重建**，绝不影响转译主流程。
- **接线**：VM 模块加载（`loadModuleFile`）与入口（`EvalFileVM`）都走
  `tstransform.TransformCached` —— 同一文件二次加载、二次进程启动都命中。
- **观测**：设 `GOX_TS_DEBUG=1`，每次转译打印 `[tstransform] cache hit|miss <path>`。

实测（见 §8 脚本）：

```
[tstransform] cache miss /private/tmp/tsdev/src/main.ts
[tstransform] cache miss /private/tmp/tsdev/src/main.ts
[tstransform] cache hit  /private/tmp/tsdev/src/main.ts
```

测试：`tstransform/sourcemap_test.go` 的 `TestTransformCachedHit`（用哨兵证明第二次
真的读了缓存，而不是靠时间/日志这种不可靠信号）、`TestTransformCacheInvalidatedByContent`、
`TestTransformCacheCorrupt`、`TestTransformCacheMissingDir`。

## 5. `gox build` 的 TS 入口（P0-3）

`cmd/gox/cmd_build.go` 的入口探测已经是 `main.js → main.tsx → main.ts → main.jsx`。
打包器 `packager/main.go` 做了两件事：

1. **TS 感知的模块收集**：`./math.js` 回落 `math.ts`，无后缀按 `.js/.jsx/.ts/.tsx`
   试探，目录走 `index.*` —— 与 VM 的解析顺序一致，避免"开发能跑、打包找不到"。
2. **嵌入前先转译、落地成 JS**：每个 TS 模块被 `TransformCached` 转译，并把相对
   导入里显式的 `.ts/.tsx/.mts/.cts/.jsx` 说明符改写成 `.js`。

**选型理由（已在 `prepareAppFiles` 注释里写明）**：选择"嵌入已转译的 JS"，而不是把
`tstransform` 链接进生成的运行时模板 —— 发布产物不该背一个只在构建时用一次的
esbuild（体积 + 启动开销），且模板保持"只认 JS"，与纯 JS 工程产物走完全相同的路径，
减少分叉。代价是模块名从 `.tsx` 变 `.js`，所以必须同步改写说明符。

测试：`packager/main_test.go` 的 `TestPrepareAppFilesRunnable`（TS 入口 → 转译 → 嵌入
→ **VM 真执行**得到结果 21）、`TestPrepareAppFilesScaffoldTS`（真实 `--ts` 模板全链路
产物可被引擎 parser 解析）、`TestResolveImportPathFallbacks`、`TestRewriteTSImportSpecifiers`。

> 不跑 `go build` 真机产物：那需要完整 GUI 工具链，属于 CI/发布环节。这里验证的是
> "转译 + 嵌入 + 解析 + 执行"这条与 `go build` 无关的语义链路（参照
> `scaffold/scaffold_test.go:TestGeneratedTSScriptsCompile` 的既有做法）。

## 6. `gox types`（P1-4）

```
gox types [输出文件]      # 缺省: 有 src/ 时写 src/gox.d.ts, 否则 gox.d.ts; '-' 打印到 stdout
```

从 **Go 侧内置模块注册表**（`object.RegisterBuiltinModule`）生成 `.d.ts`：

- 覆盖 `gx/gfx`、`gx/solid`、`gx/view`、`gx/router`、`gx/screen`、`gx/viewport`、
  `gx/theme`、`gx/dev`、`gx/storage`、`gx/update`、`gx/device`、`gx/app`、`gx/geo`、
  `gx/media`、`gx/permission`、`gx/dialog`、`gx/a11y`，以及聚合入口 `gox`，外加
  `fs / path / http / process`。
- **不编造签名**：内置导出是 Go 闭包，无法反射参数/返回类型，统一生成
  `export function NAME(...args: any[]): any;` 并加 `/** 类型待补 … */` 注释；
  非函数值生成 `export const NAME: any;` 并加注。
- 附 JSX 内置元素的宽松声明（`declare namespace JSX`，内置元素走索引签名）。
- 触发时机：`gx/solid`、`gx/storage`、`gx/update` 与聚合 `gox` 的注册在
  `stdlib.SetupGlobals` 里（不是 `init`），所以命令会先调用一次 `SetupGlobals`，
  否则这些模块会**静默缺失**。

### 用户模块的最小 `.d.ts`

文末附一段"用户模块导出清单"：扫描输出文件所在目录，列出每个模块的顶层导出名，
供手写 interface / 补全类型时参考。

**为什么是清单而不是 `declare module "./app" {}`**：TS 的**全局** `.d.ts` 里对相对
路径写 ambient module 声明会直接报 `TS2436`（Ambient module declaration cannot
specify relative module name）；写成模块增强又会与真实导出冲突。因此这里只给"起点"。
`.ts/.tsx` 的类型本就写在源文件里；对 `.js` 模块，这是补 `.d.ts` 的起步。

### 模板一致性

`scaffold/template/ts/src/gox.d.ts` **由该命令生成**，并由
`cmd/gox/cmd_types_test.go:TestGeneratedTypesMatchTemplate` 钉住"逐字节一致"。改了
内置模块导出面后，重跑：

```bash
go run ./cmd/gox types scaffold/template/ts/src/gox.d.ts
```

## 7. 装饰器（P1-5）

`tstransform` 向 esbuild 传 `TsconfigRaw: {"compilerOptions":{"experimentalDecorators":true}}`，
让 legacy TS 装饰器在**转译阶段降级成等价 JS**（`__decorate` 辅助调用），于是引擎
parser 不必认识 `@`。

- 测试：`tstransform/sourcemap_test.go:TestDecoratorDowngraded`（不含 `@log`）
  + `vm/vm_ts_fixtures_test.go:TestFixtureDecorator`（转译产物能被 VM 执行）。
- `useDefineForClassFields` 保持 esbuild 默认（对 ESNext 默认 define 语义）。显式改它
  会让类字段初始化语义漂移，而引擎的类字段实现更接近赋值路径 —— 保持默认，需要时
  再单独收口。

## 8. `gox dev`：进程级热重启 ≠ 组件级 HMR（P1-6）

`gox dev` 有两条路径：

- **进程内重载**（普通脚本）：丢弃旧 VM、重建并重跑入口（原行为）。
- **进程级热重启**（GUI/常驻脚本）：应用作为**子进程**运行；文件变更 → 杀旧进程 +
  起新进程。

触发判据：入口向上能找到 `gox.json`（脚手架工程都有）即视为 GUI 工程；也可用
`gox dev --restart <入口>` 强制。原因：`render(...)` 之后脚本阻塞在窗口消息泵里，
进程内的 dev 循环根本拿不到控制权（旧实现的 TODO）。

### 实测数字

在无头环境用 `gox dev --restart` + 一个会常驻的 `.ts` 脚本（`console.log("READY")`
后 `setInterval`），Python 以高分辨率时间戳测"写入文件 → 新子进程打印 READY"：

```
RESTART_MS: [297.8, 295.8, 267.9, 268.7]
MIN=267.9 MEDIAN=295.8 MAX=297.8
SAME_CONTENT_RESTART_MS: 271.7
```

即 **"改一行 → 应用重新起来"约 268–298ms（中位 ≈296ms），稳定低于 1s 目标**。
其中约 250ms 是防抖窗口（`devDebounce`），其余是杀旧进程 + 拉起子进程 + 子进程
冷启动 + 命中缓存的转译。内容不变的重复变更会命中转译缓存
（日志出现 `[tstransform] cache hit`）。

> 诚实边界：这是**无头**脚本的实测。真 GUI 应用还要加上"创建窗口 + 首帧渲染"的
> 平台耗时（未在本环境测量）。"进程级热重启"这条路径的延迟与窗口后端无关的部分
> 就是上面的数字。

### 与组件级 HMR 的区别

- **进程级热重启（已实现）**：丢内存状态。signal、表单输入、滚动位置全部重置。
  换来的是实现简单、对脚本零要求、状态模型清晰。
- **组件级 HMR（未实现，后续里程碑）**：保留 signal / 状态，只替换根元素树。前置条件：
  1. **gfx 侧 pump 挂钩**：需要窗口消息泵暴露"文件变更 → 在泵线程里执行重载"的
     回调（窗口是进程级资源，不能像重启那样重建）。
  2. **根元素树替换**：gfx 需要提供"用新组件替换当前根树、复用窗口"的 API。
  3. **signal 订阅迁移**：旧树销毁 / 新树挂载时，signal 的订阅关系与 effect 生命周期
     必须正确迁移，否则会泄漏或丢更新。

  在 gfx 上游把这三件事定下来之前，本里程碑**不假装实现了组件级 HMR**。

## 9. 模板与夹具（P1-7）

- `scaffold/template/ts/package.json` 与 JS 模板的 `dev` 改为
  `goxjs dev src/main.js|tsx`；TS 模板新增 `types` 脚本
  （`goxjs types src/gox.d.ts`）。npm 包同时提供 `goxjs` 与 `gox` 两个 bin。
- `scaffold/template/ts/src/gox.d.ts` 由 `gox types` 生成（§6）。
- 持久化夹具 `testdata/ts/`：`annotations.ts`（类型注解）、`enum.ts`（enum）、
  `decorator.ts`（装饰器）、`component.tsx`（TSX 组件）、`entry.ts`+`util.ts`
  （跨 `.ts` import）、`broken.ts`（故意报错）、`throw_entry.ts`+`throw_module.ts`
  （运行时错误定位）。测试见 `vm/vm_ts_fixtures_test.go`。

## 10. 自检

```bash
go build ./...
go test ./tstransform/ ./vm/ ./cmd/gox/ ./packager/ ./scaffold/
python3 scripts/check-registries.py
python3 scripts/check-imports.py
```

`scripts/check-imports.py` 会校验 `docs/`、`testdata/`、`scaffold/template/` 里所有
`.ts/.tsx` 的 import 与内置模块导出表一致 —— 新增夹具/示例时保持 import 名真实。
