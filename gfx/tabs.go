package gfx

import (
	"fmt"
	"image"

	"github.com/14752222/Gox/object"
)

// tabs 选项卡 (S4 组件库二期) —— limits.md「未实现的组件」清单的 tabs 行落地。
//
// 用法:
//
//	<tabs value={tabSignal} onChange={(e) => console.log(e.index, e.title)}>
//	  <tab title="文件">...</tab>
//	  <tab title="编辑">...</tab>
//	</tabs>
//
// 结构约定:
//   - tabs 是纵向两段容器: 顶部标签条 (title 文本 + 激活下划线, 自绘) +
//     内容区 (**激活页**的子树, 由通用机制布局绘制)。
//   - tab 是一页: title prop 是标签条文字, 子节点按 column 语义竖排
//     (直接塞多个元素也竖排, 不必再包一层 column)。
//   - **keep-alive 语义**: 全部页的节点都留在树上 (signal 状态保留),
//     非激活页只是不布局、不绘制、不命中 —— 切走再切回来, 输入框里打的
//     字、滚动位置都还在。这与路由 keepAlive / show 指令同一套哲学;
//     想要"每次切来都全新构建"请在 value 驱动下用条件渲染自己写。
//
// 受控与非受控:
//   - value prop 存在 ⇒ 受控: 激活页永远读 value (signal 经 gx/solid 接线
//     写回 Props, 与 checkbox 的 checked 同一套); 点击标签条**不改内部状态**,
//     只派发 onChange, 等脚本把新值写回 signal —— 脚本不回写就是切不动
//     (受控的定义, 不是 bug)。
//   - 没有 value prop ⇒ 非受控: 内部 activeIdx 维护, 点击直接切换。
//   - onChange 参数: { index: number, title: string }。
//
// v1 边界: 键盘方向键切页交给 T10 无障碍的焦点系统统一做; 动态增删页
// (each 生成 tab) 未支持 —— pages 只认直接子节点里的 <tab>。

const (
	tabStripPadY = 5  // 标签条上下内边距 (文字到条缘)
	tabItemPadX  = 12 // 单个标签的左右内边距
	tabGap       = 4  // 标签之间的间距
	tabUnderline = 2  // 激活下划线厚度
)

// ===== props 与状态读取 =====

// tabsPages 返回页列表: 直接子节点里 Tag=="tab" 的流内节点。
func (n *GuiNode) tabsPages() []*GuiNode {
	var out []*GuiNode
	for _, c := range n.Children {
		if c.Tag == "tab" && c.isFlowChild() {
			out = append(out, c)
		}
	}
	return out
}

// tabTitle 读取页标题 (title prop); 空串给一个兜底名, 保证标签条可点。
func (n *GuiNode) tabTitle(idx int) string {
	if s, _ := n.PropStr("title"); s != "" {
		return s
	}
	return fmt.Sprintf("Tab %d", idx+1)
}

// tabsControlled 报告是否受控: 有 value prop 就算 (数字 = 显式锁定,
// 函数 = solid 接线中的 signal —— 都不归内部状态管)。
func (n *GuiNode) tabsControlled() bool {
	_, ok := n.Props["value"]
	return ok
}

// tabsActiveIndex 返回当前激活页下标 (已按页数钳位)。受控读 value,
// 非受控读内部状态。value 是尚未接线的函数对象时 (纯 Go 嵌入/接线前的
// 首帧) 落回内部状态 —— 与 "读到 0" 的初始帧一致。
func (n *GuiNode) tabsActiveIndex() int {
	pages := n.tabsPages()
	if v, ok := n.PropNum("value"); ok {
		i := int(v)
		if i < 0 {
			i = 0
		}
		if len(pages) > 0 && i > len(pages)-1 {
			i = len(pages) - 1
		}
		return i
	}
	i := n.tabsActive
	if i < 0 {
		i = 0
	}
	if len(pages) > 0 && i > len(pages)-1 {
		i = len(pages) - 1
	}
	return i
}

// tabIsShown 报告本页是否是宿主 tabs 的激活页 (宿主不是 tabs 时视为显示
// —— <tab> 单独使用没有意义, 但不该把子树整体吞掉)。
func (n *GuiNode) tabIsShown() bool {
	p := n.Parent
	if p == nil || p.Tag != "tabs" {
		return true
	}
	return p.tabsActiveIndex() == n.optIndex
}

// tabStripAt 返回 (x,y) 命中的标签下标; 不在条上返回 -1。
func (n *GuiNode) tabStripAt(x, y int) int {
	for i, r := range n.tabStrip {
		if r.Contains(x, y) {
			return i
		}
	}
	return -1
}

// tabsInChain 从 n 起沿祖先链找第一个 tabs。
func tabsInChain(n *GuiNode) *GuiNode {
	for p := n; p != nil; p = p.Parent {
		if p.Tag == "tabs" {
			return p
		}
	}
	return nil
}

