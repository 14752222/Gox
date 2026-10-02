package gfx

import (
	"testing"

	"github.com/14752222/Gox/object"
)

// ===== M4 窗口几何 / 移动测试 =====
//
// 坐标口径 (window_move.go 头部定死): x/y = 窗口外框左上角相对**所在显示器
// 工作区**左上角的偏移。下面每个用例都在断言这一条, 而不是"绝对坐标碰巧相等"。

// TestWindowWorkAreaPos 验证"绝对坐标 → 工作区相对"的换算 (含工作区偏移)。
func TestWindowWorkAreaPos(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, d2 := twoDisplays()
	s := newMovableSurface(400, 300)
	// D2 工作区原点在 (1920, 0): 窗口绝对坐标 (1920+25, 0+15) → 相对 (25,15)。
	s.ox, s.oy = d2.WorkX+25, d2.WorkY+15
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: map[Surface]string{s: "D2"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 400, Height: 300})

	x, y, ok := win.Position()
	if !ok || x != 25 || y != 15 {
		t.Fatalf("Position = (%d,%d,%v), want (25,15,true)", x, y, ok)
	}
}

// TestMoveToConvertsToAbsolute 验证 moveTo 把工作区相对坐标换算成绝对坐标后
// 才交给后端 —— 这是后端不做二次解释的前提。
func TestMoveToConvertsToAbsolute(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, d2 := twoDisplays()
	s := newMovableSurface(400, 300)
	s.ox, s.oy = d2.WorkX, d2.WorkY
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: map[Surface]string{s: "D2"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 400, Height: 300})

	win.MoveTo(50, 60)
	DrainTasks() // moveTo 经 Post 排到 GUI 线程

	if !s.moved {
		t.Fatalf("后端未收到 MoveTo")
	}
	if s.ox != d2.WorkX+50 || s.oy != d2.WorkY+60 {
		t.Fatalf("MoveTo 后绝对坐标 = (%d,%d), want (%d,%d)",
			s.ox, s.oy, d2.WorkX+50, d2.WorkY+60)
	}
	// 读回来应与写进去的一致 (moveTo 与 position 互为逆运算)
	x, y, _ := win.Position()
	if x != 50 || y != 60 {
		t.Fatalf("MoveTo 后 Position = (%d,%d), want (50,60)", x, y)
	}
}

// TestCenterInWorkArea 验证 center() 在工作区 (而非整屏) 里居中。
func TestCenterInWorkArea(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, _ := twoDisplays() // WorkH=1040 (比屏高 1080 少 40, 模拟任务栏)
	s := newMovableSurface(800, 600)
	s.ox, s.oy = 0, 0
	f := &geoFactory{surf: s, disps: []Display{d1}, of: map[Surface]string{s: "D1"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 800, Height: 600})

	win.Center()
	DrainTasks()

	wantX := d1.WorkX + (d1.WorkW-800)/2 // 560
	wantY := d1.WorkY + (d1.WorkH-600)/2 // 220 (用工作区高 1040, 不是屏高 1080)
	if s.ox != wantX || s.oy != wantY {
		t.Fatalf("center 后绝对坐标 = (%d,%d), want (%d,%d)", s.ox, s.oy, wantX, wantY)
	}
}

// TestBoundsAndDisplay 验证 bounds()/display() 的读数。
func TestBoundsAndDisplay(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, d2 := twoDisplays()
	s := newMovableSurface(320, 240)
	s.ox, s.oy = d2.WorkX+7, d2.WorkY+9
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: map[Surface]string{s: "D2"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 320, Height: 240})

	x, y, bw, bh, disp, scale := win.Bounds()
	if x != 7 || y != 9 || bw != 320 || bh != 240 || disp != "D2" || scale != 2 {
		t.Fatalf("Bounds = (%d,%d,%d,%d,%q,%v)", x, y, bw, bh, disp, scale)
	}
	if got := win.Display(); got != "D2" {
		t.Fatalf("Display = %q, want D2", got)
	}
}

// TestWindowHandleGeometryBuiltins 钉住 JS 句柄方法的字段名与形状
// (moveTo/center/bounds/position/display)。
func TestWindowHandleGeometryBuiltins(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetWindowsForTest()

	d1, _ := twoDisplays()
	s := newMovableSurface(200, 100)
	s.ox, s.oy = 12, 34
	f := &geoFactory{surf: s, disps: []Display{d1}, of: map[Surface]string{s: "D1"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 200, Height: 100})

	obj, _ := win.jsObject().(*object.Object)
	for _, name := range []string{"moveTo", "center", "bounds", "position", "display"} {
		if v, ok := obj.GetProperty(name); !ok || !object.IsCallable(v) {
			t.Fatalf("句柄缺少几何方法 %q", name)
		}
	}
	// 内建函数体直接执行 (无 VM 时 object.CallFunction 是空操作, 拿不到返回值)。
	pos, _ := obj.GetProperty("position")
	po, _ := invokeBuiltin(t, pos).(*object.Object)
	if po == nil || objPropNum(po, "x") != 12 || objPropNum(po, "y") != 34 {
		t.Fatalf("position() = %v, want {x:12,y:34}", po)
	}
	bnd, _ := obj.GetProperty("bounds")
	bo, _ := invokeBuiltin(t, bnd).(*object.Object)
	if bo == nil {
		t.Fatalf("bounds() 不是对象")
	}
	for _, k := range []string{"x", "y", "width", "height", "displayId", "scale"} {
		if _, ok := bo.GetProperty(k); !ok {
			t.Fatalf("bounds() 缺少字段 %q", k)
		}
	}
	// moveTo 经调用即可 (换算由 moveTo 用例覆盖)
	mv, _ := obj.GetProperty("moveTo")
	invokeBuiltin(t, mv, object.NewNumber(1), object.NewNumber(2))
	DrainTasks()
	if s.ox != d1.WorkX+1 || s.oy != d1.WorkY+2 {
		t.Fatalf("句柄 moveTo 未生效: (%d,%d)", s.ox, s.oy)
	}
	// 参数缺失 → TypeError (拿到的是错误对象, 不是 undefined)
	if r := invokeBuiltin(t, mv, object.NewNumber(1)); !isErrValue(r) {
		t.Fatalf("moveTo 缺参应返回 TypeError, 实际 %T", r)
	}
}

