package gfx

import (
	"fmt"

	"github.com/14752222/Gox/object"
)

// ===== 虚拟化长列表: <scroll vlist itemHeight={N}> =====
//
// 问题: `<scroll><view each={rows}>…</view></scroll>` 对 N 行 = N 个 GuiNode。
// 十万行就是十万棵子树: 首帧要建十万个节点、每帧布局遍历十万个盒子, 内存与
// 耗时都随 N 线性增长 —— 几万行就到不可用 (见 bench-results 的 vlist 数据)。
//
// 解法: **只物化视口内的行**, 上下用两个"撑高垫片"补出总高:
//
//	<scroll vlist itemHeight={28} width={320} height={400}>
//	  <view each={rows} key="id">{(row, i) => <row height={28}>…</row>}</view>
//	</scroll>
//
//	┌───────────────┐ ← 内容区顶
//	│ spacer-top    │  高 = first*itemH          (第 0..first-1 行的位置占位)
//	├───────────────┤
//	│ row#first     │  ↑
//	│ …             │  │ 只有这一段真建了节点 (可见 + buffer)
//	│ row#last-1    │  ↓
//	├───────────────┤
//	│ spacer-bottom │  高 = (N-last)*itemH
//	└───────────────┘ ← 内容总高 = N*itemH ⇒ 滚动条长度/行程与全量版逐像素一致
//
// 于是内存与每帧成本只跟 `视口高 / itemHeight` 成正比, **与 N 无关**。
//
// ## 切口为什么在 each 引擎里而不是在 scroll 上
//
// 窗口化需要"重新调用行的渲染函数"——而渲染函数是 jsViewFor 收到的闭包参数,
// 只有它自己的 effect 拿得到。从 scroll 侧驱动需要把这份闭包再存一份到节点上,
// 等于把引擎状态复制成两份 (且每帧覆盖 each prop 会与 effect 打架)。
// 所以切口放在 **jsViewFor 的 effect 内部**: 求值时先看自己有没有被 vlist
// 容器接管, 有就只对可见子区间调用渲染函数。
//
// 滚动 → 重算的驱动靠 `vlistRev` 版本号 signal (与 router 的 rev 同一套):
// jsViewFor 的 effect 在读窗口时会调一次 vlistRevGet(), 于是**订阅**了滚动;
// 滚动改变窗口时 bump 它, effect 自然醒来重算。
//
// ## 三条硬约束 (不满足就是静默错位, 所以一律出声)
//
//  1. **定高行**: `itemHeight` 必给正数。只有固定行高才能"不测量就知道第 k 行
//     在哪"。不给就退化为全量渲染 + 一条警告。
//  2. **缓冲区**: 上下各多物化 `vlistBuffer` 行 (缺省 2)。滚动是按像素走的,
//     没缓冲会在边界闪一下才补上。`buffer={0}` 合法 (要最省内存时用)。
//  3. **窗口没换行就不重建**: 每个像素位移都重建所有行的话, 虚拟化会比不虚拟化
//     更卡 (这是"虚拟化写歪了"最常见的样子)。判据见 vlistSameWindow。
//
// ## 为什么不自动测量行高
//
// "先按估值排、渲染后测量、回填修正"需要二次布局 + 收敛判断, 且滚动中可见内容
// 会跳 (测出的行高与估值不同 ⇒ 位置整体错)。定高覆盖列表的绝大多数形态
// (表格行 / 聊天条目 / 菜单项); 变高留给将来的 measured 模式, 不做半吊子版本。

const (
	// vlistDefaultBuffer 是可见区间上下各多物化的行数 (防边界闪).
	vlistDefaultBuffer = 2
	// vlistMaxRows 是物化行数的硬上限 (兜底防呆): 参数病态 (itemHeight=1 配
	// 4000 高视口) 时会想物化几千行, 那已经不比全量便宜。截断并出声。
	vlistMaxRows = 512
	// vlistPendingRows 是"容器已知、但视口高还没算出来"那一轮的最大物化行数。
	//
	// 首帧就是这一轮: 列表 effect 在 h() 组装时跑, 那时容器还没布局。取 64 是
	// 权衡 —— 大屏 (1080p 高的滚动区 / 32px 行 ≈ 34 行) 也够覆盖首屏, 而 64 行
	// 的建树成本与"十万行"相差三个数量级。若视口真的有 200 行高, 布局阶段会把
	// 精确窗口写回来, 下一轮补上 (首次显示可能少一屏的下半部分, 一帧后到位)。
	vlistPendingRows = 64
)

