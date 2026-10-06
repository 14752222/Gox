package object

import (
	"fmt"
	"sort"
)

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
	// IsAsyncGenerator 标识 async generator 的 wrapper (IsAsync=true 且
	// IsGenerator=false，需要显式区分于普通 async 函数)。决定函数对象
	// [[Prototype]] 与 .prototype 实例原型的种类。
	IsAsyncGenerator bool
	// IsStrict 报告函数体是否严格模式 (继承 + 指令 + 类/模块强制)。
	// VM 据此决定 this 归一与未声明赋值语义。
	IsStrict bool
	// BaseSlot 是函数自身变量的起始槽位 (= 外层作用域的变量数)
	// 参数和局部变量从 BaseSlot 开始排列
	BaseSlot int
	// ArgumentsSlot 是 arguments 对象的槽位 (-1 表示未使用)
	ArgumentsSlot int
	// SelfSlot 是命名函数表达式的自引用槽位 (-1 表示无)。
	// 函数被调用时 VM 把闭包自身写入该槽，函数体内通过名字
	// 引用自身 (递归) 即读取此槽。
	SelfSlot int
	// Positions 是函数体语句的源码位置表 (offset 升序, 可为 nil)。
	// 运行时错误渲染源码帧用 (T05)。
	Positions []SrcPos
	// ParamPrologueEnd 是形参绑定前导段 (默认值/解构) 在 Instructions 中的
	// 结束偏移 (0 表示无前导段)。生成器函数在调用时先同步执行 [0, 该偏移)
	// 完成形参绑定, 函数体留待首次 next()。见 bytecode.FunctionMetadata。
	ParamPrologueEnd int
	// DeferParams 为真表示形参绑定不由调用时的前导段完成, 而留给内层驱动
	// (__spawn) 在首次驱动时执行。仅用于普通 async 函数的内层 generator:
	// 其形参求值抛错须返回 rejected Promise (而非生成器那样的同步抛出)。
	DeferParams bool
	// Constants 是函数字节码引用的常量池 (用于跨模块调用)
	// 为 nil 时使用 VM 的全局常量池
	Constants []Value
}

// SrcPos 是语句的源码位置。Offset 是语句首条指令在**所属编译单元**
// (主脚本或函数体) 指令流里的位置 —— 不同编译单元的 offset 空间独立。
// 与 bytecode.SrcPos 结构相同但独立定义 (object 与 bytecode 互不依赖,
// 由 vm 层在加载函数时转换)。
type SrcPos struct {
	Offset int
	Line   int
	Col    int
}

