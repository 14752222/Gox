package gfx

import (
	"image"
	"image/color"
	"math"
	"strings"

	"github.com/14752222/Gox/object"
)

// 装饰绘制 (§四 绘制缺口第一批, 2026-09-19): 圆角 / 渐变 / 阴影 / 边框宽度。
//
// 词汇 (通用盒子分支 + button 生效, 其余内置组件 v1 不动):
//
//	radius={8}                          圆角 (0 = 直角, 走原有 FillRect 快路径)
//	background="linear-gradient(to right, #a, #b)"
//	                                    线性渐变 (方向 to right/left/top/bottom,
//	                                    缺省 to bottom; ≥2 个色标均匀分布)
//	shadow={{x:0, y:2, blur:6, color:"#00000055"}}
//	                                    投影 (先画阴影再画面, 阴影跟随圆角)
//	borderWidth={2} borderWidth + borderStyle="dashed"
//	                                    边框宽度 (缺省 1) 与样式 (solid/dashed;
//	                                    dashed 不跟随圆角)
//
// 实现口径: 半像素覆盖抗锯齿 (像素中心到圆角矩形的有符号距离); 纯色无圆角
// 时逐字节等价于原路径, 既有用例零回归。渐变面不参与 button 的悬停提亮
// (interactiveFace 只对纯色面定义), disabled 的降饱和对两者都生效。

// maxGradientStops 限制色标数: 渐变按行/列插值, 色标再多也分辨不出来,
// 拦一个上限防脚本拼错出超长解析。
const maxGradientStops = 8

// gradPaint 是一次渐变填充: 色标沿主方向均匀分布。
type gradPaint struct {
	horizontal bool // false = 纵向 (to bottom/top)
	reverse    bool // to left / to top: 插值方向取反
	stops      []color.RGBA
}

// backgroundPaint 解析 background prop: 纯色 (原语义) 或线性渐变。
// 返回 (纯色, 渐变, 是否有面): solid 与 grad 互斥。
func (n *GuiNode) backgroundPaint() (solid color.RGBA, grad *gradPaint, ok bool) {
	s, ok := n.PropStr("background")
	if !ok {
		if n.Tag == "button" {
			return colorBtnFace, nil, true
		}
		return color.RGBA{}, nil, false
	}
	if g, ok2 := parseLinearGradient(s); ok2 {
		return color.RGBA{}, g, true
	}
	if c, ok2 := ParseColor(s); ok2 {
		return c, nil, true
	}
	if n.Tag == "button" {
		return colorBtnFace, nil, true
	}
	return color.RGBA{}, nil, false
}

// parseLinearGradient 解析 "linear-gradient(<dir>?, <color>+, ...)"。
// 方向可省 (缺省 to bottom); 色标 <2 个或任一解析失败 → 不是渐变 (调用方
// 回落纯色路径, 与字段级容错口径一致)。
func parseLinearGradient(s string) (*gradPaint, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "linear-gradient(") || !strings.HasSuffix(s, ")") {
		return nil, false
	}
	inner := s[len("linear-gradient(") : len(s)-1]
	parts := strings.Split(inner, ",")
	if len(parts) < 2 {
		return nil, false
	}
	g := &gradPaint{}
	rest := parts
	if d := strings.TrimSpace(parts[0]); strings.HasPrefix(d, "to ") {
		switch d {
		case "to right":
			g.horizontal = true
		case "to left":
			g.horizontal, g.reverse = true, true
		case "to top":
			g.reverse = true
		case "to bottom":
		default:
			return nil, false
		}
		rest = parts[1:]
	}
	if len(rest) < 2 || len(rest) > maxGradientStops {
		return nil, false
	}
	for _, p := range rest {
		c, ok := ParseColor(strings.TrimSpace(p))
		if !ok {
			return nil, false
		}
		g.stops = append(g.stops, c)
	}
	return g, true
}