// vlistRev 是"滚动窗口变了"的全局版本号。
//
// 为什么用全局而不是每容器一个: jsViewFor 的 effect 在求值当下**还不知道**
// 自己会被哪个容器接管 (父链在挂载完成后才连上), 用全局单例最省事且不会
// 漏订阅 —— 代价是任一 vlist 容器滚动会唤醒所有 vlist 列表的 effect, 而
// 每个 effect 醒来第一件事就是比对窗口 (O(1)), 没变就直接返回。
// 一个页面上的 vlist 容器是少数几个, 这点唤醒成本远低于窗口重建。
//
// **为什么不用 gx/solid 的 createSignal** (2026-10-01 改):
// 早先这里走 newRouteSignal() (与 router 同一套)。那条路要求 gx/solid 已注册,
// 而注册它的只有 stdlib —— **纯 gfx 宿主 (含 gfx 自己的测试与基准) 里它不存在**,
// 于是 vlistRevInit 恒 false、订阅建立不起来、布局阶段的 bump 唤不醒任何人:
// 窗口永远停在首帧那一次的结果上。症状极隐蔽 —— 树上只剩窗口内的十几行 (看着
// 像生效了), 但耗时随行数线性增长, 因为**建树那一步**已经把 N 行全建过一遍。
// 演示脚本因为写了 `import { createSignal } from "gx/solid"` 而自带注册, 所以
// 端到端测试测不出来; 只有基准把它逼了出来。
// 现在改成 gfx 自己维护的订阅表, 不依赖任何内置模块 —— 换后端零改动, 换宿主
// 也不该改变"列表会不会虚拟化"这种内核行为。
var vlistRev struct {
	n   float64
	sub map[*viewForState]struct{}
}

// vlistRevSubscribe 让一个列表状态订阅"窗口变化"。effect 求值期间调用一次即可
// (与 signal 的依赖收集同一时机), 幂等。
func vlistRevSubscribe(st *viewForState) {
	if st == nil {
		return
	}
	if vlistRev.sub == nil {
		vlistRev.sub = map[*viewForState]struct{}{}
	}
	vlistRev.sub[st] = struct{}{}
}

// vlistRevUnsubscribe 在列表销毁时摘掉订阅 (宿主销毁 → 行清理 → 状态不再被读)。
func vlistRevUnsubscribe(st *viewForState) {
	delete(vlistRev.sub, st)
}

// vlistRunRefresh 跑一次列表重算, 并挡住重入。
//
// 重算会重新绑定 vlistWindowFor → 又见 vlistRevBump, 不拦就是无限递归 (而且每次
// 都在建 N 行, 十万行直接退化成秒级 —— 2026-10-01 实测: 物化行数已经对了 13,
// 首帧却从 20ms 涨到 1000ms, 就是这条)。语义上也该拦: 重算读的是 st.win, 窗口在
// 本轮里是稳定的, 第二次 bump 没有信息量。
func vlistRunRefresh(st *viewForState) {
	if st == nil || st.refresh == nil || vlistRevRunning {
		return
	}
	vlistRevRunning = true
	defer func() { vlistRevRunning = false }()
	vlistDiagRefresh()
	st.refresh()
}

// vlistTopRoot 沿 Parent 走到这棵树的根 (没挂上时返回自己)。
func vlistTopRoot(n *GuiNode) *GuiNode {
	if n == nil {
		return nil
	}
	for n.Parent != nil {
		n = n.Parent
	}
	return n
}

