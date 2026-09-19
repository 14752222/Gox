package gfx

import (
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// P2-6a 文本自动换行。
//
// 所有期望值都由 runeAdvance / runeWidth 现算, 不写死像素数字 ——
// 字体候选换一台机器就变, 写死会变成"只能在作者机器上过"的用例。

// mkTextBlock 造一个带 #text 子节点的 text 元素 (可选 wrap/ellipsis 由调用方补)。
func mkTextBlock(content string, size int) *GuiNode {
	n := mkNode("text", map[string]float64{"font": float64(size)})
	n.appendTextNode(content)
	return n
}

func TestWrapTextExplicitNewlines(t *testing.T) {
	// maxWidth <= 0 = 没有宽度约束: 只按 '\n' 切, 连续换行保留空行
	got := wrapText("a\nbb\n\nc", 16, 0, 0)
	want := []string{"a", "bb", "", "c"}
	if len(got) != len(want) {
		t.Fatalf("行数 = %d (%q), want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 行 = %q, want %q", i, got[i], want[i])
		}
	}
	// 空串也要得到"一行", 调用方不必再判空
	if lines := wrapText("", 16, 0, 0); len(lines) != 1 || lines[0] != "" {
		t.Fatalf("空文本应得到一行空串, got %q", lines)
	}
	// 末尾换行 = 多一个空行 (与 textarea 的行为一致)
	if lines := wrapText("a\n", 16, 0, 0); len(lines) != 2 || lines[1] != "" {
		t.Fatalf("末尾换行应得到空行, got %q", lines)
	}
}

func TestWrapTextGreedyLatin(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	lines := wrapText(strings.Repeat("a", 10), 16, 4*aw, 0)
	if len(lines) != 3 {
		t.Fatalf("10 个字符按 4 个一行应折成 3 行, got %d (%q)", len(lines), lines)
	}
	if lens := []int{len(lines[0]), len(lines[1]), len(lines[2])}; lens[0] != 4 || lens[1] != 4 || lens[2] != 2 {
		t.Fatalf("每行字符数 = %v, want [4 4 2]", lens)
	}
	// 每行宽度都不得超限
	for i, ln := range lines {
		if w := runeWidth(ln, 16); w > 4*aw {
			t.Fatalf("第 %d 行超宽: %d > %d", i, w, 4*aw)
		}
	}
}

func TestWrapTextGreedyCJK(t *testing.T) {
	requireFont(t)
	zw := runeAdvance(16, '中')
	// 10 个汉字, 每行放 3 个 → 3/3/3/1
	lines := wrapText("中中中中中中中中中中", 16, 3*zw, 0)
	if len(lines) != 4 {
		t.Fatalf("汉字折行数 = %d (%q), want 4", len(lines), lines)
	}
	if lines[0] != "中中中" || lines[3] != "中" {
		t.Fatalf("汉字折行结果不对: %q", lines)
	}
}

func TestWrapTextMixedWidth(t *testing.T) {
	requireFont(t)
	aw, zw := runeAdvance(16, 'a'), runeAdvance(16, '中')
	if zw <= aw {
		t.Skipf("当前字体汉字不比拉丁字母宽 (a=%d 中=%d), 混排用例无意义", aw, zw)
	}
	// 拉丁字母在比例字体里宽度各不相同 ('a' 9px 'b' 10px), 所以限宽要用
	// runeWidth 现算, 不能拿 runeAdvance('a') 当"一个字母宽"用两遍。
	two := runeWidth("aa", 16)
	lines := wrapText("中aa中", 16, zw+two, 0)
	if len(lines) != 2 {
		t.Fatalf("中英混排折行数 = %d (%q), want 2", len(lines), lines)
	}
	if lines[0] != "中aa" || lines[1] != "中" {
		t.Fatalf("中英混排折行结果不对: %q", lines)
	}
}

