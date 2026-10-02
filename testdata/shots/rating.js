// 画廊截图: <rating> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/rating.js
//
// 三种一起拍: 缺省强调色 / 自定义色 + 自定义星数 / 禁用降饱和。
// 完全受控: 显示只看 value, 点第几格派发 onChange({value}), 值不变不派发。
import { createSignal, render } from "gox";

const [score, setScore] = createSignal(3);

render(
  <window title="rating" width={360} height={220}>
    <column gap={12} padding={16}>
      <row gap={8} alignItems="center">
        <rating value={score()} onChange={(e) => setScore(e.value)} />
        <text>{() => score() + " / 5"}</text>
      </row>

      <rating value={7} max={10} color="#e01b24" />

      <rating value={2} disabled={true} />
    </column>
  </window>
);
