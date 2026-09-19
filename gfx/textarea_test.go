package gfx

import (
	"image"
	"strings"
	"testing"
	"time"
)

// P2-6b 多行文本编辑器 <textarea>。
//
// 断言分两层, 与 input 一致:
//   - 脱 VM 层: 编辑语义走纯函数 taApplyKey (不需要真 VM 承接 onInput 写回),
//     尺寸/绘制/光标/滚动走 renderTree + mountTestApp;
//   - 全链路层: textarea_demo.js 验证受控写回 (真 VM 才会执行 onInput)。

// mkTextarea 造一个带 value 的多行编辑框。
func mkTextarea(value string) *GuiNode {
	n := mkNode("textarea", nil)
	if value != "" {
		withStr(n, "value", value)
	}
	return n
}

// applyKey 在给定文本与光标上按一次键, 返回 "文本/行/列/内容是否变化"。
func applyKey(t *testing.T, value string, line, col int, key string) (string, int, int, bool) {
	t.Helper()
	res := taApplyKey(strings.Split(value, "\n"), line, col, key, false, false)
	return strings.Join(res.lines, "\n"), res.line, res.col, res.changed
}

// ===== 编辑语义 (纯函数) =====

func TestTaApplyKeyInsertAndEnter(t *testing.T) {
	// 插入
	v, line, col, changed := applyKey(t, "ab", 0, 1, "x")
	if v != "axb" || line != 0 || col != 2 || !changed {
		t.Fatalf("插入结果 = %q (%d,%d) changed=%v", v, line, col, changed)
	}
	if v, _, col, _ := applyKey(t, "ab", 0, 2, "c"); v != "abc" || col != 3 {
		t.Fatalf("行尾插入 = %q col=%d", v, col)
	}
	if v, _, col, _ := applyKey(t, "ab", 0, 0, "c"); v != "cab" || col != 1 {
		t.Fatalf("行首插入 = %q col=%d", v, col)
	}
	// Enter 在中间 = 把本行切成两行, 光标落在新行行首
	v, line, col, changed = applyKey(t, "abcd", 0, 2, "Enter")
	if v != "ab\ncd" || line != 1 || col != 0 || !changed {
		t.Fatalf("Enter 分行 = %q (%d,%d) changed=%v", v, line, col, changed)
	}
	// Enter 在行尾 = 追加一个空行
	if v, line, _, _ := applyKey(t, "ab", 0, 2, "Enter"); v != "ab\n" || line != 1 {
		t.Fatalf("行尾 Enter = %q line=%d", v, line)
	}
	// Enter 在中间行 = 插在中间 (不能只处理首末两行)
	if v, line, _, _ := applyKey(t, "aa\nbb\ncc", 1, 1, "Enter"); v != "aa\nb\nb\ncc" || line != 2 {
		t.Fatalf("中间行 Enter = %q line=%d", v, line)
	}
}

func TestTaApplyKeyBackspace(t *testing.T) {
	if v, _, col, changed := applyKey(t, "ab", 0, 2, "Backspace"); v != "a" || col != 1 || !changed {
		t.Fatalf("行内退格 = %q col=%d changed=%v", v, col, changed)
	}
	// 行首退格 = 与上一行合并, 光标停在合并点
	v, line, col, changed := applyKey(t, "ab\ncd", 1, 0, "Backspace")
	if v != "abcd" || line != 0 || col != 2 || !changed {
		t.Fatalf("行首退格 = %q (%d,%d) changed=%v", v, line, col, changed)
	}
	// 首行行首退格: 不改内容, 但按键仍算被消费
	res := taApplyKey([]string{"ab"}, 0, 0, "Backspace", false, false)
	if !res.consumed || res.changed || res.lines[0] != "ab" {
		t.Fatalf("首行行首退格不该改动: %+v", res)
	}
	if v, _, _, _ := applyKey(t, "", 0, 0, "Backspace"); v != "" {
		t.Fatalf("空文本退格 = %q", v)
	}
}

