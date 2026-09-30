package gfx

import (
	"image"
	"image/color"
	"strings"

	"github.com/14752222/Gox/object"
)

// S4/T09 展示类组件五件套: alert / badge / tag / avatar / empty。
//
// 这一组的共同点:
//   - 全是"纯展示 + 少量交互": 不需要新的布局语义 (尺寸按内容或显式 prop
//     算, 与 button 同款), 绘制是手绘而非走 paintBoxDecor;
//   - 与 limits.md 的关系: 此前都归在"用现有能力模拟"里, 现在收进内核,
//     使 `<alert level="warn">…</alert>` 这类写法开箱即用。
//
// 交互边界 (诚实记录):
//   - alert 的关闭叉 / tag 的关闭叉点击后**只派发 onClose**, 不替脚本摘树
//     —— 显隐归信号管 (与 toast 同一哲学)。
//   - badge 只读 count / dot / max, 不做溢出动画。

const (
	alertPadX    = 12 // alert 左右内边距
	alertPadY    = 9  // alert 上下内边距
	alertAccentW = 4  // alert 左侧 level 色条宽 (与 toast 一致)
	alertIconW   = 14 // alert 图标边长
	alertGap     = 8  // 图标与文本之间

	badgeDotSize = 8 // badge 圆点直径
	badgePadX    = 6 // badge 数字胶囊左右内边距
	badgeOverX   = 4 // badge 相对宿主右上角的横向外扩
	badgeOverY   = 4 // badge 相对宿主右上角的纵向外扩

	tagPadX      = 8  // tag 左右内边距
	tagPadY      = 3  // tag 上下内边距
	tagGap       = 4  // tag 文字与关闭叉之间
	tagCloseSize = 10 // tag/alert 关闭叉的命中边长

	emptyIconSize = 44 // empty 图示边长
	emptyGap      = 10 // 图示与描述、描述与操作之间的间距
)

// itoa 是 strconv.Itoa 的薄封装 (本文件只用来拼 badge 数字, 不想为它多导一个包)。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// ===== alert: 横幅提示 =====
//
//	<alert level="success" closable onClose={...}>保存成功</alert>
//
// level 取 info(缺省) / success / warn / error, 决定色条与图标色。

// alertLevelColor 把 level prop 映射成强调色。
func alertLevelColor(n *GuiNode) color.RGBA {
	switch lv, _ := n.PropStr("level"); lv {
	case "success":
		return colorAccent
	case "warn", "warning":
		return colorWarn
	case "error", "danger":
		return colorDanger
	}
	return colorInfo
}

// alertClosable 读 closable prop (closable / closeable 两种拼写都认)。
func (n *GuiNode) alertClosable() bool {
	for _, name := range []string{"closable", "closeable"} {
		if v, ok := n.PropBool(name); ok {
			return v
		}
	}
	return false
}

// alertCloseRect 返回关闭叉的命中矩形 (无叉时零矩形)。
func (n *GuiNode) alertCloseRect() Rect {
	if !n.alertClosable() || n.Box.W <= 0 {
		return Rect{}
	}
	s := tagCloseSize
	return Rect{
		X: n.Box.X + n.Box.W - alertPadX - s,
		Y: n.Box.Y + (n.Box.H-s)/2,
		W: s, H: s,
	}
}

// intrinsicAlert 算横幅固有尺寸: 色条 + 图标 + 间距 + 文本内容 + 可选关闭叉。
func intrinsicAlert(n *GuiNode) (w, h int) {
	cw, ch := stackContentSize(n, false)
	w = alertAccentW + alertPadX + alertIconW + alertGap + cw
	if n.alertClosable() {
		w += tagCloseSize + tagGap
	}
	h = ch + 2*alertPadY
	if min := alertIconW; min > ch {
		h = min + 2*alertPadY
	}
	return w, h
}

// layoutAlert 布局横幅: 文本子节点放在"色条 + 图标"右侧。
func layoutAlert(n *GuiNode) {
	area := inner(n)
	x := area.X + alertAccentW + alertPadX + alertIconW + alertGap
	w := area.X + area.W - x
	if n.alertClosable() {
		w -= tagCloseSize + tagGap
	}
	if w < 0 {
		w = 0
	}
	y := area.Y
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		if cw > w {
			cw = w
		}
		c.Box = Rect{X: x, Y: y, W: cw, H: ch}
		layoutNode(c)
		y += ch
	}
	placeAbsoluteIn(n, area)
}

