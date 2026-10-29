package object

import "math"

// Iterator.prototype 上的 ES2025/ES2026 helper 方法的规范语义收口
// (rFXXbq built-ins 洼地批次)。
//
// 此前 helper 只在 *JSIterator 实例魔法上分发 (esIteratorHelper), 存在
// 四类偏差:
//   - 接收者无校验 (非迭代器 this 不抛 TypeError);
//   - 底层 next/回调抛错被吞 (回调桥错误不检查), 迭代带着错误值继续;
//   - 底层迭代器从不关闭 (IteratorClose 缺失);
//   - 生成器实例上不可用 (Generator.GetProperty 不认识 helper 名)。
//
// 本文件提供 IteratorHelperMethod(name) 工厂: 接收者经 GetIteratorDirect
// (读一次 next 方法) 校验, 迭代驱动统一走 callNext (回调桥错误检查 +
// next 返回值必须为对象), abrupt 完成时 IteratorClose 底层迭代器。
// Iterator.prototype (stdlib) 与 JSIterator 实例魔法共用同一实现。

// IteratorProto 是 %Iterator.prototype% 的注册表 (stdlib 装配时写入),
// Generator 实例的 GetProperty 兜底查这里 (规范: %GeneratorPrototype%
// 的 [[Prototype]] 就是 %Iterator.prototype%)。
var IteratorProto Value

// SetIteratorProto 注册 %Iterator.prototype%。
func SetIteratorProto(p Value) { IteratorProto = p }

// GetIteratorProto 返回 %Iterator.prototype% (未注册时为 nil)。
func GetIteratorProto() Value { return IteratorProto }

// iteratorHelperNames 是 Iterator.prototype 全部 helper 方法名
// (ES2025 + ES2026 chunking/windows; toAsync 暂缓)。
var iteratorHelperNames = []string{
	"map", "filter", "take", "drop", "flatMap", "reduce", "toArray",
	"forEach", "some", "every", "find", "includes", "join",
	"chunks", "windows",
}

// IteratorHelperNames 返回 helper 方法名列表 (stdlib 装配用)。
func IteratorHelperNames() []string { return iteratorHelperNames }

// IsIteratorHelperName 报告 name 是否为 Iterator helper 方法名。
func IsIteratorHelperName(name string) bool {
	for _, n := range iteratorHelperNames {
		if n == name {
			return true
		}
	}
	return false
}

// CloseIterator 实现 IteratorClose(iter, completion): 取 return 方法,
// 可调用则调用并丢弃结果; return 抛错经回调桥返回该错误值。
// 返回 nil 表示无错误。
func CloseIterator(iter Value) Value {
	if iter == nil {
		return nil
	}
	ret, found := iter.GetProperty("return")
	if !found || !IsCallable(ret) {
		if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
			return ev
		}
		return nil
	}
	CallFunction(ret, iter)
	if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return ev
	}
	return nil
}

// iteratorDriver 是 helper 迭代的统一驱动: 底层 next 一步。
// 返回 (value, done, errVal): errVal 非 nil 时调用方必须把迭代置为
// 异常完成 (eager helper 直接向上传播并关闭底层; lazy helper 记入
// PendingErr)。
type iteratorDriver struct {
	recv   Value
	nextFn Value
}

func newIteratorDriver(this Value) (*iteratorDriver, Value) {
	// GetIteratorDirect 前置校验 (this-non-object 族): this 非对象直接
	// TypeError, 不得先读 next (含原始值包装原型上的 getter 副作用)。
	if !IsObjectLike(this) {
		return nil, NewTypeError("Iterator method: receiver must be an Object")
	}
	nextFn, found := this.GetProperty("next")
	if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return nil, ev
	}
	if !found || !IsCallable(nextFn) {
		return nil, NewTypeError("Iterator method: receiver must have a callable `next` method")
	}
	return &iteratorDriver{recv: this, nextFn: nextFn}, nil
}

// step 推进一步: 返回 (value, done, errVal)。
func (d *iteratorDriver) step() (Value, bool, Value) {
	r := CallFunction(d.nextFn, d.recv)
	if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return UndefinedSingleton, true, ev
	}
	obj, ok := r.(*Object)
	if !ok {
		if !IsObjectLike(r) {
			return UndefinedSingleton, true, NewTypeError("Iterator: next() must return an object")
		}
	}
	if obj == nil {
		return UndefinedSingleton, true, NewTypeError("Iterator: next() must return an object")
	}
	done := false
	if dv, found := obj.GetProperty("done"); found {
		if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
			return UndefinedSingleton, true, ev
		}
		if dv != nil && dv.IsTruthy() {
			done = true
		}
	} else if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return UndefinedSingleton, true, ev
	}
	var v Value = UndefinedSingleton
	if vv, found := obj.GetProperty("value"); found {
		if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
			return UndefinedSingleton, true, ev
		}
		if vv != nil {
			v = vv
		}
	} else if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
		return UndefinedSingleton, true, ev
	}
	return v, done, nil
}

