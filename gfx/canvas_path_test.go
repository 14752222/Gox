package gfx

import (
	"image"
	"image/color"
	"math"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== 路径 / 弧 / 变换 / 像素 (rIowkb) =====
//
// 这一组锁住四件此前完全没有的事:
//   - fillArc / fillRing —— 饼图与环形图的落笔原语;
//   - beginPath..fill/stroke —— 任意多边形 (面积图 / 折线填充);
//   - save/restore/translate/scale/rotate/setTransform —— 变换;
//   - getImageData / putImageData / createImageData —— 像素读写。
//
// 断言一律用**像素**: 画完直接数颜色, 不比对中间结构。

// testImg 造一张纯白测试画布。
func testImg(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 255, 255, 255, 255
	}
	return img
}

// countPix 数出画布上等于 c 的像素数。
func countPix(img *image.RGBA, c color.RGBA) int {
	n := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			i := img.PixOffset(x, y)
			if img.Pix[i] == c.R && img.Pix[i+1] == c.G && img.Pix[i+2] == c.B {
				n++
			}
		}
	}
	return n
}

// pixAt 取一个像素的 RGBA (非预乘视角: 调用方只比不透明色, 直接比通道即可)。
func pixAt(img *image.RGBA, x, y int) color.RGBA {
	i := img.PixOffset(x, y)
	return color.RGBA{R: img.Pix[i], G: img.Pix[i+1], B: img.Pix[i+2], A: img.Pix[i+3]}
}

var testRed = color.RGBA{R: 0xFF, A: 0xFF}

// TestFillArcDrawsPieSlice 验扇形: 第一象限的 90° 扇形右半区必须有像素,
// 第三象限 (未扫过的区域) 必须一个都没有。
func TestFillArcDrawsPieSlice(t *testing.T) {
	img := testImg(40, 40)
	FillArc(img, 20, 20, 15, 0, math.Pi/2, testRed)
	// 圆心右侧、略高于圆心的点在第一象限扇形内 (y 朝下, 0→π/2 是右下方向)。
	if got := pixAt(img, 28, 22); got != testRed {
		t.Fatalf("(28,22) = %v, 期望红 (应在 0~π/2 扇形内)", got)
	}
	// 圆心左侧对称点: 未被扫过。
	if got := pixAt(img, 12, 22); got == testRed {
		t.Fatalf("(12,22) 被填到了, 但 π/2~π 不该在扇形内")
	}
	if n := countPix(img, testRed); n < 50 {
		t.Fatalf("扇形只填了 %d 个像素, 太少 (半径 15 的 1/4 圆应约 176)", n)
	}
}

// TestFillRingLeavesHole 验环形: 环带上有像素, 内孔必须是空的 —— 这正是
// 环形图区别于饼图的地方。
func TestFillRingLeavesHole(t *testing.T) {
	img := testImg(40, 40)
	FillRing(img, 20, 20, 15, 8, 0, 2*math.Pi, testRed)
	if got := pixAt(img, 20, 32); got != testRed {
		t.Fatalf("(20,32) 在环带上, 应是红, 实际 %v", got)
	}
	if got := pixAt(img, 20, 22); got == testRed {
		t.Fatalf("(20,22) 在内孔里, 不该被填")
	}
	if n := countPix(img, testRed); n < 100 {
		t.Fatalf("环带只填了 %d 个像素, 太少", n)
	}
}

// TestFillRingSectorRespectsAngles 验环形的角度裁剪: 只扫 0~π/2 时,
// 对面的环带上不该有像素。
func TestFillRingSectorRespectsAngles(t *testing.T) {
	img := testImg(40, 40)
	FillRing(img, 20, 20, 15, 9, 0, math.Pi/2, testRed)
	// (32,20) 距圆心 12 ⇒ 落在 [9,15] 环带上, 且角度为 0 (在扫过角内)。
	if got := pixAt(img, 32, 20); got != testRed {
		t.Fatalf("(32,20) 应在 0~π/2 环带内, 实际 %v", got)
	}
	// (8,20) 同样距圆心 12, 但角度是 π ⇒ 不在扫过角内。
	if got := pixAt(img, 8, 20); got == testRed {
		t.Fatalf("(8,20) 不在扫过角内, 不该被填")
	}
}

