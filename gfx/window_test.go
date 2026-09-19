package gfx

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P3-6 多窗口 =====
//
// 分三层验:
//   - 注册表语义: 注册/摘除/activeApp 的流转 (纯 Go);
//   - 事件隔离: 两个窗口各自收自己的事件, 互不串门 (纯 Go);
//   - 生命周期: 关一个另一个继续、全关才退出 (纯 Go + 全链路各一)。
//
// **为什么用 mountTestApp 而不是自己造 app**: 它已经把注册/拆卸与
// activeApp 清理做全了 (P3-6 起 registerApp + unregisterApp)。手写
// `&app{...}` 只写 activeApp 的写法在这里必然失败 —— Pump 按注册表遍历。

// mountTwoWindows 造两个各自独立的窗口 (400x300 与 320x200)。
func mountTwoWindows(t *testing.T) (*fakeSurface, *app, *fakeSurface, *app) {
	t.Helper()
	f1 := newFakeSurface()
	f1.w, f1.h = 400, 300
	f2 := newFakeSurface()
	f2.w, f2.h = 320, 200

	r1 := mkColumn(mkButton("A1"))
	r2 := mkColumn(mkButton("B1"))

	a1 := mountAppWith(t, f1, r1)
	a2 := mountAppWith(t, f2, r2)
	return f1, a1, f2, a2
}

// mountAppWith 与 mountTestApp 同义, 但接受外部传入的 surface
// (多窗口用例需要"两个窗口"而不是"每次新建一个")。
func mountAppWith(t *testing.T, fake *fakeSurface, root *GuiNode) *app {
	t.Helper()
	a := &app{
		surface:    fake,
		root:       root,
		fullDirty:  true,
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	registerApp(a)
	t.Cleanup(func() {
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
	})
	a.redraw()
	return a
}

// mkColumn 造一个单列容器 (子节点顺序摆放)。
func mkColumn(children ...*GuiNode) *GuiNode {
	c := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	for _, ch := range children {
		ch.Parent = c
		c.Children = append(c.Children, ch)
	}
	return c
}

// ===== 注册表 =====

func TestRegisterUnregisterRegistry(t *testing.T) {
	before := WindowCount()
	fake, a := mountTestApp(t, mkColumn(mkButton("x")), 200, 100)

	if WindowCount() != before+1 {
		t.Fatalf("挂载后窗口数 = %d, want %d", WindowCount(), before+1)
	}
	if appForSurface(fake) != a {
		t.Fatalf("appForSurface 应能按 Surface 反查回同一个 app")
	}
	if !Active() {
		t.Fatalf("有窗口时 Active() 应为 true")
	}

	unregisterApp(a)
	if appForSurface(fake) != nil {
		t.Fatalf("摘除后 appForSurface 应返回 nil")
	}
	if WindowCount() != before {
		t.Fatalf("摘除后窗口数 = %d, want %d", WindowCount(), before)
	}
}

func TestActiveFalseWhenNoWindow(t *testing.T) {
	// 显式清空 (SetDefaultFactory(nil) 会顺带清注册表, 见 gfx.go)
	SetDefaultFactory(nil)
	if Active() {
		t.Fatalf("无窗口时 Active() 应为 false")
	}
	if WindowCount() != 0 {
		t.Fatalf("无窗口时 WindowCount = %d", WindowCount())
	}
}

// 关掉"最近挂载"的窗口后, activeApp 应改指另一个活着的窗口, 而不是 nil。
// 这是 P3-5 → P3-6 的一个真实回归点: 旧 close() 无条件把 activeApp 置空,
// 多窗口下会让剪贴板/原生对话框在"还剩窗口"时集体失效。
func TestActiveAppFallsBackToSurvivor(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)

	if currentApp() != a2 {
		t.Fatalf("activeApp 应指向最近挂载的 a2")
	}
	a2.close()
	if currentApp() != a1 {
		t.Fatalf("关掉 a2 后 activeApp 应回落到 a1, got %v", currentApp())
	}
	if !Active() {
		t.Fatalf("还有 a1 活着, Active() 应为 true")
	}
	a1.close()
	if currentApp() != nil {
		t.Fatalf("全关后 activeApp 应为 nil")
	}
	if Active() {
		t.Fatalf("全关后 Active() 应为 false")
	}
}

