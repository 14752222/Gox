package gfx

import (
	"sort"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// ===== gx/router 的匹配层: 路由表编译 + 路径匹配 (纯函数, 不碰 VM) =====
//
// 本文件刻意与 router.go (会话/守卫/导航) 分开, 因为这一层是**纯数据变换**:
// 输入是脚本给的 routes 数组与一条路径字符串, 输出是"哪条记录 + 哪些参数"。
// 分开的好处与 gfx/textarea.go 的 taApplyKey 同一哲学 —— 路由最容易出错的
// 就是匹配规则 (静态段 / :param / 可选段 / 通配 / 嵌套拼接), 而这些规则
// 可以完全不依赖 VM 单测 (见 router_match_test.go)。
//
// ## 支持的路径词汇 (v1 承诺)
//
//	/items              静态段
//	/items/:id          :param 段 (匹配一段非空路径)
//	/items/:id?         可选段 (只能出现在末尾; 缺省时该段整段消失)
//	/files/*            通配 (吞掉剩余全部路径, 参数名 pathMatch)
//	/files/*rest        通配 + 自定义参数名
//	/items/new          与 /items/:id 同时存在时, 静态段优先 (见 routeScore)
//
// 不支持 (刻意划在 v1 之外, 写进文档而不是悄悄降级):
//   - 正则约束 `/items/:id(\\d+)` —— 需要内嵌正则解析器, 而 gx/router 的
//     定位是"桌面应用的页面切换", 不是服务端路由; 需要校验时在 beforeEnter 里做;
//   - 重复参数 / 矩阵参数 / alias —— 每一条都要在 `matched` 链与 buildPath
//     里各维护一份数据, 收益不足;
//   - 大小写不敏感匹配 —— 桌面应用路径由脚本自己生成, 区分大小写与文件系统一致。

// ===== 路径段 =====

// 路径段种类。
const (
	routeSegStatic   = iota // 静态字面量
	routeSegParam           // :name
	routeSegOptional        // :name? (只允许出现在末尾)
	routeSegCatchAll        // * 或 *name
)

// routeSegment 是编译后的一个路径段。
type routeSegment struct {
	kind    int
	literal string // 静态段: 字面量; 参数段: 参数名
}

// ===== 路由记录 =====

// 组件字段的三种状态。**必须区分"未知"与"同步"**: 懒加载的判定方式是
// "调用一次 component, 看返回值是不是 Promise" —— 第一次调用前谁也不知道
// 它是哪一种, 而这个调用本身就是构建, 不能预先试一次 (试出来的副作用是
// 组件体会跑两遍: createSignal 造两份、onMount 登记两次)。
const (
	routeCompUnknown = iota // 还没调用过
	routeCompSync           // 调用后返回元素 → 普通组件
	routeCompLazy           // 调用后返回 Promise → 懒加载
	routeCompMissing        // 记录里没给 component (布局路由: 只有 children 或只做重定向)
)

// routeRecord 是一条编译后的路由记录。
//
// 嵌套路由被**扁平化**: 一条 `/parent/child` 的记录自己持有完整路径与完整
// 段序列, 同时用 parent/children 记住层级。这样匹配只需扫一遍扁平表 (而不是
// 递归下钻), 而"嵌套 RouterView"靠 matched 链的 depth 定位 —— 两边都简单。
type routeRecord struct {
	path     string // 完整路径 (父路径 + 子路径拼接后的结果)
	name     string // 命名路由 (可空)
	segments []routeSegment
	score    int // 匹配优先级 (越大越优先, 见 routeScore)
	order    int // 声明序 (score 相同时的先手)

	component object.Value // 组件函数, 或懒加载器 (返回 Promise 的函数)
	compKind  int
	resolved  object.Value // 懒加载解析出的组件
	loadErr   object.Value // 懒加载失败原因 (Error 对象)
	// loadPromise 是"在飞的那次加载"。多个窗口同时导航到同一页时共用它 ——
	// 模块系统虽有缓存, 但共用同一个 Promise 才能保证 hooks/resolved 只被
	// 应用一次 (见 router_view.go 的 beginLoad)。
	loadPromise *object.Promise
	hooks       map[string]object.Value // 懒加载模块的命名导出 (组件级守卫从这里来)

	meta        object.Value
	redirect    object.Value
	beforeEnter object.Value
	propsVal    object.Value

	keepAlive bool // 离开后保留子树 (回来时状态原样)
	dualPane  bool // 折叠态可以作为独立一栏挂在旁边

	depth    int
	parent   *routeRecord
	children []*routeRecord
}

// routeTable 是编译结果: 扁平记录表 (按匹配优先级排好) + 名字索引。
type routeTable struct {
	records []*routeRecord // 已按 routeScore 降序 (同分按声明序)
	byName  map[string]*routeRecord
	// pending 记录"编译期发现的问题" (缺 path / 重复名字 / 可选段不在末尾),
	// 由调用方决定怎么出声 —— 匹配层不直接写 stderr, 保持纯函数。
	pending []string
}

// ===== 编译 =====

// routeCompile 把脚本给的 routes 数组编译成 routeTable。
//
// 容错口径与仓库其它内置模块一致 (与 select 的 options / view 的指令同款):
// **形状不对的项跳过并记账, 不 panic**。原因很实际: routes 是脚本数据,
// 一个拼错的字段不该让整个应用开不起来 —— 但也不能静默, 所以问题进 pending
// 由上层 (routerCompile) 经 recordWarn 出声。
func routeCompile(routes object.Value) *routeTable {
	t := &routeTable{byName: map[string]*routeRecord{}}
	arr, ok := routes.(*object.Array)
	if !ok {
		t.pending = append(t.pending, "routes 必须是数组")
		return t
	}
	order := 0
	for i, item := range arr.Elements {
		obj, ok := item.(*object.Object)
		if !ok {
			t.pending = append(t.pending, "routes["+strconv.Itoa(i)+"] 不是对象, 已跳过")
			continue
		}
		t.compileInto(obj, "", nil, 0, &order)
	}
	sort.SliceStable(t.records, func(a, b int) bool {
		ra, rb := t.records[a], t.records[b]
		if ra.score != rb.score {
			return ra.score > rb.score
		}
		if len(ra.segments) != len(rb.segments) {
			return len(ra.segments) > len(rb.segments)
		}
		return ra.order < rb.order
	})
	for _, r := range t.records {
		if r.name != "" {
			if _, dup := t.byName[r.name]; dup {
				t.pending = append(t.pending, "路由名重复: "+r.name+" (后一条不再可被 name 解析)")
				continue
			}
			t.byName[r.name] = r
		}
	}
	return t
}

// compileInto 递归编译一条记录 (children 会带着父路径继续下钻)。
func (t *routeTable) compileInto(obj *object.Object, parentPath string, parent *routeRecord, depth int, order *int) {
	rawPath := objPropStr(obj, "path")
	if rawPath == "" {
		// 没有 path: 布局路由 (只有 children) 也要求给 path —— 否则子路由的
		// 完整路径无从拼接, 会让 "/a/b" 这种写法静默失效。
		t.pending = append(t.pending, "路由记录缺少 path, 已跳过")
		return
	}
	full := joinRoutePath(parentPath, rawPath)

	rec := &routeRecord{
		path:      full,
		name:      objPropStr(obj, "name"),
		meta:      objProp(obj, "meta"),
		redirect:  objProp(obj, "redirect"),
		propsVal:  objProp(obj, "props"),
		parent:    parent,
		depth:     depth,
		order:     *order,
		compKind:  routeCompUnknown,
		keepAlive: objPropBool(obj, "keepAlive"),
		hooks:     map[string]object.Value{},
	}
	*order++
	t.compileSegments(rec, full)
	if be := objProp(obj, "beforeEnter"); be != nil {
		rec.beforeEnter = be
	}
	if c := objProp(obj, "component"); c != nil {
		rec.component = c
		// lazy(() => import(...)) 显式标记: 拆成"加载器 + 已知是懒的"。
		// 判定放在编译期 (而不是等视图第一次调用) 是必要的 —— 导航的守卫链
		// 要在提交前就知道要不要等加载 (见 router_view.go 的 lazy 说明)。
		applyLazyMarker(rec)
	} else {
		rec.compKind = routeCompMissing
	}
	// dualPane: 折叠态下能不能和"上一页"并排。默认**能给** (这是折叠屏的常态
	// 诉求: 左列表右详情), 显式 dualPane: false 才退出双栏。
	if objProp(obj, "dualPane") == nil {
		rec.dualPane = true
	} else {
		rec.dualPane = objPropBool(obj, "dualPane")
	}
	// meta.keepAlive / meta.dualPane 也认 (与 vue-router 把这类开关塞 meta 的
	// 习惯对齐; 顶层字段优先)。
	if m, ok := rec.meta.(*object.Object); ok && m != nil {
		if rec.keepAlive == false && objPropBool(m, "keepAlive") {
			rec.keepAlive = true
		}
		if objProp(m, "dualPane") != nil {
			rec.dualPane = objPropBool(m, "dualPane")
		}
	}
	// 只有 path 没有 component/children/redirect 的记录是**纯分组**:
	// 它不该参与匹配 (匹配到它等于匹配到一个空页面), 所以不放进表。
	group := rec.component == nil && rec.redirect == nil && objProp(obj, "children") != nil
	if !group {
		t.records = append(t.records, rec)
	}
	if parent != nil {
		parent.children = append(parent.children, rec)
	}

	if kids, ok := objProp(obj, "children").(*object.Array); ok {
		for _, k := range kids.Elements {
			ko, ok := k.(*object.Object)
			if !ok {
				t.pending = append(t.pending, "children 里出现非对象项, 已跳过")
				continue
			}
			t.compileInto(ko, full, rec, depth+1, order)
		}
	}
}

// compileSegments 把完整路径切成段并算优先级。
func (t *routeTable) compileSegments(rec *routeRecord, path string) {
	raw := splitRoutePath(path)
	segs := make([]routeSegment, 0, len(raw))
	for i, s := range raw {
		switch {
		case s == "*":
			segs = append(segs, routeSegment{kind: routeSegCatchAll, literal: "pathMatch"})
		case strings.HasPrefix(s, "*"):
			segs = append(segs, routeSegment{kind: routeSegCatchAll, literal: s[1:]})
		case strings.HasPrefix(s, ":"):
			name := s[1:]
			optional := strings.HasSuffix(name, "?")
			name = strings.TrimSuffix(name, "?")
			if name == "" {
				name = "param" + strconv.Itoa(i)
			}
			kind := routeSegParam
			if optional {
				kind = routeSegOptional
				if i != len(raw)-1 {
					// 可选段后面还有段: 匹配时无法判断"该不该吃掉这一段"。
					// 明确不支持 (而不是猜), 让写错的人立刻知道。
					t.pending = append(t.pending, "可选段 :"+name+"? 之后还有路径段, 只支持出现在末尾 (按必填处理)")
					kind = routeSegParam
				}
			}
			segs = append(segs, routeSegment{kind: kind, literal: name})
		default:
			segs = append(segs, routeSegment{kind: routeSegStatic, literal: s})
		}
	}
	rec.segments = segs
	rec.score = routeScore(segs)
}

// routeScore 是匹配优先级。规则一句话: **越"具体"的记录越优先**。
//
//	static 4 > param 3 > optional 2 > catchAll 1, 出现通配再整体重罚。
//
// 为什么要打分而不是"按声明序取第一个匹配": 声明序会让
// `[{path:"*"}, {path:"/home"}]` 这种顺序把 /home 永久遮住 (真实写法常见),
// 而 vue-router 的立场是"静态优先于参数优先于通配" —— 对齐主流心智比省事重要。
// 同分时按声明序 (sort 里用 order 兜底), 所以 `[{"/a/:x"},{"/a/:y"}]` 仍是先声明者胜。
func routeScore(segs []routeSegment) int {
	score := 0
	for _, s := range segs {
		switch s.kind {
		case routeSegStatic:
			score += 4
		case routeSegParam:
			score += 3
		case routeSegOptional:
			score += 2
		case routeSegCatchAll:
			score += 1
			score -= 1000 // 通配永远排在所有具体路径之后
		}
	}
	return score
}

// ===== 路径工具 =====

// splitRoutePath 把一条路径切成段 (忽略首尾与重复的 "/")。
//
//	"/"            → []
//	"/items/42"    → ["items", "42"]
//	"/items/42/"   → ["items", "42"]   (尾斜杠宽容)
//	"items"        → ["items"]         (缺前导斜杠宽容)
func splitRoutePath(path string) []string {
	trimmed := strings.Trim(path, "/")
	if trimmed == "" {
		return nil
	}
	parts := strings.Split(trimmed, "/")
	// 去掉空段 ("//a" 这类), 否则匹配会多出一段永不可能命中的空串。
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// joinRoutePath 把父子路径拼成完整路径 (子路径以 "/" 开头则视为绝对路径,
// 与 vue-router 的嵌套路由约定一致)。
func joinRoutePath(parent, child string) string {
	if parent == "" || strings.HasPrefix(child, "/") {
		return normalizeRoutePath(child)
	}
	return normalizeRoutePath(parent + "/" + child)
}

// normalizeRoutePath 规整为"以 / 开头、无尾斜杠"的形式; 根路径恒为 "/"。
func normalizeRoutePath(p string) string {
	segs := splitRoutePath(p)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// routeSplitTarget 把 "/items/42?tab=x#top" 拆成 (path, query, hash)。
func routeSplitTarget(target string) (path, query, hash string) {
	rest := target
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		hash = rest[i+1:]
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		query = rest[i+1:]
		rest = rest[:i]
	}
	return rest, query, hash
}

// routeParseQuery 解析 "a=1&b=%20x" 为 map (不做 URL 解码: 本引擎的路径由
// 脚本自己生成, 不做浏览器那套编码往返; 需要时脚本用 encodeURIComponent)。
func routeParseQuery(q string) map[string]string {
	out := map[string]string{}
	if q == "" {
		return out
	}
	for _, pair := range strings.Split(q, "&") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		out[k] = v
	}
	return out
}

// routeBuildQuery 把 map 拼回查询串 (键按字典序, 输出稳定 ⇒ 可断言)。
func routeBuildQuery(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, "&")
}

