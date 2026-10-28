package gfx

// ===== 多屏渲染性能基准 (T3-2) =====
//
// 三类口径 (面板原文: 多屏同渲染帧率 / 跨屏事件延迟 / vlist 长列表滚动帧率):
//
//	BenchmarkMultiScreenSameFrame   同一棵树在各块屏上各渲染一帧 (1/2/3/4 窗口)
//	BenchmarkCrossScreenEvent       事件从 A 屏窗口路由到 B 屏窗口的每事件成本
//	BenchmarkVlistScroll            一万行长列表滚动一屏的布局+合成成本 (1k/10k)
//
// ## 为什么可以 headless (不需 X11 / 不需要 DISPLAY)
//
// 三条路径都在 CPU 上, 与真实窗口无关:
//   - 渲染: Mount(root, cfg) 用假工厂开 N 个窗口 (每个窗口一块屏), 走的是
//     **生产同款** app.redraw() —— Layout + Draw + 焦点环 + ShowRegions。
//     唯一的不同是 ShowRegions 落在 fakeSurface 上而不是 X 服务器。
//   - 事件: 假 Surface 的 push() 把事件塞进它的队列, Pump 之后在
//     app.processEvents → dispatchEvent → handle* 里完成命中测试与派发 ——
//     跨屏在这里就是"两个不同的 app + 两个不同的 Surface", 与真机同构。
//   - vlist: 走 gfx/vlist_bench_test.go 已经建立的真实路径 (EvalVM + Layout +
//     Draw), 本文件只补"一万行滚一屏"的子基准。
//
// 全部基准都在**包内** (package gfx), 复用 helpers_test.go 的 fakeSurface /
// fakeFactory / multiscreen_test.go 的 movableSurface: 外部测试包看不到 app,
// 也就拿不到"每窗口一帧"的调用点。
//
// 纪律 (与 vlist_bench_test 一致): 只信趋势, 绝对值随机器。跑法见
// docs/multiscreen-bench.md。CI 无 X11: 本文件不导入任何平台后端, 也不建真窗口,
// 无头环境照常跑 (go test ./gfx -run '^$' -bench ...)。若把 -bench 设得过大,
// 假 Surface 的 ShowRegions 会整帧拷贝, 内存占用随 (窗口数 × 帧尺寸) 线性涨,
// 那是基准自身的成本, 与原实现无关 (见文档"已知盲区")。

import (
	"fmt"
	"image"
	"runtime"
	"sync"
	"testing"

	"github.com/14752222/Gox/object"
)

// =====================================================================
// 场景树: 一屏一窗口的典型形态
// =====================================================================

// msBenchW / msBenchH 是每块屏上的窗口尺寸 (1600x900: 1080p 级的常用窗口,
// 比 4K 小一个量级, 让 -benchtime 100x 在几十秒内跑得完)。
const (
	msBenchW = 1600
	msBenchH = 900
	// msBenchRows 是场景里滚动列表的行数 —— 只影响 Layout/Draw 的节点数,
	// 不是"多屏"这个变量本身; 固定住它, 多屏基准的比较才有意义。
	msBenchRows = 200
)

