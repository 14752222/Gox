// ===== gx-datav 热力图 =====
//
// 网格色块: 值 → 颜色 (colorFrom 冷 → colorTo 热, 走 mix 线性插值)。
// 经典用法是 7 天 × 24 小时的访问热力, 但组件不限定维度 —— 给二维数组
// 就行, 行列标签都可选。
//
// props:
//   data       二维数组 number[][] (外层行 × 内层列; 缺省 [])
//   xLabels    列标签 (可短于列数, 抽稀显示)
//   yLabels    行标签 (可短于行数)
//   width      画布宽 (缺省 "100%")
//   cellH      格高 (缺省 18)
//   cellGap    格间距 (缺省 2)
//   colorFrom  低值色 (缺省深蓝)
//   colorTo    高值色 (缺省强调色)
//   labelW     行标签列宽 (缺省 44)
//   showValue  格内数值 (缺省 false; 格够大才放得下)
//
// 取色走 draw.js 的 mix (hex 线性混合) —— 不引渐变色带, 两个端点 +
// 一个 t 就够热力图用; 要多色带 (绿→黄→红) 传 colorFrom/colorTo 即可,
// 或外层自行映射成预染色的 [r,g,b] ... 不, 保持简单: 两色插值。
//
// **mix 在 draw.js 不在 theme.js**: 曾经从 theme.js 导入, 缺名导入不报错
// 只是静默 undefined, onDraw 画第一行标签时调用才抛 —— 整块热力图只剩
// 一行行标签 (2026-10-15 实测, 画廊目检才发现)。
import { h } from "gx/gfx";
import { textWidth } from "./theme.js";
import { mix } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function HeatMap(props) {
  const cellH = props.cellH || 18;
  const cellGap = props.cellGap === undefined ? 2 : props.cellGap;
  const colorFrom = props.colorFrom || "#0c2246";
  const colorTo = props.colorTo || "#22e6ff";
  const labelW = props.labelW === undefined ? 44 : props.labelW;
  const showValue = props.showValue === true;
  const footH = 16; // 列标签行

  // 高度: 没显式给就按首帧数据行数算 (数据随后变长时 onDraw 里裁剪)
  const n0 = (val(props.data) || []).length;
  const height = props.height || Math.max(1, n0) * cellH + footH;

  return h("canvas", {
    width: props.width || "100%",
    height: height,
    onDraw: (ctx) => {
      const data = val(props.data) || [];
      const xLabels = val(props.xLabels) || [];
      const yLabels = val(props.yLabels) || [];
      const rows = data.length;
      if (rows === 0) return;
      const cols = Math.max.apply(null, data.map((r) => r.length));
      if (cols <= 0) return;

      const w = ctx.width;
      const plotX = labelW, plotY = 0;
      const plotW = w - labelW - 4;
      const cw = plotW / cols;
      // 底: 行标签 + 网格区
      const gridH = ctx.height - footH;

      // 值域
      let vMin = Infinity, vMax = -Infinity;
      for (const row of data) {
        for (const v of row) {
          const nv = Number(v);
          if (nv < vMin) vMin = nv;
          if (nv > vMax) vMax = nv;
        }
      }
      if (!isFinite(vMin)) { vMin = 0; vMax = 1; }
      const span = vMax - vMin || 1;

      for (let r = 0; r < rows; r++) {
        // 行标签 (右对齐到网格左缘)
        if (yLabels[r] !== undefined) {
          const t = String(yLabels[r]);
          const fs = 10;
          ctx.drawText(t, labelW - 6 - textWidth(t, fs), plotY + r * cellH + cellH / 2 - fs / 2, fs, "#7e93bd");
        }
        for (let c = 0; c < cols; c++) {
          const v = Number(data[r][c]);
          const t = isFinite(v) ? (v - vMin) / span : 0;
          const x = plotX + c * cw + cellGap / 2;
          const y = plotY + r * cellH + cellGap / 2;
          const cwid = cw - cellGap;
          if (y + cellH - cellGap > gridH) continue; // 视口裁剪 (数据变长)
          if (showValue && cwid >= 22) {
            const fs = 9;
            const t2 = String(Math.round(v));
            ctx.fillRect(x, y, cwid, cellH - cellGap, mix(colorFrom, colorTo, t));
            ctx.drawText(t2, x + cwid / 2 - textWidth(t2, fs) / 2,
              y + (cellH - cellGap) / 2 - fs / 2, fs, t > 0.55 ? "#0a1830" : "#a9bcdf");
          } else {
            ctx.fillRect(x, y, cwid, cellH - cellGap, mix(colorFrom, colorTo, t));
          }
        }
      }

      // 列标签: 过密抽稀到 ~10 个
      if (xLabels.length) {
        const cnt = Math.min(xLabels.length, 10);
        const step = (xLabels.length - 1) / (cnt - 1 || 1);
        const fs = 10;
        for (let k = 0; k < cnt; k++) {
          const idx = Math.round(k * step);
          const t = String(xLabels[idx]);
          const cx = plotX + (idx + 0.5) * cw;
          ctx.drawText(t, cx - textWidth(t, fs) / 2, gridH + 3, fs, "#7e93bd");
        }
      }
    },
  });
}
