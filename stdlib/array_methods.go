package stdlib

import (
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// setupArrayProto 创建 Array.prototype 对象。
// 所有数组实例的原型，包含 push, pop, map 等方法。
// 方法签名: NewBuiltinMethod(name, func(this Value, args ...Value) Value)
func setupArrayProto() *object.Object {
	p := object.NewObject()

	// push(...items): 向数组末尾添加元素，返回新长度
	p.SetBuiltinProperty("push", object.NewBuiltinMethod("push", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("push", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		if l.n+int64(len(args)) > maxArrayLikeLen {
			return object.NewRangeError("Invalid array length")
		}
		n := l.n
		for _, a := range args {
			// 规范的每一步 Set(O, ToString(n), E, true): 写索引失败 (元素位
			// 只读 / 对象不可扩展) 必须立刻抛 TypeError。
			if serr := l.setOrThrow(n, a); serr != nil {
				return serr
			}
			n++
		}
		if lerr := l.setLenOrThrow(n); lerr != nil {
			return lerr
		}
		return object.NewNumber(float64(n))
	}))

	// pop(): 移除并返回数组最后一个元素
	p.SetBuiltinProperty("pop", object.NewBuiltinMethod("pop", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("pop", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		if l.n == 0 {
			if lerr := l.setLenOrThrow(0); lerr != nil {
				return lerr
			}
			return object.UndefinedSingleton
		}
		lastIdx := l.n - 1
		last := l.get(lastIdx)
		if lerr := l.setLenOrThrow(lastIdx); lerr != nil {
			return lerr
		}
		if l.err != nil {
			return l.err
		}
		if l.arr == nil {
			if o, ok := l.obj.(*object.Object); ok {
				o.DeleteProperty(arrayIndexKey(lastIdx))
			}
		}
		return last
	}))

	// shift(): 移除并返回数组第一个元素
	p.SetBuiltinProperty("shift", object.NewBuiltinMethod("shift", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("shift", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		if l.n == 0 {
			if lerr := l.setLenOrThrow(0); lerr != nil {
				return lerr
			}
			return object.UndefinedSingleton
		}
		first := l.get(0)
		if l.err != nil {
			return l.err
		}
		if l.arr != nil {
			// 真实数组: shift 等价于"删掉索引 0 + length-1"。走逐位搬移会把
			// O(n) 变成逐元素 Set; 这里直接用切片, 但 length 的写入仍必须
			// 经 setLenOrThrow 以暴露"不可写 ⇒ TypeError"。
			els := l.arr.Elements
			if len(els) > 0 {
				l.arr.Elements = append(els[:0:0], els[1:]...)
			}
			if lerr := l.setLenOrThrow(l.n - 1); lerr != nil {
				return lerr
			}
			return first
		}
		for i := int64(1); i < l.n; i++ {
			if l.has(i) {
				if serr := l.setOrThrow(i-1, l.get(i)); serr != nil {
					return serr
				}
			} else {
				l.deleteOrThrow(i - 1)
			}
		}
		l.deleteOrThrow(l.n - 1)
		if lerr := l.setLenOrThrow(l.n - 1); lerr != nil {
			return lerr
		}
		return first
	}))

	// unshift(...items): 向数组开头添加元素，返回新长度
	p.SetBuiltinProperty("unshift", object.NewBuiltinMethod("unshift", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("unshift", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		if l.n+int64(len(args)) > maxArrayLikeLen {
			return object.NewRangeError("Invalid array length")
		}
		n := l.n
		cnt := int64(len(args))
		// 真实数组也要逐位走 Set: 元素位可能被 freeze 成只读, 或对象不可
		// 扩展 (set-length-array-is-frozen 族)。切片快速路径会绕过这些检查。
		for i := n - 1; i >= 0; i-- {
			if l.has(i) {
				if serr := l.setOrThrow(i+cnt, l.get(i)); serr != nil {
					return serr
				}
			} else {
				l.deleteOrThrow(i + cnt)
			}
		}
		for i, a := range args {
			if serr := l.setOrThrow(int64(i), a); serr != nil {
				return serr
			}
		}
		if lerr := l.setLenOrThrow(n + cnt); lerr != nil {
			return lerr
		}
		return object.NewNumber(float64(n + cnt))
	}))

	// join(separator): 用分隔符连接所有元素
	// undefined / null 元素输出为空串；separator 省略或为 null/undefined 时用 ","
	p.SetBuiltinProperty("join", object.NewBuiltinMethod("join", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("join", this)
		if errVal != nil {
			return errVal
		}
		sep := ","
		if len(args) > 0 && !isUndefinedValue(args[0]) && !isNullValue(args[0]) {
			sep = toStr(args[0])
		}
		parts := make([]string, 0, l.n)
		for i := int64(0); i < l.n; i++ {
			e := l.get(i)
			if l.err != nil {
				return l.err
			}
			if e == nil || isUndefinedValue(e) || isNullValue(e) {
				parts = append(parts, "")
			} else {
				parts = append(parts, toStr(e))
			}
		}
		return object.NewString(strings.Join(parts, sep))
	}))

	// slice(start, end): 返回数组的一部分 (浅拷贝，不修改原数组)
	p.SetBuiltinProperty("slice", object.NewBuiltinMethod("slice", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("slice", this)
		if errVal != nil {
			return errVal
		}
		n := int(l.n)
		start := 0
		end := n
		if len(args) > 0 {
			var serr object.Value
			start, serr = specIndexClamp(args[0], n)
			if serr != nil {
				return serr
			}
		}
		if len(args) > 1 {
			var eerr object.Value
			end, eerr = specIndexClamp(args[1], n)
			if eerr != nil {
				return eerr
			}
		}
		if start >= end {
			start, end = 0, 0
		}
		a, spErr := arraySpeciesCreate(l, int64(end-start))
		if spErr != nil {
			return spErr
		}
		var out int64
		for i := start; i < end; i++ {
			if l.has(int64(i)) {
				if derr := setIndexValueOrErr(a, out, l.get(int64(i))); derr != nil {
					return derr
				}
			}
			if l.err != nil {
				return l.err
			}
			out++
		}
		return a
	}))

	// splice(start, deleteCount, ...items): 修改数组内容
	p.SetBuiltinProperty("splice", object.NewBuiltinMethod("splice", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("splice", this)
		if errVal != nil {
			return errVal
		}
		if l.arr == nil {
			if mv := l.checkMutable(); mv != nil {
				return mv
			}
		}
		if l.arr == nil {
			return genericSplice(l, args)
		}
		arr := l.arr
		n := len(arr.Elements)
		start := 0
		if len(args) > 0 {
			var serr object.Value
			start, serr = specIndexClamp(args[0], n)
			if serr != nil {
				return serr
			}
		}
		// 未指定 deleteCount 时，删除从 start 到末尾的所有元素。
		// 这里必须基于 start 计算，否则 arr.Elements[start:start+deleteCount] 会越界。
		deleteCount := n - start
		if len(args) > 1 {
			d, derr := specIndex(args[1])
			if derr != nil {
				return derr
			}
			deleteCount = int(d)
			if deleteCount < 0 {
				deleteCount = 0
			}
			if deleteCount > n-start {
				deleteCount = n - start
			}
		}
		deletedArr, spErr := arraySpeciesCreate(l, int64(deleteCount))
		if spErr != nil {
			return spErr
		}
		deleted := make([]object.Value, deleteCount)
		copy(deleted, arr.Elements[start:start+deleteCount])
		for i := range deleted {
			if deleted[i] != nil {
				if derr := setIndexValueOrErr(deletedArr, int64(i), deleted[i]); derr != nil {
					return derr
				}
			}
		}
		items := []object.Value{}
		if len(args) > 2 {
			items = args[2:]
		}
		newElements := make([]object.Value, 0, n-deleteCount+len(items))
		newElements = append(newElements, arr.Elements[:start]...)
		newElements = append(newElements, items...)
		newElements = append(newElements, arr.Elements[start+deleteCount:]...)
		arr.Elements = newElements
		return deletedArr
	}))

	// concat(...arrays): 连接多个数组
	p.SetBuiltinProperty("concat", object.NewBuiltinMethod("concat", func(this object.Value, args ...object.Value) object.Value {
		// 规范第一步是 ToObject(this): null / undefined 必须抛 TypeError。
		// 此前非数组 this 被静默当成"空数组", 于是 `var c = Array.prototype
		// .concat; c()` 返回一个新数组而不是抛 (test262
		// Array/prototype/methods-called-as-functions.js)。这条以前被
		// "内建方法裸调用 → is not a function" 那个假象遮住了 (rYFTlt)。
		if isUndefinedValue(this) || isNullValue(this) {
			return thisTypeError("Array", "concat", this)
		}
		l := &arrayLike{obj: this}
		if arr, ok := this.(*object.Array); ok {
			l.arr = arr
			l.n = int64(len(arr.Elements))
		}
		a, spErr := arraySpeciesCreate(l, 0)
		if spErr != nil {
			return spErr
		}
		// 规范: this 与每个参数先经 IsConcatSpreadable 判定,
		// 可展开者按 length/索引逐项拷贝 (支持类数组与 @@isConcatSpreadable)。
		items := make([]object.Value, 0, len(args)+1)
		items = append(items, this)
		items = append(items, args...)
		var n int64
		for _, e := range items {
			spreadable, sErr := isConcatSpreadable(e)
			if sErr != nil {
				return sErr
			}
			if !spreadable {
				if n >= maxArrayLikeLen {
					return object.NewTypeError("concat result exceeds maximum length")
				}
				if derr := setIndexValueOrErr(a, n, e); derr != nil {
					return derr
				}
				n++
				continue
			}
			sl, lErr := asArrayLike("concat", e)
			if lErr != nil {
				return lErr
			}
			if n+sl.n > maxArrayLikeLen {
				return object.NewTypeError("concat result exceeds maximum length")
			}
			for i := int64(0); i < sl.n; i++ {
				if sl.has(i) {
					if derr := setIndexValueOrErr(a, n, sl.get(i)); derr != nil {
						return derr
					}
				}
				if sl.err != nil {
					return sl.err
				}
				n++
			}
		}
		return a
	}))

	// indexOf(item, fromIndex): 查找元素的索引
	// 采用 SameValueZero 比较，因此 [NaN].indexOf(NaN) === 0
	p.SetBuiltinProperty("indexOf", object.NewBuiltinMethod("indexOf", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("indexOf", this)
		if errVal != nil {
			return errVal
		}
		// 缺省 searchElement 即 undefined (规范 IndexOf 的 searchElement 参数
		// 不传时为 undefined), 故 [undefined].indexOf() === 0。
		var target object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			target = args[0]
		}
		n := int(l.n)
		fromIdx := 0
		if len(args) > 1 {
			// fromIndex 先 ToIntegerOrInfinity: 对象经 ToPrimitive(number)
			// (先 valueOf 后 toString), 用户 valueOf/toString 抛错原样传播。
			f, ferr := toIntegerOrInfinity(args[1])
			if ferr != nil {
				return ferr
			}
			if f >= int64(n) {
				return object.NewNumber(-1)
			}
			fromIdx = clampIndex(f, n)
		}
		for i := fromIdx; i < n; i++ {
			if l.err != nil {
				return l.err
			}
			if l.has(int64(i)) && strictEqualValues(l.get(int64(i)), target) {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// includes(item, fromIndex): 检查数组是否包含某元素
	// 同样采用 SameValueZero，因此 [NaN].includes(NaN) === true
	p.SetBuiltinProperty("includes", object.NewBuiltinMethod("includes", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("includes", this)
		if errVal != nil {
			return errVal
		}
		if len(args) < 1 {
			return object.NewBoolean(false)
		}
		target := args[0]
		n := int(l.n)
		fromIdx := 0
		if len(args) > 1 {
			fromIdx = clampIndex(toInt(args[1]), n)
		}
		for i := fromIdx; i < n; i++ {
			// includes 不跳过洞: 洞按 undefined 参与 SameValueZero 比较
			if l.err != nil {
				return l.err
			}
			if sameValueZero(l.get(int64(i)), target) {
				return object.NewBoolean(true)
			}
		}
		return object.NewBoolean(false)
	}))

	// find(callback, thisArg): 返回第一个满足条件的元素
	p.SetBuiltinProperty("find", object.NewBuiltinMethod("find", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("find", this, args)
		if errVal != nil {
			return errVal
		}
		for i := int64(0); i < l.n; i++ {
			elem := l.get(i)
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				elem,
				object.NewInt(i),
				l.obj,
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
	p.SetBuiltinProperty("findIndex", object.NewBuiltinMethod("findIndex", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("findIndex", this, args)
		if errVal != nil {
			return errVal
		}
		for i := int64(0); i < l.n; i++ {
			ev := l.get(i)
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				ev,
				object.NewInt(i),
				l.obj,
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
	p.SetBuiltinProperty("forEach", object.NewBuiltinMethod("forEach", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("forEach", this, args)
		if errVal != nil {
			return errVal
		}
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			object.CallFunction(callback, thisArg,
				l.get(i),
				object.NewInt(i),
				l.obj,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return object.UndefinedSingleton
	}))

	// map(callback, thisArg): 对每个元素调用回调，返回结果数组
	p.SetBuiltinProperty("map", object.NewBuiltinMethod("map", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("map", this, args)
		if errVal != nil {
			return errVal
		}
		a, spErr := arraySpeciesCreate(l, l.n)
		if spErr != nil {
			return spErr
		}
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			mapped := object.CallFunction(callback, thisArg,
				l.get(i),
				object.NewInt(i),
				l.obj,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if mapped == nil {
				mapped = object.UndefinedSingleton
			}
			if derr := setIndexValueOrErr(a, i, mapped); derr != nil {
				return derr
			}
		}
		return a
	}))

	// filter(callback, thisArg): 过滤数组，返回满足条件的元素
	p.SetBuiltinProperty("filter", object.NewBuiltinMethod("filter", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("filter", this, args)
		if errVal != nil {
			return errVal
		}
		a, spErr := arraySpeciesCreate(l, 0)
		if spErr != nil {
			return spErr
		}
		var out int64
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			elem := l.get(i)
			keep := object.CallFunction(callback, thisArg,
				elem,
				object.NewInt(i),
				l.obj,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if keep != nil && keep.IsTruthy() {
				if derr := setIndexValueOrErr(a, out, elem); derr != nil {
					return derr
				}
				out++
			}
		}
		return a
	}))

	// reduce(callback, initialValue): 归约数组为单个值
	// 规范: reduce 不接受 thisArg，回调的 this 是 undefined；
	// 空数组且未提供 initialValue 时抛 TypeError。
	p.SetBuiltinProperty("reduce", object.NewBuiltinMethod("reduce", func(this object.Value, args ...object.Value) object.Value {
		l, callback, _, errVal := arrayCallbackArgs("reduce", this, args)
		if errVal != nil {
			return errVal
		}
		var acc object.Value
		var startIdx int64
		if len(args) > 1 {
			acc = args[1]
		} else {
			// 找第一个存在的元素作为累加器初值；全空则抛 TypeError
			found := false
			for i := int64(0); i < l.n; i++ {
				if l.has(i) {
					acc = l.get(i)
					startIdx = i + 1
					found = true
					break
				}
				if l.err != nil {
					return l.err
				}
			}
			if !found {
				return object.NewTypeError("Reduce of empty array with no initial value")
			}
		}
		for i := startIdx; i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			acc = object.CallFunction(callback, object.UndefinedSingleton,
				acc,
				l.get(i),
				object.NewInt(i),
				l.obj,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return acc
	}))

	// reduceRight(callback, initialValue): 从右向左归约
	p.SetBuiltinProperty("reduceRight", object.NewBuiltinMethod("reduceRight", func(this object.Value, args ...object.Value) object.Value {
		l, callback, _, errVal := arrayCallbackArgs("reduceRight", this, args)
		if errVal != nil {
			return errVal
		}
		var acc object.Value
		endIdx := l.n - 1
		if len(args) > 1 {
			acc = args[1]
		} else {
			found := false
			for i := l.n - 1; i >= 0; i-- {
				if l.has(i) {
					acc = l.get(i)
					endIdx = i - 1
					found = true
					break
				}
				if l.err != nil {
					return l.err
				}
			}
			if !found {
				return object.NewTypeError("Reduce of empty array with no initial value")
			}
		}
		for i := endIdx; i >= 0; i-- {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			acc = object.CallFunction(callback, object.UndefinedSingleton,
				acc,
				l.get(i),
				object.NewInt(i),
				l.obj,
			)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
		}
		return acc
	}))

	// some(callback, thisArg): 如果有元素满足条件则返回 true
	p.SetBuiltinProperty("some", object.NewBuiltinMethod("some", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("some", this, args)
		if errVal != nil {
			return errVal
		}
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				l.get(i),
				object.NewInt(i),
				l.obj,
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
	p.SetBuiltinProperty("every", object.NewBuiltinMethod("every", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("every", this, args)
		if errVal != nil {
			return errVal
		}
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				l.get(i),
				object.NewInt(i),
				l.obj,
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
	p.SetBuiltinProperty("flatMap", object.NewBuiltinMethod("flatMap", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("flatMap", this, args)
		if errVal != nil {
			return errVal
		}
		var result []object.Value
		for i := int64(0); i < l.n; i++ {
			if !l.has(i) {
				continue
			}
			mapped := object.CallFunction(callback, thisArg,
				l.get(i),
				object.NewInt(i),
				l.obj,
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
	p.SetBuiltinProperty("keys", object.NewBuiltinMethod("keys", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "keys", this)
		}
		return object.NewArrayIndexIterator(arr)
	}))

	// values(): 迭代元素
	p.SetBuiltinProperty("values", object.NewBuiltinMethod("values", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "values", this)
		}
		return object.NewArrayIterator(arr)
	}))

	// entries(): 迭代 [index, element] 对
	p.SetBuiltinProperty("entries", object.NewBuiltinMethod("entries", func(this object.Value, args ...object.Value) object.Value {
		arr, ok := this.(*object.Array)
		if !ok {
			return thisTypeError("Array", "entries", this)
		}
		return object.NewArrayEntryIterator(arr)
	}))

	// ===== ES2023 findLast 系列 =====

	// findLast(callback, thisArg): 从后向前找第一个满足条件的元素
	p.SetBuiltinProperty("findLast", object.NewBuiltinMethod("findLast", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("findLast", this, args)
		if errVal != nil {
			return errVal
		}
		for i := l.n - 1; i >= 0; i-- {
			elem := l.get(i)
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				elem, object.NewInt(i), l.obj)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return elem
			}
		}
		return object.UndefinedSingleton
	}))

	// findLastIndex(callback, thisArg): 从后向前找第一个满足条件的索引
	p.SetBuiltinProperty("findLastIndex", object.NewBuiltinMethod("findLastIndex", func(this object.Value, args ...object.Value) object.Value {
		l, callback, thisArg, errVal := arrayCallbackArgs("findLastIndex", this, args)
		if errVal != nil {
			return errVal
		}
		for i := l.n - 1; i >= 0; i-- {
			ev := l.get(i)
			if l.err != nil {
				return l.err
			}
			result := object.CallFunction(callback, thisArg,
				ev, object.NewInt(i), l.obj)
			if thrown := callbackThrown(); thrown != nil {
				return thrown
			}
			if result != nil && result.IsTruthy() {
				return object.NewInt(i)
			}
		}
		return object.NewInt(-1)
	}))

	// ===== ES2023 变更即拷贝 (change-by-copy) 方法 =====

	// toSorted(compareFn): sorted() 的新副本
	p.SetBuiltinProperty("toSorted", object.NewBuiltinMethod("toSorted", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("toSorted", this)
		if errVal != nil {
			return errVal
		}
		cp := make([]object.Value, l.n)
		for i := int64(0); i < l.n; i++ {
			if l.has(i) {
				cp[i] = l.get(i)
			} else {
				cp[i] = object.UndefinedSingleton
			}
		}
		return sortArrayValues(object.NewArray(cp), args)
	}))

	// toReversed(): reversed() 的新副本
	p.SetBuiltinProperty("toReversed", object.NewBuiltinMethod("toReversed", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("toReversed", this)
		if errVal != nil {
			return errVal
		}
		cp := make([]object.Value, l.n)
		for i := int64(0); i < l.n; i++ {
			cp[i] = l.get(l.n - 1 - i)
		}
		return object.NewArray(cp)
	}))

	// toSpliced(start, deleteCount, ...items): spliced() 的新副本
	p.SetBuiltinProperty("toSpliced", object.NewBuiltinMethod("toSpliced", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("toSpliced", this)
		if errVal != nil {
			return errVal
		}
		start, count := spliceArgs(int(l.n), args)
		cp := make([]object.Value, 0, int(l.n)+len(args))
		for i := 0; i < start; i++ {
			cp = append(cp, l.get(int64(i)))
		}
		cp = append(cp, args[2:]...)
		for i := start + count; i < int(l.n); i++ {
			cp = append(cp, l.get(int64(i)))
		}
		return object.NewArray(cp)
	}))

	// with(index, value): 替换单个元素的新副本
	p.SetBuiltinProperty("with", object.NewBuiltinMethod("with", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("with", this)
		if errVal != nil {
			return errVal
		}
		if len(args) == 0 {
			return object.NewRangeError("Array.prototype.with: index is required")
		}
		idx := int(toFloat(args[0]))
		if idx < 0 {
			idx += int(l.n)
		}
		if idx < 0 || idx >= int(l.n) {
			return object.NewRangeError("Array.prototype.with: invalid index %d", idx)
		}
		cp := make([]object.Value, l.n)
		for i := int64(0); i < l.n; i++ {
			cp[i] = l.get(i)
		}
		var val object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			val = args[1]
		}
		cp[idx] = val
		return object.NewArray(cp)
	}))

	// reverse(): 反转数组 (原地修改并返回自身)
	p.SetBuiltinProperty("reverse", object.NewBuiltinMethod("reverse", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("reverse", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		n := l.n
		for i := int64(0); i < n/2; i++ {
			j := n - 1 - i
			loHas, hiHas := l.has(i), l.has(j)
			lo, hi := l.get(i), l.get(j)
			if hiHas {
				l.set(i, hi)
			} else if o, ok := l.obj.(*object.Object); ok && l.arr == nil {
				o.DeleteProperty(arrayIndexKey(i))
			}
			if loHas {
				l.set(j, lo)
			} else if o, ok := l.obj.(*object.Object); ok && l.arr == nil {
				o.DeleteProperty(arrayIndexKey(j))
			}
		}
		return l.obj
	}))

	// sort(compareFn): 排序数组 (原地修改并返回自身)
	p.SetBuiltinProperty("sort", object.NewBuiltinMethod("sort", func(this object.Value, args ...object.Value) object.Value {
		// 规范 sec-array.prototype.sort 第 2 步: IsCallable(comparefn)
		// 必须在 ToObject/读 length **之前** 观察 —— comparefn 非函数/非
		// undefined 时立即抛 TypeError, 不得触发 this.length 的 getter
		// (comparefn-nonfunction-call-throws 族)。
		var compareFn object.Value
		if len(args) > 0 && !isUndefinedValue(args[0]) {
			if !object.IsCallable(args[0]) {
				return object.NewTypeError("%s is not a function", toStr(args[0]))
			}
			compareFn = args[0]
		}
		l, errVal := asArrayLike("sort", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		return specSort(l, compareFn)
	}))

	// toString(): 数组转字符串 (等价于无参 join)
	p.SetBuiltinProperty("toString", object.NewBuiltinMethod("toString", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("toString", this)
		if errVal != nil {
			return errVal
		}
		parts := make([]string, 0, l.n)
		for i := int64(0); i < l.n; i++ {
			e := l.get(i)
			if l.err != nil {
				return l.err
			}
			if e == nil || isUndefinedValue(e) || isNullValue(e) {
				parts = append(parts, "")
			} else {
				parts = append(parts, toStr(e))
			}
		}
		return object.NewString(strings.Join(parts, ","))
	}))

	// toLocaleString(): 规范 23.1.3.32 —— 逐元素
	// ToString(Invoke(element, "toLocaleString")), null/undefined 计空串,
	// 分隔符为宿主 locale 定义 (本运行时无 Intl, 取 ",")。
	// 此前该方法完全缺失, 属性查找穿透到 Object.prototype.toLocaleString,
	// 后者对数组调 toString() 得到 "[object Array]" —— 于是依赖
	// `["",""].toLocaleString()` 取分隔符的用例 (如 TypedArray 的
	// toLocaleString {BigInt,Float} 系列) 全部失败。
	p.SetBuiltinProperty("toLocaleString", object.NewBuiltinMethod("toLocaleString", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("toLocaleString", this)
		if errVal != nil {
			return errVal
		}
		parts := make([]string, 0, l.n)
		for i := int64(0); i < l.n; i++ {
			e := l.get(i)
			if l.err != nil {
				return l.err
			}
			if e == nil || isUndefinedValue(e) || isNullValue(e) {
				parts = append(parts, "")
				continue
			}
			m, found := e.GetProperty("toLocaleString")
			if !found || !object.IsCallable(m) {
				parts = append(parts, toStr(e))
				continue
			}
			r := object.CallFunction(m, e)
			if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
				return ev
			}
			parts = append(parts, toStr(r))
		}
		return object.NewString(strings.Join(parts, ","))
	}))

	// at(index): 返回指定索引处的元素，支持负索引 (-1 表示最后一个)
	p.SetBuiltinProperty("at", object.NewBuiltinMethod("at", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("at", this)
		if errVal != nil {
			return errVal
		}
		idx := int64(0)
		if len(args) > 0 {
			// at(index) 的 index 走 ToIntegerOrInfinity: Symbol 抛 TypeError
			// (index-non-numeric-argument-tointeger-invalid)。
			var ierr object.Value
			idx, ierr = specIndex(args[0])
			if ierr != nil {
				return ierr
			}
		}
		if idx < 0 {
			idx = l.n + idx
		}
		if idx < 0 || idx >= l.n {
			return object.UndefinedSingleton
		}
		v := l.get(idx)
		if l.err != nil {
			return l.err
		}
		return v
	}))

	// fill(value, start, end): 用固定值填充数组的一部分
	p.SetBuiltinProperty("fill", object.NewBuiltinMethod("fill", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("fill", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		n := int(l.n)
		val := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			val = args[0]
		}
		start := 0
		end := n
		if len(args) > 1 {
			var serr object.Value
			start, serr = specIndexClamp(args[1], n)
			if serr != nil {
				return serr
			}
		}
		if len(args) > 2 {
			var eerr object.Value
			end, eerr = specIndexClamp(args[2], n)
			if eerr != nil {
				return eerr
			}
		}
		for i := start; i < end; i++ {
			l.set(int64(i), val)
		}
		return l.obj
	}))

	// copyWithin(target, start, end): 数组内部复制
	p.SetBuiltinProperty("copyWithin", object.NewBuiltinMethod("copyWithin", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("copyWithin", this)
		if errVal != nil {
			return errVal
		}
		if mv := l.checkMutable(); mv != nil {
			return mv
		}
		n := int(l.n)
		if n == 0 || len(args) < 1 {
			return l.obj
		}
		// 规范: every / target / start / end 依次 ToIntegerOrInfinity, 任一步
		// abrupt (Symbol / 带毒 valueOf) 都必须在任何写入前抛出。
		var terr object.Value
		target, terr := specIndexClamp(args[0], n)
		if terr != nil {
			return terr
		}
		start := 0
		end := n
		if len(args) > 1 {
			var serr object.Value
			start, serr = specIndexClamp(args[1], n)
			if serr != nil {
				return serr
			}
		}
		if len(args) > 2 {
			var eerr object.Value
			end, eerr = specIndexClamp(args[2], n)
			if eerr != nil {
				return eerr
			}
		}
		if target >= n || start >= n || start >= end {
			return l.obj
		}
		count := end - start
		if target+count > n {
			count = n - target
		}
		// 与目标重叠时按方向拷贝，避免覆盖未读的源
		if start < target && target < start+count {
			for i := count - 1; i >= 0; i-- {
				l.set(int64(target+i), l.get(int64(start+i)))
			}
		} else {
			for i := 0; i < count; i++ {
				l.set(int64(target+i), l.get(int64(start+i)))
			}
		}
		return l.obj
	}))

	// lastIndexOf(item, fromIndex): 从后向前查找 (SameValueZero，支持 NaN)
	p.SetBuiltinProperty("lastIndexOf", object.NewBuiltinMethod("lastIndexOf", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("lastIndexOf", this)
		if errVal != nil {
			return errVal
		}
		// 缺省 searchElement 即 undefined (同 indexOf)。
		var target object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			target = args[0]
		}
		n := int(l.n)
		fromIdx := n - 1
		if len(args) > 1 {
			// 同 indexOf: fromIndex 走 ToIntegerOrInfinity (对象先 ToPrimitive)。
			f, ferr := toIntegerOrInfinity(args[1])
			if ferr != nil {
				return ferr
			}
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
			if l.err != nil {
				return l.err
			}
			if l.has(int64(i)) && strictEqualValues(l.get(int64(i)), target) {
				return object.NewNumber(float64(i))
			}
		}
		return object.NewNumber(-1)
	}))

	// flat(depth): 扁平化数组。depth 默认 1，Infinity 表示完全展开
	p.SetBuiltinProperty("flat", object.NewBuiltinMethod("flat", func(this object.Value, args ...object.Value) object.Value {
		l, errVal := asArrayLike("flat", this)
		if errVal != nil {
			return errVal
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
		elements := make([]object.Value, 0, l.n)
		for i := int64(0); i < l.n; i++ {
			if l.has(i) {
				elements = append(elements, l.get(i))
			}
			if l.err != nil {
				return l.err
			}
		}
		return object.NewArray(flatArray(elements, depth))
	}))

	// 按规范标注各方法的 length（形参个数）。propertyHelper 的 length.js
	// 用例会逐一校验该值。
	for name, ln := range map[string]int{
		"at": 1, "concat": 1, "copyWithin": 2, "every": 1, "fill": 1,
		"filter": 1, "find": 1, "findIndex": 1, "findLast": 1,
		"findLastIndex": 1, "flatMap": 1, "forEach": 1, "includes": 1,
		"indexOf": 1, "join": 1, "lastIndexOf": 1, "map": 1, "push": 1,
		"reduce": 1, "reduceRight": 1, "slice": 2, "some": 1, "sort": 1,
		"splice": 2, "toSpliced": 2, "toSorted": 1, "unshift": 1, "with": 2,
	} {
		if v, ok := p.GetProperty(name); ok {
			if bm, isBM := v.(*object.BuiltinMethod); isBM {
				bm.Length = ln
			}
		}
	}

	return p
}

