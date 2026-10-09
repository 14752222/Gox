# notes —— Markdown 笔记 + 图表

一个**能拿去用**的小应用：左边笔记列表，右边预览 / 图表切换，下边改正文上面实时跟着变。
顺带把两个"够用就好"的用户态组件沉淀在这：`src/lib/chart.js`（图表）与
`src/lib/markdown.js`（Markdown → gfx 节点树）。

两者都**没有进内核**（不进 `stdlib/`、不改 `gfx/`），按 `apps/README.md` 的约定
留在应用内 `src/lib/` —— 等第二个应用要复用同一份代码时再抽 `apps/_shared/`。

## 跑起来

```bash
# 用当前源码构建的二进制（别用仓库根那个常年落后的 gox.exe）
go build -o /tmp/gox-current ./cmd/gox

/tmp/gox-current apps/notes/src/main.js            # 桌面直接跑
xvfb-run -a /tmp/gox-current apps/notes/src/main.js # 无 X 的 Linux

# 自测 / 演示（纯逻辑段不需要 X；GUI 段需要）
/tmp/gox-current apps/notes/demo/probe.js
xvfb-run -a /tmp/gox-current apps/notes/demo/probe.js
```

打包：`go run ./packager apps/notes/src/main.js --gui -o notes.exe`

## 目录

```
src/
  main.js              入口：render(<window>…)
  app.js               根组件（标题行 + 预览/图表切换 + 列表 + 编辑 + 状态行）
  store.js             signal 状态：笔记列表 / 当前选中 / 当前页签 / 正文写入
  theme.js             设计令牌（自带一份，不共享）
  lib/                 纯逻辑，不 import gox
    chart.js           图表：折线 / 条形 / 饼图 / 环形
    markdown.js        Markdown 解析 → 渲染成节点描述树
    text-metrics.js    文本宽度估算（内核没有测量接口，见下）
  components/
    note-list.js       列表（each + key）
    markdown-view.js   描述树 → 真 gfx 节点（唯一调 h() 的地方）
    chart-view.js      <canvas onDraw> 里调 drawChart
demo/
  probe.js             自测脚本（97 项断言，见下）
```

## 组件 API

### chart.js —— `drawChart(ctx, cfg)`

一条入口，按 `cfg.type` 分派：`line`（缺省）/ `bar` / `pie` / `donut`。

```js
import { drawChart } from "./lib/chart.js";

<canvas width={420} height={240} onDraw={(ctx) => drawChart(ctx, cfg)} />

const cfg = {
  type: "line",              // line | bar | pie | donut
  title: "每日投入",
  labels: ["一", "二", "三"], // 类目轴（pie/donut 时即扇区名）
  series: [                   // pie/donut 只用第一个系列
    { name: "编码", data: [6, 7, 5], color: "#3355aa" },
    { name: "会议", data: [1, 2, 3] },
  ],
  yMin: 0, yMax: 100,         // 显式给 → 固定基线，不按数据扩界
  width: 0, height: 0,        // 0 = 按画布当前尺寸画
  decimals: 1,                // 刻度标签小数位
  showLegend: undefined,      // 缺省：单系列不显示，多系列 / 饼图显示
  showGrid: true, showValues: false,
  holeRatio: 0.55,            // 环形图内/外半径比
  tickCount: 5,               // 刻度段数
  background: "#ffffff", grid: "#eceff4", axis: "#c3cad6",
  text: "#5a6472", titleColor: "#1c2430",
  padding: { top: 12, right: 14, bottom: 8, left: 12 },
};
```

- `data` 里的 `null` / `undefined` 表示"这里没采到样" → **断线**（折线）或**跳过**（条形/饼图），
  不当 0 —— 当 0 会把"没采到"画成"采到 0"，读数完全不同。
- 饼图 / 环形图跳过值 `<= 0` 的项。

其它导出（拆分出来是为了能单独测，也是 `demo/probe.js` 断言的对象）：
`niceTicks` / `dataExtent` / `fmtNum` / `normalizeChart` / `chartScale` /
`chartFrame` / `legendItems` / `drawLineChart` / `drawBarChart` / `drawPieChart` /
`CHART_PALETTE`（8 色，相邻明度错开，灰度打印也能分）。

### markdown.js —— `renderMarkdown(src, style)` / `parseMarkdown(src)`

`lib/` 不许 import `gox`，所以这里产出的是**节点描述树**（`{ tag, props, children }`），
由 `components/markdown-view.js` 用真正的 `h()` 物化。好处是同一棵树能在探针脚本里
被直接断言结构。

```js
import { renderMarkdown, parseMarkdown, markdownText, nodeText } from "./lib/markdown.js";

const tree = renderMarkdown(text, { width: 520 });  // style 可省（有缺省）
const blocks = parseMarkdown(text);                 // 只要块结构
markdownText(text);                                 // 整篇纯文本（搜索/摘要用）
nodeText(tree);                                     // 描述树 → 纯文本
```

支持的语法：

