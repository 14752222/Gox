package gfx

import (
	"image"
	"image/color"
	"strconv"
	"strings"
)

// 软件光栅化 (P2): 实心矩形、1px 边框、背景色。
// 文字渲染留给 P3 (sfnt 字体), #text 节点当前不绘制。

// namedColors 少量常用命名色 (CSS 子集, 覆盖演示需求)。
var namedColors = map[string]color.RGBA{
	"black": {0, 0, 0, 255}, "white": {255, 255, 255, 255},
	"red": {220, 20, 60, 255}, "green": {34, 139, 34, 255},
	"blue": {30, 144, 255, 255}, "gray": {128, 128, 128, 255},
	"grey": {128, 128, 128, 255}, "silver": {192, 192, 192, 255},
	"yellow": {255, 215, 0, 255}, "orange": {255, 140, 0, 255},
	"purple": {128, 0, 128, 255}, "pink": {255, 105, 180, 255},
	"brown": {139, 69, 19, 255}, "navy": {0, 0, 128, 255},
	"teal": {0, 128, 128, 255}, "crimson": {220, 20, 60, 255},
}

// 内置组件默认色板。组件专用 (background 在控件上有"强调色"语义,
// 与通用盒子的"填充色"不同), JS 侧可用 props 逐个覆盖。
var (
	colorAccent     = color.RGBA{R: 0x27, G: 0xAE, B: 0x60, A: 255} // 选中/进度填充 (主题绿)
	colorAccentText = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255} // 强调色上的前景 (勾/圆点)
	colorFieldEdge  = color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255} // checkbox/radio/switch 边框
	colorBtnFace    = color.RGBA{R: 0xE8, G: 0xE8, B: 0xE8, A: 255} // button 缺省底色
	colorBtnEdge    = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255} // button 缺省边框
	colorTrack      = color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255} // 轨道/分隔线 (浅灰)
	colorSwitchOff  = color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 255} // switch 关闭态轨道
	colorKnob       = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255} // switch 滑块
	colorText       = color.RGBA{R: 26, G: 26, B: 26, A: 255}       // 缺省文字色 (近黑)
	colorFocusRing  = color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255} // 焦点虚线框 (P1-3)
	// P2-3 / P2-4 字段与弹层
	colorFieldFace    = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255} // 输入类字段底色
	colorFieldHover   = color.RGBA{R: 0xF0, G: 0xF4, B: 0xF9, A: 255} // 字段悬停底 (浅蓝灰)
	colorFieldPress   = color.RGBA{R: 0xE0, G: 0xE9, B: 0xF4, A: 255} // 字段按压底
	colorInputEdge    = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255} // 输入类字段边框 (与 input 一致)
	colorPlaceholder  = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255} // placeholder 灰字
	colorScrollTrack  = color.RGBA{R: 0, G: 0, B: 0, A: 0x14}         // 滚动条轨道 (半透明黑)
	colorScrollThumb  = color.RGBA{R: 0xA0, G: 0xA0, B: 0xA0, A: 255} // 滚动条滑块
	colorOptionActive = color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255} // 下拉项高亮底 (浅蓝)
	colorPopupFace    = color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255} // 弹层/卡片底色
	colorPopupEdge    = color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255} // 弹层/卡片边框
	colorMask         = color.RGBA{R: 0, G: 0, B: 0, A: 0x66}         // modal 遮罩 (40% 黑)
	colorInfo         = color.RGBA{R: 0x2F, G: 0x80, B: 0xED, A: 255} // toast info
	colorWarn         = color.RGBA{R: 0xE8, G: 0x89, B: 0x0C, A: 255} // toast warn
	colorDanger       = color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255} // toast error
)

// 弹层缺省外观的十六进制写法: Go 侧构造节点时写进 props, 于是"缺省样式"
// 与"脚本显式给的样式"走完全同一条读取路径 (没有第二套默认值逻辑)。
const (
	colorPopupFaceHex = "#ffffff"
	colorPopupEdgeHex = "#999999"
)

// hoverBrighten / pressDarken 是悬停/按压的通道偏移量 (P1-4)。
const (
	hoverBrighten = 12
	pressDarken   = 24
)

// grayTint 是禁用态降饱和的灰度锚点 (各通道与之取平均)。
const grayTint = 190

