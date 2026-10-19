# Prism 设计系统

> 一句话：**Prism 是 Gox 内置组件的一套「看得见的规矩」** —— 颜色走 token、
> 尺寸走 dp、动效走三档时长、触控有 44px 下限。本文是这套规矩的门面：
> 组件该多大、该用什么色、为什么这么定，以及**从哪份代码里读出来的**。

Prism 分八批落地（S1 token 层 → S2 色板 → S3/S4 基础控件 → S5 集合 → S6 浮层 →
S7 反馈 → S8 自适应收口）。本文写于 S8，覆盖全部八批的最终口径。

相关文档：

| 文档 | 内容 |
| --- | --- |
| [theme.md](theme.md) | v1 的 33 色 token 表与 `gx/theme` 的 API 细节 |
| [accessibility.md](accessibility.md) §9 | WCAG 对比度实测表（37 条配对 × 4 套预设） |
| [multi-window.md](multi-window.md) | `gx/viewport` 的断点 API 全表 |

---

## 0. 这份文档怎么用

### 0.1 数值不是手写的

**本文所有 token 数值都由代码 dump 出来**，不去手抄 —— 手抄的表第一次就可能错，
之后每次改代码必然漂移，而这是设计系统的门面。

生成器是 `gfx/prism_tokens_test.go`（它同时是一条 CI 断言，见 §0.2）：

```bash
# 打印当前默认主题的表
GOX_PRISM_DUMP=1 go test ./gfx/ -run TestPrismTokenTables -count=1 -v

# 打印指定预设（light / dark / prism-light / prism-dark）
GOX_PRISM_DUMP=1 GOX_PRISM_THEME=prism-light go test ./gfx/ -run TestPrismTokenTables -count=1 -v
```

改了 token 之后 §2 的表应当按上面的输出重贴一遍。**表里的值变了而 §2.1 没更新 =
文档过期**，与 CI 红同类。

### 0.2 三条把"文档不会漂移"钉住的断言

`TestPrismTokenTables` 平时跑的是这几条：

| 断言 | 为什么是它 |
| --- | --- |
| 颜色 token 数 == 61 | 60 个语义色 + `warn`（`warning` 的 v1 别名）。往 `Theme` 加字段却没写进分组表 ⇒ CI 红 |
| `warn` 与 `warning` 同值 | 别名漂移的症状是"老脚本与新组件画出两种橙色"，看一眼代码发现不了 |
| 每个 key 都有非空值 | "表里有一行空值"是这类文档最常见的失效形态 |
| Space / Radius 在 1x 恒等 | 桌面 1x 逐像素不变，是全部既有 golden 的前提 |

### 0.3 单位口径（最重要的一条）

| 类别 | 单位 | 换算 |
| --- | --- | --- |
| 脚本写出来的尺寸（`width` / `padding` / `font` …） | 设计像素 dp | `density.go` 的 `dpToPx` 按窗口所在屏的 `Display.Scale` 换算 |
| `Space()` / `Radius()` 的输入 | 设计像素 dp | 同上 |
| `TouchMin` | **物理像素** | 不换算（3x 屏上再乘一次会得到 132px 的荒谬命中区） |
| Motion | 毫秒 | 不换算 |

`Scale == 1`（桌面 x11/win32/cocoa）时 `dpToPx` 走 `int()` 截断的恒等快路径 ——
**桌面 1x 的渲染结果与引入换算之前逐像素一致**，这是全部既有截图/像素断言的前提。

---

## 1. 设计原则

1. **层次靠明度差，不靠阴影**。Gox 是 CPU 软光栅，没有实时模糊、也不做大面
   积 blur；所以"谁在上面"由五级 surface 的明度阶梯 + 发丝边 + 紧错阴影表达 ——
   *不同深度的东西必须有不同的亮度*，而不是不同的阴影面积。
2. **语义与装饰分开。**`BorderControl`（受 WCAG 1.4.11 约束，必须 ≥3:1）与
   `BorderDecorative`（装饰线，刻意低于 3:1）是两个 token。用一个值的必然结局是
   两头不讨好：要么装饰线把页面切成一堆框，要么控件边界淡到看不见。
3. **尺度要么走 token，要么走 dp，不允许裸数字。** 这是 S8 修掉的一批真 bug
   （见 §4.1）：凡是"盒子里要装文字"的尺寸，不与文字同比例缩放的结果都不是
   "小一点"，而是"字被裁掉"。
4. **视觉尺寸与命中尺寸是两件事**。桌面追求密度（32px 行、36px 字段），
   手指要求 44px。解法不是二选一，而是**视觉不变、命中区按需补**（§4.3）。
5. **降级不靠调用点自觉**。reduced-motion 的唯一入口是 `MotionDuration()`，
   触摸命中区的唯一入口是 `TouchHitRect()`。散在各组件里的 `if` 一定会漏掉
   一两处 —— 而漏掉的那一处正是用户会遇到的一处。

