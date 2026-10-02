package gfx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== T10 无障碍: 焦点注册表 / Tab 遍历 / 方向键导航 / aria 语义 =====
//
// 用例分两层, 与内核的分层一致:
//   - **纯 Go 层** (mountTestApp / renderTree): Tab 顺序、焦点陷阱、方向键
//     语义、焦点校正。这层能精确控制"一次 pump 一次观察", 是断言的主场。
//   - **脚本层** (gx/a11y 模块): focusOrder() 的字段形状 —— 它是官网画廊
//     面板与文档表的输入, 属于对外承诺, 要有专门的形状用例锁住。

// a11yHit 是"被调用了几次"的计数器, 挂到 onClick / onInput / onChange 上。
type a11yHit struct{ n int }

func (h *a11yHit) handler() object.Value {
	return object.NewBuiltin("a11yHit", func(args ...object.Value) object.Value {
		h.n++
		return object.UndefinedSingleton
	})
}

// resetA11yWarnings 清掉"属性笔误只告警一次"的去重表 (包级状态, 用例之间
// 必须隔离, 否则第二个用例想验告警时已经被第一个消耗掉了)。
func resetA11yWarnings() {
	a11yPropWarnMu.Lock()
	a11yPropWarned = map[string]struct{}{}
	a11yPropWarnMu.Unlock()
}

// mkField 造一个带固有尺寸的叶子控件 (输入类) —— 不写尺寸时它们靠
// intrinsicSize 也能拿到盒子, 但显式给尺寸能让焦点顺序用例的几何断言更直白。
func mkField(tag string, w, h float64) *GuiNode {
	return mkNode(tag, map[string]float64{"width": w, "height": h})
}

// ===== 焦点遍历序 =====

func TestA11yFocusOrderFollowsTreeOrder(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 8})
	inp := mkField("input", 120, 24)
	box := mkNode("rect", map[string]float64{"width": 40, "height": 20}) // 非可聚焦容器, 应被跳过
	btn := withClick(mkButton("确定"))
	cb := mkField("checkbox", 20, 20)
	mountChildren(root, inp, box, btn, cb)

	renderTree(root, 300, 200)

	order := a11yFocusOrder(root)
	want := []*GuiNode{inp, btn, cb}
	if len(order) != len(want) {
		t.Fatalf("焦点序长度 = %d, want %d (%v)", len(order), len(want), tagsOf(order))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("焦点序第 %d 位 = %s, want %s (整序 %v)", i, order[i].Tag, want[i].Tag, tagsOf(order))
		}
	}
}

func TestA11yTabIndexPositiveComesFirst(t *testing.T) {
	root := mkNode("column", nil)
	b1 := withClick(mkButton("一")) // tabIndex 缺省 = 0
	b2 := withClick(mkButton("二"))
	withNum(b2, "tabIndex", 1)
	b3 := withClick(mkButton("三")) // 0
	mountChildren(root, b1, b2, b3)

	renderTree(root, 300, 200)

	order := a11yFocusOrder(root)
	if len(order) != 3 || order[0] != b2 || order[1] != b1 || order[2] != b3 {
		t.Fatalf("正 tabIndex 应排在最前且保持 0 组树序, got %v", tagsOf(order))
	}
}

func TestA11yTabIndexNegativeStaysOutOfOrder(t *testing.T) {
	root := mkNode("column", nil)
	b1 := withClick(mkButton("一"))
	b2 := withClick(mkButton("二"))
	withNum(b2, "tabIndex", -1)
	mountChildren(root, b1, b2)

	renderTree(root, 300, 200)

	order := a11yFocusOrder(root)
	if len(order) != 1 || order[0] != b1 {
		t.Fatalf("tabIndex=-1 不该进遍历序, got %v", tagsOf(order))
	}
	if !b2.focusable() {
		t.Fatalf("tabIndex=-1 只影响遍历序, 不该改变可聚焦性")
	}
}

func TestA11yFocusableFalseAndHiddenExcluded(t *testing.T) {
	root := mkNode("column", nil)
	off := withClick(mkButton("跳过我"))
	withBool(off, "focusable", false)
	hidden := withClick(mkButton("看不见"))
	withStr(hidden, "aria-hidden", "true")
	hiddenProp := withClick(mkButton("hidden prop"))
	withBool(hiddenProp, "hidden", true)
	dead := withClick(mkButton("禁用"))
	withBool(dead, "disabled", true)
	ok := withClick(mkButton("留下"))
	mountChildren(root, off, hidden, hiddenProp, dead, ok)

	renderTree(root, 300, 260)

	order := a11yFocusOrder(root)
	if len(order) != 1 || order[0] != ok {
		t.Fatalf("只该剩一个可聚焦按钮, got %v", tagsOf(order))
	}
}

