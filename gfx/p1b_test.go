package gfx

import (
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P1-2: 条件渲染 / 列表渲染 =====
//
// 断言要点:
//   - 函数子节点可以返回元素 / 数组 / null, slot 占位节点按类型分派;
//   - 旧子树被整个销毁: 从父节点摘除 + effect 注销 (用"改信号不再写回旧节点"
//     这个可观测副作用来验证注销真的发生了);
//   - 嵌套两层信号都能正确更新 (effect 内建 effect 的依赖收集语义);
//   - slot 对布局透明: 单子跟随子节点尺寸, 多子按父容器方向堆叠。

// evalForUI 起一个真 VM 执行脚本, 返回 (VM, 根节点, app)。
func evalForUI(t *testing.T, src string) (*vm.VM, *GuiNode, *app) {
	t.Helper()
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(src)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	a := activeApp
	appMu.Unlock()
	if a == nil {
		t.Fatalf("脚本未挂载窗口")
	}
	return v, a.root, a
}

// driveSteps 在事件循环上下文里依次执行 steps (每步之间回到循环)。
//
// 为什么不能在循环外直接调用脚本函数: VM 主脚本执行结束后 currentVM 会被
// 恢复成 nil, object.CallFunction 桥找不到 VM 时静默返回 undefined ——
// 信号 setter 也就成了空操作。事件循环期间 currentVM 是注册好的。
func driveSteps(t *testing.T, v *vm.VM, steps ...func()) {
	t.Helper()
	i := 0
	err := v.RunTimersWithPump(func(maxWait time.Duration) bool {
		if i >= len(steps) {
			return false
		}
		steps[i]()
		i++
		return true
	})
	if err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if i != len(steps) {
		t.Fatalf("只执行了 %d/%d 步", i, len(steps))
	}
}

// callGlobal 取全局函数并调用 (测试里直接驱动 signal setter)。
// 必须在 driveSteps 的步骤里调用。
func callGlobal(t *testing.T, v *vm.VM, name string, args ...object.Value) {
	t.Helper()
	fn, ok := v.Globals().Get(name)
	if !ok {
		t.Fatalf("全局 %s 缺失", name)
	}
	object.CallFunction(fn, nil, args...)
}

// slotOf 取指定节点的第一个 slot 子节点。
func slotOf(t *testing.T, parent *GuiNode) *GuiNode {
	t.Helper()
	for _, c := range parent.Children {
		if c.Tag == "slot" {
			return c
		}
	}
	t.Fatalf("父节点 %s 下没有 slot 子节点", parent.Tag)
	return nil
}

func jsArr(vals ...object.Value) object.Value { return object.NewArray(vals) }
func jsStr(s string) object.Value             { return object.NewString(s) }

// TestConditionalRenderSwitchesAndDisposes 条件渲染: 元素 ↔ null 切换,
// 且旧子树的内层 effect 必须被注销。
func TestConditionalRenderSwitchesAndDisposes(t *testing.T) {
	v, root, _ := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [show, setShow] = createSignal(true);
		const [w, setW] = createSignal(1);
		// 分支里的元素带自己的响应式 prop: 若旧子树没被 dispose,
		// 改 w 仍会写回这个已经离树的节点。
		const branch = () => h("rect", {width: () => w() * 10, height: 6, background: "#c0392b"});
		const ui = h("column", {gap: 4}, () => (show() ? branch() : null));
		render(ui, window({title: "T", width: 200, height: 120}));
	`)

	slot := slotOf(t, root)
	if len(slot.Children) != 1 || slot.Children[0].Tag != "rect" {
		t.Fatalf("初始应挂一个 rect: %v", slot.Children)
	}
	old := slot.Children[0]
	if got, _ := old.PropNum("width"); got != 10 {
		t.Fatalf("初始宽度 = %v, want 10", got)
	}

	var newSlot *GuiNode
	driveSteps(t, v,
		// 1) 切到 null: 子树整组销毁
		func() {
			callGlobal(t, v, "setShow", object.NewBoolean(false))
			if len(slot.Children) != 0 {
				t.Fatalf("切到 null 后 slot 应为空: %v", slot.Children)
			}
			if old.Parent != nil {
				t.Fatalf("旧子树未被摘除: Parent = %v", old.Parent)
			}
		},
		// 2) 旧子树的 effect 必须已注销: 改 w 不能再写回旧节点
		func() {
			callGlobal(t, v, "setW", object.NewNumber(7))
			if got, _ := old.PropNum("width"); got != 10 {
				t.Fatalf("旧子树的 effect 未注销 (宽度被写成了 %v)", got)
			}
		},
		// 3) 切回来: 挂新子树, 宽度按当前 w 求值
		func() {
			callGlobal(t, v, "setShow", object.NewBoolean(true))
			newSlot = slotOf(t, root)
			if len(newSlot.Children) != 1 || newSlot.Children[0].Tag != "rect" {
				t.Fatalf("切回后应重新挂上 rect: %v", newSlot.Children)
			}
			if got, _ := newSlot.Children[0].PropNum("width"); got != 70 {
				t.Fatalf("重新挂载后宽度 = %v, want 70", got)
			}
		},
		// 4) 新子树的 effect 有效: 改 w 立刻写回
		func() {
			callGlobal(t, v, "setW", object.NewNumber(3))
			if got, _ := newSlot.Children[0].PropNum("width"); got != 30 {
				t.Fatalf("新子树 effect 未生效: 宽度 = %v, want 30", got)
			}
		},
	)
}

// TestListRenderArraySignal 列表渲染: 数组长度变化 → 渲染节点数一致。
func TestListRenderArraySignal(t *testing.T) {
	v, root, _ := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [items, setItems] = createSignal(["a", "b"]);
		const item = (t) => h("text", null, t);
		const ui = h("column", {gap: 4}, () => items().map(item));
		render(ui, window({title: "T", width: 300, height: 200}));
	`)

	slot := slotOf(t, root)
	if len(slot.Children) != 2 {
		t.Fatalf("初始应挂 2 项: %v", slot.Children)
	}
	if slot.Children[0].TextContent() != "a" || slot.Children[1].TextContent() != "b" {
		t.Fatalf("列表内容不对: %q / %q", slot.Children[0].TextContent(), slot.Children[1].TextContent())
	}
	first := slot.Children[0]

	driveSteps(t, v,
		func() { // 追加一项
			callGlobal(t, v, "setItems", jsArr(jsStr("a"), jsStr("b"), jsStr("c")))
			if len(slot.Children) != 3 {
				t.Fatalf("追加后应挂 3 项: %v", slot.Children)
			}
			if slot.Children[2].TextContent() != "c" {
				t.Fatalf("第三项应为 c: %q", slot.Children[2].TextContent())
			}
			if first.Parent != nil {
				t.Fatalf("数组重建应销毁旧子树: %v", first.Parent)
			}
		},
		func() { // 缩短
			callGlobal(t, v, "setItems", jsArr(jsStr("only")))
			if len(slot.Children) != 1 || slot.Children[0].TextContent() != "only" {
				t.Fatalf("缩短后应为 [only]: %v", slot.Children)
			}
		},
		func() { // 空数组 → 空插槽
			callGlobal(t, v, "setItems", jsArr())
			if len(slot.Children) != 0 {
				t.Fatalf("空数组应得到空插槽: %v", slot.Children)
			}
		},
		// 顺带验证列表的布局真的按父容器方向排布 (column → 竖排, 间距用父 gap 4)
		func() {
			callGlobal(t, v, "setItems", jsArr(jsStr("p"), jsStr("q")))
			Layout(root, 300, 200) // 手动跑一次布局 (这一步不走重绘)
			a0, a1 := slot.Children[0], slot.Children[1]
			if a0.Box.H == 0 || a1.Box.Y != a0.Box.Y+a0.Box.H+4 {
				t.Fatalf("列表项未按父容器 gap 竖排: %v / %v", a0.Box, a1.Box)
			}
		},
	)
}

