package gfx

import (
	"image"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P3-2: 过渡动画 =====
//
// 分两层验:
//   - 纯函数/纯 Go 用例 —— 插值本身 (easeOut 曲线、animState 的进度换算)
//     与"受控模型下显示值 ≠ prop"这条最核心的分离。用注入的假时钟把
//     时间钉死, 免得断言靠 sleep 撞运气。
//   - 全链路用例 —— 真 VM + signal 驱动 + 心跳定时器推进, 验"signal 一变
//     起过渡、几帧后收敛到目标值、静止后停表"。

// withFakeClock 把 animNow 换成可控时钟, 用完恢复。
//
// 动画的时间语义必须能被钉死才可测: 靠真实 sleep 的断言在 CI 上要么
// 太宽松 (放过 bug) 要么太紧 (偶发失败)。
func withFakeClock(t *testing.T, start time.Time) *time.Time {
	t.Helper()
	prev := animNow
	cur := start
	animNow = func() time.Time { return cur }
	t.Cleanup(func() {
		animNow = prev
		// 心跳表是包级状态: 用例之间必须清干净, 否则一个没走完的动画
		// 会让后续用例的定时器数量断言失真。
		animNodes = map[*GuiNode]struct{}{}
		animRunning = false
	})
	return &cur
}

// ===== 纯函数层 =====

// TestEaseOutCurve 缓出曲线的三个端点与单调性。
//
// 端点必须精确 (0→0, 1→1): 差一个 ulp 都会让"动画结束"的判定漂移,
// 最终表现为"最后一帧永远画不到目标值"。
func TestEaseOutCurve(t *testing.T) {
	if got := easeOut(0); got != 0 {
		t.Fatalf("easeOut(0) = %v, want 0", got)
	}
	if got := easeOut(1); got != 1 {
		t.Fatalf("easeOut(1) = %v, want 1", got)
	}
	// 缓出: 前半程走完一半以上 (线性是 0.5)
	if got := easeOut(0.5); got <= 0.5 {
		t.Fatalf("easeOut(0.5) = %v, 缓出应快于线性", got)
	}
	// 单调递增
	prev := -1.0
	for i := 0; i <= 10; i++ {
		v := easeOut(float64(i) / 10)
		if v < prev {
			t.Fatalf("easeOut 非单调: t=%d/10 给出 %v < %v", i, v, prev)
		}
		prev = v
	}
}

// TestAnimStateInterp 时间轴采样: t=0 → from、中途在区间内、t>=dur → to。
func TestAnimStateInterp(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	withFakeClock(t, base)

	s := &animState{from: 0, to: 100, start: base, dur: 100 * time.Millisecond}

	if got := s.at(base); got != 0 {
		t.Fatalf("t=0 时 = %v, want 0", got)
	}
	if s.done(base) {
		t.Fatalf("t=0 不该算完成")
	}
	mid := s.at(base.Add(50 * time.Millisecond))
	if mid <= 0 || mid >= 100 {
		t.Fatalf("中途值 %v 不在 (0,100) 开区间", mid)
	}
	// 缓出: 半程应超过线性中点 50
	if mid <= 50 {
		t.Fatalf("半程插值 %v, 缓出应 > 50", mid)
	}
	if got := s.at(base.Add(100 * time.Millisecond)); got != 100 {
		t.Fatalf("t=dur 时 = %v, want 100", got)
	}
	if !s.done(base.Add(150 * time.Millisecond)) {
		t.Fatalf("超时后应算完成")
	}
	if got := s.at(base.Add(150 * time.Millisecond)); got != 100 {
		t.Fatalf("超时后 = %v, want 100 (钳到终点)", got)
	}
}

// TestAnimDurationZeroIsInstant 时长 <=0 视为瞬时完成, 且不除零。
func TestAnimDurationZeroIsInstant(t *testing.T) {
	base := time.Now()
	s := &animState{from: 10, to: 20, start: base, dur: 0}
	if !s.done(base) {
		t.Fatalf("dur=0 应立即完成")
	}
	if got := s.at(base); got != 20 {
		t.Fatalf("dur=0 的 at() = %v, want 20", got)
	}
	// 时钟回拨 (now 早于 start) 给 0 而不是负进度
	s2 := &animState{from: 0, to: 100, start: base, dur: 100 * time.Millisecond}
	if got := animProgress(s2, base.Add(-time.Second)); got != 0 {
		t.Fatalf("时钟回拨时进度 = %v, want 0", got)
	}
}

// TestEffectivePropNumPrefersAnim 这是整块功能的核心不变量:
// **动画进行中, prop 里已是终值, 但 effectivePropNum 必须返回插值。**
//
// 若这条坏了, 现象就是"动画完全不起作用" (画面一帧跳到目标值) ——
// 而且它不会报任何错, 只会静默失效, 所以必须有专门的用例守着。
func TestEffectivePropNumPrefersAnim(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	withFakeClock(t, base)

	n := mkNode("rect", map[string]float64{"width": 200}) // prop 已是终值
	n.anim = map[string]*animState{
		"width": {from: 0, to: 200, start: base, dur: 100 * time.Millisecond},
	}

	if got := effectivePropNum(n, "width"); got != 0 {
		t.Fatalf("t=0 时显示值 = %v, want 0 (不是 prop 的 200)", got)
	}
	// 把 start 往回推 50ms, 等价于"已经走了半程"
	n.anim["width"].start = base.Add(-50 * time.Millisecond)
	if v := effectivePropNum(n, "width"); v <= 0 || v >= 200 {
		t.Fatalf("半程显示值 = %v, 应在 (0,200)", v)
	}
	// 走完后回落到 prop (终值)
	n.anim["width"].start = base.Add(-200 * time.Millisecond)
	if v := effectivePropNum(n, "width"); v != 200 {
		t.Fatalf("走完后显示值 = %v, want 200", v)
	}
}

// TestEffectivePropNumOkCountsAnimAsPresent 有动画 == 有显式尺寸。
//
// 这条关系到布局: intrinsicSize 用 "width 是否存在" 判断该用固有尺寸还是
// 显式尺寸。动画一挂上就报 false 的话, 节点会在动画中途突然回退到内容
// 尺寸、下一帧又跳回来 (表现为"过渡时尺寸抽搐")。
func TestEffectivePropNumOkCountsAnimAsPresent(t *testing.T) {
	base := time.Now()
	withFakeClock(t, base)

	n := &GuiNode{Tag: "rect", Props: map[string]object.Value{}} // 没有 width prop
	n.anim = map[string]*animState{
		"width": {from: 0, to: 100, start: base, dur: time.Second},
	}
	if _, ok := effectivePropNumOk(n, "width"); !ok {
		t.Fatalf("有 width 动画时应报告属性存在")
	}
	// 无动画无 prop: 不存在
	m := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	if _, ok := effectivePropNumOk(m, "width"); ok {
		t.Fatalf("无动画无 prop 时不该报告存在")
	}
}

// TestEffectiveOpacityDefaultsToOne 缺省不透明; 越界与 NaN 都被钳掉。
func TestEffectiveOpacityDefaultsToOne(t *testing.T) {
	n := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	if got := effectiveOpacity(n); got != 1 {
		t.Fatalf("无 opacity prop 时 = %v, want 1", got)
	}
	cases := []struct {
		v    float64
		want float64
	}{
		{0.5, 0.5}, {0, 0}, {1, 1}, {-3, 0}, {7, 1},
	}
	for _, c := range cases {
		m := mkNode("rect", map[string]float64{"opacity": c.v})
		if got := effectiveOpacity(m); got != c.want {
			t.Fatalf("opacity=%v → %v, want %v", c.v, got, c.want)
		}
	}
}

// TestTransitionDurationForms transition prop 的两种写法。
func TestTransitionDurationForms(t *testing.T) {
	// 按属性配置
	n := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	obj := object.NewObject()
	obj.SetProperty("width", object.NewNumber(150))
	n.Props["transition"] = obj

	if got := transitionDuration(n, "width"); got != 150*time.Millisecond {
		t.Fatalf("width 时长 = %v, want 150ms", got)
	}
	if got := transitionDuration(n, "height"); got != 0 {
		t.Fatalf("未配置的 height 时长 = %v, want 0", got)
	}

	// 数字简写: 所有属性共用
	m := &GuiNode{Tag: "rect", Props: map[string]object.Value{"transition": object.NewNumber(80)}}
	if got := transitionDuration(m, "width"); got != 80*time.Millisecond {
		t.Fatalf("简写 width 时长 = %v, want 80ms", got)
	}
	if got := transitionDuration(m, "opacity"); got != 80*time.Millisecond {
		t.Fatalf("简写 opacity 时长 = %v, want 80ms", got)
	}

	// 没配 transition
	p := &GuiNode{Tag: "rect", Props: map[string]object.Value{}}
	if got := transitionDuration(p, "width"); got != 0 {
		t.Fatalf("无 transition 时长 = %v, want 0", got)
	}
	if p.hasTransition() {
		t.Fatalf("无 transition 时 hasTransition 应为 false")
	}
}

// TestAnimatablePropsWhitelist 白名单必须挡住"不该做过渡"的属性。
func TestAnimatablePropsWhitelist(t *testing.T) {
	for _, p := range []string{"width", "height", "left", "top", "opacity"} {
		if !animatableProps[p] {
			t.Fatalf("%s 应在可动画白名单里", p)
		}
	}
	// 这几个刻意排除: value 是受控组件值 (过渡会与脚本写回打架),
	// padding/gap/margin 半像素中间值会导致文字穿透。
	for _, p := range []string{"value", "padding", "gap", "margin", "flexGrow", "background"} {
		if animatableProps[p] {
			t.Fatalf("%s 不该在可动画白名单里", p)
		}
	}
}

// ===== 纯 Go 层: startTransition / animTick 生命周期 =====

// TestStartTransitionAndTickSettles 起过渡 → tick 推进 → 收敛后清表停表。
func TestStartTransitionAndTickSettles(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)

	n := mkNode("rect", map[string]float64{"width": 100})
	n.Props["transition"] = object.NewNumber(100) // 100ms
	_, a := mountTestApp(t, columnWith(n), 300, 200)

	startTransition(n, "width", 200)
	if n.anim["width"] == nil {
		t.Fatalf("startTransition 未登记 animState")
	}
	if _, ok := animNodes[n]; !ok {
		t.Fatalf("startTransition 未把节点登记进 animNodes")
	}
	if !animRunning {
		t.Fatalf("startTransition 未启用心跳")
	}
	// 起点是当时的显示值 (100), 不是 0
	if got := n.anim["width"].from; got != 100 {
		t.Fatalf("from = %v, want 100 (当前显示值)", got)
	}

	// 推进到半程: 显示值是插值而不是 prop
	*cur = base.Add(50 * time.Millisecond)
	if v := effectivePropNum(n, "width"); v <= 100 || v >= 200 {
		t.Fatalf("半程显示值 = %v, 应在 (100,200)", v)
	}
	animTick()
	if n.anim["width"] == nil {
		t.Fatalf("半程 tick 不该清掉动画")
	}

	// 推进到结束: tick 收敛并清表停表
	*cur = base.Add(150 * time.Millisecond)
	animTick()
	if n.anim != nil {
		t.Fatalf("结束后 anim 应清空, 实际 %v", n.anim)
	}
	if len(animNodes) != 0 {
		t.Fatalf("结束后 animNodes 应为空")
	}
	if animRunning {
		t.Fatalf("无活动动画时 animRunning 应为 false (静止零开销)")
	}
	if v := effectivePropNum(n, "width"); v != 200 {
		t.Fatalf("收敛后显示值 = %v, want 200", v)
	}
	// 收尾后标脏已发生 (tick 里 markNodeDirty), 且 needDraw 为真
	if !needDraw(a) {
		t.Fatalf("动画推进应标脏待重绘")
	}
}

