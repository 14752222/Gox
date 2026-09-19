package gfx

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P3-1: canvas 自绘 =====
//
// 分两块验, 因为它们查的是两件不同的事:
//   - 纯 Go 用例 (mkNode + 直接挂 onDraw builtin) 验**绘制**语义: 坐标换算、
//     裁剪、禁用态、缺省尺寸、参数解析。这条路不经过 VM。
//   - 全链路用例 (真 VM + 真 JS onDraw) 验**响应式**语义: 读了 signal 的要重绘、
//     没读 signal 的不许标脏。这是 canvas 最容易做漏的一块 —— 少了 effect,
//     画布只在挂载那一刻画一次, 之后数据怎么变都不动。

// cn 造一个数值 JS 值 (坐标 / 尺寸)。
func cn(f float64) object.Value { return object.NewNumber(f) }

// cs 造一个字符串 JS 值 (颜色 / 文本)。
func cs(s string) object.Value { return object.NewString(s) }

// ctxCall 从 Go 侧像脚本那样调一次 ctx 方法。
//
// 刻意走"取属性 → 当函数调"这条与 JS 完全相同的路径: 如果 ctx 上少挂了某个
// 方法, 这里会立刻炸, 而不是等到真脚本跑起来报 "ctx.xxx is not a function"。
// 调用本身走 callScriptFn (纯 Go 环境没有 VM 回调桥, object.CallFunction 是
// 空操作 —— 见 canvas.go 里那段说明)。
func ctxCall(t *testing.T, ctx *object.Object, name string, args ...object.Value) {
	t.Helper()
	fn, ok := ctx.GetProperty(name)
	if !ok || !object.IsCallable(fn) {
		t.Fatalf("ctx 缺少方法 %s", name)
	}
	callScriptFn(fn, args...)
}

// ctxNum 读一次 ctx 上的数值属性 (width / height)。
func ctxNum(t *testing.T, ctx *object.Object, name string) float64 {
	t.Helper()
	v, ok := ctx.GetProperty(name)
	if !ok {
		t.Fatalf("ctx 缺少属性 %s", name)
	}
	n, ok := v.(*object.Number)
	if !ok {
		t.Fatalf("ctx.%s 不是数字, 实际 %s", name, v.Type())
	}
	return n.Value
}

// canvasWith 造一个 canvas 节点, onDraw 用 Go 侧 builtin 实现。
//
// 不走 wireProp: 纯 Go 环境下没有 VM, 也就没有 createEffect 可包 —— 这里只
// 验绘制, 响应式由全链路用例覆盖。
func canvasWith(t *testing.T, w, h int, draw func(ctx *object.Object)) *GuiNode {
	t.Helper()
	n := mkNode("canvas", map[string]float64{"width": float64(w), "height": float64(h)})
	n.onDraw = object.NewBuiltin("onDraw", func(args ...object.Value) object.Value {
		if len(args) == 0 {
			return object.UndefinedSingleton
		}
		ctx, ok := args[0].(*object.Object)
		if !ok {
			return object.UndefinedSingleton
		}
		draw(ctx)
		return object.UndefinedSingleton
	})
	return n
}

func mustColor(t *testing.T, s string) color.RGBA {
	t.Helper()
	c, ok := ParseColor(s)
	if !ok {
		t.Fatalf("颜色 %q 解析失败", s)
	}
	return c
}

