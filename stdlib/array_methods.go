package stdlib

import (
	"sort"
	"strings"

	"js-runtime/object"
)

// setupArrayProto 创建 Array.prototype 对象。
// 所有数组实例的原型，包含 push, pop, map 等方法。
// 方法签名: NewBuiltinMethod(name, func(this Value, args ...Value) Value)
func setupArrayProto() *object.Object {
	p := object.NewObject()

	// push(...items): 向数组末尾添加元素，返回新长度
	p.SetProperty("push", object.NewBuiltinMethod("push", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			arr.Elements = append(arr.Elements, args...)
			return object.NewNumber(float64(len(arr.Elements)))
		}
		return object.NewNumber(0)
	}))

	// pop(): 移除并返回数组最后一个元素
	p.SetProperty("pop", object.NewBuiltinMethod("pop", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			n := len(arr.Elements)
			if n == 0 {
				return object.UndefinedSingleton
			}
			last := arr.Elements[n-1]
			arr.Elements = arr.Elements[:n-1]
			return last
		}
		return object.UndefinedSingleton
	}))

	// shift(): 移除并返回数组第一个元素
	p.SetProperty("shift", object.NewBuiltinMethod("shift", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			n := len(arr.Elements)
			if n == 0 {
				return object.UndefinedSingleton
			}
			first := arr.Elements[0]
			arr.Elements = arr.Elements[1:]
			return first
		}
		return object.UndefinedSingleton
	}))

	// unshift(...items): 向数组开头添加元素，返回新长度
	p.SetProperty("unshift", object.NewBuiltinMethod("unshift", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			arr.Elements = append(args, arr.Elements...)
			return object.NewNumber(float64(len(arr.Elements)))
		}
		return object.NewNumber(0)
	}))

	// join(separator): 用分隔符连接所有元素
	p.SetProperty("join", object.NewBuiltinMethod("join", func(this object.Value, args ...object.Value) object.Value {
		sep := ","
		if len(args) > 0 {
			if s, ok := args[0].(*object.String); ok {
				sep = s.Value
			}
		}
		if arr, ok := this.(*object.Array); ok {
			var parts []string
			for _, e := range arr.Elements {
				if e == nil || e == object.UndefinedSingleton || e == object.NullSingleton {
					parts = append(parts, "")
				} else {
					parts = append(parts, e.Inspect())
				}
			}
			return object.NewString(strings.Join(parts, sep))
		}
		return object.NewString("")
	}))

	// slice(start, end): 返回数组的一部分
	p.SetProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			n := len(arr.Elements)
			start := 0
			end := n
			if len(args) > 0 {
				start = int(toFloat(args[0]))
			}
			if len(args) > 1 {
				end = int(toFloat(args[1]))
			}
			if start < 0 {
				start = n + start
				if start < 0 {
					start = 0
				}
			}
			if end < 0 {
				end = n + end
				if end < 0 {
					end = 0
				}
			}
			if start > n {
				start = n
			}
			if end > n {
				end = n
			}
			if start >= end {
				return object.NewArray([]object.Value{})
			}
			result := make([]object.Value, end-start)
			copy(result, arr.Elements[start:end])
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	// splice(start, deleteCount, ...items): 修改数组内容
	p.SetProperty("splice", object.NewBuiltinMethod("splice", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			n := len(arr.Elements)
			start := 0
			deleteCount := n
			if len(args) > 0 {
				start = int(toFloat(args[0]))
			}
			if start < 0 {
				start = n + start
				if start < 0 {
					start = 0
				}
			}
			if start > n {
				start = n
			}
			if len(args) > 1 {
				deleteCount = int(toFloat(args[1]))
				if deleteCount < 0 {
					deleteCount = 0
				}
				if deleteCount > n-start {
					deleteCount = n - start
				}
			}
			deleted := make([]object.Value, deleteCount)
			copy(deleted, arr.Elements[start:start+deleteCount])
			items := []object.Value{}
			if len(args) > 2 {
				items = args[2:]
			}
			newElements := make([]object.Value, 0, n-deleteCount+len(items))
			newElements = append(newElements, arr.Elements[:start]...)
			newElements = append(newElements, items...)
			newElements = append(newElements, arr.Elements[start+deleteCount:]...)
			arr.Elements = newElements
			return object.NewArray(deleted)
		}
		return object.NewArray([]object.Value{})
	}))

	// concat(...arrays): 连接多个数组
	p.SetProperty("concat", object.NewBuiltinMethod("concat", func(this object.Value, args ...object.Value) object.Value {
		var result []object.Value
		if arr, ok := this.(*object.Array); ok {
			result = append(result, arr.Elements...)
		}
		for _, arg := range args {
			if argArr, ok := arg.(*object.Array); ok {
				result = append(result, argArr.Elements...)
			} else {
				result = append(result, arg)
			}
		}
		return object.NewArray(result)
	}))

	// indexOf(item, fromIndex): 查找元素的索引
	p.SetProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			if len(args) < 1 {
				return object.NewNumber(-1)
			}
			target := args[0]
			fromIdx := 0
			if len(args) > 1 {
				fromIdx = int(toFloat(args[1]))
				if fromIdx < 0 {
					fromIdx = len(arr.Elements) + fromIdx
					if fromIdx < 0 {
						fromIdx = 0
					}
				}
			}
			for i := fromIdx; i < len(arr.Elements); i++ {
				if strictEqualValues(arr.Elements[i], target) {
					return object.NewNumber(float64(i))
				}
			}
		}
		return object.NewNumber(-1)
	}))

	// includes(item, fromIndex): 检查数组是否包含某元素
	p.SetProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			if len(args) < 1 {
				return object.NewBoolean(false)
			}
			target := args[0]
			fromIdx := 0
			if len(args) > 1 {
				fromIdx = int(toFloat(args[1]))
				if fromIdx < 0 {
					fromIdx = len(arr.Elements) + fromIdx
					if fromIdx < 0 {
						fromIdx = 0
					}
				}
			}
			for i := fromIdx; i < len(arr.Elements); i++ {
				if strictEqualValues(arr.Elements[i], target) {
					return object.NewBoolean(true)
				}
			}
		}
		return object.NewBoolean(false)
	}))

	// find(callback): 返回第一个满足条件的元素
	p.SetProperty("find", object.NewBuiltinMethod("find", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if result != nil && result.IsTruthy() {
				return elem
			}
		}
		return object.UndefinedSingleton
	}))

	// findIndex(callback): 返回第一个满足条件的元素的索引
	p.SetProperty("findIndex", object.NewBuiltinMethod("findIndex", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewNumber(-1)
		}
		callback := args[0]
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if result != nil && result.IsTruthy() {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// forEach(callback): 遍历数组，对每个元素调用回调
	p.SetProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		for i, elem := range arr.Elements {
			object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
		}
		return object.UndefinedSingleton
	}))

	// map(callback): 对每个元素调用回调，返回结果数组
	p.SetProperty("map", object.NewBuiltinMethod("map", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewArray([]object.Value{})
		}
		callback := args[0]
		result := make([]object.Value, len(arr.Elements))
		for i, elem := range arr.Elements {
			mapped := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if mapped == nil {
				mapped = object.UndefinedSingleton
			}
			result[i] = mapped
		}
		return object.NewArray(result)
	}))

	// filter(callback): 过滤数组，返回满足条件的元素
	p.SetProperty("filter", object.NewBuiltinMethod("filter", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewArray([]object.Value{})
		}
		callback := args[0]
		var result []object.Value
		for i, elem := range arr.Elements {
			keep := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if keep != nil && keep.IsTruthy() {
				result = append(result, elem)
			}
		}
		return object.NewArray(result)
	}))

	// reduce(callback, initialValue): 归约数组为单个值
	p.SetProperty("reduce", object.NewBuiltinMethod("reduce", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		var acc object.Value
		startIdx := 0
		if len(args) > 1 {
			acc = args[1]
		} else {
			if len(arr.Elements) == 0 {
				return object.UndefinedSingleton
			}
			acc = arr.Elements[0]
			startIdx = 1
		}
		for i := startIdx; i < len(arr.Elements); i++ {
			acc = object.CallFunction(callback, nil,
				acc,
				arr.Elements[i],
				object.NewNumber(float64(i)),
				arr,
			)
		}
		return acc
	}))

	// reduceRight(callback, initialValue): 从右向左归约
	p.SetProperty("reduceRight", object.NewBuiltinMethod("reduceRight", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.UndefinedSingleton
		}
		callback := args[0]
		n := len(arr.Elements)
		var acc object.Value
		endIdx := n - 1
		if len(args) > 1 {
			acc = args[1]
		} else {
			if n == 0 {
				return object.UndefinedSingleton
			}
			acc = arr.Elements[n-1]
			endIdx = n - 2
		}
		for i := endIdx; i >= 0; i-- {
			acc = object.CallFunction(callback, nil,
				acc,
				arr.Elements[i],
				object.NewNumber(float64(i)),
				arr,
			)
		}
		return acc
	}))

	// some(callback): 如果有元素满足条件则返回 true
	p.SetProperty("some", object.NewBuiltinMethod("some", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewBoolean(false)
		}
		callback := args[0]
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if result != nil && result.IsTruthy() {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))

	// every(callback): 如果所有元素都满足条件则返回 true
	p.SetProperty("every", object.NewBuiltinMethod("every", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewBoolean(true)
		}
		callback := args[0]
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if result == nil || !result.IsTruthy() {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// flatMap(callback): 先 map 再 flat(1)
	p.SetProperty("flatMap", object.NewBuiltinMethod("flatMap", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewArray([]object.Value{})
		}
		callback := args[0]
		var result []object.Value
		for i, elem := range arr.Elements {
			mapped := object.CallFunction(callback, nil,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if mapped == nil {
				mapped = object.UndefinedSingleton
			}
			if mappedArr, ok := mapped.(*object.Array); ok {
				result = append(result, mappedArr.Elements...)
			} else {
				result = append(result, mapped)
			}
		}
		return object.NewArray(result)
	}))

	// reverse(): 反转数组
	p.SetProperty("reverse", object.NewBuiltinMethod("reverse", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			n := len(arr.Elements)
			for i := 0; i < n/2; i++ {
				arr.Elements[i], arr.Elements[n-1-i] = arr.Elements[n-1-i], arr.Elements[i]
			}
			return arr
		}
		return this
	}))

	// sort(compareFn): 排序数组
	p.SetProperty("sort", object.NewBuiltinMethod("sort", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			// 如果传入了比较函数，使用它进行排序
			if len(args) > 0 && object.IsCallable(args[0]) {
				compareFn := args[0]
				sort.SliceStable(arr.Elements, func(i, j int) bool {
					result := object.CallFunction(compareFn, nil, arr.Elements[i], arr.Elements[j])
					return toFloat(result) < 0
				})
				return arr
			}
			// 默认排序: 数字按数值，其他按字符串
			sort.SliceStable(arr.Elements, func(i, j int) bool {
				a := arr.Elements[i]
				b := arr.Elements[j]
				if _, aIsNum := a.(*object.Number); aIsNum {
					if _, bIsNum := b.(*object.Number); bIsNum {
						return toFloat(a) < toFloat(b)
					}
				}
				return toStr(a) < toStr(b)
			})
			return arr
		}
		return this
	}))

	// toString(): 数组转字符串
	p.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		if arr, ok := this.(*object.Array); ok {
			var parts []string
			for _, e := range arr.Elements {
				if e == nil || e == object.UndefinedSingleton || e == object.NullSingleton {
					parts = append(parts, "")
				} else {
					parts = append(parts, toStr(e))
				}
			}
			return object.NewString(strings.Join(parts, ","))
		}
		return object.NewString("")
	}))

	// at(index): 返回指定索引处的元素，支持负索引
	p.SetProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return object.UndefinedSingleton
		}
		n := len(arr.Elements)
		idx := 0
		if len(args) > 0 {
			idx = int(toFloat(args[0]))
		}
		if idx < 0 {
			idx = n + idx
		}
		if idx < 0 || idx >= n {
			return object.UndefinedSingleton
		}
		return arr.Elements[idx]
	}))

	// fill(value, start, end): 用固定值填充数组的一部分
	p.SetProperty("fill", object.NewBuiltinMethod("fill", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return this
		}
		n := len(arr.Elements)
		if n == 0 {
			return arr
		}
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			val = args[0]
		}
		start := 0
		end := n
		if len(args) > 1 {
			start = int(toFloat(args[1]))
			if start < 0 {
				start = n + start
				if start < 0 {
					start = 0
				}
			}
			if start > n {
				start = n
			}
		}
		if len(args) > 2 {
			end = int(toFloat(args[2]))
			if end < 0 {
				end = n + end
				if end < 0 {
					end = 0
				}
			}
			if end > n {
				end = n
			}
		}
		for i := start; i < end; i++ {
			arr.Elements[i] = val
		}
		return arr
	}))

	// copyWithin(target, start, end): 数组内部复制
	p.SetProperty("copyWithin", object.NewBuiltinMethod("copyWithin", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return this
		}
		n := len(arr.Elements)
		if n == 0 || len(args) < 1 {
			return arr
		}
		target := int(toFloat(args[0]))
		start := 0
		end := n
		if len(args) > 1 {
			start = int(toFloat(args[1]))
		}
		if len(args) > 2 {
			end = int(toFloat(args[2]))
		}
		// 规范化负索引
		if target < 0 {
			target = n + target
		}
		if start < 0 {
			start = n + start
		}
		if end < 0 {
			end = n + end
		}
		// 钳位
		if target < 0 {
			target = 0
		}
		if start < 0 {
			start = 0
		}
		if end < 0 {
			end = 0
		}
		if target >= n || start >= n || start >= end {
			return arr
		}
		if end > n {
			end = n
		}
		count := end - start
		if target+count > n {
			count = n - target
		}
		for i := 0; i < count; i++ {
			arr.Elements[target+i] = arr.Elements[start+i]
		}
		return arr
	}))

	// lastIndexOf(item, fromIndex): 从后向前查找
	p.SetProperty("lastIndexOf", object.NewBuiltinMethod("lastIndexOf", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok || len(args) < 1 {
			return object.NewNumber(-1)
		}
		target := args[0]
		n := len(arr.Elements)
		fromIdx := n - 1
		if len(args) > 1 {
			fromIdx = int(toFloat(args[1]))
			if fromIdx < 0 {
				fromIdx = n + fromIdx
			}
		}
		if fromIdx >= n {
			fromIdx = n - 1
		}
		if fromIdx < 0 {
			return object.NewNumber(-1)
		}
		for i := fromIdx; i >= 0; i-- {
			if strictEqualValues(arr.Elements[i], target) {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// flat(depth): 扁平化数组
	p.SetProperty("flat", object.NewBuiltinMethod("flat", func(this object.Value, args ...object.Value) object.Value {
		depth := 1
		if len(args) > 0 {
			depth = int(toFloat(args[0]))
		}
		if arr, ok := this.(*object.Array); ok {
			result := flatArray(arr.Elements, depth)
			return object.NewArray(result)
		}
		return object.NewArray([]object.Value{})
	}))

	return p
}

// flatArray 递归扁平化数组。
func flatArray(elements []object.Value, depth int) []object.Value {
	var result []object.Value
	for _, e := range elements {
		if arr, ok := e.(*object.Array); ok && depth > 0 {
			result = append(result, flatArray(arr.Elements, depth-1)...)
		} else {
			result = append(result, e)
		}
	}
	return result
}

// strictEqualValues 比较两个值是否严格相等。
func strictEqualValues(a, b object.Value) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Type() != b.Type() {
		return false
	}
	switch av := a.(type) {
	case *object.Number:
		if bv, ok := b.(*object.Number); ok {
			return av.Value == bv.Value
		}
	case *object.String:
		if bv, ok := b.(*object.String); ok {
			return av.Value == bv.Value
		}
	case *object.Boolean:
		if bv, ok := b.(*object.Boolean); ok {
			return av.Value == bv.Value
		}
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	}
	return false
}

// setupArrayGlobal 创建 Array 构造函数对象。
func setupArrayGlobal() *object.Object {
	arr := object.NewObject()
	arr.SetProperty("name", object.NewString("Array"))
	arr.SetProperty("isArray", object.NewBuiltin("isArray", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		_, ok := args[0].(*object.Array)
		return object.NewBoolean(ok)
	}))
	arr.SetProperty("from", object.NewBuiltin("from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		if a, ok := args[0].(*object.Array); ok {
			result := make([]object.Value, len(a.Elements))
			copy(result, a.Elements)
			return object.NewArray(result)
		}
		if s, ok := args[0].(*object.String); ok {
			elements := make([]object.Value, len(s.Value))
			for i, ch := range s.Value {
				elements[i] = object.NewString(string(ch))
			}
			return object.NewArray(elements)
		}
		return object.NewArray([]object.Value{})
	}))
	arr.SetProperty("of", object.NewBuiltin("of", func(args ...object.Value) object.Value {
		return object.NewArray(args)
	}))
	return arr
}