func TestTaApplyKeyDelete(t *testing.T) {
	if v, _, col, changed := applyKey(t, "ab", 0, 0, "Delete"); v != "b" || col != 0 || !changed {
		t.Fatalf("行内删除 = %q col=%d changed=%v", v, col, changed)
	}
	// 行尾删除 = 把下一行接上来
	v, line, col, changed := applyKey(t, "ab\ncd", 0, 2, "Delete")
	if v != "abcd" || line != 0 || col != 2 || !changed {
		t.Fatalf("行尾删除 = %q (%d,%d) changed=%v", v, line, col, changed)
	}
	if v, _, _, changed := applyKey(t, "ab", 0, 2, "Delete"); v != "ab" || changed {
		t.Fatalf("末行行尾删除不该改动: %q changed=%v", v, changed)
	}
}

func TestTaApplyKeyCaretMoves(t *testing.T) {
	// 左右在行边界上跨行 (文本编辑器的常规行为)
	if _, line, col, changed := applyKey(t, "ab\ncd", 1, 0, "ArrowLeft"); line != 0 || col != 2 || changed {
		t.Fatalf("行首左移 = (%d,%d) changed=%v", line, col, changed)
	}
	if _, line, col, _ := applyKey(t, "ab\ncd", 0, 2, "ArrowRight"); line != 1 || col != 0 {
		t.Fatalf("行尾右移 = (%d,%d)", line, col)
	}
	// 上下移动: 列号按目标行长度钳位, 不会把光标顶到行外
	if _, line, col, _ := applyKey(t, "abcdef\nxy", 0, 5, "ArrowDown"); line != 1 || col != 2 {
		t.Fatalf("下移钳位 = (%d,%d), want (1,2)", line, col)
	}
	if _, line, col, _ := applyKey(t, "ab\ncdefgh", 1, 5, "ArrowUp"); line != 0 || col != 2 {
		t.Fatalf("上移钳位 = (%d,%d), want (0,2)", line, col)
	}
	// Home/End 是本行首尾, 不是全文首尾
	if _, line, col, _ := applyKey(t, "ab\ncd", 1, 1, "Home"); line != 1 || col != 0 {
		t.Fatalf("Home = (%d,%d)", line, col)
	}
	if _, line, col, _ := applyKey(t, "ab\ncd", 0, 0, "End"); line != 0 || col != 2 {
		t.Fatalf("End = (%d,%d)", line, col)
	}
	// 边界: 末行下移 / 首行上移都不动
	if _, line, _, _ := applyKey(t, "ab\ncd", 1, 0, "ArrowDown"); line != 1 {
		t.Fatalf("末行下移不该越界: line=%d", line)
	}
	if _, line, _, _ := applyKey(t, "ab\ncd", 0, 0, "ArrowUp"); line != 0 {
		t.Fatalf("首行上移不该越界: line=%d", line)
	}
}

func TestTaApplyKeyConsumptionMatrix(t *testing.T) {
	consumed := []string{"a", "Z", "7", " ", "!", "中", "Enter", "Backspace",
		"Delete", "ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"}
	for _, k := range consumed {
		if res := taApplyKey([]string{"x"}, 0, 1, k, false, false); !res.consumed {
			t.Fatalf("按键 %q 应被编辑框消费", k)
		}
	}
	passed := []struct {
		key       string
		ctrl, alt bool
	}{
		{"Escape", false, false},
		{"Tab", false, false},
		{"F1", false, false},
		{"Shift", false, false},
		{"c", true, false},
		{"a", false, true},
	}
	for _, p := range passed {
		if res := taApplyKey([]string{"x"}, 0, 1, p.key, p.ctrl, p.alt); res.consumed {
			t.Fatalf("按键 %q (ctrl=%v alt=%v) 不该被编辑框消费", p.key, p.ctrl, p.alt)
		}
	}
}

func TestTaApplyKeyIsPure(t *testing.T) {
	src := []string{"ab", "cd"}
	before := strings.Join(src, "\n")
	for _, k := range []string{"x", "Enter", "Backspace", "Delete"} {
		taApplyKey(src, 0, 1, k, false, false)
	}
	if got := strings.Join(src, "\n"); got != before {
		t.Fatalf("taApplyKey 改动了入参切片: %q -> %q", before, got)
	}
}

