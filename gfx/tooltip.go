package gfx

import (
	"image"
	"time"

	"github.com/14752222/Gox/object"
)

// tooltip 悬停提示 (S4 组件库二期)。
//
// 用法 (包裹式 —— 唯一流内子节点就是触发元素):
//
//	<tooltip text="保存到云端 (Ctrl+S)" placement="bottom" delay={500}>
//	  <button>保存</button>
//	</tooltip>
//
// 结构约定:
//   - tooltip 自身是**布局透明**的包裹容器 (与单子 slot/view 同语义): 盒子
//     就是触发元素的盒子, 自身不画任何东西。不用 prop 式 (<button tooltip=…>)
//     是因为那要给每个控件都开一个 prop 口子, 而 rect/canvas 这类"用户自己
//     拼的触发区"永远覆盖不到。
//   - 弹层 (tooltip-popup) 由 Go 侧构造并挂在 tooltip 下。它登记进
//     isOverlay —— 天生逃逸裁剪、拿 overlayZBase 抬升、不占常规流, 与
//     dialog/toast 同一套层叠模型; 没有 onClick 处理器, 命中测试穿过它
//     直达下层 (hitEscapesLayer 对非模态弹层继续下钻), 于是"提示不挡交互"。
//   - 显示状态是节点的运行时字段 (tipPopup), 不来自 props: 与 select 的
//     expanded/popup 同类, 脚本只声明 text/placement/delay。
//
// 显示时机: 进入触发区后 delay 毫秒 (缺省 500)。
// 关闭时机: 移出触发区 / 光标离开窗口 / 任何一次鼠标按下 (点击 = 开始交互,
// 提示让路, 与原生控件一致) / Esc 兜底链 (优先级最低, 见 handleKey)。
//
// 计时为什么不用 time.AfterFunc: 泵没有事件、没有 JS 定时器时会睡死在
// WaitEvents 里 (tickCaretBlink 的注释说过同一件事), AfterFunc 里 Post 的
// 任务唤醒不了它。改为把 Pump 的等待预算钳到到期时刻 (见 nextTooltipWake),
// 泵到点自然醒来, processEvents 里的 tickTooltip 完成显示 —— 全程单线程,
// 不引入并发路径。

const (
	tipGap       = 6   // 弹层与触发盒的间距
	tipPadX      = 10  // 弹层水平内边距
	tipPadY      = 5   // 弹层垂直内边距
	tipMaxW      = 260 // 弹层最大宽度 (超宽文本被 DrawText 截断)
	tipWinMargin = 4   // 弹层距窗口边缘的最小留白 (clamp 用)
	tipDelay     = 500 // 缺省显示延迟 (毫秒)
	tipFlipGap   = 8   // 翻转时用的间距 (与 tipGap 同值, 命名独立便于调)
)

// ===== props =====

// tipText 读取提示文本 (text prop)。空串 = 没有可显示的内容, 不计时。
func (n *GuiNode) tipText() string {
	s, _ := n.PropStr("text")
	return s
}

// tipDelay 读取显示延迟 (delay prop, 毫秒)。非法/缺省 500。
func (n *GuiNode) tipDelay() time.Duration {
	if v, ok := n.PropNum("delay"); ok && v >= 0 {
		return time.Duration(v) * time.Millisecond
	}
	return tipDelay * time.Millisecond
}

// tipPlacement 读取弹出方位 (placement prop): top/bottom/left/right, 缺省 bottom。
func (n *GuiNode) tipPlacement() string {
	if p, _ := n.PropStr("placement"); p != "" {
		return p
	}
	return "bottom"
}

// tooltipTrigger 返回触发元素 (唯一流内子节点); 无则 nil。
func (n *GuiNode) tooltipTrigger() *GuiNode {
	for _, c := range n.Children {
		if c.isFlowChild() {
			return c
		}
	}
	return nil
}

// ===== 计时 / 显示 / 隐藏 (全部在 VM 线程执行) =====

