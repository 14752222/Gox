// 画廊截图: <select> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/select.js
//
// 这张拍的是**展开态**: 生成器会点一下字段把弹层打开 (galleryClickTargets)。
// 收起态的 select 就是一行灰字, 拍出来说明不了任何事。
import { createSignal, render } from "gox";

const CITIES = [
  { value: "sh", label: "Shanghai" },
  { value: "bj", label: "Beijing" },
  { value: "sz", label: "Shenzhen" },
  { value: "hz", label: "Hangzhou" },
];

const [city, setCity] = createSignal("sh");

render(
  <window title="select" width={360} height={300}>
    <column gap={12} padding={16}>
      {/* 弹层自带逃逸裁剪: 不会被 28px 的字段盒裁掉, 也不会被兄弟节点盖住 */}
      <select width={220} options={CITIES} model={city} />

      <text>{() => "city = " + city()}</text>
    </column>
  </window>
);
