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

	// yield: 按 AsyncGeneratorYield 语义结算 —— **不 await 值本身**
	// (规范 AsyncGeneratorYield 直接 AsyncGeneratorResolve(generator, value,
	// false); 只有体内显式 `yield await x` 才先 await)。若在此 await 值,
	// `yield somePromise` 会把 promise 解包, 破坏
	// test262 yield-star-promise-not-unwrapped (手动实现的 async 迭代器
	// 产出 promise 时不得解包)。
	req.Promise.Resolve(newAsyncIterResult(value, false))
	agFinish(g, req)
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
