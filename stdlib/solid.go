package stdlib

import (
	"fmt"
	"os"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/runtime"
)

// setupSolid 注册 SolidJS 风格的细粒度响应式 API, 并以内置模块 "gx/solid" 暴露:
//
//	import { createSignal, createEffect, createMemo } from "gx/solid";
//
// 语义对齐 Solid:
//   - createSignal(init) → [getter, setter]。setter 支持 set(v) 与 set(prev => v)
//     两种形式; 新旧值 === 相同则不通知订阅者。
//   - createEffect(fn) 立即执行一次 fn。执行期间调用过的 getter 自动成为依赖,
//     依赖变化时重新执行; 每轮执行后重新收集依赖 (上轮不再被引用的依赖退订)。
//     返回 dispose 函数, 调用后注销该 effect。
//   - createMemo(fn) 惰性计算属性: 依赖变化只标脏, 下次读取 getter 时才重算;
//     读取 memo getter 的一方同样会被追踪为依赖。
//
// 实现说明: 依赖收集用包级"正在运行的观察者"栈, 全程在 VM 单线程内执行,
// 无需加锁。effect 运行中再次触发依赖变更通过 pending 标记合并, 避免同轮
// 递归重跑。观察者抛出的异常会冒泡到触发通知的 setter / 读取处抛出
// (v1 没有 ErrorBoundary, 在报告中注明与 Solid 的这一差异)。
func setupSolid(env *runtime.Environment) {
	object.RegisterBuiltinModule("gx/solid", func() map[string]object.Value {
		return map[string]object.Value{
			"createSignal":   object.NewBuiltin("createSignal", solidCreateSignal),
			"createEffect":   object.NewBuiltin("createEffect", solidCreateEffect),
			"createMemo":     object.NewBuiltin("createMemo", solidCreateMemo),
			"createResource": object.NewBuiltin("createResource", solidCreateResource),
			"onMount":        object.NewBuiltin("onMount", solidOnMount),
			"onCleanup":      object.NewBuiltin("onCleanup", solidOnCleanup),
			"devStats":       object.NewBuiltin("devStats", solidDevStats),
		}
	})
}

// solidFrame 是 JSX 之外本模块的观察者: effect 的回调, 或 memo 的计算函数。
type solidObserver struct {
	fn       object.Value // 被追踪的 JS 函数
	deps     map[*solidSignal]struct{}
	running  bool // 正在执行 (重入保护: 期间依赖再变只记 pending)
	pending  bool // 执行期间依赖又变化, 需补跑一轮
	disposed bool
	isMemo   bool

	// memo 专有: 值挂在 cell 上, 下游观察者订阅 cell 而不是 memo 本身
	cell  *solidSignal
	dirty bool
}

// solidSignal 是一个可订阅的值单元: signal 的存储槽, 或 memo 对外的槽位。
type solidSignal struct {
	value       object.Value
	subscribers map[*solidObserver]struct{}
}

var (
	// solidStack 正在执行的观察者栈, 栈顶即当前依赖收集目标
	solidStack []*solidObserver
	// solidNotifyDepth / solidChainRuns 限制单次 setter 触发的级联重跑
	// 深度, 防止 effect 互相触发的死循环
	solidNotifyDepth int
	solidChainRuns   int
)

const solidMaxChainRuns = 1000

// ===== createSignal =====

func solidCreateSignal(args ...object.Value) object.Value {
	var init object.Value = object.UndefinedSingleton
	if len(args) > 0 {
		init = args[0]
	}
	_, getter, setter := newSignalPair(init)
	return object.NewArray([]object.Value{getter, setter})
}

// newSignalPair 造一对 (signal, getter, setter)。createResource 要在 Go 侧
// 直接持有 signal 槽位, 抽出这个共用构造。
func newSignalPair(init object.Value) (*solidSignal, object.Value, object.Value) {
	sig := &solidSignal{
		value:       init,
		subscribers: map[*solidObserver]struct{}{},
	}
	getter := object.NewBuiltin("signal", func(args ...object.Value) object.Value {
		// 依赖收集: 只有正在运行的观察者才记录此 signal
		if n := len(solidStack); n > 0 {
			obs := solidStack[n-1]
			if obs.deps == nil {
				obs.deps = map[*solidSignal]struct{}{}
			}
			obs.deps[sig] = struct{}{}
		}
		return sig.value
	})
	setter := object.NewBuiltin("setSignal", func(args ...object.Value) object.Value {
		return solidSignalSet(sig, args...)
	})
	return sig, getter, setter
}