// lerpStops 在 [0,1] 位置插值出色标颜色 (均匀分布, 端点对齐首末色标)。
func lerpStops(stops []color.RGBA, t float64) color.RGBA {
	if t <= 0 {
		return stops[0]
	}
	if t >= 1 {
		return stops[len(stops)-1]
	}
	pos := t * float64(len(stops)-1)
	i := int(pos)
	f := (pos - float64(i)) * 256
	a, b := stops[i], stops[i+1]
	mix := func(av, bv uint8) uint8 {
		return uint8(int(av) + (int(bv)-int(av))*int(f)/256)
	}
	return color.RGBA{R: mix(a.R, b.R), G: mix(a.G, b.G), B: mix(a.B, b.B), A: mix(a.A, b.A)}
}

// roundCov 计算像素中心 (px,py) 对圆角矩形 r 的覆盖率 [0,1]。
// 精确盒形 SDF: q = |p-中心| - (半轴-半径); d = len(max(q,0)) +
// min(max(qx,qy),0) - rad; coverage = 0.5 - d。
// (rad=0 时内部距离随离边远近增大 → 覆盖为 1, 不能用"到圆角的钳位距离"
// 简化式 —— 那个式子在直角时把整个内部都算成 0.5。)
func roundCov(px, py float64, r Rect, rad int) float64 {
	cx := float64(r.X) + float64(r.W)/2
	cy := float64(r.Y) + float64(r.H)/2
	qx := math.Abs(px-cx) - (float64(r.W)/2 - float64(rad))
	qy := math.Abs(py-cy) - (float64(r.H)/2 - float64(rad))
	ax, ay := qx, qy
	if ax < 0 {
		ax = 0
	}
	if ay < 0 {
		ay = 0
	}
	d := sqrtFloat(ax*ax + ay*ay)
	m := qx
	if qy > m {
		m = qy
	}
	if m < 0 {
		d += m // 深度项: 内部点离最近边的距离为负
	}
	d -= float64(rad)
	cov := 0.5 - d
	if cov < 0 {
		return 0
	}
	if cov > 1 {
		return 1
	}
	return cov
}

// sqrtFloat 集中 sqrt 依赖 (热路径审计点)。
func sqrtFloat(v float64) float64 { return math.Sqrt(v) }

// blendPixel 以覆盖率 cov 把 c 合成到 (x,y) (预乘 src-over, 与 FillRect 同
// 口径, 含 applyFade)。cov<=0 不落笔; 全覆盖不透明色走直写。
func blendPixel(img *image.RGBA, x, y int, c color.RGBA, cov float64) {
	c = applyFade(c)
	if c.A == 0 || cov <= 0 {
		return
	}
	stride := img.Stride
	i := (y-img.Rect.Min.Y)*stride + (x-img.Rect.Min.X)*4
	pix := img.Pix
	if cov >= 1 && c.A == 255 {
		pix[i], pix[i+1], pix[i+2], pix[i+3] = c.R, c.G, c.B, 255
		return
	}
	a := uint32(float64(c.A) * cov)
	if a == 0 {
		return
	}
	inv := 255 - int(a)
	pix[i] = blendChannel(premult(c.R, a), pix[i], inv)
	pix[i+1] = blendChannel(premult(c.G, a), pix[i+1], inv)
	pix[i+2] = blendChannel(premult(c.B, a), pix[i+2], inv)
	pix[i+3] = clamp8(int(a) + int(pix[i+3])*inv/255)
}

// fillRoundRect 圆角填充: 中段行列直写, 只有四个角块逐像素抗锯齿。
// rad==0 时调用方应走 FillRect (这条函数不处理该快路径)。
func fillRoundRect(img *image.RGBA, r Rect, rad int, c color.RGBA) {
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	if rad2 := min(min(r.W, r.H)/2, rad); rad2 > 0 {
		rad = rad2
	}
	// 中段 (上下角带之间) 整行直写
	midY0, midY1 := max(y0, r.Y+rad), min(y1, r.Y+r.H-rad)
	if midY0 < midY1 {
		FillRect(img, Rect{r.X, midY0, r.W, midY1 - midY0}, c)
	} else {
		midY0, midY1 = y0, y0 // 高度不足 2*rad: 全部走逐像素
	}
	// 上下角带: 逐像素 (覆盖率的抗锯齿只发生在圆角上)
	for y := y0; y < y1; y++ {
		if y >= midY0 && y < midY1 {
			continue
		}
		py := float64(y) + 0.5
		for x := x0; x < x1; x++ {
			blendPixel(img, x, y, c, roundCov(float64(x)+0.5, py, r, rad))
		}
	}
}

