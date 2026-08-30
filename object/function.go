package object

import "fmt"

// ParameterInfo 存储函数参数的元数据。
type ParameterInfo struct {
	Name    string // 参数名
	Default bool   // 是否有默认值
	Rest    bool   // 是否是剩余参数 (...)
}

// CompiledFunction 表示已编译为字节码的函数。
// 这是函数的"静态"形式，包含字节码但不包含运行时环境。
// 当函数被调用时，会创建一个 Closure 来绑定环境。
type CompiledFunction struct {
	// Instructions 是函数体的字节码 (编译后的指令序列)
	Instructions []byte
	// NumLocals 是函数所需的局部变量槽位数 (含外层捕获)
	NumLocals int
	// NumParameters 是参数个数
	NumParameters int
	// Parameters 是参数元数据 (名称、默认值、剩余参数标志)
	Parameters []ParameterInfo
	// Name 是函数名 (匿名函数为 "")
	Name string
	// IsArrow 标识是否为箭头函数
	IsArrow bool
	// IsGenerator 标识是否为生成器函数 (function*)
	IsGenerator bool
	// IsAsync 标识是否为 async 函数
	IsAsync bool
	// BaseSlot 是函数自身变量的起始槽位 (= 外层作用域的变量数)
	// 参数和局部变量从 BaseSlot 开始排列
	BaseSlot int
	// ArgumentsSlot 是 arguments 对象的槽位 (-1 表示未使用)
	ArgumentsSlot int
	// Constants 是函数字节码引用的常量池 (用于跨模块调用)
	// 为 nil 时使用 VM 的全局常量池
	Constants []Value
}

func (f *CompiledFunction) Type() ObjectType { return COMPILED_FUNCTION_OBJ }
func (f *CompiledFunction) Inspect() string {
	name := f.Name
	if name == "" {
		name = "anonymous"
	}
	return fmt.Sprintf("[Function: %s]", name)
}

func (f *CompiledFunction) IsTruthy() bool { return true }

func (f *CompiledFunction) GetProperty(name string) (Value, bool) {
	switch name {
	case "name":
		return NewString(f.Name), true
	case "length":
		return NewInt(int64(f.NumParameters)), true
	}
	return nil, false
}

func (f *CompiledFunction) SetProperty(name string, val Value) {
	// 函数属性不可设置
}

// Closure 表示一个绑定了词法环境的函数。
// 闭包 = 编译后的函数 + 捕获的环境 + this 绑定 + 捕获的局部变量。
// 每次函数声明或函数表达式求值时创建闭包。
type Closure struct {
	Fn             *CompiledFunction // 被闭包的函数
	Env            Environment       // 捕获的词法环境 (全局环境)
	This           Value             // this 绑定 (箭头函数复用外层 this)
	IsArrow        bool              // 是否为箭头函数
	CapturedLocals []Value           // 捕获的外层局部变量
	CreatedAtFrame int               // 创建时的帧索引 (用于递归自引用检测)
	Proto          Value             // prototype 属性 (new 实例的原型; 箭头函数无)
	Props          map[string]Value  // 其他可设置属性 (如 class 的静态方法)
}

func (c *Closure) Type() ObjectType { return CLOSURE_OBJ }
func (c *Closure) Inspect() string {
	if c.Fn != nil {
		return c.Fn.Inspect()
	}
	return "[Closure]"
}

func (c *Closure) IsTruthy() bool { return true }

