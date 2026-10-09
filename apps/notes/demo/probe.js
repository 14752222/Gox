// 自测 / 演示脚本 —— 用 `gox` 直接跑，不进 gfx 的 Go 测试。
//
//	go build -o /tmp/gox-current ./cmd/gox
//	/tmp/gox-current apps/notes/demo/probe.js            # 无 X 时 GUI 段自动跳过
//	xvfb-run -a /tmp/gox-current apps/notes/demo/probe.js # 全量（含真实光栅化校验）
//
// ## 为什么分成两段
//
// 第一段**纯逻辑**：给 drawChart 喂一个**假 ctx**（只记笔，不画），然后断言落笔的
// **坐标** —— 这才是"数据 → 像素位置"的映射是否正确的硬证据（`lib/` 不许 import gox，
// 所以脱离 UI 单测是本仓库验证纯逻辑的主力手段，见 apps/README.md）。
//
// 第二段**真实渲染**：真的开一个窗口，在 `<canvas onDraw>` 里调 drawChart 后用
// `ctx.getImageData` 数一遍非底色像素 —— 证明它**真的画出了东西**，而不是只把
// 调用记下来。没有 X server 时这一段会失败，脚本会打印 SKIP 并跳过（逻辑段照跑）。
//
// ⚠️ 窗口刻意只用 **300×200**：本仓库的 X11 后端在无 SHM / 无 big-request 的
// Xvfb 上，单窗口超过约 65536 像素（256KB RGBA）时那一帧的 PutImage 会失败、
// 窗口随即关闭（仓库自带的 json-toolbox 960×700 在本环境同样开不住）。
// 这是**环境限制**，不是应用问题 —— 桌面 / Windows 上按 820×640 跑没问题。
//
// ⚠️ 本文件所有顶层函数一律 `export function` —— 与 src/ 下同一条引擎限制
// （详见 apps/notes/README.md「引擎限制」）。
import { h, render } from "gox";
import {
  niceTicks,
  dataExtent,
  fmtNum,
  chartFrame,
  chartScale,
  normalizeChart,
  legendItems,
  drawChart,
  CHART_PALETTE,
} from "../src/lib/chart.js";
import {
  parseMarkdown,
  renderMarkdown,
  nodeText,
  markdownText,
  parseInline,
  splitRow,
  alignmentOf,
  isTableStart,
} from "../src/lib/markdown.js";
import { textWidth, estimateLines } from "../src/lib/text-metrics.js";
import { MarkdownView, materialize } from "../src/components/markdown-view.js";
import { ChartView } from "../src/components/chart-view.js";
import { App } from "../src/app.js";

let pass = 0;
let fail = 0;
// GUI 段的结果放在**模块作用域**：onDraw 是个闭包，往**函数局部**变量上赋值
// 传不出来（引擎限制，见 README），往模块级的 let 上写才拿得到。
let paintedCount = -1;

export function ok(cond, name, extra) {
  if (cond) {
    pass = pass + 1;
    console.log("  ok   " + name + (extra === "" ? "" : "  [" + extra + "]"));
  } else {
    fail = fail + 1;
    console.log("  FAIL " + name + (extra === "" ? "" : "  [" + extra + "]"));
  }
}

export function eq(actual, want, name) {
  ok(actual === want, name, "got=" + actual + " want=" + want);
}

export function near(actual, want, tol, name) {
  const d = actual - want;
  ok(d > -tol && d < tol, name, "got=" + actual + " want≈" + want);
}

// ===== 假 ctx：只记笔，不画 =====

export function fakeCtx(w, h) {
  const ops = [];
  const ctx = { width: w, height: h, ops: ops, isFake: true };
  ctx.fillRect = function (x, y, ww, hh, c) { ops.push(["fillRect", x, y, ww, hh, c]); };
  ctx.strokeRect = function (x, y, ww, hh, c) { ops.push(["strokeRect", x, y, ww, hh, c]); };
  ctx.line = function (x1, y1, x2, y2, c) { ops.push(["line", x1, y1, x2, y2, c]); };
  ctx.drawText = function (t, x, y, f, c) { ops.push(["drawText", t, x, y, f, c]); };
  ctx.fillCircle = function (x, y, r, c) { ops.push(["fillCircle", x, y, r, c]); };
  ctx.fillArc = function (cx, cy, r, a0, a1, c) { ops.push(["fillArc", cx, cy, r, a0, a1, c]); };
  ctx.fillRing = function (cx, cy, r, ri, a0, a1, c) { ops.push(["fillRing", cx, cy, r, ri, a0, a1, c]); };
  return ctx;
}

