// 时间轴 / 数值轴刻度：给折线图算出「该画哪几条网格线与标签」。
//
// 纯逻辑。时间轴在本监视器里是**采样序号**（运行时没有 Date/performance，
// 拿不到墙钟毫秒 —— 见 README「已知限制」），所以时间刻度就是"第 N 个样本"。
import { niceBounds } from "./scale.js";

// niceStep(rawStep) → 1/2/5 × 10^k 的"整齐步长"
function niceStep(raw) {
  if (!(raw > 0)) return 1;
  const exp = Math.floor(Math.log(raw) / Math.LN10);
  const base = Math.pow(10, exp);
  const f = raw / base;
  let nf;
  if (f <= 1) nf = 1;
  else if (f <= 2) nf = 2;
  else if (f <= 5) nf = 5;
  else nf = 10;
  return nf * base;
}

// valueTicks(lo, hi, count) → [数值, ...]（含首尾附近的整齐刻度）
export function valueTicks(lo, hi, count) {
  const n = count && count >= 2 ? count : 4;
  if (!(hi > lo)) return [lo];
  const step = niceStep((hi - lo) / (n - 1));
  const start = Math.ceil(lo / step) * step;
  const out = [];
  // 上限用 hi + 半个步长，避免浮点误差把末端刻度漏掉。
  for (let v = start; v <= hi + step * 0.5; v = v + step) {
    out.push(Math.round(v * 1000) / 1000);
  }
  return out;
}

// indexTicks(n, count) → [采样序号, ...]（时间轴：等距挑 count 个）
export function indexTicks(n, count) {
  const c = count && count >= 2 ? count : 4;
  if (n <= 0) return [];
  if (n <= c) {
    const all = [];
    for (let i = 0; i < n; i = i + 1) all.push(i);
    return all;
  }
  const out = [];
  for (let k = 0; k < c; k = k + 1) {
    out.push(Math.round((k / (c - 1)) * (n - 1)));
  }
  return out;
}

// 一条曲线在给定画布尺寸下的完整「框架」：量程 + 网格（供 chart 组件直接用）。
export function frameFor(values, opts) {
  const b = niceBounds(values, { minSpan: opts && opts.minSpan });
  return {
    lo: b.lo,
    hi: b.hi,
    span: b.span,
    valueTicks: valueTicks(b.lo, b.hi, 4),
    indexTicks: indexTicks(values.length, 4),
  };
}
