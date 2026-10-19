package gfx

// ===== Prism 预设色板（S2）=====
//
// 干什么: 给 Prism 设计系统两套**经过对比度计算**的预设：prism-light /
// prism-dark。它们与 v1 的 light / dark **并存**，不是替换 —— 旧预设的 33 个
// 历史色值一个都不动（S0 基线冻结 + 存量应用的视觉契约），想换新观感的应用
// 显式 `setTheme("prism-light")` 即可。
//
// 与 v1 预设的三点结构性差别（这也是 Prism 存在的理由）:
//
//  1. **surface 有五级阶梯**，层次靠明度而不是靠画边框；
//  2. **文字有四档**（primary / secondary / muted / disabled），快捷键、时间戳
//     这类"小字"不必再拿正文色硬凑；
//  3. **语义色是 tonal 四件套**（实底 + subtle 底 + 同族前景字），alert / toast
//     不必再用"彩色条 + 黑字"。
//
// 色值怎么来的: 先按 Prism 的调性（冷青蓝 accent + slate 中性色）取一版，再用
// gfx/contrast.go 逐对复算 WCAG 2.1 比值，**不达标就换色、不放宽阈值**。实测
// 数字与取舍记录在 docs/accessibility.md §9，由 gfx/contrast_test.go 在 CI 里
// 逐条守住 —— 本文件里出现的每个色值都能在文档里查到它的对比度证据。
//
// 为什么色值写十六进制注释而不写 color.RGBA 的十进制: 设计交付、文档与
// ThemeTokens() 的输出口径都是十六进制，中间再转一次十进制就多一处抄错的机会。
// 结构字面量里仍按 Go 惯例写 0xRR, 0xGG, 0xBB, 0xAA。

import "image/color"

// rgba 把 #rrggbb(aa) 写成 Go 字面量, 减少"设计稿 → 代码"这一步的抄写错误。
//
// 为什么不解析字符串: 主题是**进程启动时就要用**的东西，解析失败要么 panic
// 要么静默给个零值（全透明黑），两者都比编译期常量差。写死常量，错误留给
// contrast_test.go 在 CI 里报。
func rgba(r, g, b, a uint8) color.RGBA {
	return color.RGBA{R: r, G: g, B: b, A: a}
}

// ---- 亮色家族共用的一档"强调色按压" ----
//
// 61 个 token 里**没有** accentHover 字段（S1 冻结的字段集，加字段要同时改
// Theme struct / themeLight / themeDark / themeTokens / setToken / syncThemeVars
// 与 raster.go 的投影变量，风险远大于收益）。但"accent 填充的按钮按下去是什么
// 颜色"是组件层迟早要用的事实，所以这两个值以常量形式钉在这里并写进文档，
// 免得将来各组件各拍一个值。
const (
	// accentHoverLight 是亮色 accent #0E7490 的 hover/按压档（压深一档，白字
	// 在其上 7.20:1，仍是 AA）。
	accentHoverLight = 0x0B5F77
	// accentHoverDark 是暗色 accent #22D3EE 的 hover/按压档（提亮一档，深字
	// #042A33 在其上仍有 8.6:1）。
	accentHoverDark = 0x67E8F9
)

