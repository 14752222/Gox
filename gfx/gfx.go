// Package gfx 提供 Gox 的自研 GUI 渲染层 (P2: Windows 软件渲染)。
//
// 分层结构:
//   - 本文件: 平台抽象 (Surface/WindowFactory)、PostTask 队列、应用状态
//   - node.go:   元素树与 JS 侧 h() (含 gx/solid 响应式接线)
//   - layout.go: 最小布局 (column/row/gap/padding/width/height)
//   - raster.go: 软件光栅化 (纯 Go, 画到 image.RGBA)
//   - hittest.go: 命中测试
//   - render.go: gx/gfx 模块 (h/window/render) 与 Pump 事件泵
//   - win32/:    Windows 窗口后端 (纯 syscall, 无 cgo), init 自注册
//
// 线程模型: 脚本执行、消息泵、VM 回调全部在同一个 OS 线程串行执行
// (render 时 LockOSThread); PostTask 是唯一跨 goroutine 入口。
package gfx

import (
	"image"
	"sync"
	"time"
)

// DefaultWindowPos 是 WindowConfig.X/Y 的"平台自选位置"哨兵。
//
// 为什么需要哨兵: (0,0) 是一个**合法位置** (主屏工作区左上角), 不能用它表达
// "没指定位置"。约定: X 或 Y 小于 -10000 时忽略位置, 交给平台自选
// (Windows 的 CW_USEDEFAULT / cocoa 的居中)。此外全零 (Go 零值, 也是
// render() 缺省) 同样按"未指定"处理 —— 见 ResolveWindowPlacement。
const DefaultWindowPos = -100000

// WindowConfig 窗口创建配置。
//
// X/Y 的口径与 Window.MoveTo / gx/screen 的 windowInfo().x/y **完全一致**:
// 窗口外框左上角相对**目标显示器工作区**左上角的偏移 (设备像素)。
// 跨屏换算 (工作区 → 虚拟桌面绝对坐标) 由 ResolveWindowPlacement 统一完成,
// 后端只承接绝对坐标 —— 这样"坐标口径"只有一处定义。
//
// 字段分三批: 尺寸/标题 (P3-6)、位置与层级 (§四 窗口/系统缺口)、
// 尺寸约束 / 全屏 / 模态 (同批)。**新增字段的零值一律等于"不干预"**,
// 这样 `WindowConfig{Title: "T", Width: 320, Height: 200}` 这种老写法
// 语义完全不变 (既有测试里的结构体字面量比较也不用改)。
type WindowConfig struct {
	Title  string
	Width  int
	Height int

	// X/Y: 位置 (相对目标显示器工作区)。< DefaultWindowPos 或全零 = 平台自选。
	X, Y int

	// Display: 目标显示器 id (见 gx/screen 的 screens()[].id); 空 = 平台默认。
	// 给定时 X/Y 相对**该屏**工作区; 若 X/Y 也未指定, 则在该屏工作区居中
	// (这是"按屏放置"最常用的一种: render(<window display={id}>))。
	//
	// 合流备注 (2026-10-02): §四 那版另有一个 `HasPos bool` 表示"脚本到底
	// 有没有指定位置"。合流后**删掉**了它 —— 位置是否指定由 DefaultWindowPos
	// 哨兵 + 全零约定表达 (见 windowPosSpecified)。两套判据并存迟早对不上,
	// 而且结构体字面量整体比较会凭空多出一个必须记得同步的字段。
	Display string

	// 尺寸约束 (像素, 0 = 不约束)。与 Web 的 minWidth/maxWidth 同义,
	// 由后端落成平台级约束 (WM_GETMINMAXINFO / XSizeHints / NSWindow
	// minSize+maxSize) —— 是"用户拖边框"的钳位, 不是布局钳位:
	// 布局侧的 minWidth/maxWidth 仍然是节点自己的 props。
	MinWidth, MinHeight int
	MaxWidth, MaxHeight int

	// NoResize 关掉用户缩放 (缺省 false = 可缩放)。用否定式命名是为了
	// 让零值等于历史行为。
	NoResize bool

	// Fullscreen 启动即全屏 (缺省 false)。
	Fullscreen bool

	// Level 是窗口层级: "" / "normal" / "top" (置顶) / "bottom" (置底)。
	// 未知值按 normal 处理 (静默, 不让笔误把窗口卡在某个怪层级上)。
	Level string

	// Modal 标记这是一个**模态子窗口**: 创建期间 ModalParent 那个窗口的
	// 输入被屏蔽 (鼠标/键盘/滚轮/输入法), 直到本窗口关闭。
	// ModalParent 为 nil 时 Modal 无效 (没有父就没有"挡住谁"可言),
	// 只是开一个普通窗口 —— 静默降级比抛错好: 脚本拿不到父句柄是常见
	// 疏忽, 不值得让整个窗口开不出来。
	Modal       bool
	ModalParent *Window
}

