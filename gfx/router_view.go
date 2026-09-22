package gfx

import (
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// routerRefValue 是 router 对象在 Go 侧的可识别标记 (与 windowRefValue 同一手法):
// 脚本通过 <RouterView router={r}> 把 router 传回来时, 内核靠它还原出 Go 指针。
type routerRefValue struct{ r *router }

func (v *routerRefValue) Type() object.ObjectType { return object.ObjectType("ROUTER_REF") }
func (v *routerRefValue) Inspect() string         { return "<router ref>" }
func (v *routerRefValue) IsTruthy() bool          { return true }
func (v *routerRefValue) GetProperty(string) (object.Value, bool) {
	return object.UndefinedSingleton, false
}
func (v *routerRefValue) SetProperty(string, object.Value) {}

// ===== gx/router 的视图层: RouterView / RouterLink / useRoute =====
//
// 这一层把 router.go 的"路由状态"变成屏幕上的树。核心机制与 gx/view 的
// Show / For 完全同源 (宿主 slot + 可保活的分支 + runEffect 驱动), 差别只在
// "换哪一支"的判断依据是路由状态而不是脚本的布尔值。
//
// ## 为什么页面缓存是必须的 (而不是"切页就销毁更省内存")
//
// 本引擎有一条硬约束: **销毁后的静态子树无法复活** (disposeNode 会摘掉
// effect 接线, 再挂回去只是一棵不再响应式的死树, 见 node.go / view.go 的
// 反复说明)。这条约束把"页面缓存"从优化项变成了正确性要求:
//
//	折叠态从平展切成半折 → 单栏变双栏   → 页面要重新组织
//	窗口从这块屏拖到那块屏 → 尺寸/缩放变了 → 界面要重排
//	返回上一页 → 那一页的滚动位置/草稿/局部 signal 还在不在?
//
// 于是本层提供**两档状态保持**, 页面按需选:
//
//	{ keepAlive: true }  子树保活 —— 离开时只从 Children 摘出去 (不销毁),
//	                     回来原样挂回: 滚动位置、输入焦点、局部 signal 全在。
//	useRouteState()      状态袋 —— 状态存在**栈项**上而不是子树里。子树可以被
//	                     销毁重建 (折叠态切换、换屏), 页面在函数体里读回来即可。
//
// 第一档适合"经常来回切"的页 (列表/详情); 第二档适合"随姿态重建"的页
// (折叠双栏里被挤走的那一栏)。两者可叠加, 语义互补。

// ===== 页面 =====

// routerPage 是一页的宿主 (holder) 与构建状态。
//
// "一个 holder + 只构建一次"就是 keepAlive 的实现: 保活的页面在不被激活时
// 只是被摘出 Children (Parent 断链), 布局/绘制/命中都看不见它, 而它自己的
// 响应式接线仍在 —— 后台页的信号更新依然让它保持最新, 回到前台瞬间就是对的。
type routerPage struct {
	key   string
	rec   *routeRecord
	entry *routeEntry

	holder *GuiNode
	built  bool

	// loading / errBranch 是懒加载期间与失败时的占位分支 (viewBranch 是
	// 懒构建 + 保活的, 所以它们本身很便宜)。
	loading   *viewBranch
	errBranch *viewBranch
	// waitBranch 是**当前挂着的**那个占位分支 (用它判断"要不要换分支")。
	waitBranch *viewBranch

	loadErr object.Value
	pending bool
}

func newRouterPage(key string, rec *routeRecord, entry *routeEntry, loading, errTpl object.Value) *routerPage {
	return &routerPage{
		key:       key,
		rec:       rec,
		entry:     entry,
		holder:    viewNewSlot(),
		loading:   viewBranchNew(viewBranchKids(loading)),
		errBranch: viewBranchNew(viewBranchKids(errTpl)),
	}
}

// buildSync 在 wiring scope 内调用页面组件并把结果挂上; 返回非 nil 表示
// "它不是普通组件, 而是一个懒加载器" (此时本次调用就是 import, 没有浪费)。
//
// 这个"调用一次, 两种结果都用"的设计是刻意的: 组件究竟是普通函数还是
// `() => import(...)`, 唯一可靠的判定方式就是调用它 —— 预先试调一次会让
// 组件体跑两遍 (createSignal 造两份、onMount 登记两次)。所以第一次构建
// 直接把它当成页面来构建: 返回元素就挂上 (判定与构建合并), 返回 Promise
// 就转成等待态 (那次调用正好完成了 import)。
func (p *routerPage) buildSync(comp, propsArg object.Value) *object.Promise {
	sc := object.PushWiringScope()
	res := untrackCall(comp, propsArg)
	object.PopWiringScope()
	// 页面体抛错时**必须出声**: 静默的后果是"点进去一片空白, 控制台什么也没有"。
	if err := takeCallbackErr(); err != nil {
		recordWarn("gx/router 页面组件抛错: %v", err)
	}
	if pw, ok := res.(*object.Promise); ok {
		return pw
	}
	if err, ok := res.(*object.Error); ok {
		recordWarn("gx/router 页面组件返回错误: %s", object.ToString(err))
	}
	if el, ok := res.(*GuiNode); ok {
		p.holder.wireChild(el)
	}
	p.holder.cleanups = append(p.holder.cleanups, sc.Cleanups...)
	for _, fn := range sc.Mounts {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/router onMount error: %v", err)
		}
	}
	p.built = true
	return nil
}

// dispose 销毁这一页 (走内核标准的 disposeNode: effect / 动画 / 滚动状态一并收尾)。
func (p *routerPage) dispose() {
	if p == nil {
		return
	}
	disposeNode(p.holder)
	p.built = false
	p.waitBranch = nil
	p.holder.Children = nil
}

