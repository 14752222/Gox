// 画廊截图: <separator> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/separator.js
//
// 横竖两种方向。1px 发丝线的颜色是 S7 的验收点（改走 borderDecorative），
// 竖线必须紧贴文字块的高度而不是撑满容器 —— 并排两组能同时看到这两点。
import { render } from "gox";

render(
  <window title="separator" width={380} height={200}>
    <column gap={12} padding={16}>
      <text>上方内容</text>
      <separator />
      <text>下方内容</text>
      <row gap={12} alignItems="center" height={40}>
        <text>左</text>
        <separator vertical={true} />
        <text>右</text>
      </row>
    </column>
  </window>
);
