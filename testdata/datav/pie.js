// ===== gx-datav 饼图 / 环形图 =====
//
// 占比类图形一次做齐: 实心饼 (pie) 与环图 (donut) 同一套数据, 切换
// 只差一个 prop。扇区间隙 1.5° (贴边太死, 大屏上分不出瓣), 环图
// 中心放总计。悬停高亮 (onHover 回调 + 选中扇区外扩) 也带上 ——
// 大屏越来越多是触屏, 能点能悬停不是锦上添花。
//
// props:
//   data     [{name, value, color?}]  color 缺省取色板
//   donut    环图 (缺省 false); true 时 inner 为内径比 (0~1)
//   inner    环图内径比 (缺省 0.62)
//   size     画布边长 (缺省 200)
//   center   环图中心文案 (缺省总计 + 单位); pie 模式忽略
//   unit     中心文案单位
//   labelFmt 扇区百分比标签格式化 (缺省 trim1)
//   onHover  悬停扇区回调 ({name, value, index} | null)
//   startAngle 起始角 (缺省 -90°, 12 点起)
import { h } from "gx/gfx";
import { createSignal } from "gx/solid";
import { seriesColors, clamp, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

function deg(d) { return (d * Math.PI) / 180; }

function centerAt(ctx, t, cx, y, size, color) {
  ctx.drawText(String(t), cx - textWidth(t, size) / 2, y, size, color);
}

function val(v) { return typeof v === "function" ? v() : v; }

export function PieChart(props) {
  const data = val(props.data) || [];
  const donut = props.donut === true;
  const inner = clamp(props.inner === undefined ? 0.62 : props.inner, 0, 0.95);
  const size = props.size || 200;
  const unit = props.unit || "";
  const labelFmt = props.labelFmt || ((v) => String(Math.round(v * 10) / 10));
  const startAngle = props.startAngle === undefined ? -Math.PI / 2 : props.startAngle;

  const total = data.reduce((a, d) => a + (Number(d.value) || 0), 0);

  // 悬停索引: 鼠标位置 → 极径/角度 → 命中哪个扇区。不用 canvas 事件,
  // 自己算 —— 内核 hit-test 对 canvas 是整块矩形, 扇区级命中必须
  // 脚本自己判 (onMouseMove 挂在 canvas 上, 拿 {x, y} 算)。
  const [hover, setHover] = createSignal(-1);

  const cx = size / 2, cy = size / 2;
  const rOuter = size / 2 - 6;
  const rInner = donut ? rOuter * inner : 0;

  return h("canvas", {
    width: size,
    height: size,
    onMouseMove: (e) => {
      const data = val(props.data) || [];
      const dx = e.x - cx, dy = e.y - cy;
      const dist = Math.sqrt(dx * dx + dy * dy);
      if (dist < rInner || dist > rOuter + 4) { if (hover() !== -1) setHover(-1); return; }
      let a = Math.atan2(dy, dx) - startAngle;
      while (a < 0) a += Math.PI * 2;
      // 沿扇区走一遍, 看 a 落在谁家
      let acc = 0;
      let idx = -1;
      for (let i = 0; i < data.length; i++) {
        const frac = total > 0 ? (Number(data[i].value) || 0) / total : 0;
        if (a >= acc * Math.PI * 2 && a < (acc + frac) * Math.PI * 2) { idx = i; break; }
        acc += frac;
      }
      if (hover() !== idx) {
        setHover(idx);
        if (props.onHover) props.onHover(idx >= 0 ? data[idx] : null);
      }
    },
    onMouseLeave: () => setHover(-1),
    onDraw: (ctx) => {
      const data = val(props.data) || [];
      const hi = hover();
      // 中心底盘 (donut 中间别透出大屏底纹, 压一块与面板一致的底)
      if (donut) ctx.fillCircle(cx, cy, rInner, "rgba(10, 22, 48, 0.9)");

      let acc = 0;
      for (let i = 0; i < data.length; i++) {
        const d = data[i];
        const frac = total > 0 ? (Number(d.value) || 0) / total : 0;
        if (frac <= 0) { acc += frac; continue; }
        const a0 = startAngle + acc * Math.PI * 2;
        const a1 = a0 + frac * Math.PI * 2;
        // 1.5° 间隙: 尾端少吃一点
        const gap = Math.min(deg(1.5), (a1 - a0) * 0.25);
        const c = d.color || seriesColors[i % seriesColors.length];
        const rr = hi === i ? rOuter + 3 : rOuter; // 悬停外扩 3px
        if (donut) {
          ctx.fillRing(cx, cy, rr, rInner, a0, a1 - gap, c);
        } else {
          ctx.beginPath();
          ctx.moveTo(cx, cy);
          ctx.arc(cx, cy, rr, a0, a1 - gap, false);
          ctx.closePath();
          ctx.fill(c);
        }
        acc += frac;
      }

      // 环图中心: 总计 + 单位 + 悬停项
      if (donut) {
        if (hi >= 0 && hi < data.length) {
          const d = data[hi];
          const fs = Math.round(size * 0.12);
          const txt = labelFmt((Number(d.value) / total) * 100) + "%";
          centerAt(ctx, txt, cx, cy - fs / 2 - 2, fs, "#dbe8ff");
          const ns = Math.max(10, Math.round(size * 0.07));
          centerAt(ctx, d.name, cx, cy + fs / 2 + 4, ns, "#7e93bd");
        } else {
          const fs = Math.round(size * 0.16);
          const txt = props.center || String(Math.round(total)) + unit;
          centerAt(ctx, txt, cx, cy - fs / 2 - 2, fs, "#dbe8ff");
          const ns = Math.max(10, Math.round(size * 0.075));
          const sub = props.centerSub || "总计";
          centerAt(ctx, sub, cx, cy + fs / 2 + 4, ns, "#7e93bd");
        }
      }
    },
  });
}