// isConcatSpreadable 实现规范的 IsConcatSpreadable(O):
// 非对象不可展开; @@isConcatSpreadable 优先, 缺省按 IsArray。
func isConcatSpreadable(v object.Value) (bool, object.Value) {
	if !isObjectLike(v) {
		return false, nil
	}
	if desc, found := object.LookupSymbolPropertyDescriptorChain(v, object.GetGlobalSymbol("Symbol.isConcatSpreadable")); found {
		var sv object.Value
		if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
			if acc.Getter != nil && object.IsCallable(acc.Getter) {
				sv = object.CallFunction(acc.Getter, v)
				if t := callbackThrown(); t != nil {
					return false, t
				}
			}
		} else {
			sv = desc.Value
		}
		if sv != nil && !isUndefinedValue(sv) {
			return sv.IsTruthy(), nil
		}
	}
	_, isArr := v.(*object.Array)
	return isArr, nil
}

// isConstructorValue 判断值是否为构造器 (规范的 IsConstructor)。
// 内建构造器 (Array/Object/String/...) 在 Gox 里是 *BuiltinFunction,
// object.IsConstructor 已按 NotConstructable 标记区分; Proxy 一律视为
// 可构造 (target 决定, 这里放宽)。
func isConstructorValue(v object.Value) bool {
	return object.IsConstructor(v)
}