---

## 2. Token 总表

### 2.1 颜色（61 个 key = 60 语义色 + 1 个别名）

`warn` 是 v1 时代的旧名，与 `warning` 同值；新代码请用 `warning`。
带后两位的是 alpha（`ff` 已省略）：`tooltipFace` 是 95% 不透明的浮层底，
`selection` / `scrim` / `mask` / `scrollTrack` 需要与非实色背景合成后再算对比度。

<!-- 以下表格由 GOX_PRISM_DUMP=1 GOX_PRISM_THEME=prism-{light,dark} go test ./gfx/ -run TestPrismTokenTables 生成 -->

| token | prism-light | prism-dark |
| --- | --- | --- |
| **surface（表面）** | | |
| `surfaceBase` | `#f4f7fb` | `#131c2e` |
| `surfaceSunken` | `#e2e8f0` | `#0a111c` |
| `surfaceRaised` | `#f9fbfd` | `#1c2637` |
| `surfaceOverlay` | `#ffffff` | `#232e42` |
| `surfaceInset` | `#e9eef4` | `#0e1626` |
| `fieldFace` | `#ffffff` | `#0e1626` |
| `btnFace` | `#ffffff` | `#1c2637` |
| `popupFace` | `#ffffff` | `#232e42` |
| `tooltipFace` | `#0f172af2` | `#0b1220f2` |
| `menuBarFace` | `#e9eef4` | `#16203a` |
| `fieldHover` | `#f1f5f9` | `#16203a` |
| `fieldPress` | `#e2e8f0` | `#1c2637` |
| **text（文字）** | | |
| `text` | `#0f172a` | `#e6edf5` |
| `textSecondary` | `#334155` | `#b6c2d4` |
| `textMuted` | `#4a5768` | `#94a3b8` |
| `textDisabled` | `#94a3b8` | `#64748b` |
| `textInverse` | `#ffffff` | `#042a33` |
| `placeholder` | `#5e7089` | `#78879f` |
| `tooltipText` | `#f8fafc` | `#f1f5f9` |
| `menuShortcut` | `#4a5768` | `#94a3b8` |
| `accentText` | `#ffffff` | `#042a33` |
| `infoText` | `#1d4ed8` | `#93c5fd` |
| `successText` | `#166534` | `#86efac` |
| `warningText` | `#92400e` | `#fcd34d` |
| `dangerText` | `#991b1b` | `#fca5a5` |
| **border / edge（边界）** | | |
| `borderControl` | `#7a899d` | `#64748b` |
| `borderDecorative` | `#e2e8f0` | `#2a3547` |
| `inputEdge` | `#7a899d` | `#64748b` |
| `btnEdge` | `#7a899d` | `#64748b` |
| `popupEdge` | `#cbd5e1` | `#3a465c` |
| `menuBarEdge` | `#cbd5e1` | `#2a3547` |
| `menuSep` | `#e2e8f0` | `#2a3547` |
| `fieldEdge` | `#7a899d` | `#64748b` |
| **accent（强调）** | | |
| `accent` | `#0e7490` | `#22d3ee` |
| `accentSubtle` | `#cffafe` | `#0e3a47` |
| `accentText` | `#ffffff` | `#042a33` |
| `focusRing` | `#2563eb` | `#22d3ee` |
| `selection` | `#67c9e399` | `#0e749099` |
| `knob` | `#ffffff` | `#e6edf5` |
| **semantic（语义四件套）** | | |
| `info` | `#2563eb` | `#60a5fa` |
| `infoSubtle` | `#dbeafe` | `#10243c` |
| `infoText` | `#1d4ed8` | `#93c5fd` |
| `success` | `#15803d` | `#4ade80` |
| `successSubtle` | `#bbf7d0` | `#122a1e` |
| `successText` | `#166534` | `#86efac` |
| `warning` | `#b45309` | `#fbbf24` |
| `warningSubtle` | `#fef3c7` | `#2e2208` |
| `warningText` | `#92400e` | `#fcd34d` |
| `danger` | `#b91c1c` | `#f87171` |
| `dangerSubtle` | `#fee2e2` | `#3a1614` |
| `dangerText` | `#991b1b` | `#fca5a5` |
| `warn`（=`warning`） | `#b45309` | `#fbbf24` |
| **state（运行时状态）** | | |
| `stateHover` | `#f1f5f9` | `#1c2637` |
| `statePress` | `#e2e8f0` | `#232e42` |
| `stateSelected` | `#cffafe` | `#0e3a47` |
| `stateRowHover` | `#edf2f8` | `#1a2436` |
| `menuActive` | `#cffafe` | `#0e3a47` |
| `menuHighlight` | `#cffafe` | `#0e3a47` |
| `optionActive` | `#cffafe` | `#0e3a47` |
| **scroll / scrim（滚动与遮罩）** | | |
| `scrollTrack` | `#0f172a14` | `#ffffff14` |
| `scrollThumb` | `#94a3b8` | `#55637a` |
| `scrollThumbHover` | `#64748b` | `#6b7a93` |
| `track` | `#cbd5e1` | `#2a3547` |
| `switchOff` | `#94a3b8` | `#3d4a5e` |
| `scrim` | `#0000008c` | `#0000008c` |
| `mask` | `#0f172a66` | `#00000080` |

