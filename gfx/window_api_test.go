package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 窗口句柄 API: title/setTitle/resize (§四 窗口/系统缺口, 2026-09-19) =====

// TestWindowSetTitle 窗口标题可运行期修改并读回。
func TestWindowSetTitle(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		const win = render(h("column", null, h("text", {font: 14}, "hi")),
			{title: "初始标题", width: 300, height: 200});
		globalThis.g_title0 = win.title();
		globalThis.rename = (s) => { win.setTitle(s); };
		globalThis.title = () => win.title();
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobal(t, v, "g_title0", "初始标题")

	runPumpSteps(t, v, fake, []func(){
		func() { callGlobalFn(t, v, "rename", object.NewString("新标题")) },
		func() {
			if got := callGlobalInspect(t, v, "title"); got != "新标题" {
				t.Fatalf("title() = %v, want 新标题", got)
			}
			fake.mu.Lock()
			title := fake.title
			fake.mu.Unlock()
			if title != "新标题" {
				t.Fatalf("后端收到的标题 = %q", title)
			}
		},
	})
}

// TestWindowResizeFullChain resize 改客户区尺寸, 且像真后端一样产生
// EventResize → 根节点 onResize → signal → 响应式文本更新 (全链)。
func TestWindowResizeFullChain(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [win, setWin] = createSignal({width: 400, height: 300});
		const w = render(
			h("column", {
				onResize: (e) => setWin({width: e.width, height: e.height}),
			}, h("text", {font: 14}, () => win().width + "x" + win().height)),
			{title: "resize", width: 400, height: 300});
		globalThis.grow = () => { w.resize(640, 480); };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobalText(t, v, "400x300")

	runPumpSteps(t, v, fake, []func(){
		func() { callGlobalFn(t, v, "grow") }, // → fake.ResizeClient → EventResize
		func() {
			// 下一轮泵处理 resize 事件: onResize 已把 signal 更新
			assertGlobalText(t, v, "640x480")
			fake.mu.Lock()
			w, h := fake.w, fake.h
			fake.mu.Unlock()
			if w != 640 || h != 480 {
				t.Fatalf("客户区尺寸 = %dx%d, want 640x480", w, h)
			}
		},
	})
}

// TestWindowResizeRejectsBadArgs 参数校验: resize 缺参/非数字是 TypeError
// 而不是静默。
func TestWindowResizeRejectsBadArgs(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		const w = render(h("column", null), {title: "T", width: 100, height: 80});
		// 内建返回 TypeError 会作为异常抛出, 用 try/catch 在脚本侧判定
		const attempt = (fn) => { try { fn(); return "accepted"; } catch (e) { return "threw"; } };
		globalThis.bad1 = () => attempt(() => w.resize(100));
		globalThis.bad2 = () => attempt(() => w.resize("a", "b"));
		globalThis.ok = () => attempt(() => w.resize(-5, 0));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	runPumpSteps(t, v, fake, []func(){
		func() {
			if got := callGlobalInspect(t, v, "bad1"); got != "threw" {
				t.Fatalf("resize(100) 应抛 TypeError: %v", got)
			}
			if got := callGlobalInspect(t, v, "bad2"); got != "threw" {
				t.Fatalf("resize(\"a\",\"b\") 应抛 TypeError: %v", got)
			}
			// 非法数值 (<=0) 不抛错也不生效 —— Go 侧 Resize 静默拒绝
			if got := callGlobalInspect(t, v, "ok"); got != "accepted" {
				t.Fatalf("resize(-5,0) 不该抛: %v", got)
			}
			fake.mu.Lock()
			w, h := fake.w, fake.h
			fake.mu.Unlock()
			if w != 400 || h != 300 {
				t.Fatalf("非法 resize 不该生效: %dx%d", w, h)
			}
		},
	})
}
