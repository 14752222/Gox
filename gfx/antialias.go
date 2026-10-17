package gfx

import (
	"image"
	"image/color"
	"math"
)

// ===== 抗锯齿光栅 (AA) =====
//
// 背景: 内核此前整条渲染链路都是**硬边** —— fillLine / FillCircle /
// StrokeCircle / fillPath / FillRing 一律像素级取整, 于是斜线、圆弧、环带
// 全是楼梯状锯齿 (深色底 + 高饱和细线上尤其扎眼)。而 decor.go 的圆角矩形
// 早就走覆盖率 (roundCov + blendPixel), 两边口径不一致。
//
// 这一层把上面五个原语统一换成**覆盖率 (coverage)** 合成:
//
//	coverage = clamp(形状半宽 + 0.5 - 到形状边界的距离, 0, 1)
//
// 即"像素中心离边界越远越实, 恰好压在边界上是半覆盖" —— 与 roundCov 同一
// 口径。合成一律走 blendPixel (预乘 src-over, 含子树不透明度 applyFade),
// 所以半透明色、淡出、遮罩的行为与硬边时期完全一致, 只有边缘多出过渡像素。
//
// 坐标约定 (与 FillRect / blendPixel 一致): 像素 (x,y) 覆盖连续区间
// [x,x+1]×[y,y+1], 中心在 (x+0.5, y+0.5)。fillLineAA / fillDiscAA 收到的
// 整数参数按"像素索引"解释, 内部 +0.5 落到像素中心 —— 于是**整数端点的
// 水平/垂直线仍然整行/整列实色**, AA 不会把 1px 分隔线糊成两条半亮的线
// (这是所有边框/分隔线/图标的观感底线, 也是本层最容易翻车的地方)。

// tau 是整圈弧度 (2π): 调用方表达"整圈"时用, 免得为此 import math。
const tau = 2 * math.Pi

// covClamp 把 "半宽 + 0.5 - 距离" 折成 [0,1] 的覆盖率 (热路径: 不做分支外
// 的任何分配)。
func covClamp(v float64) float64 {
	if v <= 0 {
		return 0
	}
	if v >= 1 {
		return 1
	}
	return v
}

// fillLineAA 以覆盖率抗锯齿画线段 (胶囊 SDF): 端点 (x0,y0)-(x1,y1) 按像素
// 索引解释 (端点像素包含在线内), thickness 是线宽 (胶囊直径)。
//
// 逐行先解析出"到线段距离 <= hw+1"的 x 区间, 再在区内逐像素算**精确距离**
// —— 工作量与线真正覆盖的像素数成正比, 而不是与包围盒面积成正比 (一条横
// 跨画布的斜线不会被拖成 O(w*h) 的全画布遍历)。
func fillLineAA(img *image.RGBA, x0, y0, x1, y1 float64, thickness int, c color.RGBA) {
	if img == nil {
		return
	}
	if thickness < 1 {
		thickness = 1
	}
	b := img.Bounds()
	hw := float64(thickness) / 2
	// 像素索引 → 连续坐标 (落到像素中心)
	x0 += 0.5
	y0 += 0.5
	x1 += 0.5
	y1 += 0.5
	dx, dy := x1-x0, y1-y0
	segLen := math.Hypot(dx, dy)
	if segLen == 0 {
		fillDiscAA(img, x0, y0, hw, c)
		return
	}
	// 沿线段方向与法向的单位向量: s = 沿线坐标, e = 垂直距离
	ux, uy := dx/segLen, dy/segLen
	nx, ny := -uy, ux
	margin := hw + 1 // 覆盖非零的最远距离 + 1px 余量
	yLo := int(math.Floor(math.Min(y0, y1) - margin))
	yHi := int(math.Ceil(math.Max(y0, y1) + margin))
	if yLo < b.Min.Y {
		yLo = b.Min.Y
	}
	if yHi > b.Max.Y {
		yHi = b.Max.Y
	}
	for y := yLo; y < yHi; y++ {
		py := float64(y) + 0.5
		cy := py - y0 // 本行相对起点的纵向偏移
		// 区间用 A = px - x0 表示: 圆帽 (两端) 与主体 (|e|<=margin 且
		// 0<=s<=segLen) 的并集 —— 胶囊是凸的, 并集必为一段连续区间。
		var lo, hi float64
		have := false
		if r2 := margin*margin - cy*cy; r2 > 0 {
			t := math.Sqrt(r2)
			lo, hi, have = -t, t, true
		}
		if r2 := margin*margin - (cy-dy)*(cy-dy); r2 > 0 {
			t := math.Sqrt(r2)
			if a, z := dx-t, dx+t; have {
				if a < lo {
					lo = a
				}
				if z > hi {
					hi = z
				}
			} else {
				lo, hi, have = a, z, true
			}
		}
		// 主体: e(A) = A*nx + cy*ny, s(A) = A*ux + cy*uy
		b0, b1 := math.Inf(-1), math.Inf(1)
		if math.Abs(nx) > 1e-12 {
			e0 := (-margin - cy*ny) / nx
			e1 := (margin - cy*ny) / nx
			if e0 > e1 {
				e0, e1 = e1, e0
			}
			b0, b1 = e0, e1
		} else if math.Abs(cy*ny) > margin {
			b0, b1 = 1, -1 // 整行都在带宽之外
		}
		if b0 <= b1 {
			if math.Abs(ux) > 1e-12 {
				s0 := (-cy * uy) / ux
				s1 := (segLen - cy*uy) / ux
				if s0 > s1 {
					s0, s1 = s1, s0
				}
				if s0 > b0 {
					b0 = s0
				}
				if s1 < b1 {
					b1 = s1
				}
			} else if cy*uy < 0 || cy*uy > segLen {
				b0, b1 = 1, -1
			}
		}
		if b0 <= b1 {
			if have {
				if b0 < lo {
					lo = b0
				}
				if b1 > hi {
					hi = b1
				}
			} else {
				lo, hi, have = b0, b1, true
			}
		}
		if !have {
			continue
		}
		xa := int(math.Floor(x0 + lo))
		xb := int(math.Ceil(x0 + hi))
		if xa < b.Min.X {
			xa = b.Min.X
		}
		if xb > b.Max.X {
			xb = b.Max.X
		}
		for x := xa; x < xb; x++ {
			ax := float64(x) + 0.5 - x0
			s := ax*ux + cy*uy
			var d float64
			switch {
			case s < 0: // 起点圆帽
				d = math.Hypot(ax, cy)
			case s > segLen: // 终点圆帽
				d = math.Hypot(ax-dx, cy-dy)
			default: // 主体: 垂直距离
				d = math.Abs(ax*nx + cy*ny)
			}
			if cov := covClamp(hw + 0.5 - d); cov > 0 {
				blendPixel(img, x, y, c, cov)
			}
		}
	}
}

