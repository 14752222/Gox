# Gox

> 用 Go 从零实现的 JavaScript 运行时：词法分析 → 语法分析 → 字节码编译 → 栈式虚拟机执行。
> 单二进制、零外部依赖，还能把 JS 脚本打包成独立可执行文件。

📖 **[官网与使用教程](https://14752222.github.io/Gox/)** — 在线学习如何安装、运行脚本、写 GUI 应用与打包分发（源码在 [`website/`](website/)，纯静态零构建，经 GitHub Actions 发布）。

## 特性一览

- **完整编译管线** — 自研 lexer / parser / compiler / bytecode VM，108 个操作码，定长 3 字节指令编码（`[操作码 1B][操作数 2B 大端]`），解码即取即用
- **ES6+ 语言子集** — `let`/`const`（不支持 `var`）、函数与箭头函数、闭包、`class`、`async`/`await`、解构赋值、剩余/默认参数、展开、模板字符串、`for...of`、`try`/`catch`/`throw`、可选链 `?.`、空值合并 `??`、ES 模块 `import`/`export`，以及 **JSX 语法**（编译期降级为 `h(tag, props, ...children)` 调用）
- **丰富的内置对象** — `Array` / `String` / `Number` / `Object` / `Boolean` / `Math` / `JSON` / `Map` / `Set` / `WeakMap` / `WeakSet` / `Symbol` / `BigInt` / `RegExp` / `Proxy` / `Reflect` / `Iterator` / `Promise` / `ArrayBuffer` / `DataView`（TypedArray 家族）/ `WeakRef` / `FinalizationRegistry` / 完整错误类型族 / `Temporal`（取代 `Date` 的现代日期时间 API）
- **宿主能力模块** — `fs`（Node 风格，同步 + 异步两套）、`http`（客户端 `get`/`request` + 服务端 `createServer`）、`fetch`、`path`、`process`、`stats`
- **自研 GUI 渲染层** — `gx/gfx` 模块：纯 Go 软件光栅化，flex 风格布局（`column`/`row`/`gap`/`padding`）、命中测试、脏矩形局部重绘；win32（纯 syscall 无 cgo）与 X11 窗口后端，产物为无动态库依赖的静态单文件
- **事件循环** — `setTimeout` / `setInterval` / `requestIdleCallback`，以及精度可控的严格定时器变体（`setStrictTimeout` 等）；GUI 模式下事件循环接入窗口消息泵
- **响应式编程** — Dart GetX 风格的 `obs` / `computed` / `ever` / `once`，以及 SolidJS 风格的 `gx/solid` 信号（`createSignal` / `createEffect` / `createMemo`）
- **npm 分发** — [`@goxjs/goxjs`](npm/) 包内置 Windows/Linux/macOS × x64/arm64 五个平台的预编译二进制，`npm i -g @goxjs/goxjs` 即得 `goxjs` 命令
- **工具链** — 交互式 REPL、jsbuild 打包器（JS → 独立 .exe，支持 GUI 应用与纯 Go 交叉编译）、dbgtool 词法调试器

## 快速开始

方式一：从源码构建（环境要求 Go 1.26+）

```bash
git clone https://github.com/14752222/Gox.git
cd Gox
go build          # Windows 下生成 Gox.exe
```

方式二：npm 安装预编译二进制（无需 Go 环境）

```bash
npm i -g @goxjs/goxjs    # 或不安装直接跑: npx goxjs app.js
```

**REPL：**

```bash
./Gox
```

```
Gox REPL (ES6 subset, no var)
Type :exit to quit, :help for help

> let x = 10
> let y = 20
> x + y
  30
> [1, 2, 3].map(v => v * 2)
  [2, 4, 6]
```

REPL 命令：`:help` 查看帮助、`:clear` 重置环境、`:exit` 退出。

**运行脚本：**

```bash
./Gox example.js    # npm 安装的 goxjs 命令用法相同
```

脚本执行完毕后会回显最后一个顶层表达式的值（`undefined` 除外），并等待定时器与异步回调全部执行完再退出。

## 语言示例

以下示例均实际运行验证过。

**基础语法：**

```js
let x = 10
let y = 20
console.log(x + y)                                                 // 30
console.log([1, 2, 3].map(v => v * 2))                             // [2, 4, 6]
console.log([1, 2, 3].filter(v => v > 1).reduce((a, b) => a + b))  // 5
console.log(Math.hypot(3, 4))                                      // 5

let name = "Gox"
console.log(`Hello, ${name}!`)                                     // Hello, Gox!

let [a, b] = [1, 2]                                                // 解构赋值
console.log(a + b)                                                 // 3
```

**class 与异步：**

```js
class Point {
  constructor(x, y) { this.x = x; this.y = y }
  norm() { return Math.hypot(this.x, this.y) }
}
console.log(new Point(3, 4).norm())       // 5

async function main() {
  let v = await delay(10, "timer done")   // delay(ms, value) 返回 Promise
  console.log(v)
}
main()
```

```
5
Promise { <pending> }     ← 顶层回显：main() 调用的返回值
timer done                ← 定时器到期后，await 继续执行
```

**ES 模块** — `lib.js`：

```js
export const PI = 3.14
export function double(x) { return x * 2 }
```

`main.js`：

```js
import { PI, double } from "./lib.js"
setTimeout(() => console.log("tick"), 10)
console.log(double(PI))
```

```
6.28
tick
```

**响应式（GetX 风格）：**

```js
let count = obs(0)
ever(count, v => console.log("count =", v))   // 订阅时立即以当前值回调一次
count.value = 1
count.value = 2
count.value = 2   // 值未变化，不触发通知
```

```
count = 0
count = 1
count = 2
2                  ← 顶层回显：最后一条赋值表达式的值
```

## GUI 桌面应用

`gx/gfx` + `gx/solid` 提供 JSX 声明式 UI 与信号驱动的响应式更新，渲染器为纯 Go 软件光栅化（无 cgo、无动态库依赖）：

```js
import { createSignal } from "gx/solid"
import { h, window, render } from "gx/gfx"

const [count, setCount] = createSignal(0)

render(
  <column gap={8} padding={16}>
    <text font={20}>{() => `count: ${count()}`}</text>
    <button onClick={() => setCount(c => c + 1)}>加一</button>
  </column>,
  window({ title: "Counter", width: 400, height: 300 })
)
```

```bash
./Gox counter.js          # 直接运行，弹出 400x300 窗口
```

- 点击按钮 → `setCount` 更新信号 → 依赖该信号的属性/文本节点自动标脏 → 脏矩形合并后只重绘受影响区域
- 未实现的标签（拼错的名字、或还没做进 `knownTags` 的名字）会在 stderr 打印一次性警告，并仍按普通盒子渲染（不再静默成空盒子）
- 窗口后端：Windows（纯 syscall win32）与 Linux（X11，Wayland 下走 XWayland）；macOS GUI 后端尚未实现
- 字体：Windows/macOS 走静态候选路径；Linux 惰性扫描系统字体目录（`/usr/share/fonts`、`~/.local/share/fonts` 等，**CJK 字体优先**、条目上限 2000）。找不到可用字体时文字整体不渲染，错误里会给出候选条数与最后一个失败原因

事件：

| 事件 | 参数 | 分发规则 |
|---|---|---|
| `onClick` | 无 | 命中测试（最内层带 `onClick` 的节点），并把该节点设为键盘焦点 |
| `onMouseMove` | `{x, y}` | 光标下最深节点起沿祖先链找第一个处理器（不冒泡到根以外） |
| `onWheel` | `{deltaY}` | 光标所在 `scroll` 容器先消费（一格 60px），容器已到边界才继续冒泡；`deltaY` 沿用 DOM 约定（向下滚为正） |
| `onContextMenu` | `{x, y}` | 右键抬起时触发 |
| `onKeyDown` / `onKeyUp` | `{key, ctrl, shift, alt}` | 从焦点节点沿祖先链找第一个处理器 |
| `onFocus` / `onBlur` | 无 | 焦点切换时触发，沿祖先链找第一个处理器；焦点节点会画 1px 蓝色虚线框（根节点 `hideFocusRing` 可关闭） |

> 交互组件（`button` / `checkbox` / `radio` / `switch`）自动获得悬停提亮（各通道 +12）与按压压暗（-24）反馈，
> 状态由渲染层维护，脚本无需（也无法）读写。`disabled` 的子树既不响应事件也不做交互反馈。
>
> 光标离开窗口 / 窗口失活会清除悬停与按压态。Tab 键焦点遍历尚未实现（需要 focusable 注册表）。

内置元素：

| 元素 | 主要属性 | 说明 |
|---|---|---|
| `column` / `row` | `gap` / `padding` / `margin`(子级) / `alignItems` / `justifyContent` / `flexGrow`(子级) / `width` / `height` | flex 风格容器，尺寸按内容确定（交叉轴默认 stretch） |
| `text` | `font` / `color` / `width` / `wrap` / `ellipsis` | 默认单行文本、超宽硬截断；加 `wrap` 变成文本块（按宽度贪心折行、`\n` 强制换行），`ellipsis={n}` 只留 n 行并在末行补 `...` |
| `rect` | `width` / `height` / `background` / `border` | 通用盒子；未特判的标签也走这条绘制路径 |
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

**层叠与定位**

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

受控文本输入（与 `checkbox` / `select` 同一套受控语义：显示只看 `value`，编辑只派发 `onInput`）：

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
光标闪烁需要事件泵持续醒来，挂一个 `requestAnimationFrame` 循环即可（见 `testdata/input_demo.js`）。

**输入法（IME）**：`<input>` / `<textarea>` 都支持候选词输入（Windows 后端）。切到中文输入法后
敲拼音，正在拼的字由系统组合窗显示，选定候选词后**整批**插到光标处：一次提交只派发一次
`onInput`，光标一次跨过整批（不会把下一个词插到前一个词中间）。焦点不在编辑框上时输入法
自动关闭，在按钮/画布上敲字不会弹候选窗。`textarea` 里同样可用，且"提交内容自带换行"会正确
把光标落到新行。已知取舍：Linux（X11）后端暂无 IME；组合过程不在框内内联绘制。
示例见 `testdata/ime_demo.js`。

**多行文本与自动换行**

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

`<textarea>` 是多行编辑框，受控语义与 `input` 一致（显示只看 `value`，编辑只派发 `onInput`）：

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

**滚动容器**

`<scroll>` 让任意高度的内容待在固定高度的视口里，超出部分被裁掉，右侧自动出现滚动条：

```js
h("scroll", { width: 240, height: 120, onWheel: () => setOverscroll(n => n + 1) },
  rows.map((r) => h("rect", { height: 36, background: "#fff" },
    h("text", { font: 13 }, r))),
)
```

- 滚轮在容器内先被容器消费（一格 60px），**到边界才继续往外冒泡**给 `onWheel` —— 所以"到顶/到底再翻页"可以纯 JS 写；
- 溢出的内容**既画不出来也点不中**（绘制裁剪与命中裁剪用同一个视口），不会出现幽灵点击；
- 子节点的 `Box` 已经包含滚动偏移（就是屏幕坐标），不用自己再算；
- 内容不足一屏时不出滚动条，也不会给内容让出滚动条那 8px；
- 静态数组子节点会自动展开成兄弟节点，所以 `<scroll>{rows}</scroll>` 直接可用。
- 横向滚动与滚动条拖拽尚未实现，滚动条本身不可拖（只能滚轮或脚本改偏移）。

> 颜色属性（`background` / `border` / `color`）在组件标签上有语义差异：`background` 表示"选中/填充的强调色"，
> 在 `button` / `rect` 上才是普通填充色；`color` 沿祖先链继承，因此 `<button color="#fff">文字</button>` 生效。

**自绘画布**

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

**滑块**

`<slider>` 是受控滑块：`value` 决定位置（含 `min`/`max`/`step`），拖动或**单击轨道任意位置**都会派发 `onInput({value})`：

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

**响应式子节点（条件渲染 / 列表渲染）**

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

> 列表暂无 key/diff：内容变化按"清空重建"处理，小列表够用。

示例：`testdata/form_demo.js`（表单控件）、`testdata/progress_demo.js`（进度/分隔/占位）、
`testdata/button_demo.js`（按钮三态）、`testdata/events_demo.js`（鼠标/滚轮/右键/修饰键）、
`testdata/focus_demo.js`（焦点框与 focus/blur）、`testdata/hover_demo.js`（悬停与按压反馈）、
`testdata/tabs_demo.js`（条件渲染切面板）、`testdata/list_demo.js`（数组信号增删列表）、
`testdata/select_demo.js`（受控下拉框）、`testdata/dialog_demo.js`（模态对话框与右上角 toast）、
`testdata/input_demo.js`（单行输入与实时镜像）、`testdata/scroll_demo.js`（滚动容器与边界冒泡）、
`testdata/multiline_demo.js`（自动换行 / 省略号 / 硬截断三态对照）、`testdata/textarea_demo.js`（多行编辑器）、
`testdata/image_demo.js`（图片五态：自然尺寸 / 放大 / 缩小 / 坏路径占位 / 禁用）、
`testdata/canvas_demo.js`（自绘画布：signal 驱动柱状图 + ctx 原语展示）、
`testdata/slider_demo.js`（滑块：受控值 / 量程 / 禁用三态）、
`testdata/ime_demo.js`（输入法：候选词整批提交与光标跨批）、
`testdata/counter_demo.js` 与 `testdata/gui_demo.js`（响应式基础）。

## 打包成独立可执行文件

`jsbuild`（packager 目录）把入口脚本及其相对 import 的模块嵌入一个生成的 Go 工程，编译成单文件可执行程序，自带完整运行时：

```bash
go run ./packager app.js -o app.exe                    # CLI 应用
go run ./packager counter.js --gui -o counter.exe      # GUI 应用
go run ./packager app.js --gui --target linux/amd64    # 纯 Go 交叉编译，无需目标机工具链
```

```
Usage: jsbuild <input.js> [options]

Options:
  -o, --out <path>     输出文件路径 (默认: <输入文件名>.exe)
      --name <name>    应用名 (用于错误信息显示, 默认取输入文件名)
      --windowed       窗口模式: 不显示控制台窗口 (仅 Windows)
      --gui            GUI 应用: 窗口消息泵事件循环 (配合 gx/gfx render)
      --target <os>/<arch>  交叉编译目标 (windows|linux|darwin / amd64|arm64|386)
  -v, --verbose        显示构建过程输出
```

各平台的分发注意事项（Windows 图标与签名、Linux 打包格式、macOS .app bundle）见 [docs/desktop-distribution.md](docs/desktop-distribution.md)。

## 架构

```
 .js 源码
    │  lexer        词法分析
    ▼
 Token 流
    │  parser       语法分析
    ▼
 AST (ast/)
    │  compiler     符号表解析 + 字节码生成 (常量池)
    ▼
 字节码 (bytecode/)   定长 3 字节: [opcode 1B][operand 2B 大端]
    │  vm           栈式调用帧执行 + 定时器事件循环 + 模块加载
    ▼
 结果
```

- **无 GC 的显式内存管理** — 对象生命周期由运行时显式控制，循环引用有专门处理
- **单线程 VM + 跨 goroutine 调度** — `http.createServer` 等网络回调跨 goroutine 捕获后调度回 VM 单线程执行，无需锁

### 目录结构

| 目录 | 职责 |
|---|---|
| `lexer/` | 词法分析器（含 JSX 词法支持） |
| `parser/` | 语法分析器，生成 AST（含 JSX 语法降级） |
| `ast/` | AST 节点定义 |
| `compiler/` | AST → 字节码编译器（含符号表） |
| `bytecode/` | 操作码与字节码格式定义 |
| `vm/` | 栈式字节码虚拟机（调用帧、模块加载、定时器调度） |
| `object/` | 运行时对象系统（Number/Array/Map/Promise/Observable...） |
| `runtime/` | 全局环境 Environment |
| `stdlib/` | 标准库与宿主 API 实现（含 `gx/solid` 响应式信号） |
| `gfx/` | 自研 GUI 渲染层（软件光栅化、布局、命中测试、win32/X11 后端） |
| `packager/` | jsbuild 打包器（GUI 应用、交叉编译） |
| `dbgtool/` | 词法分析调试工具（打印 Token 流） |
| `npm/` | @goxjs/goxjs npm 包（跨平台二进制分发） |
| `scripts/` | 构建脚本（`build-npm.sh`：交叉编译 npm 包二进制） |
| `.github/workflows/` | CI（打 `v*` tag 自动构建并发布 npm 包；`website.yml` 发布官网到 GitHub Pages） |
| `website/` | 官网（纯静态 HTML/CSS/JS，零构建，部署于 GitHub Pages） |
| `docs/` | 文档 |
| `test/` | 测试相关：`bench/` 性能剖析基准（fib、函数调用、对象操作、数值解析），`testdata/` 示例与测试脚本 |

## 开发

```bash
go test ./...     # 运行全部测试 (bytecode/compiler/lexer/object/parser/runtime/vm)
go run ./test/bench   # 生成 cpu.prof 性能剖析
go run ./dbgtool     # 查看词法分析的 Token 流
```

深入参与开发（新增标准库 API、理解回调桥与内存管理）请阅读
[docs/js-runtime-api-tutorial.md](docs/js-runtime-api-tutorial.md)。

发版流程：修改 `npm/package.json` 的 `version` → 提交 → 打 tag（如 `v0.1.0`）→ push，CI 自动交叉编译全平台二进制并 `npm publish`（需在仓库 Secrets 配置 `NPM_TOKEN`）。

## 许可证

[Apache License 2.0](LICENSE)
