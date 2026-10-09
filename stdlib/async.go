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
	// 第 2 个实参是 wrapper 自己的 .prototype 对象, 作为实例的 [[Prototype]]
	// (规范: 实例 → fn.prototype → %AsyncGeneratorPrototype%); 缺省时回退到
	// %AsyncGeneratorPrototype% (老码路径/防御性)。
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
			if len(args) > 1 && object.IsObjectValue(args[1]) {
				ag.Proto = args[1]
			} else if proto := object.GetAsyncGeneratorProto(); proto != nil {
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

// setupAsyncGeneratorIntrinsics 装配 AsyncGenerator 的内建原型链。
//
// 规范结构 (经 Node v22 实测逐条对齐):
//
//	%AsyncGeneratorFunction% (AGF, 函数对象)
//	  .prototype = %AsyncGeneratorFunction.prototype% (AGFFP)
//	    { [[Writable]]: false, [[Enumerable]]: false, [[Configurable]]: false }
//	  .length = 1, .name = "AsyncGeneratorFunction" (均不可写可枚举、可配置)
//	  [[Prototype]] = %Function%
//
//	AGFFP (普通对象, [[Prototype]] = %Function.prototype%)
//	  .prototype = %AsyncGeneratorPrototype% (AGP)
//	    { writable: false, enumerable: false, configurable: true }
//	  .constructor = AGF { writable: false, enumerable: false, configurable: true }
//	  @@toStringTag = "AsyncGeneratorFunction" (不可写, 可配置)
//
//	AGP (普通对象, [[Prototype]] = %Object.prototype%)
//	  .constructor = AGFFP  ← 注意: 是 AGFFP 而非 AGF (Node 实测)
//	  .next / .return / .throw = 内建方法 { writable: true, enumerable: false,
//	    configurable: true }, 各方法 name/length = 1 (不可写, 可配置)
//	  @@toStringTag = "AsyncGenerator" (不可写, 可配置)
//
//	async function* 实例: [[Prototype]] = 该函数自己的 .prototype
//	  (其 [[Prototype]] 才是 AGP)。见 compileAsyncGeneratorSelf / __async_generator。
func setupAsyncGeneratorIntrinsics(env *runtime.Environment) {
	tagSym := object.GetGlobalSymbol("Symbol.toStringTag")

	// %AsyncIteratorPrototype%: 异步迭代器原型链的顶端, AGP 挂在它下面。
	// 它承载两个"取迭代器自身"的语义成员 —— @@asyncIterator (返回 this)
	// 与 @@asyncDispose (await using 的释放入口), 二者都是 writable: true /
	// enumerable: false / configurable: true (规范 17 章的默认值)。
	aip := object.NewObjectWithProto(objectPrototype)
	if itSym := object.GetGlobalSymbol("Symbol.asyncIterator"); itSym != nil {
		aip.DefineOwnSymbolProperty(itSym, object.PropertyDescriptor{
			Value: asyncIteratorProtoAsyncIterator(),
			Writable: true, Enumerable: false, Configurable: true,
		})
	}
	if adSym := object.SymbolAsyncDispose(); adSym != nil {
		aip.DefineOwnSymbolProperty(adSym, object.PropertyDescriptor{
			Value: asyncIteratorProtoAsyncDispose(),
			Writable: true, Enumerable: false, Configurable: true,
		})
	}
	object.SetAsyncIteratorProto(aip)

	// 注意: AGP 的 [[Prototype]] 是 AIP, **不是** %Object.prototype% ——
	// 规范里 Object.getPrototypeOf(Object.getPrototypeOf(gen.prototype))
	// 必须拿到 AIP (test262 AsyncIteratorPrototype/* 就是这么取它的)。
	agProto := object.NewObjectWithProto(aip) // %AsyncGeneratorPrototype%
	agFuncProto := object.NewObject()                     // %AsyncGeneratorFunction.prototype%
	// AGFFP.[[Prototype]] = %Function.prototype% (setupFunctionIntrinsics 已装配)。
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
	agFunc.SetFunctionLength(1)

	// AGP.constructor = AGFFP (Node 实测: 不是 AGF), 不可写但可配置。
	agProto.DefineOwnProperty("constructor", object.PropertyDescriptor{
		Value: agFuncProto, Writable: false, Enumerable: false, Configurable: true,
	})
	// AGP.next / .return / .throw: 可写、不可枚举、可配置的内建方法。
	for _, spec := range []struct {
		name string
		kind int
	}{
		{"next", object.AGNextKind},
		{"return", object.AGReturnKind},
		{"throw", object.AGThrowKind},
	} {
		agProto.DefineOwnProperty(spec.name, object.PropertyDescriptor{
			Value: asyncGeneratorProtoMethod(spec.name, spec.kind),
			Writable: true, Enumerable: false, Configurable: true,
		})
	}
	if tagSym != nil {
		agProto.DefineOwnSymbolProperty(tagSym, object.BuiltinSymbolProperty(object.NewString("AsyncGenerator")))
	}

	// AGFFP.prototype = AGP, 不可写但可配置 (注意: 不是不可配置)。
	agFuncProto.DefineOwnProperty("prototype", object.PropertyDescriptor{
		Value: agProto, Writable: false, Enumerable: false, Configurable: true,
	})
	agFuncProto.DefineOwnProperty("constructor", object.PropertyDescriptor{
		Value: agFunc, Writable: false, Enumerable: false, Configurable: true,
	})
	if tagSym != nil {
		agFuncProto.DefineOwnSymbolProperty(tagSym, object.BuiltinSymbolProperty(object.NewString("AsyncGeneratorFunction")))
	}

	// AGF.prototype = AGFFP, 不可写不可枚举**不可配置** (规范)。
	agFunc.DefineOwn("prototype", object.PropertyDescriptor{
		Value: agFuncProto, Writable: false, Enumerable: false, Configurable: false,
	})

	// 注: %AsyncGeneratorFunction% 是**内建 intrinsic**，不是全局对象属性 ——
	// 与 %GeneratorFunction% / %AsyncFunction% 口径一致 (Node: typeof
	// AsyncGeneratorFunction === "undefined")。该对象只应经 agFuncProto.constructor
	// 这条原型链暴露: Object.getPrototypeOf(async function*(){}).constructor。
	object.SetAsyncGeneratorProto(agProto)
	// 供 vm.createClosure 给 async generator 函数对象选 [[Prototype]]。
	object.SetAsyncGeneratorFunctionPrototype(agFuncProto)
}

// asyncGeneratorProtoMethod 构造 AGP 上的一个内建方法 (next/return/throw)。
//
// this 必须是真正的 AsyncGenerator (brand check): 否则按规范返回一个以
// TypeError reject 的 Promise (而非同步抛) —— test262
// AsyncGeneratorPrototype/*/this-val-not-* 即校验此行为。
// name 由 NewBuiltinMethod 带上; length 显式定为 1 (不可写、可配置)。
func asyncGeneratorProtoMethod(name string, kind int) object.Value {
	m := object.NewBuiltinMethod(name, func(this object.Value, args ...object.Value) object.Value {
		g, ok := this.(*object.AsyncGenerator)
		if !ok {
			p := object.NewPromise()
			p.Reject(object.NewErrorWithName("TypeError",
				"AsyncGenerator.prototype."+name+" called on incompatible receiver"))
			return p
		}
		arg := object.Value(object.UndefinedSingleton)
		if len(args) > 0 {
			arg = args[0]
		}
		return object.AsyncGeneratorMethod(g, kind, arg)
	})
	m.DefineOwn("length", object.PropertyDescriptor{
		Value: object.NewInt(1), Writable: false, Enumerable: false, Configurable: true,
	})
	return m
}

// ===== %AsyncIteratorPrototype% 的两个语义成员 (看板 rGmSsi) =====

// asyncIteratorProtoAsyncIterator 构造 %AsyncIteratorPrototype%[@@asyncIterator]。
//
// 规范全文只有一步: "Return the this value." —— 它是所有异步迭代器的
// 「我自己就是可迭代对象」入口 (for await 的 GetIterator 拿到异步迭代器后,
// 再对它取 @@asyncIterator 应原样返回)。this 不做任何装箱/校验, 所以
// `getAsyncIterator.call(4n)` 也返回 4n (test262 return-val.js)。
// name 为 "[Symbol.asyncIterator]", length 为 0 (NewBuiltinMethod 默认)。
func asyncIteratorProtoAsyncIterator() object.Value {
	return object.NewBuiltinMethod("[Symbol.asyncIterator]",
		func(this object.Value, args ...object.Value) object.Value {
			if this == nil {
				return object.UndefinedSingleton
			}
			return this
		})
}

// asyncIteratorProtoAsyncDispose 构造 %AsyncIteratorPrototype%[@@asyncDispose]。
//
// 规范 %AsyncIteratorPrototype%[@@asyncDispose]():
//
//	1. Let O be the this value.
//	2. Let promiseCapability be ! NewPromiseCapability(%Promise%).
//	3. Let return be GetMethod(O, "return").        ← getter 抛错在此中断
//	4. IfAbruptRejectPromise(return, promiseCapability).
//	5. return 为 undefined ⇒ resolve(undefined)
//	6. 否则 Call(return, O) ⇒ PromiseResolve ⇒ then(unwrap ⇒ undefined)
//	7. Return promiseCapability.[[Promise]].
//
// 关键: **它永不同步抛**。三条错误路径 (getter 抛 / 调用抛 / 返回的 promise
// 被 reject) 全部折成 rejection, 且 rejection reason 必须是**原始抛出值**
// —— `throw new CatchError()` 要让调用方 `assert.throwsAsync(CatchError)`
// 成立, 换成新 Error 对象就过不去 (同 r6OWbQ 的口径)。
func asyncIteratorProtoAsyncDispose() object.Value {
	return object.NewBuiltinMethod("[Symbol.asyncDispose]",
		func(this object.Value, args ...object.Value) object.Value {
			p := object.NewPromise()
			// 3) GetMethod(O, "return"): 属性读取会触发 getter。
			m, thrown := getMethodForAsyncDispose(this)
			if thrown != nil {
				p.Reject(thrown)
				return p
			}
			// 5) 没有 return 方法: resolve(undefined)。null/undefined 都按
			//    "没有"处理 (规范 GetMethod 的 nullish 分支), 不是 TypeError。
			if isNullishValue(m) {
				p.Resolve(object.UndefinedSingleton)
				return p
			}
			if !object.IsCallable(m) {
				// 有 return 属性但不可调用 ⇒ TypeError (GetMethod 的第 6 步)。
				p.Reject(object.NewErrorWithName("TypeError",
					"this.return is not a function"))
				return p
			}
			// 6.a) Call(return, O, « ») —— 零参数。
			res := object.CallFunction(m, this)
			if cbErr := object.TakeCallbackError(); cbErr != nil {
				p.Reject(bridgeThrownValue(cbErr))
				return p
			}
			// 6.c) PromiseResolve(%Promise%, result): result 是 promise 时
			//      采纳它 (Resolve 自带解包), 否则包成已完成的 promise。
			wrapper := object.NewPromise()
			wrapper.Resolve(res)
			// 6.e/g) unwrap: 无论 return 交回什么, 最终都以 undefined 兑现;
			//        wrapper 被 reject 时把 reason 原样传给 capability。
			wrapper.OnFulfilled(object.NewBuiltin("unwrap",
				func(a ...object.Value) object.Value {
					p.Resolve(object.UndefinedSingleton)
					return object.UndefinedSingleton
				}))
			wrapper.OnRejected(object.NewBuiltin("__asyncDispose_reject",
				func(a ...object.Value) object.Value {
					reason := object.Value(object.UndefinedSingleton)
					if len(a) > 0 && a[0] != nil {
						reason = a[0]
					}
					p.Reject(reason)
					return object.UndefinedSingleton
				}))
			return p
		})
}

// getMethodForAsyncDispose 取 this 上的 "return" 属性 (GetMethod 的前半)。
//
// 返回 (方法值, 中断值): 中断值非 nil 表示属性读取 (getter) 抛出, 调用方
// 应把它作为 rejection reason。属性读取**必须**展开 getter —— test262
// throw-return-getter.js 就是把 throw 写在 getter 里的。
func getMethodForAsyncDispose(this object.Value) (object.Value, object.Value) {
	if this == nil {
		return object.UndefinedSingleton, nil
	}
	var v object.Value = object.UndefinedSingleton
	// 用字符串键读取口而不是只认 *Object: 数组 / TypedArray / Promise 等
	// 也都有自己的 GetProperty, 而且**都会展开访问器 getter** —— 这正是
	// GetMethod 要的语义。
	if o, ok := this.(interface {
		GetProperty(string) (object.Value, bool)
	}); ok {
		if got, found := o.GetProperty("return"); found && got != nil {
			v = got
		}
	}
	// 回调桥: getter 抛出时这里才有值, 必须**紧跟**属性读取检查 ——
	// 下一次 CallFunction 会把错误槽清空。
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		return nil, bridgeThrownValue(cbErr)
	}
	return v, nil
}