// solidSignalSet 实现 setter: set(v) / set(prev => v), 值未变不通知。
// 通知期间观察者抛出的第一个异常作为返回值 (内建返回 *Error 会向上抛出)。
func solidSignalSet(sig *solidSignal, args ...object.Value) object.Value {
	var next object.Value = object.UndefinedSingleton
	if len(args) > 0 {
		next = args[0]
		// 单个函数参数按 Solid 语义视为更新函数: set(prev => v)。
		// (信号本身存函数需要包一层: set(() => () => v), 与 Solid 一致)
		if object.IsCallable(next) {
			next = object.CallFunction(next, nil, sig.value)
			if err := solidCallbackError(); err != nil {
				return err
			}
		}
	}
	if solidStrictEquals(sig.value, next) {
		return next
	}
	sig.value = next

	if solidNotifyDepth == 0 {
		solidChainRuns = 0
	}
	solidNotifyDepth++
	err := solidNotify(sig)
	solidNotifyDepth--
	return err
}

// solidCallbackError 把最近一次 CallFunction 的 JS 异常转成 Error 值;
// 无异常时返回 nil。必须同时消费 TakeCallbackError (Go 侧错误信号) 与
// TakeCallbackErrorValue (原始抛出值), 残留的 callbackError 会在之后
// 的内建调用点被 VM 误当异常抛出 (见 vm.go callbackErr 注释)。
func solidCallbackError() object.Value {
	if err := object.TakeCallbackError(); err != nil {
		if v := object.TakeCallbackErrorValue(); v != object.UndefinedSingleton {
			if e, ok := v.(*object.Error); ok {
				return e
			}
		}
		return object.NewErrorWithName("Error", err.Error())
	}
	return nil
}

// solidNotify 通知 signal 的所有订阅者, 返回首个观察者异常。
func solidNotify(sig *solidSignal) object.Value {
	if len(sig.subscribers) == 0 {
		return nil
	}
	// 快照后遍历: 重跑过程中订阅关系可能增删
	observers := make([]*solidObserver, 0, len(sig.subscribers))
	for obs := range sig.subscribers {
		observers = append(observers, obs)
	}
	var firstErr object.Value
	for _, obs := range observers {
		if err := solidRefresh(obs); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// solidRefresh 依赖变更唤醒观察者:
// effect → 立即重跑; memo → 只标脏并通知下游 (惰性重算)。
func solidRefresh(obs *solidObserver) object.Value {
	if obs.disposed {
		return nil
	}
	if obs.isMemo {
		obs.dirty = true
		return solidNotify(obs.cell)
	}
	return solidExecute(obs)
}

// solidExecute 执行观察者函数并同步订阅关系 (effect 重跑 / memo 重算共用)。
func solidExecute(obs *solidObserver) object.Value {
	if obs.disposed {
		return nil
	}
	if obs.running {
		obs.pending = true
		return nil
	}
	// 只对"重跑"计数: 级联上限是为了拦住 effect 互相触发形成的死循环,
	// 而首次执行 (deps 为空) 不可能是循环的一环。若把首次执行也计入,
	// 一次构建上千个节点 (列表渲染) 就会误触发上限保护。
	if obs.deps != nil {
		solidChainRuns++
		if solidChainRuns > solidMaxChainRuns {
			return object.NewErrorWithName("Error", "solid: effect cascade exceeded limit (possible infinite loop)")
		}
	}

	obs.running = true
	prevDeps := obs.deps
	obs.deps = nil
	solidStack = append(solidStack, obs)

	var runErr object.Value
	if obs.isMemo {
		obs.dirty = false
		result := object.CallFunction(obs.fn, nil)
		if err := solidCallbackError(); err != nil {
			runErr = err
		} else {
			obs.cell.value = result
		}
	} else {
		object.CallFunction(obs.fn, nil)
		runErr = solidCallbackError()
	}

	solidStack = solidStack[:len(solidStack)-1]
	obs.running = false

	// 同步订阅关系: 上轮有、本轮无 → 退订; 本轮依赖 → 建立/保持订阅
	newDeps := obs.deps
	if newDeps == nil {
		newDeps = map[*solidSignal]struct{}{}
		obs.deps = newDeps
	}
	for sig := range prevDeps {
		if _, ok := newDeps[sig]; !ok {
			delete(sig.subscribers, obs)
		}
	}
	for sig := range newDeps {
		if sig.subscribers == nil {
			sig.subscribers = map[*solidObserver]struct{}{}
		}
		sig.subscribers[obs] = struct{}{}
	}

	// 执行期间依赖又变化 → 补跑一轮
	if obs.pending {
		obs.pending = false
		if err := solidExecute(obs); err != nil && runErr == nil {
			runErr = err
		}
	}
	return runErr
}

// ===== createEffect =====

// solidEffectsLive 是当前存活的 effect 数 (devStats 用, 泄漏排查从"有没有"开始)。
// 只统计"拿到了 dispose"的 effect: 首次执行即抛错的那类拿不到 dispose,
// 无法注销, 计入只会让数字单调虚高。
var solidEffectsLive int

func solidCreateEffect(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("createEffect: fn must be a function")
	}
	obs := &solidObserver{fn: args[0]}
	dispose := object.NewBuiltin("disposeEffect", func(args ...object.Value) object.Value {
		obs.disposed = true
		for sig := range obs.deps {
			delete(sig.subscribers, obs)
		}
		obs.deps = nil
		if solidEffectsLive > 0 {
			solidEffectsLive--
		}
		return object.UndefinedSingleton
	})
	if err := solidExecute(obs); err != nil {
		return err
	}
	solidEffectsLive++
	return dispose
}

// ===== createMemo =====

func solidCreateMemo(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("createMemo: fn must be a function")
	}
	obs := &solidObserver{
		fn:     args[0],
		isMemo: true,
		cell: &solidSignal{
			value:       object.UndefinedSingleton,
			subscribers: map[*solidObserver]struct{}{},
		},
	}
	if err := solidExecute(obs); err != nil {
		return err
	}
	return object.NewBuiltin("memo", func(args ...object.Value) object.Value {
		// 惰性重算: 依赖变化只是标脏, 读取时才重新执行
		if obs.dirty {
			if err := solidExecute(obs); err != nil {
				return err
			}
		}
		// 下游依赖追踪挂在 cell 上, 与 signal getter 同一机制
		if n := len(solidStack); n > 0 {
			reader := solidStack[n-1]
			if reader.deps == nil {
				reader.deps = map[*solidSignal]struct{}{}
			}
			reader.deps[obs.cell] = struct{}{}
		}
		return obs.cell.value
	})
}