// TestFillArcViaPathIsPie 验「路径 API 也能画出饼图」—— HTML canvas 的经典
// 写法 (moveTo 圆心 + arc + closePath + fill) 必须成立, 否则搬现成图表代码
// 会静默画不出东西。
func TestFillArcViaPathIsPie(t *testing.T) {
	img := testImg(40, 40)
	var p canvasPath
	p.moveTo(canvasPt{x: 20, y: 20})
	p.arc(20, 20, 15, 0, math.Pi/2, false)
	p.subs[len(p.subs)-1].closed = true
	fillPath(img, &p, testRed)
	if got := pixAt(img, 28, 22); got != testRed {
		t.Fatalf("路径 fill 后 (28,22) 应是红, 实际 %v", got)
	}
	if got := pixAt(img, 12, 22); got == testRed {
		t.Fatalf("路径 fill 把未扫过的区域也填了")
	}
}

// TestStrokePathDrawsOutline 验描边: 只画轮廓, 内部保持底色。
func TestStrokePathDrawsOutline(t *testing.T) {
	img := testImg(40, 40)
	var p canvasPath
	p.moveTo(canvasPt{x: 5, y: 5})
	p.lineTo(canvasPt{x: 30, y: 5})
	p.lineTo(canvasPt{x: 30, y: 30})
	p.lineTo(canvasPt{x: 5, y: 30})
	p.subs[len(p.subs)-1].closed = true
	strokePath(img, &p, 1, testRed)
	if got := pixAt(img, 15, 5); got != testRed {
		t.Fatalf("上边 (15,5) 应有描边, 实际 %v", got)
	}
	if got := pixAt(img, 15, 15); got == testRed {
		t.Fatalf("内部 (15,15) 不该被描边填到")
	}
}

// TestXformCompose 验变换的复合序: translate(10,0) 后 scale(2,1), 点 (5,0)
// 应先缩放再平移 ⇒ (20,0)。
func TestXformCompose(t *testing.T) {
	m := identityXform().then(canvasXform{a: 1, d: 1, e: 10})
	m = m.then(canvasXform{a: 2, d: 2})
	x, y := m.apply(5, 0)
	if math.Abs(x-20) > 1e-9 || math.Abs(y-0) > 1e-9 {
		t.Fatalf("apply(5,0) = (%v,%v), 期望 (20,0)", x, y)
	}
	// 等比缩放 2×: |det| = 4 ⇒ 各向同性缩放估计 sqrt(4) = 2。
	if s := m.scaleFactor(); math.Abs(s-2) > 1e-9 {
		t.Fatalf("scaleFactor = %v, 期望 2", s)
	}
	// 非等比 (2,1): 估计值是几何平均 sqrt(2) —— 圆的半径会按它换算,
	// 这是有意的简化 (见 scaleFactor 注释)。
	if s := identityXform().then(canvasXform{a: 2, d: 1}).scaleFactor(); math.Abs(s-math.Sqrt2) > 1e-9 {
		t.Fatalf("非等比 scaleFactor = %v, 期望 sqrt(2)", s)
	}
}

// TestCtxPathAndTransform 走 ctx 这一层: 变换后的路径落笔位置必须跟着走。
func TestCtxPathAndTransform(t *testing.T) {
	img := testImg(60, 60)
	tgt := &canvasTarget{img: img, box: Rect{X: 0, Y: 0, W: 60, H: 60}}
	ctx := makeCtx(tgt)

	// 无变换: (10,10)-(30,30) 的填充应覆盖 (20,20)。
	ctxCall(t, ctx, "beginPath")
	ctxCall(t, ctx, "moveTo", cn(10), cn(10))
	ctxCall(t, ctx, "lineTo", cn(30), cn(10))
	ctxCall(t, ctx, "lineTo", cn(30), cn(30))
	ctxCall(t, ctx, "lineTo", cn(10), cn(30))
	ctxCall(t, ctx, "closePath")
	ctxCall(t, ctx, "fill", cs("#ff0000"))
	if got := pixAt(img, 20, 20); got != testRed {
		t.Fatalf("无变换时 (20,20) 应是红, 实际 %v", got)
	}

	// translate(30,30) 后画同一个方块: (20,20) 必须**不再**是红, (45,45) 才是。
	img2 := testImg(60, 60)
	tgt2 := &canvasTarget{img: img2, box: Rect{X: 0, Y: 0, W: 60, H: 60}}
	ctx2 := makeCtx(tgt2)
	ctxCall(t, ctx2, "save")
	ctxCall(t, ctx2, "translate", cn(30), cn(30))
	ctxCall(t, ctx2, "beginPath")
	ctxCall(t, ctx2, "moveTo", cn(10), cn(10))
	ctxCall(t, ctx2, "lineTo", cn(30), cn(10))
	ctxCall(t, ctx2, "lineTo", cn(30), cn(30))
	ctxCall(t, ctx2, "lineTo", cn(10), cn(30))
	ctxCall(t, ctx2, "closePath")
	ctxCall(t, ctx2, "fill", cs("#ff0000"))
	if got := pixAt(img2, 20, 20); got == testRed {
		t.Fatalf("translate 之后 (20,20) 不该再是红")
	}
	if got := pixAt(img2, 45, 45); got != testRed {
		t.Fatalf("translate 之后 (45,45) 应是红, 实际 %v", got)
	}
	// restore 回到变换前的状态: 再画一次应回到 (20,20)。
	ctxCall(t, ctx2, "restore")
	ctxCall(t, ctx2, "beginPath")
	ctxCall(t, ctx2, "moveTo", cn(10), cn(10))
	ctxCall(t, ctx2, "lineTo", cn(30), cn(10))
	ctxCall(t, ctx2, "lineTo", cn(30), cn(30))
	ctxCall(t, ctx2, "lineTo", cn(10), cn(30))
	ctxCall(t, ctx2, "closePath")
	ctxCall(t, ctx2, "fill", cs("#ff0000"))
	if got := pixAt(img2, 20, 20); got != testRed {
		t.Fatalf("restore 之后 (20,20) 应回到红, 实际 %v", got)
	}
}