// TestStartTransitionIgnoresNonAnimatable 白名单外的属性不起过渡。
func TestStartTransitionIgnoresNonAnimatable(t *testing.T) {
	base := time.Now()
	withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("progress", map[string]float64{"value": 0.5})
	startTransition(n, "value", 1)
	if n.anim != nil {
		t.Fatalf("非动画属性不该起过渡: %v", n.anim)
	}
	if animRunning {
		t.Fatalf("不该为被拒的属性启用心跳")
	}
}

// TestStartTransitionSkipsWhenTargetUnchanged 目标值没变不该起动画
// (effect 重跑时值常常没变, 每次起一段动画会让动画永远走不完)。
func TestStartTransitionSkipsWhenTargetUnchanged(t *testing.T) {
	base := time.Now()
	withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("rect", map[string]float64{"width": 100})
	n.Props["transition"] = object.NewNumber(100)
	startTransition(n, "width", 100) // 与当前显示值相同
	if n.anim != nil {
		t.Fatalf("目标未变时不该起过渡")
	}
}

// TestStartTransitionCancelsPrevious 同节点同属性的新过渡顶掉旧过渡。
func TestStartTransitionCancelsPrevious(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("rect", map[string]float64{"width": 100})
	n.Props["transition"] = object.NewNumber(1000)
	startTransition(n, "width", 200)

	// 走到 40% 时改目标
	*cur = base.Add(400 * time.Millisecond)
	mid := effectivePropNum(n, "width")
	startTransition(n, "width", 0)

	if got := n.anim["width"].from; got != mid {
		t.Fatalf("新过渡的 from = %v, want %v (当前显示值)", got, mid)
	}
	if got := n.anim["width"].to; got != 0 {
		t.Fatalf("新过渡的 to = %v, want 0", got)
	}
	if len(n.anim) != 1 {
		t.Fatalf("同属性应只有一个 animState, 实际 %d", len(n.anim))
	}
}