// isObjectLike 判断值是否为"对象" (IsConstructor/ArraySpeciesCreate 的参数校验用)。
func isObjectLike(v object.Value) bool {
	switch v.(type) {
	case *object.Object, *object.Array, *object.Closure, *object.BuiltinFunction,
		*object.BuiltinMethod, *object.PrimitiveObject, *object.Proxy:
		return true
	}
	return false
}

// setIndexValue 实现 CreateDataPropertyOrThrow(A, Pk, V) (规范 sec-createdataproperty)。
//
// 与 setIndexValueOrErr 的区别: 该方法吞掉失败 (返回 nil), 仅供"失败也无所谓"
// 的宽松调用点使用。规范里 concat/map/filter/slice/flatMap 等创建结果对象
// 一律用 OrThrow 版本 —— 结果对象不可扩展 (species 构造器 preventExtensions)
// 或索引位是不可配置访问器时必须抛 TypeError (target-array-non-extensible /
// target-array-with-non-configurable-property 族)。
func setIndexValue(a object.Value, i int64, v object.Value) {
	setIndexValueOrErr(a, i, v)
}

// setIndexValueOrErr 实现 CreateDataPropertyOrThrow 并返回失败时的 TypeError。
//
// CreateDataProperty 的定义 (7.3.4):
//  1. Let newDesc be the PropertyDescriptor { [[Value]]: V, [[Writable]]: true,
//     [[Enumerable]]: true, [[Configurable]]: true }.
//  2. Return ? O.[[DefineOwnProperty]](P, newDesc).
//
// 因此它是 **DefineOwnProperty** 语义: 绕过 setter, 且已存在且不可配置的
// 属性无法被重新定义 ⇒ 失败 ⇒ OrThrow 抛 TypeError。新索引还要过
// [[Extensible]]。这里按 *Array / *Object / 其它 三类分别落地。
func setIndexValueOrErr(a object.Value, i int64, v object.Value) object.Value {
	switch target := a.(type) {
	case *object.Array:
		key := arrayIndexKey(i)
		if !target.IsExtensible() && int64(len(target.Elements)) <= i {
			return object.NewTypeError("Cannot define property %s, object is not extensible", key)
		}
		// 已显式定义过的索引: 不可配置 ⇒ DefineOwnProperty 失败。
		if d, ok := target.OwnDescriptor(key); ok && !d.Configurable {
			return object.NewTypeError("Cannot redefine property: %s", key)
		}
		for int64(len(target.Elements)) <= i {
			target.Elements = append(target.Elements, nil)
		}
		target.Elements[i] = v
		return nil
	case *object.Object:
		key := arrayIndexKey(i)
		// 已存在且不可配置的数据属性: 其值不同或不可写 ⇒ 无法按 CreateDataProperty
		// 重定义。规范里 CreateDataPropertyOrThrow 只要求"定义成功", 因此
		// 值相同且可写时仍应成功 (非扩展对象上的既有属性不受影响)。
		if d, ok := target.Properties[key]; ok {
			if !d.Configurable {
				if _, isAcc := d.Value.(*object.Accessor); isAcc {
					return object.NewTypeError("Cannot redefine property: %s", key)
				}
				if !d.Writable || !strictEqualValues(d.Value, v) {
					return object.NewTypeError("Cannot redefine property: %s", key)
				}
			}
		} else if !target.IsExtensible() {
			return object.NewTypeError("Cannot add property %s, object is not extensible", key)
		}
		target.DefineOwnProperty(key, object.DataProperty(v))
		return nil
	}
	// 其它宿主对象 / Proxy: 走普通 Set 通道, 无法精确判定失败, 保持既有行为。
	a.SetProperty(arrayIndexKey(i), v)
	return nil
}