// vlistRevBump 抬版本号并**同步**唤醒"同一棵树里"的订阅列表重算。
//
// 与 solid signal 的语义差别: signal 是"标脏 + 由调度器补跑", 而 gfx 侧没有
// 批处理调度器 (setter 同步通知, 见 stdlib/solid.go), 这里直接跑一遍重算,
// 行为与之一致 (布局阶段调用, 重算只写节点属性, 不递归触发布局)。
// 重算失败不静默: 它会经 recordWarn 出声, 与其它属性写回同一条路。
//
// ## 为什么必须按"同一棵树"过滤 (2026-10-01 实测)
//
// 订阅表是包级全局的 (见上), 而列表状态只靠 disposeNode 退订。任何"建了树但没
// 销毁"的宿主都会永远留在表里 —— 之后**每一次** bump 都会去跑它的重算, 而那一刻
// `currentVM` 指向的是**另一个 VM 实例**: 它的 each 闭包会在错误的字节码/常量表
// 里执行, 症状是从完全无关的地方冒出一堆 `TypeError: [...] is not a function`
// 与 `ReferenceError: x is not defined`, 而且每条错误的文案都要把一个十万元素的
// 数组格式化成字符串 (O(N)), 直接把整个测试包拖到超时 (实测: TestViewFor* 跑在
// vlist demo 之前时, demo 从 0.14s 变成 10 分钟超时)。
//
// 过滤判据用**根节点身份**而不是"有没有挂上": 脱链的宿主 `Parent == nil` 会让
// 它自己看起来像个根, 光看 Parent 分不出"独立的小树"与"被摘下来的旧子树"。比
// 根节点指针则天然正确 —— 顺带把多窗口语义也收紧了: A 窗口的滚动不该唤醒 B 窗口
// 的列表 (与 appOfNode 的"标脏要标对窗口"同一条纪律)。
func vlistRevBump(from *GuiNode) {
	if len(vlistRev.sub) == 0 || vlistRevRunning {
		return
	}
	root := vlistTopRoot(from)
	vlistRev.n++
	// 快照后遍历: 重算过程中不改订阅表, 但防将来有人在这里增删。
	subs := make([]*viewForState, 0, len(vlistRev.sub))
	for st := range vlistRev.sub {
		subs = append(subs, st)
	}
	for _, st := range subs {
		if root != nil && vlistTopRoot(st.host) != root {
			continue // 别的窗口 / 别的树 (含已废弃的旧树): 不是它的滚动
		}
		vlistRunRefresh(st)
	}
}

// vlistDiagCollapse 在 vlistCollapsePending 生效时被调用 (仅测试赋值)。
var vlistDiagCollapse = func(*GuiNode, *viewForState, int) {}

// vlistDiagRefresh 统计每帧触发了多少次窗口重算 (仅测试赋值; 生产为 no-op)。
var vlistDiagRefresh = func() {}

// vlistRevRunning 标记"当前正在跑窗口重算", 见 vlistRevBump。
var vlistRevRunning bool

// ===== 布局期的窗口规划 (三遍布局的 ①) =====

// vlistPlan 是"这一遍布局该物化哪些行"的规划结果。
//
// 为什么要先规划再写: 可见行数取决于**视口高**, 而视口高取决于**有没有滚动条**,
// 而滚动条又取决于内容高 —— 内容高在窗口化时是由规划本身给的 (total*itemH)。
// 这条链子是循环的, 拆开的方式是"先按最宽的视口规划 (假设只有竖向滚动条),
// 定下总高后再用真实视口复核一次"。多出来的一遍只在滚动条真的出现时才跑,
// 而它的代价是 O(1) (写窗口 + 唤醒列表 effect), 不是 O(N)。
type vlistPlan struct {
	itemH    int
	buffer   int
	total    int
	offsetY  int
	contentH int // total*itemH (窗口化时内容高是已知量, 不量)
	contentW int
	viewH    int // 规划时的可见高 (扣轨后)
}

// vlistPlanned 报告规划是否可用 (total 为 0 也算可用: 空列表也要按窗口化走,
// 免得退化成"量一屏空内容"再让 offsetY 被错误钳到 0)。
func (p vlistPlan) vlistPlanned() bool { return p.itemH > 0 }

// scrollVlistPlan 做 ①: 按当前 area 规划窗口。
//
// 视口用"只扣右轨"的宽视口 —— 这是普通 scroll 判定轨道的同一套前提
// (scrollAxesWith 先假设只有竖向轨道)。真实视口在 ③ 复核。
func (n *GuiNode) scrollVlistPlan(area Rect) (vlistPlan, bool) {
	return n.scrollVlistPlanAt(area, n.offsetY)
}