func TestA11yContainersAreNotFocusableByDefault(t *testing.T) {
	root := mkNode("column", nil)
	// 容器默认不进 Tab 序 —— 给布局盒子加停留点是键盘用户最常抱怨的那类自伤。
	inner := mkNode("row", map[string]float64{"width": 100, "height": 30})
	mountChildren(root, inner)

	renderTree(root, 200, 120)

	if len(a11yFocusOrder(root)) != 0 {
		t.Fatalf("容器默认不该进 Tab 序: %v", tagsOf(a11yFocusOrder(root)))
	}
	// 显式声明后可以进 (脚本说"这块该能 Tab 到"就该听它的)
	withBool(inner, "focusable", true)
	renderTree(root, 200, 120)
	if len(a11yFocusOrder(root)) != 1 {
		t.Fatalf("focusable={true} 的容器应进 Tab 序")
	}
}

func TestA11yFocusOrderSkipsUnlaidOutSubtree(t *testing.T) {
	root := mkNode("column", nil)
	// tabs 的非激活页是"整支不布局"的真实场景: 页的盒子被清零, 里面的按钮
	// 从未拿到过几何。只跳过页本身是不够的 —— 键盘用户会 Tab 进一个看不见
	// 的按钮。这里锁的就是"整支剪掉"。
	tb := mkNode("tabs", map[string]float64{"width": 240, "height": 120})
	withNum(tb, "value", 0)
	var pages []*GuiNode
	var pageBtns []*GuiNode
	for i := 0; i < 3; i++ {
		page := mkNode("tab", nil)
		withStr(page, "title", "页"+string(rune('甲'+i)))
		pb := withClick(mkButton("页内按钮"))
		mountChildren(page, pb)
		pages = append(pages, page)
		pageBtns = append(pageBtns, pb)
	}
	mountChildren(tb, pages...)
	live := withClick(mkButton("页外按钮"))
	mountChildren(root, tb, live)

	renderTree(root, 300, 200)

	if pages[1].Box != (Rect{}) || pages[2].Box != (Rect{}) {
		t.Fatalf("非激活页的盒子该被清零: %v %v", pages[1].Box, pages[2].Box)
	}
	order := a11yFocusOrder(root)
	// tabs 本身 + 激活页里的按钮 + 页外按钮; 非激活页的两个按钮必须不在。
	if len(order) != 3 {
		t.Fatalf("焦点序该只含 tabs + 激活页按钮 + 页外按钮, got %v", tagsOf(order))
	}
	for _, n := range order {
		if n == pageBtns[1] || n == pageBtns[2] {
			t.Fatalf("非激活页里的按钮不该进焦点序")
		}
	}
	seen := map[*GuiNode]bool{}
	for _, n := range order {
		seen[n] = true
	}
	if !seen[tb] || !seen[pageBtns[0]] || !seen[live] {
		t.Fatalf("焦点序缺项: %v", tagsOf(order))
	}
}

// ===== Tab / Shift+Tab 遍历 =====

func mkTabTree() (*GuiNode, []*GuiNode) {
	root := mkNode("column", map[string]float64{"padding": 6})
	var btns []*GuiNode
	for i, label := range []string{"一", "二", "三"} {
		b := withClick(mkButton(label))
		withStr(b, "tagName", label+"的名字") // 只用来区分, 不参与判定
		_ = i
		btns = append(btns, b)
	}
	mountChildren(root, btns[0], btns[1], btns[2])
	return root, btns
}

func TestA11yTabTraversalAndWrap(t *testing.T) {
	root, btns := mkTabTree()
	fake, a := mountTestApp(t, root, 300, 200)

	if a.focused != nil {
		t.Fatalf("初始不该有元素级焦点")
	}
	// 正向: 一 → 二 → 三 → 绕回一
	for _, want := range []*GuiNode{btns[0], btns[1], btns[2], btns[0]} {
		pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
		if a.focused != want {
			t.Fatalf("Tab 后焦点 = %v, want %v", a.focused, want)
		}
	}
	// 反向 (Shift+Tab): 从"一"绕到"三"
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab", Shift: true})
	if a.focused != btns[2] {
		t.Fatalf("Shift+Tab 应反向绕回最后一个, got %v", a.focused)
	}
}

func TestA11yTabFromUnfocusedStateEntersFirst(t *testing.T) {
	root, btns := mkTabTree()
	fake, a := mountTestApp(t, root, 300, 200)

	// 焦点在根 (点了空白的语义) 时, Tab 从序首进 —— 不该被"根自己"卡住。
	a.setFocus(root)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != btns[0] {
		t.Fatalf("焦点在根时 Tab 应进序首, got %v", a.focused)
	}
}

func TestA11yTabWithEmptyOrderIsNotSwallowed(t *testing.T) {
	root := mkNode("column", nil)
	mountChildren(root, mkNode("rect", map[string]float64{"width": 10, "height": 10}))
	fake, a := mountTestApp(t, root, 200, 120)

	scripted := &a11yHit{}
	root.Props["onKeyDown"] = scripted.handler()

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if scripted.n != 1 {
		t.Fatalf("遍历序为空时 Tab 该照旧派发给脚本, got %d 次", scripted.n)
	}
}

// ===== Enter / Space 激活 =====

