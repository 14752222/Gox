package gfx

// 非颜色设计 token（Prism 设计系统 S1）。
//
// 为什么要有这个文件：theme.go 文件头的取舍曾说「只收颜色 token；间距/圆角继续
// 由 props 控制」—— 实际后果是没有系统缺省，每个脚本各写一套 radius/padding，
// 同一套组件在不同 app 里长得不一样。这里把「尺度」固化下来，组件读它、脚本
// 不再重复手写。
//
// 三条边界（与 Epic 约定一致）：
//  1. **不进 Theme struct**。Theme 是「颜色快照 + 可覆盖」，SetTheme 的调用方
//     不该被迫填三十个和自己无关的数字；尺度是全系统常量，不是主题的一部分。
//  2. **不做 blur / 大面积实时模糊**。gfx 是 CPU 软光栅，层次靠「发丝边 +
//     紧错阴影 + 明度差」表达，这里的尺寸都按像素级小值给。
//  3. **不主动改既有行高缺省与 defaultFontSize**。defaultFontSize()=16 与
//     行高 size+size/4 被多处断言锁定，改它会打破全部 golden。type 尺度只
//     提供层级，应用层用 lineHeight 显式 opt-in。
//
// 单位：除注明 px 的 Motion / Touch 外，Space / Radius 的数值都是**设计像素
// (dp)**，使用时经 Space()/Radius() 换算成物理像素 —— 与 density.go 的
// dpToPx 同口径，这样 1x / 2x / 3x 屏上控件比例一致（多设备定位的根基）。

// SpaceTokens 是 4pt 基准的间距尺度（设计像素）。6 与 20 是两个半档，
// 用在「比 4 大一点、比 8 小」的紧凑场景（如 badge 内边距、图标与文字间隙）。
//
// 用具名常量而不是裸数字：脚本里写 padding="space-4" 比 padding="4" 更能
// 表达意图，也让后续调整基准时只改一处。
const (
	Space0  = 0  // 无间距（重置用）
	Space1  = 2  // 发丝级：边框内侧微调
	Space2  = 4  // 最小可见间距：图标与文字
	Space3  = 6  // 半档：紧凑控件内边距
	Space4  = 8  // 常用内边距：字段左右 8
	Space5  = 12 // 中等：字段左右 12、卡片内边距
	Space6  = 16 // 标准：按钮左右 16、卡片内边距
	Space7  = 20 // 半档：大卡片内边距
	Space8  = 24 // 宽松：区块之间
	Space9  = 32 // 分节：大区块之间
	Space10 = 40 // 页面级留白
)

// Space 把设计像素 (dp) 换算成物理像素，与 density.go 的 dpToPx 同口径。
//
// 1x 屏上 dpToPx 是恒等（截断），所以桌面默认视觉零变化；2x/3x 屏上按
// displayScale 放大，控件尺寸随之等比，不会被压成"一坨小字"。
func Space(dp int) int {
	return dpToPx(float64(dp))
}

// RadiusToken 是圆角档位（设计像素）。用独立类型而不是裸 int：调用点
// Radius(RadiusMD) 比 Radius(8) 更难写错，也让 later 的 Radius(px) 适配器
// 不会和"直接给像素"混淆。
type RadiusToken int

const (
	RadiusNone RadiusToken = 0  // 直角：表格行、分隔线
	RadiusXS   RadiusToken = 4  // 极小：badge / tag 内嵌小片
	RadiusSM   RadiusToken = 6  // 小：pagination 按钮、小标签
	RadiusMD   RadiusToken = 8  // 中：按钮、输入框（组件缺省）
	RadiusLG   RadiusToken = 12 // 大：卡片、popover
	RadiusXL   RadiusToken = 16 // 特大：dialog / modal 面板
	RadiusFull RadiusToken = -1 // 全圆：tag / chip、圆形头像、pip 徽标
)

// Radius 把圆角档位换算成物理像素。
//
// RadiusFull 返回 -1：这是「全圆」的哨兵值，绘制侧见到负数就按
// min(w,h)/2 算 —— 任何固定数值都无法在未知尺寸下表达「胶囊形」，
// 而胶囊形正是 tag / chip / avatar 的语义。
func Radius(r RadiusToken) int {
	if r == RadiusFull {
		return -1
	}
	return dpToPx(float64(r))
}

// Motion 是动画时长（毫秒）。三档覆盖全部组件过渡：
//   - Fast：微交互（勾选出场、hover 变色）
//   - Base：常规过渡（switch 位移、dialog 面板）
//   - Slow：大块内容（tabs 指示条、toast 入场）
//
// 数值取 Material 3 / Radix 同档中值：120ms 以下人眼读不出"动过"，260ms
// 以上开始显得拖沓。**reduced-motion 时一律按 0 处理**（见 motion.go 的
// PrefersReducedMotion），不要在调用点各自 if。
const (
	MotionFast = 120 // ms：勾选/radio 出场、hover 过渡
	MotionBase = 180 // ms：switch 位移、tabs 指示条
	MotionSlow = 260 // ms：dialog 面板缩放、toast 入场
)

// TouchMin 是触控目标的最小边长（**物理像素**，不是设计像素）。
//
// 44 是 Apple HIG / Material 的共同下限，也是 WCAG 2.1 SC 2.5.5 (AAA) 的
// 数值。它是**硬指标**：多设备定位下，手机/平板上任何可点元素的命中区都
// 不能小于它，哪怕视觉尺寸更小（视觉小、命中区大是标准做法 —— 见
// checkbox 18×18 + 44 命中区的组合）。
//
// 不做 dpToPx 换算的原因：44 已经是物理像素口径，再乘 displayScale 会在
// 3x 屏上得到 132px 的荒谬命中区；而触控设备的 displayScale 本来就已把
// 视觉尺寸放大到了等效 44 物理像素以上。
const TouchMin = 44

// MotionDuration 把 Motion token 换算成实际生效的时长：reduced-motion
// 开启时返回 0（瞬时完成），否则原值。
//
// 这是**唯一**该被调用点使用的入口 —— 直接写 MotionBase 会绕过
// reduced-motion 降级，违反可访问性承诺。时长单位与 animate.go 的
// transitionDuration 一致（毫秒）。
func MotionDuration(ms int) int {
	if prefersReducedMotion() {
		return 0
	}
	return ms
}
