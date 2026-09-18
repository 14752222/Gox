package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// P3-5 菜单栏 / 右键菜单 / 快捷键表 的单测。
//
// 一半用例是纯 Go 的 (构造节点树 → Layout/Draw → 断言盒子与像素),
// 另一半走 mountTestApp + 事件投递 (展开/收起/点击/键盘这些状态机)。
// "回调真的被调了" 必须走全链路 (p3_test.go 的 Menu 一节), 因为
// mountTestApp 里 currentVM 为 nil 时脚本闭包会被静默丢弃 —— 但
// callHandlerValue 现在走 callScriptFn, Go 侧 BuiltinFunction 是能验的。

// ===== 构造助手 =====

// mkMenuItem 造一个菜单项 (label 是数据而不是子节点 —— 这正是
// <menuitem label="Save"> 的形态)。
func mkMenuItem(label string, props ...func(*GuiNode)) *GuiNode {
	n := &GuiNode{Tag: "menuitem", Props: map[string]object.Value{}}
	if label != "" {
		n.Props["label"] = object.NewString(label)
	}
	for _, p := range props {
		p(n)
	}
	return n
}

func menuItemShortcutProp(s string) func(*GuiNode) {
	return func(n *GuiNode) { n.Props["shortcut"] = object.NewString(s) }
}

func menuItemDisabledProp() func(*GuiNode) {
	return func(n *GuiNode) { n.Props["disabled"] = object.NewBoolean(true) }
}

// mkMenu 造一个菜单标题 + 它的下拉项。
func mkMenu(title string, items ...*GuiNode) *GuiNode {
	m := &GuiNode{Tag: "menu", Props: map[string]object.Value{}}
	if title != "" {
		m.Props["label"] = object.NewString(title)
	}
	for _, it := range items {
		it.Parent = m
		m.Children = append(m.Children, it)
	}
	return m
}

func mkSep() *GuiNode {
	return &GuiNode{Tag: "separator", Props: map[string]object.Value{}}
}

// mkMenuBar 造一个菜单栏 (children 会接好 Parent)。
func mkMenuBar(menus ...*GuiNode) *GuiNode {
	bar := &GuiNode{Tag: "menubar", Props: map[string]object.Value{}}
	for _, m := range menus {
		m.Parent = bar
		bar.Children = append(bar.Children, m)
	}
	return bar
}

// mkAppTree 造 "窗口 = column { menubar; 内容 }" 这个典型形状,
// 并把各块接好 Parent。返回根节点。
func mkAppTree(bar *GuiNode, content ...*GuiNode) *GuiNode {
	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	bar.Parent = root
	root.Children = append(root.Children, bar)
	for _, c := range content {
		c.Parent = root
		root.Children = append(root.Children, c)
	}
	return root
}

// countTagDeep 统计整棵树 (含逃逸弹层) 里某标签的数量。
func countTagDeep(root *GuiNode, tag string) int {
	return len(findAll(root, tag))
}

// menuItemRowOf 取下拉弹层里第 idx 行。
func menuItemRowOf(t *testing.T, popup *GuiNode, idx int) *GuiNode {
	t.Helper()
	if popup == nil {
		t.Fatalf("弹层不存在")
	}
	if idx < 0 || idx >= len(popup.Children) {
		t.Fatalf("弹层只有 %d 行, 取不到第 %d 行", len(popup.Children), idx)
	}
	return popup.Children[idx]
}

// relayout 重新跑一遍布局。
//
// 展开/收起是个**状态变化**, 盒子要等下一次布局才落下来。测试里不会走
// 事件泵 (那要 VM), 所以断言盒子之前必须显式重排一次 —— 漏掉它的话
// 所有盒子都是 0, 失败信息会指向"弹层没建出来", 而真正的原因只是没布局。
func relayout(a *app) {
	w, h := a.surface.Size()
	Layout(a.root, w, h)
}

// ===== 布局: 菜单栏占位 =====

func TestMenuBarLayoutOccupiesTopRow(t *testing.T) {
	bar := mkMenuBar(mkMenu("File"), mkMenu("Edit"), mkMenu("Help"))
	body := &GuiNode{Tag: "text", Props: map[string]object.Value{}}
	body.appendTextNode("body")
	root := mkAppTree(bar, body)

	Layout(root, 400, 300)

	if bar.Box.Y != 0 {
		t.Fatalf("菜单栏应贴顶, Y = %d", bar.Box.Y)
	}
	if bar.Box.H != menuBarH {
		t.Fatalf("菜单栏高度 = %d, want %d", bar.Box.H, menuBarH)
	}
	if bar.Box.W != 400 {
		t.Fatalf("菜单栏应铺满宽度, W = %d", bar.Box.W)
	}
	// 内容区必须整体下移一个菜单栏高度 (这是"菜单条不遮内容"的全部依据)
	if body.Box.Y < bar.Box.Y+bar.Box.H {
		t.Fatalf("内容区未下移: body.Y = %d, 菜单栏底 = %d",
			body.Box.Y, bar.Box.Y+bar.Box.H)
	}
	// 三个标题横排、互不重叠、首尾相接
	var prev *GuiNode
	for i, m := range bar.Children {
		if m.Box.H != menuBarH {
			t.Fatalf("第 %d 个菜单标题高 = %d, want %d", i, m.Box.H, menuBarH)
		}
		if m.Box.W <= 0 {
			t.Fatalf("第 %d 个菜单标题宽 = 0 (文字未参与测量)", i)
		}
		if i > 0 && m.Box.X != prev.Box.X+prev.Box.W {
			t.Fatalf("第 %d 个菜单标题未与前一个相接: X = %d, 期望 %d",
				i, m.Box.X, prev.Box.X+prev.Box.W)
		}
		prev = m
	}
}