func TestA11yEnterAndSpaceActivateButton(t *testing.T) {
	root, btns := mkTabTree()
	clicks := &a11yHit{}
	btns[0].Props["onClick"] = clicks.handler()
	fake, a := mountTestApp(t, root, 300, 200)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != btns[0] {
		t.Fatalf("Tab 应聚焦第一个按钮, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: " "})
	if clicks.n != 2 {
		t.Fatalf("Enter/空格各该触发一次 onClick, got %d", clicks.n)
	}
}

func TestA11yEnterWithoutHandlerFallsThroughToScript(t *testing.T) {
	root := mkNode("column", nil)
	// 刻意**不挂 onClick**: 此时 Enter 没有可激活的东西, 按键该继续派发给
	// 脚本的 onKeyDown (消费纪律: 只在真的做了事的时候消费)。
	btn := mkButton("没有处理器")
	mountChildren(root, btn)
	seen := &a11yHit{}
	btn.Props["onKeyDown"] = seen.handler()

	fake, a := mountTestApp(t, root, 300, 200)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != btn {
		t.Fatalf("Tab 应聚焦按钮, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if seen.n != 1 {
		t.Fatalf("没有 onClick 时按键该继续派发给脚本 onKeyDown, got %d", seen.n)
	}
}

func TestA11yToggleControlsUseSharedClickPath(t *testing.T) {
	// checkbox / switch / radio 的取反与选中都挂在 onClick 上 (model 指令
	// 展开的结果), 所以"键盘激活"与"鼠标点击"必然走同一条出口 —— 这个用例
	// 锁的就是"没有各写一份键盘逻辑"。
	root := mkNode("column", nil)
	cb := mkField("checkbox", 20, 20)
	sw := mkField("switch", 36, 20)
	mountChildren(root, cb, sw)

	cbHit, swHit := &a11yHit{}, &a11yHit{}
	cb.Props["onClick"] = cbHit.handler()
	sw.Props["onClick"] = swHit.handler()

	fake, a := mountTestApp(t, root, 200, 120)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: " "})
	if a.focused != cb || cbHit.n != 1 {
		t.Fatalf("空格应切换 checkbox (focus=%v, hits=%d)", a.focused, cbHit.n)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if a.focused != sw || swHit.n != 1 {
		t.Fatalf("Enter 应切换 switch (focus=%v, hits=%d)", a.focused, swHit.n)
	}
}

// ===== 方向键: radio 组 / slider / rating / tabs / pagination =====

func TestA11yRadioGroupIsOneTabStopWithArrowNavigation(t *testing.T) {
	root := mkNode("column", nil)
	r1 := mkField("radio", 20, 20)
	r2 := mkField("radio", 20, 20)
	r3 := mkField("radio", 20, 20)
	withBool(r1, "checked", true)
	mountChildren(root, r1, r2, r3)

	hits := []*a11yHit{{}, {}, {}}
	for i, r := range []*GuiNode{r1, r2, r3} {
		r.Props["onClick"] = hits[i].handler()
	}

	fake, a := mountTestApp(t, root, 220, 160)

	order := a11yFocusOrder(root)
	if len(order) != 1 || order[0] != r1 {
		t.Fatalf("一组 radio 只占一个 Tab 停留点 (选中项), got %v", tagsOf(order))
	}

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != r1 {
		t.Fatalf("Tab 应落在选中的 radio 上, got %v", a.focused)
	}
	// 方向键: 焦点移动 + 选中 (ARIA "selection follows focus")
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowDown"})
	if a.focused != r2 || hits[1].n != 1 {
		t.Fatalf("↓ 应移到第二项并选中 (focus=%v hits=%d)", a.focused, hits[1].n)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowUp"})
	if a.focused != r1 || hits[0].n != 1 {
		t.Fatalf("↑ 应回到第一项并选中 (focus=%v hits=%d)", a.focused, hits[0].n)
	}
	// 绕回: 在最后一项继续↓ → 第一项
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowUp"})
	if a.focused != r3 {
		t.Fatalf("首项↑应绕到末项, got %v", a.focused)
	}
}

func TestA11yRadioGroupKeyedByNameCrossesContainers(t *testing.T) {
	root := mkNode("column", nil)
	colA := mkNode("column", nil)
	colB := mkNode("column", nil)
	ra := mkField("radio", 20, 20)
	rb := mkField("radio", 20, 20)
	for _, r := range []*GuiNode{ra, rb} {
		withStr(r, "name", "shape")
	}
	mountChildren(colA, ra)
	mountChildren(colB, rb)
	mountChildren(root, colA, colB)

	renderTree(root, 200, 160)

	// name 相同的 radio 即使不在同一个父容器里也算一组 (与 HTML 一致)
	if len(a11yRadioGroup(root, ra)) != 2 {
		t.Fatalf("同名 radio 应跨容器成组")
	}
	if len(a11yFocusOrder(root)) != 1 {
		t.Fatalf("同名 radio 组只该占一个 Tab 停留点, got %v", tagsOf(a11yFocusOrder(root)))
	}
}

func TestA11ySliderArrowKeysStepValue(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "min": 0, "max": 10, "step": 2, "value": 4})
	mountChildren(root, sl)

	var got []float64
	sl.Props["onInput"] = object.NewBuiltin("sliderInput", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("value"); ok {
					if n, ok := v.(*object.Number); ok {
						got = append(got, n.Value)
					}
				}
			}
		}
		return object.UndefinedSingleton
	})

	fake, a := mountTestApp(t, root, 260, 120)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != sl {
		t.Fatalf("Tab 应聚焦滑块, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowUp"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	// 起点取"上一次派发出去的值"(slideVal), 不是恒为 4 的 value prop ——
	// 脚本不回写时 prop 不动, 拿它当起点会让连按方向键永远停在 4±2。
	if len(got) != 3 || got[0] != 6 || got[1] != 8 || got[2] != 6 {
		t.Fatalf("滑块方向键派发序列 = %v, want [6 8 6]", got)
	}
	// 一路按到 0 之后不再派发 (但按键仍被消费, 不落到脚本身上)
	for i := 0; i < 3; i++ {
		pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	}
	if len(got) != 6 || got[5] != 0 {
		t.Fatalf("应依次派发 4/2/0, got %v", got)
	}
	before := len(got)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	if len(got) != before {
		t.Fatalf("已在下界时不该继续派发, got %v", got)
	}
}

func TestA11yRatingArrowKeysAdjustStars(t *testing.T) {
	root := mkNode("column", nil)
	rt := mkNode("rating", map[string]float64{"width": 100, "height": 20, "max": 5, "value": 3})
	mountChildren(root, rt)

	var vals []float64
	rt.Props["onChange"] = object.NewBuiltin("ratingChange", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("value"); ok {
					if n, ok := v.(*object.Number); ok {
						vals = append(vals, n.Value)
					}
				}
			}
		}
		return object.UndefinedSingleton
	})

	fake, a := mountTestApp(t, root, 200, 120)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	want := []float64{4, 2}
	for i := range want {
		if i >= len(vals) || vals[i] != want[i] {
			t.Fatalf("星级方向键派发序列 = %v, want %v", vals, want)
		}
	}
}

