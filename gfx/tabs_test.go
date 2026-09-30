package gfx

import (
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4 rPrfGD tabs 选项卡 =====
//
// 用例覆盖六条不变量:
//   1. 几何: 标签条横排在顶部 (tabStrip 记录的命中/绘制共用几何), 激活页
//      占条下方内容区, 非激活页 Box 清零;
//   2. 绘制: 激活标签有 accent 下划线, 只有激活页的内容有墨 (keep-alive
//      留树 ≠ 留影);
//   3. 交互: 点标签条切页 (非受控改内部状态), 点内容区不切, 禁用链挡切页;
//   4. 受控: value prop 存在时点击不改内部状态, 只派发 onChange
//      {index, title}, 显示值由 value 锁定;
//   5. 固有尺寸: 条高 + 激活页内容高, 宽取条与页较大者;
//   6. 兜底: 无 title 的页得到 "Tab N", 保证标签条可点可画。

var (
	tabPgRed  = color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255} // 页一内容色
	tabPgBlue = color.RGBA{R: 0x24, G: 0x71, B: 0xA3, A: 255} // 页二内容色
)

// mkTabs 造一个 tabs 容器: 每个标题生成一页, 页内容是一块带背景色的 rect
// (色块让"哪一页在显示"可以直接按像素断言)。w/h<=0 时 tabs 不带显式尺寸
// (固有尺寸用例用)。返回 tabs 与页列表。
func mkTabs(titles []string, w, h, pw, ph float64) (*GuiNode, []*GuiNode) {
	props := map[string]float64{}
	if w > 0 {
		props["width"] = w
	}
	if h > 0 {
		props["height"] = h
	}
	tb := mkNode("tabs", props)
	var pages []*GuiNode
	for i, title := range titles {
		pg := mkNode("tab", nil)
		if title != "" {
			withStr(pg, "title", title)
		}
		bg := "#c0392b"
		if i == 1 {
			bg = "#2471a3"
		}
		box := withStr(mkNode("rect", map[string]float64{"width": pw, "height": ph}), "background", bg)
		mountChildren(pg, box)
		pages = append(pages, pg)
	}
	mountChildren(tb, pages...)
	return tb, pages
}

// tabPoint 返回第 idx 个标签的中心点 (点击用例的注入点)。
func tabPoint(tb *GuiNode, idx int) (int, int) {
	r := tb.tabStrip[idx]
	return r.X + r.W/2, r.Y + r.H/2
}

func TestTabsLayoutStripAndPages(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	mountChildren(root, tb)
	renderTree(root, 400, 300)

	if len(tb.tabStrip) != 2 {
		t.Fatalf("tabStrip 记录了 %d 个标签, want 2", len(tb.tabStrip))
	}
	// 标签条从内容区左上角起横排, 间距 tabGap
	if tb.tabStrip[0].X != tb.Box.X || tb.tabStrip[0].Y != tb.Box.Y {
		t.Fatalf("首个标签未贴 tabs 左上角: strip0=%v box=%v", tb.tabStrip[0], tb.Box)
	}
	if want := tb.tabStrip[0].X + tb.tabStrip[0].W + tabGap; tb.tabStrip[1].X != want {
		t.Fatalf("第二标签未按 tabGap 排开: X=%d want %d", tb.tabStrip[1].X, want)
	}
	// 标签宽度 = 文本测量 + 左右内边距
	tw, _ := MeasureText("File", tb.FontSize())
	if want := tw + 2*tabItemPadX; tb.tabStrip[0].W != want {
		t.Fatalf("标签宽 = %d, want 文本测量+%d 内边距 = %d", tb.tabStrip[0].W, 2*tabItemPadX, want)
	}
	// 激活页 (第 0 页) 占条下方内容区; 非激活页 Box 清零
	if pages[0].Box.Y != tb.Box.Y+tb.tabStripHeight() {
		t.Fatalf("激活页未贴标签条下方: pg.Y=%d want %d", pages[0].Box.Y, tb.Box.Y+tb.tabStripHeight())
	}
	if pages[1].Box != (Rect{}) {
		t.Fatalf("非激活页 Box 应清零, got %v", pages[1].Box)
	}
}

