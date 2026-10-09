package gfx

// ===== 文本样式轴 (§四 文本域缺口) =====
//
// v1 只有 `font` (字号) 一根轴, 于是"标题/正文/代码/强调"这些最基础的排版
// 层级在 Gox 里根本表达不出来 —— 这也是富文本组件一直做不下去的根因: 没有
// 段落与行内样式之分, 就没法定义"选中一段后加粗"。
//
// 这次补齐的五根轴, 与 CSS 同名同义:
//
//	font        → Size      字号 (像素, 已有)
//	fontFamily  → Family    字体族, 含泛型名 (monospace / serif …)
//	fontWeight  → Bold      "bold" / 600 / "normal"
//	fontStyle   → Italic    "italic" / "oblique" / "normal"
//	lineHeight  → LineH     行高 (像素; 0 = 自动 = 字号 + 字号/4)
//	letterSpacing → LetterSp 字距 (像素, 可为负)
//
// ## 为什么全部**沿父链继承**
//
// 与 FontSize 同一条理由, 而且更强烈: `#text` 节点是 JSX 字符串的隐式形式,
// **没有任何地方挂 prop** —— 不继承就等于"只有包着它的元素能设置字体", 而
// 那恰恰是唯一想设置的地方。同理, `<column fontFamily="monospace">` 覆盖
// 整块区域是所有排版工具的常识行为。
//
// 继承口径统一为"最近祖先优先": 任何一层都能覆盖, 内层赢了就不再往外找。
//
// ## 为什么量测与绘制必须共用同一套
//
// 行高与字距都会改变**内容尺寸**, 而尺寸参与兄弟节点的位置分配。量测端
// (layout.intrinsicSize / textblock) 与绘制端 (raster.drawNode) 只要有一处
// 用了自己的算法, 就会得到"盒子按 16px 算、文字按 18px 画"的错位 —— 而且
// 只在特定 prop 组合下才出现。所以两处都走 resolveTextStyle + 同一组
// Styled 函数, 不留第二套算法。

import (
	"image"
	"image/color"
	"strconv"
	"strings"

	"github.com/14752222/Gox/object"
)

// TextStyle 是一个文本节点最终生效的排版参数 (已沿父链解析完)。
type TextStyle struct {
	Size     int    // 字号 (px)
	Family   string // 字体族 ("" = 默认字体; 支持泛型名与路径)
	Bold     bool   // 粗体 (无真实变体时合成)
	Italic   bool   // 斜体 (无真实变体时合成)
	LineH    int    // 行高 (px); 0 = 自动
	LetterSp int    // 字距 (px, 加在每个字符之后)
}

// resolveTextStyle 沿父链解析出节点生效的文本样式 (最近祖先优先)。
//
// 起点是节点自己: `#text` 自己通常什么都没有, 于是整条链都会走一遍; 而
// `<text fontFamily="…" font={20}>` 这种自给自足的节点只走一步。
func resolveTextStyle(n *GuiNode) TextStyle {
	st := TextStyle{Size: n.FontSize()}
	if n == nil {
		return st
	}
	// 每根轴单独记"定没定", 不能用 st.Bold / st.Italic 代替 —— 布尔轴的
	// **有效值 false 就是一次合法赋值**: 一旦有人写了 fontWeight="normal",
	// 它就赢了祖先的 bold, 而"值恰好是 false"不能反过来被当成"还没写"。
	// (这是本文件唯一容易写错又不易察觉的地方: 写错了的症状是
	//  "在粗体标题里让一个词正常"静默失效, 但其它继承都对。)
	var famSet, boldSet, italicSet, lineSet, spSet bool
	for p := n; p != nil; p = p.Parent {
		if !famSet {
			if s, ok := p.PropStr("fontFamily"); ok && strings.TrimSpace(s) != "" {
				st.Family, famSet = s, true
			}
		}
		if !boldSet {
			if b, ok := p.boldProp(); ok {
				st.Bold, boldSet = b, true
			}
		}
		if !italicSet {
			if b, ok := p.italicProp(); ok {
				st.Italic, italicSet = b, true
			}
		}
		if !lineSet {
			// 逻辑值 → 设备像素 (density.go)
			if v, ok := p.PropNum("lineHeight"); ok && v > 0 {
				st.LineH, lineSet = dpToPx(v), true
			}
		}
		if !spSet {
			// 逻辑值 → 设备像素 (density.go)
			if v, ok := p.PropNum("letterSpacing"); ok {
				st.LetterSp, spSet = dpToPx(v), true
			}
		}
		// 五根轴都定下了就不必再往上走 (链长通常 <10, 但每层要读 5 个 prop)。
		if famSet && boldSet && italicSet && lineSet && spSet {
			break
		}
	}
	if st.Size < 8 {
		st.Size = 8
	}
	return st
}

