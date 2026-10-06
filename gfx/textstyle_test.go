package gfx

// ===== 文本样式轴: 字体族 / 粗斜体 / 行高 / 字距 (§四 文本域缺口) =====
//
// 测法分三层, 各测各的:
//
//   1. **解析层** (纯数据): prop → TextStyle 的继承与归一, 不碰字体文件;
//   2. **度量层**: 样式对宽度/高度的影响 —— 期望值一律用同一条公式现算,
//      不写死像素 (字体候选换台机器就变, 写死会变成"只在作者机器上过");
//   3. **绘制层**: 合成粗体/斜体的像素级行为 —— 用一个**手工构造的掩码**
//      直接调 blitGlyph, 于是结果与装了什么字体完全无关。
//      (例外: TestDrawTextStyledDiffersPerAxis 走真实基础字体 —— 它断言的是
//      "粗细/斜体必须看得出来"这条产品要求; 判据与字体无关, 但像素值会随字体变。)
//
// 另外两条"不依赖装了什么字体"的强断言:
//   - 泛型族 "monospace" 必须真的等宽 (iii 与 WWW 同宽);
//   - 不认识的族名必须与默认字体**度量完全一致** (静默降级到默认)。

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
	"golang.org/x/image/font/sfnt"
)

// ===== 解析层 =====

// TestFontIndexIsLazyUntilFamilyUsed 必须排在**本文件最前** (用例按源码顺序
// 执行): 一旦有别的用例用过 fontFamily, 索引就已经建好了, 这条断言就只能在
// 别处跳过了。
//
// 为什么值得单独立一条: 建索引要遍历字体目录 (本机实测 ~300ms)。若默认样式
// 就会触发它, "惰性"就成了一句空话 —— 每个用 GUI 的应用都要白付这笔钱。
func TestFontIndexIsLazyUntilFamilyUsed(t *testing.T) {
	familyIdx.mu.Lock()
	wasBuilt := familyIdx.built
	familyIdx.mu.Unlock()
	if wasBuilt {
		t.Skip("同一次进程里已有别的用例建过索引, 无法验证惰性")
	}
	requireFont(t)
	MeasureTextStyled("plain", TextStyle{Size: 16})
	if familyIdx.built {
		t.Fatalf("默认样式不该触发字体族索引")
	}
	MeasureTextStyled("plain", TextStyle{Size: 16, Family: "monospace"})
	if !familyIdx.built {
		t.Fatalf("用了 fontFamily 之后应已建索引")
	}
}

func TestResolveTextStyleInheritance(t *testing.T) {
	// 三层链: column(fontFamily/lineHeight) > text(font/fontWeight) > #text
	leaf := &GuiNode{Tag: "#text", Text: "x", Props: map[string]object.Value{}}
	mid := mkNode("text", map[string]float64{"font": 20})
	mid.Props["fontWeight"] = object.NewString("bold")
	mid.Children = []*GuiNode{leaf}
	leaf.Parent = mid
	root := withNum(mkNode("column", nil), "lineHeight", 30)
	root.Props["fontFamily"] = object.NewString("monospace")
	root.Children = []*GuiNode{mid}
	mid.Parent = root

	st := resolveTextStyle(leaf)
	if st.Size != 20 {
		t.Fatalf("字号 = %d, want 20 (中间层)", st.Size)
	}
	if st.Family != "monospace" {
		t.Fatalf("族名 = %q, want monospace (继承自根)", st.Family)
	}
	if !st.Bold {
		t.Fatalf("粗体 = false, want true (继承自中间层)")
	}
	if st.LineH != 30 {
		t.Fatalf("行高 = %d, want 30 (继承自根)", st.LineH)
	}
	// 就近优先: 叶子自己写了的赢。
	withNum(leaf, "letterSpacing", 3)
	if got := resolveTextStyle(leaf).LetterSp; got != 3 {
		t.Fatalf("字距 = %d, want 3", got)
	}
}

