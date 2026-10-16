// ===== gx-datav 子弹图 =====
//
// Stephen Few 的 bullet graph: 实际值条 + 目标线 + 背景区间带 (差/中/优
// 三档), 一屏塞进"实际 vs 目标 vs 历史区间"。比进度条多一层信息密度,
// 大屏 KPI 行的常用形态。
//
// props:
//   rows     [{name, value, target, max?, unit?}]  target 目标值, max 满格值 (缺省 100)
//   width    画布宽 (缺省 "100%")
//   rowH     行高 (缺省 26)
//   barH     条高 (缺省 10)
//   nameW    名称列宽 (缺省 80)
//   animMs   缓动 ms (缺省 800)
//   bands    背景区间比例 (缺省 [0.5, 0.75, 1] → 三档)
import { h } from "gx/gfx";
import { textWidth, fmtInt } from "./theme.js";
import { makeAnimValue } from "./motion.js";
import { alpha, roundBar } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function BulletChart(props) {
  const rowH = props.rowH || 26;
  const barH = props.barH || 10;
  const nameW = props.nameW === undefined ? 80 : props.nameW;
  const animMs = props.animMs || 800;
  const bands = props.bands || [0.5, 0.75, 1];

  const rows0 = val(props.rows) || [];
  const anims = rows0.map((r) => makeAnimValue(() => Number(r.value) || 0, animMs));
  // 区间带色: 由暗到亮, 实际值条压在最亮一档上仍能分辨
  const bandColors = ["rgba(70, 100, 160, 0.10)", "rgba(70, 100, 160, 0.16)", "rgba(70, 100, 160, 0.24)"];

  return h("canvas", {
    width: props.width || "100%",
    height: props.height || Math.max(1, rows0.length) * rowH,
    onDraw: (ctx) => {
      const rows = val(props.rows) || [];
      const w = ctx.width;
      for (let i = 0; i < rows.length; i++) {
        const r = rows[i];
        const y = i * rowH;
        const midY = y + rowH / 2;
        const v = Number(r.value) || 0;
        const max = Number(r.max) || 100;
        const target = Number(r.target) || 0;

        // 名称
        const nm = truncate(String(r.name || ""), 12, nameW);
        ctx.drawText(nm, 0, midY - 6, 12, "#d6e2f8");

        // 右端: 数值 + 目标
        const valTxt = fmtInt(v) + (r.unit || "");
        const tgtTxt = "目标 " + fmtInt(target);
        const vfs = 11;
        const valW = textWidth(valTxt, vfs);
        const tgtW = textWidth(tgtTxt, 10);
        const barX1 = w - valW - tgtW - 26;
        const barX0 = nameW + 8;
        const trackW = Math.max(0, barX1 - barX0);

        if (trackW > 20) {
          // 三档背景区间
          let prev = 0;
          for (let b = 0; b < bands.length; b++) {
            const bw = trackW * Math.min(1, bands[b]) - trackW * prev;
            if (bw > 0) {
              ctx.fillRect(barX0 + trackW * prev, midY - barH / 2, bw, barH,
                bandColors[Math.min(b, bandColors.length - 1)]);
            }
            prev = Math.min(1, bands[b]);
          }
          // 实际值条 (缓动)
          const cur = anims[i] ? anims[i].signal() : v;
          const ratio = Math.max(0, Math.min(1, cur / max));
          const bwv = Math.max(2, trackW * ratio);
          const c = ratio >= target / max ? "#2ee6a8" : "#22e6ff";
          roundBar(ctx, barX0, midY - barH / 2, bwv, barH, barH / 2, alpha(c, 0.9));
          // 目标线: 一条 2px 竖线 + 顶端小帽
          const tx = barX0 + trackW * (target / max);
          ctx.fillRect(tx - 1, midY - barH / 2 - 2, 2, barH + 4, "#ffc94d");
          ctx.fillRect(tx - 3, midY - barH / 2 - 4, 6, 2, "#ffc94d");
        }

        // 右端文本
        ctx.drawText(tgtTxt, w - valW - tgtW - 12, midY - 4, 10, "#7e93bd");
        ctx.drawText(valTxt, w - valW, midY - 6, vfs, i === 0 ? "#ffe9a8" : "#a9bcdf");
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
