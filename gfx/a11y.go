package gfx

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/14752222/Gox/object"
)

// ===== T10 无障碍与键盘导航 =====
//
// 这一层补的是**纯键盘可用性**的三块地基, 全部落在内核 (而不是各组件各自
// 实现一遍 —— 那样必然有组件漏掉, 而且 Tab 顺序会随实现漂移):
//
//  1. **焦点注册表**: `focusable` / `tabIndex` 两个 prop + 一张"原生可聚焦
//     标签"表, 决定谁进 Tab 序、按什么顺序进 (见 a11yFocusOrder);
//  2. **Tab / Shift+Tab 遍历**: 在 handleKey 里消费, 循环绕回, 弹层打开时
//     把遍历范围收进弹层 (focus trap);
//  3. **方向键导航 + Enter/Space 激活**: 组合控件 (tabs / pagination /
//     radio 组 / slider / rating) 的键盘语义。
//
// aria 语义走"隐式 role + 显式覆盖"两张表 (a11yImplicitRole / role prop),
// `aria-*` 属性按原样留在 props 里 —— 内核不做读屏桥 (读屏是移动端 v1 明确
// 不做的一项, 见 docs/mobile-adaptation.md §9), 但 role/name 是可读的,
// `gx/a11y` 的 focusOrder() 把整条焦点链连同 role/name 暴露给脚本, 官网
// 组件画廊的 a11y 面板与回归清单都读它。
//
// 与既有机制的关系: 焦点状态本身 (a.focused / GuiNode.focused) 完全没有
// 换 —— 点击获焦、输入框光标、焦点虚线框 (drawFocusRing) 都照旧。这一层只是
// 把"谁能拿焦点、怎么用键盘换焦点"从"只有鼠标能改"扩到"键盘也能改"。

// a11yNativeFocusable 是"不写 focusable 也进 Tab 序"的标签集合。
//
// 判据是"浏览器里这个标签天然可聚焦": 表单控件与按钮。刻意**不含**容器类
// (column/row/view/rect) —— 给一个纯布局盒子加 Tab 停留点会让键盘用户多按
// 好几次空 Tab, 这是可访问性上最常见的自伤。脚本真想让某个容器可聚焦时
// 显式写 focusable={true}。
var a11yNativeFocusable = map[string]struct{}{
	"button": {}, "checkbox": {}, "radio": {}, "switch": {}, "slider": {},
	"input": {}, "search": {}, "textarea": {}, "select": {}, "rating": {},
	"tabs": {}, "pagination": {},
	// T08 一期新增 (本文件同批落地): 三者都用"字段 + 弹层"的交互模型.
	"datepicker": {}, "colorpicker": {}, "upload": {},
	// list-item 默认**不**可聚焦: 它是"行"的容器约定, 列表行该不该进 Tab 序
	// 取决于它是"可选项"还是"纯展示", 由脚本的 focusable 决定。
}

// focusable 报告节点是否可进 Tab 序。
//
// 显式 focusable prop 永远赢 (两种方向都可以): focusable={false} 能把一个
// 原生可聚焦控件移出 Tab 序 (比如"用 Tab 跳过这个搜索框"), focusable={true}
// 能让容器/图标进序。
func (n *GuiNode) focusable() bool {
	if n == nil {
		return false
	}
	if v, ok := n.PropBool("focusable"); ok {
		return v
	}
	_, ok := a11yNativeFocusable[n.Tag]
	return ok
}

// tabIndexOf 读 tabIndex prop (缺省 0)。
//
// 语义与 DOM 一致:
//
//	> 0  正序优先 (按数值升序排在 0 之前 —— "先跳到我这里")
//	  0  按文档 (树) 序
//	 -1  不进 Tab 序, 但仍可被程序聚焦 (gx/a11y 的 focus())
//
// 负数统一按 -1 处理 (DOM 只认 -1, 更小的值当 -1 是各引擎的实际行为)。
func (n *GuiNode) tabIndexOf() int {
	if v, ok := n.PropNum("tabIndex"); ok {
		t := int(v)
		if t < 0 {
			return -1
		}
		return t
	}
	return 0
}

// a11yHidden 报告节点是否对无障碍层隐藏 (hidden prop 或 aria-hidden="true")。
// 隐藏节点既不进 Tab 序, 也不出现在 focusOrder() 里。
//
// aria-hidden 同时认字符串 "true" 与布尔 true: 前者是 DOM 属性的原始形态
// (HTML 里写 aria-hidden="true"), 后者是 JSX 里更顺手的写法
// (`aria-hidden={true}`), 两种都出现得很自然, 没必要只认一种。
func (n *GuiNode) a11yHidden() bool {
	if v, ok := n.PropBool("hidden"); ok && v {
		return true
	}
	if b, ok := n.PropBool("aria-hidden"); ok && b {
		return true
	}
	if s, ok := n.PropStr("aria-hidden"); ok && strings.EqualFold(s, "true") {
		return true
	}
	return false
}

// ===== aria 语义 (隐式 role + 显式覆盖) =====