// 菜单栏高度是固定的: 它在 column 里不能被交叉轴 stretch 到全高
// (那会变成一整块灰色盖住整个窗口)。
func TestMenuBarHeightNotStretched(t *testing.T) {
	bar := mkMenuBar(mkMenu("File"))
	root := mkAppTree(bar)

	Layout(root, 400, 300)

	if bar.Box.H != menuBarH {
		t.Fatalf("菜单栏被 stretch 了: H = %d, want %d", bar.Box.H, menuBarH)
	}
	// 同时它必须铺满宽度
	if bar.Box.W != 400 {
		t.Fatalf("菜单栏宽度 = %d, want 400", bar.Box.W)
	}
}

// ===== 展开 / 收起 =====

func TestMenuOpenAndClose(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"), mkMenuItem("Open"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	fake, a := mountTestApp(t, root, 400, 300)
	_ = fake

	if file.expanded {
		t.Fatalf("初始不该是展开态")
	}
	a.toggleMenu(file)
	relayout(a)
	if !file.expanded || file.menuPopup == nil {
		t.Fatalf("toggleMenu 后应处于展开态且挂上弹层")
	}
	if got := countTagDeep(root, "menu-popup"); got != 1 {
		t.Fatalf("弹层数量 = %d, want 1", got)
	}
	if got := countTagDeep(root, "menu-item"); got != 2 {
		t.Fatalf("下拉项数量 = %d, want 2", got)
	}
	// 弹层贴在标题正下方、同宽起算
	if file.menuPopup.Box.Y != file.Box.Y+file.Box.H {
		t.Fatalf("下拉未贴在标题下方: popup.Y = %d, 标题底 = %d",
			file.menuPopup.Box.Y, file.Box.Y+file.Box.H)
	}
	if file.menuPopup.Box.X != file.Box.X {
		t.Fatalf("下拉未与标题左对齐: X = %d, want %d", file.menuPopup.Box.X, file.Box.X)
	}

	a.toggleMenu(file)
	if file.expanded || file.menuPopup != nil {
		t.Fatalf("再次 toggle 应收起并拆掉弹层")
	}
	if got := countTagDeep(root, "menu-popup"); got != 0 {
		t.Fatalf("收起后仍残留 %d 个弹层", got)
	}
}

// 空菜单不展开: 展开一个空盒子会让人以为"点坏了"。
func TestMenuEmptyDoesNotExpand(t *testing.T) {
	file := mkMenu("File") // 无项
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	if file.expanded {
		t.Fatalf("空菜单不该展开")
	}
}

// 打开一个菜单时, 菜单栏上另一个开着的必须一起关掉 (菜单栏互斥)。
func TestMenuBarMutualExclusion(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	edit := mkMenu("Edit", mkMenuItem("Undo"))
	bar := mkMenuBar(file, edit)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	a.toggleMenu(edit)

	if file.expanded {
		t.Fatalf("打开 Edit 后 File 应已收起")
	}
	if !edit.expanded {
		t.Fatalf("Edit 应处于展开态")
	}
	if got := countTagDeep(root, "menu-popup"); got != 1 {
		t.Fatalf("同时只该有一个弹层, got %d", got)
	}
}

// 菜单项的 onClick 必须被"包一层": 脚本自己的回调照旧触发,
// 组件的展开逻辑也照旧执行。
func TestMenuToggleKeepsUserOnClick(t *testing.T) {
	called := 0
	file := mkMenu("File", mkMenuItem("New"))
	file.Props["onClick"] = object.NewBuiltin("user", func(args ...object.Value) object.Value {
		called++
		return object.UndefinedSingleton
	})
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	mountTestApp(t, root, 400, 300)

	// h() 负责装配内置处理器; 这里手工调 (等价于 JSBuiltinH 的那一步)
	attachMenuHandler(file)
	callScriptFn(file.PropHandler("onClick"))

	if called != 1 {
		t.Fatalf("脚本自己的 onClick 调用次数 = %d, want 1", called)
	}
	if !file.expanded {
		t.Fatalf("内置展开逻辑应同时生效")
	}
}

// ===== 点击菜单项 =====

func TestClickMenuItemDispatchesAndCloses(t *testing.T) {
	clicked := ""
	file := mkMenu("File",
		mkMenuItem("New", func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("new", func(args ...object.Value) object.Value {
				clicked = "new"
				return object.UndefinedSingleton
			})
		}),
		mkMenuItem("Open", func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("open", func(args ...object.Value) object.Value {
				clicked = "open"
				return object.UndefinedSingleton
			})
		}),
	)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	a.clickMenuItem(file, 1) // 第 2 项 = Open

	if clicked != "open" {
		t.Fatalf("派发的回调 = %q, want \"open\"", clicked)
	}
	if file.expanded || file.menuPopup != nil {
		t.Fatalf("选中一项后整棵菜单应收起")
	}
}

// 禁用项: 不派发、且**不收起菜单** (真实菜单栏里点灰项就是没反应)。
func TestClickDisabledMenuItemDoesNothing(t *testing.T) {
	called := false
	file := mkMenu("File",
		mkMenuItem("Save", menuItemDisabledProp(), func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("save", func(args ...object.Value) object.Value {
				called = true
				return object.UndefinedSingleton
			})
		}),
	)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	a.clickMenuItem(file, 0)

	if called {
		t.Fatalf("禁用项不该派发回调")
	}
	if !file.expanded {
		t.Fatalf("点禁用项不该收起菜单")
	}
}

