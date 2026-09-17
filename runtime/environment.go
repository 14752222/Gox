// Package runtime 提供 JavaScript 运行时的基础组件:
// - Environment: 作用域环境链 (块作用域 + const 检查)
// - Iterator: 迭代器协议 (for...of)
package runtime

import (
	"fmt"

	"github.com/14752222/Gox/object"
)

// Binding 表示变量绑定。
type Binding struct {
	Value   object.Value
	IsConst bool // const 声明的变量不可重新赋值
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
