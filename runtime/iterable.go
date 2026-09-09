package runtime

import "js-runtime/object"

// Iterator 表示 JavaScript 的迭代器协议。
// 任何具有 [Symbol.iterator] 方法的对象都可以产生迭代器。
// for...of 循环使用迭代器协议遍历可迭代对象。
type Iterator struct {
	// target 是被迭代的对象
	target object.Value
	// index 是当前迭代位置 (用于数组和字符串)
	index int
	// keys 是对象属性的键列表 (用于 Object.keys 迭代)
	keys []string
	// kind 表示迭代类型: "array", "string", "object", "callback"
	kind string
	// nextFn 用于 "callback" 型迭代器，直接驱动 JS 层迭代器
	nextFn func() (object.Value, bool)
}

// NewArrayIterator 创建数组迭代器
func NewArrayIterator(arr *object.Array) *Iterator {
	return &Iterator{
		target: arr,
		index:  0,
		kind:   "array",
	}
}

// NewStringIterator 创建字符串迭代器
func NewStringIterator(s *object.String) *Iterator {
	return &Iterator{
		target: s,
		index:  0,
		kind:   "string",
	}
}

// NewObjectKeysIterator 创建对象键迭代器 (用于 for...in)。
func NewObjectKeysIterator(o *object.Object) *Iterator {
	return &Iterator{
		target: o,
		index:  0,
		keys:   o.Keys(),
		kind:   "object",
	}
}

// NewObjectKeysIteratorWithKeys 使用给定的键列表创建对象键迭代器。
// 用于数组的 for...in (索引键)。
func NewObjectKeysIteratorWithKeys(target object.Value, keys []string) *Iterator {
	return &Iterator{
		target: target,
		index:  0,
		keys:   keys,
		kind:   "object",
	}
}

// Next 返回迭代器的下一个值。
// 返回: (value, done)
// 当 done 为 true 时，迭代结束。
func (it *Iterator) Next() (object.Value, bool) {
	switch it.kind {
	case "array":
		arr, ok := it.target.(*object.Array)
		if !ok {
			return object.UndefinedSingleton, true
		}
		if it.index >= len(arr.Elements) {
			return object.UndefinedSingleton, true
		}
		val := arr.Elements[it.index]
		if val == nil {
			val = object.UndefinedSingleton
		}
		it.index++
		return val, false

	case "string":
		s, ok := it.target.(*object.String)
		if !ok {
			return object.UndefinedSingleton, true
		}
		if it.index >= len(s.Value) {
			return object.UndefinedSingleton, true
		}
		// 返回单个字符的字符串
		ch := string(s.Value[it.index])
		it.index++
		return object.NewString(ch), false

	case "object":
		// for...in: 遍历对象自有键
		if it.index >= len(it.keys) {
			return object.UndefinedSingleton, true
		}
		k := it.keys[it.index]
		it.index++
		return object.NewString(k), false

	case "callback":
		if it.nextFn == nil {
			return object.UndefinedSingleton, true
		}
		return it.nextFn()
	}

	return object.UndefinedSingleton, true
}

// Type 返回迭代器类型标识
func (it *Iterator) Type() object.ObjectType { return object.ITERATOR_OBJ }

// Inspect 返回迭代器的字符串表示
func (it *Iterator) Inspect() string {
	return "[Iterator]"
}

// IsTruthy 返回迭代器的布尔值
func (it *Iterator) IsTruthy() bool { return true }

// GetProperty 获取迭代器属性
func (it *Iterator) GetProperty(name string) (object.Value, bool) {
	return nil, false
}

// SetProperty 设置迭代器属性 (不允许)
func (it *Iterator) SetProperty(name string, val object.Value) {}

// GetIterable 判断值是否可迭代，并返回对应的迭代器。
// 在 JavaScript 中，Array/String/Map/Set 及任何实现了 [Symbol.iterator]
// 的对象都是可迭代的。
func GetIterable(val object.Value) (*Iterator, bool) {
	switch v := val.(type) {
	case *object.Array:
		return NewArrayIterator(v), true
	case *object.String:
		return NewStringIterator(v), true
	case *object.TypedArray:
		it := object.NewArrayIterator(v.ToArray())
		return &Iterator{kind: "callback", nextFn: func() (object.Value, bool) {
			return it.Next()
		}}, true
	case *object.JSIterator:
		// JS 层迭代器 (arr.keys() / Iterator helpers 的产物):
		// 用回调型适配器包装，由 NextFn 驱动。
		it := v
		return &Iterator{kind: "callback", nextFn: func() (object.Value, bool) {
			return it.Next()
		}}, true
	case *object.Map:
		// for...of over Map 按规范迭代 [key, value] 对
		it := object.NewMapEntryIterator(v)
		return &Iterator{kind: "callback", nextFn: func() (object.Value, bool) {
			return it.Next()
		}}, true
	case *object.Set:
		it := object.NewSetIterator(v)
		return &Iterator{kind: "callback", nextFn: func() (object.Value, bool) {
			return it.Next()
		}}, true
	case *object.Object:
		// 实现了 [Symbol.iterator] 的普通对象: 调用该方法并适配其结果
		if fn, found := v.GetProperty(object.NewSymbol("Symbol.iterator").Inspect()); found && object.IsCallable(fn) {
			res := object.CallFunction(fn, v)
			switch r := res.(type) {
			case *object.JSIterator:
				it := r
				return &Iterator{kind: "callback", nextFn: func() (object.Value, bool) {
					return it.Next()
				}}, true
			}
		}
	}
	return nil, false
}