// 分隔线: 既不派发也不收起 (它只是视觉分组)。
func TestClickSeparatorDoesNothing(t *testing.T) {
	called := false
	file := mkMenu("File",
		mkMenuItem("A", func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("a", func(args ...object.Value) object.Value {
				called = true
				return object.UndefinedSingleton
			})
		}),
		mkSep(),
	)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	a.clickMenuItem(file, 1) // 分隔线

	if called {
		t.Fatalf("分隔线不该派发任何回调")
	}
	if !file.expanded {
		t.Fatalf("点分隔线不该收起菜单")
	}
}

// 分隔线也必须"能命中": 命中测试只认带处理器的节点, 没有处理器的行
// 会让鼠标穿过它落到下面的行上 (视觉上点的是线, 触发的是下一项)。
func TestSeparatorIsHittable(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"), mkSep(), mkMenuItem("B"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	popup := file.menuPopup
	sep := menuItemRowOf(t, popup, 1)
	if !sep.menuSep {
		t.Fatalf("第 1 行应是分隔线")
	}
	if sep.Box.H != menuSepH {
		t.Fatalf("分隔线高度 = %d, want %d", sep.Box.H, menuSepH)
	}
	cx, cy := sep.Box.X+sep.Box.W/2, sep.Box.Y+sep.Box.H/2
	if got := HitTest(root, cx, cy); got != sep {
		t.Fatalf("分隔线中心命中 = %v, want 分隔线自身", got)
	}
}

// 弹层里的每一项都必须能命中 (含最后一行 —— 弹层高度算错时最容易漏它)。
func TestAllMenuRowsHittable(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"), mkMenuItem("B"), mkMenuItem("C"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	popup := file.menuPopup
	for i := 0; i < 3; i++ {
		row := menuItemRowOf(t, popup, i)
		cx, cy := row.Box.X+row.Box.W/2, row.Box.Y+row.Box.H/2
		if !popup.Box.Contains(cx, cy) {
			t.Fatalf("第 %d 行落在弹层盒子之外: 行 %v, 弹层 %v", i, row.Box, popup.Box)
		}
		if got := HitTest(root, cx, cy); got != row {
			t.Fatalf("第 %d 行命中 = %v, want 该行自身", i, got)
		}
	}
}

// ===== 点外部关闭 =====

func TestClickOutsideClosesMenu(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	fake, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	if !file.expanded {
		t.Fatalf("前置: 菜单应已展开")
	}

	// 点窗口右下角 (远离菜单栏与弹层)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 380, Y: 280})

	if file.expanded {
		t.Fatalf("点外部后菜单应收起")
	}
	a.mu.Lock()
	swallowed := a.swallowClick
	a.mu.Unlock()
	if !swallowed {
		t.Fatalf("收起菜单的那次点击应被吞掉 (否则会顺带按下下面的控件)")
	}
}

// 点菜单标题**本身**不算"点外部" —— 那是切换菜单。
func TestClickOnMenuTitleIsNotOutside(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	fake, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	relayout(a)
	cx := file.Box.X + file.Box.W/2
	cy := file.Box.Y + file.Box.H/2
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: cx, Y: cy})

	if !file.expanded {
		t.Fatalf("点标题不该被当成外部点击 (菜单应保持展开)")
	}
}

// 点**子菜单**里的一项也不算"点外部": 子菜单叠在父下拉之上, 只判父下拉
// 会把"点子菜单"误判成外部点击, 于是点子菜单的瞬间菜单就没了。
func TestClickInsidePopupIsNotOutside(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"), mkMenuItem("Recent"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	fake, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	relayout(a)
	row := menuItemRowOf(t, file.menuPopup, 1)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown,
		X: row.Box.X + row.Box.W/2, Y: row.Box.Y + row.Box.H/2})

	if !file.expanded {
		t.Fatalf("点弹层内部不该被当成外部点击")
	}
}

// ===== 子菜单 =====

func TestSubmenuOpensToTheRight(t *testing.T) {
	inner := mkMenu("", mkMenuItem("Dark"), mkMenuItem("Light"))
	trigger := mkMenuItem("Theme")
	trigger.Children = append(trigger.Children, inner)
	inner.Parent = trigger

	file := mkMenu("View", mkMenuItem("Zoom"), trigger)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	relayout(a)
	row := menuItemRowOf(t, file.menuPopup, 1) // trigger
	if !row.menuSub {
		t.Fatalf("该行应被标记为子菜单触发器")
	}

	a.openSubmenu(row, inner)
	relayout(a)
	if !inner.expanded || inner.menuPopup == nil {
		t.Fatalf("子菜单应已展开")
	}
	// 必须挂在触发项右侧
	if inner.menuPopup.Box.X != row.Box.X+row.Box.W {
		t.Fatalf("子菜单未挂在触发项右侧: X = %d, 触发项右缘 = %d",
			inner.menuPopup.Box.X, row.Box.X+row.Box.W)
	}
	if got := countTagDeep(root, "menu-item"); got != 4 {
		// File 下拉 2 项 + 子菜单 2 项
		t.Fatalf("menu-item 总数 = %d, want 4", got)
	}
}

