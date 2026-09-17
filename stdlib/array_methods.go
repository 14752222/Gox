package stdlib

import (
	"math"
	"sort"
	"strings"

	"github.com/14752222/Gox/object"
)

// setupArrayProto 创建 Array.prototype 对象。
// 所有数组实例的原型，包含 push, pop, map 等方法。
// 方法签名: NewBuiltinMethod(name, func(this Value, args ...Value) Value)
func setupArrayProto() *object.Object {
	p := object.NewObject()

	// push(...items): 向数组末尾添加元素，返回新长度
	p.SetProperty("push", object.NewBuiltinMethod("push", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "push", this)
		}
		arr.Elements = append(arr.Elements, args...)
		return object.NewNumber(float64(len(arr.Elements)))
	}))

	// pop(): 移除并返回数组最后一个元素
	p.SetProperty("pop", object.NewBuiltinMethod("pop", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "pop", this)
		}
		n := len(arr.Elements)
		if n == 0 {
			return object.UndefinedSingleton
		}
		last := arr.Elements[n-1]
		arr.Elements = arr.Elements[:n-1]
		return last
	}))

	// shift(): 移除并返回数组第一个元素
	p.SetProperty("shift", object.NewBuiltinMethod("shift", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "shift", this)
		}
		n := len(arr.Elements)
		if n == 0 {
			return object.UndefinedSingleton
		}
		first := arr.Elements[0]
		arr.Elements = arr.Elements[1:]
		return first
	}))

	// unshift(...items): 向数组开头添加元素，返回新长度
	p.SetProperty("unshift", object.NewBuiltinMethod("unshift", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "unshift", this)
		}
		// 复制到新切片: args 是 VM 的参数切片，直接 append 可能复用其
		// 底层数组，与 Elements 混用会造成数据错乱。
		items := make([]object.Value, 0, len(args)+len(arr.Elements))
		items = append(items, args...)
		items = append(items, arr.Elements...)
		arr.Elements = items
		return object.NewNumber(float64(len(arr.Elements)))
	}))

	// join(separator): 用分隔符连接所有元素
	// undefined / null 元素输出为空串；separator 省略或为 null/undefined 时用 ","
	p.SetProperty("join", object.NewBuiltinMethod("join", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "join", this)
		}
		sep := ","
		if len(args) > 0 && !isUndefinedValue(args[0]) && !isNullValue(args[0]) {
			sep = toStr(args[0])
		}
		var parts []string
		for _, e := range arr.Elements {
			if e == nil || isUndefinedValue(e) || isNullValue(e) {
				parts = append(parts, "")
			} else {
				parts = append(parts, toStr(e))
			}
		}
		return object.NewString(strings.Join(parts, sep))
	}))

	// slice(start, end): 返回数组的一部分 (浅拷贝，不修改原数组)
	p.SetProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "slice", this)
		}
		n := len(arr.Elements)
		start := 0
		end := n
		if len(args) > 0 {
			start = clampIndex(toInt(args[0]), n)
		}
		if len(args) > 1 {
			end = clampIndex(toInt(args[1]), n)
		}
		if start >= end {
			return object.NewArray([]object.Value{})
		}
		result := make([]object.Value, end-start)
		copy(result, arr.Elements[start:end])
		return object.NewArray(result)
	}))

	// splice(start, deleteCount, ...items): 修改数组内容
	p.SetProperty("splice", object.NewBuiltinMethod("splice", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "splice", this)
		}
		n := len(arr.Elements)
		start := 0
		if len(args) > 0 {
			start = clampIndex(toInt(args[0]), n)
		}
		// 未指定 deleteCount 时，删除从 start 到末尾的所有元素。
		// 这里必须基于 start 计算，否则 arr.Elements[start:start+deleteCount] 会越界。
		deleteCount := n - start
		if len(args) > 1 {
			deleteCount = int(toInt(args[1]))
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
	// 采用 SameValueZero 比较，因此 [NaN].indexOf(NaN) === 0
	p.SetProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "indexOf", this)
		}
		if len(args) < 1 {
			return object.NewNumber(-1)
		}
		target := args[0]
		n := len(arr.Elements)
		fromIdx := 0
		if len(args) > 1 {
			fromIdx = clampIndex(toInt(args[1]), n)
		}
		for i := fromIdx; i < n; i++ {
			if sameValueZero(arr.Elements[i], target) {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// includes(item, fromIndex): 检查数组是否包含某元素
	// 同样采用 SameValueZero，因此 [NaN].includes(NaN) === true
	p.SetProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "includes", this)
		}
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		target := args[0]
		n := len(arr.Elements)
		fromIdx := 0
		if len(args) > 1 {
			fromIdx = clampIndex(toInt(args[1]), n)
		}
		for i := fromIdx; i < n; i++ {
			if sameValueZero(arr.Elements[i], target) {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))

	// find(callback, thisArg): 返回第一个满足条件的元素
	p.SetProperty("find", object.NewBuiltinMethod("find", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("find", this, args)
		if errVal != nil {
			return errVal
		}
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return elem
			}
		}
		return object.UndefinedSingleton
	}))

	// findIndex(callback, thisArg): 返回第一个满足条件的元素的索引
	p.SetProperty("findIndex", object.NewBuiltinMethod("findIndex", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("findIndex", this, args)
		if errVal != nil {
			return errVal
		}
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// forEach(callback, thisArg): 遍历数组，对每个元素调用回调
	p.SetProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("forEach", this, args)
		if errVal != nil {
			return errVal
		}
		for i, elem := range arr.Elements {
			object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return object.UndefinedSingleton
	}))

	// map(callback, thisArg): 对每个元素调用回调，返回结果数组
	p.SetProperty("map", object.NewBuiltinMethod("map", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("map", this, args)
		if errVal != nil {
			return errVal
		}
		result := make([]object.Value, len(arr.Elements))
		for i, elem := range arr.Elements {
			mapped := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if mapped == nil {
				mapped = object.UndefinedSingleton
			}
			result[i] = mapped
		}
		return object.NewArray(result)
	}))

	// filter(callback, thisArg): 过滤数组，返回满足条件的元素
	p.SetProperty("filter", object.NewBuiltinMethod("filter", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("filter", this, args)
		if errVal != nil {
			return errVal
		}
		var result []object.Value
		for i, elem := range arr.Elements {
			keep := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if keep != nil && keep.IsTruthy() {
				result = append(result, elem)
			}
		}
		return object.NewArray(result)
	}))

	// reduce(callback, initialValue): 归约数组为单个值
	// 规范: reduce 不接受 thisArg，回调的 this 是 undefined；
	// 空数组且未提供 initialValue 时抛 TypeError。
	p.SetProperty("reduce", object.NewBuiltinMethod("reduce", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, _, errVal := arrayCallbackArgs("reduce", this, args)
		if errVal != nil {
			return errVal
		}
		var acc object.Value
		startIdx := 0
		if len(args) > 1 {
			acc = args[1]
		} else {
			if len(arr.Elements) == 0 {
				return object.NewTypeError("Reduce of empty array with no initial value")
			}
			acc = arr.Elements[0]
			startIdx = 1
		}
		for i := startIdx; i < len(arr.Elements); i++ {
			acc = object.CallFunction(callback, object.UndefinedSingleton,
				acc,
				arr.Elements[i],
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return acc
	}))

	// reduceRight(callback, initialValue): 从右向左归约
	p.SetProperty("reduceRight", object.NewBuiltinMethod("reduceRight", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, _, errVal := arrayCallbackArgs("reduceRight", this, args)
		if errVal != nil {
			return errVal
		}
		n := len(arr.Elements)
		var acc object.Value
		endIdx := n - 1
		if len(args) > 1 {
			acc = args[1]
		} else {
			if n == 0 {
				return object.NewTypeError("Reduce of empty array with no initial value")
			}
			acc = arr.Elements[n-1]
			endIdx = n - 2
		}
		for i := endIdx; i >= 0; i-- {
			acc = object.CallFunction(callback, object.UndefinedSingleton,
				acc,
				arr.Elements[i],
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return acc
	}))

	// some(callback, thisArg): 如果有元素满足条件则返回 true
	p.SetProperty("some", object.NewBuiltinMethod("some", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("some", this, args)
		if errVal != nil {
			return errVal
		}
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))

	// every(callback, thisArg): 如果所有元素都满足条件则返回 true
	p.SetProperty("every", object.NewBuiltinMethod("every", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("every", this, args)
		if errVal != nil {
			return errVal
		}
		for i, elem := range arr.Elements {
			result := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result == nil || !result.IsTruthy() {
				return object.NewBoolean(false)
			}
		}
		return object.NewBoolean(true)
	}))

	// flatMap(callback, thisArg): 先 map 再 flat(1)
	p.SetProperty("flatMap", object.NewBuiltinMethod("flatMap", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("flatMap", this, args)
		if errVal != nil {
			return errVal
		}
		var result []object.Value
		for i, elem := range arr.Elements {
			mapped := object.CallFunction(callback, thisArg,
				elem,
				object.NewNumber(float64(i)),
				arr,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
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

	// ===== 迭代器方法 (ES6): 返回真正的迭代器对象 =====

	// keys(): 迭代索引
	p.SetProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "keys", this)
		}
		return object.NewArrayIndexIterator(arr)
	}))

	// values(): 迭代元素
	p.SetProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "values", this)
		}
		return object.NewArrayIterator(arr)
	}))

	// entries(): 迭代 [index, element] 对
	p.SetProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "entries", this)
		}
		return object.NewArrayEntryIterator(arr)
	}))

	// ===== ES2023 findLast 系列 =====

	// findLast(callback, thisArg): 从后向前找第一个满足条件的元素
	p.SetProperty("findLast", object.NewBuiltinMethod("findLast", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("findLast", this, args)
		if errVal != nil {
			return errVal
		}
		for i := len(arr.Elements) - 1; i >= 0; i-- {
			result := object.CallFunction(callback, thisArg,
				arr.Elements[i], object.NewInt(int64(i)), arr)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return arr.Elements[i]
			}
		}
		return object.UndefinedSingleton
	}))

	// findLastIndex(callback, thisArg): 从后向前找第一个满足条件的索引
	p.SetProperty("findLastIndex", object.NewBuiltinMethod("findLastIndex", func(this object.Value, args ...object.Value) object.Value {
		arr, callback, thisArg, errVal := arrayCallbackArgs("findLastIndex", this, args)
		if errVal != nil {
			return errVal
		}
		for i := len(arr.Elements) - 1; i >= 0; i-- {
			result := object.CallFunction(callback, thisArg,
				arr.Elements[i], object.NewInt(int64(i)), arr)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return object.NewInt(int64(i))
			}
		}
		return object.NewInt(-1)
	}))

	// ===== ES2023 变更即拷贝 (change-by-copy) 方法 =====

	// toSorted(compareFn): sorted() 的新副本
	p.SetProperty("toSorted", object.NewBuiltinMethod("toSorted", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "toSorted", this)
		}
		cp := make([]object.Value, len(arr.Elements))
		copy(cp, arr.Elements)
		return sortArrayValues(object.NewArray(cp), args)
	}))

	// toReversed(): reversed() 的新副本
	p.SetProperty("toReversed", object.NewBuiltinMethod("toReversed", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "toReversed", this)
		}
		n := len(arr.Elements)
		cp := make([]object.Value, n)
		for i := 0; i < n; i++ {
			cp[i] = arr.Elements[n-1-i]
		}
		return object.NewArray(cp)
	}))

	// toSpliced(start, deleteCount, ...items): spliced() 的新副本
	p.SetProperty("toSpliced", object.NewBuiltinMethod("toSpliced", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "toSpliced", this)
		}
		start, count := spliceArgs(len(arr.Elements), args)
		cp := make([]object.Value, 0, len(arr.Elements)+len(args))
		cp = append(cp, arr.Elements[:start]...)
		cp = append(cp, args[2:]...)
		cp = append(cp, arr.Elements[start+count:]...)
		return object.NewArray(cp)
	}))

	// with(index, value): 替换单个元素的新副本
	p.SetProperty("with", object.NewBuiltinMethod("with", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "with", this)
		}
		if len(args) == 0 {
			return object.NewRangeError("Array.prototype.with: index is required")
		}
		idx := int(toFloat(args[0]))
		if idx < 0 {
			idx += len(arr.Elements)
		}
		if idx < 0 || idx >= len(arr.Elements) {
			return object.NewRangeError("Array.prototype.with: invalid index %d", idx)
		}
		cp := make([]object.Value, len(arr.Elements))
		copy(cp, arr.Elements)
		var val object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			val = args[1]
		}
		cp[idx] = val
		return object.NewArray(cp)
	}))

	// reverse(): 反转数组 (原地修改并返回自身)
	p.SetProperty("reverse", object.NewBuiltinMethod("reverse", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "reverse", this)
		}
		n := len(arr.Elements)
		for i := 0; i < n/2; i++ {
			arr.Elements[i], arr.Elements[n-1-i] = arr.Elements[n-1-i], arr.Elements[i]
		}
		return arr
	}))

	// sort(compareFn): 排序数组 (原地修改并返回自身)
	p.SetProperty("sort", object.NewBuiltinMethod("sort", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "sort", this)
		}
		if len(args) > 0 && !isUndefinedValue(args[0]) {
			compareFn := args[0]
			if !object.IsCallable(compareFn) {
				return object.NewTypeError("%s is not a function", toStr(compareFn))
			}
			// 比较器抛异常时记录并尽快结束排序 (数组可能处于部分有序状态，
			// 与 V8 的实现定义行为一致)；排序结束后把异常抛给调用方。
			var sortThrown object.Value
			sort.SliceStable(arr.Elements, func(i, j int) bool {
				if sortThrown != nil {
					return false
				}
				result := object.CallFunction(compareFn, object.UndefinedSingleton,
					arr.Elements[i], arr.Elements[j])
				if thrown := callbackThrown(); thrown != nil {
					sortThrown = thrown
					return false
				}
				return toFloat(result) < 0
			})
			if sortThrown != nil {
				return sortThrown
			}
			return arr
		}
		// 默认排序遵循规范的 SortCompare: 先把元素转成字符串再比较。
		// 旧实现遇到两个 Number 时按数值比较，导致 [10, 9].sort() 得到
		// [9, 10]；按规范应为 [10, 9] (因为 "10" < "9")。
		sort.SliceStable(arr.Elements, func(i, j int) bool {
			return defaultSortLess(arr.Elements[i], arr.Elements[j])
		})
		return arr
	}))

	// toString(): 数组转字符串 (等价于无参 join)
	p.SetProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "toString", this)
		}
		var parts []string
		for _, e := range arr.Elements {
			if e == nil || isUndefinedValue(e) || isNullValue(e) {
				parts = append(parts, "")
			} else {
				parts = append(parts, toStr(e))
			}
		}
		return object.NewString(strings.Join(parts, ","))
	}))

	// at(index): 返回指定索引处的元素，支持负索引 (-1 表示最后一个)
	p.SetProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "at", this)
		}
		n := len(arr.Elements)
		idx := int64(0)
		if len(args) > 0 {
			idx = toInt(args[0])
		}
		if idx < 0 {
			idx = int64(n) + idx
		}
		if idx < 0 || idx >= int64(n) {
			return object.UndefinedSingleton
		}
		return arr.Elements[idx]
	}))

	// fill(value, start, end): 用固定值填充数组的一部分
	p.SetProperty("fill", object.NewBuiltinMethod("fill", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "fill", this)
		}
		n := len(arr.Elements)
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			val = args[0]
		}
		start := 0
		end := n
		if len(args) > 1 {
			start = clampIndex(toInt(args[1]), n)
		}
		if len(args) > 2 {
			end = clampIndex(toInt(args[2]), n)
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
			return thisTypeError("Array", "copyWithin", this)
		}
		n := len(arr.Elements)
		if n == 0 || len(args) < 1 {
			return arr
		}
		target := clampIndex(toInt(args[0]), n)
		start := 0
		end := n
		if len(args) > 1 {
			start = clampIndex(toInt(args[1]), n)
		}
		if len(args) > 2 {
			end = clampIndex(toInt(args[2]), n)
		}
		if target >= n || start >= n || start >= end {
			return arr
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

	// lastIndexOf(item, fromIndex): 从后向前查找 (SameValueZero，支持 NaN)
	p.SetProperty("lastIndexOf", object.NewBuiltinMethod("lastIndexOf", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "lastIndexOf", this)
		}
		if len(args) < 1 {
			return object.NewNumber(-1)
		}
		target := args[0]
		n := len(arr.Elements)
		fromIdx := n - 1
		if len(args) > 1 {
			f := toInt(args[1])
			if f < 0 {
				fromIdx = n + int(f)
			} else if f < int64(n) {
				fromIdx = int(f)
			}
			// f >= n 时保持默认 n-1
		}
		if fromIdx >= n {
			fromIdx = n - 1
		}
		if fromIdx < 0 {
			return object.NewNumber(-1)
		}
		for i := fromIdx; i >= 0; i-- {
			if sameValueZero(arr.Elements[i], target) {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// flat(depth): 扁平化数组。depth 默认 1，Infinity 表示完全展开
	p.SetProperty("flat", object.NewBuiltinMethod("flat", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "flat", this)
		}
		depth := 1
		if len(args) > 0 {
			d := toFloat(args[0])
			switch {
			case math.IsInf(d, 1):
				depth = flatInfiniteDepth
			case math.IsNaN(d):
				depth = 0
			default:
				depth = int(toInt(args[0]))
				if depth < 0 {
					depth = 0
				}
			}
		}
		return object.NewArray(flatArray(arr.Elements, depth))
	}))

	return p
}