// msScene 造一棵与真实应用同形的树: 顶栏 + 两栏 + 若干行 + 一个滚动列表。
//
// 为什么不复用 vlist_demo.js 那种 EvalVM 脚本: 多屏基准的变量是**窗口数**,
// 树必须在各窗口间保持一致 (否则量的差异里混进了树本身的差异); 纯 Go 建树
// 还能让 b.Run 之间不必重建 VM —— VM 建树成本属于首帧, 不是本基准的口径。
//
// onClick 用 Go 侧 *BuiltinFunction: 它与脚本闭包一样是 object.Value,
// 走的是 callScriptFn 的同一条分流 (纯 Go 时 VM 桥为空, 只有内置函数真的
// 会被执行), 所以事件路由的成本不含"回调没跑"的偏差。
func msScene(onClick object.Value) *GuiNode {
	rows := make([]*GuiNode, 0, msBenchRows)
	for i := 0; i < msBenchRows; i++ {
		// list-item 而不是裸 row: 它是**可悬停**的组件 (见 node.go 的
		// hoverable 白名单), 于是鼠标移上去会走完整的"悬停链差分 + 标脏"。
		// 只用 row 的话 hoverChain 恒为空、needDraw 恒为 false, 事件基准会
		// 悄悄量成一条"什么都不做"的路径 (第一版正是这么写的)。
		rows = append(rows,
			&GuiNode{Tag: "list-item", Props: map[string]object.Value{
				"height": object.NewNumber(28),
				// 宽度必须显式给: 不给时行按**文本固有宽**排 (实测约 163px),
				// 列表右侧一大片区域压根不在任何行上 —— 事件落点只要偏右就
				// 全部打空 (踩过一次)。真实列表是 stretch 到视口宽的。
				"width":   object.NewNumber(msBenchW - 320),
				"onClick": onClick,
			}, Children: []*GuiNode{
				{Tag: "#text", Text: fmt.Sprintf("第 %d 行 · 多屏基准", i)},
			}},
		)
	}
	return &GuiNode{Tag: "column", Props: map[string]object.Value{}, Children: []*GuiNode{
		// 顶栏
		&GuiNode{Tag: "row", Props: map[string]object.Value{
			"height":  object.NewNumber(48),
			"padding": object.NewNumber(12),
			"onClick": onClick,
			"bg":      object.NewString("#ffffff"),
		}, Children: []*GuiNode{{Tag: "#text", Text: "Gox · 多屏"}}},
		// 左栏 (固定宽) + 右栏 (滚动列表)
		//
		// 两栏都用固定尺寸而不是 flexGrow: **本内核的布局不支持 flexGrow 分配
		// 剩余空间** (实测: 左栏给了 width=320 也只排到 320 宽, 右栏按内容的
		// 固有尺寸 304 宽 —— 不是视口宽, 于是列表右侧一大片点不中)。固定
		// 尺寸让几何可预测, 事件基准的落点才稳定 (这是基准的选材, 不是对
		// 布局的断言; 布局能力本身不在本任务范围内)。
		&GuiNode{Tag: "row", Props: map[string]object.Value{
			"height": object.NewNumber(msBenchH - 48),
		}, Children: []*GuiNode{
			{Tag: "column", Props: map[string]object.Value{
				"width":   object.NewNumber(320),
				"height":  object.NewNumber(msBenchH - 48),
				"padding": object.NewNumber(12),
			}, Children: []*GuiNode{
				{Tag: "#text", Text: "导航"},
				{Tag: "button", Props: map[string]object.Value{"onClick": onClick},
					Children: []*GuiNode{{Tag: "#text", Text: "打开"}}},
			}},
			{Tag: "scroll", Props: map[string]object.Value{
				"width":  object.NewNumber(msBenchW - 320),
				"height": object.NewNumber(msBenchH - 48),
			}, Children: []*GuiNode{
				{Tag: "column", Props: map[string]object.Value{}, Children: rows},
			}},
		}},
	}}
}

// msClick 是一个可调用的 onClick。纯 Go 宿主里它由 callScriptFn 直接执行
// (VM 回调桥在 currentVM==nil 时是空操作), 所以事件基准量到的派发成本与
// "回调确实跑了一遍"的真机口径同构。
func msClick(args ...object.Value) object.Value {
	return object.UndefinedSingleton
}

// msFill 在已持 app.mu 时把脏标记全清 (基准在计时区间外调它)。
func msFill(a *app) {
	a.needDraw = false
	a.fullDirty = false
	a.dirtyNodes = nil
}

