// 画廊截图: gx-datav 单组件 —— ScatterChart（气泡散点图）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { ScatterChart, palette, makeRng } from "../datav/index.js";

function makeScatter(n, cx, cy, seed) {
  const rng = makeRng(seed);
  const out = [];
  for (let i = 0; i < n; i++) {
    out.push({
      x: Math.round(cx + (rng() - 0.5) * 60),
      y: Math.round(cy + (rng() - 0.5) * 50),
      r: 4 + Math.round(rng() * 20),
    });
  }
  return out;
}
const SCATTER = [
  { name: "一线", data: makeScatter(14, 70, 55, 21) },
  { name: "新一线", data: makeScatter(12, 45, 40, 22) },
];

render(
  <window title="gx-datav · ScatterChart" width={680} height={400} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <ScatterChart width={600} height={300} series={SCATTER} xLabel="GMV (千万)" yLabel="复购率" sizeRange={[6, 24]}/>
      </row>
    </column>
  </window>
);
