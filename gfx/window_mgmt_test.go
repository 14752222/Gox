package gfx

// ===== 窗口管理: 位置 / 层级 / 尺寸约束 / 全屏 / 模态 / 光标 (§四 窗口/系统缺口) =====
//
// 这一批能力分两半, 测法也跟着分两半:
//
//   - **后端能力** (位置/层级/约束/全屏/光标/激活) 是 `windowManager` /
//     `cursorHost` / `boundsProvider` 三个**可选接口**。真后端 (win32 / x11 /
//     cocoa) 的方法在本机跑不到, 而语义 (参数怎么归一、什么时候不下发、
//     后端不支持时怎么降级) 全在 gfx 层 —— 于是用 fakeWindowManager 记录
//     调用就够了, 不必有真窗口。
//
//   - **跨窗口语义** (模态) 是本模块自己的逻辑, 必须两个真窗口才测得出。
//     这里走真 `Mount` 而不是直接拼 `&app{}`: 模态关系是在 Mount 里挂的
//     (见 render.go 的 "首帧之前挂好"), 绕过 Mount 就测不到那条路径 ——
//     而"首帧前挂好"恰恰是这次刻意选的时序 (不给用户一帧的可点窗口期)。
//
// 断言纪律: 只断言**可观测的语义** (句柄读回值 / 后端收到的调用 / 窗口状态),
// 不断言内部字段的组织方式 —— 后者会随重构漂移, 而语义不会。

import (
	"testing"

	"github.com/14752222/Gox/object"
	"github.com/14752222/Gox/vm"
)

// ===== 设施 =====

// mgmtFactory 是窗口管理用例的假工厂。
//
// 为什么不用现成的 fakeFactory: 它每次 Create 都返回**同一个** Surface,
// 两个窗口注册到同一个键上会互相覆盖 —— 模态 (父/子两个窗口) 根本没法测。
// seqFactory 每次造新的但不管尺寸, cfgFactory 管尺寸但只能单窗口场景用;
// 这里要的是三者的并集, 顺带兼 displayProvider (center() 要读工作区)。
type mgmtFactory struct {
	cfgs []WindowConfig
	made []*fakeSurface
	d    Display
}

func (f *mgmtFactory) Create(cfg WindowConfig) (Surface, error) {
	f.cfgs = append(f.cfgs, cfg)
	s := newFakeSurface()
	if cfg.Width > 0 {
		s.w = cfg.Width
	}
	if cfg.Height > 0 {
		s.h = cfg.Height
	}
	f.made = append(f.made, s)
	return s, nil
}

// Displays / DisplayOf 实现 displayProvider: 一块 1000x800、任务栏在顶部
// 占 40px 的屏。工作区 (0,40,1000,760) 与整屏**刻意不同**, 否则 center()
// 用错了一处也看不出来。
func (f *mgmtFactory) Displays() []Display { return []Display{f.d} }

func (f *mgmtFactory) DisplayOf(s Surface) (string, bool) {
	for _, x := range f.made {
		if x == s {
			return f.d.ID, true
		}
	}
	return "", false
}

// surface 按创建序取第 i 个假 Surface。
func (f *mgmtFactory) surface(t *testing.T, i int) *fakeSurface {
	t.Helper()
	if i < 0 || i >= len(f.made) {
		t.Fatalf("没有第 %d 个窗口 (共 %d 个)", i, len(f.made))
	}
	return f.made[i]
}

// newMgmtFactory 装好假工厂并登记拆卸 (清注册表 + 屏表 + 调度器)。
//
// ResetDisplaysForTest 是必需的: allDisplays() 给 screenOverride 最高优先级,
// 上一个用例上报过的"折叠屏"留在表里会让本用例读到错的工作区。
func newMgmtFactory(t *testing.T) *mgmtFactory {
	t.Helper()
	ResetDisplaysForTest()
	object.GlobalScheduler().ClearAll()
	f := &mgmtFactory{d: Display{
		ID: "mgmt-0", Name: "mgmt", W: 1000, H: 800,
		WorkX: 0, WorkY: 40, WorkW: 1000, WorkH: 760,
		Scale: 1, Primary: true,
	}}
	SetDefaultFactory(f)
	t.Cleanup(func() {
		// SetDefaultFactory(nil) 顺带清空窗口注册表 (见 gfx.go 的说明) ——
		// 留着条目会让下一个用例的 Pump 去等一个再也不会来事件的假 Surface。
		SetDefaultFactory(nil)
		ResetDisplaysForTest()
		object.GlobalScheduler().ClearAll()
	})
	return f
}

// mountManaged 走真 Mount 路径开一个窗口 (于是句柄是真的、a.win 挂上了、
// 模态关系也按真实时序处理了), 返回句柄 / app / 假 Surface。
func mountManaged(t *testing.T, f *mgmtFactory, root *GuiNode, cfg WindowConfig) (*Window, *app, *fakeSurface) {
	t.Helper()
	if root == nil {
		root = mkColumn(mkButton("only"))
	}
	if cfg.Width <= 0 {
		cfg.Width = 400
	}
	if cfg.Height <= 0 {
		cfg.Height = 300
	}
	w, err := Mount(root, cfg)
	if err != nil {
		t.Fatalf("Mount: %v", err)
	}
	s := f.surface(t, len(f.made)-1)
	a := appForSurface(s)
	if a == nil {
		t.Fatalf("Mount 之后窗口没进注册表")
	}
	return w, a, s
}

// hoverLen 读悬停链长度 (模态屏蔽的直接观测点: 输入被吞 ⇒ 链不动)。
func hoverLen(a *app) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.hoverChain)
}

// ===== 配置: 两条路径都要把新字段带到建窗工厂 =====