// paintAlert 画横幅: 浅色底 (level 色 12% 混白) + 左侧色条 + 图标 + 关闭叉。
func paintAlert(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	accent := tint(alertLevelColor(n), disabled)
	FillRect(img, b, tint(softFill(alertLevelColor(n), 0.12), disabled))
	FillRect(img, Rect{X: b.X, Y: b.Y, W: alertAccentW, H: b.H}, accent)
	// 图标 (圆 + 白色竖线/点), 垂直居中
	ix := b.X + alertAccentW + alertPadX
	iy := b.Y + (b.H-alertIconW)/2
	drawAlertGlyph(img, Rect{X: ix, Y: iy, W: alertIconW, H: alertIconW}, accent, disabled)
	// 关闭叉
	if r := n.alertCloseRect(); r.W > 0 {
		drawCloseX(img, r, tint(colorPlaceholder, disabled), disabled)
	}
}

// drawAlertGlyph 画 level 图标: 实心圆 + 内部白色感叹号的"竖线 + 点"。
//
// 不做字体图标 (三平台字形不同), 也不引入矢量路径 (内核 canvas 无路径),
// 统一用"圆 + 矩形"拼装 —— 与 testdata/ui/icons.js 同一路线。
func drawAlertGlyph(img *image.RGBA, r Rect, c color.RGBA, disabled bool) {
	if r.W <= 2 || r.H <= 2 {
		return
	}
	cx := r.X + r.W/2
	cy := r.Y + r.H/2
	rad := min(r.W, r.H)/2 - 1
	if rad < 2 {
		rad = 2
	}
	FillCircle(img, cx, cy, rad, c)
	white := tint(colorAccentText, disabled)
	lw := 2
	// 竖线 (感叹号主体)
	FillRect(img, Rect{X: cx - lw/2, Y: cy - rad/2, W: lw, H: rad - 2}, white)
	// 底部点
	FillRect(img, Rect{X: cx - lw/2, Y: cy + rad/2 - 2, W: lw, H: lw}, white)
}

// ===== badge: 徽标 (包裹式, 溢出到宿主右上角) =====
//
//	<badge count={5}><button>消息</button></badge>
//	<badge dot={true}><icon name="bell" /></badge>
//
// 布局**透明** (像 tooltip): 自身盒子就是子节点的盒子, 徽标本体是画在宿主
// 右上角外的装饰, 不占流内也不参与尺寸 —— 一排带徽标的按钮仍然对齐。

// badgeCount 读 count prop。
func (n *GuiNode) badgeCount() (int, bool) {
	if v, ok := n.PropNum("count"); ok {
		return int(v), true
	}
	return 0, false
}

// badgeMax 读 max prop (超过则显示 "max+"), 缺省 99。
func (n *GuiNode) badgeMax() int {
	if v, ok := n.PropNum("max"); ok && v > 0 {
		return int(v)
	}
	return 99
}

// badgeIsDot 读 dot prop。
func (n *GuiNode) badgeIsDot() bool {
	v, _ := n.PropBool("dot")
	return v
}

// badgeText 返回徽标数字文本 ("" = 不显示数字)。
func (n *GuiNode) badgeText() string {
	c, ok := n.badgeCount()
	if !ok || c <= 0 {
		return ""
	}
	if c > n.badgeMax() {
		return itoa(n.badgeMax()) + "+"
	}
	return itoa(c)
}

// badgeVisible 报告是否要画徽标 (dot 形态恒显示)。
func (n *GuiNode) badgeVisible() bool {
	return n.badgeIsDot() || n.badgeText() != ""
}

// badgeAnchor 返回徽标锚点 (宿主右上角往外偏一点)。
func (n *GuiNode) badgeAnchor() (int, int) {
	return n.Box.X + n.Box.W - badgeOverX, n.Box.Y + badgeOverY
}

