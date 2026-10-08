package gfx

// ===== <scroll> 滚动位置**写入口** (看板 r846P0) =====
//
// <scroll> 此前只有读能力 (滚轮/拖拽改 offsetY), 脚本没有任何把滚动位置写进去
// 的入口 ⇒ 日志查看器的「跳到底部」、聊天消息流、表格定位都做不出来。本组用例
// 钉住 scrollTop / scrollLeft 两个受控 prop 的语义:
//
//   - 数字 = 绝对像素 (钳位后一次性跳到位);
//   - "top"/"bottom" (横 "start"/"end") = 别名, bottom/end 随内容变长**粘性贴底**;
//   - **目标不变就不再施加** —— 否则用户滚轮一离开, 下一帧就被 prop 拽回去
//     (表现是"滚不动"), 这条正是本组最要紧的一条。

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// mkScrollProp 造一个 scroll (宽 w 高 h) 内装 rows 个高 rowH 的行, 并先把
// props 写进节点再布局 —— 写入口是在布局期读 prop 的, 顺序不能反。
func mkScrollProp(w, h, rows, rowH int, props map[string]object.Value) (*GuiNode, *GuiNode, []*GuiNode) {
	root := mkNode("column", nil)
	sc := mkNode("scroll", map[string]float64{"width": float64(w), "height": float64(h)})
	for k, v := range props {
		sc.Props[k] = v
	}
	mountChildren(root, sc)
	var kids []*GuiNode
	for i := 0; i < rows; i++ {
		kids = append(kids, mkScrollRow(sc, rowH))
	}
	Layout(root, 400, 400)
	return root, sc, kids
}

// mkScrollRow 往 scroll 里追加一行 (rowH 高), 返回该行节点。
func mkScrollRow(sc *GuiNode, rowH int) *GuiNode {
	r := mkNode("rect", map[string]float64{"height": float64(rowH)})
	mountChildren(sc, r)
	return r
}

func TestScrollTopPropJumpsOnce(t *testing.T) {
	// 20 行 × 36 = 720, 视口 120 ⇒ max = 600
	root, sc, kids := mkScrollProp(240, 120, 20, 36,
		map[string]object.Value{"scrollTop": object.NewNumber(200)})

	if sc.offsetY != 200 {
		t.Fatalf("scrollTop=200 后 offsetY = %d, want 200", sc.offsetY)
	}
	// 子节点必须真的跟着位移 (写入口不能只改字段不重排内容)
	if kids[0].Box.Y != sc.Box.Y-200 {
		t.Fatalf("row0.Box.Y = %d, want %d (应为容器顶 - 偏移)", kids[0].Box.Y, sc.Box.Y-200)
	}

	// 同一个目标重复布局: 不重复施加 —— 于是随后的用户滚轮不会被抢回去。
	Layout(root, 400, 400)
	if sc.offsetY != 200 {
		t.Fatalf("prop 未变化时重复布局不该改变偏移, got %d", sc.offsetY)
	}
}

func TestScrollTopDoesNotFightUserScroll(t *testing.T) {
	root, sc, _ := mkScrollProp(240, 120, 20, 36,
		map[string]object.Value{"scrollTop": object.NewNumber(200)})

	// 用户滚轮向上滚回 80px
	if !sc.scrollBy(0, -80) {
		t.Fatal("scrollBy(-80) 应生效")
	}
	if sc.offsetY != 120 {
		t.Fatalf("用户滚动后 offsetY = %d, want 120", sc.offsetY)
	}
	// 下一帧 prop 仍是 200: 目标是同一处 ⇒ 不得把用户拽回 200。
	Layout(root, 400, 400)
	if sc.offsetY != 120 {
		t.Fatalf("prop 未变化却把用户拽回去了: offsetY = %d, want 120", sc.offsetY)
	}

	// prop 换成别的值 ⇒ 这时才是一次新的跳转。
	sc.Props["scrollTop"] = object.NewNumber(400)
	Layout(root, 400, 400)
	if sc.offsetY != 400 {
		t.Fatalf("prop 变为 400 后 offsetY = %d, want 400", sc.offsetY)
	}
}