// ParseColor 解析颜色:
//
//	#rgb / #rgba / #rrggbb / #rrggbbaa
//	rgb(r,g,b) / rgba(r,g,b,a)   (分量 0-255; alpha 写小数时按 0-1 换算)
//	命名色 (见 namedColors)
//
// 失败返回 ok=false。
func ParseColor(s string) (color.RGBA, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if c, ok := namedColors[s]; ok {
		return c, true
	}
	if strings.HasPrefix(s, "#") {
		return parseHexColor(s[1:])
	}
	if strings.HasPrefix(s, "rgb(") || strings.HasPrefix(s, "rgba(") {
		return parseFuncColor(s)
	}
	return color.RGBA{}, false
}

// parseHexColor 解析 # 之后的十六进制部分: 3/4 位每通道 1 个 nibble
// (重复成 2 位, 如 #f00 → #ff0000), 6/8 位每通道 2 位。
// 4/8 位的最后一位是 alpha。
func parseHexColor(hex string) (color.RGBA, bool) {
	var v [4]uint8
	switch len(hex) {
	case 3, 4:
		for i := 0; i < len(hex); i++ {
			d, ok := hexDigit(hex[i])
			if !ok {
				return color.RGBA{}, false
			}
			v[i] = d * 17 // 0xf → 0xff
		}
		if len(hex) == 3 {
			v[3] = 255
		}
	case 6, 8:
		for i := 0; i < len(hex); i += 2 {
			hi, ok1 := hexDigit(hex[i])
			lo, ok2 := hexDigit(hex[i+1])
			if !ok1 || !ok2 {
				return color.RGBA{}, false
			}
			v[i/2] = hi*16 + lo
		}
		if len(hex) == 6 {
			v[3] = 255
		}
	default:
		return color.RGBA{}, false
	}
	return color.RGBA{R: v[0], G: v[1], B: v[2], A: v[3]}, true
}

func hexDigit(b byte) (uint8, bool) {
	switch {
	case b >= '0' && b <= '9':
		return b - '0', true
	case b >= 'a' && b <= 'f':
		return b - 'a' + 10, true
	}
	return 0, false
}

// parseFuncColor 解析 rgb(...) / rgba(...)。分量按 0-255 解析 (浮点也接受,
// 如 rgb(255.5,0,0))。
//
// alpha 的两种写法都接受: 带小数点或值 <= 1 时按 0-1 比例换算
// (rgba(0,0,0,0.4) → 102, rgba(255,255,255,1) → 255, 与 CSS 一致),
// 其余按 0-255 (rgba(0,0,0,128) → 128)。这样脚本不必记住用哪套刻度 ——
// 唯一被这种宽容牺牲的是"用 1 表示 1/255 透明度", 实际不会有人这么写。
func parseFuncColor(s string) (color.RGBA, bool) {
	open := strings.IndexByte(s, '(')
	if open < 0 || !strings.HasSuffix(s, ")") {
		return color.RGBA{}, false
	}
	parts := strings.Split(s[open+1:len(s)-1], ",")
	if len(parts) != 3 && len(parts) != 4 {
		return color.RGBA{}, false
	}
	var v [4]uint8
	v[3] = 255
	for i, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return color.RGBA{}, false
		}
		f, err := strconv.ParseFloat(p, 64)
		if err != nil {
			return color.RGBA{}, false
		}
		if i == 3 {
			if strings.Contains(p, ".") || f <= 1 {
				v[3] = uint8(clampFloat(f, 0, 1)*255 + 0.5)
			} else {
				v[3] = uint8(clampFloat(f, 0, 255))
			}
			continue
		}
		v[i] = uint8(clampFloat(f, 0, 255))
	}
	return color.RGBA{R: v[0], G: v[1], B: v[2], A: v[3]}, true
}

func clampFloat(f, lo, hi float64) float64 {
	if f < lo {
		return lo
	}
	if f > hi {
		return hi
	}
	return f
}

// textColor 返回文本颜色: 自身 color prop → 祖先的 color prop (CSS 式继承,
// 让 <button color="#fff">文字</button> 这类写法生效) → 缺省近黑。
func (n *GuiNode) textColor() color.RGBA {
	for p := n; p != nil; p = p.Parent {
		if s, ok := p.PropStr("color"); ok {
			if c, ok := ParseColor(s); ok {
				return c
			}
		}
	}
	return colorText
}