func TestResolveTextStyleNormalTurnsOffInheritedBold(t *testing.T) {
	// "在粗体标题里让一个词正常" 是最普通的用法, 必须做得到 ——
	// 也就是 fontWeight 的**反关键字**要能主动关掉祖先的粗体 (而不是"没写")。
	leaf := &GuiNode{Tag: "#text", Text: "x", Props: map[string]object.Value{}}
	parent := mkNode("text", nil)
	parent.Props["fontWeight"] = object.NewString("bold")
	parent.Children = []*GuiNode{leaf}
	leaf.Parent = parent
	leaf.Props["fontWeight"] = object.NewString("normal")

	if st := resolveTextStyle(leaf); st.Bold {
		t.Fatalf("fontWeight=\"normal\" 应关掉祖先的粗体")
	}
}

func TestBoldPropParsing(t *testing.T) {
	cases := []struct {
		val  object.Value
		bold bool
	}{
		{object.NewString("bold"), true},
		{object.NewString("Bold"), true},
		{object.NewString(" bolder "), true},
		{object.NewString("semibold"), true},
		{object.NewString("black"), true},
		{object.NewString("700"), true},
		{object.NewNumber(600), true},
		{object.NewNumber(900), true},
		{object.NewNumber(500), false},
		{object.NewNumber(400), false},
		{object.NewString("normal"), false},
		{object.NewString("light"), false},
		{object.NewString("300"), false},
		{object.NewString("wat"), false}, // 不认识 = 不粗 (不是抛错)
	}
	for _, c := range cases {
		n := &GuiNode{Tag: "text", Props: map[string]object.Value{"fontWeight": c.val}}
		got, ok := n.boldProp()
		if !ok {
			t.Fatalf("fontWeight=%v 应被认作「写过」", c.val.Inspect())
		}
		if got != c.bold {
			t.Fatalf("fontWeight=%s → bold=%v, want %v", c.val.Inspect(), got, c.bold)
		}
	}
	if _, ok := (&GuiNode{Tag: "text", Props: map[string]object.Value{}}).boldProp(); ok {
		t.Fatalf("没写 fontWeight 时 ok 应为 false (不能覆盖祖先)")
	}
}

func TestItalicPropParsing(t *testing.T) {
	cases := []struct {
		val    string
		italic bool
	}{
		{"italic", true}, {"Italic", true}, {"oblique", true},
		{"normal", false}, {"wat", false},
	}
	for _, c := range cases {
		n := &GuiNode{Tag: "text", Props: map[string]object.Value{"fontStyle": object.NewString(c.val)}}
		got, ok := n.italicProp()
		if !ok || got != c.italic {
			t.Fatalf("fontStyle=%q → (%v,%v), want (%v,true)", c.val, got, ok, c.italic)
		}
	}
	if _, ok := (&GuiNode{Tag: "text", Props: map[string]object.Value{}}).italicProp(); ok {
		t.Fatalf("没写 fontStyle 时 ok 应为 false")
	}
}

