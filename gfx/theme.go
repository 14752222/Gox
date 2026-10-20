package gfx

import (
	"fmt"
	"image/color"
	"strconv"
)

// Theme 设计 token 与主题系统 (T07)。
//
// token 是"组件缺省外观的命名事实来源": raster.go 里那批 colorXxx 包级变量
// 变成 Theme 的**投影缓存** —— CurrentTheme() 是唯一事实来源, SetTheme 把
// 它同步回写包级变量 (组件绘制代码引用变量名不动, 视觉零迁移成本), 然后
// 全窗口整帧标脏, 下一帧生效。
//
// 设计取舍:
//   - 只收**颜色** token + 预设切换; 间距/圆角继续由 props 控制 (组件各自
//     有语义, 没有"全局缺省值"可讲), 字号由 defaultFontSize 兜底 —— 这些
//     不进 v1 token 表, 免得"改了不生效"的 token 比没有更糟。
//   - props 永远优先: 主题改的是**缺省值**, 脚本显式写的 background 等不
//     受影响 (切换主题不会把用户自己的配色改掉)。
//   - 切换不重启: SetTheme 当场同步 + 全窗口标脏, 下一帧就是新主题
//     (GUI 线程与 VM 线程同线程, 无锁竞争; 测试并发访问由调用方保证)。
//   - gox.json 的 "theme" 字段在 gx/theme 模块**首次被 import** 时应用
//     (与 update_module 读 gox.json version 同款模式); 脚本随后显式
//     setTheme 可覆盖它。
//
// JS 侧:
//
//	import { setTheme, toggleDark, current } from "gx/theme";
//	setTheme("dark");                     // 预设名
//	setTheme({ accent: "#e67e22" });      // 在当前主题上覆盖若干 token
//	toggleDark();                         // 亮 ↔ 暗
//	current();                            // 读当前 token 表 (调试/快照)