// windowLevel 归一化窗口层级名 (未知值 → "normal")。
func windowLevel(s string) string {
	switch s {
	case "top", "bottom", "normal":
		return s
	}
	return "normal"
}

// EventKind 窗口事件种类。
type EventKind int

const (
	EventMouseDown EventKind = iota
	EventMouseUp
	EventKeyDown
	EventKeyUp
	EventMouseMove
	EventMouseWheel
	EventMouseRightUp
	// EventMouseLeave 是"光标离开客户区 / 窗口失活"。任务书只列了前四类,
	// 这里补一类是因为 hover 态必须有明确的清除信号: 靠 MouseMove(-1,-1)
	// 之类的哨兵坐标既隐晦又和真实坐标混淆。
	EventMouseLeave
	EventResize
	// EventMove 是窗口被移动 (§四 窗口/系统缺口)。载荷口径与 EventResize
	// 的 W/H 一致: X/Y 是**窗口外框在屏幕坐标系里的左上角**, 由后端在收到
	// 平台移动消息时填 (win32 WM_MOVE / cocoa windowDidMove / X11
	// ConfigureNotify)。
	//
	// 注意它与鼠标事件的 X/Y 不是一回事 —— 那两个是客户区相对坐标, 这里
	// 的两个是屏幕绝对坐标 (窗口自己"在哪")。Event 结构里 X/Y 已经被鼠标
	// 占用, 这里按"字段名贴近语义、坐标系在文档里写清"而不是"避免重名"
	// 来选: 把窗口坐标改名成 ScreenX/ScreenY 会让"读位置"这件事在多处
	// 分裂成两套词汇。
	//
	// **字段留在绝对坐标上是刻意的**: 换算成脚本口径需要一个前提 —— "窗口
	// 在哪块屏", 而那要查显示器几何 (后端 DisplayOf / 平台 API), 属于内核的
	// 事; 后端只该报事实。render.go 派发 onMove 前会经 displayOfWorkSurface
	// 换成"所在显示器工作区相对 + 设备像素", 与 position()/bounds()/moveTo
	// 同一参照系 (于是脚本拿到的载荷能直接喂回 moveTo)。
	EventMove
	// EventIMECommit 是输入法提交的一批字符 (P2-7)。组合过程由平台自己的
	// 组合窗显示, 只有"用户选定了候选词"这一刻才会拿到结果串, 因此它天然是
	// 整批插入 —— 与 WM_CHAR 那种一次一个字符的路径完全不同。
	EventIMECommit
	EventClose
)

// Event 是窗口事件。
type Event struct {
	Kind EventKind
	X, Y int    // 鼠标事件坐标 (客户区像素)
	W, H int    // EventResize 后的新尺寸
	Key  string // EventKeyDown/EventKeyUp 的键名 (可打印字符或 Enter/Backspace/ArrowLeft/...)

	// DeltaY 是滚轮增量, 向上滚为正 (Windows WHEEL_DELTA 一格的原始语义)。
	// 注意 JS 侧 onWheel 收到的是 DOM 约定的 deltaY (向下为正), 分发时取反。
	DeltaY int

	// 修饰键状态 (随 KeyDown/KeyUp 附带)。后端在投递时刻读取键盘状态,
	// 因为平台键消息的低位状态位在部分场景下不可靠。
	Ctrl, Shift, Alt bool

	// Text 是 EventIMECommit 提交上来的整批字符 (UTF-8)。放在 Event 里而不是
	// 走 Post 回调, 是为了让"输入法提交"与按键走同一条事件通路: WndProc
	// 依旧只投递事件、不碰元素树, 而测试也能直接推一条事件验全链路。
	Text string
}

