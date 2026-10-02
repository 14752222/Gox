//go:build darwin

package cocoa

import "testing"

// TestCocoaFrameOrigin 钉住 AppKit 的 y 轴翻转公式 (M4)。
//
// setFrameOrigin 收的是外框**左下角**, 且 AppKit 全局原点在左下、y 向上;
// 而 gfx 的 (topX, topY) 是外框**左上角**、左上原点。翻错符号不会崩, 只会
// 让窗口出现在屏幕外的下方 —— 属于最难排查的一类, 所以把加减顺序用几个
// 有区分度的数钉死在单测里 (纯函数, 不碰 AppKit, 不弹窗)。
func TestCocoaFrameOrigin(t *testing.T) {
	cases := []struct {
		name                          string
		topX, topY, frameH, screenTop float64
		wantX, wantY                  float64
	}{
		// 主屏 900 高, 600 高的窗口贴顶: 左上角在对齐屏顶 → 左下角在 900-600=300。
		{"贴顶", 0, 0, 600, 900, 0, 300},
		// 贴底: 左上角在 300 → 左下角落在 0。
		{"贴底", 0, 300, 600, 900, 0, 0},
		// y 向下增大 (左上原点语义), 翻转后应减小。
		{"向下 100", 120, 100, 400, 900, 120, 400},
		// 主屏被摆在上方这种罕见排列: screenTop = origin.y + height, 公式要认它。
		{"主屏上移", 0, 50, 200, 1200, 0, 950},
	}
	for _, c := range cases {
		gx, gy := cocoaFrameOrigin(c.topX, c.topY, c.frameH, c.screenTop)
		if gx != c.wantX || gy != c.wantY {
			t.Errorf("%s: cocoaFrameOrigin(topX=%v, topY=%v, h=%v, screenTop=%v) = (%v,%v), want (%v,%v)",
				c.name, c.topX, c.topY, c.frameH, c.screenTop, gx, gy, c.wantX, c.wantY)
		}
	}
}
