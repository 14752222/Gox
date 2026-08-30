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
	mu          sync.Mutex
	State       PromiseState
	Value       Value          // fulfilled 时的值
	Reason      Value          // rejected 时的原因
	ThenCallbacks  []PromiseCallback // then 回调队列
	CatchCallbacks []PromiseCallback // catch 回调队列
	FinallyCallbacks []PromiseCallback // finally 回调队列
}

// PromiseCallback 存储回调函数和创建的后续 Promise。
type PromiseCallback struct {
	Callback    Value  // 回调函数
	NextPromise *Promise // 链式调用的下一个 Promise
	IsCatch     bool   // 是否是 catch 回调
	IsFinally   bool   // 是否是 finally 回调
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

func (p *Promise) GetProperty(name string) (Value, bool) {
	switch name {
	case "then", "catch", "finally":
		if PromiseProto != nil {
			return PromiseProto.GetProperty(name)
		}
	}
	return nil, false
}

func (p *Promise) SetProperty(name string, val Value) {}

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
			innerPromise.CatchCallbacks = append(innerPromise.CatchCallbacks, PromiseCallback{
				Callback: NewBuiltin("__inner_reject", func(args ...Value) Value {
					p.Reject(args[0])
					return UndefinedSingleton
				}),
				NextPromise: nil,
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
				CallFunction(cb.Callback, nil)
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
					result := CallFunction(cb.Callback, nil, p.Value)
					cb.NextPromise.Resolve(result)
				} else {
					cb.NextPromise.Resolve(p.Value)
				}
			} else if IsCallable(cb.Callback) {
				CallFunction(cb.Callback, nil, p.Value)
			}
		} else if p.State == PromiseRejected && cb.IsCatch {
			if cb.NextPromise != nil {
				if IsCallable(cb.Callback) {
					result := CallFunction(cb.Callback, nil, p.Reason)
					cb.NextPromise.Resolve(result)
				} else {
					cb.NextPromise.Reject(p.Reason)
				}
			} else if IsCallable(cb.Callback) {
				CallFunction(cb.Callback, nil, p.Reason)
			}
		}
	}
}

// Then 注册 fulfilled 回调，返回新的 Promise。
func (p *Promise) Then(onFulfilled Value) *Promise {
	next := NewPromise()
	p.mu.Lock()
	if p.State == PromiseFulfilled {
		p.mu.Unlock()
		if IsCallable(onFulfilled) {
			result := CallFunction(onFulfilled, nil, p.Value)
			next.Resolve(result)
		} else {
			next.Resolve(p.Value)
		}
	} else {
		p.ThenCallbacks = append(p.ThenCallbacks, PromiseCallback{
			Callback:    onFulfilled,
			NextPromise: next,
		})
		p.mu.Unlock()
	}
	return next
}

// Catch 注册 rejected 回调，返回新的 Promise。
func (p *Promise) Catch(onRejected Value) *Promise {
	next := NewPromise()
	p.mu.Lock()
	if p.State == PromiseRejected {
		p.mu.Unlock()
		if IsCallable(onRejected) {
			result := CallFunction(onRejected, nil, p.Reason)
			next.Resolve(result)
		} else {
			next.Reject(p.Reason)
		}
	} else {
		p.CatchCallbacks = append(p.CatchCallbacks, PromiseCallback{
			Callback:    onRejected,
			NextPromise: next,
			IsCatch:     true,
		})
		p.mu.Unlock()
	}
	return next
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
			CallFunction(onFinally, nil)
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