四条取舍记录（每一条都是为了过阈值而**改色**，不是放宽阈值）：

- **暗色 `accentText` 取深字 `#042a33`**（8.40:1）。代价：Prism 暗色里"强调按钮上的字"
  是深色的，与亮色相反 —— 写死"强调按钮放白字"的组件必须读 `accentText`，不能读
  `textInverse`。
- **亮色 `placeholder` 压深到 `#5e7089`**：`#64748b` 在纯白上 4.76:1 达标，但
  prism-light 的页底是 `#f4f7fb`（不是纯白），在其上只有 4.43:1。
- **暗色 `borderControl` 提到 `#64748b`**：`#556579` 在暗页底上只有 2.86:1，过不了
  WCAG 1.4.11。
- **没有 `accentHover` 字段**（S1 冻结了字段集）。强调色控件的 hover/按压档以常量
  形式留在 `gfx/theme_prism.go`（亮 `#0b5f77`、暗 `#67e8f9`）。

### 2.2 间距（4pt 基准，两个半档）

`Space1..Space10`。**6 与 20 是两个半档**，用在"比 4 大、比 8 小"的紧凑场景
（badge 内边距、图标与文字间隙）。

| token | dp | 1x px | 2x px | 3x px |
| --- | --- | --- | --- | --- |
| `Space0` | 0 | 0 | 0 | 0 |
| `Space1` | 2 | 2 | 4 | 6 |
| `Space2` | 4 | 4 | 8 | 12 |
| `Space3` | 6 | 6 | 12 | 18 |
| `Space4` | 8 | 8 | 16 | 24 |
| `Space5` | 12 | 12 | 24 | 36 |
| `Space6` | 16 | 16 | 32 | 48 |
| `Space7` | 20 | 20 | 40 | 60 |
| `Space8` | 24 | 24 | 48 | 72 |
| `Space9` | 32 | 32 | 64 | 96 |
| `Space10` | 40 | 40 | 80 | 120 |

### 2.3 圆角

| token | dp | 1x px | 2x px |
| --- | --- | --- | --- |
| `RadiusNone` | 0 | 0 | 0 |
| `RadiusXS` | 4 | 4 | 8 |
| `RadiusSM` | 6 | 6 | 12 |
| `RadiusMD` | 8 | 8 | 16 |
| `RadiusLG` | 12 | 12 | 24 |
| `RadiusXL` | 16 | 16 | 32 |
| `RadiusFull` | — | `-1`（哨兵，绘制侧按 `min(w,h)/2` 算） | `-1` |

`RadiusFull` 为什么是个负数：胶囊形在未知尺寸下无法用固定数值表达，而胶囊形正是
tag / chip / avatar / 开关轨道的语义。`resolveRadiusPx()` 拿到盒子尺寸后才把它落成
具体像素。

组件缺省档位由 `defaultRadiusFor()` 给出（`gfx/decor.go`）：字段类与按钮 `RadiusMD`、
checkbox `RadiusXS`、switch / radio `RadiusFull`。脚本写 `radius` prop 覆盖，
写负数沿用同一个哨兵语义。

### 2.4 动效

| token | ms | 用在哪 |
| --- | --- | --- |
| `MotionFast` | 120 | 复选/radio 出场、hover 变色 |
| `MotionBase` | 180 | switch 位移、tabs 指示条 |
| `MotionSlow` | 260 | dialog 面板缩放、drawer 滑入、toast 入场 |

**reduced-motion 规则**：组件不许自己判断环境，一律走 `MotionDuration(ms)` 取时长 ——
它在 reduced-motion 时返回 `0`（瞬时完成）。读取优先级（`gfx/motion.go`）：

1. 显式开关 `SetReducedMotion(*bool)`；
2. 环境变量 `GOX_REDUCED_MOTION=1`（CI 可测，打包后仍可用）；
3. `gox.json` 的 `"ui": {"reducedMotion": true}`；
4. 宿主平台探测（当前各后端都还没接，**保守返回 false**：宁可不降级，也不误判成降级）。

S7 的三处循环动画（spinner / skeleton / progress-indeterminate）在 reduced-motion 下
除了冻结相位，**还不再登记心跳** —— 静止画面不该为 16ms 的心跳买单。

### 2.5 触控

