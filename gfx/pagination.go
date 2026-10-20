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
	// pageBtnSize 是页码/翻页按钮的统一边长 (Prism S5: 32×32, 与集合行高
	// 同档)。S4 是"高 28、宽随文字长"的胶囊, 一排页码宽度参差; S5 取方形
	// —— 页码是 1~3 位数字, 居中放 32×32 里视觉重量一致。
	//
	// **刻意小于 TouchMin (44)**: 桌面端鼠标目标, 触控场景由脚本换成
	// "上一页/下一页 + 页码输入"形态。
	pageBtnSize = 32
	// pageBtnGap 是按钮间距 (Space2 = 4)。
	pageBtnGap = Space2
	// pageBtnRadiusToken 是按钮圆角 (RadiusSM = 6)。
	pageBtnRadiusToken = RadiusSM
	// pageEllipsisW 是省略号占位宽 (与按钮同宽, 保持一排格子等距)。
	pageEllipsisW = pageBtnSize
	// pageMaxSlots 超过这个页数就开始折叠。
	pageMaxSlots = 7
)

// pageBtnRadius 返回按钮圆角 (物理像素)。
func pageBtnRadius() int { return Radius(pageBtnRadiusToken) }

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
			w += Space(pageBtnGap)
		}
		w += n.pageSlotWidth(s, fontSize)
	}
	// 前后各加一个"上一页/下一页"按钮
	prevW := n.pageNavWidth("‹", fontSize)
	nextW := n.pageNavWidth("›", fontSize)
	w += prevW + Space(pageBtnGap) + Space(pageBtnGap) + nextW
	// Prism S8: 按钮边长与返回值都按设计像素换算 (否则 2x 屏上一排页码只有
	// 半个字号高, 与同一行的表格行高 64 完全不成比例)。
	return w, Space(pageBtnSize)
}

// pageSlotWidth 返回一个页码槽位的宽度。S5 起统一 32×32, 不再按文字测宽:
// 一排页码宽度一致, 页数变化时按钮不会左右跳。
func (n *GuiNode) pageSlotWidth(slot, fontSize int) int {
	return Space(pageBtnSize)
}

// pageNavWidth 返回"上一页/下一页"按钮宽度 (同样 32×32)。
func (n *GuiNode) pageNavWidth(label string, fontSize int) int {
	return Space(pageBtnSize)
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
	n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: pw, H: Space(pageBtnSize)}, page: cur - 1})
	x += pw + Space(pageBtnGap)
	// 页码槽位
	for _, s := range slots {
		w := n.pageSlotWidth(s, fontSize)
		n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: w, H: Space(pageBtnSize)}, page: s})
		x += w + Space(pageBtnGap)
	}
	// 下一页
	nw := n.pageNavWidth("›", fontSize)
	n.pageBtns = append(n.pageBtns, pageGoto{rect: Rect{X: x, Y: y, W: nw, H: Space(pageBtnSize)}, page: cur + 1})

	placeAbsoluteIn(n, area)
}

// paintPagination 画分页按钮 (Prism S5): 当前页 accent 实底 + accentText
// 文字, 其余浅灰底; 不可用的上一页/下一页变淡; 省略号用 colorTextMuted。
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
				// 省略号: 不可点, 用 muted 而不是 placeholder —— 它是
				// "中间还有页"的说明文字, 不是"这里该填内容"的占位。
				tw, th := MeasureText("…", fontSize)
				DrawText(img, img.Bounds(), "…", r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, fontSize,
					tint(colorTextMuted, disabled), tw)
				continue
			}
			drawPageBtn(img, r, itoa(slot), fontSize, slot == cur, true, disabled)
		}
	}
}

// drawPageBtn 画一个分页按钮: 底 + 居中文本。current 时强调色实底 +
// accentText 文字 (这是"我在第几页"最直接的信号); enabled=false 时整体
// 变淡 (但仍可见 —— 用户要能看见"这里有个按钮")。
//
// 圆角走 RadiusSM, 与 S3 的字段/按钮同一族但更小: 32px 的方形按钮配 r8
// 会显得"胖", r6 才和行高 32 的密度相称。
func drawPageBtn(img *image.RGBA, r Rect, label string, fontSize int, current, enabled, disabled bool) {
	var face, ink color.RGBA
	switch {
	case current:
		face = tint(colorAccent, disabled)
		ink = tint(colorAccentText, disabled)
	case !enabled:
		face = tint(colorSurfaceSunken, disabled)
		ink = tint(colorTextDisabled, disabled)
	default:
		face = tint(colorSurfaceRaised, disabled)
		ink = tint(colorText, disabled)
	}
	fillRoundRect(img, r, pageBtnRadius(), face)
	// 非当前按钮描一圈装饰边: 白底压在白色页面上时没有边界, 描边是它
	// 唯一能被看见的轮廓。当前页是实色块, 不需要 (描了反而糊一圈)。
	if !current {
		strokeRoundRectW(img, r, pageBtnRadius(), 1, tint(colorBorderDecorative, disabled))
	}
	tw, th := MeasureText(label, fontSize)
	DrawText(img, img.Bounds(), label, r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, fontSize, ink, tw)
}

// pageBtnAt 返回 (x,y) 命中的页码 (1-based); 命中省略号、落在按钮区之外返回 0。
//
// Prism S8: 命中走 touchHitContains —— 触摸模式下页码按钮的命中区补到 44。
// 32×32 的页码是桌面鼠标尺寸, 手指点上板块边上的那一颗几乎必然偏出去。
func (n *GuiNode) pageBtnAt(x, y int) int {
	for _, g := range n.pageBtns {
		if touchHitContains(g.rect, x, y) {
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
