package gfx

import (
	"image"
	"image/color"
	"strings"

	"github.com/14752222/Gox/object"
)

// 菜单栏与右键菜单 (P3-5)。
//
// ===== 为什么菜单栏是一个"普通容器"而不是窗口级装饰条 =====
//
// 任务书建议"render.go Mount 时自动插入顶部条, 占 root 第一行 24px"。
// 实现时改成了**把 <menubar> 当普通容器**: 窗口顶部那条 = 用户自己写的
// `column { menubar; content }`。
//
// 理由是那条路会同时坏掉三件事:
//
//  1. **root 的盒子**。自动插条要么改掉 root.Box (于是"窗口"的定义漂移,
//     layoutDialog/layoutToast 走 windowBox() 全都要跟着改), 要么保留
//     root 但把内容区整体下移 —— 后者的下移量要么覆盖用户的 gap/padding
//     (用户写 `column {gap:10}` 时首行会挨着菜单条), 要么就漏掉。
//  2. **测试与真机的一致性**。假 Surface 的"窗口"就是整块画布, 自动插条
//     意味着所有既有用例的坐标整体下移 24px —— 一条与菜单无关的改动
//     波及整个测试集。
//  3. **可读性**。菜单条是界面的一部分, 藏一次隐式插入会让"看到的树"
//     与"写下的树"对不上, 排查布局问题时第一反应就是去找那个不存在的节点。
//
// 代价是脚本要多写一层容器。这个代价换"零隐式行为"是划算的: 菜单条本来就
// 该和它的兄弟内容一起被排版。想省事可以包一个 JS 侧的小函数。
//
// ===== menu 的三种状态 =====
//
//   - closed:  只有标题行, 不画下拉;
//   - open:    <menubar> 里的顶级 menu 被点开, 下拉挂在它**正下方**;
//   - submenu: 下拉里嵌的 menu 被悬停/点开, 子下拉挂在它**右侧**。
//
// 三态都收在一个 expanded 字段里表达: expanded 为真 + menuSelfIsTop 判定
// 该往哪边挂。多一个 "parent menu" 字段会让状态机多一处可以不一致的地方。
//
// ===== 为什么右键菜单不用 contextMenu={<menu/>} =====
//
// 任务书写的是 `contextMenu={<menu>...</menu>}`。做不了, 原因是 JSX 的
// 元素是**单次挂载**的对象: h() 返回的 GuiNode 只有一个 Parent 字段,
// 同一个节点挂到两处会互相争抢 Parent, 于是"菜单弹出来之后原来那个位置
// 的节点链就断了"。而右键菜单天然要"每个被右键的组件各弹一个", 于是
// 每个使用点都得在脚本里**重新 h() 一次** —— 那与直接调 API 没有区别,
// 还多一层"为什么我的菜单只出现一次"的坑。
//
// 所以 v1 走数据式: `onContextMenu: (e) => openContextMenu(e.x, e.y, items)`,
// 与 <select options={...}> 的取法一致 (数据而不是声明式子节点)。
// 声明式的 contextMenu prop 留给将来有"节点复制"能力时再说。

// 菜单尺寸常量。全部收在这里, 绘制与布局共用同一份数值 —— 菜单最容易出的
// 一类问题是"测得的高度"与"画出来的高度"差几个像素, 表现为分隔线压字。
const (
	menuBarH        = 26 // 菜单栏 (menubar) 高度
	menuTitlePadX   = 10 // 菜单标题左右留白
	menuItemH       = 24 // 菜单项行高
	menuSepH        = 9  // 分隔线占位高度 (线画在中间)
	menuPadY        = 4  // 下拉弹层上下内边距
	menuMinW        = 120
	menuItemPadX    = 12 // 菜单项左留白 (图标/勾选位的代替)
	menuShortcutGap = 24 // label 与快捷键文字之间的最小间隙
)

// menuPopupZ 是菜单下拉弹层的 zIndex。比 selectPopupZ 高: 菜单通常压在
// 窗口内容之上, 且"菜单里再开下拉框"时菜单应在上面 (视觉上更自然)。
const menuPopupZ = 200

// menuCtxZ 是右键菜单的 zIndex: 再抬一层, 保证它盖住一切 (含已展开的菜单)。
const menuCtxZ = 300

// menuRowLabel 记录一行下拉项的内容。菜单项走的是"数据 → Go 侧建节点"的
// 路 (与 select 的选项一致), 因为下拉项是脚本用 h("menuitem", {...}) 声明
// 的 children —— 元素本身就是数据, 不必再转一层。
type menuItemSpec struct {
	label    string
	shortcut string
	disabled bool
	sep      bool // 分隔线 (来自 <separator>, 它没有 label)
	onClick  object.Value
	// submenu 非空表示这一项是"子菜单的触发器": 它不派发 onClick,
	// 而是把 items 作为右侧子下拉打开。
	submenu []*GuiNode
}

// ===== 状态与查询 =====

// menuIsTop 报告 menu 是否位于 <menubar> 的直属一级。
// 它决定下拉往哪边挂: 一级菜单往下, 子菜单往右。
func menuIsTop(n *GuiNode) bool {
	return n.Parent != nil && n.Parent.Tag == "menubar"
}

// menuTitle 读取菜单标题文本 (第 1 个文本子节点的内容)。
func (n *GuiNode) menuTitle() string {
	if s, ok := n.PropStr("label"); ok && s != "" {
		return s
	}
	// 没给 label 就用第一个文本子节点 —— <menu title="File"> 与
	// <menu label="File"> 两种写法都收 (title 是更自然的 JSX 写法)。
	if s, ok := n.PropStr("title"); ok && s != "" {
		return s
	}
	for _, c := range n.Children {
		if c.Tag == "#text" {
			return c.Text
		}
	}
	return ""
}

