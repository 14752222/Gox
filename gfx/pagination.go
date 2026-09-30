package gfx

import (
	"image"
	"image/color"

	"github.com/14752222/Gox/object"
)

// S4/T09 pagination 分页器。
//
//	<pagination total={200} pageSize={20} current={page()} onChange={(e) => setPage(e.page)} />
//
// **完全受控** (与 select 同款): 显示永远只看 current, 点击页码只派发
// onChange({page, pageSize}), 等脚本把新值写回 signal —— 不回写就是不动
// (受控的定义, 不是 bug)。
//
// 结构: 一排按钮区 (「‹ 上一页」+ 页码 + 「下一页 ›」), 几何记进 node.pageBtns
// (与 tabs 的 tabStrip 同一思路: 命中与绘制共用一份, 不可能错层)。
//
// 页码窗口 (与主流 UI 库一致): 页数 <= 7 全展示; 否则显示首尾 + 当前页附近
// 若干页, 中间用省略号占位 (省略号不可点)。

const (
	pageBtnMinW   = 30 // 页码按钮最小宽度
	pageBtnH      = 28 // 按钮高
	pageBtnGap    = 4  // 按钮间距
	pagePadX      = 10 // 按钮内左右边距
	pageEllipsisW = 22 // 省略号占位宽
	pageMaxSlots  = 7  // 超过这个页数就开始折叠
)

// pageGoto 是命中表里的一项。-1 表示省略号 (不可点)。
type pageGoto struct {
	rect Rect
	page int // 1-based; -1 = 省略号
}

// paginationTotal 读 total prop (总条数)。
func (n *GuiNode) paginationTotal() int {
	if v, ok := n.PropNum("total"); ok && v > 0 {
		return int(v)
	}
	return 0
}

// paginationPageSize 读 pageSize prop (缺省 10)。
func (n *GuiNode) paginationPageSize() int {
	if v, ok := n.PropNum("pageSize"); ok && v > 0 {
		return int(v)
	}
	return 10
}

// paginationPageCount 返回总页数 (至少 1)。
func (n *GuiNode) paginationPageCount() int {
	total := n.paginationTotal()
	size := n.paginationPageSize()
	if total <= 0 || size <= 0 {
		return 1
	}
	p := (total + size - 1) / size
	if p < 1 {
		p = 1
	}
	return p
}

// paginationCurrent 返回当前页 (受控读 current prop, 1-based, 已钳位)。
func (n *GuiNode) paginationCurrent() int {
	pages := n.paginationPageCount()
	cur := 1
	if v, ok := n.PropNum("current"); ok {
		cur = int(v)
	}
	if cur < 1 {
		cur = 1
	}
	if cur > pages {
		cur = pages
	}
	return cur
}

// pageWindow 计算要显示的页码槽位: 正数 = 页码, -1 = 省略号。
//
// 折叠规则 (主流 UI 库通行):
//   - pages <= 7: 全部展开;
//   - 否则固定展示首尾两页, 当前页附近按位置取 3 个, 其余折叠为省略号。
func pageWindow(cur, pages int) []int {
	if pages <= pageMaxSlots {
		out := make([]int, pages)
		for i := range out {
			out[i] = i + 1
		}
		return out
	}
	// 当前页靠近头部 / 尾部时, 收起的那一侧不显示省略号
	head := cur <= 4
	tail := cur >= pages-3
	switch {
	case head:
		return []int{1, 2, 3, 4, 5, -1, pages}
	case tail:
		return []int{1, -1, pages - 4, pages - 3, pages - 2, pages - 1, pages}
	default:
		return []int{1, -1, cur - 1, cur, cur + 1, -1, pages}
	}
}

// intrinsicPagination 算分页器固有尺寸 (按钮排一行)。
func intrinsicPagination(n *GuiNode) (w, h int) {
	fontSize := n.FontSize()
	slots := pageWindow(n.paginationCurrent(), n.paginationPageCount())
	w = 0
	for i, s := range slots {
		if i > 0 {
			w += pageBtnGap
		}
		w += n.pageSlotWidth(s, fontSize)
	}
	// 前后各加一个"上一页/下一页"按钮
	prevW := n.pageNavWidth("‹", fontSize)
	nextW := n.pageNavWidth("›", fontSize)
	w += prevW + pageBtnGap + pageBtnGap + nextW
	return w, pageBtnH
}

// pageSlotWidth 返回一个页码槽位的宽度 (正文号用测得宽 + 内边距, 但不小于最小值)。
func (n *GuiNode) pageSlotWidth(slot, fontSize int) int {
	if slot < 0 {
		return pageEllipsisW
	}
	tw, _ := MeasureText(itoa(slot), fontSize)
	w := tw + 2*pagePadX
	if w < pageBtnMinW {
		w = pageBtnMinW
	}
	return w
}