| token | 值 | 口径 |
| --- | --- | --- |
| `TouchMin` | 44 | **物理像素**，不随 `Display.Scale` 放大 |

44 是 Apple HIG / Material 的共同下限，也是 WCAG 2.1 SC 2.5.5 (AAA) 的数值。
它是硬指标 —— 但**不是"所有控件都画成 44"**，见 §4.3。

### 2.6 脚本侧怎么读

```js
import { current, setTheme } from "gx/theme";

const t = current();      // { text: "#0f172aff", surfaceBase: "#f4f7fbff", … }
// 尺度 token 也在同一张表里（且不接受覆盖 —— 尺度是全系统常量）:
t.space4, t.space8, t.space16, t.radius8, t.motionBase, t.touchMin
```

Go 侧：`gfx.ThemeTokens()`（map）、`gfx.CurrentTheme()`、`gfx.SetThemeNamed("prism-dark")`。

---

## 3. 组件规范

**约定**：下表 `dp` 列不标的都是设计像素；"行高/高"指组件自己给的缺省值（脚本显式写
`height` 永远赢）。所有常量出处逐项列"定义"，便于核对。

### 3.1 基础控件（S3/S4）

| 组件 | 尺寸（缺省） | 圆角 | 颜色 | 定义 |
| --- | --- | --- | --- | --- |
| `button` | 高 ≥36 dp；左右 16、上下 10 dp | `RadiusMD` | `btnFace` / `btnEdge` / `text` | `node.go:1155` `buttonH` |
| `input` `search` | 高 36 dp，缺省宽 160 dp，左右留白 12 dp | `RadiusMD` | `fieldFace` / `inputEdge` / `text` / `placeholder` / `selection` | `select.go:38` `fieldRowH` |
| `select` | 高 36 dp，箭头列 18 dp，最小宽 80 dp | `RadiusMD` | 同上 | `layout.go:514` |
| `select-option` | 行高 28 dp | — | `optionActive` / `text` | `select.go:39` `selectRowH` |
| `textarea` | 行数 × 行高 + 上下留白 | `RadiusMD` | 同 input | `layout.go:624` |
| `checkbox` | 18×18 dp | `RadiusXS` | `fieldEdge` / `accent` / `accentText` | `layout.go:461` |
| `radio` | 18×18 dp | `RadiusFull`（圆） | 同上 | 同上 |
| `switch` | 44×26 dp 胶囊轨道，滑块 18 dp | `RadiusFull` | `switchOff`→`accent` 插值 / `knob` | `layout.go:470`、`raster.go:747` |
| `slider` | 缺省 160×26 dp；轨道高 6、滑块 18×18 dp | 轨道胶囊 | `track` / `accent` / `knob` | `slider.go:34` |
| `progress` | 缺省宽 200 dp，轨道高 6 dp | `RadiusFull` | `track` / 语义色 | `layout.go:483` |
| 焦点环 | 2px 线宽 + 2px 底色间隙，**跟随控件圆角** | — | `focusRing` | `decor.go:289` |

**"双 ring"指的是什么**：字段类控件获焦时，它自己的边框转成 `focusRing` 色（内 ring，
见 `input.go:286` 的 `fieldEdge`），外侧再留 2px 底色间隙画一圈 2px 实线（外 ring）。中间那圈底色让
焦点环在 `surfaceBase` 上、在与 `#ffffff` 同色的字段上都看得见 —— 单 ring 贴着控件
画时，遇到同色背景会整条消失。

### 3.2 集合组件（S5）

| 组件 | 行高 | 其他几何 | 颜色 |
| --- | --- | --- | --- |
| `table` | 32 dp | 网格线 1px、选中指示条 3 dp、单元格左右留白 12 dp | `surfaceSunken`（表头）/ `borderDecorative` / `stateRowHover` / `stateSelected` / `accent` |
| `list-item` | 32 dp | 左右留白 12 dp、上下 Space2、选中条 3 dp | 同上 + `borderDecorative` 行底线 |
| `tree` | 32 dp | 每层缩进 16 dp、箭头列 16 dp、下限宽 120 dp | 同上 |
| `tabs` | 条高 = 行高 + 上下 6 dp | 标签左右 12 dp、标签间距 2 dp、激活指示条 2px | `borderDecorative` / `textSecondary` / `text` / `accent` |
| `pagination` | 按钮 32×32 dp | 间距 4 dp、省略号占位 32 dp | `accent` + `accentText`（当前页）/ `surfaceRaised` / `borderDecorative` |

三个集合组件的**行高必须同值**（table / list / tree 都是 32dp）—— 混排时才能对齐；
而它比字段行高 36 矮一档是刻意的：表格是密集阅读场景。

### 3.3 浮层组件（S6）

