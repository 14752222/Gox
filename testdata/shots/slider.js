// 画廊截图: <slider> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/slider.js
import { createSignal, render } from "gox";

const [vol, setVol] = createSignal(40);

render(
  <window title="slider" width={360} height={230}>
    <column gap={10} padding={16}>
      {/* 拖动 / 单击轨道任意位置都会改值; onInput 收到的是 number */}
      <text font={13}>{() => "volume = " + vol()}</text>
      <slider width={240} min={0} max={100} step={5} model={vol} />

      {/* step 2: 值只会落在 0/2/4/... 上 */}
      <slider width={240} min={0} max={10} step={2} value={4} />

      {/* 禁用: 拖不动, 整体降饱和 */}
      <slider width={240} min={0} max={100} step={5} value={70} disabled={true} />
    </column>
  </window>
);
