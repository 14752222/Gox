package gfx

import (
	"image"
	"image/color"
	"math"

	"github.com/14752222/Gox/object"
)

// T08 rating 星级评分组件。
//
//	<rating value={score()} max={5} onChange={(e) => setScore(e.value)} />
//	<rating value={3} max={10} color="#f5a623" />
//
// 与 select / pagination 同一哲学 —— **完全受控**: 画面只看 value prop,
// 点击只派发 onChange({value}), 不改自身状态; 脚本不把新值写回 signal
// 就是不动。model 指令把它接成 value ⇄ onChange (见 gfx/model.go)。
//
// 视觉: 每颗星占一个方格 (缺省 20px), 前 value 颗实心 (color prop, 缺省
// 主题强调色), 其余空心描边。画法刻意只动用光栅原语: 实心 = 五角星多边形
// 逐像素 even-odd 内点判定 (星形 ≤ 20px, 包围盒扫描足够), 空心 = 顶点连线
// 1px 描边 (与内置图标同一 fillLine 原语) —— 无抗锯齿、无第三方依赖。
//
// 几何: 星格宽 = Box.W / max, 星半径 = min(格宽, Box.H) * 0.45 —— 组件被
// 拉大时星形跟着变大, 缩小时保持不溢出。点击命中与绘制共用同一套几何,
// 落在哪格就派发第几颗 (第 idx+1 颗)。

const (
	// ratingDefaultMax 缺省星数 (与常见评分组件一致)。
	ratingDefaultMax = 5
	// ratingMaxCap max 的上限: 星形渲染是 O(格数 × 包围盒), 不设上限的
	// max={99999} 会把一次重绘拖成整帧; 10 已经覆盖所有现实场景。
	ratingMaxCap = 10
	// ratingCellSize 缺省每颗星的格子边长 (像素), 也是缺省高度。
	ratingCellSize = 20
	// ratingInnerRatio 五角星内半径 / 外半径。0.447 ≈ 2/(√(10+2√5)+2) 的
	// 近似: 经典"尖角五角星"的比例, 再小会瘦成针, 再大就胖成花。
	ratingInnerRatio = 0.45
	// ratingStarScale 星外半径相对格边长的比例: 留 10% 边距, 相邻星的
	// 角不会互相戳到。
	ratingStarScale = 0.45
)

// ratingMax 读 max prop (缺省 5, clamp 到 [1, 10])。
func (n *GuiNode) ratingMax() int {
	if v, ok := n.PropNum("max"); ok && v >= 1 {
		m := int(v)
		if m > ratingMaxCap {
			m = ratingMaxCap
		}
		return m
	}
	return ratingDefaultMax
}

// ratingValue 读 value prop (缺省 0 = 全空)。
func (n *GuiNode) ratingValue() float64 {
	if v, ok := n.PropNum("value"); ok {
		return v
	}
	return 0
}

// paintRating 画星级: 先补画通用装饰 (background/border 纪律), 再逐格画星。
func paintRating(img *image.RGBA, n *GuiNode, disabled bool) {
	paintBoxDecor(img, n, disabled)
	max := n.ratingMax()
	if n.Box.W <= 0 || n.Box.H <= 0 || max <= 0 {
		return
	}
	cell := float64(n.Box.W) / float64(max)
	r := math.Min(cell, float64(n.Box.H)) * ratingStarScale
	filled := tint(n.propColor("color", colorAccent), disabled)
	empty := tint(colorTrack, disabled)
	value := n.ratingValue()
	cy := float64(n.Box.Y) + float64(n.Box.H)/2
	for i := 0; i < max; i++ {
		cx := float64(n.Box.X) + cell*(float64(i)+0.5)
		if value >= float64(i+1) {
			paintStar(img, cx, cy, r, true, filled)
		} else {
			paintStar(img, cx, cy, r, false, empty)
		}
	}
}

// paintStar 画一颗五角星。filled = 多边形实心填充 (逐像素 even-odd 内点
// 判定), 否则顶点连线 1px 描边。中心坐标允许带小数, 内部取整到像素。
func paintStar(img *image.RGBA, cx, cy, r float64, filled bool, col color.RGBA) {
	poly := ratingStarPoly(cx, cy, r)
	if filled {
		for py := int(math.Floor(cy - r)); py <= int(math.Ceil(cy+r)); py++ {
			for px := int(math.Floor(cx - r)); px <= int(math.Ceil(cx+r)); px++ {
				// 用像素中心判定: 光栅化口径与 fillCircle 等原语一致。
				if ratingPointInPoly(float64(px)+0.5, float64(py)+0.5, poly) {
					img.SetRGBA(px, py, col)
				}
			}
		}
		return
	}
	for i := 0; i < len(poly); i++ {
		a := poly[i]
		b := poly[(i+1)%len(poly)]
		fillLine(img, int(a[0]), int(a[1]), int(b[0]), int(b[1]), 1, col)
	}
}

// ratingStarPoly 返回五角星的 10 个顶点 (外/内半径交替, 尖角朝上)。
func ratingStarPoly(cx, cy, r float64) [][2]float64 {
	poly := make([][2]float64, 10)
	for i := 0; i < 10; i++ {
		ang := -math.Pi/2 + float64(i)*math.Pi/5
		rad := r
		if i%2 == 1 {
			rad = r * ratingInnerRatio
		}
		poly[i] = [2]float64{cx + rad*math.Cos(ang), cy + rad*math.Sin(ang)}
	}
	return poly
}

// ratingPointInPoly even-odd 内点判定 (射线向右)。顶点数固定 10, 无需优化。
func ratingPointInPoly(px, py float64, poly [][2]float64) bool {
	inside := false
	j := len(poly) - 1
	for i := 0; i < len(poly); i++ {
		xi, yi := poly[i][0], poly[i][1]
		xj, yj := poly[j][0], poly[j][1]
		if (yi > py) != (yj > py) && px < (xj-xi)*(py-yi)/(yj-yi)+xi {
			inside = !inside
		}
		j = i
	}
	return inside
}

// ratingInChain 从 n 起沿祖先链找第一个 rating。
func ratingInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "rating" {
			return p
		}
	}
	return nil
}

// ratingPick 点击落在第几格 (x → 星序): 派发第 idx+1 颗对应的值。
// 与 tabs/pagination 同款 —— mousedown 几何命中即派发, 不等 click。
func (a *app) ratingPick(n *GuiNode, x int) {
	max := n.ratingMax()
	if max <= 0 || n.Box.W <= 0 {
		return
	}
	idx := (x - n.Box.X) * max / n.Box.W
	if idx < 0 {
		idx = 0
	}
	if idx >= max {
		idx = max - 1
	}
	a.ratingGo(n, idx+1)
}

// ratingGo 派发 onChange({value}): 值不变是 no-op (完全受控, 与
// paginationGo 同一口径 —— 显示值归 value prop, 这里不改自身状态)。
func (a *app) ratingGo(n *GuiNode, value int) {
	if float64(value) == n.ratingValue() {
		return
	}
	if n.PropHandler("onChange") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewNumber(float64(value)))
	a.callHandler(n, "onChange", arg)
}