export function opsOf(ops, name) {
  const out = [];
  for (let i = 0; i < ops.length; i++) if (ops[i][0] === name) out.push(ops[i]);
  return out;
}

export function hasOp(ops, name, at, want) {
  const list = opsOf(ops, name);
  for (let i = 0; i < list.length; i++) {
    if (list[i][at] === want) return true;
  }
  return false;
}

// ===== 1. 文本度量 =====

export function testTextMetrics() {
  console.log("— 文本度量");
  eq(textWidth("abc", 10), 18, "拉丁按 0.6em");
  eq(textWidth("中文", 10), 20, "CJK 按 1em");
  eq(textWidth("", 10), 0, "空串 0");
  eq(estimateLines("", 13, 100), 1, "空文本算 1 行");
  ok(estimateLines("a", 13, 1) > 1, "宽度不足会折行");
}

// ===== 2. 刻度与格式化 =====

export function testScale() {
  console.log("— 刻度 / 格式化");
  const t = niceTicks(3, 97, 5);
  eq(t.min, 0, "刻度下界扩到 0");
  eq(t.max, 100, "刻度上界扩到 100");
  eq(t.step, 20, "步长 1/2/5×10^n");
  eq(t.ticks.length, 6, "含两端共 6 个刻度");

  const flat = niceTicks(5, 5, 5);
  ok(flat.max > flat.min, "单点/空数据不退化成除零");

  eq(fmtNum(1e-15, 2), "0.00", "浮点毛刺收敛成 0（不显示 -0）");
  eq(fmtNum(12.345, 1), "12.3", "按 decimals 定小数位");

  const ext = dataExtent([{ data: [3, null, 7] }, { data: [-2, undefined] }]);
  eq(ext.min, -2, "extent 取最小（跳过 null/undefined）");
  eq(ext.max, 7, "extent 取最大");
  eq(dataExtent([{ data: [] }]).hasData, false, "空系列 hasData=false");
}

// ===== 3. 折线图：数据 → 坐标 =====
//
// 期望值是**手算**出来的（下面每一步都在注释里），不是从 chartFrame 抄的 ——
// 否则就是拿实现验证实现。
//
// cfg: width 400 / height 300 / yMin 0 / yMax 100 / font 11（缺省）
//   刻度 0,20,40,60,80,100 → 最长标签 "100" → labelW = round(3×0.6×11) = 20
//   左边距 = 12(padding) + 20(labelW) + 8 = 40        右边 = 400 - 14 = 386
//   上边 = 12（无标题）                                下边 = 300 - 8 - 0（单系列不显图例）= 292
//   plotW = 386 - 40 = 346                            plotH = 292 - 12 = 280
//   三点 data [0, 50, 100] → x = 40 / 40+346/2=213 / 386
//                            y = 292 / 292-140=152 / 292-280=12
export function testLineChart() {
  console.log("— 折线图数据映射");
  const cfg = {
    type: "line",
    width: 400,
    height: 300,
    labels: ["a", "b", "c"],
    series: [{ name: "s", data: [0, 50, 100] }],
    yMin: 0,
    yMax: 100,
  };
  const ctx = fakeCtx(400, 300);
  drawChart(ctx, cfg);
  const ops = ctx.ops;

  eq(opsOf(ops, "fillRect")[0][4], 300, "先铺满整块背景");

  const circles = opsOf(ops, "fillCircle");
  eq(circles.length, 3, "三个数据点各一颗实心点");
  eq(circles[0][1], 40, "第 1 点 x（贴左轴）");
  eq(circles[0][2], 292, "第 1 点 y（值 0 → 贴底）");
  eq(circles[1][1], 213, "第 2 点 x（正中）");
  eq(circles[1][2], 152, "第 2 点 y（值 50 → 正中）");
  eq(circles[2][1], 386, "第 3 点 x（贴右边）");
  eq(circles[2][2], 12, "第 3 点 y（值 100 → 贴顶）");
  eq(circles[0][4], CHART_PALETTE[0], "缺省取调色板第 1 色");

  // 折线 2 段 + y 轴 1 条 + x 轴底线 1 条 + 4 条网格（不含两端刻度）
  eq(opsOf(ops, "line").length, 8, "2 段折线 + 轴线 2 + 网格 4");
  ok(hasOp(ops, "line", 2, 152), "存在 y=152 的横向网格/连线");
  ok(hasOp(ops, "line", 1, 40), "y 轴画在 x0=40");

  const sc = chartScale(normalizeChart(cfg));
  eq(sc.step, 20, "显式 yMin/yMax 时不扩界，五等分步长 20");
  eq(legendItems(normalizeChart(cfg)).length, 1, "图例按系列逐项");
}

