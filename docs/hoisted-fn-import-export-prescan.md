# 提升编译的函数体看不见 import 绑定 / export function (P0)

## 症状

模块里"先写调用方、后写被调方"这种最自然的写法，在**被提升编译的函数体**里
解析不到同级绑定，运行期抛 `ReferenceError` —— 而那个名字就在同一文件里写着。

```js
// store.js
import { createSignal } from "gox";
import { FORMS } from "./forms.js";

// app.js
import { FORMS } from "./struct.js";
function FormBar() {
  const buttons = FORMS.map(...);   // ReferenceError: FORMS is not defined
}
```

```js
// 同一个文件内也是一样
export function boot() { return apply(); }
export function apply() { return 42; }
// ReferenceError: apply is not defined
```

真实触发（`apps/foldable-demo` 挂载即炸）：

```
vm error: ReferenceError: FORMS is not defined
    --> apps/foldable-demo/src/app.js:194:3
 194 |   const buttons = FORMS.map((f) =>
```

## 根因

与 `rLGyHa`（模块下解构声明）**完全同族** —— 同一个 `prescanScope` 的另外两个
漏登记入口。`compiler/compiler.go` 的 `compileStatements` 分三步：

1. `prescanScope(stmts)` 先登记本层的 let / const / using / function 名字；
2. 再把**列表中所有 function 声明提升编译**（编译其函数体）；
3. 最后按源码序编译其余语句。

第 2 步要求"函数体能解析到本层后面才出现的绑定"，靠的就是第 1 步的预登记。
`prescanScope` 少登记谁，谁就在函数体里退化成 `emitGlobalLoad` → 运行期
`ReferenceError`。

原实现只处理了 `LetStatement` / `ConstStatement` / `UsingStatement` /
`FunctionDeclaration` 四个 case，缺两个：

| 缺陷 | 缺失的 case | 表现 |
|---|---|---|
| **A** | `ImportDeclaration` | 提升函数体内读 import 绑定 → `ReferenceError` |
| **B** | `ExportDeclaration` | 提升函数体内读同级 `export function` / `export const` → `ReferenceError` |

缺陷 B 尤其隐蔽：`export function f(){}` 在 AST 里是 `ExportDeclaration`
**包着** `FunctionDeclaration`，所以 `FunctionDeclaration` 那个 case 根本看不到它。

### 为什么长期没被发现

三条"侥幸不中"的边界，恰好避开了绝大多数代码：

| 形态 | 是否中招 | 原因 |
|---|---|---|
| **脚本入口**顶层 | 否 | 全局作用域绑定落共享全局环境，按名查全局侥幸可用 |
| 函数**表达式 / 箭头** | 否 | 按源码序编译，不参与提升 |
| 被引用的绑定写在**函数声明之前** | 否 | 解析时已登记 |
| **模块**顶层 + 函数声明 + 后置绑定 | **是** | 模块顶层是局部槽位，必须靠预登记 |

**独立验证**（最小复现，全部走文件模块）：

```js
// dep.js
export const FORMS = [1, 2, 3];
export function applyForm(k) { /* … */ }

// w.js —— 2 个函数声明 + 从 dep.js import
import { FORMS } from "./dep.js";
function A1() { return FORMS.length; }   // ReferenceError
function B1() { return 1; }
export { A1, B1 };
```

对照实验：把 `function A1(){}` 改成 `const A1 = () => …` 或
`const A1 = function(){}` → **正常**。把 `function B1(){}` 删掉 → **正常**。
说明与"函数声明被提升编译"强相关，不是 import 本身的问题。

## 修法（最小改动，仅 `prescanScope` 两处）

### A. `ImportDeclaration` 按本地名预登记

```go
case *ast.ImportDeclaration:
    if c.isGlobalScope() {
        continue   // 全局层不能预登记，见下
    }
    for _, item := range s.NamedImports { /* prescanDeclare(item.Local, true, false) */ }
    if s.DefaultName != "" { /* … */ }
    if s.Namespace != "" { /* … */ }
```

**必须排除全局作用域**：入口脚本的 import 走 `OP_DECLARE` 写进共享全局环境
（`compileImportDeclaration` 的 `isGlobalScope` 分支）。若在全局层也预登记，
后续的 `declareOnce` 会认领到同一符号从而改走 `OP_STORE` 局部槽位，破坏脚本
"顶层绑定落全局环境"的语义。已由 `TestGlobalEntryImportStillGlobalWritable` 钉住。