// menuItems 把 menu 的流内子节点翻译成下拉项列表。
//
// 只认 menuitem 与 separator: 其它标签跳过 (而不是"渲染成空行") ——
// 下拉里出现一个来路不明的空行, 排查成本远高于"假装它不存在"。
func (n *GuiNode) menuItems() []menuItemSpec {
	var out []menuItemSpec
	for _, c := range n.Children {
		switch c.Tag {
		case "menuitem":
			it := menuItemSpec{
				label:    c.menuItemLabel(),
				shortcut: c.menuItemShortcut(),
			}
			it.disabled = c.disabledInChain()
			it.onClick = c.PropHandler("onClick")
			if sub := c.menuSubmenuNodes(); len(sub) > 0 {
				it.submenu = sub
			}
			out = append(out, it)
		case "separator":
			out = append(out, menuItemSpec{sep: true})
		}
	}
	return out
}

// menuItemLabel 读菜单项文字 (label prop 优先, 否则第一个文本子节点)。
func (n *GuiNode) menuItemLabel() string {
	if s, ok := n.PropStr("label"); ok && s != "" {
		return s
	}
	for _, c := range n.Children {
		if c.Tag == "#text" {
			return c.Text
		}
	}
	return ""
}

// menuItemShortcut 读快捷键文本 (只用于显示; 真正匹配走 shortcutSpec)。
func (n *GuiNode) menuItemShortcut() string {
	s, _ := n.PropStr("shortcut")
	return s
}

// menuSubmenuNodes 返回作为本项子菜单的 menu 子节点 (通常 0 或 1 个)。
func (n *GuiNode) menuSubmenuNodes() []*GuiNode {
	var out []*GuiNode
	for _, c := range n.Children {
		if c.Tag == "menu" {
			out = append(out, c)
		}
	}
	return out
}

// menuPopupOf 返回 menu 当前的下拉弹层节点 (未展开为 nil)。
func menuPopupOf(n *GuiNode) *GuiNode {
	if n.menuPopup == nil || !n.expanded {
		return nil
	}
	return n.menuPopup
}

// ===== 展开 / 收起 =====

// attachMenuHandler 给 menubar 直属的 <menu> 装上内置的展开处理器。
//
// 与 select 完全同一套思路: 展开/收起是组件语义而不是用户回调, 但也不能
// 抢走脚本的 onClick —— 包一层: 先走组件逻辑, 再调脚本回调。
func attachMenuHandler(n *GuiNode) {
	user := n.PropHandler("onClick")
	n.Props["onClick"] = object.NewBuiltin("menuToggle", func(args ...object.Value) object.Value {
		// 按**节点归属**找 app, 不能用 currentApp: 多窗口下 currentApp 是
		// "最近挂载的窗口", 而这里的 n 明确属于某一个具体窗口 ——
		// 用错窗口会让 toggleMenu 去改另一个窗口的展开状态 (A 的菜单开了,
		// 看到的却是 B 的界面变化)。
		a := appOfNode(n)
		if a == nil {
			return object.UndefinedSingleton
		}
		a.toggleMenu(n)
		a.callHandlerValue(user, "onClick", nil)
		return object.UndefinedSingleton
	})
}

// toggleMenu 展开 / 收起一级菜单。菜单栏里的互斥语义与真实菜单栏一致:
// 已经有一个菜单开着时, 点另一个直接换过去 (而不是先全关再开)。
func (a *app) toggleMenu(m *GuiNode) {
	if m.expanded {
		a.closeMenu(m)
		return
	}
	a.closeOtherMenus(m)
	a.setFocus(m)
	a.openMenu(m)
}

// openMenu 展开 menu 的下拉弹层 (已经是展开态则不动)。
func (a *app) openMenu(m *GuiNode) {
	if m.expanded {
		return
	}
	items := m.menuItems()
	if len(items) == 0 {
		return // 空菜单不展开: 一个空盒子会让人以为"点坏了"
	}
	m.expanded = true
	m.menuHighlight = -1
	m.menuPopup = a.buildMenuPopup(m, items, menuIsTop(m))
	markFullDirtyFor(m)
}

// closeMenu 收起 menu 及其所有后代子菜单。
//
// 必须递归: 关掉一级菜单时, 它下面挂着的子下拉是独立的节点, 不一起收就会
// 留下一块"没有父菜单的幽灵下拉", 而且没有任何交互能再关掉它。
func (a *app) closeMenu(m *GuiNode) {
	if !m.expanded && m.menuPopup == nil {
		return
	}
	a.closeMenuSubtree(m)
	m.expanded = false
	m.menuHighlight = -1
	markFullDirtyFor(m)
}

// closeMenuSubtree 销毁 m 的下拉弹层 (含全部后代子菜单状态)。
func (a *app) closeMenuSubtree(m *GuiNode) {
	if m.menuPopup != nil {
		p := m.menuPopup
		m.menuPopup = nil
		// 先递归清掉子菜单, 再拆节点: 子下拉挂在 p 的子节点上, 直接
		// disposeNode(p) 会连它们一起摘掉, 但"哪些 menu 处于展开态"这份
		// 状态在各自节点上, 不清就会留下 expanded=true 的死节点。
		for _, c := range p.Children {
			if c.Tag == "menu" {
				c.expanded = false
				c.menuHighlight = -1
			}
		}
		disposeNode(p)
	}
	m.expanded = false
	m.menuHighlight = -1
}

// closeOtherMenus 收起树上所有"不是 keep"的展开菜单 (打开新菜单前调用)。
func (a *app) closeOtherMenus(keep *GuiNode) {
	for _, m := range expandedMenus(a.rootNode()) {
		if m != keep && !underNode(keep, m) && !underNode(m, keep) {
			a.closeMenu(m)
		}
	}
}

