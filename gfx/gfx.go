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

// WindowConfig 窗口创建配置。
//
// 字段分三批: 尺寸/标题 (P3-6)、位置与层级 (§四 窗口/系统缺口)、
// 尺寸约束 / 全屏 / 模态 (同批)。**新增字段的零值一律等于"不干预"**,
// 这样 `WindowConfig{Title: "T", Width: 320, Height: 200}` 这种老写法
// 语义完全不变 (既有测试里的结构体字面量比较也不用改)。
type WindowConfig struct {
	Title  string
	Width  int
	Height int

	// X, Y 是**初始位置** (屏幕坐标, 外框左上角)。零值本身是合法坐标,
	// 所以另有一个 HasPos 说明"脚本到底有没有指定": 没指定时交给系统
	// (Windows 用 CW_USEDEFAULT, 其它平台按各自默认), 指定了才精确落点。
	//
	// 为什么不做成 *int: WindowConfig 会被测试用结构体字面量整体比较,
	// 指针字段会让"默认配置"带上一个非 nil 的指针, 比较立刻不等。
	X, Y   int
	HasPos bool

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