// TestWindowPropsCarryManagementConfig: `<window x y minWidth …>` 作根 ——
// 位置/层级/约束/缩放/全屏全部从 props 读出来。
func TestWindowPropsCarryManagementConfig(t *testing.T) {
	f := newMgmtFactory(t)
	if _, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		render(
			<window title="W" width={300} height={200} x={40} y={-20}
				minWidth={120} minHeight={100} maxWidth={800} maxHeight={600}
				resizable={false} fullscreen level="top">
				<column><button>A</button></column>
			</window>);
	`); err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if len(f.cfgs) != 1 {
		t.Fatalf("应开 1 个窗口, 实际 %d", len(f.cfgs))
	}
	cfg := f.cfgs[0]

	// x/y: 负坐标是合法落点 (左屏), 而 HasPos 说明"真的给过" ——
	// 缺一个就说明 props 路径把位置丢了。
	if !cfg.HasPos || cfg.X != 40 || cfg.Y != -20 {
		t.Fatalf("位置 = (%d,%d) hasPos=%v, want (40,-20) true", cfg.X, cfg.Y, cfg.HasPos)
	}
	if cfg.MinWidth != 120 || cfg.MinHeight != 100 || cfg.MaxWidth != 800 || cfg.MaxHeight != 600 {
		t.Fatalf("尺寸约束 = %d/%d/%d/%d, want 120/100/800/600",
			cfg.MinWidth, cfg.MinHeight, cfg.MaxWidth, cfg.MaxHeight)
	}
	if !cfg.NoResize {
		t.Fatalf("resizable={false} 应落成 NoResize=true")
	}
	if !cfg.Fullscreen {
		t.Fatalf("裸属性 fullscreen 应为 true")
	}
	if cfg.Level != "top" {
		t.Fatalf("level = %q, want top", cfg.Level)
	}
}

// TestWindowConfigObjectCarriesManagementConfig: 配置对象形态同样收全部字段
// (含 modal: parentHandle 的简写与 modal+parent 的显式写法)。
func TestWindowConfigObjectCarriesManagementConfig(t *testing.T) {
	f := newMgmtFactory(t)
	if _, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		const parent = render(h("column", null), {title: "P", width: 400, height: 300});
		render(h("column", null), {
			title: "C", width: 200, height: 150,
			x: 10, y: 20, minWidth: 90, maxHeight: 500,
			resizable: false, fullscreen: true, level: "bottom",
			modal: parent,
		});
	`); err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if len(f.cfgs) != 2 {
		t.Fatalf("应开 2 个窗口, 实际 %d", len(f.cfgs))
	}
	cfg := f.cfgs[1]
	if !cfg.HasPos || cfg.X != 10 || cfg.Y != 20 {
		t.Fatalf("位置 = (%d,%d) hasPos=%v, want (10,20) true", cfg.X, cfg.Y, cfg.HasPos)
	}
	if cfg.MinWidth != 90 || cfg.MinHeight != 0 || cfg.MaxWidth != 0 || cfg.MaxHeight != 500 {
		t.Fatalf("约束 = %d/%d/%d/%d, want 90/0/0/500",
			cfg.MinWidth, cfg.MinHeight, cfg.MaxWidth, cfg.MaxHeight)
	}
	if !cfg.NoResize || !cfg.Fullscreen || cfg.Level != "bottom" {
		t.Fatalf("缩放/全屏/层级 = %v/%v/%q, want true/true/bottom",
			cfg.NoResize, cfg.Fullscreen, cfg.Level)
	}
	// modal 简写要认得句柄: 光看 Modal=true 不够 —— 拿不到父窗口时那句
	// 只是个普通窗口 (静默降级), 所以必须断言 ModalParent 真的解析到了。
	if !cfg.Modal || cfg.ModalParent == nil {
		t.Fatalf("modal={parent} 应解析出父句柄, got modal=%v parent=%v", cfg.Modal, cfg.ModalParent)
	}
	// 父窗口号不能写死 (appSeq 是进程级发号器, 不随用例重置), 按 Surface 反查。
	parentApp := appForSurface(f.surface(t, 0))
	if parentApp == nil || cfg.ModalParent.App() != parentApp {
		t.Fatalf("modal 解析出的父句柄不是第一个窗口")
	}
}

