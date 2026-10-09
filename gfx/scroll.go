package gfx

import (
	"image"
	"strconv"

	"github.com/14752222/Gox/object"
)

// scroll 滚动容器 (P2-5; 横向滚动与滚动条拖拽 rSkhXA / RELEASE_NOTES 已知问题 2)。
//
// 结构约定:
//   - 视口 = 容器内容区 (inner) 扣掉滚动条占位: 内容超高让出右侧竖向轨道,
//     内容超宽让出底部横向轨道 (判定规则见 scrollAxes)。子节点被整体平移
//     (-offsetX, -offsetY)。
//   - **子节点的 Box 直接落在"屏幕坐标"上** (布局时已经减去偏移) —— 而不是
//     任务书原文说的"Box 不变 + 绘制变换"。这么做是为了让滚动只发生在
//     一个地方: 命中测试、脏矩形比对 (diffRects 比较 Box 的 PrevBox)、
//     光标定位都不需要再单独处理偏移。代价是每帧布局要重排一次子节点
//     (Layout 本来就每帧跑, 增量可忽略)。
//   - 裁剪: 进入 scroll 子树时把可绘制范围收缩到视口 (见 raster.go 的
//     drawNode), 于是"内容溢出容器"不会画到外面。
//   - 滚动条: 右侧/底部 8px 轨道 + 滑块 (边长 = 视口/内容 比例, 位置 = 偏移
//     比例, 最短 scrollMinThumb)。滑块可拖拽 (beginScrollDrag → scrollDragTo,
//     与 slider 共用 render.go 的 dragTarget 捕获基建)。
//
// offsetX/offsetY/contentW/contentH 是节点的运行时字段 (与 hovered/expanded
// 同类): 来自用户滚动与布局测量结果, 不是 props。

const (
	scrollTrackW   = 8  // 滚动条轨道厚度 (右侧竖向与底部横向共用)
	scrollMinThumb = 24 // 滑块最小边长 (内容极长时也要能抓住)
	scrollDefH     = 200

	// wheelDeltaUnit 是 Windows 一格滚轮的原始增量 (WHEEL_DELTA)。
	// scrollNotch 是一格滚轮对应的内容位移像素 (约两行列表项)。
	wheelDeltaUnit = 120
	scrollNotch    = 60
)

// scrollInChain 从 n 起沿祖先链找第一个 scroll (滚轮分发用: 光标可能落在
// 滚动区域里的任意子节点上)。
func scrollInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "scroll" {
			return p
		}
	}
	return nil
}

// scrollAxes 判定两个方向的滚动条是否出现。
//
// 竖向滚动条看"内容高 > 内容区高"; 横向滚动条看"内容宽 > 扣掉竖向轨道后的
// 视口宽" —— 与浏览器一致: 竖向滚动条先出现, 把可用宽度压窄后内容再溢出
// 才轮到横向。layoutScroll 的测量与 scrollViewport 的视口收缩都用这一份
// 判定, 保证"画出来的轨道"与"点得到/画得出的视口"永远同一套。
func (n *GuiNode) scrollAxes() (v, h bool) {
	area := inner(n)
	v = n.contentH > area.H
	w := area.W
	if v {
		w -= scrollTrackW
	}
	h = n.contentW > w
	return v, h
}

// scrollViewport 返回内容区里真正可见的那部分 (按 scrollAxes 扣掉轨道占位)。
func (n *GuiNode) scrollViewport() Rect {
	area := inner(n)
	v, h := n.scrollAxes()
	if v {
		area.W -= scrollTrackW
		if area.W < 0 {
			area.W = 0
		}
	}
	if h {
		area.H -= scrollTrackW
		if area.H < 0 {
			area.H = 0
		}
	}
	return area
}

// scrollMaxOffset 是 offsetY 的上限 (内容高 - 视口高); 内容不足一屏时为 0。
func (n *GuiNode) scrollMaxOffset() int {
	max := n.contentH - n.scrollViewport().H
	if max < 0 {
		max = 0
	}
	return max
}

