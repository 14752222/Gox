package stdlib

import (
	"math"
	"time"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupTimers 注册 setTimeout/setInterval/clearTimeout/clearInterval 及
// requestIdleCallback/cancelIdleCallback 全局函数。
func setupTimers(env *runtime.Environment) {
	scheduler := object.GlobalScheduler()

	// setTimeout(fn, delay, ...args)
	env.Declare("setTimeout", object.NewBuiltin("setTimeout", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewErrorWithName("TypeError", "setTimeout requires a function")
		}
		fn := args[0]
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "setTimeout: first argument must be a function")
		}
		delay := time.Duration(0)
		if len(args) > 1 {
			delay = time.Duration(toFloat(args[1]) * float64(time.Millisecond))
		}
		id := scheduler.SetTimeout(bindTimerCallback(fn, args), delay)
		return object.NewNumber(float64(id))
	}), false)

	// setInterval(fn, interval, ...args)
	env.Declare("setInterval", object.NewBuiltin("setInterval", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewErrorWithName("TypeError", "setInterval requires a function")
		}
		fn := args[0]
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "setInterval: first argument must be a function")
		}
		interval := time.Duration(0)
		if len(args) > 1 {
			interval = time.Duration(toFloat(args[1]) * float64(time.Millisecond))
		}
		id := scheduler.SetInterval(bindTimerCallback(fn, args), interval)
		return object.NewNumber(float64(id))
	}), false)

	// clearTimeout(id)
	env.Declare("clearTimeout", object.NewBuiltin("clearTimeout", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			id := int(toFloat(args[0]))
			scheduler.Clear(id)
		}
		return object.UndefinedSingleton
	}), false)

	// clearInterval(id)
	env.Declare("clearInterval", object.NewBuiltin("clearInterval", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			id := int(toFloat(args[0]))
			scheduler.Clear(id)
		}
		return object.UndefinedSingleton
	}), false)

	// requestIdleCallback(fn, options) - options: { timeout: ms }
	env.Declare("requestIdleCallback", object.NewBuiltin("requestIdleCallback", func(args ...object.Value) object.Value {
		if len(args) < 1 {
			return object.NewErrorWithName("TypeError", "requestIdleCallback requires a function")
		}
		fn := args[0]
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "requestIdleCallback: first argument must be a function")
		}
		// 可选第二参数: { timeout: 毫秒 } — 超时后即使不空闲也会派发
		timeout := time.Duration(0)
		if len(args) > 1 {
			if opts, ok := args[1].(*object.Object); ok {
				if tv, found := opts.GetProperty("timeout"); found {
					timeout = time.Duration(toFloat(tv) * float64(time.Millisecond))
				}
			}
		}
		id := scheduler.RequestIdle(bindTimerCallback(fn, args), timeout)
		return object.NewNumber(float64(id))
	}), false)

	// cancelIdleCallback(id)
	env.Declare("cancelIdleCallback", object.NewBuiltin("cancelIdleCallback", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			id := int(toFloat(args[0]))
			scheduler.CancelIdle(id)
		}
		return object.UndefinedSingleton
	}), false)

	// delay(ms, value) — 教程示例: 返回 Promise 的异步 API。
	// ms 毫秒后 resolve(value)。省略 value 时 resolve(undefined)。
	// 注意异步 API 的错误报告约定: 参数校验失败用 reject 而不是同步 throw，
	// 这样调用方总是通过 .catch / try-await 统一处理错误。
	env.Declare("delay", object.NewBuiltin("delay", func(args ...object.Value) object.Value {
		result := object.NewPromise()

		if len(args) < 1 {
			result.Reject(object.NewTypeError("delay: missing argument 1 (expected duration in milliseconds)"))
			return result
		}
		ms := toFloat(args[0])
		if math.IsNaN(ms) || ms < 0 {
			result.Reject(object.NewRangeError("delay: duration must be a non-negative number, got %s", args[0].Inspect()))
			return result
		}

		var val object.Value = object.UndefinedSingleton
		if len(args) > 1 {
			val = args[1]
		}

		// 用调度器注册一次性定时器，到期时 resolve 外层 Promise。
		// 定时器持有的是这个匿名内建函数与 result 的引用，
		// Promise settle 后引用即可被 GC 回收。
		scheduler.SetTimeout(object.NewBuiltin("__delay_resolve", func(args ...object.Value) object.Value {
			result.Resolve(val)
			return object.UndefinedSingleton
		}), time.Duration(ms*float64(time.Millisecond)))
		return result
	}), false)
}

// bindTimerCallback 将定时器回调与额外参数绑定。
//
// setTimeout(fn, delay, ...args) 规范要求 args 在回调触发时作为实参传入。
// 调度器只接受一个零参回调，因此当存在额外参数时在这里包一层闭包。
// args 是完整的调用参数列表 (args[0] 是 fn，args[1] 是延时)。
func bindTimerCallback(fn object.Value, args []object.Value) object.Value {
	if len(args) <= 2 {
		return fn
	}
	extra := append([]object.Value(nil), args[2:]...)
	return object.NewBuiltin("timerCallback", func(...object.Value) object.Value {
		return object.CallFunction(fn, nil, extra...)
	})
}
