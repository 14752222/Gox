package gfx

// 光标形状 (§四 窗口/系统缺口)。
//
// 两种来源, 优先级从高到低:
//
//  1. **窗口级覆盖** `w.setCursor("wait")` —— 忙等/加载中这类"整窗状态"
//     用它最直接, 传 null / 空串清掉 (见 window.go 的 SetCursor)。
//  2. **节点上的 `cursor` prop** —— 鼠标底下那个节点说了算; 自己没写就沿
//     祖先链找最近一个写了 `cursor` 的祖先; 整条链都没有才用**标签缺省表**
//     (输入框给 I 形光标、按钮给手型)。
//
// ## 为什么缺省表要有内容, 而不是"不写就没形状"
//
// 桌面用户的肌肉记忆是"悬停在可点区域时手型光标"。内核已经知道哪些标签
// 是交互件 (`hoverable()` 那张表), 顺手把它们映射成手型是零成本的确定性
// 收益; 反过来, 让每个脚本都手写 `cursor="pointer"` 才是漏配的来源。
//
// 缺省表**只给"明确是交互件"的标签**, 不给容器: 给 `column` 配手型会让
// 整窗变手型, 那是灾难。脚本仍可用 prop 覆盖任何节点的光标。
//
// ## 形状名取 CSS 的值域
//
// 不用自造名 (如 "hand" / "ibeam"): CSS 的 `cursor` 值域写前端的人已经会了,
// 而平台侧本来就要做一次映射 (IDC_HAND / XC_hand2 / NSCursor), 多一层自造
// 词汇只会让"这个后端支持哪些形状"变得没法查。

// cursorShapes 是全部受支持的形状名 (也是各后端映射表的键域)。
//
// "none" (隐藏光标) 一并收在这里: 全屏播放器/自绘光标需要它, 而它在三个
// 后端上都是"同一件事" (把光标设成不可见), 不另开一条通道。
var cursorShapes = map[string]struct{}{
	"default": {}, "pointer": {}, "text": {}, "crosshair": {},
	"move": {}, "grab": {}, "grabbing": {}, "wait": {}, "progress": {},
	"help": {}, "not-allowed": {},
	"ew-resize": {}, "ns-resize": {}, "nwse-resize": {}, "nesw-resize": {},
	"col-resize": {}, "row-resize": {},
	"none": {},
}

// cursorAliases 把常见别名归一到规范名 (容错口径: 不认识的形状不报错,
// 退回 "default" —— 一个笔误不该让整窗的光标失灵)。
var cursorAliases = map[string]string{
	"hand":        "pointer",
	"pointing":    "pointer",
	"ibeam":       "text",
	"i-beam":      "text",
	"caret":       "text",
	"busy":        "wait",
	"hourglass":   "wait",
	"loading":     "progress",
	"forbidden":   "not-allowed",
	"no-drop":     "not-allowed",
	"move-x":      "ew-resize",
	"move-y":      "ns-resize",
	"horizontal":  "ew-resize",
	"vertical":    "ns-resize",
	"e-resize":    "ew-resize",
	"w-resize":    "ew-resize",
	"n-resize":    "ns-resize",
	"s-resize":    "ns-resize",
	"se-resize":   "nwse-resize",
	"nw-resize":   "nwse-resize",
	"ne-resize":   "nesw-resize",
	"sw-resize":   "nesw-resize",
	"cross":       "crosshair",
	"arrow":       "default",
	"normal":      "default",
	"auto":        "default",
	"inherit":     "default",
	"pointerhand": "pointer",
}

// normalizeCursor 把形状名归一为规范名; 空串/未知值 → "default"。
// 大小写不敏感 (脚本里写 "Pointer" 也该生效)。
func normalizeCursor(s string) string {
	if s == "" {
		return "default"
	}
	low := lowerASCII(s)
	if _, ok := cursorShapes[low]; ok {
		return low
	}
	if canon, ok := cursorAliases[low]; ok {
		return canon
	}
	return "default"
}

// isKnownCursor 报告形状名是否被识别 (供测试与文档断言用;
// normalizeCursor 会把未知值默默归到 default, 那是给运行期的,
// 而这里要的是"脚本写错了没有"这个事实)。
func isKnownCursor(s string) bool {
	low := lowerASCII(s)
	if _, ok := cursorShapes[low]; ok {
		return true
	}
	_, ok := cursorAliases[low]
	return ok
}

// lowerASCII 是 ASCII 小写化 (in-place, 不分配)。形状名全是 ASCII,
// 用不上 strings.ToLower 的 Unicode 分支。
func lowerASCII(s string) string {
	b := []byte(s)
	changed := false
	for i := 0; i < len(b); i++ {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
			changed = true
		}
	}
	if !changed {
		return s
	}
	return string(b)
}

