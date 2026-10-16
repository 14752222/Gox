// ===== gx-datav 柱状图 =====
//
// 垂直柱状 (横柱由调用方换数据即可, 不做第二套布局): 顶端圆角柱 +
// 顶部数值 + X 标签 + 基线。多序列时并列 (grouped), 单序列时按色板
// 逐柱取色 (大屏常见的"彩虹柱")。
//
// props:
//   series   [{name, color?, data: [number]}]  多序列并列; 单序列可简写
//                                             成 data 数字数组 (自动包
//                                             成一条, 逐柱取色板)
//   labels   X 轴标签
//   height   画布高 (缺省 200)
//   barRatio 柱宽占槽宽比 (缺省 0.55)
//   showValue 柱顶数值 (缺省 true)
//   yMax     Y 上限 (缺省自动 niceCeil)
//   yGrid    Y 网格数 (缺省 4)
//   legend   图例 (多序列时默认开)
import { h } from "gx/gfx";
import { seriesColors, textWidth, clamp, niceCeil } from "./theme.js";
import { alpha, roundBar } from "./draw.js";

function centerAt(ctx, t, cx, y, size, color) {
  ctx.drawText(String(t), cx - textWidth(t, size) / 2, y, size, color);
}

// val(v): 同 line.js —— 数据 prop 支持 signal getter, onDraw 时求值。
function val(v) { return typeof v === "function" ? v() : v; }

export function BarChart(props) {
  let series = val(props.series) || [];
  // 简写: data: [1,2,3] → 一条"逐柱取色"的序列
  if (val(props.data) && !series.length) {
    series = [{ name: props.name || "", data: val(props.data), rainbow: true }];
  }
  const labels = val(props.labels) || [];
  const height = props.height || 200;
  const barRatio = clamp(props.barRatio === undefined ? 0.55 : props.barRatio, 0.1, 1);
  const showValue = props.showValue !== false;
  const yGrid = props.yGrid === undefined ? 4 : props.yGrid;
  const legend = props.legend === undefined ? series.length > 1 : props.legend;

  const PAD = { l: 36, r: 14, t: 18, b: 20 };
  const legendH = legend && series.length > 1 ? 18 : 0;

  let dataMax = 0;
  for (const s of series) for (const v of s.data) if (v > dataMax) dataMax = v;
  const yMax = props.yMax === undefined ? niceCeil(Math.max(dataMax * 1.15, 1)) : props.yMax;

  return h("canvas", {
    width: "100%",
    height: height + legendH,
    onDraw: (ctx) => {
      // 每次重绘取最新数据 (简写 data 也走同一条求值路)
      let series = val(props.series) || [];
      if (val(props.data) && !series.length) {
        series = [{ name: props.name || "", data: val(props.data), rainbow: true }];
      }
      const labels = val(props.labels) || [];
      const w = ctx.width;
      const hh = ctx.height - legendH;
      const x0 = PAD.l, x1 = w - PAD.r;
      const y0 = PAD.t, y1 = hh - PAD.b;
      const n = Math.max.apply(null, series.map((s) => s.data.length).concat([1]));
      const sy = (v) => y1 - ((y1 - y0) * v) / Math.max(1e-9, yMax);

      // 网格 + Y 刻度
      for (let i = 0; i <= yGrid; i++) {
        const y = Math.round(y0 + ((y1 - y0) * i) / yGrid);
        ctx.line(x0, y, x1, y, i === 0 ? "rgba(120, 155, 220, 0.35)" : "rgba(90, 130, 200, 0.14)");
        const txt = String(Math.round(yMax - (yMax * i) / yGrid));
        ctx.drawText(txt, x0 - textWidth(txt, 10) - 4, y - 5, 10, "#7e93bd");
      }

      const slot = (x1 - x0) / n;               // 每根柱的槽宽
      const groupW = slot * barRatio;
      const per = groupW / series.length;       // 多序列并列时每根宽

      for (let si = 0; si < series.length; si++) {
        const s = series[si];
        for (let i = 0; i < s.data.length; i++) {
          const v = s.data[i];
          const bx = x0 + slot * i + (slot - groupW) / 2 + per * si;
          const by = sy(v);
          const bh = y1 - by;
          if (bh <= 0) continue;
          const c = s.rainbow ? seriesColors[i % seriesColors.length]
                              : (s.color || seriesColors[si % seriesColors.length]);
          roundBar(ctx, bx, by, Math.max(2, per - 3), bh, Math.min(3, per / 2), c);
          // 柱底亮线 (贴基线, 让柱"立"在轴上)
          ctx.fillRect(bx, y1 - 1.5, Math.max(2, per - 3), 1.5, alpha(c, 0.8));
          if (showValue && per >= 18) { // 18px 才放得下两位数的标签 (曾用 22, 单序列 12 柱时刚好卡掉)
            const txt = String(Math.round(v));
            ctx.drawText(txt, bx + (per - 3) / 2 - textWidth(txt, 10) / 2, by - 13, 10, c);
          }
        }
      }

      // X 标签
      if (labels.length) {
        for (let i = 0; i < Math.min(labels.length, n); i++) {
          const cx = x0 + slot * i + slot / 2;
          centerAt(ctx, labels[i], cx, y1 + 6, 10, "#7e93bd");
        }
      }

      // 图例
      if (legendH) {
        let lx = x0;
        const ly = hh + 4;
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