// closeAnyExpandedMenu 收起任意一个展开着的菜单, 返回是否关掉了。
// Esc 的第一优先级 (比下拉框更靠前: 菜单是最表层的交互)。
func (a *app) closeAnyExpandedMenu(root *GuiNode) bool {
	list := expandedMenus(root)
	if len(list) == 0 {
		return false
	}
	// 收最深的那个: 子菜单叠在父菜单之上, 按新增顺序倒着取即可
	// (child 总是晚于 parent 加入, 见 expandedMenus 的遍历序)。
	a.closeMenu(list[len(list)-1])
	return true
}

// expandedMenus 按"父先子后"的顺序收集树中所有展开的 menu。
func expandedMenus(root *GuiNode) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "menu" && n.expanded {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	if root != nil {
		walk(root)
	}
	return out
}

// ===== 构建弹层 =====

// buildMenuPopup 造一个下拉弹层 (menu-popup + N 个 menu-item)。
//
// dirDown 为真时是"一级菜单的下拉"(挂在标题正下方), 否则是"子菜单"
// (挂在父项右侧)。定位差异在 layoutMenu 里, 这里只管节点结构。
func (a *app) buildMenuPopup(m *GuiNode, items []menuItemSpec, dirDown bool) *GuiNode {
	popup := &GuiNode{Tag: "menu-popup", Props: map[string]object.Value{}}
	withStrProp(popup, "background", colorPopupFaceHex)
	withStrProp(popup, "border", colorPopupEdgeHex)
	withNumProp(popup, "padding", menuPadY)
	withBoolProp(popup, "escapeClipping", true)
	z := menuPopupZ
	if !dirDown {
		z = menuPopupZ + 1 // 子菜单压在父下拉之上
	}
	withNumProp(popup, "zIndex", float64(z))
	popup.Parent = m
	popup.menuOwner = m
	popup.menuDirDown = dirDown

	for i := range items {
		idx := i
		it := items[i]
		row := &GuiNode{Tag: "menu-item", Props: map[string]object.Value{}}
		row.menuIndex = idx
		row.menuOwner = m
		row.Parent = popup
		if it.sep {
			row.menuSep = true
		} else {
			row.menuLabel = it.label
			row.menuShortcutText = it.shortcut
			row.menuSub = len(it.submenu) > 0
			row.menuDisabled = it.disabled
		}
		// 每一项都要能命中: 命中测试只认"带处理器的节点" (hitNode), 没有
		// 处理器的行点不中, 悬停链也起不来。分隔线给一个空处理器, 它就
		// 变成了"吞掉点击但不做任何事"的一行 —— 正是分隔线该有的行为。
		row.Props["onClick"] = object.NewBuiltin("menuItem", func(args ...object.Value) object.Value {
			// 按节点归属找 app (同 attachMenuHandler 的理由)
			if app := appOfNode(m); app != nil {
				app.clickMenuItem(m, idx)
			}
			return object.UndefinedSingleton
		})
		popup.Children = append(popup.Children, row)
	}
	// 必须挂进 menu 的 Children: 布局是自顶向下从根走的, 不挂上去就永远
	// 拿不到盒子 (弹层会被算成 0 尺寸, 画不出来也点不中)。
	m.Children = append(m.Children, popup)
	return popup
}

// clickMenuItem 处理一次菜单项点击 (由 Go 侧内置处理器调用)。
func (a *app) clickMenuItem(m *GuiNode, idx int) {
	items := m.menuItems()
	if idx < 0 || idx >= len(items) {
		return
	}
	it := items[idx]
	if it.sep || it.disabled {
		return // 分隔线与禁用项什么都不做 (连"收起菜单"都不做)
	}
	if len(it.submenu) > 0 {
		// 子菜单触发器: 展开/收起右侧下拉, 自身不派发 onClick。
		// 菜单栏上的"悬停展开"要在真实交互里靠 onMouseMove 驱动, v1 只做点击。
		if sub := a.submenuFor(m, idx); sub != nil {
			if sub.expanded {
				a.closeMenu(sub)
			} else {
				a.openMenu(sub)
			}
			return
		}
		return
	}
	// 普通项: 关掉整棵菜单树 → 焦点收回菜单标题 → 派发 onClick({x, y})。
	//
	// 坐标传的是**点击位置**: 菜单项的 onClick 语义上不该关心坐标, 但
	// 与 onContextMenu 保持同一个参数形状 (都带 x/y) 能让脚本侧少一层
	// 分支 —— "菜单项回调按位置取参数" 是最容易被记住的一致形状。
	a.closeMenuTree(m)
	a.setFocus(m)
	if it.onClick != nil {
		arg := object.NewObject()
		arg.SetProperty("x", object.NewNumber(float64(m.Box.X)))
		arg.SetProperty("y", object.NewNumber(float64(m.Box.Y)))
		// 用内置处理器时把节点还给脚本是没意义的 (它拿到的是 menuitem 节点,
		// 而 menuitem 通常没有 onClick 之外的用途), 所以这里只需要参数。
		a.callHandlerValue(it.onClick, "onClick", arg)
	}
}

// submenuFor 找到 menu 第 idx 项作为子菜单的 menu 节点。
// 返回的节点**不是**树上的节点, 而是"数据式的临时节点" —— 见 menu_item
// 的构建路径: 子菜单在 open 时才由 openSubmenu 物化。
func (a *app) submenuFor(m *GuiNode, idx int) *GuiNode {
	if m.menuPopup == nil {
		return nil
	}
	for _, c := range m.menuPopup.Children {
		if c.Tag == "menu-item" && c.menuIndex == idx && c.menuChildMenu != nil {
			return c.menuChildMenu
		}
	}
	return nil
}