func TestA11yTabsArrowKeysSwitchPages(t *testing.T) {
	root := mkNode("column", nil)
	tb := mkNode("tabs", map[string]float64{"width": 240, "height": 120})
	withNum(tb, "value", 0)
	for _, title := range []string{"甲", "乙", "丙"} {
		page := mkNode("tab", nil)
		withStr(page, "title", title)
		mountChildren(tb, page)
	}
	mountChildren(root, tb)

	var idx []float64
	tb.Props["onChange"] = object.NewBuiltin("tabsChange", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("index"); ok {
					if n, ok := v.(*object.Number); ok {
						idx = append(idx, n.Value)
					}
				}
			}
		}
		return object.UndefinedSingleton
	})

	fake, a := mountTestApp(t, root, 260, 160)

	// tabs 是复合控件: 一个 Tab 停留点, 页签之间用方向键走
	if len(a11yFocusOrder(root)) != 1 {
		t.Fatalf("tabs 只该占一个 Tab 停留点, got %v", tagsOf(a11yFocusOrder(root)))
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != tb {
		t.Fatalf("Tab 应聚焦 tabs, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowLeft"})
	// tabs 在这个用例里是**受控**的 (value=0 且脚本不回写), 所以每次按键都
	// 从 0 起算: → 给 1; ← 从 0 绕回最后一页给 2; 再 ← 还是 2。
	want := []float64{1, 2, 2}
	for i := range want {
		if i >= len(idx) || idx[i] != want[i] {
			t.Fatalf("tabs 方向键派发序列 = %v, want %v", idx, want)
		}
	}
}

func TestA11yArrowKeyWithCtrlIsLeftToShortcuts(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "min": 0, "max": 10, "value": 4})
	mountChildren(root, sl)
	hit := &a11yHit{}
	sl.Props["onInput"] = hit.handler()

	fake, a := mountTestApp(t, root, 260, 120)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowRight", Ctrl: true})
	if hit.n != 0 {
		t.Fatalf("带 Ctrl 的组合键属于快捷键的地盘, 不该改控件值")
	}
	// 而没注册成快捷键时, 按键要继续给脚本 (这里没有 onKeyDown, 什么也不发生)
	if a.focused != sl {
		t.Fatalf("焦点不该被带走")
	}
}

// ===== 焦点陷阱与校正 =====

func TestA11yFocusTrapInsideModal(t *testing.T) {
	root := mkNode("column", nil)
	outside := withClick(mkButton("外面"))
	dlg := &GuiNode{Tag: "dialog", Props: map[string]object.Value{}}
	withBoolProp(dlg, "open", true)
	card := mkNode("column", map[string]float64{"padding": 10})
	inside := withClick(mkButton("里面"))
	mountChildren(card, inside)
	mountChildren(dlg, card)
	mountChildren(root, outside, dlg)

	renderTree(root, 300, 200)

	order := a11yFocusOrder(root)
	if len(order) != 1 || order[0] != inside {
		t.Fatalf("弹层打开时遍历序只该含弹层内的控件, got %v", tagsOf(order))
	}

	// 弹层关掉后遍历序恢复 (节点仍在树上, 只是 open=false)
	withBoolProp(dlg, "open", false)
	renderTree(root, 300, 200)
	order = a11yFocusOrder(root)
	if len(order) != 1 || order[0] != outside {
		t.Fatalf("弹层关闭后应只剩页面上的控件, got %v", tagsOf(order))
	}
}

