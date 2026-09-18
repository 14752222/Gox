package gfx

import (
	"testing"
)

// ===== 路由 A 模式: 用户态 signal 切页 (2026-09-19 拍板落地) =====
//
// 路由不进内核: demo 脚本用 "一个 route signal + 页面表 + 守卫函数" 表达
// 切页 / 未保存拦截 (模式文档 docs/gui-patterns.md §1-§2)。这里对 demo 做
// 全链路交互验证 —— 模式必须真的能跑通守卫流程, 而不只是挂载不报错。

// buttonWithText 找标签文本等于 label 的 button (按钮文案是它的 #text 子节点)。
func buttonWithText(root *GuiNode, label string) *GuiNode {
	return findFirstWhere(root, func(n *GuiNode) bool {
		if n.Tag != "button" {
			return false
		}
		for _, c := range n.Children {
			if c.Tag == "#text" && c.Text == label {
				return true
			}
		}
		return false
	})
}

// click 在下一轮 pump 前注入一次对 n 的点击 (对话框用例同款: 只推 MouseUp)。
func click(fake *fakeSurface, n *GuiNode) {
	fake.push(Event{Kind: EventMouseUp, X: n.Box.X + 2, Y: n.Box.Y + 2})
}

// TestRoutingDemoScript 走完整守卫流程:
// 初始 home → 进 editor → 输入变脏 → 切 home 被拦截 (确认条出现、路由没变)
// → Stay 留下且草稿不丢 → 再切 home 又拦截 → Discard 放行切页且草稿清空。
func TestRoutingDemoScript(t *testing.T) {
	runDemoSteps(t, "routing_demo.js", []func(*GuiNode, *fakeSurface){
		// 初始: home 页。切页即卸载 ⇒ editor 的 input 不在树上。
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "route: home") {
				t.Fatalf("初始应停在 home, 实际文本不含 \"route: home\"")
			}
			if inp := findFirst(root, "input"); inp != nil {
				t.Fatalf("home 页不该挂载 editor 的 input (切页即卸载)")
			}
			click(fake, buttonWithText(root, "editor"))
		},
		// editor 已挂载: 先点输入框拿焦点。受控 input 的 value prop 要等
		// signal 写回 + 下一轮 pump 才落回节点, 所以**每个按键单独一轮**
		// (连推两个会都基于旧 value 各自覆盖, 只剩最后一个字符)。
		func(root *GuiNode, fake *fakeSurface) {
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("切到 editor 后 input 应挂载")
			}
			click(fake, inp)
		},
		func(root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "h"})
		},
		func(root *GuiNode, fake *fakeSurface) {
			fake.push(Event{Kind: EventKeyDown, Key: "i"})
		},
		// 草稿 "hi" != 已保存 "": dirty。点 home ⇒ 应被拦截。
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "(dirty)") {
				t.Fatalf("输入后应显示 (dirty)")
			}
			click(fake, buttonWithText(root, "home"))
		},
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "Unsaved changes") {
				t.Fatalf("脏 editor 切页应弹确认条")
			}
			if !textContainsAny(root, "route: editor") {
				t.Fatalf("被拦截时路由不该变, 还在 editor")
			}
			click(fake, buttonWithText(root, "Stay"))
		},
		// Stay: 留在 editor, 确认条消失, 草稿不丢。
		func(root *GuiNode, fake *fakeSurface) {
			if textContainsAny(root, "Unsaved changes") {
				t.Fatalf("Stay 后确认条应消失")
			}
			inp := findFirst(root, "input")
			if inp == nil {
				t.Fatalf("Stay 后应仍在 editor")
			}
			if v, _ := inp.PropStr("value"); v != "hi" {
				t.Fatalf("Stay 后草稿应保留, input value = %q", v)
			}
			click(fake, buttonWithText(root, "home"))
		},
		// 再次拦截, 这次选 Discard & go。
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "Unsaved changes") {
				t.Fatalf("脏稿二次切页仍应拦截")
			}
			click(fake, buttonWithText(root, "Discard & go"))
		},
		// 放行: 回到 home, input 卸载。
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "route: home") {
				t.Fatalf("Discard 后应切到 home")
			}
			if inp := findFirst(root, "input"); inp != nil {
				t.Fatalf("离开 editor 后 input 应卸载")
			}
		},
	})
}