// paintBadge 画徽标本体 (由宿主在绘制阶段调, 见 raster.go 的 badge 分支)。
func paintBadge(img *image.RGBA, n *GuiNode, disabled bool) {
	if !n.badgeVisible() || n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	ax, ay := n.badgeAnchor()
	c := tint(colorDanger, disabled)
	if n.badgeIsDot() {
		FillCircle(img, ax-badgeDotSize/2, ay, badgeDotSize/2, c)
		return
	}
	txt := n.badgeText()
	size := n.FontSize()
	if size > 13 {
		size = 13 // 徽标比正文小一号, 免得盖住宿主
	}
	tw, th := MeasureText(txt, size)
	w := tw + 2*badgePadX
	if w < badgeDotSize+4 {
		w = badgeDotSize + 4
	}
	h := th + 4
	r := Rect{X: ax - w, Y: ay - h/2, W: w, H: h}
	fillRoundRect(img, r, h/2, c)
	DrawText(img, img.Bounds(), txt, r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, size,
		tint(colorAccentText, disabled), tw)
}

// badgeChild 返回 badge 包裹的子节点 (唯一的流内子节点)。
func (n *GuiNode) badgeChild() *GuiNode {
	for _, c := range n.Children {
		if c.isFlowChild() {
			return c
		}
	}
	return nil
}

// intrinsicBadge 布局透明: 尺寸完全跟随子节点。
func intrinsicBadge(n *GuiNode) (w, h int) {
	if c := n.badgeChild(); c != nil {
		return c.intrinsicSize()
	}
	return 0, 0
}

// ===== tag: 标签 (可关闭) =====
//
//	<tag>默认</tag>
//	<tag color="#3355aa" closable onClose={...}>加粗</tag>

// tagColor 读 color/background prop; ok=false 表示用缺省浅灰外观。
//
// 走 ParseColor 而不是 parseHexColor: 前者是脚本侧的公开解析入口, 支持
// "#3355aa" / 命名色 / "rgb(...)" 三种写法, 与其它 color 属性口径一致。
func (n *GuiNode) tagColor() (color.RGBA, bool) {
	for _, name := range []string{"color", "background"} {
		if s, ok := n.PropStr(name); ok && s != "" {
			if c, ok := ParseColor(s); ok {
				return c, true
			}
		}
	}
	return colorTrack, false
}

// tagClosable 读 closable prop。
func (n *GuiNode) tagClosable() bool {
	for _, name := range []string{"closable", "closeable"} {
		if v, ok := n.PropBool(name); ok {
			return v
		}
	}
	return false
}

// tagCloseRect 返回关闭叉命中矩形。
func (n *GuiNode) tagCloseRect() Rect {
	if !n.tagClosable() || n.Box.W <= 0 {
		return Rect{}
	}
	s := tagCloseSize
	return Rect{
		X: n.Box.X + n.Box.W - tagPadX - s,
		Y: n.Box.Y + (n.Box.H-s)/2,
		W: s, H: s,
	}
}

// intrinsicTag 算标签固有尺寸 (子节点横排)。
func intrinsicTag(n *GuiNode) (w, h int) {
	cw, ch := stackContentSize(n, true)
	w = cw + 2*tagPadX
	if n.tagClosable() {
		w += tagCloseSize + tagGap
	}
	h = ch + 2*tagPadY
	if want := tagCloseSize + 2*tagPadY; h < want {
		h = want
	}
	return w, h
}

// layoutTag 布局标签: 子节点横排靠左, 关闭叉在右侧留位。
func layoutTag(n *GuiNode) {
	area := inner(n)
	w := area.W
	if n.tagClosable() {
		w -= tagCloseSize + tagGap
	}
	if w < 0 {
		w = 0
	}
	x := area.X
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		if cw > w {
			cw = w
		}
		c.Box = Rect{X: x, Y: area.Y, W: cw, H: ch}
		layoutNode(c)
		x += cw
	}
	placeAbsoluteIn(n, area)
}

