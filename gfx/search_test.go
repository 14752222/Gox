package gfx

import (
	"image"
	"testing"

	"github.com/14752222/Gox/object"
)

// T08 <search> 测试: input 的字段变体。
// 字段机制 (受控值 / 光标 / 键盘编辑 / model) 由 input 的用例覆盖, 这里只验
// "变体差异": 几何同源、图标占位、Enter 整段提交。

func mkSearch(value string) *GuiNode {
	n := mkNode("search", nil)
	if value != "" {
		withStr(n, "value", value)
	}
	return n
}

// TestSearchGeometryMatchesInput 缺省几何与 input 同一套 (高 28 / 宽 160):
// 同一张 intrinsicSize case, 改宽了就会在这条对拍上炸出来。
func TestSearchGeometryMatchesInput(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	in := mkInput("")
	s := mkSearch("")
	mountChildren(root, in, s)
	renderTree(root, 400, 200)
	if s.Box.W != in.Box.W || s.Box.H != in.Box.H {
		t.Fatalf("search 缺省几何 %dx%d 与 input %dx%d 不同",
			s.Box.W, s.Box.H, in.Box.W, in.Box.H)
	}
}

// TestSearchLeadingOffset 让位口径只有一份: 绘制与点击定位都经它取值。
func TestSearchLeadingOffset(t *testing.T) {
	if got := searchLeading(mkInput("")); got != 0 {
		t.Fatalf("input 的 leading = %d, want 0", got)
	}
	if got := searchLeading(mkSearch("")); got != searchIconW+searchIconGap {
		t.Fatalf("search 的 leading = %d, want %d", got, searchIconW+searchIconGap)
	}
}

// TestSearchPaintsLeadingIcon 用"空字段"做像素断言: 没有 placeholder / 值时,
// search 字段内部唯一可能出现的 placeholder 灰就是放大镜; input 的空字段
// 内部则什么都不该有。 (带文字的对照组行不通: placeholder 灰与图标同色,
// 文字起点又紧挨着图标, 颜色上分不出谁是谁。)
func TestSearchPaintsLeadingIcon(t *testing.T) {
	iconCol := tint(colorPlaceholder, false)

	scanGray := func(img *image.RGBA, b Rect) int {
		n := 0
		for x := b.X + 2; x < b.X+b.W-2; x++ {
			for y := b.Y + 2; y < b.Y+b.H-2; y++ {
				if img.RGBAAt(x, y) == iconCol {
					n++
				}
			}
		}
		return n
	}

	root := mkNode("column", map[string]float64{"padding": 10})
	s := mkSearch("")
	mountChildren(root, s)
	img := renderTree(root, 400, 200)
	if scanGray(img, s.Box) == 0 {
		t.Fatalf("search 空字段应画出放大镜 (字段内部没找到 placeholder 色像素)")
	}

	// 对照组: input 的空字段内部干净 (没有图标可画)
	root2 := mkNode("column", map[string]float64{"padding": 10})
	i := mkInput("")
	mountChildren(root2, i)
	img2 := renderTree(root2, 400, 200)
	if n := scanGray(img2, i.Box); n != 0 {
		t.Fatalf("input 空字段内部不该有 placeholder 色像素, got %d", n)
	}
}

// TestSearchEnterDispatchesOnSearch 获焦按 Enter → onSearch({value}),
// 值取当前受控值; Ctrl 组合键不抢 (与 input 同规); input 的 Enter 仍不消费。
func TestSearchEnterDispatchesOnSearch(t *testing.T) {
	root := mkNode("column", nil)
	s := mkSearch("golang")
	got := ""
	s.Props["onSearch"] = object.NewBuiltin("onSearch", func(args ...object.Value) object.Value {
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("value"); ok {
					if str, ok := v.(*object.String); ok {
						got = str.Value
					}
				}
			}
		}
		return object.UndefinedSingleton
	})
	mountChildren(root, s)
	_, a := mountTestApp(t, root, 300, 150)

	if !a.handleInputKey(s, "Enter", Event{Key: "Enter"}) {
		t.Fatalf("search 应消费 Enter")
	}
	if got != "golang" {
		t.Fatalf("onSearch 收到 %q, want %q", got, "golang")
	}

	if a.handleInputKey(s, "Enter", Event{Key: "Enter", Ctrl: true}) {
		t.Fatalf("带 Ctrl 的 Enter 不该被消费")
	}

	in := mkInput("x")
	if a.handleInputKey(in, "Enter", Event{Key: "Enter"}) {
		t.Fatalf("input 不该消费 Enter (变体语义对拍)")
	}
}