// routeJoinFullPath 由 path/query/hash 拼出 fullPath。
func routeJoinFullPath(path, query, hash string) string {
	out := path
	if query != "" {
		out += "?" + query
	}
	if hash != "" {
		out += "#" + hash
	}
	return out
}

// ===== 匹配 =====

// routeFind 在表里找第一条匹配 path 的记录, 返回记录与解析出的参数。
// 表已按优先级排序, 所以"第一条命中"就是"最具体的那条"。
func (t *routeTable) routeFind(path string) (*routeRecord, map[string]string) {
	segs := splitRoutePath(path)
	for _, r := range t.records {
		if params, ok := routeMatchSegments(r.segments, segs); ok {
			return r, params
		}
	}
	return nil, nil
}

// routeMatchSegments 做段对段的匹配 (纯函数, 单测的主力)。
//
// 语义细节:
//   - 参数段的值是**字符串**且不做解码 (与查询串同一口径);
//   - 可选段缺省时**不写进 params** (而不是写空串) —— `:id?` 未给时
//     `params.id === undefined`, 与 vue-router 一致, 便于用 in 判断;
//   - 通配把剩余**全部**段用 "/" 连回去 (空剩余 → 空串), 参数名默认 pathMatch;
//   - 通配之后还有段 → 不可能命中 (它在实现里就把剩余吃光了, 后面必然失配)。
func routeMatchSegments(segs []routeSegment, path []string) (map[string]string, bool) {
	params := map[string]string{}
	i := 0
	for si, seg := range segs {
		switch seg.kind {
		case routeSegStatic:
			if i >= len(path) || path[i] != seg.literal {
				return nil, false
			}
			i++
		case routeSegParam:
			if i >= len(path) {
				return nil, false
			}
			params[seg.literal] = path[i]
			i++
		case routeSegOptional:
			if i < len(path) {
				params[seg.literal] = path[i]
				i++
			}
		case routeSegCatchAll:
			if si != len(segs)-1 {
				return nil, false
			}
			params[seg.literal] = strings.Join(path[i:], "/")
			i = len(path)
		}
	}
	if i != len(path) {
		return nil, false
	}
	return params, true
}

