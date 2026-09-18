// P3-1 演示: <canvas> 自绘 + 响应式重绘。
// 运行: go run . testdata/canvas_demo.js
// 现象:
//   1. 柱状图画布 —— 每 500ms 高亮的柱子右移一格 (signal 驱动, 靠 effect
//      收集 onDraw 里读到的依赖, 变化后自动重绘);
//   2. 原语展示画布 —— 矩形/边框/圆/圆环/直线/文字, 全部按**画布局部坐标**
//      落笔 (0,0 就是画布左上角), 越界部分被裁掉。
//
// 三个容易踩的点:
//   - onDraw 必须是一个**函数**。写成 `onDraw={draw()}` 会在挂载时先调一次
//     并把返回值当回调 —— 那就什么都不画 (返回值是 undefined)。
//   - 想让它自动重绘, 就在 onDraw 里读 signal。读普通变量不会产生依赖,
//     数据变了画布不会动 (这正是设计: 依赖由函数体里读到的信号决定)。
//   - 画布不铺底 (与 HTML canvas 一样是透明的)。要底色就 ctx.fillRect
//     铺一层, 或者给 canvas 挂 background 属性。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

// 固定的几何常量: 演示脚本刻意不用除法算坐标, 保证任何机器上像素一致。
const N = 8; // 柱子数
const BW = 24; // 柱宽
const GAP = 6; // 间距
const CW = GAP + N * (BW + GAP); // 画布宽 (由上面三个常量决定)
const CH = 110;
const BASE = CH - 14; // 基线 y
const DATA = [4, 7, 3, 8, 5, 9, 6, 2];

const [tick, setTick] = createSignal(0);

const chart = h("canvas", {
  width: CW,
  height: CH,
  onDraw: (ctx) => {
    // 读到 tick() → 这个 effect 订阅了 tick, 变化时会重绘
    const cur = tick() % N;
    ctx.fillRect(0, 0, ctx.width, ctx.height, "#fafafa");
    ctx.line(0, BASE, ctx.width - 1, BASE, "#cccccc");
    ctx.drawText("bars " + N, 4, 2, 12, "#555555");
    for (let i = 0; i < N; i++) {
      const bh = DATA[i] * 8;
      const x = GAP + i * (BW + GAP);
      ctx.fillRect(x, BASE - bh, BW, bh, i === cur ? "#c0392b" : "#7f8c8d");
    }
    // 当前列的读数写在右上角
    ctx.drawText(String(DATA[cur]), CW - 20, 2, 12, "#c0392b");
  },
});

const primitives = h("canvas", {
  width: 238,
  height: 72,
  background: "#ffffff",
  border: "#bbbbbb",
  onDraw: (ctx) => {
    ctx.fillRect(6, 6, 40, 24, "#2980b9"); // 实心矩形
    ctx.strokeRect(52, 6, 40, 24, "#c0392b"); // 空心矩形
    ctx.fillCircle(122, 18, 12, "#27ae60"); // 实心圆
    ctx.strokeCircle(158, 18, 12, "#8e44ad"); // 圆环
    ctx.line(6, 40, 220, 40, "#333333"); // 直线
    // 斜线 + 超长文本: 越界部分被画布裁掉 (不会溢出到界面其它地方)
    ctx.line(6, 66, 232, 46, "#e67e22");
    ctx.drawText("drawText clipped at the right edge", 6, 48, 13, "#111111");
  },
});

render(
  h("column", { gap: 10, padding: 10 },
    h("text", { font: 13, color: "#666666" }, "signal-driven bar chart (highlight moves)"),
    chart,
    h("text", { font: 13, color: "#666666" }, "ctx primitives (local coords, clipped)"),
    primitives
  ),
  { title: "Canvas demo", width: 300, height: 300 }
);

setInterval(() => setTick(tick() + 1), 500);