// openSubmenu 物化并展开某一项的子菜单。
//
// 子菜单的源是**声明式节点** (menuitem 里嵌的 <menu>), 它在原树上只有一份;
// 这里把它作为一个独立的 menu 节点挂到 menu-item 下 —— 注意这会把它的
// Parent 指到 menu-item 上, 于是"原树里的那个 menu"就跟着搬走了。
// 对 v1 而言这是可接受的: 嵌在 menuitem 里的 menu 除了当子菜单没有别的用途。
func (a *app) openSubmenu(row *GuiNode, sub *GuiNode) {
	if sub.expanded {
		return
	}
	items := sub.menuItems()
	if len(items) == 0 {
		return
	}
	row.menuChildMenu = sub
	// 先把它从**原宿主** (声明式 <menuitem> 里嵌着的那个位置) 摘下来, 再挂到
	// 行节点上。不摘的话同一个节点会被两条路径遍历到 (原宿主 → sub, 以及
	// triggerRow → sub), 于是它的弹层与菜单项在树上各出现两份 —— 表现是
	// "子菜单多了一倍的行, 计数翻倍", 而且 Parent 字段只能指一个, 布局与
	// 命中会各用各的路径, 行为不可预测。
	if prev := sub.Parent; prev != nil && prev != row {
		removeChild(prev, sub)
	}
	sub.Parent = row
	// 必须把 sub 挂进 row.Children: 子菜单的定位源是**触发项的行盒**
	// (layoutMenu 读 n.Parent.Box), 而布局是自顶向下从根走的 —— 只设
	// Parent 不挂 Children, layoutMenuItem 就遍历不到它, 子菜单永远是 0 尺寸:
	// 画不出来, 也点不中 (命中靠 escapesInDrawOrder, 那条链同样从 Children 走)。
	// (上面已从原宿主摘下, 所以这里不会重复挂。)
	row.Children = append(row.Children, sub)
	sub.expanded = true
	sub.menuHighlight = -1
	sub.menuPopup = a.buildMenuPopup(sub, items, false)
	markFullDirtyFor(row)
}

// childOf 报告 c 是否已经是 n 的直接子节点。
func childOf(n, c *GuiNode) bool {
	for _, x := range n.Children {
		if x == c {
			return true
		}
	}
	return false
}

// removeChild 把 c 从 n.Children 里摘掉 (不在则原地不动)。
//
// 用"就地压缩"而不是新建切片: 调用点在布局/交互路径上, 每帧都可能走到,
// 这里少一次分配。c.Parent 由调用方负责 (摘下来之后往往要立刻挂到别处)。
func removeChild(n, c *GuiNode) {
	if n == nil {
		return
	}
	kept := n.Children[:0]
	for _, x := range n.Children {
		if x == c {
			continue
		}
		kept = append(kept, x)
	}
	n.Children = kept
}

// closeMenuTree 关掉与 m 同属一棵菜单树的所有菜单 (含祖先与子菜单)。
//
// 选完一项要"整棵树一起关": 只关当前那个的话, 一级菜单的下拉还开着,
// 用户会看到菜单没关掉却又没反应。
func (a *app) closeMenuTree(m *GuiNode) {
	root := menuTreeRoot(m)
	for _, x := range expandedMenus(menuTreeRootOwner(root)) {
		if underNode(x, root) || x == root {
			a.closeMenuSubtree(x)
		}
	}
	a.closeMenuSubtree(root)
	a.closeMenu(root)
}

// menuTreeRoot 沿 menuOwner 链向上找到菜单树的最顶端 menu。
func menuTreeRoot(m *GuiNode) *GuiNode {
	for {
		owner := menuOwnerOf(m)
		if owner == nil {
			return m
		}
		m = owner
	}
}

// menuOwnerOf 返回 menu 的"宿主": 一级菜单的宿主是 menubar (无 owner),
// 子菜单的宿主是它所属的 menu-item 的那个 menu。
func menuOwnerOf(m *GuiNode) *GuiNode {
	if m.Parent == nil {
		return nil
	}
	if m.Parent.Tag == "menubar" || m.Parent.Tag == "root" {
		return nil
	}
	if m.Parent.Tag == "menu-item" {
		// menu-item 的宿主 menu 记在 menuOwner 上
		return m.Parent.menuOwner
	}
	return nil
}

// menuTreeRootOwner 返回菜单树顶端的**宿主节点** (用于收集要一起关掉的菜单)。
// 顶级菜单就是树根自己, 直接返回它所在的整棵树即可。
func menuTreeRootOwner(root *GuiNode) *GuiNode {
	if root.Parent == nil {
		return root
	}
	// 根节点的 Parent 链最上是整个元素树, 从那里收集才能覆盖所有菜单
	top := root
	for top.Parent != nil {
		top = top.Parent
	}
	return top
}

// ===== 右键菜单 (P3-5) =====

