package vm

import (
	"github.com/14752222/Gox/object"
)

// 微任务队列 (Promise Jobs, todo rrpZIw / r63RpV)。
//
// ES 规范要求 promise 反应回调 (.then/.catch/.finally、async 函数 resume)
// 不在 settle 时同步执行, 而是作为 job 入队, 在当前同步执行完成后、下一个
// 宏任务 (定时器/事件回调) 前按 FIFO 执行, drain 期间新入队的 job 同轮
// 继续执行 (test262 request-queue-order 等顺序类用例依赖此语义)。
//
// 结构: object 包在 promise settle / 已结算 then 注册时经
// object.SetMicrotaskEnqueuer 注册的钩子把 job 送进来; 这里按 currentVM
// 路由到对应 VM 的队列 (与回调桥 object.CallFunction 同一套 currentVM
// 纪律)。currentVM 为空 (纯 object 上下文) 时同步执行, 回退旧语义。

func init() {
	object.SetMicrotaskEnqueuer(func(job func()) {
		if v := currentVM; v != nil {
			v.enqueueMicrotask(job)
			return
		}
		job()
	})
}

// 队列字段 (microtasks/microtaskMu/drainingMicrotasks) 定义在 vm.go 的
// VM 结构体上。

// enqueueMicrotask 追加一个 job (可被其他 goroutine 调用: 异步 I/O 完成时
// resolve promise 会走到这里)。
func (vm *VM) enqueueMicrotask(job func()) {
	vm.microtaskMu.Lock()
	vm.microtasks = append(vm.microtasks, job)
	vm.microtaskMu.Unlock()
}

// drainMicrotasks 按 FIFO 执行队列中的全部 job, 含执行期间新入队的。
// 可重入保护: job 内部经回调桥再次触发 drain 时直接返回, 由外层循环
// 继续消费 (否则同一 job 会被两层 drain 交错执行, 破坏 FIFO)。
func (vm *VM) drainMicrotasks() {
	vm.microtaskMu.Lock()
	if vm.drainingMicrotasks {
		vm.microtaskMu.Unlock()
		return
	}
	vm.drainingMicrotasks = true
	for len(vm.microtasks) > 0 {
		job := vm.microtasks[0]
		vm.microtasks[0] = nil // 便于 GC
		vm.microtasks = vm.microtasks[1:]
		vm.microtaskMu.Unlock()
		job()
		vm.microtaskMu.Lock()
	}
	vm.drainingMicrotasks = false
	vm.microtaskMu.Unlock()
}

// hasMicrotasks 报告队列是否非空 (事件循环退出判断用)。
func (vm *VM) hasMicrotasks() bool {
	vm.microtaskMu.Lock()
	defer vm.microtaskMu.Unlock()
	return len(vm.microtasks) > 0
}