// ===== 事件隔离 =====

// 事件只影响它所属的窗口: 给 A 推 MouseDown, B 的交互态纹丝不动。
//
// 注意不能在这里断言 needDraw —— pushAndPump 内部会跑完整泵 (含重绘),
// 重绘结束脏标记就被清了。脏标记的**跨窗口**隔离由
// TestMarkNodeDirtyRoutesByOwner 直接验 (它绕开泵)。
func TestEventsDoNotLeakAcrossWindows(t *testing.T) {
	f1, a1, f2, a2 := mountTwoWindows(t)

	btn1 := findFirst(a1.root, "button")
	if btn1 == nil {
		t.Fatalf("a1 里没有 button")
	}

	// 点 A 的按钮
	pushAndPump(t, f1, a1, Event{Kind: EventMouseDown, X: btn1.Box.X + 1, Y: btn1.Box.Y + 1})
	if !btn1.pressed {
		t.Fatalf("A 的按钮应进入按压态")
	}
	for _, n := range allNodes(a2.root) {
		if n.pressed || n.hovered {
			t.Fatalf("B 的节点 %s 不应有按压/悬停态", n.Tag)
		}
	}
	// B 的重绘次数不该因为 A 的事件增加
	if shots(f2) <= 0 {
		t.Fatalf("a2 首帧应已上屏")
	}
	n2 := shots(f2)
	pushAndPump(t, f1, a1, Event{Kind: EventMouseUp, X: btn1.Box.X + 1, Y: btn1.Box.Y + 1})
	if shots(f2) != n2 {
		t.Fatalf("A 的事件不该触发 B 上屏 (B 上屏次数 %d → %d)", n2, shots(f2))
	}
}

// 两个窗口各自维护焦点: 点 A 只改 A 的焦点。
func TestFocusIsPerWindow(t *testing.T) {
	f1, a1, f2, a2 := mountTwoWindows(t)

	in1 := windowInput()
	in2 := windowInput()
	a1.root.Children = append(a1.root.Children, in1)
	in1.Parent = a1.root
	a2.root.Children = append(a2.root.Children, in2)
	in2.Parent = a2.root
	relayout(a1)
	relayout(a2)

	pushAndPump(t, f1, a1, Event{Kind: EventMouseUp, X: in1.Box.X + 2, Y: in1.Box.Y + 2})
	if a1.focused != in1 {
		t.Fatalf("A 的焦点应落到 in1")
	}
	if a2.focused == in1 || a2.focused == in2 {
		t.Fatalf("点 A 不该改 B 的焦点")
	}

	pushAndPump(t, f2, a2, Event{Kind: EventMouseUp, X: in2.Box.X + 2, Y: in2.Box.Y + 2})
	if a2.focused != in2 {
		t.Fatalf("B 的焦点应落到 in2")
	}
	if a1.focused != in1 {
		t.Fatalf("点 B 不该改 A 的焦点")
	}
}

// windowInput 造一个最小 input (无 onInput 也能命中, 有 inputMinW 缺省宽)。
// 不用 mkInput 是因为它属于单窗口用例 (p2c_test.go), 只带值参数。
func windowInput() *GuiNode {
	return &GuiNode{Tag: "input", Props: map[string]object.Value{
		"value": object.NewString(""),
	}}
}

// ===== 生命周期 =====

// 关一个另一个继续: 关 A 之后 B 仍能收事件并重绘。
func TestClosingOneWindowKeepsOtherRunning(t *testing.T) {
	_, a1, f2, a2 := mountTwoWindows(t)

	a1.close()
	if a1.surfaceAlive() {
		t.Fatalf("a1 应已关闭")
	}
	if !a2.surfaceAlive() {
		t.Fatalf("a2 不该受 a1 关闭影响")
	}

	btn2 := findFirst(a2.root, "button")
	clearDirty(a2)
	f2.push(Event{Kind: EventMouseDown, X: btn2.Box.X + 1, Y: btn2.Box.Y + 1})
	if !a2.pump(time.Millisecond) {
		t.Fatalf("a2 应仍存活")
	}
	if !btn2.pressed {
		t.Fatalf("a2 关闭后仍应能收到事件")
	}
}

