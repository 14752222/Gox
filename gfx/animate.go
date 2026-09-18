package gfx

import (
	"image/color"
	"math"
	"time"

	"github.com/14752222/Gox/object"
)

// 过渡动画 (P3-2): 数值属性的补间。
//
// 两条互补的入口:
//
//  1. **声明式** —— `<rect transition={{width: 150}} />`: 该 prop 的**值发生变化**
//     时, 不直接跳过去, 而是在 duration 毫秒内按 ease-out 逐帧逼近。
//     关键在于本框架是**受控模型**: prop 就是唯一真相, 脚本一 setSignal
//     写回的就是终值 —— 要是直接读 PropNum, 画面会瞬间跳变, 动画无从谈起。
//     所以布局/绘制读取处一律走 effectivePropNum: 动画激活期间它**只认插值**
//     (忽略了已经变成终值的 prop), 动画结束才回落到 prop。
//
//  2. **命令式** —— `animate(node, prop, to, durationMs)` 或
//     `animate(from, to, durationMs, onUpdate, onDone)`: 返回 cancel 函数。
//
// 帧驱动复用 `object.GlobalScheduler().SetTimeout(16ms)` (与 `requestAnimationFrame`
// 同一套), 由宿主的事件循环 `RunTimersWithPump` 推进 —— 与光标闪烁同理:
// 没有事件也没有定时器时泵会睡着, 但动画本身会挂一个 16ms 定时器, 所以
// 动画期间泵始终是醒的。**只有存在活动动画时才续表**, 静止时零开销。
//
// v1 只做**数值属性** (见 animatableProps), 颜色过渡故意不做 (需要在
// 插值时解析两个颜色, 收益与复杂度不成比例)。

// animatableProps 是可以做过渡的属性集合。
//
// 为什么是这几个: 它们都是布局/绘制阶段直接读数值的属性, 且都是"连续量"。
// 刻意排除:
//   - `padding`/`margin`/`gap`/`flexGrow`: 尺寸类的抖动比位置类明显得多,
//     且 margin 参与兄弟位置分配, 半像素的中间值会带来难看的文字穿透。
//   - `value`(progress/slider): 它们是**受控组件值**, 过渡会与脚本自己的
//     写回打架 (脚本写 40, 控件显示 37.2, 下一帧脚本再读就懵了)。
var animatableProps = map[string]bool{
	"width":   true,
	"height":  true,
	"left":    true,
	"top":     true,
	"opacity": true,
}

// animFrame 是动画帧间隔 (~60fps), 与 jsRAF 的 16ms 保持一致。
const animFrame = 16 * time.Millisecond

// animState 是一次进行中的过渡。
//
// from 是**启动那一刻的显示值** (不是上一次的目标值) —— 动画还没结束就
// 被新的目标打断时, 从"当前看到的那个值"接着走才不会跳。
type animState struct {
	from  float64
	to    float64
	start time.Time
	dur   time.Duration
}

// at 返回 t 时刻的插值 (ease-out, t 已钳到 [0,1])。
func (s *animState) at(now time.Time) float64 {
	return s.from + (s.to-s.from)*easeOut(animProgress(s, now))
}

// done 报告过渡是否已经走完。
func (s *animState) done(now time.Time) bool {
	return animProgress(s, now) >= 1
}

// animProgress 返回归一化进度 [0,1]。
//
// 三个边界: dur<=0 视为"瞬时完成" (直接给 1, 免得除零); 时钟回拨
// (now 早于 start) 给 0 而不是负值。
func animProgress(s *animState, now time.Time) float64 {
	if s.dur <= 0 {
		return 1
	}
	el := now.Sub(s.start)
	if el <= 0 {
		return 0
	}
	if el >= s.dur {
		return 1
	}
	return float64(el) / float64(s.dur)
}

// easeOut 是缓出曲线 (cubic ease-out): 起步快、收尾慢。
// 相比线性, 像素级的移动看起来更像"有惯性"; 也避免整段动画显得僵硬。
func easeOut(t float64) float64 {
	inv := 1 - t
	return 1 - inv*inv*inv
}

// ===== 包级动画注册表 =====

var (
	// animNodes 是"当前有活动动画"的节点集合。用集合而不是切片是为了
	// 让"同节点重复登记"天然幂等 (多属性同时动画时只登一次)。
	animNodes = map[*GuiNode]struct{}{}
	// animRunning 报告动画心跳定时器是否已挂。**防重复挂表** ——
	// 每个 startTransition 都挂一次定时器的话, 10 个属性就等于 10 份心跳。
	animRunning bool
	// animNow 是取当前时间的函数 (测试可替换成假时钟)。
	animNow = time.Now
)

