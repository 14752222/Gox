package gfx

// reduced-motion 与 motion token 的用例（Prism S4 的前置）。

import "testing"

// TestMotionDurationHonoursReducedMotion 验证唯一入口的降级语义。
func TestMotionDurationHonoursReducedMotion(t *testing.T) {
	t.Cleanup(func() { SetReducedMotion(nil) })

	// 默认（未显式设置、无环境变量、无配置、无平台实现）= 不降级
	SetReducedMotion(nil)
	t.Setenv("GOX_REDUCED_MOTION", "")
	if got := MotionDuration(MotionBase); got != MotionBase {
		t.Fatalf("默认状态下 MotionDuration(MotionBase) = %d, want %d", got, MotionBase)
	}

	// 显式开启 = 全部归零（瞬时完成）
	on := true
	SetReducedMotion(&on)
	for _, ms := range []int{MotionFast, MotionBase, MotionSlow} {
		if got := MotionDuration(ms); got != 0 {
			t.Errorf("reduced-motion 下 MotionDuration(%d) = %d, want 0", ms, got)
		}
	}

	// 显式关闭 = 忽略环境变量（测试要在动画上取证时用）
	off := false
	SetReducedMotion(&off)
	t.Setenv("GOX_REDUCED_MOTION", "1")
	if got := MotionDuration(MotionSlow); got != MotionSlow {
		t.Errorf("显式关闭时 MotionDuration(MotionSlow) = %d, want %d", got, MotionSlow)
	}
}

// TestReducedMotionEnvVar 验证环境变量各写法的解析。
func TestReducedMotionEnvVar(t *testing.T) {
	t.Cleanup(func() { SetReducedMotion(nil) })
	SetReducedMotion(nil)

	yes := []string{"1", "true", "TRUE", "yes", " on "}
	for _, v := range yes {
		t.Run("on/"+v, func(t *testing.T) {
			t.Setenv("GOX_REDUCED_MOTION", v)
			if !PrefersReducedMotion() {
				t.Errorf("GOX_REDUCED_MOTION=%q 应开启 reduced-motion", v)
			}
		})
	}
	no := []string{"", "0", "false", "no", "off", "maybe"}
	for _, v := range no {
		t.Run("off/"+v, func(t *testing.T) {
			t.Setenv("GOX_REDUCED_MOTION", v)
			if PrefersReducedMotion() {
				t.Errorf("GOX_REDUCED_MOTION=%q 不该开启 reduced-motion", v)
			}
		})
	}
}

// TestSpaceAndRadiusTokens 验证尺度 token 的换算与哨兵值。
func TestSpaceAndRadiusTokens(t *testing.T) {
	// 1x 屏（无窗口时 displayScale 落到 1）：dpToPx 是截断恒等
	if got := Space(Space4); got != Space4 {
		t.Errorf("1x 下 Space(%d) = %d, want %d", Space4, got, Space4)
	}
	if got := Space(Space6); got != Space6 {
		t.Errorf("1x 下 Space(%d) = %d, want %d", Space6, got, Space6)
	}

	// RadiusFull 用负数哨兵表达"胶囊形"，不能是某个固定像素
	if got := Radius(RadiusFull); got != -1 {
		t.Errorf("Radius(RadiusFull) = %d, want -1（全圆哨兵）", got)
	}
	if got := Radius(RadiusMD); got != int(RadiusMD) {
		t.Errorf("1x 下 Radius(RadiusMD) = %d, want %d", got, int(RadiusMD))
	}

	// 0 是合法值（直角），不能被误当成"未设置"
	if got := Radius(RadiusNone); got != 0 {
		t.Errorf("Radius(RadiusNone) = %d, want 0", got)
	}
}

// TestTouchMinIsPhysical 断言触控下限不随 displayScale 变化。
//
// 这条容易被"顺手统一成 dp"改坏: 44 若乘上 3x 会变成 132px 的荒谬命中区。
func TestTouchMinIsPhysical(t *testing.T) {
	if TouchMin != 44 {
		t.Fatalf("TouchMin = %d, want 44（Apple HIG / Material 共同下限）", TouchMin)
	}
}