// untrackCall 在"不收集依赖"的前提下调用一个脚本函数 (参数照原样传进去)。
//
// **参数必须靠闭包捕获**: gx/solid 的 untrack 回调用零个参数调用它拿到的函数
// (`untrack(fn)` 语义就是 `fn()`), 所以包装层不能指望从自己的形参里拿到调用
// 参数 —— 早期版本在这里写成 `fn(args...)` 转发 wrapper 的形参, 结果是
// "页面组件收到 0 个参数", 页面体里 `props.param.id` 当场抛错、页面静默空白。
func untrackCall(fn object.Value, args ...object.Value) object.Value {
	if fn == nil || !object.IsCallable(fn) {
		return nil
	}
	if exports, ok := object.LookupBuiltinModule("gx/solid"); ok {
		if untrack, ok := exports["untrack"]; ok && object.IsCallable(untrack) {
			inner := object.NewBuiltin("page-build", func(_ ...object.Value) object.Value {
				return object.CallFunction(fn, nil, args...)
			})
			return object.CallFunction(untrack, nil, inner)
		}
	}
	return object.CallFunction(fn, nil, args...)
}

// ===== 懒加载 =====

// lazy 是显式懒加载标记: lazy(() => import("./pages/detail.js"))。
//
// **为什么除了"自动识别"还要一个显式标记**: 导航的守卫链要在**提交之前**就
// 拿到组件级守卫 (beforeRouteEnter 等), 而它只能从"已加载模块的命名导出"里
// 读。自动识别要等视图第一次调用组件才知道"这是个返回 Promise 的加载器",
// 那时守卫链已经跑完 —— 于是懒加载页的**首次**进入会漏掉 beforeRouteEnter。
// 用 lazy() 包一层后编译期就知道它是懒的, 导航会先等模块加载完再收守卫,
// 顺序完全正确。不写 lazy() 也能用, 只是首次进入时组件级守卫不生效 (会记
// 一条警告提示改用 lazy())。
type routerLazyValue struct {
	loader object.Value
}

func (l *routerLazyValue) Type() object.ObjectType { return object.ObjectType("ROUTER_LAZY") }
func (l *routerLazyValue) Inspect() string         { return "<router lazy loader>" }
func (l *routerLazyValue) IsTruthy() bool          { return true }
func (l *routerLazyValue) GetProperty(string) (object.Value, bool) {
	return object.UndefinedSingleton, false
}
func (l *routerLazyValue) SetProperty(string, object.Value) {}

func jsRouterLazy(args ...object.Value) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("lazy: 需要 () => import(...) 形式的加载函数")
	}
	return &routerLazyValue{loader: args[0]}
}

// applyLazyMarker 在编译期把 lazy(...) 的产物拆成"加载器 + 已知是懒的"。
func applyLazyMarker(rec *routeRecord) {
	if lz, ok := rec.component.(*routerLazyValue); ok {
		rec.component = lz.loader
		rec.compKind = routeCompLazy
	}
}

// beginLoad 启动 (或复用在飞的) 一次懒加载, 返回挂在飞的 Promise (没得等 → nil)。
//
// **只对"已知是懒加载"的记录调用** (compKind == routeCompLazy): 未知形态的
// 记录由视图层第一次构建时识别 (那次调用同时就是 import)。在这里贸然调用
// 一个普通组件会白构建一遍。
//
// 复用"在飞的"是必要的: 两个窗口同时导航到同一个懒加载页时, 第二次调用不该
// 再 import 一次 (模块系统虽有缓存, 但会多一层无谓的 microtask 往返), 更要紧
// 的是**结果只应用一次**, 否则 hooks/resolved 会被反复覆盖。
func beginLoad(rec *routeRecord) *object.Promise {
	if rec == nil || rec.resolved != nil || rec.loadErr != nil || rec.component == nil {
		return nil
	}
	if rec.loadPromise != nil {
		return rec.loadPromise
	}
	res := object.CallFunction(rec.component, nil)
	if err := takeCallbackErr(); err != nil {
		rec.loadErr = object.NewErrorWithName("Error", "组件加载失败: "+err.Error())
		return nil
	}
	p, ok := res.(*object.Promise)
	if !ok {
		// 同步返回: 是"伪模块"对象或直接是组件, 当场应用
		rec.compKind = routeCompSync
		applyLoadedValue(rec, res)
		return nil
	}
	adoptLoadPromise(rec, p)
	return p
}

// adoptLoadPromise 把"已经在手上的加载 Promise"登记到记录上。
//
// 内部回调只负责**把结果落到记录上** (与谁在等无关); 外部等待者 (导航的
// awaitChain / 视图的占位分支) 各自挂自己的回调去拿"结算了"这一个信号。
func adoptLoadPromise(rec *routeRecord, p *object.Promise) {
	rec.compKind = routeCompLazy
	rec.loadPromise = p
	p.OnFulfilled(object.NewBuiltin("router-load", func(args ...object.Value) object.Value {
		rec.loadPromise = nil
		applyLoadedValue(rec, argOrNil(args))
		return object.UndefinedSingleton
	}))
	p.OnRejected(object.NewBuiltin("router-load-err", func(args ...object.Value) object.Value {
		rec.loadPromise = nil
		rec.loadErr = object.NewErrorWithName("Error", "组件加载失败: "+object.ToString(argOrNil(args)))
		return object.UndefinedSingleton
	}))
}

