package gfx

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P2-3 下拉框 / P2-4 弹层 =====
//
// 这两个组件都踩在 P2-2 的层叠模型上, 所以用例的重点是"层"的两条不变量:
//   1. 绘制顺序 == 命中顺序 (看到的和点到的是同一层);
//   2. 弹层不受祖先盒子裁剪, 但模态遮罩要挡住它下面的一切交互。
// 纯 Go 层验状态机与像素, 全链路 (假 Surface + 真 VM) 验"点击 → 回调 → signal
// → 重绘"确实串起来了。

var (
	pxFieldFace    = colorFieldFace
	pxInputEdge    = colorInputEdge
	pxOptionActive = colorOptionActive
	pxPopupFace    = colorPopupFace
	pxPopupEdge    = colorPopupEdge
	pxToastAccent  = colorAccent
)

// mkSelect 造一个带 options / value 的 select (默认闭合)。
// 走 attachSelectHandler 补上内置展开处理器 —— 真实脚本里这一步由 h() 完成,
// 测试直接建节点时也必须补上, 否则命中测试找不到它 (select 就没有 onClick)。
func mkSelect(options []string, value string) *GuiNode {
	elems := make([]object.Value, 0, len(options))
	for _, o := range options {
		elems = append(elems, object.NewString(o))
	}
	n := &GuiNode{Tag: "select", Props: map[string]object.Value{
		"options": object.NewArray(elems),
		"value":   object.NewString(value),
	}}
	n.highlight = -1
	attachSelectHandler(n)
	return n
}

// textStartingWith 返回第一个以此前缀开头的文本节点内容 (脚本侧断言的镜像)。
func textStartingWith(root *GuiNode, prefix string) string {
	for _, n := range findAll(root, "#text") {
		if strings.HasPrefix(n.Text, prefix) {
			return n.Text
		}
	}
	return ""
}

// textContainsAny 报告树上有没有文本片段包含 substr。
func textContainsAny(root *GuiNode, substr string) bool {
	for _, n := range findAll(root, "#text") {
		if strings.Contains(n.Text, substr) {
			return true
		}
	}
	return false
}

// ===== P2-3 select =====

func TestSelectClosedGeometryAndFace(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"apple", "banana"}, "banana")
	mountChildren(root, sel)

	img := renderTree(root, 300, 160)

	if sel.Box.H != selectRowH {
		t.Fatalf("select 行高 = %d, want %d", sel.Box.H, selectRowH)
	}
	if sel.Box.W < selectMinW {
		t.Fatalf("select 宽度 = %d, want >= %d", sel.Box.W, selectMinW)
	}
	// 闭合态: 白底 + 1px #999 边框 (与 input 同一套外观)
	assertPx(t, img, sel.Box.X+4, sel.Box.Y+selectRowH/2, pxFieldFace, "字段白底")
	assertPx(t, img, sel.Box.X, sel.Box.Y, pxInputEdge, "字段边框")
	// 标签 (当前值 "banana") 真的画出来了: 文本行内有非底色的像素
	if !hasInkIn(img, Rect{sel.Box.X + fieldPadX, sel.Box.Y, sel.Box.W - fieldPadX*2 - selectArrowW, sel.Box.H}) {
		t.Fatalf("select 未绘制当前值文本")
	}
	// 未展开: 树上不该有下拉项
	if n := countTag(root, "select-option"); n != 0 {
		t.Fatalf("未展开时下拉项数量 = %d, want 0", n)
	}
}

// hasInkIn 报告矩形内有没有"非底色"的像素 (字体渲染受平台影响, 断言具体
// 字形不现实, 但"这一带画了东西"是稳定的)。
func hasInkIn(img *image.RGBA, r Rect) bool {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			if c := img.RGBAAt(x, y); c != pxFieldFace && c != pxWhite {
				return true
			}
		}
	}
	return false
}

func TestSelectPlaceholderShownWhenValueEmpty(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"apple"}, "")
	withStr(sel, "placeholder", "Pick a fruit")
	mountChildren(root, sel)

	renderTree(root, 300, 120)

	// 无值时按 placeholder 文本定宽 (而不是退化成最小宽度)
	tw, _ := MeasureText("Pick a fruit", sel.FontSize())
	if want := tw + 2*fieldPadX + selectArrowW; sel.Box.W != want {
		t.Fatalf("placeholder 未参与宽度计算: got %d, want %d", sel.Box.W, want)
	}
	label, ok := sel.selectLabel()
	if ok {
		t.Fatalf("value 为空时 selectLabel 应报告 ok=false, got %q", label)
	}
}

