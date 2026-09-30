package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4/T09 pagination =====
//
// 不变量:
//   1. 页数换算: ceil(total/pageSize), 至少 1;
//   2. 页码窗口: 少量页全展开; 多页折叠出省略号 (-1), 首尾恒可见;
//   3. 完全受控: 点页码只派发 onChange({page, pageSize}), 自身不改状态;
//   4. 边界: 第 1 页点"‹"、末页点"›"不派发 (越界/原地);
//   5. 几何: 按钮区记进 pageBtns, 命中与绘制共用。

func TestPaginationPageCount(t *testing.T) {
	cases := []struct {
		total, size, want int
	}{
		{200, 20, 10},
		{201, 20, 11}, // 有余数进一
		{1, 20, 1},
		{0, 20, 1}, // 无数据显示 1 页
		// pageSize 非法 (0/负) 时回落到缺省 10, 故 50 条 → 5 页
		{50, 0, 5}, // pageSize 非法 → 回落缺省 10
		{50, -3, 5},
		{100, 10, 10},
	}
	for _, c := range cases {
		n := mkNode("pagination", map[string]float64{"total": float64(c.total), "pageSize": float64(c.size)})
		if got := n.paginationPageCount(); got != c.want {
			t.Fatalf("total=%d pageSize=%d → 页数 %d, want %d", c.total, c.size, got, c.want)
		}
	}
}

func TestPageWindowFolding(t *testing.T) {
	// 少量页: 全展开
	if got := pageWindow(3, 5); !eqInts(got, []int{1, 2, 3, 4, 5}) {
		t.Fatalf("5 页应全展开, got %v", got)
	}
	if got := pageWindow(1, 7); !eqInts(got, []int{1, 2, 3, 4, 5, 6, 7}) {
		t.Fatalf("7 页应全展开, got %v", got)
	}
	// 靠头部: 右侧省略
	if got := pageWindow(2, 20); !eqInts(got, []int{1, 2, 3, 4, 5, -1, 20}) {
		t.Fatalf("靠头折叠 = %v", got)
	}
	// 靠尾部: 左侧省略
	if got := pageWindow(19, 20); !eqInts(got, []int{1, -1, 16, 17, 18, 19, 20}) {
		t.Fatalf("靠尾折叠 = %v", got)
	}
	// 中间: 两侧省略, 当前页居中
	if got := pageWindow(10, 20); !eqInts(got, []int{1, -1, 9, 10, 11, -1, 20}) {
		t.Fatalf("中间折叠 = %v", got)
	}
}

func TestPaginationCurrentClamped(t *testing.T) {
	n := mkNode("pagination", map[string]float64{"total": 50, "pageSize": 10, "current": 99})
	if got := n.paginationCurrent(); got != 5 {
		t.Fatalf("超上界的 current 应钳到 5, got %d", got)
	}
	n2 := mkNode("pagination", map[string]float64{"total": 50, "pageSize": 10, "current": 0})
	if got := n2.paginationCurrent(); got != 1 {
		t.Fatalf("下界的 current 应钳到 1, got %d", got)
	}
}

func TestPaginationRendersAndHit(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12})
	pg := mkNode("pagination", map[string]float64{"total": 50, "pageSize": 10, "current": 3})
	mountChildren(root, pg)
	renderTree(root, 500, 120)

	// 5 页全展开 → 上一页 + 5 个页码 + 下一页 = 7 个按钮
	if len(pg.pageBtns) != 7 {
		t.Fatalf("按钮数 = %d, want 7 (‹ + 5 页码 + ›)", len(pg.pageBtns))
	}
	// 当前页按钮可命中并返回页码 3
	r := pg.pageBtns[3].rect // 索引: 0=‹, 1..5=页码, 6=›
	if got := pg.pageBtnAt(r.X+r.W/2, r.Y+r.H/2); got != 3 {
		t.Fatalf("命中当前页按钮返回 %d, want 3", got)
	}
	// 命中空白返回 0
	if got := pg.pageBtnAt(pg.Box.X+pg.Box.W+50, pg.Box.Y); got != 0 {
		t.Fatalf("空白处命中返回 %d, want 0", got)
	}
}

// 完全受控: 点页码派发 onChange, 但自身 current 不变 (归 prop 管)。
func TestPaginationControlledDispatch(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12})
	pg := mkNode("pagination", map[string]float64{"total": 50, "pageSize": 10, "current": 1})
	var gotPage, gotSize float64 = -1, -1
	calls := 0
	pg.Props["onChange"] = object.NewBuiltin("onChange", func(args ...object.Value) object.Value {
		calls++
		if o, ok := args[0].(*object.Object); ok {
			if v, ok := o.GetProperty("page"); ok {
				if num, ok := v.(*object.Number); ok {
					gotPage = num.Value
				}
			}
			if v, ok := o.GetProperty("pageSize"); ok {
				if num, ok := v.(*object.Number); ok {
					gotSize = num.Value
				}
			}
		}
		return object.UndefinedSingleton
	})
	mountChildren(root, pg)

	fake, a := mountTestApp(t, root, 500, 120)

	// 点第 3 页 (pageBtns[3] = 页码 3)
	r := pg.pageBtns[3].rect
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: r.X + r.W/2, Y: r.Y + r.H/2})
	if calls != 1 {
		t.Fatalf("点页码应派发一次 onChange, got %d", calls)
	}
	if gotPage != 3 || gotSize != 10 {
		t.Fatalf("onChange 参数 = page %v size %v, want 3 / 10", gotPage, gotSize)
	}
	// 受控: 自身 current 不变
	if pg.paginationCurrent() != 1 {
		t.Fatalf("受控 pagination 自身 current 不该变, got %d", pg.paginationCurrent())
	}
}

// 边界: 首页点"‹"、末页点"›"、点当前页 → 都不派发。
func TestPaginationBoundaryNoDispatch(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12})
	pg := mkNode("pagination", map[string]float64{"total": 50, "pageSize": 10, "current": 1})
	calls := 0
	pg.Props["onChange"] = object.NewBuiltin("onChange", func(args ...object.Value) object.Value {
		calls++
		return object.UndefinedSingleton
	})
	mountChildren(root, pg)
	fake, a := mountTestApp(t, root, 500, 120)

	// 首页点"‹" (pageBtns[0], page=0)
	r := pg.pageBtns[0].rect
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: r.X + r.W/2, Y: r.Y + r.H/2})
	if calls != 0 {
		t.Fatalf("首页点上一页不该派发, got %d", calls)
	}
	// 点"页码 1" (= 当前页)
	r1 := pg.pageBtns[1].rect
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: r1.X + r1.W/2, Y: r1.Y + r1.H/2})
	if calls != 0 {
		t.Fatalf("点当前页不该派发, got %d", calls)
	}
}

// 大量页时省略号不可点。
func TestPaginationEllipsisNotClickable(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 12})
	pg := mkNode("pagination", map[string]float64{"total": 1000, "pageSize": 10, "current": 50})
	mountChildren(root, pg)
	renderTree(root, 600, 120)

	// 找到省略号槽位 (page == -1)
	found := false
	for _, g := range pg.pageBtns {
		if g.page == -1 {
			found = true
			if got := pg.pageBtnAt(g.rect.X+1, g.rect.Y+1); got != 0 {
				t.Fatalf("省略号不该可点, 返回 %d", got)
			}
		}
	}
	if !found {
		t.Fatalf("100 页应折叠出省略号")
	}
}

func eqInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
