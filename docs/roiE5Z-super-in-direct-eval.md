# roiE5Z: 直接 eval 内 `super.x` 误报 SyntaxError —— 根因、落地与边界

看板单: **roiE5Z [eval] eval 代码编成全局脚本 ⇒ super.x 在 eval 里被误报 SyntaxError**
基线: `f1a46fb` · 分支: `wt/eval-super`

---

## 1. 问题

test262 `direct-eval-*-contains-superproperty-*.js` 一族断言**不抛**（`super.x` 合法），
Gox 报 `SyntaxError: eval: invalid source (compiler: super property access outside class)`。

规范口径 (sec-performeval 18.2.1.1.1 / 18.2.1.1.2)：

| 调用点语境 | eval 源码含 `super.x` (SuperProperty) | 含 `super()` (SuperCall) |
|---|---|---|
| 类方法 / 对象方法 / 字段初始化器 / 静态块（有 [[HomeObject]]） | **合法**，按 home 解析 | SyntaxError |
| 全局代码 / 无 home 的普通函数 / 箭头函数 | SyntaxError | SyntaxError |
| 间接 eval（任何语境） | SyntaxError（按全局 eval） | SyntaxError |

## 2. 根因（逐环探针）

探针（对基线二进制 `gox-roi-base.exe`，源码 `F:/tmp/probe-roi/`）：

```
p1 class C extends A { m(){ eval('super.x') } }   → SyntaxError (compiler: super property access outside class)   ← BUG
p2 function f(){ eval('super.x;') }               → THREW SyntaxError    ✓ 正确
p3 var o={method(){ eval('super.test262;') }}     → SyntaxError           ← BUG
p4 方法内 eval('super(1);')                       → THREW SyntaxError    ✓ 正确（必须保持）
p5 顶层 eval('super.property;')                   → THREW SyntaxError    ✓ 正确
p6 箭头 eval('super.property;')                   → THREW SyntaxError    ✓ 正确
p7 static 方法 eval('super.sx')                   → SyntaxError           ← BUG
p8 字段初始化器 eval('super.x')                    → SyntaxError           ← BUG
```

错误链条（三层定位）：

1. **parser 层无错**：`parseSuperExpression`（parser.go:4236）只要求 `super` 后跟
   `(` 或 `.`，`super.x` 正常过。
2. **compiler 层早错**：eval 源码被 stdlib 包成 `(function(){ ... })`（stdlib/eval.go
   的 `runGlobalEval`），经编译桥 `compileForBridge` 编成**全局脚本单元**。该单元
   `c.currentSuperClass == ""` → `compiler.go` 的 super 属性访问分支报
   `super property access outside class`。
3. **不存在运行时 home 通道**：全仓无 `[[HomeObject]]` 概念（grep `homeObject` 零命中）。
   Gox 的 `super.x` 是**编译期静态名**代码生成：`LOAD_GLOBAL(父类名).prototype.x`
   （`emitSuperLoad`），连 `C.prototype.__proto__` 都不走（Gox 未实现 `__proto__`
   访问器，`o.__proto__` 得 undefined）。因此「外层方法有 home」这一信息在 eval 单元
   编译期完全丢失 —— 这是本单的深层困难：eval 编译期拿不到外层函数的 home object。

本质：这不是某一层的 bug，而是 **Gox 的 super 静态名模型 + eval 独立编译单元模型
之间的结构性缺口**。最小修复 = 把「调用点的 home 上下文」随直接 eval 的既有标记机制
一起带出（与 this / new.target 两桥同构）。

## 3. 落地设计（已实现，outcome 1）

沿用 this / new.target 两桥的成熟模式（编译器发射标记 → VM 消费并置桥 → stdlib 经
object 桥取用 → 编译选项进入 eval 单元），新增 **super home 桥**：

```
编译器 (compileCallExpression/emitEvalMarks)
  evalHome 上下文非空 (类方法/字段初始化器/静态语境/对象方法) 时:
    OP_EVAL_MARK_HOME        operand=home 类名        (实例语境)
    OP_EVAL_MARK_HOME_STATIC operand=home 类名        (静态语境)
    [+ OP_EVAL_MARK_SUPER    operand=父类名]           (静态且 extends)
    OP_EVAL_MARK_HOME_THIS   (对象字面量方法, 无名字)
  无 home 语境: 仍发 OP_EVAL_MARK / _INIT (语义即"清空 home")
        │
VM (consumeEvalMark)
  takePendingEvalHome() → object.SetDirectEvalSuperHome(EvalSuperHome{...})
        │
stdlib (eval.go)
  TakeDirectEvalSuperHome() → runGlobalEval → object.CompileSourceWithOpts(body,
      EvalCompileOptions{AllowNewTarget, SuperHome})
        │
编译器 (SetEvalSuperHome)
  eval 单元内 SuperProperty 合法, base 按 home 解析:
    实例:  LOAD_GLOBAL(<类>) .prototype → OP_GET_PROTO → prop
    静态+extends: LOAD_GLOBAL(<父类>) → prop
    静态基类:  LOAD_GLOBAL<类> → OP_GET_PROTO → prop  (= Function.prototype)
    ThisHome: OP_THIS → OP_GET_PROTO → prop
  SuperCall (super()) / super.method 之外的一切 super 调用形态恒拦
  (eval 单元 currentSuperClass 恒 "", 原分支报错)
```

关键决策：

- **新增 `OP_GET_PROTO` 指令**（栈 `[obj] → [proto]`）：Gox 无 `__proto__` 访问器，
  base 必须经指令取原型。
