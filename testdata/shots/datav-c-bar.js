// 画廊截图: gx-datav 单组件 —— BarChart（柱状图 (彩虹柱 / 多序列)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { BarChart, palette, seriesRandomWalk } from "../datav/index.js";

const REGIONS = ["0h", "2h", "4h", "6h", "8h", "10h", "12h", "14h", "16h", "18h", "20h", "22h"];
const BARS = seriesRandomWalk(12, 60, 22, 5, 10, 120);

render(
  <window title="gx-datav · BarChart" width={680} height={380} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <BarChart width={600} height={280} data={BARS} labels={REGIONS}/>
      </row>
    </column>
  </window>
);
