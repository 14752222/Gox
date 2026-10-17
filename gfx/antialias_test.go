package gfx

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// ===== 抗锯齿回归 =====
//
// 内核此前整条光栅链路都是**硬边** (像素级取整): 斜线、圆弧、环带、路径填充
// 全是楼梯状锯齿 —— 深色底 + 高饱和细线上尤其扎眼。antialias.go 把它们统一
// 换成覆盖率合成后, 这一组锁住两件必须同时成立的事:
//
//  1. **斜线 / 弧 / 环真的有过渡像素** —— 断言画面上存在"严格介于底色与前景
//     色之间"的中间色 (覆盖率合成的直接证据, 硬边实现下这个数恒为 0);
//  2. **横竖线仍然实色** —— AA 最容易翻车的地方是把 1px 分隔线/边框糊成两条
//     半亮的线, 所以整数端点的水平/垂直线必须保持整行/整列纯色、不外溢。
//
// 口径与 canvas_path_test.go 一致: 断言一律用**像素**, 不比对中间结构。
// 临时把 covClamp 改成"非 0 即 1"就能让第 1 组全红 —— 那是用例有效性的证明。

var aaRed = color.RGBA{R: 0xFF, A: 0xFF}

// isIntermediate 判定像素 p 是否**严格介于** bg 与 fg 之间 (至少一个通道落在
// 两端之间): 覆盖率过渡像素的判据。
func isIntermediate(p, bg, fg color.RGBA) bool {
	pc := [3]uint8{p.R, p.G, p.B}
	bc := [3]uint8{bg.R, bg.G, bg.B}
	fc := [3]uint8{fg.R, fg.G, fg.B}
	any := false
	for i := 0; i < 3; i++ {
		lo, hi := bc[i], fc[i]
		if lo > hi {
			lo, hi = hi, lo
		}
		if pc[i] < lo || pc[i] > hi {
			return false // 有通道跑出区间: 不是这两色的混合
		}
		if pc[i] != lo && pc[i] != hi {
			any = true
		}
	}
	return any
}

// countIntermediate 数出画布上的覆盖率过渡像素 (AA 的直接产物)。
func countIntermediate(img *image.RGBA, bg, fg color.RGBA) int {
	n := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if isIntermediate(pixAt(img, x, y), bg, fg) {
				n++
			}
		}
	}
	return n
}

// assertBlend 断言 (x,y) 是 bg 与 fg 之间的**过渡色** (既不是纯 bg 也不是纯
// fg): 抗锯齿边缘像素的判据 —— 恰好压在形状边界上的像素本就该是半覆盖。
//
// 既有用例里那些"圆环顶部像素 == 边框色"式的断言, 在启用 AA 后必须换成这个
// 口径: 否则会把正确的抗锯齿产物当成回归。
func assertBlend(t *testing.T, img *image.RGBA, x, y int, bg, fg color.RGBA, msg string) {
	t.Helper()
	got := pixAt(img, x, y)
	if got == bg || got == fg || !isIntermediate(got, bg, fg) {
		t.Fatalf("%s: (%d,%d) = %v, 期望 %v 与 %v 之间的过渡色", msg, x, y, got, bg, fg)
	}
}

// TestDiagonalLineHasPartialCoverage 锁「斜线有过渡像素」: 一条 60×20 的
// 2px 斜线, 硬边实现下每个像素非黑即白, AA 下边缘必然出现中间色。
func TestDiagonalLineHasPartialCoverage(t *testing.T) {
	img := testImg(80, 40)
	fillLine(img, 0, 0, 60, 20, 2, aaRed)
	if n := countIntermediate(img, color.RGBA{R: 255, G: 255, B: 255, A: 255}, aaRed); n < 20 {
		t.Fatalf("2px 斜线的过渡像素 = %d, 期望 >= 20 (抗锯齿没生效?)", n)
	}
}

// TestAxisAlignedLineStaysCrisp 锁「横竖线仍然实色」—— AA 的翻车高发区:
// 整数端点的 1px 线必须整行/整列纯色, 且**不外溢**到相邻行/列。
func TestAxisAlignedLineStaysCrisp(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	img := testImg(60, 30)
	fillLine(img, 5, 10, 40, 10, 1, aaRed)
	// 整行实色
	for x := 5; x <= 40; x++ {
		if got := pixAt(img, x, 10); got != aaRed {
			t.Fatalf("水平线 (%d,10) = %v, 期望纯红 (1px 线不该被 AA 冲淡)", x, got)
		}
	}
	// 不外溢: 上下相邻行必须仍是纯白 (一个过渡像素都不许有)
	for _, y := range []int{9, 11} {
		for x := 0; x < 60; x++ {
			if got := pixAt(img, x, y); got != white {
				t.Fatalf("水平线外溢到 (%d,%d) = %v, 期望纯白", x, y, got)
			}
		}
	}

	img2 := testImg(30, 60)
	fillLine(img2, 10, 5, 10, 40, 1, aaRed)
	for y := 5; y <= 40; y++ {
		if got := pixAt(img2, 10, y); got != aaRed {
			t.Fatalf("垂直线 (10,%d) = %v, 期望纯红", y, got)
		}
	}
	for _, x := range []int{9, 11} {
		for y := 0; y < 60; y++ {
			if got := pixAt(img2, x, y); got != white {
				t.Fatalf("垂直线外溢到 (%d,%d) = %v, 期望纯白", x, y, got)
			}
		}
	}
}