// TestAnimTickOnlyDirtiesDoesNotRedraw 心跳只标脏, 不直接重绘。
//
// 这条守着"一帧一次重绘"的性能约定: 心跳里若自己 redraw, 10 个动画节点
// 就会各自触发一整帧重绘。
func TestAnimTickOnlyDirtiesDoesNotRedraw(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)
	fake, a := mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("rect", map[string]float64{"width": 100})
	n.Props["transition"] = object.NewNumber(500)
	a.root = columnWith(n)
	Layout(a.root, 200, 200)
	a.redraw()
	before := shots(fake)

	startTransition(n, "width", 200)
	*cur = base.Add(100 * time.Millisecond)
	a.mu.Lock()
	a.needDraw = false
	a.mu.Unlock()
	animTick()

	if got := shots(fake); got != before {
		t.Fatalf("animTick 不该直接上屏 (shots %d → %d)", before, got)
	}
	if !needDraw(a) {
		t.Fatalf("animTick 应标脏")
	}
}

// TestCancelAnimOnDispose 离树节点必须从动画表里摘掉。
func TestCancelAnimOnDispose(t *testing.T) {
	base := time.Now()
	withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	parent := mkNode("column", nil)
	n := mkNode("rect", map[string]float64{"width": 100})
	n.Props["transition"] = object.NewNumber(500)
	parent.Children = []*GuiNode{n}
	n.Parent = parent

	startTransition(n, "width", 200)
	if _, ok := animNodes[n]; !ok {
		t.Fatalf("起过渡后应在 animNodes 里")
	}
	disposeNode(n)
	if _, ok := animNodes[n]; ok {
		t.Fatalf("dispose 后不该还在 animNodes 里 (会每帧标脏一个离树节点)")
	}
	if n.anim != nil {
		t.Fatalf("dispose 后 anim 应清空")
	}
}