// openContextMenu 在 (x, y) 处就地弹出菜单。
//
// items 的形状与 <menu> 的 children 一致 (menuitem / separator 节点),
// 所以脚本可以复用同一份声明。菜单挂在**根节点**下 (而不是被右键的组件
// 下): 它要逃逸一切祖先裁剪, 且定位是窗口绝对坐标。
func (a *app) openContextMenu(x, y int, items []*GuiNode) {
	if len(items) == 0 {
		return
	}
	root := a.rootNode()
	if root == nil {
		return
	}
	a.closeContextMenu()
	// 菜单树: 用一个临时 menu 承载 items。它不进常规流 (escapeClipping),
	// 也不参与布局分配, 位置由 ctxX/ctxY 显式给出。
	m := &GuiNode{Tag: "menu", Props: map[string]object.Value{}}
	m.Parent = root
	m.ctxMenu = true
	m.ctxX, m.ctxY = x, y
	for _, it := range items {
		c := it
		c.Parent = m
		m.Children = append(m.Children, c)
	}
	// 与声明式菜单不同, 右键菜单的 items 是**已经建好的节点**, 直接物化。
	specs := m.menuItems()
	if len(specs) == 0 {
		return
	}
	m.expanded = true
	m.menuHighlight = -1
	popup := &GuiNode{Tag: "menu-popup", Props: map[string]object.Value{}}
	withStrProp(popup, "background", colorPopupFaceHex)
	withStrProp(popup, "border", colorPopupEdgeHex)
	withNumProp(popup, "padding", menuPadY)
	withBoolProp(popup, "escapeClipping", true)
	withNumProp(popup, "zIndex", menuCtxZ)
	popup.Parent = m
	popup.menuOwner = m
	popup.menuDirDown = true
	for i := range specs {
		idx := i
		it := specs[i]
		row := &GuiNode{Tag: "menu-item", Props: map[string]object.Value{}}
		row.menuIndex = idx
		row.menuOwner = m
		row.Parent = popup
		if it.sep {
			row.menuSep = true
		} else {
			row.menuLabel = it.label
			row.menuShortcutText = it.shortcut
			row.menuSub = len(it.submenu) > 0
			row.menuDisabled = it.disabled
		}
		row.Props["onClick"] = object.NewBuiltin("menuItem", func(args ...object.Value) object.Value {
			// 按节点归属找 app: 右键菜单挂在**根节点**下, 归属明确
			if app := appOfNode(m); app != nil {
				app.clickContextMenuItem(m, idx)
			}
			return object.UndefinedSingleton
		})
		popup.Children = append(popup.Children, row)
	}
	m.menuPopup = popup
	m.Children = append(m.Children, popup)
	root.Children = append(root.Children, m)
	markFullDirtyFor(m)
}

// clickContextMenuItem 处理右键菜单项的点击: 关掉菜单 (连节点一起摘掉) 再派发。
func (a *app) clickContextMenuItem(m *GuiNode, idx int) {
	specs := m.menuItems()
	handler := object.Value(nil)
	if idx >= 0 && idx < len(specs) {
		it := specs[idx]
		if it.sep || it.disabled {
			return
		}
		handler = it.onClick
	}
	a.closeContextMenu()
	if handler != nil {
		arg := object.NewObject()
		arg.SetProperty("x", object.NewNumber(float64(m.ctxX)))
		arg.SetProperty("y", object.NewNumber(float64(m.ctxY)))
		a.callHandlerValue(handler, "onClick", arg)
	}
}

