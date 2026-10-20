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
//     knownTags 以免内部构造被当成未知标签)。每个可见节点一行, 行高 32
//     (Prism S5, 与 table/list 同一套常量)。
//   - 展开态是**节点的运行时状态** (treeOpen), 不来自 props: 与 select 的
//     expanded、tooltip 的 tipPopup 同类 —— 点箭头展开/收起是渲染层的临时
//     UI 状态, 不需要脚本回写 (想受控可用 onToggle 自己记)。
//   - 展开/收起只改变"哪些行物化", 不做重排动画 (v1 与 select 弹层一致:
//     立即切换, 整帧标脏)。
//   - 缩进 = 深度 × treeIndent + 前导箭头占位, 保证同层节点文字左对齐;
//     叶子节点不画箭头, 但**保留占位宽度** (否则叶子与父节点的文字会错位)。
//     缩进与左侧基础留白都走 Space token。
//   - 展开箭头是 **v 形 chevron** (与 select 的下拉箭头同一笔画), 收起时
//     朝右、展开时朝下 —— v1 的实心三角在 32px 行里显得笨重, 而字符箭头
//     (+/-) 在不同字体下宽高不一。
//
// 为什么不内置搜索/多选/拖拽: 那是 IDE 级树控件的领域, 与"提供 tree 的
// 节点组件封装"这个目标(见任务描述)不匹配。这里给最小的可用形态: 层级
// 展示 + 展开收起 + 选中回调。

const (
	// treeRowH 是行高 (Prism S5: 28 → 32, 与 tableRowH / listRowH 同值)。
	// **刻意小于 TouchMin (44)**: 桌面端鼠标行不必满足触控下限。
	treeRowH = 32
	// treeIndent 是每层缩进 (设计像素, Space6 = 16)。
	treeIndent = Space6
	// treeArrowW 是箭头列宽 (叶子也占位, 保证文字左对齐)。
	treeArrowW = 16
	// treeArrowGap 是箭头与文字间距 (Space1 = 2)。
	treeArrowGap = Space1
	treeMinW     = 120
)

// treePadX 返回左侧基础留白 (物理像素, Space4 = 8)。
func treePadX() int { return Space(Space4) }

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
		// 受控选中 (Prism S5): <tree rowSelected={key}> 在构造期拷到对应行。
		// 树是"数据 → 可见行"的单向流, 选中态也走数据而不是另存一份运行时
		// 状态 —— 重建 (展开/收起) 后选中行还是同一个 key, 不会漂移。
		if sel, ok := root.PropStr("rowSelected"); ok && sel == node.key {
			row.Props["selected"] = &object.Boolean{Value: true}
		}
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
	// Prism S8: 行高是参与排版的尺寸, 经 Space() 换算。
	rowH := Space(treeRowH)
	y := area.Y
	for _, r := range treeRows(n) {
		r.Box = Rect{X: area.X, Y: y, W: area.W, H: rowH}
		layoutNode(r)
		placeAbsoluteIn(r, r.Box)
		y += rowH
	}
	placeAbsoluteIn(n, area)
}

// treeIntrinsic 树的固有尺寸: 宽给一个下限 (树通常由 stretch 铺满), 高 = 可见
// 行数 × 行高。
func treeIntrinsic(n *GuiNode) (int, int) {
	ensureTreeBuilt(n)
	return Space(treeMinW), len(treeRows(n)) * Space(treeRowH)
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
	// 底色优先级与 table/list 同一套: 选中 > 悬停。
	if n.treeSelected() {
		FillRect(img, b, tint(colorStateSelected, disabled))
	} else if n.hovered {
		FillRect(img, b, tint(colorStateRowHover, disabled))
	}
	x := b.X + treePadX() + n.treeDepth*Space(treeIndent)
	// 箭头列 (仅展开中的父节点或可展开节点画)
	if n.treeHasChildren() {
		drawTreeChevron(img, x+Space(chevronInset), b.Y+b.H/2, n.treeOpen, tint(treeChevronColor(n), disabled))
	}
	textX := x + Space(treeArrowW) + treeArrowGap // Prism S8: 箭头列随密度放大
	tw, th := MeasureText(n.treeLabel, n.FontSize())
	if tw > 0 && textX < b.X+b.W {
		DrawText(img, img.Bounds(), n.treeLabel, textX, b.Y+(b.H-th)/2, n.FontSize(),
			tint(n.textColor(), disabled), b.X+b.W-textX-treePadX())
	}
}

// chevronInset 是 v 形箭头在箭头列里的水平内缩: 让 8px 宽的 chevron 居
// 于 16px 的箭头列中, 与文字的距离才和 select 的下拉箭头一致。
const chevronInset = 4

// drawTreeChevron 画展开指示的 v 形折线 (Prism S5)。
//
// 收起 = ">" (顶点朝右), 展开 = "v" (顶点朝下): 两者是同一笔画旋转, 展开
// 那一笔与 select 的 paintChevron 形状完全相同 —— 用户在"展开这个"与
// "打开下拉"两种动作上看到的是同一个记号。
//
// 走 fillLine (覆盖率抗锯齿) 而不是逐像素 FillRect: 斜线在小尺寸下逐像素
// 会出锯齿, 而 S5 之后的行高 32 让这条线有足够空间做 AA。
func drawTreeChevron(img *image.RGBA, x, cy int, open bool, c color.RGBA) {
	const half, thick = 4, 2
	if open {
		// v: (x-4, cy-2) → (x, cy+2) → (x+4, cy-2)
		fillLine(img, x-half, cy-2, x, cy+2, thick, c)
		fillLine(img, x, cy+2, x+half, cy-2, thick, c)
		return
	}
	// >: (x-2, cy-4) → (x+2, cy) → (x-2, cy+4)
	fillLine(img, x-2, cy-half, x+2, cy, thick, c)
	fillLine(img, x+2, cy, x-2, cy+half, thick, c)
}

// treeChevronColor 是箭头的笔画色: 选中行上要比 secondary 更重 (选中底的
// 蓝灰会让 secondary 糊掉), 其余走 secondary —— 箭头是提示而不是内容。
func treeChevronColor(n *GuiNode) color.RGBA {
	if n.treeSelected() {
		return colorText
	}
	return colorTextSecondary
}

// treeSelected 报告这一行是否是选中节点 (受控 prop, 与 list-item 同款)。
func (n *GuiNode) treeSelected() bool {
	v, ok := n.PropBool("selected")
	return ok && v
}

// treeHasChildren 这一行是否可展开 (有无子节点)。由构造期写入 —— 绘制时
// 不再回查数据 (数据可能已变, 而本帧的节点树才是"屏幕上真实的东西")。
func (n *GuiNode) treeHasChildren() bool {
	return n.treeExpandable
}
