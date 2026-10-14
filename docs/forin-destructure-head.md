# for...in 头部的解构绑定与无声明赋值形态

## 背景

`for...in` 头部长期只认**单个标识符绑定**，两张规范允许的头部形态都不支持：

| 形态 | 源码 | 改造前 |
|---|---|---|
| 解构绑定 | `for (const [a, b] in obj) {}` | parser 硬拒（见下） |
| 无声明赋值 | `for (x in obj) {}` / `for ([a, b] in o) {}` | parser 报 `no prefix parse function for RPAREN` |

第一类在 parser 里有一条**显式**的拒绝分支（`destructuring binding in for...in is
not supported, use for...of or bind a single key variable`），是第一类缺口被
"看到过但没做"的证据；第二类则是静默不支持 —— 节点形状与 for-of 完全对称，
但只有 for-of 侧实现了。

> 与 `rLGyHa`（模块解构预登记）、`r4McL4`（var 解构作用域）**同族但不同源**：
> 那两单修的是解构名的**登记/提升落点**，本单修的是 for-in 头部**压根解析不出来**。
> 三者合起来才是 for-in/解构这条链的完整覆盖。

## 语义要点（node v22 逐字节对照）

### 1. 解构的是**键字符串本身**，不是 `obj[k]`

`for...in` 每轮产出的值是**键**。所以解构/绑定的对象就是这个键字符串：

```js
for (const [a, b] in { xy: 1 }) { a === "x"; b === "y"; }
for (const { length } in { aa: 1, bbb: 2 }) { /* 2, 3 —— 是键串长度, 不是 obj.length */ }
```

这一点最容易做错（顺手写成 `obj[k]`），测试里专门钉了一条"用值反证"的用例：
`for (const [c] in { abc: 999 })` 的 `c` 必须是 `"a"` 而不是 `"9"`。

### 2. `let` / `const` 每轮新建，`var` 与赋值形态共享

与 `r4McL4` 的结论一致，迭代边界的单实例/每轮新建切分对 for-in 同样成立：

```js
var fns = [];
for (let [z] in { x1: 1, y2: 2 }) fns.push(() => z);   // "x","y" —— 每轮新建
for (var [z] in { x1: 1, y2: 2 }) fns.push(() => z);   // "y","y" —— 共享同一槽
var gg; for ([gg] in { x1:1, y2:2 }) fns2.push(() => gg); // 同样 "y","y"
```

### 3. `var` 解构名仍需提升到函数作用域层

循环外可见、声明前读取得 `undefined`（不是 `ReferenceError`）—— 与 `r4McL4`
对 `for (var [p] of …)` 的要求一致，本单在 for-in 侧的 `collectVarBindings`
补齐了同一条提升。

## 改动

### AST（`ast/ast.go`）

`ForInStatement` 新增两个表达式字段，与 `ForOfStatement` **逐字段对齐**：

```go
type ForInStatement struct {
    Token    lexer.Token
    VarDecl  Statement   // let k / const k / var k
    Variable *Identifier // 简单声明形态的迭代变量名; 其余为 nil
    Pattern  Expression  // 解构形态 (声明 or 赋值) 的模式; 其余为 nil
    Target   Expression  // 无声明且非解构的赋值目标 (标识符/成员访问); 其余为 nil
    Iterable Expression
    Body     *BlockStatement
}
```

三类头部形态的字段占用（与 for-of 同口径）：

| 头部 | VarDecl | Variable | Pattern | Target |
|---|---|---|---|---|
| `for (let k in o)` | LetStatement | k | nil | nil |
| `for (const [a,b] in o)` | ConstStatement（合成名空壳） | nil | ArrayPattern | nil |
| `for (x in o)` | nil | nil | nil | Identifier |
| `for ([a,b] in o)` | nil | nil | ArrayPattern | nil |

> 解构**赋值**形态（`for ([a,b] in o)`）与 for-of 一样放 `Pattern` 而非 `Target`，
> 由 `VarDecl == nil` 表达"赋值而非声明"—— 单一判据，避免两处口径漂移。

`String()` 相应按 Pattern / Target 还原源码形状（`Pattern != nil && VarDecl != nil`
时还要补回 `let`/`const`/`var` 关键字，否则会打印出合成名 `__destructure__`）。

### Parser（`parser/parser.go`）

1. **删除** 硬拒分支，for-in 与 for-of 统一走 `parseForInStatement` /
   `parseForInLHS`。
2. `parseForInStatement` 支持 `[` / `{` 解构头部，复用 `parseDestructuringPattern`，
   与 `parseForOfStatement` 同构。
3. 新增 **`isForInLHS`** 判定无声明 for-in 头部。与 `isForOfLHS` 同构，但两处不同：
   - 认 `IN` 而非 `OF`；
   - 额外要求**深度 0 处没有 `;`**（有分号即传统三段式，提前返回 false）。
4. 新增 **`parseForInLHS`**，与 `parseForOfLHS` 同构。
5. 新增 Parser 字段 **`noIn`**：解析无声明 for-in 的**目标表达式**期间置位。
   此时 `parseExpression` 的中缀循环把 `IN` 当作不可接续的运算符，于是
   `for (x in obj)` 的目标只解析出 `x`，`in` 留给头部处理。