// TestThickLineWidthKept 锁「AA 不改变线宽」: lineWidth=5 的竖线横向仍应是
// 5 个像素宽 (与 canvas_path_test.go 的 lineWidth 口径同一条线)。
func TestThickLineWidthKept(t *testing.T) {
	img := testImg(40, 40)
	fillLine(img, 10, 5, 10, 30, 5, aaRed)
	w := 0
	for x := 0; x < 40; x++ {
		if pixAt(img, x, 20) == aaRed {
			w++
		}
	}
	if w != 5 {
		t.Fatalf("lineWidth=5 的竖线横向宽度 = %d, 期望 5", w)
	}
}

// TestCircleAndRingHavePartialCoverage 锁「圆 / 环的边缘有过渡像素」——
// 圆弧是看板里最显眼的锯齿来源 (仪表盘弧、环形图)。
func TestCircleAndRingHavePartialCoverage(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}

	img := testImg(40, 40)
	FillCircle(img, 20, 20, 15, aaRed)
	if n := countIntermediate(img, white, aaRed); n < 20 {
		t.Fatalf("实心圆的过渡像素 = %d, 期望 >= 20 (圆周边缘没做抗锯齿?)", n)
	}
	// 圆心必须仍是满覆盖: AA 不该把实心图形整体冲淡。
	if got := pixAt(img, 20, 20); got != aaRed {
		t.Fatalf("圆心 (20,20) = %v, 期望纯红", got)
	}

	img2 := testImg(40, 40)
	StrokeCircle(img2, 20, 20, 15, aaRed)
	if n := countIntermediate(img2, white, aaRed); n < 20 {
		t.Fatalf("圆环的过渡像素 = %d, 期望 >= 20", n)
	}
	if got := pixAt(img2, 20, 20); got != white {
		t.Fatalf("圆环内部 (20,20) = %v, 应为空", got)
	}

	img3 := testImg(40, 40)
	FillRing(img3, 20, 20, 15, 8, 0, 2*math.Pi, aaRed)
	if n := countIntermediate(img3, white, aaRed); n < 20 {
		t.Fatalf("环带的过渡像素 = %d, 期望 >= 20", n)
	}
	if got := pixAt(img3, 20, 32); got != aaRed {
		t.Fatalf("环带上 (20,32) = %v, 期望纯红 (带内应是满覆盖)", got)
	}
}

// TestPathFillHasPartialCoverage 锁「路径填充 (面积图 / 扇形 / 雷达) 的边缘
// 有过渡像素」, 同时**内部必须仍是满覆盖** —— 只抹边缘、不糊整体。
func TestPathFillHasPartialCoverage(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	img := testImg(60, 60)
	var p canvasPath
	p.moveTo(canvasPt{x: 5, y: 5})
	p.lineTo(canvasPt{x: 50, y: 20}) // 斜边: 锯齿的主要来源
	p.lineTo(canvasPt{x: 50, y: 50})
	p.lineTo(canvasPt{x: 5, y: 50})
	p.subs[len(p.subs)-1].closed = true
	fillPath(img, &p, aaRed)

	if n := countIntermediate(img, white, aaRed); n < 10 {
		t.Fatalf("路径填充的过渡像素 = %d, 期望 >= 10 (斜边没做抗锯齿?)", n)
	}
	// 内部与水平边: 满覆盖
	if got := pixAt(img, 30, 40); got != aaRed {
		t.Fatalf("填充内部 (30,40) = %v, 期望纯红", got)
	}
	if got := pixAt(img, 30, 5); got != white {
		t.Fatalf("三角形外 (30,5) = %v, 应为空", got)
	}
}

// TestStrokePathHasPartialCoverage 锁「折线描边有过渡像素」—— 看板的趋势线
// 走的就是 strokePath (它是 fillLineAA 的最大客户)。
func TestStrokePathHasPartialCoverage(t *testing.T) {
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	img := testImg(60, 60)
	var p canvasPath
	p.moveTo(canvasPt{x: 5, y: 10})
	p.lineTo(canvasPt{x: 40, y: 25})
	p.lineTo(canvasPt{x: 55, y: 50})
	strokePath(img, &p, 2, aaRed)
	if n := countIntermediate(img, white, aaRed); n < 10 {
		t.Fatalf("折线描边的过渡像素 = %d, 期望 >= 10", n)
	}
}
