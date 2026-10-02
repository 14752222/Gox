package gfx

import (
	"strconv"
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
	a     *app
	mu    sync.Mutex
	obj   *object.Object // 惰性构造的 JS 对象 (同一窗口复用同一个对象)
	title string         // 最近一次设置的标题 (后端不支持时 title() 也能读回)

	// 窗口管理状态 (§四 窗口/系统缺口, 与 title 同一手法): 后端不支持时
	// 句柄自己记得住, 于是"设置-读回"在任何后端上都是一致的 —— 脚本不必
	// 探测平台能力 (canIUse 是给"要不要用"用的, 不是给"读回自己刚设的值"用的)。
	level      string // "" / "normal" / "top" / "bottom"
	fullscreen bool
	resizable  bool // 缺省 true (配置里用 NoResize 表达 false)
	hasResize  bool // 是否用户显式设过 resizable
	cursor     string
	curSet     bool
}

// windowController 是 Surface 的**可选能力**: 运行期改标题 / 改客户区尺寸
// (与 capturer / nativeDialogHost / imeController 同一模式: 不扩 Surface
// 接口, 类型断言落空即静默降级 no-op —— 改不了标题不该让应用崩)。
//
// 这一组与下面 windowManager 分开是**刻意的**: 前者的平台实现差异极小
// (每个后端都改得了标题与尺寸), 后者差异极大 (X11 的层级要 WM 配合、
// 移动端根本没有"窗口位置")。分成两个接口, 后端可以只实现其中一组,
// 两条断言各自落空各自降级, 不必为了补一个能力被迫实现一堆空方法。
type windowController interface {
	SetTitle(title string)
	ResizeClient(w, h int)
}

// windowManager 是 Surface 的**可选能力**: 窗口位置 / 层级 / 尺寸约束 /
// 全屏 / 激活。
//
// 方法名必须**导出**: win32 / x11 / cocoa 是另外的包 (它们 import gfx 来
// 实现 gfx.Surface), Go 不允许跨包实现未导出方法 —— 与 capturer 同款理由。
type windowManager interface {
	// MoveTo 把窗口外框左上角移到屏幕坐标 (x, y)。
	MoveTo(x, y int)
	// SetLevel 设层级: "normal" / "top" / "bottom"。
	SetLevel(level string)
	// SetSizeConstraints 设用户缩放时的尺寸钳位 (0 = 该方向不约束)。
	SetSizeConstraints(minW, minH, maxW, maxH int)
	// SetResizable 开关用户拖边框改尺寸。
	SetResizable(on bool)
	// SetFullscreen 进出全屏。
	SetFullscreen(on bool)
	// Activate 把窗口带到前台 (模态被挡时点父窗口要把子窗口顶上来)。
	Activate()
}

// cursorHost 是 Surface 的**可选能力**: 设置鼠标光标形状 (见 cursor.go
// 的形状表)。不支持的后端静默 no-op —— 光标形状纯属观感, 拿不到就算了。
type cursorHost interface {
	SetCursor(shape string)
}

// boundsProvider 是 Surface 的**可选能力**: 读窗口外框在屏幕坐标系里的位置
// 与尺寸。有了它 bounds() 才是真读数; 没有时退化为"最近一次 moveTo /
// EventMove 记下的值 + Surface.Size() 当尺寸"。
type boundsProvider interface {
	Bounds() (x, y, w, h int)
}

// App 返回底层 app (Go 侧持有句柄时用; 测试用它断言窗口状态)。
func (w *Window) App() *app { return w.a }

// ID 返回窗口号 (Mount 时分配, 永不复用)。
//
// 脚本侧的对应物是句柄上的 `id()`; 之所以 Go 侧也要一个, 是因为
// "按窗口记账"的设施 (gx/router 的导航栈、gx/screen 的所在显示器)
// 都活在内核里, 它们需要的是一个不依赖 Surface 指针的稳定身份。
func (w *Window) ID() int {
	if w == nil || w.a == nil {
		return 0
	}
	return w.a.id
}