// columnWith 把单个节点包成一个 column 根 (布局需要容器才有意义)。
func columnWith(kids ...*GuiNode) *GuiNode {
	root := mkNode("column", nil)
	for _, k := range kids {
		k.Parent = root
		root.Children = append(root.Children, k)
	}
	return root
}

// TestTransitionAffectsLayout 过渡必须真的影响布局结果 (不只是绘制)。
//
// 这是"声明式过渡在受控模型下能不能用"的关键: 宽度过渡期间, 节点的
// Box.W 应该取插值, 而兄弟节点的位置也应随之变化。
func TestTransitionAffectsLayout(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 400, 300)

	box := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	box.Props["transition"] = object.NewNumber(100)
	root := columnWith(box)

	startTransition(box, "width", 300)
	*cur = base.Add(100 * time.Millisecond) // 走完
	Layout(root, 400, 300)
	if box.Box.W != 300 {
		t.Fatalf("收敛后 Box.W = %d, want 300", box.Box.W)
	}

	// 半程: Box.W 应是插值 (既非 100 也非 300)
	startTransition(box, "width", 100)
	*cur = base.Add(150 * time.Millisecond) // 新过渡刚走 50ms / 100ms
	Layout(root, 400, 300)
	if box.Box.W <= 100 || box.Box.W >= 300 {
		t.Fatalf("半程 Box.W = %d, 应为插值 (100,300)", box.Box.W)
	}
}

