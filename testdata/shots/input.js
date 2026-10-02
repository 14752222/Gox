// 画廊截图: <input> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/input.js
import { createSignal, render } from "gox";

const [name, setName] = createSignal("");

render(
  <window title="input" width={360} height={200}>
    <column gap={12} padding={16}>
      {/* 受控: 显示只看 value, 输入经 onInput 写回 signal (model 一条顶两条) */}
      <input width={240} placeholder="Type your name" model={name} />

      {/* 带值 + 禁用态: 禁用是不可编辑、不参与焦点, 整体降饱和 */}
      <input width={240} value="植球" />
      <input width={240} value="不可编辑" disabled={true} />
    </column>
  </window>
);