// bridgeThrownValue 把回调桥的 Go error 还原成**原始抛出值**。
//
// 优先用值槽 (throw x 的 x): 非 Error 抛出值也必须原样传出, 否则
// `assert.throwsAsync(CatchError)` 这类按构造函数断言的用例过不去。
// 值槽为空时才退回用 error 文本造一个 Error (与 agRejectBridge 同口径)。
func bridgeThrownValue(cbErr error) object.Value {
	if thrown := object.TakeCallbackErrorValue(); thrown != nil &&
		thrown != object.UndefinedSingleton {
		return thrown
	}
	return object.NewErrorWithName("Error", cbErr.Error())
}

// isNullishValue 报告值是否为 null / undefined (GetMethod 的 nullish 分支)。
func isNullishValue(v object.Value) bool {
	if v == nil {
		return true
	}
	switch v.(type) {
	case *object.Null, *object.Undefined:
		return true
	}
	return false
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

// agResolveIterResult 结算一个请求的 iterResult { value, done }。
//
// unwrap 为真时 value 先过 **PromiseResolve** 再进 iterResult —— 规范
// AsyncGeneratorAwaitReturn 步骤 6「Let promise be Completion(PromiseResolve
// (%Promise%, completion.[[Value]]))」，步骤 9+ 再 PerformPromiseThen 把
// 解包后的值放进 iterResult。故 `it.return(somePromise)` 拿到的是**解包后**
// 的值, 而不是 promise 本身 (test262 return-{suspendedStart,suspendedYield,
// state-completed}-promise.js)。
//
// 对照地 **yield 的值不解包**: 规范 AsyncGeneratorYield 直接
// AsyncGeneratorResolve(generator, value, false), 只有体内显式
// `yield await x` 才先 await (test262 yield-star-promise-not-unwrapped:
// 手动实现的 async 迭代器产出 promise 时不得解包)。
func agResolveIterResult(req *object.AsyncGenRequest, value object.Value, done, unwrap bool) {
	if !unwrap {
		req.Promise.Resolve(newAsyncIterResult(value, done))
		return
	}
	// 规范 AsyncGeneratorAwaitReturn 步骤 6: PromiseResolve(%Promise%, value),
	// 步骤 7 规定 abrupt completion 要 reject。PromiseResolve 的 Get(x,
	// "constructor") 会触发用户定义的 getter —— broken-promise 系列靠这条
	// 路径 reject (见 agPromiseResolve)。
	p, ok, thrown := agPromiseResolve(value)
	if !ok {
		req.Promise.Reject(thrown)
		return
	}
	agAwait(p,
		func(v object.Value) { req.Promise.Resolve(newAsyncIterResult(v, done)) },
		func(reason object.Value) { req.Promise.Reject(reason) })
}

// agSettleCompleted 结算一个针对"已完成生成器"的请求。
func agSettleCompleted(req *object.AsyncGenRequest) {
	switch req.Kind {
	case object.AGReturnKind:
		agResolveIterResult(req, req.Arg, true, true)
	case object.AGThrowKind:
		req.Promise.Reject(req.Arg)
	default:
		req.Promise.Resolve(newAsyncIterResult(object.UndefinedSingleton, true))
	}
}

// agStep 用 (kind, arg) 驱动内层 generator 一步, 并按挂起类型决定后续。
func agStep(g *object.AsyncGenerator, req *object.AsyncGenRequest, kind int, arg object.Value) {
	// return 请求 + 生成器正挂起于 yield (已启动且未完成): 参数必须在**体
	// 内的 yield 挂起点** await (规范 AsyncGeneratorUnwrapYieldResumption) ——
	// 于是 await 的抛出/被拒都是"回灌进体"的异常, 体内的 try/catch 能捕获
	// 并继续 return (test262 return-suspendedYield-broken-promise-try-catch.js
	// 断言 caughtErr.message 与 {value:1, done:true})。
	//
	// 对照地 suspendedStart / completed 是**体外**交割 (规范
	// AsyncGeneratorAwaitReturn 步骤 7): 抛出直接 reject 请求, 体不被恢复
	// —— 另两个 broken-promise 用例正是断言"体一定不能跑"。
	if kind == object.AGReturnKind && g.Gen.Started && !g.Gen.Done {
		agReturnIntoSuspendedYield(g, req, arg)
		return
	}
	value, done := agDrive(g, kind, arg)

	// 体内未捕获的异常: 回调桥记为 callbackError。必须优先用原始抛出值
	// 作为 rejection reason —— 否则 catch 侧拿到 "Error: Error: x" 双前缀
	// 且与抛出值不严格相等。(与 step() 同款错误桥纪律。)
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		g.Done = true
		agRejectBridge(req, cbErr)
		agFinish(g, req)
		return
	}

	agTail(g, req, value, done)
}

