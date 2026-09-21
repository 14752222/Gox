package gfx

import (
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// ===== gx/router: Vue3 Router 风格的路由系统 =====
//
// ## 为什么是模块层而不是内核层
//
// 路由需要的一切机制在内核里**已经有了**: 条件渲染 (函数子节点)、响应式
// (gx/solid)、透明宿主 (slot)、布局每帧重跑、整帧标脏。真正的缺口只有三块
// (agent_doc/gui-routing-options.md §1.2 的 R-a/R-b/R-c), 本模块把这三块补齐,
// 但**全部落在模块层**:
//
//	R-c 路径与参数匹配 → router_match.go (纯函数, 可脱离 VM 单测)
//	R-b 历史栈/守卫/键绑定 → 本文件的 routerSession + 三级守卫
//	R-a 切页丢状态     → router_view.go 的页面缓存 (keepAlive / 状态袋)
//
// 渲染内核 (布局/绘制/命中/脏矩形) 一行未改。唯一的增补是 app 的窗口号
// (render.go 的 app.id) —— "按窗口记账"需要的最小身份, 它同时是 gx/screen
// 的"窗口在哪块屏上"与 gx/router 的"每窗口独立导航栈"的共同地基。
//
// ## 与 Vue3 Router 的对应关系 (以及三处刻意的差异)
//
//	createRouter({routes, …})      createRouter
//	router.push/replace/back/go/…  同名同义 (都返回 Promise)
//	<RouterView>  <RouterLink>     同名同义
//	useRoute()/useRouter()         同名同义
//	beforeEach / beforeEnter       同名同义
//	beforeRouteEnter/Update/Leave  同名同义 (懒加载模块的命名导出)
//	lazy(() => import(…))          显式懒加载标记 (比 vue-router 多一步, 见下)
//
//	差异 1: **没有 URL 也没有 history 模式**。桌面应用没有地址栏, "历史"就是
//	        内存里的一个栈 (memory 语义); replace 与 push 的区别只体现在栈的
//	        写法上。要 deep-link 时用 process.argv 解析初始路径 (见文档)。
//	差异 2: **`to` 一律按绝对路径解析** (不支持 "./sub")。没有"当前 URL 作为
//	        基准"的语境, 相对路径只会引入歧义。
//	差异 3: **多了「作用域」(scope) 概念** —— 每个窗口一套独立导航栈, 并且
//	        可以把若干作用域绑成同步组。详见下面"多窗口"一节。
//	差异 4: **懒加载建议写 lazy(() => import(…))**。写 `() => import(…)` 也能
//	        跑, 但首次进入时组件级守卫 (beforeRouteEnter 等) 拿不到 —— 理由
//	        见 router_view.go 里 lazy 的说明。这条是能测出来的行为差异, 所以
//	        写进文档而不是"藏着也一样"。
//
// ## 多窗口 / 多屏幕 / 折叠屏
//
// 浏览器里"一个应用 = 一个文档 = 一套历史", 桌面与折叠设备不是这样:
//
//	同进程多窗口  → 各自要有独立导航栈 (主窗停在列表, 弹窗自己进详情)
//	多显示器      → 窗口换屏后尺寸/缩放/姿态变了, 界面要重排
//	折叠屏半折    → 同一块屏被折痕分成两段, 常见做法是左列表右详情
//
// 三件事被建模成同一套机制: **会话按作用域隔离 + 环境信号驱动重算**。
//
//	scope "win:1"  ← 窗口自动获得 (render() 返回的句柄上 scope() 读得到)
//	scope "popup"  ← 脚本显式起名 (<RouterView scope="popup">)
//	router.sync([wa, wb], {mode:"mirror"})   把两者绑成同步组
//
// 折叠态的处理在 router_view.go 的 "双栏": 姿态一变 (gx/screen 的环境版本
// 抬高) 视图就重算, 页面子树按需重建, 而**导航栈/参数/状态袋与树无关,
// 因此原样保持** —— 这就是"折叠态切换时的路由重建与状态保持"。

// ===== 导航条目 =====

// routeEntry 是一次导航的结果 (历史栈里的一项)。
//
// state 是**页面状态袋**: 一个普通 JS 对象, 生命周期与"这一条栈项"一致,
// 而不是与"页面子树"一致。这条区分是折叠态/换屏能保住状态的关键 —— 页面
// 子树可能因为姿态切换被销毁重建, 但只要栈项还在, state 里的东西就还在
// (页面用 useRouteState() 读写)。
type routeEntry struct {
	rec      *routeRecord
	path     string
	fullPath string
	params   map[string]string
	query    map[string]string
	hash     string
	matched  []*routeRecord
	state    object.Value // JS 对象 (惰性建: 只有页面真的用了才建)
	js       object.Value // 给脚本看的快照 (每次提交重建, 理由见 commitSignal)
}

func (e *routeEntry) name() string {
	if e == nil || e.rec == nil {
		return ""
	}
	return e.rec.name
}

// key 是"这一项在页面缓存里的键": 同一条记录 + 同一组参数 = 同一个页面实例。
//
// 参数必须参与 key: `/items/:id` 从 42 切到 43 是**另一个**页面。vue-router
// 会复用组件实例并触发 beforeRouteUpdate, 但本引擎的组件就是普通函数,
// "复用实例"无从谈起 —— 重建更符合"每次调用都是新实例"的既有语义
// (beforeRouteUpdate 仍然会跑, 供脚本做"参数变了"的副作用)。
func (e *routeEntry) key() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(e.path)
	if len(e.params) > 0 {
		keys := make([]string, 0, len(e.params))
		for k := range e.params {
			keys = append(keys, k)
		}
		for i := 1; i < len(keys); i++ { // 参数个数通常 0-3, 插入排序足够
			for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
				keys[j], keys[j-1] = keys[j-1], keys[j]
			}
		}
		for _, k := range keys {
			b.WriteString("|")
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(e.params[k])
		}
	}
	return b.String()
}

// ===== 会话 =====

// routerSession 是一套**独立的导航栈** (一个窗口 / 一个显式作用域一份)。
//
// 为什么栈在会话里而不是在 router 里: "多窗口各有各的历史"是本模块的核心
// 需求。把栈按作用域分开之后, "独立导航栈"是结构保证而不是纪律 —— 脚本在
// A 窗口连点三次 push, B 窗口的栈一个字节都不会动。
type routerSession struct {
	r     *router
	scope string

	stack []*routeEntry
	index int // 当前项下标 (-1 = 空栈)

	navGet object.Value // 当前路由的 signal getter (gx/solid 可用时)
	navSet object.Value
	raw    object.Value // 无 signal 时的退化存储

	busy     bool       // 正在跑一次导航 (守卫可能是异步的)
	queue    []*navTask // 排队中的导航 (同一窗口串行, 不并发)
	applying bool       // 本次提交来自同步组镜像 (用于打断组内回环)

	views []*routerViewHost // 挂在本会话上的视图
}