// scrollMaxOffsetX 是 offsetX 的上限 (内容宽 - 视口宽); 内容不超宽时为 0。
func (n *GuiNode) scrollMaxOffsetX() int {
	max := n.contentW - n.scrollViewport().W
	if max < 0 {
		max = 0
	}
	return max
}

// notifyScroll 在一次**用户交互**造成的滚动之后派发 onScroll({offsetX, offsetY})。
//
// 刻意只挂在 scrollBy / scrollDragTo 上, **不挂** applyScrollCommand: 后者跑在
// 布局期, 在那儿回调脚本等于允许"布局还没排完就重建子树" (子节点正在被就地量取,
// 脚本却在重排列表)。DOM 里脚本触发的滚动同样会发 scroll 事件, 这里是刻意的取舍:
// 脚本自己发起的跳转它自己是知道的, 不差这条回调。
func (n *GuiNode) notifyScroll() {
	a := appOfNode(n)
	if a == nil {
		return
	}
	h := n.PropHandler("onScroll")
	if h == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("offsetX", object.NewNumber(float64(n.offsetX)))
	arg.SetProperty("offsetY", object.NewNumber(float64(n.offsetY)))
	a.callHandler(n, "onScroll", arg)
}

// scrollBy 按给定像素滚动 (正数 = 内容上移/左移, 即向右/向下滚)。返回值表示
// 偏移是否**任一方向**真的改变了 —— 两个方向都已到边界时返回 false, 调用方
// 据此决定要不要把滚轮事件继续往外传 (与 DOM 的滚动链一致)。
func (n *GuiNode) scrollBy(dx, dy int) bool {
	moved := false
	if dy != 0 {
		if v := clampScrollOffset(n.offsetY+dy, n.scrollMaxOffset()); v != n.offsetY {
			n.offsetY = v
			moved = true
		}
	}
	if dx != 0 {
		if v := clampScrollOffset(n.offsetX+dx, n.scrollMaxOffsetX()); v != n.offsetX {
			n.offsetX = v
			moved = true
		}
	}
	if moved {
		markNodeDirty(n)
		// 虚拟化长列表 (vlist) 的窗口**不在这里**抬版本号。
		//
		// 曾经在这里 bump 过一次, 结果每次滚动都重算两遍窗口 (这里一遍、布局里的
		// vlistPrepare 再一遍), 每遍都要重建/复用全部行节点 —— 实测 20 帧滚动触发
		// 40 次重算, 虚拟化反而比全量更慢 (2026-10-01)。布局是唯一知道"视口高、
		// 轨道、钳位后偏移"的地方, 由它调 vlistPrepare 决定窗口即可; 这里只标脏
		// (要重绘), 窗口的重算交给紧随其后的 Layout。
		//
		// 用户手势导致的偏移变化还要回报脚本: onScroll({offsetX, offsetY})。
		// 只挂在手势路径上, 布局期写 prop 那条路不派发 (见 applyScrollCommand)。
		n.notifyScroll()
	}
	return moved
}

// clampScrollOffset 把候选偏移钳进 [0, max]。max 为负 (内容不足一屏) 时
// 视为 0 —— 不修的话 "钳位" 会把偏移推成负数, 后续滚动判定全部失真。
func clampScrollOffset(v, max int) int {
	if max < 0 {
		max = 0
	}
	if v < 0 {
		return 0
	}
	if v > max {
		return max
	}
	return v
}

