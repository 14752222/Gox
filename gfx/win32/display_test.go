//go:build windows

package win32

import (
	"image"
	"testing"
	"time"

	"github.com/14752222/Gox/gfx"
)

// TestDisplaysEnumerate 在真机上跑一遍显示器枚举。
//
// 为什么值得单独一条: gfx/screen 的整条链路依赖"后端能说出有哪几块屏", 而
// 这一段是**纯 syscall + 结构体布局**的代码 (MONITORINFOEXW 的 cbSize、szDevice
// 的读取方式), 写错了不会崩, 只会静默返回空表 —— 于是屏幕适配全链路退化,
// 却没有任何报错。这里把"至少枚举出一块几何合理的主屏"钉住。
func TestDisplaysEnumerate(t *testing.T) {
	f := &factory{}
	list := f.Displays()
	if len(list) == 0 {
		t.Skip("当前会话无法枚举显示器 (无桌面/无头环境)")
	}
	primaries := 0
	for _, d := range list {
		if d.W <= 0 || d.H <= 0 {
			t.Fatalf("显示器 %q 几何不合理: %dx%d", d.ID, d.W, d.H)
		}
		if d.WorkW <= 0 || d.WorkH <= 0 {
			t.Fatalf("显示器 %q 工作区不合理: %dx%d", d.ID, d.WorkW, d.WorkH)
		}
		if d.Scale <= 0 {
			t.Fatalf("显示器 %q 缩放不合理: %v", d.ID, d.Scale)
		}
		if d.ID == "" {
			t.Fatalf("显示器 ID 为空 (szDevice 没读出来?): %+v", d)
		}
		// Windows 侧没有折叠姿态 API: 后端必须老老实实标 flat, 而不是猜一个
		// "半折"出来 (猜错会让界面在没有折痕的屏上分栏)。
		if d.Posture != "flat" || d.Foldable || d.Hinge != nil {
			t.Fatalf("win32 后端不该自带折叠姿态: %+v", d)
		}
		if d.Primary {
			primaries++
		}
	}
	if primaries != 1 {
		t.Fatalf("应恰好有一个主显示器, 实际 %d 个", primaries)
	}
}

// TestDisplayOfRejectsForeignSurface 验证"类型断言落空即降级":
// 非本后端造的 Surface 不该被误报成某块屏上的窗口。
func TestDisplayOfRejectsForeignSurface(t *testing.T) {
	f := &factory{}
	if id, ok := f.DisplayOf(nil); ok || id != "" {
		t.Fatalf("nil Surface 不该有归属: %q %v", id, ok)
	}
	if id, ok := f.DisplayOf(fakeSurface{}); ok || id != "" {
		t.Fatalf("外部 Surface 不该有归属: %q %v", id, ok)
	}
}

// fakeSurface 只实现 gfx.Surface (刻意不实现 displayProvider 相关能力)。
type fakeSurface struct{}

func (fakeSurface) Show(*image.RGBA)                           {}
func (fakeSurface) ShowRegions(*image.RGBA, []image.Rectangle) {}
func (fakeSurface) Size() (int, int)                           { return 1, 1 }
func (fakeSurface) WaitEvents(time.Duration) bool              { return true }
func (fakeSurface) Events() <-chan gfx.Event                   { return nil }
