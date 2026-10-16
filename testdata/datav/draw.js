// ===== gx-datav canvas 绘制工具层 =====
//
// 内核 canvas 已经有路径系统 (arc / beginPath / moveTo / lineTo / fill /
// stroke / fillArc / strokeArc / fillRing / translate / rotate / save /
// restore / lineWidth), 但 GUI 指南里的 canvas 一节只写到矩形/圆/直线那
// 几个原语 —— 文档滞后于代码。这里补齐图表缺的那一层, 全部是"把内核原语
// 组合成图表零件"的薄封装, 不引入任何新依赖:
//
//   smoothPath   Catmull-Rom 平滑折线 (内核没有 bezierCurveTo, 用采样逼近)
//   areaFill     折线下方的半透明面积 (分层叠加出渐变观感)
//   vGradient    垂直渐变条 (同一原理的通用版)
//   glowCircle   光晕点 (同心半透明圆, 大屏"数据点发光"的标配)
//   dashedLine   虚线
//   grid         坐标网格
//   roundBar     圆角柱 (fillRect + fillCircle 拼, 内核无圆角路径填充)
//   tickLabels   刻度标签
//   centerText   水平居中文本 (宽度走 theme.textWidth 估算)
//
// 约定: 所有函数只动 ctx, 不读外部状态; 颜色一律传字符串; 坐标都是
// **画布局部坐标** (调用方自己 translate/scale)。

import { textWidth, clamp } from "./theme.js";

// smoothPath(ctx, pts, tension): 用 Catmull-Rom 样条把折线采样成平滑曲线。
// pts = [{x, y}, ...]; tension 0 = 原折线, 1 = 标准 Catmull-Rom。
// 为什么采样而不是直接描点: 内核路径只有 lineTo, 没有二次/三次贝塞尔;
// 每段采样 ~12 个点, 视觉上与真样条无差, 成本忽略不计。
export function smoothPath(ctx, pts, tension) {
  const t = tension === undefined ? 1 : tension;
  const n = pts.length;
  if (n === 0) return;
  if (n === 1) { ctx.moveTo(pts[0].x, pts[0].y); return; }
  ctx.moveTo(pts[0].x, pts[0].y);
  const SEG = 12;
  for (let i = 0; i < n - 1; i++) {
    const p0 = pts[i - 1] || pts[i];
    const p1 = pts[i];
    const p2 = pts[i + 1];
    const p3 = pts[i + 2] || p2;
    for (let s = 1; s <= SEG; s++) {
      const u = s / SEG;
      const u2 = u * u, u3 = u2 * u;
      // Catmull-Rom 矩阵形式 (tension 折算进系数)
      const k = t / 6;
      const x = k * (-u3 + 2 * u2 - u) * p0.x + k * (3 * u3 - 5 * u2 + 2) * p1.x +
                k * (-3 * u3 + 4 * u2 + u) * p2.x + k * (u3 - u2) * p3.x;
      const y = k * (-u3 + 2 * u2 - u) * p0.y + k * (3 * u3 - 5 * u2 + 2) * p1.y +
                k * (-3 * u3 + 4 * u2 + u) * p2.y + k * (u3 - u2) * p3.y;
      ctx.lineTo(x, y);
    }
  }
}

// areaFill(ctx, pts, baseY, color, layers): 折线到 baseY 的渐变面积。
// 内核没有 createLinearGradient, 用 layers 条水平带把闭合面积裁成层,
// 由顶到底 alpha 递减, 叠出"上深下浅"的渐变观感。裁剪走
// Sutherland-Hodgman 的半平面形式 (两条水平线夹一条带), 比逐线段求交
// 可靠: 曲线来回穿越同一水平线时不会漏块。
export function areaFill(ctx, pts, baseY, color, layers) {
  const n = pts.length;
  if (n < 2) return;
  const L = layers === undefined ? 4 : layers;
  const top = Math.min.apply(null, pts.map((p) => p.y));
  const bottom = baseY;
  if (bottom - top < 2) return;
  // 闭合面积多边形: 曲线正走 + 底线回走
  const area = pts.slice();
  area.push({ x: pts[n - 1].x, y: bottom });
  area.push({ x: pts[0].x, y: bottom });
  for (let i = 0; i < L; i++) {
    const y0 = top + ((bottom - top) * i) / L;
    const y1 = top + ((bottom - top) * (i + 1)) / L;
    const band = clipBand(area, y0, y1);
    if (band.length < 3) continue;
    polyPath(ctx, band);
    ctx.fill(alpha(color, 0.34 * (1 - i / L)));
  }
}