// TestWindowConfigDefaultsUntouched: 老写法 (只有 title/width/height) 一个
// 新字段都不该被点亮 —— 新字段的零值语义必须是"不干预"。
func TestWindowConfigDefaultsUntouched(t *testing.T) {
	f := newMgmtFactory(t)
	if _, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		render(h("column", null), {title: "T", width: 320, height: 240});
	`); err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	cfg := f.cfgs[0]
	if cfg.HasPos || cfg.MinWidth != 0 || cfg.MaxWidth != 0 || cfg.NoResize ||
		cfg.Fullscreen || cfg.Level != "" || cfg.Modal || cfg.ModalParent != nil {
		t.Fatalf("默认配置被污染: %+v", cfg)
	}
	// 没有位置配置时后端不该被要求移动 (Windows 会因此收到一个 0,0 的
	// SetWindowPos, 表现为"新窗口总是跳到左上角")。
	if _, levels, _, res, full, _ := f.surface(t, 0).wm.snapshot(); len(levels) != 0 || len(res) != 0 || len(full) != 0 {
		t.Fatalf("默认配置不该下发层级/缩放/全屏: levels=%v res=%v full=%v", levels, res, full)
	}
}

// ===== 位置 =====

// TestWindowMoveToBoundsAndCenter: 位置的下发与读回, 含负坐标与居中换算。
func TestWindowMoveToBoundsAndCenter(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	// 没定过位时: 后端报不出位置 (bw<=0 的假后端), 退回 (0,0) + 客户区尺寸。
	if x, y, bw, bh := w.Bounds(); x != 0 || y != 0 || bw != 400 || bh != 300 {
		t.Fatalf("初始 bounds = (%d,%d,%d,%d), want (0,0,400,300)", x, y, bw, bh)
	}

	// 负坐标是多屏拼接的常态 (左屏在主屏左边), 不能被当非法值丢掉。
	w.MoveTo(-1200, 40)
	moves, _, _, _, _, _ := fake.wm.snapshot()
	if len(moves) != 1 || moves[0] != [2]int{-1200, 40} {
		t.Fatalf("后端收到的移动 = %v, want [[-1200 40]]", moves)
	}
	if x, y, bw, bh := w.Bounds(); x != -1200 || y != 40 || bw != 400 || bh != 300 {
		t.Fatalf("移动后 bounds = (%d,%d,%d,%d), want (-1200,40,400,300)", x, y, bw, bh)
	}

	// 居中: 工作区是 (0,40,1000,760), 窗口 400x300
	//   x = 0 + (1000-400)/2 = 300
	//   y = 40 + (760-300)/2 = 270  ← 用工作区而不是整屏 (整屏会算成 250)
	w.Center()
	moves, _, _, _, _, _ = fake.wm.snapshot()
	if len(moves) != 2 || moves[1] != [2]int{300, 270} {
		t.Fatalf("居中落点 = %v, want 最后一次是 [300 270]", moves)
	}
	if x, y, _, _ := w.Bounds(); x != 300 || y != 270 {
		t.Fatalf("居中后 bounds = (%d,%d), want (300,270)", x, y)
	}
}

// TestWindowBoundsFallsBackWithoutBoundsProvider: 后端读不到位置时,
// bounds() 退回"最近一次记录的位置 + Surface 客户区尺寸", 而不是报 0。
func TestWindowBoundsFallsBackWithoutBoundsProvider(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P", X: 50, Y: 60, HasPos: true})

	// 配置里的位置先落进缓存 (Mount 里经 MoveTo)。
	if x, y, _, _ := w.Bounds(); x != 50 || y != 60 {
		t.Fatalf("配置位置未生效: (%d,%d)", x, y)
	}

	fake.mu.Lock()
	fake.noBounds = true
	fake.bx, fake.by, fake.bw, fake.bh = 0, 0, 0, 0
	fake.mu.Unlock()

	x, y, bw, bh := w.Bounds()
	if x != 50 || y != 60 || bw != 400 || bh != 300 {
		t.Fatalf("降级 bounds = (%d,%d,%d,%d), want (50,60,400,300)", x, y, bw, bh)
	}
}

// TestWindowEventMoveUpdatesCacheAndRedraws: 窗口被用户拖动时后端投一条
// EventMove, 位置缓存与重绘请求都要跟上。
func TestWindowEventMoveUpdatesCacheAndRedraws(t *testing.T) {
	f := newMgmtFactory(t)
	w, a, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	// 端到端 (经事件泵): 位置缓存更新, bounds() 读回新位置。
	pushAndPump(t, fake, a, Event{Kind: EventMove, X: 640, Y: 480})
	if x, y, _, _ := w.Bounds(); x != 640 || y != 480 {
		t.Fatalf("EventMove 后 bounds = (%d,%d), want (640,480)", x, y)
	}

	// 重绘请求: 直接调 dispatchEvent (不经泵) 才能观察到"还没来得及被冲刷"
	// 的脏标记 —— pump 会把脏帧立刻重绘掉, 从外面看是看不出来的。
	clearDirty(a)
	a.dispatchEvent(Event{Kind: EventMove, X: 8, Y: 9})
	if !needDraw(a) {
		t.Fatalf("位置变化应请求重绘 (跨屏时 Scale 变化正是靠这次重排被发现)")
	}
}

// TestWindowEventMoveFullChain: onMove 全链路 —— 事件 → 布局根处理器 → 脚本
// 更新文本。这是"脚本能感知窗口被挪走"的唯一入口。
func TestWindowEventMoveFullChain(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [pos, setPos] = createSignal("none");
		render(
			h("column", {
				onMove: (e) => setPos(e.x + "," + e.y),
			}, h("text", {font: 14}, () => pos())),
			{title: "M", width: 300, height: 200});
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	assertGlobalText(t, v, "none")

	a := appForSurface(fake)
	if a == nil {
		t.Fatalf("窗口未注册")
	}
	runPumpSteps(t, v, fake, []func(){
		func() { fake.push(Event{Kind: EventMove, X: 128, Y: 64}) },
		func() {
			assertGlobalText(t, v, "128,64")
			a.mu.Lock()
			x, y, has := a.posX, a.posY, a.hasPos
			a.mu.Unlock()
			if !has || x != 128 || y != 64 {
				t.Fatalf("位置缓存 = (%d,%d) has=%v, want (128,64) true", x, y, has)
			}
		},
	})
}

// ===== 层级 / 约束 / 缩放 / 全屏 / 激活 =====

// TestWindowLevelNormalization: 层级名归一, 未知值静默落到 normal (笔误不该
// 把窗口卡在一个不存在的层级上)。
func TestWindowLevelNormalization(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	if got := w.Level(); got != "normal" {
		t.Fatalf("初始层级 = %q, want normal", got)
	}
	w.SetLevel("top")
	w.SetLevel("bottom")
	w.SetLevel("nonsense")
	_, levels, _, _, _, _ := fake.wm.snapshot()
	want := []string{"top", "bottom", "normal"}
	if len(levels) != 3 || levels[0] != want[0] || levels[1] != want[1] || levels[2] != want[2] {
		t.Fatalf("后端收到的层级 = %v, want %v", levels, want)
	}
	if got := w.Level(); got != "normal" {
		t.Fatalf("未知层级之后 Level() = %q, want normal", got)
	}
}

// TestWindowLevelFromConfigSkipsNormal: 配置里给 normal (或缺省) 不该下发 ——
// "设成 normal" 在部分后端是一次多余的原生调用。
func TestWindowLevelFromConfigSkipsNormal(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P", Level: "nonsense"})
	_, levels, _, _, _, _ := fake.wm.snapshot()
	if len(levels) != 0 {
		t.Fatalf("归一到 normal 的层级不该下发后端, got %v", levels)
	}
	if got := w.Level(); got != "normal" {
		t.Fatalf("Level() = %q, want normal", got)
	}

	f2 := newMgmtFactory(t)
	_, _, fake2 := mountManaged(t, f2, nil, WindowConfig{Title: "P", Level: "top"})
	_, levels2, _, _, _, _ := fake2.wm.snapshot()
	if len(levels2) != 1 || levels2[0] != "top" {
		t.Fatalf("配置层级没下发: %v", levels2)
	}
}

// TestWindowSizeConstraintsClampNegative: 负数按"不约束"归零。
// 负的上限在各平台表现不一 (win32 会得到拖不动的怪窗口), 归一在 gfx 层最省事。
func TestWindowSizeConstraintsClampNegative(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	w.SetSizeConstraints(-5, 0, 800, -1)
	_, _, cons, _, _, _ := fake.wm.snapshot()
	if len(cons) != 1 || cons[0] != [4]int{0, 0, 800, 0} {
		t.Fatalf("约束 = %v, want [[0 0 800 0]]", cons)
	}

	// 配置路径: 四项全 0 时不下发 (什么都不约束 = 不用调后端)
	f2 := newMgmtFactory(t)
	_, _, fake2 := mountManaged(t, f2, nil, WindowConfig{Title: "P"})
	_, _, cons2, _, _, _ := fake2.wm.snapshot()
	if len(cons2) != 0 {
		t.Fatalf("无约束配置不该下发, got %v", cons2)
	}
}

// TestWindowSizeConstraintsFromConfig: 配置里的约束在首帧前落地。
func TestWindowSizeConstraintsFromConfig(t *testing.T) {
	f := newMgmtFactory(t)
	_, _, fake := mountManaged(t, f, nil, WindowConfig{
		Title: "P", MinWidth: 200, MinHeight: 150, MaxWidth: 900, MaxHeight: 700,
	})
	_, _, cons, _, _, _ := fake.wm.snapshot()
	if len(cons) != 1 || cons[0] != [4]int{200, 150, 900, 700} {
		t.Fatalf("约束 = %v, want [[200 150 900 700]]", cons)
	}
}

// TestWindowResizableAndFullscreen: 缩放开关与全屏开关的读回 + 下发。
//
// 两者是**两件事**: 前者是开关, 后者是"开着的钳位范围"。这里顺带证明
// 它们走的是两条独立通道 (改一个不影响另一个的读回值)。
func TestWindowResizableAndFullscreen(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	if !w.IsResizable() {
		t.Fatalf("缺省应可缩放")
	}
	if w.IsFullscreen() {
		t.Fatalf("缺省不该全屏")
	}
	w.SetResizable(false)
	w.SetFullscreen(true)
	if w.IsResizable() || !w.IsFullscreen() {
		t.Fatalf("读回 = resizable %v fullscreen %v, want false/true", w.IsResizable(), w.IsFullscreen())
	}
	_, _, _, res, full, _ := fake.wm.snapshot()
	if len(res) != 1 || res[0] != false {
		t.Fatalf("缩放下发 = %v, want [false]", res)
	}
	if len(full) != 1 || full[0] != true {
		t.Fatalf("全屏下发 = %v, want [true]", full)
	}

	// 配置路径: resizable=false / fullscreen=true 都在首帧前落地。
	f2 := newMgmtFactory(t)
	w2, _, fake2 := mountManaged(t, f2, nil, WindowConfig{Title: "P", NoResize: true, Fullscreen: true})
	if w2.IsResizable() || !w2.IsFullscreen() {
		t.Fatalf("配置未生效: resizable=%v fullscreen=%v", w2.IsResizable(), w2.IsFullscreen())
	}
	_, _, _, res2, full2, _ := fake2.wm.snapshot()
	if len(res2) != 1 || res2[0] != false || len(full2) != 1 || full2[0] != true {
		t.Fatalf("配置下发 = res %v full %v", res2, full2)
	}
}

// TestWindowActivate: 激活是模态 bump 依赖的原语, 单独验一次。
func TestWindowActivate(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})
	w.Activate()
	w.Activate()
	if _, _, _, _, _, act := fake.wm.snapshot(); act != 2 {
		t.Fatalf("激活次数 = %d, want 2 (不做去重 —— 每次都是用户意图)", act)
	}
}

// TestWindowManagerNoBackendSilent: 后端没有 windowManager 能力时全部静默
// 降级 (句柄侧的可读值仍然正确 —— 读回自己刚设的值不该依赖平台能力)。
func TestWindowManagerNoBackendSilent(t *testing.T) {
	SetDefaultFactory(nil)
	t.Cleanup(func() { SetDefaultFactory(nil) })

	bare := &bareSurface{}
	a := &app{surface: bare, root: mkColumn(mkButton("x")), fullDirty: true, dirtyNodes: map[*GuiNode]struct{}{}}
	registerApp(a)
	t.Cleanup(func() { unregisterApp(a) })
	w := a.handle()

	w.MoveTo(10, 20)
	w.SetLevel("top")
	w.SetSizeConstraints(100, 100, 0, 0)
	w.SetResizable(false)
	w.SetFullscreen(true)
	w.Activate()
	w.SetCursor("wait") // bareSurface 没有 cursorHost

	if w.Level() != "top" || w.IsResizable() || !w.IsFullscreen() {
		t.Fatalf("降级后可读值 = level %q resizable %v fullscreen %v",
			w.Level(), w.IsResizable(), w.IsFullscreen())
	}
	if x, y, _, _ := w.Bounds(); x != 10 || y != 20 {
		t.Fatalf("降级 bounds 位置 = (%d,%d), want (10,20)", x, y)
	}
}

// ===== 光标形状 =====

// TestNormalizeCursor: 形状名归一 (大小写不敏感 + 别名 + 未知值兜底)。
func TestNormalizeCursor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "default"},
		{"pointer", "pointer"},
		{"Pointer", "pointer"},
		{"POINTER", "pointer"},
		{"hand", "pointer"},
		{"ibeam", "text"},
		{"i-beam", "text"},
		{"busy", "wait"},
		{"hourglass", "wait"},
		{"forbidden", "not-allowed"},
		{"n-resize", "ns-resize"},
		{"col-resize", "col-resize"},
		{"none", "none"},
		{"沃土", "default"}, // 非 ASCII 不该 panic, 也不该被认成形状
		{"wat", "default"},
	}
	for _, c := range cases {
		if got := normalizeCursor(c.in); got != c.want {
			t.Fatalf("normalizeCursor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if !isKnownCursor("hand") || isKnownCursor("wat") {
		t.Fatalf("isKnownCursor 判定有误")
	}
}

// TestNodeCursorShapeResolution: 节点链上的光标判定顺序 ——
// 自身 prop > 祖先 prop > 标签缺省 (沿链) > default; 禁用链不给手型。
func TestNodeCursorShapeResolution(t *testing.T) {
	// 1) 标签缺省: 鼠标落在按钮的文字上, 也要给手型 (按钮是它的祖先)。
	btn := mkButton("A")
	if got := nodeCursorShape(btn.Children[0]); got != "pointer" {
		t.Fatalf("按钮内的 #text 光标 = %q, want pointer", got)
	}
	// 2) 输入框给 I 形光标。
	in := &GuiNode{Tag: "input", Props: map[string]object.Value{}}
	if got := nodeCursorShape(in); got != "text" {
		t.Fatalf("input 光标 = %q, want text", got)
	}
	// 3) 容器不给形状 —— 给容器手型会让整窗变手型。
	if got := nodeCursorShape(mkColumn(mkButton("x"))); got != "default" {
		t.Fatalf("column 光标 = %q, want default", got)
	}
	// 4) 自身 prop 最高优先 (含别名归一)。
	self := withStr(&GuiNode{Tag: "#text", Props: map[string]object.Value{}}, "cursor", "hand")
	if got := nodeCursorShape(self); got != "pointer" {
		t.Fatalf("自身 cursor prop 未生效: %q", got)
	}
	// 5) 自身没有时看最近的祖先 prop —— 且**就近**优先 (内层赢)。
	outer := withStr(mkColumn(mkButton("x")), "cursor", "crosshair")
	withStr(outer.Children[0], "cursor", "move")
	if got := nodeCursorShape(outer.Children[0]); got != "move" {
		t.Fatalf("内层 cursor prop 未覆盖外层: %q", got)
	}
	if got := nodeCursorShape(outer.Children[0].Children[0]); got != "move" {
		t.Fatalf("沿链应取最近的祖先 prop: %q", got)
	}
	// 6) 禁用链上的按钮不给手型 (手型承诺"点了有反应")。
	dis := withBool(mkButton("D"), "disabled", true)
	if got := nodeCursorShape(dis); got != "default" {
		t.Fatalf("禁用按钮光标 = %q, want default", got)
	}
	// 显式 prop 仍然尊重 (脚本说禁用时也要手型, 那是脚本的自由)。
	withStr(dis, "cursor", "not-allowed")
	if got := nodeCursorShape(dis); got != "not-allowed" {
		t.Fatalf("禁用按钮的显式 cursor = %q, want not-allowed", got)
	}
	// 7) nil 目标 → default (鼠标离开客户区)。
	if got := nodeCursorShape(nil); got != "default" {
		t.Fatalf("nil 目标 = %q, want default", got)
	}
}

// TestCursorAppliedOnHover: 悬停 → 光标下发; 窗口级覆盖优先且去重。
func TestCursorAppliedOnHover(t *testing.T) {
	root := mkColumn(mkButton("A"))
	a := mountTestAppWith(t, root, newFakeSurface(), 300, 200)
	fake, _ := a.surface.(*fakeSurface)

	btn := findFirst(root, "button")
	if btn == nil || btn.Box.W <= 0 {
		t.Fatalf("按钮没有尺寸, 布局没跑")
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if log := fake.cursorLog(); len(log) != 1 || log[0] != "pointer" {
		t.Fatalf("悬停按钮的光标日志 = %v, want [pointer]", log)
	}

	// 同一位置再来一次: 形状没变, 不该重复调平台 API (MouseMove 是最高频事件)。
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 3, Y: btn.Box.Y + 3})
	if log := fake.cursorLog(); len(log) != 1 {
		t.Fatalf("重复悬停不该重复下发: %v", log)
	}

	// 窗口级覆盖压过节点判定; 且鼠标底下是什么都不改光标。
	a.setCursorOverride("wait")
	if got := a.cursorOverrideShape(); got != "wait" {
		t.Fatalf("覆盖值 = %q, want wait", got)
	}
	pushAndPump(t, fake, a, Event{Kind: EventMouseMove, X: btn.Box.X + 4, Y: btn.Box.Y + 4})
	if log := fake.cursorLog(); len(log) != 2 || log[1] != "wait" {
		t.Fatalf("覆盖期间光标日志 = %v, want 末尾 wait", log)
	}

	// 清除覆盖 → 立刻回到 default (此刻鼠标底下是谁不在手边, 等下次移动校正)。
	a.setCursorOverride("")
	if got := a.cursorOverrideShape(); got != "" {
		t.Fatalf("覆盖未清除: %q", got)
	}
	if log := fake.cursorLog(); len(log) != 3 || log[2] != "default" {
		t.Fatalf("清除覆盖后光标日志 = %v, want 末尾 default", log)
	}
}

// TestWindowSetCursorViaHandle: 句柄上的 setCursor 收形状与清空两种写法。
func TestWindowSetCursorViaHandle(t *testing.T) {
	f := newMgmtFactory(t)
	w, a, fake := mountManaged(t, f, nil, WindowConfig{Title: "P"})

	w.SetCursor("busy") // 别名 → wait
	if got := fake.cursorLog(); len(got) != 1 || got[0] != "wait" {
		t.Fatalf("setCursor(busy) 日志 = %v, want [wait]", got)
	}
	w.SetCursor("wait") // 与上次同一形状 → 去重
	if got := fake.cursorLog(); len(got) != 1 {
		t.Fatalf("同形状不该重复下发: %v", got)
	}
	w.SetCursor("") // 清空
	if got := fake.cursorLog(); len(got) != 2 || got[1] != "default" {
		t.Fatalf("清空后日志 = %v, want 末尾 default", got)
	}
	if ov := a.cursorOverrideShape(); ov != "" {
		t.Fatalf("清空后覆盖值 = %q, want 空", ov)
	}
}

// ===== 模态子窗口 =====

// TestModalBlocksParentInput: 模态的核心语义 —— 父窗口收不到输入, 但窗口级
// 事件 (关闭/尺寸/移动/离场) 仍然通行。
func TestModalBlocksParentInput(t *testing.T) {
	f := newMgmtFactory(t)
	parentRoot := mkColumn(withClick(mkButton("P")))
	parent, parentApp, parentFake := mountManaged(t, f, parentRoot, WindowConfig{Title: "P"})
	child, childApp, childFake := mountManaged(t, f, mkColumn(mkButton("C")), WindowConfig{
		Title: "C", Modal: true, ModalParent: parent,
	})

	// ===== 关系 + 视角 =====
	if !child.IsModal() || child.ModalParent() != parent {
		t.Fatalf("子窗口的模态关系不对: isModal=%v parent=%v", child.IsModal(), child.ModalParent())
	}
	if parent.ModalChild() != child {
		t.Fatalf("父窗口的 modalChild 不对")
	}
	if !parent.IsBlocked() || child.IsBlocked() {
		t.Fatalf("被挡判定不对: parent=%v child=%v", parent.IsBlocked(), child.IsBlocked())
	}

	// ===== 输入被吞: 鼠标移到父窗口的按钮上, 悬停链不动 =====
	btn := findFirst(parentRoot, "button")
	if btn == nil || btn.Box.W <= 0 {
		t.Fatalf("父窗口按钮没有尺寸")
	}
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if n := hoverLen(parentApp); n != 0 {
		t.Fatalf("被模态挡住时父窗口悬停链 = %d, want 0", n)
	}
	// 按压态也不能留下 (否则解除遮挡后按钮还是"按住"的样子)。
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventMouseDown, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	parentApp.mu.Lock()
	pressed := len(parentApp.pressChain)
	parentApp.mu.Unlock()
	if pressed != 0 {
		t.Fatalf("被模态挡住时父窗口按压链 = %d, want 0", pressed)
	}

	// ===== 被吞的输入会把子窗口顶到前台 (否则用户看到的是"程序死了") =====
	if _, _, _, _, _, act := childFake.wm.snapshot(); act == 0 {
		t.Fatalf("被挡时的点击应激活模态子窗口")
	}

	// ===== 保留事件: 尺寸变化照常处理 =====
	before := shots(parentFake)
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventResize, W: 500, H: 420})
	if shots(parentFake) <= before {
		t.Fatalf("被模态挡住时 resize 仍应触发重绘 (否则看起来是卡死的白板)")
	}

	// ===== 子窗口自己不受影响 =====
	childBtn := findFirst(childApp.root, "button")
	pushAndPump(t, childFake, childApp, Event{Kind: EventMouseMove, X: childBtn.Box.X + 2, Y: childBtn.Box.Y + 2})
	if n := hoverLen(childApp); n == 0 {
		t.Fatalf("模态子窗口自己应能正常响应输入")
	}
}

// TestModalKeyboardBlocked: 键盘走的是另一条通道 (焦点链), 模态对它的屏蔽
// 要单独验一次 —— 否则"被挡住时还能往输入框里敲字"这类漏网不会被发现。
//
// 观测点是**重绘**: input 是**完全受控**的 (编辑结果只经 onInput 回给脚本,
// 不回写 value prop), 所以无 VM 的用例里读不到"文本变了"; 但任何一次被字段
// 消费的按键都会 markNodeDirty → 本帧重绘, 于是上屏次数就是"这次按键到底
// 进没进去"的可靠证据。
func TestModalKeyboardBlocked(t *testing.T) {
	f := newMgmtFactory(t)
	input := mkNode("input", nil)
	root := mkColumn(input)
	parent, parentApp, parentFake := mountManaged(t, f, root, WindowConfig{Title: "P"})

	// 先点击输入框拿到焦点 (点击 = MouseDown + MouseUp, 焦点在抬起阶段设置)。
	if input.Box.W <= 0 {
		t.Fatalf("输入框没有尺寸, 布局没跑")
	}
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventMouseDown, X: input.Box.X + 2, Y: input.Box.Y + 2})
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventMouseUp, X: input.Box.X + 2, Y: input.Box.Y + 2})
	parentApp.mu.Lock()
	focused := parentApp.focused
	parentApp.mu.Unlock()
	if focused != input {
		t.Fatalf("输入框未获焦: %v", focused)
	}

	// 基线: 未被挡住时按键进字段 → 标脏 → 重绘。
	before := shots(parentFake)
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventKeyDown, Key: "a"})
	if shots(parentFake) <= before {
		t.Fatalf("基线不成立: 未被挡住时按键应触发重绘 (%d → %d)", before, shots(parentFake))
	}

	child, _, _ := mountManaged(t, f, mkColumn(mkButton("C")), WindowConfig{
		Title: "C", Modal: true, ModalParent: parent,
	})

	// 被挡住: 键盘也得哑掉, 焦点更不能变。
	parentApp.mu.Lock()
	beforeFocus := parentApp.focused
	parentApp.mu.Unlock()
	before = shots(parentFake)
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventKeyDown, Key: "b"})
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventKeyUp, Key: "b"})
	if shots(parentFake) != before {
		t.Fatalf("被模态挡住时按键不该进字段 (%d → %d)", before, shots(parentFake))
	}
	if parentApp.focused != beforeFocus {
		t.Fatalf("被模态挡住时键盘不该改焦点")
	}

	// 关掉之后立刻恢复 (与关子窗口 → 父恢复可交互是同一条断言的两面)。
	child.Close()
	DrainTasks()
	before = shots(parentFake)
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventKeyDown, Key: "c"})
	if shots(parentFake) <= before {
		t.Fatalf("解除遮挡后按键应恢复 (%d → %d)", before, shots(parentFake))
	}
}

// TestModalOverwriteSemantics: 同一个父窗口再开一个模态子窗口时, 后开的
// 成为生效者, 先前那个降级为普通窗口 (模态是"一张", 不是栈)。
func TestModalOverwriteSemantics(t *testing.T) {
	f := newMgmtFactory(t)
	parent, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "P"})
	first, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "C1", Modal: true, ModalParent: parent})
	second, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "C2", Modal: true, ModalParent: parent})

	if parent.ModalChild() != second {
		t.Fatalf("生效的模态子窗口应是后开的那个")
	}
	if first.IsModal() {
		t.Fatalf("先开的模态子窗口应被降级")
	}
	if first.ModalParent() != nil {
		t.Fatalf("降级后不该还指着父窗口")
	}
	if !parent.IsBlocked() {
		t.Fatalf("父窗口仍应被挡着 (第二个子窗口还活着)")
	}
}

// TestModalDetachOnChildClose: 关掉子窗口, 父窗口立刻恢复可交互。
func TestModalDetachOnChildClose(t *testing.T) {
	f := newMgmtFactory(t)
	parentRoot := mkColumn(withClick(mkButton("P")))
	parent, parentApp, parentFake := mountManaged(t, f, parentRoot, WindowConfig{Title: "P"})
	child, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "C", Modal: true, ModalParent: parent})

	if !parent.IsBlocked() {
		t.Fatalf("前置条件: 父窗口应被挡着")
	}
	child.Close()
	DrainTasks() // Window.Close 经 Post 投递, 这里让它落地

	if parent.IsBlocked() || parent.ModalChild() != nil || child.IsModal() {
		t.Fatalf("关掉子窗口后父窗口应恢复: blocked=%v child=%v", parent.IsBlocked(), parent.ModalChild())
	}
	// 恢复后输入要真的通 —— 只看指针是自欺欺人。
	btn := findFirst(parentRoot, "button")
	pushAndPump(t, parentFake, parentApp, Event{Kind: EventMouseMove, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	if n := hoverLen(parentApp); n == 0 {
		t.Fatalf("解除遮挡后输入应恢复")
	}
}

// TestClosingParentClosesModalChild: 关父窗口连带关子窗口 (避免"永远置顶、
// 没人挡、关不掉"的孤儿窗口)。
func TestClosingParentClosesModalChild(t *testing.T) {
	f := newMgmtFactory(t)
	parent, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "P"})
	child, childApp, _ := mountManaged(t, f, nil, WindowConfig{Title: "C", Modal: true, ModalParent: parent})
	if child.ModalParent() != parent {
		t.Fatalf("前置条件: 子窗口应认得父窗口")
	}

	parent.Close()
	DrainTasks() // 父的关闭 Post 里又 Post 了子的关闭, DrainTasks 会一路排空

	childApp.mu.Lock()
	closed := childApp.closed
	childApp.mu.Unlock()
	if !closed {
		t.Fatalf("父窗口关闭后子窗口应跟着关掉")
	}
	// 连带关系也要断干净: 留着 modalParent 指向一个已关闭的父窗口, 会让
	// IsModal() 撒谎 (closed 只是标记, 指针还在)。
	if child.IsModal() || parent.ModalChild() != nil {
		t.Fatalf("关闭后模态关系未解: isModal=%v child=%v", child.IsModal(), parent.ModalChild())
	}
}

// TestModalIgnoresBadParent: 父句柄为 nil / 自己当自己父 时静默降级成普通窗口
// (脚本拿不到父句柄是常见疏忽, 不值得让整个窗口开不出来)。
func TestModalIgnoresBadParent(t *testing.T) {
	f := newMgmtFactory(t)
	w, _, _ := mountManaged(t, f, nil, WindowConfig{Title: "C", Modal: true})
	if w.IsModal() || w.ModalParent() != nil {
		t.Fatalf("没有父窗口时应退化成普通窗口")
	}
	// 自指: attachModal 直接返回, 不制造"自己被自己挡住"的死锁。
	self, selfApp, _ := mountManaged(t, f, nil, WindowConfig{Title: "S"})
	attachModal(selfApp, selfApp)
	if self.IsModal() || self.IsBlocked() {
		t.Fatalf("自指模态应被忽略")
	}
}

// TestModalFullChainFromScript: 全链路 —— 脚本里 modal: parent, 被挡期间点击
// 父窗口的按钮不生效, 关掉子窗口之后再点才生效。
//
// 这是模态最要命的那条断言: 前面的用例只看状态指针, 这里看**真实回调有没有跑**。
func TestModalFullChainFromScript(t *testing.T) {
	f := newMgmtFactory(t)
	v, err := vm.EvalVM(`
		import { createSignal } from "gx/solid";
		import { h, render } from "gx/gfx";
		const [n, setN] = createSignal(0);
		const parent = render(
			h("column", null,
				h("button", {onClick: () => setN(n() + 1)}, () => "count =" + n())),
			{title: "P", width: 400, height: 300});
		const child = render(
			h("column", null, h("text", {font: 14}, "modal")),
			{title: "C", width: 200, height: 150, modal: parent});
		globalThis.parentWin = parent;
		globalThis.childWin = child;
		globalThis.shutChild = () => { child.close(); };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	if len(f.made) != 2 {
		t.Fatalf("应开 2 个窗口, 实际 %d", len(f.made))
	}
	parentFake, childFake := f.surface(t, 0), f.surface(t, 1)
	parentRoot := rootOfSurface(t, parentFake)
	btn := findFirst(parentRoot, "button")
	if btn == nil || btn.Box.W <= 0 {
		t.Fatalf("父窗口按钮没有尺寸")
	}

	click := func() {
		parentFake.push(Event{Kind: EventMouseUp, X: btn.Box.X + 2, Y: btn.Box.Y + 2})
	}
	runPumpSteps(t, v, parentFake, []func(){
		func() { click() }, // 被模态挡着 → 不该生效
		func() {
			if got := parentCountText(parentRoot); got != "count =0" {
				t.Fatalf("被模态挡住时按钮竟然生效了: %q", got)
			}
			// 被吞的点击把子窗口顶到前台 (原生模态的常规行为)。
			if _, _, _, _, _, act := childFake.wm.snapshot(); act == 0 {
				t.Fatalf("被挡时的点击应激活模态子窗口")
			}
		},
		func() { callGlobalFn(t, v, "shutChild") }, // 关掉模态子窗口
		func() { click() },                         // 现在该生效了
		func() {
			if got := parentCountText(parentRoot); got != "count =1" {
				t.Fatalf("解除遮挡后点击未生效: %q", got)
			}
		},
	})
}

