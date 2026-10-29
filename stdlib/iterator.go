package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupIteratorGlobal 注册 ES2025/ES2026 Iterator 全局对象。
//
// 规范要点 (rFXXbq 批次对齐):
//   - Iterator 是抽象构造器: 普通调用抛 TypeError; new Iterator() /
//     class X extends Iterator 的 super() 合法, 得到带 Iterator.prototype
//     原型、自有 next: undefined 的普通对象 (RequiresNew + 构造窗口判定);
//   - Iterator.prototype 挂全部 helper 方法 (规范语义工厂在
//     object.IteratorHelperMethod), 外加 @@iterator / @@toStringTag /
//     @@dispose;
//   - Iterator.from 包装任意 (类) 迭代器, return() 转发到底层。
func setupIteratorGlobal() *object.BuiltinFunction {
	var fn *object.BuiltinFunction
	fn = object.NewBuiltin("Iterator", func(args ...object.Value) object.Value {
		// 27.5.1 Iterator ( ): "If NewTarget is undefined or the active
		// function object, throw a TypeError" —— 直接调用与直接
		// new Iterator() 都非法, 只有子类 super() / Reflect.construct
		// (自定义 newTarget) 合法。OP_NEW / invokeWithThis 在构造窗口
		// 内置 pendingConstructTarget 供判定。
		if !object.IsConstructCall() {
			return object.NewTypeError("Iterator is not a function")
		}
		if nt := object.PendingConstructTarget(); nt == nil || nt == object.Value(fn) {
			return object.NewTypeError("Iterator: abstract class, new.target must be a subclass")
		}
		// OrdinaryCreateFromConstructor(NewTarget, "%Iterator.prototype%"):
		// 默认原型取 Iterator.prototype; extends Iterator 的 super() 路径
		// 由 vm (invokeWithThis) 按派生类 prototype 覆盖实例原型。
		o := object.NewObject()
		if p := object.GetIteratorProto(); p != nil {
			o.Proto = p
		}
		return o
	})
	fn.RequiresNew = true

	// Iterator.from(iterableOrIterator): 返回包装迭代器。
	fromFn := object.NewBuiltin("from", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("Iterator.from requires an argument")
		}
		o := args[0]
		if _, isJI := o.(*object.JSIterator); isJI {
			return o // 已是增强迭代器
		}
		// 规范: 有 @@iterator 方法 → GetIterator; 否则要求可调用 next。
		// Iterate 覆盖 Array/String/Map/Set/用户对象 (含符号键协议)。
		if next, ok := object.Iterate(o); ok {
			var it *object.JSIterator
			it = &object.JSIterator{Name: "Iterator", NextFn: func() (object.Value, bool) {
				v, done := next()
				if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
					it.PendingErr = ev
					return object.UndefinedSingleton, true
				}
				if done {
					it.MarkExhausted()
				}
				if v == nil {
					v = object.UndefinedSingleton
				}
				return v, done
			}}
			// return() 转发到底层 (get-return-method-when-call-return)。
			it.ReturnHook = func() { object.CloseIterator(o) }
			return it
		}
		next, found := o.GetProperty("next")
		if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
			return ev
		}
		if !found || !object.IsCallable(next) {
			return object.NewTypeError("Iterator.from: %s is not an iterator", toStr(args[0]))
		}
		var it *object.JSIterator
		it = &object.JSIterator{Name: "Iterator", NextFn: func() (object.Value, bool) {
			r := object.CallFunction(next, o)
			if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
				it.PendingErr = ev
				return object.UndefinedSingleton, true
			}
			res, ok := r.(*object.Object)
			if !ok {
				it.PendingErr = object.NewTypeError("Iterator.from: next() must return an object")
				return object.UndefinedSingleton, true
			}
			if dv, found := res.GetProperty("done"); found {
				if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
					it.PendingErr = ev
					return object.UndefinedSingleton, true
				}
				if b, isB := dv.(*object.Boolean); isB && b.Value {
					it.MarkExhausted()
					return object.UndefinedSingleton, true
				}
			} else if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
				it.PendingErr = ev
				return object.UndefinedSingleton, true
			}
			if vv, found := res.GetProperty("value"); found {
				if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
					it.PendingErr = ev
					return object.UndefinedSingleton, true
				}
				if vv != nil {
					return vv, false
				}
			} else if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
				it.PendingErr = ev
				return object.UndefinedSingleton, true
			}
			return object.UndefinedSingleton, false
		}}
		// return() 转发到底层 (get-return-method-when-call-return)。
		it.ReturnHook = func() { object.CloseIterator(o) }
		return it
	})
	fromFn.SetFunctionLength(1)
	fromFn.NotConstructable = true // from/non-constructible: Iterator.from 无 [[Construct]]
	fn.SetProperty("from", fromFn)

	// ES2026 Iterator.zip(iterables, {mode}) — 默认成组; mode:"longest" 补齐
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
	proto.SetBuiltinProperty("constructor", fn)
	object.SetIteratorProto(proto)

	// helper 方法: 规范语义工厂 (object.IteratorHelperMethod)。
	for _, name := range object.IteratorHelperNames() {
		proto.SetBuiltinProperty(name, object.IteratorHelperMethod(name))
	}

	// Iterator.prototype[Symbol.iterator]: 返回 this。
	proto.SetBuiltinSymbolProperty(object.GetGlobalSymbol("Symbol.iterator"),
		object.NewBuiltinMethod("[Symbol.iterator]", func(this object.Value, args ...object.Value) object.Value {
			return this
		}))

	// Iterator.prototype[Symbol.toStringTag] getter: 有可调用 next 的
	// 接收者返回 "Iterator", 否则 undefined。
	proto.DefineSymbolAccessor(object.GetGlobalSymbol("Symbol.toStringTag"),
		object.NewBuiltinMethod("get [Symbol.toStringTag]", func(this object.Value, args ...object.Value) object.Value {
			if nv, found := this.GetProperty("next"); found && object.IsCallable(nv) {
				return object.NewString("Iterator")
			}
			return object.UndefinedSingleton
		}), nil)

	// Iterator.prototype[Symbol.dispose] (ES2026 explicit resource mgmt):
	// 调用接收者的 return 方法 (有则调), 返回 undefined。
	proto.SetBuiltinSymbolProperty(object.GetGlobalSymbol("Symbol.dispose"),
		object.NewBuiltinMethod("[Symbol.dispose]", func(this object.Value, args ...object.Value) object.Value {
			if rv, found := this.GetProperty("return"); found && object.IsCallable(rv) {
				object.CallFunction(rv, this)
				if ev := object.TakeCallbackErrorValue(); ev != object.UndefinedSingleton {
					return ev
				}
			}
			return object.UndefinedSingleton
		}))

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
//
// [[Prototype]] 指向 %Object.prototype%: 使 globalThis.hasOwnProperty /
// toString / valueOf 等方法可达 (规范里全局对象的原型链含 Object.prototype)。
func setupGlobalThis(env *runtime.Environment) {
	g := object.NewGlobalObject(env)
	g.Proto = object.GetObjectPrototype()
	env.Declare("globalThis", g, false)
}
