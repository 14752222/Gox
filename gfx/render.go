package gfx

import (
	"fmt"
	"image"
	"image/color"
	"runtime"
	"sync"
	"time"

	"github.com/14752222/Gox/object"
)

// gx/gfx 模块与 GUI 应用状态。
//
// JS 侧 API:
//
//	import { h, render, requestAnimationFrame,
//	         clipboardReadText, clipboardWriteText, animate } from "gx/gfx";
//	render(
//	  <window title="Demo" width={320} height={240}>
//	    <column gap={8}>...</column>
//	  </window>
//	);
//
// 窗口配置的写法: JSX 里 <window> 直接作根元素, title/width/height 写在它
// 的属性上; h() 手拼树时第二个参数传普通对象 {title, width, height}, 整个
// 省略则用缺省 (Gox, 400x300)。多窗口 = 多次 render, 每次返回窗口句柄。
//
// render() 挂载元素树并创建窗口后立即返回; 阻塞式的消息泵由宿主入口经
// vm.RunTimersWithPump(gfx.Pump) 驱动 (见 main.go)。
//
// 脏矩形 (P3): effect 写回属性时对所在节点标脏 (dirtyNodes); 重绘时
// 先重新布局, 再对比每节点 Box/PrevBox 差异 (布局位移的兄弟节点也会被
// 捕获), 合并脏矩形后只清空+重绘+上屏受影响区域。总面积超过帧的 85%
// 时退化为整帧重绘。

// app 是**一个窗口**的应用状态 (P3-6 起可同时存在多个, 见 apps 注册表)。
//
// 每个 app 独占一个 Surface、一棵元素树、一套交互态 (悬停/按压/焦点)。
// 跨窗口不共享任何状态: 键盘焦点、拖动目标、快捷键表都是"本窗口"的 ——
// 键盘事件由平台投递到具体窗口, 天然隔离。
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

	// 全局快捷键表 (P3-5): 从树上 menuitem 的 shortcut prop 收集而来。
	// 惰性重建 (表为空时按键触发一次), 因为它只在"菜单项集合变化"时需要更新。
	shortcuts []menuShortcutEntry

	// surfaceClosed 标记"事件源已结束" (P3-6): WaitEvents 返回 false 或
	// EventClose 到达时置位, 由 processEvents 在冲刷完本轮后真正 close。
	surfaceClosed bool
}

var (
	appMu sync.Mutex
	// apps 是全部活动窗口的注册表 (P3-6)。键是 Surface 而不是 app 指针:
	// 事件泵要按"谁还有事件"遍历, 而 surface 是 app 与后端之间唯一稳定的
	// 身份 —— 拿 Surface 反查 app 也正是 WndProc 回调侧的常见需求。
	apps map[Surface]*app
	// activeApp 是"最近 Mount 的那个窗口"。
	//
	// **它不是焦点窗口**, 而是刻意保留的兼容语义 (A 方案): 剪贴板 / 原生
	// 对话框这些"本来就没有明确 owner"的能力继续经它取 surface, 于是单窗口
	// 场景下行为与 P3-5 完全一致 (既有 22 处读取与全部用例零改动)。
	// 多窗口时它们指向最新窗口 —— 这是有意的取舍: 剪贴板本就是进程级资源,
	// 而对话框需要一个 owner (传最近窗口比"传第一个"更符合直觉)。
	activeApp *app
)

// Active 报告是否有已挂载的 GUI (宿主入口据此选择事件循环模式)。
func Active() bool {
	appMu.Lock()
	defer appMu.Unlock()
	return len(apps) > 0
}

// WindowCount 返回当前活动窗口数 (调试/测试用)。
func WindowCount() int {
	appMu.Lock()
	defer appMu.Unlock()
	return len(apps)
}

// registerApp 把新窗口写入注册表并把 activeApp 指向它。
func registerApp(a *app) {
	appMu.Lock()
	if apps == nil {
		apps = map[Surface]*app{}
	}
	apps[a.surface] = a
	activeApp = a
	appMu.Unlock()
}

// unregisterApp 摘掉一个窗口 (close 时调用), 幂等。
//
// activeApp 只在恰好指向被摘掉的那个时才改: 多窗口下关掉一个旧窗口
// 不该让"最近窗口"语义跳回别的窗口 —— 但若关掉的正是它, 就得改指
// 剩下任意一个 (否则 currentApp() 会返回已关闭的窗口)。
func unregisterApp(a *app) {
	appMu.Lock()
	delete(apps, a.surface)
	if activeApp == a {
		activeApp = nil
		for _, other := range apps {
			activeApp = other
			break
		}
	}
	appMu.Unlock()
}