// cursorTagDefaults 是"没写 cursor prop 时"的标签缺省形状。
//
// 与 hoverable() 的表**刻意一致** (交互件给手型), 另加两条只有光标才有的
// 常识: 可编辑字段给 I 形光标 (哪怕它因为 disabled 不 hoverable), 分隔线
// 给纵向缩放光标。两个表都改的时候要一起想 —— 但它们是两个表: 悬停反馈
// 是"面变亮", 光标是"鼠标变样", 将来出现"要手型但不该变亮"的组件时不必
// 被绑在一起。
var cursorTagDefaults = map[string]string{
	"input":    "text",
	"search":   "text",
	"textarea": "text",

	"button":     "pointer",
	"checkbox":   "pointer",
	"radio":      "pointer",
	"switch":     "pointer",
	"select":     "pointer",
	"slider":     "pointer",
	"menu":       "pointer",
	"menu-item":  "pointer",
	"tab":        "pointer",
	"pagination": "pointer",
	"rating":     "pointer",
	"list-item":  "pointer",
	"tree-row":   "pointer",

	"separator": "col-resize",
}

// nodeCursorShape 解析节点链决定的光标形状 (target 为 nil → "default")。
//
// 判定顺序:
//  1. 从鼠标底下那个节点沿父链找**最近的 `cursor` prop** —— 显式声明优先,
//     而且"就近"符合直觉 (内层写了就以内层为准);
//  2. 都没有再看**标签缺省**: 同样沿链找, 取最近一个命中缺省表的标签;
//  3. 还没有就 "default"。
//
// 第 2 步沿链找而不是只看 target 本身是必需的: 鼠标几乎总是落在 #text 或
// 内层 rect 上, 而"按钮"是它们上面几层的祖先。禁用链上的交互件不给手型
// —— 手型承诺"点了有反应", 而禁用的按钮点了没反应。
func nodeCursorShape(target *GuiNode) string {
	if target == nil {
		return "default"
	}
	if s, ok := target.PropStr("cursor"); ok && s != "" {
		return normalizeCursor(s)
	}
	for p := target.Parent; p != nil; p = p.Parent {
		if s, ok := p.PropStr("cursor"); ok && s != "" {
			return normalizeCursor(s)
		}
	}
	if target.disabledInChain() {
		return "default"
	}
	if s, ok := cursorTagDefaults[target.Tag]; ok {
		return s
	}
	for p := target.Parent; p != nil; p = p.Parent {
		if s, ok := cursorTagDefaults[p.Tag]; ok {
			return s
		}
	}
	return "default"
}

// setCursorOverride 设/清窗口级光标覆盖 (空串 = 清除)。
//
// 立刻重算一次: 不清的话"点了忙等按钮之后光标不变", 用户会以为卡住了 ——
// 覆盖值只在 MouseMove 时生效会让它显得不可靠。清除时只能回到"鼠标底下
// 那个节点", 而那个节点此刻不在手边 (MouseMove 才带着坐标), 所以退回
// default, 等下一次移动校正 (这是所有平台的常规行为)。
func (a *app) setCursorOverride(shape string) {
	if a == nil {
		return
	}
	if shape == "" {
		a.applyCursorShape("default")
		a.mu.Lock()
		a.cursorOverride = ""
		a.mu.Unlock()
		return
	}
	norm := normalizeCursor(shape)
	a.mu.Lock()
	a.cursorOverride = norm
	a.mu.Unlock()
	a.applyCursorShape(norm)
}

// cursorOverrideShape 取当前窗口级覆盖 (空 = 无覆盖)。
func (a *app) cursorOverrideShape() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cursorOverride
}

// applyNodeCursor 按悬停目标解析并应用光标 (setHover 的收尾调用)。
func (a *app) applyNodeCursor(target *GuiNode) {
	if a == nil {
		return
	}
	if ov := a.cursorOverrideShape(); ov != "" {
		a.applyCursorShape(ov) // 覆盖期间鼠标底下是什么都不改光标
		return
	}
	a.applyCursorShape(nodeCursorShape(target))
}

// applyCursorShape 把形状下发给后端 (与上次相同则跳过)。
//
// 去重是必要的: MouseMove 是最高频的事件 (拖窗口时每像素一条), 每次都调
// 一次平台 SetCursor 是纯浪费。记 currentShape 而不是每帧重算, 也让"后端
// 不支持光标"这件事只体现在这里 (没有 cursorHost 就什么都不做, 不报错)。
func (a *app) applyCursorShape(shape string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	s := a.surface
	prev := a.cursorShape
	if prev == shape {
		a.mu.Unlock()
		return
	}
	a.cursorShape = shape
	a.mu.Unlock()
	if h, ok := s.(cursorHost); ok {
		h.SetCursor(shape)
	}
}