// scrollThumb 返回竖向滑块的矩形 (不需要滚动条时 ok=false)。
// 比例与行程按**视口** (扣轨后) 算; 位置 X 钉在 inner 最右一条轨道上
// (轨道与视口并排, 不占视口 —— 见 paintScroll)。
func (n *GuiNode) scrollThumb() (Rect, bool) {
	area := inner(n)
	vp := n.scrollViewport()
	max := n.scrollMaxOffset()
	if max <= 0 || vp.H <= 0 || area.W < scrollTrackW {
		return Rect{}, false
	}
	// max > 0 蕴含 contentH > vp.H >= 1, 除法安全。
	th := vp.H * vp.H / n.contentH
	if th < scrollMinThumb {
		th = scrollMinThumb
	}
	if th > vp.H {
		th = vp.H
	}
	pos := 0
	if span := vp.H - th; span > 0 {
		pos = span * n.offsetY / max
	}
	return Rect{
		X: area.X + area.W - scrollTrackW + 1, Y: area.Y + pos,
		W: scrollTrackW - 2, H: th,
	}, true
}

// scrollThumbX 返回横向滑块的矩形 (不需要滚动条时 ok=false)。
// 比例与行程按**视口**算; 位置 Y 钉在 inner 最下一条轨道上。
func (n *GuiNode) scrollThumbX() (Rect, bool) {
	area := inner(n)
	vp := n.scrollViewport()
	max := n.scrollMaxOffsetX()
	if max <= 0 || vp.W <= 0 || area.H < scrollTrackW {
		return Rect{}, false
	}
	tw := vp.W * vp.W / n.contentW
	if tw < scrollMinThumb {
		tw = scrollMinThumb
	}
	if tw > vp.W {
		tw = vp.W
	}
	pos := 0
	if span := vp.W - tw; span > 0 {
		pos = span * n.offsetX / max
	}
	return Rect{
		X: area.X + pos, Y: area.Y + area.H - scrollTrackW + 1,
		W: tw, H: scrollTrackW - 2,
	}, true
}

// scrollThumbAt 在 root 子树里找滑块矩形覆盖 (x,y) 的 scroll 节点, 找不到
// 返回 nil。多个滑块重叠时取**最深**的 (内层优先, 与命中测试的深优先一致)。
//
// 鼠标按下走这里而不是常规命中测试: hittest.go 把滚动条占位区从"可命中的
// 子内容"里排除了, HitTestDeep 对滑块区域返回 nil, 常规路径抓不到滑块。
func scrollThumbAt(root *GuiNode, x, y int) *GuiNode {
	if root == nil {
		return nil
	}
	var hit *GuiNode
	var walk func(n *GuiNode)
	walk = func(n *GuiNode) {
		if n == nil {
			return
		}
		if n.Tag == "scroll" && !n.disabledInChain() {
			if th, ok := n.scrollThumb(); ok && th.Contains(x, y) {
				hit = n
			}
			if th, ok := n.scrollThumbX(); ok && th.Contains(x, y) {
				hit = n
			}
		}
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)
	return hit
}

// scrollDragTo 把拖拽中的鼠标坐标应用到偏移上 (拖拽 = 捕获期间 MouseMove
// 全部喂给被拖的 scroll, 见 render.go 的 dragMove)。
//
// 换算基于 beginScrollDrag 记下的 grab 快照: 鼠标位移 × (可滚范围 / 滑块
// 可行行程)。比例用**当下**的 max 与行程 —— 拖拽期间内容被脚本改动时也能
// 跟着最新布局走; 起点 offsetY 用快照值, 避免滑块位置反馈造成的"越拖越快"。
func (n *GuiNode) scrollDragTo(x, y int) {
	changed := false
	if max := n.scrollMaxOffset(); max > 0 {
		if _, ok := n.scrollThumb(); ok {
			span := n.scrollViewport().H - n.scrollThumbH()
			if span > 0 {
				v := clampScrollOffset(n.scrollGrabOffY+(y-n.scrollGrabY)*max/span, max)
				if v != n.offsetY {
					n.offsetY = v
					changed = true
				}
			}
		}
	}
	if max := n.scrollMaxOffsetX(); max > 0 {
		if _, ok := n.scrollThumbX(); ok {
			span := n.scrollViewport().W - n.scrollThumbW()
			if span > 0 {
				v := clampScrollOffset(n.scrollGrabOffX+(x-n.scrollGrabX)*max/span, max)
				if v != n.offsetX {
					n.offsetX = v
					changed = true
				}
			}
		}
	}
	if changed {
		markNodeDirty(n)
		// 与 scrollBy 同一条纪律: 窗口重算交给紧随其后的布局 (vlistPrepare),
		// 这里只标脏。两处都 bump 的话每次拖动会重算两遍窗口 (2026-10-01 实测)。
		n.notifyScroll()
	}
}