func TestSelectPopupEscapesFieldBox(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b", "c"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw() // 展开后需重新布局才知道弹层盒子

	if !sel.expanded || sel.popup == nil {
		t.Fatalf("openSelect 未建立弹层")
	}
	rows := selectRows(sel)
	if len(rows) != 3 {
		t.Fatalf("下拉项数量 = %d, want 3", len(rows))
	}
	// 弹层贴在字段正下方、与字段等宽 (下拉框的常见观感)
	if sel.popup.Box.Y != sel.Box.Y+sel.Box.H {
		t.Fatalf("弹层未贴在字段下方: %v (字段 %v)", sel.popup.Box, sel.Box)
	}
	if sel.popup.Box.W != sel.Box.W {
		t.Fatalf("弹层宽度 = %d, want 与字段一致 (%d)", sel.popup.Box.W, sel.Box.W)
	}
	// 最后一项整体在 28px 的字段盒子之外 —— 逃逸裁剪必须让它照画
	last := rows[2]
	if last.Box.Y <= sel.Box.Y+sel.Box.H {
		t.Fatalf("下拉项应落在字段盒子之外: %v (字段 %v)", last.Box, sel.Box)
	}

	a.setHighlight(sel, 2)
	a.redraw()
	img := shotsImage(fake)
	mid := Rect{X: last.Box.X + 2, Y: last.Box.Y + selectRowH/2, W: 1, H: 1}
	assertPx(t, img, mid.X, mid.Y, pxOptionActive, "高亮行应铺浅蓝底")

	// 点到的和看到的同一层: 字段盒子之外的弹层区域照样可命中
	if hit := HitTest(root, mid.X, mid.Y); hit != last {
		t.Fatalf("弹层溢出区域应命中下拉项, got %v", hit)
	}
	// 未高亮且未被悬停的项仍是弹层白底
	assertPx(t, img, rows[0].Box.X+2, rows[0].Box.Y+selectRowH/2, pxPopupFace, "普通项为弹层白底")
	// 弹层 1px 边框在字段盒子之外也画出来了 (父盒没裁掉它)
	assertPx(t, img, sel.popup.Box.X, sel.popup.Box.Y+sel.popup.Box.H-1, pxPopupEdge, "弹层下边框")
}

func TestSelectOptionClickChoosesClosesAndRefocuses(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b", "c"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw()
	rows := selectRows(sel)

	// 点到的必须是那一行: 选项行自带 onClick (Go 侧 builtin), 命中测试要能找到它。
	// 该处理器内部调 chooseOption —— 派发走 callScriptFn (P3-5 修正), 所以
	// 纯 Go 用例里 Go 侧 builtin 是**真的会执行**的: 点击即选中并收起。
	if hit := HitTest(root, rows[1].Box.X+4, rows[1].Box.Y+selectRowH/2); hit != rows[1] {
		t.Fatalf("点击应命中第二个下拉项, got %v", hit)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: rows[1].Box.X + 4, Y: rows[1].Box.Y + selectRowH/2})
	// 回调执行 → 弹层收起 (这正是"开合由回调驱动"的证明)。
	// 注: P3-5 之前 handleClick 直接走 object.CallFunction, currentVM 为 nil
	// 时静默返回 undefined, 于是这里断言的是"弹层仍开着" —— 那条断言记录的
	// 是当时的一个真 bug (无 VM 嵌入场景下所有 onClick 都失效), 现已修掉。
	if sel.expanded {
		t.Fatalf("选中后弹层应收起")
	}
	if n := countTag(root, "select-option"); n != 0 {
		t.Fatalf("收起后树上仍有 %d 个下拉项", n)
	}
	a.mu.Lock()
	afterClick := a.focused
	a.mu.Unlock()
	if afterClick != sel {
		t.Fatalf("选中后焦点应收回 select, got %v", afterClick)
	}
}

// 无回调的选项点击 (纯状态机): 走 chooseOption 直接验"选中→收起→释放弹层"。
func TestSelectChooseOptionStateMachine(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b", "c"}, "a")
	mountChildren(root, sel)

	_, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw()

	// 状态机: 选中 → 收起 → 弹层销毁 → 焦点收回
	a.chooseOption(sel, "b")
	if sel.expanded || sel.popup != nil {
		t.Fatalf("选中后应收起并释放弹层")
	}
	if n := countTag(root, "select-option"); n != 0 {
		t.Fatalf("收起后树上仍有 %d 个下拉项", n)
	}
	a.mu.Lock()
	focused := a.focused
	a.mu.Unlock()
	if focused != sel {
		t.Fatalf("选中后焦点应收回 select, got %v", focused)
	}
}