// paintTag 画标签底 + 关闭叉 (文字由子节点常规绘制)。
func paintTag(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	base, custom := n.tagColor()
	if !custom {
		FillRect(img, b, tint(base, disabled))
		StrokeRect(img, b, tint(colorBtnEdge, disabled))
	} else {
		fillRoundRect(img, b, 3, tint(base, disabled))
	}
	if r := n.tagCloseRect(); r.W > 0 {
		drawCloseX(img, r, tint(colorPlaceholder, disabled), disabled)
	}
}

// ===== avatar: 头像 =====
//
//	<avatar src="assets/me.png" size={40} />
//	<avatar size={40}>A</avatar>        // 无图时画子文本首字母

// avatarSize 读 size prop (也接受 width), 缺省 36。
func (n *GuiNode) avatarSize() int {
	for _, name := range []string{"size", "width"} {
		if v, ok := n.PropNum(name); ok && v > 0 {
			return int(v)
		}
	}
	return 36
}

// avatarRound 报告是否画圆形 (shape="square" 时画方)。
func (n *GuiNode) avatarRound() bool {
	s, _ := n.PropStr("shape")
	return s != "square"
}

// intrinsicAvatar 是固定方形尺寸。
func intrinsicAvatar(n *GuiNode) (w, h int) {
	s := n.avatarSize()
	return s, s
}

// layoutAvatar 布局头像: 子节点 (文字) 居中。
func layoutAvatar(n *GuiNode) {
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X + (area.W-cw)/2, Y: area.Y + (area.H-ch)/2, W: cw, H: ch}
		layoutNode(c)
	}
	placeAbsoluteIn(n, area)
}

// paintAvatar 画头像: 有可解码的图就贴图, 否则浅底 + 首字母。
func paintAvatar(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	round := n.avatarRound()
	// 底色块
	if round {
		FillCircle(img, b.X+b.W/2, b.Y+b.H/2, min(b.W, b.H)/2, tint(colorOptionActive, disabled))
	} else {
		fillRoundRect(img, b, 4, tint(colorOptionActive, disabled))
	}
	// 有图: 等比缩放居中贴 (blitNearest 的 r 已是目标盒, 图按最近邻拉伸)
	if src, ok := n.PropStr("src"); ok && src != "" {
		if e, err := loadImage(src); err == nil && e != nil {
			blitNearest(img, b, e.img)
			return
		}
	}
	// 无图: 首字母
	initial := n.avatarInitial()
	if initial == "" {
		return
	}
	size := n.FontSize() + 2
	tw, th := MeasureText(initial, size)
	DrawText(img, img.Bounds(), initial, b.X+(b.W-tw)/2, b.Y+(b.H-th)/2, size,
		tint(colorText, disabled), tw)
}

// avatarInitial 取头像要显示的首字符 (子文本第一个字符, 大写)。
func (n *GuiNode) avatarInitial() string {
	for _, c := range n.Children {
		var t string
		if c.Tag == "#text" {
			t = c.Text
		} else if c.Tag == "text" {
			t = c.TextContent()
		}
		if t != "" {
			r := []rune(t)
			return strings.ToUpper(string(r[0]))
		}
	}
	return ""
}

// ===== empty: 空状态 =====
//
//	<empty desc="还没有数据" />
//	<empty desc="还没有数据"><button>新建</button></empty>

// emptyDesc 读 desc prop (也接受 description)。
func (n *GuiNode) emptyDesc() string {
	for _, name := range []string{"desc", "description"} {
		if s, ok := n.PropStr(name); ok && s != "" {
			return s
		}
	}
	return ""
}

// intrinsicEmpty 算空状态固有尺寸。
func intrinsicEmpty(n *GuiNode) (w, h int) {
	dw, dh := 0, 0
	if desc := n.emptyDesc(); desc != "" {
		dw, dh = MeasureText(desc, n.FontSize())
	}
	cw, ch := stackContentSize(n, false)
	if dw > cw {
		w = dw
	} else {
		w = cw
	}
	if w < emptyIconSize {
		w = emptyIconSize
	}
	h = emptyIconSize + emptyGap
	if dh > 0 {
		h += dh + emptyGap
	}
	h += ch
	return w, h
}

