package gfx

import (
	"image"
	"image/color"

	"github.com/14752222/Gox/object"
)

// list-item 列表行 (S4 组件库二期)。
//
// 用法 (与普通容器一样包内容 —— 它就是个"行"的视觉约定):
//
//	<scroll height={200}>
//	  <list-item selected={i()===0} onClick={() => setI(0)}>第一项</list-item>
//	  <list-item onClick={() => setI(1)}>第二项</list-item>
//	</scroll>
//
// 为什么值得单独封装: `row` 也能装内容, 但"行"这个东西有一套**跨应用一致的
// 视觉约定** —— 固定行高 (与 table/select 同一套 28px 常量)、左右内边距、
// 悬停高亮、选中底色、可选行底线。手拼 row 每次都要重述这四五条, 而且很容易
// 与旁边的表格/字段控件对不齐。list-item 把这些收成一条声明。
//
// 结构约定:
//   - 它**参与布局** (不是透明容器): 盒子 = 整行可点区域, 内容在盒内左对齐
//     垂直居中; 高度缺省 listRowH, 可被 height 覆盖。
//   - `selected` 是受控 prop (与 checkbox 的 checked 同类): 只影响底色,
//     选中状态由脚本持有 —— 与 table 的行悬停 (运行时字段) 不同, 因为
//     "选中哪一项"是业务状态而不是 UI 临时态。
//   - 分隔线默认画 (行与行之间才需要, 但组件不知道自己是不是最后一行, 所以
//     由 `divider={false}` 关掉); 列表首尾多一条线比漏线更容易被接受。
//
// 与 table/tree 的关系: 三者的行高常量同值 (tableRowH == treeRowH ==
// listRowH), 混排时能对齐。table/tree 内部不直接用 list-item —— 它们的行
// 有自己的列/缩进绘制, 抽公共绘制反而会把两套不相干的几何搅在一起; 共享
// 的是**常量与配色**, 那才是真正该统一的东西。

const (
	listRowH = 28 // 行高 (与 tableRowH / treeRowH 同值)
	listPadX = 10 // 行内左右留白
	listPadY = 4  // 行内上下留白
)

// ===== 布局 =====

// layoutListItem 布局列表行: 行内留白通过 padding 表达 (layoutStack 自己会
// 读 padding 求内容区), 子节点纵向堆叠 —— 单行文字, 或 row 拼的左右布局。
func layoutListItem(n *GuiNode) {
	applyListPadding(n)
	layoutStack(n, false)
}

// applyListPadding 把行内边距写进 props: 让 layoutStack / inner 走统一的
// padding 读取路径 (脚本显式给的 padding 优先)。
// 上下留白取小值: 行高固定 28, 内容 (单行文字约 17px) 在剩余空间里居中,
// 不需要大内边距 —— 那会把文字挤到行中间偏上或偏下。
func applyListPadding(n *GuiNode) {
	if _, ok := n.Props["padding"]; ok {
		return
	}
	n.Props["padding"] = object.NewNumber(float64(listPadY))
	if _, ok := n.Props["paddingLeft"]; !ok {
		n.Props["paddingLeft"] = object.NewNumber(float64(listPadX))
	}
	if _, ok := n.Props["paddingRight"]; !ok {
		n.Props["paddingRight"] = object.NewNumber(float64(listPadX))
	}
}

// listItemIntrinsic 列表行的固有尺寸: 高固定 listRowH; 宽按内容 + 两侧留白。
func listItemIntrinsic(n *GuiNode, w, h int) (int, int) {
	if h == 0 {
		h = listRowH
	}
	if w == 0 {
		cw, _ := n.contentSize()
		w = cw + 2*listPadX
	}
	return w, h
}

// ===== 绘制 =====

// paintListItem 画列表行: 选中底 > 悬停底 > 无底, 再画行底线。
// 优先级与字段控件一致 (选中是持续态, 悬停是瞬时态, 选中时不该被悬停盖掉)。
func paintListItem(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	switch {
	case n.listSelected():
		FillRect(img, b, tint(colorListItemSelected, disabled))
	case n.hovered && n.PropHandler("onClick") != nil:
		// 悬停反馈只在行真的可点时给 —— 纯展示的行悬停变色是误导
		FillRect(img, b, tint(colorListItemHover, disabled))
	}
	if n.listDivider() {
		FillRect(img, Rect{X: b.X, Y: b.Y + b.H - 1, W: b.W, H: 1},
			tint(colorListItemEdge, disabled))
	}
}

// ===== props =====

// listSelected 读 selected prop (受控选中态)。
func (n *GuiNode) listSelected() bool {
	v, ok := n.PropBool("selected")
	return ok && v
}

// listDivider 行底线开关: 缺省开 (divider={false} 关)。
func (n *GuiNode) listDivider() bool {
	v, ok := n.PropBool("divider")
	if !ok {
		return true
	}
	return v
}

// 列表行配色。
var (
	colorListItemHover    = color.RGBA{R: 0xEE, G: 0xF3, B: 0xFA, A: 255} // 行悬停底
	colorListItemSelected = color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255} // 行选中底 (与下拉项高亮同色)
	colorListItemEdge     = color.RGBA{R: 0xE4, G: 0xE4, B: 0xE4, A: 255} // 行底线
)