func (n *GuiNode) scrollVlistPlanAt(area Rect, offsetY int) (vlistPlan, bool) {
	cfg, ok := vlistConfigOf(n)
	if !ok {
		return vlistPlan{}, false
	}
	host := vlistFindHost(n)
	if host == nil {
		viewWarnOnce("vlist:noEach",
			"<scroll vlist>: 没找到带 each 的列表子节点 (vlist 虚拟化的是 "+
				"<scroll vlist><view each={rows}>…</view></scroll> 这种形状), "+
				"已退化为普通滚动容器")
		return vlistPlan{}, false
	}
	st := host.forState
	if st == nil {
		// 列表引擎还没把状态挂上 (理论上不会: effect 在 h() 里就跑了)。
		// 稳妥起见不接管 —— 下一帧布局会再进来一次。
		return vlistPlan{}, false
	}
	// 列表总量从 forState 取 (viewForUpdate 每轮写入) —— 宿主 slot 的 Props
	// 里没有 each (指令展开时被剥掉了), 拿不到原始列表。
	total := st.total
	contentH := total * cfg.itemH
	vpW, vpH := area.W, area.H
	if contentH > vpH {
		// 竖向轨道已确定出现: 可见宽与可见高都要让出右轨 (scrollViewport 同款)。
		vpW -= scrollTrackW
		if vpW < 0 {
			vpW = 0
		}
		vpH -= scrollTrackW
		if vpH < 0 {
			vpH = 0
		}
	}
	return vlistPlan{
		itemH:    cfg.itemH,
		buffer:   cfg.buffer,
		total:    total,
		offsetY:  offsetY,
		contentH: contentH,
		contentW: vpW,
		viewH:    vpH,
	}, true
}

// scrollViewportFinal 按"已知的总高"算最终视口 (扣轨判据用内容高而不是量出来的
// 内容高 —— 窗口化时量不出来, 量到的是十几行的高度)。
func (n *GuiNode) scrollViewportFinal(areaW, areaH int) Rect {
	w, h := areaW, areaH
	if n.contentH > areaH {
		w -= scrollTrackW
	}
	if n.contentW > w {
		h -= scrollTrackW
	}
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return Rect{X: 0, Y: 0, W: w, H: h}
}

// layoutScrollWindowed 是窗口化容器的 ③ 摆放: 在最终偏移下把物化行摆好。
//
// 与普通路径的区别只有一点: **垫片高度在这里按最终偏移重写**。
// 窗口可能是在上一次（偏移还没钳位 / 滚动条还没定下来时）准备的, 那时算出的
// 上下垫片对不上现在的偏移, 症状是"滚动条位置和内容对不上"或"滚到底还有一屏
// 空白"。垫片是 O(1) 的两个节点, 每次布局重写一次比"精确预测何时该重算"便宜。
func layoutScrollWindowed(n *GuiNode, area Rect, vp Rect) {
	host := vlistFindHost(n)
	if host == nil {
		return
	}
	st := host.forState
	if st == nil || !st.vlistOn || !st.winReady {
		return
	}
	st.win.offsetY = n.offsetY
	st.win.viewH = vp.H
	if top, bottom := vlistSpacers(st.win, st.cfg.itemH); len(st.spacers) >= 2 {
		vlistSetSpacerH(st.spacers[0], top)
		vlistSetSpacerH(st.spacers[1], bottom)
	}
	layoutContentColumn(n, area.X-n.offsetX, area.Y-n.offsetY, vp.W)
}

// ===== 配置解析 =====

// vlistConfig 是一个 scroll 上的 vlist 配置 (解析后)。
type vlistConfig struct {
	itemH  int
	buffer int
}