// TestOpacityFadeAppliesToFill 淡出必须真的作用到像素上。
//
// 注意 image.RGBA 是**预乘**表示: 半透明红 (#ff0000 @50%) 存的是
// {127, 0, 0, 127} 而不是 {255, 0, 0, 127}。所以断言要看"alpha 降了、
// RGB 按同一比例降了", 不能拿 R 通道跟不透明组比大小。
func TestOpacityFadeAppliesToFill(t *testing.T) {
	// 全不透明: 盒子中间是纯红
	root1 := columnWith(mkNode("rect", map[string]float64{"width": 40, "height": 30}))
	root1.Children[0].Props["background"] = object.NewString("#ff0000")
	img1 := image.NewRGBA(image.Rect(0, 0, 60, 40))
	Layout(root1, 60, 40)
	Draw(img1, root1)
	opaque := img1.RGBAAt(20, 15)

	if opaque.R != 255 || opaque.A != 255 {
		t.Fatalf("对照组应是不透明红, 实际 %v", opaque)
	}

	// opacity=0.5: 该像素 alpha 减半, 预乘的 R 也同比减半
	root2 := columnWith(mkNode("rect", map[string]float64{"width": 40, "height": 30, "opacity": 0.5}))
	root2.Children[0].Props["background"] = object.NewString("#ff0000")
	img2 := image.NewRGBA(image.Rect(0, 0, 60, 40))
	Layout(root2, 60, 40)
	Draw(img2, root2)
	got := img2.RGBAAt(20, 15)

	if got.A == 0 || got.A >= 255 {
		t.Fatalf("opacity=0.5 时 alpha = %d, 应在 (0,255)", got.A)
	}
	if got.R >= opaque.R {
		t.Fatalf("淡出后预乘 R = %d, 应低于不透明的 %d", got.R, opaque.R)
	}
	// 色相不变: G/B 仍是 0
	if got.G != 0 || got.B != 0 {
		t.Fatalf("淡出不该改动 G/B 通道, 实际 %v", got)
	}

	// 全透明: 完全不落笔
	root3 := columnWith(mkNode("rect", map[string]float64{"width": 40, "height": 30, "opacity": 0}))
	root3.Children[0].Props["background"] = object.NewString("#ff0000")
	img3 := image.NewRGBA(image.Rect(0, 0, 60, 40))
	Layout(root3, 60, 40)
	Draw(img3, root3)
	if p := img3.RGBAAt(20, 15); p.A != 0 {
		t.Fatalf("opacity=0 不该落笔, 实际 %v", p)
	}
}

// TestOpacityIsGrouped 父节点 opacity 作用于整棵子树 (CSS 成组语义)。
func TestOpacityIsGrouped(t *testing.T) {
	child := mkNode("rect", map[string]float64{"width": 20, "height": 20})
	child.Props["background"] = object.NewString("#00ff00")
	parent := mkNode("rect", map[string]float64{"width": 40, "height": 30, "opacity": 0.5})
	parent.Children = []*GuiNode{child}
	child.Parent = parent
	root := columnWith(parent)

	img := image.NewRGBA(image.Rect(0, 0, 60, 40))
	Layout(root, 60, 40)
	Draw(img, root)

	// 子节点自身没有 opacity, 但父节点的 0.5 应作用到它
	if p := img.RGBAAt(5, 5); p.A >= 255 {
		t.Fatalf("父节点的 opacity 未作用到子节点, 子像素 alpha = %d", p.A)
	}
}

// ===== 全链路: signal 驱动 =====

