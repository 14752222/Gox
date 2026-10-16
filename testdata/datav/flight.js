// ===== gx-datav 飞线图 =====
//
// 地图向的"数据在天上飞": 若干 (起点 → 终点) 弧线, 光点沿弧匀速巡游,
// 端点呼吸圆。不绑定任何地图 —— 给一张底图自己摆坐标, 或纯装饰用。
//
// props:
//   lines    [{from: [x, y], to: [x, y], color?, value?}]
//   width    画布宽 (缺省 400)
//   height   画布高 (缺省 260)
//   speed    光点速度 px/s (缺省 140)
//   curvature 弧的弯度 (0~1, 缺省 0.35; 0 = 直线)
//   trail    拖尾段数 (缺省 8)
//   dots     端点圆 (缺省 true)
//   labels   端点标签 [{x, y, text}] (缺省无)
import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { seriesColors } from "./theme.js";
import { alpha, glowCircle } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function FlightLines(props) {
  const width = props.width || 400;
  const height = props.height || 260;
  const speed = props.speed || 140;
  const curvature = props.curvature === undefined ? 0.35 : props.curvature;
  const trail = props.trail === undefined ? 8 : props.trail;
  const dots = props.dots !== false;

  const [t, setT] = createSignal(0);
  const startedAt = Number(Temporal.Now.instant().epochMilliseconds);
  const loop = () => {
    const dt = (Number(Temporal.Now.instant().epochMilliseconds) - startedAt) / 1000;
    setT(dt);
    requestAnimationFrame(loop);
  };
  requestAnimationFrame(loop);

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      const lines = val(props.lines) || [];
      const now = t();
      for (let i = 0; i < lines.length; i++) {
        const L = lines[i];
        const c = L.color || seriesColors[i % seriesColors.length];
        const x1 = Number(L.from[0]), y1 = Number(L.from[1]);
        const x2 = Number(L.to[0]), y2 = Number(L.to[1]);
        // 弧: 二次贝塞尔, 控制点取中点法向偏移 (弯度 × 距离)
        const mx = (x1 + x2) / 2, my = (y1 + y2) / 2;
        const dx = x2 - x1, dy = y2 - y1;
        const dist = Math.sqrt(dx * dx + dy * dy) || 1;
        const qx = mx - (dy / dist) * dist * curvature;
        const qy = my + (dx / dist) * dist * curvature;

        // 弧线本身: 采样 24 段 polyline (内核无 bezierCurveTo)
        ctx.beginPath();
        ctx.moveTo(x1, y1);
        for (let s = 1; s <= 24; s++) {
          const u = s / 24;
          const px = (1 - u) * (1 - u) * x1 + 2 * (1 - u) * u * qx + u * u * x2;
          const py = (1 - u) * (1 - u) * y1 + 2 * (1 - u) * u * qy + u * u * y2;
          ctx.lineTo(px, py);
        }
        ctx.stroke(alpha(c, 0.35));

        // 光点 + 拖尾: 沿参数 u 走, 每 1/64 弧长算一点 (匀速近似)
        const cycle = Math.max(0.6, dist / speed); // 飞完一圈的秒数
        const u0 = (now % cycle) / cycle;
        for (let k = trail; k >= 0; k--) {
          const u = Math.max(0, u0 - (k / 64) * 0.12);
          const px = (1 - u) * (1 - u) * x1 + 2 * (1 - u) * u * qx + u * u * x2;
          const py = (1 - u) * (1 - u) * y1 + 2 * (1 - u) * u * qy + u * u * y2;
          const fade = 1 - k / (trail + 1);
          ctx.fillCircle(px, py, 1.2 + fade * 1.6, alpha(c, 0.12 + fade * 0.5));
        }
        // 到达端: 一圈到尾闪一下
        if (u0 > 0.97) glowCircle(ctx, x2, y2, 4, c);

        // 端点
        if (dots) {
          const pulse = 0.5 + 0.5 * Math.sin(now * 2 + i);
          glowCircle(ctx, x1, y1, 3, c);
          ctx.fillCircle(x2, y2, 2.5 + pulse, alpha(c, 0.9));
        }
      }
      // 端点标签
      const labels = val(props.labels) || [];
      for (const lb of labels) {
        const t2 = String(lb.text || "");
        ctx.drawText(t2, Number(lb.x), Number(lb.y), 11, "#a9bcdf");
      }
    },
  });
}
