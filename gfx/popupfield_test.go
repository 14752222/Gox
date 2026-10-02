package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== T08 "字段 + 贴字段弹层" 这一族的公共机制 (popupfield.go) =====
//
// 三个组件各自的用例在各文件里; 这里锁的是**跨组件**的那几条约定 ——
// 它们最容易在"给第四个组件加支持"时被漏掉, 而漏掉的症状是"两个弹层同时
// 开着", 单看任何一个组件的用例都发现不了:
//
//  1. 同族弹层互斥 (打开一个收掉另一个, 任何两个组合都一样);
//  2. 三件套都进 Tab 序 (a11y 的注册表里少一行就静默少一个停留点);
//  3. 三件套的当前值都能被 form 收集到 (formFieldTags 少一行就少一个字段)。

// popupPairTree 造一个含 datepicker + colorpicker 的树 (两者都能开弹层)。
func popupPairTree(t *testing.T) (*GuiNode, *GuiNode, *app) {
	t.Helper()
	root := mkNode("column", map[string]float64{"padding": 10})
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(dp, "value", "2026-11-15")
	attachDatepickerHandler(dp)
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	twoColorPalette(cp)
	withStr(cp, "value", "#ff0000")
	attachColorpickerHandler(cp)
	mountChildren(root, dp, cp)
	_, a := mountTestApp(t, root, 320, 400)
	return dp, cp, a
}

func TestPopupFieldsAreMutuallyExclusive(t *testing.T) {
	dp, cp, a := popupPairTree(t)

	a.openDatepicker(dp)
	if !dp.expanded {
		t.Fatalf("前置条件: 日历该已展开")
	}
	a.openColorpicker(cp)
	if dp.expanded {
		t.Fatalf("打开色板该收掉已展开的日历")
	}
	if !cp.expanded {
		t.Fatalf("色板该已展开")
	}
	// 反向再来一次 (状态机是对称的, 不依赖"谁先打开")
	a.openDatepicker(dp)
	if cp.expanded || !dp.expanded {
		t.Fatalf("反向也应互斥: cp=%v dp=%v", cp.expanded, dp.expanded)
	}
	if n := len(expandedPopupFields(a.rootNode())); n != 1 {
		t.Fatalf("树上同时最多只有一个展开的字段弹层, got %d", n)
	}
}

func TestPopupFieldOpenIsIdempotent(t *testing.T) {
	dp, _, a := popupPairTree(t)
	a.openDatepicker(dp)
	first := dp.popup
	a.openDatepicker(dp) // 再开一次不该重建弹层 (重建会丢掉光标与已挂的 effect)
	if dp.popup != first {
		t.Fatalf("已经展开时不该重建弹层")
	}
	if n := countTag(a.rootNode(), "datepicker-popup"); n != 1 {
		t.Fatalf("弹层节点数 = %d, want 1", n)
	}
}

func TestPopupFieldCloseDisposesSubtree(t *testing.T) {
	// 弹层销毁必须走 disposeNode (它负责注销子树上的 effect 并从父节点摘除);
	// 只把 popup 指针置空的话, 节点还留在 Children 里, 布局/绘制/命中都会
	// 继续看到一个"已经关掉"的弹层。
	dp, _, a := popupPairTree(t)
	a.openDatepicker(dp)
	popup := dp.popup
	a.closePopupField(dp)

	if popup.Parent != nil {
		t.Fatalf("disposeNode 会断开 Parent (节点已离开树, 不该还能沿链走回去)")
	}
	for _, c := range dp.Children {
		if c == popup {
			t.Fatalf("弹层该已从字段的 Children 里摘掉")
		}
	}
	if n := countTag(a.rootNode(), "datepicker-popup"); n != 0 {
		t.Fatalf("树上不该还有弹层节点, got %d", n)
	}
}

func TestPopupFieldNonFieldNodeIsIgnored(t *testing.T) {
	// 容错: 把普通节点交给这一族的入口不该 panic (Go 侧嵌入 gfx 的场景可能
	// 拿到任意节点)。
	a := &app{root: mkNode("column", nil)}
	plain := mkNode("rect", nil)
	a.openPopupField(plain, nil)
	a.closePopupField(plain)
	if plain.expanded {
		t.Fatalf("非这一族的节点不该被置成展开态")
	}
	if popupFieldInChain(plain) != nil {
		t.Fatalf("rect 不该被认成字段族")
	}
}

// ===== 与 a11y / form 的接线 =====

func TestNewFieldsJoinTabOrder(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	inp := mkField("input", 120, 24)
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	btn := withClick(mkButton("提交"))
	mountChildren(root, inp, dp, cp, up, btn)

	renderTree(root, 320, 400)

	order := a11yFocusOrder(root)
	want := []*GuiNode{inp, dp, cp, up, btn}
	if len(order) != len(want) {
		t.Fatalf("焦点序长度 = %d, want %d (%v)", len(order), len(want), tagsOf(order))
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("焦点序第 %d 位 = %s, want %s", i, order[i].Tag, want[i].Tag)
		}
	}
}

