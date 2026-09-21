package gfx

import (
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== P0: 内置组件的固有尺寸 / 像素输出 / 事件链路 =====
//
// 断言策略与 font_test.go 一致: 纯 Go 层直接 Layout + Draw 到 image.RGBA 后
// 逐像素比对; 涉及回调的用假 Surface 注入事件 + 真 VM 跑事件循环。
// (共享 helper 见 helpers_test.go。)

func TestWidgetIntrinsicSize(t *testing.T) {
	root := mkNode("column", nil)
	cb, rd, sw, pr, sp := mkNode("checkbox", nil), mkNode("radio", nil),
		mkNode("switch", nil), mkNode("progress", nil), mkNode("spacer", nil)
	root.Children = []*GuiNode{cb, rd, sw, pr, sp}
	Layout(root, 400, 300)

	if cb.Box.W != 18 || cb.Box.H != 18 {
		t.Fatalf("checkbox 固有尺寸 = %v, want 18x18", cb.Box)
	}
	if rd.Box.W != 18 || rd.Box.H != 18 {
		t.Fatalf("radio 固有尺寸 = %v, want 18x18", rd.Box)
	}
	if sw.Box.W != 36 || sw.Box.H != 20 {
		t.Fatalf("switch 固有尺寸 = %v, want 36x20", sw.Box)
	}
	if pr.Box.W != 200 || pr.Box.H != 8 {
		t.Fatalf("progress 固有尺寸 = %v, want 200x8", pr.Box)
	}
	// spacer: 主轴(纵向)不占位, 交叉轴由 stretch 撑满
	if sp.Box.H != 0 || sp.Box.W != 400 {
		t.Fatalf("spacer 固有尺寸 = %v, want 高 0 宽 400 (stretch)", sp.Box)
	}

	// 显式尺寸优先
	root2 := mkNode("column", nil)
	pr2 := mkNode("progress", map[string]float64{"width": 100, "height": 4})
	root2.Children = []*GuiNode{pr2}
	Layout(root2, 400, 300)
	if pr2.Box.W != 100 || pr2.Box.H != 4 {
		t.Fatalf("progress 显式尺寸未被尊重: %v", pr2.Box)
	}
}

func TestSeparatorBothDirections(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 4})
	hsep := mkNode("separator", nil)
	vsep := withBool(mkNode("separator", map[string]float64{"height": 20}), "vertical", true)
	root.Children = []*GuiNode{hsep, vsep}
	img := renderTree(root, 400, 300)

	// 横线: 高 1, 宽占满内容区 (392)
	if hsep.Box.W != 392 || hsep.Box.H != 1 {
		t.Fatalf("横向 separator = %v, want 宽 392 高 1", hsep.Box)
	}
	assertPx(t, img, 200, 4, pxTrack, "分隔线颜色")
	// 纵线: 宽 1, 显式 height=20
	if vsep.Box.W != 1 || vsep.Box.H != 20 {
		t.Fatalf("纵向 separator = %v, want 宽 1 高 20", vsep.Box)
	}
	assertPx(t, img, 4, vsep.Box.Y+10, pxTrack, "纵向分隔线颜色")
}

func TestSpacerDrawsNothingAndGrows(t *testing.T) {
	// row: [rect 50][spacer flexGrow 1][rect 50] → 右侧 rect 被推到 x=350
	root := mkNode("row", nil)
	left := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	sp := mkNode("spacer", map[string]float64{"flexGrow": 1, "width": 40, "height": 10})
	right := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{left, sp, right}
	Layout(root, 400, 300)

	if right.Box.X != 350 {
		t.Fatalf("spacer 未吃掉主轴富余空间: right.X = %d, want 350", right.Box.X)
	}

	// 即便显式给了尺寸/背景, spacer 也不产生任何像素
	root2 := mkNode("row", nil)
	sp2 := mkNode("spacer", map[string]float64{"width": 40, "height": 10})
	withStr(sp2, "background", "#000000")
	root2.Children = []*GuiNode{sp2}
	img := renderTree(root2, 100, 60)
	if got := countColor(img, Rect{0, 0, 100, 60}, color.RGBA{A: 255}); got != 0 {
		t.Fatalf("spacer 不应绘制任何像素, 却发现 %d 个黑像素", got)
	}
	if got := countColor(img, Rect{0, 0, 100, 60}, pxWhite); got != 100*60 {
		t.Fatalf("spacer 区域应保持底色, 白像素 = %d", got)
	}
}

func TestProgressFillWidths(t *testing.T) {
	cases := []struct {
		value  float64
		filled int // 期望填充宽度 (200px 宽)
	}{
		{0, 0}, {0.5, 100}, {1, 200}, {2, 200}, {-1, 0}, {0.25, 50},
	}
	for _, tc := range cases {
		root := mkNode("column", nil)
		pr := mkNode("progress", map[string]float64{"value": tc.value})
		root.Children = []*GuiNode{pr}
		img := renderTree(root, 200, 8)

		if pr.Box.W != 200 || pr.Box.H != 8 {
			t.Fatalf("value=%v: progress 尺寸 = %v", tc.value, pr.Box)
		}
		if tc.filled > 0 {
			assertPx(t, img, tc.filled-1, 4, pxAccent, "填充末端")
		}
		if tc.filled < 200 {
			assertPx(t, img, tc.filled, 4, pxTrack, "填充之后的轨道")
		}
	}

	// background 覆盖前景色
	root := mkNode("column", nil)
	pr := mkNode("progress", map[string]float64{"value": 1, "width": 10, "height": 4})
	withStr(pr, "background", "#c0392b")
	root.Children = []*GuiNode{pr}
	img := renderTree(root, 10, 4)
	assertPx(t, img, 5, 2, pxRed, "progress 前景色可覆盖")
}

func TestCheckboxStates(t *testing.T) {
	root := mkNode("column", nil)
	unchecked := withBool(mkNode("checkbox", nil), "checked", false)
	checked := withBool(mkNode("checkbox", nil), "checked", true)
	root.Children = []*GuiNode{unchecked, checked}
	img := renderTree(root, 40, 40)

	// 未选中: 1px 边框空盒
	assertPx(t, img, 0, 0, pxFieldEdge, "checkbox 左上边框")
	assertPx(t, img, 17, 17, pxFieldEdge, "checkbox 右下边框")
	assertPx(t, img, 14, 14, pxWhite, "未选中盒内应为空")

	// 选中: 强调色填充 + 白勾 + 保留边框
	b := checked.Box
	assertPx(t, img, b.X+14, b.Y+14, pxAccent, "选中填充色")
	assertPx(t, img, b.X, b.Y, pxFieldEdge, "选中仍保留边框")
	if n := countColor(img, Rect{b.X + 1, b.Y + 1, b.W - 2, b.H - 2}, pxWhite); n < 8 {
		t.Fatalf("选中态未画出勾 (盒内白像素 = %d)", n)
	}
}