// scrollThumbH / scrollThumbW 取滑块边长 (拖拽行程换算用; 滑块不存在时 0)。
func (n *GuiNode) scrollThumbH() int {
	if th, ok := n.scrollThumb(); ok {
		return th.H
	}
	return 0
}

func (n *GuiNode) scrollThumbW() int {
	if th, ok := n.scrollThumbX(); ok {
		return th.W
	}
	return 0
}

// ===== 滚动位置写入口 (看板 r846P0) =====
//
// <scroll> 此前只有**读**能力: 偏移由滚轮与滑块拖拽维护, 脚本没有任何把它写
// 进去的入口 ⇒ "跳到底部 / 跳到指定行" 做不出来 (日志查看器的 tail -f、聊天
// 消息流、表格定位都是同一个诉求)。本次补两个受控 prop:
//
//	<scroll scrollTop={atBottom ? "bottom" : 0} scrollLeft={0}>…</scroll>
//
// 取值两种形状:
//   - 数字: 绝对像素, 随后按 [0, max] 钳位 —— 所以写一个很大的数就是"跳到底部";
//   - 字符串别名: "top" (0) / "bottom" (内容末端); 横向是 "start" / "end"。
//
// **为什么写入口做在这里 (布局期) 而不是让应用自己算**: contentH 与视口扣除
// 轨道的宽度是布局才知道的量, 应用拿不到 max, 自己算必然与滚动条/钳位脱节;
// vlist 下还牵扯窗口物化 —— 只有布局知道"可见区间在哪"。写在 layoutScroll 里,
// scrollBy / scrollDragTo / vlistPrepare 三条既有路径一条都不用改。
//
// **为什么是"目标变了才施加"而不是每帧强制同步**: offsetY 同时也是**用户交互**
// 在写的 runtime 状态。每帧把 prop 灌进 offsetY, 用户刚滚开就被拽回, 表现是
// "滚不动"。改成只在目标变化时跳一次, 两者得以共存 (详见 applyScrollCommand)。
//
// **关于反向通道 onScroll**: 用户滚动会派发 onScroll({offsetX, offsetY})
// (见 notifyScroll), 于是"用户是不是已经贴底"这类判断做得出来; 而**脚本发起的
// 跳转不重复派发** —— applyScrollCommand 跑在布局期, 在那里回调脚本等于允许
// "布局还没排完就重建子树"。DOM 里脚本触发的滚动也发 scroll 事件, 这里是刻意
// 的取舍: 脚本自己发起的跳转它自己是知道的。
//
// 要把"仅当用户贴底时才跟随最新"做成 switch, 现在两块积木都齐了: 用
// scrollTop={"bottom"} 的粘性语义 (内容变长才重新贴底) 配合 onScroll 判断。

// applyScrollCommand 在每次布局、内容尺寸已量完之后施加脚本给出的滚动目标。
//
// 位置是**死要求**:
//   - 必须在 contentH/contentW 写回之后 —— "bottom"/"end" 要按 scrollMaxOffset 算;
//   - 必须在钳位与 vlist 窗口重算之前 —— 跳过去之后, 钳位、窗口
//     (layoutScroll 里 `plan.offsetY != n.offsetY` 那个判据) 都按新偏移走。
func (n *GuiNode) applyScrollCommand() {
	moved := false
	if v, ok := n.Props["scrollTop"]; ok {
		target, key, ok := n.scrollTargetY(v)
		if ok && key != n.scrollCmdY && target != n.offsetY {
			n.offsetY = target
			n.scrollCmdY = key
			moved = true
		}
	}
	if v, ok := n.Props["scrollLeft"]; ok {
		target, key, ok := n.scrollTargetX(v)
		if ok && key != n.scrollCmdX && target != n.offsetX {
			n.offsetX = target
			n.scrollCmdX = key
			moved = true
		}
	}
	if moved {
		markNodeDirty(n)
	}
}