// fillRoundGradient 圆角渐变填充: 逐像素, 颜色按轴向位置插值。
func fillRoundGradient(img *image.RGBA, r Rect, rad int, g *gradPaint) {
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	if rad2 := min(min(r.W, r.H)/2, rad); rad2 > 0 {
		rad = rad2
	}
	span := float64(r.W - 1)
	if !g.horizontal {
		span = float64(r.H - 1)
	}
	if span <= 0 {
		span = 1
	}
	for y := y0; y < y1; y++ {
		py := float64(y) + 0.5
		for x := x0; x < x1; x++ {
			px := float64(x) + 0.5
			t := (px - float64(r.X)) / span
			if !g.horizontal {
				t = (py - float64(r.Y)) / span
			}
			if g.reverse {
				t = 1 - t
			}
			blendPixel(img, x, y, lerpStops(g.stops, t), roundCov(px, py, r, rad))
		}
	}
}

// strokeRoundRectW 圆角描边 (宽度 w): 外圆角覆盖减内圆角覆盖得到环带。
func strokeRoundRectW(img *image.RGBA, r Rect, rad, w int, c color.RGBA) {
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible || w <= 0 {
		return
	}
	if rad2 := min(min(r.W, r.H)/2, rad); rad2 > 0 {
		rad = rad2
	}
	inner := Rect{X: r.X + w, Y: r.Y + w, W: r.W - 2*w, H: r.H - 2*w}
	if inner.W <= 0 || inner.H <= 0 {
		fillRoundRect(img, r, rad, c)
		return
	}
	innerRad := rad - w
	if innerRad < 0 {
		innerRad = 0
	}
	for y := y0; y < y1; y++ {
		py := float64(y) + 0.5
		for x := x0; x < x1; x++ {
			px := float64(x) + 0.5
			cov := roundCov(px, py, r, rad) - roundCov(px, py, inner, innerRad)
			if cov > 0 {
				blendPixel(img, x, y, c, cov)
			}
		}
	}
}

// strokeRectW 直角描边 (任意宽度, 四条实心矩形)。
func strokeRectW(img *image.RGBA, r Rect, w int, c color.RGBA) {
	if w <= 0 {
		return
	}
	FillRect(img, Rect{r.X, r.Y, r.W, w}, c)
	FillRect(img, Rect{r.X, r.Y + r.H - w, r.W, w}, c)
	FillRect(img, Rect{r.X, r.Y, w, r.H}, c)
	FillRect(img, Rect{r.X + r.W - w, r.Y, w, r.H}, c)
}

// strokeDashedRectW 虚线描边 (任意宽度): 边沿主方向按 dash/gap 交替,
// 角上不保证相位连续 (与 1px 版 StrokeDashedRect 同款近似)。
func strokeDashedRectW(img *image.RGBA, r Rect, w, dash, gap int, c color.RGBA) {
	if w <= 0 {
		return
	}
	dashSeg(img, Rect{r.X, r.Y, r.W, w}, true, dash, gap, c)
	dashSeg(img, Rect{r.X, r.Y + r.H - w, r.W, w}, true, dash, gap, c)
	dashSeg(img, Rect{r.X, r.Y, w, r.H}, false, dash, gap, c)
	dashSeg(img, Rect{r.X + r.W - w, r.Y, w, r.H}, false, dash, gap, c)
}

