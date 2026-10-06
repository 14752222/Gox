package stdlib

import (
	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
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
	// async generator 的 next/return/throw 驱动 (object → stdlib 回调桥)。
	object.SetAsyncGeneratorMethod(asyncGeneratorMethod)

	// __async_generator: 把内层 Generator 包装成 AsyncGenerator 对象。
	// async generator 的 wrapper 收尾调用它 (见 compiler.compileAsyncGeneratorSelf)。
	env.Declare("__async_generator", object.NewBuiltin("__async_generator",
		func(args ...object.Value) object.Value {
			if len(args) == 0 {
				return object.UndefinedSingleton
			}
			gen, ok := args[0].(*object.Generator)
			if !ok {
				return object.UndefinedSingleton
			}
			ag := object.NewAsyncGenerator(gen)
			if proto := object.GetAsyncGeneratorProto(); proto != nil {
				ag.Proto = proto
			}
			return ag
		}), false)

	// __async_iter_check: async generator yield* 委托的迭代结果校验
	// (AsyncGeneratorYieldDelegate: Await 之后 innerResult 必须是对象, 否则
	// TypeError)。返回 Error 对象即由 VM 抛出 (内置函数错误约定)。
	env.Declare("__async_iter_check", object.NewBuiltin("__async_iter_check",
		func(args ...object.Value) object.Value {
			v := argAt(args, 0)
			if !object.IsObjectValue(v) {
				return object.NewErrorWithName("TypeError", "iterator result is not an object")
			}
			return v
		}), false)

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

	setupAsyncGeneratorIntrinsics(env)
}

// setupAsyncGeneratorIntrinsics 装配 AsyncGenerator 的内建原型链 (最小可用版)。
//
// 规范结构:
//
//	%AsyncGeneratorFunction%            全局 AsyncGeneratorFunction (函数对象)
//	  .prototype = %AsyncGeneratorFunction.prototype% (AGFFP)
//	  AGFFP.prototype = %AsyncGeneratorPrototype%      (AGP)
//	%AsyncGeneratorPrototype% (AGP): constructor = AGFFP, @@toStringTag = "AsyncGenerator"
//	async function* 实例: [[Prototype]] = AGP
//
// 已知边界 (牵扯函数对象公共模型, 本版未接入): 本运行时的函数对象尚未建立
// [[Prototype]] (Object.getPrototypeOf(fn) 对闭包返回 null), 因此
// `Object.getPrototypeOf(async function*(){}) === AGFFP` 尚不成立, 且
// `%AsyncGeneratorFunction%` 目前不可真正构造。这里先保证三块内建对象存在且
// 互相正确链接, 并把 async generator 实例的 [[Prototype]] 指向 AGP。
func setupAsyncGeneratorIntrinsics(env *runtime.Environment) {
	tagSym := object.GetGlobalSymbol("Symbol.toStringTag")

	agProto := object.NewObjectWithProto(objectPrototype) // %AsyncGeneratorPrototype%
	agFuncProto := object.NewObject()                     // %AsyncGeneratorFunction.prototype%
	// %AsyncGeneratorFunction.prototype%.[[Prototype]] = %Function.prototype%
	// (由 setupFunctionIntrinsics 先装配)。
	if fp := object.GetFunctionPrototype(); fp != nil {
		agFuncProto.Proto = fp
	}
	agFunc := object.NewBuiltin("AsyncGeneratorFunction", func(args ...object.Value) object.Value {
		return newDynamicFunction(env, args, dynFuncAsyncGenerator)
	})
	// %AsyncGeneratorFunction%.[[Prototype]] = %Function% (与 GeneratorFunction 同)。
	if fv, ok := env.Get("Function"); ok {
		agFunc.FuncPrototype = fv
	}

	agProto.SetProperty("constructor", agFuncProto)
	if tagSym != nil {
		agProto.SetBuiltinSymbolProperty(tagSym, object.NewString("AsyncGenerator"))
	}
	agFuncProto.SetProperty("prototype", agProto)
	agFuncProto.SetProperty("constructor", agFunc)
	if tagSym != nil {
		agFuncProto.SetBuiltinSymbolProperty(tagSym, object.NewString("AsyncGeneratorFunction"))
	}
	agFunc.SetProperty("prototype", agFuncProto)

	// 注: %AsyncGeneratorFunction% 是**内建 intrinsic**，不是全局对象属性 ——
	// 与 %GeneratorFunction% / %AsyncFunction% 口径一致 (Node: typeof
	// AsyncGeneratorFunction === "undefined")。此前这里 env.Declare 把它注册成
	// 全局，导致读未声明标识符 `AsyncGeneratorFunction` 不抛 ReferenceError
	// (rYVgne)。该对象只应经 agFuncProto.constructor 这条原型链暴露:
	// Object.getPrototypeOf(async function*(){}).constructor。
	// 让 AsyncGenerator 实例的 [[Prototype]] 指向 AGP (此前为 nil)。
	object.SetAsyncGeneratorProto(agProto)
	// 供 vm.createClosure 给 async generator 函数对象选 [[Prototype]]。
	object.SetAsyncGeneratorFunctionPrototype(agFuncProto)
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
	//
	// 优先用原始抛出值 (callbackErrorValue) 作为 rejection reason: 它就是
	// `throw new Error("x")` 里的那个 Error 对象本身 —— 规范要求 catch 侧
	// 拿到的与抛出的严格相等。cbErr 只是 Go 侧字符串 (已含 "Error: " 前缀)，
	// 拿它重新包一层会得到 "Error: Error: x" 的双前缀消息，且丢失原始对象。
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
			result.Reject(thrown)
		} else {
			result.Reject(object.NewErrorWithName("Error", cbErr.Error()))
		}
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

