package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// 软换行 (ru628k) —— textarea 的视觉行模型。
//
// 分两层测, 与 textarea 的既有测法一致:
//   - **纯逻辑层**: 直接给内核一张手写的视觉行表, 断言 ↑↓ / Home / End 的落点。
//     期望值全部由 runeAdvanceStyled 现算, 不写死像素 —— 字体一换就变。
//   - **几何层**: 真布局 (Layout) 之后查 taVisualModel / taCaretX / 绘制像素。
//
// 最要紧的一条是 TestSoftWrapModelMatchesWrapText: 编辑框的折行与 <text wrap>
// 的折行必须**逐段一致**。两处各写一份算法的症状是"光标画的位置和字不在同一格",
// 而且只在特定字符组合下出现。

// mkWrapped 造一个已布局的 textarea: 宽 w、高 rows 行。
func mkWrapped(t *testing.T, value string, w, rows int) (*GuiNode, *GuiNode) {
	t.Helper()
	lh := lineHeight(16)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{
		"width": float64(w), "height": float64(rows*lh + 2*textareaPadY),
	})
	withStr(ta, "value", value)
	mountChildren(root, ta)
	Layout(root, 400, 400)
	return root, ta
}

// ===== 视觉行模型 =====

func TestSoftWrapSplitsVisualLines(t *testing.T) {
	requireFont(t)
	const w = 200
	_, ta := mkWrapped(t, strings.Repeat("a", 200), w, 4)
	area := ta.taArea()
	aw := runeAdvanceStyled(ta.taTextStyle(), 'a')
	if aw <= 0 {
		t.Fatalf("'a' 的宽度 = %d", aw)
	}
	per := area.W / aw
	if per < 2 {
		t.Skipf("内容区太窄 (%dpx, 'a'=%dpx), 换行用例无意义", area.W, aw)
	}
	want := (200 + per - 1) / per
	if got := ta.taVisualCount(); got != want {
		t.Fatalf("视觉行数 = %d, want %d (每行 %d 字)", got, want, per)
	}
	// 逻辑行永远只有一行 —— 软换行不该改动受控值, 只影响显示
	if n := len(ta.taLines()); n != 1 {
		t.Fatalf("逻辑行数 = %d, want 1 (软换行不能改文本)", n)
	}
	// 内容高按**视觉行**算: 否则滚轮滚不动折出来的行
	if ta.taContentHeight() != want*ta.taLineHeight() {
		t.Fatalf("内容高 = %d, want %d", ta.taContentHeight(), want*ta.taLineHeight())
	}
}

func TestSoftWrapDisabledByProp(t *testing.T) {
	requireFont(t)
	root := mkNode("column", nil)
	ta := mkNode("textarea", map[string]float64{"width": 200, "height": 80})
	withStr(ta, "value", strings.Repeat("a", 200))
	withBool(ta, "wrap", false)
	mountChildren(root, ta)
	Layout(root, 400, 400)

	if got := ta.taVisualCount(); got != 1 {
		t.Fatalf("wrap=false 时视觉行数 = %d, want 1 (超长行靠右侧裁掉)", got)
	}
	// 缺省必须是**开** (与 CSS textarea 一致): 不写 wrap 的长行会折
	_, def := mkWrapped(t, strings.Repeat("a", 200), 200, 4)
	if def.taVisualCount() <= 1 {
		t.Fatalf("缺省应软换行, 视觉行数 = %d", def.taVisualCount())
	}
}

func TestSoftWrapModelMatchesWrapText(t *testing.T) {
	requireFont(t)
	// 模型拼回去的文本, 必须与 <text wrap> 用的那份折行逐段一致。
	// 中文/英文/空格混排各来一遍 (贪心断点在不同字符上不一样)。
	cases := []string{
		strings.Repeat("a", 137),
		strings.Repeat("中", 91),
		"hello world hello world hello world hello world hello world xyz",
		strings.Repeat("W", 3) + strings.Repeat("i", 40) + "中中中" + strings.Repeat("m", 30),
	}
	const w = 200
	for _, src := range cases {
		_, ta := mkWrapped(t, src, w, 4)
		st := ta.taTextStyle()
		model := ta.taVisualModel()
		want := wrapTextStyled(src, st, ta.taArea().W, 0)
		if len(model) != len(want) {
			t.Fatalf("%q: 视觉行数 %d, wrapTextStyled %d", short(src), len(model), len(want))
		}
		rs := []rune(src)
		for i, v := range model {
			if got := string(rs[v.start:v.end]); got != want[i] {
				t.Fatalf("%q 第 %d 段 = %q, wrapext 得到 %q", short(src), i, got, want[i])
			}
		}
	}
}