func TestA11yReconcileMovesFocusOutOfClosedDialog(t *testing.T) {
	root := mkNode("column", nil)
	outside := withClick(mkButton("外面"))
	dlg := &GuiNode{Tag: "dialog", Props: map[string]object.Value{}}
	withBoolProp(dlg, "open", true)
	card := mkNode("column", map[string]float64{"padding": 10})
	inside := withClick(mkButton("里面"))
	mountChildren(card, inside)
	mountChildren(dlg, card)
	mountChildren(root, outside, dlg)

	fake, a := mountTestApp(t, root, 300, 200)
	a.setFocus(inside)

	// 关掉弹层 (脚本把 open 写成 false) → 焦点节点整支不再绘制, 焦点悬空
	withBoolProp(dlg, "open", false)
	markFullDirtyFor(root)
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if a.focused != outside {
		t.Fatalf("弹层关闭后焦点该落到页面上第一个可聚焦控件, got %v", a.focused)
	}
}

func TestA11yReconcileMovesFocusOffDisabledControl(t *testing.T) {
	root, btns := mkTabTree()
	fake, a := mountTestApp(t, root, 300, 200)
	a.setFocus(btns[0])

	withBool(btns[0], "disabled", true)
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if a.focused != btns[1] {
		t.Fatalf("焦点落在被禁用的控件上时应交给下一个, got %v", a.focused)
	}
}

func TestA11yReconcileLeavesValidFocusAlone(t *testing.T) {
	root, btns := mkTabTree()
	fake, a := mountTestApp(t, root, 300, 200)
	a.setFocus(btns[2])
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if a.focused != btns[2] {
		t.Fatalf("站得住的焦点不该被校正挪走, got %v", a.focused)
	}
}

// ===== aria 语义 =====

func TestA11yImplicitRoleAndOverride(t *testing.T) {
	cases := []struct {
		tag  string
		want string
	}{
		{"button", "button"}, {"input", "textbox"}, {"search", "searchbox"},
		{"textarea", "textbox"}, {"select", "combobox"}, {"checkbox", "checkbox"},
		{"radio", "radio"}, {"switch", "switch"}, {"slider", "slider"},
		{"rating", "slider"}, {"tabs", "tablist"}, {"tab", "tab"},
		{"pagination", "navigation"}, {"progress", "progressbar"},
		{"dialog", "dialog"}, {"drawer", "dialog"}, {"alert", "alert"},
		{"toast", "status"}, {"table", "table"}, {"tree", "tree"},
		{"icon", "img"}, {"scroll", "region"},
		{"datepicker", "combobox"}, {"colorpicker", "combobox"},
		{"upload", "group"}, {"label", "label"}, {"form", "form"},
	}
	for _, c := range cases {
		n := &GuiNode{Tag: c.tag, Props: map[string]object.Value{}}
		if got := n.AriaRole(); got != c.want {
			t.Errorf("<%s> 隐式 role = %q, want %q", c.tag, got, c.want)
		}
	}
	// 表格/树/菜单的**内部构造**标签 (脚本写不到) 不在隐式表里: 它们的语义
	// 由宿主组件承载 (table 是 table, 行/格不是独立的无障碍对象)。这里只
	// 断言取值稳定 (空串), 免得哪天有人给它们补了 role 而没同步文档。
	for _, tag := range []string{"table-row", "table-cell", "tree-row", "menu-popup"} {
		n := &GuiNode{Tag: tag, Props: map[string]object.Value{}}
		if got := n.AriaRole(); got != "" {
			t.Errorf("内部构造标签 <%s> 不该有独立 role, got %q", tag, got)
		}
	}

	btn := &GuiNode{Tag: "button", Props: map[string]object.Value{}}
	withStr(btn, "role", "link")
	if got := btn.AriaRole(); got != "link" {
		t.Fatalf("显式 role 应覆盖隐式值, got %q", got)
	}
	withStr(btn, "role", "Role-That-Does-Not-Exist")
	if got := btn.AriaRole(); got != "button" {
		t.Fatalf("非法 role 应回落隐式值, got %q", got)
	}
}