| 块级 | |
|---|---|
| 标题 | `#` ~ `######`（**# 后必须有空白**，`#标签` 是话题标签，不当标题） |
| 段落 | 连续非空行合并成一段 |
| 列表 | `- * +` 与 `1.` `1)`；支持一层缩进；缩进续行接到上一项 |
| 引用 | `>`（多段，空 `>` 分段） |
| 代码块 | ` ``` ` 或 `~~~`，后跟语言标注；**块内不解析行内语法** |
| 表格 | 表头行 + `---` 分隔行；`:---` `:---:` `---:` 定对齐 |
| 分隔线 | `---` / `***` / `___` |

| 行内 | |
|---|---|
| `**粗**` `*斜*` `_斜_` `` `代码` `` `[文本](链接)` `\转义` | |

`style` 的字段（都有缺省，见 `normalizeStyle`）：`width / font / lineHeight / color /
headFont[6] / headColor / codeFont / codeColor / codeBg / codeFamily / quoteColor /
quoteBar / linkColor / hrColor / listGap / blockGap`。应用里由 `theme.js` 的
`markdownStyle` 经 `styleFromTheme()` 适配过来 —— 换主题只改 `theme.js`。

## 已知限制

### 组件本身

1. **没有文本测量接口**：内核 `ctx.drawText` 的返回值在脚本侧拿不到，
   `<text>` 的尺寸也是布局阶段量出来的。所以刻度留白与引用竖条高度靠
   `text-metrics.js` **估算**（CJK 按 1em、其余 0.6em）。比例字体下会偏几像素。
   将来内核补了 `ctx.measureText`，**只改 text-metrics.js 一个文件**。
2. **没有抗锯齿**：整条渲染链路是硬边，斜线有锯齿（与既有原语同口径）。
3. **没有交互**：不做 hover 提示 / 缩放 / 平移；要提示就自己在外面拼 `<tooltip>`。
4. **轴只有线性数值轴**；类目轴等宽排布，不支持时间轴。
5. **列表块的形态由第一项决定**：`- a` 后面紧跟 `1. b` 会被并成同一个列表
   （CommonMark 会切成两个）。`demo/probe.js` 里对这条有断言，改了会立刻现形。
6. Markdown 是**够用就好**的子集，刻意不做：行内 HTML / HTML 块、列表项里的子块
   （子列表与代码块）、脚注、任务列表 `- [ ]`、定义列表、自动链接 `<http://…>`、
   `&amp;` 实体还原。

### 引擎限制（写这个应用时踩到的，别踩第二次）

Gox 的模块作用域有三条**违反不报错、只在跑到那条分支时才炸**的规矩：

1. **函数能看见哪些模块级绑定，取决于它自己怎么声明**：
     - `export function f()` → 看得到 import 绑定 / 别的 export 绑定 / 普通声明
     - 普通 `function f()`（哪怕末尾 `export { f }`）→ **只看得到普通声明**
   于是 `src/` 下所有顶层函数一律 `export function`。
2. **`export` 声明不提升**（普通声明提升）：`export function` 只能调用**写在它前面**
   的 export 函数。`chart.js` 里 `paletteAt/paletteAt2/legendItems/legendRows` 整块排在
   `chartFrame` 之前就是为了这个。
3. **闭包往函数局部变量上赋值传不出来**：`onDraw` 里 `painted = n` 写不回外层函数，
   要写就写到**模块级的 `let`** 上（`demo/probe.js` 的 `paintedCount` 就是这么来的）。

另外一条老规矩照旧（见 `apps/json-toolbox/src/store.js`）：signal 一律**索引取值**，
不要数组解构。

### 验证范围（诚实交代）

- **逻辑**：97 项断言全绿（`demo/probe.js`），覆盖图表的数据 → 坐标映射（手算期望值）、
  扇区角度、边界（空数据 / 画布太小 / 不写尺寸）、Markdown 全部语法的块结构与节点树。
- **真实渲染**：在本仓库的 Linux + Xvfb 环境下只验证到**小窗口**（300×200）。
  本环境的 X11 后端无 SHM / big-request，**单窗口超过约 65536 像素（256KB RGBA）时
  那一帧就推不上去、窗口随即关闭**（仓库自带的 `apps/json-toolbox` 960×700 同样开不住）。
  所以**应用的 820×640 完整窗口未经真机验证** —— 只验证到"整棵组件树能挂载、
  canvas 里 drawChart 真的落笔（实测非底色像素 2699）、Markdown 预览能物化上屏"。
  桌面 / Windows 上按正常尺寸跑不受这条限制。
- `apps/README.md` 第 5 条（新增应用后在 `gfx/apps_smoke_test.go` 的用例表登记冒烟）
  **没做**：仓库里没有这个文件（用例表实际在 `gfx/apps_demo_test.go`，扫的是
  `testdata/apps/`）。按"不进内核"的边界，本任务改用应用内 `demo/probe.js` 承担验证。

## 自测脚本输出摘要

```
$ xvfb-run -a gox apps/notes/demo/probe.js
— 折线图数据映射
  ok   第 2 点 x（正中）  [got=213 want=213]
  ok   第 2 点 y（值 50 → 正中）  [got=152 want=152]
— 饼图 / 环形图
  ok   扇区角度合计一整圈  [got=6.283185307179586 want≈6.283185307179586]
— 真实渲染（GUI）
  ok   折线图真的落笔到帧缓冲  [非底色像素=2699]
通过 97 项，失败 0 项
```

没有 X server 时 GUI 段自动跳过，逻辑段照跑（`通过 95 项，失败 0 项（逻辑段）`）。