| 组件 | 几何 | 圆角 | 颜色 |
| --- | --- | --- | --- |
| `menu-popup` | 项行高 32 dp、分隔 9 dp、内边距 6 dp、下限宽 120 dp | `RadiusLG`（面板）/ `RadiusSM`（项高亮） | `popupFace` / `popupEdge` / `stateHover` / `menuHighlight` / `textDisabled` |
| `menubar` | 高 26 dp | — | `menuBarFace` / `menuBarEdge` / `menuActive` |
| `tooltip` | 触发盒外 6 dp、水平留白 6、垂直 4、最大宽 260 dp、延迟 500ms | `RadiusMD` | `tooltipFace`（95% 不透明）/ `tooltipText` |
| `dialog` | 面板居中，配 `scrim` 遮罩 | `RadiusXL` | `surfaceOverlay` / `popupEdge` / `scrim` |
| `drawer` | 缺省宽 280 dp、滑入 `MotionSlow` | `RadiusLG` | 同上 |
| `toast` | 最小高 36 dp、左侧色条 4 dp | — | `surfaceOverlay` / 语义色 |
| `alert` | 左右 8、上下 6 dp，左侧 level 条 3 dp，图标 14 dp，间距 8 dp | `RadiusMD` | `*Subtle` 底 + `*Text` 字（四档 level 同构） |

### 3.4 反馈与展示（S7）

| 组件 | 几何 | 颜色 / 动效 |
| --- | --- | --- |
| `spinner` | 缺省直径 24（外半径 12、环宽 3、内半径 9） | 底环 `surfaceInset`，90° 的 `accent` 弧旋转 900ms |
| `skeleton` | 圆角 `RadiusMD`，文字行高 14、间距 8 | 在 `track` 与提亮 28 的灰之间呼吸，周期 1200ms |
| `progress` indeterminate | 轨道内 1/3 宽的色块循环，周期 1200ms | `clipTo` 裁到胶囊轨道内 |
| `avatar` | 24 / 32 / 40 三档，圆形（`RadiusFull`）；`shape` 可切 `square`（`RadiusSM`）/ `rounded`（`RadiusMD`） | 文字版 `accentSubtle` 底 + 语义文字色；状态点带一圈底色挖空环 |
| `badge` | 圆点 8；数字胶囊左右留白 4、最小宽 16、字号 11；出框 4/4、抠边环 2 | `danger` 底 + `ContrastOn` 取前景色 |
| `tag` | 高约 22（默认字号下），左右留白 4 | `RadiusSM`；五色（default/success/info/warning/danger）统一 `*Subtle` 底 + `*Text` 字；关闭叉 10 dp |
| `separator` | 1px；`inset` 取 Space 台阶号 | `borderDecorative` |
| `empty` | 内部垂直居中，缺省内边距 16 dp | 描述 `textSecondary`、副描述 `textMuted` |

---

## 4. 响应式自适应（S8）

三层，各管一件事：**密度**管物理尺寸、**断点**管布局形态、**触摸模式**管命中区。

### 4.1 密度：所有"装文字的盒子"必须与文字同比例

机制见 `gfx/density.go`：脚本写出的尺寸按 dp 解释，按**窗口所在屏**的 `Scale` 换算；
缓存在每帧 `Layout` 开头刷新一次（换屏/改缩放下一帧生效）。

**S8 修掉的真 bug**：一批内核自己的度量常量是裸物理像素，不参与换算 —— 于是 2x 屏上
出现了"行还是 32px，行里的字已经按 dpToPx 放大到 32px"的荒谬比例（上下留白归零、
中文下缘被裁）。修的是这些：