// flatInfiniteDepth 是 flat() 的深度哨兵值，表示 Infinity (完全展开)。
const flatInfiniteDepth = -1

// flatArray 递归扁平化数组。
// depth 为 0 时不再展开；为 flatInfiniteDepth 时无限展开。
func flatArray(elements []object.Value, depth int) []object.Value {
	var result []object.Value
	for _, e := range elements {
		if arr, ok := e.(*object.Array); ok && depth != 0 {
			result = append(result, flatArray(arr.Elements, nextFlatDepth(depth))...)
		} else {
			result = append(result, e)
		}
	}
	return result
}

// nextFlatDepth 递减 flat 的剩余深度。Infinity 深度保持不变。
func nextFlatDepth(depth int) int {
	if depth < 0 {
		return depth
	}
	return depth - 1
}

// defaultSortLess 实现 Array.prototype.sort 的默认比较 (规范 SortCompare):
// undefined 排在最后且不参与比较，其余元素先转字符串再按 UTF-16 码元比较。
func defaultSortLess(a, b object.Value) bool {
	aUndef := isUndefinedValue(a)
	bUndef := isUndefinedValue(b)
	if aUndef || bUndef {
		// undefined 永远排在最后；两个 undefined 之间由稳定排序保持原序
		return !aUndef && bUndef
	}
	return sortKeyString(a) < sortKeyString(b)
}