// a11yImplicitRole 是"标签 → 缺省 aria role"。表的取向与已知标签表一致:
// 每个内置组件都要有一行, 否则 role 落空 ⇒ 无障碍层把它当成无名容器。
//
// 缺省的依据: HTML 隐式 role (button/checkbox/radio/textbox…) 优先, HTML 没有
// 对应物的组件取 ARIA 里语义最接近的 (rating → slider, pagination → navigation,
// drawer → dialog, spinner/skeleton → progressbar/status)。
var a11yImplicitRole = map[string]string{
	"button": "button", "checkbox": "checkbox", "radio": "radio",
	"switch": "switch", "slider": "slider", "input": "textbox",
	"search": "searchbox", "textarea": "textbox", "select": "combobox",
	"rating": "slider", "tabs": "tablist", "tab": "tab",
	"pagination": "navigation", "progress": "progressbar",
	"dialog": "dialog", "drawer": "dialog", "toast": "status",
	"alert": "alert", "tooltip": "tooltip", "tooltip-popup": "tooltip",
	"menubar": "menubar", "menu": "menu", "menuitem": "menuitem",
	"menu-item": "menuitem", "table": "table", "tree": "tree",
	"list-item": "option", "icon": "img", "avatar": "img",
	"empty": "status", "spinner": "progressbar", "skeleton": "status",
	"badge": "status", "tag": "status", "scroll": "region",
	"datepicker": "combobox", "colorpicker": "combobox",
	"upload": "group", "label": "label", "form": "form",
}

// a11yKnownRoles 是显式 role prop 的取值白名单 (ARIA 1.2 的 widget / landmark
// / document 角色子集)。写错拼写会静默退化成"无名容器", 与未知标签同款陷阱,
// 所以在 h() 里挡一道警告 (见 a11yCheckProps)。
var a11yKnownRoles = map[string]struct{}{
	"alert": {}, "alertdialog": {}, "application": {}, "article": {},
	"banner": {}, "button": {}, "cell": {}, "checkbox": {}, "columnheader": {},
	"combobox": {}, "complementary": {}, "contentinfo": {}, "definition": {},
	"dialog": {}, "directory": {}, "document": {}, "feed": {}, "figure": {},
	"form": {}, "grid": {}, "gridcell": {}, "group": {}, "heading": {},
	"img": {}, "link": {}, "list": {}, "listbox": {}, "listitem": {},
	"log": {}, "main": {}, "marquee": {}, "math": {}, "menu": {},
	"menubar": {}, "menuitem": {}, "menuitemcheckbox": {}, "menuitemradio": {},
	"navigation": {}, "none": {}, "note": {}, "option": {}, "presentation": {},
	"progressbar": {}, "radio": {}, "radiogroup": {}, "region": {}, "row": {},
	"rowgroup": {}, "rowheader": {}, "scrollbar": {}, "search": {},
	"searchbox": {}, "separator": {}, "slider": {}, "spinbutton": {},
	"status": {}, "switch": {}, "tab": {}, "table": {}, "tablist": {},
	"tabpanel": {}, "term": {}, "textbox": {}, "timer": {}, "toolbar": {},
	"tooltip": {}, "tree": {}, "treegrid": {}, "treeitem": {},
}

// AriaRole 返回节点的 role: 显式 role prop 优先 (非法值回落隐式值并由
// a11yCheckProps 告警一次), 否则用 a11yImplicitRole 的隐式值。
func (n *GuiNode) AriaRole() string {
	if n == nil {
		return ""
	}
	if s, ok := n.PropStr("role"); ok {
		if _, valid := a11yKnownRoles[strings.ToLower(s)]; valid {
			return strings.ToLower(s)
		}
	}
	return a11yImplicitRole[n.Tag]
}

// AriaName 计算节点的无障碍名 (accessible name)。取值顺序照 ARIA 的
// "author > content" 分级:
//
//	aria-label → title → 文本内容 (TextContent) → placeholder (字段类)
//
// 空串表示"没有可读名字" —— focusOrder() 会原样报出来, 而不是编一个
// ("button 3")。让缺失暴露在外面, 才有人去补 aria-label。
func (n *GuiNode) AriaName() string {
	if n == nil {
		return ""
	}
	if s, ok := n.PropStr("aria-label"); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if s, ok := n.PropStr("title"); ok && strings.TrimSpace(s) != "" {
		return s
	}
	if s := strings.TrimSpace(n.TextContent()); s != "" {
		return s
	}
	if s, ok := n.PropStr("placeholder"); ok {
		return s
	}
	return ""
}

// AriaDescription 读 aria-description / description prop (可空)。
func (n *GuiNode) AriaDescription() string {
	if s, ok := n.PropStr("aria-description"); ok {
		return s
	}
	if s, ok := n.PropStr("description"); ok {
		return s
	}
	return ""
}

