package gfx

import (
	"strconv"
	"sync"

	"github.com/14752222/Gox/object"
)

// ===== gx/view: 列表循环与条件渲染 (each / show 指令 + Switch 组件) =====
//
// 目标: 让"循环"和"条件"在 JSX 里写出来就是**声明**, 而不是一片手写的
// map / 三元 / 短路表达式, 并且把"这一轮该重建哪几行"交给内核判断。
//
// 2026-09-20 起它们是**元素级指令** (写法与 model 同层, 展开见 directive.go):
//
//	<view each={rows} key="id" fallback={<text>暂无数据</text>}>
//	  {(row, i) => <row gap={6}><text>{`${i + 1}. ${row.title}`}</text></row>}
//	</view>
//
//	<view show={user} fallback={<text>请先登录</text>}>
//	  <column gap={4}><text>{() => user().name}</text></column>
//	</view>
//
//	// 多分支仍是组件 (没有对应的元素语义), 需要 import
//	import { Switch, Match } from "gx/view";
//	<Switch>
//	  <Match when={() => phase() === "loading"}><text>加载中…</text></Match>
//	  <Match when={() => phase() === "error"}><text>出错了</text></Match>
//	  <Match when={() => true}><text>就绪</text></Match>
//	</Switch>
//
// 三个短写法先记住, 语义与展开式完全一致 (理由见下面两节):
//
//	each={rows}   signal 本身就是取值函数 ⇒ 不用再包一层 () => rows()
//	show={open}   同上 (Switch 里 Match 的 when= 同理)
//	key="id"      等价于 key={(r) => r.id}
//
// 与 Vue 的对应关系 (以及一处刻意的差异):
//
//	each         ≈ v-for (带 :key)          列表循环 + 复用
//	show         ≈ v-show / <keep-alive>    条件显隐 (隐藏 = 摘出布局流, 子树保活)
//	Switch/Match ≈ v-if / v-else-if / v-else   多分支 (仍是组件)
//
// 指令写在哪个元素上, 就重复 / 显隐哪个元素: `<view each={rows}>` 的 view 是布局
// 透明容器 (Fragment), 与旧的 <For> 逐像素一致; `<row each={rows}>` 则每项一个盒子。
//
// Show/Switch 刻意**不做** v-if 那种"隐藏即销毁": 本引擎的静态子树一旦被
// dispose, 它的响应式接线 (reactiveProps) 就永久断了 —— 重新挂回去也只是一棵
// 死树 (里面的 {() => count()} 再也不更新)。这不是舍不得销毁, 是销毁之后
// **无法复活**。所以显隐做成 keep-alive: 隐藏时只把分支从 Children 里摘出去
// (布局/绘制/命中都看不见它), 子树保持挂载、内容继续跟着 signal 走, 再显示
// 时瞬间切回、状态原样。想要 v-if 那种"每次显示都全新构建"的语义, 用函数
// 子节点写:
//
//	{() => cond() ? <column><input .../></column> : null}
//
// 元素在函数体里新建 ⇒ 每次求值都是新节点 (这是一直以来就有的写法, For 的行
// 渲染函数也是同一回事)。
//
// ## each / show 收**取值函数** —— 最简写法是直接传 signal
//
// 两个指令收到的都是**取值函数** (`() => rows()`), 不是快照值。JSX 的属性
// 表达式在 h()/组件调用的当场求值, 写 `each={rows()}` 只会把列表的第一份快照交给
// 内核, 之后 signal 再变也不会重渲染 —— 这是本运行时最常见的静默失效陷阱
// (受控 input 的 value 必须传函数是同一条纪律)。
//
// 而 **signal 本身就是一个函数**, 所以最简写法是直接传它:
//
//	<view each={rows} key="id">…</view>
//	<view show={open}>…</view>
//	<text>{draft}</text>          (函数子节点同理: 响应式子节点就是"每次求值")
//
// 包一层的写法仍然合法, 需要派生/过滤时用它: each={() => rows().filter(ok)}。
//
// 例外: 传数组字面量 / 数字字面量是**合法的静态列表** (渲染一次, 不再变化),
// 拿不准就一律传 signal 或函数。
//
// 写错时内核**会出声**: 合法形态之外的值 (each 收到字符串/对象, show 收到字符串,
// 尤其 show={open()} 这种"忘了括号"的静态布尔) 都记一条 stderr 警告并去重
// (viewCheckEach / viewCheckWhen, 见文件末尾的"误用出声")。语义照旧降级 ——
// 改不了已经写下的表达式, 至少让"它冻在第一帧"这件事有据可查。
//
// ## key: 取值函数或字段名简写
//
//	<For each={rows} key="id">                      等价于 key={(r) => r.id}
//	<For each={rows} key={(r) => r.kind + r.id}>    需要拼 key 时用取值函数
//
// 字段名取不到 (项不是对象 / 没有该字段) 时退化为位置键 —— 与不给 key 同义;
// 给了别的类型 (数字 / 对象) 会警告并同样退化 (从前它只是静默地退化为按位置匹配)。
// 注意 key 函数收到的是 (item, i), 与渲染函数同一个签名。
//
// ## 宿主是 slot: 不凭空多出一个盒子
//
// For / Show / Switch 的返回值是一个 slot 节点 —— 与响应式子节点
// (wireReactiveChild) 同一个占位约定: slot 对布局透明 (单子时尺寸完全跟随
// 子节点), 多子 (列表) 时按父容器方向堆叠 —— 放进 column 就竖排, 放进 row
// 就横排, gap 缺省跟随父容器。所以 `<For>` 自己不出现在画面上, 它只是列表项
// 的容器, 布局结果与"把每一项直接写在父元素里"一致。
//
// 每个列表项再各自挂在一个**条目宿主 slot** 上, 好处是"一行的生命周期 =
// 一棵子树的生命周期": 该行被重建或删除时, 行内登记的 onCleanup 与 effect
// 随 disposeNode 一起收尾, 不需要额外的簿记。
//
// ## key 与复用 (v1 的 diff 语义)
//
//   - 给了 key: 按 key 配对。**同 key + 同 item 引用 + 同 index** 的行原样
//     复用 (节点指针不变) —— 行内的 signal 状态、滚动位置、输入焦点全都
//     保住, 也不会白重渲染一遍;
//   - 没给 key (或 key 返回 null/undefined): 按位置配对, 与 Vue 的"无 key
//     就地复用"同义 —— 下标 i 上的项还是同一个引用就不动它;
//   - 配上了 key 但 item 引用变了 / 下标变了: 只就地重渲染**那一行**;
//   - 旧 key 消失: disposeNode 销毁该行子树 (含 onCleanup)。
//
// 于是"追加一项 / 改一行 / 尾部删除"的代价都是 O(真正变化的那几行), 而不是
// 整表重建 —— 这是它相对"函数子节点里 map 一遍" (`{() => rows().map(...)}`,
// 每次变化把整表拆掉重建) 的实际差别。
//
// 比较用**引用同一性 (===)**: `rows()` 若每次都 map 出新对象, 每行都会被判
// 为"变了"而重渲染 —— 与 Solid 的 For 同一口径。key 相同、引用相同才是
// 免重渲染的充分条件。
//
// ## 为什么"下标变了"也算变了 (以及 stable)
//
// 本引擎的 JSX 子节点是**求值一次**的静态内容 (不像 Solid 那样把表达式包成
// 响应式槽), 所以 `{(row, i) => <text>{`${i + 1}. ${row.title}`}</text>}` 里的
// 下标在挂载那一刻就被烤进文本里了。于是"这一行换了位置"就等于"它的静态内容
// 过期了", 必须重渲染 —— 否则序号会静默错位 (Vue 靠 patch、Solid 靠响应式
// 表达式, 两者都不需要重建, 本引擎两者都没有)。
//
// 重排/中间删除会移动后续所有行的下标, 按上面的规则它们都会重建。若你的行
// **不显示位置** (或者位置由行内自己的响应式内容表达), 加一个 `stable` 就
// 把下标从复用判定里摘出去:
//
//	<For each={() => rows()} key={(r) => r.id} stable>{(row) => <RowCard row={row} />}</For>
//
// stable 模式下: 只认 key + 引用, 重排/中间删除都不重建行 (行内的输入框、
// 滚动位置、局部 signal 全留住); **代价**是下标参数停在挂载时的值, 拿它拼
// 静态文本会错——这条边界由使用者负责遵守, 所以它是显式开关而不是默认。
//
// ## v1 边界 (文档即承诺)
//
//   - For 的行删除即销毁 (行是数据, 数据没了生命周期就该结束, 内存有界);
//     Show/Switch 的分支隐藏只摘出布局流 (分支是结构, 保活见上), 两者语义
//     不同是刻意的。长期只增不减地开关 Show 会一直留着旧分支的内存;
//   - 不做 key 的"移动"动画, 不做虚拟化长列表 (万行列表仍需脚本侧分页);
//   - For 的宿主是 slot, 因此**不参与容器级 wrap** (layout.go 的折行只认
//     row 的常规流子节点)。需要折行标签流时把 <For> 放进普通 row, 或改写成
//     函数子节点里的 row wrap;
//   - Match 只在 Switch 里有意义, 直接挂到别处会渲染成一行 "[view Match]"
//     文本 (误用可见, 不静默);
//   - 写作校验**只出声、不改语义** (viewCheckEach / viewCheckWhen / viewCheckKey /
//     viewCheckStable): 非法形态的降级行为与从前逐字一致 (空列表 / 恒真恒假 /
//     按位置匹配 / stable 当 false), 只是多一条去重警告。要升级成"直接报错"
//     得等一次 breaking change 窗口。
//
// 2026-09-20 起: 控制流从"包装组件"改成**元素级指令** (each / show), 与 model
// 同一层 —— 都在 h() 里展开 (见 directive.go)。本文件里保留的是两个**引擎**:
// jsViewFor (列表) 与 jsViewShow (条件显隐), 指令层只做"指令键 → 引擎 prop"的
// 翻译 + 元素副本的构造。Switch / Match 仍是组件 (多分支没有对应的元素语义)。
func init() {
	object.RegisterBuiltinModule("gx/view", func() map[string]object.Value {
		return map[string]object.Value{
			"Switch": object.NewBuiltin("Switch", jsViewSwitch),
			"Match":  object.NewBuiltin("Match", jsViewMatch),
		}
	})
}