// appScope 返回一个窗口的路由作用域标识 ("win:<id>")。
//
// 命名规则 (以及为什么不是裸 id): 作用域既可能是自动的窗口作用域, 也可能是
// 脚本显式起的名字 ("main" / "popup"), 两者共用一个名字空间。加前缀让
// "自动的"与"显式的"在调试输出里一眼可分, 也避免脚本用 "win:1" 撞上自动值。
func appScope(a *app) string {
	if a == nil {
		return ""
	}
	return "win:" + strconv.Itoa(a.id)
}

// Close 关闭本窗口。可在任意 goroutine 调用 (内部经 Post 投回 GUI 线程),
// 重复调用无副作用 (底下的 close 是幂等的)。
func (w *Window) Close() {
	a := w.a
	if a == nil {
		return
	}
	Post(func() { a.close() })
}

// SetTitle 改窗口标题。后端不支持时只更新句柄内记录 (title() 仍读得回),
// 不报错。与 close 不同**不需要 Post**: 非破坏性调用, 不动注册表、没有
// "本轮还在遍历谁"的时序问题 (脚本本来就在 GUI 线程上执行)。
func (w *Window) SetTitle(title string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.title = title
	w.mu.Unlock()
	if c, ok := w.Surface().(windowController); ok {
		c.SetTitle(title)
	}
}

// Title 返回最近一次设置的标题。
func (w *Window) Title() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.title
}

// Resize 改窗口**客户区**尺寸 (与 WindowConfig.Width/Height 同一口径)。
// 支持的后端会连带产生 EventResize (win32 的 WM_SIZE / fake 的主动投递),
// 于是 onResize → useWindowSize 整条链自动通电; 不支持时 no-op。
func (w *Window) Resize(width, height int) {
	if w == nil || width <= 0 || height <= 0 {
		return
	}
	if c, ok := w.Surface().(windowController); ok {
		c.ResizeClient(width, height)
	}
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

// ===== 窗口管理 (位置 / 层级 / 尺寸约束 / 全屏) =====

// manager 取本窗口后端的管理能力 (不支持时为 nil)。
func (w *Window) manager() windowManager {
	if m, ok := w.Surface().(windowManager); ok {
		return m
	}
	return nil
}

// MoveTo 把窗口外框左上角移到屏幕坐标 (x, y)。可传负数 (多屏拼接时左屏
// 的坐标就是负的)。负值**不能**当"参数非法"拒绝 —— 这是多屏下最常见的
// 真实坐标, 与 Resize 的"<=0 拒绝"口径完全不同, 所以这里只判 nil 与
// 已关闭, 不判范围。
func (w *Window) MoveTo(x, y int) {
	if w == nil || w.closed() {
		return
	}
	w.mu.Lock()
	w.a.mu.Lock()
	w.a.posX, w.a.posY, w.a.hasPos = x, y, true
	w.a.mu.Unlock()
	w.mu.Unlock()
	if m := w.manager(); m != nil {
		m.MoveTo(x, y)
	}
}

// Center 把窗口居中到它**当前所在显示器的工作区** (排除任务栏/状态栏)。
//
// 先读自身尺寸再算落点: 居中需要"我多大", 而后端读回来的外框尺寸才是
// 真实值 (脚本可能刚 resize 过, 也可能后端带边框)。读不到就退回配置尺寸。
func (w *Window) Center() {
	if w == nil || w.closed() {
		return
	}
	d, ok := displayOfWorkWindow(w)
	if !ok {
		return
	}
	ww, wh := w.outerSize()
	x := d.WorkX + (d.WorkW-ww)/2
	y := d.WorkY + (d.WorkH-wh)/2
	if d.WorkW <= 0 || d.WorkH <= 0 { // 工作区没报: 退回整屏
		x = d.X + (d.W-ww)/2
		y = d.Y + (d.H-wh)/2
	}
	w.MoveTo(x, y)
}

// outerSize 取窗口尺寸 (优先后端真读数, 否则退回 Surface 客户区尺寸)。
func (w *Window) outerSize() (int, int) {
	if bp, ok := w.Surface().(boundsProvider); ok {
		if _, _, bw, bh := bp.Bounds(); bw > 0 && bh > 0 {
			return bw, bh
		}
	}
	if s := w.Surface(); s != nil {
		return s.Size()
	}
	return 0, 0
}

// Bounds 读窗口外框的屏幕坐标与尺寸 (后端不支持位置时 x/y 退回最近一次
// 记录值)。返回的 w/h 是**外框**尺寸, 而 Surface.Size() 是客户区 ——
// 两者差一个标题栏与边框, 这是平台事实, 不做换算 (换不准)。
func (w *Window) Bounds() (x, y, width, height int) {
	if w == nil || w.a == nil {
		return 0, 0, 0, 0
	}
	if bp, ok := w.Surface().(boundsProvider); ok {
		if bx, by, bw, bh := bp.Bounds(); bw > 0 && bh > 0 {
			return bx, by, bw, bh
		}
	}
	w.a.mu.Lock()
	x, y = w.a.posX, w.a.posY
	w.a.mu.Unlock()
	width, height = w.outerSize()
	return x, y, width, height
}

// SetLevel 设窗口层级 ("normal" / "top" / "bottom" 之外的取值归一为 normal)。
func (w *Window) SetLevel(level string) {
	if w == nil {
		return
	}
	lv := windowLevel(level)
	w.mu.Lock()
	w.level = lv
	w.mu.Unlock()
	if m := w.manager(); m != nil {
		m.SetLevel(lv)
	}
}

// Level 返回最近一次设置的层级 ("normal" / "top" / "bottom")。
func (w *Window) Level() string {
	if w == nil {
		return "normal"
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.level == "" {
		return "normal"
	}
	return w.level
}

// SetSizeConstraints 设用户缩放窗口时的尺寸钳位 (0 = 该方向不约束)。
// 负数按 0 处理 —— "负的宽度上限"没有任何合理语义, 静默归一比让后端去
// 处理越界值安全 (win32 的 MINMAXINFO 收到负值会得到"拖不动"的怪窗口)。
func (w *Window) SetSizeConstraints(minW, minH, maxW, maxH int) {
	if w == nil {
		return
	}
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		return v
	}
	minW, minH, maxW, maxH = clamp(minW), clamp(minH), clamp(maxW), clamp(maxH)
	if m := w.manager(); m != nil {
		m.SetSizeConstraints(minW, minH, maxW, maxH)
	}
}

// SetFullscreen 进出全屏。
func (w *Window) SetFullscreen(on bool) {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.fullscreen = on
	w.mu.Unlock()
	if m := w.manager(); m != nil {
		m.SetFullscreen(on)
	}
}

// IsFullscreen 返回最近一次设置的全屏态。
func (w *Window) IsFullscreen() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.fullscreen
}