// boldProp 解析 `fontWeight`。
//
// 收三种写法, 兼容面从 CSS 抄过来的人:
//   - 数值: >= 600 算粗 (CSS 的 400=normal / 700=bold, 600 是 SemiBold 起点);
//     数字串 ("700") 同样收 —— JSX 属性写字符串是常见手滑。
//   - 关键字: bold/bolder/semibold/extrabold/heavy/black;
//   - 反关键字: normal/regular/medium/light/thin (medium=500 不算粗)。
//
// 返回的 bool 是"这个 prop 到底写没写": 写了 normal 会**主动关掉**祖先的
// 粗体 (否则"在粗体段落里让一个词正常"这种最普通的用法做不到)。
func (n *GuiNode) boldProp() (bool, bool) {
	v, ok := n.Props["fontWeight"]
	if !ok {
		return false, false
	}
	if num, isNum := v.(*object.Number); isNum {
		return num.Value >= 600, true
	}
	s := strings.ToLower(strings.TrimSpace(object.ToString(v)))
	switch s {
	case "bold", "bolder", "semibold", "demibold", "extrabold", "black", "heavy":
		return true, true
	case "normal", "regular", "medium", "light", "lighter", "thin", "book":
		return false, true
	}
	if w, err := strconv.Atoi(s); err == nil {
		return w >= 600, true
	}
	return false, true
}

// italicProp 解析 `fontStyle`。oblique 与 italic 在光栅层是同一件事 (真正的
// oblique 是几何倾斜的独立设计, 没有真实面时两者都只能合成)。
func (n *GuiNode) italicProp() (bool, bool) {
	v, ok := n.Props["fontStyle"]
	if !ok {
		return false, false
	}
	s := strings.ToLower(strings.TrimSpace(object.ToString(v)))
	switch s {
	case "italic", "oblique":
		return true, true
	case "normal":
		return false, true
	}
	return false, true
}

// ===== 样式化的度量 / 换行 / 绘制 =====

// lineHeightStyled 返回行高 (px)。显式 lineHeight 优先, 否则与 v1 同一条
// 近似公式 (字号 + 字号/4) —— 保持它不变, 默认样式下的布局结果因此与
// 加样式轴之前**逐像素一致**, 既有用例不会因为这次改动而漂移。
func lineHeightStyled(st TextStyle) int {
	if st.LineH > 0 {
		return st.LineH
	}
	size := st.Size
	if size < 8 {
		size = 8
	}
	return size + size/4
}

// runeAdvanceStyled 返回单个字符的前进宽度 (含字距)。
//
// 字距加在**字符之后**而不是之前: 这样"设了字距的文本"左边界与不设时对齐
// (首字符不会莫名其妙缩进一格), 与 CSS letter-spacing 一致。
func runeAdvanceStyled(st TextStyle, r rune) int {
	return runeAdvanceFace(st, r) + st.LetterSp
}

// runeAdvanceFace 返回单个字符的字形前进宽度 (不含字距)。
func runeAdvanceFace(st TextStyle, r rune) int {
	if r == '\t' {
		// 制表符没有字形: 按 4 个空格算 (与终端习惯一致), 字距不参与
		// 这个乘法 —— 否则 "tab 宽度"会随字距变化, 很难预期。
		return 4 * runeAdvanceFace(st, ' ')
	}
	e, err := glyphFor(st, r)
	if err != nil || e.advance <= 0 {
		return st.Size / 2
	}
	return e.advance
}

// runeWidthStyled 返回字符串在给定样式下的像素宽度。
func runeWidthStyled(text string, st TextStyle) int {
	w := 0
	for _, r := range text {
		w += runeAdvanceStyled(st, r)
	}
	return w
}

// MeasureTextStyled 测量单行文本 (像素)。h 为行高。
func MeasureTextStyled(text string, st TextStyle) (w, h int) {
	if st.Size < 8 {
		st.Size = 8
	}
	for _, r := range text {
		w += runeAdvanceStyled(st, r)
	}
	return w, lineHeightStyled(st)
}

// wrapSpan 是一段换行结果在**本段源串**里的 rune 区间 [start, end)。
//
// 为什么事实来源是下标而不是字符串: 编辑框的光标/选区都是**列号**, 而"光标该
// 画在屏幕第几行第几格"完全取决于这一列落在第几段、段内第几列。由字符串反推
// 下标 (strings.Index) 在重复字符上会撞车 ("aaaa" 折成 "aa"/"aa" 时下标是
// 0 还是 2?); 而由下标拼字符串是单向确定的。
type wrapSpan struct{ start, end int }

