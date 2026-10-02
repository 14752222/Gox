package gfx

import (
	"image"

	"github.com/14752222/Gox/object"
)

// T08 表单容器两件套: <label> 与 <form>。
//
// 这两个是**结构性**组件 (不是取值控件), 但它们缺了会让"表单"这件事在每个
// 应用里被手搓一遍: 标签的必填星号、标签列宽对齐、回车提交、整表取值。
// 内核把它们收成标签, 是为了让表单的键盘链路 (Tab 进出 / 回车提交) 有一个
// 明确的归属节点 —— T10 的 Enter 提交就是找最近的那个 form。
//
//	<form gap={12} onSubmit={(e) => save(e.values)}>
//	  <row gap={8}>
//	    <label required width={72}>姓名</label>
//	    <input name="name" model={name} placeholder="请输入姓名" />
//	  </row>
//	  <row gap={8}>
//	    <label width={72}>备注</label>
//	    <textarea name="note" model={note} height={60} />
//	  </row>
//	  <button onClick={submit}>保存</button>
//	</form>
//
// 标签列对齐由 label 自己的 width 决定, **form 不代劳**: 标签通常包在 row 里,
// 让 form 去子树深处改写宽度属于"看不见的魔法"(一个 form 级 prop 悄悄改了
// 别处的布局), 而写一次 width 的成本是一样的。想要右对齐用 label align="right"。
//
// 受控/非受控: 两者都不持有值, 所以"两用"在这里是空话 —— 表单里的**字段**
// 各自受控 (value/checked + onChange), form 只负责"把它们的当前值收集起来"
// 交给 onSubmit。收集口径见 formValues。
//
// 取值只收**带 name** 的字段 (与 HTML 表单一致): 没有 name 的字段不进 values,
// 否则后端会收到一堆空键。

// labelRequiredGap 是标签文字与必填星号之间的间距。
const labelRequiredGap = 3

// labelStarW 是必填星号的名义宽度 (星号字形窄, 用固定宽比测量更稳 ——
// 不同字体的 "*" 宽度差异很大, 量出来的值会让布局在字体切换时抖动)。
const labelStarW = 7

// labelRequired 读 required prop。
func (n *GuiNode) labelRequired() bool {
	v, _ := n.PropBool("required")
	return v
}

// labelAlignRight 报告标签是否右对齐 (align="right")。
func (n *GuiNode) labelAlignRight() bool {
	s, _ := n.PropStr("align")
	return s == "right"
}

// labelTextX 返回标签文字的左缘 x (左对齐 = 内容区左缘, 右对齐 = 右缘减宽)。
//
// 布局 (layoutLabel) 与绘制 (paintLabel) 都经它取值 —— 星号跟着文字跑,
// 两处各算一份的话右对齐时星号会被画到内容区左缘之外 (实测: 负数坐标,
// 直接在窗口外面)。
func labelTextX(n *GuiNode, area Rect, textW int) int {
	if n.labelAlignRight() {
		x := area.X + area.W - textW
		if x < area.X {
			return area.X
		}
		return x
	}
	return area.X
}

// labelStarX 返回必填星号应该画在哪个 x (左对齐时紧跟文字, 右对齐时贴文字
// 左侧)。绘制与布局共用这一处计算 —— 两处各写一份必然漂移成"星号压着字"。
func labelStarX(n *GuiNode, textX, textW int) int {
	if n.labelAlignRight() {
		return textX - labelRequiredGap - labelStarW
	}
	return textX + textW + labelRequiredGap
}

// layoutLabel 摆放标签的文字子节点。
//
// 语义是"单行文字 + 可选星号": 流式子节点横排 (与 button 同一套), 且
// align="right" 时整段贴内容区右缘 —— 表单里标签列右对齐是最常见的排版
// 需求, 没有它每个应用都要手写一堆 spacer。
//
// 星号位置的**两种**排版方向不同:
//   - 左对齐: 星号跟在文字后面, 所以固有宽度 = 文字 + gap + 星号;
//   - 右对齐: 星号在文字**左边**, 文字右缘仍要贴右缘 ⇒ 摆放时不能让出星号
//     位 (让了就成了"右对齐但右边空一格", 看起来像没对齐)。
func layoutLabel(n *GuiNode) {
	area := inner(n)
	if area.W < 0 {
		area.W = 0
	}
	if area.H < 0 {
		area.H = 0
	}
	total := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, _ := c.intrinsicSize()
		total += cw
	}
	x := labelTextX(n, area, total)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.intrinsicSize()
		if x+cw > area.X+area.W {
			cw = area.X + area.W - x
		}
		if cw < 0 {
			cw = 0
		}
		c.Box = Rect{X: x, Y: area.Y + (area.H-ch)/2, W: cw, H: ch}
		layoutNode(c)
		x += cw
	}
	placeAbsoluteIn(n, area)
}

// intrinsicLabel 返回标签的固有尺寸: 文字累加 + 星号 + padding。
func intrinsicLabel(n *GuiNode) (w, h int) {
	w, h = n.contentSize()
	if n.labelRequired() {
		w += labelRequiredGap + labelStarW
	}
	pt, pr, pb, pl := paddingOf(n)
	return w + pl + pr, h + pt + pb
}

