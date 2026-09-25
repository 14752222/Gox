//go:build darwin

// cocoa 后端真机端到端测试: 开真窗口 + 用 NSEvent 工厂方法构造事件对象 +
// sendEvent: 走 AppKit 原生响应链, 验证 "平台事件 → gfx.Event" 的翻译链路。
//
// 为什么不注入 CGEvent: 合成全局输入 (CGEventPost) 需要宿主进程有辅助功能
// 权限, CI/沙箱环境拿不到。而 NSEvent + sendEvent: 是进程内调用, 不需要任何
// TCC 授权 —— 事件经过的路径 (hitTest → view 的 mouseDown:/keyDown: IMP →
// trySend) 与真实用户输入完全一致, 只有"产生事件的方式"不同。
//
// ## 为什么场景跑在 TestMain 而不是 Test 函数里
//
// go test 的用例跑在**新 goroutine**上, 不保证落在主线程; 而 init 里的
// LockOSThread 只钉住了主 goroutine (= TestMain 所在) 。AppKit 硬性要求主
// 线程, 在用例 goroutine 里开窗口会直接挂死 (实测: objc Send 永久阻塞)。
// 所以: TestMain 在主线程上执行整个场景, 结果记进包级变量; Test 函数只断言。
//
// 注意: 测试会短暂弹一个窗口 (几百毫秒内 orderOut 收掉)。
package cocoa

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/ebitengine/purego/objc"
)

