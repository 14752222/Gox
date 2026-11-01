package stdlib

import (
	"github.com/14752222/Gox/object"
)

// ===== ES2026 Joint Iteration: Iterator.zip / Iterator.zipKeyed =====
//
// 规范要点（tc39/proposal-joint-iteration Stage 4, ES2026 27.1.3.2.4/.5 +
// 27.1.3.4.1 IteratorZip）：
//   - iterables 必须是 Object（GetIterator 同步迭代其元素）；元素经
//     GetIteratorFlattenable(reject-primitives)：原始值（含字符串）抛
//     TypeError；无 @@iterator 但有可调用 next 时视为迭代器直通；
//   - options 经 GetOptionsObject；mode ∈ {"shortest"(默认),"longest",
//     "strict"}，非法 TypeError（在任何迭代发生前校验）；
//   - mode="longest" 时取 padding（默认 undefined 填充 iterCount 个）；
//     padding 是可迭代对象，先取 iterCount 个值，多余关闭；
//   - IteratorZip 生成器逐位取值：某路耗尽时 shortest → 关闭其余返回；
//     strict → 校验其余是否同时耗尽否则 TypeError；longest → 该路置
//     null、取 padding[i]，全部耗尽才结束；
//   - 异常/早退路径一律 IteratorCloseAll 尚开的迭代器。

// zipIterRec 是 GetIteratorFlattenable 产出的手工迭代器记录。
type zipIterRec struct {
	obj        object.Value                // 迭代器对象（return() 的接收者）
	nextFn     object.Value                // 可调用的 next
	nativeNext func() (object.Value, bool) // 原生类型的 next 闭包（二选一）
}

// zipIsCallable 与 object.IsCallable 等价，但把 *Proxy 也视为可调用
// （Proxy 的可调用性由 target 决定，回调桥层的 CallFunction 能处理）。
func zipIsCallable(v object.Value) bool {
	if object.IsCallable(v) {
		return true
	}
	_, ok := v.(*object.Proxy)
	return ok
}

// zipGetIterator 实现 GetIterator(obj, sync) —— 顶层 iterables 参数专用:
// 无 @@iterator 直接 TypeError。
func zipGetIterator(v object.Value) (*zipIterRec, object.Value) {
	getProp := func(o object.Value, name string) (object.Value, bool) {
		if g, ok := o.(interface {
			GetProperty(string) (object.Value, bool)
		}); ok {
			return g.GetProperty(name)
		}
		return nil, false
	}
	symIterator := object.NewSymbol("Symbol.iterator").Inspect()
	if t, ok := v.(*object.Object); ok {
		if sym := object.GetGlobalSymbol("Symbol.iterator"); sym != nil {
			if m, found := t.GetSymbolProperty(sym); found && zipIsCallable(m) {
				it := object.CallFunction(m, v)
				if errv := callbackThrown(); errv != nil {
					return nil, errv
				}
				if it == nil {
					return nil, object.NewTypeError("Iterator.zip: iterator method returned null")
				}
				return zipMakeRec(it)
			}
		}
	}
	if m, found := getProp(v, symIterator); found && zipIsCallable(m) {
		it := object.CallFunction(m, v)
		if errv := callbackThrown(); errv != nil {
			return nil, errv
		}
		return zipMakeRec(it)
	}
	// 原生可迭代类型（Array/Set/Map/String/TypedArray 等）：经 object.Iterate
	// 取 next 闭包，包成 JSIterator 以获得可关闭的迭代器对象。
	if next, ok := object.Iterate(v); ok {
		wrapped := &object.JSIterator{Name: "Iterator", NextFn: next}
		return &zipIterRec{obj: wrapped, nativeNext: next}, nil
	}
	return nil, object.NewTypeError("Iterator.zip: value is not iterable")
}

