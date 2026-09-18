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

// hittableIn 判断子节点 c 是否在父节点 parent 内可命中 (x, y 在 c 的框内)。
// 绝对定位子节点默认被父盒裁剪, 露出父盒的部分画不出来, 也就不该被点中
// —— 这条与 drawNode 里 clipTo(父盒) 的行为一一对应。
func hittableIn(c, parent *GuiNode, x, y int) bool {
	if !c.Box.Contains(x, y) {
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
