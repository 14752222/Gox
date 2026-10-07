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
//     Call —— 用例断言 get throw 每次都出现;
//   - 步进结果原样交回外层 (OP_AWAIT/定时器泵负责解包), wrapper 不做
//     thenable 深解。
//
// 只对 *object.Object 包装; Generator / runtime.Iterator / JSIterator
// 形状无 JS getter 参与, 原样返回 (Generator 的委托本就走 genResume)。
func (vm *VM) wrapSyncIterForAsync(iter object.Value) object.Value {
	src, ok := iter.(*object.Object)
	if !ok {
		return iter
	}
	w := object.NewObject()
	// 标记源对象 (不可枚举): ASYNC_ITER_NEXT[_ARG] 据此做 next 懒缓存。
	w.DefineOwnProperty(wrapperSrcKey, object.PropertyDescriptor{
		Value: src, Writable: false, Enumerable: false, Configurable: true,
	})
	w.SetProperty("throw", object.NewBuiltin("throw", func(args ...object.Value) object.Value {
		fn, found := src.GetProperty("throw")
		if !found || fn == object.UndefinedSingleton || fn == object.NullSingleton {
			// 无 throw 方法 (规范 %AsyncFromSyncIteratorPrototype%.throw 步骤 7):
			// 先 AsyncIteratorClose(syncIterator) (若其有可调 return 则调用之,
			// 异常优先传播), 再以 TypeError reject。
			if rf, rfound := src.GetProperty("return"); rfound &&
				rf != object.UndefinedSingleton && rf != object.NullSingleton {
				if object.IsCallable(rf) {
					if _, err := vm.callFunction(rf, src, nil); err != nil {
						return errToValue(err)
					}
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
		return res
	}))
	w.SetProperty("return", object.NewBuiltin("return", func(args ...object.Value) object.Value {
		fn, found := src.GetProperty("return")
		if !found || fn == object.UndefinedSingleton || fn == object.NullSingleton {
			// 无 return 方法 (规范 %AsyncFromSyncIteratorPrototype%.return 步骤 7):
			// 迭代结束以 CreateIterResultObject(value, true) 结算 —— **value 是
			// 传入参数**, 不是 undefined。yield* 委托据此拿到 return(v) 的 v。
			v := object.Value(object.UndefinedSingleton)
			if len(args) > 0 {
				v = args[0]
			}
			return object.NewIteratorResult(v, true)
		}
		if !object.IsCallable(fn) {
			return object.NewErrorWithName("TypeError", "iterator return is not a function")
		}
		res, err := vm.callFunction(fn, src, args)
		if err != nil {
			return errToValue(err)
		}
		return res
	}))
	return w
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
