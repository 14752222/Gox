// ===== gx-datav 迷你趋势线 =====
//
// 无轴无刻度的小折线: 只表达"形状与方向", 给 KPI 卡 / 表格单元格 /
// 侧边栏嵌用。画布默认贴内容尺寸, 调用方给 width/height 即可。
//
// props:
//   data    [number] 或 signal getter
//   width   画布宽 (缺省 120)
//   height  画布高 (缺省 36)
//   color   线色 (缺省强调色)
//   fill    线下渐变面积 (缺省 true, 2 层)
//   dot     末点光晕 (缺省 true)
//   smooth  平滑 (缺省 true)
import { h } from "gx/gfx";
import { alpha, smoothPath } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function Sparkline(props) {
  const width = props.width || 120;
  const height = props.height || 36;
  const color = props.color || "#22e6ff";
  const fill = props.fill !== false;
  const dot = props.dot !== false;
  const smooth = props.smooth !== false;
  const pad = 3;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      // 画布尺寸以 ctx 为准: width/height prop 可能是百分比字符串,
      // 拿它算插值坐标会得 NaN (整条线消失)。依赖收集那一遍可能是 0,
      // 坐标全落 0 也不抛。
      const width = ctx.width;
      const height = ctx.height;
      const data = (val(props.data) || []).map(Number);
      const n = data.length;
      if (n < 2) return;
      let lo = Infinity, hi = -Infinity;
      for (const v of data) { if (v < lo) lo = v; if (v > hi) hi = v; }
      const span = hi - lo || 1;
      const pts = data.map((v, i) => ({
        x: pad + ((width - pad * 2) * i) / (n - 1),
        y: height - pad - ((height - pad * 2) * (v - lo)) / span,
      }));

      if (fill) {
        // 两层面积: 上浓下淡, 近似渐变 (内核无 createLinearGradient)
        ctx.beginPath();
        ctx.moveTo(pts[0].x, height);
        if (smooth) smoothPath(ctx, pts, 1); else for (const p of pts) ctx.lineTo(p.x, p.y);
        ctx.lineTo(pts[n - 1].x, height);
        ctx.closePath();
        ctx.fill(alpha(color, 0.10));
        ctx.beginPath();
        ctx.moveTo(pts[0].x, height - (height - pts[0].y) * 0.45);
        if (smooth) smoothPath(ctx, pts, 1); else for (const p of pts) ctx.lineTo(p.x, p.y);
        ctx.lineTo(pts[n - 1].x, height - (height - pts[n - 1].y) * 0.45);
        ctx.closePath();
        ctx.fill(alpha(color, 0.16));
      }

      ctx.beginPath();
      if (smooth) smoothPath(ctx, pts, 1);
      else { ctx.moveTo(pts[0].x, pts[0].y); for (const p of pts) ctx.lineTo(p.x, p.y); }
      ctx.lineWidth = 2;
      ctx.stroke(color);
      ctx.lineWidth = 1;

      if (dot) {
        const last = pts[n - 1];
        ctx.fillCircle(last.x, last.y, 3.5, alpha(color, 0.35));
        ctx.fillCircle(last.x, last.y, 1.8, color);
      }
    },
  });
}