func TestScrollTopBottomAliasFollowsGrowingContent(t *testing.T) {
	root, sc, _ := mkScrollProp(240, 120, 20, 36,
		map[string]object.Value{"scrollTop": object.NewString("bottom")})

	want := 20*36 - 120
	if sc.offsetY != want {
		t.Fatalf(`scrollTop:"bottom" 后 offsetY = %d, want %d`, sc.offsetY, want)
	}

	// 用户往上翻: 内容没变就不打扰 (这是"跟随最新"能用的前提 —— 否则用户
	// 根本没法回看历史)。
	if !sc.scrollBy(0, -400) {
		t.Fatal("scrollBy(-400) 应生效")
	}
	Layout(root, 400, 400)
	if sc.offsetY != want-400 {
		t.Fatalf("内容未变时不该重新贴底: offsetY = %d, want %d", sc.offsetY, want-400)
	}

	// 追加 5 行 ⇒ max 变大 ⇒ 目标变化 ⇒ 自动重新贴底 (日志流要的行为)。
	for i := 0; i < 5; i++ {
		mkScrollRow(sc, 36)
	}
	Layout(root, 400, 400)
	want2 := 25*36 - 120
	if sc.offsetY != want2 {
		t.Fatalf("内容变长后未跟随到底: offsetY = %d, want %d", sc.offsetY, want2)
	}
}

func TestScrollTopTopAliasAndClamp(t *testing.T) {
	// 越界值按 max 钳位 (写 1e9 等于"跳到底部")
	_, sc, _ := mkScrollProp(240, 120, 20, 36,
		map[string]object.Value{"scrollTop": object.NewNumber(1e9)})
	if sc.offsetY != 600 {
		t.Fatalf("越界 scrollTop 应钳到 max=600, got %d", sc.offsetY)
	}

	// "top" 回到顶部, 且不具粘性
	sc.Props["scrollTop"] = object.NewString("top")
	Layout(sc.Parent, 400, 400)
	if sc.offsetY != 0 {
		t.Fatalf(`scrollTop:"top" 后 offsetY = %d, want 0`, sc.offsetY)
	}
}

func TestScrollLeftPropAndEndAlias(t *testing.T) {
	root := mkNode("column", nil)
	sc := mkNode("scroll", map[string]float64{"width": 200, "height": 100})
	mountChildren(root, sc)
	// 横向: stretch 的行会跟着视口铺满 (不构成溢出), 所以这里给显式 width。
	wide := mkNode("rect", map[string]float64{"width": 400, "height": 40})
	mountChildren(sc, wide)
	sc.Props["scrollLeft"] = object.NewString("end")
	Layout(root, 400, 400)

	if sc.contentW != 400 {
		t.Fatalf("contentW = %d, want 400", sc.contentW)
	}
	if max := sc.scrollMaxOffsetX(); max != 400-200 {
		t.Fatalf("maxOffsetX = %d, want %d", max, 400-200)
	}
	if sc.offsetX != 400-200 {
		t.Fatalf(`scrollLeft:"end" 后 offsetX = %d, want %d`, sc.offsetX, 400-200)
	}
	if wide.Box.X != sc.Box.X-(400-200) {
		t.Fatalf("子节点未随横向偏移位移: wide.Box.X = %d", wide.Box.X)
	}
}