func TestRadioStates(t *testing.T) {
	root := mkNode("column", nil)
	off := withBool(mkNode("radio", nil), "checked", false)
	on := withBool(mkNode("radio", nil), "checked", true)
	root.Children = []*GuiNode{off, on}
	img := renderTree(root, 40, 60)

	// 圆环: 顶部/左右在环上, 四角在圆外, 中心空心
	assertPx(t, img, 9, 1, pxFieldEdge, "radio 圆环顶部")
	assertPx(t, img, 1, 9, pxFieldEdge, "radio 圆环左侧")
	assertPx(t, img, 0, 0, pxWhite, "radio 圆角外应为空")
	assertPx(t, img, 9, 9, pxWhite, "未选中圆内应为空")

	// 选中: 中心实心圆点
	b := on.Box
	assertPx(t, img, b.X+9, b.Y+9, pxAccent, "radio 选中圆点")
	assertPx(t, img, b.X+9, b.Y+1, pxFieldEdge, "选中仍保留圆环")
}

func TestSwitchStates(t *testing.T) {
	root := mkNode("column", nil)
	off := withBool(mkNode("switch", nil), "checked", false)
	on := withBool(mkNode("switch", nil), "checked", true)
	root.Children = []*GuiNode{off, on}
	img := renderTree(root, 60, 60)

	// 关闭: 左滑块(白) + 右轨道(浅灰)
	assertPx(t, img, 10, 10, pxWhite, "switch 关闭态滑块在左")
	assertPx(t, img, 30, 10, pxSwitchOff, "switch 关闭态轨道色")
	// 打开: 左轨道(强调色) + 右滑块(白)
	b := on.Box
	assertPx(t, img, b.X+10, b.Y+10, pxAccent, "switch 打开态轨道色")
	assertPx(t, img, b.X+30, b.Y+10, pxWhite, "switch 打开态滑块在右")
}

func TestFillCircleAndStrokeCircle(t *testing.T) {
	red := color.RGBA{R: 255, A: 255}

	img := image.NewRGBA(image.Rect(0, 0, 20, 20))
	FillRect(img, Rect{0, 0, 20, 20}, pxWhite)
	FillCircle(img, 10, 10, 8, red)
	assertPx(t, img, 10, 10, red, "实心圆圆心")
	assertPx(t, img, 10, 2, red, "实心圆上端点")
	assertPx(t, img, 1, 1, pxWhite, "实心圆四角之外")

	img2 := image.NewRGBA(image.Rect(0, 0, 20, 20))
	FillRect(img2, Rect{0, 0, 20, 20}, pxWhite)
	StrokeCircle(img2, 10, 10, 8, red)
	assertPx(t, img2, 10, 2, red, "圆环顶部")
	assertPx(t, img2, 2, 10, red, "圆环左端")
	assertPx(t, img2, 18, 10, red, "圆环右端")
	assertPx(t, img2, 10, 10, pxWhite, "圆环内部应为空")
}

// 已修回归: button 此前无固有尺寸 (0 高), 缺省外观不可见且命不中。
func TestButtonIntrinsicAndDefaultStyle(t *testing.T) {
	root := mkNode("column", map[string]float64{"gap": 8, "padding": 10})
	plain := mkNode("button", nil)
	plain.Children = []*GuiNode{{Tag: "#text", Text: "OK", Props: map[string]object.Value{}}}
	root.Children = []*GuiNode{plain}
	img := renderTree(root, 200, 100)

	tw, th := MeasureText("OK", 16)
	if plain.Box.W != tw+2*buttonPadX || plain.Box.H != th+2*buttonPadY {
		t.Fatalf("button 内容尺寸 = %v, want %dx%d", plain.Box, tw+2*buttonPadX, th+2*buttonPadY)
	}
	if plain.Box.H <= 0 {
		t.Fatalf("button 高度不应为 0: %v", plain.Box)
	}
	// 缺省浅灰底 + 1px 深灰边框
	assertPx(t, img, plain.Box.X+3, plain.Box.Y+plain.Box.H/2, pxBtnFace, "button 缺省底色")
	assertPx(t, img, plain.Box.X, plain.Box.Y, pxBtnEdge, "button 缺省边框")
	// 文本子节点垂直居中 (水平方向 v1 左对齐 + 8px 内边距)
	label := plain.Children[0]
	if label.Box.X != plain.Box.X+buttonPadX {
		t.Fatalf("文本子节点未按 8px 内边距左对齐: %v", label.Box)
	}
	wantY := plain.Box.Y + (plain.Box.H-th)/2
	if label.Box.Y != wantY {
		t.Fatalf("文本子节点未垂直居中: Y=%d want %d", label.Box.Y, wantY)
	}
}

func TestButtonDisabledDimsColors(t *testing.T) {
	root := mkNode("column", nil)
	on := mkNode("button", nil)
	off := withBool(mkNode("button", nil), "disabled", true)
	root.Children = []*GuiNode{on, off}
	img := renderTree(root, 200, 120)

	assertPx(t, img, on.Box.X+3, on.Box.Y+on.Box.H/2, pxBtnFace, "可用 button 底色")
	assertPx(t, img, off.Box.X+3, off.Box.Y+off.Box.H/2, pxBtnFaceOff, "禁用 button 应降饱和")
}

// TestButtonClickAndDisabled 用假 Surface 走真实事件链路:
// 点击落在按钮的文字子节点上 (需要命中测试回溯到按钮), 禁用按钮被拦截。
func TestButtonClickAndDisabled(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		let clicks = 0;
		const ui = h("column", {gap: 8, padding: 16},
			h("button", {onClick: () => { clicks = clicks + 1; }}, "加一"),
			h("button", {disabled: true, onClick: () => { clicks = clicks + 100; }}, "禁用"));
		render(ui, {title: "T", width: 400, height: 300});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	uiVal, ok := v.Globals().Get("ui")
	if !ok {
		t.Fatalf("global ui missing")
	}
	ui := uiVal.(*GuiNode)
	enabled, disabled := ui.Children[0], ui.Children[1]
	if enabled.Box.H <= 0 || disabled.Box.H <= 0 {
		t.Fatalf("button 应有内容高度: %v / %v", enabled.Box, disabled.Box)
	}

	pushOnLabel := func(btn *GuiNode) {
		label := btn.Children[0]
		fake.push(Event{Kind: EventMouseUp, X: label.Box.X + 1, Y: label.Box.Y + 1})
	}
	pushOnLabel(enabled)
	pushOnLabel(disabled)
	fake.push(Event{Kind: EventClose})

	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	cv, _ := v.Globals().Get("clicks")
	num, _ := cv.(*object.Number)
	if num == nil || num.Value != 1 {
		t.Fatalf("clicks = %v, want 1 (点在文字上应命中按钮, 禁用按钮应被拦截)", cv)
	}
}