// zipGetIteratorFlattenable 实现 GetIteratorFlattenable(v, reject-primitives)
// —— 元素级: 无 @@iterator 时 v 自身即迭代器（next 缺失则推迟到步进时报错）。
func zipGetIteratorFlattenable(v object.Value) (*zipIterRec, object.Value) {
	if v == nil || !object.IsObjectLike(v) {
		return nil, object.NewTypeError("Iterator.zip: iterables must contain only objects")
	}
	getProp := func(o object.Value, name string) (object.Value, bool) {
		if g, ok := o.(interface {
			GetProperty(string) (object.Value, bool)
		}); ok {
			return g.GetProperty(name)
		}
		return nil, false
	}
	symIterator := object.NewSymbol("Symbol.iterator").Inspect()
	if t, ok := v.(*object.Object); ok {
		if sym := object.GetGlobalSymbol("Symbol.iterator"); sym != nil {
			if m, found := t.GetSymbolProperty(sym); found && zipIsCallable(m) {
				it := object.CallFunction(m, v)
				if errv := callbackThrown(); errv != nil {
					return nil, errv
				}
				if it == nil {
					return nil, object.NewTypeError("Iterator.zip: iterator method returned null")
				}
				return zipMakeRec(it)
			}
		}
	}
	if m, found := getProp(v, symIterator); found && zipIsCallable(m) {
		it := object.CallFunction(m, v)
		if errv := callbackThrown(); errv != nil {
			return nil, errv
		}
		return zipMakeRec(it)
	}
	// 无 @@iterator：自身是迭代器（有可调用 next）则直通。
	if m, found := getProp(v, "next"); found && zipIsCallable(m) {
		return &zipIterRec{obj: v, nextFn: m}, nil
	}
	// 原生可迭代类型（Array/Set/Map/String/TypedArray 等）：经 object.Iterate
	// 取 next 闭包，包成 JSIterator 以获得可关闭的迭代器对象。
	if next, ok := object.Iterate(v); ok {
		wrapped := &object.JSIterator{Name: "Iterator", NextFn: next}
		return &zipIterRec{obj: wrapped, nativeNext: next}, nil
	}
	// 既无 @@iterator 也无 next: 规范仍令 iterator = v，缺失的 next
	// 推迟到 IteratorStepValue 时才抛 TypeError。
	return &zipIterRec{obj: v}, nil
}

func zipMakeRec(it object.Value) (*zipIterRec, object.Value) {
	if !object.IsObjectLike(it) {
		return nil, object.NewTypeError("Iterator.zip: iterator method returned a non-object")
	}
	nextFn, found := it.GetProperty("next")
	if !found || !object.IsCallable(nextFn) {
		return nil, object.NewTypeError("Iterator.zip: iterator has no callable next")
	}
	return &zipIterRec{obj: it, nextFn: nextFn}, nil
}

// zipStepValue 实现 IteratorStepValue：next() 结果须为对象，
// 返回 (value, done, err)。
func zipStepValue(r *zipIterRec) (object.Value, bool, object.Value) {
	if r.nativeNext == nil && r.nextFn == nil {
		return nil, false, object.NewTypeError("Iterator.zip: iterator has no callable next")
	}
	if r.nativeNext != nil {
		// 原生 next 闭包直接返回 (value, done)，不走结果对象协议。
		v, done := r.nativeNext()
		if errv := callbackThrown(); errv != nil {
			return nil, false, errv
		}
		if done {
			return nil, true, nil
		}
		if v == nil {
			v = object.UndefinedSingleton
		}
		return v, false, nil
	}
	res := object.CallFunction(r.nextFn, r.obj)
	if errv := callbackThrown(); errv != nil {
		return nil, false, errv
	}
	if res == nil || !object.IsObjectLike(res) {
		return nil, false, object.NewTypeError("Iterator.zip: next() returned a non-object")
	}
	doneV, _ := res.GetProperty("done")
	if errv := callbackThrown(); errv != nil {
		return nil, false, errv
	}
	done := doneV != nil && doneV.IsTruthy()
	if done {
		return nil, true, nil
	}
	val, _ := res.GetProperty("value")
	if errv := callbackThrown(); errv != nil {
		return nil, false, errv
	}
	return val, false, nil
}

// zipCloseAll 实现 IteratorCloseAll（异常吞掉，规范允许传播首个错误但
// 这里与既有 CloseIterator 语义一致取静默）。
func zipCloseAll(recs []*zipIterRec) {
	for _, r := range recs {
		if r != nil {
			object.CloseIterator(r.obj)
		}
	}
}

// zipOptions 解析 GetOptionsObject + mode + paddingOption。
func zipOptions(args []object.Value) (mode string, paddingOpt object.Value, errv object.Value) {
	mode = "shortest"
	paddingOpt = object.UndefinedSingleton
	if len(args) > 1 && args[1] != nil && args[1] != object.UndefinedSingleton && args[1] != object.NullSingleton {
		if args[1] == nil || !object.IsObjectLike(args[1]) {
			return "", nil, object.NewTypeError("Iterator.zip: options must be an object")
		}
	} else {
		return
	}
	mv, _ := args[1].GetProperty("mode")
	if errv := callbackThrown(); errv != nil {
		return "", nil, errv
	}
	if mv != nil && mv != object.UndefinedSingleton {
		ms, ok := mv.(*object.String)
		if !ok {
			return "", nil, object.NewTypeError("Iterator.zip: mode must be a string")
		}
		switch ms.Value {
		case "shortest", "longest", "strict":
			mode = ms.Value
		default:
			return "", nil, object.NewTypeError("Iterator.zip: invalid mode")
		}
	}
	if mode == "longest" {
		pv, _ := args[1].GetProperty("padding")
		if errv := callbackThrown(); errv != nil {
			return "", nil, errv
		}
		if pv != nil && pv != object.UndefinedSingleton && !object.IsObjectLike(pv) {
			return "", nil, object.NewTypeError("Iterator.zip: padding must be an object")
		}
		paddingOpt = pv
	}
	return
}