// vlistPrepare 是窗口化的**驱动入口**: 把规划好的窗口写进列表宿主的状态, 并
// (必要时) 唤醒列表 effect 重算。
//
// 为什么不能交给列表自己的 effect 算: h() 组装内层元素时就跑了列表的 effect,
// 那一刻外层 scroll 还没把它们 wireChild 进来 —— 宿主沿 Parent 链找不到
// scroll。布局发生在整棵树挂好之后, 父链才是完整的 (详见 view.go 里
// vlistWindowFor 的说明)。
//
// 返回值表示"本节点被 vlist 接管了" (布局方据此切到窗口化路径)。
func (a *app) vlistPrepare(n *GuiNode, plan vlistPlan) bool {
	if !plan.vlistPlanned() {
		return false
	}
	host := vlistFindHost(n)
	if host == nil {
		return false
	}
	st := host.forState
	if st == nil {
		return false
	}
	win := vlistCompute(plan.offsetY, plan.viewH, plan.itemH, plan.buffer, plan.total)
	cfg := vlistConfig{itemH: plan.itemH, buffer: plan.buffer}

	changed := !st.winReady || !vlistSameWindow(st.win, win) || st.cfg != cfg
	// 总量变化也要重算: 内容总高与下垫片高度都按 total 算。
	// (不能并进 vlistSameWindow —— 那条判据是给"滚动中每像素"用的热路径, 把
	// total 放进去会让首帧的 0→N 白跑一轮重算。)
	if st.total != plan.total {
		changed = true
	}
	st.cfg = cfg
	st.win = win
	st.winReady = true
	st.vlistOn = true
	if changed {
		// 窗口换行 (或首次): 唤醒**同一棵树里**的列表重算 —— 它会只物化新窗口。
		// 传 n (滚动容器): 用来把唤醒范围收到本窗口内 (别的窗口不该被叫醒)。
		vlistRevBump(n)
	}
	return true
}

// vlistStats 汇总一棵子树里的 vlist 状态 (测试/基准取证用)。
//
// 存在的理由: vlist 的每一环都可能"静默退化" —— 少写 itemHeight、列表没被
// 接管、effect 没订阅上版本号 —— 而它们的外部表现都是同一个"慢", 光看耗时
// 分不出是哪一环。把(开了没 / 总量 / 窗口 / 垫片 / 物化行数)一次打出来,
// 排查时读一行就知道断在哪。
type vlistStats struct {
	on     bool // 列表被 vlist 接管 (vlistOn)
	ready  bool // 窗口已由布局阶段写进来 (winReady)
	total  int  // 数据总行数 (全量)
	rows   int  // 真正建了节点的行数
	first  int
	last   int
	itemH  int
	spacer int // 垫片数 (0..2)
}

func (a *app) vlistStatsOf(n *GuiNode) vlistStats {
	if n == nil {
		return vlistStats{}
	}
	if n.forState != nil {
		st := n.forState
		s := vlistStats{
			on: st.vlistOn, ready: st.winReady, total: st.total,
			first: st.win.first, last: st.win.last, itemH: st.cfg.itemH,
			spacer: len(st.spacers), rows: len(st.rows),
		}
		return s
	}
	for _, c := range n.Children {
		if s := a.vlistStatsOf(c); s.total > 0 || s.on {
			return s
		}
	}
	return vlistStats{}
}

func (s vlistStats) String() string {
	return fmt.Sprintf("vlist{on=%v ready=%v 总量=%d 物化=%d 窗口=[%d,%d) itemH=%d 垫片=%d}",
		s.on, s.ready, s.total, s.rows, s.first, s.last, s.itemH, s.spacer)
}

// vlistFindHost 在 scroll 子树里找承载 each 的宿主 (那个透明 slot)。
//
// 判据是 `forState != nil` 而**不是** "Props 里有 each": 指令展开时
// expandElementDirective 会把 each 从元素 props 里剥掉 (只作为参数传给
// jsViewFor), 宿主 slot 的 Props 是空的 —— 靠 Props 找永远找不到 (2026-10-01
// 实测: 十万行全量物化, 报"没找到 each 子节点")。forState 是 jsViewFor 建的
// 跨轮状态, 挂在宿主上, 这是唯一可靠的标记。
//
// 只找**第一个** (同层级多列表见 vlistWarnMultiEach)。往下逐层找是必要的:
// `<scroll vlist><column><view each=…>` 里列表外面还包了一层布局盒子。
// 但不越过内层 scroll —— 内层滚动区的列表归内层管。
func vlistFindHost(n *GuiNode) *GuiNode {
	var found *GuiNode
	count := 0
	var walk func(p *GuiNode, depth int)
	walk = func(p *GuiNode, depth int) {
		if p == nil || depth > 8 {
			return // 深度上限: 防病态树型把布局拖慢
		}
		for _, c := range p.Children {
			if c.Tag == "scroll" {
				continue // 内层滚动区自理
			}
			if c.forState != nil {
				count++
				if found == nil {
					found = c
				}
				continue // 列表宿主的子树就是各行, 不必再往下找
			}
			walk(c, depth+1)
		}
	}
	walk(n, 0)
	if count > 1 {
		viewWarnOnce("vlist:multiEach",
			"<scroll vlist>: 找到 %d 个 each 列表, 只窗口化第一个"+
				" (多个列表要一起虚拟化请合成一个 each)", count)
	}
	return found
}

