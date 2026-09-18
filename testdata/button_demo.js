// P0-3 演示: button 的缺省外观 / 自定义配色 / disabled。
// 运行: go run . testdata/button_demo.js
// 现象: 前两个按钮点击计数 +1; 禁用按钮整体变灰且点击无任何效果
//       (既不改计数, 也不抢键盘焦点)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [count, setCount] = createSignal(0);

render(
  <window title="Button demo" width={420} height={300}>
    <column gap={12} padding={16}>
      <text font={20}>{() => `clicked: ${count()}`}</text>

      <button onClick={() => setCount(c => c + 1)}>默认按钮</button>

      <button
        background="#1a5fb4"
        border="#1a5fb4"
        color="#ffffff"
        onClick={() => setCount(c => c + 1)}
      >自定义配色</button>

      <button disabled={true} onClick={() => setCount(c => c + 100)}>禁用按钮</button>
    </column>
  </window>
);
