package gfx

import (
	"image"
	"math"

	"github.com/14752222/Gox/object"
)

// 滑块组件 `<slider min max step value onInput>` (P2-8)。
//
// 受控语义与 input / textarea 一致: `value` 决定滑块位置, 交互只派发
// `onInput({value})`, **不回写 value 滑块就不会动**。
//
// 三个与"普通控件"不同的地方:
//
//  1. **按下即锁定为拖动目标** (`app.dragTarget`): 一次拖动是一串
//     Down → Move… → Up, 中间可能滑过别的控件。v1 的做法是拖动期间
//     MouseMove 全部路由给拖动目标, 不维护悬停链 (见 handleMouseMove) ——
//     否则拖动时鼠标划过谁, 谁就闪一下高亮。
//  2. **点击轨道直接跳值**, 不必"先按下再拖" (与浏览器 `<input type=range>`
//     一致: 点哪跳哪)。所以 Down 与 Move 走的是同一条 `sliderDrag`。
//  3. **鼠标捕获** (可选能力): 拖出窗口后仍然跟手, 靠后端实现
//     `CapturePointer`/`ReleasePointer`; 后端没实现就退化成"拖出窗口即停"
//     (见 app.hasPointerCapture 的说明)。

// slider 的缺省几何。轨道只占盒子中间的一条, 滑块比轨道高 —— 与 switch
// 的思路一致 (盒子尺寸由布局决定, 视觉在盒内居中铺开)。
const (
	sliderDefW   = 160 // 缺省宽
	sliderDefH   = 24  // 缺省高
	sliderTrackH = 4   // 轨道高
	sliderKnobW  = 12  // 滑块宽
	sliderKnobH  = 12  // 滑块高
)

// sliderInChain 找祖先链上最近的 slider (命中的可能是它的子节点)。
func sliderInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "slider" {
			return p
		}
	}
	return nil
}

// ===== 取值 =====

// sliderProp 读数值属性, 缺失/非数值/NaN/Inf 一律回落 def。
//
// 拦 NaN/Inf 是有必要的: 脚本里 `max={a/b}` 一旦 b 为 0 就是 Infinity,
// 后面所有几何换算都会变成 NaN 像素 —— 表现是"滑块不见了", 而不是报错。
func sliderProp(n *GuiNode, name string, def float64) float64 {
	v, ok := n.PropNum(name)
	if !ok || math.IsNaN(v) || math.IsInf(v, 0) {
		return def
	}
	return v
}

// sliderMin / sliderMax / sliderStep 读取三个量程属性 (缺省 0 / 100 / 1)。
func sliderMin(n *GuiNode) float64  { return sliderProp(n, "min", 0) }
func sliderMax(n *GuiNode) float64  { return sliderProp(n, "max", 100) }
func sliderStep(n *GuiNode) float64 { return sliderProp(n, "step", 1) }

// sliderValue 返回当前受控值的**量化结果**。
//
// 量化在读取时做 (而不是只在对齐拖动时做): 脚本直接写 value={7.3} 也要
// 表现成"对齐到格子上的值", 否则同一个滑块会出现"拖动后对齐、手写值不对齐"
// 两套坐标, 滑块位置与读出的数就对不上了。
func sliderValue(n *GuiNode) float64 {
	min, max := sliderMin(n), sliderMax(n)
	return sliderQuantize(sliderProp(n, "value", min), min, max, sliderStep(n))
}

// sliderQuantize 把 v 钳到 [min,max] 并按 step 对齐 (相对 min 的整数倍)。
//
// step <= 0 或 NaN 表示"连续"(不对齐), 只做钳位。
// **max < min 时量程塌缩成 [min,min]** (与 HTML `<input type=range>` 的语义一致):
// 这样"写反了量程"只会得到一个不动的滑块, 而不是交换出一段没人预期的区间 ——
// 后者更难排查, 因为界面上看起来是"能拖, 但范围不对"。
// 末尾多一次 1e-6 取整是为了消掉 0.1*3 = 0.30000000000000004 这类浮点噪声 ——
// 滑块的值经常直接显示给用户, 拖出一串小数位会很难看。
func sliderQuantize(v, min, max, step float64) float64 {
	if math.IsNaN(min) {
		min = 0
	}
	if math.IsNaN(max) || max < min {
		max = min
	}
	if math.IsNaN(v) {
		v = min
	}
	// 顺序是"先量化、再钳位", 不是反过来: 反过来会让"拖到最右端"这种
	// 明确意图被吃掉 —— v=99 先钳成 10, 再按 step=100 对齐就落回 0 (最左),
	// 手感上就是"拖到头反而跳回起点"。
	if step > 0 && !math.IsInf(step, 0) {
		v = min + math.Round((v-min)/step)*step
	}
	if v < min {
		v = min
	}
	if v > max {
		v = max
	}
	return math.Round(v*1e6) / 1e6
}

