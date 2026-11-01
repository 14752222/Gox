package stdlib

import (
	"math"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupMapSet 设置 Map, Set, WeakMap, WeakSet 构造器。
func setupMapSet(env *runtime.Environment) {
	// ===== Map =====
	mapProto := setupMapProto()
	object.SetMapProto(mapProto)
	// %Map.prototype%[@@toStringTag] = "Map" —— 实例的标签来自原型链。
	setToStringTag(mapProto, "Map")

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
	setToStringTag(setProto, "Set")

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
	setToStringTag(weakMapProto, "WeakMap")
	weakMapFn := object.NewBuiltin("WeakMap", func(args ...object.Value) object.Value {
		m := object.NewMap()
		m.Weak = true
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
	setToStringTag(weakSetProto, "WeakSet")
	weakSetFn := object.NewBuiltin("WeakSet", func(args ...object.Value) object.Value {
		s := object.NewSet()
		s.Weak = true
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

// weakKeyOK 实现 CanBeHeldWeakly: 仅对象与 Symbol (非 undefined/null/
// 原始数等) 可作 WeakMap 键 / WeakSet 成员, 其余必须 TypeError
// (test262 WeakMap set / WeakSet add 的 key-not-object-throws 族)。
func weakKeyOK(v object.Value) bool {
	// 一切对象语义值 (数组/Map/正则/包装对象等, 不止 *Object) 均可弱持有;
	// IsObjectLike 覆盖 *Object/*Array/*Map/*RegExp/... 全集。
	if object.IsObjectLike(v) {
		return true
	}
	if t, ok := v.(*object.Symbol); ok {
		// 注册 Symbol (Symbol.for) 不满足 CanBeHeldWeakly —— 全局注册表
		// 永久强持有, 弱容器语义要求抛 TypeError (throw-if-key-cannot-be-
		// held-weakly 族)。
		if _, registered := object.KeyForSymbol(t); registered {
			return false
		}
		return true
	}
	return false
}

// requireMapBrand: Map 品牌检查 —— this 必须是 *Map 且**不是** WeakMap
// (内部槽 [[MapData]] vs [[WeakMapData]] 互斥, rFXXbq brand 族)。
func requireMapBrand(this object.Value, method string) (*object.Map, object.Value) {
	m, ok := this.(*object.Map)
	if !ok || m.Weak {
		return nil, thisTypeError("Map", method, this)
	}
	return m, nil
}

// requireSetBrand: Set 品牌检查 (同 requireMapBrand)。
func requireSetBrand(this object.Value, method string) (*object.Set, object.Value) {
	s, ok := this.(*object.Set)
	if !ok || s.Weak {
		return nil, thisTypeError("Set", method, this)
	}
	return s, nil
}

// requireWeakMapBrand: WeakMap 品牌检查。
func requireWeakMapBrand(this object.Value, method string) (*object.Map, object.Value) {
	m, ok := this.(*object.Map)
	if !ok || !m.Weak {
		return nil, thisTypeError("WeakMap", method, this)
	}
	return m, nil
}

// requireWeakSetBrand: WeakSet 品牌检查。
func requireWeakSetBrand(this object.Value, method string) (*object.Set, object.Value) {
	s, ok := this.(*object.Set)
	if !ok || !s.Weak {
		return nil, thisTypeError("WeakSet", method, this)
	}
	return s, nil
}

// setupWeakMapProto 创建 WeakMap.prototype (不可枚举，仅 get/set/has/delete/
// getOrInsert/getOrInsertComputed)。brand 参数用于 thisTypeError 文案。
func setupWeakMapProto() *object.Object {
	p := object.NewObject()
	p.SetBuiltinProperty("get", object.NewBuiltinMethod("get", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "get")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if !weakKeyOK(args[0]) {
			return object.UndefinedSingleton
		}
		if val, found := m.Get(args[0]); found {
			return val
		}
		return object.UndefinedSingleton
	}))
	p.SetBuiltinProperty("set", object.NewBuiltinMethod("set", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "set")
		if err != nil {
			return err
		}
		key := object.Value(object.UndefinedSingleton)
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			key = args[0]
		}
		if len(args) > 1 {
			val = args[1]
		}
		if !weakKeyOK(key) {
			return object.NewTypeError("Invalid value used as weak map key")
		}
		m.Set(key, val)
		return this
	}))
	p.SetBuiltinProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "has")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if !weakKeyOK(args[0]) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Has(args[0]))
	}))
	p.SetBuiltinProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "delete")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if !weakKeyOK(args[0]) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Delete(args[0]))
	}))
	// getOrInsert / getOrInsertComputed (ES2026 upsert): 键已存在返回现值,
	// 否则插入 (普通版插 value, Computed 版先调 callbackFn(key) 再插其返回值)。
	p.SetBuiltinProperty("getOrInsert", object.NewBuiltinMethod("getOrInsert", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "getOrInsert")
		if err != nil {
			return err
		}
		key, val := weakUpsertArgs(args)
		if !weakKeyOK(key) {
			return object.NewTypeError("Invalid value used as weak map key")
		}
		if v, found := m.Get(key); found {
			return v
		}
		m.Set(key, val)
		return val
	}))
	p.SetBuiltinProperty("getOrInsertComputed", object.NewBuiltinMethod("getOrInsertComputed", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireWeakMapBrand(this, "getOrInsertComputed")
		if err != nil {
			return err
		}
		key, cb := weakUpsertArgs(args)
		if !weakKeyOK(key) {
			return object.NewTypeError("Invalid value used as weak map key")
		}
		// 规范 upsert: callbackfn 可调用性校验在查表之前 —— 键已存在且
		// callbackfn 不可调用时仍必须 TypeError (not-a-function-callbackfn-
		// throws 的 "key already present" 段)。
		if !object.IsCallable(cb) {
			return object.NewTypeError("%s is not a function", object.TypeOf(cb))
		}
		if v, found := m.Get(key); found {
			return v
		}
		val := object.CallFunction(cb, object.UndefinedSingleton, key)
		m.Set(key, val)
		return val
	}))
	return p
}