// fillDiscAA 以覆盖率抗锯齿填充圆盘: 圆心 (cx,cy) 与半径 r 都是**连续坐标**
// (像素中心口径 ⇒ 整数圆心 + 整数半径时圆心像素是满覆盖)。
//
// 逐行按 (r+1)² 裁出 x 区间, 避免对包围盒做 O((2r)²) 的全量遍历。
func fillDiscAA(img *image.RGBA, cx, cy, r float64, c color.RGBA) {
	if img == nil || r <= 0 {
		return
	}
	b := img.Bounds()
	yLo := int(math.Floor(cy - r - 1))
	yHi := int(math.Ceil(cy + r + 1))
	if yLo < b.Min.Y {
		yLo = b.Min.Y
	}
	if yHi > b.Max.Y {
		yHi = b.Max.Y
	}
	for y := yLo; y < yHi; y++ {
		dy := float64(y) + 0.5 - cy
		rem := (r+1)*(r+1) - dy*dy
		if rem <= 0 {
			continue
		}
		half := math.Sqrt(rem)
		xa := int(math.Floor(cx - half))
		xb := int(math.Ceil(cx + half))
		if xa < b.Min.X {
			xa = b.Min.X
		}
		if xb > b.Max.X {
			xb = b.Max.X
		}
		for x := xa; x < xb; x++ {
			d := math.Hypot(float64(x)+0.5-cx, dy)
			if cov := covClamp(r + 0.5 - d); cov > 0 {
				blendPixel(img, x, y, c, cov)
			}
		}
	}
}

// fillRingAA 以覆盖率抗锯齿填充环带扇区 (环形图的一片 / 圆环描边): 连续
// 坐标圆心 (cx,cy), 内外半径 rOuter / rInner, 从 start 起扫过 sweep 弧度
// (正向顺时针, 与 FillArc 同约定)。
//
// 覆盖率 = 径向环带覆盖 × 角向扇区覆盖:
//
//	径向: clamp(rOuter+0.5-d) × clamp(d-rInner+0.5)
//	角向: 半径 d 处到两侧边界的弧长分别是 d*rel 与 d*(sweep-rel), 于是
//	      clamp(d*rel+0.5) × clamp(d*(sweep-rel)+0.5)
//
// 乘积近似在两个边界交汇处 (扇区尖角) 略有偏差, 但环带真正显眼的锯齿
// (内外两条圆弧、两侧直边) 都在 1px 内被抹平了。rInner<=0 时退成实心扇形。
func fillRingAA(img *image.RGBA, cx, cy, rOuter, rInner, start, sweep float64, c color.RGBA) {
	if img == nil || rOuter <= 0 || rInner >= rOuter {
		return
	}
	if rInner < 0 {
		rInner = 0
	}
	b := img.Bounds()
	full := sweep >= tau-1e-9
	yLo := int(math.Floor(cy - rOuter - 1))
	yHi := int(math.Ceil(cy + rOuter + 1))
	if yLo < b.Min.Y {
		yLo = b.Min.Y
	}
	if yHi > b.Max.Y {
		yHi = b.Max.Y
	}
	for y := yLo; y < yHi; y++ {
		dy := float64(y) + 0.5 - cy
		rem := (rOuter+1)*(rOuter+1) - dy*dy
		if rem <= 0 {
			continue
		}
		half := math.Sqrt(rem)
		xa := int(math.Floor(cx - half))
		xb := int(math.Ceil(cx + half))
		if xa < b.Min.X {
			xa = b.Min.X
		}
		if xb > b.Max.X {
			xb = b.Max.X
		}
		for x := xa; x < xb; x++ {
			dx := float64(x) + 0.5 - cx
			d := math.Hypot(dx, dy)
			cov := covClamp(rOuter + 0.5 - d)
			if cov <= 0 {
				continue
			}
			if rInner > 0 {
				if v := covClamp(d - rInner + 0.5); v <= 0 {
					continue
				} else {
					cov *= v
				}
			}
			if !full {
				ang := math.Atan2(dy, dx)
				rel := ang - start
				for rel < 0 {
					rel += tau
				}
				for rel >= tau {
					rel -= tau
				}
				a0 := covClamp(d*rel + 0.5)
				a1 := covClamp(d*(sweep-rel) + 0.5)
				if a0 <= 0 || a1 <= 0 {
					continue
				}
				cov *= a0 * a1
				if cov <= 0 {
					continue
				}
			}
			blendPixel(img, x, y, c, cov)
		}
	}
}