// arraySpeciesCreate 实现规范的 ArraySpeciesCreate(O, length)。
// O 非数组时直接默认建数组; 否则读 O.constructor 与 @@species 决定构造器:
//   - constructor 为 undefined / @@species 为 null 或 undefined → 默认数组
//   - constructor 不是对象 / @@species 不可构造 → TypeError
//   - constructor / @@species 的 getter 抛错 → 原样传播
//
// 返回 (结果对象, 错误值); 错误非 nil 时调用方应直接返回它。
func arraySpeciesCreate(l *arrayLike, length int64) (object.Value, object.Value) {
	mk := func() object.Value {
		return object.NewArray(make([]object.Value, length))
	}
	// 规范的 IsArray(O): Proxy 透过 target 判定 —— 仅凭 l.arr != nil
	// 会把 "Proxy 包数组" 的接收者误判为非数组, 跳过 species 查找
	// (create-proxy / is-concat-spreadable-proxy 族)。
	if !isArrayValue(l.obj) {
		return mk(), nil
	}
	c, _ := l.obj.GetProperty("constructor")
	if t := callbackThrown(); t != nil {
		return nil, t
	}
	// 规范 ArraySpeciesCreate 6~9 步:
	//   6. If IsConstructor(C) is true: (跨 realm 的 %Array% 视为 undefined)
	//   7. If Type(C) is Object: C = Get(C, @@species); null → undefined
	//   8. If C is undefined, return ArrayCreate(length)
	//   9. If IsConstructor(C) is false, throw TypeError
	// 注意 7 的判据是 "是对象" (create-species 族用 {}[@@species]=Ctor),
	// 而 9 才要求构造器 —— 故 constructor 为原始值 (1/'s'/true) 时必然
	// 在 9 抛 TypeError (create-ctor-non-object 族)。
	if !isUndefinedValue(c) && !isNullValue(c) && !isObjectLike(c) {
		return nil, object.NewTypeError("%s is not a constructor", toStr(c))
	}
	if isObjectLike(c) {
		sVal := object.Value(object.UndefinedSingleton)
		if desc, found := object.LookupSymbolPropertyDescriptorChain(c, object.GetGlobalSymbol("Symbol.species")); found {
			if acc, isAcc := desc.Value.(*object.Accessor); isAcc {
				if acc.Getter != nil && object.IsCallable(acc.Getter) {
					sVal = object.CallFunction(acc.Getter, c)
					if t := callbackThrown(); t != nil {
						return nil, t
					}
				}
			} else if desc.Value != nil {
				sVal = desc.Value
			}
		}
		// 规范 7.b: Type(C) 是对象且 @@species 取到 null 时, C 归一为
		// undefined → 第 8 步直接 ArrayCreate (create-species-null 族)。
		// 注意这与 "constructor 本身就是 null" 不同 —— 后者不走 7, 而是
		// 落到第 9 步的 IsConstructor(null)=false 抛 TypeError
		// (create-ctor-non-object 的 null 用例)。
		if sVal == nil || isNullValue(sVal) {
			return mk(), nil
		}
		c = sVal
	}
	// 步骤 8/9: C 为 undefined → 默认数组; 否则 (含 constructor===null) 必须
	// 是构造器, 否则抛 TypeError。
	if c == nil || isUndefinedValue(c) {
		return mk(), nil
	}
	if !isConstructorValue(c) {
		return nil, object.NewTypeError("%s is not a constructor", toStr(c))
	}
	sVal := c
	r := object.ConstructFunction(sVal, object.NewInt(length))
	if t := callbackThrown(); t != nil {
		return nil, t
	}
	if r == nil || !isObjectLike(r) {
		return nil, object.NewTypeError("species constructor did not return an object")
	}
	return r, nil
}