// appsSnapshot 取当前全部**存活**窗口的快照, 并顺手清掉已关闭的条目。
//
// 自愈而不是只读是刻意的: close() 之外的路径 (后端直接销毁窗口、测试里
// 遗留的替身) 都可能让注册表里留下"已经不再送事件"的 app, 而 Pump 若去等
// 它们就会**永久挂住** (假 Surface 的 WaitEvents 只等到超时, 真窗口的
// 已销毁句柄同理)。这里把清理与遍历放在同一把锁里, 保证"快照里全是活的"。
func appsSnapshot() []*app {
	appMu.Lock()
	defer appMu.Unlock()
	out := make([]*app, 0, len(apps))
	for s, a := range apps {
		a.mu.Lock()
		dead := a.closed
		a.mu.Unlock()
		if dead {
			delete(apps, s)
			continue
		}
		out = append(out, a)
	}
	return out
}

// appForSurface 按 Surface 反查 app (WndProc 事件归属 / 后端回调用)。
func appForSurface(s Surface) *app {
	appMu.Lock()
	defer appMu.Unlock()
	return apps[s]
}

// Invalidate 整帧标脏 (resize 等)。
func Invalidate() {
	markFullDirty()
}

// appOfNode 找节点所属的窗口: 沿 Parent 走到根, 再按根盒子认不出窗口,
// 所以改成"沿 Parent 走上去, 再在注册表里找根 == 该节点的 app"。
//
// 为什么需要它: 属性变化 (effect 写回) 发生在节点上, 而节点自己不持有
// 窗口引用。多窗口下"标脏"必须标对窗口, 否则 A 窗口的属性变化会让 B 窗口
// 重绘 (资源浪费) 而 A 自己不重绘 (界面不更新)。
func appOfNode(n *GuiNode) *app {
	if n == nil {
		return nil
	}
	r := n
	for r.Parent != nil {
		r = r.Parent
	}
	appMu.Lock()
	defer appMu.Unlock()
	for _, a := range apps {
		if a.root == r {
			return a
		}
	}
	// 没找到 (节点还没挂载 / 已卸载): 退化为 activeApp。
	// 这不是错误 —— 接线期间的 effect 可能先于 Mount 跑, 丢掉这次标脏
	// 也不影响最终画面 (Mount 自带首帧 fullDirty)。
	return activeApp
}

// markNodeDirty 节点级标脏 (effect 写回属性时调用)。
func markNodeDirty(n *GuiNode) {
	a := appOfNode(n)
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
//
// 第二个参数形式 markFullDirtyFor 是 P3-6 加的: 弹层状态挂在节点上, 而
// "标脏哪个窗口"要按节点归属算 (见 appOfNode)。无参形式沿用 activeApp,
// 给"没有具体节点"的调用点 (resize / Invalidate) 用。
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

// markFullDirtyFor 按节点归属整帧标脏 (弹层展开/收起路径走它)。
func markFullDirtyFor(n *GuiNode) {
	a := appOfNode(n)
	if a == nil {
		return
	}
	a.mu.Lock()
	a.needDraw = true
	a.fullDirty = true
	a.mu.Unlock()
}

// currentApp 返回"最近挂载"的应用。
//
// 语义说明 (P3-6): 它不是"焦点窗口" —— 用户点了哪个窗口不改变它。
// 需要"事件属于哪个窗口"时用 appForSurface, 需要"节点属于哪个窗口"时用
// appOfNode; 只有那些**没有具体归属**的能力 (剪贴板 / 原生对话框 / 菜单
// 模块入口) 才用 currentApp。
func currentApp() *app {
	appMu.Lock()
	defer appMu.Unlock()
	return activeApp
}

// Pump 是事件泵, 作为 vm.RunTimersWithPump 的 pump 回调:
// 等待**全部窗口**的消息 (至多 maxWait, <=0 表示无限) → 处理各窗口事件 →
// 执行 PostTask (只做一次) → 有脏区的窗口各自重绘。
// **全部窗口关闭**后返回 false 结束事件循环。
//
// 单窗口下与 P3-5 逐字节等价: 遍历只有一个元素, 顺序与结论完全一致。
func Pump(maxWait time.Duration) bool {
	list := appsSnapshot()
	if len(list) == 0 {
		return false
	}
	// 等待阶段: 逐个窗口等一遍预算 (见 sliceWait)。注意 WaitEvents 的返回值
	// 不能当作"窗口已关闭"直接退出 —— 只标记即可, 真正的关闭判定放在
	// processEvents 里 (那里才会看到 EventClose)。
	for _, a := range list {
		if !a.surfaceAlive() {
			continue
		}
		if !a.surface.WaitEvents(sliceWait(maxWait, len(list))) {
			a.markSurfaceClosed()
		}
	}
	// 任务队列是全局的: 每次 Pump 只排空一次 (多个窗口的 pump 不该各排一次,
	// 否则同一批任务会被执行多次)。
	DrainTasks()

	survived := 0
	for _, a := range list {
		if a.processEvents() {
			survived++
		}
	}
	return survived > 0
}

// multiWindowWaitCap 是多窗口时单个窗口的等待上限。
//
// 为什么需要它: 单窗口下 maxWait<=0 (无限期) 是合理的 —— 唯一的窗口就是
// 唯一的事件源。多窗口下**不能**对第一个窗口无限期等待: 那样第二个窗口的
// 事件只有在第一个窗口"醒了"之后才会被处理。Win32 的消息队列是线程级共享的
// (所以碰巧也能work), 但 X11 那种"单连接按窗口分发"的后端没有共享队列,
// 会真的卡住。所以多窗口时把无限期换成一个有界切片, 轮流醒来检查所有窗口。
//
// 32ms ≈ 两帧: 足够短, 不至于让"另一个窗口的输入"有明显延迟感;
// 又足够长, 避免空转轮询把 CPU 烧起来。单窗口路径不受影响 (仍无限期睡在
// WaitEvents 里, 与 P3-5 完全一致)。
const multiWindowWaitCap = 32 * time.Millisecond

// sliceWait 把一个等待预算切给 n 个窗口。
//
//   - 只有一个窗口: 原样返回 (无限期 → 真正的阻塞式等待, 零空转);
//   - 多个窗口 + 有限预算: 均分 (保证每个窗口都被轮到);
//   - 多个窗口 + 无限期: 换成 multiWindowWaitCap 的有界切片。
func sliceWait(maxWait time.Duration, n int) time.Duration {
	if n <= 1 {
		return maxWait
	}
	if maxWait <= 0 {
		return multiWindowWaitCap
	}
	d := maxWait / time.Duration(n)
	if d <= 0 {
		d = time.Millisecond
	}
	return d
}

// pump 是单窗口泵 (测试与"只跑一个窗口"的内部调用点用)。
func (a *app) pump(maxWait time.Duration) bool {
	if !a.surface.WaitEvents(maxWait) {
		a.markSurfaceClosed()
	}
	DrainTasks()
	return a.processEvents()
}

// surfaceAlive 报告窗口是否还没被标记关闭。
func (a *app) surfaceAlive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return !a.closed
}

