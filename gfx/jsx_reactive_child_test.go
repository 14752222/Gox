package gfx

import "testing"

// JSX 插值子节点的响应式语义 (Solid 约定):
//
//	<text>计数：{ count()}</text>   ⇔   <text>{() => `计数：${count()}`}</text>
//
// 解析器把 `{expr}` 降级成 `() => expr` (常量与显式函数除外), 渲染侧按函数
// 子节点包 effect 订阅。这组测试锁的是**端到端行为**: 初始渲染 + 信号变化后
// 文本真的跟着变。

// TestJSXMixedTextChildUpdates 混排文本+插值子节点: 点击按钮改 signal,
// `<text>计数：{ count()}</text>` 的 TextContent 必须跟着走。
func TestJSXMixedTextChildUpdates(t *testing.T) {
	src := `
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
const [count, setCount] = createSignal(0);
render(
  <window title="P" width={300} height={200}>
    <column gap={8} padding={16}>
      <text font={20}>计数：{ count()}</text>
      <button onClick={() => setCount(c => c + 1)}>add</button>
    </column>
  </window>
);
`
	v, fake := evalUI(t, src)
	root := uiRoot(t)

	runPumpSteps(t, v, fake, []func(){
		// 0) 建树: 静态段 + slot 里的动态段拼出完整文本
		func() {
			txt := findFirst(root, "text")
			if txt == nil {
				t.Fatalf("找不到 text 节点")
			}
			if got := txt.TextContent(); got != "计数：0" {
				t.Fatalf("初始 TextContent = %q, want 计数：0", got)
			}
		},
		// 1) 点三次按钮
		func() { clickCenter(fake, findFirst(root, "button")) },
		func() { clickCenter(fake, findFirst(root, "button")) },
		func() { clickCenter(fake, findFirst(root, "button")) },
		// 2) 断言: 动态段跟着 signal 走
		func() {
			if got := findFirst(root, "text").TextContent(); got != "计数：3" {
				t.Fatalf("点击三次后 TextContent = %q, want 计数：3", got)
			}
		},
	})
}

// TestJSXInterpChildListUpdates 列表插值 `{rows()}` 也是活的:
// signal 数组换代后 slot 里的子节点跟着换代。
func TestJSXInterpChildListUpdates(t *testing.T) {
	src := `
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
const [rows, setRows] = createSignal(["a", "b"]);
render(
  <window title="P" width={300} height={200}>
    <column gap={4} padding={16}>
      <text font={20}>{rows()}</text>
      <button onClick={() => setRows(["a", "b", "c"])}>add</button>
    </column>
  </window>
);
`
	v, fake := evalUI(t, src)
	root := uiRoot(t)

	runPumpSteps(t, v, fake, []func(){
		func() {
			if got := findFirst(root, "text").TextContent(); got != "ab" {
				t.Fatalf("初始 TextContent = %q, want ab", got)
			}
		},
		func() { clickCenter(fake, findFirst(root, "button")) },
		func() {
			if got := findFirst(root, "text").TextContent(); got != "abc" {
				t.Fatalf("点击后 TextContent = %q, want abc", got)
			}
		},
	})
}

// TestJSXInterpChildFunctionValue 函数标识符作插值子节点: 包一层后 getter
// 返回函数本身, mountValue 要把它再包一层订阅 (Solid insert 语义), 不能
// ToString 成 "[Function: …]"。回归自鸿蒙演示的 {foldSummary}。
func TestJSXInterpChildFunctionValue(t *testing.T) {
	src := `
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";
const [n, setN] = createSignal(1);
const summary = () => "n=" + n();
render(
  <window title="P" width={300} height={200}>
    <column gap={4} padding={16}>
      <text font={20}>{summary}</text>
      <button onClick={() => setN(2)}>add</button>
    </column>
  </window>
);
`
	v, fake := evalUI(t, src)
	root := uiRoot(t)

	runPumpSteps(t, v, fake, []func(){
		func() {
			if got := findFirst(root, "text").TextContent(); got != "n=1" {
				t.Fatalf("初始 TextContent = %q, want n=1 (不许是 [Function: …])", got)
			}
		},
		func() { clickCenter(fake, findFirst(root, "button")) },
		func() {
			if got := findFirst(root, "text").TextContent(); got != "n=2" {
				t.Fatalf("点击后 TextContent = %q, want n=2", got)
			}
		},
	})
}

// TestJSXInterpChildLiteralStatic 字面量子节点保持静态直挂 (不产生 slot):
// `{"hi"}` 与普通文本子节点同构。
func TestJSXInterpChildLiteralStatic(t *testing.T) {
	src := `
import { h, render } from "gx/gfx";
render(
  <window title="P" width={300} height={200}>
    <column gap={4} padding={16}>
      <text font={20}>{"hi"}{42}</text>
    </column>
  </window>
);
`
	v, fake := evalUI(t, src)
	root := uiRoot(t)

	runPumpSteps(t, v, fake, []func(){
		func() {
			txt := findFirst(root, "text")
			if txt == nil {
				t.Fatalf("找不到 text 节点")
			}
			if got := txt.TextContent(); got != "hi42" {
				t.Fatalf("TextContent = %q, want hi42", got)
			}
			for _, c := range txt.Children {
				if c.Tag == "slot" {
					t.Fatalf("字面量子节点不应产生 slot: %+v", txt.Children)
				}
			}
		},
	})
}

// clickCenter 在按钮中心点一次鼠标抬起 (fake 表面的命中路径)。
func clickCenter(fake *fakeSurface, btn *GuiNode) {
	if btn == nil {
		return
	}
	fake.push(Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + btn.Box.H/2})
}