// ===== 切换 (VM 线程执行) =====

// tabsSwitch 切到第 idx 页: 非受控改内部状态并整帧标脏 (旧内容区那片像素
// 属于"消失的框", 与 tooltip 收起同一道理, 局部脏矩形表达不了); 受控不动
// 内部状态 —— 值归 signal。两种模式都派发 onChange。
func (a *app) tabsSwitch(tb *GuiNode, idx int) {
	pages := tb.tabsPages()
	if idx < 0 || idx >= len(pages) {
		return
	}
	if !tb.tabsControlled() && tb.tabsActive != idx {
		tb.tabsActive = idx
		markFullDirtyFor(tb)
	}
	if tb.PropHandler("onChange") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("index", object.NewNumber(float64(idx)))
	arg.SetProperty("title", object.NewString(pages[idx].tabTitle(idx)))
	a.callHandler(tb, "onChange", arg)
}

// ===== 布局 =====

// tabStripHeight 返回标签条高度 (跟随 tabs 的字号, 沿父链继承)。
func (n *GuiNode) tabStripHeight() int {
	return lineHeight(n.FontSize()) + 2*tabStripPadY
}

// layoutTabs 布局选项卡: 先按 title 测量摆标签条 (几何记进 tabStrip,
// 绘制与命中共用同一份), 再把激活页放进条下方的内容区。
// 非激活页 Box 清零 —— 命中测试靠 Contains 直接跳过; 绘制与子树 Box
// 残留由 drawNode/hitNode 的 tab 特判兜底 (切走的页子树 Box 还留着
// 上一帧的值, 只清页 Box 不够)。
func layoutTabs(n *GuiNode) {
	area := inner(n)
	pages := n.tabsPages()
	active := n.tabsActiveIndex()

	// 标签条: 逐项测量横排, 几何记进 tabStrip (窗口坐标)
	fontSize := n.FontSize()
	x := area.X
	if cap(n.tabStrip) > 0 {
		n.tabStrip = n.tabStrip[:0]
	}
	for i, pg := range pages {
		tw, _ := MeasureText(pg.tabTitle(i), fontSize)
		w := tw + 2*tabItemPadX
		n.tabStrip = append(n.tabStrip, Rect{X: x, Y: area.Y, W: w, H: n.tabStripHeight()})
		x += w + tabGap
		pg.optIndex = i
	}

	// 内容区: 条下方。激活页占满, 非激活页 Box 清零 (keep-alive: 节点留树)
	content := Rect{X: area.X, Y: area.Y + n.tabStripHeight(), W: area.W, H: area.H - n.tabStripHeight()}
	for _, pg := range pages {
		if pg.optIndex == active {
			cw, ch := pg.sizeInArea(content.W, content.H)
			pg.Box = Rect{X: content.X, Y: content.Y, W: cw, H: ch}
			layoutNode(pg)
		} else {
			pg.Box = Rect{}
		}
	}
	placeAbsoluteIn(n, area)
}

// tabsStripSize 返回标签条的自然尺寸 (固有尺寸与绘制都用得到)。
func (n *GuiNode) tabsStripSize() (w, h int) {
	h = n.tabStripHeight()
	fontSize := n.FontSize()
	for i, pg := range n.tabsPages() {
		tw, _ := MeasureText(pg.tabTitle(i), fontSize)
		w += tw + 2*tabItemPadX + tabGap
	}
	if w > 0 {
		w -= tabGap
	}
	return w, h
}

// ===== 绘制 =====

// paintTabs 画标签条: 激活项文字用 accent 色并压 2px 下划线, 非激活用
// placeholder 灰。内容区的页子树由 drawNode 的常规递归绘制 (非激活页在
// case "tab" 里被跳过)。条底部压一条 1px 分隔线, 让标签条和内容区有视觉分界。
func paintTabs(img *image.RGBA, n *GuiNode, disabled bool) {
	pages := n.tabsPages()
	active := n.tabsActiveIndex()
	for i, r := range n.tabStrip {
		if i >= len(pages) {
			break
		}
		title := pages[i].tabTitle(i)
		size := n.FontSize()
		tw, th := MeasureText(title, size)
		color := colorPlaceholder
		if i == active {
			color = colorAccent
		}
		DrawText(img, img.Bounds(), title, r.X+tabItemPadX, r.Y+(r.H-th)/2, size,
			tint(color, disabled), r.W-2*tabItemPadX)
		if i == active && !disabled {
			FillRect(img, Rect{X: r.X + tabItemPadX, Y: r.Y + r.H - tabUnderline, W: tw, H: tabUnderline}, colorAccent)
		}
	}
	// 条底分隔线: 只画到内容区宽度 (不是整盒 —— 右侧留白不属于条)
	if area := inner(n); area.H > n.tabStripHeight() {
		FillRect(img, Rect{X: area.X, Y: area.Y + n.tabStripHeight(), W: area.W, H: 1}, colorTrack)
	}
}
