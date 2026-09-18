package gfx

import (
	"image"
	"image/color"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 字体与文本 =====

// requireFont 无可用系统字体时跳过测试 (非 Windows 环境等)。
func requireFont(t *testing.T) {
	t.Helper()
	if _, err := loadBaseFont(); err != nil {
		t.Skipf("no system font available: %v", err)
	}
}

func TestMeasureText(t *testing.T) {
	requireFont(t)
	w, h := MeasureText("Hello", 16)
	if w <= 0 || h <= 0 {
		t.Fatalf("latin measure: w=%d h=%d", w, h)
	}
	// CJK 字形宽度应约为全宽 (≈字号×字符数): "加一"@16 ≈ 32px
	w2, _ := MeasureText("加一", 16)
	if w2 < 30 || w2 > 36 {
		t.Fatalf("CJK measure = %d, want ≈32 (2 full-width glyphs)", w2)
	}
	// 字号越大越宽
	w3, _ := MeasureText("Hello", 32)
	if w3 <= w {
		t.Fatalf("larger font should measure wider: %d vs %d", w3, w)
	}
}

func TestDrawTextPixels(t *testing.T) {
	requireFont(t)
	img := image.NewRGBA(image.Rect(0, 0, 200, 50))
	white := color.RGBA{R: 255, G: 255, B: 255, A: 255}
	FillRect(img, Rect{0, 0, 200, 50}, white)

	drawn := DrawText(img, img.Bounds(), "Gox 加一", 4, 4, 20, color.RGBA{R: 200, G: 30, B: 30, A: 255}, 0)
	if drawn <= 0 {
		t.Fatalf("no pixels drawn")
	}
	// 画过的区域应有非白像素
	dark := 0
	for y := 0; y < 50; y++ {
		for x := 0; x < 200; x++ {
			c := img.RGBAAt(x, y)
			if c.R != 255 || c.G != 255 {
				dark++
			}
		}
	}
	if dark == 0 {
		t.Fatalf("text did not leave any pixels")
	}

	// 截断: maxWidth 限制绘制宽度
	img2 := image.NewRGBA(image.Rect(0, 0, 200, 50))
	FillRect(img2, Rect{0, 0, 200, 50}, white)
	full, _ := MeasureText("Hello World", 16)
	half := DrawText(img2, img2.Bounds(), "Hello World", 0, 0, 16, color.RGBA{R: 0, A: 255}, full/2)
	if half > full/2+16 { // 允许一个字符的余量
		t.Fatalf("truncation failed: drawn=%d limit≈%d", half, full/2)
	}
}

// ===== flex 布局子集 =====

func TestLayoutMarginAlignJustify(t *testing.T) {
	// margin: 10 → 子节点偏移且占位
	root := mkNode("column", nil)
	a := mkNode("rect", map[string]float64{"width": 50, "height": 10, "margin": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 10 || a.Box.Y != 10 {
		t.Fatalf("margin box = %v, want (10,10)", a.Box)
	}

	// alignItems center: 交叉轴居中 (未显式宽的节点用固有尺寸 50)
	root = mkNode("column", nil)
	root.Props["alignItems"] = object.NewString("center")
	a = mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != (400-50)/2 {
		t.Fatalf("align center x = %d, want %d", a.Box.X, (400-50)/2)
	}

	// alignItems end
	root = mkNode("column", nil)
	root.Props["alignItems"] = object.NewString("end")
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.X != 400-50 {
		t.Fatalf("align end x = %d", a.Box.X)
	}

	// justifyContent center (主轴垂直)
	root = mkNode("column", nil)
	root.Props["justifyContent"] = object.NewString("center")
	root.Children = []*GuiNode{a}
	Layout(root, 400, 300)
	if a.Box.Y != (300-10)/2 {
		t.Fatalf("justify center y = %d", a.Box.Y)
	}

	// justifyContent between: 两节点分布到两端
	root = mkNode("row", nil)
	root.Props["justifyContent"] = object.NewString("between")
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)
	if a.Box.X != 0 || b.Box.X != 400-50 {
		t.Fatalf("justify between: a=%v b=%v", a.Box, b.Box)
	}
}

func TestLayoutFlexGrow(t *testing.T) {
	// row 400 宽: a=100 + b(flexGrow:1) → b 撑满剩余 300
	root := mkNode("row", nil)
	a := mkNode("rect", map[string]float64{"width": 100, "height": 10})
	b := mkNode("rect", map[string]float64{"width": 50, "height": 10, "flexGrow": 1})
	root.Children = []*GuiNode{a, b}
	Layout(root, 400, 300)
	if b.Box.X != 100 || b.Box.W != 300 {
		t.Fatalf("flexGrow: b=%v, want x=100 w=300", b.Box)
	}
}

func TestLayoutTextIntrinsic(t *testing.T) {
	requireFont(t)
	// text 节点无显式尺寸 → 按字体测量固有宽
	root := mkNode("column", nil)
	label := &GuiNode{Tag: "text", Props: map[string]object.Value{
		"font": object.NewNumber(20),
	}}
	label.Children = []*GuiNode{{Tag: "#text", Text: "count: 42", Props: map[string]object.Value{}}}
	root.Children = []*GuiNode{label}
	Layout(root, 400, 300)

	tw, _ := MeasureText("count: 42", 20)
	if label.Box.W != tw {
		t.Fatalf("text intrinsic width = %d, want %d", label.Box.W, tw)
	}
	if label.Box.H < 20 {
		t.Fatalf("text height too small: %d", label.Box.H)
	}
}

// ===== 脏矩形 =====

// TestDirtyRectPartialUpdate 点击后只重绘脏区 (局部上屏)。
func TestDirtyRectPartialUpdate(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [count, setCount] = createSignal(0);
		const ui = h("column", {gap: 10, padding: 16},
			h("rect", {width: () => count() * 20 + 10, height: 24, background: "#c0392b"}),
			h("rect", {width: 200, height: 32, background: "#27ae60",
				onClick: () => setCount(c => c + 1)}));
		render(ui, window({title: "T", width: 400, height: 300}));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 首帧: 整帧
	fake.mu.Lock()
	firstRegions := fake.regions
	fake.mu.Unlock()
	if firstRegions != nil {
		t.Fatalf("first frame should be full-frame (regions=nil)")
	}

	fake.push(Event{Kind: EventMouseUp, X: 116, Y: 66})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	fake.mu.Lock()
	regions := fake.regions
	img := fake.img
	fake.mu.Unlock()
	if regions == nil || len(regions) == 0 {
		t.Fatalf("post-click frame should be partial (got full-frame)")
	}
	area := 0
	for _, r := range regions {
		area += r.Dx() * r.Dy()
		if r.Dx()*r.Dy() >= 400*300 {
			t.Fatalf("dirty region covers whole frame: %v", r)
		}
	}
	if area >= 400*300 {
		t.Fatalf("dirty area %d covers full frame", area)
	}
	// 帧内容仍是新宽度 (30px 红条)
	if c := img.RGBAAt(16+29, 20); c.R != 0xC0 {
		t.Fatalf("red bar not updated in partial frame: %v", c)
	}
}

// TestDirtySiblingShift 红条变宽挤动绿条时, 移位的兄弟节点也在脏区内。
func TestDirtySiblingShift(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	// row: [width=() => n*10+10 的红条][绿条], 红条变宽推动绿条右移
	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, window, render } from "gx/gfx";
		const [n, setN] = createSignal(0);
		const ui = h("row", {gap: 0},
			h("rect", {width: () => n() * 10 + 10, height: 20, background: "#c0392b"}),
			h("rect", {width: 30, height: 20, background: "#27ae60",
				onClick: () => setN(3)}));
		render(ui, window({title: "T", width: 400, height: 300}));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	fake.push(Event{Kind: EventMouseUp, X: 30, Y: 10}) // 点绿条 (10..40, 0..20)
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	fake.mu.Lock()
	img := fake.img
	fake.mu.Unlock()
	// 红条 40px + 绿条 x:40..70; 绿条新位置应为绿色
	if c := img.RGBAAt(60, 10); c.R != 0x27 || c.G != 0xAE {
		t.Fatalf("green sibling not repainted at new position: %v", c)
	}
	// 旧位置 (红条变宽前绿条在 10..40) 不再是绿色 (x=20 现在是红条内部)
	if c := img.RGBAAt(20, 10); c.R != 0xC0 {
		t.Fatalf("red bar not repainted over old green position: %v", c)
	}
}

// ===== 键盘事件 =====

func TestKeyboardEvent(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	v, err := vm.EvalVM(`
		import { h, window, render } from "gx/gfx";
		let out = "";
		const ui = h("column", null,
			h("rect", {width: 100, height: 30, background: "#27ae60",
				onClick: () => 0, onKeyDown: (e) => { out += "[" + e.key + "]"; }}),
			h("rect", {width: 100, height: 30, background: "#000",
				onKeyDown: () => { out += "child"; }}));
		render(ui, window({title: "T", width: 400, height: 300}));
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}

	// 点击第一个 rect 设焦点, 然后按键
	fake.push(Event{Kind: EventMouseUp, X: 50, Y: 15})
	fake.push(Event{Kind: EventKeyDown, Key: "a"})
	fake.push(Event{Kind: EventKeyDown, Key: "Enter"})
	fake.push(Event{Kind: EventClose})
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	kv, ok := v.Globals().Get("out")
	if !ok {
		t.Fatalf("out var missing")
	}
	s, _ := kv.(*object.String)
	if s == nil || s.Value != "[a][Enter]" {
		t.Fatalf("keys = %v, want [a][Enter]", s)
	}
}

// ===== 基准: 1000 节点树单信号变化的一帧 =====

func BenchmarkDirtyFrame1000Nodes(b *testing.B) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	defer SetDefaultFactory(nil)

	root := mkNode("column", map[string]float64{"gap": 1})
	var target *GuiNode
	for i := 0; i < 1000; i++ {
		c := &GuiNode{Tag: "rect", Props: map[string]object.Value{
			"width":      object.NewNumber(float64(50 + i%40)),
			"height":     object.NewNumber(2),
			"background": object.NewString("#888888"),
		}}
		root.Children = append(root.Children, c)
		if i == 500 {
			target = c
		}
	}
	if _, err := Mount(root, WindowConfig{Width: 400, Height: 900}); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		target.Props["width"] = object.NewNumber(float64(60 + i%50))
		markNodeDirty(target)
		activeApp.redraw()
	}
}
