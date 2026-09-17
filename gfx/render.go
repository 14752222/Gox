package gfx

import (
	"fmt"
	"image"
	"image/color"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/14752222/Gox/object"
)

// gx/gfx 模块与 GUI 应用状态。
//
// JS 侧 API:
//
//	import { h, window, render, requestAnimationFrame } from "gx/gfx";
//	render(<column gap={8}>...</column>, window({title, width, height}));
//
// render() 挂载元素树并创建窗口后立即返回; 阻塞式的消息泵由宿主入口经
// vm.RunTimersWithPump(gfx.Pump) 驱动 (见 main.go)。
//
// 脏矩形 (P3): effect 写回属性时对所在节点标脏 (dirtyNodes); 重绘时
// 先重新布局, 再对比每节点 Box/PrevBox 差异 (布局位移的兄弟节点也会被
// 捕获), 合并脏矩形后只清空+重绘+上屏受影响区域。总面积超过帧的 85%
// 时退化为整帧重绘。

// app 单窗口应用状态 (v1 全局单例)。
type app struct {
	mu         sync.Mutex
	surface    Surface
	root       *GuiNode
	img        *image.RGBA
	needDraw   bool
	fullDirty  bool // 整帧标脏 (首帧/resize)
	closed     bool
	closeOnce  sync.Once
	dirtyNodes map[*GuiNode]struct{} // 属性变化的节点 (框可能不变)
	focused    *GuiNode              // 键盘事件焦点 (点击更新, 默认根)
}

var (
	appMu     sync.Mutex
	activeApp *app
)

// Active 报告是否有已挂载的 GUI (宿主入口据此选择事件循环模式)。
func Active() bool {
	appMu.Lock()
	defer appMu.Unlock()
	return activeApp != nil
}

// Invalidate 整帧标脏 (resize 等)。
func Invalidate() {
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	a.needDraw = true
	a.fullDirty = true
	a.mu.Unlock()
}

// markNodeDirty 节点级标脏 (effect 写回属性时调用)。
func markNodeDirty(n *GuiNode) {
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		return
	}
	a.mu.Lock()
	a.needDraw = true
	if a.dirtyNodes == nil {
		a.dirtyNodes = map[*GuiNode]struct{}{}
	}
	a.dirtyNodes[n] = struct{}{}
	a.mu.Unlock()
}

// Pump 是事件泵, 作为 vm.RunTimersWithPump 的 pump 回调:
// 等待窗口消息 (至多 maxWait, <=0 表示无限) → 处理事件 → 执行 PostTask →
// 有脏区则重绘。窗口关闭后返回 false 结束事件循环。
func Pump(maxWait time.Duration) bool {
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		return false
	}
	return a.pump(maxWait)
}

func (a *app) pump(maxWait time.Duration) bool {
	// 1) 等待并分发窗口消息 (WndProc 只投递事件, 不执行 JS)
	if !a.surface.WaitEvents(maxWait) {
		a.close()
		return false
	}
	// 2) 执行投递任务
	DrainTasks()
	// 3) 处理窗口事件 (点击 → 命中测试 → onClick, 在 VM 线程执行);
	//    close 先记录, 冲刷完本轮重绘后再退出
	sawClose := false
	for {
		ev, ok := a.takeEvent()
		if !ok {
			break
		}
		switch ev.Kind {
		case EventClose:
			sawClose = true
		case EventMouseUp:
			a.handleClick(ev.X, ev.Y)
		case EventKeyDown:
			a.handleKey(ev.Key)
		case EventResize:
			a.mu.Lock()
			a.needDraw = true
			a.fullDirty = true
			a.mu.Unlock()
		}
	}
	// 4) 脏区重绘
	a.mu.Lock()
	need := a.needDraw
	a.mu.Unlock()
	if need {
		a.redraw()
	}
	if sawClose {
		a.close()
		return false
	}
	return true
}

// takeEvent 非阻塞取一条窗口事件。
func (a *app) takeEvent() (Event, bool) {
	select {
	case ev := <-a.surface.Events():
		return ev, true
	default:
		return Event{}, false
	}
}

// handleClick 命中测试并调用 onClick 回调。
func (a *app) handleClick(x, y int) {
	a.mu.Lock()
	root := a.root
	a.mu.Unlock()
	target := HitTest(root, x, y)
	if target == nil {
		return
	}
	// 点击即设为键盘焦点 (键事件沿祖先链寻找 onKeyDown)
	a.mu.Lock()
	a.focused = target
	a.mu.Unlock()

	if handler := target.PropHandler("onClick"); handler != nil {
		object.CallFunction(handler, nil)
		// 回调抛出的异常不中断事件循环 (P2 无 ErrorBoundary, 打印后继续)
		if err := takeCallbackErr(); err != nil {
			fmt.Fprintf(os.Stderr, "gfx: onClick error: %v\n", err)
		}
		// 回调可能改变了 signal → effect 已标脏
	}
}

