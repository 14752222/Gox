package gfx

import (
	"fmt"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P1-1 / P1-3 / P1-4: 事件地基 / 焦点 / 悬停按压 =====
//
// 断言分两层:
//   - 纯 Go 层: 直接组装 app (不跑 VM) 调 pump, 精确断言事件分发对节点状态
//     与脏标记的影响 (悬停未变化不能标脏这类性能约束只能这样验);
//   - 全链路: 假 Surface + 真 VM, 断言脚本回调收到的事件参数确实正确。

// 交互反馈色 (与 raster.go 的偏移量对齐, 供逐像素断言)
var (
	pxBtnHover = color.RGBA{R: 244, G: 244, B: 244, A: 255} // #e8e8e8 +12
	pxBtnPress = color.RGBA{R: 208, G: 208, B: 208, A: 255} // #e8e8e8 -24
	pxRing     = color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255}
)

// mountTestApp 组装一个挂到假 Surface 上的 app 并出首帧 (不起 VM)。
// 事件分发与脏标记的断言需要"一次 pump 一次观察", 走 VM 事件循环做不到。
//
// 注意: 没有 VM 时 object.CallFunction 是空操作 (回调桥要求 currentVM),
// 所以这里只断言节点状态 / 脏标记 / 像素; 回调参数与调用顺序走全链路测试。
func mountTestApp(t *testing.T, root *GuiNode, w, h int) (*fakeSurface, *app) {
	t.Helper()
	fake := newFakeSurface()
	fake.w, fake.h = w, h
	a := &app{
		surface:    fake,
		root:       root,
		fullDirty:  true,
		dirtyNodes: map[*GuiNode]struct{}{},
	}
	// P3-6: 登记进注册表 (不只是 activeApp) —— Pump 现在按注册表遍历窗口,
	// 只写 activeApp 的替身会在 Pump 里被完全看不见。
	registerApp(a)
	t.Cleanup(func() {
		// 摘除 + 清 activeApp: 注册表是**包级**状态, 留着会让下一个用例的
		// Pump 去等一个再也不会来事件的假 Surface (表现为整个测试挂住)。
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
	})
	a.redraw()
	return fake, a
}

// pushAndPump 注入一个事件并跑一轮 pump (处理事件 + 按需重绘)。
func pushAndPump(t *testing.T, fake *fakeSurface, a *app, ev Event) {
	t.Helper()
	fake.push(ev)
	if !a.pump(time.Millisecond) {
		t.Fatalf("pump 意外结束 (窗口被关闭)")
	}
}

// needDraw 读当前脏标记。
func needDraw(a *app) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.needDraw
}

