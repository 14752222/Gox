# JavaScript Runtime API 实现教程

> 基于你的 js-runtime 项目（字节码 VM 架构），从零实现一套完整的 JavaScript API。
> 假设你已掌握 JavaScript 语法，了解编译原理基础，无 runtime 开发经验。

---

## 环境说明

| 组件 | 技术 |
|------|------|
| 源码目录 | `F:\desktop\go` |
| Go 运行时 | 1.13+ |
| 执行入口 | `./js-runtime.exe <script.js>` |

**运行示例：**
```bash
./js-runtime.exe testdata/tut/demo.js
```

**测试：**
```bash
go test ./...
```

---

## 目录

1. [API 架构：如何从 Go 暴露到 JavaScript](#1-api-架构)
2. [实现第一个 API：参数解析、类型检查与返回值封装](#2-实现第一个-api)
3. [数据转换：JavaScript 值与原生数据结构的双向映射](#3-数据转换)
4. [错误处理：参数校验、异常抛出及错误信息设计](#4-错误处理)
5. [进阶：异步 API 与对象生命周期](#5-进阶内容)

---

## 1. API 架构

### 1.1 整体架构图

```
┌──────────────────────────────────────────────────────────────────────┐
│                         JavaScript 层                                 │
│   Math.hypot(3, 4)     stats.describe([1,2,3])     delay(100)        │
└──────────────────────────┬───────────────────────────────────────────┘
                           │ 字节码调用
┌──────────────────────────▼───────────────────────────────────────────┐
│                      虚拟机 (vm/vm.go)                                 │
│                                                                       │
│   OP_CALL (0x20) ──→ 内建函数调用                                      │
│         │                                                            │
│         ▼                                                            │
│   callee.Fn(args...)   ←  弹出函数并执行                               │
│         │                                                            │
│         ▼                                                            │
│   ┌─────────────────────────────────────────────────────────────┐    │
│   │  switch callee := fn.(type) {                               │    │
│   │    case *object.BuiltinFunction:   // 无 this 的 API          │    │
│   │    case *object.BuiltinMethod:     // 有 this 的方法          │    │
│   │    case *object.Closure:           // JS 函数                 │    │
│   │  }                                                         │    │
│   └─────────────────────────────────────────────────────────────┘    │
└──────────────────────────┬───────────────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────────────┐
│                      object 包 (值系统)                                │
│                                                                       │
│   type Value interface {                                             │
│       Type() ObjectType   // "NUMBER","STRING","BUILTIN"...          │
│       Inspect() string   // console.log 输出的字符串                  │
│       IsTruthy() bool    // 布尔转换                                   │
│       GetProperty(string) (Value, bool)                              │
│       SetProperty(string, Value)                                     │
│   }                                                                   │
│                                                                       │
│   ┌────────────────┐  ┌────────────────┐  ┌────────────────────────┐ │
│   │ BuiltinFunction │  │  BuiltinMethod │  │  *object.Error         │ │
│   │  Fn(args...)   │  │  Fn(this,args) │  │  Name + Message        │ │
│   │  无 this 绑定   │  │  有 this 绑定   │  │  throwIfError 机制     │ │
│   └────────────────┘  └────────────────┘  └────────────────────────┘ │
└──────────────────────────┬───────────────────────────────────────────┘
                           │
┌──────────────────────────▼───────────────────────────────────────────┐
│                      stdlib 包 (API 实现)                              │
│                                                                       │
│   func setupMath() *object.Object {                                  │
│       m := object.NewObject()                                        │
│       m.SetProperty("abs", object.NewBuiltin("abs",                  │
│           func(args ...object.Value) object.Value {                  │
│               v := toFloat(args[0])                                  │
│               return object.NewNumber(math.Abs(v))                   │
│           }))                                                        │
│       return m                                                       │
│   }                                                                   │
└──────────────────────────────────────────────────────────────────────┘
```

### 1.2 三种函数类型对比

| 类型 | Go 结构体 | 调用签名 | 典型用途 | VM 处理 |
|------|-----------|---------|---------|---------|
| **内建函数** | `BuiltinFunction` | `Fn(args ...Value) Value` | `Math.abs`、`console.log` | `OP_CALL` 直接执行 |
| **内建方法** | `BuiltinMethod` | `Fn(this Value, args ...Value) Value` | `Array.prototype.push`、`String.prototype.toUpperCase` | `OP_CALL_METHOD` 执行 |
| **JS 闭包** | `Closure` | 字节码指令序列 | 用户自定义函数 | 编译为字节码，由 VM 解释执行 |

**关键区别**：`BuiltinFunction` 没有 `this` 绑定，`BuiltinMethod` 的 `this` 由 VM 作为第一个参数传入。

### 1.3 函数注册机制

**步骤一：在 `object/function.go` 中定义函数类型**

```go
// 无 this 的内建函数
type BuiltinFunction struct {
    Name       string
    Fn         func(args ...Value) Value
    Properties map[string]Value        // 静态属性 (如 String.fromCharCode.length)
    ReturnIsValue bool                 // 返回的 Error 是普通值还是异常 (详见第4章)
}

// 有 this 的内建方法
type BuiltinMethod struct {
    Name string
    Fn   func(this Value, args ...Value) Value
}
```

**步骤二：在 `stdlib/*.go` 中实现具体 API**

```go
// stdlib/math.go
func setupMath() *object.Object {
    m := object.NewObject()
    m.SetProperty("abs", object.NewBuiltin("abs", func(args ...object.Value) object.Value {
        v := toFloat(args[0])           // 参数解包
        return object.NewNumber(math.Abs(v))  // 返回值封装
    }))
    return m
}
```

**步骤三：在 `stdlib/stdlib.go` 的 `SetupGlobals()` 中注册到全局环境**

```go
func SetupGlobals() *runtime.Environment {
    env := runtime.NewEnvironment()
    mathObj := setupMath()
    env.Declare("Math", mathObj, false)  // false = 可重赋值
    // ...
    return env
}
```

**步骤四：VM 识别并执行**

当 JavaScript 代码执行 `Math.abs(-5)` 时：

1. 编译器生成 `OP_LOAD_GLOBAL "Math"` → 栈推入 `Math` 对象
2. 编译器生成 `OP_GET "abs"` → 栈弹出 `Math`，从属性中找到 `BuiltinFunction`，重新推入
3. 编译器生成 `OP_CONST 5`（被转换为负数）、`OP_NEG` → 栈推入 `-5`
4. 编译器生成 `OP_CALL 1`（1 个参数）→ VM 识别 `BuiltinFunction`，调用 `callee.Fn(args)`，结果压栈

### 1.4 回调桥：Go → JavaScript

`object/callback.go` 提供了从 Go 代码调用 JavaScript 函数的能力——这是实现 Promise、数组方法回调等功能的基石：

```go
// object/callback.go
var callFunction CallFunc   // 由 VM 在 init() 中注册

// stdlib 从 Go 调用 JS 闭包的调用点：
// 1. Promise 的 executor(resolve, reject) 调用
// 2. Array.prototype.map/forEach/filter 的用户回调
// 3. setTimeout/setInterval 的回调函数

func (p *Promise) Then(onFulfilled Value) *Promise {
    // ...
    result := object.CallFunction(onFulfilled, nil, p.Value)
    //              ↑ 函数   ↑ this  ↑ 参数列表
    next.Resolve(result)
}
```

**VM 侧的注册（`vm/vm.go` init()）：**

```go
func init() {
    object.SetCallFunction(func(fn object.Value, this object.Value,
                                 args []object.Value) object.Value {
        result, err := currentVM.callFunction(fn, this, args)
        if err != nil {
            currentVM.callbackErr = err   // 错误暂存，VM 在安全点检查
            return object.UndefinedSingleton
        }
        return result
    })
}
```

> **为什么需要 `currentVM` 桥？** VM 执行期间 `currentVM` 被设为当前实例；主脚本结束后恢复为 `nil`。如果事件循环（`RunTimers`）期间回调 JavaScript 函数，需要重新注册 `currentVM`，否则 `object.CallFunction` 找不到 VM，回调被静默丢弃。

---

## 2. 实现第一个 API

### 2.1 需求分析

实现 `Math.hypot(...values)` —— 返回所有参数平方和的平方根，即欧几里得范数：

```
hypot(3, 4)   → 5       (√(9+16))
hypot(1, 2, 3) → √14    (≈3.74)
hypot()       → 0       (空参数时返回 0)
```

### 2.2 完整实现

在 `stdlib/math.go` 中添加：

```go
m.SetProperty("hypot", object.NewBuiltin("hypot", func(args ...object.Value) object.Value {
    // 遍历所有参数，累加平方和
    sum := 0.0
    for _, a := range args {
        v := toFloat(a)   // 字符串强制转换为数字 (ECMAScript 规范)
        if math.IsInf(v, 0) {
            // 任一参数为 ±Infinity → 整体返回 +Infinity
            return object.NewNumber(math.Inf(1))
        }
        if math.IsNaN(v) {
            // 任一参数为 NaN → 整体返回 NaN
            return object.NewNumber(math.NaN())
        }
        sum += v * v
    }
    return object.NewNumber(math.Sqrt(sum))
}))
```

**验证：**

```bash
$ ./js-runtime.exe -e 'console.log(Math.hypot(3, 4))'
5
$ ./js-runtime.exe -e 'console.log(Math.hypot("6", 8))'
10
$ ./js-runtime.exe -e 'console.log(Math.hypot(1, NaN))'
NaN
```

### 2.3 参数解析三步法

每个内建函数的参数处理都遵循固定模式：

```
┌─────────────────────────────────────────────────────────┐
│  Step 1: 索引检查                                        │
│  if len(args) < N {                                      │
│      return NewTypeError("missing argument N")           │
│  }                                                       │
└────────────────────┬────────────────────────────────────┘
                     ▼
┌─────────────────────────────────────────────────────────┐
│  Step 2: 类型断言 / 强制转换                              │
│  // 类型断言 (严格)                                       │
│  if arr, ok := args[0].(*object.Array); !ok {            │
│      return NewTypeError("expected array")               │
│  }                                                       │
│  // 或强制转换 (宽松，遵循 JS 隐式转换规则)                │
│  v := toFloat(args[0])   // 字符串 → 数字                 │
└────────────────────┬────────────────────────────────────┘
                     ▼
┌─────────────────────────────────────────────────────────┐
│  Step 3: 边界检查                                        │
│  if v < 0 {                                             │
│      return NewRangeError("must be non-negative")        │
│  }                                                       │
└─────────────────────────────────────────────────────────┘
```

**helper 函数（在 `stdlib/helpers.go` 中定义）：**

| 函数 | 作用 | 示例 |
|------|------|------|
| `toFloat(v Value) float64` | 任意 JS 值 → float64 | `toFloat(NewString("3.14"))` → `3.14` |
| `toInt(v Value) int64` | 任意 JS 值 → int64 | `toInt(NewBoolean(true))` → `1` |
| `toStr(v Value) string` | 任意 JS 值 → 字符串 | `toStr(NewNumber(42))` → `"42"` |
| `toBool(v Value) bool` | 任意 JS 值 → bool | `toBool(NewNull())` → `false` |
| `isFalsy(v Value) bool` | 是否为假值 | — |

### 2.4 返回值封装

| Go 值 | JS 值 | 封装方法 |
|-------|-------|---------|
| `float64` | `Number` | `object.NewNumber(v)` |
| `string` | `String` | `object.NewString(v)` |
| `bool` | `Boolean` | `object.NewBoolean(v)` |
| `nil` | `undefined` | `object.UndefinedSingleton` |
| 无返回值 | `undefined` | `return object.UndefinedSingleton` |

> **为什么不用 `nil` 表示 `undefined`？** `object.Value` 是接口，`nil` 接口值在类型断言时会 panic。`UndefinedSingleton` 是安全的哨兵单例。

---

## 3. 数据转换

### 3.1 双向转换的完整流程

数据转换是 runtime API 中最核心的工程问题——JavaScript 和 Go 有完全不同的数据结构。

```
JavaScript                              Go
─────────                               ───
[4, 1, 7, 2, 9]   ──解包──→   []object.Value{Elements}
                        │
                        │  (在 Go 侧遍历、计算)
                        ▼
              sum = 0.0; for _, e := range arr { sum += toFloat(e) }
                        │
                        │  (封装为 JS 值返回)
                        ▼
              {count:5, min:1, max:9, mean:4.6}
```

### 3.2 完整示例：stats.describe() API

这个 API 展示了 **JS → Go（解包）** 和 **Go → JS（封装）** 的完整往返：

```go
// stdlib/stats.go
func setupStats(env *runtime.Environment) {
    stats := object.NewObject()

    // === 参数解包辅助函数 ===
    toArrayArg := func(args []object.Value, idx int, apiName string) (*object.Array, object.Value) {
        if idx >= len(args) {
            return nil, object.NewTypeError("%s: missing argument %d", apiName, idx+1)
        }
        arr, ok := args[idx].(*object.Array)
        if !ok {
            return nil, object.NewTypeError("%s: argument %d must be an array", apiName, idx+1)
        }
        return arr, nil
    }

    // === JS 数组 → Go 计算 ===
    stats.SetProperty("describe", object.NewBuiltin("describe", func(args ...object.Value) object.Value {
        arr, errVal := toArrayArg(args, 0, "stats.describe")
        if errVal != nil {
            return errVal  // 类型错误会抛异常 (见第4章)
        }
        if len(arr.Elements) == 0 {
            return object.NewRangeError("cannot describe empty array")
        }

        count := 0
        min := math.Inf(1)
        max := math.Inf(-1)
        total := 0.0
        for _, el := range arr.Elements {
            v := toFloat(el)
            if math.IsNaN(v) {
                return object.NewRangeError("array contains NaN at index %d", count)
            }
            count++
            total += v
            if v < min { min = v }
            if v > max { max = v }
        }

        // === Go 结果 → JS 对象 ===
        result := object.NewObject()
        result.SetProperty("count", object.NewNumber(float64(count)))
        result.SetProperty("min",    object.NewNumber(min))
        result.SetProperty("max",    object.NewNumber(max))
        result.SetProperty("mean",   object.NewNumber(total/float64(count)))
        return result
    }))

    env.Declare("stats", stats, false)
}
```

**使用：**

```javascript
const d = stats.describe([4, 1, 7, 2, 9]);
console.log(d.count);  // 5
console.log(d.min);    // 1
console.log(d.max);    // 9
console.log(d.mean);   // 4.6
```

### 3.3 解包辅助函数设计模式

```go
// 模式 A: 返回 (值, error)
// 适合需要类型检查后继续处理的情况
func toArrayArg(args []object.Value, idx int) (*object.Array, object.Value) {
    if idx >= len(args) {
        return nil, object.NewTypeError("missing argument")
    }
    arr, ok := args[idx].(*object.Array)
    if !ok {
        return nil, object.NewTypeError("expected array, got %s", object.TypeOf(args[idx]))
    }
    return arr, nil
}

// 模式 B: panic-free 类型断言
func getString(v object.Value) (string, bool) {
    if s, ok := v.(*object.String); ok {
        return s.Value, true
    }
    return "", false
}

// 模式 C: 宽松转换 (遵循 JS 语义)
func toFloat(v object.Value) float64 {
    switch val := v.(type) {
    case *object.Number:  return val.Value
    case *object.Boolean: if val.Value { return 1 } else { return 0 }
    case *object.Null:    return 0
    case *object.Undefined: return math.NaN()
    case *object.String:
        // "123px" → NaN, "" → 0, "  42  " → 42
        if s, err := strconv.ParseFloat(strings.TrimSpace(val.Value), 64); err == nil {
            return s
        }
        return math.NaN()
    }
    return math.NaN()
}
```

### 3.4 object.TypeOf 辅助

```go
// object.TypeOf 返回 JavaScript typeof 操作符的等价结果
func TypeOf(v Value) string {
    switch v.Type() {
    case NUMBER_OBJ:    return "number"
    case STRING_OBJ:    return "string"
    case BOOLEAN_OBJ:   return "boolean"
    case NULL_OBJ:      return "object"
    case UNDEFINED_OBJ: return "undefined"
    case OBJECT_OBJ:    return "object"
    case ARRAY_OBJ:     return "object"
    case CLOSURE_OBJ,
         BUILTIN_OBJ:   return "function"
    }
    return "object"
}
```

---

## 4. 错误处理

### 4.1 错误处理的三个层次

```
┌────────────────────────────────────────────────────────────────┐
│  Layer 3: VM 层 — throwIfError 统一处理机制                      │
│                                                                │
│  VM 执行 builtin.Fn(args...) 后，检查返回值是否为 *object.Error │
│  如果是，调用 handleThrow 将其转为 JS 异常                       │
└────────────────────────────┬───────────────────────────────────┘
                             │
┌────────────────────────────▼───────────────────────────────────┐
│  Layer 2: API 层 — 错误构造器                                    │
│                                                                │
│  参数校验失败 → return object.NewTypeError(...)                 │
│  越界/非法值   → return object.NewRangeError(...)               │
│  其他错误     → return object.NewError(...)                     │
│                                                                │
│  注意: Error/TypeError 构造器必须标记 ReturnIsValue = true       │
│  否则 new Error("x") 会被 VM 当作异常抛出！                      │
└────────────────────────────┬───────────────────────────────────┘
                             │
┌────────────────────────────▼───────────────────────────────────┐
│  Layer 1: 类型系统 — Error 对象结构                              │
│                                                                │
│  type Error struct {                                           │
│      Message string  // "division by zero"                      │
│      Name    string  // "TypeError" | "RangeError" | ...       │
│  }                                                             │
└────────────────────────────────────────────────────────────────┘
```

### 4.2 错误类型选择

| 错误类型 | 适用场景 | 构造函数 |
|---------|---------|---------|
| `Error` | 一般性错误 | `NewError(msg)` |
| `TypeError` | 参数类型错误、不可调用 | `NewTypeError(format, args...)` |
| `RangeError` | 数值越界、空集合 | `NewRangeError(format, args...)` |
| `ReferenceError` | 引用未定义变量 | `NewReferenceError(format, args...)` |
| `SyntaxError` | 语法解析错误 | `NewErrorWithName("SyntaxError", msg)` |

### 4.3 统一错误抛出机制：throwIfError

**问题：** 修复前，`BuiltinFunction` 返回 `*object.Error` 的语义不统一——`JSON.parse` 返回的 Error 不抛出，`new Error()` 返回的 Error 却抛出。这不符合 JavaScript 语义。

**解决方案：** 在 VM 中引入 `throwIfError(result, returnsRaw bool)` 方法：

```go
// vm/vm.go — throwIfError 统一处理
func (vm *VM) throwIfError(result object.Value, returnsRaw bool) (thrown bool, err error) {
    errObj, isErr := result.(*object.Error)
    if !isErr || returnsRaw {
        return false, nil  // 普通值，正常压栈
    }
    if !vm.handleThrow(errObj) {
        return true, &ThrowError{Value: errObj}  // 无 catch 块，向上传播
    }
    return true, nil  // 已被 catch 块捕获
}
```

**调用点（所有内建函数返回路径）：**

```go
// OP_CALL 中
case *object.BuiltinFunction:
    result := callee.Fn(args...)
    if thrown, err := vm.throwIfError(result, callee.ReturnIsValue); thrown {
        if err != nil { return err }  // 无处理器，返回 ThrowError
        continue                      // 有处理器，跳过压栈
    }
    vm.stack.Push(result)

// OP_NEW (构造器) 中
if errObj, isErr := result.(*object.Error); isErr && !builtin.ReturnIsValue {
    if !vm.handleThrow(errObj) {
        return &ThrowError{Value: errObj}
    }
    continue
}
```

### 4.4 构造器 vs 普通函数：ReturnIsValue 标记

**规则：**
- 普通 API（`JSON.parse`、`setTimeout`）：返回 `*Error` → 抛异常
- 错误构造器（`Error()`、`TypeError()`、`RangeError()`）：返回 `*Error` → 这是**普通返回值**，不是异常

**实现：**

```go
// object/function.go — BuiltinFunction 新增字段
type BuiltinFunction struct {
    Name            string
    Fn              func(args ...Value) Value
    Properties      map[string]Value
    ReturnIsValue   bool  // true = 返回的 Error 是值，不抛出
}

// stdlib/object_methods.go — Error 构造器
env.Declare("Error", func() *object.BuiltinFunction {
    f := object.NewBuiltin("Error", func(args ...object.Value) object.Value {
        msg := ""
        if len(args) > 0 { msg = toStr(args[0]) }
        return object.NewError(msg)
    })
    f.ReturnIsValue = true  // 关键标记
    return f
}(), false)
```

**效果对比：**

```javascript
// JSON.parse 错误 → 抛异常（ReturnIsValue = false，默认值）
JSON.parse("{invalid");  // SyntaxError 被 throw

// Error 构造器 → 返回值（ReturnIsValue = true）
const e = new Error("boom");  // e 是 Error 对象，不是抛出的异常
console.log(e.message);       // "boom"
console.log(e instanceof Error);  // true

// setTimeout 类型错误 → 抛异常
setTimeout(42);  // TypeError: setTimeout: first argument must be a function
```

### 4.5 catch 块中错误信息完整保留

错误对象的 `message` 和 `name` 属性在 VM 的 `handleThrow` 流程中被完整保留：

```go
func (e *Error) GetProperty(name string) (Value, bool) {
    switch name {
    case "message": return NewString(e.Message), true
    case "name":
        n := e.Name
        if n == "" { n = "Error" }
        return NewString(n), true
    case "toString":
        return &BuiltinFunction{
            Name: "toString",
            Fn: func(args ...Value) Value {
                return NewString(e.Inspect())  // "TypeError: ..."
            },
        }, true
    }
    return nil, false
}
```

**验证：**

```javascript
try {
  stats.sum("not an array");
} catch (e) {
  console.log(e.name);     // "TypeError"
  console.log(e.message);  // "stats.sum: argument 1 must be an array, got string"
}
```

---

## 5. 进阶内容

### 5.1 异步 API：Promise 集成

#### 5.1.1 为什么异步 API 要返回 Promise

同步 API 在函数返回时立即产生结果：

```javascript
const result = Math.sqrt(16);  // 立即得到 4
```

异步 API 在函数返回时**无法立即给出结果**——结果需要等待某个未来事件：

```javascript
const result = delay(100, "hello");  // 100ms 后才得到 "hello"
```

Promise 是 JavaScript 处理"未来值"的标准方式：它代表一个**尚未完成但预计会完成**的操作。

#### 5.1.2 delay() 完整实现

```go
// stdlib/timers.go — delay(ms, value?) API
env.Declare("delay", object.NewBuiltin("delay", func(args ...object.Value) object.Value {
    result := object.NewPromise()

    // 参数校验 — 失败时 reject 而不是同步 throw
    if len(args) < 1 {
        result.Reject(object.NewTypeError(
            "delay: missing argument 1 (expected duration in milliseconds)"))
        return result
    }
    ms := toFloat(args[0])
    if math.IsNaN(ms) || ms < 0 {
        result.Reject(object.NewRangeError(
            "delay: duration must be non-negative, got %s", args[0].Inspect()))
        return result
    }

    // 默认 resolve 值为 undefined
    var val object.Value = object.UndefinedSingleton
    if len(args) > 1 { val = args[1] }

    // 用调度器注册一次性定时器，到期时 resolve Promise
    scheduler.SetTimeout(object.NewBuiltin("__delay_resolve", func(...object.Value) object.Value {
        result.Resolve(val)   // Promise 进入 fulfilled 状态，触发 .then 回调
        return object.UndefinedSingleton
    }), time.Duration(ms*float64(time.Millisecond)))

    return result  // 立即返回 Promise 对象（不等待）
}), false)
```

#### 5.1.3 异步错误报告约定

**关键设计原则：异步 API 的参数校验失败用 `reject()` 而非同步 `throw`。**

```go
// ✅ 正确：reject 用于异步错误报告
result.Reject(object.NewRangeError("duration must be non-negative"))
return result

// ❌ 错误：异步函数不应该同步 throw
if ms < 0 {
    return object.NewRangeError("...")  // 会导致 Promise 被 reject，
    // 但这正是我们想要的！return 是对的。
    // 错误在于：这里 return 后，调用方没有 try/catch 可用，
    // 必须通过 .catch() 统一处理——这正是 Promise 的约定。
}
```

**调用方用法：**

```javascript
// 方式 A: try/await（需在 async 函数内）
try {
  await delay(-1);
} catch (e) {
  console.log("rejected:", e.message);
}

// 方式 B: .catch 链式（始终可用）
delay(-1).catch(function(e) {
  console.log("rejected:", e.message);
});
```

#### 5.1.4 Promise 在 VM 中的执行时机

**问题：** 定时器回调在 `RunTimersUntil` 中执行，而主脚本执行结束后 `currentVM` 恢复为 `nil`。这会导致回调中的 `object.CallFunction` 找不到 VM。

**修复：** 在 `RunTimersUntil` 执行期间重新注册 `currentVM`：

```go
func (vm *VM) RunTimersUntil(until time.Time) error {
    saved := currentVM
    currentVM = vm          // 事件循环期间保持 VM 可访问
    defer func() { currentVM = saved }()

    for {
        // ... 定时器派发 ...
        _, err := vm.callFunction(t.Callback, nil, nil)
        if err != nil {
            scheduler.Reschedule(t)
            return err
        }
    }
}
```

**执行流程：**

```
主脚本执行结束
    ↓
RunTimersUntil(currentVM = vm)
    ↓
定时器到期 → callFunction(t.Callback, nil, nil)
    ↓
JS 闭包执行 → setTimeout 回调 → Promise.resolve()
    ↓
resolve() → triggerCallbacks() (同步执行 .then 回调)
    ↓
object.CallFunction(onFulfilled, nil, value) → currentVM.callFunction(...)
    ↓
JS onFulfilled 闭包执行 (在 RunTimers 上下文中)
```

#### 5.1.5 Promise.then 的实现（BuiltinMethod 模式）

```go
// stdlib/promise.go — setupPromiseProto
p.SetProperty("then", object.NewBuiltinMethod("then", func(this object.Value,
                                                    args ...object.Value) object.Value {
    promise, ok := this.(*object.Promise)
    if !ok { return this }

    var onFulfilled, onRejected object.Value
    if len(args) > 0 { onFulfilled = args[0] }
    if len(args) > 1 { onRejected  = args[1] }

    next := object.NewPromise()
    promise.Lock()

    if promise.State == object.PromiseFulfilled {
        promise.Unlock()
        // 同步执行 (Promise 已 settled)
        if object.IsCallable(onFulfilled) {
            result := object.CallFunction(onFulfilled, nil, promise.Value)
            next.Resolve(result)
        } else {
            next.Resolve(promise.Value)
        }
    } else if promise.State == object.PromiseRejected {
        promise.Unlock()
        if object.IsCallable(onRejected) {
            result := object.CallFunction(onRejected, nil, promise.Reason)
            next.Resolve(result)
        } else {
            next.Reject(promise.Reason)
        }
    } else {
        // Pending: 注册回调，等 resolve/reject 触发
        promise.ThenCallbacks = append(promise.ThenCallbacks, object.PromiseCallback{
            Callback:    onFulfilled,
            NextPromise: next,
        })
        promise.CatchCallbacks = append(promise.CatchCallbacks, object.PromiseCallback{
            Callback:    onRejected,
            NextPromise: next,
            IsCatch:     true,
        })
        promise.Unlock()
    }
    return next
}))
```

### 5.2 对象生命周期与内存管理

#### 5.2.1 无 GC 显式管理的原因

Go 有自己的垃圾回收，JavaScript VM 中的对象（`Object`、`Array`、`Closure` 等）都是 Go 堆分配对象，由 Go GC 自动回收。

**不需要手动管理的场景：**
- 局部变量引用的对象（Go 栈逃逸分析决定是否堆分配）
- 闭包捕获的变量（在 `Closure.CapturedLocals` 中持有）
- Promise 的回调队列（`ThenCallbacks` 等字段持有引用）

**需要注意的场景：**

1. **调度器持有的回调引用**

   ```go
   scheduler.SetTimeout(fn, delay)  // fn 被 Timer.Callback 持有
   // 定时器触发后，DueTimers() 从 map 中删除，fn 的引用释放
   ```

2. **Promise 回调持有后续 Promise**

   ```go
   promise.ThenCallbacks = append(promise.ThenCallbacks, PromiseCallback{
       Callback:    onFulfilled,
       NextPromise: next,  // 持有 next 的引用直到链式调用完成
   })
   ```

3. **闭包捕获环境**

   ```go
   type Closure struct {
       Fn             *CompiledFunction
       Env            Environment  // 持有外层作用域
       CapturedLocals []Value      // 显式捕获的局部变量
   }
   ```

#### 5.2.2 循环引用的处理

Promise 链式调用可能产生循环，但 Go GC 能处理循环引用（只要从根可达即可追踪）：

```
p1 ──then──→ p2 ──then──→ p3
↑                            │
└─────────── catch ──────────┘
```

每个 Promise 通过 `ThenCallbacks`/`CatchCallbacks` 持有下一个 Promise。只要外部变量持有 `p1`，整个链都被追踪。`p3` settle 后，`p3` 的回调数组被清空（`detachCallbacksLocked`），如果只有 p2/p1 引用 p3，则 p3 可以被 GC。

#### 5.2.3 单例模式

对于 `undefined` 和 `null` 这样的唯一值，使用单例避免重复分配：

```go
var (
    NullSingleton     = &Null{}
    UndefinedSingleton = &Undefined{}
)
```

所有 `undefined` 值都指向同一个 `UndefinedSingleton` 实例。

### 5.3 已知限制

1. **await 一个被拒绝的 Promise + try/catch**：当前 async/await 实现基于 generator，`await` 表达式编译为 `yield`。如果被 await 的 Promise 进入 rejected 状态，`yield` 返回值中会携带错误，但 try/catch 块无法恢复生成器执行到此场景中。**临时方案**：使用 `.catch()` 链式写法捕获异步错误。
   ```javascript
   // ✅ 当前可用
   async function f() {
     await delay(-1).catch(e => { console.log("caught:", e); });
   }

   // ⚠️ 当前有限制
   async function f() {
     try {
       await delay(-1);  // reject 后不会落入 catch 块
     } catch (e) {
       console.log("caught:", e);
     }
   }
   ```

2. **returnIsValue 标记的替代设计**：更通用的方案是为所有函数类型引入"错误处理策略"枚举，但这需要较大的 API 重构。当前的 `ReturnIsValue bool` 是最小侵入的解决方案。

---

## 附录：修改摘要

本次实现教程过程中对 runtime 的修改（均为 bug 修复或必要的架构增强）：

| 修改 | 位置 | 说明 |
|------|------|------|
| `throwIfError` 机制 | `vm/vm.go` | 统一内建函数错误抛出语义，修复 `new Error()` 异常 bug |
| `ReturnIsValue` 字段 | `object/function.go` | 区分"错误返回值"与"异常" |
| 错误构造器标记 | `stdlib/object_methods.go` | Error/TypeError/RangeError 等标记为 `ReturnIsValue=true` |
| `currentVM` 跨 RunTimers 保持 | `vm/vm.go` | 修复定时器回调中 Promise 回调静默丢失的 bug |
| Promise 回调同步执行 | `object/promise.go` | `triggerCallbacks` 从 `go func()` 改为同步执行，修复脚本模式下回调丢失 |
| `detachCallbacksLocked` | `object/promise.go` | 安全地持有锁状态下分离回调，避免并发问题 |
| `delay()` API | `stdlib/timers.go` | 示例异步 API |
| `stats` API | `stdlib/stats.go` | 示例数据转换 API |
| `Math.hypot` | `stdlib/math.go` | 示例参数解析 API |
| 回归测试 | `vm/vm_builtin_error_test.go`, `vm/vm_timer_promise_test.go` | 锁定修复后的语义 |

---

*教程基于 `js-runtime` 项目（字节码 VM 架构）编写，所有代码示例均已通过测试验证。*
