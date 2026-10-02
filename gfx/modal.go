package gfx

// 模态子窗口 (§四 窗口/系统缺口)。
//
// 语义只有一条: **子窗口活着的时候, 父窗口收不到交互事件**。
//
//	const parent = render(<window title="主窗口">…</window>);
//	render(<window modal={parent} title="确认">…</window>);   // 从此主窗口点不动
//
// ## 为什么做在内核层而不是各后端
//
// "谁被挡住"是**跨窗口**的语义, 而后端只看得到自己那个 hwnd。若交给平台:
// win32 要 EnableWindow、X11 要 grab、cocoa 要 runModalForWindow —— 三套
// 互不相同的机制, 而且**假 Surface (测试用) 一个都实现不了**, 模态行为
// 就永远测不到。
//
// 做在 gfx 层之后: 屏蔽发生在事件分发的入口 (与"窗口是谁"同层), 一处逻辑
// 覆盖全部后端, 假后端上照样成立。后端可选的 EnableWindow 只是锦上添花的
// **原生观感** (父窗口标题栏变灰), 不做也不影响语义 —— 所以它不是契约。
//
// ## 关闭联动
//
// 关父窗口会连带关掉它的模态子窗口。理由不是"顺便", 而是**避免孤儿**:
// 模态子窗口的输入屏蔽是绑在父窗口上的, 父窗口一关, 屏蔽对象消失, 子窗口
// 就变成一个"永远置顶、没人挡、关不掉"的怪窗口 —— Windows 的原生语义也是
// 销毁属主即销毁被属主窗口 (DestroyWindow 连带 DestroyWindow 掉 owned 窗口),
// 这里与平台对齐。

// attachModal 建立 "child 挡住 parent" 的关系 (双向指针一次挂好)。
//
// 覆盖式语义: 同一个父窗口再开一个模态子窗口时, 后开的成为"当前生效的那个",
// 先前那个**降级**为普通窗口 (指针让位)。这与平台一致 —— 模态是"一次性挡在
// 前面的那一张", 不是栈。
func attachModal(parent, child *app) {
	if parent == nil || child == nil || parent == child {
		return
	}
	parent.mu.Lock()
	old := parent.modalChild
	parent.modalChild = child
	parent.mu.Unlock()
	child.mu.Lock()
	child.modalParent = parent
	child.mu.Unlock()
	if old != nil && old != child {
		old.mu.Lock()
		if old.modalParent == parent {
			old.modalParent = nil
		}
		old.mu.Unlock()
	}
}

// detachModal 解除 child 与其父的模态关系 (child 关闭时调用, 幂等)。
// parent 侧只有当它自己还指着 child 时才清 —— 覆盖式语义下可能已经换人。
func detachModal(child *app) {
	if child == nil {
		return
	}
	child.mu.Lock()
	parent := child.modalParent
	child.modalParent = nil
	child.mu.Unlock()
	if parent == nil {
		return
	}
	parent.mu.Lock()
	if parent.modalChild == child {
		parent.modalChild = nil
	}
	parent.mu.Unlock()
}

