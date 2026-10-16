// ===== gx-datav 玫瑰图 (南丁格尔) =====
//
// 扇区角度等分、半径按值 —— 与饼图正好互补: 饼图看"占比", 玫瑰图看
// "量大者突出" (值差一倍, 面积差四倍, 视觉冲击强)。数据少 (3~8 项)
// 时最好看。
//
// props:
//   data       [{name, value, color?}]
//   size       画布边长 (缺省 220)
//   inner      中心孔径比 (缺省 0.25; 0 = 实心)
//   gap        扇区间隙 deg (缺省 1.5)
//   legend     图例 (缺省 true)
//   valueFmt   图例数值格式化 (缺省 fmtInt)
//   startAngle 起始角 (缺省 -90°, 12 点起)
import { h } from "gx/gfx";
import { seriesColors, fmtInt, textWidth, clamp } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function RoseChart(props) {
  const size = props.size || 220;
  const inner = clamp(props.inner === undefined ? 0.25 : props.inner, 0, 0.9);
  const gapDeg = props.gap === undefined ? 1.5 : props.gap;
  const legend = props.legend !== false;
  const valueFmt = props.valueFmt || ((v) => fmtInt(v));
  const startAngle = props.startAngle === undefined ? -Math.PI / 2 : props.startAngle;

  const legendH = legend && (val(props.data) || []).length ? 18 : 0;
  const cx = size / 2;
  const cy = size / 2;
  const rMax = size / 2 - 6;

  return h("canvas", {
    width: size,
    height: size + legendH,
    onDraw: (ctx) => {
      const data = val(props.data) || [];
      const n = data.length;
      if (n === 0) return;
      const total = data.reduce((a, d) => a + (Number(d.value) || 0), 0) || 1;
      const vMax = Math.max.apply(null, data.map((d) => Number(d.value) || 0)) || 1;
      const step = (Math.PI * 2) / n;
      const gap = (gapDeg * Math.PI) / 180;
      const rInner = rMax * inner;

      if (rInner > 0) ctx.fillCircle(cx, cy, rInner, "rgba(10, 22, 48, 0.9)");

      for (let i = 0; i < n; i++) {
        const d = data[i];
        const v = Number(d.value) || 0;
        const a0 = startAngle + i * step + gap / 2;
        const a1 = startAngle + (i + 1) * step - gap / 2;
        // 半径按值/最大值 (不是占比) —— 玫瑰图的灵魂
        const rr = Math.max(2, (rMax * v) / vMax);
        const c = d.color || seriesColors[i % seriesColors.length];
        if (rInner > 0) {
          ctx.fillRing(cx, cy, rr, rInner, a0, a1, c);
        } else {
          ctx.beginPath();
          ctx.moveTo(cx, cy);
          ctx.arc(cx, cy, rr, a0, a1, false);
          ctx.closePath();
          ctx.fill(c);
        }
        // 扇区描边提亮
        ctx.strokeArc(cx, cy, rr, a0, a1, alpha(c, 0.85));
        // 值标签: 扇区中线、半径 60% 处 (放得下才画)
        const am = (a0 + a1) / 2;
        const lx = cx + Math.cos(am) * rr * 0.6;
        const ly = cy + Math.sin(am) * rr * 0.6;
        const fs = 10;
        const t = String(Math.round(v));
        if (rr > 26 && textWidth(t, fs) < rr * 0.7) {
          ctx.drawText(t, lx - textWidth(t, fs) / 2, ly - fs / 2, fs, "rgba(8, 20, 44, 0.9)");
        }
      }

      // 图例
      if (legendH) {
        let lx = 8;
        const ly = size + 2;
        for (let i = 0; i < n; i++) {
          const d = data[i];
          const c = d.color || seriesColors[i % seriesColors.length];
          ctx.fillRect(lx, ly + 3, 8, 8, c);
          const nm = String(d.name);
          ctx.drawText(nm, lx + 12, ly, 11, "#a9bcdf");
          const vt = valueFmt(Number(d.value) || 0);
          ctx.drawText(vt, lx + 12 + textWidth(nm, 11) + 6, ly, 11, "#7e93bd");
          lx += 12 + textWidth(nm, 11) + 6 + textWidth(vt, 11) + 14;
        }
      }
    },
  });
}
