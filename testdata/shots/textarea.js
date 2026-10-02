// 画廊截图: <textarea> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/textarea.js
import { createSignal, render } from "gox";

const [note, setNote] = createSignal("第一行\n第二行\n第三行");

render(
  <window title="textarea" width={360} height={240}>
    <column gap={12} padding={16}>
      {/* 多行内容; 超出可视高度纵向滚动并跟随光标 */}
      <textarea rows={3} width={280} model={note} />

      {/* 空值时的灰字提示 */}
      <textarea rows={2} width={280} placeholder="Type here..." />
    </column>
  </window>
);