// startTransition 启动/改写一次属性过渡 (声明式 entry, 首次赋值不做过渡)。
//
// 详见 startTransitionFrom —— 这里只是把 allowFirst 固定为 false 的薄封装。
func startTransition(n *GuiNode, prop string, to float64) {
	startTransitionFrom(n, prop, to, false)
}

// startTransitionFrom 是 startTransition 的完整形态: allowFirst 控制
// "该属性此前完全没有值时, 要不要也做过渡"。
//
//   - 声明式 (transition prop + 受控值): false —— 与 CSS 一致, 元素刚出现时
//     不该"从 0 长出来"; 那种入场动画要由脚本显式 animate() 表达。
//   - 命令式 (animate(node, prop, ...)): true —— 脚本明确要求"从当前值
//     动到目标值", 哪怕当前值是 0 (属性还没设过), 也该动。
func startTransitionFrom(n *GuiNode, prop string, to float64, allowFirst bool) {
	if n == nil || !animatableProps[prop] {
		return
	}
	if !n.hasTransition() {
		return
	}
	dur := transitionDuration(n, prop)
	if dur <= 0 {
		// 没配 duration (或配成 0): 不退化为"瞬时", 而是根本不起动画 ——
		// 调用方会走原来的 markNodeDirty 路径。
		return
	}
	// 顺序要紧: **先读显示值, 再覆盖 prop** —— props 是受控模型的唯一真相,
	// 一写就把"当前显示值"覆盖成终值了, 顺序反了会得到 from == to 而跳过
	// 整段动画 (现象: 过渡完全不起作用, 且不报错)。
	from := effectivePropNum(n, prop)
	_, had := n.PropNum(prop)
	n.Props[prop] = object.NewNumber(to)
	if !had && !allowFirst {
		// 首次赋值 + 声明式: 不起动画 (与 CSS transition 一致)。
		// 判定用"赋值前 prop 是否存在"而不是 from == 0: 显式写 width={0}
		// 再过渡到 100 是合理用法 (from 确实等于 0), 不能一概跳过。
		markNodeDirty(n)
		return
	}
	if from == to {
		// 目标没变 (signal 重跑时值常常没变): 不起动画, 只需标脏刷新。
		markNodeDirty(n)
		return
	}
	if n.anim == nil {
		n.anim = map[string]*animState{}
	}
	n.anim[prop] = &animState{from: from, to: to, start: animNow(), dur: dur}
	animNodes[n] = struct{}{}
	ensureAnimTicker()
	markNodeDirty(n)
}

// transitionDuration 读节点某属性的过渡时长 (毫秒)。
//
// `transition` prop 有两形态:
//
//	transition={{width: 150}}          // 按属性分别配置
//	transition={150}                   // 所有可动画属性共用 (number 简写)
//
// 属性没列在映射里就返回 0 = "这个属性不过渡"。
func transitionDuration(n *GuiNode, prop string) time.Duration {
	v, ok := n.Props["transition"]
	if !ok {
		return 0
	}
	switch t := v.(type) {
	case *object.Number:
		// 简写形态: 所有属性共用一个时长
		if t.Value <= 0 {
			return 0
		}
		return time.Duration(t.Value) * time.Millisecond
	case *object.Object:
		pv, ok := t.GetProperty(prop)
		if !ok {
			return 0
		}
		if num, ok := pv.(*object.Number); ok && num.Value > 0 {
			return time.Duration(num.Value) * time.Millisecond
		}
	}
	return 0
}

// hasTransition 报告节点是否配了过渡 (供 reactiveProp 快速分流)。
func (n *GuiNode) hasTransition() bool {
	if n == nil {
		return false
	}
	_, ok := n.Props["transition"]
	return ok
}

// PropHasTransition 报告该属性是否**真的**配了过渡时长 (即 transitionDuration
// 非 0)。这是 hasTransition 的逐属性版本 —— 演示/回归测试用它按特征定位节点
// (比按下标取稳), 不必自己去解析 transition 的两种形态。
func (n *GuiNode) PropHasTransition(prop string) bool {
	return transitionDuration(n, prop) > 0
}

