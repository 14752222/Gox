package gfx

// ===== vlist 在「数据源持续追加」下的行为 (看板 rYffwm) =====
//
// 单里问的三件事在此之前**只有静态列表的数据**: 首轮物化与滚动已经有人量过
// (vlist_bench / vlist_test), 但"列表一直在长"这条路径一次都没测过 —— 而日志
// 查看器的实时 tail 走的正是这条路径。按单里写的处置口径 (**先测量后动手**),
// 这一组先把现象量出来:
//
//  1. **是否重建全列表** —— 追加 K 行时只重算了窗口内的行, 还是把整列表重建了
//     一遍 (后者意味着每来一批日志都要付一次全量成本, 行数越多越卡);
//  2. **滚动位置是否保持** —— 在列表尾部追加时, 正在回看历史的用户不能被挪走;
//  3. **增长是否收敛** —— 连追很多批之后树上的节点数该稳定在上限, 而不是一路
//     涨上去 (那种涨法在看半小时日志的过程里就会吃掉内存)。
//
// 判据一律取**行渲染函数被调用的次数**与**树上真实行数**, 不取耗时 —— 耗时随机
// 器变, 而"追加一批有没有重建全列表"是个与机器无关的结构性事实。
//
// ## 驱动口径 (踩过才知道有多要紧)
//
// 所有"滚动 / 追加之后跑一遍布局"都必须发生在**帧循环内** (driveSteps 的步骤
// 里), 不能在主循环之外自己调 Layout。原因: each 的数据源与每行渲染都是脚本
// 回调, 走 object.CallFunction —— 主脚本跑完后 currentVM 归位, 循环外它是个
// **空操作**, 回调统统返回 undefined ⇒ 列表被"读成空"。症状是滚一行之后
// `总量=0 物化=0`, 一眼看去像是内核把列表清空了, 实则是测试自己没有 VM 上下文。

import (
	"fmt"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// vlistAppendRowH 与 vlist_demo.js 的 ROW_H 一致。
const vlistAppendRowH = 28

// vlistAppendSource 造一棵可持续追加的 vlist 树。
//
// 三个导出:
//   - `__append(n)`: 往 signal 里追加 n 行 —— 这就是"数据源持续追加"的入口;
//   - `__stats.rowCalls`: 行渲染函数**累计**被调用次数 —— 追加一批后它的增量
//     就是"这一次追加重算了多少行", 这是回答"有没有重建全列表"的唯一可信指标
//     (树上最后只剩窗口内的行, 全量重建会被随后的销毁掩盖, 数树看不出来);
//   - `__stats.total`: 当前数据总行数 (由 __append 顺手写回 —— Go 侧直接读对象
//     字段即可, 不必**调用**脚本函数, 见上面的驱动口径)。
//
// `each` 给的是 getter 而非常量数组: 只有 signal 变长才是"数据源持续追加"。
func vlistAppendSource(initial int) string {
	var b strings.Builder
	b.WriteString("import { createSignal } from \"gx/solid\";\n")
	b.WriteString("import { h, render } from \"gx/gfx\";\n")
	fmt.Fprintf(&b, "const ROW_H = %d;\n", vlistAppendRowH)
	b.WriteString("const rows = [];\n")
	fmt.Fprintf(&b, "for (let i = 0; i < %d; i++) rows.push({ id: i, title: \"第 \" + i + \" 行\" });\n", initial)
	b.WriteString("const [lines, setLines] = createSignal(rows);\n")
	b.WriteString("const stats = { rowCalls: 0, total: 0 };\n")
	b.WriteString("globalThis.__stats = stats;\n")
	b.WriteString("globalThis.__append = (n) => {\n")
	b.WriteString("  const cur = lines();\n")
	b.WriteString("  const add = [];\n")
	b.WriteString("  for (let k = 0; k < n; k++) add.push({ id: cur.length + k, title: \"追加 \" + (cur.length + k) });\n")
	b.WriteString("  setLines(cur.concat(add));\n")
	b.WriteString("  stats.total = cur.length + n;\n")
	b.WriteString("};\n")
	b.WriteString("globalThis.__root = render(\n")
	b.WriteString("  h(\"column\", {},\n")
	b.WriteString("    h(\"scroll\", { vlist: true, itemHeight: ROW_H, width: 420, height: 300 },\n")
	b.WriteString("      h(\"view\", { each: () => lines(), key: \"id\" },\n")
	b.WriteString("        (r, i) => { stats.rowCalls++; return h(\"row\", { height: ROW_H, padding: 6 }, h(\"text\", { font: 12 }, i + \" · \" + r.title)); }\n")
	b.WriteString("      )\n")
	b.WriteString("    )\n")
	b.WriteString("  ),\n")
	b.WriteString("  { title: \"vlist append\", width: 470, height: 400 }\n")
	b.WriteString(");\n")
	return b.String()
}

// vlistAppendSetup 起现场: 跑脚本建树 + 首次布局, 返回 (VM, 根, scroll)。
func vlistAppendSetup(t *testing.T, initial int) (*vm.VM, *GuiNode, *GuiNode) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, _ := evalUI(t, vlistAppendSource(initial))
	root := uiRoot(t)
	sc := findFirst(root, "scroll")
	if sc == nil {
		t.Fatal("没有挂上 scroll 节点")
	}
	// 首帧那一次可以站在循环外: 它只负责"把树按窗口收口", 不涉及数据源重读。
	Layout(root, 470, 400)
	return v, root, sc
}