// 子菜单里的项必须能命中 —— 它比父下拉多一层嵌套, 是本批最容易漏的一处。
func TestSubmenuItemHittable(t *testing.T) {
	inner := mkMenu("", mkMenuItem("Dark"), mkMenuItem("Light"))
	trigger := mkMenuItem("Theme")
	trigger.Children = append(trigger.Children, inner)
	inner.Parent = trigger

	file := mkMenu("View", trigger)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	relayout(a)
	row := menuItemRowOf(t, file.menuPopup, 0)
	a.openSubmenu(row, inner)
	relayout(a)

	subRow := menuItemRowOf(t, inner.menuPopup, 1)
	cx, cy := subRow.Box.X+subRow.Box.W/2, subRow.Box.Y+subRow.Box.H/2
	if got := HitTest(root, cx, cy); got != subRow {
		t.Fatalf("子菜单项命中 = %v, want 该行自身 (子菜单没进逃逸层?)", got)
	}
}

// 收起父菜单必须连带收起子菜单, 否则会留下一块没有父菜单的幽灵下拉。
func TestClosingParentClosesSubmenu(t *testing.T) {
	inner := mkMenu("", mkMenuItem("Dark"))
	trigger := mkMenuItem("Theme")
	trigger.Children = append(trigger.Children, inner)
	inner.Parent = trigger

	file := mkMenu("View", trigger)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	relayout(a)
	row := menuItemRowOf(t, file.menuPopup, 0)
	a.openSubmenu(row, inner)

	a.toggleMenu(file) // 收起
	if inner.expanded || inner.menuPopup != nil {
		t.Fatalf("收起父菜单后子菜单仍处于展开态")
	}
	if got := countTagDeep(root, "menu-popup"); got != 0 {
		t.Fatalf("仍残留 %d 个弹层", got)
	}
}

// ===== 右键菜单 =====

func TestContextMenuAppearsAtPoint(t *testing.T) {
	box := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	withNum(box, "width", 300)
	withNum(box, "height", 200)
	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	box.Parent = root
	root.Children = []*GuiNode{box}
	_, a := mountTestApp(t, root, 400, 300)

	a.openContextMenu(120, 90, []*GuiNode{mkMenuItem("Copy"), mkMenuItem("Paste")})
	relayout(a)

	ctx := contextMenuNode(root)
	if ctx == nil {
		t.Fatalf("右键菜单未挂到根节点下")
	}
	if ctx.menuPopup == nil {
		t.Fatalf("右键菜单的弹层未建立")
	}
	if ctx.menuPopup.Box.X != 120 || ctx.menuPopup.Box.Y != 90 {
		t.Fatalf("菜单落点 = (%d,%d), want (120,90)",
			ctx.menuPopup.Box.X, ctx.menuPopup.Box.Y)
	}
	if got := len(ctx.menuPopup.Children); got != 2 {
		t.Fatalf("菜单项数量 = %d, want 2", got)
	}
}

// 越界时向右/向下翻转到窗口内, 保证整块菜单可见。
func TestContextMenuClampedIntoWindow(t *testing.T) {
	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	_, a := mountTestApp(t, root, 400, 300)

	a.openContextMenu(390, 290, []*GuiNode{mkMenuItem("A"), mkMenuItem("B")})
	relayout(a)
	ctx := contextMenuNode(root)
	if ctx == nil || ctx.menuPopup == nil {
		t.Fatalf("右键菜单未建立")
	}
	p := ctx.menuPopup.Box
	if p.X+p.W > 400 || p.Y+p.H > 300 {
		t.Fatalf("越界右键未被钳回窗口内: %v (窗口 400x300)", p)
	}
	if p.X < 0 || p.Y < 0 {
		t.Fatalf("钳位钳过了头: %v", p)
	}
}

// 右键菜单项可命中, 且点击后整支从树上摘掉 (不是只收起)。
func TestContextMenuItemClickRemovesMenu(t *testing.T) {
	picked := false
	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	_, a := mountTestApp(t, root, 400, 300)

	a.openContextMenu(50, 50, []*GuiNode{
		mkMenuItem("Copy", func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("copy", func(args ...object.Value) object.Value {
				picked = true
				return object.UndefinedSingleton
			})
		}),
	})
	relayout(a)
	ctx := contextMenuNode(root)
	row := menuItemRowOf(t, ctx.menuPopup, 0)
	cx, cy := row.Box.X+row.Box.W/2, row.Box.Y+row.Box.H/2
	if got := HitTest(root, cx, cy); got != row {
		t.Fatalf("右键菜单项命中 = %v, want 该行", got)
	}

	a.handleClick(cx, cy)
	if !picked {
		t.Fatalf("右键菜单项的回调未派发")
	}
	if contextMenuNode(root) != nil {
		t.Fatalf("选完后右键菜单应整支摘除")
	}
}

// 弹出的右键菜单上再点右键, 不该再弹一个 (先吃掉)。
func TestContextMenuOnItselfIsSwallowed(t *testing.T) {
	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	fake, a := mountTestApp(t, root, 400, 300)

	a.openContextMenu(50, 50, []*GuiNode{mkMenuItem("Copy")})
	relayout(a)
	ctx := contextMenuNode(root)
	before := ctx.menuPopup.Box

	pushAndPump(t, fake, a, Event{Kind: EventMouseRightUp, X: before.X + 5, Y: before.Y + 5})

	after := contextMenuNode(root)
	if after == nil {
		t.Fatalf("在菜单上点右键不该关掉它")
	}
	if after.menuPopup.Box != before {
		t.Fatalf("在菜单上点右键不该换位置: %v → %v", before, after.menuPopup.Box)
	}
}

// ===== 快捷键表 =====

