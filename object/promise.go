package object

import (
	"fmt"
	"sync"
)

// PromiseState 表示 Promise 的状态。
type PromiseState int

const (
	PromisePending   PromiseState = 0
	PromiseFulfilled PromiseState = 1
	PromiseRejected  PromiseState = 2
)

// Promise 表示 JavaScript 的 Promise 对象。
type Promise struct {
	mu               sync.Mutex
	State            PromiseState
	Value            Value             // fulfilled 时的值
	Reason           Value             // rejected 时的原因
	ThenCallbacks    []PromiseCallback // then 回调队列
	CatchCallbacks   []PromiseCallback // catch 回调队列
	FinallyCallbacks []PromiseCallback // finally 回调队列

	// PropDescs 承载 defineProperty / 赋值落在实例上的自有属性描述符。
	//
	// 为什么需要它: Promise 此前不是一等对象 —— GetProperty 只认
	// then/catch/finally, SetProperty 是 no-op。于是
	// `Object.defineProperty(p, 'constructor', { get(){ throw … } })` 落不
	// 下去也取不到, 规范里"取 constructor 抛错 ⇒ reject"这条路径根本走不到
	// (built-ins/AsyncGeneratorPrototype/return/*broken-promise* 3 例, rj9MwH)。
	//
	// 与 RegExp 的差别: Promise 没有规范自带的实例字段属性 (不像 lastIndex),
	// 所以这里只装**用户后加**的键 —— 缺省为空, 不影响既有行为。
	PropDescs map[string]PropertyDescriptor
	// propKeyOrder 记录自有键的创建顺序 (OwnKeys 口径)。
	propKeyOrder []string
}

// PromiseCallback 存储回调函数和创建的后续 Promise。
type PromiseCallback struct {
	Callback    Value    // 回调函数
	NextPromise *Promise // 链式调用的下一个 Promise
	IsCatch     bool     // 是否是 catch 回调
	IsFinally   bool     // 是否是 finally 回调
}

// CallPromiseHandler 调用一个 Promise 处理回调 (onFulfilled/onRejected/
// onFinally), 并把"回调返回的值"或"回调抛出的原始值"归一为结算结果。
// 返回 (result, ok):
//   - ok=true  : 回调正常返回, result 是返回值 (供 next.Resolve);
//   - ok=false : 回调抛出了异常, result 是**原始抛出值** (供 next.Reject)。
//
// 为什么必须在这里消费回调错误信号: 回调桥 (object.CallFunction) 把 JS
// 抛出以 callbackError/callbackErrorValue 形式记录, 而调用它的内建
// (`then`/`catch`/`finally`/内部结算路径) 若不当场取走, VM 会在内建返回后
// 的 checkCallbackErr 处把它当作"逃逸的未捕获异常"重新抛出 —— 于是
// `.then(onFulfilled)` 里 onFulfilled 抛错会终止整个脚本, 而不是让派生
// promise reject (规范 25.6.5.4.1 ThenableJob / PerformPromiseThen)。
//
// 原始值保真: TakeCallbackErrorValue 给出 throw x 的 x 本身 (字符串/数字/
// 对象均可), 保留 === 身份; 仅在拿不到原始值时降级为 Error(Go 错误字符串)。
func CallPromiseHandler(fn Value, args ...Value) (Value, bool) {
	if !IsCallable(fn) {
		return UndefinedSingleton, true
	}
	result := CallFunction(fn, nil, args...)
	cbErr := TakeCallbackError()
	if cbErr == nil {
		return result, true
	}
	thrown := TakeCallbackErrorValue()
	if thrown == nil || thrown == UndefinedSingleton {
		return NewErrorWithName("Error", cbErr.Error()), false
	}
	return thrown, false
}

