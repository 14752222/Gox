package gfx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== M4 多窗口 / 多屏测试设施 =====
//
// 真实后端 (win32/cocoa/x11) 的 windowBoundsProvider / displayProvider 在单测
// 里跑不了, 所以造两个替身:
//
//	movableSurface —— 假 Surface 的可移动变体 (实现 windowMover /
//	                  windowBoundsProvider / windowActiveProvider)
//	geoFactory     —— 假工厂 + 可控显示器表 (实现 displayProvider)
//
// 显示器表可控是这一批用例的前提: 只有把"窗口在哪块屏 / 工作区多大"喂确定,
// 才能断言 x/y 换算与换屏事件。

// movableSurface 在 fakeSurface 之上加了"外框绝对位置 / 前台态 / 移动记录"。
type movableSurface struct {
	*fakeSurface
	ox, oy int  // 外框左上角在虚拟桌面里的绝对坐标
	active bool // IsActive 的预设值
	moved  bool // 是否收到过 MoveTo
}

func (m *movableSurface) MoveTo(x, y int) error {
	m.ox, m.oy = x, y
	m.moved = true
	return nil
}

func (m *movableSurface) WindowBounds() (x, y, w, h int, ok bool) {
	cw, ch := m.Size()
	return m.ox, m.oy, cw, ch, true
}

func (m *movableSurface) IsActive() bool { return m.active }

func newMovableSurface(w, h int) *movableSurface {
	fs := newFakeSurface()
	fs.w, fs.h = w, h
	return &movableSurface{fakeSurface: fs}
}

// geoFactory 是可控显示器表的假工厂。
type geoFactory struct {
	surf  Surface
	disps []Display
	of    map[Surface]string
}

func (f *geoFactory) Create(cfg WindowConfig) (Surface, error) { return f.surf, nil }
func (f *geoFactory) Displays() []Display                      { return f.disps }
func (f *geoFactory) DisplayOf(s Surface) (string, bool) {
	id, ok := f.of[s]
	return id, ok
}

// mountWithFactory 用给定工厂挂一个窗口 (注册/清理口径同 mountTestApp)。
func mountWithFactory(t *testing.T, f WindowFactory, cfg WindowConfig) *Window {
	t.Helper()
	SetDefaultFactory(f)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	root := &GuiNode{Tag: "column", Props: map[string]object.Value{}}
	win, err := Mount(root, cfg)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	// Mount 会把窗口写进包级 apps 注册表 (render.go), 而这张表是**跨用例**的:
	// 不在用例结束摘掉, 后面 windows() 的断言就会把别的用例遗留的窗口一并
	// 数进来 (实测 pipeline_test 的 Mount 就漏进了 windows() 的计数)。
	t.Cleanup(func() {
		unregisterApp(win.App())
		unregisterWindowHandle(win.ID())
	})
	return win
}

// resetWindowsForTest 清空包级窗口注册表 (apps / windowHandles), 让 windows()
// 的断言只看见本用例挂的窗口。apps 是跨用例的单例 (render.go), 不清就会把
// 前面用例遗留的窗口一起数进来。
func resetWindowsForTest() {
	appMu.Lock()
	apps = nil
	activeApp = nil
	appMu.Unlock()
	windowHandlesMu.Lock()
	windowHandles = map[int]*Window{}
	windowHandlesMu.Unlock()
}

// invokeBuiltin 直接执行一个 Go 侧内建函数的函数体, 绕开 VM 回调桥。
//
// 为什么不用 object.CallFunction: 桥在 currentVM == nil 时**静默返回
// undefined** (见 object/callback.go), 于是纯 Go 单测里调 JS 内建方法拿不到
// 真实结果 —— 症状是 position() 读回 <nil>。gfx/canvas.go 的 callScriptFn
// 出于同一原因直接调 bf.Fn; 这里要拿返回值, 所以单独包一层。
func invokeBuiltin(t *testing.T, fn object.Value, args ...object.Value) object.Value {
	t.Helper()
	bf, ok := fn.(*object.BuiltinFunction)
	if !ok {
		t.Fatalf("不是 BuiltinFunction: %T", fn)
	}
	return bf.Fn(args...)
}