// a11yKnownAriaProps 是内核认得的 aria-* 属性名。
//
// 不在表里的一律告警 (warnOnce): `aria-lable="删除"` 这种拼写错不会报错、
// 只会让无障碍名静默丢失 —— 而丢失的名字恰恰是读屏用户唯一能拿到的东西。
// 表里覆盖 ARIA 1.2 的 aria-* 全集, 不是"内核用到的子集": 用不到不等于
// 写错了 (浏览器也只是透传), 这里要挡的是**拼错**, 不是"用得多"。
var a11yKnownAriaProps = map[string]struct{}{
	"aria-activedescendant": {}, "aria-atomic": {}, "aria-autocomplete": {},
	"aria-busy": {}, "aria-checked": {}, "aria-colcount": {},
	"aria-colindex": {}, "aria-colspan": {}, "aria-controls": {},
	"aria-current": {}, "aria-describedby": {}, "aria-description": {},
	"aria-details": {}, "aria-disabled": {}, "aria-dropeffect": {},
	"aria-errormessage": {}, "aria-expanded": {}, "aria-flowto": {},
	"aria-grabbed": {}, "aria-haspopup": {}, "aria-hidden": {},
	"aria-invalid": {}, "aria-keyshortcuts": {}, "aria-label": {},
	"aria-labelledby": {}, "aria-level": {}, "aria-live": {},
	"aria-modal": {}, "aria-multiline": {}, "aria-multiselectable": {},
	"aria-orientation": {}, "aria-owns": {}, "aria-placeholder": {},
	"aria-posinset": {}, "aria-pressed": {}, "aria-readonly": {},
	"aria-relevant": {}, "aria-required": {}, "aria-roledescription": {},
	"aria-rowcount": {}, "aria-rowindex": {}, "aria-rowspan": {},
	"aria-selected": {}, "aria-setsize": {}, "aria-sort": {},
	"aria-valuemax": {}, "aria-valuemin": {}, "aria-valuenow": {},
	"aria-valuetext": {},
}

var a11yPropWarnMu sync.Mutex

var a11yPropWarned = map[string]struct{}{}

// a11yWarnOnce 按 key 去重地记一条警告 (h() 是热路径, 同一笔误每次重建都会
// 再来一遍 —— 与 warnUnknownTagOnce 同款纪律)。
func a11yWarnOnce(key, format string, args ...interface{}) {
	a11yPropWarnMu.Lock()
	_, seen := a11yPropWarned[key]
	if !seen {
		a11yPropWarned[key] = struct{}{}
	}
	a11yPropWarnMu.Unlock()
	if seen {
		return
	}
	recordWarn(format, args...)
}

// a11yCheckProps 在 h() 建节点时校验无障碍相关的属性名。
//
// 只查两件会**静默失效**的事: 拼错的 aria-* 名 (白名单外且以 aria- 开头),
// 与非法 role 值 (a11yKnownRoles 外)。两者都不影响渲染, 所以不检查就没有
// 任何信号, 用户只能靠"读屏没读出东西"反推。
func a11yCheckProps(tag string, props *object.Object) {
	if props == nil {
		return
	}
	for name := range props.Properties {
		if strings.HasPrefix(name, "aria-") {
			if _, ok := a11yKnownAriaProps[name]; !ok {
				a11yWarnOnce("aria-prop:"+name,
					"<%s> 上的 %q 不是已知的 aria-* 属性 (拼写错误? 已知属性见 docs/accessibility.md)", tag, name)
			}
			continue
		}
		if name == "role" {
			if v, ok := props.Properties["role"]; ok {
				if s, ok := v.Value.(*object.String); ok {
					if _, valid := a11yKnownRoles[strings.ToLower(s.Value)]; !valid {
						a11yWarnOnce("role:"+s.Value,
							"<%s role=%q> 不是已知的 ARIA role (见 docs/accessibility.md 的 role 表)", tag, s.Value)
					}
				}
			}
		}
	}
}

// ===== 焦点遍历序 =====

// a11yFocusScope 返回 Tab 遍历的范围: 最上层可见的模态弹层 (dialog/drawer),
// 没有就整棵树。
//
// 这就是"焦点陷阱"的全部实现: 弹层打开时, 遍历序里只剩弹层内部的控件,
// 于是 Tab 绕来绕去都出不去 —— 不必额外维护一个"陷阱栈"。弹层关闭后节点
// 仍在树上 (只是 open=false), 天然被排除, 恢复由 a11yReconcileFocus 收拾。
func a11yFocusScope(root *GuiNode) *GuiNode {
	if root == nil {
		return nil
	}
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		if esc[i].Tag != "dialog" && esc[i].Tag != "drawer" {
			continue
		}
		if esc[i].overlayVisible() {
			return esc[i]
		}
	}
	return root
}