// Theme 是一组设计 token 的快照。字段名 = 包级色变量名去掉 color 前缀。
type Theme struct {
	Accent        color.RGBA // 选中/进度填充 (强调色)
	AccentText    color.RGBA // 强调色上的前景 (勾/圆点)
	FieldEdge     color.RGBA // checkbox/radio/switch 边框
	BtnFace       color.RGBA // button 缺省底色
	BtnEdge       color.RGBA // button 缺省边框
	Track         color.RGBA // 轨道/分隔线 (浅灰)
	SwitchOff     color.RGBA // switch 关闭态轨道
	Knob          color.RGBA // switch 滑块
	Text          color.RGBA // 缺省文字色
	FocusRing     color.RGBA // 焦点虚线框
	FieldFace     color.RGBA // 输入类字段底色
	FieldHover    color.RGBA // 字段悬停底
	FieldPress    color.RGBA // 字段按压底
	InputEdge     color.RGBA // 输入类字段边框
	Placeholder   color.RGBA // placeholder 灰字
	ScrollTrack   color.RGBA // 滚动条轨道 (半透明)
	ScrollThumb   color.RGBA // 滚动条滑块
	OptionActive  color.RGBA // 下拉项高亮底
	PopupFace     color.RGBA // 弹层/卡片底色
	PopupEdge     color.RGBA // 弹层/卡片边框
	Mask          color.RGBA // modal 遮罩
	Info          color.RGBA // toast info
	Warn          color.RGBA // toast warn
	Danger        color.RGBA // toast error
	TooltipFace   color.RGBA // 提示弹层底色
	TooltipText   color.RGBA // 提示弹层文字
	MenuBarFace   color.RGBA // 菜单栏底色
	MenuBarEdge   color.RGBA // 菜单栏底边线
	MenuActive    color.RGBA // 展开中的菜单标题底
	MenuHighlight color.RGBA // 菜单项高亮底
	MenuShortcut  color.RGBA // 快捷键文字
	MenuSep       color.RGBA // 菜单分隔线
	Selection     color.RGBA // 文本选区高亮底 (input / textarea)

	// ---- Prism 扩展 (S1)：语义化层级 ----
	//
	// 上面 33 个是 v1 的历史 token，名字与值都被外部依赖（ApplyOverrides 的
	// key、脚本里的 setTheme({accent:…})、ThemeTokens 的输出），**一个都不改**。
	// 下面这批是"新名字追加"，解决 v1 的三个结构性问题：
	//   - 只有一层 surface（缺 sunken/raised/overlay 阶梯，层次只能靠画边框）；
	//   - 文字只有一档（缺 secondary/muted/disabled，什么字都用 text）；
	//   - 语义色只有实底（缺 subtle 底 + text 前景，tonal 风格无从表达）。

	// Surface 系列：五级明度阶梯，从"最凹"到"最凸"。
	SurfaceBase    color.RGBA // 页面/卡片底（最常用）
	SurfaceSunken  color.RGBA // 凹陷区（表格斑马纹、代码块、inset 分组底）
	SurfaceRaised  color.RGBA // 凸起面（卡片、popover）
	SurfaceOverlay color.RGBA // 浮层（菜单、tooltip、dialog 面板）
	SurfaceInset   color.RGBA // 输入槽（字段内部、tabs 槽底）

	// Text 系列：四级文字层级。
	TextSecondary color.RGBA // 次级文字（说明、标签）
	TextMuted     color.RGBA // 弱化文字（时间戳、快捷键、辅助信息）
	TextDisabled  color.RGBA // 禁用文字
	TextInverse   color.RGBA // 反色文字（画在实底强调色/深色浮层上）

	// Border 系列：两级描边。**必须区分**——控件边界受 WCAG 1.4.11 约束
	// (≥3:1)，纯装饰线不受约束，混用会导致要么装饰线过重、要么控件边界不达标。
	BorderDecorative color.RGBA // 装饰性分隔线（豁免 1.4.11）
	BorderControl    color.RGBA // 可交互控件边界（须 ≥3:1）

	// AccentSubtle 是强调色的浅底（选中行、menu 高亮、select 选中项）。
	// v1 里 optionActive/menuHighlight 各自承担，Prism 统一到这一档。
	AccentSubtle color.RGBA

	// 语义四件套：实底 + subtle 底 + 同族前景文字。
	// v1 有 Info/Warn/Danger 实底但**缺 Success**，也没有任何 subtle/text，
	// 于是 alert/toast 只能用「彩色条 + 黑字」这种 2013 年的表达。
	// Info / Danger 的实底在上面 v1 段已有（Info / Danger），这里只补 subtle 与 text；
	// Warning 是 Warn 的语义化别名（新名字，值相同），Success 是 v1 完全缺失的一档。
	InfoSubtle    color.RGBA
	InfoText      color.RGBA
	Success       color.RGBA
	SuccessSubtle color.RGBA
	SuccessText   color.RGBA
	Warning       color.RGBA
	WarningSubtle color.RGBA
	WarningText   color.RGBA
	DangerSubtle  color.RGBA
	DangerText    color.RGBA

	// ---- W1 各批组件所需的通用状态色 ----
	//
	// 这六个是"组件会反复用到、但没有归属"的中间色。放进 Theme 的直接原因：
	// table/tree/list 三处曾各自硬编码 #EEF3FA，切暗色主题时**完全不跟随**
	// （它们不在 Theme 里，syncThemeVars 同步不到）—— 这是实测出的 bug，
	// 不是风格问题。
	StateHover       color.RGBA // 通用 hover 底（button/menuitem/tab）
	StatePress       color.RGBA // 通用按压底
	StateSelected    color.RGBA // 持久选中底（list-item selected）
	StateRowHover    color.RGBA // 行类 hover（table/tree/list 共用，替代三处硬编码）
	ScrollThumbHover color.RGBA // 滚动条滑块 hover（比静置态深一档）
	Scrim            color.RGBA // 弹层遮罩（dialog/modal 55%）
}

// themeCurrent 是当前生效的主题 (CurrentTheme 返回副本前的取用点)。
// 绘制线程读它, SetTheme 写它 —— GUI 与 VM 同线程, 常规路径无竞争;
// SetTheme 做成函数而非直接导出变量, 便于未来需要时换成 atomic.Pointer。
var themeCurrent = themeLight()

// themeName 记当前主题的名字 ("light"/"dark"/"custom"), toggleDark 用。
var themeName = "light"