// applyLoadedValue 把加载结果落到记录上。
//
// 三种形态都认, 因为脚本在别的框架里都可能这么写:
//
//	import(...)                  → 模块命名空间 (取 .default)
//	() => ({ default: Comp })    → 同步返回的"伪模块"
//	Promise.resolve(Comp)        → 直接是组件
//
// 同时从命名空间上取组件级守卫 (beforeRouteEnter / Update / Leave) —— 这就是
// vue-router 里 `<script>` 导出这些名字的等价物。
func applyLoadedValue(rec *routeRecord, v object.Value) {
	if v == nil {
		rec.loadErr = object.NewErrorWithName("Error", "组件加载结果为空")
		return
	}
	if o, ok := v.(*object.Object); ok {
		for _, name := range routerComponentHooks {
			if fn, ok := o.GetProperty(name); ok && object.IsCallable(fn) {
				rec.hooks[name] = fn
			}
		}
		if def, ok := o.GetProperty("default"); ok && def != nil {
			rec.resolved = def
			return
		}
		rec.loadErr = object.NewErrorWithName("Error", "懒加载模块没有 default 导出")
		return
	}
	if object.IsCallable(v) || isElement(v) {
		rec.resolved = v
		return
	}
	rec.loadErr = object.NewErrorWithName("Error", "懒加载结果既不是组件也不是模块对象")
}

var routerComponentHooks = []string{
	"beforeRouteEnter", "beforeRouteUpdate", "beforeRouteLeave",
}

// whenLoaded 确保某条记录已加载完毕。
//
// 返回 true = "现在就有结论 (成功或失败), 调用方继续"; false = "已挂起, cont
// 会在结算时被调用一次 (**也可能在本次调用内就同步调过了**) "。
//
// 那个"也可能同步"的边界是本文件最容易写错的地方: Promise 已结算时
// OnFulfilled 会立刻回调, 于是 cont 在 whenLoaded 内部就跑完了。调用方的
// 统一写法 `if !whenLoaded(…) { return }` 对两种情况都正确 —— 因为同步跑完时
// "继续"这件事已经发生过了。
func whenLoaded(rec *routeRecord, cont func(err object.Value)) bool {
	if rec == nil || rec.resolved != nil || rec.loadErr != nil {
		return true
	}
	p := beginLoad(rec)
	if p == nil {
		return true
	}
	fired := false
	settle := func() {
		if fired {
			return
		}
		fired = true
		cont(rec.loadErr)
	}
	p.OnFulfilled(object.NewBuiltin("when-loaded", func(args ...object.Value) object.Value {
		settle()
		return object.UndefinedSingleton
	}))
	p.OnRejected(object.NewBuiltin("when-loaded-err", func(args ...object.Value) object.Value {
		settle()
		return object.UndefinedSingleton
	}))
	return false
}

// awaitChain 等待目标路由链上的全部懒加载 (与 whenLoaded 同一套语义)。
func awaitChain(chain []*routeRecord, cont func()) bool {
	var waits []*routeRecord
	for _, rec := range chain {
		if rec.resolved != nil || rec.loadErr != nil {
			continue
		}
		if rec.compKind != routeCompLazy {
			// 未知形态: 不调用它 (调用就是构建, 不能试) —— 交给视图层第一次
			// 激活时识别。首进入因此会漏掉组件级守卫 (见 lazy 的说明)。
			continue
		}
		waits = append(waits, rec)
	}
	if len(waits) == 0 {
		return true
	}
	remaining := len(waits)
	for _, rec := range waits {
		if whenLoaded(rec, func(err object.Value) {
			remaining--
			if remaining == 0 {
				cont()
			}
		}) {
			remaining--
		}
	}
	// 无论"全同步结算"还是"有异步", 都返回 false: 前者 cont 已经跑过了,
	// 后者 cont 会在结算时跑 —— 调用方都不该再续一次。
	return false
}

// ===== 视图宿主 =====

// routerViewHost 是一个 <RouterView> 实例。
type routerViewHost struct {
	r    *router
	node *GuiNode

	explicitScope string // scope= prop (空 = 按窗口自动)
	loading       object.Value
	errTpl        object.Value
	depth         int

	sess *routerSession

	pages  map[string]*routerPage
	active []*routerPage

	// 折叠双栏用的容器 (单栏时保持 nil)
	dualRow  *GuiNode
	dualTag  string
	paneWrap [2]*GuiNode
}

// strictAppOfNode 与 appOfNode 的唯一差别: **不做 activeApp 回退**。
//
// 路由的会话绑定必须严格 —— 回退值会让"第二个窗口的 RouterView 在还没挂载时
// 认领到第一个窗口的导航栈" (h() 阶段所有窗口都还没注册, 回退值恰好是前一个
// 窗口)。这里要的是"确实挂上了某个窗口"这个事实, 拿不到就等下一次
// (见 flushRouterViews)。
func strictAppOfNode(n *GuiNode) *app {
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
	return nil
}

// sync 是视图的重算入口 (由 runEffect 驱动, 见 jsRouterView)。
//
// 依赖有四个: 会话的当前路由 signal、gx/screen 的环境版本、router 自己的重算
// 版本, 以及 gx/solid 的 untrack 之外什么都**不**订阅 (页面体自己读的 signal
// 不算依赖 —— 那是 untrack 的职责, 于是"页面内状态变化"永远不会导致整页重建)。
//
// **前两个订阅必须排在任何 early return 之前**: 视图第一次求值发生在 h() 阶段,
// 那时窗口还没注册、会话还没绑上。若把订阅写在"绑上会话之后", 这一次求值就
// 什么依赖都没建立 —— 效果是**视图此后永远收不到"折叠姿态变化 / 换屏"的通知**
// (双栏静默不生效)。这类 bug 只在"姿态真的变了"时才现形, 所以特意写在最前面。
func (h *routerViewHost) sync() {
	readEnvSignal()
	readRouterRev(h.r)
	h.bindSession()
	if h.sess == nil {
		return
	}
	if g := h.sess.navGet; g != nil {
		object.CallFunction(g, nil)
	}
	h.render()
}