// specSort 是 Array.prototype.sort 的规范路径, 对真实数组与类数组对象
// 一视同仁: 全程经 Get/Set/Delete 访问属性 (支持访问器), 且按规范处理
// 洞与 undefined。
//
// 规范语义 (sec-array.prototype.sort / SortCompare):
//   - 先把 undefined 元素与"洞"一同排到最后, 再做稳定排序;
//   - 排序用 SortCompare: 有 comparefn 时直接调用 (返回 <0 视为前);
//     无 comparefn 时把两侧转字符串按码元序比较, undefined 恒排后。
//
// 之前对真实数组直接 sort.SliceStable(Elements, ...): 绕过访问器、把 nil
// 洞当成 undefined、且自定义比较器下不把 undefined 挪到末尾 ——
// precise-* / S15.4.4.11_* 族即因此失败。
func specSort(l *arrayLike, compareFn object.Value) object.Value {
	n := int(l.n)
	// 步骤 1: 读外部元素 (索引 < n 且存在者), 洞与 undefined 记下不参与排序。
	vals := make([]object.Value, 0, n)
	undefIdx := make([]int, 0)
	holeIdx := make([]int, 0)
	for i := 0; i < n; i++ {
		if !l.has(int64(i)) {
			holeIdx = append(holeIdx, i)
		} else {
			v := l.get(int64(i))
			if l.err != nil {
				return l.err
			}
			if isUndefinedValue(v) {
				undefIdx = append(undefIdx, i)
			} else {
				vals = append(vals, v)
			}
		}
	}

	var sortThrown object.Value
	less := func(a, b object.Value) bool {
		if sortThrown != nil {
			return false
		}
		var r float64
		if compareFn != nil {
			rv := object.CallFunction(compareFn, object.UndefinedSingleton, a, b)
			if thrown := callbackThrown(); thrown != nil {
				sortThrown = thrown
				return false
			}
			r = toFloat(rv)
			if math.IsNaN(r) {
				r = 0
			}
		} else {
			as := sortKeyString(a)
			bs := sortKeyString(b)
			switch {
			case as < bs:
				r = -1
			case as > bs:
				r = 1
			}
		}
		return r < 0
	}
	sort.SliceStable(vals, func(i, j int) bool { return less(vals[i], vals[j]) })
	if sortThrown != nil {
		return sortThrown
	}

	// 步骤 2: 回写 —— 排序值在前, 随后 undefined, 最后洞。
	idx := 0
	for _, v := range vals {
		l.set(int64(idx), v)
		idx++
	}
	for range undefIdx {
		l.set(int64(idx), object.UndefinedSingleton)
		idx++
	}
	if o, ok := l.obj.(*object.Object); ok {
		for _, i := range holeIdx {
			o.DeleteProperty(arrayIndexKey(int64(i)))
		}
	}
	return l.obj
}