// 测试专用 selector (与 cocoa.go 的缓存同一批名字, 但只在此文件用)。
var (
	selMouseEventWithType = objc.RegisterName("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	selKeyEventWithType   = objc.RegisterName("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	selWindowNumber       = objc.RegisterName("windowNumber")
	selOrderOut           = objc.RegisterName("orderOut:")
)

const nsTypeKeyDown = 10

// e2eFail 非空 = 场景执行失败 (TestMain 里记录, 用例里上报)。
var e2eFail string

// e2eEvents 场景收到的事件序列 (顺序即到达顺序)。
var e2eEvents []gfx.Event

// e2eScale 场景窗口的 scale (坐标断言用)。
var e2eScale float64

// e2eBufOK / e2eBufLen / e2eBufFirst 记录 Show 后台缓冲的自检结果。
var (
	e2eBufOK    bool
	e2eBufLen   int
	e2eBufFirst byte
)

func TestMain(m *testing.M) {
	if err := runE2E(); err != nil {
		e2eFail = err.Error()
	}
	if err := runV1Scenarios(); err != nil {
		v1Fail = err.Error()
	}
	os.Exit(m.Run())
}

// runE2E 在主线程上执行完整场景。必须在主 goroutine 调用 (见文件头)。
func runE2E() error {
	s0, err := newSurface(gfx.WindowConfig{Title: "cocoa-e2e", Width: 400, Height: 300})
	if err != nil {
		return err
	}
	s := s0.(*surface)
	e2eScale = s.scale
	defer func() {
		s.win.Send(selOrderOut, objc.ID(0))
		unregSurface(s.view, s.delegate)
	}()

	winNum := objc.Send[int64](s.win, selWindowNumber)
	if winNum == 0 {
		return errNoWindowNumber
	}

	// --- 构造 NSEvent 并直接派发给 view 的已注册事件方法 ---
	//
	// 为什么不走 [NSApp sendEvent:]: 那条路要求窗口是 keyWindow (测试进程
	// 无法真正"激活"应用, 实测 isKeyWindow=false, 事件被 AppKit 丢弃)。
	// hitTest / keyWindow 路由是 AppKit 自己的代码, 不属于被测对象; 直接调
	// 用 view 方法覆盖的是我们的部分: pointInView 坐标换算 → postDevice
	// (点→设备像素) → keyFromCharacters 翻译 → 事件通道。
	selMouseDown := objc.RegisterName("mouseDown:")
	selMouseUp := objc.RegisterName("mouseUp:")
	selKeyDown := objc.RegisterName("keyDown:")

	// 鼠标按下/抬起。注意 locationInWindow 是**底左原点** (y 向上) 的窗口
	// 坐标: 想要"距顶 80 点"就得给 300-80=220。view 侧 convertPoint (isFlipped)
	// 会把它换算回 gfx 的"左上原点、y 向下"坐标系 —— 下面断言 Y==80。
	loc := nsPoint{X: 100, Y: 300 - 80}
	down := objc.ID(objc.GetClass("NSEvent")).Send(selMouseEventWithType,
		uintptr(1), loc, uint64(0), float64(0), // LeftMouseDown
		winNum, objc.ID(0), int64(1), int64(1), float32(1.0))
	objc.Send[uintptr](s.view, selMouseDown, down)
	up := objc.ID(objc.GetClass("NSEvent")).Send(selMouseEventWithType,
		uintptr(2), loc, uint64(0), float64(0), // LeftMouseUp
		winNum, objc.ID(0), int64(1), int64(1), float32(1.0))
	objc.Send[uintptr](s.view, selMouseUp, up)

	// 键盘按下 (字符 'a', 无修饰键)
	kd := objc.ID(objc.GetClass("NSEvent")).Send(selKeyEventWithType,
		uintptr(nsTypeKeyDown), nsPoint{}, uint64(0), float64(0),
		winNum, objc.ID(0), nsString("a"), nsString("a"), false, uint64(0))
	objc.Send[uintptr](s.view, selKeyDown, kd)

	// 收事件 (3 个, 每个最多等 2s)
	for i := 0; i < 3; i++ {
		select {
		case ev := <-s.Events():
			e2eEvents = append(e2eEvents, ev)
		case <-time.After(2 * time.Second):
			return errEventTimeout
		}
	}

	// 上屏: 全 0xFF 一帧, 确认 ShowRegions 的脏区拷贝落进后台缓冲
	img := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for i := range img.Pix {
		img.Pix[i] = 0xFF
	}
	s.Show(img)
	e2eBufLen = len(s.buf)
	e2eBufFirst = s.buf[0]
	e2eBufOK = e2eBufLen == 400*300*4 && e2eBufFirst == 0xFF
	return nil
}

var (
	errNoWindowNumber = errStr("窗口没有 windowNumber (未上屏?)")
	errEventTimeout   = errStr("等待事件超时")
)

type errStr string

func (e errStr) Error() string { return string(e) }

func TestCocoaMouseAndKeyE2E(t *testing.T) {
	if e2eFail != "" {
		t.Fatalf("e2e 场景失败: %s", e2eFail)
	}
	if len(e2eEvents) != 3 {
		t.Fatalf("收到 %d 个事件, want 3", len(e2eEvents))
	}

	if e2eEvents[0].Kind != gfx.EventMouseDown {
		t.Errorf("事件0 = %v, want MouseDown", e2eEvents[0].Kind)
	}
	// 坐标是设备像素口径 (点 × scale)
	if want := int(100 * e2eScale); e2eEvents[0].X != want {
		t.Errorf("MouseDown.X = %d, want %d", e2eEvents[0].X, want)
	}
	if want := int(80 * e2eScale); e2eEvents[0].Y != want {
		t.Errorf("MouseDown.Y = %d, want %d", e2eEvents[0].Y, want)
	}
	if e2eEvents[1].Kind != gfx.EventMouseUp {
		t.Errorf("事件1 = %v, want MouseUp", e2eEvents[1].Kind)
	}
	if e2eEvents[2].Kind != gfx.EventKeyDown {
		t.Errorf("事件2 = %v, want KeyDown", e2eEvents[2].Kind)
	}
	if e2eEvents[2].Key != "a" {
		t.Errorf("Key = %q, want \"a\"", e2eEvents[2].Key)
	}
	if !e2eBufOK {
		t.Errorf("后台缓冲自检失败: len=%d first=%d", e2eBufLen, e2eBufFirst)
	}
}