// themeLight 是亮色预设: 数值与主题系统引入前的缺省色板**逐字一致**
// (历史见 raster.go 的原色板注释), 默认状态下视觉零回归。
func themeLight() Theme {
	return Theme{
		Accent:        color.RGBA{R: 0x27, G: 0xAE, B: 0x60, A: 255},
		AccentText:    color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		FieldEdge:     color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255},
		BtnFace:       color.RGBA{R: 0xE8, G: 0xE8, B: 0xE8, A: 255},
		BtnEdge:       color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255},
		Track:         color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255},
		SwitchOff:     color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 255},
		Knob:          color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		Text:          color.RGBA{R: 26, G: 26, B: 26, A: 255},
		FocusRing:     color.RGBA{R: 0x1A, G: 0x5F, B: 0xB4, A: 255},
		FieldFace:     color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		FieldHover:    color.RGBA{R: 0xF0, G: 0xF4, B: 0xF9, A: 255},
		FieldPress:    color.RGBA{R: 0xE0, G: 0xE9, B: 0xF4, A: 255},
		InputEdge:     color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255},
		Placeholder:   color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255},
		ScrollTrack:   color.RGBA{R: 0, G: 0, B: 0, A: 0x14},
		ScrollThumb:   color.RGBA{R: 0xA0, G: 0xA0, B: 0xA0, A: 255},
		OptionActive:  color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255},
		PopupFace:     color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		PopupEdge:     color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255},
		Mask:          color.RGBA{R: 0, G: 0, B: 0, A: 0x66},
		Info:          color.RGBA{R: 0x2F, G: 0x80, B: 0xED, A: 255},
		Warn:          color.RGBA{R: 0xE8, G: 0x89, B: 0x0C, A: 255},
		Danger:        color.RGBA{R: 0xC0, G: 0x39, B: 0x2B, A: 255},
		TooltipFace:   color.RGBA{R: 0x26, G: 0x26, B: 0x26, A: 0xF2},
		TooltipText:   color.RGBA{R: 0xF5, G: 0xF5, B: 0xF5, A: 255},
		MenuBarFace:   color.RGBA{R: 0xF3, G: 0xF3, B: 0xF3, A: 255},
		MenuBarEdge:   color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255},
		MenuActive:    color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255},
		MenuHighlight: color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255},
		MenuShortcut:  color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 255},
		MenuSep:       color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255},
		// 选区底: 半透明强调蓝。**必须带 alpha** —— 选区画在文字之下, 用不
		// 透明色会把被选中的字整段盖掉 (经典的"选中就看不见字" bug)。
		Selection: color.RGBA{R: 0x9C, G: 0xC4, B: 0xEC, A: 0xB0},

		// ---- Prism 扩展字段的 v1 推导值 ----
		//
		// S1 的硬要求是"旧 key 值一字不变、输出零变化"。那指的**只是上面 33
		// 个历史 token**；新增字段 v1 本来就没有，它们取"从旧色板能推导出的
		// 最近邻"—— 这样旧预设在引入新字段后外观仍与改造前一致（组件读到
		// surfaceBase 时拿到的就是它原本硬编码的那个白）。
		// Prism 预设（theme_prism.go）会给出真正经过对比度计算的值。
		SurfaceBase:    color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255}, // = popupFace / fieldFace
		SurfaceSunken:  color.RGBA{R: 0xFA, G: 0xFA, B: 0xFA, A: 255}, // = 旧 table zebra #FAFAFA
		SurfaceRaised:  color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		SurfaceOverlay: color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255}, // = popupFace
		SurfaceInset:   color.RGBA{R: 0xF3, G: 0xF3, B: 0xF3, A: 255}, // = menuBarFace

		TextSecondary: color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255}, // = fieldEdge (旧次级文字实际用的色)
		TextMuted:     color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 255}, // = menuShortcut
		TextDisabled:  color.RGBA{R: 0xAA, G: 0xAA, B: 0xAA, A: 255}, // 旧 disabled 是 text+alpha, 这里给等效实色
		TextInverse:   color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255}, // = accentText

		BorderDecorative: color.RGBA{R: 0xD0, G: 0xD0, B: 0xD0, A: 255}, // = track
		BorderControl:    color.RGBA{R: 0x99, G: 0x99, B: 0x99, A: 255}, // = inputEdge

		AccentSubtle: color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255}, // = optionActive

		InfoSubtle:    color.RGBA{R: 0xE6, G: 0xF0, B: 0xFD, A: 255},
		InfoText:      color.RGBA{R: 0x1D, G: 0x4E, B: 0x89, A: 255},
		Success:       color.RGBA{R: 0x27, G: 0xAE, B: 0x60, A: 255}, // v1 无处表达成功色, 借用 accent
		SuccessSubtle: color.RGBA{R: 0xE7, G: 0xF6, B: 0xED, A: 255},
		SuccessText:   color.RGBA{R: 0x1E, G: 0x7A, B: 0x45, A: 255},
		Warning:       color.RGBA{R: 0xE8, G: 0x89, B: 0x0C, A: 255}, // = warn
		WarningSubtle: color.RGBA{R: 0xFD, G: 0xF3, B: 0xE2, A: 255},
		WarningText:   color.RGBA{R: 0x8A, G: 0x52, B: 0x08, A: 255},
		DangerSubtle:  color.RGBA{R: 0xFB, G: 0xE9, B: 0xE7, A: 255},
		DangerText:    color.RGBA{R: 0x91, G: 0x2B, B: 0x20, A: 255},

		StateHover:       color.RGBA{R: 0xF0, G: 0xF4, B: 0xF9, A: 255}, // = fieldHover
		StatePress:       color.RGBA{R: 0xE0, G: 0xE9, B: 0xF4, A: 255}, // = fieldPress
		StateSelected:    color.RGBA{R: 0xDC, G: 0xE8, B: 0xF8, A: 255}, // = optionActive
		StateRowHover:    color.RGBA{R: 0xEE, G: 0xF3, B: 0xFA, A: 255}, // = 旧 table/tree/list 硬编码值
		ScrollThumbHover: color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 255},
		Scrim:            color.RGBA{R: 0, G: 0, B: 0, A: 0x66}, // = mask (v1 遮罩本就 40%)
	}
}