// sortKeyString 返回元素在默认排序中的比较键。
func sortKeyString(v object.Value) string {
	if v == nil {
		return "undefined"
	}
	return toStr(v)
}

// arrayCallbackArgs 解析数组高阶方法的公共参数 (callback, thisArg)。
//
// 返回值的第 4 项是错误对象: 非 nil 时调用方应直接返回它，由 VM 转为异常。
// 旧实现在 this 类型不符或回调不可调用时静默返回默认值，掩盖了真实错误。
func arrayCallbackArgs(method string, this object.Value, args []object.Value) (*object.Array, object.Value, object.Value, object.Value) {
	arr, ok := this.(*object.Array)
	if !ok {
		return nil, nil, nil, thisTypeError("Array", method, this)
	}
	if len(args) < 1 || !object.IsCallable(args[0]) {
		return nil, nil, nil, object.NewTypeError("%s is not a function", argInspect(args, 0))
	}
	var thisArg object.Value
	if len(args) > 1 {
		thisArg = args[1]
	}
	return arr, args[0], thisArg, nil
}

// callbackThrown 把回调桥 (object.CallFunction) 报告的异常恢复为可抛出的
// JS 值。必须在每次 CallFunction 之后立即调用:
//   - 回调正常返回 → nil (继续循环)；
//   - 回调抛出 Error → 原始错误对象 (保留错误类型，返回给 VM 抛出)；
//   - 回调抛出非 Error 值 → 包装为通用 Error。
//
// 不检查的话，回调抛出的异常会被下一次 CallFunction 的进入清空动作
// 静默吞掉，且循环错误地继续执行后续元素 (规范要求立即中止)。
func callbackThrown() object.Value {
	cbErr := object.TakeCallbackError()
	if cbErr == nil {
		return nil
	}
	if v := object.TakeCallbackErrorValue(); v != nil {
		if _, isErr := v.(*object.Error); isErr {
			return v
		}
	}
	return object.NewErrorWithName("Error", cbErr.Error())
}

