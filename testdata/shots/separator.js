// 画廊截图: <separator> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/separator.js
//
// 三组并排:
//   1. 满宽横线 —— 1px 发丝线的颜色是 S7 的验收点 (改走 borderDecorative);
//   2. 缩进横线 inset={4} —— 档位走 Space 台阶 (Space4 = 8dp) 而不是裸像素;
//   3. 竖线 + 缩进竖线 —— 竖线不撑满容器, 高度紧贴文字块; 缩进竖线从自己的
//      盒子上端/下端各让开 Space3, 于是 "竖线短一截" 与 "横线短一截" 用的是
//      同一条 inset 语义。
import { render } from "gox";

render(
  <window title="separator" width={380} height={220}>
    <column gap={12} padding={16}>
      <text>上方内容</text>
      <separator />
      <text>下方内容</text>
      <separator inset={4} />
      <text>缩进分隔线 (inset=4 → Space4)</text>
      <row gap={12} alignItems="center" height={40}>
        <text>左</text>
        <separator vertical={true} height={24} />
        <text>右</text>
        <separator vertical={true} height={24} inset={3} />
        <text>缩进竖线</text>
      </row>
    </column>
  </window>
);
