package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupPromise 设置 Promise 构造器。
func setupPromise(env *runtime.Environment) {
	promiseProto := setupPromiseProto()
	object.SetPromiseProto(promiseProto)

	promiseFn := object.NewBuiltin("Promise", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewErrorWithName("TypeError", "Promise resolver undefined is not a function")
		}
		executor := args[0]
		if !object.IsCallable(executor) {
			return object.NewErrorWithName("TypeError", "Promise resolver is not a function")
		}

		p := object.NewPromise()

		// 创建 resolve 和 reject 函数
		resolve := object.NewBuiltin("resolve", func(args ...object.Value) object.Value {
			val := object.Value(object.UndefinedSingleton)
			if len(args) > 0 {
				val = args[0]
			}
			p.Resolve(val)
			return object.UndefinedSingleton
		})

		reject := object.NewBuiltin("reject", func(args ...object.Value) object.Value {
			reason := object.Value(object.UndefinedSingleton)
			if len(args) > 0 {
				reason = args[0]
			}
			p.Reject(reason)
			return object.UndefinedSingleton
		})

		// 调用 executor(resolve, reject)
		// 规范: executor 抛出的异常应让 Promise reject。
		// 旧实现完全忽略执行结果，executor 出错会让 Promise 永久 pending。
		//
		// 这里同时检查两个信号:
		// 1. TakeCallbackError —— 回调桥记录的 JS 异常 (涵盖 throw 任意值);
		// 2. 返回的 *Error       —— executor 返回/抛出的 Error 对象。
		ret := object.CallFunction(executor, nil, resolve, reject)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
				p.Reject(thrown)
			} else {
				p.Reject(object.NewErrorWithName("Error", cbErr.Error()))
			}
		} else if e, isErr := ret.(*object.Error); isErr {
			p.Reject(e)
		}

		return p
	})

	// Promise.resolve(value)
	promiseFn.SetProperty("resolve", object.NewBuiltin("resolve", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			p := object.NewPromise()
			p.Resolve(object.UndefinedSingleton)
			return p
		}
		// 如果已经是 Promise，直接返回
		if p, ok := args[0].(*object.Promise); ok {
			return p
		}
		p := object.NewPromise()
		p.Resolve(args[0])
		return p
	}))

	// Promise.reject(reason)
	promiseFn.SetProperty("reject", object.NewBuiltin("reject", func(args ...object.Value) object.Value {
		p := object.NewPromise()
		reason := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			reason = args[0]
		}
		p.Reject(reason)
		return p
	}))

	// Promise.all(iterable)
	promiseFn.SetProperty("all", object.NewBuiltin("all", func(args ...object.Value) object.Value {
		arr, errVal := promiseIterable(args, "Promise.all")
		if errVal != nil {
			return errVal
		}
		if len(arr.Elements) == 0 {
			p := object.NewPromise()
			p.Resolve(object.NewArray([]object.Value{}))
			return p
		}

		result := object.NewPromise()
		results := make([]object.Value, len(arr.Elements))
		remaining := len(arr.Elements)
		rejected := false

		for i, elem := range arr.Elements {
			idx := i
			if promise, ok := elem.(*object.Promise); ok {
				promise.OnFulfilled(object.NewBuiltin("__all_resolve", func(args ...object.Value) object.Value {
					if rejected {
						return object.UndefinedSingleton
					}
					results[idx] = argAt(args, 0)
					remaining--
					if remaining == 0 {
						result.Resolve(object.NewArray(results))
					}
					return object.UndefinedSingleton
				}))
				promise.OnRejected(object.NewBuiltin("__all_reject", func(args ...object.Value) object.Value {
					if !rejected {
						rejected = true
						result.Reject(argAt(args, 0))
					}
					return object.UndefinedSingleton
				}))
			} else {
				// 非 Promise 值直接当作已 resolved
				results[idx] = elem
				remaining--
				if remaining == 0 {
					result.Resolve(object.NewArray(results))
				}
			}
		}

		return result
	}))

	// Promise.race(iterable): 第一个 settled 的结果决定胜负
	promiseFn.SetProperty("race", object.NewBuiltin("race", func(args ...object.Value) object.Value {
		// 非可迭代参数必须 reject，否则返回的 Promise 会永远停留在 pending
		arr, errVal := promiseIterable(args, "Promise.race")
		if errVal != nil {
			return errVal
		}

		result := object.NewPromise()
		settled := false

		for _, elem := range arr.Elements {
			if promise, ok := elem.(*object.Promise); ok {
				promise.OnFulfilled(object.NewBuiltin("__race_resolve", func(args ...object.Value) object.Value {
					if !settled {
						settled = true
						result.Resolve(argAt(args, 0))
					}
					return object.UndefinedSingleton
				}))
				promise.OnRejected(object.NewBuiltin("__race_reject", func(args ...object.Value) object.Value {
					if !settled {
						settled = true
						result.Reject(argAt(args, 0))
					}
					return object.UndefinedSingleton
				}))
			} else {
				// 非 Promise 值直接 resolve
				if !settled {
					settled = true
					result.Resolve(elem)
				}
				break
			}
		}

		return result
	}))

	// Promise.allSettled(iterable): 等待全部 settle，永不 reject (除参数非法)
	promiseFn.SetProperty("allSettled", object.NewBuiltin("allSettled", func(args ...object.Value) object.Value {
		arr, errVal := promiseIterable(args, "Promise.allSettled")
		if errVal != nil {
			return errVal
		}
		if len(arr.Elements) == 0 {
			p := object.NewPromise()
			p.Resolve(object.NewArray([]object.Value{}))
			return p
		}

		result := object.NewPromise()
		results := make([]object.Value, len(arr.Elements))
		remaining := len(arr.Elements)

		for i, elem := range arr.Elements {
			idx := i
			if promise, ok := elem.(*object.Promise); ok {
				promise.OnFulfilled(object.NewBuiltin("__settled_resolve", func(args ...object.Value) object.Value {
					statusObj := object.NewObject()
					statusObj.SetProperty("status", object.NewString("fulfilled"))
					statusObj.SetProperty("value", argAt(args, 0))
					results[idx] = statusObj
					remaining--
					if remaining == 0 {
						result.Resolve(object.NewArray(results))
					}
					return object.UndefinedSingleton
				}))
				promise.OnRejected(object.NewBuiltin("__settled_reject", func(args ...object.Value) object.Value {
					statusObj := object.NewObject()
					statusObj.SetProperty("status", object.NewString("rejected"))
					statusObj.SetProperty("reason", argAt(args, 0))
					results[idx] = statusObj
					remaining--
					if remaining == 0 {
						result.Resolve(object.NewArray(results))
					}
					return object.UndefinedSingleton
				}))
			} else {
				statusObj := object.NewObject()
				statusObj.SetProperty("status", object.NewString("fulfilled"))
				statusObj.SetProperty("value", elem)
				results[idx] = statusObj
				remaining--
				if remaining == 0 {
					result.Resolve(object.NewArray(results))
				}
			}
		}

		return result
	}))

	// Promise.any(iterable): 第一个 fulfilled 获胜；全部 rejected 时
	// 以 AggregateError 拒绝 (ES2021)
	promiseFn.SetProperty("any", object.NewBuiltin("any", func(args ...object.Value) object.Value {
		arr, errVal := promiseIterable(args, "Promise.any")
		if errVal != nil {
			return errVal
		}

		result := object.NewPromise()
		n := len(arr.Elements)
		if n == 0 {
			result.Reject(newAggregateError([]object.Value{}, "All promises were rejected"))
			return result
		}

		rejectedCount := 0
		settled := false
		errors := make([]object.Value, n)

		for i, elem := range arr.Elements {
			idx := i
			if promise, ok := elem.(*object.Promise); ok {
				promise.OnFulfilled(object.NewBuiltin("__any_resolve", func(args ...object.Value) object.Value {
					if !settled {
						settled = true
						result.Resolve(argAt(args, 0))
					}
					return object.UndefinedSingleton
				}))
				promise.OnRejected(object.NewBuiltin("__any_reject", func(args ...object.Value) object.Value {
					if settled {
						return object.UndefinedSingleton
					}
					errors[idx] = argAt(args, 0)
					rejectedCount++
					if rejectedCount == n {
						settled = true
						result.Reject(newAggregateError(errors, "All promises were rejected"))
					}
					return object.UndefinedSingleton
				}))
			} else if !settled {
				// 非 Promise 值视为立即 fulfilled
				settled = true
				result.Resolve(elem)
			}
		}

		return result
	}))

	// Promise.withResolvers() (ES2024): 返回 {promise, resolve, reject}
	promiseFn.SetProperty("withResolvers", object.NewBuiltin("withResolvers", func(args ...object.Value) object.Value {
		p := object.NewPromise()
		var settled bool
		resolve := object.NewBuiltin("resolve", func(rargs ...object.Value) object.Value {
			val := object.Value(object.UndefinedSingleton)
			if len(rargs) > 0 {
				val = rargs[0]
			}
			if !settled {
				settled = true
				p.Resolve(val)
			}
			return object.UndefinedSingleton
		})
		reject := object.NewBuiltin("reject", func(rargs ...object.Value) object.Value {
			reason := object.Value(object.UndefinedSingleton)
			if len(rargs) > 0 {
				reason = rargs[0]
			}
			if !settled {
				settled = true
				p.Reject(reason)
			}
			return object.UndefinedSingleton
		})
		result := object.NewObject()
		result.SetProperty("promise", p)
		result.SetProperty("resolve", resolve)
		result.SetProperty("reject", reject)
		return result
	}))

	// Promise.try(fn, ...args) (ES2025): 同步调用 fn，异常转为 rejection
	promiseFn.SetProperty("try", object.NewBuiltin("try", func(args ...object.Value) object.Value {
		if len(args) == 0 || !object.IsCallable(args[0]) {
			return object.NewErrorWithName("TypeError", "Promise.try: fn must be a function")
		}
		fn := args[0]
		callArgs := []object.Value{}
		if len(args) > 1 {
			callArgs = args[1:]
		}
		p := object.NewPromise()
		ret := object.CallFunction(fn, object.UndefinedSingleton, callArgs...)
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			// 规范: throw 的原始值 (如字符串 "boom") 直接作为 rejection reason
			if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
				p.Reject(thrown)
			} else {
				p.Reject(object.NewErrorWithName("Error", cbErr.Error()))
			}
		} else if e, isErr := ret.(*object.Error); isErr && e != nil {
			p.Reject(e)
		} else {
			p.Resolve(ret)
		}
		return p
	}))

	promiseFn.SetProperty("prototype", promiseProto)
	promiseProto.SetProperty("constructor", promiseFn)

	env.Declare("Promise", promiseFn, false)
}