// routeBuildPath 用参数把记录的路径模式还原成具体路径 (resolve({name, params}) 用)。
//
//	记录 "/items/:id" + {id:"42"}        → "/items/42"
//	记录 "/items/:id?" + {}              → "/items"      (可选段整段消失)
//	记录 "/files/*rest" + {rest:"a/b"}   → "/files/a/b"
//
// 返回 ok=false 表示"必填参数缺失" —— 调用方据此报错, 而不是生成一条
// 半截路径再让守卫/界面莫名其妙地表现异常。
func routeBuildPath(rec *routeRecord, params map[string]string) (string, bool) {
	if len(rec.segments) == 0 {
		return "/", true
	}
	parts := make([]string, 0, len(rec.segments))
	for _, seg := range rec.segments {
		switch seg.kind {
		case routeSegStatic:
			parts = append(parts, seg.literal)
		case routeSegParam:
			v, ok := params[seg.literal]
			if !ok || v == "" {
				return "", false
			}
			parts = append(parts, v)
		case routeSegOptional:
			if v, ok := params[seg.literal]; ok && v != "" {
				parts = append(parts, v)
			}
		case routeSegCatchAll:
			if v, ok := params[seg.literal]; ok && v != "" {
				for _, p := range splitRoutePath(v) {
					parts = append(parts, p)
				}
			}
		}
	}
	if len(parts) == 0 {
		return "/", true
	}
	return "/" + strings.Join(parts, "/"), true
}