func TestNormalizeAndStripFamilyHelpers(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  PingFang   SC ", "pingfang sc"},
		{`"Menlo"`, "menlo"},
		{"", ""},
		{"DejaVu Sans", "dejavu sans"},
	}
	for _, c := range cases {
		if got := normalizeFamilyKey(c.in); got != c.want {
			t.Fatalf("normalizeFamilyKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := compactFamilyKey("dejavu sans"); got != "dejavusans" {
		t.Fatalf("compactFamilyKey = %q", got)
	}
	// stripStyleSuffix 只负责剥后缀, **不做大小写归一** (那是
	// normalizeFamilyKey 的活) —— 保持"原样"才能让调用方把结果原样喂给别名表。
	stemCases := []struct{ in, want string }{
		{"DejaVuSans-Bold", "DejaVuSans"},
		{"Arial Bold Italic", "Arial"},
		{"menloi", "menlo"},
		{"Helvetica", "Helvetica"},
		{"Times New Roman", "Times New Roman"},
		{"NotoSansCJK-Regular", "NotoSansCJK"},
	}
	for _, c := range stemCases {
		if got := stripStyleSuffix(c.in); got != c.want {
			t.Fatalf("stripStyleSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAppleWeightSubfamilyDetection(t *testing.T) {
	// 苹果 CJK 字体把字重写成 W3/W6, 完全不含 bold 字样 (冬青黑体实测)。
	// 不认它的话"中文加粗"在苹果设备上会整体退化成合成粗体。
	for _, s := range []string{"W6", "w9", "W7"} {
		if !appleWeightBold(s) {
			t.Fatalf("%q 应认作粗体", s)
		}
	}
	for _, s := range []string{"W3", "W0", "Regular", "W", "Wx", "W66a"} {
		if appleWeightBold(s) {
			t.Fatalf("%q 不该认作粗体", s)
		}
	}
}

// ===== 度量层 =====

func TestMeasureTextStyledLineHeight(t *testing.T) {
	requireFont(t)
	// 显式行高直接生效; 不写时与 v1 的公式一致 (字号 + 字号/4)。
	if _, h := MeasureTextStyled("x", TextStyle{Size: 16, LineH: 40}); h != 40 {
		t.Fatalf("显式行高 = %d, want 40", h)
	}
	if _, h := MeasureTextStyled("x", TextStyle{Size: 16}); h != 16+16/4 {
		t.Fatalf("自动行高 = %d, want %d", h, 16+16/4)
	}
	// 多行: 高 = 行数 × 行高 (走 wrap 口径, 与绘制端共用)
	_, h := MeasureTextMultiStyled("a\nb\nc", TextStyle{Size: 16, LineH: 25}, 0, 0)
	if h != 75 {
		t.Fatalf("三行 × 25 = %d, want 75", h)
	}
}

func TestLetterSpacingWidensAndWraps(t *testing.T) {
	requireFont(t)
	base := TextStyle{Size: 16}
	spaced := TextStyle{Size: 16, LetterSp: 5}
	w0, _ := MeasureTextStyled("abc", base)
	w1, _ := MeasureTextStyled("abc", spaced)
	// 字距加在**每个**字符之后: 3 个字符 → 恰好宽 15。
	if w1-w0 != 15 {
		t.Fatalf("字距增量 = %d, want 15", w1-w0)
	}
	// 字距参与换行判定: 宽到装不下时必须折行。
	lines := wrapTextStyled("aaaa", spaced, w0+5, 0)
	if len(lines) < 2 {
		t.Fatalf("加了字距之后应折成多行, got %q", lines)
	}
	// 负字距 (紧凑排版) 也要能变小。
	narrow := TextStyle{Size: 16, LetterSp: -2}
	w2, _ := MeasureTextStyled("abc", narrow)
	if w2 >= w0 {
		t.Fatalf("负字距应变窄: %d vs %d", w2, w0)
	}
}

func TestGenericMonospaceIsActuallyMonospaced(t *testing.T) {
	requireFont(t)
	mono := TextStyle{Size: 16, Family: "monospace"}
	wi, _ := MeasureTextStyled("iii", mono)
	wW, _ := MeasureTextStyled("WWW", mono)
	if wi != wW {
		t.Skipf("本机没找到等宽族 (iii=%d WWW=%d), 跳过", wi, wW)
	}
	// 等宽成立 ⇒ 与默认 (比例) 字体量出的结果必然不同: 否则说明
	// "monospace" 根本没生效 (退化成了默认字体)。
	di, _ := MeasureTextStyled("iii", TextStyle{Size: 16})
	dW, _ := MeasureTextStyled("WWW", TextStyle{Size: 16})
	if di == wi && dW == wW {
		t.Fatalf("monospace 与默认字体度量完全一致, 泛型族没生效")
	}
}

func TestUnknownFamilyFallsBackToDefaultMetrics(t *testing.T) {
	requireFont(t)
	// 拼错的族名不该报错, 也不该改变度量 —— 静默降级到默认字体
	// (一个笔误让整屏文字不渲染是最坏的结果)。
	a, ha := MeasureTextStyled("Hello 你好", TextStyle{Size: 18, Family: "NoSuchFont-Xyzzy"})
	b, hb := MeasureTextStyled("Hello 你好", TextStyle{Size: 18})
	if a != b || ha != hb {
		t.Fatalf("未知族名应退化成默认字体: %dx%d vs %dx%d", a, ha, b, hb)
	}
	// 带粗体的未知族名仍然"更重"(合成), 但度量不该崩。
	if w, h := MeasureTextStyled("Hello", TextStyle{Size: 18, Family: "NoSuchFont-Xyzzy", Bold: true}); w <= 0 || h <= 0 {
		t.Fatalf("未知族名 + 粗体应仍有正常度量, got %dx%d", w, h)
	}
}

// ===== 绘制层 =====

func TestBlitGlyphSyntheticBoldAndItalic(t *testing.T) {
	// 手工构造一个 2x2 掩码: 结果是确定的, 与装了什么字体无关。
	mk := func() *glyphEntry {
		m := image.NewAlpha(image.Rect(0, 0, 2, 2))
		m.SetAlpha(0, 0, color.Alpha{A: 255})
		m.SetAlpha(1, 1, color.Alpha{A: 255})
		return &glyphEntry{mask: m, advance: 4}
	}
	// 底必须是**白**的: blitMask 是 src-over 混合且只写 RGB (alpha 交给上层
	// 合成), 所以在全零的 RGBA 上"墨"与"背景"都是 R=0, 数不出墨点。
	// 这不是瑕疵而是刻意 —— 绘制层只保证颜色通道正确, 不掺和整体透明度。
	blank := func() *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 20, 20))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		return img
	}
	ink := func(img *image.RGBA) []image.Point {
		var pts []image.Point
		for y := 0; y < 20; y++ {
			for x := 0; x < 20; x++ {
				if img.RGBAAt(x, y).R < 200 {
					pts = append(pts, image.Point{x, y})
				}
			}
		}
		return pts
	}

	// 1) 普通: 掩码原地落下 (dx=5, dy=5, baseY=15 → 基线在 15)
	img := blank()
	blitGlyph(img, img.Bounds(), mk(), 5, 5, 15, color.RGBA{A: 255})
	if got := ink(img); len(got) != 2 {
		t.Fatalf("普通绘制应有 2 个墨点, got %v", got)
	}

	// 2) 合成粗体: 同一份掩码往右再压一遍 ⇒ 每个墨点右边多一个。
	img = blank()
	e := mk()
	e.synthB = true
	blitGlyph(img, img.Bounds(), e, 5, 5, 15, color.RGBA{A: 255})
	if got := ink(img); len(got) != 4 {
		t.Fatalf("合成粗体应有 4 个墨点 (横向涂抹), got %v", got)
	}

	// 3) 合成斜体: 绕**基线**剪切 —— 离基线越远右移越多, 基线及以下不动。
	//    基线在 15, 两个墨点的基准位置是 (5,5) 与 (6,6):
	//      y=5 (距基线 10) → shift = 10*21/100 = 2 → x = 5+2 = 7
	//      y=6 (距基线  9) → shift =  9*21/100 = 1 → x = 6+1 = 7
	//    两行因此在上沿对齐成斜线 (越往上越靠右)。
	img = blank()
	e = mk()
	e.synthI = true
	blitGlyph(img, img.Bounds(), e, 5, 5, 15, color.RGBA{A: 255})
	got := ink(img)
	want := map[image.Point]bool{{7, 5}: true, {7, 6}: true}
	if len(got) != 2 || !want[got[0]] || !want[got[1]] {
		t.Fatalf("合成斜体贴图像素 = %v, want [(7,5) (7,6)]", got)
	}
	// 剪切方向必须是"越靠上越往右" (反了整段斜体看起来朝左倒)。
	// 两个墨点的原始横坐标是 dx 与 dx+1, 所以剪切量 = 落点 x - 原始 x。
	if shTop, shBot := got[0].X-5, got[1].X-6; shTop <= shBot {
		t.Fatalf("剪切方向不对: 上部剪 %dpx, 下部剪 %dpx (应上大下小)", shTop, shBot)
	}
}

func TestGlyphCacheSeparatesStyleAxis(t *testing.T) {
	requireFont(t)
	reg, err := glyphFor(TextStyle{Size: 16}, 'A')
	if err != nil {
		t.Fatalf("glyphFor: %v", err)
	}
	bold, err := glyphFor(TextStyle{Size: 16, Bold: true}, 'A')
	if err != nil {
		t.Fatalf("glyphFor(bold): %v", err)
	}
	// 键里必须含样式轴: 否则真粗体与合成粗体会共用同一份掩码,
	// 症状是"设了 bold 有时生效有时不生效", 随渲染顺序漂移。
	if reg == bold {
		t.Fatalf("粗体与正体不该共用同一个缓存条目")
	}
	// 同一请求重复取必须命中同一条目 (缓存真的在工作)。
	again, _ := glyphFor(TextStyle{Size: 16, Bold: true}, 'A')
	if again != bold {
		t.Fatalf("同样的请求应命中同一缓存条目")
	}
}

func TestSyntheticAxesReachTheBlitLayer(t *testing.T) {
	requireFont(t)
	// 这是一个很隐蔽的接缝: 合成标记存在 resolvedFace 上, 而 blitGlyph 只看
	// glyphEntry —— 忘了往下传的症状是"设了 bold 完全没反应"(静默)。
	// 所以这里断言 entry 上的标记与 face 解析结果一致。
	rf, err := faceForStyle(TextStyle{Size: 16, Family: "NoSuchFont-Xyzzy", Bold: true, Italic: true})
	if err != nil {
		t.Fatalf("faceForStyle: %v", err)
	}
	if !rf.synthB || !rf.synthI {
		t.Fatalf("未知族名 + 粗斜体应走合成路径: synthB=%v synthI=%v", rf.synthB, rf.synthI)
	}
	e, err := glyphFor(TextStyle{Size: 16, Family: "NoSuchFont-Xyzzy", Bold: true, Italic: true}, 'A')
	if err != nil {
		t.Fatalf("glyphFor: %v", err)
	}
	if e.mask != nil && (!e.synthB || !e.synthI) {
		t.Fatalf("合成标记没传到 glyphEntry: synthB=%v synthI=%v", e.synthB, e.synthI)
	}
}

func TestDrawTextStyledDiffersPerAxis(t *testing.T) {
	requireFont(t)
	draw := func(st TextStyle) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, 160, 40))
		for i := range img.Pix {
			img.Pix[i] = 255
		}
		DrawTextStyled(img, img.Bounds(), "Hamburgefonstiv", 2, 2, st, color.RGBA{A: 255}, 0)
		return img
	}
	// 像素比较必须用 bytes.Equal —— strings.EqualFold 对**二进制**数据是错的:
	// 它按 rune 解码, 而非法 UTF-8 的字节一律解成 RuneError, 于是"两个不同的非法
	// 字节"会被判成相等 (0x80-0xBF 的续字节与 0xFF 都非法)。抗锯齿的中间色大量
	// 落在这些值上, 所以这个判据会随字体/字号偶然地"看不见差异"。
	same := func(a, b *image.RGBA) bool {
		return bytes.Equal(a.Pix, b.Pix)
	}
	reg := draw(TextStyle{Size: 18})
	bold := draw(TextStyle{Size: 18, Bold: true})
	ital := draw(TextStyle{Size: 18, Italic: true})

	if same(reg, bold) {
		t.Logf("字体样式探针: %s", fontStyleProbe())
		// 带上现场: "粗体等于正体"有两种完全不同的成因, 只看墨量分不出来 ——
		//   ① 面选对了但合成没生效 (synth 为假且槽位不同);
		//   ② 缺字形 ⇒ 两次都是空掩码 (mask=nil), 自然逐像素相同。
		// 把槽位 / synth / isBase / 掩码 / 墨量一次打全, 免得为取一条诊断再推一次 CI。
		r0, _ := faceForStyle(TextStyle{Size: 18})
		r1, _ := faceForStyle(TextStyle{Size: 18, Bold: true})
		g0, _ := rasterizeGlyph(r0, 18, 'H')
		g1, _ := rasterizeGlyph(r1, 18, 'H')
		maskDesc := func(g *glyphEntry) string {
			switch {
			case g == nil:
				return "条目=nil"
			case g.mask == nil:
				return "掩码=nil(缺字形)"
			default:
				return fmt.Sprintf("掩码=%v", g.mask.Bounds())
			}
		}
		t.Fatalf("粗体与正体的像素应不同 (真变体或合成都必须看得出来); 基础字体族=%q｜"+
			"正体{槽=%d synthB=%v synthI=%v base=%v 墨量=%d 'H'%s} "+
			"粗体{槽=%d synthB=%v synthI=%v base=%v 墨量=%d 'H'%s}",
			baseFamilyKey(),
			r0.id, r0.synthB, r0.synthI, r0.isBase, inkOf(reg), maskDesc(g0),
			r1.id, r1.synthB, r1.synthI, r1.isBase, inkOf(bold), maskDesc(g1))
	}
	if same(reg, ital) {
		t.Fatalf("斜体与正体的像素应不同 (合成斜体也要改变落笔位置)")
	}
	// 粗体的墨量不得少于正体 (真粗体笔画更宽, 合成粗体是叠加)。
	if inkOf(bold) < inkOf(reg) {
		t.Fatalf("粗体墨量 %d 少于正体 %d", inkOf(bold), inkOf(reg))
	}
}