func TestWrapTextNarrowerThanOneRune(t *testing.T) {
	requireFont(t)
	// 限宽比一个汉字还窄: 每个字独占一行, 且必须能收敛 (内层不能空转)
	lines := wrapText("中中中", 16, 1, 0)
	if len(lines) != 3 {
		t.Fatalf("限宽 1px 时每字各占一行, got %d (%q)", len(lines), lines)
	}
}

func TestWrapTextEllipsis(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	src := strings.Repeat("a", 20)
	lines := wrapText(src, 16, 4*aw, 2)
	if len(lines) != 2 {
		t.Fatalf("ellipsis 限 2 行, got %d (%q)", len(lines), lines)
	}
	if !strings.HasSuffix(lines[1], ellipsisMark) {
		t.Fatalf("末行应补省略号: %q", lines[1])
	}
	if w := runeWidth(lines[1], 16); w > 4*aw {
		t.Fatalf("补省略号后末行超宽: %d > %d", w, 4*aw)
	}
	if len(lines[0]) != 4 {
		t.Fatalf("首行不该被裁: %q", lines[0])
	}
	// 放得下就不加省略号
	if lines := wrapText("ab", 16, 10*aw, 1); lines[0] != "ab" {
		t.Fatalf("内容放得下时不该补省略号: %q", lines[0])
	}
}

func TestMeasureTextMulti(t *testing.T) {
	requireFont(t)
	w, h := MeasureTextMulti("a\nb\nc", 16, 0, 0)
	if h != 3*lineHeight(16) {
		t.Fatalf("多行高 = %d, want %d", h, 3*lineHeight(16))
	}
	// 宽 = 最长行 (比例字体里 'b' 比 'a' 宽, 不能拿 "a" 的宽当答案)
	wantW := runeWidth("a", 16)
	for _, r := range "bc" {
		if rw := runeWidth(string(r), 16); rw > wantW {
			wantW = rw
		}
	}
	if w != wantW {
		t.Fatalf("多行宽 = 最长行宽 = %d, want %d", w, wantW)
	}
	// 与 wrapText 同口径: 截断后按截断结果量
	lines := wrapText(strings.Repeat("a", 20), 16, 4*runeAdvance(16, 'a'), 2)
	_, h2 := MeasureTextMulti(strings.Repeat("a", 20), 16, 4*runeAdvance(16, 'a'), 2)
	if h2 != len(lines)*lineHeight(16) {
		t.Fatalf("截断后高 = %d, want %d", h2, len(lines)*lineHeight(16))
	}
}

func TestTextBlockWrapWithExplicitWidth(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	root := mkNode("column", nil)
	block := mkTextBlock(strings.Repeat("a", 20), 16)
	withBool(block, "wrap", true)
	block.Props["width"] = object.NewNumber(float64(4 * aw))
	mountChildren(root, block)

	Layout(root, 400, 300)
	if block.Box.W != 4*aw {
		t.Fatalf("定宽文本块宽 = %d, want %d", block.Box.W, 4*aw)
	}
	if want := 5 * lineHeight(16); block.Box.H != want {
		t.Fatalf("20 字按 4 字一行 = 5 行, 高 = %d, want %d", block.Box.H, want)
	}
}

func TestTextBlockWrapStretchesInColumn(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	root := mkNode("column", map[string]float64{"padding": 10})
	block := mkTextBlock(strings.Repeat("a", 200), 16)
	withBool(block, "wrap", true)
	// 兄弟节点用来验证"折行后的高度参与了位置分配"
	below := mkNode("rect", map[string]float64{"height": 20})
	mountChildren(root, block, below)

	Layout(root, 400, 300)

	const areaW = 400 - 20
	if block.Box.W != areaW {
		t.Fatalf("开 wrap 的文本块应铺满内容区: 宽 = %d, want %d", block.Box.W, areaW)
	}
	perLine := areaW / aw
	wantLines := (200 + perLine - 1) / perLine
	if want := wantLines * lineHeight(16); block.Box.H != want {
		t.Fatalf("折行高 = %d, want %d (%d 行)", block.Box.H, want, wantLines)
	}
	if below.Box.Y != 10+block.Box.H {
		t.Fatalf("兄弟节点未按折行高度下移: y = %d, want %d", below.Box.Y, 10+block.Box.H)
	}
}