// closeContextMenu 摘掉右键菜单 (整支从树上删除)。
//
// 与声明式菜单不同, 右键菜单的节点是**每次弹出时新建的**, 所以关闭就是
// 删除 —— 留着会让下一次 openContextMenu 不知道复用哪一个, 也会攒下
// 一堆永不可见 (但每帧都要走一遍布局) 的死节点。
func (a *app) closeContextMenu() {
	root := a.rootNode()
	if root == nil {
		return
	}
	kept := root.Children[:0]
	removed := false
	for _, c := range root.Children {
		if c.Tag == "menu" && c.ctxMenu {
			disposeNode(c)
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if removed {
		root.Children = kept
		markFullDirtyFor(root)
	}
}

// contextMenuNode 返回当前挂在树上的右键菜单 (无则 nil)。
func contextMenuNode(root *GuiNode) *GuiNode {
	if root == nil {
		return nil
	}
	for _, c := range root.Children {
		if c.Tag == "menu" && c.ctxMenu {
			return c
		}
	}
	return nil
}

// ===== 快捷键表 (P3-5) =====

// shortcutSpec 是一条解析后的快捷键: "Ctrl+Shift+S" 这样的文本拆成
// "修饰键组合 + 主键"。比较时三者全等才算命中。
type shortcutSpec struct {
	key              string // 已规范化 (大写单字符 / "F5" / "ArrowUp" 等)
	ctrl, shift, alt bool
}

// menuShortcutEntry 是快捷键表里的一条: 触发时要派发的回调。
type menuShortcutEntry struct {
	spec    shortcutSpec
	handler object.Value
	label   string // 菜单项文字 (仅用于诊断输出)
}

// parseShortcut 解析快捷键文本。
//
// 接受的分隔符: `+` 与空白 (两种写法都很常见: "Ctrl+S" / "Ctrl + S")。
// 主键规范化: 单字符统一大写 (Ctrl+s 与 Ctrl+S 是一回事), 其余原样
// ("F5" / "Enter" / "ArrowUp")。修饰键名大小写不敏感, 并接受几个常见别名。
//
// 返回 ok=false 表示"这串没法解析" (空串、只有修饰键、别名不认识)。
// 无法解析的快捷键**静默忽略**而不是报错: 菜单上显示一个不被识别的
// 快捷键文本, 比因为一个笔误让整个应用起不来要好得多。
func parseShortcut(s string) (shortcutSpec, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return shortcutSpec{}, false
	}
	// 先按 '+' 拆, 再对每一段按空白拆 (覆盖 "Ctrl + S" 这种手写习惯)
	var parts []string
	for _, seg := range strings.Split(s, "+") {
		for _, p := range strings.Fields(seg) {
			parts = append(parts, p)
		}
	}
	if len(parts) == 0 {
		return shortcutSpec{}, false
	}
	spec := shortcutSpec{}
	mainKey := ""
	for _, p := range parts {
		switch strings.ToLower(p) {
		case "ctrl", "control", "cmd", "command", "meta":
			// Cmd/Command 在 Windows 上落到 Ctrl: v1 只有 win32 后端,
			// 但写 "Cmd+S" 的脚本不该因此失效 (它在 mac 上才是对的)。
			spec.ctrl = true
		case "shift":
			spec.shift = true
		case "alt", "option":
			spec.alt = true
		default:
			if mainKey != "" {
				return shortcutSpec{}, false // 两个主键: 不是一条合法快捷键
			}
			mainKey = p
		}
	}
	if mainKey == "" {
		return shortcutSpec{}, false // 只有修饰键
	}
	spec.key = normalizeKeyName(mainKey)
	return spec, true
}

// normalizeKeyName 把主键名规范化到与 win32 后端 Event.Key 一致的形式。
//
// 单字符统一大写: win32 送上来的是 WM_KEYDOWN 的虚拟键字符 ('S' 已是大写,
// 但脚本可能写 "s"), 统一到一种形态才能比。
func normalizeKeyName(k string) string {
	if len(k) == 1 {
		c := k[0]
		if c >= 'a' && c <= 'z' {
			return string(c - 32)
		}
		return k
	}
	// 多字符键名: 沿用 DOM 的写法 (Enter / Escape / ArrowUp / F5 ...),
	// 只把首字母大写的形式也收下来 (脚本写 "enter" 也算命中)。
	switch strings.ToLower(k) {
	case "esc":
		return "Escape"
	case "del", "delete":
		return "Delete"
	case "ins", "insert":
		return "Insert"
	case "space", "spacebar":
		return " "
	}
	return k
}

// matchShortcut 在快捷键表里找与本次按键完全匹配的一条。
//
// 修饰键必须**恰好一致**: Ctrl+S 不该被 Ctrl+Shift+S 触发, 也不该被
// 单独的 S 触发。这是快捷键最容易写错的一条, 所以比较做成全等。
func (a *app) matchShortcut(key string, ctrl, shift, alt bool) *menuShortcutEntry {
	norm := normalizeKeyName(key)
	for i := range a.shortcuts {
		e := &a.shortcuts[i]
		if e.spec.key != norm {
			continue
		}
		if e.spec.ctrl != ctrl || e.spec.shift != shift || e.spec.alt != alt {
			continue
		}
		return e
	}
	return nil
}

// rebuildShortcuts 从整棵树收集 menuitem 的 shortcut, 重建快捷键表。
//
// 每次按键都重建? 不 —— 那样每敲一个字就要遍历整棵树。改在"菜单展开/
// 收起"以及"挂载/节点销毁"时机重建; 而菜单项通常随应用启动就固定下来。
// 这里额外提供一个惰性兜底: 表为空时重建一次 (脚本动态加了菜单项也能用,
// 代价只有第一次按键多走一遍树)。
func (a *app) rebuildShortcuts(root *GuiNode) {
	a.shortcuts = a.shortcuts[:0]
	if root == nil {
		return
	}
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "menuitem" {
			if s := n.menuItemShortcut(); s != "" {
				if spec, ok := parseShortcut(s); ok {
					// handler 允许为 nil: shortcut 是菜单项**自身**声明的属性,
					// 与它有没有 onClick 无关。实测坑: 早期版本要求 handler
					// 非空才入表, 于是"只声明快捷键、回调晚些再挂"的菜单项
					// 会静默不生效 —— 表里少一项, 按键时毫无反应也没有报错。
					a.shortcuts = append(a.shortcuts, menuShortcutEntry{
						spec: spec, handler: n.PropHandler("onClick"), label: n.menuItemLabel(),
					})
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
}

// handleShortcut 尝试用快捷键表消费一次按键, 返回是否消费了。
//
// 只要**带修饰键**的组合才进这张表: 不带修饰键的普通字母键必须留给
// 输入框与脚本的 onKeyDown, 否则菜单里写一条 shortcut="S" 就会让全应用
// 都打不出 s。
func (a *app) handleShortcut(key string, ctrl, shift, alt bool, ev Event) bool {
	if !ctrl && !alt {
		return false
	}
	root := a.rootNode()
	a.mu.Lock()
	if len(a.shortcuts) == 0 {
		a.rebuildShortcuts(root)
	}
	e := a.matchShortcut(key, ctrl, shift, alt)
	a.mu.Unlock()
	if e == nil {
		return false
	}
	// 快捷键命中的菜单项是"菜单项本身", 但回调只需要一个参数对象,
	// 与点击菜单项保持同一形状 (x/y 无意义时给 0)。
	arg := object.NewObject()
	arg.SetProperty("x", object.NewNumber(0))
	arg.SetProperty("y", object.NewNumber(0))
	arg.SetProperty("shortcut", object.NewString(keysText(key, ctrl, shift, alt)))
	a.callHandlerValue(e.handler, "onClick", arg)
	return true
}

// keysText 把一次按键拼回快捷键文本 (用于回调参数里的 shortcut 字段)。
func keysText(key string, ctrl, shift, alt bool) string {
	var b strings.Builder
	if ctrl {
		b.WriteString("Ctrl+")
	}
	if shift {
		b.WriteString("Shift+")
	}
	if alt {
		b.WriteString("Alt+")
	}
	b.WriteString(normalizeKeyName(key))
	return b.String()
}

// ===== 布局 =====

// menuBarContentWidth 菜单栏的内容宽度: 所有子节点固有宽度之和
// (缺省给 0 —— 有 stretch 的父容器会在布局里把它拉满)。
func menuBarContentWidth(n *GuiNode) int {
	w := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		if c.Tag == "menu" {
			w += c.menuTitleWidth()
			continue
		}
		cw, _ := c.intrinsicSize()
		w += cw
	}
	return w
}

// layoutMenuBar 布局菜单栏: 横向摆标题行, 高度固定 (不参与交叉轴 stretch
// 到全高 —— 菜单栏撑满容器高度是明显的错误)。
func layoutMenuBar(n *GuiNode) {
	area := inner(n)
	x := area.X
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		if c.Tag != "menu" {
			// 菜单栏里放别的标签 (常见: 右侧的状态文本/按钮): 按自身固有尺寸
			// 顺排, 不与菜单标题混在一起算 (否则"File Edit"之间的间距会随
			// 右侧图标宽度变化)。
			cw, ch := c.intrinsicSize()
			c.Box = Rect{X: x, Y: area.Y, W: cw, H: ch}
			layoutNode(c)
			x += cw
			continue
		}
		tw := c.menuTitleWidth()
		c.Box = Rect{X: x, Y: area.Y, W: tw, H: menuBarH}
		layoutNode(c)
		x += tw
	}
	placeAbsoluteIn(n, area)
}

// menuTitleWidth 菜单标题的宽度 (文字宽 + 两侧留白)。
func (n *GuiNode) menuTitleWidth() int {
	if w, ok := effectivePropNumOk(n, "width"); ok && w > 0 {
		return int(w)
	}
	tw, _ := MeasureText(n.menuTitle(), n.FontSize())
	return tw + 2*menuTitlePadX
}

// layoutMenu 布局 menu 自身 (标题行) 与它的下拉弹层。
//
// 下拉的位置在这里每帧重算 (而不是"展开时算一次"): 窗口 resize、菜单栏
// 里的前一个标题因为文字变化而变宽, 都会让正确的落点发生变化。布局本来
// 每帧都跑, 顺带算出来是最省事也最不会漂的做法。
func layoutMenu(n *GuiNode) {
	popup := menuPopupOf(n)
	if popup == nil {
		return
	}
	pw, ph := menuPopupSize(popup)
	if n.ctxMenu {
		// 右键菜单: 落点就是右键位置, 越界时往窗口内收 (见 clampMenuOrigin)。
		x, y := clampMenuOrigin(n.windowBox(), n.ctxX, n.ctxY, pw, ph)
		n.Box = Rect{X: x, Y: y, W: 0, H: 0}
		popup.Box = Rect{X: x, Y: y, W: pw, H: ph}
		layoutNode(popup)
		return
	}
	if menuIsTop(n) {
		// 一级菜单: 下拉贴在标题正下方、左对齐
		popup.Box = Rect{X: n.Box.X, Y: n.Box.Y + n.Box.H, W: pw, H: ph}
	} else {
		// 子菜单: 挂在触发项右侧, 顶部与该行对齐
		row := n.Parent
		ox, oy := row.Box.X+row.Box.W, row.Box.Y
		oy = clampSubmenuTop(n.windowBox(), oy, ph)
		popup.Box = Rect{X: ox, Y: oy, W: pw, H: ph}
	}
	layoutNode(popup)
}

// clampMenuOrigin 把右键菜单的落点钳进窗口: 超出右/下边界时向左/上翻转,
// 保证整块菜单都看得见 (否则在窗口右下角右键会得到半个菜单)。
func clampMenuOrigin(win Rect, x, y, w, h int) (int, int) {
	if x+w > win.X+win.W {
		x = win.X + win.W - w
	}
	if y+h > win.Y+win.H {
		y = win.Y + win.H - h
	}
	if x < win.X {
		x = win.X
	}
	if y < win.Y {
		y = win.Y
	}
	return x, y
}

// clampSubmenuTop 把子菜单的顶部钳进窗口 (子菜单比可用高度还高时贴顶)。
func clampSubmenuTop(win Rect, y, h int) int {
	if y+h > win.Y+win.H {
		y = win.Y + win.H - h
	}
	if y < win.Y {
		y = win.Y
	}
	return y
}

// menuPopupSize 算下拉弹层的固有尺寸: 宽取"最宽一行 (标签+快捷键)",
// 高按行类型累加 (普通项 24, 分隔线 9)。
func menuPopupSize(popup *GuiNode) (int, int) {
	maxW := menuMinW
	h := 0
	for _, row := range popup.Children {
		rh := menuItemH
		if row.menuSep {
			rh = menuSepH
			h += rh
			continue
		}
		w := menuItemPadX + measureOr0(row.menuLabel, row.FontSize())
		if row.menuShortcutText != "" {
			w += menuShortcutGap + measureOr0(row.menuShortcutText, row.FontSize())
		}
		// 子菜单标记 (右侧小三角) 的占位
		if row.menuSub {
			w += 16
		}
		w += menuItemPadX
		if w > maxW {
			maxW = w
		}
		h += rh
	}
	return maxW, h + 2*menuPadY + 2 // padding + 边框
}

// measureOr0 是 MeasureText 的容错包装 (空串直接 0, 免得白算一次字体)。
func measureOr0(s string, size int) int {
	if s == "" {
		return 0
	}
	w, _ := MeasureText(s, size)
	return w
}

// layoutMenuPopup 布局下拉弹层: 逐行摆放 (行盒撑满弹层宽)。
//
// 注意不要用 layoutStack: 那个会做 flexGrow/justifyContent 分配, 而菜单项
// 的语义是"每行等宽撑满、高度固定", 用通用栈布局反而要写一堆覆盖项。
func layoutMenuPopup(n *GuiNode) {
	area := inner(n)
	y := area.Y
	for _, row := range n.Children {
		h := menuItemH
		if row.menuSep {
			h = menuSepH
		}
		row.Box = Rect{X: area.X, Y: y, W: area.W, H: h}
		layoutNode(row)
		y += h
	}
	placeAbsoluteIn(n, area)
}

// layoutMenuItem 布局菜单项内部 (标签左、快捷键右)。
// 这里只算位置不建子节点: 文字由 paintMenuItem 直接画 (菜单项的文字是
// 数据而不是元素 —— 脚本写的是 <menuitem label="Save">)。
func layoutMenuItem(n *GuiNode) {
	// 子菜单挂在 menu-item 下 (如果有), 由 layoutMenu 定位。
	//
	// 注意 submenu 的 menu 是个**常规流子节点** (它不是弹层标签 —— 弹层是
	// 它的 menuPopup), 所以这里不能先按 isFlowChild 过滤: 早期版本先
	// `if c.isFlowChild() { continue }`, 子菜单当场被跳过, 盒子恒为 0,
	// 症状是"子菜单展开状态是对的, 但一个像素都看不见、也点不中"。
	for _, c := range n.Children {
		if c.Tag == "menu" {
			layoutMenu(c)
		}
	}
}

// ===== 绘制 =====

// paintMenuBar 画菜单栏背景 (缺省浅灰条 + 底边线)。
// 与 canvas 同理: menubar 被自己的 case 截走后走不到 default 的通用盒子
// 绘制, 所以 background/border 必须在自己的 paint 函数里补画。
func paintMenuBar(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	bg, ok := n.backgroundFor()
	if !ok {
		bg = colorMenuBarFace
	}
	FillRect(img, n.Box, tint(bg, disabled))
	// 底边线: 菜单栏与内容区之间的一条分隔, 缺省画 (可用 border 覆盖颜色)
	if bd, ok := n.borderFor(); ok {
		StrokeRect(img, n.Box, tint(bd, disabled))
	} else {
		StrokeRect(img, n.Box, tint(colorMenuBarEdge, disabled))
	}
}

// paintMenu 画菜单标题。展开态铺强调色底, 让"这个菜单正开着"有反馈。
func paintMenu(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.ctxMenu || n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	if n.expanded {
		FillRect(img, n.Box, tint(colorMenuActive, disabled))
	} else if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(n.interactiveFace(bg), disabled))
	}
	title := n.menuTitle()
	if title == "" {
		return
	}
	size := n.FontSize()
	_, th := MeasureText(title, size)
	x := n.Box.X + menuTitlePadX
	maxW := n.Box.W - menuTitlePadX
	if maxW < 0 {
		maxW = 0
	}
	DrawText(img, image.Rect(0, 0, 0, 0).Union(img.Bounds()), title, x,
		n.Box.Y+(n.Box.H-th)/2, size, tint(n.textColor(), disabled), maxW)
}

