package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupAsync 设置 async/await 的运行时辅助函数 __spawn。
// async function f() { body } 编译为:
//   function f() { return __spawn((function* () { body' }) ()); }
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
		step(gen, object.UndefinedSingleton, result)
		return result
	})
	env.Declare("__spawn", spawn, false)
}

// step 驱动 generator 一步, 完成后 resolve 结果 Promise。
func step(gen *object.Generator, arg object.Value, result *object.Promise) {
	value, done := object.GeneratorNext(gen, arg)
	if done {
		result.Resolve(value)
		return
	}

	// yield 的值是 Promise: 等待其 resolve/reject 后继续
	if p, ok := value.(*object.Promise); ok {
		p.Then(object.NewBuiltin("__step_next", func(args ...object.Value) object.Value {
			step(gen, args[0], result)
			return object.UndefinedSingleton
		}))
		p.Catch(object.NewBuiltin("__step_err", func(args ...object.Value) object.Value {
			result.Reject(args[0])
			return object.UndefinedSingleton
		}))
		return
	}

	// 非 Promise 值: 直接继续
	step(gen, value, result)
}
