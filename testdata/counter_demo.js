// P3 集成演示: 文字渲染 + flex 布局 + 响应式计数器。
// 运行: go run . testdata/counter_demo.js
// 打包: cd packager && go run . ..\testdata\counter_demo.js --gui --name counter -o counter.exe
import { createSignal } from "gx/solid";
import { h, render } from "gx/gfx";

const [count, setCount] = createSignal(0);

render(
  <window title="Counter" width={400} height={300}>
    <column gap={8} padding={16}>
      <text font={20}>{() => `count: ${count()}`}</text>
      <button onClick={() => setCount(c => c + 1)}>加一</button>
    </column>
  </window>
);
