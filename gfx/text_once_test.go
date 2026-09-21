package gfx

import (
	"bytes"
	"image"
	"testing"

	"github.com/14752222/Gox/object"
)

// 回归 (2026-09-21): <text> 容器与它的 #text 子节点各画了一遍 —— 同一个字
// 被画两次, 且两遍字号不同 (容器用自己的 font, #text 没有 font 就落默认 16),
// 症状是文字重影/发虚。gox_demo 的截图: 标题、`count = 0`、底部提示行都是双份。
//
// 判据取"像素必须与单遍参考完全一致": 容器的绘制路径与参考调的是同一个
// DrawText、同样的盒子与字号, 所以修复后应当逐字节相同。多出的那一遍会把
// 抗锯齿边缘再叠一次 ⇒ 像素必然不同, 用例就会红。刻意不信"字号恰好 16 时
// 两遍重合"这种巧合 —— 那正是这个 bug 潜伏至今的原因。

// textInkReference 单独画一遍文本, 作为"只画一次"的参照面。
func textInkReference(n *GuiNode, text string, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	FillRect(img, Rect{0, 0, w, h}, pxWhite)
	DrawText(img, img.Bounds(), text, n.Box.X, n.Box.Y, n.FontSize(),
		tint(n.textColor(), false), n.Box.W)
	return img
}

func TestTextContentDrawnOnce(t *testing.T) {
	requireFont(t)
	// 11 / 22 是"两遍不重合"的字号, 16 是默认字号 (两遍重合 ⇒ 旧实现也看不出来)
	for _, size := range []int{11, 16, 22} {
		n := mkTextBlock("gox_demo", size)
		got := renderTree(n, 200, 40)
		want := textInkReference(n, "gox_demo", 200, 40)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Errorf("font=%d: <text> 的内容被画了不止一遍 (与单遍参考不一致)", size)
		}
	}
}

// TestTextContentWrappedDrawnOnce 折行文本同理: 容器按 wrap 画了多行,
// #text 子节点还会再按单行画一遍 (而且往往更长) ⇒ 多行区域上叠一条单行文本。
func TestTextContentWrappedDrawnOnce(t *testing.T) {
	requireFont(t)
	long := "aaaa aaaa aaaa aaaa aaaa aaaa"
	n := mkTextBlock(long, 16)
	withBool(n, "wrap", true)
	n.Props["width"] = object.NewNumber(60)
	got := renderTree(n, 120, 80)

	// 参照: 按同一份折行结果逐行画
	want := image.NewRGBA(image.Rect(0, 0, 120, 80))
	FillRect(want, Rect{0, 0, 120, 80}, pxWhite)
	lh := lineHeight(n.FontSize())
	for i, ln := range n.blockLines(n.Box.W) {
		DrawText(want, want.Bounds(), ln, n.Box.X, n.Box.Y+i*lh,
			n.FontSize(), tint(n.textColor(), false), n.Box.W)
	}
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Error("折行文本被画了不止一遍 (容器一遍 + #text 子节点一遍)")
	}
}

// TestTextSkipGuardTraversal 锁住"谁该被跳过"的口径 —— 它必须与 node.go 的
// TextContent() 遍历逐字对应: 只穿过 slot / view, 遇到 text 判已画, 遇到别的
// 元素立即停 (button 的标签就靠它自己那条分支画)。
func TestTextSkipGuardTraversal(t *testing.T) {
	node := func(tag string) *GuiNode { return mkNode(tag, nil) }
	// child 挂到 parent 下, 返回 child 里的 #text 节点
	attach := func(parent *GuiNode, tag string) *GuiNode {
		c := node(tag)
		c.Parent = parent
		parent.Children = append(parent.Children, c)
		return c.appendTextNode("x")
	}

	// 1) text 的直接 #text 子节点
	if txt := appendTextHelper(node("text")); !txt.textDrawnByTextAncestor() {
		t.Error("text 的直接 #text 子节点应判为'已由容器绘制'")
	}
	// 2) 隔一层 slot / view (动态文本挂在 slot 里)
	if txt := attach(node("text"), "slot"); !txt.textDrawnByTextAncestor() {
		t.Error("slot 里的 #text 应判为'已由祖先 text 容器绘制'")
	}
	if txt := attach(node("text"), "view"); !txt.textDrawnByTextAncestor() {
		t.Error("view 里的 #text 应判为'已由祖先 text 容器绘制'")
	}
	// 3) button 的标签只能由 #text 自己画 ⇒ 绝不能被跳过
	if txt := appendTextHelper(node("button")); txt.textDrawnByTextAncestor() {
		t.Error("button 的 #text 标签不该被判为已画 (跳掉就成了空按钮)")
	}
	// 4) 跨过别的元素就不属于容器的 TextContent() 了, 不该跳过
	if txt := attach(node("text"), "column"); txt.textDrawnByTextAncestor() {
		t.Error("跨过 column 的 #text 不属于容器的 TextContent(), 不该被跳过")
	}
}

// TestButtonLabelStillDrawn 行为兜底: 跳过逻辑不能波及 button 的标签。
func TestButtonLabelStillDrawn(t *testing.T) {
	requireFont(t)
	b := mkButton("label")
	img := renderTree(b, 120, 40)
	dark := 0
	for y := b.Box.Y + 4; y < b.Box.Y+b.Box.H-4; y++ {
		for x := b.Box.X + 4; x < b.Box.X+b.Box.W-4; x++ {
			if img.RGBAAt(x, y).R < 128 {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Error("button 的标签没有画出来 (跳过逻辑误伤了 #text 唯一的绘制路径)")
	}
}

// appendTextHelper 给节点挂一个 #text 子节点并返回它。
func appendTextHelper(n *GuiNode) *GuiNode {
	return n.appendTextNode("x")
}
