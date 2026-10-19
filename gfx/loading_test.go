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
	// spinner 一挂载就登记帧心跳; 用例不关窗口, 得自己摘 —— 否则心跳会一路
	// 漏到后面的用例 (clearAnimTickerForTest 的注释)。
	t.Cleanup(clearAnimTickerForTest)
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

// 弧的起始角随时间前进: 用替换过的 loopEpoch 制造两个不同时刻。
func TestSpinnerArcStartAdvances(t *testing.T) {
	// 隔离 reduced-motion: 它一旦开着, 弧就被钉在 12 点, 这条用例测的
	// "会转" 反而应当失败。
	forceMotionForTest(t, true)

	old := loopEpoch
	t.Cleanup(func() { loopEpoch = old })

	loopEpoch = time.Now()
	a0 := spinnerArcStart()
	// 前进 1/3 周期 → 起始角应前进 1/3 圈
	loopEpoch = time.Now().Add(-spinnerPeriod / 3)
	a1 := spinnerArcStart()
	if absFloat(a1-a0) < 1e-6 {
		t.Fatalf("spinner 弧起始角未随时间前进 (a0=%f a1=%f)", a0, a1)
	}
	// 相位应始终落在 [0, tau) 的等价区间 (起点 -tau/4, 跨度一圈)
	for _, a := range []float64{a0, a1} {
		if a < -tau/4-1e-9 || a > -tau/4+tau+1e-9 {
			t.Fatalf("spinner 弧起始角越界: %f", a)
		}
	}
}

// reduced-motion: 弧钉在 12 点不动, 且不再上心跳。
//
// 这条是 S7 的硬要求, 也是本文件里唯一一条"必须开着 reduced-motion 才成立"
// 的断言: 转圈是持续位移的无限循环, 对晕动症用户是最典型的触发源。
func TestSpinnerFreezesUnderReducedMotion(t *testing.T) {
	forceMotionForTest(t, false)

	if got, want := spinnerArcStart(), -tau/4; absFloat(got-want) > 1e-9 {
		t.Fatalf("reduced-motion 下弧起始角 = %f, want %f (12 点方向)", got, want)
	}
	// 换一个时刻再取一次, 仍应是同一个角
	old := loopEpoch
	t.Cleanup(func() { loopEpoch = old })
	loopEpoch = time.Now().Add(-spinnerPeriod / 2)
	if got, want := spinnerArcStart(), -tau/4; absFloat(got-want) > 1e-9 {
		t.Fatalf("reduced-motion 下弧起始角随动了: %f", got)
	}

	// 也不该上心跳
	oldTickers := tickerNodes
	tickerNodes = map[*GuiNode]struct{}{}
	t.Cleanup(func() { tickerNodes = oldTickers })
	root := mkNode("column", nil)
	sp := mkNode("spinner", nil)
	mountChildren(root, sp)
	renderTree(root, 120, 120)
	if _, ok := tickerNodes[sp]; ok {
		t.Fatalf("reduced-motion 下 spinner 不该登记帧推进 (画面是静止的)")
	}
}

// forceMotionForTest 把 reduced-motion 开关钉死 (motion=true → 允许动)。
//
// 两个方向都要能钉: "reduced" 验证降级, "motion" 让"会动"的那几条用例不
// 因为环境里恰好设了 GOX_REDUCED_MOTION 而假红 —— 环境变量是进程级的,
// 单条用例改不掉它, 只能用这个优先级更高的显式开关盖住。
func forceMotionForTest(t *testing.T, motion bool) {
	t.Helper()
	reduced := !motion
	SetReducedMotion(&reduced)
	t.Cleanup(func() { SetReducedMotion(nil) })
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// 挂载后登记进帧集合, 心跳启动。
func TestSpinnerRegistersTicker(t *testing.T) {
	// 隔离全局状态: 用例之间不许串味
	forceMotionForTest(t, true)
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
	forceMotionForTest(t, true) // 否则"缺省会呼吸"这条前提不成立
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

// 呼吸相位在 [0,1] 之间, 且随时间变化。
func TestSkeletonBreathRange(t *testing.T) {
	forceMotionForTest(t, true)
	old := loopEpoch
	t.Cleanup(func() { loopEpoch = old })
	seen := map[float64]bool{}
	for _, d := range []time.Duration{0, skeletonPeriod / 4, skeletonPeriod / 2, skeletonPeriod} {
		loopEpoch = time.Now().Add(-d)
		b := skeletonBreath(true)
		if b < 0 || b > 1 {
			t.Fatalf("呼吸相位越界: d=%v b=%f", d, b)
		}
		seen[b] = true
	}
	if len(seen) < 2 {
		t.Fatalf("呼吸相位不随时间变化: %v", seen)
	}
	// 关掉呼吸 → 恒定 0 (静态占位是确定的一块灰)
	loopEpoch = time.Now().Add(-skeletonPeriod / 3)
	if got := skeletonBreath(false); got != 0 {
		t.Fatalf("active=false 的骨架相位 = %f, want 0", got)
	}
}

// reduced-motion: 呼吸相位冻结在中值, 且不再上心跳。
//
// 骨架是大面积明暗变化, 是 reduced-motion 规范里点名要关掉的一类动效。
// 冻在中值而不是最暗端: 最暗端与 active=false 撞色, 会让人以为"这块是不是
// 没加载出来"; 中值本身就是"这是块占位"的信息。
func TestSkeletonFreezesUnderReducedMotion(t *testing.T) {
	forceMotionForTest(t, false)
	old := loopEpoch
	t.Cleanup(func() { loopEpoch = old })
	for _, d := range []time.Duration{0, skeletonPeriod / 4, skeletonPeriod / 2} {
		loopEpoch = time.Now().Add(-d)
		if got := skeletonBreath(true); got != 0.5 {
			t.Fatalf("reduced-motion 下骨架相位 = %f (d=%v), want 0.5", got, d)
		}
	}

	oldTickers := tickerNodes
	tickerNodes = map[*GuiNode]struct{}{}
	t.Cleanup(func() { tickerNodes = oldTickers })
	sk := mkNode("skeleton", nil)
	renderTree(mkMountWrap(sk), 200, 160)
	if _, ok := tickerNodes[sk]; ok {
		t.Fatalf("reduced-motion 下 skeleton 不该登记帧推进")
	}
}

// 呼吸的两端是两级确定的灰度, 与画在什么底色上无关 (见 skeletonBreathColor)。
func TestSkeletonBreathColorIsOpaque(t *testing.T) {
	for _, phase := range []float64{0, 0.5, 1} {
		c := skeletonBreathColor(phase)
		if c.A != 255 {
			t.Fatalf("phase=%v 的骨架色 alpha=%d, 应为不透明 (呼吸走颜色插值)", phase, c.A)
		}
	}
	if skeletonBreathColor(0) == skeletonBreathColor(1) {
		t.Fatalf("骨架呼吸两端灰度相同, 呼吸不会可见")
	}
}