// TestScrollWritePropEndToEnd 端到端 (脚本 → prop → 布局 → 偏移): 上面几例
// 是直接写 Props 的 Go 侧用例, 这条走**完整 JS 链路** —— 响应式 prop 把 signal
// 值写进 Props, 布局期 applyScrollCommand 才读得到。
func TestScrollWritePropEndToEnd(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	// evalUI 而非 evalUIRoot: 只需要 root + VM, 不需要 app。
	v, _ := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [cmd, setCmd] = createSignal(0);
		const rows = [];
		for (let i = 0; i < 20; i++) rows.push(i);
		render(
			// 必须包一层: 根节点的 width/height 会被窗口撑开 (不看自己的 props),
			// 包进 column 之后 <scroll> 才是 240×120 的受控视口。
			h("column", null,
				h("scroll", { width: 240, height: 120, scrollTop: () => cmd() },
					rows.map(function (i) { return h("rect", { height: 36 }); }))),
			{ title: "scroll-write", width: 400, height: 400 }
		);
		function jump(v) { setCmd(v); }
	`)

	root := uiRoot(t)
	sc := findFirst(root, "scroll")
	if sc == nil {
		t.Fatal("没有挂上 scroll 节点")
	}
	if sc.contentH != 720 {
		t.Fatalf("contentH = %d, want 720 (脚本给的 20 行没接上?)", sc.contentH)
	}
	if max := sc.scrollMaxOffset(); max != 600 {
		t.Fatalf("maxOffset = %d, want 600", max)
	}
	if sc.offsetY != 0 {
		t.Fatalf("初始 offsetY = %d, want 0", sc.offsetY)
	}

	// 两次写入: 数字 (一次性跳到 200) → "bottom" 别名 (贴底)。
	//
	// signal 必须在帧循环里改 (主脚本跑完后 currentVM 归位, object.CallFunction
	// 桥会退化成空操作), 几何则在 driveSteps 之后手动 Layout 一次 —— 与
	// animate_test 同一口径: 真实应用里这一步由每帧的循环代劳, 测试里不想把
	// 断言挂在"重绘时机"上。
	driveSteps(t, v, func() { callGlobal(t, v, "jump", object.NewNumber(200)) })
	Layout(root, 400, 300)
	if sc.offsetY != 200 {
		t.Fatalf("scrollTop={200} 后 offsetY = %d, want 200", sc.offsetY)
	}

	driveSteps(t, v, func() { callGlobal(t, v, "jump", object.NewString("bottom")) })
	Layout(root, 400, 300)
	if sc.offsetY != 600 {
		t.Fatalf(`scrollTop={"bottom"} 后 offsetY = %d, want 600`, sc.offsetY)
	}
}

func TestScrollWritePropIgnoresUnknownValues(t *testing.T) {
	// 不认识的值 (布尔/对象/拼错的字符串) 一律忽略: 不报错也不动偏移 ——
	// 与 String(value)/Boolean 一族"降级而不炸"的口径一致。
	_, sc, _ := mkScrollProp(240, 120, 20, 36, map[string]object.Value{
		"scrollTop":  object.NewString("BOTTOM"),
		"scrollLeft": object.NewBoolean(true),
	})
	if sc.offsetY != 0 || sc.offsetX != 0 {
		t.Fatalf("不认识的取值不得改变偏移: %d/%d", sc.offsetX, sc.offsetY)
	}
	// 缺这两个 prop 的节点完全不施加写入 —— 偏移照旧由滚轮/拖拽维护。
	sc.Props["scrollTop"] = object.NewNumber(100)
	Layout(sc.Parent, 400, 400)
	if sc.offsetY != 100 {
		t.Fatalf("换成合法值后应生效, got %d", sc.offsetY)
	}
}

// TestOnScrollFiresOnUserScroll 用户滚动要派发 onScroll({offsetY}) ——
// vlist_demo.js 早就按这个约定写了 `onScroll: (e) => …`, 而 Go 侧此前**根本没有
// 实现它**: 回调从来不会被调用, 且不报错 (典型的静默失效)。
func TestOnScrollFiresOnUserScroll(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, _ := evalUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [seen, setSeen] = createSignal("none");
		const rows = [];
		for (let i = 0; i < 20; i++) rows.push(i);
		render(
			h("column", null,
				h("scroll", {
					width: 240, height: 120,
					onScroll: (e) => setSeen("offsetY=" + e.offsetY),
				}, rows.map(function (i) { return h("rect", { height: 36 }); })),
				h("text", { font: 11 }, () => seen())
			),
			{ title: "onscroll", width: 400, height: 400 }
		);
	`)

	root := uiRoot(t)
	sc := findFirst(root, "scroll")
	if sc == nil {
		t.Fatal("没有挂上 scroll 节点")
	}

	// 在帧循环里手动滚两格 (循环外没有 currentVM, 回调没法回调脚本)
	driveSteps(t, v, func() { sc.scrollBy(0, 120) })
	Layout(root, 400, 300)

	if !textContainsAny(root, "offsetY=120") {
		t.Fatal("用户滚动后未派发 onScroll (或载荷里没有 offsetY)")
	}
}