// bindSession 解析本视图所属的会话 (幂等)。
func (h *routerViewHost) bindSession() {
	if h.sess != nil {
		return
	}
	if h.explicitScope != "" {
		h.sess = h.r.sessionFor(h.explicitScope)
		h.hookSession()
		return
	}
	a := strictAppOfNode(h.node)
	if a == nil {
		return // 还没挂上: 等 flushRouterViews
	}
	h.sess = h.r.sessionFor(appScope(a))
	h.hookSession()
}

func (h *routerViewHost) hookSession() {
	if h.sess == nil {
		return
	}
	for _, v := range h.sess.views {
		if v == h {
			return
		}
	}
	h.sess.views = append(h.sess.views, h)
}

// invalidate 由会话在提交导航后调用 (覆盖"视图没轮到读 signal"的路径, 例如
// 镜像同步), 保证界面一定跟着走。
func (h *routerViewHost) invalidate() {
	if h.sess == nil {
		return
	}
	h.render()
}

// recordAtDepth 取一条路由在**本视图这一层**的记录。
//
// 嵌套路由 (父布局 + 子页面) 里, 每一层 RouterView 显示 matched 链上对应的
// 那一项: 顶层视图看 matched[0], 它里面再嵌的视图看 matched[1]。
func (h *routerViewHost) recordAtDepth(e *routeEntry) *routeRecord {
	if e == nil {
		return nil
	}
	if h.depth < len(e.matched) {
		return e.matched[h.depth]
	}
	return nil
}

// render 根据当前路由重排本视图的子节点。
func (h *routerViewHost) render() {
	sess := h.sess
	if sess == nil {
		return
	}
	var pages []*routerPage
	if cur := sess.current(); cur != nil {
		for _, e := range h.panes(sess) {
			if rec := h.recordAtDepth(e); rec != nil {
				pages = append(pages, h.pageFor(e, rec))
			}
		}
	}

	// 非激活页: 保活的摘出去 (Parent 断链但活着), 不保活的销毁。
	activeKeys := map[string]bool{}
	for _, p := range pages {
		activeKeys[p.key] = true
	}
	for k, p := range h.pages {
		if activeKeys[k] {
			continue
		}
		if h.keepAliveOf(p) {
			p.holder.Parent = nil
			continue
		}
		p.dispose()
		delete(h.pages, k)
	}
	h.active = pages

	switch {
	case len(pages) == 0:
		h.detachDual()
		h.node.Children = nil
	case len(pages) == 1:
		// 单栏: 直挂 (不套多余的盒子 —— 与 <For> 的宿主 slot 同一口径)
		h.detachDual()
		pages[0].holder.Parent = h.node
		h.node.Children = []*GuiNode{pages[0].holder}
	default:
		h.renderDual(pages)
	}
	// 页面子树出现/消失必须**整帧失效**: diffRects 看不到"刚被摘掉的子树",
	// 局部重绘会留下上一页的残影 (与弹层展开/收起同一理由, 见 markFullDirty)。
	markFullDirtyFor(h.node)
}

// keepAliveOf 报告一页是否要保活。
func (h *routerViewHost) keepAliveOf(p *routerPage) bool {
	if p == nil || p.rec == nil {
		return false
	}
	// 加载中/加载失败/还没构建的页不保活: 它们的子树只是占位分支, 重建很便宜,
	// 而且保活会让"重新加载"没法做 (下次进来还是那个错误分支)。
	if p.loadErr != nil || p.pending || !p.built {
		return false
	}
	return p.rec.keepAlive
}

// panes 决定这次要显示哪几页 (单栏 1 页 / 折叠双栏 2 页)。
func (h *routerViewHost) panes(sess *routerSession) []*routeEntry {
	cur := sess.current()
	if cur == nil {
		return nil
	}
	out := []*routeEntry{cur}
	if !h.r.foldDual || !h.dualActive() {
		return out
	}
	rec := h.recordAtDepth(cur)
	if rec == nil || !rec.dualPane {
		return out
	}
	prev := sess.previous()
	if prev == nil || prev == cur {
		return out
	}
	prevRec := h.recordAtDepth(prev)
	if prevRec == nil || !prevRec.dualPane {
		return out
	}
	// 左栏放"上一条" (列表), 右栏放"当前" (详情) —— 折叠屏最常见的用法
	return []*routeEntry{prev, cur}
}

// dualActive 报告"本视图所在窗口此刻是否处于折叠半开姿态"。
func (h *routerViewHost) dualActive() bool {
	a := strictAppOfNode(h.node)
	if a == nil {
		return false
	}
	return h.displayIsHalfOpen(a)
}

func (h *routerViewHost) displayIsHalfOpen(a *app) bool {
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	d, ok := findDisplay(displayOfSurface(s))
	if !ok || !d.Foldable {
		return false
	}
	return d.Posture == postureHalfOpen
}

