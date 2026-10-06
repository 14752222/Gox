// Package runtime 提供 JavaScript 运行时的基础组件:
// - Environment: 作用域环境链 (块作用域 + const 检查)
// - Iterator: 迭代器协议 (for...of)
package runtime

import (
	"fmt"
	"sort"

	"github.com/14752222/Gox/object"
)

// Binding 表示变量绑定。
type Binding struct {
	Value   object.Value
	IsConst bool // const 声明的变量不可重新赋值

	// IsLexical 标记用户代码的顶层词法声明 (let/const/class/import 绑定)。
	// 内置全局 (console/Math 等) 与赋值创建的全局没有此标记，
	// 因此 let 可以遮蔽它们 —— 与浏览器全局词法环境语义一致。
	IsLexical bool

	// IsFnDecl 标记顶层函数声明。函数声明允许互相重定义，
	// 但与词法声明同名时双向冲突 (规范: SyntaxError)。
	IsFnDecl bool

	// IsVar 标记顶层 var 声明。var 是全局对象 (globalThis) 的自有属性，
	// 与 let/const/class 的词法绑定相对 (后者不属于 globalThis)。
	IsVar bool

	// IsImplicit 标记"隐式赋值创建的全局" (非严格模式下对未声明名字赋值)。
	// 也是 globalThis 的自有属性，但可配置 (可 delete)，与 var 的不可配置相对。
	IsImplicit bool

	// Desc 非 nil 时覆盖绑定的默认描述符 (Object.defineProperty(globalThis,...))。
	Desc *object.PropertyDescriptor
}

// Environment 实现词法作用域环境。
// 通过 outer 指针形成作用域链，支持块作用域和变量遮蔽。
// 满足 object.Environment 接口。
type Environment struct {
	store map[string]Binding // 变量名 → 绑定
	outer *Environment       // 外层作用域 (nil 表示全局作用域)
}

// NewEnvironment 创建全局环境。
func NewEnvironment() *Environment {
	return &Environment{
		store: make(map[string]Binding),
		outer: nil,
	}
}

// NewEnclosedEnvironment 创建嵌套环境 (块作用域)。
func NewEnclosedEnvironment(outer *Environment) *Environment {
	return &Environment{
		store: make(map[string]Binding),
		outer: outer,
	}
}

// Get 查找变量。先查本地 store，未命中递归查 outer。
// 返回值和是否找到。
func (e *Environment) Get(name string) (object.Value, bool) {
	obj, ok := e.store[name]
	if ok {
		return obj.Value, true
	}
	if e.outer != nil {
		return e.outer.Get(name)
	}
	return nil, false
}

// Set 修改变量值。
// 在作用域链中查找变量并修改。如果变量是 const 绑定，返回错误。
// 如果变量不存在，返回错误 (应该先 Declare)。
func (e *Environment) Set(name string, val object.Value) error {
	binding, ok := e.store[name]
	if ok {
		if binding.IsConst {
			return fmt.Errorf("TypeError: Assignment to constant variable: %s", name)
		}
		binding.Value = val
		e.store[name] = binding
		return nil
	}
	if e.outer != nil {
		return e.outer.Set(name, val)
	}
	// 变量不存在: 在非严格模式下会创建全局变量
	// 严格模式下应该报 ReferenceError
	// 这里选择在全局环境中创建 (模拟非严格模式)
	return fmt.Errorf("ReferenceError: Assignment to undeclared variable: %s", name)
}

// Declare 声明新变量 (let/const)。
// 变量在当前作用域中创建。
func (e *Environment) Declare(name string, val object.Value, isConst bool) {
	e.store[name] = Binding{
		Value:   val,
		IsConst: isConst,
	}
}

// DeclareGlobal 执行顶层声明并写入全局环境，同时实现全局重声明检查:
//   - 词法声明 (let/const/class/import) 与已有词法声明或函数声明冲突 → SyntaxError
//   - 函数声明允许互相重定义，但与已有词法声明冲突 → SyntaxError
//   - 与无标记的绑定 (内置全局、赋值创建的全局) 不冲突，词法声明可遮蔽之
//
// REPL 每行输入独立编译，重声明只能在执行声明指令时对照持久化的
// 全局环境发现，因此检查放在运行时而非编译期。
func (e *Environment) DeclareGlobal(name string, val object.Value, isConst, isFnDecl bool) error {
	if existing, ok := e.store[name]; ok {
		// 已有词法绑定: 任何重声明都冲突。
		if existing.IsLexical {
			return fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
		}
		// 已有 var / 函数声明绑定: 与之同名的词法声明冲突。
		if !isFnDecl && (existing.IsVar || existing.IsFnDecl) {
			return fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
		}
	}
	e.store[name] = Binding{
		Value:     val,
		IsConst:   isConst,
		IsLexical: !isFnDecl,
		IsFnDecl:  isFnDecl,
	}
	return nil
}

// DeclareGlobalVar 执行顶层 var 的绑定创建 (提升期 undefined 初始化，随后由
// OP_STORE_GLOBAL 赋值)。var 是 globalThis 的自有属性 (不可配置)，因此
// IsLexical=false、IsVar=true —— 与 let/const 的词法绑定区分开。
//
//   - 与已有词法绑定 (let/const/class) 同名 → SyntaxError
//   - 与已有 var/函数声明/内建全局同名 → 允许 (var 可重声明)
//   - 已有函数声明且本次是提升占位 (undefined) → 保留函数值，避免覆盖
func (e *Environment) DeclareGlobalVar(name string, val object.Value) error {
	if existing, ok := e.store[name]; ok {
		if existing.IsLexical {
			return fmt.Errorf("SyntaxError: Identifier '%s' has already been declared", name)
		}
		if existing.IsFnDecl && val == object.UndefinedSingleton {
			return nil
		}
	}
	e.store[name] = Binding{Value: val, IsVar: true}
	return nil
}

