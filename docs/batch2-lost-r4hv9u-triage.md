# 看板 r4hv9u —— batch2 合流 8 条 LOST 逐条归因（2026-10-06）

工作流: `wt-gox-ledger2`（分支 `wt/ledger2`，基线 `f1a46fb` = `merge/batch5` 尖）。
口径: 全部用**当前 f1a46fb 自编二进制**逐条 `gox test262 -root D:/test262 -one` 复核
（batch3/4/5 合入 30 个提交后状态可能已变），归因只分三类:
**A** 已由后续提交修复 / **B** 引擎真缺口（本流修复）/ **C** 测试基建阻塞。

## 一、逐条状态与归因

| # | 用例（相对 `test/`） | 当前 pass? | 实测报错原文（首行） | 归因 | 修复提交 |
|---|---|---|---|---|---|
| 1 | `language/expressions/class/cpn-class-expr-accessors-computed-property-name-from-yield-expression.js` | ✅ 修复后 pass | f1a46fb 基线: `vm error: TypeError: yield outside generator` | **B** | `23a7aa3`（本流） |
| 2 | `language/statements/class/cpn-class-decl-accessors-computed-property-name-from-yield-expression.js` | ✅ 修复后 pass | f1a46fb 基线: `vm error: TypeError: yield outside generator` | **B** | `23a7aa3`（本流） |
| 3 | `language/expressions/object/method-definition/early-errors-object-method-await-in-formals-default.js` | ✅ 修复后 pass | f1a46fb 基线: `期望 parse 报错, 实际 runtime 阶段错误: vm error: Test262: This statement should not be evaluated.`（`({ async foo (x = await) {} })` 未做早错） | **B** | `864a1e3`（本流） |
| 4 | `language/statements/class/definition/early-errors-class-method-await-in-formals-default.js` | ✅ 修复后 pass | f1a46fb 基线: 同 #3（`class Foo { async foo (x = await) {} }` 未做早错） | **B** | `864a1e3`（本流） |
| 5 | `language/module-code/top-level-await/fulfillment-order.js` | ✅（f1a46fb 已 pass） | —— | **A** | `d62aaf5`（批次四 rEMAhe） |
| 6 | `language/module-code/top-level-await/rejection-order.js` | ✅（f1a46fb 已 pass） | —— | **A** | `d62aaf5`（批次四 rEMAhe） |
| 7 | `language/module-code/top-level-await/unobservable-global-async-evaluation-count-reset.js` | ✅（f1a46fb 已 pass） | —— | **A** | `d62aaf5`（批次四 rEMAhe） |
| 8 | `language/statements/for-await-of/async-gen-dstr-const-ary-init-iter-close.js` | ✅（f1a46fb 已 pass） | —— | **A**（基建侧抖动，非引擎缺口） | 无单一修复提交，见下 |

> 原看板备注的 C 类判断（"5–7 依赖 module 用例走真模块入口（rNR2Zk / wt/runner3-v2）"）
> 经复核**已过时**: 5–7 在 f1a46fb 上直接 pass，真正让它们转正的是批次四的
> `d62aaf5`（async 模块图求值顺序，提交信息明言"批次二 TLA 的 3 条 LOST 全部
> 转正"），不是 rNR2Zk 的模块入口改造；故按 A 归因，不构成对 wt/runner3-v2 的阻塞。

## 二、B 类修复详情（2 个提交）

### `23a7aa3` —— 静态成员计算键改在外层上下文预求值（#1、#2）

- **根因**: 静态元素全部编进 `__static_init__` 合成函数（非生成器帧），静态成员的
  `ComputedPropertyName` 也在其中编译执行 —— 外层是生成器时 `static get [yield 9](){}`
  的 `yield` 在非生成器帧执行，撞运行时 `TypeError: yield outside generator`。
- **修法**（规范 ClassElementList Evaluation: 静态 ComputedPropertyName 在外层函数
  上下文求值）: `compileClassBody` 在调用静态合成函数前，按 `statics` 定义顺序把全部
  静态计算键在外层作用域求值并收进数组（`OP_NEW_ARRAY`），作唯一实参经
  `OP_CALL_METHOD 1` 传入；`compileStaticInitFn` 多一个 `__static_keys__` 形参槽
  （slot 0）；`compileStaticElements` 内 `emitStaticKey` 一律从预求值数组按序取。
  无静态计算键时签名与行为完全不变。顺带修正静态计算键的 `this`（此前被错绑成类对象）。