// inkOf 数一张图上"有墨"的像素 (R < 250 即认为落了笔)。
func inkOf(img *image.RGBA) int {
	n := 0
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if img.RGBAAt(x, y).R < 250 {
				n++
			}
		}
	}
	return n
}

// ===== 端到端: prop → 布局 + 绘制 =====

func TestTextStylePropsFlowIntoLayout(t *testing.T) {
	requireFont(t)

	// 文本块必须**挂在 column 里**再 Layout: 直接把它当根节点会被拉伸到
	// 视口尺寸 (根节点铺满), 量出来的宽高是视口的而不是内容的 —— 那样
	// 无论样式有没有生效, 断言都会看到同一个数。
	mount := func(block *GuiNode) *GuiNode {
		root := mkNode("column", nil)
		mountChildren(root, block)
		return root
	}

	// 1) lineHeight 改变盒子高度: 单行文本块的高就是行高
	block := mkTextBlock("hello", 16)
	root := mount(block)
	Layout(root, 400, 300)
	if h0 := block.Box.H; h0 != lineHeight(16) {
		t.Fatalf("默认行高的文本块高 = %d, want %d", h0, lineHeight(16))
	}
	withNum(block, "lineHeight", 40)
	Layout(root, 400, 300)
	if h1 := block.Box.H; h1 != 40 {
		t.Fatalf("lineHeight=40 的文本块高 = %d, want 40", h1)
	}

	// 2) letterSpacing 改变盒子宽度 (字距加在**每个**字符之后: 5 字符 → 宽 30)
	plain := mkTextBlock("hello", 16)
	root2 := mount(plain)
	Layout(root2, 400, 300)
	w0 := plain.Box.W
	if w0 != runeWidth("hello", 16) {
		t.Fatalf("未加字距的文本块宽 = %d, want %d", w0, runeWidth("hello", 16))
	}
	withNum(plain, "letterSpacing", 6)
	Layout(root2, 400, 300)
	if w1 := plain.Box.W; w1 != w0+5*6 {
		t.Fatalf("letterSpacing=6 时宽 = %d, want %d (原 %d)", w1, w0+5*6, w0)
	}

	// 3) 字体族改变度量: 走布局面量出来的宽必须跟着变, 而不只是 runeWidth。
	//    断言"等宽族下 iii 与 WWW 同宽" —— 这条不依赖本机装了什么字体:
	//    若 monospace 没接上, 比例字体里 iii 与 WWW 必然不同宽。
	measure := func(family, text string) int {
		b := mkTextBlock(text, 16)
		if family != "" {
			withStr(b, "fontFamily", family)
		}
		Layout(mount(b), 400, 300)
		return b.Box.W
	}
	monoI, monoW := measure("monospace", "iii"), measure("monospace", "WWW")
	if monoI != monoW {
		t.Fatalf("泛型 monospace 在布局面没生效: iii=%d WWW=%d", monoI, monoW)
	}
	// 顺带证明它和默认字体不是同一个东西, 否则上面那条可能只是"恰好都相等"
	if measure("", "iii") == monoI && measure("", "WWW") == monoW {
		t.Logf("本机默认字体恰好等宽 (%d/%d), 泛型族断言退化为弱断言", monoI, monoW)
	}
}