// routeGetter 返回本会话当前路由的取值函数 (读它就是订阅)。
// signal 不可用时退化为普通取值函数 —— 语义一致, 只是不再响应式。
func (s *routerSession) routeGetter() object.Value {
	if s.navGet != nil {
		return s.navGet
	}
	return object.NewBuiltin("route", func(args ...object.Value) object.Value {
		return s.raw
	})
}

func (s *routerSession) current() *routeEntry {
	if s.index < 0 || s.index >= len(s.stack) {
		return nil
	}
	return s.stack[s.index]
}

// previous 返回栈里上一条 (折叠双栏的左栏用它)。
func (s *routerSession) previous() *routeEntry {
	if s.index <= 0 {
		return nil
	}
	return s.stack[s.index-1]
}

// ===== Router =====

// 同步组的模式。
const (
	syncModeMirror = "mirror" // 路径同步, 各窗口状态各自独立 (默认)
	syncModeShare  = "share"  // 路径与**状态袋**都共享 (两窗口同一份 route.state)
	syncModeFollow = "follow" // 单向: 只有源作用域的导航被跟随
)

type routerSyncGroup struct {
	members []string
	mode    string
	source  string
	// warned 保证"成员没有会话"这条提示只出一次: 它是**配置写错**的信号
	// (最常见的是把窗口句柄当成了具名作用域), 每次导航都刷一遍就成噪音了。
	warned bool
}

// router 是一个路由实例。
type router struct {
	name  string
	table *routeTable

	routesRaw   object.Value // 脚本给的原始 routes (router.routes 原样返回)
	initialPath string       // createRouter 的 initial (缺省 "/")

	sessions map[string]*routerSession
	order    []string // 作用域创建序
	active   string   // 最近活跃的作用域

	beforeEach []object.Value
	afterEach  []object.Value
	onError    []object.Value

	syncGroups []*routerSyncGroup
	backKeys   bool
	keyBound   map[int]bool
	foldDual   bool // 折叠半开时是否双栏 (默认开)

	// revGet/revSet 是"视图重算"的专用信号 (router.rebuild 抬高它)。与 gx/screen
	// 的环境版本分开: 姿态变化走环境版本, 而"脚本自己要求重排"与显示器无关。
	revGet object.Value
	revSet object.Value
	// initJS 是"还没有任何会话"时 currentRoute() 的读数 (见 initialSnapshot)
	initJS object.Value

	obj object.Value // 惰性构造的 JS 对象
}

// ===== 模块级状态 =====

var (
	// 渲染上下文栈: 组件体执行期间压栈, useRoute/useRouter 与嵌套 RouterView
	// 都读栈顶。**这是本模块唯一的隐式上下文**, 成立的理由与内核自己的
	// object.PushWiringScope 完全一样: 组件体是同步执行的, 而它需要知道
	// "我属于哪个会话、哪一层"。用完即出栈。
	routerRenderStack []*routerRenderCtx

	// 待绑定的视图 (挂上了树但还没认领窗口, 见 flushRouterViews)。
	pendingRouterViews []*routerViewHost

	// defaultRouter 是最后创建的 router: `<RouterView>` 不写 router=、以及
	// useRoute/useRouter 在无渲染上下文时都退回它 (单路由应用的省事写法)。
	defaultRouter *router

	// allRoutersList 是全部已创建的 router (默认键绑定要给每个窗口装一遍)。
	allRoutersList []*router
)

type routerRenderCtx struct {
	r    *router
	sess *routerSession
	// depth 是"本页之内的 RouterView 该用哪一层": 见 router_view.go 的 recordAtDepth。
	depth int
}

func currentRenderCtx() *routerRenderCtx {
	if n := len(routerRenderStack); n > 0 {
		return routerRenderStack[n-1]
	}
	return nil
}

func allRouters() []*router { return allRoutersList }

// ===== 会话管理 =====

func (r *router) sessionFor(scope string) *routerSession {
	if s, ok := r.sessions[scope]; ok {
		// active 的语义是"**最近用到**的作用域" (而不是"最近创建"): 脚本在
		// 没有渲染上下文的场合 (全局快捷键、定时器、菜单回调) 调
		// router.push/back 时, 该落到"用户此刻在看的那个窗口"。
		r.active = scope
		return s
	}
	s := &routerSession{r: r, scope: scope, index: -1}
	if getter, setter := newRouteSignal(); getter != nil {
		s.navGet, s.navSet = getter, setter
	}
	r.sessions[scope] = s
	r.order = append(r.order, scope)
	r.active = scope
	if r.keyBound == nil {
		r.keyBound = map[int]bool{}
	}
	// 栈底先放 initial: 这样"第一次进 RouterView 就有一个可渲染的页面",
	// 而不是先空白再等一次 push。
	if init := r.initialEntry(); init != nil {
		s.stack = append(s.stack, init)
		s.index = 0
		s.commitSignal()
	}
	return s
}

// newRouteSignal 惰性造一对 signal。gx/solid 不在时 (无头宿主/纯 Go 单测)
// 返回 nil, 会话退化为非响应式。
func newRouteSignal() (object.Value, object.Value) {
	exports, ok := object.LookupBuiltinModule("gx/solid")
	if !ok {
		return nil, nil
	}
	createSignal, ok := exports["createSignal"]
	if !ok || !object.IsCallable(createSignal) {
		return nil, nil
	}
	res := object.CallFunction(createSignal, nil, object.NullSingleton)
	arr, ok := res.(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		return nil, nil
	}
	return arr.Elements[0], arr.Elements[1]
}

func (r *router) initialEntry() *routeEntry {
	path := r.initialPath
	if path == "" {
		path = "/"
	}
	e, err := r.resolveTo(object.NewString(path), nil)
	if err != nil {
		recordWarn("gx/router: initial %q 无法解析: %v", path, err)
		return nil
	}
	return e
}

// commitSignal 把当前栈项写进会话 signal (读方即重算)。
//
// **每次提交都重建 route 快照对象**: signal 的 setter 用 === 判重, 复用同一个
// 对象会让"回到同一页"这类提交静默地不通知任何订阅者 (界面不刷新)。
// 快照身份随每次导航变化是刻意的 —— 脚本里的 route 就是"这一次导航的读数"。
func (s *routerSession) commitSignal() {
	cur := s.current()
	if cur == nil {
		return
	}
	cur.js = routeEntryToJS(cur)
	if s.navSet != nil {
		object.CallFunction(s.navSet, nil, cur.js)
	} else {
		s.raw = cur.js
	}
	// 同时抬 router 级版本号: 视图之外的地方 (根级 UI 读 router.currentRoute(),
	// 或脚本自己写的 createEffect) 也订阅它, 于是"任意窗口导航"都能让它们重算。
	s.r.bumpRevSignal()
	for _, v := range s.views {
		v.invalidate()
	}
}

// ===== 目标解析 =====

type routeResolveError struct{ msg string }

func (e *routeResolveError) Error() string { return e.msg }