// propColor 读取颜色属性 (prop 名 → 颜色), 缺失或非法时返回 fallback。
func (n *GuiNode) propColor(name string, fallback color.RGBA) color.RGBA {
	if s, ok := n.PropStr(name); ok {
		if c, ok := ParseColor(s); ok {
			return c
		}
	}
	return fallback
}

// dim 给禁用态降饱和: 各通道与 grayTint 取平均。简单实现即可,
// 不做颜色空间转换 (禁用态只需"看起来灰了")。
func dim(c color.RGBA) color.RGBA {
	return color.RGBA{
		R: uint8((int(c.R) + grayTint) / 2),
		G: uint8((int(c.G) + grayTint) / 2),
		B: uint8((int(c.B) + grayTint) / 2),
		A: c.A,
	}
}

// tint 按禁用态可选地降饱和。
func tint(c color.RGBA, disabled bool) color.RGBA {
	if disabled {
		return dim(c)
	}
	return c
}

// brighten / darken 给通道加减一个偏移后钳位到 [0,255] (hover/press 反馈)。
func brighten(c color.RGBA, d int) color.RGBA {
	return color.RGBA{R: clamp8(int(c.R) + d), G: clamp8(int(c.G) + d), B: clamp8(int(c.B) + d), A: c.A}
}

func darken(c color.RGBA, d int) color.RGBA { return brighten(c, -d) }

func clamp8(v int) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Draw 把元素树画到 img (先父后子, 后画的覆盖先画的)。
func Draw(img *image.RGBA, root *GuiNode) {
	DrawClipped(img, root, img.Bounds())
}

// DrawClipped 画元素树, 只光栅化与 clip 相交的子树 (脏矩形局部重绘)。
//
// 裁剪靠"子图"实现: 把 img 收窄成 SubImage(clip) 后再递归, FillRect 的
// 索引相对 img.Rect.Min, 于是所有绘制原语自动被限制在 clip 内 —— 不必给
// 每个原语都加一个 clip 参数。深度裁剪就是再套一层子图 (drawNode 里对
// 绝对定位子节点做父盒求交)。
//
// 逃逸裁剪的子树 (弹层) 单独收集、最后补画, 且拿回未收窄的 canvas,
// 因此不受任何祖先盒子裁剪。
//
// 收集放在最前面按整棵树做, 而不是"边遍历边收集": 后者会被可见性剪枝
// 带偏 —— 祖先盒子与脏区不相交时整个分支被跳过, 分支里的弹层也跟着丢。
// 而弹层恰恰经常超出祖先盒子 (28px 高的 select 下面挂一个下拉框), 局部
// 重绘只覆盖弹层那一片时就会整块不画。收集与裁剪解耦后, 常规内容可以
// 放心剪枝, 弹层始终按绘制序补画。
func DrawClipped(img *image.RGBA, root *GuiNode, clip image.Rectangle) {
	if root == nil {
		return
	}
	clip = clip.Intersect(img.Bounds())
	if clip.Empty() {
		return
	}
	canvas := img.SubImage(clip).(*image.RGBA)
	escapes := escapesInDrawOrder(root)
	drawNode(canvas, root)
	for _, e := range escapes {
		drawNode(canvas, e)
	}
}

// clipTo 把 img 的可绘制范围收窄到 r 与当前范围的交集。
// 交集为空时返回一个空子图 (子图 .Rect 空 → 所有原语写入都不落笔)。
func clipTo(img *image.RGBA, r Rect) *image.RGBA {
	b := img.Bounds()
	inter := image.Rectangle{
		Min: image.Point{r.X, r.Y},
		Max: image.Point{r.X + r.W, r.Y + r.H},
	}.Intersect(b)
	return img.SubImage(inter).(*image.RGBA)
}