func TestSelectOutsideClickClosesAndSwallows(t *testing.T) {
	// 用 row + alignItems:start 让两个组件并排, 避免"下面的按钮正好落在
	// 弹层覆盖范围内" —— 下拉弹层会盖住它正下方的内容, 这是真实行为。
	root := mkNode("row", nil)
	withStr(root, "alignItems", "start")
	sel := mkSelect([]string{"a", "b"}, "a")
	btn := withClick(mkNode("button", map[string]float64{"width": 120, "height": 30}))
	mountChildren(root, sel, btn)

	fake, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw()
	if sel.popup.Box.Contains(btn.Box.X+2, btn.Box.Y+2) {
		t.Fatalf("测试点落在弹层内, 用例前提不成立 (弹层 %v, 点 %d,%d)",
			sel.popup.Box, btn.Box.X+2, btn.Box.Y+2)
	}

	// 按下 → 收起弹层, 且这次点击被吞掉 (不落到下面的按钮上)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if sel.expanded {
		t.Fatalf("点弹层外应收起下拉")
	}
	a.mu.Lock()
	swallow, focused := a.swallowClick, a.focused
	a.mu.Unlock()
	if !swallow {
		t.Fatalf("这次点击应被标记吞掉")
	}
	if focused == btn {
		t.Fatalf("被吞掉的点击不应改变焦点")
	}

	// 抬起: 吞掉标记消费掉, 不再触发回调
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	a.mu.Lock()
	swallow, focused = a.swallowClick, a.focused
	a.mu.Unlock()
	if swallow {
		t.Fatalf("吞掉标记应在抬起时清除")
	}
	if focused == btn {
		t.Fatalf("被吞掉的抬起不应触发下面的按钮")
	}

	// 再点一次就恢复正常: 焦点落到按钮上
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	a.mu.Lock()
	focused = a.focused
	a.mu.Unlock()
	if focused != btn {
		t.Fatalf("下一次点击应正常命中按钮, got %v", focused)
	}
}

// TestSelectClickFieldTogglesOpen: 点字段本身是组件的内置语义 (展开),
// 不依赖脚本写 onClick; 同时验证"点开着的字段会先被外部点击逻辑收起,
// 且那次点击被吞掉, 不会连锁再展开一次"。
func TestSelectClickFieldTogglesOpen(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 200)
	if sel.PropHandler("onClick") == nil {
		t.Fatalf("select 应自带展开处理器 (否则命中测试找不到它)")
	}
	fx, fy := sel.Box.X+6, sel.Box.Y+selectRowH/2
	if hit := HitTest(root, fx, fy); hit != sel {
		t.Fatalf("点字段应命中 select, got %v", hit)
	}

	// 纯 Go 环境回调不执行, 直接走内部语义: 展开 → 点字段 → 收起
	a.toggleSelect(sel)
	a.redraw()
	if !sel.expanded {
		t.Fatalf("toggleSelect 应展开下拉")
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: fx, Y: fy})
	if sel.expanded {
		t.Fatalf("点展开中的字段应收起下拉")
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: fx, Y: fy})
	if sel.expanded {
		t.Fatalf("收起这次点击应被吞掉, 不该把下拉再次打开")
	}
}

