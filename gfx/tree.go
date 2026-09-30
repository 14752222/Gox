package gfx

import (
	"image"
	"image/color"

	"github.com/14752222/Gox/object"
)

// tree 树形控件 (S4 组件库二期)。
//
// 用法 (递归数据驱动 —— 不要求脚本拼节点):
//
//	<tree nodes={[
//	  {label:"src", children:[
//	    {label:"main.go", key:"main"},
//	    {label:"gfx", children:[{label:"node.go"}]},
//	  ]},
//	  {label:"README.md", key:"readme"},
//	]} onSelect={(node) => ...}/>
//
// 结构约定:
//   - nodes 是数据, 由 Go 侧展开成 tree-row 节点树 (脚本写不到, 但登记进
//     knownTags 以免内部构造被当成未知标签)。每个可见节点一行, 行高 28
//     (与表格/字段控件同一套常量)。
//   - 展开态是**节点的运行时状态** (treeOpen), 不来自 props: 与 select 的
//     expanded、tooltip 的 tipPopup 同类 —— 点箭头展开/收起是渲染层的临时
//     UI 状态, 不需要脚本回写 (想受控可用 onToggle 自己记)。
//   - 展开/收起只改变"哪些行物化", 不做重排动画 (v1 与 select 弹层一致:
//     立即切换, 整帧标脏)。
//   - 缩进 = 深度 × treeIndent + 前导箭头占位, 保证同层节点文字左对齐;
//     叶子节点不画箭头, 但**保留占位宽度** (否则叶子与父节点的文字会错位)。
//
// 为什么不内置搜索/多选/拖拽: 那是 IDE 级树控件的领域, 与"提供 tree 的
// 节点组件封装"这个目标(见任务描述)不匹配。这里给最小的可用形态: 层级
// 展示 + 展开收起 + 选中回调。

const (
	treeRowH     = 28 // 行高 (与 tableRowH 同值, 便于混排对齐)
	treeIndent   = 16 // 每层缩进
	treeArrowW   = 16 // 箭头列宽 (叶子也占位, 保证文字左对齐)
	treePadX     = 6  // 左侧基础留白
	treeMinW     = 120
	treeArrowGap = 2 // 箭头与文字间距
)

// treeNode 是一个节点的解析结果 (标题 / 唯一键 / 子节点)。
type treeNode struct {
	label    string
	key      string
	children []treeNode
}

// treeNodes 读取 nodes prop, 递归解析成 treeNode 树。非数组/非法元素跳过
// (与 table/select 同: 数据形状不对不 panic)。
func (n *GuiNode) treeNodes() []treeNode {
	v, ok := n.Props["nodes"]
	if !ok {
		return nil
	}
	return parseTreeNodes(v)
}

// parseTreeNodes 递归解析节点数组。
func parseTreeNodes(v object.Value) []treeNode {
	arr, ok := v.(*object.Array)
	if !ok {
		return nil
	}
	out := make([]treeNode, 0, len(arr.Elements))
	for _, e := range arr.Elements {
		obj, ok := e.(*object.Object)
		if !ok {
			// 纯字符串元素当作叶子节点 (允许 nodes={["a","b"]} 这种简写)
			if s, ok := e.(*object.String); ok {
				out = append(out, treeNode{label: s.Value, key: s.Value})
			}
			continue
		}
		node := treeNode{}
		if s, ok := objStringField(obj, "label"); ok {
			node.label = s
		}
		if s, ok := objStringField(obj, "key"); ok {
			node.key = s
		} else {
			node.key = node.label
		}
		if kids, ok := obj.GetProperty("children"); ok {
			node.children = parseTreeNodes(kids)
		}
		out = append(out, node)
	}
	return out
}

// ===== 节点树构造 =====

// buildTree 把 nodes 数据展开成 tree-row 节点树。与 buildTable 同一思路,
// 区别是**只物化可见节点**: 收起的分支不建行, 于是长树的节点数只与"展开
// 的部分"相关 (这也是不做虚拟滚动的前提 —— 折叠本身就是天然的窗口化)。
//
// 重建时机: 首次布局, 以及每次切换展开态之后 (见 toggleTreeRow)。
func buildTree(n *GuiNode) {
	// 清掉旧的内部行
	kept := n.Children[:0]
	for _, c := range n.Children {
		if c.Tag == "tree-row" {
			disposeNode(c)
			continue
		}
		kept = append(kept, c)
	}
	n.Children = kept

	// 取出上次的展开表 (按 key): 重建后要恢复各分支的展开态, 否则点一下
	// 就把别的分支全收起来了。
	openSet := n.treeOpenSet()
	nodes := n.treeNodes()
	buildTreeRows(n, nodes, 0, openSet, n)
}

