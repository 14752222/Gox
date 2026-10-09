package gfx

// 组件级手势: 长按 (onLongPress) 与滑动 (onSwipe)。
//
// 移动端应用的地基 —— apps #9 图片整理的「长按多选」、列表滑动删除、下拉刷新
// 全靠它。在此之前全仓零命中, 手势完全不存在。
//
// ===== 为什么放在事件泵层而不是组件里 =====
//
// 长按要**计时**, 滑动要**跨事件累积位移**, 两者都是"按下 → 移动 → 抬起"这条
// 序列上的整体判定, 任何单个组件都看不到完整序列。放在事件泵层 (与 hover /
// press / drag 同一条线程、同一套字段纪律) 才能让任意节点都白拿这两个手势。
//
// ===== 长按计时为什么不用 time.AfterFunc =====
//
// 与 tooltip 同源的限制 (见 tooltip.go 文件头): 泵没有事件、没有 JS 定时器时
// 会睡死在 WaitEvents 里, AfterFunc 里 Post 的任务唤醒不了它。所以沿用同一套
// 解法 —— 把 Pump 的等待预算钳到 deadline (nextLongPressWake), 泵到点自然醒来,
// processEvents 里的 tickLongPress 完成派发。全程单线程, 不引入并发路径。
//
// ===== 与 click 的关系 =====
//
// 长按或滑动一旦成立, 抬起时**不再派发 click** (置 swallowClick) —— 移动端
// 语义里长按不是点击、滑动也不是点击。没成立 (没移动、没到时长) 则完全走
// 原有的 click 路径, 行为不变。

import (
	"math"
	"time"

	"github.com/14752222/Gox/object"
)

// 手势阈值的全局缺省值: 节点没写对应 prop 时用这里的。
// 导出是为了让内嵌方 (纯 Go 场景) 能整体调一遍手感, 不必挨个节点写 prop。
var (
	// DefaultLongPressDelay 是"按住多久算长按"。
	// 500ms 与 tooltip 的缺省延迟同值: 两者都是"用户停下来了"的判定,
	// 取同一个数免得出现"提示先弹出来、长按还没成立"的错位窗口。
	DefaultLongPressDelay = 500 * time.Millisecond

	// DefaultLongPressSlop 是长按期间容许的抖动 (px)。手指/鼠标按住不动
	// 也会有 1~2px 漂移, 判据太紧会让长按永远成立不了。
	DefaultLongPressSlop = 10

	// DefaultSwipeThreshold 是判定成滑动的最小位移 (px)。
	// 40 与移动端惯例一致 (Android 的 touch slop 往上取整)。
	DefaultSwipeThreshold = 40
)

// ===== props =====

// longPressDelay 读长按阈值 (longPressDelay prop, 毫秒)。非法/缺省用全局值。
func (n *GuiNode) longPressDelay() time.Duration {
	if n == nil {
		return DefaultLongPressDelay
	}
	if v, ok := n.PropNum("longPressDelay"); ok && v >= 0 {
		return time.Duration(v) * time.Millisecond
	}
	return DefaultLongPressDelay
}

// longPressSlop 读长按容许抖动 (longPressSlop prop, px)。负值按 0 处理。
func (n *GuiNode) longPressSlop() int {
	if n == nil {
		return DefaultLongPressSlop
	}
	if v, ok := n.PropNum("longPressSlop"); ok && v >= 0 {
		return int(v)
	}
	return DefaultLongPressSlop
}

// swipeThreshold 读滑动判定位移 (swipeThreshold prop, px)。负值按 0 处理。
func (n *GuiNode) swipeThreshold() int {
	if n == nil {
		return DefaultSwipeThreshold
	}
	if v, ok := n.PropNum("swipeThreshold"); ok && v >= 0 {
		return int(v)
	}
	return DefaultSwipeThreshold
}

// ===== 手势追踪状态 =====

// gesture 是"按下 → 移动 → 抬起"这一趟的追踪状态。字段全部由 VM 线程读写,
// 与 hoverChain / pressChain 同一纪律 (mu 只在拿取/放回时按既有习惯加锁)。
type gesture struct {
	active bool
	node   *GuiNode // 按下命中的最深节点 (派发时沿祖先链找处理器)
	sx, sy int      // 起点
	x, y   int      // 最新点
	t0     time.Time
	moved  bool // 位移已超过 slop ⇒ 长按作废
	fired  bool // 本次已派发过 onLongPress
}

// gestureBegin 在一次按下时开启追踪。
//
// 只在**通用按压路径**上调用 (组件自绘区如 slider / tabs / rating 走各自的
// 几何命中分支提前 return, 不参与手势) —— 它们的 mousedown 本身就是一次性
// 动作, 再叠一层长按/滑动只会互相干扰。
func (a *app) gestureBegin(n *GuiNode, x, y int) {
	a.mu.Lock()
	a.gest = gesture{active: n != nil, node: n, sx: x, sy: y, x: x, y: y, t0: time.Now()}
	a.lpNode = nil
	a.lpDeadline = time.Time{}
	a.mu.Unlock()
	if n == nil {
		return
	}
	// 只有链上真有人要这个手势才开始计时 —— 否则每个普通点击都要白跑一趟
	// 等待预算钳制 (Pump 得为不存在的长按提前醒来)。
	if h := handlerInChain(n, "onLongPress"); h != nil {
		a.mu.Lock()
		a.lpNode = h
		a.lpDeadline = time.Now().Add(h.longPressDelay())
		a.mu.Unlock()
	}
}

