package gfx

import (
	"github.com/14752222/Gox/object"
	"testing"
)

// ===== S4 list-item =====
//
// 用例覆盖四条不变量:
//   1. 行高固定 listRowH, 与 table/select 同一套常量;
//   2. selected / hover 各自改变底色, 且 selected 优先于 hover;
//   3. divider 缺省开、给了 false 才关;
//   4. 纯展示行 (无 onClick) 悬停不变色 —— 悬停反馈只在行可点时给。

func TestListItemRowHeight(t *testing.T) {
	root := mkNode("column", map[string]float64{})
	item := &GuiNode{Tag: "list-item", Props: map[string]object.Value{}}
	mountChildren(root, item)
	renderTree(root, 300, 200)

	if item.Box.H != listRowH {
		t.Fatalf("list-item 行高 = %d, want %d", item.Box.H, listRowH)
	}
}

func TestListItemSelectedWinsOverHover(t *testing.T) {
	root := mkNode("column", map[string]float64{})
	item := withClick(&GuiNode{Tag: "list-item", Props: map[string]object.Value{}})
	withBool(item, "selected", true)
	mountChildren(root, item)

	fake, a := mountTestApp(t, root, 300, 200)
	// 悬停到行上
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: item.Box.X + 2, Y: item.Box.Y + 2})
	img := renderTree(root, 300, 200)

	if !item.hovered {
		t.Fatalf("行未进入悬停态 (事件泵没把 hovered 置上)")
	}
	if !item.listSelected() {
		t.Fatalf("selected prop 未读到")
	}
	// 选中底色应压过悬停底色
	got := img.RGBAAt(item.Box.X+item.Box.W/2, item.Box.Y+item.Box.H/2)
	if got != colorListItemSelected {
		t.Fatalf("行底色 = %v, want 选中底色 %v", got, colorListItemSelected)
	}
}

func TestListItemHoverOnlyWhenClickable(t *testing.T) {
	// 无 onClick 的纯展示行: 悬停不变色
	root := mkNode("column", map[string]float64{})
	item := &GuiNode{Tag: "list-item", Props: map[string]object.Value{}}
	mountChildren(root, item)

	fake, a := mountTestApp(t, root, 300, 200)
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: item.Box.X + 2, Y: item.Box.Y + 2})
	img := renderTree(root, 300, 200)

	if item.hovered {
		// hoverable() 把它算进去了, 但绘制时按有无 onClick 决定是否上色
		got := img.RGBAAt(item.Box.X+item.Box.W/2, item.Box.Y+item.Box.H/2)
		if got == colorListItemHover {
			t.Fatalf("无 onClick 的展示行不该画悬停底色")
		}
	}
}

func TestListItemDividerDefault(t *testing.T) {
	root := mkNode("column", map[string]float64{})
	item := &GuiNode{Tag: "list-item", Props: map[string]object.Value{}}
	mountChildren(root, item)

	if !item.listDivider() {
		t.Fatalf("divider 缺省应为开")
	}
	withBool(item, "divider", false)
	if item.listDivider() {
		t.Fatalf("divider={false} 未生效")
	}
}
