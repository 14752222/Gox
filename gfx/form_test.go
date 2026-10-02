package gfx

import (
	"image"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== T08 <label> / <form> =====

// mkLabel 造一个带文本子节点的标签。
func mkLabel(text string) *GuiNode {
	n := &GuiNode{Tag: "label", Props: map[string]object.Value{}}
	c := &GuiNode{Tag: "#text", Text: text, Props: map[string]object.Value{}}
	mountChildren(n, c)
	return n
}

// countReddish 统计矩形内"明显偏红"的像素数。
//
// 不逐像素比 colorDanger 的精确值: 文字笔画是**抗锯齿**绘制的, 字形边缘
// 与白底混过之后就不再等于原色 —— 与既有像素用例一律比精确色的做法不同,
// 这里的判据是"红通道领先绿/蓝一大截", 对"画了什么颜色"足够敏感, 又不受
// 边缘混合的影响。深灰文字三通道相等, 不会误判。
func countReddish(img *image.RGBA, r Rect) int {
	n := 0
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			c := img.RGBAAt(x, y)
			if int(c.R)-int(c.G) > 30 && int(c.R)-int(c.B) > 30 {
				n++
			}
		}
	}
	return n
}

func TestLabelRequiredStarIsDrawnAndNotInContent(t *testing.T) {
	root := mkNode("column", nil)
	plain := mkLabel("姓名")
	req := mkLabel("邮箱")
	withBool(req, "required", true)
	mountChildren(root, plain, req)

	img := renderTree(root, 200, 80)

	if countReddish(img, req.Box) == 0 {
		t.Fatalf("required 标签该画出红色星号 (盒子 %v)", req.Box)
	}
	if countReddish(img, plain.Box) != 0 {
		t.Fatalf("非 required 标签不该有红点")
	}
	// 星号是**标记**不是内容: 无障碍名与 form 取值都不该看见它
	if got := req.AriaName(); got != "邮箱" {
		t.Fatalf("星号不该污染无障碍名, got %q", got)
	}
	if got := req.TextContent(); got != "邮箱" {
		t.Fatalf("星号不该进 TextContent, got %q", got)
	}
	// required 的标签要给星号留位, 所以更宽
	if req.Box.W <= plain.Box.W {
		t.Fatalf("required 标签应更宽 (留星号位): %v vs %v", req.Box.W, plain.Box.W)
	}
}

func TestLabelAlignRight(t *testing.T) {
	root := mkNode("row", nil)
	l := mkLabel("手机号")
	withNum(l, "width", 120)
	withNum(l, "height", 40) // 显式给高, 才能看出"竖直居中"而不是"顶格"
	withStr(l, "align", "right")
	mountChildren(root, l)

	renderTree(root, 200, 60)

	text := findFirst(l, "#text")
	if text == nil {
		t.Fatalf("标签里没有文本节点")
	}
	// 右对齐: 文字右缘贴内容区右缘
	if got, want := text.Box.X+text.Box.W, l.Box.X+l.Box.W; got != want {
		t.Fatalf("右对齐的文字右缘 = %d, want %d (label.Box=%v text.Box=%v)", got, want, l.Box, text.Box)
	}
	// 竖直居中: 上下留白相等
	top := text.Box.Y - l.Box.Y
	bottom := (l.Box.Y + l.Box.H) - (text.Box.Y + text.Box.H)
	if top != bottom {
		t.Fatalf("文字该在标签内竖直居中: 上 %d / 下 %d (label=%v text=%v)", top, bottom, l.Box, text.Box)
	}
}

func TestLabelAlignRightLeavesRoomForStar(t *testing.T) {
	root := mkNode("row", nil)
	l := mkLabel("手机号")
	withNum(l, "width", 120)
	withStr(l, "align", "right")
	withBool(l, "required", true)
	mountChildren(root, l)

	img := renderTree(root, 200, 40)

	text := findFirst(l, "#text")
	// 右对齐 + required: 星号在文字**左侧**, 文字右缘仍贴右缘
	if got, want := text.Box.X+text.Box.W, l.Box.X+l.Box.W; got != want {
		t.Fatalf("右对齐+required 的文字右缘 = %d, want %d", got, want)
	}
	if countReddish(img, l.Box) == 0 {
		t.Fatalf("required 标签该有红色星号")
	}
}