// weakUpsertArgs: (key, valueOrCallback) 两参取值, 缺参补 undefined。
func weakUpsertArgs(args []object.Value) (object.Value, object.Value) {
	var key, val object.Value = object.UndefinedSingleton, object.UndefinedSingleton
	if len(args) > 0 {
		key = args[0]
	}
	if len(args) > 1 {
		val = args[1]
	}
	return key, val
}

// setupWeakSetProto 创建 WeakSet.prototype (不可枚举，仅 add/has/delete)。
func setupWeakSetProto() *object.Object {
	p := object.NewObject()
	p.SetBuiltinProperty("add", object.NewBuiltinMethod("add", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireWeakSetBrand(this, "add")
		if err != nil {
			return err
		}
		if len(args) > 0 {
			if !weakKeyOK(args[0]) {
				return object.NewTypeError("Invalid value used in weak set")
			}
			s.Add(args[0])
		}
		return this
	}))
	p.SetBuiltinProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireWeakSetBrand(this, "has")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if !weakKeyOK(args[0]) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Has(args[0]))
	}))
	p.SetBuiltinProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireWeakSetBrand(this, "delete")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		if !weakKeyOK(args[0]) {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Delete(args[0]))
	}))
	return p
}

func setupMapProto() *object.Object {
	p := object.NewObject()

	// set(key, value): 设置键值对，返回 Map 本身
	p.SetBuiltinProperty("set", object.NewBuiltinMethod("set", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "set")
		if err != nil {
			return err
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
	p.SetBuiltinProperty("get", object.NewBuiltinMethod("get", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "get")
		if err != nil {
			return err
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
	p.SetBuiltinProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "has")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Has(args[0]))
	}))

	// delete(key): 删除键值对
	p.SetBuiltinProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "delete")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(m.Delete(args[0]))
	}))

	// clear(): 清空 Map
	p.SetBuiltinProperty("clear", object.NewBuiltinMethod("clear", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "clear")
		if err != nil {
			return err
		}
		m.Clear()
		return object.UndefinedSingleton
	}))

	// forEach(callback): 遍历 Map
	p.SetBuiltinProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "forEach")
		if err != nil {
			return err
		}
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for _, entry := range m.Entries {
			object.CallFunction(callback, this, entry.Value, entry.Key, this)
		}
		return object.UndefinedSingleton
	}))

	// keys(): 返回键的迭代器对象（规范要求真迭代器，for-of 快路径不走此方法）
	p.SetBuiltinProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "keys")
		if err != nil {
			return err
		}
		return object.NewMapKeyIterator(m)
	}))

	// values(): 返回值的迭代器对象
	p.SetBuiltinProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "values")
		if err != nil {
			return err
		}
		return object.NewMapValueIterator(m)
	}))

	// entries(): 返回 [key, value] 对的迭代器对象（%Map.prototype.entries%）
	p.SetBuiltinProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "entries")
		if err != nil {
			return err
		}
		return object.NewMapEntryIterator(m)
	}))

	// getOrInsert / getOrInsertComputed (ES2026 upsert): 键已存在返回现值,
	// 否则插入 value / callbackFn(key) 的返回值。
	p.SetBuiltinProperty("getOrInsert", object.NewBuiltinMethod("getOrInsert", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "getOrInsert")
		if err != nil {
			return err
		}
		key, val := weakUpsertArgs(args)
		if v, found := m.Get(key); found {
			return v
		}
		m.Set(key, val)
		return val
	}))
	p.SetBuiltinProperty("getOrInsertComputed", object.NewBuiltinMethod("getOrInsertComputed", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "getOrInsertComputed")
		if err != nil {
			return err
		}
		key, cb := weakUpsertArgs(args)
		if !object.IsCallable(cb) {
			return object.NewTypeError("%s is not a function", object.TypeOf(cb))
		}
		if v, found := m.Get(key); found {
			return v
		}
		val := object.CallFunction(cb, object.UndefinedSingleton, key)
		m.Set(key, val)
		return val
	}))

	// size: 规范是访问器 getter (非数据属性)。
	p.DefineNonEnumerableAccessor("size", object.NewBuiltinMethod("get size", func(this object.Value, args ...object.Value) object.Value {
		m, err := requireMapBrand(this, "size")
		if err != nil {
			return err
		}
		return object.NewInt(int64(len(m.Entries)))
	}), nil)

	return p
}