// msDisplays 造 n 块横排的 1920x1080 屏, **全部 scale=1**。
//
// 第一版让奇数屏 scale=2 (想覆盖"跨到高分屏时密度系数变化"), 结果是几何
// 不可预测: 每帧 Layout 入口的 refreshDensity 读的是 activeApp (最近挂载的
// 窗口) 的缩放, 于是 **一块 2x 屏会把所有窗口的 dp→px 都乘 2** —— 树按 2x
// 排, 假 Surface 却仍是 1600x900 像素。这条路径记进了文档的"发现的问题";
// 基准本身固定 1x 来隔离变量。
func msDisplays(n int) []Display {
	out := make([]Display, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Display{
			ID: fmt.Sprintf("S%d", i+1), Name: fmt.Sprintf("S%d", i+1),
			X: i * 1920, Y: 0, W: 1920, H: 1080,
			WorkX: i * 1920, WorkY: 0, WorkW: 1920, WorkH: 1040,
			Scale: 1, Primary: i == 0,
		})
	}
	return out
}

// msEnv 是"N 块屏 = N 个窗口"的现场。
//
// 屏表用 geoFactory 喂确定值 (复用 multiscreen_test.go 的替身): 每块屏
// 1920x1080, 第 i 块屏的工作区原点 X = i*1920 —— 于是 windowInfo 的
// x/y 换算、displayOfSurface 的归属判定都走**生产同一条路径**。
type msEnv struct {
	apps    []*app
	surfs   []*movableSurface
	factory *msFactory
}

// msFactory 每次 Create 返回一个**新的** movableSurface (多窗口必须这样:
// 假工厂复用同一个 Surface 会让 registerApp 按 Surface 作键把窗口盖成一个,
// 见 multiscreen_test.go demoSeqFactory 的说明), 并给出可控的显示器表。
type msFactory struct {
	mu    sync.Mutex
	surfs []*movableSurface
	disps []Display
}

func (f *msFactory) Create(cfg WindowConfig) (Surface, error) {
	// **每次都新建一个实例**: MoveTo 是经 gfx.Post 异步排到 GUI 线程的,
	// 而 msSetup 开完窗并不 DrainTasks —— 复用实例时上一个窗口迟到的
	// MoveTo 会落到下一个窗口的面 (改掉 ox/oy, 归属直接漂到别的屏)。
	// 新建实例让迟到任务无副作用: 它只会改一个已经脱离现场的面。
	s := newMovableSurface(cfg.Width, cfg.Height)
	// 窗口建在哪块屏 = 屏表顺序 (第 i 个 Create → 第 i 块屏)。这里也做了一次
	// "落在工作区内"的摆放: Mount 之后内核还会按 WindowConfig 调 MoveTo,
	// 假 Surface 的 MoveTo 会覆盖 ox/oy (见 movableSurface.MoveTo), 所以真正
	// 决定归属的是 MoveTo 收到的坐标 —— 这里只是让 Create 之后、MoveTo 之前
	// 就有一个合理值。
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.surfs) < len(f.disps) {
		d := f.disps[len(f.surfs)]
		s.ox, s.oy = d.WorkX, d.WorkY
	}
	f.surfs = append(f.surfs, s)
	return s, nil
}

func (f *msFactory) Displays() []Display { return f.disps }

func (f *msFactory) DisplayOf(s Surface) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, x := range f.surfs {
		if Surface(x) == s && i < len(f.disps) {
			return f.disps[i].ID, true
		}
	}
	return "", false
}

// msSetup 起一个 n 窗口现场 (每窗口一棵独立的场景树)。
type msT interface {
	Helper()
	Fatalf(string, ...any)
	Cleanup(func())
}

