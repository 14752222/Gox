package gfx

import (
	"image"
	"image/color"
	"math"

	"github.com/14752222/Gox/object"
)

// ===== 路径 / 弧 / 变换 (rIowkb) =====
//
// 背景: ctx 此前只有 7 个原语 (fillRect / strokeRect / fillCircle /
// strokeCircle / line / drawText / clear), 饼图 · 环形图 · 面积图一律做不了
// —— apps 里的系统资源监视器与记账本被卡在这里, 只能退成横向条形图。
//
// 这里补两层:
//
//  1. **即时绘制原语** (与既有原语同风格, 一次调用一次落笔):
//     fillArc / strokeArc / fillRing。饼图 = fillArc, 环形图 = fillRing。
//  2. **路径 + 变换** (贴近 HTML canvas 的子集):
//     beginPath / moveTo / lineTo / closePath / arc / fill / stroke,
//     save / restore / translate / scale / rotate / setTransform。
//
// 角度一律用**弧度**, 0 指向 +x 轴, 正向为顺时针 (屏幕坐标 y 朝下, 于是
// "顺时针"与肉眼看到的一致) —— 与 HTML canvas 的 arc 约定相同, 便于把
// 现成的图表代码搬过来。
//
// 像素级取整不抗锯齿: 与既有原语 (FillCircle / fillLine) 保持一致, 整条
// 渲染链路都是硬边, 单独给路径做抗锯齿反而格格不入。

// ===== 仿射变换 =====

// canvasXform 是 2D 仿射变换 [[a c e],[b d f]]:
//
//	x' = a*x + c*y + e
//	y' = b*x + d*y + f
//
// 与 HTML canvas 的 setTransform(a,b,c,d,e,f) 参数序一致。
type canvasXform struct{ a, b, c, d, e, f float64 }

// identityXform 返回单位变换。
func identityXform() canvasXform { return canvasXform{a: 1, d: 1} }

// apply 变换一个点。
func (m canvasXform) apply(x, y float64) (float64, float64) {
	return m.a*x + m.c*y + m.e, m.b*x + m.d*y + m.f
}

// then 返回「先应用 n 再应用 m」的复合变换 (与 canvas 的矩阵乘法序一致:
// translate 后再 rotate, 等价于 m = translate ∘ rotate)。
func (m canvasXform) then(n canvasXform) canvasXform {
	return canvasXform{
		a: m.a*n.a + m.c*n.b,
		b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d,
		d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e,
		f: m.b*n.e + m.d*n.f + m.f,
	}
}

// scaleFactor 返回变换对长度的**各向同性**缩放估计 (sqrt(|det|))。
// 半径 / 线宽这类"标量尺寸"用它换算 —— 非等比变换下圆会失真, 这是有意的
// 简化 (真 canvas 会把圆变成椭圆, v1 不追求)。
func (m canvasXform) scaleFactor() float64 {
	det := m.a*m.d - m.b*m.c
	if det < 0 {
		det = -det
	}
	return math.Sqrt(det)
}

// ===== 路径 =====

// canvasSubPath 是一条子路径: 一串已变换到**画布坐标**的点 + 是否闭合。
// 点在加入时就按当时的变换算好 (与 HTML canvas 一致: 变换影响的是路径构造
// 时的那一步, 不是 fill 那一刻)。
type canvasSubPath struct {
	pts    []canvasPt
	closed bool
}

// canvasPt 是画布坐标下的一个路径点 (保留浮点, 光栅化时才取整)。
type canvasPt struct{ x, y float64 }

// canvasPath 是当前路径: 若干条子路径。
type canvasPath struct {
	subs []canvasSubPath
}

// reset 清空路径 (beginPath)。
func (p *canvasPath) reset() { p.subs = nil }

// cur 返回当前子路径 (没有就新开一条)。
func (p *canvasPath) cur() *canvasSubPath {
	if len(p.subs) == 0 {
		p.subs = append(p.subs, canvasSubPath{})
	}
	return &p.subs[len(p.subs)-1]
}