// buildTreeRows 递归物化一层的可见行: 自己一行 + (展开时) 子节点各行。
func buildTreeRows(parent *GuiNode, nodes []treeNode, depth int, openSet map[string]bool, root *GuiNode) {
	for i, node := range nodes {
		row := &GuiNode{Tag: "tree-row", Props: map[string]object.Value{}}
		row.owner = root
		row.treeLabel = node.label
		row.treeDepth = depth
		row.treeParent = root
		row.treeKey = node.key
		hasKids := len(node.children) > 0
		row.treeExpandable = hasKids
		row.treeOpen = hasKids && openSet[node.key]
		parent.Children = append(parent.Children, row)

		if hasKids {
			attachTreeToggle(row, root, node.key)
		}
		attachTreeSelect(row, root, node, i)

		if row.treeOpen {
			buildTreeRows(parent, node.children, depth+1, openSet, root)
		}
	}
}

// attachTreeToggle 挂展开/收起: 只挂在**有子节点**的行上。点击行本身既切换
// 展开也触发 onSelect —— 与文件管理器一致 (点文件夹图标是展开, 点名字是选中;
// 这里合并成一次点击做两件事, 对键盘/触摸都更宽容)。
func attachTreeToggle(row *GuiNode, root *GuiNode, key string) {
	k := key
	row.Props["onClick"] = object.NewBuiltin("treeToggle", func(args ...object.Value) object.Value {
		app := appOfNode(root)
		if app == nil {
			return object.UndefinedSingleton
		}
		app.toggleTreeRow(root, k)
		return object.UndefinedSingleton
	})
}

// attachTreeSelect 挂选中回调: onSelect 收到被点节点 (label/key/depth)。
// 与 toggle 同一次点击触发 (onClick 只能挂一个, 所以选中也在 toggle 里做)。
func attachTreeSelect(row *GuiNode, root *GuiNode, node treeNode, index int) {
	if root.PropHandler("onSelect") == nil {
		return
	}
	n := node
	idx := index
	// 已挂号时把两个动作串起来 (先切换展开, 再报选中)
	prev := row.PropHandler("onClick")
	row.Props["onClick"] = object.NewBuiltin("treeSelect", func(args ...object.Value) object.Value {
		app := appOfNode(root)
		if app == nil {
			return object.UndefinedSingleton
		}
		// 先执行展开切换 (若有)
		if prev != nil {
			callScriptFn(prev)
		}
		arg := object.NewObject()
		arg.SetProperty("label", object.NewString(n.label))
		arg.SetProperty("key", object.NewString(n.key))
		arg.SetProperty("index", object.NewNumber(float64(idx)))
		arg.SetProperty("leaf", object.NewBoolean(len(n.children) == 0))
		app.callHandlerValue(root.PropHandler("onSelect"), "onSelect", arg)
		return object.UndefinedSingleton
	})
}

// treeOpenSet 返回当前展开态的副本。展开态是 tree 根节点上的 treeOpenMap
// (key → open), 是**跨重建持久**的状态: 重建可见行时不该丢掉别的分支的
// 展开态。返回副本而不是 map 本身, 避免构建过程写坏状态。
func (n *GuiNode) treeOpenSet() map[string]bool {
	out := make(map[string]bool, len(n.treeOpenMap))
	for k, v := range n.treeOpenMap {
		if v {
			out[k] = true
		}
	}
	return out
}

// ===== 布局 =====

// layoutTree 布局树: 每行一个 28 高的条带, 行内水平起点 = 基础留白 + 深度缩进。
func layoutTree(n *GuiNode) {
	ensureTreeBuilt(n)
	area := inner(n)
	y := area.Y
	for _, r := range treeRows(n) {
		r.Box = Rect{X: area.X, Y: y, W: area.W, H: treeRowH}
		layoutNode(r)
		placeAbsoluteIn(r, r.Box)
		y += treeRowH
	}
	placeAbsoluteIn(n, area)
}