// TestSelectOutsideClickInsidePopupKeepsOpen: 点弹层里 (但没点中选项行, 例如
// 1px 内边距) 不该收起弹层 —— 否则弹层边缘会出现"点了就关"的诡异手感。
func TestSelectOutsideClickInsidePopupKeepsOpen(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw()

	// 弹层左下角的 1px padding 带 (在弹层盒子内, 不在任何选项行上)
	x, y := sel.popup.Box.X, sel.popup.Box.Y+sel.popup.Box.H-1
	if !sel.popup.Box.Contains(x, y) {
		t.Fatalf("测试点不在弹层内")
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if !sel.expanded {
		t.Fatalf("点弹层自身不该收起它")
	}
}

func TestSelectKeyboardOpenMoveWrapChooseAndEscape(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sel := mkSelect([]string{"a", "b", "c"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 200)
	a.setFocus(sel)

	key := func(k string) {
		pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: k})
	}

	key("Enter")
	if !sel.expanded {
		t.Fatalf("Enter 应展开下拉")
	}
	if sel.highlight != 0 {
		t.Fatalf("展开后光标应在首项, got %d", sel.highlight)
	}
	key("ArrowDown")
	if sel.highlight != 1 {
		t.Fatalf("ArrowDown 后光标 = %d, want 1", sel.highlight)
	}
	key("ArrowDown")
	key("ArrowDown") // 1 → 2 → 0 回环
	if sel.highlight != 0 {
		t.Fatalf("下键应回环到首项, got %d", sel.highlight)
	}
	key("ArrowUp")
	if sel.highlight != 2 {
		t.Fatalf("上键应从首项回环到末项, got %d", sel.highlight)
	}
	// 键盘光标要有视觉表现
	a.redraw()
	img := shotsImage(fake)
	rows := selectRows(sel)
	assertPx(t, img, rows[2].Box.X+2, rows[2].Box.Y+selectRowH/2, pxOptionActive, "键盘光标行高亮")

	key("Enter") // 选中光标所在项
	if sel.expanded {
		t.Fatalf("Enter 选中后应收起")
	}

	// Space 展开, Esc 收起
	key(" ")
	if !sel.expanded {
		t.Fatalf("Space 应展开下拉")
	}
	key("Escape")
	if sel.expanded {
		t.Fatalf("Esc 应收起下拉")
	}
	// 收起后 Esc 不再被消费: 没有 select 拦截时应落到 dialog 兜底 (此处无 dialog → 无副作用)
	key("Escape")
	if sel.expanded {
		t.Fatalf("Esc 不该重新展开")
	}
}

// TestSelectArrowDownOpensWhenClosed: 闭合态按下键的习惯行为是"展开并定位"。
func TestSelectArrowDownOpensWhenClosed(t *testing.T) {
	root := mkNode("column", nil)
	sel := mkSelect([]string{"a", "b"}, "a")
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 120)
	a.setFocus(sel)
	pushAndPump(t, fake, a, Event{Kind: EventKeyDown, Key: "ArrowDown"})
	if !sel.expanded {
		t.Fatalf("闭合态按下键应展开下拉")
	}
}

// TestSelectHighlightDirtiesOnlyChangedRows: 键盘移动光标只该重画变化的两行,
// 局部重绘下拖上整帧是白费 (P1-4 对 hover 提的同一条性能要求)。
func TestSelectHighlightDirtiesOnlyChangedRows(t *testing.T) {
	root := mkNode("column", nil)
	sel := mkSelect([]string{"a", "b", "c"}, "a")
	mountChildren(root, sel)

	_, a := mountTestApp(t, root, 300, 200)
	a.openSelect(sel)
	a.redraw()
	rows := selectRows(sel)

	a.setHighlight(sel, 2) // 0 → 2
	a.mu.Lock()
	dirty := a.dirtyNodes
	a.mu.Unlock()
	if len(dirty) != 2 {
		t.Fatalf("移动光标应只标脏两行, got %d", len(dirty))
	}
	if _, ok := dirty[rows[2]]; !ok {
		t.Fatalf("新的光标行未被标脏")
	}
	if _, ok := dirty[rows[0]]; !ok {
		t.Fatalf("旧的光标行未被标脏 (高亮残影)")
	}
	if _, ok := dirty[rows[1]]; ok {
		t.Fatalf("未变化的行不该被标脏")
	}
}

func TestSelectDisabledBlocksOpen(t *testing.T) {
	root := mkNode("column", nil)
	sel := mkSelect([]string{"a"}, "a")
	withBool(sel, "disabled", true)
	mountChildren(root, sel)

	fake, a := mountTestApp(t, root, 300, 120)
	// 禁用节点的点击根本不进入 handleClick (P0-3), 所以直接点它什么都不会发生
	pushAndPump(t, fake, a, Event{Kind: EventMouseUp, X: sel.Box.X + 4, Y: sel.Box.Y + sel.Box.H/2})
	if sel.expanded {
		t.Fatalf("禁用状态的下拉不该被点开")
	}
}

// ===== P2-4 dialog =====

// mkDialog 造一个 dialog (open 由 open 参数定), 内容卡片是带 padding 的 column。
func mkDialog(open bool, cardW, cardH float64) (*GuiNode, *GuiNode) {
	dlg := &GuiNode{Tag: "dialog", Props: map[string]object.Value{}}
	withBoolProp(dlg, "open", open)
	card := mkNode("column", map[string]float64{"padding": 10})
	inner := mkNode("rect", map[string]float64{"width": cardW, "height": cardH})
	mountChildren(card, inner)
	mountChildren(dlg, card)
	return dlg, card
}