// a11yFocusOrder 返回当前窗口的 Tab 遍历顺序 (已按 tabIndex 排序)。
//
// 顺序规则 (与 DOM 一致):
//  1. tabIndex > 0 的按数值升序排在最前;
//  2. tabIndex == 0 的按树序 (绘制序) 接在后面;
//  3. tabIndex < 0 的不进序。
//
// 被排除的还有: 禁用子树、对无障碍隐藏的节点 (hidden / aria-hidden)、
// 未布局 (Box 为空) 的节点、以及跑到窗口外的节点 (滚动容器里滚出视口的行
// 就是这种 —— 它们仍有 Box, 但不在窗口矩形内)。
func a11yFocusOrder(root *GuiNode) []*GuiNode {
	scope := a11yFocusScope(root)
	if scope == nil {
		return nil
	}
	var out []*GuiNode
	a11yCollectFocusable(scope, scope.Box, &out)
	out = a11yRovingRadios(out)
	return a11ySortByTabIndex(out)
}

// a11yCollectFocusable 按绘制序 DFS 收集可聚焦节点。
//
// 三个"整支跳过"的判定值得记下理由:
//   - **弹层子树**: select-popup / menu-popup 里的选项不进 Tab 序 —— 它们是
//     "箭头键在里面走"的复合控件内部结构 (与浏览器里 listbox 选项不进 Tab 序
//     完全一致), 让它们各自成一个 Tab 停留点会把一个下拉变成十几次 Tab;
//   - **Box 为空**: 未布局 / tabs 的非激活页 / 已关闭弹层的内部节点都靠这条
//     筛掉。非激活页的**子孙** Box 是上一帧的残留值, 所以必须剪整支而不是
//     只跳过这一层;
//   - **与窗口矩形不相交**: 滚动容器里滚出视口的控件不该能被 Tab 到
//     (否则键盘用户会"跳进看不见的地方")。
func a11yCollectFocusable(n *GuiNode, win Rect, out *[]*GuiNode) {
	if n == nil {
		return
	}
	if n.isOverlay() && !n.overlayVisible() {
		return
	}
	if n.Tag == "#text" {
		return
	}
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	if !rectsOverlap(n.Box, win) {
		return
	}
	if n.a11yHidden() {
		return
	}
	if n.focusable() && n.tabIndexOf() >= 0 && !n.disabledInChain() {
		*out = append(*out, n)
	}
	for _, c := range zOrderedChildren(n) {
		if c.escapeClipping() {
			continue // 弹层内部结构不进 Tab 序 (见上)
		}
		a11yCollectFocusable(c, win, out)
	}
}

// a11yRadioRoving 把一组 radio 收成一个 Tab 停留点 (roving tabindex)。
//
// 这是 ARIA radiogroup 的标准交互: **一组单选只占一次 Tab**, 组内用方向键
// 移动 (并且"选中跟着焦点走")。不做这件事的话, 一个 5 项的单选组要按 5 次
// Tab 才能走过去 —— 键盘用户最常抱怨的正是这种。
//
// 留在序里的是"当前选中的那个"; 一个都没选中时留第一个 (与浏览器行为一致:
// 首次 Tab 进组会落在第一项上)。
func a11yRovingRadios(list []*GuiNode) []*GuiNode {
	groups := map[string][]int{}
	var keys []string
	for i, n := range list {
		if n.Tag != "radio" {
			continue
		}
		k := a11yRadioGroupKey(n)
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], i)
	}
	drop := map[int]bool{}
	for _, k := range keys {
		idx := groups[k]
		if len(idx) < 2 {
			continue
		}
		keep := idx[0]
		for _, i := range idx {
			if list[i].a11yCheckedValue() {
				keep = i
				break
			}
		}
		for _, i := range idx {
			if i != keep {
				drop[i] = true
			}
		}
	}
	if len(drop) == 0 {
		return list
	}
	out := make([]*GuiNode, 0, len(list)-len(drop))
	for i, n := range list {
		if !drop[i] {
			out = append(out, n)
		}
	}
	return out
}

// a11yRadioGroupKey 给一组 radio 定分组键: 显式 name prop 优先 (跨容器也能
// 成组, 与 HTML 的 <input type=radio name=…> 一致), 否则用父容器。
//
// 用父容器兜底而不是"整棵树一组": 没有 name 的 radio 往往就是并排的几个
// 选项, 父容器正是最自然的组边界; 而"整棵树一组"会把两个不相干的单选组
// 串成一个, 方向键会跨组乱跳。
func a11yRadioGroupKey(n *GuiNode) string {
	if s, ok := n.PropStr("name"); ok && s != "" {
		return "name:" + s
	}
	if n.Parent == nil {
		return "root"
	}
	return fmt.Sprintf("parent:%p", n.Parent)
}

// a11yCheckedValue 返回 radio 是否处于选中态。
//
// radio 的选中态是**派生**的 (model.go 里写死了: model() === value), 所以
// 优先看脚本惯用的 checked prop, 没有再看 value 与组内 model 的关系 ——
// 后者内核读不到 (model 是 h() 里展开成 checked prop 的), 因此这里只看
// checked, 与绘制层 (n.checked()) 同一口径。
func (n *GuiNode) a11yCheckedValue() bool { return n.checked() }

