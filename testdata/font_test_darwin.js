// macOS 字体渲染诊断: 逐类字符各占一行, 看哪些缺字。
import { h, render } from "gx/gfx";

render(
  <window title="Font Test" width={420} height={300}>
    <column gap={4} padding={16}>
      <text font={16}>{"ASCII: Hello Gox 123"}</text>
      <text font={16}>{"小写: abcdefg"}</text>
      <text font={16}>{"大写: ABCDEFG"}</text>
      <text font={16}>{"中文: 你好植球"}</text>
      <text font={16}>{"混合: count 1 加一"}</text>
    </column>
  </window>
);
