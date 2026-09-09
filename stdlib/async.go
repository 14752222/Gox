package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupAsync 设置 async/await 的运行时辅助函数 __spawn。
// async function f() { body } 编译为:
//
//	function f() { return __spawn((function* () { body' }) ()); }
//
// 其中 body' 把 await X 编译为 yield X。
// __spawn 驱动 generator: 每个 yield 的值若是 Promise 则等待其 resolve 后
// 把结果传回 generator (作为 await 表达式的值), 直到 generator 完成,
// 最终 resolve 返回的 Promise。
func setupAsync(env *runtime.Environment) {
	spawn := object.NewBuiltin("__spawn", func(args ...object.Value) object.Value {
		result := object.NewPromise()
		if len(args) == 0 {
			result.Reject(object.NewErrorWithName("TypeError", "__spawn: missing generator"))
			return result
		}
		gen, ok := args[0].(*object.Generator)
		if !ok {
			result.Reject(object.NewErrorWithName("TypeError", "__spawn: argument is not a generator"))
			return result
		}
		step(gen, object.UndefinedSingleton, result, nil)
		return result
	})
	env.Declare("__spawn", spawn, false)
}

// step 驱动 generator 一步, 完成后 resolve 结果 Promise。
//
// throwVal 非 nil 表示把该值作为异常抛入 generator (await 的 promise
// 被 reject 时): generator 体内的 try/catch 可以捕获它并继续执行，
// 未捕获时 generator 终止、结果 Promise 被 reject。
func step(gen *object.Generator, arg object.Value, result *object.Promise, throwVal object.Value) {
	var value object.Value
	var done bool
	if throwVal != nil {
		value, done = object.GeneratorThrow(gen, throwVal)
	} else {
		value, done = object.GeneratorNext(gen, arg)
	}

	// async 函数体抛出的异常: 回调桥把它记为 callbackError，
	// 而 GeneratorNext/GeneratorThrow 会退化成 (undefined, true)。
	// 不检查的话异常会被静默吞掉，promise 变成 resolve(undefined)。
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		result.Reject(object.NewErrorWithName("Error", cbErr.Error()))
		return
	}

	if done {
		result.Resolve(value)
		return
	}

	// yield 的值是 Promise: 等待其 resolve/reject 后继续
	if p, ok := value.(*object.Promise); ok {
		p.Then(object.NewBuiltin("__step_next", func(args ...object.Value) object.Value {
			step(gen, argAt(args, 0), result, nil)
			return object.UndefinedSingleton
		}))
		p.Catch(object.NewBuiltin("__step_err", func(args ...object.Value) object.Value {
			step(gen, nil, result, argAt(args, 0))
			return object.UndefinedSingleton
		}))
		return
	}

	// 非 Promise 值: 直接继续
	step(gen, value, result, nil)
}