// a11ySortByTabIndex 按 tabIndex 排序: 正数升序在前, 0 保持树序在后。
func a11ySortByTabIndex(list []*GuiNode) []*GuiNode {
	var pos, zero []*GuiNode
	for _, n := range list {
		t := n.tabIndexOf()
		switch {
		case t > 0:
			pos = append(pos, n)
		case t == 0:
			zero = append(zero, n)
		}
	}
	sort.SliceStable(pos, func(i, j int) bool { return pos[i].tabIndexOf() < pos[j].tabIndexOf() })
	if len(pos) == 0 {
		return zero
	}
	return append(pos, zero...)
}

// a11yRadioGroup 返回与 r 同组的全部 radio (含 r 自己, 按树序, 已滤掉禁用项)。
//
// 全量重扫而不是"只看兄弟": name 分组的 radio 可以跨容器 (比如两个 column
// 各放两个选项), 只在兄弟里找会漏。树的规模由 vlist 保证在可见窗口内,
// 一次按键扫一遍是 O(可见节点), 与一次 Tab 同量级。
func a11yRadioGroup(root, r *GuiNode) []*GuiNode {
	if root == nil || r == nil {
		return nil
	}
	key := a11yRadioGroupKey(r)
	var out []*GuiNode
	var walk func(*GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.isOverlay() && !n.overlayVisible() {
			return
		}
		if n.Box.W <= 0 || n.Box.H <= 0 || n.a11yHidden() {
			return
		}
		if n.Tag == "radio" && a11yRadioGroupKey(n) == key && !n.disabledInChain() {
			out = append(out, n)
			return
		}
		for _, c := range zOrderedChildren(n) {
			walk(c)
		}
	}
	walk(root)
	return out
}

// ===== 键盘交互 (app 侧) =====

// a11yTab 处理 Tab / Shift+Tab: 把焦点移到遍历序里的下一个/上一个, 绕回。
// 返回是否消费了这次按键 (遍历序为空时不消费, 按键照旧给脚本)。
func (a *app) a11yTab(back bool) bool {
	root := a.rootNode()
	if root == nil {
		return false
	}
	// 展开中的下拉先收起: Tab 是"离开这个字段"的动作, 弹层跟着走会让人
	// 以为焦点还在下拉里。
	a.mu.Lock()
	cur := a.focused
	a.mu.Unlock()
	if sel := selectInChain(cur); sel != nil && sel.expanded {
		a.closeSelect(sel)
	}
	order := a11yFocusOrder(root)
	if len(order) == 0 {
		return false
	}
	idx := -1
	for i, n := range order {
		if n == cur {
			idx = i
			break
		}
	}
	var next *GuiNode
	switch {
	case idx < 0 && back:
		next = order[len(order)-1] // 焦点不在序里 (含 nil): 反向从末尾进
	case idx < 0:
		next = order[0]
	default:
		step := 1
		if back {
			step = -1
		}
		next = order[(idx+step+len(order))%len(order)]
	}
	a.setFocus(next)
	return true
}

// a11yHandleKey 处理无障碍层的按键 (Tab 之外的部分), 返回是否已消费。
//
// 消费纪律与内核其它内置按键处理一致: **只在真的做了事的时候消费**
// (select 的 Enter/Space、菜单的箭头、Esc 的兜底都是这个口径)。没做事就
// 返回 false, 按键继续走 script 的 onKeyDown —— 于是"组件语义"与
// "脚本自己处理键盘"两条路不会互相吞掉。
//
// 带 Ctrl/Alt 的组合键一律不碰: 那是快捷键的地盘 (Ctrl+← 在输入框里是
// "按词移动光标"这类未来语义), 而且 handleShortcut 已经排在前面 ——
// 这里再拦一道只是让"没注册成快捷键的 Ctrl+方向键"也不会误改控件值。
func (a *app) a11yHandleKey(n *GuiNode, key string, ev Event) bool {
	if n == nil || ev.Ctrl || ev.Alt {
		return false
	}
	switch key {
	case "Enter", " ":
		return a.a11yActivate(n)
	case "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown":
		return a.a11yArrow(n, key)
	}
	return false
}

// a11yActivatableRoles 是"Enter/空格 会激活它"的 role 集合。
//
// 这条判据刻意**不看标签而看 role**: 浏览器里 Enter 触发 click 的是
// button/checkbox/radio 这类**原生控件**, 一个普通的 <div onclick> 按
// Enter 什么都不会发生 —— 把"能点"当成"能键盘激活"是过度推断。自定义控件
// 想拿到这个语义, 显式写 role 即可 (`<row role="button" focusable onClick>`),
// 于是通用容器 (rect/row/column/view) 的 Enter 照旧留给脚本自己的 onKeyDown。
//
// slider 不在表里: 滑块的键盘语义是**方向键调值**, 不是"激活" (ARIA 里
// slider 也没有默认激活动作)。rating 的隐式 role 同样是 slider, 一并排除。
var a11yActivatableRoles = map[string]struct{}{
	"button": {}, "checkbox": {}, "radio": {}, "switch": {}, "link": {},
	"menuitem": {}, "menuitemcheckbox": {}, "menuitemradio": {},
	"option": {}, "tab": {}, "treeitem": {}, "combobox": {}, "spinbutton": {},
}

