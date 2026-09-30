package gfx

import (
	"image/color"
	"strings"
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== S4/T09 icon 内置图标组件 =====
//
// 不变量:
//   1. 尺寸: 正方形, 边长按 size prop (缺省 16);
//   2. 颜色: color prop 覆盖, 否则继承文字色 (沿祖先链);
//   3. 绘制: 已知图标名有墨, 未知图标名静默空 (不报错不崩);
//   4. 图标集: 内置一批常用图标, 名字稳定, 与 testdata/ui/icons.js 对齐。

// mkIcon 造一个 icon 节点。
func mkIcon(name string, size int) *GuiNode {
	n := &GuiNode{Tag: "icon", Props: map[string]object.Value{}}
	if name != "" {
		withStr(n, "name", name)
	}
	if size > 0 {
		withNum(n, "size", float64(size))
	}
	return n
}

func TestIconIntrinsicSize(t *testing.T) {
	if w, h := mkIcon("home", 0).intrinsicSize(); w != iconDefaultSize || h != iconDefaultSize {
		t.Fatalf("缺省 icon 尺寸 = %dx%d, want %d", w, h, iconDefaultSize)
	}
	if w, h := mkIcon("home", 32).intrinsicSize(); w != 32 || h != 32 {
		t.Fatalf("size=32 的 icon 尺寸 = %dx%d, want 32x32", w, h)
	}
	// 非法 size 回落缺省
	if w, _ := mkIcon("home", -5).intrinsicSize(); w != iconDefaultSize {
		t.Fatalf("非法 size 应回落缺省 %d, got %d", iconDefaultSize, w)
	}
}

func TestIconPaintsInk(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	ic := mkIcon("home", 32)
	mountChildren(root, ic)

	img := renderTree(root, 100, 100)

	if ic.Box.W != 32 || ic.Box.H != 32 {
		t.Fatalf("icon 布局盒 = %dx%d, want 32x32", ic.Box.W, ic.Box.H)
	}
	if !hasInkIn(img, ic.Box) {
		t.Fatalf("home 图标未绘制任何墨迹")
	}
}

// 未知图标名: 不画、不报错 (静默空)。
func TestIconUnknownNameIsBlank(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	ic := mkIcon("no-such-icon-xyz", 24)
	mountChildren(root, ic)

	img := renderTree(root, 100, 100)
	if hasInkIn(img, ic.Box) {
		t.Fatalf("未知图标名不该画任何墨迹")
	}
}

// 颜色: color prop 应真的把墨染成那个色。
func TestIconColorProp(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	ic := mkIcon("plus", 24)
	withStr(ic, "color", "#e01b24") // 红
	mountChildren(root, ic)

	if got := iconColor(ic); got != (color.RGBA{0xe0, 0x1b, 0x24, 0xff}) {
		t.Fatalf("iconColor 未采用 color prop: %v", got)
	}
	img := renderTree(root, 100, 100)
	// 加号的横线中段应该是红的 (取中心点附近)
	cx := ic.Box.X + ic.Box.W/2
	cy := ic.Box.Y + ic.Box.H/2
	if !hasColorNear(img, cx, cy, color.RGBA{0xe0, 0x1b, 0x24, 0xff}, 2) {
		t.Fatalf("加号未按 color prop 上色")
	}
}

// 颜色继承: 父节点 color 应传给没有自身 color 的 icon。
func TestIconColorInherits(t *testing.T) {
	parent := mkNode("row", nil)
	withStr(parent, "color", "#1a5fb4")
	ic := mkIcon("check", 16)
	mountChildren(parent, ic)

	if got := iconColor(ic); got != (color.RGBA{0x1a, 0x5f, 0xb4, 0xff}) {
		t.Fatalf("icon 应继承父节点颜色, got %v", got)
	}
}

// 图标集: 收录了约定的常用图标, 且 IconNames 稳定升序。
func TestBuiltinIconSet(t *testing.T) {
	// 与 testdata/ui/icons.js 对齐的基础图标
	want := []string{
		"arrow-left", "arrow-right", "bell", "calendar", "chat", "check",
		"close", "folder", "gear", "heart", "home", "minus", "plus",
		"search", "user",
	}
	names := IconNames()
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, w := range want {
		if !have[w] {
			t.Fatalf("内置图标集缺少 %q", w)
		}
	}
	// 升序
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("IconNames 未升序: %v", names)
		}
	}
}

// 每个内置图标都能画 (不 panic, 有墨)。
func TestEveryBuiltinIconPaints(t *testing.T) {
	for name := range builtinIcons {
		root := mkNode("column", map[string]float64{"padding": 4})
		ic := mkIcon(name, 32)
		mountChildren(root, ic)
		img := renderTree(root, 60, 60)
		if !hasInkIn(img, ic.Box) {
			t.Fatalf("图标 %q 未画任何墨迹", name)
		}
	}
}

// 组件从树上正常挂载/卸载 (无泄漏、无 panic)。
func TestIconMountClean(t *testing.T) {
	root := mkNode("column", nil)
	ic := mkIcon("home", 24)
	mountChildren(root, ic)
	renderTree(root, 80, 80)
	root.Children = nil // 摘掉
	renderTree(root, 80, 80)
	if len(root.Children) != 0 {
		t.Fatalf("卸载后 root 仍有子节点")
	}
	if !strings.Contains(strings.Join(IconNames(), ","), "home") {
		t.Fatalf("图标集异常")
	}
}