// TestTransitionOnSignal 信号驱动 x→width 过渡: 中途是插值, 最终收敛。
func TestTransitionOnSignal(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)

	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() {
		object.GlobalScheduler().ClearAll()
		animNodes = map[*GuiNode]struct{}{}
		animRunning = false
	})

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, window, render } from "gx/gfx";
		import { createSignal } from "gx/solid";
		const [w, setW] = createSignal(100);
		globalThis.grow = () => setW(300);
		render(
			h("column", null,
				h("rect", { width: w, height: 20, transition: { width: 1000 },
				            background: "#c0392b" })),
			window({ title: "anim", width: 400, height: 200 }));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatalf("未挂载应用")
	}
	rect := findFirst(a.rootNode(), "rect")
	if rect == nil {
		t.Fatalf("没找到 rect")
	}
	if rect.Box.W != 100 {
		t.Fatalf("初始宽 = %d, want 100", rect.Box.W)
	}

	// 改 signal: 起过渡
	driveSteps(t, v, func() { callGlobal(t, v, "grow") })

	// signal 已写回 prop (终值), 但显示值由动画接管
	if v, _ := rect.PropNum("width"); v != 300 {
		t.Fatalf("prop 应已被写成终值 300, 实际 %v", v)
	}
	if rect.anim["width"] == nil {
		t.Fatalf("signal 变化未启动过渡")
	}

	// 推进半程: 布局读到的应是插值
	*cur = base.Add(500 * time.Millisecond)
	Layout(a.rootNode(), 400, 200)
	if w := rect.Box.W; w <= 100 || w >= 300 {
		t.Fatalf("半程 Box.W = %d, 应为插值 (100,300)", w)
	}

	// 走到结束: 收敛到 300
	*cur = base.Add(1500 * time.Millisecond)
	animTick()
	Layout(a.rootNode(), 400, 200)
	if w := rect.Box.W; w != 300 {
		t.Fatalf("收敛后 Box.W = %d, want 300", w)
	}
	if rect.anim != nil {
		t.Fatalf("结束后 anim 应清空")
	}
}

// TestTransitionSkippedWithoutProp 没配 transition 时不退化为"慢动作",
// 而是立刻生效 (保持原有的一帧到位语义)。
func TestTransitionSkippedWithoutProp(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, window, render } from "gx/gfx";
		import { createSignal } from "gx/solid";
		const [w, setW] = createSignal(100);
		globalThis.grow = () => setW(300);
		render(h("column", null, h("rect", { width: w, height: 20 })),
		       window({ title: "plain", width: 400, height: 200 }));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	rect := findFirst(a.rootNode(), "rect")

	driveSteps(t, v, func() { callGlobal(t, v, "grow") })
	if rect.anim != nil {
		t.Fatalf("没配 transition 不该起动画")
	}
	Layout(a.rootNode(), 400, 200)
	if rect.Box.W != 300 {
		t.Fatalf("无 transition 应立即生效, Box.W = %d", rect.Box.W)
	}
}

// pumpFrames 跑若干轮真实等待的事件循环, 让 16ms 动画心跳真的到期。
//
// 关键点: **每次给泵一个有界的 maxWait** (不是让它按"下次定时器到期"自己算)。
// 当动画已经结束、定时器表空了的时候, 事件循环会把 maxWait 传成 0
// (= "无限期等待外部事件"), 而假 Surface 对 maxWait<=0 会睡 10 秒 ——
// 于是"跑 20 帧"变成"跑 200 秒", 表现为测试挂死。
// 这里把上限压到 20ms, 保证每轮都是短睡, 也顺带覆盖"泵空转"的情形。
//
// stop 返回真时提前收工 (动画完成的判据), 免得白跑满 frames 轮。
func pumpFrames(t *testing.T, v *vm.VM, a *app, frames int, stop func() bool) {
	t.Helper()
	const maxPumpWait = 20 * time.Millisecond
	n := 0
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		if n >= frames || (stop != nil && stop()) {
			return false
		}
		n++
		if maxWait <= 0 || maxWait > maxPumpWait {
			maxWait = maxPumpWait
		}
		return a.pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if n == 0 {
		t.Fatalf("事件循环一轮都没跑")
	}
}

// ===== 命令式 animate =====