// a11yActivatableTags 是"隐式 role 不在上表、但 Enter/空格 该激活它"的标签。
// 目前只有 upload: 它的隐式 role 是 group (一组文件), 而组本身不该激活 ——
// 真正可激活的是它内部那个"选择文件"区域, 但那个区域是组件自绘的 (没有独立
// 节点), 所以这里让 upload 自己承担激活语义 (它的内置 onClick 就是弹选择框)。
var a11yActivatableTags = map[string]struct{}{"upload": {}}

// a11yActivatable 报告这个节点是否有"Enter/空格 激活"的语义。
//
// 判据走 **role** 而不是"标签是否可点": 文本字段 (textbox/searchbox) 的隐式
// role 不在激活表里, 于是"输入框上挂了 onClick 时按 Enter 顺手把它触发一次"
// 这种意外不会发生 —— Enter 在单行输入框里是"提交/换行"的语义位, 属于脚本
// 或组件 (search 的 onSearch) 的地盘。
func (n *GuiNode) a11yActivatable() bool {
	if n == nil {
		return false
	}
	if _, ok := a11yActivatableTags[n.Tag]; ok {
		return true
	}
	_, ok := a11yActivatableRoles[n.AriaRole()]
	return ok
}

// a11yActivate 是 Enter/空格 的"激活"语义: 等价于用鼠标点一下这个控件。
//
// 实现刻意与鼠标路径共用同一个出口 —— 派发节点自己的 onClick。checkbox /
// switch 的取反、radio 的选中、select 与 datepicker 的展开全挂在 onClick 上
// (见 model.go 与 attachSelectHandler), 所以这里不必为每个控件写一份
// "键盘版"逻辑; 写两份的结局必然是某天只有鼠标那半被改动。
//
// 沿祖先链找最近的"可激活"节点: 焦点常常落在控件的**文本子节点**上
// (命中测试返回的是带处理器的节点, 但 #text 也可能被直接聚焦), 只看
// 焦点节点自己会漏掉"聚焦在按钮文字上按回车"。
func (a *app) a11yActivate(n *GuiNode) bool {
	for p := n; p != nil; p = p.Parent {
		if !p.a11yActivatable() {
			continue
		}
		if p.disabledInChain() {
			return false
		}
		h := p.PropHandler("onClick")
		if h == nil {
			return false
		}
		a.setFocus(p)
		a.callHandlerValue(h, "onClick", nil)
		return true
	}
	return false
}

// a11yArrow 是方向键的组件语义分派。
func (a *app) a11yArrow(n *GuiNode, key string) bool {
	switch {
	case n.Tag == "radio":
		return a.a11yRadioArrow(n, key)
	case n.Tag == "slider":
		return a.a11ySliderArrow(n, key)
	case n.Tag == "rating":
		return a.a11yRatingArrow(n, key)
	case n.Tag == "tabs":
		return a.a11yTabsArrow(n, key)
	case n.Tag == "pagination":
		return a.a11yPaginationArrow(n, key)
	}
	return false
}

// a11yRadioArrow 在 radio 组内移动 + 选中 ("选中跟着焦点走", ARIA 约定)。
func (a *app) a11yRadioArrow(r *GuiNode, key string) bool {
	group := a11yRadioGroup(a.rootNode(), r)
	if len(group) < 2 {
		return false
	}
	idx := -1
	for i, n := range group {
		if n == r {
			idx = i
			break
		}
	}
	if idx < 0 {
		return false
	}
	switch key {
	case "ArrowRight", "ArrowDown":
		idx = (idx + 1) % len(group)
	case "ArrowLeft", "ArrowUp":
		idx = (idx - 1 + len(group)) % len(group)
	default:
		return false
	}
	next := group[idx]
	a.setFocus(next)
	a.a11yActivate(next)
	return true
}

// a11ySliderArrow 用方向键把滑块挪一格 (step)。
//
// 起点取 slideVal (上一次**派发出去**的值) 而不是 value prop: 受控滑块在
// 脚本没回写时 prop 恒为初值, 拿它当起点会让连按方向键一直停在"初值 ±1",
// 键盘怎么按都挪不动。这与 sliderDrag 的去重口径是同一件事 (那边注释里
// 写了"不能拿 value prop 来比"), 两者共用同一份记账.
func (a *app) a11ySliderArrow(n *GuiNode, key string) bool {
	min, max := sliderMin(n), sliderMax(n)
	step := sliderStep(n)
	if step <= 0 {
		step = 1
	}
	delta := 0.0
	switch key {
	case "ArrowRight", "ArrowUp":
		delta = step
	case "ArrowLeft", "ArrowDown":
		delta = -step
	default:
		return false
	}
	cur := sliderValue(n)
	if n.slideValSet {
		cur = n.slideVal
	}
	v := sliderQuantize(cur+delta, min, max, step)
	if v == cur {
		return true // 已经到边界: 消费掉按键 (不给脚本), 但不再派发
	}
	n.slideVal = v
	n.slideValSet = true
	a.sliderEdited(n, v)
	return true
}

