// 画廊截图: <avatar> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/avatar.js
//
// 24 / 32 / 40 三档 (缺省 32) + 形状 + 状态点, 一行并排:
//   1. 圆形是 S7 的缺省形状 —— 三档并排能看出"同一张脸放大两次";
//   2. status="online" 的点在右下角 45°, 与头像之间抠了一圈底色环
//      (软光栅没有"擦掉这一块"的操作, 只有再画一圈);
//   3. rounded / square 是形状 prop 的两个非缺省档。
import { render } from "gox";

render(
  <window title="avatar" width={360} height={120}>
    <row gap={16} padding={16} alignItems="center">
      <avatar size={24}>王</avatar>
      <avatar size={32}>李</avatar>
      <avatar size={40} status="online">赵</avatar>
      <avatar shape="rounded" size={40}>Z</avatar>
      <avatar shape="square" size={40}>Q</avatar>
    </row>
  </window>
);
