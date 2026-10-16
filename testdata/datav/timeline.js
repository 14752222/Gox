// ===== gx-datav 时间轴 =====
//
// 垂直事件流: 主轴 + 节点 (状态着色) + 时间戳 + 标题/详情。适合告警
// 流水、发布记录、值班日志这类"按时间看事"的面板。
//
// props:
//   events  [{time, title, detail?, status?}]  status: "ok"|"warn"|"bad"|"info" (缺省 info)
//   width   画布宽 (缺省 320)
//   rowH    每事件行高 (缺省 46; 带 detail 自动放宽到 58)
//   timeW   时间列宽 (缺省 74)
//   showLine 主轴 (缺省 true)
import { h } from "gx/gfx";
import { palette, textWidth } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

const NODE_COLORS = {
  ok: palette.ok,
  warn: palette.warn,
  bad: palette.bad,
  info: palette.accent,
};

export function Timeline(props) {
  const width = props.width || 320;
  const rowH = props.rowH || 46;
  const timeW = props.timeW === undefined ? 74 : props.timeW;
  const showLine = props.showLine !== false;

  const events0 = val(props.events) || [];

  return h("canvas", {
    width: width,
    height: props.height || Math.max(1, events0.length) * rowH,
    onDraw: (ctx) => {
      const events = val(props.events) || [];
      const n = events.length;
      if (n === 0) return;
      // 截断宽度以 ctx.width 为准: width prop 可能是百分比字符串 ("94%"),
      // "94%" - 数字 得 NaN, truncate 的 maxW 变 NaN 后永远截不断,
      // 还会给每条标题暗加 ".." 后缀 (2026-10-15 实测)。
      const width = ctx.width;
      // 行高自适应: 数据比首帧长时压缩
      const effH = Math.max(24, ctx.height / n);
      const axisX = timeW + 6;

      if (showLine) {
        ctx.fillRect(axisX, 4, 1, ctx.height - 8, alpha(palette.accent, 0.25));
      }

      for (let i = 0; i < n; i++) {
        const ev = events[i];
        const y = i * effH;
        const cy = y + effH / 2;
        const st = NODE_COLORS[ev.status] ? ev.status : "info";
        const c = NODE_COLORS[st];

        // 时间戳 (右对齐到轴左缘)
        const t = String(ev.time || "");
        const tfs = 10;
        ctx.drawText(t, timeW - 6 - textWidth(t, tfs), cy - tfs / 2, tfs, palette.sub);

        // 节点: 外圈 + 实心
        ctx.fillCircle(axisX + 0.5, cy, 4.5, alpha(c, 0.25));
        ctx.fillCircle(axisX + 0.5, cy, 2.5, c);

        // 标题 (+ 详情)
        const tx = axisX + 14;
        const tfs2 = 12;
        const title = truncate(String(ev.title || ""), tfs2, width - tx - 8);
        ctx.drawText(title, tx, cy - (ev.detail ? tfs2 + 3 : tfs2 / 2), tfs2, palette.text);
        if (ev.detail) {
          const dfs = 10;
          ctx.drawText(truncate(String(ev.detail), dfs, width - tx - 8), tx, cy + 3, dfs, palette.sub);
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