// prismLight 是 Prism 亮色预设。
//
// 调性: 页底是极浅的冷灰（#F4F7FB）而不是纯白 —— 纯白会让"卡片浮在页面上"
// 这件事只能靠描边表达，而描边一旦够深就会盖过内容；有了这一档底色，卡片
// （#F9FBFD）与浮层（#FFFFFF）就能靠明度分层。
func prismLight() Theme {
	return Theme{
		// ---- 历史 33 个 token 的 Prism 取值 ----
		//
		// 名字与语义沿用 v1（脚本里的 setTheme({accent:…}) 照样生效），值全部
		// 重取。唯一刻意保持"原样"的是 Selection 的**必须带 alpha**这件事：
		// 选区画在文字下面，不透明色会把被选中的字整段盖掉。
		Accent:     rgba(0x0E, 0x74, 0x90, 255), // 青蓝。白字其上 5.36:1
		AccentText: rgba(0xFF, 0xFF, 0xFF, 255), // accent 实底上的前景
		FieldEdge:  rgba(0x7A, 0x89, 0x9D, 255), // checkbox/radio/switch 边框：控件边界，受 1.4.11 约束
		BtnFace:    rgba(0xFF, 0xFF, 0xFF, 255), // 按钮缺省底：卡片级白
		BtnEdge:    rgba(0x7A, 0x89, 0x9D, 255), // 与 btnFace 3.56:1，过 1.4.11
		Track:      rgba(0xCB, 0xD5, 0xE1, 255), // 轨道/分隔线（装饰，不受 1.4.11 约束）
		SwitchOff:  rgba(0x94, 0xA3, 0xB8, 255), // switch 关闭态轨道
		Knob:       rgba(0xFF, 0xFF, 0xFF, 255),
		Text:       rgba(0x0F, 0x17, 0x2A, 255), // 近黑的冷调，比纯黑柔和且对比度更充裕
		FocusRing:  rgba(0x25, 0x63, 0xEB, 255), // 与页底 4.88:1、与白 5.17:1，远超 1.4.11 的 3:1
		FieldFace:  rgba(0xFF, 0xFF, 0xFF, 255), // 输入字段用纯白：与页底拉开，也是 placeholder 的对比基准
		FieldHover: rgba(0xF1, 0xF5, 0xF9, 255),
		FieldPress: rgba(0xE2, 0xE8, 0xF0, 255),
		InputEdge:  rgba(0x7A, 0x89, 0x9D, 255), // = borderControl，输入边界是控件边界
		// placeholder 比 Epic 初稿的 #64748B 压深一档：#64748B 在纯白上是 4.76:1，
		// 但 Prism 的页底不是纯白，在 #F4F7FB 上只有 4.43:1 —— 差 0.07 也是不达标。
		Placeholder:   rgba(0x5E, 0x70, 0x89, 255),  // 白 5.05:1 / 页底 4.70:1
		ScrollTrack:   rgba(0x0F, 0x17, 0x2A, 0x14), // 8% 冷黑：轨道只是"这里能滚"的提示，压重了会抢内容
		ScrollThumb:   rgba(0x94, 0xA3, 0xB8, 255),
		OptionActive:  rgba(0xCF, 0xFA, 0xFE, 255), // = accentSubtle
		PopupFace:     rgba(0xFF, 0xFF, 0xFF, 255),
		PopupEdge:     rgba(0xCB, 0xD5, 0xE1, 255),
		Mask:          rgba(0x0F, 0x17, 0x2A, 0x66), // modal 遮罩 40%
		Info:          rgba(0x25, 0x63, 0xEB, 255),  // 白字其上 5.17:1
		Warn:          rgba(0xB4, 0x53, 0x09, 255),  // = warning（v1 名字）
		Danger:        rgba(0xB9, 0x1C, 0x1C, 255),  // 白字其上 6.47:1
		TooltipFace:   rgba(0x0F, 0x17, 0x2A, 0xF2), // 95% 深底：tooltip 是"浮在最上面"的层，用最深的一档
		TooltipText:   rgba(0xF8, 0xFA, 0xFC, 255),  // 合成到底色上 ≈15:1
		MenuBarFace:   rgba(0xE9, 0xEE, 0xF4, 255),
		MenuBarEdge:   rgba(0xCB, 0xD5, 0xE1, 255),
		MenuActive:    rgba(0xCF, 0xFA, 0xFE, 255),  // = accentSubtle
		MenuHighlight: rgba(0xCF, 0xFA, 0xFE, 255),  // = accentSubtle
		MenuShortcut:  rgba(0x4A, 0x57, 0x68, 255),  // = textMuted（v1 这里只有 4.04:1，见 §9 的已知问题）
		MenuSep:       rgba(0xE2, 0xE8, 0xF0, 255),  // = borderDecorative
		Selection:     rgba(0x67, 0xC9, 0xE3, 0x99), // 60% 青蓝：**必须带 alpha**，否则盖掉被选中的字

		// ---- Surface 五级阶梯 ----
		//
		// 明度自下而上递增，相邻两级相对亮度差 ≥3%（sunken 0.802 → inset 0.850
		// → base 0.927 → raised 0.962 → overlay 1.000）。差 3% 是"肉眼能看出这是
		// 两块不同的面，但不会被当成两条内容"的经验线：低于它等于没分层，高于它
		// 会让页面变成灰阶拼贴。
		SurfaceBase:    rgba(0xF4, 0xF7, 0xFB, 255), // 页面/卡片底（最常用）
		SurfaceSunken:  rgba(0xE2, 0xE8, 0xF0, 255), // 凹陷区：表格斑马纹、代码块
		SurfaceRaised:  rgba(0xF9, 0xFB, 0xFD, 255), // 卡片：比页底亮一档，浮起来
		SurfaceOverlay: rgba(0xFF, 0xFF, 0xFF, 255), // 浮层：菜单/tooltip/dialog 面板，最亮 + 阴影
		SurfaceInset:   rgba(0xE9, 0xEE, 0xF4, 255), // 输入槽 / tabs 槽底：比卡片暗，像"凹进去"

		// ---- 文字四档 ----
		//
		// 四档的相对亮度依次抬升（0.0088 → 0.0514 → 0.0928 → 0.169），既能拉开
		// 层级，又都还在页底上稳过 4.5:1 —— 包括原来最吃亏的"快捷键小字"。
		TextSecondary: rgba(0x33, 0x41, 0x55, 255), // 页底 9.64:1
		TextMuted:     rgba(0x4A, 0x57, 0x68, 255), // 页底 6.85:1 / 菜单栏底 6.71:1
		TextDisabled:  rgba(0x94, 0xA3, 0xB8, 255), // 禁用态豁免 1.4.3，但仍保持可辨（2.6:1）
		TextInverse:   rgba(0xFF, 0xFF, 0xFF, 255), // 亮色语义实底都是"够深的实色"，反色取白

		// ---- 描边两级 ----
		//
		// 拆开的意义见 Theme 的注释：控件边界要过 1.4.11 的 3:1，装饰线要"看得见
		// 但不抢戏"。用同一个值的结果必然是两头不讨好。
		BorderDecorative: rgba(0xE2, 0xE8, 0xF0, 255), // 白底 1.19:1 —— 装饰线**故意**不达标
		BorderControl:    rgba(0x7A, 0x89, 0x9D, 255), // 白 3.56:1 / 页底 3.36:1

		AccentSubtle: rgba(0xCF, 0xFA, 0xFE, 255), // 选中行 / 菜单高亮 / select 选中项

		// ---- 语义四件套 ----
		//
		// 每族三值: 实底（配 textInverse）、subtle 底（配同族 text）、text。
		// subtle 底比页底暗 4%~13%（有可辨明度差），同时同族 text 在其上 ≥4.5:1。
		InfoSubtle:    rgba(0xDB, 0xEA, 0xFE, 255),
		InfoText:      rgba(0x1D, 0x4E, 0xD8, 255), // infoSubtle 上 5.49:1
		Success:       rgba(0x15, 0x80, 0x3D, 255), // 白字其上 5.02:1
		SuccessSubtle: rgba(0xBB, 0xF7, 0xD0, 255), // 比初稿 #DCFCE7 压深：#DCFCE7 与页底只差
		// 2.3% 明度，肉眼分不出"这是块提示"还是"这是块空白"
		SuccessText:   rgba(0x16, 0x65, 0x34, 255), // successSubtle 上 6.49:1
		Warning:       rgba(0xB4, 0x53, 0x09, 255), // 白字其上 5.02:1（所以亮色不必换深字）
		WarningSubtle: rgba(0xFE, 0xF3, 0xC7, 255),
		WarningText:   rgba(0x92, 0x40, 0x0E, 255), // warningSubtle 上 6.37:1
		DangerSubtle:  rgba(0xFE, 0xE2, 0xE2, 255),
		DangerText:    rgba(0x99, 0x1B, 0x1B, 255), // dangerSubtle 上 6.80:1（比 danger 实底再压一档）

		// ---- 通用状态色 ----
		StateHover:       rgba(0xF1, 0xF5, 0xF9, 255),
		StatePress:       rgba(0xE2, 0xE8, 0xF0, 255),
		StateSelected:    rgba(0xCF, 0xFA, 0xFE, 255), // = accentSubtle
		StateRowHover:    rgba(0xED, 0xF2, 0xF8, 255), // table/tree/list 共用（替代三处硬编码）
		ScrollThumbHover: rgba(0x64, 0x74, 0x8B, 255),
		Scrim:            rgba(0x00, 0x00, 0x00, 0x8C), // 55% 黑：dialog/modal 遮罩
	}
}

