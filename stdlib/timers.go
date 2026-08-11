package stdlib

import (
	"time"

	"js-runtime/object"
	"js-runtime/runtime"
)

// setupTimers 注册 setTimeout/setInterval/clearTimeout/clearInterval 全局函数。
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
		id := scheduler.SetTimeout(fn, delay)
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
		id := scheduler.SetInterval(fn, interval)
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
}