// SetResizable 设"用户能不能拖边框改尺寸"。与 SetSizeConstraints 是两件事:
// 前者是开关, 后者是开着的钳位范围。
func (w *Window) SetResizable(on bool) {
	if w == nil || w.closed() {
		return
	}
	w.mu.Lock()
	w.resizable, w.hasResize = on, true
	w.mu.Unlock()
	if m := w.manager(); m != nil {
		m.SetResizable(on)
	}
}

// IsResizable 返回最近一次设置的缩放开关 (未设过时读配置的 NoResize 反值)。
func (w *Window) IsResizable() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.resizable
}

// Activate 把窗口带到前台。
func (w *Window) Activate() {
	if w == nil || w.closed() {
		return
	}
	if m := w.manager(); m != nil {
		m.Activate()
	}
}

// SetCursor 设窗口级光标 (形状名见 cursor.go); 空串表示"交还给节点上的
// cursor prop 决定"。窗口级是**覆盖**: 忙等状态下脚本可以强行指成 "wait",
// 清掉之后立刻回到"鼠标底下那个节点说了算"。
func (w *Window) SetCursor(shape string) {
	if w == nil || w.a == nil {
		return
	}
	w.mu.Lock()
	w.cursor, w.curSet = shape, true
	w.mu.Unlock()
	w.a.setCursorOverride(shape)
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
	// 窗口身份 (P3-7): id() 是稳定窗口号; scope() 是它在 gx/router 里的路由
	// 作用域名 (两者都是方法而不是属性 —— 属性会被快照, 而句柄对象可能在
	// 窗口创建前后被复用, 方法永远读的是当前值)。
	o.SetProperty("id", object.NewBuiltin("id", func(args ...object.Value) object.Value {
		return object.NewNumber(float64(w.ID()))
	}))
	o.SetProperty("scope", object.NewBuiltin("scope", func(args ...object.Value) object.Value {
		return object.NewString(appScope(w.a))
	}))
	// __goxWindow 是给内核自己用的内省口 (gx/screen 的 screenOf(win) /
	// gx/router 的 sync([wa, wb]) 都靠它把句柄还原成 Go 指针)。
	// 下划线开头 + 返回一个不可用的内部值: 脚本即使调到也拿不到任何东西。
	o.SetProperty("__goxWindow", object.NewBuiltin("__goxWindow", func(args ...object.Value) object.Value {
		return &windowRefValue{w: w}
	}))
	o.SetProperty("close", object.NewBuiltin("close", func(args ...object.Value) object.Value {
		w.Close()
		return object.UndefinedSingleton
	}))
	// closed 做成方法而不是布尔属性: 属性值会在构造时被快照, 而脚本需要
	// 的是"此刻关没关" —— 快照会永远返回 false, 是个隐蔽的坑。
	o.SetProperty("isClosed", object.NewBuiltin("isClosed", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.closed())
	}))
	// 运行期窗口控制 (§四 窗口/系统缺口, 2026-09-19): title()/setTitle(t)/
	// resize(w,h)。title 同样做成方法 (窗口标题随时会变, 快照属性会过期)。
	o.SetProperty("title", object.NewBuiltin("title", func(args ...object.Value) object.Value {
		return object.NewString(w.Title())
	}))
	o.SetProperty("setTitle", object.NewBuiltin("setTitle", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("setTitle: title required")
		}
		w.SetTitle(object.ToString(args[0]))
		return object.UndefinedSingleton
	}))
	o.SetProperty("resize", object.NewBuiltin("resize", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewTypeError("resize: (width, height) required")
		}
		cw, okW := args[0].(*object.Number)
		ch, okH := args[1].(*object.Number)
		if !okW || !okH {
			return object.NewTypeError("resize: width and height must be numbers")
		}
		w.Resize(int(cw.Value), int(ch.Value))
		return object.UndefinedSingleton
	}))

	// ===== 窗口管理 (§四 窗口/系统缺口) =====
	//
	// 全部做成方法而不是属性: 位置/层级/全屏态都会在窗口生命周期里变,
	// 属性值在构造时被快照, 会永远返回初始值 (与 title 同一条理由)。

	// bounds() → {x, y, width, height}
	o.SetProperty("bounds", object.NewBuiltin("bounds", func(args ...object.Value) object.Value {
		x, y, bw, bh := w.Bounds()
		return rectObject(x, y, bw, bh)
	}))
	// position() → {x, y} (只想读位置时的短写法)
	o.SetProperty("position", object.NewBuiltin("position", func(args ...object.Value) object.Value {
		x, y, _, _ := w.Bounds()
		return rectObject(x, y, 0, 0)
	}))
	o.SetProperty("moveTo", object.NewBuiltin("moveTo", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.NewTypeError("moveTo: (x, y) required")
		}
		nx, okX := args[0].(*object.Number)
		ny, okY := args[1].(*object.Number)
		if !okX || !okY {
			return object.NewTypeError("moveTo: x and y must be numbers")
		}
		w.MoveTo(int(nx.Value), int(ny.Value))
		return object.UndefinedSingleton
	}))
	o.SetProperty("center", object.NewBuiltin("center", func(args ...object.Value) object.Value {
		w.Center()
		return object.UndefinedSingleton
	}))
	o.SetProperty("level", object.NewBuiltin("level", func(args ...object.Value) object.Value {
		return object.NewString(w.Level())
	}))
	o.SetProperty("setLevel", object.NewBuiltin("setLevel", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("setLevel: level required")
		}
		w.SetLevel(object.ToString(args[0]))
		return object.UndefinedSingleton
	}))
	// setConstraints({minWidth, minHeight, maxWidth, maxHeight}) —— 四项全可省,
	// 省略即"该方向不约束"。传非对象静默 no-op (与配置解析的容错口径一致)。
	o.SetProperty("setConstraints", object.NewBuiltin("setConstraints", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("setConstraints: (options) required")
		}
		o, ok := args[0].(*object.Object)
		if !ok {
			return object.NewTypeError("setConstraints: options must be an object")
		}
		w.SetSizeConstraints(
			objIntProp(o, "minWidth"), objIntProp(o, "minHeight"),
			objIntProp(o, "maxWidth"), objIntProp(o, "maxHeight"))
		return object.UndefinedSingleton
	}))
	o.SetProperty("setResizable", object.NewBuiltin("setResizable", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewTypeError("setResizable: (on) required")
		}
		w.SetResizable(args[0].IsTruthy())
		return object.UndefinedSingleton
	}))
	o.SetProperty("isResizable", object.NewBuiltin("isResizable", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.IsResizable())
	}))
	o.SetProperty("setFullscreen", object.NewBuiltin("setFullscreen", func(args ...object.Value) object.Value {
		on := true
		if len(args) > 0 {
			on = args[0].IsTruthy()
		}
		w.SetFullscreen(on)
		return object.UndefinedSingleton
	}))
	o.SetProperty("isFullscreen", object.NewBuiltin("isFullscreen", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.IsFullscreen())
	}))
	o.SetProperty("activate", object.NewBuiltin("activate", func(args ...object.Value) object.Value {
		w.Activate()
		return object.UndefinedSingleton
	}))
	// setCursor(shape) / setCursor(null) —— null/undefined/空串都表示"清掉覆盖"。
	o.SetProperty("setCursor", object.NewBuiltin("setCursor", func(args ...object.Value) object.Value {
		if len(args) == 0 || args[0] == nil || args[0] == object.UndefinedSingleton || args[0] == object.NullSingleton {
			w.SetCursor("")
			return object.UndefinedSingleton
		}
		w.SetCursor(object.ToString(args[0]))
		return object.UndefinedSingleton
	}))
	// isModal() / modalParent(): 模态子窗口问"我是谁的模态", 父窗口问
	// "我被谁挡着"。两个都做成方法 —— 模态关系在窗口存活期间会变 (子窗口
	// 一关, 父窗口就不再被挡), 属性快照会撒谎。
	o.SetProperty("isModal", object.NewBuiltin("isModal", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.IsModal())
	}))
	// isBlocked() 是父窗口的视角: "我现在被一个模态子窗口挡着吗"。
	// 脚本用它把主界面画成禁用态 (模态的全部价值之一就是"用户看得见
	// 自己进不去")。
	o.SetProperty("isBlocked", object.NewBuiltin("isBlocked", func(args ...object.Value) object.Value {
		return object.NewBoolean(w.IsBlocked())
	}))
	o.SetProperty("modalParent", object.NewBuiltin("modalParent", func(args ...object.Value) object.Value {
		if p := w.ModalParent(); p != nil {
			return p.jsObject()
		}
		return object.NullSingleton
	}))
	o.SetProperty("modalChild", object.NewBuiltin("modalChild", func(args ...object.Value) object.Value {
		if c := w.ModalChild(); c != nil {
			return c.jsObject()
		}
		return object.NullSingleton
	}))
	w.obj = o
	return o
}

// rectObject 造一个 {x, y, ...} 的普通对象 (窗口几何的返回形状)。
// width/height 为 0 时不挂这两个键 (position() 只要 x/y)。
func rectObject(x, y, w, h int) object.Value {
	o := object.NewObject()
	o.SetProperty("x", object.NewNumber(float64(x)))
	o.SetProperty("y", object.NewNumber(float64(y)))
	if w > 0 || h > 0 {
		o.SetProperty("width", object.NewNumber(float64(w)))
		o.SetProperty("height", object.NewNumber(float64(h)))
	}
	return o
}

// objIntProp 读对象的数字属性 (缺失/类型不符 → 0)。窗口约束的容错口径:
// 坏值当作"没设", 而不是让整次调用失败。
func objIntProp(o *object.Object, name string) int {
	v, ok := o.GetProperty(name)
	if !ok {
		return 0
	}
	n, ok := v.(*object.Number)
	if !ok {
		return 0
	}
	return int(n.Value)
}

// jsWindowObject 把 Go 侧句柄转成 JS 对象 (宿主嵌入时用)。
func jsWindowObject(w *Window) object.Value {
	if w == nil {
		return object.UndefinedSingleton
	}
	return w.jsObject()
}