// Pump: 只要还有窗口活着就返回 true; 全部关闭才 false。
func TestPumpSurvivesUntilAllClosed(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)

	// 关 a1 (直接摘注册表, 模拟窗口销毁)
	a1.close()
	// 用非零短超时: 假 Surface 把 zero 当"无限期" (等价于真后端的无事件等待),
	// 那样这里会白等 10s。语义上"还能不能继续跑"与等待时长无关。
	if !Pump(time.Millisecond) {
		t.Fatalf("还剩 a2, Pump 应返回 true")
	}
	// 关 a2
	a2.close()
	if Pump(time.Millisecond) {
		t.Fatalf("全关后 Pump 应返回 false")
	}
}

// 关闭幂等: 重复 close 不 panic、不重复摘除。
func TestCloseIsIdempotentMultiWindow(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)
	a1.close()
	a1.close()
	a1.close()
	if currentApp() != a2 {
		t.Fatalf("重复关闭 a1 不该影响 a2 的 activeApp 地位")
	}
	if WindowCount() != 1 {
		t.Fatalf("窗口数 = %d, want 1", WindowCount())
	}
}

// Window 句柄: Close 经 Post 投递 (不在调用线程直接销毁), 幂等。
func TestWindowHandleCloseViaPost(t *testing.T) {
	fake, a := mountTestApp(t, mkColumn(mkButton("x")), 200, 100)
	w := &Window{a: a}
	_ = fake

	if w.closed() {
		t.Fatalf("刚挂载不该是已关闭")
	}
	w.Close()
	// Post 只是排队, 此刻还没执行 —— 这正是"不在 JS 线程直接销毁"的体现
	if w.closed() {
		t.Fatalf("Close 应经 Post 排队, 调用后立即调用不该已关闭")
	}
	DrainTasks()
	if !w.closed() {
		t.Fatalf("DrainTasks 后应已关闭")
	}
	if appForSurface(fake) != nil {
		t.Fatalf("关闭后应从注册表摘除")
	}
	// 重复调用不 panic
	w.Close()
	DrainTasks()
}

// appOfNode: 按节点归属找到正确的窗口 (多窗口标脏路由的基础)。
func TestAppOfNodeRoutesToOwningWindow(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)

	n1 := findFirst(a1.root, "button")
	n2 := findFirst(a2.root, "button")
	if appOfNode(n1) != a1 {
		t.Fatalf("n1 应归属 a1")
	}
	if appOfNode(n2) != a2 {
		t.Fatalf("n2 应归属 a2")
	}
	// 未挂载的游离节点: 回退 activeApp (不是 nil —— 接线期 effect 会走到这)
	orphan := mkButton("free")
	if appOfNode(orphan) != currentApp() {
		t.Fatalf("游离节点应回退到 activeApp")
	}
}

// markNodeDirty 的多窗口路由: 标 A 的节点只脏 A。
func TestMarkNodeDirtyRoutesByOwner(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)
	clearDirty(a1)
	clearDirty(a2)

	n1 := findFirst(a1.root, "button")
	markNodeDirty(n1)
	if !needDraw(a1) {
		t.Fatalf("a1 应被标脏")
	}
	if needDraw(a2) {
		t.Fatalf("a2 不该被标脏")
	}
	if _, ok := a1.dirtyNodes[n1]; !ok {
		t.Fatalf("a1.dirtyNodes 应含 n1")
	}

	clearDirty(a1)
	clearDirty(a2)
	n2 := findFirst(a2.root, "button")
	markFullDirtyFor(n2)
	if !a2.fullDirty {
		t.Fatalf("markFullDirtyFor 应整帧标脏 a2")
	}
	if a1.fullDirty {
		t.Fatalf("a1 不该被整帧标脏")
	}
}