// dashSeg 沿一条边按 dash/gap 画交替段。
func dashSeg(img *image.RGBA, band Rect, horizontal bool, dash, gap int, c color.RGBA) {
	if dash <= 0 {
		return
	}
	if gap < 1 {
		gap = 1
	}
	period := dash + gap
	n := 0
	if band.W <= 0 || band.H <= 0 {
		return
	}
	if horizontal {
		for x := band.X; x < band.X+band.W; x += period {
			w := min(dash, band.X+band.W-x)
			FillRect(img, Rect{x, band.Y, w, band.H}, c)
			n++
		}
	} else {
		for y := band.Y; y < band.Y+band.H; y += period {
			h := min(dash, band.Y+band.H-y)
			FillRect(img, Rect{band.X, y, band.W, h}, c)
			n++
		}
	}
	_ = n
}

// ===== 阴影 =====

// shadowSpec 是解析后的 shadow prop。
type shadowSpec struct {
	x, y, blur int
	c          color.RGBA
}

// shadowProps 解析 shadow={{x, y, blur, color}} (全部可省: x/y=0, blur=0,
// color=25% 黑)。非对象/坏字段静默忽略 (不画阴影)。
func (n *GuiNode) shadowSpec() (shadowSpec, bool) {
	v, ok := n.Props["shadow"]
	if !ok {
		return shadowSpec{}, false
	}
	o, ok := v.(*object.Object)
	if !ok {
		return shadowSpec{}, false
	}
	sh := shadowSpec{c: color.RGBA{A: 64}}
	if f, ok := o.GetProperty("x"); ok {
		if num, ok := f.(*object.Number); ok {
			sh.x = int(num.Value)
		}
	}
	if f, ok := o.GetProperty("y"); ok {
		if num, ok := f.(*object.Number); ok {
			sh.y = int(num.Value)
		}
	}
	if f, ok := o.GetProperty("blur"); ok {
		if num, ok := f.(*object.Number); ok {
			sh.blur = clampInt(int(num.Value), 0, 24)
		}
	}
	if f, ok := o.GetProperty("color"); ok {
		if s, ok := f.(*object.String); ok {
			if c, ok := ParseColor(s.Value); ok {
				sh.c = c
			}
		}
	}
	return sh, true
}

// shadowExtent 返回阴影超出盒子的外扩量 (脏矩形补偿用): 偏移 + 模糊半径。
func (n *GuiNode) shadowExtent() int {
	sh, ok := n.shadowSpec()
	if !ok {
		return 0
	}
	e := sh.blur
	if sh.x > 0 {
		e += sh.x
	}
	if sh.y > 0 {
		e += sh.y
	}
	return e
}

// clampInt 是装饰层的整数钳位 (blur 等滥用防护)。
func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// shadowMask 是阴影模糊的可复用 alpha 掩码 (按需增长, 不做每帧分配)。
var shadowMask []uint8

// paintShadow 画投影: 圆角矩形轮廓偏移 (x,y), blur>0 时做两轮盒式模糊
// (近似高斯)。先画阴影再画面 —— 调用方保证顺序。
func paintShadow(img *image.RGBA, box Rect, rad int, sh shadowSpec) {
	m := sh.blur
	r := Rect{X: box.X + sh.x - m, Y: box.Y + sh.y - m, W: box.W + 2*m, H: box.H + 2*m}
	x0, y0, x1, y1, visible := clip(img, r)
	if !visible {
		return
	}
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 {
		return
	}
	if rad2 := min(min(box.W, box.H)/2, rad); rad2 > 0 {
		rad = rad2
	}
	need := w * h
	if cap(shadowMask) < need {
		shadowMask = make([]uint8, need)
	}
	mask := shadowMask[:need]
	for i := range mask {
		mask[i] = 0
	}
	// 轮廓写入掩码 (相对掩码原点 x0/y0)
	sil := Rect{X: box.X + sh.x, Y: box.Y + sh.y, W: box.W, H: box.H}
	for y := max(y0, sil.Y); y < min(y1, sil.Y+sil.H); y++ {
		py := float64(y) + 0.5
		for x := max(x0, sil.X); x < min(x1, sil.X+sil.W); x++ {
			mask[(y-y0)*w+(x-x0)] = uint8(roundCov(float64(x)+0.5, py, sil, rad) * 255)
		}
	}
	if sh.blur > 0 {
		boxBlurAlpha(mask, w, h, sh.blur)
		boxBlurAlpha(mask, w, h, sh.blur)
	}
	// 合成
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			a := mask[(y-y0)*w+(x-x0)]
			if a == 0 {
				continue
			}
			blendPixel(img, x, y, sh.c, float64(a)/255)
		}
	}
}