// TestListRenderNestedAndScalarElements 直接返回原生数组: 嵌套数组逐元素
// 递归展开, 标量转文本, null/布尔得到空位。
func TestListRenderNestedAndScalarElements(t *testing.T) {
	v, root, _ := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [items, setItems] = createSignal(["a", "b"]);
		const ui = h("column", null, () => items());
		render(ui, window({title: "T", width: 300, height: 200}));
	`)

	slot := slotOf(t, root)
	if len(slot.Children) != 2 || slot.Children[0].TextContent() != "a" {
		t.Fatalf("初始应为两个文本: %v", slot.Children)
	}

	driveSteps(t, v,
		func() { // 标量元素 (数字) 转文本
			callGlobal(t, v, "setItems", jsArr(jsStr("x"), object.NewNumber(2)))
			if len(slot.Children) != 2 || slot.Children[1].TextContent() != "2" {
				t.Fatalf("标量元素应转成文本: %v", slot.Children)
			}
		},
		func() { // 嵌套数组逐元素递归展开
			callGlobal(t, v, "setItems", jsArr(jsArr(jsStr("n1"), jsStr("n2")), jsStr("n3")))
			if len(slot.Children) != 3 {
				t.Fatalf("嵌套数组应展开成 3 项, got %d", len(slot.Children))
			}
			for i, want := range []string{"n1", "n2", "n3"} {
				if got := slot.Children[i].TextContent(); got != want {
					t.Fatalf("第 %d 项 = %q, want %q", i, got, want)
				}
			}
		},
		func() { // null / 布尔 → 空位
			callGlobal(t, v, "setItems", jsArr(object.NullSingleton, jsStr("z"), object.NewBoolean(false)))
			if len(slot.Children) != 1 || slot.Children[0].TextContent() != "z" {
				t.Fatalf("null/布尔应得到空位, 只留 z: %v", slot.Children)
			}
		},
	)
}

// TestNestedReactiveTwoLevels 两层嵌套信号: 外层决定挂什么, 内层决定属性。
func TestNestedReactiveTwoLevels(t *testing.T) {
	v, root, _ := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [outer, setOuter] = createSignal(2);
		const [inner, setInner] = createSignal(3);
		// 外层 getter 读 outer 决定挂什么 (因此会重建), 内层函数读两个信号
		const ui = h("column", null, () => h("row", {height: outer()},
			h("rect", {width: () => inner() * outer(), height: 5, background: "#27ae60"})));
		render(ui, window({title: "T", width: 300, height: 200}));
	`)

	row := slotOf(t, root).Children[0]
	rect := row.Children[0]
	if got, _ := rect.PropNum("width"); got != 6 {
		t.Fatalf("初始宽度 = %v, want 6", got)
	}

	var row2, rect2 *GuiNode
	driveSteps(t, v,
		func() { // 改内层信号: 子树里的 effect 已随挂载建立依赖
			callGlobal(t, v, "setInner", object.NewNumber(5))
			if got, _ := rect.PropNum("width"); got != 10 {
				t.Fatalf("改内层信号后宽度 = %v, want 10", got)
			}
		},
		func() { // 改外层信号: 重建子树, 内层依赖重新建立
			callGlobal(t, v, "setOuter", object.NewNumber(4))
			row2 = slotOf(t, root).Children[0]
			if row2 == row {
				t.Fatalf("外层信号变化应重建子树 (v1 不做 diff)")
			}
			rect2 = row2.Children[0]
			if got, _ := rect2.PropNum("width"); got != 20 {
				t.Fatalf("重建后宽度 = %v, want 20", got)
			}
		},
		func() { // 重建后的内层 effect 仍然有效
			callGlobal(t, v, "setInner", object.NewNumber(6))
			if got, _ := rect2.PropNum("width"); got != 24 {
				t.Fatalf("重建后改内层信号宽度 = %v, want 24", got)
			}
		},
	)
}