// paintMenuPopup 画下拉弹层的底板。**必须在自己的 paint 里补画 background/
// border**: menu-popup 被 drawNode 的 case 截走, 通用盒子分支管不到它。
func paintMenuPopup(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(n.propColor("background", colorPopupFace), disabled))
	StrokeRect(img, b, tint(n.propColor("border", colorPopupEdge), disabled))
}

// paintMenuItem 画一行菜单项: 悬停/高亮底 + 标签 + 右侧快捷键 + 子菜单三角。
//
// 高亮来源有两个 (鼠标悬停 与 键盘光标), 与 select-option 同一套约定:
// 分开存但共用一个视觉表现。
func paintMenuItem(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	if n.menuSep {
		// 分隔线: 中间一条 1px 线, 左右各留 8px (与常见桌面菜单一致)
		y := b.Y + b.H/2
		c := tint(colorMenuSep, disabled)
		FillRect(img, Rect{X: b.X + 8, Y: y, W: b.W - 16, H: 1}, c)
		return
	}
	itemDisabled := disabled || n.menuDisabled
	if n.menuItemHighlighted() && !itemDisabled {
		FillRect(img, b, tint(colorMenuHighlight, disabled))
	}
	size := n.FontSize()
	textColor := colorText
	if itemDisabled {
		textColor = colorPlaceholder
	}
	_, th := MeasureText(n.menuLabel, size)
	y := b.Y + (b.H-th)/2
	// 快捷键先量宽度, 标签的可用宽度要给它让位
	scW := measureOr0(n.menuShortcutText, size)
	maxW := b.W - 2*menuItemPadX - scW - menuShortcutGap
	if maxW < 0 {
		maxW = 0
	}
	DrawText(img, img.Bounds(), n.menuLabel, b.X+menuItemPadX, y, size,
		tint(textColor, disabled), maxW)
	if n.menuShortcutText != "" {
		sx := b.X + b.W - menuItemPadX - scW
		DrawText(img, img.Bounds(), n.menuShortcutText, sx, y, size,
			tint(colorMenuShortcut, disabled), scW)
	}
	if n.menuSub {
		paintSubmenuArrow(img, b, tint(textColor, disabled))
	}
}