// scrollTargetY 解出一个纵向滚动目标与其去重键; 值不认识时 ok=false。
//
// 去重键的两种取法是本设计的要点:
//   - **数字 → 按原值去重**: "一次性跳", 只有值真的变了才再跳 (写常量 200
//     就是"跳到 200 一次", 之后用户爱滚哪滚哪);
//   - **"bottom" → 按解析后的像素去重**: 内容变长 ⇒ max 变大 ⇒ 目标变化 ⇒
//     自动重新贴底。这正是日志流要的"跟随最新": 用户往上翻期间内容没变就
//     不打扰, 新行一来把视口带回末端。
//   - "top" 按原值去重 (它在意图上就是"回到顶部", 不该有粘性)。
func (n *GuiNode) scrollTargetY(v object.Value) (target int, key string, ok bool) {
	switch t := v.(type) {
	case *object.Number:
		tv := clampScrollOffset(int(t.Value), n.scrollMaxOffset())
		return tv, numKey(tv), true
	case *object.String:
		switch t.Value {
		case "top":
			return 0, "top", true
		case "bottom":
			max := n.scrollMaxOffset()
			return max, "bottom:" + numKey(max), true
		}
	}
	return 0, "", false
}

// scrollTargetX 是横向版本 (规则与 scrollTargetY 对称: "start" 一次性、
// "end" 随内容变宽粘性贴右)。
func (n *GuiNode) scrollTargetX(v object.Value) (target int, key string, ok bool) {
	switch t := v.(type) {
	case *object.Number:
		tv := clampScrollOffset(int(t.Value), n.scrollMaxOffsetX())
		return tv, numKey(tv), true
	case *object.String:
		switch t.Value {
		case "start":
			return 0, "start", true
		case "end":
			max := n.scrollMaxOffsetX()
			return max, "end:" + numKey(max), true
		}
	}
	return 0, "", false
}

// numKey 把目标变成去重键。取整有意以**整数像素**为粒度: 200 与 200.4 钳位后
// 落在同一个像素, 不该被判成两次跳转。
func numKey(i int) string { return strconv.Itoa(i) }

// ===== 两遍布局 =====