// vlistHostOf 从任意节点起找"承载 each 的宿主"(给测试用: 挂载后按标记找)。
func vlistHostOf(n *GuiNode) *GuiNode {
	if n == nil {
		return nil
	}
	if n.forState != nil {
		return n
	}
	for _, c := range n.Children {
		if f := vlistHostOf(c); f != nil {
			return f
		}
	}
	return nil
}

// vlistContainerFor 找"承载 n 的那个 vlist 容器" (两路查找, 从便宜到贵)。
// 调用方通常在首轮拿到结果后缓存到 st.vlistBox (见 viewForState)。
func vlistContainerFor(n *GuiNode) *GuiNode {
	if p, _, ok := vlistFindUp(n); ok {
		return p
	}
	if app := appOfNodeOrBuilding(n); app != nil && app.root != nil {
		if p, _, ok := vlistFindInTree(app.root, n, 0); ok {
			return p
		}
	}
	return nil
}

// vlistCollapsePending 是**建树期的收口**: scroll 的节点与子节点都就位之后, 给它
// 子树里的列表定下窗口并物化。
//
// 为什么必须有这一步: JSX 的 `h("scroll", …, h("view", {each}, fn))` 里内层实参
// 先求值, 列表的第一轮 effect 跑在 scroll 节点存在**之前** —— 那时它查不到容器。
// 列表侧的做法是"先一行都不建" (见 view.go 的 vlistInitialRows 与 render.go 的
// vlistAssembly); 到这里父链已经通了, 可以正确定位容器并把窗口写进去, 于是**第一次
// 物化就只建可见区间** —— 首帧不再为 N 行付建树成本 (十万行 ~2s → 十几行)。
//
// 返回值只给测试/诊断用 (报告"有没有认领")。
func vlistCollapsePending(scroll *GuiNode) bool {
	cfg, ok := vlistConfigOf(scroll)
	if !ok {
		return false
	}
	host := vlistFindHost(scroll)
	if host == nil || host.forState == nil {
		return false
	}
	st := host.forState
	if st.refresh == nil {
		return false // effect 还没跑过 (理论上不会)
	}
	// 已经按精确窗口建过 (滚动 / 列表重算): 不动。
	if st.winReady {
		return false
	}
	// 按"待物化上限"给一个临时窗口。此时还不知道视口高 (没布局), 但**绝不能
	// 退化成全量** —— 首帧的 N 行建树正是虚拟化唯一没省掉的那块成本。
	last := vlistPendingRows
	if last > st.total {
		last = st.total
	}
	total := st.total
	st.win = vlistWindow{first: 0, last: last, total: total, contentH: total * cfg.itemH}
	st.cfg = cfg
	st.vlistOn = true
	st.vlistBox = scroll
	// 从待定名单里摘掉 (已认领)。
	vlistAssemblyClaim(st)
	vlistDiagCollapse(scroll, st, last)
	// 直接调重算而不是 vlistRevBump(): 此刻只需要物化这一个列表, 而 bump 会唤醒
	// 所有订阅者 —— 那些是别的容器/别的列表, 与本次建树无关。
	vlistRunRefresh(st)
	return true
}

// vlistAssemblyClaim 把状态从"待定"名单里摘掉 (认领或补齐时都调)。
func vlistAssemblyClaim(st *viewForState) {
	st.pendingVlist = false
	for i, p := range vlistAssembly.pending {
		if p == st {
			vlistAssembly.pending = append(vlistAssembly.pending[:i], vlistAssembly.pending[i+1:]...)
			return
		}
	}
}