// findAll 收集指定标签的全部节点 (声明序)。
func findAll(root *GuiNode, tag string) []*GuiNode {
	var out []*GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == tag {
			out = append(out, n)
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return out
}

// mkButton 造一个带文本子节点的按钮 (标签文本是命中测试回溯的常见场景)。
func mkButton(label string) *GuiNode {
	b := &GuiNode{Tag: "button", Props: map[string]object.Value{}}
	b.Children = []*GuiNode{{Tag: "#text", Text: label, Props: map[string]object.Value{}}}
	b.Children[0].Parent = b
	return b
}

// withClick 给节点挂一个空的 onClick (让 HitTest 能命中它)。
func withClick(n *GuiNode) *GuiNode {
	n.Props["onClick"] = object.NewBuiltin("click", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	return n
}

// ===== 命中测试 / 事件链 =====

func TestHitTestDeepAndHandlerChain(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	btn := withClick(mkButton("OK"))
	root.Children = []*GuiNode{btn}
	Layout(root, 400, 300)

	label := btn.Children[0]
	// 点在文字上: 深层命中是 #text 节点, 但处理器在按钮上
	lx, ly := label.Box.X+1, label.Box.Y+1
	if got := HitTestDeep(root, lx, ly); got != label {
		t.Fatalf("HitTestDeep 命中的是 %v, want 文字节点", got)
	}
	if got := handlerInChain(label, "onClick"); got != btn {
		t.Fatalf("onClick 未沿祖先链上浮到按钮: %v", got)
	}
	// 点在按钮的空白处 (padding 区域) → 命中按钮自身
	if got := HitTestDeep(root, btn.Box.X+1, btn.Box.Y+1); got != btn {
		t.Fatalf("HitTestDeep 命中 %v, want 按钮", got)
	}
	// 点空白 → 最深命中是根容器 (HitTest 仍返回 nil: 根没有 onClick)
	if got := HitTestDeep(root, 395, 295); got != root {
		t.Fatalf("空白处最深命中应为根, got %v", got)
	}
	if got := HitTest(root, 395, 295); got != nil {
		t.Fatalf("空白处 onClick 命中应为 nil, got %v", got)
	}
	if got := HitTestDeep(root, -5, -5); got != nil {
		t.Fatalf("根之外应返回 nil, got %v", got)
	}
	if got := handlerInChain(root, "onWheel"); got != nil {
		t.Fatalf("无处理器时应返回 nil, got %v", got)
	}
}

func TestHoverAndPressChains(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkButton("OK"))
	plain := mkNode("rect", map[string]float64{"width": 40, "height": 10})
	root.Children = []*GuiNode{btn, plain}
	Layout(root, 400, 300)

	// 悬停链只收"有面"的交互组件: 悬停在按钮文字上 → 按钮本体亮起
	label := btn.Children[0]
	if got := hoverChainOf(label); len(got) != 1 || got[0] != btn {
		t.Fatalf("hoverChainOf(文字) = %v, want [按钮]", got)
	}
	// 普通 rect 挂着 onClick 也不会进悬停链 (没有可提亮的"面")
	withClick(plain)
	if got := hoverChainOf(plain); len(got) != 0 {
		t.Fatalf("普通盒子不该进悬停链: %v", got)
	}

	// app.setHover 做的是链的差集: 进入置 true, 离开置 false
	a := &app{}
	a.setHover(label)
	if !btn.hovered {
		t.Fatalf("悬停文字时按钮本体应 hovered")
	}
	a.setHover(plain)
	if btn.hovered {
		t.Fatalf("离开按钮后 hovered 应复位")
	}
	a.setHover(nil)
	if plain.hovered {
		t.Fatalf("鼠标离开整个区域后 hovered 应全部复位")
	}

	// 按压链与悬停链同一批组件: 只有"有面"的交互组件会呈现按压反馈
	if got := pressChainOf(label); len(got) != 1 || got[0] != btn {
		t.Fatalf("pressChainOf(文字) = %v, want [按钮]", got)
	}
	if got := pressChainOf(plain); len(got) != 0 {
		t.Fatalf("普通盒子不该可按压 (没有可压暗的面): %v", got)
	}
	a.setPress(pressChainOf(label))
	if !btn.pressed {
		t.Fatalf("按下后按钮应 pressed")
	}
	a.setPress(nil)
	if btn.pressed {
		t.Fatalf("松开后 pressed 应复位")
	}
}

// ===== 悬停: 状态翻转必须伴随重绘, 未变化不得重绘 =====

func TestHoverDrivesRedrawOnlyOnChange(t *testing.T) {
	root := mkNode("column", map[string]float64{"gap": 8, "padding": 10})
	btn := withClick(mkButton("OK"))
	other := mkNode("rect", map[string]float64{"width": 40, "height": 20})
	root.Children = []*GuiNode{btn, other}
	fake, a := mountTestApp(t, root, 400, 300)

	if needDraw(a) {
		t.Fatalf("首帧后不该残留脏标记")
	}

	// 1) 进入按钮: 悬停变化 → 标脏 → 重绘
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if !btn.hovered {
		t.Fatalf("鼠标进入按钮后应 hovered")
	}
	if needDraw(a) {
		t.Fatalf("重绘后不该残留脏标记")
	}

	// 2) 在按钮内继续移动: 悬停链不变 → 绝不能标脏 (MouseMove 是最高频事件)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 3, Y: btn.Box.Y + 3})
	if needDraw(a) {
		t.Fatalf("悬停链未变化时不应标脏")
	}
	if !btn.hovered {
		t.Fatalf("悬停状态不该被清掉")
	}

	// 3) 移到普通 rect 上: 按钮退出悬停链 → 标脏
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: other.Box.X + 2, Y: other.Box.Y + 2})
	if needDraw(a) {
		t.Fatalf("重绘后不该残留脏标记")
	}
	if btn.hovered {
		t.Fatalf("离开按钮后应复位 hovered")
	}

	// 4) 光标离开客户区: 全部复位
	pushAndPump(t, fake, a, Event{Kind: EventMouseLeave})
	if other.hovered || btn.hovered {
		t.Fatalf("MouseLeave 后悬停应全部复位")
	}

	// 5) 悬停态确实画进了帧里: 重新悬停后按钮中心应为提亮色
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	img := shotsImage(fake)
	if img == nil {
		t.Fatalf("未捕获上屏帧")
	}
	assertPx(t, img, btn.Box.X+3, btn.Box.Y+btn.Box.H/2, pxBtnHover, "悬停提亮")
}