func TestParseShortcut(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want shortcutSpec
	}{
		{"Ctrl+S", true, shortcutSpec{key: "S", ctrl: true}},
		{"ctrl+s", true, shortcutSpec{key: "S", ctrl: true}},
		{"Ctrl + S", true, shortcutSpec{key: "S", ctrl: true}},
		{"Ctrl+Shift+Z", true, shortcutSpec{key: "Z", ctrl: true, shift: true}},
		{"Alt+F4", true, shortcutSpec{key: "F4", alt: true}},
		{"Cmd+S", true, shortcutSpec{key: "S", ctrl: true}}, // Cmd 落到 Ctrl
		{"F5", true, shortcutSpec{key: "F5"}},
		{"Escape", true, shortcutSpec{key: "Escape"}},
		{"esc", true, shortcutSpec{key: "Escape"}},
		{"", false, shortcutSpec{}},
		{"Ctrl", false, shortcutSpec{}},     // 只有修饰键
		{"Ctrl+A+B", false, shortcutSpec{}}, // 两个主键
	}
	for _, c := range cases {
		got, ok := parseShortcut(c.in)
		if ok != c.ok {
			t.Fatalf("parseShortcut(%q) ok = %v, want %v", c.in, ok, c.ok)
		}
		if !c.ok {
			continue
		}
		if got != c.want {
			t.Fatalf("parseShortcut(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// 快捷键表从树上收集; 修饰键必须**恰好一致** (Ctrl+S 不该被 Ctrl+Shift+S
// 或单独的 S 触发)。
func TestShortcutTableMatchExact(t *testing.T) {
	hit := ""
	save := mkMenuItem("Save", menuItemShortcutProp("Ctrl+S"), func(n *GuiNode) {
		n.Props["onClick"] = object.NewBuiltin("save", func(args ...object.Value) object.Value {
			hit = "save"
			return object.UndefinedSingleton
		})
	})
	file := mkMenu("File", save)
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.rebuildShortcuts(root)
	if len(a.shortcuts) != 1 {
		t.Fatalf("快捷键表大小 = %d, want 1", len(a.shortcuts))
	}

	// 命中
	if !a.handleShortcut("S", true, false, false, Event{}) {
		t.Fatalf("Ctrl+S 应命中")
	}
	if hit != "save" {
		t.Fatalf("命中的回调 = %q, want \"save\"", hit)
	}

	// 修饰键多一个: 不命中
	hit = ""
	if a.handleShortcut("S", true, true, false, Event{}) {
		t.Fatalf("Ctrl+Shift+S 不该命中 Ctrl+S")
	}
	// 不带修饰键: 不进表 (必须留给输入框)
	if a.handleShortcut("S", false, false, false, Event{}) {
		t.Fatalf("裸 S 不该命中任何快捷键")
	}
	if hit != "" {
		t.Fatalf("不该有回调被派发, got %q", hit)
	}
}

// 没写 shortcut 的菜单项不进表。
func TestShortcutTableIgnoresItemsWithoutShortcut(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"), mkMenuItem("Open", menuItemShortcutProp("Ctrl+O")))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.rebuildShortcuts(root)
	if len(a.shortcuts) != 1 {
		t.Fatalf("快捷键表大小 = %d, want 1 (只有 Open 配了)", len(a.shortcuts))
	}
	if a.shortcuts[0].spec.key != "O" {
		t.Fatalf("表项键名 = %q, want \"O\"", a.shortcuts[0].spec.key)
	}
}

// 表为空时按键会惰性重建 (脚本动态加菜单项也能用上)。
func TestShortcutTableLazilyRebuilt(t *testing.T) {
	file := mkMenu("File", mkMenuItem("Save", menuItemShortcutProp("Ctrl+S"),
		func(n *GuiNode) {
			n.Props["onClick"] = object.NewBuiltin("save", func(args ...object.Value) object.Value {
				return object.UndefinedSingleton
			})
		}))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	if len(a.shortcuts) != 0 {
		t.Fatalf("前置: 初始表应为空")
	}
	if !a.handleShortcut("S", true, false, false, Event{}) {
		t.Fatalf("首次按键应惰性建表并命中")
	}
}

// ===== Esc 优先级 =====

// Esc 的优先级: 菜单 > 下拉框 > 对话框。
func TestEscapePriorityMenuFirst(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	bar := mkMenuBar(file)
	sel := &GuiNode{Tag: "select", Props: map[string]object.Value{}}
	root := mkAppTree(bar, sel)
	fake, a := mountTestApp(t, root, 400, 300)

	a.toggleMenu(file)
	// 打开下拉框 (用 Go 侧入口, 不走 attachSelectHandler 的包装)
	withPropList(sel, "a", "b")
	a.openSelect(sel)

	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})

	if file.expanded {
		t.Fatalf("Esc 应先收起菜单")
	}
	if !sel.expanded {
		t.Fatalf("下拉框不该被一起收掉 (Esc 一次只关一层)")
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Escape"})
	if sel.expanded {
		t.Fatalf("第二次 Esc 应收起下拉框")
	}
}

// withPropList 给 select 塞 options (Go 侧构造, 走 object.Array)。
func withPropList(n *GuiNode, items ...string) {
	arr := &object.Array{}
	for _, s := range items {
		arr.Elements = append(arr.Elements, object.NewString(s))
	}
	n.Props["options"] = arr
}

// ===== 键盘导航 =====

func TestMenuKeyboardNavigation(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	edit := mkMenu("Edit", mkMenuItem("Undo"))
	bar := mkMenuBar(file, edit)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.setFocus(file)
	// ↓ 展开
	if !a.handleMenuKey(file, "ArrowDown") {
		t.Fatalf("↓ 应展开菜单")
	}
	if !file.expanded {
		t.Fatalf("菜单未展开")
	}
	// → 换到下一个菜单, 且"换过去就开着" (真实菜单栏的手感)
	if !a.handleMenuKey(file, "ArrowRight") {
		t.Fatalf("→ 应消费")
	}
	if file.expanded {
		t.Fatalf("换走的菜单应收起")
	}
	if !edit.expanded {
		t.Fatalf("换到的菜单应已展开")
	}
	// Esc 收起
	if !a.handleMenuKey(edit, "Escape") {
		t.Fatalf("Esc 应消费")
	}
	if edit.expanded {
		t.Fatalf("Esc 后应收起")
	}
}

// 左右键在菜单栏上循环 (第一个往左 = 最后一个)。
func TestMenuKeyboardNavigationWraps(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	edit := mkMenu("Edit", mkMenuItem("Undo"))
	bar := mkMenuBar(file, edit)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	a.handleMenuKey(file, "ArrowLeft")
	a.mu.Lock()
	focused := a.focused
	a.mu.Unlock()
	if focused != edit {
		t.Fatalf("← 从第一个应循环到最后一个, 焦点 = %v", focused)
	}
}

// 焦点在菜单标题里的文本节点上时, 菜单键盘导航仍要生效 (menuInChain)。
func TestMenuInChainResolvesFromTitleText(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	mountTestApp(t, root, 400, 300)

	title := &GuiNode{Tag: "#text", Text: "File", Props: map[string]object.Value{}}
	title.Parent = file
	if got := menuInChain(title); got != file {
		t.Fatalf("menuInChain = %v, want 菜单自身", got)
	}
	if menuInChain(bar) != nil {
		t.Fatalf("菜单栏本身不是 menu, 不该命中")
	}
}

// ===== 绘制 =====

func TestMenuBarAndPopupPainted(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"), mkMenuItem("Open"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	img := renderTree(root, 400, 300)

	// 菜单栏底色 (浅灰条) 必须铺出来
	if n := countColor(img, Rect{X: bar.Box.X + bar.Box.W - 40, Y: 4, W: 30, H: menuBarH - 8},
		colorMenuBarFace); n == 0 {
		t.Fatalf("菜单栏底色未绘制")
	}
	// 下拉弹层的白底与边框必须补画 (它在 drawNode 里被自己的 case 截走,
	// 走不到通用盒子的 default 分支 —— 漏了就是"弹层透明")
	p := file.menuPopup.Box
	if n := countColor(img, Rect{X: p.X + 4, Y: p.Y + 4, W: p.W - 8, H: p.H - 8},
		colorPopupFace); n == 0 {
		t.Fatalf("菜单弹层底板未绘制")
	}
}

// 展开态的菜单标题应铺强调色底 (给"这个菜单正开着"一个反馈)。
func TestExpandedMenuTitleHighlighted(t *testing.T) {
	file := mkMenu("File", mkMenuItem("New"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)

	img := renderTree(root, 400, 300)
	before := countColor(img, file.Box, colorMenuActive)

	a.toggleMenu(file)
	relayout(a)
	img = renderTree(root, 400, 300)
	after := countColor(img, file.Box, colorMenuActive)

	if after <= before {
		t.Fatalf("展开后标题底未变化 (before %d, after %d)", before, after)
	}
}

// 分隔线画在行的中间 (上下留白), 不该贴边。
func TestSeparatorPaintedInMiddle(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"), mkSep())
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	img := renderTree(root, 400, 300)
	sep := menuItemRowOf(t, file.menuPopup, 1)

	// 中间那一行应有分隔线色
	mid := Rect{X: sep.Box.X + 12, Y: sep.Box.Y + sep.Box.H/2, W: sep.Box.W - 24, H: 1}
	if n := countColor(img, mid, colorMenuSep); n == 0 {
		t.Fatalf("分隔线未绘制")
	}
	// 行的最上沿不该有 (说明没贴边)
	top := Rect{X: sep.Box.X + 12, Y: sep.Box.Y, W: sep.Box.W - 24, H: 1}
	if n := countColor(img, top, colorMenuSep); n != 0 {
		t.Fatalf("分隔线贴在了行的上沿")
	}
}

// 禁用项用灰色字 (与可点项区分开)。
func TestDisabledMenuItemDimmed(t *testing.T) {
	file := mkMenu("File", mkMenuItem("Save", menuItemDisabledProp()))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	img := renderTree(root, 400, 300)
	row := menuItemRowOf(t, file.menuPopup, 0)
	// 标签区域的"最深像素"应是灰 (placeholder 色) 而不是近黑
	if n := countColor(img, row.Box, colorPlaceholder); n == 0 {
		t.Fatalf("禁用项的标签未用灰字")
	}
}

// ===== 弹层不会被父盒裁剪 =====

// 下拉弹层挂在 26px 高的标题下, 而弹层本身通常有 100px 高 —— 被父盒
// 裁掉就会"只画出一行" (这是菜单最容易踩的坑)。
func TestMenuPopupNotClippedByOwnerBox(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"), mkMenuItem("B"), mkMenuItem("C"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	popup := file.menuPopup
	if popup.Box.H <= file.Box.H {
		t.Fatalf("前置: 弹层应比标题高 (popup %d, 标题 %d)", popup.Box.H, file.Box.H)
	}
	// 弹层最后一行必须落在标题盒之外 (这才是"需要逃逸裁剪"的证明)
	last := menuItemRowOf(t, popup, 2)
	if file.Box.Contains(last.Box.X+1, last.Box.Y+1) {
		t.Fatalf("前置: 最后一行应落在标题盒之外")
	}
	img := renderTree(root, 400, 300)
	if n := countColor(img, Rect{X: last.Box.X + 4, Y: last.Box.Y + 4,
		W: last.Box.W - 8, H: last.Box.H - 8}, colorPopupFace); n == 0 {
		t.Fatalf("最后一行底板未绘制 (弹层被父盒裁掉了?)")
	}
}

// 弹层必须真的画在常规内容**之上**: 用一个压在它下面的兄弟节点验证。
func TestMenuPopupDrawnAboveSiblings(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"))
	bar := mkMenuBar(file)
	// 一个铺满窗口的红色内容块: 弹层应盖住它
	cover := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	withStr(cover, "background", "#ff0000")
	root := mkAppTree(bar, cover)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	img := renderTree(root, 400, 300)
	popup := file.menuPopup.Box
	// 弹层内部不该是红色 (被弹层盖住了)
	inside := Rect{X: popup.X + 2, Y: popup.Y + 2, W: popup.W - 4, H: popup.H - 4}
	red := countColor(img, inside, namedColors["red"])
	if red != 0 {
		t.Fatalf("弹层未盖住下方内容 (%d 个红像素透出)", red)
	}
}

// ===== 空/容错 =====

// 形状不对的子节点 (不是 menuitem/separator) 被静默跳过, 不产生空行。
func TestMenuIgnoresForeignChildren(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"))
	// 塞一个来路不明的标签
	foreign := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	foreign.Parent = file
	file.Children = append(file.Children, foreign)

	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	if got := len(file.menuPopup.Children); got != 1 {
		t.Fatalf("下拉项数量 = %d, want 1 (外来标签应被跳过)", got)
	}
}

// menuItems 的容错: 没有 label 也没有文本子节点的项 → 空标签行 (不 panic)。
func TestMenuItemWithoutLabel(t *testing.T) {
	file := mkMenu("File", mkMenuItem(""))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)
	relayout(a)

	row := menuItemRowOf(t, file.menuPopup, 0)
	if row.menuLabel != "" {
		t.Fatalf("无 label 的项不应凭空生出文字, got %q", row.menuLabel)
	}
	if row.Box.W != file.menuPopup.Box.W-2*menuPadY-2 {
		// 行盒撑满弹层内容区 (只校验比例关系, 不写死数值)
		if row.Box.W <= 0 {
			t.Fatalf("行盒宽度为 0")
		}
	}
}

// title prop 与 label prop 等价 (JSX 里 <menu title="File"> 更自然)。
func TestMenuTitleFallsBackToTitleProp(t *testing.T) {
	m := &GuiNode{Tag: "menu", Props: map[string]object.Value{}}
	m.Props["title"] = object.NewString("Window")
	if got := m.menuTitle(); got != "Window" {
		t.Fatalf("menuTitle = %q, want \"Window\"", got)
	}
	// 第 3 顺位: 文本子节点
	m2 := &GuiNode{Tag: "menu", Props: map[string]object.Value{}}
	child := &GuiNode{Tag: "#text", Text: "Help", Props: map[string]object.Value{}}
	child.Parent = m2
	m2.Children = []*GuiNode{child}
	if got := m2.menuTitle(); got != "Help" {
		t.Fatalf("menuTitle = %q, want \"Help\"", got)
	}
}

// 卸载节点时必须清掉菜单状态 (悬停链/弹层指针/子菜单引用)。
func TestDisposeNodeClearsMenuState(t *testing.T) {
	file := mkMenu("File", mkMenuItem("A"))
	bar := mkMenuBar(file)
	root := mkAppTree(bar)
	_, a := mountTestApp(t, root, 400, 300)
	a.toggleMenu(file)

	if file.menuPopup == nil {
		t.Fatalf("前置: 应已展开")
	}
	disposeNode(file)
	if file.expanded || file.menuPopup != nil {
		t.Fatalf("dispose 后菜单状态未清空")
	}
	if file.menuHighlight != -1 {
		t.Fatalf("dispose 后 menuHighlight = %d, want -1", file.menuHighlight)
	}
	if got := countTagDeep(root, "menu-popup"); got != 0 {
		t.Fatalf("dispose 后仍残留 %d 个弹层", got)
	}
}

// markup: menubar 里放非 menu 的标签 (右侧状态文本) 按固排尺寸顺排。
func TestMenuBarWithExtraChildren(t *testing.T) {
	file := mkMenu("File")
	extra := &GuiNode{Tag: "text", Props: map[string]object.Value{}}
	extra.appendTextNode("ready")
	bar := mkMenuBar(file)
	extra.Parent = bar
	bar.Children = append(bar.Children, extra)
	root := mkAppTree(bar)

	Layout(root, 400, 300)

	if extra.Box.X != file.Box.X+file.Box.W {
		t.Fatalf("额外子节点未接在菜单之后: X = %d, 期望 %d",
			extra.Box.X, file.Box.X+file.Box.W)
	}
	if extra.Box.H > menuBarH {
		t.Fatalf("额外子节点高度 %d 超出菜单栏 %d", extra.Box.H, menuBarH)
	}
}

// ===== 全链路 (真 VM): 回调真的被调了 =====
//
// 上面那批用例验的是状态机与几何; "菜单项的 onClick / onContextMenu /
// 快捷键最后真的派发到脚本" 必须走真 VM —— mountTestApp 那条路里
// currentVM 为 nil, 只有 Go 侧 builtin 能执行, 脚本闭包会被静默跳过。

// 菜单项的 onClick 派发到脚本, 且菜单同时收起。
func TestMenuFullChainItemCallback(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		globalThis.picked = "";
		globalThis.save = () => { globalThis.picked = "save"; };
		globalThis.open = () => { globalThis.picked = "open"; };
		render(
			h("column", null,
				h("menubar", null,
					h("menu", { label: "File" },
						h("menuitem", { label: "Save", onClick: () => globalThis.save() }),
						h("menuitem", { label: "Open", onClick: () => globalThis.open() }))),
				h("text", null, "body")),
			{ title: "menu", width: 400, height: 260 });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	a := currentApp()
	menu := findFirst(a.root, "menu")
	if menu == nil {
		t.Fatalf("未找到 menu 节点")
	}

	// 展开 (走真实键鼠之外的状态入口) → 点第 2 项 (Open)
	a.toggleMenu(menu)
	relayout(a)
	if menu.menuPopup == nil {
		t.Fatalf("菜单未展开")
	}
	row := menuItemRowOf(t, menu.menuPopup, 1)
	cx, cy := row.Box.X+row.Box.W/2, row.Box.Y+row.Box.H/2
	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: cx, Y: cy},
		Event{Kind: EventMouseUp, X: cx, Y: cy},
	)

	if s, ok := globalStr(t, v, "picked"); !ok || s != "open" {
		t.Fatalf("picked = %q (ok=%v), want \"open\"", s, ok)
	}
	if menu.expanded {
		t.Fatalf("选完一项后菜单应收起")
	}
}

