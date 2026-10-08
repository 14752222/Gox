package vm

import (
	"errors"

	"github.com/14752222/Gox/object"
)

// wrapperSrcKey 是 Async-from-Sync wrapper 上标记「被委托同步迭代器
// 源对象」的自有属性名 (不可枚举)。ASYNC_ITER_NEXT / _ARG 的对象分支
// 据此把 next method 懒取一次并缓存为 own 数据属性 —— 缓存的是 JS 函数
// 本身, 因此后续调用走 callClosure 正常路径, 用户 throw 的**任意值**
// 都能作为 reject reason 传播 (builtin 返回值协议只携带 *object.Error)。
const wrapperSrcKey = "__async_from_sync_src__"

// wrapperFromSyncKey 标记该 wrapper 包装的是**同步**可迭代对象 (经
// CreateAsyncFromSyncIterator)。只有这种 wrapper 需要在 yield* 委托时把
// 步进结果的 value 按 PromiseResolve 解包 (%AsyncFromSyncIteratorPrototype%
// .next 步骤 14: valueWrapper = PromiseResolve(value)) —— 原生 @@asyncIterator
// 的值**不**解包 (yield-star-promise-not-unwrapped)。
const wrapperFromSyncKey = "__async_from_sync_value_await__"

// wrapSyncIterForAsync 按 CreateAsyncFromSyncIterator + %AsyncFromSync%
// IteratorPrototype% 语义, 把「同步可迭代产出」的 JS 迭代器对象包成异步
// 委托 (yield*) / for-await 消费专用的 wrapper。
//
// 关键属性访问序 (test262 yield-star-sync-next/async-throw 断言, Node/V8
// 实测一致):
//   - next method 懒取一次并缓存 (own 数据属性) —— V8 中委托迭代器的
//     next 第二次起不再触发 get next (同步委托与原生 @@asyncIterator
//     都一样); 缓存在首次步进时由 ASYNC_ITER_NEXT[_ARG] 完成 (见 vm.go);
//   - own "throw"/"return" 是 builtin, 内部每次 GetMethod(src) +
//     Call —— 用例断言 get throw 每次都出现; 同步源 (fromSync) 时结果按
//     %AsyncFromSyncIteratorPrototype% 语义把 value PromiseResolve 后再交回
//     (原生 @@asyncIterator 与裸 next 对象不解包);
//   - next 的步进结果由 ASYNC_ITER_NEXT[_ARG] 侧统一解包 (见 asyncFromSyncAwaitedStep)。
//
// 只对 *object.Object 包装; Generator / runtime.Iterator / JSIterator
// 形状无 JS getter 参与, 原样返回 (Generator 的委托本就走 genResume)。
func (vm *VM) wrapSyncIterForAsync(iter object.Value, fromSync bool) object.Value {
	src, ok := iter.(*object.Object)
	if !ok {
		return iter
	}
	w := object.NewObject()
	// 标记源对象 (不可枚举): ASYNC_ITER_NEXT[_ARG] 据此做 next 懒缓存。
	w.DefineOwnProperty(wrapperSrcKey, object.PropertyDescriptor{
		Value: src, Writable: false, Enumerable: false, Configurable: true,
	})
	// 标记是否同步源 (决定 yield* 委托是否解包步进值)。
	w.DefineOwnProperty(wrapperFromSyncKey, object.PropertyDescriptor{
		Value: object.NewBoolean(fromSync), Writable: false, Enumerable: false, Configurable: true,
	})
	w.SetProperty("throw", object.NewBuiltin("throw", func(args ...object.Value) object.Value {
		fn, found := src.GetProperty("throw")
		// get throw 是抛错的访问器: 原始抛出值优先 (规范 GetMethod 直接抛出),
		// 不得退化成 "does not provide a 'throw' method" 的 TypeError。
		if v, ok := vm.callbackErrAsThrownValue(); ok {
			return v
		}
		if !found || fn == object.UndefinedSingleton || fn == object.NullSingleton {
			// 无 throw 方法 (规范 %AsyncFromSyncIteratorPrototype%.throw 步骤 7):
			// 先 AsyncIteratorClose(syncIterator) (若其有可调 return 则调用之,
			// 异常优先传播), 再以 TypeError reject。
			rf, rfound := src.GetProperty("return")
			if v, ok := vm.callbackErrAsThrownValue(); ok {
				return v
			}
			if rfound && rf != object.UndefinedSingleton && rf != object.NullSingleton &&
				object.IsCallable(rf) {
				if _, err := vm.callFunction(rf, src, nil); err != nil {
					return errToValue(err)
				}
			}
			return object.NewErrorWithName("TypeError", "iterator does not provide a 'throw' method")
		}
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "iterator throw is not a function")
		}
		res, err := vm.callFunction(fn, src, args)
		if err != nil {
			return errToValue(err)
		}
		// 同步源: 结果按 %AsyncFromSyncIteratorPrototype%.throw 步骤 10-14
		// 结算 (value 须 PromiseResolve), 由调用方 (yield* 的 throw 段) AWAIT。
		if fromSync {
			return vm.asyncFromSyncAwaitedStep(res)
		}
		return res
	}))
	w.SetProperty("return", object.NewBuiltin("return", func(args ...object.Value) object.Value {
		fn, found := src.GetProperty("return")
		// get return 是抛错的访问器: 原始抛出值优先 (规范 GetMethod 直接抛出)。
		if v, ok := vm.callbackErrAsThrownValue(); ok {
			return v
		}
		if !found || fn == object.UndefinedSingleton || fn == object.NullSingleton {
			// 无 return 方法 (规范 %AsyncFromSyncIteratorPrototype%.return 步骤 7):
			// 迭代结束以 CreateIterResultObject(value, true) 结算 —— **value 是
			// 传入参数**, 不是 undefined。yield* 委托据此拿到 return(v) 的 v。
			v := object.Value(object.UndefinedSingleton)
			if len(args) > 0 {
				v = args[0]
			}
			res := object.Value(object.NewIteratorResult(v, true))
			if fromSync {
				// 规范同款: IteratorClose 的 normal 完成值也要 PromiseResolve。
				return vm.asyncFromSyncAwaitedStep(res)
			}
			return res
		}
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "iterator return is not a function")
		}
		res, err := vm.callFunction(fn, src, args)
		if err != nil {
			return errToValue(err)
		}
		// 同步源: 结果按 %AsyncFromSyncIteratorPrototype%.return 步骤 10-16
		// 结算 (value 须 PromiseResolve), 由调用方 (yield* 的 return 段) AWAIT。
		if fromSync {
			return vm.asyncFromSyncAwaitedStep(res)
		}
		return res
	}))
	return w
}