// ===== JSX 参数辅助 =====

// viewPropsArg 取 JSX 调用里的 props 参数 (省略或传 null 时为 nil)。
// 组件标签降级成直接调用后, JSX 没有属性时第一个参数就是 null。
func viewPropsArg(args []object.Value) *object.Object {
	if len(args) > 0 {
		if o, ok := args[0].(*object.Object); ok {
			return o
		}
	}
	return nil
}

// viewProp 读一个属性 (缺失返回 nil, 调用方按 "没传" 处理)。
func viewProp(props *object.Object, name string) object.Value {
	if props == nil {
		return nil
	}
	if desc, ok := props.Properties[name]; ok {
		return desc.Value
	}
	return nil
}

// viewResolve 求值一个"取值器": 函数值当场调用 (响应式取值), 其余原样返回。
func viewResolve(v object.Value) object.Value {
	if v == nil {
		return nil
	}
	if object.IsCallable(v) {
		return viewCall(v)
	}
	return v
}

// viewCall 调脚本函数并收走回调异常 (不吞: 走 recordWarn 进 gx/dev 的警告
// 缓冲, 与内核其它回调点一致)。
func viewCall(fn object.Value, args ...object.Value) object.Value {
	res := object.CallFunction(fn, nil, args...)
	if err := takeCallbackErr(); err != nil {
		recordWarn("gx/view: 回调抛错: %v", err)
	}
	return res
}

