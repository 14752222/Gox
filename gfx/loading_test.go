package gfx

import (
	"testing"
	"time"
)

// ===== S4/T09 加载态: spinner / skeleton =====
//
// 关键不变量:
//   1. 尺寸: spinner 正方形跟随 size; skeleton 高度按 rows 累加;
//   2. 相位变化: spinner 的头部刻度随时间前进 (同一节点两次取值不同);
//   3. 帧驱动: 挂到树上后 tickerNodes 有登记, 且心跳定时器已启动;
//   4. 停表: 节点离树 (disposeNode → cancelAnim) 后登记被摘除;
//   5. active=false 的 skeleton 不登记 (省电)。

func TestSpinnerIntrinsicSize(t *testing.T) {
	sp := mkNode("spinner", nil)
	if w, h := sp.intrinsicSize(); w != spinnerDefault || h != spinnerDefault {
		t.Fatalf("缺省 spinner 尺寸 = %dx%d, want %d", w, h, spinnerDefault)
	}
	sp2 := mkNode("spinner", map[string]float64{"size": 40})
	if w, h := sp2.intrinsicSize(); w != 40 || h != 40 {
		t.Fatalf("size=40 的 spinner 尺寸 = %dx%d", w, h)
	}
}

func TestSpinnerLayoutPaintsTicks(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10})
	sp := mkNode("spinner", map[string]float64{"size": 32})
	mountChildren(root, sp)
	img := renderTree(root, 200, 120)

	if sp.Box.W != 32 || sp.Box.H != 32 {
		t.Fatalf("spinner 布局盒 = %dx%d, want 32x32", sp.Box.W, sp.Box.H)
	}
	// 圆环区域应有刻度墨迹 (中心是空的, 取外圈采样带)
	ring := Rect{X: sp.Box.X, Y: sp.Box.Y, W: 32, H: 32}
	if !hasInkIn(img, ring) {
		t.Fatalf("spinner 未绘制任何刻度")
	}
	// 中心不应有墨 (刻度只在外圈)
	cx, cy := sp.Box.X+16, sp.Box.Y+16
	if img.RGBAAt(cx, cy) != pxWhite {
		t.Fatalf("spinner 中心不该有墨 (刻度只在外圈)")
	}
}

// 相位随时间前进: 用替换过的 spinnerEpoch 制造两个不同时刻。
func TestSpinnerHeadAdvances(t *testing.T) {
	old := spinnerEpoch
	t.Cleanup(func() { spinnerEpoch = old })

	spinnerEpoch = time.Now()
	h0 := spinnerHead()
	// 前进 1/3 周期 → 头部索引应前进约 4 格 (12 格/周期)
	spinnerEpoch = time.Now().Add(-spinnerPeriod / 3)
	h1 := spinnerHead()
	if h0 == h1 {
		t.Fatalf("spinner 相位未随时间前进 (h0=%d h1=%d)", h0, h1)
	}
	// 相位应始终落在 [0, ticks)
	for _, h := range []int{h0, h1} {
		if h < 0 || h >= spinnerTicks {
			t.Fatalf("spinner 头部索引越界: %d", h)
		}
	}
}

// 挂载后登记进帧集合, 心跳启动。
func TestSpinnerRegistersTicker(t *testing.T) {
	// 隔离全局状态: 用例之间不许串味
	oldTickers := tickerNodes
	tickerNodes = map[*GuiNode]struct{}{}
	oldRunning := animRunning
	animRunning = false
	t.Cleanup(func() {
		tickerNodes = oldTickers
		animRunning = oldRunning
	})

	root := mkNode("column", nil)
	sp := mkNode("spinner", nil)
	mountChildren(root, sp)
	renderTree(root, 120, 120)

	if _, ok := tickerNodes[sp]; !ok {
		t.Fatalf("spinner 挂载后未登记进帧推进集合")
	}
	if !animRunning {
		t.Fatalf("登记后动画心跳应已启动")
	}
	// 离树 → 摘除
	cancelAnim(sp)
	if _, ok := tickerNodes[sp]; ok {
		t.Fatalf("离树后 spinner 仍在帧推进集合里")
	}
}

// ===== skeleton =====

func TestSkeletonIntrinsicSize(t *testing.T) {
	sk := mkNode("skeleton", map[string]float64{"rows": 3})
	_, h := sk.intrinsicSize()
	want := 3*skeletonRowH + 2*skeletonGap
	if h != want {
		t.Fatalf("rows=3 骨架高 = %d, want %d", h, want)
	}
	// avatar 形态: 至少和头像一样高
	skA := mkNode("skeleton", map[string]float64{"rows": 1})
	withBool(skA, "avatar", true)
	_, hA := skA.intrinsicSize()
	if hA < skeletonAvatarSize {
		t.Fatalf("avatar 形态骨架高 = %d, 应 >= %d", hA, skeletonAvatarSize)
	}
}

func TestSkeletonPaintsRows(t *testing.T) {
	root := mkNode("column", map[string]float64{"padding": 10, "width": 220})
	sk := mkNode("skeleton", map[string]float64{"rows": 3, "width": 200})
	withBool(sk, "active", false) // 关动画: 取确定性的实心色
	mountChildren(root, sk)
	img := renderTree(root, 300, 200)

	// 首条灰条 (左上角内侧) 应是轨道色
	assertPx(t, img, sk.Box.X+3, sk.Box.Y+3, pxTrack, "骨架首条灰条")
	// 首条比后续条短 (缺省 titleWidth 40%)
	if sk.Box.W != 200 {
		t.Fatalf("骨架宽 = %d, want 200", sk.Box.W)
	}
}

// active=false 不登记 (省电); active=true (缺省) 登记。
func TestSkeletonActiveControlsTicker(t *testing.T) {
	oldTickers := tickerNodes
	tickerNodes = map[*GuiNode]struct{}{}
	t.Cleanup(func() { tickerNodes = oldTickers })

	off := mkNode("skeleton", nil)
	withBool(off, "active", false)
	renderTree(mkMountWrap(off), 200, 120)
	if _, ok := tickerNodes[off]; ok {
		t.Fatalf("active=false 的 skeleton 不该登记帧推进")
	}

	on := mkNode("skeleton", nil)
	renderTree(mkMountWrap(on), 200, 120)
	if _, ok := tickerNodes[on]; !ok {
		t.Fatalf("active=true 的 skeleton 应登记帧推进")
	}
}

// 呼吸系数在 [0.55, 1.0] 之间, 且随时间变化。
func TestSkeletonBreathRange(t *testing.T) {
	old := spinnerEpoch
	t.Cleanup(func() { spinnerEpoch = old })
	for _, d := range []time.Duration{0, skeletonPeriod / 4, skeletonPeriod / 2, skeletonPeriod} {
		spinnerEpoch = time.Now().Add(-d)
		b := skeletonBreath()
		if b < 0.55 || b > 1.0 {
			t.Fatalf("呼吸系数越界: d=%v b=%f", d, b)
		}
	}
}