// TestScrollWriteDemoScript 用 testdata/scroll_to_demo.js 走一遍真实脚本链路:
// 「追加一行」靠粘性别名 bottom 跟随最新 → 「回到顶部」跳到 0 → 「跳到第 6 行」
// 写绝对像素跳到位。之所以要这条端到端用例: 前面几条都是 Go 侧直接调布局,
// 受控 prop 真正落地还要过一遍 gfx/solid signal + h() 建树, 中间任何一环
// 把 prop 名写丢 (比如属性名叫 scrollTop 而执行侧读的是 scrollY) 都测不出来。
func TestScrollWriteDemoScript(t *testing.T) {
	const rowH = 28 // 与 scroll_to_demo.js 里的 ROW_H 一致

	scrollOf := func(root *GuiNode) *GuiNode {
		sc := findFirst(root, "scroll")
		if sc == nil {
			t.Fatal("demo 里没有 scroll 节点")
		}
		return sc
	}
	// 一列 actions 里每个元素是一轮 pump: 该轮先断言上一轮点击的后果, 再注入下一次点击
	// (受控 prop 要等 signal 写回 + 下一轮 pump 才落回节点, 所以一次点击一轮)。
	clickAnd := func(btn string, assert func(*GuiNode)) func(*GuiNode, *fakeSurface) {
		return func(root *GuiNode, fake *fakeSurface) {
			if assert != nil {
				assert(root)
			}
			click(fake, buttonWithText(root, btn))
		}
	}
	assertRows := func(want int) func(*GuiNode) {
		return func(root *GuiNode) {
			sc := scrollOf(root)
			if got := len(findAll(root, "rect")); got != want {
				t.Fatalf("行数 = %d, want %d", got, want)
			}
			if sc.contentH != want*rowH {
				t.Fatalf("contentH = %d, want %d", sc.contentH, want*rowH)
			}
		}
	}

	steps := []func(*GuiNode, *fakeSurface){
		// 3 行只有 84px, 视口比它高 ⇒ 还没有可滚动区间, 贴底 == 顶部 == 0。
		clickAnd("追加一行", assertRows(3)),
		clickAnd("追加一行", assertRows(4)),
		clickAnd("追加一行", assertRows(5)),
		clickAnd("追加一行", assertRows(6)),
		clickAnd("追加一行", assertRows(7)),
		clickAnd("追加一行", assertRows(8)),
		clickAnd("追加一行", assertRows(9)),
		clickAnd("追加一行", assertRows(10)),
		clickAnd("追加一行", assertRows(11)),
		// 12 行 = 336px > 视口 ⇒ 出现真正的可滚动区间。scrollTop 恒为粘性别名
		// "bottom", 所以每追加一行都要重新贴底一次 —— 这就是"跟随最新"。
		clickAnd("回到顶部", func(root *GuiNode) {
			sc := scrollOf(root)
			assertRows(12)(root)
			if sc.scrollMaxOffset() <= 0 {
				t.Fatalf("12 行应已撑出滚动区间, maxOffset = %d", sc.scrollMaxOffset())
			}
			if sc.offsetY != sc.scrollMaxOffset() {
				t.Fatalf("bottom 别名未贴底: offsetY = %d, max = %d", sc.offsetY, sc.scrollMaxOffset())
			}
		}),
		// 「回到顶部」关掉 follow 并写 "top" ⇒ 回到 0。
		clickAnd("跳到第 6 行", func(root *GuiNode) {
			sc := scrollOf(root)
			if sc.offsetY != 0 {
				t.Fatalf("top 别名未回到顶部: offsetY = %d, want 0", sc.offsetY)
			}
		}),
		// 绝对像素跳行: 6*ROW_H = 168, 超出可滚动区间时按内容总高钳位。
		clickAnd("跟随最新", func(root *GuiNode) {
			sc := scrollOf(root)
			want := 6 * rowH
			if max := sc.scrollMaxOffset(); want > max {
				want = max
			}
			if sc.offsetY != want {
				t.Fatalf("跳行未落到第 6 行: offsetY = %d, want %d (max = %d)", sc.offsetY, want, sc.scrollMaxOffset())
			}
			if sc.offsetY == 0 || sc.offsetY == sc.scrollMaxOffset() {
				t.Fatalf("跳到第 6 行不该落在两头: offsetY = %d, max = %d", sc.offsetY, sc.scrollMaxOffset())
			}
		}),
		// 「跟随最新」重新打开粘底 —— 最后一轮不再点击, 只收尾断言。
		func(root *GuiNode, fake *fakeSurface) {
			sc := scrollOf(root)
			if sc.offsetY != sc.scrollMaxOffset() {
				t.Fatalf("跟随最新未重新贴底: offsetY = %d, max = %d", sc.offsetY, sc.scrollMaxOffset())
			}
		},
	}
	runDemoSteps(t, "scroll_to_demo.js", steps)
}