func TestA11yAccessibleNameOrder(t *testing.T) {
	n := withClick(mkButton("确定"))
	if got := n.AriaName(); got != "确定" {
		t.Fatalf("文本内容应作为无障碍名, got %q", got)
	}
	withStr(n, "title", "确认提交")
	if got := n.AriaName(); got != "确认提交" {
		t.Fatalf("title 应优先于文本内容, got %q", got)
	}
	withStr(n, "aria-label", "提交表单")
	if got := n.AriaName(); got != "提交表单" {
		t.Fatalf("aria-label 优先级最高, got %q", got)
	}
	// 字段类: 没有文本内容时退回 placeholder
	inp := mkField("input", 100, 24)
	withStr(inp, "placeholder", "请输入姓名")
	if got := inp.AriaName(); got != "请输入姓名" {
		t.Fatalf("字段的 placeholder 应作为兜底名, got %q", got)
	}
	// 什么都没有 → 空串 (而不是编一个假名字)
	if got := (&GuiNode{Tag: "icon", Props: map[string]object.Value{}}).AriaName(); got != "" {
		t.Fatalf("无可读名字时该返回空串, got %q", got)
	}
}

func TestA11yUnknownAriaPropWarnsOnce(t *testing.T) {
	resetA11yWarnings()
	resetWarnRing()
	t.Cleanup(func() { resetA11yWarnings(); resetWarnRing() })

	props := object.NewObject()
	props.SetProperty("aria-lable", object.NewString("提交")) // 拼错的 aria-label
	a11yCheckProps("button", props)
	a11yCheckProps("button", props) // 第二次不该再记

	warns := warnSnapshot()
	if len(warns) != 1 {
		t.Fatalf("拼错的 aria-* 属性应告警且只告警一次, got %d 条: %v", len(warns), warns)
	}
	if !strings.Contains(warns[0].Text, "aria-lable") {
		t.Fatalf("告警内容该点名那个属性: %q", warns[0].Text)
	}

	// 合法属性不告警
	resetA11yWarnings()
	resetWarnRing()
	ok := object.NewObject()
	ok.SetProperty("aria-label", object.NewString("提交"))
	ok.SetProperty("aria-expanded", object.NewBoolean(false))
	a11yCheckProps("button", ok)
	if len(warnSnapshot()) != 0 {
		t.Fatalf("合法 aria-* 属性不该告警: %v", warnSnapshot())
	}
}

func TestA11yUnknownRoleWarnsAndFallsBack(t *testing.T) {
	resetA11yWarnings()
	resetWarnRing()
	t.Cleanup(func() { resetA11yWarnings(); resetWarnRing() })

	props := object.NewObject()
	props.SetProperty("role", object.NewString("buton")) // 拼错的 button
	a11yCheckProps("button", props)
	if len(warnSnapshot()) != 1 {
		t.Fatalf("非法 role 应告警一次, got %v", warnSnapshot())
	}
}

// ===== gx/a11y 模块 (对外形状) =====

func TestA11yModuleFocusOrderShape(t *testing.T) {
	src := `
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";
import { focusOrder, focusNext, focusPrev, focusNode, roles } from "gx/a11y";

const [q, setQ] = createSignal("");

const btn = h("button", { onClick: () => {}, "aria-label": "提交" }, "确定");

render(h("window", { title: "a11y", width: 320, height: 200 },
  h("column", { padding: 8, gap: 6 },
    h("input", { width: 160, height: 24, placeholder: "搜索", value: () => q(), onInput: (e) => setQ(e.value) }),
    btn,
    h("icon", { name: "close", size: 16, "aria-hidden": "true" })
  )
));

globalThis.probe = () => {
  globalThis.ORDER = focusOrder();
  globalThis.ROLES = roles();
  globalThis.NEXT = focusNext();
  globalThis.AFTER_NEXT = focusOrder().map((x) => x.focused);
  globalThis.PREV = focusPrev();
  globalThis.NODE_OK = focusNode(btn);
  globalThis.AFTER_NODE = focusOrder().map((x) => x.tag + ":" + x.focused);
};
`
	v, fake := evalUI(t, src)
	runPumpSteps(t, v, fake, []func(){
		func() {
			callGlobalInspect(t, v, "probe")
		},
		func() {
			order := jsArray(t, v, "ORDER")
			if len(order.Elements) != 2 {
				t.Fatalf("焦点序应含 input + button 两项, got %d", len(order.Elements))
			}
			first, ok := order.Elements[0].(*object.Object)
			if !ok {
				t.Fatalf("焦点序元素不是对象: %s", order.Elements[0].Type())
			}
			for _, key := range []string{"role", "name", "tag", "tabIndex", "disabled", "focused", "keyHint", "box"} {
				if _, ok := first.GetProperty(key); !ok {
					t.Fatalf("focusOrder() 结果缺字段 %q (字段名是 API, 见 docs/accessibility.md)", key)
				}
			}
			role := mustPropStr(t, first, "role")
			name := mustPropStr(t, first, "name")
			tag := mustPropStr(t, first, "tag")
			if tag != "input" || role != "textbox" || name != "搜索" {
				t.Fatalf("第一项 = {tag:%q role:%q name:%q}, want {input textbox 搜索}", tag, role, name)
			}
			btn, _ := order.Elements[1].(*object.Object)
			if got := mustPropStr(t, btn, "role"); got != "button" {
				t.Fatalf("第二项 role = %q, want button", got)
			}
			if got := mustPropStr(t, btn, "name"); got != "提交" {
				t.Fatalf("aria-label 应成为 button 的名字, got %q", got)
			}
			// aria-hidden 的图标不进序 (上面断言了只有两项, 这里再点一次原因)
			if len(order.Elements) != 2 {
				t.Fatalf("aria-hidden 节点不该进焦点序")
			}
			box, _ := first.GetProperty("box")
			if o, ok := box.(*object.Object); !ok {
				t.Fatalf("box 不是对象: %T", box)
			} else if _, ok := o.GetProperty("width"); !ok {
				t.Fatalf("box 缺 width")
			}
			// focusNext / focusPrev 与 Tab 走同一路径
			if b, _ := globalBool(t, v, "NEXT"); !b {
				t.Fatalf("focusNext() 应返回 true")
			}
			if b, _ := globalBool(t, v, "PREV"); !b {
				t.Fatalf("focusPrev() 应返回 true")
			}
			// focused 标记跟着焦点走 (focusNext 后第一项为 true)
			after := jsArray(t, v, "AFTER_NEXT")
			if len(after.Elements) != 2 {
				t.Fatalf("AFTER_NEXT 长度 = %d", len(after.Elements))
			}
			if b, ok := after.Elements[0].(*object.Object); ok {
				if f, ok := b.GetProperty("focused"); ok {
					if bv, ok := f.(*object.Boolean); !ok || !bv.Value {
						t.Fatalf("focusNext() 后第一项应标记 focused=true")
					}
				}
			}
			// focusNode(el) 是程序化聚焦 (等价于 DOM 的 el.focus())
			if b, _ := globalBool(t, v, "NODE_OK"); !b {
				t.Fatalf("focusNode(元素) 应返回 true")
			}
			if after := jsArrayStr(t, v, "AFTER_NODE"); len(after) != 2 || !strings.HasSuffix(after[1], ":true") {
				t.Fatalf("focusNode 后焦点该落在按钮上, got %v", after)
			}
			// roles() 是隐式 role 表, 文档/画廊共用一份口径
			rolesArr := jsArray(t, v, "ROLES")
			if len(rolesArr.Elements) < 20 {
				t.Fatalf("roles() 应返回整张隐式 role 表, got %d 行", len(rolesArr.Elements))
			}
		},
	})
}

