package gfx

import (
	"image"
	"image/color"
	"math"
	"time"

	"github.com/14752222/Gox/object"
)

// S4/T09 加载态组件: spinner (转圈) + skeleton (骨架屏)。
//
// 两者都需要**持续重绘**。这里刻意复用 animate.go 的动画心跳 (animTick,
// ~60fps 的 GlobalScheduler 定时器), 而不是另起一套定时器:
//
//   - 零新增定时器: 心跳的挂/停逻辑 (animRunning) 已经写好且经过压测,
//     重复实现只会多一处可能泄漏的地方;
//   - 静止即停: 树上没有 spinner/skeleton 时集合为空, 心跳自然停表,
//     空闲零开销 —— 与过渡动画完全一致。
//
// 与 caret 闪烁的区别: 光标闪烁由脚本挂 requestAnimationFrame 驱动 (因为
// 它只需要"相位翻转时重绘一帧"), 而 spinner 需要每帧平滑转动, 所以由内核
// 自己挂心跳, 开箱即用。

// 旋转参数: 12 根刻度, 周期 1s (与主流 UI 库的"快而不慌"手感到齐)。
const (
	spinnerTicks   = 12
	spinnerPeriod  = 900 * time.Millisecond
	spinnerDefault = 24 // 缺省直径
	skeletonRowH   = 14 // 骨架条高度
	skeletonGap    = 8  // 条间距
	skeletonRadius = 4  // 骨架条圆角
	skeletonPeriod = 1200 * time.Millisecond
)

// tickerNodes 是需要每帧推进的节点集合 (spinner / skeleton)。
// 与 animNodes 分工: 那边是"有终点的过渡", 这边是"无限循环的加载态"。
var tickerNodes = map[*GuiNode]struct{}{}

// registerTicker 把一个节点登记进帧推进集合, 并按需挂上心跳。
// 幂等: 同一节点重复登记只算一次 (map 天然去重)。
func registerTicker(n *GuiNode) {
	if n == nil {
		return
	}
	tickerNodes[n] = struct{}{}
	ensureAnimTicker()
}

// tickerTick 推进一帧加载态: 把集合里的节点逐个标脏。返回是否还有活动节点。
//
// 由 animTick 调用 (见 animate.go), 于是加载态与过渡共用一份心跳与停表逻辑。
func tickerTick() bool {
	for n := range tickerNodes {
		markNodeDirty(n)
	}
	return len(tickerNodes) > 0
}

// unregisterTicker 摘掉节点 (disposeNode 用, 避免永久标脏一个离树节点)。
func unregisterTicker(n *GuiNode) {
	delete(tickerNodes, n)
}

// ===== spinner: 转圈加载指示 =====
//
//	<spinner />                 // 缺省 24px
//	<spinner size={32} />
//	<spinner size={32} color="#3355aa" />
//
// 12 根刻度绕圈, 亮度按"离头部的角度"衰减 —— 形成转动的残影。
// 相位取自进程单调时钟, 与窗口事件无关, 所以不会因事件间隔不均而卡顿。

// spinnerSize 读 size prop (也接受 width/height), 缺省 24。
func (n *GuiNode) spinnerSize() int {
	for _, name := range []string{"size", "width", "height"} {
		if v, ok := n.PropNum(name); ok && v > 0 {
			return int(v)
		}
	}
	return spinnerDefault
}

// spinnerColor 读 color prop (缺省主题强调色)。
func (n *GuiNode) spinnerColor() color.RGBA {
	if s, ok := n.PropStr("color"); ok && s != "" {
		if c, ok := ParseColor(s); ok {
			return c
		}
	}
	return colorAccent
}

// intrinsicSpinner 是固定正方形尺寸 (长宽都跟随 size)。
func intrinsicSpinner(n *GuiNode) (w, h int) {
	s := n.spinnerSize()
	return s, s
}

// layoutSpinner 无子节点布局: 只登记帧推进 + 摆放零尺寸子节点 (通常没有)。
func layoutSpinner(n *GuiNode) {
	registerTicker(n)
	placeAbsoluteIn(n, inner(n))
}

// paintSpinner 画 12 根刻度, 按相位决定头尾亮度。
func paintSpinner(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	base := tint(n.spinnerColor(), disabled)
	cx := float64(b.X) + float64(b.W)/2
	cy := float64(b.Y) + float64(b.H)/2
	outer := math.Min(float64(b.W), float64(b.H)) / 2
	innerR := outer * 0.45
	thick := 2
	if outer >= 16 {
		thick = 3
	}
	// 相位: 头部刻度在 0..ticks 之间轮转
	head := spinnerHead()
	for i := 0; i < spinnerTicks; i++ {
		// 该刻度距头部的"落后格数" (最近的过去, 0..ticks-1)
		behind := (head - i + spinnerTicks*2) % spinnerTicks
		alpha := 1.0 - float64(behind)/float64(spinnerTicks)
		if alpha < 0.08 {
			alpha = 0.08
		}
		c := withAlpha(base, alpha)
		ang := float64(i) / float64(spinnerTicks) * 2 * math.Pi
		sin, cos := math.Sincos(ang)
		x0 := cx + innerR*cos
		y0 := cy + innerR*sin
		x1 := cx + outer*cos
		y1 := cy + outer*sin
		drawThickLine(img, int(x0), int(y0), int(x1), int(y1), thick, c)
	}
}

// spinnerHead 返回当前"头部刻度"的索引 (0..spinnerTicks-1)。
// 用单调时钟求余, 与 any 事件无关。
func spinnerHead() int {
	el := time.Since(spinnerEpoch)
	if el < 0 {
		el = 0
	}
	frac := float64(el%spinnerPeriod) / float64(spinnerPeriod)
	return int(frac * float64(spinnerTicks))
}