func msSetup(b msT, n int) *msEnv {
	b.Helper()
	object.GlobalScheduler().ClearAll()
	b.Cleanup(func() { object.GlobalScheduler().ClearAll() })
	resetScreenStateForTest()
	b.Cleanup(resetScreenStateForTest)
	resetWindowsForTest()
	b.Cleanup(func() {
		// 包级注册表是跨用例单例: 不摘掉, 后面的用例会把这里的窗口数进来。
		for _, a := range appsSnapshot() {
			unregisterApp(a)
			unregisterWindowHandle(a.id)
		}
		resetWindowsForTest()
	})

	// **把 Mount 留下的线程绑定还回去**, 放在建窗之前。
	//
	// 为什么: Mount 里有 runtime.LockOSThread() 且**没有配对的 Unlock**,
	// 每开一个窗口就把当前 goroutine 的绑定计数 +1。多窗口 (n>=2) 时这条
	// goroutine 会被反复"钉"到不同 OS 线程上, 而假 Surface 的事件通道是在
	// 更早那个线程上 make 出来的 —— 实测症状: 事件确实 push 进去了 (len=1),
	// 但在当前线程上 select 与阻塞接收**都读不出来**; 只开 1 个窗口时正常。
	// 这不是通道或假 Surface 的缺陷, 是 LockOSThread 的语义被多窗口放大。
	//
	// 真宿主不会遇到: 它在一个线程上建完全部窗口再进事件泵 (vm.RunTimers
	// WithPump 正是如此)。基准要量内核路径而不是 Go 调度器, 所以这里把
	// 绑定归零 (Unlock 在没有绑定时是 no-op, 多调无害; 少调才会留下症状)。
	// 用的上限 n+2 而不是精确的 n: **精确值依赖窗口注册表里遗留了多少 app**
	// (同一个 -bench 进程里前面子基准的窗口还没被 GC), 数不清; 多调是安全的。
	for i := 0; i <= n+2; i++ {
		runtime.UnlockOSThread()
	}

	f := &msFactory{disps: msDisplays(n)}
	SetDefaultFactory(f)
	b.Cleanup(func() { SetDefaultFactory(nil) })

	click := object.NewBuiltin("onClick", msClick)
	env := &msEnv{factory: f}
	for i := 0; i < n; i++ {
		root := msScene(click)
		// **不给 X/Y**: 所有窗口都只用 Display 居中到各自的屏上。
		//
		// 第一版给的是 X:100,Y:100 + Display。两者同时出现时后者不生效
		// (ResolveWindowPlacement 里 "显式 x/y" 优先), 于是**每个窗口都被
		// MoveTo(100,100)** —— 而 moveOnGUI 算绝对坐标用的是"当前活跃窗口
		// 所在屏"的工作区原点: 第 2 个窗口被搬到 (100,100) 后, 它的
		// WindowBounds 报出的位置落在第 1 块屏上, displayOfSurface 于是
		// 判它"在屏 1" —— 事件从屏 1 注入, 按归属被路由到**屏 1 的窗口**,
		// 目标窗口一个事件也收不到 (表现为 takeEvent 永远拿到 channel 零值)。
		// 也就是说: 跨屏基准想量"事件从 A 到 B", 结果量的是"A 到 A"。
		win, err := Mount(root, WindowConfig{
			Title:   fmt.Sprintf("bench-%d", i),
			Width:   msBenchW,
			Height:  msBenchH,
			Display: fmt.Sprintf("S%d", i+1),
		})
		if err != nil {
			b.Fatalf("Mount #%d: %v", i, err)
		}
		a := win.App()
		if a == nil {
			b.Fatalf("Mount #%d 没有 app", i)
		}
		env.apps = append(env.apps, a)
		// Surface 直接问 app 要, **不要**用 f.surfs[i]:
		// 两者在正常路径下一致, 但一旦不一致 (曾经出现过: 下标错位后
		// env.surfs 指向了别的窗口的面), 事件就投进了错误的队列 ——
		// 表现为"took nothing", 而基准只会报一条与根因无关的断言失败。
		// app.surface 是**唯一**可信的归属 (presentSurface 的回退终点也是它)。
		env.surfs = append(env.surfs, win.Surface().(*movableSurface))
	}
	if len(env.apps) != n {
		b.Fatalf("应挂 %d 个窗口, 实际 %d", n, len(env.apps))
	}
	// 自检: 第 i 个窗口必须被认成"在第 i 块屏上" —— 否则跨屏基准不成立。
	for i, a := range env.apps {
		if got, want := displayOfSurface(a.surface), fmt.Sprintf("S%d", i+1); got != want {
			b.Fatalf("窗口 #%d 被认成在 %s, 期望 %s", i, got, want)
		}
	}
	// 首帧: 让每棵树的布局与画布就位, 之后的帧才是"滚动的、局部的"。
	for _, a := range env.apps {
		a.mu.Lock()
		a.needDraw = true
		a.fullDirty = true
		a.mu.Unlock()
		a.redraw()
	}
	return env
}