// genericSort 是 sort 作用于非数组类对象时的规范路径:
// 读出 → 排序 → 经 Set 写回（洞集中到末尾并按规范删除多余索引）。
func genericSort(l *arrayLike, args []object.Value) object.Value {
	var compareFn object.Value
	if len(args) > 0 && !isUndefinedValue(args[0]) {
		if !object.IsCallable(args[0]) {
			return object.NewTypeError("%s is not a function", toStr(args[0]))
		}
		compareFn = args[0]
	}
	// 收集存在元素的值（洞不参与排序，规范把洞移到末尾并删除）
	vals := make([]object.Value, 0, l.n)
	for i := int64(0); i < l.n; i++ {
		if l.has(i) {
			vals = append(vals, l.get(i))
		}
		if l.err != nil {
			return l.err
		}
	}
	var sortThrown object.Value
	less := func(a, b object.Value) bool {
		if sortThrown != nil {
			return false
		}
		if compareFn != nil {
			if isUndefinedValue(a) || isUndefinedValue(b) {
				return !isUndefinedValue(a) && isUndefinedValue(b)
			}
			r := object.CallFunction(compareFn, object.UndefinedSingleton, a, b)
			if thrown := callbackThrown(); thrown != nil {
				sortThrown = thrown
				return false
			}
			return toFloat(r) < 0
		}
		return defaultSortLess(a, b)
	}
	sort.SliceStable(vals, func(i, j int) bool { return less(vals[i], vals[j]) })
	if sortThrown != nil {
		return sortThrown
	}
	// undefined 排到存在元素之后
	idx := int64(0)
	for _, v := range vals {
		if isUndefinedValue(v) {
			continue
		}
		l.set(idx, v)
		idx++
	}
	undefCount := int64(0)
	for _, v := range vals {
		if isUndefinedValue(v) {
			undefCount++
		}
	}
	for i := int64(0); i < undefCount; i++ {
		l.set(idx+i, object.UndefinedSingleton)
	}
	idx += undefCount
	if o, ok := l.obj.(*object.Object); ok {
		for i := idx; i < l.n; i++ {
			o.DeleteProperty(arrayIndexKey(i))
		}
	}
	return l.obj
}