// tooltipTrack 按最新的悬停命中节点维护 tooltip 计时: 悬停进了一个新的
// tooltip 触发区就开始计时; 悬停到了别处就隐藏。已计时/已显示的同一个
// tooltip 保持原 deadline (鼠标在触发区内挪动不会重置计时)。
//
// 悬停在弹层上时 target 沿 Parent 链仍能找回 host (弹层挂在 host 下),
// 于是"提示弹出后鼠标盖在弹层上"不会闪断。
func (a *app) tooltipTrack(target *GuiNode) {
	var host *GuiNode
	for p := target; p != nil; p = p.Parent {
		if p.Tag == "tooltip" {
			host = p
			break
		}
	}
	if host == nil || host.tipText() == "" || host.disabledInChain() {
		a.tooltipHide()
		return
	}
	a.mu.Lock()
	same := a.tooltipHost == host
	a.mu.Unlock()
	if same {
		return
	}
	a.tooltipHide()
	a.mu.Lock()
	a.tooltipHost = host
	a.tooltipDeadline = time.Now().Add(host.tipDelay())
	a.mu.Unlock()
}

// tooltipHide 立即隐藏 (关掉已显示的弹层、清掉计时)。没有任何 tooltip
// 活动时是无害的空操作 —— MouseLeave/按下这些高频路径都可以直接调。
func (a *app) tooltipHide() {
	a.mu.Lock()
	host := a.tooltipHost
	a.tooltipHost = nil
	a.tooltipDeadline = time.Time{}
	a.mu.Unlock()
	if host == nil || host.tipPopup == nil {
		return
	}
	popup := host.tipPopup
	host.tipPopup = nil
	// 整帧标脏而不是节点级: 弹层凭空消失的那片区域不属于任何"框变了"
	// 的节点, 与 select 收起同一道理 (见 closeSelect 的注释)。
	disposeNode(popup)
	markFullDirtyFor(host)
}

// tickTooltip 计时到期检查, 由 processEvents 每轮调用 (与 tickCaretBlink
// 同一批次)。到期且仍在计时态就物化弹层并整帧标脏。
func (a *app) tickTooltip() {
	a.mu.Lock()
	host := a.tooltipHost
	deadline := a.tooltipDeadline
	a.mu.Unlock()
	if host == nil || deadline.IsZero() || time.Now().Before(deadline) {
		return
	}
	a.showTooltip(host)
}

