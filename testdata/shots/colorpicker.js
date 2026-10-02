// 画廊截图: <colorpicker> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/colorpicker.js
//
// 拍的是**展开态** (生成器点一下字段): 色板弹层 + 字段行上的小色块。
import { createSignal, render } from "gox";

const [tint, setTint] = createSignal("#1e88e5");

render(
  <window title="colorpicker" width={380} height={330}>
    <column gap={12} padding={16}>
      {/* 缺省 24 色色板 (灰阶 + 色环), 8 列 */}
      <colorpicker width={200} model={tint} />

      <text>{() => "tint = " + tint()}</text>
    </column>
  </window>
);
