package gfx

// ===== "字段 + 贴字段弹层" 这一族组件的公共机制 (T08) =====
//
// select / datepicker / colorpicker 三个组件的展开态语义**完全一样**:
//
//	展开: 收掉其它展开中的同族字段 → 把弹层挂在自己名下 → 整帧标脏;
//	收起: 焦点若落在即将销毁的弹层上就回收给字段 → 销毁弹层 → 整帧标脏;
//	点外面 / Esc: 收起最上面那个。
//
// 逐组件各写一份的代价不是重复十来行, 而是"**先收掉其它弹层**"这一条极易漏:
// 漏了的症状是"点开日历, 下拉框还开在旁边", 而 Esc 兜底与"点外面收起"两条路
// 各自又都要再判一次。所以这块机制只留一个实现点。
//
// 弹层本身的**内容**仍然归各组件自己 (buildSelectPopup / buildDatepickerPopup /
// buildColorpickerPopup): 它们只是被挂在这里约定的位置 (n.popup) 上。

// popupFieldTags 是这一族组件的标签集合。
//
// 进这张表意味着: ① 展开态与弹层指针复用 GuiNode 的 expanded/popup 两个字段;
// ② 打开时会先收掉表里其它成员的弹层; ③ 命中测试与 Esc 兜底把它的弹层当作
// "字段的一部分"。不含 label/form 这类结构性组件 —— 它们没有弹层。
var popupFieldTags = map[string]struct{}{
	"select": {}, "datepicker": {}, "colorpicker": {},
}

// isPopupField 报告节点是否属于这一族。
func (n *GuiNode) isPopupField() bool {
	if n == nil {
		return false
	}
	_, ok := popupFieldTags[n.Tag]
	return ok
}

// popupFieldInChain 从 n 起沿祖先链找第一个这一族的字段。
//
// 链式查找是必要的: 焦点/命中点可能落在弹层里的子节点上 (它的 Parent 链
// 会穿过弹层回到字段), 而调用方要的总是"这是哪个字段的弹层"。
func popupFieldInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.isPopupField() {
			return p
		}
	}
	return nil
}

// expandedPopupFields 收集树中所有处于展开态的字段 (DFS 序, 先出现的在前)。
func expandedPopupFields(root *GuiNode) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.isPopupField() && n.expanded {
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

// openPopupField 把 popup 挂成 n 的展开弹层, 返回是否真的打开了。
//
// 整帧标脏而不是只标字段自己: 弹层新覆盖的那片区域不属于任何"框变了的节点",
// 局部重绘的脏矩形表达不了"凭空多出一块", 硬凑只会漏画。展开弹层是低频的
// 用户动作, 整帧重绘的代价可以接受。
//
// 弹层必须已经挂进 n.Children (由各组的 buildXxxPopup 完成) —— 这里只管
// 状态与脏标记, 不碰树的形状。
func (a *app) openPopupField(n *GuiNode, popup *GuiNode) bool {
	if n == nil || popup == nil || n.expanded {
		return false
	}
	for _, other := range expandedPopupFields(a.rootNode()) {
		a.closePopupField(other)
	}
	n.expanded = true
	n.popup = popup
	markFullDirtyFor(n)
	return true
}

// closePopupField 收起弹层并销毁它 (disposeNode 会递归注销弹层子树上的
// effect, 并把它从 n.Children 里摘掉)。
//
// 焦点回收必须做: 焦点可能正落在即将销毁的弹层内容上, 不清的话它会指向一个
// 已经不在树上的节点, 键盘事件从此无处可去 (症状是"关掉日历后键盘全哑")。
//
// highlight 一并复位 (-1 = 无光标): 它是这一族共用的"弹层光标"字段
// (select 的选项下标 / datepicker 的日子 / colorpicker 的色块下标),
// 留着上一轮的值会让下次展开时"凭空有个高亮"。
func (a *app) closePopupField(n *GuiNode) {
	if n == nil || !n.expanded {
		return
	}
	n.expanded = false
	n.highlight = -1
	popup := n.popup
	n.popup = nil
	if popup == nil {
		return
	}
	a.mu.Lock()
	f := a.focused
	a.mu.Unlock()
	if f != nil && underNode(f, popup) {
		a.setFocus(n)
	}
	disposeNode(popup)
	markFullDirtyFor(n)
}

// closeTopPopupField 收起最上面那个展开中的字段弹层, 返回是否收掉了。
// Esc 兜底用 (排在对话框之前: 弹层是更表层的交互)。
func (a *app) closeTopPopupField() bool {
	list := expandedPopupFields(a.rootNode())
	if len(list) == 0 {
		return false
	}
	a.closePopupField(list[len(list)-1])
	return true
}

// closePopupFieldOnOutsideClick 若有展开中的弹层且 (x,y) 落在**它的弹层之外**,
// 收起它并返回 true。
//
// 同时只会处理一个: 打开新弹层前旧的一定已经收起了 (见 openPopupField),
// 所以树上最多只有一个展开的弹层。
//
// 判据用弹层盒而不是字段盒: 点字段本身是"切换"(由字段自己的 on* 处理器接管),
// 点弹层内部是"在弹层里操作", 两者都不该被当成外部点击。
func (a *app) closePopupFieldOnOutsideClick(root *GuiNode, x, y int) bool {
	for _, f := range expandedPopupFields(root) {
		if f.popup != nil && f.popup.Box.Contains(x, y) {
			continue
		}
		a.closePopupField(f)
		return true
	}
	return false
}