// a11yRatingArrow 用方向键加减一颗星 (完全受控, 与鼠标点击同一出口)。
func (a *app) a11yRatingArrow(n *GuiNode, key string) bool {
	max := n.ratingMax()
	cur := int(n.ratingValue())
	switch key {
	case "ArrowRight", "ArrowUp":
		cur++
	case "ArrowLeft", "ArrowDown":
		cur--
	default:
		return false
	}
	if cur < 0 {
		cur = 0
	}
	if cur > max {
		cur = max
	}
	a.ratingGo(n, cur)
	return true
}

// a11yTabsArrow 用 ←/→ 切页 (Home/End 分别是第一页/最后一页)。
func (a *app) a11yTabsArrow(tb *GuiNode, key string) bool {
	pages := tb.tabsPages()
	if len(pages) == 0 {
		return false
	}
	idx := tb.tabsActiveIndex()
	switch key {
	case "ArrowRight", "ArrowDown":
		idx = (idx + 1) % len(pages)
	case "ArrowLeft", "ArrowUp":
		idx = (idx - 1 + len(pages)) % len(pages)
	default:
		return false
	}
	a.tabsSwitch(tb, idx)
	return true
}

// a11yPaginationArrow 用 ←/→ 翻页 (Home/End 到首/末页)。
func (a *app) a11yPaginationArrow(pg *GuiNode, key string) bool {
	pages := pg.paginationPageCount()
	cur := pg.paginationCurrent()
	switch key {
	case "ArrowRight":
		cur++
	case "ArrowLeft":
		cur--
	default:
		return false
	}
	if cur < 1 || cur > pages {
		return true // 边界: 消费但不派发
	}
	a.paginationGo(pg, cur)
	return true
}

// ===== 焦点校正 =====

// a11yReconcileFocus 在每轮事件处理末尾检查"焦点是否还站得住", 站不住就
// 把焦点交给当前遍历序里的第一个可聚焦节点。
//
// 为什么需要它: 焦点是个**可悬空的指针**。弹层打开 (焦点被盖在遮罩下)、
// 弹层关闭 (焦点节点整支不绘制)、条件渲染把节点摘掉、脚本把控件 disabled
// —— 这些都不是"点了别的地方", 内核没有任何事件可以挂钩, 于是焦点会留在
// 一个不可见/不可用的节点上, 表现为"Tab 从这里继续走, 但焦点框看不见"。
// 校正放在每轮事件末尾 (布局框刚更新完), 是唯一能同时看到"最新几何"与
// "当前焦点"的位置。
//
// 只在焦点**非空且失效**时才做 O(树) 的遍历序计算 —— 绝大多数事件轮次
// (没有焦点, 或焦点好好的) 只付一次 O(深度) 的判定。
func (a *app) a11yReconcileFocus() {
	a.mu.Lock()
	cur := a.focused
	root := a.root
	a.mu.Unlock()
	if cur == nil || root == nil || cur == root {
		return // 没有元素级焦点 (焦点在根 = 点了空白): 不动
	}
	if a11yFocusValid(cur, root) {
		return
	}
	order := a11yFocusOrder(root)
	if len(order) == 0 {
		a.setFocus(nil)
		return
	}
	a.setFocus(order[0])
}

// a11yFocusValid 报告焦点节点当前是否还站得住 (可见 / 可用 / 没被弹层盖住)。
func a11yFocusValid(n, root *GuiNode) bool {
	if n == nil {
		return false
	}
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return false
	}
	if n.a11yHidden() || n.disabledInChain() {
		return false
	}
	if !focusPathVisible(n) {
		return false
	}
	if coveredByOverlay(n, root) {
		return false
	}
	return true
}

// ===== gx/a11y 模块 =====

func init() {
	object.RegisterBuiltinModule("gx/a11y", func() map[string]object.Value {
		return map[string]object.Value{
			"focusOrder": object.NewBuiltin("focusOrder", jsFocusOrder),
			"focusNode":  object.NewBuiltin("focusNode", jsFocusNode),
			"focusNext":  object.NewBuiltin("focusNext", jsFocusNext),
			"focusPrev":  object.NewBuiltin("focusPrev", jsFocusPrev),
			"roles":      object.NewBuiltin("roles", jsA11yRoles),
		}
	})
}