// asyncFromSyncAwaitedStep 实现 %AsyncFromSyncIteratorPrototype%.next 的
// 「值须 PromiseResolve」语义 (步骤 9-14): 拆出同步迭代器产出 step 的
// done/value (done 先读, 与 IteratorComplete→IteratorValue 同序), 把 value
// 解包成 promise, 返回一个结算为 { value: resolvedValue, done } 的 promise。
//
// 仅 yield* 委托**同步**可迭代时使用 (native @@asyncIterator 的值不解包)。
// done 为真也照样解包 (规范对 done 无例外)。
func (vm *VM) asyncFromSyncAwaitedStep(step object.Value) object.Value {
	o, ok := step.(*object.Object)
	if !ok {
		// 非对象 (含 promise): 原样交回, 由外层 AWAIT 处理。
		return step
	}
	firstArg := func(args []object.Value) object.Value {
		if len(args) > 0 {
			return args[0]
		}
		return object.UndefinedSingleton
	}
	doneV, _ := o.GetProperty("done")
	if err := vm.checkCallbackErr(); err != nil {
		return errToValue(err)
	}
	valV, _ := o.GetProperty("value")
	if err := vm.checkCallbackErr(); err != nil {
		return errToValue(err)
	}
	done := doneV.IsTruthy()
	// 规范步骤 14 (next/return/throw 三处同款): valueWrapper =
	// PromiseResolve(%Promise%, value) —— 与 done 无关。done 为真时同样要
	// PromiseResolve: %AsyncFromSyncIteratorPrototype% 一律以
	// CreateIterResultObject(resolvedValue, done) 结算, 故 `yield* 同步迭代器`
	// 的表达式值 (done 那一步的 value) 也是已解包的值。
	p := object.NewPromise()
	p.Resolve(valV) // PromiseResolve 语义: promise/thenable 值被采纳 (reject 会传染)
	out := object.NewPromise()
	p.Then(object.NewBuiltin("__afs_value", func(args ...object.Value) object.Value {
		out.Resolve(object.NewIteratorResult(firstArg(args), done))
		return object.UndefinedSingleton
	}))
	p.Catch(object.NewBuiltin("__afs_value_err", func(args ...object.Value) object.Value {
		out.Reject(firstArg(args))
		return object.UndefinedSingleton
	}))
	return out
}

