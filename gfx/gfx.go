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
type WindowConfig struct {
	Title  string
	Width  int
	Height int
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