func TestTaApplyKeyClampsStaleCaret(t *testing.T) {
	// 受控值被 JS 改短后, 旧光标 (行 5 / 列 99) 必须被钳回合法位置而不是越界
	res := taApplyKey([]string{"ab"}, 5, 99, "x", false, false)
	if res.line != 0 || res.col != 3 {
		t.Fatalf("越界光标未钳位: (%d,%d)", res.line, res.col)
	}
	if got := strings.Join(res.lines, "\n"); got != "abx" {
		t.Fatalf("钳位后插入结果 = %q", got)
	}
}

// ===== 尺寸 / 绘制 / 滚动 =====

func TestTextareaDefaultGeometry(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	ta := mkTextarea("")
	mountChildren(root, ta)
	Layout(root, 400, 300)

	wantH := textareaDefaultRows*lineHeight(ta.FontSize()) + 2*textareaPadY
	if ta.Box.W != textareaMinW || ta.Box.H != wantH {
		t.Fatalf("缺省尺寸 = %dx%d, want %dx%d", ta.Box.W, ta.Box.H, textareaMinW, wantH)
	}
	root2 := mkNode("column", nil)
	ta2 := mkNode("textarea", map[string]float64{"rows": 6})
	mountChildren(root2, ta2)
	Layout(root2, 400, 300)
	if want := 6*lineHeight(ta2.FontSize()) + 2*textareaPadY; ta2.Box.H != want {
		t.Fatalf("rows=6 时高 = %d, want %d", ta2.Box.H, want)
	}
	root3 := mkNode("column", nil)
	ta3 := mkNode("textarea", map[string]float64{"width": 300, "height": 90})
	mountChildren(root3, ta3)
	Layout(root3, 400, 300)
	if ta3.Box.W != 300 || ta3.Box.H != 90 {
		t.Fatalf("显式尺寸被覆盖: %v", ta3.Box)
	}
	// 不参与交叉轴 stretch (与 input 一致: 打字时宽度不该跳)
	if ta3.Box.W == 400 {
		t.Fatalf("textarea 不该被拉伸")
	}
}

func TestTextareaDrawsEachLineAndClips(t *testing.T) {
	requireFont(t)
	lh := lineHeight(16)
	root := mkNode("column", nil)
	// 只给 2 行高的空间, 内容有 3 行 → 第三行必须被裁掉
	ta := mkNode("textarea", map[string]float64{
		"width": 200, "height": float64(2*lh + 2*textareaPadY),
	})
	withStr(ta, "value", "one\ntwo\nthree")
	mountChildren(root, ta)
	img := renderTree(root, 300, 300)

	area := ta.taArea()
	for i := 0; i < 2; i++ {
		band := Rect{X: area.X, Y: area.Y + i*lh, W: area.W, H: lh}
		if n := countColor(img, band, pxWhite); n == band.W*band.H {
			t.Fatalf("第 %d 行没有绘制内容", i)
		}
	}
	// 溢出内容不得画到框外
	outside := Rect{X: 0, Y: ta.Box.Y + ta.Box.H + 2, W: 300, H: 40}
	if n := countColor(img, outside, pxWhite); n != outside.W*outside.H {
		t.Fatalf("溢出内容画到了框外")
	}
}

func TestTextareaPlaceholderPixels(t *testing.T) {
	requireFont(t)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{"width": 200, "height": 80})
	withStr(ta, "placeholder", "Type here...")
	mountChildren(root, ta)
	img := renderTree(root, 300, 200)

	area := ta.taArea()
	if n := countColor(img, area, pxWhite); n == area.W*area.H {
		t.Fatalf("placeholder 没有绘制")
	}
	// 不能断言"存在恰好等于 colorPlaceholder 的像素": 字形是抗锯齿的,
	// 边缘像素会与白底按 alpha 混合成中间色, 深色像素只在描边中心出现。
	// 改断言**最暗像素的亮度**: 占位灰字比正文浅得多, 两者的可分性是稳定的。
	phLuma := minLuma(img, area)
	if phLuma < 300 {
		t.Fatalf("placeholder 看起来太黑 (最暗亮度 %d), 应使用占位灰", phLuma)
	}
	// 有真实值时不该再画占位灰字 (而且正文明显更黑)
	withStr(ta, "value", "real")
	img2 := renderTree(root, 300, 200)
	if rtLuma := minLuma(img2, area); rtLuma > 200 {
		t.Fatalf("正文颜色太浅 (最暗亮度 %d), 说明还在画 placeholder", rtLuma)
	}
}