// layoutScroll 布局滚动容器。
//
// 两遍布局: 第一遍按完整视口堆叠量出内容总高, 收窄后 (出现竖向滚动条) 再排
// 一次, 最后按最终偏移摆放 (子节点 Box 即屏幕坐标)。
//
// **虚拟化 (vlist)**: 开窗口化的容器在**第一遍之前**先让列表把可见窗口备好
// (vlistPrepare) —— 内容总高必须是 N*itemH, 否则量到的只是"少数几行的高度",
// 滚动条长度与 offsetY 钳位会全错 (见 vlist.go 的文件头)。
func layoutScroll(n *GuiNode) {
	area := inner(n)
	if area.W <= 0 || area.H <= 0 {
		n.contentH = 0
		n.contentW = 0
		n.offsetX = 0
		n.offsetY = 0
		placeAbsoluteIn(n, area)
		return
	}

	// 虚拟化 (vlist) 的三遍布局。窗口化的容器比普通滚动容器**多一遍**,
	// 而且顺序是死的要求 (错一步就是"窗口不生效"或"总高变成一屏"):
	//
	//	①全量算窗口 → ②写窗口 + 唤醒列表 effect → ③量内容 / 定偏移 / 摆放
	//
	// 判据 (有没有开 vlist / itemHeight 合不合法) 放在这里而不是先调
	// vlistPrepare: 后者是**布局决策**的前置, 它必须知道内容宽的收缩结果才能
	// 算出正确的可见行数 (见下)。所以先规划 → 再按需走三遍, 不按需就两遍。
	plan, vlistOK := n.scrollVlistPlan(area)
	if vlistOK {
		// ②4 写窗口 + 必要时唤醒列表 effect 重算。
		if app := appOfNode(n); app != nil {
			if !app.vlistPrepare(n, plan) {
				vlistOK = false // 列表侧没接管 (没找到 each / 状态缺失)
			}
		} else {
			vlistOK = false
		}
	}

	// 内容高。
	//
	// **窗口化时绝不能量内容**: 量出来的是"当前只物化了十几行"的高度, 总高会
	// 塌成一屏 —— 滚动条长度、offsetY 钳位、垫片高度全部跟着错, 而且不报错。
	// 总高在这条路上是**已知量** (total*itemH), 直接写。
	//
	// 普通容器照旧量 (第一遍), 此时若出现竖向滚动条就收窄再量一次。
	contentH := 0
	contentW := 0
	if vlistOK {
		contentH = plan.contentH
		contentW = plan.contentW
	} else {
		contentH = layoutContentColumn(n, area.X, area.Y, area.W)
		contentW = flowContentWidth(n)
		v, h := n.scrollAxesWith(contentH, contentW)
		if v || h {
			w, hgt := area.W, area.H
			if v {
				w -= scrollTrackW
				if w < 0 {
					w = 0
				}
			}
			if h {
				hgt -= scrollTrackW
				if hgt < 0 {
					hgt = 0
				}
			}
			if w != area.W && contentH > hgt {
				contentH = layoutContentColumn(n, area.X, area.Y, w)
			}
		}
	}

	n.contentH = contentH
	n.contentW = contentW

	// 滚动位置**写入口** (看板 r846P0): 必须落在"内容尺寸已量完"之后
	// (解析 "bottom" 要用 scrollMaxOffset)、"钳位与窗口重算"之前 (新偏移要
	// 参与后续两步)。详见 applyScrollCommand 的说明。
	n.applyScrollCommand()

	// 钳位偏移 (内容变短/变窄后旧的偏移可能越界)
	n.offsetY = clampScrollOffset(n.offsetY, n.scrollMaxOffset())
	if n.offsetY < 0 {
		n.offsetY = 0
	}
	n.offsetX = clampScrollOffset(n.offsetX, n.scrollMaxOffsetX())
	if n.offsetX < 0 {
		n.offsetX = 0
	}

	// ③ 摆放。窗口化要在**最终偏移**下再准备一次窗口 —— 钳位可能已经改变了
	// 偏移 (滚过底/顶), 那时窗口得跟着改, 否则可见区摆成上一轮的区间。
	if vlistOK && plan.offsetY != n.offsetY {
		if app := appOfNode(n); app != nil {
			if p2, ok2 := n.scrollVlistPlanAt(area, n.offsetY); ok2 {
				plan = p2
				app.vlistPrepare(n, plan)
			}
		}
	}
	if vlistOK {
		layoutScrollWindowed(n, area, n.scrollViewportFinal(area.W, area.H))
	} else if n.offsetX != 0 || n.offsetY != 0 {
		w := n.scrollViewportFinal(area.W, area.H).W
		layoutContentColumn(n, area.X-n.offsetX, area.Y-n.offsetY, w)
	}
	abs := area
	abs.X -= n.offsetX
	abs.Y -= n.offsetY
	placeAbsoluteIn(n, abs)
}

// scrollAxesWith 用给定的内容尺寸跑 scrollAxes 的判定 (布局中内容尺寸还是
// 局部变量, 还没写回字段 —— 判定逻辑与 scrollAxes 保持一致, 见那边的说明)。
func (n *GuiNode) scrollAxesWith(contentH, contentW int) (v, h bool) {
	area := inner(n)
	v = contentH > area.H
	w := area.W
	if v {
		w -= scrollTrackW
	}
	h = contentW > w
	return v, h
}