// effectivePropNum 是布局/绘制**唯一**该用的数值属性读取口: 动画激活期间
// 返回插值, 否则回落到 prop 原值。
//
// 为什么不能直接读 PropNum: 受控模型下 `n.Props[prop]` 在动画一开始就已经是
// **终值**了 (见 startTransition)。直接读它等于取消动画。所以这里必须
// 以"动画期间只认 animState"为准。
func effectivePropNum(n *GuiNode, name string) float64 {
	if s, ok := n.anim[name]; ok {
		if now := animNow(); !s.done(now) {
			return s.at(now)
		}
		// 已走完但心跳还没轮到它: 直接用终值 (等同于 tick 清理后的结果),
		// 不能让读者看到"过期的一帧"。清理交给 animTick。
		return s.to
	}
	v, _ := n.PropNum(name)
	return v
}

// effectivePropNumOk 与 effectivePropNum 同义, 但额外报告"属性是否存在"。
//
// 这个区分很重要: 布局要靠 `width` 是否存在来判断"该用固有尺寸还是显式尺寸"
// (见 intrinsicSize / hasExplicitCross)。动画一挂上 animState, 属性就
// 必须算作存在 —— 否则会出现"过渡到一半时节点突然回退到内容尺寸"。
func effectivePropNumOk(n *GuiNode, name string) (float64, bool) {
	if s, ok := n.anim[name]; ok {
		if now := animNow(); !s.done(now) {
			return s.at(now), true
		}
		return s.to, true
	}
	return n.PropNum(name)
}