// IteratorHelperMethod 返回规范语义的 Iterator.prototype.<name> 方法。
func IteratorHelperMethod(name string) *BuiltinMethod {
	lengths := map[string]int{
		"map": 1, "filter": 1, "take": 1, "drop": 1, "flatMap": 1,
		"reduce": 1, "toArray": 0, "forEach": 1, "some": 1, "every": 1,
		"find": 1, "includes": 1, "join": 1, "chunks": 1, "windows": 1,
	}
	var m *BuiltinMethod
	switch name {
	case "map", "filter", "flatMap":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			// 副作用顺序 (argument-effect-order): IsObject(this) →
			// IsCallable(mapper) → GetIteratorDirect。
			if !IsObjectLike(this) {
				return NewTypeError("Iterator.prototype.%s: receiver must be an Object", name)
			}
			fn := argOr(args, 0)
			if !IsCallable(fn) {
				return NewTypeError("Iterator.prototype.%s: callback must be a function", name)
			}
			d, errVal := newIteratorDriver(this)
			if errVal != nil {
				return errVal
			}
			i := 0
			var innerNext func() (Value, bool)
			var inner Value
			closeOuter := func(errVal Value) Value {
				if ev := CloseIterator(this); ev != nil {
					return ev
				}
				return errVal
			}
			var it *JSIterator
			it = &JSIterator{Name: "Iterator " + name, NextFn: func() (Value, bool) {
				for {
					if inner != nil {
						v, done := innerNext()
						if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
							it.PendingErr = closeOuter(ev)
							return UndefinedSingleton, true
						}
						if !done {
							return v, false
						}
						inner, innerNext = nil, nil
					}
					v, done, err := d.step()
					if err != nil {
						it.PendingErr = closeOuter(err)
						return UndefinedSingleton, true
					}
					if done {
						it.exhausted = true
						return UndefinedSingleton, true
					}
					switch name {
					case "map":
						r := CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
						if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
							it.PendingErr = closeOuter(ev)
							return UndefinedSingleton, true
						}
						i++
						if r == nil {
							r = UndefinedSingleton
						}
						return r, false
					case "filter":
						keep := CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
						if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
							it.PendingErr = closeOuter(ev)
							return UndefinedSingleton, true
						}
						i++
						if keep != nil && keep.IsTruthy() {
							return v, false
						}
					case "flatMap":
						r := CallFunction(fn, UndefinedSingleton, v)
						if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
							it.PendingErr = closeOuter(ev)
							return UndefinedSingleton, true
						}
						nx, ok := Iterate(r)
						if !ok {
							it.PendingErr = closeOuter(NewTypeError("Iterator.prototype.flatMap: callback must return an iterable"))
							return UndefinedSingleton, true
						}
						inner, innerNext = r, nx
					}
				}
			}}
			it.ReturnHook = func() { CloseIterator(this) }
			return it
		})

	case "take", "drop":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			d, errVal := newIteratorDriver(this)
			if errVal != nil {
				return errVal
			}
			// limit: ToNumber (回调桥错误传播)。
			raw := argOr(args, 0)
			num, err2 := iteratorToNumber(raw)
			if err2 != nil {
				return err2
			}
			if math.IsNaN(num) {
				return NewTypeError("Iterator.prototype.%s: limit must be a number, got NaN", name)
			}
			limit := num
			if name == "drop" && math.IsNaN(limit) {
				limit = 0
			}
			if limit < 0 {
				if ev := CloseIterator(this); ev != nil {
					return ev
				}
				return NewRangeError("Iterator.prototype.%s: limit must be >= 0, got %v", name, limit)
			}
			count := 0.0
			var it *JSIterator
			it = &JSIterator{Name: "Iterator " + name, NextFn: func() (Value, bool) {
				if name == "take" {
					if count >= limit {
						if ev := CloseIterator(this); ev != nil {
							it.PendingErr = ev
						}
						return UndefinedSingleton, true
					}
					v, done, err := d.step()
					if err != nil {
						it.PendingErr = err
						return UndefinedSingleton, true
					}
					if done {
						it.exhausted = true
						return UndefinedSingleton, true
					}
					count++
					return v, false
				}
				// drop
				for count < limit {
					count++
					_, done, err := d.step()
					if err != nil {
						it.PendingErr = err
						return UndefinedSingleton, true
					}
					if done {
						it.exhausted = true
						return UndefinedSingleton, true
					}
				}
				v, done, err := d.step()
				if err != nil {
					it.PendingErr = err
					return UndefinedSingleton, true
				}
				if done {
					it.exhausted = true
				}
				return v, done
			}}
			it.ReturnHook = func() { CloseIterator(this) }
			return it
		})

	case "chunks", "windows":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			d, errVal := newIteratorDriver(this)
			if errVal != nil {
				return errVal
			}
			// chunkSize/windowSize: 不做数值强制转换 —— 非数字直接 TypeError
			// (chunkSize-no-coercion: valueOf/toString 不得调用)。
			raw := argOr(args, 0)
			num, isNum := raw.(*Number)
			if !isNum {
				return NewTypeError("Iterator.prototype.%s: size must be a number", name)
			}
			n := num.Value
			if math.IsNaN(n) || math.IsInf(n, 0) || n != math.Trunc(n) {
				return NewTypeError("Iterator.prototype.%s: size must be an integer", name)
			}
			if n < 1 || n >= 1<<32 {
				if ev := CloseIterator(this); ev != nil {
					return ev
				}
				return NewRangeError("Iterator.prototype.%s: size is out of range: %v", name, n)
			}
			size := int(n)
			// undersized 参数 (ES2026): undefined → "only-full"; 其余仅接受
			// "only-full"/"allow-partial" 两个字符串, 其它一律 TypeError
			// (不做任何强制转换; abrupt 先 IteratorClose 再抛)。
			undersized := "only-full"
			if name == "windows" {
				rawU := argOr(args, 1)
				if rawU != UndefinedSingleton {
					s, isStr := rawU.(*String)
					if !isStr || (s.Value != "only-full" && s.Value != "allow-partial") {
						if ev := CloseIterator(this); ev != nil {
							return ev
						}
						return NewTypeError("Iterator.prototype.windows: undersized must be \"only-full\" or \"allow-partial\"")
					}
					undersized = s.Value
				}
			}
			allowPartial := undersized == "allow-partial"
			buf := make([]Value, 0, size)
			var it *JSIterator
			it = &JSIterator{Name: "Iterator " + name, NextFn: func() (Value, bool) {
				for {
					v, done, err := d.step()
					if err != nil {
						it.PendingErr = err
						return UndefinedSingleton, true
					}
				if done {
					it.exhausted = true
					if name == "chunks" && len(buf) > 0 {
						out := buf
						buf = nil
						return NewArray(out), false
					}
					// windows + allow-partial: 收尾产出欠尺寸窗口。
					if name == "windows" && allowPartial && len(buf) > 0 {
						out := buf
						buf = nil
						return NewArray(out), false
					}
					return UndefinedSingleton, true
				}
					if name == "chunks" {
						buf = append(buf, v)
						if len(buf) >= size {
							out := make([]Value, size)
							copy(out, buf)
							buf = buf[:0]
							return NewArray(out), false
						}
						continue
					}
					// windows: 滑动窗口
					if len(buf) == size {
						copy(buf, buf[1:])
						buf[size-1] = v
						out := make([]Value, size)
						copy(out, buf)
						return NewArray(out), false
					}
					buf = append(buf, v)
					if len(buf) == size {
						out := make([]Value, size)
						copy(out, buf)
						return NewArray(out), false
					}
				}
			}}
			it.ReturnHook = func() { CloseIterator(this) }
			return it
		})

	case "toArray":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			list, errVal := iteratorCollectList(this, name, true)
			if errVal != nil {
				return errVal
			}
			return NewArray(list)
		})

	case "forEach":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			fn := argOr(args, 0)
			if !IsCallable(fn) {
				return NewTypeError("Iterator.prototype.forEach: callback must be a function")
			}
			_, errVal := iteratorCollect(this, name, func(v Value, i int) Value {
				CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
				return UndefinedSingleton
			})
			if errVal != nil {
				return errVal
			}
			return UndefinedSingleton
		})

	case "some", "every":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			fn := argOr(args, 0)
			if !IsCallable(fn) {
				return NewTypeError("Iterator.prototype.%s: callback must be a function", name)
			}
			_, errVal := iteratorCollect(this, name, func(v Value, i int) Value {
				r := CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
				if name == "some" && r != nil && r.IsTruthy() {
					return TrueSingleton // 短路信号
				}
				if name == "every" && (r == nil || !r.IsTruthy()) {
					return FalseSingleton
				}
				return nil
			})
			if errVal != nil {
				return errVal
			}
			if name == "some" {
				return FalseSingleton
			}
			return TrueSingleton
		})

	case "find":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			fn := argOr(args, 0)
			if !IsCallable(fn) {
				return NewTypeError("Iterator.prototype.find: callback must be a function")
			}
			found, errVal := iteratorCollect(this, name, func(v Value, i int) Value {
				r := CallFunction(fn, UndefinedSingleton, v, NewInt(int64(i)))
				if r != nil && r.IsTruthy() {
					return v
				}
				return nil
			})
			if errVal != nil {
				return errVal
			}
			if found == nil {
				return UndefinedSingleton
			}
			return found
		})

	case "includes":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			search := argOr(args, 0)
			found, errVal := iteratorCollect(this, name, func(v Value, i int) Value {
				if sameValueZero(v, search) {
					return TrueSingleton
				}
				return nil
			})
			if errVal != nil {
				return errVal
			}
			return NewBoolean(found == TrueSingleton)
		})

	case "join":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			sep := ","
			if len(args) > 0 && args[0] != UndefinedSingleton {
				sep = ToString(args[0])
			}
			parts, errVal := iteratorCollectList(this, name, false)
			if errVal != nil {
				return errVal
			}
			out := make([]byte, 0, 32)
			for i, p := range parts {
				if i > 0 {
					out = append(out, sep...)
				}
				out = append(out, ToString(p)...)
			}
			return NewString(string(out))
		})

	case "reduce":
		m = NewBuiltinMethod(name, func(this Value, args ...Value) Value {
			fn := argOr(args, 0)
			if !IsCallable(fn) {
				return NewTypeError("Iterator.prototype.reduce: callback must be a function")
			}
			d, errVal := newIteratorDriver(this)
			if errVal != nil {
				return errVal
			}
			var acc Value
			hasAcc := len(args) > 1
			if hasAcc {
				acc = args[1]
			}
			i := 0
			for {
				v, done, err := d.step()
				if err != nil {
					CloseIterator(this)
					return err
				}
				if done {
					if !hasAcc {
						return NewTypeError("Iterator.prototype.reduce: no initial value")
					}
					return acc
				}
				if !hasAcc {
					acc = v
					hasAcc = true
					i++
					continue
				}
				r := CallFunction(fn, UndefinedSingleton, acc, v, NewInt(int64(i)))
				if ev := TakeCallbackErrorValue(); ev != UndefinedSingleton {
					CloseIterator(this)
					return ev
				}
				acc = r
				i++
			}
		})

	default:
		return nil
	}
	if ln, ok := lengths[name]; ok {
		m.SetFunctionLength(ln)
	}
	return m
}