// moveTo 开始一条新子路径。
func (p *canvasPath) moveTo(pt canvasPt) {
	p.subs = append(p.subs, canvasSubPath{pts: []canvasPt{pt}})
}

// lineTo 向当前子路径追加一个点。
func (p *canvasPath) lineTo(pt canvasPt) {
	s := p.cur()
	s.pts = append(s.pts, pt)
}

// arc 把一段圆弧采样成折线追加到路径。
//
// 起点与当前点不同时先补一条直线 (HTML canvas 的 arc 语义: 从当前点连到
// 弧的起点), 于是 `moveTo(cx,cy); arc(...); closePath()` 正是扇形。
func (p *canvasPath) arc(cx, cy, r, start, end float64, counterclockwise bool) {
	if r <= 0 {
		return
	}
	sweep := end - start
	if counterclockwise {
		// 逆向: 把增量折成负向的最短弧。
		for sweep > 0 {
			sweep -= 2 * math.Pi
		}
	} else {
		for sweep < 0 {
			sweep += 2 * math.Pi
		}
	}
	// 采样密度: 每约 2px 一段, 上限 720 段 (整圆时约 0.5°/段)。
	n := int(math.Abs(sweep) * r / 2)
	if n < 8 {
		n = 8
	}
	if n > 720 {
		n = 720
	}
	first := canvasPt{x: cx + r*math.Cos(start), y: cy + r*math.Sin(start)}
	s := p.cur()
	if len(s.pts) == 0 {
		s.pts = append(s.pts, first)
	} else {
		last := s.pts[len(s.pts)-1]
		if math.Abs(last.x-first.x) > 0.01 || math.Abs(last.y-first.y) > 0.01 {
			s.pts = append(s.pts, first)
		}
	}
	for i := 1; i <= n; i++ {
		ang := start + sweep*float64(i)/float64(n)
		s.pts = append(s.pts, canvasPt{x: cx + r*math.Cos(ang), y: cy + r*math.Sin(ang)})
	}
}

// ===== 光栅化 =====

// fillPath 用扫描线 even-odd 规则填充路径的所有子路径。
//
// 逐行求交点是 O(rows × edges), 对画布尺寸 (几百 px) 足够; 不追求
// nonzero 规则 —— 甜甜圈靠 fillRing 直接画, 不靠两个反向子路径挖洞。
func fillPath(img *image.RGBA, p *canvasPath, c color.RGBA) {
	if img == nil || len(p.subs) == 0 {
		return
	}
	// 全部子路径的边收集到一起, 一起求交 (even-odd 跨子路径生效)。
	type edge struct{ x0, y0, x1, y1 float64 }
	var edges []edge
	minY, maxY := math.MaxFloat64, -math.MaxFloat64
	for _, s := range p.subs {
		n := len(s.pts)
		for i := 0; i < n; i++ {
			a := s.pts[i]
			b := s.pts[(i+1)%n]
			if i == n-1 && !s.closed {
				break // 未闭合: 最后一点不连回起点
			}
			if a.y == b.y {
				continue // 水平边不影响交点计数
			}
			edges = append(edges, edge{a.x, a.y, b.x, b.y})
			minY = math.Min(minY, math.Min(a.y, b.y))
			maxY = math.Max(maxY, math.Max(a.y, b.y))
		}
	}
	if len(edges) == 0 {
		return
	}
	y0 := int(math.Floor(minY))
	y1 := int(math.Ceil(maxY))
	for y := y0; y <= y1; y++ {
		yc := float64(y) + 0.5
		var xs []float64
		for _, e := range edges {
			lo, hi := e.y0, e.y1
			if lo > hi {
				lo, hi = hi, lo
			}
			// 半开区间 [lo, hi): 避免顶点被算两次
			if yc < lo || yc >= hi {
				continue
			}
			t := (yc - e.y0) / (e.y1 - e.y0)
			xs = append(xs, e.x0+t*(e.x1-e.x0))
		}
		if len(xs) < 2 {
			continue
		}
		// 插入排序: 交点数通常 < 20, 比 sort.Slice 的开销小。
		for i := 1; i < len(xs); i++ {
			for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
				xs[j], xs[j-1] = xs[j-1], xs[j]
			}
		}
		for i := 0; i+1 < len(xs); i += 2 {
			xa := int(math.Ceil(xs[i] - 0.5))
			xb := int(math.Ceil(xs[i+1] - 0.5))
			if xb <= xa {
				continue
			}
			FillRect(img, Rect{X: xa, Y: y, W: xb - xa, H: 1}, c)
		}
	}
}