// themeDark 是暗色预设: 底色压到 #1E~#2D 段, 文字/描边提到近白, 强调色
// 整体提亮一档 (暗底上饱和色会发闷); tooltip 反转为深底浅字并保持与弹层
// 同调, 遮罩维持半透明黑。
func themeDark() Theme {
	return Theme{
		Accent:        color.RGBA{R: 0x2F, G: 0xBF, B: 0x6B, A: 255},
		AccentText:    color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		FieldEdge:     color.RGBA{R: 0x88, G: 0x88, B: 0x88, A: 255},
		BtnFace:       color.RGBA{R: 0x2D, G: 0x2D, B: 0x2D, A: 255},
		BtnEdge:       color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255},
		Track:         color.RGBA{R: 0x3A, G: 0x3A, B: 0x3A, A: 255},
		SwitchOff:     color.RGBA{R: 0x4A, G: 0x4A, B: 0x4A, A: 255},
		Knob:          color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255},
		Text:          color.RGBA{R: 0xEA, G: 0xEA, B: 0xEA, A: 255},
		FocusRing:     color.RGBA{R: 0x4D, G: 0x8F, B: 0xE8, A: 255},
		FieldFace:     color.RGBA{R: 0x1E, G: 0x1E, B: 0x1E, A: 255},
		FieldHover:    color.RGBA{R: 0x25, G: 0x2B, B: 0x33, A: 255},
		FieldPress:    color.RGBA{R: 0x2B, G: 0x33, B: 0x3D, A: 255},
		InputEdge:     color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255},
		Placeholder:   color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 255},
		ScrollTrack:   color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x14},
		ScrollThumb:   color.RGBA{R: 0x5A, G: 0x5A, B: 0x5A, A: 255},
		OptionActive:  color.RGBA{R: 0x2A, G: 0x3B, B: 0x52, A: 255},
		PopupFace:     color.RGBA{R: 0x25, G: 0x25, B: 0x25, A: 255},
		PopupEdge:     color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255},
		Mask:          color.RGBA{R: 0, G: 0, B: 0, A: 0x66},
		Info:          color.RGBA{R: 0x4A, G: 0x9D, B: 0xF0, A: 255},
		Warn:          color.RGBA{R: 0xF0, G: 0xA0, B: 0x30, A: 255},
		Danger:        color.RGBA{R: 0xE0, G: 0x55, B: 0x45, A: 255},
		TooltipFace:   color.RGBA{R: 0x33, G: 0x33, B: 0x33, A: 0xF2},
		TooltipText:   color.RGBA{R: 0xF0, G: 0xF0, B: 0xF0, A: 255},
		MenuBarFace:   color.RGBA{R: 0x23, G: 0x23, B: 0x23, A: 255},
		MenuBarEdge:   color.RGBA{R: 0x3A, G: 0x3A, B: 0x3A, A: 255},
		MenuActive:    color.RGBA{R: 0x2A, G: 0x3B, B: 0x52, A: 255},
		MenuHighlight: color.RGBA{R: 0x2A, G: 0x3B, B: 0x52, A: 255},
		MenuShortcut:  color.RGBA{R: 0x90, G: 0x90, B: 0x90, A: 255},
		MenuSep:       color.RGBA{R: 0x3A, G: 0x3A, B: 0x3A, A: 255},
		// 暗底上不能照抄亮色的浅蓝: 那样选区比正文还亮, 反差方向就反了。
		// 这里用低饱和的深蓝 + 更高的 alpha (暗底本身对比度低)。
		Selection: color.RGBA{R: 0x2E, G: 0x54, B: 0x7A, A: 0xD0},

		// Prism 扩展字段的 v1 暗色推导值（理由同 themeLight 同段注释）。
		SurfaceBase:    color.RGBA{R: 0x1E, G: 0x1E, B: 0x1E, A: 255}, // = fieldFace
		SurfaceSunken:  color.RGBA{R: 0x18, G: 0x18, B: 0x18, A: 255}, // 比 base 再压一档
		SurfaceRaised:  color.RGBA{R: 0x2D, G: 0x2D, B: 0x2D, A: 255}, // = btnFace
		SurfaceOverlay: color.RGBA{R: 0x25, G: 0x25, B: 0x25, A: 255}, // = popupFace
		SurfaceInset:   color.RGBA{R: 0x23, G: 0x23, B: 0x23, A: 255}, // = menuBarFace

		TextSecondary: color.RGBA{R: 0xC8, G: 0xC8, B: 0xC8, A: 255},
		TextMuted:     color.RGBA{R: 0x90, G: 0x90, B: 0x90, A: 255}, // = menuShortcut
		TextDisabled:  color.RGBA{R: 0x6A, G: 0x6A, B: 0x6A, A: 255},
		TextInverse:   color.RGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 255}, // 暗色 accent 是亮绿, 反色仍取白

		BorderDecorative: color.RGBA{R: 0x3A, G: 0x3A, B: 0x3A, A: 255}, // = track
		BorderControl:    color.RGBA{R: 0x55, G: 0x55, B: 0x55, A: 255}, // = inputEdge

		AccentSubtle: color.RGBA{R: 0x2A, G: 0x3B, B: 0x52, A: 255}, // = optionActive

		InfoSubtle:    color.RGBA{R: 0x1B, G: 0x2A, B: 0x3D, A: 255},
		InfoText:      color.RGBA{R: 0x8C, G: 0xC2, B: 0xF7, A: 255},
		Success:       color.RGBA{R: 0x2F, G: 0xBF, B: 0x6B, A: 255}, // = accent
		SuccessSubtle: color.RGBA{R: 0x18, G: 0x2E, B: 0x22, A: 255},
		SuccessText:   color.RGBA{R: 0x6E, G: 0xD8, B: 0x9A, A: 255},
		Warning:       color.RGBA{R: 0xF0, G: 0xA0, B: 0x30, A: 255},
		WarningSubtle: color.RGBA{R: 0x33, G: 0x28, B: 0x14, A: 255},
		WarningText:   color.RGBA{R: 0xF5, G: 0xC0, B: 0x76, A: 255},
		DangerSubtle:  color.RGBA{R: 0x35, G: 0x1C, B: 0x1A, A: 255},
		DangerText:    color.RGBA{R: 0xF2, G: 0x93, B: 0x88, A: 255},

		StateHover:       color.RGBA{R: 0x25, G: 0x2B, B: 0x33, A: 255}, // = fieldHover
		StatePress:       color.RGBA{R: 0x2B, G: 0x33, B: 0x3D, A: 255}, // = fieldPress
		StateSelected:    color.RGBA{R: 0x2A, G: 0x3B, B: 0x52, A: 255}, // = optionActive
		StateRowHover:    color.RGBA{R: 0x2A, G: 0x30, B: 0x38, A: 255}, // 旧硬编码 #EEF3FA 的暗色对应
		ScrollThumbHover: color.RGBA{R: 0x77, G: 0x77, B: 0x77, A: 255},
		Scrim:            color.RGBA{R: 0, G: 0, B: 0, A: 0x66}, // = mask
	}
}