func setupSetProto() *object.Object {
	p := object.NewObject()

	// add(value): 添加值，返回 Set 本身
	p.SetBuiltinProperty("add", object.NewBuiltinMethod("add", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireSetBrand(this, "add")
		if err != nil {
			return err
		}
		if len(args) > 0 {
			s.Add(args[0])
		}
		return this
	}))

	// has(value): 检查值是否存在
	p.SetBuiltinProperty("has", object.NewBuiltinMethod("has", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireSetBrand(this, "has")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Has(args[0]))
	}))

	// delete(value): 删除值
	p.SetBuiltinProperty("delete", object.NewBuiltinMethod("delete", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireSetBrand(this, "delete")
		if err != nil {
			return err
		}
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.Delete(args[0]))
	}))

	// clear(): 清空 Set
	p.SetBuiltinProperty("clear", object.NewBuiltinMethod("clear", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireSetBrand(this, "clear")
		if err != nil {
			return err
		}
		s.Clear()
		return object.UndefinedSingleton
	}))

	// forEach(callback): 遍历 Set
	p.SetBuiltinProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		s, err := requireSetBrand(this, "forEach")
		if err != nil {
			return err
		}
		if len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for _, val := range s.Values {
			object.CallFunction(callback, this, val, val, this)
		}
		return object.UndefinedSingleton
	}))

	// entries(): 返回 [value, value] 对的迭代器对象（规范 %Set.prototype.entries%）
	p.SetBuiltinProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return thisTypeError("Set", "entries", this)
		}
		return object.NewSetEntryIterator(s)
	}))

	// values(): 返回值的迭代器对象
	p.SetBuiltinProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return thisTypeError("Set", "values", this)
		}
		return object.NewSetIterator(s)
	}))

	// keys(): Set 的 keys 与 values 同为值迭代器
	p.SetBuiltinProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return thisTypeError("Set", "keys", this)
		}
		return object.NewSetIterator(s)
	}))

	// size: 规范是访问器 getter (非数据属性)。
	p.DefineNonEnumerableAccessor("size", object.NewBuiltinMethod("get size", func(this object.Value, args ...object.Value) object.Value {
		s, ok := this.(*object.Set)
		if !ok {
			return object.NewErrorWithName("TypeError", "Method Set.prototype.size called on incompatible receiver")
		}
		return object.NewInt(int64(len(s.Values)))
	}), nil)

	return p
}

