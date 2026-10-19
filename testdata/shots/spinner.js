// 画廊截图: <spinner> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/spinner.js
//
// 三档尺寸 + 自定义色。动画在离屏渲染里是静态的一帧（取帧时相位固定），
// 所以要验的是"弧长与线宽的比例"，不是转速。
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
