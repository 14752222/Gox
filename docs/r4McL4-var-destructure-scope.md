# r4McL4：`var` 解构的绑定落点必须在**函数作用域层**

> 单：`r4McL4`（var 解构作用域）
> 修复提交：`d3ccb5d`（`compiler/compiler.go` + `ast/pattern_names.go` + `vm/var_destructure_scope_test.go`）

## 一、症状

`var` 解构声明的绑定，**只要需要从声明所在的块/循环之外读取，就抛 `ReferenceError`**。
同作用域内（声明与读取在同一块）反而正常 —— 这个"半好半坏"的形状是定位的起点。

对照实测（node v22 vs 修复前的 `Gox/main` = `438b29a`）：

| 用例 | node v22 | 修复前 Gox |
|---|---|---|
| `for (var [p] of [[1],[2],[3]]) {}` 后读 `p` | `3` | **`ReferenceError`** |
| `for (var {q} of [{q:7}]) {}` 后读 `q` | `7` | **`ReferenceError`** |
| `{ var [r] = [9]; }` 后读 `r` | `9` | **`ReferenceError`** |
| `for (var [t] = [5]; false;) {}` 后读 `t` | `5` | **`ReferenceError`** |
| `for (var [a,[b]] of [[1,[2]]]) {}` 后读 `a`/`b` | `1,2` | **`ReferenceError`** |
| `for (var [c] of [[3]]) { var [d] = [4]; }` 后读 `c`/`d` | `3,4` | **`ReferenceError`** |
| `var [u] = [1], v = 2;` 读 `u`/`v`（**同作用域**） | `1,2` | `1,2` ✅ |

## 二、根因

`var` 解构**借用了 `let` 的解构编译路径**。`compileDestructureAssignment` 的第二参
`isDecl bool` 只能表达两态（赋值 / 声明），`let`、`const`、`var` 三种声明全传 `true`：

```go
// compileVarStatement（修复前）
if err := c.compileDestructureAssignment(assign, true); err != nil { … }
// compileLetStatement / compileConstStatement 同样是 (assign, true)
```

而 `bindPatternTarget` 在 `isDecl == true` 时统一走 **`c.declareOnce(...)`** —— 即
**当前（块）作用域**的登记：

```go
case *ast.Identifier:
    if isDecl {
        sym, err := c.declareOnce(t.Value, false, false)   // ← let 口径：登记进当前块
```

于是 `var [p] of …` 的 `p` 落在**循环体的块作用域**里，块退出即消失；块外读 `p`
在非全局作用域解析不到符号 ⇒ 退化成 `OP_LOAD_GLOBAL` ⇒ 运行期 `ReferenceError`。
同作用域用例能过，只是因为声明与读取共享同一个块。

另外两条同根缺口：

1. **`for`-of 的 `Pattern` 分支绕过了 var 处理**。`compileForOfStatement` 里
   `if stmt.Pattern != nil` 分支直接 `compilePatternBind(stmt.Pattern, stmt.VarDecl != nil)`，
   而紧邻的 `else if` 分支才是正确按 `*ast.VarStatement` 落 `FuncLayer` 的那个 ——
   `for (var [p] of …)` 被前一个分支截胡，永远走不到它。
2. **提升缺失**。`collectVarBindingsStmt` 显式跳过解构声明项
   （`node.Name.Value != destructureSyntheticName`），解构 `var` 的真实绑定名
   既不参与提升、也不在函数编译开始就分配槽位。后果是槽位在**块/循环内部**
   才分配（晚于循环记录的 `sealFrom`），会被 `OP_ITER_BOUNDARY` 当作"每轮新建的
   词法绑定"克隆，破坏 `var` 的单实例语义。

规范上（ES2015+ `ForDeclaration` / `VariableStatement`）：`var` 绑定创建在
**VariableEnvironment**（函数/全局作用域），块只影响可见性的书写位置，不影响绑定落点；
`for (var …)` 各轮迭代共享同一个绑定。

## 三、修法

### 3.1 引入三态 `bindKind`（解构编译里唯一的语义分叉点）

```go
type bindKind int

const (
	bindAssign  bindKind = iota // [a] = x            赋值解构：写已有绑定
	bindLexical                 // let/const [a] = x  声明解构：登记在当前块
	bindVar                     // var [a] = x        声明解构：登记在函数作用域层
)
```