func TestPressStateVisualAndDispatch(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkButton("OK"))
	root.Children = []*GuiNode{btn}
	fake, a := mountTestApp(t, root, 200, 120)

	cx, cy := btn.Box.X+btn.Box.W/2, btn.Box.Y+btn.Box.H/2

	// 按下: 记录按压目标 + 标脏 (画面压暗)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: cx, Y: cy})
	if !btn.pressed {
		t.Fatalf("MouseDown 后按钮应 pressed")
	}
	img := shotsImage(fake)
	assertPx(t, img, btn.Box.X+3, cy, pxBtnPress, "按压压暗")

	// 松开: 复位 + 标脏
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: cx, Y: cy})
	if btn.pressed {
		t.Fatalf("MouseUp 后 pressed 应复位")
	}
	assertPx(t, shotsImage(fake), btn.Box.X+3, cy, pxBtnFace, "松开后恢复底色")

	// 点空白区域不该产生按压态
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: 195, Y: 115})
	if btn.pressed {
		t.Fatalf("点空白处不该让按钮进入按压态")
	}

	// 禁用按钮既不进入按压态, 也不触发 onClick
	root2 := mkNode("column", nil)
	dis := withBool(withClick(mkButton("NO")), "disabled", true)
	root2.Children = []*GuiNode{dis}
	fake2, a2 := mountTestApp(t, root2, 200, 120)
	pushAndPump(t, fake2, a2, Event{Kind: EventMouseDown, X: dis.Box.X + 2, Y: dis.Box.Y + 2})
	if dis.pressed {
		t.Fatalf("禁用按钮不该进入按压态")
	}
}

// ===== 焦点: 虚线焦点框像素 (回调顺序见全链路测试) =====

func TestFocusRingPixels(t *testing.T) {
	root := mkNode("column", map[string]float64{"gap": 10, "padding": 12})
	a1, b1 := withClick(mkButton("A")), withClick(mkButton("B"))
	root.Children = []*GuiNode{a1, b1}
	fake, app := mountTestApp(t, root, 300, 160)

	// 首帧: 无焦点 → 无焦点框
	if c := shotsImage(fake).RGBAAt(a1.Box.X+1, a1.Box.Y+1); c == pxRing {
		t.Fatalf("首帧不该有焦点框")
	}

	pushAndPump(t, fake, app, Event{Kind: EventMouseUp, X: a1.Box.X + 2, Y: a1.Box.Y + 2})
	if app.focused != a1 {
		t.Fatalf("点击后焦点应落在 A 上: %v", app.focused)
	}
	// 焦点框落在 A 的盒内 1px 处 (2px 实线起笔)
	assertPx(t, shotsImage(fake), a1.Box.X+1, a1.Box.Y+1, pxRing, "A 的焦点框")
	if c := shotsImage(fake).RGBAAt(b1.Box.X+1, b1.Box.Y+1); c == pxRing {
		t.Fatalf("B 不该有焦点框")
	}

	pushAndPump(t, fake, app, Event{Kind: EventMouseUp, X: b1.Box.X + 2, Y: b1.Box.Y + 2})
	img := shotsImage(fake)
	assertPx(t, img, b1.Box.X+1, b1.Box.Y+1, pxRing, "B 的焦点框")
	if c := img.RGBAAt(a1.Box.X+1, a1.Box.Y+1); c == pxRing {
		t.Fatalf("A 的旧焦点框未被擦掉 (局部重绘下会残留)")
	}
}

