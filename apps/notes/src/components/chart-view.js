// 图表视图 —— `<canvas onDraw>` 里调 `lib/chart.js` 的 `drawChart`。
//
// ## 为什么 cfg 收函数
//
// `onDraw` 会被跑两遍：一遍用**空操作 ctx** 收依赖（此时画布盒子可能还是 0），
// 一遍用真 ctx 落笔（见 gfx/canvas.go 头注释）。所以：
//
//   - cfg 收**取值函数**，onDraw 里第一件事就是 `p.cfg()` —— 这样换笔记（signal 变）
//     会自动重绘；写成 `cfg={cfg}` 只画第一帧，之后换笔记图不动。
//   - **别在 onDraw 里靠 `ctx.width` 决定要不要读 signal**（收依赖那一遍可能是 0）。
//     drawChart 内部读 `ctx.width` 只用来定尺寸，不用来决定读不读数据，所以安全。
//
// ## 尺寸
//
// 不给 cfg 写 width / height，drawChart 就按画布当前尺寸画 —— 同一份配置能塞进
// 不同大小的卡片，不必在配置里重复写一遍。
import { h } from "gox";
import { drawChart } from "../lib/chart.js";
import { colors } from "../theme.js";

export function ChartView(p) {
  return (
    <canvas
      width={p.width}
      height={p.height}
      background={colors.panel}
      onDraw={(ctx) => {
        const cfg = p.cfg === undefined ? null : p.cfg();
        if (cfg === null || cfg === undefined) {
          ctx.fillRect(0, 0, ctx.width, ctx.height, colors.panel);
          ctx.drawText("（这篇笔记没有图表）", 12, 12, 13, colors.muted);
          return;
        }
        drawChart(ctx, cfg);
      }}
    />
  );
}