func (c *Closure) GetProperty(name string) (Value, bool) {
	// 先查自定义属性 (如 class 的静态方法)
	if c.Props != nil {
		if val, ok := c.Props[name]; ok {
			return val, true
		}
	}
	switch name {
	case "name":
		if c.Fn != nil {
			return NewString(c.Fn.Name), true
		}
		return NewString(""), true
	case "length":
		if c.Fn != nil {
			return NewInt(int64(c.Fn.NumParameters)), true
		}
		return NewInt(0), true
	case "prototype":
		// 箭头函数没有 prototype
		if c.IsArrow {
			return UndefinedSingleton, true
		}
		if c.Proto != nil {
			return c.Proto, true
		}
		// 惰性创建默认 prototype (含 constructor 自引用)
		if c.Proto == nil {
			p := NewObject()
			p.SetProperty("constructor", c)
			c.Proto = p
		}
		return c.Proto, true
	case "call":
		// Function.prototype.call
		return &BuiltinFunction{
			Name: "call",
			Fn: func(args ...Value) Value {
				// call(thisArg, arg1, arg2, ...) 的处理在 VM 中
				return UndefinedSingleton
			},
		}, true
	case "apply":
		// Function.prototype.apply
		return &BuiltinFunction{
			Name: "apply",
			Fn: func(args ...Value) Value {
				return UndefinedSingleton
			},
		}, true
	case "bind":
		// Function.prototype.bind
		return &BuiltinFunction{
			Name: "bind",
			Fn: func(args ...Value) Value {
				return UndefinedSingleton
			},
		}, true
	}
	return nil, false
}

func (c *Closure) SetProperty(name string, val Value) {
	if name == "prototype" {
		c.Proto = val
		return
	}
	// 其余属性存入 Props (如 class 的静态方法)
	if c.Props == nil {
		c.Props = make(map[string]Value)
	}
	c.Props[name] = val
}

// BuiltinFunction 表示用 Go 实现的内建函数。
// 用于 console.log, Math.abs, JSON.stringify 等不需要 this 的函数。
type BuiltinFunction struct {
	Name       string
	Fn         func(args ...Value) Value
	Properties map[string]Value // 静态属性 (如 String.fromCharCode)
	// ReturnIsValue 为 true 时，Fn 返回的 *Error 是"普通值"而非异常，
	// VM 不会将其抛出。典型例子: Error/TypeError 等错误构造器——
	// new Error("x") 与 Error("x") 都应返回错误对象本身，而不是 throw。
	ReturnIsValue bool
}

func (b *BuiltinFunction) Type() ObjectType { return BUILTIN_OBJ }
func (b *BuiltinFunction) Inspect() string {
	return fmt.Sprintf("[Function: %s]", b.Name)
}

func (b *BuiltinFunction) IsTruthy() bool { return true }

func (b *BuiltinFunction) GetProperty(name string) (Value, bool) {
	// 先查自定义属性
	if b.Properties != nil {
		if val, ok := b.Properties[name]; ok {
			return val, true
		}
	}
	switch name {
	case "name":
		return NewString(b.Name), true
	case "length":
		return NewInt(0), true
	}
	return nil, false
}

func (b *BuiltinFunction) SetProperty(name string, val Value) {
	if b.Properties == nil {
		b.Properties = make(map[string]Value)
	}
	b.Properties[name] = val
}

// NewBuiltin 创建内建函数的便捷函数
func NewBuiltin(name string, fn func(args ...Value) Value) *BuiltinFunction {
	return &BuiltinFunction{Name: name, Fn: fn}
}

// BuiltinMethod 表示需要 this 绑定的内建方法。
// 用于 Array.prototype.push, String.prototype.toUpperCase 等原型方法。
// this 作为第一个参数传递，实际参数从 args 切片获取。
type BuiltinMethod struct {
	Name string
	Fn   func(this Value, args ...Value) Value
}

func (b *BuiltinMethod) Type() ObjectType { return BUILTIN_OBJ }
func (b *BuiltinMethod) Inspect() string {
	return fmt.Sprintf("[Function: %s]", b.Name)
}

func (b *BuiltinMethod) IsTruthy() bool { return true }

func (b *BuiltinMethod) GetProperty(name string) (Value, bool) {
	switch name {
	case "name":
		return NewString(b.Name), true
	case "length":
		return NewInt(0), true
	}
	return nil, false
}

func (b *BuiltinMethod) SetProperty(name string, val Value) {
	// 不可设置
}

// NewBuiltinMethod 创建内建方法的便捷函数
func NewBuiltinMethod(name string, fn func(this Value, args ...Value) Value) *BuiltinMethod {
	return &BuiltinMethod{Name: name, Fn: fn}
}