// gestureMove 更新最新点, 并在位移超过 slop 时作废长按。
func (a *app) gestureMove(x, y int) {
	a.mu.Lock()
	g := a.gest
	if !g.active {
		a.mu.Unlock()
		return
	}
	g.x, g.y = x, y
	slop := g.node.longPressSlop()
	if !g.moved && (absInt(x-g.sx) > slop || absInt(y-g.sy) > slop) {
		g.moved = true
		// 长按作废: 手指已经挪开了, 这不是"按住不动"
		a.lpNode = nil
		a.lpDeadline = time.Time{}
	}
	a.gest = g
	a.mu.Unlock()
}

// gestureEnd 在一次抬起时收尾: 判定并派发滑动。
//
// 返回 true 表示这一趟被识别成了滑动 —— 调用方据此吞掉紧随其后的 click。
func (a *app) gestureEnd(x, y int) bool {
	a.mu.Lock()
	g := a.gest
	a.lpNode = nil
	a.lpDeadline = time.Time{}
	a.gest = gesture{}
	a.mu.Unlock()
	if !g.active {
		return false
	}
	// 长按已经派发过了: 这一趟归长按, 不再判滑动 (长按后手指挪一下不算滑动)
	if g.fired {
		return true
	}
	g.x, g.y = x, y
	dx, dy := x-g.sx, y-g.sy
	th := g.node.swipeThreshold()
	if absInt(dx) < th && absInt(dy) < th {
		return false
	}
	h := handlerInChain(g.node, "onSwipe")
	if h == nil {
		return false
	}
	a.callHandler(h, "onSwipe", newSwipeArg(dx, dy, x, y, time.Since(g.t0)))
	return true
}

// gestureCancel 丢弃本次追踪 (光标离开窗口 / 按压被释放)。
func (a *app) gestureCancel() {
	a.mu.Lock()
	a.gest = gesture{}
	a.lpNode = nil
	a.lpDeadline = time.Time{}
	a.mu.Unlock()
}

// ===== 长按派发 =====

// tickLongPress 在长按计时到期时派发 onLongPress。由 processEvents 调用
// (与 tickTooltip 同一节拍), 泵被 nextLongPressWake 钳到 deadline 后醒来。
func (a *app) tickLongPress() {
	a.mu.Lock()
	h := a.lpNode
	deadline := a.lpDeadline
	a.mu.Unlock()
	if h == nil || deadline.IsZero() || time.Now().Before(deadline) {
		return
	}
	a.mu.Lock()
	// 与 showTooltip 同款防御: host 可能已被换掉/取消, 只派发仍归属自己的这次
	if a.lpNode != h || a.lpDeadline.IsZero() {
		a.mu.Unlock()
		return
	}
	a.lpDeadline = time.Time{}
	a.gest.fired = true
	g := a.gest
	// 长按成立 ⇒ 抬起时不再派发 click (移动端语义: 长按不是点击)
	a.swallowClick = true
	a.mu.Unlock()

	arg := object.NewObject()
	arg.SetProperty("x", object.NewNumber(float64(g.x)))
	arg.SetProperty("y", object.NewNumber(float64(g.y)))
	arg.SetProperty("duration", object.NewNumber(float64(time.Since(g.t0).Milliseconds())))
	a.callHandler(h, "onLongPress", arg)
}

// nextLongPressWake 返回全部窗口里最近的长按计时剩余时长; 没有任何计时在跑
// 时 ok=false。Pump 用它钳制等待预算 —— 已到期返回 0, 泵本轮立即醒来。
func nextLongPressWake() (time.Duration, bool) {
	found := false
	var out time.Duration
	appMu.Lock()
	for _, a := range apps {
		a.mu.Lock()
		if a.lpNode != nil && !a.lpDeadline.IsZero() {
			d := time.Until(a.lpDeadline)
			if d < 0 {
				d = 0
			}
			if !found || d < out {
				out = d
				found = true
			}
		}
		a.mu.Unlock()
	}
	appMu.Unlock()
	return out, found
}

// ===== 载荷 =====

// newSwipeArg 构造 onSwipe 的载荷。
//
// direction 取**位移较大的那个轴** (dominant axis), 与移动端惯例一致:
// 斜着划也只报一个方向, 免得脚本自己去做"这算横滑还是竖滑"的判定。
// dx / dy 保留符号 (向右为正、向下为正), distance 是欧氏距离 —— 想要"划了
// 多远"用 distance, 想要"往哪划、各划了多少"用 direction + dx/dy。
func newSwipeArg(dx, dy, x, y int, dur time.Duration) object.Value {
	arg := object.NewObject()
	arg.SetProperty("direction", object.NewString(swipeDirection(dx, dy)))
	arg.SetProperty("dx", object.NewNumber(float64(dx)))
	arg.SetProperty("dy", object.NewNumber(float64(dy)))
	arg.SetProperty("distance", object.NewNumber(math.Sqrt(float64(dx*dx+dy*dy))))
	arg.SetProperty("duration", object.NewNumber(float64(dur.Milliseconds())))
	arg.SetProperty("x", object.NewNumber(float64(x)))
	arg.SetProperty("y", object.NewNumber(float64(y)))
	return arg
}

// swipeDirection 按主导轴给出方向名。
func swipeDirection(dx, dy int) string {
	if absInt(dx) >= absInt(dy) {
		if dx < 0 {
			return "left"
		}
		return "right"
	}
	if dy < 0 {
		return "up"
	}
	return "down"
}

// gestureActive 报告当前是否正在追踪一趟手势 (测试与自身断言用)。
func (a *app) gestureActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.gest.active
}