// prismDark 是 Prism 暗色预设。
//
// 与亮色最大的**方向性**差别在 accent 上：暗底上"亮青 + 白字"只有 1.9:1 这种
// 灾难级读数，所以暗色的 accent 是**高亮青 + 深字**（#22D3EE + #042A33，
// 8.40:1）。这是本轮最关键的取舍：它意味着 Prism 暗色里"accent 实底上的字"是
// 深色，与亮色相反 —— 任何写死"强调按钮上放白字"的组件都必须读 accentText
// 而不是读 textInverse。
func prismDark() Theme {
	return Theme{
		Accent:     rgba(0x22, 0xD3, 0xEE, 255), // 高亮青
		AccentText: rgba(0x04, 0x2A, 0x33, 255), // 深字：与 accent 8.40:1
		FieldEdge:  rgba(0x64, 0x74, 0x8B, 255),
		BtnFace:    rgba(0x1C, 0x26, 0x37, 255), // = surfaceRaised
		BtnEdge:    rgba(0x64, 0x74, 0x8B, 255), // 与 btnFace 3.19:1，刚好过 1.4.11
		Track:      rgba(0x2A, 0x35, 0x47, 255),
		SwitchOff:  rgba(0x3D, 0x4A, 0x5E, 255),
		// 滑块取近白而不是纯白: 纯白滑块在暗底上会"发光", 视觉重量压过字段本身
		Knob:       rgba(0xE6, 0xED, 0xF5, 255),
		Text:       rgba(0xE6, 0xED, 0xF5, 255), // 页底 14.43:1
		FocusRing:  rgba(0x22, 0xD3, 0xEE, 255), // = accent，暗底上 9.43:1
		FieldFace:  rgba(0x0E, 0x16, 0x26, 255), // = surfaceInset（输入槽那一级）
		FieldHover: rgba(0x16, 0x20, 0x3A, 255),
		FieldPress: rgba(0x1C, 0x26, 0x37, 255),
		InputEdge:  rgba(0x64, 0x74, 0x8B, 255),
		// placeholder 沿用 Epic 的 #78879F：它在字段底上是 4.96:1、页底 4.67:1。
		// 这一档在暗色里反而比亮色好做（暗底对比度的"分母"本来就小）。
		Placeholder:   rgba(0x78, 0x87, 0x9F, 255),
		ScrollTrack:   rgba(0xFF, 0xFF, 0xFF, 0x14), // 8% 白：暗底上要用"加光"而不是"压暗"来表达轨道
		ScrollThumb:   rgba(0x55, 0x63, 0x7A, 255),
		OptionActive:  rgba(0x0E, 0x3A, 0x47, 255), // = accentSubtle
		PopupFace:     rgba(0x23, 0x2E, 0x42, 255), // = surfaceOverlay
		PopupEdge:     rgba(0x3A, 0x46, 0x5C, 255),
		Mask:          rgba(0x00, 0x00, 0x00, 0x80), // modal 遮罩 50%
		Info:          rgba(0x60, 0xA5, 0xFA, 255),  // 暗底上语义实底要提亮，配深字 5.97:1
		Warn:          rgba(0xFB, 0xBF, 0x24, 255),  // = warning
		Danger:        rgba(0xF8, 0x71, 0x71, 255),
		TooltipFace:   rgba(0x0B, 0x12, 0x20, 0xF2), // 比浮层更深一档：tooltip 是"最上层"
		TooltipText:   rgba(0xF1, 0xF5, 0xF9, 255),  // 合成后 ≈14:1
		MenuBarFace:   rgba(0x16, 0x20, 0x3A, 255),
		MenuBarEdge:   rgba(0x2A, 0x35, 0x47, 255),
		MenuActive:    rgba(0x0E, 0x3A, 0x47, 255),
		MenuHighlight: rgba(0x0E, 0x3A, 0x47, 255),
		MenuShortcut:  rgba(0x94, 0xA3, 0xB8, 255), // = textMuted，菜单栏底上 6.29:1
		MenuSep:       rgba(0x2A, 0x35, 0x47, 255),
		Selection:     rgba(0x0E, 0x74, 0x90, 0x99), // 60% 深青：暗底上选区若太亮，反差方向就反了

		// ---- Surface 五级阶梯 ----
		//
		// 暗色的阶梯方向与亮色相反（越靠上越亮：sunken 最黑、overlay 最亮），
		// 相邻两级相对亮度差都在 29%~39%（暗端的相对差天然更大）。
		SurfaceBase:    rgba(0x13, 0x1C, 0x2E, 255), // 页面底
		SurfaceSunken:  rgba(0x0A, 0x11, 0x1C, 255), // 代码块 / 斑马纹
		SurfaceRaised:  rgba(0x1C, 0x26, 0x37, 255), // 卡片
		SurfaceOverlay: rgba(0x23, 0x2E, 0x42, 255), // 浮层：菜单 / dialog 面板
		SurfaceInset:   rgba(0x0E, 0x16, 0x26, 255), // 输入槽

		// ---- 文字四档 ----
		TextSecondary: rgba(0xB6, 0xC2, 0xD4, 255), // 页底 9.45:1
		TextMuted:     rgba(0x94, 0xA3, 0xB8, 255), // 页底 6.64:1 / 菜单栏底 6.29:1
		TextDisabled:  rgba(0x64, 0x74, 0x8B, 255),
		TextInverse:   rgba(0x04, 0x2A, 0x33, 255), // 暗色语义实底都是亮色，反色取深

		BorderDecorative: rgba(0x2A, 0x35, 0x47, 255),
		BorderControl:    rgba(0x64, 0x74, 0x8B, 255),

		// ---- 语义四件套 ----
		//
		// 暗色的 subtle 底不是"亮色 subtle 底反过来"，而是"实底往页底方向压到
		// 10% 浓度"：它要比页底亮一点点（能看出是块提示），又不能亮到抢内容。
		// 同族 text 一律取比实底更亮的一档，保证在 subtle 上 ≥4.5:1。
		InfoSubtle:    rgba(0x10, 0x24, 0x3C, 255),
		InfoText:      rgba(0x93, 0xC5, 0xFD, 255), // infoSubtle 上 8.70:1
		Success:       rgba(0x4A, 0xDE, 0x80, 255),
		SuccessSubtle: rgba(0x12, 0x2A, 0x1E, 255),
		SuccessText:   rgba(0x86, 0xEF, 0xAC, 255), // successSubtle 上 10.88:1
		Warning:       rgba(0xFB, 0xBF, 0x24, 255),
		WarningSubtle: rgba(0x2E, 0x22, 0x08, 255),
		WarningText:   rgba(0xFC, 0xD3, 0x4D, 255), // warningSubtle 上 10.80:1
		DangerSubtle:  rgba(0x3A, 0x16, 0x14, 255),
		DangerText:    rgba(0xFC, 0xA5, 0xA5, 255), // dangerSubtle 上 8.48:1

		AccentSubtle: rgba(0x0E, 0x3A, 0x47, 255), // 选中行 / 菜单高亮

		StateHover:       rgba(0x1C, 0x26, 0x37, 255),
		StatePress:       rgba(0x23, 0x2E, 0x42, 255),
		StateSelected:    rgba(0x0E, 0x3A, 0x47, 255), // = accentSubtle
		StateRowHover:    rgba(0x1A, 0x24, 0x36, 255),
		ScrollThumbHover: rgba(0x6B, 0x7A, 0x93, 255),
		Scrim:            rgba(0x00, 0x00, 0x00, 0x8C), // 55% 黑（与亮色同值：遮罩是"压暗"，不随主题反向）
	}
}
