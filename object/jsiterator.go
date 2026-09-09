package object

import "strconv"

// JSIterator 是 JS 层可见的迭代器对象。
//
// Array.prototype.keys/values/entries、ES2025 Iterator helpers、
// for...of 与展开运算符都基于它。核心是一个 NextFn 闭包:
// 每次调用返回 (value, done)。
//
// GetProperty("next") 返回符合迭代器协议的结果对象 {value, done}，
// GetProperty("Symbol(Symbol.iterator)") 返回自身，使迭代器天然可迭代。
type JSIterator struct {
	Name string
	// NextFn 返回 (value, done)。done 为 true 时迭代结束，value 应为 undefined。
	NextFn func() (Value, bool)
	// closed 标记 return() 已被调用 (提前终止)。
	closed bool
}

func (it *JSIterator) Type() ObjectType { return ITERATOR_OBJ }
func (it *JSIterator) Inspect() string {
	if it.Name != "" {
		return "[object " + it.Name + " Iterator]"
	}
	return "[object Iterator]"
}
func (it *JSIterator) IsTruthy() bool { return true }

// Next 执行一次迭代，内部使用 (供 runtime 直接驱动)。
func (it *JSIterator) Next() (Value, bool) {
	if it.closed || it.NextFn == nil {
		return UndefinedSingleton, true
	}
	v, done := it.NextFn()
	if v == nil {
		v = UndefinedSingleton
	}
	return v, done
}

func (it *JSIterator) GetProperty(name string) (Value, bool) {
	switch name {
	case "next":
		return NewBuiltin("next", func(args ...Value) Value {
			v, done := it.Next()
			return NewIteratorResult(v, done)
		}), true
	case "return":
		return NewBuiltin("return", func(args ...Value) Value {
			it.closed = true
			return NewIteratorResult(UndefinedSingleton, true)
		}), true
	case NewSymbol("Symbol.iterator").Inspect():
		return NewBuiltin("[Symbol.iterator]", func(args ...Value) Value {
			return it
		}), true
	default:
		// ES2025 Iterator helpers (map/filter/take/drop/flatMap/reduce/
		// toArray/forEach/some/every/find)
		if v, ok := it.esIteratorHelper(name); ok {
			return v, true
		}
	}
	return nil, false
}

func (it *JSIterator) SetProperty(name string, val Value) {}

// NewIteratorResult 构造迭代器协议的结果对象 {value, done}。
func NewIteratorResult(v Value, done bool) *Object {
	o := NewObject()
	o.SetProperty("value", v)
	o.SetProperty("done", NewBoolean(done))
	return o
}

// NewArrayIterator 迭代数组元素。
func NewArrayIterator(arr *Array) *JSIterator {
	i := 0
	return &JSIterator{Name: "Array", NextFn: func() (Value, bool) {
		if i >= len(arr.Elements) {
			return UndefinedSingleton, true
		}
		v := arr.Elements[i]
		i++
		if v == nil {
			v = UndefinedSingleton
		}
		return v, false
	}}
}

// NewArrayIndexIterator 迭代数组索引 (Array.prototype.keys)。
func NewArrayIndexIterator(arr *Array) *JSIterator {
	i := 0
	return &JSIterator{Name: "Array", NextFn: func() (Value, bool) {
		if i >= len(arr.Elements) {
			return UndefinedSingleton, true
		}
		v := NewInt(int64(i))
		i++
		return v, false
	}}
}

// NewArrayEntryIterator 迭代 [index, element] 对 (Array.prototype.entries)。
func NewArrayEntryIterator(arr *Array) *JSIterator {
	i := 0
	return &JSIterator{Name: "Array", NextFn: func() (Value, bool) {
		if i >= len(arr.Elements) {
			return UndefinedSingleton, true
		}
		v := NewArray([]Value{NewInt(int64(i)), arr.Elements[i]})
		i++
		return v, false
	}}
}

// NewMapEntryIterator 迭代 Map 的 [key, value] 对。
func NewMapEntryIterator(m *Map) *JSIterator {
	i := 0
	return &JSIterator{Name: "Map", NextFn: func() (Value, bool) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if i >= len(m.Entries) {
			return UndefinedSingleton, true
		}
		e := m.Entries[i]
		v := NewArray([]Value{e.Key, e.Value})
		i++
		return v, false
	}}
}

