// ===== gx-datav 倒计时 =====
//
// 活动/开班倒计时: 天 · 时 · 分 · 秒四格, 每秒推进。目标时间用 epoch
// 毫秒 (运行时不有 Date, 但 Temporal.Now.instant().epochMilliseconds 给的
// 就是它; 目标端用 Date.parse 在**宿主页面**算好再传进来即可)。
// 到点后停在 0 (不穿负数), 可选 onEnd 回调。
//
// props:
//   target   目标 epoch ms (数字或 signal getter)
//   size     单字宽 (缺省 34); 格高 = size * 1.5
//   gap      格间距 (缺省 8)
//   showDays 天数格 (缺省 true; false 时只留 时/分/秒)
//   color    数字色 (缺省主文本色)
//   accent   "秒"格强调色 (缺省 warn 黄 —— 最后一位最抓眼)
//   bg       格底 (缺省半透明深蓝)
//   onEnd    到点回调 (一次)
import { h } from "gx/gfx";
import { createSignal } from "gx/solid";
import { palette } from "./theme.js";
import { alpha, centerText } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function Countdown(props) {
  const size = props.size || 34;
  const gap = props.gap === undefined ? 8 : props.gap;
  const showDays = props.showDays !== false;
  const color = props.color || palette.text;
  const accent = props.accent || palette.warn;
  const bg = props.bg || "rgba(10, 24, 56, 0.8)";

  const fontSize = Math.round(size * 0.5);
  const cellH = Math.round(size * 1.5);
  const unitFs = Math.max(9, Math.round(size * 0.2));

  // 剩余毫秒: setTimeout 对齐到下一秒边界后 setInterval —— 与 ClockPanel
  // 同款"对齐再进循环", 避免随启动相位漂移。
  const [left, setLeft] = createSignal(remain());
  const msNow = Number(Temporal.Now.instant().epochMilliseconds);
  setTimeout(() => {
    setLeft(remain());
    setInterval(() => setLeft(remain()), 1000);
  }, 1000 - (msNow % 1000));

  function remain() {
    const t = Number(val(props.target)) || 0;
    return Math.max(0, t - Number(Temporal.Now.instant().epochMilliseconds));
  }

  // 四格 (或三格): 每格 = 数字 canvas + 单位小字
  const units = showDays ? ["天", "时", "分", "秒"] : ["时", "分", "秒"];
  const n = units.length;
  const width = n * size + (n - 1) * gap;

  const children = [];
  for (let i = 0; i < n; i++) {
    const isSec = i === n - 1;
    children.push(h("column", { width: size, gap: 2, alignItems: "center" },
      h("canvas", {
        width: size, height: cellH,
        onDraw: (ctx) => {
          const ms = left();
          const parts = split(ms, showDays);
          const txt = String(parts[i]).padStart(showDays && i === 0 ? 2 : 2, "0");
          ctx.fillRect(0, 0, size, cellH, bg);
          ctx.strokeRect(0.5, 0.5, size - 1, cellH - 1, alpha(isSec ? accent : color, 0.25));
          centerText(ctx, txt, size / 2, (cellH - fontSize) / 2, fontSize,
            isSec ? accent : color);
        },
      }),
      h("canvas", {
        width: size, height: unitFs + 4,
        onDraw: (ctx) => {
          centerText(ctx, units[i], size / 2, 0, unitFs, palette.sub);
        },
      })
    ));
  }

  return h("row", { gap: gap }, children);
}

// split(ms, withDays): 毫秒 → [天, 时, 分, 秒]
function split(ms, withDays) {
  const s = Math.floor(ms / 1000);
  const d = Math.floor(s / 86400);
  const hh = Math.floor((s % 86400) / 3600);
  const mm = Math.floor((s % 3600) / 60);
  const ss = s % 60;
  return withDays ? [d, hh, mm, ss] : [hh + d * 24, mm, ss];
}
