package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== JSX 缺省工厂 (h) 的自动补齐: 端到端 =====
//
// 报错现场是"用了 JSX 但窗口起不来: h is not defined" —— 起因是 JSX 被降级成
// h(...) 调用而脚本没导入 h (只 import render 能编译, 挂载即失败)。compiler 现在
// 按需补 `import { h } from "gx/gfx"`; 这两个用例分别在"照常出窗口"与
// "自定义工厂不被顶掉"两侧钉住它。

// TestJSXWithoutImportHMounts 只 import render、不 import h 的 JSX 脚本要能挂窗口。
func TestJSXWithoutImportHMounts(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { render } from "gx/gfx";
		const ui = <column gap={8} padding={16}>
			<text font={20}>hello</text>
			<button>加一</button>
		</column>;
		render(ui, {title: "T", width: 320, height: 220});
	`)
	if err != nil {
		t.Fatalf("用了 JSX 但没 import h, 应当照常编译执行: %v", err)
	}
	if !Active() {
		t.Fatal("窗口没挂上 (render 未生效)")
	}
	if shots(fake) < 1 {
		t.Fatal("首帧未上屏")
	}

	uiVal, ok := v.Globals().Get("ui")
	if !ok {
		t.Fatal("global ui missing")
	}
	ui, ok := uiVal.(*GuiNode)
	if !ok {
		t.Fatalf("ui 不是元素节点: %T", uiVal)
	}
	if ui.Tag != "column" || len(ui.Children) != 2 {
		t.Fatalf("元素树不对: tag=%s children=%d", ui.Tag, len(ui.Children))
	}
	if ui.Children[0].Box.H <= 0 {
		t.Fatalf("文本子节点没量到尺寸 (自动补的工厂没接上渲染): %+v", ui.Children[0].Box)
	}

	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("事件循环: %v", err)
	}
	if Active() {
		t.Fatal("关闭后应用未退出")
	}
}

// TestJSXKeepsUserDefinedFactoryEndToEnd 脚本自己定义的 h 必须继续生效 ——
// 自动补的工厂绝不能顶掉它 (这是"不补"那条规则在运行时侧的对照)。
func TestJSXKeepsUserDefinedFactoryEndToEnd(t *testing.T) {
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	// 自定义 h 只数调用次数、返回 null: 不挂窗口, 断言集中在"谁被调用了"。
	v, err := vm.EvalVM(`
		let calls = 0;
		function h(tag, props) { calls = calls + 1; return null; }
		const ui = <column gap={8}><text>hi</text></column>;
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	callsVal, _ := v.Globals().Get("calls")
	calls, _ := callsVal.(*object.Number)
	if calls == nil || calls.Value != 2 {
		t.Fatalf("自定义 h 应被调用 2 次 (column + text), 实际 %v —— 自动工厂不能顶掉它", callsVal)
	}
	if Active() {
		t.Fatal("该脚本不该挂窗口 (自定义 h 返回 null)")
	}
}