// TestFocusBlurOrderFullChain 焦点回调顺序必须走真 VM: Go 侧直接组装的
// app 没有 currentVM, CallFunction 是空操作。
func TestFocusBlurOrderFullChain(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let log = "";
		const block = (name) => h("button", {
			onClick: () => 0,
			onFocus: () => { log = log + "focus:" + name + ";"; },
			onBlur: () => { log = log + "blur:" + name + ";"; },
		}, name);
		const ui = h("column", {gap: 10, padding: 12}, block("A"), block("B"));
		render(ui, {title: "T", width: 300, height: 160});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	btns := findAll(root, "button")
	if len(btns) != 2 {
		t.Fatalf("按钮数量 = %d, want 2", len(btns))
	}
	fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
	fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 3, Y: btns[0].Box.Y + 3}) // 同节点重复点击
	fake.push(Event{Kind: EventMouseUp, X: btns[1].Box.X + 2, Y: btns[1].Box.Y + 2})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	val, _ := v.Globals().Get("log")
	s, _ := val.(*object.String)
	want := "focus:A;blur:A;focus:B;"
	if s == nil || s.Value != want {
		t.Fatalf("焦点事件序列 = %q, want %q", s, want)
	}
}

func TestFocusRingHideProp(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkButton("OK"))
	root.Children = []*GuiNode{btn}
	root.Props["hideFocusRing"] = object.NewBoolean(true)
	fake, a := mountTestApp(t, root, 200, 120)

	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if c := shotsImage(fake).RGBAAt(btn.Box.X+1, btn.Box.Y+1); c == pxRing {
		t.Fatalf("hideFocusRing=true 时不该画焦点框")
	}
}

// ===== 全链路: 脚本侧确实收到正确的事件参数 =====

