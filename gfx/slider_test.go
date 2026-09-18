package gfx

import (
	"math"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P2-8: slider 滑块 =====
//
// 分两层验:
//   - 纯函数 (量化/反算/几何) —— 滑块最容易错的地方是"轨道坐标 ↔ 值"的换算,
//     而它是纯数学, 不需要界面就能钉死。
//   - 全链路 (真 VM + 真事件序列) —— 拖动是一串 Down→Move…→Up, 只有走完
//     整条链路才能验到"拖动期间路由给谁、松手后有没有清干净、重复值有没有挡掉"。

// pumpEvents 造一个"每轮推一个事件再 Pump"的泵。
//
// 与 pushAndPump 的区别: 这个走 v.RunTimersWithPump, 事件循环期间 currentVM
// 是注册好的, 脚本回调 (以及回调里的 signal setter) 才真的会执行。只调
// a.pump 的话回调会被回调桥静默丢掉 —— 断言"onInput 被调用了几次"必须用它。
//
// 假 Surface 的 events/arrived 通道各只有 16 格且每轮 WaitEvents 只消费一个
// 唤醒信号, 一次推多个事件会直接 chan send 死锁 (整个包挂住, 无 panic 无输出)。
func pumpEvents(t *testing.T, v *vm.VM, a *app, evs ...Event) {
	t.Helper()
	a.mu.Lock()
	fake, ok := a.surface.(*fakeSurface)
	a.mu.Unlock()
	if !ok {
		t.Fatalf("测试用的 app 不是假 Surface, 无法注入事件")
	}
	i := 0
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		if i >= len(evs) {
			return false
		}
		fake.push(evs[i])
		i++
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if i != len(evs) {
		t.Fatalf("只推送了 %d/%d 个事件", i, len(evs))
	}
}

// jsArrayLen 读全局数组的长度 (测试里用来数回调次数)。
func jsArrayLen(t *testing.T, v *vm.VM, name string) int {
	t.Helper()
	return len(jsArray(t, v, name).Elements)
}

// jsArray 取全局数组。
func jsArray(t *testing.T, v *vm.VM, name string) *object.Array {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	arr, ok := val.(*object.Array)
	if !ok {
		t.Fatalf("全局 %s 不是数组, 实际 %s", name, val.Type())
	}
	return arr
}

// jsNumAt 取全局数组里第 i 个元素 (数值)。
func jsNumAt(t *testing.T, v *vm.VM, name string, i int) float64 {
	t.Helper()
	arr := jsArray(t, v, name)
	if i >= len(arr.Elements) || i < 0 {
		t.Fatalf("全局 %s 只有 %d 个元素, 取不到第 %d 个", name, len(arr.Elements), i)
	}
	n, ok := arr.Elements[i].(*object.Number)
	if !ok {
		t.Fatalf("全局 %s[%d] 不是数字, 实际 %s", name, i, arr.Elements[i].Type())
	}
	return n.Value
}

// sliderApp 起一个"value 受控 + 记录每次 onInput"的滑块, 并返回 (VM, 根, app, 滑块)。
func sliderApp(t *testing.T, extra string) (*vm.VM, *GuiNode, *app, *GuiNode) {
	t.Helper()
	v, root, a := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [val, setVal] = createSignal(0);
		const seen = [];
		const s = h("slider", {
			width: 160, height: 24,
			min: 0, max: 100, step: 1,
			value: () => val(),
			onInput: (e) => { seen.push(e.value); setVal(e.value); },
		});
		render(h("column", null, s), window({ title: "slider", width: 300, height: 120 }));
		`+extra)
	sl := findFirst(root, "slider")
	if sl == nil {
		t.Fatalf("没有挂上 slider 节点")
	}
	return v, root, a, sl
}

// TestSliderQuantize 量化: 对齐到 step、钳位、退化量程、浮点噪声。
func TestSliderQuantize(t *testing.T) {
	cases := []struct {
		v, min, max, step, want float64
	}{
		{12, 0, 100, 5, 10},   // 就近取整: 12 → 10
		{13, 0, 100, 5, 15},   // 13 → 15
		{12.5, 0, 100, 5, 15}, // 正中间: math.Round 半值向上
		{-7, 0, 100, 5, 0},    // 下越界钳到 min
		{999, 0, 100, 5, 100}, // 上越界钳到 max
		{50, 0, 100, 0, 50},   // step<=0 = 连续, 只钳位
		{123, 0, 100, 0, 100}, // 连续也要钳位
		{3, 10, 0, 5, 10},     // max<min → 量程塌缩成 [min,min], 取值恒为 min
		{0.1, 0, 1, 0.1, 0.1}, // 浮点: 不能出现 0.30000000000000004
		{0.3, 0, 1, 0.1, 0.3},
		{7, 0, 10, 100, 0},   // step 比量程还大: 只能落在 min 或 max
		{99, 0, 10, 100, 10}, //
	}
	for _, c := range cases {
		if got := sliderQuantize(c.v, c.min, c.max, c.step); got != c.want {
			t.Fatalf("quantize(%v, %v, %v, %v) = %v, want %v", c.v, c.min, c.max, c.step, got, c.want)
		}
	}
	if got := sliderQuantize(math.NaN(), 0, 100, 1); got != 0 {
		t.Fatalf("NaN 应回落 min, 实际 %v", got)
	}
}

// TestSliderValueFromX 轨道坐标 → 值 (含滑块半宽的内缩)。
func TestSliderValueFromX(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "min": 0, "max": 100, "step": 1})
	root.Children = []*GuiNode{sl}
	Layout(root, 300, 120)
	if sl.Box != (Rect{X: 0, Y: 0, W: 160, H: 24}) {
		t.Fatalf("slider Box = %v", sl.Box)
	}

	cases := []struct {
		x    int
		want float64
	}{
		{-50, 0},   // 轨道左侧之外 → min
		{6, 0},     // 滑块中心在最左时 = min
		{43, 25},   // (43-6)/148 = 0.25
		{80, 50},   // 正中
		{154, 100}, // 滑块中心在最右时 = max
		{999, 100}, // 轨道右侧之外 → max
	}
	for _, c := range cases {
		if got := sliderValueFromX(sl, c.x); got != c.want {
			t.Fatalf("valueFromX(%d) = %v, want %v", c.x, got, c.want)
		}
	}
}

// TestSliderKnobRoundTrip 位置与值是互逆的: 按值算出滑块左缘, 再从该位置反算,
// 应当得到同一个值。两条换算一旦有一条写歪, 现象就是"拖到哪都不对"。
func TestSliderKnobRoundTrip(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "min": 0, "max": 100, "step": 1})
	root.Children = []*GuiNode{sl}
	Layout(root, 300, 120)

	for _, want := range []float64{0, 25, 50, 75, 100} {
		withNum(sl, "value", want)
		left := sliderKnobLeft(sl, sliderKnobW)
		// 滑块中心所在的那一点
		got := sliderValueFromX(sl, left+sliderKnobW/2)
		if got != want {
			t.Fatalf("value=%v → knobLeft=%d → 反算 %v", want, left, got)
		}
	}
}

// TestSliderDegenerateRange 量程退化 (max<=min) 不能出 NaN / 除零。
func TestSliderDegenerateRange(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "min": 5, "max": 5, "value": 5})
	root.Children = []*GuiNode{sl}
	Layout(root, 300, 120)

	if got := sliderValue(sl); got != 5 {
		t.Fatalf("退化量程的 value = %v, want 5", got)
	}
	if got := sliderRatio(sl); got != 0 {
		t.Fatalf("退化量程的 ratio = %v, want 0", got)
	}
	if got := sliderValueFromX(sl, 80); got != 5 {
		t.Fatalf("退化量程里 valueFromX = %v, want 5", got)
	}
	// max < min 也要能画: 量程塌缩到 min (滑块停在最左, 不产生 NaN 坐标)
	withNum(sl, "max", 0)
	if got := sliderValueFromX(sl, 80); got != 5 {
		t.Fatalf("max<min 时 valueFromX = %v, want 5 (塌缩到 min)", got)
	}
	if got := sliderKnobLeft(sl, sliderKnobW); got != sl.Box.X {
		t.Fatalf("max<min 时滑块应在最左, 实际 x = %d", got)
	}
	// NaN 属性 (脚本里 0/0) 不能把几何带成 NaN
	sl.Props["max"] = object.NewNumber(math.NaN())
	if got := sliderValueFromX(sl, 80); math.IsNaN(got) {
		t.Fatalf("NaN 属性污染了取值")
	}
}

// TestSliderIntrinsicSize 缺省 160×24; 显式尺寸优先。
func TestSliderIntrinsicSize(t *testing.T) {
	root := mkNode("column", nil)
	def := mkNode("slider", nil)
	big := mkNode("slider", map[string]float64{"width": 300, "height": 30})
	root.Children = []*GuiNode{def, big}
	Layout(root, 400, 200)

	if def.Box.W != sliderDefW || def.Box.H != sliderDefH {
		t.Fatalf("缺省尺寸 = %dx%d, want %dx%d", def.Box.W, def.Box.H, sliderDefW, sliderDefH)
	}
	if big.Box.W != 300 || big.Box.H != 30 {
		t.Fatalf("显式尺寸被覆盖: %v", big.Box)
	}
}

// TestSliderPaintGeometry 轨道居中、已填充段画到滑块中心、滑块在白底上带边框。
func TestSliderPaintGeometry(t *testing.T) {
	root := mkNode("column", nil)
	sl := mkNode("slider", map[string]float64{"width": 160, "height": 24, "value": 50})
	root.Children = []*GuiNode{sl}

	img := renderTree(root, 200, 60)
	// 轨道高 4, 在 24 高的盒子里居中 → y = 24/2 - 2 = 10..13
	trackY := sliderDefH/2 - sliderTrackH/2
	// 值 50 → ratio .5 → 滑块左缘 = round(.5*(160-12)) = 74, 中心 80
	knobLeft := sliderKnobLeft(sl, sliderKnobW)
	if knobLeft != 74 {
		t.Fatalf("值 50 时滑块左缘 = %d, want 74", knobLeft)
	}

	accent := colorAccent
	assertPx(t, img, 0, trackY+1, accent, "轨道左端已有填充")
	assertPx(t, img, knobLeft-4, trackY+1, accent, "填充一直画到滑块中心前")
	assertPx(t, img, knobLeft+sliderKnobW+20, trackY+1, colorTrack, "滑块右侧还是空轨道")
	assertPx(t, img, knobLeft+sliderKnobW/2, sliderDefH/2, colorKnob, "滑块内部是白底")
	assertPx(t, img, knobLeft, sliderDefH/2-sliderKnobH/2, colorFieldEdge, "滑块上边是边框")
	// 轨道之外 (盒子上下留白) 不该被填
	assertPx(t, img, 0, 0, pxWhite, "盒子里的轨道以外保持背景")
}

// TestSliderDisabledNoDrag 禁用滑块: 按下不改值, 也不进入拖动。
func TestSliderDisabledNoDrag(t *testing.T) {
	v, _, a, sl := sliderApp(t, "")
	withBool(sl, "disabled", true)
	Layout(a.rootNode(), 300, 120)

	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: 43, Y: 12},
		Event{Kind: EventMouseUp, X: 43, Y: 12},
	)
	if n := jsArrayLen(t, v, "seen"); n != 0 {
		t.Fatalf("禁用滑块不该派发 onInput, 实际 %d 次", n)
	}
	if a.dragTarget != nil {
		t.Fatalf("禁用滑块不该成为拖动目标")
	}
}

// TestSliderDragSequence 完整拖动: down → 移动三次 → up。
//
// 断言三件事: ① 每个鼠标位置只派发一次 (重复值被挡掉); ② 最终 value 正确;
// ③ 松手后 dragTarget 归 nil、按压态复位。
func TestSliderDragSequence(t *testing.T) {
	v, _, a, sl := sliderApp(t, "")

	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: 6, Y: 12},   // 归零
		Event{Kind: EventMouseMove, X: 43, Y: 12},  // 25
		Event{Kind: EventMouseMove, X: 43, Y: 12},  // 重复 → 不派发
		Event{Kind: EventMouseMove, X: 80, Y: 12},  // 50
		Event{Kind: EventMouseMove, X: 999, Y: 12}, // 越界 → 100
		Event{Kind: EventMouseUp, X: 999, Y: 12},   // 松手
	)
	if n := jsArrayLen(t, v, "seen"); n != 4 {
		t.Fatalf("onInput 应派发 4 次 (重复值不派发), 实际 %d 次: %v", n, jsArray(t, v, "seen").Elements)
	}
	for i, want := range []float64{0, 25, 50, 100} {
		if got := jsNumAt(t, v, "seen", i); got != want {
			t.Fatalf("第 %d 次派发 = %v, want %v", i, got, want)
		}
	}
	if got, _ := sl.PropNum("value"); got != 100 {
		t.Fatalf("拖动后 value prop = %v, want 100 (受控回写)", got)
	}
	if a.dragTarget != nil {
		t.Fatalf("松手后 dragTarget 未清空: %v", a.dragTarget)
	}
	if sl.pressed {
		t.Fatalf("松手后按压态未复位")
	}
}

// TestSliderClickJump 单击轨道任意位置直接跳值 (不必先按住再拖)。
func TestSliderClickJump(t *testing.T) {
	v, _, a, _ := sliderApp(t, "")

	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: 43, Y: 12},
		Event{Kind: EventMouseUp, X: 43, Y: 12},
	)
	if n := jsArrayLen(t, v, "seen"); n != 1 {
		t.Fatalf("单击应只派发一次 onInput, 实际 %d 次", n)
	}
	if got := jsNumAt(t, v, "seen", 0); got != 25 {
		t.Fatalf("单击 1/4 处应跳值 25, 实际 %v", got)
	}
	if a.dragTarget != nil {
		t.Fatalf("单击结束后不该留下拖动目标")
	}
}

// TestSliderDropBackNoWriteBack 脚本不回写 value 时:
//   - value prop 不变 (受控语义: 显示只看 value);
//   - 同一位置的重复移动**不会**反复派发 (靠 slideVal 挡, 不能靠 prop 比 ——
//     prop 永远不变, 拿它比较等于没挡);
//   - 松手后再按同一位置仍会派发 (endDrag 复位了缓存)。
func TestSliderDropBackNoWriteBack(t *testing.T) {
	v, root, a := evalForUI(t, `
		import { h, window, render } from "gx/gfx";
		const seen = [];
		const s = h("slider", {
			width: 160, height: 24, min: 0, max: 100, step: 1, value: 0,
			onInput: (e) => { seen.push(e.value); },   // 刻意不回写
		});
		render(h("column", null, s), window({ title: "slider", width: 300, height: 120 }));
	`)
	sl := findFirst(root, "slider")

	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: 43, Y: 12},
		Event{Kind: EventMouseMove, X: 43, Y: 12}, // 重复 → 不该再派发
		Event{Kind: EventMouseMove, X: 43, Y: 12},
		Event{Kind: EventMouseUp, X: 43, Y: 12},
	)
	if n := jsArrayLen(t, v, "seen"); n != 1 {
		t.Fatalf("同一位置重复移动只该派发 1 次, 实际 %d 次", n)
	}
	if got, _ := sl.PropNum("value"); got != 0 {
		t.Fatalf("脚本不回写时 value prop 不该变, 实际 %v", got)
	}

	// 松手后再按同一位置: 缓存已复位 → 仍然派发
	pumpEvents(t, v, a, Event{Kind: EventMouseDown, X: 43, Y: 12})
	if n := jsArrayLen(t, v, "seen"); n != 2 {
		t.Fatalf("再次按下同一位置应重新派发, 实际累计 %d 次", n)
	}
}

// TestSliderPressVisualKeepsHighlight 拖动中保持按压态 (滑块颜色变化),
// 且拖动期间鼠标划过别的控件不给它们加悬停高亮。
func TestSliderNoHoverDuringDrag(t *testing.T) {
	v, root, a := evalForUI(t, `
		import { h, window, render } from "gx/gfx";
		const btn = h("button", {width: 100, height: 30}, "btn");
		const s = h("slider", {width: 160, height: 24, min: 0, max: 100, value: 0});
		render(h("column", null, btn, s), window({ title: "slider", width: 300, height: 120 }));
	`)
	sl := findFirst(root, "slider")
	btn := findFirst(root, "button")
	if sl == nil || btn == nil {
		t.Fatalf("演示树不全: slider=%v button=%v", sl, btn)
	}
	Layout(a.rootNode(), 300, 120)

	pumpEvents(t, v, a,
		Event{Kind: EventMouseDown, X: 20, Y: sl.Box.Y + 12},
		// 拖到按钮上方: 拖动期间不该给按钮加悬停
		Event{Kind: EventMouseMove, X: 50, Y: btn.Box.Y + 15},
		Event{Kind: EventMouseUp, X: 50, Y: btn.Box.Y + 15},
	)
	if btn.hovered {
		t.Fatalf("拖动期间按钮被加了悬停高亮")
	}
	if btn.pressed {
		t.Fatalf("拖动期间按钮被加了按压态")
	}
}