// vlistStatValue 读 __stats 上的一个数字字段。
func vlistStatValue(t *testing.T, v *vm.VM, field string) int {
	t.Helper()
	s, ok := v.Globals().Get("__stats")
	if !ok {
		t.Fatal("缺 __stats")
	}
	obj, ok := s.(*object.Object)
	if !ok {
		t.Fatalf("__stats 不是对象: %T", s)
	}
	n, ok := obj.GetProperty(field)
	if !ok {
		t.Fatalf("__stats 缺字段 %s", field)
	}
	num, ok := n.(*object.Number)
	if !ok {
		t.Fatalf("stats.%s 不是数字: %T", field, n)
	}
	return int(num.Value)
}

// vlistFrame 在**帧循环内**滚动若干像素并重排一次 —— 与真机一帧同构 (见文首)。
func vlistFrame(t *testing.T, v *vm.VM, root, sc *GuiNode, dy int) {
	t.Helper()
	driveSteps(t, v, func() {
		if dy != 0 {
			sc.scrollBy(0, dy)
		}
		Layout(root, 470, 400)
	})
}

// vlistAppendN 追加一批行并重排 (同样必须走在帧循环内)。
func vlistAppendN(t *testing.T, v *vm.VM, root *GuiNode, n int) {
	t.Helper()
	driveSteps(t, v, func() {
		callGlobal(t, v, "__append", object.NewNumber(float64(n)))
	})
	Layout(root, 470, 400)
}

// TestVlistAppend 是这一组的主用例: 追加一批之后总高跟上总行数, 而重算的行数
// 与树上物化的行数都保持在窗口量级。
//
// initial 取 5000 是刻意的: 远超一屏 (窗口十几行 + buffer), 于是"重算行数"一旦
// 接近 5000 就说明重建了全列表 —— 这个判据在几百行的小列表上根本分不出来。
func TestVlistAppend(t *testing.T) {
	const initial = 5000
	v, root, sc := vlistAppendSetup(t, initial)

	// 前置: 虚拟化确实生效了 (没生效的话后面量到的东西没有意义)。
	if want := initial * vlistAppendRowH; sc.contentH != want {
		t.Fatalf("初始 contentH = %d, want %d (虚拟化没生效?)", sc.contentH, want)
	}
	t.Logf("初始: 树上行数=%d %s", countTagDeep(sc, "row"), vlistStatsOfApp(sc).String())

	// 追一批 —— 日志查看器里这就是"接口推来一批新行"。此时视口在顶部, 新行
	// 落在窗口之外 ⇒ 理想结果是**一行都不用重算**。
	const add = 100
	base := vlistStatValue(t, v, "rowCalls")
	vlistAppendN(t, v, root, add)

	if got := vlistStatValue(t, v, "total"); got != initial+add {
		t.Fatalf("追加后总行数 = %d, want %d (signal 没生效)", got, initial+add)
	}
	// ① 总高必须跟上 —— 滚动条长度/行程全靠它, 跟不上就说明窗口漏掉了新内容。
	if want := (initial + add) * vlistAppendRowH; sc.contentH != want {
		t.Fatalf("追加后 contentH = %d, want %d (内容总高没跟上新增行)", sc.contentH, want)
	}
	// ② 重算行数必须是窗口量级, 不能是整列表。
	if grew := vlistStatValue(t, v, "rowCalls") - base; grew > 200 {
		t.Fatalf("追加 %d 行重算了 %d 行 —— 这是重建全列表 (每批日志都要付一次全量成本)", add, grew)
	}
	// ③ 树上的行数保持有界。
	if rows := countTagDeep(sc, "row"); rows > 40 {
		t.Fatalf("追加后树上物化了 %d 行, 远超窗口上限", rows)
	}
	t.Logf("追 %d 行: 重算 %d 行, 树上行数 %d, contentH=%d, %s", add,
		vlistStatValue(t, v, "rowCalls")-base, countTagDeep(sc, "row"), sc.contentH,
		vlistStatsOfApp(sc).String())
}