// short 把长串截短, 只为了让失败信息可读。
func short(s string) string {
	rs := []rune(s)
	if len(rs) <= 24 {
		return s
	}
	return string(rs[:24]) + "…"
}

// ===== ↑↓ / Home / End (纯逻辑) =====

// wrapView 造一张"一整行折成 N 段"的视觉行表 (每段 per 列), 外加一个"第二逻辑行"。
func wrapView(per, total int) []taVisual {
	var out []taVisual
	for s := 0; s < total; s += per {
		e := s + per
		if e > total {
			e = total
		}
		out = append(out, taVisual{line: 0, start: s, end: e})
	}
	return append(out, taVisual{line: 1, start: 0, end: 2})
}

func TestSoftWrapArrowCrossesVisualLines(t *testing.T) {
	requireFont(t)
	st := TextStyle{Size: 16}
	aw := runeAdvanceStyled(st, 'a')
	// 8 个 'a' 折成两段 (每段 4) + 第二逻辑行 "bb"
	lines := []string{strings.Repeat("a", 8), "bb"}
	view := wrapView(4, 8)
	base := taEditIn{lines: lines, vm: view, multi: true, st: st, aimX: aw}

	// ↓ 从第一段第 1 列 → 第二段: 逻辑行不变, 列 +4
	in := base
	in.line, in.col = 0, 1
	if r := taApplyKeyEx(in, "ArrowDown"); r.line != 0 || r.col != 5 {
		t.Fatalf("↓ 跨视觉行 = (%d,%d), want (0,5)", r.line, r.col)
	}
	// ↑ 走回去
	in.line, in.col = 0, 5
	if r := taApplyKeyEx(in, "ArrowUp"); r.line != 0 || r.col != 1 {
		t.Fatalf("↑ 跨视觉行 = (%d,%d), want (0,1)", r.line, r.col)
	}
	// 再 ↓ 两次 → 落到第二逻辑行
	in.line, in.col = 0, 1
	r := taApplyKeyEx(in, "ArrowDown")
	r = taApplyKeyEx(taEditIn{lines: r.lines, line: r.line, col: r.col, vm: view, multi: true, st: st, aimX: aw}, "ArrowDown")
	if r.line != 1 {
		t.Fatalf("连按两次 ↓ 应到第二逻辑行, got (%d,%d)", r.line, r.col)
	}
	// 边界: 最后一条视觉行再 ↓ 不动
	in.line, in.col = 1, 1
	if r := taApplyKeyEx(in, "ArrowDown"); r.line != 1 || r.col != 1 {
		t.Fatalf("末行 ↓ 不该越界: (%d,%d)", r.line, r.col)
	}
}

func TestSoftWrapArrowKeepsPixelAim(t *testing.T) {
	requireFont(t)
	st := TextStyle{Size: 16}
	// 用宽度明显不同的字符: 比例字体下"第 3 列"与"第 3 个像素位"不是一回事。
	// 若按列号上下走, 光标会在折行处左右横跳。
	aw := runeAdvanceStyled(st, 'a')
	wW := runeAdvanceStyled(st, 'W')
	if aw == wW {
		t.Skip("本机 'a' 与 'W' 同宽 (等宽字体), 像素期望值用例无意义")
	}
	lines := []string{"aaaa" + "WWWW" + "aaaa"}
	view := []taVisual{{line: 0, start: 0, end: 4}, {line: 0, start: 4, end: 8}, {line: 0, start: 8, end: 12}}

	// 第一段第 1 列 (x = aw) 下移: 第二段全是 'W' → x = aw 落在第 1 个 'W' 之后
	in := taEditIn{lines: lines, line: 0, col: 1, vm: view, multi: true, st: st, aimX: aw}
	r := taApplyKeyEx(in, "ArrowDown")
	if r.line != 0 || r.col != 5 {
		t.Fatalf("↓ 保持像素 x: (%d,%d), want (0,5)", r.line, r.col)
	}
	// 再下移到第三段 ('a'): 同样 x = aw → 第 1 列之后 = 9
	in.line, in.col, in.aimX = r.line, r.col, aw
	r = taApplyKeyEx(in, "ArrowDown")
	if r.col != 9 {
		t.Fatalf("再 ↓ 保持像素 x: col=%d, want 9", r.col)
	}
	// 没有期望 x 时退回"按列号钳位" (老语义, 单行框走这条)
	in.line, in.col, in.aimX = 0, 1, -1
	if r := taApplyKeyEx(in, "ArrowDown"); r.col != 4 {
		t.Fatalf("无期望 x 时列号应钳到段首: col=%d, want 4", r.col)
	}
}