// layoutEmpty 布局空状态: 图示 → 描述 → 子节点, 全部居中。
func layoutEmpty(n *GuiNode) {
	area := inner(n)
	y := area.Y + emptyIconSize + emptyGap
	if desc := n.emptyDesc(); desc != "" {
		_, th := MeasureText(desc, n.FontSize())
		y += th + emptyGap
	}
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X + (area.W-cw)/2, Y: y, W: cw, H: ch}
		layoutNode(c)
		y += ch
	}
	placeAbsoluteIn(n, area)
}

// paintEmpty 画空状态图示 + 描述文字 (子节点由常规绘制处理)。
func paintEmpty(img *image.RGBA, n *GuiNode, disabled bool) {
	area := inner(n)
	ix := area.X + (area.W-emptyIconSize)/2
	drawEmptyGlyph(img, Rect{X: ix, Y: area.Y, W: emptyIconSize, H: emptyIconSize},
		tint(colorTrack, disabled))
	if desc := n.emptyDesc(); desc != "" {
		dw, dh := MeasureText(desc, n.FontSize())
		dx := area.X + (area.W-dw)/2
		dy := area.Y + emptyIconSize + emptyGap
		DrawText(img, img.Bounds(), desc, dx, dy, n.FontSize(), tint(colorPlaceholder, disabled), dw)
		_ = dh
	}
}

// drawEmptyGlyph 画"空"的简笔图示: 一个开口方箱 + 内部一小段斜线。
func drawEmptyGlyph(img *image.RGBA, r Rect, c color.RGBA) {
	if r.W <= 8 || r.H <= 8 {
		return
	}
	box := Rect{X: r.X + 4, Y: r.Y + r.H/3, W: r.W - 8, H: r.H * 2 / 3}
	StrokeRect(img, box, c)
	// 箱盖横线 (略宽于箱体)
	FillRect(img, Rect{X: box.X - 2, Y: box.Y, W: box.W + 4, H: 2}, c)
	// 内部斜线 (示意空)
	x0 := box.X + 6
	y0 := box.Y + box.H - 6
	for i := 0; i < 3; i++ {
		FillRect(img, Rect{X: x0 + i*3, Y: y0 - i*3, W: 2, H: 2}, c)
	}
}

// ===== 共用绘制小件 =====

// drawCloseX 在一个小方框里画一个 "×" (两条交叉线), 供 alert/tag 关闭叉复用。
func drawCloseX(img *image.RGBA, r Rect, c color.RGBA, disabled bool) {
	if r.W <= 2 || r.H <= 2 {
		return
	}
	pad := 2
	x0, y0 := r.X+pad, r.Y+pad
	x1, y1 := r.X+r.W-pad-1, r.Y+r.H-pad-1
	fillLine(img, x0, y0, x1, y1, 1, c)
	fillLine(img, x1, y0, x0, y1, 1, c)
}

// closeButtonAt 从 n 起沿祖先链找带关闭叉的 alert / tag, 若 (x,y) 落在
// 其叉上则返回该节点, 否则 nil。与 tabsInChain + tabStripAt 同一模式:
// 关闭叉是组件自绘区, 不在子树里, 只能靠几何命中。
func closeButtonAt(n *GuiNode, x, y int) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		var r Rect
		switch p.Tag {
		case "alert":
			r = p.alertCloseRect()
		case "tag":
			r = p.tagCloseRect()
		default:
			continue
		}
		if r.W > 0 && r.Contains(x, y) {
			return p
		}
	}
	return nil
}

// softFill 把一个颜色按比例 f 混到白底上 (做 alert 的浅色背景)。
//
// f=0 得纯白, f=1 得原色。直接用整数加权, 不用浮点 —— 结果稳定可断言。
func softFill(c color.RGBA, f float64) color.RGBA {
	if f <= 0 {
		return color.RGBA{R: 255, G: 255, B: 255, A: c.A}
	}
	if f > 1 {
		f = 1
	}
	inv := 1 - f
	return color.RGBA{
		R: uint8(float64(c.R)*f + 255*inv),
		G: uint8(float64(c.G)*f + 255*inv),
		B: uint8(float64(c.B)*f + 255*inv),
		A: c.A,
	}
}

// 抑制未使用告警占位: object 在部分构建标签下会被内联使用, 保留导入。
var _ = object.UndefinedSingleton
