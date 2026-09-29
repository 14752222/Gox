# 设计 Token 与主题系统

Gox 内置组件的缺省外观由一组**命名颜色 token** 驱动（T07）。换主题 = 换一组
token 值；改 token 全局生效；暗/亮切换**不重启**（下一帧生效）。

本文是 token 表的权威来源。token 的 Go 侧定义在 `gfx/theme.go`
（`Theme` 结构 + `themeLight()` / `themeDark()` 两个预设）。

## 三条主题入口（优先级从高到低）

| 入口 | 时机 | 写法 |
|------|------|------|
| 脚本调用 | 运行中任意时刻 | `setTheme("dark")` / `setTheme({accent: "#e67e22"})` / `toggleDark()` |
| `<window>` prop | `render()` 挂载前 | `<window theme="dark" title="...">` |
| gox.json | `gx/theme` 模块首次被 import 时 | `"theme": "dark"` |

- `<window>` prop 让**首帧**就是目标主题（不会"先亮一帧再变暗"）；
- gox.json 的值在 `import { ... } from "gx/theme"` 时应用（与 `update` 模块
  读 `version` 同款模式），脚本随后显式 `setTheme` 可覆盖它；
- `gox.json` 里 `theme` 只认 `"light"` / `"dark"`，别的值在 `gox dev` 启动时
  直接报错（拦错别字）。

## JS API（`gx/theme` 模块）

```js
import { setTheme, toggleDark, current } from "gx/theme";

setTheme("dark");                    // 预设名；未知名返回 false
setTheme({ accent: "#e67e22", btnFace: "#abcdef" });  // 在当前主题上覆盖若干 token
                                     // → 返回实际生效的个数；主题名变为 "custom"
toggleDark();                        // 亮 ↔ 暗（自定义状态下按最近预设名的反面切）
current();                           // 读当前 token 表 { token: "#rrggbbaa", ... }
```

## 优先级纪律：props 永远赢

主题改的是**组件缺省色**。脚本显式写的 `background`、`color` 等 props 不受
主题切换影响 —— 切暗色不会把用户自己的配色改掉。想让某块界面跟随主题，
就不要给它写死颜色。

## Token 表（32 项，亮 / 暗预设）

| Token | 作用 | Light | Dark |
|-------|------|-------|------|
| `accent` | 选中/进度填充（强调色） | `#27ae60ff` | `#2fbf6bff` |
| `accentText` | 强调色上的前景（勾/圆点） | `#ffffffff` | `#ffffffff` |
| `fieldEdge` | checkbox/radio/switch 边框 | `#555555ff` | `#888888ff` |
| `btnFace` | button 缺省底色 | `#e8e8e8ff` | `#2d2d2dff` |
| `btnEdge` | button 缺省边框 | `#999999ff` | `#555555ff` |
| `track` | 轨道/分隔线 | `#d0d0d0ff` | `#3a3a3aff` |
| `switchOff` | switch 关闭态轨道 | `#c8c8c8ff` | `#4a4a4aff` |
| `knob` | switch 滑块 | `#ffffffff` | `#ffffffff` |
| `text` | 缺省文字色 | `#1a1a1aff` | `#eaeaeaff` |
| `focusRing` | 焦点虚线框 | `#1a5fb4ff` | `#4d8fe8ff` |
| `fieldFace` | 输入类字段底色 | `#ffffffff` | `#1e1e1eff` |
| `fieldHover` | 字段悬停底 | `#f0f4f9ff` | `#252b33ff` |
| `fieldPress` | 字段按压底 | `#e0e9f4ff` | `#2b333dff` |
| `inputEdge` | 输入类字段边框 | `#999999ff` | `#555555ff` |
| `placeholder` | placeholder 灰字 | `#999999ff` | `#777777ff` |
| `scrollTrack` | 滚动条轨道（半透明） | `#00000014` | `#ffffff14` |
| `scrollThumb` | 滚动条滑块 | `#a0a0a0ff` | `#5a5a5aff` |
| `optionActive` | 下拉项高亮底 | `#dce8f8ff` | `#2a3b52ff` |
| `popupFace` | 弹层/卡片底色 | `#ffffffff` | `#252525ff` |
| `popupEdge` | 弹层/卡片边框 | `#999999ff` | `#555555ff` |
| `mask` | modal 遮罩 | `#00000066` | `#00000066` |
| `info` | toast info | `#2f80edff` | `#4a9df0ff` |
| `warn` | toast warn | `#e8890cff` | `#f0a030ff` |
| `danger` | toast error | `#c0392bff` | `#e05545ff` |
| `tooltipFace` | 提示弹层底色 | `#262626f2` | `#333333f2` |
| `tooltipText` | 提示弹层文字 | `#f5f5f5ff` | `#f0f0f0ff` |
| `menuBarFace` | 菜单栏底色 | `#f3f3f3ff` | `#232323ff` |
| `menuBarEdge` | 菜单栏底边线 | `#d0d0d0ff` | `#3a3a3aff` |
| `menuActive` | 展开中的菜单标题底 | `#dce8f8ff` | `#2a3b52ff` |
| `menuHighlight` | 菜单项高亮底 | `#dce8f8ff` | `#2a3b52ff` |
| `menuShortcut` | 快捷键文字 | `#777777ff` | `#909090ff` |
| `menuSep` | 菜单分隔线 | `#d0d0d0ff` | `#3a3a3aff` |

颜色写法与 props 同一套：`#rgb` / `#rgba` / `#rrggbb` / `#rrggbbaa` /
`rgb(r,g,b)` / `rgba(r,g,b,a)` / 命名色（见 `gfx/raster.go` 的 `namedColors`）。

## 覆盖范围说明（v1 边界）

- **间距 / 圆角 / 阴影不是 token**：圆角 (`radius`)、间距 (`gap`)、留白
  (`margin`) 由组件 props 逐个控制 —— 它们没有"全局缺省值"可讲（列表的
  gap 和表单的 gap 语义不同），做成 token 会出现"改了不生效"的陷阱。
- **字号不是 token**：缺省字号由 `defaultFontSize()`（16dp 语义）兜底，
  字体仍由 `fontSize` / `fontWeight` props 控制。
- 新增 token 的步骤：`Theme` 结构加字段 → 两个预设各补一行 →
  `syncThemeVars` / `setToken` / `themeTokens` 三处登记（表驱动，漏一处
  `current()` 快照就少一个键）→ 本文档表格加一行。

## Go 侧（嵌入 gfx 的宿主程序）

```go
gfx.SetThemeNamed("dark")          // 预设切换
t := gfx.CurrentTheme()            // 读当前主题（副本）
gfx.ApplyOverrides(map[string]string{"accent": "#e67e22"})
```

`SetTheme` 当场把 token 同步回组件绘制的色变量并给所有存活窗口整帧标脏，
下一帧即新主题 —— 不需要重建窗口或重启 VM。
