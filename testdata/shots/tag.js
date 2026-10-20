// 画廊截图: <tag> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/tag.js
//
// 三行, 覆盖 S7 的三条验收点:
//   1. 高度 22 / 圆角 RadiusSM (6) —— 与按钮的 36 高、RadiusMD (8) 是两个档,
//      标签是"贴着文字的一小片", 不能做成按钮;
//   2. variant 走色系 (success / info / warning / danger): 浅底 + 深字, 与
//      alert 同一套"面/墨"规矩, 四个并排能看出四色是同一族而不是四个随手色;
//   3. background + color 仍然能直接盖掉 (自定义色是逃生口, 不是主路)。
import { render } from "gox";

render(
  <window title="tag" width={380} height={200}>
    <column gap={16} padding={16}>
      <row gap={8}>
        <tag>默认</tag>
        <tag>较长一点的标签</tag>
        <tag closable={true} onClose={() => {}}>可关闭</tag>
      </row>
      <row gap={8}>
        <tag variant="success">成功</tag>
        <tag variant="info">信息</tag>
        <tag variant="warning">警告</tag>
        <tag variant="danger">危险</tag>
      </row>
      <row gap={8}>
        <tag background="#e7f6ed" color="#1e7a45">自定义</tag>
      </row>
    </column>
  </window>
);
