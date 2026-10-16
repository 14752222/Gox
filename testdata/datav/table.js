// ===== gx-datav 轮播表格 =====
//
// 数据大屏的表格不是给人翻页的, 是"自己动"的: 行带从下往上连续
// 滚动, 到底循环。全 canvas 自画 —— flex + scrollTop 写入口也能做,
// 但循环回到顶部会有一次硬跳 (内容不重复), canvas 连续位移没有这
// 个断裂。
//
// 机制: 一个 phase 信号 (0~总行高) 由 requestAnimationFrame 推进,
// 每帧 setPhase → canvas 重绘; 画的时候 offset = phase % 行高, 内容
// 画两遍 (一遍在 offset 上方、一遍在 offset + 总高), 视觉上无缝。
//
// props:
//   columns  [{key, label, width?, align?}]  width 是权重 (缺省均分),
//             align: "left"|"right" (缺省 left; number 型默认 right)
//   rows     [{[key]: 值}]
//   width    画布宽 (缺省 100%)
//   height   可视高 (缺省 200)
//   rowHeight 行高 (缺省 30)
//   header   表头 (缺省 true)
//   speed    滚动速度 px/s (缺省 30)
//   interval 每行停顿 ms (缺省 0, 连续滚)
//   zebra    斑马纹 (缺省 true)
import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { textWidth } from "./theme.js";

// val(v): 数据 prop 支持 signal getter —— onDraw 时求值并订阅。
function val(v) { return typeof v === "function" ? v() : v; }

export function ScrollTable(props) {
  const columns = val(props.columns) || [];
  const rows = val(props.rows) || [];
  const width = props.width || "100%";
  const height = props.height || 200;
  const rowHeight = props.rowHeight || 30;
  const header = props.header !== false;
  const speed = props.speed || 30;
  const zebra = props.zebra !== false;

  const headH = header ? 26 : 0;
  const totalH = rows.length * rowHeight;

  // 滚动相位: rAF 推进。总行高 <= 可视高时不滚 (静止, 不占心跳)。
  //
  // 坑位 (运行时 TDZ 误判): 定时器/rAF 回调闭包**不能在内层块里引用
  // 外层函数体的变量** —— `let x; if (need) { const loop = () => { x = 1 } ... }`
  // 执行到回调会报 "Cannot access lexical declaration before initialization"
  // (对"外层声明 + 内层块闭包跨栈访问"误判, 即使运行时早已初始化)。
  // 绕法: loop 与它引用的变量都放在**函数体同一层**, 块里只留注册调用。
  const needScroll = totalH > height - headH;
  const [phase, setPhase] = createSignal(0);
  const startedAt = Number(Temporal.Now.instant().epochMilliseconds);
  const loop = () => {
    const dt = (Number(Temporal.Now.instant().epochMilliseconds) - startedAt) / 1000;
    setPhase((dt * speed) % totalH);
    requestAnimationFrame(loop);
  };
  if (needScroll) requestAnimationFrame(loop);

  // 列宽权重归一
  const weights = columns.map((c) => c.width === undefined ? 1 : c.width);
  const wSum = weights.reduce((a, b) => a + b, 0) || 1;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      const rows = val(props.rows) || [];
      const columns = val(props.columns) || [];
      const w = ctx.width;
      const off = phase(); // 订阅相位
      const xs = [];
      let acc = 0;
      for (let i = 0; i < columns.length; i++) {
        xs.push(acc);
        acc += (weights[i] / wSum) * w;
      }
      const cellX = (i) => xs[i];
      const cellW = (i) => (weights[i] / wSum) * w;

      // 表头
      if (header) {
        ctx.fillRect(0, 0, w, headH, "rgba(28, 52, 100, 0.65)");
        ctx.line(0, headH, w, headH, "rgba(120, 155, 220, 0.4)");
        for (let i = 0; i < columns.length; i++) {
          const c = columns[i];
          const align = c.align || (isNumeric(rows[0] && rows[0][c.key]) ? "right" : "left");
          drawCell(ctx, c.label, cellX(i), cellW(i), 0, headH, align, 11, "#a9bcdf", false);
        }
      }

      // 行带: 画两遍实现无缝循环
      const viewH = height - headH;
      for (let pass = 0; pass < 2; pass++) {
        const baseY = headH - off + pass * totalH;
        for (let r = 0; r < rows.length; r++) {
          const y = baseY + r * rowHeight;
          if (y + rowHeight < headH || y > height) continue; // 视口裁剪
          if (zebra && r % 2 === 1) {
            ctx.fillRect(0, y, w, rowHeight, "rgba(60, 95, 160, 0.10)");
          }
          const row = rows[r];
          for (let i = 0; i < columns.length; i++) {
            const c = columns[i];
            const v = row[c.key];
            const align = c.align || (isNumeric(v) ? "right" : "left");
            drawCell(ctx, v, cellX(i), cellW(i), y, rowHeight, align, 12, "#d6e2f8", true);
          }
          // 行线
          ctx.line(0, y + rowHeight, w, y + rowHeight, "rgba(90, 130, 200, 0.10)");
        }
      }
    },
  });
}

// drawCell: 单元格文本 (截断 + 对齐)。pad = 8px 左右留白。
function drawCell(ctx, v, x, cw, y, ch, align, fs, color, ellipsis) {
  const txt = v === undefined || v === null ? "" : String(v);
  const pad = 8;
  const maxW = cw - pad * 2;
  let t = txt;
  if (ellipsis && textWidth(t, fs) > maxW) {
    t = "";
    for (let i = 0; i < txt.length; i++) { // 索引循环: for...of 按字节迭代, 见 theme.js
      if (textWidth(t + txt.charAt(i) + "..", fs) > maxW) break;
      t += txt.charAt(i);
    }
    t += "..";
  }
  let tx = x + pad;
  if (align === "right") tx = x + cw - pad - textWidth(t, fs);
  ctx.drawText(t, tx, y + (ch - fs) / 2, fs, color);
}

function isNumeric(v) { return typeof v === "number"; }