// paintLabel 画标签: 先补通用装饰, 再在文字之后画必填星号 (`*` 用 danger 色)。
//
// 星号画在这里而不是塞一个 #text 子节点: 它是**标记**不是内容, 塞进树里会
// 让 TextContent() 变成 "姓名*", 无障碍名、h() 的文本拼接、乃至 form 取值
// 都要跟着处理这个尾巴 —— 一个纯视觉标记不值得污染数据面。
func paintLabel(img *image.RGBA, n *GuiNode, disabled bool) {
	paintBoxDecor(img, n, disabled)
	if !n.labelRequired() {
		return
	}
	text := n.TextContent()
	size := n.FontSize()
	tw, th := MeasureText(text, size)
	if th < 1 {
		th = lineHeight(size)
	}
	area := inner(n)
	y := area.Y + (area.H-th)/2
	x := labelStarX(n, labelTextX(n, area, tw), tw)
	DrawText(img, img.Bounds(), "*", x, y, size, tint(colorDanger, disabled), labelStarW+2)
}

// ===== form =====

// formDefaultGap 是 form 的缺省行距。表单行挤在一起是纯粹的可用性问题,
// 给一个 10px 的缺省比让每个应用自己写 gap 更省事 (显式 gap 永远覆盖它)。
const formDefaultGap = 10

// formInChain 从 n 起沿祖先链找第一个 form。
func formInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "form" {
			return p
		}
	}
	return nil
}

// formFieldTags 是"form 会收集其值"的标签。
//
// 新增取值控件时这里要补一行 —— 这是"form 帮你收集值"这个承诺的代价, 所以
// 单独列一张表而不是散在 switch 里: 漏了的表现是"提交上去少一个字段",
// 而少字段不会报错, 只会让后端收到不完整的表单。
var formFieldTags = map[string]struct{}{
	"input": {}, "search": {}, "textarea": {}, "select": {}, "checkbox": {},
	"switch": {}, "radio": {}, "slider": {}, "rating": {}, "datepicker": {},
	"colorpicker": {}, "upload": {},
}

// formShouldSubmit 判断"焦点在 n 上按回车该不该提交表单"。
//
// 从焦点往上走, 路上遇到 form 就说明中间没有别的可激活控件 (回车归表单);
// 先遇到可激活控件 (button / checkbox / switch / radio / combobox 类) 则
// 回车归它 —— 焦点在按钮上按回车是"按这个按钮", 在复选框上是"切换", 都不该
// 顺手把整张表单提交出去。这与浏览器里 Enter 在 checkbox 上什么都不做、
// 在 textarea 上换行是同一套直觉。
//
// 可编辑字段只认 input / search / textarea: select 与两个 picker 的隐式 role
// 是 combobox (可激活, Enter 用来展开弹层), 它们不需要在这条路上单独列。
// 实际上能走到这里的只有 input —— textarea 的 Enter 是换行、search 的
// Enter 是 onSearch, 都由 handleFieldKey 先消费掉了。
func formShouldSubmit(n *GuiNode) bool {
	editable := false
	for p := n; p != nil; p = p.Parent {
		switch p.Tag {
		case "form":
			return editable
		case "input", "search", "textarea":
			editable = true
		default:
			if p.a11yActivatable() {
				return false
			}
		}
	}
	return false
}

// formValues 收集表单当前值: 遍历子树, 取每个带 name prop 的字段的值。
//
// 取值一律读**受控 prop** (value/checked), 不读内部状态 —— 与内核"受控组件
// 的显示只从 props 来"同一口径。所以"提交上去的是旧值"只可能有一个原因:
// 脚本没收 onInput 写回 signal (那就是受控的定义, 不是 bug)。
func formValues(form *GuiNode) *object.Object {
	out := object.NewObject()
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.isOverlay() && !n.overlayVisible() {
			return // 关闭的弹层里的字段不算 (它们的值不属于这次提交)
		}
		if _, isField := formFieldTags[n.Tag]; isField {
			if name, ok := n.PropStr("name"); ok && name != "" {
				if v, ok := formFieldValue(n); ok {
					out.SetProperty(name, v)
				}
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(form)
	return out
}

// formFieldValue 读单个字段的当前值。
//
// radio 是特例: 它是**一组**共用一个 name, 只有选中的那个贡献值 (值取该
// radio 自己的 value prop)。其它情况直接读各自的受控 prop。未选中的 radio
// 返回 ok=false, 于是同名的后续 radio 不会把已写入的值覆盖掉。
func formFieldValue(n *GuiNode) (object.Value, bool) {
	switch n.Tag {
	case "input", "search", "textarea":
		return object.NewString(n.inputValue()), true
	case "select":
		return object.NewString(n.selectValue()), true
	case "checkbox", "switch":
		return object.NewBoolean(n.checked()), true
	case "radio":
		if !n.checked() {
			return object.UndefinedSingleton, false
		}
		if s, ok := n.PropStr("value"); ok {
			return object.NewString(s), true
		}
		return object.NewString(n.TextContent()), true
	case "slider":
		return object.NewNumber(sliderValue(n)), true
	case "rating":
		return object.NewNumber(n.ratingValue()), true
	case "datepicker", "colorpicker":
		if s, ok := n.PropStr("value"); ok {
			return object.NewString(s), true
		}
		return object.NewString(""), true
	case "upload":
		if v, ok := n.Props["value"]; ok {
			return v, true
		}
		return object.NewArray(nil), true
	}
	return object.UndefinedSingleton, false
}

// formSubmit 派发 onSubmit({values})。values 是 {name: 值} 对象。
func (a *app) formSubmit(form *GuiNode) bool {
	if form == nil || form.disabledInChain() {
		return false
	}
	h := form.PropHandler("onSubmit")
	if h == nil {
		return false
	}
	arg := object.NewObject()
	arg.SetProperty("values", formValues(form))
	a.callHandler(form, "onSubmit", arg)
	return true
}
