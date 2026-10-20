package gfx

// ===== WCAG 2.1 对比度计算（Prism S2）=====
//
// 干什么: 给"这个字在这个底上到底看不看得清"一个**可断言的数字**。
//
// 为什么自己写而不是引第三方: 判据只有 WCAG 2.1 的十几行公式，而 gfx 的硬
// 约束是零 cgo + 不新增依赖；更关键的是这份公式要被 CI 当成**契约**用 ——
// 自己写才能把"分界取 0.03928 还是 0.04045""alpha 怎么算"这些取舍写进注释，
// 让下一次改色的人知道自己在改什么。
//
// 三个阈值来自 WCAG 2.1：
//   - 1.4.3 Contrast (Minimum) AA：正文文字 ≥4.5:1；大号文字（≥18pt，或
//     ≥14pt 加粗）≥3:1；
//   - 1.4.11 Non-text Contrast AA：控件边界、图形对象等非文本元素 ≥3:1。
//
// 两个容易踩的坑（本文件就是为了堵它们）:
//
//  1. **不能直接拿 sRGB 通道值算**。显示器输出是 gamma 编码过的，#808080
//     的"物理亮度"不是 #FFFFFF 的一半，而是约 21.6%。必须先线性化
//     （srgbLinearize），再按人眼对三原色的敏感度加权（relativeLuminance）。
//
//  2. **带 alpha 的色不能直接比**。Theme 里 selection / mask / scrim /
//     scrollTrack / tooltipFace 都是半透明的，它们"看起来是什么颜色"取决于
//     画在什么上面。拿原始 RGBA 去比，得到的是"这层颜料和那层颜料的比值"，
//     不是用户看到的比值。所以要先 Composite 到实际背景上，再比。

import (
	"image/color"
	"math"
)

// WCAG 2.1 的三个 AA 阈值。取常量而不是散在测试里的字面量: 阈值是**承诺**,
// 承诺要能被文档、测试、未来的 lint 三处引用同一份定义。
const (
	// ContrastTextMin 是正文文字对比度下限 (WCAG 2.1 · 1.4.3 AA)。
	ContrastTextMin = 4.5
	// ContrastLargeTextMin 是大号文字对比度下限 (1.4.3 AA): 18pt 以上,
	// 或 14pt 以上且加粗。标题、数字大盘这类用这一档。
	ContrastLargeTextMin = 3.0
	// ContrastNonTextMin 是非文本对比度下限 (1.4.11 AA): 控件边界、图标、
	// 图形对象。注意它约束的是"识别控件所必需的边界", 纯装饰线不受约束 ——
	// 这就是为什么 Theme 要把 BorderControl 与 BorderDecorative 拆开。
	ContrastNonTextMin = 3.0
)

// srgbLinearize 把一个 sRGB 通道值 (0..255) 转成线性光 (0..1)。
//
// 分界取 WCAG 2.1 原文的 0.03928（sRGB 规范里是 0.04045，两者的差异只影响
// 极暗端第三位小数）：这里跟着 WCAG 走，因为**判据的权威来源必须是 WCAG**，
// 不然实测值与官方参考值（#777777 vs #FFFFFF ≈4.48）对不上，反而说不清是谁错了。
func srgbLinearize(c uint8) float64 {
	s := float64(c) / 255
	if s <= 0.03928 {
		// 低值段是直线段: gamma 曲线在接近 0 处的斜率有限, 取直线是为了
		// 避免 (0,0) 处的导数发散。
		return s / 12.92
	}
	return math.Pow((s+0.055)/1.055, 2.4)
}

// relativeLuminance 按 ITU-R BT.709 的权重把线性 RGB 压成一个亮度标量 (0..1)。
//
// 权重不是等分的: 人眼对绿最敏感 (0.7152), 对蓝最迟钝 (0.0722)。所以"把蓝
// 通道压暗"在视觉上远比"把绿通道压暗"来得轻 —— 这也是暗色主题里蓝色系总
// 显得"更暗"的原因。
//
// 注意这里**不看 alpha**: 传入的应是最终呈现在屏幕上的颜色。带 alpha 的请先
// 用 Composite 合成。
func relativeLuminance(c color.RGBA) float64 {
	return 0.2126*srgbLinearize(c.R) +
		0.7152*srgbLinearize(c.G) +
		0.0722*srgbLinearize(c.B)
}

// Contrast 返回两个**不透明**颜色的对比度 (1..21)。
//
// 返回值恒 ≥1 且 ≤21: 纯黑 vs 纯白是 21, 同色是 1。L1 取较亮者, 所以顺序
// 无关 (Contrast(a,b) == Contrast(b,a))。
//
// 两个入参若有 alpha, 结果没有意义 —— 请改用 ContrastOn。
func Contrast(a, b color.RGBA) float64 {
	l1, l2 := relativeLuminance(a), relativeLuminance(b)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// Composite 把半透明前景 fg 按 source-over 画到不透明背景 bg 上, 返回实际呈
// 现出的颜色 (alpha 恒为 255)。
//
// 为什么是"背景不透明"这个前提: 屏幕最终一定落在某个不透明的东西上（窗口
// 底、页面底），多层半透明可以逐层调用本函数自底向上叠。反过来若允许透明
// 背景, 就得引入 Porter-Duff 的一般形式, 而这里没有一个真实场景需要它。
//
// +0.5 是四舍五入: 直接截断会让 0.949 的 alpha 每层都偏暗 1 个色阶, 叠几层
// 之后实测色与理论色差出肉眼可见的一档。
func Composite(fg, bg color.RGBA) color.RGBA {
	a := float64(fg.A) / 255
	inv := 1 - a
	mix := func(f, b uint8) uint8 {
		v := a*float64(f) + inv*float64(b) + 0.5
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v)
	}
	return color.RGBA{
		R: mix(fg.R, bg.R),
		G: mix(fg.G, bg.G),
		B: mix(fg.B, bg.B),
		A: 255,
	}
}

// ContrastOn 返回"半透明前景画在 bg 上之后, 与 bg 的对比度"。
//
// 典型用法: 选区底 selection (带 alpha) 画在 fieldFace 上的可辨识度、遮罩
// scrim 压在页面上的可辨识度。它与 Contrast 的差别不是精度问题, 是**对象不
// 同**: Contrast 比的是两个最终颜色, ContrastOn 比的是"一层半透明"与"它下
// 面的东西"。
func ContrastOn(fg, bg color.RGBA) float64 {
	return Contrast(Composite(fg, bg), bg)
}

// contrastHex 把颜色渲染成 #rrggbbaa, 只用于断言失败时的错误信息。
//
// 不带 alpha 会被误读: #0f172a 与 #0f172aff 是同一个颜色, 但 #0f172a00 是
// 全透明 —— 排查"这块怎么没画出来"时, 这两者的区别就是全部线索。
func contrastHex(c color.RGBA) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, 9)
	out = append(out, '#')
	for _, v := range []uint8{c.R, c.G, c.B, c.A} {
		out = append(out, digits[v>>4], digits[v&0x0f])
	}
	return string(out)
}