// TestCtxLineWidthAccessor 验 lineWidth 是**双向**访问器: 写进去的值能被
// 读回, 也真的影响描边粗细。
func TestCtxLineWidthAccessor(t *testing.T) {
	tgt := &canvasTarget{img: testImg(40, 40), box: Rect{X: 0, Y: 0, W: 40, H: 40}}
	ctx := makeCtx(tgt)
	if got := ctxNum(t, ctx, "lineWidth"); got != 1 {
		t.Fatalf("缺省 lineWidth = %v, 期望 1", got)
	}
	ctx.SetProperty("lineWidth", cn(4))
	if got := ctxNum(t, ctx, "lineWidth"); got != 4 {
		t.Fatalf("写 4 之后读回 %v, 期望 4 (访问器没接上状态机?)", got)
	}
	// 真值来源是属性本身, 不是 t.state 里的副本。
	if got := lineWidthOf(ctx, -1); got != 4 {
		t.Fatalf("lineWidthOf = %d, 期望 4", got)
	}
	// 并且真的影响落笔粗细: 一条竖线设成 5px 后, 横向应有 5 个红像素。
	img := testImg(40, 40)
	tgt2 := &canvasTarget{img: img, box: Rect{X: 0, Y: 0, W: 40, H: 40}}
	ctx2 := makeCtx(tgt2)
	ctx2.SetProperty("lineWidth", cn(5))
	ctxCall(t, ctx2, "beginPath")
	ctxCall(t, ctx2, "moveTo", cn(10), cn(5))
	ctxCall(t, ctx2, "lineTo", cn(10), cn(30))
	ctxCall(t, ctx2, "stroke", cs("#ff0000"))
	w := 0
	for x := 0; x < 40; x++ {
		if pixAt(img, x, 20) == testRed {
			w++
		}
	}
	if w != 5 {
		t.Fatalf("lineWidth=5 时竖线的横向宽度 = %d, 期望 5", w)
	}
}

// TestCtxArcPrimitives 验 ctx.fillArc / ctx.strokeArc / ctx.fillRing 都在
// (此前 ctx 上根本没有这三个方法, ctxCall 会直接炸)。
func TestCtxArcPrimitives(t *testing.T) {
	img := testImg(60, 60)
	ctx := makeCtx(&canvasTarget{img: img, box: Rect{X: 0, Y: 0, W: 60, H: 60}})
	ctxCall(t, ctx, "fillArc", cn(30), cn(30), cn(20), cn(0), cn(math.Pi/2), cs("#ff0000"))
	if got := pixAt(img, 40, 35); got != testRed {
		t.Fatalf("fillArc: (40,35) 应是红, 实际 %v", got)
	}

	img2 := testImg(60, 60)
	ctx2 := makeCtx(&canvasTarget{img: img2, box: Rect{X: 0, Y: 0, W: 60, H: 60}})
	ctxCall(t, ctx2, "strokeArc", cn(30), cn(30), cn(20), cn(0), cn(2*math.Pi), cs("#ff0000"))
	// 圆环上: 圆心正上方 (30,10) 应是红; 圆心 (30,30) 不该是红。
	if got := pixAt(img2, 30, 10); got != testRed {
		t.Fatalf("strokeArc: (30,10) 应是红, 实际 %v", got)
	}
	if got := pixAt(img2, 30, 30); got == testRed {
		t.Fatalf("strokeArc: 圆心不该被填")
	}

	img3 := testImg(60, 60)
	ctx3 := makeCtx(&canvasTarget{img: img3, box: Rect{X: 0, Y: 0, W: 60, H: 60}})
	ctxCall(t, ctx3, "fillRing", cn(30), cn(30), cn(20), cn(10), cn(0), cn(2*math.Pi), cs("#ff0000"))
	if got := pixAt(img3, 30, 45); got != testRed {
		t.Fatalf("fillRing: (30,45) 在环带上, 应是红, 实际 %v", got)
	}
	if got := pixAt(img3, 30, 32); got == testRed {
		t.Fatalf("fillRing: (30,32) 在内孔里, 不该被填")
	}
}