// TestCanvasDrawPixels 六个原语都要真的落笔, 且落在**画布局部坐标**上。
func TestCanvasDrawPixels(t *testing.T) {
	const cw, ch = 60, 40
	root := mkNode("column", nil)
	c := canvasWith(t, cw, ch, func(ctx *object.Object) {
		// 尺寸属性必须在真 ctx 上读得到: 脚本的柱状图布局全靠它
		if got := ctxNum(t, ctx, "width"); got != cw {
			t.Fatalf("ctx.width = %v, want %d", got, cw)
		}
		if got := ctxNum(t, ctx, "height"); got != ch {
			t.Fatalf("ctx.height = %v, want %d", got, ch)
		}
		ctxCall(t, ctx, "fillRect", cn(0), cn(0), cn(cw), cn(ch), cs("#f0f0f0"))
		ctxCall(t, ctx, "fillRect", cn(5), cn(5), cn(10), cn(10), cs("#c0392b"))
		ctxCall(t, ctx, "fillCircle", cn(40), cn(20), cn(6), cs("#2ecc71"))
		ctxCall(t, ctx, "line", cn(0), cn(30), cn(59), cn(30), cs("#333333"))
		ctxCall(t, ctx, "strokeRect", cn(0), cn(0), cn(cw), cn(ch), cs("#000000"))
		ctxCall(t, ctx, "drawText", cs("A"), cn(2), cn(20), cn(12), cs("#000000"))
	})
	root.Children = []*GuiNode{c}

	img := renderTree(root, 120, 80)
	if c.Box != (Rect{X: 0, Y: 0, W: cw, H: ch}) {
		t.Fatalf("canvas Box = %v, want 0,0,%d,%d", c.Box, cw, ch)
	}
	assertPx(t, img, 5, 5, mustColor(t, "#c0392b"), "fillRect 左上角")
	assertPx(t, img, 14, 14, mustColor(t, "#c0392b"), "fillRect 右下角 (10x10 含端点)")
	assertPx(t, img, 40, 20, mustColor(t, "#2ecc71"), "fillCircle 圆心")
	assertPx(t, img, 30, 30, mustColor(t, "#333333"), "line 沿线")
	assertPx(t, img, 20, 8, mustColor(t, "#f0f0f0"), "未被覆盖处 = 画布底色")
	assertPx(t, img, 0, 0, mustColor(t, "#000000"), "strokeRect 左上角")
	assertPx(t, img, cw-1, ch/2, mustColor(t, "#000000"), "strokeRect 右边缘")
	// 文本是抗锯齿的: 断言"这块区域确实变暗了", 不断言某个具体色值
	if l := minLuma(img, Rect{X: 2, Y: 20, W: 14, H: 16}); l > 200 {
		t.Fatalf("drawText 未落笔 (最暗亮度 %d)", l)
	}
}

// TestCanvasLocalCoordsFollowBox canvas 不在原点时 ctx 的局部坐标要跟着走
// (脚本永远按"画布左上角 = (0,0)"思考, 不该关心画布被摆在哪)。
func TestCanvasLocalCoordsFollowBox(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 13})
	c := canvasWith(t, 20, 20, func(ctx *object.Object) {
		ctxCall(t, ctx, "fillRect", cn(0), cn(0), cn(20), cn(20), cs("#e67e22"))
	})
	root.Children = []*GuiNode{c}

	img := renderTree(root, 100, 80)
	if c.Box.X != 13 || c.Box.Y != 13 {
		t.Fatalf("canvas Box = %v, want 起点 (13,13)", c.Box)
	}
	orange := mustColor(t, "#e67e22")
	assertPx(t, img, 13, 13, orange, "局部 (0,0) 应落在 Box 左上角")
	assertPx(t, img, 32, 32, orange, "局部 (19,19) 应落在 Box 右下角")
	assertPx(t, img, 12, 13, pxWhite, "Box 左侧外不得被污染")
	assertPx(t, img, 33, 20, pxWhite, "Box 右侧外不得被污染")
	assertPx(t, img, 20, 33, pxWhite, "Box 下侧外不得被污染")
}

// TestCanvasClipsToBox 越界绘制被夹在画布盒内: 画错坐标不该污染界面其它部分。
func TestCanvasClipsToBox(t *testing.T) {
	root := mkNode("row", nil)
	c := canvasWith(t, 30, 20, func(ctx *object.Object) {
		// 故意画一张远超画布的矩形 + 一个越界的圆
		ctxCall(t, ctx, "fillRect", cn(-50), cn(-50), cn(500), cn(500), cs("#c0392b"))
		ctxCall(t, ctx, "fillCircle", cn(200), cn(200), cn(30), cs("#2ecc71"))
	})
	root.Children = []*GuiNode{c}

	img := renderTree(root, 200, 100)
	red := mustColor(t, "#c0392b")
	assertPx(t, img, 0, 0, red, "盒内应被填满")
	assertPx(t, img, 29, 19, red, "盒内右下角应被填满")
	assertPx(t, img, 30, 10, pxWhite, "盒右外侧不得被污染")
	assertPx(t, img, 10, 20, pxWhite, "盒下外侧不得被污染")
}

