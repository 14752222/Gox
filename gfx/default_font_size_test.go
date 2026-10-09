package gfx

import "testing"

// ===== defaultFontSize: 兜底字号随显示器 Scale 缩放 =====
//
// 移动端/Retina 上内核坐标是设备像素, "没写 font"的控件若仍按 16 像素画,
// 3x 屏上物理字号只剩 5.3pt —— 整个 UI 挤成一小块 (iOS 模拟器实测)。
// 口径: 16 × 窗口所在显示器的 Scale, 四舍五入; Scale=1 与无窗口时为 16。

// scaledFactory 是带 displayProvider 的假工厂: 报告一块 Scale 可调的主屏。
type scaledFactory struct {
	s     *fakeSurface
	scale float64
}

func (f *scaledFactory) Create(cfg WindowConfig) (Surface, error) { return f.s, nil }

func (f *scaledFactory) Displays() []Display {
	return []Display{{
		ID: "test-hidpi", Name: "HiDPI", W: 1206, H: 2622,
		WorkW: 1206, WorkH: 2622, Scale: f.scale, Primary: true,
	}}
}

func (f *scaledFactory) DisplayOf(s Surface) (string, bool) {
	if s == f.s {
		return "test-hidpi", true
	}
	return "", false
}

func TestDefaultFontSizeScalesWithDisplay(t *testing.T) {
	fake := newFakeSurface()

	// 无窗口: 兜底 16 (font_inherit_test 依赖的口径不能被破坏)。
	if got := defaultFontSize(); got != 16 {
		t.Fatalf("无活动窗口时应为 16, got %d", got)
	}

	// 3x 移动屏: 16dp → 48px。
	SetDefaultFactory(&scaledFactory{s: fake, scale: 3})
	a := &app{surface: fake, dirtyNodes: map[*GuiNode]struct{}{}}
	registerApp(a)
	t.Cleanup(func() {
		unregisterApp(a)
		appMu.Lock()
		if activeApp == a {
			activeApp = nil
		}
		appMu.Unlock()
		SetDefaultFactory(nil)
	})
	if got := defaultFontSize(); got != 48 {
		t.Fatalf("3x 屏上应为 48, got %d", got)
	}

	// 节点走 FontSize() 的兜底路径也要吃到缩放。
	lone := mkNode("text", nil)
	if got := lone.FontSize(); got != 48 {
		t.Fatalf("无 font prop 的节点在 3x 屏上应落 48, got %d", got)
	}

	// 显式 font 也走同一条换算链 (rpr9zf §1: 高密度屏上 font={20} 曾经就是
	// 20 个物理像素 —— 界面小到没法用)。20dp × 3 = 60px。
	explicit := mkNode("text", map[string]float64{"font": 20})
	if got := explicit.FontSize(); got != 60 {
		t.Fatalf("3x 屏上显式 font=20 应为 60, got %d", got)
	}
	// 于是"没写 font"与"写 font={16}"在任何屏上都是同一个物理字号。
	if got := mkNode("text", map[string]float64{"font": 16}).FontSize(); got != 48 {
		t.Fatalf("3x 屏上 font=16 应与兜底一致 (48), got %d", got)
	}
}
