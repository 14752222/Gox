// ===== gx-datav 折线图 =====
//
// 多序列折线: 网格 + Y 刻度 + X 标签 + Catmull-Rom 平滑曲线 + 渐变
// 面积 + 数据点光晕 + 末值标签 + 图例。所有常见诉求一次给齐, 单项
// 关不掉的是"重" (网格), 其余都是 prop。
//
// props:
//   series   [{name, color?, data: [number]}]  —— 多条线按索引取色板
//   labels   X 轴刻度 (与 data 等长)
//   height   画布高 (缺省 200), 宽由容器给 ("100%")
//   smooth   平滑 (缺省 true); false = 原折线
//   area     渐变面积 (缺省 true, 仅第一条线)
//   yMax     Y 上限 (缺省自动: 数据最大值的 1.15 倍, 向上取整到刻度)
//   yMin     Y 下限 (缺省 0)
//   yGrid    Y 网格线数 (缺省 4)
//   legend   图例 (缺省 true)
//   valueFmt 末值标签格式化 (缺省 trimFloat 1 位)
import { h } from "gx/gfx";
import { palette, seriesColors, textWidth, niceCeil } from "./theme.js";
import { alpha, smoothPath, areaFill, glowCircle } from "./draw.js";

// val(v): 数据 prop 允许直接传数组, 也允许传 signal getter —— 后者每次
// onDraw 求值, 读取的 signal 自动成为绘制依赖, 数据一变就重绘。
function val(v) { return typeof v === "function" ? v() : v; }

export function LineChart(props) {
  const series = val(props.series) || [];
  const labels = val(props.labels) || [];
  const height = props.height || 200;
  const smooth = props.smooth !== false;
  const area = props.area !== false;
  const yGrid = props.yGrid === undefined ? 4 : props.yGrid;
  const legend = props.legend !== false;
  const yMin = props.yMin === undefined ? 0 : props.yMin;
  const valueFmt = props.valueFmt || ((v) => trim1(v));

  // 布局边距: 左给 Y 刻度, 下给 X 标签, 上给末值气泡
  const PAD = { l: 36, r: 14, t: 16, b: 20 };
  const legendH = legend && series.length ? 18 : 0;

  // Y 上限: 数据峰值的 1.15 倍向上取整到"好看"的刻度 (1/2/5 × 10^k)
  let dataMax = yMin;
  for (const s of series) for (const v of s.data) if (v > dataMax) dataMax = v;
  const yMax = props.yMax === undefined ? niceCeil(Math.max(dataMax * 1.15, yMin + 1)) : props.yMax;

  return h("canvas", {
    width: "100%",
    height: height + legendH,
    onDraw: (ctx) => {
      // 每次重绘都取最新数据 (props 可以是 signal getter)
      const series = val(props.series) || [];
      const labels = val(props.labels) || [];
      const w = ctx.width;
      const hh = ctx.height - legendH;
      const x0 = PAD.l, x1 = w - PAD.r;
      const y0 = PAD.t, y1 = hh - PAD.b;
      const n = maxLen(series);
      const sx = (i) => x0 + ((x1 - x0) * i) / Math.max(1, n - 1);
      const sy = (v) => y1 - ((y1 - y0) * (v - yMin)) / Math.max(1e-9, yMax - yMin);

      // 1. 网格 + Y 刻度
      for (let i = 0; i <= yGrid; i++) {
        const y = Math.round(y0 + ((y1 - y0) * i) / yGrid);
        ctx.line(x0, y, x1, y, i === 0 ? "rgba(120, 155, 220, 0.35)" : "rgba(90, 130, 200, 0.14)");
        const val = yMax - ((yMax - yMin) * i) / yGrid;
        const txt = valueFmt(val);
        ctx.drawText(txt, x0 - textWidth(txt, 10) - 4, y - 5, 10, "#7e93bd");
      }

      // 2. X 标签 (首尾贴边)
      if (labels.length) {
        const cnt = Math.min(labels.length, 8); // 过密时均匀抽稀
        const step = (labels.length - 1) / (cnt - 1 || 1);
        for (let k = 0; k < cnt; k++) {
          const idx = Math.round(k * step);
          centerAt(ctx, labels[idx], sx(idx), y1 + 6, 10, "#7e93bd");
        }
      }

      // 3. 各序列
      for (let si = 0; si < series.length; si++) {
        const s = series[si];
        const c = s.color || seriesColors[si % seriesColors.length];
        const pts = s.data.map((v, i) => ({ x: sx(i), y: sy(v) }));
        if (pts.length === 0) continue;

        if (area && si === 0 && pts.length > 1) {
          areaFill(ctx, pts, y1, c, 4);
        }
        ctx.beginPath();
        if (smooth) smoothPath(ctx, pts, 1);
        else { ctx.moveTo(pts[0].x, pts[0].y); for (const p of pts) ctx.lineTo(p.x, p.y); }
        ctx.lineWidth = 2;
        ctx.stroke(c);
        ctx.lineWidth = 1;

        // 数据点: 末点亮 + 光晕, 其余小暗点 (点太密会糊, 隔点画)
        const dotStep = pts.length > 24 ? 2 : 1;
        for (let i = 0; i < pts.length; i += dotStep) {
          if (i === pts.length - 1) continue;
          ctx.fillCircle(pts[i].x, pts[i].y, 2, alpha(c, 0.5));
        }
        const last = pts[pts.length - 1];
        glowCircle(ctx, last.x, last.y, 3, c);
        // 末值标签
        const txt = valueFmt(s.data[s.data.length - 1]);
        ctx.drawText(txt, Math.min(last.x + 6, x1 - textWidth(txt, 11)), last.y - 14, 11, c);
      }

      // 4. 图例
      if (legend && series.length) {
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

function maxLen(series) {
  let m = 0;
  for (const s of series) if (s.data.length > m) m = s.data.length;
  return m;
}

function trim1(v) { return String(Math.round(v * 10) / 10); }

function centerAt(ctx, t, cx, y, size, color) {
  ctx.drawText(String(t), cx - textWidth(t, size) / 2, y, size, color);
}
