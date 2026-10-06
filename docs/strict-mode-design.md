# Gox 严格模式 (strict mode) 传播设计 —— 看板 r63RpV 子项 1

> 状态: **本轮仅设计, 不实现**。基线 f1a46fb。后续工作流按本文落地。

## 0. 现状盘点 (f1a46fb)

严格上下文在引擎里已有**两套并行标志**, 均按 "上下文布尔 + 进出保存/恢复"
模式工作, 这是本设计的基础:

| 层 | 标志 | 入口语义 |
|----|------|----------|
| parser | `Parser.strict` (parser/parser.go:36) | script 顶层默认 sloppy; 指令 prologue 命中精确 `"use strict"` → true (parser.go:453-455); **模块顶层恒 strict** (parser.go:432 `p.strict = p.module`); class 体/函数体经 `setStrict` (parser.go:196) 进出保存恢复 |
| compiler | `Compiler.strict` (compiler/compiler.go:42) | `Compile` 入口 `c.strict = program.Strict \|\| c.moduleMode` (compiler.go:310); class 体编译期强制 true (compiler.go:2457-2459); 盖章到 `FunctionMetadata.IsStrict` (compiler.go:2991 等 4 处) → `object.Closure.IsStrict` (object/function.go:41), VM `callClosure` 据此决定 sloppy this 归一与否 |

**已生效的 strict 语义** (均随早期批次合流, 无需重做):

- strict 下赋值未声明名 → 运行期 ReferenceError: `emitAssignmentStore` 对
  `unresolved && c.strict` 发 `OP_STORE_UNDECLARED` (compiler.go:3958);
- 未声明名读取 (`x++`/`--x`/`arguments` 裸引用) → 运行期 ReferenceError
  (r63RpV 子项 2, f1a46fb 内 `1181ce2`, 走 `OP_LOAD_GLOBAL`);
- 编译期 SyntaxError: `delete` 标识符 (compiler.go:4174)、eval/arguments 作
  赋值目标 (compiler.go:4238);
- 解析期早错 (parser 各 early-errors 文件): 重复形参、eval/arguments 作
  绑定名、legacy 八进制字面量、严格保留字作绑定名、new.target 位置。

**尚未接通 (本设计的核心缺口)**:

1. **strict 下 `with` 语句未报 SyntaxError**。parser.go:1624 的
   `withStrictForbidden` 是 with 工作流刻意留下的**单一挂载点**:
   `var withStrictForbidden = func() bool { return false }`, 注释明示
   "合流时只需把这一行替换为读取当前函数/单元的 strict 状态"。
2. strict 传播的**收尾断言**缺失: `p.strict`/`c.strict` 已是继承式传播, 但
   没有任何测试矩阵钉住 "四入口 × 嵌套函数 × eval" 的传播结果, 回归无护栏。
3. 各早错规则对"继承来的 strict"(外层指令/模块)与"自身 strict"未做区分
   (14.1.2 非简单形参 + 自身指令的情形已由 `lastBodyUsesStrict` 覆盖,
   其余规则理论上只需当前 strict, 待实现时逐条核对)。

## 1. 四个入口的传播方案

### 1.1 script 顶层 `"use strict"` 指令 (Directive Prologue)

- parser: `ParseProgram` 的 prologue 循环已有实现 (parser.go:446-462):
  `directiveString` + `isUseStrictDirective` 精确判定 (排除
  `("use strict")` 括号形与 `"use\u0020strict"` 转义形), 命中即
  `program.Strict = true; p.strict = true`。
- compiler: `program.Strict` 已被 `Compile` 入口消费 (compiler.go:310)。
- **结论: 已通, 无需改动**; 设计上只需把 1.4 的函数体路径对齐同一口径。

### 1.2 模块默认 strict

- parser: `SetModule(true)` → `p.strict = p.module` (parser.go:432), 模块
  顶层无需指令即 strict。
- compiler: `c.moduleMode` 与 `program.Strict` 并列参与入口赋值。
- **结论: 已通**。eval 以 CommonJS 风格脚本执行时 strict 只看源码指令
  (见 §4 说明)。

### 1.3 class 体

- parser: `parseClassBody` 前后 `setStrict(true)` (parser.go:3892/3981 两处
  类相关入口), 类体及其字段初始化器、方法体全部按 strict 解析。
- compiler: class 体编译期 `c.strict = true` 并恢复 (compiler.go:2457-2459),
  隐式构造器的 `meta.IsStrict` 不漏盖章。
- **结论: 已通**; 后续新增早错规则时 class 体自动继承, 无需特判。

### 1.4 函数体 directive prologue 与继承

- parser: `parseFunctionBodyWithStrict(isAsync)` (parser.go:1865) 以
  `p.strict = inherited` 进入, 体首 prologue 命中指令则置 true (parser.go:1845),
  结果写 `fn.Strict` (AST) 与 `p.lastBodyUsesStrict` (14.1.2 早错用)。
- compiler: `meta.IsStrict = c.strict` 在函数编译出口盖章 (compiler.go:5942/
  6076/6185), 箭头按词法继承所在帧 strict。
- **结论: 已通**; 剩余风险只在 "嵌套函数体内再出现指令" 的传播顺序,
  由 1.4 的 save/restore 语义天然保证 (函数返回时恢复外层值)。

## 2. 与 moduleEE / usingDeclAllowed 标志机制的整合

现有三个上下文标志是同一家族, strict 不应新增第四旗, 而是**挂到同一
save/restore 框架**:

