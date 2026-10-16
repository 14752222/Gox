// ===== gx-datav 状态指示灯组 =====
//
// 服务健康看板: 一行/一列状态灯 (呼吸光晕) + 名称 + 状态文案。适合
// 系统监控大屏的"总览条"—— 一眼扫过去, 红橙绿各自几颗。
//
// props:
//   items   [{name, status, detail?}]  status: "ok" | "warn" | "bad" | "off"
//   layout  "row" | "column" (缺省 column)
//   gap     项间距 (缺省 10)
//   dot     灯直径 (缺省 9)
//   showDetail 显示 detail 副文案 (缺省 true)
//   breathe 呼吸动画 (缺省 true; 关掉则静态灯)
//   width   画布宽 (column 布局时缺省 220; row 时由内容定)
import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { palette, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

// 状态色表: 集中一处, 换语义只改这里
const STATUS_COLORS = {
  ok: palette.ok,
  warn: palette.warn,
  bad: palette.bad,
  off: "rgba(120, 140, 175, 0.5)",
};
const STATUS_TEXT = { ok: "正常", warn: "警告", bad: "故障", off: "离线" };

export function StatusLight(props) {
  const layout = props.layout || "column";
  const gap = props.gap === undefined ? 10 : props.gap;
  const dot = props.dot || 9;
  const showDetail = props.showDetail !== false;
  const breathe = props.breathe !== false;

  // 呼吸相位: 每项错开一点 (index * 0.25), 整组像在"此起彼伏"
  const [ph, setPh] = createSignal(0);
  const startedAt = Number(Temporal.Now.instant().epochMilliseconds);
  const loop = () => {
    const dt = (Number(Temporal.Now.instant().epochMilliseconds) - startedAt) / 1000;
    setPh(dt % 2.4);
    requestAnimationFrame(loop);
  };
  if (breathe) requestAnimationFrame(loop);

  const items0 = val(props.items) || [];
  const rowH = showDetail ? 30 : 20;
  const width = props.width || (layout === "column" ? 220 : items0.length * 130);

  return h("canvas", {
    width: width,
    height: props.height || (layout === "column" ? items0.length * rowH : rowH),
    onDraw: (ctx) => {
      const items = val(props.items) || [];
      const p = ph();
      const w = ctx.width;
      for (let i = 0; i < items.length; i++) {
        const it = items[i];
        const st = STATUS_COLORS[it.status] ? it.status : "off";
        const c = STATUS_COLORS[st];
        // 每项错相 0.25s: 光晕脉冲不同步, 避免"整组一起闪"的机械感
        const phase = (p + i * 0.25) % 2.4;
        const pulse = breathe && st !== "off"
          ? 0.5 + 0.5 * Math.sin((phase / 2.4) * Math.PI * 2)
          : 0.5;
        const r = dot / 2;

        let x, y, cy;
        if (layout === "row") {
          x = 10 + i * ((w - 20) / Math.max(1, items.length));
          cy = ctx.height / 2;
        } else {
          x = 2;
          cy = i * rowH + rowH / 2;
        }
        const cx0 = x + r;

        // 灯: 外扩呼吸光晕 + 实心点 + 高光
        if (st !== "off") {
          ctx.fillCircle(cx0, cy, r + 3 + pulse * 2.5, alpha(c, 0.10 + pulse * 0.12));
        }
        ctx.fillCircle(cx0, cy, r, c);
        ctx.fillCircle(cx0 - r * 0.25, cy - r * 0.25, Math.max(1, r * 0.3), alpha("#ffffff", 0.55));

        // 名称 + 状态 (+ detail)
        const tx = x + dot + 8;
        const fs = 12;
        ctx.drawText(String(it.name || ""), tx, cy - fs / 2 - (showDetail && it.detail ? 6 : 0), fs, palette.text);
        ctx.drawText(STATUS_TEXT[st], tx + textWidth(String(it.name || ""), fs) + 8,
          cy - fs / 2 - (showDetail && it.detail ? 6 : 0), fs, c);
        if (showDetail && it.detail) {
          ctx.drawText(String(it.detail), tx, cy + 4, 10, palette.sub);
        }
      }
    },
  });
}