// objectPrototype 是全局 Object.prototype 引用 (SetupGlobals 时填充)。
// 迭代结果对象需要它的原型是 Object.prototype (规范 CreateIterResultObject)。
var objectPrototype object.Value

// SetObjectPrototypeRef 由 SetupGlobals 记录 Object.prototype。
func SetObjectPrototypeRef(p object.Value) { objectPrototype = p }

// newAsyncIterResult 构造 {value, done}, 原型为 Object.prototype。
func newAsyncIterResult(value object.Value, done bool) object.Value {
	res := object.NewAsyncGeneratorIterResult(value, done)
	if objectPrototype != nil {
		res.Proto = objectPrototype
	}
	return res
}

// ===== async generator 驱动 =====
//
// 与 step() 的关键差异: async generator 的 next/return/throw 各自返回一个
// Promise, 且体内有两种挂起点 —— await (内部, 等待后自动恢复) 与
// yield (消费者可见, 结算 next() 的 Promise)。挂起类型由 VM 写入
// Generator.LastYieldIsAwait (OP_AWAIT 置真, OP_YIELD 置假)。
//
// 多个并发请求按 FIFO 排队: 一个请求未结算前, 后续请求只入队不驱动。

// asyncGeneratorMethod 是 object.AsyncGenerator 的方法驱动入口。
func asyncGeneratorMethod(g *object.AsyncGenerator, kind int, arg object.Value) *object.Promise {
	p := object.NewPromise()
	g.Requests = append(g.Requests, &object.AsyncGenRequest{Kind: kind, Arg: arg, Promise: p})
	if !g.Running {
		agResumeNext(g)
	}
	return p
}

// agResumeNext 处理队首请求。已完成的生成器直接按请求种类结算。
func agResumeNext(g *object.AsyncGenerator) {
	if g.Running || len(g.Requests) == 0 {
		return
	}
	req := g.Requests[0]
	if g.Done {
		g.Requests = g.Requests[1:]
		agSettleCompleted(req)
		agResumeNext(g)
		return
	}
	g.Running = true
	agStep(g, req, req.Kind, req.Arg)
}

