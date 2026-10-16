// ===== gx-datav 柱线组合图 (双 Y 轴) =====
//
// 左轴柱 + 右轴线, 一套 x 标签两套量纲 (销量 × 转化率 的标配形态)。
// 柱走圆角柱, 线走 Catmull-Rom 平滑 + 末值光晕点, 两侧刻度各自
// niceCeil。比 LineChart 只多一根右轴 —— 数据 prop 的形态与折线图
// 完全一致, 从 line.js 迁移零成本。
//
// props:
//   bars    [{name, color?, data: [number]}]  左轴 (柱)
//   lines   [{name, color?, data: [number]}]  右轴 (线)
//   labels  X 轴刻度 (与 data 等长)
//   height  画布高 (缺省 240)
//   smooth  线平滑 (缺省 true)
//   barRatio 柱宽占槽宽比 (缺省 0.5)
//   yGrid   Y 网格数 (缺省 4)
//   width   画布宽 (缺省 "100%")
import { h } from "gx/gfx";
import { seriesColors, textWidth, niceCeil } from "./theme.js";
import { smoothPath, roundBar, glowCircle } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function ComboChart(props) {
  const height = props.height || 240;
  const smooth = props.smooth !== false;
  const barRatio = Math.max(0.1, Math.min(1, props.barRatio === undefined ? 0.5 : props.barRatio));
  const yGrid = props.yGrid === undefined ? 4 : props.yGrid;

  const PAD = { l: 40, r: 44, t: 14, b: 20 };
  const legendH = 18;

  return h("canvas", {
    width: props.width || "100%",
    height: height + legendH,
    onDraw: (ctx) => {
      const bars = val(props.bars) || [];
      const lines = val(props.lines) || [];
      const labels = val(props.labels) || [];
      const w = ctx.width;
      const hh = ctx.height - legendH;
      const x0 = PAD.l, x1 = w - PAD.r;
      const y0 = PAD.t, y1 = hh - PAD.b;

      let n = 1;
      for (const s of bars) n = Math.max(n, s.data.length);
      for (const s of lines) n = Math.max(n, s.data.length);

      // 两套轴域各自 niceCeil
      let barMax = 0, lineMax = 0;
      for (const s of bars) for (const v of s.data) if (Number(v) > barMax) barMax = Number(v);
      for (const s of lines) for (const v of s.data) if (Number(v) > lineMax) lineMax = Number(v);
      barMax = niceCeil(Math.max(barMax * 1.15, 1));
      lineMax = niceCeil(Math.max(lineMax * 1.15, 1));

      const sx = (i) => x0 + ((x1 - x0) * i) / Math.max(1, n - 1);
      const syBar = (v) => y1 - ((y1 - y0) * v) / barMax;
      const syLine = (v) => y1 - ((y1 - y0) * v) / lineMax;

      // 网格 + 左刻度 (柱) + 右刻度 (线)
      for (let i = 0; i <= yGrid; i++) {
        const y = Math.round(y0 + ((y1 - y0) * i) / yGrid);
        ctx.line(x0, y, x1, y, i === yGrid ? "rgba(120, 155, 220, 0.35)" : "rgba(90, 130, 200, 0.14)");
        const lt = String(Math.round(barMax - (barMax * i) / yGrid));
        ctx.drawText(lt, x0 - textWidth(lt, 10) - 4, y - 5, 10, "#7e93bd");
        const rt = String(Math.round(lineMax - (lineMax * i) / yGrid));
        ctx.drawText(rt, x1 + 6, y - 5, 10, "#7e93bd");
      }

      // X 标签 (抽稀到 ~8 个)
      if (labels.length) {
        const cnt = Math.min(labels.length, 8);
        const step = (labels.length - 1) / (cnt - 1 || 1);
        for (let k = 0; k < cnt; k++) {
          const idx = Math.round(k * step);
          const t = String(labels[idx]);
          ctx.drawText(t, sx(idx) - textWidth(t, 10) / 2, y1 + 6, 10, "#7e93bd");
        }
      }

      // 柱 (多序列并列)
      const slot = (x1 - x0) / Math.max(1, n);
      const groupW = slot * barRatio;
      const per = groupW / Math.max(1, bars.length);
      for (let si = 0; si < bars.length; si++) {
        const s = bars[si];
        const c = s.color || seriesColors[si % seriesColors.length];
        for (let i = 0; i < s.data.length; i++) {
          const bx = x0 + slot * i + (slot - groupW) / 2 + per * si;
          const by = syBar(Number(s.data[i]) || 0);
          const bh = y1 - by;
          if (bh <= 0) continue;
          roundBar(ctx, bx, by, Math.max(2, per - 3), bh, Math.min(3, per / 2), c);
        }
      }

      // 线 (多序列)
      for (let si = 0; si < lines.length; si++) {
        const s = lines[si];
        const c = s.color || seriesColors[(bars.length + si) % seriesColors.length];
        const pts = s.data.map((v, i) => ({ x: sx(i), y: syLine(Number(v) || 0) }));
        if (pts.length === 0) continue;
        ctx.beginPath();
        if (smooth) smoothPath(ctx, pts, 1);
        else { ctx.moveTo(pts[0].x, pts[0].y); for (const p of pts) ctx.lineTo(p.x, p.y); }
        ctx.lineWidth = 2;
        ctx.stroke(c);
        ctx.lineWidth = 1;
        const last = pts[pts.length - 1];
        glowCircle(ctx, last.x, last.y, 3, c);
        const txt = String(Math.round(Number(s.data[s.data.length - 1]) || 0));
        ctx.drawText(txt, Math.min(last.x + 6, x1 - textWidth(txt, 11)), last.y - 14, 11, c);
      }

      // 图例: 柱方块 + 线线段
      let lx = x0;
      const ly = hh + 4;
      for (const s of bars) {
        const c = s.color || seriesColors[0];
        ctx.fillRect(lx, ly + 2, 8, 8, c);
        ctx.drawText(s.name, lx + 12, ly, 11, "#a9bcdf");
        lx += 12 + textWidth(s.name, 11) + 14;
      }
      for (let si = 0; si < lines.length; si++) {
        const s = lines[si];
        const c = s.color || seriesColors[(bars.length + si) % seriesColors.length];
        ctx.fillRect(lx, ly + 5, 8, 2, c);
        ctx.drawText(s.name, lx + 12, ly, 11, "#a9bcdf");
        lx += 12 + textWidth(s.name, 11) + 14;
      }
    },
  });
}