func TestSoftWrapHomeEndUseVisualLine(t *testing.T) {
	requireFont(t)
	lines := []string{strings.Repeat("a", 8), "bb"}
	view := wrapView(4, 8)
	base := taEditIn{lines: lines, vm: view, multi: true, aimX: -1}

	// 光标在第二段中间: Home/End 是**第二条视觉行**的首尾, 不是逻辑行的
	in := base
	in.line, in.col = 0, 6
	if r := taApplyKeyEx(in, "Home"); r.col != 4 {
		t.Fatalf("Home 应到视觉行首 (4), got %d", r.col)
	}
	if r := taApplyKeyEx(in, "End"); r.col != 8 {
		t.Fatalf("End 应到视觉行尾 (8), got %d", r.col)
	}
	// 不换行时 (vm=nil) 仍然是逻辑行首尾 —— 老语义不能变
	nb := taEditIn{lines: []string{"abcdef"}, line: 0, col: 3, multi: true, aimX: -1}
	if r := taApplyKeyEx(nb, "Home"); r.col != 0 {
		t.Fatalf("不换行 Home = %d, want 0", r.col)
	}
	if r := taApplyKeyEx(nb, "End"); r.col != 6 {
		t.Fatalf("不换行 End = %d, want 6", r.col)
	}
}

func TestSoftWrapInsertStaysLogical(t *testing.T) {
	// 换行是纯显示变换: 插入位置必须还是**逻辑列**, 不能被视觉行影响
	// (否则在折行处打字会插到别的行去)。
	lines := []string{strings.Repeat("a", 8)}
	view := wrapView(4, 8)
	in := taEditIn{lines: lines, line: 0, col: 6, vm: view, multi: true, aimX: -1}
	r := taApplyKeyEx(in, "X")
	if got := strings.Join(r.lines, "\n"); got != strings.Repeat("a", 6)+"X"+"aa" {
		t.Fatalf("折行处插入结果 = %q", got)
	}
	if r.line != 0 || r.col != 7 {
		t.Fatalf("插入后光标 = (%d,%d), want (0,7)", r.line, r.col)
	}
}

// ===== 几何: 点击落点 / 光标位置 / 滚动 / 绘制 =====

func TestSoftWrapClickPlacesCaretOnVisualLine(t *testing.T) {
	requireFont(t)
	st := TextStyle{Size: 16}
	aw := runeAdvanceStyled(st, 'a')
	const w = 200
	_, ta := mkWrapped(t, strings.Repeat("a", 200), w, 6)
	area := ta.taArea()
	per := area.W / aw
	lh := ta.taLineHeight()
	a := &app{}

	// 点第二条视觉行的行首 → 逻辑行仍是 0, 列 = per
	a.taSetCaretFromXY(ta, area.X, area.Y+lh+2)
	if ta.caretLine != 0 {
		t.Fatalf("软换行不该改逻辑行: caretLine=%d", ta.caretLine)
	}
	if ta.caret != per {
		t.Fatalf("点第二视觉行首 → 列 = %d, want %d", ta.caret, per)
	}
	// 点第一条视觉行里第 3 个字符的中点之后 → 列 = 4
	const k = 3
	a.taSetCaretFromXY(ta, area.X+k*aw+aw/2+1, area.Y+2)
	if ta.caret != k+1 {
		t.Fatalf("点第一视觉行第 %d 字之后 → 列 = %d, want %d", k, ta.caret, k+1)
	}
	// 点超出右边界 → 停在**本条视觉行**的行尾 (不是整行的行尾)
	a.taSetCaretFromXY(ta, area.X+area.W+50, area.Y+lh+2)
	if ta.caret != 2*per {
		t.Fatalf("点超右边界 → 列 = %d, want %d", ta.caret, 2*per)
	}
}

