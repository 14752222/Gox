// ===== gx-datav 进度条组 =====
//
// 一行一条的横向进度: 名称 + 轨道 + 缓动条 + 右端数值。与 RankingList
// 的分工: 排行榜强调"排序与竞争" (奖牌/名次), 本组件强调"完成度"
// (每行独立 max, 条满格即 100%)。
//
// props:
//   rows       [{name, value, max?, color?}]  max 缺省 100
//   width      画布宽 (缺省 "100%")
//   rowHeight  行高 (缺省 30)
//   barH       条高 (缺省 8)
//   showValue  右端数值 (缺省 true)
//   valueFmt   数值格式化 (缺省 "78%" 百分比样式)
//   nameWidth  名称列宽 (缺省 90)
//   animMs     缓动 ms (缺省 800)
import { h } from "gx/gfx";
import { textWidth } from "./theme.js";
import { makeAnimValue } from "./motion.js";
import { alpha, roundBar } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function ProgressList(props) {
  const rowHeight = props.rowHeight || 30;
  const barH = props.barH || 8;
  const showValue = props.showValue !== false;
  const nameWidth = props.nameWidth === undefined ? 90 : props.nameWidth;
  const animMs = props.animMs || 800;
  const valueFmt = props.valueFmt || ((v, max) => Math.round((v / max) * 100) + "%");

  const rows0 = val(props.rows) || [];
  // 每行一个缓动值 (rows 变长时多出来的行动画从 0 起, 见 motion.js)
  const anims = rows0.map((r) => makeAnimValue(() => Number(r.value) || 0, animMs));

  return h("canvas", {
    width: props.width || "100%",
    height: props.height || Math.max(1, rows0.length) * rowHeight,
    onDraw: (ctx) => {
      const rows = val(props.rows) || [];
      const w = ctx.width;
      for (let i = 0; i < rows.length; i++) {
        const r = rows[i];
        const y = i * rowHeight;
        const midY = y + rowHeight / 2;
        const v = Number(r.value) || 0;
        const max = Number(r.max) || 100;
        const c = r.color || "#22e6ff";

        // 名称 (超出截断)
        const name = String(r.name);
        const nm = truncate(name, 12, nameWidth);
        ctx.drawText(nm, 0, midY - 6, 12, "#d6e2f8");

        // 数值 (右对齐)
        const valTxt = showValue ? String(valueFmt(v, max)) : "";
        const valW = showValue ? textWidth(valTxt, 12) : 0;

        // 轨道 + 条
        const barX0 = nameWidth + 10;
        const barX1 = w - valW - 10;
        const trackW = Math.max(0, barX1 - barX0);
        if (trackW > 20) {
          ctx.fillRect(barX0, midY - barH / 2, trackW, barH, "rgba(70, 100, 160, 0.18)");
          const cur = anims[i] ? anims[i].signal() : v;
          const ratio = Math.max(0, Math.min(1, cur / max));
          const bw = Math.max(2, trackW * ratio);
          roundBar(ctx, barX0, midY - barH / 2, bw, barH, barH / 2, alpha(c, 0.9));
          ctx.fillCircle(barX0 + bw - 1, midY, barH / 2, c);
        }
        if (showValue) {
          ctx.drawText(valTxt, w - valW, midY - 6, 12, i === 0 ? "#ffe9a8" : "#a9bcdf");
        }
      }
    },
  });
}

function truncate(s, size, maxW) {
  if (textWidth(s, size) <= maxW) return s;
  let out = "";
  for (let i = 0; i < s.length; i++) { // 索引循环: for...of 按字节迭代, 见 theme.js
    if (textWidth(out + s.charAt(i) + "..", size) > maxW) break;
    out += s.charAt(i);
  }
  return out + "..";
}
