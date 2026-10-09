// 图表组件库 —— 折线图 / 条形图 / 饼图 / 环形图。
//
// ## 它在哪一层
//
// **纯逻辑层**：不 import gox，不碰 signal。输入是 `(ctx, cfg)`，输出是"往 ctx
// 落了一堆笔"。于是它既能被 `<canvas onDraw>` 直接调用，也能被一个**假的 ctx**
// 在探针脚本里跑一遍 —— 后者是本仓库验证纯逻辑的主力手段（见 `demo/`）。
//
// ## 为什么不进 stdlib/
//
// 见 `apps/README.md`：设计令牌与组件**每个应用自带一份**，不建共享目录 ——
// 应用要能整个目录拷走独立运行，依赖 `../_shared` 会断。图表与 Markdown 都属于
// "第二个使用者还没出现"的典型，现在抽公共目录只会把接口锁死在只有第一个
// 使用者见过的形状上。等第二个应用要复用同一份代码时再抽 `apps/_shared/`。
//
// ## 依赖的内核能力
//
// 全部走 `<canvas>` 的 ctx 原语（见 `gfx/canvas.go` 与 `gfx/canvas_path.go`）：
// fillRect / strokeRect / line / drawText / fillArc / fillRing。
// 弧与环形扇区是 rIowkb 那批原语补的 —— 这也是饼图/环形图能做的**前提**：
// 早先 ctx 只有 7 个原语时，饼图只能退成横向条形图。
//
// ## 已知限制（写在最前面，免得读者试出来才发现）
//
//  1. **没有文本测量接口**：ctx 的 `drawText` 返回值在 Go 侧被丢弃了，所以刻度
//     标签的留白靠 `textWidth` **估算**（CJK 按 1em、其余按 0.6em）。比例字体
//     下会偏几像素，够用但不逐像素精确。
//  2. **没有抗锯齿**：整条渲染链路是硬边（与既有原语同口径），斜线会有锯齿。
//  3. **没有交互**：不做 hover 提示 / 缩放 / 平移。要提示就自己在外面拼
//     `<tooltip>`，把命中判定交给脚本。
//  4. **轴只支持线性数值轴**；类目轴等宽排布，不支持时间轴。

import { textWidth } from "./text-metrics.js";

// ===== 缺省配色 =====
//
// 取色偏冷、相邻两项明度错开 —— 目的是**灰度打印也能区分**（记账本要导出）。

export const CHART_PALETTE = [
  "#3355aa", // 蓝
  "#3f9d55", // 绿
  "#d98026", // 橙
  "#c0392b", // 红
  "#7c5cbf", // 紫
  "#2aa198", // 青
  "#b5651d", // 棕
  "#55637a", // 石板灰
];

// ===== 小工具 =====

// num: 取数值，缺失 / 非数值一律回落到 def。
//
// 为什么静默回落而不是报错：绘制函数里的一个 undefined 参数不该让整帧渲染中断
// （"画歪了"立刻看得见，而 JS 侧的栈往往指不到真正写错的那行）。
export function num(v, def) {
  return typeof v === "number" && !isNaN(v) ? v : def;
}

export function str(v, def) {
  return v === undefined || v === null ? def : String(v);
}

export function clamp(v, lo, hi) {
  if (v < lo) return lo;
  if (v > hi) return hi;
  return v;
}

// fmtNum: 定小数位输出。
//
// 顺带把浮点毛刺收敛成 0 —— niceTicks 算出来的 0 常常是 1e-15 这种值，
// 直接 toFixed 会显示成 "-0"，看着像引擎算错了。
export function fmtNum(v, decimals) {
  let d = num(decimals, 0);
  if (d < 0) d = 0;
  let x = num(v, 0);
  if (Math.abs(x) < 1e-9) x = 0;
  return x.toFixed(d);
}

// ===== 刻度 =====

// niceTicks 把 [min,max] 扩成"整齐"的刻度区间。
//
// 步长取 1/2/5 × 10^n：这是刻度算法的通行做法 —— 这样刻度数字位数少、
// 间隔均匀，读图的人不必在心算里做除法。返回值里的 min/max 是**扩过界**的
// （min <= 原 min、max >= 原 max），可以直接当轴的定义域用。
export function niceTicks(min, max, count) {
  const n = num(count, 5) <= 0 ? 5 : num(count, 5);
  let lo = num(min, 0);
  let hi = num(max, 0);
  if (!(hi > lo)) hi = lo + 1; // 空数据 / 单点：给一个不退化区间，免得除零
  const raw = (hi - lo) / n;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const norm = raw / mag;
  let unit = 10;
  if (norm <= 1) unit = 1;
  else if (norm <= 2) unit = 2;
  else if (norm <= 5) unit = 5;
  const step = unit * mag;
  const tmin = Math.floor(lo / step) * step;
  const tmax = Math.ceil(hi / step) * step;
  const ticks = [];
  let v = tmin;
  let guard = 0;
  // +step*0.5 是浮点容差：整圈累加会漂，不用容差会漏掉最后一个刻度
  while (v <= tmax + step * 0.5 && guard < 256) {
    ticks.push(v);
    v = v + step;
    guard = guard + 1;
  }
  return { min: tmin, max: tmax, step: step, ticks: ticks };
}