func TestUnknownTagWarnsOnce(t *testing.T) {
	warnMu.Lock()
	warnSeenTags = map[string]struct{}{}
	warnMu.Unlock()

	var warned []string
	old := warnUnknownTag
	warnUnknownTag = func(tag string) { warned = append(warned, tag) }
	defer func() {
		warnUnknownTag = old
		warnMu.Lock()
		warnSeenTags = map[string]struct{}{}
		warnMu.Unlock()
	}()

	nodeVal := JSBuiltinH(object.NewString("frobnicator"), nil)
	JSBuiltinH(object.NewString("frobnicator"), nil) // 第二次不重复告警
	JSBuiltinH(object.NewString("checkbox"), nil)    // 已知标签不告警

	if len(warned) != 1 || warned[0] != "frobnicator" {
		t.Fatalf("warned = %v, want 仅 frobnicator 一次", warned)
	}

	// 渲染行为不变: 未知标签仍走通用盒子分支 (可填充背景)
	unknown, ok := nodeVal.(*GuiNode)
	if !ok || unknown.Tag != "frobnicator" {
		t.Fatalf("未知标签节点 = %v", nodeVal)
	}
	unknown.Props["background"] = object.NewString("#c0392b")
	unknown.Props["width"] = object.NewNumber(10)
	unknown.Props["height"] = object.NewNumber(10)
	root := mkNode("column", nil)
	root.Children = []*GuiNode{unknown}
	img := renderTree(root, 40, 40)
	assertPx(t, img, 5, 5, pxRed, "未知标签仍渲染为通用盒子")
}

// TestNestedContainerSizesAndDraws 回归: 容器的固有尺寸此前只取显式
// width/height → 嵌套 column/row 恒为 0 尺寸, 而 drawNode 会跳过"自身盒为空"
// 的子树, 导致嵌套几层的界面整片不渲染。
func TestNestedContainerSizesAndDraws(t *testing.T) {
	outer := mkNode("column", map[string]float64{"gap": 6, "padding": 8})
	inner := mkNode("row", map[string]float64{"gap": 4})
	left := withStr(mkNode("rect", map[string]float64{"width": 30, "height": 12}), "background", "#c0392b")
	right := withStr(mkNode("rect", map[string]float64{"width": 20, "height": 18}), "background", "#27ae60")
	inner.Children = []*GuiNode{left, right}
	outer.Children = []*GuiNode{inner}
	img := renderTree(outer, 100, 60)

	if inner.Box.H != 18 {
		t.Fatalf("嵌套 row 高度 = %d, want 18 (内容尺寸: 子节点最大高)", inner.Box.H)
	}
	if inner.Box.W != 84 {
		t.Fatalf("嵌套 row 宽度 = %d, want 84 (stretch 撑满内容区)", inner.Box.W)
	}
	if left.Box.X != 8 || left.Box.Y != 8 {
		t.Fatalf("左块位置 = %v, want (8,8)", left.Box)
	}
	if right.Box.X != 42 {
		t.Fatalf("右块位置 = %v, want x=42 (30+4)", right.Box)
	}
	// 嵌套层级内的像素真的被绘制出来了
	assertPx(t, img, 10, 10, pxRed, "嵌套容器内的左块")
	assertPx(t, img, 44, 10, pxAccent, "嵌套容器内的右块")

	// 零尺寸盒子的子树同样不能丢: <rect> 自身没给尺寸, 但子节点有
	zeroParent := mkNode("rect", nil)
	child := withStr(mkNode("rect", map[string]float64{"width": 6, "height": 6}), "background", "#c0392b")
	zeroParent.Children = []*GuiNode{child}
	root := mkNode("column", nil)
	root.Children = []*GuiNode{zeroParent}
	img2 := renderTree(root, 40, 40)
	assertPx(t, img2, child.Box.X+2, child.Box.Y+2, pxRed, "零尺寸父盒内的子节点")
}

// TestCounterDemoClick 端到端跑 README 的计数器示例: 点击按钮 (落在文字上)
// → onClick 计数 +1 → 响应式文本更新。此前 button 无高度且命中不回溯,
// 这个示例是点不动的。
func TestCounterDemoClick(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "testdata", "counter_demo.js"))
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

	label := findFirst(root, "#text")
	if label == nil || label.Text != "count: 0" {
		t.Fatalf("初始文本 = %v, want count: 0", label)
	}
	btn := findFirst(root, "button")
	fake.push(Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + btn.Box.H/2})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if label.Text != "count: 1" {
		t.Fatalf("点击后文本 = %q, want \"count: 1\"", label.Text)
	}
}

// TestGuiDemoClickUpdatesBar 端到端跑 gui_demo.js: 点击绿块 → count=1 →
// 红条宽度变为 20px (走"命中按钮 → 改 signal → effect 写回 width → 重绘")。
func TestGuiDemoClickUpdatesBar(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("..", "testdata", "gui_demo.js"))
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

	bar := root.Children[0] // 红条
	if bar.Box.W != 0 {
		t.Fatalf("初始红条宽度 = %d, want 0", bar.Box.W)
	}
	green := root.Children[1] // 绿块 (可点)
	fake.push(Event{Kind: EventMouseUp, X: green.Box.X + 10, Y: green.Box.Y + 10})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}
	if bar.Box.W != 20 {
		t.Fatalf("点击后红条宽度 = %d, want 20", bar.Box.W)
	}
	img := shotsImage(fake)
	if img == nil {
		t.Fatalf("未捕获上屏帧")
	}
	assertPx(t, img, bar.Box.X+19, bar.Box.Y+5, pxRed, "红条末端像素")
}

