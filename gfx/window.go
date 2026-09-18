package gfx

import (
	"sync"

	"github.com/14752222/Gox/object"
)

// 窗口句柄 (P3-6)。
//
// 多窗口的入口是 `render()`, 它现在**返回一个句柄**而不是 undefined:
//
//	const w1 = render(<window title="A" width={320} height={200}><Counter label="A" /></window>);
//	const w2 = render(<window title="B" width={320} height={200}><Counter label="B" /></window>);
//	w1.close();   // 只关第一个窗口, 第二个继续跑
//
// 为什么需要句柄: "关掉某一个窗口"在多窗口下是刚需 (验收里就是"关一个另一个
// 继续运行"), 而脚本除了 render 的返回值之外没有别的办法指认是哪个窗口。
//
// ## 为什么 close() 必须经 Post 投回 GUI 线程
//
// 窗口销毁是平台动作 (win32 的 DestroyWindow, X11 的 XDestroyWindow), 必须在
// 创建它的线程上做 —— 而 JS 里的 `w.close()` 一定是在 GUI 线程执行的
// (脚本、事件泵、VM 回调同线程串行)。所以直接调 `a.close()` 看起来也行?
//
// **不行, 反例是时序**: `a.close()` 会把窗口从注册表里摘掉, 而调用它的那一刻
// 可能正在 `processEvents` 的遍历里 (`Pump` 拿着 `appsSnapshot` 的副本在跑)。
// 从副本对应的树里摘掉自己不会 panic, 但**本轮之后的重绘/上屏**就不再走了,
// 平台窗口要等下一次 Pump 才发现"没人认领它"。更麻烦的是 `close()` 里可能
// 触发脚本回调 (onClose), 在遍历中改注册表等于在迭代时改集合。
//
// 所以统一走 `Post`: 它把"销毁"排到本轮 Pump 之后的 DrainTasks 执行, 让
// "谁还活着"的判定与"摘除"在时间上彻底分开。这也与仓库既有的线程纪律一致
// (跨线程只经 Post)。
//
// 注意 Post 队列是**全局**的: 多窗口下 DrainTasks 每轮只跑一次, 见 Pump。

// Window 是一个已挂载窗口的句柄。
type Window struct {
	a   *app
	mu  sync.Mutex
	obj *object.Object // 惰性构造的 JS 对象 (同一窗口复用同一个对象)
}

// App 返回底层 app (Go 侧持有句柄时用; 测试用它断言窗口状态)。
func (w *Window) App() *app { return w.a }

// Close 关闭本窗口。可在任意 goroutine 调用 (内部经 Post 投回 GUI 线程),
// 重复调用无副作用 (底下的 close 是幂等的)。
func (w *Window) Close() {
	a := w.a
	if a == nil {
		return
	}
	Post(func() { a.close() })
}

// Surface 返回本窗口的像素面 (测试/后端回调用)。
func (w *Window) Surface() Surface {
	if w.a == nil {
		return nil
	}
	w.a.mu.Lock()
	defer w.a.mu.Unlock()
	return w.a.surface
}

// closed 报告窗口是否已关闭。
func (w *Window) closed() bool {
	if w.a == nil {
		return true
	}
	w.a.mu.Lock()
	defer w.a.mu.Unlock()
	return w.a.closed
}

// jsObject 把句柄包装成 JS 对象 (render 的返回值)。
//
// 方法用 object.NewBuiltin 而不是让 JS 侧自己写原型链: 与仓库里其它
// "Go 提供能力、JS 直接调用"的内置对象 (如 Math/Object) 保持同一写法,
// 且 close 的实现必须在 Go 侧 (它要碰 app 指针)。
func (w *Window) jsObject() object.Value {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.obj != nil {
		return w.obj
	}
	o := object.NewObject()
	o.SetProperty("close", object.NewBuiltin("close", func(args ...object.Value) object.Value {
		w.Close()
		return object.UndefinedSingleton
	}))
	// closed 做成方法而不是布尔属性: 属性值会在构造时被快照, 而脚本需要
	// 的是"此刻关没关" —— 快照会永远返回 false, 是个隐蔽的坑。
	o.SetProperty("isClosed", object.NewBuiltin("isClosed", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.closed())
	}))
	w.obj = o
	return o
}

// jsWindowObject 把 Go 侧句柄转成 JS 对象 (宿主嵌入时用)。
func jsWindowObject(w *Window) object.Value {
	if w == nil {
		return object.UndefinedSingleton
	}
	return w.jsObject()
}