// genericSplice 是 splice 作用于非数组类对象时的规范路径:
// 全部经 Get/Set/Delete 属性操作，支持 {length:n, 0:..., 1:...} 类数组。
func genericSplice(l *arrayLike, args []object.Value) object.Value {
	length := l.n
	if len(args) == 0 {
		l.setLen(length)
		return object.NewArray([]object.Value{})
	}
	start := clampIndex(toInt(args[0]), int(length))
	var deleteCount int
	var items []object.Value
	if len(args) == 1 {
		deleteCount = int(length) - start
	} else {
		deleteCount = int(toInt(args[1]))
		if deleteCount < 0 {
			deleteCount = 0
		}
		if deleteCount > int(length)-start {
			deleteCount = int(length) - start
		}
		items = args[2:]
	}
	deletedArr, spErr := arraySpeciesCreate(l, int64(deleteCount))
	if spErr != nil {
		return spErr
	}
	for i := 0; i < deleteCount; i++ {
		if l.has(int64(start + i)) {
			if derr := setIndexValueOrErr(deletedArr, int64(i), l.get(int64(start+i))); derr != nil {
				return derr
			}
		}
		if l.err != nil {
			return l.err
		}
	}
	del := func(i int64) {
		if o, ok := l.obj.(*object.Object); ok {
			o.DeleteProperty(arrayIndexKey(i))
		}
	}
	insertCount := len(items)
	if length-int64(deleteCount)+int64(insertCount) > maxArrayLikeLen {
		return object.NewRangeError("Invalid array length")
	}
	if insertCount < deleteCount {
		for k := start; k < int(length)-deleteCount; k++ {
			from, to := int64(k+deleteCount), int64(k+insertCount)
			if l.has(from) {
				l.set(to, l.get(from))
			} else {
				del(to)
			}
		}
		for k := int64(length); k > int64(length)-int64(deleteCount-insertCount); k-- {
			del(k - 1)
		}
	} else if insertCount > deleteCount {
		for k := int64(length) - int64(deleteCount); k > int64(start); k-- {
			from, to := k+int64(deleteCount)-1, k+int64(insertCount)-1
			if l.has(from) {
				l.set(to, l.get(from))
			} else {
				del(to)
			}
		}
	}
	for i, it := range items {
		l.set(int64(start+i), it)
	}
	l.setLen(length - int64(deleteCount) + int64(insertCount))
	return deletedArr
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

// isObjectValue 判断值是否为对象（需要走 ToPrimitive 才能转数的那类）。
func isObjectValue(v object.Value) bool {
	switch v.(type) {
	case *object.Object, *object.Array, *object.Date:
		return true
	}
	return false
}

// specIndex implements the spec's ToIntegerOrInfinity at array algorithm
// positions (start / end / target / count). Unlike the loose toInt helper it
// is abrupt on Symbol and on objects whose valueOf/toString throws —
// `[].copyWithin(0, Symbol())` must throw a TypeError rather than silently
// treating the Symbol as 0 (test262 *-as-symbol / tointeger-invalid 族)。
// Returns (0, non-nil thrown) when the caller should return that value.
func specIndex(v object.Value) (int64, object.Value) {
	if v == nil {
		return 0, object.NewTypeError("Cannot convert undefined to a number")
	}
	if _, isSym := v.(*object.Symbol); isSym {
		return 0, object.NewTypeError("Cannot convert a Symbol value to a number")
	}
	return toIntegerOrInfinity(v)
}

// specIndexClamp 对 specIndex 的结果做 clampIndex, 顺带把异常短路掉。
// 调用方拿到 errVal 非 nil 时应直接返回它。
func specIndexClamp(v object.Value, n int) (int, object.Value) {
	i, errVal := specIndex(v)
	if errVal != nil {
		return 0, errVal
	}
	return clampIndex(i, n), nil
}

// toNumberViaPrimitive 对对象做规范的 ToPrimitive(number hint) → ToNumber:
// 先 valueOf 后 toString，取第一个返回原语的结果；用户代码抛错则原样传播。
func toNumberViaPrimitive(v object.Value) (float64, object.Value) {
	for _, m := range []string{"valueOf", "toString"} {
		fn, ok := v.GetProperty(m)
		if !ok || !object.IsCallable(fn) {
			continue
		}
		r := object.CallFunction(fn, v)
		if t := callbackThrown(); t != nil {
			return 0, t
		}
		if r != nil && !isObjectValue(r) {
			if _, isSym := r.(*object.Symbol); isSym {
				continue
			}
			return toFloat(r), nil
		}
	}
	return 0, object.NewTypeError("Cannot convert object to primitive value")
}

// toLength 实现 ECMAScript 的 ToLength: 转数后钳位到 [0, 2^53-1]。
func toLength(v object.Value) int64 {
	n := toFloat(v)
	if math.IsNaN(n) || n <= 0 {
		return 0
	}
	if n >= 9007199254740991 {
		return 9007199254740991
	}
	return int64(n)
}

// arrayLike 是 Array.prototype 泛型算法的接收者视图。
// 规范里这些方法先 ToObject(this)，再按 length/索引做属性读写，
// 因此对 {length:2, 0:'a', 1:'b'} 这类类数组对象同样适用。
// arr 为非 nil 时走 Elements 快速路径（保持对真实数组的原有行为与性能）。
type arrayLike struct {
	obj object.Value
	arr *object.Array
	n   int64
	// err 记录属性读取过程中 getter 抛出的第一个异常（经回调桥捕获）。
	// 规范要求迭代立即中止并把该异常抛给调用方。
	err object.Value
}

// asArrayLike 做规范的 ToObject(this) + LengthOfArrayLike。
// this 为 null/undefined 时返回 TypeError（泛型算法的第 1 步）。
func asArrayLike(method string, this object.Value) (*arrayLike, object.Value) {
	if this == nil || isUndefinedValue(this) || isNullValue(this) {
		return nil, thisTypeError("Array", method, this)
	}
	if arr, ok := this.(*object.Array); ok {
		return &arrayLike{obj: arr, arr: arr, n: int64(len(arr.Elements))}, nil
	}
	// 规范的 ToObject(this): 原始值 (boolean/number/string/symbol) 要包成
	// 对应包装对象再读写属性。漏掉这一步会让回调的第三个参数 obj 拿到原始
	// 值 (规范要求 obj === ToObject(this)), 且原型上的 length/索引读不到 ——
	// test262 的 15.4.4.*/S15.4.4.* "applied to primitive" 族即因此整族失败。
	this = toObjectValue(this)
	var n int64
	if lv, ok := this.GetProperty("length"); ok && lv != nil {
		// ToLength(Symbol) 按规范抛 TypeError (LengthOfArrayLike 的 abrupt)
		if _, isSym := lv.(*object.Symbol); isSym {
			return nil, object.NewTypeError("Cannot convert a Symbol value to a number")
		}
		if isObjectValue(lv) {
			f, nerr := toNumberViaPrimitive(lv)
			if nerr != nil {
				return nil, nerr
			}
			n = toLength(object.NewNumber(f))
		} else {
			n = toLength(lv)
		}
	}
	return &arrayLike{obj: this, n: n}, nil
}

// toIntegerOrInfinity 实现规范的 ToIntegerOrInfinity:
// 对象先 ToPrimitive(number hint) (先 valueOf 后 toString), 再 ToNumber,
// 最后 NaN→0、±∞ 保留、其余向零取整。返回 (值, 错误值)。
//
// indexOf/lastIndexOf 等以 fromIndex 为参数的算法必须走这条路径:
// fromIndex 是对象时其 valueOf/toString 必须被调用, 且抛错原样传播。
func toIntegerOrInfinity(v object.Value) (int64, object.Value) {
	f := 0.0
	if isObjectValue(v) {
		var nerr object.Value
		f, nerr = toNumberViaPrimitive(v)
		if nerr != nil {
			return 0, nerr
		}
	} else {
		f = toFloat(v)
	}
	switch {
	case math.IsNaN(f), f == 0:
		return 0, nil
	case math.IsInf(f, 1):
		return math.MaxInt64, nil
	case math.IsInf(f, -1):
		return math.MinInt64, nil
	}
	truncated := math.Trunc(f)
	const int64Limit = 9223372036854775808.0
	if truncated >= int64Limit {
		return math.MaxInt64, nil
	}
	if truncated <= -int64Limit {
		return math.MinInt64, nil
	}
	return int64(truncated), nil
}

// toObjectValue 实现规范的 ToObject (sec-object-type):
// undefined/null 抛 TypeError (调用方自行保证其已排除), 其余原始值包成对应
// 的包装对象, 已是对象则原样返回。Array.prototype 的泛型算法第 1 步即此。
func toObjectValue(v object.Value) object.Value {
	switch p := v.(type) {
	case *object.Boolean:
		return object.NewPrimitiveObject(p, object.GetBooleanProto())
	case *object.Number:
		return object.NewPrimitiveObject(p, object.GetNumberProto())
	case *object.String:
		return object.NewPrimitiveObject(p, object.GetStringProto())
	case *object.Symbol:
		return object.NewPrimitiveObject(p, object.GetSymbolProto())
	}
	return v
}

// has 实现 HasProperty(O, Pk)（含原型链）；数组的 nil 元素是洞。
func (l *arrayLike) has(i int64) bool {
	if l.arr != nil {
		return l.arr.Has(arrayIndexKey(i))
	}
	v, ok := l.obj.GetProperty(arrayIndexKey(i))
	if t := callbackThrown(); t != nil && l.err == nil {
		l.err = t
	}
	return ok && v != nil
}

// get 实现 Get(O, Pk)，缺失时返回 undefined。
// getter 抛出的异常记入 l.err（首个），调用方须在每次 get 后检查并中止。
func (l *arrayLike) get(i int64) object.Value {
	v, ok := l.obj.GetProperty(arrayIndexKey(i))
	if t := callbackThrown(); t != nil && l.err == nil {
		l.err = t
	}
	if ok && v != nil {
		return v
	}
	return object.UndefinedSingleton
}

// set 实现 Set(O, Pk, V)。
func (l *arrayLike) set(i int64, v object.Value) {
	if l.arr != nil {
		for int64(len(l.arr.Elements)) <= i {
			l.arr.Elements = append(l.arr.Elements, nil)
		}
		l.arr.Elements[i] = v
		return
	}
	l.obj.SetProperty(arrayIndexKey(i), v)
}

// setLen 设置 length 属性（pop/shift 等需要把新长度写回对象）。
func (l *arrayLike) setLen(n int64) {
	l.setLenOrThrow(n)
}

// lengthWritable 实现规范对 length 可写性的检查:
//   - 真实数组: length 的自有描述符恒为可写 (但 Object.freeze 会把索引/
//     length 一并锁死 —— 冻结后的数组 [[Set]] length 必须失败);
//   - 其它对象: 读 length 自有描述符的 Writable。
//
// 用于 pop/shift/push/unshift 的第二步 "Set(O, "length", ..., true)"。
func (l *arrayLike) lengthWritable() bool {
	if d, ok := l.ownDescriptor("length"); ok {
		return d.Writable
	}
	return true
}

// ownDescriptor 查自有属性描述符 (区分 *Array 与其它对象的存储)。
func (l *arrayLike) ownDescriptor(name string) (object.PropertyDescriptor, bool) {
	if store, ok := l.obj.(object.OwnPropertyStore); ok {
		return store.OwnDescriptor(name)
	}
	return object.PropertyDescriptor{}, false
}

// setLenOrThrow 实现规范的 Set(O, "length", newLen, true)。
// 失败时返回 TypeError: 数组 length 不可写 (freeze / defineProperty) 时
// push/pop/shift/unshift 必须抛出 (set-length-* 族)。
//
// 必须先**真的发起写入**再判定 —— 不能只看 lengthWritable() 短路: 接收者
// 可能是带 length setter 的普通对象, 那种情况下 Set 会调用 setter 并成功
// (splice/set_length_no_args 要求 setCallCount === 1)。
func (l *arrayLike) setLenOrThrow(n int64) object.Value {
	if l.arr != nil {
		// 真实数组: length 的自有描述符决定成败, 无需触发任何访问器。
		if !l.lengthWritable() {
			return object.NewTypeError("Cannot assign to read only property 'length' of object")
		}
		if n < int64(len(l.arr.Elements)) {
			l.arr.Elements = l.arr.Elements[:n]
		} else {
			for int64(len(l.arr.Elements)) < n {
				l.arr.Elements = append(l.arr.Elements, nil)
			}
		}
		l.n = n
		return nil
	}
	l.obj.SetProperty("length", object.NewInt(n))
	if t := callbackThrown(); t != nil {
		return t
	}
	l.n = n
	return nil
}

// setOrThrow 实现规范的 Set(O, Pk, V, true)。
//
// 与宽松的 set 不同, 它按 Throw=true 语义检查失败: 已有且不可写的数据属性、
// 已有且不可配置的访问器 (无 setter)、以及不可扩展对象上的新属性都要抛
// TypeError。pop/shift/push/unshift 这些必须把索引写入的失败暴露出来
// (例如 getter 把 length 改成只读后, 后续 Set 必须抛)。
//
// 注意实际失败判据来自 SetProperty 之后的状态检查: Gox 的 SetProperty 是
// 静默失败式 (非严格模式语义), 这里用"写入后是否真的落到目标"来判定。
func (l *arrayLike) setOrThrow(i int64, v object.Value) object.Value {
	key := arrayIndexKey(i)
	// 已有显式描述符: 不可写数据属性 / 无 setter 的访问器 ⇒ Set 失败。
	if d, ok := l.ownDescriptor(key); ok {
		if acc, isAcc := d.Value.(*object.Accessor); isAcc {
			if acc.Setter == nil || !object.IsCallable(acc.Setter) {
				return object.NewTypeError("Cannot set property %s of object which has only a getter", key)
			}
		} else if !d.Writable {
			return object.NewTypeError("Cannot assign to read only property %s of object", key)
		}
	} else if !object.IsExtensible(l.obj) {
		// 数组的"新索引"受 [[Extensible]] 约束; 但数组索引的扩展性由
		// Array.SetProperty 判定, 这里只在明确不可扩展且越界时报错。
		if l.arr == nil || i >= int64(len(l.arr.Elements)) {
			return object.NewTypeError("Cannot add property %s, object is not extensible", key)
		}
	}
	l.set(i, v)
	if t := callbackThrown(); t != nil {
		return t
	}
	return nil
}

// deleteOrThrow 实现规范的 DeletePropertyOrThrow(O, Pk): 删除失败 (自有
// 不可配置属性) 时抛 TypeError, 否则忽略结果。shift/unshift 在搬移元素后
// 要删掉多余索引, 不可配置时必须报错。
func (l *arrayLike) deleteOrThrow(i int64) object.Value {
	key := arrayIndexKey(i)
	if d, ok := l.ownDescriptor(key); ok && !d.Configurable {
		return object.NewTypeError("Cannot delete property %s of object", key)
	}
	if deleter, ok := l.obj.(object.PropDeleter); ok {
		deleter.DeleteOwn(key)
	} else if o, ok := l.obj.(*object.Object); ok {
		o.DeleteProperty(key)
	}
	return nil
}

// maxArrayLikeLen 是 ToLength 的上界 2^53-1。
const maxArrayLikeLen = 9007199254740991

// checkMutable 拦截会写索引/length 的方法作用于 String 接收者的情形:
// 字符串 (以及 String 包装对象) 的索引与 length 均不可写, 规范的
// Set(..., Throw=true) 必抛 TypeError。
//
// 只拦"String 本质"的接收者 —— 不能用 "length 描述符不可写" 作为判据:
// 那样 copyWithin/fill 在 {length:{valueOf(){throw}}} 这类对象上会先抛
// 自己的 TypeError 而不是让 length 的 abrupt 先冒出来
// (return-abrupt-from-this-length 族), reverse/shift 在冻结数组上也会被
// 误伤 (Object.freeze 后 length 不可写, 但 reverse 是合法的)。
func (l *arrayLike) checkMutable() object.Value {
	switch l.obj.(type) {
	case *object.String, *object.PrimitiveObject:
		if _, isStr := object.UnwrapPrimitive(l.obj).(*object.String); isStr {
			return object.NewTypeError("Cannot assign to read only property of object")
		}
	}
	return nil
}

// arrayIndexKey 把索引转成属性名。
func arrayIndexKey(i int64) string {
	return strconv.FormatInt(i, 10)
}

// arrayCallbackArgs 解析数组高阶方法的公共参数 (callback, thisArg)。
//
// 返回值的第 4 项是错误对象: 非 nil 时调用方应直接返回它，由 VM 转为异常。
// this 按规范做 ToObject（null/undefined 抛 TypeError），类数组对象也可使用。
func arrayCallbackArgs(method string, this object.Value, args []object.Value) (*arrayLike, object.Value, object.Value, object.Value) {
	l, errVal := asArrayLike(method, this)
	if errVal != nil {
		return nil, nil, nil, errVal
	}
	if len(args) < 1 || !object.IsCallable(args[0]) {
		return nil, nil, nil, object.NewTypeError("%s is not a function", argInspect(args, 0))
	}
	var thisArg object.Value
	if len(args) > 1 {
		thisArg = args[1]
	}
	return l, args[0], thisArg, nil
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
		// 用户抛出的非 Error 值 (Test262Error 实例、throw 42 等):
		// 包一层 Error 让 VM 识别"这是异常", 原始值经 Original 字段透传。
		if !isUndefinedValue(v) {
			w := object.NewErrorWithName("Error", cbErr.Error())
			w.Original = v
			return w
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
	// 其余类型 (Object/Array/Symbol/BigInt/函数等) 按引用同一性比较
	return a == b
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
		// Array(len): 单参数且为整数时，返回指定长度的稀疏数组。
		// Elements 的 nil 即洞 (规范语义: (0 in Array(3)) === false)。
		if len(args) == 1 {
			if n, ok := args[0].(*object.Number); ok {
				l := n.Value
				if math.IsNaN(l) || l != math.Trunc(l) || l < 0 || l > 4294967295 {
					return object.NewRangeError("Invalid array length")
				}
				return object.NewArray(make([]object.Value, int(l)))
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
		// 规范 IsArray: Proxy 递归看 target, revoked proxy 抛 TypeError。
		ok, errVal := isArrayValueOrThrow(args[0])
		if errVal != nil {
			return errVal
		}
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
			// array-like 或可迭代对象: 规范 Array.from 先看 @@iterator, 没有
			// 再按 length + 索引属性逐项读。旧实现只认 Array/String/Set/Map,
			// 于是 {length:n} 这类 array-like 被静默当成空数组 —— test262
			// harness 的 makeArray 正是 `Array.from({length:n}, fn)`。
			if next, ok := object.Iterate(args[0]); ok {
				for i := 0; ; i++ {
					v, done := next()
					if done {
						break
					}
					elements = append(elements, v)
					if i > 1<<24 {
						break
					}
				}
			} else if lv, ok := args[0].GetProperty("length"); ok && lv != nil {
				lf, lerr := toNumberViaPrimitive(lv)
				if lerr != nil {
					return lerr
				}
				n := int(lf)
				for i := 0; i < n; i++ {
					v, _ := args[0].GetProperty(strconv.Itoa(i))
					if v == nil {
						v = object.UndefinedSingleton
					}
					elements = append(elements, v)
				}
			} else {
				return object.NewArray([]object.Value{})
			}
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