| 常量 | 值（dp） | 位置 |
| --- | --- | --- |
| `fieldRowH` / `selectRowH` / `fieldPadX` / `selectArrowW` / `selectMinW` / `inputMinW` | 36 / 28 / 12 / 18 / 80 / 160 | `select.go:38`、`input.go:34` |
| `tableRowH` / `tableRowBarW` | 32 / 3 | `table.go:49` |
| `listRowH` | 32 | `list.go:41` |
| `treeRowH` / `treeArrowW` / `treeMinW` | 32 / 16 / 120 | `tree.go:45` |
| `menuItemH` / `menuSepH` / `menuPadY` / `menuMinW` / `menuItemPadX` / `menuShortcutGap` | 32 / 9 / 6 / 120 / 12 / 24 | `menu.go:59` |
| `pageBtnSize` / `pageBtnGap` | 32 / 4 | `pagination.go:31` |
| `sliderDefW/H` `sliderTrackH` `sliderKnobW/H` | 160 / 26 / 6 / 18 | `slider.go:34` |
| checkbox / radio / switch 视觉尺寸 | 18 / 44×26 | `layout.go:461` |
| `toastMargin` | 16 | `overlay.go:20` |
| `tipPadX` / `tipPadY` / `tipMaxW` | 6 / 4 / 260 | `tooltip.go:41` |
| `drawerDefaultW` | 280 | `drawer.go:36` |
| `alertIconW` / `badgeMinW` | 14 / 16 | `feedback.go:28` |
| `avatarDefault` / `avatarStatusD` | 32 / 10 | `feedback.go:47` |
| `emptyIconSize` | 44 | `feedback.go:51` |
| `toastAccentW` / toast 卡片内边距 / 最小高 | 4 / 16 / 36 | `overlay.go:23`、`layout.go:584` |
| `tipGap` / `tipWinMargin` / `tipFlipGap` | 6 / 4 / 8 | `tooltip.go:41` |
| `dpFieldIconW` / `dpFieldMinW` / `dpPopupPad` / `dpHeadH` / `dpWeekH` / `dpCellW×H` / `dpNavW` / `dpReadoutH` | 14 / 130 / 8 / 26 / 20 / 28×24 / 22 / 20 | `datepicker.go:43` |
| `cpSwatchSize` / `cpFieldMinW` / `cpPopupPad` / `cpCellSize` / `cpCellGap` / `cpReadoutH` | 14 / 110 / 8 / 22 / 4 / 20 | `colorpicker.go:38` |
| `alertPadX` / `alertPadY` / `alertAccentW` / `alertGap` | 8 / 6 / 3 / 8 | `feedback.go:25` |
| `tagPadX` / `tagPadY` / `tagGap` / `tagCloseSize` | 4 / 1 / 4 / 10 | `feedback.go:41` |
| `badgePadX` / `badgeDotD` / `badgeOverX,Y` / `badgeRingW` / `badgeFontM` | 4 / 8 / 4 / 2 / 11 | `feedback.go:32` |
| `menuBarH` / `menuTitlePadX` / `menuPadY` | 26 / 10 / 6 | `menu.go:59`、`layout.go:717` |
| `searchIconW` / `searchIconGap` | 14 / 4 | `search.go:24` |
| `textareaPadX` / `textareaPadY` / `textareaMinW` | 12 / 8 / 240 | `textarea.go:37`、`layout.go:632` |
| `upFieldIconW` / `upFieldMinW` / `upDashedGap` | 14 / 180 / 4 | `upload.go:24` |
| `treeIndent` / `arrowGap`（测量侧的 `chevronInset` 等已换算） | 16 / 2 | `tree.go:47` |
| `listSelectBarW` | 3 | `list.go:38` |
| `scrollTrackW` / `scrollMinThumb` / `scrollDefH` | 8 / 24 / 200 | `scroll.go:31`、`layout.go:630` |
| `skeletonRowH` / `skeletonGap` / `skeletonAvatarSize` / 缺省宽 | 14 / 8 / 36 / 200 | `loading.go:43` |
| `ratingCellSize` | 20 | `rating.go:36` |
| `iconDefaultSize` | 16 | `icon.go:28` |
| 媒体兜底缺省 `imgPlaceholderW,H` / `videoDefaultW,H` / `audioDefaultW,H` / `canvasDefW,H` | 320×240 / 320×180 / 300×54 / 300×150 | `image.go:38`、`video.go:59`、`audio.go:61`、`canvas.go:44` |

两个 picker 的网格另外各抽一个"中间层”——`dateMetricsOf()`（`datepicker.go:495`）与
`cpMetricsOf()`（`colorpicker.go:56`）——把一组 dp 常量一次性换成物理像素。**为什么不
在各用点现取**：日历/色板的同一组数字喂四处（固有尺寸、布局、绘制、`dx/step` 的命中
反算），一处漏换算的表现不是"格子小一点"，而是"点在 3 号选中了 10 号"。

同一批里另有三处**真正的读数 bug**（不是漏换算，而是"同一条信息两条口径"）：
`avatarSize()`、`spinnerSize()`、`iconSize()` 都把脚本写的 `size` / `width` /
`height` 当物理像素返回，而 prop 本身是逻辑值 —— 于是 2x 屏上写了 `size={48}`
的头像 / spinner / 图标，反而不写 size 的（缺省 32dp→64px 等）大不了多少，写
了还不如不写。现在三处都与 `drawerWidth()` 同一口径走 `dpToPx` / `Space()`。

**这些改动在 1x 上逐像素不变**（`Space()` 在 `Scale<=1` 时是恒等），桌面全部既有
截图与像素断言都不受影响 —— 这也是敢一次性改这么多处的唯一理由。余下的裸常量见 §7。

验证：`gfx/adaptive_s8_test.go` 的

| 用例 | 盯的东西 |
| --- | --- |
| `TestSpaceTokensScaleWithDensity` / `TestRadiusTokensScaleWithDensity` | token 换算三档（1/2/3x），`RadiusFull` 哨兵不动 |
| `TestRowMetricsScaleWithDensity` | 集合/菜单行高在 2x 上应为 64，1x 回到 32 |
| `TestPopupMetricsScaleWithDensity` | 两个 picker 的弹层整体翻倍，且 1x 数字与常量注释一致 |
| `TestOverlayAndFeedbackMetricsScaleWithDensity` | toast / tooltip / 装饰尺度：比的是**扣掉文字后剩下的那一圈** —— 字形按物理像素取整，文字宽度并不随密度严格线性（实测 68→144 而非 136），把它拉进断言只会得到一条允许误差 ±8 的软断言 |

