// P2-5 演示: 滚动容器 —— 滚轮滚动 + 视口裁剪 + 滚动条。
// 运行: go run . testdata/scroll_demo.js
// 现象: 20 行内容塞进 120px 高的视口, 只显示前几行, 右侧出现 8px 轨道 + 滑块;
//   把光标放在滚动区里滚轮向下滚, 内容上移, 滑块跟着变长/下移;
//   滚到边界后继续滚, 滚轮事件才冒泡给脚本的 onWheel (这里用来计数) ——
//   与 DOM 的滚动链一致: 先在滚动画布上消费, 到边界才继续往外传。
//   - 溢出的内容既画不出来也点不中 (裁剪与命中用同一个视口);
//   - 容器不给 height 时缺省 200; 内容不足一屏则不出滚动条。
import { createSignal } from "gx/solid";
import { h, window, render } from "gx/gfx";

const ROWS = 20;
const ROW_H = 36;

const [overscroll, setOverscroll] = createSignal(0);

const rows = [];
for (let i = 0; i < ROWS; i++) {
  rows.push(
    h(
      "rect",
      { height: ROW_H, background: i % 2 ? "#eef2f7" : "#ffffff" },
      h("text", { font: 13 }, `row ${i}`)
    )
  );
}

render(
  <column gap={8} padding={12}>
    <text font={16}>Scroll</text>

    <scroll width={240} height={120} onWheel={() => setOverscroll((n) => n + 1)}>
      {rows}
    </scroll>

    <text>{() => `overscroll events = ${overscroll()}`}</text>
  </column>,
  window({ title: "Scroll demo", width: 320, height: 240 })
);