- **A/B**（f1a46fb 基线 vs 修复, JSON 定向对比, 见提交信息）:
  `expressions/class` GAIN 1 / LOST 0；`statements/class` GAIN 3 / LOST 0
  （含 2 条附带转正: `private-meth-static-ary-ptrn-elem-id-init-fn-name-class.js`、
  `private-meth-ary-ptrn-elem-id-iter-done.js`）。
- **测试**: 新增 `vm/static_computed_key_test.go` 2 例（生成器内 yield 计算键端到端 +
  计算键先于静态字段初始化器的求值顺序）。

### `864a1e3` —— async 函数形参区 await 保留字早错（#3、#4）

- **根因**: `parseParameters` 把形参区恒置 ~Await，但缺「async 函数形参里 await 是
  保留字」判据 —— `x = await` 的 `await` 被当成 sloppy 标识符收下，负例放行到运行期
  才被 `$DONOTEVALUATE` 探针揭穿。
- **修法**（与 Node 22 逐形态对齐）: 新增 parser 字段 `awaitReservedInParams`，仅
  「async 函数/箭头/方法的形参列表（含默认表达式）」窗口内置位，`parseParameters` 的
  12 个调用点逐个显式传所属函数自身的 async 与否；`setAllowAwait` 联动清零/恢复
  （嵌套函数/箭头**体**不是形参窗口，其中 await 回归普通标识符）；`isBindingName` 与
  `parseAwaitExpression` 的标识符回退路径在该窗口内拦截 AWAIT —— 形参名、解构绑定名、
  默认表达式、成员访问、对象/类计算键（`[await]`）全形态覆盖。
- **A/B**: `async-function` +2、`async-arrow-function` +2、`async-generator` +1、
  `method-definition` +2、`statements/class/definition` +2、`top-level-await` 0，
  合计 **GAIN 9 / LOST 0**（整族 await-in-formals 转正）。
- **测试**: `parser/param_early_error_test.go` 新增 `TestAwaitReservedInAsyncParams`
  22 例正/负面对照。

## 三、A 类详情（#5–#8，无需引擎改动）

### #5–#7 TLA 顺序三连 —— `d62aaf5`（批次四, rEMAhe）

`feat(vm/compiler): async 模块图求值顺序`: 根因是 `OP_IMPORT` 同步内联求值 + TLA 后
promise 回调中途重入 ⇒ 依赖求值顺序错。新增 `moduleRecord` 状态机（静态依赖
leaf-to-root 门控、TLA 挂起-恢复、rejection 沿依赖边传播且依赖方本体不运行、
dynamic import 等模块完成）。提交信息明言"批次二 TLA 的 3 条 LOST 全部转正"，
定向 `top-level-await|module-code` 259/622 → 262/622（GAIN 3 / LOST 0）。
f1a46fb 上 `-one` 复核: 三条均 pass。

### #8 for-await 解构 IteratorClose —— 无引擎缺口，batch2 全量跑的判定抖动

- 在 **merge/batch2 尖（`7a016b6`）** 上用 `-one` 复跑即 **pass**；`merge/batch3`
  （`b6ece6e`）、f1a46fb 同样 pass ⇒ 引擎侧从 batch2 起就满足该用例，批次二全量 A/B
  记 LOST 是**全量跑的判定抖动**（该用例 `flags: [generated, async]`，`-jobs 4` 分片
  并发下的超时/孤儿重派类抖动，与 §十四 记录的 ±2 判定漂移同源）。
- f1a46fb 基线与本流修复候选在 `for-await-of` 整族 filter 上 JSON 逐例对比:
  1187/1187 完全一致，GAIN 0 / LOST 0 ⇒ 确认无回归、无残留缺口。
- 归因按 **A** 记账（"当前已由后续状态转正"）；如需单点追责可查全量跑的执行日志，
  引擎侧无需任何修复。**C 类判断不成立**（不依赖 rNR2Zk 模块入口）。

## 四、结论

- **A（后续提交/基建侧已转正）**: #5、#6、#7（`d62aaf5`）、#8（全量跑抖动，引擎无缺口）。
- **B（引擎真缺口，本流修复）**: #1、#2（`23a7aa3`）、#3、#4（`864a1e3`）。
- **C（基建阻塞）**: 无。rNR2Zk / wt/runner3-v2 对本组 8 条不构成阻塞。
- 修复后 `go test ./parser/... ./compiler/... ./vm/...` 全绿；8/8 用例最终全部 pass。
