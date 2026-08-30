package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
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

	// ===== WeakMap (简化: 与 Map 相同行为) =====
	weakMapFn := object.NewBuiltin("WeakMap", func(args ...object.Value) object.Value {
		m := object.NewMap()
		return m
	})
	env.Declare("WeakMap", weakMapFn, false)

	// ===== WeakSet (简化: 与 Set 相同行为) =====
	weakSetFn := object.NewBuiltin("WeakSet", func(args ...object.Value) object.Value {
		s := object.NewSet()
		return s
	})
	env.Declare("WeakSet", weakSetFn, false)
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