// TestVlistAppendMidScroll 在中段追加 —— 这是"窗口与新内容重叠"的那一路:
// 追加的数据可能落在视口之外, 也可能正好挡住视口。无论哪种, 重算都必须只有窗口
// 那么多行, 而且**滚回去看到的内容不能是空白**。
func TestVlistAppendMidScroll(t *testing.T) {
	const initial = 3000
	v, root, sc := vlistAppendSetup(t, initial)

	vlistFrame(t, v, root, sc, 2000)
	if sc.offsetY != 2000 {
		t.Fatalf("前置: 滚到 %d, 实际 %d", 2000, sc.offsetY)
	}

	base := vlistStatValue(t, v, "rowCalls")
	const add = 200
	vlistAppendN(t, v, root, add)

	if got := vlistStatValue(t, v, "total"); got != initial+add {
		t.Fatalf("总行数 = %d, want %d", got, initial+add)
	}
	if grew := vlistStatValue(t, v, "rowCalls") - base; grew > 200 {
		t.Fatalf("中段追加 %d 行重算了 %d 行 —— 超出窗口量级", add, grew)
	}
	rows := countTagDeep(sc, "row")
	if rows == 0 {
		t.Fatal("中段追加后一行都没物化 —— 再滚下去会是空白")
	}
	if rows > 40 {
		t.Fatalf("中段追加后物化了 %d 行, 超出窗口上限", rows)
	}
	t.Logf("中段追加 %d 行: 重算 %d 行, 树上 %d 行, %s", add,
		vlistStatValue(t, v, "rowCalls")-base, rows, vlistStatsOfApp(sc).String())
}

// TestVlistAppendKeepsScrollPosition 回看历史时不能被追加挪走。
//
// 这是 tail 类界面最常见的抱怨: 你在翻半小时前的报错, 新日志一来视口就被弹到
// 底部。"跟随最新"应当是**脚本显式要**的语义 (写 scrollTop="bottom"), 而不是
// 追加的副作用 —— 所以这里断言的是**不动**。
func TestVlistAppendKeepsScrollPosition(t *testing.T) {
	const initial = 3000
	v, root, sc := vlistAppendSetup(t, initial)

	vlistFrame(t, v, root, sc, 2000)
	want := sc.offsetY
	if want != 2000 {
		t.Fatalf("前置: 滚到 %d, 实际 %d", 2000, sc.offsetY)
	}

	for i := 0; i < 2; i++ {
		vlistAppendN(t, v, root, 50)
	}

	if sc.offsetY != want {
		t.Fatalf("追加后偏移从 %d 被改成 %d —— 正在回看历史的用户不该被挪走", want, sc.offsetY)
	}
	if got := vlistStatValue(t, v, "total"); got != initial+100 {
		t.Fatalf("总行数 = %d, want %d", got, initial+100)
	}
	t.Logf("追加 100 行后偏移仍为 %d, %s", sc.offsetY, vlistStatsOfApp(sc).String())
}

// TestVlistAppendGrowthConverges 连追多批之后树上的节点数要收敛。
//
// 关注点是"有没有累积": 每批都 leak 一点节点的话, 几十行的断言看不出来, 但挂机
// 看日志半小时就会胀出来。这里跑满 9 批, 断言**后一批不比基线大**。
func TestVlistAppendGrowthConverges(t *testing.T) {
	const initial = 2000
	v, root, sc := vlistAppendSetup(t, initial)

	// 基线取**第一批之后**: 首帧在顶部时窗口是 [0,13), 滚进中段稳定成 15 行,
	// 头一批差这两行是正常的窗型差, 不是累积。
	vlistFrame(t, v, root, sc, 112)
	vlistAppendN(t, v, root, 20)
	baseNodes := countNodes(sc)
	t.Logf("基线 (第 1 批后): 节点 %d, 行 %d", baseNodes, countTagDeep(sc, "row"))

	for i := 0; i < 8; i++ {
		// 每批都滚一段再追加 —— 顺便走过"窗口平移 + 追加"叠在一起的路径
		// (盯日志的用户本来就是一边滚一边看新行涌进来)。
		vlistFrame(t, v, root, sc, 112) // 4 行
		vlistAppendN(t, v, root, 20)

		nodes, rows := countNodes(sc), countTagDeep(sc, "row")
		if nodes > baseNodes {
			t.Fatalf("第 %d 批后节点数 %d > 基线 %d: 追加在累积节点", i+2, nodes, baseNodes)
		}
		if rows == 0 {
			t.Fatalf("第 %d 批后一行都没物化 —— 滚动+追加把列表清空了", i+2)
		}
		if rows > 40 {
			t.Fatalf("第 %d 批后树上 %d 行, 超出窗口上限", i+2, rows)
		}
	}
	wantTotal := initial + 20*9
	if got := vlistStatValue(t, v, "total"); got != wantTotal {
		t.Fatalf("总行数 = %d, want %d", got, wantTotal)
	}
	t.Logf("9 批之后: 节点 %d (基线 %d), 行 %d, 总行数 %d",
		countNodes(sc), baseNodes, countTagDeep(sc, "row"), vlistStatValue(t, v, "total"))
}

// vlistStatsOfApp 取列表现场的一句话视图: vlist 的每一环都可能"静默退化",
// 日志里先打一句现场, 出问题时读一行就知道断在哪。
func vlistStatsOfApp(sc *GuiNode) vlistStats {
	for _, c := range sc.Children {
		if c.forState != nil {
			st := c.forState
			return vlistStats{
				on: st.vlistOn, ready: st.winReady, total: st.total,
				first: st.win.first, last: st.win.last, itemH: st.cfg.itemH,
				spacer: len(st.spacers), rows: len(st.rows),
			}
		}
	}
	return vlistStats{}
}
