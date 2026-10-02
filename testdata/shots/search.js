// 画廊截图: <search> —— 由 gfx/gallery_shot_test.go 离屏渲染成 PNG
// (生成方式见 docs/dev-workflow.md 的「组件截图流水线」)。
// 运行: ./gox testdata/shots/search.js
//
// 与 input 的分工: 左侧放大镜图标提示"这是搜索位",
// 获焦时按 Enter 走专门的 onSearch({value}), 不用在 onKeyDown 里手判键名。
import { createSignal, render } from "gox";

const [q, setQ] = createSignal("植球");

render(
  <window title="search" width={360} height={210}>
    <column gap={12} padding={16}>
      <search width={240} model={q} onSearch={(e) => setQ(e.value)} />

      <search width={240} placeholder="Search…" />

      <text>{() => "q = " + q()}</text>
    </column>
  </window>
);
