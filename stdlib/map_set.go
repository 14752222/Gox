package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupMapSet 设置 Map, Set, WeakMap, WeakSet 构造器。
func setupMapSet(env *runtime.Environment) {
	// ===== Map =====
	mapProto := setupMapProto()
	object.SetMapProto(mapProto)

	mapFn := object.NewBuiltin("Map", func(args ...object.Value) object.Value {
		m := object.NewMap()
		// 可选: 传入可迭代对象初始化
		if len(args) > 0 {
			if arr, ok := args[0].(*object.Array); ok {
				for _, entry := range arr.Elements {
					if entryArr, ok := entry.(*object.Array); ok && len(entryArr.Elements) >= 2 {
						m.Set(entryArr.Elements[0], entryArr.Elements[1])
					}
				}
			}
		}
		return m
	})
	mapFn.SetProperty("prototype", mapProto)
	env.Declare("Map", mapFn, false)

	// Map.groupBy(items, callback) (ES2024): 键保持原始值
	mapFn.SetProperty("groupBy", object.NewBuiltin("groupBy", func(args ...object.Value) object.Value {
		return groupByImpl(args, true)
	}))

	// ===== Set =====
	setProto := setupSetProto()
	object.SetSetProto(setProto)

	setFn := object.NewBuiltin("Set", func(args ...object.Value) object.Value {
		s := object.NewSet()
		if len(args) > 0 {
			if arr, ok := args[0].(*object.Array); ok {
				for _, elem := range arr.Elements {
					s.Add(elem)
				}
			}
		}
		return s
	})
	setFn.SetProperty("prototype", setProto)
	env.Declare("Set", setFn, false)

	// ===== ES2025 Set 组合方法 =====
	// 这些方法在原型上实现 (见 setupSetProto 末尾的 setupSetCombinators)。
	setupSetCombinators(setProto)

	// ===== WeakMap =====
	// WeakMap 复用 *object.Map 的存储结构，但绑定**独立的实例级原型**:
	// 规范中 WeakMap 不可枚举，因此只暴露 get/set/has/delete，
	// 不提供 size / clear / forEach / keys / values / entries。
	// 过去这里既没设置 prototype 属性也没绑定实例原型，导致
	// new WeakMap().set(k, v) 找不到方法而失败。
	weakMapProto := setupWeakMapProto()
	weakMapFn := object.NewBuiltin("WeakMap", func(args ...object.Value) object.Value {
		m := object.NewMap()
		m.SetProto(weakMapProto)
		if len(args) > 0 {
			if arr, ok := args[0].(*object.Array); ok {
				for _, entry := range arr.Elements {
					if entryArr, ok := entry.(*object.Array); ok && len(entryArr.Elements) >= 2 {
						m.Set(entryArr.Elements[0], entryArr.Elements[1])
					}
				}
			}
		}
		return m
	})
	weakMapFn.SetProperty("prototype", weakMapProto)
	env.Declare("WeakMap", weakMapFn, false)

	// ===== WeakSet =====
	// 同理，WeakSet 只暴露 add/has/delete。
	weakSetProto := setupWeakSetProto()
	weakSetFn := object.NewBuiltin("WeakSet", func(args ...object.Value) object.Value {
		s := object.NewSet()
		s.SetProto(weakSetProto)
		if len(args) > 0 {
			if arr, ok := args[0].(*object.Array); ok {
				for _, elem := range arr.Elements {
					s.Add(elem)
				}
			}
		}
		return s
	})
	weakSetFn.SetProperty("prototype", weakSetProto)
	env.Declare("WeakSet", weakSetFn, false)
}

// setupWeakMapProto 创建 WeakMap.prototype (不可枚举，仅四个方法)。
func setupWeakMapProto() *object.Object {
	p := object.NewObject()
	p.SetProperty("get", object.NewBuiltinMethod("get", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok || len(args) == 0 {
			return object.UndefinedSingleton
		}
		if val, found := m.Get(args[0]); found {
			return val
		}
		return object.UndefinedSingleton
	}))
	p.SetProperty("set", object.NewBuiltinMethod("set", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return thisTypeError("WeakMap", "set", this)
		}
		key := object.Value(object.UndefinedSingleton)
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			key = args[0]
		}
		if len(args) > 1 {
			val = args[1]
		}
		m.Set(key, val)
		return this
	}))
	p.SetProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Has(args[0]))
	}))
	p.SetProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Delete(args[0]))
	}))
	return p
}

