package stdlib

import (
	"js-runtime/object"
	"js-runtime/runtime"
)

// setupObs 注册 Dart GetX 风格的响应式 API。
//
//	obs(0)        → Rx 标量:  .value 读写, .listen(fn), .refresh(), .close()
//	obs([1,2])    → RxList:   push/pop/... 触发通知, 索引读写触发通知
//	obs(new Map)  → RxMap:    set/delete/clear 触发通知
//	computed(fn)  → 计算属性: 自动追踪 fn 内读取的 Rx 单元, 依赖变化时失效
//	ever(rx, fn)  → 每次 rx 变化调用 fn (GetX worker)
//	once(rx, fn)  → 仅第一次变化调用
//
// 通知语义与 GetX 一致: 新旧值相等时不通知; listen 立即以当前值回调
// 一次 (BehaviorSubject); 订阅返回 {close()} 取消订阅。
func setupObs(env *runtime.Environment) {
	obsFn := object.NewBuiltin("obs", func(args ...object.Value) object.Value {
		var v object.Value = object.UndefinedSingleton
		if len(args) > 0 {
			v = args[0]
		}
		switch src := v.(type) {
		case *object.Array:
			return object.NewObservableList(src)
		case *object.Map:
			return object.NewObservableMap(src)
		case *object.Observable, *object.ObservableList, *object.ObservableMap, *object.Computed:
			// 已是 Rx 单元: 原样返回 (GetX .obs 幂等语义)
			return src
		}
		return object.NewObservable(v)
	})
	obsFn.SetProperty("name", object.NewString("obs"))
	env.Declare("obs", obsFn, false)

	// computed(fn): 自动依赖追踪的计算属性
	env.Declare("computed", object.NewBuiltin("computed", func(args ...object.Value) object.Value {
		if len(args) == 0 || !object.IsCallable(args[0]) {
			return object.NewTypeError("computed: fn must be a function")
		}
		return object.NewComputed(args[0])
	}), false)

	// ever(rx, fn): 每次变化回调 (订阅即返回取消订阅对象)
	env.Declare("ever", object.NewBuiltin("ever", func(args ...object.Value) object.Value {
		rx, fn, errVal := rxWorkerArgs("ever", args)
		if errVal != nil {
			return errVal
		}
		return rx.RxSubscribe(fn)
	}), false)

	// once(rx, fn): 仅首次变化回调
	env.Declare("once", object.NewBuiltin("once", func(args ...object.Value) object.Value {
		rx, fn, errVal := rxWorkerArgs("once", args)
		if errVal != nil {
			return errVal
		}
		var sub object.Value
		var fired bool
		wrapper := object.NewBuiltin("__once", func(wargs ...object.Value) object.Value {
			if fired {
				return object.UndefinedSingleton
			}
			fired = true
			// 取消后续订阅
			if sub != nil {
				if so, ok := sub.(*object.Object); ok {
					if cv, found := so.GetProperty("close"); found && object.IsCallable(cv) {
						object.CallFunction(cv, so)
					}
				}
			}
			nv := object.Value(object.UndefinedSingleton)
			if len(wargs) > 0 {
				nv = wargs[0]
			}
			ov := nv
			if len(wargs) > 1 {
				ov = wargs[1]
			}
			return object.CallFunction(fn, nil, nv, ov)
		})
		sub = rx.RxSubscribe(wrapper)
		return sub
	}), false)
}

// rxWorkerArgs 校验 worker (ever/once) 参数: (Rx 单元, 回调)。
func rxWorkerArgs(name string, args []object.Value) (object.ObservableState, object.Value, object.Value) {
	if len(args) < 2 || !object.IsCallable(args[1]) {
		return nil, nil, object.NewTypeError("%s: (listener, callback) required", name)
	}
	switch rx := args[0].(type) {
	case object.ObservableState:
		return rx, args[1], nil
	case *object.Observable:
		return rx, args[1], nil
	}
	return nil, nil, object.NewTypeError("%s: first argument must be an Rx unit (obs/computed)", name)
}