// flowContentWidth 测内容的固有总宽 (相对内容区左沿 x0 的最大右边缘)。
// 取**固有宽** (intrinsicSize) 而不是布局后的 Box.W: 默认 stretch 的子节点
// 铺满视口是"跟随容器", 不构成横向溢出 —— 与浏览器一致, 块级子元素默认
// 不触发横向滚动条, 只有显式更宽 (或文本等固有内容超宽) 才滚。
// 绝对定位/弹层子节点不算 (与 contentH 只统计流内子是同一约定 —— 它们
// 不该撑出横向滚动条, 溢出部分由 z 序与裁剪处理)。align 的 center/end 偏移
// 不参与: 那是把不超宽的内容在视口内摆放, 不是溢出。
func flowContentWidth(n *GuiNode) int {
	right := 0
	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue
		}
		cw, _ := c.intrinsicSize()
		mg := marginOf(c)
		if r := cw + 2*mg; r > right {
			right = r
		}
	}
	return right
}

// layoutContentColumn 把流内子节点在 (x, y) 处纵向堆叠, 宽度给定、高度不限,
// 返回内容总高 (含 gap 与 margin)。交叉轴按 alignItems 处理: 默认 stretch
// 铺满 w —— 滚动列表的行通常要占满视口宽度。
// 横向滚动时调用方传 x = 视口左沿 - offsetX: 子节点 Box 一次落到屏幕坐标。
func layoutContentColumn(n *GuiNode, x, y, w int) int {
	g := n.gapOf()
	align := n.alignItems()
	pos := 0
	first := true

	for _, c := range n.Children {
		if !c.isFlowChild() {
			continue // 绝对定位/弹层: 不占内容高度 (由 placeAbsoluteIn 摆放)
		}
		cw, ch := c.intrinsicSize()
		mg := marginOf(c)
		if !first {
			pos += g
		}
		first = false
		pos += mg

		cross := cw
		if align == "stretch" && !c.hasExplicitCross(false) &&
			(cross == 0 || c.stretchesCross()) {
			cross = w - 2*mg
		}
		if cross < 0 {
			cross = 0
		}
		off := 0
		switch align {
		case "center":
			off = (w - cross - 2*mg) / 2
		case "end":
			off = w - cross - 2*mg
		}
		if off < 0 {
			off = 0
		}

		// 文本块: 盒宽定下来才能知道折几行 (见 textblock.go)
		ch = c.blockHeight(cross, ch)

		c.Box = Rect{X: x + mg + off, Y: y + pos, W: cross, H: ch}
		layoutNode(c)
		pos += ch + mg
	}
	return pos
}

// paintScroll 画滚动条 (右侧竖向 + 底部横向轨道与滑块)。容器本身不画底色,
// 按需读 background/border (与通用盒子一致)。
// 轨道钉在 inner 的最右/最下边缘 (与视口并排, 不占视口); 两个轨道同现时
// 横向轨道画到竖向轨道左沿为止, 交叉角归竖向轨道。
func paintScroll(img *image.RGBA, n *GuiNode, disabled bool) {
	if bg, ok := n.backgroundFor(); ok {
		FillRect(img, n.Box, tint(bg, disabled))
	}
	if bd, ok := n.borderFor(); ok {
		StrokeRect(img, n.Box, tint(bd, disabled))
	}
	area := inner(n)
	if thumb, ok := n.scrollThumb(); ok {
		FillRect(img, Rect{
			X: area.X + area.W - scrollTrackW, Y: area.Y,
			W: scrollTrackW, H: area.H,
		}, tint(colorScrollTrack, disabled))
		FillRect(img, thumb, tint(colorScrollThumb, disabled))
	}
	if thumb, ok := n.scrollThumbX(); ok {
		trackW := area.W
		if _, vok := n.scrollThumb(); vok {
			trackW -= scrollTrackW
		}
		if trackW > 0 {
			FillRect(img, Rect{
				X: area.X, Y: area.Y + area.H - scrollTrackW,
				W: trackW, H: scrollTrackW,
			}, tint(colorScrollTrack, disabled))
		}
		FillRect(img, thumb, tint(colorScrollThumb, disabled))
	}
}