func (p *Promise) Type() ObjectType { return PROMISE_OBJ }
func (p *Promise) Inspect() string {
	switch p.State {
	case PromisePending:
		return "Promise { <pending> }"
	case PromiseFulfilled:
		return fmt.Sprintf("Promise { %s }", p.Value.Inspect())
	case PromiseRejected:
		return fmt.Sprintf("Promise { <rejected>: %s }", p.Reason.Inspect())
	}
	return "Promise { <pending> }"
}
func (p *Promise) IsTruthy() bool { return true }

// GetProto 返回实例的 [[Prototype]] = %Promise.prototype%。
//
// 事实来源是鸭子类型入口 `interface{ GetProto() Value }`。此前 *Promise 缺
// 这个方法 (只靠 object_proto.go 的类型穷举兜底), 链式查找 (symbol 成员 /
// Object.getPrototypeOf 的统一通道) 在实例处断掉。
func (p *Promise) GetProto() Value { return PromiseProto }

// GetProperty 两级查找: 自有属性 (含 getter 触发) → %Promise.prototype%。
func (p *Promise) GetProperty(name string) (Value, bool) {
	// (1) 自有属性: 访问器调 getter (this = 实例), 数据属性取描述符里的值。
	//     broken-promise 用例的 `constructor` getter 就在这一层抛出。
	if d, ok := p.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Getter != nil && IsCallable(acc.Getter) {
				return CallFunction(acc.Getter, p), true
			}
			return UndefinedSingleton, true
		}
		if d.Value == nil {
			return UndefinedSingleton, true
		}
		return d.Value, true
	}
	// (2) 原型: 全量委托。此前只认 then/catch/finally, 于是 p.constructor /
	//     p[@@toStringTag] 一律"不存在" —— 与 *Object / *RegExp 的口径不一致。
	if PromiseProto != nil {
		return PromiseProto.GetProperty(name)
	}
	return nil, false
}

// SetProperty 与 *RegExp.SetProperty 同口径: 自有访问器调 setter, 不可写数据
// 属性静默失败 (非严格模式), 其余落成普通自有数据属性。
//
// 此前是 no-op —— `p.foo = 1` 与 `p.constructor = X` 都被静默丢弃。
func (p *Promise) SetProperty(name string, val Value) {
	if d, ok := p.PropDescs[name]; ok && !d.Deleted {
		if acc, isAcc := d.Value.(*Accessor); isAcc {
			if acc.Setter != nil && IsCallable(acc.Setter) {
				CallFunction(acc.Setter, p, val)
			}
			return
		}
		if !d.Writable {
			return
		}
		d.Value = val
		p.PropDescs[name] = d
		return
	}
	p.DefineOwn(name, DataProperty(val))
}

// ===== OwnPropertyStore 五件套 (Object.defineProperty /
// getOwnPropertyDescriptor / getOwnPropertyNames / hasOwn 的统一落点) =====