// strokePath 沿路径的边落线 (线宽 w, 硬边)。
func strokePath(img *image.RGBA, p *canvasPath, w int, c color.RGBA) {
	if img == nil || len(p.subs) == 0 {
		return
	}
	if w < 1 {
		w = 1
	}
	for _, s := range p.subs {
		n := len(s.pts)
		last := n
		if !s.closed {
			last = n - 1
		}
		for i := 0; i < last; i++ {
			a := s.pts[i]
			b := s.pts[(i+1)%n]
			fillLine(img, int(math.Round(a.x)), int(math.Round(a.y)),
				int(math.Round(b.x)), int(math.Round(b.y)), w, c)
		}
	}
}

// FillArc 填充扇形 (饼图的每一片): 圆心 (cx,cy)、半径 r、从 start 到 end
// 的一段圆弧扫过的区域。角度为弧度, 正向顺时针。
func FillArc(img *image.RGBA, cx, cy, r int, start, end float64, c color.RGBA) {
	if img == nil || r <= 0 {
		return
	}
	var p canvasPath
	p.moveTo(canvasPt{x: float64(cx), y: float64(cy)})
	p.arc(float64(cx), float64(cy), float64(r), start, end, false)
	p.subs[len(p.subs)-1].closed = true
	fillPath(img, &p, c)
}

// StrokeArc 描一段圆弧 (1px 硬边, 线宽固定 —— 更粗的请用路径 + stroke)。
func StrokeArc(img *image.RGBA, cx, cy, r int, start, end float64, c color.RGBA) {
	if img == nil || r <= 0 {
		return
	}
	var p canvasPath
	p.arc(float64(cx), float64(cy), float64(r), start, end, false)
	strokePath(img, &p, 1, c)
}

// FillRing 填充环形扇区 (环形图 / 甜甜圈图的每一片): 外半径 rOuter 与内半径
// rInner 之间、从 start 到 end 扫过的环带。
//
// 走「按行判定」而不是「外扇形挖掉内扇形」: 后者在 rInner 很小的时候会在
// 圆心附近留下难看的锯齿, 而环形图恰恰常用很小的内半径。
func FillRing(img *image.RGBA, cx, cy, rOuter, rInner int, start, end float64, c color.RGBA) {
	if img == nil || rOuter <= 0 || rInner >= rOuter {
		return
	}
	if rInner < 0 {
		rInner = 0
	}
	// 归一化扫过角: 与 canvasPath.arc 同一套折算法, 保证两者画出来的
	// 扇形边界一致。
	sweep := end - start
	for sweep < 0 {
		sweep += 2 * math.Pi
	}
	for sweep > 2*math.Pi {
		sweep -= 2 * math.Pi
	}
	ro2, ri2 := float64(rOuter*rOuter), float64(rInner*rInner)
	for dy := -rOuter; dy <= rOuter; dy++ {
		for dx := -rOuter; dx <= rOuter; dx++ {
			d2 := float64(dx*dx + dy*dy)
			if d2 > ro2 || d2 < ri2 {
				continue
			}
			// 角度判定: atan2 的 0 在 +x 轴, 正向为 (y 朝下时) 顺时针,
			// 与 FillArc 的约定一致。整圈 (sweep >= 2π-ε) 跳过判定。
			if sweep < 2*math.Pi-1e-9 {
				ang := math.Atan2(float64(dy), float64(dx))
				rel := ang - start
				for rel < 0 {
					rel += 2 * math.Pi
				}
				if rel > sweep {
					continue
				}
			}
			FillRect(img, Rect{X: cx + dx, Y: cy + dy, W: 1, H: 1}, c)
		}
	}
}