// jsArrayStr 把全局字符串数组读成 Go 切片 (断言"标签:焦点"这类拼接值时用)。
func jsArrayStr(t *testing.T, v *vm.VM, name string) []string {
	t.Helper()
	arr := jsArray(t, v, name)
	out := make([]string, 0, len(arr.Elements))
	for _, el := range arr.Elements {
		s, ok := el.(*object.String)
		if !ok {
			t.Fatalf("全局 %s 含非字符串元素: %s", name, el.Type())
		}
		out = append(out, s.Value)
	}
	return out
}

func mustPropStr(t *testing.T, o *object.Object, name string) string {
	t.Helper()
	v, ok := o.GetProperty(name)
	if !ok {
		t.Fatalf("缺字段 %q", name)
	}
	s, ok := v.(*object.String)
	if !ok {
		t.Fatalf("字段 %q 不是字符串: %s", name, v.Type())
	}
	return s.Value
}

// tagsOf 把节点序列摊成标签序列 (失败信息里用)。
func tagsOf(list []*GuiNode) []string {
	out := make([]string, 0, len(list))
	for _, n := range list {
		out = append(out, n.Tag)
	}
	return out
}

// ===== 演示脚本: 纯键盘走完整流程 (docs/accessibility.md §7.1 的回归) =====