func TestNewFieldsExcludedWhenNotFocusable(t *testing.T) {
	// focusable={false} 能把原生可聚焦控件移出 Tab 序 (三件套走的是同一张表,
	// 但"显式 prop 永远赢"这条要对它们同样成立)。
	root := mkNode("column", nil)
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withBool(dp, "focusable", false)
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	withBool(up, "hidden", true)
	mountChildren(root, dp, up)
	renderTree(root, 320, 200)

	if order := a11yFocusOrder(root); len(order) != 0 {
		t.Fatalf("显式排除的字段不该进焦点序, got %v", tagsOf(order))
	}
}

func TestFormCollectsPickerAndUploadValues(t *testing.T) {
	form := mkNode("form", nil)
	inp := &GuiNode{Tag: "input", Props: map[string]object.Value{}}
	withStr(inp, "name", "nick")
	withStr(inp, "value", "amy")
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(dp, "name", "birthday")
	withStr(dp, "value", "2001-03-04")
	cp := &GuiNode{Tag: "colorpicker", Props: map[string]object.Value{}}
	withStr(cp, "name", "theme")
	withStr(cp, "value", "#1e88e5")
	up := &GuiNode{Tag: "upload", Props: map[string]object.Value{}}
	withStr(up, "name", "resume")
	up.Props["value"] = object.NewArray([]object.Value{object.NewString("/tmp/cv.pdf")})
	unnamed := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	withStr(unnamed, "value", "2020-01-01") // 没有 name: 不进 form 取值

	mountChildren(form, inp, dp, cp, up, unnamed)
	root := mkNode("column", nil)
	mountChildren(root, form)

	var submitted []*object.Object
	form.Props["onSubmit"] = object.NewBuiltin("capture", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				submitted = append(submitted, o)
			}
		}
		return object.UndefinedSingleton
	})

	// 走真实链路: Tab 聚焦输入框 → 回车提交 (而不是直接调 formValues,
	// 那样测不到"回车提交"这条 a11y/表单的接线)。
	fake, a := mountTestApp(t, root, 400, 400)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != inp {
		t.Fatalf("Tab 应聚焦输入框, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if len(submitted) != 1 {
		t.Fatalf("回车应提交表单一次, got %d", len(submitted))
	}
	v, ok := submitted[0].GetProperty("values")
	if !ok {
		t.Fatalf("onSubmit 载荷缺 values")
	}
	captured, ok := v.(*object.Object)
	if !ok {
		t.Fatalf("values 不是对象")
	}

	want := map[string]string{
		"nick":     "amy",
		"birthday": "2001-03-04",
		"theme":    "#1e88e5",
	}
	for k, v := range want {
		pv, ok := captured.GetProperty(k)
		if !ok {
			t.Fatalf("values 缺少 %q (keys=%v)", k, keysOf(captured))
		}
		if got := valueText(pv); got != v {
			t.Fatalf("values[%q] = %q, want %q", k, got, v)
		}
	}
	// upload: 值是数组 (路径)
	pv, ok := captured.GetProperty("resume")
	if !ok {
		t.Fatalf("values 缺少 resume (keys=%v)", keysOf(captured))
	}
	arr, ok := pv.(*object.Array)
	if !ok || len(arr.Elements) != 1 {
		t.Fatalf("upload 的值该是数组, got %v", pv)
	}
	if got := valueText(arr.Elements[0]); got != "/tmp/cv.pdf" {
		t.Fatalf("upload 路径 = %q", got)
	}
	// 没有 name 的字段不进 values (否则后端会收到一堆空键)
	if len(keysOf(captured)) != 4 {
		t.Fatalf("values 键数 = %d, want 4 (%v)", len(keysOf(captured)), keysOf(captured))
	}
}

func TestFieldChainLeavesCtrlCombosToShortcuts(t *testing.T) {
	// 整条字段链在入口统一挡下 Ctrl/Alt 组合键: 同一个组合键的行为不该取决于
	// 焦点落在哪种控件上。select 的键盘分支早期漏过这一条 —— Ctrl+↓ 在输入框、
	// 日历、色板上都放行, 在下拉框上却会展开。
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := &GuiNode{Tag: "select", Props: map[string]object.Value{}}
	sel.Props["options"] = object.NewArray([]object.Value{object.NewString("a")})
	attachSelectHandler(sel)
	dp := &GuiNode{Tag: "datepicker", Props: map[string]object.Value{}}
	attachDatepickerHandler(dp)
	mountChildren(root, sel, dp)
	_, a := mountTestApp(t, root, 320, 300)

	a.setFocus(sel)
	if a.handleFieldKey(sel, "ArrowDown", Event{Kind: EventKeyDown, Key: "ArrowDown", Ctrl: true}) {
		t.Fatalf("Ctrl+↓ 不该被字段链消费")
	}
	if sel.expanded {
		t.Fatalf("Ctrl+↓ 不该展开下拉")
	}
	// 不带修饰键时照常
	if !a.handleFieldKey(sel, "ArrowDown", Event{Kind: EventKeyDown, Key: "ArrowDown"}) {
		t.Fatalf("普通 ↓ 该被下拉消费")
	}
	if !sel.expanded {
		t.Fatalf("普通 ↓ 该展开下拉")
	}
	a.closeSelect(sel)

	a.setFocus(dp)
	if a.handleFieldKey(dp, "Enter", Event{Kind: EventKeyDown, Key: "Enter", Alt: true}) {
		t.Fatalf("Alt+Enter 不该被字段链消费")
	}
	if dp.expanded {
		t.Fatalf("Alt+Enter 不该展开日历")
	}
}
