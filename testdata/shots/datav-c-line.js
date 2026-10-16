// 画廊截图: gx-datav 单组件 —— LineChart（多序列折线 (平滑 + 渐变面积)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { LineChart, palette, seriesRandomWalk } from "../datav/index.js";

const HOURS = Array.from({ length: 24 }, (_, i) => (i < 10 ? "0" + i : String(i)) + ":00");
const SERIES = [
  { name: "订单量", data: seriesRandomWalk(24, 120, 18, 7) },
  { name: "退单量", data: seriesRandomWalk(24, 30, 8, 11) },
];

render(
  <window title="gx-datav · LineChart" width={680} height={400} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <LineChart width={600} height={320} series={SERIES} labels={HOURS} yMax={220}/>
      </row>
    </column>
  </window>
);