func TestMouseAndKeyEventsFullChain(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let move = "", wheel = "", ctx = "", down = "", up = "", focused = "", blurred = "";
		const target = h("button", {
			onClick: () => 0,
			onMouseMove: (e) => { move = e.x + "," + e.y; },
			onWheel: (e) => { wheel = e.deltaY; },
			onContextMenu: (e) => { ctx = e.x + "," + e.y; },
			onKeyDown: (e) => { down = e.key + (e.ctrl ? "+ctrl" : "") + (e.shift ? "+shift" : "") + (e.alt ? "+alt" : ""); },
			onKeyUp: (e) => { up = e.key + (e.ctrl ? "+ctrl" : ""); },
			onFocus: () => { focused = "in"; },
			onBlur: () => { blurred = "out"; },
		}, "T");
		const ui = h("column", null, target, h("rect", {width: 40, height: 20, onClick: () => 0}));
		render(ui, {title: "T", width: 400, height: 300});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	btns := findAll(root, "button")
	if len(btns) != 1 {
		t.Fatalf("按钮数量 = %d", len(btns))
	}
	x, y := btns[0].Box.X+2, btns[0].Box.Y+2

	fake.push(Event{Kind: EventMouseMove, X: x, Y: y})
	fake.push(Event{Kind: EventMouseWheel, X: x, Y: y, DeltaY: 120})
	fake.push(Event{Kind: EventMouseRightUp, X: x, Y: y})
	fake.push(Event{Kind: EventMouseUp, X: x, Y: y}) // 点击 → 焦点落到按钮
	fake.push(Event{Kind: EventKeyDown, Key: "s", Ctrl: true, Shift: true})
	fake.push(Event{Kind: EventKeyUp, Key: "s", Ctrl: true})
	// 点第二个节点 → 焦点转移, 按钮收到 onBlur
	rect := findFirst(root, "rect")
	fake.push(Event{Kind: EventMouseUp, X: rect.Box.X + 1, Y: rect.Box.Y + 1})
	fake.push(Event{Kind: EventClose})

	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	pos := fmt.Sprintf("%d,%d", x, y)
	got := func(name string) string {
		val, ok := v.Globals().Get(name)
		if !ok {
			t.Fatalf("全局变量 %s 缺失", name)
		}
		s, _ := val.(*object.String)
		if s == nil {
			t.Fatalf("%s 不是字符串: %v", name, val)
		}
		return s.Value
	}
	if s := got("move"); s != pos {
		t.Fatalf("onMouseMove 参数 = %q, want %q", s, pos)
	}
	// Go 层 DeltaY 向上为正, 脚本侧按 DOM 约定取反 → 这里应是 -120
	wheelVal, _ := v.Globals().Get("wheel")
	if n, ok := wheelVal.(*object.Number); !ok || n.Value != -120 {
		t.Fatalf("onWheel deltaY = %v, want -120", wheelVal)
	}
	if s := got("ctx"); s != pos {
		t.Fatalf("onContextMenu 坐标 = %q, want %q", s, pos)
	}
	if s := got("down"); s != "s+ctrl+shift" {
		t.Fatalf("onKeyDown 参数 = %q, want s+ctrl+shift", s)
	}
	if s := got("up"); s != "s+ctrl" {
		t.Fatalf("onKeyUp 参数 = %q, want s+ctrl", s)
	}
	if s := got("focused"); s != "in" {
		t.Fatalf("onFocus 未触发: %q", s)
	}
	if s := got("blurred"); s != "out" {
		t.Fatalf("onBlur 未触发: %q", s)
	}
}

// TestEventHandlerErrorsDoNotBreakLoop 回调抛异常时后续事件仍要处理。
func TestEventHandlerErrorsDoNotBreakLoop(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let seen = 0;
		const ui = h("column", null,
			h("button", {
				onClick: () => 0,
				onMouseMove: () => { throw new Error("boom"); },
				onWheel: () => { seen = seen + 1; },
			}, "T"));
		render(ui, {title: "T", width: 400, height: 300});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	fake.push(Event{Kind: EventMouseMove, X: 2, Y: 2})
	fake.push(Event{Kind: EventMouseWheel, X: 2, Y: 2, DeltaY: -120})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	val, _ := v.Globals().Get("seen")
	num, _ := val.(*object.Number)
	if num == nil || num.Value != 1 {
		t.Fatalf("回调抛异常后事件循环中断了: seen = %v", val)
	}
}

// TestStrokeDashedRect 虚线框: 实/空交替, 四边都在。
func TestStrokeDashedRect(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 20, 12))
	FillRect(img, Rect{0, 0, 20, 12}, pxWhite)
	StrokeDashedRect(img, Rect{2, 2, 16, 8}, pxRing, 2, 2)

	assertPx(t, img, 2, 2, pxRing, "虚线起点")
	assertPx(t, img, 3, 2, pxRing, "虚线第二像素")
	assertPx(t, img, 4, 2, pxWhite, "虚线的空档")
	assertPx(t, img, 2, 9, pxRing, "底边虚线")
	// 左边: y=2,3 实 / y=4,5 空 / y=6,7 实
	assertPx(t, img, 2, 4, pxWhite, "左边空档")
	assertPx(t, img, 2, 6, pxRing, "左边实线")
	// 内部不受影响
	assertPx(t, img, 10, 5, pxWhite, "虚线框内部")
}
