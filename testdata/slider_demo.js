// P2-8 演示: <slider> 滑块。
// 运行: go run . testdata/slider_demo.js
// 现象:
//   1. volume 滑块 (0..100, step 5) —— 拖动或**单击轨道任意位置**都会改值,
//      上方的数字与下面的红条跟随变化;
//   2. zoom 滑块 (0..10, step 2) —— 值只会落在 0/2/4/6/8/10 上;
//   3. disabled 滑块 —— 拖不动, 整体降饱和。
//
// 三个容易踩的点:
//   - 滑块是**受控**的: `value` 决定它的位置, 交互只派发 `onInput({value})`。
//     脚本不回写 value, 滑块就会弹回原位 (和 input / textarea 同一套语义)。
//   - `onInput` 的参数是 **number** (不是字符串): `e.value` 直接拿去算就行,
//     不用 parseFloat。
//   - 拖动期间鼠标划过别的控件**不会**给它们加悬停高亮 —— 这是刻意的:
//     一次拖动是一个手势, 中途划过谁都不算"指向"它。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [vol, setVol] = createSignal(40);
const [zoom, setZoom] = createSignal(4);

render(
  h("column", { gap: 10, padding: 12 },
    h("text", { font: 13, width: 200 }, () => "volume: " + vol()),
    h("slider", {
      width: 200, min: 0, max: 100, step: 5,
      value: () => vol(),
      onInput: (e) => setVol(e.value),
    }),
    h("rect", { height: 12, background: "#c0392b", width: () => vol() * 1.6 }),

    h("text", { font: 13, width: 200 }, () => "zoom: " + zoom()),
    h("slider", {
      width: 200, min: 0, max: 10, step: 2,
      value: () => zoom(),
      onInput: (e) => setZoom(e.value),
    }),

    h("text", { font: 13, width: 200 }, "disabled:"),
    h("slider", { width: 200, min: 0, max: 100, step: 5, value: 70, disabled: true })
  ),
  { title: "Slider demo", width: 260, height: 260 }
);