func drawNode(img *image.RGBA, n *GuiNode) {
	clipRect := img.Bounds()
	if clipRect.Empty() {
		return
	}
	// 关闭的弹层直接剪掉整支: dialog 只是把盒子清空了, 子树还在树上
	// (脚本仍持有节点), 不挡这一下的话关掉的对话框内容照画不误。
	if n.isOverlay() && !n.overlayVisible() {
		return
	}
	box := image.Rectangle{
		Min: image.Point{n.Box.X, n.Box.Y},
		Max: image.Point{n.Box.X + n.Box.W, n.Box.Y + n.Box.H},
	}
	// 不与脏区相交的子树整支跳过 (#text 无固定框, 由父级框粗判)。
	// 零尺寸盒子只在没有子节点时才剪掉: 子节点可能溢出父框 (未约束尺寸
	// 的容器、绝对定位), 父框为空不代表子树不可见。
	if n.Tag != "#text" {
		if box.Empty() {
			if len(n.Children) == 0 {
				return
			}
		} else if !box.Overlaps(clipRect) {
			return
		}
	}
	// 禁用态沿祖先链继承 (按需查找, 树深通常 <10, 相对光栅化开销可忽略)
	disabled := n.disabledInChain()
	if n.Tag == "#text" {
		// 文本节点: 在自身框内绘制 (超宽截断)
		DrawText(img, clipRect, n.Text, n.Box.X, n.Box.Y, n.FontSize(),
			tint(n.textColor(), disabled), n.Box.W)
		return
	}
	switch n.Tag {
	case "text":
		// 文本容器: 拼接 #text 子节点; 开了 wrap 就逐行绘制 (行高统一取
		// lineHeight, 与 textblock.go 的测量口径一致), 否则单行截断。
		if text := n.TextContent(); text != "" {
			c := tint(n.textColor(), disabled)
			if n.wrapsText() {
				lines := n.blockLines(n.Box.W)
				lh := lineHeight(n.FontSize())
				for i, ln := range lines {
					if y := n.Box.Y + i*lh; y >= n.Box.Y+n.Box.H {
						break // 盒高不够 (被父容器压过) 就不再画多余的整行
					}
					DrawText(img, clipRect, ln, n.Box.X, n.Box.Y+i*lh,
						n.FontSize(), c, n.Box.W)
				}
			} else {
				DrawText(img, clipRect, text, n.Box.X, n.Box.Y, n.FontSize(), c, n.Box.W)
			}
		}
	case "checkbox", "radio", "switch", "progress", "separator", "spacer":
		// 内置组件: 自带外观 (background 在控件上是强调色, 不走通用填充)
		drawWidget(img, n, disabled)
	case "select":
		paintSelect(img, n, disabled)
	case "select-option":
		paintSelectOption(img, n, disabled)
	case "dialog":
		paintDialog(img, n)
	case "toast":
		paintToast(img, n, disabled)
	case "input":
		paintInput(img, n, disabled)
	case "scroll":
		paintScroll(img, n, disabled)
	case "textarea":
		paintTextarea(img, n, disabled)
	case "image":
		paintImage(img, n, disabled)
	case "canvas":
		paintCanvas(img, n, disabled)
	default:
		// 通用盒子 / button: background 填充 + border 描边 (button 有缺省外观)。
		// 交互反馈 (P1-4) 只对 button 有实际效果: 其他标签没有缺省面,
		// 未显式给 background 时 backgroundFor 返回 false, 不会走到这里。
		if bg, ok := n.backgroundFor(); ok {
			FillRect(img, n.Box, tint(n.interactiveFace(bg), disabled))
		}
		if bd, ok := n.borderFor(); ok {
			StrokeRect(img, n.Box, tint(bd, disabled))
		}
	}
	for _, c := range zOrderedChildren(n) {
		if c.escapeClipping() {
			// 逃逸子树 (弹层) 不在这里画: 它们已由 DrawClipped 按绘制序
			// 收集, 并在常规内容之后以"根层级"重画一次 (不受父盒裁剪)。
			continue
		}
		sub := img
		switch {
		case n.Tag == "scroll":
			// 滚动容器: 子内容一律裁到视口 (内容超高时右侧还扣掉滚动条
			// 占位), 溢出容器的部分不绘制也不命中 (命中测试用同一套盒子)。
			sub = clipTo(img, n.scrollViewport())
		case c.positionAbsolute():
			// 绝对定位默认仍被父节点盒子裁剪; 想溢出父框必须显式
			// escapeClipping (弹层组件的出口就是这个开关)。
			sub = clipTo(img, n.Box)
		}
		drawNode(sub, c)
	}
}

