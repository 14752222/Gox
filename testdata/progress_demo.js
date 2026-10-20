// P0-2 演示: progress / separator / spacer。
// 运行: ./gox testdata/progress_demo.js
// 现象: 进度条每 400ms 前进 10%, 满格后归零; 下面三条是 S7 的固定态 ——
//       两条 level 语义色 (success / danger) 与一条不定进度 (滑块循环位移);
//       分隔线分隔上下区域; spacer 吃掉整行富余空间, 把两个色块推到两端。
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

// 用整数步进 (0..10) 而不是浮点累加, 避免 0.30000000000000004 这类误差
const [step, setStep] = createSignal(0);
setInterval(() => setStep(s => (s >= 10 ? 0 : s + 1)), 400);

render(
  <window title="Progress demo" width={420} height={380}>
    <column gap={12} padding={16}>
      <text font={18}>Progress demo</text>
      <progress value={() => step() / 10}/>
      <text>{() => `value: ${step() * 10}%`}</text>

      <separator inset={4}/>

      <text>level 语义色 + 不定进度</text>
      <progress value={0.8} width={200} level="success"/>
      <progress value={0.3} width={200} level="danger"/>
      <progress width={200} indeterminate={true}/>

      <row gap={0}>
        <rect width={80} height={24} background="#c0392b"/>
        <spacer flexGrow={1}/>
        <rect width={80} height={24} background="#27ae60"/>
      </row>
      <separator/>
    </column>
  </window>
);
