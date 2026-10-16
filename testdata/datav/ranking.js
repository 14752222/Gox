// ===== gx-datav 排行榜 =====
//
// Top N 榜单: 名次徽章 (前三金银铜) + 名称 + 数值条 + 数值。条宽按
// 值/最大值 比例, 值变化时缓动追平 (每行一个 makeAnimValue)。
// 全 canvas 自画 —— 条要动画、要避让文本, flex 做不到"条宽连续插值"
// 之外的轻量 (其实 flex 的 transition 只认 width 等五个属性且要显式
// 声明, 这里一行一条动画, 用 canvas 更直白)。
//
// props:
//   rows      [{name, value}]  (或 {label}, 兼容 key 名)
//   max       条满格值 (缺省 rows 里的最大值)
//   rowHeight 行高 (缺省 30)
//   color     条色 (缺省强调色)
//   showBar    数值条 (缺省 true)
//   valueFmt   数值格式化 (缺省 fmtInt)
//   nameFmt    名称列宽截断
import { h } from "gx/gfx";
import { fmtInt, textWidth } from "./theme.js";
import { makeAnimValue } from "./motion.js";
import { alpha, roundBar } from "./draw.js";

// val(v): 数据 prop 支持 signal getter。注意本组件的动画状态按**首帧**
// rows 建立 (文档约定: 换列表请换整个 props 值)。
function val(v) { return typeof v === "function" ? v() : v; }

export function RankingList(props) {
  const rows = (val(props.rows) || []).map((r) => ({
    name: r.name === undefined ? r.label : r.name,
    value: Number(r.value === undefined ? 0 : r.value),
  }));
  const rowHeight = props.rowHeight || 30;
  const color = props.color || "#22e6ff";
  const showBar = props.showBar !== false;
  const valueFmt = props.valueFmt || ((v) => fmtInt(v));
  const animMs = props.animMs || 800;

  let maxV = props.max;
  if (maxV === undefined) {
    maxV = 1;
    for (const r of rows) if (r.value > maxV) maxV = r.value;
  }

  // 每行一个缓动值 (rows 变化时这里不会重建 —— 文档约定: 换列表请换
  // 整个 props 值, 组件按首帧 rows 建动画状态)
  const anims = rows.map((r) => makeAnimValue(() => r.value, animMs));
  const barH = 8;

  const width = props.width || 320;
  const height = rows.length * rowHeight;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      // 名称/数值取最新 rows; 条形动画仍按建组件时的行 (换列表=换 props)
      const rows = val(props.rows) || [];
      // 宽度取 ctx.width (内核解析"100%"后的像素值): props.width 可能是
      // "100%" 字符串, 直接拿来做算术会得 NaN —— 数值文本画在 NaN 坐标
      // (静默不显示), 条也被 trackW>20 挡掉。曾在此踩过。
      const width = ctx.width;
      for (let i = 0; i < rows.length; i++) {
        const r = rows[i];
        const y = i * rowHeight;
        const midY = y + rowHeight / 2;

        // 名次徽章: 前三名奖牌色, 其余暗徽章
        const bw = 22;
        const bc = i === 0 ? "#ffd166" : i === 1 ? "#c9d6e8" : i === 2 ? "#e8a87c" : "rgba(70, 100, 160, 0.35)";
        ctx.fillRect(0, midY - 9, bw, 18, bc);
        const rank = String(i + 1);
        ctx.drawText(rank, bw / 2 - textWidth(rank, 11) / 2, midY - 5.5, 11,
          i < 3 ? "#1a2a45" : "#a9bcdf");

        // 名称
        const nameX = bw + 8;
        const nameFs = 12;
        const nameMaxW = props.nameWidth || 110;
        ctx.drawText(truncate(r.name, nameFs, nameMaxW), nameX, midY - nameFs / 2, nameFs, "#d6e2f8");

        // 数值 (右对齐到画布右缘, 条在它左边)
        const valFs = 13;
        const valTxt = valueFmt(r.value);
        const valW = textWidth(valTxt, valFs);
        ctx.drawText(valTxt, width - valW, midY - valFs / 2, valFs, i < 3 ? "#ffe9a8" : "#a9bcdf");

        // 数值条: 从名称尾到数值前, 高度 barH 居中
        if (showBar) {
          const barX0 = nameX + nameMaxW + 8;
          const barX1 = width - valW - 12;
          const trackW = barX1 - barX0;
          if (trackW > 20) {
            // 轨道
            ctx.fillRect(barX0, midY - barH / 2, trackW, barH, "rgba(70, 100, 160, 0.18)");
            // 条: 缓动值 → 圆角
            const ratio = anims[i].signal() / maxV;
            const bw2 = Math.max(2, trackW * ratio);
            const c = i === 0 ? "#ffd166" : i === 1 ? "#c9d6e8" : i === 2 ? "#e8a87c" : color;
            roundBar(ctx, barX0, midY - barH / 2, bw2, barH, barH / 2, alpha(c, 0.9));
            // 条端头亮点
            ctx.fillCircle(barX0 + bw2 - 1, midY, barH / 2, c);
          }
        }
      }
    },
  });
}

function truncate(s, size, maxW) {
  const str = String(s);
  if (textWidth(str, size) <= maxW) return str;
  let out = "";
  for (let i = 0; i < str.length; i++) { // 索引循环: for...of 按字节迭代, 见 theme.js
    if (textWidth(out + str.charAt(i) + "..", size) > maxW) break;
    out += str.charAt(i);
  }
  return out + "..";
}