// TestExampleScriptsMount 用假 Surface 把演示脚本跑一遍: 它们是组件的真实
// JSX 用法, 这里验证能编译、能挂载、能干净关闭, 并对挂载后的元素树与上屏帧
// 做几何/像素抽查 (窗口观感仍需人工验收)。含 P3 起的既有演示, 防回归。
func TestExampleScriptsMount(t *testing.T) {
	scripts := []string{
		"form_demo.js", "progress_demo.js", "button_demo.js", // P0 新增
		"events_demo.js", "focus_demo.js", "hover_demo.js", // P1 事件/焦点/悬停
		"tabs_demo.js", "list_demo.js", // P1 条件渲染 / 列表渲染
		"select_demo.js", "dialog_demo.js", // P2-3 下拉框 / P2-4 弹层
		"input_demo.js",                  // P2-1 单行输入
		"canvas_demo.js",                 // P3-1 自绘画布
		"slider_demo.js",                 // P2-8 滑块
		"ime_demo.js",                    // P2-7 输入法 (提交由平台投递, 这里只验挂载)
		"clipboard_demo.js",              // P3-3 剪贴板 (读写由后端提供, 这里只验挂载)
		"transition_demo.js",             // P3-2 过渡动画 (首帧应静止: 见断言)
		"dialog_native_demo.js",          // P3-4 原生对话框 (测试注入假后端应答)
		"menu_demo.js",                   // P3-5 菜单栏 / 右键菜单 / 快捷键
		"resize_demo.js",                 // 屏幕 A: onResize + useWindowSize 模式
		"routing_demo.js",                // 路由 A: signal 切页 (交互全流程见 routing_test.go)
		"resource_demo.js",               // 状态 B: createResource (交互断言见 resource_test.go)
		"dev_panel_demo.js",              // devtools A: gx/dev 快照面板 (结构见 dev_test.go)
		"kit_demo.js",                    // 样式 F: 用户态设计套件 (令牌/变体/主题)
		"view_demo.js",                   // gx/view: 声明式循环与条件 (交互断言见 view_test.go)
		"elastic_layout_demo.js",         // 布局弹性词汇: 百分比 / min-max / flexShrink / wrap
		"grid_demo.js",                   // 网格布局: columns={n} 等宽卡片栅格
		"counter_demo.js", "gui_demo.js", // 既有演示 (布局改动后回归)
		// 注: image_demo.js 不在本列表 —— 它的 src 是相对文件路径, 必须从仓库根
		// 目录运行 (而本用例的 cwd 是 gfx/)。由 TestImageDemoScript 专职覆盖。
		// storage_demo.js 同理: 它会真实写存储文件, 需 GOX_STORAGE_DIR 隔离,
		// 由 TestStorageDemoScript 专职覆盖。
		// router_demo.js / router_window_demo.js 同理: 前者含懒加载
		// import("./router_page_detail.js") —— 相对脚本所在目录解析, 需要把
		// 模块基准路径设成 testdata/; 后者开两个窗口。分别由
		// TestRouterDemoScript / TestRouterWindowDemoScript 专职覆盖 (它们
		// 额外断言交互与状态保留, 比"挂载不报错"强得多)。
	}
	for _, name := range scripts {
		t.Run(name, func(t *testing.T) {
			// 定时器调度器是进程级单例: 演示脚本注册的 setInterval/setTimeout
			// 若不清掉会泄漏到后续测试的事件循环里, 在别的 VM 上跑出
			// "xxx is not defined" 这种跟本用例毫无关系的报错。
			object.GlobalScheduler().ClearAll()
			t.Cleanup(func() { object.GlobalScheduler().ClearAll() })

			src, err := os.ReadFile(filepath.Join("..", "testdata", name))
			if err != nil {
				t.Fatalf("读取脚本: %v", err)
			}
			fake := newFakeSurface()
			// 演示里可能有原生对话框调用 (P3-4): 没人点按钮的自动化环境里,
			// 真对话框会挂到超时。给个自动应答的假后端 —— 顺带证明
			// "可选接口可替换"这条设计确实能落到测试上。
			fake.dialog = &fakeDialogHost{confirmAnswer: true, openPath: "/tmp/picked.txt", openOk: true}
			SetDefaultFactory(&fakeFactory{fake})
			defer SetDefaultFactory(nil)

			v, err := vm.EvalVM(string(src))
			if err != nil {
				t.Fatalf("执行脚本: %v", err)
			}
			if !Active() {
				t.Fatalf("脚本未挂载窗口 (render 未生效)")
			}
			if shots(fake) < 1 {
				t.Fatalf("首帧未上屏")
			}
			appMu.Lock()
			root := activeApp.root
			appMu.Unlock()
			checkDemoTree(t, name, root, fake)

			fake.push(Event{Kind: EventClose})
			if err := v.RunTimersWithPump(Pump); err != nil {
				t.Fatalf("事件循环: %v", err)
			}
			if Active() {
				t.Fatalf("关闭后应用未退出")
			}
		})
	}
}