func (p *Promise) OwnKeys() []string {
	keys := make([]string, 0, len(p.propKeyOrder))
	for _, k := range p.propKeyOrder {
		if deletedProp(p.PropDescs, k) {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (p *Promise) EnumerableOwnKeys() []string {
	var keys []string
	for _, k := range p.propKeyOrder {
		d, ok := p.PropDescs[k]
		if !ok || d.Deleted || !d.Enumerable {
			continue
		}
		keys = append(keys, k)
	}
	return keys
}

func (p *Promise) HasOwn(name string) bool {
	_, ok := p.OwnDescriptor(name)
	return ok
}

func (p *Promise) OwnDescriptor(name string) (PropertyDescriptor, bool) {
	if d, ok := p.PropDescs[name]; ok {
		if d.Deleted {
			return PropertyDescriptor{}, false
		}
		return d, true
	}
	return PropertyDescriptor{}, false
}

func (p *Promise) DefineOwn(name string, desc PropertyDescriptor) bool {
	if p.PropDescs == nil {
		p.PropDescs = make(map[string]PropertyDescriptor)
	}
	if _, exists := p.PropDescs[name]; !exists {
		p.propKeyOrder = append(p.propKeyOrder, name)
	}
	p.PropDescs[name] = desc
	return true
}

// DeleteOwn 删除实例的可配置自有属性。没有这个键时返回 true (与
// *RegExp.DeleteOwn 同口径: 不存在的键删起来"成功")。
func (p *Promise) DeleteOwn(name string) bool {
	d, ok := p.PropDescs[name]
	if !ok {
		return true
	}
	if d.Deleted {
		return true
	}
	if !d.Configurable {
		return false
	}
	delete(p.PropDescs, name)
	for i, k := range p.propKeyOrder {
		if k == name {
			p.propKeyOrder = append(p.propKeyOrder[:i], p.propKeyOrder[i+1:]...)
			break
		}
	}
	return true
}

// NewPromise 创建一个 pending 状态的 Promise。
func NewPromise() *Promise {
	return &Promise{
		State: PromisePending,
	}
}

// Resolve 将 Promise 状态变为 fulfilled。
func (p *Promise) Resolve(val Value) {
	p.mu.Lock()

	if p.State != PromisePending {
		p.mu.Unlock()
		return
	}

	// 如果 val 是 Promise，则等待它
	if innerPromise, ok := val.(*Promise); ok {
		innerPromise.mu.Lock()
		if innerPromise.State == PromiseFulfilled {
			val = innerPromise.Value
		} else if innerPromise.State == PromiseRejected {
			p.State = PromiseRejected
			p.Reason = innerPromise.Reason
			cbs := p.detachCallbacksLocked()
			p.mu.Unlock()
			innerPromise.mu.Unlock()
			invokePromiseCallbacks(p, cbs)
			return
		} else {
			// 内部 Promise 仍 pending，注册回调
			innerPromise.ThenCallbacks = append(innerPromise.ThenCallbacks, PromiseCallback{
				Callback: NewBuiltin("__inner_resolve", func(args ...Value) Value {
					p.Resolve(args[0])
					return UndefinedSingleton
				}),
				NextPromise: nil,
			})
			// IsCatch 必须为 true: invokePromiseCallbacks 只对 IsCatch 的回调
			// 走 rejection 分支。此前漏标 ⇒ 内层 promise 稍后被 reject 时本
			// 回调被静默跳过, 外层 p 永久停留 pending —— 即 "then 回调返回一个
			// 最终会 reject 的 promise 时派生 promise 永不 reject"。
			innerPromise.CatchCallbacks = append(innerPromise.CatchCallbacks, PromiseCallback{
				Callback: NewBuiltin("__inner_reject", func(args ...Value) Value {
					p.Reject(args[0])
					return UndefinedSingleton
				}),
				NextPromise: nil,
				IsCatch:     true,
			})
			innerPromise.mu.Unlock()
			p.mu.Unlock()
			return
		}
		innerPromise.mu.Unlock()
	}

	p.State = PromiseFulfilled
	p.Value = val
	cbs := p.detachCallbacksLocked()
	p.mu.Unlock()
	invokePromiseCallbacks(p, cbs)
}

// Reject 将 Promise 状态变为 rejected。
func (p *Promise) Reject(reason Value) {
	p.mu.Lock()

	if p.State != PromisePending {
		p.mu.Unlock()
		return
	}

	p.State = PromiseRejected
	p.Reason = reason
	cbs := p.detachCallbacksLocked()
	p.mu.Unlock()
	invokePromiseCallbacks(p, cbs)
}

// detachCallbacks 取出并清空已注册的回调队列 (调用时必须持有 p.mu)。
func (p *Promise) detachCallbacksLocked() []PromiseCallback {
	callbacks := make([]PromiseCallback, 0, len(p.ThenCallbacks)+len(p.CatchCallbacks)+len(p.FinallyCallbacks))
	callbacks = append(callbacks, p.ThenCallbacks...)
	callbacks = append(callbacks, p.CatchCallbacks...)
	callbacks = append(callbacks, p.FinallyCallbacks...)
	p.ThenCallbacks = nil
	p.CatchCallbacks = nil
	p.FinallyCallbacks = nil
	return callbacks
}

// invokePromiseCallbacks 同步执行 Promise 回调 (调用时不得持有 p.mu，
// 否则回调中再次访问该 Promise 会死锁)。
//
// 注意: 这里必须同步执行而不是 go func()。原因:
// 脚本模式下主线程在 RunTimers 返回后即退出进程，
// 若回调在独立 goroutine 中执行，进程可能在回调运行前就结束，
// 导致 "setTimeout 里 resolve 的 Promise 的 .then 永远不触发"。
func invokePromiseCallbacks(p *Promise, callbacks []PromiseCallback) {
	for _, cb := range callbacks {
		if cb.IsFinally {
			if IsCallable(cb.Callback) {
				// finally 回调抛错: 覆盖原结算结果 (规范: 仅当回调正常返回
				// 才透传原值/原因)。
				if res, ok := CallPromiseHandler(cb.Callback); !ok {
					if cb.NextPromise != nil {
						cb.NextPromise.Reject(res)
					}
					continue
				}
			}
			if cb.NextPromise != nil {
				if p.State == PromiseFulfilled {
					cb.NextPromise.Resolve(p.Value)
				} else {
					cb.NextPromise.Reject(p.Reason)
				}
			}
		} else if p.State == PromiseFulfilled && !cb.IsCatch {
			if cb.NextPromise != nil {
				if IsCallable(cb.Callback) {
					res, ok := CallPromiseHandler(cb.Callback, p.Value)
					if ok {
						cb.NextPromise.Resolve(res)
					} else {
						cb.NextPromise.Reject(res)
					}
				} else {
					// nil 回调: 原样透传 fulfillment (catch 注册的透传位)
					cb.NextPromise.Resolve(p.Value)
				}
			} else if IsCallable(cb.Callback) {
				// 无 next: 结果被丢弃, 但必须消费回调错误信号, 否则
				// 内建返回后 VM 会把它当逃逸异常重抛。
				CallPromiseHandler(cb.Callback, p.Value)
			}
		} else if p.State == PromiseRejected && cb.IsCatch {
			if cb.NextPromise != nil {
				if IsCallable(cb.Callback) {
					res, ok := CallPromiseHandler(cb.Callback, p.Reason)
					if ok {
						cb.NextPromise.Resolve(res)
					} else {
						cb.NextPromise.Reject(res)
					}
				} else {
					// nil 回调: 原样透传 rejection (then 注册的透传位)
					cb.NextPromise.Reject(p.Reason)
				}
			} else if IsCallable(cb.Callback) {
				CallPromiseHandler(cb.Callback, p.Reason)
			}
		}
	}
}

// Then 注册 fulfilled 回调，返回新的 Promise。
//
// 语义等价于 then(onFulfilled, undefined): rejection 会原样透传给 next。
// 因此 pending 状态下必须**同时**注册一个 rejection 透传回调，
// 否则 Promise 被 reject 时 next 会永远停留在 pending。
func (p *Promise) Then(onFulfilled Value) *Promise {
	next := NewPromise()
	p.mu.Lock()
	switch p.State {
	case PromiseFulfilled:
		p.mu.Unlock()
		if IsCallable(onFulfilled) {
			// onFulfilled 抛错 ⇒ next reject (原始抛出值), 而非逃逸未捕获。
			if res, ok := CallPromiseHandler(onFulfilled, p.Value); ok {
				next.Resolve(res)
			} else {
				next.Reject(res)
			}
		} else {
			next.Resolve(p.Value)
		}
	case PromiseRejected:
		p.mu.Unlock()
		next.Reject(p.Reason)
	default:
		p.ThenCallbacks = append(p.ThenCallbacks, PromiseCallback{
			Callback:    onFulfilled,
			NextPromise: next,
		})
		p.CatchCallbacks = append(p.CatchCallbacks, PromiseCallback{
			Callback:    nil, // nil 表示透传 rejection
			NextPromise: next,
			IsCatch:     true,
		})
		p.mu.Unlock()
	}
	return next
}

// Catch 注册 rejected 回调，返回新的 Promise。
// 同理，fulfillment 会原样透传给 next。
func (p *Promise) Catch(onRejected Value) *Promise {
	next := NewPromise()
	p.mu.Lock()
	switch p.State {
	case PromiseRejected:
		p.mu.Unlock()
		if IsCallable(onRejected) {
			if res, ok := CallPromiseHandler(onRejected, p.Reason); ok {
				next.Resolve(res)
			} else {
				next.Reject(res)
			}
		} else {
			next.Reject(p.Reason)
		}
	case PromiseFulfilled:
		p.mu.Unlock()
		next.Resolve(p.Value)
	default:
		p.CatchCallbacks = append(p.CatchCallbacks, PromiseCallback{
			Callback:    onRejected,
			NextPromise: next,
			IsCatch:     true,
		})
		p.ThenCallbacks = append(p.ThenCallbacks, PromiseCallback{
			Callback:    nil, // nil 表示透传 fulfillment
			NextPromise: next,
		})
		p.mu.Unlock()
	}
	return next
}

// OnFulfilled 只注册 fulfilled 回调，不关心 rejection。
// 供 Promise.all / Promise.race 等内部实现使用: 它们自己分别注册
// 成功与失败回调，无需 Then 的透传机制 (否则会额外创建无用的 Promise)。
func (p *Promise) OnFulfilled(fn Value) {
	p.mu.Lock()
	if p.State == PromiseFulfilled {
		p.mu.Unlock()
		if IsCallable(fn) {
			// 内部 API (Promise.all/race 等自管结算): 仍须消费错误信号,
			// 否则内建返回后 VM 会把回调抛出当作逃逸异常。
			CallPromiseHandler(fn, p.Value)
		}
		return
	}
	p.ThenCallbacks = append(p.ThenCallbacks, PromiseCallback{Callback: fn})
	p.mu.Unlock()
}

// OnRejected 只注册 rejected 回调，不关心 fulfillment。
func (p *Promise) OnRejected(fn Value) {
	p.mu.Lock()
	if p.State == PromiseRejected {
		p.mu.Unlock()
		if IsCallable(fn) {
			CallPromiseHandler(fn, p.Reason)
		}
		return
	}
	p.CatchCallbacks = append(p.CatchCallbacks, PromiseCallback{
		Callback: fn,
		IsCatch:  true,
	})
	p.mu.Unlock()
}

// Finally 注册 finally 回调，返回新的 Promise。
func (p *Promise) Finally(onFinally Value) *Promise {
	next := NewPromise()
	p.mu.Lock()
	if p.State != PromisePending {
		val := p.Value
		reason := p.Reason
		state := p.State
		p.mu.Unlock()
		if IsCallable(onFinally) {
			// onFinally 抛错: 覆盖原结算结果 (next reject 原始抛出值)。
			if res, ok := CallPromiseHandler(onFinally); !ok {
				next.Reject(res)
				return next
			}
		}
		if state == PromiseFulfilled {
			next.Resolve(val)
		} else {
			next.Reject(reason)
		}
	} else {
		p.FinallyCallbacks = append(p.FinallyCallbacks, PromiseCallback{
			Callback:    onFinally,
			NextPromise: next,
			IsFinally:   true,
		})
		p.mu.Unlock()
	}
	return next
}

var PromiseProto Value

func SetPromiseProto(p Value) { PromiseProto = p }

// Lock 加锁 (供外部包使用)。
func (p *Promise) Lock() {
	p.mu.Lock()
}

// Unlock 解锁 (供外部包使用)。
func (p *Promise) Unlock() {
	p.mu.Unlock()
}