// TestCanvasDefaultSize 不给 width/height 时要有缺省实体尺寸。
//
// 这条不是顺手加的: canvas 不是容器 (stretchesCross 为 false), 拿不到父容器
// 的交叉轴拉伸。缺省值若落成 0, 整块画布会被 drawNode 整支跳过 —— 现象是
// "onDraw 明明在跑, 屏幕上什么都没有", 最难排查的一类"没反应"。
func TestCanvasDefaultSize(t *testing.T) {
	root := mkNode("column", nil)
	c := mkNode("canvas", nil)
	root.Children = []*GuiNode{c}

	Layout(root, 400, 300)
	if c.Box.W != canvasDefW || c.Box.H != canvasDefH {
		t.Fatalf("缺省尺寸 = %dx%d, want %dx%d", c.Box.W, c.Box.H, canvasDefW, canvasDefH)
	}
}

// TestCanvasDisabledTint 禁用态 (自身或祖先) 要把每个落笔色降饱和。
func TestCanvasDisabledTint(t *testing.T) {
	draw := func(ctx *object.Object) {
		ctxCall(t, ctx, "fillRect", cn(0), cn(0), cn(30), cn(20), cs("#c0392b"))
	}

	on := mkNode("column", nil)
	on.Children = []*GuiNode{canvasWith(t, 30, 20, draw)}
	onImg := renderTree(on, 60, 40)

	off := mkNode("column", nil)
	c2 := canvasWith(t, 30, 20, draw)
	withBool(c2, "disabled", true)
	off.Children = []*GuiNode{c2}
	offImg := renderTree(off, 60, 40)

	if onImg.RGBAAt(10, 10) == offImg.RGBAAt(10, 10) {
		t.Fatalf("disabled 的 canvas 应与启用态不同, 都是 %v", onImg.RGBAAt(10, 10))
	}
}

// TestCanvasChromeWithoutOnDraw 没有 onDraw 时 background/border 仍然生效。
//
// 守的是 drawNode 的分派结构: canvas 被自己的 case 截走了, 走不到 default
// 分支的"通用盒子"绘制。不在 paintCanvas 里补这一下,
// `<canvas background="#fff">` 就会被**静默忽略**。
func TestCanvasChromeWithoutOnDraw(t *testing.T) {
	root := mkNode("column", nil)
	c := mkNode("canvas", map[string]float64{"width": 30, "height": 20})
	withStr(c, "background", "#123456")
	withStr(c, "border", "#000000")
	root.Children = []*GuiNode{c}

	img := renderTree(root, 60, 40)
	assertPx(t, img, 10, 10, mustColor(t, "#123456"), "canvas 底色")
	assertPx(t, img, 0, 0, mustColor(t, "#000000"), "canvas 边框")
}

// TestCanvasArgParsing 参数解析刻意宽松: 缺参不炸、数字当灰度、非法色回落。
//
// 一个 undefined 参数不该让整帧渲染中断 —— "画歪了"立刻看得见, 而抛异常
// 会把后面所有节点的绘制一起带走, 定位成本高得多。
func TestCanvasArgParsing(t *testing.T) {
	root := mkNode("column", nil)
	c := canvasWith(t, 30, 20, func(ctx *object.Object) {
		ctxCall(t, ctx, "fillRect", cn(0), cn(0), cn(30), cn(20), cs("#ffffff"))
		ctxCall(t, ctx, "fillRect", cn(2), cn(2), cn(4), cn(4))              // 缺颜色 → 缺省黑
		ctxCall(t, ctx, "fillRect", cn(10), cn(2), cn(4), cn(4), cn(128))    // 数字 → 灰度
		ctxCall(t, ctx, "fillRect", cn(18), cn(2), cn(4), cn(4), cs("nope")) // 非法色 → 缺省黑
		ctxCall(t, ctx, "line", cn(0))                                       // 参数不足 → 当 0
		ctxCall(t, ctx, "fillCircle")                                        // 全缺 → 半径 0 直接返回
		ctxCall(t, ctx, "strokeCircle")
		ctxCall(t, ctx, "strokeRect")
		ctxCall(t, ctx, "drawText")
		ctxCall(t, ctx, "clear") // 缺参 → 缺省黑铺满整盒
	})
	root.Children = []*GuiNode{c}

	img := renderTree(root, 60, 40)
	assertPx(t, img, 1, 1, canvasDefaultInk, "clear() 缺参时用缺省黑铺满")
	assertPx(t, img, 29, 19, canvasDefaultInk, "clear() 应铺满整个画布盒")
}