// resolveTo 把脚本给的 `to` 解析成一条 routeEntry。
//
//	"/items/42?tab=x#top"
//	{ name: "detail", params: { id: 42 }, query: { tab: "x" } }
//	{ path: "/items/42" }
//
// 解析结果**不含 replace 等动作标记** —— 那是"这次动作怎么做", 由调用方读。
func (r *router) resolveTo(to object.Value, extraParams map[string]string) (*routeEntry, error) {
	if to == nil {
		return nil, &routeResolveError{"to 不能为空"}
	}
	var path, query, hash string
	switch v := to.(type) {
	case *object.String:
		path, query, hash = routeSplitTarget(v.Value)
	case *object.Object:
		if p := objPropStr(v, "path"); p != "" {
			path, query, hash = routeSplitTarget(p)
			if qo, ok := objProp(v, "query").(*object.Object); ok {
				for k, desc := range qo.Properties {
					query = mergeQuery(query, k+"="+valueToParam(desc.Value))
				}
			} else if qs := objPropStr(v, "query"); qs != "" {
				query = mergeQuery(query, qs)
			}
			if h := objPropStr(v, "hash"); h != "" {
				hash = h
			}
		} else if name := objPropStr(v, "name"); name != "" {
			rec := r.table.byName[name]
			if rec == nil {
				return nil, &routeResolveError{"没有名为 " + name + " 的路由"}
			}
			params := map[string]string{}
			if po, ok := objProp(v, "params").(*object.Object); ok {
				for k, desc := range po.Properties {
					params[k] = valueToParam(desc.Value)
				}
			}
			built, ok := routeBuildPath(rec, params)
			if !ok {
				return nil, &routeResolveError{"路由 " + name + " 缺少必填参数"}
			}
			path = built
			if qo, ok := objProp(v, "query").(*object.Object); ok {
				for k, desc := range qo.Properties {
					query = mergeQuery(query, k+"="+valueToParam(desc.Value))
				}
			}
			hash = objPropStr(v, "hash")
		} else {
			return nil, &routeResolveError{"to 对象需要 path 或 name"}
		}
	default:
		return nil, &routeResolveError{"to 必须是字符串或 {path|name} 对象, 实际 " + string(to.Type())}
	}

	path = normalizeRoutePath(path)
	rec, params := r.table.routeFind(path)
	if rec == nil {
		return nil, &routeResolveError{"没有匹配 " + path + " 的路由 (也没有 * 兜底)"}
	}
	for k, v := range extraParams {
		params[k] = v
	}
	entry := &routeEntry{
		rec:     rec,
		path:    path,
		params:  params,
		query:   routeParseQuery(query),
		hash:    hash,
		matched: routeRecordMatchChain(rec),
	}
	entry.fullPath = routeJoinFullPath(path, routeBuildQuery(entry.query), hash)
	return entry, nil
}

func mergeQuery(base, add string) string {
	if add == "" {
		return base
	}
	m := routeParseQuery(base)
	for k, v := range routeParseQuery(add) {
		m[k] = v
	}
	return routeBuildQuery(m)
}

// valueToParam 把参数值转成字符串 (数字/布尔都合法: {id: 42} 是最常见的写法)。
func valueToParam(v object.Value) string {
	if s, ok := v.(*object.String); ok {
		return s.Value
	}
	if v == nil {
		return ""
	}
	return object.ToString(v)
}

// ===== 路由快照 → JS 对象 =====

func routeEntryToJS(e *routeEntry) object.Value {
	if e == nil {
		return object.NullSingleton
	}
	o := object.NewObject()
	o.SetProperty("path", object.NewString(e.path))
	o.SetProperty("fullPath", object.NewString(e.fullPath))
	o.SetProperty("name", object.NewString(e.name()))
	o.SetProperty("params", stringMapToJS(e.params))
	o.SetProperty("query", stringMapToJS(e.query))
	o.SetProperty("hash", object.NewString(e.hash))
	if e.rec != nil && e.rec.meta != nil {
		o.SetProperty("meta", e.rec.meta)
	} else {
		o.SetProperty("meta", object.NewObject())
	}
	m := make([]object.Value, 0, len(e.matched))
	for _, rec := range e.matched {
		mo := object.NewObject()
		mo.SetProperty("path", object.NewString(rec.path))
		mo.SetProperty("name", object.NewString(rec.name))
		if rec.meta != nil {
			mo.SetProperty("meta", rec.meta)
		} else {
			mo.SetProperty("meta", object.NewObject())
		}
		m = append(m, mo)
	}
	o.SetProperty("matched", object.NewArray(m))
	// state: 状态袋 (同一栈项永远返回同一个对象 ⇒ 页面重建后读到的还是那份)
	o.SetProperty("state", e.stateObject())
	return o
}

func (e *routeEntry) stateObject() object.Value {
	if e.state == nil {
		e.state = object.NewObject()
	}
	return e.state
}

func stringMapToJS(m map[string]string) object.Value {
	o := object.NewObject()
	for k, v := range m {
		o.SetProperty(k, object.NewString(v))
	}
	return o
}

// ===== 守卫 =====

// guardStep 是一次守卫调用。
//
// label 只用于诊断 (进 gx/dev 的警告缓冲): 三级守卫叠在一起时, "是哪一级
// 拦的"是最省排查时间的一条信息。
type guardStep struct {
	label string
	fn    object.Value
}

const routerMaxRedirects = 8

// buildGuards 按 Vue 的顺序收集这一次导航要跑的守卫:
//
//  1. 离开拦截  beforeRouteLeave  当前路由各层, 由内向外
//  2. 全局      beforeEach
//  3. 进入拦截  beforeEnter       目标链上"新出现"的记录, 由外向内
//  4. 更新拦截  beforeRouteUpdate 同一条记录但参数变了
//  5. 进入组件  beforeRouteEnter  目标链各层, 由外向内
//
// 组件级守卫来自三处 (优先级从高到低):
//
//	(a) 懒加载模块的命名导出  export function beforeRouteLeave() {}
//	(b) 组件函数对象上的属性  Home.beforeRouteLeave = () => {}
//	(c) 路由记录自身的字段    { path, beforeEnter }
//
// (a) 与 (b) 的差别只是"组件怎么给的" —— 懒加载拿到模块命名空间, 非懒加载
// 拿到函数对象, 两者从同一个名字上读, 脚本侧看不出差别。
func (r *router) buildGuards(ctx *navCtx) []guardStep {
	var out []guardStep
	from, to := ctx.from, ctx.to

	if from != nil {
		for i := len(from.matched) - 1; i >= 0; i-- {
			rec := from.matched[i]
			if fn := componentHook(rec, "beforeRouteLeave"); fn != nil {
				out = append(out, guardStep{"beforeRouteLeave(" + rec.path + ")", fn})
			}
		}
	}
	for _, fn := range r.beforeEach {
		out = append(out, guardStep{"beforeEach", fn})
	}
	if to != nil {
		for _, rec := range to.matched {
			if from != nil && containsRecord(from.matched, rec) {
				// 复用同一层: 参数变了才跑 beforeRouteUpdate
				if !sameParams(from, to) {
					if fn := componentHook(rec, "beforeRouteUpdate"); fn != nil {
						out = append(out, guardStep{"beforeRouteUpdate(" + rec.path + ")", fn})
					}
				}
				continue
			}
			if rec.beforeEnter != nil {
				out = append(out, guardStep{"beforeEnter(" + rec.path + ")", rec.beforeEnter})
			}
		}
		for _, rec := range to.matched {
			if fn := componentHook(rec, "beforeRouteEnter"); fn != nil {
				out = append(out, guardStep{"beforeRouteEnter(" + rec.path + ")", fn})
			}
		}
	}
	return out
}

