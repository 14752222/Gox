# 闲时任务 P1：为 Gox 实现 SolidJS 式响应式 + JSX 语法

## 项目背景

- 仓库：当前工作区（Gox 项目根目录），这是一个纯 Go 实现的 JavaScript 引擎（Gox），
  目录结构：`lexer/ parser/ ast/ compiler/ bytecode/ vm/ object/ runtime/ stdlib/`，
  入口 `main.go`，打包器 `packager/`。
- go.mod 零第三方依赖，**必须保持**。代码注释为中文，新代码遵循同样风格。
- 总目标（仅供理解，本任务只做 P1）：让 Gox 支持类 SolidJS 的 GUI 编程模型，
  JSX 降级为 `h()` 调用，未来接自研渲染器。本任务不涉及任何窗口/渲染代码。

## 任务范围（P1，三件事）

### 1. signals：Go 实现的细粒度响应式，暴露为 `gx/solid` 模块

在 `stdlib/` 新增实现（建议 `stdlib/solid.go`），参考现有内建对象的注册方式
（`stdlib/stdlib.go` 的 `SetupGlobals`，以及 host module 机制可看
`vm/vm_host_modules_test.go`），让 JS 侧可以 `import { createSignal, createEffect, createMemo } from "gx/solid"`。
若模块系统不适合，可退而求其次暴露为全局对象 `gx.solid`，在报告中说明选择。

API 语义（对齐 Solid）：
- `createSignal(initial)` 返回 `[getter, setter]`：
  - `getter()` 返回当前值；若在 `createEffect`/`createMemo` 回调执行期间被调用，则自动订阅。
  - `setter(v)` 或 `setter(prev => v)`（更新函数收旧值）；新值与旧值 `===` 相同则不通知。
- `createEffect(fn)`：立即执行一次 fn；执行期间调用过的 getter 构成依赖集；
  依赖变化时重新执行 fn，且**每次运行后重新收集依赖**（上轮未被引用的依赖要退订）。
  返回一个 dispose 函数，调用后注销该 effect。
- `createMemo(fn)`：类似 effect，但缓存计算值，getter 读缓存；依赖变化只标脏、下次读取时重算。

实现要点：用 Go 侧全局"当前正在运行的 effect"栈做依赖收集，全部在 VM 单线程内执行，
不需要锁。不需要 batch/onCleanup（留作后续）。

### 2. JSX：lexer/parser 层语法糖，降级为 `h()` 调用

**compiler 和 bytecode 一律不改**——JSX 在 parser 阶段直接重写成普通 CallExpression。

降级规则（v1）：
- `<tag attr={expr} attr2="字面量">...子节点...</tag>`
  → `h("tag", {attr: expr, attr2: "字面量"}, ...子节点)`
- 自闭合 `<tag attr={v} />` → `h("tag", {attr: v})`
- 子节点：文本串与 `{任意表达式}` 按出现顺序作为 h 的后续参数；
  **子节点是函数时原样传递，不求值**（响应式 computed 约定）。
- 大写开头的标签视为组件：`<Counter step={2} >hi</Counter>`
  → `Counter({step: 2}, "hi")`（解析为作用域内的标识符调用）。
- 空白规则（Babel/Solid 语义）：跨行文本按行首尾 trim，纯空白行删除。
- `h` 是普通标识符，按词法作用域解析，引擎不做任何自动注入。
- v1 不做：`<>...</>` 片段、`{...spread}`、HTML 实体、JSX 注释。

lexer 实现策略：加一个模式栈——`<` 在**表达式位置**出现且后随标识符/`>` 时进入
"标签内"模式（词法：标识符、`=`、`{` 切回普通表达式词法直到配对 `}`）；
标签闭合后进入"子文本"模式（裸文本直到 `<` 或 `{`）。参考主流手写 JSX 解析器的做法。

### 3. 测试与演示

- parser 测试：给出 JSX 输入，断言产出的 AST 与手写 `h(...)` 调用的 AST 等价（覆盖：
  自闭合、嵌套、文本+表达式混排、函数子节点、大写组件、空白规整）。
- signals 测试（vm 层）：effect 立即执行、依赖变化触发重跑、依赖退订正确
  （改无关 signal 不触发）、memo 缓存、更新函数形式 setter。
- 集成演示：新建 `test/testdata/jsx_demo.js`，内容大致为：

```js
import { createSignal, createEffect } from "gx/solid";
function h(tag, props, ...children) { return { tag, props, children }; }
const [count, setCount] = createSignal(0);
const ui =
  <column gap={8}>
    <text font={20}>{() => `count: ${count()}`}</text>
    <button onClick={() => setCount(c => c + 1)}>加一</button>
  </column>;
console.log(JSON.stringify(ui, null, 2));
createEffect(() => console.log("count is", count()));
setCount(5);
```

用 `go run . test/testdata/jsx_demo.js` 验证：打印出正确的节点树，且 effect 输出 `0` 后输出 `5`。

## 验收标准（全部满足才算完成）

1. `go build ./...` 通过。
2. `go test ./...` 全绿（**不得修改既有测试的断言来凑绿**；既有测试失败且与本次改动无关时，停止并在报告中说明）。
3. 上面的演示脚本按预期运行。
4. 新增代码有中文注释，风格与周边代码一致。

## 安全边界（务必遵守）

- **不执行任何 git commit / push / reset**，改动全部留在工作区。当前工作区有大量未提交的
  他人改动，严禁回退或格式化无关文件。
- 不引入第三方 Go 依赖，不使用 cgo，不联网安装任何东西。
- 只改与本任务相关的文件：lexer/ parser/ ast/（如需 AST 节点）stdlib/ 及对应 *_test.go、test/testdata/。

## 交付物

完成后在仓库根目录写 `IDLE_TASK_REPORT_P1.md`：列出每个改动的文件与意图、
`gx/solid` 的暴露方式及理由、测试运行结果原文、遗留问题（如 v1 未做的 JSX 特性）。
若中途被卡住超过 3 次尝试，同样写报告说明卡点与已有进展，然后停止。
