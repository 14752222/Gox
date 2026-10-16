// ===== gx-datav 环形进度 =====
//
// 单环进度: 底部整圈轨道 + 从顶部 (-90°) 顺时针的进度弧 + 端点光晕 +
// 中心数值。值变化走 makeAnimValue 缓动, 途中每帧重绘。
//
// props:
//   value    0~100 (越界钳制)
//   size     直径 (缺省 120)
//   width    环厚 px (缺省 10)
//   color    进度色 (缺省强调色)
//   track    轨道色 (缺省暗蓝)
//   label    顶部小标签 (缺省无; 数值恒显示, 不被 label 替换)
//   sub      底部副标签 (缺省空)
//   unit     主标签后缀 (缺省 "%")
//   ticks    外圈刻度数 (0 = 不画)
//   glow     端点光晕 (缺省 true)
//   duration 缓动 ms (缺省 900)
import { h } from "gx/gfx";
import { makeAnimValue } from "./motion.js";
import { clamp, textWidth, polar } from "./theme.js";
import { alpha, glowCircle } from "./draw.js";

export function RingProgress(props) {
  const size = props.size || 120;
  const ringW = props.width || 10;
  const color = props.color || "#22e6ff";
  const track = props.track || "rgba(64, 128, 255, 0.15)";
  const unit = props.unit === undefined ? "%" : props.unit;
  const ticks = props.ticks || 0;
  const glow = props.glow !== false;
  const duration = props.duration || 900;

  const getValue = typeof props.value === "function" ? props.value : () => props.value;
  const anim = makeAnimValue(getValue, duration);

  const label = props.label; // 缺省由 onDraw 用当前值拼百分比
  const sub = props.sub || "";

  return h("canvas", {
    width: size,
    height: size,
    onDraw: (ctx) => {
      const v = anim.signal(); // 订阅 → 缓动每帧重绘
      const ratio = clamp(v / 100, 0, 1);
      const cx = size / 2, cy = size / 2;
      const rOuter = size / 2 - 2;
      const rInner = rOuter - ringW;
      const start = -Math.PI / 2; // 12 点方向

      // 外圈刻度 (先画, 压在轨道下面)
      if (ticks > 0) {
        const rt = rOuter + 3;
        for (let i = 0; i < ticks; i++) {
          const a = (Math.PI * 2 * i) / ticks - Math.PI / 2;
          const on = i / ticks <= ratio;
          const c = on ? alpha(color, 0.9) : "rgba(120, 150, 200, 0.25)";
          const len = i % 5 === 0 ? 6 : 3;
          const p1 = polar(cx, cy, rt, a);
          const p2 = polar(cx, cy, rt + len, a);
          ctx.line(p1.x, p1.y, p2.x, p2.y, c);
        }
      }

      // 轨道: 整圈 fillRing (rInner → rOuter 的一圈)
      ctx.fillRing(cx, cy, rOuter, rInner, 0, Math.PI * 2, track);

      // 进度弧: 端点加 0.5° 圆角帽的近似 (fillRing 无 lineCap,
      // 起终点各外扩一个线宽内的小扇片, 视觉上就是圆头)
      if (ratio > 0) {
        const end = start + ratio * Math.PI * 2;
        ctx.fillRing(cx, cy, rOuter, rInner, start, end, color);
        // 端点光晕
        if (glow) {
          const p = polar(cx, cy, (rOuter + rInner) / 2, end);
          glowCircle(ctx, p.x, p.y, ringW * 0.4, color);
        }
      }

      // 中心文本三层: 上标签 (label, 小) / 数值 (大, 恒显示) / 副标签 (sub, 小)。
      // 大屏 KPI 环的惯例是"数值做大、标签做小" —— 曾让 label 替换数值,
      // 结果环上只看得见"订单/完成率", 78% 那个最该被看见的数字反而没有。
      const mainFs = Math.max(14, Math.round(size * 0.24));
      const capFs = Math.max(9, Math.round(size * 0.11));
      const cyTxt = size / 2;
      if (label !== undefined) {
        ctx.drawText(String(label), cx - textWidth(String(label), capFs) / 2,
          cyTxt - mainFs / 2 - capFs - 6, capFs, "#7e93bd");
      }
      ctx.drawText(String(Math.round(v)) + unit, cx - textWidth(String(Math.round(v)) + unit, mainFs) / 2,
        cyTxt - mainFs / 2 - (sub ? 4 : 0), mainFs, props.textColor || "#dbe8ff");
      if (sub) {
        ctx.drawText(sub, cx - textWidth(sub, capFs) / 2, cyTxt + mainFs / 2 + 2, capFs, "#7e93bd");
      }
    },
  });
}