// ===== 4. 条形图 =====
//
// labels 2 个 / yMin 0 / yMax 20 / data [10, 20]
//   刻度 0,4,8,12,16,20 → 最长标签 2 字符 → labelW = round(2×0.6×11) = 13
//   左 = 12+13+8 = 33，右 = 386，plotW = 353，plotH = 280
//   基线 zero = 292；值 10 → 高 140，值 20 → 高 280（正好 2 倍）
export function testBarChart() {
  console.log("— 条形图");
  const cfg = {
    type: "bar",
    width: 400,
    height: 300,
    labels: ["a", "b"],
    series: [{ name: "x", data: [10, 20] }],
    yMin: 0,
    yMax: 20,
  };
  const ctx = fakeCtx(400, 300);
  drawChart(ctx, cfg);
  const bars = [];
  const rects = opsOf(ctx.ops, "fillRect");
  for (let i = 0; i < rects.length; i++) {
    if (rects[i][4] !== 300) bars.push(rects[i]); // 排除整块背景
  }
  eq(bars.length, 2, "两个类目各一根条");
  if (bars.length === 2) {
    eq(bars[0][4], 140, "值 10 → 高 140");
    eq(bars[1][4], 280, "值 20 → 高 280");
    ok(bars[1][2] < bars[0][2], "值大的条更靠上");
  }
}

// ===== 5. 饼图 / 环形图 =====
//
// 4 项等值 → 4 个扇区，每个 π/2，起点 -π/2（12 点方向）。
export function testPieChart() {
  console.log("— 饼图 / 环形图");
  const base = {
    width: 300,
    height: 300,
    labels: ["a", "b", "c", "d"],
    series: [{ data: [1, 1, 1, 1] }],
  };

  const pie = fakeCtx(300, 300);
  drawChart(pie, { type: "pie", width: 300, height: 300, labels: base.labels, series: base.series });
  const arcs = opsOf(pie.ops, "fillArc");
  eq(arcs.length, 4, "饼图 4 个扇区");
  let sum = 0;
  for (let i = 0; i < arcs.length; i++) sum = sum + (arcs[i][5] - arcs[i][4]);
  near(sum, Math.PI * 2, 1e-9, "扇区角度合计一整圈");
  near(arcs[0][4], -Math.PI / 2, 1e-9, "起点在 12 点方向");
  eq(hasOp(pie.ops, "fillRing", 0, 0), false, "饼图不走 fillRing");

  const donut = fakeCtx(300, 300);
  drawChart(donut, { type: "donut", width: 300, height: 300, labels: base.labels, series: base.series });
  const rings = opsOf(donut.ops, "fillRing");
  eq(rings.length, 4, "环形图 4 个扇区");
  eq(rings[0][4], Math.round(rings[0][3] * 0.55), "内半径 = 外半径 × holeRatio(0.55)");

  const withZero = fakeCtx(300, 300);
  drawChart(withZero, {
    type: "pie",
    width: 300,
    height: 300,
    labels: ["有", "零", "负"],
    series: [{ data: [3, 0, -1] }],
  });
  eq(opsOf(withZero.ops, "fillArc").length, 1, "值 <= 0 的项不占扇区");
}

// ===== 6. 边界：空数据 / 画布太小 =====

export function testEdgeCases() {
  console.log("— 边界");
  const empty = fakeCtx(300, 200);
  drawChart(empty, { type: "line", width: 300, height: 200, labels: ["a"], series: [{ data: [] }] });
  ok(hasOp(empty.ops, "drawText", 1, "（无数据）"), "空数据画提示而不是崩");

  const tiny = fakeCtx(30, 30);
  drawChart(tiny, { type: "line", width: 30, height: 30, labels: ["a", "b"], series: [{ data: [1, 2] }] });
  ok(hasOp(tiny.ops, "drawText", 1, "（画布太小）"), "画布太小给提示而不是画糊的图");

  // cfg 不给 width/height 时按画布尺寸画
  const auto = fakeCtx(240, 160);
  const f = chartFrame(auto, normalizeChart({ type: "line", labels: ["a", "b"], series: [{ data: [1, 2] }] }));
  eq(f.W, 240, "未给 width 时取 ctx.width");
  eq(f.H, 160, "未给 height 时取 ctx.height");
}

// ===== 7. Markdown：块级解析 =====