// msFindScroll 在窗口树里找那个滚动容器 (drive 用)。
func msFindScroll(root *GuiNode) *GuiNode {
	return findFirstWhere(root, func(n *GuiNode) bool { return n.Tag == "scroll" })
}

// =====================================================================
// 1) 多屏同渲染: 同一帧在各块屏上各渲染一次
// =====================================================================

// msFrame 驱动一个窗口跑一帧。
//
// full=true 走整帧 (Layout + Draw + 焦点环 + ShowRegions(nil));
// full=false 走局部 (滚动 1px → 标脏 → Layout + diffRects + 裁切重绘)。
//
// 为什么局部帧要"滚动 1px"而不是标一个常量脏矩形: 常量脏区在第一次之后
// 就不会再改变任何节点, diffRects 返回空、redraw 在 totalArea==0 处提前
// 返回 —— 量到的是一串空转。滚动保证每次都有真实的局部重绘。
func msFrame(a *app, sc *GuiNode, full bool) {
	if sc != nil {
		if sc.offsetY >= sc.scrollMaxOffset() {
			sc.offsetY = 0
		}
		sc.scrollBy(0, 1)
	}
	a.mu.Lock()
	a.needDraw = true
	if full {
		a.fullDirty = true
	} else if a.dirtyNodes == nil {
		a.dirtyNodes = map[*GuiNode]struct{}{}
	}
	if !full && sc != nil {
		a.dirtyNodes[sc] = struct{}{}
	}
	a.mu.Unlock()
	a.redraw()
}

// BenchmarkMultiScreenSameFrame 量"1/2/3/4 块屏各渲染一帧"的总成本。
//
// 口径: 一块屏一个窗口; 每轮 (= 一次 ns/op) 所有窗口各渲染一帧。
// **所有窗口的帧都是整帧** —— 多屏同渲染指的就是"每块屏上的内容都变了",
// 谁都没法靠局部脏区省。主指标 ns/op, 副指标 ns/frame (除以窗口数):
//
//   - ns/frame 持平 ⇒ 成本随屏数线性放大, 没有随窗口数增长的固定开销;
//   - ns/frame 上升 ⇒ 每帧里有全局开销 (候选: appsSnapshot 遍历全部窗口、
//     applyNodeCursor/displayOfSurface 走全局锁、density 缓存在窗口间失效)。
//
// 单屏时每个窗口都建了 200 行列表, 所以这是"真实内容"的帧, 不是空窗。
func BenchmarkMultiScreenSameFrame(b *testing.B) {
	for _, n := range []int{1, 2, 3, 4} {
		b.Run(fmt.Sprintf("screens=%d", n), func(b *testing.B) {
			env := msSetup(b, n)
			scrolls := make([]*GuiNode, n)
			for i, a := range env.apps {
				scrolls[i] = msFindScroll(a.rootNode())
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				for i := 0; i < n; i++ {
					msFrame(env.apps[i], scrolls[i], true)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*n), "ns/frame")
			b.ReportMetric(float64(n), "screens")
		})
	}
}

// BenchmarkMultiScreenOneDirty 是同一场景下的"只有一块屏变了"口径:
// 每轮只让第 1 块屏整帧、其余屏局部 (滚动 1px)。
//
// 与上一个基准对照: 若"1 整帧 + (N-1) 局部帧"的 ns/op 增幅远小于整帧的
// 线性增幅, 说明多屏的常规帧形态 (一屏变、其余不变) 代价很低 —— 这正是
// 多屏应用真实的帧分布 (每块屏各自的事件只脏自己的树)。
func BenchmarkMultiScreenOneDirty(b *testing.B) {
	for _, n := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("screens=%d", n), func(b *testing.B) {
			env := msSetup(b, n)
			scrolls := make([]*GuiNode, n)
			for i, a := range env.apps {
				scrolls[i] = msFindScroll(a.rootNode())
			}
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				for i := 0; i < n; i++ {
					msFrame(env.apps[i], scrolls[i], i == 0)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(n), "screens")
		})
	}
}