// viewTruthy 把值当条件读 (null/undefined 视为假)。
func viewTruthy(v object.Value) bool {
	if v == nil {
		return false
	}
	return v.IsTruthy()
}

// viewNewSlot 建一个宿主 slot。Tag "slot" 是内核的透明占位标签 (JS 侧看不到,
// 也不在 knownTags 里), 与 wireReactiveChild 用的是同一个。
func viewNewSlot() *GuiNode {
	return &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
}

// ===== 分支 (Show / Switch 共用): keep-alive 的显隐单元 =====
//
// 一个分支 = 一个 holder slot + 一份**尚未构建**的原始子节点清单。分支第一次
// 被激活时才构建 (懒构建: 从没显示过的分支不会有任何 effect 在跑), 之后一直
// 保活 —— 隐藏只是把 holder 从宿主的 Children 里摘出去 (Parent 断链), 布局/
// 绘制/命中都看不见它, 但它自己的响应式接线还在, 内容继续跟着 signal 走。
//
// 构建走 wireChild (而不是 mountValue), 于是分支里两种写法都得到正确处理:
//   - 静态子元素 → 直接挂上, 靠它自己的 reactiveProps 保持内容新鲜;
//   - 函数子节点 `{() => <text>{count()}</text>}` → 交给 wireReactiveChild,
//     依赖变化时只重建这一支 (与散写时完全同一套语义)。
type viewBranch struct {
	holder *GuiNode
	kids   []object.Value
	built  bool
}

func viewBranchNew(kids []object.Value) *viewBranch {
	return &viewBranch{holder: viewNewSlot(), kids: kids}
}

// viewBranchKids 把一个 prop 值包成子节点清单 (缺失的 prop 不产生子节点 ——
// 否则会渲染出一行 "undefined" 文本)。
func viewBranchKids(v object.Value) []object.Value {
	if v == nil {
		return nil
	}
	return []object.Value{v}
}

