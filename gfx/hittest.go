package gfx

// 命中测试: 按布局框从顶到底找 onClick 目标。
// 同层后声明的子节点绘制在上层, 因此倒序遍历; 命中即停 (v1 不冒泡)。
//
// 遍历顺序必须与 raster.go 的绘制顺序完全一致 (都由 layer.go 的
// zOrderedChildren / escapesIn 提供): "看到的"和"点到的"错层是层叠类
// 组件最难排查的一类 bug。

// HitTest 返回 (x, y) 处最内层带 onClick 处理器的节点。
func HitTest(root *GuiNode, x, y int) *GuiNode {
	if root == nil || !root.Box.Contains(x, y) {
		return nil
	}
	return hitEscapesLayer(root, x, y, hitNode)
}

// hitNode 在 n 的子树内命中测试; 返回带 handler 的最深节点, 无则 nil。
func hitNode(n *GuiNode, x, y int) *GuiNode {
	// 后声明的子节点绘制在上层, 倒序遍历。子树内没有处理器时继续往下层找,
	// 最后才判断自身: 文本子节点自身没有 onClick, 若不回溯, 点中按钮上的
	// 文字就会丢掉整次点击 (按钮的 Box 会随内容布局而覆盖文字)。
	kids := zOrderedChildren(n)
	for i := len(kids) - 1; i >= 0; i-- {
		c := kids[i]
		if c.escapeClipping() {
			continue // 弹层已由 hitEscapesLayer 在顶层统一处理
		}
		if !hittableIn(c, n, x, y) {
			continue
		}
		if h := hitNode(c, x, y); h != nil {
			return h
		}
	}
	if n.PropHandler("onClick") != nil {
		return n
	}
	return nil
}

// HitTestDeep 返回 (x, y) 处最内层的节点, 不论它有没有事件处理器。
// "光标下是什么" (hover / 事件冒泡起点) 与 "点了会触发谁" (HitTest) 是
// 两个问题: 悬停在按钮的文字上, 命中的是 #text 子节点, 事件再沿祖先链上浮。
func HitTestDeep(root *GuiNode, x, y int) *GuiNode {
	if root == nil || !root.Box.Contains(x, y) {
		return nil
	}
	return hitEscapesLayer(root, x, y, hitDeep)
}

// hitDeep 返回子树内最深的包含该点的节点 (调用方已确认 n 自身包含该点)。
func hitDeep(n *GuiNode, x, y int) *GuiNode {
	kids := zOrderedChildren(n)
	for i := len(kids) - 1; i >= 0; i-- {
		c := kids[i]
		if c.escapeClipping() {
			continue // 弹层已由 hitEscapesLayer 在顶层统一处理
		}
		if !hittableIn(c, n, x, y) {
			continue
		}
		if hit := hitDeep(c, x, y); hit != nil {
			return hit
		}
	}
	return n
}

// hitEscapesLayer 在整个树的弹层里倒序命中, 命中则返回, 否则回落到常规树。
//
// 弹层必须在这里"一次找全"而不是在各层递归里顺手找: 弹层被提升到根层级
// 绘制, 完全不受祖先盒子裁剪, 因此哪怕祖先的盒子不包含该点 (最常见的就是
// 28px 高的 select 下面挂一个下拉框), 它的弹层依然是可命中的。
// 倒序遍历 = 倒着绘制, 与 escapesInDrawOrder 的顺序配合保证"最上面的先命中"。
//
// 模态弹层 (dialog) 吃掉落在它身上的点击: 遮罩的作用就是"其下内容不可达",
// 若这里返回 nil 后继续下钻到常规树, 就会点到遮罩下面的按钮 —— 看得见的
// 遮罩挡不住看不见的点击, 这是模态实现里最容易漏的一条。
// 非模态弹层 (toast) 与普通逃逸子树 (下拉弹层) 缝隙里的点击照旧下钻。
func hitEscapesLayer(root *GuiNode, x, y int, recurse func(*GuiNode, int, int) *GuiNode) *GuiNode {
	esc := escapesInDrawOrder(root)
	for i := len(esc) - 1; i >= 0; i-- {
		e := esc[i]
		if !e.Box.Contains(x, y) {
			continue
		}
		if h := recurse(e, x, y); h != nil {
			return h
		}
		if e.isModal() && e.overlayVisible() {
			return nil
		}
	}
	return recurse(root, x, y)
}