// 多窗口下 Pump 必须只在**一轮**里 DrainTasks 一次 (否则任务被执行多遍)。
func TestPumpDrainsTasksOncePerRound(t *testing.T) {
	_, a1, _, a2 := mountTwoWindows(t)
	_ = a1
	_ = a2

	calls := 0
	Post(func() { calls++ })
	Pump(time.Millisecond)
	if calls != 1 {
		t.Fatalf("一次 Pump 里任务应只执行一次, got %d", calls)
	}
}

// ===== 全链路 (真 VM) =====

// 脚本开两个窗口, 各自独立计数; 关掉一个另一个继续, 全关退出。
func TestMultiWindowFullChain(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	var (
		fakeA, fakeB *fakeSurface
		created      int
	)
	// 工厂每次 Create 返回一个新的假 Surface (每次 render 一个新窗口)
	SetDefaultFactory(&seqFactory{
		onCreate: func(idx int, s *fakeSurface) {
			created++
			if idx == 0 {
				fakeA = s
			} else {
				fakeB = s
			}
		},
	})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let clicksA = 0;
		let clicksB = 0;
		const wA = render(
			h("column", null,
				h("button", { onClick: () => { clicksA++; } }, "A")
			),
			{ title: "A", width: 300, height: 200 }
		);
		const wB = render(
			h("column", null,
				h("button", { onClick: () => { clicksB++; } }, "B")
			),
			{ title: "B", width: 300, height: 200 }
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if created != 2 {
		t.Fatalf("应创建 2 个窗口, got %d", created)
	}
	if WindowCount() != 2 {
		t.Fatalf("注册表应有 2 个窗口, got %d", WindowCount())
	}
	if fakeA == nil || fakeB == nil {
		t.Fatalf("两个假 Surface 都应被注入")
	}

	// render 的返回值必须是对象且带 close
	wAVal := globalVal(t, v, "wA")
	wBVal := globalVal(t, v, "wB")
	if _, ok := wAVal.(*object.Object); !ok {
		t.Fatalf("render 应返回窗口句柄对象, got %T", wAVal)
	}
	if _, ok := wBVal.(*object.Undefined); ok {
		t.Fatalf("render 不该再返回 undefined")
	}

	// 找出两个窗口的按钮
	rootA := rootOfSurface(t, fakeA)
	rootB := rootOfSurface(t, fakeB)
	btnA := findFirst(rootA, "button")
	btnB := findFirst(rootB, "button")
	if btnA == nil || btnB == nil {
		t.Fatalf("两个窗口都应各有一个按钮")
	}

	round := 0
	err = v.RunTimersWithPump(func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			// 点 A 的按钮
			fakeA.push(Event{Kind: EventMouseUp, X: btnA.Box.X + 1, Y: btnA.Box.Y + 1})
		case 2:
			// 点 B 的按钮
			fakeB.push(Event{Kind: EventMouseUp, X: btnB.Box.X + 1, Y: btnB.Box.Y + 1})
		case 3:
			// 关掉 A: 应不影响 B。
			// 顺手给 B 推一个无害事件: 假 Surface 在"没有事件"时会把
			// 无限期等待退化成 10s 超时, 不推的话这一轮白等 10 秒。
			if m, ok := wAVal.(*object.Object); ok {
				if fn, ok := m.GetProperty("close"); ok {
					callScriptFn(fn)
					DrainTasks() // 让关闭真的落地
				}
			}
			fakeB.push(Event{Kind: EventMouseLeave})
		case 4:
			// A 关了, 再点 B 仍应生效
			fakeB.push(Event{Kind: EventMouseUp, X: btnB.Box.X + 1, Y: btnB.Box.Y + 1})
		default:
			// 收尾: 关 B 并送 Close 事件让循环退出
			if m, ok := wBVal.(*object.Object); ok {
				if fn, ok := m.GetProperty("close"); ok {
					callScriptFn(fn)
				}
			}
			fakeB.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	if got, _ := globalNum(t, v, "clicksA"); got != 1 {
		t.Fatalf("A 的计数 = %v, want 1", got)
	}
	if got, _ := globalNum(t, v, "clicksB"); got != 2 {
		t.Fatalf("B 的计数 = %v, want 2 (关掉 A 后仍能继续点击)", got)
	}
}

// seqFactory 每次 Create 都造一个新的假 Surface (多窗口场景专用:
// fakeFactory 每次返回**同一个**, 那只能用来验单窗口)。
//
// 它把造出来的 surface 按创建序记下来, 于是用例可以按"第 1 个窗口 /
// 第 2 个窗口"寻址 —— 这正是多窗口断言的基本需要 (Pump 会同时驱动它们)。
type seqFactory struct {
	made []*fakeSurface
	// onCreate 在每次建窗后回调 (可空); 演示脚本用例用它做即时断言。
	onCreate func(idx int, s *fakeSurface)
}

func (f *seqFactory) Create(cfg WindowConfig) (Surface, error) {
	s := newFakeSurface()
	f.made = append(f.made, s)
	if f.onCreate != nil {
		f.onCreate(len(f.made)-1, s)
	}
	return s, nil
}

// window 按创建序取第 i 个窗口的假 Surface。
func (f *seqFactory) window(t *testing.T, i int) *fakeSurface {
	t.Helper()
	if i < 0 || i >= len(f.made) {
		t.Fatalf("没有第 %d 个窗口 (共 %d 个)", i, len(f.made))
	}
	return f.made[i]
}

// rootOfSurface 按 Surface 反查窗口的根节点 (全链路多窗口断言用)。
func rootOfSurface(t *testing.T, s Surface) *GuiNode {
	t.Helper()
	a := appForSurface(s)
	if a == nil {
		t.Fatalf("该 Surface 未注册窗口")
	}
	return a.root
}

// clearDirty 清掉窗口的脏标记 (断言"是否被标脏"前先复位)。
func clearDirty(a *app) {
	a.mu.Lock()
	a.needDraw = false
	a.fullDirty = false
	a.dirtyNodes = map[*GuiNode]struct{}{}
	a.mu.Unlock()
}

// ===== 演示脚本 =====

// TestMultiWindowDemoScript 跑一遍 P3-6 的演示脚本: 它是多窗口的真实 JSX 用法,
// 验证"两个窗口都挂上、计数互相独立、关一个另一个继续、全关退出"。
//
// 为什么**不**登记进 TestExampleScriptsMount: 那个用例的假工厂每次返回
// **同一个** Surface (它验的是单窗口演示), 两个窗口注册到同一个键上会互相
// 覆盖。多窗口需要 seqFactory (每次一个新 Surface), 所以单独立一个用例 ——
// 与 image_demo.js 单独立 TestImageDemoScript 是同一个理由。
func TestMultiWindowDemoScript(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", "multiwindow_demo.js"))
	if err != nil {
		t.Fatalf("读取演示脚本: %v", err)
	}
	factory := &seqFactory{}
	SetDefaultFactory(factory)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if len(factory.made) != 2 {
		t.Fatalf("演示应开 2 个窗口, 实际 %d", len(factory.made))
	}
	if WindowCount() != 2 {
		t.Fatalf("注册表应有 2 个窗口, 实际 %d", WindowCount())
	}

	fA, fB := factory.window(t, 0), factory.window(t, 1)
	rootA, rootB := rootOfSurface(t, fA), rootOfSurface(t, fB)

	// ===== 几何: 两个窗口各自有完整的一棵独立树 =====
	if rootA == rootB {
		t.Fatalf("两个窗口应是各自独立的元素树")
	}
	if n := countTag(rootA, "menubar"); n != 1 {
		t.Fatalf("A 的 menubar 数量 = %d, want 1", n)
	}
	if n := countTag(rootB, "menubar"); n != 1 {
		t.Fatalf("B 的 menubar 数量 = %d, want 1", n)
	}
	// 两个窗口的计数文本初始都应是各自标题 + 0
	textA := findFirst(rootA, "#text")
	_ = textA
	if bx := findFirst(rootB, "button"); bx == nil || bx.Box.W <= 0 {
		t.Fatalf("B 的按钮应有尺寸: %v", bx)
	}

	// 初始帧: 两个窗口各自都上过屏
	if shots(fA) < 1 || shots(fB) < 1 {
		t.Fatalf("两个窗口都应出过首帧 (%d / %d)", shots(fA), shots(fB))
	}

	// ===== 独立计数 =====
	// 找 "+1" 按钮 (两个窗口各一个, 取各自的第一个 row 里的第一个按钮)
	btnA := plusButtonOf(t, rootA)
	btnB := plusButtonOf(t, rootB)

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		switch round {
		case 1:
			fA.push(Event{Kind: EventMouseUp, X: btnA.Box.X + 2, Y: btnA.Box.Y + 2})
			fA.push(Event{Kind: EventMouseUp, X: btnA.Box.X + 2, Y: btnA.Box.Y + 2})
		case 2:
			fB.push(Event{Kind: EventMouseUp, X: btnB.Box.X + 2, Y: btnB.Box.Y + 2})
		case 3:
			// 关掉 A: 不应影响 B
			closeWindowViaScript(t, v, "windowA")
		case 4:
			// A 已关, B 继续可点
			fB.push(Event{Kind: EventMouseUp, X: btnB.Box.X + 2, Y: btnB.Box.Y + 2})
		default:
			// 收尾: 关 B, 让事件循环退出
			closeWindowViaScript(t, v, "windowB")
			fB.push(Event{Kind: EventClose})
		}
		// 每轮补一个无害唤醒: 队列空时假 Surface 会按 10s 超时长阻塞
		fB.push(Event{Kind: EventMouseLeave})
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("全关后应用未退出")
	}

	// 文本断言: A 点 2 次 → 2; B 点 2 次 → 2 (关掉 A 之后那次也算上)
	if got := counterText(rootA); got != "A count = 2" {
		t.Fatalf("A 的计数文本 = %q, want \"A count = 2\"", got)
	}
	if got := counterText(rootB); got != "B count = 2" {
		t.Fatalf("B 的计数文本 = %q, want \"B count = 2\" (关掉 A 后仍应继续)", got)
	}
}

// plusButtonOf 找计数窗口里的 "+1" 按钮 (第三行 row 的第一个 button)。
func plusButtonOf(t *testing.T, root *GuiNode) *GuiNode {
	t.Helper()
	btns := findAll(root, "button")
	if len(btns) == 0 {
		t.Fatalf("窗口里没有 button")
	}
	// 布局顺序: +1 / -1 / reset / Close this window ⇒ 第一个就是 +1
	return btns[0]
}

// counterText 取形如 "X count = N" 的文本节点内容。
func counterText(root *GuiNode) string {
	for _, n := range allNodes(root) {
		if n.Tag == "#text" && len(n.Text) > 7 && n.Text[1:7] == " count" {
			return n.Text
		}
	}
	return ""
}

// closeWindowViaScript 调脚本里的 globalThis.windowX.close()。
func closeWindowViaScript(t *testing.T, v *vm.VM, name string) {
	t.Helper()
	val, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失 (render 的返回值没存进去?)", name)
	}
	o, ok := val.(*object.Object)
	if !ok {
		t.Fatalf("全局 %s 不是窗口句柄对象: %T", name, val)
	}
	fn, ok := o.GetProperty("close")
	if !ok {
		t.Fatalf("%s 没有 close 方法", name)
	}
	callScriptFn(fn)
	DrainTasks() // 让 Post 排队的销毁动作落地
}