// DeclareImplicit 创建"隐式赋值全局"绑定 (非严格模式下对未声明名字赋值，
// 或 globalThis.x = v 落到未声明绑定)。它是 globalThis 的自有属性且可配置
// (可 delete)，与 var 的不可配置相对。
func (e *Environment) DeclareImplicit(name string, val object.Value) {
	e.store[name] = Binding{Value: val, IsImplicit: true}
}

// BindingNames 返回全局环境中全部绑定名 (字典序，保证确定性)。
// 供 GlobalObject 枚举 globalThis 的自有属性。
func (e *Environment) BindingNames() []string {
	names := make([]string, 0, len(e.store))
	for k := range e.store {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// BindingInfoOf 返回全局环境里 name 绑定的元信息 (存在性 / 值 / 种类标记)。
func (e *Environment) BindingInfoOf(name string) object.GlobalBindingInfo {
	b, ok := e.store[name]
	if !ok {
		return object.GlobalBindingInfo{Exists: false}
	}
	info := object.GlobalBindingInfo{
		Exists:     true,
		Value:      b.Value,
		IsConst:    b.IsConst,
		IsLexical:  b.IsLexical,
		IsVar:      b.IsVar,
		IsFnDecl:   b.IsFnDecl,
		IsImplicit: b.IsImplicit,
	}
	if b.Desc != nil {
		info.HasDesc = true
		info.Desc = *b.Desc
	}
	return info
}

// SetGlobalBindingValue 直接改写已有全局绑定的值 (Object.defineProperty 等
// 精确控制路径使用)，同时记录描述符覆盖。
func (e *Environment) SetGlobalBindingValue(name string, val object.Value, desc object.PropertyDescriptor) bool {
	b, ok := e.store[name]
	if !ok {
		return false
	}
	b.Value = val
	d := desc
	b.Desc = &d
	e.store[name] = b
	return true
}

// DefineGlobalBinding 以完整描述符在全局环境上定义/新建绑定
// (Object.defineProperty(globalThis, ...) 用)。
func (e *Environment) DefineGlobalBinding(name string, desc object.PropertyDescriptor) error {
	b, ok := e.store[name]
	if !ok {
		b = Binding{}
	}
	d := desc
	b.Value = desc.Value
	b.Desc = &d
	e.store[name] = b
	return nil
}

// DeleteGlobalBinding 删除全局绑定。仅当它对应 globalThis 的自有可配置属性
// 时成功；不存在时也返回 true (规范 delete 对不存在属性返回 true)。
func (e *Environment) DeleteGlobalBinding(name string) bool {
	b, ok := e.store[name]
	if !ok {
		return true
	}
	if b.IsLexical {
		// 词法绑定不是 globalThis 的属性: delete 无效果但返回 true。
		return true
	}
	desc := e.OwnDescriptorOf(name, b)
	if !desc.Configurable {
		return false
	}
	delete(e.store, name)
	return true
}

// OwnDescriptorOf 计算某个绑定的 globalThis 自有属性描述符。
// 词法绑定 (let/const/class) 不是 globalThis 的自有属性，返回 Configurable=false
// 且不表示自身存在 (调用方需先用 IsLexical 判段)。
func (e *Environment) OwnDescriptorOf(name string, b Binding) object.PropertyDescriptor {
	if b.Desc != nil {
		return *b.Desc
	}
	switch {
	case b.IsVar, b.IsFnDecl:
		// 顶层 var / 函数声明: 可写、可枚举、不可配置。
		return object.PropertyDescriptor{
			Value: b.Value, Writable: true, Enumerable: true, Configurable: false,
		}
	case b.IsImplicit:
		// 隐式赋值全局: 可写、可枚举、可配置。
		return object.PropertyDescriptor{
			Value: b.Value, Writable: true, Enumerable: true, Configurable: true,
		}
	default:
		// 内建/宿主全局: 可写、不可枚举、可配置；const 内建 (NaN/Infinity/
		// undefined) 不可写也不可配置。
		if b.IsConst {
			return object.PropertyDescriptor{
				Value: b.Value, Writable: false, Enumerable: false, Configurable: false,
			}
		}
		return object.PropertyDescriptor{
			Value: b.Value, Writable: true, Enumerable: false, Configurable: true,
		}
	}
}

// IsConst 检查变量是否为 const 绑定。
// 沿作用域链查找。
func (e *Environment) IsConst(name string) bool {
	binding, ok := e.store[name]
	if ok {
		return binding.IsConst
	}
	if e.outer != nil {
		return e.outer.IsConst(name)
	}
	return false
}

// Outer 返回外层环境。
// 满足 object.Environment 接口。
func (e *Environment) Outer() object.Environment {
	if e.outer == nil {
		return nil
	}
	return e.outer
}

// GetInner 返回内部环境指针 (用于 VM 直接操作)
func (e *Environment) GetInner() *Environment {
	return e
}

// HasLocal 检查变量是否在当前作用域中定义 (不查外层)
func (e *Environment) HasLocal(name string) bool {
	_, ok := e.store[name]
	return ok
}

// CopyTo 将当前作用域的所有变量复制到目标环境 (用于函数调用参数传递)
func (e *Environment) CopyTo(target *Environment) {
	for name, binding := range e.store {
		target.store[name] = binding
	}
}
