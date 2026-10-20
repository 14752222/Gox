// 画廊截图: <progress> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/progress.js
//
// 五条并列, 覆盖 S7 的三处验收点:
//   1. 轨道厚度 6 (Space3) + 胶囊圆角 —— 满格那条右端是圆头而不是方角,
//      且填充与轨道共用同一套圆角 (不再有"填充方角压在圆角轨道上");
//   2. level 走语义色 (success / warn), 与 alert 同 prop 名同取值;
//   3. indeterminate 是 1/3 轨道宽的滑块循环位移, 两端被轨道剪掉。
//
// 缺省高度就是 6, 不必显式给 height; 宽度必须给 —— progress 靠父容器
// stretch 拿宽度, 离屏脚本里没容器撑它就会退化成 0 宽空盒。
import { render } from "gox";

render(
  <window title="progress" width={360} height={200}>
    <column gap={16} padding={16}>
      <progress value={0} width={300} />
      <progress value={0.35} width={300} />
      <progress value={0.7} width={300} level="success" />
      <progress value={0.5} width={300} level="warn" />
      <progress value={0.9} width={300} level="danger" />
      <progress width={300} indeterminate={true} />
    </column>
  </window>
);