// zipSetup 完成 zip/zipKeyed 共享的前置步骤，返回 (iters, padding, errv)。
// iterablesArg: zip 传 nil（走 GetIterator），zipKeyed 传宿主对象
// （键枚举路径）。keysOut 由 zipKeyed 传入收集（zip 忽略）。
func zipSetup(args []object.Value, keyed bool, keysOut *[]string) (string, []*zipIterRec, []object.Value, object.Value) {
	if len(args) == 0 || args[0] == nil || !object.IsObjectLike(args[0]) {
		return "", nil, nil, object.NewTypeError("Iterator.zip: iterables must be an object")
	}
	mode, paddingOpt, errv := zipOptions(args)
	if errv != nil {
		return "", nil, nil, errv
	}
	var iters []*zipIterRec
	var padding []object.Value
	if keyed {
		// zipKeyed: 自有可枚举属性（值非 undefined）即 iterables，
		// 键序即 OwnPropertyKeys 序。
		host := args[0]
		var allKeys []string
		if hk, ok := host.(interface{ OwnKeys() []string }); ok {
			allKeys = hk.OwnKeys()
		} else {
			return "", nil, nil, object.NewTypeError("Iterator.zipKeyed: iterables must be an object")
		}
		hasOwn := func(k string) bool {
			if h, ok := host.(interface{ HasOwnProperty(string) bool }); ok {
				return h.HasOwnProperty(k)
			}
			return false
		}
		for _, key := range allKeys {
			if !hasOwn(key) {
				continue
			}
			val, _ := host.GetProperty(key)
			if errv := callbackThrown(); errv != nil {
				zipCloseAll(iters)
				return "", nil, nil, errv
			}
			if val == nil || val == object.UndefinedSingleton {
				continue
			}
			*keysOut = append(*keysOut, key)
			r, errv := zipGetIteratorFlattenable(val)
			if errv != nil {
				zipCloseAll(iters)
				return "", nil, nil, errv
			}
			iters = append(iters, r)
		}
	} else {
		input, errv := zipGetIterator(args[0])
		if errv != nil {
			return "", nil, nil, errv
		}
		for {
			item, done, errv := zipStepValue(input)
			if errv != nil {
				zipCloseAll(iters)
				object.CloseIterator(input.obj)
				return "", nil, nil, errv
			}
			if done {
				break
			}
			r, errv := zipGetIteratorFlattenable(item)
			if errv != nil {
				zipCloseAll(append([]*zipIterRec{input}, iters...))
				return "", nil, nil, errv
			}
			iters = append(iters, r)
		}
	}
	iterCount := len(iters)
	if mode == "longest" {
		if paddingOpt == nil || paddingOpt == object.UndefinedSingleton {
			for i := 0; i < iterCount; i++ {
				padding = append(padding, object.UndefinedSingleton)
			}
		} else {
			pIter, errv := zipGetIteratorFlattenable(paddingOpt)
			if errv != nil {
				zipCloseAll(iters)
				return "", nil, nil, errv
			}
			if keyed {
				// zipKeyed: padding 按键逐个 Get(paddingOption, key)
				for _, key := range *keysOut {
					pv, _ := paddingOpt.GetProperty(key)
					if errv := callbackThrown(); errv != nil {
						zipCloseAll(append([]*zipIterRec{pIter}, iters...))
						return "", nil, nil, errv
					}
					if pv == nil {
						pv = object.UndefinedSingleton
					}
					padding = append(padding, pv)
				}
			} else {
				usingIterator := true
				for i := 0; i < iterCount; i++ {
					if usingIterator {
						v, done, errv := zipStepValue(pIter)
						if errv != nil {
							zipCloseAll(append([]*zipIterRec{pIter}, iters...))
							return "", nil, nil, errv
						}
						if done {
							usingIterator = false
							padding = append(padding, object.UndefinedSingleton)
						} else {
							padding = append(padding, v)
						}
					} else {
						padding = append(padding, object.UndefinedSingleton)
					}
				}
				if usingIterator {
					object.CloseIterator(pIter.obj)
				}
			}
		}
	}
	return mode, iters, padding, nil
}