// backgroundFor 返回节点填充色: 显式 background prop 优先, button 无显式
// 值时回落到缺省浅灰面, 其余标签无填充。
func (n *GuiNode) backgroundFor() (color.RGBA, bool) {
	if s, ok := n.PropStr("background"); ok {
		if c, ok := ParseColor(s); ok {
			return c, true
		}
	}
	if n.Tag == "button" {
		return colorBtnFace, true
	}
	return color.RGBA{}, false
}

// borderFor 返回节点描边色: 同上, button 缺省 1px 深灰边框。
func (n *GuiNode) borderFor() (color.RGBA, bool) {
	if s, ok := n.PropStr("border"); ok {
		if c, ok := ParseColor(s); ok {
			return c, true
		}
	}
	if n.Tag == "button" {
		return colorBtnEdge, true
	}
	return color.RGBA{}, false
}

// drawWidget 分派内置组件的绘制 (P0-1 / P0-2)。
func drawWidget(img *image.RGBA, n *GuiNode, disabled bool) {
	switch n.Tag {
	case "checkbox":
		paintCheckbox(img, n, disabled)
	case "radio":
		paintRadio(img, n, disabled)
	case "switch":
		paintSwitch(img, n, disabled)
	case "progress":
		paintProgress(img, n, disabled)
	case "separator":
		// 分隔线: 盒子即细条 (横 1px 高 / 纵 1px 宽), 直接填充
		FillRect(img, n.Box, tint(n.propColor("background", colorTrack), disabled))
	case "spacer":
		// 弹性占位: 不绘制任何内容, 仅参与布局 (flexGrow 撑开主轴)
	}
}

// paintCheckbox 画复选框: 18x18 边框空盒, 选中时填强调色并画勾。
// 勾用两段 2px 粗的短线拼接, 斜率按 18px 盒子近似 (不做抗锯齿)。
func paintCheckbox(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	edge := tint(n.propColor("border", colorFieldEdge), disabled)
	if n.checked() {
		FillRect(img, b, tint(n.faceColor(colorAccent), disabled))
	}
	StrokeRect(img, b, edge)
	if !n.checked() {
		return
	}
	mark := tint(n.propColor("color", colorAccentText), disabled)
	// 勾: 起点 (22%,50%) → 折点 (39%,67%) → 终点 (72%,28%), 按盒子比例取点
	start := image.Pt(b.X+pct(b.W, 22), b.Y+pct(b.H, 50))
	bottom := image.Pt(b.X+pct(b.W, 39), b.Y+pct(b.H, 67))
	end := image.Pt(b.X+pct(b.W, 72), b.Y+pct(b.H, 28))
	fillLine(img, start.X, start.Y, bottom.X, bottom.Y, 2, mark)
	fillLine(img, bottom.X, bottom.Y, end.X, end.Y, 2, mark)
}

// paintRadio 画单选框: 18x18 外圈 1px 圆环, 选中时中心实心圆点。
// 分组互斥不做进内核, 由 JS 用 signal 控制 checked (见 testdata/form_demo.js)。
func paintRadio(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	// 半径取 (短边-1)/2: 圆心在盒子中心, 保证圆环整圈落在盒子内
	r := (min(b.W, b.H) - 1) / 2
	if r < 2 {
		return
	}
	cx, cy := b.X+b.W/2, b.Y+b.H/2
	StrokeCircle(img, cx, cy, r, tint(n.propColor("border", colorFieldEdge), disabled))
	if n.checked() {
		dotR := min(b.W, b.H) / 4 // 18px 盒子 → 直径约 8px
		if dotR < 1 {
			dotR = 1
		}
		FillCircle(img, cx, cy, dotR, tint(n.faceColor(colorAccent), disabled))
	}
}

// paintSwitch 画开关: 36x20 方形轨道 + 16x16 方块滑块, 位置由 checked 决定。
// (v1 方形轨道, 圆角轨道留待绘制系统升级。)
func paintSwitch(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	checked := n.checked()
	track := n.interactiveFace(colorSwitchOff) // 关闭态也做悬停/按压反馈
	if checked {
		track = n.faceColor(colorAccent)
	}
	FillRect(img, b, tint(track, disabled))

	knobW := b.H * 16 / 20 // 20px 高时 16px 宽 (盒子被拉伸时按比例)
	if knobW > b.W/2 {
		knobW = b.W / 2
	}
	if knobW < 2 {
		return
	}
	kw := knobW
	kh := b.H - 4
	if kh < 2 {
		kh = b.H
	}
	ky := b.Y + (b.H-kh)/2
	kx := b.X + 2
	if checked {
		kx = b.X + b.W - kw - 2
	}
	FillRect(img, Rect{X: kx, Y: ky, W: kw, H: kh}, tint(colorKnob, disabled))
}