// minLuma 返回区域内最暗像素的 R+G+B 之和 (越小越黑), 用于区分"灰字/正文"。
func minLuma(img *image.RGBA, r Rect) int {
	best := -1
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			p := img.RGBAAt(x, y)
			s := int(p.R) + int(p.G) + int(p.B)
			if best < 0 || s < best {
				best = s
			}
		}
	}
	return best
}

// ===== 全链路 (假 Surface + 真 VM) =====

// TestTextareaDemoTypingMirrorsValue 验证受控写回: 只有真 VM 在场时
// object.CallFunction 才会执行 onInput 并写回 signal (无 VM 时回调静默返回).
func TestTextareaDemoTypingMirrorsValue(t *testing.T) {
	runDemoSteps(t, "textarea_demo.js", []func(*GuiNode, *fakeSurface){
		// 1) 点编辑框获焦
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if ta == nil {
				t.Fatalf("textarea_demo 缺少 textarea 节点")
			}
			if ta.Box.H != 4*lineHeight(ta.FontSize())+2*textareaPadY {
				t.Fatalf("rows=4 未生效: %v", ta.Box)
			}
			fake.push(Event{Kind: EventMouseUp, X: ta.Box.X + 6, Y: ta.Box.Y + 6})
		},
		// 2) 敲两个字符
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if !ta.focused {
				t.Fatalf("点击后 textarea 未获焦")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "h"})
			fake.push(Event{Kind: EventKeyDown, Key: "i"})
		},
		// 3) 镜像文本跟随 (受控写回), 然后回车换行
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if !textContainsAny(root, `value = "hi"`) {
				t.Fatalf("镜像文本未跟随输入: %q", ta.taValue())
			}
			if ta.caretLine != 0 || ta.caret != 2 {
				t.Fatalf("输入两个字符后光标 = (%d,%d), want (0,2)", ta.caretLine, ta.caret)
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Enter"})
		},
		// 4) Enter 被编辑框消费并插入换行 (单行 input 会放行给上层, 多行框不会)
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if ta.taValue() != "hi\n" {
				t.Fatalf("Enter 未插入换行: %q", ta.taValue())
			}
			if ta.caretLine != 1 || ta.caret != 0 {
				t.Fatalf("Enter 后光标 = (%d,%d), want (1,0)", ta.caretLine, ta.caret)
			}
			fake.push(Event{Kind: EventKeyDown, Key: "y"})
			fake.push(Event{Kind: EventKeyDown, Key: "o"})
		},
		// 5) 两行文本; 再按 Esc (编辑框不消费 → 冒泡到脚本 onKeyDown)
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if ta.taValue() != "hi\nyo" {
				t.Fatalf("两行输入结果 = %q", ta.taValue())
			}
			if !textContainsAny(root, "value = \"hi\nyo\"") {
				t.Fatalf("镜像文本未反映两行值")
			}
			fake.push(Event{Kind: EventKeyDown, Key: "Escape"})
		},
		// 6) Esc 冒泡到脚本, 文本未被改动
		func(root *GuiNode, fake *fakeSurface) {
			ta := findFirst(root, "textarea")
			if ta.taValue() != "hi\nyo" {
				t.Fatalf("Esc 改动了文本: %q", ta.taValue())
			}
			if !textContainsAny(root, "escapes = 1") {
				t.Fatalf("Esc 未冒泡到 onKeyDown")
			}
		},
	})
}