func TestDialogMaskAlphaAndCenteredCard(t *testing.T) {
	root := mkNode("column", nil)
	back := withStr(mkNode("rect", map[string]float64{"width": 200, "height": 120}), "background", "#1a5fb4")
	dlg, card := mkDialog(true, 40, 20)
	mountChildren(root, back, dlg)

	img := renderTree(root, 200, 120)

	// 遮罩是半透明的: 蓝色底 #1a5fb4 被 40% 黑压暗 (预乘 src-over, 整数截断)
	// 26*153/255=15, 95*153/255=57, 180*153/255=108
	assertPx(t, img, 5, 5, color.RGBA{15, 57, 108, 255}, "遮罩应透出底下的蓝色")
	// 卡片居中: (200-60)/2=70, (120-40)/2=40 → 内容 40+20 与 20+20
	if card.Box.X != 70 || card.Box.Y != 40 || card.Box.W != 60 || card.Box.H != 40 {
		t.Fatalf("内容卡片未居中: %v (want 70,40,60,40)", card.Box)
	}
	// 卡片缺省白底 (dialog 补的), 盖在遮罩之上
	assertPx(t, img, card.Box.X+5, card.Box.Y+5, pxPopupFace, "卡片缺省白底")
	assertPx(t, img, card.Box.X, card.Box.Y, pxPopupEdge, "卡片缺省边框")
}

func TestClosedDialogInvisibleAndPassThrough(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	dlg, _ := mkDialog(false, 40, 20)
	mountChildren(root, btn, dlg)

	img := renderTree(root, 200, 120)

	if dlg.Box != (Rect{}) {
		t.Fatalf("关闭的 dialog 盒子应为空: %v", dlg.Box)
	}
	assertPx(t, img, btn.Box.X+2, btn.Box.Y+2, pxBtnFace, "关闭的 dialog 不该画遮罩")
	// 关闭的弹层也不拦截: 点击照旧命中它下面的按钮
	if hit := HitTest(root, btn.Box.X+2, btn.Box.Y+2); hit != btn {
		t.Fatalf("关闭的 dialog 不应拦截点击, got %v", hit)
	}
}

func TestDialogMaskSwallowsClicksBelow(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	dlg, card := mkDialog(true, 40, 20)
	cardBtn := withClick(mkNode("button", map[string]float64{"width": 30, "height": 16}))
	mountChildren(card, cardBtn)
	mountChildren(root, btn, dlg)

	renderTree(root, 200, 120)

	// 遮罩区: HitTest 必须返回 nil (不下钻到遮罩下面的按钮)
	mx, my := btn.Box.X+2, btn.Box.Y+2
	if hit := HitTest(root, mx, my); hit != nil {
		t.Fatalf("遮罩应吃掉其下的点击, got %v", hit)
	}
	// 事件层据此判定"点的是遮罩" → 触发 onClose
	d := modalAt(root, mx, my)
	if d != dlg {
		t.Fatalf("modalAt 未找到遮罩上的 dialog: %v", d)
	}
	if !dialogMaskHit(dlg, mx, my) {
		t.Fatalf("遮罩区应判定为 mask 命中")
	}
	// 点在内容卡片上不算"点遮罩"
	if dialogMaskHit(dlg, card.Box.X+2, card.Box.Y+2) {
		t.Fatalf("内容卡片不该判定为 mask 命中")
	}
	// 卡片内部的按钮照常可命中 (模态不吞自己的内容)
	if hit := HitTest(root, cardBtn.Box.X+2, cardBtn.Box.Y+2); hit != cardBtn {
		t.Fatalf("卡片内的按钮应可命中, got %v", hit)
	}
}

// TestModalBlocksHoverBelow: 鼠标移到遮罩下方时, 悬停反馈不该点亮被遮住的控件。
func TestModalBlocksHoverBelow(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	dlg, _ := mkDialog(true, 40, 20)
	mountChildren(root, btn, dlg)

	fake, a := mountTestApp(t, root, 200, 120)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if btn.hovered {
		t.Fatalf("被遮罩盖住的按钮不该进入悬停态")
	}
}

// ===== P2-4 toast =====