// paintProgress 画进度条: 轨道浅灰 + 前景按 value 比例填充, value 已钳位 [0,1]。
func paintProgress(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	FillRect(img, b, tint(colorTrack, disabled))
	fw := int(float64(b.W) * n.progressValue())
	if fw <= 0 {
		return
	}
	if fw > b.W {
		fw = b.W
	}
	FillRect(img, Rect{X: b.X, Y: b.Y, W: fw, H: b.H},
		tint(n.propColor("background", colorAccent), disabled))
}

// clip 求矩形与画布的交集。
func clip(img *image.RGBA, r Rect) (x0, y0, x1, y1 int, visible bool) {
	b := img.Bounds()
	x0, y0 = max(r.X, b.Min.X), max(r.Y, b.Min.Y)
	x1, y1 = min(r.X+r.W, b.Max.X), min(r.Y+r.H, b.Max.Y)
	return x0, y0, x1, y1, x0 < x1 && y0 < y1
}

// FillRect 填充实心矩形 (越界部分裁剪)。
//
// 不透明色 (A=255) 直接写入 (绝大多数绘制走这条); 半透明色与底色做
// src-over 合成而不是覆盖 —— 遮罩层/toast 需要"透出下面的内容"。
//
// 索引一律相对 img.Rect.Min 计算: 传入子图 (SubImage, 见 raster.go 的
// clipTo) 时光栅化范围自动收窄到子图内, 这就是父盒裁剪/脏区裁剪的实现
// 方式 —— 全图时 Min=(0,0), 与直觉写法完全等价。
func FillRect(img *image.RGBA, r Rect, c color.RGBA) {
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	ca := uint32(c.A)
	if ca == 0 {
		return // 全透明: 不改动画面 (也不该白白遍历像素)
	}
	stride := img.Stride
	pix := img.Pix
	ox, oy := img.Rect.Min.X, img.Rect.Min.Y
	// 预乘 alpha (image.RGBA 的表示约定)
	cr, cg, cb := premult(c.R, ca), premult(c.G, ca), premult(c.B, ca)
	if ca == 255 {
		for y := y0; y < y1; y++ {
			row := pix[(y-oy)*stride:]
			for x := x0; x < x1; x++ {
				i := (x - ox) * 4
				row[i], row[i+1], row[i+2], row[i+3] = cr, cg, cb, 255
			}
		}
		return
	}
	// 半透明: out = src + dst*(1-a)。dst 已是预乘表示, 直接相加即可,
	// 无需再除一次 alpha (那是非预乘表示才需要的步骤)。
	inv := 255 - int(ca)
	for y := y0; y < y1; y++ {
		row := pix[(y-oy)*stride:]
		for x := x0; x < x1; x++ {
			i := (x - ox) * 4
			row[i] = blendChannel(cr, row[i], inv)
			row[i+1] = blendChannel(cg, row[i+1], inv)
			row[i+2] = blendChannel(cb, row[i+2], inv)
			row[i+3] = clamp8(int(ca) + int(row[i+3])*inv/255)
		}
	}
}

// blendChannel 是预乘色 src-over 合成的单通道运算: src + dst*(1-a)。
// inv = 255-a, 由调用方在循环外算好。
func blendChannel(src, dst uint8, inv int) uint8 {
	return clamp8(int(src) + int(dst)*inv/255)
}

// StrokeRect 画 1px 边框 (四条边的实心矩形)。
func StrokeRect(img *image.RGBA, r Rect, c color.RGBA) {
	FillRect(img, Rect{r.X, r.Y, r.W, 1}, c)
	FillRect(img, Rect{r.X, r.Y + r.H - 1, r.W, 1}, c)
	FillRect(img, Rect{r.X, r.Y, 1, r.H}, c)
	FillRect(img, Rect{r.X + r.W - 1, r.Y, 1, r.H}, c)
}

// ===== P0 绘制原语: 圆 / 粗线 =====

