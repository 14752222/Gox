// 应用状态与动作。
//
// 跨组件共享状态的**全部机制**就是"把 signal 建在模块作用域"（与 json-toolbox 同）。
// 派生值不另存一份，写成读 signal 的普通函数即可 —— 第二份状态一定会和第一份对不上。
//
// ⚠️ 两条引擎纪律，改这个文件时别踩：
//  1. **signal 一律用索引取值**（`const p = createSignal(v); const get = p[0];`）。
//     不要写 `const [x, setX] = createSignal(v)`：数组解构出的绑定在模块作用域下
//     被同模块函数引用时会报 `ReferenceError: setX is not defined`（见
//     json-toolbox/src/store.js 的头注释）。
//  2. **一律先普通声明、末尾统一 `export { … }`**。Gox 的模块里，未导出的函数
//     引用不到在声明处就 export 的绑定（详见 `lib/chart.js` 末尾）。
import { createSignal } from "gox";

// ===== 笔记数据 =====
//
// 每篇笔记带一份 chart 配置 —— 于是"图表"页不用另做一套数据编辑器，
// 换笔记就换图，四种图型各有一篇能直接看的样例。
const SAMPLE_NOTES = [
  {
    id: "weekly",
    title: "周报：第 41 周",
    chart: {
      type: "line",
      title: "每日投入（小时）",
      labels: ["一", "二", "三", "四", "五", "六", "日"],
      series: [
        { name: "编码", data: [6.5, 7, 5.5, 8, 7.5, 2, 0] },
        { name: "会议", data: [1.5, 2, 3, 1, 2.5, 0, 0] },
      ],
      decimals: 1,
    },
    body: [
      "# 第 41 周",
      "",
      "**主线**：把图表与 Markdown 两个 lib 落到 `apps/notes/`，不进内核。",
      "",
      "## 做完的",
      "",
      "- 折线 / 条形 / 饼图 / 环形四型，全部走 `ctx` 原语",
      "- Markdown 块级：标题、段落、列表、引用、代码块、表格、分隔线",
      "- 行内：**粗** / *斜* / `代码` / [链接](https://example.com)",
      "",
      "> 没有文本测量接口，刻度留白是**估算**的 —— 见 README 的已知限制。",
      "",
      "## 下周",
      "",
      "1. 引用块竖条高度按估算行数算，误差可接受",
      "2. 等第二个应用要用同一份代码，再抽 `apps/_shared/`",
    ].join("\n"),
  },
  {
    id: "spend",
    title: "月度开销",
    chart: {
      type: "bar",
      title: "近六个月（元）",
      labels: ["5月", "6月", "7月", "8月", "9月", "10月"],
      series: [
        { name: "餐饮", data: [1200, 1350, 1180, 1420, 1290, 1310] },
        { name: "交通", data: [320, 300, 410, 260, 350, 290] },
      ],
      showValues: false,
    },
    body: [
      "# 月度开销",
      "",
      "| 月份 | 餐饮 | 交通 | 合计 |",
      "| --- | ---: | ---: | ---: |",
      "| 5月 | 1200 | 320 | 1520 |",
      "| 6月 | 1350 | 300 | 1650 |",
      "| 7月 | 1180 | 410 | 1590 |",
      "",
      "- 餐饮是**大头**，占比常年过七成",
      "- 交通波动主要来自打车",
      "",
      "---",
      "",
      "记账本要能导出，所以图表配色取冷色且相邻明度错开（灰度打印也能分）。",
    ].join("\n"),
  },
  {
    id: "time",
    title: "时间分配",
    chart: {
      type: "donut",
      title: "一天 24 小时去哪了",
      labels: ["睡觉", "工作", "通勤", "吃饭", "其他"],
      series: [{ data: [7.5, 9, 1.5, 1.5, 4.5] }],
      showValues: true,
    },
    body: [
      "# 时间分配（环形图）",
      "",
      "环形图与饼图共用 `drawPieChart`，区别只在 `type` 与中间的空洞比例：",
      "",
      "```js",
      "drawChart(ctx, { type: 'donut', holeRatio: 0.55, series: [{ data: [...] }] });",
      "```",
      "",
      "`holeRatio` 是**内半径 / 外半径**，0 就是饼图，0.9 就只剩一圈细环。",
      "",
      "> 数值 <= 0 的项会被跳过 —— 占比为 0 的类别不该占一格扇区。",
    ].join("\n"),
  },
  {
    id: "cheatsheet",
    title: "语法速查",
    chart: {
      type: "pie",
      title: "笔记里各类块占比",
      labels: ["标题", "列表", "代码", "表格", "引用"],
      series: [{ data: [12, 30, 18, 8, 6] }],
      showValues: true,
    },
    body: [
      "# 支持的语法",
      "",
      "## 块级",
      "",
      "- 标题 `#` ~ `######`（**# 后必须有空格**，否则 `#标签` 不当标题）",
      "- 列表 `- * +` 与 `1.`；支持一层缩进嵌套",
      "- 引用 `>`（多段）",
      "- 围栏代码块 ``` 与 ~~~，第一串后面的内容是语言标注",
      "- 表格：表头行 + `---` 分隔行，`:---` / `---:` / `:---:` 定对齐",
      "- 分隔线 `---` / `***` / `___`",
      "",
      "## 行内",
      "",
      "- **粗** `**x**`",
      "- *斜* `*x*` 或 `_x_`",
      "- `代码` 反引号（里面**不解析**行内语法）",
      "- [链接](https://example.com)",
      "- 转义 `\\*不是斜体\\*`",
      "",
      "## 刻意不做",
      "",
      "- 行内 HTML / HTML 块",
      "- 列表项里的子块（子列表、代码块）",
      "- 脚注、任务列表、自动链接、`&amp;` 实体",
    ].join("\n"),
  },
];