export function testMarkdownBlocks() {
  console.log("— Markdown 块级");

  const b = parseMarkdown(
    [
      "# 一级标题",
      "",
      "正文 **粗** *斜* `代码` [链接](https://example.com) 转义 \\*不是斜体\\*",
      "",
      "- 无序一",
      "  - 无序二（缩进）",
      "",
      "> 引用第一段",
      ">",
      "> 引用第二段",
      "",
      "```js",
      "const a = 1;",
      "```",
      "",
      "| 名称 | 数量 | 单价 |",
      "| :--- | :---: | ---: |",
      "| 苹果 | 3 | 2.5 |",
      "| 梨 | 10 | 1.0 |",
      "",
      "---",
      "",
      "#标签不是标题",
    ].join("\n")
  );

  const kinds = [];
  for (let i = 0; i < b.length; i++) kinds.push(b[i].kind);
  eq(kinds.join(","), "heading,paragraph,list,quote,code,table,hr,paragraph", "块序列与判据顺序");

  eq(b[0].level, 1, "ATX 标题级别");
  eq(b[7].runs[0].text, "#标签不是标题", "# 后无空白 → 段落（话题标签不当标题）");

  const runs = b[1].runs;
  let bold = 0, italic = 0, code = 0, link = "";
  for (let i = 0; i < runs.length; i++) {
    if (runs[i].bold) bold = runs[i].text;
    if (runs[i].italic) italic = runs[i].text;
    if (runs[i].code) code = runs[i].text;
    if (runs[i].href !== "") link = runs[i].href;
  }
  eq(bold, "粗", "**粗**");
  eq(italic, "斜", "*斜*");
  eq(code, "代码", "`代码`");
  eq(link, "https://example.com", "[文本](链接)");
  ok(nodeText(renderMarkdown("转义 \\*不是斜体\\*", {})).indexOf("*不是斜体*") >= 0, "\\* 转义成字面星号");

  eq(b[2].ordered, false, "无序列表");
  eq(b[2].items.length, 2, "无序两项（含缩进续接）");
  eq(b[2].items[1].indent, 2, "缩进 2 空格 → indent=2");

  const ol = parseMarkdown("1. 有序一\n2. 有序二\n");
  eq(ol[0].kind, "list", "有序列表");
  eq(ol[0].ordered, true, "数字 + . / ) 开头 → ordered");
  eq(ol[0].items.length, 2, "有序两项");

  // 已知限制（写进 README 了）：列表块的形态由**第一项**定，无序与有序相邻
  // 不会切成两个列表 —— 断言在这里是为了让"将来改了"立刻现形。
  const mixed = parseMarkdown("- a\n1. b\n");
  eq(mixed.length, 1, "无序与有序相邻合成一个列表（已知限制）");
  eq(mixed[0].ordered, false, "列表形态由第一项决定");

  eq(b[3].paragraphs.length, 2, "空 > 行把引用分段");
  eq(b[4].lang, "js", "围栏后那串是语言标注");
  eq(b[4].lines.length, 1, "代码块原样收行");
  eq(b[4].closed, true, "围栏闭合");

  eq(b[5].header.join("|"), "名称|数量|单价", "表头");
  eq(b[5].rows.length, 2, "数据行");
  eq(b[5].align.join(","), "left,center,right", ":--- / :---: / ---: 三种对齐");
  eq(alignmentOf(":---:"), "center", "alignmentOf 居中");
  eq(isTableStart(["| a |", "| --- |"], 0), true, "有分隔行才算表格");
  eq(isTableStart(["a|b", "正文"], 0), false, "正文里的竖线不误判成表格");
  eq(splitRow("| a | b |").length, 2, "splitRow 吃掉首尾竖线");
}

// ===== 8. Markdown：渲染成节点描述树 =====

