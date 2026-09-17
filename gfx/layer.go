package gfx

import "sort"

// 层叠模型 (P2-2): zIndex 绘制/命中顺序、position=absolute 脱离常规流、
// escapeClipping 逃逸父级裁剪。
//
// 绘制与命中测试必须用同一套顺序, 否则会出现"看到的和点到的不是一层"
// —— 这是层叠类组件最难查的一类 bug, 所以两个顺序都由本文件提供。
//
// 约定:
//   - 同层内先按 zIndex 升序, 相同 zIndex 保持声明序 (稳定排序, 后声明在上)。
//     Children 数组本身不被改动, 排序只影响本轮的绘制/命中遍历顺序。
//   - position="absolute" 的子节点脱离常规流, 按 left/top 相对父内容区定位,
//     且默认仍被父节点盒子裁剪 (弹层需要显式 escapeClipping 才能溢出)。
//   - escapeClipping 为 true 的子树被收集到"根层级", 在所有常规内容之后绘制,
//     且不受任何祖先盒子裁剪 (下拉框/对话框/toast 这类弹层的出口)。

// ===== 节点语义 (props 读取) =====

// positionAbsolute 报告节点是否脱离常规流 (position="absolute")。
func (n *GuiNode) positionAbsolute() bool {
	v, _ := n.PropStr("position")
	return v == "absolute"
}

// zIndexOf 读取同层绘制顺序 (默认 0; 值越大越靠上)。
func (n *GuiNode) zIndexOf() int {
	v, _ := n.PropNum("zIndex")
	return int(v)
}

// escapeClipping 报告该子树是否逃逸祖先裁剪 (收集到根层级最后绘制)。
// 弹层标签 (dialog/toast) 天生需要逃逸, 由 isOverlay 隐式打开。
func (n *GuiNode) escapeClipping() bool {
	if n.isOverlay() {
		return true
	}
	v, _ := n.PropBool("escapeClipping")
	return v
}

// isOverlay 报告标签是否天生是"覆盖在内容之上"的弹层: 不占常规流、
// 逃逸裁剪、自动拿到高层 zIndex (见 drawOrderKey 的注释)。
func (n *GuiNode) isOverlay() bool {
	switch n.Tag {
	case "dialog", "toast":
		return true
	}
	return false
}

// isModal 报告节点是否是"拦截其下全部交互"的模态弹层。
// 目前只有 dialog: toast 是非模态的, 点它下面的东西应当照常生效。
func (n *GuiNode) isModal() bool {
	return n.Tag == "dialog"
}

// isFlowChild 报告子节点是否参与父容器的常规流分配 (尺寸累加 / 位置排布)。
// 绝对定位与弹层都不占位。
func (n *GuiNode) isFlowChild() bool {
	return !n.positionAbsolute() && !n.isOverlay()
}

// absoluteOffset 读取绝对定位的 left/top 偏移 (默认 0, 相对父内容区)。
func (n *GuiNode) absoluteOffset() (left, top int) {
	l, _ := n.PropNum("left")
	t, _ := n.PropNum("top")
	return int(l), int(t)
}

// ===== 遍历顺序 =====

// zOrderedChildren 返回按绘制顺序 (从底到顶) 排列的子节点。
// 全部 zIndex 为 0 时直接返回原切片, 避免每帧无谓分配。
func zOrderedChildren(n *GuiNode) []*GuiNode {
	if !needsZSort(n.Children) {
		return n.Children
	}
	kids := append([]*GuiNode(nil), n.Children...)
	sort.SliceStable(kids, func(i, j int) bool {
		return kids[i].drawOrderKey() < kids[j].drawOrderKey()
	})
	return kids
}

// needsZSort 判断是否需要排序 (任一子节点带非零 zIndex 或弹层标签)。
func needsZSort(children []*GuiNode) bool {
	for _, c := range children {
		if c.zIndexOf() != 0 || c.isOverlay() {
			return true
		}
	}
	return false
}

// drawOrderKey 是排序键: 弹层标签整体抬到最高一层 (它们永远盖住普通内容,
// 不必让脚本给每个 dialog 手写一个大 zIndex), 同层内再比 zIndex。
func (n *GuiNode) drawOrderKey() int {
	k := n.zIndexOf()
	if k < 0 {
		k = 0
	}
	if n.isOverlay() {
		k += overlayZBase
	}
	return k
}