// agSettleCompleted 结算一个针对"已完成生成器"的请求。
func agSettleCompleted(req *object.AsyncGenRequest) {
	switch req.Kind {
	case object.AGReturnKind:
		req.Promise.Resolve(newAsyncIterResult(req.Arg, true))
	case object.AGThrowKind:
		req.Promise.Reject(req.Arg)
	default:
		req.Promise.Resolve(newAsyncIterResult(object.UndefinedSingleton, true))
	}
}

// agStep 用 (kind, arg) 驱动内层 generator 一步, 并按挂起类型决定后续。
func agStep(g *object.AsyncGenerator, req *object.AsyncGenRequest, kind int, arg object.Value) {
	var value object.Value
	var done bool
	switch kind {
	case object.AGNextKind:
		value, done = object.GeneratorNext(g.Gen, arg)
	case object.AGReturnKind:
		value, done = object.GeneratorReturn(g.Gen, arg)
	default:
		value, done = object.GeneratorThrow(g.Gen, arg)
	}

	// 体内未捕获的异常: 回调桥记为 callbackError。必须优先用原始抛出值
	// 作为 rejection reason —— 否则 catch 侧拿到 "Error: Error: x" 双前缀
	// 且与抛出值不严格相等。(与 step() 同款错误桥纪律。)
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		g.Done = true
		agRejectBridge(req, cbErr)
		agFinish(g, req)
		return
	}

	if done {
		g.Done = true
		req.Promise.Resolve(newAsyncIterResult(value, true))
		agFinish(g, req)
		return
	}

	if g.Gen.LastYieldIsAwait {
		// await: 等待值后自动恢复 (不结算对外 Promise)。
		agAwait(value,
			func(resolved object.Value) { agStep(g, req, object.AGNextKind, resolved) },
			func(reason object.Value) { agStep(g, req, object.AGThrowKind, reason) })
		return
	}

	// yield: 先按 AsyncGeneratorYield 语义 await 值, 再对消费者结算。
	// 值 reject 时把 reason 抛回 yield 点 (体内 try/catch 可捕获), 继续驱动。
	agAwait(value,
		func(resolved object.Value) {
			req.Promise.Resolve(newAsyncIterResult(resolved, false))
			agFinish(g, req)
		},
		func(reason object.Value) { agStep(g, req, object.AGThrowKind, reason) })
}

// agRejectBridge 用原始抛出值 (优先) 结算 rejection。
func agRejectBridge(req *object.AsyncGenRequest, cbErr error) {
	if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
		req.Promise.Reject(thrown)
		return
	}
	req.Promise.Reject(object.NewErrorWithName("Error", cbErr.Error()))
}

// agFinish 把一个已结算的请求移出队列, 让出 executing 状态并继续处理后续请求。
func agFinish(g *object.AsyncGenerator, req *object.AsyncGenRequest) {
	if len(g.Requests) > 0 && g.Requests[0] == req {
		g.Requests = g.Requests[1:]
	} else {
		for i, r := range g.Requests {
			if r == req {
				g.Requests = append(g.Requests[:i], g.Requests[i+1:]...)
				break
			}
		}
	}
	g.Running = false
	agResumeNext(g)
}

// agAwait 按 await 语义处理一个值: Promise 等待其结算, 非 Promise 立即透传。
// 回调按 Gox 现有的同步 Promise 模型执行 (结算即回调)。
func agAwait(value object.Value, onResolve, onReject func(object.Value)) {
	if p, ok := value.(*object.Promise); ok {
		p.Then(object.NewBuiltin("__ag_step", func(args ...object.Value) object.Value {
			onResolve(argAt(args, 0))
			return object.UndefinedSingleton
		}))
		p.Catch(object.NewBuiltin("__ag_step_err", func(args ...object.Value) object.Value {
			onReject(argAt(args, 0))
			return object.UndefinedSingleton
		}))
		return
	}
	onResolve(value)
}
