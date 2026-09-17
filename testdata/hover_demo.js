// P1-4 演示: button / checkbox / switch 的 hover 与 press 视觉反馈。
// 运行: go run . testdata/hover_demo.js
// 现象: 鼠标移上去背景提亮 (+12), 按住压暗 (-24); 移开/松手恢复原色。
//   - hovered/pressed 是渲染层的运行时状态, 不是 props, 脚本读不到也不需要读;
//   - 反馈只对有"面"的交互组件生效 (普通 rect 挂 onClick 不会变色)。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const [safe, setSafe] = createSignal(false);
const [auto, setAuto] = createSignal(true);

render(
  <column gap={14} padding={16}>
    <text font={18}>Hover and press</text>

    <row gap={12} alignItems="center">
      <button onClick={() => 0}>Default</button>
      <button background="#27ae60" color="#ffffff" onClick={() => 0}>Green</button>
      <button disabled={true} onClick={() => 0}>Disabled</button>
    </row>

    <row gap={10} alignItems="center">
      <checkbox checked={() => safe()} onClick={() => setSafe(v => !v)}/>
      <text>{() => `safe mode: ${safe() ? "on" : "off"}`}</text>
    </row>

    <row gap={10} alignItems="center">
      <switch checked={() => auto()} onClick={() => setAuto(v => !v)}/>
      <text>{() => `auto update: ${auto() ? "on" : "off"}`}</text>
    </row>

    <text>hover to brighten, hold the mouse down to darken</text>
  </column>,
  window({ title: "Hover demo", width: 440, height: 280 })
);