// ===== render 的窗口配置形态 =====
//
// render 有三种合法形态 (见 jsRender): <window> 根元素 / 普通配置对象 / 全默认。
// 这里验"配置真的到达建窗工厂"与各错误分支返回 Error 而不是 panic。

// cfgFactory 记录每次 Create 收到的窗口配置 (断言 render 把配置传对了),
// 并让假 Surface 的尺寸跟随配置 —— 布局结果与窗口配置一致才算数。
type cfgFactory struct{ cfgs []WindowConfig }

func (f *cfgFactory) Create(cfg WindowConfig) (Surface, error) {
	f.cfgs = append(f.cfgs, cfg)
	s := newFakeSurface()
	s.w, s.h = cfg.Width, cfg.Height
	return s, nil
}

// evalRender 在真 VM 里跑一段 render 脚本 (走完整 JSX → h → render 链路),
// 返回 VM 与录制了建窗配置的工厂。
func evalRender(t *testing.T, src string) (*vm.VM, *cfgFactory) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	factory := &cfgFactory{}
	SetDefaultFactory(factory)
	t.Cleanup(func() {
		SetDefaultFactory(nil)
		for _, a := range appsSnapshot() {
			a.close()
		}
	})
	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	return v, factory
}

// TestRenderWindowElementCarriesConfig: JSX 里 <window> 作根 —— 配置从它的
// props 读出, 布局根换成它的子元素, window 本身不进树。
func TestRenderWindowElementCarriesConfig(t *testing.T) {
	v, factory := evalRender(t, `
		import { h, render } from "gx/gfx";
		const w = render(
			<window title="W" width={220} height={460}>
				<column gap={8}><button>A</button></column>
			</window>
		);
	`)

	if WindowCount() != 1 {
		t.Fatalf("应挂载 1 个窗口, got %d", WindowCount())
	}
	if len(factory.cfgs) != 1 {
		t.Fatalf("建窗应发生 1 次, got %d", len(factory.cfgs))
	}
	if got := factory.cfgs[0]; got != (WindowConfig{Title: "W", Width: 220, Height: 460}) {
		t.Fatalf("窗口配置 = %+v, want {W 220 460}", got)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()
	if root.Tag != "column" {
		t.Fatalf("布局根应是 column (<window> 已拆包), got %s", root.Tag)
	}
	for _, n := range allNodes(root) {
		if n.Tag == "window" {
			t.Fatalf("<window> 不该出现在元素树里")
		}
	}
	// 句柄语义不受形态影响: 返回对象且 close 能关掉它
	if _, ok := globalVal(t, v, "w").(*object.Object); !ok {
		t.Fatalf("render 应返回窗口句柄对象, got %T", globalVal(t, v, "w"))
	}
	closeWindowViaScript(t, v, "w")
	if WindowCount() != 0 {
		t.Fatalf("close 后窗口数 = %d, want 0", WindowCount())
	}
}

// TestRenderConfigObjectForm: h() 手拼树的形态 —— 第二参数是普通对象。
func TestRenderConfigObjectForm(t *testing.T) {
	_, factory := evalRender(t, `
		import { h, render } from "gx/gfx";
		render(h("column", null, h("button", null, "A")), { title: "T", width: 320, height: 200 });
	`)
	if got := factory.cfgs[0]; got != (WindowConfig{Title: "T", Width: 320, Height: 200}) {
		t.Fatalf("窗口配置 = %+v, want {T 320 200}", got)
	}
}

// TestRenderDefaultsWhenNoConfig: 配置整体省略 → Gox 400x300。
func TestRenderDefaultsWhenNoConfig(t *testing.T) {
	_, factory := evalRender(t, `
		import { h, render } from "gx/gfx";
		render(h("column"));
	`)
	if got := factory.cfgs[0]; got != (WindowConfig{Title: "Gox", Width: 400, Height: 300}) {
		t.Fatalf("缺省配置 = %+v, want {Gox 400 300}", got)
	}
}

// TestRenderFormErrors: 各错误分支返回 Error 值 (而不是 panic / 静默挂载)。
func TestRenderFormErrors(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	col := mkColumn(mkButton("x"))
	winOf := func(children ...*GuiNode) *GuiNode {
		n := &GuiNode{Tag: "window", Props: map[string]object.Value{}}
		for _, c := range children {
			c.Parent = n
			n.Children = append(n.Children, c)
		}
		return n
	}
	cases := []struct {
		name string
		args []object.Value
	}{
		{"window 无子元素", []object.Value{winOf()}},
		{"window 两个子元素", []object.Value{winOf(col, mkColumn(mkButton("y")))}},
		{"window 根又带配置对象", []object.Value{winOf(col), object.NewObject()}},
		{"第二个参数不是对象", []object.Value{col, object.NewString("x")}},
		{"嵌套 window", []object.Value{winOf(winOf(col))}},
		{"第一个参数不是元素", []object.Value{object.NewString("x")}},
		{"无参数", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := jsRender(tc.args...)
			if _, ok := got.(*object.Error); !ok {
				t.Fatalf("应返回 Error, 实际 %s (%s)", got.Type(), got.Inspect())
			}
		})
	}
}
