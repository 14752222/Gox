package gfx

import (
	"image"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// TestIMECommitRepaintsDependentText 回归 (2026-10-02, Android 模拟器实测):
// IME 提交 → onInput → signal 更新之后, 依赖同一 signal 的**兄弟文本子节点**
// 必须落在本次局部重绘的上屏区域里。
//
// 实测症状: 输入框自己的值更新了, 但同列的 "输入内容: …" 文本停在旧值,
// 直到下一次整帧重绘才恢复 —— 树里的 Text 其实已经改对 (effect 跑了,
// markNodeDirty 也调了, 受控回写链路完好), 问题出在脏矩形没有覆盖到它,
// 用户看到的是"受控联动坏了"。
//
// 本用例钉住两层事实: ① 响应层真的更新了 Text; ② 上屏区域真的覆盖标签。
// ②失败说明重绘层丢区, ①失败说明响应层断链 —— 两种病因共用这个现象。
func TestIMECommitRepaintsDependentText(t *testing.T) {
	src := `
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [name, setName] = createSignal("");
		const [dbg, setDbg] = createSignal(0);
		render(
			h("window", { title: "t" },
				h("column", { padding: 10, gap: 8 },
					h("input", {
						width: 200, height: 36,
						value: () => name(),
						onInput: (e) => { setDbg((c) => c + 1); setName(e.value); },
					}),
					h("text", { font: 14 }, () => "输入内容: " + name()),
					h("text", { font: 14 }, () => "dbg=" + dbg()),
				)
			)
		);
	`
	v, fake := evalUI(t, src)
	appMu.Lock()
	a := activeApp
	root := a.root
	appMu.Unlock()
	if a == nil || root == nil {
		t.Fatalf("脚本未挂载窗口")
	}

	// 找 input 与标签文本节点 (#text)
	var in, label, dbg *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if in == nil && n.Tag == "input" {
			in = n
		}
		if label == nil && n.Tag == "#text" && strings.HasPrefix(n.Text, "输入内容") {
			label = n
		}
		if dbg == nil && n.Tag == "#text" && strings.HasPrefix(n.Text, "dbg=") {
			dbg = n
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	if in == nil || label == nil || dbg == nil {
		t.Fatalf("没找到 input / 标签文本节点")
	}

	// 首帧 + 获焦帧: 建立 PrevBox 基线 (与真机时序一致: 先点输入框获焦)
	a.setFocus(in)
	a.redraw()

	// 提交一批文本 (等价于用户在输入法里选定了候选词)。
	// 必须走 pumpEvents (RunTimersWithPump) —— 只调 a.pump 的话 currentVM
	// 没注册, 脚本回调会被回调桥静默丢掉, 测出来的就是假阴性。
	pumpEvents(t, v, a, Event{Kind: EventIMECommit, Text: "hi"})

	// ① 响应层: effect 必须已经把标签文本改对
	if !strings.Contains(label.Text, "hi") {
		vp, _ := in.Props["value"]
		var warns []string
		for _, w := range warnSnapshot() {
			warns = append(warns, w.Text)
		}
		t.Fatalf("提交后: input.Props[value]=%v, label.Text=%q, dbg.Text=%q, 内核警告=%v",
			object.ToString(vp), label.Text, dbg.Text, warns)
	}

	// ② 重绘层: 上屏区域必须覆盖标签的盒子
	fake.mu.Lock()
	regions := fake.regions
	fake.mu.Unlock()
	if regions == nil {
		t.Fatalf("IME 提交后走的是整帧上屏 (regions=nil) —— 与真机现象不符, 请核对用例前提")
	}
	lb := label.Box
	lr := image.Rect(lb.X, lb.Y, lb.X+lb.W, lb.Y+lb.H)
	for _, r := range regions {
		if r.Overlaps(lr) {
			return // 覆盖到了, 链路完好
		}
	}
	t.Fatalf("标签区域 %v 不在任何局部上屏区域 %v 内 —— 屏幕将停留在旧文本", lr, regions)
}