// NewMapKeyIterator 迭代 Map 的键。
func NewMapKeyIterator(m *Map) *JSIterator {
	i := 0
	return &JSIterator{Name: "Map", NextFn: func() (Value, bool) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if i >= len(m.Entries) {
			return UndefinedSingleton, true
		}
		v := m.Entries[i].Key
		i++
		return v, false
	}}
}

// NewMapValueIterator 迭代 Map 的值。
func NewMapValueIterator(m *Map) *JSIterator {
	i := 0
	return &JSIterator{Name: "Map", NextFn: func() (Value, bool) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if i >= len(m.Entries) {
			return UndefinedSingleton, true
		}
		v := m.Entries[i].Value
		i++
		return v, false
	}}
}

// NewSetIterator 迭代 Set 的值。
func NewSetIterator(s *Set) *JSIterator {
	i := 0
	return &JSIterator{Name: "Set", NextFn: func() (Value, bool) {
		s.mu.RLock()
		defer s.mu.RUnlock()
		if i >= len(s.Values) {
			return UndefinedSingleton, true
		}
		v := s.Values[i]
		i++
		return v, false
	}}
}

// NewStringCodePointIterator 按码点迭代字符串 (for...of 的语义)。
func NewStringCodePointIterator(s *String) *JSIterator {
	runes := []rune(s.Value)
	i := 0
	return &JSIterator{NextFn: func() (Value, bool) {
		if i >= len(runes) {
			return UndefinedSingleton, true
		}
		v := NewString(string(runes[i]))
		i++
		return v, false
	}}
}

// Iterate 返回一个驱动任意 JS 可迭代值的 next 闭包。
// 支持 Array/String/Map/Set/JSIterator 以及实现 [Symbol.iterator] 的对象。
// 不可迭代时 ok 为 false。
func Iterate(v Value) (next func() (Value, bool), ok bool) {
	switch t := v.(type) {
	case *Array:
		it := NewArrayIterator(t)
		return it.Next, true
	case *String:
		it := NewStringCodePointIterator(t)
		return it.Next, true
	case *Map:
		it := NewMapEntryIterator(t)
		return it.Next, true
	case *Set:
		it := NewSetIterator(t)
		return it.Next, true
	case *JSIterator:
		return t.Next, true
	case *TypedArray:
		it := NewArrayIterator(t.ToArray())
		return it.Next, true
	case *Object:
		if fn, found := t.GetProperty(NewSymbol("Symbol.iterator").Inspect()); found && IsCallable(fn) {
			res := CallFunction(fn, v)
			if jit, isIt := res.(*JSIterator); isIt {
				return jit.Next, true
			}
		}
	}
	return nil, false
}