> `noIn` 对应急规范里的 `Expression[~In]`。不这么做的话 `parseExpression`
> 会把 `x in obj` 整个吃成一个 `BinaryExpression`，头部就再也看不到 `in` 了 ——
> 这正是改造中途实测到的那两个 "invalid left-hand side in for...in loop"。
>
> 判据与 node 一致：node 对 `for (i = "x" in a; ...)` 报的是
> `Invalid left-hand side in for-loop`，说明它同样把深度 0 的 `in` 优先解释为
> for-in 关键字（于是 `i = "x"` 成了非法左值），而不是当成 `in` 运算符。

`noIn` 的生效范围严格限制在目标表达式内（进入前存旧值、解析后立即恢复），
用 `defer` 亦可；嵌套 for-in 的目标里再出现 for-in 时旧值会被正确保存/恢复。

### Early errors（`parser/block_early_errors.go`）

新增 `forInHeadLexicalNames`，与 `forOfHeadLexicalNames` 同构：把 `Pattern` 里
的真实绑定名补进 for-in 头部的词法名集合，供
`for (const [a] in o) { let a; }` 这类同层重复声明早错使用。

> `forHeadVarNames`（块内 var 名收集）保持不含解构名 —— 与 for-of 的既有口径
> 一致，不引入两形态间的不对称。`let k; for (var [k] in o) {}` 的冲突由
> `compiler/scope.go` 的 `collectVarBindings` 路径报出（实测与 node 一致）。

### Compiler（`compiler/loops.go` / `compiler/scope.go`）

`compileForInStatement` 的头部绑定段重写为**四条路径**，与 for-of 逐条同构：

| 头部 | 处理 |
|---|---|
| `Pattern != nil` | `compilePatternBind`，kind 按 `VarDecl` 三态分流（nil→`bindAssign`、var→`bindVar`、其余→`bindLexical`） |
| `Target != nil` | `compileForOfTargetAssign`（写外部已有绑定） |
| `var x` | 函数作用域层共享槽位（原逻辑不变） |
| `let/const x` | 每轮新建块作用域槽位（原逻辑不变） |

`compiler/scope.go` 的 `collectVarBindings` for-in 分支补上
`collectBindingNames(node.Pattern)` —— var 解构名的提升落点（`r4McL4` 在
for-of 侧的同一条）。

## 验证

### node v22 逐字节对照

**九形态对照**（声明解构 const / 声明简单 / 无声明标识符 / 无声明解构 /
无声明成员访问 / var 简单 / var 解构 / let 每轮新建 / 无声明解构跨轮共享）：
Gox 与 node 输出**逐行完全一致**。

**test262 风格 10 例对照**（解构 + 每轮独立 / 解构 + 共享 / 成员访问 /
rest 元素 / 默认值 / 空数组模式 / 空对象模式 / 键顺序 / 无声明解构赋值 /
`for (let {toString: s} in …)`）：输出**逐字节一致**。

### 新增回归测试

`vm/forin_destructure_test.go`（8 个）：

| 测试 | 覆盖 |
|---|---|
| `TestForInDestructure_KeysAreStrings` | 解构的是键字符串（含"用值反证"用例） |
| `TestForInDestructure_LetConstPerIteration` | let/const 每轮新建 + 简单绑定对照 |
| `TestForInDestructure_VarSharesSingleBinding` | var 跨轮共享槽 |
| `TestForInDestructure_VarHoisted` | 循环外可见 / 声明前 undefined / 函数体作用域 |
| `TestForInAssignmentTarget` | 标识符 / 成员 / 数组解构 / 对象解构赋值 + 共享 |
| `TestForInSimpleBinding_StillWorks` | 简单绑定回归护栏（含空对象） |
| `TestForInDestructure_BreakContinue` | break / continue 在解构头部下 |
| `TestForInDestructure_NestedPattern` | 嵌套模式 + 默认值 |

`parser/parser_test.go` 的 `TestForHeaderBindingKeywordBoundary` 从"断言拒绝"
改为"断言接受并核对 AST 字段"（4 种声明解构 + 4 种赋值形态）。

### 全量闸门

- `go build ./...` / `go vet ./...` 干净；
- `go test -count=1 ./...` **25 个包全通过，0 失败**；
- `python3 scripts/check-registries.py` → 全部 6 项通过；
- `python3 scripts/check-imports.py` → OK；
- `go test ./tools/initpurity/` → ok；
- `go test ./cmd/gox/ -run TestLint` → ok；
- `go run ./cmd/gox lint apps` → ok（64 文件无命中）。

> test262 套件不在本机（`/d/test262` 是另一台机器的路径），故用 node 对照探针
> 替代多套件 A/B；受影响形态（for-in 头部解构绑定）在 language suite 中的
> 覆盖本就稀疏，探针 + 回归测试是更贴近的保障。

## 已知未覆盖 / 存疑

- `for (var [s] in …)` 与 `for (let [s] in …)` 的**早错细节**（如解构名与
  形参同名、与 catch 参数同名）沿用既有路径，未逐一补测。
- `for...in` 的 **`using` 声明**仍是早错（`for-in loop variable declaration
  may not use 'using'`），与 node 一致，仍按原样保留。
- `for...of` 侧的 `forHeadVarNames` 与本侧同样不含解构名 —— 两形态**对称地**
  保留该口径，若将来要收紧需一并处理。
