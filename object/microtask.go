package object

// 微任务队列 (Promise Jobs) 调度钩子。
//
// Gox 原来是「结算即回调」的同步 Promise 模型: promise settle 后立刻
// 同步执行 .then 回调, 不符合 ES 规范的微任务语义 (回调应入队, 在当前
// 同步执行完成后、下一个宏任务前按 FIFO 执行, test262 request-queue-order
// 等顺序类用例因此失败)。
//
// object 包不能反向依赖 vm 包, 故这里只留一个调度钩子: vm 包在 init 时
// 注册 enqueuer, promise 反应回调统一经 schedulePromiseJob 进入当前 VM 的
// job 队列; 未注册时 (纯 object 上下文, 如 object 包单测) 回退为同步执行,
// 保持既有语义。
var microtaskEnqueuer func(job func())

// SetMicrotaskEnqueuer 注册微任务入队钩子 (由 vm 包初始化时调用一次)。
func SetMicrotaskEnqueuer(fn func(job func())) {
	microtaskEnqueuer = fn
}

// schedulePromiseJob 把一个 promise 反应回调交给微任务队列;
// 队列未接入时同步执行 (回退到旧的同步 Promise 模型)。
func schedulePromiseJob(job func()) {
	if microtaskEnqueuer != nil {
		microtaskEnqueuer(job)
		return
	}
	job()
}

// EnqueuePromiseJob 是 schedulePromiseJob 的导出版: 供 vm/stdlib 的
// async 函数 await 恢复等"规范要求经微任务续跑"的路径使用。
func EnqueuePromiseJob(job func()) {
	schedulePromiseJob(job)
}