// wrapperIsFromSync 报告该 Async-from-Sync wrapper 是否包装同步可迭代对象
// (决定 yield* 委托是否解包步进值)。
func (vm *VM) wrapperIsFromSync(w *object.Object) bool {
	v, ok := w.GetProperty(wrapperFromSyncKey)
	if !ok {
		return false
	}
	return v.IsTruthy()
}

// syncWrapperNext 取 Async-from-Sync wrapper 的 next method 及其 this
// (源同步迭代器): own 已缓存 (首次步进后) 或从源对象懒取一次并写回
// wrapper 的 own 数据属性。
//
// this 用源对象而非 wrapper: 规范 %AsyncFromSyncIteratorPrototype%.next
// 是 Call(nextMethod, syncIterator, «value») —— test262 断言 call next
// 的 thisValue.name === "syncIterator"。缓存的是 JS 函数本身, 因此用户
// throw 的任意值经 callClosure 正常传播为 reject reason (不会像 builtin
// 返回值协议那样降级)。
func (vm *VM) syncWrapperNext(w *object.Object) (fn, src object.Value, ok bool) {
	srcVal, hasSrc := w.GetProperty(wrapperSrcKey)
	if !hasSrc {
		return nil, nil, false
	}
	srcObj, isObj := srcVal.(*object.Object)
	if !isObj {
		return nil, nil, false
	}
	nf, found := w.GetProperty("next") // own 缓存优先 (第二次起)
	if !found {
		nf, found = srcObj.GetProperty("next")
		if !found || !object.IsCallable(nf) {
			return nil, nil, false
		}
		w.SetProperty("next", nf) // 懒缓存: 第二次起不再触发 src 的 next getter
	}
	if !object.IsCallable(nf) {
		return nil, nil, false
	}
	return nf, srcObj, true
}

// callbackErrAsThrownValue 把内建函数体内 GetProperty 触发的回调桥错误按
// **抛出值类型**分流 —— 两个通道的可表达范围不同, 不能一律走同一条:
//
//   - 抛出值确为 *object.Error: 走内建函数**返回值**通道 (由 throwIfError
//     识别并抛出), 桥信号随之消费。test262 AsyncFromSyncIteratorPrototype
//     的 poisoned-get-throw 一族靠它拿到原始抛出值。
//   - 其余任意 JS 值 (普通对象 / 原始值, test262 惯用
//     `throw { name: "inner error" }`): 返回值通道**不认**非 Error 值 ——
//     errToValue 会把它当正常返回值, 于是「该 reject」退化成「正常完成」
//     (test262 for-await-of/iterator-close-non-throw-get-method-abrupt)。
//     这类值改为**原样退回桥信号**(错误槽 + 值槽一并还原, 两槽缺一不可),
//     由外层异步驱动按原始抛出值 reject。
//
// 返回值 ok=true 表示调用方应 `return v`; ok=false 表示调用方按原有正常
// 路径继续 (桥信号仍在, 不得当作「无错误」)。
func (vm *VM) callbackErrAsThrownValue() (object.Value, bool) {
	err := vm.checkCallbackErr()
	if err == nil {
		return object.UndefinedSingleton, false
	}
	v := errToValue(err)
	if _, ok := v.(*object.Error); ok {
		object.TakeCallbackErrorValue() // 值槽同步清空, 两槽保持一致
		return v, true
	}
	object.SetCallbackError(err)
	object.SetCallbackErrorValue(v)
	return object.UndefinedSingleton, false
}

// errToValue 把 VM 调用返回的 Go error 转成可沿 builtin 返回值传播的
// object.Value (*object.Error), 供 vm.throwIfError 检出并走 JS 抛出流程。
// *ThrowError 保留原始抛出值 (可能是任意 JS 值 —— throwIfError 只认
// *object.Error, 非 Error 实例会降级为普通返回值, 已知限制)。
func errToValue(err error) object.Value {
	var te *ThrowError
	if errors.As(err, &te) {
		return te.Value
	}
	return object.NewErrorWithName("Error", err.Error())
}