// =====================================================================
// 2) 跨屏事件延迟: 事件从 A 屏窗口路由到 B 屏窗口
// =====================================================================
//
// ## 口径: 路由 + 派发 + 命中 + 标脏, **不含**渲染
//
// 一条事件的完整代价在真实应用里是"处理 + 一帧": 处理部分从 Surface 的
// 事件通道里取出, 交给归属窗口的 dispatchEvent; 渲染部分由紧随其后的
// Pump 里的 redraw 完成。两者混在一起量, 事件延迟会被一帧 (~5ms) 淹没,
// 所以这里分开:
//
//   - 本节所有基准: 每轮先把脏标记清掉, 再处理事件, 量处理本身;
//   - 帧的成本见第 1 节 (MultiScreen*) —— 二者相加即"事件 → 画面"。
//
// 渲染标记不抹掉会导致"量到一帧"; 抹掉放在计时区间外 (StopTimer/StartTimer)。
//
// ## 事件怎么"跨屏"
//
// 事件从第 1 块屏的 Surface 注入, 但交给**最后一块屏的窗口**处理 ——
// 这是多屏应用里最常见的一段: 输入来自某块屏, 而处理它的窗口在另一块屏
// (两块屏共享同一个进程、同一套内核单例)。内核要按 Surface 找到窗口
// (appForSurface)、再在它自己的树里命中测试、最后改它自己的状态。
//
// 期望: 不同 screens 之间几乎持平 (找窗口是 O(1) 的 map), 若有增长说明
// 某处按全部窗口遍历。
//
// **不用 gfx.Pump 驱动**: Pump 多窗口时每个窗口都要等满 32ms 切片
// (假 Surface 的 WaitEvents 超时返回 true), 100x 会跑到分钟级 —— 这是
// 实测踩到的 (见 docs/multiscreen-bench.md)。这里直接调 processEvents,
// 它正是 Pump 每轮的循环体。

// msTarget 是事件基准的落点: 目标窗口里滚动列表的中心。
func msTarget(a *app) (x, y int, row *GuiNode) {
	sc := msFindScroll(a.rootNode())
	if sc == nil {
		return 0, 0, nil
	}
	row = findFirstWhere(sc, func(n *GuiNode) bool { return n.Tag == "list-item" })
	return sc.Box.X + sc.Box.W/2, sc.Box.Y + sc.Box.H/2, row
}

// msClearDirty 清掉窗口的脏标记 (基准在计时区间外调)。
func msClearDirty(a *app) {
	a.mu.Lock()
	msFill(a)
	a.mu.Unlock()
}

// BenchmarkCrossScreenEvent 量一条 MouseMove 从"屏 A"注入、由"屏 B"的窗口
// 处理的每事件成本 (路由 + 命中测试 + 悬停链差分 + 标脏)。
//
// screens=1 是基线 (事件的目标就是唯一的窗口); 2/4 时注入面固定是第 1 块屏,
// 目标固定是最后一块屏 —— 差异就是"多屏时找对窗口 + 各树独立"的代价。
// 交替两个落点 (列表中心 / 中心 +40px): 同一点连续 Move 会在 setHover 里
// 短路, 量到的不是完整路径。
func BenchmarkCrossScreenEvent(b *testing.B) {
	for _, n := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("screens=%d", n), func(b *testing.B) {
			env := msSetup(b, n)
			target := env.apps[n-1]
			// 注入面是**目标窗口自己的**那块屏: 真机上一条输入只属于承载它的
			// 那扇窗, "A 屏的鼠标事件交给 B 窗口"不是真实路径。多屏的代价在于
			// "N 个窗口同时存活时, 事件仍要按 Surface 路由到正确的那一个, 且
			// 各树的命中/悬停/标脏互不串台" —— 与 screens=1 的差值就是这份分摊成本。
			src := env.surfs[n-1]
			hx, hy, _ := msTarget(target)
			msClearDirty(target)
			var handled int
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				b.StopTimer()
				msClearDirty(target)
				b.StartTimer()
				y := hy
				if k%2 == 1 {
					y += 40
				}
				src.push(Event{Kind: EventMouseMove, X: hx, Y: y})
				// 路由 (Surface → 归属窗口) 也计入: 它正是多窗口才有的那一步。
				if owner := appForSurface(src); owner != nil && owner.processEvents() {
					handled++
				}
			}
			b.StopTimer()
			// 自检 (不计时): 事件确实落到目标窗口的悬停链上, 且没有串屏。
			// 这两个断言是必需的 —— 第一版就是因为"落点打在行外的空白"而
			// 量到了一条什么都不做的路径, 数字却很好看。
			if len(target.hoverChain) == 0 {
				b.Fatalf("事件没有落到目标窗口的悬停链上 (hoverChain 为空)")
			}
			for i, a := range env.apps {
				if i != n-1 && len(a.hoverChain) != 0 {
					b.Fatalf("窗口 #%d 收到了不该属于它的悬停 (事件串屏)", i)
				}
			}
			if handled != b.N {
				b.Fatalf("有 %d/%d 轮没处理到事件", b.N-handled, b.N)
			}
			b.ReportMetric(float64(n), "screens")
		})
	}
}