// checkDemoTree 抽查演示脚本挂载后的元素树与上屏帧。
func checkDemoTree(t *testing.T, name string, root *GuiNode, fake *fakeSurface) {
	t.Helper()
	switch name {
	case "resource_demo.js":
		// 取数定时器 (800ms) 在关闭前不会到: 首帧就是 pending 态
		tx := findFirstWhere(root, func(n *GuiNode) bool { return n.Tag == "text" && n.TextContent() == "loading..." })
		if tx == nil {
			t.Fatalf("pending 态应显示 loading...")
		}
	case "dev_panel_demo.js":
		if n := countTag(root, "text"); n < 5 {
			t.Fatalf("面板文本行数量 = %d, want >= 5", n)
		}
		if n := countTag(root, "button"); n != 1 {
			t.Fatalf("bump 按钮数量 = %d, want 1", n)
		}
	case "kit_demo.js":
		if n := countTag(root, "button"); n != 5 {
			t.Fatalf("按钮数量 = %d, want 5 (主题切换+primary+normal+small+disabled)", n)
		}
		if n := countTag(root, "input"); n != 1 {
			t.Fatalf("Field 输入框数量 = %d, want 1", n)
		}
	case "view_demo.js":
		// 首帧: keyed + stable 列表 3 行 (RowCard 用 note 标了行 id),
		// Switch 命中 loading 分支, 空列表 fallback 不该出现。
		notes := map[string]int{}
		for _, r := range findAll(root, "row") {
			if s, ok := r.PropStr("note"); ok {
				notes[s]++
			}
		}
		for _, id := range []string{"a", "b", "c"} {
			if notes[id] != 1 {
				t.Fatalf("列表行 %q 数量 = %d, want 1 (首帧三行: %v)", id, notes[id], notes)
			}
		}
		if notes["phase-loading"] != 1 {
			t.Fatalf("Switch 首帧应命中 loading 分支: %v", notes)
		}
		if notes["list-empty"] != 0 || notes["panel-off"] != 0 {
			t.Fatalf("首帧不该出现 fallback 分支: %v", notes)
		}
		if n := countTag(root, "input"); n != 4 {
			t.Fatalf("输入框数量 = %d, want 4 (三行行内各一个 + 面板一个)", n)
		}
	case "grid_demo.js":
		if n := countTag(root, "grid"); n != 1 {
			t.Fatalf("grid 数量 = %d, want 1", n)
		}
		var cards []*GuiNode
		for _, c := range findAll(root, "column") {
			if _, ok := c.PropNum("radius"); ok {
				cards = append(cards, c)
			}
		}
		if len(cards) != 6 {
			t.Fatalf("卡片数量 = %d, want 6", len(cards))
		}
		// 卡片是容器 → 拉伸到格: 同行等宽等高, 列距 = 列宽 + gap
		if cards[0].Box.H != cards[1].Box.H || cards[0].Box.W != cards[1].Box.W {
			t.Fatalf("同行卡片应等宽等高: %v vs %v", cards[0].Box, cards[1].Box)
		}
		if d := cards[1].Box.X - cards[0].Box.X; d != cards[0].Box.W+10 {
			t.Fatalf("列距应 = 列宽+gap(10): got %d", d)
		}
	case "elastic_layout_demo.js":
		// 三张 30% 卡片: 首卡宽度 = (520-2*14 padding-2*10 gap)*30% ≈ 143
		cards := findAll(root, "column")
		var pctCards []*GuiNode
		for _, c := range cards {
			if w, _ := c.PropStr("width"); w == "30%" {
				pctCards = append(pctCards, c)
			}
		}
		if len(pctCards) != 3 {
			t.Fatalf("30%% 卡片数量 = %d, want 3", len(pctCards))
		}
		if pctCards[0].Box.W <= 100 || pctCards[0].Box.W >= 160 {
			t.Fatalf("首卡百分比宽度 = %d, want ~143 ((520-28-20)*0.3)", pctCards[0].Box.W)
		}
		// grow+maxWidth 条: 520-28=492 > 360 → 钳在 360
		bars := findAll(root, "rect")
		clamped := false
		for _, b := range bars {
			if _, ok := b.PropNum("maxWidth"); ok && b.Box.W == 360 {
				clamped = true
			}
		}
		if !clamped {
			t.Fatalf("应有被 maxWidth=360 钳位的 grow 条")
		}
	case "form_demo.js":
		cb := findFirst(root, "checkbox")
		if cb == nil || cb.Box.W != 18 || cb.Box.H != 18 {
			t.Fatalf("checkbox 未按 18x18 布局: %v", cb)
		}
		if n := countTag(root, "radio"); n != 3 {
			t.Fatalf("radio 数量 = %d, want 3", n)
		}
		sw := findFirst(root, "switch")
		if sw == nil || sw.Box.W != 36 || sw.Box.H != 20 {
			t.Fatalf("switch 未按 36x20 布局: %v", sw)
		}
		// 脚本里 notify 初值为 true → 开关打开: 左轨道强调色、右滑块白色
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		assertPx(t, img, sw.Box.X+10, sw.Box.Y+10, pxAccent, "form_demo 开关打开态")
		assertPx(t, img, sw.Box.X+30, sw.Box.Y+10, pxWhite, "form_demo 滑块在右")
	case "progress_demo.js":
		pr := findFirst(root, "progress")
		if pr == nil || pr.Box.W != 200 || pr.Box.H != 8 {
			t.Fatalf("progress 未按 200x8 布局: %v", pr)
		}
		if n := countTag(root, "separator"); n != 2 {
			t.Fatalf("separator 数量 = %d, want 2", n)
		}
		sp := findFirst(root, "spacer")
		if sp == nil || sp.Box.W <= 80 {
			t.Fatalf("spacer 未撑开富余空间: %v", sp)
		}
	case "button_demo.js":
		if n := countTag(root, "button"); n != 3 {
			t.Fatalf("button 数量 = %d, want 3", n)
		}
		var buttons []*GuiNode
		var walk func(n *GuiNode)
		walk = func(n *GuiNode) {
			if n.Tag == "button" {
				buttons = append(buttons, n)
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(root)
		for i, b := range buttons {
			if b.Box.H <= 0 {
				t.Fatalf("第 %d 个 button 无内容高度: %v", i, b.Box)
			}
		}
		// 第三个按钮 disabled → 底色降饱和
		dis := buttons[2]
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		assertPx(t, img, dis.Box.X+3, dis.Box.Y+dis.Box.H/2, pxBtnFaceOff, "button_demo 禁用按钮变灰")
	case "canvas_demo.js":
		// 自绘画布: 柱状图 (246x110) + 原语展示 (238x72, 显式 background/border)
		cvs := findAll(root, "canvas")
		if len(cvs) != 2 {
			t.Fatalf("canvas_demo 的 canvas 数量 = %d, want 2", len(cvs))
		}
		chart, prim := cvs[0], cvs[1]
		if chart.Box.W != 246 || chart.Box.H != 110 {
			t.Fatalf("柱状图画布尺寸 = %v, want 246x110", chart.Box)
		}
		if prim.Box.W != 238 || prim.Box.H != 72 {
			t.Fatalf("原语画布尺寸 = %v, want 238x72", prim.Box)
		}
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		// 首帧 tick=0 → 第 0 根柱子是强调色, 其余是灰蓝
		if n := countColor(img, chart.Box, pxRed); n == 0 {
			t.Fatalf("柱状图缺少高亮柱 #c0392b")
		}
		if gray, ok := ParseColor("#7f8c8d"); ok {
			if n := countColor(img, chart.Box, gray); n == 0 {
				t.Fatalf("柱状图缺少普通柱 #7f8c8d")
			}
		} else {
			t.Fatalf("演示色 #7f8c8d 解析失败")
		}
		// 原语: 第 6~46 列 × 第 6~30 行 是纯色实心矩形 (精确面积, 没有被任何
		// 别的图元压到) —— 用它同时验"落笔位置对"与"用局部坐标"
		if blue, ok := ParseColor("#2980b9"); ok {
			inner := Rect{X: prim.Box.X + 6, Y: prim.Box.Y + 6, W: 40, H: 24}
			if n := countColor(img, inner, blue); n != 40*24 {
				t.Fatalf("实心矩形覆盖 %d 像素, want %d", n, 40*24)
			}
		} else {
			t.Fatalf("演示色 #2980b9 解析失败")
		}
		// 越界绘制会被裁掉: 原语画布右侧外面必须还是窗口底色
		assertPx(t, img, prim.Box.X+prim.Box.W+2, prim.Box.Y+20, pxWhite, "画布右外侧应保持窗口底色")
	case "slider_demo.js":
		// 滑块: 两个可用 (200x24) + 一个禁用, 且 value 要真的驱动布局
		sls := findAll(root, "slider")
		if len(sls) != 3 {
			t.Fatalf("slider_demo 的 slider 数量 = %d, want 3", len(sls))
		}
		for i, sl := range sls {
			if sl.Box.W != 200 || sl.Box.H != 24 {
				t.Fatalf("第 %d 个滑块尺寸 = %v, want 200x24", i, sl.Box)
			}
		}
		if v, _ := sls[2].PropBool("disabled"); !v {
			t.Fatalf("第三个滑块应为 disabled")
		}
		// volume 初值 40 → 红条宽 = 40*1.6 = 64 (证明 onInput 的数值真的流进了布局)
		bar := findFirst(root, "rect")
		if bar == nil || bar.Box.W != 64 {
			t.Fatalf("红条宽度 = %v, want 64 (volume 初值 40 × 1.6)", bar)
		}
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		// 值 40 → ratio .4 → 滑块左缘 = round(.4*(200-12)) = 75, 填充到 81
		trackY := sls[0].Box.Y + (24-sliderTrackH)/2
		if n := countColor(img, Rect{X: sls[0].Box.X, Y: trackY, W: 60, H: sliderTrackH}, colorAccent); n == 0 {
			t.Fatalf("滑块左段应已填充强调色")
		}
		if n := countColor(img, Rect{X: sls[0].Box.X + 120, Y: trackY, W: 60, H: sliderTrackH}, colorTrack); n == 0 {
			t.Fatalf("滑块右段应还是空轨道")
		}
		// 禁用滑块整盒降饱和: 不再出现"纯轨道色"
		if n := countColor(img, sls[2].Box, colorTrack); n != 0 {
			t.Fatalf("禁用滑块的轨道色未被降饱和 (%d 个像素)", n)
		}
	case "dialog_native_demo.js":
		// 原生对话框演示: 三个按钮 (Alert / Confirm / Open file) 必须可见可点,
		// 且首帧不该真的弹过任何系统对话框 —— 自动化环境里没人点按钮,
		// 真弹一个模态框会让用例一直等到超时。
		btns := findAll(root, "button")
		if len(btns) != 3 {
			t.Fatalf("dialog_native_demo 的 button 数量 = %d, want 3", len(btns))
		}
		for i, b := range btns {
			if b.Box.H <= 0 {
				t.Fatalf("dialog_native_demo 第 %d 个按钮无内容高度: %v", i, b.Box)
			}
			if len(b.Children) == 0 || b.Children[0].Box.W <= 0 {
				t.Fatalf("dialog_native_demo 第 %d 个按钮文字未布局: %v", i, b.Children)
			}
		}
		if n := fake.dialog.messageCalls(); len(n) != 0 {
			t.Fatalf("首帧不该弹任何消息框, got %d 次", len(n))
		}
		if n := fake.dialog.openCalls(); len(n) != 0 {
			t.Fatalf("首帧不该弹文件选择框, got %d 次", len(n))
		}
	case "counter_demo.js":
		// 既有演示: button 必须有内容高度, 否则点击永远命不中 (见 P0-3)
		btn := findFirst(root, "button")
		if btn == nil || btn.Box.H <= 0 {
			t.Fatalf("counter_demo 的 button 无内容高度: %v", btn)
		}
		if len(btn.Children) == 0 || btn.Children[0].Box.W <= 0 {
			t.Fatalf("counter_demo 的按钮文字未布局: %v", btn.Children)
		}
	case "events_demo.js":
		// 事件演示: 浅蓝框 (rect) 上挂齐鼠标/键盘处理器, 初始坐标为占位文案
		box := findFirst(root, "rect")
		if box == nil || box.Box.W != 380 || box.Box.H != 110 {
			t.Fatalf("events_demo 的交互区未按 380x110 布局: %v", box)
		}
		for _, h := range []string{"onMouseMove", "onWheel", "onContextMenu", "onKeyDown", "onKeyUp"} {
			if box.PropHandler(h) == nil {
				t.Fatalf("events_demo 的交互区缺少 %s 处理器", h)
			}
		}
	case "focus_demo.js":
		// 焦点演示: 三个可点块 + 根节点上的 hideFocusRing 响应式开关
		if n := countTag(root, "button"); n != 3 {
			t.Fatalf("focus_demo 的 button 数量 = %d, want 3", n)
		}
		if _, ok := root.PropBool("hideFocusRing"); !ok {
			t.Fatalf("focus_demo 的根节点缺少 hideFocusRing 开关")
		}
		for _, b := range findAll(root, "button") {
			for _, h := range []string{"onFocus", "onBlur"} {
				if b.PropHandler(h) == nil {
					t.Fatalf("focus_demo 的块缺少 %s 处理器", h)
				}
			}
		}
	case "hover_demo.js":
		btns := findAll(root, "button")
		if len(btns) != 3 {
			t.Fatalf("hover_demo 的 button 数量 = %d, want 3", len(btns))
		}
		if d, _ := btns[2].PropBool("disabled"); !d {
			t.Fatalf("hover_demo 的第三个按钮应为 disabled")
		}
		if n := countTag(root, "checkbox"); n != 1 {
			t.Fatalf("hover_demo 的 checkbox 数量 = %d, want 1", n)
		}
		if n := countTag(root, "switch"); n != 1 {
			t.Fatalf("hover_demo 的 switch 数量 = %d, want 1", n)
		}
	case "tabs_demo.js":
		// 条件渲染: 默认 tab 0 → 只挂一个面板 (标题 + 色块 + 说明)
		if n := countTag(root, "button"); n != 3 {
			t.Fatalf("tabs_demo 的 button 数量 = %d, want 3", n)
		}
		slots := 0
		var walk func(n *GuiNode)
		walk = func(n *GuiNode) {
			if n.Tag == "slot" {
				slots++
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(root)
		if slots == 0 {
			t.Fatalf("tabs_demo 没有动态子节点插槽")
		}
		if n := countTag(root, "rect"); n != 1 {
			t.Fatalf("tabs_demo 初始应只有一个面板色块, got %d", n)
		}
		// 面板里的色块按 stretch 撑满内容区 (证明 slot 对布局透明)
		panel := findFirst(root, "rect")
		if panel == nil || panel.Box.H != 48 || panel.Box.W <= 200 {
			t.Fatalf("tabs_demo 面板色块布局异常: %v", panel)
		}
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		assertPx(t, img, panel.Box.X+5, panel.Box.Y+5, pxRed, "默认面板应为红色")
	case "list_demo.js":
		// 列表渲染: 初始 2 项 → 2 个色块 + 2 行文字
		if n := countTag(root, "button"); n != 2 {
			t.Fatalf("list_demo 的 button 数量 = %d, want 2", n)
		}
		// 每行一个 10x10 色块, 加上标题/计数等没有其它色块
		blocks := 0
		var walk func(n *GuiNode)
		walk = func(n *GuiNode) {
			if n.Tag == "rect" && n.Box.W == 10 && n.Box.H == 10 {
				blocks++
			}
			for _, c := range n.Children {
				walk(c)
			}
		}
		walk(root)
		if blocks != 2 {
			t.Fatalf("list_demo 初始应有 2 个列表项色块, got %d", blocks)
		}
		if n := countTag(root, "separator"); n != 1 {
			t.Fatalf("list_demo 的 separator 数量 = %d, want 1", n)
		}
	case "select_demo.js":
		// 下拉演示: 两个字段行 (高 28, 显式宽 220), 初始都是闭合态
		sels := findAll(root, "select")
		if len(sels) != 2 {
			t.Fatalf("select_demo 的 select 数量 = %d, want 2", len(sels))
		}
		for i, s := range sels {
			if s.Box.H != selectRowH || s.Box.W != 220 {
				t.Fatalf("第 %d 个 select 未按 220x%d 布局: %v", i, selectRowH, s.Box)
			}
			if s.expanded || s.popup != nil {
				t.Fatalf("第 %d 个 select 初始不应处于展开态", i)
			}
		}
		if n := countTag(root, "select-option"); n != 0 {
			t.Fatalf("未展开时不该有下拉项, got %d", n)
		}
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		assertPx(t, img, sels[0].Box.X+4, sels[0].Box.Y+selectRowH/2, pxFieldFace, "select_demo 字段白底")
		assertPx(t, img, sels[0].Box.X+4, sels[0].Box.Y, pxBtnEdge, "select_demo 字段 1px 边框")
	case "menu_demo.js":
		// 菜单演示: 顶部菜单栏 (26px, 铺满宽), 三个标题 + 右侧状态文本;
		// 初始无弹层 (所有菜单都是收起的), 也没挂右键菜单。
		bar := findFirst(root, "menubar")
		if bar == nil {
			t.Fatalf("menu_demo 缺少 menubar 节点")
		}
		if bar.Box.Y != 0 || bar.Box.H != menuBarH {
			t.Fatalf("menubar 未占据顶部一行: %v (want Y=0 H=%d)", bar.Box, menuBarH)
		}
		// 菜单栏铺满窗口宽 (用根盒做参照, 不直接读 fake 的 w/h: 那是互斥量
		// 保护的字段, 测试里裸读会踩 data race)。
		if bar.Box.W != root.Box.W {
			t.Fatalf("menubar 未铺满窗口宽: W = %d, want %d", bar.Box.W, root.Box.W)
		}
		// 标题行在同一水平线上依次排开, 互不重叠
		titles := findAll(root, "menu")
		if len(titles) != 4 { // File / Edit / View + View 里嵌的 Theme 子菜单
			t.Fatalf("menu 数量 = %d, want 4", len(titles))
		}
		prevRight := bar.Box.X
		for i, m := range titles[:3] {
			if m.Box.Y != bar.Box.Y || m.Box.H != menuBarH {
				t.Fatalf("第 %d 个菜单标题未与菜单栏同行: %v", i, m.Box)
			}
			if m.Box.X < prevRight {
				t.Fatalf("第 %d 个菜单标题与前一个重叠: X = %d, 前一个右缘 = %d",
					i, m.Box.X, prevRight)
			}
			prevRight = m.Box.X + m.Box.W
		}
		// 首帧应无任何弹层, 菜单项与分隔线都还没物化
		if n := countTag(root, "menu-popup"); n != 0 {
			t.Fatalf("menu_demo 初始不该有弹层, got %d", n)
		}
		if n := countTag(root, "menu-item"); n != 0 {
			t.Fatalf("menu_demo 初始不该有菜单项行, got %d", n)
		}
		if contextMenuNode(root) != nil {
			t.Fatalf("menu_demo 初始不该挂右键菜单")
		}
		// 菜单栏是普通容器: 内容区必须紧贴在它下方 (没有"隐式插入的顶部条"
		// 把内容区再往下推一段 —— 那会让下面的坐标整体漂移)。
		body := root.Children[len(root.Children)-1]
		if body.Box.Y != bar.Box.Y+bar.Box.H {
			t.Fatalf("内容区未紧贴菜单栏下方: body.Y = %d, 菜单栏底 = %d",
				body.Box.Y, bar.Box.Y+bar.Box.H)
		}
	case "clipboard_demo.js":
		// 剪贴板演示: 一个多行框 + 两个按钮; 首帧 status 为空、未点过按钮
		ta := findFirst(root, "textarea")
		if ta == nil {
			t.Fatalf("clipboard_demo 缺少 textarea 节点")
		}
		if ta.taValue() != "Hello from Gox" {
			t.Fatalf("初始值 = %q, want %q", ta.taValue(), "Hello from Gox")
		}
		if got := findAll(root, "button"); len(got) != 2 {
			t.Fatalf("按钮数 = %d, want 2", len(got))
		}
	case "ime_demo.js":
		// 输入法演示: 两个编辑器都挂上, 初始空值, 提交次数从 0 起
		in := findFirst(root, "input")
		ta := findFirst(root, "textarea")
		if in == nil || ta == nil {
			t.Fatalf("ime_demo 缺少 input/textarea 节点")
		}
		if in.inputValue() != "" || ta.taValue() != "" {
			t.Fatalf("ime_demo 初始值应为空, got %q / %q", in.inputValue(), ta.taValue())
		}
		if in.focused || ta.focused {
			t.Fatalf("首帧不该有编辑器处于获焦态")
		}
	case "input_demo.js":
		// 单行输入: 初始空值, placeholder 态, 未获焦所以不画光标
		in := findFirst(root, "input")
		if in == nil {
			t.Fatalf("input_demo 缺少 input 节点")
		}
		if in.Box.H != selectRowH || in.Box.W != 260 {
			t.Fatalf("input 未按 260x%d 布局: %v", selectRowH, in.Box)
		}
		if in.Box.W != 260 {
			t.Fatalf("input 宽度 = %d, want 260", in.Box.W)
		}
		if in.inputValue() != "" || in.hasInputValue() {
			t.Fatalf("input_demo 初始值应为空, got %q", in.inputValue())
		}
		if in.focused {
			t.Fatalf("首帧不该有输入框处于获焦态")
		}
		// 首帧不该画出光标: 光标是近黑的 1px 竖线, 字段内部不该出现这个颜色。
		// (不能只看左侧留白那一格 —— 演示带 placeholder, 那里正好有灰字。)
		img := shotsImage(fake)
		if img == nil {
			t.Fatalf("未捕获上屏帧")
		}
		for y := in.Box.Y + 1; y < in.Box.Y+in.Box.H-1; y++ {
			for x := in.Box.X + 1; x < in.Box.X+in.Box.W-1; x++ {
				if img.RGBAAt(x, y) == pxCaret {
					t.Fatalf("input_demo 未获焦却画了光标: (%d,%d)", x, y)
				}
			}
		}
	case "dialog_demo.js":
		// 弹层演示: 初始 open=false 的 dialog (在树上但不绘制) + 未挂载的 toast
		dlg := findFirst(root, "dialog")
		if dlg == nil {
			t.Fatalf("dialog_demo 缺少 dialog 节点")
		}
		if dlg.overlayVisible() {
			t.Fatalf("dialog_demo 的 dialog 初始应为关闭态")
		}
		if dlg.Box != (Rect{}) {
			t.Fatalf("关闭的 dialog 盒子应为空: %v", dlg.Box)
		}
		if n := countTag(root, "toast"); n != 0 {
			t.Fatalf("toast 由条件渲染控制, 初始不该挂载, got %d", n)
		}
		if dlg.PropHandler("onClose") == nil {
			t.Fatalf("dialog_demo 的 dialog 缺少 onClose 处理器")
		}
	case "transition_demo.js":
		// 过渡动画演示: 首帧是"静止"状态 —— 首次赋值不做过渡 (与 CSS 一致),
		// 所以挂载后不该有任何活动动画, 尺寸就是终值。
		// 6 个 rect: 蓝条 / 淡出组的红块+橙块 / 灰色轨道 / 绿点 / 黄条。
		// 这里不按下标取 (容易随脚本微调错位), 一律按 props 特征定位。
		rects := findAll(root, "rect")
		if len(rects) != 6 {
			t.Fatalf("transition_demo 的 rect 数量 = %d, want 6", len(rects))
		}
		// ① 宽度过渡的起点: wide=false → 60 (带 width transition 的那个就是蓝条)
		blue := findFirstWhere(root, func(n *GuiNode) bool {
			return n.Tag == "rect" && n.PropHasTransition("width")
		})
		if blue == nil {
			t.Fatalf("transition_demo 缺少带 width 过渡的蓝条")
		}
		if blue.Box.W != 60 || blue.Box.H != 18 {
			t.Fatalf("蓝条初始尺寸 = %v, want 60x18", blue.Box)
		}
		// ② 不透明度组的行容器 (两个 24x24 色块 + 文本)
		fading := findFirstWhere(root, func(n *GuiNode) bool {
			return n.Tag == "row" && n.PropHasTransition("opacity")
		})
		if fading == nil {
			t.Fatalf("transition_demo 缺少带 opacity 过渡的 row 容器")
		}
		if v, ok := fading.PropNum("opacity"); !ok || v != 1 {
			t.Fatalf("淡出组初始 opacity = %v (ok=%v), want 1", v, ok)
		}
		if fading.Box.H != 28 {
			t.Fatalf("淡出组未按 28 高布局: %v", fading.Box)
		}
		// ③ 位移过渡: 绿点绝对定位, left 由轨道坐标 +4 决定
		dot := findFirstWhere(root, func(n *GuiNode) bool {
			return n.Tag == "rect" && n.PropHasTransition("left")
		})
		track := findFirstWhere(root, func(n *GuiNode) bool {
			return n.Tag == "rect" && n.PropHas("position") && n.Box.W == 320
		})
		if dot == nil || track == nil {
			t.Fatalf("transition_demo 缺少绿点或灰色轨道 (dot=%v track=%v)", dot, track)
		}
		if dot.Box.X != track.Box.X+4 || dot.Box.Y != track.Box.Y+2 {
			t.Fatalf("绿点位置 = %v (轨道 %v), want 轨道内 +4/+2", dot.Box, track.Box)
		}
		// ④ 命令式 animate 的黄条: 初始 40 宽 (status=idle)。
		// 蓝条同样是"带 width 过渡的 rect", 只能靠高度区分 (18 vs 14)。
		yellow := findFirstWhere(root, func(n *GuiNode) bool {
			if n.Tag != "rect" || !n.PropHasTransition("width") {
				return false
			}
			h, _ := n.PropNum("height")
			return h == 14
		})
		if yellow == nil {
			t.Fatalf("transition_demo 缺少黄条")
		}
		if yellow.Box.W != 40 || yellow.Box.H != 14 {
			t.Fatalf("黄条初始尺寸 = %v, want 40x14", yellow.Box)
		}
		if yellow == blue {
			t.Fatalf("黄条与蓝条定位到同一个节点, 特征选取不够特异")
		}
		// 首帧不该有任何动画在跑 (否则静止界面会一直占着 16ms 帧定时器)
		for _, n := range rects {
			if len(n.anim) != 0 {
				t.Fatalf("%s 首帧不该有活动动画: %v", n.Tag, n.anim)
			}
		}
		if animRunning {
			t.Fatalf("首帧不该有动画定时器在跑")
		}
	case "resize_demo.js":
		// 屏幕 A 演示初始宽布局: win signal 初值 520 (与窗口配置一致, 与假
		// Surface 的固定 400 宽无关 —— 断言的是响应式管线, 不是后端)。
		if !textContainsAny(root, "wide layout") {
			t.Fatalf("初始 (520px) 应是宽布局")
		}
		if !textContainsAny(root, "520 x 360") {
			t.Fatalf("尺寸文本应显示初值 520 x 360")
		}
		// 底部进度条宽度 = max(60, 520-300) = 220, 靠 signal 驱动
		bar := findFirst(root, "rect")
		if bar == nil || bar.Box.W != 220 || bar.Box.H != 10 {
			t.Fatalf("跟随窗口的进度条 = %v, want 220x10", bar)
		}
	case "routing_demo.js":
		// 路由 A 演示初始 home: 切页即卸载 ⇒ editor 的 input 不在树上,
		// 导航两个按钮 (交互全流程见 TestRoutingDemoScript)。
		if !textContainsAny(root, "route: home") {
			t.Fatalf("初始应停在 home")
		}
		if inp := findFirst(root, "input"); inp != nil {
			t.Fatalf("home 页不该挂载 editor 的 input")
		}
		if n := countTag(root, "button"); n != 2 {
			t.Fatalf("导航 button 数量 = %d, want 2", n)
		}
	}
}