func TestFormDefaultGapAndExplicitOverride(t *testing.T) {
	// 缺省 gap=10 (h() 里补的): 两个 20 高的按钮之间应留 10px
	root := mkNode("column", nil)
	form := &GuiNode{Tag: "form", Props: map[string]object.Value{}}
	a1 := mkNode("button", map[string]float64{"width": 60, "height": 20})
	a2 := mkNode("button", map[string]float64{"width": 60, "height": 20})
	mountChildren(form, a1, a2)
	mountChildren(root, form)

	renderTree(root, 200, 200)
	if got := a2.Box.Y - (a1.Box.Y + a1.Box.H); got != formDefaultGap {
		t.Fatalf("form 缺省行距 = %d, want %d", got, formDefaultGap)
	}
	if form.Box.Y != 0 {
		t.Fatalf("form 是普通流内容器, 不该像弹层那样取整窗: %v", form.Box)
	}

	withNum(form, "gap", 4)
	renderTree(root, 200, 200)
	if got := a2.Box.Y - (a1.Box.Y + a1.Box.H); got != 4 {
		t.Fatalf("显式 gap 应覆盖缺省值, got %d", got)
	}
}

// formTree 造一张"输入框 + 按钮"的最小表单, 返回 (根, 表单, 输入框, 提交按钮)。
func formTree(withSubmit bool) (*GuiNode, *GuiNode, *GuiNode, *GuiNode) {
	root := mkNode("column", nil)
	form := &GuiNode{Tag: "form", Props: map[string]object.Value{}}
	inp := mkField("input", 120, 24)
	withStr(inp, "name", "email")
	withStr(inp, "value", "a@b.c")
	btn := withClick(mkButton("保存"))
	mountChildren(form, inp, btn)
	mountChildren(root, form)
	return root, form, inp, btn
}