// renderDual 组织双栏布局。
//
// 分割比例用 **flexGrow 的比值**表达 (左 4 : 右 6), 不是像素: 布局宽度的真值
// 要到布局那一刻才知道 (窗口未必占满整块屏), 而 flexGrow 天然按"剩余空间的
// 比例"分配 —— 不需要知道任何具体像素就能得到 40/60 的效果。
func (h *routerViewHost) renderDual(pages []*routerPage) {
	orient, ratio := h.dualSplit()
	tag := "row"
	if orient == "horizontal" {
		tag = "column"
	}
	if h.dualRow == nil || h.dualTag != tag {
		if h.dualRow != nil {
			// 方向变了: 旧容器连同它挂着的页面一起摘掉 (页面由 render 统一
			// 重新挂到新容器上, 这里只断链不销毁 —— 保活页不能销毁)。
			h.dualRow.Children = nil
			h.dualRow.Parent = nil
			h.dualRow = nil
		}
		h.dualRow = &GuiNode{Tag: tag, Props: map[string]object.Value{}}
		h.dualTag = tag
		for i := 0; i < 2; i++ {
			h.paneWrap[i] = &GuiNode{Tag: "view", Props: map[string]object.Value{}}
			h.paneWrap[i].Parent = h.dualRow
		}
		h.dualRow.Children = []*GuiNode{h.paneWrap[0], h.paneWrap[1]}
	}
	left := int(ratio*100 + 0.5)
	if left < 1 {
		left = 1
	}
	if left > 99 {
		left = 99
	}
	h.paneWrap[0].Props["flexGrow"] = object.NewNumber(float64(left))
	h.paneWrap[1].Props["flexGrow"] = object.NewNumber(float64(100 - left))
	for i := 0; i < 2; i++ {
		p := pages[i]
		p.holder.Parent = h.paneWrap[i]
		h.paneWrap[i].Children = []*GuiNode{p.holder}
	}
	h.dualRow.Parent = h.node
	h.node.Children = []*GuiNode{h.dualRow}
}

// dualSplit 报告折痕方向与分割比例。
func (h *routerViewHost) dualSplit() (string, float64) {
	a := strictAppOfNode(h.node)
	if a == nil {
		return "vertical", 0.5
	}
	a.mu.Lock()
	s := a.surface
	a.mu.Unlock()
	d, ok := findDisplay(displayOfSurface(s))
	if !ok {
		return "vertical", 0.5
	}
	return displayHingeOrientation(d), displaySplitRatio(d)
}

// detachDual 摘掉双栏容器 (回到单栏时用)。
func (h *routerViewHost) detachDual() {
	if h.dualRow != nil {
		h.dualRow.Children = nil
		h.dualRow.Parent = nil
		h.dualRow = nil
		h.dualTag = ""
	}
	for i := range h.paneWrap {
		h.paneWrap[i] = nil
	}
}

// pageFor 取/建一页的缓存条目, 并保证它"该构建时已经构建好"。
func (h *routerViewHost) pageFor(e *routeEntry, rec *routeRecord) *routerPage {
	key := h.depthKey(e)
	p, ok := h.pages[key]
	if !ok {
		p = newRouterPage(key, rec, e, h.loading, h.errTpl)
		h.pages[key] = p
	}
	if p.rec != rec || p.entry != e {
		// 同 key 不同记录/条目 (理论上只在参数键碰撞时出现): 内容必须重建
		p.dispose()
		p.rec, p.entry, p.loadErr, p.pending = rec, e, nil, false
	}
	h.buildOrWait(p, rec)
	return p
}

// depthKey 把嵌套层级编进页面键: 同一实例在深度变化后不会复用错页面。
func (h *routerViewHost) depthKey(e *routeEntry) string {
	return strconv.Itoa(h.depth) + "|" + e.key()
}

// buildOrWait 构建页面, 或在等加载 / 加载失败时挂对应分支。
func (h *routerViewHost) buildOrWait(p *routerPage, rec *routeRecord) {
	if p.built && p.loadErr == nil {
		return // keepAlive 的正常路径: 已经构建好, 直接用
	}
	if rec.component == nil {
		// 纯分组/纯重定向记录: 这一层没有内容可渲染
		p.holder.Children = nil
		p.built = true
		return
	}
	if rec.loadErr != nil {
		p.loadErr = rec.loadErr
		p.pending = false
		p.built = true
		h.showWaitBranch(p, p.errBranch)
		return
	}
	comp := rec.resolved
	if comp == nil {
		if rec.compKind == routeCompLazy {
			// 已知是懒加载但还没解析 (例如自动识别的首进入: 导航没等它)
			h.waitForLoad(p, rec, beginLoad(rec))
			return
		}
		// 同步组件, 或"形态未知"——未知的直接当页面构建 (见 buildSync 的说明)
		comp = rec.component
	}
	h.clearWaitBranch(p)
	props := h.pageProps(p.entry, rec)
	// 页面体执行期间压入渲染上下文: 嵌套 RouterView 靠它拿到层级,
	// useRoute/useRouteState/RouterLink 靠它找到会话。用完即出栈。
	routerRenderStack = append(routerRenderStack, &routerRenderCtx{
		r: h.r, sess: h.sess, depth: h.depth + 1,
	})
	pw := p.buildSync(comp, props)
	routerRenderStack = routerRenderStack[:len(routerRenderStack)-1]
	if pw != nil {
		// 原来是个懒加载器: 这次调用已经完成 import, 转成等待态
		adoptLoadPromise(rec, pw)
		h.waitForLoad(p, rec, pw)
		return
	}
	// 只有"原本形态未知"才记成同步 —— 懒加载记录已经由 applyLoadedValue
	// 定成 routeCompLazy, 覆盖它会让下一次导航不再等它 (也丢掉 reload 语义)。
	if rec.compKind == routeCompUnknown {
		rec.compKind = routeCompSync
	}
	// resolved 指向组件函数本身: 于是 componentHook 能从它身上读
	// beforeRouteEnter 这类"挂在函数属性上"的守卫 (非懒加载的写法)。
	if rec.resolved == nil {
		rec.resolved = rec.component
	}
	p.pending = false
	p.loadErr = nil
}

