//go:build darwin

// 窗口管理 (§四 窗口/系统缺口) 的真机验证: 建窗落点 / bounds() 往返 /
// moveTo() / EventMove 的**投递时机**。
//
// 场景纪律与 cocoa_e2e_test.go 一致: 场景在 TestMain 的主线程上跑 (AppKit
// 硬性要求主线程), 结果记进包级变量, Test 函数只断言。
//
// ## 这里唯一值得真机跑的是"坐标系 + 单位"
//
// AppKit 是**底左原点 + 点**, gfx 全栈 (与 win32 的 GetWindowRect / X11 的
// root 坐标) 是**左上原点 + 设备像素**。两组换算都是纯算术, 但错一个乘除
// 方向都不会报警 —— 单屏 Retina 上表现为"窗口跳到屏幕外"或"停在别处,
// 差一倍", 而编译器与 go vet 对此完全无感。所以断言用的是
// "设 60/90 → 读回 60/90" 这种往返, 而不是"窗口创建成功"。
//
// ## 顺带钉住"建窗期不投 EventMove"
//
// AppKit 在 initWithContentRect: 与 center 上都会发 windowDidMove。若照单
// 全收, 所有"建窗后按顺序收头 N 个事件"的调用方都会平白多收一串
// EventMove (实测: 本包的 IME 与鼠标键盘两个真机场景就因此断言错位)。
//
// ## 第三个场景: 缩放比 (backingScaleFactor) 的保鲜
//
// 位置的单位换算已在 M4 合流时收归 gfx 层 (Display.PosInPoints), 但**客户区
// 尺寸与输入坐标**仍由 cocoa 后端自己乘/除 scale。这份缓存一旦过期 (窗口被拖到
// 缩放不同的屏上), 表现是"点不准 + 尺寸读回错单位 + Retina 上发虚", 都不报错。
//
// 单屏机器上没法真插一块缩放不同的屏, 于是直接驱动 applyBackingScale —— 它正是
// windowDidChangeBackingProperties: 回调里跑的那段。钩子本身是否挂上, 用运行时
// 的 respondsToSelector: 问 (而不是去 grep 源码)。
package cocoa

