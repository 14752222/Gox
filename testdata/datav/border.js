// ===== gx-datav BorderBox 科技风边框 =====
//
// 数据大屏的"大屏感"一半来自边框: 直角硬边 + 四角标记 + 半透明深底,
// 让内容像"嵌"在一块显示屏里。本组件提供 6 种预置边框, 全部用 canvas
// 原语画在一个铺满面板的画布上 (边框装饰层), 内容走普通 flex 流:
//
//   <BorderBox type={1} width={460} height={300} color="#22e6ff">
//     <column ...>...</column>
//   </BorderBox>
//
// 结构 (为什么是绝对定位三件套):
//   rect    面板底 (半透明深色, 让边框与内容有区分)
//   canvas  边框装饰 (绝对定位铺满, zIndex 压在最上, 不挡内容命中 ——
//           装饰层只在边缘几像素内落笔, 命中测试按绘制区域走)
//   column  内容流 (padding 由 type 决定: 斜角边框要留更大边距)
// 内核没有 stack 容器, 绝对定位 + zIndex 就是层叠的正解 (见 GUI 指南
// §5.2); canvas 不做事件命中之外的参与, 天然不干扰内容交互。
//
// 六种边框:
//   1 经典角标  四角 L 形亮线 + 面板 1px 边          (最通用)
//   2 双层边框  外细边 + 内亮边 + 四角短标
//   3 斜切角    八边形斜角 + 角线                    (机甲风)
//   4 左光条    左侧渐变竖条 + 细边框                (轻量, 适合列表)
//   5 外框光晕  双层边 + 四角亮块 + 外扩辉光          (重氛围)
//   6 科技角    四角复合装饰 + 顶部标题槽 + 可选扫描线
//
// 颜色语义: color 是主强调色 (角标/描边), background 是面板底。

import { h, requestAnimationFrame } from "gx/gfx";
import { createSignal } from "gx/solid";
import { palette, textWidth } from "./theme.js";
import { alpha, vGradient } from "./draw.js";

// 边框装饰厚度 (px): 各 type 共用的一档视觉重量, 单独抽出是为了
// "所有边框看起来是一套"而不是六个各自为政。
const EDGE = 2;
const CORNER = 14;

// cornerMarks(ctx, w, h, c, len, weight): 四角 L 形标记 (type 1/2 的
// 主角)。从四角向内缩 inset 后, 横竖各一笔。
function cornerMarks(ctx, w, h, c, len, weight, inset) {
  const i = inset === undefined ? 0 : inset;
  ctx.lineWidth = weight;
  ctx.beginPath();
  // 左上
  ctx.moveTo(i, i + len); ctx.lineTo(i, i); ctx.lineTo(i + len, i);
  // 右上
  ctx.moveTo(w - i - len, i); ctx.lineTo(w - i, i); ctx.lineTo(w - i, i + len);
  // 左下
  ctx.moveTo(i, h - i - len); ctx.lineTo(i, h - i); ctx.lineTo(i + len, h - i);
  // 右下
  ctx.moveTo(w - i - len, h - i); ctx.lineTo(w - i, h - i); ctx.lineTo(w - i, h - i - len);
  ctx.stroke(c);
  ctx.lineWidth = 1;
}