// effectiveOpacity 返回节点的显示不透明度 (默认 1)。
//
// 与 effectivePropNum 分开是因为缺省值不同: 没写 `opacity` 的节点是
// **不透明** (1), 而 effectivePropNum 的缺省是 0。
// 顺手把非法值 (NaN) 与越界值钳掉 —— opacity 一旦变成 NaN, 整棵子树
// 的混合计算都会塌成透明 (表现为"节点凭空消失")。
func effectiveOpacity(n *GuiNode) float64 {
	v := effectivePropNum(n, "opacity")
	if _, ok := n.anim["opacity"]; !ok {
		if _, ok := n.PropNum("opacity"); !ok {
			return 1
		}
	}
	if math.IsNaN(v) {
		return 1
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// ===== 子树淡出 (opacity) =====

// fadeStack 是绘制期的"当前不透明度"栈。
//
// 为什么不把 opacity 当参数一路传下去: 绘制原语的签名 (FillRect/StrokeRect/
// DrawText/FillCircle...) 到处都是调用点, 加参数是几十处机械改动; 而
// opacity 是**绘制期全局状态**, 本来就不该出现在公开原语的签名里。
// 用显式栈 (而不是一个可变的全局量) 是为了配 drawNode 的递归:
// 进入子树前压, 出来弹 —— 任何一条 return 路径都不会把值泄漏给兄弟节点。
//
// CSS 语义: opacity 是**成组**的 —— 父节点 0.5 意味着整棵子树整体半透明,
// 而不是"子节点各自 0.5 再混合"。栈式相乘恰好就是这个语义。
var fadeStack = []float64{1}

// applyFade 把当前不透明度乘到颜色的 alpha 上 (其余通道不动)。
//
// 乘 alpha (而不是与背景做颜色插值) 才是"淡出": 保留原色相、只是越来越透。
// 栈顶为 1 时直接返回原值, 让**绝大多数绘制走零开销的快路径**。
func applyFade(c color.RGBA) color.RGBA {
	f := fadeStack[len(fadeStack)-1]
	if f >= 1 {
		return c
	}
	if f <= 0 {
		return color.RGBA{}
	}
	c.A = uint8(float64(c.A) * f)
	return c
}

// fadeOf 返回当前不透明度因子 (栈顶)。给绕过 FillRect 的绘制路径用 (image 的
// 像素直拷); 绝大多数调用方直接用 applyFade 即可。
func fadeOf() float64 {
	return fadeStack[len(fadeStack)-1]
}

// pushFade 压入一层不透明度, 返回一个"恢复"闭包 (defer 用)。
func pushFade(f float64) func() {
	prev := fadeStack[len(fadeStack)-1]
	fadeStack = append(fadeStack, prev*f)
	return func() { fadeStack = fadeStack[:len(fadeStack)-1] }
}

// ===== 动画心跳 =====

// ensureAnimTicker 挂上动画心跳定时器 (已挂则不重复挂)。
func ensureAnimTicker() {
	if animRunning {
		return
	}
	animRunning = true
	scheduleAnimTick()
}

// scheduleAnimTick 登记下一次心跳。
func scheduleAnimTick() {
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("animTick", func(args ...object.Value) object.Value {
		animTick()
		return object.UndefinedSingleton
	}), animFrame)
}

// animTick 推进一帧动画。
//
// **只标脏, 不直接重绘**: 重绘交给 Pump 的脏矩形流程 (与 canvas 的
// "onDraw 里再标脏不会递归重绘"同一条纪律)。在这里直接 redraw 的话,
// 一帧里会被每个动画节点各触发一次全帧重绘。
func animTick() {
	now := animNow()
	for n := range animNodes {
		for prop, s := range n.anim {
			if s.done(now) {
				delete(n.anim, prop)
				continue
			}
			markNodeDirty(n)
		}
		if len(n.anim) == 0 {
			n.anim = nil
			delete(animNodes, n)
			continue
		}
		// 还没走完: 保证下一帧继续 (它可能只是"这一帧刚好还没结束")。
		markNodeDirty(n)
	}
	if len(animNodes) > 0 {
		scheduleAnimTick()
		return
	}
	animRunning = false // 全部收工: 停表, 静止态零开销
}

// cancelAnim 摘掉节点的全部动画 (disposeNode 用), 避免心跳标脏一个离树节点。
func cancelAnim(n *GuiNode) {
	if n == nil {
		return
	}
	if n.anim != nil {
		n.anim = nil
	}
	delete(animNodes, n)
}

// ===== 命令式 API =====

// jsAnimate 实现 `animate(...)`, 两种签名:
//
//	animate(node, prop, to, durationMs)                     → cancel 函数
//	animate(from, to, durationMs, onUpdate, onDone?)        → cancel 函数
//
// 前者是"给某个元素做属性过渡"的简写 (等价于挂 transition + 改 signal);
// 后者是纯粹的数值补间, 回调里一般写 signal。
//
// 两者都返回 cancel 函数 (调用即停, 停在当前值上, 不跳回)。
func jsAnimate(args ...object.Value) object.Value {
	if len(args) >= 4 {
		if n, ok := args[0].(*GuiNode); ok {
			return animateNodeProp(n, args[1:])
		}
	}
	return animateRaw(args)
}

// animateNodeProp 实现 animate(node, prop, to, durationMs)。
func animateNodeProp(n *GuiNode, rest []object.Value) object.Value {
	if len(rest) < 1 {
		return object.NewTypeError("animate: prop name required")
	}
	prop := valueText(rest[0])
	if !animatableProps[prop] {
		return object.NewTypeError("animate: prop %s is not animatable", prop)
	}
	to := numberArg(rest, 1, 0)
	dur := numberArg(rest, 2, 0)

	// 命令式与声明式共用一套 animState: 走同一个插值出口 (effectivePropNum),
	// 于是"命令式动画期间布局读到的也是插值"。
	st := &animState{from: effectivePropNum(n, prop), to: to, start: animNow(),
		dur: time.Duration(dur) * time.Millisecond}
	if st.dur <= 0 {
		// 时长为 0: 直接落终值, 不起表。
		n.Props[prop] = object.NewNumber(to)
		markNodeDirty(n)
		return object.NewBuiltin("cancel", func(args ...object.Value) object.Value {
			return object.UndefinedSingleton
		})
	}
	if n.anim == nil {
		n.anim = map[string]*animState{}
	}
	n.anim[prop] = st
	animNodes[n] = struct{}{}
	ensureAnimTicker()
	markNodeDirty(n)
	return animCancelFn(n, prop, st)
}

// animCancelFn 造一个"取消某个动画"的函数。取消时把**当前插值**写回 prop。
//
// 为什么写回当前值而不是目标值: 取消的语义是"停在这里", 不是"跳到终点"。
// 不写回的话下一帧 effectivePropNum 回落到 prop (还是旧值), 画面会跳回去。
func animCancelFn(n *GuiNode, prop string, st *animState) object.Value {
	return object.NewBuiltin("cancelAnimation", func(args ...object.Value) object.Value {
		cur, ok := n.anim[prop]
		if !ok || cur != st {
			return object.UndefinedSingleton // 已被新动画顶掉 / 已自然结束
		}
		v := st.at(animNow())
		delete(n.anim, prop)
		if len(n.anim) == 0 {
			n.anim = nil
			delete(animNodes, n)
		}
		n.Props[prop] = object.NewNumber(v)
		markNodeDirty(n)
		return object.UndefinedSingleton
	})
}

// animateRaw 实现 animate(from, to, durationMs, onUpdate, onDone?)。
//
// 与声明式的区别: 这里**不碰元素树**, 每帧把插值交给 onUpdate(v), 由脚本
// 自己决定写哪个 signal。所以它不需要 animState 表 —— 一个定时器链足矣。
//
// 返回一个 cancel 函数: 调用后停止推进, **不**补发 onDone (取消不是完成),
// 也不会把值跳到终点。
func animateRaw(args []object.Value) object.Value {
	if len(args) < 4 {
		return object.NewTypeError("animate: (from, to, durationMs, onUpdate, onDone?) required")
	}
	from := numOf(args[0])
	to := numOf(args[1])
	dur := time.Duration(numOf(args[2])) * time.Millisecond
	onUpdate := args[3]
	if !object.IsCallable(onUpdate) {
		return object.NewTypeError("animate: onUpdate must be a function")
	}
	var onDone object.Value
	if len(args) >= 5 && object.IsCallable(args[4]) {
		onDone = args[4]
	}
	// token 是这次动画的身份: cancel 把它置 nil, 链上的下一帧看到就停。
	// 用共享的标记位而不是 Clear(timerID), 是因为链会不断登记**新的**
	// 定时器 —— cancel 时拿不到"将来那个还没登记出来的" id。
	token := &animToken{}
	if dur <= 0 {
		// 时长为 0: 直接落终点 (与 animate(node, prop, ...) 的口径一致),
		// 不挂表 —— 否则 t 恒为 0, onUpdate 会被无限调用 0 值。
		callScriptFn(onUpdate, object.NewNumber(to))
		if onDone != nil {
			callScriptFn(onDone)
		}
		return object.NewBuiltin("cancelAnimation", func(cargs ...object.Value) object.Value {
			return object.UndefinedSingleton
		})
	}
	runAnimChain(token, from, to, dur, animNow(), onUpdate, onDone)
	return object.NewBuiltin("cancelAnimation", func(cargs ...object.Value) object.Value {
		token.cancel()
		return object.UndefinedSingleton
	})
}

// animToken 是一次命令式动画的存活标记。
type animToken struct {
	cancelled bool
}

func (t *animToken) cancel() { t.cancelled = true }

// runAnimChain 登记"下一帧"并递归推进。
//
// 采用"每帧一个一次性定时器"而不是 SetInterval: 一是与 RAF 语义一致
// (回调里若又起一段动画, 不会和旧 interval 撞车); 二是帧率可随
// requestAnimationFrame 的既有实现统一调整。
func runAnimChain(token *animToken, from, to float64, dur time.Duration, start time.Time,
	onUpdate, onDone object.Value) {
	if token.cancelled {
		return
	}
	object.GlobalScheduler().SetTimeout(object.NewBuiltin("animateTick", func(args ...object.Value) object.Value {
		if token.cancelled {
			return object.UndefinedSingleton
		}
		now := animNow()
		t := 0.0
		if dur > 0 {
			t = float64(now.Sub(start)) / float64(dur)
		}
		done := t >= 1
		if done {
			t = 1
		}
		v := from + (to-from)*easeOut(t)
		callScriptFn(onUpdate, object.NewNumber(v))
		if done {
			if onDone != nil {
				callScriptFn(onDone)
			}
			return object.UndefinedSingleton
		}
		runAnimChain(token, from, to, dur, start, onUpdate, onDone)
		return object.UndefinedSingleton
	}), animFrame)
}

// numberArg 读位置参数里的数字 (缺省 def)。
func numberArg(args []object.Value, i int, def float64) float64 {
	if i >= len(args) {
		return def
	}
	return numOf(args[i])
}

// numOf 把参数当数字读 (非数值一律 0, 与 canvasNum 同一套宽松口径)。
//
// 刻意不报错: 一个 undefined 的时长参数不该让整段动画抛异常 ——
// 最坏的结果是"动画时长按 0 处理, 直接落终值", 界面照常可用。
func numOf(v object.Value) float64 {
	switch x := v.(type) {
	case *object.Number:
		if math.IsNaN(x.Value) || math.IsInf(x.Value, 0) {
			return 0
		}
		return x.Value
	case *object.Boolean:
		if x.Value {
			return 1
		}
	}
	return 0
}
