// 画廊截图: <progress> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/progress.js
//
// 四档进度（0 / 35% / 75% / 100%）+ 自定义色 + 显式宽度。
// progress 的缺省高度只有 8px，直接排进 column 会因为父容器的 stretch 拿到
// 0 宽度而变成空盒 —— 所以这里显式给 width，模拟真实用法。
// 轨道厚度是 S7 的验收点，四档并列才能看出"轨道与填充是否共用同一套圆角与厚度"。
import { render } from "gox";

render(
  <window title="progress" width={360} height={260}>
    <column gap={14} padding={16}>
      {/* 轨道与填充上下叠放，厚度差靠 height 拉开，便于看清两者边界 */}
      <progress value={0} width={300} height={8} />
      <progress value={0.35} width={300} height={8} />
      <progress value={0.75} width={300} height={8} />
      <progress value={1} width={300} height={8} />
      <progress value={0.6} width={300} height={12} color="#1a5fb4" />
    </column>
  </window>
);