// wrapRuneSpans 是**分段算法的唯一实现**: 贪心逐 rune 累加, 超宽即折;
// maxWidth <= 0 表示没有宽度约束 (整段一行); 单字符宽于 maxWidth 时让它独占
// 一段 —— 否则内层永远凑不满, 直接死循环。
//
// wrapTextStyled 由它派生 (再拼回字符串), 编辑框的软换行也由它派生 ——
// 两处若各写一份, 漂移的症状是"光标画的位置和字不在同一格", 而且只在
// 特定字符组合下出现, 极难查。
func wrapRuneSpans(rs []rune, adv func(rune) int, maxWidth int) []wrapSpan {
	if maxWidth <= 0 {
		return []wrapSpan{{0, len(rs)}}
	}
	var spans []wrapSpan
	start, curW := 0, 0
	for i, r := range rs {
		a := adv(r)
		if curW > 0 && curW+a > maxWidth {
			spans = append(spans, wrapSpan{start, i})
			start, curW = i, 0
		}
		curW += a
	}
	return append(spans, wrapSpan{start, len(rs)})
}

// wrapTextStyled 是 wrapText 的样式版: 显式 '\n' 强制换行; 其余贪心逐 rune
// 累加, 超宽即折; maxWidth <= 0 表示没有宽度约束; 单字符宽于 maxWidth 时让它
// 独占一行 (否则死循环); maxLines > 0 时末行补 "..."。
func wrapTextStyled(text string, st TextStyle, maxWidth, maxLines int) []string {
	if st.Size < 8 {
		st.Size = 8
	}
	var lines []string
	for _, para := range strings.Split(text, "\n") {
		rs := []rune(para)
		for _, sp := range wrapRuneSpans(rs, func(r rune) int { return runeAdvanceStyled(st, r) }, maxWidth) {
			lines = append(lines, string(rs[sp.start:sp.end]))
		}
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ellipsizeStyled(lines[maxLines-1], st, maxWidth)
	}
	return lines
}

// ellipsizeStyled 是 ellipsize 的样式版 (同一算法, 样式化度量)。
func ellipsizeStyled(line string, st TextStyle, maxWidth int) string {
	if maxWidth <= 0 {
		return line + ellipsisMark
	}
	if runeWidthStyled(line, st)+runeWidthStyled(ellipsisMark, st) <= maxWidth {
		return line + ellipsisMark
	}
	markW := runeWidthStyled(ellipsisMark, st)
	rs := []rune(line)
	used := 0
	for len(rs) > 0 {
		adv := runeAdvanceStyled(st, rs[len(rs)-1])
		if used+markW+adv > maxWidth {
			break
		}
		used += adv
		rs = rs[:len(rs)-1]
	}
	return string(rs) + ellipsisMark
}

// MeasureTextMultiStyled 多行测量: 宽 = 最长行, 高 = 行数 × 行高。
func MeasureTextMultiStyled(text string, st TextStyle, maxWidth, maxLines int) (w, h int) {
	if st.Size < 8 {
		st.Size = 8
	}
	lines := wrapTextStyled(text, st, maxWidth, maxLines)
	for _, ln := range lines {
		if lw := runeWidthStyled(ln, st); lw > w {
			w = lw
		}
	}
	return w, len(lines) * lineHeightStyled(st)
}

// DrawTextStyled 在 (x, y) (左上角) 按样式绘制单行文本, 限制在 clip 内;
// maxWidth > 0 时超出硬截断 (v1 不加省略号, 与 DrawText 同口径)。
// 返回实际绘制的宽度 (含字距)。
func DrawTextStyled(img *image.RGBA, clip image.Rectangle, text string, x, y int,
	st TextStyle, c color.RGBA, maxWidth int) int {
	if st.Size < 8 {
		st.Size = 8
	}
	// 子树不透明度 (P3-2): 文字走"字形覆盖度当 alpha"的混合 (见 blitGlyph,
	// 只用 c.R/G/B), 所以这里不能像 FillRect 那样改 c.A —— 把淡出因子
	// 编码进 alpha 通道传下去, blitGlyph 会乘到覆盖度上。
	c = applyFade(c)
	rf, err := faceForStyle(st)
	if err != nil {
		return 0
	}
	dotY := y + rf.ascent // 基线
	drawn := 0
	for _, r := range text {
		e, err := glyphFor(st, r)
		if err != nil {
			return drawn
		}
		adv := e.advance + st.LetterSp
		if maxWidth > 0 && drawn+adv > maxWidth {
			break
		}
		if e.mask != nil {
			blitGlyph(img, clip, e, x+drawn+e.offX, dotY+e.offY, dotY, c)
		}
		drawn += adv
	}
	return drawn
}
