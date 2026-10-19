// 画廊截图: <avatar> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/avatar.js
//
// circle / rounded / square 三种形状 + 首字母回退（无 src 时画首字）。
// 首字母的垂直基线是这类组件最常见的错位点，一行并排最容易看出来。
import { render } from "gox";

render(
  <window title="avatar" width={360} height={140}>
    <row gap={16} padding={16} alignItems="center">
      <avatar>王</avatar>
      <avatar shape="rounded">李</avatar>
      <avatar shape="square">Z</avatar>
      <avatar size={48}>大</avatar>
    </row>
  </window>
);