// handleKey 键盘事件: 从焦点节点沿祖先链找第一个 onKeyDown 调用。
func (a *app) handleKey(key string) {
	a.mu.Lock()
	n := a.focused
	if n == nil {
		n = a.root
	}
	a.mu.Unlock()
	for n != nil {
		if handler := n.PropHandler("onKeyDown"); handler != nil {
			ev := object.NewObject()
			ev.SetProperty("key", object.NewString(key))
			object.CallFunction(handler, nil, ev)
			if err := takeCallbackErr(); err != nil {
				fmt.Fprintf(os.Stderr, "gfx: onKeyDown error: %v\n", err)
			}
			return
		}
		n = n.Parent
	}
}

// takeCallbackErr 消费最近一次 CallFunction 的异常信号 (Go 侧与值都要清,
// 否则残留信号会在后续内建调用点被误抛, 见 vm.go callbackErr 注释)。
func takeCallbackErr() error {
	if err := object.TakeCallbackError(); err != nil {
		object.TakeCallbackErrorValue()
		return err
	}
	return nil
}

// close 结束应用 (清空 activeApp, 幂等)。
func (a *app) close() {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
	})
}

// ===== 脏矩形 =====

// diffRects 重布局后收集脏矩形: Box 变化的节点 (新旧框) + 属性变化
// 但框未变的节点。返回 nil 表示无脏区。
func (a *app) diffRects() []Rect {
	var rects []Rect
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Box != n.PrevBox {
			rects = append(rects, n.PrevBox, n.Box)
			n.PrevBox = n.Box
		} else if _, dirty := a.dirtyNodes[n]; dirty {
			rects = append(rects, n.Box)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(a.root)
	return rects
}

// markAllPrev 首帧/整帧时同步 PrevBox。
func markAllPrev(n *GuiNode) {
	n.PrevBox = n.Box
	for _, c := range n.Children {
		markAllPrev(c)
	}
}

// mergeRects 贪心合并相交矩形 (控制上屏 InvalidateRect 次数)。
func mergeRects(rects []Rect, maxCount int) []Rect {
	merged := append([]Rect(nil), rects...)
	for {
		if len(merged) <= maxCount {
			break
		}
		// 超额: 合并任意相交的一对
		progress := false
	outer:
		for i := 0; i < len(merged); i++ {
			for j := i + 1; j < len(merged); j++ {
				if u, ok := unionIfOverlap(merged[i], merged[j]); ok {
					merged[i] = u
					merged = append(merged[:j], merged[j+1:]...)
					progress = true
					break outer
				}
			}
		}
		if !progress {
			break
		}
	}
	return merged
}

// unionIfOverlap 两矩形相交 (或相接) 则返回并集。
func unionIfOverlap(a, b Rect) (Rect, bool) {
	if a.W <= 0 || a.H <= 0 {
		return b, true
	}
	if b.W <= 0 || b.H <= 0 {
		return a, true
	}
	overlap := a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
	if !overlap {
		return Rect{}, false
	}
	return Rect{
		X: minInt(a.X, b.X), Y: minInt(a.Y, b.Y),
		W: maxInt(a.X+a.W, b.X+b.W) - minInt(a.X, b.X),
		H: maxInt(a.Y+a.H, b.Y+b.H) - minInt(a.Y, b.Y),
	}, true
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// redraw 布局 + 光栅化 + 上屏 (仅 GUI 线程调用)。
func (a *app) redraw() {
	a.mu.Lock()
	a.needDraw = false
	full := a.fullDirty
	a.fullDirty = false
	dirtyNodes := a.dirtyNodes
	a.dirtyNodes = nil
	root := a.root
	a.mu.Unlock()

	w, h := a.surface.Size()
	if w <= 0 || h <= 0 {
		return
	}
	if a.img == nil || a.img.Bounds().Dx() != w || a.img.Bounds().Dy() != h {
		a.img = image.NewRGBA(image.Rect(0, 0, w, h))
		full = true
	}
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	if full {
		Layout(root, w, h)
		markAllPrev(root)
		FillRect(a.img, Rect{0, 0, w, h}, white)
		Draw(a.img, root)
		a.surface.ShowRegions(a.img, nil)
		return
	}

	// 局部: 重新布局, 对比新旧框收集脏区
	a.dirtyNodes = dirtyNodes // diffRects 读取
	Layout(root, w, h)
	rects := mergeRects(a.diffRects(), 16)
	a.dirtyNodes = nil

	totalArea := 0
	for _, r := range rects {
		totalArea += r.W * r.H
	}
	if totalArea == 0 {
		return
	}
	if totalArea > w*h*85/100 {
		// 脏区覆盖大半屏: 整帧更划算
		markAllPrev(root)
		FillRect(a.img, Rect{0, 0, w, h}, white)
		Draw(a.img, root)
		a.surface.ShowRegions(a.img, nil)
		return
	}

	clipRects := make([]image.Rectangle, 0, len(rects))
	for _, r := range rects {
		// 裁剪到画布
		r.X, r.Y = maxInt(r.X, 0), maxInt(r.Y, 0)
		r.W, r.H = minInt(r.W, w-r.X), minInt(r.H, h-r.Y)
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		FillRect(a.img, r, white)
		DrawClipped(a.img, root, image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H))
		clipRects = append(clipRects, image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H))
	}
	if len(clipRects) > 0 {
		a.surface.ShowRegions(a.img, clipRects)
	}
}