func TestTextStyleInheritedByTextNode(t *testing.T) {
	requireFont(t)
	// 绘制路径: 祖先把样式传下去, #text 自己什么都不挂也要吃到。
	// 断言"画出来的像素不同" —— 这比读 struct 更能证明链路真的通了。
	build := func(family string) *GuiNode {
		block := mkTextBlock("IIIIII", 16)
		if family != "" {
			withStr(block, "fontFamily", family)
		}
		Layout(block, 400, 400)
		return block
	}
	def := renderTree(build(""), 400, 60)
	mono := renderTree(build("monospace"), 400, 60)
	if def == nil || mono == nil {
		t.Fatalf("渲染失败")
	}
	if bytes.Equal(def.Pix, mono.Pix) {
		t.Fatalf("fontFamily 没有影响到 #text 的绘制")
	}
}

// b2i 把 bool 打成 0/1 —— 探针行要塞进 CI 注解 1200 字符的预算里, 每个字段都得省。
func b2i(v bool) int {
	if v {
		return 1
	}
	return 0
}

// fontStyleProbe 把"一次样式请求在族索引里落到哪个文件的第几个面"打成一行。
//
// TestDrawTextStyledDiffersPerAxis 失败时这是关键事实: 粗体与正体**逐像素相同**,
// 在索引层面只有两种可能 —— ① 该族注册了 {bold:true}, 但它指的面其实就是正体
// (查表命中 ⇒ **不触发合成**); ② 该族没有 {bold:true}, 本该退成"默认字体 + 合成",
// 而合成标记没生效。
// 只看渲染结果分不出这两者 (一个是"选错面", 一个是"标记丢了")。所以把查表结果
// 连同命中面的**子族名**一并报出来 —— 子族名与它被登记的轴标不一致, 就是
// "索引把正体标成了粗体"的直接证据。
func fontStyleProbe() string {
	baseKey := baseFamilyKey() // 必须在取 ix.mu 之前算: 它自己要取 fontMu
	familyIdx.build()

	familyIdx.mu.Lock()
	axes := []styleAxis{}
	srcs := []faceSrc{}
	if m, ok := familyIdx.files[baseKey]; ok {
		for a := range m {
			axes = append(axes, a)
		}
		sort.Slice(axes, func(i, j int) bool {
			if axes[i].bold != axes[j].bold {
				return !axes[i].bold // 正体在前, 便于与"命中面"对照
			}
			return !axes[i].italic
		})
		for _, a := range axes {
			srcs = append(srcs, m[a])
		}
	}
	type stylePick struct {
		want  styleAxis
		src   faceSrc
		synth styleAxis
		ok    bool
	}
	wants := []styleAxis{{bold: true}, {italic: true}, {}}
	picks := make([]stylePick, 0, len(wants))
	for _, w := range wants {
		src, synth, ok := familyIdx.pickLocked(baseKey, w)
		picks = append(picks, stylePick{want: w, src: src, synth: synth, ok: ok})
	}
	total := len(familyIdx.files)
	familyIdx.mu.Unlock()

	// 面的子族名要**出了锁再读**: fontFromSrc 自己取 fontSrcMu, 而
	// resetFontCaches 是 fontSrcMu → ix.mu 的顺序取锁, 持 ix.mu 再取
	// fontSrcMu 会成环。
	var buf sfnt.Buffer
	desc := func(src faceSrc) string {
		f, err := fontFromSrc(src)
		if err != nil {
			return fmt.Sprintf("%s#%d(读不出)", filepath.Base(src.path), src.index)
		}
		return fmt.Sprintf("%s#%d(子族=%q)", filepath.Base(src.path), src.index,
			fontNameOf(f, &buf, sfnt.NameIDSubfamily))
	}

	var b strings.Builder
	fmt.Fprintf(&b, "默认族=%q", baseKey)
	// 默认字体自己是哪个面? 这一条是谜题的关键: 若候选表把**粗体文件**排在了正体
	// 之前 (Linux 目录序里 NotoSansCJK-Bold.ttc 就在 NotoSansCJK-Regular.ttc 前), 那么
	// "无样式"请求画的其实是粗体面, 而"要粗体"请求经索引也落到同一个面 ⇒ 两张图
	// 逐像素相同, 且两个文件的族名一样 (都叫 "Noto Sans CJK JP"), 从渲染结果上根本
	// 区分不出来。所以必须把默认字体自己的**子族名**报出来。
	if bf, err := loadBaseFont(); err == nil {
		sub := fontNameOf(bf, &buf, sfnt.NameIDSubfamily)
		a := axisFromFont(bf, sub)
		initFontCandidates()
		names := make([]string, 0, 3)
		for i, p := range fontCandidates {
			if i >= 3 {
				break
			}
			names = append(names, filepath.Base(p))
		}
		fmt.Fprintf(&b, " 默认字体 子族=%q→轴{b=%d i=%d} 候选前3=[%s]",
			sub, b2i(a.bold), b2i(a.italic), strings.Join(names, ","))
	}
	if len(srcs) == 0 {
		fmt.Fprintf(&b, " 该族**不在索引里** (索引共 %d 个族)", total)
	} else {
		items := make([]string, 0, len(srcs))
		for i, a := range axes {
			items = append(items, fmt.Sprintf("{b=%d i=%d}→%s",
				b2i(a.bold), b2i(a.italic), desc(srcs[i])))
		}
		fmt.Fprintf(&b, " 注册轴=%d[%s]", len(axes), strings.Join(items, " "))
	}
	for _, p := range picks {
		if !p.ok {
			fmt.Fprintf(&b, " ｜问{b=%d i=%d}=未命中", b2i(p.want.bold), b2i(p.want.italic))
			continue
		}
		fmt.Fprintf(&b, " ｜问{b=%d i=%d}→%s 合成{b=%d i=%d}",
			b2i(p.want.bold), b2i(p.want.italic), desc(p.src),
			b2i(p.synth.bold), b2i(p.synth.italic))
	}
	return b.String()
}