func TestToastLayoutAndLevelAccent(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	toast := &GuiNode{Tag: "toast", Props: map[string]object.Value{}}
	withStrProp(toast, "message", "Saved successfully")
	withStrProp(toast, "level", "success")
	mountChildren(root, toast)

	img := renderTree(root, 400, 200)

	// 贴右上角: 右边距 16, 上边距 16
	if toast.Box.X+toast.Box.W != 400-toastMargin || toast.Box.Y != toastMargin {
		t.Fatalf("toast 未贴右上角: %v", toast.Box)
	}
	if toast.Box.H != 36 {
		t.Fatalf("toast 高度 = %d, want 36", toast.Box.H)
	}
	if toast.Box.W <= toastAccentW+fieldPadX {
		t.Fatalf("toast 宽度异常: %v", toast.Box)
	}
	// 左侧 level 色条
	assertPx(t, img, toast.Box.X+1, toast.Box.Y+toast.Box.H/2, pxToastAccent, "success 色条")
	// 卡片白底 + 边框
	assertPx(t, img, toast.Box.X+toastAccentW+1, toast.Box.Y+toast.Box.H/2, pxPopupFace, "toast 白底")

	// level 映射
	cases := map[string]color.RGBA{
		"":        colorInfo,
		"info":    colorInfo,
		"success": colorAccent,
		"warn":    colorWarn,
		"warning": colorWarn,
		"error":   colorDanger,
	}
	for level, want := range cases {
		n := &GuiNode{Tag: "toast", Props: map[string]object.Value{}}
		if level != "" {
			withStrProp(n, "level", level)
		}
		if got := toastLevelColor(n); got != want {
			t.Errorf("level=%q → %v, want %v", level, got, want)
		}
	}
}

// TestToastIsNotModal: toast 是非模态的, 它下面的内容照常可点。
func TestToastIsNotModal(t *testing.T) {
	root := mkNode("column", nil)
	btn := withClick(mkNode("button", map[string]float64{"width": 80, "height": 30}))
	toast := &GuiNode{Tag: "toast", Props: map[string]object.Value{}}
	withStrProp(toast, "message", "hi")
	mountChildren(root, btn, toast)

	renderTree(root, 400, 200)
	if hit := HitTest(root, btn.Box.X+2, btn.Box.Y+2); hit != btn {
		t.Fatalf("toast 是非模态的, 不该拦住下面的按钮, got %v", hit)
	}
	if toast.isModal() {
		t.Fatalf("toast 不应被判定为模态")
	}
	// toast 自身区域没有处理器 → 命中回落到它下面的内容
	if hit := HitTest(root, toast.Box.X+2, toast.Box.Y+toast.Box.H/2); hit != btn && hit != nil {
		t.Fatalf("toast 区域没有处理器时不该命中它自己: %v", hit)
	}
}

// TestToastOpenFalseHides: toast 用 open prop 控制可见性时, 关闭态整支不绘制。
func TestToastOpenFalseHides(t *testing.T) {
	root := mkNode("column", nil)
	toast := &GuiNode{Tag: "toast", Props: map[string]object.Value{}}
	withStrProp(toast, "message", "hi")
	withBoolProp(toast, "open", false)
	mountChildren(root, toast)

	img := renderTree(root, 400, 200)
	if toast.Box != (Rect{}) {
		t.Fatalf("open=false 的 toast 盒子应为空: %v", toast.Box)
	}
	if got := countColor(img, Rect{0, 0, 400, 200}, pxPopupFace); got != 400*200 {
		t.Fatalf("open=false 的 toast 不该绘制, 非底色的像素 = %d", 400*200-got)
	}
}

// ===== P2-4 全链路 (假 Surface + 真 VM) =====

