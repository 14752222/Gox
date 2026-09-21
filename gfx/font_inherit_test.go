package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// 回归 (2026-09-21): font 沿父链继承。
//
// 之前 FontSize() 只看自己的 prop、否则默认 16 —— 而 `#text` 节点永远不带 font
// prop (它是内容载体), 于是父元素上写的 font 一辈子不生效: `<button font={13}>标签
// </button>` 的标签仍按 16 测量+绘制 (kit_demo 的按钮就是这么写的)。测量与绘制都走
// FontSize(), 所以两边一致地错, 不会自己暴露。

// TestFontSizeInherits 口径: 自身显式 > 最近祖先显式 > 默认 16。
func TestFontSizeInherits(t *testing.T) {
	outer := mkNode("column", map[string]float64{"font": 22})
	mid := mkNode("row", nil)
	mid.Parent = outer
	outer.Children = append(outer.Children, mid)
	leaf := mid.appendTextNode("x")

	if got := leaf.FontSize(); got != 22 {
		t.Errorf("隔两层的 #text 字号 = %d, want 22 (沿父链继承)", got)
	}
	// 中间层显式覆盖: 就近生效
	mid.Props["font"] = object.NewNumber(11)
	if got := leaf.FontSize(); got != 11 {
		t.Errorf("中间层写 font=11 后 = %d, want 11 (最近祖先优先)", got)
	}
	// 自身显式最高优先
	leaf.Props["font"] = object.NewNumber(9)
	if got := leaf.FontSize(); got != 9 {
		t.Errorf("自身写 font=9 后 = %d, want 9", got)
	}
	// 非法值 (过小) 不算显式, 继续往上找
	leaf.Props["font"] = object.NewNumber(2)
	if got := leaf.FontSize(); got != 11 {
		t.Errorf("font=2 视为非法, 应回落到祖先的 11, got %d", got)
	}
	// 全链都没有 ⇒ 默认 16
	lone := mkNode("text", nil).appendTextNode("y")
	if got := lone.FontSize(); got != 16 {
		t.Errorf("没有任何 font 时应为默认 16, got %d", got)
	}
}

// TestButtonLabelHonoursFontProp 行为兜底: 父元素 (button) 上的 font 必须作用到
// 它的标签 —— 标签的测量盒宽要跟着字号变, 且与 MeasureText 口径一致。
func TestButtonLabelHonoursFontProp(t *testing.T) {
	requireFont(t)
	labelBox := func(fontSize float64) (int, int) {
		b := mkButton("label")
		if fontSize > 0 {
			b.Props["font"] = object.NewNumber(fontSize)
		}
		renderTree(b, 200, 60)
		kid := b.Children[0]
		return kid.Box.W, kid.Box.H
	}
	w11, _ := labelBox(11)
	wDef, _ := labelBox(0)
	want11, _ := MeasureText("label", 11)
	wantDef, _ := MeasureText("label", 16)

	if w11 != want11 {
		t.Errorf("font=11 的标签盒宽 = %d, want %d (MeasureText 口径)", w11, want11)
	}
	if wDef != wantDef {
		t.Errorf("默认标签盒宽 = %d, want %d", wDef, wantDef)
	}
	if w11 >= wDef {
		t.Errorf("font=11 的标签 (%d) 竟然不比默认 16 的 (%d) 窄 —— font 没生效", w11, wDef)
	}
}

// TestTextContainerChildMeasuresLikeContainer 顺带锁住一致性:
// <text font=N> 里的 #text 子节点继承后, 测量盒宽与容器自身口径相同
// (这才是"容器画一遍"的布局前提; 之前子节点按 16 量, 容器按 N 画, 两边对不上)。
func TestTextContainerChildMeasuresLikeContainer(t *testing.T) {
	requireFont(t)
	for _, size := range []int{11, 17, 22} {
		n := mkTextBlock("count = 13", size)
		renderTree(n, 240, 40)
		kid := n.Children[0]
		want, _ := MeasureText("count = 13", size)
		if kid.FontSize() != size {
			t.Errorf("font=%d: #text 子节点字号 = %d, 应与容器一致", size, kid.FontSize())
		}
		if kid.Box.W != want {
			t.Errorf("font=%d: #text 子节点盒宽 = %d, want %d", size, kid.Box.W, want)
		}
	}
}