// nextTooltipWake 返回全部窗口里最近的 tooltip 计时剩余时长; 没有任何
// 计时在跑时 ok=false。Pump 用它钳制等待预算 (见 render.go) —— 已到期时
// 返回 0, 泵本轮立即醒来, 由 tickTooltip 完成显示。
func nextTooltipWake() (time.Duration, bool) {
	found := false
	var out time.Duration
	appMu.Lock()
	for _, a := range apps {
		a.mu.Lock()
		if a.tooltipHost != nil && !a.tooltipDeadline.IsZero() {
			d := time.Until(a.tooltipDeadline)
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

// showTooltip 物化弹层。从计时态进入显示态 (deadline 清零)。
func (a *app) showTooltip(host *GuiNode) {
	if host.tipText() == "" || host.tipPopup != nil {
		return
	}
	a.mu.Lock()
	// host 可能已经在鼠标离开时被换掉: 只显示仍归属自己的那次计时。
	if a.tooltipHost != host || a.tooltipDeadline.IsZero() {
		a.mu.Unlock()
		return
	}
	a.tooltipDeadline = time.Time{}
	a.mu.Unlock()

	popup := &GuiNode{Tag: "tooltip-popup", Props: map[string]object.Value{}}
	popup.owner = host // 弹层回指宿主: 绘制时要读 text/font (与 select-option.owner 同套路)
	popup.Parent = host
	host.tipPopup = popup
	host.Children = append(host.Children, popup)
	markFullDirtyFor(host)
}

// ===== 布局 =====

// layoutTooltip 布局悬停提示: 触发元素 (唯一流内子节点) 按内容区定位,
// tooltip 自身对布局透明 (intrinsicSize 跟随子节点)。显示中的弹层不走
// 流内分配, 由 positionTooltipPopup 按 placement 显式定位。
func layoutTooltip(n *GuiNode) {
	area := inner(n)
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, ch := c.sizeInArea(area.W, area.H)
		c.Box = Rect{X: area.X, Y: area.Y, W: cw, H: ch}
		layoutNode(c)
	}
	placeAbsoluteIn(n, area)
	if p := n.tipPopup; p != nil {
		positionTooltipPopup(n, p)
	}
}

// positionTooltipPopup 布局期定位弹层: 按 placement 放在触发盒旁边, 放不下
// 时翻到另一侧, 最后 clamp 进窗口。布局每帧都跑, 窗口 resize 后弹层自然跟随
// (与 layoutSelect 的定位同一思路)。
func positionTooltipPopup(host, popup *GuiNode) {
	win := host.windowBox()
	tw, th := tooltipPopupSize(host)
	b := host.Box
	if trig := host.tooltipTrigger(); trig != nil {
		b = trig.Box
	}
	x, y := b.X, b.Y
	switch host.tipPlacement() {
	case "top":
		x, y = b.X+b.W/2-tw/2, b.Y-tipGap-th
	case "left":
		x, y = b.X-tipGap-tw, b.Y+b.H/2-th/2
	case "right":
		x, y = b.X+b.W+tipGap, b.Y+b.H/2-th/2
	default: // bottom
		x, y = b.X+b.W/2-tw/2, b.Y+b.H+tipGap
	}
	// 越界翻转: 缺省方位在窗口边缘放不下时翻到对侧 (clamp 到窗口内是兜底,
	// 翻转才是主要手段 —— clamp 会让提示盖住触发元素本身)。
	switch host.tipPlacement() {
	case "top":
		if y < win.Y+tipWinMargin {
			y = b.Y + b.H + tipFlipGap
		}
	case "bottom":
		if y+th > win.Y+win.H-tipWinMargin {
			y = b.Y - tipFlipGap - th
		}
	case "right":
		if x+tw > win.X+win.W-tipWinMargin {
			x = b.X - tipFlipGap - tw
		}
	case "left":
		if x < win.X+tipWinMargin {
			x = b.X + b.W + tipFlipGap
		}
	}
	// clamp 进窗口: 先上界后下界 —— 弹层比窗口还宽/高时退化成贴左/上边缘,
	// 至少保证可见 (弹层 400 宽塞进 160 窗口时, 两把 clamp 顺序反了会把
	// 提示整个推出窗外)。
	if max := win.X + win.W - tw - tipWinMargin; x > max {
		x = max
	}
	if min := win.X + tipWinMargin; x < min {
		x = min
	}
	if max := win.Y + win.H - th - tipWinMargin; y > max {
		y = max
	}
	if min := win.Y + tipWinMargin; y < min {
		y = min
	}
	popup.Box = Rect{X: x, Y: y, W: tw, H: th}
}

// tooltipPopupSize 计算弹层尺寸: 文本测量 + 内边距, 宽度封顶 tipMaxW。
// 字号跟随触发元素 (弹层挂在 host 下, FontSize 沿 Parent 链继承;
// 想改字号直接在 <tooltip> 上写 font={12})。
func tooltipPopupSize(host *GuiNode) (int, int) {
	tw, th := MeasureText(host.tipText(), host.FontSize())
	tw += 2 * tipPadX
	th += 2 * tipPadY
	if tw > tipMaxW {
		tw = tipMaxW
	}
	if tw < 2*tipPadX {
		tw = 2 * tipPadX
	}
	return tw, th
}

// ===== 绘制 =====

// paintTooltipPopup 画提示弹层: 深色底 + 白字 (主流 tooltip 外观), 单行
// 居中, 超宽由 DrawText 按 maxW 截断。内容是数据 (text prop) 而不是元素树,
// 所以文字在这里直接画, 不物化 #text 子节点 (与 toast 画 message 同理)。
func paintTooltipPopup(img *image.RGBA, n *GuiNode, disabled bool) {
	b := n.Box
	if b.W <= 0 || b.H <= 0 || n.owner == nil {
		return
	}
	FillRect(img, b, tint(colorTooltipFace, disabled))
	msg := n.owner.tipText()
	if msg == "" {
		return
	}
	size := n.FontSize()
	_, th := MeasureText(msg, size)
	DrawText(img, img.Bounds(), msg, b.X+tipPadX, b.Y+(b.H-th)/2, size,
		tint(colorTooltipText, disabled), b.W-2*tipPadX)
}