// jsFocusOrder 是 `focusOrder()` → 焦点遍历序的只读快照。
//
//	import { focusOrder } from "gx/a11y";
//	focusOrder();
//	// [{ role, name, description, tag, tabIndex, disabled, hint, box }...]
//
// 用途有三个 (都不是"给读屏用"—— 内核没有读屏桥):
//   - **回归**: `docs/accessibility.md` 的验收清单要求"纯键盘可完成填表→提交→
//     关弹窗", 这条链路在测试里就是"按 Tab 若干次 → 断言当前焦点是谁";
//   - **自检**: focusOrder() 里出现 name 为空的 button/图片, 就是漏了
//     aria-label —— 比"读屏用户反馈读不出东西"早得多;
//   - **官网画廊**: a11y 面板直接列出当前示例的焦点链, 比截图更能说明问题。
//
// 报的是**当前帧**的几何 (Box), 所以调用点要保证窗口已经渲染过一帧。
func jsFocusOrder(args ...object.Value) object.Value {
	a := currentApp()
	if a == nil {
		return object.NewArray(nil)
	}
	root := a.rootNode()
	order := a11yFocusOrder(root)
	a.mu.Lock()
	cur := a.focused
	a.mu.Unlock()
	out := make([]object.Value, 0, len(order))
	for _, n := range order {
		out = append(out, a11yNodeInfo(n, n == cur))
	}
	return object.NewArray(out)
}

// a11yNodeInfo 把一个节点摊成无障碍快照对象。
func a11yNodeInfo(n *GuiNode, focused bool) object.Value {
	o := object.NewObject()
	o.SetProperty("role", object.NewString(n.AriaRole()))
	o.SetProperty("name", object.NewString(n.AriaName()))
	o.SetProperty("tag", object.NewString(n.Tag))
	o.SetProperty("tabIndex", object.NewNumber(float64(n.tabIndexOf())))
	o.SetProperty("disabled", object.NewBoolean(n.disabledInChain()))
	o.SetProperty("focused", object.NewBoolean(focused))
	if d := n.AriaDescription(); d != "" {
		o.SetProperty("description", object.NewString(d))
	}
	// hint: 这个控件的键盘用法 (方向键 / Enter), 让画廊面板能直接显示
	// "怎么用键盘操作它"。返回空串表示"只有 Tab 停留, 没有专属键"。
	if h := a11yKeyHint(n); h != "" {
		o.SetProperty("keyHint", object.NewString(h))
	}
	box := object.NewObject()
	box.SetProperty("x", object.NewNumber(float64(n.Box.X)))
	box.SetProperty("y", object.NewNumber(float64(n.Box.Y)))
	box.SetProperty("width", object.NewNumber(float64(n.Box.W)))
	box.SetProperty("height", object.NewNumber(float64(n.Box.H)))
	o.SetProperty("box", box)
	return o
}

// a11yKeyHint 返回控件的键盘用法说明 (文档与画廊共用一份口径)。
func a11yKeyHint(n *GuiNode) string {
	switch n.Tag {
	case "radio":
		return "方向键在本组内移动并选中"
	case "slider":
		return "方向键按一步（step）增减"
	case "rating":
		return "方向键加减一颗星"
	case "tabs":
		return "←/→ 切换页签"
	case "pagination":
		return "←/→ 翻页"
	case "select", "datepicker", "colorpicker":
		return "Enter/空格 展开, 方向键选择, Esc 收起"
	case "upload":
		return "Enter/空格 打开文件选择对话框"
	case "checkbox", "switch":
		return "空格/Enter 切换"
	case "button":
		return "空格/Enter 触发"
	case "input", "search", "textarea":
		return "直接输入; 上下左右编辑, Tab 离开"
	}
	return ""
}

// jsFocusNode 是 `focusNode(el)` → boolean: 程序化把焦点给某个节点 (等价于
// DOM 的 el.focus())。节点不在当前树里 / 已禁用时返回 false。
func jsFocusNode(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewBoolean(false)
	}
	node, ok := args[0].(*GuiNode)
	if !ok {
		return object.NewTypeError("focusNode: element required")
	}
	a := appOfNode(node)
	if a == nil || node.disabledInChain() {
		return object.NewBoolean(false)
	}
	a.setFocus(node)
	return object.NewBoolean(true)
}

// jsFocusNext / jsFocusPrev 是 `focusNext()` / `focusPrev()` → boolean:
// 与按 Tab / Shift+Tab 完全同一条路径 (含绕回与弹层范围限制)。
func jsFocusNext(args ...object.Value) object.Value { return a11yMoveFocus(false) }

func jsFocusPrev(args ...object.Value) object.Value { return a11yMoveFocus(true) }

func a11yMoveFocus(back bool) object.Value {
	a := currentApp()
	if a == nil {
		return object.NewBoolean(false)
	}
	return object.NewBoolean(a.a11yTab(back))
}

// jsA11yRoles 是 `roles()` → 隐式 role 表 (标签 → role)。
// 官网文档与画廊表用它, 免得同一张表在文档里手抄一遍然后漂移。
func jsA11yRoles(args ...object.Value) object.Value {
	keys := make([]string, 0, len(a11yImplicitRole))
	for k := range a11yImplicitRole {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]object.Value, 0, len(keys))
	for _, tag := range keys {
		row := object.NewObject()
		row.SetProperty("tag", object.NewString(tag))
		row.SetProperty("role", object.NewString(a11yImplicitRole[tag]))
		if h := a11yKeyHint(&GuiNode{Tag: tag}); h != "" {
			row.SetProperty("keyHint", object.NewString(h))
		}
		out = append(out, row)
	}
	return object.NewArray(out)
}