// pageNavWidth 返回"上一页/下一页"按钮宽度。
func (n *GuiNode) pageNavWidth(label string, fontSize int) int {
	tw, _ := MeasureText(label, fontSize)
	w := tw + 2*pagePadX
	if w < pageBtnMinW {
		w = pageBtnMinW
	}
	return w
}

// layoutPagination 布局分页器: 横排按钮, 几何记进 pageBtns (命中/绘制共用)。
func layoutPagination(n *GuiNode) {
	area := inner(n)
	fontSize := n.FontSize()
	pages := n.paginationPageCount()
	cur := n.paginationCurrent()
	slots := pageWindow(cur, pages)

	if cap(n.pageBtns) > 0 {
		n.pageBtns = n.pageBtns[:0]
	}
	x := area.X
	y := area.Y

	// 上一页
	pw := n.pageNavWidth("‹", fontSize)
	n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: pw, H: pageBtnH}, page: cur - 1})
	x += pw + pageBtnGap
	// 页码槽位
	for _, s := range slots {
		w := n.pageSlotWidth(s, fontSize)
		n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: w, H: pageBtnH}, page: s})
		x += w + pageBtnGap
	}
	// 下一页
	nw := n.pageNavWidth("›", fontSize)
	n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: nw, H: pageBtnH}, page: cur + 1})

	placeAbsoluteIn(n, area)
}

// paintPagination 画分页按钮: 当前页 accent 底白字, 其余浅灰底; 不可用的
// 上一页/下一页变淡。
func paintPagination(img *image.RGBA, n *GuiNode, disabled bool) {
	fontSize := n.FontSize()
	pages := n.paginationPageCount()
	cur := n.paginationCurrent()
	slots := pageWindow(cur, pages)

	for i, g := range n.pageBtns {
		r := g.rect
		if r.W <= 0 || r.H <= 0 {
			continue
		}
		switch {
		case i == 0: // 上一页
			drawPageBtn(img, r, "‹", fontSize, false, cur > 1, disabled)
		case i == len(n.pageBtns)-1: // 下一页
			drawPageBtn(img, r, "›", fontSize, false, cur < pages, disabled)
		default:
			slot := slots[i-1]
			if slot < 0 {
				// 省略号: 灰字, 不可点
				tw, th := MeasureText("…", fontSize)
				DrawText(img, img.Bounds(), "…", r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, fontSize,
					tint(colorPlaceholder, disabled), tw)
				continue
			}
			drawPageBtn(img, r, itoa(slot), fontSize, slot == cur, true, disabled)
		}
	}
}

// drawPageBtn 画一个分页按钮: 底 + 居中文本。current 时强调色底白字,
// enabled=false 时整体变淡 (但仍可见 —— 用户要能看见"这里有个按钮")。
func drawPageBtn(img *image.RGBA, r Rect, label string, fontSize int, current, enabled, disabled bool) {
	var face, ink color.RGBA
	switch {
	case current:
		face = tint(colorAccent, disabled)
		ink = tint(colorAccentText, disabled)
	case !enabled:
		face = tint(colorTrack, disabled)
		ink = tint(colorPlaceholder, disabled)
	default:
		face = tint(colorBtnFace, disabled)
		ink = tint(colorText, disabled)
	}
	fillRoundRect(img, r, 4, face)
	tw, th := MeasureText(label, fontSize)
	DrawText(img, img.Bounds(), label, r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, fontSize, ink, tw)
}

// pageBtnAt 返回 (x,y) 命中的页码 (1-based); 命中省略号或空白返回 0。
func (n *GuiNode) pageBtnAt(x, y int) int {
	for _, g := range n.pageBtns {
		if g.rect.Contains(x, y) {
			if g.page < 1 {
				return 0
			}
			return g.page
		}
	}
	return 0
}

// paginationInChain 从 n 起沿祖先链找第一个 pagination。
func paginationInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "pagination" {
			return p
		}
	}
	return nil
}

// paginationGo 切到目标页: 只派发 onChange({page, pageSize}), 不改自身状态
// —— pagination 是完全受控的, 显示值归 current prop (与 select 同一哲学)。
func (a *app) paginationGo(pg *GuiNode, page int) {
	pages := pg.paginationPageCount()
	cur := pg.paginationCurrent()
	if page < 1 || page > pages || page == cur {
		return
	}
	if pg.PropHandler("onChange") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("page", object.NewNumber(float64(page)))
	arg.SetProperty("pageSize", object.NewNumber(float64(pg.paginationPageSize())))
	a.callHandler(pg, "onChange", arg)
}