// overlayZBase 是弹层标签的层级基线: 脚本的 zIndex 只需在各自区间内
// 有意义 —— 普通内容 < 弹层, 且弹层之间仍可用 zIndex 互相压盖。
const overlayZBase = 1 << 20

// escapesInDrawOrder 按绘制顺序返回整棵树里的逃逸裁剪子树。
//
// 顺序必须与 DrawClipped 的 worklist 完全一致 (命中测试要倒着遍历):
// 先在常规树里按 z 序 DFS 收集, 再对收集到的弹层本身继续收集 —— 弹层里
// 嵌弹层时, 内层是"画在外层之后"的, 而不是"紧跟在外层后面"。
//
// 不透明的关闭弹层不下钻: drawNode 对关闭的弹层直接返回, 它的后代根本
// 没进绘制队列, 命中测试自然也不该把它们算进来 (两边必须同进同出)。
func escapesInDrawOrder(root *GuiNode) []*GuiNode {
	var out []*GuiNode
	collectEscapes(root, &out)
	for i := 0; i < len(out); i++ {
		if out[i].isOverlay() && !out[i].overlayVisible() {
			continue
		}
		collectEscapes(out[i], &out)
	}
	return out
}

// collectEscapes 把 n 的逃逸子孙按 z 序 DFS 追加到 out; 遇到逃逸子树即
// 停止下钻 (它内部留到下一轮), 与 drawNode 的收集规则逐条对应。
func collectEscapes(n *GuiNode, out *[]*GuiNode) {
	for _, c := range zOrderedChildren(n) {
		if c.escapeClipping() {
			*out = append(*out, c)
			continue
		}
		collectEscapes(c, out)
	}
}

// ===== 弹层可见性与遮盖判定 =====

// overlayVisible 报告弹层当前是否可见: 有 open prop 就按它 (dialog 的常态),
// 没有则"挂在树上即可见" (toast 由 JS 侧挂载/卸载控制)。
func (n *GuiNode) overlayVisible() bool {
	if v, ok := n.PropBool("open"); ok {
		return v
	}
	return true
}

// coveredByOverlay 报告节点 n 是否被某个弹层盖住。
//
// 判据是**几何重叠**而不是"树里有没有弹层": 焦点虚线框画在 Draw 之后,
// 会浮在遮罩之上, 焦点节点被盖住时那个框就是"透过遮罩的幽灵", 必须跳过。
// 但反过来说, 一个贴在右上角的 toast 与窗口中间的按钮并不重叠, 那时抑制
// 焦点框就是误判 —— 用 toast 做全局提示是很常见的用法。
func coveredByOverlay(n, root *GuiNode) bool {
	if n == nil || root == nil {
		return false
	}
	for _, o := range escapesInDrawOrder(root) {
		if !o.isOverlay() || !o.overlayVisible() {
			continue
		}
		if underNode(n, o) {
			continue // 焦点就在这个弹层里, 不是"被盖住"
		}
		if rectsOverlap(o.Box, n.Box) {
			return true
		}
	}
	return false
}

// underNode 报告 n 是否位于 sub 子树内 (含 sub 自身)。
func underNode(n, sub *GuiNode) bool {
	for p := n; p != nil; p = p.Parent {
		if p == sub {
			return true
		}
	}
	return false
}

// focusPathVisible 报告从 n 到根的路径上有没有"不可见的弹层"。
// dialog 关闭只设 open=false 而节点仍在树上, 整支都不绘制 —— 焦点若还留在
// 它内部的控件上, 虚线框也不能自己冒出来。
func focusPathVisible(n *GuiNode) bool {
	for p := n; p != nil; p = p.Parent {
		if p.isOverlay() && !p.overlayVisible() {
			return false
		}
	}
	return true
}

// rectsOverlap 判断两矩形是否有交集 (任一边长 <=0 视为不相交)。
func rectsOverlap(a, b Rect) bool {
	if a.W <= 0 || a.H <= 0 || b.W <= 0 || b.H <= 0 {
		return false
	}
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