func TestTextBlockUnwrappedKeepsSingleLine(t *testing.T) {
	requireFont(t)
	root := mkNode("column", map[string]float64{"padding": 10})
	block := mkTextBlock(strings.Repeat("a", 200), 16) // 没开 wrap
	mountChildren(root, block)
	Layout(root, 400, 300)

	if block.Box.H != lineHeight(16) {
		t.Fatalf("未开 wrap 的文本应保持单行: 高 = %d", block.Box.H)
	}
	if block.Box.W != runeWidth(strings.Repeat("a", 200), 16) {
		t.Fatalf("未开 wrap 的文本不该被拉伸: 宽 = %d", block.Box.W)
	}
}

func TestTextBlockEllipsisProp(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	root := mkNode("column", nil)
	block := mkTextBlock(strings.Repeat("a", 40), 16)
	block.Props["ellipsis"] = object.NewNumber(2) // 隐含 wrap, 只留 2 行
	block.Props["width"] = object.NewNumber(float64(4 * aw))
	mountChildren(root, block)

	Layout(root, 400, 300)
	if want := 2 * lineHeight(16); block.Box.H != want {
		t.Fatalf("ellipsis 限 2 行时高 = %d, want %d", block.Box.H, want)
	}
	lines := block.blockLines(block.Box.W)
	if len(lines) != 2 || !strings.HasSuffix(lines[1], ellipsisMark) {
		t.Fatalf("ellipsis 行内容不对: %q", lines)
	}
}

func TestTextBlockWrapDrawsEveryLine(t *testing.T) {
	requireFont(t)
	aw := runeAdvance(16, 'a')
	root := mkNode("column", nil)
	block := mkTextBlock(strings.Repeat("a", 20), 16)
	withBool(block, "wrap", true)
	block.Props["width"] = object.NewNumber(float64(4 * aw))
	mountChildren(root, block)

	img := renderTree(root, 200, 200)
	lh := lineHeight(16)
	if n := countColor(img, Rect{0, 0, 200, 200}, pxWhite); n == 200*200 {
		t.Fatalf("wrap 文本一个像素都没画")
	}
	// 5 行: 每一行的横带里都要有非白像素 (整行漏画是最容易犯的错)
	for i := 0; i < 5; i++ {
		band := Rect{X: 0, Y: i * lh, W: 4 * aw, H: lh}
		if n := countColor(img, band, pxWhite); n == band.W*band.H {
			t.Fatalf("第 %d 行 (y %d..%d) 没有绘制内容", i, band.Y, band.Y+band.H)
		}
	}
}

func TestWrapsTextPropMatrix(t *testing.T) {
	cases := []struct {
		name  string
		props func(*GuiNode)
		want  bool
	}{
		{"无 prop", func(*GuiNode) {}, false},
		{"wrap=false", func(n *GuiNode) { withBool(n, "wrap", false) }, false},
		{"wrap=true", func(n *GuiNode) { withBool(n, "wrap", true) }, true},
		{"ellipsis=true", func(n *GuiNode) { withBool(n, "ellipsis", true) }, true},
		{"ellipsis=3", func(n *GuiNode) { n.Props["ellipsis"] = object.NewNumber(3) }, true},
		{"ellipsis=0", func(n *GuiNode) { n.Props["ellipsis"] = object.NewNumber(0) }, false},
	}
	for _, tc := range cases {
		n := mkTextBlock("hi", 16)
		tc.props(n)
		if got := n.wrapsText(); got != tc.want {
			t.Fatalf("%s: wrapsText = %v, want %v", tc.name, got, tc.want)
		}
	}
	// #text 文本节点永远单行 (没有地方挂 prop)
	raw := &GuiNode{Tag: "#text", Text: "hi", Props: map[string]object.Value{}}
	withBool(raw, "wrap", true)
	if raw.wrapsText() {
		t.Fatalf("#text 不该支持 wrap")
	}
}
