// P0-1 演示: checkbox / radio / switch 三个受控组件。
// 运行: go run . testdata/form_demo.js
// 现象: 勾选框切换、单选互斥、开关切换, 右侧文字即时跟随。
//   - 三个组件都是"纯受控": 控件自身不存状态, checked 完全由 signal 驱动;
//   - radio 的互斥不在内核里, 而是靠共享一个 signal + 比较值实现 (见 setSize)。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [agree, setAgree] = createSignal(false);
const [size, setSize] = createSignal("S");
const [notify, setNotify] = createSignal(true);

render(
  <window title="Form demo" width={420} height={340}>
    <column gap={14} padding={16}>
      <text font={18}>Form controls</text>

      <row gap={8} alignItems="center">
        <checkbox checked={() => agree()} onClick={() => setAgree(v => !v)}/>
        <text>{() => (agree() ? "Agreed" : "Not agreed")}</text>
      </row>

      <row gap={14} alignItems="center">
        <row gap={4} alignItems="center">
          <radio checked={() => size() === "S"} onClick={() => setSize("S")}/>
          <text>S</text>
        </row>
        <row gap={4} alignItems="center">
          <radio checked={() => size() === "M"} onClick={() => setSize("M")}/>
          <text>M</text>
        </row>
        <row gap={4} alignItems="center">
          <radio checked={() => size() === "L"} onClick={() => setSize("L")}/>
          <text>L</text>
        </row>
      </row>
      <text>{() => `size: ${size()}`}</text>

      <row gap={8} alignItems="center">
        <switch checked={() => notify()} onClick={() => setNotify(v => !v)}/>
        <text>{() => (notify() ? "Notifications on" : "Notifications off")}</text>
      </row>
    </column>
  </window>
);