// setupWeakSetProto 创建 WeakSet.prototype (不可枚举，仅三个方法)。
func setupWeakSetProto() *object.Object {
	p := object.NewObject()
	p.SetProperty("add", object.NewBuiltinMethod("add", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return thisTypeError("WeakSet", "add", this)
		}
		if len(args) > 0 {
			s.Add(args[0])
		}
		return this
	}))
	p.SetProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Has(args[0]))
	}))
	p.SetProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok || len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Delete(args[0]))
	}))
	return p
}

func setupMapProto() *object.Object {
	p := object.NewObject()

	// set(key, value): 设置键值对，返回 Map 本身
	p.SetProperty("set", object.NewBuiltinMethod("set", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return this
		}
		key := object.Value(object.UndefinedSingleton)
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			key = args[0]
		}
		if len(args) > 1 {
			val = args[1]
		}
		m.Set(key, val)
		return this
	}))

	// get(key): 获取键对应的值
	p.SetProperty("get", object.NewBuiltinMethod("get", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.UndefinedSingleton
		}
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if val, found := m.Get(args[0]); found {
			return val
		}
		return object.UndefinedSingleton
	}))

	// has(key): 检查键是否存在
	p.SetProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.NewBoolean(false)
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Has(args[0]))
	}))

	// delete(key): 删除键值对
	p.SetProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.NewBoolean(false)
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Delete(args[0]))
	}))

	// clear(): 清空 Map
	p.SetProperty("clear", object.NewBuiltinMethod("clear", func(this object.Value, args ...object.Value) object.Value {
		if m, ok := this.(*object.Map); ok {
			m.Clear()
		}
		return object.UndefinedSingleton
	}))

	// forEach(callback): 遍历 Map
	p.SetProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for _, entry := range m.Entries {
			object.CallFunction(callback, this, entry.Value, entry.Key, this)
		}
		return object.UndefinedSingleton
	}))

	// keys(): 返回键的迭代器（简化为数组）
	p.SetProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		keys := make([]object.Value, len(m.Entries))
		for i, entry := range m.Entries {
			keys[i] = entry.Key
		}
		return object.NewArray(keys)
	}))

	// values(): 返回值的迭代器（简化为数组）
	p.SetProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		vals := make([]object.Value, len(m.Entries))
		for i, entry := range m.Entries {
			vals[i] = entry.Value
		}
		return object.NewArray(vals)
	}))

	// entries(): 返回键值对数组
	p.SetProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		m, ok := this.(*object.Map)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		entries := make([]object.Value, len(m.Entries))
		for i, entry := range m.Entries {
			entries[i] = object.NewArray([]object.Value{entry.Key, entry.Value})
		}
		return object.NewArray(entries)
	}))

	return p
}

func setupSetProto() *object.Object {
	p := object.NewObject()

	// add(value): 添加值，返回 Set 本身
	p.SetProperty("add", object.NewBuiltinMethod("add", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return this
		}
		if len(args) > 0 {
			s.Add(args[0])
		}
		return this
	}))

	// has(value): 检查值是否存在
	p.SetProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewBoolean(false)
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Has(args[0]))
	}))

	// delete(value): 删除值
	p.SetProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewBoolean(false)
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Delete(args[0]))
	}))

	// clear(): 清空 Set
	p.SetProperty("clear", object.NewBuiltinMethod("clear", func(this object.Value, args ...object.Value) object.Value {
		if s, ok := this.(*object.Set); ok {
			s.Clear()
		}
		return object.UndefinedSingleton
	}))

	// forEach(callback): 遍历 Set
	p.SetProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for _, val := range s.Values {
			object.CallFunction(callback, this, val, val, this)
		}
		return object.UndefinedSingleton
	}))

	// entries(): 返回 [value, value] 对数组
	p.SetProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		entries := make([]object.Value, len(s.Values))
		for i, val := range s.Values {
			entries[i] = object.NewArray([]object.Value{val, val})
		}
		return object.NewArray(entries)
	}))

	// values(): 返回值的数组
	p.SetProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		vals := make([]object.Value, len(s.Values))
		copy(vals, s.Values)
		return object.NewArray(vals)
	}))

	// keys(): Set 的 keys 与 values 相同
	p.SetProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewArray([]object.Value{})
		}
		vals := make([]object.Value, len(s.Values))
		copy(vals, s.Values)
		return object.NewArray(vals)
	}))

	return p
}

