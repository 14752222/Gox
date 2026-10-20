// 画廊截图: <spinner> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/spinner.js
//
// 三档尺寸 + 自定义色。动画在离屏渲染里是静态的一帧（取帧时相位固定），
// 所以要验的是**底环 + 90° 高亮弧**这套形状本身：环宽随直径等比缩放
// (24 时 3px)，弧长恒为一圈的四分之一，三档看起来是同一个东西的三倍大小。
import { render } from "gox";

render(
  <window title="spinner" width={360} height={160}>
    <row gap={24} padding={16} alignItems="center">
      <spinner />
      <spinner size={28} />
      <spinner size={40} color="#c0392b" />
    </row>
  </window>
);