// setupZipMethods 注册 Iterator.zip 与 Iterator.zipKeyed（规范算法）。
func setupZipMethods(fn *object.BuiltinFunction) {
	// zipFinish: 数组结果；zipKeyedFinish: 以 keys 构造 null 原型对象。
	buildZipIter := func(mode string, iters []*zipIterRec, padding []object.Value, finish func([]object.Value) (object.Value, object.Value)) *object.JSIterator {
		var it *object.JSIterator
		open := make([]bool, len(iters))
		for i := range open {
			open[i] = true
		}
		openCount := len(iters)
		closeAllOpen := func() {
			for i, o := range open {
				if o && iters[i] != nil {
					object.CloseIterator(iters[i].obj)
					open[i] = false
				}
			}
			openCount = 0
		}
		it = &object.JSIterator{Name: "Iterator Helper", NextFn: func() (object.Value, bool) {
			if len(iters) == 0 {
				it.MarkExhausted()
				return object.UndefinedSingleton, true
			}
			results := make([]object.Value, 0, len(iters))
			for i := 0; i < len(iters); i++ {
				if iters[i] == nil {
					// 已耗尽: 仅 longest 到达这里
					results = append(results, padding[i])
					continue
				}
				v, done, errv := zipStepValue(iters[i])
				if errv != nil {
					open[i] = false
					zipCloseAllOpenExcept(open, iters)
					it.PendingErr = errv
					return object.UndefinedSingleton, true
				}
				if done {
					open[i] = false
					iters[i] = nil
					openCount--
					switch mode {
					case "shortest":
						closeAllOpen()
						it.MarkExhausted()
						return object.UndefinedSingleton, true
					case "strict":
						if i != 0 {
							closeAllOpen()
							it.PendingErr = object.NewTypeError("Iterator.zip: iterables have different lengths")
							return object.UndefinedSingleton, true
						}
						for k := 1; k < len(iters); k++ {
							if iters[k] == nil {
								continue
							}
							_, d2, errv2 := zipStepValue(iters[k])
							if errv2 != nil {
								open[k] = false
								zipCloseAllOpenExcept(open, iters)
								it.PendingErr = errv2
								return object.UndefinedSingleton, true
							}
							if d2 {
								iters[k] = nil
								open[k] = false
								openCount--
							} else {
								closeAllOpen()
								it.PendingErr = object.NewTypeError("Iterator.zip: iterables have different lengths")
								return object.UndefinedSingleton, true
							}
						}
						it.MarkExhausted()
						return object.UndefinedSingleton, true
					default: // longest
						if openCount == 0 {
							it.MarkExhausted()
							return object.UndefinedSingleton, true
						}
						v = padding[i]
					}
				}
				results = append(results, v)
			}
			out, errv := finish(results)
			if errv != nil {
				closeAllOpen()
				it.PendingErr = errv
				return object.UndefinedSingleton, true
			}
			return out, false
		}}
		it.ReturnHook = func() {
			closeAllOpen()
		}
		return it
	}

	finishZip := func(results []object.Value) (object.Value, object.Value) {
		return object.NewArray(results), nil
	}
	zipFn := object.NewBuiltin("zip", func(args ...object.Value) object.Value {
		mode, iters, padding, errv := zipSetup(args, false, nil)
		if errv != nil {
			return errv
		}
		return buildZipIter(mode, iters, padding, finishZip)
	})
	zipFn.NotConstructable = true
	fn.SetProperty("zip", zipFn)

	zipKeyedFn := object.NewBuiltin("zipKeyed", func(args ...object.Value) object.Value {
		var keys []string
		mode, iters, padding, errv := zipSetup(args, true, &keys)
		if errv != nil {
			return errv
		}
		finish := func(results []object.Value) (object.Value, object.Value) {
			obj := object.NewObjectWithProto(object.NullSingleton)
			for i, k := range keys {
				obj.DefineOwnProperty(k, object.DataProperty(results[i]))
			}
			return obj, nil
		}
		return buildZipIter(mode, iters, padding, finish)
	})
	zipKeyedFn.NotConstructable = true
	fn.SetProperty("zipKeyed", zipKeyedFn)
}

// zipCloseAllOpenExcept 关闭 open 中仍为 true 的所有迭代器。
func zipCloseAllOpenExcept(open []bool, iters []*zipIterRec) {
	for i, o := range open {
		if o && iters[i] != nil {
			object.CloseIterator(iters[i].obj)
			open[i] = false
		}
	}
}
