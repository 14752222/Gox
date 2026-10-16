// ===== gx-datav 水球图 =====
//
// ECharts 那一颗会晃的水球: 圆球 + 双正弦波 + 中心百分比。百分比变化走
// makeAnimValue 缓动, 波面永不停 (rAF 推进相位) —— "数据是活的"在大屏上
// 就是靠这种小动作。
//
// props:
//   value     0~100 (越界钳制)
//   size      画布边长 (缺省 160)
//   color     球体描边/主色 (缺省强调色)
//   waveColor 波浪色 (缺省 ok 绿)
//   label     底部小标签 (缺省无; 数值恒显示)
//   unit      数值后缀 (缺省 "%")
//   duration  值缓动 ms (缺省 900)
//   wave      波幅占半径比 (缺省 0.08)
//   ring      外圈描边 (缺省 true)
//
// 实现要点: 内核 ctx 没有 clip() —— 圆内裁剪不能靠路径相交, 改用
// **scanline 填充**: 逐行扫过圆的竖径, 每行用勾股定理算圆内半宽,
// 波面在该行的 x 区间画一条 1px 横线。~size 行 fillRect, 成本可忽略,
// 且天然获得"双波叠色" (同一行先判后波再判前波)。
import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { makeAnimValue } from "./motion.js";
import { clamp, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function LiquidFill(props) {
  const size = props.size || 160;
  const color = props.color || "#22e6ff";
  const waveColor = props.waveColor || "#2ee6a8";
  const unit = props.unit === undefined ? "%" : props.unit;
  const duration = props.duration || 900;
  const waveAmp = props.wave === undefined ? 0.08 : props.wave;
  const ring = props.ring !== false;

  const getValue = typeof props.value === "function" ? props.value : () => props.value;
  const anim = makeAnimValue(getValue, duration);

  // 波相位: 独立于值缓动的第二根心跳。loop 与所引用变量同在函数体一层
  // (TDZ 误判绕法, 同 table.js)。
  const [wp, setWp] = createSignal(0);
  const startedAt = Number(Temporal.Now.instant().epochMilliseconds);
  const loop = () => {
    const dt = (Number(Temporal.Now.instant().epochMilliseconds) - startedAt) / 1000;
    setWp(dt % 4); // 4 秒一个相位周期, 取模防无限增长
    requestAnimationFrame(loop);
  };
  requestAnimationFrame(loop);

  return h("canvas", {
    width: size,
    height: size,
    onDraw: (ctx) => {
      const v = anim.signal();
      const ratio = clamp(v / 100, 0, 1);
      const cx = size / 2, cy = size / 2;
      const r = size / 2 - (ring ? 6 : 2);

      if (ring) {
        ctx.strokeCircle(cx, cy, r + 3, alpha(color, 0.5));
        ctx.strokeCircle(cx, cy, r + 1, alpha(color, 0.25));
      }

      // 球内底 (比面板深一档, 让球体从背景里"鼓"出来)
      ctx.fillCircle(cx, cy, r, "rgba(8, 18, 42, 0.85)");

      // 波面基准线: 满值在球顶, 0 在球底
      const top = cy + r - 2 * r * ratio;
      const amp = r * waveAmp;
      const ph = wp() * (Math.PI / 2); // 相位 → 弧度

      for (let y = Math.ceil(cy - r); y <= Math.floor(cy + r); y++) {
        const dy = y - cy;
        const half = Math.floor(Math.sqrt(Math.max(0, r * r - dy * dy)));
        if (half <= 0) continue;
        // 两条波: 后波异色异相 (慢且反向), 前波主色
        const back = top + amp * Math.sin(dy / r * 3 - ph * 1.4) + amp * 0.5;
        const front = top + amp * Math.sin(dy / r * 4 + ph);
        if (y >= front) {
          ctx.fillRect(cx - half, y, half * 2, 1, alpha(waveColor, 0.55));
        } else if (y >= back) {
          ctx.fillRect(cx - half, y, half * 2, 1, alpha(waveColor, 0.28));
        }
      }

      // 中心文本: 百分比 + 底部标签
      const fs = Math.max(16, Math.round(size * 0.17));
      const txt = String(Math.round(v)) + unit;
      ctx.drawText(txt, cx - textWidth(txt, fs) / 2, cy - fs / 2 - 4, fs, "#eaf6ff");
      if (props.label) {
        const ls = Math.max(10, Math.round(size * 0.075));
        ctx.drawText(String(props.label), cx - textWidth(String(props.label), ls) / 2,
          cy + fs / 2 + 2, ls, "#9fc2ee");
      }
    },
  });
}
