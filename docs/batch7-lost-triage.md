# batch7 全量 A/B 的 3 条 LOST 归因（lead 独立复核）

- 基线：`merge/batch6` 尖 `c6e6ae6`（实测 base **16969/23726**）
- 候选：`merge/batch7` 尖 `5030c7f`（实测 cand **17226/23726**）
- 结果：**GAIN 260 / LOST 3**

3 条 LOST 全在 `language/module-code/`，逐条复核如下。**结论：全部是
`9b001f6`（runner 改走真模块入口 + negative 判据只取首行）揭掉遮羞布后暴露的
真缺口，不是 batch7 引擎改动引入的回归**；其中两条已在本批（batch8）正面修复。

## 复核方法

不依赖跑分，直接用「真实 harness + 真模块入口」跑用例并打印引擎错误原文，
对照 `cmd/gox/cmd_test262.go` 的 `judgePhase` / `errorHead` 判定逻辑。

## 逐条

### 1. `language/module-code/eval-self-abrupt.js`
用例体 `throw new Test262Error();`，`negative: { phase: runtime, type: Test262Error }`。

引擎错误原文（`vm.EvalModuleFileVMWithGlobals`）：
```
vm error: { message: "" }
    --> .../eval-self-abrupt.js:19:2
  |
 19 | throw new Test262Error();
  |  ^
```

**根因**：`Test262Error` 实例在 Go 侧是 `*object.Object`（非 `*object.Error`），
模块入口的未捕获异常渲染走 `ThrowError.Error()` → `Value.Inspect()`，得到
`{ message: "" }`，**构造器类型名完全丢失**。runner 的 `negative.type` 只取错误
首行做 `strings.Contains`，类型名一丢即判 LOST。

注意：`message` 为空是**正确 JS 语义**（`sta.js` 里 `this.message = message || ""`），
缺的是**渲染**——没走 JS 的 `ToString`（用户对象有自定义 `toString`，
应得 `Test262Error: `）。

**归属**：`9b001f6` 的 ①（negative 判据只取首行）让这条从「源码回显侥幸判过」
变成「真判」；而 ②（module 走真模块入口）使渲染路径与 script 入口分叉，
两个入口对同一抛出值渲染不一致 —— ②是引擎侧真缺口。
**已在 batch8 修复**（commit `47fca04`：模块入口改走 `vm.uncaughtError`，与
script 入口同口径）。两个 module-code 用例共 **GAIN 2 / LOST 0**。

### 2. `language/module-code/eval-export-dflt-expr-err-eval.js`
同上：`export default (function() { throw new Test262Error(); })();`，同一根因、
同一修复覆盖。**已在 batch8 修复**。

### 3. `language/module-code/instn-local-bndng-const.js`
断言链末段：
```js
const test262 = 23;
assert.throws(TypeError, function() { test262 = null; });   // ← 这里失败
assert.sameValue(test262, 23, 'binding is not mutable');
```

引擎报 `Expected a TypeError to be thrown but no exception was thrown at all`。

**复核过程**（最小复现，真模块入口）：
```js
const q = 1;
try { q = 2; } catch (e) { caught = e.constructor.name; }
// 实测: caught === "" （没抛）, q 变成 2 —— 模块顶层 const 完全可变
```
对照：**同一段代码以 script 执行会正确抛 TypeError**。

**根因（三条，缺一不可）**：
1. `compiler.isGlobalScope()` = `scope.Depth()==0 && !moduleMode`，模块模式下**恒 false**；
2. 于是 `compileConstStatement` 走 `else` 分支，用 `OP_STORE_CONST` 把模块顶层
   const 当**局部槽位**初始化，而非全局词法绑定；
3. 赋值路径只发 `OP_STORE`（纯槽位写入，**从不查 `Symbol.IsConst`**）⇒ 写入永远成功。

`compiler/symbol_table.go:14` 的 `IsConst` 字段一直存在，但
`compiler/compiler.go:1662` 注释直言「局部槽位根本不查（`IsConst` 目前无人读取）」
—— 这正是本条的病灶：**该字段从未在任何赋值路径上被读取过**。

**归属**：`9b001f6` ②让 module 用例第一次真正以模块语义执行，于是这条断言
第一次被真判。属**真缺口**（模块顶层 const 不可变性零实现），非回归。
**已在 batch8 修复**（commit `a2a671d`：新增 `OP_STORE_CONST_GUARD`，接到全部
局部赋值位置；顺带修正 class 绑定被错标为 const 的既有缺陷）。

## 附：batch7 其余改动的独立复核

batch7 = batch6 + 10 引擎提交 + 8 文档提交。除上述 3 条 LOST 外，
`go build ./...`、`go vet`、`go test -count=1 ./...`（20 包）全绿。
30 个定向 filter 的 A/B（`language` 全量）净 +257。

**两个被诚实标注为「跳过」的公共模型问题**（ag3 agent 复核，非本批引入）：
1. **函数 `.length` 未按规范截断**（首个默认值/rest 前计数）—— `NumParameters`
   同时充当 length 与形参放置个数，拆开需贯穿 compiler→bytecode→vm→object。
   影响 `dflt-params` 288 例中 32 例，**基线即如此**。
2. **数组解构忽略被覆盖的 `Array.prototype[Symbol.iterator]`**（走索引快路径）——
   四类函数表现一致，是共用解构引擎问题；`async-generator/dstr` 12 例失败
   全属此类，**已逐条核对全是基线失败**。

这两条建议**另立看板跟踪单**（不改这批），避免它们在后续排期里继续隐形。