// TestMultilineDemoWrapAndEllipsis 验证 JSX 里的 wrap / ellipsis 真的走到布局:
// 同一段文本在三种写法下的盒高必须分别是 多行 / 恰好 2 行 / 单行。
func TestMultilineDemoWrapAndEllipsis(t *testing.T) {
	runDemoSteps(t, "multiline_demo.js", []func(*GuiNode, *fakeSurface){
		func(root *GuiNode, fake *fakeSurface) {
			blocks := findAll(root, "text")
			if len(blocks) < 6 {
				t.Fatalf("multiline_demo 的 text 节点数 = %d, want >= 6", len(blocks))
			}
			// 顺序: 标签, wrap 块, 标签, wrap+ellipsis 块, 标签, 不换行块
			wrap, ell, plain := blocks[1], blocks[3], blocks[5]
			lh := lineHeight(wrap.FontSize())

			if wrap.Box.W != 260 {
				t.Fatalf("wrap 块未按 width=260 布局: %d", wrap.Box.W)
			}
			if wrap.Box.H <= lh {
				t.Fatalf("wrap 块只有一行 (高 %d), 换行没生效", wrap.Box.H)
			}
			if ell.Box.H != 2*lh {
				t.Fatalf("ellipsis=2 的块高 = %d, want %d", ell.Box.H, 2*lh)
			}
			lines := ell.blockLines(ell.Box.W)
			if len(lines) != 2 || !strings.HasSuffix(lines[1], ellipsisMark) {
				t.Fatalf("ellipsis 块内容不对: %q", lines)
			}
			if plain.Box.H != lineHeight(plain.FontSize()) {
				t.Fatalf("不给 wrap 的块应保持单行: 高 %d", plain.Box.H)
			}
			// 三块都挂满宽度约束, 不换行的那块会被硬截断 (宽度仍是 260)
			if plain.Box.W != 260 {
				t.Fatalf("不换行块宽 = %d, want 260", plain.Box.W)
			}
		},
	})
}

func TestTextareaScrollFollowsCaret(t *testing.T) {
	lh := lineHeight(16)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{
		"width": 200, "height": float64(3*lh + 2*textareaPadY),
	})
	withStr(ta, "value", "l0\nl1\nl2\nl3\nl4\nl5")
	mountChildren(root, ta)
	Layout(root, 300, 300)

	// 光标在第 5 行 (内容 6 行, 可视 3 行) → offsetY 必须把它带进视野
	ta.caretLine, ta.caret = 5, 0
	ta.taEnsureCaretVisible()
	area := ta.taArea()
	if ta.offsetY+area.H < 6*lh {
		t.Fatalf("光标行不在视野内: offsetY=%d area=%+v", ta.offsetY, area)
	}
	if max := ta.taMaxOffset(); ta.offsetY > max {
		t.Fatalf("offsetY 超出上限: %d > %d", ta.offsetY, max)
	}
	// 回到第 0 行 → 偏移回到顶部
	ta.caretLine, ta.caret = 0, 0
	ta.taEnsureCaretVisible()
	if ta.offsetY != 0 {
		t.Fatalf("光标回到首行后 offsetY = %d, want 0", ta.offsetY)
	}
	// 内容变短 (外部把 value 改小) 后布局必须把越界偏移钳回来
	ta.offsetY = 500
	withStr(ta, "value", "only")
	Layout(root, 300, 300)
	if ta.offsetY != 0 {
		t.Fatalf("内容变短后 offsetY 未钳位: %d", ta.offsetY)
	}
}