import (
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

// 建窗落点与 moveTo 的目标。
//
// 口径 (2026-10-02 合流后): `WindowConfig.X/Y` 与 `moveTo` 都是**相对目标显示器
// 工作区**的偏移 (设备像素), 内核经 `gfx.ResolveWindowPlacement` 换算成**绝对
// 坐标**才交给后端; cocoa 的 `WindowBounds` 回的也是绝对坐标 (点)。
// 所以下面断言的是"内核要求的绝对落点"与"回读的绝对坐标"互逆, 而不是直接拿
// 请求值当期望 —— 两者差着一个工作区原点 (macOS 是菜单栏的高度)。
const (
	wmPlaceX, wmPlaceY = 60, 90
	wmMoveX, wmMoveY   = 140, 210
)

// nearPx 容差: 平台可能把外框原点对齐到设备像素栅格, 允许 2 像素偏差。
// 真正的 bug 类 (scale 乘除写反) 偏差是"一倍", 这个容差拦不住任何真问题。
func nearPx(got, want int) bool {
	d := got - want
	return d >= -2 && d <= 2
}

var (
	wmFail       error // 场景执行失败
	wmScale      float64
	wmPosOK      bool // 建窗落点 + bounds() 往返
	wmPlaceRead  [4]int
	wmPlaceExp   [2]int // 内核要求的绝对落点 (ResolveWindowPlacement 的结果)
	wmPlaceMoves int    // 建窗期收到的 EventMove 个数 (应为 0)
	wmMovePosOK  bool   // moveTo 之后 bounds() 读回
	wmMoveRead   [2]int
	wmMoveEvOK   bool // moveTo 之后收到 EventMove 且坐标对得上
	wmMoveEvs    int  // moveTo 之后收到的 EventMove 个数
)

// runWindowScenario 在主线程上执行窗口管理场景。必须在主 goroutine 调用。
func runWindowScenario() error {
	cfg := gfx.WindowConfig{
		Title: "cocoa-win", Width: 320, Height: 240,
		X: wmPlaceX, Y: wmPlaceY,
	}
	s0, err := newSurface(cfg)
	if err != nil {
		return err
	}
	s := s0.(*surface)
	wmScale = s.scale
	defer func() {
		s.win.Send(selOrderOut, objc.ID(0))
		unregSurface(s.view, s.delegate)
	}()

	// 1) 建窗落点: 内核要求 (工作区相对 → 绝对) 与回读 (点, 左上原点) 必须互逆 ——
	// 这就是 appKitOriginX/Y 与 gfxOriginY 这一对换算的验证。
	expX, expY, expOK := gfx.ResolveWindowPlacement(cfg)
	wmPlaceExp = [2]int{expX, expY}
	x, y, w, h := s.Bounds()
	wmPlaceRead = [4]int{x, y, w, h}
	wmPosOK = expOK && nearPx(x, expX) && nearPx(y, expY) && w > 0 && h > 0

	// 2) 建窗期 (initWithContentRect: / 上屏) 不该投 EventMove。
	placeMoves, _ := collectMoves(s, 250*time.Millisecond)
	wmPlaceMoves = len(placeMoves)

	// 3) moveTo: 位置与事件都要对。
	s.MoveTo(wmMoveX, wmMoveY)
	moves, _ := collectMoves(s, 600*time.Millisecond)
	x2, y2, _, _ := s.Bounds()
	wmMoveRead = [2]int{x2, y2}
	wmMovePosOK = nearPx(x2, wmMoveX) && nearPx(y2, wmMoveY)
	wmMoveEvs = len(moves)
	if len(moves) > 0 {
		last := moves[len(moves)-1]
		wmMoveEvOK = nearPx(last.X, wmMoveX) && nearPx(last.Y, wmMoveY)
	}
	return nil
}

// collectMoves 排空事件通道直到 budget 用尽, 返回 (EventMove 序列, 其它事件数)。
//
// 中间用 WaitEvents 跑几轮 run loop: windowDidMove 是 AppKit 内部的通知
// 投递, 不跑 run loop 不保证送达 (与 sendEvent: 那种同步 IMP 调用不同)。
func collectMoves(s *surface, budget time.Duration) ([]gfx.Event, int) {
	var moves []gfx.Event
	others := 0
	deadline := time.Now().Add(budget)
	for {
		for {
			select {
			case ev := <-s.Events():
				if ev.Kind == gfx.EventMove {
					moves = append(moves, ev)
				} else {
					others++
				}
				continue
			default:
			}
			break
		}
		if time.Now().After(deadline) {
			return moves, others
		}
		s.WaitEvents(10 * time.Millisecond)
	}
}

// TestCocoaWindowGeomAndMoveEvent 断言窗口几何往返与 EventMove 的投递时机。
func TestCocoaWindowGeomAndMoveEvent(t *testing.T) {
	if wmFail != nil {
		t.Fatalf("窗口管理场景失败: %v", wmFail)
	}
	if !wmPosOK {
		t.Errorf("建窗落点 (相对 工作区 %d,%d ⇒ 期望绝对 %d,%d) 与 bounds() 读回 (x=%d y=%d w=%d h=%d) 不互逆 "+
			"(坐标系换算 或 单位乘除写反; scale=%g)",
			wmPlaceX, wmPlaceY, wmPlaceExp[0], wmPlaceExp[1],
			wmPlaceRead[0], wmPlaceRead[1], wmPlaceRead[2], wmPlaceRead[3], wmScale)
	}
	if wmPlaceMoves != 0 {
		t.Errorf("建窗期收到 %d 个 EventMove, want 0 (假移动会顶掉调用方的头几个事件)", wmPlaceMoves)
	}
	if !wmMovePosOK {
		t.Errorf("moveTo(%d, %d) 之后 bounds() 读回 (x=%d y=%d)",
			wmMoveX, wmMoveY, wmMoveRead[0], wmMoveRead[1])
	}
	if wmMoveEvs == 0 {
		t.Error("moveTo 之后没收到 EventMove (移动没上报 / run loop 没跑到通知)")
	}
	if !wmMoveEvOK {
		t.Error("EventMove 的 X/Y 与目标位置不符 (窗口坐标是屏幕绝对坐标, 设备像素)")
	}
}

// ===== 场景三: 缩放比 (backing scale) 的保鲜 =====

var selChangeBacking = objc.RegisterName("windowDidChangeBackingProperties:")

// 建窗尺寸 (设备像素) / 约束 (设备像素) / 输入探针坐标 (点)。
// 取值都偏小: 免得约束或改尺寸撞上 AppKit 的"窗口必须留在可见区内"钳位,
// 把断言搅浑。
const (
	bsWantW, bsWantH   = 320, 240
	bsMinW, bsMinH     = 100, 80
	bsResW, bsResH     = 400, 300
	bsProbeX, bsProbeY = 10, 20
)

var (
	bsFail error

	bsBaseScale  float64 // 建窗时缓存的比值
	bsCurScaleOK bool    // 缓存 == 窗口自己的 backingScaleFactor (建窗落点后对齐过)
	bsHooked     bool    // delegate 认 windowDidChangeBackingProperties:

	bsBaseSize  [2]int // 建窗后的客户区设备像素尺寸
	bsBaseBnd   [4]int
	bsBaseMinPt float64
	bsBaseDevX  int // postDevice 探针: 输入坐标被换算成多少设备像素
	bsBaseDevY  int

	bsDblScale   float64
	bsDblSize    [2]int
	bsDblBnd     [4]int
	bsDblMinPt   float64
	bsDblDevX    int
	bsDblDevY    int
	bsDblResizes int
	bsDblResW    int
	bsDblResH    int
	bsDblMoves   int
	bsDblMoveEv  gfx.Event

	bsResPt     float64 // ResizeClient 之后客户区的**点**尺寸
	bsResPtWant float64
	bsResSize   [2]int

	bsBackSize [2]int
)

// runBackingScaleScenario 在主线程上执行缩放比场景。必须在主 goroutine 调用。
func runBackingScaleScenario() error {
	s0, err := newSurface(gfx.WindowConfig{
		Title: "cocoa-scale", Width: bsWantW, Height: bsWantH,
		X: wmPlaceX, Y: wmPlaceY,
	})
	if err != nil {
		return err
	}
	s := s0.(*surface)
	defer func() {
		s.win.Send(selOrderOut, objc.ID(0))
		unregSurface(s.view, s.delegate)
	}()

	bsBaseScale = s.scale
	bsCurScaleOK = s.currentScale() == bsBaseScale
	bsHooked = objc.ID(s.delegate).Send(selResponds, selChangeBacking) != 0

	bsBaseBnd = bounds4(s)
	bw, bh := s.Size()
	bsBaseSize = [2]int{bw, bh}

	// 约束以设备像素给进, 后端立刻按基准比值换成点落地 (minSize 读回点值)。
	s.SetSizeConstraints(bsMinW, bsMinH, 0, 0)
	bsBaseMinPt = readMinSizePt(s)
	bsBaseDevX, bsBaseDevY = probeDeviceConversion(s)

	drainEvents(s, 250*time.Millisecond) // 清掉建窗期残留

	// ---- 比值翻倍 (等价于窗口被挪到缩放翻倍的屏上) ----
	k := bsBaseScale * 2
	bsDblScale = k
	s.applyBackingScale(k)

	dw, dh := s.Size()
	bsDblSize = [2]int{dw, dh}
	bsDblBnd = bounds4(s)
	bsDblMinPt = readMinSizePt(s)

	// 先把补报的事件收干净, 再投输入探针 —— 探针会**丢弃**排在它前面的事件
	// (它扫到 MouseMove 就返回), 顺序反过来会把 Resize/Move 吃掉。
	for _, ev := range drainEvents(s, 400*time.Millisecond) {
		switch ev.Kind {
		case gfx.EventResize:
			bsDblResizes++
			bsDblResW, bsDblResH = ev.W, ev.H
		case gfx.EventMove:
			bsDblMoves++
			bsDblMoveEv = ev
		}
	}
	bsDblDevX, bsDblDevY = probeDeviceConversion(s)

	// ---- 出方向: ResizeClient 收设备像素, 落到 AppKit 要除**新**比值 ----
	s.ResizeClient(bsResW, bsResH)
	bsResPt = objc.Send[nsRect](s.win.Send(selContentView), selBounds).Size.Width
	bsResPtWant = float64(bsResW) / k
	drainEvents(s, 400*time.Millisecond) // 让 windowDidResize: 走完
	rw, rh := s.Size()
	bsResSize = [2]int{rw, rh}

	// ---- 还原 ----
	s.applyBackingScale(bsBaseScale)
	nw, nh := s.Size()
	bsBackSize = [2]int{nw, nh}
	return nil
}

// bounds4 把 Bounds() 的四元组收成数组 (断言时按下标一套写完)。
func bounds4(s *surface) [4]int {
	x, y, w, h := s.Bounds()
	return [4]int{x, y, w, h}
}

// readMinSizePt 读窗口约束的 minSize 宽度 (点)。断言"约束是否跟着比值重推"用的
// 是它 —— 内核给的是设备像素, 落到 AppKit 是点, 偏一倍会算错窗口最小尺寸。
func readMinSizePt(s *surface) float64 {
	return objc.Send[nsSize](s.win, selMinSize).Width
}

// probeDeviceConversion 投一个"点坐标"输入事件, 读回它被换算成的设备像素 ——
// 这是鼠标/滚轮命中测试用的换算, 也是用户最能直接感觉到的一处 (比值错了就是
// "点不准", 且不报错)。取不到则返回 (-1,-1)。
//
// **调用时机**: 它扫到 MouseMove 就返回, 排在它前面的事件会被丢弃 —— 要在
// 队列已排空 (或前面的事件已经收完) 时再调。
func probeDeviceConversion(s *surface) (int, int) {
	s.postDevice(gfx.Event{Kind: gfx.EventMouseMove, X: bsProbeX, Y: bsProbeY})
	for {
		select {
		case ev := <-s.Events():
			if ev.Kind == gfx.EventMouseMove {
				return ev.X, ev.Y
			}
		default:
			return -1, -1
		}
	}
}

// drainEvents 排空事件通道直到 budget 用尽, 按到达顺序返回。
func drainEvents(s *surface, budget time.Duration) []gfx.Event {
	var out []gfx.Event
	deadline := time.Now().Add(budget)
	for {
		for {
			select {
			case ev := <-s.Events():
				out = append(out, ev)
				continue
			default:
			}
			break
		}
		if time.Now().After(deadline) {
			return out
		}
		s.WaitEvents(10 * time.Millisecond)
	}
}

// nearPt 点坐标容差: AppKit 会把尺寸/原点对齐到设备像素栅格 (Retina 上是半点),
// 允许 1 点。真 bug 类 (比值乘除反向) 的偏差是"一倍"。
func nearPt(got, want float64) bool {
	d := got - want
	return d >= -1 && d <= 1
}

// TestCocoaBackingScaleRefresh 断言"比值一变, 所有设备像素口径的量都跟着变"。
func TestCocoaBackingScaleRefresh(t *testing.T) {
	if bsFail != nil {
		t.Fatalf("缩放比场景失败: %v", bsFail)
	}
	if !bsHooked {
		t.Error("delegate 不认 windowDidChangeBackingProperties: —— " +
			"跨屏后缩放比永远刷不新, 之后输入的坐标与尺寸换算都停在旧单位")
	}
	if !bsCurScaleOK {
		t.Error("建窗后缓存的比值 != 窗口自己的 backingScaleFactor —— " +
			"落点没对齐 (笔记本 Retina 主屏 + 外接 1080p 时整块屏的鼠标坐标差一倍)")
	}
	if !nearPx(bsBaseSize[0], bsWantW) || !nearPx(bsBaseSize[1], bsWantH) {
		t.Errorf("建窗后客户区 = %v 设备像素, want %dx%d (落点对齐不该改掉脚本要的尺寸)",
			bsBaseSize, bsWantW, bsWantH)
	}

	// 比值翻倍: 客户区的**点**尺寸不变, 设备像素翻倍。
	if !nearPx(bsDblSize[0], bsBaseSize[0]*2) || !nearPx(bsDblSize[1], bsBaseSize[1]*2) {
		t.Errorf("比值翻倍后 Size() = %v, want ≈ %v 的两倍 (客户区设备像素尺寸没跟着改)",
			bsDblSize, bsBaseSize)
	}
	wantBnd := [4]int{bsBaseBnd[0] * 2, bsBaseBnd[1] * 2, bsBaseBnd[2] * 2, bsBaseBnd[3] * 2}
	for i := range wantBnd {
		if !nearPx(bsDblBnd[i], wantBnd[i]) {
			t.Errorf("比值翻倍后 bounds()[%d] = %d, want ≈ %d (换算没跟着走)", i, bsDblBnd[i], wantBnd[i])
		}
	}

	// 输入坐标: postDevice 是"点 → 设备像素", 比值变了就必须跟着变。
	wantBaseDevX, wantBaseDevY := int(float64(bsProbeX)*bsBaseScale), int(float64(bsProbeY)*bsBaseScale)
	if bsBaseDevX != wantBaseDevX || bsBaseDevY != wantBaseDevY {
		t.Errorf("基准比值下输入 (%d,%d) 点被换算成 (%d,%d), want (%d,%d)",
			bsProbeX, bsProbeY, bsBaseDevX, bsBaseDevY, wantBaseDevX, wantBaseDevY)
	}
	wantDblDevX, wantDblDevY := int(float64(bsProbeX)*bsDblScale), int(float64(bsProbeY)*bsDblScale)
	if bsDblDevX != wantDblDevX || bsDblDevY != wantDblDevY {
		t.Errorf("比值翻倍后输入 (%d,%d) 点被换算成 (%d,%d), want (%d,%d) —— "+
			"命中的是错位置 (点不准, 且不报错)",
			bsProbeX, bsProbeY, bsDblDevX, bsDblDevY, wantDblDevX, wantDblDevY)
	}

	// 约束: 点值必须按新比值重推, 否则窗口最小尺寸整体偏一倍。
	wantMinPt := float64(bsMinW) / bsDblScale
	if !nearPt(bsDblMinPt, wantMinPt) {
		t.Errorf("比值翻倍后 minSize = %gpt, want ≈ %gpt (约束没重推)", bsDblMinPt, wantMinPt)
	}
	if nearPt(bsBaseMinPt, wantMinPt) {
		t.Errorf("基准比值下的 minSize (%gpt) 与新比值下的期望值相同 —— 该断言是空转的", bsBaseMinPt)
	}

	// 补报的两个事件: 漏报会让内核停在旧单位, 报了就要对得上。
	if bsDblResizes == 0 {
		t.Error("比值变了没补报 EventResize —— 内核那边客户区的设备像素尺寸会一直停在旧值")
	} else if !nearPx(bsDblResW, bsDblSize[0]) || !nearPx(bsDblResH, bsDblSize[1]) {
		t.Errorf("EventResize 报了 %dx%d, want ≈ %v", bsDblResW, bsDblResH, bsDblSize)
	}
	if bsDblMoves == 0 {
		t.Error("比值变了没补报 EventMove —— 内核那边存的位置会一直停在旧单位")
	} else if !nearPx(bsDblMoveEv.X, bsDblBnd[0]) || !nearPx(bsDblMoveEv.Y, bsDblBnd[1]) {
		t.Errorf("EventMove 报了 (%d, %d), want ≈ (%d, %d)",
			bsDblMoveEv.X, bsDblMoveEv.Y, bsDblBnd[0], bsDblBnd[1])
	}

	// 出方向: ResizeClient 收设备像素, 落到 AppKit 要除**新**比值。
	if !nearPt(bsResPt, bsResPtWant) {
		t.Errorf("ResizeClient(%d) 之后客户区点宽 = %gpt, want %gpt (出方向还在用旧比值)",
			bsResW, bsResPt, bsResPtWant)
	}
	if !nearPx(bsResSize[0], bsResW) || !nearPx(bsResSize[1], bsResH) {
		t.Errorf("ResizeClient(%d, %d) 之后 Size() = %v (设备像素往返不成立)",
			bsResW, bsResH, bsResSize)
	}

	// 还原: 跨比值不变的量是客户区的**点**尺寸, 所以还原后设备像素应缩回基准比值。
	wantBackX := int(float64(bsResSize[0])*bsBaseScale/bsDblScale + 0.5)
	wantBackY := int(float64(bsResSize[1])*bsBaseScale/bsDblScale + 0.5)
	if !nearPx(bsBackSize[0], wantBackX) || !nearPx(bsBackSize[1], wantBackY) {
		t.Errorf("比值还原后 Size() = %v, want ≈ [%d %d] (客户区点尺寸没被保持住)",
			bsBackSize, wantBackX, wantBackY)
	}
}