// runDemoSteps 读入演示脚本并挂载, 然后按 step 逐轮注入事件驱动事件循环。
// 每轮之间会真正重绘, 因此"先点开 → 再点遮罩"这类依赖新布局的序列才测得到
// (一次性把所有事件塞进去只会用同一份旧布局处理全部点击)。
func runDemoSteps(t *testing.T, name string, steps []func(root *GuiNode, fake *fakeSurface)) {
	t.Helper()
	// 演示脚本会注册定时器 (progress_demo 的 setInterval 等), 而调度器是
	// 进程级单例: 不清掉的话, 上一个测试遗留的定时器会在本测试的事件循环里
	// 触发, 报出莫名其妙的 "xxx is not defined"。
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

	src, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	appMu.Lock()
	root := activeApp.root
	appMu.Unlock()

	round := 0
	pump := func(maxWait time.Duration) bool {
		round++
		// 每轮先补一个"无害唤醒"事件: 只做断言的步骤自己不产生事件, 而事件泵
		// 在队列为空时会按 WaitEvents 的语义长时间阻塞 (假 Surface 在
		// maxWait<=0 时是 10 秒), 白等一场还会把以秒计的定时器提前耗掉。
		// EventMouseLeave 的语义就是"没有悬停、没有按压", 重复投递是幂等的。
		fake.push(Event{Kind: EventMouseLeave})
		if round-1 < len(steps) {
			steps[round-1](root, fake)
		} else {
			fake.push(Event{Kind: EventClose})
		}
		return Pump(maxWait)
	}
	if err := v.RunTimersWithPump(pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if Active() {
		t.Fatalf("关闭后应用未退出")
	}
}

// demoCardAt 返回 dialog 的内容卡片 (流内子节点)。
func demoCard(dlg *GuiNode) *GuiNode {
	for _, c := range dlg.Children {
		if c.isFlowChild() {
			return c
		}
	}
	return nil
}

func TestDialogDemoMaskBlocksCoveredButton(t *testing.T) {
	// 第 1 轮: 点 "Open dialog"; 第 2 轮: 点被遮住的按钮 (不该生效);
	// 第 3 轮: 断言计数没变 —— 且这次点击落在遮罩上, 所以对话同时被关闭。
	runDemoSteps(t, "dialog_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			dlg := findFirst(root, "dialog")
			if !dlg.overlayVisible() {
				t.Fatalf("点 Open dialog 后 dialog 应可见")
			}
			covered := findAll(root, "button")[2]
			card := demoCard(dlg)
			if card == nil || card.Box.Contains(covered.Box.X+2, covered.Box.Y+2) {
				t.Fatalf("用例前提不成立: 被遮住的按钮落在了卡片内")
			}
			fake.push(Event{Kind: EventMouseUp, X: covered.Box.X + 2, Y: covered.Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			if got := textStartingWith(root, "covered clicks = "); got != "covered clicks = 0" {
				t.Fatalf("遮罩下方的按钮被点到了: %q", got)
			}
			if !textContainsAny(root, "closed by mask") {
				t.Fatalf("点遮罩应触发 onClose")
			}
		},
	})
}

func TestDialogDemoCardBodyDoesNotClose(t *testing.T) {
	// 点卡片空白处 (padding 带) 既不该关闭对话, 也不该穿透到下面的按钮
	runDemoSteps(t, "dialog_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			dlg := findFirst(root, "dialog")
			card := demoCard(dlg)
			if card == nil {
				t.Fatalf("dialog 缺少内容卡片")
			}
			// 卡片左上角 padding 带: 在卡片内, 但不在任何子节点上
			fake.push(Event{Kind: EventMouseUp, X: card.Box.X + 2, Y: card.Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			if !findFirst(root, "dialog").overlayVisible() {
				t.Fatalf("点卡片自身不该关闭对话")
			}
			if textContainsAny(root, "closed by") {
				t.Fatalf("点卡片自身不该触发 onClose")
			}
			if got := textStartingWith(root, "covered clicks = "); got != "covered clicks = 0" {
				t.Fatalf("点卡片不该穿透到下面的按钮: %q", got)
			}
		},
	})
}

func TestDialogDemoCloseButton(t *testing.T) {
	runDemoSteps(t, "dialog_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			closeBtn := findAll(root, "button")[3] // 卡片里的 Close
			fake.push(Event{Kind: EventMouseUp, X: closeBtn.Box.X + 2, Y: closeBtn.Box.Y + closeBtn.Box.H/2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			if findFirst(root, "dialog").overlayVisible() {
				t.Fatalf("点 Close 应关闭对话")
			}
			if !textContainsAny(root, "closed by button") {
				t.Fatalf("卡片里的 Close 按钮未触发 onClose")
			}
		},
	})
}

func TestDialogDemoEscClosesTopDialog(t *testing.T) {
	runDemoSteps(t, "dialog_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			fake.push(Event{Kind: EventMouseUp, X: btns[0].Box.X + 2, Y: btns[0].Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			if !findFirst(root, "dialog").overlayVisible() {
				t.Fatalf("前置条件: dialog 应已打开")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Escape"})
		},
		func(root *GuiNode, fake *fakeSurface) {
			if findFirst(root, "dialog").overlayVisible() {
				t.Fatalf("Esc 应关掉最上层的 dialog")
			}
			if !textContainsAny(root, "closed by mask") {
				t.Fatalf("Esc 未触发 dialog 的 onClose")
			}
		},
	})
}