// dataExtent 取所有系列的最小 / 最大值（跳过 null / undefined —— 那是"断线"）。
export function dataExtent(series) {
  let min = 0;
  let max = 0;
  let found = false;
  for (let si = 0; si < series.length; si++) {
    const pts = series[si].data || [];
    for (let i = 0; i < pts.length; i++) {
      const v = pts[i];
      if (typeof v !== "number") continue;
      if (isNaN(v)) continue;
      if (!found) {
        min = v;
        max = v;
        found = true;
      } else {
        if (v < min) min = v;
        if (v > max) max = v;
      }
    }
  }
  return { min: min, max: max, hasData: found };
}

// ===== 配置归一化 =====

// normalizeChart 把用户给的 cfg 补成完整配置。
//
// 为什么要有这一层：下面每个绘制函数都要读十来个字段，逐个写 `cfg.x || 缺省`
// 既啰嗦又容易漏。归一化一次，绘制函数里只管读。
export function normalizeChart(cfg) {
  const c = cfg || {};
  const font = num(c.font, 11);
  const series = c.series || [];
  const type = str(c.type, "line");
  const pie = type === "pie" || type === "donut";
  const pad = c.padding || {};
  const out = {
    type: type,
    title: str(c.title, ""),
    labels: c.labels || [],
    series: series,
    width: num(c.width, 0),
    height: num(c.height, 0),
    font: font,
    titleFont: num(c.titleFont, font + 3),
    tickCount: num(c.tickCount, 0) > 0 ? num(c.tickCount, 0) : 5,
    // 图例缺省：**单系列不显示**（只有一个系列时图例是纯噪音），多系列才显示。
    showLegend: c.showLegend === undefined ? (pie || series.length > 1) : !!c.showLegend,
    showGrid: c.showGrid === undefined ? true : !!c.showGrid,
    showValues: !!c.showValues,
    background: str(c.background, "#ffffff"),
    grid: str(c.grid, "#eceff4"),
    axis: str(c.axis, "#c3cad6"),
    text: str(c.text, "#5a6472"),
    titleColor: str(c.titleColor, "#1c2430"),
    holeRatio: num(c.holeRatio, 0.55),
    decimals: num(c.decimals, 0),
    padding: {
      top: num(pad.top, 12),
      right: num(pad.right, 14),
      bottom: num(pad.bottom, 8),
      left: num(pad.left, 12),
    },
  };
  // yMin / yMax **只有在显式给了数值时才带上**：chartScale 靠"这个键在不在"
  // （而不是值是不是 undefined）区分"要固定基线"和"按数据自适应"，所以这里
  // 不能无脑复制 —— 缺了这一段的后果是 cfg.yMin 被静默丢掉，固定基线永不生效。
  if (typeof c.yMin === "number" && !isNaN(c.yMin)) out.yMin = c.yMin;
  if (typeof c.yMax === "number" && !isNaN(c.yMax)) out.yMax = c.yMax;
  return out;
}

// chartScale 算出 y 轴的定义域与刻度。
//
// yMin / yMax 显式给定时**照用不扩界** —— 固定基线的图（比如"0~100% 的 CPU"）
// 不该因为这一轮数据没顶到边就自己缩。
export function chartScale(cfg) {
  const ext = dataExtent(cfg.series);
  const hasLo = typeof cfg.yMin === "number";
  const hasHi = typeof cfg.yMax === "number";
  const lo = hasLo ? cfg.yMin : ext.min;
  const hi = hasHi ? cfg.yMax : ext.max;
  if (hasLo || hasHi) {
    const n = cfg.tickCount;
    const step = (hi - lo) / n;
    const ticks = [];
    for (let i = 0; i <= n; i++) ticks.push(lo + step * i);
    return { min: lo, max: hi, step: step, ticks: ticks, hasData: ext.hasData };
  }
  const t = niceTicks(lo, hi, cfg.tickCount);
  t.hasData = ext.hasData;
  return t;
}

