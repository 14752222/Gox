// P0-2 演示: progress / separator / spacer。
// 运行: go run . testdata/progress_demo.js
// 现象: 进度条每 400ms 前进 10%, 满格后归零; 分隔线分隔上下区域;
//       spacer 吃掉整行富余空间, 把两个色块推到左右两端。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

// 用整数步进 (0..10) 而不是浮点累加, 避免 0.30000000000000004 这类误差
const [step, setStep] = createSignal(0);
setInterval(() => setStep(s => (s >= 10 ? 0 : s + 1)), 400);

render(
  <window title="Progress demo" width={420} height={300}>
    <column gap={12} padding={16}>
      <text font={18}>Progress demo</text>
      <progress value={() => step() / 10}/>
      <text>{() => `value: ${step() * 10}%`}</text>

      <separator/>

      <row gap={0}>
        <rect width={80} height={24} background="#c0392b"/>
        <spacer flexGrow={1}/>
        <rect width={80} height={24} background="#27ae60"/>
      </row>
      <separator/>
    </column>
  </window>
);