// CurrentTheme 返回当前主题的副本 (调用方改副本不影响生效主题)。
func CurrentTheme() Theme {
	return themeCurrent
}

// ThemeName 返回当前主题名 ("light"/"dark"/"custom")。
func ThemeName() string {
	return themeName
}

// SetTheme 应用一个主题: 同步投影缓存 + 全窗口标脏, 下一帧生效。
func SetTheme(t Theme) {
	themeCurrent = t
	syncThemeVars()
	markAllWindowsDirty()
}

// SetThemeNamed 按预设名切换 ("light"/"dark"/"prism-light"/"prism-dark"),
// 未知名返回 false 且不动现状。
//
// 两套 Prism 预设与 v1 **并存**而不是替换: 旧预设的色值是存量契约, 改一个就
// 等于给所有已上线的应用发一次无请求的视觉更新; 想要 Prism 的观感由调用方
// 显式点名 (gox.json 的 "theme" 字段或 setTheme("prism-dark"))。
func SetThemeNamed(name string) bool {
	switch name {
	case "light":
		SetTheme(themeLight())
	case "dark":
		SetTheme(themeDark())
	case "prism-light":
		SetTheme(prismLight())
	case "prism-dark":
		SetTheme(prismDark())
	default:
		return false
	}
	themeName = name
	return true
}

