// ===== gx-datav 仪表盘 =====
//
// 半圆速度表: 135° 起、225° 扫过 (正下方留缺口给单位标签)。三段式
// 刻度带 (绿→黄→红) 模拟"安全区/警戒区", 指针 + 中心大数值, 值变化
// 时指针与数字一起缓动。
//
// props:
//   value    当前值
//   min      (缺省 0)
//   max      (缺省 100)
//   unit     单位串 (缺省 "")
//   label   表名 (画在底部缺口处)
//   size     画布宽 (缺省 260), 高 = size * 0.62
//   color   指针色 (缺省强调色)
//   duration 缓动 ms (缺省 900)
//   decimals 小数位 (缺省 0)
import { h } from "gx/gfx";
import { makeAnimValue } from "./motion.js";
import { clamp, polar, textWidth } from "./theme.js";
import { alpha, mix, polyPath } from "./draw.js";

export function Gauge(props) {
  const min = props.min === undefined ? 0 : props.min;
  const max = props.max === undefined ? 100 : props.max;
  const unit = props.unit || "";
  const label = props.label || "";
  const size = props.size || 260;
  const color = props.color || "#22e6ff";
  const duration = props.duration || 900;
  const decimals = props.decimals || 0;
  const height = Math.round(size * 0.62);

  const getValue = typeof props.value === "function" ? props.value : () => props.value;
  const anim = makeAnimValue(getValue, duration);

  // 225° 扫过, 起点 135° (左下), 正下方留 45° 缺口
  const start = deg(135);
  const SWEEP = Math.PI * 1.25;

  return h("canvas", {
    width: size,
    height: height,
    onDraw: (ctx) => {
      const v = anim.signal();
      const ratio = clamp((v - min) / (max - min), 0, 1);
      const cx = size / 2;
      const cy = height - Math.round(height * 0.18);
      const r = Math.min(size / 2, height) - 14;

      const end = start + SWEEP;

      // 1. 刻度带: 24 段 fillRing, 颜色按"安全→警戒"插值
      const SEG = 24;
      for (let i = 0; i < SEG; i++) {
        const t0 = i / SEG, t1 = (i + 1) / SEG;
        const a0 = start + SWEEP * t0;
        const a1 = start + SWEEP * t1;
        const on = t1 <= ratio + 0.001;
        const c = on ? bandColor(t0) : alpha("#2a3f63", 0.9);
        // 段间留 1px 缝: 每段少吃 0.6° 的尾
        ctx.fillRing(cx, cy, r, r - 8, a0, a1 - deg(0.7), c);
      }

      // 2. 主刻度线 (每 1/10) + 刻度值
      const N = 10;
      for (let i = 0; i <= N; i++) {
        const t = i / N;
        const a = start + SWEEP * t;
        const p1 = polar(cx, cy, r - 11, a);
        const p2 = polar(cx, cy, r - (i % 5 === 0 ? 19 : 15), a);
        ctx.line(p1.x, p1.y, p2.x, p2.y, "rgba(160, 190, 240, 0.75)");
        if (i % 5 === 0) {
          const val = min + (max - min) * t;
          const txt = String(Math.round(val));
          const p3 = polar(cx, cy, r - 32, a);
          const fs = 10;
          ctx.drawText(txt, p3.x - textWidth(txt, fs) / 2, p3.y - fs / 2, fs, "rgba(160, 190, 240, 0.85)");
        }
      }

      // 3. 指针
      const pa = start + SWEEP * ratio;
      const tip = polar(cx, cy, r - 14, pa);
      const b1 = polar(cx, cy, 7, pa + Math.PI / 2);
      const b2 = polar(cx, cy, 7, pa - Math.PI / 2);
      polyPath(ctx, [tip, b1, b2]);
      ctx.fill(color);
      // 指针中心帽
      ctx.fillCircle(cx, cy, 6, "#0d1f3f");
      ctx.strokeCircle(cx, cy, 6, color);

      // 4. 中心数值 + 单位 + 表名
      const fs = Math.max(18, Math.round(size * 0.13));
      const txt = trimTo(v, decimals) + unit;
      ctx.drawText(txt, cx - textWidth(txt, fs) / 2, cy - fs - 6, fs, "#dbe8ff");
      if (label) {
        const ls = Math.max(10, Math.round(size * 0.05));
        ctx.drawText(label, cx - textWidth(label, ls) / 2, cy + 12, ls, "#7e93bd");
      }
    },
  });
}

function deg(d) { return (d * Math.PI) / 180; }

// bandColor(t): 刻度带取色 —— 前 55% 绿、中 25% 黄、后 20% 红。
// 用 mix 在交界处平滑, 与仪表盘"越过警戒区变红"的直觉一致。
function bandColor(t) {
  if (t < 0.55) return mix("#2ee6a8", "#ffc94d", t / 0.55);
  if (t < 0.8) return mix("#ffc94d", "#ff5c7a", (t - 0.55) / 0.25);
  return "#ff5c7a";
}

function trimTo(v, d) {
  const n = Number(v.toFixed(d));
  return String(n);
}