// vlistFind 从节点起沿**祖先链**找最近的 vlist 容器 (供列表侧使用)。
//
// 沿祖先链找而不是只看直接父节点: 列表宿主外面常常还有一层布局盒子
// (`<scroll vlist><column><view each={rows}>…`), 只认直接父节点会静默不生效。
// 遇到**内层 scroll** 就停: 嵌套滚动区里的列表归内层管, 外层窗口化它会错位。
func vlistFind(n *GuiNode) (*GuiNode, vlistConfig, bool) {
	// 先在父链上常规查找。首轮 effect (h() 组装期) 父链还没接上, 这条会落空 ——
	// 那是**已知且已处理**的情形: h(scroll) 在子节点接好后会调
	// vlistCollapsePending 回头补上窗口 (见 node.go)。
	if p, cfg, ok := vlistFindUp(n); ok {
		return p, cfg, true
	}
	// 父链没接上时, 从窗口的根向下找承载本节点的 vlist 容器 (后续轮次的路)。
	if app := appOfNodeOrBuilding(n); app != nil && app.root != nil {
		if p, cfg, ok := vlistFindInTree(app.root, n, 0); ok {
			return p, cfg, true
		}
	}
	// 前面都落空但之前认过容器: 用它 (树搜索在建树期可能早于登记, 顺序不确定;
	// 认过一次就不该因为时序抖动而"这一轮突然不是 vlist 列表了")。
	if st := n.forState; st != nil && st.vlistBox != nil {
		if cfg, ok := vlistConfigOf(st.vlistBox); ok {
			return st.vlistBox, cfg, true
		}
	}
	return nil, vlistConfig{}, false
}

// vlistFindUp 是常规的"沿祖先链找 vlist 容器"。
func vlistFindUp(n *GuiNode) (*GuiNode, vlistConfig, bool) {
	for p := n.Parent; p != nil; p = p.Parent {
		if p.Tag == "scroll" {
			cfg, ok := vlistConfigOf(p)
			if ok {
				return p, cfg, true
			}
			return nil, vlistConfig{}, false
		}
		// 弹层 / 逃逸子树另起一层坐标, 不当它是"同一个滚动区里的行"
		if p.isOverlay() {
			return nil, vlistConfig{}, false
		}
	}
	return nil, vlistConfig{}, false
}

// vlistFindInTree 在 root 子树里找"包含 target 的那个 vlist scroll 容器"。
//
// 做法: 自顶向下走, 每遇到一个带 vlist 配置的 scroll 就在它的子树里找 target;
// 找到就返回它。找不到 (target 不在任何 vlist 容器里) 返回 ok=false, 列表照常
// 走全量路径 —— 与"没写 vlist"完全同一条路, 不会把普通列表误窗口化。
func vlistFindInTree(root, target *GuiNode, depth int) (*GuiNode, vlistConfig, bool) {
	if root == nil || depth > 24 {
		return nil, vlistConfig{}, false
	}
	if root.Tag == "scroll" {
		if cfg, ok := vlistConfigOf(root); ok && nodeInTree(root, target) {
			return root, cfg, true
		}
	}
	for _, c := range root.Children {
		if p, cfg, ok := vlistFindInTree(c, target, depth+1); ok {
			return p, cfg, true
		}
	}
	return nil, vlistConfig{}, false
}

// vlistConfigOf 解析 scroll 节点上的 vlist 配置 (没开 vlist 则 ok=false)。
//
// 判据都要求**显式声明**: 没写 vlist prop 直接不进入这里, 所以下面每条"不行"
// 都值得出声 —— 用户写了 vlist 就是想要窗口化, 静默退化成全量渲染是最坏结果
// (他以为已经优化了, 而性能数字会让他去别处找原因)。
func vlistConfigOf(n *GuiNode) (vlistConfig, bool) {
	if !vlistSwitchOn(n) {
		return vlistConfig{}, false
	}
	itemH := 0
	if h, ok := n.PropNum("itemHeight"); ok && h > 0 {
		itemH = int(h)
	}
	if itemH <= 0 {
		viewWarnOnce("vlist:itemHeight",
			"<scroll vlist>: 缺少 itemHeight (正数, 每行的固定高) —— 窗口化需要"+
				"\"不测量就知道第 k 行在哪\", 已退化为全量渲染")
		return vlistConfig{}, false
	}
	buf := vlistDefaultBuffer
	if b, ok := n.PropNum("buffer"); ok && b >= 0 {
		buf = int(b)
	}
	return vlistConfig{itemH: itemH, buffer: buf}, true
}