// waitForLoad 挂 loading 分支并等加载结算 (结算后 invalidate → 重算这一页)。
func (h *routerViewHost) waitForLoad(p *routerPage, rec *routeRecord, pending *object.Promise) {
	h.showWaitBranch(p, p.loading)
	p.pending = true
	p.built = false
	if pending == nil {
		// 没有可等的 Promise: 说明加载已经当场完成或当场失败。
		p.pending = false
		h.buildOrWait(p, rec)
		return
	}
	settle := object.NewBuiltin("router-view-load", func(args ...object.Value) object.Value {
		p.pending = false
		p.built = false
		h.invalidate()
		return object.UndefinedSingleton
	})
	pending.OnFulfilled(settle)
	pending.OnRejected(settle)
}

// showWaitBranch 把 loading / error 分支挂到页宿主上 (同一分支对象复用:
// viewBranch 是懒构建 + 保活的)。
func (h *routerViewHost) showWaitBranch(p *routerPage, b *viewBranch) {
	if p.waitBranch == b && len(p.holder.Children) == 1 && p.holder.Children[0] == b.holder {
		return
	}
	if p.waitBranch != nil && p.waitBranch != b {
		// 换分支 (loading → error): 旧的必须**销毁**而不是保活 —— 保留它等于
		// 留下一个看不见但仍在跑的子树, 且下次进加载态会看到上一次的陈旧内容。
		disposeNode(p.waitBranch.holder)
		p.holder.Children = nil
	}
	b.attach(p.holder, nil)
	b.build()
	p.waitBranch = b
}

// clearWaitBranch 清掉占位分支 (真正的内容要挂上来了)。
func (h *routerViewHost) clearWaitBranch(p *routerPage) {
	if p.waitBranch == nil {
		return
	}
	disposeNode(p.waitBranch.holder)
	p.waitBranch = nil
	p.holder.Children = nil
}

// pageProps 组装传给页面组件的 props。
//
// 传的是**平铺的一包**: `{route, params, query, param, path, name, meta, …}`。
// 与 vue-router 的差异: 那边默认只给 route 一个对象, 参数要 `route.params.id`;
// 这里额外把每个参数平铺一份 (`param.id` 与 `route.params.id` 等价) ——
// 桌面应用一个页面通常只有一两个参数, 平铺写法更短。
func (h *routerViewHost) pageProps(e *routeEntry, rec *routeRecord) object.Value {
	o := object.NewObject()
	o.SetProperty("route", routeEntryToJS(e))
	o.SetProperty("path", object.NewString(e.path))
	o.SetProperty("fullPath", object.NewString(e.fullPath))
	o.SetProperty("name", object.NewString(rec.name))
	o.SetProperty("params", stringMapToJS(e.params))
	o.SetProperty("query", stringMapToJS(e.query))
	pm := object.NewObject()
	for k, v := range e.params {
		pm.SetProperty(k, object.NewString(v))
	}
	o.SetProperty("param", pm)
	if rec.meta != nil {
		o.SetProperty("meta", rec.meta)
	} else {
		o.SetProperty("meta", object.NewObject())
	}
	o.SetProperty("depth", object.NewNumber(float64(h.depth)))
	// 路由记录上的静态 props 合并进来 ({props:{mode:"edit"}});
	// props: true 时把参数平铺成 props (vue-router 的同名行为)。
	if ro, ok := rec.propsVal.(*object.Object); ok {
		for k, desc := range ro.Properties {
			o.SetProperty(k, desc.Value)
		}
	} else if b, ok := rec.propsVal.(*object.Boolean); ok && b.Value {
		for k, v := range e.params {
			o.SetProperty(k, object.NewString(v))
		}
	}
	return o
}

// ===== 待绑定队列 (挂载即认领窗口) =====

// flushRouterViews 把"已挂上树但还没认领窗口"的 RouterView 收尾。
//
// **由 app.redraw 在布局之前调用** (见 render.go): 这是唯一"树已经接好、却又
// 还没上屏"的时刻。放这里的三个理由:
//
//  1. h() 阶段 (构建期) 所有窗口都还没注册, 那时按窗口解析会话一定会认错;
//  2. 挂载钩子只能覆盖"首次挂载", 覆盖不了后来才出现的 RouterView
//     (条件分支里现挂一个、懒加载页里嵌一个、列表行里放一个);
//  3. 放在 redraw 最前面, 本帧就画出正确内容 —— 放后面会先闪一帧空白。
func flushRouterViews(a *app) {
	if a == nil {
		return
	}
	bindRouterKeys(a)
	if len(pendingRouterViews) == 0 {
		return
	}
	list := pendingRouterViews
	pendingRouterViews = nil
	for _, h := range list {
		owner := strictAppOfNode(h.node)
		if owner == nil {
			// 还没挂上 → 留着; 从未被挂上 (被路由变化丢弃的孤儿) → 丢掉,
			// 否则它会每帧重试一次 (白费功夫, 还掩盖"树没接上"的问题)。
			if h.node != nil && h.node.Parent == nil && h.node != a.root {
				continue
			}
			pendingRouterViews = append(pendingRouterViews, h)
			continue
		}
		if owner != a {
			// 属于别的窗口: 等那个窗口重绘时处理 (每帧只处理本窗口的)
			pendingRouterViews = append(pendingRouterViews, h)
			continue
		}
		h.bindSession()
		h.sync()
	}
}

// ===== 组件实现 =====