// build 构建分支内容 (只构建一次, 幂等)。调用前应已 attach: 构建期建立的
// effect 会立刻求值并标脏, 那时 Parent 链已经接上, 脏标记才落在正确的窗口上;
// onMount 也因此在"子树已挂上"之后才跑 (与 wireReactiveChild 同一顺序约定)。
func (b *viewBranch) build() {
	if b.built {
		return
	}
	b.built = true
	sc := object.PushWiringScope()
	for _, k := range b.kids {
		b.holder.wireChild(k)
	}
	b.holder.cleanups = append(b.holder.cleanups, sc.Cleanups...)
	object.PopWiringScope()
	for _, fn := range sc.Mounts {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/view onMount error: %v", err)
		}
	}
}

// attach 切换宿主当前的布局子节点: 旧的断链 (保活但不出现在布局流里), 新的接上。
func (b *viewBranch) attach(host *GuiNode, prev *viewBranch) {
	if prev != nil {
		prev.holder.Parent = nil
	}
	b.holder.Parent = host
	host.Children = []*GuiNode{b.holder}
}

// viewDetachCleanup 把"当前没挂上的分支"的销毁挂到宿主的清理链上。
//
// 为什么需要: 隐藏的分支不在 host.Children 里, 于是 disposeNode(host) 的递归
// 走不到它 —— 它的 effect 与子树的 onCleanup 会永远活着 (保活不等于永久泄漏)。
// 宿主的 cleanups 本来就是"该节点销毁时执行的回调表", 在这里补收即可。
// active 传函数而不是快照: 要到销毁那一刻才知道谁是"没挂上的那个"。
func viewDetachCleanup(host *GuiNode, active func() *viewBranch, branches []*viewBranch) {
	host.cleanups = append(host.cleanups, object.NewBuiltin("view-detach-cleanup",
		func(args ...object.Value) object.Value {
			cur := active()
			for _, b := range branches {
				if b != nil && b != cur {
					disposeNode(b.holder)
				}
			}
			return object.UndefinedSingleton
		}))
}

// viewSame 判断两个值是否"同一个" —— JS 的 === 语义, 用于判断列表项是否
// 真的换掉了 (换了才重渲染那一行)。
//
// 不借 vm 里的 strictEquals: 它是包内私有, 而这里只需"基本类型比值得、
// 引用类型比指针"。**引用类型比指针**意味着 rows() 每次 map 出新对象时每行
// 都会被判为变了 (与 Solid 的 For 同口径)。
func viewSame(a, b object.Value) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if a.Type() != b.Type() {
		return false
	}
	switch x := a.(type) {
	case *object.Number:
		y, ok := b.(*object.Number)
		return ok && x.Value == y.Value
	case *object.String:
		y, ok := b.(*object.String)
		return ok && x.Value == y.Value
	case *object.Boolean:
		y, ok := b.(*object.Boolean)
		return ok && x.Value == y.Value
	case *object.Null, *object.Undefined:
		return true // 类型已经相等, 两者都是同一个空值
	}
	return a == b // 引用类型: 指针同一性
}

// ===== For: 列表循环渲染 =====

// viewRow 是一个已挂载的列表行: 它的宿主 slot + 配对信息 (key/item/index)。
type viewRow struct {
	key   string
	item  object.Value
	index int
	host  *GuiNode
}

// viewForState 是 For 的跨轮次状态。做成结构体而不是一串闭包变量: 每次重算
// 都要读写这几项, 传一个 *viewForState 比五个返回值清楚得多。
type viewForState struct {
	rows            []*viewRow
	fallback        *viewBranch // 空列表 fallback (keep-alive: 只构建一次, 只在空列表时挂上)
	showingFallback bool
	warnedDupKey    bool
}