// ToggleDark 在亮/暗间切换; 当前是自定义主题时按"最近一次预设名"的反面切。
//
// 换向**停留在同一家族内**: 当前是 prism-dark 时切到 prism-light, 而不是跳到
// v1 的 light —— 那样一回合一换, 观感会在"冷青 slate"与"绿色灰白"之间来回
// 断裂, 而用户只是想开个夜间模式。
func ToggleDark() {
	switch themeName {
	case "dark":
		SetThemeNamed("light")
	case "prism-light":
		SetThemeNamed("prism-dark")
	case "prism-dark":
		SetThemeNamed("prism-light")
	default:
		// "light" 与 "custom" 都落到这里: custom 无法反推它源自哪个家族,
		// 沿用 v1 的行为切到 v1 暗色。
		SetThemeNamed("dark")
	}
}

// ApplyOverrides 在当前主题上覆盖若干 token (key = 字段名小驼峰, 如
// "accent"/"btnFace"; value = 与 props 同一套颜色写法: #hex / rgb() /
// 命名色)。未知的 key 或解析失败的颜色静默跳过 —— 覆盖是锦上添花,
// 不该让整个调用失败。
func ApplyOverrides(overrides map[string]string) int {
	t := themeCurrent
	applied := 0
	for key, raw := range overrides {
		c, ok := ParseColor(raw)
		if !ok {
			continue
		}
		if t.setToken(key, c) {
			applied++
		}
	}
	if applied > 0 {
		themeName = "custom"
		SetTheme(t)
	}
	return applied
}

// setToken 按名字写一个 token (ApplyOverrides 的表驱动落点)。
func (t *Theme) setToken(name string, c color.RGBA) bool {
	switch name {
	case "accent":
		t.Accent = c
	case "accentText":
		t.AccentText = c
	case "fieldEdge":
		t.FieldEdge = c
	case "btnFace":
		t.BtnFace = c
	case "btnEdge":
		t.BtnEdge = c
	case "track":
		t.Track = c
	case "switchOff":
		t.SwitchOff = c
	case "knob":
		t.Knob = c
	case "text":
		t.Text = c
	case "focusRing":
		t.FocusRing = c
	case "fieldFace":
		t.FieldFace = c
	case "fieldHover":
		t.FieldHover = c
	case "fieldPress":
		t.FieldPress = c
	case "inputEdge":
		t.InputEdge = c
	case "placeholder":
		t.Placeholder = c
	case "scrollTrack":
		t.ScrollTrack = c
	case "scrollThumb":
		t.ScrollThumb = c
	case "optionActive":
		t.OptionActive = c
	case "popupFace":
		t.PopupFace = c
	case "popupEdge":
		t.PopupEdge = c
	case "mask":
		t.Mask = c
	case "info":
		t.Info = c
	case "warn":
		t.Warn = c
	case "danger":
		t.Danger = c
	case "tooltipFace":
		t.TooltipFace = c
	case "tooltipText":
		t.TooltipText = c
	case "menuBarFace":
		t.MenuBarFace = c
	case "menuBarEdge":
		t.MenuBarEdge = c
	case "menuActive":
		t.MenuActive = c
	case "menuHighlight":
		t.MenuHighlight = c
	case "menuShortcut":
		t.MenuShortcut = c
	case "menuSep":
		t.MenuSep = c
	case "selection":
		t.Selection = c

	// ---- Prism 扩展 (S1) ----
	// 漏一个分支的后果是 ApplyOverrides({"surfaceBase":…}) **静默跳过**
	// （返回的 applied 计数不含它），脚本作者会以为内核不认识这个 token。
	// themeTokens / syncThemeVars 同理；三处必须与 struct 字段一一对应，
	// 由 theme_snapshot_test.go 的完备性用例兜底。
	case "surfaceBase":
		t.SurfaceBase = c
	case "surfaceSunken":
		t.SurfaceSunken = c
	case "surfaceRaised":
		t.SurfaceRaised = c
	case "surfaceOverlay":
		t.SurfaceOverlay = c
	case "surfaceInset":
		t.SurfaceInset = c
	case "textSecondary":
		t.TextSecondary = c
	case "textMuted":
		t.TextMuted = c
	case "textDisabled":
		t.TextDisabled = c
	case "textInverse":
		t.TextInverse = c
	case "borderDecorative":
		t.BorderDecorative = c
	case "borderControl":
		t.BorderControl = c
	case "accentSubtle":
		t.AccentSubtle = c
	case "infoSubtle":
		t.InfoSubtle = c
	case "infoText":
		t.InfoText = c
	case "success":
		t.Success = c
	case "successSubtle":
		t.SuccessSubtle = c
	case "successText":
		t.SuccessText = c
	case "warning":
		t.Warning = c
	case "warningSubtle":
		t.WarningSubtle = c
	case "warningText":
		t.WarningText = c
	case "dangerSubtle":
		t.DangerSubtle = c
	case "dangerText":
		t.DangerText = c
	case "stateHover":
		t.StateHover = c
	case "statePress":
		t.StatePress = c
	case "stateSelected":
		t.StateSelected = c
	case "stateRowHover":
		t.StateRowHover = c
	case "scrollThumbHover":
		t.ScrollThumbHover = c
	case "scrim":
		t.Scrim = c
	default:
		return false
	}
	return true
}