func TestTextareaClickPlacesCaret(t *testing.T) {
	lh := lineHeight(16)
	root := mkNode("column", nil)
	// 可视 3 行
	ta := mkNode("textarea", map[string]float64{"width": 200, "height": float64(3*lh + 2*textareaPadY)})
	withStr(ta, "value", "hello\nworld")
	mountChildren(root, ta)
	Layout(root, 300, 300)
	a := &app{}
	area := ta.taArea()

	a.taSetCaretFromXY(ta, area.X, area.Y+lh+2)
	if ta.caretLine != 1 || ta.caret != 0 {
		t.Fatalf("点击第二行行首 = (%d,%d)", ta.caretLine, ta.caret)
	}
	a.taSetCaretFromXY(ta, area.X+999, area.Y+2)
	if ta.caretLine != 0 || ta.caret != len([]rune("hello")) {
		t.Fatalf("点击行尾 = (%d,%d)", ta.caretLine, ta.caret)
	}
	// 滚动后再点: y 要按 offsetY 折算回内容行号。6 行内容可视 3 行 ⇒
	// 最大偏移正好是 3 行, 所以"滚到底"时视野顶部就是内容第 3 行。
	withStr(ta, "value", "a\nb\nc\nd\ne\nf")
	ta.offsetY = 3 * lh
	Layout(root, 300, 300)
	if ta.offsetY != 3*lh {
		t.Fatalf("滚到底时 offsetY = %d, want %d", ta.offsetY, 3*lh)
	}
	a.taSetCaretFromXY(ta, area.X, area.Y+2)
	if ta.caretLine != 3 {
		t.Fatalf("滚动后点击首行 = 第 %d 行, want 3", ta.caretLine)
	}
}

func TestTextareaWheelScrollsAndBubbles(t *testing.T) {
	lh := lineHeight(16)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{"width": 200, "height": float64(3*lh + 2*textareaPadY)})
	withStr(ta, "value", "l0\nl1\nl2\nl3\nl4\nl5\nl6\nl7")
	mountChildren(root, ta)
	fake, a := mountTestApp(t, root, 400, 300)

	cx, cy := ta.Box.X+10, ta.Box.Y+10
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: -120})
	if ta.offsetY != scrollNotch {
		t.Fatalf("框内滚轮 offsetY = %d, want %d", ta.offsetY, scrollNotch)
	}
	// 一直滚到底 → 钳位
	for i := 0; i < 4; i++ {
		pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: cx, Y: cy, DeltaY: -120})
	}
	if ta.offsetY != ta.taMaxOffset() {
		t.Fatalf("未滚到底: %d != %d", ta.offsetY, ta.taMaxOffset())
	}
	// 光标在框外 → 不影响它
	pushAndPump(t, fake, a, Event{Kind: EventMouseWheel, X: 380, Y: 280, DeltaY: -120})
	if ta.offsetY != ta.taMaxOffset() {
		t.Fatalf("框外滚轮不该影响 textarea: %d", ta.offsetY)
	}
}

func TestTextareaFocusDrawsCaret(t *testing.T) {
	requireFont(t)
	lh := lineHeight(16)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{"width": 200, "height": float64(2*lh + 2*textareaPadY)})
	withStr(ta, "value", "ab")
	mountChildren(root, ta)
	_, a := mountTestApp(t, root, 400, 300)
	resetCaretPhase(time.Now()) // 让"此刻"处于可见相, 断言才稳定

	a.setFocus(ta)
	a.redraw()
	withCaret := countColor(a.img, ta.taArea(), colorText)

	// 再确认"没有光标时"的基线: 把相位起点往前拨半个周期 → 此刻处于灭相。
	// (注意是往前拨: 把 epoch 设到未来会使 now-epoch 变负数, 整除截断成 0,
	// 结果仍是"亮", 断言会永远相等而看不出问题。)
	resetCaretPhase(time.Now().Add(-caretBlinkMS * time.Millisecond))
	markNodeDirty(ta)
	a.redraw()
	noCaret := countColor(a.img, ta.taArea(), colorText)

	if withCaret <= noCaret {
		t.Fatalf("获焦时光标未绘制: 有光标 %d px, 无光标 %d px", withCaret, noCaret)
	}
	// 失焦后也不画光标 (与不可见相基线一致)
	resetCaretPhase(time.Now())
	a.setFocus(nil)
	markNodeDirty(ta)
	a.redraw()
	if n := countColor(a.img, ta.taArea(), colorText); n != noCaret {
		t.Fatalf("失焦后仍画了光标: %d != %d", n, noCaret)
	}
}