// ===== createResource (状态管理方案 B, 2026-09-19 拍板落地) =====

// solidCreateResource 是 Solid 风格的异步资源:
//
//	const [data, res] = createResource(fetcher);
//	data()        → undefined (pending) | 值 | 上一次的值 (refreshing / error)
//	res.state()   → "pending" | "ready" | "refreshing" | "error"
//	res.error()   → 错误值 (仅 error 态有值, 其余 undefined)
//	res.refetch() → 重取 (保留旧值显示, 即 refreshing)
//
// (Solid 的 `const [data, { refetch }] = ...` 嵌套解构在本引擎不可用 ——
// parser 不支持数组解构里嵌对象模式, 所以 controls 作为第二个元素取出来用。)
//
// 与 Solid 的刻意差异 (v1 减法, **属公共 API 承诺**, 详见 docs/gui-patterns.md):
//   - state/error 不挂在 data 函数上 (本引擎函数值不带属性), 放进返回的
//     controls 元素, 三者都是可追踪的 signal getter;
//   - error 态的 data() **不抛异常** (v1 无 ErrorBoundary, 抛了会冒泡进任意
//     effect, 炸得没有上下文): 返回上一次的值 (从未成功过则 undefined),
//     错误只从 res.error() 读;
//   - fetcher 返回非 Promise (同步值) 时按"立即可用"处理;
//   - 不做 source signal 自动重取 (Solid 的二参形态), 需要联动用
//     createEffect 手动串。
//
// latest-wins: 每次取数递增 token, 回来时 token 不符直接丢弃 —— 快速连续
// refetch 时旧响应不会覆盖新状态, 竞态在结构上不可能。
func solidCreateResource(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("createResource: fetcher must be a function")
	}
	fetcher := args[0]

	valueSig, valueGet, _ := newSignalPair(object.UndefinedSingleton)
	stateSig, stateGet, _ := newSignalPair(object.NewString("pending"))
	errSig, errGet, _ := newSignalPair(object.UndefinedSingleton)
	hasValue := false
	token := 0

	// start 发起一次取数 (createResource 与 refetch 共用)。返回值只可能是
	// nil 或 *object.Error (订阅者抛出的第一个异常, 沿 signal setter 语义
	// 传给发起方) —— 注意 solidSignalSet 的裸返回值**不是**错误通道:
	// 值未变时它返回新值本身, 必须经 errOnly 过滤。
	start := func() object.Value {
		token++
		my := token
		var firstErr object.Value
		note := func(res object.Value) {
			if e, ok := res.(*object.Error); ok && firstErr == nil {
				firstErr = e
			}
		}
		if hasValue {
			note(solidSignalSet(stateSig, object.NewString("refreshing")))
		} else {
			note(solidSignalSet(stateSig, object.NewString("pending")))
		}
		result := object.CallFunction(fetcher, nil)
		if e := solidCallbackError(); e != nil {
			// fetcher 同步抛出 = 一次失败 (资源创建本身不炸, 错误进 error())
			if my == token {
				solidSignalSet(errSig, e)
				note(solidSignalSet(stateSig, object.NewString("error")))
			}
			return firstErr
		}
		if p, ok := result.(*object.Promise); ok {
			p.OnFulfilled(object.NewBuiltin("__resource_ok", func(args ...object.Value) object.Value {
				if my != token {
					return object.UndefinedSingleton // latest-wins: 过期响应丢弃
				}
				hasValue = true
				solidSignalSet(valueSig, callbackArg(args))
				solidSignalSet(errSig, object.UndefinedSingleton)
				solidSignalSet(stateSig, object.NewString("ready"))
				return object.UndefinedSingleton
			}))
			p.OnRejected(object.NewBuiltin("__resource_fail", func(args ...object.Value) object.Value {
				if my != token {
					return object.UndefinedSingleton
				}
				solidSignalSet(errSig, callbackArg(args))
				solidSignalSet(stateSig, object.NewString("error"))
				return object.UndefinedSingleton
			}))
			return firstErr
		}
		// 同步 fetcher: 包装成立即可用的资源
		hasValue = true
		solidSignalSet(valueSig, result)
		note(solidSignalSet(stateSig, object.NewString("ready")))
		return firstErr
	}
	if err := start(); err != nil {
		return err
	}

	data := object.NewBuiltin("resource", func(args ...object.Value) object.Value {
		// 经 getter 读值以走依赖收集 (在 effect 里读 data() 才会订阅更新)
		return object.CallFunction(valueGet, nil)
	})
	controls := object.NewObject()
	controls.SetProperty("refetch", object.NewBuiltin("refetch", func(args ...object.Value) object.Value {
		return start()
	}))
	controls.SetProperty("state", stateGet)
	controls.SetProperty("error", errGet)
	return object.NewArray([]object.Value{data, controls})
}