### 4.2 断点：已存在，就在 `gx/viewport`

**不需要新建** —— 断点系统随 M4 自适应断点布局落地，Go 侧在 `gfx/viewport.go:917` 起：

| API | 返回 |
| --- | --- |
| `breakpoints()` | 当前表 `{sm:0, md:600, lg:840, xl:1200}`（dp） |
| `breakpoint(win?)` | 当前档名，不订阅 |
| `useBreakpoint(win?)` | 取值函数 + 订阅，**必须无条件调用**（写在三元分支里会漏订阅） |
| `above(name)` / `below(name)` / `between(a,b)` | 区间判定（未知档名一律 false） |
| `matchBreakpoint({sm:…, lg:…})` | 命中档的值；一个都没命中返回 `undefined`（让"没覆盖到"显式暴露） |
| `setBreakpoints(table)` / `resetBreakpoints()` | 整表替换 / 复位（非法项静默跳过；全非法保持原表并告警） |

两套**阈值**的关系 —— 它们是同一件事的两种叫法，默认刻意复用同一组数字：

```
sm  (0..599)    ↔ compact
md  (600..839)  ↔ medium
lg  (840..1199) ┐
xl  (≥1200)     ┘ ↔ expanded（断点把 expanded 再切一刀，给超宽桌面留 xl）
```

- **尺寸类**（`widthClass()` 的 `compact`/`medium`/`expanded`，阈值 600/840dp）是内核对
  物理宽度档的**分类**，宿主报了就以宿主为准；
- **断点**是**命名阈值**，应用层的语言，面积随**窗口**宽度走（不是屏幕宽度 —— 分屏时
  窗口才是真相）。

一个已知差异（不是 bug）：`deriveSizeClasses` 把 840dp 判成 medium（闭区间），而断点表
`lg:840` 按"下界"语义把 840dp 归到 lg。要严格对齐就 `setBreakpoints({sm:0, md:600, lg:841})`。

resize 不经过 `ReportViewport`，宿主必须在 `WM_SIZE` / `windowDidResize` /
`ConfigureNotify` 里调 `gfx.NotifyViewportChanged()`（`gfx/viewport.go:1114`），否则
`useBreakpoint()` 不会重算。

### 4.3 触摸模式：视觉不变，命中区补到 44

**问题** §3 的几何对鼠标是对的，对手指不够：32px 的行、26px 的开关、页码按钮 32×32 都
低于 44px 触控下限。把控件普遍拉到 44 会毁掉桌面密度（S3 刚把字段行定在 36）。

**方案**：加一个"触摸模式"，只扩**命中区**，不动绘制尺寸。

```go
// Go 侧（宿主）
gfx.SetTouchMode(&on)      // 显式开关（优先级最高）；nil = 回落
gfx.TouchModeEnabled()     // 读当前状态
```
```bash
GOX_TOUCH_MODE=1 gox run .   # 环境变量（CI 与打包后仍可用）
```

默认值：**移动端 GOOS（android/ios/harmony）= 开，桌面 = 关**。桌面保持关是为了让既有
桌面应用的命中行为**逐像素不变**（`TestTouchModeDefaultOffOnDesktop` 盯着这条）。

覆盖范围：

| 类别 | 行为 | 理由 |
| --- | --- | --- |
| `checkbox` / `radio`（18×18） | **无条件**补到 44 | S4 就发出去的既有行为；18 连鼠标都嫌小 |
| `button` / `switch` / `slider` / `tag` / `icon` / `avatar` / 字段类 / picker | 触摸模式下两轴都补 | 26~36 高，鼠标够、手指不够 |
| `list-item` / `table-row` / `tree-row` / `menu-item` | 触摸模式下**只补垂直** | 行已撑满宽度，横向外扩落在父容器外，不增益却会让"点行外侧空白"误判成点行 |
| tabs 标签条、pagination 页码、alert/tag 关闭叉 | 组件自绘区走 `touchHitContains` | 它们是"画出来的矩形"，不进节点遍历，漏掉就等于漏掉半套 UI |
| 不带 `onClick` 的节点 | 不补 | 参与只会把 hover 归属搅乱，换不来任何手指能摸到的收益 |

三条纪律写在代码里并有断言盯着：

1. 扩展**不改 `Box`** —— 绘制与命中是两个数据，共用一个会让"行高"这类数被
   两种语义拉扯；