// FillCircle 以 (cx,cy) 为圆心、r 为半径填充实心圆 (逐行扫描 x²+y²≤r²,
// 不做抗锯齿)。坐标按像素中心对齐, 圆心与半径都是整数像素。
func FillCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	if r <= 0 {
		return
	}
	rr := r * r
	for dy := -r; dy <= r; dy++ {
		// 该行半宽 = floor(sqrt(r²-dy²))
		dx := isqrt(rr - dy*dy)
		if dx < 0 {
			continue
		}
		FillRect(img, Rect{X: cx - dx, Y: cy + dy, W: 2*dx + 1, H: 1}, c)
	}
}

// StrokeCircle 画 1px 圆环 (外半径 r 与内半径 r-1 之间的环带)。
func StrokeCircle(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	if r <= 0 {
		return
	}
	inner := r - 1
	rr, ir := r*r, inner*inner
	for dy := -r; dy <= r; dy++ {
		dx := isqrt(rr - dy*dy)
		if dx < 0 {
			continue
		}
		ix := -1
		if inner > 0 {
			if v := ir - dy*dy; v >= 0 {
				ix = isqrt(v)
			}
		}
		if ix < 0 {
			FillRect(img, Rect{X: cx - dx, Y: cy + dy, W: 2*dx + 1, H: 1}, c)
			continue
		}
		// 环带: 左端到右端挖掉中间 [cx-ix, cx+ix]
		FillRect(img, Rect{X: cx - dx, Y: cy + dy, W: dx - ix, H: 1}, c)
		FillRect(img, Rect{X: cx + ix + 1, Y: cy + dy, W: dx - ix, H: 1}, c)
	}
}

// fillLine 画任意斜率的粗线: 主轴步进 + 每次落一个 thickness² 的实心方块。
// 用于勾/箭头等"近似即可"的装饰线 (不追求抗锯齿与端点形状)。
func fillLine(img *image.RGBA, x0, y0, x1, y1, thickness int, c color.RGBA) {
	if thickness < 1 {
		thickness = 1
	}
	dx, dy := x1-x0, y1-y0
	steps := abs(dx)
	if abs(dy) > steps {
		steps = abs(dy)
	}
	if steps == 0 {
		FillRect(img, Rect{X: x0 - thickness/2, Y: y0 - thickness/2, W: thickness, H: thickness}, c)
		return
	}
	for i := 0; i <= steps; i++ {
		x := x0 + dx*i/steps
		y := y0 + dy*i/steps
		FillRect(img, Rect{X: x - thickness/2, Y: y - thickness/2, W: thickness, H: thickness}, c)
	}
}

// StrokeDashedRect 画 1px 虚线矩形 (焦点框): 每条边按 dash 段实、gap 段空
// 交替。用"逐段 FillRect"而不是逐像素, 长边上省掉大量重复裁剪计算。
func StrokeDashedRect(img *image.RGBA, r Rect, c color.RGBA, dash, gap int) {
	if r.W <= 0 || r.H <= 0 || dash <= 0 {
		return
	}
	if gap < 0 {
		gap = 0
	}
	period := dash + gap
	// 横向边: 从 x 起长 w, 每 period 画 dash 宽的一段
	horiz := func(x, y, w int) {
		for dx := 0; dx < w; dx += period {
			n := min(dash, w-dx)
			FillRect(img, Rect{X: x + dx, Y: y, W: n, H: 1}, c)
		}
	}
	vert := func(x, y, h int) {
		for dy := 0; dy < h; dy += period {
			n := min(dash, h-dy)
			FillRect(img, Rect{X: x, Y: y + dy, W: 1, H: n}, c)
		}
	}
	horiz(r.X, r.Y, r.W)
	horiz(r.X, r.Y+r.H-1, r.W)
	vert(r.X, r.Y, r.H)
	vert(r.X+r.W-1, r.Y, r.H)
}

// isqrt 整数平方根 (向下取整); 入参为负时返回 -1 (表示该行无像素)。
func isqrt(v int) int {
	if v < 0 {
		return -1
	}
	x := 0
	for (x+1)*(x+1) <= v {
		x++
	}
	return x
}

// pct 取尺寸的百分比 (四舍五入), 用于把装饰图形按盒子比例定位。
func pct(v, p int) int {
	return (v*p + 50) / 100
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// premult 按 alpha 预乘一个通道 (v * a / 255)。
func premult(v uint8, a uint32) uint8 {
	return uint8(uint32(v) * a / 255)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