// 各 type 的绘制函数: (ctx, w, h, color, bg) => void。拆成表而不是
// switch —— 新增一种边框就是加一项, 不改动其它分支。
const painters = {
  // 1 经典角标: 细边 + 四角粗标
  1(ctx, w, h, c) {
    ctx.strokeRect(0.5, 0.5, w - 1, h - 1, alpha(c, 0.35));
    cornerMarks(ctx, w, h, c, CORNER, EDGE);
  },

  // 2 双层边框: 外细内亮, 四角短标加粗
  2(ctx, w, h, c) {
    ctx.strokeRect(0.5, 0.5, w - 1, h - 1, alpha(c, 0.45));
    ctx.strokeRect(3.5, 3.5, w - 7, h - 7, alpha(c, 0.18));
    cornerMarks(ctx, w, h, c, 10, EDGE, 3);
  },

  // 3 斜切角: 八边形 (四角斜切 cut) + 角线
  3(ctx, w, h, c) {
    const cut = 16;
    ctx.beginPath();
    ctx.moveTo(cut, 0.5); ctx.lineTo(w - cut, 0.5);
    ctx.lineTo(w - 0.5, cut); ctx.lineTo(w - 0.5, h - cut);
    ctx.lineTo(w - cut, h - 0.5); ctx.lineTo(cut, h - 0.5);
    ctx.lineTo(0.5, h - cut); ctx.lineTo(0.5, cut);
    ctx.closePath();
    ctx.stroke(alpha(c, 0.5));
    // 斜切面上的亮线
    ctx.beginPath();
    ctx.moveTo(cut, 0.5); ctx.lineTo(0.5, cut);
    ctx.moveTo(w - cut, 0.5); ctx.lineTo(w - 0.5, cut);
    ctx.moveTo(0.5, h - cut); ctx.lineTo(cut, h - 0.5);
    ctx.moveTo(w - 0.5, h - cut); ctx.lineTo(w - cut, h - 0.5);
    ctx.lineWidth = 2;
    ctx.stroke(c);
    ctx.lineWidth = 1;
  },

  // 4 左光条: 左侧 3px 渐变竖条 + 细边框 (适合排名/列表类面板)
  4(ctx, w, h, c) {
    ctx.strokeRect(0.5, 0.5, w - 1, h - 1, alpha(c, 0.22));
    vGradient(ctx, 0, 0, 3, h, c, 0.9, 0.15, 8);
    // 左条上下端头的小亮块
    ctx.fillRect(0, 0, 8, 2, c);
    ctx.fillRect(0, h - 2, 8, 2, c);
  },

  // 5 外框光晕: 内亮边 + 外扩两圈半透明边 + 四角亮块
  5(ctx, w, h, c) {
    ctx.strokeRect(4.5, 4.5, w - 9, h - 9, alpha(c, 0.85));
    ctx.strokeRect(1.5, 1.5, w - 3, h - 3, alpha(c, 0.25));
    ctx.strokeRect(-0.5, -0.5, w + 1, h + 1, alpha(c, 0.10)); // 外扩被画布裁掉一半, 剩下的是辉光
    cornerMarks(ctx, w, h, c, 18, EDGE);
    // 四角实心亮块
    ctx.fillRect(2, 2, 4, 4, c);
    ctx.fillRect(w - 6, 2, 4, 4, c);
    ctx.fillRect(2, h - 6, 4, 4, c);
    ctx.fillRect(w - 6, h - 6, 4, 4, c);
  },

  // 6 科技角: 四角复合装饰 (L + 短横 + 点), 顶边标题槽由 title 绘制
  6(ctx, w, h, c) {
    ctx.strokeRect(0.5, 0.5, w - 1, h - 1, alpha(c, 0.2));
    const L = 22, T = 3;
    // 左上: 双 L (外粗内细错位)
    ctx.lineWidth = T;
    ctx.beginPath();
    ctx.moveTo(0, L); ctx.lineTo(0, 0); ctx.lineTo(L, 0);
    ctx.stroke(c);
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(4, L + 4); ctx.lineTo(4, 4); ctx.lineTo(L + 4, 4);
    ctx.stroke(alpha(c, 0.55));
    // 右上 / 左下 / 右下同款
    ctx.lineWidth = T;
    ctx.beginPath();
    ctx.moveTo(w - L, 0); ctx.lineTo(w, 0); ctx.lineTo(w, L);
    ctx.moveTo(0, h - L); ctx.lineTo(0, h); ctx.lineTo(L, h);
    ctx.moveTo(w - L, h); ctx.lineTo(w, h); ctx.lineTo(w, h - L);
    ctx.stroke(c);
    ctx.lineWidth = 1;
    ctx.beginPath();
    ctx.moveTo(w - L - 4, 4); ctx.lineTo(w - 4, 4); ctx.lineTo(w - 4, L + 4);
    ctx.moveTo(4, h - L - 4); ctx.lineTo(4, h - 4); ctx.lineTo(L + 4, h - 4);
    ctx.moveTo(w - L - 4, h - 4); ctx.lineTo(w - 4, h - 4); ctx.lineTo(w - 4, h - L - 4);
    ctx.stroke(alpha(c, 0.55));
    // 角点
    ctx.fillCircle(6, 6, 1.5, c);
    ctx.fillCircle(w - 6, 6, 1.5, c);
    ctx.fillCircle(6, h - 6, 1.5, c);
    ctx.fillCircle(w - 6, h - 6, 1.5, c);
  },
};

// 内容 padding: 按 type 给不同的内边距 (斜角边框必须让开斜切面)。
const PADDING = { 1: 10, 2: 14, 3: 24, 4: 12, 5: 16, 6: 14 };

// scanline(ctx, w, h, t, color): type 6 的扫描线 —— 一条 2px 高的亮带
// 从顶到底循环 (t ∈ [0,1) 为相位)。大屏"活着"的标志, 默认关。
function scanline(ctx, w, h, t, color) {
  const y = (t % 1) * h;
  vGradient(ctx, 2, y - 8, w - 4, 16, color, 0, 0.5, 6);
  ctx.fillRect(2, y, w - 4, 1.5, alpha(color, 0.8));
}