// treeIntrinsic 树的固有尺寸: 宽给一个下限 (树通常由 stretch 铺满), 高 = 可见
// 行数 × 行高。
func treeIntrinsic(n *GuiNode) (int, int) {
	ensureTreeBuilt(n)
	return treeMinW, len(treeRows(n)) * treeRowH
}

// ensureTreeBuilt 惰性构建 (与 ensureTableBuilt 同理): 首次布局时物化可见行。
func ensureTreeBuilt(n *GuiNode) {
	if n.treeBuilt {
		return
	}
	n.treeBuilt = true
	buildTree(n)
}

// treeRows 取全部直接挂着的 tree-row (可见行都平铺在 tree 下, 靠 treeDepth
// 表达层级而不是嵌套 —— 平铺的真实原因见 buildTreeRows 的说明)。
func treeRows(n *GuiNode) []*GuiNode {
	out := make([]*GuiNode, 0, len(n.Children))
	for _, c := range n.Children {
		if c.Tag == "tree-row" {
			out = append(out, c)
		}
	}
	return out
}

// ===== 交互 =====

// toggleTreeRow 切换某个节点的展开态并重建可见行 (VM 线程执行)。展开态记在
// tree 根节点的展开表里 (按 key), 重建后其它分支的展开态得以保留。
func (a *app) toggleTreeRow(root *GuiNode, key string) {
	if root.treeOpenMap == nil {
		root.treeOpenMap = map[string]bool{}
	}
	root.treeOpenMap[key] = !root.treeOpenMap[key]
	buildTree(root)
	markFullDirtyFor(root)
}

// ===== 绘制 =====

// paintTreeRow 画一行树节点: 悬停底 + 缩进箭头 + 文本。
// 箭头用两个三角形 (▶/▼ 的几何近似) 而不是字符 —— 字符箭头在不同字体下
// 基线/宽度差异很大, 会让缩进看起来忽宽忽窄。
func paintTreeRow(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	if n.hovered {
		FillRect(img, b, tint(colorTreeRowHover, disabled))
	}
	x := b.X + treePadX + n.treeDepth*treeIndent
	// 箭头列 (仅展开中的父节点或可展开节点画)
	if n.treeHasChildren() {
		drawTreeArrow(img, x, b.Y+b.H/2, n.treeOpen, tint(colorTreeText, disabled))
	}
	textX := x + treeArrowW + treeArrowGap
	tw, th := MeasureText(n.treeLabel, n.FontSize())
	if tw > 0 && textX < b.X+b.W {
		DrawText(img, img.Bounds(), n.treeLabel, textX, b.Y+(b.H-th)/2, n.FontSize(),
			tint(n.textColor(), disabled), b.X+b.W-textX-treePadX)
	}
}

// drawTreeArrow 画展开指示三角: 收起 = 右向 ▶, 展开 = 下向 ▼。
// 用 4×4 的三角块像素级画 (小尺寸下比矢量更锐利)。
func drawTreeArrow(img *image.RGBA, x, cy int, open bool, c color.RGBA) {
	const s = 4
	if open {
		// ▼: 上边宽, 逐行收窄
		for i := 0; i < s; i++ {
			w := s - i
			FillRect(img, Rect{X: x + i, Y: cy - s/2 + i, W: w, H: 1}, c)
		}
		return
	}
	// ▶: 左边高, 逐列收窄
	for i := 0; i < s; i++ {
		h := s - i
		FillRect(img, Rect{X: x + i, Y: cy - h/2, W: 1, H: h}, c)
	}
}

// treeHasChildren 这一行是否可展开 (有无子节点)。由构造期写入 —— 绘制时
// 不再回查数据 (数据可能已变, 而本帧的节点树才是"屏幕上真实的东西")。
func (n *GuiNode) treeHasChildren() bool {
	return n.treeExpandable
}

// 树配色。
var (
	colorTreeRowHover = color.RGBA{R: 0xEE, G: 0xF3, B: 0xFA, A: 255} // 行悬停底
	colorTreeText     = color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255} // 箭头色
)