// parentCountText 取模态全链路用例里父窗口的计数文本。
func parentCountText(root *GuiNode) string {
	for _, n := range allNodes(root) {
		if n.Tag == "#text" && len(n.Text) >= 6 && n.Text[:5] == "count" {
			return n.Text
		}
	}
	return ""
}

// ===== 句柄 JS 面 =====

// TestWindowJSHandleManagementSurface: JS 句柄上的新方法全部能用, 并且
// 参数校验与 Go 侧口径一致 (缺参抛 TypeError, 非法值静默)。
func TestWindowJSHandleManagementSurface(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		const w = render(h("column", null), {title: "T", width: 400, height: 300});
		const attempt = (fn) => { try { fn(); return "accepted"; } catch (e) { return "threw"; } };
		globalThis.move = () => { w.moveTo(70, -30); };
		globalThis.pos = () => w.position().x + "," + w.position().y;
		globalThis.box = () => { const b = w.bounds(); return b.x + "," + b.y + "," + b.width + "," + b.height; };
		globalThis.lvl = () => { w.setLevel("wat"); return w.level(); };
		globalThis.cons = () => { w.setConstraints({minWidth: 150, maxHeight: 500}); };
		globalThis.badCons = () => attempt(() => w.setConstraints("nope"));
		globalThis.full = () => { w.setFullscreen(); return String(w.isFullscreen()); };
		globalThis.resizeOff = () => { w.setResizable(false); return String(w.isResizable()); };
		globalThis.cur = () => { w.setCursor("hand"); };
		globalThis.curOff = () => { w.setCursor(null); };
		globalThis.isModal = () => String(w.isModal());
		globalThis.act = () => { w.activate(); };
		globalThis.center = () => { w.center(); };
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	runPumpSteps(t, v, fake, []func(){
		func() {
			if got := callGlobalInspect(t, v, "isModal"); got != "false" {
				t.Fatalf("普通窗口 isModal() = %s", got)
			}
			if got := callGlobalInspect(t, v, "badCons"); got != "threw" {
				t.Fatalf("setConstraints(\"nope\") 应抛 TypeError: %s", got)
			}
			callGlobalFn(t, v, "move")
			if got := callGlobalInspect(t, v, "pos"); got != "70,-30" {
				t.Fatalf("position() = %s, want 70,-30", got)
			}
			if got := callGlobalInspect(t, v, "box"); got != "70,-30,400,300" {
				t.Fatalf("bounds() = %s, want 70,-30,400,300", got)
			}
			if got := callGlobalInspect(t, v, "lvl"); got != "normal" {
				t.Fatalf("setLevel(\"wat\") 之后 level() = %s, want normal", got)
			}
			callGlobalFn(t, v, "cons")
			if got := callGlobalInspect(t, v, "full"); got != "true" {
				t.Fatalf("setFullscreen() = %s, want true", got)
			}
			if got := callGlobalInspect(t, v, "resizeOff"); got != "false" {
				t.Fatalf("setResizable(false) = %s, want false", got)
			}
			callGlobalFn(t, v, "cur")
			callGlobalFn(t, v, "curOff")
			callGlobalFn(t, v, "act")
			callGlobalFn(t, v, "center")

			// 后端侧核对: 位置 / 约束 / 层级 / 光标都到了假后端。
			moves, levels, cons, res, full, act := fake.wm.snapshot()
			if len(moves) < 2 || moves[0] != [2]int{70, -30} {
				t.Fatalf("移动未下发: %v", moves)
			}
			if len(levels) != 1 || levels[0] != "normal" {
				t.Fatalf("层级未下发: %v", levels)
			}
			if len(cons) != 1 || cons[0] != [4]int{150, 0, 0, 500} {
				t.Fatalf("约束 = %v, want [[150 0 0 500]]", cons)
			}
			if len(res) != 1 || res[0] != false || len(full) != 1 || full[0] != true {
				t.Fatalf("缩放/全屏 = %v / %v", res, full)
			}
			if act != 1 {
				t.Fatalf("activate 次数 = %d, want 1", act)
			}
			if log := fake.cursorLog(); len(log) != 2 || log[0] != "pointer" || log[1] != "default" {
				t.Fatalf("光标日志 = %v, want [pointer default]", log)
			}
		},
	})
}