// callbackArg 取回调首参 (无参时 undefined)。
func callbackArg(args []object.Value) object.Value {
	if len(args) > 0 {
		return args[0]
	}
	return object.UndefinedSingleton
}

// ===== onMount / onCleanup (生命周期, 2026-09-19 拍板与 createResource 同批) =====
//
// 语义: 登记到**当前正在构建的响应式子树** (条件/列表渲染的 getter 求值期)。
// 子树挂上后 onMount 逐个执行; 子树被替换或销毁时 onCleanup 逆序执行。
// 机制: gfx 在 wireReactiveChild 期间压入接线作用域 (object/wiring.go),
// 这里读栈顶登记 —— 两个包经 object 汇合, 互不依赖。
//
// v1 边界 (文档写明): 只在响应式子树内有意义; 顶层脚本直接调用是 no-op
// (打一次警告)。列表渲染里同一代多个组件的登记在同一批执行, 粒度是"代"
// 而不是"组件实例"。

var lifecycleOutsideWarned bool

func warnLifecycleOutside(name string) {
	if lifecycleOutsideWarned {
		return
	}
	lifecycleOutsideWarned = true
	fmt.Fprintf(os.Stderr,
		"solid: %s outside a reactive child is a no-op (it only works inside components rendered by conditional/list rendering)\n", name)
}

func solidOnMount(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("onMount: fn must be a function")
	}
	sc := object.CurrentWiringScope()
	if sc == nil {
		warnLifecycleOutside("onMount")
		return object.UndefinedSingleton
	}
	sc.Mounts = append(sc.Mounts, args[0])
	return object.UndefinedSingleton
}

func solidOnCleanup(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("onCleanup: fn must be a function")
	}
	sc := object.CurrentWiringScope()
	if sc == nil {
		warnLifecycleOutside("onCleanup")
		return object.UndefinedSingleton
	}
	sc.Cleanups = append(sc.Cleanups, args[0])
	return object.UndefinedSingleton
}

// ===== devStats (gx/dev 快照的数据源之一, 字段名不稳定) =====

func solidDevStats(args ...object.Value) object.Value {
	out := object.NewObject()
	out.SetProperty("effects", object.NewNumber(float64(solidEffectsLive)))
	return out
}

// solidStrictEquals 实现 JS 的 === 语义 (用于变更通知判定):
// 原始类型比值, 其余类型比引用。
func solidStrictEquals(a, b object.Value) bool {
	switch x := a.(type) {
	case *object.Number:
		y, ok := b.(*object.Number)
		return ok && x.Value == y.Value
	case *object.String:
		y, ok := b.(*object.String)
		return ok && x.Value == y.Value
	case *object.Boolean:
		y, ok := b.(*object.Boolean)
		return ok && x.Value == y.Value
	case *object.Null:
		_, ok := b.(*object.Null)
		return ok
	case *object.Undefined:
		_, ok := b.(*object.Undefined)
		return ok
	case *object.BigInt:
		y, ok := b.(*object.BigInt)
		return ok && x.Value.Cmp(y.Value) == 0
	}
	return a == b
}