// esIteratorHelper 在 JSIterator 上查找 ES2025 Iterator helper 方法时调用。
// fnName 不是 helper 方法名时返回 ok=false。
func (it *JSIterator) esIteratorHelper(name string) (Value, bool) {
	switch name {
	case "map":
		return NewBuiltin("map", func(args ...Value) Value {
			fn := args[0]
			i := 0
			return &JSIterator{Name: "Map", NextFn: func() (Value, bool) {
				v, done := it.Next()
				if done {
					return v, true
				}
				return CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i))), false
			}}
		}), true
	case "filter":
		return NewBuiltin("filter", func(args ...Value) Value {
			fn := args[0]
			i := 0
			return &JSIterator{Name: "Filter", NextFn: func() (Value, bool) {
				for {
					v, done := it.Next()
					if done {
						return v, true
					}
					keep := CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
					i++
					if keep != nil && keep != UndefinedSingleton && keep != NullSingleton &&
						keep != FalseSingleton && keep.IsTruthy() {
						return v, false
					}
				}
			}}
		}), true
	case "take":
		return NewBuiltin("take", func(args ...Value) Value {
			limit := int(valueToF64(args[0]))
			if limit < 0 {
				return NewRangeError("Iterator.prototype.take: limit must be >= 0, got %d", limit)
			}
			taken := 0
			return &JSIterator{Name: "Take", NextFn: func() (Value, bool) {
				if taken >= limit {
					return UndefinedSingleton, true
				}
				taken++
				return it.Next()
			}}
		}), true
	case "drop":
		return NewBuiltin("drop", func(args ...Value) Value {
			limit := int(valueToF64(args[0]))
			if limit < 0 {
				return NewRangeError("Iterator.prototype.drop: limit must be >= 0, got %d", limit)
			}
			dropped := 0
			return &JSIterator{Name: "Drop", NextFn: func() (Value, bool) {
				for dropped < limit {
					dropped++
					if _, done := it.Next(); done {
						return UndefinedSingleton, true
					}
				}
				return it.Next()
			}}
		}), true
	case "flatMap":
		return NewBuiltin("flatMap", func(args ...Value) Value {
			fn := args[0]
			innerNext := func() (Value, bool) { return UndefinedSingleton, true }
			var inner Value
			return &JSIterator{Name: "FlatMap", NextFn: func() (Value, bool) {
				for {
					if inner != nil {
						if v, done := innerNext(); !done {
							return v, false
						}
						inner = nil
					}
					v, done := it.Next()
					if done {
						return v, true
					}
					r := CallFunction(fn, UndefinedSingleton, v)
					next, ok := Iterate(r)
					if !ok {
						return NewTypeError("Iterator.prototype.flatMap: callback must return an iterable"), true
					}
					inner, innerNext = r, next
				}
			}}
		}), true
	case "reduce":
		return NewBuiltin("reduce", func(args ...Value) Value {
			fn := args[0]
			var acc Value
			if len(args) > 1 {
				acc = args[1]
			} else {
				var done bool
				if acc, done = it.Next(); done {
					return NewTypeError("Iterator.prototype.reduce: no initial value")
				}
			}
			i := 0
			for {
				v, done := it.Next()
				if done {
					return acc
				}
				acc = CallFunction(fn, UndefinedSingleton, acc, v, NewInt(int64(i)))
				i++
			}
		}), true
	case "toArray":
		return NewBuiltin("toArray", func(args ...Value) Value {
			var out []Value
			for {
				v, done := it.Next()
				if done {
					return NewArray(out)
				}
				out = append(out, v)
			}
		}), true
	case "forEach":
		return NewBuiltin("forEach", func(args ...Value) Value {
			fn := args[0]
			i := 0
			for {
				v, done := it.Next()
				if done {
					return UndefinedSingleton
				}
				CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
				i++
			}
		}), true
	case "some":
		return NewBuiltin("some", func(args ...Value) Value {
			fn := args[0]
			i := 0
			for {
				v, done := it.Next()
				if done {
					return FalseSingleton
				}
				if CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i))).IsTruthy() {
					return TrueSingleton
				}
				i++
			}
		}), true
	case "every":
		return NewBuiltin("every", func(args ...Value) Value {
			fn := args[0]
			i := 0
			for {
				v, done := it.Next()
				if done {
					return TrueSingleton
				}
				if !CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i))).IsTruthy() {
					return FalseSingleton
				}
				i++
			}
		}), true
	case "find":
		return NewBuiltin("find", func(args ...Value) Value {
			fn := args[0]
			i := 0
			for {
				v, done := it.Next()
				if done {
					return UndefinedSingleton
				}
				if CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i))).IsTruthy() {
					return v
				}
				i++
			}
		}), true
	}
	return nil, false
}

// valueToF64 将值转换为 float64 (Iterator helper 的数值参数用)。
func valueToF64(v Value) float64 {
	switch t := v.(type) {
	case *Number:
		return t.Value
	case *Boolean:
		if t.Value {
			return 1
		}
		return 0
	case *String:
		return strToFloat(t.Value)
	}
	return 0
}

func strToFloat(s string) float64 {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return f
}

// NewNextOnlyIterator 包装一个只有 next 方法的对象为增强迭代器
// (Iterator.from 的类迭代器路径)。
func NewNextOnlyIterator(target Value, nextFn Value) *JSIterator {
	return &JSIterator{NextFn: func() (Value, bool) {
		r := CallFunction(nextFn, target)
		res, ok := r.(*Object)
		if !ok {
			return UndefinedSingleton, true
		}
		if dv, found := res.GetProperty("done"); found {
			if b, isB := dv.(*Boolean); isB && b.Value {
				return UndefinedSingleton, true
			}
		}
		if vv, found := res.GetProperty("value"); found {
			return vv, false
		}
		return UndefinedSingleton, false
	}}
}

// ApplyIteratorHelper 在 JSIterator 实例上应用 ES2025 helper 方法。
// 供 Iterator.prototype 上的同名方法委托调用 (this 必须是 *JSIterator)。
func ApplyIteratorHelper(recv Value, name string, args []Value) (Value, bool) {
	it, ok := recv.(*JSIterator)
	if !ok {
		return nil, false
	}
	return it.esIteratorHelper(name)
}
