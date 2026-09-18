package gfx

import (
	"errors"
	"image"
	"image/color"
	"sync"
	"testing"
	"time"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 纯 Go 层测试: 颜色 / 光栅化 / 布局 / 命中测试 =====

func TestParseColor(t *testing.T) {
	c, ok := ParseColor("#c0392b")
	if !ok || c != (color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255}) {
		t.Fatalf("#c0392b → %v ok=%v", c, ok)
	}
	c, ok = ParseColor("#0f0")
	if !ok || c != (color.RGBA{R: 0, G: 0xFF, B: 0, A: 255}) {
		t.Fatalf("#0f0 → %v ok=%v", c, ok)
	}
	if _, ok := ParseColor("crimson"); !ok {
		t.Fatalf("named color crimson should parse")
	}
	if _, ok := ParseColor("notacolor"); ok {
		t.Fatalf("invalid color should fail")
	}
}

func TestFillRectClipped(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	FillRect(img, Rect{X: 5, Y: 5, W: 20, H: 20}, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	// 右下角被裁剪到画布内
	if got := img.RGBAAt(9, 9); got.R != 1 || got.G != 2 || got.B != 3 {
		t.Fatalf("clipped fill (9,9) = %v", got)
	}
	if got := img.RGBAAt(4, 9); got.R != 0 {
		t.Fatalf("outside rect should be untouched, got %v", got)
	}
	// 完全出界
	FillRect(img, Rect{X: 100, Y: 100, W: 5, H: 5}, color.RGBA{R: 9, G: 9, B: 9, A: 255})
	if got := img.RGBAAt(9, 9); got.R != 1 {
		t.Fatalf("fully-outside fill must not draw, got %v", got)
	}
}

func TestStrokeRect1px(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 10, 10))
	StrokeRect(img, Rect{X: 2, Y: 2, W: 4, H: 4}, color.RGBA{R: 255, A: 255})
	for _, p := range [][2]int{{2, 2}, {5, 2}, {2, 5}, {5, 5}} {
		if img.RGBAAt(p[0], p[1]).R != 255 {
			t.Fatalf("border corner %v not drawn", p)
		}
	}
	if img.RGBAAt(3, 3).R != 0 {
		t.Fatalf("interior should be empty")
	}
}

// mkNode 造一个带数值属性的测试节点。
func mkNode(tag string, props map[string]float64) *GuiNode {
	n := &GuiNode{Tag: tag, Props: map[string]object.Value{}}
	for k, v := range props {
		n.Props[k] = object.NewNumber(v)
	}
	return n
}

func TestLayoutColumnRow(t *testing.T) {
	// column: padding 10, gap 5; 两个子节点 [w×h] = [100×20], [未指定宽×30]
	root := mkNode("column", map[string]float64{"padding": 10, "gap": 5})
	a := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	b := mkNode("rect", map[string]float64{"height": 30})
	root.Children = []*GuiNode{a, b}

	Layout(root, 400, 300)

	if a.Box != (Rect{X: 10, Y: 10, W: 100, H: 20}) {
		t.Fatalf("child a box = %v", a.Box)
	}
	// b 未指定 width → 占满内容区; y = 10 + 20 + 5
	if b.Box != (Rect{X: 10, Y: 35, W: 380, H: 30}) {
		t.Fatalf("child b box = %v", b.Box)
	}

	// row: 主轴横向
	root2 := mkNode("row", map[string]float64{"gap": 4})
	c := mkNode("rect", map[string]float64{"width": 50, "height": 10})
	d := mkNode("rect", map[string]float64{"width": 60, "height": 10})
	root2.Children = []*GuiNode{c, d}
	Layout(root2, 400, 300)
	if c.Box != (Rect{X: 0, Y: 0, W: 50, H: 10}) || d.Box != (Rect{X: 54, Y: 0, W: 60, H: 10}) {
		t.Fatalf("row layout: c=%v d=%v", c.Box, d.Box)
	}
}

func TestHitTest(t *testing.T) {
	root := mkNode("column", nil)
	red := mkNode("rect", map[string]float64{"width": 100, "height": 20})
	green := mkNode("rect", map[string]float64{"width": 200, "height": 30})
	green.Props["onClick"] = object.NewBuiltin("click", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})
	root.Children = []*GuiNode{red, green}
	Layout(root, 400, 300)

	// 布局: red (0,0,100,20), green (0,20,200,30)
	if HitTest(root, 50, 25) != green {
		t.Fatalf("click on green should hit green")
	}
	if HitTest(root, 50, 10) != nil {
		t.Fatalf("click on red (无 onClick) → nil")
	}
	if HitTest(root, 50, 51) != nil {
		t.Fatalf("empty area below children → nil")
	}
	if HitTest(root, 399, 299) != nil {
		t.Fatalf("empty area → nil")
	}
}

// ===== 全链路测试: 假 Surface + 真 VM (render → pump → 点击 → 响应式) =====