// jsViewFor 是 <For each={...} key={...} fallback={...}>{(item, i) => ...}</For>。
func jsViewFor(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	each := viewProp(props, "each")
	keyFn := viewProp(props, "key")
	fallback := viewProp(props, "fallback")
	// stable: 行只认 key + 引用, 位置变化不重渲染 (下标参数停在挂载时的值)。
	// 语义与代价见文件头 "为什么下标变了也算变了"。只认字面量布尔 ——
	// 传函数/表达式会被类型断言吃成 false, 所以这里出声 (见 viewCheckStable)。
	stableProp := viewProp(props, "stable")
	viewCheckStable(stableProp)
	stable, _ := stableProp.(*object.Boolean)
	stableRows := stable != nil && stable.Value

	// 取列表来源: each 收取值函数 (signal 本身也行) / 数组字面量 / 数字。
	viewCheckEach(each)
	// 取 key: 取值函数, 或者字段名简写 key="id" (等价于 key={(r) => r.id})。
	if s, ok := keyFn.(*object.String); ok {
		keyFn = viewKeyField(s.Value)
	} else {
		viewCheckKey(keyFn)
	}

	// children: 渲染函数。JSX 里写 {(item, i) => ...}; 多个子节点时只认第一个
	// 函数 (其余是排版空白, 归一化后不会产生值)。
	var render object.Value
	for _, c := range args[1:] {
		if object.IsCallable(c) {
			render = c
			break
		}
	}
	if each == nil {
		recordWarn("gx/view For: 缺少 each (列表来源), 渲染为空列表")
	}
	if render == nil && fallback == nil {
		recordWarn("gx/view For: 既没有渲染函数 ({(item, i) => ...}) 也没有 fallback, 渲染为空")
	}

	host := viewNewSlot()
	if g := viewProp(props, "gap"); g != nil {
		// gap 透传到宿主: 列表项之间的间距与直接写子元素一致 (layout 的 gapOf
		// 对 slot 缺省回落父容器的 gap, 显式给了就用它)。
		host.Props["gap"] = g
	}

	st := &viewForState{fallback: viewBranchNew(viewBranchKids(fallback))}
	// fallback 分支保活 (与 Show 同理): "空 → 有数据 → 再空"不该让 fallback
	// 被销毁再复活 —— 静态子树复活后是死树。销毁挂在宿主身上, 一起收尾。
	viewDetachCleanup(host, func() *viewBranch {
		if st.showingFallback {
			return st.fallback
		}
		return nil
	}, []*viewBranch{st.fallback})

	dispose := runEffect(func() object.Value {
		viewForUpdate(host, st, each, keyFn, render, stableRows)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		host.effects = append(host.effects, dispose)
	}
	return host
}

// viewList 把 each 的求值结果规整成列表:
//
//	数组          → 元素序列
//	数字 n        → 0..n-1 (Vue 的 v-for="n in 10")
//	null/undefined/布尔 → 空列表
//
// 其它可迭代对象 (Map/Set) 不认: 列表渲染只接受数组, 需要别的形态请在脚本里
// Array.from 先把形状规整好 (内核不猜用户的意图)。
func viewList(each object.Value) []object.Value {
	v := viewResolve(each)
	switch x := v.(type) {
	case *object.Array:
		return x.Elements
	case *object.Number:
		n := int(x.Value)
		if n < 0 {
			n = 0
		}
		out := make([]object.Value, n)
		for i := range out {
			out[i] = object.NewNumber(float64(i))
		}
		return out
	}
	return nil
}

// viewRowKey 算一行的配对键。
//
// 用户 key 函数返回可哈希标量 (字符串/数字/布尔) 时用它; 返回 null/undefined
// 或不给 key 时退化为位置键 —— 两者都对应"就地复用" (下标上的项没换就不动)。
// **重复键**必须处理: 同一个宿主节点会被两行同时认领 ⇒ 树被写坏。发现重复就
// 把该行降级为位置键并警告一次 (只警告一次, 重排是热路径)。
func viewRowKey(keyFn, item object.Value, i int, used map[string]bool, warnedDup *bool) string {
	key := ""
	// 只调可调用的: 非函数 (key={42} 这类) 已经在 jsViewFor 里警告过了, 这里再抛一个
	// "42 is not a function" 只会多一层噪音, 结果同样是退化为位置键。
	if keyFn != nil && object.IsCallable(keyFn) {
		if s, ok := viewKeyString(viewCall(keyFn, item, object.NewNumber(float64(i)))); ok {
			key = s
		}
	}
	if key == "" {
		key = "#" + strconv.Itoa(i)
	}
	if used[key] {
		if !*warnedDup {
			recordWarn("gx/view For: 出现重复 key %q, 冲突行已退化为按位置匹配 (key 必须全表唯一)", key)
			*warnedDup = true
		}
		key = "#dup:" + strconv.Itoa(i)
	}
	used[key] = true
	return key
}

// viewKeyString 把 key 值转成可比较的字符串键, 附带类型前缀 (区分 1 与 "1",
// 否则数字 id 与字符串 id 会错误合流)。返回 ok=false 表示这个值不适合当 key。
func viewKeyString(v object.Value) (string, bool) {
	switch x := v.(type) {
	case *object.String:
		return "s:" + x.Value, true
	case *object.Number:
		return "n:" + strconv.FormatFloat(x.Value, 'g', -1, 64), true
	case *object.Boolean:
		if x.Value {
			return "b:1", true
		}
		return "b:0", true
	}
	return "", false
}

// ===== 误用出声: 取值函数类 prop 的写法校验 =====
//
// 为什么值得单独一块: 控制流的三个键 (each / show / key) 都收**取值函数**,
// 而写成快照 (each={rows()} / show={open()}) 与正确写法在屏幕上只差一对括号,
// 后果却是静默的 —— 列表与条件从此不再变化, 没有任何报错。与 gfx/model.go 同一条
// 纪律: 改不了已经写下的表达式, 至少让"写错了"这件事出声。
//
// 合法形态各自独立, 不合并成一条"通用规则" (合并只会得到一条谁也记不住的规则):
//
//	each    取值函数 (传 signal 本身也行) / 数组字面量 / 数字   —— 静态列表是合法写法
//	show    取值函数 / 布尔字面量  (Switch 里 Match 的 when= 同理)
//	key     取值函数 / 字段名字符串 (key="id")
//	stable  字面量布尔 (它压根不是响应式 prop)
//
// 警告一律去重: h() 在每次重建时都会跑 (列表行的渲染函数、响应式分支换代),
// 不去重会在热路径上刷屏 —— 与 warnUnknownTagOnce / modelWarnOnce 同一条纪律。

var (
	viewWarnMu   sync.Mutex
	viewWarnSeen = map[string]struct{}{}
)

// viewWarnOnce 同一类警告只说一次。
func viewWarnOnce(key, format string, args ...interface{}) {
	viewWarnMu.Lock()
	_, seen := viewWarnSeen[key]
	if !seen {
		viewWarnSeen[key] = struct{}{}
	}
	viewWarnMu.Unlock()
	if !seen {
		recordWarn(format, args...)
	}
}

// resetViewWarns 清空去重表 (仅测试用: 警告环是跨用例共享的进程级状态)。
func resetViewWarns() {
	viewWarnMu.Lock()
	viewWarnSeen = map[string]struct{}{}
	viewWarnMu.Unlock()
}

// viewCheckEach 校验 each: 取值函数 / 数组 / 数字, 其余一律渲染成空列表 (静默)。
func viewCheckEach(v object.Value) {
	if v == nil || object.IsCallable(v) {
		return
	}
	switch v.(type) {
	case *object.Array, *object.Number:
		return
	}
	viewWarnOnce("each", "each 指令: 需要取值函数 (传 signal 本身也行) 或数组/数字字面量, "+
		"收到 %s —— 它会一直渲染成空列表", v.Type())
}

// viewCheckWhen 校验 when (来自 show 指令的 show= 与 Switch 里 Match 的 when=):
// 取值函数 / 布尔字面量。
//
// 布尔字面量单独给一条文案: `show={open()}` 就是这么写出来的, 而它的现象是
// "条件冻在第一帧" —— 只喊"要传函数"用户未必对得上号。
func viewCheckWhen(what string, v object.Value) {
	if v == nil || object.IsCallable(v) {
		return
	}
	if _, ok := v.(*object.Boolean); ok {
		viewWarnOnce("when:"+what, "%s: 收到静态布尔 (%s) —— 刻意的常量条件可忽略; "+
			"若是忘了函数括号, 条件之后不会再跟着 signal 变", what, v.Inspect())
		return
	}
	viewWarnOnce("when:"+what, "%s: 需要取值函数 (传 signal 本身也行), 收到 %s —— "+
		"它会被当成固定真值/假值, 条件不会再变", what, v.Type())
}

// viewCheckKey 校验 key: 取值函数 / 字段名字符串。
func viewCheckKey(v object.Value) {
	if v == nil || object.IsCallable(v) {
		return
	}
	if _, ok := v.(*object.String); ok {
		return
	}
	viewWarnOnce("key", "each 指令: key 需要取值函数或字段名简写 (key=\"id\"), 收到 %s —— "+
		"该行会退化为按位置匹配", v.Type())
}

// viewCheckStable 校验 stable: 只认字面量布尔 (传函数会被类型断言静默吃成 false)。
func viewCheckStable(v object.Value) {
	if v == nil {
		return
	}
	if _, ok := v.(*object.Boolean); ok {
		return
	}
	viewWarnOnce("stable", "each 指令: stable 只认字面量布尔, 收到 %s —— "+
		"它会被静默当成 false (位置变化仍会重建行)", v.Type())
}

// viewKeyField 把 key="id" 展开成取值函数: 从列表项上取同名属性。
//
// 取不到 (项不是对象 / 没有该字段) 时返回 undefined ⇒ viewRowKey 退化为位置键,
// 与不给 key 同义 —— 这正是"字段名写错"的自然后果, 不必再加一条警告。
func viewKeyField(name string) object.Value {
	return object.NewBuiltin("view.key."+name, func(args ...object.Value) object.Value {
		if len(args) == 0 || args[0] == nil {
			return object.UndefinedSingleton
		}
		if v, ok := args[0].GetProperty(name); ok && v != nil {
			return v
		}
		return object.UndefinedSingleton
	})
}

// viewForUpdate 按最新的 each 值重算列表 (就地改 st)。
//
// stableRows 为真时下标不参与复用判定 (见文件头: stable 的语义与代价)。
//
// 顺序不能倒: 先按新顺序配对并复用, **最后**才销毁没配上对的旧行 —— 反过来
// 会把这一轮还要复用的行一起扔掉。宿主子节点的重排也放在最后, 因为复用行的
// 节点指针一直活到最后一步。
//
// 空列表 + 有 fallback 时, 挂的是 fallback 分支的 holder (它保活, 见 jsViewFor);
// 此时所有行照常销毁 (行是数据: 列表空了它们就该收尾), 但 fallback 不动。
func viewForUpdate(host *GuiNode, st *viewForState, each, keyFn, render object.Value, stableRows bool) {
	items := viewList(each)
	rows := make([]*viewRow, 0, len(items))

	// 空列表: 只显示 fallback (懒构建: 从没空过就永远不会构建它)
	if len(items) == 0 && st.fallback.kids != nil {
		for _, o := range st.rows {
			disposeNode(o.host)
		}
		st.rows = nil
		st.fallback.holder.Parent = host
		host.Children = []*GuiNode{st.fallback.holder}
		if !st.showingFallback {
			st.fallback.build()
			st.showingFallback = true
			markNodeDirty(host)
		}
		return
	}
	// 有数据: fallback 从 Children 里摘掉 (保活, 不销毁)
	if st.showingFallback {
		st.fallback.holder.Parent = nil
		st.showingFallback = false
	}

	// 旧行索引: key → 行。同 key 只在表里留第一个 (生成期已保证 key 唯一)。
	oldByKey := make(map[string]*viewRow, len(st.rows))
	for _, r := range st.rows {
		if _, dup := oldByKey[r.key]; !dup {
			oldByKey[r.key] = r
		}
	}

	used := make(map[string]bool, len(items))
	for i, item := range items {
		key := viewRowKey(keyFn, item, i, used, &st.warnedDupKey)
		prev := oldByKey[key]
		if prev == nil {
			r := &viewRow{key: key, item: item, index: i, host: viewNewSlot()}
			viewMountRow(r, render, i, item)
			rows = append(rows, r)
			continue
		}
		if viewSame(prev.item, item) && (stableRows || prev.index == i) {
			// 稳定快路径: 配对相等 ⇒ 一行不重建 (节点指针不变, 行内状态全留)
			prev.index = i
			rows = append(rows, prev)
			continue
		}
		viewMountRow(prev, render, i, item)
		rows = append(rows, prev)
	}

	// 销毁没有复用的旧行: 子树连同它接线的 effect / onCleanup 一起收尾
	kept := make(map[*viewRow]bool, len(rows))
	for _, r := range rows {
		kept[r] = true
	}
	for _, o := range st.rows {
		if !kept[o] {
			disposeNode(o.host)
		}
	}
	st.rows = rows

	// 按新顺序重排宿主子节点 (复用行的位置跟着改, 节点本身不重建)
	kids := make([]*GuiNode, 0, len(rows))
	for _, r := range rows {
		r.host.Parent = host
		kids = append(kids, r.host)
	}
	host.Children = kids
	markNodeDirty(host)
}

// viewMountRow (重)挂一行的内容。
//
// 顺序与 wireReactiveChild 一致: 先跑上一代的 onCleanup 再拆旧子树 (清理回调
// 可能还要看一眼即将销毁的子树), 然后在新的一代作用域里求值渲染函数, 最后
// 装值 + 跑这一代的 onMount。渲染期登记的 onCleanup 挂到**行宿主**上, 于是
// "行被删掉"与"行内子树销毁"是同一个时刻。
//
// render 为 nil (理论上只有误用时才发生) 时这一行是空的 —— 不抛错, 界面少一行
// 比整棵树挂掉好排查 (而缺 render 的情况在 jsViewFor 里已经警告过了)。
func viewMountRow(r *viewRow, render object.Value, index int, item object.Value) {
	host := r.host
	runCleanups(host)
	clearSlot(host)

	sc := object.PushWiringScope()
	var v object.Value
	if render != nil {
		v = viewCall(render, item, object.NewNumber(float64(index)))
	}
	host.cleanups = append(host.cleanups, sc.Cleanups...)
	object.PopWiringScope()

	r.index = index
	r.item = item
	mountValue(host, v)
	for _, fn := range sc.Mounts {
		object.CallFunction(fn, nil)
		if err := takeCallbackErr(); err != nil {
			recordWarn("gx/view onMount error: %v", err)
		}
	}
}

// ===== Show: 条件渲染 =====

// jsViewShow 是 <Show when={...} fallback={...}>...</Show> (keep-alive 显隐)。
//
// 条件为真显示 children, 为假显示 fallback (没有 fallback 就是空宿主: 零尺寸,
// 对布局完全透明)。两条分支各自保活, 来回切换不重建 —— 于是行内的输入框、
// 滚动位置、局部 signal 都留在原地, 也不会踩到"静态子树被销毁后无法复活"。
// 语义与代价见文件头 "与 Vue 的对应关系"。
func jsViewShow(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	when := viewProp(props, "when")
	fallback := viewProp(props, "fallback")
	if when == nil {
		recordWarn("show 指令: 缺少 show= 条件, 按隐藏处理")
	}
	viewCheckWhen("show 指令", when)

	body := viewBranchNew(args[1:])
	fb := viewBranchNew(viewBranchKids(fallback))
	host := viewNewSlot()
	var active *viewBranch
	viewDetachCleanup(host, func() *viewBranch { return active }, []*viewBranch{body, fb})

	dispose := runEffect(func() object.Value {
		want := fb
		if viewTruthy(viewResolve(when)) {
			want = body
		}
		if want == active {
			// 分支没变: 什么都不用做 —— 内容挂在树上, 自己的 effect 会保持它新鲜。
			return object.UndefinedSingleton
		}
		want.attach(host, active)
		want.build()
		active = want
		markNodeDirty(host)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		host.effects = append(host.effects, dispose)
	}
	return host
}

// ===== Switch / Match: 多分支条件 =====

// viewMatchValue 是 Match 的返回值: 一个**数据标记**, 不是元素。它不参与
// 布局/绘制, 只被 Switch 读取 (when + 子节点); 误挂在别处时会渲染成一行
// "[view Match]" 文本 —— 误用可见, 不静默。
type viewMatchValue struct {
	when object.Value
	kids []object.Value
}

func (m *viewMatchValue) Type() object.ObjectType { return object.ObjectType("VIEW_MATCH") }
func (m *viewMatchValue) Inspect() string         { return "[view Match]" }
func (m *viewMatchValue) IsTruthy() bool          { return true }
func (m *viewMatchValue) GetProperty(string) (object.Value, bool) {
	return object.UndefinedSingleton, false
}
func (m *viewMatchValue) SetProperty(string, object.Value) {}

// jsViewMatch 是 <Match when={...}>...</Match>。
func jsViewMatch(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	when := viewProp(props, "when")
	if when == nil {
		recordWarn("Switch 的 Match: 缺少 when (条件), 按 false 处理")
	}
	viewCheckWhen("Switch 的 Match", when)
	return &viewMatchValue{when: when, kids: args[1:]}
}

// jsViewSwitch 是 <Switch fallback={...}><Match .../>...</Switch>:
// 按声明序取第一个 when 为真的 Match, 都不真则用 fallback。分支同样 keep-alive。
//
// 与 Show 的唯一结构差异: 候选分支是**声明期**就建好的 (每个 Match 一个 holder),
// 判定顺序即声明顺序 —— 与 v-if / v-else-if 链一致。
func jsViewSwitch(args ...object.Value) object.Value {
	props := viewPropsArg(args)
	fallback := viewProp(props, "fallback")

	matches := make([]*viewMatchValue, 0, len(args)-1)
	for _, c := range args[1:] {
		if c == nil {
			continue
		}
		if m, ok := c.(*viewMatchValue); ok {
			matches = append(matches, m)
			continue
		}
		// 只认 Match: 别的子节点是误用 (Switch 不是布局容器)
		recordWarn("gx/view Switch: 子节点只认 <Match>, 收到 %s 已忽略", c.Type())
	}
	if len(matches) == 0 {
		recordWarn("gx/view Switch: 没有 <Match> 子节点, 只可能渲染 fallback")
	}

	branches := make([]*viewBranch, 0, len(matches)+1)
	for _, m := range matches {
		branches = append(branches, viewBranchNew(m.kids))
	}
	fb := viewBranchNew(viewBranchKids(fallback))
	branches = append(branches, fb)

	host := viewNewSlot()
	var active *viewBranch
	viewDetachCleanup(host, func() *viewBranch { return active }, branches)

	dispose := runEffect(func() object.Value {
		want := fb
		for i, m := range matches {
			if viewTruthy(viewResolve(m.when)) {
				want = branches[i]
				break
			}
		}
		if want == active {
			return object.UndefinedSingleton
		}
		want.attach(host, active)
		want.build()
		active = want
		markNodeDirty(host)
		return object.UndefinedSingleton
	})
	if dispose != nil {
		host.effects = append(host.effects, dispose)
	}
	return host
}
