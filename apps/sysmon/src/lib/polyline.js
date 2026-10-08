// 折线点集生成：把采样序列转成画布上的整数像素点，再切成线段。
//
// 纯逻辑。canvas 的 ctx.line(x1,y1,x2,y2,color) 只画单像素直线、没有路径对象，
// 所以折线必须由应用自己切成一段段短线 —— 这里就是那段切分。
import { niceBounds, mapX, mapY } from "./scale.js";

// buildPoints(values, opts) → [{x,y}, ...]
//
// opts: { width, height, lo, hi, padLeft, padRight, padTop, padBottom }
// lo/hi 省略时对 values 自适应（niceBounds）。
export function buildPoints(values, opts) {
  const n = values.length;
  const bounds =
    opts && typeof opts.lo === "number" && typeof opts.hi === "number"
      ? { lo: opts.lo, hi: opts.hi }
      : niceBounds(values, { minSpan: opts && opts.minSpan });
  const pts = [];
  for (let i = 0; i < n; i = i + 1) {
    pts.push({
      x: mapX(i, n, opts.width, opts.padLeft, opts.padRight),
      y: mapY(values[i], bounds.lo, bounds.hi, opts.height, opts.padTop, opts.padBottom),
    });
  }
  return pts;
}

// segments(points) → [[p0,p1], [p1,p2], ...]
export function segments(points) {
  const out = [];
  for (let i = 1; i < points.length; i = i + 1) {
    out.push([points[i - 1], points[i]]);
  }
  return out;
}

// 便捷入口：直接给出线段（canvas onDraw 里一步到位）。
export function buildSegments(values, opts) {
  return segments(buildPoints(values, opts));
}

export { niceBounds, mapX, mapY };