// jsRouterView 是 <RouterView router={r} scope="popup" loading={…} error={…} />。
func jsRouterView(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	r := routerFromProps(props)
	if r == nil {
		recordWarn("gx/router RouterView: 没有可用的 router (既没给 router= 也没创建过)")
		return viewNewSlot()
	}
	h := &routerViewHost{
		r:             r,
		node:          viewNewSlot(),
		explicitScope: objPropStr(props, "scope"),
		loading:       viewProp(props, "loading"),
		errTpl:        viewProp(props, "error"),
		pages:         map[string]*routerPage{},
	}
	if ctx := currentRenderCtx(); ctx != nil {
		h.depth = ctx.depth
	}
	h.bindSession()
	if h.sess == nil {
		pendingRouterViews = append(pendingRouterViews, h)
	}

	// 清理链: 本视图被销毁时把所有缓存的页一并销毁。保活页不在 Children 里,
	// disposeNode 走不到它们 —— 与 viewDetachCleanup 是同一个问题。
	host := h.node
	host.cleanups = append(host.cleanups, object.NewBuiltin("router-view-cleanup",
		func(args ...object.Value) object.Value {
			for _, p := range h.pages {
				p.dispose()
			}
			h.pages = map[string]*routerPage{}
			if h.sess != nil {
				for i, v := range h.sess.views {
					if v == h {
						h.sess.views = append(h.sess.views[:i], h.sess.views[i+1:]...)
						break
					}
				}
			}
			return object.UndefinedSingleton
		}))

	dispose := runEffect(func() object.Value {
		h.sync()
		return object.UndefinedSingleton
	})
	if dispose != nil {
		host.effects = append(host.effects, dispose)
	}
	h.render()
	return host
}

// jsRouterLink 是 <RouterLink to="/items" replace activeBackground="#dde">文字</RouterLink>。
//
// 实现是一个 `view` 容器: 布局透明 (不额外占盒子), 但可点击、可装饰。之所以
// 不复用 button: 链接在界面里通常是一行小字, 而 button 有自己的缺省高度/边框/
// 按压反馈 —— 让脚本决定外观, 路由器只提供导航语义。
func jsRouterLink(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	r := routerFromProps(props)
	node := &GuiNode{Tag: "view", Props: map[string]object.Value{}}
	if r == nil {
		recordWarn("gx/router RouterLink: 没有可用的 router")
		return node
	}
	to := viewProp(props, "to")
	if to == nil {
		recordWarn("gx/router RouterLink: 缺少 to")
	}
	replace := false
	if b, ok := viewProp(props, "replace").(*object.Boolean); ok {
		replace = b.Value
	}
	disabled := false
	if b, ok := viewProp(props, "disabled").(*object.Boolean); ok {
		disabled = b.Value
	}
	scope := objPropStr(props, "scope")
	// __routeTo 是解析出来的目标路径 (内省用: 测试按它定位链接, gx/dev 面板
	// 也能显示"这个链接指向哪")。它不参与布局, 纯属可读性。
	if target, err := r.resolveTo(to, nil); err == nil {
		node.Props["__routeTo"] = object.NewString(target.path)
	}
	// 脚本给的其它属性原样接到 view 上 (函数值仍然是响应式属性)。
	// 排除路由器自己的语义键, 免得它们被当成布局属性。
	for name, desc := range props.Properties {
		switch name {
		case "to", "replace", "disabled", "scope", "router", "activeBackground":
			continue
		}
		node.wireProp(name, desc.Value)
	}
	// 便捷的活动态样式: activeBackground 是"命中当前路由时的背景"。
	// 做成**响应式 prop** (包一层取值函数) 而不是构建时快照 —— 否则路由一变
	// 高亮就冻住了。这正是"函数 prop 即响应式"这条既有纪律的直接应用。
	if ab := viewProp(props, "activeBackground"); ab != nil {
		node.wireProp("background", object.NewBuiltin("active-bg", func(args ...object.Value) object.Value {
			// create=false: 这个取值函数会在 h() 阶段就被 effect 立刻求值一次,
			// 若它顺手建会话, 每个应用都会凭空多出一个没人用的 "default" 作用域。
			//
			// 必须先订阅 router 版本号 (与 jsCurrentRoute 记的是同一笔账):
			// isActive 走 current() 的纯 Go 读栈, 不碰任何 signal —— 不订阅的话
			// 本 effect 没有依赖, 只在接线时跑一次, 高亮从此冻在第一帧
			// (2026-09-22 脚手架冒烟实测: 活动页签永远不亮, 三个页签全是底色)。
			if r.revGet != nil {
				object.CallFunction(r.revGet, nil)
			}
			s := linkSessionForNode(r, node, scope, false)
			if s != nil && s.isActive(to) {
				return ab
			}
			if desc, ok := props.GetProperty("background"); ok {
				return desc
			}
			return object.NullSingleton
		}))
	}
	// 单击导航 (保留脚本自己的 onClick, 链式调用)
	prevClick := viewProp(props, "onClick")
	node.Props["onClick"] = object.NewBuiltin("router-link", func(args ...object.Value) object.Value {
		if !disabled {
			if s := linkSessionForNode(r, node, scope, true); s != nil {
				s.startNav(&navTask{to: to, replace: replace})
			}
		}
		if object.IsCallable(prevClick) {
			return object.CallFunction(prevClick, nil, args...)
		}
		return object.UndefinedSingleton
	})
	// 回车/空格也能激活 (键盘可达性): 事件沿焦点链回溯到本节点
	node.Props["onKeyDown"] = object.NewBuiltin("router-link-key", func(args ...object.Value) object.Value {
		eo, _ := argOrNil(args).(*object.Object)
		if eo == nil || disabled {
			return object.UndefinedSingleton
		}
		if k := objPropStr(eo, "key"); k == "Enter" || k == " " {
			if s := linkSessionForNode(r, node, scope, true); s != nil {
				s.startNav(&navTask{to: to, replace: replace})
			}
		}
		return object.UndefinedSingleton
	})
	for _, c := range args[1:] {
		node.wireChild(c)
	}
	return node
}

