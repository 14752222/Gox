package gfx

import (
	"image"

	"github.com/14752222/Gox/object"
)

// T08 搜索框 <search>: input 的字段变体。
//
// 与 <input> 共用同一套字段机制 —— 受控 value / onInput / model 指令 /
// placeholder / 光标与点击定位 (inputInChain 把两者收进同一条链)。差异只有
// 两点:
//
//  1. 左侧画一个放大镜 (复用内置 icon "search" 的绘制函数, 颜色用
//     placeholder 灰 —— 图标是"字段的一部分", 不跟文字色走);
//  2. 获焦时按 Enter 派发 onSearch({value}) —— 搜索是"整段提交"的交互,
//     逐键 onInput 之外再给一个明确的提交点 (与 DOM 搜索框一致)。
//
// 注意 <textarea> 不做这个变体: 多行编辑里 Enter 是换行, 语义冲突。
// 绘制核心与光标定位共用 paintField / searchLeading 这一个口径
// (见 input.go 的 paintField 注释)。

const (
	searchIconW   = 14 // 左侧放大镜的边长
	searchIconGap = 4  // 图标与文字的间距
)

// searchLeading 报告字段里"文字区左移量" (图标占位); 非 search 标签恒为 0。
// 绘制 (paintField) 与点击定位 (setCaretFromX) 都经它取值 —— 两处各写一份
// 迟早漂移, 表现为"光标画的位置和点击落点对不上"。
func searchLeading(n *GuiNode) int {
	if n.Tag == "search" {
		return searchIconW + searchIconGap
	}
	return 0
}

// paintSearch 绘制搜索框: 与 input 同一套字段核心 + 左侧放大镜。
func paintSearch(img *image.RGBA, n *GuiNode, disabled bool) {
	paintField(img, n, disabled, searchLeading(n))
	b := n.Box
	if b.W <= 0 || b.H <= 0 {
		return
	}
	fn := builtinIcons["search"]
	if fn == nil {
		return // 内置图标表被改动了名字 —— 与 <icon> 同口径: 不画, 不崩
	}
	fn(img, Rect{X: b.X + fieldPadX, Y: b.Y + (b.H-searchIconW)/2, W: searchIconW, H: searchIconW},
		searchIconW, tint(colorPlaceholder, disabled))
}

// dispatchSearch 派发 onSearch({value}) (Enter 提交入口; 值取当前受控值)。
func (a *app) dispatchSearch(n *GuiNode) {
	if n.PropHandler("onSearch") == nil {
		return
	}
	arg := object.NewObject()
	arg.SetProperty("value", object.NewString(n.inputValue()))
	a.callHandler(n, "onSearch", arg)
}
