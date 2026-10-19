// 画廊截图: <pagination> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// 运行: ./gox testdata/shots/pagination.js
//
// 两档页码窗口: 首页（省略号在右）与中间页（两侧都有省略号）。S5 把当前项
// 从"只有描边"改成"强调色实底"，第 1 页与第 7 页的对比就是验收点。
import { createSignal, render } from "gox";

const [p1, setP1] = createSignal(1);
const [p7, setP7] = createSignal(7);

render(
  <window title="pagination" width={420} height={180}>
    <column gap={16} padding={16}>
      <pagination total={() => 200} pageSize={() => 10} current={() => p1()} onChange={e => setP1(e.page)} />
      <pagination total={() => 200} pageSize={() => 10} current={() => p7()} onChange={e => setP7(e.page)} />
    </column>
  </window>
);