const pNotes = createSignal(SAMPLE_NOTES);
const notes = pNotes[0];
const setNotes = pNotes[1];

const pSelId = createSignal("weekly");
const selId = pSelId[0];
const setSelId = pSelId[1];

const pTab = createSignal("preview"); // preview | chart
const tab = pTab[0];
const setTab = pTab[1];

const pStatus = createSignal("就绪 —— 左边选笔记，下边改正文，上边切预览/图表");
const status = pStatus[0];
const setStatus = pStatus[1];

// ===== 派生 =====

// current: 当前笔记。找不到（改过 id / 空列表）就退到第一条，界面不该出现"没有选中"。
export function current() {
  const list = notes();
  const id = selId();
  for (let i = 0; i < list.length; i++) {
    if (list[i].id === id) return list[i];
  }
  return list.length > 0 ? list[0] : null;
}

// currentBody / currentChart 是给组件当**取值函数**用的（组件只在函数体内读 signal
// 才能建立依赖；在组件体里 `current().body` 只拿到第一帧的快照）。
export function currentBody() {
  const n = current();
  return n === null ? "" : n.body;
}

export function currentChart() {
  const n = current();
  return n === null ? null : n.chart;
}

// ===== 动作 =====

export function select(id) {
  setSelId(id);
  const list = notes();
  for (let i = 0; i < list.length; i++) {
    if (list[i].id === id) {
      setStatus("已选「" + list[i].title + "」");
      return;
    }
  }
}

// setBody: 改当前笔记的正文。
//
// **造新对象、换新数组** —— 原地改字段引用没变，`each` 的行不会重建（apps/README
// 第 3 条）。这里没有二次状态，但还是按同一条纪律写，免得以后加字段时忘了。
export function setBody(text) {
  const id = selId();
  const list = notes();
  const next = [];
  let hit = false;
  for (let i = 0; i < list.length; i++) {
    const n = list[i];
    if (n.id === id) {
      hit = true;
      next.push({ id: n.id, title: n.title, body: text, chart: n.chart });
    } else {
      next.push(n);
    }
  }
  if (!hit) return;
  setNotes(next);
  setStatus("已更新 —— 正文 " + text.length + " 字符");
}

// signal 这几根是**普通 const**（不是函数），走末尾 `export {}` 导出 ——
// 与 json-toolbox/src/store.js 同一条写法。
export { notes, selId, tab, setTab, status };
