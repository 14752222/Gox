// ===== gx-datav 词云 =====
//
// 螺旋布局的词云: 按词频映射字号, 从中心沿阿基米德螺线向外找第一个
// 不撞已放词的位置。内核 drawText **不受 rotate/translate 变换影响**
// (变换只作用于路径原语, 实测 canvas.go 的 origin() 不应用变换) ——
// 所以词一律水平摆放, 靠字号差与疏密出效果, 不假装能旋转。
//
// props:
//   words   [{text, weight}]  weight 越大字越大 (缺省 1)
//   width   画布宽 (缺省 360)
//   height  画布高 (缺省 220)
//   minSize / maxSize 字号范围 (缺省 12 / 34)
//   color   文字色 (缺省主文本色; 高频词用强调色)
//   accentTop 前 N 个高频词用强调色 (缺省 3)
//   seed    布局种子 (同种子同布局, 截图可复现)
import { h } from "gx/gfx";
import { textWidth, makeRng } from "./theme.js";
import { alpha } from "./draw.js";

function val(v) { return typeof v === "function" ? v() : v; }

export function WordCloud(props) {
  const width = props.width || 360;
  const height = props.height || 220;
  const minSize = props.minSize || 12;
  const maxSize = props.maxSize || 34;
  const color = props.color || "#a9bcdf";
  const accentTop = props.accentTop === undefined ? 3 : props.accentTop;

  return h("canvas", {
    width: width,
    height: height,
    onDraw: (ctx) => {
      // 画布尺寸一律以 ctx 为准: width/height prop 可能是百分比字符串
      // ("94%"), 拿它做布局算术会得 NaN —— 螺线搜索全落在 NaN 坐标上,
      // drawText 被裁剪掉, 整块词云静默消失 (与 ranking.js / status.js
      // 同一教训)。依赖收集那一遍 ctx 尺寸可能是 0: 螺线随即越界退出,
      // 不画也不抛, 信号依赖已在函数前部登记。
      const width = ctx.width;
      const height = ctx.height;
      const words = (val(props.words) || []).slice();
      if (words.length === 0) return;
      // 归一化权重 → 字号; 按字号从大到小放 (大的先占中心)
      let wMax = 0;
      for (const w of words) if (Number(w.weight) > wMax) wMax = Number(w.weight);
      wMax = wMax || 1;
      for (const w of words) {
        w._fs = Math.round(minSize + (maxSize - minSize) * (Number(w.weight) || 0) / wMax);
      }
      words.sort((a, b) => b._fs - a._fs);

      // 已占矩形表: 粗略 AABB 碰撞 (词云用矩形够用, 多边形碰撞是杀鸡用牛刀)
      const placed = [];
      const cx = width / 2, cy = height / 2;
      const rng = makeRng(props.seed === undefined ? 42 : props.seed);

      for (let i = 0; i < words.length; i++) {
        const w = words[i];
        const fs = w._fs;
        const tw = textWidth(String(w.text), fs);
        const th = Math.round(fs * 1.15);
        // 螺线搜索: 半径递增加角度, 找第一个不撞的位置
        let px = 0, py = 0, ok = false;
        for (let step = 0; step < 900; step++) {
          const ang = step * 0.35 + i * 0.7; // 每个词的起始角错开, 避免都从正右出发
          const rad = 2.2 * step * 0.55;
          const x = cx + Math.cos(ang) * rad;
          const y = cy + Math.sin(ang) * rad * 0.72; // 竖方向压扁: 词是横长的
          const box = { x0: x - tw / 2, y0: y - th / 2, x1: x + tw / 2, y1: y + th / 2 };
          if (box.x0 < 2 || box.y0 < 2 || box.x1 > width - 2 || box.y1 > height - 2) {
            if (rad > Math.max(width, height)) break;
            continue;
          }
          let hit = false;
          for (const p of placed) {
            if (box.x0 < p.x1 && box.x1 > p.x0 && box.y0 < p.y1 && box.y1 > p.y0) { hit = true; break; }
          }
          if (!hit) { px = x; py = y; ok = true; placed.push(box); break; }
        }
        if (!ok) continue; // 放不下就放弃这个词 (画布满了)
        const c = i < accentTop ? "#22e6ff" : color;
        const jitter = (rng() - 0.5) * 0.35; // 微抖动打破机械感
        ctx.drawText(String(w.text), px - tw / 2, py - th / 2 + jitter, fs,
          i < accentTop ? c : alpha(c, 0.55 + 0.4 * (w._fs / maxSize)));
      }
    },
  });
}