export function paletteAt(i) {
  return CHART_PALETTE[i % CHART_PALETTE.length];
}

export function paletteAt2(s, i) {
  if (s.color !== undefined && s.color !== "") return str(s.color, "");
  return paletteAt(i);
}

// legendItems 图例条目：饼图按**标签**逐项，其余按系列逐项。
export function legendItems(cfg) {
  const items = [];
  if (cfg.type === "pie" || cfg.type === "donut") {
    const s0 = cfg.series[0];
    if (s0 !== undefined) {
      const data = s0.data || [];
      const colors = s0.colors || [];
      for (let i = 0; i < data.length; i++) {
        const label = i < cfg.labels.length ? str(cfg.labels[i], "") : String(i);
        const color = i < colors.length ? str(colors[i], "") : paletteAt(i);
        items.push({ name: label, color: color });
      }
    }
    return items;
  }
  for (let i = 0; i < cfg.series.length; i++) {
    const s = cfg.series[i];
    const name = s.name === undefined || s.name === "" ? "系列 " + (i + 1) : str(s.name, "");
    items.push({ name: name, color: paletteAt2(s, i) });
  }
  return items;
}

// legendRows 把条目贪心分行（放不下就换行），返回每行的条目与 x 偏移。
export function legendRows(cfg, W) {
  const items = legendItems(cfg);
  const avail = W - cfg.padding.left - cfg.padding.right;
  const rows = [];
  let cur = [];
  let x = 0;
  for (let i = 0; i < items.length; i++) {
    const w = 8 + 4 + textWidth(items[i].name, cfg.font) + 14;
    if (cur.length > 0 && x + w > avail) {
      rows.push(cur);
      cur = [];
      x = 0;
    }
    cur.push({ item: items[i], x: x });
    x = x + w;
  }
  if (cur.length > 0) rows.push(cur);
  return rows;
}

// chartFrame 算出画布内的绘图区（已扣掉标题与图例占的地方）。
//
// 左边距是**按最长刻度标签算的**，不是写死的：y 标签换成 "1024" 或 "1.5万"
// 时留白会自己跟上，不用改库。
export function chartFrame(ctx, cfg) {
  const sc = chartScale(cfg);
  const W = cfg.width > 0 ? cfg.width : ctx.width;
  const H = cfg.height > 0 ? cfg.height : ctx.height;

  let labelW = 0;
  for (let i = 0; i < sc.ticks.length; i++) {
    const w = textWidth(fmtNum(sc.ticks[i], cfg.decimals), cfg.font);
    if (w > labelW) labelW = w;
  }

  const titleH = cfg.title === "" ? 0 : cfg.titleFont + 10;
  const legendH = cfg.showLegend ? legendRows(cfg, W).length * (cfg.font + 6) + 6 : 0;

  const left = cfg.padding.left + labelW + 8;
  const right = W - cfg.padding.right;
  const top = cfg.padding.top + titleH;
  const bottom = H - cfg.padding.bottom - legendH;

  return {
    W: W,
    H: H,
    sc: sc,
    x0: left,
    x1: right,
    y0: top,
    y1: bottom,
    plotW: right - left,
    plotH: bottom - top,
    legendTop: bottom + 6,
  };
}


// ===== 绘制：轴与背景 =====

export function drawBackground(ctx, cfg) {
  ctx.fillRect(0, 0, ctx.width, ctx.height, cfg.background);
}

export function drawTitle(ctx, cfg, f) {
  if (cfg.title === "") return;
  ctx.drawText(cfg.title, f.x0 - 20, cfg.padding.top, cfg.titleFont, cfg.titleColor);
}

// drawYAxis 画 y 轴：横向网格线 + 右侧对齐的刻度数字。
export function drawYAxis(ctx, cfg, f) {
  const sc = f.sc;
  const span = sc.max - sc.min;
  for (let i = 0; i < sc.ticks.length; i++) {
    const v = sc.ticks[i];
    const y = Math.round(f.y1 - ((v - sc.min) / span) * f.plotH);
    if (cfg.showGrid && v > sc.min && v < sc.max) {
      ctx.line(f.x0, y, f.x1, y, cfg.grid);
    }
    const label = fmtNum(v, cfg.decimals);
    const x = f.x0 - 8 - textWidth(label, cfg.font);
    ctx.drawText(label, x, y - Math.round(cfg.font * 0.6), cfg.font, cfg.text);
  }
  // 轴线（左）
  ctx.line(f.x0, f.y0, f.x0, f.y1, cfg.axis);
}

