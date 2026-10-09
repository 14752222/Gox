package gfx

// 手势 (长按 / 滑动) 的回归测试 (ryB64Z)。
//
// 断言分两层, 与 events_test.go 同一套路:
//   - 纯 Go 层: 直接组装 app 不跑 VM, 精确断言手势判定与 click 的互斥关系;
//   - 全链路: 假 Surface + 真 VM, 断言脚本回调收到的载荷字段确实正确。
//
// 长按依赖"泵被等待预算钳到 deadline 后醒来"这条机制 (不是 AfterFunc),
// 所以计时用例必须走 pump —— 直接调 tickLongPress 验不到泵会不会睡死。

import (
	"math"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 纯 Go 层 =====

// gestureNode 造一个可命中、并挂上指定手势回调的节点。
// onLongPress / onSwipe 为 nil 表示不挂该回调。
func gestureNode(tag string, w, h float64, onLongPress, onSwipe func(object.Value)) *GuiNode {
	n := mkNode(tag, map[string]float64{"width": w, "height": h})
	if onLongPress != nil {
		n.Props["onLongPress"] = object.NewBuiltin("onLongPress", func(args ...object.Value) object.Value {
			onLongPress(firstArg(args))
			return object.UndefinedSingleton
		})
	}
	if onSwipe != nil {
		n.Props["onSwipe"] = object.NewBuiltin("onSwipe", func(args ...object.Value) object.Value {
			onSwipe(firstArg(args))
			return object.UndefinedSingleton
		})
	}
	return n
}

func firstArg(args []object.Value) object.Value {
	if len(args) == 0 {
		return nil
	}
	return args[0]
}

// numProp 读载荷里的数值字段; 取不到返回 def (便于"字段不存在"的断言)。
func gestNum(v object.Value, name string, def float64) float64 {
	o, ok := v.(*object.Object)
	if !ok {
		return def
	}
	got, _ := o.GetProperty(name)
	n, ok := got.(*object.Number)
	if !ok {
		return def
	}
	return n.Value
}

func gestStr(v object.Value, name string) string {
	o, ok := v.(*object.Object)
	if !ok {
		return ""
	}
	got, _ := o.GetProperty(name)
	if s, ok := got.(*object.String); ok {
		return s.Value
	}
	return ""
}

// gestureApp 挂一棵只有手势节点的树, 返回 fake / app / 节点。
func gestureApp(t *testing.T, n *GuiNode) (*fakeSurface, *app) {
	t.Helper()
	root := mkNode("column", nil)
	root.Children = []*GuiNode{n}
	n.Parent = root
	Layout(root, 300, 200)
	return mountTestApp(t, root, 300, 200)
}

func TestSwipeDirections(t *testing.T) {
	cases := []struct {
		name string
		from [2]int
		to   [2]int
		want string
	}{
		{"向右", [2]int{40, 100}, [2]int{140, 104}, "right"},
		{"向左", [2]int{200, 100}, [2]int{100, 96}, "left"},
		{"向下", [2]int{100, 40}, [2]int{104, 140}, "down"},
		// 起点终点都必须落在 rect 的 260x180 盒内 —— 出了盒就命不中,
		// 手势根本不会开始 (这条曾经写错过: 用 y=200 起手, 盒高只有 180)。
		{"向上", [2]int{100, 170}, [2]int{96, 70}, "up"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var got object.Value
			n := gestureNode("rect", 260, 180, nil, func(v object.Value) { got = v })
			fake, a := gestureApp(t, n)

			pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: c.from[0], Y: c.from[1]})
			pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: c.to[0], Y: c.to[1]})
			pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: c.to[0], Y: c.to[1]})

			if got == nil {
				t.Fatalf("onSwipe 未派发")
			}
			if d := gestStr(got, "direction"); d != c.want {
				t.Fatalf("direction = %q, want %q", d, c.want)
			}
			wantDX := float64(c.to[0] - c.from[0])
			wantDY := float64(c.to[1] - c.from[1])
			if dx := gestNum(got, "dx", -99999); dx != wantDX {
				t.Fatalf("dx = %v, want %v", dx, wantDX)
			}
			if dy := gestNum(got, "dy", -99999); dy != wantDY {
				t.Fatalf("dy = %v, want %v", dy, wantDY)
			}
			// distance 是欧氏距离 (保留一位小数比较, 免得浮点末位抖动)
			wantDist := math.Sqrt(wantDX*wantDX + wantDY*wantDY)
			if d := gestNum(got, "distance", -1); absF(d-wantDist) > 0.01 {
				t.Fatalf("distance = %v, want %v", d, wantDist)
			}
			// 终点坐标
			if x := gestNum(got, "x", -1); x != float64(c.to[0]) {
				t.Fatalf("x = %v, want %v", x, c.to[0])
			}
			if y := gestNum(got, "y", -1); y != float64(c.to[1]) {
				t.Fatalf("y = %v, want %v", y, c.to[1])
			}
		})
	}
}

