# 第三方库兼容性矩阵（实测）

> 本文是「主流 npm 库在 Gox 上能不能跑」的**实测记录**，结论全部来自冒烟脚本
> 实际执行，不是推测。口径与 [`docs/npm-compat.md`](./npm-compat.md) 三条红线一致；
> 本文回答的是更具体的问题：**红线之内，真实库到底能不能跑起来。**
>
> 测试方法：npm 安装各库（版本见表），编写最小 ESM 冒烟脚本覆盖核心 API，
> 用 `gox <script>.mjs` 执行，记录通过/失败及第一失败点。测试时间：2026-10。
> 冒烟脚本保存于本文件附录，可复跑。

## 结论表

| 库 | 版本 | 结论 | 说明 / 失败原因 |
|---|---|---|---|
| uuid | 14.0.3 | 部分可用 | `v4()` 生成、唯一性、`validate` 中 2/3 通过；`validate()` 失败：运行期报 `REGEX is not defined`（正则字面量子集不支持） |
| lodash-es | 4.18.1 | 不可用 | 跨模块具名导入绑定失败：单导 `lodash-es/chunk` 即报 `toInteger is not defined`（模块间循环互引时导入绑定未正确建立）；另有正则不受支持（`(?=...)` lookahead、`\u200d` 等 unicode 转义） |
| axios | 1.20.0 | 不可用 | 构建产物含字符类 `[\s\uFEFF\xA0]`，编译期 `SyntaxError: Invalid regular expression`（正则 `\uFEFF` 转义不支持） |
| dayjs | 1.11.23 | 不可用 | 默认入口 `main: dayjs.min.js` 为 UMD/CJS，解析报错（红线一：不做 CJS）；改走子路径 `import "dayjs/esm"` 可解析，但编译失败：模块作用域重复声明 `diff` 被拒绝 |
| validator | 13.15.35 | 不可用 | 编译期解析失败：`missing semicolon before COMMA`（压缩产物含 Gox 语法子集不覆盖的写法） |
| qs | 6.16.0 | 不可用 | 主入口为 CJS：`ReferenceError: require is not defined`（红线一，刻意 loud failure） |
| ms | 2.1.3 | 不可用 | 主入口为 CJS：`ReferenceError: module is not defined`（红线一） |
| nanoid | 6.0.2 | 不可用 | 模块绑定失败：`GET_RANDOM_LIMIT is not defined`（跨模块绑定建立顺序问题，同 lodash-es 一类） |

## 失败模式归类（比单库结论更重要）

实测 8 个库，失败全部落在 4 类根因上：

1. **纯 CJS 包**（qs、ms、dayjs 默认入口、lodash 主包）：红线一既定行为，
   `require/module is not defined`，loud failure，非 bug。
2. **模块绑定在循环/深层互引下失效**（lodash-es、nanoid）：`import` 的具名绑定
   在被依赖模块执行时未建立，报 `X is not defined`。这是**运行时模块求值
   顺序的真实缺陷**，堵死了 lodash-es 这类内部多文件互引的大包——优先级最高的
   生态欠账。
3. **正则子集**（axios、lodash-es、uuid 部分）：lookahead `(?=...)`、
   `\uFEFF`/`\u200d` 等 unicode 转义不支持。现代打包产物的压缩代码普遍踩中。
4. **压缩产物语法子集缺口**（validator、dayjs.min）：parser 在合法但压缩过的
   语法上报 `missing semicolon before COMMA`。

**选包建议**（给用户的口径）：单文件、无内部循环互引、不依赖复杂正则的
纯 ESM 小包可以跑（uuid 主体即属此类）；其余主流库 v0 基本不可用，优先用
Gox 内置模块（`fs/path/http/process`）或 `gx/*` 宿主模块替代。

## 附录：冒烟脚本（节选核心断言）

```js
// uuid —— 唯一通过的库的主体
import { v4, validate } from "uuid";
/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(v4()); // ✅
v4() !== v4();          // ✅
validate(v4());         // ❌ REGEX is not defined

// lodash-es —— 单函数子路径导入即失败
import chunk from "lodash-es/chunk";
chunk([1,2,3,4], 2);    // ❌ toInteger is not defined

// dayjs —— 绕过 CJS 主入口后仍失败
import dayjs from "dayjs/esm";
dayjs("2020-01-02");    // ❌ Identifier 'diff' has already been declared
```

> 完整脚本（axios / validator / qs / ms / nanoid / lodash-es 20 项）见任务
> rRNzkC 的工作区 `/tmp/compat/smoke_*.mjs`，可按上表方法复跑。
