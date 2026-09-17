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

	// 交互状态 (P1-4 / P2-8): 悬停链与按压捕获目标。
	// 只保存"当前生效"的链, 与新的链做差集即可知道哪些节点需要翻转状态。
	hoverChain []*GuiNode
	pressChain []*GuiNode
	dragTarget *GuiNode // 鼠标捕获目标: 非空时 MouseMove 全部路由给它 (slider 等拖拽)

	// swallowClick 吃掉紧随其后的那次点击 (P2-3): 点在下拉弹层之外时,
	// 这次按下只用来"收起弹层", 不该顺带触发下面的控件。
	// Down 与 Up 是两个事件, 所以这个消息要在两次事件之间留存。
	swallowClick bool
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
	markFullDirty()
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

// markFullDirty 整帧标脏。
//
// 弹层的展开/收起必须走这条: 弹层新覆盖 (或刚让出) 的那片区域不属于任何
// "框发生了变化" 的节点 —— 节点已经被摘掉了, diffRects 从树上根本看不到它,
// 局部重绘的脏矩形表达不了"擦掉刚刚消失的弹层", 结果就是残留一块下拉框。
func markFullDirty() {
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

// currentApp 返回当前挂载的应用 (Go 侧内置回调拿不到 app 指针时用)。
func currentApp() *app {
	appMu.Lock()
	defer appMu.Unlock()
	return activeApp
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
		case EventMouseDown:
			a.handleMouseDown(ev.X, ev.Y)
		case EventMouseUp:
			a.releasePress()
			a.handleClick(ev.X, ev.Y)
		case EventMouseMove:
			a.handleMouseMove(ev.X, ev.Y)
		case EventMouseWheel:
			a.handleWheel(ev.X, ev.Y, ev.DeltaY)
		case EventMouseRightUp:
			a.handleContextMenu(ev.X, ev.Y)
		case EventMouseLeave:
			// 光标离开客户区 / 窗口失活: 清掉悬停与按压态
			a.setHover(nil)
			a.releasePress()
		case EventKeyDown:
			a.handleKey(ev.Key, "onKeyDown", ev)
		case EventKeyUp:
			a.handleKey(ev.Key, "onKeyUp", ev)
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

// rootNode 取当前根节点 (加锁读)。
func (a *app) rootNode() *GuiNode {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.root
}

// handleClick 命中测试并调用 onClick 回调, 同时把命中节点设为键盘焦点。
func (a *app) handleClick(x, y int) {
	a.mu.Lock()
	swallow := a.swallowClick
	a.swallowClick = false
	a.mu.Unlock()
	if swallow {
		// 这次点击的按下阶段已经用于收起下拉弹层 (见 handleMouseDown),
		// 抬起阶段不能再触发下面的控件 —— "点外面收起下拉" 不该顺带按到别的按钮。
		return
	}
	root := a.rootNode()
	target := HitTest(root, x, y)
	if target == nil {
		// 没命中任何处理器。若点落在模态遮罩上 (而不是内容卡片上), 那是
		// "点外部关闭" 语义 (P2-4); 点在卡片身上什么都不做。
		if d := modalAt(root, x, y); d != nil && dialogMaskHit(d, x, y) {
			a.callHandler(d, "onClose", nil)
		}
		return
	}
	// 禁用子树 (P0-3): 既不触发回调, 也不改变键盘焦点
	if target.disabledInChain() {
		return
	}
	// 点击即设为键盘焦点 (键事件沿祖先链寻找 onKeyDown)
	a.setFocus(target)

	if handler := target.PropHandler("onClick"); handler != nil {
		object.CallFunction(handler, nil)
		// 回调抛出的异常不中断事件循环 (P2 无 ErrorBoundary, 打印后继续)
		if err := takeCallbackErr(); err != nil {
			fmt.Fprintf(os.Stderr, "gfx: onClick error: %v\n", err)
		}
		// 回调可能改变了 signal → effect 已标脏
	}
}

// setFocus 切换键盘焦点并派发 onBlur/onFocus (各沿祖先链找第一个处理器)。
// 焦点节点自身标脏, 让虚线焦点框在新旧位置各自重绘一次 (局部重绘下
// 旧框必须被该节点的脏矩形覆盖掉, 否则会残留)。
func (a *app) setFocus(target *GuiNode) {
	a.mu.Lock()
	old := a.focused
	a.focused = target
	a.mu.Unlock()
	if old == target {
		return
	}
	if old != nil {
		if h := handlerInChain(old, "onBlur"); h != nil {
			a.callHandler(h, "onBlur", nil)
		}
		markNodeDirty(old)
	}
	if target != nil {
		if h := handlerInChain(target, "onFocus"); h != nil {
			a.callHandler(h, "onFocus", nil)
		}
		markNodeDirty(target)
	}
}

// handleKey 键盘事件: 先交给焦点链上的字段类组件内部消费 (下拉框的展开/
// 高亮/选择), 未被消费的再沿祖先链找 JS 处理器。
// Tab 遍历仍不做: 需要 focusable 注册表, 留待后续版本 (见 README 的 GUI 限制一节)。
func (a *app) handleKey(key, name string, ev Event) {
	a.mu.Lock()
	n := a.focused
	if n == nil {
		n = a.root
	}
	a.mu.Unlock()
	if n == nil {
		return
	}
	if name == "onKeyDown" && a.handleFieldKey(n, key) {
		return
	}
	handler := handlerInChain(n, name)
	if handler == nil {
		// Esc 兜底 (P2-4): 焦点链上没有处理器时, 先收起展开的下拉框,
		// 否则关掉最上层的对话框。焦点在对话框里的输入控件上时, Esc 会
		// 先被文本框/下拉吃掉了, 走到这里的都是"焦点不在可交互控件里"。
		if name == "onKeyDown" && key == "Escape" {
			if a.closeAnyExpandedSelect(a.rootNode()) {
				return
			}
			a.closeTopDialog()
		}
		return
	}
	arg := object.NewObject()
	arg.SetProperty("key", object.NewString(key))
	arg.SetProperty("ctrl", object.NewBoolean(ev.Ctrl))
	arg.SetProperty("shift", object.NewBoolean(ev.Shift))
	arg.SetProperty("alt", object.NewBoolean(ev.Alt))
	a.callHandler(handler, name, arg)
}

// handleMouseMove 维护悬停链并派发 onMouseMove({x, y})。
// 事件频率最高: 悬停链未变化时不标脏 (P1-4 的性能前提)。
func (a *app) handleMouseMove(x, y int) {
	target := HitTestDeep(a.rootNode(), x, y)
	a.setHover(target)
	if h := handlerInChain(target, "onMouseMove"); h != nil {
		a.callHandlerWithPoint(h, "onMouseMove", x, y)
	}
}

// handleWheel 派发 onWheel({deltaY})。Win32 的 DeltaY 向上为正,
// 这里按 DOM 约定取反 (向下滚为正值), 免得两套符号在脚本里打架。
func (a *app) handleWheel(x, y, deltaY int) {
	target := HitTestDeep(a.rootNode(), x, y)
	h := handlerInChain(target, "onWheel")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("deltaY", object.NewNumber(float64(-deltaY)))
	a.callHandler(h, "onWheel", arg)
}

// handleContextMenu 派发 onContextMenu({x, y})。坐标一并给出, 供
// 右键菜单就地弹出 (P3-5)。
func (a *app) handleContextMenu(x, y int) {
	target := HitTestDeep(a.rootNode(), x, y)
	h := handlerInChain(target, "onContextMenu")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("x", object.NewNumber(float64(x)))
	arg.SetProperty("y", object.NewNumber(float64(y)))
	a.callHandler(h, "onContextMenu", arg)
}

// handleMouseDown 记录按压目标 (视觉按压态 + 后续拖拽捕获的入口),
// 并处理"点在下拉弹层之外 → 收起下拉且吞掉这次点击"。
// 只有落在有交互意义的节点上才记录按压态, 点空白处不该出现按压态。
func (a *app) handleMouseDown(x, y int) {
	root := a.rootNode()
	if a.closeSelectOnOutsideClick(root, x, y) {
		// 这次按下只服务于"收起弹层": 置吞掉标记, 拖动悬停与按压态一并复位
		a.mu.Lock()
		a.swallowClick = true
		a.mu.Unlock()
		a.setHover(nil)
		a.releasePress()
		return
	}
	target := HitTestDeep(root, x, y)
	if target == nil || target.disabledInChain() {
		a.releasePress()
		return
	}
	a.setPress(pressChainOf(target))
}

// closeSelectOnOutsideClick 若有展开中的下拉框且 (x,y) 落在其弹层之外,
// 收起它并返回 true。同时只处理一个: 打开新下拉前旧的一定已经收起了
// (见 openSelect), 所以树上最多只有一个展开的弹层。
func (a *app) closeSelectOnOutsideClick(root *GuiNode, x, y int) bool {
	for _, sel := range expandedSelects(root) {
		if sel.popup != nil && sel.popup.Box.Contains(x, y) {
			continue
		}
		a.closeSelect(sel)
		return true
	}
	return false
}

// releasePress 结束按压态 (MouseUp / 未命中时)。
func (a *app) releasePress() {
	a.setPress(nil)
}

// callHandler 调用节点上的事件回调: arg 为 nil 表示无参数。异常打印不中断事件循环。
func (a *app) callHandler(n *GuiNode, name string, arg object.Value) {
	a.callHandlerValue(n.PropHandler(name), name, arg)
}

// callHandlerValue 用原始函数值调用回调 (Go 侧内置处理器包装脚本回调时用,
// 与 callHandler 共享同一套异常处理)。
func (a *app) callHandlerValue(handler object.Value, name string, arg object.Value) {
	if handler == nil {
		return
	}
	if arg == nil {
		object.CallFunction(handler, nil)
	} else {
		object.CallFunction(handler, nil, arg)
	}
	if err := takeCallbackErr(); err != nil {
		fmt.Fprintf(os.Stderr, "gfx: %s error: %v\n", name, err)
	}
}

// callHandlerWithPoint 用 {x, y} 参数调用回调 (鼠标位置类事件)。
func (a *app) callHandlerWithPoint(n *GuiNode, name string, x, y int) {
	arg := object.NewObject()
	arg.SetProperty("x", object.NewNumber(float64(x)))
	arg.SetProperty("y", object.NewNumber(float64(y)))
	a.callHandler(n, name, arg)
}

// ===== 悬停 / 按压链维护 =====

// setHover 把悬停态从旧链迁到新链, 只对发生变化的节点标脏。
func (a *app) setHover(target *GuiNode) {
	a.mu.Lock()
	prev := a.hoverChain
	a.hoverChain = hoverChainOf(target)
	next := a.hoverChain
	a.mu.Unlock()
	applyChain(prev, next, func(n *GuiNode, on bool) {
		if n.hovered != on {
			n.hovered = on
			markNodeDirty(n)
		}
	})
}

// setPress 设置按压链 (nil = 全部释放)。
func (a *app) setPress(chain []*GuiNode) {
	a.mu.Lock()
	prev := a.pressChain
	a.pressChain = chain
	a.mu.Unlock()
	applyChain(prev, chain, func(n *GuiNode, on bool) {
		if n.pressed != on {
			n.pressed = on
			markNodeDirty(n)
		}
	})
}

// hoverChainOf 收集祖先链上需要悬停反馈的节点 (含被悬停组件的祖先组件,
// 这样悬停在按钮文字上时按钮本体也会亮起)。
func hoverChainOf(target *GuiNode) []*GuiNode {
	var chain []*GuiNode
	for p := target; p != nil; p = p.Parent {
		if p.hoverable() {
			chain = append(chain, p)
		}
	}
	return chain
}

// pressChainOf 收集按压反馈节点 (与悬停链同一批组件)。
func pressChainOf(target *GuiNode) []*GuiNode {
	var chain []*GuiNode
	for p := target; p != nil; p = p.Parent {
		if p.pressable() {
			chain = append(chain, p)
		}
	}
	return chain
}

// applyChain 对新旧链做差集: 离开的置 false, 进入的置 true。
// 用线性查找而非 map: 链长 = 树深 (通常 <10), 建 map 反而更贵。
func applyChain(prev, next []*GuiNode, set func(n *GuiNode, on bool)) {
	for _, n := range prev {
		if !containsNode(next, n) {
			set(n, false)
		}
	}
	for _, n := range next {
		if !containsNode(prev, n) {
			set(n, true)
		}
	}
}

func containsNode(list []*GuiNode, n *GuiNode) bool {
	for _, x := range list {
		if x == n {
			return true
		}
	}
	return false
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
		a.drawFocusRing(a.img)
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
		a.drawFocusRing(a.img)
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
		// 焦点框在所有内容之上重画一次: 它可能横跨多个脏区, 逐区补画
		// 反而更绕; 框始终画在焦点节点自身的盒内, 所以仍落在脏区内。
		a.drawFocusRing(a.img)
		a.surface.ShowRegions(a.img, clipRects)
	}
}

// drawFocusRing 给当前焦点节点画虚线框 (P1-3)。根节点接焦时不需要
// 焦点反馈 (点击空白处即焦点回到根), 可用根节点 props.hideFocusRing
// 整体关闭 (演示脚本里想拍"无焦点框"的画面时用)。
func (a *app) drawFocusRing(img *image.RGBA) {
	a.mu.Lock()
	n := a.focused
	root := a.root
	a.mu.Unlock()
	if n == nil || root == nil || n == root {
		return
	}
	if hide, _ := root.PropBool("hideFocusRing"); hide {
		return
	}
	if n.disabledInChain() {
		return
	}
	// 焦点落在弹层自身 (点遮罩会把焦点给 dialog) 时不画: 那个框会绕着整个
	// 窗口画一圈虚线, 既没有意义又很醒目。
	if n.isOverlay() {
		return
	}
	// 焦点在"已关闭的弹层"的子树里时也不画: dialog 关掉只是 open=false,
	// 节点仍在树上 (脚本没销毁它), 整支都不绘制, 焦点框不能自己冒出来。
	if !focusPathVisible(n) {
		return
	}
	// 焦点框画在 Draw 之后, 会浮在遮罩之上: 焦点节点被打开的弹层盖住时
	// 不该画 (否则是"透过遮罩的幽灵框")。
	if coveredByOverlay(n, root) {
		return
	}
	// 内缩 1px: 不覆盖节点自己的边框 (button 缺省有 1px 边), 框看得清
	r := Rect{X: n.Box.X + 1, Y: n.Box.Y + 1, W: n.Box.W - 2, H: n.Box.H - 2}
	if r.W < 2 || r.H < 2 {
		return
	}
	StrokeDashedRect(img, r, colorFocusRing, 2, 2)
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