// sliderRatio 把当前值映射成 [0,1] 的比例 (量程退化时恒为 0, 即停在 min 那端)。
func sliderRatio(n *GuiNode) float64 {
	min, max := sliderMin(n), sliderMax(n)
	if max <= min {
		return 0
	}
	r := (sliderValue(n) - min) / (max - min)
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	return r
}

// sliderKnobLeft 返回滑块左边缘的屏幕 x。
//
// 滑块中心在轨道两端各内缩半个滑块宽 (而不是滑块贴到盒子边上再露一半):
// 位置越界会让滑块看起来"掉出"了控件, 命中区也跟着偏。
func sliderKnobLeft(n *GuiNode, knobW int) int {
	travel := n.Box.W - knobW
	if travel < 0 {
		travel = 0
	}
	return n.Box.X + int(sliderRatio(n)*float64(travel)+0.5)
}

// sliderValueFromX 把轨道上的一点反算成量化后的值 (点击跳值 / 拖动都走它)。
func sliderValueFromX(n *GuiNode, x int) float64 {
	b := n.Box
	knobW := sliderKnobW
	if knobW > b.W {
		knobW = b.W
	}
	travel := b.W - knobW
	if travel <= 0 {
		return sliderQuantize(sliderMin(n), sliderMin(n), sliderMax(n), sliderStep(n))
	}
	// 以滑块中心为参照: 光标在轨道最左/最右时正好对应 min/max
	r := (float64(x-b.X) - float64(knobW)/2) / float64(travel)
	if r < 0 {
		r = 0
	}
	if r > 1 {
		r = 1
	}
	min, max := sliderMin(n), sliderMax(n)
	return sliderQuantize(min+r*(max-min), min, max, sliderStep(n))
}

// ===== 绘制 =====

// paintSlider 画轨道 + 已填充段 + 滑块。
//
// 已填充段从轨道左端画到**滑块中心**: 这样 min 时填充宽度为半个滑块宽 (不外溢),
// max 时正好铺满整条轨道末端 —— 与浏览器 range 的观感一致。
func paintSlider(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	knobW, knobH := sliderKnobW, sliderKnobH
	if knobW > b.W {
		knobW = b.W
	}
	if knobH > b.H {
		knobH = b.H
	}
	trackH := sliderTrackH
	if trackH > b.H {
		trackH = b.H
	}
	ty := b.Y + (b.H-trackH)/2
	FillRect(img, Rect{X: b.X, Y: ty, W: b.W, H: trackH}, tint(colorTrack, disabled))

	knobLeft := sliderKnobLeft(n, knobW)
	if fillW := knobLeft + knobW/2 - b.X; fillW > 0 {
		FillRect(img, Rect{X: b.X, Y: ty, W: fillW, H: trackH},
			tint(n.faceColor(colorAccent), disabled))
	}

	knob := Rect{X: knobLeft, Y: b.Y + (b.H-knobH)/2, W: knobW, H: knobH}
	// 滑块是白底: 用 fieldFace (而非 faceColor 的提亮) 才能表达出悬停/按压 ——
	// 白色提亮再提也没变化, fieldFace 走的是"轻微染色"那条路。
	FillRect(img, knob, tint(n.fieldFace(colorKnob), disabled))
	StrokeRect(img, knob, tint(colorFieldEdge, disabled))
}

// ===== 交互 =====

// sliderEdited 派发 onInput({value}) (受控回写入口)。
//
// value 用 Number 而不是字符串: 滑块的语义是数值, 让脚本每次自己 parseFloat
// 一个字符串属于无谓的负担 (input/textarea 是文本, 所以那边是字符串)。
func (a *app) sliderEdited(n *GuiNode, v float64) {
	markNodeDirty(n)
	if n.PropHandler("onInput") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewNumber(v))
	a.callHandler(n, "onInput", arg)
}

// sliderDrag 把轨道坐标 x 应用到滑块上: 值变了才派发 onInput 并标脏。
//
// "值没变就不派发"这条不是省事: 鼠标每移动一像素都会来一次 MouseMove,
// 不挡的话一次拖动会给脚本灌几百次调用, 而且绝大多数是同一个量化值。
// slideVal 记录的是**上一次派发出去的值** (而不是当前 prop) —— 脚本不回写
// value 时 prop 永远不变, 拿 prop 比较等于没挡。
func (a *app) sliderDrag(n *GuiNode, x int) {
	v := sliderValueFromX(n, x)
	if n.slideValSet && v == n.slideVal {
		return
	}
	n.slideVal = v
	n.slideValSet = true
	a.sliderEdited(n, v)
}

// dragMove 把一次鼠标移动喂给拖动目标。v1 只有 slider 会拖。
func (a *app) dragMove(n *GuiNode, x, y int) {
	if n == nil {
		return
	}
	if n.Tag == "slider" {
		a.sliderDrag(n, x)
	}
}