// TestAnimateRawCallsOnUpdate animate(from,to,dur,onUpdate,onDone) 的帧回调。
func TestAnimateRawCallsOnUpdate(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, window, render, animate } from "gx/gfx";
		globalThis.samples = [];
		globalThis.finished = false;
		globalThis.run = () => {
			animate(0, 100, 48, function (v) { globalThis.samples.push(v); },
				function () { globalThis.finished = true; });
		};
		render(h("column", null, h("text", null, "anim")),
		       window({ title: "raw", width: 200, height: 120 }));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatalf("未挂载应用")
	}
	driveSteps(t, v, func() { callGlobal(t, v, "run") })
	// 让心跳跑足够多轮 (每轮真实等待一个 16ms 帧), 动画完成即提前收工
	pumpFrames(t, v, a, 40, func() bool {
		b, _ := globalBool(t, v, "finished")
		return b
	})

	n := jsArrayLen(t, v, "samples")
	if n < 2 {
		t.Fatalf("onUpdate 只被调了 %d 次, 至少应有 2 帧", n)
	}
	// 最后一帧必须是终值
	arr := jsArray(t, v, "samples")
	last := arr.Elements[len(arr.Elements)-1]
	num, ok := last.(*object.Number)
	if !ok {
		t.Fatalf("样本不是数字: %s", last.Type())
	}
	if num.Value != 100 {
		t.Fatalf("最后一帧 = %v, want 100", num.Value)
	}
	if b, ok := globalBool(t, v, "finished"); !ok || !b {
		t.Fatalf("onDone 未被调用")
	}
	// 值必须单调不减, 且不得越过终点
	prev := -1.0
	for i, e := range arr.Elements {
		x, ok := e.(*object.Number)
		if !ok {
			t.Fatalf("第 %d 个样本不是数字: %s", i, e.Type())
		}
		if x.Value < prev {
			t.Fatalf("第 %d 帧 %v < 上一帧 %v (非单调)", i, x.Value, prev)
		}
		if x.Value > 100 {
			t.Fatalf("第 %d 帧 %v 越过终点 100", i, x.Value)
		}
		prev = x.Value
	}
}

// TestAnimateNodePropCancel 命令式改元素属性 + cancel 停在当前值。
func TestAnimateNodePropCancel(t *testing.T) {
	base := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	cur := withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("rect", map[string]float64{"width": 0})
	cancel := animateNodeProp(n, []object.Value{
		object.NewString("width"), object.NewNumber(100), object.NewNumber(1000),
	})
	if n.anim["width"] == nil {
		t.Fatalf("animate(node, prop, ...) 未起过渡")
	}
	if !object.IsCallable(cancel) {
		t.Fatalf("animate 应返回 cancel 函数")
	}

	*cur = base.Add(500 * time.Millisecond)
	mid := effectivePropNum(n, "width")
	callScriptFn(cancel)
	if n.anim != nil {
		t.Fatalf("cancel 后 anim 应清空")
	}
	// cancel 把当前插值写回 prop (停在原地, 不跳回 0 也不跳到 100)
	if got := effectivePropNum(n, "width"); got != mid {
		t.Fatalf("cancel 后显示值 = %v, want %v (停在当前值)", got, mid)
	}
	if got, _ := n.PropNum("width"); got != mid {
		t.Fatalf("cancel 应把当前值写回 prop, 实际 %v", got)
	}
}

// TestAnimateRejectsNonAnimatableProp 白名单外的属性返回 TypeError。
func TestAnimateRejectsNonAnimatableProp(t *testing.T) {
	n := mkNode("rect", nil)
	got := animateNodeProp(n, []object.Value{
		object.NewString("padding"), object.NewNumber(10), object.NewNumber(100),
	})
	if _, ok := got.(*object.Error); !ok {
		t.Fatalf("非动画属性应返回 TypeError, 实际 %s", got.Type())
	}
	if n.anim != nil {
		t.Fatalf("被拒的属性不该起动画")
	}
}

// TestAnimateZeroDurationLandsImmediately 时长为 0 直接落终值, 不起表。
func TestAnimateZeroDurationLandsImmediately(t *testing.T) {
	base := time.Now()
	withFakeClock(t, base)
	mountTestApp(t, mkNode("column", nil), 200, 200)

	n := mkNode("rect", map[string]float64{"width": 10})
	animateNodeProp(n, []object.Value{
		object.NewString("width"), object.NewNumber(80), object.NewNumber(0),
	})
	if n.anim != nil {
		t.Fatalf("时长为 0 不该起表")
	}
	if got, _ := n.PropNum("width"); got != 80 {
		t.Fatalf("时长为 0 应直接落终值 80, 实际 %v", got)
	}
	if animRunning {
		t.Fatalf("不该启用心跳")
	}
}
