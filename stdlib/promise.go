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
		object.CallFunction(executor, nil, resolve, reject)

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
		if len(args) == 0 {
			p := object.NewPromise()
			p.Resolve(object.NewArray([]object.Value{}))
			return p
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			p := object.NewPromise()
			p.Reject(object.NewErrorWithName("TypeError", "Promise.all argument is not iterable"))
			return p
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
				promise.Then(object.NewBuiltin("__all_resolve", func(args ...object.Value) object.Value {
					if rejected {
						return object.UndefinedSingleton
					}
					results[idx] = args[0]
					remaining--
					if remaining == 0 {
						result.Resolve(object.NewArray(results))
					}
					return object.UndefinedSingleton
				}))
				promise.Catch(object.NewBuiltin("__all_reject", func(args ...object.Value) object.Value {
					if !rejected {
						rejected = true
						result.Reject(args[0])
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

	// Promise.race(iterable)
	promiseFn.SetProperty("race", object.NewBuiltin("race", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			p := object.NewPromise()
			return p
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			p := object.NewPromise()
			return p
		}

		result := object.NewPromise()
		settled := false

		for _, elem := range arr.Elements {
			if promise, ok := elem.(*object.Promise); ok {
				promise.Then(object.NewBuiltin("__race_resolve", func(args ...object.Value) object.Value {
					if !settled {
						settled = true
						result.Resolve(args[0])
					}
					return object.UndefinedSingleton
				}))
				promise.Catch(object.NewBuiltin("__race_reject", func(args ...object.Value) object.Value {
					if !settled {
						settled = true
						result.Reject(args[0])
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

	// Promise.allSettled(iterable)
	promiseFn.SetProperty("allSettled", object.NewBuiltin("allSettled", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			p := object.NewPromise()
			p.Resolve(object.NewArray([]object.Value{}))
			return p
		}
		arr, ok := args[0].(*object.Array)
		if !ok {
			p := object.NewPromise()
			p.Resolve(object.NewArray([]object.Value{}))
			return p
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
				promise.Then(object.NewBuiltin("__settled_resolve", func(args ...object.Value) object.Value {
					statusObj := object.NewObject()
					statusObj.SetProperty("status", object.NewString("fulfilled"))
					statusObj.SetProperty("value", args[0])
					results[idx] = statusObj
					remaining--
					if remaining == 0 {
						result.Resolve(object.NewArray(results))
					}
					return object.UndefinedSingleton
				}))
				promise.Catch(object.NewBuiltin("__settled_reject", func(args ...object.Value) object.Value {
					statusObj := object.NewObject()
					statusObj.SetProperty("status", object.NewString("rejected"))
					statusObj.SetProperty("reason", args[0])
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

	env.Declare("Promise", promiseFn, false)
}

func setupPromiseProto() *object.Object {
	p := object.NewObject()

	// then(onFulfilled, onRejected)
	p.SetProperty("then", object.NewBuiltinMethod("then", func(this object.Value, args ...object.Value) object.Value {
		promise, ok := this.(*object.Promise)
		if !ok {
			return this
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
			return this
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
			return this
		}
		var onFinally object.Value
		if len(args) > 0 {
			onFinally = args[0]
		}
		return promise.Finally(onFinally)
	}))

	return p
}