// twoDisplays 返回一对有区分度的显示器 (D1 主屏 1080p 工作区到 1040, D2 副屏
// 1280x1024 且 scale=2 —— 覆盖"工作区高度 != 屏高"与"非 1 缩放"两种情形)。
func twoDisplays() (Display, Display) {
	d1 := Display{ID: "D1", Name: "D1", X: 0, Y: 0, W: 1920, H: 1080,
		WorkX: 0, WorkY: 0, WorkW: 1920, WorkH: 1040, Scale: 1, Primary: true}
	d2 := Display{ID: "D2", Name: "D2", X: 1920, Y: 0, W: 1280, H: 1024,
		WorkX: 1920, WorkY: 0, WorkW: 1280, WorkH: 1024, Scale: 2}
	return d1, d2
}

// ===== 窗口列表 =====

// TestWindowsListFields 钉住 windows() 的字段集合与取值。
func TestWindowsListFields(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetWindowsForTest()

	d1, d2 := twoDisplays()
	s1 := newMovableSurface(800, 600)
	s1.ox, s1.oy = 100, 50 // D1 工作区内 (0,0)
	s1.active = true
	s2 := newMovableSurface(400, 300)
	s2.ox, s2.oy = 1920+20, 30 // D2 工作区内
	f := &geoFactory{surf: s1, disps: []Display{d1, d2}, of: map[Surface]string{}}

	w1 := mountWithFactory(t, f, WindowConfig{Title: "A", Width: 800, Height: 600})
	w1.SetTitle("A2")
	// 第二个窗口复用同一工厂 (Create 返回同一个 surf 不适用于双窗口, 所以
	// 换一个工厂实例指向 s2)。
	f2 := &geoFactory{surf: s2, disps: []Display{d1, d2}, of: f.of}
	w2 := mountWithFactory(t, f2, WindowConfig{Title: "B", Width: 400, Height: 300})
	// render() 返回句柄时会调 jsObject() 把窗口登记进 windowHandles (windows()
	// 的标题来源); Go 侧直接 Mount 不走 render, 这里显式补上这一登记,
	// 让断言走的是与生产同一条路径。
	w1.jsObject()
	w2.jsObject()

	f.of[s1] = "D1"
	f.of[s2] = "D2"

	list := windowsToJS()
	arr, ok := list.(*object.Array)
	if !ok || len(arr.Elements) != 2 {
		t.Fatalf("windows() 应有 2 项, 实际 %v", list)
	}
	// 找 id == w1.ID() 的那一项
	var e1 *object.Object
	for _, e := range arr.Elements {
		o := e.(*object.Object)
		if objPropNum(o, "id") == float64(w1.ID()) {
			e1 = o
		}
	}
	if e1 == nil {
		t.Fatalf("windows() 里找不到 w1")
	}
	checks := map[string]float64{
		"x": 100, "y": 50, "width": 800, "height": 600, "scale": 1,
	}
	for k, want := range checks {
		if got := objPropNum(e1, k); got != want {
			t.Errorf("windows()[w1].%s = %v, want %v", k, got, want)
		}
	}
	if s := objPropStr(e1, "title"); s != "A2" {
		t.Errorf("title = %q, want A2", s)
	}
	if s := objPropStr(e1, "displayId"); s != "D1" {
		t.Errorf("displayId = %q, want D1", s)
	}
	if s := objPropStr(e1, "scope"); s != appScope(w1.App()) {
		t.Errorf("scope = %q, want %q", s, appScope(w1.App()))
	}
	// active/focused 字段必须存在且为布尔
	if v, ok := e1.GetProperty("active"); !ok || v == nil {
		t.Errorf("缺少 active 字段")
	}
	if v, ok := e1.GetProperty("focused"); !ok || v == nil {
		t.Errorf("缺少 focused 字段")
	}
	// focused 走 IsActive (s1.active=true)
	if b, ok := objProp(e1, "focused").(*object.Boolean); !ok || !b.Value {
		t.Errorf("w1.focused 应为 true (IsActive=true)")
	}

	// window(id): 单个 / 不存在 → null
	if got := windowByIDToJS(w2.ID()); got == object.NullSingleton {
		t.Errorf("window(%d) 不应为 null", w2.ID())
	}
	if got := windowByIDToJS(999999); got != object.NullSingleton {
		t.Errorf("不存在的窗口号应返回 null")
	}
}

