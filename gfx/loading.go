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

// 循环动画参数 (Prism S7)。
//
// 三处无限循环 (spinner 旋转 / skeleton 呼吸 / progress 不定位移) 共用一套
// 节奏: 都在 900~1200ms 这一档。为什么不直接用 MotionSlow(260ms): 那个
// 量级是**过渡**的 (从 A 到 B 走完就停), 套在无限循环上等于每秒四个来回,
// 焦躁且抢注意力。这一档是 Material 的 circular-progress 与各大库 shimmer
// 的共同取值区间。
//
// 三者共用一个时间原点 loopEpoch: 树上同时挂着的加载态会**同步呼吸**
// (列表里五个骨架条一起明暗), 而不是各按各的挂载时刻错开 —— 同屏多个
// 加载指示器错相闪动是最廉价也最难看的那种动效。
const (
	spinnerDefault = 24          // 缺省直径 (外半径 12)
	spinnerRingW   = 3           // 环宽 (缺省尺寸下 24/2-3 = 内半径 9)
	spinnerArc     = math.Pi / 2 // 高亮弧的张角: 90°
	spinnerPeriod  = 900 * time.Millisecond

	skeletonRowH   = 14
	skeletonGap    = 8
	skeletonPeriod = 1200 * time.Millisecond
	// skeletonBreathSpan 是呼吸两端灰度的通道跨度。
	//
	// 骨架条没有独立 token (61 个 token 的字段集 S1 已冻结), 所以两级灰度
	// 由 track 这一档派生: 暗端 = track, 亮端 = track 提亮 28。这与 S1 里
	// accentHoverLight 那两个常量的处境完全一样 —— 需要但它不在字段集里,
	// 就以常量形式钉在组件层并写进注释, 免得各组件各拍一个值。
	skeletonBreathSpan = 28
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

// unregisterTickersIn 摘掉某棵树上全部加载态的帧登记 (关窗口时调用)。
//
// 为什么不能在 disposeNode 里等着: 整棵树随窗口一起消失时**没人走 dispose**
// (disposeNode 是给"旧子树被换掉"用的那条路), 而 tickerNodes 与心跳是包级
// 状态 —— 不摘的话, 关掉的窗口会留着 16ms 心跳, 每帧去标脏已经不上屏的节点,
// 一直到进程结束。这是 S6 修的那个"没人起心跳"的镜像: 那边是抽屉关了却没人
// 起表, 这边是窗口关了却没人摘表。
//
// 只摘这棵树的: 多窗口下关掉一个, 另一个窗口的加载态还得继续转。
func unregisterTickersIn(root *GuiNode) {
	if root == nil {
		return
	}
	for n := range tickerNodes {
		for p := n; p != nil; p = p.Parent {
			if p == root {
				unregisterTicker(n)
				break
			}
		}
	}
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
// Prism S8: size / width / height 是脚本写的**逻辑值**, 与 avatarSize 同一条
// 口径 —— 直接返回 int(v) 的结果是"写了 size={48} 的 spinner 在 2x 屏上比缺省
// 的 24dp (→48px) 小一号", 而这个 bug 只在真机上看得见。
func (n *GuiNode) spinnerSize() int {
	for _, name := range []string{"size", "width", "height"} {
		if v, ok := n.PropNum(name); ok && v > 0 {
			return dpToPx(v)
		}
	}
	return Space(spinnerDefault)
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
//
// reduced-motion 下**不登记**: 弧的相位已被冻结 (spinnerArcStart), 每帧
// 重绘只会画出同一张图, 而心跳是 16ms 一次的定时器 —— 让晕动症用户为一张
// 静止的图付 60fps 的重绘代价是说不过去的。
func layoutSpinner(n *GuiNode) {
	if prefersReducedMotion() {
		unregisterTicker(n)
	} else {
		registerTicker(n)
	}
	placeAbsoluteIn(n, inner(n))
}

// paintSpinner 画转圈: 一整圈极淡的底环 + 一段 90° 的强调色高亮弧, 弧随时间
// 绕圆心转。
//
// 软光栅没有变换矩阵, "旋转"没有 transform 可用 —— 唯一的办法是**按角度落笔**:
// fillRingAA(img, cx, cy, rOuter, rInner, start, sweep, c) 直接收 (起始角, 张角)
// 两个参数, 让 start 随时间推进, 画出来的就是一段在转的弧。这也顺带解释了
// 为什么是"弧"而不是"渐隐的尾巴": 逐像素算覆盖率时, 一条尾巴要按角度插值
// 颜色, 而 fillRingAA 一次调用只认一个颜色; 分段画尾巴则要在环上叠十几次
// 半透明色, 成本与锯齿都不可控。90° 实弧是"能看清在转"的最经济表达。
func paintSpinner(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	d := min(b.W, b.H)
	if d < 4 {
		return
	}
	cx := float64(b.X) + float64(b.W)/2
	cy := float64(b.Y) + float64(b.H)/2
	outer := float64(d) / 2
	// 环宽随尺寸等比缩放 (24 时是 3), 小尺寸下保底 2 免得环断成虚线。
	ringW := float64(d) * float64(spinnerRingW) / float64(spinnerDefault)
	if ringW < 2 {
		ringW = 2
	}
	if ringW >= outer {
		ringW = outer - 1
	}
	innerR := outer - ringW
	// 底环: 比 track 更淡的一档 (surfaceInset), 只交代"这里有个圈"。
	fillRingAA(img, cx, cy, outer, innerR, 0, tau, tint(colorSurfaceInset, disabled))
	// 高亮弧: 从 12 点方向起步, 随时间绕行。
	fillRingAA(img, cx, cy, outer, innerR, spinnerArcStart(), spinnerArc,
		tint(n.spinnerColor(), disabled))
}

// spinnerArcStart 返回高亮弧的起始角 (弧度, 顺时针, 0 = 3 点方向)。
//
// reduced-motion 时恒返回 -tau/4 (12 点方向): 弧不再位移, 但"这是个转圈
// 加载指示器"的形状信息还在 —— 直接换成静态的"加载中"文字会丢掉空间占位,
// 换成不转的完整圆环又会让人以为卡住了。停在 12 点是最能读作"起点"的角度。
func spinnerArcStart() float64 {
	if prefersReducedMotion() {
		return -tau / 4
	}
	el := time.Since(loopEpoch)
	if el < 0 {
		el = 0
	}
	frac := float64(el%spinnerPeriod) / float64(spinnerPeriod)
	return -tau/4 + frac*tau
}

// loopEpoch 是三处加载态循环 (spinner / skeleton / progress 不定) 共用的
// 相位计时起点, 取进程单调时钟, 与 any 事件无关。
var loopEpoch = time.Now()

// withAlpha 给颜色乘一个 alpha 系数 (0..1)。
//
// 注意它**不是**"全局透明度": 软光栅没有图层不透明度, alpha 只在 FillRect
// 那一层按 src-over 与底下的像素混合。所以它能表达"淡入" (遮罩/渐显), 但
// 表达不了"这整块半透明" —— skeleton 的呼吸因此走颜色插值而不是它。
func withAlpha(c color.RGBA, f float64) color.RGBA {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: uint8(float64(c.A) * f)}
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
	// Prism S8: 缺省宽是设计像素 (与 inputMinW / selectMinW 同一类缺省).
	w = Space(200)
	rows := n.skeletonRows()
	lineH := rows*Space(skeletonRowH) + (rows-1)*Space(skeletonGap)
	if lineH < 0 {
		lineH = 0
	}
	avat := 0
	if n.skeletonAvatar() {
		avat = Space(skeletonAvatarSize)
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
//
// active=false (静态占位) 与 reduced-motion (呼吸停在中值) 都不上心跳:
// 两者每帧画出来的都是同一张图, 没有理由让 16ms 定时器一直跑。
func layoutSkeleton(n *GuiNode) {
	if n.skeletonActive() && !prefersReducedMotion() {
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
	// 呼吸 = 在两级灰度之间插值 (见 skeletonBreathColor 的注释)。
	base := tint(skeletonBreathColor(skeletonBreath(n.skeletonActive())), disabled)
	x := area.X
	lineW := area.W
	if n.skeletonAvatar() {
		// 头像圆
		avat := Space(skeletonAvatarSize)
		cy := area.Y + min(area.H, avat)/2
		FillCircle(img, x+avat/2, cy, avat/2, base)
		x += avat + Space(skeletonGap)
		lineW = area.X + area.W - x
		if lineW < 0 {
			lineW = 0
		}
	}
	rows := n.skeletonRows()
	y := area.Y
	// 圆角取 RadiusMD(8), 但条高只有 14 —— fillRoundRect 自己会把半径钳
	// 到 min(w,h)/2, 短条因此自然成为胶囊而不是"四角各缺一块"的怪形状。
	rad := Radius(RadiusMD)
	rowH, gap := Space(skeletonRowH), Space(skeletonGap)
	for i := 0; i < rows; i++ {
		w := lineW
		if i == 0 && !n.skeletonAvatar() {
			w = int(float64(lineW) * n.skeletonTitleW())
		}
		fillRoundRect(img, Rect{X: x, Y: y, W: w, H: rowH}, rad, base)
		y += rowH + gap
	}
}

// skeletonBreath 返回呼吸相位 [0,1] (三角波, 周期 skeletonPeriod)。
//
// 三角波而不是正弦: 直接拿相位算 sin 要在绘制热路径上引一次三角函数, 而
// 三角波只需两次比较 —— 而且 0→1→0 的往返在视觉上比正弦更"有节奏"
// (正弦在两端停留过久, 呼吸的两端会显得粘住)。
//
// 三条降级规矩:
//  1. active=false → 恒定 0 (最暗端)。静态骨架就该是一块确定的灰, 而不是
//     "恰好取到某一帧的呼吸态";
//  2. reduced-motion → 恒定 0.5 (中值)。**这条是硬要求**: 骨架呼吸是整块
//     大面积明暗变化, 对前庭敏感人群是最容易诱发不适的一类动效; 冻结在
//     中值既保留了"这是块占位"的信息, 又没有任何亮度变化;
//  3. 其余情况按墙钟推进。
func skeletonBreath(active bool) float64 {
	if !active {
		return 0
	}
	if prefersReducedMotion() {
		return 0.5
	}
	el := time.Since(loopEpoch)
	if el < 0 {
		el = 0
	}
	frac := float64(el%skeletonPeriod) / float64(skeletonPeriod)
	// 三角波: 0→1→0
	tri := frac * 2
	if tri > 1 {
		tri = 2 - tri
	}
	return tri
}

// skeletonBreathColor 按呼吸相位在两级灰度之间插值。
//
// 为什么是"插值颜色"而不是"插值 alpha": 软光栅没有全局图层不透明度,
// withAlpha 出来的半透明色要在 FillRect 里与**底下已经画好的像素**做 src-over,
// 于是骨架条盖在卡片底色上时它是"变淡", 盖在页面底色上时是另一种淡 —— 同一
// 个 alpha 在不同底上给出不同颜色, 深浅两套主题下还会差得更远。把颜色算成
// 确定的两个值再插值, 结果与画在什么上面无关, 也就能被像素断言直接验。
func skeletonBreathColor(phase float64) color.RGBA {
	return mixColor(colorTrack, brighten(colorTrack, skeletonBreathSpan), phase)
}

// 保证 object 在本文件的测试/构建组合下被引用 (Builtin 命名与其它组件一致)。
var _ = object.UndefinedSingleton