func TestFormEnterSubmitsFromInput(t *testing.T) {
	root, form, inp, btn := formTree(true)
	var submitted []*object.Object
	form.Props["onSubmit"] = object.NewBuiltin("submit", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				submitted = append(submitted, o)
			}
		}
		return object.UndefinedSingleton
	})
	btnClicks := &a11yHit{}
	btn.Props["onClick"] = btnClicks.handler()

	fake, a := mountTestApp(t, root, 260, 160)
	if len(a11yFocusOrder(root)) == 0 {
		t.Fatalf("表单里应有可聚焦控件")
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	if a.focused != inp {
		t.Fatalf("Tab 应聚焦输入框, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})

	if len(submitted) != 1 {
		t.Fatalf("回车应提交表单一次, got %d", len(submitted))
	}
	values, ok := submitted[0].GetProperty("values")
	if !ok {
		t.Fatalf("onSubmit 载荷缺 values")
	}
	obj, ok := values.(*object.Object)
	if !ok {
		t.Fatalf("values 不是对象: %s", values.Type())
	}
	got, ok := obj.GetProperty("email")
	if !ok {
		t.Fatalf("values 里应有 name=\"email\" 字段, 实际只有键 %v", keysOf(obj))
	}
	if s, _ := got.(*object.String); s == nil || s.Value != "a@b.c" {
		t.Fatalf("values.email = %v, want a@b.c", got.Inspect())
	}
	if btnClicks.n != 0 {
		t.Fatalf("输入框里回车不该顺手按下表单里的按钮")
	}
}

func TestFormEnterOnButtonActivatesButtonNotSubmit(t *testing.T) {
	root, form, _, btn := formTree(true)
	submits := 0
	form.Props["onSubmit"] = object.NewBuiltin("submit", func(args ...object.Value) object.Value {
		submits++
		return object.UndefinedSingleton
	})
	clicks := &a11yHit{}
	btn.Props["onClick"] = clicks.handler()

	fake, a := mountTestApp(t, root, 260, 160)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"}) // 输入框
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"}) // 按钮
	if a.focused != btn {
		t.Fatalf("Tab 应走到按钮上, got %v", a.focused)
	}
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if clicks.n != 1 {
		t.Fatalf("回车在按钮上应触发 onClick, got %d", clicks.n)
	}
	if submits != 0 {
		t.Fatalf("回车在按钮上不该提交表单")
	}
}

func TestFormWithoutHandlerDoesNotSwallowEnter(t *testing.T) {
	// 没有 onSubmit 时回车该继续给脚本的 onKeyDown (消费纪律: 只在做了事时消费)
	root, _, inp, _ := formTree(false)
	seen := &a11yHit{}
	inp.Props["onKeyDown"] = seen.handler()

	fake, a := mountTestApp(t, root, 260, 160)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Tab"})
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if seen.n != 1 {
		t.Fatalf("没有 onSubmit 时按键该派发给脚本, got %d", seen.n)
	}
}

func TestFormValuesCollectsAllFieldKinds(t *testing.T) {
	root := mkNode("column", nil)
	form := &GuiNode{Tag: "form", Props: map[string]object.Value{}}

	inp := mkField("input", 100, 24)
	withStr(inp, "name", "user")
	withStr(inp, "value", "amy")

	ta := mkNode("textarea", map[string]float64{"width": 100, "height": 40})
	withStr(ta, "name", "note")
	withStr(ta, "value", "你好")

	sel := mkField("select", 100, 28)
	withStr(sel, "name", "city")
	withStr(sel, "value", "sh")

	cb := mkField("checkbox", 18, 18)
	withStr(cb, "name", "agree")
	withBoolProp(cb, "checked", true)

	radioA := mkField("radio", 18, 18)
	withStr(radioA, "name", "plan")
	withStr(radioA, "value", "pro")
	radioB := mkField("radio", 18, 18)
	withStr(radioB, "name", "plan")
	withStr(radioB, "value", "free")
	withBoolProp(radioB, "checked", true)

	sl := mkNode("slider", map[string]float64{"width": 100, "height": 24, "min": 0, "max": 10, "value": 7})
	withStr(sl, "name", "volume")

	anon := mkField("input", 60, 24) // 没有 name: 不进 values (与 HTML 一致)
	withStr(anon, "value", "忽略我")

	mountChildren(form, inp, ta, sel, cb, radioA, radioB, sl, anon)
	mountChildren(root, form)
	renderTree(root, 260, 320)

	values := formValues(form)
	cases := []struct{ key, want string }{
		{"user", "amy"},
		{"note", "你好"},
		{"city", "sh"},
		{"agree", "true"},
		{"plan", "free"}, // 只有选中的那个 radio 贡献值
		{"volume", "7"},
	}
	for _, c := range cases {
		v, ok := values.GetProperty(c.key)
		if !ok {
			t.Fatalf("values 缺字段 %q (有: %v)", c.key, keysOf(values))
		}
		if v.Inspect() != c.want {
			t.Fatalf("values.%s = %s, want %s", c.key, v.Inspect(), c.want)
		}
	}
	if _, ok := values.GetProperty("plan_free"); ok {
		t.Fatalf("未选择的 radio 不该贡献值")
	}
	if _, ok := values.GetProperty(""); ok {
		t.Fatalf("没有 name 的字段不该进 values")
	}
	if _, ok := values.GetProperty("undefined"); ok {
		t.Fatalf("没有 name 的字段不该进 values")
	}
}

func TestFormValuesSkipsClosedOverlayFields(t *testing.T) {
	root := mkNode("column", nil)
	form := &GuiNode{Tag: "form", Props: map[string]object.Value{}}
	dlg := &GuiNode{Tag: "dialog", Props: map[string]object.Value{}}
	withBoolProp(dlg, "open", false)
	card := mkNode("column", nil)
	hidden := mkField("input", 80, 24)
	withStr(hidden, "name", "secret")
	withStr(hidden, "value", "x")
	mountChildren(card, hidden)
	mountChildren(dlg, card)
	mountChildren(form, dlg)
	mountChildren(root, form)
	renderTree(root, 240, 160)

	if _, ok := formValues(form).GetProperty("secret"); ok {
		t.Fatalf("关闭的弹层里的字段不该算进这次提交")
	}
	withBoolProp(dlg, "open", true)
	renderTree(root, 240, 160)
	if _, ok := formValues(form).GetProperty("secret"); !ok {
		t.Fatalf("打开的弹层里的字段应该算进提交")
	}
}

func TestFormDisabledBlocksSubmit(t *testing.T) {
	root, form, inp, _ := formTree(true)
	withBool(form, "disabled", true)
	submits := 0
	form.Props["onSubmit"] = object.NewBuiltin("submit", func(args ...object.Value) object.Value {
		submits++
		return object.UndefinedSingleton
	})

	fake, a := mountTestApp(t, root, 260, 160)
	// 禁用子树里的字段不进遍历序 (a11y) 且点击不获焦 —— 这里直接把焦点按上去
	// 模拟"脚本自己聚焦", 回车也不该提交。
	a.setFocus(inp)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "Enter"})
	if submits != 0 {
		t.Fatalf("禁用的表单不该被回车提交")
	}
}

// keysOf 列出对象的所有键 (失败信息里用)。
func keysOf(o *object.Object) []string {
	out := make([]string, 0, len(o.Properties))
	for k := range o.Properties {
		out = append(out, k)
	}
	return out
}