// 无 title 的页给兜底名 "Tab N" —— 测量与绘制/命中都走同一份文本。
func TestTabsMissingTitleFallback(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, _ := mkTabs([]string{"", ""}, 320, 120, 100, 60)
	mountChildren(root, tb)
	renderTree(root, 400, 300)

	tw, _ := MeasureText("Tab 1", tb.FontSize())
	if want := tw + 2*tabItemPadX; tb.tabStrip[0].W != want {
		t.Fatalf("兜底标题未生效: 标签宽 = %d, want %d (按 \"Tab 1\" 测量)", tb.tabStrip[0].W, want)
	}
}

func TestTabsPaintsUnderlineAndActivePageOnly(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	mountChildren(root, tb)
	img := renderTree(root, 400, 300)

	// 激活标签下划线: accent 色, 压在标签底部
	r0 := tb.tabStrip[0]
	assertPx(t, img, r0.X+tabItemPadX+1, r0.Y+r0.H-1, pxAccent, "激活标签下划线")
	// 激活页内容可见
	assertPx(t, img, pages[0].Box.X+5, pages[0].Box.Y+5, tabPgRed, "激活页色块")
	// 非激活页没有墨 (全图找不到蓝色) —— keep-alive 留树但不留影
	if n := countColor(img, Rect{0, 0, 400, 300}, tabPgBlue); n != 0 {
		t.Fatalf("非激活页被画了出来 (%d 个蓝色像素)", n)
	}
}

func TestTabsUncontrolledClickSwitches(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	mountChildren(root, tb)

	fake, a := mountTestApp(t, root, 400, 300)
	x, y := tabPoint(tb, 1)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})

	if tb.tabsActive != 1 {
		t.Fatalf("点击第二个标签后内部激活页 = %d, want 1", tb.tabsActive)
	}
	// keep-alive: 两页节点都还在树上
	if n := countTag(root, "tab"); n != 2 {
		t.Fatalf("切页后树上有 %d 个 tab, want 2 (keep-alive)", n)
	}
	// 重绘后蓝色页显示、红色页消失
	img := renderTree(root, 400, 300)
	assertPx(t, img, pages[1].Box.X+5, pages[1].Box.Y+5, tabPgBlue, "切换后激活页色块")
	if n := countColor(img, Rect{0, 0, 400, 300}, tabPgRed); n != 0 {
		t.Fatalf("切走的页仍被绘制 (%d 个红色像素)", n)
	}
}

// 点在内容区 (激活页内部) 不切页 —— tabStripAt 只认标签条几何。
func TestTabsContentClickDoesNotSwitch(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	mountChildren(root, tb)

	fake, a := mountTestApp(t, root, 400, 300)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: pages[0].Box.X + 5, Y: pages[0].Box.Y + 5})
	if tb.tabsActive != 0 {
		t.Fatalf("点内容区不该切页, 内部激活页 = %d", tb.tabsActive)
	}
}

func TestTabsControlledValueLocksAndDispatches(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	withNum(tb, "value", 1) // 受控: 显示值锁在第二页
	var gotIdx = -1.0
	var gotTitle string
	tb.Props["onChange"] = object.NewBuiltin("onChange", func(args ...object.Value) object.Value {
		if o, ok := args[0].(*object.Object); ok {
			if v, ok := o.GetProperty("index"); ok {
				if n, ok := v.(*object.Number); ok {
					gotIdx = n.Value
				}
			}
			if v, ok := o.GetProperty("title"); ok {
				if s, ok := v.(*object.String); ok {
					gotTitle = s.Value
				}
			}
		}
		return object.UndefinedSingleton
	})
	mountChildren(root, tb)
	renderTree(root, 400, 300)
	if pages[1].Box == (Rect{}) || pages[0].Box != (Rect{}) {
		t.Fatalf("value=1 时激活页应是第二页: pg0=%v pg1=%v", pages[0].Box, pages[1].Box)
	}

	fake, a := mountTestApp(t, root, 400, 300)
	x, y := tabPoint(tb, 0)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})

	// 受控: 内部状态不动, 显示值仍由 value 决定; 只派发 onChange
	if tb.tabsActive != 0 {
		t.Fatalf("受控模式不该改内部状态 (tabsActive=%d —— 初始值就是 0, 点了也不许变)", tb.tabsActive)
	}
	if tb.tabsActiveIndex() != 1 {
		t.Fatalf("受控显示值应仍读 value=1, got %d", tb.tabsActiveIndex())
	}
	if gotIdx != 0 || gotTitle != "File" {
		t.Fatalf("onChange 参数不对: index=%v title=%q, want 0 / \"File\"", gotIdx, gotTitle)
	}
	// 重新布局: 第一页仍不显示 (脚本没回写 value)
	img := renderTree(root, 400, 300)
	if n := countColor(img, Rect{0, 0, 400, 300}, tabPgRed); n != 0 {
		t.Fatalf("受控模式下脚本未回写, 第一页不该显示 (%d 个红色像素)", n)
	}
}