// pathSubRows 是 fillPath 每条像素行的**竖直子扫描线**条数。
// 4 条 ⇒ 横向边 (竖直方向) 与斜边 (水平方向) 都被抹平; 再往上加收益递减,
// 而软件光栅的耗时是线性上升的。
const pathSubRows = 4

// fillPath 用扫描线 even-odd 规则填充路径的所有子路径 (覆盖率抗锯齿)。
//
// 每条像素行取 pathSubRows 条子扫描线, 每条求出 x 交点区间后按权重
// 1/pathSubRows 把"该子行的水平覆盖"累加进本行覆盖缓冲 —— 于是斜边与横向
// 边都被抹平, 而不只是斜边 (只按像素中心求一次交点的话, 横向边依旧硬)。
//
// 复杂度 O(rows × edges × pathSubRows): 交点求值仍是逐边一次, 与硬边版本
// 同阶。不追求 nonzero 规则 —— 甜甜圈靠 fillRing 直接画, 不靠两个反向子
// 路径挖洞。
func fillPath(img *image.RGBA, p *canvasPath, c color.RGBA) {
	if img == nil || len(p.subs) == 0 {
		return
	}
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
	b := img.Bounds()
	width := b.Dx()
	if width <= 0 {
		return
	}
	cov := make([]float64, width)
	w := 1.0 / float64(pathSubRows)
	yStart := int(math.Floor(minY))
	yEnd := int(math.Ceil(maxY))
	if yStart < b.Min.Y {
		yStart = b.Min.Y
	}
	if yEnd > b.Max.Y {
		yEnd = b.Max.Y
	}
	var xs []float64
	for y := yStart; y < yEnd; y++ {
		for i := range cov {
			cov[i] = 0
		}
		dirty := false
		for k := 0; k < pathSubRows; k++ {
			yc := float64(y) + (float64(k)+0.5)/float64(pathSubRows)
			xs = xs[:0]
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
				xa, xb := xs[i], xs[i+1]
				if xb <= xa {
					continue
				}
				lx := int(math.Floor(xa))
				rx := int(math.Floor(xb))
				if rx <= lx {
					// 整段落在同一个像素内: 覆盖就是这一段的宽度
					dirty = addSpanCov(cov, b.Min.X, lx, lx, w*(xb-xa)) || dirty
					continue
				}
				dirty = addSpanCov(cov, b.Min.X, lx, lx, w*(float64(lx+1)-xa)) || dirty
				if rx > lx+1 {
					dirty = addSpanCov(cov, b.Min.X, lx+1, rx-1, w) || dirty
				}
				dirty = addSpanCov(cov, b.Min.X, rx, rx, w*(xb-float64(rx))) || dirty
			}
		}
		if !dirty {
			continue
		}
		for i, v := range cov {
			if v <= 0 {
				continue
			}
			blendPixel(img, b.Min.X+i, y, c, covClamp(v))
		}
	}
}

// addSpanCov 给覆盖缓冲的 [from,to] 段各加 v (越界的列直接丢弃: 调用方传
// 的是画布内的行, 画布外的列不该落笔)。返回是否真的加了 (供调用方记账)。
func addSpanCov(cov []float64, origin, from, to int, v float64) bool {
	if v <= 0 {
		return false
	}
	if from < origin {
		from = origin
	}
	if to >= origin+len(cov) {
		to = origin + len(cov) - 1
	}
	if from > to {
		return false
	}
	for i := from; i <= to; i++ {
		cov[i-origin] += v
	}
	return true
}
