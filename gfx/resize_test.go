package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 屏幕适配 A: onResize 窗口级事件 (2026-09-19 拍板落地) =====
//
// EventResize 在"标脏整帧"之外, 还要派发 onResize({width, height}) 给布局根
// —— resize 是窗口级事件, 与焦点在哪无关, 所以从根节点链上找处理器
// (与 onKeyDown 的焦点链是两条路)。载荷字段名与设备 API 的 getSystemInfo
// 统一为 width/height (两处词汇一次定好, 见 agent_doc/gui-responsive-screen-options.md §3.1)。

// TestResizeDispatchesOnResizeToRoot 全链路: 注入 EventResize → 根上的
// onResize 收到 {width, height} → signal 更新 → 响应式 prop 重算 → 按新窗口
// 尺寸重新布局。这是 useWindowSize 模式 (docs/gui-patterns.md) 的内核依赖。
func TestResizeDispatchesOnResizeToRoot(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		import { createSignal } from "gx/solid";
		let got = "";
		const [win, setWin] = createSignal({width: 400, height: 300});
		render(
			h("column", {
				gap: 8,
				onResize: (e) => { got = e.width + "x" + e.height; setWin({width: e.width, height: e.height}); },
			},
				h("rect", {width: () => win().width - 60, height: 12, background: "#3355aa"}),
				h("text", {font: 14}, () => "now " + win().width + "x" + win().height)),
			{title: "resize", width: 400, height: 300});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()
	bar := findFirst(root, "rect")
	if bar == nil || bar.Box.W != 340 {
		t.Fatalf("初始 rect 宽 = %v, want 340 (400-60)", bar.Box)
	}
	before := shots(fake)

	// 后端语义: 先改真实尺寸, 再投递 EventResize (win32/x11 都是这个顺序)
	fake.w, fake.h = 640, 480
	fake.push(Event{Kind: EventResize, W: 640, H: 480})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	val, _ := v.Globals().Get("got")
	s, _ := val.(*object.String)
	if s == nil || s.Value != "640x480" {
		t.Fatalf("onResize 载荷 = %q, want \"640x480\"", val.Inspect())
	}
	if bar.Box.W != 580 {
		t.Fatalf("resize 后 rect 宽 = %d, want 580 (640-60); onResize 未接通响应式布局", bar.Box.W)
	}
	if shots(fake) <= before {
		t.Fatalf("resize 后应有重绘上屏")
	}
}

// TestResizeHandlerOnlyOnRoot onResize 是窗口级事件: 挂在**非根**节点上不派发
// (与"resize 属于窗口, 不属于某个控件"的语义一致, 文档里写明挂根节点)。
func TestResizeHandlerOnlyOnRoot(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let got = "none";
		render(
			h("column", {gap: 8},
				h("row", {onResize: (e) => { got = e.width + "x" + e.height; }},
					h("text", {font: 14}, "inner row"))),
			{title: "T", width: 300, height: 200});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	fake.push(Event{Kind: EventResize, W: 500, H: 400})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	val, _ := v.Globals().Get("got")
	s, _ := val.(*object.String)
	if s == nil || s.Value != "none" {
		t.Fatalf("非根 onResize 不该派发, got = %q", val.Inspect())
	}
}