// routeRecordMatchChain 返回一条记录从根到自己的链 (matched 数组的来源)。
// 嵌套 RouterView 按 depth 取链上第 depth 项。
func routeRecordMatchChain(rec *routeRecord) []*routeRecord {
	n := 0
	for r := rec; r != nil; r = r.parent {
		n++
	}
	out := make([]*routeRecord, n)
	for r := rec; r != nil; r = r.parent {
		n--
		out[n] = r
	}
	return out
}

// ===== 对象读值辅助 =====
//
// 与 view.go 的 viewProp 同款容错口径: 缺失返回 nil, 类型不符当没给。
// 单独写一份而不是复用 viewProp: 那个签名收 *object.Object 且语义是
// "JSX 属性", 这里的读法属于路由表编译, 两者的注释/容错理由不同 (见各自说明),
// 共用一个函数反而会让改一处影响另一处。

func objProp(o *object.Object, name string) object.Value {
	if o == nil {
		return nil
	}
	if v, ok := o.GetProperty(name); ok {
		return v
	}
	return nil
}

func objPropStr(o *object.Object, name string) string {
	if s, ok := objProp(o, name).(*object.String); ok {
		return s.Value
	}
	return ""
}

func objPropBool(o *object.Object, name string) bool {
	if b, ok := objProp(o, name).(*object.Boolean); ok {
		return b.Value
	}
	return false
}