// setupSetCombinators 注册 ES2025 Set 组合方法:
// union / intersection / difference / symmetricDifference / isSubsetOf /
// isSupersetOf / isDisjointFrom。
func setupSetCombinators(p *object.Object) {
	// getSetRecord 实现规范的 GetSetRecord(other)：
	//  1. Type(other) 必须是 Object，否则 TypeError；
	//  2. size = Get(other, "size") → ToNumber —— **读取顺序在 has/keys 之前**
	//     （test262 GetSetRecord coerces size / size-is-a-number 盯这条）；
	//  3. NaN → TypeError；ToIntegerOrInfinity 截断；
	//  4. has = Get(other, "has")，必须可调用；
	//  5. keys = Get(other, "keys")，必须可调用。
	// 这样 Map/Array/普通对象/class 实例都能按 set-like 协议参与组合，
	// 而非旧实现"仅接受可迭代"（array-throws / allows-set-like-* 族）。
	type setRecord struct {
		size int64
		has  object.Value
		keys object.Value
		obj  object.Value
	}
	getProp := func(v object.Value, name string) (object.Value, bool) {
		if g, ok := v.(interface {
			GetProperty(string) (object.Value, bool)
		}); ok {
			return g.GetProperty(name)
		}
		return nil, false
	}
	toNum := func(v object.Value) (float64, object.Value) {
		if object.IsObjectLike(v) {
			return toNumberViaPrimitive(v)
		}
		if _, ok := v.(*object.Symbol); ok {
			return 0, object.NewTypeError("Cannot convert a Symbol value to a number")
		}
		return toFloat(v), nil
	}
	getSetRecord := func(v object.Value, method string) (*setRecord, object.Value) {
		if !object.IsObjectLike(v) {
			return nil, object.NewTypeError("Set.prototype.%s: argument must be an object", method)
		}
		sizeV, found := getProp(v, "size")
		if !found {
			sizeV = object.UndefinedSingleton
		}
		numSize, errv := toNum(sizeV)
		if errv != nil {
			return nil, errv
		}
		if math.IsNaN(numSize) {
			return nil, object.NewTypeError("Set.prototype.%s: 'size' is not a number", method)
		}
		has, _ := getProp(v, "has")
		if has == nil || !object.IsCallable(has) {
			return nil, object.NewTypeError("Set.prototype.%s: 'has' is not callable", method)
		}
		keys, _ := getProp(v, "keys")
		if keys == nil || !object.IsCallable(keys) {
			return nil, object.NewTypeError("Set.prototype.%s: 'keys' is not callable", method)
		}
		return &setRecord{size: int64(numSize), has: has, keys: keys, obj: v}, nil
	}

	// parseSetRecord = RequireInternalSlot(this, [[SetData]]) + GetSetRecord(other)
	parseSetRecord := func(this object.Value, args []object.Value, method string) (*object.Set, *setRecord, object.Value) {
		s, ok := this.(*object.Set)
		if !ok {
			return nil, nil, thisTypeError("Set", method, this)
		}
		if len(args) == 0 {
			return nil, nil, object.NewTypeError("Set.prototype.%s: argument is required", method)
		}
		r, errv := getSetRecord(args[0], method)
		if errv != nil {
			return nil, nil, errv
		}
		return s, r, nil
	}

	// recHas 调用 record.has(value)，用户代码抛错原样传播。
	recHas := func(r *setRecord, v object.Value) (bool, object.Value) {
		res := object.CallFunction(r.has, r.obj, v)
		if t := callbackThrown(); t != nil {
			return false, t
		}
		return res.IsTruthy(), nil
	}

	// getKeysIter 调用 record.keys() 得到迭代器对象（GetIteratorFromMethod 简化，
	// 要求 next 可调用）。
	getKeysIter := func(r *setRecord) (iterObj, nextFn object.Value, errv object.Value) {
		res := object.CallFunction(r.keys, r.obj)
		if t := callbackThrown(); t != nil {
			return nil, nil, t
		}
		nextFn, found := getProp(res, "next")
		if !found || !object.IsCallable(nextFn) {
			return nil, nil, object.NewTypeError("Set.prototype: keys() result has no callable next")
		}
		return res, nextFn, nil
	}

	// iterStep 执行一次 next()，返回 (value, done, err)。
	iterStep := func(iterObj, nextFn object.Value) (object.Value, bool, object.Value) {
		res := object.CallFunction(nextFn, iterObj)
		if t := callbackThrown(); t != nil {
			return nil, false, t
		}
		doneV, _ := getProp(res, "done")
		done := doneV != nil && doneV.IsTruthy()
		val, _ := getProp(res, "value")
		return val, done, nil
	}

	// iterClose 在早退/异常路径调用 iterator.return()（异常被吞，规范如此）。
	// **必须消费回调错误槽**：return() 抛错（test262 专门有 return-throws
	// 族）若留在槽里会污染同进程后续用例（JSON/parse/duplicate-proto 被
	// 无辜带挂的根因）。
	iterClose := func(iterObj object.Value) {
		if ret, found := getProp(iterObj, "return"); found && object.IsCallable(ret) {
			object.CallFunction(ret, iterObj)
			object.TakeCallbackErrorValue()
		}
	}

	// normZero 实现 -0 → +0（SameValueZero 下等价，且 SetData 归一化要求）。
	normZero := func(v object.Value) object.Value {
		if n, ok := v.(*object.Number); ok && n.Value == 0 {
			return object.NewNumber(0)
		}
		return v
	}

	// union(other): resultSetData = copy(this) → 遍历 keys(): -0 归一后
	// result 中不存在则追加。result 对象在迭代**之后**才创建（规范 step 8）。
	p.SetBuiltinProperty("union", object.NewBuiltinMethod("union", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "union")
		if s == nil {
			return errv
		}
		out := object.NewSet()
		for _, v := range s.Values {
			out.Add(v)
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			if !out.Has(normZero(k)) {
				out.Add(normZero(k))
			}
		}
		return out
	}))

	// intersection(other): this.size ≤ other.size 时遍历 this 用 rec.has；
	// 否则遍历 other 的 keys() 用 this 的 SetDataHas。两策略产出顺序不同，
	// 规范如此。
	p.SetBuiltinProperty("intersection", object.NewBuiltinMethod("intersection", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "intersection")
		if s == nil {
			return errv
		}
		out := object.NewSet()
		if len(s.Values) <= int(r.size) {
			for _, v := range s.Values {
				has, errv := recHas(r, v)
				if errv != nil {
					return errv
				}
				if has {
					out.Add(v)
				}
			}
			return out
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			if s.Has(normZero(k)) {
				out.Add(k)
			}
		}
		return out
	}))

	// difference(other): this.size ≤ other.size 时遍历 this 用 rec.has 过滤；
	// 否则复制 this 再遍历 keys() 删除（此路径**绝不调用** rec.has）。
	p.SetBuiltinProperty("difference", object.NewBuiltinMethod("difference", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "difference")
		if s == nil {
			return errv
		}
		out := object.NewSet()
		if len(s.Values) <= int(r.size) {
			for _, v := range s.Values {
				has, errv := recHas(r, v)
				if errv != nil {
					return errv
				}
				if !has {
					out.Add(v)
				}
			}
			return out
		}
		resultSet := object.NewSet()
		for _, v := range s.Values {
			resultSet.Add(v)
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			resultSet.Delete(normZero(k))
		}
		for _, v := range resultSet.Values {
			out.Add(v)
		}
		return out
	}))

	// symmetricDifference(other): resultSetData = copy(this) → 遍历 keys()：
	// 已在结果中则删除、否则追加。**全程不调用 rec.has**。
	p.SetBuiltinProperty("symmetricDifference", object.NewBuiltinMethod("symmetricDifference", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "symmetricDifference")
		if s == nil {
			return errv
		}
		out := object.NewSet()
		resultSet := object.NewSet()
		for _, v := range s.Values {
			resultSet.Add(v)
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			if resultSet.Has(normZero(k)) {
				resultSet.Delete(normZero(k))
			} else {
				resultSet.Add(normZero(k))
			}
		}
		for _, v := range resultSet.Values {
			out.Add(v)
		}
		return out
	}))

	// isSubsetOf(other): this 元素数 > other.size 直接 false；否则遍历 this
	// 用 rec.has（has 调用期间 this 可能增长，动态取长度）。
	p.SetBuiltinProperty("isSubsetOf", object.NewBuiltinMethod("isSubsetOf", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "isSubsetOf")
		if s == nil {
			return errv
		}
		if len(s.Values) > int(r.size) {
			return object.NewBoolean(false)
		}
		for idx := 0; idx < len(s.Values); idx++ {
			has, errv := recHas(r, s.Values[idx])
			if errv != nil {
				return errv
			}
			if !has {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// isSupersetOf(other): this 元素数 < other.size 直接 false；否则遍历
	// keys()，任一元素不在 this 中 → IteratorClose + false。
	p.SetBuiltinProperty("isSupersetOf", object.NewBuiltinMethod("isSupersetOf", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "isSupersetOf")
		if s == nil {
			return errv
		}
		if len(s.Values) < int(r.size) {
			return object.NewBoolean(false)
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			if !s.Has(normZero(k)) {
				iterClose(iterObj)
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// isDisjointFrom(other): this.size ≤ other.size 时遍历 this 用 rec.has；
	// 否则遍历 keys() 用 this 的 SetDataHas。
	p.SetBuiltinProperty("isDisjointFrom", object.NewBuiltinMethod("isDisjointFrom", func(this object.Value, args ...object.Value) object.Value {
		s, r, errv := parseSetRecord(this, args, "isDisjointFrom")
		if s == nil {
			return errv
		}
		if len(s.Values) <= int(r.size) {
			for _, v := range s.Values {
				has, errv := recHas(r, v)
				if errv != nil {
					return errv
				}
				if has {
					return object.NewBoolean(false)
				}
			}
			return object.NewBoolean(true)
		}
		iterObj, nextFn, errv := getKeysIter(r)
		if errv != nil {
			return errv
		}
		for {
			k, done, errv := iterStep(iterObj, nextFn)
			if errv != nil {
				iterClose(iterObj)
				return errv
			}
			if done {
				break
			}
			if s.Has(normZero(k)) {
				iterClose(iterObj)
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))
}
