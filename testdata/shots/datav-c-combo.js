// 画廊截图: gx-datav 单组件 —— ComboChart（柱线组合图 (双 Y 轴)）
//
// 单组件图: 不套 BorderBox（边框会抢戏），组件直接居中在深空底上。
// 一律静态值 + 确定性种子 —— 截图要能复现。
import { render } from "gox";
import { ComboChart, palette, seriesRandomWalk } from "../datav/index.js";

const HOURS = ["00", "03", "06", "09", "12", "15", "18", "21", "24"];
const BARS = [{ name: "销量", data: seriesRandomWalk(9, 60, 22, 31) }];
const LINES = [{ name: "转化率", data: seriesRandomWalk(9, 42, 9, 32) }];

render(
  <window title="gx-datav · ComboChart" width={680} height={400} hideFocusRing={true}>
    <column width="100%" height="100%" background={palette.bg} padding={16}>
      <row flexGrow={1} alignItems="center" justifyContent="center">
        <ComboChart width={600} height={320} bars={BARS} lines={LINES} labels={HOURS}/>
      </row>
    </column>
  </window>
);
