// P3-2 演示: 过渡动画 (transition prop + animate 命令式 API)。
// 运行: go run . testdata/transition_demo.js
// 现象:
//   1. **宽度过渡** —— 按钮切换时, 蓝条宽度在 400ms 内平滑伸展/收缩,
//      而不是"一帧跳到位";
//   2. **不透明度过渡** —— 红/橙块整组在 opacity 1 ↔ 0.15 之间渐隐渐显
//      (opacity 是"成组"属性: 父节点半透明 = 整棵子树一起淡);
//   3. **位移过渡** —— 绿点沿 left 平滑移动 (绝对定位 + left 过渡);
//   4. **命令式 animate** —— 点击 "bounce" 时黄条用 animate() 自行插值,
//      每帧把进度写进 status 文本 (这段不经过任何元素属性)。
//
// 四个容易踩的点:
//   - **首次赋值不做过渡** (与 CSS 一致): 元素刚挂上时不会"从 0 长出来"。
//     想要入场动画, 用 animate() 显式表达。
//   - `transition` 只对**数值属性**生效, 且只认 width/height/left/top/opacity
//     (见 gfx/animate.go 的 animatableProps)。`value`/`padding`/`gap` 刻意排除:
//     前者的过渡会与脚本自己的受控写回打架, 后者半像素中间值会让文字穿透。
//   - 过渡期间**布局读到的是插值**, 所以兄弟节点会跟着让位 (不是只有视觉在动)。
//   - 动画结束前脚本里读到的 prop 已经是**终值**了 —— prop 是唯一真相,
//     插值只活在渲染层。想读"当前显示值"要自己在 signal 里维护。
import { createSignal } from "gx/solid";
import { h, render, animate } from "gx/gfx";

const [wide, setWide] = createSignal(false);
const [visible, setVisible] = createSignal(true);
const [right, setRight] = createSignal(false);
const [status, setStatus] = createSignal("idle");

render(
  h("column", { gap: 8, padding: 14 },
    h("text", { font: 13, width: 320 }, "1. transition: width"),
    h("button", { onClick: () => setWide(v => !v) }, "toggle width"),
    h("rect", {
      height: 18,
      background: "#2f80ed",
      transition: { width: 400 },
      width: () => (wide() ? 300 : 60)
    }),

    h("text", { font: 13, width: 320 }, "2. transition: opacity (成组淡出)"),
    h("button", { onClick: () => setVisible(v => !v) }, "toggle opacity"),
    h("row", {
      gap: 6, height: 28,
      transition: { opacity: 350 },
      opacity: () => (visible() ? 1 : 0.15)
    },
      h("rect", { width: 24, height: 24, background: "#c0392b" }),
      h("rect", { width: 24, height: 24, background: "#e8890c" }),
      h("text", { font: 12, width: 80, height: 24 }, "fading")
    ),

    h("text", { font: 13, width: 320 }, "3. transition: left (绝对定位位移)"),
    h("button", { onClick: () => setRight(v => !v) }, "slide"),
    h("rect", {
      width: 320, height: 24, background: "#f0f0f0",
      position: "absolute", top: 218
    }),
    h("rect", {
      width: 20, height: 20, background: "#27ae60",
      position: "absolute", top: 220,
      transition: { left: 500 },
      left: () => (right() ? 296 : 4)
    }),
    h("spacer", { height: 26 }),

    h("text", { font: 13, width: 320 }, "4. animate(from, to, dur, onUpdate, onDone)"),
    h("button", {
      onClick: () => {
        setStatus("running");
        animate(0, 100, 600,
          (v) => setStatus("progress " + Math.round(v) + "%"),
          () => setStatus("done"));
      }
    }, "bounce"),
    h("rect", {
      height: 14, background: "#f2c94c",
      transition: { width: 300 },
      width: () => (status() === "done" ? 300 : 40)
    }),
    h("text", { font: 12, width: 320 }, () => status())
  ),
  { title: "Transition demo", width: 360, height: 430 }
);
