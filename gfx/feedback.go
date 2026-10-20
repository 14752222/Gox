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
	alertPadX    = Space4 // alert 左右内边距 (8, Space token)
	alertPadY    = Space3 // alert 上下内边距 (6, Space token)
	alertAccentW = 3      // alert 左侧 level 实色条宽 (Prism S6: 4→3 发丝级)
	alertIconW   = 14     // alert 图标边长
	alertGap     = 8      // 图标与文本之间

	// badge 是**挂在宿主右上角外**的装饰, 四个数都是"出框"的量。
	badgeDotD  = 8  // 圆点直径
	badgePadX  = 4  // 数字胶囊左右内边距 (Space2)
	badgeFontM = 11 // 徽标字号 (比正文小一档, 免得盖住宿主)
	badgeOverX = 4  // 徽标越过宿主右缘 (Space2)
	badgeOverY = 4  // 徽标越过宿主上缘 (Space2)
	badgeRingW = 2  // 抠边环宽 (Space1)
	badgeMinW  = 16 // 数字胶囊最小宽 (一个字符时也别瘪成竖条)

	// tag: 高度 22 左右 (默认字号 16 + 行高 20, 上下各 1)、左右内边距 4。
	tagPadX      = Space2 // tag 左右内边距 (4)
	tagPadY      = 1      // tag 上下内边距 (默认字号下盒高 22)
	tagGap       = 4      // tag 文字与关闭叉之间
	tagCloseSize = 10     // tag/alert 关闭叉的命中边长

	// avatar: 24 / 32 / 40 是推荐档位 (size prop 本身不限档)。
	avatarDefault = 32
	avatarStatusD = 10 // 状态点直径
	avatarRingW   = 2  // 状态点抠边环宽 (Space1)

	emptyIconSize = 44     // empty 图示边长
	emptyGap      = Space5 // 图示与描述、描述与操作之间的间距 (12)
	emptySubGap   = Space2 // 主说明与副说明之间 (4)
)

// knockoutRingW 是"把装饰从宿主边缘上抠出来"的那一圈环宽 (badge / avatar
// 状态点共用同一条规矩: 见 drawKnockoutDisc 的注释)。
const knockoutRingW = badgeRingW

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

// alertLevelPalette 把 level prop 映射成语义三件套 (Prism S6):
// 实色 (左侧色条/图标)、subtle 底 (整块底色)、同族文字色 (subtle 上的前景)。
//
// 为什么不再用"彩色条 + 白底 + 黑字": 那是 v1 唯一凑得出来的表达 (只有实色
// token), 观感停留在 2013 年; tonal 三件套的对比度是逐对算过的 (见
// docs/accessibility.md §9), subtle 底 + 同族深字在四套预设里都稳过 AA。
func alertLevelPalette(n *GuiNode) (solid, subtle, text color.RGBA) {
	switch lv, _ := n.PropStr("level"); lv {
	case "success":
		return colorSuccess, colorSuccessSubtle, colorSuccessText
	case "warn", "warning":
		return colorWarning, colorWarningSubtle, colorWarningText
	case "error", "danger":
		return colorDanger, colorDangerSubtle, colorDangerText
	}
	return colorInfo, colorInfoSubtle, colorInfoText
}

// alertLevelColor 把 level prop 映射成强调实色 (色条/图标用)。
func alertLevelColor(n *GuiNode) color.RGBA {
	solid, _, _ := alertLevelPalette(n)
	return solid
}

// alertTextColor 是 alert 内文字的缺省色 (未写 color prop 时由 textColor 回落到这里)。
func alertTextColor(n *GuiNode) color.RGBA {
	_, _, txt := alertLevelPalette(n)
	return txt
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
	s := Space(tagCloseSize)
	return Rect{
		X: n.Box.X + n.Box.W - Space(alertPadX) - s,
		Y: n.Box.Y + (n.Box.H-s)/2,
		W: s, H: s,
	}
}

// intrinsicAlert 算横幅固有尺寸: 色条 + 图标 + 间距 + 文本内容 + 可选关闭叉。
func intrinsicAlert(n *GuiNode) (w, h int) {
	cw, ch := stackContentSize(n, false)
	// Prism S8: alert 这一组度量全是设计像素 (alertPadX 本身就是 Space4),
	// 而 ch/cw 来自已经换算过的内容 —— 漏换算的一侧就是"2x 屏上文字贴着图标"。
	padX, padY := Space(alertPadX), Space(alertPadY)
	accentW, gap := Space(alertAccentW), Space(alertGap)
	closeSide := Space(tagCloseSize) + Space(tagGap)
	w = accentW + padX + Space(alertIconW) + gap + cw
	if n.alertClosable() {
		w += closeSide
	}
	h = ch + 2*padY
	if min := Space(alertIconW); min > ch {
		h = min + 2*padY
	}
	return w, h
}