// Surface 是平台窗口的抽象: 一块可上屏的像素面 + 事件流。
// 为 P4 的 X11/macOS 后端预留; 实现负责 RGBA→平台格式的转换。
type Surface interface {
	// Show 将一整帧像素上屏。
	Show(img *image.RGBA)
	// ShowRegions 只把给定区域上屏 (脏矩形局部呈现; rects 为空 = 全帧)。
	ShowRegions(img *image.RGBA, rects []image.Rectangle)
	// Size 返回当前客户区尺寸 (像素)。
	Size() (w, h int)
	// WaitEvents 等待外部事件至多 maxWait (<=0 表示无限期), 期间分发并
	// 处理平台消息 (消息 → Event 投递到 Events 通道)。
	// 事件源关闭 (窗口销毁) 时返回 false。
	WaitEvents(maxWait time.Duration) bool
	// Events 返回事件流 (实现投递, 带缓冲; 满时允许丢弃)。
	Events() <-chan Event
}

// WindowFactory 创建窗口。
type WindowFactory interface {
	Create(cfg WindowConfig) (Surface, error)
}

var defaultFactory WindowFactory

// SetDefaultFactory 注册默认窗口后端 (由平台包 init 调用)。
//
// 传 nil 表示"撤销后端", 此时**顺带清空窗口注册表** (P3-6)。
// 语义是自洽的: 没有后端就不可能有活着的窗口, 留下注册表条目只会让 Pump
// 去等一个再也不会送事件的 Surface (表现为事件循环永久挂住)。
// 生产代码只以非 nil 调用 (平台包 init); nil 这条路是测试拆卸用的,
// 在那儿它替代了"逐个窗口 close"的样板代码。
func SetDefaultFactory(f WindowFactory) {
	appMu.Lock()
	defer appMu.Unlock()
	defaultFactory = f
	if f == nil {
		for s, a := range apps {
			a.mu.Lock()
			a.closed = true
			a.mu.Unlock()
			delete(apps, s)
		}
		activeApp = nil
	}
}

// ===== 跨线程唤醒原语 (rl65eE) =====
//
// 问题: Post 的 check-then-sleep 竞态 —— Pump 在睡进 WaitEvents **之前**
// 检查 hasPendingPost(), 另一线程在这之后 Post 的任务看不到 ⇒ 单窗口空闲时
// 会以无界预算睡下去, 直到恰好来了平台输入事件才醒。真机症状: 跨线程 Post
// 的动作 (文件对话框结果、网络回调转 JS、跨线程 Window.Close) 点了没反应,
// 动一下鼠标才好。
//
// 解法: 显式唤醒, 而不是继续给等待预算加各种"上限"。Post 在入队后向一个
// **进程级**通道写一次 token; 实现 waker 可选接口的后端把这个通道并进自己
// 的平台等待集合, 于是"睡在 WaitEvents 里"的泵会被立刻打断, 本轮的
// DrainTasks 就能执行任务。
//
// 为什么是进程级: Post 的队列本就是全局的、且没有目标窗口 (v1 广播语义),
// 所以唤醒信号也只描述"有跨线程任务"这一件全局事实 —— 哪个窗口的
// WaitEvents 先醒都行, Pump 随后统一 DrainTasks。
var wakeCh = make(chan struct{}, 1)

// signalWake 尝试投递一个唤醒 token。缓冲 1 + 非阻塞发送 = 天然去重:
// 已有未消费 token 时不重复投 (唤醒只需"至少一次", 后端也不必区分次数)。
func signalWake() {
	select {
	case wakeCh <- struct{}{}:
	default:
	}
}

// WakeChan 返回唤醒通道的接收端 —— 实现 waker 的后端把它并入等待集合。
// 语义: 读到值 = "刚有跨线程任务投递, 别睡了"。
func WakeChan() <-chan struct{} { return wakeCh }