// ===== ctx 状态与新增原语 =====

// canvasState 是 ctx 的可保存/恢复状态 (save / restore 的载体)。
type canvasState struct {
	xform canvasXform
	// 路径构造时的线宽 (stroke 用)。
	lineWidth int
}

// defaultCanvasState 返回初始状态: 单位变换 + 1px 线宽。
func defaultCanvasState() canvasState { return canvasState{xform: identityXform(), lineWidth: 1} }

// toCanvas 把画布局部坐标按当前变换换算成画布坐标 (浮点, 未加 origin)。
func (t *canvasTarget) toCanvas(x, y float64) canvasPt {
	px, py := t.state.xform.apply(x, y)
	return canvasPt{x: px, y: py}
}

// toScreen 把画布局部坐标换算成**屏幕**坐标 (加 Box 偏移)。
func (t *canvasTarget) toScreen(x, y float64) canvasPt {
	p := t.toCanvas(x, y)
	return canvasPt{x: p.x + float64(t.box.X), y: p.y + float64(t.box.Y)}
}

// numf 读取第 i 个参数为浮点 (角度/变换参数用; 整数参数请走 canvasNum)。
func canvasNumF(args []object.Value, i int) float64 {
	if i >= len(args) {
		return 0
	}
	if n, ok := args[i].(*object.Number); ok {
		return n.Value
	}
	return float64(canvasNum(args, i))
}

// boolAt 读取第 i 个参数为布尔 (缺失/非布尔一律 false)。
func canvasBool(args []object.Value, i int) bool {
	if i >= len(args) {
		return false
	}
	if b, ok := args[i].(*object.Boolean); ok {
		return b.Value
	}
	return false
}