// spinnerEpoch 是相位计时起点。
var spinnerEpoch = time.Now()

// withAlpha 给颜色乘一个 alpha 系数 (0..1), 做刻度的亮度衰减。
func withAlpha(c color.RGBA, f float64) color.RGBA {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: uint8(float64(c.A) * f)}
}

// drawThickLine 画一条任意方向的粗线 (以方块填充, 无抗锯齿 —— 与内核
// 其它装饰线同一路线: 观感一致、零依赖)。
func drawThickLine(img *image.RGBA, x0, y0, x1, y1, thick int, c color.RGBA) {
	if thick < 1 {
		thick = 1
	}
	steps := max(absInt(x1-x0), absInt(y1-y0))
	if steps == 0 {
		FillRect(img, Rect{X: x0 - thick/2, Y: y0 - thick/2, W: thick, H: thick}, c)
		return
	}
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(float64(x0) + (float64(x1)-float64(x0))*t)
		y := int(float64(y0) + (float64(y1)-float64(y0))*t)
		FillRect(img, Rect{X: x - thick/2, Y: y - thick/2, W: thick, H: thick}, c)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// ===== skeleton: 骨架屏 =====
//
//	<skeleton rows={3} />
//	<skeleton rows={2} avatar={true} />
//
// 占位灰条 + 呼吸式明暗 (active), 让"内容还在路上"这件事一眼可读。
// rows 是条数; avatar 为真时左侧多一个圆形占位 (列表项常见形态)。

// skeletonRows 读 rows prop, 缺省 3 (与多数 UI 库一致)。
func (n *GuiNode) skeletonRows() int {
	if v, ok := n.PropNum("rows"); ok && v > 0 {
		return int(v)
	}
	return 3
}

// skeletonActive 读 active prop (缺省 true: 默认就呼吸)。
func (n *GuiNode) skeletonActive() bool {
	if v, ok := n.PropBool("active"); ok {
		return v
	}
	return true
}

// skeletonAvatar 读 avatar prop。
func (n *GuiNode) skeletonAvatar() bool {
	v, _ := n.PropBool("avatar")
	return v
}

// skeletonTitleW 读 titleWidth prop (可选, 首条宽度比例 %, 缺省 40)。
func (n *GuiNode) skeletonTitleW() float64 {
	if v, ok := n.PropNum("titleWidth"); ok && v > 0 && v <= 100 {
		return v / 100
	}
	return 0.4
}

// intrinsicSkeleton 算骨架屏固有尺寸。
//
// 宽度: 给了 width 用它, 否则缺省 200 (骨架屏通常由父容器 stretch 撑开)。
// 高度: avatar 行高 或 rows*(条高+间距) - 间距, 取较大者。
func intrinsicSkeleton(n *GuiNode) (w, h int) {
	w = 200
	rows := n.skeletonRows()
	lineH := rows*skeletonRowH + (rows-1)*skeletonGap
	if lineH < 0 {
		lineH = 0
	}
	avat := 0
	if n.skeletonAvatar() {
		avat = skeletonAvatarSize
	}
	if avat > lineH {
		h = avat
	} else {
		h = lineH
	}
	return w, h
}

const skeletonAvatarSize = 36

// layoutSkeleton 无子节点布局 (纯自绘), 只登记帧推进。
func layoutSkeleton(n *GuiNode) {
	if n.skeletonActive() {
		registerTicker(n)
	} else {
		unregisterTicker(n)
	}
	placeAbsoluteIn(n, inner(n))
}

// paintSkeleton 画占位条 (avatar 圆 + rows 条灰条), active 时叠加呼吸明暗。
func paintSkeleton(img *image.RGBA, n *GuiNode, disabled bool) {
	area := inner(n)
	if area.W <= 0 || area.H <= 0 {
		return
	}
	base := tint(colorTrack, disabled)
	if n.skeletonActive() {
		// 呼吸: 在 0.55..1.0 之间往返 (三角波, 比正弦更"有节奏")
		base = withAlpha(base, skeletonBreath())
	}
	x := area.X
	lineW := area.W
	if n.skeletonAvatar() {
		// 头像圆
		cy := area.Y + min(area.H, skeletonAvatarSize)/2
		FillCircle(img, x+skeletonAvatarSize/2, cy, skeletonAvatarSize/2, base)
		x += skeletonAvatarSize + skeletonGap
		lineW = area.X + area.W - x
		if lineW < 0 {
			lineW = 0
		}
	}
	rows := n.skeletonRows()
	y := area.Y
	for i := 0; i < rows; i++ {
		w := lineW
		if i == 0 && !n.skeletonAvatar() {
			w = int(float64(lineW) * n.skeletonTitleW())
		}
		fillRoundRect(img, Rect{X: x, Y: y, W: w, H: skeletonRowH}, skeletonRadius, base)
		y += skeletonRowH + skeletonGap
	}
}

// skeletonBreath 返回 0.55..1.0 之间的呼吸系数 (三角波, 周期 skeletonPeriod)。
func skeletonBreath() float64 {
	el := time.Since(spinnerEpoch)
	if el < 0 {
		el = 0
	}
	frac := float64(el%skeletonPeriod) / float64(skeletonPeriod)
	// 三角波: 0→1→0
	tri := frac * 2
	if tri > 1 {
		tri = 2 - tri
	}
	return 0.55 + 0.45*tri
}

// 保证 object 在本文件的测试/构建组合下被引用 (Builtin 命名与其它组件一致)。
var _ = object.UndefinedSingleton