- `moduleEE` (parser.go:78): "按模块语义做早错" 的弱模块标志 (test262
  模块用例以脚本方式执行)。strict 整合点: `moduleEE` 单元同样恒 strict
  (模块无 sloppy), 故 parser.go:432 一行扩为 `p.strict = p.module || p.moduleEE`
  即可, compiler 侧 `moduleMode` 保持独立语义 (真模块编译用)。
- `usingAllowed` / `usingDeclAllowed()` (parser.go:97/862): StatementListItem
  位置谓词, 在 `parseBlockImpl`/`parseBlockWithDirectives` 进出时置位。
  strict 的新早错 (with/赋值未声明名等) 与它**判定点同层** (都在
  StatementListItem/语句级), 直接读 `p.strict`/`c.strict`, 不需要新机制。
- `evalTopLevel` (parser.go:~110): eval 源码标志。eval 的 strict 合成规则
  已在 stdlib/eval.go:258-262 实现 ("源码含指令 OR 调用者 strict ⇒ 包装
  函数严格"), 与 §1.4 同口径, 严格传播对 eval 透明。

**整合结论**: 四入口传播全部已存在于 `p.strict`/`c.strict` 继承链; 新规则
一律读取这两个标志, 不引入新状态; 唯一的 literal 改动点是
`withStrictForbidden` 挂载点 (§3 P0)。

## 3. 剩余工作清单 (分期, 后续工作流)

- **P0** (一行): `withStrictForbidden` 改为读当前 strict。因它是包级
  `func() bool` 闭包而非 Parser 方法, 落地时改为
  `p.withStrictForbidden()` 方法或经 `currentParserStrict` 注入, 调用点
  (parser.go:1631) 不动。验收: test262 `statements/with/strict-*.js` 与
  `12.10.1-*-s.js` 由 FAIL 转 pass (基线 25 个 with 失败中的 12 个)。
- **P1**: 传播矩阵测试 (`parser`/`compiler` 两个包各一组): 四入口 ×
  嵌套函数 × class × eval × moduleEE, 钉住 `p.strict`/`c.strict`/
  `meta.IsStrict` 在每个组合上的值。
- **P2**: 逐条核对剩余 strict 早错 (严格八进制重启、`arguments`/`eval` 作
  绑定名的函数级版本、delete 未限定名的 eval 口径), 与 §0 已有规则合并
  去重。

## 4. 已知非目标 (别的工作流)

- 模块顶层 `this=undefined`: 已由 `wt/this-semantics` 半成品随批次合流
  (f1a46fb 内 `e93a99a`), `vm/top_level_this_test.go` 有守卫。
- eval 早错 (类字段初始化器内直接 eval): `wt-gox-eval2` 半成品, 属
  `wt/eval-ee` 工作流。
- eval 对非 Error 抛出值的限制: 见 §5。

## 5. eval 对非 Error 抛出值的模型级限制与桥接方案 —— 看板 r63RpV 子项 5

**现象** (f1a46fb 基线探针): 普通脚本 `try { throw "x" }` catch 到原字符串
(`typeof e === "string"`); 但 `try { eval('throw "x";') }` catch 到的是包装
出来的 `Error` 对象 (`typeof e === "object"`), 原值丢失。

**根因 (模型级)**: VM 的异常经 Go 返回值传播, 只支持 `*object.Error`
(`vm.throwIfError`, vm.go:3098: 内建返回 Error 且非 `ReturnIsValue` ⇒
抛)。stdlib 内建 (eval) 从 Go 侧**没有抛出通道**, 只能把异常编码进返回值。
回调桥 (object/callback.go) 为此设了双槽: `callbackError` (Go 字符串) 与
`callbackErrorValue` (原始 JS 值, vm.go:332 `setCallbackErrorValueFromThrow`
写入)。`runGlobalEval` (stdlib/eval.go:231-246) 已经能把原值取回
(`TakeCallbackErrorValue`) —— 但取回的原值只要不是 `*object.Error`, 就过不了
"内建返回值" 边界, 只能 `NewErrorWithName("Error", ...)` 包一层。**限制不在
eval 本身, 而在"Go 内建调用边界只能以返回值传异常"这一模型约束**;
throw 字符串/数字/普通对象的 eval、Promise executor、timer 回调同族。

**可行桥接 (按推荐序, 均不破坏 try/catch 状态机)**:

- **A. 载体法 (推荐)**: 新增 `*object.ThrownError` (内嵌原始 `object.Value`
  与可读 message)。`runGlobalEval` 对非 Error 抛出值返回 ThrownError 而非
  包装 Error; VM 唯一 choke point `throwIfError` (vm.go:3098) 解包: 命中
  ThrownError 就 `handleThrow(内嵌原值)`。原值已由 `callbackErrorValue` 送到
  嘴边, **不引入任何新全局状态**; 改动收敛在 object/vm/stdlib 三处。收益外溢:
  所有经 throwIfError 的内建路径 (vm.go:1441/1578/3452) 一并获得原样抛
  非 Error 值能力。注意: ThrownError 落到 `e.message` 等普通读法仍需可读
  (内嵌 Go 字符串消息), 或显式记录为"仅解包路径可见"。
- **B. 部分桥接 (最小改动)**: 维持包 Error, 但把原值挂到 `.value` 属性,
  catch 侧 `e.value` 取回载荷。`typeof`/`===` 身份语义仍不符, test262 的
  身份类用例仍 FAIL, 只适合"先保载荷"的过渡。
- **C (不做)**: 让 Go 侧 panic 穿过 VM —— 会绕过 try/catch 的栈恢复与
  finally 语义, 破坏不变量。

**验收口径**: 探针 `eval('throw "s"')` 的 `typeof` 与 `===` 原值; test262
`-filter 'eval-code'` A/B 无 LOST。
