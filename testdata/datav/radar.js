// ===== gx-datav 雷达图 =====
//
// 多维能力对比: levels 层同心多边形网格 + 轴线 + 多序列数据多边形
// (半透明填充 + 描边 + 顶点)。标签贴在轴末端外侧, 按所在象限自动
// 对齐 (右半区左对齐、左半区右对齐), 不会压到图上。
//
// props:
//   axes    轴名称数组 (3~8 条)
//   series  [{name, color?, values: [number]}]  各轴取值 (0~max)
//   size    画布边长 (缺省 220)
//   max     轴最大值 (缺省 100)
//   levels  网格层数 (缺省 4)
//   legend   图例 (缺省 true)
import { h } from "gx/gfx";
import { seriesColors, clamp, textWidth, polar } from "./theme.js";
import { alpha, polyPath } from "./draw.js";

// val(v): 数据 prop 支持 signal getter (onDraw 时求值 → 自动订阅)。
function val(v) { return typeof v === "function" ? v() : v; }

export function RadarChart(props) {
  const axes = val(props.axes) || [];
  const series = val(props.series) || [];
  const size = props.size || 220;
  const max = props.max === undefined ? 100 : props.max;
  const levels = props.levels === undefined ? 4 : props.levels;
  const legend = props.legend !== false;

  const N = axes.length;
  const legendH = legend && series.length ? 16 : 0;
  const cx = size / 2;
  const cy = size / 2;
  const r = size / 2 - Math.max(24, Math.round(size * 0.14)); // 外圈给标签留位

  // 第 i 条轴的角度: 从正上方起, 顺时针均分
  const angleOf = (i) => -Math.PI / 2 + (Math.PI * 2 * i) / N;
  const pt = (i, rr) => polar(cx, cy, rr, angleOf(i));

  return h("canvas", {
    width: size,
    height: size + legendH,
    onDraw: (ctx) => {
      const axes = val(props.axes) || [];
      const series = val(props.series) || [];
      const N = axes.length;
      // 1. 网格: levels 层同心多边形 (最外层实线, 其余虚线感弱化)
      for (let l = levels; l >= 1; l--) {
        const rr = (r * l) / levels;
        const pts = [];
        for (let i = 0; i < N; i++) pts.push(pt(i, rr));
        polyPath(ctx, pts);
        ctx.lineWidth = l === levels ? 1.5 : 1;
        ctx.stroke(l === levels ? "rgba(120, 155, 220, 0.4)" : "rgba(90, 130, 200, 0.16)");
        ctx.lineWidth = 1;
      }

      // 2. 轴线
      for (let i = 0; i < N; i++) {
        const p = pt(i, r);
        ctx.line(cx, cy, p.x, p.y, "rgba(120, 155, 220, 0.25)");
      }

      // 3. 轴标签: 按象限对齐
      for (let i = 0; i < N; i++) {
        const a = angleOf(i);
        const lx = cx + (r + 10) * Math.cos(a);
        const ly = cy + (r + 10) * Math.sin(a);
        const fs = 11;
        const tw = textWidth(axes[i], fs);
        const right = Math.cos(a) > 0.3;      // 右半区: 文字放点左侧 (左对齐)
        const left = Math.cos(a) < -0.3;      // 左半区: 右对齐
        const tx = right ? lx : left ? lx - tw : lx - tw / 2;
        ctx.drawText(axes[i], tx, ly - fs / 2, fs, "#a9bcdf");
      }

      // 4. 数据多边形
      for (let si = 0; si < series.length; si++) {
        const s = series[si];
        const c = s.color || seriesColors[si % seriesColors.length];
        const pts = [];
        for (let i = 0; i < N; i++) {
          const v = clamp((Number(s.values[i]) || 0) / max, 0, 1);
          pts.push(pt(i, r * v));
        }
        polyPath(ctx, pts);
        ctx.fill(alpha(c, 0.18));
        ctx.lineWidth = 2;
        ctx.stroke(c);
        ctx.lineWidth = 1;
        for (const p of pts) {
          ctx.fillCircle(p.x, p.y, 2.5, c);
        }
      }

      // 5. 图例
      if (legendH) {
        let lx = 8;
        const ly = size + 2;
        for (let si = 0; si < series.length; si++) {
          const s = series[si];
          const c = s.color || seriesColors[si % seriesColors.length];
          ctx.fillRect(lx, ly + 3, 8, 8, c);
          ctx.drawText(s.name, lx + 12, ly, 11, "#a9bcdf");
          lx += 12 + textWidth(s.name, 11) + 14;
        }
      }
    },
  });
}
