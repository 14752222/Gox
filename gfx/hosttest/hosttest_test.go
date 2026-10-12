package hosttest

// ===== 套件自测: 好实现全绿 + 每个硬断言都有「真的会红」的负例 =====
//
// 负例手法: 内部断言函数 (checkXxx) 都吃 failer 接口, 这里用记录型 failRecorder
// 直接跑它们并断言「记录到了失败」—— 等效于「在子测试里跑一个 expectFail 的
// 内部 testing.T」, 但不需要伪造 *testing.T (它没有可用的外部构造手法)。
//
// 关于任务要求的「故意少报真实支持的能力 ⇒ 测试必须失败」: 字面少报 (支持却
// 完全不声明) 黑盒不可枚举 (见包注释「黑盒极限」), 负例落在它唯一可判的对偶
// 「声明了却做不到」上 —— TestCheckDeclaredUsableNegative 就是那个负例: 宿主
// 把真实支持面报错方向 (声明 camera.takePhoto 却自认 unsupported), canIUse
// 因此说谎, 契约必须红。

import (
	"errors"
	"fmt"
	"image"
	"strings"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
	"github.com/14752222/Gox/object"
)

// ===== 记录型 failer =====

var errFatalPanic = errors.New("hosttest test: Fatalf")

type failRecorder struct {
	failures []string
	logs     []string
}

func (r *failRecorder) Helper() {}

func (r *failRecorder) Logf(format string, args ...interface{}) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

func (r *failRecorder) Errorf(format string, args ...interface{}) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

// Fatalf 记录后以 panic 模拟 testing.T.Fatalf 的「停止当前用例」; runCheck 回收。
func (r *failRecorder) Fatalf(format string, args ...interface{}) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	panic(errFatalPanic)
}

// runCheck 跑一个断言函数并返回它记录的失败列表。
func runCheck(fn func(f failer)) (failures []string, logs []string) {
	r := &failRecorder{}
	func() {
		defer func() {
			if p := recover(); p != nil && p != errFatalPanic {
				panic(p) // 断言函数自身的 bug 不该被当成「断言红了」
			}
		}()
		fn(r)
	}()
	return r.failures, r.logs
}

// expectRed 断言「断言真的会红」, 并把失败消息打到日志里 (人眼核对消息质量)。
func expectRed(t *testing.T, what string, fn func(f failer)) []string {
	t.Helper()
	failures, _ := runCheck(fn)
	if len(failures) == 0 {
		t.Fatalf("%s: 违约实现居然全绿 —— 这条契约断言是摆设", what)
	}
	for _, msg := range failures {
		t.Logf("%s 红得正确: %s", what, msg)
	}
	return failures
}

// expectGreen 断言「好实现一条失败都没有」。
func expectGreen(t *testing.T, what string, fn func(f failer)) {
	t.Helper()
	failures, _ := runCheck(fn)
	for _, msg := range failures {
		t.Errorf("%s: 好实现被误伤: %s", what, msg)
	}
}

// ===== 假宿主 =====

// staticHost 是声明表与应答都可配的最小宿主。
type staticHost struct {
	caps []string
	call func(method string) gfx.NativeCallResult
}

func (h *staticHost) Capabilities() []string { return h.caps }

func (h *staticHost) Call(method string, args object.Value) gfx.NativeCallResult {
	return h.call(method)
}

// ===== 正例: 好宿主 / 好后端跑完整套件事全绿 =====

func TestRunNativeHostContractGoodHost(t *testing.T) {
	host := &staticHost{
		caps: []string{"probe.ping", "extra"},
		call: func(method string) gfx.NativeCallResult {
			if method == "probe.ping" {
				return gfx.NativeResult(object.NewString("pong"))
			}
			return gfx.NativeFailure(gfx.ErrUnsupported, "test host: %s 未实现", method)
		},
	}
	RunNativeHostContract(t, host)
}

func TestRunSurfaceContractGoodSurface(t *testing.T) {
	RunSurfaceContract(t, func(gfx.WindowConfig) (gfx.Surface, error) {
		return newFakeSurface(), nil
	})
}

// TestFakeSurfaceCloseSemantics 演示 WaitEvents 的 false 语义 (gfx.go Surface
// 注释: 事件源关闭时返回 false) —— 契约黑盒只能钉「活着不许 false」, 关闭方向
// 由这条自测补上。
func TestFakeSurfaceCloseSemantics(t *testing.T) {
	f := newFakeSurface()
	if !f.WaitEvents(5 * time.Millisecond) {
		t.Fatalf("活着时 WaitEvents 应为 true")
	}
	if !f.WaitEventsWake(5*time.Millisecond, nil) {
		t.Fatalf("活着时 WaitEventsWake 应为 true")
	}
	f.close()
	if f.WaitEvents(5 * time.Millisecond) {
		t.Fatalf("关闭后 WaitEvents 应为 false (事件源关闭)")
	}
	if f.WaitEventsWake(5*time.Millisecond, nil) {
		t.Fatalf("关闭后 WaitEventsWake 应为 false")
	}
}

// ===== 负例: 每条硬断言都必须真的会红 =====