// linkSessionForNode 解析一个链接作用于哪个会话。优先级:
//
//  1. 显式 scope= 属性
//  2. 渲染上下文 (链接写在页面体里 → 那个页面所属的会话)
//  3. **节点归属的窗口** —— 根级链接靠这条落到"自己在的那个窗口",
//     而不是"最近挂载的窗口" (多窗口下两者常常不是同一个)
//  4. 最近活跃的作用域 (兜底)
//
// create=false 时第 3/4 步不建会话 (只查已有): 供 isActive 这类"只读"用途,
// 否则一个纯粹的样式判断会凭空造出一个空会话。
func linkSessionForNode(r *router, node *GuiNode, scope string, create bool) *routerSession {
	if scope != "" {
		if create {
			return r.sessionFor(scope)
		}
		return r.sessionForOptional(scope)
	}
	if ctx := currentRenderCtx(); ctx != nil && ctx.r == r {
		return ctx.sess
	}
	if a := strictAppOfNode(node); a != nil {
		return r.sessionFor(appScope(a))
	}
	if create {
		return r.sessionFor(r.activeScope())
	}
	return r.sessionForOptional(r.activeScope())
}

// routerFromProps 从 props 里取 router (显式 router= 优先, 否则默认 router)。
func routerFromProps(props *object.Object) *router {
	if v := viewProp(props, "router"); v != nil {
		if r, ok := v.(*routerRefValue); ok {
			return r.r
		}
	}
	return defaultRouter
}

// ===== use* 系列 =====

func jsUseRoute(args ...object.Value) object.Value {
	s := pickSession()
	if s == nil {
		recordWarn("gx/router useRoute: 没有可用会话 (不在 RouterView 内且没创建过 router)")
		return object.NewBuiltin("route", func(args ...object.Value) object.Value {
			return object.NullSingleton
		})
	}
	return s.routeGetter()
}

func jsUseRouter(args ...object.Value) object.Value {
	if ctx := currentRenderCtx(); ctx != nil {
		return ctx.r.jsObject()
	}
	if defaultRouter != nil {
		return defaultRouter.jsObject()
	}
	recordWarn("gx/router useRouter: 还没有创建过 router")
	return object.NullSingleton
}

// jsUseRouteState 返回当前路由的**状态袋**读写器。
//
// 状态袋挂在**栈项**上, 因此天然跨"页面子树重建"(折叠态切换、换屏、甚至
// keepAlive=false 的页面)存活 —— 这是"状态外提"最省事的落点:
//
//	const st = useRouteState();
//	st.set("offset", 120);          // 页面被销毁重建之后:
//	const off = st.get("offset", 0);
func jsUseRouteState(args ...object.Value) object.Value {
	s := pickSession()
	if s == nil {
		recordWarn("gx/router useRouteState: 没有可用会话")
		return object.NullSingleton
	}
	bag := func() *object.Object {
		cur := s.current()
		if cur == nil {
			return object.NewObject()
		}
		bo, _ := cur.stateObject().(*object.Object)
		if bo == nil {
			return object.NewObject()
		}
		return bo
	}
	o := object.NewObject()
	o.SetProperty("get", object.NewBuiltin("get", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		if v, ok := bag().GetProperty(object.ToString(args[0])); ok {
			return v
		}
		if len(args) > 1 {
			return args[1]
		}
		return object.UndefinedSingleton
	}))
	o.SetProperty("set", object.NewBuiltin("set", func(args ...object.Value) object.Value {
		if len(args) < 2 {
			return object.UndefinedSingleton
		}
		bag().SetProperty(object.ToString(args[0]), args[1])
		return object.UndefinedSingleton
	}))
	o.SetProperty("has", object.NewBuiltin("has", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.NewBoolean(false)
		}
		_, ok := bag().GetProperty(object.ToString(args[0]))
		return object.NewBoolean(ok)
	}))
	o.SetProperty("all", object.NewBuiltin("all", func(args ...object.Value) object.Value {
		return bag()
	}))
	o.SetProperty("clear", object.NewBuiltin("clear", func(args ...object.Value) object.Value {
		if cur := s.current(); cur != nil {
			cur.state = object.NewObject()
		}
		return object.UndefinedSingleton
	}))
	return o
}

// pickSession 解析"当前该用哪个会话": 渲染上下文 → 默认 router 的活跃作用域。
func pickSession() *routerSession {
	if ctx := currentRenderCtx(); ctx != nil {
		return ctx.sess
	}
	if defaultRouter != nil {
		return defaultRouter.sessionFor(defaultRouter.activeScope())
	}
	return nil
}

// readEnvSignal 读一次环境版本 (读即订阅, 见 screen.go)。
func readEnvSignal() {
	if g := envSignal(); g != nil {
		object.CallFunction(g, nil)
	}
}

// readRouterRev 读一次 router 的重算版本 (router.rebuild 抬高它)。
func readRouterRev(r *router) {
	if r != nil && r.revGet != nil {
		object.CallFunction(r.revGet, nil)
	}
}

// ===== 会话上的活动态判定 (RouterLink 用) =====

// isActive 报告一条 to 是否命中当前路由 (前缀匹配: /items 命中 /items/42)。
func (s *routerSession) isActive(to object.Value) bool {
	cur := s.current()
	if cur == nil {
		return false
	}
	target, err := s.r.resolveTo(to, nil)
	if err != nil || target == nil || target.rec == nil || cur.rec == nil {
		return false
	}
	if target.path == "/" {
		return cur.path == "/"
	}
	return cur.path == target.path || strings.HasPrefix(cur.path, target.path+"/")
}