// isErrValue 报告一个值是不是 JS 错误对象 (内建参数校验失败的返回形态)。
func isErrValue(v object.Value) bool {
	_, ok := v.(*object.Error)
	return ok
}

// TestMoveDegradesWhenBackendUnsupported 验证没有 windowMover / windowBoundsProvider
// 的后端上, moveTo 静默降级 (no-op), position 退化为 (0,0)。
func TestMoveDegradesWhenBackendUnsupported(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, _ := twoDisplays()
	s := newFakeSurface() // 只实现 Surface, 没有几何可选能力
	s.w, s.h = 300, 200
	f := &geoFactory{surf: s, disps: []Display{d1}, of: map[Surface]string{s: "D1"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 300, Height: 200})

	win.MoveTo(10, 10) // 不该 panic
	DrainTasks()
	if x, y, ok := win.Position(); ok || x != 0 || y != 0 {
		t.Fatalf("不支持的后端 Position 应为 (0,0,false), 实际 (%d,%d,%v)", x, y, ok)
	}
}

// TestPositionUnitPointsRoundTrip 钉住"位置单位是点"的后端 (cocoa) 上的换算:
// 脚本侧口径恒为设备像素, 与后端点坐标之间只在 gfx 层换一次 (除以/乘以 scale)。
//
// 少了这层换算, Retina(scale=2) 上 moveTo/position 会差一倍, 而且不报错 ——
// 窗口只是被挪到错误的地方。这里把读、写、往返三条路径都钉住。
func TestPositionUnitPointsRoundTrip(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetWindowsForTest()

	// 一块"点位置"的屏 (模拟 cocoa Retina): 工作区原点在 (100,50) 点, scale=2。
	d := Display{ID: "R", Name: "Retina", X: 100, Y: 50, W: 2560, H: 1600,
		WorkX: 100, WorkY: 50, WorkW: 2560, WorkH: 1560, Scale: 2,
		Primary: true, PosInPoints: true}
	s := newMovableSurface(800, 600)
	s.ox, s.oy = d.WorkX+30, d.WorkY+40 // 后端单位 (点) 下: 工作区 + (30,40)
	f := &geoFactory{surf: s, disps: []Display{d}, of: map[Surface]string{s: "R"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 800, Height: 600})

	// 读: 30/40 点 → 60/80 设备像素 (×2)
	if x, y, ok := win.Position(); !ok || x != 60 || y != 80 {
		t.Fatalf("Position = (%d,%d,%v), want (60,80,true) [点→设备像素 ×2]", x, y, ok)
	}
	// 写: moveTo(120,160) 设备像素 = 60/80 点 → 绝对 (100+60, 50+80) 点
	win.MoveTo(120, 160)
	DrainTasks()
	if s.ox != d.WorkX+60 || s.oy != d.WorkY+80 {
		t.Fatalf("MoveTo 后绝对点坐标 = (%d,%d), want (%d,%d)",
			s.ox, s.oy, d.WorkX+60, d.WorkY+80)
	}
	// 往返一致
	if x, y, _ := win.Position(); x != 120 || y != 160 {
		t.Fatalf("往返后 Position = (%d,%d), want (120,160)", x, y)
	}

	// ResolveWindowPlacement: 按屏居中与显式位置也要按点返回。
	SetDefaultFactory(f)
	t.Cleanup(func() { SetDefaultFactory(nil) })
	// 居中: x = 100 + (2560-800)/2/2 = 540; y = 50 + (1560-600)/2/2 = 290
	if x, y, ok := ResolveWindowPlacement(WindowConfig{Width: 800, Height: 600, Display: "R"}); !ok || x != 540 || y != 290 {
		t.Fatalf("按屏居中 = (%d,%d,%v), want (540,290,true)", x, y, ok)
	}
	// 显式 (20,30) 设备像素 = (10,15) 点 → (110,65)
	if x, y, ok := ResolveWindowPlacement(WindowConfig{X: 20, Y: 30}); !ok || x != 110 || y != 65 {
		t.Fatalf("显式位置 = (%d,%d,%v), want (110,65,true)", x, y, ok)
	}
}