// fakeSurface 是 Surface 的测试替身: 无真窗口, 事件由测试注入。
type fakeSurface struct {
	events  chan Event
	arrived chan struct{} // WaitEvents 的唤醒信号 (不消费 events)
	mu      sync.Mutex
	img     *image.RGBA
	w, h    int
	shown   int
	regions []image.Rectangle // 最近一次 ShowRegions 的区域 (nil=整帧)

	// dialog 非空时, fakeSurface 就同时满足 nativeDialogHost 可选接口 (P3-4)。
	// 字段放在这里而不是各测试文件里加包装类型, 是为了让"注入假原生框"
	// 与其它可选接口 (剪贴板等) 用同一种写法。
	dialog *fakeDialogHost
}

// ShowMessage 转发给注入的假原生框 (未注入时返回错误 = "后端不支持")。
func (f *fakeSurface) ShowMessage(kind NativeDialogKind, title, message string) (bool, error) {
	if f.dialog == nil {
		return false, errors.New("test: 未注入假对话框")
	}
	return f.dialog.ShowMessage(kind, title, message)
}

// ShowOpenFile 同上。
func (f *fakeSurface) ShowOpenFile(opts NativeFileOptions) (string, bool, error) {
	if f.dialog == nil {
		return "", false, errors.New("test: 未注入假对话框")
	}
	return f.dialog.ShowOpenFile(opts)
}

func newFakeSurface() *fakeSurface {
	return &fakeSurface{events: make(chan Event, 16), arrived: make(chan struct{}, 16), w: 400, h: 300}
}

func (f *fakeSurface) Show(img *image.RGBA) {
	f.ShowRegions(img, nil)
}

func (f *fakeSurface) ShowRegions(img *image.RGBA, rects []image.Rectangle) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := *image.NewRGBA(img.Bounds())
	copy(cp.Pix, img.Pix)
	f.img = &cp
	f.shown++
	f.regions = rects
}

func (f *fakeSurface) Size() (int, int) { return f.w, f.h }

func (f *fakeSurface) WaitEvents(maxWait time.Duration) bool {
	if maxWait <= 0 {
		maxWait = 10 * time.Second
	}
	select {
	case <-f.arrived:
		return true
	case <-time.After(maxWait):
		return true
	}
}

func (f *fakeSurface) Events() <-chan Event { return f.events }

// push 注入一个窗口事件并唤醒 WaitEvents。
func (f *fakeSurface) push(ev Event) {
	f.events <- ev
	f.arrived <- struct{}{}
}

func shots(f *fakeSurface) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.shown
}

// TestReactivePipeline 端到端: JS 建树(render 挂载到假窗口) → 注入点击 →
// onClick 改 signal → effect 更新宽度属性 → 重绘。
func TestReactivePipeline(t *testing.T) {
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
	if !Active() {
		t.Fatalf("render should mount the app")
	}
	if shots(fake) < 1 {
		t.Fatalf("initial frame not shown")
	}

	// 布局: padding 16 → 红块 (16,16,10,24); 绿块 (16,50,200,32)
	// 注入点击绿块中心 (116, 66) 与关闭事件
	fake.push(Event{Kind: EventMouseUp, X: 116, Y: 66})
	fake.push(Event{Kind: EventClose})

	// 事件循环驱动: 点击在 pump 内处理 (currentVM 由循环注册)
	if err := v.RunTimersWithPump(Pump); err != nil {
		t.Fatalf("RunTimersWithPump: %v", err)
	}

	// 断言: count=1 → 红块宽度属性 = 30
	uiVal, ok := v.Globals().Get("ui")
	if !ok {
		t.Fatalf("global ui missing")
	}
	ui := uiVal.(*GuiNode)
	red := ui.Children[0]
	w, ok := red.PropNum("width")
	if !ok || w != 30 {
		t.Fatalf("reactive width after click: got %v ok=%v, want 30", w, ok)
	}
	// 断言: 点击后的重绘帧里红块宽度 30px (close 冲刷了重绘)
	fake.mu.Lock()
	img := fake.img
	shown := fake.shown
	fake.mu.Unlock()
	if img == nil || shown < 2 {
		t.Fatalf("post-click frame not captured (shown=%d)", shown)
	}
	if c := img.RGBAAt(16, 20); c.R != 0xC0 || c.G != 0x39 {
		t.Fatalf("red bar pixel = %v", c)
	}
	if c := img.RGBAAt(16+29, 20); c.R != 0xC0 || c.G != 0x39 {
		t.Fatalf("pixel at width edge should be red: %v", c)
	}
	if c := img.RGBAAt(16+31, 20); c.R == 0xC0 && c.G == 0x39 {
		t.Fatalf("pixel beyond width should not be red")
	}
	if Active() {
		t.Fatalf("app should be closed")
	}
}

type fakeFactory struct{ s *fakeSurface }

func (f *fakeFactory) Create(cfg WindowConfig) (Surface, error) { return f.s, nil }