func TestSelectDemoPickOptionUpdatesMirror(t *testing.T) {
	runDemoSteps(t, "select_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			// 点第一个下拉 → 展开
			sel := findAll(root, "select")[0]
			fake.push(Event{Kind: EventMouseUp, X: sel.Box.X + 6, Y: sel.Box.Y + selectRowH/2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			sel := findAll(root, "select")[0]
			if !sel.expanded {
				t.Fatalf("点击字段应展开下拉")
			}
			rows := selectRows(sel)
			if len(rows) != 4 {
				t.Fatalf("城市下拉应有 4 项, got %d", len(rows))
			}
			// 选第三项 (Shenzhen)
			fake.push(Event{Kind: EventMouseUp, X: rows[2].Box.X + 4, Y: rows[2].Box.Y + selectRowH/2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			sel := findAll(root, "select")[0]
			if sel.expanded {
				t.Fatalf("选中后应收起下拉")
			}
			if got := sel.selectValue(); got != "sz" {
				t.Fatalf("受控值未随 onChange 更新: %q", got)
			}
			if got := textStartingWith(root, "city = "); !strings.Contains(got, "city = sz") {
				t.Fatalf("镜像文本未更新: %q", got)
			}
		},
	})
}

func TestSelectDemoOutsideClickClosesWithoutSideEffect(t *testing.T) {
	// 点开着的字段 → 先被"点外部"逻辑收起, 那次点击被吞掉, 不会连锁再展开;
	// 同时已选值不受影响。走真 VM, 因此这里验的是完整链路而不是内部状态。
	runDemoSteps(t, "select_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			sel := findAll(root, "select")[0]
			fake.push(Event{Kind: EventMouseUp, X: sel.Box.X + 6, Y: sel.Box.Y + selectRowH/2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			sel := findAll(root, "select")[0]
			if !sel.expanded {
				t.Fatalf("前置条件: 第一个下拉应已展开")
			}
			x, y := sel.Box.X+6, sel.Box.Y+selectRowH/2
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
			fake.push(Event{Kind: EventMouseUp, X: x, Y: y})
		},
		func(root *GuiNode, fake *fakeSurface) {
			sels := findAll(root, "select")
			if sels[0].expanded {
				t.Fatalf("点展开中的字段应收起下拉")
			}
			if got := sels[0].selectValue(); got != "sh" {
				t.Fatalf("收起下拉不该改变已选值: %q", got)
			}
			if got := textStartingWith(root, "city = "); !strings.Contains(got, "city = sh") {
				t.Fatalf("镜像文本被意外改动: %q", got)
			}
		},
	})
}

func TestToastDemoMountsAtTopRightAndStaysNonModal(t *testing.T) {
	// 点 "Show toast" → 条件渲染挂上 toast 节点; 走真 VM 因此这条链路是
	// "JS 改 signal → effect 挂子树 → 布局 → 上屏"的完整验证。
	//
	// 不在这里断言"3 秒后自动消失": 那是脚本 side 的 setTimeout 语义 (内核
	// 对它无感知), 定时器机制本身由 vm 包的定时器用例覆盖, 在这儿等墙钟
	// 只会让测试变慢且不稳。
	runDemoSteps(t, "dialog_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			btns := findAll(root, "button")
			fake.push(Event{Kind: EventMouseUp, X: btns[1].Box.X + 2, Y: btns[1].Box.Y + 2})
		},
		func(root *GuiNode, fake *fakeSurface) {
			toast := findFirst(root, "toast")
			if toast == nil {
				t.Fatalf("点击后应挂载 toast")
			}
			// 窗口尺寸取假 Surface 的 (它不理会 window({width}) 配置)
			w, _ := fake.Size()
			if toast.Box.X+toast.Box.W != w-toastMargin || toast.Box.Y != toastMargin {
				t.Fatalf("toast 未贴右上角: %v (窗口宽 %d)", toast.Box, w)
			}
			img := shotsImage(fake)
			assertPx(t, img, toast.Box.X+1, toast.Box.Y+toast.Box.H/2, pxToastAccent, "toast 色条")
			// 非模态: 它下面的按钮照常可命中
			covered := findAll(root, "button")[2]
			if hit := HitTest(root, covered.Box.X+2, covered.Box.Y+2); hit != covered {
				t.Fatalf("toast 是非模态的, 不该拦住下面的按钮, got %v", hit)
			}
		},
	})
}