// TestWindowInfoHasOriginXY 修 bug 锁字段: windowInfo().x/.y 必须存在
// (docs/gui-guide.md 早已让用户读它们, 而实现此前缺失 —— 文档-实现不一致)。
func TestWindowInfoHasOriginXY(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, d2 := twoDisplays()
	s := newMovableSurface(640, 480)
	s.ox, s.oy = d1.WorkX+33, d1.WorkY+44
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: map[Surface]string{s: "D1"}}
	win := mountWithFactory(t, f, WindowConfig{Title: "t", Width: 640, Height: 480})

	info, ok := windowInfoToJS(win).(*object.Object)
	if !ok {
		t.Fatalf("windowInfo 不是对象")
	}
	if _, ok := info.GetProperty("x"); !ok {
		t.Fatalf("windowInfo 缺少 x 字段 (文档已用 .x)")
	}
	if _, ok := info.GetProperty("y"); !ok {
		t.Fatalf("windowInfo 缺少 y 字段 (文档已用 .y)")
	}
	if got := objPropNum(info, "x"); got != 33 {
		t.Errorf("windowInfo().x = %v, want 33 (工作区相对)", got)
	}
	if got := objPropNum(info, "y"); got != 44 {
		t.Errorf("windowInfo().y = %v, want 44", got)
	}
}

// ===== 跨屏事件 =====

// TestWindowDisplayChangeEvent 验证:
//   - 首次比对不产生事件 (不报"从空串换屏");
//   - 换屏后产生一条带 windowId/fromDisplay/toDisplay 的事件;
//   - 同屏重复比对不重复派发;
//   - onWindowDisplayChange 回调与 useWindowDisplay 都能收到。
func TestWindowDisplayChangeEvent(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetWindowsForTest()

	d1, d2 := twoDisplays()
	s := newMovableSurface(500, 400)
	s.ox, s.oy = 0, 0
	of := map[Surface]string{s: "D1"}
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: of}
	mountWithFactory(t, f, WindowConfig{Title: "t", Width: 500, Height: 400})

	// 纯 Go 环境没有 VM, onWindowDisplayChange 的回调体不会被执行 (回调桥在
	// currentVM == nil 时是空操作), 所以这里断言两样都不依赖 VM 的东西:
	// 回调接线 (钩子表长度) 与内核生成的载荷快照 (windowDisplayLast / 版本号)。
	off := addWindowDisplayHook([]object.Value{object.NewBuiltin("cb", func(args ...object.Value) object.Value {
		return object.UndefinedSingleton
	})})
	if n := windowDisplayHookCount(); n != 1 {
		t.Fatalf("onWindowDisplayChange 未登记回调, 钩子数 = %d", n)
	}

	rev0 := windowDisplayRevNow()
	// 首次: 只记不报
	CheckWindowDisplay(s)
	if windowDisplayLastNow() != nil {
		t.Fatalf("首次比对不该产生换屏事件")
	}
	if windowDisplayRevNow() != rev0 {
		t.Fatalf("首次比对不该抬高版本")
	}
	// 同屏: 仍不报
	CheckWindowDisplay(s)
	if windowDisplayLastNow() != nil {
		t.Fatalf("同屏比对不该产生事件")
	}
	// 换到 D2: 恰好派发一次
	of[s] = "D2"
	CheckWindowDisplay(s)
	if got := windowDisplayRevNow() - rev0; got != 1 {
		t.Fatalf("换屏应恰好派发 1 次, 实际 %d", got)
	}
	e, _ := windowDisplayLastNow().(*object.Object)
	if e == nil {
		t.Fatalf("换屏后没有载荷快照")
	}
	if from := objPropStr(e, "fromDisplay"); from != "D1" {
		t.Errorf("fromDisplay = %q, want D1", from)
	}
	if to := objPropStr(e, "toDisplay"); to != "D2" {
		t.Errorf("toDisplay = %q, want D2", to)
	}
	if id := objPropNum(e, "windowId"); id != float64(mustActiveWindowID()) {
		t.Errorf("windowId = %v, want %d", id, mustActiveWindowID())
	}
	// useWindowDisplay 的读数 = 最近一次事件 (内建函数体直接执行, 不经 VM 桥)
	mod, _ := object.LookupBuiltinModule("gx/screen")
	inner := invokeBuiltin(t, mod["useWindowDisplay"])
	last := invokeBuiltin(t, inner)
	lastObj, _ := last.(*object.Object)
	if lastObj == nil || objPropStr(lastObj, "toDisplay") != "D2" {
		t.Fatalf("useWindowDisplay 未回读到最近事件: %v", last)
	}

	// 注销回调
	invokeBuiltin(t, off)
	if n := windowDisplayHookCount(); n != 0 {
		t.Fatalf("offWindowDisplayChange 未注销回调, 钩子数 = %d", n)
	}
}

