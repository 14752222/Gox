# Gox Test262 提分工程 — T05 交付报告

**周期**：T05（第五轮：按失败面聚类定向提分）
**结论**：language 套件 **78.86% → 85.57%（+6.71pp，+1591 例）**，全程逐用例对比**零真实退潮**；
built-ins 套件本轮 **0 增 0 退**（14796/23823 = 62.11% 不变），但徽章数字从 35.7% 修正为 62.1% —— 见文末「口径说明」。

---

## 一、成果总览

| 指标 | 起点（本轮基线实测） | 终点 | 变化 |
|------|------|------|------|
| language 合规率 | 78.863%（18711/23726） | **85.569%（20302/23726）** | **+6.71pp** |
| language 通过用例数 | 18711 | **20302** | +1591 |
| built-ins 合规率 | 62.108%（14796/23823） | 62.108%（14796/23823） | 0（逐用例对比 0 增 0 退） |
| 全仓单测 | 绿 | 绿 | 无回归 |

> 起点不是仓库里的旧基线数字，而是**同一个 test262 checkout、同一台机器、同一个二进制口径**下用
> 改动前的代码实测跑出来的（18711/23726）—— 与 docs/test262-baseline.json 记录的 78.86% 完全吻合，
> 说明跑批环境可信，后面的每一步增量都是可归因的。

### 分步增量（每步都单独跑全量并做逐用例 diff）

| # | 修复 | 增量 | 退潮 |
|---|------|-----:|-----:|
| 1 | 私有名的 Unicode 转义 + 标识符字符集（ID_Start/ID_Continue） | +413 | 0 |
| 2 | class 方法/访问器 **不可枚举**（新增 6 个 opcode） | +755 | 0 |
| 3 | 入口模块的**顶层 await** 走异步驱动 | +228 | 0 |
| 4 | `import.source()`（ES2025 source phase import）语法与语义 | +81 | 0 |
| 5 | 数字字面量早错（分隔符位置 / BigInt 前导零 / 数字后紧跟标识符） | +63 | 0 |
| 6 | 单语句体不得出现词法声明（`if (x) const y`） | +51 | 0（1 条 flaky，见下） |
| | **合计** | **+1591** | **0** |

「退潮」列是相对**上一步**的逐用例 diff：本轮每一步都要求退潮为 0 才继续下一步。唯一例外是
`language/statements/for/dstr/let-ary-ptrn-elem-id-iter-val-err.js` —— 它在全量跑批里偶发失败，
单独跑（`-filter`）则通过，属引擎包级状态导致的**分片邻居污染**（cmd/gox/cmd_test262.go 文件头已
记录该性质），不是本轮改动引入的回归。

---

## 二、六个根因修复

### 1. 私有名的 Unicode 转义（lexer）

`class C { #\u006F = 1 }` 解析失败：`#` 后紧跟 `\` 时词法器只认标识符起始字符，转义形态直接切成
ILLEGAL，于是整簇 `*-privatename-identifier*` 用例在**类体里**报 `unexpected token in class body: ILLEGAL`。
规范 PrivateIdentifier :: `#` IdentifierName，而 IdentifierName 的任一字符都可写成 `\uXXXX` / `\u{XXXX}`。

- `lexer/lexer.go` 的 `case '#'` 分支接受 `\u` 起始的转义标识符，字面量解码后与 `#o` 同为一个 PRIVATE_NAME；
- 顺带补齐标识符字符集：ID_Start 补 **Nl**（罗马数字）与 **Other_ID_Start**（U+2118 ℘ / U+212E / U+309B / U+309C），
  ID_Continue 补 **Mn/Mc/Pc**、**Other_ID_Continue**（U+00B7 / U+0387 / U+1369..1371 / U+19DA）与 **ZWNJ/ZWJ**；
- ZWNJ/ZWJ 只加在 `isIdentifierPart`、**不加在** `isIdentifierStart`，且 `readIdentifierWithEscapes` 的
  起始位判据收紧为 `isIdentifierStart` —— 否则 `var \u200C;` / `class C { #\u200D_ZWJ; }` 这两簇
  negative 用例会被放过（规范里零宽字符只能作 IdentifierPart）。

### 2. class 方法/访问器不可枚举（bytecode + vm + compiler）

`Object.getOwnPropertyDescriptor(C.prototype, 'm').enumerable` 报 `true`。规范 MakeMethod 走的是
**DefineProperty**（`{enumerable: false, writable: true, configurable: true}`），而编译器此前复用
`OP_SET_PROP`（赋值语义，enumerable: true）。这是全量失败面里最大的单点。

新增 6 个 opcode，栈序/operand 与既有指令一致，只换描述符的 `[[Enumerable]]`：

| opcode | 值 | 用途 |
|--------|----|------|
| `OP_SET_METHOD` | 0x83 | 类方法（命名键，不可枚举数据属性） |
| `OP_SET_GETTER_NE` / `OP_SET_SETTER_NE` | 0x84 / 0x85 | 类访问器（命名键，不可枚举） |
| `OP_SET_METHOD_DYN` | 0x86 | 类方法（计算键，含 Symbol 键） |
| `OP_SET_GETTER_DYN_NE` / `OP_SET_SETTER_DYN_NE` | 0x87 / 0x88 | 类访问器（计算键） |

