# Gox GUI 开发指南

用 JSX + 信号（signal）写桌面界面。本文是 `gx/gfx` 渲染层及其配套宿主模块的完整参考、
API 语义与已知取舍；上手最短路径见 [README 的 GUI 章节](../README.md#gui-桌面应用)。

- **适用前提**：Go 1.26+ 构建的 Gox（或 npm 安装的 `goxjs`）。
- **后端支持**：Windows（win32，纯 syscall、无 cgo）与 Linux（X11，Wayland 下走 XWayland）；macOS 后端尚未实现。
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
- [12. 相关文档](#12-相关文档)

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
./Gox counter.js          # 直接运行，弹出 400x300 窗口
```

- 点击按钮 → `setCount` 更新信号 → 依赖该信号的属性 / 文本节点自动标脏 → 脏矩形合并后只重绘受影响区域。
- 窗口配置写在根元素上：JSX 里 `<window title width height>` 包住整棵树；`h()` 手拼树时
  `render(tree, {title, width, height})` 传普通对象，省略则用缺省（Gox 400x300）。
- `render()` 返回窗口句柄 `{close(), isClosed(), title(), setTitle(t), resize(w, h)}`，
  可**调用多次**开多窗口（详见 [9.5 多窗口](#95-多窗口)）。
- 未实现的标签（拼错的名字、或还没做进 `knownTags` 的名字）会在 stderr 打印一次性警告，
  并仍按普通盒子渲染（不再静默成空盒子）。

## 2. 运行时行为与平台支持

| 能力 | Windows（win32） | Linux（X11） | 说明 |
|---|---|---|---|
| 窗口 | 支持 | 支持（Wayland 走 XWayland） | 多窗口见 [9.5](#95-多窗口) |
| 字体 | 静态候选路径 | 惰性扫描系统字体目录 | Linux 扫 `/usr/share/fonts`、`~/.local/share/fonts` 等，**CJK 字体优先**，条目上限 2000 |
| 输入法（IME） | 支持 | 暂不支持 | 见 [6.2](#62-输入法-ime) |
| 剪贴板 | 支持 | 暂不支持 | 见 [9.2](#92-剪贴板) |
| 原生对话框 | 支持 | 降级为写 stderr | 见 [9.1](#91-原生系统对话框) |
| 菜单栏 / 右键菜单 | 支持 | 支持 | gfx **自绘**，不依赖系统菜单 API，三平台观感一致 |

找不到可用字体时文字整体不渲染，错误里会给出候选条数与最后一个失败原因。

## 3. 事件模型

| 事件 | 参数 | 分发规则 |
|---|---|---|
| `onClick` | 无 | 命中测试（最内层带 `onClick` 的节点），并把该节点设为键盘焦点 |
| `onMouseMove` | `{x, y}` | 光标下最深节点起沿祖先链找第一个处理器（不冒泡到根以外） |
| `onWheel` | `{deltaY}` | 光标所在 `scroll` 容器先消费（一格 60px），容器已到边界才继续冒泡；`deltaY` 沿用 DOM 约定（向下滚为正） |
| `onContextMenu` | `{x, y}` | 右键抬起时触发；常配合 `openContextMenu(e.x, e.y, items)` 弹右键菜单 |
| `onKeyDown` / `onKeyUp` | `{key, ctrl, shift, alt}` | 从焦点节点沿祖先链找第一个处理器 |
| `onFocus` / `onBlur` | 无 | 焦点切换时触发，沿祖先链找第一个处理器；焦点节点会画 1px 蓝色虚线框（根节点 `hideFocusRing` 可关闭） |
| `onResize` | `{width, height}` | **窗口级**事件：窗口尺寸变化时派发给**布局根**（挂非根节点不触发），不走焦点链；尺寸为物理像素。拖窗口边缘或脚本调 `win.resize()` 都会触发 |
| `shortcut`（属性，非事件） | 回调收 `{x, y, shortcut}` | `<menuitem shortcut="Ctrl+S">`：不必展开菜单，快捷键表在事件泵层直接匹配。只认带 `Ctrl`/`Alt` 的组合，且修饰键**全等**（`Ctrl+S` 不会被 `Ctrl+Shift+S` 触发） |

> 交互组件（`button` / `checkbox` / `radio` / `switch`）自动获得悬停提亮（各通道 +12）与按压压暗（-24）反馈，
> 状态由渲染层维护，脚本无需（也无法）读写。`disabled` 的子树既不响应事件也不做交互反馈。
>
> 光标离开窗口 / 窗口失活会清除悬停与按压态。Tab 键焦点遍历尚未实现（需要 focusable 注册表）。

## 4. 内置元素参考

| 元素 | 主要属性 | 说明 |
|---|---|---|
| `column` / `row` | `gap` / `padding` / `margin`(子级) / `alignItems` / `justifyContent` / `flexGrow` / `flexShrink`(子级) / `wrap` / `width` / `height` | flex 风格容器，尺寸按内容确定（交叉轴默认 stretch）；`row` 加 `wrap` 放不下折行，`gap` 兼作行内间距与行间距 |
| `view` | `each` / `show` / `fallback` / `key` / `stable` / `gap` | **布局透明的容器**（Fragment）：单子时尺寸完全跟随子节点、多子（列表）按父容器方向堆叠，自己不占盒子；**元素级指令就写在这类元素上** —— `each={rows}` 按列表重复本元素（keyed 复用 / `stable` / `fallback`），`show={open}` keep-alive 显隐。指令对任何内置元素标签都有效，写在 `<column>` / `<row>` 上就是"每一项一个盒子" |
| `grid` | `columns`（1~32） | 等宽列网格：声明序逐行填格，列宽均分内容宽，格子无显式高时拉到行高；不做轨道语法 / colSpan（不等宽列用 `row` + 百分比组合） |
| `text` | `font` / `color` / `width` / `wrap` / `ellipsis` | 默认单行文本、超宽硬截断；加 `wrap` 变成文本块（按宽度贪心折行、`\n` 强制换行），`ellipsis={n}` 只留 n 行并在末行补 `...` |
| `rect` | `width` / `height` / `background` / `border` / `radius` / `shadow` / `borderWidth` / `borderStyle` | 通用盒子；未特判的标签也走这条绘制路径；装饰属性（圆角 / 阴影 / 渐变 / 边框宽度）见 [5.3](#53-装饰绘制) |
| `button` | `onClick` / `disabled` / `background` / `border` / `color` / `padding` | 缺省浅灰底 + 深灰边框，文字子节点垂直居中；`disabled` 时整体变灰且不响应点击 |
| `checkbox` / `radio` | `checked` / `onClick` / `border` / `background` / `color` | 18×18 受控控件；`background` 是选中填充色，radio 互斥在 JS 侧用 signal 实现 |
| `switch` | `checked` / `onClick` | 36×20 受控开关（方形轨道），`background` 覆盖打开态轨道色 |
| `progress` | `value`(0~1，越界自动钳位) / `background` / `width` / `height` | 缺省 200×8，轨道浅灰 + 前景主题绿 |
| `separator` | `vertical` / `background` | 横向 1px 高、宽度由容器拉伸；纵向宽度 1px，需显式 `height` |
| `spacer` | `flexGrow` | 不绘制任何内容，仅吃主轴富余空间，用法 `<spacer flexGrow={1}/>` |
| `select` | `value` / `options` / `onChange` / `placeholder` / `disabled` | 受控下拉框；`options` 可为字符串数组或 `{value,label}` 数组，选中派发 `onChange({value})`；键盘可开合/移动/选中/Esc 关闭 |
| `dialog` | `open` / `onClose` | 模态弹层：40% 黑遮罩 + 居中卡片（流内子节点即卡片内容）；点遮罩 / Esc / 卡片内按钮触发 `onClose`，遮罩吞掉其下点击 |
| `toast` | `message` / `level` | 非模态提示，固定右上角；`level` 取 `success` / `warn` / `error` / `info` 决定色条，显隐由 JS 侧信号控制 |
| `input` | `value` / `onInput` / `placeholder` / `disabled` | 单行受控输入（沿 `value` 显示，编辑派发 `onInput({value})`）；获焦边框转蓝并显示闪烁竖线光标，点击可定位光标；支持 ←/→/Home/End/Backspace/Delete，`Enter`/`Esc` 不消费；支持 IME 候选词整批提交（Windows） |
| `textarea` | `value` / `onInput` / `rows` / `placeholder` / `disabled` | 多行受控编辑器；光标 `{行,列}` 二维移动（↑↓←→/Home/End/Backspace/Delete），**`Enter` 插入换行**（不同于 input）；内容超高时纵向滚动并跟随光标；同样支持 IME。缺省 4 行 × 240px |
| `scroll` | `width` / `height` / `onWheel` | 纵向滚动容器：内容超高时右侧出现 8px 轨道 + 比例滑块，滚轮滚动（一格 60px），到边界后滚轮才冒泡给 `onWheel`；溢出的内容既画不出来也点不中。缺省高 200 |
| `image` | `src` / `width` / `height` / `disabled` | 显示 png / jpeg / gif 图片（Go 标准库解码，无新增依赖）；不给 `width`/`height` 时用图片自然尺寸，给了就按最近邻缩放；`src` 相对**进程工作目录**解析，加载失败画灰底交叉线占位（stderr 每个路径只警告一次），不中断其它内容 |
| `canvas` | `width` / `height` / `onDraw(ctx)` / `background` / `border` | 自绘画布：`onDraw` 收到一个 ctx，用 `ctx.fillRect/strokeRect/fillCircle/strokeCircle/line/drawText/clear` 直接落笔，坐标是**画布局部坐标**（0,0 = 左上角），越界部分自动裁掉；`ctx.width` / `ctx.height` 是画布尺寸。`onDraw` 里读到的 signal 变化会自动重绘（缺省 200×120） |
| `slider` | `value` / `onInput` / `min` / `max` / `step` / `disabled` | 受控滑块（`min`/`max`/`step` 缺省 0/100/1）：显示只看 `value`，拖动或**单击轨道任意位置**派发 `onInput({value})`（`value` 是 **number**）；拖出窗口仍跟手（win32 走 `SetCapture`）。缺省 160×24 |
| `menubar` | `background` / `border` | 菜单栏容器（缺省 26px 高、自动铺满容器宽）；**它只是个普通容器**，脚本自己写 `column { menubar; 内容 }`，gfx 不会往 root 里偷偷插一条 |
| `menu` | `label`（或 `title`、或文本子节点） | 菜单标题；作为 `<menubar>` 的直接子节点时是**顶级菜单**（下拉挂在标题正下方），嵌在 `<menuitem>` 里时是**子菜单**（挂在触发项右侧）。空菜单点了不展开 |
| `menuitem` | `label` / `shortcut` / `disabled` / `onClick` | 菜单项；点中派发 `onClick({x, y})` 并收起整棵菜单。`disabled` 灰字且点了没反应（**也不收起**）；内嵌一个 `<menu>` 即成为子菜单触发器（点击展开/收起右侧下拉，自身不派发 `onClick`）。分隔线用已有的 `<separator>`，它照样可命中（点了没反应） |

> 颜色属性（`background` / `border` / `color`）在组件标签上有语义差异：`background` 表示"选中/填充的强调色"，
> 在 `button` / `rect` 上才是普通填充色；`color` 沿祖先链继承，因此 `<button color="#fff">文字</button>` 生效。

> 受控组件（`input` / `textarea` / `slider` / `select` / `checkbox` / `switch` / `radio`）另有一条
> **`model` 指令**：`<input model={draft} />` 一次接好读（`value`）与写（`onInput`），不用再手写
> `value={() => draft()} onInput={(e) => setDraft(e.value)}`。语义表见 [6.0](#60-一条指令搞定读写model)，
> 完整设计见 [gui-model-binding.md](gui-model-binding.md)。

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
| `input` / `textarea` | `value` | `onInput({value})` | 字符串 |
| `slider` | `value` | `onInput({value})` | 数字（不转换） |
| `select` | `value` | `onChange({value})` | 字符串 |
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

### 6.2 输入法 IME

`<input>` / `<textarea>` 都支持候选词输入（Windows 后端）。切到中文输入法后敲拼音，
正在拼的字由系统组合窗显示，选定候选词后**整批**插到光标处：一次提交只派发一次 `onInput`，
光标一次跨过整批（不会把下一个词插到前一个词中间）。焦点不在编辑框上时输入法自动关闭，
在按钮/画布上敲字不会弹候选窗。`textarea` 里同样可用，且"提交内容自带换行"会正确把光标落到新行。

已知取舍：Linux（X11）后端暂无 IME；组合过程不在框内内联绘制。
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

- 行只由 `\n` 切分（**不做软换行**），所以光标 `{行, 列}` 与文本严格对应；超长行会被右侧裁掉；
- `Enter` **被编辑框消费**（插入换行）—— 与单行 `input` 相反，多行框里 Enter 就是内容；
  `Esc` / `Tab` / 功能键 / 带 `Ctrl`+`Alt` 的组合键仍然放行给脚本；
- 内容超过可视高度后自动纵向滚动，且**滚动跟随光标**（在底部回车时光标不会跑到框外）；
  也可以把光标放进框里滚滚轮。

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
- 横向滚动与滚动条拖拽尚未实现，滚动条本身不可拖（只能滚轮或脚本改偏移）。

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
刻意排除，理由见 [gui-component-status.md](gui-component-status.md) §20）。**首次赋值不做过渡**
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

## 9. 宿主能力

### 9.1 原生系统对话框

`gx/dialog` 把系统消息框与"打开文件"对话框直接接到脚本上：

```js
import { alert, confirm, openFile } from "gx/dialog"

await alert("All changes have been saved.", "Gox")          // 只有一个"确定"
const yes = await confirm("Delete this file?", "Please confirm")  // → true / false
const path = await openFile({                               // → 完整路径 / null（取消）
  title: "Pick a source file",
  filter: [
    { name: "Text files", pattern: "*.txt;*.md" },
    { name: "All files",  pattern: "*.*" },
  ],
})
```

三件事值得留意：

- **是 async 的**（与同步的剪贴板不同）。实现是"同步落地 + 异步外观"：Go 侧真的阻塞到用户作答，
  Promise 的 resolve 投回脚本事件循环 —— 所以 `await` 之后的代码在对话框关掉前不会执行。
- **模态期间界面不冻结**：消息框以主窗口为 owner，Windows 会自动替我们泵模态消息，
  重绘 / 拖动都正常，也**不需要**自己写 goroutine 或消息循环。
- **取消不是错误**：`openFile` 取消返回 `null`（与浏览器 File System Access API 一致），不必 try/catch。
  **没有原生能力的后端会降级**：内容写到 stderr 并立刻返回（`confirm` 取 true、`openFile` 取 null）。

> 语法提示：运行时只支持 `async function`，**不支持 `async () => {}`**。
> 事件处理器要写成 `onClick: async function () { ... }`。

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
getStorage("theme", "light")            // → dark（读不到时返回默认值）
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

## 10. 调试

`gx/dev` 提供运行时快照 `devSnapshot()`（帧统计 / 缓存命中 / 最近警告，可做调试面板）：

```js
import { devSnapshot } from "gx/dev";
```

示例：[testdata/dev_panel_demo.js](../testdata/dev_panel_demo.js)。

## 11. 示例索引

`testdata/` 下的 GUI 示例均可直接用 `./Gox <file>` 运行：

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
| [image_demo.js](../testdata/image_demo.js) | 图片五态：自然尺寸 / 放大 / 缩小 / 坏路径占位 / 禁用 |
| [events_demo.js](../testdata/events_demo.js) | 鼠标 / 滚轮 / 右键 / 修饰键 |
| [focus_demo.js](../testdata/focus_demo.js) | 焦点框与 focus/blur |
| [hover_demo.js](../testdata/hover_demo.js) | 悬停与按压反馈 |
| [dialog_demo.js](../testdata/dialog_demo.js) | 模态对话框与右上角 toast |
| [tabs_demo.js](../testdata/tabs_demo.js) | 条件渲染切面板 |
| [list_demo.js](../testdata/list_demo.js) | 数组信号增删列表 |
| [model_demo.js](../testdata/model_demo.js) | `model` 双向绑定：八类控件一条指令 + 手写写法对照 |

**窗口、菜单与系统能力**

| 示例 | 内容 |
|---|---|
| [multiwindow_demo.js](../testdata/multiwindow_demo.js) | 多窗口：两窗口独立计数、关一个另一个继续跑、全关退出 |
| [resize_demo.js](../testdata/resize_demo.js) | 窗口自适应：onResize 断点切栏 + 句柄 resize/setTitle |
| [menu_demo.js](../testdata/menu_demo.js) | 菜单栏：下拉 / 子菜单 / 禁用项 / 快捷键 / 右键菜单 |
| [clipboard_demo.js](../testdata/clipboard_demo.js) | 剪贴板：同步读写与失败降级 |
| [dialog_native_demo.js](../testdata/dialog_native_demo.js) | 原生对话框：alert / confirm / 打开文件，全 async await |
| [storage_demo.js](../testdata/storage_demo.js) | gx/storage 持久化读写 |
| [dev_panel_demo.js](../testdata/dev_panel_demo.js) | gx/dev 调试面板：帧 / 缓存 / 树 / 警告 |

**响应式与视图**

| 示例 | 内容 |
|---|---|
| [counter_demo.js](../testdata/counter_demo.js) | 响应式基础 |
| [gui_demo.js](../testdata/gui_demo.js) | 响应式基础（综合） |
| [jsx_demo.js](../testdata/jsx_demo.js) | JSX 语法降级 + `gx/solid` 响应式（用 debug `h` 构建纯数据节点树，不开窗口） |
| [rx_demo.js](../testdata/rx_demo.js) | GetX 风格响应式：`obs` / `computed` / `ever` / `once` |
| [view_demo.js](../testdata/view_demo.js) | 声明式视图：For / Show / Switch |
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

## 12. 相关文档

| 文档 | 内容 |
|---|---|
| [README.md](../README.md) | 项目总览、安装、语言示例、打包与发版 |
| [gui-router.md](gui-router.md) | `gx/router` + `gx/screen` 使用手册（路由表 / 三级守卫 / 懒加载 / 两档状态保留 / 多窗口作用域 / 折叠双栏 / 排障表） |
| [gui-component-status.md](gui-component-status.md) | 组件实现现状、逐批落地记录与设计取舍（§编号最权威） |
| [gui-patterns.md](gui-patterns.md) | 用户态模式手册（路由、状态、主题等惯用法） |
| [gui-model-binding.md](gui-model-binding.md) | `model` 双向绑定：接口设计、语义表、与 Vue 的对照、反例 |
| [gui-prompts.md](gui-prompts.md) | GUI 需求/提示词记录 |
| [gui-styling-options.md](gui-styling-options.md) | 样式方案调研（含 §20 可动画属性的取舍理由） |
| [desktop-distribution.md](desktop-distribution.md) | 各平台分发注意事项（图标、签名、打包格式） |
| [undecided-and-unimplemented.md](undecided-and-unimplemented.md) | 未决与未实现清单 |