export function testMarkdownTree() {
  console.log("— Markdown 节点树");

  const root = renderMarkdown("# 标题\n\n段落 中文\n\n```\nx\n```\n\n> 引用\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n\n---\n", {
    width: 400,
  });
  eq(root.tag, "column", "根节点是 column");
  eq(root.props.width, 400, "根节点带排版宽度");

  const kids = root.children;
  eq(kids.length, 6, "六个块各一个子节点（标题/段落/代码/引用/表格/分隔线）");
  eq(kids[0].tag, "row", "标题 → row(wrap)");
  eq(kids[0].children[0].props.font, 21, "h1 字号 = 13 + 8");
  eq(kids[0].children[0].children[0], "标题", "标题文本");

  // CJK 逐字切分成多个 <text>（gfx 的 <text> 是整盒，折行只能发生在盒之间）
  const para = kids[1];
  eq(para.tag, "row", "段落 → row(wrap)");
  ok(para.children.length >= 4, "中文段落被切成多个可折行的 text 盒", "n=" + para.children.length);
  eq(nodeText(para), "段落 中文", "nodeText 能把树拼回原文");

  eq(kids[2].tag, "column", "代码块 → column");
  eq(kids[2].props.background, "#eef1f6", "代码块有底色");
  eq(kids[2].children[0].props.fontFamily, "monospace", "代码块等宽");

  eq(kids[3].tag, "row", "引用 → row（竖条 + 正文列）");
  eq(kids[3].children[0].tag, "rect", "引用左侧是一块窄 rect");
  eq(kids[3].children[0].props.width, 3, "竖条宽 3");
  ok(kids[3].children[0].props.height > 0, "竖条高度 > 0（按估算行数算）");

  eq(kids[4].tag, "table", "表格 → table（数据进、节点树出）");
  eq(kids[4].props.columns.length, 2, "两列");
  eq(kids[4].props.rows[0].c0, "1", "行按列的 key 取值");

  eq(kids[5].tag, "rect", "分隔线 → rect");
  eq(kids[5].props.height, 1, "分隔线高 1px");

  // 描述树能被真的 h() 物化（未知标签会被 h() 警告一次，这里顺带证明标签都在白名单里）
  const built = materialize(root);
  ok(String(built).indexOf("GuiNode column") >= 0, "描述树被 h() 物化成真的 GuiNode", String(built));

  const t = markdownText("# 标题\n\n- a\n- b\n");
  ok(t.indexOf("标题") >= 0 && t.indexOf("a\nb") >= 0, "markdownText 取整篇纯文本");
  eq(parseInline("*斜*")[0].italic, true, "parseInline 单独可用");
}

// ===== 9. 真实渲染（要 X / xvfb）=====

// paintedPixels 返回画布上"不是底色"的像素数 —— 用 getImageData 读真实帧缓冲，
// 这是"确实画上去了"的硬证据（假 ctx 只能证明调用序列对）。
export function paintedPixels(ctx, bg) {
  const img = ctx.getImageData(0, 0, ctx.width, ctx.height);
  if (img === null || img === undefined) return -1;
  let n = 0;
  const d = img.data;
  for (let i = 0; i < d.length; i = i + 4) {
    if (d[i] !== bg[0] || d[i + 1] !== bg[1] || d[i + 2] !== bg[2]) n = n + 1;
  }
  return n;
}

export function runGui() {
  console.log("— 真实渲染（GUI）");
  const lineCfg = {
    type: "line",
    title: "每日投入",
    labels: ["一", "二", "三", "四", "五"],
    series: [
      { name: "编码", data: [6, 7, 5, 8, 7] },
      { name: "会议", data: [1, 2, 3, 1, 2] },
    ],
  };
  let mounted = false;

  const win = render(
    <window title="apps/notes 自测" width={300} height={200}>
      <column gap={4} padding={4}>
        <canvas
          width={280}
          height={90}
          onDraw={(ctx) => {
            drawChart(ctx, lineCfg);
            // onDraw 会跑两遍（收依赖那遍是空操作 ctx，getImageData 拿不到图），
            // 所以只认拿得到图的那次。
            const n = paintedPixels(ctx, [255, 255, 255]);
            if (n >= 0) paintedCount = n;
          }}
        />
        <MarkdownView source={"# 挂上了\n\n- 一\n- 二\n\n> 引用"} width={280} height={60} />
        <ChartView cfg={() => lineCfg} width={280} height={40} />
        <App />
      </column>
    </window>
  );
  mounted = true;

  setTimeout(() => {
    ok(mounted, "窗口挂上（图表 canvas / Markdown 预览 / 完整 App 同时挂载不报错）");
    ok(paintedCount > 500, "折线图真的落笔到帧缓冲", "非底色像素=" + paintedCount);
    console.log("");
    console.log("通过 " + pass + " 项，失败 " + fail + " 项");
    win.close();
    if (fail > 0) throw new Error(fail + " 项断言失败");
  }, 400);
}

// ===== 入口 =====

export function runLogic() {
  testTextMetrics();
  testScale();
  testLineChart();
  testBarChart();
  testPieChart();
  testEdgeCases();
  testMarkdownBlocks();
  testMarkdownTree();
}

runLogic();

try {
  runGui();
} catch (e) {
  // 没有 X server（CI / 纯终端）时 render() 会失败：逻辑段已经跑完，这里降级。
  console.log("");
  console.log("SKIP 真实渲染段 —— GUI 起不来（无 X server）：" + e);
  console.log("（要覆盖这一段：xvfb-run -a gox apps/notes/demo/probe.js）");
  console.log("");
  console.log("通过 " + pass + " 项，失败 " + fail + " 项（逻辑段）");
  if (fail > 0) throw new Error(fail + " 项断言失败");
}
