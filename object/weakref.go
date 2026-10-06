package object

import "sync"

// WeakRef / FinalizationRegistry / GlobalObject。
//
// WeakRef 语义说明: 解释器中的对象由 Go GC 管理，解释器内部持有完整的
// 强引用链 (常量池/环境/栈)，因此"目标是否仍被引用"在 JS 层不可观测。
// 这里保持 API 兼容: deref() 在目标存活期间返回目标；因为无法感知回收，
// 只要运行中 deref() 总是返回目标 (与"无显式清除时的最坏存活假设"一致)。

// WeakRef 弱引用对象。
type WeakRef struct {
	Target Value
}

func (w *WeakRef) Type() ObjectType          { return WEAKREF_OBJ }
func (w *WeakRef) Inspect() string           { return "[object WeakRef]" }
func (w *WeakRef) IsTruthy() bool            { return true }
func (w *WeakRef) SetProperty(string, Value) {}

func (w *WeakRef) GetProperty(name string) (Value, bool) {
	switch name {
	case "deref":
		return NewBuiltin("deref", func(args ...Value) Value {
			if w.Target == nil {
				return UndefinedSingleton
			}
			return w.Target
		}), true
	}
	return nil, false
}

func NewWeakRef(target Value) *WeakRef { return &WeakRef{Target: target} }

// ===== FinalizationRegistry =====

type finRegistryEntry struct {
	target          Value
	held            Value
	unregisterToken Value
}

// FinalizationRegistry 注册对象终结回调。
// 由于回收不可观测，cleanup 回调不会自动触发。
type FinalizationRegistry struct {
	Cleanup Value
	entries []finRegistryEntry
	mu      sync.Mutex
}

func (r *FinalizationRegistry) Type() ObjectType          { return OBJECT_OBJ }
func (r *FinalizationRegistry) Inspect() string           { return "[object FinalizationRegistry]" }
func (r *FinalizationRegistry) IsTruthy() bool            { return true }
func (r *FinalizationRegistry) SetProperty(string, Value) {}

func (r *FinalizationRegistry) GetProperty(name string) (Value, bool) {
	switch name {
	case "register":
		return NewBuiltin("register", func(args ...Value) Value {
			if len(args) == 0 || !IsObjectValue(args[0]) {
				return NewTypeError("FinalizationRegistry.register: target must be an object")
			}
			var held Value = UndefinedSingleton
			if len(args) > 1 {
				held = args[1]
			}
			token := args[0]
			if len(args) > 2 && args[2] != UndefinedSingleton {
				token = args[2]
			}
			r.mu.Lock()
			r.entries = append(r.entries, finRegistryEntry{
				target:          args[0],
				held:            held,
				unregisterToken: token,
			})
			r.mu.Unlock()
			return UndefinedSingleton
		}), true
	case "unregister":
		return NewBuiltin("unregister", func(args ...Value) Value {
			if len(args) == 0 || !IsObjectValue(args[0]) {
				return NewTypeError("FinalizationRegistry.unregister: token must be an object")
			}
			r.mu.Lock()
			kept := r.entries[:0]
			for _, e := range r.entries {
				if looseEqualsRef(e.unregisterToken, args[0]) {
					continue
				}
				kept = append(kept, e)
			}
			r.entries = kept
			r.mu.Unlock()
			return TrueSingleton
		}), true
	case "cleanupSome":
		return NewBuiltin("cleanupSome", func(args ...Value) Value {
			// 回收不可观测，无挂起的清理项
			return UndefinedSingleton
		}), true
	}
	return nil, false
}

func NewFinalizationRegistry(cleanup Value) *FinalizationRegistry {
	return &FinalizationRegistry{Cleanup: cleanup}
}

// looseEqualsRef 宽松比较两个 token 是否同一对象/同值。
func looseEqualsRef(a, b Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	switch av := a.(type) {
	case *String:
		if bv, ok := b.(*String); ok {
			return av.Value == bv.Value
		}
	case *Number:
		if bv, ok := b.(*Number); ok {
			return av.Value == bv.Value
		}
	}
	return a == b
}

// isObjectValue 判断值是否为对象类型 (WeakRef/Registry 的目标检查用)。
func IsObjectValue(v Value) bool {
	switch v.(type) {
	case *Object, *Array, *Map, *Set, *Error, *Closure, *BuiltinFunction,
		*BuiltinMethod, *RegExp, *Promise, *Generator, *Proxy, *JSIterator,
		*WeakRef, *GlobalObject, *FinalizationRegistry:
		return true
	}
	return false
}

// ===== GlobalObject =====

// GlobalBindingInfo 描述全局环境里一个绑定的元信息 (供 globalThis 的自有
// 属性查询)。由 runtime.Environment 构造。
type GlobalBindingInfo struct {
	Exists     bool
	Value      Value
	IsConst    bool
	IsLexical  bool // let/const/class: 词法绑定，不是 globalThis 的自有属性
	IsVar      bool // 顶层 var: 自有属性，不可配置
	IsFnDecl   bool // 顶层函数声明: 自有属性，不可配置
	IsImplicit bool // 隐式赋值全局: 自有属性，可配置
	HasDesc    bool // 是否带显式描述符覆盖 (defineProperty)
	Desc       PropertyDescriptor
}

// GlobalBindingProvider 由全局环境 (runtime.Environment) 实现，供
// GlobalObject 枚举绑定名与查询绑定元信息。
type GlobalBindingProvider interface {
	BindingNames() []string
	BindingInfoOf(name string) GlobalBindingInfo
}