计算键路径顺带去掉了旧 `SET_INDEX` 留下的 `DUP`/`SWAP`/`POP` 栈搬运（那些 opcode 弹三元组，
`*_DYN` 弹二留一）。对象字面量方法 `{ m(){} }` 仍走 `OP_SET_PROP`（enumerable: true）—— 两者语义不同，
不能合并。

### 3. 入口模块的顶层 await（vm/eval.go）

`language/module-code/top-level-await` 全簇报 `TypeError: yield outside generator`。根因：`runModuleBody`
只对**被 import 的模块**做了 `HasTopLevelAwait()` 分流，而**入口模块**经 `EvalModuleFileVMWithGlobals`
直接 `RunCompiled` —— 模块体里的 `OP_YIELD/OP_AWAIT` 落在非生成器帧上。补上分流走 `RunCompiledAsync`。

### 4. `import.source()`（ES2025 source phase import）

test262 的 `dynamic-import` 簇里 `import.source(obj)` 被当成 import **声明**解析，报
"import declarations may only appear at the top level of a module"。

- parser：`import` `.` `source` `(` 形态解析为 `DynamicImportExpression{IsSourcePhase:true}`
  （新增 `isSourcePhaseImportAhead()`，语句层与 `parseNewExpression` 都要先问它 ——
  `new import.source(...)` 是早错，与 `new import(...)` 同辙）；
- 语义按规范 16.2.1.7.2：SourceTextModuleRecord 的 GetModuleSource 恒抛 SyntaxError，
  故新增 `OP_IMPORT_SOURCE`（0x89）：求值 specifier（ToString 副作用照常发生）后返回
  **rejected Promise(SyntaxError)**，不加载模块。

### 5. 数字字面量早错（lexer）

| 形态 | 规则 |
|------|------|
| `1__0` / `1_` / `0x_1` / `10._1` / `1e_1` | NumericLiteralSeparator 的位置约束（新增 `invalidNumericSeparator`，校验**原始文本**，因为有效数字里 `_` 已被剥掉） |
| `00n` / `01n` / `08n` / `012348n` | BigIntLiteral 不允许前导 0 后跟数字 |
| `3in []` | 数字字面量后紧跟 IdentifierStart 是早错 |
| `0_0` / `01_0` / `00_0` | 单 `0` 与 LegacyOctalLikeDecimal 不允许分隔符 —— 数字停在 `0`，后面的 `_0` 是标识符，由「数字后紧跟标识符」规则兜住 |

十六进制的 `0xE_E` 必须放行：`E` 在十六进制里是数字而非指数符，所以指数相关的两条规则对
带进制前缀的字面量不适用（`hasRadixPrefix`）。

### 6. 单语句体不得出现词法声明（parser）

`if (true) const x = null;` / `while (0) let y;` 此前被当合法声明执行。规范里 if/while/for/do/label 的
**单语句体**不是 StatementList，Declaration 不在此合法。统一在 `parseBody()` 的非 block 分支拦：
let/const/class 一律报错；函数声明仅严格模式报错（Annex B 的 sloppy 扩展允许）。

配套修正 `let` 的换行形态：`let` 后换行再跟**标识符**或 `{` 时，`let` 是标识符引用（ASI 补分号），
不能按声明报错；但 `let [` 不受换行影响，仍是 ExpressionStatement 的 lookahead 禁止形态
（`if (false) let\n[a] = 0;` 必须报 parse 错）。

---

## 三、口径说明（关于 built-ins 徽章 35.7% → 62.1%）

本轮用**改动前**的代码实测 built-ins 得 14796/23823 = 62.108%，而徽章上写的是 35.7%。两者都是真的：
徽章来自 `docs/test262-baseline-builtins.json` 的「上一次通过闸门时的合规率」，而仓库 main 上的引擎代码
早已领先那份旧基线 —— CI 的闸门只拦「跌过 0.5pp」，涨了会刷新基线，但那次刷新显然没赶上最近几批改动。
本轮顺带把两份徽章与两份基线刷成实测值（language 85.6% / built-ins 62.1%）。

测试环境：tc39/test262 `main`（本次 checkout 2026-10-22）、Go 1.26.2、linux/amd64、
`gox test262 -jobs 32`（128 固定分片，与 CI 同口径）。

---

## 四、剩余洼地（下一步依据）

本轮跑完后 language 仍失败 3424 条，按「目录族」排：

| 目录族 | 失败 | 形态 | 说明 |
|--------|-----:|------|------|
| `language/statements/class` + `expressions/class` | ~700 | 语义偏差 / 早错 | 剩下的多是逐条规范细节（私有名重复声明、字段初始化顺序、`#x in obj` 边界） |
| `language/expressions/dynamic-import` | ~230 | 语义偏差 | `import()` 的 Promise 语义与模块求值时序 |
| `language/eval-code/direct` | ~169 | 语义偏差 | 直接 eval 的作用域/严格性继承 |
| `language/expressions/compound-assignment` | ~131 | 语义偏差 | 复合赋值的 ToPrimitive/引用语义 |
| `language/import/import-defer` | ~91 | 功能零实现 | `import defer` 未实现（ES2025） |
| `language/expressions/object` | ~107 | 语义偏差 + 早错 | 生成器体内 `yield` 作标识符引用等 |

「应 parse 报错却执行到运行期」这一形态仍有约 570 条，但已经**高度分散**（`$DONOTEVALUATE` 用例按规范
条文逐条出题，一个根因 20~40 条），继续提分要从「按簇开单」切到「按规范条文逐条对齐」。