// clipBand(pts, y0, y1): 多边形被水平带 [y0, y1] 裁出的部分。
function clipHalfPlane(pts, y, keepAbove) {
  const out = [];
  for (let i = 0; i < pts.length; i++) {
    const cur = pts[i];
    const prev = pts[(i - 1 + pts.length) % pts.length];
    const curIn = keepAbove ? cur.y >= y : cur.y <= y;
    const prevIn = keepAbove ? prev.y >= y : prev.y <= y;
    if (curIn) out.push(cur);
    if (curIn !== prevIn) {
      const t = (y - prev.y) / (cur.y - prev.y);
      out.push({ x: prev.x + (cur.x - prev.x) * t, y: y });
    }
  }
  return out;
}

function clipBand(pts, y0, y1) {
  return clipHalfPlane(clipHalfPlane(pts, y1, false), y0, true);
}

// vGradient(ctx, x, y, w, h, color, fromAlpha, toAlpha, steps): 垂直
// 渐变矩形。steps 默认 10 —— 每层 = h/steps 高的一条 fillRect。
export function vGradient(ctx, x, y, w, h, color, fromAlpha, toAlpha, steps) {
  const N = steps === undefined ? 10 : steps;
  if (N < 1 || h <= 0) return;
  const step = h / N;
  for (let i = 0; i < N; i++) {
    const a = fromAlpha + (toAlpha - fromAlpha) * (i / (N - 1));
    ctx.fillRect(x, y + i * step, w, step + 1, alpha(color, a)); // +1 防层间发丝缝
  }
}

// glowCircle(ctx, cx, cy, r, color, layers): 光晕 —— 由外向内叠半透明圆。
// 用于数据点、环形进度端点、翻牌器高亮等"要发光"的地方。
export function glowCircle(ctx, cx, cy, r, color, layers) {
  const L = layers === undefined ? 3 : layers;
  for (let i = L; i >= 1; i--) {
    ctx.fillCircle(cx, cy, r + i * 2.5, alpha(color, 0.10 * i / L));
  }
  ctx.fillCircle(cx, cy, r, color);
}

// dashedLine(ctx, x1, y1, x2, y2, color, dash): 等间隔虚线。
// 内核 line 是实线, 虚线按 dash 长度切片逐段画 (斜线按弧长参数化)。
export function dashedLine(ctx, x1, y1, x2, y2, color, dash) {
  const d = dash === undefined ? 4 : dash;
  const dx = x2 - x1, dy = y2 - y1;
  const len = Math.sqrt(dx * dx + dy * dy);
  if (len < 1) return;
  const ux = dx / len, uy = dy / len;
  let t = 0;
  while (t < len) {
    const e = Math.min(t + d, len);
    ctx.line(x1 + ux * t, y1 + uy * t, x1 + ux * e, y1 + uy * e, color);
    t = e + d;
  }
}

// grid(ctx, w, h, xStep, yStep, color, dashed): 坐标网格。
export function grid(ctx, w, h, xStep, yStep, color, dashed) {
  if (yStep > 0) {
    for (let y = yStep; y < h; y += yStep) {
      if (dashed) dashedLine(ctx, 0, y, w, y, color);
      else ctx.line(0, y, w, y, color);
    }
  }
  if (xStep > 0) {
    for (let x = xStep; x < w; x += xStep) {
      if (dashed) dashedLine(ctx, x, 0, x, h, color);
      else ctx.line(x, 0, x, h, color);
    }
  }
}

// roundBar(ctx, x, y, w, h, r, color): 圆角柱 (顶端两角圆, 底端方)。
// 内核没有圆角矩形路径, 用"柱身 fillRect + 顶部中段 + 左右两个四分之一
// 圆帽"拼 —— 底端贴基线, 圆角只会在视觉上看到顶端, 与柱状图的常规画法一致。
// **圆帽半径是 rr 不是 w/2**: 曾用 fillCircle(..., w/2) 当"顶端半圆",
// 竖柱 (宽 ~20) 看不出问题, 横条 (宽 ~200) 直接画出一个半径 100 的巨圆
// —— 排行榜的数值条全变成大圆球 (2026-10 实测)。
export function roundBar(ctx, x, y, w, h, r, color) {
  if (h <= 0 || w <= 0) return;
  const rr = Math.min(r, w / 2, h / 2);
  if (rr <= 0.5) { // 太薄: 圆角退化成一个像素的缝, 不如直角
    ctx.fillRect(x, y, w, h, color);
    return;
  }
  ctx.fillRect(x, y + rr, w, h - rr, color);              // 柱身 (圆角以下)
  ctx.fillRect(x + rr, y, w - 2 * rr, rr + 1, color);     // 顶部中段
  ctx.fillCircle(x + rr, y + rr, rr, color);              // 左上圆帽
  ctx.fillCircle(x + w - rr, y + rr, rr, color);          // 右上圆帽
}