func TestSoftWrapCaretPixelsAlignedWithText(t *testing.T) {
	requireFont(t)
	_, ta := mkWrapped(t, strings.Repeat("a", 200), 200, 6)
	st := ta.taTextStyle()
	model := ta.taVisualModel()
	per := len([]rune(ta.taLines()[0])) // 单逻辑行
	aw := runeAdvanceStyled(st, 'a')

	// 光标在第二视觉行行首: 视觉行号 1, x = 0 (相对内容区左边界)
	ta.caretLine, ta.caret = 0, 0
	lines := ta.taLines()
	rs := []rune(lines[0])
	// 找出第一段的长度
	first := model[0].end
	ta.caret = first
	if got := ta.taCaretVisual(model); got != 1 {
		t.Fatalf("段边界上的光标应在下一视觉行 (1), got %d", got)
	}
	if got := ta.taCaretX(model, st); got != 0 {
		t.Fatalf("第二视觉行行首的光标 x = %d, want 0", got)
	}
	// 段内第 k 列 → x = k*aw (与绘制文字用的是同一套前进宽度)
	ta.caret = first + 2
	if got := ta.taCaretX(model, st); got != 2*aw {
		t.Fatalf("段内第 2 列的光标 x = %d, want %d", got, 2*aw)
	}
	// 保证前提成立 (否则上面的断言是空转)
	if per <= first {
		t.Fatalf("前提不成立: 单行 %d 列, 第一段 %d 列", per, first)
	}
	if len(rs) != per {
		t.Fatalf("rune 数与 len(taLines) 不一致")
	}
}

func TestSoftWrapScrollCountsVisualLines(t *testing.T) {
	requireFont(t)
	aw := runeAdvanceStyled(TextStyle{Size: 16}, 'a')
	const w = 200
	_, ta := mkWrapped(t, strings.Repeat("a", 200), w, 3)
	area := ta.taArea()
	per := area.W / aw
	total := ta.taVisualCount()
	if total <= 3 {
		t.Fatalf("前提不成立: 只有 %d 条视觉行", total)
	}
	// 光标放到文本末尾 → 最后一条视觉行必须被带进视野
	ta.caretLine, ta.caret = 0, 200
	ta.taEnsureCaretVisible()
	if ta.offsetY+area.H < (total-1)*ta.taLineHeight() {
		t.Fatalf("末行不在视野内: offsetY=%d area.H=%d 内容 %d 行", ta.offsetY, area.H, total)
	}
	if max := ta.taMaxOffset(); ta.offsetY > max {
		t.Fatalf("offsetY 超上限: %d > %d", ta.offsetY, max)
	}
	// 回到开头 → 偏移归零
	ta.caretLine, ta.caret = 0, 0
	ta.taEnsureCaretVisible()
	if ta.offsetY != 0 {
		t.Fatalf("光标回开头后 offsetY = %d, want 0", ta.offsetY)
	}
	// 每行段宽 > 0 的自检 (per 被用在别处, 这里防止它退化成 0)
	if per < 2 {
		t.Fatalf("per = %d", per)
	}
}

func TestSoftWrapPaintsEveryVisualLine(t *testing.T) {
	requireFont(t)
	// 视觉行是**分别绘制**的 (整段按一条长文本画的话, 折出来的行会被裁掉)。
	// 断言: 每一条可见视觉行的横带里都有墨。
	long := strings.Repeat("a", 60) + "\n" + strings.Repeat("b", 60)
	root, ta := mkWrapped(t, long, 200, 8)
	img := renderTree(root, 400, 400)
	area := ta.taArea()
	lh := ta.taLineHeight()
	model := ta.taVisualModel()
	if len(model) < 3 {
		t.Fatalf("前提不成立: 视觉行数 = %d", len(model))
	}
	for i := 0; i < len(model) && i*lh < area.H; i++ {
		band := Rect{X: area.X, Y: area.Y + i*lh, W: area.W, H: lh}
		if n := countColor(img, band, pxWhite); n == band.W*band.H {
			t.Fatalf("第 %d 条视觉行没有绘制内容", i)
		}
	}
}

func TestSoftWrapEllipsisPropUnaffected(t *testing.T) {
	// 编辑框**不做省略号**: 它是可编辑的, 藏掉内容会让用户改不到。
	// 这里顺便钉住"ellipsis 只是 <text> 的 prop", 不会把 textarea 也裁掉。
	requireFont(t)
	root, ta := mkWrapped(t, strings.Repeat("a", 200), 200, 3)
	ta.Props["ellipsis"] = object.NewNumber(1)
	Layout(root, 400, 400)
	if got := ta.taVisualCount(); got < 2 {
		t.Fatalf("textarea 不该被 ellipsis 截断: 视觉行数 = %d", got)
	}
}