// TestCanvasArgCoercion 颜色/数值参数的各条转换分支。
func TestCanvasArgCoercion(t *testing.T) {
	if c := canvasColor([]object.Value{cn(128)}, 0); c != (color.RGBA{R: 128, G: 128, B: 128, A: 255}) {
		t.Fatalf("数字 128 应为中灰, 实际 %v", c)
	}
	if c := canvasColor([]object.Value{cn(-5)}, 0); c != (color.RGBA{A: 255}) {
		t.Fatalf("负数灰度应钳到 0, 实际 %v", c)
	}
	if c := canvasColor([]object.Value{cn(999)}, 0); c != (color.RGBA{R: 255, G: 255, B: 255, A: 255}) {
		t.Fatalf("超界灰度应钳到 255, 实际 %v", c)
	}
	if c := canvasColor(nil, 3); c != canvasDefaultInk {
		t.Fatalf("缺参应回落缺省黑, 实际 %v", c)
	}
	if c := canvasColor([]object.Value{cs("nope")}, 0); c != canvasDefaultInk {
		t.Fatalf("非法颜色串应回落缺省黑, 实际 %v", c)
	}
	if c := canvasColor([]object.Value{cs("red")}, 0); c != namedColors["red"] {
		t.Fatalf("命名色应走 ParseColor, 实际 %v", c)
	}
	if got := canvasNum([]object.Value{cn(7.9)}, 0); got != 7 {
		t.Fatalf("canvasNum(7.9) = %d, want 7", got)
	}
	if got := canvasNum([]object.Value{object.NewBoolean(true)}, 0); got != 1 {
		t.Fatalf("canvasNum(true) = %d, want 1", got)
	}
	if got := canvasNum([]object.Value{cs("3")}, 0); got != 0 {
		t.Fatalf("canvasNum(\"3\") = %d, want 0 (非数值一律 0, 不报错)", got)
	}
	if got := canvasNum(nil, 0); got != 0 {
		t.Fatalf("canvasNum(缺参) = %d, want 0", got)
	}
	if got := canvasStr([]object.Value{cn(42)}, 0); got != "42" {
		t.Fatalf("canvasStr(42) = %q, want \"42\"", got)
	}
	if got := canvasStr(nil, 0); got != "" {
		t.Fatalf("canvasStr(缺参) = %q, want \"\"", got)
	}
}

// TestCanvasCtxMethodParity 空操作 ctx 与真 ctx 的方法表/属性必须一致。
//
// 依赖收集阶段用的是空操作 ctx。两边一旦漂开, 症状是"挂载那一帧画得出来,
// 但改 signal 就报 ctx.xxx is not a function" —— 极难定位。这里逐个名字比对。
func TestCanvasCtxMethodParity(t *testing.T) {
	names := []string{"fillRect", "strokeRect", "fillCircle", "strokeCircle", "line", "drawText", "clear"}
	box := Rect{X: 1, Y: 2, W: 30, H: 40}
	noop := makeNoopCtx(box)
	sub := image.NewRGBA(image.Rect(1, 2, 31, 42))
	live := makeDrawCtx(sub, box, false)

	for _, name := range names {
		if fn, ok := noop.GetProperty(name); !ok || !object.IsCallable(fn) {
			t.Fatalf("空操作 ctx 缺少方法 %s", name)
		}
		if fn, ok := live.GetProperty(name); !ok || !object.IsCallable(fn) {
			t.Fatalf("真 ctx 缺少方法 %s", name)
		}
	}
	for ctx, what := range map[*object.Object]string{noop: "空操作", live: "真"} {
		v, ok := ctx.GetProperty("width")
		if !ok {
			t.Fatalf("%s ctx 缺少 width 属性", what)
		}
		n, ok := v.(*object.Number)
		if !ok || n.Value != 30 {
			t.Fatalf("%s ctx 的 width = %v, want 30", what, v)
		}
		v, ok = ctx.GetProperty("height")
		if !ok {
			t.Fatalf("%s ctx 缺少 height 属性", what)
		}
		if n, ok := v.(*object.Number); !ok || n.Value != 40 {
			t.Fatalf("%s ctx 的 height = %v, want 40", what, v)
		}
	}
}

// ===== 响应式 (全链路) =====

// evalCanvasUI 基于 evalUI (helpers_test.go) 挂载脚本, 额外摘除窗口注册:
// canvas 用例不总走 EventClose 收尾, 注册表 (包级状态) 里遗留的假窗口
// 会让下一个用例的 Pump 等一个再也不会来事件的 Surface。
func evalCanvasUI(t *testing.T, src string) (*vm.VM, *GuiNode, *fakeSurface) {
	t.Helper()
	v, fake := evalUI(t, src)
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatalf("脚本未挂载窗口")
	}
	t.Cleanup(func() {
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
	})
	return v, a.root, fake
}