### B. `ExportDeclaration` 拆包后递归预登记

```go
case *ast.ExportDeclaration:
    if s.IsDefault || s.Declaration == nil {
        continue   // export {a} / export … from 不引入新绑定
    }
    if err := c.prescanScope([]ast.Statement{s.Declaration}); err != nil {
        return err
    }
```

复用 `prescanScope` 自身处理内层声明（`export function` → `FunctionDeclaration`
分支，`export const` → `ConstStatement` 分支），不重复逻辑。

## 验证

### 单元测试

新增 `vm/hoisted_fn_import_binding_test.go`（9 条）。基线（`b5d3ddd`）上
**6 条失败**，修后全绿：

| 测试 | 基线 | 修复后 |
|---|---|---|
| TestHoistedFnSeesImportedBinding | FAIL (FORMS) | PASS |
| TestHoistedFnSeesImportedFunction | FAIL (double) | PASS |
| TestHoistedFnSeesImportedDefaultAndNamespace | FAIL (d) | PASS |
| TestHoistedFnSeesExportFunction | FAIL (apply) | PASS |
| TestHoistedFnSeesExportConst | FAIL (LIMIT) | PASS |
| TestExportFunctionMutualRecursion | FAIL (isOdd) | PASS |
| TestGlobalEntryImportStillGlobalWritable | PASS | PASS |
| TestShadowingLocalStillWins | PASS | PASS |
| TestImportRedeclareStillSyntaxError | PASS | PASS |

后三条是**反向守卫**：分别保证全局层不被误改、遮蔽语义不变、重复声明仍报
`SyntaxError`（预登记引入了新的重复检查路径，必须确认没把早错吞掉）。

### 应用级

`apps/foldable-demo` 挂载从 `ReferenceError` 变为成功；`gfx/apps_smoke_test.go`
的 `appSuite` 从 10 项扩到 11 项，`TestAppsSuiteSmoke` 全绿。

### 构建 / 静态检查

- `go build ./...` / `go vet ./...` 干净；
- `go test -count=1 ./...` **25 个包全通过，0 失败**；
- `python3 scripts/check-registries.py` → 全部 6 项通过；
- `python3 scripts/check-imports.py` → OK。

## 顺带修掉的测试串味

把 `foldable-demo` 加入 `appSuite` 后，`gfx` 包出现 4 个与本缺陷无关的失败：

```
--- FAIL: TestDefaultFontSizeScalesWithDisplay   (3x 屏上应为 48, got 16)
--- FAIL: TestDensityScaleTwoDoublesExplicitSizes (2x 屏系数应为 2, got 1)
--- FAIL: TestDensityScaleFollowsWindowAcrossScreens
--- FAIL: TestDensitySafeAreaStyleIsDp
```

根因是**用例串味**：`foldable-demo` 会调 `gx/screen` 的 `reportPosture` 往宿主上报
一块"折叠屏"，而上报是**整表覆盖**且会一直留着（`screenOverride`），于是后面按真实屏
断言的密度/DPI 用例读到了上一轮塞进来的假屏。

修法：`TestAppsSuiteSmoke` 的每个子用例前后调 `ResetDisplaysForTest()`
（等价于 JS 侧 `resetDisplays()`）。单跑那 4 个用例一直是绿的，
只有"跑完 apps 套件再跑"才红 —— 典型的状态泄漏，不是密度逻辑的问题。

## 已知未覆盖 / 存疑

- 与 `rLGyHa` 同族的 **`var` 解构** 缺陷（走 `compileVarStatement` 独立路径）
  仍未修，本次同样未涉及。
  > **后续**：已由 `r4McL4`（`docs/r4McL4-var-destructure-scope.md`）修复 ——
  > 引入三态 `bindKind` 把 var 解构的登记落点改到函数作用域层，并补上提升。
  > for-in 侧的同一条由 `docs/forin-destructure-head.md` 补齐。
- `export class C extends B {}` 里父类名若来自后置的 `export const`，理论上
  走的是同一套预登记，但 `emitSuperLoad` 另有 `moduleMode` 分支兜底；本次未
  专门补测，留待有实际触发时再评估。
