# rLGyHa — 模块作用域下解构声明的绑定在函数体内不可见 (P0)

## 症状

模块顶层解构声明产生的绑定，在**同模块内的函数**里不可见：

```js
// mod3.js
const z = 9;
const [x, y] = [1, 2];            // 顶层直接读 x / lx / oa 都正常
let [lx, ly] = [3, 4];
const { oa, ob } = { oa: 5, ob: 6 };

function fPlain() { return z; }    // 正常（普通 const）
function fDestr() { return x; }    // ReferenceError: x is not defined
function fLetDestr() { return lx; } // ReferenceError: lx is not defined
function fObjDestr() { return oa; } // ReferenceError: oa is not defined
export { fPlain, fDestr, fLetDestr, fObjDestr };
```

关键边界：

| 声明形态 | 顶层直接读 | 提升的函数内读 |
|---|---|---|
| `const z = 9`（普通） | 正常 | 正常 |
| `const [x, y] = …` | 正常 | **ReferenceError** |
| `let [lx, ly] = …` | 正常 | **ReferenceError** |
| `const { oa } = …` | 正常 | **ReferenceError** |

与 `createSignal` / 内建 / `const` 均无关（`let` 与对象解构同样中）。

## 根因

**不是模块专有**。真正的边界是「**非全局作用域**里，列表顶部被提升编译的函数体
在解构声明的名字被登记之前就要解析该名字」。

链路（`compiler/compiler.go`）：

1. `compileStatements` 分两步（`compiler.go:556-584`）：**先把列表里所有函数声明
   提升并编译其函数体**，再按源码序编译其余语句。
2. 函数体能解析"列表后面才出现的词法绑定"，靠的是 `prescanScope` 预先把名字登记
   进作用域（`compiler.go:543-546` 的注释即为此而立）。
3. 但 `prescanScope` 对解构声明项只**跳过**合成名 `__destructure__`、**没有登记
   模式里的真实绑定名**（原注释："真正的绑定名在 compilePatternBind 阶段登记"）。
4. 于是 `const [x] = …` 之后、提升编译 `function f(){ return x; }` 时，`x` 尚未
   进入作用域 → `compileIdentifier` 落到 `emitGlobalLoad` 退化路径
   （`compiler.go:4436-4441`）→ 运行期在无该全局属性的作用域抛 `ReferenceError`。

**为什么只在模块里显形**：脚本**顶层**的绑定是全局属性，`emitLoad` 按名查全局
（`sym.Depth == 0 && !moduleMode`，`compiler.go:4366-4372`），侥幸可用；模块顶层的
绑定是**局部槽位**（`isGlobalScope()` 恒 false，`compiler.go:4257`），必须靠预登记
解析，于是失效。

**独立验证**：脚本里把同样的解构 + 提升函数放进**函数体作用域**也复现（与模块无关）：

```js
function outer() {
  const [a, b] = [1, 2];
  function g() { return a + b; }   // ReferenceError: a is not defined
  return g();
}
```

反之，**箭头函数 / 函数表达式不提升**，按源码序在解构之后编译，本来就正常 ——
这进一步坐实"提升编译早于名字登记"就是根因。

## 修法（最小改动，仅 `prescanScope` 一处）

新增 `prescanDeclarator`：普通声明项照旧；解构声明项用现成的
`ast.PatternBoundNames` 取出模式里的真实绑定名，逐个 `prescanDeclare`。
`LetStatement` / `ConstStatement` 的 `Name` / `More` 循环改走该 helper。

解构名**一律按 let 语义（`isConst=false`）预登记**，与既有解构声明路径
（`bindPatternTarget → declareOnce(name,false,false)`）口径一致，不改动可写性边界。

## 验证

### 单元测试

新增 `vm/destructure_scope_binding_test.go`（10 条）：

- 模块顶层：数组解构 const / let、对象解构 const、后置解构 const、普通 const 守卫；
- 函数体作用域：数组 / 对象解构 + 提升函数；
- 反向回归守卫：脚本顶层解构 + 提升函数、箭头函数、解构 let 跨函数赋值。

基线（`b381cbc`）上其中 **7 条失败**（均为 `ReferenceError`），修后全绿。

| 测试 | 基线 | 修复后 |
|---|---|---|
| TestModuleArrayDestructureConstVisibleInHoistedFn | FAIL (x) | PASS |
| TestModuleArrayDestructureLetVisibleInHoistedFn | FAIL (lx) | PASS |
| TestModuleObjectDestructureConstVisibleInHoistedFn | FAIL (oa) | PASS |
| TestModuleDestructureAfterPlainConstVisibleInHoistedFn | FAIL (d1) | PASS |
| TestFnScopeDestructureVisibleInHoistedFn | FAIL (a) | PASS |
| TestFnScopeObjectDestructureVisibleInHoistedFn | FAIL (m) | PASS |
| TestModuleDestructureBindingWritableAcrossFn | FAIL (a) | PASS |
| TestModulePlainConstStillVisibleInHoistedFn | PASS | PASS |
| TestScriptTopLevelDestructureVisibleInHoistedFn | PASS | PASS |
| TestArrowAfterDestructureStillWorks | PASS | PASS |

### A/B（test262 language suite，`-jobs 4`）

```
gox test262 -root /d/test262 -suite language -jobs 4 -json <out.json>
```

| 指标 | 基线 (b381cbc) | 修复后 |
|---|---|---|
| total | 23726 | 23726 |
| passed | 18098 | 18098 |
| failed | 5587 | 5587 |
| skipped | 41 | 41 |

逐用例 diff（按 `path` 比对 `pass` 字段）：

```
only_a = 0   only_b = 0   pass_differs = 0
  gained(False->True) = 0   LOST(True->False) = 0
GAIN = 0
```

零回归（`only_a` / `only_b` / `pass_differs` 三项全 0）；本次修复对 test262
language suite 无增益也无损失（该缺陷形态未被 language suite 覆盖）。

### 构建/静态检查

`go build ./...` / `go vet ./...` 干净；`go test -count=1 ./...` 全包通过
（含 `vm` 8.259s、`compiler` 0.183s、`parser` 0.347s）。

## 已知未覆盖 / 存疑

- **`var` 解构**（`var [a,b] = …`）在非全局作用域下有**同一形态的缺陷**，且
  基线与本修复后**均复现**（`ReferenceError: a is not defined`）。它走的是
  `compileVarStatement` / `declareVar`（函数层登记）那条独立路径，不在
  `prescanScope` 的 let/const 分支里，故本次未动 —— 属本单范围外的相邻缺陷，
  另行评估。
- `using` 声明按规范不可解构，未涉及。
- 解构声明目前仍按 **let 语义**登记（`const [a]=…` 的绑定可写）—— 这是既有
  边界，本次刻意不改（改它会影响可写性语义与 test262 结果，超出本单范围）。