// TestCanvasSignalRedraw onDraw 里读到的 signal 变化 → 画布重绘。
func TestCanvasSignalRedraw(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, root, fake := evalCanvasUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [on, setOn] = createSignal(false);
		render(
			h("column", null,
				h("canvas", {
					width: 40, height: 20,
					onDraw: (ctx) => {
						ctx.fillRect(0, 0, ctx.width, ctx.height, on() ? "#c0392b" : "#f0f0f0");
					},
				})
			),
			{ title: "canvas", width: 200, height: 120 }
		);
	`)

	c := findFirst(root, "canvas")
	if c == nil {
		t.Fatalf("没有挂上 canvas 节点")
	}
	if c.onDraw == nil {
		t.Fatalf("onDraw 未接线 (wireProp 里的 onDraw 特判没生效?)")
	}
	// 挂载期就应该画过一帧
	first := shotsImage(fake)
	if first == nil {
		t.Fatalf("首帧未上屏")
	}
	assertPx(t, first, c.Box.X+10, c.Box.Y+10, mustColor(t, "#f0f0f0"), "初帧底色")

	// 在事件循环里改 signal (主脚本结束后 currentVM 为 nil, 循环外改不了),
	// 同一轮再推一个无害事件, 让本轮走完重绘。
	round := 0
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			fake.push(Event{Kind: EventMouseLeave})
		case 2:
			callGlobal(t, v, "setOn", object.NewBoolean(true))
			fake.push(Event{Kind: EventMouseLeave})
		default:
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	img := shotsImage(fake)
	if img == nil {
		t.Fatalf("没有捕获到帧")
	}
	assertPx(t, img, c.Box.X+10, c.Box.Y+10, mustColor(t, "#c0392b"), "signal 变化后应重绘成红")
}

// TestCanvasNoDrawDepsNoSpin onDraw 不读 signal 时, signal 变化不该让它标脏。
//
// 断言点在 **dirtyNodes** 而不是像素: 像素会被"这一帧是整帧重绘还是局部重绘"
// 干扰 (整帧重绘时所有画布都会被重画一遍, 与依赖追踪无关)。dirtyNodes 是
// markNodeDirty 的落点, 恰好就是"effect 到底有没有跑"的直接证据。
//
// 用兄弟画布当对照: 它确实被标脏了, 证明这一轮 tick 真的发生过 —— 否则
// "没标脏"也可能只是什么都没跑。
func TestCanvasNoDrawDepsNoSpin(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	v, root, fake := evalCanvasUI(t, `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [on, setOn] = createSignal(false);
		let plain = 0;   // 普通变量: onDraw 读它不会产生依赖
		const bump = () => { plain = 1; setOn(true); };
		render(
			h("column", null,
				h("canvas", {width: 20, height: 20,
					onDraw: (ctx) => { ctx.fillRect(0, 0, 20, 20, on() ? "#c0392b" : "#f0f0f0"); }}),
				h("canvas", {width: 20, height: 20,
					onDraw: (ctx) => { ctx.fillRect(0, 0, 20, 20, plain ? "#2ecc71" : "#f0f0f0"); }})
			),
			{ title: "canvas", width: 200, height: 120 }
		);
	`)

	cvs := findAll(root, "canvas")
	if len(cvs) != 2 {
		t.Fatalf("应有两个 canvas, 实际 %d", len(cvs))
	}
	reactive, inert := cvs[0], cvs[1]

	var reactiveDirty, inertDirty, probed bool
	round := 0
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			fake.push(Event{Kind: EventMouseLeave})
		case 2:
			callGlobal(t, v, "bump")
			// 就在这一轮、重绘之前读脏节点集合 (重绘会把 dirtyNodes 清空)
			appMu.Lock()
			a := activeApp
			appMu.Unlock()
			a.mu.Lock()
			_, reactiveDirty = a.dirtyNodes[reactive]
			_, inertDirty = a.dirtyNodes[inert]
			a.mu.Unlock()
			probed = true
			fake.push(Event{Kind: EventMouseLeave})
		default:
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if !probed {
		t.Fatalf("第二轮没有跑到 (事件循环提前退出)")
	}
	if !reactiveDirty {
		t.Fatalf("读 signal 的画布未被标脏: signal 变化没有触发重绘")
	}
	if inertDirty {
		t.Fatalf("不读 signal 的画布被标脏了: onDraw 产生了无谓的标脏 (effect 依赖收集有问题)")
	}
}