// TestA11yFormDemoKeyboardWalkthrough 在演示脚本 (testdata/a11y_form_demo.js) 上
// 跑一遍 T10 的 DoD: **不碰鼠标**完成"填表 → 提交 → 关弹窗"。逐条对应验收清单:
//
//	#1–#2  Tab 进序并按树序前进 —— 每一站都断言 role 与无障碍名;
//	#4     打字写进当前字段;
//	#11    焦点在输入框里按 Enter 提交表单 (值由**内核**收集, 不是脚本自己拼的);
//	#13    弹层里反复 Tab 出不去 (焦点陷阱);
//	#14    Esc 关闭弹层;
//	#15    关闭后焦点自动校正到弹层外的第一个可聚焦控件。
//
// 为什么要有这条"整条链路"的用例: 上面每一条规则在纯 Go 层都有更精确的用例
// (Tab 循环 / 陷阱 / 校正 / 回车提交各一个), 但 DoD 问的是"合起来能不能把事做完" ——
// 只测部件时, "字段顺序没问题、每个控件也能用键盘操作、但整体走一遍就是走不通"
// 这种组合缺陷是抓不到的。
//
// 与 model_demo 同理: 假 Surface 的尺寸决定布局尺寸 (fakeFactory 不读
// WindowConfig), 所以这里手工设成脚本里 <window> 的尺寸 —— 顺带把"内容溢出窗口"
// 也断言掉: 窗口不是滚动容器, 溢出的字段既画不出来也点不中, 也就不进焦点序,
// 于是"越界字段被静默跳过"最容易被误判成 Tab 遍历的 bug。
func TestA11yFormDemoKeyboardWalkthrough(t *testing.T) {
	const demoW, demoH = 660, 620

	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "a11y_form_demo.js"))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	fake.w, fake.h = demoW, demoH
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	has := func(frag string) bool { return textContainsAny(root, frag) }
	dialog := func() *GuiNode { return findFirst(root, "dialog") }

	steps := []func(){
		// 0) 初态: 没有焦点, 还没提交; 顺带确认内容没溢出窗口
		func() {
			if !has("焦点: (无)") {
				t.Fatalf("初态该没有焦点: %v", viewTexts(root))
			}
			if !has("(还没提交)") {
				t.Fatalf("初态该是" + "还没提交" + ": %v", viewTexts(root))
			}
			bottom := 0
			for _, n := range allNodes(root) {
				if n == root {
					continue
				}
				if b := n.Box.Y + n.Box.H; b > bottom {
					bottom = b
				}
			}
			if bottom > demoH-14 {
				t.Fatalf("内容溢出窗口: 底部 y=%d > 可用高 %d (溢出的字段会静默退出焦点序)",
					bottom, demoH-14)
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Tab"})
		},
		// 1) Tab 进序首: 姓名字段; 直接打字
		func() {
			if !has(`焦点: textbox "姓名"`) {
				t.Fatalf("首次 Tab 该停在姓名输入框: %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "a"})
		},
		// 2) 是字段而不是快照: 显示与 signal 都跟着走; 回车提交
		func() {
			if !has("= a") {
				t.Fatalf("打字没写回 signal: %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Enter"})
		},
		// 3) 提交成功: 值来自内核的 formValues (只收带 name 的字段)
		func() {
			if !has("name=a") || !has("city=beijing") || !has("plan=free") {
				t.Fatalf("回车没有提交表单 (或取值口径不对): %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Tab"})
		},
	}

	// 继续按声明顺序走完剩下的站点 —— 每一站都断言 role + 无障碍名,
	// 于是"漏一站 / 顺序错 / 名字丢失"都会在正确的站点上失败。
	hops := []struct{ what, role string }{
		{"备注", "textbox"},
		{"城市", "combobox"},
		{"截止日", "combobox"},
		{"主题色", "combobox"},
		{"音量", "slider"},
		{"附件", "group"},
		{"同意条款", "checkbox"},
		{"通知", "switch"},
		{"套餐", "radio"}, // 一组单选只占一个停留点
		{"提交", "button"},
		{"打开确认弹窗", "button"},
	}
	for i, h := range hops {
		i, h := i, h
		steps = append(steps, func() {
			want := `焦点: ` + h.role + ` "` + h.what + `"`
			if !has(want) {
				t.Fatalf("第 %d 站该是 %s, 实际: %v", i+2+1, want, viewTexts(root))
			}
			if i == len(hops)-1 {
				fake.push(Event{Kind: EventKeyDown, Key: "Enter"}) // 打开弹层
				return
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Tab"})
		})
	}

	steps = append(steps,
		// 弹层打开: 焦点进弹层内的第一个可聚焦控件 (不是留在被遮住的触发按钮上)
		func() {
			if !dialog().overlayVisible() {
				t.Fatalf("Enter 没打开弹层")
			}
			if !has(`焦点: button "确认"`) {
				t.Fatalf("弹层打开后焦点该落在弹层内: %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Tab"})
		},
		func() {
			if !has(`焦点: button "取消"`) {
				t.Fatalf("弹层内 Tab 该走到下一个按钮: %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Tab"})
		},
		// 焦点陷阱: 绕回弹层内第一个, 而不是跑到弹层外的"姓名"
		func() {
			if !has(`焦点: button "确认"`) {
				t.Fatalf("Tab 跑出弹层了 (焦点陷阱失效): %v", viewTexts(root))
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Escape"})
		},
		// Esc 关闭 + 焦点校正: 弹层外的序首 (姓名)
		func() {
			if dialog().overlayVisible() {
				t.Fatalf("Esc 没关闭弹层")
			}
			if !has(`焦点: textbox "姓名"`) {
				t.Fatalf("关闭后焦点该校正到弹层外的第一个可聚焦控件: %v", viewTexts(root))
			}
		},
	)

	// 每一步只投 1 个事件 (见下面 pump 里的说明), 所以这里不用 runPumpSteps:
	// 它每轮都补一个"无害唤醒", 而本用例的步骤**自己**都要投事件 ⇒ 一轮 2 个。
	// 假 Surface 的 arrived 通道容量是 16 且每轮只被消耗 1 个 token, 19 步下来
	// 会堵在第 17 步的 push 上 (整用例挂到超时 —— 本次真踩过)。
	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		if round-1 < len(steps) {
			before := len(fake.events)
			steps[round-1]()
			if len(fake.events) == before {
				fake.push(Event{Kind: EventMouseLeave})
			}
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
}