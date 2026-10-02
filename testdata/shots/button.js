// 画廊截图: <button> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/button.js
//
// 三个都写进一屏, 是为了让"缺省外观 / 自定义配色 / 禁用降饱和"能在同一张图上
// 对比 —— 这三条正是 button 最容易被改坏的地方 (禁用态忘记降饱和,
// 自定义配色只改了背景没改边框)。
import { h, render } from "gox";

render(
  <window title="button" width={360} height={200}>
    <column gap={12} padding={16}>
      <button onClick={() => {}}>默认按钮</button>

      <button
        background="#1a5fb4"
        border="#1a5fb4"
        color="#ffffff"
        onClick={() => {}}
      >自定义配色</button>

      <button disabled={true} onClick={() => {}}>禁用按钮</button>
    </column>
  </window>
);