// addCanvasPathPrimitives 往 ctx 上挂路径 / 弧 / 变换 / 像素原语。
//
// 与既有原语一样, t.img == nil (依赖收集阶段) 时**只执行不落笔** —— 但
// 状态机 (当前路径 / 变换栈) 照样推进, 否则"依赖收集时报 undefined 方法、
// 真绘制时正常"这类只有一半路径能跑到的问题又会出现。
func addCanvasPathPrimitives(ctx *object.Object, t *canvasTarget) {
	// ---- 变换 ----

	ctx.SetProperty("save", object.NewBuiltin("save", func(args ...object.Value) object.Value {
		t.state.lineWidth = lineWidthOf(ctx, t.state.lineWidth)
		t.stack = append(t.stack, t.state)
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("restore", object.NewBuiltin("restore", func(args ...object.Value) object.Value {
		if len(t.stack) > 0 {
			t.state = t.stack[len(t.stack)-1]
			t.stack = t.stack[:len(t.stack)-1]
			ctx.SetProperty("lineWidth", object.NewNumber(float64(t.state.lineWidth)))
		}
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("translate", object.NewBuiltin("translate", func(args ...object.Value) object.Value {
		n := canvasXform{a: 1, d: 1, e: canvasNumF(args, 0), f: canvasNumF(args, 1)}
		t.state.xform = t.state.xform.then(n)
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("scale", object.NewBuiltin("scale", func(args ...object.Value) object.Value {
		n := canvasXform{a: canvasNumF(args, 0), d: canvasNumF(args, 1)}
		t.state.xform = t.state.xform.then(n)
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("rotate", object.NewBuiltin("rotate", func(args ...object.Value) object.Value {
		a := canvasNumF(args, 0)
		cos, sin := math.Cos(a), math.Sin(a)
		n := canvasXform{a: cos, b: sin, c: -sin, d: cos}
		t.state.xform = t.state.xform.then(n)
		return object.UndefinedSingleton
	}))

	// setTransform(a,b,c,d,e,f): 直接**替换**当前变换 (不是叠加), 与 HTML
	// canvas 同名方法一致。reset 用单位矩阵, 不碰 save/restore 栈。
	ctx.SetProperty("setTransform", object.NewBuiltin("setTransform", func(args ...object.Value) object.Value {
		t.state.xform = canvasXform{
			a: canvasNumF(args, 0), b: canvasNumF(args, 1), c: canvasNumF(args, 2),
			d: canvasNumF(args, 3), e: canvasNumF(args, 4), f: canvasNumF(args, 5),
		}
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("resetTransform", object.NewBuiltin("resetTransform", func(args ...object.Value) object.Value {
		t.state.xform = identityXform()
		return object.UndefinedSingleton
	}))

	// ---- 路径 ----

	ctx.SetProperty("beginPath", object.NewBuiltin("beginPath", func(args ...object.Value) object.Value {
		t.path.reset()
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("moveTo", object.NewBuiltin("moveTo", func(args ...object.Value) object.Value {
		t.path.moveTo(t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("lineTo", object.NewBuiltin("lineTo", func(args ...object.Value) object.Value {
		t.path.lineTo(t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("closePath", object.NewBuiltin("closePath", func(args ...object.Value) object.Value {
		if len(t.path.subs) > 0 {
			t.path.subs[len(t.path.subs)-1].closed = true
		}
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("arc", object.NewBuiltin("arc", func(args ...object.Value) object.Value {
		// 半径是"标量尺寸": 按当前变换的等比缩放换算 (见 scaleFactor)。
		r := canvasNumF(args, 2) * t.state.xform.scaleFactor()
		c := t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1))
		t.path.arc(c.x, c.y, r, canvasNumF(args, 3), canvasNumF(args, 4), canvasBool(args, 5))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("fill", object.NewBuiltin("fill", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		fillPath(t.img, &t.path, t.ink(canvasColor(args, 0)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("stroke", object.NewBuiltin("stroke", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		w := lineWidthOf(ctx, t.state.lineWidth)
		if w < 1 {
			w = 1
		}
		strokePath(t.img, &t.path, w, t.ink(canvasColor(args, 0)))
		return object.UndefinedSingleton
	}))

	// lineWidth 以**普通属性**暴露: `ctx.lineWidth = 3` 随手写、随手读。
	//
	// 这里**刻意不用访问器**: 访问器的 getter 要走 object.CallFunction, 而回调
	// 桥在没有 VM 时静默返回 undefined (见 canvas.go 的 callScriptFn 说明) ——
	// 于是纯 Go 嵌入 (无 VM) 读 ctx.lineWidth 会拿到 undefined。改成普通属性
	// 后读写都只碰 Properties, 两个阶段行为一致; 线宽的真值来源就是这个属性
	// (见 lineWidthOf), save/restore 也按它存取。
	ctx.SetProperty("lineWidth", object.NewNumber(float64(t.state.lineWidth)))

	// ---- 即时弧原语 (饼图 / 环形图一步到位) ----

	ctx.SetProperty("fillArc", object.NewBuiltin("fillArc", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		p := t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1))
		r := canvasNumF(args, 2) * t.state.xform.scaleFactor()
		FillArc(t.img, int(math.Round(p.x)), int(math.Round(p.y)), int(math.Round(r)),
			canvasNumF(args, 3), canvasNumF(args, 4), t.ink(canvasColor(args, 5)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("strokeArc", object.NewBuiltin("strokeArc", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		p := t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1))
		r := canvasNumF(args, 2) * t.state.xform.scaleFactor()
		StrokeArc(t.img, int(math.Round(p.x)), int(math.Round(p.y)), int(math.Round(r)),
			canvasNumF(args, 3), canvasNumF(args, 4), t.ink(canvasColor(args, 5)))
		return object.UndefinedSingleton
	}))

	// fillRing(cx, cy, rOuter, rInner, start, end, color): 环形图的一片。
	ctx.SetProperty("fillRing", object.NewBuiltin("fillRing", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		p := t.toScreen(canvasNumF(args, 0), canvasNumF(args, 1))
		s := t.state.xform.scaleFactor()
		ro := int(math.Round(canvasNumF(args, 2) * s))
		ri := int(math.Round(canvasNumF(args, 3) * s))
		FillRing(t.img, int(math.Round(p.x)), int(math.Round(p.y)), ro, ri,
			canvasNumF(args, 4), canvasNumF(args, 5), t.ink(canvasColor(args, 6)))
		return object.UndefinedSingleton
	}))

	// ---- 像素读写 ----

	ctx.SetProperty("getImageData", object.NewBuiltin("getImageData", makeGetImageData(t)))
	ctx.SetProperty("putImageData", object.NewBuiltin("putImageData", makePutImageData(t)))
	ctx.SetProperty("createImageData", object.NewBuiltin("createImageData", makeCreateImageData(t)))
}

// makeGetImageData 返回 ctx.getImageData(x, y, w, h) 的实现: 读出画布内
// (x,y,w,h) 的像素, 返回一个 `{width, height, data}` 形态的 ImageData。
//
// data 是 **Uint8ClampedArray**, 每像素 4 字节 RGBA —— 与 HTML canvas 一致,
// 于是取色 / 逐像素校验 / 自研二维码比对这类代码可以直接照搬。
//
// 注意 Gox 的帧缓冲是**预乘 alpha** (image.RGBA 的表示约定), 而外部约定
// (以及 putImageData 的输入) 是非预乘: 读出时反预乘, 写回时再预乘。
// 不透明像素 (A=255) 两侧完全等价, 只有半透明的中间态需要换算。
func makeGetImageData(t *canvasTarget) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		w, h := canvasNum(args, 2), canvasNum(args, 3)
		if w <= 0 || h <= 0 {
			return nullImageData(0, 0)
		}
		x := t.box.X + canvasNum(args, 0)
		y := t.box.Y + canvasNum(args, 1)
		kind, _ := object.LookupTAKind("Uint8ClampedArray")
		data := object.NewTypedArray(kind, w*h*4)
		if t.img == nil {
			// 依赖收集阶段: 形状照给, 内容全 0 —— 脚本读 data.length /
			// 按索引取值的写法两阶段行为一致。
			return newImageData(w, h, data)
		}
		b := t.img.Bounds()
		ox, oy := t.img.Rect.Min.X, t.img.Rect.Min.Y
		for row := 0; row < h; row++ {
			sy := y + row
			if sy < b.Min.Y || sy >= b.Max.Y {
				continue
			}
			src := t.img.Pix[(sy-oy)*t.img.Stride:]
			for col := 0; col < w; col++ {
				sx := x + col
				if sx < b.Min.X || sx >= b.Max.X {
					continue
				}
				i := (sx - ox) * 4
				r, g, bb, a := src[i], src[i+1], src[i+2], src[i+3]
				if a > 0 && a < 255 {
					r, g, bb = unpremult(r, a), unpremult(g, a), unpremult(bb, a)
				}
				data.SetElement((row*w+col)*4+0, object.NewNumber(float64(r)))
				data.SetElement((row*w+col)*4+1, object.NewNumber(float64(g)))
				data.SetElement((row*w+col)*4+2, object.NewNumber(float64(bb)))
				data.SetElement((row*w+col)*4+3, object.NewNumber(float64(a)))
			}
		}
		return newImageData(w, h, data)
	}
}

// makePutImageData 返回 ctx.putImageData(imageData, x, y) 的实现: 把
// ImageData 的像素写回画布左上角 (x,y) 处。
func makePutImageData(t *canvasTarget) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		if t.img == nil || len(args) < 1 {
			return object.UndefinedSingleton
		}
		src, ok := args[0].(*object.Object)
		if !ok {
			return object.UndefinedSingleton
		}
		dv, _ := src.GetProperty("data")
		data, ok := dv.(*object.TypedArray)
		if !ok {
			return object.UndefinedSingleton
		}
		wv, _ := src.GetProperty("width")
		hv, _ := src.GetProperty("height")
		w := int(toNumValue(wv))
		h := int(toNumValue(hv))
		if w <= 0 || h <= 0 {
			return object.UndefinedSingleton
		}
		dx := t.box.X + canvasNum(args, 1)
		dy := t.box.Y + canvasNum(args, 2)
		b := t.img.Bounds()
		ox, oy := t.img.Rect.Min.X, t.img.Rect.Min.Y
		for row := 0; row < h; row++ {
			sy := dy + row
			if sy < b.Min.Y || sy >= b.Max.Y {
				continue
			}
			dst := t.img.Pix[(sy-oy)*t.img.Stride:]
			for col := 0; col < w; col++ {
				sx := dx + col
				if sx < b.Min.X || sx >= b.Max.X {
					continue
				}
				base := (row*w + col) * 4
				r := byte(toNumValue(data.GetElement(base + 0)))
				g := byte(toNumValue(data.GetElement(base + 1)))
				bb := byte(toNumValue(data.GetElement(base + 2)))
				a := byte(toNumValue(data.GetElement(base + 3)))
				if a == 0 {
					continue // 全透明: 不改动画面
				}
				i := (sx - ox) * 4
				if a == 255 {
					dst[i], dst[i+1], dst[i+2], dst[i+3] = r, g, bb, 255
					continue
				}
				dst[i] = premult(r, uint32(a))
				dst[i+1] = premult(g, uint32(a))
				dst[i+2] = premult(bb, uint32(a))
				dst[i+3] = a
			}
		}
		return object.UndefinedSingleton
	}
}

// makeCreateImageData 返回 ctx.createImageData(w, h) 的实现: 造一块全透明
// (全 0) 的 ImageData, 供脚本离屏算像素后再 putImageData。
func makeCreateImageData(t *canvasTarget) func(args ...object.Value) object.Value {
	return func(args ...object.Value) object.Value {
		w, h := canvasNum(args, 0), canvasNum(args, 1)
		if w <= 0 || h <= 0 {
			return nullImageData(0, 0)
		}
		if len(args) >= 3 {
			// createImageData(imageData) 形态: 沿用传入的尺寸。
			if src, ok := args[0].(*object.Object); ok {
				if wv, found := src.GetProperty("width"); found {
					w = int(toNumValue(wv))
				}
				if hv, found := src.GetProperty("height"); found {
					h = int(toNumValue(hv))
				}
			}
		}
		kind, _ := object.LookupTAKind("Uint8ClampedArray")
		return newImageData(w, h, object.NewTypedArray(kind, w*h*4))
	}
}

// nullImageData 返回 0 尺寸的 ImageData (越界 / 参数缺失时的兜底: 让脚本
// 拿到一个形状正确的空对象, 而不是 undefined —— 后者会让 data.length 直接炸)。
func nullImageData(w, h int) object.Value {
	kind, _ := object.LookupTAKind("Uint8ClampedArray")
	return newImageData(w, h, object.NewTypedArray(kind, 0))
}

// newImageData 组装 `{width, height, data}` 形态的 ImageData 对象。
func newImageData(w, h int, data *object.TypedArray) object.Value {
	o := object.NewObject()
	o.SetProperty("width", object.NewNumber(float64(w)))
	o.SetProperty("height", object.NewNumber(float64(h)))
	o.SetProperty("data", data)
	return o
}

// lineWidthOf 读 ctx.lineWidth 属性的当前值, 读不到 (非法值 / 未挂) 时回落
// 到 def。线宽的**真值来源**就是这个属性 —— 不另存一份, 免得两处漂开。
func lineWidthOf(ctx *object.Object, def int) int {
	if ctx == nil {
		return def
	}
	v, ok := ctx.GetProperty("lineWidth")
	if !ok {
		return def
	}
	n, ok := v.(*object.Number)
	if !ok || n.Value < 1 {
		return def
	}
	return int(n.Value)
}

// toNumValue 把 JS 值取成 float64 (TypedArray 的 GetIndex 返回 Number)。
func toNumValue(v object.Value) float64 {
	if v == nil {
		return 0
	}
	if n, ok := v.(*object.Number); ok {
		return n.Value
	}
	if b, ok := v.(*object.Boolean); ok {
		if b.Value {
			return 1
		}
		return 0
	}
	return 0
}

// unpremult 把预乘通道还原成非预乘 (getImageData 的读出侧)。
func unpremult(c uint8, a uint8) uint8 {
	v := int(c) * 255 / int(a)
	if v > 255 {
		return 255
	}
	return uint8(v)
}
