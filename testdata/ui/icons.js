// ===== gx-ui 内置图标集 (Phase 4, 方案 A: canvas 自绘) =====
//
// 零文件依赖: 每个图标是一个 (ctx, color) => void 的自绘函数, 在 24x24 的
// 逻辑坐标空间里用 canvas 原语 (fillRect / strokeRect / fillCircle /
// strokeCircle / line) 画出来, 坐标按 ctx.width 等比缩放, 因此任意尺寸的
// canvas 都能用 (20 / 24 / 32 ...), 越界部分由画布自动裁掉。
//
// 用法一 (直接画):
//   <canvas width={24} height={24} onDraw={(ctx) => icons.home(ctx, "#1a5fb4")} />
//
// 用法二 (IconView 兼容层, 本组件库内部统一走这条):
//   icon 字段同时接受"自绘函数"或"image 路径" (方案 B):
//     { icon: icons.home }                 → canvas 自绘
//     { icon: "assets/home.png" }          → <image src=... /> (相对进程工作目录)
//
// 刻意不做: 抗锯齿 / 路径 / 变换 (内核 canvas 目前没有), 所以图标全部用
// 直线 + 矩形 + 圆拼装 —— 看得出来是像素风, 但三平台观感一致且零依赖。
import { h } from "gx/gfx";

const draw = (fn) => fn;

export const icons = {
  // 房子: 屋顶两笔 + 方体 + 门
  home: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.line(3 * u, 11 * u, 12 * u, 4 * u, c);
    ctx.line(12 * u, 4 * u, 21 * u, 11 * u, c);
    ctx.strokeRect(5 * u, 11 * u, 14 * u, 9 * u, c);
    ctx.fillRect(10 * u, 14 * u, 4 * u, 6 * u, c);
  }),

  // 放大镜: 圆环 + 斜柄
  search: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.strokeCircle(10 * u, 10 * u, 6 * u, c);
    ctx.line(14.5 * u, 14.5 * u, 20 * u, 20 * u, c);
    ctx.line(15.5 * u, 13.5 * u, 21 * u, 19 * u, c);
  }),

  // 人: 头圆 + 肩圆 (下半被画布裁掉, 正好是半身像)
  user: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.fillCircle(12 * u, 7.5 * u, 4 * u, c);
    ctx.fillCircle(12 * u, 22 * u, 8 * u, c);
  }),

  // 齿轮: 外圈 + 内圈 + 8 根齿
  gear: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.strokeCircle(12 * u, 12 * u, 6 * u, c);
    ctx.fillCircle(12 * u, 12 * u, 2 * u, c);
    for (let i = 0; i < 8; i++) {
      const a = (Math.PI * 2 * i) / 8;
      const x1 = 12 * u + Math.cos(a) * 6.5 * u;
      const y1 = 12 * u + Math.sin(a) * 6.5 * u;
      const x2 = 12 * u + Math.cos(a) * 9 * u;
      const y2 = 12 * u + Math.sin(a) * 9 * u;
      ctx.line(x1, y1, x2, y2, c);
    }
  }),

  // 铃: 钟形 (圆裁半) + 底线 + 铃锤
  bell: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.strokeCircle(12 * u, 10 * u, 6 * u, c);
    ctx.line(6 * u, 10 * u, 6 * u, 15 * u, c);
    ctx.line(18 * u, 10 * u, 18 * u, 15 * u, c);
    ctx.line(5 * u, 15 * u, 19 * u, 15 * u, c);
    ctx.fillCircle(12 * u, 18.5 * u, 1.5 * u, c);
  }),

  // 气泡: 方框 + 左下尾巴
  chat: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.strokeRect(4 * u, 5 * u, 16 * u, 11 * u, c);
    ctx.line(4 * u, 16 * u, 4 * u, 20 * u, c);
    ctx.line(4 * u, 20 * u, 9 * u, 16 * u, c);
  }),

  // 文件夹: 标签耳 + 方体
  folder: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.line(3 * u, 7 * u, 9 * u, 7 * u, c);
    ctx.line(9 * u, 7 * u, 11 * u, 9 * u, c);
    ctx.strokeRect(3 * u, 9 * u, 18 * u, 10 * u, c);
    ctx.line(3 * u, 7 * u, 3 * u, 19 * u, c);
  }),

  // 日历: 方框 + 两根挂钉 + 横线
  calendar: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.strokeRect(4 * u, 6 * u, 16 * u, 14 * u, c);
    ctx.line(4 * u, 10 * u, 20 * u, 10 * u, c);
    ctx.line(8 * u, 4 * u, 8 * u, 8 * u, c);
    ctx.line(16 * u, 4 * u, 16 * u, 8 * u, c);
  }),

  // 心: 两圆 + 交叉线拼下尖
  heart: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.fillCircle(8.5 * u, 9 * u, 4 * u, c);
    ctx.fillCircle(15.5 * u, 9 * u, 4 * u, c);
    ctx.line(5 * u, 11 * u, 12 * u, 19 * u, c);
    ctx.line(12 * u, 19 * u, 19 * u, 11 * u, c);
  }),

  // 加号
  plus: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.line(12 * u, 5 * u, 12 * u, 19 * u, c);
    ctx.line(5 * u, 12 * u, 19 * u, 12 * u, c);
  }),

  // 对勾
  check: draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.line(5 * u, 12.5 * u, 10 * u, 17.5 * u, c);
    ctx.line(10 * u, 17.5 * u, 19 * u, 6.5 * u, c);
  }),

  // 左箭头 (返回)
  "arrow-left": draw((ctx, c) => {
    const u = ctx.width / 24;
    ctx.line(19 * u, 12 * u, 5 * u, 12 * u, c);
    ctx.line(5 * u, 12 * u, 11 * u, 6 * u, c);
    ctx.line(5 * u, 12 * u, 11 * u, 18 * u, c);
  }),
};

// 图标名清单 (逻辑测试用它断言数量与可调用性)。
export const ICON_KEYS = Object.keys(icons);

// IconView: icon 字段的兼容层 (方案 A / B 并存)。
//   function → canvas 自绘 (零文件依赖)
//   string   → image 路径 (走打包资源链路, 相对进程工作目录)
//   其余     → 不渲染 (不抛错, 静默空)
export function IconView(p) {
  const size = p.size || 24;
  if (typeof p.icon === "string") {
    return <image src={p.icon} width={size} height={size} />;
  }
  if (typeof p.icon === "function") {
    return <canvas width={size} height={size} onDraw={(ctx) => p.icon(ctx, p.color)} />;
  }
  return null;
}