// boxBlurAlpha 对 alpha 掩码做一次半径 b 的盒式模糊 (水平+垂直两趟,
// 滑窗和)。b=0 原样返回。
func boxBlurAlpha(mask []uint8, w, h, b int) {
	if b <= 0 || w <= 0 || h <= 0 {
		return
	}
	tmp := make([]uint8, len(mask))
	win := 2*b + 1
	// 水平
	for y := 0; y < h; y++ {
		row := y * w
		sum := 0
		for x := -b; x <= b; x++ {
			sum += int(mask[row+clampInt(x, 0, w-1)])
		}
		for x := 0; x < w; x++ {
			tmp[row+x] = uint8(sum / win)
			sum += int(mask[row+clampInt(x+b+1, 0, w-1)]) - int(mask[row+clampInt(x-b, 0, w-1)])
		}
	}
	// 垂直
	for x := 0; x < w; x++ {
		sum := 0
		for y := -b; y <= b; y++ {
			sum += int(tmp[clampInt(y, 0, h-1)*w+x])
		}
		for y := 0; y < h; y++ {
			mask[y*w+x] = uint8(sum / win)
			sum += int(tmp[clampInt(y+b+1, 0, h-1)*w+x]) - int(tmp[clampInt(y-b, 0, h-1)*w+x])
		}
	}
}

// paintBoxDecor 是通用盒子分支的统一装饰出口 (shadow → background → border)。
func paintBoxDecor(img *image.RGBA, n *GuiNode, disabled bool) {
	rad, _ := n.PropNum("radius")
	radius := int(rad)
	if radius < 0 {
		radius = 0
	}
	if sh, ok := n.shadowSpec(); ok {
		paintShadow(img, n.Box, radius, sh)
	}
	if solid, grad, ok := n.backgroundPaint(); ok {
		switch {
		case grad != nil && radius > 0:
			fillRoundGradient(img, n.Box, radius, tintGrad(grad, disabled))
		case grad != nil:
			fillRoundGradient(img, n.Box, 0, tintGrad(grad, disabled))
		case radius > 0:
			fillRoundRect(img, n.Box, radius, tint(n.interactiveFace(solid), disabled))
		default:
			// 快路径: 与原实现逐字节等价 (既有像素断言的回归保障)
			FillRect(img, n.Box, tint(n.interactiveFace(solid), disabled))
		}
	}
	if bd, ok := n.borderFor(); ok {
		bd = tint(bd, disabled)
		w, _ := n.PropNum("borderWidth")
		bw := int(w)
		if bw <= 0 {
			bw = 1
		}
		style, _ := n.PropStr("borderStyle")
		switch {
		case style == "dashed":
			strokeDashedRectW(img, n.Box, bw, 2*bw, 2*bw, bd)
		case radius > 0:
			strokeRoundRectW(img, n.Box, radius, bw, bd)
		default:
			if bw == 1 {
				StrokeRect(img, n.Box, bd) // 快路径: 与原实现一致
			} else {
				strokeRectW(img, n.Box, bw, bd)
			}
		}
	}
}

// tintGrad 对渐变整体施加 disabled 降饱和 (不做悬停提亮: interactiveFace
// 只定义在纯色面上)。
func tintGrad(g *gradPaint, disabled bool) *gradPaint {
	if !disabled {
		return g
	}
	out := &gradPaint{horizontal: g.horizontal, stops: make([]color.RGBA, len(g.stops))}
	for i, c := range g.stops {
		out.stops[i] = tint(c, true)
	}
	return out
}