// setupSetCombinators 注册 ES2025 Set 组合方法:
// union / intersection / difference / symmetricDifference / isSubsetOf /
// isSupersetOf / isDisjointFrom。
func setupSetCombinators(p *object.Object) {
	// setLikeArg 将参数解析为可迭代的 *object.Set (简化: 仅接受 Set)
	parseSetArg := func(this object.Value, args []object.Value, method string) (*object.Set, object.Value) {
		s, ok := this.(*object.Set)
		if !ok {
			return nil, thisTypeError("Set", method, this)
		}
		if len(args) == 0 {
			return nil, object.NewTypeError("Set.prototype.%s: argument is required", method)
		}
		return s, args[0]
	}

	// newSetFromIterable 从任意可迭代值构建新 Set
	newSetFromIterable := func(v object.Value) (*object.Set, bool) {
		out := object.NewSet()
		next, ok := object.Iterate(v)
		if !ok {
			return nil, false
		}
		for {
			item, done := next()
			if done {
				return out, true
			}
			out.Add(item)
		}
	}

	// union(other): 两个集合的所有唯一元素
	p.SetProperty("union", object.NewBuiltinMethod("union", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "union")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.union: argument is not iterable")
		}
		out := object.NewSet()
		for _, v := range s.Values {
			out.Add(v)
		}
		for _, v := range other.Values {
			out.Add(v)
		}
		return out
	}))

	// intersection(other): 同时存在于两个集合的元素
	p.SetProperty("intersection", object.NewBuiltinMethod("intersection", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "intersection")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.intersection: argument is not iterable")
		}
		out := object.NewSet()
		for _, v := range s.Values {
			if other.Has(v) {
				out.Add(v)
			}
		}
		return out
	}))

	// difference(other): 在 this 中但不在 other 中的元素
	p.SetProperty("difference", object.NewBuiltinMethod("difference", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "difference")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.difference: argument is not iterable")
		}
		out := object.NewSet()
		for _, v := range s.Values {
			if !other.Has(v) {
				out.Add(v)
			}
		}
		return out
	}))

	// symmetricDifference(other): 只在一个集合中出现的元素
	p.SetProperty("symmetricDifference", object.NewBuiltinMethod("symmetricDifference", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "symmetricDifference")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.symmetricDifference: argument is not iterable")
		}
		out := object.NewSet()
		for _, v := range s.Values {
			if !other.Has(v) {
				out.Add(v)
			}
		}
		for _, v := range other.Values {
			if !s.Has(v) {
				out.Add(v)
			}
		}
		return out
	}))

	// isSubsetOf(other): this 的每个元素都在 other 中
	p.SetProperty("isSubsetOf", object.NewBuiltinMethod("isSubsetOf", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "isSubsetOf")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.isSubsetOf: argument is not iterable")
		}
		for _, v := range s.Values {
			if !other.Has(v) {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// isSupersetOf(other): other 的每个元素都在 this 中
	p.SetProperty("isSupersetOf", object.NewBuiltinMethod("isSupersetOf", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "isSupersetOf")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.isSupersetOf: argument is not iterable")
		}
		for _, v := range other.Values {
			if !s.Has(v) {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// isDisjointFrom(other): 无共同元素
	p.SetProperty("isDisjointFrom", object.NewBuiltinMethod("isDisjointFrom", func(this object.Value, args ...object.Value) object.Value {
		s, arg := parseSetArg(this, args, "isDisjointFrom")
		if s == nil {
			return arg
		}
		other, ok := newSetFromIterable(arg)
		if !ok {
			return object.NewTypeError("Set.prototype.isDisjointFrom: argument is not iterable")
		}
		for _, v := range s.Values {
			if other.Has(v) {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))
}