func TestTabsIntrinsicSize(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, pages := mkTabs([]string{"File", "Edit"}, 0, 0, 120, 70) // 无显式尺寸
	mountChildren(root, tb)

	w, h := tb.intrinsicSize()
	sw, sh := tb.tabsStripSize()
	if w != sw && w != 120 {
		t.Fatalf("固有宽 = %d, want max(条宽 %d, 页宽 120)", w, sw)
	}
	if want := sh + 70; h != want {
		t.Fatalf("固有高 = %d, want 条高 %d + 页高 70 = %d", h, sh, want)
	}
	// 页自身的固有尺寸按 column 语义测量
	pw, ph := pages[0].intrinsicSize()
	if pw != 120 || ph != 70 {
		t.Fatalf("页固有尺寸 = %dx%d, want 120x70", pw, ph)
	}
}

// 禁用链挡切页: handleMouseDown 在进入 tabs 分支前就拦截了禁用目标。
func TestTabsDisabledBlocksSwitch(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	tb, _ := mkTabs([]string{"File", "Edit"}, 320, 160, 120, 80)
	withBool(tb, "disabled", true)
	mountChildren(root, tb)

	fake, a := mountTestApp(t, root, 400, 300)
	x, y := tabPoint(tb, 1)
	pushAndPump(t, fake, a, Event{Kind: EventMouseDown, X: x, Y: y})
	if tb.tabsActive != 0 {
		t.Fatalf("禁用的 tabs 不该响应标签条点击")
	}
}

// ===== 演示脚本冒烟 (testdata/tabs_demo.js) =====
//
// 走真实 VM + 事件循环: 点受控 tabs 的第二个标签 → onChange 写回 signal →
// 镜像文本更新。这条链路是纯 Go 用例测不到的 (solid 接线在脚本侧)。
func TestTabsDemoSmoke(t *testing.T) {
	runDemoSteps(t, "tabs_demo.js", []func(*GuiNode, *fakeSurface){
		// 1) 两个 tabs 挂载, 受控镜像初始为 0; 点受控 tabs 的第二个标签
		func(root *GuiNode, fake *fakeSurface) {
			tbs := findAll(root, "tabs")
			if len(tbs) != 2 {
				t.Fatalf("tabs 数量 = %d, want 2 (受控 + 非受控)", len(tbs))
			}
			if !textContainsAny(root, "受控激活页: 0") {
				t.Fatalf("受控镜像初始文本未出现")
			}
			tb := tbs[0]
			if len(tb.tabStrip) < 2 {
				t.Fatalf("受控 tabs 标签条未布局 (%d 个)", len(tb.tabStrip))
			}
			x, y := tabPoint(tb, 1)
			fake.push(Event{Kind: EventMouseDown, X: x, Y: y})
		},
		// 2) onChange → setTab(1) → 重渲染: 镜像文本变为 1, 且三个页都还在树上
		func(root *GuiNode, fake *fakeSurface) {
			if !textContainsAny(root, "受控激活页: 1") {
				t.Fatalf("受控切页后镜像未更新 (onChange → signal → 重渲染链路断了)")
			}
			if n := countTag(root, "tab"); n != 5 {
				t.Fatalf("树上有 %d 个 tab, want 5 (3 + 2, keep-alive)", n)
			}
		},
	})
}
