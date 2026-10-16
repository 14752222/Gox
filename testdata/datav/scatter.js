// ===== gx-datav 气泡散点图 =====
//
// 散点 + 气泡 (第三维映射到半径): 网格 + 双向刻度 + 多序列。用于
// "GMV × 复购率 × 用户数"这类三维关系一屏看完的场景。
//
// props:
//   series    [{name, color?, data: [{x, y, r?}]}]  r 缺省取中档
//   xLabel    X 轴名 (缺省无)
//   yLabel    Y 轴名 (缺省无)
//   width     画布宽 (缺省 "100%")
//   height    画布高 (缺省 240)
//   sizeRange [最小, 最大] 气泡直径 px (缺省 [6, 26])
//   grid      Y 网格数 (缺省 4)
//   legend    图例 (缺省 true)
//   valueFmt  刻度格式化 (缺省整数)
import { h } from "gx/gfx";
import { seriesColors, textWidth, niceCeil } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function ScatterChart(props) {
  const height = props.height || 240;
  const grid = props.yGrid === undefined ? 4 : props.yGrid;
  const legend = props.legend !== false;
  const sizeRange = props.sizeRange || [6, 26];
  const valueFmt = props.valueFmt || ((v) => String(Math.round(v)));

  const PAD = { l: 40, r: 16, t: 14, b: 26 };
  const legendH = legend && (val(props.series) || []).length ? 16 : 0;

  return h("canvas", {
    width: props.width || "100%",
    height: height + legendH,
    onDraw: (ctx) => {
      const series = val(props.series) || [];
      const w = ctx.width;
      const hh = ctx.height - legendH;
      const x0 = PAD.l, x1 = w - PAD.r;
      const y0 = PAD.t, y1 = hh - PAD.b;

      // 轴域: 各序列 x/y 的极值, niceCeil 取整
      let xMax = 0, yMax = 0, rMax = 1;
      for (const s of series) {
        for (const p of s.data) {
          if (Number(p.x) > xMax) xMax = Number(p.x);
          if (Number(p.y) > yMax) yMax = Number(p.y);
          if (Number(p.r) > rMax) rMax = Number(p.r);
        }
      }
      xMax = niceCeil(Math.max(xMax * 1.1, 1));
      yMax = niceCeil(Math.max(yMax * 1.1, 1));
      const sx = (v) => x0 + ((x1 - x0) * v) / xMax;
      const sy = (v) => y1 - ((y1 - y0) * v) / yMax;
      const sr = (r) => {
        const t = rMax > 0 ? (Number(r) || 0) / rMax : 0.5;
        return (sizeRange[0] + (sizeRange[1] - sizeRange[0]) * t) / 2;
      };

      // 网格 + 双向刻度
      for (let i = 0; i <= grid; i++) {
        const y = Math.round(y0 + ((y1 - y0) * i) / grid);
        ctx.line(x0, y, x1, y, i === grid ? "rgba(120, 155, 220, 0.35)" : "rgba(90, 130, 200, 0.14)");
        const txt = valueFmt(yMax - (yMax * i) / grid);
        ctx.drawText(txt, x0 - textWidth(txt, 10) - 4, y - 5, 10, "#7e93bd");
      }
      for (let i = 0; i <= 4; i++) {
        const x = Math.round(x0 + ((x1 - x0) * i) / 4);
        const txt = valueFmt((xMax * i) / 4);
        ctx.drawText(txt, x - textWidth(txt, 10) / 2, y1 + 8, 10, "#7e93bd");
      }
      if (props.xLabel) {
        const t = String(props.xLabel);
        ctx.drawText(t, x1 - textWidth(t, 10), y1 + 8, 10, "#9fc2ee");
      }
      if (props.yLabel) {
        const t = String(props.yLabel);
        ctx.drawText(t, x0 - textWidth(t, 10) - 4, y0 - 10, 10, "#9fc2ee");
      }

      // 气泡: 半透明填充 + 实心描边 + 微光晕 (大屏上"点"要有存在感)
      for (let si = 0; si < series.length; si++) {
        const s = series[si];
        const c = s.color || seriesColors[si % seriesColors.length];
        for (const p of s.data) {
          const bx = sx(Number(p.x) || 0), by = sy(Number(p.y) || 0), br = sr(p.r);
          ctx.fillCircle(bx, by, br, alpha(c, 0.35));
          ctx.strokeCircle(bx, by, br, c);
          ctx.fillCircle(bx, by, Math.max(1.5, br * 0.3), alpha(c, 0.9));
        }
      }

      // 图例 (带一个样例气泡)
      if (legendH) {
        let lx = x0;
        const ly = hh + 4;
        for (let si = 0; si < series.length; si++) {
          const s = series[si];
          const c = s.color || seriesColors[si % seriesColors.length];
          ctx.fillCircle(lx + 5, ly + 4, 5, alpha(c, 0.4));
          ctx.strokeCircle(lx + 5, ly + 4, 5, c);
          ctx.drawText(s.name, lx + 14, ly, 11, "#a9bcdf");
          lx += 14 + textWidth(s.name, 11) + 18;
        }
      }
    },
  });
}
