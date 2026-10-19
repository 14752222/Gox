// 画廊截图: <alert> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/alert.js
//
// 四种语义并列，这是 Prism tonal 底（subtle 底 + 同族文字 + 28% 边框）最直接
// 的样张: 一眼能看出四档是否"同族不同色"，而不是四段互不相干的彩色条。
import { render } from "gox";

render(
  <window title="alert" width={400} height={330}>
    <column gap={12} padding={16}>
      <alert level="info">这是一条信息提示。</alert>
      <alert level="success">操作已成功完成。</alert>
      <alert level="warning">磁盘空间不足，请清理。</alert>
      <alert level="error">连接失败，请重试。</alert>
    </column>
  </window>
);