// agDrive 用 (kind, arg) 驱动内层 generator 一步, 返回 (值, 是否完成)。
func agDrive(g *object.AsyncGenerator, kind int, arg object.Value) (object.Value, bool) {
	switch kind {
	case object.AGNextKind:
		return object.GeneratorNext(g.Gen, arg)
	case object.AGReturnKind:
		return object.GeneratorReturn(g.Gen, arg)
	default:
		return object.GeneratorThrow(g.Gen, arg)
	}
}

// agTail 处理一次驱动之后的收尾: 完成 / await / yield 三分支。
// 完成值是否再过 PromiseResolve 由请求种类决定 (见 agResolveIterResult)。
func agTail(g *object.AsyncGenerator, req *object.AsyncGenRequest, value object.Value, done bool) {
	if done {
		g.Done = true
		// return 请求的结算值要过 PromiseResolve 解包 (见 agResolveIterResult);
		// next / throw 的完成值不解包。
		agResolveIterResult(req, value, true, req.Kind == object.AGReturnKind)
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

	// yield: 按 AsyncGeneratorYield 语义结算 —— **不 await 值本身**
	// (规范 AsyncGeneratorYield 直接 AsyncGeneratorResolve(generator, value,
	// false); 只有体内显式 `yield await x` 才先 await)。若在此 await 值,
	// `yield somePromise` 会把 promise 解包, 破坏
	// test262 yield-star-promise-not-unwrapped (手动实现的 async 迭代器
	// 产出 promise 时不得解包)。
	req.Promise.Resolve(newAsyncIterResult(value, false))
	agFinish(g, req)
}

// agReturnIntoSuspendedYield 处理 suspendedYield 状态下的 return(v)。
//
// 规范 AsyncGeneratorUnwrapYieldResumption: yield 挂起点收到的 return 完成
// 要先 Await(v) (即 PromiseResolve(%Promise%, v)), 且
//   - PromiseResolve 的 Get(v, "constructor") 抛错 ⇒ 该抛出值在 yield 点抛出;
//   - Await 被拒 ⇒ 拒绝原因在 yield 点抛出;
//   - 兑现为 w ⇒ 以 return(w) 完成恢复体 (finally 等照常展开)。
// 前两条都是"回灌进体", 故体内的 catch 能接住并改写返回值 (Node v22 实测:
// `it.return(Promise.reject(new Error('X')))` 在 suspendedYield 下产出
// {value:1, done:true} 且 caught=X; 在 suspendedStart 下直接 reject X)。
func agReturnIntoSuspendedYield(g *object.AsyncGenerator, req *object.AsyncGenRequest, arg object.Value) {
	p, ok, thrown := agPromiseResolve(arg)
	if !ok {
		// PromiseResolve 抛错: 以原始抛出值 throw 进体 (体不被跳过)。
		agResumeSuspended(g, req, object.AGThrowKind, thrown)
		return
	}

	// 取 awaited 值, 但**绝不在回调帧里驱动 generator**。
	//
	// 为什么: Gox 没有微任务队列 —— 已结算的 promise 的 then 回调是同步跑
	// 的, 于是"在回调里恢复体"会让 VM 按**回调帧**的栈深换算挂起状态
	// (PendingTries 的 RelStackBase / RelFrameIdx, 以及 finally 展开期间挂
	// 起的 PendingVal)。实测后果: return-suspendedYield-try-finally.js 的
	// 第三跳应产出 'sent-value', 却退化成 undefined —— 挂起的 return 值在
	// 换算中丢了。故这里只**记录**结果, 回到 agStep 的本帧再驱动。
	//
	// 真正异步的 promise (回调在将来才触发) 没有本帧可用, 只能由回调自己
	// 驱动 —— 此时 syncPhase 已为假。
	var settled, isThrow bool
	var res object.Value
	syncPhase := true
	agAwait(p,
		func(v object.Value) {
			settled, res, isThrow = true, v, false
			if !syncPhase {
				agResumeSuspended(g, req, object.AGReturnKind, v)
			}
		},
		func(reason object.Value) {
			settled, res, isThrow = true, reason, true
			if !syncPhase {
				agResumeSuspended(g, req, object.AGThrowKind, reason)
			}
		})
	syncPhase = false
	if settled {
		if isThrow {
			agResumeSuspended(g, req, object.AGThrowKind, res)
		} else {
			agResumeSuspended(g, req, object.AGReturnKind, res)
		}
	}
}

// agResumeSuspended 从 yield 挂起点用 (kind, arg) 恢复已挂起的生成器体,
// 之后与 agStep 走同一套收尾 (完成 / await / yield)。
func agResumeSuspended(g *object.AsyncGenerator, req *object.AsyncGenRequest, kind int, arg object.Value) {
	value, done := agDrive(g, kind, arg)
	if cbErr := object.TakeCallbackError(); cbErr != nil {
		// 体内未接住: 抛出值作为 rejection (与 agStep 同款错误桥纪律)。
		g.Done = true
		agRejectBridge(req, cbErr)
		agFinish(g, req)
		return
	}
	agTail(g, req, value, done)
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

// agPromiseResolve 实现规范 **PromiseResolve(%Promise%, x)** (27.2.1.1):
//
//	1. If IsPromise(x) is true, then
//	   a. Let xConstructor be ? Get(x, "constructor").
//	   b. If SameValue(xConstructor, C) is true, return x.
//	2. Let promiseCapability be ? NewPromiseCapability(C).
//	3. Perform ? Call(promiseCapability.[[Resolve]], undefined, « x »).
//	4. Return promiseCapability.[[Promise]].
//
// 返回 (promise, ok, thrown):
//   - ok=true  : promise 是可用的结算源;
//   - ok=false : 步骤 1.a 的 Get 抛了错, thrown 是**原始抛出值** —— 调用方
//     据此走 AsyncGeneratorAwaitReturn 步骤 7 (以该值 reject)。
//
// 为什么单独开一个函数: 步骤 1.a 是 broken-promise 系列用例唯一的一条路径
// —— `Object.defineProperty(p, 'constructor', { get(){ throw … } })` 造出
// "取 constructor 会抛错" 的 promise, 规范要求 reject 那个抛出值。此前
// agAwait 看到 *Promise 就直接 .then(), 这一步压根没实现 (rj9MwH)。
func agPromiseResolve(x object.Value) (object.Value, bool, object.Value) {
	if p, ok := x.(*object.Promise); ok {
		ctor, _ := p.GetProperty("constructor")
		// getter 抛错: 取走原始抛出值 (与 CallPromiseHandler / agAwait 同款
		// 错误桥纪律 —— 保真 throw x 的 x 本身, 而不是 Go 错误字符串)。
		if cbErr := object.TakeCallbackError(); cbErr != nil {
			thrown := object.TakeCallbackErrorValue()
			if thrown == nil || thrown == object.UndefinedSingleton {
				thrown = object.NewErrorWithName("Error", cbErr.Error())
			}
			return nil, false, thrown
		}
		// SameValue(xConstructor, %Promise%) ⇒ 原样返回
		if ctor == promiseCtor {
			return p, true, nil
		}
	}
	// 非 Promise, 或 constructor 被换成了别的东西 ⇒ NewPromiseCapability + resolve。
	// Gox 的同步 Promise 模型下, resolve(x) 若 x 是 promise 会自行挂接。
	np := object.NewPromise()
	np.Resolve(x)
	return np, true, nil
}

// agAwait 按 await 语义处理一个值: Promise 等待其结算, thenable 调其
// then(onFulfilled, onRejected), 其余值立即透传。
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
	// thenable (Await 语义, 规范 Await/PromiseResolve 的 ThenableJob):
	// 有 callable then 的对象调 then(onFulfilled, onRejected)。Gox 的同步
	// Promise 模型 (结算即回调, 无微任务队列) 下用户 thenable 通常同步
	// 回调 —— 直接驱动; settled 守卫防 then 多次回调 (规范只认第一次)。
	if o, ok := value.(*object.Object); ok {
		if thenFn, has := o.GetProperty("then"); has && object.IsCallable(thenFn) {
			var settled bool
			fulfil := object.NewBuiltin("__ag_thenable_fulfil", func(args ...object.Value) object.Value {
				if !settled {
					settled = true
					onResolve(argAt(args, 0))
				}
				return object.UndefinedSingleton
			})
			reject := object.NewBuiltin("__ag_thenable_reject", func(args ...object.Value) object.Value {
				if !settled {
					settled = true
					onReject(argAt(args, 0))
				}
				return object.UndefinedSingleton
			})
			res := object.CallFunction(thenFn, value, fulfil, reject)
			if !settled {
				// then() 自身同步抛错 (可能抛任意值, 非 Error): 规范以该值
				// reject thenable。优先用原始抛出值 (callbackErrorValue),
				// 否则退化为 Go 错误字符串 —— 与其它错误桥路径同款纪律。
				if cbErr := object.TakeCallbackError(); cbErr != nil {
					settled = true
					if thrown := object.TakeCallbackErrorValue(); thrown != object.UndefinedSingleton {
						onReject(thrown)
					} else {
						onReject(object.NewErrorWithName("Error", cbErr.Error()))
					}
					return
				}
				if errObj, isErr := res.(*object.Error); isErr {
					settled = true
					onReject(errObj)
				}
			}
			return
		}
	}
	onResolve(value)
}