// 下面三个小读者把换屏状态放锁里读 (状态是包级单例, 直接裸读在 -race 下会报)。
func windowDisplayHookCount() int {
	screenMu.Lock()
	defer screenMu.Unlock()
	return len(windowDisplayHooks)
}

func windowDisplayLastNow() object.Value {
	screenMu.Lock()
	defer screenMu.Unlock()
	return windowDisplayLast
}

func windowDisplayRevNow() int {
	screenMu.Lock()
	defer screenMu.Unlock()
	return windowDisplayRev
}

func mustActiveWindowID() int {
	if a := currentApp(); a != nil {
		return a.id
	}
	return 0
}

// TestResolveWindowPlacement 验证按屏放置的三种情形:
// 指定 display 居中 / 指定 x/y / 都不指定 → 平台默认。
func TestResolveWindowPlacement(t *testing.T) {
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)

	d1, d2 := twoDisplays()
	// 用 reportPosture 把屏表塞进 screenOverride (不需要真工厂)。
	// 直接注入更省事: 走 resetDisplays 之外没有公开入口 —— 用 geoFactory 即可。
	s := newMovableSurface(100, 100)
	f := &geoFactory{surf: s, disps: []Display{d1, d2}, of: map[Surface]string{}}
	SetDefaultFactory(f)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	// 情形 1: 什么都没给 → 平台默认
	if _, _, ok := ResolveWindowPlacement(WindowConfig{Width: 800, Height: 600}); ok {
		t.Fatalf("未指定位置应返回 ok=false")
	}
	// 情形 2: 指定 display=d2, 居中 → D2 工作区居中
	// 期望 x = 1920 + (1280-800)/2 = 2160, y = 0 + (1024-600)/2 = 212
	x, y, ok := ResolveWindowPlacement(WindowConfig{Width: 800, Height: 600, Display: "D2"})
	if !ok || x != 2160 || y != 212 {
		t.Fatalf("D2 居中 = (%d,%d,%v), want (2160,212,true)", x, y, ok)
	}
	// 情形 3: 指定 x/y (主屏) → WorkXY + x/y
	x, y, ok = ResolveWindowPlacement(WindowConfig{Width: 100, Height: 100, X: 40, Y: 60})
	if !ok || x != 40 || y != 60 {
		t.Fatalf("主屏指定位置 = (%d,%d,%v), want (40,60,true)", x, y, ok)
	}
	// 情形 4: 数字序号 display (render 里写 display={1})
	x, y, ok = ResolveWindowPlacement(WindowConfig{Width: 400, Height: 400, Display: "1"})
	if !ok || x != 1920+(1280-400)/2 || y != (1024-400)/2 {
		t.Fatalf("数字序号 display=1 居中 = (%d,%d,%v)", x, y, ok)
	}
	// 情形 5: 哨兵值 → 平台默认
	if _, _, ok := ResolveWindowPlacement(WindowConfig{X: DefaultWindowPos, Y: DefaultWindowPos}); ok {
		t.Fatalf("哨兵位置应视为未指定")
	}
}

