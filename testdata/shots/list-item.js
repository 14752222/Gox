// 画廊截图: <list-item> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/list-item.js
//
// 四种行态并列: 常态 / 选中 / 禁用 / 无分隔线。选中态的"左强调条"是 S5
// 的新增表达，这四行就是它的验收样张。
import { render } from "gox";

render(
  <window title="list-item" width={360} height={300}>
    <column padding={16} gap={0}>
      <list-item onClick={() => {}}>常态列表项</list-item>
      <list-item selected={true} onClick={() => {}}>选中列表项</list-item>
      <list-item disabled={true} onClick={() => {}}>禁用列表项</list-item>
      <list-item divider={false} onClick={() => {}}>无分隔线</list-item>
    </column>
  </window>
);