- **home 靠 `LOAD_GLOBAL(类名)` 解析**，与 Gox 既有 super 静态名模型同边界：
  顶层 class / var 赋值的类表达式可解析；函数内局部类名运行期 `ReferenceError`
  （旧行为 SyntaxError，均为报错，可观察差异仅错误类型）。
- **对象字面量方法的 home 取 this**（规范 [[HomeObject]] 无名字可加载）：
  `o.m()` 直接调用正确；`o.m.call(x)` 读错宿主（规范仍读 o）。`super-prop-method.js`
  正是直接调用形态。
- **嵌套普通函数沿用外层 home**（规范应断）：与 Gox 既有 super 行为一致
  （非 eval 的 `super.x` 嵌套函数也如此），未额外收紧。
- `super.method()`（SuperProperty 上的调用）在 home 语境下**放行**（规范合法，
  复用同一 base 加载）；`super()`（SuperCall）恒 SyntaxError。

## 4. 改了什么

| 文件 | 改动 |
|---|---|
| `bytecode/opcode.go` | +4 指令：`OP_EVAL_MARK_HOME(0x6D)` / `_STATIC(0x6E)` / `_THIS(0x6F)` / `_SUPER(0x56)`；+`OP_GET_PROTO(0xF2)` |
| `compiler/compiler.go` | `evalHomeCtx` / `evalSuperHomeCtx` + `SetEvalSuperHome`；类体/静态元素/对象字面量方法设置 home 上下文；`emitEvalMarks` 发射 home 标记（发了 home 就不发普通 MARK）；eval 单元 super 代码生成 `emitEvalSuperBase/Property`；`super.method()` 分支支持 home |
| `object/compilehook.go` | `EvalSuperHome` 结构 + `Set/TakeDirectEvalSuperHome` 桥；`EvalCompileOptions` + `CompileSourceWithOpts`（super home × new.target 组合） |
| `vm/compile.go` | `compileForBridgeWithOpts` 注册 opts 桥；`compileSourceOpts` 增 superHome 参数 |
| `vm/vm.go` | pending home 状态 + 4 条标记的 case + `clear/takePendingEvalHome`、`markName`；`consumeEvalMark` 置桥；`OP_GET_PROTO` case |
| `stdlib/eval.go` | `runGlobalEval` 取 super home，三选一编译桥（opts / NT / 普通） |
| `vm/eval_super_home_test.go` | 新增 10 个回归测试（类方法/静态/对象方法/字段初始化器/箭头继承/无 home 负例/super() 早错/super.method()/多语句/new.target 组合） |

## 5. 验收结果

- `go test ./parser/... ./compiler/... ./vm/...`：全绿（`go test ./...` 亦全绿）。
- 探针（候选二进制）：p1 → `["before",42,"after"]`；p3 → `first undefined / second 262`；
  p7 → `static 7`；p8 → `executed true`；看板字面探针
  `class C { m(){ return eval("super.x") } }` → `undefined`（不再 SyntaxError）；
  `eval("super()")` 仍 `THREW SyntaxError`；p2/p5/p6 负例保持 SyntaxError。
- test262 定向 A/B（`python F:/tmp/abd.py base cand`）：

| filter | GAIN | LOST |
|---|---|---|
| `eval-code` | 1（`super-prop-method.js`） | **0** |
| `expressions/class/elements` | 4 | **0** |
| `statements/class/elements` | 4 | **0** |
| `expressions/super` | 2（`prop-dot-cls/obj-val-from-eval.js`） | **0** |

  合计 **GAIN 11 / LOST 0**。看板单一族
  `derived/private/nested/nested-private-derived-cls-direct-eval-contains-superproperty-1.js`
  （expressions 与 statements 两处）全部转绿。

## 6. 遗留边界（未做，非本单范围）

1. **`super[expr]` 计算形式**：解析器只认 `super.` / `super(`（既有缺口，与 eval 无关），
   故 `*-contains-superproperty-2.js` 族仍失败。修法：parser 允许 `super[` 前缀。
2. **非 eval 的 super 缺口依旧**：基类方法 `class A { m(){ super.toString } }`、对象
   字面量方法、基类字段初始化器里的 super.x 仍编译错（探针 p9–p13）
   —— 这些不走 eval 标记，需要把 `evalHome` 机制推广为普通 super 代码生成
   （即运行时/静态 home 模型统一），属独立单。
3. **静态 super 非 eval 路径仍错**：`class C extends A { static m(){ super.sm() } }`
   编译成 `LOAD_GLOBAL(A).prototype.sm`（多跳了 prototype，应为 A 本身），Gox 未链接
   `ctor.__proto__`。eval 侧已按规范生成（`LOAD_GLOBAL(A).sm`），两侧不一致，留待统一。
4. **局部类名**：函数内 `class C extends A { m(){ eval('super.x') } }` 运行期
   `ReferenceError`（旧为 SyntaxError）。
5. **多语句 eval 完成值**：Gox 对多语句 eval 恒返回 undefined（既有），故
   `x = eval('...; () => super.x')` 这类依赖完成值的形态仍不可用。
6. **`eval(...["super.x"])` 展开形式**：Gox 按直接 eval 处理（Node 按间接），home 桥
   随之生效 —— 与既有分歧 4 同源，未处理。

## 7. 结论

本单按 **outcome 1（实现）** 闭环：根因是 eval 独立编译单元丢失调用点 home 上下文，
以「OP_EVAL_MARK 家族的 home 标记 → VM 桥 → 编译选项」的最小改造补上该通道，
SuperProperty 在「外层是方法」时合法且求值正确，SuperCall 仍 SyntaxError，
定向 A/B 全绿（GAIN 11 / LOST 0）。剩余 6 项边界均为 Gox super 静态名模型的
既有缺口或独立特性，见上。