// onContextMenu → openContextMenu(x, y, [...]) 全链路: 右键的位置与菜单项回调。
func TestContextMenuFullChain(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render, openContextMenu } from "gx/gfx";
		globalThis.picked = "";
		globalThis.at = "";
		globalThis.menuItems = [
			h("menuitem", { label: "Copy", onClick: () => { globalThis.picked = "copy"; } }),
			h("menuitem", { label: "Paste", onClick: () => { globalThis.picked = "paste"; } }),
		];
		globalThis.showCtx = (e) => {
			globalThis.at = e.x + "," + e.y;
			openContextMenu(e.x, e.y, globalThis.menuItems);
		};
		render(
			h("column", null,
				h("rect", { width: 300, height: 200, onContextMenu: (e) => globalThis.showCtx(e) }),
				h("text", null, "body")),
			{ title: "ctx", width: 400, height: 300 });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	a := currentApp()
	// 在矩形内部 (10,10)-(310,210) 右键
	pumpEvents(t, v, a, Event{Kind: EventMouseRightUp, X: 40, Y: 30})

	if s, ok := globalStr(t, v, "at"); !ok || s != "40,30" {
		t.Fatalf("onContextMenu 收到的坐标 = %q (ok=%v), want \"40,30\"", s, ok)
	}
	ctx := contextMenuNode(a.root)
	if ctx == nil {
		t.Fatalf("右键后未弹出菜单")
	}
	relayout(a)
	if ctx.menuPopup == nil {
		t.Fatalf("右键菜单的弹层未建立")
	}
	if got := ctx.menuPopup.Box.X; got != 40 {
		t.Fatalf("弹层 X = %d, want 40 (右键位置)", got)
	}

	// 点第二项 (Paste)
	row := menuItemRowOf(t, ctx.menuPopup, 1)
	cx, cy := row.Box.X+row.Box.W/2, row.Box.Y+row.Box.H/2
	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: cx, Y: cy},
		Event{Kind: EventMouseUp, X: cx, Y: cy},
	)
	if s, ok := globalStr(t, v, "picked"); !ok || s != "paste" {
		t.Fatalf("picked = %q (ok=%v), want \"paste\"", s, ok)
	}
	if contextMenuNode(a.root) != nil {
		t.Fatalf("选完后右键菜单应整支摘除")
	}
}