// promiseIterable 解析 Promise.all/race/allSettled/any 的可迭代参数。
//
// 参数缺失或非数组时返回一个已 reject 的 Promise: 规范要求以 TypeError
// 拒绝。旧实现返回永久 pending 的 Promise，会让 await 永远挂起。
func promiseIterable(args []object.Value, api string) (*object.Array, object.Value) {
	if len(args) == 0 || !isArrayValue(args[0]) {
		rejected := object.NewPromise()
		rejected.Reject(object.NewTypeError("%s argument is not iterable", api))
		return nil, rejected
	}
	return args[0].(*object.Array), nil
}

// argAt 安全取第 i 个参数，越界时返回 undefined。
// Promise 回调接收的参数个数不受控，直接 args[0] 会 panic。
func argAt(args []object.Value, i int) object.Value {
	if i < len(args) && args[i] != nil {
		return args[i]
	}
	return object.UndefinedSingleton
}

// newAggregateError 构造 AggregateError (Promise.any 全部失败时使用)。
func newAggregateError(errors []object.Value, message string) object.Value {
	e := object.NewErrorWithName("AggregateError", message)
	e.SetProperty("errors", object.NewArray(errors))
	return e
}

func setupPromiseProto() *object.Object {
	p := object.NewObject()

	// then(onFulfilled, onRejected)
	p.SetProperty("then", object.NewBuiltinMethod("then", func(this object.Value, args ...object.Value) object.Value {
		promise, ok := this.(*object.Promise)
		if !ok {
			return thisTypeError("Promise", "then", this)
		}
		var onFulfilled object.Value
		var onRejected object.Value
		if len(args) > 0 {
			onFulfilled = args[0]
		}
		if len(args) > 1 {
			onRejected = args[1]
		}

		next := object.NewPromise()
		promise.Lock()

		if promise.State == object.PromiseFulfilled {
			promise.Unlock()
			if object.IsCallable(onFulfilled) {
				result := object.CallFunction(onFulfilled, nil, promise.Value)
				next.Resolve(result)
			} else {
				next.Resolve(promise.Value)
			}
		} else if promise.State == object.PromiseRejected {
			promise.Unlock()
			if object.IsCallable(onRejected) {
				result := object.CallFunction(onRejected, nil, promise.Reason)
				next.Resolve(result)
			} else {
				next.Reject(promise.Reason)
			}
		} else {
			// Pending: 注册回调
			if object.IsCallable(onFulfilled) {
				promise.ThenCallbacks = append(promise.ThenCallbacks, object.PromiseCallback{
					Callback:    onFulfilled,
					NextPromise: next,
				})
			} else {
				// 没有 onFulfilled，透传值
				promise.ThenCallbacks = append(promise.ThenCallbacks, object.PromiseCallback{
					Callback:    nil,
					NextPromise: next,
				})
			}
			if object.IsCallable(onRejected) {
				promise.CatchCallbacks = append(promise.CatchCallbacks, object.PromiseCallback{
					Callback:    onRejected,
					NextPromise: next,
					IsCatch:     true,
				})
			} else {
				// 没有 onRejected，透传错误
				promise.CatchCallbacks = append(promise.CatchCallbacks, object.PromiseCallback{
					Callback:    nil,
					NextPromise: next,
					IsCatch:     true,
				})
			}
			promise.Unlock()
		}
		return next
	}))

	// catch(onRejected)
	p.SetProperty("catch", object.NewBuiltinMethod("catch", func(this object.Value, args ...object.Value) object.Value {
		promise, ok := this.(*object.Promise)
		if !ok {
			return thisTypeError("Promise", "catch", this)
		}
		var onRejected object.Value
		if len(args) > 0 {
			onRejected = args[0]
		}
		return promise.Catch(onRejected)
	}))

	// finally(onFinally)
	p.SetProperty("finally", object.NewBuiltinMethod("finally", func(this object.Value, args ...object.Value) object.Value {
		promise, ok := this.(*object.Promise)
		if !ok {
			return thisTypeError("Promise", "finally", this)
		}
		var onFinally object.Value
		if len(args) > 0 {
			onFinally = args[0]
		}
		return promise.Finally(onFinally)
	}))

	return p
}