func TestCheckCapabilitiesShapeNegative(t *testing.T) {
	expectRed(t, "空白项", func(f failer) {
		checkCapabilitiesShape(f, []string{"", "  "})
	})
	expectRed(t, "首尾空白", func(f failer) {
		checkCapabilitiesShape(f, []string{" camera.takePhoto"})
	})
	expectRed(t, "空能力/空动作", func(f failer) {
		checkCapabilitiesShape(f, []string{".takePhoto", "camera."})
	})
	// 正例: 合法形状不红
	expectGreen(t, "合法形状", func(f failer) {
		checkCapabilitiesShape(f, []string{"camera.takePhoto", "insets"})
	})
}

func TestCheckCapabilitiesStableNegative(t *testing.T) {
	stable := &staticHost{caps: []string{"a.x"}}
	drifter := &driftyHost{answers: [][]string{{"a.x"}, {"a.x", "b.y"}}}
	expectRed(t, "声明漂移", func(f failer) {
		checkCapabilitiesStable(f, drifter)
	})
	expectGreen(t, "声明稳定", func(f failer) {
		checkCapabilitiesStable(f, stable)
	})
}

// driftyHost 每次 Capabilities() 回答下一项 (声明表漂移)。
type driftyHost struct {
	answers [][]string
	n       int
}

func (d *driftyHost) Capabilities() []string {
	ans := d.answers[d.n%len(d.answers)]
	d.n++
	return ans
}

func (d *driftyHost) Call(method string, args object.Value) gfx.NativeCallResult {
	return gfx.NativeResult(object.NullSingleton)
}

func TestCheckNoReportOnlyNegative(t *testing.T) {
	expectRed(t, "声明 battery", func(f failer) {
		checkNoReportOnly(f, []string{"battery"})
	})
	expectRed(t, "声明 network 的方法名形态", func(f failer) {
		checkNoReportOnly(f, []string{"network.get"})
	})
	// 正例: insets 是接口注释明确允许的能力名声明, 不得误伤
	expectGreen(t, "insets 合法", func(f failer) {
		checkNoReportOnly(f, []string{"insets", "device.info"})
	})
}

// TestCheckDeclaredUsableNegative 是任务点名的负例: 「故意少报真实支持的能力」
// 的可判对偶 —— 宿主声明了 camera.takePhoto (canIUse 因此说 true), Call 却自认
// unsupported。契约必须红, 否则「声明即可用」就是一句空话。
func TestCheckDeclaredUsableNegative(t *testing.T) {
	liar := &staticHost{
		caps: []string{"camera.takePhoto"},
		call: func(method string) gfx.NativeCallResult {
			return gfx.NativeFailure(gfx.ErrUnsupported, "其实做不到")
		},
	}
	failures := expectRed(t, "声明了却 unsupported", func(f failer) {
		checkDeclaredUsable(f, liar)
	})
	if !strings.Contains(failures[0], "camera.takePhoto") {
		t.Fatalf("失败消息应点名违约方法, 实际: %q", failures[0])
	}

	panicker := &staticHost{
		caps: []string{"camera.takePhoto"},
		call: func(method string) gfx.NativeCallResult {
			panic("宿主崩了")
		},
	}
	expectRed(t, "Call panic", func(f failer) {
		checkDeclaredUsable(f, panicker)
	})

	// 正例: Pending (异步回填) 与 ErrUnavailable (运行时态) 都合法
	async := &staticHost{
		caps: []string{"camera.takePhoto", "location.get"},
		call: func(method string) gfx.NativeCallResult {
			if method == "location.get" {
				return gfx.NativeFailure(gfx.ErrUnavailable, "飞行模式")
			}
			return gfx.NativePending()
		},
	}
	expectGreen(t, "Pending/Unavailable 合法", func(f failer) {
		checkDeclaredUsable(f, async)
	})
}

// pollingWakerSurface 是「把 maxWait 切成小片轮询」的反面教材 (gfx.go waker
// 注释点名禁止): 片长 1.5s, 片与片之间才非阻塞看一眼 wake。
type pollingWakerSurface struct{ *fakeSurface }

func (p *pollingWakerSurface) WaitEventsWake(maxWait time.Duration, wake <-chan struct{}) bool {
	deadline := time.Now().Add(maxWait)
	for {
		select {
		case <-wake:
			return true
		default:
		}
		if time.Now().After(deadline) {
			return true
		}
		time.Sleep(1500 * time.Millisecond)
	}
}

func TestCheckWakerNegative(t *testing.T) {
	polling := &pollingWakerSurface{newFakeSurface()}
	expectRed(t, "切片轮询 waker", func(f failer) {
		checkWaker(f, polling)
	})
	// 正例: 真并入的实现不红
	expectGreen(t, "真并入 waker", func(f failer) {
		checkWaker(f, newFakeSurface())
	})
	// 未实现 waker 是合法退化: 不红, 但应留下 Logf 说明
	_, logs := runCheck(func(f failer) {
		checkWaker(f, &bareSurface{})
	})
	if len(logs) == 0 {
		t.Fatalf("未实现 waker 时应 Logf 说明退化口径")
	}
}

// bareSurface 只实现 Surface 接口 (不实现 waker), 验证「未实现 = 合法退化」。
type bareSurface struct{}

func (b *bareSurface) Show(*image.RGBA)                           {}
func (b *bareSurface) ShowRegions(*image.RGBA, []image.Rectangle) {}
func (b *bareSurface) Size() (int, int)                           { return 200, 150 }
func (b *bareSurface) WaitEvents(time.Duration) bool              { return true }
func (b *bareSurface) Events() <-chan gfx.Event                   { return nil }
