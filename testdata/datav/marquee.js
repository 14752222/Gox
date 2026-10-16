// ===== gx-datav 滚动消息栏 =====
//
// 大屏底部/顶部的"跑马灯": 一条文字水平无缝滚动 (内容画两遍, 与
// ScrollTable 同款的无环缝思路)。适合告警播报、系统公告这类"要让所有人
// 抬头就看见"的信息。
//
// props:
//   text      字符串 (或 signal getter) —— 建议带前后缀分隔, 如 "● 系统正常 "
//   speed     滚动速度 px/s (缺省 60); 0 = 静止
//   fontSize  字号 (缺省 14)
//   color     文字色 (缺省主文本色)
//   accent    前缀标记色 (缺省强调色)
//   gap       两遍之间的间距 (缺省 80)
//   bg        底色 (缺省透明; 给半透明面板色可压住底纹)
//   height    画布高 (缺省按字号自适应)
//
// 坑位同 table.js: rAF 的 loop 与它引用的变量必须同在函数体一层 ——
// 内层块闭包引用外层函数体变量会踩 TDZ 误判。
import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { palette, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function MarqueeText(props) {
  const speed = props.speed === undefined ? 60 : props.speed;
  const fontSize = props.fontSize || 14;
  const color = props.color || palette.text;
  const accent = props.accent || palette.accent;
  const gap = props.gap === undefined ? 80 : props.gap;
  const bg = props.bg;
  const height = props.height || Math.round(fontSize * 1.8);

  const [phase, setPhase] = createSignal(0);
  const startedAt = Number(Temporal.Now.instant().epochMilliseconds);
  const loop = () => {
    const dt = (Number(Temporal.Now.instant().epochMilliseconds) - startedAt) / 1000;
    setPhase(dt * speed);
    requestAnimationFrame(loop);
  };
  if (speed > 0) requestAnimationFrame(loop);

  return h("canvas", {
    width: "100%",
    height: height,
    onDraw: (ctx) => {
      const w = ctx.width;
      if (bg) ctx.fillRect(0, 0, w, height, bg);
      const txt = String(val(props.text) || "");
      if (!txt) return;
      const cycleW = textWidth(txt, fontSize) + gap;
      const y = Math.round((height - fontSize) / 2);

      // 前缀标记: 文字左边一枚小菱形 (大屏公告的"台标"位)
      const markX = 10, markCY = height / 2;
      ctx.fillRect(markX, markCY - 3, 6, 6, accent);
      const x0 = markX + 14;
      const inner = w - x0 - 10;

      if (cycleW <= inner) {
        // 短消息: 不滚, 静态展示 (滚一个空程反而像坏了)
        ctx.drawText(txt, x0, y, fontSize, color);
        return;
      }
      // 长消息: phase 对整循环宽取模, 画两遍 —— 一遍在视口内、一遍接在
      // 它右边, 右边那份滑完左边这份正好补位, 视觉上无环缝。
      let off = phase() % cycleW;
      if (off < 0) off += cycleW;
      const x1 = x0 + inner - off;
      ctx.drawText(txt, x1, y, fontSize, color);
      ctx.drawText(txt, x1 + cycleW, y, fontSize, color);
    },
  });
}