// componentHook 读一条记录的组件级守卫 (读不到 → nil, 调用方就不产生这一步,
// 于是"没写守卫"的路径上没有任何额外开销)。
func componentHook(rec *routeRecord, name string) object.Value {
	if rec == nil {
		return nil
	}
	if fn, ok := rec.hooks[name]; ok && object.IsCallable(fn) {
		return fn
	}
	if rec.resolved != nil {
		if v, ok := rec.resolved.GetProperty(name); ok && object.IsCallable(v) {
			return v
		}
	}
	return nil
}

func containsRecord(list []*routeRecord, rec *routeRecord) bool {
	for _, x := range list {
		if x == rec {
			return true
		}
	}
	return false
}

// sameParams 保守判定"两次导航的参数一样"。逐层比对参数的收益不值那个复杂度,
// 而误判成"变了"的代价只是多跑一次 beforeRouteUpdate。
func sameParams(a, b *routeEntry) bool {
	if a == nil || b == nil {
		return false
	}
	if len(a.params) != len(b.params) {
		return false
	}
	for k, v := range a.params {
		if b.params[k] != v {
			return false
		}
	}
	return true
}

// ===== 导航执行 =====

// navTask 是一次导航请求。
type navTask struct {
	to      object.Value
	replace bool
	pop     *routeEntry // 非 nil: 弹栈式 (back/forward/go), 目标已在栈里
	force   bool        // 跳过"目标与当前相同"的短路
	promise *object.Promise
}

// navCtx 是一次导航的执行上下文。整个流程是"先校验, 再提交": 守卫期间
// **不动栈**, 于是"被拦截"的导航天然不会留下半截状态 (这也是 back 能安全
// 回滚的前提)。
type navCtx struct {
	sess      *routerSession
	task      *navTask
	from      *routeEntry
	to        *routeEntry
	redirects int
	guards    []guardStep
	gi        int
}

// navigate 启动一次导航 (忙则排队: 同一窗口串行, 不并发)。
func (s *routerSession) navigate(task *navTask) {
	if s.busy {
		s.queue = append(s.queue, task)
		return
	}
	s.busy = true
	s.runTask(task)
}

func (s *routerSession) runTask(task *navTask) {
	ctx := &navCtx{sess: s, task: task, from: s.current()}
	if task.pop != nil {
		ctx.to = task.pop
	} else {
		e, err := s.r.resolveTo(task.to, nil)
		if err != nil {
			s.fail(ctx, object.NewErrorWithName("Error", "gx/router: "+err.Error()))
			return
		}
		ctx.to = e
	}
	s.execNav(ctx)
}

// execNav 是导航主链: 短路 → 重定向字段 → 等懒加载 → 收守卫 → 跑守卫 → 提交。
func (s *routerSession) execNav(ctx *navCtx) {
	if ctx.to == nil {
		s.fail(ctx, object.NewErrorWithName("Error", "gx/router: 目标路由为空"))
		return
	}
	// 目标就是当前页: 短路 (但 force 时仍然提交一次 —— 脚本可能靠它"刷新")。
	if !ctx.task.force && ctx.task.pop == nil && ctx.from != nil &&
		ctx.from.rec == ctx.to.rec && ctx.from.key() == ctx.to.key() {
		s.finish(ctx, navOutcome(routeEntryToJS(ctx.to), "", ""))
		return
	}
	// 记录上的 redirect 字段: 字符串 / {path|name} / 函数
	if ctx.to.rec != nil && ctx.to.rec.redirect != nil {
		rd := ctx.to.rec.redirect
		if object.IsCallable(rd) {
			rd = object.CallFunction(rd, nil, routeEntryToJS(ctx.to))
			if err := takeCallbackErr(); err != nil {
				s.fail(ctx, object.NewErrorWithName("Error", "gx/router: redirect 函数抛错: "+err.Error()))
				return
			}
		}
		s.restart(ctx, rd)
		return
	}
	// 等目标链上的懒加载就绪: 组件级守卫只能从"已加载的模块命名导出"里读,
	// 所以必须先等 (见 router_view.go 的 lazy 说明)。
	if !awaitChain(ctx.to.matched, func() { s.execNavAfterLoad(ctx) }) {
		return // 已挂起; cont 结算后会继续 (也可能已经在本调用内同步续跑过)
	}
	s.execNavAfterLoad(ctx)
}

func (s *routerSession) execNavAfterLoad(ctx *navCtx) {
	if rec := ctx.to.rec; rec != nil && rec.loadErr != nil {
		// 加载失败: 不拦导航 —— 用户已经点了"去那一页", 把他留在旧页面上
		// 更没有出路。提交过去, 由视图显示 error 分支 (页面自己能重试)。
		recordWarn("gx/router: 目标组件加载失败: %s", object.ToString(rec.loadErr))
	}
	ctx.guards = s.r.buildGuards(ctx)
	ctx.gi = 0
	s.runGuard(ctx)
}

// restart 处理一次重定向 (守卫返回值或记录上的 redirect 字段)。
func (s *routerSession) restart(ctx *navCtx, to object.Value) {
	ctx.redirects++
	if ctx.redirects > routerMaxRedirects {
		s.fail(ctx, object.NewErrorWithName("Error",
			"gx/router: 重定向次数超过 "+strconv.Itoa(routerMaxRedirects)+" 次, 疑似死循环"))
		return
	}
	e, err := s.r.resolveTo(to, nil)
	if err != nil {
		s.fail(ctx, object.NewErrorWithName("Error", "gx/router: 重定向目标无法解析: "+err.Error()))
		return
	}
	ctx.to = e
	ctx.task.pop = nil // 重定向之后就是一次全新的 push/replace
	s.execNav(ctx)
}

// runGuard 逐条执行守卫; 遇到 Promise 就挂起 (由 then 回调续跑)。
func (s *routerSession) runGuard(ctx *navCtx) {
	for ctx.gi < len(ctx.guards) {
		step := ctx.guards[ctx.gi]
		ctx.gi++
		res := object.CallFunction(step.fn, nil, routeEntryToJS(ctx.to), routeEntryFromJS(ctx.from))
		if err := takeCallbackErr(); err != nil {
			s.fail(ctx, object.NewErrorWithName("Error", "gx/router: "+step.label+" 抛错: "+err.Error()))
			return
		}
		if p, ok := res.(*object.Promise); ok {
			// 异步守卫: 挂起, 结算后从下一条继续。
			// settled 挡掉"fulfilled 与 rejected 都被触发"的极端情形 (Promise
			// 语义上不会, 但脚本可以自己造 Promise 对象)。
			settled := false
			p.OnFulfilled(object.NewBuiltin("guard-then", func(args ...object.Value) object.Value {
				if settled {
					return object.UndefinedSingleton
				}
				settled = true
				if stop := s.applyGuardResult(ctx, argOrNil(args)); !stop {
					s.runGuard(ctx)
				}
				return object.UndefinedSingleton
			}))
			p.OnRejected(object.NewBuiltin("guard-catch", func(args ...object.Value) object.Value {
				if settled {
					return object.UndefinedSingleton
				}
				settled = true
				s.fail(ctx, object.NewErrorWithName("Error", "gx/router: "+step.label+
					" 被拒绝: "+object.ToString(argOrNil(args))))
				return object.UndefinedSingleton
			}))
			return
		}
		if s.applyGuardResult(ctx, res) {
			return
		}
	}
	s.commit(ctx)
}