// TestSlotLayoutTransparency slot 的布局语义: 单子跟随子节点, 多子按父方向堆叠。
func TestSlotLayoutTransparency(t *testing.T) {
	// 1) 单子 slot 包 checkbox: 控件不能被父容器的 stretch 拉变形
	root := mkNode("column", map[string]float64{"padding": 10})
	slot := &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
	cb := mkNode("checkbox", nil)
	slot.Children = []*GuiNode{cb}
	cb.Parent = slot
	slot.Parent = root
	root.Children = []*GuiNode{slot}
	Layout(root, 400, 200)

	if cb.Box.W != 18 || cb.Box.H != 18 {
		t.Fatalf("单子 slot 下的 checkbox 被拉变形: %v", cb.Box)
	}
	if slot.Box.W != 18 || slot.Box.H != 18 {
		t.Fatalf("单子 slot 尺寸应等于子节点: %v", slot.Box)
	}
	if slot.Box.X != 10 || slot.Box.Y != 10 {
		t.Fatalf("slot 应占父容器内容区起点: %v", slot.Box)
	}

	// 2) 单子 slot 包容器: 容器该 stretch 时仍然 stretch
	root2 := mkNode("column", nil)
	slot2 := &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
	inner := mkNode("row", map[string]float64{"height": 20})
	slot2.Children = []*GuiNode{inner}
	inner.Parent = slot2
	slot2.Parent = root2
	root2.Children = []*GuiNode{slot2}
	Layout(root2, 300, 100)
	if inner.Box.W != 300 {
		t.Fatalf("slot 里的容器应撑满交叉轴: %v", inner.Box)
	}

	// 3) 多子 slot (列表): 按父容器方向堆叠, 间距沿用父容器的 gap。
	//    a 显式给了 width → 不被 stretch; b 没有 → 撑满交叉轴。
	root3 := mkNode("column", map[string]float64{"gap": 6, "padding": 4})
	slot3 := &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
	a := mkNode("rect", map[string]float64{"width": 20, "height": 10})
	b := mkNode("rect", map[string]float64{"height": 12})
	slot3.Children = []*GuiNode{a, b}
	a.Parent, b.Parent = slot3, slot3
	slot3.Parent = root3
	root3.Children = []*GuiNode{slot3}
	Layout(root3, 200, 100)

	if a.Box.Y != 4 || b.Box.Y != 4+10+6 {
		t.Fatalf("多子 slot 未按父方向竖排 / gap 未继承: a=%v b=%v", a.Box, b.Box)
	}
	if slot3.Box.H != 10+6+12 {
		t.Fatalf("多子 slot 高度 = %d, want 28", slot3.Box.H)
	}
	if a.Box.W != 20 {
		t.Fatalf("显式 width 不该被 stretch 覆盖: %v", a.Box)
	}
	if b.Box.W != 192 {
		t.Fatalf("多子 slot 内的子节点应撑满交叉轴: %v (want 200-4*2)", b.Box)
	}

	// 4) 父容器是 row 时, 多子 slot 横排
	root4 := mkNode("row", map[string]float64{"gap": 4})
	slot4 := &GuiNode{Tag: "slot", Props: map[string]object.Value{}}
	c := mkNode("rect", map[string]float64{"width": 20, "height": 10})
	d := mkNode("rect", map[string]float64{"width": 30, "height": 10})
	slot4.Children = []*GuiNode{c, d}
	c.Parent, d.Parent = slot4, slot4
	slot4.Parent = root4
	root4.Children = []*GuiNode{slot4}
	Layout(root4, 200, 100)
	if c.Box.X != 0 || d.Box.X != 24 {
		t.Fatalf("row 下的多子 slot 应横排: c=%v d=%v", c.Box, d.Box)
	}
}

