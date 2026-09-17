// Package object 定义了 JavaScript 运行时中的所有值类型。
//
// 核心 Value 接口是所有 JS 值的统一表示，包括:
// - 原始类型: Number, String, Boolean, Null, Undefined
// - 引用类型: Object, Array, Function (CompiledFunction/Closure/BuiltinFunction)
// - 辅助类型: Error, Iterator
//
// 原型链通过 Object.Proto 字段实现，属性查找时先查自身再沿原型链向上。
package object

// ObjectType 表示值的内部类型标识。
type ObjectType string

const (
	NUMBER_OBJ    ObjectType = "NUMBER"    // 统一数字类型 (IEEE 754 double)
	STRING_OBJ    ObjectType = "STRING"    // 字符串
	BOOLEAN_OBJ   ObjectType = "BOOLEAN"   // 布尔值
	NULL_OBJ      ObjectType = "NULL"      // null
	UNDEFINED_OBJ ObjectType = "UNDEFINED" // undefined
	ARRAY_OBJ     ObjectType = "ARRAY"     // 数组
	OBJECT_OBJ    ObjectType = "OBJECT"    // 对象
	ERROR_OBJ     ObjectType = "ERROR"     // 错误

	// 函数相关类型
	COMPILED_FUNCTION_OBJ ObjectType = "COMPILED_FUNCTION" // 已编译函数 (字节码)
	CLOSURE_OBJ           ObjectType = "CLOSURE"           // 闭包 (函数 + 捕获环境)
	BUILTIN_OBJ           ObjectType = "BUILTIN"           // 内建函数 (Go 实现)

	// 迭代器
	ITERATOR_OBJ ObjectType = "ITERATOR"

	// Symbol
	SYMBOL_OBJ ObjectType = "SYMBOL" // Symbol 类型 (唯一且不可变)

	// BigInt
	BIGINT_OBJ ObjectType = "BIGINT" // BigInt 任意精度整数

	// ===== Temporal (ES2027) =====
	// Temporal 的所有类型都是不可变值对象，内部状态存于 Go 字段而非
	// Properties map——若存进 map，Object.keys() 会泄漏内部 slot。
	TEMPORAL_INSTANT_OBJ        ObjectType = "TEMPORAL_INSTANT"         // Temporal.Instant (精确时间点)
	TEMPORAL_PLAIN_DATE_OBJ     ObjectType = "TEMPORAL_PLAIN_DATE"      // Temporal.PlainDate (无时区日期)
	TEMPORAL_PLAIN_TIME_OBJ     ObjectType = "TEMPORAL_PLAIN_TIME"      // Temporal.PlainTime (无日期时间)
	TEMPORAL_PLAIN_DATETIME_OBJ ObjectType = "TEMPORAL_PLAIN_DATETIME"  // Temporal.PlainDateTime
	TEMPORAL_PLAIN_YM_OBJ       ObjectType = "TEMPORAL_PLAIN_YEARMONTH" // Temporal.PlainYearMonth
	TEMPORAL_PLAIN_MD_OBJ       ObjectType = "TEMPORAL_PLAIN_MONTHDAY"  // Temporal.PlainMonthDay
	TEMPORAL_ZONED_DATETIME_OBJ ObjectType = "TEMPORAL_ZONED_DATETIME"  // Temporal.ZonedDateTime
	TEMPORAL_DURATION_OBJ       ObjectType = "TEMPORAL_DURATION"        // Temporal.Duration
	TEMPORAL_TIMEZONE_OBJ       ObjectType = "TEMPORAL_TIMEZONE"        // Temporal.TimeZone
	TEMPORAL_CALENDAR_OBJ       ObjectType = "TEMPORAL_CALENDAR"        // Temporal.Calendar

	// 集合类型
	MAP_OBJ ObjectType = "MAP" // Map (键值对集合)
	SET_OBJ ObjectType = "SET" // Set (唯一值集合)

	// 异步
	PROMISE_OBJ ObjectType = "PROMISE" // Promise (异步计算)

	// 正则
	REGEXP_OBJ ObjectType = "REGEXP" // RegExp (正则表达式)

	// Proxy
	PROXY_OBJ ObjectType = "PROXY" // Proxy (代理)

	// 访问器
	ACCESSOR_OBJ ObjectType = "ACCESSOR" // getter/setter 访问器属性

	// 生成器
	GENERATOR_OBJ ObjectType = "GENERATOR" // 生成器对象 (function* 的实例)

	// ES2021/全局
	WEAKREF_OBJ ObjectType = "WEAKREF" // WeakRef 弱引用对象
	GLOBAL_OBJ  ObjectType = "GLOBAL"  // globalThis 全局对象

	// 响应式 (Dart GetX 风格)
	OBSERVABLE_OBJ ObjectType = "OBSERVABLE" // obs()/computed() 响应式单元
)