// markSurfaceClosed 标记"事件源已结束" (WaitEvents 返回 false 的窗口)。
// 只标脏不直接 close: 关闭动作要等本轮事件处理完 (processEvents) 再做,
// 否则同一轮里剩下的窗口会被跳过。
func (a *app) markSurfaceClosed() {
	a.mu.Lock()
	a.surfaceClosed = true
	a.mu.Unlock()
}

// processEvents 处理本窗口本轮的全部事件 + 脏区重绘。
// 返回 false 表示本窗口已结束 (不再存活)。
func (a *app) processEvents() bool {
	// 1) 处理窗口事件 (点击 → 命中测试 → onClick, 在 VM 线程执行);
	//    close 先记录, 冲刷完本轮重绘后再退出
	sawClose := false
	a.mu.Lock()
	pending := a.surfaceClosed
	a.mu.Unlock()
	if pending {
		sawClose = true
	}
	for {
		ev, ok := a.takeEvent()
		if !ok {
			break
		}
		a.dispatchEvent(ev)
		if ev.Kind == EventClose {
			sawClose = true
		}
	}
	// 2) 脏区重绘
	// 输入框光标闪烁 (P2-1): 相位翻转时才标脏, 于是每次闪烁只重绘一帧,
	// 而不是 60fps 常驻重绘 (光标闪烁不需要每一帧都变)。
	a.tickCaretBlink()
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

// dispatchEvent 分发一条窗口事件 (从 pump 里拆出来: 单窗口与多窗口共用)。
func (a *app) dispatchEvent(ev Event) {
	switch ev.Kind {
	case EventClose:
		// 由调用方 (processEvents) 统一记录后退出
	case EventMouseDown:
		a.handleMouseDown(ev.X, ev.Y)
	case EventMouseUp:
		// 走完整流程: 结束拖动 (还鼠标捕获) → 清按压态 → 派发点击
		a.handleMouseUp(ev.X, ev.Y)
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
		// 拖动中 (P2-8): 支持鼠标捕获的后端会在窗口外继续送事件, 可以
		// 安心等 MouseUp; 不支持的后端则**永远等不到**, 只能在这里放弃,
		// 否则 dragTarget 卡死 (下次移进窗口时没按键也会拖着滑块跑)。
		if !a.hasPointerCapture() {
			a.endDrag()
		}
	case EventKeyDown:
		a.handleKey(ev.Key, "onKeyDown", ev)
	case EventKeyUp:
		a.handleKey(ev.Key, "onKeyUp", ev)
	case EventResize:
		a.mu.Lock()
		a.needDraw = true
		a.fullDirty = true
		a.mu.Unlock()
		// 屏幕适配 A (2026-09-19 拍板): resize 是**窗口级**事件, 派发给根节点
		// 链上的 onResize({width, height}) —— 不走焦点链 (焦点在哪个输入框上
		// 与"窗口变了多大"无关), 直接从布局根找处理器。载荷字段名与
		// getSystemInfo (设备 API 方案 B) 统一为 width/height, 两处词汇一次定好。
		// 脚本侧包成 signal + 断点 memo 的模式见 docs/gui-patterns.md。
		if root := a.rootNode(); root != nil {
			if h := handlerInChain(root, "onResize"); h != nil {
				arg := object.NewObject()
				arg.SetProperty("width", object.NewNumber(float64(ev.W)))
				arg.SetProperty("height", object.NewNumber(float64(ev.H)))
				a.callHandler(h, "onResize", arg)
			}
		}
	case EventIMECommit:
		// P2-7: 整批插入到当前焦点的编辑框 (焦点不可编辑时内部丢弃)
		a.insertIMECommit(ev.Text)
	}
}

// caretPhase 记录上一次重绘时光标相位的取值: 只有相位翻转的那一帧才需要
// 重绘输入框 (见 tickCaretBlink)。初值与 caretEpoch 时刻的相位一致。
var caretPhase = true

// tickCaretBlink 在光标闪烁相位翻转时把获焦的输入框标脏 (P2-1)。
//
// 它只能"在事件泵醒着的时候"生效: 窗口既没有事件、也没有任何定时器时,
// 泵会在 WaitEvents 里睡着, 此时只有点击/按键才会触发重绘, 光标不闪。
// 需要持续闪烁的应用挂一个 requestAnimationFrame 循环即可
// (testdata/input_demo.js 就是这么做的) —— 浏览器里也是动画帧在驱动光标闪烁。
func (a *app) tickCaretBlink() {
	a.mu.Lock()
	in := inputInChain(a.focused)
	a.mu.Unlock()
	if in == nil {
		return
	}
	visible := caretVisibleAt(time.Now())
	if visible == caretPhase {
		return
	}
	caretPhase = visible
	markNodeDirty(in)
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
		// 没有 onClick 的字段类组件也要能点击获焦: 命中测试只认"带处理器的
		// 节点" (HitTest), input 表面没有处理器, 常规路径会直接判成"点了空白"。
		// 这里退一步用最深命中节点判断, 顺带把光标落到点击位置 (P2-1)。
		if deep := HitTestDeep(root, x, y); deep != nil {
			if ta := textareaInChain(deep); ta != nil && !ta.disabledInChain() {
				a.setFocus(ta)
				a.taSetCaretFromXY(ta, x, y)
				return
			}
			if in := inputInChain(deep); in != nil && !in.disabledInChain() {
				a.setFocus(in)
				a.setCaretFromX(in, x)
				return
			}
		}
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
	// 输入框另加一步: 光标落到点击位置 (脚本自己挂 onClick 时同样适用)
	if in := inputInChain(target); in != nil && in.Tag == "input" {
		a.setCaretFromX(in, x)
	}
	if ta := textareaInChain(target); ta != nil && ta.Tag == "textarea" {
		a.taSetCaretFromXY(ta, x, y)
	}

	// 走 callHandlerValue (内部是 callScriptFn) 而不是 object.CallFunction:
	// 后者经 VM 回调桥, currentVM 为 nil 时**静默返回 undefined** —— 纯 Go
	// 嵌入 / 单测里挂在节点上的 *BuiltinFunction 就永远不执行, 而脚本闭包
	// 与内置回调这两类混在同一个 prop 里, 分流只能在 callScriptFn 做。
	// 这里的 target 是 hitNode 找到的"带 onClick 的最深节点", 处理器的
	// 归属节点就是它, 因此与其它事件路径共用同一套派发逻辑。
	if handler := target.PropHandler("onClick"); handler != nil {
		a.callHandlerValue(handler, "onClick", nil)
	}
}

// setFocus 切换键盘焦点并派发 onBlur/onFocus (各沿祖先链找第一个处理器)。
// 焦点节点自身标脏, 让虚线焦点框在新旧位置各自重绘一次 (局部重绘下
// 旧框必须被该节点的脏矩形覆盖掉, 否则会残留)。
func (a *app) setFocus(target *GuiNode) {
	a.mu.Lock()
	old := a.focused
	a.focused = target
	s := a.surface
	a.mu.Unlock()
	// 输入框 (P2-1) 的"获焦"是画在节点上的状态 (边框颜色 + 是否画光标):
	// 节点级字段让绘制侧不必反查 app。这里与 a.focused 严格同步。
	if old != nil && old != target {
		old.focused = false
	}
	if target != nil {
		target.focused = true
	}
	if old == target {
		return
	}
	// P2-7: 焦点变了, 输入法的开关跟着变 —— 只有落在 input/textarea 上才开,
	// 在按钮/画布上敲字不该弹出候选窗。后端不支持 (X11 / 假 Surface) 时
	// 这里的类型断言直接落空, 退化成"输入法一直开着"。
	if c, ok := s.(imeController); ok {
		c.SetIMEEnabled(imeTarget(target) != nil)
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
	if name == "onKeyDown" && a.handleFieldKey(n, key, ev) {
		return
	}
	// 全局快捷键 (P3-5) 排在字段消费之后: 焦点在输入框里时 Ctrl+S 该不该
	// 触发"保存"? 应该 —— 输入框不消费带 Ctrl 的组合键 (见 input.go),
	// 所以顺序上不会打架。只有"带 Ctrl/Alt"的组合才进快捷键表。
	if name == "onKeyDown" && a.handleShortcut(key, ev.Ctrl, ev.Shift, ev.Alt, ev) {
		return
	}
	// 菜单的键盘导航 (P3-5): 焦点在 menu 标题上时, ←→ 换菜单、↓/Enter 展开。
	if name == "onKeyDown" {
		if m := menuInChain(n); m != nil && a.handleMenuKey(m, key) {
			return
		}
	}
	handler := handlerInChain(n, name)
	if handler == nil {
		// Esc 兜底 (P2-4/P3-5): 优先级 = 菜单 > 下拉框 > 对话框。
		// 从最表层的交互开始收: 菜单弹在下拉之上, 下拉弹在对话框之上。
		if name == "onKeyDown" && key == "Escape" {
			root := a.rootNode()
			if a.closeAnyExpandedMenu(root) {
				return
			}
			if a.closeAnyExpandedSelect(root) {
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
//
// 拖动期间 (P2-8) 走**另一条路**: 事件只喂拖动目标, 不维护悬停链。理由是
// 拖动是"独占"交互 —— 鼠标从 A 拖到 B 的过程中划过一堆控件, 让它们挨个闪
// 悬停高亮既难看, 也暗示"你可以点它们" (实际上这一串移动属于同一个手势)。
func (a *app) handleMouseMove(x, y int) {
	a.mu.Lock()
	drag := a.dragTarget
	a.mu.Unlock()
	if drag != nil {
		a.dragMove(drag, x, y)
		return
	}
	target := HitTestDeep(a.rootNode(), x, y)
	a.setHover(target)
	if h := handlerInChain(target, "onMouseMove"); h != nil {
		a.callHandlerWithPoint(h, "onMouseMove", x, y)
	}
}

// handleWheel 先做内建滚动 (P2-5), 再派发 onWheel({deltaY})。
//
// 滚轮的原生增量向上为正 (Windows WHEEL_DELTA 一格 = 120), 这里一格折算
// scrollNotch 像素; 内容往下滚 = 子内容上移 = offsetY 增大, 所以取负号。
//
// 滚到边界时 scrollBy 返回 false ⇒ 事件继续往外传 (派发 onWheel), 与 DOM 的
// 滚动链一致; 已经在滚动画布上消费掉的滚轮不会触发脚本回调。
// Win32 的 DeltaY 向上为正, 派发给脚本时按 DOM 约定取反 (向下滚为正值),
// 免得两套符号在脚本里打架。
func (a *app) handleWheel(x, y, deltaY int) {
	target := HitTestDeep(a.rootNode(), x, y)
	if sc := scrollInChain(target); sc != nil {
		if sc.scrollBy(-deltaY * scrollNotch / wheelDeltaUnit) {
			return
		}
	}
	// 多行编辑框自己也能滚 (内容比框高时)。与 scroll 同理: 只有真的滚动了
	// 才吞掉滚轮, 到边界继续往外传。
	if ta := textareaInChain(target); ta != nil {
		if ta.taOffsetBy(-deltaY * scrollNotch / wheelDeltaUnit) {
			return
		}
	}
	h := handlerInChain(target, "onWheel")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("deltaY", object.NewNumber(float64(-deltaY)))
	a.callHandler(h, "onWheel", arg)
}

// handleContextMenu 派发 onContextMenu({x, y})。坐标一并给出, 供右键菜单直接使用。
//
// P3-5 起多了两步: ① 先收起已经弹出的右键菜单 (在菜单上再点右键 = 换一个菜单);
// ② 焦点菜单先吃掉这次右键 —— 在弹出的菜单上点右键不该再弹一个菜单。
func (a *app) handleContextMenu(x, y int) {
	root := a.rootNode()
	// 在已弹出的右键菜单上再点右键: 吞掉, 不换位置也不重弹。
	//
	// 判据要用**弹层盒**而不是菜单节点盒: ctx menu 是个零尺寸的定位锚
	// (Box = {x, y, 0, 0}), 拿它去做 Contains 永远为假, 于是"在菜单上点右键"
	// 会被当成外部点击把菜单关掉 —— 用户看到的是"菜单一点右键就消失"。
	// 所以与 handleMouseDown 里的判据保持一致, 都用 menuPopupOf(ctx).Box。
	if ctx := contextMenuNode(root); ctx != nil {
		if p := menuPopupOf(ctx); p != nil && p.Box.Contains(x, y) {
			return
		}
	}
	a.closeContextMenu()
	target := HitTestDeep(root, x, y)
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
	// 右键菜单最先收: 它盖在一切之上, 点它之外任何地方都是"关掉它"。
	// 与下拉框同理 —— 这次按下只服务于收起, 顺便吞掉该次点击。
	if ctx := contextMenuNode(root); ctx != nil {
		if p := menuPopupOf(ctx); p == nil || !p.Box.Contains(x, y) {
			a.closeContextMenu()
			a.swallowClick = true
			a.setHover(nil)
			a.releasePress()
			return
		}
	}
	if a.closeMenuOnOutsideClick(root, x, y) {
		// 这次按下只服务于"收起弹层": 置吞掉标记, 拖动悬停与按压态一并复位
		a.mu.Lock()
		a.swallowClick = true
		a.mu.Unlock()
		a.setHover(nil)
		a.releasePress()
		return
	}
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
	// slider (P2-8): 按下即锁定拖动目标, 并按点击位置**直接跳值** ——
	// 不必"先按住再拖", 与浏览器 `<input type=range>` 的手感一致。
	if sl := sliderInChain(target); sl != nil {
		a.setPress(pressChainOf(sl))
		a.beginDrag(sl)
		a.sliderDrag(sl, x)
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

// closeMenuOnOutsideClick 若有展开中的菜单且 (x,y) 落在**整棵菜单树**
// (菜单栏标题 + 所有下拉) 之外, 收起它并返回 true。
//
// 判据必须比"落在下拉之外"更宽: 点菜单标题本身是"切换菜单"(由标题上的
// 内置处理器接管), 不是"点外面"; 而子菜单叠在父下拉之上, 只判父下拉
// 会把"点在子菜单上"误判成外部点击 —— 于是点子菜单的瞬间菜单就没了。
func (a *app) closeMenuOnOutsideClick(root *GuiNode, x, y int) bool {
	list := expandedMenus(root)
	if len(list) == 0 {
		return false
	}
	for _, m := range list {
		if menuHitArea(m).Contains(x, y) {
			return false
		}
		if p := menuPopupOf(m); p != nil && p.Box.Contains(x, y) {
			return false
		}
	}
	// 收最深的那个即可: closeMenu 会连带收起它的后代, 但同级兄弟菜单
	// (比如菜单栏上另一个开着的) 需要各自收 —— 正常流程保证最多只有一个。
	for _, m := range list {
		a.closeMenu(m)
	}
	return true
}

// menuHitArea 是菜单标题的命中区 (右键菜单没有标题, 用它的弹层代替)。
func menuHitArea(m *GuiNode) Rect {
	if m.ctxMenu {
		if p := menuPopupOf(m); p != nil {
			return p.Box
		}
		return Rect{}
	}
	return m.Box
}

// menuInChain 从 n 起沿祖先链找第一个 menu (菜单键盘导航用: 焦点可能落在
// 菜单标题、或标题下的文本节点上)。
func menuInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "menu" {
			return p
		}
	}
	return nil
}

// releasePress 结束按压态 (MouseUp / 未命中时)。
func (a *app) releasePress() {
	a.setPress(nil)
}

// ===== P2-8 拖动 (slider) =====

// capturer 是 Surface 的**可选能力**: 拖动期间把鼠标事件钉在本窗口上。
//
// 为什么定义成可选接口而不是给 Surface 加两个方法: Surface 有三个实现
// (win32 / x11 / 测试用 fakeSurface), 扩接口就要同步改三处, 而且假 Surface
// 根本没有"窗口"可以捕获。做成类型断言后, 没实现的后端自动退化为
// "拖出窗口即停止跟踪" —— 功能不坏, 只是不跟手。
//
// 方法名必须**导出**: Go 不允许跨包实现未导出方法, 而 win32 是另一个包
// (它 import gfx 来实现 gfx.Surface), 所以 setCapture 这种小写名做不到。
type capturer interface {
	CapturePointer()
	ReleasePointer()
}

// hasPointerCapture 报告当前后端是否支持鼠标捕获。
//
// 它的用途只有一个: 决定"光标离开窗口时要不要放弃拖动"。有捕获的后端会在
// 窗口外继续送 MouseMove/MouseUp, 可以放心等着; 没有捕获的后端则**永远等不到
// 那次 MouseUp** —— 不主动放弃就会留下一个卡死的 dragTarget (下次鼠标移进窗口
// 时, 明明没按键也会拖着滑块跑)。
func (a *app) hasPointerCapture() bool {
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	_, ok := s.(capturer)
	return ok
}

// beginDrag 把 n 设为拖动目标并申请鼠标捕获。
func (a *app) beginDrag(n *GuiNode) {
	a.mu.Lock()
	a.dragTarget = n
	s := a.surface
	a.mu.Unlock()
	if c, ok := s.(capturer); ok {
		c.CapturePointer()
	}
}

// endDrag 结束拖动: 清目标、还捕获、复位"上次派发的值"。
//
// 复位 slideValSet 是必须的: 拖动期间挡重复派发靠的是"和上次派发值相等就跳过",
// 若不复位, 松手后再按同一位置 (值没变) 就不会派发 onInput —— 表现为
// "第一次拖有效, 第二次拖同一个位置没反应"。
func (a *app) endDrag() {
	a.mu.Lock()
	t := a.dragTarget
	a.dragTarget = nil
	s := a.surface
	a.mu.Unlock()
	if t == nil {
		return
	}
	t.slideValSet = false
	if c, ok := s.(capturer); ok {
		c.ReleasePointer()
	}
}

// handleMouseUp 处理左键抬起: 先结束拖动 (还鼠标捕获), 再清按压态,
// 最后才走点击派发。
//
// 顺序不能换: releasePress 与 endDrag 都要在 handleClick **之前**完成,
// 否则一次"拖动结束"会被后面的命中测试当成普通点击再处理一遍 (按压态还没复位,
// 视觉上按钮会一直暗着)。
func (a *app) handleMouseUp(x, y int) {
	a.endDrag()
	a.releasePress()
	a.handleClick(x, y)
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
	// 走 callScriptFn 而不是直接 object.CallFunction: 后者经 VM 回调桥,
	// currentVM 为 nil 时**静默返回 undefined** (纯 Go 嵌入 gfx 的场景),
	// 而 Go 侧的 *BuiltinFunction 根本不需要过桥 (见 canvas.go 的说明)。
	if arg == nil {
		callScriptFn(handler)
	} else {
		callScriptFn(handler, arg)
	}
	if err := takeCallbackErr(); err != nil {
		warnEventError(name, err)
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

// close 结束本窗口 (从注册表摘除, 幂等)。
//
// 与 P3-5 的差异: 不再无条件把 activeApp 清空 —— 那是单窗口的写法,
// 多窗口下会把"最近窗口"错误地清掉 (剩下还有活着的窗口, 但
// currentApp() 返回 nil, 剪贴板/原生对话框全失效)。
func (a *app) close() {
	a.closeOnce.Do(func() {
		a.mu.Lock()
		a.closed = true
		a.mu.Unlock()
		unregisterApp(a)
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
		devFrameTick(true)
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
		devFrameTick(true)
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
		devFrameTick(false)
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
			"render":                object.NewBuiltin("render", jsRender),
			"requestAnimationFrame": object.NewBuiltin("requestAnimationFrame", jsRAF),
			// P3-3 剪贴板 (同步: 脚本与窗口同线程, 直接调原生 API 即为正确线程)
			"clipboardReadText":  object.NewBuiltin("clipboardReadText", jsClipboardReadText),
			"clipboardWriteText": object.NewBuiltin("clipboardWriteText", jsClipboardWriteText),
			// P3-2 过渡动画 (命令式; 声明式走 transition prop, 不经模块)
			"animate": object.NewBuiltin("animate", jsAnimate),
			// P3-5 右键菜单 (就地弹出; 声明式菜单栏走 menubar/menu/menuitem 标签)
			"openContextMenu": object.NewBuiltin("openContextMenu", jsOpenContextMenu),
		}
	})
	// gx/dialog (P3-4) 单列一个模块: 它不依赖元素树, 只依赖"当前有没有窗口",
	// 与 gx/gfx 的绑定关系比剪贴板还弱。
	object.RegisterBuiltinModule("gx/dialog", func() map[string]object.Value {
		return map[string]object.Value{
			"alert":    object.NewBuiltin("alert", jsAlert),
			"confirm":  object.NewBuiltin("confirm", jsConfirm),
			"openFile": object.NewBuiltin("openFile", jsOpenFile),
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

// jsRender 挂载元素树并创建窗口, 接受三种形态:
//
//	render(<window title="T" width={W} height={H}><column .../></window>); // JSX: 窗口配置即根元素
//	render(tree, { title: "T", width: W, height: H });                    // h() 手拼: 普通配置对象
//	render(tree);                                                          // 全默认 (Gox 400x300)
//
// <window> 不是组件: render 在挂载前把它"拆包" —— title/width/height 从它的
// props 读出当窗口配置, 布局根换成它唯一的子元素。它本身不进树、不参与
// 布局与绘制, 所以任何窗口级配置 (未来的 resizable 等) 都应该写在这里,
// 而不是混进布局根的 props。
func jsRender(args ...object.Value) object.Value {
	if len(args) == 0 || len(args) > 2 {
		return object.NewTypeError("render: (element[, config]) required")
	}
	root, ok := args[0].(*GuiNode)
	if !ok {
		return object.NewTypeError("render: first argument must be an element (from h/JSX)")
	}

	// 形态 1: <window> 根元素 —— 配置与内容写在一起
	if root.Tag == "window" {
		if len(args) > 1 {
			return object.NewTypeError("render: <window> element already carries config; drop the second argument")
		}
		if len(root.Children) != 1 {
			return object.NewTypeError("render: <window> needs exactly one child element, got %d", len(root.Children))
		}
		// 拆包: 布局根换成唯一子元素, 并断开它对 <window> 的 Parent 引用 ——
		// wireChild 已经把 Parent 指到了 window 节点上, 而它不参与布局,
		// 任何"沿 Parent 走到树顶"的逻辑 (appOfNode / 弹层贴边) 都必须落在
		// 挂载根上, 否则会拿到一个从未布局过的 0 尺寸节点。
		child := root.Children[0]
		child.Parent = nil
		root.Children = nil
		return mountJS(child, windowConfigFromProps(root))
	}

	// 形态 2/3: 普通根元素 + 可选配置对象
	cfg := defaultWindowConfig()
	if len(args) == 2 {
		cfgVal, ok := args[1].(*object.Object)
		if !ok {
			return object.NewTypeError("render: second argument must be a config object, e.g. {title, width, height}")
		}
		applyWindowConfig(&cfg, cfgVal)
	}
	return mountJS(root, cfg)
}

// defaultWindowConfig 是 render 的缺省窗口配置。
func defaultWindowConfig() WindowConfig {
	return WindowConfig{Title: "Gox", Width: 400, Height: 300}
}

// applyWindowConfig 从普通对象读 title/width/height (与字段级容错口径一致:
// 类型不符的项静默落回缺省, "title 写成数字"这类笔误不至于让窗口开不出来)。
func applyWindowConfig(cfg *WindowConfig, o *object.Object) {
	if v, ok := o.GetProperty("title"); ok {
		if s, ok := v.(*object.String); ok {
			cfg.Title = s.Value
		}
	}
	if v, ok := o.GetProperty("width"); ok {
		if n, ok := v.(*object.Number); ok && n.Value > 0 {
			cfg.Width = int(n.Value)
		}
	}
	if v, ok := o.GetProperty("height"); ok {
		if n, ok := v.(*object.Number); ok && n.Value > 0 {
			cfg.Height = int(n.Value)
		}
	}
}

// windowConfigFromProps 从 <window> 元素的 props 读窗口配置 (容错口径同
// applyWindowConfig: 坏类型的项落回缺省)。
func windowConfigFromProps(n *GuiNode) WindowConfig {
	cfg := defaultWindowConfig()
	if s, ok := n.PropStr("title"); ok {
		cfg.Title = s
	}
	if v, ok := n.PropNum("width"); ok && v > 0 {
		cfg.Width = int(v)
	}
	if v, ok := n.PropNum("height"); ok && v > 0 {
		cfg.Height = int(v)
	}
	return cfg
}

// mountJS 建窗并把句柄包装成 JS 对象 (Mount 失败转成 JS Error)。
func mountJS(root *GuiNode, cfg WindowConfig) object.Value {
	win, err := Mount(root, cfg)
	if err != nil {
		return object.NewErrorWithName("Error", "gfx: "+err.Error())
	}
	return win.jsObject()
}

// jsOpenContextMenu 是 openContextMenu(x, y, items): 在 (x, y) 处就地弹出菜单。
//
// items 是**菜单项节点数组** (menuitem / separator), 所以脚本可以复用与
// 声明式菜单完全相同的写法, 只是不挂在树上而已。非节点元素静默跳过 ——
// 与 options 的容错口径一致 (数据形状不对不该让渲染层 panic)。
func jsOpenContextMenu(args ...object.Value) object.Value {
	if len(args) < 3 {
		return object.NewTypeError("openContextMenu: (x, y, items) required")
	}
	x, okX := args[0].(*object.Number)
	y, okY := args[1].(*object.Number)
	if !okX || !okY {
		return object.NewTypeError("openContextMenu: x and y must be numbers")
	}
	arr, ok := args[2].(*object.Array)
	if !ok {
		return object.NewTypeError("openContextMenu: items must be an array of menuitem elements")
	}
	a := currentApp()
	if a == nil {
		return object.UndefinedSingleton
	}
	items := make([]*GuiNode, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		if n, ok := e.(*GuiNode); ok {
			items = append(items, n)
		}
	}
	a.openContextMenu(int(x.Value), int(y.Value), items)
	return object.UndefinedSingleton
}

// Mount 创建窗口并挂载元素树 (JS render 的 Go 层实现, 测试可用假工厂替身)。
// 调用后主 goroutine 锁定 OS 线程 (窗口消息投递到创建线程)。
//
// P3-6 起可多次调用 (每次开一个新窗口), 返回该窗口的句柄。返回值而不是
// 只返回 error 是必要的: 脚本要能 `w.close()` 关掉**指定的**那个窗口。
func Mount(root *GuiNode, cfg WindowConfig) (*Window, error) {
	if root.Tag == "window" {
		// render 已经把根上的 <window> 拆包过; 走到这说明窗口元素嵌在了
		// 内容里 —— 它不是组件, 渲染成盒子毫无意义, 直接报错更好查。
		return nil, fmt.Errorf("<window> can only be the root element passed to render()")
	}
	if defaultFactory == nil {
		return nil, fmt.Errorf("no window backend available on this platform")
	}
	runtime.LockOSThread()

	surface, err := defaultFactory.Create(cfg)
	if err != nil {
		return nil, fmt.Errorf("create window: %w", err)
	}

	a := &app{
		surface:    surface,
		root:       root,
		fullDirty:  true,
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	registerApp(a)

	a.redraw() // 首帧
	return &Window{a: a}, nil
}