// applyGuardResult 解释守卫的返回值; 返回 true 表示"导航已终结或已挂起"。
//
//	undefined / true / 其它普通值 → 放行
//	false                        → 中止 (栈不动)
//	字符串 / {path|name} 对象     → 重定向
//	Error                        → 失败
func (s *routerSession) applyGuardResult(ctx *navCtx, res object.Value) bool {
	if err, ok := res.(*object.Error); ok {
		s.fail(ctx, err)
		return true
	}
	if b, ok := res.(*object.Boolean); ok && !b.Value {
		s.finish(ctx, navOutcome(routeEntryFromJS(ctx.from), "aborted", "守卫返回 false"))
		return true
	}
	if isRedirectTarget(res) {
		s.restart(ctx, res)
		return true
	}
	return false
}

// isRedirectTarget 判断守卫返回值是"去哪儿"还是"放行"。
//
// 字符串与含 path/name 的对象算重定向; 其余 (undefined/null/true/数字/其它对象)
// 都放行 —— `return 1` 更可能是笔误, 把它当地址会得到一条无法解析的目标,
// 而"放行 + 让页面自己处理"更符合"守卫只表达 yes/no/去哪儿"这三类语义。
func isRedirectTarget(v object.Value) bool {
	switch x := v.(type) {
	case *object.String:
		return x.Value != ""
	case *object.Object:
		if _, ok := x.GetProperty("path"); ok {
			return true
		}
		if _, ok := x.GetProperty("name"); ok {
			return true
		}
	}
	return false
}

// commit 提交这次导航 (改栈 + 通知 + 同步组)。
func (s *routerSession) commit(ctx *navCtx) {
	if ctx.to == nil {
		s.fail(ctx, object.NewErrorWithName("Error", "gx/router: 目标路由为空"))
		return
	}
	if ctx.to.rec != nil && ctx.to.rec.component == nil && ctx.to.rec.redirect == nil && len(ctx.to.rec.children) == 0 {
		// 布局父记录 (只有 children): 允许提交, 视图那一层渲染不出内容而已。
		// 不拦是因为"/parent"本身就是个合法去处 (父布局会显示自己的 RouterView)。
	}
	if ctx.task.pop != nil {
		// 弹栈: 目标已在栈里, 只移动指针。**必须按对象身份找**, 不能按 path ——
		// 历史里可能有三份 /items。
		idx := -1
		for i, e := range s.stack {
			if e == ctx.to {
				idx = i
				break
			}
		}
		if idx < 0 {
			s.finish(ctx, navOutcome(routeEntryFromJS(ctx.from), "exhausted", "目标不在历史栈里"))
			return
		}
		s.index = idx
	} else if ctx.task.replace {
		if s.index < 0 {
			s.stack = append(s.stack, ctx.to)
			s.index = 0
		} else {
			s.stack[s.index] = ctx.to
			// replace 之后原来的"前进"部分必须丢弃 (与浏览器 replace 同义)
			s.stack = s.stack[:s.index+1]
		}
	} else {
		if s.index >= 0 && s.index < len(s.stack)-1 {
			s.stack = s.stack[:s.index+1] // push 截断前进部分
		}
		s.stack = append(s.stack, ctx.to)
		s.index = len(s.stack) - 1
	}

	toJS := routeEntryToJS(ctx.to)
	fromJS := routeEntryFromJS(ctx.from)
	s.finish(ctx, navOutcome(toJS, "", ""))
	s.commitSignal()

	// afterEach: 提交之后跑, 拿到的 route 是**已经生效**的那一份; 不参与拦截,
	// 抛错只记警告。
	for _, fn := range s.r.afterEach {
		object.CallFunction(fn, nil, toJS, fromJS)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/router afterEach: 回调抛错: %v", err)
		}
	}
	s.r.propagateSync(s, ctx.to)
}

// finish 结束本次导航 (结算 Promise + 放行队列)。
func (s *routerSession) finish(ctx *navCtx, outcome object.Value) {
	s.busy = false
	if ctx.task != nil && ctx.task.promise != nil {
		ctx.task.promise.Resolve(outcome)
	}
	if len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.navigate(next)
	}
}

// fail 以错误结束本次导航。
func (s *routerSession) fail(ctx *navCtx, err object.Value) {
	s.busy = false
	s.reportError(err)
	if ctx.task != nil && ctx.task.promise != nil {
		ctx.task.promise.Reject(err)
	}
	if len(s.queue) > 0 {
		next := s.queue[0]
		s.queue = s.queue[1:]
		s.navigate(next)
	}
}

func (s *routerSession) reportError(err object.Value) {
	for _, fn := range s.r.onError {
		object.CallFunction(fn, nil, err)
		if e := takeCallbackErr(); e != nil {
			recordWarn("gx/router onError: 回调抛错: %v", e)
		}
	}
}

// navOutcome 组装导航结果的 JS 对象: {ok, route, reason, detail}。
func navOutcome(route object.Value, reason, detail string) object.Value {
	o := object.NewObject()
	o.SetProperty("ok", object.NewBoolean(reason == ""))
	o.SetProperty("route", route)
	o.SetProperty("reason", object.NewString(reason))
	if detail != "" {
		o.SetProperty("detail", object.NewString(detail))
	}
	return o
}

func routeEntryFromJS(e *routeEntry) object.Value {
	if e == nil {
		return object.NullSingleton
	}
	return routeEntryToJS(e)
}

func argOrNil(args []object.Value) object.Value {
	if len(args) == 0 {
		return object.UndefinedSingleton
	}
	return args[0]
}

// ===== 跨窗口 / 跨屏同步 =====