2. **以盒子的中心外扩**而不是从左上角长 —— 控件画在哪，命中区就围在哪；
3. 命中区**允许重叠**，重叠时按 z 序取后绘制者（与"谁在上面就点得到谁"同口径）。

**没做的（写在 `TODO(Prism)` 里）**：日历格 / 色块这类**密铺网格**。它们用
`(x-gridX)/cellW` 把整块区域映射到格子，本身没有缝隙可补；真正缺的是"格子该更大"，
那是单元格两档尺寸的事而不是命中层的事。

---

## 5. 主题切换

四套预设，**Prism 与 v1 并存而非替换**：

| 预设 | 定位 | 怎么选 |
| --- | --- | --- |
| `light` / `dark` | v1 legacy 33 色，**存量契约** | 不写在 gox.json 就是它（向后兼容） |
| `prism-light` / `prism-dark` | Prism 60 色，过 WCAG AA | 显式点名 |

```js
setTheme("prism-dark");        // 脚本
toggleDark();                  // 亮 ↔ 暗，**停留在同一家族内**
```
```go
gfx.SetThemeNamed("prism-dark")  // Go 侧
gfx.ToggleDark()                 // prism-dark ↔ prism-light（不会跳去 v1 的 light）
```
```json
{ "theme": "prism-dark" }        // gox.json（config 层的名单已放开四个名字）
```

`ToggleDark` 为什么"留在家族内"：如果用户只是想开个夜间模式，从 prism-dark 跳到
v1 的 dark 会让观感在"冷青 slate"与"绿色灰白"之间来回断裂。

**不改 v1 色值是刻意的**：那 33 个色值是存量应用的默认外观，改一个等于给所有已上线的
应用发一次它们没请求过的视觉更新。`light`/`dark` 有 33 条
不达标配对（清单见 [accessibility.md](accessibility.md) §9.5，**不修**）；要达标就迁到
Prism。

---

## 6. 可达性摘要

完整实测表与 anti-pattern 清单在 [accessibility.md](accessibility.md)。四条承诺：

| 主题 | 门槛 | 谁盯着 |
| --- | --- | --- |
| **对比度** | 正文 ≥4.5:1（WCAG 1.4.3 AA）、控件边界/图标 ≥3:1（1.4.11 AA） | `gfx/contrast_test.go`，37 条配对 × 4 套预设；`prism-*` 全绿，`light/dark` 只收集不失败 |
| **焦点环** | 双 ring（2px 环 + 2px 底缝）、跟随控件圆角、全部可聚焦控件都有 | `gfx/a11y.go` 的焦点注册表 + `TestContrastAlphaAwarePairs` 里的焦点环配对 |
| **reduced-motion** | 所有时长收敛到 `MotionDuration()`，归零即瞬时；循环动画同时停止登记心跳 | `gfx/motion_test.go` |
| **触控尺寸** | 触摸模式下所有交互控件的命中区 ≥44px（视觉尺寸不变） | `gfx/adaptive_s8_test.go` |

两条**明确不承诺**（别当 bug 报）：

- 装饰性分隔线（`borderDecorative`）不承诺 3:1 —— 够深就会把页面切成一堆框；
- 禁用态文字（`textDisabled`）不承诺 4.5:1 —— WCAG 1.4.3 豁免 inactive 控件。

---

## 7. 已知缺口（S8 收尾时未改）

| 缺口 | 现状 | 为什么没改 |
| --- | --- | --- |
| 度量常量的**使用点**还有 ~10 处没走 `Space()` | 全是刻意项：`tableBorderW`(1) 与 `focusRingW/Gap`(2+2) 是发丝尺度、`badgeRingW` 与 `pageBtnSize` 是别名声明、`spinnerRingW` 走的是与直径的比例、`menuPadY` 是当作 `padding` prop 传下去（prop 本身是 dp，下游会换算）、其余是注释里的名字 | S8 已把"参与排版的尺寸"全部换算掉（§4.1 的表加上了第三批：menu / search / textarea / upload / tree / list / scroll / skeleton / rating / icon / 四个媒体缺省）。剩下这十个里每一个换算了都会让 2x 上变**脏**：1px 网格线变 2px、2px 焦点环比控件还粗。逐条保留理由已写进各文件的注释 |
| 日历格 / 色块的触摸命中区 | `TODO(Prism)` 标在 `datepicker.go` / `colorpicker.go` | 密铺网格缺的是"格子更大"而不是命中区（§4.3 末） |
| 触摸模式的宿主接入 | 目前只到 GOOS 猜测，没有真设备上报通道 | 需要宿主先实现"输入设备能力"查询，与 reduced-motion 的平台探测是同一个待接插槽（`gfx/motion.go:130`） |
| `TouchMin` 之外的密度缺口：`displayScale` 不在 gox.json 可配 | 走 `Display.Scale`，宿主上报 | 属于宿主能力，不在 Prism 范围 |