// iteratorCollect eager 驱动整条迭代: fn 返回非 nil 即短路 (携带结果),
// 任何 abrupt 完成先 IteratorClose 再传播错误。返回 (短路值或 nil, 错误值)。
func iteratorCollect(this Value, api string, fn func(v Value, i int) Value) (Value, Value) {
	list, errVal := iteratorCollectList(this, api, true)
	if errVal != nil {
		return nil, errVal
	}
	for i, v := range list {
		if r := fn(v, i); r != nil {
			return r, nil
		}
	}
	return nil, nil
}

// iteratorCollectList eager 收集全部迭代值。closeOnErr 控制协议违规/
// 回调异常等 abrupt 完成时是否 IteratorClose —— 规范中 join 的算法不包
// IteratorClose (does-not-close-on-iterator-protocol-violation), 其余
// eager helper (toArray/forEach/some/every/find/reduce) 都要先关闭再传播。
func iteratorCollectList(this Value, api string, closeOnErr bool) ([]Value, Value) {
	d, errVal := newIteratorDriver(this)
	if errVal != nil {
		return nil, errVal
	}
	var out []Value
	for {
		v, done, err := d.step()
		if err != nil {
			if closeOnErr {
				CloseIterator(this)
			}
			return nil, err
		}
		if done {
			return out, nil
		}
		out = append(out, v)
	}
}

// iteratorToNumber 实现 helper 数值参数的 ToNumber (回调桥错误传播,
// Symbol 抛 TypeError)。
func iteratorToNumber(v Value) (float64, Value) {
	f, err := toF64E(v)
	if err != nil {
		return 0, err
	}
	return f, nil
}

// sameValueZero 判定 SameValueZero (includes 用): 数值含 ±0 相等与
// NaN 相等, BigInt 按值, 其余按引用同一性。
func sameValueZero(a, b Value) bool {
	if a == b {
		return true
	}
	an, aok := a.(*Number)
	bn, bok := b.(*Number)
	if aok && bok {
		return an.Value == bn.Value || (math.IsNaN(an.Value) && math.IsNaN(bn.Value))
	}
	ab, aok := a.(*BigInt)
	bb, bok := b.(*BigInt)
	if aok && bok {
		return ab.Value.Cmp(bb.Value) == 0
	}
	return false
}
