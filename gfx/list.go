package gfx

import (
	"image"

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
	// listRowH 是行高 (Prism S5: 28 → 32, 与 tableRowH / treeRowH 同值)。
	// 比字段行高 36 矮一档; **刻意小于 TouchMin (44)**: 桌面上鼠标行不必
	// 满足触控下限, 触控场景该用卡片式列表而不是把每行撑到 44。
	listRowH = 32
	// listPadYToken 是行内上下留白的设计像素 (Space2 = 4); 写进 padding prop
	// 时经 Space() 换算成物理像素。
	listPadYToken = Space2
	// listSelectBarW 是选中行左侧的强调指示条宽度 (Prism S5)。
	listSelectBarW = 3
)

// listPadX 返回行内左右留白 (物理像素, Space5 = 12)。
func listPadX() int { return Space(Space5) }

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
	n.Props["padding"] = object.NewNumber(float64(Space(listPadYToken)))
	if _, ok := n.Props["paddingLeft"]; !ok {
		n.Props["paddingLeft"] = object.NewNumber(float64(listPadX()))
	}
	if _, ok := n.Props["paddingRight"]; !ok {
		n.Props["paddingRight"] = object.NewNumber(float64(listPadX()))
	}
}

// listItemIntrinsic 列表行的固有尺寸: 高固定 listRowH; 宽按内容 + 两侧留白。
func listItemIntrinsic(n *GuiNode, w, h int) (int, int) {
	if h == 0 {
		h = Space(listRowH) // Prism S8: 行高按设计像素换算
	}
	if w == 0 {
		cw, _ := n.contentSize()
		w = cw + 2*listPadX()
	}
	return w, h
}

// ===== 绘制 =====

// paintListItem 画列表行: 选中底 > 悬停底 > 无底, 再画行底线。
// 优先级与字段控件一致 (选中是持续态, 悬停是瞬时态, 选中时不该被悬停盖掉)。
//
// Prism S5: 选中行除了整行铺 colorStateSelected, 再在左侧压一条强调色
// 指示条 —— 软光栅没有透明度/阴影, "选中"在浅底上单靠色差不够醒目, 而
// 一条 3px 的实色竖条在彩色屏幕上与灰阶上都读得出来 (也是评审样张
// testdata/shots/list-item.js 说明里提的那条"左强调条")。
func paintListItem(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	switch {
	case n.listSelected():
		FillRect(img, b, tint(colorStateSelected, disabled))
		FillRect(img, Rect{X: b.X, Y: b.Y, W: Space(listSelectBarW), H: b.H},
			tint(colorAccent, disabled))
	case n.hovered && n.PropHandler("onClick") != nil:
		// 悬停反馈只在行真的可点时给 —— 纯展示的行悬停变色是误导
		FillRect(img, b, tint(colorStateRowHover, disabled))
	}
	if n.listDivider() {
		FillRect(img, Rect{X: b.X, Y: b.Y + b.H - 1, W: b.W, H: 1},
			tint(colorBorderDecorative, disabled))
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
