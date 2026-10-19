// 画廊截图: <tag> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tag.js
//
// 尺寸档位 + 可关闭 + 自定义色。胶囊形（radius full）在 S7 是硬要求，
// 这里并排放三个不同字数的标签，能看出"胶囊是随内容宽度自适应"的。
import { render } from "gox";

render(
  <window title="tag" width={380} height={300}>
    <column gap={16} padding={16}>
      <row gap={8} wrap={true}>
        <tag>默认</tag>
        <tag>较长一点的标签</tag>
        <tag closable={true} onClose={() => {}}>可关闭</tag>
      </row>
      <row gap={8}>
        <tag background="#e7f6ed" color="#1e7a45">成功</tag>
        <tag background="#fbe9e7" color="#912b20">失败</tag>
      </row>
    </column>
  </window>
);