// blockedByModal 报告本窗口是否正被一个模态子窗口挡着。
//
// 判据是 **modalChild** 而不是 modalParent, 这一点极易写反: modalParent 非空
// 意味着"本窗口**是**模态子窗口" (那是 IsModal), 而"被挡住"是"本窗口**有**
// 一个活着的模态子窗口"。写反的后果不是不生效而是**反着生效** —— 挡住的是
// 子窗口自己, 父窗口照收输入, 模态形同不存在 (模态全链路用例逮到过一次)。
//
// 还要看子窗口是否已关闭: 指针在 detachModal 之前可能还挂着, 而"对着一张
// 已销毁的窗口断言自己被挡住"会让父窗口永久锁死。
func (a *app) blockedByModal() bool {
	a.mu.Lock()
	c := a.modalChild
	a.mu.Unlock()
	if c == nil {
		return false
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	return !closed
}

// modalChildApp 返回当前挡住本窗口的子窗口 (没有则 nil)。
func (a *app) modalChildApp() *app {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.modalChild
}

// modalParentApp 返回本窗口挡住的父窗口 (不是模态子窗口则 nil)。
func (a *app) modalParentApp() *app {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.modalParent
}

// modalBlocksEvent 报告这条事件要不要在模态屏蔽下丢掉。
//
// 保留三类: 窗口关闭 (用户点标题栏的叉必须能关掉父窗口)、尺寸变化 (拖边框
// 仍要重绘, 否则被挡的窗口看起来是"卡死的白板")、移动 (拖动标题栏仍要跟手,
// 而且 onMove 是纯通知)。丢弃的是全部**输入**: 点击、移动、滚轮、右键、
// 键盘、输入法 —— 模态的全部意义就是这些。
//
// 不丢 MouseLeave 是有意的: 光标离开客户区时悬停态必须清干净 (否则被挡
// 期间按钮一直是"悬停/按住"的假状态, 解除遮挡后仍然亮着)。
func (a *app) modalBlocksEvent(kind EventKind) bool {
	switch kind {
	case EventClose, EventResize, EventMove, EventMouseLeave:
		return false
	}
	return true
}

// modalBump 在被模态挡住的窗口收到输入时, 把挡住它的子窗口顶到前台。
//
// 这一步不做的话有一个非常具体的坏体验: 用户点父窗口, 什么都不发生, 而模态
// 子窗口可能被别的应用盖住了 —— 用户看到的是"程序死了"。平台的原生模态
// (win32 的模态对话框) 会自动把对话框闪一下/带回前台, 这里做同一件事。
//
// 只在鼠标类事件上做 (键盘事件没有"往哪看"的语义)。Activate 是后端可选的,
// 没有后端支持时整条降级为 no-op, 语义仍成立 (父窗口依然被挡着)。
func (a *app) modalBump() {
	if a == nil {
		return
	}
	c := a.modalChildApp()
	if c == nil {
		return
	}
	if s := c.surfaceOf(); s != nil {
		if m, ok := s.(windowManager); ok {
			m.Activate()
		}
	}
}

// surfaceOf 取本窗口的 Surface (加锁读)。
func (a *app) surfaceOf() Surface {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.surface
}

// closeModalChild 递归关掉本窗口挡着的模态子窗口 (由 app.close 调用)。
//
// 走 Post 而不是直接调 close: 与 Window.Close 同一条纪律 —— 这里是"某窗口
// 正在被关闭"的收尾路径, 而收尾可能发生在事件遍历中间 (processEvents 里
// 的 sawClose), 那时直接改注册表就是在迭代时改集合。
func (a *app) closeModalChild() {
	c := a.modalChildApp()
	if c == nil {
		return
	}
	Post(func() { c.close() })
}

// IsModal 报告本窗口是否是模态子窗口 (即"我正在挡住别的窗口")。
func (w *Window) IsModal() bool {
	if w == nil || w.a == nil {
		return false
	}
	return w.a.modalParentApp() != nil
}

// IsBlocked 报告本窗口是否正被一个模态子窗口挡着 (父窗口视角)。
//
// 与 IsModal 是一对: IsModal 是子窗口问"我在挡谁", IsBlocked 是父窗口问
// "我被谁挡着"。脚本用后者把主界面画成禁用态 —— 模态的价值之一就是让用户
// **看得见**自己进不去; 光把输入吞掉而不给任何视觉提示, 用户会觉得程序死了。
func (w *Window) IsBlocked() bool {
	if w == nil || w.a == nil {
		return false
	}
	return w.a.blockedByModal()
}

// ModalParent 返回本窗口挡住的父窗口 (非模态窗口返回 nil)。
func (w *Window) ModalParent() *Window {
	if w == nil || w.a == nil {
		return nil
	}
	if p := w.a.modalParentApp(); p != nil {
		return p.handle()
	}
	return nil
}

// ModalChild 返回当前挡住本窗口的子窗口 (没有被挡返回 nil)。
func (w *Window) ModalChild() *Window {
	if w == nil || w.a == nil {
		return nil
	}
	if c := w.a.modalChildApp(); c != nil {
		return c.handle()
	}
	return nil
}

// handle 返回窗口的稳定句柄 (Mount 时装上的那个)。理论上不会为 nil
// (Mount 一定建句柄), 但 go:build 之外的调用路径 (测试直接构造 app)
// 可能没有, 那就现造一个 —— 退化也比 nil 解引用好。
func (a *app) handle() *Window {
	a.mu.Lock()
	w := a.win
	a.mu.Unlock()
	if w != nil {
		return w
	}
	w = &Window{a: a}
	a.mu.Lock()
	if a.win == nil {
		a.win = w
	}
	a.mu.Unlock()
	return w
}