// argInspect 取第 idx 个参数的可读表示，越界时返回 "undefined"。
func argInspect(args []object.Value, idx int) string {
	if idx >= len(args) {
		return "undefined"
	}
	return toStr(args[idx])
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

// sameValueZero 实现 ECMAScript 的 SameValueZero 比较。
// 与 === 的唯一区别是 NaN 等于自身。
// indexOf / lastIndexOf / includes 使用此语义。
func sameValueZero(a, b object.Value) bool {
	if strictEqualValues(a, b) {
		return true
	}
	an, aok := a.(*object.Number)
	bn, bok := b.(*object.Number)
	if aok && bok {
		return math.IsNaN(an.Value) && math.IsNaN(bn.Value)
	}
	return false
}

// setupArrayGlobal 创建 Array 构造器。
//
// 这里返回 *object.BuiltinFunction 而非常规对象: Array 的 typeof 必须是
// "function"，且 Array(3) / new Array(1, 2) 必须可调用。静态方法
// (isArray/from/of) 作为构造器自身的属性挂载，语义不变。
func setupArrayGlobal() *object.BuiltinFunction {
	arr := object.NewBuiltin("Array", func(args ...object.Value) object.Value {
		// Array(len): 单参数且为整数时，返回指定长度的数组。
		// 本运行时没有"空洞"的概念，用 undefined 填充，
		// 因此 Array(3).length === 3，但 (0 in Array(3)) 为 true。
		if len(args) == 1 {
			if n, ok := args[0].(*object.Number); ok {
				l := n.Value
				if math.IsNaN(l) || l != math.Trunc(l) || l < 0 || l > 4294967295 {
					return object.NewRangeError("Invalid array length")
				}
				els := make([]object.Value, int(l))
				for i := range els {
					els[i] = object.UndefinedSingleton
				}
				return object.NewArray(els)
			}
		}
		// Array(...items): 直接以参数为元素
		items := append([]object.Value(nil), args...)
		return object.NewArray(items)
	})
	arr.SetProperty("name", object.NewString("Array"))
	arr.SetProperty("isArray", object.NewBuiltin("isArray", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		_, ok := args[0].(*object.Array)
		return object.NewBoolean(ok)
	}))
	// Array.from(arrayLike, mapFn, thisArg)
	arr.SetProperty("from", object.NewBuiltin("from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewArray([]object.Value{})
		}
		var elements []object.Value
		switch src := args[0].(type) {
		case *object.Array:
			elements = append([]object.Value(nil), src.Elements...)
		case *object.String:
			// 按 UTF-16 码元拆分。旧实现用 len(s.Value) (字节数) 分配切片、
			// 却用 `for i, ch := range` (字节偏移) 赋值，遇到非 ASCII 字符
			// 会在切片尾部留下 nil 空洞。
			for _, ch := range object.SplitCharsUTF16(src.Value) {
				elements = append(elements, object.NewString(ch))
			}
		case *object.Set:
			elements = append([]object.Value(nil), src.Values...)
		case *object.Map:
			for _, entry := range src.Entries {
				elements = append(elements,
					object.NewArray([]object.Value{entry.Key, entry.Value}))
			}
		default:
			return object.NewArray([]object.Value{})
		}

		// 可选的 mapFn(value, index)
		if len(args) > 1 {
			fn := args[1]
			if !object.IsCallable(fn) {
				return object.NewTypeError("%s is not a function", toStr(fn))
			}
			var thisArg object.Value
			if len(args) > 2 {
				thisArg = args[2]
			}
			mapped := make([]object.Value, len(elements))
			for i, e := range elements {
				v := object.CallFunction(fn, thisArg, e, object.NewNumber(float64(i)))
				if thrown := callbackThrown(); thrown != nil {
					return thrown
				}
				if v == nil {
					v = object.UndefinedSingleton
				}
				mapped[i] = v
			}
			elements = mapped
		}
		return object.NewArray(elements)
	}))

	// Array.of(...items)
	arr.SetProperty("of", object.NewBuiltin("of", func(args ...object.Value) object.Value {
		// 复制 args: 它是 VM 的参数切片，直接持有会与 VM 栈共享底层数组
		items := append([]object.Value(nil), args...)
		return object.NewArray(items)
	}))

	// Array.fromAsync(iterable, mapFn, thisArg) (ES2023):
	// 异步收集可迭代的值，返回 Promise<Array>。
	// 元素为 Promise 时先等待 (与 Array.from 不同)；mapFn 在 await 之后应用。
	// 实现为 Promise.then 链的串行展开 (Resolve 对 Promise 参数自动采用)。
	arr.SetProperty("fromAsync", object.NewBuiltin("fromAsync", func(args ...object.Value) object.Value {
		result := object.NewPromise()
		var mapFn object.Value = object.UndefinedSingleton
		var thisArg object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			mapFn = args[1]
			if !object.IsCallable(mapFn) {
				result.Reject(object.NewErrorWithName("TypeError", "Array.fromAsync: mapFn must be a function"))
				return result
			}
			if len(args) > 2 {
				thisArg = args[2]
			}
		}
		var src object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			src = args[0]
		}
		next, ok := object.Iterate(src)
		if !ok {
			result.Reject(object.NewErrorWithName("TypeError", "Array.fromAsync: argument is not iterable"))
			return result
		}

		// 同步抽取全部元素 (同步迭代器)
		var items []object.Value
		for {
			item, done := next()
			if done {
				break
			}
			items = append(items, item)
		}

		elements := make([]object.Value, 0, len(items))

		// acc: 串行化每个元素的等待与映射
		acc := object.NewPromise()
		acc.Resolve(object.UndefinedSingleton)
		for _, item := range items {
			item := item
			if p, isP := item.(*object.Promise); isP {
				// Promise 元素: 先等待其 resolve，再推送终值
				acc = acc.Then(goFn(func(rargs []object.Value) object.Value {
					return p // Resolve 会自动采用并等待
				}))
				acc = acc.Then(goFn(func(rargs []object.Value) object.Value {
					return pushItem(mapFn, thisArg, rargs[0], len(elements), &elements)
				}))
			} else {
				acc = acc.Then(goFn(func(rargs []object.Value) object.Value {
					return pushItem(mapFn, thisArg, item, len(elements), &elements)
				}))
			}
		}
		acc.Then(goFn(func(rargs []object.Value) object.Value {
			result.Resolve(object.NewArray(elements))
			return object.UndefinedSingleton
		}))
		return result
	}))
	return arr
}

