package gfx

import (
	"image"
	"image/color"

	"github.com/14752222/Gox/object"
)

// canvas 自绘组件 `<canvas onDraw>` (P3-1)。
//
// 脚本拿到一个 ctx 对象, 用它的原语把图直接画进帧缓冲:
//
//	<canvas width={200} height={80} onDraw={(ctx) => {
//	  ctx.fillRect(0, 0, ctx.width, ctx.height, "#f4f4f4");
//	  ctx.line(0, 79, 199, 79, "#ccc");
//	  ctx.fillCircle(20, 20, 8, "#e74c3c");
//	  ctx.drawText("hi", 4, 4, 13, "#333");
//	}} />
//
// 坐标一律是**画布局部坐标** (0,0 = 画布左上角), 由这里加上 Box 偏移。
// 所有绘制都被夹在画布盒内: 越界部分不落笔 (ctx 拿到的是一张以 Box 为界的
// 子图, 原语自身也走 clip)。这样脚本画错坐标不会污染画布外的界面。
//
// 响应式是怎么接上的 (两个阶段, 刻意分开):
//
//  1. **依赖收集** (挂载时 + 每次 signal 变化): wireDraw 用 createEffect 包一层,
//     在 effect 里用**空操作 ctx** 把 onDraw 跑一遍 —— 跑这一遍唯一的目的是
//     让函数体里读到的 signal 注册成依赖, 并在依赖变化时标脏。
//  2. **真正绘制** (drawNode 里): paintCanvas 用**真 ctx** 再跑一遍, 这次才落笔。
//
// 为什么不在 effect 里直接画: effect 可能在「还没有帧缓冲」「布局尚未跑」时
// 触发, 而绘制必须发生在某个具体的脏矩形子图上。把绘制留在绘制阶段,
// 职责单一, 也不会出现「同一次变化画两遍」。
//
// 代价: onDraw 每次依赖变化会执行两遍 (一遍空跑收依赖, 一遍真画)。v1 接受 ——
// onDraw 应当是纯绘制函数, 不应有副作用。**推论**: 别在 onDraw 里靠
// `ctx.width` 去决定要不要读某个 signal (依赖收集时 Box 可能还是 0),
// 把 signal 读取放在函数体前部最稳。

// canvas 缺省尺寸。一个不写 width/height 的 canvas 必须有可见的实体,
// 否则 0 尺寸子树会被 drawNode 整支跳过, 界面上什么都看不到。
const (
	canvasDefW = 200
	canvasDefH = 120
)

// canvasDefaultInk 是未给颜色 (或颜色解析失败) 时的落笔色, 与 HTML canvas
// 的 fillStyle 缺省值一致。
var canvasDefaultInk = color.RGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xFF}

// ===== ctx 生命周期 =====

// canvasTarget 是一次 ctx 的落笔目标。
//
// **img == nil 表示"空操作"**: 依赖收集阶段用同一个类型、同一套方法实现,
// 仅仅是所有落笔直接返回。共用一份实现是有意为之 —— 如果给空操作另写一套
// 方法表, 两边的方法名/参数顺序迟早会漂开, 症状是"依赖收集时 onDraw 报
// ctx.xxx is not a function", 而真正绘制时却好好的, 极难排查。
type canvasTarget struct {
	img      *image.RGBA // nil = 只执行不落笔 (依赖收集)
	box      Rect        // 画布在屏幕上的位置 (坐标换算 + 宽度限制)
	disabled bool        // 禁用态: 每个颜色过一遍 tint
}

// origin 返回画布局部坐标 → 屏幕坐标的偏移量。
func (t *canvasTarget) origin() (int, int) { return t.box.X, t.box.Y }

// ink 把脚本给的颜色按禁用态降饱和。
func (t *canvasTarget) ink(c color.RGBA) color.RGBA { return tint(c, t.disabled) }