// TestConditionalRenderPixels 条件渲染的结果确实画进了帧里。
func TestConditionalRenderPixels(t *testing.T) {
	v, root, a := evalForUI(t, `
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [flag, setFlag] = createSignal(true);
		const ui = h("column", null, () => (flag()
			? h("rect", {width: 120, height: 40, background: "#c0392b"})
			: h("rect", {width: 120, height: 40, background: "#27ae60"})));
		render(ui, window({title: "T", width: 200, height: 100}));
	`)
	fake := a.surface.(*fakeSurface)

	rect := slotOf(t, root).Children[0]
	assertPx(t, shotsImage(fake), rect.Box.X+5, rect.Box.Y+5, pxRed, "初始红色分支")

	driveSteps(t, v, func() {
		callGlobal(t, v, "setFlag", object.NewBoolean(false))
		a.redraw()
	})
	rect2 := slotOf(t, root).Children[0]
	if rect2 == rect {
		t.Fatalf("分支切换应重建子树 (v1 不做 diff)")
	}
	assertPx(t, shotsImage(fake), rect2.Box.X+5, rect2.Box.Y+5, pxAccent, "切换到绿色分支")
	// 旧位置不应残留红色
	if c := shotsImage(fake).RGBAAt(rect.Box.X+119, rect.Box.Y+39); c == pxRed {
		t.Fatalf("旧分支像素未清掉")
	}
}