// waker 是 Surface 的**可选能力**: 把 gfx.Post 的跨线程唤醒 (见 WakeChan)
// 并进后端自己的等待集合。
//
// 为什么做成可选接口而不是给 Surface 加方法 (内核纪律: 可选能力一律走
// "可选接口", Surface 不扩): Surface 有三个真后端 (win32/x11/cocoa) 与
// 测试用假 Surface —— 扩接口要同步改所有实现, 而假 Surface 的 WaitEvents
// 本来就 select 在 Go channel 上 (测试里直接推事件即可), 根本没有"平台
// 等待集合"可并入。类型断言后, 未实现的后端自动退化为
// "WaitEvents + postDrainCap 上限" (行为不坏, 只是空闲期多几次空转)。
//
// 方法名必须**导出** (与 capturer / clipboardHost 同理): win32/x11/cocoa
// 都是另一个包, Go 不允许跨包实现未导出方法。
type waker interface {
	// WaitEventsWake 与 Surface.WaitEvents 同义, 但额外等待 wake:
	// 从 wake 读到值 (gfx.Post 的信号) 时立即返回 true, 让 Pump 本轮立刻
	// DrainTasks。maxWait<=0 仍表示无限期 —— 此时只有 wake / 平台事件 /
	// 窗口关闭能唤醒它。
	//
	// 实现要点: 平台的阻塞等待必须**真正**把 wake 并进同一个等待集合
	// (win32 走 MsgWaitForMultipleObjectsEx 的手柄数组; x11 走 select;
	// cocoa 走 post 一个空事件), 而不是把 maxWait 切成小片轮询 —— 后者
	// 只是把"无限期睡死"换成"有界轮询", 与本次修复的意图相反。
	WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool
}

// ===== PostTask 队列 =====
// WndProc 等平台回调绝不直接执行 JS, 一律投递任务由 Pump 在 VM 线程执行。

var (
	postMu    sync.Mutex
	postQueue []func()
)

// Post 投递一个任务到 GUI 线程。
//
// P3-6 多窗口语义: 队列是**全局的**, 任务没有目标窗口 —— Pump 每轮只
// DrainTasks 一次, 于是任务在"任意窗口还在跑"时都会被执行 (v1 的广播语义)。
// 目前唯一的调用点是 Window.Close (关自己) 与平台回调用它推跨线程动作,
// 二者都不依赖执行时机落在某个具体窗口上, 所以广播是安全的。
// 若将来出现"任务必须由特定窗口处理"的需求 (如按窗口分发的 PostMessage),
// 再给 Post 加目标参数 (Surface 或 *Window) 并在这里做路由 —— 不要现在
// 猜, 因为多一个参数会让所有既有调用点都要解释"这个任务属于谁"。
func Post(task func()) {
	postMu.Lock()
	postQueue = append(postQueue, task)
	postMu.Unlock()
	// 入队后立刻给后端一个唤醒信号 (见 wakeCh): 若此刻泵正睡在
	// WaitEvents 里, 这一下会把它叫醒, 于是本轮 DrainTasks 就能执行任务。
	// 这是修复 "check-then-sleep" 竞态的关键 —— hasPendingPost 的检查与
	// 睡进 WaitEvents 之间有一个窗口, 只靠 Pump 侧检查补不上。
	signalWake()
}

// hasPendingPost 报告是否有已投递、尚未执行的 Post 任务 (Pump 用)。
func hasPendingPost() bool {
	postMu.Lock()
	defer postMu.Unlock()
	return len(postQueue) > 0
}

// DrainTasks 取出并执行全部排队任务 (仅应在 GUI 线程/Pump 内调用)。
// 多窗口下由 Pump 每轮调用**一次** (不是每个窗口一次), 否则同一批任务
// 会被执行多遍。
func DrainTasks() {
	for {
		postMu.Lock()
		var task func()
		if len(postQueue) > 0 {
			task = postQueue[0]
			postQueue = postQueue[1:]
		}
		postMu.Unlock()
		if task == nil {
			return
		}
		task()
	}
}