// TestCtxImageDataRoundTrip 验像素读写的往返: 画一块红, 读出来, 挪个位写回,
// 目标位置必须是红、源位置必须是原样。
func TestCtxImageDataRoundTrip(t *testing.T) {
	img := testImg(40, 40)
	ctx := makeCtx(&canvasTarget{img: img, box: Rect{X: 0, Y: 0, W: 40, H: 40}})
	ctxCall(t, ctx, "fillRect", cn(0), cn(0), cn(10), cn(10), cs("#ff0000"))

	fn, _ := ctx.GetProperty("getImageData")
	got := callValue(fn, cn(0), cn(0), cn(10), cn(10))
	idata, ok := got.(*object.Object)
	if !ok {
		t.Fatalf("getImageData 返回 %T, 期望对象", got)
	}
	wv, _ := idata.GetProperty("width")
	if n, ok := wv.(*object.Number); !ok || n.Value != 10 {
		t.Fatalf("imageData.width = %v, 期望 10", wv)
	}
	dv, _ := idata.GetProperty("data")
	ta, ok := dv.(*object.TypedArray)
	if !ok {
		t.Fatalf("imageData.data 是 %T, 期望 TypedArray", dv)
	}
	if ta.Length != 10*10*4 {
		t.Fatalf("data.length = %d, 期望 400", ta.Length)
	}
	// 第一个像素应是 R=255,G=0,B=0,A=255 (非预乘读出)。
	if v := toNumValue(ta.GetElement(0)); v != 255 {
		t.Fatalf("data[0] = %v, 期望 255", v)
	}
	if v := toNumValue(ta.GetElement(1)); v != 0 {
		t.Fatalf("data[1] = %v, 期望 0", v)
	}

	// 写到 (20,20): 那里该变红。
	pf, _ := ctx.GetProperty("putImageData")
	callValue(pf, idata, cn(20), cn(20))
	if got := pixAt(img, 25, 25); got != testRed {
		t.Fatalf("putImageData 之后 (25,25) 应是红, 实际 %v", got)
	}
}

// TestCtxCreateImageData 验 createImageData 造出全 0 的指定尺寸缓冲。
func TestCtxCreateImageData(t *testing.T) {
	ctx := makeCtx(&canvasTarget{img: testImg(8, 8), box: Rect{X: 0, Y: 0, W: 8, H: 8}})
	fn, _ := ctx.GetProperty("createImageData")
	got := callValue(fn, cn(3), cn(4))
	o, ok := got.(*object.Object)
	if !ok {
		t.Fatalf("createImageData 返回 %T", got)
	}
	hv, _ := o.GetProperty("height")
	if n, ok := hv.(*object.Number); !ok || n.Value != 4 {
		t.Fatalf("height = %v, 期望 4", hv)
	}
	dv, _ := o.GetProperty("data")
	ta := dv.(*object.TypedArray)
	if ta.Length != 3*4*4 {
		t.Fatalf("data.length = %d, 期望 48", ta.Length)
	}
	if v := toNumValue(ta.GetElement(0)); v != 0 {
		t.Fatalf("新建缓冲应全 0, data[0] = %v", v)
	}
}

