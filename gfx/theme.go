package gfx

import (
	"fmt"
	"image/color"

	"github.com/14752222/Gox/config"
	"github.com/14752222/Gox/object"
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

// SetThemeNamed 按预设名切换 ("light"/"dark"), 未知名返回 false 且不动现状。
func SetThemeNamed(name string) bool {
	switch name {
	case "light":
		SetTheme(themeLight())
	case "dark":
		SetTheme(themeDark())
	default:
		return false
	}
	themeName = name
	return true
}

// ToggleDark 在亮/暗间切换; 当前是自定义主题时按"最近一次预设名"的反面切。
func ToggleDark() {
	if themeName == "dark" {
		SetThemeNamed("light")
		return
	}
	SetThemeNamed("dark")
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

// ─────────────────────────────────────────────────────────────────────────────
// gx/theme JS 门面
// ─────────────────────────────────────────────────────────────────────────────

func init() {
	object.RegisterBuiltinModule("gx/theme", func() map[string]object.Value {
		// gox.json 的 "theme" 字段在此消费 (模块首次被 import 时)。
		// 脚本随后显式 setTheme 会覆盖它; window 元素的 theme prop 在
		// render() 时应用, 晚于这里 —— 三条入口优先级: setTheme 调用 >
		// window prop > gox.json。
		if cfg, err := config.Load("."); err == nil && cfg.Theme != "" {
			SetThemeNamed(cfg.Theme)
		}
		return map[string]object.Value{
			"setTheme":   object.NewBuiltin("setTheme", jsSetTheme),
			"toggleDark": object.NewBuiltin("toggleDark", jsToggleDark),
			"current":    object.NewBuiltin("current", jsCurrentTheme),
		}
	})
}

// jsSetTheme setTheme("light"|"dark") 或 setTheme({accent: "#e67e22", ...})。
// 预设名返回布尔 (未知名 false); 对象形式返回实际生效的覆盖个数。
func jsSetTheme(args ...object.Value) object.Value {
	if len(args) == 0 {
		return object.NewTypeError("setTheme: expected a preset name or an overrides object")
	}
	switch v := args[0].(type) {
	case *object.String:
		return object.NewBoolean(SetThemeNamed(v.Value))
	case *object.Object:
		overrides := map[string]string{}
		for _, key := range v.Keys() {
			if val, ok := v.GetProperty(key); ok {
				if s, ok := val.(*object.String); ok {
					overrides[key] = s.Value
				}
			}
		}
		return object.NewNumber(float64(ApplyOverrides(overrides)))
	default:
		return object.NewTypeError("setTheme: expected a string or an object, got %s", object.TypeOf(args[0]))
	}
}

// jsToggleDark toggleDark(): 亮 ↔ 暗。
func jsToggleDark(args ...object.Value) object.Value {
	ToggleDark()
	return object.NewBoolean(true)
}

// jsCurrentTheme current(): 读当前 token 表 (键 = token 名, 值 = #rrggbbaa
// 十六进制), 供 devtools 快照或脚本按 token 取色。
func jsCurrentTheme(args ...object.Value) object.Value {
	t := themeCurrent
	o := object.NewObject()
	for key, hex := range themeTokens(&t) {
		o.SetProperty(key, object.NewString(hex))
	}
	return o
}

// themeTokens 把主题导出为 "token 名 → #rrggbbaa" 表 (jsCurrentTheme 与
// 文档生成共用同一份口径)。
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
}
