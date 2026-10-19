// 画廊截图: <drawer> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/drawer.js
//
// 抽屉只有打开时才有内容可看（关闭态就是整屏）。这里以 open 初始值直接
// 拍打开态: 遮罩 + 侧栏面板，两者的层次关系是 S7 统一缺省的检查点。
import { createSignal, render } from "gox";

const [open, setOpen] = createSignal(true);

render(
  <window title="drawer" width={420} height={320}>
    <column padding={16} gap={12}>
      <text>页面内容</text>
      <button onClick={() => setOpen(true)}>打开抽屉</button>
      <drawer open={() => open()} onClose={() => setOpen(false)}>
        <column gap={12} padding={16}>
          <text>抽屉标题</text>
          <list-item onClick={() => {}}>第一项</list-item>
          <list-item onClick={() => {}}>第二项</list-item>
        </column>
      </drawer>
    </column>
  </window>
);