// TestNoopCtxHasAllPrimitives 是"两阶段同构"护栏: 空操作 ctx 必须挂齐全部
// 新方法 —— 否则依赖收集阶段会报 "ctx.fillArc is not a function", 而真绘制
// 时却是好的, 这类只有一半路径能跑到的问题极难排查。
func TestNoopCtxHasAllPrimitives(t *testing.T) {
	ctx := makeNoopCtx(Rect{X: 0, Y: 0, W: 40, H: 40})
	for _, name := range []string{
		"beginPath", "moveTo", "lineTo", "closePath", "arc", "fill", "stroke",
		"save", "restore", "translate", "scale", "rotate", "setTransform", "resetTransform",
		"fillArc", "strokeArc", "fillRing",
		"getImageData", "putImageData", "createImageData",
	} {
		if fn, ok := ctx.GetProperty(name); !ok || !object.IsCallable(fn) {
			t.Fatalf("空操作 ctx 缺少方法 %s", name)
		}
	}
	if _, ok := ctx.GetProperty("lineWidth"); !ok {
		t.Fatalf("空操作 ctx 缺少 lineWidth 属性")
	}
}

// TestNoopCtxRunsWithoutPanic 验依赖收集阶段 (img == nil) 跑完一整套路径 /
// 变换 / 像素调用不 panic, 也不往任何地方落笔。
func TestNoopCtxRunsWithoutPanic(t *testing.T) {
	ctx := makeNoopCtx(Rect{X: 0, Y: 0, W: 40, H: 40})
	ctxCall(t, ctx, "save")
	ctxCall(t, ctx, "translate", cn(5), cn(5))
	ctxCall(t, ctx, "rotate", cn(math.Pi / 4))
	ctxCall(t, ctx, "beginPath")
	ctxCall(t, ctx, "moveTo", cn(0), cn(0))
	ctxCall(t, ctx, "arc", cn(20), cn(20), cn(10), cn(0), cn(math.Pi))
	ctxCall(t, ctx, "closePath")
	ctxCall(t, ctx, "fill", cs("#ff0000"))
	ctxCall(t, ctx, "stroke", cs("#00ff00"))
	ctxCall(t, ctx, "fillArc", cn(10), cn(10), cn(5), cn(0), cn(1), cs("#ff0000"))
	ctxCall(t, ctx, "fillRing", cn(10), cn(10), cn(8), cn(4), cn(0), cn(1), cs("#ff0000"))
	ctxCall(t, ctx, "restore")

	fn, _ := ctx.GetProperty("getImageData")
	got := callValue(fn, cn(0), cn(0), cn(4), cn(4))
	o := got.(*object.Object)
	dv, _ := o.GetProperty("data")
	ta := dv.(*object.TypedArray)
	if ta.Length != 4*4*4 {
		t.Fatalf("空操作阶段 data.length = %d, 期望 64 (形状必须与真绘制一致)", ta.Length)
	}
}

// callValue 从 Go 侧调一个 JS 可调用值并取回返回值。
func callValue(fn object.Value, args ...object.Value) object.Value {
	if bf, ok := fn.(*object.BuiltinFunction); ok {
		return bf.Fn(args...)
	}
	return object.CallFunction(fn, nil, args...)
}

// TestCanvasPieChartFromJS 全链路: 真 VM + 真 JS onDraw 画一张环形图,
// 断言环带上有色、内孔与未扫过的一侧无色。
//
// 这是 rIowkb 的验收场景 —— 此前 ctx 上没有 fillRing / fillArc, 这张图在
// 脚本里根本画不出来 (只能退成横向条形图)。
func TestCanvasPieChartFromJS(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	_, root, fake := evalCanvasUI(t, `
		import { h, render } from "gx/gfx";
		render(
			h("column", null,
				h("canvas", {
					width: 60, height: 60,
					onDraw: (ctx) => {
						ctx.fillRing(30, 30, 24, 12, 0, Math.PI, "#c0392b");
					},
				})
			),
			{ title: "pie", width: 200, height: 160 }
		);
	`)

	c := findFirst(root, "canvas")
	if c == nil {
		t.Fatalf("没有挂上 canvas 节点")
	}
	img := shotsImage(fake)
	if img == nil {
		t.Fatalf("没有捕获到帧")
	}
	red := mustColor(t, "#c0392b")
	// 局部 (30,48): 距圆心 18 (在 12~24 环带上), 角度 π/2 (在 0~π 内) ⇒ 有色。
	assertPx(t, img, c.Box.X+30, c.Box.Y+48, red, "环带上应有色")
	// 局部 (30,35): 距圆心 5 ⇒ 内孔, 无色。
	if got := pixAt(img, c.Box.X+30, c.Box.Y+35); got == red {
		t.Fatalf("内孔不该被填: (30,35) = %v", got)
	}
	// 局部 (30,12): 距圆心 18 但角度是 -π/2 (未扫过的一侧) ⇒ 无色。
	if got := pixAt(img, c.Box.X+30, c.Box.Y+12); got == red {
		t.Fatalf("未扫过的一侧不该被填: (30,12) = %v", got)
	}
}