// TestWindowJSHandleMissingArgs: 缺参一律 TypeError 而不是静默 no-op
// (静默会让脚本里的笔误变成"功能莫名其妙不生效")。
func TestWindowJSHandleMissingArgs(t *testing.T) {
	fake := newFakeSurface()
	SetDefaultFactory(&fakeFactory{fake})
	t.Cleanup(func() { SetDefaultFactory(nil) })

	v, err := vm.EvalVM(`
		import { h, render } from "gx/gfx";
		const w = render(h("column", null), {title: "T", width: 200, height: 150});
		const attempt = (fn) => { try { fn(); return "accepted"; } catch (e) { return "threw"; } };
		globalThis.a = () => attempt(() => w.moveTo(1));
		globalThis.b = () => attempt(() => w.moveTo("x", "y"));
		globalThis.c = () => attempt(() => w.setLevel());
		globalThis.d = () => attempt(() => w.setConstraints());
		globalThis.e = () => attempt(() => w.setResizable());
	`)
	if err != nil {
		t.Fatalf("EvalVM: %v", err)
	}
	runPumpSteps(t, v, fake, []func(){
		func() {
			for _, name := range []string{"a", "b", "c", "d", "e"} {
				if got := callGlobalInspect(t, v, name); got != "threw" {
					t.Fatalf("%s 应抛 TypeError, got %s", name, got)
				}
			}
		},
	})
}
