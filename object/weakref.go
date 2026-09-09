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

// GlobalObject 是由全局环境背书的对象，作为 globalThis 暴露。
// 读属性 = 在全局环境查找绑定；写属性 = 更新或创建全局绑定。
type GlobalObject struct {
	Env Environment
}

func (g *GlobalObject) Type() ObjectType { return GLOBAL_OBJ }
func (g *GlobalObject) Inspect() string  { return "[object globalThis]" }
func (g *GlobalObject) IsTruthy() bool   { return true }

func (g *GlobalObject) GetProperty(name string) (Value, bool) {
	return g.Env.Get(name)
}

func (g *GlobalObject) SetProperty(name string, val Value) {
	if err := g.Env.Set(name, val); err != nil {
		// 未声明的名字: 非严格模式下隐式创建全局变量
		g.Env.Declare(name, val, false)
	}
}

func NewGlobalObject(env Environment) *GlobalObject { return &GlobalObject{Env: env} }