// drawXAxis 画 x 轴：底线 + 类目标签（按槽位居中）。
export function drawXAxis(ctx, cfg, f) {
  ctx.line(f.x0, f.y1, f.x1, f.y1, cfg.axis);
  const n = cfg.labels.length;
  if (n === 0) return;
  const slot = f.plotW / n;
  for (let i = 0; i < n; i++) {
    const label = str(cfg.labels[i], "");
    if (label === "") continue;
    const cx = f.x0 + slot * (i + 0.5);
    const x = Math.round(cx - textWidth(label, cfg.font) / 2);
    ctx.drawText(label, x, f.y1 + 6, cfg.font, cfg.text);
  }
}

export function drawLegend(ctx, cfg, f) {
  if (!cfg.showLegend) return;
  const rows = legendRows(cfg, f.W);
  for (let r = 0; r < rows.length; r++) {
    const y = f.legendTop + r * (cfg.font + 6);
    const entries = rows[r];
    for (let k = 0; k < entries.length; k++) {
      const e = entries[k];
      const x = f.x0 + e.x;
      ctx.fillRect(x, y + 2, 8, 8, e.item.color);
      ctx.drawText(e.item.name, x + 12, y, cfg.font, cfg.text);
    }
  }
}

export function drawEmpty(ctx, cfg, f) {
  ctx.drawText("（无数据）", f.x0 + 8, f.y0 + 8, cfg.font, cfg.text);
}

// ===== 绘制：三种图 =====

// drawLineChart 折线：系列并列，每条折线按类目槽位取点，点上一颗小实心圆。
//
// null / undefined 表示"这里没有采样"，做法是**断线**（跳过这一段），
// 而不是当 0 —— 当 0 会把"没采到"画成"采到 0"，读数完全不同。
export function drawLineChart(ctx, cfg, f) {
  const sc = f.sc;
  const span = sc.max - sc.min;
  const n = cfg.labels.length;
  for (let si = 0; si < cfg.series.length; si++) {
    const s = cfg.series[si];
    const pts = s.data || [];
    const color = paletteAt2(s, si);
    let px = 0;
    let py = 0;
    let has = false;
    for (let i = 0; i < n && i < pts.length; i++) {
      const v = pts[i];
      if (typeof v !== "number" || isNaN(v)) {
        has = false;
        continue;
      }
      const x = n > 1 ? Math.round(f.x0 + (f.plotW * i) / (n - 1)) : Math.round(f.x0 + f.plotW / 2);
      const y = Math.round(f.y1 - ((v - sc.min) / span) * f.plotH);
      if (has) ctx.line(px, py, x, y, color);
      ctx.fillCircle(x, y, 2, color);
      px = x;
      py = y;
      has = true;
    }
  }
}

// drawBarChart 分组条形：一个类目一组，组内按系列并排。
//
// 基线取 0（当 0 在定义域内），否则取定义域下界 —— 于是全正的序列从底部长上来，
// 含负值的序列从 0 向上下两侧长，都是"看长度就读得出量级"的形态。
export function drawBarChart(ctx, cfg, f) {
  const sc = f.sc;
  const span = sc.max - sc.min;
  const n = cfg.labels.length;
  const k = cfg.series.length;
  if (n === 0 || k === 0) return;
  const zero = sc.min <= 0 && sc.max >= 0 ? Math.round(f.y1 - ((0 - sc.min) / span) * f.plotH) : f.y1;
  const slot = f.plotW / n;
  const inner = slot * 0.72;
  const bw = Math.max(1, Math.round(inner / k));
  for (let i = 0; i < n; i++) {
    const gx = f.x0 + slot * i + (slot - inner) / 2;
    for (let si = 0; si < k; si++) {
      const pts = cfg.series[si].data || [];
      const v = pts[i];
      if (typeof v !== "number" || isNaN(v)) continue;
      const yv = Math.round(f.y1 - ((v - sc.min) / span) * f.plotH);
      const top = Math.min(yv, zero);
      const h = Math.abs(zero - yv);
      if (h <= 0) continue;
      const x = Math.round(gx + bw * si);
      ctx.fillRect(x, top, bw - 1, h, paletteAt2(cfg.series[si], si));
      if (cfg.showValues) {
        const t = fmtNum(v, cfg.decimals);
        ctx.drawText(t, x, top - cfg.font - 2, cfg.font, cfg.text);
      }
    }
  }
}