// centerText(ctx, text, cx, y, size, color): 以 cx 为水平中心画文本。
// drawText 的 (x, y) 是左上角, 所以 x = cx - 估算宽/2。
export function centerText(ctx, text, cx, y, size, color) {
  ctx.drawText(String(text), cx - textWidth(text, size) / 2, y, size, color);
}

// rightText(ctx, text, rx, y, size, color): 右缘对齐 (图例/数值常用)。
export function rightText(ctx, text, rx, y, size, color) {
  ctx.drawText(String(text), rx - textWidth(text, size), y, size, color);
}

// tickLabels(ctx, labels, x0, x1, y, size, color): X 轴等分刻度标签,
// 首尾贴边、其余居中 (与折线图 X 语义一致)。
export function tickLabels(ctx, labels, x0, x1, y, size, color) {
  const n = labels.length;
  if (n === 0) return;
  const span = x1 - x0;
  for (let i = 0; i < n; i++) {
    const x = x0 + (span * i) / (n - 1 || 1);
    const cx = i === 0 ? x0 : i === n - 1 ? x1 : x;
    centerText(ctx, labels[i], cx, y, size, color);
  }
}

// polyPath(ctx, pts): 闭合多边形路径 (雷达图数据区/网格用)。
export function polyPath(ctx, pts) {
  if (pts.length === 0) return;
  ctx.beginPath();
  ctx.moveTo(pts[0].x, pts[0].y);
  for (let i = 1; i < pts.length; i++) ctx.lineTo(pts[i].x, pts[i].y);
  ctx.closePath();
}

// ---- 颜色工具 ----

// alpha(color, a): 给 #rgb / #rrggbb / rgb() / rgba() 颜色换个 alpha。
// 返回 rgba(...) 字符串; 已带 alpha 的输入按新值覆盖。命名色 (red 等)
// 没有解析表, 原样返回 (调用方这时应传 hex)。
export function alpha(color, a) {
  const c = String(color);
  const av = clamp(a, 0, 1);
  if (c.startsWith("#")) {
    const hex = c.slice(1);
    let r, g, b;
    if (hex.length === 3) {
      r = parseInt(hex[0] + hex[0], 16);
      g = parseInt(hex[1] + hex[1], 16);
      b = parseInt(hex[2] + hex[2], 16);
    } else if (hex.length >= 6) {
      r = parseInt(hex.slice(0, 2), 16);
      g = parseInt(hex.slice(2, 4), 16);
      b = parseInt(hex.slice(4, 6), 16);
    } else {
      return c;
    }
    return "rgba(" + r + "," + g + "," + b + "," + av + ")";
  }
  const m = c.match(/^rgba?\(([^)]+)\)$/);
  if (m) {
    const parts = m[1].split(",");
    const r = Math.round(Number(parts[0]));
    const g = Math.round(Number(parts[1]));
    const b = Math.round(Number(parts[2]));
    return "rgba(" + r + "," + g + "," + b + "," + av + ")";
  }
  return c;
}

// mix(c1, c2, t): 两个 hex 颜色线性混合, t=0 → c1, t=1 → c2。
// 用于"进度沿弧渐变"这类内核渐变缺席的场合 (仪表盘刻度带)。
export function mix(c1, c2, t) {
  const p1 = parseHex(c1), p2 = parseHex(c2);
  const k = clamp(t, 0, 1);
  const r = Math.round(p1[0] + (p2[0] - p1[0]) * k);
  const g = Math.round(p1[1] + (p2[1] - p1[1]) * k);
  const b = Math.round(p1[2] + (p2[2] - p1[2]) * k);
  return "#" + hex2(r) + hex2(g) + hex2(b);
}

function hex2(v) { return (v < 16 ? "0" : "") + v.toString(16); }

function parseHex(c) {
  const h = String(c).replace("#", "");
  if (h.length === 3) {
    return [parseInt(h[0] + h[0], 16), parseInt(h[1] + h[1], 16), parseInt(h[2] + h[2], 16)];
  }
  return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)];
}