// TestSwipeBelowThreshold 位移没到阈值不算滑动 —— 而且 click 要照常派发。
func TestSwipeBelowThreshold(t *testing.T) {
	swiped := false
	clicked := 0
	n := gestureNode("rect", 260, 180, nil, func(object.Value) { swiped = true })
	n.Props["onClick"] = object.NewBuiltin("onClick", func(...object.Value) object.Value {
		clicked++
		return object.UndefinedSingleton
	})
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 118, Y: 103}) // 18px < 40
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 118, Y: 103})

	if swiped {
		t.Fatal("位移 18px 未达阈值 40, 不应判成滑动")
	}
	if clicked != 1 {
		t.Fatalf("click 应照常派发一次, got %d", clicked)
	}
}

// TestSwipeSwallowsClick 滑动成立时, 抬起不应再派发 click。
func TestSwipeSwallowsClick(t *testing.T) {
	swiped := false
	clicked := 0
	n := gestureNode("rect", 260, 180, nil, func(object.Value) { swiped = true })
	n.Props["onClick"] = object.NewBuiltin("onClick", func(...object.Value) object.Value {
		clicked++
		return object.UndefinedSingleton
	})
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 40, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 200, Y: 102})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 200, Y: 102})

	if !swiped {
		t.Fatal("onSwipe 未派发")
	}
	if clicked != 0 {
		t.Fatalf("滑动成立时不应再派发 click, got %d", clicked)
	}
}

// TestSwipeThresholdProp 阈值可按节点配 (swipeThreshold prop)。
func TestSwipeThresholdProp(t *testing.T) {
	swiped := false
	n := gestureNode("rect", 260, 180, nil, func(object.Value) { swiped = true })
	n.Props["swipeThreshold"] = object.NewNumber(20) // 缺省 40 → 改成 20
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 130, Y: 101}) // 30px
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 130, Y: 101})

	if !swiped {
		t.Fatal("swipeThreshold=20 时 30px 位移应判成滑动")
	}
}

// TestSwipeNeedsHandler 没有 onSwipe 时滑动不成立 (不能吞掉 click)。
func TestSwipeNeedsHandler(t *testing.T) {
	clicked := 0
	n := mkNode("rect", map[string]float64{"width": 260, "height": 180})
	n.Props["onClick"] = object.NewBuiltin("onClick", func(...object.Value) object.Value {
		clicked++
		return object.UndefinedSingleton
	})
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 40, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 200, Y: 102})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 200, Y: 102})

	if clicked != 1 {
		t.Fatalf("没有 onSwipe 时 click 应照常派发, got %d", clicked)
	}
}

// TestLongPressFiresOnDeadline 长按在计时到期后派发 (走 pump 的等待预算钳制,
// 不是 AfterFunc)。
func TestLongPressFiresOnDeadline(t *testing.T) {
	var got object.Value
	n := gestureNode("rect", 260, 180, func(v object.Value) { got = v }, nil)
	n.Props["longPressDelay"] = object.NewNumber(30)
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	if got != nil {
		t.Fatal("刚按下就派发了 onLongPress —— 计时没生效")
	}
	// 睡过 deadline 再泵一轮: 泵必须被 nextLongPressWake 叫醒并派发
	time.Sleep(40 * time.Millisecond)
	fake.push(Event{Kind: EventMouseMove, X: 100, Y: 100})
	a.pump(time.Millisecond)

	if got == nil {
		t.Fatal("长按计时到期后未派发 onLongPress (泵可能睡死了)")
	}
	if x := gestNum(got, "x", -1); x != 100 {
		t.Fatalf("载荷 x = %v, want 100", x)
	}
	if d := gestNum(got, "duration", -1); d < 30 {
		t.Fatalf("duration = %v, 应 >= 30 (长按阈值)", d)
	}
}