// drawPieChart 饼图 / 环形图：只画第一个系列，每项一把扇区。
//
// 起点固定在 12 点方向（-π/2）—— 与多数图表库一致，于是"第一项从顶部顺时针
// 铺开"是可以预期的。环形图走 fillRing（按行判定），饼图走 fillArc。
export function drawPieChart(ctx, cfg, f) {
  const s0 = cfg.series[0];
  if (s0 === undefined) return;
  const data = s0.data || [];
  const colors = s0.colors || [];
  let total = 0;
  for (let i = 0; i < data.length; i++) {
    const v = data[i];
    if (typeof v === "number" && !isNaN(v) && v > 0) total = total + v;
  }
  if (total <= 0) return;

  const cx = Math.round((f.x0 + f.x1) / 2);
  const cy = Math.round((f.y0 + f.y1) / 2);
  const r = Math.max(4, Math.round(Math.min(f.plotW, f.plotH) / 2) - 4);
  const ri = Math.round(r * cfg.holeRatio);
  const donut = cfg.type === "donut";

  let a = -Math.PI / 2;
  for (let i = 0; i < data.length; i++) {
    const v = data[i];
    if (typeof v !== "number" || isNaN(v) || v <= 0) continue;
    const sweep = (v / total) * Math.PI * 2;
    const end = a + sweep;
    const color = i < colors.length ? str(colors[i], "") : paletteAt(i);
    if (donut) ctx.fillRing(cx, cy, r, ri, a, end, color);
    else ctx.fillArc(cx, cy, r, a, end, color);
    if (cfg.showValues) {
      const mid = a + sweep / 2;
      const rr = donut ? (r + ri) / 2 : r * 0.62;
      const pct = Math.round((v / total) * 100);
      const t = pct + "%";
      const tx = Math.round(cx + Math.cos(mid) * rr - textWidth(t, cfg.font) / 2);
      const ty = Math.round(cy + Math.sin(mid) * rr - cfg.font * 0.6);
      ctx.drawText(t, tx, ty, cfg.font, cfg.text);
    }
    a = end;
  }
}

// drawChart 统一入口：按 cfg.type 分派。
//
// 用法（在 `<canvas onDraw>` 里）：
//
//	<canvas width={420} height={240} onDraw={(ctx) => drawChart(ctx, cfg)} />
//
// cfg 未给 width / height 时按画布当前尺寸画 —— 于是同一份配置能塞进不同大小的
// 卡片里，不必在配置里重复写一遍尺寸。
export function drawChart(ctx, cfg) {
  const c = normalizeChart(cfg);
  drawBackground(ctx, c);
  const f = chartFrame(ctx, c);

  if (!f.sc.hasData) {
    drawEmpty(ctx, c, f);
    return;
  }
  if (f.plotW <= 8 || f.plotH <= 8) {
    // 画布太小：画了也是一团糊，给一句提示比画错强
    ctx.drawText("（画布太小）", 6, 6, c.font, c.text);
    return;
  }

  drawTitle(ctx, c, f);
  if (c.type === "bar") {
    drawYAxis(ctx, c, f);
    drawXAxis(ctx, c, f);
    drawBarChart(ctx, c, f);
  } else if (c.type === "pie" || c.type === "donut") {
    drawPieChart(ctx, c, f);
  } else {
    drawYAxis(ctx, c, f);
    drawXAxis(ctx, c, f);
    drawLineChart(ctx, c, f);
  }
  drawLegend(ctx, c, f);
}

// ===== 为什么连内部辅助函数都写着 `export function`，且顺序不能随便调 =====
//
// 看着像"导出面过宽 + 洁癖"，其实是**两条引擎限制**逼出来的（最小复现见
// apps/notes/README.md「引擎限制」一节）：
//
//  1. 函数能看见哪些模块级绑定，**取决于它自己是怎么声明的**：
//       `export function f()`  → 看得到 import 绑定 / 别的 export 绑定 / 普通声明
//       `function f()`（哪怕末尾 `export { f }`）→ **只看得到普通声明**
//     所以"普通函数引用 import / export 绑定"会在**跑到那条分支时才**抛
//     `ReferenceError: xxx is not defined`。
//  2. **`export` 声明不提升**（普通声明提升）：`export function` 只能调用**写在
//     它前面**的 export 函数。上面 `paletteAt / paletteAt2 / legendItems /
//     legendRows` 整块排在 `chartFrame` 之前，就是为了这个 —— 别为了"看起来更顺"
//     把它们挪到后面，那样图例一出现就炸。
//
// 于是本文件的规矩：**顶层函数一律 `export function`，常量一律 `export const`；
// 被谁调用，就写在谁的上面。** 违反不会在加载时报错，只会在某个分支第一次跑到
// 时抛 ReferenceError —— `demo/probe.js` 就是用来把这种错误挡在提交前的。
