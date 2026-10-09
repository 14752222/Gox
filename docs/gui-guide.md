# Gox GUI 开发指南

用 JSX + 信号（signal）写桌面界面。本文是 `gx/gfx` 渲染层及其配套宿主模块的完整参考、
API 语义与已知取舍；上手最短路径见 [README 的 GUI 章节](../README.md#gui-桌面应用)。

- **适用前提**：Go 1.26+ 构建的 Gox（或 npm 安装的 `goxjs`）。
- **后端支持**：Windows（win32，纯 syscall、无 cgo）、Linux（X11，Wayland 下走 XWayland）与 macOS（cocoa，purego 桥接 Objective-C Runtime，无 cgo）。
- **渲染模型**：纯 Go 软件光栅化，无动态库依赖，产物为静态单文件。

## 目录

- [1. 快速上手](#1-快速上手)
- [2. 运行时行为与平台支持](#2-运行时行为与平台支持)
- [3. 事件模型](#3-事件模型)
- [4. 内置元素参考](#4-内置元素参考)
- [5. 布局](#5-布局)
- [6. 受控组件与文本输入](#6-受控组件与文本输入)
- [7. 绘制与动画](#7-绘制与动画)
- [8. 响应式与视图](#8-响应式与视图)
- [9. 宿主能力](#9-宿主能力)
- [10. 调试](#10-调试)
- [11. 示例索引](#11-示例索引)
- [12. 症状速查（窗口起不来 / 值不对时先看这里）](#12-症状速查窗口起不来--值不对时先看这里)
- [13. 相关文档](#13-相关文档)

## 1. 快速上手

从零新建一个工程用脚手架 —— 生成 `package.json` + `src/` 布局的默认工程，开箱即跑：

```bash
gox create my-app          # 同为 goxjs create / npx @goxjs/goxjs create
cd my-app && npm install && npm run dev
```

下面是最小骨架，直接存成一个 `.js` 文件也能跑（JSX 会被降级成 `h(...)` 调用，
所以**用了 JSX 的文件必须 import `h`**）：

模块导入有两种写法：聚合入口 `gox` 一行拿全常用 API（`gox` 是所有 `gx/*` 模块导出的并集，
应用代码推荐）；细分模块按需导入（库代码推荐）。

```js
import { h, render, createSignal } from "gox"        // 聚合入口（一行拿全）
// 等价的细分写法:
// import { createSignal } from "gx/solid"
// import { h, render } from "gx/gfx"

const [count, setCount] = createSignal(0)

render(
  <window title="Counter" width={400} height={300}>
    <column gap={8} padding={16}>
      <text font={20}>{() => `count: ${count()}`}</text>
      <button onClick={() => setCount(c => c + 1)}>加一</button>
    </column>
  </window>
)
```

```bash
./gox counter.js          # 直接运行，弹出 400x300 窗口
```

- 点击按钮 → `setCount` 更新信号 → 依赖该信号的属性 / 文本节点自动标脏 → 脏矩形合并后只重绘受影响区域。
- 窗口配置写在根元素上：JSX 里 `<window title width height>` 包住整棵树；`h()` 手拼树时
  `render(tree, {title, width, height})` 传普通对象，省略则用缺省（Gox 400x300）。
- `render()` 返回窗口句柄 `{close(), isClosed(), title(), setTitle(t), resize(w, h)}`，
  可**调用多次**开多窗口（详见 [9.5 多窗口](#95-多窗口)）。
- 未实现的标签（拼错的名字、或还没做进 `knownTags` 的名字）会在 stderr 打印一次性警告，
  并仍按普通盒子渲染（不再静默成空盒子）。

## 2. 运行时行为与平台支持

| 能力 | Windows（win32） | Linux（X11） | macOS（cocoa） | 说明 |
|---|---|---|---|---|
| 窗口 | 支持 | 支持（Wayland 走 XWayland） | 支持 | 多窗口见 [9.5](#95-多窗口)；macOS 上 `w.close()` 只解除注册不销毁平台窗口（与 win32 同语义） |
| 窗口管理<br>（位置 / 层级 / 约束 / 全屏 / 光标 / 模态） | 全部支持 | 除光标形状外全部支持 | 全部支持 | 见 [9.5 的"窗口管理"](#95-多窗口)；未实现的能力**静默降级**为默认行为，不报错 |
| 字体 | 静态候选路径 | 惰性扫描系统字体目录 | 静态候选优先 + 目录扫描 | Linux 扫 `/usr/share/fonts`、`~/.local/share/fonts` 等，**CJK 字体优先**，条目上限 2000；macOS 静态候选（PingFang / Hiragino Sans GB 等）排在扫描结果之前 |
| 输入法（IME） | 支持 | 暂不支持 | 支持（组合过程不在框内内联绘制） | 见 [6.2](#62-输入法-ime)；macOS 经 NSTextInputClient 协议（消息转发实现），焦点在编辑框上时开启 |
| 剪贴板 | 支持 | 暂不支持 | 支持（纯文本） | 见 [9.2](#92-剪贴板) |
| 原生对话框 | 支持 | 降级为写 stderr | 支持（NSAlert/NSOpenPanel/NSSavePanel，runModal 模态） | 见 [9.1](#91-原生系统对话框) |
| 菜单栏 / 右键菜单 | 支持 | 支持 | 支持 | gfx **自绘**，不依赖系统菜单 API，三平台观感一致；应用主菜单只有最小项（含 Cmd+Q） |
| 原生能力层<br>（`gx/device` · `app` · `geo` · `media` · `permission` · `viewport`） | 部分：电量 / 网络 / 亮度 / 屏幕常亮 / 打开系统设置页 / 震动（软降级）走宿主；相机 / 定位 / 相册 / 权限**诚实缺省** | 同 Windows 口径 | 同 Windows 口径 | 见 [9.6](#96-原生能力层)。**没有的能力就报没有** —— 缺能力时异步 API 报 `unsupported`（先用 `canIUse` 判断），不返回假数据 |

macOS 后端（cocoa）已知限制：

- **IME 组合过程不在框内内联绘制**（与 Windows 同口径：候选词上屏前由系统候选窗回显拼音，选定后整批提交）；正在组合时全部按键交给输入法（Enter 提交原串 / Esc 取消）。英文/符号键入与全部功能键经 `event.characters`/`keyCode` 直通，行为与旧版一致。
- **保存文件对话框已全通**：`gx/dialog` 的 `saveFile`（2026-10-01 补脚本侧入口）—— Windows 走 `GetSaveFileNameW` + 覆盖确认，macOS 走 `NSSavePanel`，取消返回 `null`。
- **多屏枚举已支持**（`gx/screen` 可见全部 NSScreen：frame/visibleFrame/缩放/主屏标记，ID 取 `NSScreenNumber` 稳定标识）；显示器插拔 / 分辨率变化 / 窗口跨屏会派发 `onDisplayChange`（2026-10-02 补齐：NSApplicationDidChangeScreenParametersNotification + NSWindowDidChangeScreenNotification → `gfx.Post`，与 win32 的 WM_DISPLAYCHANGE 同构）。
- 拖动（slider 等）在光标离开窗口后**仍然跟手**：AppKit 按住按键期间会持续投递 `mouseDragged:`，等价于天然鼠标捕获。

找不到可用字体时文字整体不渲染，错误里会给出候选条数与最后一个失败原因。

### 移动端宿主（Android / iOS / 鸿蒙）

上表三列是**桌面**后端。移动端不由 gfx 直接开窗口，而是由各平台的**壳工程**驱动同一份内核（node / layout / raster / font / 事件泵一行不改），界面逻辑仍然全部写在 JS 里：

| 平台 | 壳 | 现状 |
|---|---|---|
| Android | Kotlin（`SurfaceView` + `Choreographer`） | 六个能力模块基本齐备；模拟器 x86_64 / API 34 **实测通过**（上屏 / 触摸 / 软键盘 IME / 安全区 / 折叠屏） |
| iOS | Swift（`UIView` + `CADisplayLink`） | 六个能力模块大部分齐备（`exitApp` 报 `unsupported` —— iOS 不允许应用自杀）；壳工程与构建脚本（含 TestFlight）齐备，**真机验收待做** |
| 鸿蒙 | ArkTS（`PixelMap` + `onTouch`） | 只通了安全区与折叠上报两条**纯上报**通道，六个能力模块仍是桩；交叉编译 + HAP 构建通过，**设备上尚未实跑** |

移动端独有的三处消费点（都是响应式、桌面端自动退化）：安全区 `useInsets()`、软键盘避让 `useKeyboardHeight()`、断点 `widthClass()`；折叠屏半折时 `RouterView` 自动双栏。壳与内核的分工、JNI / NAPI 契约、逐方法（模块 × 平台）状态表与首帧自检清单见 [`app/NATIVE-HOST.md`](../app/NATIVE-HOST.md)；移动端打包前置（**需 Gox 源码仓库** + 平台工具链）与三平台构建步骤见官网教程 <https://14752222.github.io/Gox/guide/gui>（「跑在手机上」一节）。

## 3. 事件模型

| 事件 | 参数 | 分发规则 |
|---|---|---|
| `onClick` | 无 | 命中测试（最内层带 `onClick` 的节点），并把该节点设为键盘焦点 |
| `onMouseMove` | `{x, y}` | 光标下最深节点起沿祖先链找第一个处理器（不冒泡到根以外） |
| `onWheel` | `{deltaY}` | 光标所在 `scroll` 容器先消费（一格 60px，`Shift` 修饰走横向；纵向不可滚而内容超宽时横向兜底），容器两个方向都已到边界才继续冒泡；`deltaY` 沿用 DOM 约定（向下滚为正） |
| `onContextMenu` | `{x, y}` | 右键抬起时触发；常配合 `openContextMenu(e.x, e.y, items)` 弹右键菜单 |
| `onKeyDown` / `onKeyUp` | `{key, ctrl, shift, alt}` | 从焦点节点沿祖先链找第一个处理器 |
| `onFocus` / `onBlur` | 无 | 焦点切换时触发，沿祖先链找第一个处理器；焦点节点会画 1px 蓝色虚线框（根节点 `hideFocusRing` 可关闭） |
| `onLongPress` | `{x, y, duration}` | 按住不动到 `longPressDelay`（缺省 500ms）时触发；触发后抬起**不再派发 `onClick`**。详见 [3.2](#32-手势长按与滑动) |
| `onSwipe` | `{direction, dx, dy, distance, duration, x, y}` | 抬起时位移超过 `swipeThreshold`（缺省 40px）触发，`direction` 取主导轴（`left`/`right`/`up`/`down`）；触发后**不再派发 `onClick`**。详见 [3.2](#32-手势长按与滑动) |
| `onResize` | `{width, height}` | **窗口级**事件：窗口尺寸变化时派发给**布局根**（挂非根节点不触发），不走焦点链；尺寸为物理像素。拖窗口边缘或脚本调 `win.resize()` 都会触发 |
| `shortcut`（属性，非事件） | 回调收 `{x, y, shortcut}` | `<menuitem shortcut="Ctrl+S">`：不必展开菜单，快捷键表在事件泵层直接匹配。只认带 `Ctrl`/`Alt` 的组合，且修饰键**全等**（`Ctrl+S` 不会被 `Ctrl+Shift+S` 触发） |

> 交互组件（`button` / `checkbox` / `radio` / `switch`）自动获得悬停提亮（各通道 +12）与按压压暗（-24）反馈，
> 状态由渲染层维护，脚本无需（也无法）读写。`disabled` 的子树既不响应事件也不做交互反馈。
>
> 光标离开窗口 / 窗口失活会清除悬停与按压态。**`Tab` / `Shift+Tab` 焦点遍历、方向键导航与
> Enter/Space 激活已在无障碍层落地**（2026-10-02 起，见 [3.1](#31-焦点与键盘遍历无障碍) 与
> [accessibility.md](accessibility.md)）；焦点仍可以只用鼠标改，键盘是新增的一条路，不是替换。

### 3.1 焦点与键盘遍历（无障碍）

键盘是一条**与鼠标并列**的路，不是替代：点击仍然能改焦点，键盘只是多了一条"不碰鼠标
也能把事做完"的路。落地在 `gfx/a11y.go`（一层，全组件共用），完整规范、每个控件的键位表
与验收清单见 [accessibility.md](accessibility.md)。

- **`Tab` / `Shift+Tab`** 在遍历序里前进 / 后退，到两端**绕回**；遍历序为空时不消费这个键
  （按键照旧给脚本）。
- **谁进序**：标签表决定（`button` / `checkbox` / `radio` / `switch` / `slider` / `input` /
  `search` / `textarea` / `select` / `rating` / `tabs` / `pagination` / `datepicker` /
  `colorpicker` / `upload`），并可用 `focusable={true/false}` 覆盖；顺序按 `tabIndex`
  （`>0` 升序前置、`0` 树序、`<0` 出序但仍可 `focusNode()`）。容器类默认**不进**序 ——
  给纯布局盒子加停留点会让键盘用户为到达真控件多按几次空 Tab。
- **焦点框**：2px 虚线（`colorFocusRing`），焦点在根 / 弹层自身 / 已关闭弹层内 / 被打开的
  弹层盖住 / 节点被禁用时不画；窗口根写 `hideFocusRing` 可整体关掉（截图用）。
- **方向键**按组件分工：`radio` 组 / `slider` / `rating` / `tabs` / `pagination` / `select`
  与 `datepicker`（日历）/ `colorpicker`（色板）。一维组（前六个）到端**环绕**，二维组
  （日历 / 色板）**停住**；带 `Ctrl` / `Alt` 的组合键一律不碰（那是快捷键的地盘）。
- **`Enter` / `Space` 的"激活"就是"用鼠标点它"**：走同一个 `onClick` 出口，所以
  `checkbox` 取反、`radio` 选中、`select` 展开不必各写一份键盘版逻辑。能不能被激活看
  **role**（`<rect onClick>` 想被 Enter 激活要显式写 `role="button"`）。
- **回车提交表单**：焦点在 `input` / `search` 里按 `Enter` = 提交所在的 `<form>`
  （派发 `onSubmit({values})`）；焦点在按钮上按 `Enter` 是按下这个按钮。
- **弹层焦点陷阱**：`dialog` / `drawer` 打开时 Tab 只在弹层内循环（不必维护陷阱栈），
  关闭后焦点自动校正到遍历序里的第一个可聚焦节点。
- **脚本侧接口**：`gx/a11y` 的 `focusOrder()`（当前遍历序快照，含 role / name / tabIndex /
  box）、`focusNode(el)`、`focusNext()` / `focusPrev()`（与 Tab 同一条路径）、`roles()`。
  用途是**回归与自检**：`focusOrder()` 里出现 `name` 为空的按钮，就是漏了 `aria-label`。

### 3.2 手势：长按与滑动

长按与滑动都是"按下 → 移动 → 抬起"这条序列上的**整体判定**，所以它们不挂在某个
组件上，而是落地在事件泵层（`gfx/gesture.go`）—— 任何节点都白拿这两个手势，包括
`<rect>` 这种没有自带行为的盒子。回调与 `onClick` 同口径：从命中节点沿祖先链找第一个
处理器，所以写在父容器上就对整个子树生效。

| prop | 含义 | 缺省 |
|---|---|---|
| `longPressDelay` | 按住多久算长按（ms） | 500（与 tooltip 同值） |
| `longPressSlop` | 长按期间容许的抖动（px）；超过就作废长按 | 10 |
| `swipeThreshold` | 判定成滑动的最小位移（px） | 40 |

```jsx
<rect width={260} height={180}
      longPressDelay={400} swipeThreshold={60}
      onClick={() => log.push("click")}
      onLongPress={(e) => log.push(`lp:${e.x},${e.y},${e.duration}`)}
      onSwipe={(e) => log.push(`swipe:${e.direction},${e.dx},${e.dy}`)} />
```

**手势与 click 互斥**：长按或滑动一旦成立，抬起时**不再派发 `onClick`**（移动端语义里
长按不是点击、滑动也不是点击）。没成立 —— 没到时长、或位移没到阈值 —— 则完全走原有的
click 路径，行为不变。这一条是长按多选 / 滑动删除能用的前提：否则每次长按都会顺带
触发一次选中。

`onSwipe` 的 `direction` 取**位移较大的那个轴**（dominant axis，与移动端惯例一致），
斜着划也只报一个方向；`dx` / `dy` 保留符号（向右为正、向下为正），想要"划了多远"用
`distance`（欧氏距离）。`duration` 是从按下到判定的毫秒数。

两条边界值得记住：

- **长按期间挪开手指就作废**：超过 `longPressSlop` 判定为"这不是按住不动"，长按不再
  触发（缺省 10px 的余量是给手指漂移的）。作废之后如果位移够大，抬起时仍会判成滑动。
- **长按已触发就不再判滑动**：长按后手指挪一下不算滑动，免得两个手势打架。

组件自绘区（`slider` / `tabs` / `rating` 等带几何命中分支的）不参与手势 —— 它们的
`mousedown` 本身就是一次性动作，再叠一层长按 / 滑动只会互相干扰。

## 4. 内置元素参考

| 元素 | 主要属性 | 说明 |
|---|---|---|
| `column` / `row` | `gap` / `padding` / `margin`(子级) / `alignItems` / `justifyContent` / `flexGrow` / `flexShrink`(子级) / `wrap` / `width` / `height` | flex 风格容器，尺寸按内容确定（交叉轴默认 stretch）；`row` 加 `wrap` 放不下折行，`gap` 兼作行内间距与行间距 |
| `view` | `each` / `show` / `fallback` / `key` / `stable` / `gap` | **布局透明的容器**（Fragment）：单子时尺寸完全跟随子节点、多子（列表）按父容器方向堆叠，自己不占盒子；**元素级指令就写在这类元素上** —— `each={rows}` 按列表重复本元素（keyed 复用 / `stable` / `fallback`），`show={open}` keep-alive 显隐。指令对任何内置元素标签都有效，写在 `<column>` / `<row>` 上就是"每一项一个盒子" |
| `grid` | `columns`（1~32） | 等宽列网格：声明序逐行填格，列宽均分内容宽，格子无显式高时拉到行高；不做轨道语法 / colSpan（不等宽列用 `row` + 百分比组合） |
| `text` | `font` / `color` / `width` / `wrap` / `ellipsis` / `fontFamily` / `fontWeight` / `fontStyle` / `lineHeight` / `letterSpacing` | 默认单行文本、超宽硬截断；加 `wrap` 变成文本块（按宽度贪心折行、`\n` 强制换行），`ellipsis={n}` 只留 n 行并在末行补 `...`。**五根文本样式轴沿父链继承**（自身 > 最近祖先 > 缺省），与 `font` 同一口径，见 [6.3](#63-多行文本与自动换行)。`fontFamily` 支持泛型名与族名；`fontWeight` / `fontStyle` 无真实字重变体时**合成**粗体/斜体 |
| `rect` | `width` / `height` / `background` / `border` / `radius` / `shadow` / `borderWidth` / `borderStyle` | 通用盒子；未特判的标签也走这条绘制路径；装饰属性（圆角 / 阴影 / 渐变 / 边框宽度）见 [5.3](#53-装饰绘制) |
| `button` | `onClick` / `disabled` / `background` / `border` / `color` / `font` / `padding` | 缺省浅灰底 + 深灰边框，文字子节点垂直居中；`disabled` 时整体变灰且不响应点击。标签字号同样走 `font` 继承（`font={13}` 写在这里就管标签） |
| `checkbox` / `radio` | `checked` / `onClick` / `border` / `background` / `color` | 18×18 受控控件；`background` 是选中填充色，radio 互斥在 JS 侧用 signal 实现 |
| `switch` | `checked` / `onClick` | 36×20 受控开关（方形轨道），`background` 覆盖打开态轨道色 |
| `progress` | `value`(0~1，越界自动钳位) / `background` / `width` / `height` | 缺省 200×8，轨道浅灰 + 前景主题绿 |
| `separator` | `vertical` / `background` | 横向 1px 高、宽度由容器拉伸；纵向宽度 1px，需显式 `height` |
| `spacer` | `flexGrow` | 不绘制任何内容，仅吃主轴富余空间，用法 `<spacer flexGrow={1}/>` |
| `select` | `value` / `options` / `onChange` / `placeholder` / `disabled` | 受控下拉框；`options` 可为字符串数组或 `{value,label}` 数组，选中派发 `onChange({value})`；键盘可开合/移动/选中/Esc 关闭 |
| `datepicker` | `value` / `onChange` / `min` / `max` / `placeholder` / `disabled` | 受控日期选择器：字段 + 贴字段弹层（月历）。`value` 是 `"YYYY-MM-DD"`（**只有这一种格式**，非零填充与不存在的日期一律当无值），点格子派发 `onChange({value})`；`min`/`max` 拦在弹层展开与选中两处。键盘：`Enter`/`Space` 展开，`←/→` ±1 天、`↑/↓` ±7 天、`PageUp/PageDown` ±1 月、`Home/End` 本月首末日，`Esc` 收起（见 [3.1](#31-焦点与键盘遍历无障碍)） |
| `colorpicker` | `value` / `onChange` / `colors` / `columns` / `placeholder` / `disabled` | 受控取色板：字段（色块 + 十六进制）+ 贴字段弹层（色格网格）。`colors` 缺省是 24 色内置色板，**显式给空数组就真的是空色板**（空色板不展开）；`columns` 1~32（缺省 8）。点色格派发 `onChange({value})`（值比较忽略大小写与空白）；键盘 `Enter`/`Space` 展开、方向键走格、`Home/End` 首末格，二维格子在边界**停住**不环绕 |
| `upload` | `value` / `onChange` / `multiple` / `accept` / `filter` / `placeholder` / `disabled` | 文件选择字段（虚线边框 + `folder` 图标）：点击直接调**平台原生**"打开文件"对话框，弹出的载荷是 `{files, paths}`（对象数组 + 路径数组）。**受控/非受控两用**：有 `value` 就读它（字符串数组或 `{name,path}` 对象数组），没有就自己维护已选列表（选中立即更新显示）。`multiple` = 多次选择**累加**（底层对话框一次只回一个路径）；`filter`（`"图片\|*.png;*.jpg"`）优先于 `accept`；后端没接对话框时**什么都不做**（stderr 一次告警，绝不编造假文件名）。键盘 `Enter`/`Space` 打开对话框 |
| `label` | `required` / `align` / `width` | 表单标签：单行文字 + `required` 时的红色星号，`align="right"` 整段贴右缘。**不给 `for`** —— 文字子节点就是名字，无障碍名由它出（见 [3.1](#31-焦点与键盘遍历无障碍)） |
| `form` | `onSubmit` / `gap` / `padding` | 表单容器：纵排（语义与 `column` 一致），只加一件事——**回车提交**。焦点在 `input` / `search` 里按 `Enter` 派发 `onSubmit({values})`，`values` 只收**带 `name`** 的字段（与 HTML 一致）。标签列宽对齐归 `label` 自己管，`form` 不代劳 |
| `tabs` / `tab` | `value` / `onChange`（tabs）、`title`（tab） | 选项卡：顶部标签条 + 内容区，`<tab title="文件">` 直接堆在 `<tabs>` 下即为页。`value` 存在 ⇒ 受控（点击只派发 `onChange({index, title})`，等脚本把新下标写回 signal）；缺省非受控（内部切换）。页是 **keep-alive** 的：全部页留树（输入框内容、滚动位置都保留），非激活页只是不布局、不绘制、不命中 |
| `dialog` | `open` / `onClose` | 模态弹层：40% 黑遮罩 + 居中卡片（流内子节点即卡片内容）；点遮罩 / Esc / 卡片内按钮触发 `onClose`，遮罩吞掉其下点击 |
| `toast` | `message` / `level` | 非模态提示，固定右上角；`level` 取 `success` / `warn` / `error` / `info` 决定色条，显隐由 JS 侧信号控制 |
| `alert` | `level` / `closable` / `onClose` | 横幅提示：四档 `level`（`info` / `success` / `warn` / `error`，缺省 `info`）决定左侧色条与图标色；`closable` 时右上角出现关闭叉，点它派发 `onClose`（**不替脚本摘树**，显隐归信号管） |
| `tag` | `color` / `closable` / `onClose` | 小标签：胶囊底色按 `color`（缺省浅灰），`closable` 时尾部有叉，点叉派发 `onClose` |
| `badge` | `value` / `max` / `dot` | 角标：**包裹式**（唯一流内子节点即宿主，尺寸跟随它），角标画在宿主右上角。`value` 超过 `max` 显示 `max+`（缺省 99）；`value` 为 0/负数自动隐藏；`dot` 为小红点（不显示数字） |
| `avatar` | `name` / `size` / `color` / `round` | 头像：`size` 为边长（缺省 40），`color` 为底色（缺省主题蓝），显示 `name` 首字；`round` 画成正圆，否则圆角方块 |
| `empty` | `desc` / 子节点 | 空状态：居中提示（`desc` 文案 + 可选子节点作插图），绘制一个占位图形 |
| `spinner` | `size` / `color` | 转圈加载：12 根刻度绕圈、亮度按角度衰减形成残影；`size` 为边长（缺省 24）。靠**动画心跳**持续重绘（与过渡动画共用一根 16ms 表，静止时一起停表，零开销） |
| `skeleton` | `rows` / `avatar` / `active` | 骨架屏：`rows` 条占位行（缺省 3）+ 可选左侧圆形 `avatar`；`active`（缺省真）时亮度呼吸闪烁（同一根动画心跳）；`active={false}` 时不登记心跳、静态显示 |
| `pagination` | `total` / `pageSize` / `current` / `onChange` | 分页器：**完全受控**（显示只看 `current`，点击页码只派发 `onChange({page, pageSize})`）。页数 = `ceil(total/pageSize)`（`pageSize` 缺省 10）；页数 > 7 时折叠出省略号（首尾恒可见、省略号不可点）；第 1 页点 `‹`、末页点 `›` 不派发 |
| `icon` | `name` / `size` / `color` | 内置图标：`name` 取内置图标集（见下方清单），`size` 为边长（缺省 16），`color` 缺省继承文字色；未知名字静默不画。纯光栅原语绘制、三平台零依赖 |
| `drawer` | `open` / `side` / `width` / `onClose` | 抽屉弹层：**复用 dialog 的弹层机制**（遮罩铺满窗口、模态、Esc/点遮罩关闭），内容卡片贴 `side`（`left`/`right`，缺省 `right`）边、宽按 `width`（缺省 280），打开时从侧边滑入（靠动画心跳推进进度，无新增定时器） |
| `input` | `value` / `onInput` / `placeholder` / `disabled` | 单行受控输入（沿 `value` 显示，编辑派发 `onInput({value})`）；获焦边框转蓝并显示闪烁竖线光标，点击可定位光标；支持 ←/→/Home/End/Backspace/Delete，`Enter`/`Esc` 不消费；**文本选区**（拖选 / Shift+方向键）与 `Ctrl/Cmd`+`A`/`C`/`X`/`V`（全选/复制/剪切/粘贴）；支持 IME 候选词整批提交（Windows） |
| `search` | 同 `input` + `onSearch` | `input` 的字段变体：左侧放大镜，获焦按 `Enter` 整段提交 `onSearch({value})`（逐键 `onInput` 照旧），其余与 `input` 一致 |
| `rating` | `value` / `max` / `onChange` / `color` / `disabled` | 星级评分：**完全受控**（显示只看 `value`，点击第几格就派发 `onChange({value})`，值不变不派发）。`max` 缺省 5、上限 10；每颗星占 20px 方格（缺省 100×20），星形半径按 min(格宽, 高) 自适应；实心星走 `color` prop（缺省主题强调色），其余空心描边。`model` 口径与 `select` 相同 |
| `textarea` | `value` / `onInput` / `rows` / `placeholder` / `disabled` / `wrap` | 多行受控编辑器；光标 `{行,列}` 二维移动（↑↓←→/Home/End/Backspace/Delete），**`Enter` 插入换行**（不同于 input）；**软换行缺省开**（按内容区宽度折行，`wrap={false}` 关掉），↑↓/Home/End/点击定位都按**屏幕上的行**走；选区与 `Ctrl/Cmd`+`A`/`C`/`X`/`V` 与 `input` 同一套；内容超高时纵向滚动并跟随光标；同样支持 IME。缺省 4 行 × 240px |
| `scroll` | `width` / `height` / `scrollTop` / `scrollLeft` / `onScroll` / `onWheel` / `vlist` / `itemHeight` / `buffer` | 滚动容器：内容超高时右侧、超宽时底部出现 8px 轨道 + 比例滑块；滚轮滚动（一格 60px，`Shift+滚轮`走横向），滑块可拖拽，到边界后滚轮才冒泡给 `onWheel`；溢出的内容既画不出来也点不中。缺省高 200。**写入口**（见下）：`scrollTop` / `scrollLeft` 把滚动位置写进去（数字 = 绝对像素，另有别名 `top`/`bottom` 与 `start`/`end`），用户手势滚动时派发 `onScroll({offsetX, offsetY})`。加 `vlist itemHeight={N}` 即变成[虚拟化长列表](#_6-6-虚拟化长列表-vlist)：只物化可见的行，十万行与十行的成本一样 |
| `image` | `src` / `width` / `height` / `disabled` | 显示 png / jpeg / gif 图片（Go 标准库解码，无新增依赖）；不给 `width`/`height` 时用图片自然尺寸，给了就按最近邻缩放；`src` 相对**进程工作目录**解析，加载失败画灰底交叉线占位（stderr 每个路径只警告一次），不中断其它内容 |
| `video` | `src` / `poster` / `playing` / `autoplay` / `muted` / `loop` / `volume` / `controls` / `fit` | 视频框：**标签与宿主契约**（S8）。内核**不解码** —— 播放交给窗口后端可选实现的 `nativeVideoHost`（平台视频层：MF / AVPlayerLayer / SurfaceView），决策见 [video-decision.md](video-decision.md)。后端没这块能力时画 `poster` 封面（没封面就深色底 + 播放三角），并**诚实报错**：stderr 告警一次 + 对该节点派发一次 `onError({code:"unsupported"})`，`canIUse("video")` 照实回答 `false`。`playing`（等价 `autoplay`）、`muted` / `loop` / `volume` 都是**受控**属性，宿主上报的状态经 `onReady` / `onPlay` / `onPause` / `onEnded` / `onTimeUpdate({currentTime, duration})` 回到脚本。`fit` 取 `contain`（缺省）/ `cover` / `fill`；不给尺寸时用封面自然尺寸，兜底 320×180 |
| `canvas` | `width` / `height` / `onDraw(ctx)` / `background` / `border` | 自绘画布：`onDraw` 收到一个 ctx，用 `ctx.fillRect/strokeRect/fillCircle/strokeCircle/line/drawText/clear` 直接落笔，坐标是**画布局部坐标**（0,0 = 左上角），越界部分自动裁掉；`ctx.width` / `ctx.height` 是画布尺寸。`onDraw` 里读到的 signal 变化会自动重绘（缺省 200×120） |
| `slider` | `value` / `onInput` / `min` / `max` / `step` / `disabled` | 受控滑块（`min`/`max`/`step` 缺省 0/100/1）：显示只看 `value`，拖动或**单击轨道任意位置**派发 `onInput({value})`（`value` 是 **number**）；拖出窗口仍跟手（win32 走 `SetCapture`）。缺省 160×24 |
| `menubar` | `background` / `border` | 菜单栏容器（缺省 26px 高、自动铺满容器宽）；**它只是个普通容器**，脚本自己写 `column { menubar; 内容 }`，gfx 不会往 root 里偷偷插一条 |
| `menu` | `label`（或 `title`、或文本子节点） | 菜单标题；作为 `<menubar>` 的直接子节点时是**顶级菜单**（下拉挂在标题正下方），嵌在 `<menuitem>` 里时是**子菜单**（挂在触发项右侧）。空菜单点了不展开 |
| `menuitem` | `label` / `shortcut` / `disabled` / `onClick` | 菜单项；点中派发 `onClick({x, y})` 并收起整棵菜单。`disabled` 灰字且点了没反应（**也不收起**）；内嵌一个 `<menu>` 即成为子菜单触发器（点击展开/收起右侧下拉，自身不派发 `onClick`）。分隔线用已有的 `<separator>`，它照样可命中（点了没反应） |
| `table` | `columns` / `rows` / `zebra` / `borderless` / `onRowClick` / `width` | 声明式数据表格：喂 `columns` + `rows` 两组数据即可（`columns` 可写字符串数组简写或 `{key, label, width?, align?}`；`rows` 可按列下标或按 `key` 取），表头 / 网格线 / 列宽分配都由组件负责。行高固定 28px，行**默认不可点**，挂 `onRowClick` 才派发 `{index, row}`。`columns` / `rows` 在**首次布局时物化**成内部行 —— 数据变了要换 prop 值触发重建，就地改数组元素不反映 |
| `tree` | `nodes` / `onSelect` | 声明式树：`nodes` 是递归的 `{label, key?, children?}`（纯字符串数组当叶子简写）。展开 / 收起是**渲染层状态**（点有子节点的行即切换），初始全收起；展开态按 `key` 跨重建保留（不给 `key` 时用 `label`，同层必须唯一） |
| `list-item` | `selected` / `divider` / `height` | 列表行：普通容器 + “行”这套约定（固定 28px 行高、左右 10px 留白、悬停高亮、选中底色、行底线缺省开）。`selected` 是**受控**选中态（只影响底色，状态由脚本持有）；没挂 `onClick` 的行悬停不变色 |
| `tooltip` | `text` / `placement` / `delay` | 悬停提示：包住触发元素，鼠标停留 `delay` 毫秒（缺省 500）后在 `placement`（`top` / `bottom` / `left` / `right`，缺省 `bottom`，贴边自动翻边）弹一段深色小字。布局透明（盒子就是触发元素的盒子），弹层**不挡交互** |

> 颜色属性（`background` / `border` / `color`）在组件标签上有语义差异：`background` 表示"选中/填充的强调色"，
> 在 `button` / `rect` 上才是普通填充色；`color` 沿祖先链继承，因此 `<button color="#fff">文字</button>` 生效。

> **内置图标清单**（`<icon name=... />`）：`home` / `search` / `user` / `gear` / `bell` / `chat` /
> `folder` / `calendar` / `heart` / `plus` / `minus` / `close` / `check` / `arrow-left` / `arrow-right`。
> 全部在 24×24 逻辑网格里用直线 / 矩形 / 圆拼装（像素风，三平台观感一致、零依赖）；冷门图标交给
> 项目自己的 `<canvas>` 组件（`docs/gui-guide.md` 的 canvas 一节），内核不做无限扩张的图标库。

> 受控组件（`input` / `search` / `textarea` / `select` / `rating` / `datepicker` / `colorpicker` /
> `upload` / `checkbox` / `switch` / `radio`）另有一条
> **`model` 指令**：`<input model={draft} />` 一次接好读（`value`）与写（`onInput`），不用再手写
> `value={() => draft()} onInput={(e) => setDraft(e.value)}`。语义表见 [6.0](#60-一条指令搞定读写model)，
> 完整设计见 [gui-model-binding.md](gui-model-binding.md)（`upload` 的写回取载荷里的 `paths`，
> 是这一族里唯一的例外，见该文 §3）。

## 5. 布局

### 5.1 弹性布局：百分比 / min-max / flexShrink / 折行 / 网格

尺寸词汇不止整数像素，组合起来可以纯声明地做自适应界面：

```js
h("row", { gap: 10 },
  h("rect", { width: "33%", height: 80, radius: 10 }),                  // 百分比：按父容器内容区解析
  h("rect", { flexGrow: 1, minWidth: 120, maxWidth: 320, height: 16 }), // grow + min-max 钳位
)
```

- `width="50%"` 百分比字符串按**父容器内容区**解析，坏格式静默忽略回退固有尺寸；百分比算显式尺寸
  （不再吃交叉轴 stretch），也不撑大父容器
- `minWidth/maxWidth/minHeight/maxHeight` 在 stretch / grow / shrink / 百分比全部落定后**终钳位**
  （v1 不回收钳位差：grow 超过 maxWidth 的富余不再分给别人）
- `flexShrink` 是 `flexGrow` 的对称面：主轴溢出时按**系数×基础尺寸**加权分摊（CSS 同款权重），下限由 minWidth 兜底
- `<row wrap>` 放不下折到下一行（声明序贪心）：`gap` 兼作行内间距与行间距，grow / shrink / justifyContent
  只在行内生效，alignItems 对**行高**生效
- `<grid columns={3}>` 等宽列网格：格子无显式宽时拉伸到列宽、无显式高时拉到行高（该行最高者），
  `alignItems` 在格内两轴同时生效

### 5.2 层叠与定位

任何节点都可挂 `zIndex`（同层绘制与命中顺序，越大越靠上，相同值保持声明序）、
`position="absolute"` + `left`/`top`（脱离常规流，相对父内容区定位）与 `escapeClipping`
（子树的绘制与命中溢出父盒，收集到根层级最后绘制）。`dialog`/`toast` 天生是弹层，自带高层级基线，
不必手写大 `zIndex`：

```js
h("column", null,
  h("rect", { width: 200, height: 100, background: "#eee" }),
  // 绝对定位 + 逃逸裁剪：绘制与命中都溢出父盒
  h("rect", {
    position: "absolute", left: 40, top: 20, width: 120, height: 60,
    background: "rgba(192, 57, 43, 0.6)", escapeClipping: true,
  }),
)
```

颜色支持命名色与 `#rgb` / `#rgba` / `#rrggbb` / `#rrggbbaa` / `rgb()` / `rgba()`（alpha 可写 `0~255` 或 `0~1`），
带 alpha 的颜色会与下方内容做真正的混合（`dialog` 的遮罩就是这么实现的）。

### 5.3 装饰绘制

`radius` / `shadow` / `borderWidth` / `borderStyle` 对所有走通用盒子绘制分支的标签生效
（`rect` / `button` / 容器等）：

```js
h("rect", {
  width: 220, height: 64, radius: 12,                          // 圆角，自动钳到短边一半（够大即胶囊形）
  background: "linear-gradient(to right, #667eea, #764ba2)",   // 线性渐变：方向词可省（缺省 to bottom），2~8 个色标均匀分布
  shadow: { x: 0, y: 4, blur: 12, color: "#00000033" },        // 四项全可省，color 缺省 25% 黑，blur 钳 0~24
  borderWidth: 2, borderStyle: "dashed",                       // 边框宽度任意（缺省 1），dashed 虚线段长 = 2×宽
})
```

- `radius=0` 的纯色填充与 1px 实线边框走与无装饰时**逐字节等价**的快路径 —— 不加装饰零开销；
  圆角用半像素覆盖抗锯齿
- 阴影**先画再画面**（不会被自身盖住），轮廓跟随圆角；移动带阴影的节点时脏矩形自动按阴影外扩，不残留
- 坏格式静默回落（非法渐变 → 非法颜色 → 无填充），画歪看得见但不中断整帧
- 渐变面不参与 button 悬停提亮（提亮只定义在纯色面），`disabled` 降饱和对两者都生效

## 6. 受控组件与文本输入

### 6.0 一条指令搞定读写：`model`

受控组件是**完全受控**的：显示只看 `value`/`checked`，改动只派发 `onInput`/`onChange`/`onClick`，
值落地要等脚本写回 signal。`model` 把这两件事收成一条指令（等价于 Vue 的 `v-model`）：

```js
const [draft, setDraft] = createSignal("")

// 旧：两个 prop，漏掉 onInput 就"打不进字"（不报错）
h("input", { value: () => draft(), onInput: (e) => setDraft(e.value) })

// 新：一条指令，读写都由内核接好
h("input", { model: draft })
```

| 标签 | 读方向 | 写方向事件 | 写进去的值 |
|---|---|---|---|
| `input` / `search` / `textarea` | `value` | `onInput({value})` | 字符串 |
| `slider` | `value` | `onInput({value})` | 数字（不转换） |
| `select` | `value` | `onChange({value})` | 字符串 |
| `rating` | `value` | `onChange({value})` | 数字（第几颗星） |
| `checkbox` / `switch` | `checked` | `onClick()` | 布尔（写入取反） |
| `radio` | `checked`（= `model() === value`） | `onClick()` | 属性 `value` 原样写入 |

`model` 收 **signal**（`createSignal` 的 getter 自带 `set`）或 **[get, set] 二元组**：

```js
h("input", { model: draft })                                    // signal
h("input", { model: [() => user().name, (v) => setUser({ ...user(), name: v })] })  // 自定义来源
```

三条不静默的规则：① 同时给 `model` 与 `value`/`checked` ⇒ `model` 覆盖并警告；
② 同时给 `onInput` 等 ⇒ **两个都跑**（model 写回在前），所以 `<input model={q} onInput={(e) => search(e.value)} />`
是合法组合；③ 传标量 / 无 setter 的函数 / 不支持的标签 ⇒ 降级（只读或忽略）并打 stderr 警告。

可跑示例 `testdata/model_demo.js`；接口设计、与 Vue 的逐条对照、反例清单见
[gui-model-binding.md](gui-model-binding.md)。

### 6.1 受控文本输入

受控语义与 `checkbox` / `select` 一致：显示只看 `value`，编辑只派发 `onInput`。

```js
const [name, setName] = createSignal("")

h("input", {
  width: 240,
  placeholder: "Type your name",
  value: () => name(),          // 显示内容永远来自 signal
  onInput: (e) => setName(e.value),  // 不回写的话输入不会有反应
})
```

获焦后边框转蓝并出现闪烁竖线光标；`←`/`→`/`Home`/`End` 移动光标，`Backspace`/`Delete` 删除，
点击框内任意位置可定位光标。`Enter`/`Esc` 不被输入框消费，会冒泡到 `onKeyDown`。
光标闪烁需要事件泵持续醒来，挂一个 `requestAnimationFrame` 循环即可
（见 [testdata/input_demo.js](../testdata/input_demo.js)）。

单行框也有**文本选区**：按住鼠标拖选、`Shift` + 方向键扩展、`Ctrl/Cmd`+`A` 全选，
`Ctrl/Cmd`+`C`/`X`/`V` 复制/剪切/粘贴 —— 与 `<textarea>` 完全同一套实现，细节见
[6.3 末尾的"选区与剪贴板"](#63-多行文本与自动换行)。

### 6.2 输入法 IME

`<input>` / `<search>` / `<textarea>` 都支持候选词输入（Windows 后端）。切到中文输入法后敲拼音，
正在拼的字由系统组合窗显示，选定候选词后**整批**插到光标处：一次提交只派发一次 `onInput`，
光标一次跨过整批（不会把下一个词插到前一个词中间）。焦点不在编辑框上时输入法自动关闭，
在按钮/画布上敲字不会弹候选窗。`textarea` 里同样可用，且"提交内容自带换行"会正确把光标落到新行。

已知取舍：Linux（X11）后端暂无 IME；组合过程不在框内内联绘制（Windows 与 macOS 同口径，
macOS 的候选窗由系统绘制、选定后整批提交，纯键盘布局（ABC 等）下英文照旧直入）。
示例见 [testdata/ime_demo.js](../testdata/ime_demo.js)。

### 6.3 多行文本与自动换行

`<text>` 加 `wrap` 就变成会自动折行的文本块（按可用宽度贪心断行，中西文一视同仁），
`ellipsis` 用来限行数并补省略号：

```js
h("column", { gap: 8 },
  // 折行：高度按行数自动变高；宽度取显式 width，没写就铺满容器可用宽度
  h("text", { wrap: true, width: 260, font: 14 }, longText),
  // 最多 2 行，超出补 "..."
  h("text", { wrap: true, ellipsis: 2, width: 260, font: 14 }, longText),
  // 不给 wrap 就还是老行为：单行、超宽硬截断
  h("text", { width: 260, font: 14 }, longText),
)
```

`<textarea>` 是多行编辑框，受控语义与 `input` 一致：

```js
const [text, setText] = createSignal("")

h("textarea", {
  rows: 5,
  width: 300,
  placeholder: "Type here...",
  value: () => text(),
  onInput: (e) => setText(e.value),   // 不回写就不会有反应
})
```

- **软换行缺省开**（与 CSS `textarea` 一致）：长行按内容区宽度贪心折行，屏幕上看到几行就是几行；
  `wrap={false}` 关掉，回到"超长行被右侧裁掉"的老行为。折行算法与 `<text wrap>` 共用同一份实现；
- 换行是**纯显示**的：`value` 里仍然只有 `\n` 一种换行，逻辑行 / 列号与文本严格对应，
  所以插入、退格、左右移动都不受折行影响；受影响的是**按屏幕行**的四类动作 ——
  `↑`/`↓` 跨视觉行（并保持**像素横向位置**，"第 5 列"在不同行上对应的 x 不同）、
  `Home`/`End` 跳到**屏幕上那一行**的首尾、点击定位按 y 找行按 x 找列、滚动跟随光标；
- `Enter` **被编辑框消费**（插入换行）—— 与单行 `input` 相反，多行框里 Enter 就是内容；
  `Esc` / 功能键 / 带 `Ctrl`+`Alt` 的组合键仍然放行给脚本；
  **`Tab` 不再放行**（2026-10-02 起由无障碍层消费：它是"离开这个字段"的动作，见 [3.1](#31-焦点与键盘遍历无障碍)）；
- 内容超过可视高度后自动纵向滚动，且**滚动跟随光标**（在底部回车时光标不会跑到框外）；
  也可以把光标放进框里滚滚轮。行数按**视觉行**算，折出来的行照样能滚到底。

#### 文本样式：字体族 / 粗斜体 / 行高 / 字距

除 `font` 之外还有四根样式轴，全部**沿父链继承**（最近祖先优先），写在 `column` / `row` /
`button` / `textarea` 上对其内全部文本生效 —— 与 `font` 同一口径：

| prop | 取值 | 说明 |
|---|---|---|
| `fontFamily` | 族名 / 泛型名 / 字体文件路径 | `monospace` / `serif` / `sans-serif` 三个泛型名按平台挑一个真等宽/衬线族；认不出的族名**静默退回默认字体**（不报错、不改度量，一个笔误不该让整屏文字不变） |
| `fontWeight` | `"bold"` / `600` / `"700"` / `"normal"` | ≥600 算粗。**`"normal"` 能主动关掉祖先的粗体**（"在粗体标题里让一个词正常"要靠这条） |
| `fontStyle` | `"italic"` / `"oblique"` / `"normal"` | 斜体 |
| `lineHeight` | 像素，`0` = 自动 | 自动值 = 字号 + 字号/4（保持与加样式轴之前逐像素一致） |
| `letterSpacing` | 像素，可为负 | 加在**每个字符之后**（首字符不缩进，与 CSS 一致）；参与换行判定，负值做紧凑排版 |

```js
h("column", { fontFamily: "monospace", lineHeight: 22 },
  h("text", { font: 14 }, "代码块：整列都是等宽 + 22px 行高"),
  h("text", { font: 14, fontStyle: "italic" }, "斜体"),
  h("text", { font: 14, fontWeight: "normal" }, "这里把祖先的粗体关掉"),
)
```

- 没找到**真实**字重/斜体变体时**合成**：粗体是同一份掩码往右 1px 再压一遍，斜体绕**基线**
  剪切（字底钉住、字顶向右倾）。真实变体优先，所以在装了真粗体的族上不会看到合成痕迹；
- 量测与绘制共用同一套样式 —— 行高/字距会改变**内容尺寸**，两处若各算一套，就会出现
  "盒子按 16px 排、文字按 18px 画"的错位；
- 字体族索引**惰性构建**：不写 `fontFamily` 就绝不扫字体目录（本机实测建索引约 300ms），
  用泛型名或族名时才建一次；
- `<input>` / `<textarea>` 同样吃这五根轴（在编辑框上写 `fontFamily="monospace"` 就是代码输入框），
  行高也会改变编辑框的滚动与视觉行划分。

#### 选区、复制与剪贴板（`input` / `textarea` 通用）

| 操作 | 效果 |
|---|---|
| 鼠标拖选 | 按下即定位插入点并钉住锚点，拖动扩展/收缩；松手**保留**选区。拖出窗口在 Windows/macOS 上仍然跟手（内部用 `CapturePointer`） |
| `Shift` + `←`/`→`/`↑`/`↓`/`Home`/`End` | 扩展选区（锚点固定）；已选区时按不带 `Shift` 的方向键 = 收起并落到选区头/尾 |
| `Ctrl`/`Cmd` + `A` | 全选 |
| `Ctrl`/`Cmd` + `C` / `X` / `V` | 复制 / 剪切 / 粘贴（走系统剪贴板，见 [9.2](#92-剪贴板)） |
| 打字 / `Enter` / `Backspace` / `Delete` | 有选区时**替换或删除选区**（选区的意义就在这里），没有选区时才是原来的单字符行为 |

- 剪贴板剪贴/复制在**没有选区时不消费按键**（继续冒泡给脚本），也**不覆盖**用户已有的剪贴板 ——
  "没选中就复制整行"那种解释会静默毁掉用户的数据；
- 单行框粘贴多行文本时，换行折成**空格**（单行框渲染不了第二行，直接塞进去会像丢字）；
- 拿不到剪贴板（后端不支持 / 被别的进程占着）**不影响剪切本身**：内容照样删掉，只是剪贴板没更新；
- 选区高亮用主题的 `selection` token（半透明，画在文字**之下** —— 不透明会把选中的字盖掉），
  见 [theme.md](theme.md)；
- `Ctrl` 与 macOS 的 `Cmd` 都归一到同一个修饰位：`Cmd+C` 在 macOS 上就是复制，不需要两套判断。

### 6.4 滑块

`<slider>` 是受控滑块：`value` 决定位置（含 `min`/`max`/`step`），拖动或**单击轨道任意位置**
都会派发 `onInput({value})`：

```js
const [vol, setVol] = createSignal(40)

h("slider", {
  width: 200, min: 0, max: 100, step: 5,
  value: () => vol(),
  onInput: (e) => setVol(e.value),   // e.value 是 number，不是字符串
})
```

- **受控语义**与 `input` / `textarea` 一致：不回写 `value`，滑块会弹回原位；
- 点击轨道**直接跳值**，不必"先按住再拖"；
- 拖动中鼠标划过别的控件**不会**给它们加悬停高亮 —— 一次拖动算一个手势；
- 拖动期间鼠标移出窗口在 Windows 上仍然跟手（内部用 `SetCapture`）；
- `step ≤ 0` 表示连续取值；`max < min` 时量程塌缩到 `min`（滑块停在最左），不会产生 NaN。

### 6.5 滚动容器

`<scroll>` 让任意高度的内容待在固定高度的视口里，超出部分被裁掉，右侧自动出现滚动条：

```js
h("scroll", { width: 240, height: 120, onWheel: () => setOverscroll(n => n + 1) },
  rows.map((r) => h("rect", { height: 36, background: "#fff" },
    h("text", { font: 13 }, r))),
)
```

- 滚轮在容器内先被容器消费（一格 60px），**到边界才继续往外冒泡**给 `onWheel` ——
  所以"到顶/到底再翻页"可以纯 JS 写；
- 溢出的内容**既画不出来也点不中**（绘制裁剪与命中裁剪用同一个视口），不会出现幽灵点击；
- 子节点的 `Box` 已经包含滚动偏移（就是屏幕坐标），不用自己再算；
- 内容不足一屏时不出滚动条，也不会给内容让出滚动条那 8px；
- 静态数组子节点会自动展开成兄弟节点，所以 `<scroll>{rows}</scroll>` 直接可用。
- **横向滚动**：内容固有宽度（子节点的显式 `width`、文本固有宽等）超出视口时，
  底部出现横向轨道与滑块，`Shift+滚轮`横向滚；纵向不可滚而内容超宽时普通滚轮
  也会横向兜底。默认铺满（stretch）的子节点是"跟随容器"，不会触发横向滚动条。
- **滚动条可拖拽**：按住滑块直接拖（拖拽期间鼠标捕获，划过别的控件不会误触）；
  纵向与横向滑块都支持，行程按"可滚范围 / 滑块行程"等比换算。

**滚动位置的写入口**：上面这些都是"用户手势驱动"，脚本此前没有任何把滚动位置写
进去的办法 —— 于是「跳到底部」「跳到第 N 行」「跟随最新一条」全都做不出来。两个受控
prop 补上这个方向：

| prop | 取值 | 含义 |
| --- | --- | --- |
| `scrollTop` | number | 竖直方向的**绝对像素**偏移，按内容总高钳位（写 `99999` 等于滚到底） |
| | `"top"` / `"bottom"` | 别名。`"bottom"` 是**粘性**的：内容变长时会重新贴底，正好给"跟随最新"用 |
| `scrollLeft` | number / `"start"` / `"end"` | 同上，横向版本（`start` 对应 `top`、`end` 对应 `bottom`） |

```jsx
// 聊天/日志那种"尾巴跟随": 内容一长就自动贴到底
<scroll height={140} scrollTop={follow() ? "bottom" : 0} onScroll={(e) => setPos(e.offsetY)}>
  {() => lines().map((t) => <rect height={28}><text>{t}</text></rect>)}
</scroll>
```

三条必须记住的口径：

1. **一帧只认一次新目标**：同一个目标值不会被反复施加。这条是刻意的 —— 否则每帧都会
   把偏移拽回脚本给的值，用户用滚轮往上翻之后**下一帧就被抢回去**，表现为"滚不动"。
   要重新定位请换一个新的值；要一直贴底请用粘性别名 `"bottom"`（它的去重键带上了
   解出的最大值，内容变长才算"新目标"）。
2. **写入与上报是两回事**：手势滚动会派发 `onScroll({offsetX, offsetY})`，
   布局期写 prop **不派发** —— 写是一次内部搬指针，再回调脚本等于布局过程中重入，会把脏区
   和布局状态搅在一起。读当前位置请自己记住，或用 `onScroll` 跟着更。
3. **写入发生在布局期**：先把这一屏的内容量出来才知道能滚多远，所以 `scrollTop` 是在
   内容测量之后、钳位与 vlist 开窗之前施加的 —— 同一帧内写进去的偏移立刻参与钳位和开窗，
   不会"慢一帧"。

配合方向（手势 → 脚本）就是 `onScroll`：拿到 `offsetY` 就能判断"用户是不是还在底部"，
从而决定要不要继续跟随 —— 两个方向凑齐才是完整的 `tail -f` 交互。完整演示见
[testdata/scroll_to_demo.js](../testdata/scroll_to_demo.js)。

### 6.6 虚拟化长列表（vlist）

行数上万时，`<scroll>` 默认会把**每一行都建成节点**：十万行 = 十万棵子树，首帧建树
就要两秒多，每帧布局还要遍历十万个盒子。加两个属性就能让成本与行数**脱钩**：

```js
h("scroll", { vlist: true, itemHeight: 28, width: 420, height: 300 },
  h("view", { each: rows, key: "id" },
    (r, i) => h("row", { height: 28 }, h("text", {}, i + " · " + r.title))
  )
)
```

- `vlist`（无值或 `true`）开启窗口化，`itemHeight` 是**每行的固定高**（必给正数）；
- 内核只物化**可见区间 + 上下各 `buffer` 行**（`buffer` 缺省 2，`buffer={0}` 合法），
  上下用两个撑高垫片补出总高，所以**滚动条长度、行程与全量渲染逐像素一致**；
- 行内拿到的下标是**全局下标**（滚到第 50000 行，`i` 就是 50000，不是 0），
  所以行内容不会因为窗口平移而错位；
- 行高必须固定。变高行需要"测量 → 回填 → 二次布局"，且滚动中可见内容会跳，
  所以本期不做半吊子版本。

实测（本机 i7-10700K，`go test ./gfx -bench BenchmarkVlist`）：

| 行数 | 首帧（全量） | 首帧（vlist） | 滚动一帧（全量） | 滚动一帧（vlist） |
| --- | --- | --- | --- | --- |
| 1 000 | 16 ms | **2 ms** | 6.5 ms | **0.17 ms** |
| 10 000 | 206 ms | **10 ms** | 93 ms | **0.11 ms** |
| 100 000 | 2 187 ms | **89 ms** | 999 ms | **0.12 ms** |

vlist 那一列**与行数脱钩**：首帧渲染调用恒为 64 次（待物化上限，落屏后收敛到视口内的
十几行），滚动帧恒定在 0.1 ms 量级。首帧剩下的那点增长来自脚本里造十万行**数据**
（JS 数组与对象），不是建行节点。完整数据见 [bench-results/vlist-20261001.json](../bench-results/vlist-20261001.json)。

三个容易踩的点：

1. **`itemHeight` 漏写就是静默退化**：不写会退化成全量渲染并告警一次
   （`<scroll vlist>: 缺少 itemHeight …`），性能问题会被误以为是别的原因；
2. **列表形状要对**：vlist 窗口化的是 `<scroll vlist><view each={rows}>…</view></scroll>`
   这种形状 —— 列表要能被容器找到（中间隔一层布局盒子也行）。找不到时告警一次并退化为普通滚动容器；
3. **嵌套滚动区**：内层 `<scroll>` 里的列表归内层管，外层 vlist 不会去窗口化它（会错位）。

**持续追加（日志 tail / 聊天流）**：上面的数据都来自静态列表。把列表换成**会变长的数据源**再测一遍（`gfx/vlist_append_test.go`，两千到五千行、每批追加 20~200 行、连追九批）：

| 关注点 | 现象 | 结论 |
| --- | --- | --- |
| 追加会重建全列表吗 | 顶部追加 100 行：**重算 0 行**；中段追加 200 行：同样 0 行。内容总高恒等于 `总数 * itemHeight`（5000→5100 行时 `contentH` 从 140000 变 142800） | **不重建**。新行落在窗口之外时一行都不用重算，成本与"列表有多长"无关 |
| 滚动位置会被挪走吗 | 滚到 2000px 处连追两批（共 100 行），偏移仍是 2000，窗口照样是 `[69, 84)` | **不动**。回看历史不会被新数据打断 |
| 节点数会不会累积 | 9 批之后节点数 79、树上 15 行，与第 1 批之后完全一致 | **收敛**。既不复建也不泄漏，挂机看日志不会胀 |

三条合起来说明「日志查看器那种持续追加」不需要动内核，缺的只有**跟随最新**这个语义 ——
它由脚本显式表达：`scrollTop={"bottom"}`（见 [§6.5 的滚动位置写入口](#65-滚动容器)），
配合 `onScroll` 判断用户是不是还在底部，就能做出"用户贴底时才跟随"的完整 tail 行为。

### 6.7 字段类三件套：日期 / 颜色 / 文件

`datepicker` / `colorpicker` 是"字段 + 贴字段弹层"这一族的第二、三个成员（第一个是
`select`）：点字段展开、点外部或 `Esc` 收起、选中后**焦点回到字段**，三者共用同一套状态机，
所以同族弹层天然互斥（展开一个会先收掉另一个）。

```jsx
const [birthday, setBirthday] = createSignal("");
const [brand, setBrand] = createSignal("#2f80ed");
const [attach, setAttach] = createSignal([]);

<form gap={12} padding={16} onSubmit={(e) => save(e.values)}>
  <row gap={8}>
    <label required width={72}>生日</label>
    <datepicker name="birthday" model={birthday} min="1920-01-01" max="2010-12-31" />
  </row>
  <row gap={8}>
    <label width={72} align="right">主题色</label>
    <colorpicker name="brand" model={brand} colors={["#ffffff", "#2f80ed", "#c0392b"]} columns={3} />
  </row>
  <row gap={8}>
    <label width={72}>附件</label>
    <upload name="attach" model={attach} accept=".pdf,.png" multiple />
  </row>
  <button onClick={submit}>保存</button>
</form>
```

- **`datepicker` 只认 `"YYYY-MM-DD"`**（`value` 与 `onChange` 都是它）：非零填充（`2026-1-5`）、
  日历上不存在的日期（`2026-02-30`）一律当"没有值"处理，不是报错也不是猜；
- **`colorpicker` 的 `colors={[]}` 是空色板**（空色板不展开、点了没反应），要"用内置色板"
  就整个不写这个 prop —— 24 色缺省色板有一份名单写在该文件头，想换就整份给全；
- **`upload` 没有对话框后端时什么都不做**（stderr 一次告警），**绝不编造文件名**：静默编造
  会让业务逻辑以为选到了文件，这是比"点了没反应"更坏的失败；
- 三个都在 `gx/a11y` 的焦点序里，键盘行为见 [3.1](#31-焦点与键盘遍历无障碍)：
  日历里 `←/→` 走天、`↑/↓` 走周、`PageUp/PageDown` 翻月，色板里方向键走格（边界停住）。
- 日历与色板**没有为每个格子建节点**（整块自绘 + 几何命中，与 tabs / pagination / rating 同一套），
  所以 `focusOrder()` 里看不到"42 个日期按钮"——需要"跳到某一天"用方向键或 `value`。

## 7. 绘制与动画

### 7.1 自绘画布

`<canvas>` 给脚本一个直接落笔的画布，`onDraw` 收到一个 `ctx`（坐标是画布局部坐标，越界自动裁掉）：

```js
h("canvas", {
  width: 200, height: 80,
  onDraw: (ctx) => {
    ctx.fillRect(0, 0, ctx.width, ctx.height, "#fafafa")   // 铺底
    ctx.line(0, 79, ctx.width - 1, 79, "#ccc")             // 基线
    ctx.fillCircle(24, 30, 14, "#27ae60")                  // 实心圆
    ctx.strokeCircle(60, 30, 14, "#8e44ad")                // 圆环
    ctx.drawText("hi " + count(), 4, 4, 13, "#333")        // 读 signal → 自动重绘
  },
})
```

- ctx 方法：`fillRect(x,y,w,h,color)` / `strokeRect` / `fillCircle(cx,cy,r,color)` /
  `strokeCircle` / `line(x1,y1,x2,y2,color)` / `drawText(text,x,y,size,color)` / `clear(color)`；
  另有只读属性 `ctx.width` / `ctx.height`。
- 颜色写字符串（`"#f00"` / `"red"` / `"rgba(0,0,0,.5)"`），也可以写一个数字当灰度（`0~255`）；
  参数缺失或颜色非法**不会抛错**，按缺省值（黑色 / 0）处理 —— 画歪看得见，比整帧中断好排查。
- **响应式**：`onDraw` 里读到的 signal 变化会自动重绘。读普通变量不会（依赖只看 signal）；
  因此把读 signal 的语句放在函数体前部最稳。
- 画布默认不铺底（与 HTML canvas 一样透明），要底色就 `ctx.clear(...)`/`ctx.fillRect(...)`
  或给 canvas 挂 `background`。不给尺寸时缺省 200×120。
- `disabled` 时每个落笔色自动降饱和。
- 目前只有最近邻/无插值的直线与圆（无抗锯齿、无路径、无变换、无渐变）。

### 7.2 过渡动画

给节点挂 `transition`，它的**数值属性发生变化**时就不再一帧跳到位，而是在指定毫秒内按 ease-out 逐帧逼近：

```js
const [wide, setWide] = createSignal(false)

h("rect", {
  height: 18,
  background: "#2f80ed",
  transition: { width: 400 },          // width 用 400ms 过渡
  width: () => (wide() ? 300 : 60),    // 值一变就开始补间
})

// 简写：所有可动画属性共用同一时长
h("row", { transition: 350, opacity: () => (visible() ? 1 : 0.15) }, /* ... */)
```

可动画属性只有 `width` / `height` / `left` / `top` / `opacity` 五个（`value`、`padding`、`gap` 等
刻意排除，理由见 `agent_doc/gui-component-status.md` §20；`agent_doc/` 过程文档已迁往项目共享资产盘、仅协作者可见，仓库里不留副本）。**首次赋值不做过渡**
（与 CSS 一致），想要入场动画用命令式 API：

```js
import { animate } from "gx/gfx"

const cancel = animate(0, 100, 600, (v) => setProgress(v), () => setDone(true))
// 也可以改元素属性：animate(node, "width", 200, 300)
// cancel() → 停在当前插值
```

`opacity` 是**成组**属性：父节点半透明 = 整棵子树一起淡。
动画期间布局读到的是**插值**，所以兄弟节点会跟着让位。帧驱动复用与光标闪烁同一套 16ms 定时器，
**没有活动动画时不占定时器**（静止零开销）。示例：
[testdata/transition_demo.js](../testdata/transition_demo.js)。

## 8. 响应式与视图

### 8.1 响应式子节点（条件渲染 / 列表渲染）

子节点传函数即为响应式，effect 会自动追踪它读到的信号并在变化时重新挂载：

```js
h("column", null,
  h("button", { onClick: () => setTab(0) }, "首页"),
  h("button", { onClick: () => setTab(1) }, "设置"),

  // 条件渲染：返回元素直接替换
  () => tab() === 0
    ? h("rect", { width: 120, height: 40, background: "#c0392b" })
    : h("rect", { width: 120, height: 40, background: "#2980b9" }),

  // 列表渲染：返回数组即展开成元素列表，增删项自动挂载/卸载
  () => items().map((it) => h("text", null, it)),
)
```

> **⚠️ 最容易踩、且唯一不出声的一条：文本子节点写成快照。**
> JSX 在 parser 层就降级成一次普通调用，子节点是 `h()` 的**实参** ——
> ``<text>count: {count()}</text>`` 等价于 `h("text", null, "count: ", count())`，
> `count()` 在进入 `h()` 之前就求值完了；`h()` 收到的只是一个数字，于是它建成一个**静态**
> `#text` 节点，此后再没有东西回头看那个 signal（实测：信号变了，那个文本节点的指针与内容都不动）。
> 正解是传闭包 ``{() => `count: ${count()}`}`` —— `h()` 走"响应式子节点"分支（slot 占位 + effect），
> 变化时**只改那个文本节点的内容**，不拆树、不碰兄弟节点。
>
> **这条路径运行期没有警告，是刻意的**：属性那边能出声，是因为那些 prop 有"必须是取值函数"的契约
> （`value` / `each` / `show`），收到标量就能判定为漏了括号；而**文本子节点天生收标量**
> （`<text>你好</text>` 本身就是字符串子节点），运行期分不出"静态文本"和"快照" ——
> ``<text>{n}</text>`` 里的 `n` 是普通变量时完全合法。
>
> 但"运行期判不出"不等于"工具兜不了" —— **源码里看得出那是一次调用**，所以静态分析
> 抓得住。用 `gox lint`：
>
> ```bash
> gox lint apps            # 扫整个 apps/（CI 里也跑这一条）
> gox lint src/app.js      # 或只扫一个文件
> ```
>
> 它会把子节点位置的 `{sig()}` 报成 `snapshot-child`（顺带抓子节点区的 `//`、
> `each` 无 `key`、`lib/` 引 `gox`，四条规则全部出自 apps/README.md）。
> 嵌套元素（`<column><text>{n()}</text></column>`）与组件调用（`<Toolbar/>`）不会误报 ——
> 它们在降级后的 AST 上同样长得像"一个调用"，是这条规则最容易踩的坑。
>
> 另有一类**运行期抓得到**的：若子节点是 `obs()` / `computed()` 返回的**对象本身**
> （`{bad}` 而不是 `{bad.value}`），界面上会渲染出 `Rx<…>` 这种乱码且永不更新 ——
> 对象还在，就判得出。dev 模式（`GOX_DEV=1` 或 `gox dev`）下内核会直接告警。
>
> 顺带一条同源的静默坑：**子节点区的 `//` 不是注释**。在 `<column>` 里写一行 `// 说明`
> 会被当成一个**文本子节点**渲染出来（不报错、不出警告）；JSX 注释要写 `{/* … */}`
> （空插值，解析时被跳过），或者把注释挪到 JSX 外面。
>
> **另一条同族的静默坑：三元 / 短路会"吃掉"订阅。** 函数子节点 / 函数 prop 的 effect
> 只追踪**实际读到**的信号 —— 把订阅型读数（`useXxx()` 一族，哪些带订阅见 §9 的分族表）
> 写在某个分支里，首帧走的若是另一条分支，那次调用根本没发生 ⇒ 依赖集为空，
> 之后**永远不重跑**，且没有任何警告：
>
> ```js
> // ❌ 首帧 hasFold() 为 false ⇒ useReservedRegions()() 没被调用 ⇒ 订阅没建立，
> //    后来折叠上报、保留区变了，这条文本也不更新
> {() => hasFold()
>   ? "折痕 " + useReservedRegions()().division.length + " 条"
>   : "未检测到折痕"}
>
> // ✅ 先无条件取一次订阅型读数，再按值分支 —— 订阅恒建立，条件也跟着重跑
> {() => {
>   const r = useReservedRegions()();   // 先订阅
>   if (!hasFold()) return "未检测到折痕";
>   return "折痕 " + r.division.length + " 条";
> }}
> ```
>
> 纯读数（`hasFold()` / `posture()` / `widthClass()` 这类不订阅的）写在哪个分支都没关系
> —— 但需要"跟着环境变"的那次订阅调用必须**无条件执行**；条件本身是纯读数时，
> 也要靠同一 effect 里的订阅型读数把它"带"着重跑。

求值结果按类型分派：元素直接挂载，数组递归展开，`false`/`true`/`null`/`undefined` 渲染为**空**，
字符串与数字渲染为文本，其他对象走 `toString()`。元素 ↔ 标量相互切换时复用同一个内部占位节点，
不会打断其他子节点的布局。

> 函数子节点 map 出来的列表，内容变化按"清空重建"处理 —— 要 keyed 复用（行内状态保留、
> 只重渲染变化的行）用 `each` 指令，见下节。

### 8.2 列表、条件与多分支（元素级指令 + gx/view）

列表与条件渲染是**元素级指令**（写在元素上，对标 Vue 的 `v-for` / `v-show`）；
多分支用 `gx/view` 的 `Switch` / `Match`：

```js
import { Switch, Match } from "gx/view"      // each / show 是 h() 层的指令, 不用 import

<column gap={8}>
  <view each={rows} key="id" fallback={<text>暂无数据</text>}>
    {(row, i) => <text>{(i + 1) + ". " + row.title}</text>}
  </view>

  <view show={open}>
    <input width={150} model={draft} />
  </view>

  <Switch>
    <Match when={() => phase() === "loading"}><progress value={0.5} /></Match>
    <Match when={() => phase() === "error"}><text>出错了</text></Match>
  </Switch>
</column>
```

> **迁移（2026-09-20）**：`<For>` / `<Show>` 组件已移除，改用元素级指令 ——
> `<For each={x} key={k}>…</For>` → `<view each={x} key={k}>…</view>`，
> `<Show when={x} fallback={f}>…</Show>` → `<view show={x} fallback={f}>…</view>`。
> 语义（keyed 复用 / `stable` / `fallback` / keep-alive / 懒构建）一字未改；
> 指令可以写在任何内置元素上，换标签就是"每一项一个盒子"（`<row each={rows}>`）。

- **短写法**：`each={rows}` / `show={open}`（signal 本身就是取值函数，不用再包箭头）、
  `key="id"`（等价于 `key={(r) => r.id}`）。需要派生/过滤时再写函数：`each={() => rows().filter(ok)}`
- `each` 的复用判定是 **key 配对 + item 引用同一性 + 下标**：命中的行原样复用（行内输入框、
  滚动位置、局部 signal 全留着），只就地重渲染真正变了的行；`each` 也接受数字（生成 0..n-1）。
  `stable` 可把下标移出判定（重排 / 中间删除不重建，代价是下标参数停在挂载值）；
  重复 key 会降级为位置键并警告一次（不写坏树）
- `each` / `show` **要收取值函数**：传 `rows()` / `open()` 只拿到一张快照，之后信号再变也不重渲染 ——
  与受控 input 的 `value` 必须传函数是同一条纪律。**写错会出警告**（`each` 收到字符串/对象、
  `show` 收到字符串、忘了括号的 `show={open()}`、`key` 收到数字、`stable` 传函数），
  降级行为不变、只是不再静默
- `show` / `Switch` 走 **keep-alive**：分支懒构建且只构建一次，隐藏只是摘出布局流（子树保活，再显示状态原样）。
  刻意不做 v-if —— 静态子树销毁后重新挂回去是"看着一样但不再响应式"的死树；要该语义用函数子节点
  `{() => cond() ? <X/> : null}`（函数体内每次求值都新建元素）
- 宿主是**透明占位节点**：放进 column 竖排、放进 row 横排，自己不多一层盒子；
  但 `row wrap` 的折行不认它（要折行标签流请用普通 row）

### 8.3 异步资源与生命周期（gx/solid）

```js
import { createResource, onMount, onCleanup } from "gx/solid"

const [data, res] = createResource(fetchRows)   // fetcher 同步立即 ready，异步先 pending
// res.state() ∈ pending / ready / refreshing / error
// res.error() 读错误、res.refetch() 重取（latest-wins：过期响应丢弃）
// error 态 data() 不抛 —— 返回上一次成功的值（从未成功则 undefined）

onMount(() => console.log("子树挂上"))           // 登记到"当前正在构建的响应式子树"
onCleanup(() => console.log("子树换代 / 销毁"))   // 顶层调用是 no-op（警告一次）
```

示例：[testdata/view_demo.js](../testdata/view_demo.js)（For keyed 复用 / Show 保活 / Switch 分支）、
[testdata/resource_demo.js](../testdata/resource_demo.js)（pending→ready / refetch 保旧值 / 失败后恢复）、
[testdata/kit_demo.js](../testdata/kit_demo.js)（设计套件：令牌主题 + 变体按钮工厂 + 装饰卡片）。

### 8.4 自适应断点（gx/viewport）

断点是**命名阈值**：窗口宽度越过阈值就换一档。默认表 `sm:0 / md:600 / lg:840 / xl:1200`（dp），
与尺寸类（`widthClass()` 的 compact/medium/expanded，阈值 600/840dp）复用同一组数字 ——
前者是业务可自定义的"命名"，后者是内核对"物理宽度档"的分类。

```js
import { useBreakpoint, matchBreakpoint, breakpoints, setBreakpoints } from "gx/viewport";

const bp = useBreakpoint();                 // () => "sm" | "md" | "lg" | "xl"（取值 + 订阅）

const layout = () => {
  bp();                                     // ← 先无条件订阅（matchBreakpoint 自身不订阅）
  return matchBreakpoint({
    sm: { cols: 1, side: false },
    md: { cols: 1, side: true },
    lg: { cols: 2, side: true },
    xl: { cols: 2, side: true, info: true },
  });
};

h("text", null, () => `breakpoint = ${bp()} / cols = ${layout().cols}`);
```

- `breakpoints()` 读表；`setBreakpoints({...})` 整表替换（自定义阈值，例如 `{phone:0, tablet:720, desk:1100}`）；
  `resetBreakpoints()` 复位。非法项静默跳过，全非法时保持原表并告警（清空表会让 `breakpoint()` 永远返回空串，更难查）。
- `above(name)` / `below(name)` / `between(a, b)` 是三个区间判定（未知档名一律 false；`between` 是半开区间 `[a,b)`，参数反序等价）。
- **按窗口宽度算，不是屏幕宽度**：多窗口/分屏下每个窗口各算各的（客户区宽度 ÷ 所在显示器缩放）。
- 订阅纪律同 §8.1：`useBreakpoint()` 是订阅型读数，必须**无条件调用**（写在三元分支里会漏掉订阅）。

示例：[testdata/breakpoint_demo.js](../testdata/breakpoint_demo.js)（同一份代码在四个尺寸下自动换形态，按钮直接 resize 到四档）。
完整 API 与坐标口径见 [multi-window.md](multi-window.md)。

### 8.5 computed 求值抛错：`.value` 抛出原始抛出值

`computed(fn)` 的契约是 **`.value` ≡ 调用 `fn()`**。`fn()` 会抛，读 `.value` 也就该抛 ——
而且抛出的是**原始抛出值**（`throw x` 的 `x` 本身），不降级成字符串、不包装成 `Error`：

```js
const total = computed(() => {
  if (qty.value < 0) throw new RangeError("qty 不能为负");
  return price.value * qty.value;
});

total.value      // qty < 0 时真的抛出那个 RangeError 实例（=== 抛出侧）
total.error      // 同一个实例，但**不抛** —— 给"渲染错误态而不是炸掉"的出口
```

三条边界，都是踩过的坑：

| 边界 | 行为 | 为什么 |
|---|---|---|
| 失败态**会缓存** | 失败后 `fn` 不再重跑，直到依赖变化或 `refresh()` | 否则每读一次 `.value` 就执行一次 `fn`，带副作用的计算会被执行 N 遍 |
| 失败态**仍追踪依赖** | 依赖变到一个能算出值的状态后自动恢复 | 否则会永久锁死在上一次的错误里，即使依赖已经变好 |
| 失败**不通知监听者** | 进入错误态时不触发 `listen` 回调；恢复到正常值才触发 | 一次求值失败不该把整条 effect / 渲染链路打断 |

所以「抛错的 computed 会不会把 effect 链路断掉」的答案是：**不会断**。错误只在**读取点**暴露
（`.value` 抛 / `.error` 不抛），怎么处置由读的人决定。

GUI 里推荐这样写 —— 不要指望 try/catch 兜住渲染过程：

```js
// ✅ 读 .error 渲染错误态，链路照常跑
{() => total.error ? "数量不合法" : `合计 ${total.value}`}

// ❌ 直接读 .value：一旦算错就是一次未捕获异常
{() => `合计 ${total.value}`}
```

`console.log(computed)` / 字符串拼接走的是诊断路径，不会因为处在错误态而抛，
也不会往回调桥里写错误信号（否则一个纯诊断动作会变成"某处抛错了"）。

## 9. 宿主能力

### 9.1 原生系统对话框

`gx/dialog` 把系统消息框与"打开文件"对话框直接接到脚本上：

```js
import { alert, confirm, openFile, saveFile } from "gx/dialog"

await alert("All changes have been saved.", "Gox")          // 只有一个"确定"
const yes = await confirm("Delete this file?", "Please confirm")  // → true / false
const path = await openFile({                               // → 完整路径 / null（取消）
  title: "Pick a source file",
  filter: [
    { name: "Text files", pattern: "*.txt;*.md" },
    { name: "All files",  pattern: "*.*" },
  ],
})
const out = await saveFile({ default: "report.txt" })   // → 确认保存的路径 / null（取消）
```

三件事值得留意：

- **是 async 的**（与同步的剪贴板不同）。实现是"同步落地 + 异步外观"：Go 侧真的阻塞到用户作答，
  Promise 的 resolve 投回脚本事件循环 —— 所以 `await` 之后的代码在对话框关掉前不会执行。
- **模态期间界面不冻结**：Windows 以主窗口为 owner 自动泵模态消息；macOS 走 NSAlert/NSPanel 的
  `runModal`（AppKit 官方嵌套 run loop），重绘 / 拖动都正常，也**不需要**自己写 goroutine 或消息循环。
- **取消不是错误**：`openFile` / `saveFile` 取消返回 `null`（与浏览器 File System Access API 一致），不必 try/catch。
  **没有原生能力的后端会降级**：内容写到 stderr 并立刻返回（`confirm` 取 true、`openFile` / `saveFile` 取 null）。

> 语法提示：事件处理器的两种写法都可以：`onClick: async function () { ... }` 或
> `onClick: async () => { ... }`（箭头形式的 `this` 是词法的，要拿外层 `this` 就用它）。

示例：[testdata/dialog_native_demo.js](../testdata/dialog_native_demo.js)。

### 9.2 剪贴板

`gx/gfx` 导出两个**同步**函数（脚本与窗口在同一个 OS 线程，直接调原生 API 即为正确的线程，不需要 `await`）：

```js
import { clipboardReadText, clipboardWriteText } from "gx/gfx";

const ok = clipboardWriteText("hello");   // → true / false
const s = clipboardReadText();            // → 字符串，读不到时为空串
```

拿不到剪贴板（被别的进程占着，或后端不支持）时**静默降级**：写返回 `false`、读返回空串，不抛异常。
Windows 后端走 `OpenClipboard` + `CF_UNICODETEXT`（打开失败会重试 5 次 × 20ms，期间 UI 冻结 ≤100ms）；
Linux（X11）暂未实现。示例：[testdata/clipboard_demo.js](../testdata/clipboard_demo.js)。

### 9.3 应用数据持久化（gx/storage）

```js
import { setAppName, setStorage, getStorage, getStorageInfo } from "gx/storage"

setAppName("MyApp")                     // 决定数据落在哪个子目录（缺省从脚本文件名推）
setStorage("theme", "dark")
getStorage("theme") ?? "light"           // → dark（缺失时返回 undefined, 没有默认值参数）
getStorageInfo()                        // → { keys, currentSize, limit }
```

数据落在 `os.UserConfigDir()/Gox/<应用名>`（Windows `%AppData%`、Linux `~/.config`、
macOS `~/Library/Application Support`）；可用环境变量 `GOX_STORAGE_DIR` 改写根目录。

两个刻意取舍：**存储文件损坏按空存储处理并告警**（数据坏了不该让应用起不来），
但**写入失败抛错**（静默丢持久化数据更难排查）；存函数 / 循环引用在写入时抛 TypeError。
另有 `removeStorage` / `clearStorage` / `appDataDir`。
示例：[testdata/storage_demo.js](../testdata/storage_demo.js)。

### 9.4 菜单栏与右键菜单

菜单栏是**自绘**的（不走 win32 菜单 API），所以三个平台观感一致。它只是个普通容器 ——
脚本自己写 `column { menubar; 内容 }`，gfx 不会往根节点里偷偷插一条：

```js
import { h, render, openContextMenu } from "gx/gfx";

render(
  h("column", null,
    h("menubar", null,
      h("menu", { label: "File" },
        h("menuitem", { label: "New",  shortcut: "Ctrl+N", onClick: () => say("New") }),
        h("menuitem", { label: "Open", shortcut: "Ctrl+O", onClick: () => say("Open") }),
        h("separator", null),
        h("menuitem", { label: "Save As", disabled: true }),
        h("menuitem", { label: "Theme" },                  // 内嵌 menu = 子菜单
          h("menu", null,
            h("menuitem", { label: "Dark",  onClick: () => say("Dark") }),
            h("menuitem", { label: "Light", onClick: () => say("Light") })))),
      h("text", null, "ready")),                          // 菜单栏里放别的标签按固有尺寸顺排
    h("column", { padding: 16 },
      h("rect", {
        width: 320, height: 160, background: "#e8eef7",
        onContextMenu: (e) => openContextMenu(e.x, e.y, [
          h("menuitem", { label: "Copy", onClick: () => say("Copy") }),
          h("menuitem", { label: "Paste", onClick: () => say("Paste") }),
        ]),
      }))),
  { title: "Menu", width: 480, height: 380 });
```

- **下拉是弹层**：溢出 26px 的菜单栏显示，不被裁剪也不被后面的兄弟盖住；点外部收起（该次点击被吞掉，
  不会顺带按到下面的控件）；点菜单栏另一个标题直接换过去（互斥）。
- **键盘**：焦点在菜单栏时 ←/→ 在标题间循环（换过去就开着），↓/Enter/Space 展开，Esc 收起。
  **Esc 的优先级是"菜单 > 下拉框 > 对话框"**，一次只关一层。
- **快捷键**由 Go 侧一张表在事件泵层匹配，**菜单不必展开**就能用。只认带 `Ctrl`/`Alt` 的组合
  （不然菜单里写 `shortcut="S"` 会让整个应用打不出 `s`），且修饰键**全等**
  （`Ctrl+S` 不会被 `Ctrl+Shift+S` 触发）。命中回落 `onClick({x, y, shortcut: "Ctrl+S"})`。
- **右键菜单走数据式 API**（`openContextMenu(x, y, items)`）而不是 `contextMenu` prop：
  JSX 元素是**单次挂载**的对象（一个节点只有一个 `Parent`），做成 prop 的话同一个 `<menu>`
  挂到多个组件上会互相争抢 `Parent` —— 每个使用点都得重新 `h()` 一次，与直接调 API 没区别。
- 越界会自动向左/上翻折（在窗口右下角右键也能看到整块菜单）；
  在右键菜单上再点右键会被吞掉（不换位置、不重弹）。

示例：[testdata/menu_demo.js](../testdata/menu_demo.js)。

### 9.5 多窗口

`render()` 可以调用多次，每次开一个独立窗口 —— 各有自己的元素树、焦点、交互态与事件循环：

```js
import { h, render } from "gx/gfx";
import { createSignal } from "gx/solid";

const n = createSignal(0);                       // 想跨窗口共享状态就在顶层建信号

function counter(title) {
  return h("column", { padding: 12, gap: 8 },
    h("text", null, title),
    h("text", null, () => "count = " + n()),
    h("button", { onClick: () => n(n() + 1) }, "+1"),
    h("button", { onClick: () => wB.close() }, "close me"));
}

const wA = render(counter("Window A"), { title: "A", width: 320, height: 200 });
const wB = render(counter("Window B"), { title: "B", width: 320, height: 200 });
```

- `render()` 返回**窗口句柄**：`w.close()` 关掉这个窗口，`w.isClosed()` 查状态。
  关闭是**异步受理**的（内部经 `Post` 投回 GUI 线程，避免在遍历注册表时改注册表），
  返回时窗口可能还没真正消失；关窗口是幂等的。
- 句柄还能改标题与尺寸：`w.title()` 读、`w.setTitle(t)` 写（做成方法而非属性 ——
  属性值构造时被快照，会永远返回旧标题）、`w.resize(width, height)` 改**客户区**尺寸
  （与 `<window width height>` 同口径），支持的后端会连带触发 `onResize`（于是
  useWindowSize 断点布局跟着自动切）；`resize` 缺参 / 非数字抛 TypeError，数值 ≤0 静默拒绝，
  后端不支持时 no-op（`title()` 仍读得回最近设置的值）。
- **关一个，其余继续跑**；只有**全部窗口都关掉**，事件循环才退出、进程才结束。
- 每个窗口是**各自跑一遍组件函数**：两张窗口的节点不共享。想同步状态就在脚本顶层
  共享 `createSignal`（如上例的 `n`），别指望同名全局变量自动串起来。
- 每个窗口**焦点独立**：在 B 里点击不会把 A 的焦点框带过去。
- 事件泵对多个窗口是**切片轮询**（Windows 有线程级消息队列，可共享；X11 是单连接、
  没有共享队列，对第一个窗口无限期阻塞会饿死其余窗口）—— 所以多窗口下等待上限是
  32ms 的有界轮询，单窗口仍是原来的阻塞零空转。
- `gfx.Post` 的任务在当前版本是**广播**（所有窗口的泵各处理一次）；现有任务都幂等。

示例：[testdata/multiwindow_demo.js](../testdata/multiwindow_demo.js)（开两个窗口各自计数，
`File - Close window` / `Ctrl+Q` 关掉当前窗口，关一个另一个继续跑）。

**窗口几何（M4）** —— 句柄还能读写窗口位置：

```js
const w = render(<window title="geo" width={320} height={200}>…</window>);
w.moveTo(40, 60);                                  // 当前屏工作区内移动（缺参抛 TypeError）
w.center();                                        // 当前屏工作区居中
w.position();                                      // { x, y }（工作区相对，设备像素）
w.bounds();                                        // { x, y, width, height, displayId, scale }
w.display();                                       // 所在显示器 id
```

坐标口径是**窗口外框左上角相对其所在显示器工作区左上角**（设备像素）—— 详见
[multi-window.md](multi-window.md) §1。把窗口拖到副屏后，`moveTo(0,0)` 就是"副屏工作区左上角"。

**窗口列表 / 跨屏事件（M4）**：

```js
import { windows, window, onWindowDisplayChange, useWindowDisplay } from "gx/screen";

windows();   // [{ id, title, scope, x, y, width, height, scale, displayId, active, focused }, …]
window(3);   // 单个窗口条目；不存在 → null

const off = onWindowDisplayChange(({ windowId, fromDisplay, toDisplay }) =>
  console.log(`win ${windowId}: ${fromDisplay} -> ${toDisplay}`));
off();       // 注销
```

**应用接续（M8）** —— 把一条导航栈（含 `route.state`）从一个作用域**搬迁**到另一个：

```js
import { createRouter, RouterView, useRouter } from "gx/router";

const router = createRouter({ routes: [ /* … */ ], initial: "/" });
const wa = render(<window title="home">{() => RouterView()}</window>);
const wb = render(<window title="road">{() => RouterView()}</window>);

await router.handoff(wa, wb);                        // 搬迁：road 接住整条栈，home 复位回首页
await router.handoff(wa, wb, { keepSource: true });  // 复制：home 不动，road 拿克隆
router.continuity();                                 // 只读内省：谁持有哪条栈（接续前体检）
```

`handoff` 与 `router.sync` 的镜像/共享/跟随模式不同：sync 之后源窗口仍在原页面继续存在，
handoff 之后源被腾空（回到栈底/首页）—— 这才是"接续"的物理动作。完整语义、返回值与
"跨设备接续不做"的边界见 [multi-window.md](multi-window.md) §6/§8。

示例：[testdata/multiscreen_demo.js](../testdata/multiscreen_demo.js)（两窗口 + 跨屏事件 + 接续搬迁/复制）。
#### 窗口管理：层级 / 约束 / 全屏 / 光标 / 模态

> **两套 API 的分工（2026-10-02 合流后）**：**位置**归上面 M4 那一套
> （`moveTo` / `center` / `position` / `bounds` / `display`，口径是"工作区相对 + 设备像素"）；
> **窗口系统态**归本节（层级 / 尺寸约束 / 缩放开关 / 全屏 / 光标 / 模态）。
> 位置相关的三个方法不在这里重复定义 —— 同一个后端类型上不可能有两个同名
> `MoveTo`，gfx 层也只保留了一处坐标换算（`ResolveWindowPlacement`）。

窗口自身的几何与系统态既能在根元素上声明，也能在句柄上运行时改：

| `<window>` prop | 取值 | 说明 |
|---|---|---|
| `x` / `y` | 整数 | 初始位置，**相对目标显示器工作区**（外框左上角、设备像素），与 `moveTo` 同口径。**不写（或写哨兵 `DefaultWindowPos`）就不干预** —— 交给系统决定 |
| `display` | 字符串 | 目标显示器 id（`gx/screen` 的 `screens()[].id`，也收数字序号）。给了它，`x` / `y` 就相对**那台屏**的工作区；只给 `display` 不给 `x`/`y` = 在那台屏上居中 |
| `minWidth` / `minHeight` / `maxWidth` / `maxHeight` | 整数 | 尺寸约束；`0` = 该方向不限制 |
| `resizable` | 布尔 | 允许用户拉伸窗口（缺省真）；`resizable={false}` 同时关掉最大化按钮 |
| `fullscreen` | 布尔 | 开窗即全屏 |
| `level` | `"normal"` / `"top"` / `"bottom"` | 窗口层级（置顶/置底）；认不出的值退回 `normal` |
| `modal` | 布尔 / 窗口句柄 | 把本窗口设成**模态子窗口**：`modal` 或 `modal={true}` 用**当前活动窗口**当父窗口，`modal={wParent}` 指定父窗口 |

> `h()` 手拼树时 `render(tree, {…})` 的配置对象支持同一批键（外加一个 `parent`，
> 与 `modal: true` 配对使用，等价于 `modal: <句柄>`）。约束里的 `0` / 负数一律按
> **不约束**处理 —— 负数在各平台表现不一（win32 上会得到一个拖不动的怪窗口），在入口归一最省事。

对应的方法（都挂在 `render()` 返回的窗口句柄上，`w` 即句柄）：

```js
const w = render(tree, { title: "Inspector", width: 320, height: 200, x: 480, y: 120 })
w.setConstraints({ minWidth: 280, maxWidth: 640, minHeight: 160 })
w.setResizable(false); w.isResizable()       // → false
w.setFullscreen(true); w.isFullscreen()      // → true
w.setLevel("top"); w.level()                 // → "top"
w.activate()                                 // 提到前台（抢焦点）
w.setCursor("grab")                          // 改窗口级光标形状
w.setCursor(null)                            // 清掉窗口级覆盖，回到按悬停节点决定
```

位置/几何（`moveTo` / `center` / `position` / `bounds` / `display`）见上面 M4 那一节。

- **移动事件**：窗口被拖动时向布局根派发 `onMove({x, y})`
  （win32 `WM_MOVE` / cocoa `windowDidMove` / X11 `ConfigureNotify`，三平台都上报）。
  **建窗期的位置落地不上报** —— 脚本本来就知道窗口被放在哪（给过 `x`/`y`，或者接受了居中），
  把它当真实移动上报会让"按顺序收头几个事件"的调用方平白多收一串 `EventMove`。
  载荷 `{x, y}` 与下一段的**坐标口径表完全一致**（所在显示器工作区相对 + 设备像素）——
  后端填的是平台原生绝对坐标，内核在派发前换算一次，于是 `onMove` 收到的值可以
  **原样喂回 `moveTo`**（"挪回去 / 按落点吸附"不必自己查屏几何）：
  `onMove` 报 `(a, b)` ⇒ `moveTo(a, b)` 把窗口放回原处。
- **坐标口径**（`x` / `y` / `moveTo` / `position()` / `bounds()` / `onMove` 共用一套）：

  | 轴 | 口径 |
  |---|---|
  | 原点 | **左上**（与 CSS 一致；macOS 内部是左下，换算在 cocoa 后端做掉） |
  | 单位 | **设备像素**（不是点/逻辑像素）—— macOS 上进出都乘除 `backingScaleFactor` |
  | 参照物 | **所在显示器的工作区左上角**（排除任务栏/状态栏），即 `moveTo(0,0)` = 贴着该屏工作区左上角 |
  | 尺寸 | `width` / `height` 是**内容区**尺寸，不是外框 —— 两者差一条标题栏，这是平台事实，不做换算（换不准） |

  > **跨屏后单位会重新对齐**（macOS）：窗口被拖到缩放不同的屏上（或那块屏的缩放设置被改）时，
  > 同一个"点"尺寸对应的设备像素变了，所有以设备像素为准的量都会**当场重算并重新上报** ——
  > 位置与尺寸各补一个 `onMove` / `onResize`，尺寸约束按新比值重新落地，输入坐标换算随之切换。
  > 脚本侧不用做任何事。建窗时也会在窗口落到目标屏之后对齐一次（笔记本 Retina 主屏 + 外接
  > 1080p 是最常见的组合，按主屏猜会让整块屏的鼠标坐标差一倍）。
  > Windows 的坐标本来就是物理像素（Per-Monitor V2 DPI 感知）、Linux 恒为像素，都没有这个问题。

- **光标形状**：任意节点可挂 `cursor` prop（沿父链继承），悬停到它上面时自动切形状，
  值域取 CSS 的那一套（`default` / `pointer` / `text` / `crosshair` / `move` / `grab` /
  `grabbing` / `wait` / `progress` / `help` / `not-allowed` / `ew-resize` / `ns-resize` /
  `nwse-resize` / `nesw-resize` / `col-resize` / `row-resize` / `none`，以及 `hand`、`ibeam`
  这类别名）。`input` / `search` / `textarea` 缺省就是 `text`，`button` / `menu` / `tab` 这类可点
  控件缺省 `pointer` —— 不必逐个写。**认不出的形状退回 `default`**，不报错。
  `w.setCursor(null)` 清掉窗口级覆盖，回到"按悬停节点自动解析"；同一个形状重复设置
  不会重复调平台 API（悬停时每帧都会算一次形状）；
- **模态子窗口**：`modal` 为真时，父窗口在子窗口关掉之前**收不到任何输入**
  （键盘与鼠标都被拦住，窗口仍可拖动/缩放/关闭）。`isBlocked()` 查父窗口当前是否被挡，
  `isModal()` / `modalParent()` / `modalChild()` 查这层关系。模态是**覆盖式**的
  （同一父窗口只有一个模态子窗口，新的顶掉旧的），**关父窗口会连带关掉子窗口**，
  父窗口不存在时静默降级为普通窗口。

平台支持与降级（**没实现的就当没有，不报错**）：

| 能力 | Windows | Linux | macOS |
|---|---|---|---|
| 位置 / 居中 / 读几何 | `SetWindowPos` / `GetWindowRect` | EWMH + ConfigureNotify | `setFrameOrigin:` |
| 尺寸约束 | `WM_GETMINMAXINFO` | `WM_NORMAL_HINTS` | `setMinSize:` / `setMaxSize:` |
| 缩放开关 / 全屏 | 样式位 + 全屏切换 | `_NET_WM_STATE_FULLSCREEN` | `setStyleMask:`（同步改样式位，不用异步的 `toggleFullScreen:`） |
| 层级 `top`/`bottom` | `HWND_TOPMOST` / `HWND_BOTTOM` | `_NET_WM_STATE_ABOVE` / `_BELOW` | 窗口 `level` |
| 光标形状 | `WM_SETCURSOR` + `LoadCursor` | 暂无（静默降级为默认箭头） | NSCursor |
| 跨屏后缩放比刷新 | 无需（坐标本就是物理像素） | 无需（恒为像素） | `windowDidChangeBackingProperties:` |
| 模态与 `onMove` | 支持 | 支持 | 支持 |

> 读几何与"外框"口径：Windows / macOS 由平台直接给外框矩形，**Linux 下 `WindowBounds()`
> 读的是客户区**（`TranslateCoordinates` 到根窗口）—— reparenting WM 会给窗口套一层
> 自己的装饰，于是 `moveTo` 与 `bounds()` 之间差一圈标题栏。X11 后端整体标注为
> "未经实机验证"，这处落差要靠 `_NET_FRAME_EXTENTS` 实机核对，暂时按协议原义实现。

> 移动 / 缩放 / 全屏的**结果**以平台回报为准（`bounds()` 读的是平台现值，不是脚本设的期望值）——
> 用户手动拖过窗口之后，读到的就是拖动后的位置。约束、层级、全屏在**首帧之前**就应用，
> 所以不会出现"先闪一下普通窗口再变全屏"。

### 9.6 原生能力层

六个模块（`gx/device` · `gx/app` · `gx/geo` · `gx/media` · `gx/permission` · `gx/viewport`）
共用**同一个宿主契约**，不是六套机制：

    gx/device      设备信息、电量、网络、震动、屏幕亮度与常亮、打开系统设置页
    gx/app         前后台状态、内存告警、返回键、分享、退出、屏幕方向
    gx/geo         定位（取一次 / 持续监听）、两点距离
    gx/media       拍照、选图 / 选视频、保存图片、预览
    gx/permission  权限查询 / 申请 / 打开应用设置页
    gx/viewport    安全区 insets、软键盘高度、分屏与多窗口形态、宽度档、折叠保留区

**三种调用形态**是这一层的设计核心，示例脚本里各演示一遍：

| 形态 | 例子 | 写法 |
|---|---|---|
| 拉取型（同步可得） | `deviceInfo()` | 直接读返回值 |
| 动作型（要等用户 / 系统） | `takePhoto()` / `getLocation()` | `await`；失败 reject |
| 上报型（宿主主动告知） | `battery()` / `useBattery()` | 同步读快照；`useXxx()` 返回**取值函数**，宿主上报时自动刷新 |

```js
import { deviceInfo, battery, canIUse } from "gx/device";
import { getLocation } from "gx/geo";
import { takePhoto } from "gx/media";

console.log(deviceInfo().platform, battery().level);   // 拉取型 / 上报型

if (canIUse("camera")) {            // ← 事前判断，而不是靠 catch 兜底
  const photo = await takePhoto({ count: 1 });
}

try {
  const loc = await getLocation({ highAccuracy: true });
} catch (e) {
  if (e.errCode === "permission-denied") console.warn(e.message);
}
```

**错误码一共八个**（跨平台统一，平台差异被压进这几个词里）：`unsupported` /
`permission-denied` / `cancelled` / `timeout` / `busy` / `unavailable` / `platform-error` /
`invalid-arg`。异常对象同时带 `errCode`+`errMsg`（做判断用）与 `name`+`message`（直接打印用）。
**`cancelled` 是用户主动取消，不是失败。**

**缺能力时不软降级**：静默返回假数据会污染业务逻辑（你以为拍到了，其实没有）。所以动作型 API
缺能力一律 reject（先用 `canIUse` 判断）；只有"纯上报型"状态（电池 / 网络 / 前后台 / 安全区）
在没人上报时给**明确的缺省值**（`supported: false` / `connected: false` / insets 全 0），
这与 `canIUse` 的结论一致。

**桌面上的行为**：这一层主要为移动端设计。桌面上能真实给出的（电量、网络、亮度、屏幕常亮、
打开设置页、震动软降级）就给真值；给不出的一律明说 —— 异步 API 报 `unsupported`。桌面**不实现**
相机 / 定位 / 相册，也不瞎编。

**`gx/screen` 与 `gx/viewport` 别混**：前者答"我这台设备是什么样"（显示器 / 姿态 / 折痕），
后者答"我这个窗口被怎么摆"（安全区 / 键盘 / 分屏）。折叠屏展开后刘海在左上、折起来在顶部 ——
同一台设备两种 insets，所以后者挂在**窗口**上。

> 写宿主或做测试时才需要碰上报通道：`gfx.ReportBattery` / `ReportNetwork` / `ReportAppState` /
> `ReportPermission` / `ReportLocation` / `ReportViewport`，在系统回调里调（GUI 线程），
> 内核存快照并把环境版本 +1，脚本侧 `useXxx()` 跟着变 —— 与 `gx/screen` 的 `reportPosture`
> 同一套机制。折叠屏另有 `gfx/mobile.ReportDisplayFold`（表见 [app/NATIVE-HOST.md](../app/NATIVE-HOST.md)）。

#### 折叠屏：三件套读数 + 一个判定入口

折叠屏的独有能力不是"屏幕更大"，而是**屏幕被折痕切成两段、用户物理上只能看一段**。
框架只出信号（姿态 / 折痕 / 保留区）与一个联合判定入口，**布局形态由应用决定** ——
这是官方立场（华为"不推荐用折叠状态监听接口实现响应式布局"）与内核既有立场
（"是否分栏由姿态决定，折痕只负责怎么分"）的共同结论。

```js
import { reservedRegions, hasFold, layoutMode } from "gx/viewport";
import { posture, hinge, regions, splitRatio, hingeOrientation } from "gx/screen";

const r = reservedRegions();        // 纯读数（不订阅）: { division:[…], occlusion:[…], all:[…] }
const m = layoutMode();             // 同上: { posture, widthClass, foldAware, suggested }
```

| 读数 | 返回 | 要点 |
|---|---|---|
| `reservedRegions(win?)` | `{division, occlusion, all}` | 三个键恒存在（空时是空数组），`reservedRegions().division.length` 永远可写 |
| `hasFold(win?)` | bool | **结构性**信号：有折痕的机器恒 true，不随折叠/平展 flip-flop（否则列数会跟着姿态跳） |
| `layoutMode(win?)` | `{posture, widthClass, foldAware, suggested}` | `suggested` ∈ `"single"` / `"dual"` / `"tablet"`。**只给建议，框架不改布局** |
| `splitRatio(win?)` | number | 折痕分割比例，钳 [0.2, 0.8]；无折痕退化 0.5 |
| `hingeOrientation(win?)` | string | `"vertical"`（左右折，折痕是竖条）/ `"horizontal"`（上下折） |
| `useReservedRegions(win?)` | `() => {division, occlusion, all}` | **两段式**：外层绑窗口返回 getter，内层 `get()` 才订阅 + 取值。**同时**订阅两个信号（窗口环境 + 屏表）—— 只读一个是常见疏漏，症状是"折一下不更新，转个屏才更新" |
| `useLayoutMode(win?)` | `() => {posture, widthClass, foldAware, suggested}` | 同上两段式，`layoutMode()` 的订阅版 |

**订阅型 vs 纯读数**（"会不会跟着环境变"的分族；把订阅型写进三元 / 短路的某条分支是静默坑，见 §8.1）：

- **订阅型**（调用时读版本号信号 = 建立订阅，环境变化会让引用它的 effect 重跑）：
  - **两段式**（外层绑窗口返回 getter、内层才订阅 + 取值，**少写一层括号拿到的是函数对象，属性全是 undefined**）：
    gx/screen 的 `useScreen()()` / `useScreens()()` / `usePosture()()` / `useWindowInfo()()`；
    gx/viewport 的 `useReservedRegions()()` / `useLayoutMode()()`
  - **一段式**（调用即订阅）：gx/viewport 的 `useViewport()` / `useInsets()` / `useKeyboardHeight()` / `useMultiWindow()`
- **纯读数**（每次读当前快照，**不订阅**）：gx/viewport 的 `viewport()` / `insets()` / `keyboardHeight()` /
  `keyboardVisible()` / `multiWindow()` / `isSplit()` / `splitInfo()` / `contentArea()` / `safeAreaStyle()` /
  `widthClass()` / `isCompactWidth()` / `isMediumWidth()` / `isExpandedWidth()` / `isTabletLayout()` /
  `reservedRegions()` / `hasFold()` / `layoutMode()`；gx/screen 的 `posture()` / `hinge()` / `regions()` /
  `splitRatio()` / `hingeOrientation()` / `windowInfo()`

纯读数不是"错的"：`hasFold()` 是结构性信号（有折痕的机器恒 true），事件回调（`onViewportChange` /
`onDisplayChange`）里读纯读数也够用。**错的是把订阅型读数写进三元 / 短路的某条分支** ——
首帧没走到的分支不建立订阅，effect 零依赖、永不重跑且无警告（§8.1）。

**避让（opt-in）**：内核元素默认**不**避让保留区（历史行为不变，避免老界面莫名位移）。
要避让就显式声明：

```jsx
<row avoidReserved="division">      {/* 只避折痕；"occlusion" 只避遮挡；"all"/true 全避 */}
  …
</row>
```

`avoidReserved` **不适用于 `<scroll>`**：滚动容器里收缩可用区会让内容与滚动条语义打架
（会打一条一次性告警并忽略）。需要在滚动区里避让时，自己在内容外层加一层带
`avoidReserved` 的容器。

**三条最容易踩的**（都是静默失效，不报错）：

1. **别用姿态推布局**。上下折（Pocket 系列）展开后宽度**可能仍 < 600dp**，
   用 `posture === "flat"` 推"该变宽了"必然翻车；而且官方时序里 `windowSizeChange`
   早于 `foldStatusChange(展开态)` ⇒ 用姿态驱动会落后一帧。**布局只信宽度。**
2. **没人上报 ⇒ 姿态恒 `flat`**。Windows / X11 没有姿态查询 API —— 不是 bug，
   是"提供通道不猜姿态"。排查第一步用 `posture()` 确认读到的是不是 `"half-open"`。
3. **折痕坐标是显示器坐标**（设备像素）。全屏时等于窗口坐标，分屏 / 自由窗口下不等价 ——
   要换算就减去窗口在屏上的原点（`windowInfo().x / .y`）。

示例：[testdata/native_demo.js](../testdata/native_demo.js)（设备信息面板，三种形态各一遍）。

## 10. 调试

`gx/dev` 提供运行时快照 `devSnapshot()`（帧统计 / 缓存命中 / 最近警告，可做调试面板）：

```js
import { devSnapshot } from "gx/dev";
```

示例：[testdata/dev_panel_demo.js](../testdata/dev_panel_demo.js)。

## 11. 示例索引

`testdata/` 下的 GUI 示例均可直接用 `./gox <file>` 运行：

**布局与绘制**

| 示例 | 内容 |
|---|---|
| [elastic_layout_demo.js](../testdata/elastic_layout_demo.js) | 百分比三等分 / 加权收缩 / 标签流折行，拖窗口全程零 JS |
| [grid_demo.js](../testdata/grid_demo.js) | 等宽列网格 + 渐变圆角卡片 |
| [multiline_demo.js](../testdata/multiline_demo.js) | 自动换行 / 省略号 / 硬截断三态对照 |
| [canvas_demo.js](../testdata/canvas_demo.js) | 自绘画布：signal 驱动柱状图 + ctx 原语展示 |
| [transition_demo.js](../testdata/transition_demo.js) | 过渡动画：宽度 / 成组淡出 / 位移 / 命令式 animate |

**控件与交互**

| 示例 | 内容 |
|---|---|
| [form_demo.js](../testdata/form_demo.js) | 表单控件 |
| [button_demo.js](../testdata/button_demo.js) | 按钮三态 |
| [progress_demo.js](../testdata/progress_demo.js) | 进度 / 分隔 / 占位 |
| [select_demo.js](../testdata/select_demo.js) | 受控下拉框 |
| [slider_demo.js](../testdata/slider_demo.js) | 滑块：受控值 / 量程 / 禁用三态 |
| [input_demo.js](../testdata/input_demo.js) | 单行输入与实时镜像 |
| [textarea_demo.js](../testdata/textarea_demo.js) | 多行编辑器 |
| [ime_demo.js](../testdata/ime_demo.js) | 输入法：候选词整批提交与光标跨批 |
| [scroll_demo.js](../testdata/scroll_demo.js) | 滚动容器与边界冒泡 |
| [scroll_to_demo.js](../testdata/scroll_to_demo.js) | 滚动位置**写入口**：`scrollTop` 跟随最新 / 回到顶部 / 跳到第 N 行 + `onScroll` 回读 |
| [vlist_demo.js](../testdata/vlist_demo.js) | 虚拟化长列表：十万行只物化十几行，滚动条与全量版一致 |
| [image_demo.js](../testdata/image_demo.js) | 图片五态：自然尺寸 / 放大 / 缩小 / 坏路径占位 / 禁用 |
| [video_demo.js](../testdata/video_demo.js) | 视频框三态：封面 contain / 无封面占位 / `fit=cover` + 用户 background（桌面后端降级为封面 + 一次 `onError`） |
| [events_demo.js](../testdata/events_demo.js) | 鼠标 / 滚轮 / 右键 / 修饰键 |
| [focus_demo.js](../testdata/focus_demo.js) | 焦点框与 focus/blur |
| [a11y_form_demo.js](../testdata/a11y_form_demo.js) | 无障碍键盘走查：纯键盘"填表 → 提交 → 关弹窗"，含 ② 提交结果与 ③ `focusOrder()` 自检投影；顺带覆盖 label / form / datepicker / colorpicker / upload（见 [3.1](#31-焦点与键盘遍历无障碍)） |
| [hover_demo.js](../testdata/hover_demo.js) | 悬停与按压反馈 |
| [dialog_demo.js](../testdata/dialog_demo.js) | 模态对话框与右上角 toast |
| [tabs_demo.js](../testdata/tabs_demo.js) | 选项卡：受控切页 / keep-alive 页 / 非受控 |
| [feedback_demo.js](../testdata/feedback_demo.js) | 反馈与数据类组件一屏：alert / tag / badge / avatar / icon / spinner / skeleton / pagination / empty / drawer |
| [condrender_demo.js](../testdata/condrender_demo.js) | 条件渲染切面板（教学版，完整重建语义） |
| [list_demo.js](../testdata/list_demo.js) | 数组信号增删列表 |
| [model_demo.js](../testdata/model_demo.js) | `model` 双向绑定：每类受控控件一条指令 + 手写写法对照 |
| [shots/](../testdata/shots/) | 15 个组件一屏一例（官网画廊截图素材；生成方式见 [dev-workflow](dev-workflow.md#组件画廊截图流水线)） |

**窗口、菜单与系统能力**

| 示例 | 内容 |
|---|---|
| [multiwindow_demo.js](../testdata/multiwindow_demo.js) | 多窗口：两窗口独立计数、关一个另一个继续跑、全关退出 |
| [multiscreen_demo.js](../testdata/multiscreen_demo.js) | 多屏协同（M8）：窗口列表 / 跨屏事件 / `router.handoff` 接续搬迁与复制（见 [multi-window.md](multi-window.md)） |
| [breakpoint_demo.js](../testdata/breakpoint_demo.js) | 自适应断点（M4）：同一份代码在 sm/md/lg/xl 四档自动换形态（见 §8.4） |
| [resize_demo.js](../testdata/resize_demo.js) | 窗口自适应：onResize 断点切栏 + 句柄 resize/setTitle |
| [menu_demo.js](../testdata/menu_demo.js) | 菜单栏：下拉 / 子菜单 / 禁用项 / 快捷键 / 右键菜单 |
| [clipboard_demo.js](../testdata/clipboard_demo.js) | 剪贴板：同步读写与失败降级 |
| [dialog_native_demo.js](../testdata/dialog_native_demo.js) | 原生对话框：alert / confirm / 打开/保存文件，全 async await |
| [search_demo.js](../testdata/search_demo.js) | 搜索框：input 字段变体（放大镜 + Enter 提交 onSearch） |
| [rating_demo.js](../testdata/rating_demo.js) | 星级评分：完全受控（value / max / onChange） |
| [storage_demo.js](../testdata/storage_demo.js) | gx/storage 持久化读写 |
| [dev_panel_demo.js](../testdata/dev_panel_demo.js) | gx/dev 调试面板：帧 / 缓存 / 树 / 警告 |
| [native_demo.js](../testdata/native_demo.js) | 原生能力层（§9.6）：设备信息 / 电量 / 网络 / 定位 / 能力检测，拉取型·动作型·上报响应型三种形态 |

**响应式与视图**

| 示例 | 内容 |
|---|---|
| [counter_demo.js](../testdata/counter_demo.js) | 响应式基础 |
| [gui_demo.js](../testdata/gui_demo.js) | 响应式基础（综合） |
| [jsx_demo.js](../testdata/jsx_demo.js) | JSX 语法降级 + `gx/solid` 响应式（用 debug `h` 构建纯数据节点树，不开窗口） |
| [rx_demo.js](../testdata/rx_demo.js) | GetX 风格响应式：`obs` / `computed` / `ever` / `once` |
| [view_demo.js](../testdata/view_demo.js) | 声明式视图：`each` / `show` 指令 + `Switch` / `Match` |
| [view_demo2.js](../testdata/view_demo2.js) | 同上，改写版：把"复用还是重建"做成可读的 gen / builds 计数（`gfx/view_demo2_test.go`） |
| [model_demo.js](../testdata/model_demo.js) | 受控组件的 `model` 双向绑定：八类控件一条指令 + 手写写法对照 |
| [resource_demo.js](../testdata/resource_demo.js) | createResource 异步资源三态 |
| [kit_demo.js](../testdata/kit_demo.js) | 设计套件：令牌主题与变体工厂 |

**路由与屏幕**

| 示例 | 内容 |
|---|---|
| [router_demo.js](../testdata/router_demo.js) | `gx/router` 核心：路由表 / `:param` 匹配 / 三级守卫 / 懒加载 / `keepAlive` / 状态袋 / `*` 兜底 |
| [router_window_demo.js](../testdata/router_window_demo.js) | 多窗口独立导航栈 + 镜像同步 + 多屏姿态 + 折叠双栏 |
| [router_page_detail.js](../testdata/router_page_detail.js) | 懒加载页面模块（被 `router_demo.js` 的 `lazy(() => import(…))` 加载，不是独立入口） |
| [routing_demo.js](../testdata/routing_demo.js) | **无模块时代**的用户态写法（signal 切页 + 未保存拦截）；3 页以内的小工具仍推荐，更多页面用上面的 `gx/router` |

**导航壳（PC / 移动自适应）**

| 示例 | 内容 |
|---|---|
| [tabbar_demo.js](../testdata/tabbar_demo.js) | `AppShell` + `TabBar`/`SideNav`：宽窄断点自动切形态、badge、快捷键、折叠（组件库见 [gui-tabbar.md](gui-tabbar.md)） |
| [tabbar_logic_test.js](../testdata/tabbar_logic_test.js) | 导航组件库纯逻辑验证（badge 格式化 / DPI 换算 / 断点分派 / createTabs），不开窗口 |
| [tabbar_smoke_test.js](../testdata/tabbar_smoke_test.js) | headless 挂载冒烟：桌面形态挂载 → 跨断点 resize 切 TabBar → 切回 → 退出 |

## 12. 症状速查（窗口起不来 / 值不对时先看这里）

这些坑的共同点是**不报错或报错离原因很远**（静默 `undefined`、第一帧对之后不对），
所以先按现象查，再翻对应的正文小节。

| 症状 | 根因 | 正解 |
|---|---|---|
| 窗口起不来，控制台 `h is not defined` | JSX 在 parser 层被降级成 `h(...)` 调用，而脚本没绑定 `h` —— 只 `import { render }` 能编译通过，挂载那一刻才炸 | **已自动补齐**（2026-09-22 起）：文件里没有 `h` 时编译器补一条 `import { h } from "gx/gfx"`。显式 `import { h, render }` 仍推荐、也仍优先；自己定义/导入的 `h` 一个字节都不动 |
| `alert is not a function`，或某个导入名拿到的恒是 `undefined` | 名字取错了模块（`alert` 在 `gx/dialog`，不在 `gx/gfx`）；从**文件模块**import 不存在的名字仍会**静默拿到 `undefined`** | 改从 `gx/dialog` 取，或用 `gox` 聚合入口。**从内置模块 import 不存在的名字现在是编译期报错**，并会直接指出它在哪个模块 / 是不是拼错 |
| `usePosture()` 拿到的不是字符串 | 所有 `useXxx()` 返回的都是**取值函数**（信号语义：放进函数 prop / 函数子节点才能跟着变），不是当前值 | `const r = usePosture(); r()`。只要"此刻的值"就直接用 `posture(win)`（返回字符串） |
| `each` / `show` 从 `gx/view` 里 `import` 不到 | 它们是**元素级指令**：写在 JSX 属性上、在 `h()` 里展开，**不在任何模块的导出表里** | 不 import：`<view each={rows} key="id">{…}</view>`。`gx/view` 只导出 `Switch` / `Match`。误 import 现在编译期报错（见 §8.2） |
| `const [data, {refetch}] = createResource(f)` 不起作用 | 嵌套解构（数组里套对象）当年解析失败，直接是 parser 报错 | **已修**（2026-09-22）：嵌套解构照常能用。等价写法 `const [data, res] = createResource(f)` + `res.refetch()`（见 §8.3） |
| 折叠屏折起来但没变双栏 | 姿态**没人上报** ⇒ 恒为平展。Windows / X11 没有姿态查询 API，框架的立场是"提供通道不猜姿态" | 宿主侧调 `reportPosture({posture:"half-open"})`；排查第一步用 `posture(win)` 确认读到的确实是 `"half-open"`（见 [gui-router.md](gui-router.md) §9.2 / §9.4） |
| `hinge()` 的宽度读出来是 0，回填后折痕像丢了 | `hinge()` / `regions()` 的**输出**用 `width`/`height`，而 `reportPosture` 的**入参**只读 `w`/`h` ⇒ 回填时静默读成 0（折痕宽度只影响双栏比例，所以什么错都不报） | 两种拼法现在都认（2026-09-22 起，短名优先）：`reportPosture({ hinge: hinge() })` 可以直接回填 |
| 响应式 prop / 条件 / 列表只有第一帧是对的 | prop 收到的是**取值函数**，写成快照（`disabled={count() === 0}`、`each={rows()}`）之后就再也不同步 | 一律传函数：`disabled={() => count() === 0}` / `each={rows}` / `show={cond}`。内核会对非法形态打去重警告（见 §8.1） |
| **文本停在第一帧，信号变了它不动** | **子节点**同样在调用当场求值：``<text>count: {count()}</text>`` 里的 `count()` 在 `h()` 之前就求值完，`h()` 建出来的是一个静态文本节点 | 写成函数子节点：``<text>{() => `count: ${count()}`}</text>``。**这条没有警告、也不可能有** —— 文本子节点天生收标量，运行期分不出"静态文本"和"快照"（§8.1），只能靠纪律 |
| **折叠 / 转屏后界面不更新，也不报错不警告** | 订阅型读数写在**三元 / 短路**里被"吃掉"：effect 只追踪实际读到的信号，首帧没走到的分支里那次调用没发生 ⇒ 依赖集为空、永不重跑（§8.1） | **先无条件取一次订阅型读数再分支**：`const r = useReservedRegions()(); if (!hasFold()) return "未检测到折痕"; …`。哪些读数带订阅见 §9 的"订阅型 vs 纯读数"分族 |
| **`<video>` 只有封面/播放三角，没有画面** | 本后端**没有平台视频层**（`nativeVideoHost`）—— 桌面三后端目前都没接。这不是 bug：内核不解码、也不假装在播 | 先问再选路：`canIUse("video")` 为 `false` 就跳系统播放器（`gx/media` 的 `preview()`）。现象上 stderr 会有一条告警，且该节点收到一次 `onError({code:"unsupported"})`（决策与后端接入见 [video-decision.md](video-decision.md)） |

> 本表里"JSX 缺省工厂"、"缺名导入"、"嵌套解构"、"折痕键名"四件事都在 2026-09-22
> 修在框架里了；文档里其它地方若还写着"必须自己 import h""嵌套解构不支持"，以本节为准。
> 表里**没有、也不会有**警告的是"文本子节点写成快照"与"订阅写在条件分支里被吃掉"两条 —— 前者天生无法判定，后者在首帧之前分不出哪条分支会被走（§8.1），都不是漏了没做。

## 13. 相关文档

| 文档 | 内容 |
|---|---|
| [README.md](../README.md) | 项目总览、安装、语言示例、打包与发版 |
| [tutorial.md](tutorial.md) | 实战教程：API 调用方式与参数、内置模块导入、`gox create` 建工程、路由定义与注册 |
| [gui-router.md](gui-router.md) | `gx/router` + `gx/screen` 使用手册（路由表 / 三级守卫 / 懒加载 / 两档状态保留 / 多窗口作用域 / 折叠双栏 / 排障表） |
| [multi-window.md](multi-window.md) | 多窗口 / 多屏 / 自适应断点 / 应用接续：坐标口径、JS API、平台支持矩阵、`router.handoff` 语义与 v1 边界（M4/M8） |
| [gui-tabbar.md](gui-tabbar.md) | 导航壳：TabBar（移动）/ SideNav（桌面）/ AppShell 分派器，安全区、软键盘、返回键、断点等平台差异的收口 |
| [gui-patterns.md](gui-patterns.md) | 用户态模式手册（路由、状态、主题等惯用法） |
| [gui-model-binding.md](gui-model-binding.md) | `model` 双向绑定：接口设计、语义表、与 Vue 的对照、反例 |
| [accessibility.md](accessibility.md) | 无障碍与键盘导航规范：焦点模型 / 键位表 / aria 语义 / `gx/a11y` 模块 / 验收清单（T10） |
| [desktop-distribution.md](desktop-distribution.md) | 各平台分发注意事项（图标、签名、打包格式） |

> 组件现状台账、需求提示词、样式/选型调研、未决清单等**过程性文档不随仓库发布**：
> 它们统一放在项目共享资产盘的 `agent_doc/` 目录（仅协作者可见），仓库里不留副本
> （2026-09-27 起本地 `agent_doc/` 已删除，`.gitignore` 也已排除该路径）。
