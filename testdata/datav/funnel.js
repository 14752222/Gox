// ===== gx-datav 漏斗图 =====
//
// 转化分析标配: 上宽下窄的梯形层叠, 每层右侧标注数值与对上一层的
// 转化率。数据按传入顺序画 (惯例是从大到小), 不自动排序 —— 顺序也是
// 业务语义 (注册→激活→付费), 组件不替用户猜。
//
// props:
//   data      [{name, value, color?}]  从上到下
//   width     画布宽 (缺省 340)
//   rowH      每层高 (缺省 34; 数据变长时按画布高自适应缩放)
//   gap       层间距 (缺省 6)
//   showRate  层级间转化率 (缺省 true)
//   valueFmt  数值格式化 (缺省 fmtInt)
//   neck      末层底宽/顶宽比 (缺省 0.55; 1 = 收成尖底)
//
// 画法: 第 i 层梯形的上下边宽按 value_i / value_0 比例; 下边宽取下一层
// 的值 —— 相邻层共享一条边, 视觉上是一条连续的漏斗而不是叠起来的梯形。
// 文本一律画在漏斗**右侧空白区** (层内宽度随数据变, 放不稳)。
import { h } from "gx/gfx";
import { seriesColors, fmtInt, textWidth } from "./theme.js";
import { polyPath } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function FunnelChart(props) {
  const width = props.width || 340;
  const rowH = props.rowH || 34;
  const gap = props.gap === undefined ? 6 : props.gap;
  const showRate = props.showRate !== false;
  const valueFmt = props.valueFmt || ((v) => fmtInt(v));
  const neck = props.neck === undefined ? 0.55 : props.neck;

  // 右侧文字区: 名称 + 数值 + 转化率, 漏斗只占左 52%
  // (funnelW/cx 在 onDraw 里按 ctx.width 现算 —— width prop 可能是百分比
  //  字符串, 建树期算一次会把 NaN 带进每一笔绘制, 见 wordcloud.js 的教训)

  // 高度: 没显式给就按首帧数据长度算 (getter 数据随后变长时, onDraw 里
  // 按 ctx.height 反推行高, 不会画出界)
  const n0 = (val(props.data) || []).length;
  const height = props.height || Math.max(1, n0) * rowH + Math.max(0, n0 - 1) * gap;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      const data = val(props.data) || [];
      const n = data.length;
      if (n === 0) return;
      // 画布尺寸以 ctx 为准 (width prop 可能是百分比字符串)
      const funnelW = Math.round(ctx.width * 0.52);
      const cx = funnelW / 2;
      const v0 = Math.max.apply(null, data.map((d) => Number(d.value) || 0)) || 1;
      // 行高自适应: 数据比首帧长时压缩, 短时留白
      const effRowH = Math.max(8, (ctx.height - (n - 1) * gap) / n);

      for (let i = 0; i < n; i++) {
        const d = data[i];
        const v = Number(d.value) || 0;
        const vNext = i < n - 1 ? (Number(data[i + 1].value) || 0) : v * neck;
        const y = i * (effRowH + gap);
        const wTop = (funnelW * v) / v0;
        const wBot = (funnelW * vNext) / v0;
        const c = d.color || seriesColors[i % seriesColors.length];

        polyPath(ctx, [
          { x: cx - wTop / 2, y: y },
          { x: cx + wTop / 2, y: y },
          { x: cx + wBot / 2, y: y + effRowH },
          { x: cx - wBot / 2, y: y + effRowH },
        ]);
        ctx.fill(c);

        const midY = y + effRowH / 2;
        // 右侧: 名称 (左) · 数值 (右) · 转化率 (数值下方)
        const tx = funnelW + 12;
        const valTxt = valueFmt(v);
        const valW = textWidth(valTxt, 12) + (showRate && i > 0 ? 46 : 0);
        const nameMaxW = Math.max(20, width - tx - valW - 12);
        const name = truncate(String(d.name), 12, nameMaxW);
        ctx.drawText(name, tx, midY - 7, 12, "#d6e2f8");
        ctx.drawText(valTxt, width - textWidth(valTxt, 12), midY - 7, 12, "#ffe9a8");
        if (showRate && i > 0) {
          const prev = Number(data[i - 1].value) || 0;
          const rate = prev > 0 ? (v / prev) * 100 : 0;
          const rateTxt = rate.toFixed(1) + "%";
          ctx.drawText(rateTxt, width - textWidth(rateTxt, 10), midY + 5, 10, "#7e93bd");
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