// makeCtx 构造 ctx 对象。空操作与真绘制共用它, 差别只在 t.img 是否为 nil。
func makeCtx(t *canvasTarget) *object.Object {
	ctx := object.NewObject()

	// 尺寸以**属性**而不是方法暴露: 脚本里 `ctx.width` 比 `ctx.width()` 自然,
	// 也与 HTML canvas 的 `canvas.width` 一致。
	ctx.SetProperty("width", object.NewNumber(float64(t.box.W)))
	ctx.SetProperty("height", object.NewNumber(float64(t.box.H)))

	ctx.SetProperty("fillRect", object.NewBuiltin("fillRect", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		FillRect(t.img, t.rect(args), t.ink(canvasColor(args, 4)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("strokeRect", object.NewBuiltin("strokeRect", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		StrokeRect(t.img, t.rect(args), t.ink(canvasColor(args, 4)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("fillCircle", object.NewBuiltin("fillCircle", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		ox, oy := t.origin()
		FillCircle(t.img, ox+canvasNum(args, 0), oy+canvasNum(args, 1), canvasNum(args, 2),
			t.ink(canvasColor(args, 3)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("strokeCircle", object.NewBuiltin("strokeCircle", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		ox, oy := t.origin()
		StrokeCircle(t.img, ox+canvasNum(args, 0), oy+canvasNum(args, 1), canvasNum(args, 2),
			t.ink(canvasColor(args, 3)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("line", object.NewBuiltin("line", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		ox, oy := t.origin()
		// 线宽固定 1: 原语支持更粗, 但 v1 不暴露"画笔粗细"这个维度,
		// 需要粗线就多画几条相邻的 (或等 P3-2 的样式体系)。
		fillLine(t.img, ox+canvasNum(args, 0), oy+canvasNum(args, 1),
			ox+canvasNum(args, 2), oy+canvasNum(args, 3), 1, t.ink(canvasColor(args, 4)))
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("drawText", object.NewBuiltin("drawText", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		ox, oy := t.origin()
		text := canvasStr(args, 0)
		x := ox + canvasNum(args, 1)
		y := oy + canvasNum(args, 2)
		size := canvasNum(args, 3)
		c := t.ink(canvasColor(args, 4))
		// maxWidth 取"从落笔点到画布右缘的剩余宽度", 于是超长文本在画布右缘
		// 截断, 而不是溢出到画布外面去 (子图裁剪虽然也能挡住, 但让它根本
		// 不画更省事, 也避免半个字形卡在边界上)。
		avail := t.box.X + t.box.W - x
		if avail < 0 {
			avail = 0
		}
		DrawText(t.img, t.img.Bounds(), text, x, y, size, c, avail)
		return object.UndefinedSingleton
	}))

	ctx.SetProperty("clear", object.NewBuiltin("clear", func(args ...object.Value) object.Value {
		if t.img == nil {
			return object.UndefinedSingleton
		}
		FillRect(t.img, t.box, t.ink(canvasColor(args, 0)))
		return object.UndefinedSingleton
	}))

	return ctx
}

// makeNoopCtx 构造"只执行不落笔"的 ctx (依赖收集阶段用)。
func makeNoopCtx(box Rect) *object.Object {
	return makeCtx(&canvasTarget{box: box})
}

// makeDrawCtx 构造真正落笔的 ctx。sub 必须已经是**以画布盒为界的子图**。
func makeDrawCtx(sub *image.RGBA, box Rect, disabled bool) *object.Object {
	return makeCtx(&canvasTarget{img: sub, box: box, disabled: disabled})
}

// callScriptFn 从 Go 侧调用一个脚本可调用值 (args 原样作为实参)。
//
// 为什么不直接用 object.CallFunction: 那条路要经过 VM 注册的回调桥, 而桥在
// currentVM == nil 时**静默返回 undefined** —— 于是"不启动 VM 的纯 Go 嵌入"
// 调回调会什么都不发生 (onDraw 不画、onClick 不响), 脚本里却一切正常, 属于
// 最难定位的一类"没反应"。
//
// Go 侧 builtin (object.BuiltinFunction) 本身不需要 VM: 桥对它做的事也就是
// bf.Fn(args...)。这里直接执行, 把"不依赖 VM"落实到实现上。真正的 JS 闭包
// 仍然走桥 —— 它们必须有 VM 才能执行。
func callScriptFn(fn object.Value, args ...object.Value) {
	if bf, ok := fn.(*object.BuiltinFunction); ok {
		bf.Fn(args...)
		return
	}
	object.CallFunction(fn, nil, args...)
}

// rect 把前 4 个参数当成 (x, y, w, h) 的画布局部矩形换算成屏幕矩形。
func (t *canvasTarget) rect(args []object.Value) Rect {
	ox, oy := t.origin()
	return Rect{
		X: ox + canvasNum(args, 0),
		Y: oy + canvasNum(args, 1),
		W: canvasNum(args, 2),
		H: canvasNum(args, 3),
	}
}

// ===== 参数解析 =====

// canvasNum 读取第 i 个参数为整数像素。
//
// 缺失/非数值一律当 0, 不报错: 绘制函数里的一个 undefined 参数不应该让整个
// 界面渲染中断 (错误在 JS 侧更难定位, 而"画歪了"立刻看得见)。
func canvasNum(args []object.Value, i int) int {
	if i >= len(args) {
		return 0
	}
	switch v := args[i].(type) {
	case *object.Number:
		return int(v.Value)
	case *object.Boolean:
		if v.Value {
			return 1
		}
		return 0
	}
	return 0
}

// canvasStr 读取第 i 个参数为字符串 (非字符串走 ToString)。
func canvasStr(args []object.Value, i int) string {
	if i >= len(args) {
		return ""
	}
	return object.ToString(args[i])
}

// canvasColor 读取第 i 个参数为颜色。
//
// 颜色既可写字符串 ("#f00" / "red" / "rgba(0,0,0,.5)"), 也可写一个数字当灰度
// (0-255) —— 柱状图里 `ctx.fillRect(..., 200)` 比 `"#c8c8c8"` 顺手得多。
// 解析失败回落到黑色 (理由同 canvasNum: 不让一个笔误毁掉整帧)。
func canvasColor(args []object.Value, i int) color.RGBA {
	if i >= len(args) {
		return canvasDefaultInk
	}
	switch v := args[i].(type) {
	case *object.String:
		if c, ok := ParseColor(v.Value); ok {
			return c
		}
	case *object.Number:
		g := int(v.Value)
		if g < 0 {
			g = 0
		}
		if g > 255 {
			g = 255
		}
		return color.RGBA{R: uint8(g), G: uint8(g), B: uint8(g), A: 0xFF}
	}
	return canvasDefaultInk
}

// ===== 接线 (依赖收集阶段) =====

// wireDraw 接线 onDraw 回调。
//
// 与事件回调 (wireProp 里 on* 直接存 Props) 的关键差别: onDraw **不关心事件**,
// 它关心的是"函数体里读了哪些 signal" —— 所以必须包一层 effect, 否则脚本改了
// 数据画布纹丝不动 (这正是 canvas 组件最容易做漏的一步)。
//
// 真正落笔在 paintCanvas 里, 这里只负责"执行一遍以登记依赖 + 依赖变化时标脏"。
func (n *GuiNode) wireDraw(fn object.Value) {
	n.onDraw = fn
	if fn == nil || !object.IsCallable(fn) {
		return
	}
	dispose := runEffect(func() object.Value {
		n.collectDrawDeps()
		return object.UndefinedSingleton
	})
	if dispose != nil {
		n.effects = append(n.effects, dispose)
	}
}

// collectDrawDeps 用空操作 ctx 跑一遍 onDraw, 然后把本节点标脏。
//
// 标脏是必须的: effect 被唤醒说明"画的内容该变了", 而重绘要走
// Pump → DrawClipped, 不标脏这一帧就不会重画这块区域。
//
// 空操作 ctx 用当前的 Box 构造 (而不是零 Box): 依赖收集发生在 effect 重跑时,
// 那时上一帧的布局结果还在, `ctx.width` 之类的读取能拿到合理值。
func (n *GuiNode) collectDrawDeps() {
	if n.onDraw == nil || !object.IsCallable(n.onDraw) {
		return
	}
	callScriptFn(n.onDraw, makeNoopCtx(n.Box))
	markNodeDirty(n)
}

// ===== 绘制 (落笔阶段) =====

// paintCanvas 绘制 canvas 组件: 把 onDraw 用一个真 ctx 跑一遍。
func paintCanvas(img *image.RGBA, n *GuiNode, disabled bool) {
	if n.Box.W <= 0 || n.Box.H <= 0 {
		return
	}
	if n.onDraw == nil || !object.IsCallable(n.onDraw) {
		// 没给 onDraw: 只保留 background / border (下面照画), 不报错 ——
		// canvas 先挂着、回调后补上是一种合理的写法。
		paintCanvasChrome(img, n, disabled)
		return
	}
	paintCanvasChrome(img, n, disabled)
	// 画布本身不铺底 (与 HTML canvas 的透明语义一致), 子图直接盖在背景上。
	sub := clipTo(img, n.Box)
	callScriptFn(n.onDraw, makeDrawCtx(sub, n.Box, disabled))
}

// paintCanvasChrome 画 canvas 自己的底色与边框。
//
// drawNode 的 default 分支 (通用盒子) 管不到 canvas —— 它被 canvas 的 case
// 截走了。所以 background/border 这两个"所有节点都该认"的属性必须在这里补上,
// 否则 `<canvas background="#fff">` 会被**静默忽略**, 只能靠 ctx.clear 兜。
func paintCanvasChrome(img *image.RGBA, n *GuiNode, disabled bool) {
	if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(bg, disabled))
	}
	if bd, ok := n.borderFor(); ok {
		StrokeRect(img, n.Box, tint(bd, disabled))
	}
}