// propagateSync 把刚发生在 src 会话上的导航同步给同组的其它作用域。
func (r *router) propagateSync(src *routerSession, entry *routeEntry) {
	if src.applying || entry == nil {
		return
	}
	for _, g := range r.syncGroups {
		if !containsStr(g.members, src.scope) {
			continue
		}
		if g.mode == syncModeFollow && g.source != "" && g.source != src.scope {
			continue
		}
		for _, scope := range g.members {
			if scope == src.scope {
				continue
			}
			dst, ok := r.sessions[scope]
			if !ok {
				// 成员还没有会话: 可能是"窗口还没挂载 RouterView"(正常, 稍后
				// 自己会绑上), 也可能是名字写错 —— 这一条只说一次。
				if !g.warned {
					g.warned = true
					recordWarn("gx/router sync: 作用域 %q 还没有会话 (窗口句柄代表 win:N 自动作用域; 具名作用域要用 scope= 字符串), 本次同步已跳过", scope)
				}
				continue
			}
			dst.applying = true
			var target *routeEntry
			if g.mode == syncModeShare {
				// share: 两边共用同一条栈项 ⇒ 连页面状态袋都共享
				// (这是脚本可见的"跨窗口状态同步"语义)
				target = entry
			} else {
				copied := *entry
				copied.state = nil // mirror: 路径同步, 页面状态各自独立
				copied.js = nil
				target = &copied
			}
			// 把"当前项之后"的部分截掉再压, 保持各窗口的栈形状一致
			cut := dst.index + 1
			if cut > len(dst.stack) {
				cut = len(dst.stack)
			}
			if cut < 0 {
				cut = 0
			}
			dst.stack = append(dst.stack[:cut], target)
			dst.index = len(dst.stack) - 1
			dst.commitSignal()
			dst.applying = false
		}
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// ===== 会话对外操作 (返回 Promise 以便脚本 await) =====

// startNav 发起一次导航并返回 Promise。
//
// 返回 Promise 而不是同步布尔: 守卫可以是异步的 —— 接口形态必须容得下
// "这次导航还没结束", 否则以后加异步守卫就是破坏性变更。
func (s *routerSession) startNav(task *navTask) object.Value {
	p := object.NewPromise()
	task.promise = p
	s.navigate(task)
	return p
}

// resolvedNav 立即结算的一次导航 (没有历史可退这类"当场就有结论"的情形)。
func resolvedNav(reason, detail string, cur *routeEntry) object.Value {
	p := object.NewPromise()
	p.Resolve(navOutcome(routeEntryFromJS(cur), reason, detail))
	return p
}

func (s *routerSession) back() object.Value {
	if s.index <= 0 {
		return resolvedNav("exhausted", "已经在栈底", s.current())
	}
	return s.startNav(&navTask{pop: s.stack[s.index-1], force: true})
}

func (s *routerSession) forward() object.Value {
	if s.index < 0 || s.index >= len(s.stack)-1 {
		return resolvedNav("exhausted", "已经在栈顶", s.current())
	}
	return s.startNav(&navTask{pop: s.stack[s.index+1], force: true})
}

func (s *routerSession) goDelta(n int) object.Value {
	idx := s.index + n
	if n == 0 || idx < 0 || idx >= len(s.stack) {
		return resolvedNav("exhausted", "目标位置超出历史栈", s.current())
	}
	return s.startNav(&navTask{pop: s.stack[idx], force: true})
}

// ===== 默认键绑定 (Alt+← / Alt+→) =====

// bindRouterKeys 给窗口根节点补上"后退/前进"热键。
//
// 手法与 select 的 attachSelectHandler 一致: **包装**脚本自己的 onKeyDown
// 而不是覆盖它 (脚本可能已经在根上挂了快捷键), 两层都跑。
//
// 键位选 Alt+←/→ 的理由: Backspace 已被输入框吃掉, Esc 是 dialog/select 的
// "取消"语义, 而 Alt+方向键是浏览器与主流桌面应用共同的"历史导航"键位,
// 冲突面最小。不要它就把 createRouter 的 backKeys 置 false。
func bindRouterKeys(a *app) {
	if a == nil || a.root == nil {
		return
	}
	for _, r := range allRouters() {
		if !r.backKeys || r.keyBound[a.id] {
			continue
		}
		if r.keyBound == nil {
			r.keyBound = map[int]bool{}
		}
		r.keyBound[a.id] = true
		installBackKeys(r, a)
	}
}

func installBackKeys(r *router, a *app) {
	root := a.root
	if root == nil {
		return
	}
	prev := root.Props["onKeyDown"]
	aID := a.id
	handler := object.NewBuiltin("router-back-keys", func(args ...object.Value) object.Value {
		if eo, ok := argOrNil(args).(*object.Object); ok && objPropBool(eo, "alt") {
			switch objPropStr(eo, "key") {
			case "ArrowLeft":
				if s := r.sessionForOptional(appScopeOfID(aID)); s != nil {
					s.back()
				}
			case "ArrowRight":
				if s := r.sessionForOptional(appScopeOfID(aID)); s != nil {
					s.forward()
				}
			}
		}
		if object.IsCallable(prev) {
			return object.CallFunction(prev, nil, args...)
		}
		return object.UndefinedSingleton
	})
	root.Props["onKeyDown"] = handler
}

// appScopeOfID 按窗口号取作用域名 (键处理时窗口可能已经关了 → 空串)。
func appScopeOfID(id int) string {
	appMu.Lock()
	defer appMu.Unlock()
	for _, a := range apps {
		if a.id == id {
			return appScope(a)
		}
	}
	return ""
}

// sessionForOptional 只在会话已存在时取它 (键绑定不该凭空造出一个会话:
// 那会让"没打开过路由的窗口"多出一份空历史)。
func (r *router) sessionForOptional(scope string) *routerSession {
	if scope == "" {
		return nil
	}
	return r.sessions[scope]
}

// ===== JS 对象与模块注册 =====

// jsObject 把 router 包装成脚本可见的对象 (同一实例复用同一个对象)。
func (r *router) jsObject() object.Value {
	if r.obj != nil {
		return r.obj
	}
	o := object.NewObject()
	o.SetProperty("name", object.NewString(r.name))
	o.SetProperty("routes", r.routesRaw)

	// currentRoute() 是**取值函数** (signal 语义): 放进函数 prop / 函数子节点
	// 里就是响应式的。不做成属性 —— 属性会被快照, 而路由随时在变。
	o.SetProperty("currentRoute", object.NewBuiltin("currentRoute", func(args ...object.Value) object.Value {
		// 先订阅 router 版本号: 这样"页面之外"的读取 (顶部标题、状态栏)
		// 在任何窗口导航后都会重算, 而不是冻在第一次求值那一刻。
		if r.revGet != nil {
			object.CallFunction(r.revGet, nil)
		}
		s := r.pickScopeArgRead(args)
		if s == nil {
			// 还没有任何会话 (构建期读一次): 给 initial 的快照而不是 null ——
			// 脚本里 `router.currentRoute().path` 是最常见的写法, 返回 null
			// 会让它在挂载前当场抛错。
			return r.initialSnapshot()
		}
		return object.CallFunction(s.routeGetter(), nil)
	}))
	o.SetProperty("resolve", object.NewBuiltin("resolve", func(args ...object.Value) object.Value {
		e, err := r.resolveTo(argOrNil(args), nil)
		if err != nil {
			return object.NewErrorWithName("Error", "gx/router: "+err.Error())
		}
		return routeEntryToJS(e)
	}))
	o.SetProperty("push", object.NewBuiltin("push", func(args ...object.Value) object.Value {
		return r.navAction(args, false)
	}))
	o.SetProperty("replace", object.NewBuiltin("replace", func(args ...object.Value) object.Value {
		return r.navAction(args, true)
	}))
	o.SetProperty("back", object.NewBuiltin("back", func(args ...object.Value) object.Value {
		if s := r.pickScopeArg(args); s != nil {
			return s.back()
		}
		return resolvedNav("no-scope", "没有可用会话", nil)
	}))
	o.SetProperty("forward", object.NewBuiltin("forward", func(args ...object.Value) object.Value {
		if s := r.pickScopeArg(args); s != nil {
			return s.forward()
		}
		return resolvedNav("no-scope", "没有可用会话", nil)
	}))
	o.SetProperty("go", object.NewBuiltin("go", func(args ...object.Value) object.Value {
		n := 0
		if num, ok := argOrNil(args).(*object.Number); ok {
			n = int(num.Value)
		}
		if s := r.pickScopeArg(args[1:]); s != nil {
			return s.goDelta(n)
		}
		return resolvedNav("no-scope", "没有可用会话", nil)
	}))
	o.SetProperty("isActive", object.NewBuiltin("isActive", func(args ...object.Value) object.Value {
		s := r.pickScopeArgRead(args[1:])
		if s == nil {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(s.isActive(argOrNil(args)))
	}))
	o.SetProperty("isExactActive", object.NewBuiltin("isExactActive", func(args ...object.Value) object.Value {
		s := r.pickScopeArgRead(args[1:])
		cur := (*routeEntry)(nil)
		if s != nil {
			cur = s.current()
		}
		if cur == nil {
			return object.NewBoolean(false)
		}
		e, err := r.resolveTo(argOrNil(args), nil)
		if err != nil {
			return object.NewBoolean(false)
		}
		return object.NewBoolean(cur.path == e.path)
	}))
	o.SetProperty("beforeEach", object.NewBuiltin("beforeEach", func(args ...object.Value) object.Value {
		return r.addHook(&r.beforeEach, args, "beforeEach")
	}))
	o.SetProperty("afterEach", object.NewBuiltin("afterEach", func(args ...object.Value) object.Value {
		return r.addHook(&r.afterEach, args, "afterEach")
	}))
	o.SetProperty("onError", object.NewBuiltin("onError", func(args ...object.Value) object.Value {
		return r.addHook(&r.onError, args, "onError")
	}))
	o.SetProperty("scopes", object.NewBuiltin("scopes", func(args ...object.Value) object.Value {
		out := make([]object.Value, 0, len(r.order))
		for _, sc := range r.order {
			so := object.NewObject()
			so.SetProperty("scope", object.NewString(sc))
			s := r.sessions[sc]
			so.SetProperty("depth", object.NewNumber(float64(len(s.stack))))
			so.SetProperty("index", object.NewNumber(float64(s.index)))
			so.SetProperty("path", object.NewString(currentPathOf(s)))
			so.SetProperty("busy", object.NewBoolean(s.busy))
			out = append(out, so)
		}
		return object.NewArray(out)
	}))
	o.SetProperty("history", object.NewBuiltin("history", func(args ...object.Value) object.Value {
		s := r.pickScopeArgRead(args)
		if s == nil {
			return object.NewArray(nil)
		}
		out := make([]object.Value, 0, len(s.stack))
		for _, e := range s.stack {
			eo := object.NewObject()
			eo.SetProperty("path", object.NewString(e.path))
			eo.SetProperty("fullPath", object.NewString(e.fullPath))
			eo.SetProperty("name", object.NewString(e.name()))
			out = append(out, eo)
		}
		return object.NewArray(out)
	}))
	o.SetProperty("index", object.NewBuiltin("index", func(args ...object.Value) object.Value {
		if s := r.pickScopeArgRead(args); s != nil {
			return object.NewNumber(float64(s.index))
		}
		return object.NewNumber(-1)
	}))
	o.SetProperty("sync", object.NewBuiltin("sync", func(args ...object.Value) object.Value {
		return r.jsSync(args)
	}))
	o.SetProperty("unsync", object.NewBuiltin("unsync", func(args ...object.Value) object.Value {
		r.syncGroups = nil
		return object.UndefinedSingleton
	}))
	o.SetProperty("rebuild", object.NewBuiltin("rebuild", func(args ...object.Value) object.Value {
		r.bumpRevision()
		return object.UndefinedSingleton
	}))
	o.SetProperty("__goxRouter", object.NewBuiltin("__goxRouter", func(args ...object.Value) object.Value {
		return &routerRefValue{r: r}
	}))
	r.obj = o
	return o
}

func currentPathOf(s *routerSession) string {
	if cur := s.current(); cur != nil {
		return cur.path
	}
	return ""
}

// pickScopeArg 解析"这一次操作作用于哪个会话": 显式作用域名/窗口句柄 → 那个,
// 否则用当前渲染上下文 → 最近活跃的会话 (**没有就建一个**, 因为 push/back
// 这类动作必须有个落点)。
func (r *router) pickScopeArg(args []object.Value) *routerSession {
	if len(args) > 0 {
		if scope, ok := r.scopeNameOf(args[0]); ok {
			return r.sessionFor(scope)
		}
	}
	if ctx := currentRenderCtx(); ctx != nil && ctx.r == r {
		return ctx.sess
	}
	return r.sessionFor(r.activeScope())
}

// pickScopeArgRead 与 pickScopeArg 同源, 但**绝不新建会话** —— 供 currentRoute /
// history / index 这类只读接口用。
//
// 为什么这条区分是必须的: 只读接口会在 h() 阶段被函数 prop / 函数子节点的
// effect 立刻求值一次, 那时窗口还没注册、会话还没建; 若只读接口顺手建会话,
// 每个应用都会凭空多出一个"没人用的 default 作用域", 而且根级 UI 会把自己
// 绑死在那个空作用域上 (症状: 顶部路由文本永远停在 initial 不动)。
func (r *router) pickScopeArgRead(args []object.Value) *routerSession {
	if len(args) > 0 {
		if scope, ok := r.scopeNameOf(args[0]); ok {
			return r.sessionForOptional(scope)
		}
	}
	if ctx := currentRenderCtx(); ctx != nil && ctx.r == r {
		return ctx.sess
	}
	return r.sessionForOptional(r.activeScope())
}

// initialSnapshot 是"还没有会话"时的当前路由读数 (基于 initial 解析)。
func (r *router) initialSnapshot() object.Value {
	if r.initJS != nil {
		return r.initJS
	}
	e, err := r.resolveTo(object.NewString(r.initialPath), nil)
	if err != nil {
		r.initJS = object.NullSingleton
		return r.initJS
	}
	r.initJS = routeEntryToJS(e)
	return r.initJS
}

// scopeNameOf 把"作用域名 / 窗口句柄"统一成作用域名。
func (r *router) scopeNameOf(v object.Value) (string, bool) {
	switch x := v.(type) {
	case *object.String:
		if x.Value != "" {
			return x.Value, true
		}
	case *object.Object:
		// 窗口句柄: 用它的 scope() 方法 (见 window.go)
		if fn, ok := x.GetProperty("scope"); ok && object.IsCallable(fn) {
			if _, isWin := x.GetProperty("__goxWindow"); isWin {
				return object.ToString(object.CallFunction(fn, nil)), true
			}
		}
	}
	return "", false
}

// navAction 实现 push / replace。
func (r *router) navAction(args []object.Value, replace bool) object.Value {
	to := argOrNil(args)
	// 可选的第 2 个参数: {scope: …} 或窗口句柄 (目标窗口)
	target := r.pickScopeArg(args[1:])
	if o, ok := argOrNil(args[1:]).(*object.Object); ok {
		if scope, ok := r.scopeNameOf(objProp(o, "scope")); ok {
			target = r.sessionFor(scope)
		}
	}
	if target == nil {
		return resolvedNav("no-scope", "没有可用会话", nil)
	}
	return target.startNav(&navTask{to: to, replace: replace})
}

// activeScope 返回"最近活跃的作用域"; 没有任何会话时用第一个窗口兜底。
func (r *router) activeScope() string {
	if r.active != "" {
		return r.active
	}
	if a := currentApp(); a != nil {
		return appScope(a)
	}
	return "default"
}

func (r *router) addHook(list *[]object.Value, args []object.Value, name string) object.Value {
	if len(args) == 0 || !object.IsCallable(args[0]) {
		return object.NewTypeError("gx/router %s: 需要回调函数", name)
	}
	fn := args[0]
	*list = append(*list, fn)
	return object.NewBuiltin(name+"-off", func(args ...object.Value) object.Value {
		for i, x := range *list {
			if x == fn {
				*list = append((*list)[:i], (*list)[i+1:]...)
				break
			}
		}
		return object.UndefinedSingleton
	})
}

// bumpRevSignal 只抬版本号 (通知订阅者重算), 不主动触发视图重绘 ——
// 视图自己会经会话的 signal 收到通知, 这里再来一次就是多余的重排。
func (r *router) bumpRevSignal() {
	if r.revSet != nil {
		object.CallFunction(r.revSet, nil, object.NewNumber(1))
	}
}

// bumpRevision 抬高视图重算信号 (router.rebuild: 脚本主动要求全量重排,
// 例如宿主把窗口挪到另一块屏后补一次通知)。
func (r *router) bumpRevision() {
	r.bumpRevSignal()
	for _, scope := range r.order {
		if s, ok := r.sessions[scope]; ok {
			for _, v := range s.views {
				v.invalidate()
			}
		}
	}
}

// jsSync 实现 router.sync([wa, wb], {mode, source})。
func (r *router) jsSync(args []object.Value) object.Value {
	arr, ok := argOrNil(args).(*object.Array)
	if !ok || len(arr.Elements) < 2 {
		return object.NewTypeError("gx/router sync: 需要至少两个作用域/窗口, 如 sync([wa, wb], {mode:'mirror'})")
	}
	members := make([]string, 0, len(arr.Elements))
	for _, item := range arr.Elements {
		scope, ok := r.scopeNameOf(item)
		if !ok {
			return object.NewTypeError("gx/router sync: 成员必须是窗口句柄或作用域名字符串")
		}
		members = append(members, scope)
	}
	mode := syncModeMirror
	source := ""
	if o, ok := argOrNil(args[1:]).(*object.Object); ok {
		if m := objPropStr(o, "mode"); m != "" {
			switch m {
			case syncModeMirror, syncModeShare, syncModeFollow:
				mode = m
			default:
				return object.NewTypeError("gx/router sync: mode 只支持 mirror / share / follow, 收到 %s", m)
			}
		}
		if sc := objPropStr(o, "source"); sc != "" {
			source = sc
		}
	}
	dst := -1
	for i, g := range r.syncGroups {
		if len(g.members) == len(members) && g.members[0] == members[0] {
			dst = i
			break
		}
	}
	g := &routerSyncGroup{members: members, mode: mode, source: source}
	if dst >= 0 {
		r.syncGroups[dst] = g
	} else {
		r.syncGroups = append(r.syncGroups, g)
	}
	return object.UndefinedSingleton
}

// ===== 注册 gx/router =====

// jsCreateRouter 是 createRouter({routes, …}) / createRouter(routes, opts)。
func jsCreateRouter(args ...object.Value) object.Value {
	var (
		routes object.Value
		opts   *object.Object
	)
	switch v := argOrNil(args).(type) {
	case *object.Array:
		routes = v
		opts, _ = argOrNil(args[1:]).(*object.Object)
	case *object.Object:
		opts = v
		routes = objProp(v, "routes")
	default:
		return object.NewTypeError("createRouter: 需要 {routes:[…]} 或 (routes, opts)")
	}
	if routes == nil {
		return object.NewTypeError("createRouter: 缺少 routes")
	}

	r := &router{
		table:     routeCompile(routes),
		routesRaw: routes,
		sessions:  map[string]*routerSession{},
		backKeys:  true,
		foldDual:  true,
	}
	for _, p := range r.table.pending {
		recordWarn("gx/router: %s", p)
	}
	r.revGet, r.revSet = newRouteSignal()

	if opts != nil {
		if n := objPropStr(opts, "name"); n != "" {
			r.name = n
		}
		if v := objProp(opts, "initial"); v != nil {
			if s, ok := v.(*object.String); ok {
				r.initialPath = s.Value
			} else if o, ok := v.(*object.Object); ok {
				e, err := r.resolveTo(o, nil)
				if err == nil && e != nil {
					// initial 给了对象形态: 记下它的路径 (名字形态也能用)
					r.initialPath = e.fullPath
				} else {
					recordWarn("gx/router: initial 无法解析: %v", err)
				}
			}
		}
		if v, ok := objProp(opts, "backKeys").(*object.Boolean); ok {
			r.backKeys = v.Value
		}
		if v, ok := objProp(opts, "foldable").(*object.Boolean); ok {
			r.foldDual = v.Value
		}
	}
	if r.initialPath == "" {
		r.initialPath = "/"
	}
	defaultRouter = r
	allRoutersList = append(allRoutersList, r)
	return r.jsObject()
}

func init() {
	object.RegisterBuiltinModule("gx/router", func() map[string]object.Value {
		return map[string]object.Value{
			"createRouter":  object.NewBuiltin("createRouter", jsCreateRouter),
			"RouterView":    object.NewBuiltin("RouterView", jsRouterView),
			"RouterLink":    object.NewBuiltin("RouterLink", jsRouterLink),
			"lazy":          object.NewBuiltin("lazy", jsRouterLazy),
			"useRoute":      object.NewBuiltin("useRoute", jsUseRoute),
			"useRouter":     object.NewBuiltin("useRouter", jsUseRouter),
			"useRouteState": object.NewBuiltin("useRouteState", jsUseRouteState),
		}
	})
}

// resetRouterStateForTest 清空模块级状态 (用例之间不许串味: 与窗口注册表、
// gx/screen 的状态同一纪律)。
func resetRouterStateForTest() {
	allRoutersList = nil
	defaultRouter = nil
	pendingRouterViews = nil
	routerRenderStack = nil
}
