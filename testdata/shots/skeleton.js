// 画廊截图: <skeleton> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/skeleton.js
//
// 三种骨架形态: 纯文本行、头像 + 文本、卡片。shimmer 的呼吸相位在离屏帧里
// 固定在初始值，所以这里验的是**块之间的间距与圆角**，以及 avatar 变体。
import { render } from "gox";

render(
  <window title="skeleton" width={380} height={320}>
    <column gap={16} padding={16}>
      <skeleton rows={3} />
      <skeleton rows={2} avatar={true} />
      <skeleton rows={4} titleWidth={40} />
    </column>
  </window>
);
