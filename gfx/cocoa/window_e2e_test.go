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
package cocoa

import (
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

// 建窗落点与 moveTo 的目标 (gfx 口径: 左上原点 + 设备像素)。
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
	wmPlaceMoves int  // 建窗期收到的 EventMove 个数 (应为 0)
	wmMovePosOK  bool // moveTo 之后 bounds() 读回
	wmMoveRead   [2]int
	wmMoveEvOK   bool // moveTo 之后收到 EventMove 且坐标对得上
	wmMoveEvs    int  // moveTo 之后收到的 EventMove 个数
)

// runWindowScenario 在主线程上执行窗口管理场景。必须在主 goroutine 调用。
func runWindowScenario() error {
	s0, err := newSurface(gfx.WindowConfig{
		Title: "cocoa-win", Width: 320, Height: 240,
		X: wmPlaceX, Y: wmPlaceY, HasPos: true,
	})
	if err != nil {
		return err
	}
	s := s0.(*surface)
	wmScale = s.scale
	defer func() {
		s.win.Send(selOrderOut, objc.ID(0))
		unregSurface(s.view, s.delegate)
	}()

	// 1) 建窗落点: 回读即验证 appKitOriginX/Y 与 gfxOriginY 互逆。
	x, y, w, h := s.Bounds()
	wmPlaceRead = [4]int{x, y, w, h}
	wmPosOK = nearPx(x, wmPlaceX) && nearPx(y, wmPlaceY) && w > 0 && h > 0

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
		t.Errorf("建窗落点 (x=%d y=%d) 与 bounds() 读回 (x=%d y=%d w=%d h=%d) 不互逆 "+
			"(坐标系换算 或 单位乘除写反; scale=%g)",
			wmPlaceX, wmPlaceY, wmPlaceRead[0], wmPlaceRead[1], wmPlaceRead[2], wmPlaceRead[3], wmScale)
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