// GlobalObject 是由全局环境背书的对象，作为 globalThis 暴露。
// 读属性 = 在全局环境查找绑定 (未命中回退 [[Prototype]])；
// 写属性 = 更新或创建全局绑定。
//
// 自有属性 (own property) 语义: 全局环境对象记录里的绑定 —— 顶层 var /
// 函数声明 / 隐式赋值全局 / 内建全局 —— 都是 globalThis 的自有属性；
// 顶层 let/const/class 只存在于全局词法环境，不是 globalThis 的属性
// (见 runtime.Environment 的 IsLexical 标记)。
type GlobalObject struct {
	Env   Environment
	Proto Value // [[Prototype]]，装配期指向 %Object.prototype%
}

func (g *GlobalObject) Type() ObjectType { return GLOBAL_OBJ }
func (g *GlobalObject) Inspect() string  { return "[object globalThis]" }
func (g *GlobalObject) IsTruthy() bool   { return true }

func (g *GlobalObject) GetProperty(name string) (Value, bool) {
	if v, ok := g.Env.Get(name); ok {
		// 访问器绑定 (Object.defineProperty(globalThis, k, {get}))。
		if acc, isAcc := v.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, g), true
			}
			return UndefinedSingleton, true
		}
		return v, true
	}
	// 未命中全局绑定: 回退 [[Prototype]] (Object.prototype），使
	// globalThis.hasOwnProperty / toString / valueOf 等方法可达。
	if g.Proto != nil {
		return g.Proto.GetProperty(name)
	}
	return nil, false
}

func (g *GlobalObject) SetProperty(name string, val Value) {
	// 已存在的全局绑定遵循其描述符: 访问器调用 setter；不可写数据属性静默失败。
	if d, ok := g.OwnDescriptor(name); ok {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, g, val)
			}
			return
		}
		if !d.Writable {
			return
		}
	}
	if err := g.Env.Set(name, val); err != nil {
		// 未声明的名字: 非严格模式下隐式创建全局变量。
		if imp, ok := g.Env.(interface {
			DeclareImplicit(string, Value)
		}); ok {
			imp.DeclareImplicit(name, val)
			return
		}
		g.Env.Declare(name, val, false)
	}
}

// bindingProvider 取全局环境的绑定枚举能力。
func (g *GlobalObject) bindingProvider() (GlobalBindingProvider, bool) {
	p, ok := g.Env.(GlobalBindingProvider)
	return p, ok
}

// OwnKeys 返回 globalThis 的自有属性名 (不含词法绑定 let/const/class)。
func (g *GlobalObject) OwnKeys() []string {
	p, ok := g.bindingProvider()
	if !ok {
		return nil
	}
	all := p.BindingNames()
	out := make([]string, 0, len(all))
	for _, n := range all {
		if !p.BindingInfoOf(n).IsLexical {
			out = append(out, n)
		}
	}
	return out
}

// HasOwn 检查 name 是否为 globalThis 的自有属性。
func (g *GlobalObject) HasOwn(name string) bool {
	_, ok := g.OwnDescriptor(name)
	return ok
}

// OwnDescriptor 返回 globalThis 上 name 的自有属性描述符。词法绑定
// (let/const/class) 不是自有属性，返回 false。
func (g *GlobalObject) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	p, ok := g.bindingProvider()
	if !ok {
		return PropertyDescriptor{}, false
	}
	info := p.BindingInfoOf(name)
	if !info.Exists || info.IsLexical {
		return PropertyDescriptor{}, false
	}
	return g.descriptorFor(name, info), true
}

// descriptorFor 由绑定元信息计算自有属性描述符。
func (g *GlobalObject) descriptorFor(name string, info GlobalBindingInfo) PropertyDescriptor {
	if info.HasDesc {
		return info.Desc
	}
	// 访问器绑定: Value 存 *Accessor。
	if acc, isAcc := info.Value.(*Accessor); isAcc {
		_ = acc
		return PropertyDescriptor{Value: info.Value, Writable: false, Enumerable: true, Configurable: true}
	}
	switch {
	case info.IsVar, info.IsFnDecl:
		return PropertyDescriptor{Value: info.Value, Writable: true, Enumerable: true, Configurable: false}
	case info.IsImplicit:
		return PropertyDescriptor{Value: info.Value, Writable: true, Enumerable: true, Configurable: true}
	default:
		if info.IsConst {
			return PropertyDescriptor{Value: info.Value, Writable: false, Enumerable: false, Configurable: false}
		}
		return PropertyDescriptor{Value: info.Value, Writable: true, Enumerable: false, Configurable: true}
	}
}

// EnumerableOwnKeys 返回 globalThis 上可枚举的自有属性名。
func (g *GlobalObject) EnumerableOwnKeys() []string {
	p, ok := g.bindingProvider()
	if !ok {
		return nil
	}
	all := p.BindingNames()
	out := make([]string, 0, len(all))
	for _, n := range all {
		d, ok := g.OwnDescriptor(n)
		if ok && d.Enumerable {
			out = append(out, n)
		}
	}
	return out
}

// DeleteOwn 删除 globalThis 的自有可配置属性。
func (g *GlobalObject) DeleteOwn(name string) bool {
	if m, ok := g.Env.(interface {
		DeleteGlobalBinding(string) bool
	}); ok {
		return m.DeleteGlobalBinding(name)
	}
	return true
}

// DefineGlobal 以描述符在 globalThis 上定义/新建自有属性。
func (g *GlobalObject) DefineGlobal(name string, desc PropertyDescriptor) bool {
	if m, ok := g.Env.(interface {
		DefineGlobalBinding(string, PropertyDescriptor) error
	}); ok {
		_ = m.DefineGlobalBinding(name, desc)
		return true
	}
	return false
}

func NewGlobalObject(env Environment) *GlobalObject { return &GlobalObject{Env: env} }
