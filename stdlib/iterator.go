package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupIteratorGlobal 注册 ES2025 Iterator 全局对象。
//
// Iterator 是迭代器 helpers 的命名空间: Iterator.from 把任意可迭代值
// 转换为增强迭代器 (支持 map/filter/take/...)。helper 方法的实际实现
// 在 object.JSIterator.esIteratorHelper 中，这里只提供静态方法。
//
// Iterator.prototype 上同样需要 helper 方法: for-of 产生的裸迭代器
// (object.JSIterator) 通过 GetProperty 直接响应这些方法名，因此
// prototype 对象本身保持空壳即可满足 typeof 检查。
func setupIteratorGlobal() *object.BuiltinFunction {
	fn := object.NewBuiltin("Iterator", func(args ...object.Value) object.Value {
		// 规范: Iterator 作为构造器调用时抛错 (抽象类)
		return object.NewTypeError("Iterator is not a constructor")
	})

	// Iterator.from(iterableOrIterator): 返回增强迭代器
	fn.SetProperty("from", object.NewBuiltin("from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("Iterator.from requires an argument")
		}
		switch v := args[0].(type) {
		case *object.JSIterator:
			return v // 已是增强迭代器
		case *object.Array:
			return object.NewArrayIterator(v)
		case *object.String:
			return object.NewStringCodePointIterator(v)
		case *object.Map:
			return object.NewMapEntryIterator(v)
		case *object.Set:
			return object.NewSetIterator(v)
		case *object.Object:
			// 类迭代器: 有 next 方法即视为迭代器
			if nv, ok := v.GetProperty("next"); ok && object.IsCallable(nv) {
				return object.NewNextOnlyIterator(v, nv)
			}
			// [Symbol.iterator] 方法
			next, ok := object.Iterate(v)
			if !ok {
				return object.NewTypeError("Iterator.from: %s is not iterable", toStr(v))
			}
			return &object.JSIterator{NextFn: next}
		}
		return object.NewTypeError("Iterator.from: %s is not iterable", toStr(args[0]))
	}))

	// ES2026 Iterator.zip(iterables, {mode}) — 默认成组；mode:"longest" 补齐
	fn.SetProperty("zip", object.NewBuiltin("zip", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("Iterator.zip requires an iterable of iterables")
		}
		nexts, ok := collectIterables(args[0])
		if !ok {
			return object.NewTypeError("Iterator.zip: argument is not iterable")
		}
		mode := "short"
		if len(args) > 1 {
			if o, isObj := args[1].(*object.Object); isObj {
				if mv, found := o.GetProperty("mode"); found {
					mode = toStr(mv)
				}
			}
		}
		return &object.JSIterator{NextFn: func() (object.Value, bool) {
			group := make([]object.Value, len(nexts))
			anyDone := false
			allDone := true
			for i, next := range nexts {
				v, done := next()
				if done {
					anyDone = true
					group[i] = object.UndefinedSingleton
				} else {
					allDone = false
					group[i] = v
				}
			}
			if anyDone && mode == "short" {
				return object.UndefinedSingleton, true
			}
			if allDone {
				return object.UndefinedSingleton, true
			}
			return object.NewArray(group), false
		}}
	}))

	// ES2026 Iterator.concat(...iterables) — 顺序拼接
	fn.SetProperty("concat", object.NewBuiltin("concat", func(args ...object.Value) object.Value {
		nexts, ok := collectIterables(object.NewArray(args))
		if !ok {
			return object.NewTypeError("Iterator.concat: arguments must be iterables")
		}
		idx := 0
		return &object.JSIterator{NextFn: func() (object.Value, bool) {
			for {
				if idx >= len(nexts) {
					return object.UndefinedSingleton, true
				}
				if v, done := nexts[idx](); !done {
					return v, false
				}
				idx++
			}
		}}
	}))

	proto := object.NewObject()
	fn.SetProperty("prototype", proto)
	proto.SetProperty("constructor", fn)

	// Iterator.prototype 上的 ES2025 helper 方法 (作用于 this 迭代器)。
	// 实际逻辑在 object.ApplyIteratorHelper / JSIterator.esIteratorHelper。
	for _, name := range []string{"map", "filter", "take", "drop", "flatMap",
		"reduce", "toArray", "forEach", "some", "every", "find"} {
		helperName := name
		proto.SetProperty(helperName, object.NewBuiltinMethod(helperName, func(this object.Value, args ...object.Value) object.Value {
			if v, ok := object.ApplyIteratorHelper(this, helperName, args); ok {
				return v
			}
			return object.NewTypeError("Iterator.prototype.%s: receiver must be an Iterator", helperName)
		}))
	}
	return fn
}

// collectIterables 把一组可迭代值转换为 next 闭包列表。
func collectIterables(v object.Value) ([]func() (object.Value, bool), bool) {
	next, ok := object.Iterate(v)
	if !ok {
		return nil, false
	}
	var out []func() (object.Value, bool)
	for {
		item, done := next()
		if done {
			return out, true
		}
		itemNext, ok := object.Iterate(item)
		if !ok {
			return nil, false
		}
		out = append(out, itemNext)
	}
}

// setupWeakRefGlobals 注册 WeakRef 与 FinalizationRegistry。
//
// 运行时对象由 Go GC 管理，解释器持有强引用链，因此无法观测"对象被
// 回收"。这里的实现保持 API 兼容: WeakRef.deref() 在目标存在时返回它；
// FinalizationRegistry 的清理回调不会自动触发 (cleanupSome 可手动触发)。
func setupWeakRefGlobals(env *runtime.Environment) {
	weakRefFn := object.NewBuiltin("WeakRef", func(args ...object.Value) object.Value {
		var target object.Value
		if len(args) > 0 {
			target = args[0]
		}
		if target != nil && !object.IsObjectValue(target) {
			return object.NewTypeError("WeakRef: target must be an object, got %s", toStr(target))
		}
		return object.NewWeakRef(target)
	})
	weakRefFn.SetProperty("prototype", object.NewObject())
	env.Declare("WeakRef", weakRefFn, false)

	regFn := object.NewBuiltin("FinalizationRegistry", func(args ...object.Value) object.Value {
		var cleanup object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			cleanup = args[0]
		}
		if cleanup != object.UndefinedSingleton && !object.IsCallable(cleanup) {
			return object.NewTypeError("FinalizationRegistry: cleanup callback must be a function")
		}
		return object.NewFinalizationRegistry(cleanup)
	})
	regFn.SetProperty("prototype", object.NewObject())
	env.Declare("FinalizationRegistry", regFn, false)
}

// setupGlobalThis 注册 globalThis —— 由全局环境背书的对象。
// 读写 globalThis.x 即读写全局绑定。
func setupGlobalThis(env *runtime.Environment) {
	env.Declare("globalThis", object.NewGlobalObject(env), false)
}
