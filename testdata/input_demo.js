// P2-1 演示: 单行文本输入 —— 输入 / 退格 / 删除 / 光标移动。
// 运行: go run . testdata/input_demo.js
// 现象: 点输入框获焦 (边框转蓝、出现闪烁的 1px 竖线光标), 直接敲键盘即可输入,
//   下面的镜像文本实时跟着变 —— value 是受控的, 显示内容永远来自 signal。
//   - Backspace / Delete 删字符, ←/→ 移动光标, Home / End 跳到首尾;
//   - 点击框内任意位置可把光标落过去 (按点击 x 找最近的字符边界);
//   - placeholder 在值为空时以灰字显示, 且光标停在最左 (不被灰字挤走);
//   - Enter 不被输入框消费 → 冒泡到 onKeyDown, 这里用来计数;
//   - 带 Ctrl/Alt 的组合键也不消费, 留给脚本自己处理。
// 注意: 光标闪烁靠事件泵持续醒来驱动 —— 没有事件也没有定时器时泵会睡着,
//   光标就冻住了。这里挂一个空转的 requestAnimationFrame 循环 (浏览器里
//   也是动画帧在驱动光标闪烁)。
// 中文 IME 组合输入见 P2-7; 选区与拖选 v1 未做。
import { createSignal } from "gx/solid";
import { h, window, render, requestAnimationFrame } from "gx/gfx";

const [name, setName] = createSignal("");
const [enters, setEnters] = createSignal(0);

function tick() {
  requestAnimationFrame(tick);
}
tick();

render(
  <column gap={10} padding={16}>
    <text font={18}>Input</text>

    <input
      width={260}
      placeholder="Type your name"
      value={() => name()}
      onInput={(e) => setName(e.value)}
      onKeyDown={(e) => {
        if (e.key === "Enter") setEnters((n) => n + 1);
      }}
    />

    <text>{() => `name = "${name()}"`}</text>
    <text>{() => `enter presses = ${enters()}`}</text>
  </column>,
  window({ title: "Input demo", width: 360, height: 240 })
);
