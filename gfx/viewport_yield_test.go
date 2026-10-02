package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/vm"
)

// TestKeyboardYieldRelayouts 回归 (2026-10-02, Android 模拟器定位):
// 宿主上报键盘高度之后, 响应式 paddingBottom 写回 120 —— 除了"文本内容要变",
// **布局也必须按新的内容盒重算**, 否则贴着底部的东西 (输入框 / 状态行) 会被软
// 键盘盖住。
//
// ## 断言对象是"内容盒底边", 不是"顶部的行"
//
// 早先这个用例断言的是"标题行被推到键盘上方", 于是长期假失败。那是**断言写错
// 了**, 不是布局错了: flex 的 column 自顶向下排, paddingBottom 变大只是把内容
// 盒的底边抬起来 —— 已经排好的顶部子节点不会跟着下移, 这与 CSS 的直觉一致。
//
// 真正需要让位的是"贴着底边"的那些: 所以这里用 justifyContent=end 把它按到
// 内容盒底边上, 于是 paddingBottom 一变它就跟着上移。断言写成"该行的底边 ==
// 内容盒底边", 它既表达了下游真正依赖的契约, 也能在 paddingBottom 没进布局时
// 立刻变红。
//
// 上报与断言都必须在泵轮次内做 (viewport_kb_test.go 文件头的同一条纪律:
// 泵外调上报, 回调桥 currentVM==nil 会把脚本闭包静默丢弃, 测出假阴性)。
func TestKeyboardYieldRelayouts(t *testing.T) {
	resetViewportStateForTest()
	t.Cleanup(resetViewportStateForTest)

	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { useKeyboardHeight, useInsets } from "gx/viewport";
		import { h, render } from "gx/gfx";
		render(
			h("window", { title: "t" },
				h("column", { gap: 4, padding: 10, justifyContent: "end",
					paddingTop: () => useInsets().top + 10,
					paddingBottom: () => Math.max(useInsets().bottom, useKeyboardHeight(), 10) },
					h("text", null, "标题行"),
					h("text", null, () => "kb=" + useKeyboardHeight()),
				)
			)
		);
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	a := activeApp
	root := a.root
	appMu.Unlock()

	var col, kbRow *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n.Tag == "column" && col == nil {
			col = n
		}
		if n.Tag == "#text" && strings.HasPrefix(n.Text, "kb=") && kbRow == nil {
			kbRow = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if col == nil || kbRow == nil {
		t.Fatalf("没找到 column / kb= 文本节点")
	}
	kbRowY0 := kbRow.Box.Y

	var steps []func()
	// 宿主的真实顺序: 先报键盘, 安全区随后 (还会再分发) —— 见 gfx.ReportInsets。
	steps = append(steps, func() {
		ReportKeyboardHeight(nil, 120)
		ReportInsets(nil, Insets{Bottom: 63})
	})
	steps = append(steps, func() {
		pb, ok := col.PropNum("paddingBottom")
		if !ok || int(pb) != 120 {
			t.Errorf("响应式 paddingBottom 未写回: got %v (has=%v), want 120", pb, ok)
			return
		}
		if !strings.Contains(kbRow.Text, "kb=120") {
			t.Errorf("kb 文本 = %q, want 含 kb=120", kbRow.Text)
		}
		contentBottom := col.Box.Y + col.Box.H - int(pb)
		if got := kbRow.Box.Y + kbRow.Box.H; got != contentBottom {
			t.Errorf("布局未按新内容盒重算: 贴底行底边=%d, 内容盒底边=%d (paddingBottom=%v)",
				got, contentBottom, pb)
		}
		if kbRow.Box.Y >= kbRowY0 {
			t.Errorf("键盘让位后贴底行没有上移: y=%d, 初始 y=%d", kbRow.Box.Y, kbRowY0)
		}
	})
	runPumpSteps(t, v, fake, steps)
}
