package gfx

// 命中测试: 按布局框从顶到底找 onClick 目标。
// 同层后声明的子节点绘制在上层, 因此倒序遍历; 命中即停 (v1 不冒泡)。

// HitTest 返回 (x, y) 处最内层带 onClick 处理器的节点。
func HitTest(root *GuiNode, x, y int) *GuiNode {
	if root == nil || !root.Box.Contains(x, y) {
		return nil
	}
	return hitNode(root, x, y)
}

// hitNode 在 n 的子树内命中测试; 返回带 handler 的最深节点, 无则 nil。
func hitNode(n *GuiNode, x, y int) *GuiNode {
	// 后声明的子节点绘制在上层, 倒序遍历; 命中某子节点即进入其子树,
	// 不再穿透到低层兄弟 (视觉上点击落在最上层元素)。
	for i := len(n.Children) - 1; i >= 0; i-- {
		c := n.Children[i]
		if c.Box.Contains(x, y) {
			return hitNode(c, x, y)
		}
	}
	if n.PropHandler("onClick") != nil {
		return n
	}
	return nil
}