`isDecl bool` 全量替换为 `kind bindKind`（7 个函数签名 + 全部透传点），
三处调用方按语义分流：

| 调用点 | kind |
|---|---|
| `compileLetStatement` / `compileConstStatement`（首项 + `More`） | `bindLexical` |
| `compileVarStatement`（首项 + `More`） | **`bindVar`** |
| `compileAssignmentExpression` 的解构赋值 | `bindAssign` |
| `for`-of / `for await`-of 的 `Pattern`：`VarDecl == nil` | `bindAssign` |
| 同上：`*ast.VarStatement` / 其余（let/const） | `bindVar` / `bindLexical` |

> `for ([a, b] of xs)` 是**赋值**形态（`VarDecl` 为 `nil`），必须落 `bindAssign` ——
> 这处在实现时被漏过一次（误判成 `bindLexical`），由既有测试
> `TestForOfLHSAssignment`（期望 `a*100+b == 908`，错则得 `102`）当场拦下。

### 3.2 把 var 的登记口径提为方法，供解构路径复用

`compileVarStatement` 里原有的 `declareVar` / `emitAssign` 两个闭包提取为
`(*Compiler).declareFuncLayerVar` / `(*Compiler).emitVarAssign`，`bindPatternTarget`
的 `bindVar` 分支直接复用 —— 消除"同一语义两份实现"的漂移风险。

### 3.3 补齐提升

新增 `collectVarDeclaratorNames(name, value, declare)`：解构声明项（`Name` 是合成名
`__destructure__`）用 `ast.PatternBoundNames(value)` 取模式里的真实绑定名，
逐个登记提升；`collectVarBindingsStmt` 的 `VarStatement` / `ForOfStatement`
两个分支改走它。

同时修 `ast.PatternBoundNames` 漏收 `ObjectPattern.RestTarget` 的问题 ——
否则 `var {x, ...r} = obj` 的 `r` 不会被提升。

## 四、验证

### 4.1 node v22 对照探针（21 例全对齐）

`for (var [p] of …)` 循环外读 `p` = `3`；闭包共享槽 `3,3,3`（vs `let` 的 `1,2,3`）；
声明前读取得 `undefined`（提升）；`var {x, ...r}` rest 可见；
`var [d = 5] = []` 默认值生效；`var [a] = [1]; var a = 2;` 复用绑定；
简单参数与 `var [x]` 同名复用；`var` 与 `let` 同层同名**仍** `SyntaxError`（两个方向）。

### 4.2 新增回归护栏

`vm/var_destructure_scope_test.go`（9 个测试）：块外可见、for-of 循环外可见、
提升 undefined、跨轮共享槽、rest/默认值、重复声明合法、let/const 冲突报错、
`let` 解构仍每轮新建（对照组）、`for ([a,b] of xs)` 赋值形态仍生效（回归护栏）。

### 4.3 全量 A/B

**基线 = 修复前的 `Gox/main` = `438b29a`**（`remote-main.json`，身份经逐用例 diff 多轮验证）：

- 全量 `language` **23726** 例：`18126（76.3972%）` → **`18127（76.4014%）`**；
- 逐用例 diff：`only_a = 0` / `only_b = 0` / **`pass_differs = 1`（GAIN 1 / LOST 0）**；
- 唯一翻转：**`language/statements/for-of/head-var-bound-names-dup.js`**（GAIN）；
- `go test ./...` 20 个包全绿。

> 计分范围内只命中 1 例（`for-of` 头部的 `var` 绑定名重复）；症状表里的
> `{ var [r] = 9; }`、`for (var [t] = 5; …)` 等形态 test262 `language` 未覆盖，
> 其价值由 node v22 对照探针（21 例）与新增回归测试（9 例）保证。

## 五、未覆盖（另立单）

`for (var [s] in enum)` —— `ForInStatement` 的 AST 里**根本没有 `Pattern` 字段**
（只有 `Variable *Identifier`），parser 直接报
`destructuring binding in for...in is not supported`。这是 **parser 层**的显式缺口，
不在本次 `compiler/` 修复范围内。