// ===== 演示脚本冒烟 (避免"文档/示例与实现不一致") =====
//
// 演示脚本不在 TestExampleScriptsMount 的清单里 (那份清单归另一批用例维护),
// 但它们用的是新 API —— 语法/导入名写错不会让任何用例变红, 只会让读者一运行
// 就报错。这里把两个新演示各跑一遍 EvalVM (会真正挂载), 钉住"能解析 + 导入名
// 存在 + 能开窗"。

// demoSeqFactory 每次 Create 返回一个**新的**假 Surface (多窗口演示必须这样 ——
// 假工厂若复用同一个 Surface, registerApp 会按 Surface 作键把两个窗口盖成一个)。
type demoSeqFactory struct{ surfs []*fakeSurface }

func (f *demoSeqFactory) Create(cfg WindowConfig) (Surface, error) {
	s := newFakeSurface()
	if cfg.Width > 0 && cfg.Height > 0 {
		s.w, s.h = cfg.Width, cfg.Height
	}
	f.surfs = append(f.surfs, s)
	return s, nil
}

// evalDemo 跑一个演示脚本并返回 (VM, 工厂开出的假 Surface 列表)。
func evalDemo(t *testing.T, name string, f WindowFactory) (*vm.VM, []*fakeSurface) {
	t.Helper()
	object.GlobalScheduler().ClearAll()
	t.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	resetScreenStateForTest()
	t.Cleanup(resetScreenStateForTest)
	resetRouterStateForTest()
	t.Cleanup(resetRouterStateForTest)
	resetWindowsForTest()
	t.Cleanup(func() {
		for _, a := range appsSnapshot() {
			unregisterApp(a)
		}
		resetWindowsForTest()
	})

	src, err := os.ReadFile(filepath.Join("..", "testdata", name))
	if err != nil {
		t.Fatalf("读取脚本: %v", err)
	}
	SetDefaultFactory(f)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(string(src))
	if err != nil {
		t.Fatalf("EvalVM %s: %v", name, err)
	}
	surfs := []*fakeSurface(nil)
	if sf, ok := f.(*demoSeqFactory); ok {
		surfs = sf.surfs
	}
	return v, surfs
}

// TestBreakpointDemoMounts 验证断点演示能挂载 (导入名: gx/viewport 的
// useBreakpoint/matchBreakpoint/breakpoints)。
func TestBreakpointDemoMounts(t *testing.T) {
	f := &demoSeqFactory{}
	if _, surfs := evalDemo(t, "breakpoint_demo.js", f); len(surfs) != 1 {
		t.Fatalf("断点演示应开 1 个窗口, 实际 %d", len(surfs))
	}
	if got := len(appsSnapshot()); got != 1 {
		t.Fatalf("应登记 1 个窗口, 实际 %d", got)
	}
}

// TestMultiscreenDemoMounts 验证多屏/接续演示能挂载并开出两个窗口
// (导入名: gx/screen 的 windows/onWindowDisplayChange, gx/router 的 handoff)。
func TestMultiscreenDemoMounts(t *testing.T) {
	f := &demoSeqFactory{}
	if _, surfs := evalDemo(t, "multiscreen_demo.js", f); len(surfs) != 2 {
		t.Fatalf("多屏演示应开 2 个窗口, 实际 %d", len(surfs))
	}
	if got := len(appsSnapshot()); got != 2 {
		t.Fatalf("应登记 2 个窗口, 实际 %d", got)
	}
}