// markAllWindowsDirty 让所有存活窗口整帧重绘 (主题切换的可见化)。
func markAllWindowsDirty() {
	for _, a := range appsSnapshot() {
		a.mu.Lock()
		a.needDraw = true
		a.fullDirty = true
		a.mu.Unlock()
	}
}

// ThemeTokens 把当前主题导出为 "token 名 → #rrggbbaa" 表 (gfx/bind 的
// gx/theme current() 与文档生成共用同一份口径)。
func ThemeTokens() map[string]string {
	t := themeCurrent
	return themeTokens(&t)
}

// themeTokens 把主题导出为 "token 名 → #rrggbbaa" 表。
func themeTokens(t *Theme) map[string]string {
	hex := func(c color.RGBA) string {
		return fmt.Sprintf("#%02x%02x%02x%02x", c.R, c.G, c.B, c.A)
	}
	return map[string]string{
		"accent":        hex(t.Accent),
		"accentText":    hex(t.AccentText),
		"fieldEdge":     hex(t.FieldEdge),
		"btnFace":       hex(t.BtnFace),
		"btnEdge":       hex(t.BtnEdge),
		"track":         hex(t.Track),
		"switchOff":     hex(t.SwitchOff),
		"knob":          hex(t.Knob),
		"text":          hex(t.Text),
		"focusRing":     hex(t.FocusRing),
		"fieldFace":     hex(t.FieldFace),
		"fieldHover":    hex(t.FieldHover),
		"fieldPress":    hex(t.FieldPress),
		"inputEdge":     hex(t.InputEdge),
		"placeholder":   hex(t.Placeholder),
		"scrollTrack":   hex(t.ScrollTrack),
		"scrollThumb":   hex(t.ScrollThumb),
		"optionActive":  hex(t.OptionActive),
		"popupFace":     hex(t.PopupFace),
		"popupEdge":     hex(t.PopupEdge),
		"mask":          hex(t.Mask),
		"info":          hex(t.Info),
		"warn":          hex(t.Warn),
		"danger":        hex(t.Danger),
		"tooltipFace":   hex(t.TooltipFace),
		"tooltipText":   hex(t.TooltipText),
		"menuBarFace":   hex(t.MenuBarFace),
		"menuBarEdge":   hex(t.MenuBarEdge),
		"menuActive":    hex(t.MenuActive),
		"menuHighlight": hex(t.MenuHighlight),
		"selection":     hex(t.Selection),
		"menuShortcut":  hex(t.MenuShortcut),
		"menuSep":       hex(t.MenuSep),

		// ---- Prism 扩展 (S1)：27 个颜色字段 ----
		"surfaceBase":      hex(t.SurfaceBase),
		"surfaceSunken":    hex(t.SurfaceSunken),
		"surfaceRaised":    hex(t.SurfaceRaised),
		"surfaceOverlay":   hex(t.SurfaceOverlay),
		"surfaceInset":     hex(t.SurfaceInset),
		"textSecondary":    hex(t.TextSecondary),
		"textMuted":        hex(t.TextMuted),
		"textDisabled":     hex(t.TextDisabled),
		"textInverse":      hex(t.TextInverse),
		"borderDecorative": hex(t.BorderDecorative),
		"borderControl":    hex(t.BorderControl),
		"accentSubtle":     hex(t.AccentSubtle),
		"infoSubtle":       hex(t.InfoSubtle),
		"infoText":         hex(t.InfoText),
		"success":          hex(t.Success),
		"successSubtle":    hex(t.SuccessSubtle),
		"successText":      hex(t.SuccessText),
		"warning":          hex(t.Warning),
		"warningSubtle":    hex(t.WarningSubtle),
		"warningText":      hex(t.WarningText),
		"dangerSubtle":     hex(t.DangerSubtle),
		"dangerText":       hex(t.DangerText),
		"stateHover":       hex(t.StateHover),
		"statePress":       hex(t.StatePress),
		"stateSelected":    hex(t.StateSelected),
		"stateRowHover":    hex(t.StateRowHover),
		"scrollThumbHover": hex(t.ScrollThumbHover),
		"scrim":            hex(t.Scrim),

		// ---- 非颜色 token (S1)：数字以十进制字符串输出 ----
		//
		// 为什么也放进 ThemeTokens：JS 侧 `current()` 是脚本读"系统尺度"的
		// 唯一入口，而间距/圆角/reduced-motion 恰恰是脚本最该复用的东西
		// （手写 padding="12" 的脚本无法随 2x 屏等比放大）。这 6 个 key
		// **不接受覆盖**（setToken 里没有它们）—— 尺度是全系统常量，
		// 不是单个应用能改的东西。
		"space4":     strconv.Itoa(Space(Space2)),
		"space8":     strconv.Itoa(Space(Space4)),
		"space16":    strconv.Itoa(Space(Space6)),
		"radius8":    strconv.Itoa(Radius(RadiusMD)),
		"motionBase": strconv.Itoa(MotionBase),
		"touchMin":   strconv.Itoa(TouchMin),
	}
}

