package gfx

import (
	"image"
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// T08 <rating> 测试: 缺省几何、按值填色、点击派发 (完全受控)。
// 几何/填色断言互相独立, 星形画法改动时会在这里先炸。

func mkRating(value float64) *GuiNode {
	n := mkNode("rating", nil)
	if value > 0 {
		withNum(n, "value", value)
	}
	return n
}

// TestRatingDefaultGeometry 缺省 5 颗星 × 20px 方格 ⇒ 100x20; max 参与宽度。
func TestRatingDefaultGeometry(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	r5 := mkRating(0)
	r10 := mkNode("rating", nil)
	withNum(r10, "max", 10)
	mountChildren(root, r5, r10)
	renderTree(root, 400, 200)
	if r5.Box.W != 100 || r5.Box.H != 20 {
		t.Fatalf("rating 缺省几何 %dx%d, want 100x20", r5.Box.W, r5.Box.H)
	}
	if r10.Box.W != 200 {
		t.Fatalf("max=10 的缺省宽 %d, want 200", r10.Box.W)
	}
}

// TestRatingPaintFillsByValue value=3 ⇒ 前 3 格有实心色像素、后 2 格没有;
// value=0 ⇒ 全空心 (无实心色, 但有描边灰)。断言逐格统计, 不看单点。
func TestRatingPaintFillsByValue(t *testing.T) {
	countCol := func(img *image.RGBA, b Rect, col color.RGBA) int {
		n := 0
		for x := b.X; x < b.X+b.W; x++ {
			for y := b.Y; y < b.Y+b.H; y++ {
				if img.RGBAAt(x, y) == col {
					n++
				}
			}
		}
		return n
	}

	root := mkNode("column", map[string]float64{"padding": 10})
	r := mkRating(3)
	mountChildren(root, r)
	img := renderTree(root, 400, 200)

	accent := tint(colorAccent, false)
	cellW := r.Box.W / r.ratingMax()
	for i := 0; i < r.ratingMax(); i++ {
		cell := Rect{X: r.Box.X + i*cellW, Y: r.Box.Y, W: cellW, H: r.Box.H}
		n := countCol(img, cell, accent)
		if i < 3 && n == 0 {
			t.Fatalf("第 %d 颗星应实心 (该格没有强调色像素)", i+1)
		}
		if i >= 3 && n != 0 {
			t.Fatalf("第 %d 颗星应空心 (该格出现 %d 个强调色像素)", i+1, n)
		}
	}

	// 对照组: value=0 全空心 —— 没有强调色, 但描边灰存在。
	root2 := mkNode("column", map[string]float64{"padding": 10})
	r0 := mkRating(0)
	mountChildren(root2, r0)
	img2 := renderTree(root2, 400, 200)
	if n := countCol(img2, r0.Box, accent); n != 0 {
		t.Fatalf("value=0 不该有实心像素, got %d", n)
	}
	if n := countCol(img2, r0.Box, tint(colorTrack, false)); n == 0 {
		t.Fatalf("value=0 应有空心描边 (没找到轨道灰像素)")
	}
}

// TestRatingClickDispatchesOnChange mousedown 几何命中 → onChange({value});
// 值不变是 no-op; value prop 决定当前显示 (完全受控)。
func TestRatingClickDispatchesOnChange(t *testing.T) {
	root := mkNode("column", nil)
	r := mkRating(5)
	got := 0
	calls := 0
	r.Props["onChange"] = object.NewBuiltin("onChange", func(args ...object.Value) object.Value {
		calls++
		if len(args) > 0 {
			if o, ok := args[0].(*object.Object); ok {
				if v, ok := o.GetProperty("value"); ok {
					if num, ok := v.(*object.Number); ok {
						got = int(num.Value)
					}
				}
			}
		}
		return object.UndefinedSingleton
	})
	mountChildren(root, r)
	_, a := mountTestApp(t, root, 300, 150)

	// value=5, 点第 3 格中心 → onChange({value: 3})
	cx := r.Box.X + 2*r.Box.W/5 + r.Box.W/10
	a.handleMouseDown(cx, r.Box.Y+r.Box.H/2)
	if calls != 1 || got != 3 {
		t.Fatalf("onChange 收到 %d 次 (值 %d), want 1 次 (值 3)", calls, got)
	}

	// 值不变 → no-op: 把 value 置 3 再点第 3 格, 不派发。
	withNum(r, "value", 3)
	a.handleMouseDown(cx, r.Box.Y+r.Box.H/2)
	if calls != 1 {
		t.Fatalf("值不变不该派发, 实际 %d 次", calls)
	}

	// 点第 5 格: 3 → 5, 照常派发 (最后一格取整边界: x = 末像素也算第 5 颗)。
	a.handleMouseDown(r.Box.X+r.Box.W-1, r.Box.Y+r.Box.H/2)
	if calls != 2 || got != 5 {
		t.Fatalf("第二次 onChange 收到 %d 次 (值 %d), want 2 次 (值 5)", calls, got)
	}
}