// touchHitBox 是小控件的**命中区** (Prism S4 起, S8 扩触觉不重要全集):
// 视觉尺寸不变, 但命中区补到 TouchMin (44)。
//
// 为什么只在这里扩、不改 Box: 视觉尺寸与命中尺寸是两件事。checkbox 是 18×18,
// 而 Apple HIG / Material 与 WCAG 2.1 SC 2.5.5 的触控下限是 44 —— 把盒子放大到
// 44 会撑开整行 (一排复选框之间凭空多出 26px), 而扩大命中区只是让"点在图标
// 旁边一点"也算数。命中区重叠时的取舍按 z 序: 遍历是倒序的, 后声明的先命中。
//
// 哪些标签会被补, 分两类:
//   - **checkbox / radio**: 18×18, 无条件补。这是 S4 就发出去的既有行为 (改它
//     会让已经依赖"点在图标边上"的脚本静默失效), 而且 18 连鼠标都不算宽裕;
//   - **其余交互控件** (button / switch / tag / icon / menu-item / list-item /
//     slider / rating …): 只在**触摸模式**下补 (见 touchmode.go)。它们 26~36 高,
//     对鼠标够用, 只有手指才需要 44 —— 无条件补会让"桌子上的表格行"与"桌上
//     的 Toolbar 按钮"互相吃点击, 那是比漏点更难查的一类 bug。
//
// 触觉模式下额外的一条约束: 只有**自己带 onClick** 的节点才参与扩展。命中区
// 扩展必然与邻居重叠, 让不带处理器的节点也参与, 只会把 HitTestDeep 的悬停归属
// (hover 探针走的是同一套通报序) 搅乱, 却换不来任何手指能摸到的收益。
func touchHitBox(n *GuiNode) Rect {
	if alwaysTouchExpand(n.Tag) {
		return TouchInflate(n.Box, true, true)
	}
	if !touchModeActive() || n.PropHandler("onClick") == nil {
		return n.Box
	}
	if touchRowTag(n.Tag) {
		return touchHitRectRow(n.Box)
	}
	if !touchExpandTag(n.Tag) {
		return n.Box
	}
	return TouchInflate(n.Box, true, true)
}

// alwaysTouchExpand 是"无条件补到 44"的标签 —— 只有视觉远小于任何输入设备
// 舒适尺寸的那一档 (checkbox / radio 的 18×18)。
func alwaysTouchExpand(tag string) bool {
	return tag == "checkbox" || tag == "radio"
}

// touchRowTag 是"整行"类标签: 只补垂直方向 (见 touchHitRectRow 的理由)。
func touchRowTag(tag string) bool {
	switch tag {
	case "list-item", "table-row", "tree-row", "menu-item":
		return true
	}
	return false
}

// touchExpandTag 是触觉模式下两轴都补的交互控件。
//
// slider / switch 这类"拖动型"控件一并包进来是刻意的: 拖动手势一旦在按下那
// 一判定成"没按到", 后面就没有第二次机会了 —— 而它们的起点常常落在 Thumb
// 之外的轨道边缘上。
func touchExpandTag(tag string) bool {
	switch tag {
	case "button", "switch", "slider", "checkbox", "radio", "tag",
		"icon", "avatar", "upload", "select", "input", "search",
		"textarea", "rating", "datepicker", "colorpicker", "table-cell":
		return true
	}
	return false
}

// hittableIn 判断子节点 c 是否在父节点 parent 内可命中 (x, y 在 c 的框内)。
// 绝对定位子节点默认被父盒裁剪, 露出父盒的部分画不出来, 也就不该被点中
// —— 这条与 drawNode 里 clipTo(父盒) 的行为一一对应。
func hittableIn(c, parent *GuiNode, x, y int) bool {
	if !touchHitBox(c).Contains(x, y) {
		return false
	}
	// 滚动容器: 视口之外的子内容既画不出来也不该被点中 (与 drawNode 里
	// clipTo(视口) 的行为一一对应)。
	if parent.Tag == "scroll" && !parent.scrollViewport().Contains(x, y) {
		return false
	}
	if c.positionAbsolute() && !parent.Box.Contains(x, y) {
		return false
	}
	return true
}

// handlerInChain 从 n 起沿祖先链找第一个带 name 处理器的节点, 无则 nil。
// 事件冒泡策略与 onKeyDown 一致 (v1 不做 stopPropagation / capture)。
func handlerInChain(n *GuiNode, name string) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.PropHandler(name) != nil {
			return p
		}
	}
	return nil
}