// SortSrcPos 按 Offset 升序排序。
func SortSrcPos(s []SrcPos) {
	sort.Slice(s, func(i, j int) bool { return s[i].Offset < s[j].Offset })
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
	// FuncPrototype 是函数对象自身的 [[Prototype]] (与实例侧的 Proto 不同)。
	// 按函数种类指向 %Function.prototype% / %GeneratorFunction.prototype% /
	// %AsyncFunction.prototype% / %AsyncGeneratorFunction.prototype%。
	// 由 vm.createClosure 赋值；nil 时回退到全局 %Function.prototype%。
	FuncPrototype Value
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
	// 先查自定义属性 (如 class 的静态方法/静态访问器)
	if c.Props != nil {
		if val, ok := c.Props[name]; ok {
			// 访问器属性: 调用 getter (this = 类本身)
			if acc, isAcc := val.(*Accessor); isAcc {
				if acc.Getter != nil && IsCallable(acc.Getter) {
					return CallFunction(acc.Getter, c), true
				}
				return UndefinedSingleton, true
			}
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
		// 箭头函数、async 函数(非生成器)都没有 prototype 属性。
		// 注意 async generator 的 wrapper 也是 IsAsync=true 但 IsGenerator=false，
		// 它**有** prototype —— 需用 IsAsyncGenerator 把它排除。
		if c.IsArrow || (c.Fn != nil && c.Fn.IsAsync && !c.Fn.IsGenerator && !c.Fn.IsAsyncGenerator) {
			return UndefinedSingleton, true
		}
		if c.Proto != nil {
			return c.Proto, true
		}
		// 惰性创建默认 prototype (含 constructor 自引用)
		p := NewObject()
		p.SetBuiltinProperty("constructor", c)
		// 实例原型 (fn.prototype) 的 [[Prototype]]:
		//   普通/async 函数 → %Object.prototype%; function* → %GeneratorPrototype%;
		//   async function* → %AsyncGeneratorPrototype%。(async 函数无 prototype,
		//   已在上方提前返回。)
		if c.Fn != nil {
			if c.Fn.IsAsyncGenerator {
				if gp := GetAsyncGeneratorProto(); gp != nil {
					p.Proto = gp
				}
			} else if c.Fn.IsGenerator {
				if gp := GetGeneratorPrototype(); gp != nil {
					p.Proto = gp
				}
			} else if op := GetObjectPrototype(); op != nil {
				p.Proto = op
			}
		} else if op := GetObjectPrototype(); op != nil {
			p.Proto = op
		}
		c.Proto = p
		return c.Proto, true
	case "call", "apply", "bind", "toString":
		// Function.prototype 共享方法实现见 funcproto.go
		return funcProtoLookup(c, name)
	}
	// 其余属性沿函数对象 [[Prototype]] 链查找 (如 .constructor → Function /
	// GeneratorFunction / AsyncFunction / AsyncGeneratorFunction)。
	if val, ok := funcProtoLookupChain(c, name); ok {
		return val, true
	}
	return nil, false
}

func (c *Closure) SetProperty(name string, val Value) {
	if name == "prototype" {
		c.Proto = val
		return
	}
	// 已有访问器且写入的不是新访问器: 调用 setter (this = 类本身)
	if c.Props != nil {
		if cur, ok := c.Props[name]; ok {
			if acc, isAcc := cur.(*Accessor); isAcc {
				if _, valIsAcc := val.(*Accessor); !valIsAcc {
					if acc.Setter != nil && IsCallable(acc.Setter) {
						CallFunction(acc.Setter, c, val)
					}
					return
				}
			}
		}
	}
	// 其余属性存入 Props (如 class 的静态方法/访问器)
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
	// FuncPrototype 是函数对象自身的 [[Prototype]]。nil 时回退到全局
	// %Function.prototype% (绝大多数内置函数如此)。
	FuncPrototype Value
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
	case "call", "apply", "bind", "toString":
		return funcProtoLookup(b, name)
	}
	// 其余属性沿函数对象 [[Prototype]] 链查找 (如 .constructor)。
	if val, ok := funcProtoLookupChain(b, name); ok {
		return val, true
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

// NamePropertyOf 返回函数类值 (Closure / BuiltinFunction / BuiltinMethod) 上
// "name" 自有属性的描述符。ok=false 表示该值不是函数类, 没有 name 自有属性。
//
// 规范里函数的 name 是 { writable:false, enumerable:false, configurable:true }
// 的数据属性 (SetFunctionName / 内建函数定义)。Gox 的 name 值存在结构体字段
// 里 (而非 Object.Properties), 故 getOwnPropertyDescriptor / hasOwnProperty
// 需要这个统一的读取口来"看见"它。
//
// Closure 的 Props 若显式带了 "name" (例如类静态方法 `static name(){}`),
// 以存储值为准 —— 与 Closure.GetProperty 的查找优先级一致。
func NamePropertyOf(v Value) (PropertyDescriptor, bool) {
	fnName := func(name string) PropertyDescriptor {
		return PropertyDescriptor{Value: NewString(name), Writable: false, Enumerable: false, Configurable: true}
	}
	switch f := v.(type) {
	case *Closure:
		if f.Props != nil {
			if val, ok := f.Props["name"]; ok {
				return PropertyDescriptor{Value: val, Writable: false, Enumerable: false, Configurable: true}, true
			}
		}
		if f.Fn != nil {
			return fnName(f.Fn.Name), true
		}
		return fnName(""), true
	case *BuiltinFunction:
		return fnName(f.Name), true
	case *BuiltinMethod:
		return fnName(f.Name), true
	}
	return PropertyDescriptor{}, false
}

// BuiltinMethod 表示需要 this 绑定的内建方法。
// 用于 Array.prototype.push, String.prototype.toUpperCase 等原型方法。
// this 作为第一个参数传递，实际参数从 args 切片获取。
type BuiltinMethod struct {
	Name string
	Fn   func(this Value, args ...Value) Value
	// FuncPrototype 是函数对象自身的 [[Prototype]]。nil 时回退到全局
	// %Function.prototype%。
	FuncPrototype Value
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
	case "call", "apply", "bind", "toString":
		return funcProtoLookup(b, name)
	}
	if val, ok := funcProtoLookupChain(b, name); ok {
		return val, true
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