// 用法同文件头。**children 是变参**: JSX 把 <BorderBox>…</BorderBox> 降级成
// BorderBox(props, ...children) (parser/jsx.go buildJSXCall), 不是 props.children
// —— 读 props.children 会静默得到空内容 (2026-10 实测: 树里内容列 children 为 [])。
//
// **布局类 prop 透传**: 根节点是个 column, 调用方写在 <BorderBox> 上的
// flexGrow / flexShrink / min* / max* / margin 是"我这个面板与父容器"的
// 契约, 原样转交根列。少转一个的症状是**静默的**: <BorderBox flexGrow={1}>
// 面板不伸长, 同列下方留一条窗口底色的空带, 而脚本照跑、截图断言照绿
// (2026-10-15 实测: 三屏大列的 flexGrow 面板全没伸开, 画面下方 1/4 是空的)。
// 反过来, 描述面板**内部**编排的 prop (padding / gap / radius / border /
// type / color / title …) 由 BorderBox 自己消费, 不透传。
const PASSTHRU_LAYOUT_PROPS = [
  "flexGrow", "flexShrink",
  "minWidth", "maxWidth", "minHeight", "maxHeight",
  "margin",
];
export function BorderBox(props, ...children) {
  props = props || {}; // 无属性写法 <BorderBox>…</BorderBox> 时 props 是 null
  const type = props.type || 1;
  const color = props.color || palette.accent;
  const background = props.background || palette.panel;
  // 命名躲开 import 的 h —— 局部变量叫 h 会把 h(tag, props) 遮住,
  // 报错形态是 "100 is not a function" ( props.height 被当函数调 )。
  const boxW = props.width;
  const boxH = props.height;
  const title = props.title;
  const scan = props.scan === true;
  const pad = props.padding === undefined ? PADDING[type] : props.padding;
  const paint = painters[type] || painters[1];

  // 扫描相位: 只在 scan 开启时跑动画心跳; 关闭时连信号都不建。
  // loop 与它引用的 setTick/startedAt 同在函数体一层 —— 内层块闭包引用
  // 外层变量会踩 TDZ 误判 (详见 table.js 同款注释)。
  let phase = null;
  if (scan) {
    const [tick, setTick] = createSignal(0);
    phase = tick;
    const startedAt = Date_now();
    const loop = () => {
      setTick(((Date_now() - startedAt) % 4000) / 4000);
      requestAnimationFrame(loop);
    };
    requestAnimationFrame(loop);
  }

  // 绝对定位铺满面板: 少了 position:"absolute" 它会当普通 flex 子项占掉
  // 100% 高度, 把内容流挤到面板外 (症状: 边框可见, 内容全无)。
  // 不加 zIndex —— 文档序在内容流之前, 天然垫在底下, 内容里的交互画布
  // (饼图悬停等) 才能收到命中。
  const decor = h("canvas", {
    position: "absolute", left: 0, top: 0,
    width: "100%",
    height: "100%",
    onDraw: (ctx) => {
      if (phase) phase(); // 订阅相位 → 扫描线推进时重绘
      ctx.clear("rgba(0,0,0,0)");
      paint(ctx, ctx.width, ctx.height, color);
      if (title) {
        // 标题槽: 顶部居中, 底下一段面板底色把边线遮断, 文字像嵌在槽里
        ctx.fillRect(boxW / 2 - 70, 0, 140, 3, background);
        ctx.drawText(title, boxW / 2 - textWidth(title, 15) / 2, 6, 15, color);
      }
      if (phase) scanline(ctx, ctx.width, ctx.height, phase(), alpha(color, 0.5));
    },
  });

  // 根列 props: 显式尺寸 + 透传的布局契约 (见文件头 PASSTHRU_LAYOUT_PROPS)
  const rootProps = { width: boxW, height: boxH };
  for (const k of PASSTHRU_LAYOUT_PROPS) {
    if (props[k] !== undefined) rootProps[k] = props[k];
  }

  return h("column", rootProps,
    h("rect", {
      position: "absolute", left: 0, top: 0, width: "100%", height: "100%",
      background: background,
      radius: props.radius || 0,
      border: props.border === false ? "rgba(0,0,0,0)" : alpha(color, 0.25),
      borderWidth: props.border === false ? 0 : 1,
    }),
    decor,
    h("column", {
      width: "100%", height: "100%",
      padding: pad,
      // 有标题时顶上多让一截, 避开标题槽
      paddingTop: title ? pad + 12 : pad,
    }, children) // 数组子节点: wireChild 会逐个展开 (gfx/node.go)
  );
}

// Date_now: 包一层便于测试替换。运行时没有 Date, 毫秒时间戳走
// Temporal.Now.instant().epochMilliseconds。
function Date_now() {
  return Number(Temporal.Now.instant().epochMilliseconds);
}