// Value 是所有 JavaScript 值的核心接口。
// 每种值类型都实现此接口，VM 和运行时通过此接口统一操作所有值。
type Value interface {
	// Type 返回值的类型标识
	Type() ObjectType
	// Inspect 返回值的可读字符串表示 (用于 console.log 和调试)
	Inspect() string
	// IsTruthy 返回值的布尔真值 (用于条件判断)
	IsTruthy() bool
	// GetProperty 获取对象属性，沿原型链查找。返回值和是否找到。
	GetProperty(name string) (Value, bool)
	// SetProperty 设置对象属性。
	SetProperty(name string, val Value)
}

// Environment 是作用域环境接口。
// 定义在 object 包中是为了让 Closure 类型能引用环境，
// 同时避免 object ↔ runtime 的循环依赖。
// 具体实现 runtime.Environment 满足此接口。
type Environment interface {
	// Get 查找变量，先查本地，未命中递归查外层。
	Get(name string) (Value, bool)
	// Set 修改变量值，const 绑定会返回错误。
	Set(name string, val Value) error
	// Declare 声明新变量 (let/const)。
	Declare(name string, val Value, isConst bool)
	// GetConst 检查变量是否为 const 绑定。
	IsConst(name string) bool
	// Outer 返回外层环境。
	Outer() Environment
}

// ===== 常量值: Null 和 Undefined =====

// Null 表示 JavaScript 的 null 值。
type Null struct{}

func (n *Null) Type() ObjectType                      { return NULL_OBJ }
func (n *Null) Inspect() string                       { return "null" }
func (n *Null) IsTruthy() bool                        { return false }
func (n *Null) GetProperty(name string) (Value, bool) { return nil, false }
func (n *Null) SetProperty(name string, val Value)    {}

// Undefined 表示 JavaScript 的 undefined 值。
type Undefined struct{}

func (u *Undefined) Type() ObjectType                      { return UNDEFINED_OBJ }
func (u *Undefined) Inspect() string                       { return "undefined" }
func (u *Undefined) IsTruthy() bool                        { return false }
func (u *Undefined) GetProperty(name string) (Value, bool) { return nil, false }
func (u *Undefined) SetProperty(name string, val Value)    {}

// ===== 全局单例 =====

var (
	NullSingleton      = &Null{}
	UndefinedSingleton = &Undefined{}
)

// ===== 辅助函数 =====

// TypeOf 返回 JavaScript typeof 操作符的字符串结果。
func TypeOf(v Value) string {
	switch v.Type() {
	case NUMBER_OBJ:
		return "number"
	case STRING_OBJ:
		return "string"
	case BOOLEAN_OBJ:
		return "boolean"
	case NULL_OBJ:
		return "object" // JavaScript 历史遗留: typeof null === "object"
	case UNDEFINED_OBJ:
		return "undefined"
	case ARRAY_OBJ, OBJECT_OBJ, ERROR_OBJ, ITERATOR_OBJ, MAP_OBJ, SET_OBJ, PROMISE_OBJ, REGEXP_OBJ, PROXY_OBJ, GENERATOR_OBJ:
		return "object"
	case SYMBOL_OBJ:
		return "symbol"
	case BIGINT_OBJ:
		return "bigint"
	case COMPILED_FUNCTION_OBJ, CLOSURE_OBJ, BUILTIN_OBJ:
		return "function"
	case WEAKREF_OBJ, GLOBAL_OBJ, OBSERVABLE_OBJ:
		return "object"
	case TEMPORAL_INSTANT_OBJ, TEMPORAL_PLAIN_DATE_OBJ, TEMPORAL_PLAIN_TIME_OBJ,
		TEMPORAL_PLAIN_DATETIME_OBJ, TEMPORAL_PLAIN_YM_OBJ, TEMPORAL_PLAIN_MD_OBJ,
		TEMPORAL_ZONED_DATETIME_OBJ, TEMPORAL_DURATION_OBJ, TEMPORAL_TIMEZONE_OBJ,
		TEMPORAL_CALENDAR_OBJ:
		return "object"
	default:
		return "undefined"
	}
}

// IsFalsy 判断值是否为假值 (false, 0, "", null, undefined, NaN)。
func IsFalsy(v Value) bool {
	switch val := v.(type) {
	case *Null:
		return true
	case *Undefined:
		return true
	case *Boolean:
		return !val.Value
	case *Number:
		return val.Value == 0 || val.Value != val.Value // 0 或 NaN
	case *String:
		return val.Value == ""
	case *BigInt:
		return val.Value.Sign() == 0
	default:
		return false
	}
}

// IsTruthy 是 IsFalsy 的否定。
func IsTruthyValue(v Value) bool {
	return !IsFalsy(v)
}