// vlistSwitchOn 读 vlist 开关。
//
// 必须**同时认布尔与数字**: 脚本里最自然的写法是 `vlist` / `vlist={true}`
// (JSX 无值属性 = true), 而 PropNum 只认数字 —— 只看 PropNum 会让最常用的
// 那个写法静默不生效 (2026-10-01 实测踩到: cfg ok=false, 十万行全量物化)。
// 数字形态留给"开关 + 参数"合一的场景 (vlist={1} 与 vlist={0} 等价于开/关)。
func vlistSwitchOn(n *GuiNode) bool {
	v, ok := n.Props["vlist"]
	if !ok || v == nil {
		return false
	}
	return vlistSwitchTruthy(v)
}

func vlistSwitchTruthy(v object.Value) bool {
	switch x := v.(type) {
	case *object.Boolean:
		return x.Value
	case *object.Number:
		return x.Value != 0
	}
	// 函数值 (响应式 prop) 在这里不做求值: vlist 是结构开关而不是数据,
	// 写成函数说明写的人想动态开关 —— 那需要重建整棵子树, 不在本期范围。
	return false
}

// ===== 窗口几何 (纯函数: 单独测) =====

// vlistWindow 描述一次窗口化的物化区间 (半开区间 [first, last))。
//
// offsetY / viewH 是**算它时的输入**: 垫片高度要在最终偏移下重算 (见
// layoutScrollWindowed), 而那时不能回头去问容器要偏移 (容器可能已经被钳位,
// 但垫片是随窗口一起定下来的), 所以把它们随窗口一起存住。
type vlistWindow struct {
	first, last int
	total       int
	contentH    int
	offsetY     int
	viewH       int
}

// vlistSameWindow 两个窗口是否需要重建子树: 物化区间一样就不用动。
// **热路径判据**: 滚动是逐像素的, 大多数帧窗口其实没变行, 这里返回 true
// 就跳过了整轮子树重建 —— 少这一条, 虚拟化会比不虚拟化更卡。
//
// **不含 total**: 总量是数据侧的事, 由调用方单独比较 (见 vlistPrepare)。
// 把它放进来会让首帧必然"变了" —— 布局前 total 是 0、布局后是 N, 于是白跑
// 一轮全量重算, 而那一轮在 N=10万时是几百毫秒 (2026-10-01 实测)。
func vlistSameWindow(a, b vlistWindow) bool {
	return a.first == b.first && a.last == b.last
}

// vlistCompute 按当前偏移与视口算要物化的区间。
//
// offsetY < 0 / viewH 与 itemH 非正 / total 非正 这些边界都在这里收口,
// 让调用方 (effect 内) 不需要写任何防御分支。
func vlistCompute(offsetY, viewH, itemH, buffer, total int) vlistWindow {
	w := vlistWindow{total: total, offsetY: offsetY, viewH: viewH}
	if itemH <= 0 || total <= 0 {
		return w
	}
	w.contentH = total * itemH
	if offsetY < 0 {
		offsetY = 0
	}
	if viewH < 0 {
		viewH = 0
	}
	// 可见行: offsetY 落在第 offsetY/itemH 行; 末行 = 底边**上一像素**所在行
	// (底边本身是下一屏的第一个像素, 不算可见 —— 用 (bottom-1)/itemH 而不是
	// ceil(bottom/itemH), 后者在底边正好落在行边界时会把多余的一行算进来)。
	first := offsetY/itemH - buffer
	last := 0
	if bottom := offsetY + viewH; bottom > 0 {
		last = (bottom-1)/itemH + 1
	}
	last += buffer
	if first < 0 {
		first = 0
	}
	if last > total {
		last = total
	}
	if last < first {
		last = first
	}
	// 病态参数兜底: 截断到 vlistMaxRows, 以视口中心为基准 (保证可见区还在)
	if last-first > vlistMaxRows {
		center := offsetY / itemH
		first = center - vlistMaxRows/2
		if first < 0 {
			first = 0
		}
		last = first + vlistMaxRows
		if last > total {
			last = total
			first = last - vlistMaxRows
			if first < 0 {
				first = 0
			}
		}
	}
	w.first, w.last = first, last
	return w
}

// vlistSpacers 由窗口算上下垫片高度。返回 (top, bottom)。
func vlistSpacers(w vlistWindow, itemH int) (int, int) {
	if itemH <= 0 {
		return 0, 0
	}
	top := w.first * itemH
	bottom := (w.total - w.last) * itemH
	if bottom < 0 {
		bottom = 0
	}
	return top, bottom
}