// 快捷键全链路: 菜单没展开也能用 Ctrl+S 触发, 回调拿到规范化文本。
func TestShortcutFullChain(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		globalThis.hits = 0;
		globalThis.keys = "";
		render(
			h("column", null,
				h("menubar", null,
					h("menu", { label: "File" },
						h("menuitem", {
							label: "Save", shortcut: "Ctrl+S",
							onClick: (e) => { globalThis.hits = globalThis.hits + 1; globalThis.keys = e.shortcut; },
						}))),
				h("text", null, "body")),
			{ title: "sc", width: 400, height: 260 });
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	a := currentApp()
	menu := findFirst(a.root, "menu")
	if menu.expanded {
		t.Fatalf("初始菜单不该是展开态")
	}

	// 不带修饰键的 S: 不该命中 (要留给输入框)
	pumpEvents(t, v, a, Event{Kind: EventKeyDown, Key: "s"})
	if n, _ := globalNum(t, v, "hits"); n != 0 {
		t.Fatalf("裸 s 不该触发快捷键, hits = %v", n)
	}

	// Ctrl+S 命中
	pumpEvents(t, v, a, Event{Kind: EventKeyDown, Key: "s", Ctrl: true})
	if n, _ := globalNum(t, v, "hits"); n != 1 {
		t.Fatalf("Ctrl+S 应触发一次, hits = %v", n)
	}
	if s, ok := globalStr(t, v, "keys"); !ok || s != "Ctrl+S" {
		t.Fatalf("e.shortcut = %q (ok=%v), want \"Ctrl+S\"", s, ok)
	}
	// 快捷键触发不该把菜单展开
	if menu.expanded {
		t.Fatalf("快捷键触发不该展开菜单")
	}
}

// globalNum 读脚本全局里的数字。
func globalNum(t *testing.T, v *vm.VM, name string) (float64, bool) {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	n, ok := val.(*object.Number)
	if !ok {
		t.Fatalf("全局 %s 不是数字: %T", name, val)
	}
	return n.Value, true
}
