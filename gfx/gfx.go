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
	EventResize
	EventClose
)

// Event 是窗口事件。
type Event struct {
	Kind EventKind
	X, Y int    // 鼠标事件坐标 (客户区像素)
	W, H int    // EventResize 后的新尺寸
	Key  string // EventKeyDown 的键名 (可打印字符或 Enter/Backspace/ArrowLeft/...)
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
func SetDefaultFactory(f WindowFactory) { defaultFactory = f }

// ===== PostTask 队列 =====
// WndProc 等平台回调绝不直接执行 JS, 一律投递任务由 Pump 在 VM 线程执行。

var (
	postMu    sync.Mutex
	postQueue []func()
)

// Post 投递一个任务到 GUI 线程。
func Post(task func()) {
	postMu.Lock()
	postQueue = append(postQueue, task)
	postMu.Unlock()
}

// DrainTasks 取出并执行全部排队任务 (仅应在 GUI 线程/Pump 内调用)。
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
