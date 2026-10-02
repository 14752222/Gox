// 画廊截图: <label> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/label.js
//
// 三个都在一屏: 必填星号 / 右对齐 / 普通标签。
// 星号是**标记**不是内容 —— 它不进 TextContent(), 也不进无障碍名。
import { render } from "gox";

render(
  <window title="label" width={360} height={200}>
    <column gap={12} padding={16}>
      <row gap={8} alignItems="center">
        <label width={72} required>手机号</label>
        <input width={180} placeholder="11 位手机号" />
      </row>

      <row gap={8} alignItems="center">
        <label width={72} align="right">邮箱</label>
        <input width={180} />
      </row>

      <row gap={8} alignItems="center">
        <label width={72}>备注</label>
        <text color="#8a93a0">(选填)</text>
      </row>
    </column>
  </window>
);