// TestLongPressDelayZero 阈值 0 时下一轮 pump 立即派发。
func TestLongPressDelayZero(t *testing.T) {
	var got object.Value
	n := gestureNode("rect", 260, 180, func(v object.Value) { got = v }, nil)
	n.Props["longPressDelay"] = object.NewNumber(0)
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 50, Y: 60})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 50, Y: 60})
	if got == nil {
		t.Fatal("longPressDelay=0 时应在下一轮 pump 派发 onLongPress")
	}
	if y := gestNum(got, "y", -1); y != 60 {
		t.Fatalf("载荷 y = %v, want 60", y)
	}
}

// TestLongPressSwallowsClick 长按成立时抬起不再派发 click (长按不是点击)。
func TestLongPressSwallowsClick(t *testing.T) {
	lp := 0
	clicked := 0
	n := gestureNode("rect", 260, 180, func(object.Value) { lp++ }, nil)
	n.Props["longPressDelay"] = object.NewNumber(0)
	n.Props["onClick"] = object.NewBuiltin("onClick", func(...object.Value) object.Value {
		clicked++
		return object.UndefinedSingleton
	})
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 100, Y: 100}) // 触发到点派发
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 100, Y: 100})

	if lp != 1 {
		t.Fatalf("onLongPress 应派发一次, got %d", lp)
	}
	if clicked != 0 {
		t.Fatalf("长按成立时不应再派发 click, got %d", clicked)
	}
}

// TestLongPressCancelledByMovement 按住后挪开超过 slop ⇒ 长按作废。
func TestLongPressCancelledByMovement(t *testing.T) {
	lp := 0
	n := gestureNode("rect", 260, 180, func(object.Value) { lp++ }, nil)
	n.Props["longPressDelay"] = object.NewNumber(30)
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	// 挪 60px, 远超缺省 slop 10
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 160, Y: 100})
	time.Sleep(40 * time.Millisecond)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 160, Y: 100})

	if lp != 0 {
		t.Fatalf("按住后挪开了仍派发 onLongPress (slop 判据失效), got %d", lp)
	}
}

// TestLongPressSlopToleratesJitter slop 内的抖动不算挪开 (手指按住也会漂)。
//
// 用 30ms 延迟而不是 0: 0 会在**按下那一轮** pump 里就到点派发, 之后的
// MouseMove 无论怎么挪都来不及作废 —— 那样的用例测的是运气不是 slop。
func TestLongPressSlopToleratesJitter(t *testing.T) {
	lp := 0
	n := gestureNode("rect", 260, 180, func(object.Value) { lp++ }, nil)
	n.Props["longPressDelay"] = object.NewNumber(30)
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 104, Y: 103}) // 4px < slop 10
	time.Sleep(40 * time.Millisecond)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 104, Y: 103})
	if lp != 1 {
		t.Fatalf("slop 内抖动不应作废长按, onLongPress 派发 %d 次, want 1", lp)
	}
}

// TestLongPressSlopProp slop 可按节点配 (longPressSlop prop)。
func TestLongPressSlopProp(t *testing.T) {
	lp := 0
	n := gestureNode("rect", 260, 180, func(object.Value) { lp++ }, nil)
	n.Props["longPressDelay"] = object.NewNumber(30)
	n.Props["longPressSlop"] = object.NewNumber(2) // 收得很紧
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 106, Y: 100}) // 6px > slop 2
	time.Sleep(40 * time.Millisecond)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 106, Y: 100})
	if lp != 0 {
		t.Fatalf("longPressSlop=2 时 6px 位移应作废长按, got %d", lp)
	}
}

// TestLongPressNoHandler 链上没有 onLongPress 时不计时 (不白跑等待预算钳制)。
func TestLongPressNoHandler(t *testing.T) {
	n := mkNode("rect", map[string]float64{"width": 260, "height": 180})
	fake, a := gestureApp(t, n)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 100, Y: 100})
	a.mu.Lock()
	_, ok := a.lpNode, !a.lpDeadline.IsZero()
	a.mu.Unlock()
	if ok {
		t.Fatal("没有 onLongPress 时不应启动长按计时")
	}
}

