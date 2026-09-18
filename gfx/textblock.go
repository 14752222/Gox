package gfx

// 文本块自动换行 (P2-6a)。
//
// 约定: `text` 元素是"文本块"的唯一载体 —— #text 文本节点永远单行
// (它是 JSX 里字符串子节点的隐式形式, 没有地方挂 prop)。所以 wrap /
// ellipsis 只认 `text` 标签。
//
// 两个 prop:
//   - `wrap`      : true 开启自动换行; 有它才可能多行。
//   - `ellipsis`  : 数字 = 最多几行且末行加 "..."; true 等价 1 行。
//                   它隐含 wrap (不换行就无从"超行截断")。
//
// 宽度约束从哪来 (决定了换行在哪断):
//   1. 显式 `width` —— 最高优先级, 也是唯一在 intrinsicSize 阶段就能确定的
//      情况 (所以"定宽 + wrap"的文本块在父容器里量尺寸就是准的);
//   2. 父容器交叉轴 stretch 给的盒宽 —— 见 stretchesCross(): 开了 wrap 的
//      文本块**默认铺满可用宽度**, 否则它没有换行依据 (列里会照样溢出);
//      这种情况需要在父容器把盒宽定下来之后**回头再算一次高度**。
//
// 为什么高度要算两遍而不是"只在绘制时换行": 盒高参与兄弟节点的位置分配
// (column 的主轴是高度), 量错了会压住下面的兄弟。绘制端与测量端共用
// wrapText, 保证两边行数一致。

// wrapsText 报告节点是否为开启自动换行的文本块。
func (n *GuiNode) wrapsText() bool {
	if n.Tag != "text" {
		return false
	}
	if b, ok := n.PropBool("wrap"); ok && b {
		return true
	}
	if v, ok := n.PropNum("wrap"); ok && v > 0 {
		return true
	}
	if v, ok := n.PropNum("ellipsis"); ok && v > 0 {
		return true
	}
	if b, ok := n.PropBool("ellipsis"); ok && b {
		return true
	}
	return false
}

// maxLines 返回行数上限 (0 = 不限)。只有 `ellipsis` 会限制行数。
func (n *GuiNode) maxLines() int {
	if v, ok := n.PropNum("ellipsis"); ok && v > 0 {
		return int(v)
	}
	if b, ok := n.PropBool("ellipsis"); ok && b {
		return 1
	}
	return 0
}

// blockWrapWidth 返回换行所用宽度: 显式 width 优先, 否则用父给的约束宽。
// 返回 0 表示"没有宽度约束" ⇒ 不折行 (只按 '\n' 分)。
func (n *GuiNode) blockWrapWidth(constraint int) int {
	if v, ok := n.PropNum("width"); ok && int(v) > 0 {
		return int(v)
	}
	if constraint < 0 {
		return 0
	}
	return constraint
}

// blockLines 返回文本块换行后的行 (已按 ellipsis 截断), 绘制与测量共用。
func (n *GuiNode) blockLines(constraint int) []string {
	return wrapText(n.TextContent(), n.FontSize(), n.blockWrapWidth(constraint), n.maxLines())
}

// blockSize 按约束宽测量文本块 (宽 = 最长行, 高 = 行数 × 行高)。
func (n *GuiNode) blockSize(constraint int) (w, h int) {
	return MeasureTextMulti(n.TextContent(), n.FontSize(),
		n.blockWrapWidth(constraint), n.maxLines())
}

// blockHeight 在父容器定下最终盒宽之后重新求高: 只有"开了 wrap 且没有
// 显式 width"的文本块需要 (其余情况 fallback 已经是准确的)。
func (n *GuiNode) blockHeight(cross, fallback int) int {
	if !n.wrapsText() {
		return fallback
	}
	if _, ok := n.PropNum("width"); ok {
		return fallback // 定宽: intrinsicSize 已经按同一宽度算过
	}
	if cross <= 0 {
		return fallback
	}
	_, h := n.blockSize(cross)
	return h
}