// menuItemHighlighted 报告菜单项是否高亮 (悬停 或 键盘光标)。
func (n *GuiNode) menuItemHighlighted() bool {
	if n.hovered {
		return true
	}
	return n.menuOwner != nil && n.menuOwner.expanded &&
		n.menuOwner.menuHighlight == n.menuIndex
}

// paintSubmenuArrow 画子菜单指示三角 (向右的实心小三角)。
func paintSubmenuArrow(img *image.RGBA, b Rect, c color.RGBA) {
	cx := b.X + b.W - menuItemPadX + 2
	cy := b.Y + b.H/2
	for i := 0; i < 4; i++ {
		FillRect(img, Rect{X: cx + i, Y: cy - 3 + i, W: 1, H: 7 - 2*i}, c)
	}
}

// ===== 键盘 (菜单) =====

// handleMenuKey 处理焦点在菜单标题上时的方向键: 左右切换一级菜单,
// 下箭头展开。返回是否消费。
func (a *app) handleMenuKey(m *GuiNode, key string) bool {
	if m.Parent == nil || m.Parent.Tag != "menubar" {
		return false
	}
	bar := m.Parent
	switch key {
	case "ArrowRight", "ArrowLeft":
		delta := 1
		if key == "ArrowLeft" {
			delta = -1
		}
		idx := -1
		var titles []*GuiNode
		for _, c := range bar.Children {
			if c.Tag == "menu" {
				if c == m {
					idx = len(titles)
				}
				titles = append(titles, c)
			}
		}
		if len(titles) == 0 || idx < 0 {
			return false
		}
		next := titles[wrapIndex(idx+delta, len(titles))]
		a.setFocus(next)
		// 已经开着菜单时, 左右键应"直接换到旁边那个" (真实菜单栏的手感)
		if m.expanded {
			a.closeOtherMenus(next)
			a.openMenu(next)
		}
		return true
	case "ArrowDown", "Enter", " ":
		a.openMenu(m)
		return true
	case "Escape":
		if m.expanded {
			a.closeMenu(m)
			return true
		}
	}
	return false
}