// TestGestureCancelledOnMouseLeave 光标离开窗口要丢弃这一趟手势。
func TestGestureCancelledOnMouseLeave(t *testing.T) {
	swiped := false
	n := gestureNode("rect", 260, 180, nil, func(object.Value) { swiped = true })
	fake, a := gestureApp(t, n)

	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 40, Y: 100})
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if a.gestureActive() {
		t.Fatal("MouseLeave 后应丢弃手势追踪")
	}
	// 之后再抬起: 不该补派一次滑动
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 200, Y: 100})
	if swiped {
		t.Fatal("手势已取消, 不该再派发 onSwipe")
	}
}

// TestGestureHandlerOnAncestor 手势回调可以写在祖先上 (点在子节点也认),
// 与 onClick 的祖先链上浮口径一致。
func TestGestureHandlerOnAncestor(t *testing.T) {
	swiped := false
	parent := gestureNode("column", 260, 180, nil, func(object.Value) { swiped = true })
	child := mkNode("rect", map[string]float64{"width": 100, "height": 100})
	parent.Children = []*GuiNode{child}
	child.Parent = parent
	fake, a := gestureApp(t, parent)

	// 点在子节点上
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 30, Y: 30})
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: 130, Y: 32})
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: 130, Y: 32})
	if !swiped {
		t.Fatal("点在子节点上时, 祖先的 onSwipe 应被派发")
	}
}

// ===== 全链路 (真 VM + 真 JS 回调) =====

// TestGestureFromJS 端到端: 脚本写的 onLongPress / onSwipe 收到的载荷字段正确
// (真 VM + 真 JS 回调, 不是 Go 侧打桩)。
func TestGestureFromJS(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		var log = [];
		render(
			h("column", null,
				h("rect", {
					width: 260, height: 180,
					longPressDelay: 30,
					onLongPress: (e) => { log.push("lp:" + e.x + "," + e.y) },
					onSwipe: (e) => { log.push("swipe:" + e.direction + "," + e.dx + "," + e.dy) }
				})
			),
			{ title: "gesture", width: 300, height: 200 }
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 两个约束叠在一起, 决定了这里必须"一步一泵"而不能一次性 push 完:
	//
	// 1. 回调必须**在事件循环里**跑: 主脚本执行结束后 currentVM 被恢复成 nil,
	//    object.CallFunction 桥找不到 VM 就静默返回 undefined —— 循环外的
	//    回调全是空操作 (condrender_test.go 的 driveSteps 注释讲过同一件事)。
	// 2. processEvents 一轮会**排空**事件队列, 而 tickLongPress 在排空之后
	//    才跑。一次性 push 完的话 MouseUp 会先把手势收尾 (清掉长按计时),
	//    长按永远等不到它的那一轮 —— 所以每条事件要单独占一轮泵。
	push := func(ev Event) func() { return func() { fake.push(ev) } }
	hold := func(ms int, ev Event) func() {
		// 先按住 ms 毫秒再推事件: 让长按计时真的走完 (阈值 30ms)。
		return func() { time.Sleep(time.Duration(ms) * time.Millisecond); fake.push(ev) }
	}
	steps := []func(){
		push(Event{Kind: EventMouseDown, X: 50, Y: 50}),
		hold(60, Event{Kind: EventMouseMove, X: 50, Y: 50}), // 到点 ⇒ 派发长按
		push(Event{Kind: EventMouseUp, X: 50, Y: 50}),       // 长按已成立 ⇒ 吞掉
		push(Event{Kind: EventMouseDown, X: 40, Y: 100}),
		push(Event{Kind: EventMouseMove, X: 140, Y: 130}),
		push(Event{Kind: EventMouseUp, X: 140, Y: 130}), // 滑动成立 ⇒ 吞掉
		push(Event{Kind: EventClose}),                   // 收尾, 让事件循环能退出
	}
	i := 0
	err = v.RunTimersWithPump(func(maxWait time.Duration) bool {
		if i < len(steps) {
			steps[i]()
			i++
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if i != len(steps) {
		t.Fatalf("只推了 %d/%d 条事件", i, len(steps))
	}
	assertLog(t, v, "lp:50,50", "swipe:right,100,30")
}

// assertLog 断言脚本全局 log 数组的全部内容。
func assertLog(t *testing.T, v *vm.VM, want ...string) {
	t.Helper()
	got := jsArrayStr(t, v, "log")
	if len(got) != len(want) {
		t.Fatalf("log = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("log[%d] = %q, want %q (全量 %v)", i, got[i], want[i], got)
		}
	}
}

func absF(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