// goFn 把 Go 闭包包装成 JS 可调用值 (Promise.then 回调用)。
func goFn(f func(args []object.Value) object.Value) object.Value {
	return object.NewBuiltin("", func(args ...object.Value) object.Value {
		return f(args)
	})
}

// pushItem 应用 mapFn (可选) 并把结果推入 elements。
// mapFn 返回 Promise 时返回该 Promise，由 then 链采用后推送终值。
func pushItem(mapFn, thisArg, item object.Value, idx int, elements *[]object.Value) object.Value {
	if mapFn != nil && !isUndefinedValue(mapFn) {
		mapped := object.CallFunction(mapFn, thisArg, item, object.NewInt(int64(idx)))
		if thrown := callbackThrown(); thrown != nil {
			return thrown
		}
		if _, isP := mapped.(*object.Promise); isP {
			return mapped
		}
		*elements = append(*elements, mapped)
		return mapped
	}
	*elements = append(*elements, item)
	return item
}

// sortArrayValues 对给定数组就地排序并返回之。
// 支持 compareFn 参数；默认排序与 Array.prototype.sort 一致 (字符串序)。
// 供 sort / toSorted 共用。
func sortArrayValues(arr *object.Array, args []object.Value) object.Value {
	if len(args) > 0 && !isUndefinedValue(args[0]) {
		compareFn := args[0]
		if !object.IsCallable(compareFn) {
			return object.NewTypeError("%s is not a function", toStr(compareFn))
		}
		var sortThrown object.Value
		sort.SliceStable(arr.Elements, func(i, j int) bool {
			if sortThrown != nil {
				return false
			}
			result := object.CallFunction(compareFn, object.UndefinedSingleton,
				arr.Elements[i], arr.Elements[j])
			if thrown := callbackThrown(); thrown != nil {
				sortThrown = thrown
				return false
			}
			return toFloat(result) < 0
		})
		if sortThrown != nil {
			return sortThrown
		}
		return arr
	}
	sort.SliceStable(arr.Elements, func(i, j int) bool {
		return defaultSortLess(arr.Elements[i], arr.Elements[j])
	})
	return arr
}

// spliceArgs 计算 splice/toSpliced 的 (start, deleteCount)。
// args: [start, deleteCount, ...items]。
func spliceArgs(n int, args []object.Value) (int, int) {
	start := 0
	if len(args) > 0 {
		start = clampIndex(toInt(args[0]), n)
	}
	count := n - start
	if len(args) > 1 {
		count = int(toInt(args[1]))
		if count < 0 {
			count = 0
		}
		if count > n-start {
			count = n - start
		}
	}
	return start, count
}