// syncThemeVars 把当前主题同步进 raster.go 的包级色变量 (组件绘制代码的
// 引用点)。变量 = 投影缓存, 事实来源是 themeCurrent。
func syncThemeVars() {
	t := themeCurrent
	colorAccent = t.Accent
	colorAccentText = t.AccentText
	colorFieldEdge = t.FieldEdge
	colorBtnFace = t.BtnFace
	colorBtnEdge = t.BtnEdge
	colorTrack = t.Track
	colorSwitchOff = t.SwitchOff
	colorKnob = t.Knob
	colorText = t.Text
	colorFocusRing = t.FocusRing
	colorFieldFace = t.FieldFace
	colorFieldHover = t.FieldHover
	colorFieldPress = t.FieldPress
	colorInputEdge = t.InputEdge
	colorPlaceholder = t.Placeholder
	colorScrollTrack = t.ScrollTrack
	colorScrollThumb = t.ScrollThumb
	colorOptionActive = t.OptionActive
	colorPopupFace = t.PopupFace
	colorPopupEdge = t.PopupEdge
	colorMask = t.Mask
	colorInfo = t.Info
	colorWarn = t.Warn
	colorDanger = t.Danger
	colorTooltipFace = t.TooltipFace
	colorTooltipText = t.TooltipText
	colorMenuBarFace = t.MenuBarFace
	colorMenuBarEdge = t.MenuBarEdge
	colorMenuActive = t.MenuActive
	colorMenuHighlight = t.MenuHighlight
	colorMenuShortcut = t.MenuShortcut
	colorMenuSep = t.MenuSep
	colorSelection = t.Selection

	// Prism 扩展 (S1)。组件绘制代码读的是这批包级变量，漏同步的症状是
	// "切主题后某个组件颜色不跟随"，且不会有任何报错。
	colorSurfaceBase = t.SurfaceBase
	colorSurfaceSunken = t.SurfaceSunken
	colorSurfaceRaised = t.SurfaceRaised
	colorSurfaceOverlay = t.SurfaceOverlay
	colorSurfaceInset = t.SurfaceInset
	colorTextSecondary = t.TextSecondary
	colorTextMuted = t.TextMuted
	colorTextDisabled = t.TextDisabled
	colorTextInverse = t.TextInverse
	colorBorderDecorative = t.BorderDecorative
	colorBorderControl = t.BorderControl
	colorAccentSubtle = t.AccentSubtle
	colorInfoSubtle = t.InfoSubtle
	colorInfoText = t.InfoText
	colorSuccess = t.Success
	colorSuccessSubtle = t.SuccessSubtle
	colorSuccessText = t.SuccessText
	colorWarning = t.Warning
	colorWarningSubtle = t.WarningSubtle
	colorWarningText = t.WarningText
	colorDangerSubtle = t.DangerSubtle
	colorDangerText = t.DangerText
	colorStateHover = t.StateHover
	colorStatePress = t.StatePress
	colorStateSelected = t.StateSelected
	colorStateRowHover = t.StateRowHover
	colorScrollThumbHover = t.ScrollThumbHover
	colorScrim = t.Scrim
}