// ===== gx/gfx 模块注册 =====

func init() {
	object.RegisterBuiltinModule("gx/gfx", func() map[string]object.Value {
		return map[string]object.Value{
			"h":                     object.NewBuiltin("h", JSBuiltinH),
			"window":                object.NewBuiltin("window", jsWindow),
			"render":                object.NewBuiltin("render", jsRender),
			"requestAnimationFrame": object.NewBuiltin("requestAnimationFrame", jsRAF),
		}
	})
}

// jsRAF 是 requestAnimationFrame: 以 ~60fps 帧间隔把回调挂到事件循环的
// 定时器调度上 (与 setTimeout 同一线程串行; 回调在下一次循环迭代执行)。
func jsRAF(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("requestAnimationFrame: callback required")
	}
	id := object.GlobalScheduler().SetTimeout(args[0], 16*time.Millisecond)
	return object.NewNumber(float64(id))
}

// jsWindow 包装窗口配置对象: window({title, width, height}) 原样返回,
// 真正建窗在 render() (必须在 GUI 线程)。
func jsWindow(args ...object.Value) object.Value {
	if len(args) > 0 {
		if _, ok := args[0].(*object.Object); ok {
			return args[0]
		}
	}
	return object.NewTypeError("window: config object required, e.g. window({title, width, height})")
}

// jsRender 挂载元素树并创建窗口。
func jsRender(args ...object.Value) object.Value {
	if len(args) < 2 {
		return object.NewTypeError("render: (vnode, windowConfig) required")
	}
	root, ok := args[0].(*GuiNode)
	if !ok {
		return object.NewTypeError("render: first argument must be an element (from h/JSX)")
	}
	cfgVal, ok := args[1].(*object.Object)
	if !ok {
		return object.NewTypeError("render: second argument must be window({...})")
	}

	cfg := WindowConfig{Title: "Gox", Width: 400, Height: 300}
	if v, ok := cfgVal.GetProperty("title"); ok {
		if s, ok := v.(*object.String); ok {
			cfg.Title = s.Value
		}
	}
	if v, ok := cfgVal.GetProperty("width"); ok {
		if n, ok := v.(*object.Number); ok && n.Value > 0 {
			cfg.Width = int(n.Value)
		}
	}
	if v, ok := cfgVal.GetProperty("height"); ok {
		if n, ok := v.(*object.Number); ok && n.Value > 0 {
			cfg.Height = int(n.Value)
		}
	}

	if err := Mount(root, cfg); err != nil {
		return object.NewErrorWithName("Error", "gfx: "+err.Error())
	}
	return object.UndefinedSingleton
}

// Mount 创建窗口并挂载元素树 (JS render 的 Go 层实现, 测试可用假工厂替身)。
// 调用后主 goroutine 锁定 OS 线程 (窗口消息投递到创建线程)。
func Mount(root *GuiNode, cfg WindowConfig) error {
	if defaultFactory == nil {
		return fmt.Errorf("no window backend available on this platform")
	}
	runtime.LockOSThread()

	surface, err := defaultFactory.Create(cfg)
	if err != nil {
		return fmt.Errorf("create window: %w", err)
	}

	a := &app{
		surface:    surface,
		root:       root,
		fullDirty:  true,
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	appMu.Lock()
	activeApp = a
	appMu.Unlock()

	a.redraw() // 首帧
	return nil
}