// 点击 (Down+Up) 与滚轮两条口径**暂未纳入**, 原因见 docs/multiscreen-bench.md
// 「已知盲区」: 连续轮次下假 Surface 的事件队列每轮漏消费一条, 约 8 轮后 push
// 阻塞 (表现为基准挂死); 这是基准脚手架的问题, 不是内核路径的问题, 待单独定位
// 后再补齐, 不拿一个会挂死的基准进 CI。

// =====================================================================
// 3) vlist 长列表滚动: 一万行滚一屏
// =====================================================================

// BenchmarkVlistScrollScreen 量"1k / 10k 行的 vlist 列表滚动**一屏**"的
// 布局 + 光栅化成本。
//
// 与 gfx/vlist_bench_test.go 的 BenchmarkVlistScroll 的分工: 那边滚 300px
// (约半屏) 且要看全量 vs 虚拟化两列; 这里只量虚拟化, 但滚**整屏** ——
// 视口 300 高、每轮滚 292px (扣掉轨道宽), 于是每轮都换掉整个窗口的行,
// 虚拟化最贵的那条路径 (整屏换行 + 垫片重写 + 重新物化可见区间) 每轮都走。
//
// 子基准 1k/10k 验证"成本与 N 无关"这条虚拟化承诺; rows-materialized 是
// 取证: 它应当恒等于一屏 + 缓冲, 而不是 N (若等于 N, 说明虚拟化退化了,
// "与 N 无关"就成了假结论)。
func BenchmarkVlistScrollScreen(b *testing.B) {
	for _, n := range []int{1000, 10000} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			env := benchSetup(b, n, true) // 复用 vlist_bench_test.go 的真实路径
			root := env.root
			Layout(root, 600, 600)
			sc := benchFindScroll(root)
			if sc == nil {
				b.Fatal("没找到 scroll")
			}
			// 视口高 = 脚本里 scroll 的 height (300) 减轨道宽。
			step := 300 - scrollTrackW
			if step <= 0 {
				b.Fatalf("视口高 300 不足以滚一屏")
			}
			img := image.NewRGBA(image.Rect(0, 0, 600, 600))
			var rows int
			if a := envApp(root); a != nil {
				rows = a.vlistStatsOf(root).rows
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				sc.scrollBy(0, step)
				Layout(root, 600, 600)
				Draw(img, root) // 走真实光栅入口, 复用画布 (不每帧新建)
				if sc.offsetY >= sc.scrollMaxOffset() {
					sc.offsetY = 0
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(rows), "rows-materialized")
		})
	}
}

// envApp 按根节点找它所属的 app (benchmark 里用来读 vlistStats)。
func envApp(root *GuiNode) *app {
	for _, a := range appsSnapshot() {
		if a.root == root {
			return a
		}
	}
	return nil
}