// layoutAlert 布局横幅: 文本子节点放在"色条 + 图标"右侧。
func layoutAlert(n *GuiNode) {
	area := inner(n)
	x := area.X + Space(alertAccentW) + Space(alertPadX) + Space(alertIconW) + Space(alertGap)
	w := area.X + area.W - x
	if n.alertClosable() {
		w -= Space(tagCloseSize) + Space(tagGap)
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

// paintAlert 画横幅 (Prism S6 tonal 风格): 整块 subtle 语义底 (圆角 r8) +
// 左侧 3px 实色条 (贴着圆角走) + 实色图标 + 关闭叉。文字色由 textColor 的
// alert 分支回落到同族 text 色 (见 raster.go)。
func paintAlert(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	solid, subtle, txt := alertLevelPalette(n)
	accent := tint(solid, disabled)
	bg := tint(subtle, disabled)
	rad := Radius(RadiusMD)
	fillRoundRect(img, b, rad, bg)
	// 左色条: 中段直写, 上下两个角带按外层圆角的覆盖率逐像素落笔 ——
	// 色条贴着圆角弧线收边, 而不是一根戳出圆角的方条。
	accentW := Space(alertAccentW)
	if accentW > 0 && b.H > 2*rad {
		FillRect(img, Rect{X: b.X, Y: b.Y + rad, W: accentW, H: b.H - 2*rad}, accent)
	}
	for dy := 0; dy < rad && dy < b.H; dy++ {
		for dx := 0; dx < accentW && dx < b.W; dx++ {
			if cov := roundCov(float64(b.X+dx)+0.5, float64(b.Y+dy)+0.5, b, rad); cov > 0 {
				blendPixel(img, b.X+dx, b.Y+dy, accent, cov)
			}
			yy := b.Y + b.H - 1 - dy
			if cov := roundCov(float64(b.X+dx)+0.5, float64(yy)+0.5, b, rad); cov > 0 {
				blendPixel(img, b.X+dx, yy, accent, cov)
			}
		}
	}
	// 图标 (实色圆 + 内部竖线/点), 垂直居中
	ix := b.X + Space(alertAccentW) + Space(alertPadX)
	iy := b.Y + (b.H-Space(alertIconW))/2
	drawAlertGlyph(img, Rect{X: ix, Y: iy, W: Space(alertIconW), H: Space(alertIconW)}, accent, disabled)
	// 关闭叉: 用同族文字色 (比 placeholder 更融进 tonal 底)
	if r := n.alertCloseRect(); r.W > 0 {
		drawCloseX(img, r, tint(txt, disabled), disabled)
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

// badgeSpec 是一枚徽标的全部参数。
//
// 两套写法共用它:
//
//	<badge count={5}><button>消息</button></badge>   // 包裹式 (徽标是独立的节点)
//	<button badge={5} max={99}>消息</button>         // 挂在宿主上 (脚本里最常见的写法)
//
// 为什么两套都要有: 包裹式多一层节点、多一次布局穿透, 而徽标 99% 的用法是
// "给一个已经有尺寸的控件加个角标" —— 那句话本来就写在宿主上最自然。
// 两种写法最终落成同一个 badgeSpec, 也就只有一条绘制路径。
type badgeSpec struct {
	count    int
	hasCount bool
	dot      bool
	max      int
}

// badgeSpec 读本节点上的徽标参数 (两套 prop 名都认)。
func (n *GuiNode) badgeSpec() badgeSpec {
	s := badgeSpec{max: 99}
	if v, ok := n.PropNum("count"); ok {
		s.count, s.hasCount = int(v), true
	}
	if v, _ := n.PropBool("dot"); v {
		s.dot = true
	}
	if v, ok := n.PropNum("badge"); ok {
		s.count, s.hasCount = int(v), true
	}
	if v, _ := n.PropBool("badgeDot"); v {
		s.dot = true
	}
	if v, ok := n.PropNum("max"); ok && v > 0 {
		s.max = int(v)
	}
	return s
}

// text 返回徽标数字文本 ("" = 不显示数字)。
func (s badgeSpec) text() string {
	if s.dot || !s.hasCount || s.count <= 0 {
		return ""
	}
	if s.count > s.max {
		return itoa(s.max) + "+"
	}
	return itoa(s.count)
}

// visible 报告是否要画徽标 (dot 形态恒显示)。
func (s badgeSpec) visible() bool {
	return s.dot || s.text() != ""
}

// badgeText 返回徽标数字文本 ("" = 不显示数字)。
func (n *GuiNode) badgeText() string { return n.badgeSpec().text() }

// badgeVisible 报告是否要画徽标 (dot 形态恒显示)。
func (n *GuiNode) badgeVisible() bool { return n.badgeSpec().visible() }

// badgeAnchor 返回徽标本体的右上角 (宿主右上角再往外让出 over 那一段)。
func (n *GuiNode) badgeAnchor() (int, int) {
	return n.Box.X + n.Box.W + Space(badgeOverX), n.Box.Y - Space(badgeOverY)
}

// paintBadge 画徽标本体 (由宿主在绘制阶段调, 见 raster.go 的徽标收尾)。
//
// 三件事, 都是"徽标压在宿主上"这件事逼出来的:
//  1. 抠边环: 徽标有一半压在宿主上, 直接画会被宿主的边缘/描边糊住边界。
//     先向外扩一环再落本体色, 就有了一条"宿主被挖掉一块"的缝。环色取
//     SurfaceBase (页底) —— 徽标绝大多数时候悬在页底上, 而且**没有读像素
//     反推底色这条路** (软件光栅器没有"取当前像素"的读接口, 有的只是逐点
//     算覆盖率), 取页底 token 是在"不知道底下是什么"时能拿到的最好近似;
//  2. 胶囊: 圆角取盒高的一半, 数字位数变化时 (5 → 128 → 99+) 形状自适应;
//  3. 文字色按对比度挑: 徽标底是语义实色, 深色主题里那一档实色会反过来变亮,
//     写死"白字"在暗色主题上是灾难 (S2 里 accent 就已经踩过一次)。
func paintBadge(img *image.RGBA, n *GuiNode, disabled bool) {
	sp := n.badgeSpec()
	if !sp.visible() || n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	// Prism S8: 徽标整套度量都是设计像素, 而它是**唯一自带字号**的装饰件 ——
	// 字号漏换算时, 高密屏上的表现是"数字没变、胶囊大了一倍"。
	size := dpToPx(badgeFontM)
	padX, dotD, ringW := Space(badgePadX), Space(badgeDotD), Space(badgeRingW)
	face := tint(colorDanger, disabled)
	ring := tint(colorSurfaceBase, disabled)
	ax, ay := n.badgeAnchor()
	if sp.dot {
		drawKnockoutDisc(img, ax-dotD/2, ay+dotD/2, dotD/2, ring, face)
		return
	}
	txt := sp.text()
	tw, th := MeasureText(txt, size)
	w := tw + 2*padX
	if minW := Space(badgeMinW); w < minW {
		w = minW
	}
	h := th + Space(2)
	if h < dotD {
		h = dotD
	}
	r := Rect{X: ax - w, Y: ay, W: w, H: h}
	rad := h / 2
	// 环: 盒外扩一整圈再落本体 (圆角同步加大, 内外弧线才平行)
	fillRoundRect(img, Rect{X: r.X - ringW, Y: r.Y - ringW,
		W: r.W + 2*ringW, H: r.H + 2*ringW}, rad+ringW, ring)
	fillRoundRect(img, r, rad, face)
	DrawText(img, img.Bounds(), txt, r.X+(r.W-tw)/2, r.Y+(r.H-th)/2, size,
		badgeInkColor(face), tw)
}

// badgeInkColor 在"同族深字"与"反色"之间挑对比度高的那个。
//
// 用 ContrastOn 而不是目测: 徽标底是**语义实色**, 而语义实色在深浅两套主题
// 里的方向是反的 (亮色主题的 danger 够深 → 配白字; 暗色主题的 danger 提亮
// 了 → 配深字)。写死任何一个都会在另一套主题上翻车, 而这两个候选里总有一个
// 是达标的 (见 docs/accessibility.md §9 的四对实测)。
func badgeInkColor(face color.RGBA) color.RGBA {
	deep, inverse := colorDangerText, colorTextInverse
	if ContrastOn(deep, face) >= ContrastOn(inverse, face) {
		return deep
	}
	return inverse
}

// drawKnockoutDisc 画一个"带抠边环的圆点": 先向外扩 ringW 画一圈底环, 再
// 落本体色。
func drawKnockoutDisc(img *image.RGBA, cx, cy, r int, ring, body color.RGBA) {
	if r <= 0 {
		return
	}
	// fillDiscAA 的圆心是像素中心口径, 与 FillCircle 同 (+0.5)。
	// Prism S8: 环宽随密度换算 —— 它是"本体之外再扩一圈"的量, 本体半径已经
	// 换算过, 环不换算的结果是高密屏上只剩一条发丝缝。
	ringW := Space(knockoutRingW)
	fillDiscAA(img, float64(cx)+0.5, float64(cy)+0.5, float64(r+ringW), ring)
	fillDiscAA(img, float64(cx)+0.5, float64(cy)+0.5, float64(r), body)
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

// tagVariant 读 variant prop。
func (n *GuiNode) tagVariant() string {
	s, _ := n.PropStr("variant")
	return s
}

// tagPalette 返回 tag 的底/字两色 —— 与 S6 的 alert **同一套 tonal 语言**:
// 语义 subtle 底 + 同族深字, 对比度逐对算过 (见 docs/accessibility.md §9)。
//
// v1 的 tag 是"浅灰底 + 1px 边", 且把 color 当成**底色**用 (于是
// <tag color="#1e7a45"> 出来的是绿底绿字, 字直接看不见)。S7 把 color 正回
// 它在这个系统里 everywhere else 的语义 —— 文字色: 一是与 button / text /
// alert 口径一致, 二是 textColor() 的继承链本就会命中它, tag 自己不必再
// 抄一遍。
func (n *GuiNode) tagPalette() (face, ink color.RGBA) {
	switch n.tagVariant() {
	case "success":
		face, ink = colorSuccessSubtle, colorSuccessText
	case "info":
		face, ink = colorInfoSubtle, colorInfoText
	case "warning", "warn":
		face, ink = colorWarningSubtle, colorWarningText
	case "danger", "error":
		face, ink = colorDangerSubtle, colorDangerText
	default:
		// default 档没有专属 token (61 个 token 的字段集 S1 已冻结, 加字段
		// 要动 Theme 六处)。中性芯片的三个候选里只有 track 在两套预设下
		// 都真的看得见:
		//   - surfaceInset / stateHover 在 prism-light 上与页底 (#F4F7FB)
		//     的相对亮度差只有 ~1%~9%, 芯片会整块糊进页面;
		//   - track (#D0D0D0 / #CBD5E1) 与页底的差是 1.57:1 / 1.39:1,
		//     两套预设里都是"一眼能看出这是块标签"。
		// 这是"没有 token 就自己钉常量"之外的另一条路: 用**已有的**那个
		// 在两套预设下都成立的 token, 而不是新造一个。
		face, ink = colorTrack, colorTextSecondary
	}
	// 显式 background 覆盖底, 其余交给继承链处理文字色。
	if c, ok := n.propColorOK("background"); ok {
		face = c
	}
	return face, ink
}

// tagTextColor 是 tag 内文字的缺省色 (未写 color prop 时由 textColor 回落)。
func tagTextColor(n *GuiNode) color.RGBA {
	_, ink := n.tagPalette()
	return ink
}

// propColorOK 读颜色 prop 并返回是否读到 (propColor 的"带 ok"版本 ——
// 组件要知道"到底写没写", 而不是拿一个 fallback 当真)。
func (n *GuiNode) propColorOK(name string) (color.RGBA, bool) {
	if s, ok := n.PropStr(name); ok {
		if c, ok := ParseColor(s); ok {
			return c, true
		}
	}
	return color.RGBA{}, false
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
	// Prism S8: tagPadX 本身是 Space2 (4dp), 取值处必须经 Space() 换算。
	s := Space(tagCloseSize)
	return Rect{
		X: n.Box.X + n.Box.W - Space(tagPadX) - s,
		Y: n.Box.Y + (n.Box.H-s)/2,
		W: s, H: s,
	}
}

// intrinsicTag 算标签固有尺寸 (子节点横排)。
func intrinsicTag(n *GuiNode) (w, h int) {
	cw, ch := stackContentSize(n, true)
	padX, padY := Space(tagPadX), Space(tagPadY)
	w = cw + 2*padX
	if n.tagClosable() {
		w += Space(tagCloseSize) + Space(tagGap)
	}
	h = ch + 2*padY
	if want := Space(tagCloseSize) + 2*padY; h < want {
		h = want
	}
	return w, h
}

// layoutTag 布局标签: 子节点横排靠左, 关闭叉在右侧留位。
func layoutTag(n *GuiNode) {
	// 这里**不能借 inner(n)**：inner 只读节点自己的 padding prop，而 <tag> 的
	// 内边距是 tagPadX/tagPadY 这一对常量（intrinsicTag 撑盒子用的也是它）。
	// 两处口径不一致的后果：盒子按「内容 + 2*tagPadX」被撑大，布局却把内容摆
	// 在 Box 原点 ⇒ 文字贴着左边框、右侧凭空多出一截空白（观感先坏），进而
	// 让「贴着边框内侧取样」的像素断言撞进字形抗锯齿边缘 —— macOS 上字形顶得
	// 更高，于是只有 mac 那一档红（见 TestTagDefaultAndCustomColor 的注释）。
	b := n.Box
	padX, padY := Space(tagPadX), Space(tagPadY)
	area := Rect{
		X: b.X + padX,
		Y: b.Y + padY,
		W: b.W - 2*padX,
		H: b.H - 2*padY,
	}
	if area.W < 0 {
		area.W = 0
	}
	if area.H < 0 {
		area.H = 0
	}
	w := area.W
	if n.tagClosable() {
		w -= Space(tagCloseSize) + Space(tagGap)
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
//
// v1 的缺省外观是"直角浅灰块 + 1px 边"。S7 换成 RadiusSM(6) 的 tonal 片:
// 高度压到 22、左右内边距收到 4, 与 alert 站进同一套语义色。
func paintTag(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	face, _ := n.tagPalette()
	rad := resolveRadiusPx(b, nodeRadiusPx(n, RadiusSM))
	fillRoundRect(img, b, rad, tint(face, disabled))
	if r := n.tagCloseRect(); r.W > 0 {
		// 关闭叉是组件自绘区 (不是子节点), 拿不到自己的悬停态, 只能借
		// tag 的: 鼠标在 tag 上就把叉提亮一档, 给"这里能点"一点反馈。
		ink := colorTextMuted
		if n.hovered && !n.disabledInChain() {
			ink = colorText
		}
		drawCloseX(img, r, tint(ink, disabled), disabled)
	}
}

// ===== avatar: 头像 =====
//
//	<avatar src="assets/me.png" size={40} />
//	<avatar size={40}>A</avatar>        // 无图时画子文本首字母

// avatarSize 读 size prop (也接受 width), 缺省 32。
//
// 24 / 32 / 40 是推荐档位 (与行高 32 / 40 的集合组件对齐), 但不做离散约束:
// 项目里有 size={48} 这类用法, 硬卡成档位会静默改掉别人的布局。
//
// 密度: 脚本写的 size/width 是**逻辑值** (与 width prop 同一口径, 见 drawerWidth),
// 内置缺省 avatarDefault 是设计像素 (与 fieldRowH 同一口径) —— 两条路都换算成
// 设备像素, 否则 2x 屏上 size={48} 的头像比没写 size 的还小。
func (n *GuiNode) avatarSize() int {
	for _, name := range []string{"size", "width"} {
		if v, ok := n.PropNum(name); ok && v > 0 {
			return dpToPx(v)
		}
	}
	return Space(avatarDefault)
}

// avatarShape 返回形状: circle (缺省) / rounded / square。
func (n *GuiNode) avatarShape() string {
	s, _ := n.PropStr("shape")
	switch s {
	case "square", "rounded":
		return s
	}
	return "circle"
}

// avatarStatus 读 status prop: online / offline / away。
func (n *GuiNode) avatarStatus() string {
	s, _ := n.PropStr("status")
	return s
}

// avatarStatusColor 把 status 映射成语义色 ("" = 不画状态点)。
func (n *GuiNode) avatarStatusColor() (color.RGBA, bool) {
	switch n.avatarStatus() {
	case "online":
		return colorSuccess, true
	case "away":
		return colorWarning, true
	case "offline":
		return colorTextDisabled, true
	}
	return color.RGBA{}, false
}

// avatarRadius 返回头像的圆角 (circle 时返回哨兵 -1, 由 resolveRadiusPx 落
// 成短边的一半)。
func (n *GuiNode) avatarRadius() int {
	switch n.avatarShape() {
	case "square":
		return Radius(RadiusSM)
	case "rounded":
		return Radius(RadiusMD)
	}
	return Radius(RadiusFull)
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

// paintAvatar 画头像: 有可解码的图就按形状裁着贴, 否则语义底 + 首字母。
func paintAvatar(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	face := tint(colorAccentSubtle, disabled)
	circle := n.avatarShape() == "circle"
	rad := resolveRadiusPx(b, n.avatarRadius())
	if circle {
		fillDiscAA(img, float64(b.X)+float64(b.W)/2, float64(b.Y)+float64(b.H)/2,
			float64(min(b.W, b.H))/2, face)
	} else {
		fillRoundRect(img, b, rad, face)
	}
	// 有图: 按形状裁着贴 (见 blitNearestShaped —— 没有路径裁剪, 只能逐像素
	// 判形状)。v1 这里直接 blitNearest 一整块方图, 圆形头像于是被撑成方的。
	if src, ok := n.PropStr("src"); ok && src != "" {
		if e, err := loadImage(src); err == nil && e != nil {
			blitNearestShaped(img, b, e.img, rad, circle)
			n.paintAvatarStatus(img, b, disabled)
			return
		}
	}
	// 无图: 首字母 (用语义 text, 在 accentSubtle 上两套预设都稳过 4.5:1)
	initial := n.avatarInitial()
	if initial != "" {
		size := n.FontSize() + 2
		tw, th := MeasureText(initial, size)
		DrawText(img, img.Bounds(), initial, b.X+(b.W-tw)/2, b.Y+(b.H-th)/2, size,
			tint(colorText, disabled), tw)
	}
	n.paintAvatarStatus(img, b, disabled)
}

// paintAvatarStatus 画右下角的状态点 (有 status prop 才画)。
//
// 与 badge 同一个难题: 它压在头像边线上, 不抠边就会被头像的弧线吃掉一半。
// 于是复用同一条规矩 —— 外扩一环页底色再落本体。
func (n *GuiNode) paintAvatarStatus(img *image.RGBA, b Rect, disabled bool) {
	c, ok := n.avatarStatusColor()
	if !ok {
		return
	}
	// 右下角"内接"位置: 沿 45° 方向坐在弧线上, 一半探出头像外。
	d := min(b.W, b.H)
	if d < Space(avatarStatusD) {
		return
	}
	off := float64(d)/2 - float64(Space(avatarStatusD))/2
	cx := float64(b.X) + float64(b.W)/2 + off*0.7071
	cy := float64(b.Y) + float64(b.H)/2 + off*0.7071
	drawKnockoutDisc(img, int(cx), int(cy), Space(avatarStatusD)/2,
		tint(colorSurfaceBase, disabled), tint(c, disabled))
}

// blitNearestShaped 把图片按"圆/圆角矩形"裁进目标盒。
//
// 没有路径裁剪, 只有两条路可走: 一是逐像素判"这个像素在不在形状里"再决定
// 落不落笔 (这里选的就是它), 二是先画方图再在四角补背景色 (要猜底色, 且
// 圆角处会留下硬边)。逐像素的代价是 O(w*h) —— 头像盒最多几十像素见方,
// 与省下的"猜底色"那堆特例相比很划算。
//
// 形状外的像素**保留底下的东西** (头像的语义底), 于是圆外露出的是一片
// accentSubtle, 而不是被挖成白洞。
func blitNearestShaped(img *image.RGBA, r Rect, src *image.RGBA, rad int, circle bool) {
	sb := src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	if r.W <= 0 || r.H <= 0 || sw <= 0 || sh <= 0 {
		return
	}
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	cx := float64(r.X) + float64(r.W)/2
	cy := float64(r.Y) + float64(r.H)/2
	if circle {
		rad = min(r.W, r.H) / 2
	}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			cov := 0.0
			if circle {
				cov = discCov(px, py, cx, cy, float64(rad))
			} else {
				cov = roundCov(px, py, r, rad)
			}
			if cov <= 0 {
				continue
			}
			// 最近邻采样 (与 blitNearest 同口径)
			sx := sb.Min.X + (x-r.X)*sw/r.W
			sy := sb.Min.Y + (y-r.Y)*sh/r.H
			c := src.RGBAAt(sx, sy)
			if c.A == 0 {
				continue
			}
			blendPixel(img, x, y, c, cov*float64(c.A)/255)
		}
	}
}

// discCov 是像素中心 (px,py) 对圆心 (cx,cy) 半径 r 的覆盖率。
// 与 fillDiscAA 内部的判据逐字一致 (r+0.5-d 钳到 [0,1])。
func discCov(px, py, cx, cy, r float64) float64 {
	d := sqrtFloat((px-cx)*(px-cx) + (py-cy)*(py-cy))
	cov := r + 0.5 - d
	if cov < 0 {
		return 0
	}
	if cov > 1 {
		return 1
	}
	return cov
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

// emptySubDesc 读副说明 subDesc prop (也接受 subDescription)。
//
// 副说明是"再给一句怎么办"的位置: 主说明说"还没有数据", 副说明说"换个筛选
// 条件试试"。两行分主次 (secondary / muted) 而不是同色两行 —— 同色的两行会
// 被读成"一句话被折行了"。
func (n *GuiNode) emptySubDesc() string {
	for _, name := range []string{"subDesc", "subDescription"} {
		if s, ok := n.PropStr(name); ok && s != "" {
			return s
		}
	}
	return ""
}

// emptyGraphic 返回充当插图的子节点 (目前只认 <icon>), 没有则 nil。
//
// 为什么要把插图从"子节点列表"里挑出来: 空状态是 **插图 → 描述 → 操作** 的
// 固定三段, 而子节点是平铺的一列 —— 若不挑, <icon> 会被排到描述和操作之间
// (V 形构图, 插图在最下面), 与所有空态的观感都相反。挑出来之后剩下的子节点
// 就是操作区, 顺序与脚本书写顺序一致。
//
// 只认 <icon> 而不是"第一个子节点": 操作区里第一个常常就是按钮, 按位置挑会
// 把按钮当成插图送上去。
func (n *GuiNode) emptyGraphic() *GuiNode {
	for _, c := range n.Children {
		if c.Tag == "icon" {
			return c
		}
	}
	return nil
}

// emptyGraphicH 是插图占的高度: 有 <icon> 就按它的尺寸, 否则内置简笔箱的。
func emptyGraphicH(n *GuiNode) int {
	if g := n.emptyGraphic(); g != nil {
		_, gh := g.intrinsicSize()
		return gh
	}
	return Space(emptyIconSize)
}

// emptyPadDp 是 <empty> 的内边距缺省值 (设计像素), 脚本显式写了 padding 时为 0。
//
// 为什么默认给 Space6 (16) 而不是 0: 空状态的视觉主体是"一整块留白里的几行
// 字", 盒子紧贴内容时它跟一个装了文字的 column 没有区别, 放进卡片/弹层就是
// 字贴着边。S7 的验收点是"内边距 ≥ Space6", 与其让每个脚本各写一遍
// padding={16}, 不如组件自带。
//
// 脚本写了 padding 就返回 0: 那一档完全交给 paddingOf / inner 的老路径
// (padding 是"往盒子里缩"而不是"把盒子撑大"), 与本组件此前的语义一致,
// 免得改一处默认把显式 padding 的用法也改掉。
func emptyPadDp(n *GuiNode) int {
	if _, ok := n.PropNum("padding"); ok {
		return 0
	}
	return Space6
}

// intrinsicEmpty 算空状态固有尺寸。
//
// 缺省时四边各加 Space6 (见 emptyPadDp): 空态的盒子是"内容 + 呼吸", 不是
// 内容的外接矩形。
func intrinsicEmpty(n *GuiNode) (w, h int) {
	dw := 0
	if desc := n.emptyDesc(); desc != "" {
		dw, _ = MeasureText(desc, n.FontSize())
	}
	if sub := n.emptySubDesc(); sub != "" {
		sw, _ := MeasureText(sub, n.FontSize())
		if sw > dw {
			dw = sw
		}
	}
	// 操作区尺寸: 插图不算在内 (它量在 emptyGraphicH 里), 否则 44px 的图标
	// 会被重复计入高度。
	aw, ah := 0, 0
	graphic := n.emptyGraphic()
	for _, c := range n.Children {
		if !c.isFlowChild() || c == graphic {
			continue
		}
		cw, ch := c.intrinsicSize()
		if cw > aw {
			aw = cw
		}
		ah += ch
	}
	if dw > aw {
		w = dw
	} else {
		w = aw
	}
	if w < Space(emptyIconSize) {
		w = Space(emptyIconSize)
	}
	// 高度直接取 emptyContentH: 布局 (居中偏移) 与测量必须用同一个数字,
	// 否则"算居中的高度"和"算盒子的高度"会各自漏掉一个 gap。
	pad := Space(emptyPadDp(n))
	return w + 2*pad, n.emptyContentH() + 2*pad
}

// layoutEmpty 布局空状态: 插图 → 描述 → 副说明 → 操作, 全部水平居中,
// 整块在盒子里垂直居中 (盒子比内容高时, 内容整体下移差值的一半)。
func layoutEmpty(n *GuiNode) {
	area := inner(n)
	gh := emptyGraphicH(n)
	// 垂直居中: 盒子比内容高时把整块下移差值的一半 (空态最常见的两种情况
	// 是"卡片里的一小块"与"撑满整个内容区", 后者不居中的话字会粘在上边)。
	total := n.emptyContentH()
	graphic := n.emptyGraphic()
	y := area.Y + (area.H-total)/2
	if y < area.Y {
		y = area.Y
	}
	if g := graphic; g != nil {
		gw, _ := g.intrinsicSize()
		g.Box = Rect{X: area.X + (area.W-gw)/2, Y: y, W: gw, H: gh}
		layoutNode(g)
	}
	y += gh + emptyGap
	if desc := n.emptyDesc(); desc != "" {
		_, th := MeasureText(desc, n.FontSize())
		y += th
		if n.emptySubDesc() != "" {
			y += emptySubGap + th
		}
		y += emptyGap
	}
	for _, c := range n.Children {
		if !c.isFlowChild() || c == graphic {
			continue
		}
		cw, ch := c.intrinsicSize()
		c.Box = Rect{X: area.X + (area.W-cw)/2, Y: y, W: cw, H: ch}
		layoutNode(c)
		y += ch
	}
	placeAbsoluteIn(n, area)
}

// paintEmpty 画空状态图示 + 说明文字 (子节点由常规绘制处理)。
//
// 内置简笔箱只在没有 <icon> 插图时画: 两个图形摞在一起不是"更丰富", 是
// 两个互相抢戏的方块。
func paintEmpty(img *image.RGBA, n *GuiNode, disabled bool) {
	area := inner(n)
	gh := emptyGraphicH(n)
	oy := area.Y + max(0, (area.H-n.emptyContentH())/2)
	if n.emptyGraphic() == nil {
		ix := area.X + (area.W-Space(emptyIconSize))/2
		drawEmptyGlyph(img, Rect{X: ix, Y: oy, W: Space(emptyIconSize), H: Space(emptyIconSize)},
			tint(colorTrack, disabled))
	}
	desc := n.emptyDesc()
	if desc == "" {
		return
	}
	size := n.FontSize()
	dw, th := MeasureText(desc, size)
	dx := area.X + (area.W-dw)/2
	dy := oy + gh + emptyGap
	DrawText(img, img.Bounds(), desc, dx, dy, size, tint(colorTextSecondary, disabled), dw)
	sub := n.emptySubDesc()
	if sub == "" {
		return
	}
	sw, _ := MeasureText(sub, size)
	DrawText(img, img.Bounds(), sub, area.X+(area.W-sw)/2, dy+th+emptySubGap, size,
		tint(colorTextMuted, disabled), sw)
}

// emptyContentH 是"插图 + 文字 + 操作"的竖排总高 (与 layoutEmpty 的 total 同
// 一口径)。提成函数而不是两边各算一遍: 布局与绘制用同一个数字, 才不会出现
// "字按居中算、图按顶边画" 这种错开几像素的错位。
func (n *GuiNode) emptyContentH() int {
	h := emptyGraphicH(n)
	if desc := n.emptyDesc(); desc != "" {
		_, th := MeasureText(desc, n.FontSize())
		h += emptyGap + th
		if n.emptySubDesc() != "" {
			h += emptySubGap + th
		}
	}
	graphic := n.emptyGraphic()
	for _, c := range n.Children {
		if !c.isFlowChild() || c == graphic {
			continue
		}
		_, ch := c.intrinsicSize()
		h += emptyGap + ch
	}
	return h
}

// drawEmptyGlyph 画"空"的简笔图示: 一个开口方箱 + 内部一小段斜线。
//
// 箱体占满整个 44 的方框 (上下各留 2 给箱盖): 旧实现只占下 2/3, 于是方框里
// 有 14px 的空白; 那段空白被算进"内容高度", 垂直居中就把整块内容算低了 ——
// 量出来的盒子与看到的墨不是同一个东西。
func drawEmptyGlyph(img *image.RGBA, r Rect, c color.RGBA) {
	if r.W <= 8 || r.H <= 8 {
		return
	}
	box := Rect{X: r.X + 4, Y: r.Y + 2, W: r.W - 8, H: r.H - 4}
	StrokeRect(img, box, c)
	// 箱盖横线 (略宽于箱体, 压在箱体上沿)
	FillRect(img, Rect{X: box.X - 2, Y: box.Y, W: box.W + 4, H: 2}, c)
	// 内部斜线 (示意空): 三段 2px 方块, 从左下往右上
	x0 := box.X + 6
	y0 := box.Y + box.H - 8
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
//
// Prism S8: 触摸模式下把叉的命中区补到 TouchMin —— 关闭叉画出来只有十几像素
// 见方, 是全库最容易点空的目标, 而点空的代价是"关不掉"。视觉尺寸不变。
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
		if r.W > 0 && touchHitContains(r, x, y) {
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
